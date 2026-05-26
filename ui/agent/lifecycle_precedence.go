package agent

import (
	"strings"

	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/ui/msg"
)

type agentLifecycleSource int

const (
	agentLifecycleSourceClaims agentLifecycleSource = iota
	agentLifecycleSourceDemotion
)

type agentLifecycleUpdate struct {
	Source        agentLifecycleSource
	CorrelationID string
	Status        AgentStatus
	ActivityState events.AgentUIState
}

func applyAgentLifecycleUpdate(agent *AgentState, update agentLifecycleUpdate) bool {
	if agent == nil {
		return false
	}
	if !shouldApplyAgentLifecycleUpdate(agent, update) {
		return false
	}
	agent.Status = update.Status
	agent.ActivityState = normalizeLifecycleActivityState(agent, update.ActivityState)
	markAgentCorrelationActive(agent, update.CorrelationID)
	return true
}

func shouldApplyAgentLifecycleUpdate(agent *AgentState, update agentLifecycleUpdate) bool {
	if agent == nil {
		return false
	}
	if shouldIgnoreClosedAgentCorrelation(agent, update) {
		return false
	}
	if isActiveLifecycleUpdate(update) && shouldIgnoreStaleAgentCorrelation(agent, update.CorrelationID) {
		return false
	}
	switch update.Source {
	case agentLifecycleSourceClaims:
		return true
	case agentLifecycleSourceDemotion:
		return isActiveStatus(agent.Status)
	default:
		return false
	}
}

func normalizeLifecycleActivityState(agent *AgentState, state events.AgentUIState) events.AgentUIState {
	state = events.NormalizeAgentUIState(state)
	if state != events.AgentUIStateNone {
		return state
	}
	return defaultUIStateForStatus(agent)
}

func isTerminalLifecycleState(agent *AgentState) bool {
	if agent == nil {
		return false
	}
	if knowledgeAgentHasPendingReplicaLoad(agent) {
		return false
	}
	switch agent.Status {
	case StatusSuccess, StatusError, StatusWaiting:
		return true
	default:
		return isTerminalUIState(agent.ActivityState)
	}
}

func isActiveLifecycleUpdate(update agentLifecycleUpdate) bool {
	switch update.Source {
	case agentLifecycleSourceClaims:
		return isActiveStatus(update.Status)
	default:
		return false
	}
}

func shouldIgnoreClosedAgentCorrelation(agent *AgentState, update agentLifecycleUpdate) bool {
	if agent == nil {
		return false
	}
	correlationID := strings.TrimSpace(update.CorrelationID)
	if correlationID == "" {
		return false
	}
	if correlationID != strings.TrimSpace(agent.lastTerminalCorrelationID) {
		return false
	}
	if correlationID == strings.TrimSpace(agent.activeCorrelationID) {
		return false
	}
	switch update.Source {
	case agentLifecycleSourceClaims:
		return isActiveStatus(update.Status)
	default:
		return true
	}
}

func shouldIgnoreStaleAgentCorrelation(agent *AgentState, correlationID string) bool {
	if agent == nil {
		return false
	}
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return false
	}
	if correlationID != strings.TrimSpace(agent.lastTerminalCorrelationID) {
		return false
	}
	return correlationID != strings.TrimSpace(agent.activeCorrelationID)
}

func markAgentCorrelationActive(agent *AgentState, correlationID string) {
	if agent == nil {
		return
	}
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return
	}
	agent.activeCorrelationID = correlationID
	if strings.TrimSpace(agent.interruptCorrelationID) == "" {
		agent.interruptCorrelationID = correlationID
	}
}

func markAgentCorrelationTerminal(agent *AgentState, correlationID string) {
	if agent == nil {
		return
	}
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return
	}
	agent.lastTerminalCorrelationID = correlationID
	if strings.TrimSpace(agent.activeCorrelationID) == correlationID {
		agent.activeCorrelationID = ""
	}
	if strings.TrimSpace(agent.interruptCorrelationID) == correlationID {
		agent.interruptCorrelationID = ""
	}
}

func trackAgentActivityCorrelation(agent *AgentState, ev msg.ActivityEventMsg) {
	if agent == nil || ev.Event == nil {
		return
	}
	correlationID := strings.TrimSpace(ev.Event.CorrelationID)
	if correlationID == "" {
		return
	}
	if isTerminalLifecycleState(agent) {
		markAgentCorrelationTerminal(agent, correlationID)
		return
	}
	if isActiveStatus(agent.Status) {
		markAgentCorrelationActive(agent, correlationID)
	}
}
