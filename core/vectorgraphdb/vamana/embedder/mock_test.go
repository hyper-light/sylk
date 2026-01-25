package embedder

import (
	"context"
	"testing"
)

func TestMockEmbedder_Deterministic(t *testing.T) {
	e := NewMockEmbedder(EmbeddingDimension)

	text := "func TestMockEmbedder_Deterministic"
	v1, err := e.Embed(context.Background(), text)
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}
	v2, err := e.Embed(context.Background(), text)
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}

	if len(v1) != EmbeddingDimension {
		t.Errorf("Expected dimension %d, got %d", EmbeddingDimension, len(v1))
	}

	for i := range v1 {
		if v1[i] != v2[i] {
			t.Errorf("Embeddings differ at index %d: %f != %f", i, v1[i], v2[i])
			break
		}
	}
}

func TestMockEmbedder_DifferentInputs(t *testing.T) {
	e := NewMockEmbedder(EmbeddingDimension)

	v1, _ := e.Embed(context.Background(), "hello")
	v2, _ := e.Embed(context.Background(), "world")

	same := true
	for i := range v1 {
		if v1[i] != v2[i] {
			same = false
			break
		}
	}

	if same {
		t.Error("Different inputs should produce different embeddings")
	}
}

func TestMockEmbedder_EmbedBatch(t *testing.T) {
	e := NewMockEmbedder(EmbeddingDimension)

	texts := []string{"func Foo", "type Bar", "const Baz"}
	results, err := e.EmbedBatch(context.Background(), texts)
	if err != nil {
		t.Fatalf("EmbedBatch failed: %v", err)
	}

	if len(results) != len(texts) {
		t.Errorf("Expected %d results, got %d", len(texts), len(results))
	}

	for i, r := range results {
		if len(r) != EmbeddingDimension {
			t.Errorf("Result %d has wrong dimension: %d", i, len(r))
		}
	}
}

func TestMockEmbedder_ImplementsInterface(t *testing.T) {
	var _ Embedder = (*MockEmbedder)(nil)
}
