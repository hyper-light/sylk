package cmt

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// Test Helpers
// =============================================================================

// createTestSQLiteStorage creates a temporary SQLite storage for testing.
func createTestSQLiteStorage(t *testing.T) (*SQLiteStorage, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	storage, err := NewSQLiteStorage(path)
	if err != nil {
		t.Fatalf("NewSQLiteStorage failed: %v", err)
	}

	if err := storage.Open(); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	return storage, path
}

// createTestNode creates a Node with FileInfo for testing.
func createTestNode(key string) *Node {
	info := &FileInfo{
		Path:        key,
		ContentHash: Hash{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32},
		Size:        1024,
		ModTime:     time.Now().Truncate(time.Second),
		Permissions: 0644,
		Indexed:     true,
		IndexedAt:   time.Now().Truncate(time.Second),
	}
	return NewNode(key, info)
}

// createTestTree creates a tree with the given keys.
func createTestTree(keys []string) *Node {
	var root *Node
	for _, key := range keys {
		node := createTestNode(key)
		root = Insert(root, node)
	}
	return root
}

// collectTreeKeys collects all keys from a tree in sorted order.
func collectTreeKeys(root *Node) []string {
	var keys []string
	collectKeysRecursive(root, &keys)
	return keys
}

// collectKeysRecursive is a helper for collectTreeKeys.
func collectKeysRecursive(node *Node, keys *[]string) {
	if node == nil {
		return
	}
	collectKeysRecursive(node.Left, keys)
	*keys = append(*keys, node.Key)
	collectKeysRecursive(node.Right, keys)
}

// =============================================================================
// NewSQLiteStorage Tests
// =============================================================================

func TestNewSQLiteStorage(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	storage, err := NewSQLiteStorage(path)
	if err != nil {
		t.Fatalf("NewSQLiteStorage failed: %v", err)
	}

	if storage == nil {
		t.Fatal("NewSQLiteStorage returned nil")
	}

	if storage.path != path {
		t.Errorf("path = %q, want %q", storage.path, path)
	}
}

func TestNewSQLiteStorage_EmptyPath(t *testing.T) {
	t.Parallel()

	_, err := NewSQLiteStorage("")
	if err != ErrStoragePathEmpty {
		t.Errorf("NewSQLiteStorage error = %v, want %v", err, ErrStoragePathEmpty)
	}
}

// =============================================================================
// Open Tests
// =============================================================================

func TestSQLiteStorage_Open(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	storage, err := NewSQLiteStorage(path)
	if err != nil {
		t.Fatalf("NewSQLiteStorage failed: %v", err)
	}

	if err := storage.Open(); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer storage.Close()

	// Database file should exist
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("database file should exist after Open")
	}
}

func TestSQLiteStorage_Open_ExistingDatabase(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	// Create and close first storage
	storage1, err := NewSQLiteStorage(path)
	if err != nil {
		t.Fatalf("NewSQLiteStorage failed: %v", err)
	}

	if err := storage1.Open(); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Save some data
	node := createTestNode("test/file.go")
	if err := storage1.SaveNode(node); err != nil {
		t.Fatalf("SaveNode failed: %v", err)
	}

	if err := storage1.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Open existing database
	storage2, err := NewSQLiteStorage(path)
	if err != nil {
		t.Fatalf("NewSQLiteStorage (reopen) failed: %v", err)
	}

	if err := storage2.Open(); err != nil {
		t.Fatalf("Open (reopen) failed: %v", err)
	}
	defer storage2.Close()

	// Data should persist
	count, err := storage2.GetNodeCount()
	if err != nil {
		t.Fatalf("GetNodeCount failed: %v", err)
	}

	if count != 1 {
		t.Errorf("GetNodeCount = %d, want 1", count)
	}
}

func TestSQLiteStorage_Open_InvalidPath(t *testing.T) {
	t.Parallel()

	storage, err := NewSQLiteStorage("/nonexistent/directory/test.db")
	if err != nil {
		t.Fatalf("NewSQLiteStorage failed: %v", err)
	}

	err = storage.Open()
	if err == nil {
		storage.Close()
		t.Error("Open should fail for invalid path")
	}
}

func TestSQLiteStorage_Open_AlreadyOpen(t *testing.T) {
	t.Parallel()

	storage, _ := createTestSQLiteStorage(t)
	defer storage.Close()

	// Second Open should return error
	err := storage.Open()
	if err != ErrStorageAlreadyOpen {
		t.Errorf("Open error = %v, want %v", err, ErrStorageAlreadyOpen)
	}
}

// =============================================================================
// Close Tests
// =============================================================================

func TestSQLiteStorage_Close(t *testing.T) {
	t.Parallel()

	storage, _ := createTestSQLiteStorage(t)

	if err := storage.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Double close should not error
	if err := storage.Close(); err != nil {
		t.Errorf("second Close failed: %v", err)
	}
}

func TestSQLiteStorage_Close_NotOpen(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	storage, err := NewSQLiteStorage(path)
	if err != nil {
		t.Fatalf("NewSQLiteStorage failed: %v", err)
	}

	// Close without Open should not error
	if err := storage.Close(); err != nil {
		t.Errorf("Close (not open) failed: %v", err)
	}
}

// =============================================================================
// Save Tests
// =============================================================================

func TestSQLiteStorage_Save_EmptyTree(t *testing.T) {
	t.Parallel()

	storage, _ := createTestSQLiteStorage(t)
	defer storage.Close()

	// Save nil (empty tree)
	if err := storage.Save(nil); err != nil {
		t.Errorf("Save(nil) failed: %v", err)
	}

	// Should have no nodes
	count, err := storage.GetNodeCount()
	if err != nil {
		t.Fatalf("GetNodeCount failed: %v", err)
	}

	if count != 0 {
		t.Errorf("GetNodeCount = %d, want 0", count)
	}
}

func TestSQLiteStorage_Save_SingleNode(t *testing.T) {
	t.Parallel()

	storage, _ := createTestSQLiteStorage(t)
	defer storage.Close()

	root := createTestNode("src/main.go")

	if err := storage.Save(root); err != nil {
		t.Errorf("Save failed: %v", err)
	}

	count, err := storage.GetNodeCount()
	if err != nil {
		t.Fatalf("GetNodeCount failed: %v", err)
	}

	if count != 1 {
		t.Errorf("GetNodeCount = %d, want 1", count)
	}
}

func TestSQLiteStorage_Save_MultipleNodes(t *testing.T) {
	t.Parallel()

	storage, _ := createTestSQLiteStorage(t)
	defer storage.Close()

	keys := []string{"a.go", "b.go", "c.go", "d.go", "e.go"}
	root := createTestTree(keys)

	if err := storage.Save(root); err != nil {
		t.Errorf("Save failed: %v", err)
	}

	count, err := storage.GetNodeCount()
	if err != nil {
		t.Fatalf("GetNodeCount failed: %v", err)
	}

	if count != int64(len(keys)) {
		t.Errorf("GetNodeCount = %d, want %d", count, len(keys))
	}
}

func TestSQLiteStorage_Save_ReplacesExisting(t *testing.T) {
	t.Parallel()

	storage, _ := createTestSQLiteStorage(t)
	defer storage.Close()

	// Save initial tree
	keys1 := []string{"a.go", "b.go", "c.go"}
	root1 := createTestTree(keys1)
	if err := storage.Save(root1); err != nil {
		t.Fatalf("Save (first) failed: %v", err)
	}

	// Save different tree
	keys2 := []string{"x.go", "y.go"}
	root2 := createTestTree(keys2)
	if err := storage.Save(root2); err != nil {
		t.Fatalf("Save (second) failed: %v", err)
	}

	// Should only have new tree nodes
	count, err := storage.GetNodeCount()
	if err != nil {
		t.Fatalf("GetNodeCount failed: %v", err)
	}

	if count != int64(len(keys2)) {
		t.Errorf("GetNodeCount = %d, want %d", count, len(keys2))
	}
}

func TestSQLiteStorage_Save_LargeTree(t *testing.T) {
	t.Parallel()

	storage, _ := createTestSQLiteStorage(t)
	defer storage.Close()

	// Create tree with 1000 nodes using unique keys
	keys := make([]string, 1000)
	for i := range keys {
		keys[i] = filepath.Join("src", "pkg", "file"+string(rune('a'+i/100))+string(rune('a'+i/10%10))+string(rune('a'+i%10))+".go")
	}
	root := createTestTree(keys)

	if err := storage.Save(root); err != nil {
		t.Errorf("Save (large tree) failed: %v", err)
	}

	count, err := storage.GetNodeCount()
	if err != nil {
		t.Fatalf("GetNodeCount failed: %v", err)
	}

	if count != int64(len(keys)) {
		t.Errorf("GetNodeCount = %d, want %d", count, len(keys))
	}
}

func TestSQLiteStorage_Save_NotOpen(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	storage, err := NewSQLiteStorage(path)
	if err != nil {
		t.Fatalf("NewSQLiteStorage failed: %v", err)
	}

	root := createTestNode("test.go")
	err = storage.Save(root)
	if err != ErrStorageNotOpen {
		t.Errorf("Save error = %v, want %v", err, ErrStorageNotOpen)
	}
}

// =============================================================================
// Load Tests
// =============================================================================

func TestSQLiteStorage_Load_Empty(t *testing.T) {
	t.Parallel()

	storage, _ := createTestSQLiteStorage(t)
	defer storage.Close()

	root, err := storage.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if root != nil {
		t.Error("Load on empty storage should return nil")
	}
}

func TestSQLiteStorage_Load_SingleNode(t *testing.T) {
	t.Parallel()

	storage, _ := createTestSQLiteStorage(t)
	defer storage.Close()

	// Save a node
	original := createTestNode("src/main.go")
	if err := storage.SaveNode(original); err != nil {
		t.Fatalf("SaveNode failed: %v", err)
	}

	// Load tree
	root, err := storage.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if root == nil {
		t.Fatal("Load returned nil, expected node")
	}

	if root.Key != original.Key {
		t.Errorf("Key = %q, want %q", root.Key, original.Key)
	}
}

func TestSQLiteStorage_Load_ReconstructsTree(t *testing.T) {
	t.Parallel()

	storage, _ := createTestSQLiteStorage(t)
	defer storage.Close()

	// Create and save tree
	keys := []string{"d.go", "b.go", "f.go", "a.go", "c.go", "e.go", "g.go"}
	original := createTestTree(keys)
	if err := storage.Save(original); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Load tree
	loaded, err := storage.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded == nil {
		t.Fatal("Load returned nil")
	}

	// Verify all keys present in BST order
	loadedKeys := collectTreeKeys(loaded)
	originalKeys := collectTreeKeys(original)

	if len(loadedKeys) != len(originalKeys) {
		t.Fatalf("loaded %d keys, want %d", len(loadedKeys), len(originalKeys))
	}

	for i, key := range loadedKeys {
		if key != originalKeys[i] {
			t.Errorf("key[%d] = %q, want %q", i, key, originalKeys[i])
		}
	}
}

func TestSQLiteStorage_Load_PreservesFileInfo(t *testing.T) {
	t.Parallel()

	storage, _ := createTestSQLiteStorage(t)
	defer storage.Close()

	// Create node with specific FileInfo
	originalInfo := &FileInfo{
		Path:        "src/main.go",
		ContentHash: Hash{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32},
		Size:        4096,
		ModTime:     time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
		Permissions: 0755,
		Indexed:     true,
		IndexedAt:   time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC),
	}
	original := NewNode("src/main.go", originalInfo)

	if err := storage.Save(original); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Load and verify
	loaded, err := storage.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded == nil || loaded.Info == nil {
		t.Fatal("Load returned nil node or info")
	}

	info := loaded.Info
	if info.Path != originalInfo.Path {
		t.Errorf("Path = %q, want %q", info.Path, originalInfo.Path)
	}
	if info.ContentHash != originalInfo.ContentHash {
		t.Error("ContentHash mismatch")
	}
	if info.Size != originalInfo.Size {
		t.Errorf("Size = %d, want %d", info.Size, originalInfo.Size)
	}
	if !info.ModTime.Equal(originalInfo.ModTime) {
		t.Errorf("ModTime = %v, want %v", info.ModTime, originalInfo.ModTime)
	}
	if info.Permissions != originalInfo.Permissions {
		t.Errorf("Permissions = %o, want %o", info.Permissions, originalInfo.Permissions)
	}
	if info.Indexed != originalInfo.Indexed {
		t.Errorf("Indexed = %v, want %v", info.Indexed, originalInfo.Indexed)
	}
	if !info.IndexedAt.Equal(originalInfo.IndexedAt) {
		t.Errorf("IndexedAt = %v, want %v", info.IndexedAt, originalInfo.IndexedAt)
	}
}

func TestSQLiteStorage_Load_NotOpen(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	storage, err := NewSQLiteStorage(path)
	if err != nil {
		t.Fatalf("NewSQLiteStorage failed: %v", err)
	}

	_, err = storage.Load()
	if err != ErrStorageNotOpen {
		t.Errorf("Load error = %v, want %v", err, ErrStorageNotOpen)
	}
}

// =============================================================================
// SaveNode Tests
// =============================================================================

func TestSQLiteStorage_SaveNode(t *testing.T) {
	t.Parallel()

	storage, _ := createTestSQLiteStorage(t)
	defer storage.Close()

	node := createTestNode("src/main.go")

	if err := storage.SaveNode(node); err != nil {
		t.Errorf("SaveNode failed: %v", err)
	}

	count, err := storage.GetNodeCount()
	if err != nil {
		t.Fatalf("GetNodeCount failed: %v", err)
	}

	if count != 1 {
		t.Errorf("GetNodeCount = %d, want 1", count)
	}
}

func TestSQLiteStorage_SaveNode_NilNode(t *testing.T) {
	t.Parallel()

	storage, _ := createTestSQLiteStorage(t)
	defer storage.Close()

	err := storage.SaveNode(nil)
	if err != ErrStorageNilNode {
		t.Errorf("SaveNode error = %v, want %v", err, ErrStorageNilNode)
	}
}

func TestSQLiteStorage_SaveNode_Updates(t *testing.T) {
	t.Parallel()

	storage, _ := createTestSQLiteStorage(t)
	defer storage.Close()

	// Save initial node
	node := createTestNode("src/main.go")
	if err := storage.SaveNode(node); err != nil {
		t.Fatalf("SaveNode failed: %v", err)
	}

	// Update with new info
	node.Info.Size = 8192
	node.Info.Indexed = false
	if err := storage.SaveNode(node); err != nil {
		t.Fatalf("SaveNode (update) failed: %v", err)
	}

	// Load and verify update
	loaded, err := storage.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded.Info.Size != 8192 {
		t.Errorf("Size = %d, want 8192", loaded.Info.Size)
	}
	if loaded.Info.Indexed {
		t.Error("Indexed should be false after update")
	}

	// Should still be only one node
	count, _ := storage.GetNodeCount()
	if count != 1 {
		t.Errorf("GetNodeCount = %d, want 1", count)
	}
}

func TestSQLiteStorage_SaveNode_MultipleNodes(t *testing.T) {
	t.Parallel()

	storage, _ := createTestSQLiteStorage(t)
	defer storage.Close()

	keys := []string{"a.go", "b.go", "c.go", "d.go", "e.go"}
	for _, key := range keys {
		node := createTestNode(key)
		if err := storage.SaveNode(node); err != nil {
			t.Errorf("SaveNode(%s) failed: %v", key, err)
		}
	}

	count, err := storage.GetNodeCount()
	if err != nil {
		t.Fatalf("GetNodeCount failed: %v", err)
	}

	if count != int64(len(keys)) {
		t.Errorf("GetNodeCount = %d, want %d", count, len(keys))
	}
}

func TestSQLiteStorage_SaveNode_NotOpen(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	storage, err := NewSQLiteStorage(path)
	if err != nil {
		t.Fatalf("NewSQLiteStorage failed: %v", err)
	}

	node := createTestNode("test.go")
	err = storage.SaveNode(node)
	if err != ErrStorageNotOpen {
		t.Errorf("SaveNode error = %v, want %v", err, ErrStorageNotOpen)
	}
}

// =============================================================================
// DeleteNode Tests
// =============================================================================

func TestSQLiteStorage_DeleteNode(t *testing.T) {
	t.Parallel()

	storage, _ := createTestSQLiteStorage(t)
	defer storage.Close()

	// Save a node
	node := createTestNode("src/main.go")
	if err := storage.SaveNode(node); err != nil {
		t.Fatalf("SaveNode failed: %v", err)
	}

	// Delete it
	if err := storage.DeleteNode("src/main.go"); err != nil {
		t.Errorf("DeleteNode failed: %v", err)
	}

	count, err := storage.GetNodeCount()
	if err != nil {
		t.Fatalf("GetNodeCount failed: %v", err)
	}

	if count != 0 {
		t.Errorf("GetNodeCount = %d, want 0", count)
	}
}

func TestSQLiteStorage_DeleteNode_NotExists(t *testing.T) {
	t.Parallel()

	storage, _ := createTestSQLiteStorage(t)
	defer storage.Close()

	// Delete non-existent node should not error
	if err := storage.DeleteNode("nonexistent.go"); err != nil {
		t.Errorf("DeleteNode (not exists) failed: %v", err)
	}
}

func TestSQLiteStorage_DeleteNode_EmptyKey(t *testing.T) {
	t.Parallel()

	storage, _ := createTestSQLiteStorage(t)
	defer storage.Close()

	err := storage.DeleteNode("")
	if err != ErrStorageEmptyKey {
		t.Errorf("DeleteNode error = %v, want %v", err, ErrStorageEmptyKey)
	}
}

func TestSQLiteStorage_DeleteNode_PreservesOthers(t *testing.T) {
	t.Parallel()

	storage, _ := createTestSQLiteStorage(t)
	defer storage.Close()

	// Save multiple nodes
	keys := []string{"a.go", "b.go", "c.go"}
	for _, key := range keys {
		if err := storage.SaveNode(createTestNode(key)); err != nil {
			t.Fatalf("SaveNode failed: %v", err)
		}
	}

	// Delete one
	if err := storage.DeleteNode("b.go"); err != nil {
		t.Fatalf("DeleteNode failed: %v", err)
	}

	// Verify others remain
	count, err := storage.GetNodeCount()
	if err != nil {
		t.Fatalf("GetNodeCount failed: %v", err)
	}

	if count != 2 {
		t.Errorf("GetNodeCount = %d, want 2", count)
	}

	// Load and verify b.go is gone
	root, err := storage.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	loadedKeys := collectTreeKeys(root)
	for _, key := range loadedKeys {
		if key == "b.go" {
			t.Error("b.go should have been deleted")
		}
	}
}

func TestSQLiteStorage_DeleteNode_NotOpen(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	storage, err := NewSQLiteStorage(path)
	if err != nil {
		t.Fatalf("NewSQLiteStorage failed: %v", err)
	}

	err = storage.DeleteNode("test.go")
	if err != ErrStorageNotOpen {
		t.Errorf("DeleteNode error = %v, want %v", err, ErrStorageNotOpen)
	}
}

// =============================================================================
// GetNodeCount Tests
// =============================================================================

func TestSQLiteStorage_GetNodeCount_Empty(t *testing.T) {
	t.Parallel()

	storage, _ := createTestSQLiteStorage(t)
	defer storage.Close()

	count, err := storage.GetNodeCount()
	if err != nil {
		t.Fatalf("GetNodeCount failed: %v", err)
	}

	if count != 0 {
		t.Errorf("GetNodeCount = %d, want 0", count)
	}
}

func TestSQLiteStorage_GetNodeCount_Accuracy(t *testing.T) {
	t.Parallel()

	storage, _ := createTestSQLiteStorage(t)
	defer storage.Close()

	// Add nodes incrementally and verify count
	for i := 0; i < 10; i++ {
		key := "file" + string(rune('a'+i)) + ".go"
		if err := storage.SaveNode(createTestNode(key)); err != nil {
			t.Fatalf("SaveNode failed: %v", err)
		}

		count, err := storage.GetNodeCount()
		if err != nil {
			t.Fatalf("GetNodeCount failed: %v", err)
		}

		expected := int64(i + 1)
		if count != expected {
			t.Errorf("GetNodeCount = %d, want %d", count, expected)
		}
	}
}

func TestSQLiteStorage_GetNodeCount_NotOpen(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	storage, err := NewSQLiteStorage(path)
	if err != nil {
		t.Fatalf("NewSQLiteStorage failed: %v", err)
	}

	_, err = storage.GetNodeCount()
	if err != ErrStorageNotOpen {
		t.Errorf("GetNodeCount error = %v, want %v", err, ErrStorageNotOpen)
	}
}

// =============================================================================
// Round-Trip Tests
// =============================================================================

func TestSQLiteStorage_RoundTrip_SaveLoad(t *testing.T) {
	t.Parallel()

	storage, _ := createTestSQLiteStorage(t)
	defer storage.Close()

	// Create complex tree
	keys := []string{
		"src/main.go",
		"src/config/config.go",
		"src/handlers/user.go",
		"src/handlers/auth.go",
		"pkg/utils/helpers.go",
		"tests/main_test.go",
	}
	original := createTestTree(keys)

	// Save
	if err := storage.Save(original); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Load
	loaded, err := storage.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Verify structure
	originalKeys := collectTreeKeys(original)
	loadedKeys := collectTreeKeys(loaded)

	if len(loadedKeys) != len(originalKeys) {
		t.Fatalf("loaded %d keys, want %d", len(loadedKeys), len(originalKeys))
	}

	for i, key := range loadedKeys {
		if key != originalKeys[i] {
			t.Errorf("key[%d] = %q, want %q", i, key, originalKeys[i])
		}
	}
}

func TestSQLiteStorage_RoundTrip_CloseReopen(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	// First session: save tree
	storage1, err := NewSQLiteStorage(path)
	if err != nil {
		t.Fatalf("NewSQLiteStorage failed: %v", err)
	}

	if err := storage1.Open(); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	keys := []string{"a.go", "b.go", "c.go"}
	original := createTestTree(keys)
	if err := storage1.Save(original); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	if err := storage1.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Second session: load tree
	storage2, err := NewSQLiteStorage(path)
	if err != nil {
		t.Fatalf("NewSQLiteStorage (reopen) failed: %v", err)
	}

	if err := storage2.Open(); err != nil {
		t.Fatalf("Open (reopen) failed: %v", err)
	}
	defer storage2.Close()

	loaded, err := storage2.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	loadedKeys := collectTreeKeys(loaded)
	if len(loadedKeys) != len(keys) {
		t.Errorf("loaded %d keys, want %d", len(loadedKeys), len(keys))
	}
}

func TestSQLiteStorage_RoundTrip_IncrementalUpdates(t *testing.T) {
	t.Parallel()

	storage, _ := createTestSQLiteStorage(t)
	defer storage.Close()

	// Save individual nodes
	keys := []string{"a.go", "b.go", "c.go"}
	for _, key := range keys {
		if err := storage.SaveNode(createTestNode(key)); err != nil {
			t.Fatalf("SaveNode failed: %v", err)
		}
	}

	// Delete one
	if err := storage.DeleteNode("b.go"); err != nil {
		t.Fatalf("DeleteNode failed: %v", err)
	}

	// Add another
	if err := storage.SaveNode(createTestNode("d.go")); err != nil {
		t.Fatalf("SaveNode failed: %v", err)
	}

	// Load and verify
	loaded, err := storage.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	loadedKeys := collectTreeKeys(loaded)
	expectedKeys := []string{"a.go", "c.go", "d.go"}

	if len(loadedKeys) != len(expectedKeys) {
		t.Fatalf("loaded %d keys, want %d", len(loadedKeys), len(expectedKeys))
	}

	for i, key := range loadedKeys {
		if key != expectedKeys[i] {
			t.Errorf("key[%d] = %q, want %q", i, key, expectedKeys[i])
		}
	}
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestSQLiteStorage_ConcurrentReads(t *testing.T) {
	t.Parallel()

	storage, _ := createTestSQLiteStorage(t)
	defer storage.Close()

	// Save initial data
	keys := []string{"a.go", "b.go", "c.go", "d.go", "e.go"}
	root := createTestTree(keys)
	if err := storage.Save(root); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)

	errors := make(chan error, goroutines)

	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			if _, err := storage.Load(); err != nil {
				errors <- err
			}
			if _, err := storage.GetNodeCount(); err != nil {
				errors <- err
			}
		}()
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent read error: %v", err)
	}
}

func TestSQLiteStorage_ConcurrentWrites(t *testing.T) {
	t.Parallel()

	storage, _ := createTestSQLiteStorage(t)
	defer storage.Close()

	const goroutines = 10
	const nodesPerGoroutine = 20

	var wg sync.WaitGroup
	wg.Add(goroutines)

	errors := make(chan error, goroutines*nodesPerGoroutine)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < nodesPerGoroutine; i++ {
				key := "file" + string(rune('a'+id)) + string(rune('0'+i%10)) + ".go"
				if err := storage.SaveNode(createTestNode(key)); err != nil {
					errors <- err
				}
			}
		}(g)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent write error: %v", err)
	}

	// Verify data integrity
	count, err := storage.GetNodeCount()
	if err != nil {
		t.Fatalf("GetNodeCount failed: %v", err)
	}

	// Note: count may be less than goroutines*nodesPerGoroutine due to key collisions
	if count == 0 {
		t.Error("expected some nodes after concurrent writes")
	}
}

func TestSQLiteStorage_ConcurrentMixedOperations(t *testing.T) {
	t.Parallel()

	storage, _ := createTestSQLiteStorage(t)
	defer storage.Close()

	const goroutines = 8
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			key := "file" + string(rune('a'+id)) + ".go"

			for i := 0; i < 20; i++ {
				switch i % 4 {
				case 0, 1:
					_ = storage.SaveNode(createTestNode(key))
				case 2:
					_ = storage.DeleteNode(key)
				case 3:
					_, _ = storage.GetNodeCount()
				}
			}
		}(g)
	}

	wg.Wait()

	// Should not error or deadlock
	_, err := storage.GetNodeCount()
	if err != nil {
		t.Errorf("GetNodeCount after concurrent operations failed: %v", err)
	}
}

// =============================================================================
// Error Tests
// =============================================================================

func TestSQLiteStorage_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err     error
		wantMsg string
	}{
		{ErrStoragePathEmpty, "storage path cannot be empty"},
		{ErrStorageNotOpen, "storage is not open"},
		{ErrStorageAlreadyOpen, "storage is already open"},
		{ErrStorageNilNode, "node cannot be nil"},
		{ErrStorageEmptyKey, "key cannot be empty"},
	}

	for _, tt := range tests {
		t.Run(tt.wantMsg, func(t *testing.T) {
			t.Parallel()
			if tt.err.Error() != tt.wantMsg {
				t.Errorf("error = %q, want %q", tt.err.Error(), tt.wantMsg)
			}
		})
	}
}

// =============================================================================
// Node Without FileInfo Tests
// =============================================================================

func TestSQLiteStorage_SaveNode_NilFileInfo(t *testing.T) {
	t.Parallel()

	storage, _ := createTestSQLiteStorage(t)
	defer storage.Close()

	// Node without FileInfo
	node := &Node{
		Key:      "test.go",
		Priority: ComputePriority("test.go"),
		Info:     nil,
	}

	if err := storage.SaveNode(node); err != nil {
		t.Errorf("SaveNode (nil info) failed: %v", err)
	}

	// Load and verify
	loaded, err := storage.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded == nil {
		t.Fatal("Load returned nil")
	}

	if loaded.Info != nil {
		t.Error("expected nil Info for loaded node")
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkSQLiteStorage_SaveNode(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.db")

	storage, err := NewSQLiteStorage(path)
	if err != nil {
		b.Fatalf("NewSQLiteStorage failed: %v", err)
	}

	if err := storage.Open(); err != nil {
		b.Fatalf("Open failed: %v", err)
	}
	defer storage.Close()

	node := createTestNode("bench.go")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = storage.SaveNode(node)
	}
}

func BenchmarkSQLiteStorage_Save(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.db")

	storage, err := NewSQLiteStorage(path)
	if err != nil {
		b.Fatalf("NewSQLiteStorage failed: %v", err)
	}

	if err := storage.Open(); err != nil {
		b.Fatalf("Open failed: %v", err)
	}
	defer storage.Close()

	// Create tree with 100 nodes using unique keys
	keys := make([]string, 100)
	for i := range keys {
		keys[i] = "file" + string(rune('a'+i/10)) + string(rune('a'+i%10)) + ".go"
	}
	root := createTestTree(keys)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = storage.Save(root)
	}
}

func BenchmarkSQLiteStorage_Load(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.db")

	storage, err := NewSQLiteStorage(path)
	if err != nil {
		b.Fatalf("NewSQLiteStorage failed: %v", err)
	}

	if err := storage.Open(); err != nil {
		b.Fatalf("Open failed: %v", err)
	}
	defer storage.Close()

	// Save 1000 nodes with unique keys
	for i := 0; i < 1000; i++ {
		key := filepath.Join("src", "pkg", "file"+string(rune('a'+i/100))+string(rune('a'+i/10%10))+string(rune('a'+i%10))+".go")
		_ = storage.SaveNode(createTestNode(key))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = storage.Load()
	}
}
