package forest

import (
	"context"
	"fmt"
	"log/slog"
)

// ────────────────────────────────────────────────────────────────────
// Issue #10 Phase A.2 — training examples pruner
// ────────────────────────────────────────────────────────────────────
//
// forest_training_examples accumulates one row per (retrieval,
// candidate) pair. Once a row's been used for training (label set,
// trainer has consumed it, retention window elapsed), it can be
// pruned without losing learning signal — the model already
// absorbed it.
//
// Conservative policy: never prune rows with utility_label IS NULL
// (pending labels). Only prune labeled rows older than
// trainingExamplesRetention.

const (
	// projectorTrainingExamplesPrunerName is the lease holder name.
	projectorTrainingExamplesPrunerName = "training_examples_pruner"

	// trainingExamplesPruneBatchSize bounds rows deleted per cycle.
	trainingExamplesPruneBatchSize = 5000
)

// startTrainingExamplesPruner launches the tracked goroutine.
func (m *MemoryForest) startTrainingExamplesPruner() {
	if m == nil {
		return
	}
	m.startWorker(projectorTrainingExamplesPrunerName, trainingExamplesPruneBatchSize, func() {
		m.runTrainingExamplesPrunerLoop()
	})
}

// runTrainingExamplesPrunerLoop drives the pruner via the canonical
// runMaintenanceCycle helper.
func (m *MemoryForest) runTrainingExamplesPrunerLoop() {
	for m.runMaintenanceCycle(
		projectorTrainingExamplesPrunerName,
		m.trainingExamplesPruneInterval,
		trainingExamplesPruneBatchSize,
		m.runTrainingExamplesPruneCallback,
	) {
	}
}

// runTrainingExamplesPruneCallback runs one prune cycle, logs +
// records errors as artifacts, returns processed count.
func (m *MemoryForest) runTrainingExamplesPruneCallback() int {
	processed, err := m.runTrainingExamplesPruneOnce(m.runCtx)
	if err != nil {
		slog.Warn("forest_training_examples_prune_failed", "err", err.Error())
		m.recordPrunerArtifact(projectorTrainingExamplesPrunerName, err)
	}
	return processed
}

// runTrainingExamplesPruneOnce deletes labeled training examples
// older than the retention window. Returns the number of rows
// deleted so the loop can decide drain-vs-sleep.
//
// Bounded by SELECT-rowid-LIMIT inside the DELETE: SQLite doesn't
// support LIMIT on DELETE directly, so we use a subquery on rowid
// to cap per-cycle work.
func (m *MemoryForest) runTrainingExamplesPruneOnce(ctx context.Context) (int, error) {
	cutoff := timeNowSecondsMinus(m.trainingExamplesRetention)
	res, err := m.db.ExecContext(ctx, `
		DELETE FROM forest_training_examples
		WHERE  rowid IN (
		    SELECT rowid FROM forest_training_examples
		    WHERE  utility_label IS NOT NULL
		      AND  updated_at < ?
		    LIMIT  ?
		)
	`, cutoff, trainingExamplesPruneBatchSize)
	if err != nil {
		return 0, fmt.Errorf("delete aged training examples: %w", err)
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("training examples rows affected: %w", err)
	}
	return int(deleted), nil
}
