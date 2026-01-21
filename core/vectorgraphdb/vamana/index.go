// Package vamana implements the DiskANN/Vamana algorithm for approximate nearest
// neighbor search.

package vamana

// VamanaIndex defines the full interface for Vamana index operations.
// It combines insertion, deletion, search, and lifecycle management into
// a single interface suitable for complete index implementations.
//
// Implementations must be safe for concurrent use from multiple goroutines.
// The interface is designed as a drop-in replacement for HNSW index consumers
// in the vectorgraphdb package.
type VamanaIndex interface {
	VamanaInserter
	VamanaSearcher

	// Delete removes a node from the index by its external ID.
	// Returns an error if the node does not exist or deletion fails.
	// Implementations should clean up all graph connections referencing the node.
	Delete(id string) error

	// Size returns the number of nodes currently in the index.
	// This count reflects committed insertions minus deletions.
	Size() int

	// Close releases all resources held by the index.
	// After Close returns, the index must not be used.
	// Implementations should flush any pending writes before returning.
	Close() error
}

// VamanaInserter defines the interface for batch insert operations.
// This subset interface enables write-only access patterns useful for
// bulk loading or streaming ingestion pipelines.
//
// Implementations must be safe for concurrent use from multiple goroutines.
type VamanaInserter interface {
	// Insert adds a new vector to the index with associated metadata.
	// The id is the external string identifier (typically UUID or content hash).
	// The vector must have consistent dimensionality across all insertions.
	// Domain and nodeType provide filtering metadata for search operations.
	//
	// If a node with the same id already exists, implementations may either
	// update the existing node or return an error, depending on configuration.
	Insert(id string, vector []float32, domain uint8, nodeType uint16) error

	// Size returns the number of nodes currently in the index.
	Size() int
}

// VamanaSearcher defines the interface for search and retrieval operations.
// This subset interface enables read-only access patterns useful for
// search services that don't need write capabilities.
//
// Implementations must be safe for concurrent use from multiple goroutines.
type VamanaSearcher interface {
	// Search finds the k nearest neighbors to the query vector.
	// Results are returned sorted by similarity in descending order.
	// The filter parameter may be nil to search without filtering.
	// Returns an empty slice if the index is empty or no matches pass the filter.
	Search(query []float32, k int, filter *SearchFilter) []SearchResult

	// GetVector retrieves the stored vector for a node by its external ID.
	// Returns an error if the node does not exist.
	// The returned slice is a copy; modifications do not affect the index.
	GetVector(id string) ([]float32, error)
}
