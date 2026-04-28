// Prepared DAG registry — orchestrator-side store of DAGs that have
// been prepared (verified attestation, built DAG, recorded WAL/SQLite)
// but not yet submitted to the scheduler. Populated by Phase=Prepare
// ingest, drained by Phase=ExecutePrepared (submit) or
// Phase=DiscardPrepared (drop).
//
// Two-phase design context: the architect publishes a prepare-only
// handoff at plan-finalization (after Guardian preflight) so the
// orchestrator's prep cost overlaps with user approval-dialog review
// time. On approve, ExecutePrepared looks up the prepared DAG and
// only runs scheduler.Submit — tens-of-ms vs. hundreds-of-ms of
// post-approval prep work in the legacy single-phase flow.
//
// Strict invariants:
//   - No pod or VFS allocation happens during prepare. Pods are
//     constructed lazily per-task at dispatch (see ensureTaskPod).
//     Discarding a prepared DAG never has to release per-task pod
//     resources because none were created.
//   - WAL/SQLite rows ARE written during prepare (durability for the
//     plan record). Discard marks the DAG state="discarded" rather
//     than deleting — operators can still see the prep happened.
//   - Idempotent on re-prepare: a second prepare for the same plan_id
//     supersedes the first (drops it). Used when the architect
//     regenerates a plan and needs to re-prepare with new attestation.
package orchestrator

import (
	"sync"

	"github.com/adalundhe/sylk/core/dag"
)

// preparedDAG holds the in-memory state needed to submit a prepared
// DAG without re-running prepareExecution. Keyed by plan_id in the
// orchestrator's preparedDAGs map.
type preparedDAG struct {
	planID     string
	sessionID  string
	workflowID string
	dag        *dag.DAG
	preflight  *guardianPlanPreflight
}

// preparedDAGRegistry guards the orchestrator's preparedDAGs map.
// Embedded into Orchestrator so the methods below have natural
// access via the o.* receiver.
type preparedDAGRegistry struct {
	mu  sync.Mutex
	all map[string]*preparedDAG
}

func newPreparedDAGRegistry() *preparedDAGRegistry {
	return &preparedDAGRegistry{
		all: make(map[string]*preparedDAG),
	}
}

// registerPreparedDAG stores the prepared state for plan_id. If a
// prior entry exists (re-prepare), it's superseded — the old DAG is
// dropped. The dag itself is in-memory only at this point; WAL/SQLite
// rows already exist from prepareExecution.
func (o *Orchestrator) registerPreparedDAG(planID string, attempt *planHandoffIngestAttempt) {
	if o == nil || o.preparedDAGs == nil || attempt == nil {
		return
	}
	entry := &preparedDAG{
		planID:     planID,
		sessionID:  attempt.handoff.SessionID,
		workflowID: attempt.workflowID,
		dag:        attempt.dag,
		preflight:  attempt.preflight,
	}
	o.preparedDAGs.mu.Lock()
	defer o.preparedDAGs.mu.Unlock()
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
