package guide

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// decodeClassificationResult tries each JSON candidate extracted from the raw
// model output and returns the first that unmarshals into a ClassificationResult.
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

// looksLikeCompleteJSONObject is a quick heuristic for checking whether a raw
// string starts with '{' and ends with '}' after trimming whitespace.
func looksLikeCompleteJSONObject(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	return strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")
}

// traceClassificationContext extracts trace-friendly classification context
// from the context.Context, if present.
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
