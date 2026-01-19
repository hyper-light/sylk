// Package cmt provides a Cartesian Merkle Tree for file manifest tracking.
// This file implements the CMT operations with thread-safe access and WAL logging.
package cmt

import (
	"sync"
)

// =============================================================================
// CMT Configuration
// =============================================================================

// CMTConfig configures the Cartesian Merkle Tree.
type CMTConfig struct {
	// WALPath is the path to the write-ahead log file.
	WALPath string

	// StoragePath is the path for persistent SQLite storage (optional).
	StoragePath string

	// SyncOnWrite enables fsync after each WAL write.
	SyncOnWrite bool
}

// =============================================================================
// Storage Interface
// =============================================================================

// Storage interface for persistent CMT storage.
type Storage interface {
	Save(root *Node) error
	Load() (*Node, error)
	Close() error
}

// =============================================================================
// CMT Struct
// =============================================================================

// CMT is a Cartesian Merkle Tree for file manifest tracking.
// Thread-safe with RWMutex. All mutations are logged to WAL.
type CMT struct {
	root    *Node
	size    int64
	wal     *WAL
	storage Storage // Optional, can be nil
	mu      sync.RWMutex
}

// =============================================================================
// Constructor Functions
// =============================================================================

// NewCMT creates a new CMT with the given configuration.
// Returns an error if WAL creation fails.
func NewCMT(config CMTConfig) (*CMT, error) {
	wal, err := NewWAL(WALConfig{
		Path:        config.WALPath,
		SyncOnWrite: config.SyncOnWrite,
	})
	if err != nil {
		return nil, err
	}

	return &CMT{
		root:    nil,
		size:    0,
		wal:     wal,
		storage: nil,
	}, nil
}

// NewCMTWithWAL creates a CMT with an existing WAL.
// Useful for testing or when WAL is managed externally.
func NewCMTWithWAL(wal *WAL) *CMT {
	return &CMT{
		root:    nil,
		size:    0,
		wal:     wal,
		storage: nil,
	}
}

// =============================================================================
// Insert Operation
// =============================================================================

// Insert adds or updates a file entry.
// Logs to WAL before modifying tree.
// Returns error if key is empty, info is nil, or WAL logging fails.
func (c *CMT) Insert(key string, info *FileInfo) error {
	if err := c.logInsert(key, info); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.insertInternal(key, info)
	return nil
}

// logInsert logs an insert operation to WAL.
func (c *CMT) logInsert(key string, info *FileInfo) error {
	return c.wal.LogInsert(key, info)
}

// insertInternal performs the tree insertion without locking.
// Must be called with write lock held.
// This implements upsert behavior by first deleting any existing entry.
func (c *CMT) insertInternal(key string, info *FileInfo) {
	existed := c.keyExists(key)

	// Delete existing entry if present (upsert behavior)
	if existed {
		c.root = Delete(c.root, key)
	}

	newNode := NewNode(key, info.Clone())
	c.root = Insert(c.root, newNode)

	if !existed {
		c.size++
	}
}

// keyExists checks if a key exists in the tree.
// Must be called with lock held.
func (c *CMT) keyExists(key string) bool {
	return Get(c.root, key) != nil
}

// =============================================================================
// Delete Operation
// =============================================================================

// Delete removes a file entry by key.
// Logs to WAL before modifying tree.
// Returns error if key is empty or WAL logging fails.
// Deleting a non-existent key is a no-op (succeeds silently).
func (c *CMT) Delete(key string) error {
	if err := c.logDelete(key); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.deleteInternal(key)
	return nil
}

// logDelete logs a delete operation to WAL.
func (c *CMT) logDelete(key string) error {
	return c.wal.LogDelete(key)
}

// deleteInternal performs the tree deletion without locking.
// Must be called with write lock held.
func (c *CMT) deleteInternal(key string) {
	existed := c.keyExists(key)

	c.root = Delete(c.root, key)

	if existed {
		c.size--
	}
}

// =============================================================================
// Get Operation
// =============================================================================

// Get retrieves a file entry by key.
// Returns nil if not found.
// Returns a clone to prevent external mutation.
func (c *CMT) Get(key string) *FileInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	node := Get(c.root, key)
	if node == nil {
		return nil
	}
	return node.Info.Clone()
}

// =============================================================================
// Update Operation
// =============================================================================

// Update updates an existing file entry.
// This is equivalent to Insert (upsert behavior).
func (c *CMT) Update(key string, info *FileInfo) error {
	return c.Insert(key, info)
}

// =============================================================================
// RootHash Operation
// =============================================================================

// RootHash returns the current root Merkle hash.
// Returns zero hash if tree is empty.
// O(1) operation - returns stored hash.
func (c *CMT) RootHash() Hash {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.root == nil {
		return Hash{}
	}
	return c.root.Hash
}

// =============================================================================
// Size Operation
// =============================================================================

// Size returns the number of entries in the tree.
func (c *CMT) Size() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.size
}

// =============================================================================
// Walk Operation
// =============================================================================

// Walk performs an in-order traversal of the tree.
// The callback receives each node's key and FileInfo.
// Return false from callback to stop iteration.
func (c *CMT) Walk(fn func(key string, info *FileInfo) bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	walkInOrder(c.root, fn)
}

// walkInOrder performs recursive in-order traversal.
// Returns false if traversal should stop.
func walkInOrder(node *Node, fn func(key string, info *FileInfo) bool) bool {
	if node == nil {
		return true
	}

	if !walkInOrder(node.Left, fn) {
		return false
	}

	if !fn(node.Key, node.Info) {
		return false
	}

	return walkInOrder(node.Right, fn)
}

// =============================================================================
// Checkpoint Operation
// =============================================================================

// Checkpoint creates a checkpoint marker in the WAL.
func (c *CMT) Checkpoint() error {
	return c.wal.Checkpoint()
}

// =============================================================================
// Recover Operation
// =============================================================================

// Recover replays WAL entries to rebuild the tree.
// This should be called on a fresh CMT to restore state.
func (c *CMT) Recover() error {
	entries, err := c.wal.Recover()
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.replayEntries(entries)
	return nil
}

// replayEntries replays WAL entries to rebuild tree state.
// Must be called with write lock held.
func (c *CMT) replayEntries(entries []WALEntry) {
	for i := range entries {
		c.replayEntry(&entries[i])
	}
}

// replayEntry replays a single WAL entry.
// Must be called with write lock held.
func (c *CMT) replayEntry(entry *WALEntry) {
	switch entry.Op {
	case WALOpInsert:
		c.insertInternal(entry.Key, entry.Info)
	case WALOpDelete:
		c.deleteInternal(entry.Key)
	// Checkpoint entries are markers, no action needed
	case WALOpCheckpoint, WALOpTruncate:
		// No-op
	}
}

// =============================================================================
// Close Operation
// =============================================================================

// Close syncs the WAL and closes all resources.
func (c *CMT) Close() error {
	return c.wal.Close()
}
