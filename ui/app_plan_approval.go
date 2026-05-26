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
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/claims"
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
	proposal := m.hydratePlanApprovalProposal(request.Proposal)
	// Same gating rule as command approvals: only one dialog
	// substitutes the input panel at a time. Plan-approval and
	// command-approval queues are separate so a pending plan dialog
	// doesn't block a high-priority command-approval (and vice versa);
	// the queue drains in the order proposals arrived for each kind.
	if m.planApproval != nil || m.commandApproval != nil || m.layerDecision != nil {
		m.planApprovalQ = append(m.planApprovalQ, proposal)
		return
	}
	m.planApproval = &planApprovalState{
		proposal:    proposal,
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
			proposal:    m.hydratePlanApprovalProposal(next),
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
// activatePlanApprovalOption: marks selection visually, publishes the
// verdict to the bus immediately, and defers ONLY the dialog dismissal
// by ~75ms so the user still sees press feedback. The bus publish is
// no longer blocked by the visual delay — the architect receives the
// verdict at click time, not 75ms later.
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
	m.publishPlanApprovalVerdict(proposal, verdict)
	return tea.Tick(75*time.Millisecond, func(time.Time) tea.Msg {
		return msg.PlanApprovalResolvedMsg{}
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
func (m *AppModel) publishPlanApprovalVerdict(proposal *planapproval.Proposal, verdict planapproval.Verdict) {
	if proposal == nil || m.deps.GuideBus == nil {
		return
	}
	// Guardian subscribes on TopicResponses("guardian", g.id). The
	// proposal bridge stamps the publishing Guardian ID into Metadata so
	// namespaced/session-specific Guardian IDs receive the verdict too.
	payload := map[string]any{
		"decision": string(verdict),
		"approved": verdict == planapproval.VerdictApprove,
	}
	targetAgentID := planApprovalResponseTarget(proposal)
	topic := guide.TopicResponses("guardian", targetAgentID)
	if err := m.deps.GuideBus.Publish(topic, &guide.Message{
		ID:            uuid.New().String(),
		CorrelationID: proposal.CorrelationID,
		Type:          guide.MessageTypeResponse,
		SourceAgentID: "tui",
		Payload:       payload,
		Timestamp:     time.Now(),
	}); err != nil {
		slog.Warn("ui_plan_approval_response_publish_failed",
			"topic", topic,
			"correlation_id", proposal.CorrelationID,
			"verdict", string(verdict),
			"error", err.Error(),
		)
	}
}

func planApprovalResponseTarget(proposal *planapproval.Proposal) string {
	if proposal == nil || proposal.Metadata == nil {
		return "guardian"
	}
	for _, key := range []string{"target_agent_id", "source_agent_id"} {
		if value, ok := proposal.Metadata[key].(string); ok {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		}
	}
	return "guardian"
}

func (m *AppModel) refreshPlanApprovalArtifact(ev msg.ClaimPresentationMsg) {
	if strings.TrimSpace(ev.SourceType) != "artifact" || strings.TrimSpace(ev.SourceID) == "" {
		return
	}
	if m.planApproval != nil {
		m.planApproval.proposal = hydratePlanApprovalProposalFromPresentation(m.planApproval.proposal, ev)
		m.markSlotDirty(compositor.SlotInput)
		m.viewDirty = true
	}
	for i := range m.planApprovalQ {
		m.planApprovalQ[i] = hydratePlanApprovalProposalFromPresentation(m.planApprovalQ[i], ev)
	}
}

func (m *AppModel) hydratePlanApprovalProposal(proposal *planapproval.Proposal) *planapproval.Proposal {
	clone := clonePlanApprovalProposal(proposal)
	if clone == nil {
		return nil
	}
	artifactID := strings.TrimSpace(clone.PlanArtifactID)
	if artifactID == "" {
		return clone
	}
	if clone.Metadata == nil {
		clone.Metadata = map[string]any{}
	}
	clone.Metadata["plan_artifact_resolved"] = false
	if m == nil || m.claimsBridge == nil {
		return clone
	}
	sessionID := firstNonEmpty(strings.TrimSpace(clone.SessionID), planApprovalMetadataString(clone.Metadata, "session_id"))
	artifact, ok := m.claimsBridge.ResolveArtifact(sessionID, artifactID)
	if !ok || artifact == nil {
		return clone
	}
	return hydratePlanApprovalProposalFromArtifact(clone, artifact)
}

func hydratePlanApprovalProposalFromPresentation(proposal *planapproval.Proposal, ev msg.ClaimPresentationMsg) *planapproval.Proposal {
	clone := clonePlanApprovalProposal(proposal)
	if clone == nil || strings.TrimSpace(clone.PlanArtifactID) == "" {
		return clone
	}
	if strings.TrimSpace(clone.PlanArtifactID) != strings.TrimSpace(ev.SourceID) {
		return clone
	}
	if clone.Metadata == nil {
		clone.Metadata = map[string]any{}
	}
	if content := strings.TrimSpace(ev.Content); content != "" {
		clone.PlanText = content
	}
	if replaceKey := strings.TrimSpace(ev.ReplaceKey); replaceKey != "" {
		clone.PlanArtifactReplaceKey = replaceKey
		clone.Metadata["plan_artifact_replace_key"] = replaceKey
	}
	clone.Metadata["plan_artifact_id"] = strings.TrimSpace(ev.SourceID)
	clone.Metadata["plan_artifact_resolved"] = true
	if hash := planApprovalMetadataString(ev.Metadata, "content_hash", "plan_artifact_content_hash"); hash != "" {
		clone.Metadata["plan_artifact_content_hash"] = hash
	} else if strings.TrimSpace(ev.Content) != "" {
		clone.Metadata["plan_artifact_content_hash"] = planApprovalMarkdownHash(ev.Content)
	}
	for _, key := range []string{"plan_id", "epoch", "revision", "task_count"} {
		if ev.Metadata != nil {
			if value, ok := ev.Metadata[key]; ok {
				clone.Metadata["plan_artifact_"+key] = value
				if _, exists := clone.Metadata[key]; !exists {
					clone.Metadata[key] = value
				}
			}
		}
	}
	return clone
}

func hydratePlanApprovalProposalFromArtifact(proposal *planapproval.Proposal, artifact *claims.Artifact) *planapproval.Proposal {
	clone := clonePlanApprovalProposal(proposal)
	if clone == nil || artifact == nil {
		return clone
	}
	if !claims.PresentationMatches(artifact.Presentation, string(claims.PresentationAudienceUser), string(claims.PresentationSurfaceApproval)) {
		return clone
	}
	if clone.Metadata == nil {
		clone.Metadata = map[string]any{}
	}
	if content := strings.TrimSpace(artifact.Reference); content != "" {
		clone.PlanText = content
	}
	artifactID := strings.TrimSpace(artifact.ID)
	if artifactID != "" {
		clone.PlanArtifactID = artifactID
		clone.Metadata["plan_artifact_id"] = artifactID
	}
	if p := claims.NormalizePresentation(artifact.Presentation); p != nil {
		if replaceKey := strings.TrimSpace(p.ReplaceKey); replaceKey != "" {
			clone.PlanArtifactReplaceKey = replaceKey
			clone.Metadata["plan_artifact_replace_key"] = replaceKey
		}
	}
	clone.Metadata["plan_artifact_resolved"] = true
	if hash := planApprovalMetadataString(artifact.Metadata, "content_hash", "plan_artifact_content_hash"); hash != "" {
		clone.Metadata["plan_artifact_content_hash"] = hash
	} else if strings.TrimSpace(artifact.Reference) != "" {
		clone.Metadata["plan_artifact_content_hash"] = planApprovalMarkdownHash(artifact.Reference)
	}
	for _, key := range []string{"plan_id", "epoch", "revision", "task_count"} {
		if artifact.Metadata != nil {
			if value, ok := artifact.Metadata[key]; ok {
				clone.Metadata["plan_artifact_"+key] = value
				if _, exists := clone.Metadata[key]; !exists {
					clone.Metadata[key] = value
				}
			}
		}
	}
	return clone
}

func clonePlanApprovalProposal(proposal *planapproval.Proposal) *planapproval.Proposal {
	if proposal == nil {
		return nil
	}
	clone := *proposal
	if len(proposal.DriftSignals) > 0 {
		clone.DriftSignals = append([]planapproval.DriftSignal(nil), proposal.DriftSignals...)
	}
	if len(proposal.Metadata) > 0 {
		clone.Metadata = make(map[string]any, len(proposal.Metadata))
		for key, value := range proposal.Metadata {
			clone.Metadata[key] = value
		}
	}
	return &clone
}

// commitPlanApproval is retained for the legacy delivery path (a
// PlanApprovalCommitMsg arrived without the verdict already published
// by activatePlanApprovalOption). It republishes idempotently and
// returns the resolved signal so the dialog dismisses.
func (m *AppModel) commitPlanApproval(commit msg.PlanApprovalCommitMsg) tea.Cmd {
	m.publishPlanApprovalVerdict(commit.Proposal, commit.Verdict)
	return func() tea.Msg {
		return msg.PlanApprovalResolvedMsg{}
	}
}
