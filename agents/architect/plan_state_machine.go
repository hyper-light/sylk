package architect

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/adalundhe/sylk/agents/guide"
)

// Sentinel errors for plan state machine transitions.
var (
	ErrIllegalTransition     = errors.New("illegal plan state transition")
	ErrTransitionGuardFailed = errors.New("transition guard rejected")
	ErrTransitionRace        = errors.New("concurrent transition race")
)

// planTransitions defines the legal state graph. Every non-terminal state
// also has an edge to Failed. Terminal states (Completed, Failed) have no
// outgoing edges.
var planTransitions = map[PlanStatus][]PlanStatus{
	PlanStatusPending:       {PlanStatusAnalyzing, PlanStatusFailed, PlanStatusSuperseded},
	PlanStatusAnalyzing:     {PlanStatusConsulting, PlanStatusDesigning, PlanStatusFailed, PlanStatusSuperseded},
	PlanStatusConsulting:    {PlanStatusClarifying, PlanStatusDesigning, PlanStatusFailed, PlanStatusSuperseded},
	PlanStatusClarifying:    {PlanStatusPending, PlanStatusFailed, PlanStatusSuperseded},
	PlanStatusDesigning:     {PlanStatusGenerating, PlanStatusFailed, PlanStatusSuperseded},
	PlanStatusGenerating:    {PlanStatusOrchestrating, PlanStatusFailed, PlanStatusSuperseded},
	PlanStatusOrchestrating: {PlanStatusReady, PlanStatusExecuting, PlanStatusFailed, PlanStatusSuperseded},
	PlanStatusReady:         {PlanStatusOrchestrating, PlanStatusExecuting, PlanStatusPending, PlanStatusFailed, PlanStatusSuperseded},
	PlanStatusExecuting:     {PlanStatusCompleted, PlanStatusFailed, PlanStatusPending},
	PlanStatusCompleted:     {},
	PlanStatusFailed:        {},
	PlanStatusSuperseded:    {},
}

// guardKey identifies a specific from→to edge for guard registration.
type guardKey struct {
	from PlanStatus
	to   PlanStatus
}

// TransitionGuard returns nil to allow, non-nil error to reject. Guards
// receive the plan for read-only inspection. Must not mutate the plan.
type TransitionGuard func(plan *DesignPlan) error

// PlanStateChangeEvent is emitted to callbacks after a successful CAS.
type PlanStateChangeEvent struct {
	PlanID string
	From   PlanStatus
	To     PlanStatus
	Epoch  uint64
}

// PlanStateChangeCallback is invoked after a successful transition. Callbacks
// fire AFTER the CAS succeeds, OUTSIDE all locks. Must not block or acquire
// state machine locks.
type PlanStateChangeCallback func(event PlanStateChangeEvent)

// PlanStateMachine enforces the plan lifecycle transition table using atomic
// compare-and-swap for lock-free state changes. Guards validate preconditions
// before the CAS. Callbacks fire after.
type PlanStateMachine struct {
	planID     string
	state      atomic.Int32
	epoch      atomic.Uint64
	callbackMu sync.Mutex
	callbacks  []PlanStateChangeCallback
	guardMu    sync.RWMutex
	guards     map[guardKey][]TransitionGuard
}

// NewPlanStateMachine creates a state machine with the given initial state
// and registers default guards.
func NewPlanStateMachine(planID string, initial PlanStatus) *PlanStateMachine {
	sm := &PlanStateMachine{
		planID: planID,
		guards: map[guardKey][]TransitionGuard{},
	}
	sm.state.Store(int32(initial))
	sm.registerDefaultGuards()
	return sm
}

// NewPlanStateMachineWithEpoch creates a state machine with the given initial
// state and epoch. Used for restoring persisted plans that carry an epoch.
func NewPlanStateMachineWithEpoch(planID string, initial PlanStatus, epoch uint64) *PlanStateMachine {
	sm := NewPlanStateMachine(planID, initial)
	sm.epoch.Store(epoch)
	return sm
}

// Epoch returns the monotonic version counter via atomic load.
func (sm *PlanStateMachine) Epoch() uint64 {
	return sm.epoch.Load()
}

// BumpEpoch advances the monotonic plan version without changing lifecycle
// state. Revisions of a Ready plan need this because the review artifact and
// approval gate are versioned even when the plan remains in Ready.
func (sm *PlanStateMachine) BumpEpoch() uint64 {
	return sm.epoch.Add(1)
}

// State returns the current plan status via atomic load.
func (sm *PlanStateMachine) State() PlanStatus {
	return PlanStatus(sm.state.Load())
}

// IsTerminal returns true for Completed or Failed.
func (sm *PlanStateMachine) IsTerminal() bool {
	s := sm.State()
	return s == PlanStatusCompleted || s == PlanStatusFailed
}

// TransitionTo validates the transition, runs guards, and performs an atomic
// CAS. Returns nil on success.
func (sm *PlanStateMachine) TransitionTo(to PlanStatus, plan *DesignPlan) error {
	from := sm.State()
	if !isValidPlanTransition(from, to) {
		return fmt.Errorf("%w: %s -> %s (plan %s)", ErrIllegalTransition, from, to, sm.planID)
	}
	if err := sm.runGuards(from, to, plan); err != nil {
		return err
	}
	if !sm.state.CompareAndSwap(int32(from), int32(to)) {
		return fmt.Errorf("%w: %s -> %s (plan %s)", ErrTransitionRace, from, to, sm.planID)
	}
	newEpoch := sm.epoch.Add(1)
	sm.fireCallbacks(PlanStateChangeEvent{PlanID: sm.planID, From: from, To: to, Epoch: newEpoch})
	return nil
}

// TransitionToFailed is a convenience for transitioning any non-terminal state
// to Failed. The error is logged in the event but not stored by the SM.
func (sm *PlanStateMachine) TransitionToFailed(plan *DesignPlan, reason error) error {
	_ = reason // available for guard inspection via plan.Error
	return sm.TransitionTo(PlanStatusFailed, plan)
}

// OnStateChange registers a callback fired after successful transitions.
func (sm *PlanStateMachine) OnStateChange(cb PlanStateChangeCallback) {
	if cb == nil {
		return
	}
	sm.callbackMu.Lock()
	sm.callbacks = append(sm.callbacks, cb)
	sm.callbackMu.Unlock()
}

// AddGuard registers a guard for a specific from→to edge.
func (sm *PlanStateMachine) AddGuard(from, to PlanStatus, guard TransitionGuard) {
	if guard == nil {
		return
	}
	key := guardKey{from: from, to: to}
	sm.guardMu.Lock()
	sm.guards[key] = append(sm.guards[key], guard)
	sm.guardMu.Unlock()
}

// isValidPlanTransition checks the transition table. Standalone and testable.
func isValidPlanTransition(from, to PlanStatus) bool {
	targets, ok := planTransitions[from]
	if !ok {
		return false
	}
	for _, t := range targets {
		if t == to {
			return true
		}
	}
	return false
}

// runGuards executes all registered guards for the from→to edge.
func (sm *PlanStateMachine) runGuards(from, to PlanStatus, plan *DesignPlan) error {
	key := guardKey{from: from, to: to}
	sm.guardMu.RLock()
	guards := sm.guards[key]
	sm.guardMu.RUnlock()
	for _, g := range guards {
		if err := g(plan); err != nil {
			return fmt.Errorf("%w: %s -> %s: %v", ErrTransitionGuardFailed, from, to, err)
		}
	}
	return nil
}

// fireCallbacks invokes all registered callbacks outside any locks.
func (sm *PlanStateMachine) fireCallbacks(event PlanStateChangeEvent) {
	sm.callbackMu.Lock()
	cbs := make([]PlanStateChangeCallback, len(sm.callbacks))
	copy(cbs, sm.callbacks)
	sm.callbackMu.Unlock()
	for _, cb := range cbs {
		cb(event)
	}
}

// registerDefaultGuards sets up the built-in guards from the plan.
func (sm *PlanStateMachine) registerDefaultGuards() {
	sm.AddGuard(PlanStatusReady, PlanStatusExecuting, guardReadyToExecuting)
	sm.AddGuard(PlanStatusConsulting, PlanStatusClarifying, guardConsultingToClarifying)
	sm.AddGuard(PlanStatusAnalyzing, PlanStatusDesigning, guardDirectAnalyzeToDesigning)
	sm.AddGuard(PlanStatusConsulting, PlanStatusDesigning, guardConsultingToDesigning)
}

// guardReadyToExecuting ensures the plan passes execution validation.
func guardReadyToExecuting(plan *DesignPlan) error {
	return validatePlanForExecution(plan)
}

// guardConsultingToClarifying ensures clarification questions exist.
func guardConsultingToClarifying(plan *DesignPlan) error {
	if plan == nil || len(plan.ClarificationQuestions) == 0 {
		return fmt.Errorf("clarification questions must be non-empty")
	}
	return nil
}

// guardConsultingToDesigning ensures no pending clarification questions.
func guardConsultingToDesigning(plan *DesignPlan) error {
	if plan != nil && len(plan.ClarificationQuestions) > 0 {
		return fmt.Errorf("clarification questions must be resolved before designing")
	}
	return nil
}

// guardDirectAnalyzeToDesigning allows the planner to skip the optional
// consulting stage when it already has enough evidence to design, while still
// refusing to design if unresolved clarification questions were recorded.
func guardDirectAnalyzeToDesigning(plan *DesignPlan) error {
	return guardConsultingToDesigning(plan)
}

// readyPlanDirective builds the ResponseDirective for a plan in Ready state.
// Extracted here so both the derived method and feedbackReadyDirective share
// the same construction logic.
func readyPlanDirective(planID string, epoch ...uint64) *guide.ResponseDirective {
	metadata := map[string]any{"plan_id": planID}
	if len(epoch) > 0 {
		metadata["epoch"] = epoch[0]
	}
	return &guide.ResponseDirective{
		Phase:    guide.PhasePlanApproval,
		AgentID:  "architect",
		Metadata: metadata,
		TTL:      ReadyPlanMaxAge,
	}
}
