// Package cmt implements a Cartesian Merkle Tree for file manifest tracking.
package cmt

import (
	"encoding/json"
	"testing"
	"time"
)

// =============================================================================
// Hash Tests
// =============================================================================

func TestHash_IsZero(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		hash     Hash
		expected bool
	}{
		{
			name:     "zero hash returns true",
			hash:     Hash{},
			expected: true,
		},
		{
			name:     "all zeros explicit returns true",
			hash:     Hash{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			expected: true,
		},
		{
			name:     "first byte non-zero returns false",
			hash:     Hash{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			expected: false,
		},
		{
			name:     "last byte non-zero returns false",
			hash:     Hash{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
			expected: false,
		},
		{
			name:     "middle byte non-zero returns false",
			hash:     Hash{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			expected: false,
		},
		{
			name:     "all bytes 0xff returns false",
			hash:     Hash{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.hash.IsZero()
			if got != tt.expected {
				t.Errorf("Hash.IsZero() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestHash_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		hash     Hash
		expected string
	}{
		{
			name:     "zero hash returns 64 zeros",
			hash:     Hash{},
			expected: "0000000000000000000000000000000000000000000000000000000000000000",
		},
		{
			name:     "first byte 0x01 returns correct hex",
			hash:     Hash{0x01, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			expected: "0100000000000000000000000000000000000000000000000000000000000000",
		},
		{
			name:     "last byte 0xff returns correct hex",
			hash:     Hash{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff},
			expected: "00000000000000000000000000000000000000000000000000000000000000ff",
		},
		{
			name:     "all 0xff returns correct hex",
			hash:     Hash{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
			expected: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		},
		{
			name:     "mixed bytes returns correct hex",
			hash:     Hash{0xde, 0xad, 0xbe, 0xef, 0xca, 0xfe, 0xba, 0xbe, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			expected: "deadbeefcafebabe000000000000000000000000000000000000000000000000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.hash.String()
			if got != tt.expected {
				t.Errorf("Hash.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestHash_StringLength(t *testing.T) {
	t.Parallel()

	// Any hash should produce exactly 64 hex characters
	testHashes := []Hash{
		{},
		{0x01},
		{0xff, 0xff, 0xff, 0xff},
	}

	for i, h := range testHashes {
		got := h.String()
		if len(got) != 64 {
			t.Errorf("Hash[%d].String() length = %d, want 64", i, len(got))
		}
	}
}

// =============================================================================
// ComputePriority Tests
// =============================================================================

func TestComputePriority_Deterministic(t *testing.T) {
	t.Parallel()

	// Same key should always produce the same priority
	testKeys := []string{
		"src/main.go",
		"README.md",
		"pkg/utils/helper.go",
		"/absolute/path/to/file.txt",
		"",
		"a",
		"this is a very long key with spaces and special characters!@#$%^&*()",
	}

	for _, key := range testKeys {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			p1 := ComputePriority(key)
			p2 := ComputePriority(key)
			p3 := ComputePriority(key)

			if p1 != p2 || p2 != p3 {
				t.Errorf("ComputePriority(%q) not deterministic: got %d, %d, %d", key, p1, p2, p3)
			}
		})
	}
}

func TestComputePriority_DifferentKeysProduceDifferentPriorities(t *testing.T) {
	t.Parallel()

	// Different keys should (almost always) produce different priorities
	keys := []string{
		"a",
		"b",
		"c",
		"aa",
		"ab",
		"src/main.go",
		"src/main.rs",
	}

	priorities := make(map[uint64]string)
	for _, key := range keys {
		p := ComputePriority(key)
		if existing, ok := priorities[p]; ok {
			t.Errorf("ComputePriority collision: %q and %q both produce %d", key, existing, p)
		}
		priorities[p] = key
	}
}

func TestComputePriority_Distribution(t *testing.T) {
	t.Parallel()

	// Basic randomness check: priorities should be distributed across the uint64 range
	// We'll check that priorities don't all fall in one quadrant
	const numKeys = 1000
	var quadrants [4]int // Divide uint64 range into 4 quadrants

	for i := range numKeys {
		key := generateTestKey(i)
		p := ComputePriority(key)

		// Determine which quadrant this priority falls into
		quadrant := p / (^uint64(0) / 4)
		if quadrant > 3 {
			quadrant = 3
		}
		quadrants[quadrant]++
	}

	// Each quadrant should have at least 10% of keys (very loose check)
	minPerQuadrant := numKeys / 10
	for i, count := range quadrants {
		if count < minPerQuadrant {
			t.Errorf("Quadrant %d has only %d keys, expected at least %d (distribution may be skewed)",
				i, count, minPerQuadrant)
		}
	}
}

func TestComputePriority_EmptyKey(t *testing.T) {
	t.Parallel()

	// Empty key should still produce a valid priority
	p := ComputePriority("")
	// Just verify it's deterministic
	if p != ComputePriority("") {
		t.Error("ComputePriority(\"\") not deterministic")
	}
}

// generateTestKey creates a unique key for testing distribution
func generateTestKey(i int) string {
	return "test/path/file" + string(rune('A'+i%26)) + string(rune('a'+i/26%26)) + ".go"
}

// =============================================================================
// FileInfo Tests
// =============================================================================

func TestFileInfo_Validate(t *testing.T) {
	t.Parallel()

	now := time.Now()
	validHash := Hash{0xde, 0xad, 0xbe, 0xef}

	tests := []struct {
		name        string
		info        FileInfo
		expectError bool
	}{
		{
			name: "valid file info",
			info: FileInfo{
				Path:        "/path/to/file.go",
				ContentHash: validHash,
				Size:        1024,
				ModTime:     now,
				Permissions: 0644,
				Indexed:     true,
				IndexedAt:   now,
			},
			expectError: false,
		},
		{
			name: "empty path is invalid",
			info: FileInfo{
				Path:        "",
				ContentHash: validHash,
				Size:        1024,
				ModTime:     now,
				Permissions: 0644,
			},
			expectError: true,
		},
		{
			name: "zero content hash is invalid",
			info: FileInfo{
				Path:        "/path/to/file.go",
				ContentHash: Hash{},
				Size:        1024,
				ModTime:     now,
				Permissions: 0644,
			},
			expectError: true,
		},
		{
			name: "negative size is invalid",
			info: FileInfo{
				Path:        "/path/to/file.go",
				ContentHash: validHash,
				Size:        -1,
				ModTime:     now,
				Permissions: 0644,
			},
			expectError: true,
		},
		{
			name: "zero size is valid (empty file)",
			info: FileInfo{
				Path:        "/path/to/empty.txt",
				ContentHash: validHash,
				Size:        0,
				ModTime:     now,
				Permissions: 0644,
			},
			expectError: false,
		},
		{
			name: "zero mod time is invalid",
			info: FileInfo{
				Path:        "/path/to/file.go",
				ContentHash: validHash,
				Size:        1024,
				ModTime:     time.Time{},
				Permissions: 0644,
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.info.Validate()
			if tt.expectError && err == nil {
				t.Error("FileInfo.Validate() expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("FileInfo.Validate() unexpected error: %v", err)
			}
		})
	}
}

func TestFileInfo_JSONSerialization(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Second) // Truncate for JSON comparison
	info := FileInfo{
		Path:        "/path/to/file.go",
		ContentHash: Hash{0xde, 0xad, 0xbe, 0xef, 0xca, 0xfe, 0xba, 0xbe},
		Size:        1024,
		ModTime:     now,
		Permissions: 0644,
		Indexed:     true,
		IndexedAt:   now,
	}

	// Serialize to JSON
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	// Deserialize back
	var restored FileInfo
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	// Compare fields
	if restored.Path != info.Path {
		t.Errorf("Path mismatch: got %q, want %q", restored.Path, info.Path)
	}
	if restored.ContentHash != info.ContentHash {
		t.Errorf("ContentHash mismatch: got %v, want %v", restored.ContentHash, info.ContentHash)
	}
	if restored.Size != info.Size {
		t.Errorf("Size mismatch: got %d, want %d", restored.Size, info.Size)
	}
	if !restored.ModTime.Equal(info.ModTime) {
		t.Errorf("ModTime mismatch: got %v, want %v", restored.ModTime, info.ModTime)
	}
	if restored.Permissions != info.Permissions {
		t.Errorf("Permissions mismatch: got %o, want %o", restored.Permissions, info.Permissions)
	}
	if restored.Indexed != info.Indexed {
		t.Errorf("Indexed mismatch: got %v, want %v", restored.Indexed, info.Indexed)
	}
}

func TestFileInfo_Clone(t *testing.T) {
	t.Parallel()

	now := time.Now()
	original := &FileInfo{
		Path:        "/path/to/file.go",
		ContentHash: Hash{0xde, 0xad, 0xbe, 0xef},
		Size:        1024,
		ModTime:     now,
		Permissions: 0644,
		Indexed:     true,
		IndexedAt:   now,
	}

	cloned := original.Clone()

	// Verify clone is equal
	if cloned.Path != original.Path {
		t.Errorf("Clone Path mismatch")
	}
	if cloned.ContentHash != original.ContentHash {
		t.Errorf("Clone ContentHash mismatch")
	}
	if cloned.Size != original.Size {
		t.Errorf("Clone Size mismatch")
	}

	// Verify clone is independent (modifying clone doesn't affect original)
	cloned.Path = "/modified/path"
	if original.Path == cloned.Path {
		t.Error("Clone is not independent - modifying clone affected original")
	}
}

// =============================================================================
// Node Tests
// =============================================================================

func TestNewNode(t *testing.T) {
	t.Parallel()

	now := time.Now()
	info := &FileInfo{
		Path:        "/src/main.go",
		ContentHash: Hash{0xde, 0xad, 0xbe, 0xef},
		Size:        1024,
		ModTime:     now,
		Permissions: 0644,
		Indexed:     true,
		IndexedAt:   now,
	}

	node := NewNode("/src/main.go", info)

	if node == nil {
		t.Fatal("NewNode returned nil")
	}

	if node.Key != "/src/main.go" {
		t.Errorf("NewNode Key = %q, want %q", node.Key, "/src/main.go")
	}

	expectedPriority := ComputePriority("/src/main.go")
	if node.Priority != expectedPriority {
		t.Errorf("NewNode Priority = %d, want %d", node.Priority, expectedPriority)
	}

	if node.Left != nil {
		t.Error("NewNode Left should be nil")
	}

	if node.Right != nil {
		t.Error("NewNode Right should be nil")
	}

	if node.Info == nil {
		t.Error("NewNode Info should not be nil")
	}

	if node.Info.Path != info.Path {
		t.Errorf("NewNode Info.Path = %q, want %q", node.Info.Path, info.Path)
	}
}

func TestNewNode_NilInfo(t *testing.T) {
	t.Parallel()

	node := NewNode("/src/main.go", nil)

	if node == nil {
		t.Fatal("NewNode returned nil")
	}

	if node.Info != nil {
		t.Error("NewNode with nil info should have nil Info")
	}

	if node.Key != "/src/main.go" {
		t.Errorf("NewNode Key = %q, want %q", node.Key, "/src/main.go")
	}
}

func TestNode_IsLeaf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		node     *Node
		expected bool
	}{
		{
			name:     "node with no children is leaf",
			node:     &Node{Key: "a"},
			expected: true,
		},
		{
			name:     "node with left child is not leaf",
			node:     &Node{Key: "b", Left: &Node{Key: "a"}},
			expected: false,
		},
		{
			name:     "node with right child is not leaf",
			node:     &Node{Key: "a", Right: &Node{Key: "b"}},
			expected: false,
		},
		{
			name:     "node with both children is not leaf",
			node:     &Node{Key: "b", Left: &Node{Key: "a"}, Right: &Node{Key: "c"}},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.node.IsLeaf()
			if got != tt.expected {
				t.Errorf("Node.IsLeaf() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestNode_IsLeaf_NilReceiver(t *testing.T) {
	t.Parallel()

	var node *Node
	// Calling IsLeaf on nil should be safe and return true (no children)
	if !node.IsLeaf() {
		t.Error("nil Node.IsLeaf() should return true")
	}
}

func TestNode_Size(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		node     *Node
		expected int64
	}{
		{
			name:     "nil node has size 0",
			node:     nil,
			expected: 0,
		},
		{
			name:     "single node has size 1",
			node:     &Node{Key: "a"},
			expected: 1,
		},
		{
			name:     "node with left child has size 2",
			node:     &Node{Key: "b", Left: &Node{Key: "a"}},
			expected: 2,
		},
		{
			name:     "node with right child has size 2",
			node:     &Node{Key: "a", Right: &Node{Key: "b"}},
			expected: 2,
		},
		{
			name:     "node with both children has size 3",
			node:     &Node{Key: "b", Left: &Node{Key: "a"}, Right: &Node{Key: "c"}},
			expected: 3,
		},
		{
			name: "larger tree has correct size",
			node: &Node{
				Key: "d",
				Left: &Node{
					Key:  "b",
					Left: &Node{Key: "a"},
					Right: &Node{Key: "c"},
				},
				Right: &Node{
					Key:   "f",
					Left:  &Node{Key: "e"},
					Right: &Node{Key: "g"},
				},
			},
			expected: 7,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.node.Size()
			if got != tt.expected {
				t.Errorf("Node.Size() = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestNode_SizeConsistency(t *testing.T) {
	t.Parallel()

	// Build a tree manually and verify size is consistent
	root := &Node{Key: "m"}
	root.Left = &Node{Key: "f"}
	root.Left.Left = &Node{Key: "c"}
	root.Left.Right = &Node{Key: "i"}
	root.Right = &Node{Key: "t"}
	root.Right.Left = &Node{Key: "p"}
	root.Right.Right = &Node{Key: "z"}

	if root.Size() != 7 {
		t.Errorf("Root size = %d, want 7", root.Size())
	}

	if root.Left.Size() != 3 {
		t.Errorf("Left subtree size = %d, want 3", root.Left.Size())
	}

	if root.Right.Size() != 3 {
		t.Errorf("Right subtree size = %d, want 3", root.Right.Size())
	}
}

// =============================================================================
// Hash Constant Tests
// =============================================================================

func TestHashSize_Constant(t *testing.T) {
	t.Parallel()

	if HashSize != 32 {
		t.Errorf("HashSize = %d, want 32 (SHA-256 size)", HashSize)
	}

	// Verify Hash type matches HashSize
	var h Hash
	if len(h) != HashSize {
		t.Errorf("len(Hash{}) = %d, want %d", len(h), HashSize)
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestNodeWithFileInfo_Integration(t *testing.T) {
	t.Parallel()

	now := time.Now()

	// Create a realistic file info
	info := &FileInfo{
		Path:        "/Users/dev/project/src/main.go",
		ContentHash: Hash{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f, 0x20},
		Size:        4096,
		ModTime:     now,
		Permissions: 0755,
		Indexed:     true,
		IndexedAt:   now,
	}

	// Validate file info
	if err := info.Validate(); err != nil {
		t.Fatalf("FileInfo.Validate() failed: %v", err)
	}

	// Create node
	node := NewNode(info.Path, info)

	// Verify node is a leaf
	if !node.IsLeaf() {
		t.Error("New node should be a leaf")
	}

	// Verify size is 1
	if node.Size() != 1 {
		t.Errorf("New node size = %d, want 1", node.Size())
	}

	// Verify priority is deterministic
	if node.Priority != ComputePriority(info.Path) {
		t.Error("Node priority doesn't match computed priority")
	}
}

func TestBuildSmallTree(t *testing.T) {
	t.Parallel()

	// Build a small tree structure to verify all types work together
	now := time.Now()

	createInfo := func(path string) *FileInfo {
		return &FileInfo{
			Path:        path,
			ContentHash: Hash{byte(len(path))}, // Simple non-zero hash
			Size:        int64(len(path) * 100),
			ModTime:     now,
			Permissions: 0644,
			Indexed:     true,
			IndexedAt:   now,
		}
	}

	// Create nodes (manually arranged as a BST)
	root := NewNode("m/file.go", createInfo("m/file.go"))
	root.Left = NewNode("a/file.go", createInfo("a/file.go"))
	root.Right = NewNode("z/file.go", createInfo("z/file.go"))

	// Verify BST property holds
	if root.Left.Key >= root.Key {
		t.Error("BST property violated: left key >= root key")
	}
	if root.Right.Key <= root.Key {
		t.Error("BST property violated: right key <= root key")
	}

	// Verify size
	if root.Size() != 3 {
		t.Errorf("Tree size = %d, want 3", root.Size())
	}

	// Verify leaf detection
	if !root.Left.IsLeaf() {
		t.Error("Left child should be a leaf")
	}
	if !root.Right.IsLeaf() {
		t.Error("Right child should be a leaf")
	}
	if root.IsLeaf() {
		t.Error("Root should not be a leaf")
	}
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestHash_Equality(t *testing.T) {
	t.Parallel()

	h1 := Hash{0xde, 0xad, 0xbe, 0xef}
	h2 := Hash{0xde, 0xad, 0xbe, 0xef}
	h3 := Hash{0xde, 0xad, 0xbe, 0xee} // One bit different

	if h1 != h2 {
		t.Error("Equal hashes should be ==")
	}

	if h1 == h3 {
		t.Error("Different hashes should not be ==")
	}
}

func TestFileInfo_LargeSize(t *testing.T) {
	t.Parallel()

	now := time.Now()
	info := &FileInfo{
		Path:        "/path/to/large/file.bin",
		ContentHash: Hash{0x01},
		Size:        1 << 40, // 1 TB
		ModTime:     now,
		Permissions: 0644,
	}

	if err := info.Validate(); err != nil {
		t.Errorf("Large file size should be valid: %v", err)
	}
}

func TestComputePriority_UnicodeKey(t *testing.T) {
	t.Parallel()

	// Unicode paths should work correctly
	keys := []string{
		"/path/to/\u4e2d\u6587.go",  // Chinese characters
		"/path/to/\u65e5\u672c.go",  // Japanese characters
		"/path/to/\U0001f600.txt",   // Emoji
		"/path/to/caf\u00e9.txt",    // Accented character
	}

	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			p1 := ComputePriority(key)
			p2 := ComputePriority(key)
			if p1 != p2 {
				t.Errorf("ComputePriority not deterministic for unicode key %q", key)
			}
		})
	}
}

func TestNode_DeepTree(t *testing.T) {
	t.Parallel()

	// Build a deep (unbalanced) tree to test Size recursion
	var root *Node
	current := &Node{Key: "a"}
	root = current

	for i := 1; i < 100; i++ {
		current.Right = &Node{Key: string(rune('a' + i%26))}
		current = current.Right
	}

	if root.Size() != 100 {
		t.Errorf("Deep tree size = %d, want 100", root.Size())
	}
}
