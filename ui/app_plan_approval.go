// Plan approval dialog wiring on the AppModel side.
//
// Parallels the command-approval flow at app_editor_mouse.go: arrival
// handler enqueues if another dialog is showing and pops one when free,
// commit handler publishes the structured Decision back through the
// bus to Guardian, and a resolve helper restores focus + drains the
// queue. The render path is in app_layout_render.go (renderPlanApprovalView)
// and is wired into inputPanelView via a check on m.planApproval.
package ui

import (
	"log/slog"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/planapproval"
	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/compositor"
	"github.com/adalundhe/sylk/ui/msg"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
)

func (m *AppModel) handlePlanApprovalRequest(request msg.PlanApprovalRequestMsg) {
	if request.Proposal == nil {
		return
	}
	// Same gating rule as command approvals: only one dialog
	// substitutes the input panel at a time. Plan-approval and
	// command-approval queues are separate so a pending plan dialog
	// doesn't block a high-priority command-approval (and vice versa);
	// the queue drains in the order proposals arrived for each kind.
	if m.planApproval != nil || m.commandApproval != nil || m.layerDecision != nil {
		m.planApprovalQ = append(m.planApprovalQ, request.Proposal)
		return
	}
	m.planApproval = &planApprovalState{
		proposal:    request.Proposal,
		selected:    0,
		activated:   -1,
		returnFocus: m.focus.Current(),
	}
	m.focus.SetFocus(component.FocusInput)
	m.syncFocusState()
	m.recalcLayout()
	m.markSlotDirty(compositor.SlotInput)
	m.viewDirty = true
}

// resolvePlanApproval clears the active dialog, restores focus, and
// pops the next queued plan-approval proposal (if any). Mirrors
// resolveCommandApproval.
func (m *AppModel) resolvePlanApproval() {
	if m.planApproval == nil {
		return
	}
	restore := m.planApproval.returnFocus
	m.planApproval = nil
	if len(m.planApprovalQ) > 0 {
		next := m.planApprovalQ[0]
		m.planApprovalQ = m.planApprovalQ[1:]
		m.planApproval = &planApprovalState{
			proposal:    next,
			selected:    0,
			activated:   -1,
			returnFocus: restore,
		}
		m.focus.SetFocus(component.FocusInput)
	} else if restore != component.FocusID(0) {
		m.focus.SetFocus(restore)
	}
	m.syncFocusState()
	m.recalcLayout()
	m.markSlotDirty(compositor.SlotInput)
	m.viewDirty = true
}

// handlePlanApprovalKey is the keyboard handler for the plan-approval
// dialog. Mirrors handleCommandApprovalKey: arrow keys + tab move
// the selection, enter / space activate the highlighted option,
// escape activates Reject (the dialog's cancel verb). Returns the
// model unchanged for any other key so non-dialog hotkeys can pass
// through to the next handler in the predicate chain.
func (m *AppModel) handlePlanApprovalKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.planApproval == nil {
		return m, nil
	}
	switch key.String() {
	case "up", "shift+tab":
		m.planApproval.selected = wrappedIndex(m.planApproval.selected-1, len(planApprovalOptions))
		m.planApproval.activated = -1
		m.markSlotDirty(compositor.SlotInput)
		m.viewDirty = true
		return m, nil
	case "down", "tab":
		m.planApproval.selected = wrappedIndex(m.planApproval.selected+1, len(planApprovalOptions))
		m.planApproval.activated = -1
		m.markSlotDirty(compositor.SlotInput)
		m.viewDirty = true
		return m, nil
	case "enter", " ":
		return m, m.activatePlanApprovalOption(m.planApproval.selected)
	case "esc":
		// Reject is the dialog's explicit cancel verb (per the
		// architectural contract: three buttons, no separate dismiss).
		// Find Reject by decision string rather than hardcoding the
		// index so reordering planApprovalOptions can't silently
		// remap the cancel key.
		return m, m.activatePlanApprovalOption(planApprovalRejectIndex())
	default:
		return m, nil
	}
}

// planApprovalRejectIndex returns the index of the Reject option in
// planApprovalOptions. Exists so the escape-key handler doesn't
// hardcode an index that would silently break if the option order
// changes.
func planApprovalRejectIndex() int {
	for i, opt := range planApprovalOptions {
		if opt.decision == "reject" {
			return i
		}
	}
	return len(planApprovalOptions) - 1
}

// activatePlanApprovalOption is invoked from key/mouse handlers when
// the user selects an option (Approve / Modify / Reject). Mirrors
// activateCommandApprovalOption: marks selection visually then defers
// the commit by ~75ms so the user sees the press feedback before the
// dialog disappears.
func (m *AppModel) activatePlanApprovalOption(index int) tea.Cmd {
	if m.planApproval == nil || index < 0 || index >= len(planApprovalOptions) {
		return nil
	}
	m.planApproval.selected = index
	m.planApproval.activated = index
	m.markSlotDirty(compositor.SlotInput)
	m.viewDirty = true
	option := planApprovalOptions[index]
	proposal := m.planApproval.proposal
	verdict := planapproval.Verdict(option.decision)
	return tea.Tick(75*time.Millisecond, func(time.Time) tea.Msg {
		return msg.PlanApprovalCommitMsg{
			Proposal: proposal,
			Verdict:  verdict,
		}
	})
}

// commitPlanApproval publishes the structured Decision back through
// the bus to Guardian (TopicResponses("guardian", proposal-source)).
// Guardian's pendingApprovals channel unblocks and the architect's
// continuation handler routes the verdict.
//
// Payload shape mirrors commitCommandApproval: a map with `decision`,
// `approved`, `reason`. We set `decision` to the verdict string so
// Guardian's handleBusResponse decodes it directly into ApprovalResult,
// and `approved` to true only for VerdictApprove (modify and reject
// are explicit non-approval decisions but still successful user
// responses, not denials in the sense Guardian uses for command
// approval; the architect's handler branches on `decision`).
func (m *AppModel) commitPlanApproval(commit msg.PlanApprovalCommitMsg) tea.Cmd {
	if commit.Proposal == nil || m.deps.GuideBus == nil {
		return func() tea.Msg {
			return msg.PlanApprovalResolvedMsg{}
		}
	}
	// The guardian topic for responses is fixed by Guardian's own
	// subscription pattern (TopicResponses("guardian", g.id)). The
	// proposal carries the source agent that published it, but for
	// the canonical guardian-mediated flow that's always guardian's
	// own ID. We reuse the existing command-approval response topic
	// resolution: Guardian sets up the subscription on its own
	// session-bound channels so we publish to the canonical
	// guardian responses topic.
	payload := map[string]any{
		"decision": string(commit.Verdict),
		"approved": commit.Verdict == planapproval.VerdictApprove,
	}
	topic := guide.TopicResponses("guardian", "guardian")
	if err := m.deps.GuideBus.Publish(topic, &guide.Message{
		ID:            uuid.New().String(),
		CorrelationID: commit.Proposal.CorrelationID,
		Type:          guide.MessageTypeResponse,
		SourceAgentID: "tui",
		Payload:       payload,
		Timestamp:     time.Now(),
	}); err != nil {
		slog.Warn("ui_plan_approval_response_publish_failed",
			"topic", topic,
			"correlation_id", commit.Proposal.CorrelationID,
			"verdict", string(commit.Verdict),
			"error", err.Error(),
		)
	}
	return func() tea.Msg {
		return msg.PlanApprovalResolvedMsg{}
	}
}
