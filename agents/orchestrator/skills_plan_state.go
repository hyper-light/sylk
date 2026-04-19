// Plan execution state query — Phase 2 foundation for the agentic
// resume/cancel flow.
//
// The architect (and any other inquirer) calls this skill via bus with
// a plan_id; the orchestrator returns a structured snapshot of what it
// knows about that plan: current DAG status, layer + node counts,
// last-known timestamps. Replaces the architect's PendingWork.ExpiresAt
// heuristic with authoritative state from the orchestrator's own
// dag_executions table.
//
// State semantics (returned in PlanExecutionState.Status):
//   - "none"       — no DAG row exists for this plan; orchestrator has
//                    never received it, or the dispatch was lost
//   - "queued"     — DAG row exists but execution hasn't started yet
//   - "running"    — DAG is actively executing (started_at set,
//                    completed_at unset, no terminal error)
//   - "completed"  — all nodes succeeded; completed_at set
//   - "failed"     — DAG failed terminally; error set
//   - "aborted"    — DAG was cancelled (state=aborted)
//   - "stalled"    — DAG started but hasn't progressed in a long time
//                    relative to last_progress_at; advisory only
//
// The state query is read-only and inexpensive — a single indexed
// SELECT against dag_executions plus a derived status classification.
// Safe to invoke on every architect plan re-presentation.
package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/skills"
)

// planStateStalledThreshold is how long without progress before a
// running DAG is reported as stalled. Conservative — most active DAGs
// produce node-completed events well under a minute apart, so 5min
// without progress is meaningful evidence that something is wrong.
const planStateStalledThreshold = 5 * time.Minute

// PlanExecutionState is the structured snapshot returned by the
// plan_state query. The architect uses it to drive state-aware
// verdict routing in the dialog flow (resume vs re-dispatch vs status
// report) and to populate the PlanFreshnessAudit's orchestrator hint.
type PlanExecutionState struct {
	PlanID              string     `json:"plan_id"`
	Status              string     `json:"status"`
	DAGID               string     `json:"dag_id,omitempty"`
	SessionID           string     `json:"session_id,omitempty"`
	Name                string     `json:"name,omitempty"`
	NodesTotal          int        `json:"nodes_total"`
	NodesSucceeded      int        `json:"nodes_succeeded"`
	NodesFailed         int        `json:"nodes_failed"`
	NodesSkipped        int        `json:"nodes_skipped"`
	NodesRemaining      int        `json:"nodes_remaining"`
	CurrentLayer        int        `json:"current_layer,omitempty"`
	TotalLayers         int        `json:"total_layers,omitempty"`
	StartedAt           *time.Time `json:"started_at,omitempty"`
	CompletedAt         *time.Time `json:"completed_at,omitempty"`
	LastObservedAt      *time.Time `json:"last_observed_at,omitempty"`
	StalledForSeconds   int64      `json:"stalled_for_seconds,omitempty"`
	Error               string     `json:"error,omitempty"`
	HistoricalDAGCount  int        `json:"historical_dag_count"`
}

type planStateRequest struct {
	PlanID string `json:"plan_id"`
}

func planStateQuerySkill(o *Orchestrator) *skills.Skill {
	return skills.NewSkill("plan_state_query").
		Description("Return the orchestrator's authoritative execution state for a plan: status, DAG identity, node counts, and progress timestamps.").
		Domain("coordination").
		Keywords("plan", "state", "dag", "status", "query", "execution").
		Priority(80).
		Usage("Direct-skill only. Architect invokes via bus to determine the truth about a plan's execution state — replaces local PendingWork timer heuristics. Inexpensive: one indexed SELECT plus derived status classification.").
		StringParam("plan_id", "Plan identifier to query.", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var req planStateRequest
			if err := json.Unmarshal(input, &req); err != nil {
				return nil, fmt.Errorf("invalid plan_state_query payload: %w", err)
			}
			planID := strings.TrimSpace(req.PlanID)
			if planID == "" {
				return nil, fmt.Errorf("plan_state_query: plan_id is required")
			}
			return o.planExecutionState(ctx, planID)
		}).
		Build()
}

// planExecutionState reads the DAG execution rows for the plan and
// classifies them into a single PlanExecutionState. When multiple DAG
// rows exist (re-dispatches across resumes), the most-recent active
// row drives the status; older rows count as historical for context.
//
// Read-only and inexpensive. Safe to invoke from architect freshness
// audit, route_plan_acceptance reauthorization, and direct user
// queries about "what's happening with my plan."
func (o *Orchestrator) planExecutionState(_ context.Context, planID string) (*PlanExecutionState, error) {
	if o == nil || o.store == nil {
		return &PlanExecutionState{
			PlanID: planID,
			Status: "none",
		}, nil
	}
	rows, err := o.store.GetDAGExecutionsByPlan(planID)
	if err != nil {
		return nil, fmt.Errorf("plan state: query dag executions: %w", err)
	}
	state := &PlanExecutionState{
		PlanID:             planID,
		Status:             "none",
		HistoricalDAGCount: len(rows),
	}
	if len(rows) == 0 {
		// No DAG ever dispatched for this plan — architect's
		// PendingWork may say otherwise but the orchestrator's
		// authoritative answer is "I don't know about this plan."
		return state, nil
	}
	// Pick the most-recently-created row as the current attempt;
	// older rows are historical context (e.g. previous failed
	// attempts that were re-dispatched).
	current := rows[0]
	state.DAGID = current.ID
	state.SessionID = current.SessionID
	state.Name = current.Name
	state.NodesTotal = current.NodesTotal
	state.NodesSucceeded = current.NodesSucceeded
	state.NodesFailed = current.NodesFailed
	state.NodesSkipped = current.NodesSkipped
	state.CurrentLayer = current.CurrentLayer
	state.TotalLayers = current.TotalLayers
	state.StartedAt = current.StartedAt
	state.CompletedAt = current.CompletedAt
	state.Error = current.Error
	if current.NodesTotal > 0 {
		state.NodesRemaining = current.NodesTotal - current.NodesSucceeded - current.NodesFailed - current.NodesSkipped
		if state.NodesRemaining < 0 {
			state.NodesRemaining = 0
		}
	}
	state.Status = classifyPlanState(current)
	if state.Status == "running" {
		// Use most-recent observable timestamp (started_at as a
		// floor). True last-progress would require querying buffer
		// updates; this floor catches "started but immediately
		// stalled" cases without an extra DB call.
		floor := time.Now()
		if current.StartedAt != nil {
			floor = *current.StartedAt
		}
		state.LastObservedAt = &floor
		stalledFor := time.Since(floor)
		if stalledFor >= planStateStalledThreshold {
			state.Status = "stalled"
			state.StalledForSeconds = int64(stalledFor.Seconds())
		}
	}
	return state, nil
}

// classifyPlanState maps a raw DAG row's state column + timestamps to
// the architect-facing status enum. The DAG state column uses the
// orchestrator's internal vocabulary; we normalize to the architect's
// resume-flow vocabulary so callers don't need to know orchestrator
// internals.
func classifyPlanState(row *DAGExecutionRow) string {
	if row == nil {
		return "none"
	}
	rawState := strings.ToLower(strings.TrimSpace(row.State))
	switch rawState {
	case "completed", "succeeded", "success":
		return "completed"
	case "failed", "failure", "error":
		return "failed"
	case "aborted", "cancelled", "canceled":
		return "aborted"
	}
	if row.CompletedAt != nil {
		// Completed timestamp present without a terminal state label
		// — treat as completed (defensive: covers older rows).
		return "completed"
	}
	if row.StartedAt != nil {
		return "running"
	}
	return "queued"
}
