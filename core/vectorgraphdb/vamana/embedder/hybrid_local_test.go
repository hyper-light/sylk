package embedder

import (
	"context"
	"math"
	"testing"
)

func TestHybridLocalEmbedder_Dimension(t *testing.T) {
	e := NewHybridLocalEmbedder()

	if e.Dimension() != EmbeddingDimension {
		t.Errorf("Expected dimension %d, got %d", EmbeddingDimension, e.Dimension())
	}
}

func TestHybridLocalEmbedder_Embed(t *testing.T) {
	e := NewHybridLocalEmbedder()
	ctx := context.Background()

	vec, err := e.Embed(ctx, "func TestHybridLocalEmbedder")
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}

	if len(vec) != EmbeddingDimension {
		t.Errorf("Expected dimension %d, got %d", EmbeddingDimension, len(vec))
	}

	var norm float64
	for _, v := range vec {
		norm += float64(v * v)
	}
	norm = math.Sqrt(norm)

	if math.Abs(norm-1.0) > 0.001 {
		t.Errorf("Expected unit vector, got norm %f", norm)
	}
}

func TestHybridLocalEmbedder_Deterministic(t *testing.T) {
	e := NewHybridLocalEmbedder()
	ctx := context.Background()
	text := "func TestHybridLocalEmbedder_Deterministic"

	v1, _ := e.Embed(ctx, text)
	v2, _ := e.Embed(ctx, text)

	for i := range v1 {
		if v1[i] != v2[i] {
			t.Errorf("Embeddings differ at index %d: %f != %f", i, v1[i], v2[i])
			break
		}
	}
}

func TestHybridLocalEmbedder_SimilarTexts(t *testing.T) {
	e := NewHybridLocalEmbedder()
	ctx := context.Background()

	v1, _ := e.Embed(ctx, "func processUserRequest")
	v2, _ := e.Embed(ctx, "func processUserResponse")
	v3, _ := e.Embed(ctx, "type DatabaseConnection")

	sim12 := cosineSimilarity(v1, v2)
	sim13 := cosineSimilarity(v1, v3)

	if sim12 <= sim13 {
		t.Errorf("Similar functions should have higher similarity: processRequest-processResponse=%f, processRequest-DatabaseConnection=%f",
			sim12, sim13)
	}
}

func TestHybridLocalEmbedder_EmbedBatch(t *testing.T) {
	e := NewHybridLocalEmbedder()
	ctx := context.Background()

	texts := []string{
		"func Foo()",
		"type Bar struct",
		"const MaxSize = 100",
	}

	results, err := e.EmbedBatch(ctx, texts)
	if err != nil {
		t.Fatalf("EmbedBatch failed: %v", err)
	}

	if len(results) != len(texts) {
		t.Errorf("Expected %d results, got %d", len(texts), len(results))
	}

	for i, vec := range results {
		if len(vec) != EmbeddingDimension {
			t.Errorf("Result %d has wrong dimension: %d", i, len(vec))
		}
	}
}

func TestHybridLocalEmbedder_EmptyInput(t *testing.T) {
	e := NewHybridLocalEmbedder()
	ctx := context.Background()

	vec, err := e.Embed(ctx, "")
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}

	if len(vec) != EmbeddingDimension {
		t.Errorf("Expected dimension %d, got %d", EmbeddingDimension, len(vec))
	}
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"hello world", []string{"hello", "world"}},
		{"func_name", []string{"func_name"}},
		{"camelCase", []string{"camelcase"}},
		{"a b", []string{}},
		{"", []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			tokens := tokenize(tt.input)
			if len(tokens) != len(tt.expected) {
				t.Errorf("Expected %d tokens, got %d: %v", len(tt.expected), len(tokens), tokens)
			}
		})
	}
}

func TestExtractNgrams(t *testing.T) {
	tests := []struct {
		input    string
		n        int
		expected int
	}{
		{"hello", 3, 3},
		{"hi", 3, 0},
		{"abc", 3, 1},
		{"", 3, 0},
	}

	for _, tt := range tests {
		ngrams := extractNgrams(tt.input, tt.n)
		if len(ngrams) != tt.expected {
			t.Errorf("extractNgrams(%q, %d): expected %d, got %d", tt.input, tt.n, tt.expected, len(ngrams))
		}
	}
}

func TestHybridLocalEmbedder_ImplementsInterface(t *testing.T) {
	var _ Embedder = (*HybridLocalEmbedder)(nil)
}

func cosineSimilarity(a, b []float32) float64 {
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i] * b[i])
		normA += float64(a[i] * a[i])
		normB += float64(b[i] * b[i])
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
