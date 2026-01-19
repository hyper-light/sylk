package cmt

import (
	"crypto/sha256"
)

// =============================================================================
// Hash Computation
// =============================================================================

// ComputeNodeHash computes the Merkle hash for a single node.
// Formula: SHA256(left.Hash || key || info.ContentHash || right.Hash)
// For nil children or nil info, zero hashes are used.
func ComputeNodeHash(node *Node) Hash {
	if node == nil {
		return Hash{}
	}

	h := sha256.New()

	writeChildHash(h, node.Left)
	h.Write([]byte(node.Key))
	writeContentHash(h, node.Info)
	writeChildHash(h, node.Right)

	var result Hash
	copy(result[:], h.Sum(nil))
	return result
}

// writeChildHash writes a child's hash to the hasher, or zeros if nil.
func writeChildHash(h interface{ Write([]byte) (int, error) }, child *Node) {
	if child != nil {
		h.Write(child.Hash[:])
		return
	}
	h.Write(make([]byte, HashSize))
}

// writeContentHash writes content hash from FileInfo, or zeros if nil.
func writeContentHash(h interface{ Write([]byte) (int, error) }, info *FileInfo) {
	if info != nil {
		h.Write(info.ContentHash[:])
		return
	}
	h.Write(make([]byte, HashSize))
}

// =============================================================================
// Path Updates
// =============================================================================

// UpdatePathHashes updates hashes from leaf to root along a path.
// The path should be ordered from leaf to root.
// Nil nodes in the path are skipped.
func UpdatePathHashes(path []*Node) {
	for _, node := range path {
		if node == nil {
			continue
		}
		node.Hash = ComputeNodeHash(node)
	}
}

// =============================================================================
// Single Node Verification
// =============================================================================

// VerifyNodeHash verifies a single node's hash is correct.
// Returns true if the computed hash matches the stored hash.
// Nil nodes are considered valid.
func VerifyNodeHash(node *Node) bool {
	if node == nil {
		return true
	}
	return ComputeNodeHash(node) == node.Hash
}

// =============================================================================
// Subtree Verification
// =============================================================================

// VerifySubtree recursively verifies all hashes in a subtree.
// Returns (valid bool, invalidPaths []string).
func VerifySubtree(node *Node) (bool, []string) {
	if node == nil {
		return true, nil
	}

	var invalidPaths []string
	verifySubtreeRecursive(node, &invalidPaths)

	return len(invalidPaths) == 0, invalidPaths
}

// verifySubtreeRecursive is the recursive helper for VerifySubtree.
func verifySubtreeRecursive(node *Node, invalidPaths *[]string) {
	if node == nil {
		return
	}

	verifySubtreeRecursive(node.Left, invalidPaths)
	verifySubtreeRecursive(node.Right, invalidPaths)

	if !VerifyNodeHash(node) {
		*invalidPaths = append(*invalidPaths, node.Key)
	}
}

// =============================================================================
// Tree Verifier
// =============================================================================

// TreeVerifier provides full tree verification with detailed reporting.
type TreeVerifier struct {
	root *Node
}

// NewTreeVerifier creates a verifier for the given tree.
func NewTreeVerifier(root *Node) *TreeVerifier {
	return &TreeVerifier{root: root}
}

// VerificationResult contains the result of tree verification.
type VerificationResult struct {
	Valid        bool
	TotalNodes   int64
	InvalidNodes int64
	InvalidPaths []string
	Errors       []error
}

// Verify checks all nodes in the tree and returns a detailed result.
func (v *TreeVerifier) Verify() *VerificationResult {
	result := &VerificationResult{
		InvalidPaths: make([]string, 0),
		Errors:       make([]error, 0),
	}

	v.verifyRecursive(v.root, result)

	result.Valid = result.InvalidNodes == 0
	return result
}

// verifyRecursive traverses the tree and collects verification results.
func (v *TreeVerifier) verifyRecursive(node *Node, result *VerificationResult) {
	if node == nil {
		return
	}

	result.TotalNodes++

	v.verifyRecursive(node.Left, result)
	v.verifyRecursive(node.Right, result)

	if !VerifyNodeHash(node) {
		result.InvalidNodes++
		result.InvalidPaths = append(result.InvalidPaths, node.Key)
	}
}
