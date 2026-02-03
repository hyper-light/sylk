// Package sylkdir provides version-aware data stores for session isolation.
package sylkdir

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
)

// VersionNodeStore writes nodes to the session's shared data file with
// per-version offset indexes for O(1) lookups and zero-copy checkpoints.
//
// Concurrency model: single-writer-multiple-reader.
// Reads (ReadFromVersion, ReadAllFromVersion, Count) are lock-free
// using sync.Map for index lookup and lock-free OffsetIndex operations.
// Writes (WriteToVersion, WriteBatchToVersion) are serialized via writeMu.
type VersionNodeStore struct {
	session *Session
	indexes sync.Map   // string -> *OffsetIndex
	writeMu sync.Mutex
}

// NewVersionNodeStore creates a node store for the given session.
func NewVersionNodeStore(sess *Session) *VersionNodeStore {
	s := &VersionNodeStore{session: sess}
	sess.RegisterNodeStore(s)
	return s
}

// Write writes a node to the current HEAD version.
func (s *VersionNodeStore) Write(node *Node) error {
	return s.WriteToVersion(s.session.Manifest.Head, node)
}

// WriteToVersion writes a node to a specific version.
func (s *VersionNodeStore) WriteToVersion(version SemanticVersion, node *Node) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	// WAL log before data write for crash safety.
	if s.session.WAL != nil {
		if _, err := s.session.WAL.LogNodeInsert(nodeToWALData(node)); err != nil {
			return fmt.Errorf("wal log node: %w", err)
		}
	}

	record, err := marshalNodeRecord(node)
	if err != nil {
		return err
	}

	offset, err := s.session.NodeDataFile.Append(record)
	if err != nil {
		return fmt.Errorf("append node: %w", err)
	}

	s.nodeIndex(version).Set(node.ID, offset)
	return nil
}

// WriteBatch writes multiple nodes to the current HEAD version.
func (s *VersionNodeStore) WriteBatch(nodes []*Node) error {
	return s.WriteBatchToVersion(s.session.Manifest.Head, nodes)
}

// WriteBatchToVersion writes multiple nodes to a specific version.
// WAL entries are batched with a single fsync for the entire batch.
func (s *VersionNodeStore) WriteBatchToVersion(version SemanticVersion, nodes []*Node) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if err := s.walLogNodeBatch(nodes); err != nil {
		return err
	}
	return s.appendNodeBatch(version, nodes)
}

// walLogNodeBatch writes all node WAL entries with a single sync.
func (s *VersionNodeStore) walLogNodeBatch(nodes []*Node) error {
	if s.session.WAL == nil {
		return nil
	}
	walData := make([]*WALNodeData, len(nodes))
	for i, node := range nodes {
		walData[i] = nodeToWALData(node)
	}
	return s.session.WAL.LogNodeBatch(walData)
}

// appendNodeBatch appends all nodes to the data file and updates the index.
func (s *VersionNodeStore) appendNodeBatch(version SemanticVersion, nodes []*Node) error {
	idx := s.nodeIndex(version)
	for _, node := range nodes {
		if err := s.appendSingleNode(idx, node); err != nil {
			return err
		}
	}
	return nil
}

// appendSingleNode marshals and appends one node to the data file.
func (s *VersionNodeStore) appendSingleNode(idx *OffsetIndex, node *Node) error {
	record, err := marshalNodeRecord(node)
	if err != nil {
		return err
	}
	offset, err := s.session.NodeDataFile.Append(record)
	if err != nil {
		return fmt.Errorf("append node %d: %w", node.ID, err)
	}
	idx.Set(node.ID, offset)
	return nil
}

// ReadFromVersion reads a node from a specific version.
func (s *VersionNodeStore) ReadFromVersion(version SemanticVersion, nodeID uint32) (*Node, error) {
	idx := s.loadIndex(version)
	if idx == nil {
		return nil, ErrNodeNotFound
	}

	offset, ok := idx.Get(nodeID)
	if !ok {
		return nil, ErrNodeNotFound
	}

	return readNodeAtOffset(s.session.NodeDataFile, offset)
}

// ReadFromAncestorChain reads a node from HEAD version.
// Since versions are cumulative snapshots, HEAD contains all ancestor data.
func (s *VersionNodeStore) ReadFromAncestorChain(nodeID uint32) (*Node, error) {
	return s.ReadFromVersion(s.session.Manifest.Head, nodeID)
}

// ReadAllFromVersion reads all nodes from a specific version.
// No dedup needed — the offset index guarantees one entry per ID.
func (s *VersionNodeStore) ReadAllFromVersion(version SemanticVersion) ([]*Node, error) {
	idx := s.loadIndex(version)
	if idx == nil {
		return []*Node{}, nil
	}

	nodes := make([]*Node, 0, idx.Count())
	var readErr error
	idx.ForEach(func(_ uint32, offset int64) bool {
		node, err := readNodeAtOffset(s.session.NodeDataFile, offset)
		if err != nil {
			readErr = err
			return false
		}
		nodes = append(nodes, node)
		return true
	})

	return nodes, readErr
}

// ReadAllFromAncestorChain reads all nodes from HEAD version.
func (s *VersionNodeStore) ReadAllFromAncestorChain() ([]*Node, error) {
	return s.ReadAllFromVersion(s.session.Manifest.Head)
}

// Count returns the number of nodes in HEAD version.
func (s *VersionNodeStore) Count() int {
	idx := s.loadIndex(s.session.Manifest.Head)
	if idx == nil {
		return 0
	}
	return int(idx.Count())
}

// SaveIndexes saves all in-memory offset indexes to disk.
func (s *VersionNodeStore) SaveIndexes() error {
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

// RegisterIndex stores an OffsetIndex in the in-memory map for a version.
// Used during checkpoint to register the cloned index.
func (s *VersionNodeStore) RegisterIndex(version SemanticVersion, idx *OffsetIndex) {
	s.indexes.Store(version.String(), idx)
}

// getInMemoryIndex returns the in-memory OffsetIndex for a version, or nil.
func (s *VersionNodeStore) getInMemoryIndex(version SemanticVersion) *OffsetIndex {
	if val, ok := s.indexes.Load(version.String()); ok {
		return val.(*OffsetIndex)
	}
	return nil
}

// nodeIndex returns the OffsetIndex for a version, creating if needed.
// Called under writeMu; uses sync.Map for storage.
func (s *VersionNodeStore) nodeIndex(version SemanticVersion) *OffsetIndex {
	vKey := version.String()
	if val, ok := s.indexes.Load(vKey); ok {
		return val.(*OffsetIndex)
	}
	path := s.session.NodeIndexPath(version)
	idx, err := LoadOffsetIndex(path)
	if err != nil {
		idx = NewOffsetIndex(path, offsetIndexMinCapacity)
	}
	s.indexes.Store(vKey, idx)
	return idx
}

// loadIndex returns the OffsetIndex for a version (read path). Lock-free.
// Caches loaded indexes via sync.Map.LoadOrStore.
func (s *VersionNodeStore) loadIndex(version SemanticVersion) *OffsetIndex {
	vKey := version.String()
	if val, ok := s.indexes.Load(vKey); ok {
		return val.(*OffsetIndex)
	}
	path := s.session.NodeIndexPath(version)
	idx, err := LoadOffsetIndex(path)
	if err != nil {
		return nil
	}
	actual, _ := s.indexes.LoadOrStore(vKey, idx)
	return actual.(*OffsetIndex)
}

// marshalNodeRecord serializes a node as [size:4][binary_data].
func marshalNodeRecord(node *Node) ([]byte, error) {
	data, err := node.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("marshal node: %w", err)
	}
	record := make([]byte, 4+len(data))
	binary.LittleEndian.PutUint32(record[0:4], uint32(len(data)))
	copy(record[4:], data)
	return record, nil
}

// readNodeAtOffset reads a node from a shared data file at the given offset.
func readNodeAtOffset(sdf *SharedDataFile, offset int64) (*Node, error) {
	// Read size prefix.
	sizeBuf := make([]byte, 4)
	if _, err := sdf.ReadAt(sizeBuf, offset); err != nil {
		return nil, err
	}
	size := binary.LittleEndian.Uint32(sizeBuf)

	// Read node data.
	data := make([]byte, size)
	if _, err := sdf.ReadAt(data, offset+4); err != nil {
		return nil, err
	}

	node := &Node{}
	if err := node.UnmarshalBinary(data); err != nil {
		return nil, err
	}
	return node, nil
}

// VersionEdgeStore writes edges to session version directories.
type VersionEdgeStore struct {
	session *Session
	mu      sync.RWMutex

	// Per-version outgoing index: version string -> sourceID -> []offset
	outgoing map[string]map[uint32][]int64
	// Per-version incoming index: version string -> targetID -> []offset
	incoming map[string]map[uint32][]int64
}

// NewVersionEdgeStore creates an edge store for the given session.
func NewVersionEdgeStore(sess *Session) *VersionEdgeStore {
	return &VersionEdgeStore{
		session:  sess,
		outgoing: make(map[string]map[uint32][]int64),
		incoming: make(map[string]map[uint32][]int64),
	}
}

// Write writes an edge to the current HEAD version.
func (s *VersionEdgeStore) Write(edge *Edge) error {
	return s.WriteToVersion(s.session.Manifest.Head, edge)
}

// WriteToVersion writes an edge to a specific version.
func (s *VersionEdgeStore) WriteToVersion(version SemanticVersion, edge *Edge) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// WAL log before .bin write for crash safety.
	if s.session.WAL != nil {
		if _, err := s.session.WAL.LogEdgeInsert(edgeToWALData(edge)); err != nil {
			return fmt.Errorf("wal log edge: %w", err)
		}
	}

	dataPath := filepath.Join(s.session.VersionPath(version), "edges", "data.bin")

	if err := os.MkdirAll(filepath.Dir(dataPath), 0755); err != nil {
		return fmt.Errorf("create edges dir: %w", err)
	}

	f, err := os.OpenFile(dataPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open edges file: %w", err)
	}
	defer f.Close()

	offset, _ := f.Seek(0, io.SeekEnd)

	data := edge.MarshalBinary()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write edge: %w", err)
	}

	// Update indexes
	vKey := version.String()
	s.ensureIndexes(vKey)
	s.outgoing[vKey][edge.SourceID] = append(s.outgoing[vKey][edge.SourceID], offset)
	s.incoming[vKey][edge.TargetID] = append(s.incoming[vKey][edge.TargetID], offset)

	return nil
}

// WriteBatch writes multiple edges to the current HEAD version.
func (s *VersionEdgeStore) WriteBatch(edges []*Edge) error {
	return s.WriteBatchToVersion(s.session.Manifest.Head, edges)
}

// WriteBatchToVersion writes multiple edges to a specific version.
// WAL entries are batched with a single fsync for the entire batch.
func (s *VersionEdgeStore) WriteBatchToVersion(version SemanticVersion, edges []*Edge) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.walLogEdgeBatch(edges); err != nil {
		return err
	}
	return s.appendEdgeBatch(version, edges)
}

// walLogEdgeBatch writes all edge WAL entries with a single sync.
func (s *VersionEdgeStore) walLogEdgeBatch(edges []*Edge) error {
	if s.session.WAL == nil {
		return nil
	}
	walData := make([]*WALEdgeData, len(edges))
	for i, edge := range edges {
		walData[i] = edgeToWALData(edge)
	}
	return s.session.WAL.LogEdgeBatch(walData)
}

// appendEdgeBatch writes all edges to the data file and updates indexes.
func (s *VersionEdgeStore) appendEdgeBatch(version SemanticVersion, edges []*Edge) error {
	dataPath := filepath.Join(s.session.VersionPath(version), "edges", "data.bin")

	if err := os.MkdirAll(filepath.Dir(dataPath), 0755); err != nil {
		return fmt.Errorf("create edges dir: %w", err)
	}

	f, err := os.OpenFile(dataPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open edges file: %w", err)
	}
	defer f.Close()

	offset, _ := f.Seek(0, io.SeekEnd)
	vKey := version.String()
	s.ensureIndexes(vKey)

	for _, edge := range edges {
		data := edge.MarshalBinary()
		if _, err := f.Write(data); err != nil {
			return fmt.Errorf("write edge: %w", err)
		}
		s.outgoing[vKey][edge.SourceID] = append(s.outgoing[vKey][edge.SourceID], offset)
		s.incoming[vKey][edge.TargetID] = append(s.incoming[vKey][edge.TargetID], offset)
		offset += int64(len(data))
	}

	return nil
}

func (s *VersionEdgeStore) ensureIndexes(vKey string) {
	if s.outgoing[vKey] == nil {
		s.outgoing[vKey] = make(map[uint32][]int64)
	}
	if s.incoming[vKey] == nil {
		s.incoming[vKey] = make(map[uint32][]int64)
	}
}

// GetOutgoingFromVersion gets all outgoing edges from a node in a specific version.
// With cumulative snapshots, always scans the file since the in-memory index
// may not include edges copied from parent versions during checkpoint.
func (s *VersionEdgeStore) GetOutgoingFromVersion(version SemanticVersion, nodeID uint32) ([]*Edge, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dataPath := filepath.Join(s.session.VersionPath(version), "edges", "data.bin")

	f, err := os.Open(dataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []*Edge{}, nil
		}
		return nil, err
	}
	defer f.Close()

	// Always scan file for correctness with cumulative snapshots
	return s.scanEdgesWithSource(f, nodeID)
}

// GetOutgoingFromAncestorChain gets all outgoing edges from HEAD version.
// Since versions are cumulative snapshots, HEAD contains all ancestor data.
func (s *VersionEdgeStore) GetOutgoingFromAncestorChain(nodeID uint32) ([]*Edge, error) {
	return s.GetOutgoingFromVersion(s.session.Manifest.Head, nodeID)
}

// GetIncomingFromVersion gets all incoming edges to a node in a specific version.
func (s *VersionEdgeStore) GetIncomingFromVersion(version SemanticVersion, nodeID uint32) ([]*Edge, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dataPath := filepath.Join(s.session.VersionPath(version), "edges", "data.bin")

	f, err := os.Open(dataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []*Edge{}, nil
		}
		return nil, err
	}
	defer f.Close()

	vKey := version.String()
	if idx, ok := s.incoming[vKey]; ok {
		if offsets, ok := idx[nodeID]; ok {
			return s.readEdgesAtOffsets(f, offsets)
		}
	}

	return s.scanEdgesWithTarget(f, nodeID)
}

// ReadAllFromVersion reads all edges from a specific version.
// With cumulative snapshots, deduplicates by EdgeKey (last occurrence wins).
func (s *VersionEdgeStore) ReadAllFromVersion(version SemanticVersion) ([]*Edge, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dataPath := filepath.Join(s.session.VersionPath(version), "edges", "data.bin")

	f, err := os.Open(dataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []*Edge{}, nil
		}
		return nil, err
	}
	defer f.Close()

	// Deduplicate by EdgeKey (last occurrence wins for cumulative snapshots)
	edgeMap := make(map[EdgeKey]*Edge)
	var order []EdgeKey
	buf := make([]byte, EdgeRecordSize)

	for {
		if _, err := io.ReadFull(f, buf); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		edge := &Edge{}
		if err := edge.UnmarshalBinary(buf); err != nil {
			return nil, err
		}
		key := edge.EdgeKey()
		if _, seen := edgeMap[key]; !seen {
			order = append(order, key)
		}
		edgeMap[key] = edge
	}

	// Return in order of first appearance
	edges := make([]*Edge, len(order))
	for i, key := range order {
		edges[i] = edgeMap[key]
	}
	return edges, nil
}

// ReadAllFromAncestorChain reads all edges from HEAD version.
// Since versions are cumulative snapshots, HEAD contains all ancestor data.
func (s *VersionEdgeStore) ReadAllFromAncestorChain() ([]*Edge, error) {
	return s.ReadAllFromVersion(s.session.Manifest.Head)
}

func (s *VersionEdgeStore) readEdgesAtOffsets(f *os.File, offsets []int64) ([]*Edge, error) {
	edges := make([]*Edge, 0, len(offsets))
	buf := make([]byte, EdgeRecordSize)

	for _, offset := range offsets {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return nil, err
		}
		if _, err := io.ReadFull(f, buf); err != nil {
			return nil, err
		}
		edge := &Edge{}
		if err := edge.UnmarshalBinary(buf); err != nil {
			return nil, err
		}
		edges = append(edges, edge)
	}

	return edges, nil
}

func (s *VersionEdgeStore) scanEdgesWithSource(f *os.File, sourceID uint32) ([]*Edge, error) {
	f.Seek(0, io.SeekStart)
	var edges []*Edge
	buf := make([]byte, EdgeRecordSize)

	for {
		if _, err := io.ReadFull(f, buf); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		edge := &Edge{}
		if err := edge.UnmarshalBinary(buf); err != nil {
			return nil, err
		}
		if edge.SourceID == sourceID {
			edges = append(edges, edge)
		}
	}

	return edges, nil
}

func (s *VersionEdgeStore) scanEdgesWithTarget(f *os.File, targetID uint32) ([]*Edge, error) {
	f.Seek(0, io.SeekStart)
	var edges []*Edge
	buf := make([]byte, EdgeRecordSize)

	for {
		if _, err := io.ReadFull(f, buf); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		edge := &Edge{}
		if err := edge.UnmarshalBinary(buf); err != nil {
			return nil, err
		}
		if edge.TargetID == targetID {
			edges = append(edges, edge)
		}
	}

	return edges, nil
}

// Count returns the number of edges in HEAD version.
func (s *VersionEdgeStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := 0
	vKey := s.session.Manifest.Head.String()
	if idx, ok := s.outgoing[vKey]; ok {
		for _, offsets := range idx {
			count += len(offsets)
		}
	}
	return count
}

// VersionDocument represents a document stored in version storage.
type VersionDocument struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Type      string `json:"type"`
	Content   string `json:"content"`
	Language  string `json:"language,omitempty"`
	IndexedAt int64  `json:"indexed_at"`
}

// VersionDocStore writes documents to the session's shared doc data file with
// per-version offset indexes for O(1) lookups and zero-copy checkpoints.
// Binary format per record: [Size:4][JSON:Size]
//
// Concurrency model: single-writer-multiple-reader.
// Reads are lock-free using sync.Map and lock-free OffsetIndex operations.
// Writes are serialized via writeMu.
type VersionDocStore struct {
	session *Session
	indexes sync.Map   // string -> *OffsetIndex
	writeMu sync.Mutex
}

// NewVersionDocStore creates a document store for the given session.
func NewVersionDocStore(sess *Session) *VersionDocStore {
	s := &VersionDocStore{session: sess}
	sess.RegisterDocStore(s)
	return s
}

// Write writes a document to the current HEAD version.
func (s *VersionDocStore) Write(doc *VersionDocument) error {
	return s.WriteToVersion(s.session.Manifest.Head, doc)
}

// WriteToVersion writes a document to a specific version.
func (s *VersionDocStore) WriteToVersion(version SemanticVersion, doc *VersionDocument) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	// WAL log before data write for crash safety.
	if s.session.WAL != nil {
		if _, err := s.session.WAL.LogDocInsert(docToWALData(doc)); err != nil {
			return fmt.Errorf("wal log doc: %w", err)
		}
	}

	record, err := marshalDocRecord(doc)
	if err != nil {
		return err
	}

	offset, err := s.session.DocDataFile.Append(record)
	if err != nil {
		return fmt.Errorf("append doc: %w", err)
	}

	docID := s.session.DocIDMap.GetOrAssign(doc.ID)
	s.docIndex(version).Set(docID, offset)
	return nil
}

// WriteBatch writes multiple documents to the current HEAD version.
func (s *VersionDocStore) WriteBatch(docs []*VersionDocument) error {
	return s.WriteBatchToVersion(s.session.Manifest.Head, docs)
}

// WriteBatchToVersion writes multiple documents to a specific version.
// WAL entries are batched with a single fsync for the entire batch.
func (s *VersionDocStore) WriteBatchToVersion(version SemanticVersion, docs []*VersionDocument) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if err := s.walLogDocBatch(docs); err != nil {
		return err
	}
	return s.appendDocBatch(version, docs)
}

// walLogDocBatch writes all doc WAL entries with a single sync.
func (s *VersionDocStore) walLogDocBatch(docs []*VersionDocument) error {
	if s.session.WAL == nil {
		return nil
	}
	walData := make([]*WALDocData, len(docs))
	for i, doc := range docs {
		walData[i] = docToWALData(doc)
	}
	return s.session.WAL.LogDocBatch(walData)
}

// appendDocBatch appends all docs to the data file and updates the index.
func (s *VersionDocStore) appendDocBatch(version SemanticVersion, docs []*VersionDocument) error {
	idx := s.docIndex(version)
	for _, doc := range docs {
		if err := s.appendSingleDoc(idx, doc); err != nil {
			return err
		}
	}
	return nil
}

// appendSingleDoc marshals and appends one document to the data file.
func (s *VersionDocStore) appendSingleDoc(idx *OffsetIndex, doc *VersionDocument) error {
	record, err := marshalDocRecord(doc)
	if err != nil {
		return err
	}
	offset, err := s.session.DocDataFile.Append(record)
	if err != nil {
		return fmt.Errorf("append doc %s: %w", doc.ID, err)
	}
	idx.Set(s.session.DocIDMap.GetOrAssign(doc.ID), offset)
	return nil
}

// ReadFromVersion reads all documents from a specific version.
// No dedup needed — the offset index guarantees one entry per doc ID.
func (s *VersionDocStore) ReadFromVersion(version SemanticVersion) ([]*VersionDocument, error) {
	idx := s.loadDocIndex(version)
	if idx == nil {
		return []*VersionDocument{}, nil
	}

	docs := make([]*VersionDocument, 0, idx.Count())
	var readErr error
	idx.ForEach(func(_ uint32, offset int64) bool {
		doc, err := readDocAtOffset(s.session.DocDataFile, offset)
		if err != nil {
			readErr = err
			return false
		}
		docs = append(docs, doc)
		return true
	})

	return docs, readErr
}

// ReadByDocRef reads a single document by its uint32 DocRef from a version.
// DocRef is the key assigned by DocIDMap and stored in the OffsetIndex.
func (s *VersionDocStore) ReadByDocRef(version SemanticVersion, docRef uint32) (*VersionDocument, error) {
	idx := s.loadDocIndex(version)
	if idx == nil {
		return nil, fmt.Errorf("sylkdir: no doc index for version %s", version.String())
	}
	offset, ok := idx.Get(docRef)
	if !ok {
		return nil, fmt.Errorf("sylkdir: doc ref %d not found", docRef)
	}
	return readDocAtOffset(s.session.DocDataFile, offset)
}

// ReadFromAncestorChain reads all documents from HEAD version.
// Since versions are cumulative snapshots, HEAD contains all ancestor data.
func (s *VersionDocStore) ReadFromAncestorChain() ([]*VersionDocument, error) {
	return s.ReadFromVersion(s.session.Manifest.Head)
}

// Count returns the number of documents in HEAD version.
func (s *VersionDocStore) Count() (int, error) {
	idx := s.loadDocIndex(s.session.Manifest.Head)
	if idx == nil {
		return 0, nil
	}
	return int(idx.Count()), nil
}

// SaveIndexes saves all in-memory offset indexes to disk.
func (s *VersionDocStore) SaveIndexes() error {
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

// RegisterIndex stores an OffsetIndex in the in-memory map for a version.
// Used during checkpoint to register the cloned index.
func (s *VersionDocStore) RegisterIndex(version SemanticVersion, idx *OffsetIndex) {
	s.indexes.Store(version.String(), idx)
}

// getInMemoryIndex returns the in-memory OffsetIndex for a version, or nil.
func (s *VersionDocStore) getInMemoryIndex(version SemanticVersion) *OffsetIndex {
	if val, ok := s.indexes.Load(version.String()); ok {
		return val.(*OffsetIndex)
	}
	return nil
}

// docIndex returns the OffsetIndex for a version, creating if needed.
// Called under writeMu; uses sync.Map for storage.
func (s *VersionDocStore) docIndex(version SemanticVersion) *OffsetIndex {
	vKey := version.String()
	if val, ok := s.indexes.Load(vKey); ok {
		return val.(*OffsetIndex)
	}
	path := s.session.DocIndexPath(version)
	idx, err := LoadOffsetIndex(path)
	if err != nil {
		idx = NewOffsetIndex(path, offsetIndexMinCapacity)
	}
	s.indexes.Store(vKey, idx)
	return idx
}

// loadDocIndex returns the OffsetIndex for a version (read path). Lock-free.
// Caches loaded indexes via sync.Map.LoadOrStore.
func (s *VersionDocStore) loadDocIndex(version SemanticVersion) *OffsetIndex {
	vKey := version.String()
	if val, ok := s.indexes.Load(vKey); ok {
		return val.(*OffsetIndex)
	}
	path := s.session.DocIndexPath(version)
	idx, err := LoadOffsetIndex(path)
	if err != nil {
		return nil
	}
	actual, _ := s.indexes.LoadOrStore(vKey, idx)
	return actual.(*OffsetIndex)
}

// docRecordMinSize is the minimum binary doc record size:
// TotalSize(4) + IndexedAt(8) + IDLen(2) + PathLen(2) + TypeLen(2) + ContentLen(4) + LangLen(2) = 24
const docRecordMinSize = 24

// marshalDocRecord serializes a document as binary:
// [TotalSize:4][IndexedAt:8][IDLen:2][ID][PathLen:2][Path][TypeLen:2][Type][ContentLen:4][Content][LangLen:2][Language]
func marshalDocRecord(doc *VersionDocument) ([]byte, error) {
	payloadSize := docRecordMinSize - 4 + len(doc.ID) + len(doc.Path) + len(doc.Type) + len(doc.Content) + len(doc.Language)
	record := make([]byte, 4+payloadSize)

	binary.LittleEndian.PutUint32(record[0:4], uint32(payloadSize))
	off := 4

	binary.LittleEndian.PutUint64(record[off:off+8], uint64(doc.IndexedAt))
	off += 8

	off = docWriteStr16(record, off, doc.ID)
	off = docWriteStr16(record, off, doc.Path)
	off = docWriteStr16(record, off, doc.Type)

	binary.LittleEndian.PutUint32(record[off:off+4], uint32(len(doc.Content)))
	off += 4
	copy(record[off:], doc.Content)
	off += len(doc.Content)

	docWriteStr16(record, off, doc.Language)
	return record, nil
}

// docWriteStr16 writes a 2-byte length-prefixed string and returns the new offset.
func docWriteStr16(buf []byte, off int, s string) int {
	binary.LittleEndian.PutUint16(buf[off:off+2], uint16(len(s)))
	off += 2
	copy(buf[off:], s)
	return off + len(s)
}

// readDocAtOffset reads a document from a shared data file at the given offset.
func readDocAtOffset(sdf *SharedDataFile, offset int64) (*VersionDocument, error) {
	sizeBuf := make([]byte, 4)
	if _, err := sdf.ReadAt(sizeBuf, offset); err != nil {
		return nil, err
	}
	size := binary.LittleEndian.Uint32(sizeBuf)

	data := make([]byte, size)
	if _, err := sdf.ReadAt(data, offset+4); err != nil {
		return nil, err
	}

	return unmarshalDocPayload(data)
}

// unmarshalDocPayload decodes a binary doc payload (without the TotalSize prefix).
func unmarshalDocPayload(data []byte) (*VersionDocument, error) {
	if len(data) < docRecordMinSize-4 {
		return nil, fmt.Errorf("doc record truncated: %d bytes", len(data))
	}

	doc := &VersionDocument{}
	off := 0

	doc.IndexedAt = int64(binary.LittleEndian.Uint64(data[off : off+8]))
	off += 8

	var err error
	doc.ID, off, err = docReadStr16(data, off)
	if err != nil {
		return nil, err
	}
	doc.Path, off, err = docReadStr16(data, off)
	if err != nil {
		return nil, err
	}
	doc.Type, off, err = docReadStr16(data, off)
	if err != nil {
		return nil, err
	}

	if off+4 > len(data) {
		return nil, fmt.Errorf("doc content length truncated")
	}
	contentLen := int(binary.LittleEndian.Uint32(data[off : off+4]))
	off += 4
	if off+contentLen > len(data) {
		return nil, fmt.Errorf("doc content truncated")
	}
	doc.Content = string(data[off : off+contentLen])
	off += contentLen

	doc.Language, _, err = docReadStr16(data, off)
	if err != nil {
		return nil, err
	}
	return doc, nil
}

// docReadStr16 reads a 2-byte length-prefixed string and returns the new offset.
func docReadStr16(data []byte, off int) (string, int, error) {
	if off+2 > len(data) {
		return "", off, fmt.Errorf("doc string length truncated at %d", off)
	}
	slen := int(binary.LittleEndian.Uint16(data[off : off+2]))
	off += 2
	if off+slen > len(data) {
		return "", off, fmt.Errorf("doc string data truncated at %d", off)
	}
	return string(data[off : off+slen]), off + slen, nil
}

// VersionVector represents a vector embedding for a node.
type VersionVector struct {
	NodeID uint32
	Vector []float32
}

// VersionVectorStore writes vectors to the session's shared data file with
// per-version offset indexes for O(1) lookups and zero-copy checkpoints.
// Binary format per record: [NodeID:4][Dim:4][Vector:Dim*4]
//
// Concurrency model: single-writer-multiple-reader.
// Reads are lock-free using sync.Map and lock-free OffsetIndex operations.
// Writes are serialized via writeMu.
type VersionVectorStore struct {
	session *Session
	indexes sync.Map   // string -> *OffsetIndex
	writeMu sync.Mutex
}

// NewVersionVectorStore creates a vector store for the given session.
func NewVersionVectorStore(sess *Session) *VersionVectorStore {
	s := &VersionVectorStore{session: sess}
	sess.RegisterVectorStore(s)
	return s
}

// Write writes a vector to the current HEAD version.
func (s *VersionVectorStore) Write(vec *VersionVector) error {
	return s.WriteToVersion(s.session.Manifest.Head, vec)
}

// WriteToVersion writes a vector to a specific version.
func (s *VersionVectorStore) WriteToVersion(version SemanticVersion, vec *VersionVector) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	// WAL log before data write for crash safety.
	if s.session.WAL != nil {
		if _, err := s.session.WAL.LogVectorInsert(vectorToWALData(vec)); err != nil {
			return fmt.Errorf("wal log vector: %w", err)
		}
	}

	record := marshalVectorRecord(vec)
	offset, err := s.session.VectorDataFile.Append(record)
	if err != nil {
		return fmt.Errorf("append vector: %w", err)
	}

	s.vectorIndex(version).Set(vec.NodeID, offset)
	return nil
}

// WriteBatch writes multiple vectors to the current HEAD version.
func (s *VersionVectorStore) WriteBatch(vecs []*VersionVector) error {
	return s.WriteBatchToVersion(s.session.Manifest.Head, vecs)
}

// WriteBatchToVersion writes multiple vectors to a specific version.
// WAL entries are batched with a single fsync for the entire batch.
func (s *VersionVectorStore) WriteBatchToVersion(version SemanticVersion, vecs []*VersionVector) error {
	if len(vecs) == 0 {
		return nil
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if err := s.walLogVectorBatch(vecs); err != nil {
		return err
	}
	return s.appendVectorBatch(version, vecs)
}

// walLogVectorBatch writes all vector WAL entries with a single sync.
func (s *VersionVectorStore) walLogVectorBatch(vecs []*VersionVector) error {
	if s.session.WAL == nil {
		return nil
	}
	walData := make([]*WALVectorData, len(vecs))
	for i, vec := range vecs {
		walData[i] = vectorToWALData(vec)
	}
	return s.session.WAL.LogVectorBatch(walData)
}

// appendVectorBatch appends all vectors to the data file and updates the index.
func (s *VersionVectorStore) appendVectorBatch(version SemanticVersion, vecs []*VersionVector) error {
	idx := s.vectorIndex(version)
	for _, vec := range vecs {
		if err := s.appendSingleVector(idx, vec); err != nil {
			return err
		}
	}
	return nil
}

// appendSingleVector marshals and appends one vector to the data file.
func (s *VersionVectorStore) appendSingleVector(idx *OffsetIndex, vec *VersionVector) error {
	record := marshalVectorRecord(vec)
	offset, err := s.session.VectorDataFile.Append(record)
	if err != nil {
		return fmt.Errorf("append vector %d: %w", vec.NodeID, err)
	}
	idx.Set(vec.NodeID, offset)
	return nil
}

// GetFromVersion reads a vector by node ID from a specific version.
func (s *VersionVectorStore) GetFromVersion(version SemanticVersion, nodeID uint32) (*VersionVector, error) {
	idx := s.loadVectorIndex(version)
	if idx == nil {
		return nil, nil
	}

	offset, ok := idx.Get(nodeID)
	if !ok {
		return nil, nil
	}

	return readVectorAtOffset(s.session.VectorDataFile, offset)
}

// GetFromAncestorChain reads a vector by node ID from HEAD version.
func (s *VersionVectorStore) GetFromAncestorChain(nodeID uint32) (*VersionVector, error) {
	return s.GetFromVersion(s.session.Manifest.Head, nodeID)
}

// ReadAllFromVersion reads all vectors from a specific version.
// No dedup needed — the offset index guarantees one entry per node ID.
func (s *VersionVectorStore) ReadAllFromVersion(version SemanticVersion) ([]*VersionVector, error) {
	idx := s.loadVectorIndex(version)
	if idx == nil {
		return []*VersionVector{}, nil
	}

	vectors := make([]*VersionVector, 0, idx.Count())
	var readErr error
	idx.ForEach(func(_ uint32, offset int64) bool {
		vec, err := readVectorAtOffset(s.session.VectorDataFile, offset)
		if err != nil {
			readErr = err
			return false
		}
		vectors = append(vectors, vec)
		return true
	})

	return vectors, readErr
}

// ReadAllFromAncestorChain reads all vectors from HEAD version.
func (s *VersionVectorStore) ReadAllFromAncestorChain() ([]*VersionVector, error) {
	return s.ReadAllFromVersion(s.session.Manifest.Head)
}

// Count returns the number of vectors in HEAD version.
func (s *VersionVectorStore) Count() (int, error) {
	idx := s.loadVectorIndex(s.session.Manifest.Head)
	if idx == nil {
		return 0, nil
	}
	return int(idx.Count()), nil
}

// SaveIndexes saves all in-memory offset indexes to disk.
func (s *VersionVectorStore) SaveIndexes() error {
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

// RegisterIndex stores an OffsetIndex in the in-memory map for a version.
func (s *VersionVectorStore) RegisterIndex(version SemanticVersion, idx *OffsetIndex) {
	s.indexes.Store(version.String(), idx)
}

// getInMemoryIndex returns the in-memory OffsetIndex for a version, or nil.
func (s *VersionVectorStore) getInMemoryIndex(version SemanticVersion) *OffsetIndex {
	if val, ok := s.indexes.Load(version.String()); ok {
		return val.(*OffsetIndex)
	}
	return nil
}

// vectorIndex returns the OffsetIndex for a version, creating if needed.
// Called under writeMu; uses sync.Map for storage.
func (s *VersionVectorStore) vectorIndex(version SemanticVersion) *OffsetIndex {
	vKey := version.String()
	if val, ok := s.indexes.Load(vKey); ok {
		return val.(*OffsetIndex)
	}
	path := s.session.VectorIndexPath(version)
	idx, err := LoadOffsetIndex(path)
	if err != nil {
		idx = NewOffsetIndex(path, offsetIndexMinCapacity)
	}
	s.indexes.Store(vKey, idx)
	return idx
}

// loadVectorIndex returns the OffsetIndex for a version (read path). Lock-free.
// Caches loaded indexes via sync.Map.LoadOrStore.
func (s *VersionVectorStore) loadVectorIndex(version SemanticVersion) *OffsetIndex {
	vKey := version.String()
	if val, ok := s.indexes.Load(vKey); ok {
		return val.(*OffsetIndex)
	}
	path := s.session.VectorIndexPath(version)
	idx, err := LoadOffsetIndex(path)
	if err != nil {
		return nil
	}
	actual, _ := s.indexes.LoadOrStore(vKey, idx)
	return actual.(*OffsetIndex)
}

// marshalVectorRecord serializes a vector as [NodeID:4][Dim:4][Vector:Dim*4].
func marshalVectorRecord(vec *VersionVector) []byte {
	dim := len(vec.Vector)
	record := make([]byte, 8+dim*4)
	binary.LittleEndian.PutUint32(record[0:4], vec.NodeID)
	binary.LittleEndian.PutUint32(record[4:8], uint32(dim))
	for i, v := range vec.Vector {
		binary.LittleEndian.PutUint32(record[8+i*4:], uint32FromFloat32(v))
	}
	return record
}

// readVectorAtOffset reads a vector from a shared data file at the given offset.
func readVectorAtOffset(sdf *SharedDataFile, offset int64) (*VersionVector, error) {
	header := make([]byte, 8)
	if _, err := sdf.ReadAt(header, offset); err != nil {
		return nil, err
	}

	nodeID := binary.LittleEndian.Uint32(header[0:4])
	dim := binary.LittleEndian.Uint32(header[4:8])

	vecBuf := make([]byte, dim*4)
	if _, err := sdf.ReadAt(vecBuf, offset+8); err != nil {
		return nil, err
	}

	vec := make([]float32, dim)
	for i := range vec {
		vec[i] = float32FromUint32(binary.LittleEndian.Uint32(vecBuf[i*4:]))
	}

	return &VersionVector{NodeID: nodeID, Vector: vec}, nil
}

// uint32FromFloat32 converts float32 to uint32 bit representation.
func uint32FromFloat32(f float32) uint32 {
	return math.Float32bits(f)
}

// float32FromUint32 converts uint32 bit representation to float32.
func float32FromUint32(u uint32) float32 {
	return math.Float32frombits(u)
}

// --- WAL conversion helpers ---

func nodeToWALData(n *Node) *WALNodeData {
	return &WALNodeData{
		NodeID:   n.ID,
		Domain:   n.Domain,
		NodeType: n.NodeType,
		Key:      n.CanonicalKey,
		Name:     n.Name,
		Path:     n.Path,
	}
}

func edgeToWALData(e *Edge) *WALEdgeData {
	return &WALEdgeData{
		SourceID:  e.SourceID,
		TargetID:  e.TargetID,
		EdgeType:  e.Type,
		Weight:    e.Weight,
		SessionID: e.SessionID,
	}
}

func vectorToWALData(v *VersionVector) *WALVectorData {
	return &WALVectorData{
		NodeID: v.NodeID,
		Vector: v.Vector,
	}
}

func docToWALData(d *VersionDocument) *WALDocData {
	return &WALDocData{
		ID:       d.ID,
		Path:     d.Path,
		DocType:  d.Type,
		Content:  d.Content,
		Language: d.Language,
	}
}
