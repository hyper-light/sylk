//go:build gguf

package embedder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	llama "github.com/go-skynet/go-llama.cpp"
)

type GGUFConfig struct {
	Tier      ModelTier
	CacheDir  string
	UseGPU    bool
	GPULayers int
	Threads   int
}

type GGUFEmbedder struct {
	model     *llama.LLama
	spec      ModelSpec
	modelPath string
	useGPU    bool
	threads   int
	mu        sync.RWMutex
	fallback  Embedder
}

func NewGGUFEmbedder(ctx context.Context, cfg GGUFConfig) (*GGUFEmbedder, error) {
	spec := cfg.Tier.Spec()
	if spec.Format != FormatGGUF {
		return nil, fmt.Errorf("tier %s does not use GGUF format", cfg.Tier)
	}

	if cfg.CacheDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}
		cfg.CacheDir = filepath.Join(home, ".sylk", "models")
	}

	manager := NewGGUFManager(cfg.CacheDir)
	modelPath, err := manager.EnsureModel(ctx, cfg.Tier)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure model: %w", err)
	}

	threads := cfg.Threads
	if threads == 0 {
		threads = runtime.NumCPU()
		if threads > 8 {
			threads = 8
		}
	}

	gpuLayers := 0
	if cfg.UseGPU {
		gpuLayers = cfg.GPULayers
		if gpuLayers == 0 {
			gpuLayers = 99
		}
	}

	opts := []llama.ModelOption{
		llama.SetContext(2048),
		llama.EnableEmbeddings,
		llama.SetGPULayers(gpuLayers),
	}

	model, err := llama.New(modelPath, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load model: %w", err)
	}

	return &GGUFEmbedder{
		model:     model,
		spec:      spec,
		modelPath: modelPath,
		useGPU:    cfg.UseGPU,
		threads:   threads,
		fallback:  NewEnhancedHybridEmbedder(),
	}, nil
}

func (e *GGUFEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.model == nil {
		return e.fallback.Embed(ctx, text)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	embedding, err := e.model.Embeddings(text, llama.SetThreads(e.threads))
	if err != nil {
		return nil, fmt.Errorf("embedding failed: %w", err)
	}

	if len(embedding) > e.spec.Dimension {
		embedding = embedding[:e.spec.Dimension]
	}

	return embedding, nil
}

func (e *GGUFEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		embedding, err := e.Embed(ctx, text)
		if err != nil {
			return nil, fmt.Errorf("batch embedding failed at index %d: %w", i, err)
		}
		results[i] = embedding
	}
	return results, nil
}

func (e *GGUFEmbedder) Dimension() int {
	return e.spec.Dimension
}

func (e *GGUFEmbedder) MaxInputBytes() int {
	return e.spec.MaxInputTokens * e.spec.BytesPerToken
}

func (e *GGUFEmbedder) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.model != nil {
		e.model.Free()
		e.model = nil
	}
	return nil
}

func (e *GGUFEmbedder) IsReady() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.model != nil
}

func (e *GGUFEmbedder) Fallback() Embedder {
	return e.fallback
}

func (e *GGUFEmbedder) ModelPath() string {
	return e.modelPath
}

func (e *GGUFEmbedder) UsingGPU() bool {
	return e.useGPU
}
