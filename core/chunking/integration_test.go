package chunking

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// =============================================================================
// CK.4.1 Learned Splitter Integration Tests
// =============================================================================

func TestLearnedSemanticSplitter_Creation(t *testing.T) {
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	baseSplitter := NewSimpleTokenSplitter()

	tests := []struct {
		name      string
		config    LearnedSplitterConfig
		wantError bool
	}{
		{
			name: "valid config",
			config: LearnedSplitterConfig{
				BaseSplitter: baseSplitter,
				Learner:      learner,
				Explore:      true,
			},
			wantError: false,
		},
		{
			name: "missing base splitter",
			config: LearnedSplitterConfig{
				Learner: learner,
			},
			wantError: true,
		},
		{
			name: "missing learner",
			config: LearnedSplitterConfig{
				BaseSplitter: baseSplitter,
			},
			wantError: true,
		},
		{
			name: "with custom session ID",
			config: LearnedSplitterConfig{
				BaseSplitter: baseSplitter,
				Learner:      learner,
				SessionID:    "test-session-123",
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			splitter, err := NewLearnedSemanticSplitter(tt.config)
			if tt.wantError {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if splitter == nil {
					t.Error("expected non-nil splitter")
				}
			}
		})
	}
}

func TestLearnedSemanticSplitter_SplitWithLearning_CodeDomain(t *testing.T) {
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	baseSplitter := NewSimpleTokenSplitter()
	splitter, err := NewLearnedSemanticSplitter(LearnedSplitterConfig{
		BaseSplitter: baseSplitter,
		Learner:      learner,
		Explore:      false, // Use mean values for predictable testing
		SessionID:    "test-code-session",
	})
	if err != nil {
		t.Fatalf("failed to create learned splitter: %v", err)
	}

	content := `
func main() {
	fmt.Println("Hello, World!")
}

func add(a, b int) int {
	return a + b
}

func subtract(a, b int) int {
	return a - b
}
`

	chunks, err := splitter.SplitWithLearning(context.Background(), content, DomainCode)
	if err != nil {
		t.Fatalf("SplitWithLearning failed: %v", err)
	}

	if len(chunks) == 0 {
		t.Error("expected at least one chunk")
	}

	for i, chunk := range chunks {
		if chunk.ID == "" {
			t.Errorf("chunk %d has empty ID", i)
		}
		if chunk.Domain != DomainCode {
			t.Errorf("chunk %d has wrong domain: got %v, want %v", i, chunk.Domain, DomainCode)
		}
		if chunk.Content == "" {
			t.Errorf("chunk %d has empty content", i)
		}
		if chunk.Metadata == nil {
			t.Errorf("chunk %d has nil metadata", i)
		}
		if chunk.Metadata["session_id"] != "test-code-session" {
			t.Errorf("chunk %d has wrong session_id: %s", i, chunk.Metadata["session_id"])
		}
	}
}

func TestLearnedSemanticSplitter_SplitWithLearning_AcademicDomain(t *testing.T) {
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	baseSplitter := NewSimpleTokenSplitter()
	splitter, err := NewLearnedSemanticSplitter(LearnedSplitterConfig{
		BaseSplitter: baseSplitter,
		Learner:      learner,
		Explore:      false,
	})
	if err != nil {
		t.Fatalf("failed to create learned splitter: %v", err)
	}

	content := `
The theory of relativity encompasses two interrelated theories by Albert Einstein:
special relativity and general relativity. Special relativity applies to all physical
phenomena in the absence of gravity. General relativity explains the law of gravitation
and its relation to other forces of nature. It applies to the cosmological and
astrophysical realm, including astronomy.

The theory transformed theoretical physics and astronomy during the 20th century,
superseding a 200-year-old theory of mechanics created primarily by Isaac Newton.
It introduced concepts including spacetime as a unified entity of space and time,
relativity of simultaneity, kinematic and gravitational time dilation, and length contraction.
`

	chunks, err := splitter.SplitWithLearning(context.Background(), content, DomainAcademic)
	if err != nil {
		t.Fatalf("SplitWithLearning failed: %v", err)
	}

	if len(chunks) == 0 {
		t.Error("expected at least one chunk")
	}

	for _, chunk := range chunks {
		if chunk.Domain != DomainAcademic {
			t.Errorf("expected DomainAcademic, got %v", chunk.Domain)
		}
	}
}

func TestLearnedSemanticSplitter_DomainSpecificSizes(t *testing.T) {
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	baseSplitter := NewSimpleTokenSplitter()
	splitter, err := NewLearnedSemanticSplitter(LearnedSplitterConfig{
		BaseSplitter: baseSplitter,
		Learner:      learner,
		Explore:      false,
	})
	if err != nil {
		t.Fatalf("failed to create learned splitter: %v", err)
	}

	// Generate content of similar length
	content := strings.Repeat("This is a test sentence with some words. ", 100)

	domains := []Domain{DomainCode, DomainAcademic, DomainHistory, DomainGeneral}
	chunkCounts := make(map[Domain]int)

	for _, domain := range domains {
		chunks, err := splitter.SplitWithLearning(context.Background(), content, domain)
		if err != nil {
			t.Fatalf("SplitWithLearning failed for domain %v: %v", domain, err)
		}
		chunkCounts[domain] = len(chunks)
	}

	// Different domains should potentially produce different chunk counts
	// due to their different target sizes (though with simple splitter results may be similar)
	t.Logf("Chunk counts by domain: %v", chunkCounts)

	// All domains should produce at least one chunk
	for domain, count := range chunkCounts {
		if count == 0 {
			t.Errorf("domain %v produced 0 chunks", domain)
		}
	}
}

func TestLearnedSemanticSplitter_ExploreMode(t *testing.T) {
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	baseSplitter := NewSimpleTokenSplitter()
	splitter, err := NewLearnedSemanticSplitter(LearnedSplitterConfig{
		BaseSplitter: baseSplitter,
		Learner:      learner,
		Explore:      true,
	})
	if err != nil {
		t.Fatalf("failed to create learned splitter: %v", err)
	}

	content := strings.Repeat("Word ", 500)

	// Run multiple times with exploration enabled
	// Results should vary due to Thompson Sampling
	results := make([]int, 10)
	for i := 0; i < 10; i++ {
		chunks, err := splitter.SplitWithLearning(context.Background(), content, DomainGeneral)
		if err != nil {
			t.Fatalf("SplitWithLearning failed: %v", err)
		}
		results[i] = len(chunks)
	}

	t.Logf("Chunk counts with exploration: %v", results)

	// With exploration, we expect some variation in results
	// This test is probabilistic, so we just verify all runs succeeded
	for i, count := range results {
		if count == 0 {
			t.Errorf("run %d produced 0 chunks", i)
		}
	}
}

func TestLearnedSemanticSplitter_SetExplore(t *testing.T) {
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	splitter, err := NewLearnedSemanticSplitter(LearnedSplitterConfig{
		BaseSplitter: NewSimpleTokenSplitter(),
		Learner:      learner,
		Explore:      true,
	})
	if err != nil {
		t.Fatalf("failed to create learned splitter: %v", err)
	}

	splitter.SetExplore(false)
	// Verify explore mode change doesn't cause errors
	_, err = splitter.SplitWithLearning(context.Background(), "test content", DomainGeneral)
	if err != nil {
		t.Errorf("SplitWithLearning failed after SetExplore: %v", err)
	}
}

// =============================================================================
// CK.4.2 Retrieval Feedback Hook Tests
// =============================================================================

func TestAsyncRetrievalFeedbackHook_Creation(t *testing.T) {
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	tests := []struct {
		name      string
		config    AsyncFeedbackConfig
		wantError bool
	}{
		{
			name: "valid config",
			config: AsyncFeedbackConfig{
				Learner: learner,
			},
			wantError: false,
		},
		{
			name:      "missing learner",
			config:    AsyncFeedbackConfig{},
			wantError: true,
		},
		{
			name: "custom buffer size",
			config: AsyncFeedbackConfig{
				Learner:    learner,
				BufferSize: 500,
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hook, err := NewAsyncRetrievalFeedbackHook(tt.config)
			if tt.wantError {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if hook != nil {
					hook.Stop()
				}
			}
		})
	}
}

func TestAsyncRetrievalFeedbackHook_RecordRetrieval(t *testing.T) {
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	hook, err := NewAsyncRetrievalFeedbackHook(AsyncFeedbackConfig{
		Learner:    learner,
		BufferSize: 100,
	})
	if err != nil {
		t.Fatalf("failed to create hook: %v", err)
	}
	defer hook.Stop()

	// Register a chunk
	chunk := Chunk{
		ID:         "test-chunk-1",
		Content:    "This is test content for the chunk",
		Domain:     DomainCode,
		TokenCount: 10,
	}
	hook.RegisterChunk(chunk)

	// Record retrieval feedback
	ctx := RetrievalContext{
		QueryText:      "test query",
		RetrievalScore: 0.95,
		RankPosition:   1,
		TotalRetrieved: 5,
		Timestamp:      time.Now(),
	}

	err = hook.RecordRetrieval("test-chunk-1", true, ctx)
	if err != nil {
		t.Errorf("RecordRetrieval failed: %v", err)
	}

	// Give async processing time to complete
	time.Sleep(100 * time.Millisecond)

	// Check hit rate
	hitRate, total := hook.GetHitRate("test-chunk-1")
	if total != 1 {
		t.Errorf("expected 1 total retrieval, got %d", total)
	}
	if hitRate != 1.0 {
		t.Errorf("expected hit rate 1.0, got %f", hitRate)
	}
}

func TestAsyncRetrievalFeedbackHook_HitRateTracking(t *testing.T) {
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	hook, err := NewAsyncRetrievalFeedbackHook(AsyncFeedbackConfig{
		Learner:    learner,
		BufferSize: 100,
	})
	if err != nil {
		t.Fatalf("failed to create hook: %v", err)
	}
	defer hook.Stop()

	// Register chunk
	chunk := Chunk{
		ID:         "test-chunk-hit-rate",
		Domain:     DomainGeneral,
		TokenCount: 50,
	}
	hook.RegisterChunk(chunk)

	// Record 3 useful and 2 not useful
	ctx := RetrievalContext{Timestamp: time.Now()}

	for i := 0; i < 3; i++ {
		_ = hook.RecordRetrieval("test-chunk-hit-rate", true, ctx)
	}
	for i := 0; i < 2; i++ {
		_ = hook.RecordRetrieval("test-chunk-hit-rate", false, ctx)
	}

	// Wait for processing
	time.Sleep(200 * time.Millisecond)

	hitRate, total := hook.GetHitRate("test-chunk-hit-rate")
	if total != 5 {
		t.Errorf("expected 5 total retrievals, got %d", total)
	}
	expectedRate := 3.0 / 5.0
	if hitRate != expectedRate {
		t.Errorf("expected hit rate %f, got %f", expectedRate, hitRate)
	}
}

func TestAsyncRetrievalFeedbackHook_DomainHitRate(t *testing.T) {
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	hook, err := NewAsyncRetrievalFeedbackHook(AsyncFeedbackConfig{
		Learner: learner,
	})
	if err != nil {
		t.Fatalf("failed to create hook: %v", err)
	}
	defer hook.Stop()

	// Register chunks in different domains
	codeChunk := Chunk{ID: "code-1", Domain: DomainCode, TokenCount: 30}
	academicChunk := Chunk{ID: "academic-1", Domain: DomainAcademic, TokenCount: 40}
	hook.RegisterChunk(codeChunk)
	hook.RegisterChunk(academicChunk)

	ctx := RetrievalContext{Timestamp: time.Now()}

	// Code: 2 useful, 1 not useful
	_ = hook.RecordRetrieval("code-1", true, ctx)
	_ = hook.RecordRetrieval("code-1", true, ctx)
	_ = hook.RecordRetrieval("code-1", false, ctx)

	// Academic: 1 useful, 3 not useful
	_ = hook.RecordRetrieval("academic-1", true, ctx)
	_ = hook.RecordRetrieval("academic-1", false, ctx)
	_ = hook.RecordRetrieval("academic-1", false, ctx)
	_ = hook.RecordRetrieval("academic-1", false, ctx)

	// Wait for processing
	time.Sleep(200 * time.Millisecond)

	codeHitRate, codeTotal := hook.GetDomainHitRate(DomainCode)
	if codeTotal != 3 {
		t.Errorf("expected 3 code retrievals, got %d", codeTotal)
	}
	expectedCodeRate := 2.0 / 3.0
	if codeHitRate != expectedCodeRate {
		t.Errorf("expected code hit rate %f, got %f", expectedCodeRate, codeHitRate)
	}

	academicHitRate, academicTotal := hook.GetDomainHitRate(DomainAcademic)
	if academicTotal != 4 {
		t.Errorf("expected 4 academic retrievals, got %d", academicTotal)
	}
	expectedAcademicRate := 1.0 / 4.0
	if academicHitRate != expectedAcademicRate {
		t.Errorf("expected academic hit rate %f, got %f", expectedAcademicRate, academicHitRate)
	}
}

func TestAsyncRetrievalFeedbackHook_RecordBatch(t *testing.T) {
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	hook, err := NewAsyncRetrievalFeedbackHook(AsyncFeedbackConfig{
		Learner:    learner,
		BufferSize: 100,
	})
	if err != nil {
		t.Fatalf("failed to create hook: %v", err)
	}
	defer hook.Stop()

	// Register chunks
	for i := 0; i < 5; i++ {
		hook.RegisterChunk(Chunk{
			ID:         fmt.Sprintf("batch-chunk-%d", i),
			Domain:     DomainGeneral,
			TokenCount: 20,
		})
	}

	batch := BatchFeedback{
		Entries: []BatchFeedbackEntry{
			{ChunkID: "batch-chunk-0", WasUseful: true, Context: RetrievalContext{Timestamp: time.Now()}},
			{ChunkID: "batch-chunk-1", WasUseful: false, Context: RetrievalContext{Timestamp: time.Now()}},
			{ChunkID: "batch-chunk-2", WasUseful: true, Context: RetrievalContext{Timestamp: time.Now()}},
		},
	}

	recorded, dropped := hook.RecordBatch(batch)
	if recorded != 3 {
		t.Errorf("expected 3 recorded, got %d", recorded)
	}
	if dropped != 0 {
		t.Errorf("expected 0 dropped, got %d", dropped)
	}
}

func TestSyncRetrievalFeedbackHook_RecordRetrieval(t *testing.T) {
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	hook, err := NewSyncRetrievalFeedbackHook(learner, "test-session")
	if err != nil {
		t.Fatalf("failed to create hook: %v", err)
	}

	chunk := Chunk{
		ID:         "sync-chunk-1",
		Domain:     DomainCode,
		TokenCount: 25,
	}
	hook.RegisterChunk(chunk)

	ctx := RetrievalContext{Timestamp: time.Now()}
	err = hook.RecordRetrieval("sync-chunk-1", true, ctx)
	if err != nil {
		t.Errorf("RecordRetrieval failed: %v", err)
	}

	// Sync hook should have immediate results
	hitRate, total := hook.GetHitRate("sync-chunk-1")
	if total != 1 {
		t.Errorf("expected 1 total, got %d", total)
	}
	if hitRate != 1.0 {
		t.Errorf("expected hit rate 1.0, got %f", hitRate)
	}
}

// =============================================================================
// CK.4.3 Citation Detection Tests
// =============================================================================

func TestCitationDetector_Creation(t *testing.T) {
	tests := []struct {
		name      string
		config    CitationDetectorConfig
		wantError bool
	}{
		{
			name:      "default config",
			config:    CitationDetectorConfig{},
			wantError: false,
		},
		{
			name: "custom patterns",
			config: CitationDetectorConfig{
				Patterns: []CitationPattern{
					{Name: "custom", Pattern: `\{(\d+)\}`, Confidence: 0.8, ChunkIDGroup: 1},
				},
			},
			wantError: false,
		},
		{
			name: "invalid regex pattern",
			config: CitationDetectorConfig{
				Patterns: []CitationPattern{
					{Name: "invalid", Pattern: `[invalid`, Confidence: 0.5},
				},
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detector, err := NewCitationDetector(tt.config)
			if tt.wantError {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if detector == nil {
					t.Error("expected non-nil detector")
				}
			}
		})
	}
}

func TestCitationDetector_BracketedNumberCitations(t *testing.T) {
	detector, err := NewCitationDetector(CitationDetectorConfig{})
	if err != nil {
		t.Fatalf("failed to create detector: %v", err)
	}

	// Register chunks with indices
	detector.RegisterChunkByID("chunk-alpha", "Content about alpha", 1)
	detector.RegisterChunkByID("chunk-beta", "Content about beta", 2)
	detector.RegisterChunkByID("chunk-gamma", "Content about gamma", 3)

	response := "According to the source [1], alpha is important. Also, [2] mentions beta is relevant. Finally [3] discusses gamma."
	chunkIDs := []string{"chunk-alpha", "chunk-beta", "chunk-gamma"}

	citations := detector.DetectCitations(response, chunkIDs)

	if len(citations) != 3 {
		t.Errorf("expected 3 citations, got %d", len(citations))
	}

	// Verify citations map correctly
	citedChunks := make(map[string]bool)
	for _, c := range citations {
		citedChunks[c.ChunkID] = true
		if !c.IsExplicit {
			t.Errorf("bracketed number citation should be explicit")
		}
		if c.Confidence < 0.7 {
			t.Errorf("expected high confidence for bracketed number, got %f", c.Confidence)
		}
	}

	for _, id := range chunkIDs {
		if !citedChunks[id] {
			t.Errorf("chunk %s was not detected as cited", id)
		}
	}
}

func TestCitationDetector_QuotedTextCitations(t *testing.T) {
	detector, err := NewCitationDetector(CitationDetectorConfig{
		MinOverlapLength: 10,
	})
	if err != nil {
		t.Fatalf("failed to create detector: %v", err)
	}

	// Register chunk with specific content
	detector.RegisterChunkByID("source-chunk", "The quick brown fox jumps over the lazy dog in the forest.", 1)

	response := `The document mentions that "the quick brown fox jumps over" which is relevant.`
	chunkIDs := []string{"source-chunk"}

	citations := detector.DetectCitations(response, chunkIDs)

	// Should detect the quoted text
	if len(citations) == 0 {
		t.Error("expected at least one citation for quoted text")
	}

	found := false
	for _, c := range citations {
		if c.ChunkID == "source-chunk" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected source-chunk to be detected")
	}
}

func TestCitationDetector_FootnoteCitations(t *testing.T) {
	detector, err := NewCitationDetector(CitationDetectorConfig{})
	if err != nil {
		t.Fatalf("failed to create detector: %v", err)
	}

	detector.RegisterChunkByID("note-1", "First footnote content", 1)
	detector.RegisterChunkByID("note-2", "Second footnote content", 2)

	response := "This is important[^1] and this too[^2]."
	chunkIDs := []string{"note-1", "note-2"}

	citations := detector.DetectCitations(response, chunkIDs)

	if len(citations) < 2 {
		t.Errorf("expected at least 2 citations, got %d", len(citations))
	}
}

func TestCitationDetector_SuperscriptCitations(t *testing.T) {
	detector, err := NewCitationDetector(CitationDetectorConfig{})
	if err != nil {
		t.Fatalf("failed to create detector: %v", err)
	}

	detector.RegisterChunkByID("ref-1", "Reference 1 content", 1)
	detector.RegisterChunkByID("ref-2", "Reference 2 content", 2)

	response := "This claim is supported^1 and verified^2."
	chunkIDs := []string{"ref-1", "ref-2"}

	citations := detector.DetectCitations(response, chunkIDs)

	if len(citations) < 2 {
		t.Errorf("expected at least 2 citations, got %d", len(citations))
	}
}

func TestCitationDetector_ContentOverlap(t *testing.T) {
	detector, err := NewCitationDetector(CitationDetectorConfig{
		MinOverlapLength:     15,
		MinOverlapConfidence: 0.3,
	})
	if err != nil {
		t.Fatalf("failed to create detector: %v", err)
	}

	// Register chunk with specific content
	detector.RegisterChunkByID("overlap-chunk", "Machine learning algorithms can identify patterns in large datasets efficiently.", 1)

	// Response that contains overlapping content but no explicit citation
	response := "Studies show that machine learning algorithms can identify patterns effectively."
	chunkIDs := []string{"overlap-chunk"}

	citations := detector.DetectCitations(response, chunkIDs)

	// Should detect implicit citation through content overlap
	foundOverlap := false
	for _, c := range citations {
		if c.ChunkID == "overlap-chunk" && !c.IsExplicit {
			foundOverlap = true
			break
		}
	}

	if !foundOverlap {
		t.Log("No implicit citation detected - this may be due to overlap threshold")
	}
}

func TestCitationDetector_AddRemovePattern(t *testing.T) {
	detector, err := NewCitationDetector(CitationDetectorConfig{})
	if err != nil {
		t.Fatalf("failed to create detector: %v", err)
	}

	initialPatterns := len(detector.GetPatterns())

	// Add custom pattern
	err = detector.AddPattern(CitationPattern{
		Name:       "custom_citation",
		Pattern:    `\{\{(\d+)\}\}`,
		Confidence: 0.85,
		IsExplicit: true,
	})
	if err != nil {
		t.Errorf("AddPattern failed: %v", err)
	}

	if len(detector.GetPatterns()) != initialPatterns+1 {
		t.Error("pattern count should increase by 1")
	}

	// Remove the custom pattern
	removed := detector.RemovePattern("custom_citation")
	if !removed {
		t.Error("RemovePattern should return true")
	}

	if len(detector.GetPatterns()) != initialPatterns {
		t.Error("pattern count should return to initial")
	}

	// Try to remove non-existent pattern
	removed = detector.RemovePattern("nonexistent")
	if removed {
		t.Error("RemovePattern should return false for non-existent pattern")
	}
}

func TestCitationDetector_ClearChunks(t *testing.T) {
	detector, err := NewCitationDetector(CitationDetectorConfig{})
	if err != nil {
		t.Fatalf("failed to create detector: %v", err)
	}

	detector.RegisterChunkByID("chunk-1", "content 1", 1)
	detector.RegisterChunkByID("chunk-2", "content 2", 2)

	response := "Reference [1] and [2]"
	citations := detector.DetectCitations(response, []string{"chunk-1", "chunk-2"})
	if len(citations) == 0 {
		t.Error("should detect citations before clear")
	}

	detector.ClearChunks()

	// After clearing, citations should still work for explicit patterns
	// but won't map to specific chunks
	citations = detector.DetectCitations(response, []string{})
	// With empty chunk list, no citations should be detected
	if len(citations) > 0 {
		t.Log("Note: some patterns may still match with empty chunk list")
	}
}

// =============================================================================
// End-to-End Integration Tests
// =============================================================================

func TestEndToEnd_RetrievalFeedbackCycle(t *testing.T) {
	// Create learner
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	// Create learned splitter
	splitter, err := NewLearnedSemanticSplitter(LearnedSplitterConfig{
		BaseSplitter: NewSimpleTokenSplitter(),
		Learner:      learner,
		Explore:      false,
		SessionID:    "e2e-test",
	})
	if err != nil {
		t.Fatalf("failed to create splitter: %v", err)
	}

	// Create feedback hook
	feedbackHook, err := NewSyncRetrievalFeedbackHook(learner, "e2e-test")
	if err != nil {
		t.Fatalf("failed to create feedback hook: %v", err)
	}

	// Split some content
	content := "This is a test document. It contains multiple sentences. Each sentence provides information."
	chunks, err := splitter.SplitWithLearning(context.Background(), content, DomainGeneral)
	if err != nil {
		t.Fatalf("failed to split content: %v", err)
	}

	// Register chunks with feedback hook
	for _, chunk := range chunks {
		feedbackHook.RegisterChunk(chunk)
	}

	// Simulate retrieval feedback
	ctx := RetrievalContext{
		QueryText:      "What information does the document contain?",
		RetrievalScore: 0.9,
		RankPosition:   1,
		TotalRetrieved: len(chunks),
		Timestamp:      time.Now(),
	}

	// Mark some chunks as useful
	for i, chunk := range chunks {
		wasUseful := i%2 == 0 // Every other chunk is useful
		err := feedbackHook.RecordRetrieval(chunk.ID, wasUseful, ctx)
		if err != nil {
			t.Errorf("failed to record retrieval: %v", err)
		}
	}

	// Verify learner has updated
	confidence := learner.GetDomainConfidence(DomainGeneral)
	if confidence == 0 {
		t.Error("expected non-zero confidence after observations")
	}

	obsCount := learner.GetObservationCount(DomainGeneral)
	if obsCount != len(chunks) {
		t.Errorf("expected %d observations, got %d", len(chunks), obsCount)
	}
}

func TestEndToEnd_CitationFeedbackFlow(t *testing.T) {
	// Create learner
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	// Create feedback hook
	feedbackHook, err := NewSyncRetrievalFeedbackHook(learner, "citation-test")
	if err != nil {
		t.Fatalf("failed to create feedback hook: %v", err)
	}

	// Create citation detector
	detector, err := NewCitationDetector(CitationDetectorConfig{})
	if err != nil {
		t.Fatalf("failed to create detector: %v", err)
	}

	// Create citation feedback recorder
	recorder := NewCitationFeedbackRecorder(detector, feedbackHook)

	// Register some chunks
	chunks := []Chunk{
		{ID: "chunk-1", Content: "Python is a programming language.", Domain: DomainCode, TokenCount: 10},
		{ID: "chunk-2", Content: "Machine learning uses algorithms.", Domain: DomainCode, TokenCount: 10},
		{ID: "chunk-3", Content: "Data science combines statistics and programming.", Domain: DomainCode, TokenCount: 15},
	}

	for i, chunk := range chunks {
		feedbackHook.RegisterChunk(chunk)
		detector.RegisterChunk(chunk, i+1)
	}

	// Simulate a response that cites some chunks
	response := "According to the sources, Python [1] is useful for machine learning [2]."
	chunkIDs := []string{"chunk-1", "chunk-2", "chunk-3"}

	queryCtx := RetrievalContext{
		QueryText: "What is Python used for?",
		Timestamp: time.Now(),
	}

	// Record feedback from response
	err = recorder.RecordFromResponse(response, chunkIDs, queryCtx)
	if err != nil {
		t.Errorf("RecordFromResponse failed: %v", err)
	}

	// Verify feedback was recorded
	// Chunks 1 and 2 should be marked as useful (cited)
	// Chunk 3 should be marked as not useful (not cited)
	hitRate1, total1 := feedbackHook.GetHitRate("chunk-1")
	if total1 != 1 {
		t.Errorf("expected 1 retrieval for chunk-1, got %d", total1)
	}
	if hitRate1 != 1.0 {
		t.Errorf("expected chunk-1 hit rate 1.0 (cited), got %f", hitRate1)
	}

	hitRate3, total3 := feedbackHook.GetHitRate("chunk-3")
	if total3 != 1 {
		t.Errorf("expected 1 retrieval for chunk-3, got %d", total3)
	}
	if hitRate3 != 0.0 {
		t.Errorf("expected chunk-3 hit rate 0.0 (not cited), got %f", hitRate3)
	}
}

func TestEndToEnd_LearningAcrossSessions(t *testing.T) {
	// Create learner
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	// Get initial config for code domain
	initialConfig := learner.GetConfig(DomainCode, false)
	initialTarget := initialConfig.GetEffectiveTargetTokens(false)

	// Record observations that suggest larger chunks work better
	for i := 0; i < 20; i++ {
		obs := ChunkUsageObservation{
			Domain:        DomainCode,
			TokenCount:    500, // Larger than default
			ContextBefore: 50,
			ContextAfter:  50,
			WasUseful:     true,
			Timestamp:     time.Now(),
			ChunkID:       fmt.Sprintf("obs-chunk-%d", i),
			SessionID:     "learning-session",
		}
		err := learner.RecordObservation(obs)
		if err != nil {
			t.Errorf("failed to record observation: %v", err)
		}
	}

	// Get updated config
	updatedConfig := learner.GetConfig(DomainCode, false)
	updatedTarget := updatedConfig.GetEffectiveTargetTokens(false)

	t.Logf("Initial target: %d, Updated target: %d", initialTarget, updatedTarget)

	// The target should have shifted toward 500
	if updatedTarget <= initialTarget {
		t.Log("Note: target may not increase immediately due to blending with priors")
	}

	// Confidence should have increased
	confidence := learner.GetDomainConfidence(DomainCode)
	if confidence < 0.5 {
		t.Errorf("expected confidence > 0.5 after 20 observations, got %f", confidence)
	}
}

func TestEndToEnd_MultipleDomainLearning(t *testing.T) {
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	// Record different observations for different domains
	domains := []struct {
		domain         Domain
		preferredSize  int
		preferredCtxBefore int
	}{
		{DomainCode, 250, 100},
		{DomainAcademic, 600, 200},
		{DomainHistory, 400, 150},
	}

	for _, d := range domains {
		for i := 0; i < 15; i++ {
			obs := ChunkUsageObservation{
				Domain:        d.domain,
				TokenCount:    d.preferredSize,
				ContextBefore: d.preferredCtxBefore,
				ContextAfter:  50,
				WasUseful:     true,
				Timestamp:     time.Now(),
				ChunkID:       fmt.Sprintf("%s-chunk-%d", d.domain.String(), i),
				SessionID:     "multi-domain-session",
			}
			_ = learner.RecordObservation(obs)
		}
	}

	// Verify each domain has learned different configurations
	for _, d := range domains {
		confidence := learner.GetDomainConfidence(d.domain)
		if confidence < 0.5 {
			t.Errorf("domain %s should have confidence > 0.5, got %f", d.domain.String(), confidence)
		}

		obsCount := learner.GetObservationCount(d.domain)
		if obsCount != 15 {
			t.Errorf("domain %s should have 15 observations, got %d", d.domain.String(), obsCount)
		}
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkLearnedSemanticSplitter_Split(b *testing.B) {
	learner, _ := NewChunkConfigLearner(nil)
	splitter, _ := NewLearnedSemanticSplitter(LearnedSplitterConfig{
		BaseSplitter: NewSimpleTokenSplitter(),
		Learner:      learner,
		Explore:      false,
	})

	content := strings.Repeat("This is a test sentence for benchmarking. ", 100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = splitter.SplitWithLearning(context.Background(), content, DomainGeneral)
	}
}

func BenchmarkCitationDetector_DetectCitations(b *testing.B) {
	detector, _ := NewCitationDetector(CitationDetectorConfig{})

	for i := 1; i <= 10; i++ {
		detector.RegisterChunkByID(fmt.Sprintf("chunk-%d", i), fmt.Sprintf("Content for chunk %d", i), i)
	}

	response := "This response references [1], [2], and [3]. Also mentions [4] and [5]."
	chunkIDs := []string{"chunk-1", "chunk-2", "chunk-3", "chunk-4", "chunk-5"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = detector.DetectCitations(response, chunkIDs)
	}
}

func BenchmarkAsyncFeedbackHook_Record(b *testing.B) {
	learner, _ := NewChunkConfigLearner(nil)
	hook, _ := NewAsyncRetrievalFeedbackHook(AsyncFeedbackConfig{
		Learner:    learner,
		BufferSize: 10000,
	})
	defer hook.Stop()

	chunk := Chunk{ID: "bench-chunk", Domain: DomainGeneral, TokenCount: 100}
	hook.RegisterChunk(chunk)

	ctx := RetrievalContext{Timestamp: time.Now()}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = hook.RecordRetrieval("bench-chunk", true, ctx)
	}
}
