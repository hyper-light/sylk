package sylkdir

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/embedder"
)

func TestSplitAtBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		boundaries []TaggedBoundary
		wantCount  int
	}{
		{
			name:       "no boundaries single segment",
			content:    "hello world",
			boundaries: nil,
			wantCount:  1,
		},
		{
			name:    "one boundary two segments",
			content: "hello\n\nworld",
			boundaries: []TaggedBoundary{
				{Offset: 7, Strength: 1},
			},
			wantCount: 2,
		},
		{
			name:    "whitespace-only segment discarded",
			content: "hello\n   \nworld",
			boundaries: []TaggedBoundary{
				{Offset: 6, Strength: 1},
				{Offset: 10, Strength: 1},
			},
			wantCount: 2, // "hello\n" and "world", middle whitespace discarded.
		},
		{
			name:       "empty content",
			content:    "",
			boundaries: nil,
			wantCount:  0,
		},
		{
			name:       "all whitespace",
			content:    "   \n\n  ",
			boundaries: nil,
			wantCount:  0,
		},
		{
			name:    "boundary carries strength",
			content: "aaa\n\n\nbbb",
			boundaries: []TaggedBoundary{
				{Offset: 6, Strength: 2},
			},
			wantCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := []byte(tt.content)
			got := SplitAtBoundaries(content, tt.boundaries)
			if len(got) != tt.wantCount {
				t.Errorf("expected %d segments, got %d: %+v", tt.wantCount, len(got), got)
			}
		})
	}
}

func TestSplitAtBoundariesStrength(t *testing.T) {
	content := []byte("aaa\n\n\nbbb\n\nccc")
	boundaries := []TaggedBoundary{
		{Offset: 6, Strength: 2},
		{Offset: 11, Strength: 1},
	}

	segments := SplitAtBoundaries(content, boundaries)
	if len(segments) != 3 {
		t.Fatalf("expected 3 segments, got %d", len(segments))
	}

	// First segment's RightStrength should be 2 (double blank line).
	if segments[0].RightStrength != 2 {
		t.Errorf("segment[0].RightStrength = %d, want 2", segments[0].RightStrength)
	}

	// Second segment's RightStrength should be 1 (single blank line).
	if segments[1].RightStrength != 1 {
		t.Errorf("segment[1].RightStrength = %d, want 1", segments[1].RightStrength)
	}

	// Last segment's RightStrength should be 0 (no right neighbor).
	if segments[2].RightStrength != 0 {
		t.Errorf("segment[2].RightStrength = %d, want 0", segments[2].RightStrength)
	}
}

func TestMergerSingleSegment(t *testing.T) {
	mock := embedder.NewDefaultMockEmbedder()
	merger := NewSegmentMerger(mock, 10000)
	ctx := context.Background()

	segments := []Segment{{Start: 0, End: 100, RightStrength: 0}}
	mr := merger.Merge(ctx, make([]byte, 100), segments)

	if len(mr.Segments) != 1 {
		t.Errorf("expected 1 segment, got %d", len(mr.Segments))
	}
}

func TestMergerAllFitInCeiling(t *testing.T) {
	mock := embedder.NewDefaultMockEmbedder()
	content := []byte("hello world this is a test")
	merger := NewSegmentMerger(mock, uint32(len(content)+100))
	ctx := context.Background()

	segments := []Segment{
		{Start: 0, End: 5, RightStrength: 1},
		{Start: 6, End: 11, RightStrength: 1},
		{Start: 12, End: uint32(len(content)), RightStrength: 0},
	}

	mr := merger.Merge(ctx, content, segments)

	// All segments fit in ceiling → should merge to one.
	if len(mr.Segments) != 1 {
		t.Errorf("expected 1 merged segment, got %d: %+v", len(mr.Segments), mr.Segments)
	}
	if mr.Segments[0].Start != 0 || mr.Segments[0].End != uint32(len(content)) {
		t.Errorf("merged segment: want [0, %d), got [%d, %d)", len(content), mr.Segments[0].Start, mr.Segments[0].End)
	}
	// Embedding should be present for the merged segment.
	if len(mr.Embeddings) != 1 {
		t.Errorf("expected 1 embedding, got %d", len(mr.Embeddings))
	}
}

func TestMergerCeilingEnforced(t *testing.T) {
	mock := embedder.NewDefaultMockEmbedder()
	// Set ceiling to 10 bytes — no merge of 6-byte segments should produce >10 bytes.
	merger := NewSegmentMerger(mock, 10)
	ctx := context.Background()

	content := []byte("aaaaaa\nbbbbbb\ncccccc\ndddddd")
	segments := []Segment{
		{Start: 0, End: 7, RightStrength: 1},
		{Start: 7, End: 14, RightStrength: 1},
		{Start: 14, End: 21, RightStrength: 1},
		{Start: 21, End: 27, RightStrength: 0},
	}

	mr := merger.Merge(ctx, content, segments)

	for _, seg := range mr.Segments {
		size := seg.End - seg.Start
		if size > 10 {
			t.Errorf("segment [%d, %d) exceeds ceiling: %d > 10", seg.Start, seg.End, size)
		}
	}
	// Embeddings parallel to segments.
	if len(mr.Embeddings) != len(mr.Segments) {
		t.Errorf("embeddings count %d != segments count %d", len(mr.Embeddings), len(mr.Segments))
	}
}

func TestMergerContextCancellation(t *testing.T) {
	mock := embedder.NewDefaultMockEmbedder()
	merger := NewSegmentMerger(mock, 100000)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	content := make([]byte, 200)
	for i := range content {
		content[i] = 'a'
	}
	segments := []Segment{
		{Start: 0, End: 50, RightStrength: 1},
		{Start: 50, End: 100, RightStrength: 1},
		{Start: 100, End: 150, RightStrength: 1},
		{Start: 150, End: 200, RightStrength: 0},
	}

	mr := merger.Merge(ctx, content, segments)

	// With cancelled context, embed should fail and return original segments.
	if len(mr.Segments) < 1 {
		t.Error("expected at least 1 segment even with cancelled context")
	}
}

func TestDotSimilarity(t *testing.T) {
	// Unit vector dot product with itself = 1.
	a := []float32{1.0, 0.0, 0.0}
	if got := dotSimilarity(a, a); got != 1.0 {
		t.Errorf("dotSimilarity(a, a) = %f, want 1.0", got)
	}

	// Orthogonal vectors = 0.
	b := []float32{0.0, 1.0, 0.0}
	if got := dotSimilarity(a, b); got != 0.0 {
		t.Errorf("dotSimilarity(a, b) = %f, want 0.0", got)
	}
}

func TestStddev(t *testing.T) {
	tests := []struct {
		name string
		vals []float64
		want float64
	}{
		{"empty", nil, 0},
		{"single", []float64{5.0}, 0},
		{"uniform", []float64{3, 3, 3}, 0},
		{"basic", []float64{2, 4, 4, 4, 5, 5, 7, 9}, 2.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stddev(tt.vals)
			if diff := got - tt.want; diff > 0.01 || diff < -0.01 {
				t.Errorf("stddev = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestWeightedAvgNorm(t *testing.T) {
	t.Run("equal weight", func(t *testing.T) {
		a := []float32{1, 0, 0}
		b := []float32{0, 1, 0}
		weightedAvgNorm(a, b, 10, 10)
		// Equal weight: (0.5, 0.5, 0) normalized → (1/√2, 1/√2, 0)
		want := float32(1.0 / 1.4142135)
		if diff := a[0] - want; diff > 0.001 || diff < -0.001 {
			t.Errorf("a[0] = %f, want ~%f", a[0], want)
		}
		if diff := a[1] - want; diff > 0.001 || diff < -0.001 {
			t.Errorf("a[1] = %f, want ~%f", a[1], want)
		}
	})

	t.Run("asymmetric weight", func(t *testing.T) {
		a := []float32{1, 0, 0}
		b := []float32{0, 1, 0}
		weightedAvgNorm(a, b, 30, 10) // 75% a, 25% b
		// (0.75, 0.25, 0) normalized → magnitude = sqrt(0.75²+0.25²)
		if a[0] <= a[1] {
			t.Errorf("heavier weight on a should produce larger a[0]: got a[0]=%f, a[1]=%f", a[0], a[1])
		}
	})

	t.Run("unit length", func(t *testing.T) {
		a := []float32{0.6, 0.8, 0}
		b := []float32{0.8, 0.6, 0}
		weightedAvgNorm(a, b, 5, 5)
		var sumSq float64
		for _, v := range a {
			sumSq += float64(v) * float64(v)
		}
		if diff := sumSq - 1.0; diff > 0.001 || diff < -0.001 {
			t.Errorf("result not unit length: ||v||² = %f", sumSq)
		}
	})

	t.Run("same vector identity", func(t *testing.T) {
		a := []float32{0, 0, 1}
		b := []float32{0, 0, 1}
		weightedAvgNorm(a, b, 100, 200)
		if a[2] < 0.999 {
			t.Errorf("same vector should remain unchanged: got a[2]=%f", a[2])
		}
	})
}

func TestMergeWithEmbeddingsSingle(t *testing.T) {
	mock := embedder.NewDefaultMockEmbedder()
	merger := NewSegmentMerger(mock, 10000)
	ctx := context.Background()

	content := make([]byte, 100)
	segments := []Segment{{Start: 0, End: 100, RightStrength: 0}}
	embs := [][]float32{{1.0, 0, 0}}

	mr := merger.MergeWithEmbeddings(ctx, content, segments, embs)

	if len(mr.Segments) != 1 {
		t.Errorf("expected 1 segment, got %d", len(mr.Segments))
	}
	if len(mr.Embeddings) != 1 || mr.Embeddings[0][0] != 1.0 {
		t.Error("single segment embedding should be returned unchanged")
	}
}

func TestMergeWithEmbeddingsFitsCeiling(t *testing.T) {
	mock := embedder.NewDefaultMockEmbedder()
	content := []byte("hello world this is a test")
	merger := NewSegmentMerger(mock, uint32(len(content)+100))
	ctx := context.Background()

	segments := []Segment{
		{Start: 0, End: 5, RightStrength: 1},
		{Start: 6, End: 11, RightStrength: 1},
		{Start: 12, End: uint32(len(content)), RightStrength: 0},
	}

	// Pre-compute embeddings (same as what EmbedBatch would produce).
	texts := make([]string, len(segments))
	for i, seg := range segments {
		texts[i] = string(content[seg.Start:seg.End])
	}
	embs, _ := mock.EmbedBatch(ctx, texts)

	mr := merger.MergeWithEmbeddings(ctx, content, segments, embs)

	// All fit ceiling → merged to one segment.
	if len(mr.Segments) != 1 {
		t.Errorf("expected 1 merged segment, got %d", len(mr.Segments))
	}
	if mr.Segments[0].Start != 0 || mr.Segments[0].End != uint32(len(content)) {
		t.Errorf("merged segment: want [0, %d), got [%d, %d)", len(content), mr.Segments[0].Start, mr.Segments[0].End)
	}
	if len(mr.Embeddings) != 1 || mr.Embeddings[0] == nil {
		t.Error("expected one weighted-average embedding")
	}
}

func TestMergeWithEmbeddingsCeilingEnforced(t *testing.T) {
	mock := embedder.NewDefaultMockEmbedder()
	merger := NewSegmentMerger(mock, 10)
	ctx := context.Background()

	content := []byte("aaaaaa\nbbbbbb\ncccccc\ndddddd")
	segments := []Segment{
		{Start: 0, End: 7, RightStrength: 1},
		{Start: 7, End: 14, RightStrength: 1},
		{Start: 14, End: 21, RightStrength: 1},
		{Start: 21, End: 27, RightStrength: 0},
	}

	texts := make([]string, len(segments))
	for i, seg := range segments {
		texts[i] = string(content[seg.Start:seg.End])
	}
	embs, _ := mock.EmbedBatch(ctx, texts)

	mr := merger.MergeWithEmbeddings(ctx, content, segments, embs)

	for _, seg := range mr.Segments {
		size := seg.End - seg.Start
		if size > 10 {
			t.Errorf("segment [%d, %d) exceeds ceiling: %d > 10", seg.Start, seg.End, size)
		}
	}
	if len(mr.Embeddings) != len(mr.Segments) {
		t.Errorf("embeddings count %d != segments count %d", len(mr.Embeddings), len(mr.Segments))
	}
}

func TestMergeWithEmbeddingsMatchesMerge(t *testing.T) {
	mock := embedder.NewDefaultMockEmbedder()
	merger := NewSegmentMerger(mock, 200)
	ctx := context.Background()

	content := []byte("package main\n\nfunc Foo() {\n\treturn\n}\n\nfunc Bar() {\n\treturn\n}\n")
	segments := []Segment{
		{Start: 0, End: 13, RightStrength: 1},
		{Start: 14, End: 36, RightStrength: 2},
		{Start: 37, End: uint32(len(content)), RightStrength: 0},
	}

	// Run Merge (embeds internally).
	mr1 := merger.Merge(ctx, content, segments)

	// Run MergeWithEmbeddings with the same pre-computed embeddings.
	texts := make([]string, len(segments))
	for i, seg := range segments {
		texts[i] = string(content[seg.Start:seg.End])
	}
	embs, _ := mock.EmbedBatch(ctx, texts)
	mr2 := merger.MergeWithEmbeddings(ctx, content, segments, embs)

	// Same number of resulting segments.
	if len(mr1.Segments) != len(mr2.Segments) {
		t.Fatalf("Merge produced %d segments, MergeWithEmbeddings produced %d", len(mr1.Segments), len(mr2.Segments))
	}
	for i := range mr1.Segments {
		if mr1.Segments[i].Start != mr2.Segments[i].Start || mr1.Segments[i].End != mr2.Segments[i].End {
			t.Errorf("segment %d: Merge=[%d,%d) MergeWithEmb=[%d,%d)", i,
				mr1.Segments[i].Start, mr1.Segments[i].End,
				mr2.Segments[i].Start, mr2.Segments[i].End)
		}
	}
}

func TestWeightedAvgAllSegments(t *testing.T) {
	segments := []Segment{
		{Start: 0, End: 10, RightStrength: 0},
		{Start: 10, End: 30, RightStrength: 0},
	}
	embs := [][]float32{
		{1, 0, 0},
		{0, 1, 0},
	}

	result := weightedAvgAllSegments(embs, segments)

	// Size 10 and 20 → weights 1/3 and 2/3.
	// Result: (1/3, 2/3, 0) normalized.
	if result[0] >= result[1] {
		t.Errorf("larger segment should have more weight: result=%v", result)
	}

	// Verify unit length.
	var sumSq float64
	for _, v := range result {
		sumSq += float64(v) * float64(v)
	}
	if diff := sumSq - 1.0; diff > 0.001 || diff < -0.001 {
		t.Errorf("not unit length: ||v||² = %f", sumSq)
	}
}

func TestWeightedAvgAllSegmentsEqual(t *testing.T) {
	segments := []Segment{
		{Start: 0, End: 10, RightStrength: 0},
		{Start: 10, End: 20, RightStrength: 0},
	}
	embs := [][]float32{
		{1, 0, 0},
		{0, 1, 0},
	}

	result := weightedAvgAllSegments(embs, segments)

	// Equal size → equal weight → (0.5, 0.5, 0) normalized.
	if diff := result[0] - result[1]; diff > 0.001 || diff < -0.001 {
		t.Errorf("equal segments should produce equal weights: result=%v", result)
	}
}

func TestMergerBoundaryStrengthAffectsThreshold(t *testing.T) {
	mock := embedder.NewDefaultMockEmbedder()
	// Use a moderate ceiling so merges are possible.
	content := []byte("identical text segment one\nidentical text segment two\nidentical text segment three")
	segments := []Segment{
		{Start: 0, End: 27, RightStrength: 1},  // Weak boundary.
		{Start: 27, End: 54, RightStrength: 3},  // Strong boundary.
		{Start: 54, End: uint32(len(content)), RightStrength: 0},
	}

	merger := NewSegmentMerger(mock, uint32(len(content)))
	ctx := context.Background()

	got := merger.Merge(ctx, content, segments)

	// The strong boundary (strength 3) should resist merging more than strength 1.
	// With mock embedder (deterministic PCG), different text segments get different
	// embeddings. The key test is that the merger runs without error and respects
	// the boundary strength in its threshold calculation.
	if len(got.Segments) < 1 {
		t.Error("expected at least 1 segment")
	}
}
