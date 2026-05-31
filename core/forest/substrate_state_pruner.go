package forest

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// ────────────────────────────────────────────────────────────────────
// Issue #10 Phase A.3 — substrate state pruner
// ────────────────────────────────────────────────────────────────────
//
// Substrate state (forest_substrate_state, _frontiers, _edges,
// _sessions) accumulates per (session_id, branch_id). Long-running
// deployments collect rows for sessions that are no longer active.
// This pruner identifies sessions whose latest event is older than
// the retention window and drops their substrate footprint in one
// transaction.
//
// "Inactive session" detection: query forest_branches grouped by
// session_id, find max(updated_at). Sessions whose max is older
// than the cutoff are eligible.

const (
	// projectorSubstrateStatePrunerName is the lease holder name.
	projectorSubstrateStatePrunerName = "substrate_state_pruner"

	// substrateStatePruneBatchSize bounds the count of inactive
	// sessions processed per cycle. Each session triggers DELETEs
	// across four substrate tables; the batch caps total work.
	substrateStatePruneBatchSize = 32
)

// startSubstrateStatePruner launches the tracked goroutine.
func (m *MemoryForest) startSubstrateStatePruner() {
	if m == nil {
		return
	}
	m.startWorker(projectorSubstrateStatePrunerName, substrateStatePruneBatchSize, func() {
		m.runSubstrateStatePrunerLoop()
	})
}

// runSubstrateStatePrunerLoop drives the pruner via the canonical
// runMaintenanceCycle helper.
func (m *MemoryForest) runSubstrateStatePrunerLoop() {
	for m.runMaintenanceCycle(
		projectorSubstrateStatePrunerName,
		m.substrateStatePruneInterval,
		substrateStatePruneBatchSize,
		m.runSubstrateStatePruneCallback,
	) {
	}
}

// runSubstrateStatePruneCallback runs one prune cycle, logs +
// records errors as artifacts, returns processed count.
func (m *MemoryForest) runSubstrateStatePruneCallback() int {
	processed, err := m.runSubstrateStatePruneOnce(m.runCtx)
	if err != nil {
		slog.Warn("forest_substrate_state_prune_failed", "err", err.Error())
		m.recordPrunerArtifact(projectorSubstrateStatePrunerName, err)
	}
	return processed
}

// runSubstrateStatePruneOnce identifies inactive sessions and drops
// their substrate footprint. One transaction per cycle so the four
// table DELETEs commit atomically — if any fails, all roll back.
func (m *MemoryForest) runSubstrateStatePruneOnce(ctx context.Context) (int, error) {
	inactiveSessions, err := m.findInactiveSubstrateSessions(ctx, substrateStatePruneBatchSize)
	if err != nil {
		return 0, err
	}
	if len(inactiveSessions) == 0 {
		return 0, nil
	}
	if err := m.dropSubstrateForSessions(ctx, inactiveSessions); err != nil {
		return 0, err
	}
	return len(inactiveSessions), nil
}

// findInactiveSubstrateSessions returns up to `limit` session_ids
// whose substrate footprint is eligible for pruning. A session is
// inactive when its latest forest_branches.updated_at is older than
// the retention cutoff (or when no branches reference the session
// at all but substrate state still exists for it).
//
// LEFT JOIN handles the "orphan substrate session" case: rows in
// forest_substrate_sessions whose session_id has no branches.
func (m *MemoryForest) findInactiveSubstrateSessions(ctx context.Context, limit int) ([]string, error) {
	cutoff := timeNowSecondsMinus(m.substrateStateRetention)
	rows, err := m.db.QueryContext(ctx, `
		SELECT s.session_id
		FROM   forest_substrate_sessions s
		LEFT JOIN forest_branches b ON b.session_id = s.session_id
		GROUP BY s.session_id
		HAVING COALESCE(MAX(b.updated_at), 0) < ?
		LIMIT  ?
	`, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("find inactive substrate sessions: %w", err)
	}
	defer rows.Close()
	out := make([]string, 0, limit)
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			return nil, fmt.Errorf("scan inactive session: %w", err)
		}
		out = append(out, sid)
	}
	return out, rows.Err()
}

// dropSubstrateForSessions deletes substrate state for the given
// session ids in one transaction. Touches all four substrate tables.
// forest_substrate_state, _edges, _sessions are session-keyed;
// forest_substrate_frontiers is keyed by context_key (derived from
// session_id via substrateContextKey), so we delete it by the
// derived context_keys.
//
// The substrate_sessions row is removed last so a partial failure
// leaves the row in place and the session re-eligible for pruning
// next cycle.
func (m *MemoryForest) dropSubstrateForSessions(ctx context.Context, sessionIDs []string) error {
	if len(sessionIDs) == 0 {
		return nil
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin substrate state prune tx: %w", err)
	}
	defer tx.Rollback()

	placeholders, args := buildSessionInArgs(sessionIDs)
	contextKeyPlaceholders, contextKeyArgs := buildContextKeyInArgs(sessionIDs)

	type deleteOp struct {
		name  string
		query string
		args  []any
	}
	deletes := []deleteOp{
		{
			name:  "forest_substrate_state",
			query: `DELETE FROM forest_substrate_state WHERE session_id IN (` + placeholders + `)`,
			args:  args,
		},
		{
			name:  "forest_substrate_frontiers",
			query: `DELETE FROM forest_substrate_frontiers WHERE context_key IN (` + contextKeyPlaceholders + `)`,
			args:  contextKeyArgs,
		},
		{
			name:  "forest_substrate_edges",
			query: `DELETE FROM forest_substrate_edges WHERE session_id IN (` + placeholders + `)`,
			args:  args,
		},
		{
			name:  "forest_substrate_sessions",
			query: `DELETE FROM forest_substrate_sessions WHERE session_id IN (` + placeholders + `)`,
			args:  args,
		},
	}
	for _, d := range deletes {
		if _, err := tx.ExecContext(ctx, d.query, d.args...); err != nil {
			return fmt.Errorf("prune %s: %w", d.name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit substrate state prune: %w", err)
	}
	return nil
}

// buildContextKeyInArgs returns placeholders + args for an IN clause
// over context_keys derived from the given session_ids.
func buildContextKeyInArgs(sessionIDs []string) (string, []any) {
	parts := make([]string, len(sessionIDs))
	args := make([]any, 0, len(sessionIDs))
	for i, sid := range sessionIDs {
		parts[i] = "?"
		args = append(args, substrateContextKey(sid))
	}
	return strings.Join(parts, ","), args
}

// buildSessionInArgs returns the placeholders + args for an IN clause
// over session_ids.
func buildSessionInArgs(sessionIDs []string) (string, []any) {
	parts := make([]string, len(sessionIDs))
	args := make([]any, 0, len(sessionIDs))
	for i, id := range sessionIDs {
		parts[i] = "?"
		args = append(args, id)
	}
	return strings.Join(parts, ","), args
}
