// Plan-centric orchestration lifecycle verbs: cancel_orchestration
// and resume_orchestration. These complement the DAG-centric verbs
// (cancel_dag, execute_dag) by speaking in the architect's
// vocabulary: "I want this PLAN cancelled / resumed" rather than
// "I want DAG dag_abc123 cancelled / re-executed."
//
// Why first-class verbs distinct from dispatch:
//   - dispatch (execute_dag / ingest_plan) means "this is a brand new
//     plan; build a DAG and start it"
//   - cancel_orchestration means "stop the active execution for this
//     plan, regardless of which DAG attempt is currently running"
//   - resume_orchestration means "this plan's execution is in a state
//     where work needs to continue (stalled / failed / aborted) —
//     restart it as a fresh attempt while preserving the plan's
//     identity and history count"
//
// All three verbs are plan-id-addressed so the architect doesn't need
// to track per-attempt DAG IDs across architect demote/restore cycles.
// The orchestrator looks up the active DAG via dag_executions and
// performs the right action.
package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/dag"
	"github.com/adalundhe/sylk/core/skills"
)

// cancelOrchestrationRequest carries the plan-centric cancel
// payload. Reason is required for audit; without it the cancel WAL
// row is uninterpretable in postmortems.
type cancelOrchestrationRequest struct {
	PlanID string `json:"plan_id"`
	Reason string `json:"reason"`
}

// cancelOrchestrationResult tells the caller exactly what happened.
// Always non-error: "no active DAG" is a successful outcome (the
// plan is already not running) so the architect doesn't have to
// distinguish "cancel failed" from "nothing to cancel."
type cancelOrchestrationResult struct {
	PlanID            string `json:"plan_id"`
	Cancelled         bool   `json:"cancelled"`
	DAGID             string `json:"dag_id,omitempty"`
	PriorState        string `json:"prior_state,omitempty"`
	NoActiveExecution bool   `json:"no_active_execution"`
	Reason            string `json:"reason,omitempty"`
}

func cancelOrchestrationSkill(o *Orchestrator) *skills.Skill {
	return skills.NewSkill("cancel_orchestration").
		Description("Cancel the active execution for a plan. Plan-centric wrapper around cancel_dag — looks up the active DAG via plan_id and aborts it. Returns no_active_execution=true (success) if the plan has no running DAG.").
		Domain("orchestration").
		Keywords("cancel", "abort", "stop", "plan", "orchestration", "halt").
		Priority(85).
		Usage("Direct-skill. Architect invokes when a state-aware verdict (e.g. user wants to cancel a running plan) demands the plan be stopped without dispatching anything new. Idempotent: cancelling an already-cancelled or never-started plan returns success with no_active_execution=true.").
		StringParam("plan_id", "Plan identifier whose active DAG should be cancelled.", true).
		StringParam("reason", "Reason for cancellation, persisted with the DAG row for audit.", true).
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			var req cancelOrchestrationRequest
			if err := json.Unmarshal(input, &req); err != nil {
				return nil, fmt.Errorf("invalid cancel_orchestration payload: %w", err)
			}
			req.PlanID = strings.TrimSpace(req.PlanID)
			req.Reason = strings.TrimSpace(req.Reason)
			if req.PlanID == "" {
				return nil, fmt.Errorf("cancel_orchestration: plan_id is required")
			}
			if req.Reason == "" {
				return nil, fmt.Errorf("cancel_orchestration: reason is required (audit trail)")
			}
			return o.cancelOrchestrationForPlan(req.PlanID, req.Reason)
		}).
		Build()
}

// cancelOrchestrationForPlan finds the active DAG for plan_id and
// cancels it. "Active" means the most-recent DAG row that is in a
// non-terminal state (queued / running / stalled). Terminal-state
// rows (completed / failed / aborted) are skipped — a successful
// cancel result with no_active_execution=true is the right
// idempotent answer.
func (o *Orchestrator) cancelOrchestrationForPlan(planID, reason string) (*cancelOrchestrationResult, error) {
	if o == nil || o.store == nil {
		return &cancelOrchestrationResult{
			PlanID:            planID,
			NoActiveExecution: true,
			Reason:            reason,
		}, nil
	}
	rows, err := o.store.GetDAGExecutionsByPlan(planID)
	if err != nil {
		return nil, fmt.Errorf("cancel_orchestration: query plan rows: %w", err)
	}
	for _, row := range rows {
		state := strings.ToLower(strings.TrimSpace(row.State))
		switch state {
		case "completed", "succeeded", "success",
			"failed", "failure", "error",
			"aborted", "cancelled", "canceled":
			continue
		}
		if err := o.dagBridge.Cancel(row.ID, reason); err != nil {
			return nil, fmt.Errorf("cancel_orchestration: cancel dag %s: %w", row.ID, err)
		}
		return &cancelOrchestrationResult{
			PlanID:     planID,
			Cancelled:  true,
			DAGID:      row.ID,
			PriorState: row.State,
			Reason:     reason,
		}, nil
	}
	return &cancelOrchestrationResult{
		PlanID:            planID,
		NoActiveExecution: true,
		Reason:            reason,
	}, nil
}

// resumeOrchestrationRequest carries the plan-centric resume
// payload. Mode is advisory: "fresh" forces a new attempt regardless
// of prior state; "auto" lets the orchestrator pick the best action
// based on what it sees ("auto" is the default and recommended call).
type resumeOrchestrationRequest struct {
	PlanID string `json:"plan_id"`
	Mode   string `json:"mode,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// resumeOrchestrationResult tells the caller what happened. Action
// is the discrete decision so callers can branch on it without
// re-classifying state themselves: "redispatched" means a fresh
// DAG was kicked off; "still_running" means the prior attempt is
// alive and we left it alone; "no_plan" means there's no plan to
// resume from.
type resumeOrchestrationResult struct {
	PlanID    string `json:"plan_id"`
	Action    string `json:"action"`
	NewDAGID  string `json:"new_dag_id,omitempty"`
	PriorDAG  string `json:"prior_dag,omitempty"`
	PriorState string `json:"prior_state,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

func resumeOrchestrationSkill(o *Orchestrator) *skills.Skill {
	return skills.NewSkill("resume_orchestration").
		Description("Resume execution for a plan. Plan-centric verb that classifies the plan's current execution state and either (a) leaves a running attempt alone, (b) redispatches a stalled/failed/aborted attempt as a fresh DAG, or (c) reports no_plan if the orchestrator has never seen the plan. Distinct from execute_dag: resume preserves the plan's history count and is intended for state-recovery flows, not new dispatches.").
		Domain("orchestration").
		Keywords("resume", "restart", "continue", "plan", "orchestration", "recover").
		Priority(85).
		Usage("Direct-skill. Architect invokes after a state-aware verdict (e.g. user approved a stalled / failed / aborted plan) when the right action is to continue the existing plan's execution rather than dispatch as if it were brand new. mode=auto is the recommended default; mode=fresh forces a redispatch even if the orchestrator would otherwise leave a running attempt alone.").
		StringParam("plan_id", "Plan identifier whose execution should be resumed.", true).
		StringParam("mode", "Optional. 'auto' (default) lets orchestrator decide; 'fresh' forces a redispatch.", false).
		StringParam("reason", "Optional. Audit context for the resume action.", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var req resumeOrchestrationRequest
			if err := json.Unmarshal(input, &req); err != nil {
				return nil, fmt.Errorf("invalid resume_orchestration payload: %w", err)
			}
			req.PlanID = strings.TrimSpace(req.PlanID)
			req.Mode = strings.ToLower(strings.TrimSpace(req.Mode))
			if req.PlanID == "" {
				return nil, fmt.Errorf("resume_orchestration: plan_id is required")
			}
			if req.Mode == "" {
				req.Mode = "auto"
			}
			return o.resumeOrchestrationForPlan(ctx, req.PlanID, req.Mode, req.Reason)
		}).
		Build()
}

// resumeOrchestrationForPlan classifies the plan's current state and
// performs the right resume action. The action set:
//
//   - "no_plan"     : no DAG row exists for this plan; orchestrator
//                     was never told about it. Caller should treat
//                     this as "use execute_dag instead."
//   - "still_running": active attempt exists; we left it alone.
//                     Caller should not redispatch on top.
//   - "redispatched": stalled/failed/aborted attempt found; we
//                     re-executed the same DAG as a fresh attempt.
//                     The new DAG ID is in NewDAGID.
//
// mode=fresh skips the still_running short-circuit and ALWAYS
// redispatches. Caller should use this only when the user explicitly
// wants a parallel attempt (rare).
func (o *Orchestrator) resumeOrchestrationForPlan(ctx context.Context, planID, mode, reason string) (*resumeOrchestrationResult, error) {
	if o == nil || o.store == nil {
		return &resumeOrchestrationResult{
			PlanID: planID,
			Action: "no_plan",
			Reason: reason,
		}, nil
	}
	rows, err := o.store.GetDAGExecutionsByPlan(planID)
	if err != nil {
		return nil, fmt.Errorf("resume_orchestration: query plan rows: %w", err)
	}
	if len(rows) == 0 {
		return &resumeOrchestrationResult{
			PlanID: planID,
			Action: "no_plan",
			Reason: reason,
		}, nil
	}
	// Most-recent row is the current attempt; classify it.
	current := rows[0]
	currentStatus := classifyPlanState(current)
	if currentStatus == "running" && mode != "fresh" {
		return &resumeOrchestrationResult{
			PlanID:     planID,
			Action:     "still_running",
			PriorDAG:   current.ID,
			PriorState: current.State,
			Reason:     reason,
		}, nil
	}
	if currentStatus == "completed" && mode != "fresh" {
		// Completed DAG: not a resume case. Caller should ask the
		// user explicitly before duplicating completed work.
		return &resumeOrchestrationResult{
			PlanID:     planID,
			Action:     "already_completed",
			PriorDAG:   current.ID,
			PriorState: current.State,
			Reason:     reason,
		}, nil
	}
	d := &dag.DAG{}
	if err := d.UnmarshalJSON([]byte(current.DAGJSON)); err != nil {
		return nil, fmt.Errorf("resume_orchestration: decode prior dag: %w", err)
	}
	newDAGID, err := o.dagBridge.Execute(ctx, d, planID, current.SessionID)
	if err != nil {
		return nil, fmt.Errorf("resume_orchestration: redispatch: %w", err)
	}
	return &resumeOrchestrationResult{
		PlanID:     planID,
		Action:     "redispatched",
		NewDAGID:   newDAGID,
		PriorDAG:   current.ID,
		PriorState: current.State,
		Reason:     reason,
	}, nil
}

// resumeOrchestrationDeadline bounds how long the orchestrator
// considers waiting for in-flight cancel acknowledgement before
// returning. Currently unused — Cancel() is synchronous — but kept
// as a constant so the future async-cancel path has a documented
// budget rather than an arbitrary literal.
const resumeOrchestrationDeadline = 5 * time.Second

var _ = resumeOrchestrationDeadline
