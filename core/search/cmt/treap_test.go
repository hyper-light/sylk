package cmt

import (
	"crypto/sha256"
	"math/rand"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSplitEmptyTree verifies that splitting nil returns (nil, nil).
func TestSplitEmptyTree(t *testing.T) {
	left, right := Split(nil, "anykey")
	assert.Nil(t, left, "left subtree should be nil for empty tree")
	assert.Nil(t, right, "right subtree should be nil for empty tree")
}

// TestSplitSingleNodeByKeyLess verifies splitting a single node when key < node.Key.
func TestSplitSingleNodeByKeyLess(t *testing.T) {
	node := newTestNode("middle", 50)
	left, right := Split(node, "apple") // "apple" < "middle"

	assert.Nil(t, left, "left should be nil when split key < only node key")
	require.NotNil(t, right, "right should contain the node")
	assert.Equal(t, "middle", right.Key)
}

// TestSplitSingleNodeByKeyGreater verifies splitting a single node when key >= node.Key.
func TestSplitSingleNodeByKeyGreater(t *testing.T) {
	node := newTestNode("middle", 50)
	left, right := Split(node, "zebra") // "zebra" > "middle"

	require.NotNil(t, left, "left should contain the node")
	assert.Equal(t, "middle", left.Key)
	assert.Nil(t, right, "right should be nil when split key > only node key")
}

// TestSplitSingleNodeByKeyEqual verifies splitting a single node when key == node.Key.
func TestSplitSingleNodeByKeyEqual(t *testing.T) {
	node := newTestNode("middle", 50)
	left, right := Split(node, "middle") // exactly equal

	// When key == node.key, node goes to right subtree (key <= node.key branch)
	assert.Nil(t, left, "left should be nil when split key equals node key")
	require.NotNil(t, right, "right should contain the node when split key equals node key")
	assert.Equal(t, "middle", right.Key)
}

// TestSplitMultipleNodes verifies splitting a tree with multiple nodes.
func TestSplitMultipleNodes(t *testing.T) {
	// Build a small tree: insert nodes so we get a proper structure
	var root *Node
	keys := []string{"dog", "cat", "elephant", "bird", "fish"}
	for _, k := range keys {
		root = Insert(root, newTestNode(k, rand.Uint64()))
	}

	// Split at "dog"
	left, right := Split(root, "dog")

	// Verify all keys in left are < "dog"
	leftKeys := collectKeys(left)
	for _, k := range leftKeys {
		assert.True(t, k < "dog", "left tree should only contain keys < split key, found: %s", k)
	}

	// Verify all keys in right are >= "dog"
	rightKeys := collectKeys(right)
	for _, k := range rightKeys {
		assert.True(t, k >= "dog", "right tree should only contain keys >= split key, found: %s", k)
	}

	// Verify we haven't lost any keys
	allKeys := append(leftKeys, rightKeys...)
	sort.Strings(allKeys)
	sort.Strings(keys)
	assert.Equal(t, keys, allKeys, "split should preserve all keys")
}

// TestSplitMaintainsBSTProperty verifies the BST property is maintained after split.
func TestSplitMaintainsBSTProperty(t *testing.T) {
	var root *Node
	keys := []string{"mango", "apple", "banana", "cherry", "date", "fig", "grape"}
	for _, k := range keys {
		root = Insert(root, newTestNode(k, rand.Uint64()))
	}

	left, right := Split(root, "date")

	assert.True(t, verifyBSTProperty(left), "left subtree should maintain BST property")
	assert.True(t, verifyBSTProperty(right), "right subtree should maintain BST property")
}

// TestSplitRecalculatesHashes verifies Merkle hashes are recalculated on affected nodes.
func TestSplitRecalculatesHashes(t *testing.T) {
	var root *Node
	keys := []string{"b", "a", "c"}
	for _, k := range keys {
		root = Insert(root, newTestNode(k, rand.Uint64()))
	}

	// Record original hash
	originalHash := root.Hash

	// Split modifies the tree structure
	left, right := Split(root, "b")

	// Verify hashes are valid (non-zero) on resulting trees
	if left != nil {
		assert.NotEqual(t, [32]byte{}, left.Hash, "left root should have valid hash")
		assert.True(t, verifyHashIntegrity(left), "left tree hashes should be valid")
	}
	if right != nil {
		assert.NotEqual(t, [32]byte{}, right.Hash, "right root should have valid hash")
		assert.True(t, verifyHashIntegrity(right), "right tree hashes should be valid")
	}

	// The original root hash should differ from any new root hash
	// (because tree structure changed)
	if left != nil && right != nil {
		assert.NotEqual(t, originalHash, left.Hash, "left hash should differ from original")
		assert.NotEqual(t, originalHash, right.Hash, "right hash should differ from original")
	}
}

// TestMergeTwoNilTrees verifies merging nil with nil returns nil.
func TestMergeTwoNilTrees(t *testing.T) {
	result := Merge(nil, nil)
	assert.Nil(t, result, "merging two nil trees should return nil")
}

// TestMergeNilWithNonNil verifies merging nil with a tree returns the tree.
func TestMergeNilWithNonNil(t *testing.T) {
	node := newTestNode("test", 100)

	// nil + tree = tree
	result := Merge(nil, node)
	require.NotNil(t, result)
	assert.Equal(t, "test", result.Key)

	// tree + nil = tree
	node2 := newTestNode("test2", 200)
	result2 := Merge(node2, nil)
	require.NotNil(t, result2)
	assert.Equal(t, "test2", result2.Key)
}

// TestMergeTwoTreesMaintainsPriority verifies heap property after merge.
func TestMergeTwoTreesMaintainsPriority(t *testing.T) {
	// Create two separate trees that can be merged (left keys < right keys)
	var leftTree *Node
	leftKeys := []string{"apple", "apricot", "avocado"}
	for _, k := range leftKeys {
		leftTree = Insert(leftTree, newTestNode(k, rand.Uint64()))
	}

	var rightTree *Node
	rightKeys := []string{"banana", "blueberry", "blackberry"}
	for _, k := range rightKeys {
		rightTree = Insert(rightTree, newTestNode(k, rand.Uint64()))
	}

	merged := Merge(leftTree, rightTree)

	require.NotNil(t, merged)
	assert.True(t, verifyHeapProperty(merged), "merged tree should maintain heap property")
}

// TestMergeMaintainsBSTProperty verifies BST property after merge.
func TestMergeMaintainsBSTProperty(t *testing.T) {
	var leftTree *Node
	leftKeys := []string{"a", "b", "c"}
	for _, k := range leftKeys {
		leftTree = Insert(leftTree, newTestNode(k, rand.Uint64()))
	}

	var rightTree *Node
	rightKeys := []string{"x", "y", "z"}
	for _, k := range rightKeys {
		rightTree = Insert(rightTree, newTestNode(k, rand.Uint64()))
	}

	merged := Merge(leftTree, rightTree)

	require.NotNil(t, merged)
	assert.True(t, verifyBSTProperty(merged), "merged tree should maintain BST property")

	// Verify all keys are present
	allKeys := collectKeys(merged)
	sort.Strings(allKeys)
	expected := []string{"a", "b", "c", "x", "y", "z"}
	assert.Equal(t, expected, allKeys)
}

// TestMergeRecalculatesHashes verifies Merkle hashes are recalculated.
func TestMergeRecalculatesHashes(t *testing.T) {
	leftNode := newTestNode("a", 100)
	rightNode := newTestNode("z", 50)

	merged := Merge(leftNode, rightNode)

	require.NotNil(t, merged)
	assert.NotEqual(t, [32]byte{}, merged.Hash, "merged root should have valid hash")
	assert.True(t, verifyHashIntegrity(merged), "merged tree hashes should be valid")
}

// TestInsertIntoEmptyTree verifies inserting into nil creates a single-node tree.
func TestInsertIntoEmptyTree(t *testing.T) {
	node := newTestNode("first", 42)
	root := Insert(nil, node)

	require.NotNil(t, root)
	assert.Equal(t, "first", root.Key)
	assert.Nil(t, root.Left)
	assert.Nil(t, root.Right)
}

// TestInsertIntoPopulatedTree verifies inserting maintains tree properties.
func TestInsertIntoPopulatedTree(t *testing.T) {
	var root *Node
	keys := []string{"dog", "cat", "elephant"}
	for _, k := range keys {
		root = Insert(root, newTestNode(k, rand.Uint64()))
	}

	// Insert new key
	root = Insert(root, newTestNode("bird", rand.Uint64()))

	// Verify all keys present
	allKeys := collectKeys(root)
	sort.Strings(allKeys)
	assert.Equal(t, []string{"bird", "cat", "dog", "elephant"}, allKeys)

	// Verify properties
	assert.True(t, verifyBSTProperty(root), "tree should maintain BST property after insert")
	assert.True(t, verifyHeapProperty(root), "tree should maintain heap property after insert")
}

// TestInsertMaintainsOrdering verifies keys are properly ordered.
func TestInsertMaintainsOrdering(t *testing.T) {
	var root *Node
	keys := []string{"m", "a", "z", "k", "b", "y"}
	for _, k := range keys {
		root = Insert(root, newTestNode(k, rand.Uint64()))
	}

	// In-order traversal should give sorted keys
	inOrderKeys := collectKeysInOrder(root)
	expected := []string{"a", "b", "k", "m", "y", "z"}
	assert.Equal(t, expected, inOrderKeys, "in-order traversal should yield sorted keys")
}

// TestInsertDuplicateKey verifies behavior when inserting duplicate key.
func TestInsertDuplicateKey(t *testing.T) {
	var root *Node
	root = Insert(root, newTestNode("key", 100))
	root = Insert(root, newTestNode("key", 200)) // same key, different priority

	// Both should be in tree (treap allows duplicates in different positions)
	allKeys := collectKeys(root)
	assert.Len(t, allKeys, 2, "duplicate keys should both be in tree")
}

// TestDeleteFromSingleNodeTree verifies deleting the only node returns nil.
func TestDeleteFromSingleNodeTree(t *testing.T) {
	root := newTestNode("only", 50)
	root = Delete(root, "only")

	assert.Nil(t, root, "deleting only node should return nil")
}

// TestDeleteFromPopulatedTree verifies deleting maintains tree properties.
func TestDeleteFromPopulatedTree(t *testing.T) {
	var root *Node
	keys := []string{"dog", "cat", "elephant", "bird", "fish"}
	for _, k := range keys {
		root = Insert(root, newTestNode(k, rand.Uint64()))
	}

	// Delete "elephant"
	root = Delete(root, "elephant")

	// Verify "elephant" is gone
	allKeys := collectKeys(root)
	assert.NotContains(t, allKeys, "elephant")
	assert.Len(t, allKeys, 4)

	// Verify properties
	assert.True(t, verifyBSTProperty(root), "tree should maintain BST property after delete")
	assert.True(t, verifyHeapProperty(root), "tree should maintain heap property after delete")
}

// TestDeleteNonExistentKey verifies deleting non-existent key doesn't break tree.
func TestDeleteNonExistentKey(t *testing.T) {
	var root *Node
	keys := []string{"a", "b", "c"}
	for _, k := range keys {
		root = Insert(root, newTestNode(k, rand.Uint64()))
	}

	// Delete key that doesn't exist
	originalKeys := collectKeys(root)
	root = Delete(root, "nonexistent")

	// Tree should be unchanged
	newKeys := collectKeys(root)
	sort.Strings(originalKeys)
	sort.Strings(newKeys)
	assert.Equal(t, originalKeys, newKeys, "deleting non-existent key should not change tree")

	// Properties should still hold
	assert.True(t, verifyBSTProperty(root))
	assert.True(t, verifyHeapProperty(root))
}

// TestDeleteFromEmptyTree verifies deleting from nil doesn't panic.
func TestDeleteFromEmptyTree(t *testing.T) {
	root := Delete(nil, "anykey")
	assert.Nil(t, root, "deleting from empty tree should return nil")
}

// TestGetExistingKey verifies Get returns the correct node.
func TestGetExistingKey(t *testing.T) {
	var root *Node
	keys := []string{"dog", "cat", "elephant"}
	for _, k := range keys {
		root = Insert(root, newTestNode(k, rand.Uint64()))
	}

	// Get existing key
	found := Get(root, "cat")
	require.NotNil(t, found)
	assert.Equal(t, "cat", found.Key)
}

// TestGetNonExistentKey verifies Get returns nil for missing key.
func TestGetNonExistentKey(t *testing.T) {
	var root *Node
	keys := []string{"dog", "cat", "elephant"}
	for _, k := range keys {
		root = Insert(root, newTestNode(k, rand.Uint64()))
	}

	// Get non-existent key
	found := Get(root, "zebra")
	assert.Nil(t, found, "Get should return nil for non-existent key")
}

// TestGetFromEmptyTree verifies Get on nil returns nil.
func TestGetFromEmptyTree(t *testing.T) {
	found := Get(nil, "anykey")
	assert.Nil(t, found, "Get on empty tree should return nil")
}

// TestStressManyInsertsAndDeletes verifies invariants under stress.
func TestStressManyInsertsAndDeletes(t *testing.T) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	var root *Node
	inserted := make(map[string]bool)

	// Insert 100 random keys
	for i := 0; i < 100; i++ {
		key := randomKey(rng, 8)
		root = Insert(root, newTestNode(key, rng.Uint64()))
		inserted[key] = true

		// Verify after each insert
		assert.True(t, verifyBSTProperty(root), "BST property violated after insert %d", i)
		assert.True(t, verifyHeapProperty(root), "heap property violated after insert %d", i)
	}

	// Delete 50 random keys
	deleted := 0
	for key := range inserted {
		if deleted >= 50 {
			break
		}
		root = Delete(root, key)
		delete(inserted, key)
		deleted++

		// Verify after each delete
		if root != nil {
			assert.True(t, verifyBSTProperty(root), "BST property violated after delete %d", deleted)
			assert.True(t, verifyHeapProperty(root), "heap property violated after delete %d", deleted)
		}
	}

	// Verify remaining keys are findable
	for key := range inserted {
		found := Get(root, key)
		require.NotNil(t, found, "key %s should still be in tree", key)
		assert.Equal(t, key, found.Key)
	}
}

// TestSplitMergeRoundTrip verifies split then merge recovers original keys.
func TestSplitMergeRoundTrip(t *testing.T) {
	var root *Node
	keys := []string{"e", "b", "h", "a", "c", "f", "i", "d", "g"}
	for _, k := range keys {
		root = Insert(root, newTestNode(k, rand.Uint64()))
	}

	originalKeys := collectKeys(root)
	sort.Strings(originalKeys)

	// Split
	left, right := Split(root, "e")

	// Merge back
	merged := Merge(left, right)

	// Verify all keys recovered
	newKeys := collectKeys(merged)
	sort.Strings(newKeys)
	assert.Equal(t, originalKeys, newKeys, "split then merge should preserve all keys")

	// Verify properties
	assert.True(t, verifyBSTProperty(merged))
	assert.True(t, verifyHeapProperty(merged))
	assert.True(t, verifyHashIntegrity(merged))
}

// TestHashConsistency verifies that identical trees have identical hashes.
func TestHashConsistency(t *testing.T) {
	// Build two trees with same keys and priorities
	keys := []string{"a", "b", "c"}
	priorities := []uint64{100, 200, 50}

	var tree1 *Node
	for i, k := range keys {
		tree1 = Insert(tree1, newTestNode(k, priorities[i]))
	}

	var tree2 *Node
	for i, k := range keys {
		tree2 = Insert(tree2, newTestNode(k, priorities[i]))
	}

	// Root hashes should be identical
	assert.Equal(t, tree1.Hash, tree2.Hash, "identical trees should have identical root hashes")
}

// TestHeapPropertyWithSpecificPriorities verifies max-heap property explicitly.
func TestHeapPropertyWithSpecificPriorities(t *testing.T) {
	// Insert with known priorities - highest priority should be root
	root := Insert(nil, newTestNode("low", 10))
	root = Insert(root, newTestNode("high", 1000))
	root = Insert(root, newTestNode("medium", 100))

	// Root should have highest priority
	assert.Equal(t, uint64(1000), root.Priority, "root should have highest priority")
	assert.Equal(t, "high", root.Key)

	// Verify full heap property
	assert.True(t, verifyHeapProperty(root))
}

// --- Test Helpers ---

// newTestNode creates a Node with the given key and priority.
func newTestNode(key string, priority uint64) *Node {
	contentHash := sha256.Sum256([]byte(key))
	return &Node{
		Key:      key,
		Priority: priority,
		Info: &FileInfo{
			Path:        "/test/" + key,
			ContentHash: Hash(contentHash),
		},
	}
}

// collectKeys returns all keys in the tree (any order).
func collectKeys(node *Node) []string {
	if node == nil {
		return nil
	}
	keys := []string{node.Key}
	keys = append(keys, collectKeys(node.Left)...)
	keys = append(keys, collectKeys(node.Right)...)
	return keys
}

// collectKeysInOrder returns keys via in-order traversal (sorted).
func collectKeysInOrder(node *Node) []string {
	if node == nil {
		return nil
	}
	var keys []string
	keys = append(keys, collectKeysInOrder(node.Left)...)
	keys = append(keys, node.Key)
	keys = append(keys, collectKeysInOrder(node.Right)...)
	return keys
}

// verifyBSTProperty checks that all left children < node < all right children.
func verifyBSTProperty(node *Node) bool {
	return verifyBSTPropertyWithBounds(node, "", "")
}

func verifyBSTPropertyWithBounds(node *Node, minKey, maxKey string) bool {
	if node == nil {
		return true
	}
	if minKey != "" && node.Key <= minKey {
		return false
	}
	if maxKey != "" && node.Key >= maxKey {
		return false
	}
	return verifyBSTPropertyWithBounds(node.Left, minKey, node.Key) &&
		verifyBSTPropertyWithBounds(node.Right, node.Key, maxKey)
}

// verifyHeapProperty checks that parent priority >= child priorities.
func verifyHeapProperty(node *Node) bool {
	if node == nil {
		return true
	}
	if node.Left != nil && node.Left.Priority > node.Priority {
		return false
	}
	if node.Right != nil && node.Right.Priority > node.Priority {
		return false
	}
	return verifyHeapProperty(node.Left) && verifyHeapProperty(node.Right)
}

// verifyHashIntegrity checks that each node's hash is correctly computed.
func verifyHashIntegrity(node *Node) bool {
	if node == nil {
		return true
	}
	expectedHash := ComputeNodeHash(node)
	if node.Hash != expectedHash {
		return false
	}
	return verifyHashIntegrity(node.Left) && verifyHashIntegrity(node.Right)
}

// randomKey generates a random string of given length.
func randomKey(rng *rand.Rand, length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rng.Intn(len(charset))]
	}
	return string(b)
}
