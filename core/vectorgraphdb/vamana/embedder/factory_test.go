package embedder

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewEmbedder_DefaultReturnsHybridLocal(t *testing.T) {
	ResetHardwareCache()
	ctx := context.Background()

	result, err := NewEmbedder(ctx, FactoryConfig{})

	if err != nil {
		t.Fatalf("NewEmbedder failed: %v", err)
	}

	if result.Source != "hybrid-local" {
		t.Errorf("Default should return hybrid-local, got %s", result.Source)
	}

	if result.Tier != TierHybridLocal {
		t.Errorf("Default should return TierHybridLocal, got %v", result.Tier)
	}
}

func TestNewEmbedder_ForceTier(t *testing.T) {
	ResetHardwareCache()
	ctx := context.Background()

	tier := TierHybridLocal
	result, err := NewEmbedder(ctx, FactoryConfig{
		ForceTier: &tier,
	})

	if err != nil {
		t.Fatalf("NewEmbedder failed: %v", err)
	}

	if result.Tier != TierHybridLocal {
		t.Errorf("Expected tier %v, got %v", TierHybridLocal, result.Tier)
	}

	if result.Source != "hybrid-local" {
		t.Errorf("Expected source 'hybrid-local', got %s", result.Source)
	}
}

func TestNewEmbedder_ReturnsCorrectDimension(t *testing.T) {
	ResetHardwareCache()
	ctx := context.Background()

	result, err := NewEmbedder(ctx, FactoryConfig{})

	if err != nil {
		t.Fatalf("NewEmbedder failed: %v", err)
	}

	if result.Embedder.Dimension() != EmbeddingDimension {
		t.Errorf("Expected dimension %d, got %d", EmbeddingDimension, result.Embedder.Dimension())
	}
}

func TestNewEmbedder_HardwareDetected(t *testing.T) {
	ResetHardwareCache()
	ctx := context.Background()

	result, err := NewEmbedder(ctx, FactoryConfig{})

	if err != nil {
		t.Fatalf("NewEmbedder failed: %v", err)
	}

	if result.Hardware.CPUCores <= 0 {
		t.Error("Hardware detection should report CPU cores")
	}

	if result.Hardware.SystemRAMGB <= 0 {
		t.Error("Hardware detection should report RAM")
	}
}

func TestNewLocalEmbedder(t *testing.T) {
	ResetHardwareCache()
	ctx := context.Background()

	result, err := NewLocalEmbedder(ctx)
	if err != nil {
		t.Fatalf("NewLocalEmbedder failed: %v", err)
	}

	if result.Source != "hybrid-local" {
		t.Errorf("NewLocalEmbedder should return hybrid-local, got %s", result.Source)
	}

	if result.Embedder.Dimension() != EmbeddingDimension {
		t.Errorf("Expected dimension %d, got %d", EmbeddingDimension, result.Embedder.Dimension())
	}
}

func TestNewHybridEmbedder(t *testing.T) {
	e := NewHybridEmbedder()

	if e.Dimension() != EmbeddingDimension {
		t.Errorf("Expected dimension %d, got %d", EmbeddingDimension, e.Dimension())
	}

	vec, err := e.Embed(context.Background(), "test input")
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}

	if len(vec) != EmbeddingDimension {
		t.Errorf("Expected vector length %d, got %d", EmbeddingDimension, len(vec))
	}
}

func TestFactoryResult_CanEmbed(t *testing.T) {
	ResetHardwareCache()
	ctx := context.Background()

	result, err := NewEmbedder(ctx, FactoryConfig{})

	if err != nil {
		t.Fatalf("NewEmbedder failed: %v", err)
	}

	vec, err := result.Embedder.Embed(ctx, "func TestFactoryResult_CanEmbed()")
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}

	if len(vec) != EmbeddingDimension {
		t.Errorf("Expected dimension %d, got %d", EmbeddingDimension, len(vec))
	}
}

func TestNewEmbedder_EnableHighQuality_FallsBackOnInsufficientRAM(t *testing.T) {
	ResetHardwareCache()
	ctx := context.Background()

	result, err := NewEmbedder(ctx, FactoryConfig{
		EnableHighQuality: true,
	})

	if err != nil {
		t.Fatalf("NewEmbedder failed: %v", err)
	}

	validSources := map[string]bool{
		"hybrid-local":                  true,
		"hybrid-local-fallback":         true,
		"gguf-cpu-Qwen3-Embedding-0.6B": true,
		"gguf-cpu-Qwen3-Embedding-4B":   true,
		"gguf-gpu-Qwen3-Embedding-0.6B": true,
		"gguf-gpu-Qwen3-Embedding-4B":   true,
	}

	if !validSources[result.Source] {
		t.Errorf("Unexpected source: %s", result.Source)
	}

	if result.Embedder.Dimension() != EmbeddingDimension {
		t.Errorf("Expected dimension %d, got %d", EmbeddingDimension, result.Embedder.Dimension())
	}
}

func TestNewEmbedder_VoyageWithHighQuality_ValidKey(t *testing.T) {
	ResetHardwareCache()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer valid-test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		resp := voyageResponse{
			Data:  []voyageEmbeddingData{{Embedding: make([]float32, voyageCode3Dim), Index: 0}},
			Model: string(VoyageCode3),
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	ctx := context.Background()
	result, err := NewEmbedder(ctx, FactoryConfig{
		VoyageAPIKey:      "valid-test-key",
		EnableHighQuality: true,
		voyageBaseURL:     server.URL,
	})

	if err != nil {
		t.Fatalf("NewEmbedder failed: %v", err)
	}

	if result.Source != "voyage-api" {
		t.Errorf("Expected source 'voyage-api', got %s", result.Source)
	}

	if result.Embedder.Dimension() != voyageCode3Dim {
		t.Errorf("Expected dimension %d, got %d", voyageCode3Dim, result.Embedder.Dimension())
	}
}

func TestNewEmbedder_VoyageWithHighQuality_InvalidKey_FallsBack(t *testing.T) {
	ResetHardwareCache()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(voyageErrorResponse{
			Error: struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Code    string `json:"code"`
			}{Message: "invalid api key", Type: "authentication_error"},
		})
	}))
	defer server.Close()

	ctx := context.Background()
	result, err := NewEmbedder(ctx, FactoryConfig{
		VoyageAPIKey:      "invalid-key",
		EnableHighQuality: true,
		voyageBaseURL:     server.URL,
	})

	if err != nil {
		t.Fatalf("NewEmbedder failed: %v", err)
	}

	if result.Source == "voyage-api" {
		t.Error("Should NOT use voyage-api with invalid key when EnableHighQuality is set")
	}

	validFallbackSources := map[string]bool{
		"hybrid-local":                  true,
		"hybrid-local-fallback":         true,
		"gguf-cpu-Qwen3-Embedding-0.6B": true,
		"gguf-cpu-Qwen3-Embedding-4B":   true,
		"gguf-gpu-Qwen3-Embedding-0.6B": true,
		"gguf-gpu-Qwen3-Embedding-4B":   true,
	}

	if !validFallbackSources[result.Source] {
		t.Errorf("Expected fallback source, got %s", result.Source)
	}
}

func TestNewEmbedder_VoyageWithoutHighQuality_UsesVoyageDirectly(t *testing.T) {
	ResetHardwareCache()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := voyageResponse{
			Data:  []voyageEmbeddingData{{Embedding: make([]float32, voyageCode3Dim), Index: 0}},
			Model: string(VoyageCode3),
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	ctx := context.Background()
	result, err := NewEmbedder(ctx, FactoryConfig{
		VoyageAPIKey:      "any-key",
		EnableHighQuality: false,
		voyageBaseURL:     server.URL,
	})

	if err != nil {
		t.Fatalf("NewEmbedder failed: %v", err)
	}

	if result.Source != "voyage-api" {
		t.Errorf("Expected source 'voyage-api' without EnableHighQuality, got %s", result.Source)
	}
}
