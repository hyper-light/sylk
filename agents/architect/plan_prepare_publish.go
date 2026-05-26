// Architect-side fire-and-forget publishers for two-phase orchestrator
// ingest. The architect tags handoff payloads with PlanHandoffPhase so
// the orchestrator's ingest skill can branch:
//
//   - publishPreparedHandoff   → Phase=Prepare, called at plan-finalize
//   - publishDiscardPrepared   → Phase=DiscardPrepared, called on
//     user reject/modify so the orchestrator drops cheap prep state
//     instead of carrying it forever.
//
// publishExecutePrepared is NOT here — it's the existing
// dispatchPlanExecution path, just stamped with Phase=ExecutePrepared
// (see plan_execution.go).
//
// All three are fire-and-forget: failures log and continue. The
// orchestrator's executePrepared has a graceful fallback to full
// ingest if no prepared state exists, so a lost prepare publish
// degrades to legacy single-phase behavior — never to ungated
// dispatch.
package architect

import (
	"context"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/google/uuid"
)

// publishPreparedHandoff submits the architect's plan-finalize
// testament: a free-floating testament whose artifact carries the
// full PlanHandoff JSON. This is *evidence*, not a dispatch — the
// architect declares "I have finished planning, here is the plan."
//
// The handoff artifact's ID is pre-stamped (uuid.NewString) and
// recorded on the plan struct as plan.HandoffPayloadArtifactID. The
// same testament also carries the user-presentable plan_markdown
// artifact recorded as plan.PlanMarkdownArtifactID. The user-accept
// dispatch claim (dispatchPlanExecution) carries a validation that
// references the handoff artifact ID, so the orchestrator's claim
// intake can resolve the artifact and run ingestPlan deterministically
// — no LLM tool loop, no parallel bus message, no race on the receipt.
//
// The ready-plan path treats this submission as required evidence:
// callers that are about to ask for user review must check the returned
// error and avoid misleading final prose if the board is unavailable.
//
// Phase semantics live ENTIRELY on the testament/artifact side now.
// The bus path that previously carried Phase=Prepare is gone — the
// orchestrator's prefetch/preparation reaction is driven by
// observing this testament via its plan-scoped subscription.
func (a *Architect) publishPreparedHandoff(ctx context.Context, plan *DesignPlan) error {
	if a == nil || plan == nil {
		return nil
	}
	payload := buildPhasedHandoffPayload(plan, "plan-prepared", PlanHandoffPhasePrepare)
	if !isPlanHandoffPayloadValid(payload) {
		return fmt.Errorf("invalid plan handoff payload for plan %s", plan.ID)
	}
	hasCurrentPlanArtifact := a.planHasCurrentMarkdownArtifactOnBoard(plan)
	if hasCurrentPlanArtifact && strings.TrimSpace(plan.HandoffPayloadArtifactID) != "" {
		return nil
	}
	priorPlanArtifactID := ""
	if strings.TrimSpace(plan.PlanMarkdownArtifactID) != "" && !hasCurrentPlanArtifact {
		priorPlanArtifactID = plan.PlanMarkdownArtifactID
	}
	var planArtifact *claims.Artifact
	replaceKey := plan.PlanMarkdownReplaceKey
	contentHash := plan.PlanMarkdownContentHash
	artifactEpoch := plan.PlanMarkdownArtifactEpoch
	if !hasCurrentPlanArtifact {
		var err error
		planArtifact, replaceKey, contentHash, artifactEpoch, err = a.buildPlanMarkdownArtifact(plan, priorPlanArtifactID)
		if err != nil {
			return err
		}
	}
	handoffArtifactID := uuid.NewString()
	handoffArtifact := a.architectArtifact(claims.ArtifactKindPlanHandoffPayload, payload)
	handoffArtifact.ID = handoffArtifactID
	handoffArtifact.Metadata = map[string]any{
		"plan_id":  plan.ID,
		"epoch":    planMarkdownArtifactEpoch(plan),
		"revision": plan.Revision,
		"phase":    string(PlanHandoffPhasePrepare),
	}
	artifacts := []*claims.Artifact{handoffArtifact}
	if planArtifact != nil {
		artifacts = append([]*claims.Artifact{planArtifact}, artifacts...)
	}
	testament := a.architectTestament(
		fmt.Sprintf("Plan %s ready for review: %d tasks, revision %d", plan.ID, len(plan.Tasks), plan.Revision),
		"committed",
		artifacts,
	)
	if err := a.architectSubmitTestamentSync(ctx, testament); err != nil {
		return err
	}
	plan.HandoffPayloadArtifactID = handoffArtifactID
	if planArtifact != nil {
		plan.PlanMarkdownArtifactID = planArtifact.ID
		plan.PlanMarkdownReplaceKey = replaceKey
		plan.PlanMarkdownContentHash = contentHash
		plan.PlanMarkdownArtifactEpoch = artifactEpoch
	}
	if a.planStore != nil {
		if err := a.planStore.Upsert(plan); err != nil {
			a.logWarn("publishPreparedHandoff: failed to persist plan artifact metadata",
				"plan_id", plan.ID,
				"error", err.Error())
		}
	}
	return nil
}

// publishDiscardPrepared tells the orchestrator to drop any prepared
// DAG it's holding for plan_id. Called on user reject/modify so
// prepared prep state isn't leaked. Best-effort like the others —
// idempotent on the orchestrator side (no-op if no prepared state).
func (a *Architect) publishDiscardPrepared(ctx context.Context, plan *DesignPlan) {
	if a == nil || a.bus == nil || !a.running || plan == nil {
		return
	}
	targetAgentID := a.knownAgentIDByType("orchestrator", "")
	if strings.TrimSpace(targetAgentID) == "" {
		return
	}
	payload := buildPhasedHandoffPayload(plan, "plan-discarded", PlanHandoffPhaseDiscardPrepared)
	if !isPlanHandoffPayloadValid(payload) {
		return
	}
	req := &guide.RouteRequest{
		Input:               payload,
		CorrelationID:       "discard_" + uuid.NewString(),
		ParentCorrelationID: originalCIDFromContext(ctx),
		TargetAgentID:       targetAgentID,
		SessionID:           plan.SessionID,
	}
	if err := a.publishRouteRequest(req); err != nil {
		a.logWarn("publishDiscardPrepared: publish failed",
			"plan_id", plan.ID,
			"error", err.Error())
	}
}
