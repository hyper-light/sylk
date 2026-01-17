package hnsw

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"unsafe"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

func (h *Index) Save(db *sql.DB) error {
	h.mu.RLock()
	defer h.mu.RUnlock()

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	if err := h.saveMetadata(tx); err != nil {
		return err
	}

	if err := h.saveGraph(tx); err != nil {
		return err
	}

	return tx.Commit()
}

func (h *Index) saveMetadata(tx *sql.Tx) error {
	_, err := tx.Exec(`
		UPDATE hnsw_metadata SET
			entry_point = ?,
			max_level = ?,
			m = ?,
			ef_construct = ?,
			ef_search = ?,
			level_mult = ?,
			total_nodes = ?,
			updated_at = datetime('now')
		WHERE id = 1
	`, h.entryPoint, h.maxLevel, h.M, h.efConstruct, h.efSearch, h.levelMult, len(h.vectors))
	if err != nil {
		return fmt.Errorf("update metadata: %w", err)
	}
	return nil
}

func (h *Index) saveGraph(tx *sql.Tx) error {
	if _, err := tx.Exec("DELETE FROM hnsw_graph"); err != nil {
		return fmt.Errorf("clear graph: %w", err)
	}

	stmt, err := tx.Prepare(`INSERT INTO hnsw_graph (id, node_id, layer, neighbors) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	for layerIdx, layer := range h.layers {
		if err := h.saveLayer(stmt, layer, layerIdx); err != nil {
			return err
		}
	}
	return nil
}

func (h *Index) saveLayer(stmt *sql.Stmt, layer *layer, layerIdx int) error {
	layer.mu.RLock()
	defer layer.mu.RUnlock()

	for nodeID, node := range layer.nodes {
		neighborsJSON, err := json.Marshal(node.neighbors)
		if err != nil {
			return fmt.Errorf("marshal neighbors: %w", err)
		}
		id := fmt.Sprintf("%s:%d", nodeID, layerIdx)
		if _, err := stmt.Exec(id, nodeID, layerIdx, string(neighborsJSON)); err != nil {
			return fmt.Errorf("insert graph node: %w", err)
		}
	}
	return nil
}

func (h *Index) Load(db *sql.DB) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if err := h.loadMetadata(db); err != nil {
		return err
	}

	return h.loadGraph(db)
}

func (h *Index) loadMetadata(db *sql.DB) error {
	row := db.QueryRow(`
		SELECT entry_point, max_level, m, ef_construct, ef_search, level_mult, total_nodes
		FROM hnsw_metadata WHERE id = 1
	`)

	var entryPoint sql.NullString
	var totalNodes int

	err := row.Scan(&entryPoint, &h.maxLevel, &h.M, &h.efConstruct, &h.efSearch, &h.levelMult, &totalNodes)
	if err != nil {
		return fmt.Errorf("scan metadata: %w", err)
	}

	if entryPoint.Valid {
		h.entryPoint = entryPoint.String
	}
	return nil
}

func (h *Index) loadGraph(db *sql.DB) error {
	rows, err := db.Query(`SELECT node_id, layer, neighbors FROM hnsw_graph ORDER BY layer`)
	if err != nil {
		return fmt.Errorf("query graph: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		if err := h.loadGraphRow(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (h *Index) loadGraphRow(rows *sql.Rows) error {
	var nodeID string
	var layerIdx int
	var neighborsJSON string

	if err := rows.Scan(&nodeID, &layerIdx, &neighborsJSON); err != nil {
		return fmt.Errorf("scan graph row: %w", err)
	}

	h.ensureLayers(layerIdx)

	var neighbors []string
	if err := json.Unmarshal([]byte(neighborsJSON), &neighbors); err != nil {
		return fmt.Errorf("unmarshal neighbors: %w", err)
	}

	h.layers[layerIdx].addNode(nodeID)
	h.layers[layerIdx].setNeighbors(nodeID, neighbors)
	return nil
}

func (h *Index) LoadVectors(db *sql.DB) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	rows, err := db.Query(`
		SELECT v.node_id, v.embedding, v.magnitude, n.domain, n.node_type
		FROM vectors v
		JOIN nodes n ON v.node_id = n.id
	`)
	if err != nil {
		return fmt.Errorf("query vectors: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		if err := h.loadVectorRow(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (h *Index) loadVectorRow(rows *sql.Rows) error {
	var nodeID string
	var embeddingBlob []byte
	var magnitude float64
	var domain string
	var nodeType string

	if err := rows.Scan(&nodeID, &embeddingBlob, &magnitude, &domain, &nodeType); err != nil {
		return fmt.Errorf("scan vector row: %w", err)
	}

	embedding := bytesToFloat32s(embeddingBlob)
	h.vectors[nodeID] = embedding
	h.magnitudes[nodeID] = magnitude
	h.domains[nodeID] = vectorgraphdb.Domain(domain)
	h.nodeTypes[nodeID] = vectorgraphdb.NodeType(nodeType)
	return nil
}

func bytesToFloat32s(b []byte) []float32 {
	if len(b)%4 != 0 {
		return nil
	}
	result := make([]float32, len(b)/4)
	for i := range result {
		bits := uint32(b[i*4]) | uint32(b[i*4+1])<<8 | uint32(b[i*4+2])<<16 | uint32(b[i*4+3])<<24
		result[i] = float32FromBits(bits)
	}
	return result
}

func float32FromBits(b uint32) float32 {
	return *(*float32)(unsafe.Pointer(&b))
}

func float32ToBytes(f []float32) []byte {
	result := make([]byte, len(f)*4)
	for i, v := range f {
		bits := float32ToBits(v)
		result[i*4] = byte(bits)
		result[i*4+1] = byte(bits >> 8)
		result[i*4+2] = byte(bits >> 16)
		result[i*4+3] = byte(bits >> 24)
	}
	return result
}

func float32ToBits(f float32) uint32 {
	return *(*uint32)(unsafe.Pointer(&f))
}
