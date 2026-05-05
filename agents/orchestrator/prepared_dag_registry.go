// Prepared DAG registry — orchestrator-side store of DAGs that have
// been prepared (verified attestation, built DAG, recorded WAL/SQLite,
// pods pre-activated, claims posted) but not yet submitted to the
// scheduler. Populated by Phase=Prepare ingest, drained by
// Phase=ExecutePrepared (submit) or Phase=DiscardPrepared (drop).
//
// Two-phase design context: the architect publishes a prepare-only
// handoff at plan-finalization (immediately after Guardian preflight,
// before the LLM final-text-turn) so the orchestrator's prep cost
// overlaps with user approval-dialog review time. On approve,
// ExecutePrepared looks up the prepared DAG and only runs
// scheduler.Submit — tens-of-ms vs. hundreds-of-ms of post-approval
// prep work.
//
// Concurrency invariants:
//
//  1. Generation counter (`Revision`) prevents revision races. Each
//     architect plan revision bumps Revision. registerPreparedDAG
//     drops a stale prepare if a higher-revision entry already exists
//     (e.g. user clicked Modify, architect generated a revised plan
//     and a new prepare arrived before the old one's prepare-publish
//     was processed). takePreparedDAG returns the highest-revision
//     entry available.
//
//  2. ReadyCh signals when a prepare HAS PUBLISHED its DAG payload
//     into the registry, ready for Submit. ExecutePrepared can wait
//     briefly on ReadyCh when a prepare is in-flight but hasn't
//     finished writing PreparedCtx/PreparedDAG yet. Without this, a
//     fast user click would race with the prepare and fall back to
//     full ingest unnecessarily.
package orchestrator

import (
	"context"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/dag"
)

// preparedDAG holds the in-memory state needed to submit a prepared
// DAG without re-running prepareExecution. Keyed by plan_id in the
// orchestrator's preparedDAGs map.
type preparedDAG struct {
	planID     string
	sessionID  string
	workflowID string
	revision   int // monotonic generation; later revisions supersede earlier
	dag        *dag.DAG
	preflight  *guardianPlanPreflight

	// readyCh is closed when the prepare has written its DAG and is
	// usable by Submit. Concurrent ExecutePrepared callers wait on
	// this with a timeout before falling back to full ingest.
	readyCh chan struct{}
}

// markReady atomically signals that the prepared entry is fully
// populated (DAG built, pods pre-activated, claims posted). Idempotent.
func (p *preparedDAG) markReady() {
	if p == nil || p.readyCh == nil {
		return
	}
	select {
	case <-p.readyCh:
		// already closed; ignore
	default:
		close(p.readyCh)
	}
}

// preparedDAGRegistry guards the orchestrator's preparedDAGs map.
type preparedDAGRegistry struct {
	mu  sync.Mutex
	all map[string]*preparedDAG
}

func newPreparedDAGRegistry() *preparedDAGRegistry {
	return &preparedDAGRegistry{
		all: make(map[string]*preparedDAG),
	}
}

// registerPreparedDAG stores the prepared state for plan_id. The
// generation counter (revision) prevents revision races: if a
// later revision already exists, the new (older) entry is dropped.
// If the new revision supersedes an existing entry, the existing
// entry's readyCh is signaled (so any waiters unblock and re-check
// for the fresher revision) before being replaced.
func (o *Orchestrator) registerPreparedDAG(planID string, attempt *planHandoffIngestAttempt) {
	if o == nil || o.preparedDAGs == nil || attempt == nil {
		return
	}
	revision := 0
	if attempt.handoff != nil {
		revision = attempt.handoff.Revision
	}
	entry := &preparedDAG{
		planID:     planID,
		sessionID:  attempt.handoff.SessionID,
		workflowID: attempt.workflowID,
		revision:   revision,
		dag:        attempt.dag,
		preflight:  attempt.preflight,
		readyCh:    make(chan struct{}),
	}
	entry.markReady() // single-step register: ready as soon as it lands

	o.preparedDAGs.mu.Lock()
	defer o.preparedDAGs.mu.Unlock()
	if existing, ok := o.preparedDAGs.all[planID]; ok && existing.revision > revision {
		// Stale prepare arrived after a higher-revision one
		// already registered. Drop silently.
		return
	}
	if existing, ok := o.preparedDAGs.all[planID]; ok {
		// Supersede: unblock any waiters on the old entry so they
		// see the new one on next lookup.
		existing.markReady()
	}
	o.preparedDAGs.all[planID] = entry
}

// takePreparedDAG removes and returns the prepared entry for plan_id.
// Returns nil if no entry exists. Used by both ExecutePrepared (to
// adopt the prepared state into a submit) and DiscardPrepared (to
// drop the entry).
func (o *Orchestrator) takePreparedDAG(planID string) *preparedDAG {
	if o == nil || o.preparedDAGs == nil {
		return nil
	}
	o.preparedDAGs.mu.Lock()
	defer o.preparedDAGs.mu.Unlock()
	entry, ok := o.preparedDAGs.all[planID]
	if !ok {
		return nil
	}
	delete(o.preparedDAGs.all, planID)
	return entry
}

// peekPreparedDAG returns the entry for plan_id without removing it.
// Used by waitForPreparedDAG to inspect ready state.
func (o *Orchestrator) peekPreparedDAG(planID string) *preparedDAG {
	if o == nil || o.preparedDAGs == nil {
		return nil
	}
	o.preparedDAGs.mu.Lock()
	defer o.preparedDAGs.mu.Unlock()
	return o.preparedDAGs.all[planID]
}

// waitForPreparedDAG blocks up to timeout for a prepared entry for
// plan_id to become ready. Returns the entry on success, nil on
// timeout or if no prepare was ever in flight. Does NOT remove the
// entry — caller must call takePreparedDAG explicitly.
//
// This closes the prep/click race: a fast user click that arrives
// before prepare's DAG payload is published into the registry will
// wait briefly instead of immediately falling back to full ingest.
func (o *Orchestrator) waitForPreparedDAG(ctx context.Context, planID string, timeout time.Duration) *preparedDAG {
	entry := o.peekPreparedDAG(planID)
	if entry == nil {
		return nil
	}
	if entry.readyCh == nil {
		return entry
	}
	// Already ready?
	select {
	case <-entry.readyCh:
		return entry
	default:
	}
	// Wait with bound.
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case <-entry.readyCh:
		return entry
	case <-waitCtx.Done():
		return nil
	}
}
