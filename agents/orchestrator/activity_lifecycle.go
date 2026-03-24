package orchestrator

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/versioning"
)

func isPipelinePanelAgentType(agentType string) bool {
	agentType = strings.TrimSpace(agentType)
	for _, candidate := range PipelinePanelAgentTypes {
		if candidate == agentType {
			return true
		}
	}
	return false
}

func (o *Orchestrator) publishStandaloneAgentActivity(
	agentType,
	content string,
	visibility events.EventVisibility,
	data map[string]any,
) {
	if o == nil || o.activityPub == nil {
		return
	}
	agentType = strings.TrimSpace(agentType)
	content = strings.TrimSpace(content)
	if agentType == "" || content == "" || isPipelinePanelAgentType(agentType) {
		return
	}
	evt := events.NewActivityEvent(events.EventTypeAgentAction, firstNonEmpty(strings.TrimSpace(o.config.SessionID), orchestratorStateSessionID(o)), content)
	evt.AgentID = agentType
	evt.Visibility = visibility
	evt.Data["agent_type"] = agentType
	evt.Data["agent_name"] = agentshared.AgentDisplayName(agentType)
	for key, value := range data {
		evt.Data[key] = value
	}
	o.activityPub.PublishActivity(evt)
}

func (o *Orchestrator) publishTaskDraftMergeSuccess(task *TaskRecord, version versioning.SemanticVersion) {
	if o == nil || task == nil {
		return
	}
	label := strings.TrimSpace(firstNonEmpty(task.Name, task.ID))
	if label == "" {
		label = "task"
	}
	evt := events.NewActivityEvent(
		events.EventTypeSuccess,
		firstNonEmpty(strings.TrimSpace(task.SessionID), strings.TrimSpace(o.config.SessionID), orchestratorStateSessionID(o)),
		fmt.Sprintf("Operational transform merged %s into the global VFS at version %s.", label, version.String()),
	)
	evt.AgentID = o.config.AgentID
	evt.Visibility = events.VisibilityUser
	evt.Data["agent_type"] = "orchestrator"
	evt.Data["agent_name"] = "Orchestrator"
	evt.Data["source"] = "task_draft_merge"
	evt.Data["task_id"] = strings.TrimSpace(task.ID)
	if name := strings.TrimSpace(task.Name); name != "" {
		evt.Data["task_name"] = name
	}
	evt.Data["global_vfs_version"] = version.String()
	if o.activityPub != nil {
		o.activityPub.PublishActivity(evt)
	}
	o.publishUserVisibleSystemResponse(evt.Content)
}

func (o *Orchestrator) publishTaskDraftMergeFailure(task *TaskRecord, err error) {
	if o == nil || task == nil || err == nil {
		return
	}
	label := strings.TrimSpace(firstNonEmpty(task.Name, task.ID))
	if label == "" {
		label = "task"
	}
	evt := events.NewActivityEvent(
		events.EventTypeFailure,
		firstNonEmpty(strings.TrimSpace(task.SessionID), strings.TrimSpace(o.config.SessionID), orchestratorStateSessionID(o)),
		fmt.Sprintf("Operational transform failed while merging %s into the global VFS: %s", label, err.Error()),
	)
	evt.AgentID = o.config.AgentID
	evt.Visibility = events.VisibilityUser
	evt.Data["agent_type"] = "orchestrator"
	evt.Data["agent_name"] = "Orchestrator"
	evt.Data["source"] = "task_draft_merge"
	evt.Data["task_id"] = strings.TrimSpace(task.ID)
	if name := strings.TrimSpace(task.Name); name != "" {
		evt.Data["task_name"] = name
	}
	if o.activityPub != nil {
		o.activityPub.PublishActivity(evt)
	}
	o.publishUserVisibleSystemError(evt.Content)
}

func (o *Orchestrator) publishTaskDraftMergeStarted(task *TaskRecord) {
	if o == nil || task == nil {
		return
	}
	label := strings.TrimSpace(firstNonEmpty(task.Name, task.ID))
	if label == "" {
		label = "task"
	}
	o.publishUserVisibleSystemResponse(
		fmt.Sprintf("Operational transform received the accepted pipeline handoff for %s and is applying it to the global VFS.", label),
	)
}

func (o *Orchestrator) publishUserVisibleSystemResponse(content string) {
	o.publishUserVisibleSystemRouteResponse(content, "")
}

func (o *Orchestrator) publishUserVisibleSystemError(content string) {
	o.publishUserVisibleSystemRouteResponse("", content)
}

func (o *Orchestrator) publishUserVisibleSystemRouteResponse(content, errText string) {
	if o == nil || o.bus == nil {
		return
	}
	content = strings.TrimSpace(content)
	errText = strings.TrimSpace(errText)
	if content == "" && errText == "" {
		return
	}
	resp := &guide.RouteResponse{
		CorrelationID:       "system_" + generateMessageID(),
		Success:             errText == "",
		RespondingAgentID:   strings.TrimSpace(o.config.AgentID),
		RespondingAgentName: "Orchestrator",
		Data:                content,
		Error:               errText,
	}
	msg := guide.NewResponseMessage(generateMessageID(), resp)
	msg.TargetAgentID = "tui"
	if err := o.bus.Publish(guide.TopicResponses("tui", "tui"), msg); err != nil {
		o.logWarnMsg("publish user-visible orchestrator response", "error", err)
	}
}
