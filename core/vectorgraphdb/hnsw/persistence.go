package hnsw

import (
	"database/sql"
	"fmt"
	"strconv"
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

	if err := h.ensureMetaKeys(tx); err != nil {
		return err
	}

	if err := h.saveMetadata(tx); err != nil {
		return err
	}

	if err := h.saveGraph(tx); err != nil {
		return err
	}

	return tx.Commit()
}

func (h *Index) saveMetadata(tx *sql.Tx) error {
	if err := h.ensureMetaKeys(tx); err != nil {
		return err
	}

	_, err := tx.Exec(`
		UPDATE hnsw_meta SET
			value = CASE key
				WHEN 'entry_point' THEN ?
				WHEN 'max_level' THEN ?
				WHEN 'm' THEN ?
				WHEN 'ef_construct' THEN ?
				WHEN 'ef_search' THEN ?
				WHEN 'level_mult' THEN ?
				WHEN 'total_nodes' THEN ?
				ELSE value
			END
		WHERE key IN ('entry_point', 'max_level', 'm', 'ef_construct', 'ef_search', 'level_mult', 'total_nodes')
	`, h.entryPoint, h.maxLevel, h.M, h.efConstruct, h.efSearch, h.levelMult, len(h.vectors))
	if err != nil {
		return fmt.Errorf("update metadata: %w", err)
	}
	return nil
}

func (h *Index) saveGraph(tx *sql.Tx) error {
	if _, err := tx.Exec("DELETE FROM hnsw_edges"); err != nil {
		return fmt.Errorf("clear graph: %w", err)
	}

	stmt, err := tx.Prepare(`INSERT INTO hnsw_edges (source_id, target_id, level) VALUES (?, ?, ?)`)
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
		neighborIDs := node.neighbors.GetIDs()
		for _, neighborID := range neighborIDs {
			if _, err := stmt.Exec(nodeID, neighborID, layerIdx); err != nil {
				return fmt.Errorf("insert graph edge: %w", err)
			}
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

	// W4P.15: Ensure layers exist based on maxLevel from metadata.
	// This handles the case where there are no edges (e.g., single node).
	if h.maxLevel >= 0 {
		h.ensureLayers(h.maxLevel)
	}

	if err := h.loadGraph(db); err != nil {
		return err
	}

	h.refreshDerivedFields()
	return nil
}

func (h *Index) loadMetadata(db *sql.DB) error {
	rows, err := db.Query("SELECT key, value FROM hnsw_meta")
	if err != nil {
		return fmt.Errorf("query metadata: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var key string
		var value string
		if err := rows.Scan(&key, &value); err != nil {
			return fmt.Errorf("scan metadata: %w", err)
		}
		h.applyMetaValue(key, value)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate metadata: %w", err)
	}
	return nil
}

func (h *Index) loadGraph(db *sql.DB) error {
	rows, err := db.Query(`SELECT source_id, target_id, level FROM hnsw_edges ORDER BY level`)
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
	var sourceID string
	var targetID string
	var level int

	if err := rows.Scan(&sourceID, &targetID, &level); err != nil {
		return fmt.Errorf("scan graph row: %w", err)
	}

	h.ensureLayers(level)
	h.layers[level].addNode(sourceID)
	h.layers[level].addNode(targetID)
	h.layers[level].addNeighbor(sourceID, targetID, 0, h.maxNeighborsForLevel(level))
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
	if err := rows.Err(); err != nil {
		return err
	}

	// W4P.15: Recompute edge distances after loading vectors
	h.recomputeEdgeDistances()
	return nil
}

func (h *Index) loadVectorRow(rows *sql.Rows) error {
	var nodeID string
	var embeddingBlob []byte
	var magnitude float64
	var domain int64
	var nodeType int64

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

// recomputeEdgeDistances recalculates all edge distances using stored vectors.
// W4P.15: Called after LoadVectors to restore correct neighbor ordering.
// W12.10: Restructured to avoid holding h.mu while acquiring layer locks.
// Caller must hold h.mu (write lock).
func (h *Index) recomputeEdgeDistances() {
	for _, layer := range h.layers {
		h.recomputeLayerDistances(layer)
	}
}

// recomputeLayerDistances recalculates distances for all edges in a single layer.
// W12.10: Collects all data with proper lock ordering to avoid deadlock.
// Caller must hold h.mu (write lock).
func (h *Index) recomputeLayerDistances(l *layer) {
	// Collect node-neighbor pairs with layer lock held
	updates := h.collectDistanceUpdates(l)

	// Apply updates - layer's ConcurrentNeighborSet handles its own locking
	for _, update := range updates {
		update.neighbors.UpdateDistance(update.neighborID, update.distance)
	}
}

// distanceUpdate holds data needed to update a single neighbor distance.
// W12.10: Used to batch updates and avoid holding multiple locks.
type distanceUpdate struct {
	neighbors  *ConcurrentNeighborSet
	neighborID string
	distance   float32
}

// collectDistanceUpdates collects all distance updates for a layer.
// W12.10: Acquires layer lock, computes distances, releases lock before returning.
// Caller must hold h.mu (read or write lock).
func (h *Index) collectDistanceUpdates(l *layer) []distanceUpdate {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var updates []distanceUpdate
	for nodeID, node := range l.nodes {
		nodeUpdates := h.computeNodeDistanceUpdates(nodeID, node)
		updates = append(updates, nodeUpdates...)
	}
	return updates
}

// computeNodeDistanceUpdates computes distance updates for all neighbors of a node.
// W12.10: Does not acquire any locks - caller must hold both h.mu and l.mu.
func (h *Index) computeNodeDistanceUpdates(nodeID string, node *layerNode) []distanceUpdate {
	srcVec, srcMag, srcOK := h.getVectorAndMagnitudeLocked(nodeID)
	if !srcOK {
		return nil
	}

	neighbors := node.neighbors.GetSortedNeighbors()
	updates := make([]distanceUpdate, 0, len(neighbors))

	for _, neighbor := range neighbors {
		dstVec, dstMag, dstOK := h.getVectorAndMagnitudeLocked(neighbor.ID)
		if !dstOK {
			continue
		}
		similarity := CosineSimilarity(srcVec, dstVec, srcMag, dstMag)
		distance := float32(1.0 - similarity)
		updates = append(updates, distanceUpdate{
			neighbors:  node.neighbors,
			neighborID: neighbor.ID,
			distance:   distance,
		})
	}
	return updates
}

// getVectorAndMagnitudeLocked retrieves vector and magnitude without locking.
// Caller must hold h.mu (read or write lock).
func (h *Index) getVectorAndMagnitudeLocked(id string) ([]float32, float64, bool) {
	vec, vecExists := h.vectors[id]
	if !vecExists {
		return nil, 0, false
	}
	mag, magExists := h.magnitudes[id]
	if !magExists {
		return nil, 0, false
	}
	return vec, mag, true
}

func (h *Index) ensureMetaKeys(tx *sql.Tx) error {
	keys := []string{"entry_point", "max_level", "m", "ef_construct", "ef_search", "level_mult", "total_nodes"}
	for _, key := range keys {
		if _, err := tx.Exec("INSERT OR IGNORE INTO hnsw_meta (key, value) VALUES (?, '')", key); err != nil {
			return fmt.Errorf("insert meta key %s: %w", key, err)
		}
	}
	return nil
}

func (h *Index) applyMetaValue(key, value string) {
	switch key {
	case "entry_point":
		h.entryPoint = value
	case "max_level":
		h.maxLevel = parseIntDefault(value, -1)
	case "m":
		h.M = parseIntDefault(value, h.M)
	case "ef_construct":
		h.efConstruct = parseIntDefault(value, h.efConstruct)
	case "ef_search":
		h.efSearch = parseIntDefault(value, h.efSearch)
	case "level_mult":
		h.levelMult = parseFloatDefault(value, h.levelMult)
	case "total_nodes":
		return
	}
}

func (h *Index) refreshDerivedFields() {
	maxLevel := -1
	for level, layer := range h.layers {
		if layer.nodeCount() > 0 {
			maxLevel = level
		}
	}
	h.maxLevel = maxLevel

	if h.entryPoint == "" {
		for level := h.maxLevel; level >= 0; level-- {
			ids := h.layers[level].allNodeIDs()
			if len(ids) > 0 {
				h.entryPoint = ids[0]
				break
			}
		}
	}
}

func (h *Index) maxNeighborsForLevel(level int) int {
	if level == 0 {
		return h.M * 2
	}
	return h.M
}

func parseIntDefault(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func parseFloatDefault(value string, fallback float64) float64 {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
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
