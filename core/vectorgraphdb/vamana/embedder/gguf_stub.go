//go:build !gguf

package embedder

import (
	"context"
	"errors"
)

var errGGUFNotBuilt = errors.New("GGUF support not compiled; rebuild with -tags gguf")

type GGUFConfig struct {
	Tier      ModelTier
	CacheDir  string
	UseGPU    bool
	GPULayers int
}

type GGUFEmbedder struct {
	spec     ModelSpec
	fallback Embedder
}

func NewGGUFEmbedder(_ context.Context, cfg GGUFConfig) (*GGUFEmbedder, error) {
	return nil, errGGUFNotBuilt
}

func (e *GGUFEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return nil, errGGUFNotBuilt
}

func (e *GGUFEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, errGGUFNotBuilt
}

func (e *GGUFEmbedder) Dimension() int {
	return EmbeddingDimension
}

func (e *GGUFEmbedder) MaxInputBytes() int {
	// Stub: conservative fallback based on dimension.
	return EmbeddingDimension * 4
}

func (e *GGUFEmbedder) Close() error {
	return nil
}

func (e *GGUFEmbedder) IsReady() bool {
	return false
}

func (e *GGUFEmbedder) Fallback() Embedder {
	return e.fallback
}

func (e *GGUFEmbedder) ModelPath() string {
	return ""
}

func (e *GGUFEmbedder) UsingGPU() bool {
	return false
}
