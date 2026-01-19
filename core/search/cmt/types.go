// Package cmt implements a Cartesian Merkle Tree for file manifest tracking.
// It provides O(log n) operations with cryptographic integrity verification.
//
// A Cartesian Merkle Tree (CMT) combines three properties:
//   - BST property: Keys are ordered (file paths in lexicographic order)
//   - Heap property: Priorities ensure balanced tree (randomized via SHAKE-128)
//   - Merkle hashes: Each node contains hash of its subtree for integrity verification
package cmt

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"time"

	"golang.org/x/crypto/sha3"
)

// =============================================================================
// Constants
// =============================================================================

// HashSize is the size of SHA-256 hashes in bytes.
const HashSize = 32

// =============================================================================
// Hash Type
// =============================================================================

// Hash represents a SHA-256 hash.
type Hash [HashSize]byte

// IsZero returns true if the hash is all zeros.
func (h Hash) IsZero() bool {
	for _, b := range h {
		if b != 0 {
			return false
		}
	}
	return true
}

// String returns the hexadecimal representation of the hash.
func (h Hash) String() string {
	return hex.EncodeToString(h[:])
}

// =============================================================================
// FileInfo
// =============================================================================

// FileInfo contains metadata about an indexed file.
type FileInfo struct {
	Path        string    `json:"path"`
	ContentHash Hash      `json:"content_hash"` // SHA-256 of file content
	Size        int64     `json:"size"`
	ModTime     time.Time `json:"mod_time"`
	Permissions uint32    `json:"permissions"` // Unix file mode
	Indexed     bool      `json:"indexed"`     // Whether file has been indexed
	IndexedAt   time.Time `json:"indexed_at"`
}

// Validation errors for FileInfo.
var (
	ErrFileInfoPathEmpty        = errors.New("file info path cannot be empty")
	ErrFileInfoContentHashZero  = errors.New("file info content hash cannot be zero")
	ErrFileInfoSizeNegative     = errors.New("file info size cannot be negative")
	ErrFileInfoModTimeZero      = errors.New("file info mod time cannot be zero")
)

// Validate checks that the FileInfo has valid required fields.
func (f *FileInfo) Validate() error {
	if f.Path == "" {
		return ErrFileInfoPathEmpty
	}
	if f.ContentHash.IsZero() {
		return ErrFileInfoContentHashZero
	}
	if f.Size < 0 {
		return ErrFileInfoSizeNegative
	}
	if f.ModTime.IsZero() {
		return ErrFileInfoModTimeZero
	}
	return nil
}

// Clone creates a deep copy of the FileInfo.
func (f *FileInfo) Clone() *FileInfo {
	if f == nil {
		return nil
	}
	return &FileInfo{
		Path:        f.Path,
		ContentHash: f.ContentHash,
		Size:        f.Size,
		ModTime:     f.ModTime,
		Permissions: f.Permissions,
		Indexed:     f.Indexed,
		IndexedAt:   f.IndexedAt,
	}
}

// =============================================================================
// Node
// =============================================================================

// Node represents a node in the Cartesian Merkle Tree.
type Node struct {
	Key      string    // File path (BST ordering)
	Priority uint64    // SHAKE-128 derived priority (heap ordering)
	Left     *Node     // Left child (keys < Key)
	Right    *Node     // Right child (keys > Key)
	Hash     Hash      // Merkle hash of this subtree
	Info     *FileInfo // File metadata
}

// NewNode creates a new node with the given key and file info.
// Priority is computed from the key using SHAKE-128.
func NewNode(key string, info *FileInfo) *Node {
	return &Node{
		Key:      key,
		Priority: ComputePriority(key),
		Info:     info,
	}
}

// ComputePriority computes a deterministic priority from a key using SHAKE-128.
// The priority provides pseudo-random balancing for the treap structure.
func ComputePriority(key string) uint64 {
	shake := sha3.NewShake128()
	shake.Write([]byte(key))
	var buf [8]byte
	shake.Read(buf[:])
	return binary.BigEndian.Uint64(buf[:])
}

// IsLeaf returns true if the node has no children.
// A nil node is considered a leaf (vacuously true).
func (n *Node) IsLeaf() bool {
	if n == nil {
		return true
	}
	return n.Left == nil && n.Right == nil
}

// Size returns the number of nodes in the subtree rooted at this node.
// A nil node has size 0.
func (n *Node) Size() int64 {
	if n == nil {
		return 0
	}
	return 1 + n.Left.Size() + n.Right.Size()
}
