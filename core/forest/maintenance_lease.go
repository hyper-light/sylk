package forest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// ────────────────────────────────────────────────────────────────────
// Shared lease acquisition + artifact recording for maintenance
// goroutines. Every periodic worker (sweepers, pruners, archival,
// trainer) follows the same pattern: take a row in
// forest_projector_state, refresh it with the lease window, return
// errLeaseHeldByOther to the loop when another process holds it.
// On error, the loop calls recordPrunerArtifact to write the
// failure as a durable artifact (last_error / last_error_at).
//
// All paths bounds-checked, error-returning, panic-free.

// acquireGenericLease takes (or renews) a lease row for the named
// maintenance worker. Caller is responsible for the loop semantics
// (sleep + retry on errLeaseHeldByOther). Returns the canonical
// errLeaseHeldByOther sentinel so callers can use errors.Is to
// detect the contention case.
func (m *MemoryForest) acquireGenericLease(name string) error {
	if err := m.ensureProjectorRow(name); err != nil {
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
		name, now, m.projectorID)
	if err != nil {
		return fmt.Errorf("acquire %s lease: %w", name, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s lease rows affected: %w", name, err)
	}
	if affected == 0 {
		return errLeaseHeldByOther
	}
	return nil
}

// timeNowSecondsMinus returns the unix-second timestamp `d` ago.
// Helper for cutoff arithmetic shared by the pruners.
func timeNowSecondsMinus(d time.Duration) int64 {
	return time.Now().UTC().Add(-d).Unix()
}

// runMaintenanceCycle is the canonical loop body for every periodic
// maintenance worker (pruners, sweepers, archival). Returns true to
// continue (caller should re-loop), false to exit (shutdown signal
// fired during sleep). Encapsulates: stop-check, lease acquisition,
// lease-error handling, work invocation, drain-then-sleep cadence.
//
// `work` returns the count of items processed in this cycle. The
// callback is responsible for its own error logging + artifact
// recording — the helper deliberately doesn't peek into work errors
// so each worker can attach its own slog event names + artifacts
// without coupling.
//
// Cyclomatic complexity: 4 branches (stop, lease err type, lease err
// path, batch-full). Calling loops collapse to a one-line `for {…}`.
func (m *MemoryForest) runMaintenanceCycle(
	name string,
	interval time.Duration,
	batchSize int,
	work func() int,
) bool {
	if m.shouldStopProjector() {
		return false
	}
	if err := m.acquireGenericLease(name); err != nil {
		return m.handleLeaseAcquireError(name, interval, err)
	}
	processed := work()
	if processed >= batchSize {
		return true
	}
	return m.sleepProjector(interval)
}

// handleLeaseAcquireError centralizes the contention vs hard-error
// branches so runMaintenanceCycle stays flat. Returns the same
// continue/exit signal as the helper.
func (m *MemoryForest) handleLeaseAcquireError(name string, interval time.Duration, err error) bool {
	if errors.Is(err, errLeaseHeldByOther) {
		return m.sleepProjector(projectorWaitForLease)
	}
	slog.Warn("forest_maintenance_lease_failed",
		"projector", name, "err", err.Error())
	m.recordPrunerArtifact(name, err)
	return m.sleepProjector(interval)
}

// recordPrunerArtifact persists a maintenance failure on the named
// projector_state row. Errors as artifacts: every failure becomes
// a durable last_error / last_error_at on the worker's lease row,
// readable by Health() and operator queries.
//
// Best-effort: a write failure here is logged but doesn't loop back
// into the caller's error path (the original error is already being
// handled).
func (m *MemoryForest) recordPrunerArtifact(name string, cause error) {
	if cause == nil || m.runCtx == nil || m.runCtx.Err() != nil {
		return
	}
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return
	}
	msg := truncateError(cause.Error(), projectorErrTruncate)
	now := time.Now().UTC().Unix()
	if _, err := m.db.ExecContext(m.runCtx, `
		UPDATE forest_projector_state
		SET    last_error    = ?,
		       last_error_at = ?,
		       updated_at    = ?
		WHERE  projector_name = ?
	`, msg, now, now, name); err != nil {
		slog.Warn("forest_pruner_record_artifact_failed",
			"projector", name, "err", err.Error())
	}
}
