package vectorgraphdb

// SnapshotSearchResult represents a search result from a snapshot.
// This mirrors the hnsw.SearchResult without creating an import cycle.
type SnapshotSearchResult struct {
	ID         string
	Similarity float64
	Domain     Domain
	NodeType   NodeType
}

// SnapshotSearchFilter defines filter criteria for snapshot search.
type SnapshotSearchFilter struct {
	Domains       []Domain
	NodeTypes     []NodeType
	MinSimilarity float64
}

// Snapshot provides read-only access to a point-in-time index state.
type Snapshot interface {
	// Search performs k-nearest neighbor search on the snapshot.
	Search(query []float32, k int, filter *SnapshotSearchFilter) []SnapshotSearchResult
	// ID returns the unique identifier for this snapshot.
	ID() uint64
}

// SnapshotManager manages snapshot lifecycle (creation, release, GC).
type SnapshotManager interface {
	// CreateSnapshot captures the current index state.
	CreateSnapshot() Snapshot
	// ReleaseSnapshot marks a snapshot as no longer needed.
	ReleaseSnapshot(id uint64)
	// GCLoop runs garbage collection at regular intervals.
	GCLoop(ctx interface{})
	// SnapshotCount returns the number of active snapshots.
	SnapshotCount() int
}

// VectorIndex provides vector search and modification operations.
type VectorIndex interface {
	// Search performs k-nearest neighbor search.
	Search(query []float32, k int, filter *SnapshotSearchFilter) []SnapshotSearchResult
	// Insert adds a vector to the index.
	Insert(id string, vector []float32, domain Domain, nodeType NodeType) error
	// Delete removes a vector from the index.
	Delete(id string) error
	// Size returns the number of vectors in the index.
	Size() int
}
