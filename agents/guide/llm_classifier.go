package guide

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
)

// LLMClassifier implements ClassifierService using any providers.ProviderAdapter.
// It relies on Complete() (supported by all providers) and extracts JSON from
// free-text output via decodeClassificationResult — no ResponseSchema or
// ResponseMIMEType (Gemini-specific structured output fields) needed.
type LLMClassifier struct {
	provider    providers.ProviderAdapter
	model       string
	config      RouterConfig
	corrections *correctionMemory
	eventLogger *agentlog.SessionEventLogger
}

// NewLLMClassifier creates a provider-agnostic LLM classifier.
func NewLLMClassifier(provider providers.ProviderAdapter, model string, config RouterConfig) *LLMClassifier {
	return &LLMClassifier{
		provider:    provider,
		model:       resolveClassifierModel(model, config.Model),
		config:      config,
		corrections: newCorrectionMemory(config.MaxCorrections),
	}
}

// AddCorrection records a routing correction for future classification hints.
func (c *LLMClassifier) AddCorrection(correction CorrectionRecord) {
	if c.corrections == nil {
		return
	}
	c.corrections.add(correction)
}

// Classify sends the input to the provider via Complete() and parses the
// resulting JSON classification.
func (c *LLMClassifier) Classify(ctx context.Context, input string) (*ClassificationResult, error) {
	geminiTrace("classifier", "classify_start", map[string]any{
		"model":              c.model,
		"input_len":          len(input),
		"input_preview":      tracePreview(input, 220),
		"runtime_context":    traceClassificationContext(ctx),
		"has_provider":       c.provider != nil,
		"classifier_timeout": c.config.ClassificationTimeout.String(),
	})

	systemPrompt := FormatClassificationPromptWithRuntime(
		c.formatCorrections(input),
		classificationPromptRuntimeFromContext(ctx),
	)

	if c.config.ClassificationTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.config.ClassificationTimeout)
		defer cancel()
	}

	req := &providers.Request{
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: input},
		},
		Model:              c.model,
		SystemPrompt:       systemPrompt,
		Temperature:        classificationTemperature(c.config),
		SkipProviderSkills: true,
	}
	llmruntime.ApplyStage(req, guideClassifierStageProfile(c.config.ThinkingLevel), llmruntime.ApplyOptions{
		Model:     guideRuntimeModel(req, c.model),
		MaxTokens: req.MaxTokens,
		AgentID:   "guide",
		SessionID: "",
	})

	logClassifierLLMCallStarted(c.eventLogger, req)
	completeStart := time.Now()
	guideFileLog().Info("DEBUG: classifier_complete_start",
		"model", c.model,
		"system_prompt_len", len(systemPrompt),
		"input_len", len(input),
		"skip_provider_skills", req.SkipProviderSkills,
		"reasoning_effort", req.ReasoningEffort)
	resp, err := c.provider.Complete(ctx, req)
	classifyDur := time.Since(completeStart)
	guideFileLog().Info("DEBUG: classifier_complete_done",
		"model", c.model,
		"elapsed_ms", classifyDur.Milliseconds(),
		"error", err,
		"response_len", classifierResponseLen(resp))
	logClassifierLLMCall(c.eventLogger, c.model, resp, classifyDur, err)
	if err != nil {
		geminiTrace("classifier", "classify_complete_failed", map[string]any{
			"error":         err.Error(),
			"model":         c.model,
			"input_preview": tracePreview(input, 160),
		})
		return nil, fmt.Errorf("llm classification: %w", err)
	}

	text := strings.TrimSpace(resp.Content)
	if text == "" {
		geminiTrace("classifier", "classify_empty_response", map[string]any{
			"model":         c.model,
			"input_preview": tracePreview(input, 160),
			"tool_calls":    len(resp.ToolCalls),
		})
		return nil, fmt.Errorf("llm classification: empty response from %s", c.model)
	}

	result, err := decodeClassificationResult(text)
	if err != nil {
		geminiTrace("classifier", "classify_decode_failed", map[string]any{
			"error":           err.Error(),
			"candidate_count": len(classificationJSONCandidates(text)),
			"raw_preview":     tracePreview(text, 260),
		})
		return nil, fmt.Errorf("llm classification decode: %w", err)
	}

	normalized := normalizeClassificationResult(result)
	geminiTrace("classifier", "classify_success", map[string]any{
		"intent":      normalized.Intent,
		"domain":      normalized.Domain,
		"target":      normalized.TargetAgent,
		"confidence":  normalized.Confidence,
		"raw_preview": tracePreview(text, 260),
	})
	return normalized, nil
}

func (c *LLMClassifier) formatCorrections(input string) string {
	if c.corrections == nil {
		return ""
	}
	records := c.corrections.selectForPrompt(input, c.maxPromptCorrections())
	return formatCorrectionExamples(records)
}

func (c *LLMClassifier) maxPromptCorrections() int {
	if c.config.MaxPromptCorrections > 0 {
		return c.config.MaxPromptCorrections
	}
	return defaultMaxPromptCorrections
}

// classificationTemperature returns a temperature pointer from the config,
// or nil if zero (letting the provider choose its default).
func classificationTemperature(cfg RouterConfig) *float64 {
	if cfg.Temperature == 0 {
		return nil
	}
	value := cfg.Temperature
	return &value
}

// resolveClassifierModel returns the first non-empty model string.
func resolveClassifierModel(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// resolveClassifierReasoningEffort maps the guide's ThinkingLevel config to a
// provider ReasoningEffort string. Passes through the configured level directly.
func resolveClassifierReasoningEffort(thinkingLevel string) string {
	return strings.ToLower(strings.TrimSpace(thinkingLevel))
}

func classifierResponseLen(resp *providers.Response) int {
	if resp == nil {
		return 0
	}
	return len(resp.Content)
}

// logClassifierLLMCallStarted records the dispatch of an LLM call from the
// Guide's classifier or self-response, paired with logClassifierLLMCall at
// completion. Captures model, message count, and tools-offered count so
// hung calls remain observable in the WAL until the response (or an error)
// arrives.
func logClassifierLLMCallStarted(el *agentlog.SessionEventLogger, req *providers.Request) {
	if el == nil || req == nil {
		return
	}
	el.LogWALJSON(agentlog.EventLLMRequestSent, &agentlog.LLMPayload{
		Model: req.Model,
	})
	el.LogEvent(agentlog.JSONLEntry{
		Timestamp: time.Now(),
		Level:     "info",
		Agent:     "guide",
		Event:     "llm_call_started",
		EventCode: agentlog.EventLLMRequestSent,
		Data: map[string]any{
			"model":          req.Model,
			"messages_count": len(req.Messages),
			"tools_offered":  len(req.Tools),
		},
	})
}

// logClassifierLLMCall records an LLM call from the Guide's classifier.
func logClassifierLLMCall(el *agentlog.SessionEventLogger, model string, resp *providers.Response, dur time.Duration, err error) {
	if el == nil {
		return
	}
	inTok, outTok := 0, 0
	if resp != nil {
		inTok, outTok = resp.Usage.InputTokens, resp.Usage.OutputTokens
	}

	eventType := agentlog.EventLLMResponseReceived
	if err != nil {
		eventType = agentlog.EventLLMError
	}

	el.LogWALJSON(eventType, &agentlog.LLMPayload{
		Model:        model,
		InputTokens:  inTok,
		OutputTokens: outTok,
		DurationNs:   dur.Nanoseconds(),
	})

	data := map[string]any{
		"model":         model,
		"input_tokens":  inTok,
		"output_tokens": outTok,
	}
	if resp != nil && resp.Content != "" {
		data["response_preview"] = truncateLogStr(resp.Content, 512)
	}

	entry := agentlog.JSONLEntry{
		Timestamp:  time.Now(),
		Level:      "info",
		Agent:      "guide",
		Event:      "llm_call",
		EventCode:  eventType,
		DurationNs: dur.Nanoseconds(),
		Data:       data,
	}
	if err != nil {
		entry.Level = "error"
		entry.Event = "llm_error"
		entry.Error = err.Error()
	}
	el.LogEvent(entry)
}

// Ensure LLMClassifier satisfies ClassifierService at compile time.
var _ ClassifierService = (*LLMClassifier)(nil)
