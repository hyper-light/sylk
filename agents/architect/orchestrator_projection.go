// Architect-side projection of orchestrator plan execution state.
//
// Built incrementally from two existing event streams:
//   - PlanHandoffReceiptUpdate control-plane messages (already routed
//     through applyPlanHandoffReceiptUpdate). Carries accepted /
//     dag_built / submitted / running / failed transitions and the
//     dag_id ↔ plan_id mapping needed to interpret dag.status events.
//   - dag.status topic. Carries terminal succeeded / failed / aborted
//     events emitted by the orchestrator's DAG bridge.
//
// Replaces the synchronous plan_state_query RPC on the approval hot
// path. The RPC pattern bounded latency; the projection bounds
// staleness — same correctness profile (eventually-consistent) with
// no per-approval round-trip. Empty projection (no events seen yet)
// signals "no orchestrator context" exactly like the timeout path
// the sync RPC takes today.
package architect

import (
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
)

// projectedStalledThreshold mirrors the orchestrator's
// planStateStalledThreshold so the architect's locally-derived stall
// classification matches what plan_state_query would have reported.
const projectedStalledThreshold = 5 * time.Minute

type orchestratorProjection struct {
	mu     sync.RWMutex
	byPlan map[string]*projectedPlanState
	byDAG  map[string]string // dag_id → plan_id (set when a receipt update carries a DAGID)
}

type projectedPlanState struct {
	PlanID         string
	SessionID      string
	DAGID          string
	Status         string
	NodesTotal     int
	NodesSucceeded int
	NodesFailed    int
	NodesSkipped   int
	NodesRemaining int
	StartedAt      *time.Time
	CompletedAt    *time.Time
	LastObservedAt *time.Time
	Error          string
	UpdatedAt      time.Time
}

func newOrchestratorProjection() *orchestratorProjection {
	return &orchestratorProjection{
		byPlan: make(map[string]*projectedPlanState),
		byDAG:  make(map[string]string),
	}
}

// lookup returns a snapshot copy of the projected state for planID, or
// nil if no events have populated it yet. Snapshot is by-value so
// callers can't mutate projection internals.
func (p *orchestratorProjection) lookup(planID string) *projectedPlanState {
	if p == nil {
		return nil
	}
	planID = strings.TrimSpace(planID)
	if planID == "" {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if s, ok := p.byPlan[planID]; ok {
		out := *s
		return &out
	}
	return nil
}

// applyReceiptUpdate folds a PlanHandoffReceiptUpdate into the
// projection. Idempotent — same update applied twice yields the same
// state. Tolerates missing/zero fields.
func (p *orchestratorProjection) applyReceiptUpdate(update *agentshared.PlanHandoffReceiptUpdate) {
	if p == nil || update == nil || update.Receipt == nil {
		return
	}
	planID := strings.TrimSpace(update.PlanID)
	if planID == "" {
		planID = strings.TrimSpace(update.Receipt.PlanID)
	}
	if planID == "" {
		return
	}
	receipt := update.Receipt
	p.mu.Lock()
	defer p.mu.Unlock()
	state, ok := p.byPlan[planID]
	if !ok {
		state = &projectedPlanState{PlanID: planID}
		p.byPlan[planID] = state
	}
	if receipt.SessionID != "" {
		state.SessionID = receipt.SessionID
	}
	if dagID := strings.TrimSpace(receipt.DAGID); dagID != "" {
		state.DAGID = dagID
		p.byDAG[dagID] = planID
	}
	if mapped := mapReceiptStatusToProjected(receipt.Status); mapped != "" {
		state.Status = mapped
	}
	if !receipt.RunningAt.IsZero() {
		t := receipt.RunningAt
		if state.StartedAt == nil || t.Before(*state.StartedAt) {
			state.StartedAt = &t
		}
	} else if !receipt.SubmittedAt.IsZero() && state.StartedAt == nil {
		t := receipt.SubmittedAt
		state.StartedAt = &t
	}
	if !receipt.UpdatedAt.IsZero() {
		state.LastObservedAt = &receipt.UpdatedAt
	}
	if receipt.ErrorText != "" {
		state.Error = receipt.ErrorText
	}
	state.UpdatedAt = time.Now()
}

func mapReceiptStatusToProjected(rs agentshared.PlanHandoffReceiptStatus) string {
	switch rs {
	case agentshared.PlanHandoffReceiptStatusAccepted,
		agentshared.PlanHandoffReceiptStatusDAGBuilt,
		agentshared.PlanHandoffReceiptStatusSubmitted:
		return "queued"
	case agentshared.PlanHandoffReceiptStatusRunning:
		return "running"
	case agentshared.PlanHandoffReceiptStatusFailed:
		return "failed"
	}
	return ""
}

// applyDAGStatus folds a dag.status terminal event into the
// projection. dag_id is resolved to plan_id via byDAG; events for
// unknown dag_ids are dropped (the architect didn't author the plan
// or hasn't yet seen its dag_built receipt).
func (p *orchestratorProjection) applyDAGStatus(dagID, state, errMsg string) {
	if p == nil {
		return
	}
	dagID = strings.TrimSpace(dagID)
	if dagID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	planID, ok := p.byDAG[dagID]
	if !ok {
		return
	}
	s, ok := p.byPlan[planID]
	if !ok {
		return
	}
	now := time.Now()
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "succeeded", "success", "completed":
		s.Status = "completed"
		s.CompletedAt = &now
	case "failed", "failure", "error":
		s.Status = "failed"
		if errMsg != "" {
			s.Error = errMsg
		}
		s.CompletedAt = &now
	case "aborted", "cancelled", "canceled":
		s.Status = "aborted"
		s.CompletedAt = &now
	default:
		// Non-terminal states (e.g. layer_completed) update progress
		// but don't change status.
	}
	s.LastObservedAt = &now
	s.UpdatedAt = now
}

// stallAdjustedStatus returns the projected status with stall
// classification applied. Read-only — does not mutate the projection.
// Mirrors the orchestrator's classifyPlanState stall logic.
func (s *projectedPlanState) stallAdjustedStatus() string {
	if s == nil {
		return "none"
	}
	if s.Status != "running" {
		return s.Status
	}
	floor := time.Now()
	if s.StartedAt != nil {
		floor = *s.StartedAt
	}
	if s.LastObservedAt != nil && s.LastObservedAt.After(floor) {
		floor = *s.LastObservedAt
	}
	if time.Since(floor) >= projectedStalledThreshold {
		return "stalled"
	}
	return "running"
}

// handleDAGStatusEvent decodes a dag.status bus message and folds it
// into the projection. Best-effort — malformed payloads are dropped
// silently rather than failing the subscription.
func (a *Architect) handleDAGStatusEvent(msg *guide.Message) error {
	if a == nil || msg == nil || msg.Payload == nil {
		return nil
	}
	payload, ok := msg.Payload.(map[string]any)
	if !ok {
		return nil
	}
	dagID, _ := payload["dag_id"].(string)
	state, _ := payload["state"].(string)
	errMsg, _ := payload["error"].(string)
	a.orchestratorProjection.applyDAGStatus(dagID, state, errMsg)
	return nil
}

// asOrchestratorPlanState converts the projection to the type that
// existing callers (classifyApprovalRouting, freshness audit) consume.
// Returns nil if the projection has nothing for this plan.
func (p *orchestratorProjection) asOrchestratorPlanState(planID string) *orchestratorPlanState {
	snap := p.lookup(planID)
	if snap == nil {
		return nil
	}
	stalled := int64(0)
	status := snap.stallAdjustedStatus()
	if status == "stalled" {
		floor := time.Now()
		if snap.StartedAt != nil {
			floor = *snap.StartedAt
		}
		if snap.LastObservedAt != nil && snap.LastObservedAt.After(floor) {
			floor = *snap.LastObservedAt
		}
		stalled = int64(time.Since(floor).Seconds())
	}
	return &orchestratorPlanState{
		PlanID:            snap.PlanID,
		Status:            status,
		DAGID:             snap.DAGID,
		SessionID:         snap.SessionID,
		NodesTotal:        snap.NodesTotal,
		NodesSucceeded:    snap.NodesSucceeded,
		NodesFailed:       snap.NodesFailed,
		NodesSkipped:      snap.NodesSkipped,
		NodesRemaining:    snap.NodesRemaining,
		StartedAt:         snap.StartedAt,
		CompletedAt:       snap.CompletedAt,
		LastObservedAt:    snap.LastObservedAt,
		StalledForSeconds: stalled,
		Error:             snap.Error,
	}
}
