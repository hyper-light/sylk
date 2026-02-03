// Package sylkdir provides version-aware data stores for global knowledge graph isolation.
package sylkdir

import (
	"encoding/binary"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
)

// GlobalVersionNodeStore writes nodes to the global shared data file with
// per-version offset indexes for O(1) lookups and zero-copy version creation.
//
// Concurrency model: single-writer-multiple-reader.
// Reads (ReadFromVersion, ReadAllFromVersion) are lock-free using sync.Map
// for index lookup and lock-free OffsetIndex operations.
// Writes (WriteToVersion, WriteBatchToVersion) are serialized via writeMu.
type GlobalVersionNodeStore struct {
	sylkDir    *SylkDir
	head       atomic.Pointer[SemanticVersion]
	dataFile   *SharedDataFile
	indexes    sync.Map // string -> *OffsetIndex
	tombstones sync.Map // string -> *TombstoneBitmap
	writeMu    sync.Mutex
}

// NewGlobalVersionNodeStore creates a node store for the global KG.
func NewGlobalVersionNodeStore(sd *SylkDir, head SemanticVersion) (*GlobalVersionNodeStore, error) {
	df, err := OpenSharedDataFile(sd.GlobalNodeDataPath())
	if err != nil {
		return nil, fmt.Errorf("open global node data: %w", err)
	}
	s := &GlobalVersionNodeStore{
		sylkDir:  sd,
		dataFile: df,
	}
	s.head.Store(&head)
	return s, nil
}

// Close closes the shared data file.
func (s *GlobalVersionNodeStore) Close() error {
	if s.dataFile == nil {
		return nil
	}
	return s.dataFile.Close()
}

// SetHead updates the HEAD version. Lock-free via atomic pointer swap.
func (s *GlobalVersionNodeStore) SetHead(head SemanticVersion) {
	s.head.Store(&head)
}

// Write writes a node to the current HEAD version.
func (s *GlobalVersionNodeStore) Write(node *Node) error {
	return s.WriteToVersion(*s.head.Load(), node)
}

// WriteToVersion writes a node to a specific version.
func (s *GlobalVersionNodeStore) WriteToVersion(version SemanticVersion, node *Node) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	record, err := marshalNodeRecord(node)
	if err != nil {
		return err
	}

	offset, err := s.dataFile.Append(record)
	if err != nil {
		return fmt.Errorf("append global node: %w", err)
	}

	s.nodeIndex(version).Set(node.ID, offset)
	return nil
}

// WriteBatch writes multiple nodes to the current HEAD version.
func (s *GlobalVersionNodeStore) WriteBatch(nodes []*Node) error {
	return s.WriteBatchToVersion(*s.head.Load(), nodes)
}

// WriteBatchToVersion writes multiple nodes to a specific version.
// Uses 3-phase batch: pre-marshal all → single AppendBatch → single SetBatch.
func (s *GlobalVersionNodeStore) WriteBatchToVersion(version SemanticVersion, nodes []*Node) error {
	if len(nodes) == 0 {
		return nil
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	records := make([][]byte, len(nodes))
	ids := make([]uint32, len(nodes))
	for i, node := range nodes {
		rec, err := marshalNodeRecord(node)
		if err != nil {
			return err
		}
		records[i] = rec
		ids[i] = node.ID
	}

	offsets, err := s.dataFile.AppendBatch(records)
	if err != nil {
		return fmt.Errorf("append global nodes: %w", err)
	}

	s.nodeIndex(version).SetBatch(ids, offsets)
	return nil
}

// ReadFromVersion reads a node from a specific version.
func (s *GlobalVersionNodeStore) ReadFromVersion(version SemanticVersion, nodeID uint32) (*Node, error) {
	idx := s.loadIndex(version)
	if idx == nil {
		return nil, ErrNodeNotFound
	}

	offset, ok := idx.Get(nodeID)
	if !ok {
		return nil, ErrNodeNotFound
	}

	return readNodeAtOffset(s.dataFile, offset)
}

// ReadAll reads all nodes from HEAD version.
func (s *GlobalVersionNodeStore) ReadAll() ([]*Node, error) {
	return s.ReadAllFromVersion(*s.head.Load())
}

// ReadAllFromVersion reads all live nodes from a specific version.
// Dead nodes (tracked by the version's tombstone bitmap) are excluded.
func (s *GlobalVersionNodeStore) ReadAllFromVersion(version SemanticVersion) ([]*Node, error) {
	tb, err := loadCachedTombstone(&s.tombstones, s.sylkDir, version)
	if err != nil {
		return nil, err
	}

	idx := s.loadIndex(version)
	if idx == nil {
		return []*Node{}, nil
	}

	nodes := make([]*Node, 0, idx.Count())
	var readErr error
	idx.ForEach(func(id uint32, offset int64) bool {
		if tb.IsDead(id) {
			return true
		}
		node, err := readNodeAtOffset(s.dataFile, offset)
		if err != nil {
			readErr = err
			return false
		}
		nodes = append(nodes, node)
		return true
	})

	return nodes, readErr
}

// CountForVersion returns the number of entries in the offset index for a version.
// This is the total node count including entries for dead nodes not yet compacted.
// Lock-free.
func (s *GlobalVersionNodeStore) CountForVersion(version SemanticVersion) uint32 {
	idx := s.loadIndex(version)
	if idx == nil {
		return 0
	}
	return idx.Count()
}

// SaveIndexes saves all in-memory offset indexes to disk.
func (s *GlobalVersionNodeStore) SaveIndexes() error {
	var saveErr error
	s.indexes.Range(func(_, val any) bool {
		idx, ok := val.(*OffsetIndex)
		if !ok {
			return true
		}
		if err := idx.Save(); err != nil {
			saveErr = err
			return false
		}
		return true
	})
	return saveErr
}

// nodeIndexPath returns the path to the node offset index for a version.
func (s *GlobalVersionNodeStore) nodeIndexPath(version SemanticVersion) string {
	return filepath.Join(s.sylkDir.GlobalVersionPath(version), "nodes", "index.bin")
}

// nodeIndex returns the OffsetIndex for a version, creating if needed.
// Called under writeMu; uses sync.Map for storage.
func (s *GlobalVersionNodeStore) nodeIndex(version SemanticVersion) *OffsetIndex {
	vKey := version.String()
	if val, ok := s.indexes.Load(vKey); ok {
		return val.(*OffsetIndex)
	}
	path := s.nodeIndexPath(version)
	idx, err := LoadOffsetIndex(path)
	if err != nil {
		idx = NewOffsetIndex(path, offsetIndexMinCapacity)
	}
	s.indexes.Store(vKey, idx)
	return idx
}

// loadIndex returns the OffsetIndex for a version (read path). Lock-free.
// Caches loaded indexes via sync.Map.LoadOrStore.
func (s *GlobalVersionNodeStore) loadIndex(version SemanticVersion) *OffsetIndex {
	vKey := version.String()
	if val, ok := s.indexes.Load(vKey); ok {
		return val.(*OffsetIndex)
	}
	path := s.nodeIndexPath(version)
	idx, err := LoadOffsetIndex(path)
	if err != nil {
		return nil
	}
	actual, _ := s.indexes.LoadOrStore(vKey, idx)
	return actual.(*OffsetIndex)
}

// GlobalVersionEdgeStore writes edges to a shared EdgeShardStore with
// per-version tombstone filtering. The EdgeShardStore provides O(1) lookups
// and deduplication via edgeKeyIndex.
//
// Concurrency model: single-writer-multiple-reader via EdgeShardStore.
type GlobalVersionEdgeStore struct {
	sylkDir    *SylkDir
	head       SemanticVersion
	store      *EdgeShardStore
	tombstones sync.Map // string -> *TombstoneBitmap
}

// NewGlobalVersionEdgeStore creates an edge store backed by a shared EdgeShardStore.
func NewGlobalVersionEdgeStore(sd *SylkDir, head SemanticVersion) (*GlobalVersionEdgeStore, error) {
	store := NewEdgeShardStore(sd.GlobalEdgeDataPath())
	if err := store.Init(); err != nil {
		return nil, fmt.Errorf("init global edge shard store: %w", err)
	}
	return &GlobalVersionEdgeStore{
		sylkDir: sd,
		head:    head,
		store:   store,
	}, nil
}

// Close saves edge indexes and releases resources.
func (s *GlobalVersionEdgeStore) Close() error {
	if s.store == nil {
		return nil
	}
	return s.store.Close()
}

// SetHead updates the HEAD version.
func (s *GlobalVersionEdgeStore) SetHead(head SemanticVersion) {
	s.head = head
}

// Write writes an edge to the shared store.
func (s *GlobalVersionEdgeStore) Write(edge *Edge) error {
	return s.store.Write(edge)
}

// WriteBatch writes multiple edges to the shared store.
func (s *GlobalVersionEdgeStore) WriteBatch(edges []*Edge) error {
	return s.store.WriteBatch(edges)
}

// GetOutgoingFromVersion gets all live outgoing edges from a node in a version.
// Edges referencing dead nodes are excluded via tombstone.
func (s *GlobalVersionEdgeStore) GetOutgoingFromVersion(version SemanticVersion, nodeID uint32) ([]*Edge, error) {
	tb, err := loadCachedTombstone(&s.tombstones, s.sylkDir, version)
	if err != nil {
		return nil, err
	}

	edges, err := s.store.GetOutgoing(nodeID)
	if err != nil {
		return nil, err
	}

	live := make([]*Edge, 0, len(edges))
	for _, edge := range edges {
		if tb.IsEdgeAlive(edge.SourceID, edge.TargetID) {
			live = append(live, edge)
		}
	}
	return live, nil
}

// ReadAll reads all live edges from HEAD version.
func (s *GlobalVersionEdgeStore) ReadAll() ([]*Edge, error) {
	return s.ReadAllFromVersion(s.head)
}

// ReadAllFromVersion reads all live edges from a specific version.
// Edges referencing dead nodes (either endpoint) are excluded.
func (s *GlobalVersionEdgeStore) ReadAllFromVersion(version SemanticVersion) ([]*Edge, error) {
	tb, err := loadCachedTombstone(&s.tombstones, s.sylkDir, version)
	if err != nil {
		return nil, err
	}

	return s.store.ReadAllFiltered(func(sourceID, targetID uint32) bool {
		return tb.IsEdgeAlive(sourceID, targetID)
	})
}

// Store returns the underlying EdgeShardStore for direct access.
func (s *GlobalVersionEdgeStore) Store() *EdgeShardStore {
	return s.store
}

// GlobalVersionDocStore writes documents to the global shared doc data file with
// per-version offset indexes for O(1) lookups and zero-copy version creation.
//
// Concurrency model: single-writer-multiple-reader.
// Reads are lock-free using sync.Map and lock-free OffsetIndex operations.
// Writes are serialized via writeMu.
type GlobalVersionDocStore struct {
	sylkDir    *SylkDir
	head       atomic.Pointer[SemanticVersion]
	dataFile   *SharedDataFile
	docIDMap   *DocIDMap
	indexes    sync.Map // string -> *OffsetIndex
	tombstones sync.Map // string -> *TombstoneBitmap
	writeMu    sync.Mutex
}

// NewGlobalVersionDocStore creates a document store for the global KG.
func NewGlobalVersionDocStore(sd *SylkDir, head SemanticVersion) (*GlobalVersionDocStore, error) {
	df, err := OpenSharedDataFile(sd.GlobalDocDataPath())
	if err != nil {
		return nil, fmt.Errorf("open global doc data: %w", err)
	}
	docIDMap, loadErr := LoadDocIDMap(sd.GlobalDocIDMapPath())
	if loadErr != nil {
		docIDMap = NewDocIDMap(sd.GlobalDocIDMapPath())
	}
	s := &GlobalVersionDocStore{
		sylkDir:  sd,
		dataFile: df,
		docIDMap: docIDMap,
	}
	s.head.Store(&head)
	return s, nil
}

// Close persists offset indexes, saves the DocIDMap, and closes the shared data file.
func (s *GlobalVersionDocStore) Close() error {
	if err := s.SaveIndexes(); err != nil {
		return fmt.Errorf("save global doc indexes on close: %w", err)
	}
	if s.docIDMap != nil {
		if err := s.docIDMap.Save(); err != nil {
			if s.dataFile != nil {
				s.dataFile.Close()
			}
			return fmt.Errorf("save global doc id map: %w", err)
		}
	}
	if s.dataFile == nil {
		return nil
	}
	return s.dataFile.Close()
}

// DocIDMap returns the underlying DocIDMap for external callers (e.g., commit).
func (s *GlobalVersionDocStore) DocIDMap() *DocIDMap {
	return s.docIDMap
}

// SetHead updates the HEAD version. Lock-free via atomic pointer swap.
func (s *GlobalVersionDocStore) SetHead(head SemanticVersion) {
	s.head.Store(&head)
}

// Write writes a document to the current HEAD version.
func (s *GlobalVersionDocStore) Write(doc *VersionDocument) error {
	return s.WriteToVersion(*s.head.Load(), doc)
}

// WriteToVersion writes a document to a specific version.
func (s *GlobalVersionDocStore) WriteToVersion(version SemanticVersion, doc *VersionDocument) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	record, err := marshalDocRecord(doc)
	if err != nil {
		return err
	}

	offset, err := s.dataFile.Append(record)
	if err != nil {
		return fmt.Errorf("append global doc: %w", err)
	}

	docID := s.docIDMap.GetOrAssign(doc.ID)
	s.docIndex(version).Set(docID, offset)
	return nil
}

// WriteBatch writes multiple documents to the current HEAD version.
func (s *GlobalVersionDocStore) WriteBatch(docs []*VersionDocument) error {
	return s.WriteBatchToVersion(*s.head.Load(), docs)
}

// WriteBatchToVersion writes multiple documents to a specific version.
// Uses 3-phase batch: pre-marshal all → single AppendBatch → single SetBatch.
func (s *GlobalVersionDocStore) WriteBatchToVersion(version SemanticVersion, docs []*VersionDocument) error {
	if len(docs) == 0 {
		return nil
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	records := make([][]byte, len(docs))
	ids := make([]uint32, len(docs))
	for i, doc := range docs {
		rec, err := marshalDocRecord(doc)
		if err != nil {
			return err
		}
		records[i] = rec
		ids[i] = s.docIDMap.GetOrAssign(doc.ID)
	}

	offsets, err := s.dataFile.AppendBatch(records)
	if err != nil {
		return fmt.Errorf("append global docs: %w", err)
	}

	s.docIndex(version).SetBatch(ids, offsets)
	return nil
}

// ReadAll reads all documents from HEAD version.
func (s *GlobalVersionDocStore) ReadAll() ([]*VersionDocument, error) {
	return s.ReadAllFromVersion(*s.head.Load())
}

// ReadAllFromVersion reads all live documents from a specific version.
// Documents not referenced by any live node (via DocRef) are excluded.
func (s *GlobalVersionDocStore) ReadAllFromVersion(version SemanticVersion) ([]*VersionDocument, error) {
	tb, err := loadCachedTombstone(&s.tombstones, s.sylkDir, version)
	if err != nil {
		return nil, err
	}

	idx := s.loadDocIndex(version)
	if idx == nil {
		return []*VersionDocument{}, nil
	}

	// Build set of DocRef values from live nodes. If any live node lacks
	// a DocRef (pre-migration data), skip filtering entirely.
	liveDocRefs, allResolved, err := collectGlobalLiveDocRefs(s.sylkDir, version, tb)
	if err != nil {
		return nil, fmt.Errorf("collect live doc refs: %w", err)
	}

	docs := make([]*VersionDocument, 0, idx.Count())
	var readErr error
	idx.ForEach(func(docID uint32, offset int64) bool {
		// Only filter when all live nodes have DocRef set.
		if allResolved && !liveDocRefs[docID] {
			return true
		}
		doc, err := readDocAtOffset(s.dataFile, offset)
		if err != nil {
			readErr = err
			return false
		}
		docs = append(docs, doc)
		return true
	})

	return docs, readErr
}

// SaveIndexes saves all in-memory offset indexes to disk.
func (s *GlobalVersionDocStore) SaveIndexes() error {
	var saveErr error
	s.indexes.Range(func(_, val any) bool {
		idx, ok := val.(*OffsetIndex)
		if !ok {
			return true
		}
		if err := idx.Save(); err != nil {
			saveErr = err
			return false
		}
		return true
	})
	return saveErr
}

// docIndexPath returns the path to the doc offset index for a version.
func (s *GlobalVersionDocStore) docIndexPath(version SemanticVersion) string {
	return filepath.Join(s.sylkDir.GlobalVersionPath(version), "docs", "index.bin")
}

// docIndex returns the OffsetIndex for a version, creating if needed.
// Called under writeMu; uses sync.Map for storage.
func (s *GlobalVersionDocStore) docIndex(version SemanticVersion) *OffsetIndex {
	vKey := version.String()
	if val, ok := s.indexes.Load(vKey); ok {
		return val.(*OffsetIndex)
	}
	path := s.docIndexPath(version)
	idx, err := LoadOffsetIndex(path)
	if err != nil {
		idx = NewOffsetIndex(path, offsetIndexMinCapacity)
	}
	s.indexes.Store(vKey, idx)
	return idx
}

// loadDocIndex returns the OffsetIndex for a version (read path). Lock-free.
// Caches loaded indexes via sync.Map.LoadOrStore.
func (s *GlobalVersionDocStore) loadDocIndex(version SemanticVersion) *OffsetIndex {
	vKey := version.String()
	if val, ok := s.indexes.Load(vKey); ok {
		return val.(*OffsetIndex)
	}
	path := s.docIndexPath(version)
	idx, err := LoadOffsetIndex(path)
	if err != nil {
		return nil
	}
	actual, _ := s.indexes.LoadOrStore(vKey, idx)
	return actual.(*OffsetIndex)
}

// GlobalVersionVectorStore writes vectors to the global shared data file with
// per-version offset indexes for O(1) lookups and zero-copy version creation.
//
// Concurrency model: single-writer-multiple-reader.
// Reads are lock-free using sync.Map and lock-free OffsetIndex operations.
// Writes are serialized via writeMu.
type GlobalVersionVectorStore struct {
	sylkDir    *SylkDir
	head       atomic.Pointer[SemanticVersion]
	dataFile   *SharedDataFile
	indexes    sync.Map // string -> *OffsetIndex
	tombstones sync.Map // string -> *TombstoneBitmap
	writeMu    sync.Mutex
}

// NewGlobalVersionVectorStore creates a vector store for the global KG.
func NewGlobalVersionVectorStore(sd *SylkDir, head SemanticVersion) (*GlobalVersionVectorStore, error) {
	df, err := OpenSharedDataFile(sd.GlobalVectorDataPath())
	if err != nil {
		return nil, fmt.Errorf("open global vector data: %w", err)
	}
	s := &GlobalVersionVectorStore{
		sylkDir:  sd,
		dataFile: df,
	}
	s.head.Store(&head)
	return s, nil
}

// Close closes the shared data file.
func (s *GlobalVersionVectorStore) Close() error {
	if s.dataFile == nil {
		return nil
	}
	return s.dataFile.Close()
}

// SetHead updates the HEAD version. Lock-free via atomic pointer swap.
func (s *GlobalVersionVectorStore) SetHead(head SemanticVersion) {
	s.head.Store(&head)
}

// Write writes a vector to the current HEAD version.
func (s *GlobalVersionVectorStore) Write(vec *VersionVector) error {
	return s.WriteToVersion(*s.head.Load(), vec)
}

// WriteToVersion writes a vector to a specific version.
func (s *GlobalVersionVectorStore) WriteToVersion(version SemanticVersion, vec *VersionVector) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	record := marshalVectorRecord(vec)
	offset, err := s.dataFile.Append(record)
	if err != nil {
		return fmt.Errorf("append global vector: %w", err)
	}

	s.vectorIndex(version).Set(vec.NodeID, offset)
	return nil
}

// WriteBatch writes multiple vectors to the current HEAD version.
func (s *GlobalVersionVectorStore) WriteBatch(vecs []*VersionVector) error {
	return s.WriteBatchToVersion(*s.head.Load(), vecs)
}

// WriteBatchToVersion writes multiple vectors to a specific version.
// Pre-marshals all vectors into a single contiguous buffer using direct memory
// copy (marshalVectorRecordTo), then writes via AppendRaw — single allocation,
// single I/O, single index update.
func (s *GlobalVersionVectorStore) WriteBatchToVersion(version SemanticVersion, vecs []*VersionVector) error {
	if len(vecs) == 0 {
		return nil
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	dim := len(vecs[0].Vector)
	recSize := vectorRecordByteSize(dim)

	buf := make([]byte, len(vecs)*recSize)
	ids := make([]uint32, len(vecs))
	for i, vec := range vecs {
		marshalVectorRecordTo(buf[i*recSize:(i+1)*recSize], vec)
		ids[i] = vec.NodeID
	}

	baseOffset, err := s.dataFile.AppendRaw(buf)
	if err != nil {
		return fmt.Errorf("append global vectors: %w", err)
	}

	offsets := make([]int64, len(vecs))
	for i := range vecs {
		offsets[i] = baseOffset + int64(i*recSize)
	}

	s.vectorIndex(version).SetBatch(ids, offsets)
	return nil
}

// ReadAll reads all vectors from HEAD version.
func (s *GlobalVersionVectorStore) ReadAll() ([]*VersionVector, error) {
	return s.ReadAllFromVersion(*s.head.Load())
}

// ReadAllFromVersion reads all live vectors from a specific version.
// Vectors belonging to dead nodes are skipped via the tombstone bitmap.
func (s *GlobalVersionVectorStore) ReadAllFromVersion(version SemanticVersion) ([]*VersionVector, error) {
	tb, err := loadCachedTombstone(&s.tombstones, s.sylkDir, version)
	if err != nil {
		return nil, err
	}

	idx := s.loadVectorIndex(version)
	if idx == nil {
		return []*VersionVector{}, nil
	}

	vectors := make([]*VersionVector, 0, idx.Count())
	var readErr error
	idx.ForEach(func(id uint32, offset int64) bool {
		if tb.IsDead(id) {
			return true
		}
		vec, err := readVectorAtOffset(s.dataFile, offset)
		if err != nil {
			readErr = err
			return false
		}
		vectors = append(vectors, vec)
		return true
	})

	return vectors, readErr
}

// CountForVersion returns the number of entries in the offset index for a version.
// Lock-free.
func (s *GlobalVersionVectorStore) CountForVersion(version SemanticVersion) uint32 {
	idx := s.loadVectorIndex(version)
	if idx == nil {
		return 0
	}
	return idx.Count()
}

// SaveIndexes saves all in-memory offset indexes to disk.
func (s *GlobalVersionVectorStore) SaveIndexes() error {
	var saveErr error
	s.indexes.Range(func(_, val any) bool {
		idx, ok := val.(*OffsetIndex)
		if !ok {
			return true
		}
		if err := idx.Save(); err != nil {
			saveErr = err
			return false
		}
		return true
	})
	return saveErr
}

// vectorIndexPath returns the path to the vector offset index for a version.
func (s *GlobalVersionVectorStore) vectorIndexPath(version SemanticVersion) string {
	return filepath.Join(s.sylkDir.GlobalVersionPath(version), "vectors", "index.bin")
}

// vectorIndex returns the OffsetIndex for a version, creating if needed.
// Called under writeMu; uses sync.Map for storage.
func (s *GlobalVersionVectorStore) vectorIndex(version SemanticVersion) *OffsetIndex {
	vKey := version.String()
	if val, ok := s.indexes.Load(vKey); ok {
		return val.(*OffsetIndex)
	}
	path := s.vectorIndexPath(version)
	idx, err := LoadOffsetIndex(path)
	if err != nil {
		idx = NewOffsetIndex(path, offsetIndexMinCapacity)
	}
	s.indexes.Store(vKey, idx)
	return idx
}

// loadVectorIndex returns the OffsetIndex for a version (read path). Lock-free.
// Caches loaded indexes via sync.Map.LoadOrStore.
func (s *GlobalVersionVectorStore) loadVectorIndex(version SemanticVersion) *OffsetIndex {
	vKey := version.String()
	if val, ok := s.indexes.Load(vKey); ok {
		return val.(*OffsetIndex)
	}
	path := s.vectorIndexPath(version)
	idx, err := LoadOffsetIndex(path)
	if err != nil {
		return nil
	}
	actual, _ := s.indexes.LoadOrStore(vKey, idx)
	return actual.(*OffsetIndex)
}

// loadCachedTombstone loads a tombstone bitmap with per-version caching.
// Tombstones are immutable after their version is committed, so caching is safe.
// Uses sync.Map.LoadOrStore for lock-free concurrent access.
func loadCachedTombstone(cache *sync.Map, sd *SylkDir, version SemanticVersion) (*TombstoneBitmap, error) {
	vKey := version.String()
	if val, ok := cache.Load(vKey); ok {
		return val.(*TombstoneBitmap), nil
	}
	tb, err := LoadTombstoneBitmap(sd.GlobalVersionPath(version))
	if err != nil {
		return nil, fmt.Errorf("load tombstones: %w", err)
	}
	actual, _ := cache.LoadOrStore(vKey, tb)
	return actual.(*TombstoneBitmap), nil
}

// nodeDocRefRecordOffset is the byte position of the DocRef field within a
// node record stored in a SharedDataFile. Records are [size:4][header:32][...],
// and DocRef occupies header bytes [20:24], so the absolute file offset is 24.
const nodeDocRefRecordOffset = 24

// collectGlobalLiveDocRefs builds a set of DocRef values from live global nodes.
// Returns the set and whether ALL live nodes have a resolved DocRef (non-zero).
// When allResolved is false, callers should skip DocRef-based filtering to
// preserve backward compatibility with nodes created before DocRef was added.
func collectGlobalLiveDocRefs(sd *SylkDir, version SemanticVersion, tb *TombstoneBitmap) (refs map[uint32]bool, allResolved bool, err error) {
	nodeIdxPath := filepath.Join(sd.GlobalVersionPath(version), "nodes", "index.bin")
	nodeIdx, loadErr := LoadOffsetIndex(nodeIdxPath)
	if loadErr != nil {
		// No node index → no nodes → no filtering needed, include all docs.
		return map[uint32]bool{}, false, nil
	}

	nodeDF, openErr := OpenSharedDataFile(sd.GlobalNodeDataPath())
	if openErr != nil {
		return nil, false, fmt.Errorf("open node data for doc ref scan: %w", openErr)
	}
	defer nodeDF.Close()

	refs = make(map[uint32]bool)
	allResolved = true
	liveCount := 0
	var buf [4]byte

	nodeIdx.ForEach(func(nodeID uint32, offset int64) bool {
		if tb.IsDead(nodeID) {
			return true
		}
		liveCount++
		if _, readErr := nodeDF.ReadAt(buf[:], offset+nodeDocRefRecordOffset); readErr != nil {
			return true
		}
		docRef := binary.LittleEndian.Uint32(buf[:])
		if docRef == 0 {
			allResolved = false
		} else {
			refs[docRef] = true
		}
		return true
	})

	// No live nodes → nothing to filter against, include all docs.
	if liveCount == 0 {
		return map[uint32]bool{}, false, nil
	}

	return refs, allResolved, nil
}
