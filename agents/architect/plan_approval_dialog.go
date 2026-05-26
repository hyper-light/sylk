// Plan acceptance dialog flow on the architect side.
//
// When a plan reaches PlanStatusReady the architect publishes a route
// request to Guardian's plan_approval_gate skill, which builds and
// publishes a planapproval.Proposal to the TUI and blocks on the user's
// clicked verdict. The architect records a continuation so its turn
// yields cleanly; when Guardian's response arrives,
// handlePlanApprovalContinuation routes the verdict:
//
//   - Approve → existing actOnAccept → dispatchPlanExecution
//   - Modify  → publish "what would you like changed?" stream message
//     and clear the pending continuation. The user's next
//     normal-routed message goes through architect's planning
//     intent and triggers a fresh plan revision.
//   - Reject  → publish "what would you like to do instead?" stream
//     message and clear the pending continuation. The user's
//     next message can take the conversation in any direction.
//
// This replaces the prior "free-form text → Guide classifier → verdict"
// pipeline for the primary acceptance path. The dialog is the canonical
// decision mechanism; classification is bypassed when an explicit
// verdict is available.
package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/planapproval"
	"github.com/google/uuid"
)

// planApprovalGateSkillName is the Guardian skill that publishes the
// dialog and blocks on the user's verdict. Kept as a const so the
// architect's invocation and the test fixtures stay in lock-step.
const planApprovalGateSkillName = "plan_approval_gate"

// planApprovalGateRequest mirrors guardian.planApprovalRequest. Defined
// here as a separate type to avoid a guardian import cycle; the Guardian
// skill unmarshals into its own type from the same JSON shape.
type planApprovalGateRequest struct {
	PlanID                 string                     `json:"plan_id"`
	SessionID              string                     `json:"session_id,omitempty"`
	PlanName               string                     `json:"plan_name,omitempty"`
	PlanSummary            string                     `json:"plan_summary,omitempty"`
	PlanText               string                     `json:"plan_text"`
	PlanArtifactID         string                     `json:"plan_artifact_id,omitempty"`
	PlanArtifactReplaceKey string                     `json:"plan_artifact_replace_key,omitempty"`
	FreshnessSummary       string                     `json:"freshness_summary,omitempty"`
	DriftSignals           []planapproval.DriftSignal `json:"drift_signals,omitempty"`
	OrchestratorStateHint  string                     `json:"orchestrator_state_hint,omitempty"`
	Metadata               map[string]any             `json:"metadata,omitempty"`
}

// planApprovalGateResult mirrors guardian.planApprovalResult.
type planApprovalGateResult struct {
	PlanID  string               `json:"plan_id"`
	Verdict planapproval.Verdict `json:"verdict"`
	Reason  string               `json:"reason,omitempty"`
}

// presentPlanApprovalDialogBestEffort triggers the dialog publication
// alongside the user-facing plan presentation. Idempotent: if a pending
// plan-approval continuation already exists for this plan (the user
// hasn't clicked yet), the call is a no-op so we don't republish the
// dialog every turn. Errors are logged but never propagated — failure
// to publish the dialog must not break the architect's user-facing
// response. The free-form text classification fallback (via the phase
// gate directive) still applies so the user can type a response if the
// dialog never appears.
func (a *Architect) presentPlanApprovalDialogBestEffort(ctx context.Context, plan *DesignPlan) {
	if a == nil || plan == nil {
		return
	}
	if plan.PendingWork != nil && plan.PendingWork.Kind == string(continuationKindPlanApproval) {
		architectDebugLog().Info("presentPlanApprovalDialog: SKIP_ALREADY_PENDING",
			"plan_id", plan.ID,
			"correlation_id", plan.PendingWork.CorrelationID)
		return
	}
	if err := a.requestPlanApprovalDialog(ctx, plan); err != nil {
		a.logWarn("presentPlanApprovalDialog: publish failed (free-form text fallback still active)",
			"plan_id", plan.ID,
			"error", err.Error())
		architectDebugLog().Warn("presentPlanApprovalDialog: PUBLISH_FAILED",
			"plan_id", plan.ID,
			"error", err.Error())
		return
	}
	architectDebugLog().Info("presentPlanApprovalDialog: PUBLISHED",
		"plan_id", plan.ID)
}

// requestPlanApprovalDialog publishes the plan acceptance proposal
// through Guardian and records a pending continuation so the architect's
// turn yields. Caller MUST be inside an active route handler with
// architect.bus available; returns an error otherwise. On success the
// continuation is persisted and the caller can return a DelegatedError
// to the tool runtime.
func (a *Architect) requestPlanApprovalDialog(ctx context.Context, plan *DesignPlan) error {
	if a == nil {
		return fmt.Errorf("architect is nil")
	}
	if a.bus == nil || !a.running {
		return fmt.Errorf("architect bus is unavailable for plan approval")
	}
	if plan == nil {
		return fmt.Errorf("plan is required for plan approval dialog")
	}

	guardianID := a.knownAgentIDByType("guardian", "guardian")
	if strings.TrimSpace(guardianID) == "" {
		return fmt.Errorf("no registered guardian to gate plan approval")
	}
	if err := a.ensurePlanMarkdownArtifact(ctx, plan); err != nil {
		return fmt.Errorf("plan review artifact unavailable: %w", err)
	}
	planText := strings.TrimSpace(formatPlanForChat(plan))
	if planText == "" {
		return fmt.Errorf("plan %s has no reviewable markdown", plan.ID)
	}

	req := planApprovalGateRequest{
		PlanID:                 plan.ID,
		SessionID:              strings.TrimSpace(plan.SessionID),
		PlanName:               derivePlanName(plan),
		PlanSummary:            planAcceptanceSummary(plan),
		PlanText:               planText,
		PlanArtifactID:         strings.TrimSpace(plan.PlanMarkdownArtifactID),
		PlanArtifactReplaceKey: strings.TrimSpace(plan.PlanMarkdownReplaceKey),
		Metadata: map[string]any{
			"plan_id":                    plan.ID,
			"epoch":                      planMarkdownArtifactEpoch(plan),
			"plan_artifact_id":           strings.TrimSpace(plan.PlanMarkdownArtifactID),
			"plan_artifact_replace_key":  strings.TrimSpace(plan.PlanMarkdownReplaceKey),
			"plan_artifact_content_hash": strings.TrimSpace(plan.PlanMarkdownContentHash),
			"plan_status":                plan.SM().State().String(),
			"task_count":                 len(plan.Tasks),
		},
	}
	// Reuse the audit cached by the conversation enrichment path
	// (preparePlanPresentationAudit). The dialog publish runs AFTER
	// compose, so the audit the LLM saw in its narrative context is
	// already in cache; this read is a no-op-fast path that keeps
	// dialog/chat in lockstep. If the cache is empty (audit failed
	// or compose path skipped enrichment) we run a fresh resume
	// audit so the dialog still has context.
	if audit := a.preparePlanPresentationAudit(ctx, plan, true); audit != nil {
		req.FreshnessSummary = audit.Summary
		req.DriftSignals = append([]planapproval.DriftSignal(nil), audit.Signals...)
		req.OrchestratorStateHint = audit.OrchestratorStateHint
		architectDebugLog().Info("requestPlanApprovalDialog: AUDIT_ATTACHED",
			"plan_id", plan.ID,
			"fresh", audit.Fresh,
			"signals", len(audit.Signals),
			"orchestrator_state", audit.OrchestratorStateHint,
			"recommendation", string(audit.Recommendation))
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("encode plan approval request: %w", err)
	}

	correlationID := "plan_approval_" + uuid.NewString()
	record := &ArchitectContinuation{
		ID:                      "cont_" + uuid.NewString(),
		Kind:                    continuationKindPlanApproval,
		State:                   continuationStatusPending,
		PlanID:                  plan.ID,
		SessionID:               req.SessionID,
		TargetAgentID:           guardianID,
		ResponseCorrelationID:   correlationID,
		InvocationCorrelationID: originalCIDFromContext(ctx),
		ToolName:                planApprovalGateSkillName,
		RequestJSON:             string(payload),
		CreatedAt:               time.Now().UTC(),
		// Match the gate's 10-minute internal timeout; the continuation
		// expires slightly after so the gate's own timeout fires first
		// and produces a structured error rather than a stale
		// continuation hanging around.
		ExpiresAt: time.Now().UTC().Add(11 * time.Minute),
	}
	userMessage := "I've drafted a plan for your review. Use the Approve / Modify / Reject buttons in the input panel to decide."
	if err := a.recordPendingContinuation(plan, record, userMessage); err != nil {
		return fmt.Errorf("record plan approval continuation: %w", err)
	}

	routeReq := &guide.RouteRequest{
		CorrelationID: correlationID,
		Input:         string(payload),
		TargetAgentID: guardianID,
		SessionID:     req.SessionID,
		Metadata: map[string]any{
			"direct_skill": planApprovalGateSkillName,
			"plan_id":      plan.ID,
		},
	}
	if err := a.publishRouteRequest(routeReq); err != nil {
		a.clearPlanPendingContinuationBestEffort(plan, correlationID, "plan approval publish failed")
		a.completeContinuationBestEffort(record, continuationStatusFailed, "", err.Error(), "plan approval publish failed")
		return fmt.Errorf("publish plan approval request: %w", err)
	}
	return nil
}

// handlePlanApprovalContinuation receives Guardian's verdict response
// and routes to the appropriate architect verdict handler. Mirrors the
// shape of handleGuardianApprovalContinuation but switches on the
// planapproval.Verdict instead of a raw approve/deny boolean.
func (a *Architect) handlePlanApprovalContinuation(
	msg *guide.Message,
	record *ArchitectContinuation,
) error {
	resp, respJSON, err := routeResponseFromMessage(msg)
	if err != nil {
		a.completeContinuationBestEffort(record, continuationStatusFailed, "", err.Error(), "plan approval response decode failed")
		return err
	}
	plan := a.planForContinuation(record)
	if err := validateCurrentPlanApprovalContinuation(plan, record); err != nil {
		if plan != nil && plan.PendingWork != nil && plan.PendingWork.CorrelationID == record.ResponseCorrelationID {
			a.clearPlanPendingContinuationBestEffort(plan, record.ResponseCorrelationID, "stale plan approval verdict")
		}
		a.completeContinuationBestEffort(record, continuationStatusFailed, respJSON, err.Error(), "stale plan approval verdict")
		a.publishNotificationPush("That plan approval is stale because the plan has changed. Please review the latest plan before approving.")
		return nil
	}
	if plan != nil {
		a.clearPlanPendingContinuationBestEffort(plan, record.ResponseCorrelationID, "plan approval verdict received")
	}
	if !resp.Success {
		a.completeContinuationBestEffort(record, continuationStatusFailed, respJSON, resp.Error, "plan approval gate returned error")
		a.publishNotificationPush("Plan approval was not completed: " + resp.Error)
		return nil
	}

	verdict, reason, err := decodePlanApprovalVerdict(resp.Data)
	if err != nil {
		a.completeContinuationBestEffort(record, continuationStatusFailed, respJSON, err.Error(), "plan approval verdict decode failed")
		a.publishNotificationPush("Plan approval verdict could not be parsed.")
		return err
	}

	a.completeContinuationBestEffort(record, continuationStatusCompleted, respJSON, "", "plan approval verdict received")

	// Submit verdict testament to claims board.
	planID := ""
	if plan != nil {
		planID = plan.ID
	}
	a.architectSubmitTestament(context.Background(), a.architectTestament(
		fmt.Sprintf("Plan %s verdict: %s", planID, verdict),
		"committed",
		[]*claims.Artifact{
			a.architectArtifact("verdict", string(verdict)),
			a.architectArtifact("reason", reason),
			a.architectArtifact("plan_id", planID),
		},
	))

	if plan == nil {
		// Plan no longer exists in the active plan store (e.g. demoted
		// + reaped). Surface this to the user rather than silently
		// dropping; verdict acted on a missing plan is a noteworthy
		// edge case.
		a.publishNotificationPush(fmt.Sprintf("I received your %s decision, but the plan is no longer active in this session. Let me know what you'd like to do next.", verdict))
		return nil
	}

	switch verdict {
	case planapproval.VerdictApprove:
		ctx := context.Background()
		return a.routeApprovedPlanByOrchestratorState(ctx, plan)
	case planapproval.VerdictModify:
		// Modify means the user wants the plan revised. The cached
		// audit applies to the soon-to-be-stale plan; invalidate it
		// so the next presentation re-runs against the revised plan.
		// Also drop any prepared DAG the orchestrator was holding for
		// this plan revision — the next plan generation will publish
		// a fresh prepare against the revised payload.
		a.invalidatePlanAuditCache(plan.ID)
		a.publishDiscardPrepared(context.Background(), plan)
		ask := "What would you like changed about the plan? Tell me which tasks to revise, what to add, or what to remove and I'll update it for you."
		if reason != "" {
			ask = ask + "\n\n" + reason
		}
		a.publishNotificationPush(ask)
		return nil
	case planapproval.VerdictReject:
		// Reject is the cancel verb. The current plan is being scrapped;
		// drop its cached audit so a fresh-direction follow-up doesn't
		// reuse stale evidence. Likewise, tell the orchestrator to
		// drop its prepared DAG so prep state isn't leaked.
		a.invalidatePlanAuditCache(plan.ID)
		a.publishDiscardPrepared(context.Background(), plan)
		ask := "Understood — the plan isn't right. What would you like to do instead? You can describe a different approach or tell me to start over from scratch."
		if reason != "" {
			ask = ask + "\n\n" + reason
		}
		a.publishNotificationPush(ask)
		return nil
	default:
		a.publishNotificationPush(fmt.Sprintf("Unrecognized plan approval verdict: %q", verdict))
		return fmt.Errorf("unrecognized verdict %q", verdict)
	}
}

// planAcceptanceSummary returns a short one-paragraph synopsis of the
// plan suitable for the dialog's header. Falls back to the plan's
// query when no architect-derived summary is present.
func planAcceptanceSummary(plan *DesignPlan) string {
	if plan == nil {
		return ""
	}
	if plan.Architecture != nil && strings.TrimSpace(plan.Architecture.Description) != "" {
		return strings.TrimSpace(plan.Architecture.Description)
	}
	return strings.TrimSpace(plan.Query)
}

// decodePlanApprovalVerdict pulls verdict + reason out of the gate's
// returned data payload (which may arrive as a structured map or a JSON
// blob depending on transport).
func decodePlanApprovalVerdict(data any) (planapproval.Verdict, string, error) {
	if data == nil {
		return "", "", fmt.Errorf("plan approval verdict payload is nil")
	}
	switch typed := data.(type) {
	case *planApprovalGateResult:
		if typed == nil {
			return "", "", fmt.Errorf("plan approval result is nil")
		}
		return typed.Verdict, typed.Reason, nil
	case planApprovalGateResult:
		return typed.Verdict, typed.Reason, nil
	case map[string]any:
		verdict, _ := typed["verdict"].(string)
		reason, _ := typed["reason"].(string)
		return planapproval.Verdict(strings.TrimSpace(verdict)), strings.TrimSpace(reason), nil
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return "", "", fmt.Errorf("encode plan approval verdict for fallback decode: %w", err)
	}
	var result planApprovalGateResult
	if err := json.Unmarshal(encoded, &result); err != nil {
		return "", "", fmt.Errorf("decode plan approval verdict: %w", err)
	}
	return result.Verdict, result.Reason, nil
}

func validateCurrentPlanApprovalContinuation(plan *DesignPlan, record *ArchitectContinuation) error {
	if record == nil || record.Kind != continuationKindPlanApproval || plan == nil {
		return nil
	}
	if plan.PendingWork != nil &&
		plan.PendingWork.Kind == string(continuationKindPlanApproval) &&
		strings.TrimSpace(plan.PendingWork.CorrelationID) != "" &&
		strings.TrimSpace(plan.PendingWork.CorrelationID) != strings.TrimSpace(record.ResponseCorrelationID) &&
		planHasActivePendingWork(plan, time.Now().UTC()) {
		return fmt.Errorf("newer plan approval is pending for plan %s", plan.ID)
	}
	if !planHasCurrentMarkdownArtifact(plan) {
		return fmt.Errorf("plan %s has no current review artifact", plan.ID)
	}
	var req planApprovalGateRequest
	if strings.TrimSpace(record.RequestJSON) != "" {
		if err := json.Unmarshal([]byte(record.RequestJSON), &req); err != nil {
			return fmt.Errorf("decode plan approval request: %w", err)
		}
	}
	if req.PlanID != "" && strings.TrimSpace(req.PlanID) != strings.TrimSpace(plan.ID) {
		return fmt.Errorf("approval request plan %s does not match current plan %s", req.PlanID, plan.ID)
	}
	if req.PlanArtifactID != "" && strings.TrimSpace(req.PlanArtifactID) != strings.TrimSpace(plan.PlanMarkdownArtifactID) {
		return fmt.Errorf("approval artifact %s is stale; current artifact is %s", req.PlanArtifactID, plan.PlanMarkdownArtifactID)
	}
	if req.PlanArtifactReplaceKey != "" && strings.TrimSpace(req.PlanArtifactReplaceKey) != strings.TrimSpace(plan.PlanMarkdownReplaceKey) {
		return fmt.Errorf("approval replace key %s is stale; current replace key is %s", req.PlanArtifactReplaceKey, plan.PlanMarkdownReplaceKey)
	}
	if hash := planApprovalRequestMetadataString(req.Metadata, "plan_artifact_content_hash", "content_hash"); hash != "" &&
		hash != strings.TrimSpace(plan.PlanMarkdownContentHash) {
		return fmt.Errorf("approval artifact hash %s is stale; current hash is %s", hash, plan.PlanMarkdownContentHash)
	}
	if epoch := planApprovalRequestMetadataUint64(req.Metadata, "epoch", "plan_artifact_epoch"); epoch > 0 &&
		epoch != planMarkdownArtifactEpoch(plan) {
		return fmt.Errorf("approval epoch %d is stale; current epoch is %d", epoch, planMarkdownArtifactEpoch(plan))
	}
	return nil
}

func planApprovalRequestMetadataString(metadata map[string]any, keys ...string) string {
	for _, key := range keys {
		if metadata == nil {
			return ""
		}
		if value := stringFromAny(metadata[key]); strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func planApprovalRequestMetadataUint64(metadata map[string]any, keys ...string) uint64 {
	for _, key := range keys {
		if metadata == nil {
			return 0
		}
		switch value := metadata[key].(type) {
		case uint64:
			return value
		case uint:
			return uint64(value)
		case int:
			if value > 0 {
				return uint64(value)
			}
		case int64:
			if value > 0 {
				return uint64(value)
			}
		case float64:
			if value > 0 {
				return uint64(value)
			}
		case json.Number:
			if n, err := value.Int64(); err == nil && n > 0 {
				return uint64(n)
			}
		case string:
			n, _ := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
			return n
		}
	}
	return 0
}
