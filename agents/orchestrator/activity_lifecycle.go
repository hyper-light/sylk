package orchestrator

import (
	"fmt"
	"strings"

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
	evt := events.NewActivityEvent(events.EventTypeAgentAction, o.config.SessionID, content)
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
	if o == nil || o.activityPub == nil || task == nil {
		return
	}
	label := strings.TrimSpace(firstNonEmpty(task.Name, task.ID))
	if label == "" {
		label = "task"
	}
	evt := events.NewActivityEvent(
		events.EventTypeSuccess,
		o.config.SessionID,
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
	evt.Data["chat_visible"] = true
	o.activityPub.PublishActivity(evt)
}

func (o *Orchestrator) publishTaskDraftMergeFailure(task *TaskRecord, err error) {
	if o == nil || o.activityPub == nil || task == nil || err == nil {
		return
	}
	label := strings.TrimSpace(firstNonEmpty(task.Name, task.ID))
	if label == "" {
		label = "task"
	}
	evt := events.NewActivityEvent(
		events.EventTypeFailure,
		o.config.SessionID,
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
	o.activityPub.PublishActivity(evt)
}
