// Package search provides PF.6.2: Bleve Performance Benchmark Suite.
// This file contains comprehensive benchmarks for the Bleve search system
// including index creation, document indexing, search queries, async queue
// throughput, cache performance, and fusion result processing.
package search

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"
	bleveQuery "github.com/blevesearch/bleve/v2/search/query"

	"github.com/adalundhe/sylk/core/knowledge/query"
	bleveSearch "github.com/adalundhe/sylk/core/search/bleve"
	"github.com/adalundhe/sylk/core/search"
)

// =============================================================================
// Test Document Generation
// =============================================================================

// generateTestDocument creates a test document with the given index.
func generateTestDocument(idx int) *search.Document {
	content := fmt.Sprintf("This is test document number %d with some content for indexing.", idx)
	return &search.Document{
		ID:         fmt.Sprintf("doc-%d", idx),
		Path:       fmt.Sprintf("/test/path/file%d.go", idx),
		Type:       search.DocTypeSourceCode,
		Language:   "go",
		Content:    content,
		Symbols:    []string{"TestFunc", "TestType", "TestVar"},
		Comments:   "// Test comment",
		Imports:    []string{"fmt", "testing"},
		Checksum:   search.GenerateChecksum([]byte(content)),
		ModifiedAt: time.Now(),
		IndexedAt:  time.Now(),
	}
}

// generateTestDocuments creates n test documents.
func generateTestDocuments(n int) []*search.Document {
	docs := make([]*search.Document, n)
	for i := 0; i < n; i++ {
		docs[i] = generateTestDocument(i)
	}
	return docs
}

// =============================================================================
// Index Creation Benchmarks
// =============================================================================

// BenchmarkIndexCreation benchmarks creating a new Bleve index.
func BenchmarkIndexCreation(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		tmpDir := b.TempDir()
		indexPath := filepath.Join(tmpDir, "test.bleve")

		indexMapping := bleve.NewIndexMapping()
		index, err := bleve.New(indexPath, indexMapping)
		if err != nil {
			b.Fatalf("failed to create index: %v", err)
		}
		index.Close()
	}
}

// BenchmarkIndexCreationWithMapping benchmarks creating an index with document mapping.
func BenchmarkIndexCreationWithMapping(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		tmpDir := b.TempDir()
		indexPath := filepath.Join(tmpDir, "test.bleve")

		indexMapping := buildDocumentMapping()
		index, err := bleve.New(indexPath, indexMapping)
		if err != nil {
			b.Fatalf("failed to create index: %v", err)
		}
		index.Close()
	}
}

// buildDocumentMapping creates a document mapping for benchmarks.
func buildDocumentMapping() mapping.IndexMapping {
	indexMapping := bleve.NewIndexMapping()

	docMapping := bleve.NewDocumentMapping()

	textFieldMapping := bleve.NewTextFieldMapping()
	textFieldMapping.Store = true
	textFieldMapping.IncludeTermVectors = true

	docMapping.AddFieldMappingsAt("content", textFieldMapping)
	docMapping.AddFieldMappingsAt("path", textFieldMapping)
	docMapping.AddFieldMappingsAt("symbols", textFieldMapping)

	indexMapping.AddDocumentMapping("document", docMapping)

	return indexMapping
}

// =============================================================================
// Document Indexing Benchmarks
// =============================================================================

// BenchmarkDocumentIndexing benchmarks single document indexing.
func BenchmarkDocumentIndexing(b *testing.B) {
	tmpDir := b.TempDir()
	indexPath := filepath.Join(tmpDir, "test.bleve")

	index, err := bleve.New(indexPath, bleve.NewIndexMapping())
	if err != nil {
		b.Fatalf("failed to create index: %v", err)
	}
	defer index.Close()

	doc := generateTestDocument(0)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		doc.ID = fmt.Sprintf("doc-%d", i)
		if err := index.Index(doc.ID, doc); err != nil {
			b.Fatalf("failed to index document: %v", err)
		}
	}
}

// BenchmarkBatchIndexing benchmarks batch document indexing with table-driven sizes.
func BenchmarkBatchIndexing(b *testing.B) {
	batchSizes := []int{100, 1000}

	for _, batchSize := range batchSizes {
		b.Run(fmt.Sprintf("BatchSize%d", batchSize), func(b *testing.B) {
			benchmarkBatchIndexingN(b, batchSize)
		})
	}
}

// benchmarkBatchIndexingN benchmarks batch indexing with n documents.
func benchmarkBatchIndexingN(b *testing.B, batchSize int) {
	docs := generateTestDocuments(batchSize)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		tmpDir := b.TempDir()
		indexPath := filepath.Join(tmpDir, "test.bleve")
		index, err := bleve.New(indexPath, bleve.NewIndexMapping())
		if err != nil {
			b.Fatalf("failed to create index: %v", err)
		}
		b.StartTimer()

		batch := index.NewBatch()
		for _, doc := range docs {
			if err := batch.Index(doc.ID, doc); err != nil {
				b.Fatalf("failed to add to batch: %v", err)
			}
		}
		if err := index.Batch(batch); err != nil {
			b.Fatalf("failed to commit batch: %v", err)
		}

		b.StopTimer()
		index.Close()
		os.RemoveAll(tmpDir)
		b.StartTimer()
	}
}

// =============================================================================
// Search Query Benchmarks
// =============================================================================

// BenchmarkSearchQueries benchmarks various search query types.
func BenchmarkSearchQueries(b *testing.B) {
	tmpDir := b.TempDir()
	indexPath := filepath.Join(tmpDir, "test.bleve")

	index, err := bleve.New(indexPath, bleve.NewIndexMapping())
	if err != nil {
		b.Fatalf("failed to create index: %v", err)
	}
	defer index.Close()

	// Pre-populate index with documents
	docs := generateTestDocuments(1000)
	batch := index.NewBatch()
	for _, doc := range docs {
		batch.Index(doc.ID, doc)
	}
	index.Batch(batch)

	queryTests := []struct {
		name  string
		query bleveQuery.Query
	}{
		{"Simple", bleve.NewQueryStringQuery("test")},
		{"Fuzzy", bleve.NewFuzzyQuery("documnt")},
		{"Phrase", bleve.NewPhraseQuery([]string{"test", "document"}, "content")},
	}

	for _, tc := range queryTests {
		b.Run(tc.name, func(b *testing.B) {
			benchmarkSearchQuery(b, index, tc.query)
		})
	}
}

// benchmarkSearchQuery benchmarks a specific search query.
func benchmarkSearchQuery(b *testing.B, index bleve.Index, q bleveQuery.Query) {
	req := bleve.NewSearchRequest(q)
	req.Size = 10

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := index.Search(req)
		if err != nil {
			b.Fatalf("search failed: %v", err)
		}
	}
}

// BenchmarkSearchWithHighlight benchmarks search with highlighting enabled.
func BenchmarkSearchWithHighlight(b *testing.B) {
	tmpDir := b.TempDir()
	indexPath := filepath.Join(tmpDir, "test.bleve")

	index, err := bleve.New(indexPath, bleve.NewIndexMapping())
	if err != nil {
		b.Fatalf("failed to create index: %v", err)
	}
	defer index.Close()

	// Pre-populate index
	docs := generateTestDocuments(500)
	batch := index.NewBatch()
	for _, doc := range docs {
		batch.Index(doc.ID, doc)
	}
	index.Batch(batch)

	query := bleve.NewQueryStringQuery("test document")
	req := bleve.NewSearchRequest(query)
	req.Size = 10
	req.Highlight = bleve.NewHighlight()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := index.Search(req)
		if err != nil {
			b.Fatalf("search failed: %v", err)
		}
	}
}

// =============================================================================
// Async Index Queue Throughput Benchmarks
// =============================================================================

// BenchmarkAsyncQueueSubmit benchmarks async queue submission throughput.
func BenchmarkAsyncQueueSubmit(b *testing.B) {
	ctx := context.Background()

	indexFn := func(docID string, doc interface{}) error { return nil }
	deleteFn := func(docID string) error { return nil }

	config := bleveSearch.AsyncIndexQueueConfig{
		MaxQueueSize:  100000,
		BatchSize:     100,
		FlushInterval: 100 * time.Millisecond,
	}

	queue := bleveSearch.NewAsyncIndexQueue(ctx, config, indexFn, deleteFn, nil)
	defer queue.Close()

	doc := generateTestDocument(0)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		op := bleveSearch.NewIndexOperation(bleveSearch.OpIndex, doc.ID, doc)
		queue.Submit(op)
	}
}

// BenchmarkAsyncQueueThroughput benchmarks async queue end-to-end throughput.
func BenchmarkAsyncQueueThroughput(b *testing.B) {
	batchSizes := []int{100, 1000}

	for _, batchSize := range batchSizes {
		b.Run(fmt.Sprintf("BatchSize%d", batchSize), func(b *testing.B) {
			benchmarkAsyncQueueThroughputN(b, batchSize)
		})
	}
}

// benchmarkAsyncQueueThroughputN benchmarks async queue with n documents per iteration.
func benchmarkAsyncQueueThroughputN(b *testing.B, docsPerIteration int) {
	ctx := context.Background()

	processed := 0
	indexFn := func(docID string, doc interface{}) error {
		processed++
		return nil
	}
	deleteFn := func(docID string) error { return nil }

	config := bleveSearch.AsyncIndexQueueConfig{
		MaxQueueSize:  100000,
		BatchSize:     100,
		FlushInterval: 10 * time.Millisecond,
	}

	docs := generateTestDocuments(docsPerIteration)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		queue := bleveSearch.NewAsyncIndexQueue(ctx, config, indexFn, deleteFn, nil)

		for _, doc := range docs {
			op := bleveSearch.NewIndexOperation(bleveSearch.OpIndex, doc.ID, doc)
			queue.Submit(op)
		}

		queue.Flush(ctx)
		queue.Close()
	}
}

// =============================================================================
// Search Cache Benchmarks
// =============================================================================

// BenchmarkRegexCacheHit benchmarks regex cache hit performance.
func BenchmarkRegexCacheHit(b *testing.B) {
	cache := query.NewRegexCache()

	// Pre-populate cache
	pattern := `[a-zA-Z0-9_]+`
	_, err := cache.GetOrCompile(pattern)
	if err != nil {
		b.Fatalf("failed to compile pattern: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = cache.GetOrCompile(pattern)
	}
}

// BenchmarkRegexCacheMiss benchmarks regex cache miss performance.
func BenchmarkRegexCacheMiss(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		cache := query.NewRegexCache()
		b.StartTimer()

		pattern := fmt.Sprintf(`test_%d`, i)
		_, _ = cache.GetOrCompile(pattern)
	}
}

// BenchmarkRegexCacheParallel benchmarks regex cache under parallel access.
func BenchmarkRegexCacheParallel(b *testing.B) {
	cache := query.NewRegexCache()

	// Pre-populate with some patterns
	patterns := []string{`[a-z]+`, `[0-9]+`, `\w+`, `\d+`, `[A-Z][a-z]+`}
	for _, p := range patterns {
		cache.GetOrCompile(p)
	}

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			pattern := patterns[i%len(patterns)]
			_, _ = cache.GetOrCompile(pattern)
			i++
		}
	})
}

// =============================================================================
// RRF Fusion Benchmarks
// =============================================================================

// BenchmarkRRFFusion benchmarks RRF fusion result processing.
func BenchmarkRRFFusion(b *testing.B) {
	resultSizes := []int{10, 50, 100}

	for _, size := range resultSizes {
		b.Run(fmt.Sprintf("Results%d", size), func(b *testing.B) {
			benchmarkRRFFusionN(b, size)
		})
	}
}

// benchmarkRRFFusionN benchmarks RRF fusion with n results per source.
func benchmarkRRFFusionN(b *testing.B, resultsPerSource int) {
	textResults := generateTextResults(resultsPerSource)
	semanticResults := generateSemanticResults(resultsPerSource)
	graphResults := generateGraphResults(resultsPerSource)

	fusion := query.DefaultRRFFusion()
	weights := query.DefaultQueryWeights()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = fusion.Fuse(textResults, semanticResults, graphResults, weights)
	}
}

// generateTextResults generates mock text search results.
func generateTextResults(n int) []query.TextResult {
	results := make([]query.TextResult, n)
	for i := 0; i < n; i++ {
		results[i] = query.TextResult{
			ID:      fmt.Sprintf("text-%d", i),
			Score:   1.0 - float64(i)*0.01,
			Content: fmt.Sprintf("Text content %d", i),
		}
	}
	return results
}

// generateSemanticResults generates mock semantic search results.
func generateSemanticResults(n int) []query.VectorResult {
	results := make([]query.VectorResult, n)
	for i := 0; i < n; i++ {
		results[i] = query.VectorResult{
			ID:       fmt.Sprintf("semantic-%d", i),
			Score:    1.0 - float64(i)*0.01,
			Distance: float32(i) * 0.1,
		}
	}
	return results
}

// generateGraphResults generates mock graph traversal results.
func generateGraphResults(n int) []query.GraphResult {
	results := make([]query.GraphResult, n)
	for i := 0; i < n; i++ {
		results[i] = query.GraphResult{
			ID:    fmt.Sprintf("graph-%d", i),
			Score: 1.0 - float64(i)*0.01,
			Path:  []string{"start", fmt.Sprintf("node-%d", i), "end"},
		}
	}
	return results
}

// BenchmarkRRFFusionWithMemory benchmarks RRF fusion with memory scores.
func BenchmarkRRFFusionWithMemory(b *testing.B) {
	resultsPerSource := 50

	textResults := generateTextResults(resultsPerSource)
	semanticResults := generateSemanticResults(resultsPerSource)
	graphResults := generateGraphResults(resultsPerSource)
	memoryScores := generateMemoryScores(resultsPerSource * 3)

	fusion := query.DefaultRRFFusion()
	weights := query.DefaultQueryWeightsWithMemory()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = fusion.FuseWithMemory(textResults, semanticResults, graphResults, weights, memoryScores)
	}
}

// generateMemoryScores generates mock memory activation scores.
func generateMemoryScores(n int) map[string]float64 {
	scores := make(map[string]float64, n)
	for i := 0; i < n; i++ {
		scores[fmt.Sprintf("doc-%d", i)] = 1.0 - float64(i)*0.01
	}
	return scores
}

// BenchmarkRRFBatchFusion benchmarks batch RRF fusion operations.
func BenchmarkRRFBatchFusion(b *testing.B) {
	batchSizes := []int{5, 10, 20}

	for _, batchSize := range batchSizes {
		b.Run(fmt.Sprintf("Batch%d", batchSize), func(b *testing.B) {
			benchmarkRRFBatchFusionN(b, batchSize)
		})
	}
}

// benchmarkRRFBatchFusionN benchmarks batch fusion with n queries.
func benchmarkRRFBatchFusionN(b *testing.B, batchSize int) {
	queries := make([]query.FusionQuery, batchSize)
	for i := 0; i < batchSize; i++ {
		queries[i] = query.FusionQuery{
			Text:     generateTextResults(20),
			Semantic: generateSemanticResults(20),
			Graph:    generateGraphResults(20),
		}
	}

	fusion := query.DefaultRRFFusion()
	weights := query.DefaultQueryWeights()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = fusion.FuseBatch(queries, weights)
	}
}

// =============================================================================
// Parallel Search Benchmarks
// =============================================================================

// BenchmarkSearchParallel benchmarks parallel search operations.
func BenchmarkSearchParallel(b *testing.B) {
	tmpDir := b.TempDir()
	indexPath := filepath.Join(tmpDir, "test.bleve")

	index, err := bleve.New(indexPath, bleve.NewIndexMapping())
	if err != nil {
		b.Fatalf("failed to create index: %v", err)
	}
	defer index.Close()

	// Pre-populate index
	docs := generateTestDocuments(1000)
	batch := index.NewBatch()
	for _, doc := range docs {
		batch.Index(doc.ID, doc)
	}
	index.Batch(batch)

	query := bleve.NewQueryStringQuery("test")
	req := bleve.NewSearchRequest(query)
	req.Size = 10

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := index.Search(req)
			if err != nil {
				b.Errorf("search failed: %v", err)
			}
		}
	})
}

// =============================================================================
// Index Manager Benchmarks
// =============================================================================

// BenchmarkIndexManagerIndex benchmarks IndexManager single document indexing.
func BenchmarkIndexManagerIndex(b *testing.B) {
	tmpDir := b.TempDir()
	indexPath := filepath.Join(tmpDir, "test.bleve")

	mgr := bleveSearch.NewIndexManager(indexPath)
	if err := mgr.Open(); err != nil {
		b.Fatalf("failed to open index: %v", err)
	}
	defer mgr.Close()

	ctx := context.Background()
	doc := generateTestDocument(0)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		doc.ID = fmt.Sprintf("doc-%d", i)
		if err := mgr.Index(ctx, doc); err != nil {
			b.Fatalf("failed to index: %v", err)
		}
	}
}

// BenchmarkIndexManagerBatch benchmarks IndexManager batch indexing.
func BenchmarkIndexManagerBatch(b *testing.B) {
	batchSizes := []int{100, 1000}

	for _, batchSize := range batchSizes {
		b.Run(fmt.Sprintf("BatchSize%d", batchSize), func(b *testing.B) {
			benchmarkIndexManagerBatchN(b, batchSize)
		})
	}
}

// benchmarkIndexManagerBatchN benchmarks IndexManager batch with n documents.
func benchmarkIndexManagerBatchN(b *testing.B, batchSize int) {
	docs := generateTestDocuments(batchSize)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		tmpDir := b.TempDir()
		indexPath := filepath.Join(tmpDir, "test.bleve")
		mgr := bleveSearch.NewIndexManager(indexPath)
		if err := mgr.Open(); err != nil {
			b.Fatalf("failed to open index: %v", err)
		}
		b.StartTimer()

		if err := mgr.IndexBatch(ctx, docs); err != nil {
			b.Fatalf("failed to batch index: %v", err)
		}

		b.StopTimer()
		mgr.Close()
		os.RemoveAll(tmpDir)
		b.StartTimer()
	}
}
