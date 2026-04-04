package bridge

import (
	"strings"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/ui/agentidentity"
)

func streamRuntimeAgentID(stream *guide.StreamResponse) string {
	if stream == nil {
		return ""
	}
	return agentidentity.RuntimeAgentID(
		strings.TrimSpace(stream.RespondingAgentID),
		streamMetadataString(stream, "runtime_agent_id"),
	)
}

func normalizeActivityEventForUI(event *events.ActivityEvent) *events.ActivityEvent {
	if event == nil {
		return nil
	}
	cloned := *event
	if event.Data != nil {
		cloned.Data = make(map[string]any, len(event.Data)+2)
		for key, value := range event.Data {
			cloned.Data[key] = value
		}
	}
	if cloned.Data == nil {
		cloned.Data = make(map[string]any, 2)
	}
	rawAgentID := strings.TrimSpace(event.AgentID)
	visibleAgentID := agentidentity.VisibleAgentID(
		rawAgentID,
		activityMetadataString(cloned.Data, "canonical_agent_id"),
		activityMetadataString(cloned.Data, "agent_type"),
		activityMetadataString(cloned.Data, "pipeline_id"),
		activityMetadataString(cloned.Data, "task_id"),
	)
	runtimeAgentID := agentidentity.RuntimeAgentID(rawAgentID, activityMetadataString(cloned.Data, "runtime_agent_id"))
	if runtimeAgentID != "" && runtimeAgentID != visibleAgentID && activityMetadataString(cloned.Data, "runtime_agent_id") == "" {
		cloned.Data["runtime_agent_id"] = runtimeAgentID
	}
	if visibleAgentID != "" {
		cloned.AgentID = visibleAgentID
		cloned.Data["canonical_agent_id"] = visibleAgentID
	}
	return &cloned
}

func activityMetadataString(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	value, ok := data[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}
