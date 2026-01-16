package archivalist

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"
)

func TestTemporalPartitionManager_AddEntry(t *testing.T) {
	pm := NewTemporalPartitionManager(DefaultTemporalPartitionConfig())

	now := time.Now()
	err := pm.AddEntry("entry1", now, 100)
	if err != nil {
		t.Fatalf("Failed to add entry: %v", err)
	}

	err = pm.AddEntry("entry2", now.Add(-time.Hour), 200)
	if err != nil {
		t.Fatalf("Failed to add entry: %v", err)
	}

	stats := pm.Stats()
	if stats.TotalEntries != 2 {
		t.Errorf("Expected 2 entries, got %d", stats.TotalEntries)
	}

	if stats.TotalPartitions < 1 {
		t.Errorf("Expected at least 1 partition, got %d", stats.TotalPartitions)
	}
}

func TestTemporalPartitionManager_QueryTimeRange(t *testing.T) {
	pm := NewTemporalPartitionManager(DefaultTemporalPartitionConfig())

	baseTime := time.Now()
	pm.AddEntry("entry1", baseTime, 100)
	pm.AddEntry("entry2", baseTime.Add(-24*time.Hour), 100)
	pm.AddEntry("entry3", baseTime.Add(-48*time.Hour), 100)

	results := pm.QueryTimeRange(
		baseTime.Add(-72*time.Hour),
		baseTime.Add(time.Hour),
	)

	if len(results) != 3 {
		t.Errorf("Expected 3 results, got %d", len(results))
	}

	results = pm.QueryTimeRange(
		baseTime.Add(-12*time.Hour),
		baseTime.Add(time.Hour),
	)

	if len(results) != 1 {
		t.Errorf("Expected 1 result, got %d", len(results))
	}
}

func TestTemporalPartitionManager_PartitionKeys(t *testing.T) {
	pm := NewTemporalPartitionManager(TemporalPartitionConfig{
		Granularity:            PartitionDaily,
		MaxEntriesPerPartition: 10000,
		MaxPartitionsInMemory:  30,
	})

	testTime := time.Date(2024, 6, 15, 14, 30, 0, 0, time.UTC)
	key := pm.GeneratePartitionKey(testTime)

	if key != "2024-06-15" {
		t.Errorf("Expected key 2024-06-15, got %s", key)
	}

	start, end := pm.GetPartitionBounds(key)
	expectedStart := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)
	expectedEnd := time.Date(2024, 6, 16, 0, 0, 0, 0, time.UTC)

	if !start.Equal(expectedStart) {
		t.Errorf("Expected start %v, got %v", expectedStart, start)
	}
	if !end.Equal(expectedEnd) {
		t.Errorf("Expected end %v, got %v", expectedEnd, end)
	}
}

func TestTemporalPartitionManager_Compaction(t *testing.T) {
	config := DefaultTemporalPartitionConfig()
	config.CompactThreshold = time.Millisecond
	pm := NewTemporalPartitionManager(config)

	oldTime := time.Now().Add(-time.Hour)
	pm.AddEntry("old_entry", oldTime, 100)

	time.Sleep(10 * time.Millisecond)

	compacted := pm.CompactOldPartitions()
	if compacted < 1 {
		t.Errorf("Expected at least 1 partition to be compacted, got %d", compacted)
	}

	stats := pm.Stats()
	if stats.CompactedCount < 1 {
		t.Errorf("Expected compacted count >= 1, got %d", stats.CompactedCount)
	}
}

func TestTemporalPartitionManager_Concurrent(t *testing.T) {
	pm := NewTemporalPartitionManager(DefaultTemporalPartitionConfig())

	var wg sync.WaitGroup
	baseTime := time.Now()

	for i := range 100 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			entryTime := baseTime.Add(time.Duration(idx%24) * time.Hour)
			pm.AddEntry(
				"entry"+string(rune('0'+idx%10)),
				entryTime,
				100,
			)
		}(i)
	}

	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pm.QueryTimeRange(baseTime.Add(-48*time.Hour), baseTime.Add(48*time.Hour))
		}()
	}

	wg.Wait()

	stats := pm.Stats()
	if stats.TotalEntries == 0 {
		t.Error("Expected entries to be added")
	}
}

func TestSimilarityIndex_Insert(t *testing.T) {
	idx := NewSimilarityIndex(SimilarityIndexConfig{
		Dimension:      4,
		M:              4,
		EfConstruction: 10,
		EfSearch:       10,
	})

	idx.Insert("v1", []float32{1, 0, 0, 0})
	idx.Insert("v2", []float32{0, 1, 0, 0})
	idx.Insert("v3", []float32{0, 0, 1, 0})

	if idx.Count() != 3 {
		t.Errorf("Expected 3 nodes, got %d", idx.Count())
	}
}

func TestSimilarityIndex_Search(t *testing.T) {
	idx := NewSimilarityIndex(SimilarityIndexConfig{
		Dimension:      4,
		M:              4,
		EfConstruction: 10,
		EfSearch:       10,
	})

	idx.Insert("v1", []float32{1, 0, 0, 0})
	idx.Insert("v2", []float32{0.9, 0.1, 0, 0})
	idx.Insert("v3", []float32{0, 1, 0, 0})
	idx.Insert("v4", []float32{0, 0, 1, 0})

	results := idx.Search([]float32{1, 0, 0, 0}, 2)

	if len(results) < 1 {
		t.Fatal("Expected at least 1 result")
	}

	if results[0].ID != "v1" {
		t.Errorf("Expected v1 as top result, got %s", results[0].ID)
	}

	if results[0].Score < 0.99 {
		t.Errorf("Expected score close to 1.0, got %f", results[0].Score)
	}
}

func TestSimilarityIndex_SearchWithThreshold(t *testing.T) {
	idx := NewSimilarityIndex(SimilarityIndexConfig{
		Dimension:      4,
		M:              4,
		EfConstruction: 10,
		EfSearch:       10,
	})

	idx.Insert("v1", []float32{1, 0, 0, 0})
	idx.Insert("v2", []float32{0.9, 0.1, 0, 0})
	idx.Insert("v3", []float32{0, 1, 0, 0})

	results := idx.SearchWithThreshold([]float32{1, 0, 0, 0}, 0.95, 10)

	for _, result := range results {
		if result.Score < 0.95 {
			t.Errorf("Result %s has score %f below threshold", result.ID, result.Score)
		}
	}
}

func TestSimilarityIndex_Remove(t *testing.T) {
	idx := NewSimilarityIndex(SimilarityIndexConfig{
		Dimension:      4,
		M:              4,
		EfConstruction: 10,
		EfSearch:       10,
	})

	idx.Insert("v1", []float32{1, 0, 0, 0})
	idx.Insert("v2", []float32{0, 1, 0, 0})

	if idx.Count() != 2 {
		t.Errorf("Expected 2 nodes, got %d", idx.Count())
	}

	idx.Remove("v1")

	if idx.Count() != 1 {
		t.Errorf("Expected 1 node after removal, got %d", idx.Count())
	}

	results := idx.Search([]float32{1, 0, 0, 0}, 10)
	for _, result := range results {
		if result.ID == "v1" {
			t.Error("Found v1 after removal")
		}
	}
}

func TestCompressor_Compress(t *testing.T) {
	c := NewCompressor(DefaultCompressorConfig())

	baseText := "This is test data that should be compressed. The quick brown fox jumps over the lazy dog. "
	var data []byte
	for range 50 {
		data = append(data, []byte(baseText)...)
	}

	compressed, err := c.Compress(data)
	if err != nil {
		t.Fatalf("Compression failed: %v", err)
	}

	if compressed.Type == CompressionNone && len(data) > 1024 {
		t.Logf("Note: Compression type is none for %d bytes of data", len(data))
	}

	if compressed.CompressedSize < compressed.OriginalSize {
		t.Logf("Compression successful: %d -> %d (%.1f%%)",
			compressed.OriginalSize, compressed.CompressedSize,
			float64(compressed.CompressedSize)/float64(compressed.OriginalSize)*100)
	}
}

func TestCompressor_Decompress(t *testing.T) {
	c := NewCompressor(DefaultCompressorConfig())

	original := []byte("This is test data that should be compressed and decompressed. " +
		"Making it long enough to trigger compression. " +
		"The data should match exactly after round-trip.")

	compressed, err := c.Compress(original)
	if err != nil {
		t.Fatalf("Compression failed: %v", err)
	}

	decompressed, err := c.Decompress(compressed)
	if err != nil {
		t.Fatalf("Decompression failed: %v", err)
	}

	if !bytes.Equal(original, decompressed) {
		t.Error("Decompressed data doesn't match original")
	}
}

func TestCompressor_SmallData(t *testing.T) {
	c := NewCompressor(CompressorConfig{
		Type:              CompressionGzip,
		MinSizeToCompress: 1024,
	})

	smallData := []byte("small")

	compressed, err := c.Compress(smallData)
	if err != nil {
		t.Fatalf("Compression failed: %v", err)
	}

	if compressed.Type != CompressionNone {
		t.Errorf("Small data should not be compressed, got type %s", compressed.Type)
	}
}

func TestCompressor_EntryCompression(t *testing.T) {
	c := NewCompressor(CompressorConfig{
		Type:              CompressionGzip,
		MinSizeToCompress: 10,
	})

	entry := &Entry{
		ID:       "test_entry",
		Category: CategoryInsight,
		Title:    "Test Entry",
		Content: "This is a test entry with enough content to be worth compressing. " +
			"Adding more content to ensure we exceed the minimum size threshold.",
		Source:    SourceModelClaudeOpus45,
		SessionID: "sess_123",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	compressed, err := c.CompressEntry(entry)
	if err != nil {
		t.Fatalf("Entry compression failed: %v", err)
	}

	if compressed.ID != entry.ID {
		t.Errorf("Compressed entry ID mismatch: got %s, want %s", compressed.ID, entry.ID)
	}

	decompressed, err := c.DecompressEntry(compressed)
	if err != nil {
		t.Fatalf("Entry decompression failed: %v", err)
	}

	if decompressed.Content != entry.Content {
		t.Error("Decompressed content doesn't match original")
	}
}

func TestQueryOptimizer_Plan(t *testing.T) {
	qo := NewQueryOptimizer(DefaultQueryOptimizerConfig())

	qo.UpdateStatistics(IndexStatistics{
		TotalEntries: 10000,
		EntriesByCategory: map[Category]int64{
			CategoryInsight:  1000,
			CategoryDecision: 500,
		},
		EntriesBySource: map[SourceModel]int64{
			SourceModelClaudeOpus45: 8000,
			SourceModelGPT52Codex:   2000,
		},
		AvgEntriesPerDay: 100,
	})

	query := ArchiveQuery{
		Categories: []Category{CategoryInsight},
		Limit:      100,
	}

	plan := qo.Plan(query)

	if plan == nil {
		t.Fatal("Expected non-nil plan")
	}

	if len(plan.Operations) == 0 {
		t.Error("Expected at least one operation in plan")
	}

	if plan.Operations[0].Type != OpIndexCategory && plan.Operations[0].Type != OpScanAll {
		t.Logf("First operation type: %s", plan.Operations[0].Type)
	}
}

func TestQueryOptimizer_CachePlan(t *testing.T) {
	qo := NewQueryOptimizer(QueryOptimizerConfig{
		MaxCachedPlans: 100,
		PlanCacheTTL:   time.Minute,
	})

	query := ArchiveQuery{
		Categories: []Category{CategoryInsight},
	}

	plan1 := qo.Plan(query)
	plan2 := qo.Plan(query)

	if plan1.CacheKey != plan2.CacheKey {
		t.Error("Cache keys should match for same query")
	}

	stats := qo.Stats()
	if stats.CacheHits < 1 {
		t.Error("Expected at least one cache hit")
	}
}

func TestQueryOptimizer_IDLookup(t *testing.T) {
	qo := NewQueryOptimizer(DefaultQueryOptimizerConfig())

	query := ArchiveQuery{
		IDs: []string{"id1", "id2", "id3"},
	}

	plan := qo.Plan(query)

	if plan.Operations[0].Type != OpIndexID {
		t.Errorf("Expected ID index operation, got %s", plan.Operations[0].Type)
	}
}

func TestExporter_ExportJSONL(t *testing.T) {
	exporter := NewExporter(ExportConfig{
		Format:           ExportFormatJSONL,
		IncludeFacts:     false,
		IncludeSummaries: false,
	})

	store := NewStore(StoreConfig{TokenThreshold: 100000})

	store.InsertEntry(&Entry{
		Category: CategoryInsight,
		Title:    "Test Entry 1",
		Content:  "Test content 1",
		Source:   SourceModelClaudeOpus45,
	})

	store.InsertEntry(&Entry{
		Category: CategoryDecision,
		Title:    "Test Entry 2",
		Content:  "Test content 2",
		Source:   SourceModelGPT52Codex,
	})

	var buf bytes.Buffer
	err := exporter.ExportToWriter(&buf, store, nil)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	if buf.Len() == 0 {
		t.Error("Expected non-empty export")
	}

	stats := exporter.Stats()
	if stats.EntriesExported < 2 {
		t.Errorf("Expected at least 2 entries exported, got %d", stats.EntriesExported)
	}
}

func TestExporter_ExportJSON(t *testing.T) {
	exporter := NewExporter(ExportConfig{
		Format:           ExportFormatJSON,
		IncludeFacts:     false,
		IncludeSummaries: false,
	})

	store := NewStore(StoreConfig{TokenThreshold: 100000})
	store.InsertEntry(&Entry{
		Category: CategoryInsight,
		Title:    "Test",
		Content:  "Test content",
		Source:   SourceModelClaudeOpus45,
	})

	var buf bytes.Buffer
	err := exporter.ExportToWriter(&buf, store, nil)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	if buf.Len() < 2 || buf.Bytes()[0] != '{' {
		t.Error("Expected JSON object output")
	}
}

func TestImporter_Import(t *testing.T) {
	exporter := NewExporter(ExportConfig{
		Format:           ExportFormatJSONL,
		IncludeFacts:     false,
		IncludeSummaries: false,
	})

	sourceStore := NewStore(StoreConfig{TokenThreshold: 100000})
	sourceStore.InsertEntry(&Entry{
		ID:       "entry1",
		Category: CategoryInsight,
		Title:    "Test Entry",
		Content:  "Test content for import",
		Source:   SourceModelClaudeOpus45,
	})

	var buf bytes.Buffer
	exporter.ExportToWriter(&buf, sourceStore, nil)

	importer := NewImporter(ImportConfig{
		MergeStrategy: MergeStrategyOverwrite,
		BatchSize:     100,
	})

	targetStore := NewStore(StoreConfig{TokenThreshold: 100000})

	result, err := importer.ImportFromReader(&buf, targetStore, nil)
	if err != nil {
		t.Fatalf("Import failed: %v", err)
	}

	if result.EntriesImported < 1 {
		t.Errorf("Expected at least 1 entry imported, got %d", result.EntriesImported)
	}
}

func TestImporter_MergeStrategy(t *testing.T) {
	importer := NewImporter(ImportConfig{
		MergeStrategy: MergeStrategySkip,
		BatchSize:     100,
	})

	store := NewStore(StoreConfig{TokenThreshold: 100000})

	existingEntry := &Entry{
		ID:        "existing",
		Category:  CategoryInsight,
		Content:   "Original content",
		Source:    SourceModelClaudeOpus45,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	store.InsertEntry(existingEntry)

	importData := `{"type":"header","data":{"version":"1.0"}}
{"type":"entry","data":{"id":"existing","category":"insight","content":"New content","source":"claude-opus-4-5-20251101"}}`

	result, err := importer.ImportFromReader(bytes.NewReader([]byte(importData)), store, nil)
	if err != nil {
		t.Fatalf("Import failed: %v", err)
	}

	if result.EntriesSkipped != 1 {
		t.Errorf("Expected 1 entry skipped, got %d", result.EntriesSkipped)
	}

	entry, _ := store.GetEntry("existing")
	if entry.Content != "Original content" {
		t.Error("Entry was modified despite skip strategy")
	}
}

func TestCosineSimilarity(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{1, 0, 0}
	sim := cosineSimilarity(a, b)
	if sim < 0.999 {
		t.Errorf("Expected similarity ~1.0 for identical vectors, got %f", sim)
	}

	c := []float32{1, 0, 0}
	d := []float32{0, 1, 0}
	sim = cosineSimilarity(c, d)
	if sim > 0.001 {
		t.Errorf("Expected similarity ~0 for orthogonal vectors, got %f", sim)
	}

	e := []float32{1, 0, 0}
	f := []float32{-1, 0, 0}
	sim = cosineSimilarity(e, f)
	if sim > -0.999 {
		t.Errorf("Expected similarity ~-1.0 for opposite vectors, got %f", sim)
	}
}

func TestEnhancements_Integration(t *testing.T) {
	partitionMgr := NewTemporalPartitionManager(DefaultTemporalPartitionConfig())
	simIndex := NewSimilarityIndex(SimilarityIndexConfig{
		Dimension:      4,
		M:              4,
		EfConstruction: 10,
		EfSearch:       10,
	})
	compressor := NewCompressor(DefaultCompressorConfig())
	optimizer := NewQueryOptimizer(DefaultQueryOptimizerConfig())

	embedder := NewMockEmbedder(4)
	ctx := context.Background()

	entries := []struct {
		id      string
		content string
		time    time.Time
	}{
		{"e1", "First entry about testing", time.Now()},
		{"e2", "Second entry about development", time.Now().Add(-time.Hour)},
		{"e3", "Third entry about debugging", time.Now().Add(-2 * time.Hour)},
	}

	for _, entry := range entries {
		partitionMgr.AddEntry(entry.id, entry.time, 100)

		embedding, err := embedder.Embed(ctx, entry.content)
		if err != nil {
			t.Fatalf("Failed to generate embedding: %v", err)
		}
		simIndex.Insert(entry.id, embedding)

		data := []byte(entry.content + " " + entry.content + " " + entry.content)
		_, err = compressor.Compress(data)
		if err != nil {
			t.Fatalf("Failed to compress: %v", err)
		}
	}

	query := ArchiveQuery{
		Categories: []Category{CategoryInsight},
		Limit:      10,
	}
	plan := optimizer.Plan(query)
	if plan == nil {
		t.Fatal("Failed to generate query plan")
	}

	queryEmb, _ := embedder.Embed(ctx, "testing")
	results := simIndex.Search(queryEmb, 2)
	if len(results) < 1 {
		t.Error("Expected at least 1 similarity result")
	}

	timeResults := partitionMgr.QueryTimeRange(
		time.Now().Add(-3*time.Hour),
		time.Now(),
	)
	if len(timeResults) != 3 {
		t.Errorf("Expected 3 time range results, got %d", len(timeResults))
	}

	t.Logf("Integration test passed - Partitions: %d, Indexed: %d, Plan ops: %d",
		partitionMgr.PartitionCount(),
		simIndex.Count(),
		len(plan.Operations))
}
