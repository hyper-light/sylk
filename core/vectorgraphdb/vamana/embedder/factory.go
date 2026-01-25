package embedder

import (
	"context"
	"os"
	"path/filepath"
)

type FactoryConfig struct {
	CacheDir         string
	ForceLocal       bool
	ForceTier        *ModelTier
	VoyageAPIKey     string
	SkipModelLoad    bool
	OrtLibraryPath   string
	AutoDownloadLibs bool
	LibraryManager   *LibraryManager
}

type FactoryResult struct {
	Embedder Embedder
	Tier     ModelTier
	Source   string
	Hardware HardwareCapabilities
}

func NewEmbedder(ctx context.Context, cfg FactoryConfig) (*FactoryResult, error) {
	hardware := DetectHardware()

	if !cfg.ForceLocal {
		apiKey := cfg.VoyageAPIKey
		if apiKey == "" {
			apiKey = os.Getenv("VOYAGE_API_KEY")
		}

		if apiKey != "" {
			voyageCfg := VoyageConfig{
				APIKey: apiKey,
				Model:  VoyageCode3,
			}

			embedder, err := NewVoyageEmbedder(voyageCfg)
			if err == nil {
				return &FactoryResult{
					Embedder: embedder,
					Tier:     TierQwen3,
					Source:   "voyage-api",
					Hardware: hardware,
				}, nil
			}
		}
	}

	var tier ModelTier
	if cfg.ForceTier != nil {
		tier = *cfg.ForceTier
	} else {
		tier = hardware.SelectModelTier()
	}

	switch tier {
	case TierHybridLocal:
		return &FactoryResult{
			Embedder: NewHybridLocalEmbedder(),
			Tier:     TierHybridLocal,
			Source:   "hybrid-local",
			Hardware: hardware,
		}, nil

	case TierGTELarge, TierQwen3:
		ortPath := cfg.OrtLibraryPath
		if ortPath == "" {
			ortPath = findOrtLibraryPath()
		}

		if ortPath == "" && cfg.AutoDownloadLibs {
			libMgr := cfg.LibraryManager
			if libMgr == nil {
				var err error
				libMgr, err = NewLibraryManager()
				if err != nil {
					return &FactoryResult{
						Embedder: NewHybridLocalEmbedder(),
						Tier:     TierHybridLocal,
						Source:   "hybrid-local-fallback",
						Hardware: hardware,
					}, nil
				}
			}

			if _, err := libMgr.EnsureLibraries(); err != nil {
				return &FactoryResult{
					Embedder: NewHybridLocalEmbedder(),
					Tier:     TierHybridLocal,
					Source:   "hybrid-local-fallback",
					Hardware: hardware,
				}, nil
			}

			ortPath = libMgr.LibDir()
		}

		onnxCfg := ONNXConfig{
			Tier:           tier,
			CacheDir:       cfg.CacheDir,
			UseGPU:         hardware.HasNVIDIAGPU && hardware.VRAMGB >= tier.Spec().MinVRAMGB,
			OrtLibraryPath: ortPath,
		}

		embedder, err := NewONNXEmbedder(onnxCfg)
		if err != nil {
			return &FactoryResult{
				Embedder: NewHybridLocalEmbedder(),
				Tier:     TierHybridLocal,
				Source:   "hybrid-local-fallback",
				Hardware: hardware,
			}, nil
		}

		if !cfg.SkipModelLoad {
			if err := embedder.EnsureModel(ctx); err != nil {
				return &FactoryResult{
					Embedder: embedder.Fallback(),
					Tier:     TierHybridLocal,
					Source:   "hybrid-local-fallback",
					Hardware: hardware,
				}, nil
			}
		}

		source := "onnx-" + tier.String()
		if embedder.IsReady() {
			return &FactoryResult{
				Embedder: embedder,
				Tier:     tier,
				Source:   source,
				Hardware: hardware,
			}, nil
		}

		return &FactoryResult{
			Embedder: embedder.Fallback(),
			Tier:     TierHybridLocal,
			Source:   "hybrid-local-fallback",
			Hardware: hardware,
		}, nil

	default:
		return &FactoryResult{
			Embedder: NewHybridLocalEmbedder(),
			Tier:     TierHybridLocal,
			Source:   "hybrid-local",
			Hardware: hardware,
		}, nil
	}
}

func NewLocalEmbedder(ctx context.Context) (*FactoryResult, error) {
	cfg := FactoryConfig{ForceLocal: true}
	return NewEmbedder(ctx, cfg)
}

func NewHybridEmbedder() Embedder {
	return NewHybridLocalEmbedder()
}

func findOrtLibraryPath() string {
	candidates := []string{
		"/usr/lib",
		"/usr/local/lib",
		"/usr/lib/x86_64-linux-gnu",
	}

	home, err := os.UserHomeDir()
	if err == nil {
		candidates = append(candidates, filepath.Join(home, ".sylk", "lib"))
	}

	cwd, err := os.Getwd()
	if err == nil {
		candidates = append(candidates, filepath.Join(cwd, "lib"))
		for {
			parent := filepath.Dir(cwd)
			if parent == cwd {
				break
			}
			cwd = parent
			candidates = append(candidates, filepath.Join(cwd, "lib"))
		}
	}

	for _, dir := range candidates {
		libPath := filepath.Join(dir, "libonnxruntime.so")
		if _, err := os.Stat(libPath); err == nil {
			return dir
		}
	}

	return ""
}
