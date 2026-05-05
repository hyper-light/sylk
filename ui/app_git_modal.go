package ui

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/ui/conflictview"
	"github.com/adalundhe/sylk/ui/modal"
	"github.com/adalundhe/sylk/ui/msg"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func (m *AppModel) handleConflictPreview(typed msg.ConflictPreviewMsg) tea.Cmd {
	if typed.Result == nil || typed.Result.Clean {
		if m.pendingSeqOp != nil {
			op := m.pendingSeqOp
			m.pendingSeqOp = nil
			return m.executeSequencerOp(op)
		}
		return nil
	}

	conflicts := typed.Result.Conflicts
	items := make([]modal.ListModalItem, len(conflicts))
	for i, c := range conflicts {
		items[i] = modal.ListModalItem{
			Label: c.Path,
			Color: m.config.Theme().Palette.Error,
		}
	}
	footer := fmt.Sprintf("%d file(s) will conflict", len(conflicts))
	lm := modal.NewListModal("Conflicts Detected ("+typed.Op+")", items, footer,
		[]string{"Continue", "Cancel"}, m.config.Theme())
	m.modalOverlay.Push(lm)
	if m.pendingSeqOp != nil {
		m.pendingSeqOp.phase = 2
	}
	return nil
}

func (m *AppModel) handleIntegrationDetected(typed msg.IntegrationDetectedMsg) tea.Cmd {
	var integrated int
	for _, r := range typed.Results {
		if r.Integrated {
			integrated++
		}
	}

	if integrated == 0 {
		if m.pendingSeqOp != nil {
			return m.conflictPreviewCherryPickCmd(m.pendingSeqOp.hashes, nil)
		}
		return nil
	}

	items := make([]modal.ListModalItem, 0, len(typed.Results))
	for _, r := range typed.Results {
		badge := ""
		var color lipgloss.Color
		if r.Integrated {
			badge = "INTEGRATED"
			color = m.config.Theme().Palette.Teal
		}
		items = append(items, modal.ListModalItem{
			Label:  r.CommitHash[:min(len(r.CommitHash), 8)],
			Detail: r.Subject,
			Badge:  badge,
			Color:  color,
		})
	}
	footer := fmt.Sprintf("%d of %d commit(s) already integrated", integrated, len(typed.Results))
	lm := modal.NewListModal("Cherry-Pick Integration Check", items, footer,
		[]string{"Apply All", "Cancel"}, m.config.Theme())
	m.modalOverlay.Push(lm)
	if m.pendingSeqOp != nil {
		m.pendingSeqOp.phase = 1
	}
	return nil
}

func (m *AppModel) handleAbortPreserved(typed msg.AbortPreservedMsg) tea.Cmd {
	if typed.Err != nil {
		m.statusBar.SetFlash("Abort preservation failed: " + typed.Err.Error())
	} else if typed.Preservation != nil {
		parts := []string{"State preserved"}
		if typed.Preservation.BackupBranch != "" {
			parts = append(parts, "ref: "+typed.Preservation.BackupBranch)
		}
		if len(typed.Preservation.StashedPaths) > 0 {
			parts = append(parts, fmt.Sprintf("%d file(s) stashed", len(typed.Preservation.StashedPaths)))
		}
		m.statusBar.SetFlash(strings.Join(parts, " - "))
	}

	bus := m.gitBus
	return func() tea.Msg {
		if err := bus.SequencerAbort(); err != nil {
			return sequencerAbortFailedMsg{reason: err.Error()}
		}
		return sequencerAbortedMsg{}
	}
}

func (m *AppModel) handleBranchStashAvailable(typed msg.BranchStashAvailableMsg) tea.Cmd {
	m.statusBar.SetFlash(
		fmt.Sprintf("Branch stash available (%d files) - restoring", typed.Meta.FileCount))
	bus := m.gitBus
	branch := typed.Meta.BranchName
	return func() tea.Msg {
		err := bus.UnstashForBranch(branch)
		return msg.BranchStashRestoredMsg{Err: err}
	}
}

func (m *AppModel) handleModalClosed(result any) tea.Cmd {
	if !m.modalOverlay.Active() {
		m.overlay = overlayNone
	}

	lr, ok := result.(modal.ListModalResult)
	if !ok {
		return nil
	}

	if m.pendingCommitPhase != 0 {
		return m.routePreCommitModal(lr)
	}
	if m.pendingSeqOp != nil {
		return m.routeSequencerModal(lr)
	}
	if m.pendingSyntaxValidation {
		m.pendingSyntaxValidation = false
		if lr.Action == 0 {
			return func() tea.Msg {
				return conflictview.SyntaxValidationResultMsg{Proceed: true}
			}
		}
		return nil
	}

	return nil
}

func (m *AppModel) routePreCommitModal(lr modal.ListModalResult) tea.Cmd {
	phase := m.pendingCommitPhase
	paths := m.pendingCommitPaths
	message := m.pendingCommitMessage
	m.pendingCommitPaths = nil
	m.pendingCommitMessage = ""
	m.pendingCommitPhase = 0

	if lr.Action != 0 {
		m.statusBar.SetFlash("Commit cancelled")
		return nil
	}

	bus := m.gitBus
	switch phase {
	case 1:
		return func() tea.Msg {
			secrets, err := bus.ScanStagedSecrets(paths)
			if err == nil && len(secrets) > 0 {
				return msg.SecretsDetectedMsg{Findings: secrets, Paths: paths, Message: message}
			}
			return msg.PreCommitCleanMsg{Paths: paths, Message: message}
		}
	case 2:
		return func() tea.Msg {
			if err := bus.CommitFiles(paths, message); err != nil {
				return commitFailedMsg{reason: err.Error()}
			}
			return commitSucceededMsg{message: message}
		}
	}
	return nil
}

func (m *AppModel) routeSequencerModal(lr modal.ListModalResult) tea.Cmd {
	op := m.pendingSeqOp
	m.pendingSeqOp = nil

	if lr.Action != 0 {
		if m.commitTree != nil {
			m.commitTree.ClearLoadingMessage()
		}
		m.statusBar.SetFlash(op.op + " cancelled")
		return nil
	}

	return m.executeSequencerOp(op)
}

func (m *AppModel) executeSequencerOp(op *pendingSequencerOp) tea.Cmd {
	bus := m.gitBus
	switch op.op {
	case "cherry-pick":
		return func() tea.Msg {
			status, err := bus.CherryPickSequence(op.hashes, op.target)
			if err != nil {
				return sequencerFailedMsg{reason: err.Error()}
			}
			return sequencerResultMsg{status: status}
		}
	case "rebase":
		return func() tea.Msg {
			status, err := bus.RebaseInteractive(op.target, op.sourceBranch, op.plan)
			if err != nil {
				return sequencerFailedMsg{reason: err.Error()}
			}
			return sequencerResultMsg{status: status}
		}
	case "merge":
		return m.executeMergeBranch(op.delete)
	}
	return nil
}
