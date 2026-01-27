// Package sylkdir provides version-aware data stores for session isolation.
package sylkdir

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
)

// VersionNodeStore writes nodes to session version directories.
type VersionNodeStore struct {
	session *Session
	mu      sync.RWMutex

	// Per-version indexes: version string -> nodeID -> offset
	indexes map[string]map[uint32]int64
}

// NewVersionNodeStore creates a node store for the given session.
func NewVersionNodeStore(sess *Session) *VersionNodeStore {
	return &VersionNodeStore{
		session: sess,
		indexes: make(map[string]map[uint32]int64),
	}
}

// Write writes a node to the current HEAD version.
func (s *VersionNodeStore) Write(node *Node) error {
	return s.WriteToVersion(s.session.Manifest.Head, node)
}

// WriteToVersion writes a node to a specific version.
func (s *VersionNodeStore) WriteToVersion(version SemanticVersion, node *Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dataPath := filepath.Join(s.session.VersionPath(version), "nodes", "data.bin")

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(dataPath), 0755); err != nil {
		return fmt.Errorf("create nodes dir: %w", err)
	}

	// Open file for append
	f, err := os.OpenFile(dataPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open nodes file: %w", err)
	}
	defer f.Close()

	// Get current offset for index
	offset, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("seek: %w", err)
	}

	// Serialize and write node with size prefix
	data, err := node.MarshalBinary()
	if err != nil {
		return fmt.Errorf("marshal node: %w", err)
	}

	// Write size prefix (4 bytes) + data
	sizeBuf := make([]byte, 4)
	binary.LittleEndian.PutUint32(sizeBuf, uint32(len(data)))
	if _, err := f.Write(sizeBuf); err != nil {
		return fmt.Errorf("write size: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write node: %w", err)
	}

	// Update in-memory index
	vKey := version.String()
	if s.indexes[vKey] == nil {
		s.indexes[vKey] = make(map[uint32]int64)
	}
	s.indexes[vKey][node.ID] = offset

	return nil
}

// WriteBatch writes multiple nodes to the current HEAD version.
func (s *VersionNodeStore) WriteBatch(nodes []*Node) error {
	return s.WriteBatchToVersion(s.session.Manifest.Head, nodes)
}

// WriteBatchToVersion writes multiple nodes to a specific version.
func (s *VersionNodeStore) WriteBatchToVersion(version SemanticVersion, nodes []*Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dataPath := filepath.Join(s.session.VersionPath(version), "nodes", "data.bin")

	if err := os.MkdirAll(filepath.Dir(dataPath), 0755); err != nil {
		return fmt.Errorf("create nodes dir: %w", err)
	}

	f, err := os.OpenFile(dataPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open nodes file: %w", err)
	}
	defer f.Close()

	offset, _ := f.Seek(0, io.SeekEnd)

	vKey := version.String()
	if s.indexes[vKey] == nil {
		s.indexes[vKey] = make(map[uint32]int64)
	}

	sizeBuf := make([]byte, 4)
	for _, node := range nodes {
		data, err := node.MarshalBinary()
		if err != nil {
			return fmt.Errorf("marshal node %d: %w", node.ID, err)
		}

		// Write size prefix + data
		binary.LittleEndian.PutUint32(sizeBuf, uint32(len(data)))
		if _, err := f.Write(sizeBuf); err != nil {
			return fmt.Errorf("write size: %w", err)
		}
		if _, err := f.Write(data); err != nil {
			return fmt.Errorf("write node %d: %w", node.ID, err)
		}
		s.indexes[vKey][node.ID] = offset
		offset += int64(4 + len(data))
	}

	return nil
}

// ReadFromVersion reads a node from a specific version.
func (s *VersionNodeStore) ReadFromVersion(version SemanticVersion, nodeID uint32) (*Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dataPath := filepath.Join(s.session.VersionPath(version), "nodes", "data.bin")

	f, err := os.Open(dataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNodeNotFound
		}
		return nil, err
	}
	defer f.Close()

	// Check in-memory index first
	vKey := version.String()
	if idx, ok := s.indexes[vKey]; ok {
		if offset, ok := idx[nodeID]; ok {
			return s.readNodeAt(f, offset)
		}
	}

	// Scan file for node
	return s.scanForNode(f, nodeID)
}

// ReadFromAncestorChain reads a node from the ancestor chain (newest first).
func (s *VersionNodeStore) ReadFromAncestorChain(nodeID uint32) (*Node, error) {
	chain := s.session.GetAncestorChain()

	for _, version := range chain {
		node, err := s.ReadFromVersion(version, nodeID)
		if err == nil {
			return node, nil
		}
		if err != ErrNodeNotFound {
			return nil, err
		}
	}

	return nil, ErrNodeNotFound
}

// ReadAllFromVersion reads all nodes from a specific version.
func (s *VersionNodeStore) ReadAllFromVersion(version SemanticVersion) ([]*Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dataPath := filepath.Join(s.session.VersionPath(version), "nodes", "data.bin")

	f, err := os.Open(dataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []*Node{}, nil
		}
		return nil, err
	}
	defer f.Close()

	var nodes []*Node
	for {
		node, err := s.readNextNode(f)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}

	return nodes, nil
}

// ReadAllFromAncestorChain reads all nodes from the ancestor chain.
// Returns nodes deduplicated by ID (newest version wins).
func (s *VersionNodeStore) ReadAllFromAncestorChain() ([]*Node, error) {
	chain := s.session.GetAncestorChain()
	seen := make(map[uint32]bool)
	var result []*Node

	for _, version := range chain {
		nodes, err := s.ReadAllFromVersion(version)
		if err != nil {
			return nil, err
		}
		for _, node := range nodes {
			if !seen[node.ID] {
				seen[node.ID] = true
				result = append(result, node)
			}
		}
	}

	return result, nil
}

func (s *VersionNodeStore) readNodeAt(f *os.File, offset int64) (*Node, error) {
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	return s.readNextNode(f)
}

func (s *VersionNodeStore) readNextNode(f *os.File) (*Node, error) {
	// Read size prefix (4 bytes)
	sizeBuf := make([]byte, 4)
	if _, err := io.ReadFull(f, sizeBuf); err != nil {
		return nil, err
	}
	size := binary.LittleEndian.Uint32(sizeBuf)

	// Read node data
	data := make([]byte, size)
	if _, err := io.ReadFull(f, data); err != nil {
		return nil, err
	}

	node := &Node{}
	if err := node.UnmarshalBinary(data); err != nil {
		return nil, err
	}

	return node, nil
}

func (s *VersionNodeStore) scanForNode(f *os.File, nodeID uint32) (*Node, error) {
	f.Seek(0, io.SeekStart)

	for {
		node, err := s.readNextNode(f)
		if err == io.EOF {
			return nil, ErrNodeNotFound
		}
		if err != nil {
			return nil, err
		}
		if node.ID == nodeID {
			return node, nil
		}
	}
}

// Count returns the number of nodes in HEAD version.
func (s *VersionNodeStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	vKey := s.session.Manifest.Head.String()
	if idx, ok := s.indexes[vKey]; ok {
		return len(idx)
	}
	return 0
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
func (s *VersionEdgeStore) WriteBatchToVersion(version SemanticVersion, edges []*Edge) error {
	s.mu.Lock()
	defer s.mu.Unlock()

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

	// Use index if available
	vKey := version.String()
	if idx, ok := s.outgoing[vKey]; ok {
		if offsets, ok := idx[nodeID]; ok {
			return s.readEdgesAtOffsets(f, offsets)
		}
	}

	// Scan file
	return s.scanEdgesWithSource(f, nodeID)
}

// GetOutgoingFromAncestorChain gets all outgoing edges from the ancestor chain.
func (s *VersionEdgeStore) GetOutgoingFromAncestorChain(nodeID uint32) ([]*Edge, error) {
	chain := s.session.GetAncestorChain()
	seen := make(map[EdgeKey]bool)
	var result []*Edge

	for _, version := range chain {
		edges, err := s.GetOutgoingFromVersion(version, nodeID)
		if err != nil {
			return nil, err
		}
		for _, edge := range edges {
			key := edge.EdgeKey()
			if !seen[key] {
				seen[key] = true
				result = append(result, edge)
			}
		}
	}

	return result, nil
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

// GetIncomingFromAncestorChain gets all incoming edges from the ancestor chain.
func (s *VersionEdgeStore) GetIncomingFromAncestorChain(nodeID uint32) ([]*Edge, error) {
	chain := s.session.GetAncestorChain()
	seen := make(map[EdgeKey]bool)
	var result []*Edge

	for _, version := range chain {
		edges, err := s.GetIncomingFromVersion(version, nodeID)
		if err != nil {
			return nil, err
		}
		for _, edge := range edges {
			key := edge.EdgeKey()
			if !seen[key] {
				seen[key] = true
				result = append(result, edge)
			}
		}
	}

	return result, nil
}

// ReadAllFromVersion reads all edges from a specific version.
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
		edges = append(edges, edge)
	}

	return edges, nil
}

// ReadAllFromAncestorChain reads all edges from the ancestor chain.
func (s *VersionEdgeStore) ReadAllFromAncestorChain() ([]*Edge, error) {
	chain := s.session.GetAncestorChain()
	seen := make(map[EdgeKey]bool)
	var result []*Edge

	for _, version := range chain {
		edges, err := s.ReadAllFromVersion(version)
		if err != nil {
			return nil, err
		}
		for _, edge := range edges {
			key := edge.EdgeKey()
			if !seen[key] {
				seen[key] = true
				result = append(result, edge)
			}
		}
	}

	return result, nil
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

// VersionDocStore writes documents to session version directories as JSONL.
type VersionDocStore struct {
	session *Session
	mu      sync.RWMutex
}

// NewVersionDocStore creates a document store for the given session.
func NewVersionDocStore(sess *Session) *VersionDocStore {
	return &VersionDocStore{
		session: sess,
	}
}

// Write writes a document to the current HEAD version.
func (s *VersionDocStore) Write(doc *VersionDocument) error {
	return s.WriteToVersion(s.session.Manifest.Head, doc)
}

// WriteToVersion writes a document to a specific version.
func (s *VersionDocStore) WriteToVersion(version SemanticVersion, doc *VersionDocument) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	docsPath := filepath.Join(s.session.VersionPath(version), "docs", "batch.jsonl")

	if err := os.MkdirAll(filepath.Dir(docsPath), 0755); err != nil {
		return fmt.Errorf("create docs dir: %w", err)
	}

	f, err := os.OpenFile(docsPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open docs file: %w", err)
	}
	defer f.Close()

	data, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal doc: %w", err)
	}

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write doc: %w", err)
	}

	return nil
}

// WriteBatch writes multiple documents to the current HEAD version.
func (s *VersionDocStore) WriteBatch(docs []*VersionDocument) error {
	return s.WriteBatchToVersion(s.session.Manifest.Head, docs)
}

// WriteBatchToVersion writes multiple documents to a specific version.
func (s *VersionDocStore) WriteBatchToVersion(version SemanticVersion, docs []*VersionDocument) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	docsPath := filepath.Join(s.session.VersionPath(version), "docs", "batch.jsonl")

	if err := os.MkdirAll(filepath.Dir(docsPath), 0755); err != nil {
		return fmt.Errorf("create docs dir: %w", err)
	}

	f, err := os.OpenFile(docsPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open docs file: %w", err)
	}
	defer f.Close()

	for _, doc := range docs {
		data, err := json.Marshal(doc)
		if err != nil {
			return fmt.Errorf("marshal doc: %w", err)
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			return fmt.Errorf("write doc: %w", err)
		}
	}

	return nil
}

// ReadFromVersion reads all documents from a specific version.
func (s *VersionDocStore) ReadFromVersion(version SemanticVersion) ([]*VersionDocument, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	docsPath := filepath.Join(s.session.VersionPath(version), "docs", "batch.jsonl")

	f, err := os.Open(docsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []*VersionDocument{}, nil
		}
		return nil, err
	}
	defer f.Close()

	var docs []*VersionDocument
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		doc := &VersionDocument{}
		if err := json.Unmarshal(line, doc); err != nil {
			return nil, fmt.Errorf("parse doc: %w", err)
		}
		docs = append(docs, doc)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return docs, nil
}

// ReadFromAncestorChain reads all documents from the ancestor chain.
// Returns documents deduplicated by ID (newest version wins).
func (s *VersionDocStore) ReadFromAncestorChain() ([]*VersionDocument, error) {
	chain := s.session.GetAncestorChain()
	seen := make(map[string]bool)
	var result []*VersionDocument

	for _, version := range chain {
		docs, err := s.ReadFromVersion(version)
		if err != nil {
			return nil, err
		}
		for _, doc := range docs {
			if !seen[doc.ID] {
				seen[doc.ID] = true
				result = append(result, doc)
			}
		}
	}

	return result, nil
}

// Count returns the number of documents in HEAD version.
func (s *VersionDocStore) Count() (int, error) {
	docs, err := s.ReadFromVersion(s.session.Manifest.Head)
	if err != nil {
		return 0, err
	}
	return len(docs), nil
}

// CountInAncestorChain returns the total unique documents in ancestor chain.
func (s *VersionDocStore) CountInAncestorChain() (int, error) {
	docs, err := s.ReadFromAncestorChain()
	if err != nil {
		return 0, err
	}
	return len(docs), nil
}

// VersionVector represents a vector embedding for a node.
type VersionVector struct {
	NodeID uint32
	Vector []float32
}

// VersionVectorStore writes vectors to session version directories.
// Binary format per record: [NodeID:4][Dim:4][Vector:Dim*4]
type VersionVectorStore struct {
	session *Session
	mu      sync.RWMutex

	// Per-version index: version string -> nodeID -> offset
	indexes map[string]map[uint32]int64
}

// NewVersionVectorStore creates a vector store for the given session.
func NewVersionVectorStore(sess *Session) *VersionVectorStore {
	return &VersionVectorStore{
		session: sess,
		indexes: make(map[string]map[uint32]int64),
	}
}

// Write writes a vector to the current HEAD version.
func (s *VersionVectorStore) Write(vec *VersionVector) error {
	return s.WriteToVersion(s.session.Manifest.Head, vec)
}

// WriteToVersion writes a vector to a specific version.
func (s *VersionVectorStore) WriteToVersion(version SemanticVersion, vec *VersionVector) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dataPath := filepath.Join(s.session.VersionPath(version), "vectors", "data.bin")

	if err := os.MkdirAll(filepath.Dir(dataPath), 0755); err != nil {
		return fmt.Errorf("create vectors dir: %w", err)
	}

	f, err := os.OpenFile(dataPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open vectors file: %w", err)
	}
	defer f.Close()

	offset, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("seek: %w", err)
	}

	// Write: [NodeID:4][Dim:4][Vector:Dim*4]
	header := make([]byte, 8)
	binary.LittleEndian.PutUint32(header[0:4], vec.NodeID)
	binary.LittleEndian.PutUint32(header[4:8], uint32(len(vec.Vector)))
	if _, err := f.Write(header); err != nil {
		return fmt.Errorf("write header: %w", err)
	}

	// Write vector data as float32 array
	for _, v := range vec.Vector {
		buf := make([]byte, 4)
		binary.LittleEndian.PutUint32(buf, uint32FromFloat32(v))
		if _, err := f.Write(buf); err != nil {
			return fmt.Errorf("write vector: %w", err)
		}
	}

	// Update index
	vKey := version.String()
	if s.indexes[vKey] == nil {
		s.indexes[vKey] = make(map[uint32]int64)
	}
	s.indexes[vKey][vec.NodeID] = offset

	return nil
}

// WriteBatch writes multiple vectors to the current HEAD version.
func (s *VersionVectorStore) WriteBatch(vecs []*VersionVector) error {
	return s.WriteBatchToVersion(s.session.Manifest.Head, vecs)
}

// WriteBatchToVersion writes multiple vectors to a specific version.
func (s *VersionVectorStore) WriteBatchToVersion(version SemanticVersion, vecs []*VersionVector) error {
	if len(vecs) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	dataPath := filepath.Join(s.session.VersionPath(version), "vectors", "data.bin")

	if err := os.MkdirAll(filepath.Dir(dataPath), 0755); err != nil {
		return fmt.Errorf("create vectors dir: %w", err)
	}

	f, err := os.OpenFile(dataPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open vectors file: %w", err)
	}
	defer f.Close()

	vKey := version.String()
	if s.indexes[vKey] == nil {
		s.indexes[vKey] = make(map[uint32]int64)
	}

	for _, vec := range vecs {
		offset, err := f.Seek(0, io.SeekEnd)
		if err != nil {
			return fmt.Errorf("seek: %w", err)
		}

		// Write header
		header := make([]byte, 8)
		binary.LittleEndian.PutUint32(header[0:4], vec.NodeID)
		binary.LittleEndian.PutUint32(header[4:8], uint32(len(vec.Vector)))
		if _, err := f.Write(header); err != nil {
			return fmt.Errorf("write header: %w", err)
		}

		// Write vector data
		for _, v := range vec.Vector {
			buf := make([]byte, 4)
			binary.LittleEndian.PutUint32(buf, uint32FromFloat32(v))
			if _, err := f.Write(buf); err != nil {
				return fmt.Errorf("write vector: %w", err)
			}
		}

		s.indexes[vKey][vec.NodeID] = offset
	}

	return nil
}

// GetFromVersion reads a vector by node ID from a specific version.
func (s *VersionVectorStore) GetFromVersion(version SemanticVersion, nodeID uint32) (*VersionVector, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dataPath := filepath.Join(s.session.VersionPath(version), "vectors", "data.bin")

	f, err := os.Open(dataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	// Try index first
	vKey := version.String()
	if idx, ok := s.indexes[vKey]; ok {
		if offset, ok := idx[nodeID]; ok {
			return s.readVectorAtOffset(f, offset)
		}
	}

	// Scan file
	return s.scanForNodeID(f, nodeID)
}

// GetFromAncestorChain reads a vector by node ID from the ancestor chain.
// Returns the first (newest) version found.
func (s *VersionVectorStore) GetFromAncestorChain(nodeID uint32) (*VersionVector, error) {
	chain := s.session.GetAncestorChain()

	for _, version := range chain {
		vec, err := s.GetFromVersion(version, nodeID)
		if err != nil {
			return nil, err
		}
		if vec != nil {
			return vec, nil
		}
	}

	return nil, nil
}

// ReadAllFromVersion reads all vectors from a specific version.
func (s *VersionVectorStore) ReadAllFromVersion(version SemanticVersion) ([]*VersionVector, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dataPath := filepath.Join(s.session.VersionPath(version), "vectors", "data.bin")

	f, err := os.Open(dataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []*VersionVector{}, nil
		}
		return nil, err
	}
	defer f.Close()

	var vectors []*VersionVector
	header := make([]byte, 8)

	for {
		if _, err := io.ReadFull(f, header); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}

		nodeID := binary.LittleEndian.Uint32(header[0:4])
		dim := binary.LittleEndian.Uint32(header[4:8])

		vec := make([]float32, dim)
		buf := make([]byte, 4)
		for i := range vec {
			if _, err := io.ReadFull(f, buf); err != nil {
				return nil, err
			}
			vec[i] = float32FromUint32(binary.LittleEndian.Uint32(buf))
		}

		vectors = append(vectors, &VersionVector{
			NodeID: nodeID,
			Vector: vec,
		})
	}

	return vectors, nil
}

// ReadAllFromAncestorChain reads all vectors from the ancestor chain.
// Deduplicates by nodeID (newest version wins).
func (s *VersionVectorStore) ReadAllFromAncestorChain() ([]*VersionVector, error) {
	chain := s.session.GetAncestorChain()
	seen := make(map[uint32]bool)
	var result []*VersionVector

	for _, version := range chain {
		vecs, err := s.ReadAllFromVersion(version)
		if err != nil {
			return nil, err
		}
		for _, vec := range vecs {
			if !seen[vec.NodeID] {
				seen[vec.NodeID] = true
				result = append(result, vec)
			}
		}
	}

	return result, nil
}

// Count returns the number of vectors in HEAD version.
func (s *VersionVectorStore) Count() (int, error) {
	vecs, err := s.ReadAllFromVersion(s.session.Manifest.Head)
	if err != nil {
		return 0, err
	}
	return len(vecs), nil
}

// CountInAncestorChainVectors returns the total unique vectors in ancestor chain.
func (s *VersionVectorStore) CountInAncestorChainVectors() (int, error) {
	vecs, err := s.ReadAllFromAncestorChain()
	if err != nil {
		return 0, err
	}
	return len(vecs), nil
}

func (s *VersionVectorStore) readVectorAtOffset(f *os.File, offset int64) (*VersionVector, error) {
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}

	header := make([]byte, 8)
	if _, err := io.ReadFull(f, header); err != nil {
		return nil, err
	}

	nodeID := binary.LittleEndian.Uint32(header[0:4])
	dim := binary.LittleEndian.Uint32(header[4:8])

	vec := make([]float32, dim)
	buf := make([]byte, 4)
	for i := range vec {
		if _, err := io.ReadFull(f, buf); err != nil {
			return nil, err
		}
		vec[i] = float32FromUint32(binary.LittleEndian.Uint32(buf))
	}

	return &VersionVector{NodeID: nodeID, Vector: vec}, nil
}

func (s *VersionVectorStore) scanForNodeID(f *os.File, targetNodeID uint32) (*VersionVector, error) {
	f.Seek(0, io.SeekStart)
	header := make([]byte, 8)

	for {
		if _, err := io.ReadFull(f, header); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}

		nodeID := binary.LittleEndian.Uint32(header[0:4])
		dim := binary.LittleEndian.Uint32(header[4:8])

		if nodeID == targetNodeID {
			vec := make([]float32, dim)
			buf := make([]byte, 4)
			for i := range vec {
				if _, err := io.ReadFull(f, buf); err != nil {
					return nil, err
				}
				vec[i] = float32FromUint32(binary.LittleEndian.Uint32(buf))
			}
			return &VersionVector{NodeID: nodeID, Vector: vec}, nil
		}

		// Skip this vector's data
		if _, err := f.Seek(int64(dim)*4, io.SeekCurrent); err != nil {
			return nil, err
		}
	}

	return nil, nil
}

// uint32FromFloat32 converts float32 to uint32 bit representation.
func uint32FromFloat32(f float32) uint32 {
	return math.Float32bits(f)
}

// float32FromUint32 converts uint32 bit representation to float32.
func float32FromUint32(u uint32) float32 {
	return math.Float32frombits(u)
}
