package events

import "strings"

// AgentUIState is a UI-facing activity state for richer per-agent badges.
// It complements, rather than replaces, the broader ActivityEvent type and
// the agent panel's coarse operational status.
type AgentUIState string

const (
	AgentUIStateNone           AgentUIState = ""
	AgentUIStateThinking       AgentUIState = "thinking"
	AgentUIStateResponding     AgentUIState = "responding"
	AgentUIStateWriting        AgentUIState = "writing"
	AgentUIStateRunning        AgentUIState = "running"
	AgentUIStateSearching      AgentUIState = "searching"
	AgentUIStateAuditing       AgentUIState = "auditing"
	AgentUIStatePassed         AgentUIState = "passed"
	AgentUIStateFailed         AgentUIState = "failed"
	AgentUIStateValidating     AgentUIState = "validating"
	AgentUIStateInspecting     AgentUIState = "inspecting"
	AgentUIStateBlocked        AgentUIState = "blocked"
	AgentUIStateAllowed        AgentUIState = "allowed"
	AgentUIStateRouting        AgentUIState = "routing"
	AgentUIStateClassifying    AgentUIState = "classifying"
	AgentUIStatePlanning       AgentUIState = "planning"
	AgentUIStatePlanAccepted   AgentUIState = "plan_accepted"
	AgentUIStatePlanFailed     AgentUIState = "plan_failed"
	AgentUIStatePlanRejected   AgentUIState = "plan_rejected"
	AgentUIStateTestingPending AgentUIState = "testing_pending"
	AgentUIStateTestingRunning AgentUIState = "testing_running"
	AgentUIStateTestingFailed  AgentUIState = "testing_failed"
)

func (s AgentUIState) String() string {
	return string(s)
}

// NormalizeAgentUIState canonicalizes a UI state string to a supported value.
func NormalizeAgentUIState(state AgentUIState) AgentUIState {
	switch strings.TrimSpace(strings.ToLower(state.String())) {
	case "":
		return AgentUIStateNone
	case AgentUIStateThinking.String():
		return AgentUIStateThinking
	case AgentUIStateResponding.String():
		return AgentUIStateResponding
	case AgentUIStateWriting.String():
		return AgentUIStateWriting
	case AgentUIStateRunning.String():
		return AgentUIStateRunning
	case AgentUIStateSearching.String():
		return AgentUIStateSearching
	case AgentUIStateAuditing.String():
		return AgentUIStateAuditing
	case AgentUIStatePassed.String():
		return AgentUIStatePassed
	case AgentUIStateFailed.String():
		return AgentUIStateFailed
	case AgentUIStateValidating.String():
		return AgentUIStateValidating
	case AgentUIStateInspecting.String():
		return AgentUIStateInspecting
	case AgentUIStateBlocked.String():
		return AgentUIStateBlocked
	case AgentUIStateAllowed.String():
		return AgentUIStateAllowed
	case AgentUIStateRouting.String():
		return AgentUIStateRouting
	case AgentUIStateClassifying.String():
		return AgentUIStateClassifying
	case AgentUIStatePlanning.String():
		return AgentUIStatePlanning
	case AgentUIStatePlanAccepted.String():
		return AgentUIStatePlanAccepted
	case AgentUIStatePlanFailed.String():
		return AgentUIStatePlanFailed
	case AgentUIStatePlanRejected.String():
		return AgentUIStatePlanRejected
	case AgentUIStateTestingPending.String():
		return AgentUIStateTestingPending
	case AgentUIStateTestingRunning.String():
		return AgentUIStateTestingRunning
	case AgentUIStateTestingFailed.String():
		return AgentUIStateTestingFailed
	default:
		return AgentUIStateNone
	}
}

// AgentUIStateFromData extracts a UI state from an activity event data map.
func AgentUIStateFromData(data map[string]any) AgentUIState {
	if len(data) == 0 {
		return AgentUIStateNone
	}
	switch typed := data["ui_state"].(type) {
	case AgentUIState:
		return NormalizeAgentUIState(typed)
	case string:
		return NormalizeAgentUIState(AgentUIState(typed))
	default:
		return AgentUIStateNone
	}
}

// SetAgentUIState records a UI state in the activity event data payload.
func SetAgentUIState(evt *ActivityEvent, state AgentUIState) {
	if evt == nil {
		return
	}
	state = NormalizeAgentUIState(state)
	if state == AgentUIStateNone {
		return
	}
	if evt.Data == nil {
		evt.Data = make(map[string]any)
	}
	evt.Data["ui_state"] = state.String()
}
