package cmt

// =============================================================================
// Split Operation
// =============================================================================

// Split divides a tree into two trees at the given key.
// Returns (left, right) where:
//   - left contains all nodes with key < splitKey
//   - right contains all nodes with key >= splitKey
//
// Both returned trees maintain BST and heap properties.
// Merkle hashes are recalculated for affected nodes.
func Split(node *Node, splitKey string) (left, right *Node) {
	if node == nil {
		return nil, nil
	}

	if splitKey <= node.Key {
		return splitLeft(node, splitKey)
	}
	return splitRight(node, splitKey)
}

// splitLeft handles the case where splitKey <= node.Key.
// The current node goes to the right subtree.
func splitLeft(node *Node, splitKey string) (left, right *Node) {
	leftPart, rightPart := Split(node.Left, splitKey)
	node.Left = rightPart
	updateHash(node)
	return leftPart, node
}

// splitRight handles the case where splitKey > node.Key.
// The current node goes to the left subtree.
func splitRight(node *Node, splitKey string) (left, right *Node) {
	leftPart, rightPart := Split(node.Right, splitKey)
	node.Right = leftPart
	updateHash(node)
	return node, rightPart
}

// =============================================================================
// Merge Operation
// =============================================================================

// Merge combines two trees into one.
// Precondition: all keys in left < all keys in right
// Returns a new tree maintaining BST and heap properties.
// Merkle hashes are recalculated for merged nodes.
func Merge(left, right *Node) *Node {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}

	if left.Priority > right.Priority {
		return mergeLeftDominant(left, right)
	}
	return mergeRightDominant(left, right)
}

// mergeLeftDominant handles merge when left has higher priority.
func mergeLeftDominant(left, right *Node) *Node {
	left.Right = Merge(left.Right, right)
	ensureChildHashesComputed(left)
	updateHash(left)
	return left
}

// mergeRightDominant handles merge when right has higher or equal priority.
func mergeRightDominant(left, right *Node) *Node {
	right.Left = Merge(left, right.Left)
	ensureChildHashesComputed(right)
	updateHash(right)
	return right
}

// ensureChildHashesComputed ensures both children have valid hashes.
func ensureChildHashesComputed(node *Node) {
	if node.Left != nil && node.Left.Hash.IsZero() {
		updateHash(node.Left)
	}
	if node.Right != nil && node.Right.Hash.IsZero() {
		updateHash(node.Right)
	}
}

// =============================================================================
// Insert Operation
// =============================================================================

// Insert adds a node to the tree using split and merge.
// Returns the new root.
func Insert(root *Node, newNode *Node) *Node {
	if newNode == nil {
		return root
	}

	// Ensure new node has computed hash
	updateHash(newNode)

	if root == nil {
		return newNode
	}

	// Split at new node's key, then merge: left + newNode + right
	left, right := Split(root, newNode.Key)
	return Merge(Merge(left, newNode), right)
}

// =============================================================================
// Delete Operation
// =============================================================================

// Delete removes a node by key using split and merge.
// Returns the new root (or nil if tree becomes empty).
func Delete(root *Node, key string) *Node {
	if root == nil {
		return nil
	}

	// Three-way split: left (< key), middle (= key), right (> key)
	left, greaterOrEqual := Split(root, key)
	_, right := splitGreaterThan(greaterOrEqual, key)

	// Discard the equal part (the node to delete)
	// splitGreaterThan already merged equal's children into right
	return Merge(left, right)
}

// splitGreaterThan splits a tree into nodes equal to key and nodes greater than key.
// Precondition: all nodes in tree have key >= splitKey
// Returns (equal, greater) where equal is the node with exact key match (isolated),
// and greater contains all nodes with key > splitKey.
func splitGreaterThan(node *Node, key string) (equal, greater *Node) {
	if node == nil {
		return nil, nil
	}

	if node.Key == key {
		// Found the match - isolate it and return its right subtree as greater
		rightSubtree := node.Right
		node.Right = nil
		leftSubtree := node.Left
		node.Left = nil
		updateHash(node)
		// The greater part is the right subtree of the matched node
		// The left subtree of the matched node also needs to be handled
		// but since precondition says all keys >= splitKey, left has keys < node.Key
		// which still >= splitKey, so they should go to greater as well
		// Actually, left subtree has keys < node.Key but >= splitKey, so they stay with equal
		// But we want to return equal isolated, so we merge left into greater
		return node, Merge(leftSubtree, rightSubtree)
	}

	// node.Key > key, so split recursively in left subtree
	if node.Key > key {
		eq, gt := splitGreaterThan(node.Left, key)
		node.Left = gt
		updateHash(node)
		return eq, node
	}

	// node.Key < key (shouldn't happen if precondition is met)
	return nil, node
}

// =============================================================================
// Get Operation
// =============================================================================

// Get finds a node by key.
// Returns nil if not found.
func Get(root *Node, key string) *Node {
	if root == nil {
		return nil
	}

	if key == root.Key {
		return root
	}

	if key < root.Key {
		return Get(root.Left, key)
	}
	return Get(root.Right, key)
}

// =============================================================================
// Internal Helpers
// =============================================================================

// updateHash recalculates and sets the Merkle hash for a node.
func updateHash(node *Node) {
	if node != nil {
		node.Hash = ComputeNodeHash(node)
	}
}
