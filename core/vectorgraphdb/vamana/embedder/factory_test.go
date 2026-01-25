package embedder

import (
	"context"
	"testing"
)

func TestNewEmbedder_ForcesLocal(t *testing.T) {
	ResetHardwareCache()
	ctx := context.Background()

	result, err := NewEmbedder(ctx, FactoryConfig{
		ForceLocal:    true,
		SkipModelLoad: true,
	})

	if err != nil {
		t.Fatalf("NewEmbedder failed: %v", err)
	}

	if result.Source == "voyage-api" {
		t.Error("ForceLocal should prevent Voyage API usage")
	}
}

func TestNewEmbedder_ForceTier(t *testing.T) {
	ResetHardwareCache()
	ctx := context.Background()

	tier := TierHybridLocal
	result, err := NewEmbedder(ctx, FactoryConfig{
		ForceLocal:    true,
		ForceTier:     &tier,
		SkipModelLoad: true,
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

	result, err := NewEmbedder(ctx, FactoryConfig{
		ForceLocal:    true,
		SkipModelLoad: true,
	})

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

	result, err := NewEmbedder(ctx, FactoryConfig{
		ForceLocal:    true,
		SkipModelLoad: true,
	})

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

	if result.Source == "voyage-api" {
		t.Error("NewLocalEmbedder should not use Voyage API")
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

	result, err := NewEmbedder(ctx, FactoryConfig{
		ForceLocal:    true,
		SkipModelLoad: true,
	})

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
