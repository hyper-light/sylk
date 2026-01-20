package vectorgraphdb

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/knowledge/relations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// VTC.8 - Integration Tests for TCQueryEngine
// =============================================================================

func setupTCQueryEngine(t *testing.T) (*TCQueryEngine, *VectorGraphDB, string) {
	db, path := setupTestDB(t)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	// Create test graph: A -> B -> C -> D, A -> C
	ns.InsertNode(&GraphNode{ID: "A", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "B", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{0.9, 0.2, 0.1})
	ns.InsertNode(&GraphNode{ID: "C", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{0.8, 0.3, 0.1})
	ns.InsertNode(&GraphNode{ID: "D", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{0.7, 0.4, 0.1})
	ns.InsertNode(&GraphNode{ID: "E", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{0.1, 0.1, 0.9})

	es.InsertEdge(&GraphEdge{SourceID: "A", TargetID: "B", EdgeType: EdgeTypeCalls})
	es.InsertEdge(&GraphEdge{SourceID: "B", TargetID: "C", EdgeType: EdgeTypeCalls})
	es.InsertEdge(&GraphEdge{SourceID: "C", TargetID: "D", EdgeType: EdgeTypeCalls})
	es.InsertEdge(&GraphEdge{SourceID: "A", TargetID: "C", EdgeType: EdgeTypeCalls})

	// Create TC index
	graph := map[string][]string{
		"A": {"B", "C"},
		"B": {"C"},
		"C": {"D"},
		"D": {},
		"E": {},
	}

	tcData := map[string]map[string]bool{
		"A": {"B": true, "C": true, "D": true},
		"B": {"C": true, "D": true},
		"C": {"D": true},
	}

	tcIndex := relations.NewVersionedIntervalIndex(graph, tcData)

	mock := newMockHNSWSearcher()
	mock.setResults([]HNSWSearchResult{
		{ID: "A", Similarity: 1.0},
		{ID: "B", Similarity: 0.9},
		{ID: "C", Similarity: 0.8},
		{ID: "D", Similarity: 0.7},
		{ID: "E", Similarity: 0.5},
	})
	mock.addVector("A", []float32{1, 0.1, 0.1})

	vs := NewVectorSearcher(db, mock)
	gt := NewGraphTraverser(db)
	tce := NewTCQueryEngine(db, vs, gt, tcIndex)

	return tce, db, path
}

func TestTCQueryEngine_Creation(t *testing.T) {
	tce, db, path := setupTCQueryEngine(t)
	defer cleanupDB(db, path)

	assert.NotNil(t, tce)
	assert.NotNil(t, tce.GetTCIndex())
	assert.Equal(t, uint64(1), tce.GetTCVersion())
}

func TestTCQueryEngine_IsReachable(t *testing.T) {
	tce, db, path := setupTCQueryEngine(t)
	defer cleanupDB(db, path)

	// Test reachable pairs
	assert.True(t, tce.IsReachable("A", "B"))
	assert.True(t, tce.IsReachable("A", "C"))
	assert.True(t, tce.IsReachable("A", "D"))
	assert.True(t, tce.IsReachable("B", "C"))
	assert.True(t, tce.IsReachable("B", "D"))
	assert.True(t, tce.IsReachable("C", "D"))

	// Test non-reachable pairs
	assert.False(t, tce.IsReachable("D", "A"))
	assert.False(t, tce.IsReachable("B", "A"))
	assert.False(t, tce.IsReachable("A", "E"))
	assert.False(t, tce.IsReachable("E", "A"))
}

func TestTCQueryEngine_IsReachable_NilIndex(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	ns.InsertNode(&GraphNode{ID: "A", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "B", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{0.9, 0.2, 0.1})
	es.InsertEdge(&GraphEdge{SourceID: "A", TargetID: "B", EdgeType: EdgeTypeCalls})

	mock := newMockHNSWSearcher()
	vs := NewVectorSearcher(db, mock)
	gt := NewGraphTraverser(db)

	// Create without TC index
	tce := NewTCQueryEngine(db, vs, gt, nil)

	// Should fall back to traversal
	assert.True(t, tce.IsReachable("A", "B"))
}

func TestTCQueryEngine_GetAllReachable(t *testing.T) {
	tce, db, path := setupTCQueryEngine(t)
	defer cleanupDB(db, path)

	// Test from A - should reach B, C, D
	reachableFromA := tce.GetAllReachable("A")
	assert.Len(t, reachableFromA, 3)
	assert.Contains(t, reachableFromA, "B")
	assert.Contains(t, reachableFromA, "C")
	assert.Contains(t, reachableFromA, "D")

	// Test from B - should reach C, D
	reachableFromB := tce.GetAllReachable("B")
	assert.Len(t, reachableFromB, 2)
	assert.Contains(t, reachableFromB, "C")
	assert.Contains(t, reachableFromB, "D")

	// Test from D - should reach nothing
	reachableFromD := tce.GetAllReachable("D")
	assert.Empty(t, reachableFromD)

	// Test from E (isolated) - should reach nothing
	reachableFromE := tce.GetAllReachable("E")
	assert.Empty(t, reachableFromE)
}

func TestTCQueryEngine_GetAllReachableNodes(t *testing.T) {
	tce, db, path := setupTCQueryEngine(t)
	defer cleanupDB(db, path)

	nodes, err := tce.GetAllReachableNodes("A")
	require.NoError(t, err)
	assert.Len(t, nodes, 3)

	nodeIDs := make(map[string]bool)
	for _, n := range nodes {
		nodeIDs[n.ID] = true
	}
	assert.True(t, nodeIDs["B"])
	assert.True(t, nodeIDs["C"])
	assert.True(t, nodeIDs["D"])
}

func TestTCQueryEngine_LookupInSnapshot(t *testing.T) {
	tce, db, path := setupTCQueryEngine(t)
	defer cleanupDB(db, path)

	// Test using snapshot lookup (uses TC data directly)
	assert.True(t, tce.LookupInSnapshot("A", "B"))
	assert.True(t, tce.LookupInSnapshot("A", "D"))
	assert.False(t, tce.LookupInSnapshot("D", "A"))
	assert.False(t, tce.LookupInSnapshot("E", "A"))
}

func TestTCQueryEngine_BatchLookup(t *testing.T) {
	tce, db, path := setupTCQueryEngine(t)
	defer cleanupDB(db, path)

	pairs := [][2]string{
		{"A", "B"},
		{"A", "C"},
		{"A", "D"},
		{"B", "A"},
		{"D", "A"},
		{"E", "A"},
	}

	results := tce.BatchLookup(pairs)

	assert.True(t, results["A->B"])
	assert.True(t, results["A->C"])
	assert.True(t, results["A->D"])
	assert.False(t, results["B->A"])
	assert.False(t, results["D->A"])
	assert.False(t, results["E->A"])
}

func TestTCQueryEngine_BatchLookup_NilIndex(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	ns.InsertNode(&GraphNode{ID: "A", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "B", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{0.9, 0.2, 0.1})
	es.InsertEdge(&GraphEdge{SourceID: "A", TargetID: "B", EdgeType: EdgeTypeCalls})

	mock := newMockHNSWSearcher()
	vs := NewVectorSearcher(db, mock)
	gt := NewGraphTraverser(db)
	tce := NewTCQueryEngine(db, vs, gt, nil)

	pairs := [][2]string{
		{"A", "B"},
		{"B", "A"},
	}

	results := tce.BatchLookup(pairs)

	// Should use fallback traversal
	assert.True(t, results["A->B"])
	assert.False(t, results["B->A"])
}

func TestTCQueryEngine_TCHybridQuery(t *testing.T) {
	tce, db, path := setupTCQueryEngine(t)
	defer cleanupDB(db, path)

	results, err := tce.TCHybridQuery([]float32{1, 0, 0}, []string{"A"}, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, results)
}

func TestTCQueryEngine_TCHybridQuery_WithReachabilityFilter(t *testing.T) {
	tce, db, path := setupTCQueryEngine(t)
	defer cleanupDB(db, path)

	opts := &TCHybridQueryOptions{
		HybridQueryOptions: &HybridQueryOptions{
			VectorLimit: 10,
		},
		UseTCReachability: true,
		TCSourceNode:      "A",
	}

	results, err := tce.TCHybridQuery([]float32{1, 0, 0}, []string{"A"}, opts)
	require.NoError(t, err)

	// Results should only include nodes reachable from A (B, C, D) + A itself
	for _, r := range results {
		if r.Node == nil {
			continue
		}
		nodeID := r.Node.ID
		// E is not reachable from A, so it should be filtered out
		assert.NotEqual(t, "E", nodeID, "E should be filtered out as it's not reachable from A")
	}
}

func TestTCQueryEngine_TCHybridQuery_WithBoost(t *testing.T) {
	tce, db, path := setupTCQueryEngine(t)
	defer cleanupDB(db, path)

	// First, run without boost
	optsNoBoost := &TCHybridQueryOptions{
		HybridQueryOptions: &HybridQueryOptions{
			VectorLimit: 10,
		},
		UseTCReachability: false,
		TCSourceNode:      "A",
		BoostReachable:    0, // No boost
	}

	resultsNoBoost, err := tce.TCHybridQuery([]float32{1, 0, 0}, []string{"A"}, optsNoBoost)
	require.NoError(t, err)

	// Then run with boost
	optsWithBoost := &TCHybridQueryOptions{
		HybridQueryOptions: &HybridQueryOptions{
			VectorLimit: 10,
		},
		UseTCReachability: false, // Don't filter, just boost
		TCSourceNode:      "A",
		BoostReachable:    1.5, // 50% boost
	}

	resultsWithBoost, err := tce.TCHybridQuery([]float32{1, 0, 0}, []string{"A"}, optsWithBoost)
	require.NoError(t, err)

	// Both should return results
	assert.NotEmpty(t, resultsNoBoost)
	assert.NotEmpty(t, resultsWithBoost)
}

func TestTCQueryEngine_GetReachabilityStats(t *testing.T) {
	tce, db, path := setupTCQueryEngine(t)
	defer cleanupDB(db, path)

	stats := tce.GetReachabilityStats("A")

	assert.Equal(t, "A", stats.Source)
	assert.True(t, stats.Available)
	assert.Equal(t, uint64(1), stats.Version)
	assert.Equal(t, 3, stats.ReachableCount) // B, C, D
}

func TestTCQueryEngine_GetReachabilityStats_NilIndex(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	mock := newMockHNSWSearcher()
	vs := NewVectorSearcher(db, mock)
	gt := NewGraphTraverser(db)
	tce := NewTCQueryEngine(db, vs, gt, nil)

	stats := tce.GetReachabilityStats("A")

	assert.Equal(t, "A", stats.Source)
	assert.False(t, stats.Available)
	assert.Equal(t, uint64(0), stats.Version)
}

func TestTCQueryEngine_SetTCIndex(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	mock := newMockHNSWSearcher()
	vs := NewVectorSearcher(db, mock)
	gt := NewGraphTraverser(db)
	tce := NewTCQueryEngine(db, vs, gt, nil)

	assert.Nil(t, tce.GetTCIndex())
	assert.Equal(t, uint64(0), tce.GetTCVersion())

	// Create and set TC index
	graph := map[string][]string{"A": {"B"}}
	tcData := map[string]map[string]bool{"A": {"B": true}}
	tcIndex := relations.NewVersionedIntervalIndex(graph, tcData)

	tce.SetTCIndex(tcIndex)

	assert.NotNil(t, tce.GetTCIndex())
	assert.Equal(t, uint64(1), tce.GetTCVersion())
}

// =============================================================================
// Concurrency Tests for Integration
// =============================================================================

func TestTCQueryEngine_ConcurrentReachability(t *testing.T) {
	tce, db, path := setupTCQueryEngine(t)
	defer cleanupDB(db, path)

	const numGoroutines = 50
	const iterations = 1000

	var wg sync.WaitGroup
	var errors atomic.Int64

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for j := 0; j < iterations; j++ {
				// Mix of different operations
				switch j % 4 {
				case 0:
					if !tce.IsReachable("A", "D") {
						errors.Add(1)
					}
				case 1:
					reachable := tce.GetAllReachable("A")
					if len(reachable) != 3 {
						errors.Add(1)
					}
				case 2:
					if !tce.LookupInSnapshot("B", "C") {
						errors.Add(1)
					}
				case 3:
					pairs := [][2]string{{"A", "B"}, {"A", "D"}}
					results := tce.BatchLookup(pairs)
					if !results["A->B"] || !results["A->D"] {
						errors.Add(1)
					}
				}
			}
		}()
	}

	wg.Wait()
	assert.Equal(t, int64(0), errors.Load(), "No errors should occur during concurrent operations")
}

func TestTCQueryEngine_ConcurrentQueriesWithIndexRebuild(t *testing.T) {
	tce, db, path := setupTCQueryEngine(t)
	defer cleanupDB(db, path)

	const numReaders = 20
	const numRebuilds = 50
	const readsPerReader = 500

	var wg sync.WaitGroup
	var errors atomic.Int64
	stop := make(chan struct{})

	// Start readers
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for j := 0; j < readsPerReader; j++ {
				select {
				case <-stop:
					return
				default:
				}

				// Perform TC operations
				_ = tce.IsReachable("A", "D")
				_ = tce.GetAllReachable("A")
				_ = tce.LookupInSnapshot("A", "B")

				// Verify consistency
				stats := tce.GetReachabilityStats("A")
				if stats.Available && stats.ReachableCount < 0 {
					errors.Add(1)
				}
			}
		}()
	}

	// Perform rebuilds
	wg.Add(1)
	go func() {
		defer wg.Done()

		graph := map[string][]string{
			"A": {"B", "C"},
			"B": {"C"},
			"C": {"D"},
			"D": {},
			"E": {},
		}

		tcData := map[string]map[string]bool{
			"A": {"B": true, "C": true, "D": true},
			"B": {"C": true, "D": true},
			"C": {"D": true},
		}

		for i := 0; i < numRebuilds; i++ {
			_ = tce.tcIndex.Rebuild(graph, tcData)
			time.Sleep(time.Millisecond)
		}
		close(stop)
	}()

	wg.Wait()
	assert.Equal(t, int64(0), errors.Load(), "No errors during concurrent reads and rebuilds")
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkTCQueryEngine_IsReachable(b *testing.B) {
	db, path := setupTestDB(&testing.T{})
	defer cleanupDB(db, path)

	graph := map[string][]string{
		"A": {"B", "C"},
		"B": {"C"},
		"C": {"D"},
		"D": {},
	}

	tcData := map[string]map[string]bool{
		"A": {"B": true, "C": true, "D": true},
		"B": {"C": true, "D": true},
		"C": {"D": true},
	}

	tcIndex := relations.NewVersionedIntervalIndex(graph, tcData)
	mock := newMockHNSWSearcher()
	vs := NewVectorSearcher(db, mock)
	gt := NewGraphTraverser(db)
	tce := NewTCQueryEngine(db, vs, gt, tcIndex)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = tce.IsReachable("A", "D")
	}
}

func BenchmarkTCQueryEngine_GetAllReachable(b *testing.B) {
	db, path := setupTestDB(&testing.T{})
	defer cleanupDB(db, path)

	graph := map[string][]string{
		"A": {"B", "C"},
		"B": {"C"},
		"C": {"D"},
		"D": {},
	}

	tcData := map[string]map[string]bool{
		"A": {"B": true, "C": true, "D": true},
		"B": {"C": true, "D": true},
		"C": {"D": true},
	}

	tcIndex := relations.NewVersionedIntervalIndex(graph, tcData)
	mock := newMockHNSWSearcher()
	vs := NewVectorSearcher(db, mock)
	gt := NewGraphTraverser(db)
	tce := NewTCQueryEngine(db, vs, gt, tcIndex)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = tce.GetAllReachable("A")
	}
}

func BenchmarkTCQueryEngine_BatchLookup(b *testing.B) {
	db, path := setupTestDB(&testing.T{})
	defer cleanupDB(db, path)

	graph := map[string][]string{
		"A": {"B", "C"},
		"B": {"C"},
		"C": {"D"},
		"D": {},
	}

	tcData := map[string]map[string]bool{
		"A": {"B": true, "C": true, "D": true},
		"B": {"C": true, "D": true},
		"C": {"D": true},
	}

	tcIndex := relations.NewVersionedIntervalIndex(graph, tcData)
	mock := newMockHNSWSearcher()
	vs := NewVectorSearcher(db, mock)
	gt := NewGraphTraverser(db)
	tce := NewTCQueryEngine(db, vs, gt, tcIndex)

	pairs := [][2]string{
		{"A", "B"},
		{"A", "C"},
		{"A", "D"},
		{"B", "C"},
		{"B", "D"},
		{"C", "D"},
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = tce.BatchLookup(pairs)
	}
}

func BenchmarkTCQueryEngine_ConcurrentReachability(b *testing.B) {
	db, path := setupTestDB(&testing.T{})
	defer cleanupDB(db, path)

	graph := map[string][]string{
		"A": {"B", "C"},
		"B": {"C"},
		"C": {"D"},
		"D": {},
	}

	tcData := map[string]map[string]bool{
		"A": {"B": true, "C": true, "D": true},
		"B": {"C": true, "D": true},
		"C": {"D": true},
	}

	tcIndex := relations.NewVersionedIntervalIndex(graph, tcData)
	mock := newMockHNSWSearcher()
	vs := NewVectorSearcher(db, mock)
	gt := NewGraphTraverser(db)
	tce := NewTCQueryEngine(db, vs, gt, tcIndex)

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = tce.IsReachable("A", "D")
		}
	})
}
