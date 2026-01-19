// Package temporal provides temporal query and write capabilities for VectorGraphDB.
// This file implements TemporalEdgeWriter (TG.3.1) for creating, updating, and
// deleting temporal edges with bitemporal tracking.
package temporal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// =============================================================================
// Writer Errors
// =============================================================================

var (
	// ErrNilEdge indicates a nil edge was provided.
	ErrNilEdge = errors.New("edge is nil")

	// ErrEdgeNotFound indicates the specified edge does not exist.
	ErrEdgeNotFound = errors.New("edge not found")

	// ErrInvalidValidTimeRange indicates valid_from is after valid_to.
	ErrInvalidValidTimeRange = errors.New("valid_from must be before or equal to valid_to")

	// ErrEdgeAlreadyDeleted indicates the edge has already been soft-deleted.
	ErrEdgeAlreadyDeleted = errors.New("edge is already deleted")

	// ErrEdgeNotDeleted indicates the edge is not soft-deleted (for restore).
	ErrEdgeNotDeleted = errors.New("edge is not deleted")

	// ErrEdgeSuperseded indicates the edge has been superseded and cannot be modified.
	ErrEdgeSuperseded = errors.New("edge has been superseded")
)

// =============================================================================
// TemporalEdgeWriter (TG.3.1)
// =============================================================================

// TemporalEdgeWriter provides write operations for temporal edges with
// bitemporal tracking. It manages both valid time (when relationships are
// true in the real world) and transaction time (when changes are recorded).
//
// Temporal Integrity Rules:
//  1. valid_from <= valid_to (if valid_to is not nil)
//  2. tx_start is always set on insert
//  3. tx_end is NULL for current versions
//  4. When updating, old version gets tx_end = now
//  5. Multi-row operations use transactions
type TemporalEdgeWriter struct {
	db *sql.DB
}

// NewTemporalEdgeWriter creates a new TemporalEdgeWriter with the given database connection.
func NewTemporalEdgeWriter(db *sql.DB) *TemporalEdgeWriter {
	return &TemporalEdgeWriter{db: db}
}

// =============================================================================
// Create Operations
// =============================================================================

// CreateEdge creates a new temporal edge with the specified valid time range.
// The transaction start time (tx_start) is automatically set to the current time.
// The transaction end time (tx_end) is NULL, indicating this is the current version.
//
// Parameters:
//   - edge: The temporal edge to create (ID will be set after insert)
//   - validFrom: When the edge relationship became valid (nil = always valid)
//   - validTo: When the edge relationship ceased to be valid (nil = currently valid)
//
// Returns the ID of the newly created edge.
func (w *TemporalEdgeWriter) CreateEdge(ctx context.Context, edge *vectorgraphdb.TemporalEdge, validFrom, validTo *time.Time) error {
	if w.db == nil {
		return ErrNilDB
	}
	if edge == nil {
		return ErrNilEdge
	}

	// Validate temporal integrity: valid_from <= valid_to
	if validFrom != nil && validTo != nil && validFrom.After(*validTo) {
		return ErrInvalidValidTimeRange
	}

	now := time.Now()

	// Set temporal fields on the edge
	edge.ValidFrom = validFrom
	edge.ValidTo = validTo
	edge.TxStart = &now
	edge.TxEnd = nil // Current version

	// Serialize metadata to JSON
	metadataJSON := "{}"
	if edge.Metadata != nil {
		data, err := json.Marshal(edge.Metadata)
		if err == nil {
			metadataJSON = string(data)
		}
	}

	// Convert times to Unix timestamps for storage
	var vfUnix, vtUnix interface{}
	if validFrom != nil {
		vfUnix = validFrom.Unix()
	}
	if validTo != nil {
		vtUnix = validTo.Unix()
	}
	txStartUnix := now.Unix()

	query := `
		INSERT INTO edges (source_id, target_id, edge_type, weight, metadata, created_at, valid_from, valid_to, tx_start, tx_end)
		VALUES (?, ?, ?, ?, ?, datetime('now'), ?, ?, ?, NULL)
	`

	result, err := w.db.ExecContext(ctx, query,
		edge.SourceID,
		edge.TargetID,
		edge.EdgeType,
		edge.Weight,
		metadataJSON,
		vfUnix,
		vtUnix,
		txStartUnix,
	)
	if err != nil {
		return fmt.Errorf("create temporal edge: %w", err)
	}

	// Set the ID on the edge
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("get last insert id: %w", err)
	}
	edge.ID = id

	return nil
}

// =============================================================================
// Update Operations
// =============================================================================

// UpdateEdge creates a new version of an edge with updated data.
// This implements temporal versioning - the old version is marked as superseded
// (tx_end set) and a new version is created with the new data.
//
// This is NOT an in-place update. The original edge is preserved for historical
// queries, and a new row is created representing the current state.
//
// Parameters:
//   - edgeID: The ID of the edge to update
//   - newData: Map of fields to update (weight, metadata, edge_type supported)
//   - validFrom: New valid_from time (nil keeps existing)
//
// Returns the ID of the newly created version.
func (w *TemporalEdgeWriter) UpdateEdge(ctx context.Context, edgeID int64, newData map[string]any, validFrom *time.Time) error {
	if w.db == nil {
		return ErrNilDB
	}

	// Start a transaction for atomicity
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	now := time.Now()
	nowUnix := now.Unix()

	// Get the current version of the edge
	currentEdge, err := w.getEdgeByIDTx(ctx, tx, edgeID)
	if err != nil {
		return err
	}

	// Check if the edge is already superseded
	if currentEdge.TxEnd != nil {
		return ErrEdgeSuperseded
	}

	// Mark the old version as superseded
	_, err = tx.ExecContext(ctx, `
		UPDATE edges SET tx_end = ? WHERE id = ? AND tx_end IS NULL
	`, nowUnix, edgeID)
	if err != nil {
		return fmt.Errorf("supersede old version: %w", err)
	}

	// Prepare the new version with updated fields
	newWeight := currentEdge.Weight
	if val, ok := newData["weight"].(float64); ok {
		newWeight = val
	}

	newMetadata := currentEdge.Metadata
	if val, ok := newData["metadata"].(map[string]any); ok {
		newMetadata = val
	}

	newEdgeType := currentEdge.EdgeType
	if val, ok := newData["edge_type"].(vectorgraphdb.EdgeType); ok {
		newEdgeType = val
	}

	newValidFrom := currentEdge.ValidFrom
	if validFrom != nil {
		newValidFrom = validFrom
	}

	// Validate temporal integrity
	if newValidFrom != nil && currentEdge.ValidTo != nil && newValidFrom.After(*currentEdge.ValidTo) {
		return ErrInvalidValidTimeRange
	}

	// Serialize metadata
	metadataJSON := "{}"
	if newMetadata != nil {
		data, err := json.Marshal(newMetadata)
		if err == nil {
			metadataJSON = string(data)
		}
	}

	// Convert times to Unix
	var vfUnix, vtUnix interface{}
	if newValidFrom != nil {
		vfUnix = newValidFrom.Unix()
	}
	if currentEdge.ValidTo != nil {
		vtUnix = currentEdge.ValidTo.Unix()
	}

	// Insert the new version
	_, err = tx.ExecContext(ctx, `
		INSERT INTO edges (source_id, target_id, edge_type, weight, metadata, created_at, valid_from, valid_to, tx_start, tx_end)
		VALUES (?, ?, ?, ?, ?, datetime('now'), ?, ?, ?, NULL)
	`,
		currentEdge.SourceID,
		currentEdge.TargetID,
		newEdgeType,
		newWeight,
		metadataJSON,
		vfUnix,
		vtUnix,
		nowUnix,
	)
	if err != nil {
		return fmt.Errorf("insert new version: %w", err)
	}

	return tx.Commit()
}

// =============================================================================
// Delete Operations
// =============================================================================

// DeleteEdge performs a soft delete by setting valid_to to the specified time.
// The edge is not physically removed from the database, allowing historical
// queries to still find it.
//
// This represents "the relationship ceased to exist at this time" in the
// valid time dimension.
func (w *TemporalEdgeWriter) DeleteEdge(ctx context.Context, edgeID int64, deletedAt time.Time) error {
	if w.db == nil {
		return ErrNilDB
	}

	// Check if edge exists and is not already deleted
	edge, err := w.getEdgeByID(ctx, edgeID)
	if err != nil {
		return err
	}

	if edge.ValidTo != nil {
		return ErrEdgeAlreadyDeleted
	}

	deletedAtUnix := deletedAt.Unix()

	// Validate temporal integrity
	if edge.ValidFrom != nil && edge.ValidFrom.After(deletedAt) {
		return ErrInvalidValidTimeRange
	}

	result, err := w.db.ExecContext(ctx, `
		UPDATE edges SET valid_to = ? WHERE id = ? AND valid_to IS NULL AND tx_end IS NULL
	`, deletedAtUnix, edgeID)
	if err != nil {
		return fmt.Errorf("soft delete edge: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return ErrEdgeSuperseded
	}

	return nil
}

// RestoreEdge clears valid_to to restore a soft-deleted edge.
// This makes the edge valid again (currently valid with no end time).
func (w *TemporalEdgeWriter) RestoreEdge(ctx context.Context, edgeID int64) error {
	if w.db == nil {
		return ErrNilDB
	}

	// Check if edge exists and is deleted
	edge, err := w.getEdgeByID(ctx, edgeID)
	if err != nil {
		return err
	}

	if edge.ValidTo == nil {
		return ErrEdgeNotDeleted
	}

	// Check if edge is superseded
	if edge.TxEnd != nil {
		return ErrEdgeSuperseded
	}

	result, err := w.db.ExecContext(ctx, `
		UPDATE edges SET valid_to = NULL WHERE id = ? AND valid_to IS NOT NULL AND tx_end IS NULL
	`, edgeID)
	if err != nil {
		return fmt.Errorf("restore edge: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return ErrEdgeSuperseded
	}

	return nil
}

// HardDeleteEdge physically removes all versions of an edge from the database.
// This is intended for GDPR compliance or data purging scenarios where
// historical data must be completely removed.
//
// WARNING: This operation is irreversible and removes all historical versions.
func (w *TemporalEdgeWriter) HardDeleteEdge(ctx context.Context, edgeID int64) error {
	if w.db == nil {
		return ErrNilDB
	}

	// Check if edge exists
	_, err := w.getEdgeByID(ctx, edgeID)
	if err != nil {
		return err
	}

	// Delete the edge and all its versions
	// Note: For temporal versioning where versions share the same ID,
	// this deletes all versions. For separate-row versioning, this
	// deletes only this specific row.
	_, err = w.db.ExecContext(ctx, `DELETE FROM edges WHERE id = ?`, edgeID)
	if err != nil {
		return fmt.Errorf("hard delete edge: %w", err)
	}

	return nil
}

// =============================================================================
// Version Management
// =============================================================================

// SupersedeEdge creates a new version of an edge and marks the old one as superseded.
// Unlike UpdateEdge which modifies specific fields, this replaces the entire edge
// with a new TemporalEdge instance.
//
// The old edge gets tx_end set to now, and the new edge gets tx_start set to now.
func (w *TemporalEdgeWriter) SupersedeEdge(ctx context.Context, edgeID int64, newEdge *vectorgraphdb.TemporalEdge) error {
	if w.db == nil {
		return ErrNilDB
	}
	if newEdge == nil {
		return ErrNilEdge
	}

	// Start a transaction for atomicity
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	now := time.Now()
	nowUnix := now.Unix()

	// Get the current version
	currentEdge, err := w.getEdgeByIDTx(ctx, tx, edgeID)
	if err != nil {
		return err
	}

	// Check if already superseded
	if currentEdge.TxEnd != nil {
		return ErrEdgeSuperseded
	}

	// Validate temporal integrity on new edge
	if newEdge.ValidFrom != nil && newEdge.ValidTo != nil && newEdge.ValidFrom.After(*newEdge.ValidTo) {
		return ErrInvalidValidTimeRange
	}

	// Mark the old version as superseded
	_, err = tx.ExecContext(ctx, `
		UPDATE edges SET tx_end = ? WHERE id = ? AND tx_end IS NULL
	`, nowUnix, edgeID)
	if err != nil {
		return fmt.Errorf("supersede old version: %w", err)
	}

	// Set transaction time on new edge
	newEdge.TxStart = &now
	newEdge.TxEnd = nil

	// Serialize metadata
	metadataJSON := "{}"
	if newEdge.Metadata != nil {
		data, err := json.Marshal(newEdge.Metadata)
		if err == nil {
			metadataJSON = string(data)
		}
	}

	// Convert times to Unix
	var vfUnix, vtUnix interface{}
	if newEdge.ValidFrom != nil {
		vfUnix = newEdge.ValidFrom.Unix()
	}
	if newEdge.ValidTo != nil {
		vtUnix = newEdge.ValidTo.Unix()
	}

	// Insert the new version
	result, err := tx.ExecContext(ctx, `
		INSERT INTO edges (source_id, target_id, edge_type, weight, metadata, created_at, valid_from, valid_to, tx_start, tx_end)
		VALUES (?, ?, ?, ?, ?, datetime('now'), ?, ?, ?, NULL)
	`,
		newEdge.SourceID,
		newEdge.TargetID,
		newEdge.EdgeType,
		newEdge.Weight,
		metadataJSON,
		vfUnix,
		vtUnix,
		nowUnix,
	)
	if err != nil {
		return fmt.Errorf("insert superseding edge: %w", err)
	}

	// Set the ID on the new edge
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("get last insert id: %w", err)
	}
	newEdge.ID = id

	return tx.Commit()
}

// SetValidTimeRange updates the valid time range for an edge.
// This is useful for correcting historical data without creating a new version.
//
// Note: This modifies the edge in place. For versioned updates, use UpdateEdge.
func (w *TemporalEdgeWriter) SetValidTimeRange(ctx context.Context, edgeID int64, validFrom, validTo *time.Time) error {
	if w.db == nil {
		return ErrNilDB
	}

	// Validate temporal integrity
	if validFrom != nil && validTo != nil && validFrom.After(*validTo) {
		return ErrInvalidValidTimeRange
	}

	// Check if edge exists and is not superseded
	edge, err := w.getEdgeByID(ctx, edgeID)
	if err != nil {
		return err
	}

	if edge.TxEnd != nil {
		return ErrEdgeSuperseded
	}

	// Convert times to Unix
	var vfUnix, vtUnix interface{}
	if validFrom != nil {
		vfUnix = validFrom.Unix()
	}
	if validTo != nil {
		vtUnix = validTo.Unix()
	}

	result, err := w.db.ExecContext(ctx, `
		UPDATE edges SET valid_from = ?, valid_to = ? WHERE id = ? AND tx_end IS NULL
	`, vfUnix, vtUnix, edgeID)
	if err != nil {
		return fmt.Errorf("set valid time range: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return ErrEdgeSuperseded
	}

	return nil
}

// =============================================================================
// Helper Methods
// =============================================================================

// getEdgeByID retrieves an edge by its ID.
func (w *TemporalEdgeWriter) getEdgeByID(ctx context.Context, edgeID int64) (*vectorgraphdb.TemporalEdge, error) {
	query := baseTemporalEdgeSelect + ` WHERE id = ?`

	rows, err := w.db.QueryContext(ctx, query, edgeID)
	if err != nil {
		return nil, fmt.Errorf("query edge by id: %w", err)
	}
	defer rows.Close()

	edges, err := scanTemporalEdges(rows)
	if err != nil {
		return nil, err
	}

	if len(edges) == 0 {
		return nil, ErrEdgeNotFound
	}

	return &edges[0], nil
}

// getEdgeByIDTx retrieves an edge by its ID within a transaction.
func (w *TemporalEdgeWriter) getEdgeByIDTx(ctx context.Context, tx *sql.Tx, edgeID int64) (*vectorgraphdb.TemporalEdge, error) {
	query := baseTemporalEdgeSelect + ` WHERE id = ?`

	rows, err := tx.QueryContext(ctx, query, edgeID)
	if err != nil {
		return nil, fmt.Errorf("query edge by id: %w", err)
	}
	defer rows.Close()

	edges, err := scanTemporalEdges(rows)
	if err != nil {
		return nil, err
	}

	if len(edges) == 0 {
		return nil, ErrEdgeNotFound
	}

	return &edges[0], nil
}

// GetCurrentEdge retrieves the current (non-superseded) version of an edge.
// Returns ErrEdgeNotFound if no current version exists.
func (w *TemporalEdgeWriter) GetCurrentEdge(ctx context.Context, edgeID int64) (*vectorgraphdb.TemporalEdge, error) {
	if w.db == nil {
		return nil, ErrNilDB
	}

	query := baseTemporalEdgeSelect + ` WHERE id = ? AND tx_end IS NULL`

	rows, err := w.db.QueryContext(ctx, query, edgeID)
	if err != nil {
		return nil, fmt.Errorf("query current edge: %w", err)
	}
	defer rows.Close()

	edges, err := scanTemporalEdges(rows)
	if err != nil {
		return nil, err
	}

	if len(edges) == 0 {
		return nil, ErrEdgeNotFound
	}

	return &edges[0], nil
}

// GetLatestEdgeBetween retrieves the latest current edge between two nodes.
// Returns ErrEdgeNotFound if no edge exists.
func (w *TemporalEdgeWriter) GetLatestEdgeBetween(ctx context.Context, sourceID, targetID string) (*vectorgraphdb.TemporalEdge, error) {
	if w.db == nil {
		return nil, ErrNilDB
	}

	query := baseTemporalEdgeSelect + `
		WHERE source_id = ? AND target_id = ? AND tx_end IS NULL
		ORDER BY tx_start DESC
		LIMIT 1
	`

	rows, err := w.db.QueryContext(ctx, query, sourceID, targetID)
	if err != nil {
		return nil, fmt.Errorf("query latest edge between nodes: %w", err)
	}
	defer rows.Close()

	edges, err := scanTemporalEdges(rows)
	if err != nil {
		return nil, err
	}

	if len(edges) == 0 {
		return nil, ErrEdgeNotFound
	}

	return &edges[0], nil
}
