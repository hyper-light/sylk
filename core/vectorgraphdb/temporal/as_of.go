package temporal

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// =============================================================================
// AsOfQuerier (TG.1.1)
// =============================================================================

// AsOfQuerier provides point-in-time queries for temporal edges.
// It retrieves edges that were valid at a specific moment in time.
type AsOfQuerier struct {
	db *sql.DB
}

// NewAsOfQuerier creates a new AsOfQuerier with the given database connection.
func NewAsOfQuerier(db *sql.DB) *AsOfQuerier {
	return &AsOfQuerier{db: db}
}

// GetEdgesAsOf returns all edges for a node that were valid at the specified point in time.
// An edge is considered valid at time t if:
//   - valid_from <= t AND (valid_to IS NULL OR valid_to > t)
//
// This implements the standard temporal "as of" semantics where we want to see
// the state of the graph at a specific historical moment.
func (q *AsOfQuerier) GetEdgesAsOf(ctx context.Context, nodeID string, asOf time.Time) ([]vectorgraphdb.TemporalEdge, error) {
	if q.db == nil {
		return nil, ErrNilDB
	}

	asOfUnix := asOf.Unix()

	// Query for edges where:
	// 1. The source is the specified node
	// 2. valid_from <= asOf (edge had started being valid)
	// 3. valid_to IS NULL OR valid_to > asOf (edge hadn't expired yet)
	query := baseTemporalEdgeSelect + `
		WHERE source_id = ?
		AND (valid_from IS NULL OR valid_from <= ?)
		AND (valid_to IS NULL OR valid_to > ?)
		ORDER BY valid_from ASC
	`

	rows, err := q.db.QueryContext(ctx, query, nodeID, asOfUnix, asOfUnix)
	if err != nil {
		return nil, fmt.Errorf("query edges as of %v: %w", asOf, err)
	}
	defer rows.Close()

	return scanTemporalEdges(rows)
}

// GetEdgesAsOfWithTarget returns edges between two specific nodes that were valid
// at the specified point in time.
func (q *AsOfQuerier) GetEdgesAsOfWithTarget(ctx context.Context, sourceID, targetID string, asOf time.Time) ([]vectorgraphdb.TemporalEdge, error) {
	if q.db == nil {
		return nil, ErrNilDB
	}

	asOfUnix := asOf.Unix()

	query := baseTemporalEdgeSelect + `
		WHERE source_id = ?
		AND target_id = ?
		AND (valid_from IS NULL OR valid_from <= ?)
		AND (valid_to IS NULL OR valid_to > ?)
		ORDER BY valid_from ASC
	`

	rows, err := q.db.QueryContext(ctx, query, sourceID, targetID, asOfUnix, asOfUnix)
	if err != nil {
		return nil, fmt.Errorf("query edges as of %v between %s and %s: %w", asOf, sourceID, targetID, err)
	}
	defer rows.Close()

	return scanTemporalEdges(rows)
}

// GetNodesAsOf returns all nodes that existed at the specified point in time.
// A node is considered to exist at time t if it was created before or at t
// and has not been superseded by that time.
// Note: This is an optional method that queries the nodes table directly.
func (q *AsOfQuerier) GetNodesAsOf(ctx context.Context, asOf time.Time) ([]vectorgraphdb.GraphNode, error) {
	if q.db == nil {
		return nil, ErrNilDB
	}

	asOfStr := asOf.Format(time.RFC3339)

	// Query for nodes where:
	// 1. created_at <= asOf (node existed at that time)
	// 2. superseded_by IS NULL OR the superseding happened after asOf
	query := `
		SELECT
			id, domain, node_type, name, path, package, line_start, line_end, signature,
			session_id, timestamp, category, url, source, authors, published_at,
			content, content_hash, metadata, verified, verification_type, confidence, trust_level,
			created_at, updated_at, expires_at, superseded_by
		FROM nodes
		WHERE created_at <= ?
		AND (superseded_by IS NULL OR updated_at > ?)
		ORDER BY created_at ASC
	`

	rows, err := q.db.QueryContext(ctx, query, asOfStr, asOfStr)
	if err != nil {
		return nil, fmt.Errorf("query nodes as of %v: %w", asOf, err)
	}
	defer rows.Close()

	return scanNodes(rows)
}

// GetIncomingEdgesAsOf returns edges pointing to the specified node that were valid
// at the specified point in time.
func (q *AsOfQuerier) GetIncomingEdgesAsOf(ctx context.Context, nodeID string, asOf time.Time) ([]vectorgraphdb.TemporalEdge, error) {
	if q.db == nil {
		return nil, ErrNilDB
	}

	asOfUnix := asOf.Unix()

	query := baseTemporalEdgeSelect + `
		WHERE target_id = ?
		AND (valid_from IS NULL OR valid_from <= ?)
		AND (valid_to IS NULL OR valid_to > ?)
		ORDER BY valid_from ASC
	`

	rows, err := q.db.QueryContext(ctx, query, nodeID, asOfUnix, asOfUnix)
	if err != nil {
		return nil, fmt.Errorf("query incoming edges as of %v: %w", asOf, err)
	}
	defer rows.Close()

	return scanTemporalEdges(rows)
}

// GetEdgesAsOfByType returns edges of a specific type for a node that were valid
// at the specified point in time.
func (q *AsOfQuerier) GetEdgesAsOfByType(ctx context.Context, nodeID string, edgeType vectorgraphdb.EdgeType, asOf time.Time) ([]vectorgraphdb.TemporalEdge, error) {
	if q.db == nil {
		return nil, ErrNilDB
	}

	asOfUnix := asOf.Unix()

	query := baseTemporalEdgeSelect + `
		WHERE source_id = ?
		AND edge_type = ?
		AND (valid_from IS NULL OR valid_from <= ?)
		AND (valid_to IS NULL OR valid_to > ?)
		ORDER BY valid_from ASC
	`

	rows, err := q.db.QueryContext(ctx, query, nodeID, edgeType, asOfUnix, asOfUnix)
	if err != nil {
		return nil, fmt.Errorf("query edges as of %v by type %v: %w", asOf, edgeType, err)
	}
	defer rows.Close()

	return scanTemporalEdges(rows)
}

// =============================================================================
// Node Scanning Helper
// =============================================================================

// scanNodes scans node rows into a slice of GraphNode.
func scanNodes(rows *sql.Rows) ([]vectorgraphdb.GraphNode, error) {
	var nodes []vectorgraphdb.GraphNode

	for rows.Next() {
		node, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, *node)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate node rows: %w", err)
	}

	return nodes, nil
}

// scanNode scans a single row into a GraphNode.
func scanNode(rows *sql.Rows) (*vectorgraphdb.GraphNode, error) {
	var (
		node          vectorgraphdb.GraphNode
		path          sql.NullString
		pkg           sql.NullString
		lineStart     sql.NullInt64
		lineEnd       sql.NullInt64
		signature     sql.NullString
		sessionID     sql.NullString
		timestamp     sql.NullString
		category      sql.NullString
		url           sql.NullString
		source        sql.NullString
		authors       sql.NullString
		publishedAt   sql.NullString
		content       sql.NullString
		contentHash   sql.NullString
		metadata      sql.NullString
		verified      bool
		verifyType    sql.NullInt64
		confidence    sql.NullFloat64
		trustLevel    sql.NullInt64
		createdAt     string
		updatedAt     string
		expiresAt     sql.NullString
		supersededBy  sql.NullString
	)

	err := rows.Scan(
		&node.ID,
		&node.Domain,
		&node.NodeType,
		&node.Name,
		&path,
		&pkg,
		&lineStart,
		&lineEnd,
		&signature,
		&sessionID,
		&timestamp,
		&category,
		&url,
		&source,
		&authors,
		&publishedAt,
		&content,
		&contentHash,
		&metadata,
		&verified,
		&verifyType,
		&confidence,
		&trustLevel,
		&createdAt,
		&updatedAt,
		&expiresAt,
		&supersededBy,
	)
	if err != nil {
		return nil, fmt.Errorf("scan node: %w", err)
	}

	// Apply nullable fields
	if path.Valid {
		node.Path = path.String
	}
	if pkg.Valid {
		node.Package = pkg.String
	}
	if lineStart.Valid {
		node.LineStart = int(lineStart.Int64)
	}
	if lineEnd.Valid {
		node.LineEnd = int(lineEnd.Int64)
	}
	if signature.Valid {
		node.Signature = signature.String
	}
	if sessionID.Valid {
		node.SessionID = sessionID.String
	}
	if timestamp.Valid {
		if t, err := time.Parse(time.RFC3339, timestamp.String); err == nil {
			node.Timestamp = t
		}
	}
	if category.Valid {
		node.Category = category.String
	}
	if url.Valid {
		node.URL = url.String
	}
	if source.Valid {
		node.Source = source.String
	}
	if authors.Valid {
		node.Authors = authors.String
	}
	if publishedAt.Valid {
		if t, err := time.Parse(time.RFC3339, publishedAt.String); err == nil {
			node.PublishedAt = t
		}
	}
	if content.Valid {
		node.Content = content.String
	}
	if contentHash.Valid {
		node.ContentHash = contentHash.String
	}
	if metadata.Valid && metadata.String != "" {
		var m map[string]any
		if err := json.Unmarshal([]byte(metadata.String), &m); err == nil {
			node.Metadata = m
		}
	}
	node.Verified = verified
	if verifyType.Valid {
		node.VerificationType = vectorgraphdb.VerificationType(verifyType.Int64)
	}
	if confidence.Valid {
		node.Confidence = confidence.Float64
	}
	if trustLevel.Valid {
		node.TrustLevel = vectorgraphdb.TrustLevel(trustLevel.Int64)
	}
	if createdAt != "" {
		if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
			node.CreatedAt = t
		}
	}
	if updatedAt != "" {
		if t, err := time.Parse(time.RFC3339, updatedAt); err == nil {
			node.UpdatedAt = t
		}
	}
	if expiresAt.Valid {
		if t, err := time.Parse(time.RFC3339, expiresAt.String); err == nil {
			node.ExpiresAt = t
		}
	}
	if supersededBy.Valid {
		node.SupersededBy = supersededBy.String
	}

	return &node, nil
}
