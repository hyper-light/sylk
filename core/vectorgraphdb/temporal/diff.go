package temporal

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// =============================================================================
// TemporalDiffer (TG.2.2)
// =============================================================================

// TemporalDiffer provides diff operations for comparing graph state across time.
// It can compute the differences between two points in time, identifying edges
// that were added, removed, or modified during that period.
type TemporalDiffer struct {
	db *sql.DB
}

// NewTemporalDiffer creates a new TemporalDiffer with the given database connection.
func NewTemporalDiffer(db *sql.DB) *TemporalDiffer {
	return &TemporalDiffer{db: db}
}

// =============================================================================
// Diff Result Types
// =============================================================================

// GraphDiff represents the differences between two points in time.
// It contains edges that were added, removed, or modified during the time range.
type GraphDiff struct {
	// Added contains edges that started being valid during the range.
	// These are edges where valid_from >= T1 AND valid_from < T2.
	Added []vectorgraphdb.TemporalEdge `json:"added"`

	// Removed contains edges that stopped being valid during the range.
	// These are edges where valid_to >= T1 AND valid_to < T2.
	Removed []vectorgraphdb.TemporalEdge `json:"removed"`

	// Modified contains edges that were modified during the range.
	// These have transaction updates: tx_start >= T1 AND tx_start < T2 AND tx_end IS NOT NULL.
	Modified []EdgeModification `json:"modified"`

	// T1 is the start of the time range compared.
	T1 time.Time `json:"t1"`

	// T2 is the end of the time range compared.
	T2 time.Time `json:"t2"`
}

// EdgeModification represents a modification to an edge over time.
// It contains the before and after states along with a description of changes.
type EdgeModification struct {
	// Before is the state of the edge before the modification.
	// May be nil if the edge was created during the period.
	Before *vectorgraphdb.TemporalEdge `json:"before,omitempty"`

	// After is the state of the edge after the modification.
	// May be nil if the edge was deleted during the period.
	After *vectorgraphdb.TemporalEdge `json:"after,omitempty"`

	// Changes is a list of human-readable descriptions of what changed.
	Changes []string `json:"changes"`
}

// Summary returns a human-readable summary of the graph diff.
func (d *GraphDiff) Summary() string {
	if d == nil {
		return "No diff available"
	}

	var parts []string
	parts = append(parts, fmt.Sprintf("Time range: %s to %s", d.T1.Format(time.RFC3339), d.T2.Format(time.RFC3339)))

	if len(d.Added) > 0 {
		parts = append(parts, fmt.Sprintf("Added: %d edge(s)", len(d.Added)))
		for i, edge := range d.Added {
			if i >= 3 {
				parts = append(parts, fmt.Sprintf("  ... and %d more", len(d.Added)-3))
				break
			}
			parts = append(parts, fmt.Sprintf("  + %s -> %s (%s)", edge.SourceID, edge.TargetID, edge.EdgeType.String()))
		}
	}

	if len(d.Removed) > 0 {
		parts = append(parts, fmt.Sprintf("Removed: %d edge(s)", len(d.Removed)))
		for i, edge := range d.Removed {
			if i >= 3 {
				parts = append(parts, fmt.Sprintf("  ... and %d more", len(d.Removed)-3))
				break
			}
			parts = append(parts, fmt.Sprintf("  - %s -> %s (%s)", edge.SourceID, edge.TargetID, edge.EdgeType.String()))
		}
	}

	if len(d.Modified) > 0 {
		parts = append(parts, fmt.Sprintf("Modified: %d edge(s)", len(d.Modified)))
		for i, mod := range d.Modified {
			if i >= 3 {
				parts = append(parts, fmt.Sprintf("  ... and %d more", len(d.Modified)-3))
				break
			}
			if mod.After != nil {
				parts = append(parts, fmt.Sprintf("  ~ %s -> %s: %s", mod.After.SourceID, mod.After.TargetID, strings.Join(mod.Changes, ", ")))
			} else if mod.Before != nil {
				parts = append(parts, fmt.Sprintf("  ~ %s -> %s: %s", mod.Before.SourceID, mod.Before.TargetID, strings.Join(mod.Changes, ", ")))
			}
		}
	}

	if len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Modified) == 0 {
		parts = append(parts, "No changes detected")
	}

	return strings.Join(parts, "\n")
}

// IsEmpty returns true if the diff contains no changes.
func (d *GraphDiff) IsEmpty() bool {
	if d == nil {
		return true
	}
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Modified) == 0
}

// TotalChanges returns the total number of changes in the diff.
func (d *GraphDiff) TotalChanges() int {
	if d == nil {
		return 0
	}
	return len(d.Added) + len(d.Removed) + len(d.Modified)
}

// =============================================================================
// Diff Methods
// =============================================================================

// DiffGraph computes the differences between the graph at time t1 and t2.
// It returns edges that were added, removed, or modified during that period.
//
// SQL patterns used:
//   - Added: valid_from >= t1 AND valid_from < t2
//   - Removed: valid_to >= t1 AND valid_to < t2
//   - Modified: tx_start >= t1 AND tx_start < t2 AND tx_end IS NOT NULL
func (d *TemporalDiffer) DiffGraph(ctx context.Context, t1, t2 time.Time) (*GraphDiff, error) {
	if d.db == nil {
		return nil, ErrNilDB
	}

	if !t1.Before(t2) {
		return nil, ErrInvalidTimeRange
	}

	t1Unix := t1.Unix()
	t2Unix := t2.Unix()

	diff := &GraphDiff{
		T1:       t1,
		T2:       t2,
		Added:    make([]vectorgraphdb.TemporalEdge, 0),
		Removed:  make([]vectorgraphdb.TemporalEdge, 0),
		Modified: make([]EdgeModification, 0),
	}

	// Query for added edges (started being valid during the range)
	addedQuery := baseTemporalEdgeSelect + `
		WHERE valid_from >= ?
		AND valid_from < ?
		ORDER BY valid_from ASC
	`

	addedRows, err := d.db.QueryContext(ctx, addedQuery, t1Unix, t2Unix)
	if err != nil {
		return nil, fmt.Errorf("query added edges: %w", err)
	}
	defer addedRows.Close()

	diff.Added, err = scanTemporalEdges(addedRows)
	if err != nil {
		return nil, fmt.Errorf("scan added edges: %w", err)
	}

	// Query for removed edges (stopped being valid during the range)
	removedQuery := baseTemporalEdgeSelect + `
		WHERE valid_to IS NOT NULL
		AND valid_to >= ?
		AND valid_to < ?
		ORDER BY valid_to ASC
	`

	removedRows, err := d.db.QueryContext(ctx, removedQuery, t1Unix, t2Unix)
	if err != nil {
		return nil, fmt.Errorf("query removed edges: %w", err)
	}
	defer removedRows.Close()

	diff.Removed, err = scanTemporalEdges(removedRows)
	if err != nil {
		return nil, fmt.Errorf("scan removed edges: %w", err)
	}

	// Query for modified edges (transaction started during the range and was superseded)
	modifiedQuery := baseTemporalEdgeSelect + `
		WHERE tx_start IS NOT NULL
		AND tx_start >= ?
		AND tx_start < ?
		AND tx_end IS NOT NULL
		ORDER BY tx_start ASC
	`

	modifiedRows, err := d.db.QueryContext(ctx, modifiedQuery, t1Unix, t2Unix)
	if err != nil {
		return nil, fmt.Errorf("query modified edges: %w", err)
	}
	defer modifiedRows.Close()

	modifiedEdges, err := scanTemporalEdges(modifiedRows)
	if err != nil {
		return nil, fmt.Errorf("scan modified edges: %w", err)
	}

	// For each modified edge, try to find its successor
	for _, before := range modifiedEdges {
		mod := EdgeModification{
			Before:  copyTemporalEdge(&before),
			Changes: []string{},
		}

		// Try to find the successor (the edge that replaced this one)
		after, err := d.findSuccessorEdge(ctx, &before)
		if err == nil && after != nil {
			mod.After = after
			mod.Changes = computeEdgeChanges(&before, after)
		} else {
			mod.Changes = []string{"edge superseded"}
		}

		diff.Modified = append(diff.Modified, mod)
	}

	return diff, nil
}

// DiffNode computes the differences for a specific node's edges between t1 and t2.
// This is more focused than DiffGraph and only returns changes for edges
// where the specified node is the source.
func (d *TemporalDiffer) DiffNode(ctx context.Context, nodeID string, t1, t2 time.Time) (*GraphDiff, error) {
	if d.db == nil {
		return nil, ErrNilDB
	}

	if !t1.Before(t2) {
		return nil, ErrInvalidTimeRange
	}

	t1Unix := t1.Unix()
	t2Unix := t2.Unix()

	diff := &GraphDiff{
		T1:       t1,
		T2:       t2,
		Added:    make([]vectorgraphdb.TemporalEdge, 0),
		Removed:  make([]vectorgraphdb.TemporalEdge, 0),
		Modified: make([]EdgeModification, 0),
	}

	// Query for added edges for this node
	addedQuery := baseTemporalEdgeSelect + `
		WHERE source_id = ?
		AND valid_from >= ?
		AND valid_from < ?
		ORDER BY valid_from ASC
	`

	addedRows, err := d.db.QueryContext(ctx, addedQuery, nodeID, t1Unix, t2Unix)
	if err != nil {
		return nil, fmt.Errorf("query added edges for node %s: %w", nodeID, err)
	}
	defer addedRows.Close()

	diff.Added, err = scanTemporalEdges(addedRows)
	if err != nil {
		return nil, fmt.Errorf("scan added edges: %w", err)
	}

	// Query for removed edges for this node
	removedQuery := baseTemporalEdgeSelect + `
		WHERE source_id = ?
		AND valid_to IS NOT NULL
		AND valid_to >= ?
		AND valid_to < ?
		ORDER BY valid_to ASC
	`

	removedRows, err := d.db.QueryContext(ctx, removedQuery, nodeID, t1Unix, t2Unix)
	if err != nil {
		return nil, fmt.Errorf("query removed edges for node %s: %w", nodeID, err)
	}
	defer removedRows.Close()

	diff.Removed, err = scanTemporalEdges(removedRows)
	if err != nil {
		return nil, fmt.Errorf("scan removed edges: %w", err)
	}

	// Query for modified edges for this node
	modifiedQuery := baseTemporalEdgeSelect + `
		WHERE source_id = ?
		AND tx_start IS NOT NULL
		AND tx_start >= ?
		AND tx_start < ?
		AND tx_end IS NOT NULL
		ORDER BY tx_start ASC
	`

	modifiedRows, err := d.db.QueryContext(ctx, modifiedQuery, nodeID, t1Unix, t2Unix)
	if err != nil {
		return nil, fmt.Errorf("query modified edges for node %s: %w", nodeID, err)
	}
	defer modifiedRows.Close()

	modifiedEdges, err := scanTemporalEdges(modifiedRows)
	if err != nil {
		return nil, fmt.Errorf("scan modified edges: %w", err)
	}

	// For each modified edge, try to find its successor
	for _, before := range modifiedEdges {
		mod := EdgeModification{
			Before:  copyTemporalEdge(&before),
			Changes: []string{},
		}

		// Try to find the successor
		after, err := d.findSuccessorEdge(ctx, &before)
		if err == nil && after != nil {
			mod.After = after
			mod.Changes = computeEdgeChanges(&before, after)
		} else {
			mod.Changes = []string{"edge superseded"}
		}

		diff.Modified = append(diff.Modified, mod)
	}

	return diff, nil
}

// DiffBetweenNodes computes the differences for edges between two specific nodes.
func (d *TemporalDiffer) DiffBetweenNodes(ctx context.Context, sourceID, targetID string, t1, t2 time.Time) (*GraphDiff, error) {
	if d.db == nil {
		return nil, ErrNilDB
	}

	if !t1.Before(t2) {
		return nil, ErrInvalidTimeRange
	}

	t1Unix := t1.Unix()
	t2Unix := t2.Unix()

	diff := &GraphDiff{
		T1:       t1,
		T2:       t2,
		Added:    make([]vectorgraphdb.TemporalEdge, 0),
		Removed:  make([]vectorgraphdb.TemporalEdge, 0),
		Modified: make([]EdgeModification, 0),
	}

	// Query for added edges between these nodes
	addedQuery := baseTemporalEdgeSelect + `
		WHERE source_id = ?
		AND target_id = ?
		AND valid_from >= ?
		AND valid_from < ?
		ORDER BY valid_from ASC
	`

	addedRows, err := d.db.QueryContext(ctx, addedQuery, sourceID, targetID, t1Unix, t2Unix)
	if err != nil {
		return nil, fmt.Errorf("query added edges for %s->%s: %w", sourceID, targetID, err)
	}
	defer addedRows.Close()

	diff.Added, err = scanTemporalEdges(addedRows)
	if err != nil {
		return nil, fmt.Errorf("scan added edges: %w", err)
	}

	// Query for removed edges between these nodes
	removedQuery := baseTemporalEdgeSelect + `
		WHERE source_id = ?
		AND target_id = ?
		AND valid_to IS NOT NULL
		AND valid_to >= ?
		AND valid_to < ?
		ORDER BY valid_to ASC
	`

	removedRows, err := d.db.QueryContext(ctx, removedQuery, sourceID, targetID, t1Unix, t2Unix)
	if err != nil {
		return nil, fmt.Errorf("query removed edges for %s->%s: %w", sourceID, targetID, err)
	}
	defer removedRows.Close()

	diff.Removed, err = scanTemporalEdges(removedRows)
	if err != nil {
		return nil, fmt.Errorf("scan removed edges: %w", err)
	}

	// Query for modified edges between these nodes
	modifiedQuery := baseTemporalEdgeSelect + `
		WHERE source_id = ?
		AND target_id = ?
		AND tx_start IS NOT NULL
		AND tx_start >= ?
		AND tx_start < ?
		AND tx_end IS NOT NULL
		ORDER BY tx_start ASC
	`

	modifiedRows, err := d.db.QueryContext(ctx, modifiedQuery, sourceID, targetID, t1Unix, t2Unix)
	if err != nil {
		return nil, fmt.Errorf("query modified edges for %s->%s: %w", sourceID, targetID, err)
	}
	defer modifiedRows.Close()

	modifiedEdges, err := scanTemporalEdges(modifiedRows)
	if err != nil {
		return nil, fmt.Errorf("scan modified edges: %w", err)
	}

	// For each modified edge, try to find its successor
	for _, before := range modifiedEdges {
		mod := EdgeModification{
			Before:  copyTemporalEdge(&before),
			Changes: []string{},
		}

		after, err := d.findSuccessorEdge(ctx, &before)
		if err == nil && after != nil {
			mod.After = after
			mod.Changes = computeEdgeChanges(&before, after)
		} else {
			mod.Changes = []string{"edge superseded"}
		}

		diff.Modified = append(diff.Modified, mod)
	}

	return diff, nil
}

// =============================================================================
// Helper Functions
// =============================================================================

// findSuccessorEdge finds the edge that replaced a given edge.
// It looks for an edge with the same source and target that was created
// around when the given edge was superseded.
func (d *TemporalDiffer) findSuccessorEdge(ctx context.Context, edge *vectorgraphdb.TemporalEdge) (*vectorgraphdb.TemporalEdge, error) {
	if edge.TxEnd == nil {
		return nil, nil // No successor if edge wasn't superseded
	}

	txEndUnix := edge.TxEnd.Unix()

	// Look for an edge with the same source/target that was created around when this one ended
	query := baseTemporalEdgeSelect + `
		WHERE source_id = ?
		AND target_id = ?
		AND id != ?
		AND tx_start IS NOT NULL
		AND tx_start >= ?
		AND tx_start <= ?
		ORDER BY tx_start ASC
		LIMIT 1
	`

	// Allow a 1-second tolerance for finding the successor
	rows, err := d.db.QueryContext(ctx, query, edge.SourceID, edge.TargetID, edge.ID, txEndUnix-1, txEndUnix+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	edges, err := scanTemporalEdges(rows)
	if err != nil {
		return nil, err
	}

	if len(edges) == 0 {
		return nil, nil
	}

	return &edges[0], nil
}

// copyTemporalEdge creates a copy of a TemporalEdge.
func copyTemporalEdge(edge *vectorgraphdb.TemporalEdge) *vectorgraphdb.TemporalEdge {
	if edge == nil {
		return nil
	}

	copy := *edge

	// Deep copy pointers
	if edge.ValidFrom != nil {
		vf := *edge.ValidFrom
		copy.ValidFrom = &vf
	}
	if edge.ValidTo != nil {
		vt := *edge.ValidTo
		copy.ValidTo = &vt
	}
	if edge.TxStart != nil {
		ts := *edge.TxStart
		copy.TxStart = &ts
	}
	if edge.TxEnd != nil {
		te := *edge.TxEnd
		copy.TxEnd = &te
	}

	// Deep copy metadata
	if edge.Metadata != nil {
		copy.Metadata = make(map[string]any, len(edge.Metadata))
		for k, v := range edge.Metadata {
			copy.Metadata[k] = v
		}
	}

	return &copy
}

// computeEdgeChanges computes the list of changes between two edge versions.
func computeEdgeChanges(before, after *vectorgraphdb.TemporalEdge) []string {
	if before == nil || after == nil {
		return []string{"edge replaced"}
	}

	var changes []string

	// Check edge type change
	if before.EdgeType != after.EdgeType {
		changes = append(changes, fmt.Sprintf("edge type changed from %s to %s", before.EdgeType.String(), after.EdgeType.String()))
	}

	// Check weight change
	if before.Weight != after.Weight {
		changes = append(changes, fmt.Sprintf("weight changed from %.2f to %.2f", before.Weight, after.Weight))
	}

	// Check valid_from change
	if !timeEqual(before.ValidFrom, after.ValidFrom) {
		changes = append(changes, "valid_from changed")
	}

	// Check valid_to change
	if !timeEqual(before.ValidTo, after.ValidTo) {
		changes = append(changes, "valid_to changed")
	}

	// Check metadata changes (simple comparison)
	if len(before.Metadata) != len(after.Metadata) {
		changes = append(changes, "metadata changed")
	} else if len(before.Metadata) > 0 {
		for k, v := range before.Metadata {
			if afterV, ok := after.Metadata[k]; !ok || v != afterV {
				changes = append(changes, "metadata changed")
				break
			}
		}
	}

	if len(changes) == 0 {
		changes = append(changes, "edge updated")
	}

	return changes
}

// timeEqual compares two time pointers for equality.
func timeEqual(t1, t2 *time.Time) bool {
	if t1 == nil && t2 == nil {
		return true
	}
	if t1 == nil || t2 == nil {
		return false
	}
	return t1.Equal(*t2)
}
