package shared

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// SummarizeInterAgentPayload extracts a short human-facing summary from a
// routed inter-agent payload. Typed payloads (implementers of
// InterAgentResponsePayload) short-circuit to their declared Summary
// field, bypassing all the defensive JSON / map inspection below. The
// fallback path is retained for untyped data from agents that have not
// yet migrated to typed payloads; those paths are scheduled for deletion
// once all producers emit typed responses.
func SummarizeInterAgentPayload(payload any) string {
	if summary := InterAgentPayloadSummary(payload); summary != "" {
		return summary
	}
	return summarizeInterAgentPayload(payload, 0)
}

func summarizeInterAgentPayload(payload any, depth int) string {
	if depth > 6 || payload == nil {
		return ""
	}
	// Second typed check: pointers unwrapped from a map[string]any after
	// re-marshal/unmarshal cycles (bus-serialized paths) reach us here as
	// map[string]any even when the producer emitted a typed struct. The
	// typed union check at the top handles the in-process path; this
	// preserves correct summary extraction for the serialized path once
	// all producers emit typed structs (the JSON keys will match the
	// struct's `summary` tag).

	switch typed := payload.(type) {
	case string:
		return summarizeInterAgentString(typed, depth)
	case []byte:
		return summarizeInterAgentString(string(typed), depth)
	case map[string]any:
		return summarizeInterAgentMap(typed, depth)
	case []string:
		return summarizeInterAgentStrings(typed)
	case []any:
		return summarizeInterAgentSlice(typed, depth)
	default:
		if values, ok := summarizeInterAgentToMap(typed); ok {
			return summarizeInterAgentMap(values, depth)
		}
		return normalizeInlineString(fmt.Sprint(typed))
	}
}

func summarizeInterAgentString(raw string, depth int) string {
	trimmed := normalizeInlineString(raw)
	if trimmed == "" {
		return ""
	}

	var values map[string]any
	if err := json.Unmarshal([]byte(raw), &values); err == nil {
		if summary := summarizeInterAgentMap(values, depth+1); summary != "" {
			return summary
		}
	}

	var items []any
	if err := json.Unmarshal([]byte(raw), &items); err == nil {
		if summary := summarizeInterAgentSlice(items, depth+1); summary != "" {
			return summary
		}
	}

	return trimmed
}

func summarizeInterAgentMap(values map[string]any, depth int) string {
	if len(values) == 0 {
		return ""
	}

	for _, key := range []string{"user_message", "response", "message", "summary", "description", "answer", "content"} {
		if summary := summarizeInterAgentMapField(values, key, depth); summary != "" {
			return summary
		}
	}

	for _, key := range []string{"data", "Data", "result", "Result", "evaluation", "grant", "payload"} {
		if summary := summarizeInterAgentMapField(values, key, depth); summary != "" {
			return summary
		}
	}

	if evidence, ok := values["evidence"]; ok {
		if summary := summarizeInterAgentPayload(evidence, depth+1); summary != "" {
			return summary
		}
	}

	for _, key := range []string{"reason", "decision", "status"} {
		if summary := summarizeInterAgentMapField(values, key, depth); summary != "" {
			return summary
		}
	}

	if summary := summarizeInterAgentState(values); summary != "" {
		return summary
	}

	return summarizeInterAgentScalarMap(values)
}

func summarizeInterAgentMapField(values map[string]any, key string, depth int) string {
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	return summarizeInterAgentPayload(value, depth+1)
}

func summarizeInterAgentSlice(items []any, depth int) string {
	if len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, minInt(len(items), 3))
	seen := make(map[string]struct{}, minInt(len(items), 3))
	for _, item := range items {
		summary := summarizeInterAgentPayload(item, depth+1)
		if summary == "" {
			continue
		}
		key := strings.ToLower(summary)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		parts = append(parts, summary)
		if len(parts) == 3 {
			break
		}
	}
	return normalizeInlineString(strings.Join(parts, "; "))
}

func summarizeInterAgentStrings(items []string) string {
	if len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, minInt(len(items), 3))
	seen := make(map[string]struct{}, minInt(len(items), 3))
	for _, item := range items {
		item = normalizeInlineString(item)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		parts = append(parts, item)
		if len(parts) == 3 {
			break
		}
	}
	return strings.Join(parts, "; ")
}

func summarizeInterAgentState(values map[string]any) string {
	if approved, ok := summarizeInterAgentBool(values["approved"]); ok {
		if approved {
			return "approved"
		}
		return "denied"
	}
	if valid, ok := summarizeInterAgentBool(values["valid"]); ok {
		if valid {
			return "valid"
		}
		return "invalid"
	}
	if clean, ok := summarizeInterAgentBool(values["clean"]); ok {
		if clean {
			return "clean"
		}
		return "issues found"
	}
	if ready, ok := summarizeInterAgentBool(values["ready"]); ok {
		if ready {
			return "ready"
		}
	}
	return ""
}

func summarizeInterAgentScalarMap(values map[string]any) string {
	lines := make([]string, 0, len(values))
	for key, value := range values {
		scalar, ok := summarizeInterAgentScalar(value)
		if !ok {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s: %s", key, scalar))
	}
	if len(lines) == 0 {
		return ""
	}
	sort.Strings(lines)
	if len(lines) > 3 {
		lines = lines[:3]
	}
	return strings.Join(lines, ", ")
}

func summarizeInterAgentScalar(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		text := normalizeInlineString(typed)
		return text, text != ""
	case bool:
		if typed {
			return "true", true
		}
		return "false", true
	case float64:
		return strings.TrimSpace(fmt.Sprintf("%v", typed)), true
	case float32:
		return strings.TrimSpace(fmt.Sprintf("%v", typed)), true
	case int:
		return fmt.Sprintf("%d", typed), true
	case int32:
		return fmt.Sprintf("%d", typed), true
	case int64:
		return fmt.Sprintf("%d", typed), true
	case uint:
		return fmt.Sprintf("%d", typed), true
	case uint32:
		return fmt.Sprintf("%d", typed), true
	case uint64:
		return fmt.Sprintf("%d", typed), true
	default:
		return "", false
	}
}

func summarizeInterAgentBool(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true":
			return true, true
		case "false":
			return false, true
		default:
			return false, false
		}
	default:
		return false, false
	}
}

func summarizeInterAgentToMap(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case nil:
		return nil, false
	case map[string]any:
		return typed, true
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, false
	}
	return decoded, true
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
