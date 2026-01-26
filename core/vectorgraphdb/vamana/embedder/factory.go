package embedder

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/adalundhe/sylk/core/llm"
)

type FactoryConfig struct {
	CacheDir          string
	EnableHighQuality bool
	ForceTier         *ModelTier
	VoyageAPIKey      string

	// voyageBaseURL is used for testing to inject a mock server URL.
	// Empty string uses the default Voyage API URL.
	voyageBaseURL string
}

type FactoryResult struct {
	Embedder Embedder
	Tier     ModelTier
	Source   string
	Hardware HardwareCapabilities
	UseGPU   bool
}

func NewEmbedder(ctx context.Context, cfg FactoryConfig) (*FactoryResult, error) {
	hardware := DetectHardware()

	if cfg.CacheDir == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			cfg.CacheDir = filepath.Join(home, ".sylk", "models")
		}
	}

	if cfg.VoyageAPIKey == "" {
		if key, err := llm.ResolveAPIKey("voyage"); err == nil {
			cfg.VoyageAPIKey = key
		}
	}

	if cfg.ForceTier != nil {
		return createEmbedderForTier(ctx, *cfg.ForceTier, cfg, hardware)
	}

	if cfg.VoyageAPIKey != "" && cfg.EnableHighQuality {
		if err := verifyVoyageAPIKeyWithURL(ctx, cfg.VoyageAPIKey, cfg.voyageBaseURL); err == nil {
			return createVoyageEmbedderWithURL(cfg.VoyageAPIKey, cfg.voyageBaseURL, hardware)
		}
		slog.Warn("voyage API key verification failed, falling back to local embedder",
			"error", "verification_failed")
		tier := hardware.SelectHighQualityTier()
		return createEmbedderForTier(ctx, tier, cfg, hardware)
	}

	if cfg.VoyageAPIKey != "" {
		return createVoyageEmbedderWithURL(cfg.VoyageAPIKey, cfg.voyageBaseURL, hardware)
	}

	if cfg.EnableHighQuality {
		tier := hardware.SelectHighQualityTier()
		return createEmbedderForTier(ctx, tier, cfg, hardware)
	}

	return &FactoryResult{
		Embedder: NewEnhancedHybridEmbedder(),
		Tier:     TierHybridLocal,
		Source:   "hybrid-local",
		Hardware: hardware,
		UseGPU:   false,
	}, nil
}

func createVoyageEmbedderWithURL(apiKey, baseURL string, hardware HardwareCapabilities) (*FactoryResult, error) {
	voyageCfg := VoyageConfig{
		APIKey:  apiKey,
		Model:   VoyageCode3,
		BaseURL: baseURL,
	}
	embedder, err := NewVoyageEmbedder(voyageCfg)
	if err != nil {
		return nil, err
	}
	return &FactoryResult{
		Embedder: embedder,
		Tier:     TierQwen3_4B,
		Source:   "voyage-api",
		Hardware: hardware,
		UseGPU:   false,
	}, nil
}

func createEmbedderForTier(ctx context.Context, tier ModelTier, cfg FactoryConfig, hardware HardwareCapabilities) (*FactoryResult, error) {
	if tier == TierHybridLocal {
		return &FactoryResult{
			Embedder: NewEnhancedHybridEmbedder(),
			Tier:     TierHybridLocal,
			Source:   "hybrid-local",
			Hardware: hardware,
			UseGPU:   false,
		}, nil
	}

	if !hardware.CanRunTier(tier) {
		if tier == TierQwen3_4B {
			tier = TierQwen3_0_6B
			if !hardware.CanRunTier(tier) {
				return &FactoryResult{
					Embedder: NewEnhancedHybridEmbedder(),
					Tier:     TierHybridLocal,
					Source:   "hybrid-local-fallback",
					Hardware: hardware,
					UseGPU:   false,
				}, nil
			}
		} else {
			return &FactoryResult{
				Embedder: NewEnhancedHybridEmbedder(),
				Tier:     TierHybridLocal,
				Source:   "hybrid-local-fallback",
				Hardware: hardware,
				UseGPU:   false,
			}, nil
		}
	}

	useGPU := hardware.CanUseGPU(tier)

	ggufCfg := GGUFConfig{
		Tier:     tier,
		CacheDir: cfg.CacheDir,
		UseGPU:   useGPU,
	}

	embedder, err := NewGGUFEmbedder(ctx, ggufCfg)
	if err != nil {
		return &FactoryResult{
			Embedder: NewEnhancedHybridEmbedder(),
			Tier:     TierHybridLocal,
			Source:   "hybrid-local-fallback",
			Hardware: hardware,
			UseGPU:   false,
		}, nil
	}

	source := "gguf-cpu-" + tier.String()
	if useGPU {
		source = "gguf-gpu-" + tier.String()
	}

	return &FactoryResult{
		Embedder: embedder,
		Tier:     tier,
		Source:   source,
		Hardware: hardware,
		UseGPU:   useGPU,
	}, nil
}

func NewLocalEmbedder(ctx context.Context) (*FactoryResult, error) {
	cfg := FactoryConfig{EnableHighQuality: false}
	return NewEmbedder(ctx, cfg)
}

func NewHighQualityEmbedder(ctx context.Context) (*FactoryResult, error) {
	cfg := FactoryConfig{EnableHighQuality: true}
	return NewEmbedder(ctx, cfg)
}

// NewHybridEmbedder returns the enhanced hybrid embedder (default).
func NewHybridEmbedder() Embedder {
	return NewEnhancedHybridEmbedder()
}
