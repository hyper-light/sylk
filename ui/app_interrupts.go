package ui

import (
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	tea "github.com/charmbracelet/bubbletea"
)

func (m *AppModel) handleInterrupt() tea.Cmd {
	if m.agentPanel == nil || !m.agentPanel.HasActiveAgent() {
		m.statusBar.SetFlash("Press Ctrl+C again to quit")
		return nil
	}
	return m.interruptActiveRoute("ctrl+c")
}

func (m *AppModel) interruptActiveRoute(reason string) tea.Cmd {
	return m.publishSessionInterrupt(reason, "Agent interrupt requested")
}

func (m *AppModel) interruptAllActiveRoutes(reason string) tea.Cmd {
	return m.publishSessionInterrupt(reason, "All agents interrupt requested")
}

func (m *AppModel) publishSessionInterrupt(reason, flash string) tea.Cmd {
	sessionID := m.resolveRouteSessionID("")
	if m.statusBar != nil {
		m.statusBar.SetFlash(flash)
	}
	if !m.promptQueue.IsEmpty() {
		m.promptQueue.SetPaused(true)
		m.recalcLayout()
		m.viewDirty = true
	}

	return func() tea.Msg {
		trimmedReason := strings.TrimSpace(reason)
		if m.deps.InterruptAllAgents != nil {
			if err := m.deps.InterruptAllAgents(sessionID, trimmedReason); err != nil && m.walLogger != nil {
				m.walLogger.Warn("ui_session_interrupt_failed", "session_id", sessionID, "error", err.Error())
			}
			return nil
		}
		if m.deps.GuideBus == nil {
			return nil
		}
		req := &guide.UserInterruptRequest{
			SessionID:     sessionID,
			SourceAgentID: sourceAgentTUI,
			Scope:         guide.UserInterruptScopeSession,
			Reason:        trimmedReason,
			Timestamp:     time.Now(),
		}
		if err := m.deps.GuideBus.Publish(guide.TopicGuideRequests, guide.NewUserInterruptMessage("", req)); err != nil && m.walLogger != nil {
			m.walLogger.Warn("ui_user_interrupt_publish_failed", "session_id", sessionID, "error", err.Error())
		}
		return nil
	}
}
