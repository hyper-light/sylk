// Package testing provides end-to-end performance regression tests for PF.6.6.
// Tests measure latencies at each pipeline stage and compare against baseline thresholds.
package testing

import (
	"context"
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/knowledge/inference"
	"github.com/adalundhe/sylk/core/knowledge/query"
	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/adalundhe/sylk/core/vectorgraphdb/hnsw"
)

// =============================================================================
// Baseline Thresholds (configurable via environment variables)
// =============================================================================

// thresholds holds configurable performance baselines.
type thresholds struct {
	VectorSearchMS int64 // Vector search: <10ms for k=10 on 10000 vectors
	TextSearchMS   int64 // Text search: <50ms for simple query
	FusionMS       int64 // Fusion: <5ms for combining 100 results
	InferenceMS    int64 // Inference: <100ms for 5-hop graph traversal
}

// getThresholds reads thresholds from environment or uses defaults.
func getThresholds() thresholds {
	return thresholds{
		VectorSearchMS: getEnvInt64("REGRESSION_VECTOR_SEARCH_MS", 10),
		TextSearchMS:   getEnvInt64("REGRESSION_TEXT_SEARCH_MS", 50),
		FusionMS:       getEnvInt64("REGRESSION_FUSION_MS", 5),
		InferenceMS:    getEnvInt64("REGRESSION_INFERENCE_MS", 100),
	}
}

// getEnvInt64 reads an int64 from environment or returns default.
func getEnvInt64(key string, defaultVal int64) int64 {
	if val := os.Getenv(key); val != "" {
		if parsed, err := strconv.ParseInt(val, 10, 64); err == nil {
			return parsed
		}
	}
	return defaultVal
}

// =============================================================================
// Test Helpers
// =============================================================================

// skipIfEnvSet skips the test if SKIP_REGRESSION_TESTS is set.
func skipIfEnvSet(t *testing.T) {
	if os.Getenv("SKIP_REGRESSION_TESTS") != "" {
		t.Skip("Skipping regression tests: SKIP_REGRESSION_TESTS is set")
	}
}

// memoryHighWaterMark returns current allocated memory in bytes.
func memoryHighWaterMark() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.Alloc
}

// reportMemoryDelta logs memory usage change between two measurements.
func reportMemoryDelta(t *testing.T, before, after uint64) {
	if after >= before {
		t.Logf("Memory delta: +%d bytes", after-before)
	} else {
		t.Logf("Memory delta: -%d bytes (GC occurred)", before-after)
	}
}

// reportLatency logs latency and checks against threshold.
func reportLatency(t *testing.T, name string, latency, threshold time.Duration) {
	t.Logf("%s latency: %v (threshold: %v)", name, latency, threshold)
	if latency > 2*threshold {
		t.Errorf("%s exceeded 2x baseline: %v > %v", name, latency, 2*threshold)
	}
}

// generateTestVector creates a normalized test vector of given dimension.
func generateTestVector(dim int, seed float32) []float32 {
	vec := make([]float32, dim)
	for i := range vec {
		vec[i] = seed + float32(i)*0.01
	}
	hnsw.NormalizeVector(vec)
	return vec
}

// =============================================================================
// Performance Regression Tests
// =============================================================================

// TestRegressionVectorSearch tests vector search performance on 10000 vectors.
func TestRegressionVectorSearch(t *testing.T) {
	skipIfEnvSet(t)
	th := getThresholds()

	t.Run("VectorSearch_10000_k10", func(t *testing.T) {
		memBefore := memoryHighWaterMark()

		// Setup: Create HNSW index with 10000 vectors
		index := createTestIndex(t, 10000, 128)

		// Measure search latency
		queryVec := generateTestVector(128, 0.5)
		start := time.Now()
		results := index.Search(queryVec, 10, nil)
		latency := time.Since(start)

		memAfter := memoryHighWaterMark()
		reportMemoryDelta(t, memBefore, memAfter)

		if len(results) == 0 {
			t.Fatal("Vector search returned no results")
		}

		threshold := time.Duration(th.VectorSearchMS) * time.Millisecond
		reportLatency(t, "VectorSearch", latency, threshold)
	})
}

// createTestIndex builds an HNSW index with n vectors.
func createTestIndex(t *testing.T, n, dim int) *hnsw.Index {
	cfg := hnsw.DefaultConfig()
	cfg.Dimension = dim
	index := hnsw.New(cfg)

	for i := range n {
		vec := generateTestVector(dim, float32(i)*0.001)
		id := "node-" + strconv.Itoa(i)
		if err := index.Insert(id, vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFunction); err != nil {
			t.Fatalf("Failed to insert vector %d: %v", i, err)
		}
	}
	return index
}

// TestRegressionTextSearch tests text search performance.
func TestRegressionTextSearch(t *testing.T) {
	skipIfEnvSet(t)
	th := getThresholds()

	t.Run("TextSearch_SimpleQuery", func(t *testing.T) {
		memBefore := memoryHighWaterMark()

		// Setup: Create mock text results
		textResults := createMockTextResults(100)

		// Measure simulated text search processing latency
		start := time.Now()
		processed := processTextResults(textResults)
		latency := time.Since(start)

		memAfter := memoryHighWaterMark()
		reportMemoryDelta(t, memBefore, memAfter)

		if len(processed) == 0 {
			t.Fatal("Text search processing returned no results")
		}

		threshold := time.Duration(th.TextSearchMS) * time.Millisecond
		reportLatency(t, "TextSearch", latency, threshold)
	})
}

// createMockTextResults generates n mock text search results.
func createMockTextResults(n int) []query.TextResult {
	results := make([]query.TextResult, n)
	for i := range n {
		results[i] = query.TextResult{
			ID:      "doc-" + strconv.Itoa(i),
			Score:   1.0 - float64(i)*0.01,
			Content: "Test content for document " + strconv.Itoa(i),
		}
	}
	return results
}

// processTextResults simulates text result processing.
func processTextResults(results []query.TextResult) []query.TextResult {
	processed := make([]query.TextResult, 0, len(results))
	for _, r := range results {
		if r.Score > 0.1 {
			processed = append(processed, r)
		}
	}
	return processed
}

// TestRegressionFusion tests RRF fusion performance.
func TestRegressionFusion(t *testing.T) {
	skipIfEnvSet(t)
	th := getThresholds()

	t.Run("Fusion_100_Results", func(t *testing.T) {
		memBefore := memoryHighWaterMark()

		// Setup: Create mock results from different sources
		textResults := createMockTextResults(100)
		vectorResults := createMockVectorResults(100)
		graphResults := createMockGraphResults(100)

		// Measure fusion latency
		fusion := query.DefaultRRFFusion()
		weights := query.DefaultQueryWeights()

		start := time.Now()
		fused := fusion.Fuse(textResults, vectorResults, graphResults, weights)
		latency := time.Since(start)

		memAfter := memoryHighWaterMark()
		reportMemoryDelta(t, memBefore, memAfter)

		if len(fused) == 0 {
			t.Fatal("Fusion returned no results")
		}

		threshold := time.Duration(th.FusionMS) * time.Millisecond
		reportLatency(t, "Fusion", latency, threshold)
	})
}

// createMockVectorResults generates n mock vector search results.
func createMockVectorResults(n int) []query.VectorResult {
	results := make([]query.VectorResult, n)
	for i := range n {
		results[i] = query.VectorResult{
			ID:    "vec-" + strconv.Itoa(i),
			Score: 1.0 - float64(i)*0.01,
		}
	}
	return results
}

// createMockGraphResults generates n mock graph traversal results.
func createMockGraphResults(n int) []query.GraphResult {
	results := make([]query.GraphResult, n)
	for i := range n {
		results[i] = query.GraphResult{
			ID:    "graph-" + strconv.Itoa(i),
			Path:  []string{"start", "middle", "graph-" + strconv.Itoa(i)},
			Score: 1.0 - float64(i)*0.01,
		}
	}
	return results
}

// TestRegressionInference tests inference pipeline performance.
func TestRegressionInference(t *testing.T) {
	skipIfEnvSet(t)
	th := getThresholds()

	t.Run("Inference_5HopTraversal", func(t *testing.T) {
		memBefore := memoryHighWaterMark()

		// Setup: Create edges for 5-hop traversal simulation
		edges := createChainedEdges(5, 100)
		rules := createTransitivityRules()

		// Measure inference latency
		evaluator := inference.NewRuleEvaluator()
		chainer := inference.NewForwardChainer(evaluator, 10)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		start := time.Now()
		results, err := chainer.Evaluate(ctx, rules, edges)
		latency := time.Since(start)

		memAfter := memoryHighWaterMark()
		reportMemoryDelta(t, memBefore, memAfter)

		if err != nil && err != inference.ErrMaxIterationsReached {
			t.Fatalf("Inference failed: %v", err)
		}

		t.Logf("Inference derived %d new edges", len(results))

		threshold := time.Duration(th.InferenceMS) * time.Millisecond
		reportLatency(t, "Inference", latency, threshold)
	})
}

// createChainedEdges creates edges forming chains for traversal testing.
func createChainedEdges(hops, chainsPerHop int) []inference.Edge {
	edges := make([]inference.Edge, 0, hops*chainsPerHop)
	for hop := range hops {
		for chain := range chainsPerHop {
			source := "node-" + strconv.Itoa(hop) + "-" + strconv.Itoa(chain)
			target := "node-" + strconv.Itoa(hop+1) + "-" + strconv.Itoa(chain)
			edges = append(edges, inference.NewEdge(source, "connects", target))
		}
	}
	return edges
}

// createTransitivityRules creates inference rules for transitivity testing.
func createTransitivityRules() []inference.InferenceRule {
	return []inference.InferenceRule{
		{
			ID:       "transitivity",
			Name:     "Transitivity Rule",
			Priority: 1,
			Enabled:  true,
			Head: inference.RuleCondition{
				Subject:   "?x",
				Predicate: "connects",
				Object:    "?z",
			},
			Body: []inference.RuleCondition{
				{Subject: "?x", Predicate: "connects", Object: "?y"},
				{Subject: "?y", Predicate: "connects", Object: "?z"},
			},
		},
	}
}

// =============================================================================
// End-to-End Pipeline Tests
// =============================================================================

// TestRegressionIndexingPipeline tests the complete indexing flow.
func TestRegressionIndexingPipeline(t *testing.T) {
	skipIfEnvSet(t)

	t.Run("IndexingPipeline_1000_Documents", func(t *testing.T) {
		memBefore := memoryHighWaterMark()

		// Stage 1: Document creation
		start := time.Now()
		docs := createTestDocuments(1000)
		docLatency := time.Since(start)
		t.Logf("Document creation: %v (%d docs)", docLatency, len(docs))

		// Stage 2: Vector generation
		start = time.Now()
		vectors := generateDocumentVectors(docs, 128)
		vectorLatency := time.Since(start)
		t.Logf("Vector generation: %v (%d vectors)", vectorLatency, len(vectors))

		// Stage 3: HNSW indexing
		start = time.Now()
		index := indexVectors(t, vectors)
		indexLatency := time.Since(start)
		t.Logf("HNSW indexing: %v (%d nodes)", indexLatency, index.Size())

		memAfter := memoryHighWaterMark()
		reportMemoryDelta(t, memBefore, memAfter)

		totalLatency := docLatency + vectorLatency + indexLatency
		t.Logf("Total indexing pipeline: %v", totalLatency)
	})
}

// testDocument represents a document for indexing tests.
type testDocument struct {
	ID      string
	Content string
}

// createTestDocuments generates n test documents.
func createTestDocuments(n int) []testDocument {
	docs := make([]testDocument, n)
	for i := range n {
		docs[i] = testDocument{
			ID:      "doc-" + strconv.Itoa(i),
			Content: "Test document content number " + strconv.Itoa(i),
		}
	}
	return docs
}

// generateDocumentVectors creates vectors for documents.
func generateDocumentVectors(docs []testDocument, dim int) map[string][]float32 {
	vectors := make(map[string][]float32, len(docs))
	for i, doc := range docs {
		vectors[doc.ID] = generateTestVector(dim, float32(i)*0.001)
	}
	return vectors
}

// indexVectors builds an HNSW index from document vectors.
func indexVectors(t *testing.T, vectors map[string][]float32) *hnsw.Index {
	var dim int
	for _, v := range vectors {
		dim = len(v)
		break
	}

	cfg := hnsw.DefaultConfig()
	cfg.Dimension = dim
	index := hnsw.New(cfg)

	for id, vec := range vectors {
		err := index.Insert(id, vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
		if err != nil {
			t.Fatalf("Failed to index vector %s: %v", id, err)
		}
	}
	return index
}

// TestRegressionSearchPipeline tests the complete search flow.
func TestRegressionSearchPipeline(t *testing.T) {
	skipIfEnvSet(t)

	t.Run("SearchPipeline_QueryToFusion", func(t *testing.T) {
		memBefore := memoryHighWaterMark()

		// Setup: Pre-populate index
		index := createTestIndex(t, 5000, 128)

		// Stage 1: Vector search
		queryVec := generateTestVector(128, 0.5)
		start := time.Now()
		vectorResults := index.Search(queryVec, 50, nil)
		vectorLatency := time.Since(start)
		t.Logf("Vector search: %v (%d results)", vectorLatency, len(vectorResults))

		// Stage 2: Text search (simulated)
		start = time.Now()
		textResults := createMockTextResults(50)
		textLatency := time.Since(start)
		t.Logf("Text search: %v (%d results)", textLatency, len(textResults))

		// Stage 3: Convert and fuse results
		start = time.Now()
		convertedVec := convertHNSWToVectorResults(vectorResults)
		graphResults := createMockGraphResults(50)

		fusion := query.DefaultRRFFusion()
		fused := fusion.Fuse(textResults, convertedVec, graphResults, nil)
		fusionLatency := time.Since(start)
		t.Logf("Fusion: %v (%d fused results)", fusionLatency, len(fused))

		memAfter := memoryHighWaterMark()
		reportMemoryDelta(t, memBefore, memAfter)

		totalLatency := vectorLatency + textLatency + fusionLatency
		t.Logf("Total search pipeline: %v", totalLatency)
	})
}

// convertHNSWToVectorResults converts HNSW results to query.VectorResult.
func convertHNSWToVectorResults(results []hnsw.SearchResult) []query.VectorResult {
	converted := make([]query.VectorResult, len(results))
	for i, r := range results {
		converted[i] = query.VectorResult{
			ID:    r.ID,
			Score: r.Similarity,
		}
	}
	return converted
}

// TestRegressionGraphInferencePipeline tests graph traversal with inference.
func TestRegressionGraphInferencePipeline(t *testing.T) {
	skipIfEnvSet(t)

	t.Run("GraphInference_5Hops_WithRules", func(t *testing.T) {
		memBefore := memoryHighWaterMark()

		// Stage 1: Build initial edge set
		start := time.Now()
		baseEdges := createChainedEdges(5, 50)
		edgeLatency := time.Since(start)
		t.Logf("Edge creation: %v (%d edges)", edgeLatency, len(baseEdges))

		// Stage 2: Apply inference rules
		rules := createTransitivityRules()
		evaluator := inference.NewRuleEvaluator()
		chainer := inference.NewForwardChainer(evaluator, 5)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		start = time.Now()
		derived, err := chainer.Evaluate(ctx, rules, baseEdges)
		inferenceLatency := time.Since(start)
		if err != nil && err != inference.ErrMaxIterationsReached {
			t.Fatalf("Inference failed: %v", err)
		}
		t.Logf("Inference: %v (%d derived edges)", inferenceLatency, len(derived))

		memAfter := memoryHighWaterMark()
		reportMemoryDelta(t, memBefore, memAfter)

		totalLatency := edgeLatency + inferenceLatency
		t.Logf("Total graph inference pipeline: %v", totalLatency)
	})
}
