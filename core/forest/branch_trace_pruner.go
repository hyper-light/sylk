package forest

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// ────────────────────────────────────────────────────────────────────
// Issue #10 Phase A.1 — branch trace pruner
// ────────────────────────────────────────────────────────────────────
//
// Background pruner for forest_branch_traces. The existing
// WarmthStore.RecordAccess prunes inline on every access — that's
// fine for steady state but pathologically slow on the first access
// after a long quiet period and never runs for branches no longer
// being accessed (they accumulate phantom rows forever).
//
// Tracked goroutine, lease-coordinated, drain-then-sleep, errors
// recorded as artifacts on the projector_state lease row. Single
// SQL per excess branch; per-cycle work bounded by batch size.

const (
	// projectorBranchTracePrunerName is the lease holder name.
	projectorBranchTracePrunerName = "branch_trace_pruner"

	// defaultBranchTraceRetentionPerBranch caps trace rows kept per
	// branch. Must match (or exceed) the inline prune-back limit in
	// WarmthStore so the background pruner doesn't fight the inline
	// path. 64 is what the warmth code uses.
	defaultBranchTraceRetentionPerBranch = 64

	// defaultBranchTracePruneInterval is the cadence between scans.
	// 15m is short enough that excess accumulates only briefly,
	// long enough that pruning cost is amortized.
	defaultBranchTracePruneInterval = 15 * time.Minute

	// defaultBranchTracePruneBatchSize bounds per-cycle work. Each
	// branch with excess triggers one DELETE; this caps the count of
	// branches per cycle. Cycles that fill the batch immediately
	// re-loop without sleeping.
	defaultBranchTracePruneBatchSize = 128
)

func resolveBranchTraceRetentionPerBranch(n int) int {
	if n <= 0 {
		return defaultBranchTraceRetentionPerBranch
	}
	return n
}

func resolveBranchTracePruneInterval(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultBranchTracePruneInterval
	}
	return d
}

// startBranchTracePruner launches the tracked goroutine.
func (m *MemoryForest) startBranchTracePruner() {
	if m == nil {
		return
	}
	m.registerRuntimeTicker("branch_trace_pruner_interval", m.branchTracePruneInterval)
	m.startWorker(projectorBranchTracePrunerName, m.branchTraceRetentionPerBranch, func(context.Context) error {
		m.runBranchTracePrunerLoop()
		return nil
	})
}

// runBranchTracePrunerLoop drives the pruner via the canonical
// runMaintenanceCycle helper. Cyclomatic complexity 1 (just the
// for loop); all branching lives in the shared helper.
func (m *MemoryForest) runBranchTracePrunerLoop() {
	for m.runMaintenanceCycle(
		projectorBranchTracePrunerName,
		m.branchTracePruneInterval,
		defaultBranchTracePruneBatchSize,
		m.runBranchTracePruneCallback,
	) {
	}
}

// runBranchTracePruneCallback runs one prune cycle, logs + records
// errors as artifacts, and returns the processed count. Pure
// callback for the maintenance loop helper.
func (m *MemoryForest) runBranchTracePruneCallback() int {
	processed, err := m.runBranchTracePruneOnce(m.runCtx)
	if err != nil {
		slog.Warn("forest_branch_trace_prune_failed", "err", err.Error())
		m.recordPrunerArtifact(projectorBranchTracePrunerName, err)
	}
	return processed
}

// runBranchTracePruneOnce trims excess trace rows for branches that
// have more than `retentionPerBranch` entries. Returns the count of
// branches processed (regardless of per-branch trim outcome).
func (m *MemoryForest) runBranchTracePruneOnce(ctx context.Context) (int, error) {
	excess, err := m.loadBranchesWithExcessTraces(ctx, defaultBranchTracePruneBatchSize)
	if err != nil {
		return 0, fmt.Errorf("load excess-trace branches: %w", err)
	}
	if len(excess) == 0 {
		return 0, nil
	}
	for _, branchID := range excess {
		if err := m.pruneOneBranchTraces(ctx, branchID); err != nil {
			slog.Debug("forest_branch_trace_prune_branch_failed",
				"branch_id", branchID, "err", err.Error())
		}
	}
	return len(excess), nil
}

// loadBranchesWithExcessTraces finds branches whose row count in
// forest_branch_traces exceeds the per-branch retention. Bounded by
// `limit` so a single cycle does bounded work.
func (m *MemoryForest) loadBranchesWithExcessTraces(ctx context.Context, limit int) ([]string, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT branch_id
		FROM   forest_branch_traces
		GROUP BY branch_id
		HAVING COUNT(*) > ?
		LIMIT  ?
	`, m.branchTraceRetentionPerBranch, limit)
	if err != nil {
		return nil, fmt.Errorf("query excess-trace branches: %w", err)
	}
	defer rows.Close()
	out := make([]string, 0, limit)
	for rows.Next() {
		var branchID string
		if err := rows.Scan(&branchID); err != nil {
			return nil, fmt.Errorf("scan excess-trace branch: %w", err)
		}
		out = append(out, branchID)
	}
	return out, rows.Err()
}

// pruneOneBranchTraces deletes the oldest rows for one branch,
// leaving the most recent `retentionPerBranch`. Single SQL.
func (m *MemoryForest) pruneOneBranchTraces(ctx context.Context, branchID string) error {
	_, err := m.db.ExecContext(ctx, `
		DELETE FROM forest_branch_traces
		WHERE branch_id = ?
		  AND rowid NOT IN (
		      SELECT rowid FROM forest_branch_traces
		      WHERE branch_id = ?
		      ORDER BY accessed_at DESC
		      LIMIT  ?
		  )
	`, branchID, branchID, m.branchTraceRetentionPerBranch)
	if err != nil {
		return fmt.Errorf("prune traces for %s: %w", branchID, err)
	}
	return nil
}
