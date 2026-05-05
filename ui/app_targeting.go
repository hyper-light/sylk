package ui

import (
	"fmt"
	"strings"
)

func normalizeExplicitTargetAgent(raw string) string {
	target := strings.ToLower(strings.TrimSpace(raw))
	if target == "" {
		return ""
	}
	if alias, ok := explicitTargetAliases[target]; ok {
		target = alias
	}
	if !isSimpleTargetToken(target) {
		return ""
	}
	return target
}

func (m *AppModel) resolveConcreteTargetAgent(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if target := normalizeExplicitTargetAgent(raw); target != "" {
		return target
	}
	if m != nil && m.agentPanel != nil {
		if resolved := strings.TrimSpace(m.agentPanel.ResolveTargetAgentID(raw)); resolved != "" {
			return resolved
		}
	}
	return ""
}

func (m *AppModel) resolveSubmitTarget(explicit string) string {
	target := strings.TrimSpace(explicit)
	if normalized := normalizeExplicitTargetAgent(target); normalized != "" {
		target = normalized
	}
	if target != "" {
		if target == "guide" || target == "g" {
			m.manualTargetAgent = ""
			return ""
		}
		m.manualTargetAgent = target
		if normalizeAgentID(target) != m.engagedAgentID {
			m.clearEngagedAgent()
		}
		return target
	}

	target = strings.TrimSpace(m.manualTargetAgent)
	if normalized := normalizeExplicitTargetAgent(target); normalized != "" {
		target = normalized
	}
	if target != "" {
		if target == "guide" || target == "g" {
			m.manualTargetAgent = ""
			return ""
		}
		if normalizeAgentID(target) != m.engagedAgentID {
			m.clearEngagedAgent()
		}
		return target
	}

	engaged := normalizeExplicitTargetAgent(m.engagedAgentID)
	if engaged != "" && engaged != "guide" && engaged != "g" {
		return engaged
	}
	return ""
}

func (m *AppModel) syncManualTargetFromAgentSelection() {
	if m == nil || m.agentPanel == nil {
		return
	}
	selected := strings.TrimSpace(m.agentPanel.SelectedAgentID())
	if selected == "" {
		return
	}
	if selected == "guide" || selected == "g" {
		m.manualTargetAgent = ""
		m.clearEngagedAgent()
		return
	}
	m.manualTargetAgent = selected
	if normalizeAgentID(selected) != m.engagedAgentID {
		m.clearEngagedAgent()
	}
}

func isSimpleTargetToken(token string) bool {
	for _, r := range token {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

func routingStatusText(target string) string {
	if target == "" {
		return "Classifying and routing request"
	}
	return fmt.Sprintf("Routing request to @%s", target)
}

// setEngagedAgent updates the sticky engaged agent for conversation continuity.
// Non-guide agents are tracked; "guide" is ignored since the Guide is a router.
func (m *AppModel) setEngagedAgent(agentID string) {
	normalized := normalizeAgentID(agentID)
	if normalized == "" || normalized == "guide" {
		return
	}
	m.engagedAgentID = normalized
	if m.agentPanel != nil {
		m.agentPanel.SetEngagedAgent(normalized)
	}
	if m.statusBar != nil {
		m.statusBar.SetEngagedAgent(normalized)
	}
}

// clearEngagedAgent removes engagement tracking, forcing full classification.
func (m *AppModel) clearEngagedAgent() {
	m.engagedAgentID = ""
	if m.agentPanel != nil {
		m.agentPanel.ClearEngagedAgent()
	}
	if m.statusBar != nil {
		m.statusBar.SetEngagedAgent("")
	}
}
