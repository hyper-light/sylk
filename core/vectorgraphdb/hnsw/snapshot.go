package hnsw

import (
	"encoding/binary"
	"errors"
	"hash"
	"hash/crc32"
	"math"
	"sort"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

var (
	ErrSnapshotChecksumMismatch = errors.New("hnsw: snapshot checksum mismatch")
	ErrSnapshotNotValidated     = errors.New("hnsw: snapshot not validated")
)

type LayerSnapshot struct {
	Nodes map[string][]string
}

type HNSWSnapshot struct {
	ID         uint64
	SeqNum     uint64
	CreatedAt  time.Time
	EntryPoint string
	MaxLevel   int
	EfSearch   int
	Layers     []LayerSnapshot
	Vectors    map[string][]float32
	Magnitudes map[string]float64
	Domains    map[string]vectorgraphdb.Domain
	NodeTypes  map[string]vectorgraphdb.NodeType
	Checksum   uint32
	validated  bool
	readers    atomic.Int32
}

func NewLayerSnapshot(l *layer, idToString []string) LayerSnapshot {
	if l == nil {
		return LayerSnapshot{Nodes: make(map[string][]string)}
	}
	return LayerSnapshot{Nodes: copyLayerNodes(l, idToString)}
}

func copyLayerNodes(l *layer, idToString []string) map[string][]string {
	l.mu.RLock()
	defer l.mu.RUnlock()

	nodes := make(map[string][]string, len(l.nodes))
	for id, node := range l.nodes {
		stringID := idToString[id]
		neighborIDs := node.neighbors.GetIDs()
		stringNeighbors := make([]string, len(neighborIDs))
		for i, nid := range neighborIDs {
			stringNeighbors[i] = idToString[nid]
		}
		nodes[stringID] = stringNeighbors
	}
	return nodes
}

func copyStringSlice(src []string) []string {
	if src == nil {
		return nil
	}
	dst := make([]string, len(src))
	copy(dst, src)
	return dst
}

func (ls *LayerSnapshot) GetNeighbors(id string) []string {
	neighbors, exists := ls.Nodes[id]
	if !exists {
		return nil
	}
	return copyStringSlice(neighbors)
}

func (ls *LayerSnapshot) HasNode(id string) bool {
	_, exists := ls.Nodes[id]
	return exists
}

func (ls *LayerSnapshot) NodeCount() int {
	return len(ls.Nodes)
}

func NewHNSWSnapshot(idx *Index, seqNum uint64) *HNSWSnapshot {
	if idx == nil {
		snap := &HNSWSnapshot{
			SeqNum:     seqNum,
			CreatedAt:  time.Now(),
			MaxLevel:   -1,
			Layers:     make([]LayerSnapshot, 0),
			Vectors:    make(map[string][]float32),
			Magnitudes: make(map[string]float64),
			Domains:    make(map[string]vectorgraphdb.Domain),
			NodeTypes:  make(map[string]vectorgraphdb.NodeType),
			validated:  true,
		}
		snap.Checksum = snap.computeChecksum()
		return snap
	}
	return createSnapshotFromIndex(idx, seqNum)
}

func createSnapshotFromIndex(idx *Index, seqNum uint64) *HNSWSnapshot {
	entryPointStr := ""
	if idx.entryPoint != invalidNodeID {
		entryPointStr = idx.idToString[idx.entryPoint]
	}

	snap := &HNSWSnapshot{
		SeqNum:     seqNum,
		CreatedAt:  time.Now(),
		EntryPoint: entryPointStr,
		MaxLevel:   idx.maxLevel,
		EfSearch:   idx.efSearch,
		Layers:     copyLayers(idx.layers, idx.idToString),
		Vectors:    idx.GetVectors(),
		Magnitudes: idx.GetMagnitudes(),
		Domains:    idx.GetDomains(),
		NodeTypes:  idx.GetNodeTypes(),
		validated:  true,
	}
	snap.Checksum = snap.computeChecksum()
	return snap
}

func copyLayers(layers []*layer, idToString []string) []LayerSnapshot {
	if len(layers) == 0 {
		return make([]LayerSnapshot, 0)
	}
	return copyLayersWithBatchLock(layers, idToString)
}

func copyLayersWithBatchLock(layers []*layer, idToString []string) []LayerSnapshot {
	for _, l := range layers {
		if l != nil {
			l.mu.RLock()
		}
	}
	defer func() {
		for i := len(layers) - 1; i >= 0; i-- {
			if layers[i] != nil {
				layers[i].mu.RUnlock()
			}
		}
	}()

	snapshots := make([]LayerSnapshot, len(layers))
	for i, l := range layers {
		snapshots[i] = copyLayerNodesUnlocked(l, idToString)
	}
	return snapshots
}

func copyLayerNodesUnlocked(l *layer, idToString []string) LayerSnapshot {
	if l == nil {
		return LayerSnapshot{Nodes: make(map[string][]string)}
	}
	nodes := make(map[string][]string, len(l.nodes))
	for id, node := range l.nodes {
		stringID := idToString[id]
		neighborIDs := node.neighbors.GetIDs()
		stringNeighbors := make([]string, len(neighborIDs))
		for i, nid := range neighborIDs {
			stringNeighbors[i] = idToString[nid]
		}
		nodes[stringID] = stringNeighbors
	}
	return LayerSnapshot{Nodes: nodes}
}

func copyVectors(vectors map[string][]float32) map[string][]float32 {
	result := make(map[string][]float32, len(vectors))
	for id, vec := range vectors {
		result[id] = copyFloat32Slice(vec)
	}
	return result
}

func copyFloat32Slice(src []float32) []float32 {
	if src == nil {
		return nil
	}
	dst := make([]float32, len(src))
	copy(dst, src)
	return dst
}

func copyMagnitudes(magnitudes map[string]float64) map[string]float64 {
	result := make(map[string]float64, len(magnitudes))
	for id, mag := range magnitudes {
		result[id] = mag
	}
	return result
}

func copyDomains(domains map[string]vectorgraphdb.Domain) map[string]vectorgraphdb.Domain {
	result := make(map[string]vectorgraphdb.Domain, len(domains))
	for id, domain := range domains {
		result[id] = domain
	}
	return result
}

func copyNodeTypes(nodeTypes map[string]vectorgraphdb.NodeType) map[string]vectorgraphdb.NodeType {
	result := make(map[string]vectorgraphdb.NodeType, len(nodeTypes))
	for id, nodeType := range nodeTypes {
		result[id] = nodeType
	}
	return result
}

func (s *HNSWSnapshot) AcquireReader() int32 {
	return s.readers.Add(1)
}

func (s *HNSWSnapshot) ReleaseReader() (int32, bool) {
	for {
		current := s.readers.Load()
		if current <= 0 {
			return current, false
		}
		if s.readers.CompareAndSwap(current, current-1) {
			return current - 1, true
		}
	}
}

func (s *HNSWSnapshot) ReaderCount() int32 {
	return s.readers.Load()
}

func (s *HNSWSnapshot) IsEmpty() bool {
	return len(s.Vectors) == 0
}

func (s *HNSWSnapshot) Size() int {
	return len(s.Vectors)
}

func (s *HNSWSnapshot) GetVector(id string) []float32 {
	vec, exists := s.Vectors[id]
	if !exists {
		return nil
	}
	return copyFloat32Slice(vec)
}

func (s *HNSWSnapshot) GetMagnitude(id string) (float64, bool) {
	mag, exists := s.Magnitudes[id]
	return mag, exists
}

func (s *HNSWSnapshot) GetLayer(level int) *LayerSnapshot {
	if level < 0 || level >= len(s.Layers) {
		return nil
	}
	return &s.Layers[level]
}

func (s *HNSWSnapshot) LayerCount() int {
	return len(s.Layers)
}

func (s *HNSWSnapshot) ContainsVector(id string) bool {
	_, exists := s.Vectors[id]
	return exists
}

func (s *HNSWSnapshot) computeChecksum() uint32 {
	h := crc32.NewIEEE()

	writeUint64(h, s.SeqNum)
	writeString(h, s.EntryPoint)
	writeInt(h, s.MaxLevel)
	writeInt(h, s.EfSearch)

	s.checksumVectors(h)
	s.checksumMagnitudes(h)
	s.checksumLayers(h)
	s.checksumDomains(h)
	s.checksumNodeTypes(h)

	return h.Sum32()
}

func (s *HNSWSnapshot) checksumVectors(h hash.Hash32) {
	ids := sortedKeys(s.Vectors)
	for _, id := range ids {
		writeString(h, id)
		vec := s.Vectors[id]
		for _, v := range vec {
			writeFloat32(h, v)
		}
	}
}

func (s *HNSWSnapshot) checksumMagnitudes(h hash.Hash32) {
	ids := sortedMagnitudeKeys(s.Magnitudes)
	for _, id := range ids {
		writeString(h, id)
		writeFloat64(h, s.Magnitudes[id])
	}
}

func (s *HNSWSnapshot) checksumLayers(h hash.Hash32) {
	writeInt(h, len(s.Layers))
	for i, layer := range s.Layers {
		writeInt(h, i)
		checksumLayerSnapshot(h, &layer)
	}
}

func checksumLayerSnapshot(h hash.Hash32, layer *LayerSnapshot) {
	ids := sortedLayerKeys(layer.Nodes)
	for _, id := range ids {
		writeString(h, id)
		neighbors := layer.Nodes[id]
		writeInt(h, len(neighbors))
		for _, neighbor := range neighbors {
			writeString(h, neighbor)
		}
	}
}

func (s *HNSWSnapshot) checksumDomains(h hash.Hash32) {
	ids := sortedDomainKeys(s.Domains)
	for _, id := range ids {
		writeString(h, id)
		writeInt(h, int(s.Domains[id]))
	}
}

func (s *HNSWSnapshot) checksumNodeTypes(h hash.Hash32) {
	ids := sortedNodeTypeKeys(s.NodeTypes)
	for _, id := range ids {
		writeString(h, id)
		writeInt(h, int(s.NodeTypes[id]))
	}
}

func (s *HNSWSnapshot) ValidateChecksum() error {
	computed := s.computeChecksum()
	if computed != s.Checksum {
		return ErrSnapshotChecksumMismatch
	}
	s.validated = true
	return nil
}

func (s *HNSWSnapshot) IsValidated() bool {
	return s.validated
}

func (s *HNSWSnapshot) GetChecksum() uint32 {
	return s.Checksum
}

func writeUint64(h hash.Hash32, v uint64) {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], v)
	h.Write(buf[:])
}

func writeInt(h hash.Hash32, v int) {
	writeUint64(h, uint64(v))
}

func writeFloat32(h hash.Hash32, v float32) {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], floatBits(v))
	h.Write(buf[:])
}

func writeFloat64(h hash.Hash32, v float64) {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], doubleBits(v))
	h.Write(buf[:])
}

func writeString(h hash.Hash32, s string) {
	writeInt(h, len(s))
	h.Write([]byte(s))
}

func floatBits(f float32) uint32 {
	return math.Float32bits(f)
}

func doubleBits(f float64) uint64 {
	return math.Float64bits(f)
}

func sortedKeys(m map[string][]float32) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedMagnitudeKeys(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedLayerKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedDomainKeys(m map[string]vectorgraphdb.Domain) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedNodeTypeKeys(m map[string]vectorgraphdb.NodeType) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
