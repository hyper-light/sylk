package hnsw

import (
	"database/sql"
	"fmt"
	"math"
	"strconv"

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

	entryPointStr := ""
	if h.entryPoint != invalidNodeID {
		entryPointStr = h.idToString[h.entryPoint]
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
	`, entryPointStr, h.maxLevel, h.M, h.efConstruct, h.efSearch, h.levelMult, len(h.vectors))
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
		nodeIDStr := h.idToString[nodeID]
		neighborIDs := node.neighbors.GetIDs()
		for _, neighborID := range neighborIDs {
			neighborIDStr := h.idToString[neighborID]
			if _, err := stmt.Exec(nodeIDStr, neighborIDStr, layerIdx); err != nil {
				return fmt.Errorf("insert graph edge: %w", err)
			}
		}
	}
	return nil
}

func (h *Index) Load(db *sql.DB) error {
	stagedMeta, err := h.stageMetadataLoad(db)
	if err != nil {
		return err
	}

	stagedGraph, err := h.stageGraphLoad(db)
	if err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	h.commitStagedMetadata(stagedMeta)

	if h.maxLevel >= 0 {
		h.ensureLayers(h.maxLevel)
	}

	h.commitStagedGraph(stagedGraph)
	h.refreshDerivedFields()
	return nil
}

type stagedMetadata struct {
	entryPoint  string
	maxLevel    int
	m           int
	efConstruct int
	efSearch    int
	levelMult   float64
}

func (h *Index) stageMetadataLoad(db *sql.DB) (*stagedMetadata, error) {
	rows, err := db.Query("SELECT key, value FROM hnsw_meta")
	if err != nil {
		return nil, fmt.Errorf("query metadata: %w", err)
	}
	defer rows.Close()

	staged := &stagedMetadata{
		maxLevel:    -1,
		m:           h.M,
		efConstruct: h.efConstruct,
		efSearch:    h.efSearch,
		levelMult:   h.levelMult,
	}

	for rows.Next() {
		var key string
		var value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("scan metadata: %w", err)
		}
		h.applyStagedMetaValue(key, value, staged)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate metadata: %w", err)
	}
	return staged, nil
}

func (h *Index) applyStagedMetaValue(key, value string, staged *stagedMetadata) {
	switch key {
	case "entry_point":
		staged.entryPoint = value
	case "max_level":
		staged.maxLevel = parseIntDefault(value, -1)
	case "m":
		staged.m = parseIntDefault(value, staged.m)
	case "ef_construct":
		staged.efConstruct = parseIntDefault(value, staged.efConstruct)
	case "ef_search":
		staged.efSearch = parseIntDefault(value, staged.efSearch)
	case "level_mult":
		staged.levelMult = parseFloatDefault(value, staged.levelMult)
	}
}

func (h *Index) commitStagedMetadata(staged *stagedMetadata) {
	if staged.entryPoint == "" {
		h.entryPoint = invalidNodeID
	} else {
		internalID, exists := h.stringToID[staged.entryPoint]
		if exists {
			h.entryPoint = internalID
		} else {
			h.entryPoint = invalidNodeID
		}
	}
	h.maxLevel = staged.maxLevel
	h.M = staged.m
	h.efConstruct = staged.efConstruct
	h.efSearch = staged.efSearch
	h.levelMult = staged.levelMult
}

type stagedGraphEdge struct {
	sourceID string
	targetID string
	level    int
}

func (h *Index) stageGraphLoad(db *sql.DB) ([]stagedGraphEdge, error) {
	rows, err := db.Query(`SELECT source_id, target_id, level FROM hnsw_edges ORDER BY level`)
	if err != nil {
		return nil, fmt.Errorf("query graph: %w", err)
	}
	defer rows.Close()

	var staged []stagedGraphEdge
	for rows.Next() {
		edge, err := h.stageGraphRow(rows)
		if err != nil {
			return nil, err
		}
		staged = append(staged, edge)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate graph rows: %w", err)
	}
	return staged, nil
}

func (h *Index) stageGraphRow(rows *sql.Rows) (stagedGraphEdge, error) {
	var edge stagedGraphEdge
	if err := rows.Scan(&edge.sourceID, &edge.targetID, &edge.level); err != nil {
		return stagedGraphEdge{}, fmt.Errorf("scan graph row: %w", err)
	}
	return edge, nil
}

func (h *Index) commitStagedGraph(staged []stagedGraphEdge) {
	for _, edge := range staged {
		h.ensureLayers(edge.level)
		sourceID := h.getOrCreateInternalID(edge.sourceID)
		targetID := h.getOrCreateInternalID(edge.targetID)
		h.layers[edge.level].addNode(sourceID)
		h.layers[edge.level].addNode(targetID)
		h.layers[edge.level].addNeighbor(sourceID, targetID, 0, h.maxNeighborsForLevel(edge.level))
	}
}

func (h *Index) getOrCreateInternalID(stringID string) uint32 {
	if internalID, exists := h.stringToID[stringID]; exists {
		return internalID
	}
	internalID := h.nextID
	h.nextID++
	h.stringToID[stringID] = internalID
	h.idToString = append(h.idToString, stringID)
	return internalID
}

func (h *Index) LoadVectors(db *sql.DB) error {
	staged, err := h.stageVectorLoad(db)
	if err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	h.commitStagedVectors(staged)

	h.recomputeEdgeDistances()
	return nil
}

type stagedVectorData struct {
	vectors    map[string][]float32
	magnitudes map[string]float64
	domains    map[string]vectorgraphdb.Domain
	nodeTypes  map[string]vectorgraphdb.NodeType
}

func (h *Index) stageVectorLoad(db *sql.DB) (*stagedVectorData, error) {
	rows, err := db.Query(`
		SELECT v.node_id, v.embedding, v.magnitude, n.domain, n.node_type
		FROM vectors v
		JOIN nodes n ON v.node_id = n.id
	`)
	if err != nil {
		return nil, fmt.Errorf("query vectors: %w", err)
	}
	defer rows.Close()

	staged := &stagedVectorData{
		vectors:    make(map[string][]float32),
		magnitudes: make(map[string]float64),
		domains:    make(map[string]vectorgraphdb.Domain),
		nodeTypes:  make(map[string]vectorgraphdb.NodeType),
	}

	for rows.Next() {
		if err := h.stageVectorRow(rows, staged); err != nil {
			return nil, err
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate vector rows: %w", err)
	}
	return staged, nil
}

func (h *Index) stageVectorRow(rows *sql.Rows, staged *stagedVectorData) error {
	var nodeID string
	var embeddingBlob []byte
	var magnitude float64
	var domain int64
	var nodeType int64

	if err := rows.Scan(&nodeID, &embeddingBlob, &magnitude, &domain, &nodeType); err != nil {
		return fmt.Errorf("scan vector row: %w", err)
	}

	embedding := bytesToFloat32s(embeddingBlob)
	staged.vectors[nodeID] = embedding
	staged.magnitudes[nodeID] = magnitude
	staged.domains[nodeID] = vectorgraphdb.Domain(domain)
	staged.nodeTypes[nodeID] = vectorgraphdb.NodeType(nodeType)
	return nil
}

func (h *Index) commitStagedVectors(staged *stagedVectorData) {
	for stringID, vec := range staged.vectors {
		internalID := h.getOrCreateInternalID(stringID)
		h.vectors[internalID] = vec
	}
	for stringID, mag := range staged.magnitudes {
		internalID := h.stringToID[stringID]
		h.magnitudes[internalID] = mag
	}
	for stringID, domain := range staged.domains {
		internalID := h.stringToID[stringID]
		h.domains[internalID] = domain
	}
	for stringID, nodeType := range staged.nodeTypes {
		internalID := h.stringToID[stringID]
		h.nodeTypes[internalID] = nodeType
	}
}

func (h *Index) recomputeEdgeDistances() {
	for _, layer := range h.layers {
		h.recomputeLayerDistances(layer)
	}
}

func (h *Index) recomputeLayerDistances(l *layer) {
	updates := h.collectDistanceUpdates(l)
	for _, update := range updates {
		update.neighbors.UpdateDistance(update.neighborID, update.distance)
	}
}

type distanceUpdate struct {
	neighbors  *ConcurrentNeighborSet
	neighborID uint32
	distance   float32
}

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

func (h *Index) computeNodeDistanceUpdates(nodeID uint32, node *layerNode) []distanceUpdate {
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

func (h *Index) getVectorAndMagnitudeLocked(id uint32) ([]float32, float64, bool) {
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

func (h *Index) refreshDerivedFields() {
	maxLevel := -1
	for level, layer := range h.layers {
		if layer.nodeCount() > 0 {
			maxLevel = level
		}
	}
	h.maxLevel = maxLevel

	if h.entryPoint == invalidNodeID {
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
	return math.Float32frombits(b)
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
	return math.Float32bits(f)
}
