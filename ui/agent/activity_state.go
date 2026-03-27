package agent

import (
	"strings"

	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/ui/msg"
)

func inferUIStateFromActivity(agent *AgentState, ev msg.ActivityEventMsg) events.AgentUIState {
	if agent == nil || ev.Event == nil {
		return events.AgentUIStateNone
	}
	if explicit := events.AgentUIStateFromData(ev.Event.Data); explicit != events.AgentUIStateNone {
		return explicit
	}

	content := strings.ToLower(strings.TrimSpace(ev.Event.Content))
	switch agent.AgentType {
	case "guide":
		switch {
		case ev.Event.EventType == events.EventTypeAgentDecision:
			return events.AgentUIStateClassifying
		case strings.Contains(content, "routing to "), strings.Contains(content, "request forwarded"):
			return events.AgentUIStateRouting
		}
	case "architect":
		switch ev.Event.EventType {
		case events.EventTypeAgentError, events.EventTypeFailure:
			if strings.Contains(content, "rejected") {
				return events.AgentUIStatePlanRejected
			}
			return events.AgentUIStatePlanFailed
		case events.EventTypeSuccess:
			if strings.Contains(content, "accepted") {
				return events.AgentUIStatePlanAccepted
			}
		}
	case "guardian":
		switch {
		case strings.Contains(content, "allowed"), strings.Contains(content, "approved"):
			return events.AgentUIStateAllowed
		case strings.Contains(content, "blocked"), strings.Contains(content, "denied"):
			return events.AgentUIStateBlocked
		case strings.Contains(content, "inspect"), strings.Contains(content, "scan"), strings.Contains(content, "quarantine"):
			return events.AgentUIStateInspecting
		}
	}

	return defaultUIStateForStatus(agent)
}

func inferUIStateFromStreamProgress(agent *AgentState, progress msg.StreamProgressMsg) events.AgentUIState {
	if agent == nil {
		return events.AgentUIStateNone
	}
	if explicit := events.NormalizeAgentUIState(progress.UIState); explicit != events.AgentUIStateNone {
		return explicit
	}
	return defaultProgressUIState(agent)
}

func defaultProgressUIState(agent *AgentState) events.AgentUIState {
	if agent == nil {
		return events.AgentUIStateNone
	}
	switch agent.AgentType {
	case "architect":
		return events.AgentUIStatePlanning
	case "guide":
		return events.AgentUIStateThinking
	case "orchestrator":
		return events.AgentUIStateThinking
	case "inspector", "inspector-pipeline":
		return events.AgentUIStateAuditing
	case "tester", "tester-pipeline":
		return events.AgentUIStateTestingPending
	case "librarian", "academic", "archivalist":
		return events.AgentUIStateSearching
	default:
		return events.AgentUIStateThinking
	}
}

func defaultUIStateForStatus(agent *AgentState) events.AgentUIState {
	if agent == nil {
		return events.AgentUIStateNone
	}
	switch agent.Status {
	case StatusThinking:
		return defaultProgressUIState(agent)
	case StatusActing:
		switch agent.AgentType {
		case "architect":
			return events.AgentUIStatePlanning
		case "guide":
			return events.AgentUIStateRouting
		case "orchestrator":
			return events.AgentUIStateRunning
		case "inspector", "inspector-pipeline":
			return events.AgentUIStateAuditing
		case "tester", "tester-pipeline":
			return events.AgentUIStateTestingPending
		case "librarian", "academic", "archivalist":
			return events.AgentUIStateSearching
		case "guardian":
			return events.AgentUIStateValidating
		default:
			return events.AgentUIStateRunning
		}
	case StatusSuccess:
		switch agent.AgentType {
		case "inspector", "inspector-pipeline":
			return events.AgentUIStatePassed
		}
	case StatusError:
		switch agent.AgentType {
		case "architect":
			return events.AgentUIStatePlanFailed
		case "inspector", "inspector-pipeline":
			return events.AgentUIStateFailed
		case "tester", "tester-pipeline":
			return events.AgentUIStateTestingFailed
		}
	}
	return events.AgentUIStateNone
}

func inferUIStateFromToolStart(agent *AgentState, toolName, argsSummary string) events.AgentUIState {
	if agent == nil {
		return events.AgentUIStateNone
	}
	name := strings.ToLower(strings.TrimSpace(toolName))
	args := strings.ToLower(strings.TrimSpace(argsSummary))
	switch agent.AgentType {
	case "architect":
		return events.AgentUIStatePlanning
	case "guide":
		return events.AgentUIStateThinking
	case "orchestrator":
		return events.AgentUIStateRunning
	case "inspector", "inspector-pipeline":
		return events.AgentUIStateAuditing
	case "tester", "tester-pipeline":
		if isTestInvocation(name, args) {
			return events.AgentUIStateTestingRunning
		}
		return events.AgentUIStateTestingPending
	case "librarian", "academic", "archivalist":
		return events.AgentUIStateSearching
	case "guardian":
		if isInspectionTool(name) {
			return events.AgentUIStateInspecting
		}
		return events.AgentUIStateValidating
	default:
		if isWriteTool(name) {
			return events.AgentUIStateWriting
		}
		if isTestTool(name) {
			return events.AgentUIStateTestingRunning
		}
		return events.AgentUIStateRunning
	}
}

func inferUIStateFromToolFailure(agent *AgentState, event msg.ToolCallEventMsg) events.AgentUIState {
	if agent == nil {
		return events.AgentUIStateNone
	}
	switch agent.AgentType {
	case "tester", "tester-pipeline":
		return events.AgentUIStateTestingFailed
	case "inspector", "inspector-pipeline":
		return events.AgentUIStateFailed
	case "guardian":
		if guardianBlockedText(event.ErrorMsg) || guardianBlockedText(event.Output) {
			return events.AgentUIStateBlocked
		}
		return events.AgentUIStateValidating
	default:
		if isTestTool(event.ToolName) {
			return events.AgentUIStateTestingFailed
		}
	}
	return defaultUIStateForStatus(agent)
}

func fallbackUIStateAfterToolSuccess(agent *AgentState) events.AgentUIState {
	if agent == nil {
		return events.AgentUIStateNone
	}
	switch agent.AgentType {
	case "architect":
		return events.AgentUIStatePlanning
	case "guide":
		return events.AgentUIStateThinking
	case "orchestrator":
		return events.AgentUIStateThinking
	case "inspector", "inspector-pipeline":
		return events.AgentUIStateAuditing
	case "tester", "tester-pipeline":
		return events.AgentUIStateTestingPending
	case "librarian", "academic", "archivalist":
		return events.AgentUIStateSearching
	case "guardian":
		return events.AgentUIStateValidating
	default:
		return events.AgentUIStateThinking
	}
}

func isTerminalUIState(state events.AgentUIState) bool {
	switch events.NormalizeAgentUIState(state) {
	case events.AgentUIStatePassed,
		events.AgentUIStateFailed,
		events.AgentUIStateBlocked,
		events.AgentUIStateAllowed,
		events.AgentUIStatePlanAccepted,
		events.AgentUIStatePlanFailed,
		events.AgentUIStatePlanRejected,
		events.AgentUIStateTestingFailed:
		return true
	default:
		return false
	}
}

func toolSummaryText(toolName, argsSummary string) string {
	toolName = strings.TrimSpace(toolName)
	argsSummary = strings.TrimSpace(argsSummary)
	switch {
	case toolName == "" && argsSummary == "":
		return ""
	case argsSummary == "":
		return "Tool: " + toolName
	case toolName == "":
		return argsSummary
	default:
		return "Tool: " + toolName + " " + argsSummary
	}
}

func isWriteTool(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return containsAny(name,
		"write", "edit", "patch", "apply_patch", "create_file", "update_file",
		"replace", "rename", "delete_file", "insert", "append", "prepend")
}

func isTestTool(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return containsAny(name,
		"test", "pytest", "jest", "vitest", "go_test", "cargo_test", "rspec",
		"playwright", "cypress")
}

func isInspectionTool(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return containsAny(name, "inspect", "scan", "quarantine", "review", "audit")
}

func guardianBlockedText(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	return containsAny(text, "blocked", "denied", "not allowed")
}

func isTestInvocation(toolName, argsSummary string) bool {
	joined := strings.TrimSpace(strings.ToLower(toolName + " " + argsSummary))
	return containsAny(joined,
		"go test",
		"go_test",
		"npm test",
		"pnpm test",
		"yarn test",
		"cargo test",
		"pytest",
		"vitest",
		"jest",
		"rspec",
		"playwright test",
		"cypress",
		"unittest",
		"unit_test",
		"exec_command test",
		"run test",
		"bash test",
		" test ",
	)
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}
