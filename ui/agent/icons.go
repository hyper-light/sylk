package agent

import (
	"github.com/adalundhe/sylk/core/events"
	"github.com/charmbracelet/lipgloss"
)

type activityIcon struct {
	nerd     string
	fallback string
}

var (
	sharedThinkingIcon       = activityIcon{nerd: "󰧑", fallback: "◉"}
	sharedRespondingIcon     = activityIcon{nerd: "󱅰", fallback: "»"}
	sharedWritingIcon        = activityIcon{nerd: "󰷬", fallback: "✎"}
	sharedRunningIcon        = activityIcon{nerd: "󰞷", fallback: "⚙"}
	testingPendingIcon       = activityIcon{nerd: "󰤑", fallback: "◌"}
	testingRunningIcon       = activityIcon{nerd: "󰙨", fallback: "⚗"}
	testingFailedIcon        = activityIcon{nerd: "󰤒", fallback: "✕"}
	guideRoutingIcon         = activityIcon{nerd: "󰴽", fallback: "⇄"}
	architectPlanningIcon    = activityIcon{nerd: "󰺾", fallback: "⌘"}
	architectAcceptedIcon    = activityIcon{nerd: "󰢨", fallback: "✓"}
	architectFailedIcon      = activityIcon{nerd: "󰳷", fallback: "⚠"}
	architectRejectedIcon    = activityIcon{nerd: "󱘝", fallback: "⊘"}
	orchestratorRunningIcon  = activityIcon{nerd: "󰘬", fallback: "⇄"}
	inspectorAuditingIcon    = activityIcon{nerd: "󱥼", fallback: "◌"}
	inspectorPassedIcon      = activityIcon{nerd: "󰴄", fallback: "✓"}
	inspectorFailedIcon      = activityIcon{nerd: "󱗣", fallback: "✕"}
	librarianSearchingIcon   = activityIcon{nerd: "󰺅", fallback: "⌕"}
	academicSearchingIcon    = activityIcon{nerd: "󰱽", fallback: "⌕"}
	archivalistSearchingIcon = activityIcon{nerd: "󱝪", fallback: "⌕"}
	guardianValidatingIcon   = activityIcon{nerd: "󰶚", fallback: "⚖"}
	guardianInspectingIcon   = activityIcon{nerd: "󱉶", fallback: "⌕"}
	guardianBlockedIcon      = activityIcon{nerd: "󰳌", fallback: "⊘"}
	guardianAllowedIcon      = activityIcon{nerd: "󰳈", fallback: "✓"}
)

var guideClassifyingIcons = [...]activityIcon{
	{nerd: "󰵌", fallback: "X"},
	{nerd: "󰵕", fallback: "Z"},
	{nerd: "󰵑", fallback: "Y"},
}

func resolveAgentIconGlyph(agent AgentState, anim AnimState) string {
	spec := resolveAgentIcon(agent, anim)
	if anim.NerdFonts && spec.nerd != "" {
		return spec.nerd
	}
	if spec.fallback != "" {
		return spec.fallback
	}
	return resolveStaticIcon(agent.Status)
}

func resolveAgentIcon(agent AgentState, anim AnimState) activityIcon {
	switch events.NormalizeAgentUIState(agent.ActivityState) {
	case events.AgentUIStateThinking:
		return sharedThinkingIcon
	case events.AgentUIStateResponding:
		return sharedRespondingIcon
	case events.AgentUIStateWriting:
		return sharedWritingIcon
	case events.AgentUIStateRunning:
		if agent.AgentType == "orchestrator" {
			return orchestratorRunningIcon
		}
		return sharedRunningIcon
	case events.AgentUIStateSearching:
		switch agent.AgentType {
		case "librarian":
			return librarianSearchingIcon
		case "academic":
			return academicSearchingIcon
		case "archivalist":
			return archivalistSearchingIcon
		default:
			return sharedThinkingIcon
		}
	case events.AgentUIStateAuditing:
		return inspectorAuditingIcon
	case events.AgentUIStatePassed:
		return inspectorPassedIcon
	case events.AgentUIStateFailed:
		return inspectorFailedIcon
	case events.AgentUIStateValidating:
		return guardianValidatingIcon
	case events.AgentUIStateInspecting:
		return guardianInspectingIcon
	case events.AgentUIStateBlocked:
		return guardianBlockedIcon
	case events.AgentUIStateAllowed:
		return guardianAllowedIcon
	case events.AgentUIStateRouting:
		return guideRoutingIcon
	case events.AgentUIStateClassifying:
		return guideClassifyingIcons[anim.DotFrame%len(guideClassifyingIcons)]
	case events.AgentUIStatePlanning:
		return architectPlanningIcon
	case events.AgentUIStatePlanAccepted:
		return architectAcceptedIcon
	case events.AgentUIStatePlanFailed:
		return architectFailedIcon
	case events.AgentUIStatePlanRejected:
		return architectRejectedIcon
	case events.AgentUIStateTestingPending:
		return testingPendingIcon
	case events.AgentUIStateTestingRunning:
		return testingRunningIcon
	case events.AgentUIStateTestingFailed:
		return testingFailedIcon
	default:
		return activityIcon{}
	}
}

func iconDisplayWidth(glyph string, nerdFonts bool) int {
	width := lipgloss.Width(glyph)
	if !nerdFonts {
		return width
	}
	for _, r := range glyph {
		if isPUARune(r) {
			width++
		}
	}
	return width
}

func isPUARune(r rune) bool {
	return (r >= 0xE000 && r <= 0xF8FF) ||
		(r >= 0xF0000 && r <= 0xFFFFD) ||
		(r >= 0x100000 && r <= 0x10FFFD)
}
