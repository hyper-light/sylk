package forest

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// ────────────────────────────────────────────────────────────────────
// Issue #10 Phase C — substrate edge incremental updates
// ────────────────────────────────────────────────────────────────────
//
// Replaces the legacy DELETE-all + INSERT-all pattern in
// refreshSubstrateState with a diff-based approach:
//
//   1. Load current edges for the session.
//   2. Build desired-edges map from the recomputed set.
//   3. Classify each desired-edge as INSERT (new) / UPDATE (changed
//      values) / unchanged (no-op).
//   4. Classify each current-edge missing from the desired set as
//      DELETE.
//   5. Apply only the diff in one transaction.
//
// Bounded by actual change. A session with thousands of edges and
// no recomputation differences does zero writes. Big sessions with
// localized change touch only the changed rows.

// edgeKey is the (source, target) identity within a session. The
// session is fixed per call.
type edgeKey struct {
	source string
	target string
}

// substrateEdgeRow is the persisted shape we compare against.
type substrateEdgeRow struct {
	conductance float64
	flux        float64
	redundancy  float64
	inhibition  float64
}

// applySubstrateEdgeDiffTx is the entry point. Called inside the
// substrate-refresh transaction. Computes the diff between
// `desired` and what's persisted, applies it, returns nil on
// success.
//
// Edge equality: floats are compared with epsilon tolerance to
// avoid noisy UPDATEs on numerically-trivial differences.
func applySubstrateEdgeDiffTx(
	ctx context.Context,
	tx *sql.Tx,
	sessionID, contextKey string,
	desired []substrateEdge,
	now time.Time,
) error {
	current, err := loadCurrentSubstrateEdgesTx(ctx, tx, sessionID)
	if err != nil {
		return err
	}
	desiredMap := substrateEdgesToMap(desired)
	toUpsert, toDelete := classifySubstrateEdgeDiff(current, desiredMap)

	if err := upsertSubstrateEdgesTx(ctx, tx, sessionID, contextKey, toUpsert, now); err != nil {
		return err
	}
	if err := deleteOrphanSubstrateEdgesTx(ctx, tx, sessionID, toDelete); err != nil {
		return err
	}
	return nil
}

// loadCurrentSubstrateEdgesTx reads the persisted edge set for the
// session into a (source, target) → values map. Bounded query
// per session (rare edge count >> branch count).
func loadCurrentSubstrateEdgesTx(ctx context.Context, tx *sql.Tx, sessionID string) (map[edgeKey]substrateEdgeRow, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT source_branch_id, target_branch_id, conductance, flux, redundancy, inhibition
		FROM   forest_substrate_edges
		WHERE  session_id = ?
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load current substrate edges: %w", err)
	}
	defer rows.Close()
	out := make(map[edgeKey]substrateEdgeRow)
	for rows.Next() {
		var (
			key edgeKey
			row substrateEdgeRow
		)
		if err := rows.Scan(&key.source, &key.target, &row.conductance, &row.flux, &row.redundancy, &row.inhibition); err != nil {
			return nil, fmt.Errorf("scan current substrate edge: %w", err)
		}
		out[key] = row
	}
	return out, rows.Err()
}

// substrateEdgesToMap projects the recomputed in-memory edge slice
// into the same map shape as loadCurrentSubstrateEdgesTx for
// O(1) diff classification.
func substrateEdgesToMap(edges []substrateEdge) map[edgeKey]substrateEdgeRow {
	out := make(map[edgeKey]substrateEdgeRow, len(edges))
	for i := range edges {
		e := &edges[i]
		out[edgeKey{source: e.SourceID, target: e.TargetID}] = substrateEdgeRow{
			conductance: e.Conductance,
			flux:        e.Flux,
			redundancy:  e.Redundancy,
			inhibition:  e.Inhibition,
		}
	}
	return out
}

// classifySubstrateEdgeDiff computes (toUpsert, toDelete). toUpsert
// is the subset of `desired` whose values changed (or are new);
// toDelete is the subset of `current` whose key is missing from
// `desired`. Equality uses epsilon tolerance to skip numerically
// trivial differences — the recomputation is never bit-stable.
func classifySubstrateEdgeDiff(
	current, desired map[edgeKey]substrateEdgeRow,
) (toUpsert map[edgeKey]substrateEdgeRow, toDelete []edgeKey) {
	toUpsert = make(map[edgeKey]substrateEdgeRow, len(desired))
	for key, row := range desired {
		if existing, ok := current[key]; ok {
			if substrateEdgeRowsEqual(existing, row) {
				continue
			}
		}
		toUpsert[key] = row
	}
	for key := range current {
		if _, present := desired[key]; !present {
			toDelete = append(toDelete, key)
		}
	}
	return toUpsert, toDelete
}

// substrateEdgeEpsilon is the tolerance for float equality on edge
// values. Below this, two values are considered "the same" — avoids
// noisy UPDATEs on round-trip numerical drift.
const substrateEdgeEpsilon = 1e-6

func substrateEdgeRowsEqual(a, b substrateEdgeRow) bool {
	return floatsClose(a.conductance, b.conductance) &&
		floatsClose(a.flux, b.flux) &&
		floatsClose(a.redundancy, b.redundancy) &&
		floatsClose(a.inhibition, b.inhibition)
}

func floatsClose(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < substrateEdgeEpsilon
}

// upsertSubstrateEdgesTx writes the changed/added edges. ON
// CONFLICT updates only the value columns (key is fixed).
func upsertSubstrateEdgesTx(
	ctx context.Context,
	tx *sql.Tx,
	sessionID, contextKey string,
	toUpsert map[edgeKey]substrateEdgeRow,
	now time.Time,
) error {
	if len(toUpsert) == 0 {
		return nil
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO forest_substrate_edges
		(session_id, source_branch_id, target_branch_id, conductance, flux, redundancy, inhibition, updated_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id, source_branch_id, target_branch_id) DO UPDATE SET
			conductance = excluded.conductance,
			flux        = excluded.flux,
			redundancy  = excluded.redundancy,
			inhibition  = excluded.inhibition,
			updated_at  = excluded.updated_at,
			metadata    = excluded.metadata
	`)
	if err != nil {
		return fmt.Errorf("prepare substrate edge upsert: %w", err)
	}
	defer stmt.Close()
	metadata := marshalJSON(map[string]any{"context_key": contextKey})
	for key, row := range toUpsert {
		if _, err := stmt.ExecContext(ctx,
			sessionID, key.source, key.target,
			row.conductance, row.flux, row.redundancy, row.inhibition,
			now.Unix(), metadata,
		); err != nil {
			return fmt.Errorf("upsert substrate edge %s→%s: %w", key.source, key.target, err)
		}
	}
	return nil
}

// deleteOrphanSubstrateEdgesTx removes edges that were in the DB
// but no longer in the desired set. Batched as a single DELETE
// using a (source, target) tuple list — SQLite's IN-with-tuple
// requires an OR construction, which is cheap for our typical
// orphan counts (few dozen at most).
func deleteOrphanSubstrateEdgesTx(
	ctx context.Context,
	tx *sql.Tx,
	sessionID string,
	toDelete []edgeKey,
) error {
	if len(toDelete) == 0 {
		return nil
	}
	clauses := make([]string, len(toDelete))
	args := make([]any, 0, 1+2*len(toDelete))
	args = append(args, sessionID)
	for i, key := range toDelete {
		clauses[i] = "(source_branch_id = ? AND target_branch_id = ?)"
		args = append(args, key.source, key.target)
	}
	query := `DELETE FROM forest_substrate_edges WHERE session_id = ? AND (` +
		strings.Join(clauses, " OR ") + `)`
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("delete orphan substrate edges: %w", err)
	}
	return nil
}
