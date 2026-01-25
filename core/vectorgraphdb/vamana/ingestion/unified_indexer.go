package ingestion

import (
	"context"
	"fmt"
	"time"

	mainIngestion "github.com/adalundhe/sylk/core/vectorgraphdb/ingestion"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/embedder"
)

type UnifiedIndexerConfig struct {
	RootPath       string
	IgnorePatterns []string
	SQLitePath     string
	BlevePath      string
	VectorDir      string
	Workers        int
	SkipPersist    bool
	SkipBleve      bool
	SkipVector     bool
	EmbedderConfig embedder.FactoryConfig
}

type UnifiedIndexerResult struct {
	CodeResult   *mainIngestion.IngestionResult
	VectorResult *VectorPipelineResult
	TotalTime    time.Duration
}

type UnifiedIndexer struct {
	config UnifiedIndexerConfig
}

func NewUnifiedIndexer(config UnifiedIndexerConfig) *UnifiedIndexer {
	return &UnifiedIndexer{config: config}
}

func (u *UnifiedIndexer) Index(ctx context.Context) (*UnifiedIndexerResult, error) {
	start := time.Now()
	result := &UnifiedIndexerResult{}

	codeConfig := &mainIngestion.Config{
		RootPath:       u.config.RootPath,
		IgnorePatterns: u.config.IgnorePatterns,
		SQLitePath:     u.config.SQLitePath,
		BlevePath:      u.config.BlevePath,
		Workers:        u.config.Workers,
		SkipPersist:    u.config.SkipPersist,
		SkipBleve:      u.config.SkipBleve,
	}

	codeResult, err := mainIngestion.IngestCodebase(ctx, codeConfig)
	if err != nil {
		return nil, fmt.Errorf("code ingestion: %w", err)
	}
	result.CodeResult = codeResult

	if !u.config.SkipVector && u.config.VectorDir != "" && codeResult.Graph != nil {
		vectorResult, err := u.runVectorPipeline(ctx, codeResult.Graph)
		if err != nil {
			return nil, fmt.Errorf("vector pipeline: %w", err)
		}
		result.VectorResult = vectorResult
	}

	result.TotalTime = time.Since(start)
	return result, nil
}

func (u *UnifiedIndexer) runVectorPipeline(ctx context.Context, graph *mainIngestion.CodeGraph) (*VectorPipelineResult, error) {
	pipelineConfig := VectorPipelineConfig{
		StorageDir:     u.config.VectorDir,
		EmbedderConfig: u.config.EmbedderConfig,
		BatchSize:      64,
		Workers:        1,
	}

	pipeline, err := NewVectorIngestionPipeline(ctx, pipelineConfig)
	if err != nil {
		return nil, err
	}
	defer pipeline.Close()

	return pipeline.IngestCodeGraph(ctx, graph)
}

func IndexWithVectors(ctx context.Context, rootPath, vectorDir string) (*UnifiedIndexerResult, error) {
	indexer := NewUnifiedIndexer(UnifiedIndexerConfig{
		RootPath:    rootPath,
		VectorDir:   vectorDir,
		SkipPersist: true,
		EmbedderConfig: embedder.FactoryConfig{
			ForceLocal:    true,
			SkipModelLoad: true,
		},
	})
	return indexer.Index(ctx)
}
