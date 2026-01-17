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

type HNSWInserter interface {
	Insert(id string, vector []float32, domain Domain, nodeType NodeType) error
	Delete(id string) error
}

type NodeStore struct {
	db   *VectorGraphDB
	hnsw HNSWInserter
}

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
	node.AccessedAt = now

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
	data, _ := json.Marshal(node.Metadata)
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
	if err := ns.insertEmbedding(tx, node.ID, embedding); err != nil {
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
		INSERT INTO nodes (id, domain, node_type, content_hash, metadata, created_at, updated_at, accessed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, node.ID, node.Domain, node.NodeType, node.ContentHash, string(metadata),
		node.CreatedAt.Format(time.RFC3339), node.UpdatedAt.Format(time.RFC3339), node.AccessedAt.Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("insert node: %w", err)
	}
	return nil
}

func (ns *NodeStore) insertEmbedding(tx *sql.Tx, nodeID string, embedding []float32) error {
	blob := float32sToBytes(embedding)
	mag := computeMagnitude(embedding)
	_, err := tx.Exec(`
		INSERT INTO vectors (id, node_id, embedding, magnitude, model_version)
		VALUES (?, ?, ?, ?, ?)
	`, "vec-"+nodeID, nodeID, blob, mag, "v1")
	if err != nil {
		return fmt.Errorf("insert embedding: %w", err)
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
		SELECT id, domain, node_type, content_hash, metadata, created_at, updated_at, accessed_at
		FROM nodes WHERE id = ?
	`, id)
	return ns.scanNode(row)
}

func (ns *NodeStore) scanNode(row *sql.Row) (*GraphNode, error) {
	var node GraphNode
	var metadataJSON string
	var createdAt, updatedAt, accessedAt string

	err := row.Scan(&node.ID, &node.Domain, &node.NodeType, &node.ContentHash,
		&metadataJSON, &createdAt, &updatedAt, &accessedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNodeNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan node: %w", err)
	}

	return ns.parseNode(&node, metadataJSON, createdAt, updatedAt, accessedAt)
}

func (ns *NodeStore) parseNode(node *GraphNode, metadataJSON, createdAt, updatedAt, accessedAt string) (*GraphNode, error) {
	if err := json.Unmarshal([]byte(metadataJSON), &node.Metadata); err != nil {
		node.Metadata = make(map[string]any)
	}
	node.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	node.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	node.AccessedAt, _ = time.Parse(time.RFC3339, accessedAt)
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
		UPDATE nodes SET domain = ?, node_type = ?, content_hash = ?, metadata = ?, updated_at = ?
		WHERE id = ?
	`, node.Domain, node.NodeType, node.ContentHash, string(metadata),
		node.UpdatedAt.Format(time.RFC3339), node.ID)
	if err != nil {
		return fmt.Errorf("update node: %w", err)
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
		SELECT id, domain, node_type, content_hash, metadata, created_at, updated_at, accessed_at
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
	var metadataJSON, createdAt, updatedAt, accessedAt string

	err := rows.Scan(&node.ID, &node.Domain, &node.NodeType, &node.ContentHash,
		&metadataJSON, &createdAt, &updatedAt, &accessedAt)
	if err != nil {
		return nil, fmt.Errorf("scan node row: %w", err)
	}

	return ns.parseNode(&node, metadataJSON, createdAt, updatedAt, accessedAt)
}

func (ns *NodeStore) GetNodesByContentHash(hash string) ([]*GraphNode, error) {
	rows, err := ns.db.db.Query(`
		SELECT id, domain, node_type, content_hash, metadata, created_at, updated_at, accessed_at
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
	result, err := ns.db.db.Exec("UPDATE nodes SET accessed_at = ? WHERE id = ?", now, id)
	if err != nil {
		return fmt.Errorf("touch node: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNodeNotFound
	}
	return nil
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
