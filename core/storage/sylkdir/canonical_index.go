package sylkdir

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// ErrKeyNotFound is returned when a canonical key doesn't exist in the index.
var ErrKeyNotFound = errors.New("sylkdir: canonical key not found")

// CanonicalKeyIndex provides O(log n) lookup from canonical keys to node IDs.
// It supports supersession: when a node is updated, the old key points to the new node ID.
//
// Format examples:
//   - "repo:path/to/file.go:FunctionName:func"
//   - "repo:path/to/file.go:StructName:struct"
//   - "doi:10.1234/paper-id"
//   - "url:https://example.com/doc"
//   - "session:ses_abc:decision:12345"
type CanonicalKeyIndex struct {
	indexPath string
	mu        sync.RWMutex

	// keyToID maps canonical key to current node ID.
	// For superseded nodes, the key points to the newest node.
	keyToID map[string]uint32

	// pathIndex maps file path → {canonical key → nodeID} for O(1) per-file lookups.
	// Maintained in sync with keyToID for all keys with "file:" or "symbol:" prefixes.
	pathIndex map[string]map[string]uint32

	// filePathToID maps file path → nodeID for "file:" keys only.
	// Enables O(1) AllFilePaths enumeration without scanning keyToID.
	filePathToID map[string]uint32

	// dirty tracks if the index needs to be persisted.
	dirty bool
}

// NewCanonicalKeyIndex creates a new canonical key index.
func NewCanonicalKeyIndex(indexPath string) *CanonicalKeyIndex {
	return &CanonicalKeyIndex{
		indexPath:    indexPath,
		keyToID:      make(map[string]uint32),
		pathIndex:    make(map[string]map[string]uint32),
		filePathToID: make(map[string]uint32),
	}
}

// NewCanonicalKeyIndexFromSylkDir creates a CanonicalKeyIndex from a SylkDir.
func NewCanonicalKeyIndexFromSylkDir(sd *SylkDir) *CanonicalKeyIndex {
	return NewCanonicalKeyIndex(sd.GlobalCanonicalIndexPath())
}

// Init initializes the index, loading from disk if present.
func (c *CanonicalKeyIndex) Init() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	indexFile := filepath.Join(c.indexPath, "canonical_keys.idx")
	if _, err := os.Stat(indexFile); os.IsNotExist(err) {
		return nil // No index file yet
	}

	return c.loadIndex(indexFile)
}

// Lookup returns the node ID for a canonical key.
// Returns ErrKeyNotFound if the key doesn't exist.
func (c *CanonicalKeyIndex) Lookup(key string) (uint32, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if id, ok := c.keyToID[key]; ok {
		return id, nil
	}
	return 0, ErrKeyNotFound
}

// Set adds or updates a canonical key to node ID mapping.
// If the key already exists, this handles supersession by updating
// the mapping to point to the new node ID.
func (c *CanonicalKeyIndex) Set(key string, nodeID uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.keyToID[key] = nodeID
	c.addToPathIndex(key, nodeID)
	c.dirty = true
}

// SetIfNotExists adds a mapping only if the key doesn't already exist.
// Returns the existing node ID if the key exists, or the new node ID if set.
// Also returns whether the key was newly set.
func (c *CanonicalKeyIndex) SetIfNotExists(key string, nodeID uint32) (existingID uint32, wasSet bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if existing, ok := c.keyToID[key]; ok {
		return existing, false
	}

	c.keyToID[key] = nodeID
	c.addToPathIndex(key, nodeID)
	c.dirty = true
	return nodeID, true
}

// Delete removes a canonical key from the index.
// This is typically not used since nodes are superseded rather than deleted.
func (c *CanonicalKeyIndex) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.keyToID[key]; ok {
		delete(c.keyToID, key)
		c.removeFromPathIndex(key)
		c.dirty = true
	}
}

// Has returns true if the key exists in the index.
func (c *CanonicalKeyIndex) Has(key string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.keyToID[key]
	return ok
}

// Count returns the number of keys in the index.
func (c *CanonicalKeyIndex) Count() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.keyToID)
}

// Keys returns all canonical keys in sorted order.
func (c *CanonicalKeyIndex) Keys() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	keys := make([]string, 0, len(c.keyToID))
	for k := range c.keyToID {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// IsDirty returns true if the index has unsaved changes.
func (c *CanonicalKeyIndex) IsDirty() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.dirty
}

// Save persists the index to disk.
// Uses a sorted format for efficient on-disk lookup (future optimization).
func (c *CanonicalKeyIndex) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.saveIndex()
}

// saveIndex writes the index to disk (caller holds lock).
func (c *CanonicalKeyIndex) saveIndex() error {
	indexFile := filepath.Join(c.indexPath, "canonical_keys.idx")

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(indexFile), 0755); err != nil {
		return fmt.Errorf("sylkdir: failed to create index directory: %w", err)
	}

	// Sort keys for deterministic output and future binary search
	keys := make([]string, 0, len(c.keyToID))
	for k := range c.keyToID {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Calculate buffer size
	// Format: count(4) + entries(keyLen(2) + key + nodeID(4))
	totalSize := 4
	for _, k := range keys {
		totalSize += 2 + len(k) + 4
	}

	buf := make([]byte, totalSize)

	// Write count
	binary.LittleEndian.PutUint32(buf[0:4], uint32(len(keys)))

	// Write entries
	offset := 4
	for _, key := range keys {
		nodeID := c.keyToID[key]

		// Key length (2 bytes)
		binary.LittleEndian.PutUint16(buf[offset:offset+2], uint16(len(key)))
		offset += 2

		// Key data
		copy(buf[offset:], key)
		offset += len(key)

		// Node ID (4 bytes)
		binary.LittleEndian.PutUint32(buf[offset:offset+4], nodeID)
		offset += 4
	}

	// Write to temp file
	tmpFile := indexFile + ".tmp"
	if err := os.WriteFile(tmpFile, buf, 0644); err != nil {
		return fmt.Errorf("sylkdir: failed to write index: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpFile, indexFile); err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("sylkdir: failed to rename index: %w", err)
	}

	c.dirty = false
	return nil
}

// loadIndex reads the index from disk and rebuilds secondary indexes (caller holds lock).
func (c *CanonicalKeyIndex) loadIndex(indexFile string) error {
	data, err := os.ReadFile(indexFile)
	if err != nil {
		return fmt.Errorf("sylkdir: failed to read index: %w", err)
	}

	if len(data) < 4 {
		return fmt.Errorf("sylkdir: index file too short")
	}

	count := binary.LittleEndian.Uint32(data[0:4])
	c.keyToID = make(map[string]uint32, count)
	c.pathIndex = make(map[string]map[string]uint32, count/2)
	c.filePathToID = make(map[string]uint32, count/4)

	offset := 4
	for i := uint32(0); i < count; i++ {
		key, nodeID, newOffset, err := decodeIndexEntry(data, offset, i)
		if err != nil {
			return err
		}
		offset = newOffset
		c.keyToID[key] = nodeID
		c.addToPathIndex(key, nodeID)
	}

	c.dirty = false
	return nil
}

// decodeIndexEntry decodes a single key-nodeID pair from the index binary format.
func decodeIndexEntry(data []byte, offset int, entryNum uint32) (string, uint32, int, error) {
	if offset+2 > len(data) {
		return "", 0, 0, fmt.Errorf("sylkdir: index truncated at entry %d", entryNum)
	}

	keyLen := int(binary.LittleEndian.Uint16(data[offset : offset+2]))
	offset += 2

	if offset+keyLen+4 > len(data) {
		return "", 0, 0, fmt.Errorf("sylkdir: index truncated at entry %d key", entryNum)
	}

	key := string(data[offset : offset+keyLen])
	offset += keyLen

	nodeID := binary.LittleEndian.Uint32(data[offset : offset+4])
	offset += 4

	return key, nodeID, offset, nil
}

// Close saves the index if dirty and releases resources.
func (c *CanonicalKeyIndex) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.dirty {
		return c.saveIndex()
	}
	return nil
}

// Merge merges another index into this one.
// For duplicate keys, the provided override function determines the winner.
// If override is nil, the new value wins (supersession behavior).
func (c *CanonicalKeyIndex) Merge(other *CanonicalKeyIndex, override func(key string, oldID, newID uint32) uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()

	other.mu.RLock()
	defer other.mu.RUnlock()

	for key, newID := range other.keyToID {
		resolvedID := newID
		if oldID, exists := c.keyToID[key]; exists && override != nil {
			resolvedID = override(key, oldID, newID)
		}
		c.keyToID[key] = resolvedID
		c.addToPathIndex(key, resolvedID)
	}

	if len(other.keyToID) > 0 {
		c.dirty = true
	}
}

// LookupPrefix returns all keys that match the given prefix.
// This is O(n) in the current implementation; could be optimized with a trie.
func (c *CanonicalKeyIndex) LookupPrefix(prefix string) map[string]uint32 {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make(map[string]uint32)
	for key, id := range c.keyToID {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			result[key] = id
		}
	}
	return result
}

// DeletePrefix removes all keys matching the given prefix and returns them.
// This is O(n) in the current implementation for arbitrary prefixes.
func (c *CanonicalKeyIndex) DeletePrefix(prefix string) map[string]uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()

	deleted := make(map[string]uint32)
	for key, id := range c.keyToID {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			deleted[key] = id
			delete(c.keyToID, key)
			c.removeFromPathIndexLocked(key)
		}
	}
	if len(deleted) > 0 {
		c.dirty = true
	}
	return deleted
}

// ── Path-based secondary index ─────────────────────────────────────────────

// extractPathFromKey extracts the file path from a canonical key.
// Returns "" for keys that don't follow "file:<path>" or "symbol:<path>:..." format.
func extractPathFromKey(key string) string {
	if strings.HasPrefix(key, "file:") {
		return key[len("file:"):]
	}
	if strings.HasPrefix(key, "symbol:") {
		return extractSymbolPath(key)
	}
	return ""
}

// extractSymbolPath extracts the file path component from a "symbol:<path>:<name>:<kind>" key.
func extractSymbolPath(key string) string {
	rest := key[len("symbol:"):]
	idx := strings.Index(rest, ":")
	if idx < 0 {
		return ""
	}
	return rest[:idx]
}

// addToPathIndex adds a key to the path-based secondary indexes. Caller holds lock.
func (c *CanonicalKeyIndex) addToPathIndex(key string, nodeID uint32) {
	path := extractPathFromKey(key)
	if path == "" {
		return
	}
	bucket := c.pathIndex[path]
	if bucket == nil {
		bucket = make(map[string]uint32)
		c.pathIndex[path] = bucket
	}
	bucket[key] = nodeID
	if strings.HasPrefix(key, "file:") {
		c.filePathToID[path] = nodeID
	}
}

// removeFromPathIndex removes a key from the path-based secondary indexes. Caller holds lock.
func (c *CanonicalKeyIndex) removeFromPathIndex(key string) {
	c.removeFromPathIndexLocked(key)
}

// removeFromPathIndexLocked removes a key from path indexes. Caller holds lock.
func (c *CanonicalKeyIndex) removeFromPathIndexLocked(key string) {
	path := extractPathFromKey(key)
	if path == "" {
		return
	}
	bucket := c.pathIndex[path]
	if bucket == nil {
		return
	}
	delete(bucket, key)
	if len(bucket) == 0 {
		delete(c.pathIndex, path)
	}
	if strings.HasPrefix(key, "file:") {
		delete(c.filePathToID, path)
	}
}

// LookupByPath returns all canonical keys (file + symbol) associated with a file path.
// O(1) lookup via path index. Returns nil if no keys exist for the path.
func (c *CanonicalKeyIndex) LookupByPath(path string) map[string]uint32 {
	c.mu.RLock()
	defer c.mu.RUnlock()

	bucket := c.pathIndex[path]
	if len(bucket) == 0 {
		return nil
	}
	result := make(map[string]uint32, len(bucket))
	for k, v := range bucket {
		result[k] = v
	}
	return result
}

// AllFilePaths returns a snapshot of all file paths and their node IDs.
// O(n) in the number of file nodes (NOT total keys).
func (c *CanonicalKeyIndex) AllFilePaths() map[string]uint32 {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make(map[string]uint32, len(c.filePathToID))
	for path, id := range c.filePathToID {
		result[path] = id
	}
	return result
}

// Stats returns statistics about the index.
type CanonicalIndexStats struct {
	KeyCount      int
	TotalKeyBytes int64
	IsDirty       bool
}

func (c *CanonicalKeyIndex) Stats() CanonicalIndexStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var totalBytes int64
	for key := range c.keyToID {
		totalBytes += int64(len(key))
	}

	return CanonicalIndexStats{
		KeyCount:      len(c.keyToID),
		TotalKeyBytes: totalBytes,
		IsDirty:       c.dirty,
	}
}
