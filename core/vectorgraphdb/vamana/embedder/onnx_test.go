package embedder

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func findOrtLibrary() string {
	candidates := []string{
		"lib",
		"/usr/lib",
		"/usr/local/lib",
	}

	cwd, _ := os.Getwd()
	for {
		for _, candidate := range candidates {
			var dir string
			if filepath.IsAbs(candidate) {
				dir = candidate
			} else {
				dir = filepath.Join(cwd, candidate)
			}
			libPath := filepath.Join(dir, "libonnxruntime.so")
			if _, err := os.Stat(libPath); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(cwd)
		if parent == cwd {
			break
		}
		cwd = parent
	}

	return ""
}

func TestONNXEmbedder_GTELarge(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping ONNX test in short mode (requires model download)")
	}

	if !isORTEnabled() {
		t.Skip("ORT not enabled, run with -tags ORT")
	}

	ortLibPath := findOrtLibrary()
	if ortLibPath == "" {
		t.Skip("ONNX Runtime library not found")
	}

	tmpDir := t.TempDir()

	embedder, err := NewONNXEmbedder(ONNXConfig{
		Tier:           TierGTELarge,
		CacheDir:       tmpDir,
		UseGPU:         false,
		OrtLibraryPath: ortLibPath,
	})
	if err != nil {
		t.Fatalf("NewONNXEmbedder: %v", err)
	}
	defer embedder.Close()

	if embedder.IsReady() {
		t.Error("embedder should not be ready before EnsureModel")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	t.Log("Downloading and loading model (this may take a few minutes)...")
	if err := embedder.EnsureModel(ctx); err != nil {
		t.Fatalf("EnsureModel: %v", err)
	}

	if !embedder.IsReady() {
		t.Error("embedder should be ready after EnsureModel")
	}

	text := "core/vectorgraphdb/vamana/embedder function NewONNXEmbedder"
	vec, err := embedder.Embed(ctx, text)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	if len(vec) != EmbeddingDimension {
		t.Errorf("expected %d dimensions, got %d", EmbeddingDimension, len(vec))
	}

	var sumSq float32
	for _, v := range vec {
		sumSq += v * v
	}
	norm := float32(1.0)
	if sumSq < 0.99 || sumSq > 1.01 {
		t.Logf("Note: vector not normalized (sum of squares = %f)", sumSq)
	} else {
		t.Logf("Vector is normalized (sum of squares = %f)", sumSq)
	}
	_ = norm

	t.Logf("Embedding dimension: %d", len(vec))
	t.Logf("First 5 values: %v", vec[:5])
}

func TestONNXEmbedder_BatchInference(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping ONNX test in short mode")
	}

	if !isORTEnabled() {
		t.Skip("ORT not enabled, run with -tags ORT")
	}

	ortLibPath := findOrtLibrary()
	if ortLibPath == "" {
		t.Skip("ONNX Runtime library not found")
	}

	tmpDir := t.TempDir()

	embedder, err := NewONNXEmbedder(ONNXConfig{
		Tier:           TierGTELarge,
		CacheDir:       tmpDir,
		UseGPU:         false,
		OrtLibraryPath: ortLibPath,
	})
	if err != nil {
		t.Fatalf("NewONNXEmbedder: %v", err)
	}
	defer embedder.Close()

	ctx := context.Background()
	if err := embedder.EnsureModel(ctx); err != nil {
		t.Fatalf("EnsureModel: %v", err)
	}

	texts := []string{
		"function GetUser returns user data",
		"struct Config holds configuration",
		"interface Repository defines database operations",
	}

	results, err := embedder.EmbedBatch(ctx, texts)
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}

	if len(results) != len(texts) {
		t.Errorf("expected %d embeddings, got %d", len(texts), len(results))
	}

	for i, vec := range results {
		if len(vec) != EmbeddingDimension {
			t.Errorf("embedding %d: expected %d dimensions, got %d", i, EmbeddingDimension, len(vec))
		}
	}

	t.Logf("Successfully embedded %d texts", len(texts))
}

func TestONNXEmbedder_FallbackWhenNotLoaded(t *testing.T) {
	tmpDir := t.TempDir()

	embedder, err := NewONNXEmbedder(ONNXConfig{
		Tier:     TierGTELarge,
		CacheDir: tmpDir,
		UseGPU:   false,
	})
	if err != nil {
		t.Fatalf("NewONNXEmbedder: %v", err)
	}
	defer embedder.Close()

	ctx := context.Background()
	vec, err := embedder.Embed(ctx, "test text")
	if err != nil {
		t.Fatalf("Embed (fallback): %v", err)
	}

	if len(vec) != EmbeddingDimension {
		t.Errorf("fallback should produce %d dimensions, got %d", EmbeddingDimension, len(vec))
	}

	t.Log("Fallback embedder works when model not loaded")
}

func TestONNXEmbedder_Dimension(t *testing.T) {
	tmpDir := t.TempDir()

	embedder, err := NewONNXEmbedder(ONNXConfig{
		Tier:     TierGTELarge,
		CacheDir: tmpDir,
	})
	if err != nil {
		t.Fatalf("NewONNXEmbedder: %v", err)
	}
	defer embedder.Close()

	if embedder.Dimension() != EmbeddingDimension {
		t.Errorf("expected dimension %d, got %d", EmbeddingDimension, embedder.Dimension())
	}
}
