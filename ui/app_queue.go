package ui

import (
	"github.com/adalundhe/sylk/ui/compositor"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/queue"
	tea "github.com/charmbracelet/bubbletea"
)

func (m *AppModel) tryAdvanceQueue() tea.Cmd {
	if m.promptQueue.IsPaused() || m.promptQueue.IsEmpty() {
		return nil
	}
	ready := m.promptQueue.AdvanceReady(func(string) bool { return false })
	if len(ready) == 0 {
		m.recalcLayout()
		m.viewDirty = true
		return nil
	}
	ids := make([]string, len(ready))
	for i, e := range ready {
		m.promptQueue.MarkDispatching(e.ID)
		ids[i] = e.ID
	}
	return func() tea.Msg {
		return msg.QueueAdvanceMsg{EntryIDs: ids}
	}
}

func (m *AppModel) dispatchQueueEntries(entryIDs []string) tea.Cmd {
	var cmds []tea.Cmd
	for _, id := range entryIDs {
		entry := m.promptQueue.Find(id)
		if entry == nil || entry.State != queue.StateDispatching {
			continue
		}
		submit := msg.SubmitPromptMsg{
			Text:        entry.Text,
			TargetAgent: entry.TargetAgent,
			SessionID:   entry.SessionID,
		}
		if cmd := m.handleSubmit(submit); cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.promptQueue.MarkCompleted(id)
	}
	m.recalcLayout()
	m.markSlotDirty(compositor.SlotQueue)
	m.viewDirty = true
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}
