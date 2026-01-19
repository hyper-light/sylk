package temporal

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// =============================================================================
// BetweenQuerier (TG.1.2)
// =============================================================================

// BetweenQuerier provides range-based temporal queries for edges.
// It retrieves edges that were valid during a specified time range.
type BetweenQuerier struct {
	db *sql.DB
}

// NewBetweenQuerier creates a new BetweenQuerier with the given database connection.
func NewBetweenQuerier(db *sql.DB) *BetweenQuerier {
	return &BetweenQuerier{db: db}
}

// GetEdgesBetween returns all edges for a node that were valid during the specified time range.
// An edge is considered valid during a range [start, end) if there is any overlap between
// the edge's valid period [valid_from, valid_to) and the query range.
//
// This captures three scenarios:
//  1. Edge was created during the range (valid_from within [start, end))
//  2. Edge was deleted during the range (valid_to within [start, end))
//  3. Edge was modified during the range (spans across the range boundaries)
//
// The overlap condition is: valid_from < end AND (valid_to IS NULL OR valid_to > start)
func (q *BetweenQuerier) GetEdgesBetween(ctx context.Context, nodeID string, start, end time.Time) ([]vectorgraphdb.TemporalEdge, error) {
	if q.db == nil {
		return nil, ErrNilDB
	}

	if !start.Before(end) {
		return nil, ErrInvalidTimeRange
	}

	startUnix := start.Unix()
	endUnix := end.Unix()

	// Query for edges where the valid period overlaps with [start, end):
	// - valid_from < end (the edge started before the range ended)
	// - valid_to IS NULL OR valid_to > start (the edge hasn't ended or ended after range start)
	query := baseTemporalEdgeSelect + `
		WHERE source_id = ?
		AND (valid_from IS NULL OR valid_from < ?)
		AND (valid_to IS NULL OR valid_to > ?)
		ORDER BY valid_from ASC
	`

	rows, err := q.db.QueryContext(ctx, query, nodeID, endUnix, startUnix)
	if err != nil {
		return nil, fmt.Errorf("query edges between %v and %v: %w", start, end, err)
	}
	defer rows.Close()

	return scanTemporalEdges(rows)
}

// GetEdgesCreatedBetween returns all edges that were created (valid_from) during
// the specified time range [start, end).
// This is useful for finding new relationships that appeared during a period.
func (q *BetweenQuerier) GetEdgesCreatedBetween(ctx context.Context, start, end time.Time) ([]vectorgraphdb.TemporalEdge, error) {
	if q.db == nil {
		return nil, ErrNilDB
	}

	if !start.Before(end) {
		return nil, ErrInvalidTimeRange
	}

	startUnix := start.Unix()
	endUnix := end.Unix()

	// Query for edges where valid_from is within the range [start, end)
	query := baseTemporalEdgeSelect + `
		WHERE valid_from >= ?
		AND valid_from < ?
		ORDER BY valid_from ASC
	`

	rows, err := q.db.QueryContext(ctx, query, startUnix, endUnix)
	if err != nil {
		return nil, fmt.Errorf("query edges created between %v and %v: %w", start, end, err)
	}
	defer rows.Close()

	return scanTemporalEdges(rows)
}

// GetEdgesExpiredBetween returns all edges that expired (valid_to) during
// the specified time range [start, end).
// This is useful for finding relationships that ended during a period.
func (q *BetweenQuerier) GetEdgesExpiredBetween(ctx context.Context, start, end time.Time) ([]vectorgraphdb.TemporalEdge, error) {
	if q.db == nil {
		return nil, ErrNilDB
	}

	if !start.Before(end) {
		return nil, ErrInvalidTimeRange
	}

	startUnix := start.Unix()
	endUnix := end.Unix()

	// Query for edges where valid_to is within the range [start, end)
	// Only include edges that actually have an expiration (valid_to IS NOT NULL)
	query := baseTemporalEdgeSelect + `
		WHERE valid_to IS NOT NULL
		AND valid_to >= ?
		AND valid_to < ?
		ORDER BY valid_to ASC
	`

	rows, err := q.db.QueryContext(ctx, query, startUnix, endUnix)
	if err != nil {
		return nil, fmt.Errorf("query edges expired between %v and %v: %w", start, end, err)
	}
	defer rows.Close()

	return scanTemporalEdges(rows)
}

// GetEdgesBetweenWithTarget returns edges between two specific nodes that were valid
// during the specified time range.
func (q *BetweenQuerier) GetEdgesBetweenWithTarget(ctx context.Context, sourceID, targetID string, start, end time.Time) ([]vectorgraphdb.TemporalEdge, error) {
	if q.db == nil {
		return nil, ErrNilDB
	}

	if !start.Before(end) {
		return nil, ErrInvalidTimeRange
	}

	startUnix := start.Unix()
	endUnix := end.Unix()

	query := baseTemporalEdgeSelect + `
		WHERE source_id = ?
		AND target_id = ?
		AND (valid_from IS NULL OR valid_from < ?)
		AND (valid_to IS NULL OR valid_to > ?)
		ORDER BY valid_from ASC
	`

	rows, err := q.db.QueryContext(ctx, query, sourceID, targetID, endUnix, startUnix)
	if err != nil {
		return nil, fmt.Errorf("query edges between %v and %v for %s->%s: %w", start, end, sourceID, targetID, err)
	}
	defer rows.Close()

	return scanTemporalEdges(rows)
}

// GetIncomingEdgesBetween returns edges pointing to the specified node that were valid
// during the specified time range.
func (q *BetweenQuerier) GetIncomingEdgesBetween(ctx context.Context, nodeID string, start, end time.Time) ([]vectorgraphdb.TemporalEdge, error) {
	if q.db == nil {
		return nil, ErrNilDB
	}

	if !start.Before(end) {
		return nil, ErrInvalidTimeRange
	}

	startUnix := start.Unix()
	endUnix := end.Unix()

	query := baseTemporalEdgeSelect + `
		WHERE target_id = ?
		AND (valid_from IS NULL OR valid_from < ?)
		AND (valid_to IS NULL OR valid_to > ?)
		ORDER BY valid_from ASC
	`

	rows, err := q.db.QueryContext(ctx, query, nodeID, endUnix, startUnix)
	if err != nil {
		return nil, fmt.Errorf("query incoming edges between %v and %v: %w", start, end, err)
	}
	defer rows.Close()

	return scanTemporalEdges(rows)
}

// GetEdgesBetweenByType returns edges of a specific type for a node that were valid
// during the specified time range.
func (q *BetweenQuerier) GetEdgesBetweenByType(ctx context.Context, nodeID string, edgeType vectorgraphdb.EdgeType, start, end time.Time) ([]vectorgraphdb.TemporalEdge, error) {
	if q.db == nil {
		return nil, ErrNilDB
	}

	if !start.Before(end) {
		return nil, ErrInvalidTimeRange
	}

	startUnix := start.Unix()
	endUnix := end.Unix()

	query := baseTemporalEdgeSelect + `
		WHERE source_id = ?
		AND edge_type = ?
		AND (valid_from IS NULL OR valid_from < ?)
		AND (valid_to IS NULL OR valid_to > ?)
		ORDER BY valid_from ASC
	`

	rows, err := q.db.QueryContext(ctx, query, nodeID, edgeType, endUnix, startUnix)
	if err != nil {
		return nil, fmt.Errorf("query edges between %v and %v by type %v: %w", start, end, edgeType, err)
	}
	defer rows.Close()

	return scanTemporalEdges(rows)
}

// GetEdgesModifiedBetween returns edges that had any change (created, expired, or superseded)
// during the specified time range. This combines created and expired queries.
func (q *BetweenQuerier) GetEdgesModifiedBetween(ctx context.Context, start, end time.Time) ([]vectorgraphdb.TemporalEdge, error) {
	if q.db == nil {
		return nil, ErrNilDB
	}

	if !start.Before(end) {
		return nil, ErrInvalidTimeRange
	}

	startUnix := start.Unix()
	endUnix := end.Unix()

	// Query for edges where either:
	// 1. valid_from is within the range (created)
	// 2. valid_to is within the range (expired)
	// 3. tx_start is within the range (transaction started)
	// 4. tx_end is within the range (transaction superseded)
	query := baseTemporalEdgeSelect + `
		WHERE (
			(valid_from >= ? AND valid_from < ?)
			OR (valid_to >= ? AND valid_to < ?)
			OR (tx_start >= ? AND tx_start < ?)
			OR (tx_end >= ? AND tx_end < ?)
		)
		ORDER BY COALESCE(valid_from, tx_start) ASC
	`

	rows, err := q.db.QueryContext(ctx, query,
		startUnix, endUnix,
		startUnix, endUnix,
		startUnix, endUnix,
		startUnix, endUnix,
	)
	if err != nil {
		return nil, fmt.Errorf("query edges modified between %v and %v: %w", start, end, err)
	}
	defer rows.Close()

	return scanTemporalEdges(rows)
}

// GetEdgesCreatedBetweenForNode returns edges for a specific node that were created
// during the specified time range.
func (q *BetweenQuerier) GetEdgesCreatedBetweenForNode(ctx context.Context, nodeID string, start, end time.Time) ([]vectorgraphdb.TemporalEdge, error) {
	if q.db == nil {
		return nil, ErrNilDB
	}

	if !start.Before(end) {
		return nil, ErrInvalidTimeRange
	}

	startUnix := start.Unix()
	endUnix := end.Unix()

	query := baseTemporalEdgeSelect + `
		WHERE source_id = ?
		AND valid_from >= ?
		AND valid_from < ?
		ORDER BY valid_from ASC
	`

	rows, err := q.db.QueryContext(ctx, query, nodeID, startUnix, endUnix)
	if err != nil {
		return nil, fmt.Errorf("query edges created between %v and %v for node %s: %w", start, end, nodeID, err)
	}
	defer rows.Close()

	return scanTemporalEdges(rows)
}

// GetEdgesExpiredBetweenForNode returns edges for a specific node that expired
// during the specified time range.
func (q *BetweenQuerier) GetEdgesExpiredBetweenForNode(ctx context.Context, nodeID string, start, end time.Time) ([]vectorgraphdb.TemporalEdge, error) {
	if q.db == nil {
		return nil, ErrNilDB
	}

	if !start.Before(end) {
		return nil, ErrInvalidTimeRange
	}

	startUnix := start.Unix()
	endUnix := end.Unix()

	query := baseTemporalEdgeSelect + `
		WHERE source_id = ?
		AND valid_to IS NOT NULL
		AND valid_to >= ?
		AND valid_to < ?
		ORDER BY valid_to ASC
	`

	rows, err := q.db.QueryContext(ctx, query, nodeID, startUnix, endUnix)
	if err != nil {
		return nil, fmt.Errorf("query edges expired between %v and %v for node %s: %w", start, end, nodeID, err)
	}
	defer rows.Close()

	return scanTemporalEdges(rows)
}
