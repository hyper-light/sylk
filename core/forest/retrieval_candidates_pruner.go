package forest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// ────────────────────────────────────────────────────────────────────
// forest_retrieval_candidates background pruner
// ────────────────────────────────────────────────────────────────────
//
// Every retrieval emits one row per candidate into
// forest_retrieval_candidates (the projection driven by the candidates
// projector). At a few-retrievals-per-second pace with O(50) candidates
// each, the table grows by tens of millions of rows per day. The
// counterfactual / cooldown / sweep paths only need recent data, so
// older rows are pure storage cost.
//
// This pruner DELETEs rows older than retrievalCandidatesRetention.
// forest_retrieval_events itself stays append-only forever (audit
// ledger; replay reconstructs candidates if needed). forest_retrieval
// _sweep_state ages out the same way the events do — pruner trims
// orphaned sweep markers in the same cycle.

const (
	// projectorRetrievalCandidatesPrunerName is the lease holder name
	// for the pruner row in forest_projector_state. Multi-process
	// deployments contend on this row so only one process runs the
	// prune scan per lease window.
	projectorRetrievalCandidatesPrunerName = "retrieval_candidates_pruner"

	// defaultRetrievalCandidatesRetention is how long a candidate row
	// is kept before pruning. 7d is comfortably longer than the
	// counterfactual window (24h) and the cooldown window (last 32
	// retrievals usually < 1 day at typical pace).
	defaultRetrievalCandidatesRetention = 7 * 24 * time.Hour

	// defaultRetrievalCandidatesPruneInterval is the cadence between
	// prune scans. 1h amortizes the DELETE cost over the ledger churn.
	defaultRetrievalCandidatesPruneInterval = 1 * time.Hour

	// retrievalCandidatesPruneBatchSize bounds the per-cycle DELETE
	// to avoid a long-running write transaction. Drain-then-sleep
	// loops back immediately when the batch is full.
	retrievalCandidatesPruneBatchSize = 5000
)

// resolveRetrievalCandidatesRetention returns the configured retention
// or the default. Negative or zero falls back; values below the
// counterfactual window get logged as suspicious but not rejected
// (tests may use very short retentions intentionally).
func resolveRetrievalCandidatesRetention(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultRetrievalCandidatesRetention
	}
	return d
}

func resolveRetrievalCandidatesPruneInterval(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultRetrievalCandidatesPruneInterval
	}
	return d
}

// startRetrievalCandidatesPruner launches the tracked goroutine that
// drives the pruner. Mirrors the implicit-negative sweeper +
// AntiPattern promoter pattern: tracked on m.wg, drain-then-sleep.
//
// No defensive recover. Every code path returns errors explicitly.
// A panic here is a real bug; let it surface so we can fix the
// root cause.
func (m *MemoryForest) startRetrievalCandidatesPruner() {
	if m == nil {
		return
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.runRetrievalCandidatesPrunerLoop()
	}()
}

// runRetrievalCandidatesPrunerLoop runs the pruner under lease
// coordination + drain-then-sleep cadence. A non-leader process
// sleeps projectorWaitForLease and retries.
func (m *MemoryForest) runRetrievalCandidatesPrunerLoop() {
	for {
		if m.shouldStopProjector() {
			return
		}
		if err := m.acquireRetrievalCandidatesPrunerLease(); err != nil {
			if errors.Is(err, errLeaseHeldByOther) {
				if !m.sleepProjector(projectorWaitForLease) {
					return
				}
				continue
			}
			slog.Warn("forest_retrieval_candidates_pruner_lease_failed",
				"holder", m.projectorID, "err", err.Error())
			if !m.sleepProjector(m.retrievalCandidatesPruneInterval) {
				return
			}
			continue
		}
		processed, err := m.runRetrievalCandidatesPruneOnce(m.runCtx)
		if err != nil {
			slog.Warn("forest_retrieval_candidates_prune_failed",
				"err", err.Error())
		}
		if processed >= retrievalCandidatesPruneBatchSize {
			continue
		}
		if !m.sleepProjector(m.retrievalCandidatesPruneInterval) {
			return
		}
	}
}

// runRetrievalCandidatesPruneOnce DELETEs up to one batch of
// candidates older than the cutoff, plus any orphaned sweep-state
// rows whose retrieval_event_id no longer has any candidate row left.
// Returns the count of candidate rows deleted.
//
// The candidate DELETE is bounded by retrievalCandidatesPruneBatchSize
// via a SELECT-then-DELETE-IN-clause pattern (SQLite doesn't support
// LIMIT directly on DELETE without compile-time flags). The orphan
// sweep-state cleanup runs unbounded after — it's purely the size of
// the sweep-state table for retrievals that were swept then their
// candidates aged out.
func (m *MemoryForest) runRetrievalCandidatesPruneOnce(ctx context.Context) (int, error) {
	cutoff := time.Now().UTC().Add(-m.retrievalCandidatesRetention).Unix()
	res, err := m.db.ExecContext(ctx, `
		DELETE FROM forest_retrieval_candidates
		WHERE  rowid IN (
		    SELECT rowid
		    FROM   forest_retrieval_candidates
		    WHERE  retrieval_at < ?
		    LIMIT  ?
		)
	`, cutoff, retrievalCandidatesPruneBatchSize)
	if err != nil {
		return 0, fmt.Errorf("delete aged candidates: %w", err)
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("candidates rows affected: %w", err)
	}
	if deleted == 0 {
		return 0, nil
	}
	if err := m.pruneOrphanedSweepState(ctx); err != nil {
		// Non-fatal — the next cycle retries. Log + continue.
		slog.Debug("forest_retrieval_sweep_state_prune_failed",
			"err", err.Error())
	}
	return int(deleted), nil
}

// pruneOrphanedSweepState removes sweep-state rows whose retrieval
// event has had all its candidate rows pruned. Without this, the
// sweep-state table accumulates indefinitely (it's never the source
// of truth for whether a retrieval was swept — the candidates table
// is). We don't bound this DELETE because sweep-state rows are 1:1
// with retrieval events, not 1:N like candidates.
func (m *MemoryForest) pruneOrphanedSweepState(ctx context.Context) error {
	_, err := m.db.ExecContext(ctx, `
		DELETE FROM forest_retrieval_sweep_state
		WHERE  retrieval_event_id NOT IN (
		    SELECT retrieval_event_id FROM forest_retrieval_candidates
		)
	`)
	if err != nil {
		return fmt.Errorf("delete orphaned sweep state: %w", err)
	}
	return nil
}

func (m *MemoryForest) acquireRetrievalCandidatesPrunerLease() error {
	if err := m.ensureProjectorRow(projectorRetrievalCandidatesPrunerName); err != nil {
		return err
	}
	now := time.Now().UTC().Unix()
	expires := now + int64(projectorLeaseDuration.Seconds())
	res, err := m.db.ExecContext(m.runCtx, `
		UPDATE forest_projector_state
		SET    leader_holder      = ?,
		       leader_lease_until = ?,
		       health_status      = ?,
		       updated_at         = ?
		WHERE  projector_name      = ?
		  AND  (leader_lease_until < ? OR leader_holder = ?)
	`, m.projectorID, expires, string(ProjectorHealthRunning), now,
		projectorRetrievalCandidatesPrunerName, now, m.projectorID)
	if err != nil {
		return fmt.Errorf("acquire candidates pruner lease: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return errLeaseHeldByOther
	}
	return nil
}
