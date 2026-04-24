// Plan acceptance approval gate.
//
// Mirrors the command-approval pattern (skills_command_approval.go) but
// for the plan acceptance dialog: the architect calls this skill via bus
// when a plan reaches Ready; Guardian builds a planapproval.Proposal,
// publishes it to the TUI, blocks on the user's clicked decision, and
// returns the verdict to the architect. The architect routes the verdict
// to actOnAccept / "what would you like changed?" / "what would you like
// to do instead?" — each of which is a normal architect operation.
//
// The dialog UI itself (ui/app_layout_render.go) substitutes the input
// panel with a three-button approval widget when a planapproval.Proposal
// is in flight, identical in mechanism to the command-approval and
// layer-decision dialogs.
package guardian

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/planapproval"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/google/uuid"
)

// planApprovalControlTimeout bounds how long Guardian waits for the
// user to click a verdict before returning a context-deadline error.
// Plans warrant a longer window than command approvals — users may
// scroll the plan, switch panes, or step away to think. 10 minutes
// matches the human attention budget for review.
const planApprovalControlTimeout = 10 * time.Minute

// planApprovalRequest is the wire format the architect sends to invoke
// this skill. Carries the full plan text + identity so Guardian can
// build the proposal without round-tripping back to the architect.
type planApprovalRequest struct {
	PlanID                string                     `json:"plan_id"`
	SessionID             string                     `json:"session_id,omitempty"`
	PlanName              string                     `json:"plan_name,omitempty"`
	PlanSummary           string                     `json:"plan_summary,omitempty"`
	PlanText              string                     `json:"plan_text"`
	FreshnessSummary      string                     `json:"freshness_summary,omitempty"`
	DriftSignals          []planapproval.DriftSignal `json:"drift_signals,omitempty"`
	OrchestratorStateHint string                     `json:"orchestrator_state_hint,omitempty"`
	Metadata              map[string]any             `json:"metadata,omitempty"`
}

// planApprovalResult is the structured response Guardian returns to the
// architect after the user clicks a verdict. The architect's verdict
// handler branches on Verdict.
type planApprovalResult struct {
	PlanID  string             `json:"plan_id"`
	Verdict planapproval.Verdict `json:"verdict"`
	Reason  string             `json:"reason,omitempty"`
}

// planApprovalGateSkill is the bus-callable entry point. The architect
// invokes this via direct skill routing (same mechanism as
// tool_execution_control). Guardian builds the proposal, blocks on the
// user's click, returns the structured verdict.
func planApprovalGateSkill(g *Guardian) *skills.Skill {
	return skills.NewSkill("plan_approval_gate").
		Description("Present a plan acceptance dialog (Approve / Modify / Reject) to the user and return the structured verdict.").
		Domain("control").
		Keywords("guardian", "plan", "approval", "dialog", "policy", "execution").
		Priority(100).
		Usage("Direct-skill only. Architect invokes this when a plan reaches Ready (or is being re-presented after revision/resume). Returns the user's clicked verdict; architect's handler routes accept/modify/reject from there.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var req planApprovalRequest
			if err := json.Unmarshal(input, &req); err != nil {
				return nil, fmt.Errorf("invalid plan approval request payload: %w", err)
			}
			if req.PlanID == "" {
				return nil, fmt.Errorf("plan_approval_gate: plan_id is required")
			}
			if req.PlanText == "" {
				return nil, fmt.Errorf("plan_approval_gate: plan_text is required")
			}
			return g.requestPlanApproval(ctx, &req)
		}).
		Build()
}

// requestPlanApproval is the Guardian-internal implementation that
// publishes the planapproval.Proposal to the TUI and blocks on the
// user's clicked decision. Mirrors requestCommandApproval but with the
// three-verdict shape and a longer timeout.
func (g *Guardian) requestPlanApproval(ctx context.Context, req *planApprovalRequest) (*planApprovalResult, error) {
	if g == nil || g.bus == nil {
		return nil, fmt.Errorf("guardian bus is unavailable for plan approval")
	}
	if req == nil {
		return nil, fmt.Errorf("plan approval request is required")
	}

	// Post claim: architect asks guardian to present plan for user acceptance.
	sessionID := g.activeSessionID
	planTitle := strings.TrimSpace(req.PlanName)
	if planTitle == "" {
		planTitle = req.PlanID
	}
	g.guardianPostClaim(ctx,
		guardianClaimAction(claims.ActionTypeTask),
		guardianExternalClaim(
			"Accept plan: "+truncateCommandForClaim(planTitle, 60),
			"Architect requests user acceptance of implementation plan",
			"architect",
			[]claims.ClaimScopeEntry{{Kind: "plan", Key: req.PlanID}},
			claims.ActionTypeTask,
			[]*claims.Validation{
				guardianValidation(claims.ValidationTypeInspection, true, "User approves plan scope and approach", "User clicks Approve in TUI dialog"),
				guardianValidation(claims.ValidationTypeInspection, false, "Plan reflects current codebase state", "Freshness summary shows no stale references"),
				guardianValidation(claims.ValidationTypeInspection, false, "No significant codebase drift since planning", "Drift signals below threshold"),
			},
		),
	)

	// Submit testament on exit.
	var planResult *planApprovalResult
	var planErr error
	defer func() {
		if planErr != nil {
			g.guardianSubmitTestament(ctx, guardianTestamentAction(), guardianTestament(
				sessionID, "Plan approval failed: "+planErr.Error(), "committed", "architect",
				[]*claims.Artifact{guardianArtifact(sessionID, "error", planErr.Error())},
			))
			return
		}
		if planResult == nil {
			return
		}
		artifacts := []*claims.Artifact{
			guardianArtifact(sessionID, "verdict", string(planResult.Verdict)),
		}
		if planResult.Reason != "" {
			artifacts = append(artifacts, guardianArtifact(sessionID, "user_reason", planResult.Reason))
		}
		if req.FreshnessSummary != "" {
			artifacts = append(artifacts, guardianArtifact(sessionID, "freshness_analysis", req.FreshnessSummary))
		}
		if len(req.DriftSignals) > 0 {
			artifacts = append(artifacts, guardianJSONArtifact(sessionID, "drift_analysis", req.DriftSignals))
		}
		g.guardianSubmitTestament(ctx, guardianTestamentAction(), guardianTestament(
			sessionID, "Plan "+string(planResult.Verdict)+": "+planTitle, "committed", "architect", artifacts,
		))
	}()

	correlationID := uuid.New().String()
	proposal := &planapproval.Proposal{
		PlanID:                req.PlanID,
		CorrelationID:         correlationID,
		SessionID:             req.SessionID,
		PlanName:              req.PlanName,
		PlanSummary:           req.PlanSummary,
		PlanText:              req.PlanText,
		FreshnessSummary:      req.FreshnessSummary,
		DriftSignals:          append([]planapproval.DriftSignal(nil), req.DriftSignals...),
		OrchestratorStateHint: req.OrchestratorStateHint,
		Metadata:              req.Metadata,
		Timestamp:             time.Now(),
	}

	// Pending channel keyed by correlationID. handleBusResponse already
	// routes responses by correlationID into pendingApprovals — we
	// reuse that single source of truth for both command and plan
	// approvals so there's one decision-routing path inside Guardian.
	ch := make(chan ApprovalResult, 1)
	g.pendingMu.Lock()
	g.pendingApprovals[correlationID] = ch
	g.pendingMu.Unlock()
	defer func() {
		g.pendingMu.Lock()
		delete(g.pendingApprovals, correlationID)
		g.pendingMu.Unlock()
	}()

	g.publishActivityStateWithVisibility(ctx, events.EventTypeAgentAction, events.VisibilitySystem,
		"Awaiting user plan acceptance", events.AgentUIStateValidating)

	msg := &guide.Message{
		ID:            uuid.New().String(),
		CorrelationID: correlationID,
		Type:          guide.MessageTypeProposal,
		SourceAgentID: g.id,
		Payload:       proposal,
		Timestamp:     time.Now(),
	}
	if err := g.bus.Publish(guide.TopicResponses("tui", "tui"), msg); err != nil {
		planErr = fmt.Errorf("publish plan approval proposal: %w", err)
		return nil, planErr
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, planApprovalControlTimeout)
	defer cancel()

	select {
	case result := <-ch:
		verdict := planapproval.Verdict(result.Decision)
		if !verdict.IsValid() {
			if result.Approved {
				verdict = planapproval.VerdictApprove
			} else {
				verdict = planapproval.VerdictReject
			}
		}
		planResult = &planApprovalResult{
			PlanID:  req.PlanID,
			Verdict: verdict,
			Reason:  result.Reason,
		}
		return planResult, nil
	case <-timeoutCtx.Done():
		planErr = fmt.Errorf("plan approval timed out after %s waiting for user decision", planApprovalControlTimeout)
		return nil, planErr
	case <-ctx.Done():
		planErr = ctx.Err()
		return nil, planErr
	}
}
