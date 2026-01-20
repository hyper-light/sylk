package hnsw

import (
	"sync"
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTestIndex(t *testing.T) *Index {
	t.Helper()
	idx := New(DefaultConfig())
	return idx
}

func createPopulatedIndex(t *testing.T) *Index {
	t.Helper()
	idx := New(DefaultConfig())

	vectors := []struct {
		id       string
		vec      []float32
		domain   vectorgraphdb.Domain
		nodeType vectorgraphdb.NodeType
	}{
		{"vec1", []float32{1.0, 0.0, 0.0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFunction},
		{"vec2", []float32{0.9, 0.1, 0.0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFunction},
		{"vec3", []float32{0.0, 1.0, 0.0}, vectorgraphdb.DomainHistory, vectorgraphdb.NodeTypeHistoryEntry},
		{"vec4", []float32{0.0, 0.9, 0.1}, vectorgraphdb.DomainHistory, vectorgraphdb.NodeTypeSession},
		{"vec5", []float32{0.0, 0.0, 1.0}, vectorgraphdb.DomainAcademic, vectorgraphdb.NodeTypePaper},
	}

	for _, v := range vectors {
		err := idx.Insert(v.id, v.vec, v.domain, v.nodeType)
		require.NoError(t, err)
	}

	return idx
}

func TestSnapshot_Search_EmptySnapshot(t *testing.T) {
	snap := NewHNSWSnapshot(nil, 1)

	results := snap.Search([]float32{1.0, 0.0, 0.0}, 5, nil)
	assert.Nil(t, results)
}

func TestSnapshot_Search_KZero(t *testing.T) {
	idx := createPopulatedIndex(t)
	snap := NewHNSWSnapshot(idx, 1)

	results := snap.Search([]float32{1.0, 0.0, 0.0}, 0, nil)
	assert.Nil(t, results)
}

func TestSnapshot_Search_NegativeK(t *testing.T) {
	idx := createPopulatedIndex(t)
	snap := NewHNSWSnapshot(idx, 1)

	results := snap.Search([]float32{1.0, 0.0, 0.0}, -1, nil)
	assert.Nil(t, results)
}

func TestSnapshot_Search_EmptyQuery(t *testing.T) {
	idx := createPopulatedIndex(t)
	snap := NewHNSWSnapshot(idx, 1)

	results := snap.Search([]float32{}, 5, nil)
	assert.Nil(t, results)
}

func TestSnapshot_Search_NilQuery(t *testing.T) {
	idx := createPopulatedIndex(t)
	snap := NewHNSWSnapshot(idx, 1)

	results := snap.Search(nil, 5, nil)
	assert.Nil(t, results)
}

func TestSnapshot_Search_ZeroMagnitudeQuery(t *testing.T) {
	idx := createPopulatedIndex(t)
	snap := NewHNSWSnapshot(idx, 1)

	results := snap.Search([]float32{0.0, 0.0, 0.0}, 5, nil)
	assert.Nil(t, results)
}

func TestSnapshot_Search_BasicSearch(t *testing.T) {
	idx := createPopulatedIndex(t)
	snap := NewHNSWSnapshot(idx, 1)

	results := snap.Search([]float32{1.0, 0.0, 0.0}, 2, nil)

	require.NotNil(t, results)
	require.Len(t, results, 2)
	// vec1 should be the closest (exact match)
	assert.Equal(t, "vec1", results[0].ID)
	// vec2 should be second (0.9, 0.1, 0.0) is close to (1.0, 0.0, 0.0)
	assert.Equal(t, "vec2", results[1].ID)
}

func TestSnapshot_Search_ReturnsMetadata(t *testing.T) {
	idx := createPopulatedIndex(t)
	snap := NewHNSWSnapshot(idx, 1)

	results := snap.Search([]float32{1.0, 0.0, 0.0}, 1, nil)

	require.Len(t, results, 1)
	assert.Equal(t, vectorgraphdb.DomainCode, results[0].Domain)
	assert.Equal(t, vectorgraphdb.NodeTypeFunction, results[0].NodeType)
}

func TestSnapshot_Search_SameResultsAsLiveSearch_Quiescent(t *testing.T) {
	idx := createPopulatedIndex(t)

	// Get live search results
	liveResults := idx.Search([]float32{1.0, 0.0, 0.0}, 5, nil)

	// Get snapshot search results
	snap := NewHNSWSnapshot(idx, 1)
	snapResults := snap.Search([]float32{1.0, 0.0, 0.0}, 5, nil)

	require.Equal(t, len(liveResults), len(snapResults))
	for i := range liveResults {
		assert.Equal(t, liveResults[i].ID, snapResults[i].ID)
		assert.InDelta(t, liveResults[i].Similarity, snapResults[i].Similarity, 0.0001)
	}
}

func TestSnapshot_Search_UnaffectedByConcurrentInserts(t *testing.T) {
	idx := createPopulatedIndex(t)

	// Create snapshot before concurrent inserts
	snap := NewHNSWSnapshot(idx, 1)
	initialSize := snap.Size()

	// Perform concurrent inserts
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			vec := []float32{float32(id) * 0.1, 0.5, 0.5}
			err := idx.Insert(
				"concurrent"+string(rune('A'+id)),
				vec,
				vectorgraphdb.DomainCode,
				vectorgraphdb.NodeTypeFunction,
			)
			assert.NoError(t, err)
		}(i)
	}
	wg.Wait()

	// Verify snapshot size unchanged
	assert.Equal(t, initialSize, snap.Size())

	// Verify snapshot search still works and doesn't see new inserts
	results := snap.Search([]float32{0.5, 0.5, 0.5}, 10, nil)
	for _, r := range results {
		assert.NotContains(t, r.ID, "concurrent")
	}

	// Verify live index sees the new inserts
	assert.Greater(t, idx.Size(), initialSize)
}

func TestSnapshot_Search_FilterByDomain(t *testing.T) {
	idx := createPopulatedIndex(t)
	snap := NewHNSWSnapshot(idx, 1)

	filter := &SearchFilter{
		Domains: []vectorgraphdb.Domain{vectorgraphdb.DomainCode},
	}

	results := snap.Search([]float32{0.5, 0.5, 0.5}, 10, filter)

	for _, r := range results {
		assert.Equal(t, vectorgraphdb.DomainCode, r.Domain)
	}
}

func TestSnapshot_Search_FilterByNodeType(t *testing.T) {
	idx := createPopulatedIndex(t)
	snap := NewHNSWSnapshot(idx, 1)

	filter := &SearchFilter{
		NodeTypes: []vectorgraphdb.NodeType{vectorgraphdb.NodeTypeFunction},
	}

	results := snap.Search([]float32{0.5, 0.5, 0.5}, 10, filter)

	for _, r := range results {
		assert.Equal(t, vectorgraphdb.NodeTypeFunction, r.NodeType)
	}
}

func TestSnapshot_Search_FilterByMinSimilarity(t *testing.T) {
	idx := createPopulatedIndex(t)
	snap := NewHNSWSnapshot(idx, 1)

	filter := &SearchFilter{
		MinSimilarity: 0.9,
	}

	results := snap.Search([]float32{1.0, 0.0, 0.0}, 10, filter)

	for _, r := range results {
		assert.GreaterOrEqual(t, r.Similarity, 0.9)
	}
}

func TestSnapshot_Search_FilterCombined(t *testing.T) {
	idx := createPopulatedIndex(t)
	snap := NewHNSWSnapshot(idx, 1)

	filter := &SearchFilter{
		Domains:       []vectorgraphdb.Domain{vectorgraphdb.DomainCode},
		NodeTypes:     []vectorgraphdb.NodeType{vectorgraphdb.NodeTypeFunction},
		MinSimilarity: 0.5,
	}

	results := snap.Search([]float32{0.5, 0.5, 0.5}, 10, filter)

	for _, r := range results {
		assert.Equal(t, vectorgraphdb.DomainCode, r.Domain)
		assert.Equal(t, vectorgraphdb.NodeTypeFunction, r.NodeType)
		assert.GreaterOrEqual(t, r.Similarity, 0.5)
	}
}

func TestSnapshot_Search_FilterExcludesAll(t *testing.T) {
	idx := createPopulatedIndex(t)
	snap := NewHNSWSnapshot(idx, 1)

	// Filter for a domain that doesn't exist in results
	filter := &SearchFilter{
		MinSimilarity: 0.999,
	}

	// Search for something that won't have exact matches
	results := snap.Search([]float32{0.33, 0.33, 0.33}, 10, filter)

	// Should have no results since nothing has similarity >= 0.999
	assert.Empty(t, results)
}

func TestSnapshot_Search_KLargerThanResults(t *testing.T) {
	idx := createPopulatedIndex(t)
	snap := NewHNSWSnapshot(idx, 1)

	results := snap.Search([]float32{1.0, 0.0, 0.0}, 100, nil)

	// Should return all vectors (5 in test index)
	assert.LessOrEqual(t, len(results), 5)
}

func TestSnapshot_greedySearchLayer_OutOfBoundsLevel(t *testing.T) {
	idx := createPopulatedIndex(t)
	snap := NewHNSWSnapshot(idx, 1)

	// Try to search at level beyond what exists
	result := snap.greedySearchLayer([]float32{1.0, 0.0, 0.0}, 1.0, snap.EntryPoint, 100)
	assert.Equal(t, snap.EntryPoint, result)
}

func TestSnapshot_distance_MissingVector(t *testing.T) {
	snap := &HNSWSnapshot{
		Vectors:    make(map[string][]float32),
		Magnitudes: make(map[string]float64),
	}

	dist := snap.distance([]float32{1.0, 0.0}, 1.0, "nonexistent")
	assert.Equal(t, 2.0, dist) // max cosine distance
}

func TestSnapshot_distance_MissingMagnitude(t *testing.T) {
	snap := &HNSWSnapshot{
		Vectors:    map[string][]float32{"test": {1.0, 0.0}},
		Magnitudes: make(map[string]float64),
	}

	dist := snap.distance([]float32{1.0, 0.0}, 1.0, "test")
	assert.Equal(t, 2.0, dist) // max cosine distance
}

func TestSnapshot_Search_ConcurrentReads(t *testing.T) {
	idx := createPopulatedIndex(t)
	snap := NewHNSWSnapshot(idx, 1)

	var wg sync.WaitGroup
	numReaders := 100

	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results := snap.Search([]float32{1.0, 0.0, 0.0}, 5, nil)
			assert.NotNil(t, results)
			assert.NotEmpty(t, results)
		}()
	}

	wg.Wait()
}

func TestSnapshot_getDomain_NilDomainsMap(t *testing.T) {
	snap := &HNSWSnapshot{
		Domains: nil,
	}

	domain := snap.getDomain("any")
	assert.Equal(t, vectorgraphdb.DomainCode, domain)
}

func TestSnapshot_getNodeType_NilNodeTypesMap(t *testing.T) {
	snap := &HNSWSnapshot{
		NodeTypes: nil,
	}

	nodeType := snap.getNodeType("any")
	assert.Equal(t, vectorgraphdb.NodeTypeFile, nodeType)
}

func TestSnapshot_getDomain_MissingID(t *testing.T) {
	snap := &HNSWSnapshot{
		Domains: map[string]vectorgraphdb.Domain{
			"exists": vectorgraphdb.DomainHistory,
		},
	}

	domain := snap.getDomain("nonexistent")
	assert.Equal(t, vectorgraphdb.DomainCode, domain)
}

func TestSnapshot_getNodeType_MissingID(t *testing.T) {
	snap := &HNSWSnapshot{
		NodeTypes: map[string]vectorgraphdb.NodeType{
			"exists": vectorgraphdb.NodeTypeFunction,
		},
	}

	nodeType := snap.getNodeType("nonexistent")
	assert.Equal(t, vectorgraphdb.NodeTypeFile, nodeType)
}

func TestSnapshot_Search_NoEntryPoint(t *testing.T) {
	snap := &HNSWSnapshot{
		EntryPoint: "",
		MaxLevel:   0,
		Vectors:    map[string][]float32{"vec1": {1.0, 0.0}},
		Magnitudes: map[string]float64{"vec1": 1.0},
		Layers:     []LayerSnapshot{{Nodes: map[string][]string{"vec1": {}}}},
	}

	results := snap.Search([]float32{1.0, 0.0}, 5, nil)
	assert.Nil(t, results)
}

func TestSnapshot_isValidSearchInput(t *testing.T) {
	tests := []struct {
		name       string
		entryPoint string
		query      []float32
		k          int
		expected   bool
	}{
		{"empty entry point", "", []float32{1.0}, 5, false},
		{"empty query", "ep", []float32{}, 5, false},
		{"nil query", "ep", nil, 5, false},
		{"k zero", "ep", []float32{1.0}, 0, false},
		{"k negative", "ep", []float32{1.0}, -1, false},
		{"valid", "ep", []float32{1.0}, 5, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snap := &HNSWSnapshot{EntryPoint: tc.entryPoint}
			result := snap.isValidSearchInput(tc.query, tc.k)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestSnapshot_getLayerNeighbors(t *testing.T) {
	snap := &HNSWSnapshot{
		Layers: []LayerSnapshot{
			{Nodes: map[string][]string{"node1": {"neighbor1", "neighbor2"}}},
		},
	}

	t.Run("valid level and node", func(t *testing.T) {
		neighbors := snap.getLayerNeighbors("node1", 0)
		assert.Equal(t, []string{"neighbor1", "neighbor2"}, neighbors)
	})

	t.Run("level out of bounds", func(t *testing.T) {
		neighbors := snap.getLayerNeighbors("node1", 5)
		assert.Nil(t, neighbors)
	})

	t.Run("missing node", func(t *testing.T) {
		neighbors := snap.getLayerNeighbors("nonexistent", 0)
		assert.Nil(t, neighbors)
	})
}

func TestSnapshot_findCloserNeighbor(t *testing.T) {
	snap := &HNSWSnapshot{
		Vectors:    map[string][]float32{"n1": {1.0, 0.0}, "n2": {0.5, 0.5}},
		Magnitudes: map[string]float64{"n1": 1.0, "n2": 0.707},
	}

	query := []float32{1.0, 0.0}
	queryMag := 1.0

	t.Run("finds closer neighbor", func(t *testing.T) {
		improved, newNode, newDist := snap.findCloserNeighbor(query, queryMag, []string{"n1"}, 1.0)
		assert.True(t, improved)
		assert.Equal(t, "n1", newNode)
		assert.Less(t, newDist, 1.0)
	})

	t.Run("no improvement", func(t *testing.T) {
		improved, _, _ := snap.findCloserNeighbor(query, queryMag, []string{"n2"}, 0.0)
		assert.False(t, improved)
	})

	t.Run("empty neighbors", func(t *testing.T) {
		improved, _, _ := snap.findCloserNeighbor(query, queryMag, []string{}, 0.5)
		assert.False(t, improved)
	})
}

func TestSnapshot_passesMinSimilarity(t *testing.T) {
	snap := &HNSWSnapshot{}

	assert.True(t, snap.passesMinSimilarity(0.8, 0))
	assert.True(t, snap.passesMinSimilarity(0.8, -1))
	assert.True(t, snap.passesMinSimilarity(0.8, 0.8))
	assert.True(t, snap.passesMinSimilarity(0.9, 0.8))
	assert.False(t, snap.passesMinSimilarity(0.7, 0.8))
}

func TestSnapshot_passesDomainFilter(t *testing.T) {
	snap := &HNSWSnapshot{
		Domains: map[string]vectorgraphdb.Domain{
			"code":    vectorgraphdb.DomainCode,
			"history": vectorgraphdb.DomainHistory,
		},
	}

	assert.True(t, snap.passesDomainFilter("code", nil))
	assert.True(t, snap.passesDomainFilter("code", []vectorgraphdb.Domain{}))
	assert.True(t, snap.passesDomainFilter("code", []vectorgraphdb.Domain{vectorgraphdb.DomainCode}))
	assert.False(t, snap.passesDomainFilter("code", []vectorgraphdb.Domain{vectorgraphdb.DomainHistory}))
}

func TestSnapshot_passesNodeTypeFilter(t *testing.T) {
	snap := &HNSWSnapshot{
		NodeTypes: map[string]vectorgraphdb.NodeType{
			"func":  vectorgraphdb.NodeTypeFunction,
			"paper": vectorgraphdb.NodeTypePaper,
		},
	}

	assert.True(t, snap.passesNodeTypeFilter("func", nil))
	assert.True(t, snap.passesNodeTypeFilter("func", []vectorgraphdb.NodeType{}))
	assert.True(t, snap.passesNodeTypeFilter("func", []vectorgraphdb.NodeType{vectorgraphdb.NodeTypeFunction}))
	assert.False(t, snap.passesNodeTypeFilter("func", []vectorgraphdb.NodeType{vectorgraphdb.NodeTypePaper}))
}

func TestSnapshot_createSearchResult(t *testing.T) {
	snap := &HNSWSnapshot{
		Vectors:    map[string][]float32{"exists": {1.0, 0.0}},
		Magnitudes: map[string]float64{"exists": 1.0},
	}

	t.Run("existing vector", func(t *testing.T) {
		result := snap.createSearchResult([]float32{1.0, 0.0}, 1.0, "exists")
		require.NotNil(t, result)
		assert.Equal(t, "exists", result.ID)
		assert.InDelta(t, 1.0, result.Similarity, 0.0001)
	})

	t.Run("missing vector", func(t *testing.T) {
		result := snap.createSearchResult([]float32{1.0, 0.0}, 1.0, "missing")
		assert.Nil(t, result)
	})

	t.Run("missing magnitude", func(t *testing.T) {
		snap.Vectors["nomag"] = []float32{1.0, 0.0}
		result := snap.createSearchResult([]float32{1.0, 0.0}, 1.0, "nomag")
		assert.Nil(t, result)
	})
}

func TestSnapshot_sortCandidates(t *testing.T) {
	snap := &HNSWSnapshot{}

	candidates := []SearchResult{
		{ID: "low", Similarity: 0.3},
		{ID: "high", Similarity: 0.9},
		{ID: "mid", Similarity: 0.6},
	}

	sorted := snap.sortCandidates(candidates)

	assert.Equal(t, "high", sorted[0].ID)
	assert.Equal(t, "mid", sorted[1].ID)
	assert.Equal(t, "low", sorted[2].ID)
}

// W4P.7: Tests for efSearch configuration in snapshot search

func TestSnapshot_EfSearch_PassedFromIndex(t *testing.T) {
	// Create index with custom efSearch
	cfg := DefaultConfig()
	cfg.EfSearch = 200
	idx := New(cfg)

	err := idx.Insert("vec1", []float32{1.0, 0.0, 0.0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFunction)
	require.NoError(t, err)

	// Create snapshot and verify efSearch is passed
	snap := NewHNSWSnapshot(idx, 1)
	assert.Equal(t, 200, snap.EfSearch)
}

func TestSnapshot_EfSearch_DefaultWhenNotSet(t *testing.T) {
	snap := &HNSWSnapshot{
		EfSearch: 0, // Not set
	}

	// When EfSearch is 0, should use max(k*2, DefaultEfSearch)
	// For k=10, max(20, 50) = 50
	efSearch := snap.getEffectiveEfSearch(10)
	assert.Equal(t, vectorgraphdb.DefaultEfSearch, efSearch)

	// For k=100, max(200, 50) = 200
	efSearch = snap.getEffectiveEfSearch(100)
	assert.Equal(t, 200, efSearch)
}

func TestSnapshot_EfSearch_UsesConfiguredValue(t *testing.T) {
	snap := &HNSWSnapshot{
		EfSearch: 150,
	}

	// Should use configured value regardless of k
	assert.Equal(t, 150, snap.getEffectiveEfSearch(10))
	assert.Equal(t, 150, snap.getEffectiveEfSearch(100))
	assert.Equal(t, 150, snap.getEffectiveEfSearch(1000))
}

func TestSnapshot_Search_QualityWithConfiguredEfSearch(t *testing.T) {
	// Create index with specific efSearch
	cfg := DefaultConfig()
	cfg.EfSearch = 100
	idx := New(cfg)

	// Insert enough vectors to make efSearch relevant
	for i := 0; i < 50; i++ {
		vec := []float32{
			float32(i) * 0.02,
			float32(50-i) * 0.02,
			0.5,
		}
		err := idx.Insert(
			"vec"+string(rune('A'+i)),
			vec,
			vectorgraphdb.DomainCode,
			vectorgraphdb.NodeTypeFunction,
		)
		require.NoError(t, err)
	}

	query := []float32{0.5, 0.5, 0.5}
	k := 10

	// Get snapshot search results (should use efSearch=100 from index)
	snap := NewHNSWSnapshot(idx, 1)
	assert.Equal(t, 100, snap.EfSearch, "Snapshot should have efSearch from index")
	assert.Equal(t, 100, snap.getEffectiveEfSearch(k), "Effective efSearch should be configured value")

	snapResults := snap.Search(query, k, nil)

	// Verify search returns valid results
	require.NotEmpty(t, snapResults, "Search should return results")
	require.LessOrEqual(t, len(snapResults), k, "Should not exceed k results")

	// Results should be sorted by similarity
	for i := 1; i < len(snapResults); i++ {
		assert.GreaterOrEqual(t, snapResults[i-1].Similarity, snapResults[i].Similarity,
			"Results should be sorted by descending similarity")
	}
}

func TestSnapshot_getEffectiveEfSearch(t *testing.T) {
	tests := []struct {
		name       string
		efSearch   int
		k          int
		wantResult int
	}{
		{
			name:       "uses configured value when set",
			efSearch:   150,
			k:          10,
			wantResult: 150,
		},
		{
			name:       "uses default when not set and k is small",
			efSearch:   0,
			k:          10,
			wantResult: vectorgraphdb.DefaultEfSearch,
		},
		{
			name:       "uses k*2 when not set and k*2 > default",
			efSearch:   0,
			k:          100,
			wantResult: 200,
		},
		{
			name:       "configured value overrides k*2 logic",
			efSearch:   50,
			k:          100,
			wantResult: 50,
		},
		{
			name:       "handles k=1",
			efSearch:   0,
			k:          1,
			wantResult: vectorgraphdb.DefaultEfSearch,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snap := &HNSWSnapshot{EfSearch: tc.efSearch}
			result := snap.getEffectiveEfSearch(tc.k)
			assert.Equal(t, tc.wantResult, result)
		})
	}
}

func TestSnapshot_EfSearch_NilIndexUsesZero(t *testing.T) {
	// When creating snapshot from nil index, EfSearch should be 0
	snap := NewHNSWSnapshot(nil, 1)
	assert.Equal(t, 0, snap.EfSearch)

	// Effective efSearch should fall back to default behavior
	efSearch := snap.getEffectiveEfSearch(10)
	assert.Equal(t, vectorgraphdb.DefaultEfSearch, efSearch)
}

// W4P.16: Tests for entry point validation per-layer during search

func TestSnapshot_hasValidEntryVector(t *testing.T) {
	tests := []struct {
		name       string
		vectors    map[string][]float32
		magnitudes map[string]float64
		nodeID     string
		expected   bool
	}{
		{
			name:       "valid entry with both vector and magnitude",
			vectors:    map[string][]float32{"node1": {1.0, 0.0}},
			magnitudes: map[string]float64{"node1": 1.0},
			nodeID:     "node1",
			expected:   true,
		},
		{
			name:       "missing vector",
			vectors:    map[string][]float32{},
			magnitudes: map[string]float64{"node1": 1.0},
			nodeID:     "node1",
			expected:   false,
		},
		{
			name:       "missing magnitude",
			vectors:    map[string][]float32{"node1": {1.0, 0.0}},
			magnitudes: map[string]float64{},
			nodeID:     "node1",
			expected:   false,
		},
		{
			name:       "both missing",
			vectors:    map[string][]float32{},
			magnitudes: map[string]float64{},
			nodeID:     "node1",
			expected:   false,
		},
		{
			name:       "different node exists",
			vectors:    map[string][]float32{"other": {1.0, 0.0}},
			magnitudes: map[string]float64{"other": 1.0},
			nodeID:     "node1",
			expected:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snap := &HNSWSnapshot{
				Vectors:    tc.vectors,
				Magnitudes: tc.magnitudes,
			}
			result := snap.hasValidEntryVector(tc.nodeID)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestSnapshot_isValidLayer(t *testing.T) {
	snap := &HNSWSnapshot{
		Layers: []LayerSnapshot{
			{Nodes: map[string][]string{}},
			{Nodes: map[string][]string{}},
		},
	}

	tests := []struct {
		name     string
		level    int
		expected bool
	}{
		{"valid layer 0", 0, true},
		{"valid layer 1", 1, true},
		{"out of bounds positive", 2, false},
		{"out of bounds negative", -1, false},
		{"large out of bounds", 100, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := snap.isValidLayer(tc.level)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestSnapshot_nodeExistsAtLayer(t *testing.T) {
	snap := &HNSWSnapshot{
		Layers: []LayerSnapshot{
			{Nodes: map[string][]string{"node1": {"n2"}, "node2": {}}},
			{Nodes: map[string][]string{"node1": {}}},
		},
	}

	tests := []struct {
		name     string
		nodeID   string
		level    int
		expected bool
	}{
		{"node exists at layer 0", "node1", 0, true},
		{"node exists at layer 1", "node1", 1, true},
		{"node2 exists at layer 0 only", "node2", 0, true},
		{"node2 missing at layer 1", "node2", 1, false},
		{"nonexistent node", "node3", 0, false},
		{"invalid layer", "node1", 5, false},
		{"negative layer", "node1", -1, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := snap.nodeExistsAtLayer(tc.nodeID, tc.level)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestSnapshot_searchLayerWithValidation_ValidEntry(t *testing.T) {
	snap := &HNSWSnapshot{
		Vectors:    map[string][]float32{"node1": {1.0, 0.0}, "node2": {0.9, 0.1}},
		Magnitudes: map[string]float64{"node1": 1.0, "node2": 0.906},
		Layers: []LayerSnapshot{
			{Nodes: map[string][]string{"node1": {"node2"}, "node2": {"node1"}}},
			{Nodes: map[string][]string{"node1": {}}},
		},
	}

	query := []float32{0.9, 0.1}
	queryMag := 0.906

	// Should perform normal search and potentially find a better node
	result := snap.searchLayerWithValidation(query, queryMag, "node1", 0)
	assert.NotEmpty(t, result)
}

func TestSnapshot_searchLayerWithValidation_InvalidLayer(t *testing.T) {
	snap := &HNSWSnapshot{
		Layers: []LayerSnapshot{
			{Nodes: map[string][]string{"node1": {}}},
		},
	}

	// Should return entry unchanged when layer is invalid
	result := snap.searchLayerWithValidation([]float32{1.0}, 1.0, "node1", 5)
	assert.Equal(t, "node1", result)
}

func TestSnapshot_searchLayerWithValidation_MissingEntryAtLayer(t *testing.T) {
	snap := &HNSWSnapshot{
		Vectors:    map[string][]float32{"node1": {1.0, 0.0}},
		Magnitudes: map[string]float64{"node1": 1.0},
		Layers: []LayerSnapshot{
			{Nodes: map[string][]string{"node1": {}}},
			{Nodes: map[string][]string{}}, // node1 missing at layer 1
		},
	}

	// Should return entry unchanged when node missing at layer
	result := snap.searchLayerWithValidation([]float32{1.0, 0.0}, 1.0, "node1", 1)
	assert.Equal(t, "node1", result)
}

func TestSnapshot_Search_EntryPointVectorMissing(t *testing.T) {
	snap := &HNSWSnapshot{
		EntryPoint: "node1",
		MaxLevel:   0,
		Vectors:    map[string][]float32{}, // Entry point vector missing
		Magnitudes: map[string]float64{"node1": 1.0},
		Layers: []LayerSnapshot{
			{Nodes: map[string][]string{"node1": {}}},
		},
	}

	results := snap.Search([]float32{1.0, 0.0}, 5, nil)
	assert.Nil(t, results)
}

func TestSnapshot_Search_EntryPointMagnitudeMissing(t *testing.T) {
	snap := &HNSWSnapshot{
		EntryPoint: "node1",
		MaxLevel:   0,
		Vectors:    map[string][]float32{"node1": {1.0, 0.0}},
		Magnitudes: map[string]float64{}, // Entry point magnitude missing
		Layers: []LayerSnapshot{
			{Nodes: map[string][]string{"node1": {}}},
		},
	}

	results := snap.Search([]float32{1.0, 0.0}, 5, nil)
	assert.Nil(t, results)
}

func TestSnapshot_Search_ValidEntryAtAllLayers(t *testing.T) {
	// Happy path: valid entry point at all layers
	idx := createPopulatedIndex(t)
	snap := NewHNSWSnapshot(idx, 1)

	results := snap.Search([]float32{1.0, 0.0, 0.0}, 3, nil)

	require.NotNil(t, results)
	assert.NotEmpty(t, results)
	// Verify results are sorted by similarity
	for i := 1; i < len(results); i++ {
		assert.GreaterOrEqual(t, results[i-1].Similarity, results[i].Similarity)
	}
}

func TestSnapshot_Search_EntryMissingAtSomeLayer(t *testing.T) {
	// Entry point exists in vectors but missing at layer 1
	snap := &HNSWSnapshot{
		EntryPoint: "node1",
		MaxLevel:   1,
		Vectors: map[string][]float32{
			"node1": {1.0, 0.0},
			"node2": {0.9, 0.1},
		},
		Magnitudes: map[string]float64{
			"node1": 1.0,
			"node2": 0.906,
		},
		Layers: []LayerSnapshot{
			{Nodes: map[string][]string{"node1": {"node2"}, "node2": {"node1"}}},
			{Nodes: map[string][]string{"node2": {}}}, // node1 missing at layer 1
		},
	}

	// Should still work - skips layer 1 and proceeds to layer 0
	results := snap.Search([]float32{1.0, 0.0}, 2, nil)
	require.NotNil(t, results)
	assert.NotEmpty(t, results)
}

func TestSnapshot_Search_EmptyLayer(t *testing.T) {
	// Layer exists but has no nodes
	snap := &HNSWSnapshot{
		EntryPoint: "node1",
		MaxLevel:   1,
		Vectors:    map[string][]float32{"node1": {1.0, 0.0}},
		Magnitudes: map[string]float64{"node1": 1.0},
		Layers: []LayerSnapshot{
			{Nodes: map[string][]string{"node1": {}}},
			{Nodes: map[string][]string{}}, // Empty layer
		},
	}

	// Should handle empty layer gracefully
	results := snap.Search([]float32{1.0, 0.0}, 2, nil)
	require.NotNil(t, results)
	assert.Len(t, results, 1)
	assert.Equal(t, "node1", results[0].ID)
}

func TestSnapshot_Search_SingleLayer(t *testing.T) {
	snap := &HNSWSnapshot{
		EntryPoint: "node1",
		MaxLevel:   0,
		Vectors: map[string][]float32{
			"node1": {1.0, 0.0},
			"node2": {0.9, 0.1},
		},
		Magnitudes: map[string]float64{
			"node1": 1.0,
			"node2": 0.906,
		},
		Layers: []LayerSnapshot{
			{Nodes: map[string][]string{"node1": {"node2"}, "node2": {"node1"}}},
		},
	}

	results := snap.Search([]float32{1.0, 0.0}, 2, nil)
	require.NotNil(t, results)
	assert.Len(t, results, 2)
}

func TestSnapshot_Search_NoLayers(t *testing.T) {
	snap := &HNSWSnapshot{
		EntryPoint: "node1",
		MaxLevel:   -1,
		Vectors:    map[string][]float32{"node1": {1.0, 0.0}},
		Magnitudes: map[string]float64{"node1": 1.0},
		Layers:     []LayerSnapshot{},
	}

	// With no layers but valid entry vector data, returns the entry point
	// since initializeCandidates uses vector data directly (not layer data)
	results := snap.Search([]float32{1.0, 0.0}, 2, nil)
	require.NotNil(t, results)
	assert.Len(t, results, 1)
	assert.Equal(t, "node1", results[0].ID)
}

func TestSnapshot_Search_ValidEntryButNoNeighbors(t *testing.T) {
	// Entry point valid but has no neighbors at any layer
	snap := &HNSWSnapshot{
		EntryPoint: "node1",
		MaxLevel:   1,
		Vectors:    map[string][]float32{"node1": {1.0, 0.0}},
		Magnitudes: map[string]float64{"node1": 1.0},
		Layers: []LayerSnapshot{
			{Nodes: map[string][]string{"node1": {}}},
			{Nodes: map[string][]string{"node1": {}}},
		},
	}

	results := snap.Search([]float32{1.0, 0.0}, 5, nil)
	require.NotNil(t, results)
	assert.Len(t, results, 1)
	assert.Equal(t, "node1", results[0].ID)
}

func TestSnapshot_Search_ConcurrentWithEntryValidation(t *testing.T) {
	idx := createPopulatedIndex(t)
	snap := NewHNSWSnapshot(idx, 1)

	var wg sync.WaitGroup
	numReaders := 50

	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			// Vary the query to exercise different paths
			query := []float32{float32(id%3) * 0.5, float32((id+1)%3) * 0.5, float32((id+2)%3) * 0.5}
			results := snap.Search(query, 3, nil)
			// Should always return valid results from a properly built snapshot
			assert.NotNil(t, results)
		}(i)
	}

	wg.Wait()
}

func TestSnapshot_isValidLayer_EmptyLayers(t *testing.T) {
	snap := &HNSWSnapshot{
		Layers: []LayerSnapshot{},
	}

	assert.False(t, snap.isValidLayer(0))
	assert.False(t, snap.isValidLayer(-1))
	assert.False(t, snap.isValidLayer(1))
}

func TestSnapshot_nodeExistsAtLayer_EmptyLayers(t *testing.T) {
	snap := &HNSWSnapshot{
		Layers: []LayerSnapshot{},
	}

	assert.False(t, snap.nodeExistsAtLayer("any", 0))
}
