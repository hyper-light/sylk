package ui

import (
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	agentpkg "github.com/adalundhe/sylk/ui/agent"
	tea "github.com/charmbracelet/bubbletea"
)

type appInterruptTarget struct {
	CorrelationID string
	AgentID       string
	AgentType     string
}

func (m *AppModel) handleInterrupt() tea.Cmd {
	if _, ok := m.resolveInterruptTarget(); !ok {
		if m.statusBar != nil {
			m.statusBar.SetFlash("Press Ctrl+C again to quit")
		}
		return nil
	}
	return m.interruptActiveRoute("ctrl+c")
}

func (m *AppModel) interruptActiveRoute(reason string) tea.Cmd {
	target, ok := m.resolveInterruptTarget()
	if !ok {
		uiDebugFileLog().Info("INTERRUPT_DEBUG: ui_interrupt_active_no_target", "reason", reason)
		if m.statusBar != nil {
			m.statusBar.SetFlash("No active agent to interrupt")
		}
		return nil
	}
	uiDebugFileLog().Info("INTERRUPT_DEBUG: ui_interrupt_active_target",
		"reason", reason,
		"correlation_id", target.CorrelationID,
		"agent_id", target.AgentID,
		"agent_type", target.AgentType,
	)
	m.markInterruptRequested([]appInterruptTarget{target}, false)
	return m.publishRequestInterrupt(target, reason, "Agent interrupt requested")
}

func (m *AppModel) interruptAllActiveRoutes(reason string) tea.Cmd {
	targets := m.resolveAllInterruptTargets()
	uiDebugFileLog().Info("INTERRUPT_DEBUG: ui_interrupt_all_targets",
		"reason", reason,
		"target_count", len(targets),
	)
	m.markInterruptRequested(targets, true)
	return m.publishSessionInterrupt(reason, "All agents interrupt requested", targets)
}

func (m *AppModel) markInterruptRequested(targets []appInterruptTarget, all bool) {
	if m == nil {
		return
	}
	if m.agentPanel != nil {
		if all {
			m.agentPanel.DemoteAllActive()
		} else {
			for _, target := range targets {
				if id := firstNonEmpty(target.AgentID, target.AgentType); strings.TrimSpace(id) != "" {
					m.agentPanel.DemoteAgent(id)
				}
			}
		}
	}
	if m.chat != nil {
		for _, target := range targets {
			if cid := strings.TrimSpace(target.CorrelationID); cid != "" {
				m.chat.MarkInterrupted(cid)
			}
		}
	}
	m.viewDirty = true
}

func (m *AppModel) publishRequestInterrupt(target appInterruptTarget, reason, flash string) tea.Cmd {
	target.CorrelationID = strings.TrimSpace(target.CorrelationID)
	if target.CorrelationID == "" {
		return nil
	}
	sessionID := m.resolveRouteSessionID("")
	if m.statusBar != nil {
		m.statusBar.SetFlash(flash)
	}
	m.pausePromptQueueForInterrupt()

	return func() tea.Msg {
		trimmedReason := strings.TrimSpace(reason)
		if m.deps.GuideBus == nil {
			uiDebugFileLog().Info("INTERRUPT_DEBUG: ui_request_interrupt_no_guide_bus",
				"session_id", sessionID,
				"correlation_id", target.CorrelationID,
				"agent_id", target.AgentID,
				"agent_type", target.AgentType,
			)
			if m.deps.InterruptAllAgents != nil {
				if err := m.deps.InterruptAllAgents(sessionID, trimmedReason); err != nil && m.walLogger != nil {
					m.walLogger.Warn("ui_session_interrupt_failed", "session_id", sessionID, "error", err.Error())
				}
			}
			return nil
		}
		req := &guide.UserInterruptRequest{
			CorrelationID: target.CorrelationID,
			SessionID:     sessionID,
			SourceAgentID: sourceAgentTUI,
			Scope:         guide.UserInterruptScopeRequest,
			Reason:        trimmedReason,
			Timestamp:     time.Now(),
		}
		uiDebugFileLog().Info("INTERRUPT_DEBUG: ui_publish_user_interrupt",
			"topic", guide.TopicGuideRequests,
			"scope", string(req.Scope),
			"session_id", sessionID,
			"correlation_id", target.CorrelationID,
			"agent_id", target.AgentID,
			"agent_type", target.AgentType,
		)
		if err := m.deps.GuideBus.Publish(guide.TopicGuideRequests, guide.NewUserInterruptMessage("", req)); err != nil && m.walLogger != nil {
			m.walLogger.Warn("ui_user_interrupt_publish_failed", "session_id", sessionID, "correlation_id", target.CorrelationID, "error", err.Error())
		}
		m.publishCancelAction(target, sessionID, trimmedReason)
		m.publishSessionCancelAction(target, sessionID, trimmedReason)
		return nil
	}
}

func (m *AppModel) publishSessionInterrupt(reason, flash string, targets []appInterruptTarget) tea.Cmd {
	sessionID := m.resolveRouteSessionID("")
	if m.statusBar != nil {
		m.statusBar.SetFlash(flash)
	}
	m.pausePromptQueueForInterrupt()

	return func() tea.Msg {
		trimmedReason := strings.TrimSpace(reason)
		if m.deps.GuideBus != nil {
			req := &guide.UserInterruptRequest{
				SessionID:     sessionID,
				SourceAgentID: sourceAgentTUI,
				Scope:         guide.UserInterruptScopeSession,
				Reason:        trimmedReason,
				Timestamp:     time.Now(),
			}
			uiDebugFileLog().Info("INTERRUPT_DEBUG: ui_publish_user_interrupt",
				"topic", guide.TopicGuideRequests,
				"scope", string(req.Scope),
				"session_id", sessionID,
				"target_count", len(targets),
			)
			if err := m.deps.GuideBus.Publish(guide.TopicGuideRequests, guide.NewUserInterruptMessage("", req)); err != nil && m.walLogger != nil {
				m.walLogger.Warn("ui_user_interrupt_publish_failed", "session_id", sessionID, "error", err.Error())
			}
			for _, target := range targets {
				m.publishCancelAction(target, sessionID, trimmedReason)
				m.publishSessionCancelAction(target, sessionID, trimmedReason)
			}
			if m.deps.InterruptAllAgents != nil {
				if err := m.deps.InterruptAllAgents(sessionID, trimmedReason); err != nil && m.walLogger != nil {
					m.walLogger.Warn("ui_session_interrupt_failed", "session_id", sessionID, "error", err.Error())
				}
			}
			return nil
		}
		if m.deps.InterruptAllAgents != nil {
			if err := m.deps.InterruptAllAgents(sessionID, trimmedReason); err != nil && m.walLogger != nil {
				m.walLogger.Warn("ui_session_interrupt_failed", "session_id", sessionID, "error", err.Error())
			}
		}
		return nil
	}
}

func (m *AppModel) pausePromptQueueForInterrupt() {
	if m == nil || m.promptQueue.IsEmpty() {
		return
	}
	m.promptQueue.SetPaused(true)
	m.recalcLayout()
	m.viewDirty = true
}

func (m *AppModel) resolveInterruptTarget() (appInterruptTarget, bool) {
	if m == nil {
		return appInterruptTarget{}, false
	}
	for _, candidate := range []string{m.manualTargetAgent, m.engagedAgentID} {
		if target, ok := m.resolveInterruptTargetForAgent(candidate); ok {
			return target, true
		}
	}
	if m.agentPanel != nil {
		if target, ok := m.agentPanel.SelectedInterruptTarget(); ok {
			return m.preferChatInterruptTarget(appInterruptTargetFromAgent(target)), true
		}
	}
	if m.chat != nil {
		if target, ok := m.chat.LatestActiveInterruptTarget(); ok {
			return appInterruptTarget{
				CorrelationID: target.CorrelationID,
				AgentID:       firstNonEmpty(target.RuntimeAgentID, target.AgentID, target.AgentType),
				AgentType:     target.AgentType,
			}, true
		}
	}
	targets := m.resolveAllInterruptTargets()
	if len(targets) == 0 {
		return appInterruptTarget{}, false
	}
	return targets[len(targets)-1], true
}

func (m *AppModel) resolveInterruptTargetForAgent(agentID string) (appInterruptTarget, bool) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return appInterruptTarget{}, false
	}
	if m.agentPanel != nil {
		if target, ok := m.agentPanel.InterruptTargetForAgent(agentID); ok {
			return m.preferChatInterruptTarget(appInterruptTargetFromAgent(target)), true
		}
	}
	if m.chat != nil {
		for _, target := range m.chat.ActiveInterruptTargets() {
			if interruptTargetMatchesAgent(target.AgentID, target.AgentType, target.RuntimeAgentID, agentID) {
				return appInterruptTarget{
					CorrelationID: target.CorrelationID,
					AgentID:       firstNonEmpty(target.RuntimeAgentID, target.AgentID, target.AgentType),
					AgentType:     target.AgentType,
				}, true
			}
		}
	}
	return appInterruptTarget{}, false
}

func (m *AppModel) preferChatInterruptTarget(target appInterruptTarget) appInterruptTarget {
	if m == nil || m.chat == nil {
		return target
	}
	for _, chatTarget := range m.chat.ActiveInterruptTargets() {
		if !interruptTargetMatchesAgent(chatTarget.AgentID, chatTarget.AgentType, chatTarget.RuntimeAgentID, target.AgentID) &&
			!interruptTargetMatchesAgent(chatTarget.AgentID, chatTarget.AgentType, chatTarget.RuntimeAgentID, target.AgentType) {
			continue
		}
		correlationID := strings.TrimSpace(chatTarget.CorrelationID)
		if correlationID == "" || correlationID == strings.TrimSpace(target.CorrelationID) {
			continue
		}
		return appInterruptTarget{
			CorrelationID: correlationID,
			AgentID:       firstNonEmpty(chatTarget.RuntimeAgentID, target.AgentID, chatTarget.AgentID, chatTarget.AgentType),
			AgentType:     firstNonEmpty(target.AgentType, chatTarget.AgentType),
		}
	}
	return target
}

func (m *AppModel) resolveAllInterruptTargets() []appInterruptTarget {
	if m == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var targets []appInterruptTarget
	add := func(target appInterruptTarget) {
		target.CorrelationID = strings.TrimSpace(target.CorrelationID)
		if target.CorrelationID == "" {
			return
		}
		if _, ok := seen[target.CorrelationID]; ok {
			return
		}
		seen[target.CorrelationID] = struct{}{}
		target.AgentID = strings.TrimSpace(target.AgentID)
		targets = append(targets, target)
	}
	if m.agentPanel != nil {
		for _, target := range m.agentPanel.ActiveInterruptTargets() {
			add(appInterruptTargetFromAgent(target))
		}
	}
	if m.chat != nil {
		for _, target := range m.chat.ActiveInterruptTargets() {
			add(appInterruptTarget{
				CorrelationID: target.CorrelationID,
				AgentID:       firstNonEmpty(target.RuntimeAgentID, target.AgentID, target.AgentType),
				AgentType:     target.AgentType,
			})
		}
	}
	return targets
}

func appInterruptTargetFromAgent(target agentpkg.InterruptTarget) appInterruptTarget {
	return appInterruptTarget{
		CorrelationID: target.CorrelationID,
		AgentID:       firstNonEmpty(target.RoutingID, target.AgentID, target.AgentType),
		AgentType:     target.AgentType,
	}
}

func interruptTargetMatchesAgent(agentID, agentType, runtimeAgentID, candidate string) bool {
	candidate = strings.ToLower(strings.TrimSpace(candidate))
	if candidate == "" {
		return false
	}
	values := []string{agentID, agentType, runtimeAgentID}
	for _, value := range values {
		if strings.ToLower(strings.TrimSpace(value)) == candidate {
			return true
		}
	}
	return false
}

func (m *AppModel) selectedInterruptDebugAgentID() string {
	if m == nil || m.agentPanel == nil {
		return ""
	}
	return m.agentPanel.SelectedAgentID()
}

func (m *AppModel) publishCancelAction(target appInterruptTarget, sessionID, reason string) {
	m.publishCancelActionWithScope(target, sessionID, reason, true)
}

func (m *AppModel) publishSessionCancelAction(target appInterruptTarget, sessionID, reason string) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	m.publishCancelActionWithScope(target, sessionID, reason, false)
}

func (m *AppModel) publishCancelActionWithScope(target appInterruptTarget, sessionID, reason string, includeCorrelation bool) {
	if m == nil || m.deps.GuideBus == nil {
		return
	}
	target.AgentID = strings.TrimSpace(target.AgentID)
	target.CorrelationID = strings.TrimSpace(target.CorrelationID)
	if target.AgentID == "" {
		uiDebugFileLog().Info("INTERRUPT_DEBUG: ui_cancel_action_no_agent",
			"session_id", sessionID,
			"correlation_id", target.CorrelationID,
			"agent_type", target.AgentType,
		)
		return
	}
	data := map[string]any{
		"session_id": sessionID,
	}
	correlationID := ""
	if includeCorrelation {
		correlationID = target.CorrelationID
	}
	if correlationID != "" {
		data["correlation_id"] = target.CorrelationID
	}
	if reason != "" {
		data["reason"] = reason
	}
	action := &guide.ActionRequest{
		CorrelationID: correlationID,
		SourceAgentID: sourceAgentTUI,
		TargetAgentID: target.AgentID,
		Action:        "cancel",
		Data:          data,
		FireAndForget: true,
		Timestamp:     time.Now(),
	}
	uiDebugFileLog().Info("INTERRUPT_DEBUG: ui_publish_cancel_action",
		"topic", guide.TopicGuideRequests,
		"session_id", sessionID,
		"target_agent", target.AgentID,
		"target_type", target.AgentType,
		"correlation_id", correlationID,
		"session_scoped", !includeCorrelation,
	)
	if err := m.deps.GuideBus.Publish(guide.TopicGuideRequests, guide.NewActionMessage("", action)); err != nil && m.walLogger != nil {
		m.walLogger.Warn("ui_cancel_action_publish_failed", "session_id", sessionID, "target_agent", target.AgentID, "correlation_id", correlationID, "error", err.Error())
	}
	for _, topic := range interruptDirectRequestTopics(target) {
		uiDebugFileLog().Info("INTERRUPT_DEBUG: ui_publish_direct_cancel_action",
			"topic", topic,
			"session_id", sessionID,
			"target_agent", target.AgentID,
			"target_type", target.AgentType,
			"correlation_id", correlationID,
			"session_scoped", !includeCorrelation,
		)
		if err := m.deps.GuideBus.Publish(topic, guide.NewActionMessage("", action)); err != nil && m.walLogger != nil {
			m.walLogger.Warn("ui_direct_cancel_action_publish_failed", "session_id", sessionID, "topic", topic, "target_agent", target.AgentID, "correlation_id", correlationID, "error", err.Error())
		}
	}
}

func interruptDirectRequestTopics(target appInterruptTarget) []string {
	agentID := strings.TrimSpace(target.AgentID)
	if agentID == "" {
		return nil
	}
	seen := map[string]struct{}{}
	var topics []string
	add := func(agentType string) {
		agentType = strings.TrimSpace(agentType)
		if agentType == "" {
			return
		}
		topic := guide.TopicRequests(agentType, agentID)
		if _, ok := seen[topic]; ok {
			return
		}
		seen[topic] = struct{}{}
		topics = append(topics, topic)
	}
	add(target.AgentType)
	add(agentID)
	return topics
}
