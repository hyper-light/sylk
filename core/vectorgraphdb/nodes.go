package vectorgraphdb

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
	"unsafe"
)

var (
	ErrNodeNotFound     = errors.New("node not found")
	ErrInvalidDomain    = errors.New("invalid domain")
	ErrInvalidNodeType  = errors.New("invalid node type for domain")
	ErrDuplicateNode    = errors.New("node already exists")
	ErrEmbeddingMissing = errors.New("embedding required for node")
)

// HNSWInserter defines the interface for HNSW index operations.
// This abstraction allows the NodeStore to insert/delete vectors
// without direct dependency on the HNSW implementation.
type HNSWInserter interface {
	Insert(id string, vector []float32, domain Domain, nodeType NodeType) error
	Delete(id string) error
	DeleteBatch(ids []string) error
}

// NodeStore provides CRUD operations for graph nodes in VectorGraphDB.
// It manages both the SQLite persistence and HNSW vector index synchronization.
//
// Thread Safety: NodeStore operations are thread-safe through the underlying
// VectorGraphDB and HNSW index implementations.
//
// Consistency: Node insertions are performed in a transaction with the embedding,
// followed by HNSW index insertion. HNSW failures do not roll back the database.
type NodeStore struct {
	db   *VectorGraphDB
	hnsw HNSWInserter
}

// NewNodeStore creates a new NodeStore with the given database and optional HNSW index.
// If hnswIndex is nil, vector operations will be skipped.
func NewNodeStore(db *VectorGraphDB, hnswIndex HNSWInserter) *NodeStore {
	return &NodeStore{db: db, hnsw: hnswIndex}
}

func (ns *NodeStore) InsertNode(node *GraphNode, embedding []float32) error {
	if err := ns.validateNode(node); err != nil {
		return err
	}

	node.ContentHash = ns.computeContentHash(node)
	now := time.Now()
	node.CreatedAt = now
	node.UpdatedAt = now
	if node.Name == "" {
		node.Name = node.ID
	}

	return ns.insertNodeTx(node, embedding)
}

func (ns *NodeStore) validateNode(node *GraphNode) error {
	if !node.Domain.IsValid() {
		return ErrInvalidDomain
	}
	if !node.NodeType.IsValidForDomain(node.Domain) {
		return ErrInvalidNodeType
	}
	return nil
}

func (ns *NodeStore) computeContentHash(node *GraphNode) string {
	payload := map[string]any{
		"content":  node.Content,
		"metadata": node.Metadata,
	}
	data, _ := json.Marshal(payload)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (ns *NodeStore) insertNodeTx(node *GraphNode, embedding []float32) error {
	if err := ns.insertNodeToDB(node, embedding); err != nil {
		return err
	}
	return ns.insertNodeToHNSW(node, embedding)
}

func (ns *NodeStore) insertNodeToDB(node *GraphNode, embedding []float32) error {
	tx, err := ns.db.BeginTx()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if err := ns.insertNodeRow(tx, node); err != nil {
		return err
	}
	if err := ns.insertEmbedding(tx, node.ID, embedding, node.Domain, node.NodeType); err != nil {
		return err
	}
	return tx.Commit()
}

func (ns *NodeStore) insertNodeToHNSW(node *GraphNode, embedding []float32) error {
	if ns.hnsw == nil {
		return nil
	}
	return ns.hnsw.Insert(node.ID, embedding, node.Domain, node.NodeType)
}

func (ns *NodeStore) insertNodeRow(tx *sql.Tx, node *GraphNode) error {
	metadata, _ := json.Marshal(node.Metadata)
	_, err := tx.Exec(`
		INSERT INTO nodes (id, domain, node_type, name, path, package, line_start, line_end, signature,
			session_id, timestamp, category, url, source, authors, published_at,
			content, content_hash, metadata, verified, verification_type, confidence, trust_level,
			created_at, updated_at, expires_at, superseded_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, node.ID, node.Domain, node.NodeType, node.Name, node.Path, node.Package,
		nullInt(node.LineStart), nullInt(node.LineEnd), node.Signature,
		nullString(node.SessionID), nullTime(node.Timestamp), nullString(node.Category),
		nullString(node.URL), nullString(node.Source), nullJSON(node.Authors), nullTime(node.PublishedAt),
		nullString(node.Content), node.ContentHash, string(metadata),
		node.Verified, nullInt(int(node.VerificationType)), nullFloat(node.Confidence), nullInt(int(node.TrustLevel)),
		node.CreatedAt.Format(time.RFC3339), node.UpdatedAt.Format(time.RFC3339), nullTime(node.ExpiresAt), nullString(node.SupersededBy))
	if err != nil {
		return fmt.Errorf("insert node %s (domain=%v, type=%v): %w", node.ID, node.Domain, node.NodeType, err)
	}
	return nil
}

func (ns *NodeStore) insertEmbedding(tx *sql.Tx, nodeID string, embedding []float32, domain Domain, nodeType NodeType) error {
	blob := float32sToBytes(embedding)
	mag := computeMagnitude(embedding)
	_, err := tx.Exec(`
		INSERT INTO vectors (node_id, embedding, magnitude, dimensions, domain, node_type)
		VALUES (?, ?, ?, ?, ?, ?)
	`, nodeID, blob, mag, EmbeddingDimension, domain, nodeType)
	if err != nil {
		return fmt.Errorf("insert embedding for node %s (dims=%d): %w", nodeID, len(embedding), err)
	}
	return nil
}

func computeMagnitude(v []float32) float64 {
	var sum float32
	for _, val := range v {
		sum += val * val
	}
	return math.Sqrt(float64(sum))
}

func (ns *NodeStore) GetNode(id string) (*GraphNode, error) {
	row := ns.db.db.QueryRow(`
		SELECT id, domain, node_type, name, path, package, line_start, line_end, signature,
			session_id, timestamp, category, url, source, authors, published_at,
			content, content_hash, metadata, verified, verification_type, confidence, trust_level,
			created_at, updated_at, expires_at, superseded_by
		FROM nodes WHERE id = ?
	`, id)
	return ns.scanNode(row)
}

func (ns *NodeStore) scanNode(row *sql.Row) (*GraphNode, error) {
	var node GraphNode
	var metadataJSON, createdAt, updatedAt string
	var path, pkg, signature sql.NullString
	var lineStart, lineEnd sql.NullInt64
	var sessionID, category, url, source, content, supersededBy sql.NullString
	var timestamp, publishedAt, expiresAt sql.NullString
	var authors sql.NullString
	var verificationType, trustLevel sql.NullInt64
	var confidence sql.NullFloat64

	err := row.Scan(&node.ID, &node.Domain, &node.NodeType, &node.Name,
		&path, &pkg, &lineStart, &lineEnd, &signature,
		&sessionID, &timestamp, &category,
		&url, &source, &authors, &publishedAt,
		&content, &node.ContentHash, &metadataJSON,
		&node.Verified, &verificationType, &confidence, &trustLevel,
		&createdAt, &updatedAt, &expiresAt, &supersededBy)
	if err == sql.ErrNoRows {
		return nil, ErrNodeNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan node: %w", err)
	}

	node.Path = path.String
	node.Package = pkg.String
	node.LineStart = int(lineStart.Int64)
	node.LineEnd = int(lineEnd.Int64)
	node.Signature = signature.String
	node.SessionID = sessionID.String
	node.Timestamp, _ = time.Parse(time.RFC3339, timestamp.String)
	node.Category = category.String
	node.URL = url.String
	node.Source = source.String
	if authors.Valid {
		var decoded any
		if err := json.Unmarshal([]byte(authors.String), &decoded); err == nil {
			node.Authors = decoded
		}
	}
	node.PublishedAt, _ = time.Parse(time.RFC3339, publishedAt.String)
	node.Content = content.String
	node.VerificationType = VerificationType(verificationType.Int64)
	node.Confidence = confidence.Float64
	node.TrustLevel = TrustLevel(trustLevel.Int64)
	node.ExpiresAt, _ = time.Parse(time.RFC3339, expiresAt.String)
	node.SupersededBy = supersededBy.String
	if err == sql.ErrNoRows {
		return nil, ErrNodeNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan node: %w", err)
	}

	return ns.parseNode(&node, metadataJSON, createdAt, updatedAt)
}

func (ns *NodeStore) parseNode(node *GraphNode, metadataJSON, createdAt, updatedAt string) (*GraphNode, error) {
	if err := json.Unmarshal([]byte(metadataJSON), &node.Metadata); err != nil {
		node.Metadata = make(map[string]any)
	}
	node.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	node.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return node, nil
}

func (ns *NodeStore) UpdateNode(node *GraphNode) error {
	if err := ns.validateNode(node); err != nil {
		return err
	}

	node.ContentHash = ns.computeContentHash(node)
	node.UpdatedAt = time.Now()

	return ns.updateNodeRow(node)
}

func (ns *NodeStore) updateNodeRow(node *GraphNode) error {
	metadata, _ := json.Marshal(node.Metadata)
	result, err := ns.db.db.Exec(`
		UPDATE nodes SET domain = ?, node_type = ?, name = ?, path = ?, package = ?, line_start = ?, line_end = ?, signature = ?,
			session_id = ?, timestamp = ?, category = ?, url = ?, source = ?, authors = ?, published_at = ?,
			content = ?, content_hash = ?, metadata = ?, verified = ?, verification_type = ?, confidence = ?, trust_level = ?,
			updated_at = ?, expires_at = ?, superseded_by = ?
		WHERE id = ?
	`, node.Domain, node.NodeType, node.Name, node.Path, node.Package,
		nullInt(node.LineStart), nullInt(node.LineEnd), node.Signature,
		nullString(node.SessionID), nullTime(node.Timestamp), nullString(node.Category),
		nullString(node.URL), nullString(node.Source), nullJSON(node.Authors), nullTime(node.PublishedAt),
		nullString(node.Content), node.ContentHash, string(metadata),
		node.Verified, nullInt(int(node.VerificationType)), nullFloat(node.Confidence), nullInt(int(node.TrustLevel)),
		node.UpdatedAt.Format(time.RFC3339), nullTime(node.ExpiresAt), nullString(node.SupersededBy), node.ID)
	if err != nil {
		return fmt.Errorf("update node %s: %w", node.ID, err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNodeNotFound
	}
	return nil
}

func (ns *NodeStore) DeleteNode(id string) error {
	result, err := ns.db.db.Exec("DELETE FROM nodes WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete node: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNodeNotFound
	}

	if ns.hnsw != nil {
		ns.hnsw.Delete(id)
	}
	return nil
}

func (ns *NodeStore) GetNodesByType(domain Domain, nodeType NodeType, limit int) ([]*GraphNode, error) {
	rows, err := ns.db.db.Query(`
		SELECT id, domain, node_type, name, path, package, line_start, line_end, signature,
			session_id, timestamp, category, url, source, authors, published_at,
			content, content_hash, metadata, verified, verification_type, confidence, trust_level,
			created_at, updated_at, expires_at, superseded_by
		FROM nodes WHERE domain = ? AND node_type = ? LIMIT ?
	`, domain, nodeType, limit)
	if err != nil {
		return nil, fmt.Errorf("query nodes: %w", err)
	}
	defer rows.Close()

	return ns.scanNodes(rows)
}

func (ns *NodeStore) scanNodes(rows *sql.Rows) ([]*GraphNode, error) {
	var nodes []*GraphNode
	for rows.Next() {
		node, err := ns.scanNodeRow(rows)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

func (ns *NodeStore) scanNodeRow(rows *sql.Rows) (*GraphNode, error) {
	var node GraphNode
	var metadataJSON, createdAt, updatedAt sql.NullString
	var path, pkg, signature sql.NullString
	var lineStart, lineEnd sql.NullInt64
	var sessionID, category, url, source, content, supersededBy sql.NullString
	var timestamp, publishedAt, expiresAt sql.NullString
	var authors sql.NullString
	var verificationType, trustLevel sql.NullInt64
	var confidence sql.NullFloat64

	err := rows.Scan(&node.ID, &node.Domain, &node.NodeType, &node.Name,
		&path, &pkg, &lineStart, &lineEnd, &signature,
		&sessionID, &timestamp, &category,
		&url, &source, &authors, &publishedAt,
		&content, &node.ContentHash, &metadataJSON,
		&node.Verified, &verificationType, &confidence, &trustLevel,
		&createdAt, &updatedAt, &expiresAt, &supersededBy)
	if err != nil {
		return nil, fmt.Errorf("scan node row: %w", err)
	}

	node.Path = path.String
	node.Package = pkg.String
	if lineStart.Valid {
		node.LineStart = int(lineStart.Int64)
	}
	if lineEnd.Valid {
		node.LineEnd = int(lineEnd.Int64)
	}
	node.Signature = signature.String
	node.SessionID = sessionID.String
	if timestamp.Valid {
		node.Timestamp, _ = time.Parse(time.RFC3339, timestamp.String)
	}
	node.Category = category.String
	node.URL = url.String
	node.Source = source.String
	if authors.Valid {
		var decoded any
		if err := json.Unmarshal([]byte(authors.String), &decoded); err == nil {
			node.Authors = decoded
		}
	}
	if publishedAt.Valid {
		node.PublishedAt, _ = time.Parse(time.RFC3339, publishedAt.String)
	}
	node.Content = content.String
	if verificationType.Valid {
		node.VerificationType = VerificationType(verificationType.Int64)
	}
	if confidence.Valid {
		node.Confidence = confidence.Float64
	}
	if trustLevel.Valid {
		node.TrustLevel = TrustLevel(trustLevel.Int64)
	}
	if expiresAt.Valid {
		node.ExpiresAt, _ = time.Parse(time.RFC3339, expiresAt.String)
	}
	node.SupersededBy = supersededBy.String

	return ns.parseNode(&node, metadataJSON.String, createdAt.String, updatedAt.String)
}

func (ns *NodeStore) GetNodesByContentHash(hash string) ([]*GraphNode, error) {
	rows, err := ns.db.db.Query(`
		SELECT id, domain, node_type, name, path, package, line_start, line_end, signature,
			session_id, timestamp, category, url, source, authors, published_at,
			content, content_hash, metadata, verified, verification_type, confidence, trust_level,
			created_at, updated_at, expires_at, superseded_by
		FROM nodes WHERE content_hash = ?
	`, hash)
	if err != nil {
		return nil, fmt.Errorf("query nodes: %w", err)
	}
	defer rows.Close()

	return ns.scanNodes(rows)
}

func (ns *NodeStore) TouchNode(id string) error {
	now := time.Now().Format(time.RFC3339)
	result, err := ns.db.db.Exec("UPDATE nodes SET updated_at = ? WHERE id = ?", now, id)
	if err != nil {
		return fmt.Errorf("touch node: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNodeNotFound
	}
	return nil
}

// GetNodesBatch loads multiple nodes by their IDs in a single query using WHERE id IN (...).
// This eliminates the N+1 query pattern by fetching all nodes at once.
// Returns a map of node ID to node for all found nodes. Missing nodes are silently omitted.
func (ns *NodeStore) GetNodesBatch(ids []string) (map[string]*GraphNode, error) {
	if len(ids) == 0 {
		return make(map[string]*GraphNode), nil
	}

	// Build placeholders for IN clause
	placeholders := make([]byte, 0, len(ids)*2)
	args := make([]any, len(ids))
	for i, id := range ids {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args[i] = id
	}

	query := `
		SELECT id, domain, node_type, name, path, package, line_start, line_end, signature,
			session_id, timestamp, category, url, source, authors, published_at,
			content, content_hash, metadata, verified, verification_type, confidence, trust_level,
			created_at, updated_at, expires_at, superseded_by
		FROM nodes WHERE id IN (` + string(placeholders) + `)`

	rows, err := ns.db.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("batch query nodes: %w", err)
	}
	defer rows.Close()

	nodes, err := ns.scanNodes(rows)
	if err != nil {
		return nil, err
	}

	// Convert slice to map for efficient lookup
	result := make(map[string]*GraphNode, len(nodes))
	for _, node := range nodes {
		result[node.ID] = node
	}
	return result, nil
}

// GetNodesBatchSlice loads multiple nodes by their IDs and returns them as a slice.
// This is a convenience wrapper around GetNodesBatch that preserves order when possible.
// Missing nodes are omitted from the result.
func (ns *NodeStore) GetNodesBatchSlice(ids []string) ([]*GraphNode, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	nodeMap, err := ns.GetNodesBatch(ids)
	if err != nil {
		return nil, err
	}

	// Preserve order of input IDs where nodes exist
	result := make([]*GraphNode, 0, len(nodeMap))
	for _, id := range ids {
		if node, exists := nodeMap[id]; exists {
			result = append(result, node)
		}
	}
	return result, nil
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullInt(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

func nullTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func nullFloat(value float64) any {
	if value == 0 {
		return nil
	}
	return value
}

func nullJSON(value any) any {
	if value == nil {
		return nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return string(payload)
}

func float32sToBytes(f []float32) []byte {
	result := make([]byte, len(f)*4)
	for i, v := range f {
		bits := *(*uint32)(unsafe.Pointer(&v))
		result[i*4] = byte(bits)
		result[i*4+1] = byte(bits >> 8)
		result[i*4+2] = byte(bits >> 16)
		result[i*4+3] = byte(bits >> 24)
	}
	return result
}
