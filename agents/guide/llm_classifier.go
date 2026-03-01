package guide

import (
	"context"
	"fmt"
	"strings"
	"time"

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
		ReasoningEffort:    resolveClassifierReasoningEffort(c.config.ThinkingLevel),
		SkipProviderSkills: true,
	}

	completeStart := time.Now()
	guideFileLog().Info("DEBUG: classifier_complete_start",
		"model", c.model,
		"system_prompt_len", len(systemPrompt),
		"input_len", len(input),
		"skip_provider_skills", req.SkipProviderSkills,
		"reasoning_effort", req.ReasoningEffort)
	resp, err := c.provider.Complete(ctx, req)
	guideFileLog().Info("DEBUG: classifier_complete_done",
		"model", c.model,
		"elapsed_ms", time.Since(completeStart).Milliseconds(),
		"error", err,
		"response_len", classifierResponseLen(resp))
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

// Ensure LLMClassifier satisfies ClassifierService at compile time.
var _ ClassifierService = (*LLMClassifier)(nil)
