package temporal

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// =============================================================================
// EdgeHistoryQuerier (TG.2.1)
// =============================================================================

// EdgeHistoryQuerier provides version history queries for temporal edges.
// It retrieves all historical versions of an edge, enabling time-travel
// queries and audit trails.
type EdgeHistoryQuerier struct {
	db *sql.DB
}

// NewEdgeHistoryQuerier creates a new EdgeHistoryQuerier with the given database connection.
func NewEdgeHistoryQuerier(db *sql.DB) *EdgeHistoryQuerier {
	return &EdgeHistoryQuerier{db: db}
}

// GetEdgeHistory returns all versions of an edge over time, ordered by transaction
// start time (most recent first). This includes the current version, all historical
// versions, and soft-deleted versions.
//
// The history is tracked by transaction time (tx_start, tx_end), which records when
// each version was recorded and superseded in the database.
func (q *EdgeHistoryQuerier) GetEdgeHistory(ctx context.Context, edgeID int64) ([]vectorgraphdb.TemporalEdge, error) {
	if q.db == nil {
		return nil, ErrNilDB
	}

	// Query for all versions of the edge, ordered by tx_start DESC (most recent first).
	// This includes both current versions (tx_end IS NULL) and superseded versions.
	query := baseTemporalEdgeSelect + `
		WHERE id = ?
		ORDER BY tx_start DESC
	`

	rows, err := q.db.QueryContext(ctx, query, edgeID)
	if err != nil {
		return nil, fmt.Errorf("query edge history for id %d: %w", edgeID, err)
	}
	defer rows.Close()

	edges, err := scanTemporalEdges(rows)
	if err != nil {
		return nil, err
	}

	// If no edges found, this edge ID doesn't exist
	if len(edges) == 0 {
		return nil, ErrNoResults
	}

	return edges, nil
}

// GetEdgeVersionAt returns the specific version of an edge that was current at
// a given point in time. This uses transaction time (tx_start, tx_end) to determine
// which version was the "truth" at that moment in the database.
//
// A version is current at time t if: tx_start <= t AND (tx_end IS NULL OR tx_end > t)
func (q *EdgeHistoryQuerier) GetEdgeVersionAt(ctx context.Context, edgeID int64, asOf time.Time) (*vectorgraphdb.TemporalEdge, error) {
	if q.db == nil {
		return nil, ErrNilDB
	}

	asOfUnix := asOf.Unix()

	// Query for the version that was current at the specified time.
	// tx_start <= asOf: the version had been recorded by that time
	// tx_end IS NULL OR tx_end > asOf: the version hadn't been superseded yet
	query := baseTemporalEdgeSelect + `
		WHERE id = ?
		AND (tx_start IS NULL OR tx_start <= ?)
		AND (tx_end IS NULL OR tx_end > ?)
		LIMIT 1
	`

	rows, err := q.db.QueryContext(ctx, query, edgeID, asOfUnix, asOfUnix)
	if err != nil {
		return nil, fmt.Errorf("query edge version at %v for id %d: %w", asOf, edgeID, err)
	}
	defer rows.Close()

	edges, err := scanTemporalEdges(rows)
	if err != nil {
		return nil, err
	}

	if len(edges) == 0 {
		return nil, ErrNoResults
	}

	return &edges[0], nil
}

// GetEdgeVersionCount returns the total number of versions for an edge.
// This counts all transaction-time versions, including the current one
// and all superseded versions.
func (q *EdgeHistoryQuerier) GetEdgeVersionCount(ctx context.Context, edgeID int64) (int, error) {
	if q.db == nil {
		return 0, ErrNilDB
	}

	query := `SELECT COUNT(*) FROM edges WHERE id = ?`

	var count int
	err := q.db.QueryRowContext(ctx, query, edgeID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count edge versions for id %d: %w", edgeID, err)
	}

	return count, nil
}

// GetEdgeHistoryBySourceTarget returns all versions of edges between two specific
// nodes, ordered by transaction start time (most recent first).
func (q *EdgeHistoryQuerier) GetEdgeHistoryBySourceTarget(ctx context.Context, sourceID, targetID string) ([]vectorgraphdb.TemporalEdge, error) {
	if q.db == nil {
		return nil, ErrNilDB
	}

	query := baseTemporalEdgeSelect + `
		WHERE source_id = ?
		AND target_id = ?
		ORDER BY tx_start DESC
	`

	rows, err := q.db.QueryContext(ctx, query, sourceID, targetID)
	if err != nil {
		return nil, fmt.Errorf("query edge history for %s->%s: %w", sourceID, targetID, err)
	}
	defer rows.Close()

	return scanTemporalEdges(rows)
}

// GetEdgeHistoryForNode returns all versions of all edges originating from
// a specific node, ordered by transaction start time (most recent first).
func (q *EdgeHistoryQuerier) GetEdgeHistoryForNode(ctx context.Context, nodeID string) ([]vectorgraphdb.TemporalEdge, error) {
	if q.db == nil {
		return nil, ErrNilDB
	}

	query := baseTemporalEdgeSelect + `
		WHERE source_id = ?
		ORDER BY tx_start DESC
	`

	rows, err := q.db.QueryContext(ctx, query, nodeID)
	if err != nil {
		return nil, fmt.Errorf("query edge history for node %s: %w", nodeID, err)
	}
	defer rows.Close()

	return scanTemporalEdges(rows)
}

// GetSupersededEdges returns all edges that have been superseded (tx_end IS NOT NULL),
// optionally filtered by a time range.
func (q *EdgeHistoryQuerier) GetSupersededEdges(ctx context.Context) ([]vectorgraphdb.TemporalEdge, error) {
	if q.db == nil {
		return nil, ErrNilDB
	}

	query := baseTemporalEdgeSelect + `
		WHERE tx_end IS NOT NULL
		ORDER BY tx_end DESC
	`

	rows, err := q.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query superseded edges: %w", err)
	}
	defer rows.Close()

	return scanTemporalEdges(rows)
}

// GetSoftDeletedEdges returns all edges that have been soft-deleted
// (valid_to IS NOT NULL), indicating they are no longer valid in the real world.
func (q *EdgeHistoryQuerier) GetSoftDeletedEdges(ctx context.Context) ([]vectorgraphdb.TemporalEdge, error) {
	if q.db == nil {
		return nil, ErrNilDB
	}

	query := baseTemporalEdgeSelect + `
		WHERE valid_to IS NOT NULL
		ORDER BY valid_to DESC
	`

	rows, err := q.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query soft-deleted edges: %w", err)
	}
	defer rows.Close()

	return scanTemporalEdges(rows)
}
