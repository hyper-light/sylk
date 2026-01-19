package cmt

import (
	"crypto/sha256"
	"testing"
	"time"
)

// =============================================================================
// Test Helpers
// =============================================================================

// makeHash creates a Hash from a string for testing.
func makeHash(s string) Hash {
	return sha256.Sum256([]byte(s))
}

// makeLeafNode creates a leaf node with FileInfo for testing.
func makeLeafNode(key, content string) *Node {
	contentHash := makeHash(content)
	return &Node{
		Key: key,
		Info: &FileInfo{
			Path:        key,
			Size:        int64(len(content)),
			ModTime:     time.Now(),
			ContentHash: contentHash,
		},
	}
}

// makeInternalNode creates an internal node with children for testing.
func makeInternalNode(key string, left, right *Node) *Node {
	return &Node{
		Key:   key,
		Left:  left,
		Right: right,
	}
}

// computeExpectedNodeHash manually computes what the hash should be.
func computeExpectedNodeHash(node *Node) Hash {
	h := sha256.New()

	// Left child hash
	if node.Left != nil {
		h.Write(node.Left.Hash[:])
	} else {
		h.Write(make([]byte, HashSize))
	}

	// Key
	h.Write([]byte(node.Key))

	// Content hash from FileInfo
	if node.Info != nil {
		h.Write(node.Info.ContentHash[:])
	} else {
		h.Write(make([]byte, HashSize))
	}

	// Right child hash
	if node.Right != nil {
		h.Write(node.Right.Hash[:])
	} else {
		h.Write(make([]byte, HashSize))
	}

	var result Hash
	copy(result[:], h.Sum(nil))
	return result
}

// =============================================================================
// ComputeNodeHash Tests
// =============================================================================

func TestComputeNodeHash_NilNode(t *testing.T) {
	t.Parallel()

	hash := ComputeNodeHash(nil)

	if !hash.IsZero() {
		t.Errorf("ComputeNodeHash(nil) should return zero hash")
	}
}

func TestComputeNodeHash_LeafNode(t *testing.T) {
	t.Parallel()

	node := makeLeafNode("/path/to/file.go", "package main")

	hash := ComputeNodeHash(node)
	expected := computeExpectedNodeHash(node)

	if hash != expected {
		t.Errorf("ComputeNodeHash() = %x, want %x", hash, expected)
	}
}

func TestComputeNodeHash_LeafNodeWithNilInfo(t *testing.T) {
	t.Parallel()

	node := &Node{
		Key:  "/path/to/file.go",
		Info: nil,
	}

	hash := ComputeNodeHash(node)
	expected := computeExpectedNodeHash(node)

	if hash != expected {
		t.Errorf("ComputeNodeHash() = %x, want %x", hash, expected)
	}
}

func TestComputeNodeHash_NodeWithLeftChild(t *testing.T) {
	t.Parallel()

	left := makeLeafNode("/path/a.go", "content a")
	left.Hash = ComputeNodeHash(left)

	node := makeInternalNode("/path/b.go", left, nil)

	hash := ComputeNodeHash(node)
	expected := computeExpectedNodeHash(node)

	if hash != expected {
		t.Errorf("ComputeNodeHash() = %x, want %x", hash, expected)
	}
}

func TestComputeNodeHash_NodeWithRightChild(t *testing.T) {
	t.Parallel()

	right := makeLeafNode("/path/c.go", "content c")
	right.Hash = ComputeNodeHash(right)

	node := makeInternalNode("/path/b.go", nil, right)

	hash := ComputeNodeHash(node)
	expected := computeExpectedNodeHash(node)

	if hash != expected {
		t.Errorf("ComputeNodeHash() = %x, want %x", hash, expected)
	}
}

func TestComputeNodeHash_NodeWithBothChildren(t *testing.T) {
	t.Parallel()

	left := makeLeafNode("/path/a.go", "content a")
	left.Hash = ComputeNodeHash(left)

	right := makeLeafNode("/path/c.go", "content c")
	right.Hash = ComputeNodeHash(right)

	node := makeInternalNode("/path/b.go", left, right)

	hash := ComputeNodeHash(node)
	expected := computeExpectedNodeHash(node)

	if hash != expected {
		t.Errorf("ComputeNodeHash() = %x, want %x", hash, expected)
	}
}

func TestComputeNodeHash_Deterministic(t *testing.T) {
	t.Parallel()

	node := makeLeafNode("/path/to/file.go", "package main")

	hash1 := ComputeNodeHash(node)
	hash2 := ComputeNodeHash(node)
	hash3 := ComputeNodeHash(node)

	if hash1 != hash2 || hash2 != hash3 {
		t.Errorf("ComputeNodeHash() not deterministic")
	}
}

func TestComputeNodeHash_DifferentKeysDifferentHashes(t *testing.T) {
	t.Parallel()

	node1 := makeLeafNode("/path/a.go", "content")
	node2 := makeLeafNode("/path/b.go", "content")

	hash1 := ComputeNodeHash(node1)
	hash2 := ComputeNodeHash(node2)

	if hash1 == hash2 {
		t.Errorf("Different keys should produce different hashes")
	}
}

func TestComputeNodeHash_DifferentContentDifferentHashes(t *testing.T) {
	t.Parallel()

	node1 := makeLeafNode("/path/file.go", "content a")
	node2 := makeLeafNode("/path/file.go", "content b")

	hash1 := ComputeNodeHash(node1)
	hash2 := ComputeNodeHash(node2)

	if hash1 == hash2 {
		t.Errorf("Different content should produce different hashes")
	}
}

// =============================================================================
// UpdatePathHashes Tests
// =============================================================================

func TestUpdatePathHashes_NilPath(t *testing.T) {
	t.Parallel()

	// Should not panic
	UpdatePathHashes(nil)
}

func TestUpdatePathHashes_EmptyPath(t *testing.T) {
	t.Parallel()

	// Should not panic
	UpdatePathHashes([]*Node{})
}

func TestUpdatePathHashes_SingleNode(t *testing.T) {
	t.Parallel()

	node := makeLeafNode("/path/file.go", "content")
	originalHash := node.Hash

	UpdatePathHashes([]*Node{node})

	if node.Hash == originalHash {
		t.Errorf("Hash should be updated")
	}

	expected := ComputeNodeHash(node)
	if node.Hash != expected {
		t.Errorf("Hash = %x, want %x", node.Hash, expected)
	}
}

func TestUpdatePathHashes_LeafToRoot(t *testing.T) {
	t.Parallel()

	// Create a simple tree: root -> left leaf
	leaf := makeLeafNode("/path/a.go", "content a")
	root := makeInternalNode("/path/root", leaf, nil)

	// Update from leaf to root
	UpdatePathHashes([]*Node{leaf, root})

	// Verify leaf hash
	expectedLeaf := ComputeNodeHash(leaf)
	if leaf.Hash != expectedLeaf {
		t.Errorf("Leaf hash = %x, want %x", leaf.Hash, expectedLeaf)
	}

	// Verify root hash (should include updated leaf hash)
	expectedRoot := computeExpectedNodeHash(root)
	if root.Hash != expectedRoot {
		t.Errorf("Root hash = %x, want %x", root.Hash, expectedRoot)
	}
}

func TestUpdatePathHashes_ThreeNodePath(t *testing.T) {
	t.Parallel()

	// Create a path: leaf -> parent -> grandparent
	leaf := makeLeafNode("/a/b/file.go", "content")
	parent := makeInternalNode("/a/b", leaf, nil)
	grandparent := makeInternalNode("/a", parent, nil)

	UpdatePathHashes([]*Node{leaf, parent, grandparent})

	// Each node should have correct hash
	if leaf.Hash != ComputeNodeHash(leaf) {
		t.Errorf("Leaf hash incorrect")
	}
	if parent.Hash != computeExpectedNodeHash(parent) {
		t.Errorf("Parent hash incorrect")
	}
	if grandparent.Hash != computeExpectedNodeHash(grandparent) {
		t.Errorf("Grandparent hash incorrect")
	}
}

func TestUpdatePathHashes_SkipsNilNodes(t *testing.T) {
	t.Parallel()

	node := makeLeafNode("/path/file.go", "content")

	// Path with nil nodes should not panic
	UpdatePathHashes([]*Node{nil, node, nil})

	expected := ComputeNodeHash(node)
	if node.Hash != expected {
		t.Errorf("Hash = %x, want %x", node.Hash, expected)
	}
}

// =============================================================================
// VerifyNodeHash Tests
// =============================================================================

func TestVerifyNodeHash_NilNode(t *testing.T) {
	t.Parallel()

	result := VerifyNodeHash(nil)

	if !result {
		t.Errorf("VerifyNodeHash(nil) should return true")
	}
}

func TestVerifyNodeHash_ValidLeaf(t *testing.T) {
	t.Parallel()

	node := makeLeafNode("/path/file.go", "content")
	node.Hash = ComputeNodeHash(node)

	if !VerifyNodeHash(node) {
		t.Errorf("VerifyNodeHash() = false for valid node")
	}
}

func TestVerifyNodeHash_ValidNodeWithChildren(t *testing.T) {
	t.Parallel()

	left := makeLeafNode("/path/a.go", "content a")
	left.Hash = ComputeNodeHash(left)

	right := makeLeafNode("/path/c.go", "content c")
	right.Hash = ComputeNodeHash(right)

	node := makeInternalNode("/path/b.go", left, right)
	node.Hash = ComputeNodeHash(node)

	if !VerifyNodeHash(node) {
		t.Errorf("VerifyNodeHash() = false for valid node with children")
	}
}

func TestVerifyNodeHash_TamperedHash(t *testing.T) {
	t.Parallel()

	node := makeLeafNode("/path/file.go", "content")
	node.Hash = ComputeNodeHash(node)

	// Tamper with hash
	node.Hash[0] ^= 0xFF

	if VerifyNodeHash(node) {
		t.Errorf("VerifyNodeHash() = true for tampered hash")
	}
}

func TestVerifyNodeHash_TamperedContent(t *testing.T) {
	t.Parallel()

	node := makeLeafNode("/path/file.go", "original content")
	node.Hash = ComputeNodeHash(node)

	// Tamper with content
	node.Info.ContentHash = makeHash("modified content")

	if VerifyNodeHash(node) {
		t.Errorf("VerifyNodeHash() = true for tampered content")
	}
}

func TestVerifyNodeHash_TamperedKey(t *testing.T) {
	t.Parallel()

	node := makeLeafNode("/path/file.go", "content")
	node.Hash = ComputeNodeHash(node)

	// Tamper with key
	node.Key = "/path/other.go"

	if VerifyNodeHash(node) {
		t.Errorf("VerifyNodeHash() = true for tampered key")
	}
}

func TestVerifyNodeHash_ZeroHash(t *testing.T) {
	t.Parallel()

	node := makeLeafNode("/path/file.go", "content")
	// Hash is zero by default

	if VerifyNodeHash(node) {
		t.Errorf("VerifyNodeHash() = true for zero hash")
	}
}

// =============================================================================
// VerifySubtree Tests
// =============================================================================

func TestVerifySubtree_NilNode(t *testing.T) {
	t.Parallel()

	valid, invalidPaths := VerifySubtree(nil)

	if !valid {
		t.Errorf("VerifySubtree(nil) should return true")
	}
	if len(invalidPaths) != 0 {
		t.Errorf("invalidPaths should be empty")
	}
}

func TestVerifySubtree_ValidLeaf(t *testing.T) {
	t.Parallel()

	node := makeLeafNode("/path/file.go", "content")
	node.Hash = ComputeNodeHash(node)

	valid, invalidPaths := VerifySubtree(node)

	if !valid {
		t.Errorf("VerifySubtree() = false for valid leaf")
	}
	if len(invalidPaths) != 0 {
		t.Errorf("invalidPaths = %v, want empty", invalidPaths)
	}
}

func TestVerifySubtree_ValidTree(t *testing.T) {
	t.Parallel()

	// Build a valid tree
	left := makeLeafNode("/path/a.go", "content a")
	left.Hash = ComputeNodeHash(left)

	right := makeLeafNode("/path/c.go", "content c")
	right.Hash = ComputeNodeHash(right)

	root := makeInternalNode("/path/b.go", left, right)
	root.Hash = ComputeNodeHash(root)

	valid, invalidPaths := VerifySubtree(root)

	if !valid {
		t.Errorf("VerifySubtree() = false for valid tree")
	}
	if len(invalidPaths) != 0 {
		t.Errorf("invalidPaths = %v, want empty", invalidPaths)
	}
}

func TestVerifySubtree_CorruptedRoot(t *testing.T) {
	t.Parallel()

	left := makeLeafNode("/path/a.go", "content a")
	left.Hash = ComputeNodeHash(left)

	right := makeLeafNode("/path/c.go", "content c")
	right.Hash = ComputeNodeHash(right)

	root := makeInternalNode("/path/b.go", left, right)
	root.Hash = ComputeNodeHash(root)
	root.Hash[0] ^= 0xFF // Corrupt root

	valid, invalidPaths := VerifySubtree(root)

	if valid {
		t.Errorf("VerifySubtree() = true for corrupted root")
	}
	if len(invalidPaths) != 1 || invalidPaths[0] != "/path/b.go" {
		t.Errorf("invalidPaths = %v, want [/path/b.go]", invalidPaths)
	}
}

func TestVerifySubtree_CorruptedLeaf(t *testing.T) {
	t.Parallel()

	left := makeLeafNode("/path/a.go", "content a")
	left.Hash = ComputeNodeHash(left)
	left.Hash[0] ^= 0xFF // Corrupt left leaf

	right := makeLeafNode("/path/c.go", "content c")
	right.Hash = ComputeNodeHash(right)

	root := makeInternalNode("/path/b.go", left, right)
	root.Hash = ComputeNodeHash(root)

	valid, invalidPaths := VerifySubtree(root)

	if valid {
		t.Errorf("VerifySubtree() = true for corrupted leaf")
	}
	if len(invalidPaths) != 1 || invalidPaths[0] != "/path/a.go" {
		t.Errorf("invalidPaths = %v, want [/path/a.go]", invalidPaths)
	}
}

func TestVerifySubtree_MultipleCorruptions(t *testing.T) {
	t.Parallel()

	left := makeLeafNode("/path/a.go", "content a")
	left.Hash = ComputeNodeHash(left)
	left.Hash[0] ^= 0xFF // Corrupt

	right := makeLeafNode("/path/c.go", "content c")
	right.Hash = ComputeNodeHash(right)
	right.Hash[0] ^= 0xFF // Corrupt

	root := makeInternalNode("/path/b.go", left, right)
	root.Hash = ComputeNodeHash(root)

	valid, invalidPaths := VerifySubtree(root)

	if valid {
		t.Errorf("VerifySubtree() = true for corrupted tree")
	}
	if len(invalidPaths) != 2 {
		t.Errorf("invalidPaths = %v, want 2 entries", invalidPaths)
	}
}

func TestVerifySubtree_DeepCorruption(t *testing.T) {
	t.Parallel()

	// Build a deeper tree
	leaf1 := makeLeafNode("/a/1.go", "content 1")
	leaf1.Hash = ComputeNodeHash(leaf1)

	leaf2 := makeLeafNode("/a/3.go", "content 3")
	leaf2.Hash = ComputeNodeHash(leaf2)

	internal := makeInternalNode("/a/2.go", leaf1, leaf2)
	internal.Hash = ComputeNodeHash(internal)

	leaf3 := makeLeafNode("/b/1.go", "content b1")
	leaf3.Hash = ComputeNodeHash(leaf3)

	root := makeInternalNode("/root", internal, leaf3)
	root.Hash = ComputeNodeHash(root)

	// Corrupt deep leaf
	leaf1.Hash[0] ^= 0xFF

	valid, invalidPaths := VerifySubtree(root)

	if valid {
		t.Errorf("VerifySubtree() = true for deep corruption")
	}
	// When a leaf is corrupted, both the leaf and its parent will be invalid
	// because the parent's stored hash was computed with the original leaf hash.
	// The verification visits leaf first (post-order), so leaf1 and internal are invalid.
	if len(invalidPaths) < 1 {
		t.Errorf("invalidPaths should have at least 1 entry, got %v", invalidPaths)
	}
	// The corrupted leaf should be in the invalid paths
	hasCorruptedLeaf := false
	for _, p := range invalidPaths {
		if p == "/a/1.go" {
			hasCorruptedLeaf = true
			break
		}
	}
	if !hasCorruptedLeaf {
		t.Errorf("invalidPaths = %v, should contain /a/1.go", invalidPaths)
	}
}

// =============================================================================
// TreeVerifier Tests
// =============================================================================

func TestNewTreeVerifier(t *testing.T) {
	t.Parallel()

	node := makeLeafNode("/path/file.go", "content")
	verifier := NewTreeVerifier(node)

	if verifier == nil {
		t.Fatal("NewTreeVerifier() returned nil")
	}
}

func TestNewTreeVerifier_NilRoot(t *testing.T) {
	t.Parallel()

	verifier := NewTreeVerifier(nil)

	if verifier == nil {
		t.Fatal("NewTreeVerifier(nil) returned nil")
	}
}

func TestTreeVerifier_Verify_ValidTree(t *testing.T) {
	t.Parallel()

	left := makeLeafNode("/path/a.go", "content a")
	left.Hash = ComputeNodeHash(left)

	right := makeLeafNode("/path/c.go", "content c")
	right.Hash = ComputeNodeHash(right)

	root := makeInternalNode("/path/b.go", left, right)
	root.Hash = ComputeNodeHash(root)

	verifier := NewTreeVerifier(root)
	result := verifier.Verify()

	if !result.Valid {
		t.Errorf("result.Valid = false, want true")
	}
	if result.TotalNodes != 3 {
		t.Errorf("result.TotalNodes = %d, want 3", result.TotalNodes)
	}
	if result.InvalidNodes != 0 {
		t.Errorf("result.InvalidNodes = %d, want 0", result.InvalidNodes)
	}
	if len(result.InvalidPaths) != 0 {
		t.Errorf("result.InvalidPaths = %v, want empty", result.InvalidPaths)
	}
	if len(result.Errors) != 0 {
		t.Errorf("result.Errors = %v, want empty", result.Errors)
	}
}

func TestTreeVerifier_Verify_NilRoot(t *testing.T) {
	t.Parallel()

	verifier := NewTreeVerifier(nil)
	result := verifier.Verify()

	if !result.Valid {
		t.Errorf("result.Valid = false for nil tree")
	}
	if result.TotalNodes != 0 {
		t.Errorf("result.TotalNodes = %d, want 0", result.TotalNodes)
	}
}

func TestTreeVerifier_Verify_SingleNode(t *testing.T) {
	t.Parallel()

	node := makeLeafNode("/path/file.go", "content")
	node.Hash = ComputeNodeHash(node)

	verifier := NewTreeVerifier(node)
	result := verifier.Verify()

	if !result.Valid {
		t.Errorf("result.Valid = false for valid single node")
	}
	if result.TotalNodes != 1 {
		t.Errorf("result.TotalNodes = %d, want 1", result.TotalNodes)
	}
}

func TestTreeVerifier_Verify_CorruptedNodes(t *testing.T) {
	t.Parallel()

	left := makeLeafNode("/path/a.go", "content a")
	left.Hash = ComputeNodeHash(left)
	left.Hash[0] ^= 0xFF // Corrupt

	right := makeLeafNode("/path/c.go", "content c")
	right.Hash = ComputeNodeHash(right)

	root := makeInternalNode("/path/b.go", left, right)
	root.Hash = ComputeNodeHash(root)

	verifier := NewTreeVerifier(root)
	result := verifier.Verify()

	if result.Valid {
		t.Errorf("result.Valid = true for corrupted tree")
	}
	if result.TotalNodes != 3 {
		t.Errorf("result.TotalNodes = %d, want 3", result.TotalNodes)
	}
	if result.InvalidNodes != 1 {
		t.Errorf("result.InvalidNodes = %d, want 1", result.InvalidNodes)
	}
	if len(result.InvalidPaths) != 1 {
		t.Errorf("result.InvalidPaths = %v, want 1 entry", result.InvalidPaths)
	}
}

func TestTreeVerifier_Verify_MultipleCorruptions(t *testing.T) {
	t.Parallel()

	left := makeLeafNode("/path/a.go", "content a")
	left.Hash = ComputeNodeHash(left)
	left.Hash[0] ^= 0xFF

	right := makeLeafNode("/path/c.go", "content c")
	right.Hash = ComputeNodeHash(right)
	right.Hash[0] ^= 0xFF

	root := makeInternalNode("/path/b.go", left, right)
	root.Hash = ComputeNodeHash(root)
	root.Hash[0] ^= 0xFF

	verifier := NewTreeVerifier(root)
	result := verifier.Verify()

	if result.Valid {
		t.Errorf("result.Valid = true for corrupted tree")
	}
	if result.InvalidNodes != 3 {
		t.Errorf("result.InvalidNodes = %d, want 3", result.InvalidNodes)
	}
}

// =============================================================================
// Hash.IsZero Tests
// =============================================================================

func TestHash_IsZero_True(t *testing.T) {
	t.Parallel()

	var h Hash
	if !h.IsZero() {
		t.Errorf("zero hash should return true")
	}
}

func TestHash_IsZero_False(t *testing.T) {
	t.Parallel()

	h := makeHash("content")
	if h.IsZero() {
		t.Errorf("non-zero hash should return false")
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestComputeNodeHash_EmptyKey(t *testing.T) {
	t.Parallel()

	node := &Node{
		Key: "",
		Info: &FileInfo{
			ContentHash: makeHash("content"),
		},
	}

	hash := ComputeNodeHash(node)
	if hash.IsZero() {
		t.Errorf("hash should not be zero for empty key")
	}
}

func TestComputeNodeHash_LargeKey(t *testing.T) {
	t.Parallel()

	largeKey := make([]byte, 10000)
	for i := range largeKey {
		largeKey[i] = byte(i % 256)
	}

	node := &Node{
		Key: string(largeKey),
		Info: &FileInfo{
			ContentHash: makeHash("content"),
		},
	}

	hash := ComputeNodeHash(node)
	if hash.IsZero() {
		t.Errorf("hash should not be zero for large key")
	}
}

func TestVerifySubtree_SingleNodeTree(t *testing.T) {
	t.Parallel()

	node := makeLeafNode("/only.go", "content")
	node.Hash = ComputeNodeHash(node)

	valid, invalidPaths := VerifySubtree(node)

	if !valid {
		t.Errorf("single node tree should be valid")
	}
	if len(invalidPaths) != 0 {
		t.Errorf("invalidPaths should be empty")
	}
}

func TestUpdatePathHashes_OrderMatters(t *testing.T) {
	t.Parallel()

	leaf := makeLeafNode("/path/file.go", "content")
	root := makeInternalNode("/path", leaf, nil)

	// Correct order: leaf first, then root
	UpdatePathHashes([]*Node{leaf, root})

	leafHash := leaf.Hash
	rootHash := root.Hash

	// Reset hashes
	leaf.Hash = Hash{}
	root.Hash = Hash{}

	// Wrong order: root first (root will have wrong hash because leaf isn't updated yet)
	UpdatePathHashes([]*Node{root, leaf})

	// Root hash should be different because it was computed before leaf was updated
	if root.Hash == rootHash {
		t.Errorf("Order should affect final root hash")
	}

	// But leaf hash should be the same
	if leaf.Hash != leafHash {
		t.Errorf("Leaf hash should be the same regardless of order")
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkComputeNodeHash_Leaf(b *testing.B) {
	node := makeLeafNode("/path/to/file.go", "package main\n\nfunc main() {}\n")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ComputeNodeHash(node)
	}
}

func BenchmarkComputeNodeHash_WithChildren(b *testing.B) {
	left := makeLeafNode("/path/a.go", "content a")
	left.Hash = ComputeNodeHash(left)

	right := makeLeafNode("/path/c.go", "content c")
	right.Hash = ComputeNodeHash(right)

	node := makeInternalNode("/path/b.go", left, right)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ComputeNodeHash(node)
	}
}

func BenchmarkVerifyNodeHash(b *testing.B) {
	node := makeLeafNode("/path/to/file.go", "package main")
	node.Hash = ComputeNodeHash(node)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		VerifyNodeHash(node)
	}
}

func BenchmarkVerifySubtree_Small(b *testing.B) {
	left := makeLeafNode("/path/a.go", "content a")
	left.Hash = ComputeNodeHash(left)

	right := makeLeafNode("/path/c.go", "content c")
	right.Hash = ComputeNodeHash(right)

	root := makeInternalNode("/path/b.go", left, right)
	root.Hash = ComputeNodeHash(root)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		VerifySubtree(root)
	}
}

func BenchmarkTreeVerifier_Verify(b *testing.B) {
	// Build a larger tree
	nodes := make([]*Node, 15)
	for i := 0; i < 8; i++ {
		nodes[i] = makeLeafNode("/leaf"+string(rune('0'+i)), "content"+string(rune('0'+i)))
		nodes[i].Hash = ComputeNodeHash(nodes[i])
	}

	// Build internal nodes
	for i := 8; i < 12; i++ {
		leftIdx := (i - 8) * 2
		rightIdx := leftIdx + 1
		nodes[i] = makeInternalNode("/internal"+string(rune('0'+i-8)), nodes[leftIdx], nodes[rightIdx])
		nodes[i].Hash = ComputeNodeHash(nodes[i])
	}

	for i := 12; i < 14; i++ {
		leftIdx := 8 + (i-12)*2
		rightIdx := leftIdx + 1
		nodes[i] = makeInternalNode("/level2"+string(rune('0'+i-12)), nodes[leftIdx], nodes[rightIdx])
		nodes[i].Hash = ComputeNodeHash(nodes[i])
	}

	root := makeInternalNode("/root", nodes[12], nodes[13])
	root.Hash = ComputeNodeHash(root)

	verifier := NewTreeVerifier(root)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		verifier.Verify()
	}
}
