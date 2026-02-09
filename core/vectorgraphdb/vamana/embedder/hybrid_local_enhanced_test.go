package embedder

import (
	"context"
	"math"
	"strings"
	"testing"
)

func TestEnhancedHybridEmbedder_Dimension(t *testing.T) {
	e := NewEnhancedHybridEmbedder()
	if got := e.Dimension(); got != EmbeddingDimension {
		t.Errorf("Dimension() = %d, want %d", got, EmbeddingDimension)
	}
}

func TestEnhancedHybridEmbedder_Embed(t *testing.T) {
	e := NewEnhancedHybridEmbedder()
	ctx := context.Background()

	vec, err := e.Embed(ctx, "hello world")
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}

	if len(vec) != EmbeddingDimension {
		t.Errorf("len(vec) = %d, want %d", len(vec), EmbeddingDimension)
	}

	// Verify normalization
	var mag float64
	for _, v := range vec {
		mag += float64(v * v)
	}
	if math.Abs(mag-1.0) > 1e-5 {
		t.Errorf("vector magnitude = %f, want 1.0", math.Sqrt(mag))
	}
}

func TestEnhancedHybridEmbedder_Determinism(t *testing.T) {
	e := NewEnhancedHybridEmbedder()
	ctx := context.Background()

	text := "The quick brown fox jumps over the lazy dog"

	vec1, _ := e.Embed(ctx, text)
	vec2, _ := e.Embed(ctx, text)

	for i := range vec1 {
		if vec1[i] != vec2[i] {
			t.Errorf("non-deterministic: vec1[%d]=%f, vec2[%d]=%f", i, vec1[i], i, vec2[i])
			break
		}
	}
}

func TestEnhancedHybridEmbedder_EmbedBatch(t *testing.T) {
	e := NewEnhancedHybridEmbedder()
	ctx := context.Background()

	texts := []string{
		"hello world",
		"foo bar baz",
		"the quick brown fox",
	}

	vecs, err := e.EmbedBatch(ctx, texts)
	if err != nil {
		t.Fatalf("EmbedBatch() error = %v", err)
	}

	if len(vecs) != len(texts) {
		t.Fatalf("len(vecs) = %d, want %d", len(vecs), len(texts))
	}

	for i, vec := range vecs {
		if len(vec) != EmbeddingDimension {
			t.Errorf("len(vecs[%d]) = %d, want %d", i, len(vec), EmbeddingDimension)
		}
	}
}

func TestEnhancedHybridEmbedder_EmptyText(t *testing.T) {
	e := NewEnhancedHybridEmbedder()
	ctx := context.Background()

	vec, err := e.Embed(ctx, "")
	if err != nil {
		t.Fatalf("Embed(\"\") error = %v", err)
	}

	if len(vec) != EmbeddingDimension {
		t.Errorf("len(vec) = %d, want %d", len(vec), EmbeddingDimension)
	}
}

func TestEnhancedHybridEmbedder_SimilarTexts(t *testing.T) {
	e := NewEnhancedHybridEmbedder()
	ctx := context.Background()

	// These texts should be similar
	text1 := "The cat sat on the mat"
	text2 := "A cat was sitting on the mat"
	text3 := "Quantum physics explains particle behavior"

	vec1, _ := e.Embed(ctx, text1)
	vec2, _ := e.Embed(ctx, text2)
	vec3, _ := e.Embed(ctx, text3)

	sim12 := cosineSim(vec1, vec2)
	sim13 := cosineSim(vec1, vec3)

	// Similar texts should have higher similarity than unrelated texts
	if sim12 <= sim13 {
		t.Errorf("similar texts sim=%f should be greater than unrelated sim=%f", sim12, sim13)
	}
}

func TestEnhancedHybridEmbedder_CodeSimilarity(t *testing.T) {
	e := NewEnhancedHybridEmbedder()
	ctx := context.Background()

	// Code snippets with similar functionality
	code1 := "func add(a, b int) int { return a + b }"
	code2 := "func sum(x, y int) int { return x + y }"
	code3 := "SELECT * FROM users WHERE id = 1"

	vec1, _ := e.Embed(ctx, code1)
	vec2, _ := e.Embed(ctx, code2)
	vec3, _ := e.Embed(ctx, code3)

	sim12 := cosineSim(vec1, vec2)
	sim13 := cosineSim(vec1, vec3)

	// Similar code should have higher similarity than SQL
	if sim12 <= sim13 {
		t.Errorf("similar code sim=%f should be greater than unrelated sim=%f", sim12, sim13)
	}
}

func TestEnhancedHybridEmbedder_PositionalSensitivity(t *testing.T) {
	e := NewEnhancedHybridEmbedder()
	ctx := context.Background()

	// Word order matters
	text1 := "dog bites man"
	text2 := "man bites dog"
	text3 := "dog bites man" // Same as text1

	vec1, _ := e.Embed(ctx, text1)
	vec2, _ := e.Embed(ctx, text2)
	vec3, _ := e.Embed(ctx, text3)

	sim12 := cosineSim(vec1, vec2)
	sim13 := cosineSim(vec1, vec3)

	// Identical texts should be more similar than reordered texts
	if sim13 <= sim12 {
		t.Errorf("identical texts sim=%f should be greater than reordered sim=%f", sim13, sim12)
	}
}

func TestEnhancedHybridEmbedder_LongText(t *testing.T) {
	e := NewEnhancedHybridEmbedder()
	ctx := context.Background()

	// Generate long text
	longText := strings.Repeat("This is a test sentence with multiple words. ", 100)

	vec, err := e.Embed(ctx, longText)
	if err != nil {
		t.Fatalf("Embed(longText) error = %v", err)
	}

	if len(vec) != EmbeddingDimension {
		t.Errorf("len(vec) = %d, want %d", len(vec), EmbeddingDimension)
	}

	// Verify normalization
	var mag float64
	for _, v := range vec {
		mag += float64(v * v)
	}
	if math.Abs(mag-1.0) > 1e-5 {
		t.Errorf("vector magnitude = %f, want 1.0", math.Sqrt(mag))
	}
}

func TestEnhancedHybridEmbedder_SpecialCharacters(t *testing.T) {
	e := NewEnhancedHybridEmbedder()
	ctx := context.Background()

	texts := []string{
		"hello_world",
		"CamelCase",
		"ALLCAPS",
		"123numbers456",
		"special!@#$%chars",
		"emoji🚀test",
		"日本語テスト",
	}

	for _, text := range texts {
		vec, err := e.Embed(ctx, text)
		if err != nil {
			t.Errorf("Embed(%q) error = %v", text, err)
			continue
		}
		if len(vec) != EmbeddingDimension {
			t.Errorf("Embed(%q) len = %d, want %d", text, len(vec), EmbeddingDimension)
		}
	}
}

func TestEnhancedHybridEmbedder_BatchContextCancellation(t *testing.T) {
	e := NewEnhancedHybridEmbedder()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	texts := make([]string, 100)
	for i := range texts {
		texts[i] = "test text"
	}

	_, err := e.EmbedBatch(ctx, texts)
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func cosineSim(a, b []float32) float64 {
	var dot, magA, magB float64
	for i := range a {
		dot += float64(a[i] * b[i])
		magA += float64(a[i] * a[i])
		magB += float64(b[i] * b[i])
	}
	if magA == 0 || magB == 0 {
		return 0
	}
	return dot / (math.Sqrt(magA) * math.Sqrt(magB))
}

func BenchmarkEnhancedHybridEmbedder_Embed(b *testing.B) {
	e := NewEnhancedHybridEmbedder()
	ctx := context.Background()
	text := "The quick brown fox jumps over the lazy dog"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = e.Embed(ctx, text)
	}
}

func BenchmarkEnhancedHybridEmbedder_EmbedLong(b *testing.B) {
	e := NewEnhancedHybridEmbedder()
	ctx := context.Background()
	text := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 50)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = e.Embed(ctx, text)
	}
}

func BenchmarkEnhancedHybridEmbedder_EmbedBatch(b *testing.B) {
	e := NewEnhancedHybridEmbedder()
	ctx := context.Background()

	texts := make([]string, 100)
	for i := range texts {
		texts[i] = "The quick brown fox jumps over the lazy dog"
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = e.EmbedBatch(ctx, texts)
	}
}

