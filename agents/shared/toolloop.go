package shared

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/providers"
)

// MaxConsecutiveToolErrors is the threshold of consecutive all-error tool-call
// turns before the tool loop aborts. Derived from the UpdateToolErrors contract:
// a single successful call resets the counter to 0, so this represents two
// complete turns where every call in the batch errored — strong evidence of a
// systematic rather than transient failure.
const MaxConsecutiveToolErrors = 2

// ToolCallSignature identifies a tool call by its name and serialized arguments.
type ToolCallSignature struct {
	Name      string
	Arguments string
}

// MarshalToolOutput converts arbitrary tool output to a string representation.
// Strings pass through directly, fmt.Stringer values use their String method,
// and all other types are JSON-marshaled.
func MarshalToolOutput(data any) (string, error) {
	switch typed := data.(type) {
	case string:
		return typed, nil
	case fmt.Stringer:
		return typed.String(), nil
	default:
		payload, err := json.Marshal(data)
		if err != nil {
			return "", fmt.Errorf("marshal tool output: %w", err)
		}
		return string(payload), nil
	}
}

// ToolErrorPayload returns a JSON-encoded error payload for a tool execution failure.
// Returns an empty string when err is nil.
func ToolErrorPayload(err error) string {
	if err == nil {
		return ""
	}
	payload, marshalErr := json.Marshal(map[string]any{
		"error": strings.TrimSpace(err.Error()),
	})
	if marshalErr != nil {
		return `{"error":"tool execution failed"}`
	}
	return string(payload)
}

// CoerceMap converts an arbitrary value to map[string]any via JSON round-trip.
// Returns nil if the value is nil, already a map[string]any (returned directly),
// or cannot be marshaled/unmarshaled.
func CoerceMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	if mapped, ok := value.(map[string]any); ok {
		return mapped
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var mapped map[string]any
	if err := json.Unmarshal(payload, &mapped); err != nil {
		return nil
	}
	return mapped
}

// DetectToolCallDuplicate checks whether every call in the batch has been seen
// before. It updates the seen map with new counts and returns true only when all
// calls in the batch are duplicates. The second return value is the first
// duplicate signature encountered (zero value if none).
func DetectToolCallDuplicate(calls []providers.ToolCall, seen map[ToolCallSignature]int) (bool, ToolCallSignature) {
	batch := make([]ToolCallSignature, 0, len(calls))
	for _, call := range calls {
		sig := ToolCallSignature{
			Name:      strings.TrimSpace(call.Name),
			Arguments: strings.TrimSpace(call.Arguments),
		}
		batch = append(batch, sig)
	}
	allDup := true
	var firstDup ToolCallSignature
	for _, sig := range batch {
		seen[sig]++
		if seen[sig] <= 1 {
			allDup = false
		} else if firstDup.Name == "" {
			firstDup = sig
		}
	}
	return allDup, firstDup
}

// UpdateToolErrors increments the consecutive-error counter when every call in a
// batch failed, and resets it to zero otherwise.
func UpdateToolErrors(current, errCount, totalCalls int) int {
	if totalCalls > 0 && errCount == totalCalls {
		return current + 1
	}
	return 0
}
