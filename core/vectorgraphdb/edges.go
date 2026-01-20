package vectorgraphdb

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	ErrEdgeNotFound        = errors.New("edge not found")
	ErrInvalidEdgeType     = errors.New("invalid edge type")
	ErrInvalidCrossDomain  = errors.New("invalid cross-domain edge")
	ErrEdgeEndpointMissing = errors.New("edge endpoint node not found")
)

// EdgeStore provides CRUD operations for graph edges in VectorGraphDB.
// It manages relationships between nodes with support for edge types,
// weights, and cross-domain validation.
//
// Thread Safety: EdgeStore operations are thread-safe through the underlying
// VectorGraphDB implementation.
//
// Cross-Domain Edges: Edges between nodes in different domains are validated
// against allowed cross-domain edge type rules.
type EdgeStore struct {
	db *VectorGraphDB
}

// NewEdgeStore creates a new EdgeStore with the given database.
func NewEdgeStore(db *VectorGraphDB) *EdgeStore {
	return &EdgeStore{db: db}
}

func (es *EdgeStore) InsertEdge(edge *GraphEdge) error {
	if err := es.validateEdge(edge); err != nil {
		return err
	}

	if err := es.validateEndpoints(edge); err != nil {
		return err
	}

	edge.CreatedAt = time.Now()

	return es.insertEdgeRow(edge)
}

func (es *EdgeStore) validateEdge(edge *GraphEdge) error {
	if !edge.EdgeType.IsValid() {
		return ErrInvalidEdgeType
	}
	return nil
}

func (es *EdgeStore) validateEndpoints(edge *GraphEdge) error {
	fromDomain, err := es.getNodeDomain(edge.SourceID)
	if err != nil {
		return fmt.Errorf("from node: %w", ErrEdgeEndpointMissing)
	}

	toDomain, err := es.getNodeDomain(edge.TargetID)
	if err != nil {
		return fmt.Errorf("to node: %w", ErrEdgeEndpointMissing)
	}

	if fromDomain != toDomain {
		return es.validateCrossDomainEdge(edge.EdgeType, fromDomain, toDomain)
	}
	return nil
}

func (es *EdgeStore) getNodeDomain(nodeID string) (Domain, error) {
	var domain Domain
	err := es.db.db.QueryRow("SELECT domain FROM nodes WHERE id = ?", nodeID).Scan(&domain)
	if err == sql.ErrNoRows {
		return 0, ErrEdgeEndpointMissing
	}
	if err != nil {
		return 0, err
	}
	return domain, nil
}

func (es *EdgeStore) validateCrossDomainEdge(edgeType EdgeType, from, to Domain) error {
	if !IsValidCrossDomainEdge(edgeType, from, to) {
		return ErrInvalidCrossDomain
	}
	return nil
}

func (es *EdgeStore) insertEdgeRow(edge *GraphEdge) error {
	metadata, _ := json.Marshal(edge.Metadata)
	result, err := es.db.db.Exec(`
		INSERT INTO edges (source_id, target_id, edge_type, weight, metadata, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, edge.SourceID, edge.TargetID, edge.EdgeType, edge.Weight,
		string(metadata), edge.CreatedAt.Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("insert edge from %s to %s (type=%v): %w", edge.SourceID, edge.TargetID, edge.EdgeType, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("fetch edge id for %s->%s: %w", edge.SourceID, edge.TargetID, err)
	}
	edge.ID = id
	return nil
}

func (es *EdgeStore) GetEdge(id int64) (*GraphEdge, error) {
	row := es.db.db.QueryRow(`
		SELECT id, source_id, target_id, edge_type, weight, metadata, created_at
		FROM edges WHERE id = ?
	`, id)
	return es.scanEdge(row)
}

func (es *EdgeStore) scanEdge(row *sql.Row) (*GraphEdge, error) {
	var edge GraphEdge
	var metadataJSON, createdAt string

	err := row.Scan(&edge.ID, &edge.SourceID, &edge.TargetID, &edge.EdgeType,
		&edge.Weight, &metadataJSON, &createdAt)
	if err == sql.ErrNoRows {
		return nil, ErrEdgeNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan edge: %w", err)
	}

	return es.parseEdge(&edge, metadataJSON, createdAt)
}

func (es *EdgeStore) parseEdge(edge *GraphEdge, metadataJSON, createdAt string) (*GraphEdge, error) {
	if err := json.Unmarshal([]byte(metadataJSON), &edge.Metadata); err != nil {
		edge.Metadata = make(map[string]any)
	}
	edge.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return edge, nil
}

func (es *EdgeStore) GetEdgesBetween(fromID, toID string) ([]*GraphEdge, error) {
	rows, err := es.db.db.Query(`
		SELECT id, source_id, target_id, edge_type, weight, metadata, created_at
		FROM edges WHERE source_id = ? AND target_id = ?
	`, fromID, toID)
	if err != nil {
		return nil, fmt.Errorf("query edges: %w", err)
	}
	defer rows.Close()

	return es.scanEdges(rows)
}

func (es *EdgeStore) scanEdges(rows *sql.Rows) ([]*GraphEdge, error) {
	var edges []*GraphEdge
	for rows.Next() {
		edge, err := es.scanEdgeRow(rows)
		if err != nil {
			return nil, err
		}
		edges = append(edges, edge)
	}
	return edges, rows.Err()
}

func (es *EdgeStore) scanEdgeRow(rows *sql.Rows) (*GraphEdge, error) {
	var edge GraphEdge
	var metadataJSON, createdAt string

	err := rows.Scan(&edge.ID, &edge.SourceID, &edge.TargetID, &edge.EdgeType,
		&edge.Weight, &metadataJSON, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("scan edge row: %w", err)
	}

	return es.parseEdge(&edge, metadataJSON, createdAt)
}

func (es *EdgeStore) GetOutgoingEdges(nodeID string, edgeTypes ...EdgeType) ([]*GraphEdge, error) {
	if len(edgeTypes) == 0 {
		return es.getOutgoingEdgesAll(nodeID)
	}
	return es.getOutgoingEdgesFiltered(nodeID, edgeTypes)
}

func (es *EdgeStore) getOutgoingEdgesAll(nodeID string) ([]*GraphEdge, error) {
	rows, err := es.db.db.Query(`
		SELECT id, source_id, target_id, edge_type, weight, metadata, created_at
		FROM edges WHERE source_id = ?
	`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("query edges: %w", err)
	}
	defer rows.Close()

	return es.scanEdges(rows)
}

func (es *EdgeStore) getOutgoingEdgesFiltered(nodeID string, edgeTypes []EdgeType) ([]*GraphEdge, error) {
	query := `
		SELECT id, source_id, target_id, edge_type, weight, metadata, created_at
		FROM edges WHERE source_id = ? AND edge_type IN (`
	args := make([]any, 0, len(edgeTypes)+1)
	args = append(args, nodeID)

	for i, et := range edgeTypes {
		if i > 0 {
			query += ","
		}
		query += "?"
		args = append(args, et)
	}
	query += ")"

	rows, err := es.db.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query edges: %w", err)
	}
	defer rows.Close()

	return es.scanEdges(rows)
}

func (es *EdgeStore) GetIncomingEdges(nodeID string, edgeTypes ...EdgeType) ([]*GraphEdge, error) {
	if len(edgeTypes) == 0 {
		return es.getIncomingEdgesAll(nodeID)
	}
	return es.getIncomingEdgesFiltered(nodeID, edgeTypes)
}

func (es *EdgeStore) getIncomingEdgesAll(nodeID string) ([]*GraphEdge, error) {
	rows, err := es.db.db.Query(`
		SELECT id, source_id, target_id, edge_type, weight, metadata, created_at
		FROM edges WHERE target_id = ?
	`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("query edges: %w", err)
	}
	defer rows.Close()

	return es.scanEdges(rows)
}

func (es *EdgeStore) getIncomingEdgesFiltered(nodeID string, edgeTypes []EdgeType) ([]*GraphEdge, error) {
	query := `
		SELECT id, source_id, target_id, edge_type, weight, metadata, created_at
		FROM edges WHERE target_id = ? AND edge_type IN (`
	args := make([]any, 0, len(edgeTypes)+1)
	args = append(args, nodeID)

	for i, et := range edgeTypes {
		if i > 0 {
			query += ","
		}
		query += "?"
		args = append(args, et)
	}
	query += ")"

	rows, err := es.db.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query edges: %w", err)
	}
	defer rows.Close()

	return es.scanEdges(rows)
}

func (es *EdgeStore) DeleteEdge(id int64) error {
	result, err := es.db.db.Exec("DELETE FROM edges WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete edge id=%d: %w", id, err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrEdgeNotFound
	}
	return nil
}

func (es *EdgeStore) DeleteEdgesBetween(fromID, toID string) error {
	_, err := es.db.db.Exec("DELETE FROM edges WHERE source_id = ? AND target_id = ?", fromID, toID)
	if err != nil {
		return fmt.Errorf("delete edges from %s to %s: %w", fromID, toID, err)
	}
	return nil
}
