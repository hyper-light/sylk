package sylkdir

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
)

// Edge shard storage constants.
const (
	// DefaultEdgeShardSize is the node ID range per shard.
	DefaultEdgeShardSize = 65536

	// EdgeRecordSize is the fixed size of a binary edge record.
	// Layout: SourceID(4) + TargetID(4) + Type(1) + Weight(4) + SessionID(4) + AgentID(2) + CreatedAt(8) + UpdatedAt(8)
	EdgeRecordSize = 35
)

// ErrEdgeNotFound is returned when an edge doesn't exist.
var ErrEdgeNotFound = errors.New("sylkdir: edge not found")

// Edge represents an edge in the knowledge graph.
type Edge struct {
	// Identity (compound key)
	SourceID uint32 // Source node ID
	TargetID uint32 // Target node ID
	Type     uint8  // Edge type enum

	// Payload
	Weight float32 // Computed weight

	// Provenance
	SessionID uint32 // Session that created/updated
	AgentID   uint16 // Agent that created/updated
	CreatedAt uint64 // Unix nano (immutable)
	UpdatedAt uint64 // Unix nano (mutable)
}

// EdgeKey returns the compound key for this edge.
func (e *Edge) EdgeKey() EdgeKey {
	return EdgeKey{
		SourceID: e.SourceID,
		TargetID: e.TargetID,
		Type:     e.Type,
	}
}

// EdgeKey is the compound key that uniquely identifies an edge.
type EdgeKey struct {
	SourceID uint32
	TargetID uint32
	Type     uint8
}

// MarshalBinary encodes the edge to a fixed-size binary record.
func (e *Edge) MarshalBinary() []byte {
	buf := make([]byte, EdgeRecordSize)
	e.MarshalBinaryTo(buf)
	return buf
}

// MarshalBinaryTo encodes the edge into the provided buffer (must be >= EdgeRecordSize).
func (e *Edge) MarshalBinaryTo(buf []byte) {
	binary.LittleEndian.PutUint32(buf[0:4], e.SourceID)
	binary.LittleEndian.PutUint32(buf[4:8], e.TargetID)
	buf[8] = e.Type
	binary.LittleEndian.PutUint32(buf[9:13], math.Float32bits(e.Weight))
	binary.LittleEndian.PutUint32(buf[13:17], e.SessionID)
	binary.LittleEndian.PutUint16(buf[17:19], e.AgentID)
	binary.LittleEndian.PutUint64(buf[19:27], e.CreatedAt)
	binary.LittleEndian.PutUint64(buf[27:35], e.UpdatedAt)
}

// UnmarshalBinary decodes the edge from a binary record.
func (e *Edge) UnmarshalBinary(data []byte) error {
	if len(data) < EdgeRecordSize {
		return fmt.Errorf("sylkdir: edge data too short: %d < %d", len(data), EdgeRecordSize)
	}

	e.SourceID = binary.LittleEndian.Uint32(data[0:4])
	e.TargetID = binary.LittleEndian.Uint32(data[4:8])
	e.Type = data[8]
	e.Weight = math.Float32frombits(binary.LittleEndian.Uint32(data[9:13]))
	e.SessionID = binary.LittleEndian.Uint32(data[13:17])
	e.AgentID = binary.LittleEndian.Uint16(data[17:19])
	e.CreatedAt = binary.LittleEndian.Uint64(data[19:27])
	e.UpdatedAt = binary.LittleEndian.Uint64(data[27:35])

	return nil
}

// EdgeShardStore manages edge storage in sharded binary files.
// Edges are sharded by sourceID / shardSize.
// Each shard contains:
//   - edges.bin: packed edge records
//   - outgoing.idx: sourceID → edge offsets
//   - incoming.idx: targetID → edge offsets
type EdgeShardStore struct {
	basePath  string
	shardSize uint32
	mu        sync.RWMutex

	// In-memory indexes
	outgoingIndex map[uint32][]int64 // sourceID → file offsets
	incomingIndex map[uint32][]int64 // targetID → file offsets
	edgeKeyIndex  map[EdgeKey]int64  // edge key → file offset (for updates)

	// Track which shard each offset belongs to
	shardForOffset map[int64]uint32
}

// NewEdgeShardStore creates a new edge shard store.
func NewEdgeShardStore(basePath string) *EdgeShardStore {
	return &EdgeShardStore{
		basePath:       basePath,
		shardSize:      DefaultEdgeShardSize,
		outgoingIndex:  make(map[uint32][]int64),
		incomingIndex:  make(map[uint32][]int64),
		edgeKeyIndex:   make(map[EdgeKey]int64),
		shardForOffset: make(map[int64]uint32),
	}
}

// NewEdgeShardStoreFromSylkDir creates an EdgeShardStore from a SylkDir.
func NewEdgeShardStoreFromSylkDir(sd *SylkDir) *EdgeShardStore {
	return NewEdgeShardStore(sd.GlobalEdgeDataPath())
}

// Init initializes the edge shard store, loading indexes if present.
func (s *EdgeShardStore) Init() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Scan for existing shards and load indexes
	if err := os.MkdirAll(s.basePath, 0755); err != nil {
		return fmt.Errorf("sylkdir: failed to create edges directory: %w", err)
	}

	entries, err := os.ReadDir(s.basePath)
	if err != nil {
		return fmt.Errorf("sylkdir: failed to read edges directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			shardPath := filepath.Join(s.basePath, entry.Name())
			if err := s.loadShardIndex(shardPath); err != nil {
				// Log but continue - partial index is better than none
				continue
			}
		}
	}

	return nil
}

// Write writes an edge to the appropriate shard.
// If the edge already exists, it updates the record.
func (s *EdgeShardStore) Write(edge *Edge) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if edge exists (for update)
	key := edge.EdgeKey()
	if existingOffset, exists := s.edgeKeyIndex[key]; exists {
		// Update existing edge
		return s.updateEdgeAtOffset(edge, existingOffset)
	}

	// New edge - append to shard
	shardNum := edge.SourceID / s.shardSize
	shardPath := s.shardPath(shardNum)

	// Ensure shard directory exists
	if err := os.MkdirAll(shardPath, 0755); err != nil {
		return fmt.Errorf("sylkdir: failed to create shard directory: %w", err)
	}

	edgesFile := filepath.Join(shardPath, "edges.bin")
	f, err := os.OpenFile(edgesFile, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("sylkdir: failed to open edges file: %w", err)
	}
	defer f.Close()

	// Get current offset
	offset, err := f.Seek(0, 2)
	if err != nil {
		return fmt.Errorf("sylkdir: failed to seek: %w", err)
	}

	// Encode unique offset combining shard and position
	// Format: shard(16 bits) << 48 | offset(48 bits)
	globalOffset := int64(shardNum)<<48 | offset

	// Write edge record
	data := edge.MarshalBinary()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("sylkdir: failed to write edge: %w", err)
	}

	// Update indexes
	s.outgoingIndex[edge.SourceID] = append(s.outgoingIndex[edge.SourceID], globalOffset)
	s.incomingIndex[edge.TargetID] = append(s.incomingIndex[edge.TargetID], globalOffset)
	s.edgeKeyIndex[key] = globalOffset
	s.shardForOffset[globalOffset] = shardNum

	return nil
}

// updateEdgeAtOffset updates an existing edge at the given offset.
func (s *EdgeShardStore) updateEdgeAtOffset(edge *Edge, globalOffset int64) error {
	shardNum := uint32(globalOffset >> 48)
	localOffset := globalOffset & 0xFFFFFFFFFFFF

	shardPath := s.shardPath(shardNum)
	edgesFile := filepath.Join(shardPath, "edges.bin")

	f, err := os.OpenFile(edgesFile, os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("sylkdir: failed to open edges file for update: %w", err)
	}
	defer f.Close()

	if _, err := f.Seek(localOffset, 0); err != nil {
		return fmt.Errorf("sylkdir: failed to seek for update: %w", err)
	}

	data := edge.MarshalBinary()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("sylkdir: failed to write edge update: %w", err)
	}

	return nil
}

// WriteBatch writes multiple edges. Edges with existing keys are updated in place.
func (s *EdgeShardStore) WriteBatch(edges []*Edge) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Group new edges by shard, collect updates separately
	type shardWrite struct {
		edge  *Edge
		shard uint32
	}
	var newEdges []shardWrite
	var updates []shardWrite

	for _, edge := range edges {
		key := edge.EdgeKey()
		if _, exists := s.edgeKeyIndex[key]; exists {
			updates = append(updates, shardWrite{edge: edge, shard: uint32(s.edgeKeyIndex[key] >> 48)})
		} else {
			newEdges = append(newEdges, shardWrite{edge: edge, shard: edge.SourceID / s.shardSize})
		}
	}

	// Process updates (in-place writes)
	for _, upd := range updates {
		offset := s.edgeKeyIndex[upd.edge.EdgeKey()]
		if err := s.updateEdgeAtOffset(upd.edge, offset); err != nil {
			return err
		}
	}

	// Group new edges by shard for sequential I/O
	shardBatches := make(map[uint32][]*Edge)
	for _, sw := range newEdges {
		shardBatches[sw.shard] = append(shardBatches[sw.shard], sw.edge)
	}

	for shardNum, batch := range shardBatches {
		if err := s.writeShardBatch(shardNum, batch); err != nil {
			return err
		}
	}

	return nil
}

// writeShardBatch writes a batch of new edges to a single shard.
// Caller must hold the write lock.
func (s *EdgeShardStore) writeShardBatch(shardNum uint32, edges []*Edge) error {
	shardPath := s.shardPath(shardNum)
	if err := os.MkdirAll(shardPath, 0755); err != nil {
		return fmt.Errorf("sylkdir: create shard dir: %w", err)
	}

	edgesFile := filepath.Join(shardPath, "edges.bin")
	f, err := os.OpenFile(edgesFile, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("sylkdir: open edges file: %w", err)
	}
	defer f.Close()

	offset, err := f.Seek(0, 2)
	if err != nil {
		return fmt.Errorf("sylkdir: seek: %w", err)
	}

	for _, edge := range edges {
		globalOffset := int64(shardNum)<<48 | offset
		data := edge.MarshalBinary()
		if _, err := f.Write(data); err != nil {
			return fmt.Errorf("sylkdir: write edge: %w", err)
		}

		key := edge.EdgeKey()
		s.outgoingIndex[edge.SourceID] = append(s.outgoingIndex[edge.SourceID], globalOffset)
		s.incomingIndex[edge.TargetID] = append(s.incomingIndex[edge.TargetID], globalOffset)
		s.edgeKeyIndex[key] = globalOffset
		s.shardForOffset[globalOffset] = shardNum
		offset += int64(len(data))
	}

	return nil
}

// ReadAll returns all edges in the store (no filtering).
func (s *EdgeShardStore) ReadAll() ([]*Edge, error) {
	s.mu.RLock()
	offsets := make([]int64, 0, len(s.edgeKeyIndex))
	for _, offset := range s.edgeKeyIndex {
		offsets = append(offsets, offset)
	}
	s.mu.RUnlock()

	return s.readEdgesAtOffsets(offsets)
}

// ReadAllFiltered returns all edges where filter returns true.
// The filter receives sourceID and targetID for each edge.
func (s *EdgeShardStore) ReadAllFiltered(filter func(sourceID, targetID uint32) bool) ([]*Edge, error) {
	all, err := s.ReadAll()
	if err != nil {
		return nil, err
	}

	filtered := make([]*Edge, 0, len(all))
	for _, edge := range all {
		if filter(edge.SourceID, edge.TargetID) {
			filtered = append(filtered, edge)
		}
	}
	return filtered, nil
}

// GetOutgoing returns all edges with the given source node ID.
func (s *EdgeShardStore) GetOutgoing(sourceID uint32) ([]*Edge, error) {
	s.mu.RLock()
	offsets := s.outgoingIndex[sourceID]
	s.mu.RUnlock()

	return s.readEdgesAtOffsets(offsets)
}

// GetIncoming returns all edges with the given target node ID.
func (s *EdgeShardStore) GetIncoming(targetID uint32) ([]*Edge, error) {
	s.mu.RLock()
	offsets := s.incomingIndex[targetID]
	s.mu.RUnlock()

	return s.readEdgesAtOffsets(offsets)
}

// GetEdge returns a specific edge by its compound key.
func (s *EdgeShardStore) GetEdge(sourceID, targetID uint32, edgeType uint8) (*Edge, error) {
	key := EdgeKey{SourceID: sourceID, TargetID: targetID, Type: edgeType}

	s.mu.RLock()
	offset, ok := s.edgeKeyIndex[key]
	s.mu.RUnlock()

	if !ok {
		return nil, ErrEdgeNotFound
	}

	edges, err := s.readEdgesAtOffsets([]int64{offset})
	if err != nil {
		return nil, err
	}
	if len(edges) == 0 {
		return nil, ErrEdgeNotFound
	}
	return edges[0], nil
}

// readEdgesAtOffsets reads edges at the given global offsets.
func (s *EdgeShardStore) readEdgesAtOffsets(offsets []int64) ([]*Edge, error) {
	if len(offsets) == 0 {
		return nil, nil
	}

	// Group by shard for efficient I/O
	shardOffsets := make(map[uint32][]int64)
	for _, off := range offsets {
		shardNum := uint32(off >> 48)
		shardOffsets[shardNum] = append(shardOffsets[shardNum], off&0xFFFFFFFFFFFF)
	}

	var result []*Edge
	buf := make([]byte, EdgeRecordSize)

	for shardNum, localOffsets := range shardOffsets {
		shardPath := s.shardPath(shardNum)
		edgesFile := filepath.Join(shardPath, "edges.bin")

		f, err := os.Open(edgesFile)
		if err != nil {
			continue // Shard file not found
		}

		for _, localOffset := range localOffsets {
			if _, err := f.Seek(localOffset, 0); err != nil {
				continue
			}
			if _, err := f.Read(buf); err != nil {
				continue
			}

			edge := &Edge{}
			if err := edge.UnmarshalBinary(buf); err == nil {
				result = append(result, edge)
			}
		}

		f.Close()
	}

	return result, nil
}

// Count returns the total number of edges.
func (s *EdgeShardStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.edgeKeyIndex)
}

// SaveIndexes persists all shard indexes to disk.
func (s *EdgeShardStore) SaveIndexes() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Group edges by shard
	shardEdges := make(map[uint32][]EdgeKey)
	for key, offset := range s.edgeKeyIndex {
		shardNum := uint32(offset >> 48)
		shardEdges[shardNum] = append(shardEdges[shardNum], key)
	}

	// Save each shard's indexes
	for shardNum := range shardEdges {
		if err := s.saveShardIndex(shardNum); err != nil {
			return err
		}
	}

	return nil
}

// saveShardIndex writes the index files for a shard.
func (s *EdgeShardStore) saveShardIndex(shardNum uint32) error {
	shardPath := s.shardPath(shardNum)

	// Build outgoing index for this shard
	outgoingFile := filepath.Join(shardPath, "outgoing.idx")
	if err := s.writeNodeIndex(outgoingFile, s.outgoingIndex, shardNum); err != nil {
		return err
	}

	// Build incoming index for this shard
	incomingFile := filepath.Join(shardPath, "incoming.idx")
	if err := s.writeNodeIndex(incomingFile, s.incomingIndex, shardNum); err != nil {
		return err
	}

	return nil
}

// writeNodeIndex writes a nodeID → offsets index to disk.
func (s *EdgeShardStore) writeNodeIndex(path string, index map[uint32][]int64, shardNum uint32) error {
	// Count entries belonging to this shard
	var entries []struct {
		nodeID  uint32
		offsets []int64
	}

	for nodeID, offsets := range index {
		var shardOffsets []int64
		for _, off := range offsets {
			if uint32(off>>48) == shardNum {
				shardOffsets = append(shardOffsets, off&0xFFFFFFFFFFFF)
			}
		}
		if len(shardOffsets) > 0 {
			entries = append(entries, struct {
				nodeID  uint32
				offsets []int64
			}{nodeID, shardOffsets})
		}
	}

	// Calculate buffer size: count(4) + entries(nodeID(4) + numOffsets(4) + offsets(8 each))
	totalSize := 4
	for _, e := range entries {
		totalSize += 4 + 4 + len(e.offsets)*8
	}

	buf := make([]byte, totalSize)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(len(entries)))

	offset := 4
	for _, e := range entries {
		binary.LittleEndian.PutUint32(buf[offset:offset+4], e.nodeID)
		offset += 4
		binary.LittleEndian.PutUint32(buf[offset:offset+4], uint32(len(e.offsets)))
		offset += 4
		for _, off := range e.offsets {
			binary.LittleEndian.PutUint64(buf[offset:offset+8], uint64(off))
			offset += 8
		}
	}

	tmpFile := path + ".tmp"
	if err := os.WriteFile(tmpFile, buf, 0644); err != nil {
		return fmt.Errorf("sylkdir: failed to write index: %w", err)
	}

	if err := os.Rename(tmpFile, path); err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("sylkdir: failed to rename index: %w", err)
	}

	return nil
}

// loadShardIndex loads indexes from a shard directory.
func (s *EdgeShardStore) loadShardIndex(shardPath string) error {
	// Parse shard number from path
	var shardNum uint32
	_, err := fmt.Sscanf(filepath.Base(shardPath), "shard_%04d", &shardNum)
	if err != nil {
		return nil // Not a shard directory
	}

	// Load outgoing index
	outgoingFile := filepath.Join(shardPath, "outgoing.idx")
	if err := s.readNodeIndex(outgoingFile, s.outgoingIndex, shardNum); err != nil {
		// Try to rebuild from edges.bin
		return s.rebuildShardIndex(shardPath, shardNum)
	}

	// Load incoming index
	incomingFile := filepath.Join(shardPath, "incoming.idx")
	if err := s.readNodeIndex(incomingFile, s.incomingIndex, shardNum); err != nil {
		return s.rebuildShardIndex(shardPath, shardNum)
	}

	// Rebuild edgeKeyIndex by scanning edges
	return s.rebuildKeyIndex(shardPath, shardNum)
}

// readNodeIndex reads a nodeID → offsets index from disk.
func (s *EdgeShardStore) readNodeIndex(path string, index map[uint32][]int64, shardNum uint32) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	if len(data) < 4 {
		return fmt.Errorf("sylkdir: index file too short")
	}

	count := binary.LittleEndian.Uint32(data[0:4])
	offset := 4

	for i := uint32(0); i < count; i++ {
		if offset+8 > len(data) {
			return fmt.Errorf("sylkdir: index truncated")
		}

		nodeID := binary.LittleEndian.Uint32(data[offset : offset+4])
		numOffsets := binary.LittleEndian.Uint32(data[offset+4 : offset+8])
		offset += 8

		if offset+int(numOffsets)*8 > len(data) {
			return fmt.Errorf("sylkdir: index truncated")
		}

		for j := uint32(0); j < numOffsets; j++ {
			localOff := int64(binary.LittleEndian.Uint64(data[offset : offset+8]))
			globalOff := int64(shardNum)<<48 | localOff
			index[nodeID] = append(index[nodeID], globalOff)
			offset += 8
		}
	}

	return nil
}

// rebuildShardIndex rebuilds indexes by scanning edges.bin.
func (s *EdgeShardStore) rebuildShardIndex(shardPath string, shardNum uint32) error {
	edgesFile := filepath.Join(shardPath, "edges.bin")
	data, err := os.ReadFile(edgesFile)
	if err != nil {
		return nil // No edges file
	}

	for localOffset := int64(0); localOffset+EdgeRecordSize <= int64(len(data)); localOffset += EdgeRecordSize {
		edge := &Edge{}
		if err := edge.UnmarshalBinary(data[localOffset : localOffset+EdgeRecordSize]); err != nil {
			continue
		}

		globalOffset := int64(shardNum)<<48 | localOffset
		s.outgoingIndex[edge.SourceID] = append(s.outgoingIndex[edge.SourceID], globalOffset)
		s.incomingIndex[edge.TargetID] = append(s.incomingIndex[edge.TargetID], globalOffset)
		s.edgeKeyIndex[edge.EdgeKey()] = globalOffset
		s.shardForOffset[globalOffset] = shardNum
	}

	return nil
}

// rebuildKeyIndex rebuilds the edgeKeyIndex by scanning edges.bin.
func (s *EdgeShardStore) rebuildKeyIndex(shardPath string, shardNum uint32) error {
	edgesFile := filepath.Join(shardPath, "edges.bin")
	data, err := os.ReadFile(edgesFile)
	if err != nil {
		return nil // No edges file
	}

	for localOffset := int64(0); localOffset+EdgeRecordSize <= int64(len(data)); localOffset += EdgeRecordSize {
		edge := &Edge{}
		if err := edge.UnmarshalBinary(data[localOffset : localOffset+EdgeRecordSize]); err != nil {
			continue
		}

		globalOffset := int64(shardNum)<<48 | localOffset
		s.edgeKeyIndex[edge.EdgeKey()] = globalOffset
		s.shardForOffset[globalOffset] = shardNum
	}

	return nil
}

func (s *EdgeShardStore) shardPath(shardNum uint32) string {
	return filepath.Join(s.basePath, fmt.Sprintf("shard_%04d", shardNum))
}

// Close saves indexes and releases resources.
func (s *EdgeShardStore) Close() error {
	return s.SaveIndexes()
}

// EdgeStats returns statistics about the edge store.
type EdgeStats struct {
	TotalEdges    int
	ShardCount    int
	OutgoingNodes int // Nodes with outgoing edges
	IncomingNodes int // Nodes with incoming edges
}

func (s *EdgeShardStore) Stats() EdgeStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	shards := make(map[uint32]bool)
	for _, off := range s.edgeKeyIndex {
		shards[uint32(off>>48)] = true
	}

	return EdgeStats{
		TotalEdges:    len(s.edgeKeyIndex),
		ShardCount:    len(shards),
		OutgoingNodes: len(s.outgoingIndex),
		IncomingNodes: len(s.incomingIndex),
	}
}
