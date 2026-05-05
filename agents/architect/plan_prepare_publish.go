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
// The artifact's ID is pre-stamped (uuid.NewString) and recorded on
// the plan struct as plan.HandoffPayloadArtifactID. The user-accept
// dispatch claim (dispatchPlanExecution) carries a validation that
// references this artifact ID, so the orchestrator's claim intake
// can resolve the artifact and run ingestPlan deterministically —
// no LLM tool loop, no parallel bus message, no race on the receipt.
//
// Best-effort on the testament submission itself: if the board is
// unavailable the user-accept path can still re-submit a fresh
// testament+artifact and reference that one. Failures are logged.
//
// Phase semantics live ENTIRELY on the testament/artifact side now.
// The bus path that previously carried Phase=Prepare is gone — the
// orchestrator's prefetch/preparation reaction is driven by
// observing this testament via its plan-scoped subscription.
func (a *Architect) publishPreparedHandoff(ctx context.Context, plan *DesignPlan) {
	if a == nil || plan == nil {
		return
	}
	payload := buildPhasedHandoffPayload(plan, "plan-prepared", PlanHandoffPhasePrepare)
	if !isPlanHandoffPayloadValid(payload) {
		return
	}
	artifactID := uuid.NewString()
	artifact := a.architectArtifact(claims.ArtifactKindPlanHandoffPayload, payload)
	artifact.ID = artifactID
	artifact.Metadata = map[string]any{
		"plan_id":  plan.ID,
		"revision": plan.Revision,
		"phase":    string(PlanHandoffPhasePrepare),
	}
	testament := a.architectTestament(
		fmt.Sprintf("Plan %s prepared: %d tasks, revision %d", plan.ID, len(plan.Tasks), plan.Revision),
		"committed",
		[]*claims.Artifact{artifact},
	)
	plan.HandoffPayloadArtifactID = artifactID
	a.architectSubmitTestament(ctx, testament)
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
