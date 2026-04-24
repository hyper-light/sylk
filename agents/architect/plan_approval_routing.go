// State-aware verdict routing for approved plans.
//
// The dialog already showed the user a snapshot of orchestrator state
// (via the freshness audit's OrchestratorStateHint), but the user can
// spend minutes staring at the dialog before clicking — and the
// orchestrator keeps working. So when a verdict comes back as Approve
// we re-query the orchestrator ONCE and branch on what it tells us,
// rather than blindly calling actOnAccept which would redispatch on
// top of a running/completed DAG.
//
// Branching matrix (minimal, user-first, additive):
//
//   - none / queued / aborted → dispatch as normal. Current behavior.
//   - stalled                  → dispatch + notify ("restarting stalled
//     execution"). The orchestrator's redispatch supersedes the
//     stalled DAG.
//   - failed                   → dispatch + notify (explicit
//     "retrying after prior failure"). User saw the failure in the
//     dialog; Approve means "try again."
//   - running                  → DO NOT dispatch. Notify the user
//     their existing execution is continuing. A redispatch here would
//     create a parallel attempt with undefined interleaving.
//   - completed                → DO NOT dispatch. Notify the user the
//     plan already completed in a prior run and ask whether they
//     want a fresh run.
//
// Philosophy: the user's Approve click is interpreted charitably. When
// the orchestrator says "already running" or "already done", the
// Approve signal may have been "OK, keep going" rather than "start a
// new attempt". Rather than guess, we stop the dispatch and ask — a
// short pause is cheap; a duplicate run is expensive and confusing.
package architect

import (
	"context"
	"fmt"

	"github.com/adalundhe/sylk/core/claims"
)

// approvalRoutingAction is the discrete decision the routing step
// returns after observing orchestrator state. Pure-helper friendly —
// the live routing code maps an action to dispatch + notification,
// while tests pin the action matrix without needing bus plumbing.
type approvalRoutingAction string

const (
	// approvalActionDispatchSilent: dispatch the plan with no
	// state-context notification (the normal happy path).
	approvalActionDispatchSilent approvalRoutingAction = "dispatch_silent"

	// approvalActionDispatchWithNotice: dispatch the plan AND publish
	// a context notice ("retrying after failure", "restarting after
	// stall"). The notice contextualizes the user's Approve click.
	approvalActionDispatchWithNotice approvalRoutingAction = "dispatch_with_notice"

	// approvalActionNotifyOnly: do NOT dispatch. Publish a notice
	// asking the user to confirm before duplicating an in-flight or
	// already-completed run.
	approvalActionNotifyOnly approvalRoutingAction = "notify_only"
)

// approvalRoutingDecision pairs an action with the user-facing
// notice text. Built by classifyApprovalRouting so the live code can
// publish + dispatch in a uniform shape, and so the test matrix can
// pin both (action, notice) pairs together.
type approvalRoutingDecision struct {
	Action approvalRoutingAction
	Notice string
}

// classifyApprovalRouting is the pure decision function: given an
// orchestrator state, return what the architect should do with an
// Approve verdict. State==nil is treated identically to status=none
// (no orchestrator record) so the architect-without-orchestrator
// case dispatches normally.
func classifyApprovalRouting(state *orchestratorPlanState) approvalRoutingDecision {
	status := "none"
	if state != nil {
		status = state.Status
	}
	switch status {
	case "running":
		return approvalRoutingDecision{
			Action: approvalActionNotifyOnly,
			Notice: fmt.Sprintf(
				"This plan is already running on the orchestrator (%d/%d nodes complete). I'm letting the existing run continue rather than redispatching. If you want to cancel and restart, say so and I'll coordinate that.",
				safeNodesSucceeded(state), safeNodesTotal(state),
			),
		}
	case "completed":
		return approvalRoutingDecision{
			Action: approvalActionNotifyOnly,
			Notice: "This plan already completed in a prior run. Rather than start a duplicate, let me know if you want me to run it again from scratch or move on to something else.",
		}
	case "stalled":
		return approvalRoutingDecision{
			Action: approvalActionDispatchWithNotice,
			Notice: fmt.Sprintf(
				"This plan was stalled (no progress for %ds). Re-dispatching as a fresh execution.",
				safeStalledSeconds(state),
			),
		}
	case "failed":
		return approvalRoutingDecision{
			Action: approvalActionDispatchWithNotice,
			Notice: "Retrying this plan after its prior failure. I'll let you know how it goes.",
		}
	default:
		// "none", "queued", "aborted", or any unknown status → treat
		// as fresh dispatch. Unknown statuses fall through rather
		// than blocking so a future orchestrator state enum
		// addition doesn't silently strand approvals.
		return approvalRoutingDecision{
			Action: approvalActionDispatchSilent,
		}
	}
}

// routeApprovedPlanByOrchestratorState is the dispatch-time branch
// on orchestrator state. Returns nil on notify-only branches so the
// caller treats them as successful completions of the continuation —
// the user will steer the next step via a follow-up message.
//
// For stalled/failed branches the routing prefers resume_orchestration
// over plain dispatch — that's the verb that semantically matches
// "continue this plan's execution" rather than "dispatch a new plan."
// If resume returns no_plan / already_completed (rare race), the
// architect falls back to actOnAccept so the plan still moves forward.
func (a *Architect) routeApprovedPlanByOrchestratorState(ctx context.Context, plan *DesignPlan) error {
	if a == nil || plan == nil {
		return fmt.Errorf("architect or plan is nil")
	}
	state := a.queryOrchestratorPlanState(ctx, plan.ID)
	decision := classifyApprovalRouting(state)
	status := orchestratorStatusOrNone(state)
	architectDebugLog().Info("routeApprovedPlanByOrchestratorState: DECIDED",
		"plan_id", plan.ID,
		"status", status,
		"action", string(decision.Action))

	// Approval routing testament.
	a.architectSubmitTestament(ctx, a.architectTestament(
		fmt.Sprintf("Routing approved plan %s: %s (orch status: %s)", plan.ID, decision.Action, status),
		"committed",
		[]*claims.Artifact{
			a.architectArtifact("orchestrator_status", status),
			a.architectArtifact("routing_action", string(decision.Action)),
			a.architectArtifact("notice", decision.Notice),
		},
	))

	if decision.Notice != "" {
		a.publishNotificationPush(decision.Notice)
	}
	switch decision.Action {
	case approvalActionNotifyOnly:
		return nil
	case approvalActionDispatchWithNotice:
		// stalled / failed → resume verb is semantically correct.
		// Falls back to dispatch on any resume-side problem so the
		// user's Approve click never strands the plan.
		return a.resumeOrFallbackDispatch(ctx, plan, status)
	case approvalActionDispatchSilent:
		return a.dispatchAfterApproval(ctx, plan)
	default:
		return a.dispatchAfterApproval(ctx, plan)
	}
}

// resumeOrFallbackDispatch tries the orchestrator's resume verb
// first, then falls back to ordinary dispatch if resume returns an
// outcome that means "you should have dispatched instead" (no_plan)
// or fails entirely. The fallback exists so a transient orchestrator
// hiccup never strands a user-approved plan.
func (a *Architect) resumeOrFallbackDispatch(ctx context.Context, plan *DesignPlan, priorStatus string) error {
	reason := fmt.Sprintf("user-approved resume after %s", priorStatus)
	result, err := a.resumeOrchestrationViaOrchestrator(ctx, plan.ID, "auto", reason)
	if err != nil {
		a.architectSubmitTestament(ctx, a.architectTestament(
			fmt.Sprintf("Resume failed for plan %s — falling back to dispatch", plan.ID),
			"committed",
			[]*claims.Artifact{
				a.architectArtifact("prior_status", priorStatus),
				a.architectArtifact("error", err.Error()),
				a.architectArtifact("fell_back_to_dispatch", "true"),
			},
		))
		architectDebugLog().Info("resumeOrFallbackDispatch: RESUME_FAILED_FALLBACK",
			"plan_id", plan.ID,
			"prior_status", priorStatus,
			"error", err.Error())
		return a.dispatchAfterApproval(ctx, plan)
	}
	// Resume testament.
	resumeAction := "unknown"
	if result != nil {
		resumeAction = result.Action
	}
	a.architectSubmitTestament(ctx, a.architectTestamentWithSubject(
		fmt.Sprintf("Resume result: %s for plan %s", resumeAction, plan.ID),
		"committed", "orchestrator",
		[]*claims.Artifact{
			a.architectArtifact("prior_status", priorStatus),
			a.architectArtifact("resume_action", resumeAction),
			a.architectArtifact("fell_back_to_dispatch", "false"),
		},
	))

	switch result.Action {
	case "redispatched":
		architectDebugLog().Info("resumeOrFallbackDispatch: REDISPATCHED",
			"plan_id", plan.ID,
			"new_dag_id", result.NewDAGID,
			"prior_dag", result.PriorDAG)
		return nil
	case "still_running":
		// Race: state changed between query and resume. The
		// orchestrator says it's running now; honor that.
		a.publishNotificationPush("The orchestrator reports this plan is now running. Letting it continue.")
		return nil
	case "already_completed":
		a.publishNotificationPush("The orchestrator reports this plan completed since you approved. Nothing to redispatch.")
		return nil
	case "no_plan":
		// Orchestrator never saw this plan — fall back to fresh
		// dispatch so the plan actually moves forward.
		architectDebugLog().Info("resumeOrFallbackDispatch: NO_PLAN_FALLBACK",
			"plan_id", plan.ID)
		return a.dispatchAfterApproval(ctx, plan)
	default:
		architectDebugLog().Info("resumeOrFallbackDispatch: UNKNOWN_ACTION_FALLBACK",
			"plan_id", plan.ID,
			"action", result.Action)
		return a.dispatchAfterApproval(ctx, plan)
	}
}

// dispatchAfterApproval is the normal post-verdict dispatch call. All
// state branches that DO dispatch route through this helper so the
// bookkeeping (error surfacing, notification on failure) stays in one
// place.
func (a *Architect) dispatchAfterApproval(ctx context.Context, plan *DesignPlan) error {
	if _, err := a.actOnAccept(ctx, plan); err != nil {
		a.publishNotificationPush("I couldn't dispatch the approved plan: " + err.Error())
		return err
	}
	return nil
}

// safeStalledSeconds returns the StalledForSeconds field or 0 when
// state is nil or the field is absent. Tiny helper but avoids the
// nil-deref pattern at every call site.
func safeStalledSeconds(state *orchestratorPlanState) int64 {
	if state == nil {
		return 0
	}
	return state.StalledForSeconds
}

func safeNodesSucceeded(state *orchestratorPlanState) int {
	if state == nil {
		return 0
	}
	return state.NodesSucceeded
}

func safeNodesTotal(state *orchestratorPlanState) int {
	if state == nil {
		return 0
	}
	return state.NodesTotal
}

func orchestratorStatusOrNone(state *orchestratorPlanState) string {
	if state == nil {
		return "none"
	}
	return state.Status
}
