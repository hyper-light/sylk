package ui

import (
	"strings"
	"time"

	"github.com/adalundhe/sylk/ui/chat"
	"github.com/adalundhe/sylk/ui/compositor"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/status"
	tea "github.com/charmbracelet/bubbletea"
)

func (m *AppModel) resolveRouteSessionID(candidate string) string {
	if sessionID := strings.TrimSpace(candidate); sessionID != "" {
		return sessionID
	}
	if m != nil && m.deps.SessionManager != nil {
		if active, ok := m.deps.SessionManager.GetActive(); ok && active != nil {
			if sessionID := strings.TrimSpace(active.ID()); sessionID != "" {
				return sessionID
			}
		}
	}
	return defaultGuideSessionID
}

func (m *AppModel) handleQuit() tea.Cmd {
	return tea.Quit
}

func (m *AppModel) handleTick(tick msg.TickMsg) tea.Cmd {
	if tick.Gen != m.tickGen {
		return nil
	}
	if m.animationsSuspended(tick.Time) {
		return m.continueTickChain()
	}

	if !m.scroll.settled() {
		m.tickScrollMomentum()
		m.viewDirty = true
	}

	var cmds []tea.Cmd
	if m.gitPanel != nil {
		if cmd := m.gitPanel.DrainCmd(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if m.commitTree != nil {
		if cmd := m.commitTree.DrainCmd(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	cmds = append(cmds, m.continueTickChain())
	return tea.Batch(cmds...)
}

func (m *AppModel) handleDecorTick(tick msg.DecorTickMsg) tea.Cmd {
	if tick.Gen != m.decorGen {
		return nil
	}
	if m.animationsSuspended(tick.Time) {
		return m.continueDecorTickChain()
	}

	changed := false
	changed = m.advanceStatusDecor(tick, changed)
	changed = m.advanceChatDecor(tick, changed)
	changed = m.expireTabArrowFlash(tick.Time, changed)
	changed = m.refreshChatActivityRingHint(changed)
	changed = m.advanceQueueStripDecor(changed)
	changed = m.advanceRightPanelDecor(changed)
	changed = m.advanceGitPanelDecor(changed)
	changed = m.advanceSidebarDecor(tick.Time, changed)
	changed = m.advanceFocusDecor(tick.Time, changed)
	if m.viewMode == ViewMemory && m.refreshMemoryView(false) {
		changed = true
	}

	if changed {
		m.viewDirty = true
	}

	return m.continueDecorTickChain()
}

func (m *AppModel) advanceStatusDecor(tick msg.DecorTickMsg, changed bool) bool {
	if !m.statusBar.IsAnimating() {
		return changed
	}
	model, cmd := m.statusBar.Update(tick)
	_ = cmd
	m.statusBar = model.(*status.Model)
	return changed || m.statusBar.ViewDirty()
}

func (m *AppModel) advanceChatDecor(tick msg.DecorTickMsg, changed bool) bool {
	if !m.chat.HasActiveAnimation() {
		return changed
	}
	chatComp, _ := m.chat.Update(tick)
	m.chat = chatComp.(*chat.Model)
	return true
}

func (m *AppModel) expireTabArrowFlash(now time.Time, changed bool) bool {
	tabFlashChanged := false
	if (m.tabArrowFlashLeftUntil != (time.Time{})) && !now.Before(m.tabArrowFlashLeftUntil) {
		m.tabArrowFlashLeftUntil = time.Time{}
		tabFlashChanged = true
	}
	if (m.tabArrowFlashRightUntil != (time.Time{})) && !now.Before(m.tabArrowFlashRightUntil) {
		m.tabArrowFlashRightUntil = time.Time{}
		tabFlashChanged = true
	}
	if tabFlashChanged {
		m.markSlotDirty(compositor.SlotRight)
		return true
	}
	return changed
}

func (m *AppModel) refreshChatActivityRingHint(changed bool) bool {
	if (m.leftRing.empty() && m.rightRing.empty()) || !m.chat.HasActiveAnimation() {
		return changed
	}
	m.statusBar.SetViewRingHint(m.buildRingHint())
	return true
}

func (m *AppModel) advanceQueueStripDecor(changed bool) bool {
	if m.promptQueue.IsEmpty() || m.promptQueue.IsPaused() {
		return changed
	}
	m.markSlotDirty(compositor.SlotQueue)
	return true
}

func (m *AppModel) advanceRightPanelDecor(changed bool) bool {
	if m.commitTree != nil && m.commitTree.NeedsDecorTick() {
		m.markSlotDirty(compositor.SlotRight)
		changed = true
	}
	if m.diffViewActive && m.diffView != nil && m.diffView.NeedsDecorTick() {
		m.diffView.AdvanceSpinner()
		m.markSlotDirty(compositor.SlotRight)
		changed = true
	}
	if m.mergeDiffViewActive && m.mergeDiffView != nil && m.mergeDiffView.NeedsDecorTick() {
		m.mergeDiffView.AdvanceSpinner()
		m.markSlotDirty(compositor.SlotRight)
		changed = true
	}
	if m.conflictViewActive && m.conflictView != nil && m.conflictView.NeedsDecorTick() {
		m.markSlotDirty(compositor.SlotRight)
		changed = true
	}
	if m.planView != nil && m.planView.NeedsDecorTick() {
		m.planView.MarkViewDirty()
		m.markSlotDirty(compositor.SlotRight)
		changed = true
	}
	return changed
}

func (m *AppModel) advanceGitPanelDecor(changed bool) bool {
	if m.viewMode != ViewGit || m.gitPanel == nil || !m.gitPanel.NeedsDecorTick() {
		return changed
	}
	m.gitPanel.MarkViewDirty()
	m.markSlotDirty(m.sidebarFileListSlot())
	return true
}

func (m *AppModel) advanceSidebarDecor(now time.Time, changed bool) bool {
	agentActive := m.hasActiveAgent()
	if m.agentPanel != nil && m.agentPanel.AdvanceDecor(now) {
		m.markSlotDirty(compositor.SlotLeft)
		changed = true
	}
	if m.sessionPanel != nil {
		m.sessionPanel.SetAgentActive(agentActive)
	}
	if agentActive && m.sessionPanel != nil {
		m.sessionPanel.AdvanceDotFrame()
		m.markSlotDirty(compositor.SlotLeft)
		changed = true
	}
	return changed
}

func (m *AppModel) advanceFocusDecor(now time.Time, changed bool) bool {
	m.focusGradient = m.currentFocusGradient()
	if m.input != nil && m.focusGradient != nil && m.input.CanScroll() {
		if m.input.SetScrollIndicatorColor(m.focusGradient.Sample(now.Sub(m.focusRingStart))) {
			m.markSlotDirty(compositor.SlotInput)
			changed = true
		}
	}
	if m.focusBorderFrameChanged(now) {
		m.markSlotBorderDirty(m.focusBorderGroup())
		changed = true
	}
	return changed
}
