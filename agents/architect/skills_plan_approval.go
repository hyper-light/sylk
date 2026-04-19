// LLM-callable trigger for the plan acceptance dialog.
//
// The dialog is normally fired automatically by executeConversation
// after the tool loop produces a Ready plan, but that path is fragile
// when the LLM completes its turn through a non-canonical return
// (recovery branches, cached protocol completion, mid-stream context
// hand-offs). Exposing the dialog as a first-class skill lets the
// LLM trigger it explicitly: after start_planning reports Ready, the
// LLM calls present_plan_approval_dialog(plan_id) before composing
// its narrative summary, so the user sees BOTH the prose AND the
// Approve/Modify/Reject buttons.
//
// Idempotent — if a pending plan-approval continuation already exists
// for the plan, the call is a no-op. Best-effort — never errors out
// the tool loop; failures are surfaced in the result map and logged.
package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
)

type presentPlanApprovalDialogRequest struct {
	PlanID string `json:"plan_id"`
}

func presentPlanApprovalDialogSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("present_plan_approval_dialog").
		Description("Publish the Approve / Modify / Reject dialog to the user for a Ready plan. Call this AFTER start_planning reports the plan reached Ready and BEFORE composing your narrative summary. Idempotent — safe to call repeatedly; the dialog only publishes once per pending continuation.").
		Domain("planning").
		Keywords("plan", "approval", "dialog", "approve", "present").
		Priority(75).
		Usage("Direct-skill. Invoke once per Ready plan to give the user the explicit verdict buttons. The dialog renders in the input panel and routes the user's clicked verdict (Approve / Modify / Reject) directly to the architect — replacing free-form text classification for the primary acceptance path. After calling, you can still write a narrative summary; the dialog appears alongside it.").
		StringParam("plan_id", "Plan identifier to publish the dialog for. Must reference a plan in PlanStatusReady.", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var req presentPlanApprovalDialogRequest
			if err := json.Unmarshal(input, &req); err != nil {
				return nil, fmt.Errorf("invalid present_plan_approval_dialog payload: %w", err)
			}
			req.PlanID = strings.TrimSpace(req.PlanID)
			if req.PlanID == "" {
				return nil, fmt.Errorf("present_plan_approval_dialog: plan_id is required")
			}
			plan := a.planStore.Get(req.PlanID)
			if plan == nil {
				return map[string]any{
					"published": false,
					"plan_id":   req.PlanID,
					"error":     "plan not found in active plan store",
				}, nil
			}
			if plan.PendingWork != nil && plan.PendingWork.Kind == string(continuationKindPlanApproval) {
				return map[string]any{
					"published":      false,
					"plan_id":        req.PlanID,
					"already_pending": true,
					"correlation_id": plan.PendingWork.CorrelationID,
				}, nil
			}
			if err := a.requestPlanApprovalDialog(ctx, plan); err != nil {
				return map[string]any{
					"published": false,
					"plan_id":   req.PlanID,
					"error":     err.Error(),
				}, nil
			}
			return map[string]any{
				"published": true,
				"plan_id":   req.PlanID,
			}, nil
		}).
		Build()
}
