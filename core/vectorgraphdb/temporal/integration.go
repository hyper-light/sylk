// Package temporal provides temporal query capabilities for VectorGraphDB.
// This file implements TG.4.1 - TemporalDB wrapper/extension that adds
// temporal methods to VectorGraphDB.
package temporal

import (
	"context"
	"database/sql"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// =============================================================================
// TemporalDB (TG.4.1)
// =============================================================================

// TemporalDB wraps a database connection with temporal query and write capabilities.
// It provides a unified interface for temporal operations by composing
// the various temporal components (queriers, differ, writer).
//
// Usage:
//
//	db, _ := vectorgraphdb.Open("path/to/db")
//	tdb := temporal.NewTemporalDB(db.DB())
//	edges, _ := tdb.GetEdgesAsOf(ctx, "nodeID", time.Now().Add(-24*time.Hour))
type TemporalDB struct {
	db             *sql.DB
	asOfQuerier    *AsOfQuerier
	betweenQuerier *BetweenQuerier
	historyQuerier *EdgeHistoryQuerier
	differ         *TemporalDiffer
	writer         *TemporalEdgeWriter
}

// NewTemporalDB creates a new TemporalDB wrapper for the given SQL database connection.
// It initializes all temporal components (queriers, differ, writer).
//
// Returns nil if db is nil.
func NewTemporalDB(db *sql.DB) *TemporalDB {
	if db == nil {
		return nil
	}

	return &TemporalDB{
		db:             db,
		asOfQuerier:    NewAsOfQuerier(db),
		betweenQuerier: NewBetweenQuerier(db),
		historyQuerier: NewEdgeHistoryQuerier(db),
		differ:         NewTemporalDiffer(db),
		writer:         NewTemporalEdgeWriter(db),
	}
}

// =============================================================================
// Database Access
// =============================================================================

// DB returns the underlying SQL database connection.
func (t *TemporalDB) DB() *sql.DB {
	return t.db
}

// =============================================================================
// AsOf Queries (Point-in-Time)
// =============================================================================

// GetEdgesAsOf returns all edges for a node that were valid at the specified
// point in time. An edge is considered valid at time t if:
//   - valid_from <= t AND (valid_to IS NULL OR valid_to > t)
//
// This implements the standard temporal "as of" semantics where we want to see
// the state of the graph at a specific historical moment.
func (t *TemporalDB) GetEdgesAsOf(ctx context.Context, nodeID string, asOf time.Time) ([]vectorgraphdb.TemporalEdge, error) {
	return t.asOfQuerier.GetEdgesAsOf(ctx, nodeID, asOf)
}

// GetEdgesAsOfWithTarget returns edges between two specific nodes that were valid
// at the specified point in time.
func (t *TemporalDB) GetEdgesAsOfWithTarget(ctx context.Context, sourceID, targetID string, asOf time.Time) ([]vectorgraphdb.TemporalEdge, error) {
	return t.asOfQuerier.GetEdgesAsOfWithTarget(ctx, sourceID, targetID, asOf)
}

// GetIncomingEdgesAsOf returns edges pointing to the specified node that were valid
// at the specified point in time.
func (t *TemporalDB) GetIncomingEdgesAsOf(ctx context.Context, nodeID string, asOf time.Time) ([]vectorgraphdb.TemporalEdge, error) {
	return t.asOfQuerier.GetIncomingEdgesAsOf(ctx, nodeID, asOf)
}

// GetEdgesAsOfByType returns edges of a specific type for a node that were valid
// at the specified point in time.
func (t *TemporalDB) GetEdgesAsOfByType(ctx context.Context, nodeID string, edgeType vectorgraphdb.EdgeType, asOf time.Time) ([]vectorgraphdb.TemporalEdge, error) {
	return t.asOfQuerier.GetEdgesAsOfByType(ctx, nodeID, edgeType, asOf)
}

// GetNodesAsOf returns all nodes that existed at the specified point in time.
func (t *TemporalDB) GetNodesAsOf(ctx context.Context, asOf time.Time) ([]vectorgraphdb.GraphNode, error) {
	return t.asOfQuerier.GetNodesAsOf(ctx, asOf)
}

// =============================================================================
// Between Queries (Range-Based)
// =============================================================================

// GetEdgesBetween returns all edges for a node that were valid during the
// specified time range. An edge is considered valid during a range [start, end)
// if there is any overlap between the edge's valid period and the query range.
func (t *TemporalDB) GetEdgesBetween(ctx context.Context, nodeID string, start, end time.Time) ([]vectorgraphdb.TemporalEdge, error) {
	return t.betweenQuerier.GetEdgesBetween(ctx, nodeID, start, end)
}

// GetEdgesCreatedBetween returns all edges that were created (valid_from) during
// the specified time range [start, end).
func (t *TemporalDB) GetEdgesCreatedBetween(ctx context.Context, start, end time.Time) ([]vectorgraphdb.TemporalEdge, error) {
	return t.betweenQuerier.GetEdgesCreatedBetween(ctx, start, end)
}

// GetEdgesExpiredBetween returns all edges that expired (valid_to) during
// the specified time range [start, end).
func (t *TemporalDB) GetEdgesExpiredBetween(ctx context.Context, start, end time.Time) ([]vectorgraphdb.TemporalEdge, error) {
	return t.betweenQuerier.GetEdgesExpiredBetween(ctx, start, end)
}

// GetEdgesBetweenWithTarget returns edges between two specific nodes that were valid
// during the specified time range.
func (t *TemporalDB) GetEdgesBetweenWithTarget(ctx context.Context, sourceID, targetID string, start, end time.Time) ([]vectorgraphdb.TemporalEdge, error) {
	return t.betweenQuerier.GetEdgesBetweenWithTarget(ctx, sourceID, targetID, start, end)
}

// GetIncomingEdgesBetween returns edges pointing to the specified node that were valid
// during the specified time range.
func (t *TemporalDB) GetIncomingEdgesBetween(ctx context.Context, nodeID string, start, end time.Time) ([]vectorgraphdb.TemporalEdge, error) {
	return t.betweenQuerier.GetIncomingEdgesBetween(ctx, nodeID, start, end)
}

// GetEdgesBetweenByType returns edges of a specific type for a node that were valid
// during the specified time range.
func (t *TemporalDB) GetEdgesBetweenByType(ctx context.Context, nodeID string, edgeType vectorgraphdb.EdgeType, start, end time.Time) ([]vectorgraphdb.TemporalEdge, error) {
	return t.betweenQuerier.GetEdgesBetweenByType(ctx, nodeID, edgeType, start, end)
}

// GetEdgesModifiedBetween returns edges that had any change during the specified
// time range. This includes created, expired, or superseded edges.
func (t *TemporalDB) GetEdgesModifiedBetween(ctx context.Context, start, end time.Time) ([]vectorgraphdb.TemporalEdge, error) {
	return t.betweenQuerier.GetEdgesModifiedBetween(ctx, start, end)
}

// =============================================================================
// History Queries (Version History)
// =============================================================================

// GetEdgeHistory returns all versions of an edge over time, ordered by transaction
// start time (most recent first). This includes the current version, all historical
// versions, and soft-deleted versions.
func (t *TemporalDB) GetEdgeHistory(ctx context.Context, edgeID int64) ([]vectorgraphdb.TemporalEdge, error) {
	return t.historyQuerier.GetEdgeHistory(ctx, edgeID)
}

// GetEdgeVersionAt returns the specific version of an edge that was current at
// a given point in time. This uses transaction time (tx_start, tx_end) to determine
// which version was the "truth" at that moment in the database.
func (t *TemporalDB) GetEdgeVersionAt(ctx context.Context, edgeID int64, asOf time.Time) (*vectorgraphdb.TemporalEdge, error) {
	return t.historyQuerier.GetEdgeVersionAt(ctx, edgeID, asOf)
}

// GetEdgeVersionCount returns the total number of versions for an edge.
func (t *TemporalDB) GetEdgeVersionCount(ctx context.Context, edgeID int64) (int, error) {
	return t.historyQuerier.GetEdgeVersionCount(ctx, edgeID)
}

// GetEdgeHistoryBySourceTarget returns all versions of edges between two specific
// nodes, ordered by transaction start time (most recent first).
func (t *TemporalDB) GetEdgeHistoryBySourceTarget(ctx context.Context, sourceID, targetID string) ([]vectorgraphdb.TemporalEdge, error) {
	return t.historyQuerier.GetEdgeHistoryBySourceTarget(ctx, sourceID, targetID)
}

// GetEdgeHistoryForNode returns all versions of all edges originating from
// a specific node, ordered by transaction start time (most recent first).
func (t *TemporalDB) GetEdgeHistoryForNode(ctx context.Context, nodeID string) ([]vectorgraphdb.TemporalEdge, error) {
	return t.historyQuerier.GetEdgeHistoryForNode(ctx, nodeID)
}

// GetSupersededEdges returns all edges that have been superseded (tx_end IS NOT NULL).
func (t *TemporalDB) GetSupersededEdges(ctx context.Context) ([]vectorgraphdb.TemporalEdge, error) {
	return t.historyQuerier.GetSupersededEdges(ctx)
}

// GetSoftDeletedEdges returns all edges that have been soft-deleted
// (valid_to IS NOT NULL), indicating they are no longer valid in the real world.
func (t *TemporalDB) GetSoftDeletedEdges(ctx context.Context) ([]vectorgraphdb.TemporalEdge, error) {
	return t.historyQuerier.GetSoftDeletedEdges(ctx)
}

// =============================================================================
// Diff Operations (Graph Comparison)
// =============================================================================

// DiffGraph computes the differences between the graph at time t1 and t2.
// It returns a GraphDiff containing edges that were added, removed, or modified
// during that period.
func (t *TemporalDB) DiffGraph(ctx context.Context, t1, t2 time.Time) (*GraphDiff, error) {
	return t.differ.DiffGraph(ctx, t1, t2)
}

// DiffNode computes the differences for a specific node's edges between t1 and t2.
// This is more focused than DiffGraph and only returns changes for edges
// where the specified node is the source.
func (t *TemporalDB) DiffNode(ctx context.Context, nodeID string, t1, t2 time.Time) (*GraphDiff, error) {
	return t.differ.DiffNode(ctx, nodeID, t1, t2)
}

// DiffBetweenNodes computes the differences for edges between two specific nodes.
func (t *TemporalDB) DiffBetweenNodes(ctx context.Context, sourceID, targetID string, t1, t2 time.Time) (*GraphDiff, error) {
	return t.differ.DiffBetweenNodes(ctx, sourceID, targetID, t1, t2)
}

// =============================================================================
// Write Operations (Temporal Edge Management)
// =============================================================================

// CreateTemporalEdge creates a new temporal edge with the specified valid time range.
// The transaction start time (tx_start) is automatically set to the current time.
// The transaction end time (tx_end) is NULL, indicating this is the current version.
//
// Parameters:
//   - edge: The temporal edge to create (ID will be set after insert)
//   - validFrom: When the edge relationship became valid (nil = always valid)
//   - validTo: When the edge relationship ceased to be valid (nil = currently valid)
func (t *TemporalDB) CreateTemporalEdge(ctx context.Context, edge *vectorgraphdb.TemporalEdge, validFrom, validTo *time.Time) error {
	return t.writer.CreateEdge(ctx, edge, validFrom, validTo)
}

// UpdateTemporalEdge creates a new version of an edge with updated data.
// This implements temporal versioning - the old version is marked as superseded
// (tx_end set) and a new version is created with the new data.
//
// Parameters:
//   - edgeID: The ID of the edge to update
//   - newData: Map of fields to update (weight, metadata, edge_type supported)
//   - validFrom: New valid_from time (nil keeps existing)
func (t *TemporalDB) UpdateTemporalEdge(ctx context.Context, edgeID int64, newData map[string]any, validFrom *time.Time) error {
	return t.writer.UpdateEdge(ctx, edgeID, newData, validFrom)
}

// DeleteTemporalEdge performs a soft delete by setting valid_to to the specified time.
// The edge is not physically removed from the database, allowing historical
// queries to still find it.
func (t *TemporalDB) DeleteTemporalEdge(ctx context.Context, edgeID int64, deletedAt time.Time) error {
	return t.writer.DeleteEdge(ctx, edgeID, deletedAt)
}

// RestoreTemporalEdge clears valid_to to restore a soft-deleted edge.
func (t *TemporalDB) RestoreTemporalEdge(ctx context.Context, edgeID int64) error {
	return t.writer.RestoreEdge(ctx, edgeID)
}

// HardDeleteTemporalEdge physically removes all versions of an edge from the database.
// WARNING: This operation is irreversible and removes all historical versions.
func (t *TemporalDB) HardDeleteTemporalEdge(ctx context.Context, edgeID int64) error {
	return t.writer.HardDeleteEdge(ctx, edgeID)
}

// SupersedeTemporalEdge creates a new version of an edge and marks the old one as superseded.
// Unlike UpdateTemporalEdge which modifies specific fields, this replaces the entire edge
// with a new TemporalEdge instance.
func (t *TemporalDB) SupersedeTemporalEdge(ctx context.Context, edgeID int64, newEdge *vectorgraphdb.TemporalEdge) error {
	return t.writer.SupersedeEdge(ctx, edgeID, newEdge)
}

// SetTemporalValidTimeRange updates the valid time range for an edge.
// This is useful for correcting historical data without creating a new version.
func (t *TemporalDB) SetTemporalValidTimeRange(ctx context.Context, edgeID int64, validFrom, validTo *time.Time) error {
	return t.writer.SetValidTimeRange(ctx, edgeID, validFrom, validTo)
}

// GetCurrentTemporalEdge retrieves the current (non-superseded) version of an edge.
func (t *TemporalDB) GetCurrentTemporalEdge(ctx context.Context, edgeID int64) (*vectorgraphdb.TemporalEdge, error) {
	return t.writer.GetCurrentEdge(ctx, edgeID)
}

// GetLatestTemporalEdgeBetween retrieves the latest current edge between two nodes.
func (t *TemporalDB) GetLatestTemporalEdgeBetween(ctx context.Context, sourceID, targetID string) (*vectorgraphdb.TemporalEdge, error) {
	return t.writer.GetLatestEdgeBetween(ctx, sourceID, targetID)
}

// =============================================================================
// Component Access
// =============================================================================

// AsOfQuerier returns the underlying AsOfQuerier for direct access.
// This is useful when you need to use methods not exposed by TemporalDB.
func (t *TemporalDB) AsOfQuerier() *AsOfQuerier {
	return t.asOfQuerier
}

// BetweenQuerier returns the underlying BetweenQuerier for direct access.
func (t *TemporalDB) BetweenQuerier() *BetweenQuerier {
	return t.betweenQuerier
}

// HistoryQuerier returns the underlying EdgeHistoryQuerier for direct access.
func (t *TemporalDB) HistoryQuerier() *EdgeHistoryQuerier {
	return t.historyQuerier
}

// Differ returns the underlying TemporalDiffer for direct access.
func (t *TemporalDB) Differ() *TemporalDiffer {
	return t.differ
}

// Writer returns the underlying TemporalEdgeWriter for direct access.
func (t *TemporalDB) Writer() *TemporalEdgeWriter {
	return t.writer
}
