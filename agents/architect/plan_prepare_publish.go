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
	"strings"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/google/uuid"
)

// publishPreparedHandoff sends a Phase=Prepare handoff to the
// orchestrator so it can run prepareExecution (verify Guardian
// attestation, build DAG, write WAL/SQLite rows) overlapping with
// the user's approval-dialog review time. On approve, the
// orchestrator's executePrepared just runs scheduler.Submit against
// this prepared DAG.
//
// Best-effort. On any error (no orchestrator registered, bus down,
// payload invalid) we log and return without failing the plan —
// dispatch will fall back to full ingest at approval time.
func (a *Architect) publishPreparedHandoff(ctx context.Context, plan *DesignPlan) {
	if a == nil || a.bus == nil || !a.running || plan == nil {
		return
	}
	targetAgentID := a.knownAgentIDByType("orchestrator", "")
	if strings.TrimSpace(targetAgentID) == "" {
		return
	}
	payload := buildPhasedHandoffPayload(plan, "plan-prepared", PlanHandoffPhasePrepare)
	if !isPlanHandoffPayloadValid(payload) {
		return
	}
	req := &guide.RouteRequest{
		Input:               payload,
		CorrelationID:       "prepare_" + uuid.NewString(),
		ParentCorrelationID: originalCIDFromContext(ctx),
		TargetAgentID:       targetAgentID,
		SessionID:           plan.SessionID,
	}
	if err := a.publishRouteRequest(req); err != nil {
		a.logWarn("publishPreparedHandoff: publish failed",
			"plan_id", plan.ID,
			"error", err.Error())
	}
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
