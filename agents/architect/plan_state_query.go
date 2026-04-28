// Architect-side resolver for orchestrator plan execution state.
//
// Originally a synchronous RPC (plan_state_query route request to the
// orchestrator with a 3-second timeout). Now reads from the local
// orchestratorProjection — built incrementally from the existing
// PlanHandoffReceiptUpdate flow and the dag.status topic — so the
// approval hot path doesn't pay a per-call round-trip.
//
// Same correctness profile as before (eventually-consistent; "no
// orchestrator context" returns nil and callers degrade gracefully)
// with bounded staleness instead of bounded latency. The orchestrator
// still registers the plan_state_query skill (skills_plan_state.go)
// for ad-hoc external queries; the architect just no longer drives
// it on the dialog dispatch path.
package architect

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// orchestratorPlanState is the architect-side mirror of the
// orchestrator's PlanExecutionState. Defined here as a separate type
// to avoid an architect→orchestrator import cycle; the JSON shape is
// identical so encoding round-trips cleanly.
type orchestratorPlanState struct {
	PlanID             string     `json:"plan_id"`
	Status             string     `json:"status"`
	DAGID              string     `json:"dag_id,omitempty"`
	SessionID          string     `json:"session_id,omitempty"`
	Name               string     `json:"name,omitempty"`
	NodesTotal         int        `json:"nodes_total"`
	NodesSucceeded     int        `json:"nodes_succeeded"`
	NodesFailed        int        `json:"nodes_failed"`
	NodesSkipped       int        `json:"nodes_skipped"`
	NodesRemaining     int        `json:"nodes_remaining"`
	CurrentLayer       int        `json:"current_layer,omitempty"`
	TotalLayers        int        `json:"total_layers,omitempty"`
	StartedAt          *time.Time `json:"started_at,omitempty"`
	CompletedAt        *time.Time `json:"completed_at,omitempty"`
	LastObservedAt     *time.Time `json:"last_observed_at,omitempty"`
	StalledForSeconds  int64      `json:"stalled_for_seconds,omitempty"`
	Error              string     `json:"error,omitempty"`
	HistoricalDAGCount int        `json:"historical_dag_count"`
}

// queryOrchestratorPlanState resolves orchestrator plan state from
// the architect's local projection. Returns nil when the projection
// has no record for the plan, which the freshness audit and approval
// routing both treat as "no orchestrator context" (same fallback as
// the old timed-out RPC).
//
// The ctx parameter is retained for signature compatibility with
// existing call sites; cancellation is moot for a local map lookup.
func (a *Architect) queryOrchestratorPlanState(_ context.Context, planID string) *orchestratorPlanState {
	if a == nil {
		return nil
	}
	planID = strings.TrimSpace(planID)
	if planID == "" {
		return nil
	}
	return a.orchestratorProjection.asOrchestratorPlanState(planID)
}

// orchestratorStateHint converts the structured plan state into a
// short string suitable for the proposal's OrchestratorStateHint
// field. Empty when state is nil (architect couldn't query) so the
// dialog falls back to file/age signals only without an empty hint
// label cluttering the UI.
func orchestratorStateHint(state *orchestratorPlanState) string {
	if state == nil {
		return ""
	}
	switch state.Status {
	case "running":
		if state.NodesTotal > 0 {
			return fmt.Sprintf("orchestrator: running (%d/%d nodes)", state.NodesSucceeded, state.NodesTotal)
		}
		return "orchestrator: running"
	case "stalled":
		return fmt.Sprintf("orchestrator: stalled (no progress for %ds)", state.StalledForSeconds)
	case "completed":
		return "orchestrator: completed"
	case "failed":
		return "orchestrator: failed"
	case "aborted":
		return "orchestrator: aborted"
	case "queued":
		return "orchestrator: queued"
	case "none":
		// "none" is the default — no DAG row exists. Surface only when
		// the architect's PendingWork suggested otherwise; otherwise
		// the absence of orchestrator context isn't meaningful.
		return ""
	default:
		return ""
	}
}
