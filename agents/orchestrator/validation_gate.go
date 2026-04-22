package orchestrator

import (
	"context"
	"strings"
	"sync"
)

// dispatchHoldGate enforces execution holds at the dag_bridge dispatch
// permit chokepoint. It is a thin projection over two sources of truth:
//
//  1. The store's execution_holds table — authoritative for validation
//     holds. The gate queries store.DAGIsHeld on every wait iteration
//     and subscribes to store.HoldsNotifyChannel for wake-up. No
//     in-memory state shadows the store's view.
//
//  2. An in-memory per-DAG refcount for command approval holds. These
//     are ephemeral (begin on approval request, resolve on user
//     response), tied to the active DAG, and never cross plan
//     boundaries — so persisting them adds overhead without the
//     correctness payoff that motivated moving validation holds to the
//     store.
//
// There is no `activate(sessionID)` method and no `isActive(sessionID)`
// flag. The session-wide "everything is blocked" state used to be
// reconstructed on bootstrap from a store read, which drifted from
// the store whenever a hold resolved without the in-memory flag being
// cleared. Supersede-on-new-plan plus store-as-single-source-of-truth
// eliminates both the drift source and the bootstrap leak. See
// docs/EXECUTION_HOLDS.md.
type dispatchHoldGate struct {
	store *Store

	mu       sync.Mutex
	sessions map[string]*dispatchHoldApprovalState
}

// dispatchHoldApprovalState tracks in-memory command approval holds
// for a single session. validation holds live in the store, not here.
type dispatchHoldApprovalState struct {
	// dagHoldRefs counts active approval holds per DAG. A DAG is held
	// if the count is > 0. Refs increase in activateDAG and decrease
	// in resolveHold.
	dagHoldRefs map[string]int
	// dagHolds maps holdID → dagID so resolveHold can find which DAG
	// to decrement without the caller repeating the dagID.
	dagHolds map[string]string
	// changed closes whenever approval state transitions, unblocking
	// waiters. Store-backed wakeups come through HoldsNotifyChannel
	// instead.
	changed chan struct{}
}

func newDispatchHoldGate(store *Store) *dispatchHoldGate {
	return &dispatchHoldGate{
		store:    store,
		sessions: make(map[string]*dispatchHoldApprovalState),
	}
}

func (g *dispatchHoldGate) ensure(sessionID string) *dispatchHoldApprovalState {
	state := g.sessions[sessionID]
	if state != nil {
		return state
	}
	state = &dispatchHoldApprovalState{
		dagHoldRefs: make(map[string]int),
		dagHolds:    make(map[string]string),
		changed:     make(chan struct{}),
	}
	g.sessions[sessionID] = state
	return state
}

// activateDAG registers a per-DAG command approval hold (in-memory
// only). Validation holds don't use this path — they go through the
// store and get picked up by DAGIsHeld on the next wait iteration.
func (g *dispatchHoldGate) activateDAG(sessionID, dagID, holdID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	state := g.ensure(sessionID)
	dagID = strings.TrimSpace(dagID)
	holdID = strings.TrimSpace(holdID)
	if dagID == "" || holdID == "" {
		return
	}
	if existing, ok := state.dagHolds[holdID]; ok {
		if existing == dagID {
			return
		}
	}
	state.dagHolds[holdID] = dagID
	state.dagHoldRefs[dagID]++
	old := state.changed
	state.changed = make(chan struct{})
	close(old)
}

// resolveHold releases a per-DAG command approval hold. Safe to call
// with an unknown holdID — that's the expected no-op on duplicate
// approval callbacks.
func (g *dispatchHoldGate) resolveHold(sessionID, holdID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	state := g.ensure(sessionID)
	holdID = strings.TrimSpace(holdID)
	if holdID == "" {
		return
	}
	dagID, ok := state.dagHolds[holdID]
	if !ok {
		return
	}
	delete(state.dagHolds, holdID)
	if dagID != "" {
		if refs := state.dagHoldRefs[dagID] - 1; refs > 0 {
			state.dagHoldRefs[dagID] = refs
		} else {
			delete(state.dagHoldRefs, dagID)
		}
	}
	old := state.changed
	state.changed = make(chan struct{})
	close(old)
}

// isHeld reports whether dagID is currently blocked for dispatch in
// (sessionID, planID). Combines the store's validation-hold view with
// the in-memory approval refcount. Used by the execution-hold
// shielding path (dagBridge.isExecutionHeld); wait() is what blocks
// the dispatch permit.
func (g *dispatchHoldGate) isHeld(sessionID, planID, dagID string) bool {
	dagID = strings.TrimSpace(dagID)
	if g.store != nil {
		if held, _, _, err := g.store.DAGIsHeld(context.Background(), sessionID, planID, dagID); err == nil && held {
			return true
		}
	}
	if dagID == "" {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	state := g.ensure(sessionID)
	return state.dagHoldRefs[dagID] > 0
}

// wait blocks until dagID is clear to dispatch for (sessionID,
// planID) — both from the store's perspective and from any pending
// in-memory approval holds. Returns immediately if already clear.
// Wake-ups come from two sources, selected in parallel: the store's
// HoldsNotifyChannel (any execution_holds write for the session) and
// the gate's own `changed` channel (approval hold transitions). No
// timer, no polling — transitions in either source unblock the wait.
func (g *dispatchHoldGate) wait(ctx context.Context, sessionID, planID, dagID string) error {
	dagID = strings.TrimSpace(dagID)
	for {
		// Snapshot the in-memory approval refcount + the local
		// `changed` channel.
		g.mu.Lock()
		state := g.ensure(sessionID)
		dagHeld := dagID != "" && state.dagHoldRefs[dagID] > 0
		changed := state.changed
		g.mu.Unlock()

		// Subscribe to store notifications *before* the store read to
		// avoid missing a write that lands between the read and the
		// select — the channel buffers the happens-before edge.
		var storeNotify <-chan struct{}
		if g.store != nil {
			storeNotify = g.store.HoldsNotifyChannel(sessionID)
		}

		storeHeld := false
		if g.store != nil {
			held, _, _, err := g.store.DAGIsHeld(ctx, sessionID, planID, dagID)
			if err != nil {
				return err
			}
			storeHeld = held
		}

		if !storeHeld && !dagHeld {
			return nil
		}

		select {
		case <-changed:
		case <-storeNotify:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
