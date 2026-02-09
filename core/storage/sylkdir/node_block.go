package sylkdir

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Node block storage constants.
const (
	// DefaultNodeBlockSize is the number of nodes per block file.
	DefaultNodeBlockSize = 4096

	// NodeHeaderSize is the fixed header size for binary node format.
	// Layout: ID(4) + Domain(1) + Type(1) + CreatedBy(2) + CreatedAt(8) +
	//         SessionID(4) + DocRef(4) + SupersededBy(4) + Supersedes(4)
	// Total: 32 bytes (cache-line aligned, zero padding)
	NodeHeaderSize = 32

	// MaxStringLen is the maximum length for a variable-length string field.
	MaxStringLen = 65535
)

// ErrNodeNotFound is returned when a node doesn't exist.
var ErrNodeNotFound = errors.New("sylkdir: node not found")

// ErrInvalidNodeData is returned when node data is corrupt.
var ErrInvalidNodeData = errors.New("sylkdir: invalid node data")

// Node represents a node in the knowledge graph with binary serialization support.
type Node struct {
	// Identity
	ID           uint32 // Globally unique
	CanonicalKey string // Unique logical identifier

	// Classification
	Domain   uint8 // Domain enum
	NodeType uint8 // NodeType enum

	// Provenance
	CreatedBy uint16 // Agent ID
	CreatedAt uint64 // Unix nano timestamp
	SessionID uint32 // Session that created this node

	// Document reference (many-to-one: multiple nodes → one document)
	DocRef uint32 // Document ID from DocIDMap (0 = no document)

	// Content
	Name      string // Human-readable name
	Path      string // File path or URL
	Package   string // Package/module
	Signature string // Function signature

	// Supersession chain
	SupersededBy uint32 // ID of newer version (0 = current)
	Supersedes   uint32 // ID of older version (0 = original)
}

// BinarySize returns the serialized size in bytes.
func (n *Node) BinarySize() int {
	return NodeHeaderSize +
		2 + len(n.CanonicalKey) +
		2 + len(n.Name) +
		2 + len(n.Path) +
		2 + len(n.Package) +
		2 + len(n.Signature)
}

// MarshalBinary encodes the node to binary format.
// Format: fixed 32-byte header + variable-length strings (length-prefixed).
func (n *Node) MarshalBinary() ([]byte, error) {
	buf := make([]byte, n.BinarySize())
	n.MarshalBinaryTo(buf)
	return buf, nil
}

// MarshalBinaryTo encodes the node into the provided buffer (must be >= BinarySize()).
func (n *Node) MarshalBinaryTo(buf []byte) {
	// Write fixed header (32 bytes, zero padding)
	binary.LittleEndian.PutUint32(buf[0:4], n.ID)
	buf[4] = n.Domain
	buf[5] = n.NodeType
	binary.LittleEndian.PutUint16(buf[6:8], n.CreatedBy)
	binary.LittleEndian.PutUint64(buf[8:16], n.CreatedAt)
	binary.LittleEndian.PutUint32(buf[16:20], n.SessionID)
	binary.LittleEndian.PutUint32(buf[20:24], n.DocRef)
	binary.LittleEndian.PutUint32(buf[24:28], n.SupersededBy)
	binary.LittleEndian.PutUint32(buf[28:32], n.Supersedes)

	// Write variable-length strings
	offset := NodeHeaderSize
	offset = writeString(buf, offset, n.CanonicalKey)
	offset = writeString(buf, offset, n.Name)
	offset = writeString(buf, offset, n.Path)
	offset = writeString(buf, offset, n.Package)
	writeString(buf, offset, n.Signature)
}

// UnmarshalBinary decodes the node from binary format.
func (n *Node) UnmarshalBinary(data []byte) error {
	if len(data) < NodeHeaderSize {
		return ErrInvalidNodeData
	}

	// Read fixed header (32 bytes, zero padding)
	n.ID = binary.LittleEndian.Uint32(data[0:4])
	n.Domain = data[4]
	n.NodeType = data[5]
	n.CreatedBy = binary.LittleEndian.Uint16(data[6:8])
	n.CreatedAt = binary.LittleEndian.Uint64(data[8:16])
	n.SessionID = binary.LittleEndian.Uint32(data[16:20])
	n.DocRef = binary.LittleEndian.Uint32(data[20:24])
	n.SupersededBy = binary.LittleEndian.Uint32(data[24:28])
	n.Supersedes = binary.LittleEndian.Uint32(data[28:32])

	// Read variable-length strings
	offset := NodeHeaderSize
	var err error

	n.CanonicalKey, offset, err = readString(data, offset)
	if err != nil {
		return err
	}

	n.Name, offset, err = readString(data, offset)
	if err != nil {
		return err
	}

	n.Path, offset, err = readString(data, offset)
	if err != nil {
		return err
	}

	n.Package, offset, err = readString(data, offset)
	if err != nil {
		return err
	}

	n.Signature, _, err = readString(data, offset)
	if err != nil {
		return err
	}

	return nil
}

func writeString(buf []byte, offset int, s string) int {
	binary.LittleEndian.PutUint16(buf[offset:offset+2], uint16(len(s)))
	copy(buf[offset+2:], s)
	return offset + 2 + len(s)
}

func readString(data []byte, offset int) (string, int, error) {
	if offset+2 > len(data) {
		return "", 0, ErrInvalidNodeData
	}
	length := int(binary.LittleEndian.Uint16(data[offset : offset+2]))
	if offset+2+length > len(data) {
		return "", 0, ErrInvalidNodeData
	}
	return string(data[offset+2 : offset+2+length]), offset + 2 + length, nil
}

// NodeBlockStore manages node storage in block files.
// Nodes are organized into blocks of DefaultNodeBlockSize nodes each.
// Each block is stored as a separate file: block_NNNN.bin
type NodeBlockStore struct {
	blocksPath string
	indexPath  string
	blockSize  uint32
	mu         sync.RWMutex

	// nodeIndex maps nodeID to (blockNum, offset, size) for fast lookup.
	nodeIndex map[uint32]nodeLocation
}

type nodeLocation struct {
	blockNum uint32
	offset   int64
	size     uint32
}

// NewNodeBlockStore creates a new node block store.
func NewNodeBlockStore(blocksPath, indexPath string) *NodeBlockStore {
	return &NodeBlockStore{
		blocksPath: blocksPath,
		indexPath:  indexPath,
		blockSize:  DefaultNodeBlockSize,
		nodeIndex:  make(map[uint32]nodeLocation),
	}
}

// NewNodeBlockStoreFromSylkDir creates a NodeBlockStore from a SylkDir.
func NewNodeBlockStoreFromSylkDir(sd *SylkDir) *NodeBlockStore {
	return NewNodeBlockStore(sd.GlobalNodeBlocksPath(), sd.GlobalCanonicalIndexPath())
}

// Init initializes the node block store, loading existing index if present.
func (s *NodeBlockStore) Init() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Load index if it exists
	indexFile := filepath.Join(s.indexPath, "nodes.idx")
	if _, err := os.Stat(indexFile); err == nil {
		return s.loadIndex(indexFile)
	}

	return nil
}

// Write writes a node to the appropriate block file.
// Returns the block number and offset where the node was written.
func (s *NodeBlockStore) Write(node *Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Serialize node
	data, err := node.MarshalBinary()
	if err != nil {
		return fmt.Errorf("sylkdir: failed to marshal node: %w", err)
	}

	// Determine block number
	blockNum := node.ID / s.blockSize
	blockPath := s.blockPath(blockNum)

	// Ensure block directory exists
	if err := os.MkdirAll(filepath.Dir(blockPath), 0755); err != nil {
		return fmt.Errorf("sylkdir: failed to create block directory: %w", err)
	}

	// Open/create block file
	f, err := os.OpenFile(blockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("sylkdir: failed to open block file: %w", err)
	}
	defer f.Close()

	// Seek to end for append
	offset, err := f.Seek(0, 2)
	if err != nil {
		return fmt.Errorf("sylkdir: failed to seek: %w", err)
	}

	// Write size prefix (4 bytes) + data
	sizeBuf := make([]byte, 4)
	binary.LittleEndian.PutUint32(sizeBuf, uint32(len(data)))
	if _, err := f.Write(sizeBuf); err != nil {
		return fmt.Errorf("sylkdir: failed to write size: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("sylkdir: failed to write data: %w", err)
	}

	// Update in-memory index
	s.nodeIndex[node.ID] = nodeLocation{
		blockNum: blockNum,
		offset:   offset,
		size:     uint32(len(data)),
	}

	return nil
}

// Read reads a node by ID.
func (s *NodeBlockStore) Read(nodeID uint32) (*Node, error) {
	s.mu.RLock()
	loc, ok := s.nodeIndex[nodeID]
	s.mu.RUnlock()

	if !ok {
		return nil, ErrNodeNotFound
	}

	blockPath := s.blockPath(loc.blockNum)
	f, err := os.Open(blockPath)
	if err != nil {
		return nil, fmt.Errorf("sylkdir: failed to open block: %w", err)
	}
	defer f.Close()

	// Seek to node position
	if _, err := f.Seek(loc.offset, 0); err != nil {
		return nil, fmt.Errorf("sylkdir: failed to seek: %w", err)
	}

	// Read size prefix
	sizeBuf := make([]byte, 4)
	if _, err := f.Read(sizeBuf); err != nil {
		return nil, fmt.Errorf("sylkdir: failed to read size: %w", err)
	}
	size := binary.LittleEndian.Uint32(sizeBuf)

	// Read node data
	data := make([]byte, size)
	if _, err := f.Read(data); err != nil {
		return nil, fmt.Errorf("sylkdir: failed to read data: %w", err)
	}

	// Unmarshal
	node := &Node{}
	if err := node.UnmarshalBinary(data); err != nil {
		return nil, err
	}

	return node, nil
}

// ReadBatch reads multiple nodes by ID.
// Returns nodes in the same order as input IDs.
// Missing nodes are returned as nil in the result slice.
func (s *NodeBlockStore) ReadBatch(nodeIDs []uint32) ([]*Node, error) {
	result := make([]*Node, len(nodeIDs))

	// Group by block for efficient I/O
	blockReads := make(map[uint32][]int) // blockNum -> indices into nodeIDs
	s.mu.RLock()
	for i, id := range nodeIDs {
		if loc, ok := s.nodeIndex[id]; ok {
			blockReads[loc.blockNum] = append(blockReads[loc.blockNum], i)
		}
	}
	s.mu.RUnlock()

	// Read each block once
	for blockNum, indices := range blockReads {
		blockPath := s.blockPath(blockNum)
		f, err := os.Open(blockPath)
		if err != nil {
			continue // Skip unreadable blocks
		}

		for _, i := range indices {
			id := nodeIDs[i]
			s.mu.RLock()
			loc := s.nodeIndex[id]
			s.mu.RUnlock()

			if _, err := f.Seek(loc.offset, 0); err != nil {
				continue
			}

			sizeBuf := make([]byte, 4)
			if _, err := f.Read(sizeBuf); err != nil {
				continue
			}
			size := binary.LittleEndian.Uint32(sizeBuf)

			data := make([]byte, size)
			if _, err := f.Read(data); err != nil {
				continue
			}

			node := &Node{}
			if err := node.UnmarshalBinary(data); err == nil {
				result[i] = node
			}
		}

		f.Close()
	}

	return result, nil
}

// Count returns the number of nodes in the store.
func (s *NodeBlockStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.nodeIndex)
}

// Exists returns true if a node with the given ID exists.
func (s *NodeBlockStore) Exists(nodeID uint32) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.nodeIndex[nodeID]
	return ok
}

// SaveIndex persists the in-memory index to disk.
func (s *NodeBlockStore) SaveIndex() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	indexFile := filepath.Join(s.indexPath, "nodes.idx")

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(indexFile), 0755); err != nil {
		return fmt.Errorf("sylkdir: failed to create index directory: %w", err)
	}

	// Write to temp file
	tmpFile := indexFile + ".tmp"
	f, err := os.Create(tmpFile)
	if err != nil {
		return fmt.Errorf("sylkdir: failed to create index file: %w", err)
	}

	// Format: count(4) + entries(nodeID(4) + blockNum(4) + offset(8) + size(4) = 20 bytes each)
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, uint32(len(s.nodeIndex)))
	if _, err := f.Write(buf); err != nil {
		f.Close()
		os.Remove(tmpFile)
		return fmt.Errorf("sylkdir: failed to write index count: %w", err)
	}

	entryBuf := make([]byte, 20)
	for nodeID, loc := range s.nodeIndex {
		binary.LittleEndian.PutUint32(entryBuf[0:4], nodeID)
		binary.LittleEndian.PutUint32(entryBuf[4:8], loc.blockNum)
		binary.LittleEndian.PutUint64(entryBuf[8:16], uint64(loc.offset))
		binary.LittleEndian.PutUint32(entryBuf[16:20], loc.size)
		if _, err := f.Write(entryBuf); err != nil {
			f.Close()
			os.Remove(tmpFile)
			return fmt.Errorf("sylkdir: failed to write index entry: %w", err)
		}
	}

	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmpFile)
		return fmt.Errorf("sylkdir: failed to sync index: %w", err)
	}

	if err := f.Close(); err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("sylkdir: failed to close index: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpFile, indexFile); err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("sylkdir: failed to rename index: %w", err)
	}

	return nil
}

// loadIndex loads the index from disk.
func (s *NodeBlockStore) loadIndex(indexFile string) error {
	data, err := os.ReadFile(indexFile)
	if err != nil {
		return fmt.Errorf("sylkdir: failed to read index: %w", err)
	}

	if len(data) < 4 {
		return ErrInvalidNodeData
	}

	count := binary.LittleEndian.Uint32(data[0:4])
	expectedSize := 4 + int(count)*20
	if len(data) < expectedSize {
		return ErrInvalidNodeData
	}

	s.nodeIndex = make(map[uint32]nodeLocation, count)
	offset := 4
	for i := uint32(0); i < count; i++ {
		nodeID := binary.LittleEndian.Uint32(data[offset : offset+4])
		blockNum := binary.LittleEndian.Uint32(data[offset+4 : offset+8])
		locOffset := int64(binary.LittleEndian.Uint64(data[offset+8 : offset+16]))
		size := binary.LittleEndian.Uint32(data[offset+16 : offset+20])

		s.nodeIndex[nodeID] = nodeLocation{
			blockNum: blockNum,
			offset:   locOffset,
			size:     size,
		}
		offset += 20
	}

	return nil
}

func (s *NodeBlockStore) blockPath(blockNum uint32) string {
	return filepath.Join(s.blocksPath, fmt.Sprintf("block_%04d.bin", blockNum))
}

// Close closes the node block store and persists the index.
func (s *NodeBlockStore) Close() error {
	return s.SaveIndex()
}
