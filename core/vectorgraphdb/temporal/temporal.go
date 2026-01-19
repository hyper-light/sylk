// Package temporal provides temporal query capabilities for VectorGraphDB.
// It implements point-in-time (AsOf) and range-based (Between) queries
// for edges with bitemporal tracking.
package temporal

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrNilDB indicates a nil database connection was provided.
	ErrNilDB = errors.New("database connection is nil")

	// ErrInvalidTimeRange indicates start time is after end time.
	ErrInvalidTimeRange = errors.New("start time must be before end time")

	// ErrNoResults indicates no results were found.
	ErrNoResults = errors.New("no results found")
)

// =============================================================================
// SQL Query Constants
// =============================================================================

// Base query for selecting temporal edges.
const baseTemporalEdgeSelect = `
	SELECT
		id, source_id, target_id, edge_type, weight, metadata, created_at,
		valid_from, valid_to, tx_start, tx_end
	FROM edges
`

// =============================================================================
// Helper Functions
// =============================================================================

// scanTemporalEdge scans a single row into a TemporalEdge.
// This is the primary helper for converting SQL rows to domain objects.
func scanTemporalEdge(rows *sql.Rows) (*vectorgraphdb.TemporalEdge, error) {
	var (
		edge        vectorgraphdb.TemporalEdge
		metadataJSON sql.NullString
		createdAt   string
		validFrom   sql.NullInt64
		validTo     sql.NullInt64
		txStart     sql.NullInt64
		txEnd       sql.NullInt64
	)

	err := rows.Scan(
		&edge.ID,
		&edge.SourceID,
		&edge.TargetID,
		&edge.EdgeType,
		&edge.Weight,
		&metadataJSON,
		&createdAt,
		&validFrom,
		&validTo,
		&txStart,
		&txEnd,
	)
	if err != nil {
		return nil, fmt.Errorf("scan temporal edge: %w", err)
	}

	// Parse metadata JSON
	if metadataJSON.Valid && metadataJSON.String != "" {
		if err := json.Unmarshal([]byte(metadataJSON.String), &edge.Metadata); err != nil {
			edge.Metadata = make(map[string]any)
		}
	} else {
		edge.Metadata = make(map[string]any)
	}

	// Parse created_at timestamp
	if createdAt != "" {
		if parsed, err := time.Parse(time.RFC3339, createdAt); err == nil {
			edge.CreatedAt = parsed
		}
	}

	// Parse temporal fields (stored as Unix timestamps in INTEGER columns)
	if validFrom.Valid {
		t := time.Unix(validFrom.Int64, 0)
		edge.ValidFrom = &t
	}
	if validTo.Valid {
		t := time.Unix(validTo.Int64, 0)
		edge.ValidTo = &t
	}
	if txStart.Valid {
		t := time.Unix(txStart.Int64, 0)
		edge.TxStart = &t
	}
	if txEnd.Valid {
		t := time.Unix(txEnd.Int64, 0)
		edge.TxEnd = &t
	}

	return &edge, nil
}

// scanTemporalEdges scans all rows into a slice of TemporalEdges.
func scanTemporalEdges(rows *sql.Rows) ([]vectorgraphdb.TemporalEdge, error) {
	var edges []vectorgraphdb.TemporalEdge

	for rows.Next() {
		edge, err := scanTemporalEdge(rows)
		if err != nil {
			return nil, err
		}
		edges = append(edges, *edge)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}

	return edges, nil
}

// timeToUnix converts a time.Time to Unix timestamp for SQL queries.
// Returns nil if the time is zero.
func timeToUnix(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t.Unix()
}
