// Package knowledge provides comprehensive performance benchmarks for the
// Knowledge Graph system components.
//
// PF.6.3: Knowledge Graph Performance Benchmark Suite
//
// This file contains benchmarks for:
//   - EntityLinker operations
//   - GraphTraverser path finding
//   - ActivationCache operations
//   - TokenSet operations
//
// Note: Rule evaluator and inference engine benchmarks are located in their
// respective packages (core/knowledge/inference) to avoid import cycles.
package knowledge

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/knowledge/memory"
	"github.com/adalundhe/sylk/core/knowledge/query"
)

// =============================================================================
// Test Fixtures
// =============================================================================

// createBenchEntities creates a slice of entities for benchmarking.
func createBenchEntities(count int) []ExtractedEntity {
	entities := make([]ExtractedEntity, count)
	for i := 0; i < count; i++ {
		entities[i] = ExtractedEntity{
			Name:      fmt.Sprintf("Entity%d", i),
			Kind:      EntityKindFunction,
			FilePath:  fmt.Sprintf("/test/file%d.go", i%10),
			StartLine: i,
			EndLine:   i + 10,
		}
	}
	return entities
}

// createBenchCamelCaseEntities creates entities with camelCase names.
func createBenchCamelCaseEntities(count int) []ExtractedEntity {
	prefixes := []string{"process", "handle", "validate", "create", "update"}
	suffixes := []string{"User", "Data", "Request", "Response", "Config"}

	entities := make([]ExtractedEntity, count)
	for i := 0; i < count; i++ {
		prefix := prefixes[i%len(prefixes)]
		suffix := suffixes[i%len(suffixes)]
		entities[i] = ExtractedEntity{
			Name:      fmt.Sprintf("%s%s%d", prefix, suffix, i),
			Kind:      EntityKindFunction,
			FilePath:  fmt.Sprintf("/test/file%d.go", i%10),
			StartLine: i,
			EndLine:   i + 10,
		}
	}
	return entities
}

// =============================================================================
// EntityLinker Benchmarks
// =============================================================================

// BenchmarkEntityLinker_IndexEntities benchmarks entity indexing.
func BenchmarkEntityLinker_IndexEntities(b *testing.B) {
	sizes := []int{100, 500, 1000}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("Size_%d", size), func(b *testing.B) {
			entities := createBenchEntities(size)
			linker := NewEntityLinker(nil)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				linker.Clear()
				linker.IndexEntities(entities)
			}
		})
	}
}

// BenchmarkEntityLinker_ResolveReference benchmarks reference resolution.
func BenchmarkEntityLinker_ResolveReference(b *testing.B) {
	linker := NewEntityLinker(nil)
	entities := createBenchEntities(1000)
	linker.IndexEntities(entities)

	contextEntity := ExtractedEntity{
		Name:     "TestContext",
		FilePath: "/test/file0.go",
		Kind:     EntityKindFunction,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		linker.ResolveReference("Entity500", contextEntity)
	}
}

// BenchmarkEntityLinker_FuzzyMatchScore benchmarks fuzzy matching.
func BenchmarkEntityLinker_FuzzyMatchScore(b *testing.B) {
	testCases := []struct {
		name string
		ref  string
		tgt  string
	}{
		{"ExactMatch", "processData", "processData"},
		{"CaseInsensitive", "processdata", "ProcessData"},
		{"Prefix", "proc", "processUserData"},
		{"CamelCaseSplit", "procUser", "processUserData"},
		{"LevenshteinClose", "prcessData", "processData"},
	}

	config := DefaultEntityLinkerConfig()
	config.CaseSensitive = false
	linker := NewEntityLinkerWithConfig(nil, config)

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				linker.fuzzyMatchScore(tc.ref, tc.tgt)
			}
		})
	}
}

// BenchmarkEntityLinker_GetEntitiesByName benchmarks name lookup.
func BenchmarkEntityLinker_GetEntitiesByName(b *testing.B) {
	linker := NewEntityLinker(nil)
	entities := createBenchEntities(1000)
	linker.IndexEntities(entities)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		linker.GetEntitiesByName("Entity500")
	}
}

// =============================================================================
// GraphTraverser Benchmarks
// =============================================================================

// benchEdgeQuerier implements query.EdgeQuerier for benchmarks.
type benchEdgeQuerier struct {
	nodes map[string][]query.Edge
}

func newBenchEdgeQuerier(nodeCount, edgesPerNode int) *benchEdgeQuerier {
	mock := &benchEdgeQuerier{
		nodes: make(map[string][]query.Edge),
	}

	for i := 0; i < nodeCount; i++ {
		nodeID := fmt.Sprintf("node%d", i)
		edges := make([]query.Edge, edgesPerNode)
		for j := 0; j < edgesPerNode; j++ {
			edges[j] = query.Edge{
				ID:       int64(i*edgesPerNode + j),
				SourceID: nodeID,
				TargetID: fmt.Sprintf("node%d", (i+j+1)%nodeCount),
				EdgeType: "calls",
				Weight:   1.0,
			}
		}
		mock.nodes[nodeID] = edges
	}

	return mock
}

func (m *benchEdgeQuerier) GetOutgoingEdges(nodeID string, _ []string) ([]query.Edge, error) {
	return m.nodes[nodeID], nil
}

func (m *benchEdgeQuerier) GetIncomingEdges(_ string, _ []string) ([]query.Edge, error) {
	return nil, nil
}

func (m *benchEdgeQuerier) GetNodesByPattern(pattern *query.NodeMatcher) ([]string, error) {
	if pattern != nil && pattern.Matches() {
		return []string{"node0"}, nil
	}
	return nil, nil
}

// BenchmarkGraphTraverser_Execute benchmarks graph traversal.
func BenchmarkGraphTraverser_Execute(b *testing.B) {
	testCases := []struct {
		name         string
		nodes        int
		edgesPerNode int
		maxHops      int
	}{
		{"Small_1Hop", 100, 3, 1},
		{"Small_2Hops", 100, 3, 2},
		{"Medium_1Hop", 500, 5, 1},
		{"Medium_2Hops", 500, 5, 2},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			mock := newBenchEdgeQuerier(tc.nodes, tc.edgesPerNode)
			traverser := query.NewGraphTraverser(mock)
			ctx := context.Background()

			entityType := "function"
			pattern := &query.GraphPattern{
				StartNode: &query.NodeMatcher{EntityType: &entityType},
				Traversals: []query.TraversalStep{
					{Direction: query.DirectionOutgoing, MaxHops: tc.maxHops},
				},
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				traverser.Execute(ctx, pattern, 10)
			}
		})
	}
}

// BenchmarkGraphTraverser_PathLength benchmarks path length impact.
func BenchmarkGraphTraverser_PathLength(b *testing.B) {
	maxHops := []int{1, 2, 3, 4}

	mock := newBenchEdgeQuerier(200, 4)
	traverser := query.NewGraphTraverser(mock)
	ctx := context.Background()

	for _, hops := range maxHops {
		b.Run(fmt.Sprintf("Hops_%d", hops), func(b *testing.B) {
			entityType := "function"
			pattern := &query.GraphPattern{
				StartNode: &query.NodeMatcher{EntityType: &entityType},
				Traversals: []query.TraversalStep{
					{Direction: query.DirectionOutgoing, MaxHops: hops},
				},
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				traverser.Execute(ctx, pattern, 20)
			}
		})
	}
}

// =============================================================================
// ActivationCache Benchmarks
// =============================================================================

// BenchmarkActivationCache_GetSetBench benchmarks cache get/set operations.
func BenchmarkActivationCache_GetSetBench(b *testing.B) {
	cache := memory.NewDefaultActivationCache()
	defer cache.Stop()

	now := time.Now()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		nodeID := fmt.Sprintf("node%d", i%100)
		cache.Set(nodeID, now, float64(i)*0.1)
		cache.Get(nodeID, now)
	}
}

// BenchmarkActivationCache_GetOrComputeBench benchmarks cached computation.
func BenchmarkActivationCache_GetOrComputeBench(b *testing.B) {
	cache := memory.NewDefaultActivationCache()
	defer cache.Stop()

	now := time.Now()
	computeCount := 0

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		nodeID := fmt.Sprintf("node%d", i%50)
		cache.GetOrCompute(nodeID, now, func() float64 {
			computeCount++
			return float64(computeCount) * 0.1
		})
	}
}

// BenchmarkActivationCache_Batch benchmarks batch operations.
func BenchmarkActivationCache_Batch(b *testing.B) {
	batchSizes := []int{10, 50, 100}

	for _, size := range batchSizes {
		b.Run(fmt.Sprintf("BatchSize_%d", size), func(b *testing.B) {
			cache := memory.NewDefaultActivationCache()
			defer cache.Stop()

			now := time.Now()
			nodeIDs := make([]string, size)
			activations := make(map[string]float64, size)
			for i := 0; i < size; i++ {
				nodeIDs[i] = fmt.Sprintf("node%d", i)
				activations[nodeIDs[i]] = float64(i) * 0.1
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				cache.SetBatch(activations, now)
				cache.GetBatch(nodeIDs, now)
			}
		})
	}
}

// BenchmarkActivationCache_LRUEviction benchmarks eviction performance.
func BenchmarkActivationCache_LRUEviction(b *testing.B) {
	config := memory.SmallActivationCacheConfig()
	cache := memory.NewActivationCache(config)
	defer cache.Stop()

	now := time.Now()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		nodeID := fmt.Sprintf("node%d", i)
		cache.Set(nodeID, now, float64(i)*0.1)
	}
}

// =============================================================================
// TokenSet Benchmarks
// =============================================================================

// BenchmarkTokenSet_FromStringBench benchmarks string tokenization.
func BenchmarkTokenSet_FromStringBench(b *testing.B) {
	testStrings := []struct {
		name string
		str  string
	}{
		{"Short", "hello"},
		{"Medium", "process user data validation"},
		{"Long", "this is a longer string with multiple words for tokenization testing"},
		{"CamelCase", "processUserDataValidationHandler"},
	}

	for _, tc := range testStrings {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				FromString(tc.str)
			}
		})
	}
}

// BenchmarkTokenSet_OverlapBench benchmarks Jaccard similarity computation.
func BenchmarkTokenSet_OverlapBench(b *testing.B) {
	testCases := []struct {
		name string
		s1   string
		s2   string
	}{
		{"Identical", "process data", "process data"},
		{"Partial", "process user data", "validate user input"},
		{"Different", "hello world", "foo bar baz"},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			ts1 := FromString(tc.s1)
			ts2 := FromString(tc.s2)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ts1.Overlap(ts2)
			}
		})
	}
}

// BenchmarkTokenSet_IntersectionBench benchmarks set intersection.
func BenchmarkTokenSet_IntersectionBench(b *testing.B) {
	sizes := []int{10, 50, 100}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("Size_%d", size), func(b *testing.B) {
			ts1 := NewTokenSetWithCapacity(size)
			ts2 := NewTokenSetWithCapacity(size)

			for i := 0; i < size; i++ {
				ts1.Add(fmt.Sprintf("token%d", i))
				ts2.Add(fmt.Sprintf("token%d", i+size/2))
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ts1.Intersection(ts2)
			}
		})
	}
}

// BenchmarkTokenSet_QuickRejectBench benchmarks pre-filtering.
func BenchmarkTokenSet_QuickRejectBench(b *testing.B) {
	ts1 := FromString("process user data validation")
	ts2 := FromString("handle request processing")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ts1.QuickReject(ts2, 0.3)
	}
}

// BenchmarkTokenSet_CommonTokenCountBench benchmarks common token counting.
func BenchmarkTokenSet_CommonTokenCountBench(b *testing.B) {
	sizes := []int{10, 50, 100}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("Size_%d", size), func(b *testing.B) {
			ts1 := NewTokenSetWithCapacity(size)
			ts2 := NewTokenSetWithCapacity(size)

			for i := 0; i < size; i++ {
				ts1.Add(fmt.Sprintf("token%d", i))
				ts2.Add(fmt.Sprintf("token%d", i+size/3))
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ts1.CommonTokenCount(ts2)
			}
		})
	}
}

// BenchmarkTokenSetFromIdentifierBench benchmarks code identifier tokenization.
func BenchmarkTokenSetFromIdentifierBench(b *testing.B) {
	identifiers := []string{
		"processUserData",
		"handleHTTPRequest",
		"validate_input_data",
		"GetUserByID",
		"process_and_validate_user_request_data",
	}

	for _, id := range identifiers {
		b.Run(id, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				TokenSetFromIdentifier(id)
			}
		})
	}
}

// =============================================================================
// Combined Workflow Benchmarks
// =============================================================================

// BenchmarkEntityLinker_IndexAndResolve benchmarks full index and resolve cycle.
func BenchmarkEntityLinker_IndexAndResolve(b *testing.B) {
	entities := createBenchCamelCaseEntities(500)
	contextEntity := ExtractedEntity{
		Name:     "TestContext",
		FilePath: "/test/file0.go",
		Kind:     EntityKindFunction,
	}

	config := DefaultEntityLinkerConfig()
	config.CaseSensitive = false

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		linker := NewEntityLinkerWithConfig(nil, config)
		linker.IndexEntities(entities)

		linker.ResolveReference("processUser100", contextEntity)
		linker.ResolveReference("handleData200", contextEntity)
		linker.ResolveReference("validateConfig300", contextEntity)
	}
}

// BenchmarkTokenSet_PrefilterCandidatesBench benchmarks candidate filtering.
func BenchmarkTokenSet_PrefilterCandidatesBench(b *testing.B) {
	candidateCounts := []int{100, 500, 1000}

	for _, count := range candidateCounts {
		b.Run(fmt.Sprintf("Candidates_%d", count), func(b *testing.B) {
			queryTS := FromString("process user data validation")

			candidates := make([]string, count)
			for i := 0; i < count; i++ {
				candidates[i] = fmt.Sprintf("handle request %d", i)
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				PrefilterCandidates(queryTS, candidates, func(s string) TokenSet {
					return FromString(s)
				}, 0.2)
			}
		})
	}
}
