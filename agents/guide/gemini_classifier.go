package guide

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/adalundhe/sylk/core/providers"
)

// GeminiClassifier implements ClassifierService using Gemini's structured outputs.
type GeminiClassifier struct {
	provider    *providers.GoogleProvider
	config      RouterConfig
	corrections *correctionMemory
}

func NewGeminiClassifier(provider *providers.GoogleProvider, config RouterConfig) *GeminiClassifier {
	return &GeminiClassifier{
		provider:    provider,
		config:      config,
		corrections: newCorrectionMemory(config.MaxCorrections),
	}
}

// AddCorrection adds a correction for learning
func (c *GeminiClassifier) AddCorrection(correction CorrectionRecord) {
	if c.corrections == nil {
		return
	}
	c.corrections.add(correction)
}

func (c *GeminiClassifier) formatCorrections(input string) string {
	if c.corrections == nil {
		return ""
	}
	records := c.corrections.selectForPrompt(input, c.maxPromptCorrections())
	return formatCorrectionExamples(records)
}

func (c *GeminiClassifier) maxPromptCorrections() int {
	if c.config.MaxPromptCorrections > 0 {
		return c.config.MaxPromptCorrections
	}
	return defaultMaxPromptCorrections
}

// Classify classifies a natural language query using structured outputs
func (c *GeminiClassifier) Classify(ctx context.Context, input string) (*ClassificationResult, error) {
	geminiTrace("classifier", "classify_start", map[string]any{
		"model":              c.classificationModel(),
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

	textPart, err := c.streamClassificationJSON(ctx, systemPrompt, input)
	if err != nil {
		geminiTrace("classifier", "classify_stream_failed", map[string]any{
			"error":         err.Error(),
			"model":         c.classificationModel(),
			"input_preview": tracePreview(input, 160),
		})
		return nil, err
	}

	result, err := decodeClassificationResult(textPart)
	if err == nil {
		normalized := normalizeClassificationResult(result)
		geminiTrace("classifier", "classify_success_stream", map[string]any{
			"intent":      normalized.Intent,
			"domain":      normalized.Domain,
			"target":      normalized.TargetAgent,
			"confidence":  normalized.Confidence,
			"raw_preview": tracePreview(textPart, 260),
		})
		return normalized, nil
	}
	geminiTrace("classifier", "classify_stream_decode_failed", map[string]any{
		"error":           err.Error(),
		"candidate_count": len(classificationJSONCandidates(textPart)),
		"raw_preview":     tracePreview(textPart, 260),
	})

	fallbackText, fallbackErr := c.generateClassificationJSON(ctx, systemPrompt, input)
	if fallbackErr != nil {
		geminiTrace("classifier", "classify_fallback_failed", map[string]any{
			"stream_error":   err.Error(),
			"fallback_error": fallbackErr.Error(),
			"input_preview":  tracePreview(input, 160),
		})
		return nil, fmt.Errorf("failed to unmarshal structured output: %w (fallback failed: %v)", err, fallbackErr)
	}
	result, err = decodeClassificationResult(fallbackText)
	if err != nil {
		geminiTrace("classifier", "classify_fallback_decode_failed", map[string]any{
			"error":           err.Error(),
			"candidate_count": len(classificationJSONCandidates(fallbackText)),
			"raw_preview":     tracePreview(fallbackText, 260),
		})
		return nil, fmt.Errorf("failed to unmarshal structured output: %w", err)
	}
	normalized := normalizeClassificationResult(result)
	geminiTrace("classifier", "classify_success_fallback", map[string]any{
		"intent":      normalized.Intent,
		"domain":      normalized.Domain,
		"target":      normalized.TargetAgent,
		"confidence":  normalized.Confidence,
		"raw_preview": tracePreview(fallbackText, 260),
	})
	return normalized, nil
}

func decodeClassificationResult(text string) (*ClassificationResult, error) {
	var lastErr error
	for _, candidate := range classificationJSONCandidates(text) {
		var result ClassificationResult
		if err := json.Unmarshal([]byte(candidate), &result); err == nil {
			return &result, nil
		} else {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("empty structured output")
	}
	return nil, lastErr
}

func (c *GeminiClassifier) classificationModel() string {
	model := strings.TrimSpace(c.config.Model)
	if model == "" {
		return "gemini-3.1-pro-preview"
	}
	return model
}

func (c *GeminiClassifier) classificationTemperature() *float64 {
	if c.config.Temperature == 0 {
		return nil
	}
	value := c.config.Temperature
	return &value
}

func (c *GeminiClassifier) buildClassificationRequest(systemPrompt string, input string) *providers.Request {
	responseSchema := c.buildResponseSchemaMap()
	return &providers.Request{
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: input},
		},
		Model:            c.classificationModel(),
		SystemPrompt:     systemPrompt,
		Temperature:      c.classificationTemperature(),
		ReasoningEffort:  resolveClassifierReasoningEffort(c.config.ThinkingLevel),
		ResponseMIMEType: "application/json",
		ResponseSchema:   responseSchema,
		Tools: []providers.Tool{
			c.classificationTool(responseSchema),
		},
	}
}

// resolveClassifierReasoningEffort maps the guide's ThinkingLevel config to a
// provider ReasoningEffort string. Passes through the configured level directly.
func resolveClassifierReasoningEffort(thinkingLevel string) string {
	return strings.ToLower(strings.TrimSpace(thinkingLevel))
}

func (c *GeminiClassifier) classificationTool(responseSchema map[string]any) providers.Tool {
	return providers.Tool{
		Name:        ClassificationToolName,
		Description: "Classify the user request into intent/domain/target with confidence.",
		Parameters:  responseSchema,
	}
}

func (c *GeminiClassifier) streamClassificationJSON(
	ctx context.Context,
	systemPrompt string,
	input string,
) (string, error) {
	req := c.buildClassificationRequest(systemPrompt, input)
	var text strings.Builder
	var toolArgsDelta strings.Builder
	toolArgsLatest := ""
	chunkCounts := map[string]int{}
	toolCallIDs := map[string]struct{}{}
	err := c.provider.StreamWithHandler(ctx, req, func(chunk *providers.StreamChunk) error {
		chunkCounts[string(chunk.Type)]++
		if chunk.Type == providers.ChunkTypeText {
			text.WriteString(chunk.Text)
			return nil
		}
		if chunk.Type == providers.ChunkTypeToolDelta && chunk.ToolCall != nil {
			args := strings.TrimSpace(chunk.ToolCall.ArgumentsDelta)
			if args != "" {
				if id := strings.TrimSpace(chunk.ToolCall.ID); id != "" {
					toolCallIDs[id] = struct{}{}
				}
				toolArgsLatest = args
				if !looksLikeCompleteJSONObject(args) {
					toolArgsDelta.WriteString(args)
				}
			}
		}
		return nil
	})
	if err != nil {
		geminiTrace("classifier", "stream_request_error", map[string]any{
			"error":         err.Error(),
			"chunk_counts":  chunkCounts,
			"text_len":      text.Len(),
			"tool_args_len": len(toolArgsLatest),
			"tool_call_ids": traceSortedKeys(toolCallIDs),
		})
		return "", fmt.Errorf("gemini classification: %w", err)
	}
	trimmed := strings.TrimSpace(text.String())
	if trimmed != "" {
		geminiTrace("classifier", "stream_candidate_text", map[string]any{
			"chunk_counts": chunkCounts,
			"text_len":     len(trimmed),
			"text_preview": tracePreview(trimmed, 260),
		})
		return trimmed, nil
	}
	if toolArgsLatest == "" {
		toolArgsLatest = strings.TrimSpace(toolArgsDelta.String())
	}
	if toolArgsLatest != "" {
		geminiTrace("classifier", "stream_candidate_tool", map[string]any{
			"chunk_counts":  chunkCounts,
			"tool_args_len": len(toolArgsLatest),
			"tool_preview":  tracePreview(toolArgsLatest, 260),
			"tool_call_ids": traceSortedKeys(toolCallIDs),
		})
		return toolArgsLatest, nil
	}
	geminiTrace("classifier", "stream_no_candidates", map[string]any{
		"chunk_counts":   chunkCounts,
		"text_len":       len(trimmed),
		"tool_delta_len": toolArgsDelta.Len(),
		"tool_call_ids":  traceSortedKeys(toolCallIDs),
	})
	return "", fmt.Errorf("no response candidates from gemini")
}

func (c *GeminiClassifier) generateClassificationJSON(
	ctx context.Context,
	systemPrompt string,
	input string,
) (string, error) {
	req := c.buildClassificationRequest(systemPrompt, input)
	resp, err := c.provider.Complete(ctx, req)
	if err != nil {
		geminiTrace("classifier", "fallback_request_error", map[string]any{
			"error": err.Error(),
			"model": c.classificationModel(),
		})
		return "", fmt.Errorf("gemini classification fallback: %w", err)
	}
	text := strings.TrimSpace(resp.Content)
	if text != "" {
		geminiTrace("classifier", "fallback_candidate_text", map[string]any{
			"text_len":     len(text),
			"text_preview": tracePreview(text, 260),
			"tool_calls":   len(resp.ToolCalls),
		})
		return text, nil
	}
	for _, call := range resp.ToolCalls {
		args := strings.TrimSpace(call.Arguments)
		if args != "" {
			geminiTrace("classifier", "fallback_candidate_tool", map[string]any{
				"tool_id":       call.ID,
				"tool_name":     call.Name,
				"tool_args_len": len(args),
				"tool_preview":  tracePreview(args, 260),
			})
			return args, nil
		}
	}
	geminiTrace("classifier", "fallback_no_candidates", map[string]any{
		"tool_calls": len(resp.ToolCalls),
		"metadata":   resp.ProviderMetadata,
	})
	return "", fmt.Errorf("no response candidates from gemini fallback")
}

func traceClassificationContext(ctx context.Context) map[string]any {
	value, ok := classificationContextFromContext(ctx)
	if !ok {
		return nil
	}
	return map[string]any{
		"session_id":                value.SessionID,
		"active_conversation_agent": value.ActiveConversationAgent,
		"active_conversation_turns": value.ActiveConversationTurns,
		"active_conversation_age":   value.ActiveConversationAge,
		"active_conversation_score": value.ActiveConversationScore,
	}
}

func looksLikeCompleteJSONObject(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	return strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")
}

func traceSortedKeys(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func (c *GeminiClassifier) buildResponseSchemaMap() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"is_retrospective": map[string]any{
				"type":        "boolean",
				"description": "True if query is about PAST actions, observations, or learnings. False if about FUTURE needs, plans, or requirements.",
			},
			"rejection_reason": map[string]any{
				"type":        "string",
				"description": "If not retrospective and target is archivalist, explain why the query cannot be handled",
			},
			"intent": map[string]any{
				"type":        "string",
				"enum":        []string{"recall", "store", "check", "declare", "complete", "find", "search", "locate", "plan", "design", "help", "status", "chat", "unknown"},
				"description": "The classified intent of the query",
			},
			"domain": map[string]any{
				"type":        "string",
				"enum":        []string{"local", "history", "research", "planning", "system", "compliance", "testing", "general", "unknown"},
				"description": "The domain/category of the query",
			},
			"target_agent": map[string]any{
				"type":        "string",
				"enum":        []string{"librarian", "engineer", "designer", "tester", "inspector", "archivalist", "academic", "orchestrator", "architect", "guide", "unknown"},
				"description": "Which agent should handle this query",
			},
			"entities": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scope":         map[string]any{"type": "string", "description": "Area/component being queried"},
					"timeframe":     map[string]any{"type": "string", "description": "Time reference if any"},
					"agent_id":      map[string]any{"type": "string", "description": "Specific agent ID if mentioned"},
					"agent_name":    map[string]any{"type": "string", "description": "Specific agent name if mentioned"},
					"file_paths":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "File paths mentioned"},
					"error_type":    map[string]any{"type": "string", "description": "Type of error if failure-related"},
					"error_message": map[string]any{"type": "string", "description": "Error message if provided"},
					"query":         map[string]any{"type": "string", "description": "Free-form query text for context searches"},
				},
			},
			"confidence": map[string]any{
				"type":        "number",
				"description": "Classification confidence from 0.0 to 1.0",
			},
			"rejected": map[string]any{
				"type":        "boolean",
				"description": "True when the request is too ambiguous to safely route",
			},
			"reason": map[string]any{
				"type":        "string",
				"description": "Clarifying question or rejection reason when rejected=true",
			},
			"multi_intent": map[string]any{
				"type":        "boolean",
				"description": "True if the query contains multiple intents",
			},
			"sub_results": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"is_retrospective": map[string]any{"type": "boolean"},
						"intent":           map[string]any{"type": "string", "enum": []string{"recall", "store", "check", "declare", "complete", "find", "search", "locate", "plan", "design", "help", "status", "chat", "unknown"}},
						"domain":           map[string]any{"type": "string", "enum": []string{"local", "history", "research", "planning", "system", "compliance", "testing", "general", "unknown"}},
						"target_agent":     map[string]any{"type": "string", "enum": []string{"librarian", "engineer", "designer", "tester", "inspector", "archivalist", "academic", "orchestrator", "architect", "guide", "unknown"}},
						"confidence":       map[string]any{"type": "number"},
					},
					"required": []string{"is_retrospective", "intent", "domain", "target_agent", "confidence"},
				},
				"description": "Sub-intents if multi_intent is true",
			},
		},
		"required": []string{"is_retrospective", "intent", "domain", "target_agent", "confidence"},
	}
}

// Ensure GeminiClassifier implements ClassifierService
var _ ClassifierService = (*GeminiClassifier)(nil)
