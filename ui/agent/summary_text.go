package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/ui/msg"
)

func summarizeAgentPanelActivity(ev *events.ActivityEvent) string {
	if ev == nil {
		return ""
	}
	if text := summarizeAgentPanelPayload(ev.Summary); text != "" {
		if isNonInformativeLLMPanelText(ev.EventType, text) {
			return ""
		}
		return text
	}
	if text := summarizeAgentPanelData(ev.Data); text != "" {
		if isNonInformativeLLMPanelText(ev.EventType, text) {
			return ""
		}
		return text
	}
	if isNonInformativeLLMPanelText(ev.EventType, ev.Content) {
		return ""
	}
	if text := summarizeAgentPanelPayload(ev.Content); text != "" {
		return text
	}
	return ""
}

func summarizeAgentPanelToolPayload(raw string) string {
	return summarizeAgentPanelPayload(raw)
}

func summarizeAgentPanelToolEvent(event msg.ToolCallEventMsg) string {
	switch event.Phase {
	case 0:
		if text := summarizeAgentPanelInterAgentTool(event.InterAgent, false); text != "" {
			return text
		}
		if text := summarizeAgentPanelPayload(event.FullArgs); text != "" {
			return text
		}
		if text := normalizeAgentPanelText(event.ArgsSummary); text != "" {
			return text
		}
		return toolSummaryText(event.ToolName, event.ArgsSummary)
	case 1:
		if text := summarizeAgentPanelInterAgentTool(event.InterAgent, true); text != "" {
			return text
		}
		if !event.Success {
			if text := summarizeAgentPanelToolPayload(event.ErrorMsg); text != "" {
				return text
			}
		} else if text := summarizeAgentPanelToolPayload(event.Output); text != "" {
			return text
		}
		if text := summarizeAgentPanelPayload(event.FullArgs); text != "" {
			return text
		}
		if text := normalizeAgentPanelText(event.ArgsSummary); text != "" {
			return text
		}
		return toolSummaryText(event.ToolName, event.ArgsSummary)
	default:
		return ""
	}
}

func summarizeAgentPanelInterAgentTool(meta *msg.InterAgentToolEventMsg, completed bool) string {
	if meta == nil {
		return ""
	}
	summary := normalizeAgentPanelText(meta.Summary)
	if summary == "" {
		return ""
	}
	target := ""
	if len(meta.AgentTypes) > 0 {
		target = normalizeAgentPanelText(meta.AgentTypes[0])
	}
	switch strings.TrimSpace(meta.Kind) {
	case "consult":
		if target == "" {
			return summary
		}
		if completed {
			return humanizeAgentPanelKey(target) + ": " + summary
		}
		return "Consulting " + target + ": " + summary
	case "challenge":
		if target == "" {
			return summary
		}
		if completed {
			return humanizeAgentPanelKey(target) + ": " + summary
		}
		return "Challenging " + target + ": " + summary
	case "store":
		if target == "" {
			return summary
		}
		if completed {
			return humanizeAgentPanelKey(target) + ": " + summary
		}
		return "Storing with " + target + ": " + summary
	default:
		return summary
	}
}

type agentPanelPayloadSummary struct {
	parsed     any
	summary    string
	structured bool
	valid      bool
}

func summarizeAgentPanelPayload(raw string) string {
	return compactAgentPanelPayload(raw).summary
}

func compactAgentPanelPayload(raw string) agentPanelPayloadSummary {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return agentPanelPayloadSummary{}
	}
	if !looksLikeStructuredPayload(raw) {
		return agentPanelPayloadSummary{summary: normalizeAgentPanelText(raw)}
	}

	parsed, ok := decodeStructuredAgentPanelPayload(raw)
	if !ok {
		return agentPanelPayloadSummary{
			structured: true,
		}
	}

	result := agentPanelPayloadSummary{
		parsed:     parsed,
		structured: true,
		valid:      true,
	}
	if text := summarizeStructuredAgentPanelValue(parsed); text != "" {
		result.summary = normalizeAgentPanelText(text)
	}
	return result
}

func decodeStructuredAgentPanelPayload(raw string) (any, bool) {
	const maxStructuredPayloadDepth = 3

	current := strings.TrimSpace(raw)
	if current == "" || !looksLikeStructuredPayload(current) {
		return nil, false
	}

	for depth := 0; depth < maxStructuredPayloadDepth; depth++ {
		var parsed any
		if err := json.Unmarshal([]byte(current), &parsed); err != nil {
			return nil, false
		}
		nested, ok := parsed.(string)
		if !ok {
			return parsed, true
		}
		nested = strings.TrimSpace(nested)
		if !looksLikeStructuredPayload(nested) {
			return parsed, true
		}
		current = nested
	}

	return nil, false
}

func shouldApplyStructuredProgressSummary(existing string, payload agentPanelPayloadSummary) bool {
	existing = normalizeAgentPanelText(existing)
	if payload.summary == "" {
		return false
	}
	if !payload.structured {
		return true
	}
	if existing == "" {
		return true
	}
	if !payload.valid {
		return false
	}
	return isExplicitProgressStructuredSummary(payload.parsed)
}

func isExplicitProgressStructuredSummary(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		if dispatched, ok := typed["handoff_dispatched"].(bool); ok && dispatched {
			return true
		}
		if _, ok := typed["Criteria"]; ok {
			return true
		}
		if _, ok := typed["criteria"]; ok {
			return true
		}
		if found, ok := typed["found"].(bool); ok && !found {
			if count, ok := numericValue(typed["count"]); ok && count == 0 {
				return true
			}
		}
		for _, key := range []string{
			"summary",
			"user_message",
			"message",
			"content",
			"question",
			"query",
			"task_summary",
			"plan_summary",
			"acceptance_summary",
			"approach",
			"description",
			"decision",
			"action",
			"reason",
			"next_action",
			"status",
			"intent",
			"rationale",
			"output_file",
			"path",
			"file",
			"command",
		} {
			if text := summarizeStructuredAgentPanelValue(typed[key]); text != "" {
				return true
			}
		}
		return false
	case map[string]string:
		converted := make(map[string]any, len(typed))
		for key, value := range typed {
			converted[key] = value
		}
		return isExplicitProgressStructuredSummary(converted)
	default:
		return false
	}
}

func summarizeAgentPanelData(data map[string]any) string {
	if len(data) == 0 {
		return ""
	}
	for _, key := range []string{
		"summary",
		"description",
		"question",
		"query",
		"approach",
		"action",
		"decision",
		"message",
		"response",
		"request",
		"reason",
		"result",
		"output",
		"context",
		"error",
	} {
		if text := summarizeStructuredAgentPanelValue(data[key]); text != "" {
			return normalizeAgentPanelText(text)
		}
	}
	if toolName, ok := data["tool_name"].(string); ok && strings.TrimSpace(toolName) != "" {
		return "Tool: " + normalizeAgentPanelText(toolName)
	}
	if text := summarizeStructuredAgentPanelValue(data); text != "" {
		return normalizeAgentPanelText(text)
	}
	return ""
}

func summarizeStructuredAgentPanelValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		summary := compactAgentPanelPayload(typed)
		if summary.summary != "" {
			return summary.summary
		}
		if summary.structured {
			return ""
		}
		return normalizeAgentPanelText(typed)
	case []any:
		if len(typed) == 0 {
			return "No entries"
		}
		return fmt.Sprintf("%d items", len(typed))
	case map[string]any:
		if dispatched, ok := typed["handoff_dispatched"].(bool); ok && dispatched {
			return "Handoff dispatched"
		}
		if found, ok := typed["found"].(bool); ok && !found {
			if count, ok := numericValue(typed["count"]); ok && count == 0 {
				return "No results found"
			}
		}
		if _, ok := typed["Criteria"]; ok {
			return "Loaded validation criteria"
		}
		if _, ok := typed["criteria"]; ok {
			return "Loaded validation criteria"
		}
		for _, key := range []string{
			"summary",
			"user_message",
			"message",
			"content",
			"response",
			"question",
			"query",
			"task_summary",
			"plan_summary",
			"acceptance_summary",
			"approach",
			"description",
			"result",
			"output",
			"decision",
			"action",
			"reason",
			"request",
			"next_action",
			"status",
			"intent",
			"rationale",
			"output_file",
			"path",
			"file",
			"command",
		} {
			if text := summarizeStructuredAgentPanelValue(typed[key]); text != "" {
				return text
			}
		}
		return summarizeSimpleStructuredMap(typed)
	case map[string]string:
		converted := make(map[string]any, len(typed))
		for key, value := range typed {
			converted[key] = value
		}
		return summarizeStructuredAgentPanelValue(converted)
	case float64:
		if typed == float64(int64(typed)) {
			return fmt.Sprintf("%d", int64(typed))
		}
		return fmt.Sprintf("%g", typed)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

func summarizeSimpleStructuredMap(data map[string]any) string {
	parts := make([]string, 0, 3)
	for _, key := range []string{"status", "intent", "error"} {
		if text := summarizeStructuredAgentPanelValue(data[key]); text != "" {
			parts = append(parts, humanizeAgentPanelKey(key)+": "+text)
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, ", ")
	}
	return ""
}

func humanizeAgentPanelKey(key string) string {
	key = strings.TrimSpace(strings.ReplaceAll(key, "_", " "))
	if key == "" {
		return ""
	}
	return strings.ToUpper(key[:1]) + key[1:]
}

func normalizeAgentPanelText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", " ")
	text = strings.ReplaceAll(text, "\r", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "\t", " ")
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if strings.EqualFold(text, "null") {
		return ""
	}
	return text
}

func isNonInformativeLLMPanelText(eventType events.EventType, raw string) bool {
	text := normalizeAgentPanelText(raw)
	if text == "" {
		return false
	}
	lower := strings.ToLower(text)
	switch lower {
	case "active", "done", "pending", "idle":
		return true
	}
	switch eventType {
	case events.EventTypeLLMRequest:
		return text == "thinking" || text == "Streaming response started" || strings.HasPrefix(lower, "llm request to ")
	case events.EventTypeLLMResponse:
		return text == "Streaming response complete" || strings.HasPrefix(lower, "llm response from ")
	default:
		return false
	}
}

func looksLikeStructuredPayload(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	switch raw[0] {
	case '{', '[', '"':
		return true
	default:
		return false
	}
}

func numericValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case int32:
		return float64(typed), true
	default:
		return 0, false
	}
}
