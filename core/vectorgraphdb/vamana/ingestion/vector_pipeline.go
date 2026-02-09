package ingestion

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
	mainIngestion "github.com/adalundhe/sylk/core/vectorgraphdb/ingestion"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/embedder"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
)

type VectorPipelineConfig struct {
	StorageDir     string
	EmbedderConfig embedder.FactoryConfig
	BatchSize      int
	Workers        int
	OnProgress     func(phase string, current, total int64)
}

type VectorPipelineResult struct {
	SymbolCount    int
	EmbedderSource string
	EmbedderTier   embedder.ModelTier
	Duration       time.Duration
	SerializeTime  time.Duration
	EmbedTime      time.Duration
	WriteTime      time.Duration
	DiskSizeBytes  int64
}

type VectorIngestionPipeline struct {
	config     VectorPipelineConfig
	embedder   embedder.Embedder
	source     string
	tier       embedder.ModelTier
	serializer *embedder.Serializer
	progress   atomic.Int64
	totalItems atomic.Int64
}

func NewVectorIngestionPipeline(ctx context.Context, config VectorPipelineConfig) (*VectorIngestionPipeline, error) {
	if config.StorageDir == "" {
		return nil, fmt.Errorf("storage directory is required")
	}

	if config.BatchSize <= 0 {
		config.BatchSize = 64
	}

	if config.Workers <= 0 {
		config.Workers = 1
	}

	result, err := embedder.NewEmbedder(ctx, config.EmbedderConfig)
	if err != nil {
		return nil, fmt.Errorf("create embedder: %w", err)
	}

	return &VectorIngestionPipeline{
		config:     config,
		embedder:   result.Embedder,
		source:     result.Source,
		tier:       result.Tier,
		serializer: embedder.NewSerializer(),
	}, nil
}

func (p *VectorIngestionPipeline) IngestCodeGraph(ctx context.Context, graph *mainIngestion.CodeGraph) (*VectorPipelineResult, error) {
	start := time.Now()
	result := &VectorPipelineResult{
		EmbedderSource: p.source,
		EmbedderTier:   p.tier,
	}

	for i := range graph.Files {
		p.serializer.RegisterFile(&graph.Files[i])
	}

	serializeStart := time.Now()
	texts := p.serializeSymbols(graph.Symbols)
	result.SerializeTime = time.Since(serializeStart)
	result.SymbolCount = len(texts)

	if len(texts) == 0 {
		result.Duration = time.Since(start)
		return result, nil
	}

	p.totalItems.Store(int64(len(texts)))
	p.reportProgress("embedding", 0, int64(len(texts)))

	embedStart := time.Now()
	embeddings, err := p.computeEmbeddings(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("compute embeddings: %w", err)
	}
	result.EmbedTime = time.Since(embedStart)

	p.reportProgress("writing", 0, int64(len(embeddings)))

	writeStart := time.Now()
	diskSize, err := p.writeToStorage(ctx, graph.Symbols, embeddings)
	if err != nil {
		return nil, fmt.Errorf("write to storage: %w", err)
	}
	result.WriteTime = time.Since(writeStart)
	result.DiskSizeBytes = diskSize

	result.Duration = time.Since(start)
	p.reportProgress("complete", int64(len(embeddings)), int64(len(embeddings)))

	return result, nil
}

func (p *VectorIngestionPipeline) serializeSymbols(symbols []mainIngestion.SymbolNode) []string {
	texts := make([]string, len(symbols))
	for i := range symbols {
		texts[i] = p.serializer.SerializeSymbol(&symbols[i])
	}
	return texts
}

func (p *VectorIngestionPipeline) computeEmbeddings(ctx context.Context, texts []string) ([][]float32, error) {
	embeddings := make([][]float32, 0, len(texts))

	for i := 0; i < len(texts); i += p.config.BatchSize {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		end := i + p.config.BatchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch := texts[i:end]

		batchEmbeddings, err := p.embedder.EmbedBatch(ctx, batch)
		if err != nil {
			return nil, fmt.Errorf("embed batch %d-%d: %w", i, end, err)
		}

		embeddings = append(embeddings, batchEmbeddings...)
		p.progress.Store(int64(len(embeddings)))
		p.reportProgress("embedding", int64(len(embeddings)), p.totalItems.Load())
	}

	return embeddings, nil
}

func (p *VectorIngestionPipeline) writeToStorage(
	ctx context.Context,
	symbols []mainIngestion.SymbolNode,
	embeddings [][]float32,
) (int64, error) {
	dimension := p.embedder.Dimension()

	vectorPath := filepath.Join(p.config.StorageDir, "vectors.bin")
	vectorStore, err := storage.CreateVectorStore(vectorPath, dimension, len(symbols))
	if err != nil {
		return 0, fmt.Errorf("create vector store: %w", err)
	}
	defer vectorStore.Close()

	labelPath := filepath.Join(p.config.StorageDir, "labels.bin")
	labelStore, err := storage.CreateLabelStore(labelPath, len(symbols))
	if err != nil {
		return 0, fmt.Errorf("create label store: %w", err)
	}
	defer labelStore.Close()

	idMap := storage.NewIDMap()

	writer := NewBatchVectorWriter(BatchWriterConfig{
		VectorStore: vectorStore,
		LabelStore:  labelStore,
		IDMap:       idMap,
		BatchSize:   1000,
		OnProgress: func(written, _ int64) {
			p.reportProgress("writing", written, int64(len(symbols)))
		},
	})

	for i, sym := range symbols {
		if err := ctx.Err(); err != nil {
			return 0, err
		}

		symbolID := p.makeSymbolID(sym)
		nodeType := symbolKindToNodeType(sym.Kind)

		if err := writer.Add(symbolID, embeddings[i], vectorgraphdb.DomainCode, nodeType); err != nil {
			return 0, fmt.Errorf("add symbol %s: %w", sym.Name, err)
		}
	}

	if err := writer.Close(); err != nil {
		return 0, fmt.Errorf("close writer: %w", err)
	}

	idMapPath := filepath.Join(p.config.StorageDir, "idmap.bin")
	if err := idMap.SaveBinary(idMapPath); err != nil {
		return 0, fmt.Errorf("save id map: %w", err)
	}

	return p.calculateDiskSize(), nil
}

func (p *VectorIngestionPipeline) makeSymbolID(sym mainIngestion.SymbolNode) string {
	return fmt.Sprintf("%d:%s", sym.FileID, sym.Name)
}

func (p *VectorIngestionPipeline) calculateDiskSize() int64 {
	var size int64
	files := []string{"vectors.bin", "labels.bin", "idmap.bin"}
	for _, name := range files {
		path := filepath.Join(p.config.StorageDir, name)
		if info, err := os.Stat(path); err == nil {
			size += info.Size()
		}
	}
	return size
}

func (p *VectorIngestionPipeline) reportProgress(phase string, current, total int64) {
	if p.config.OnProgress != nil {
		p.config.OnProgress(phase, current, total)
	}
}

func (p *VectorIngestionPipeline) Close() error {
	return nil
}

func (p *VectorIngestionPipeline) Source() string {
	return p.source
}

func (p *VectorIngestionPipeline) Tier() embedder.ModelTier {
	return p.tier
}

func (p *VectorIngestionPipeline) Dimension() int {
	return p.embedder.Dimension()
}

func symbolKindToNodeType(kind mainIngestion.SymbolKind) uint16 {
	switch kind {
	case mainIngestion.SymbolKindFunction:
		return NodeTypeFunction
	case mainIngestion.SymbolKindMethod:
		return NodeTypeMethod
	case mainIngestion.SymbolKindType:
		return NodeTypeStruct
	case mainIngestion.SymbolKindInterface:
		return NodeTypeInterface
	case mainIngestion.SymbolKindConst:
		return NodeTypeConstant
	case mainIngestion.SymbolKindVar:
		return NodeTypeVariable
	default:
		return NodeTypeUnknown
	}
}

func (p *VectorIngestionPipeline) IngestCodeGraphParallel(
	ctx context.Context,
	graph *mainIngestion.CodeGraph,
	workers int,
) (*VectorPipelineResult, error) {
	if workers <= 1 {
		return p.IngestCodeGraph(ctx, graph)
	}

	start := time.Now()
	result := &VectorPipelineResult{
		EmbedderSource: p.source,
		EmbedderTier:   p.tier,
	}

	for i := range graph.Files {
		p.serializer.RegisterFile(&graph.Files[i])
	}

	serializeStart := time.Now()
	texts := p.serializeSymbols(graph.Symbols)
	result.SerializeTime = time.Since(serializeStart)
	result.SymbolCount = len(texts)

	if len(texts) == 0 {
		result.Duration = time.Since(start)
		return result, nil
	}

	p.totalItems.Store(int64(len(texts)))
	p.reportProgress("embedding", 0, int64(len(texts)))

	embedStart := time.Now()
	embeddings, err := p.computeEmbeddingsParallel(ctx, texts, workers)
	if err != nil {
		return nil, fmt.Errorf("compute embeddings: %w", err)
	}
	result.EmbedTime = time.Since(embedStart)

	p.reportProgress("writing", 0, int64(len(embeddings)))

	writeStart := time.Now()
	diskSize, err := p.writeToStorage(ctx, graph.Symbols, embeddings)
	if err != nil {
		return nil, fmt.Errorf("write to storage: %w", err)
	}
	result.WriteTime = time.Since(writeStart)
	result.DiskSizeBytes = diskSize

	result.Duration = time.Since(start)
	p.reportProgress("complete", int64(len(embeddings)), int64(len(embeddings)))

	return result, nil
}

func (p *VectorIngestionPipeline) computeEmbeddingsParallel(
	ctx context.Context,
	texts []string,
	workers int,
) ([][]float32, error) {
	embeddings := make([][]float32, len(texts))
	chunkSize := (len(texts) + workers - 1) / workers

	var wg sync.WaitGroup
	errCh := make(chan error, workers)

	for w := range workers {
		start := w * chunkSize
		if start >= len(texts) {
			break
		}

		end := start + chunkSize
		if end > len(texts) {
			end = len(texts)
		}

		wg.Add(1)
		go func(start, end int) {
			defer wg.Done()

			for i := start; i < end; i += p.config.BatchSize {
				if err := ctx.Err(); err != nil {
					select {
					case errCh <- err:
					default:
					}
					return
				}

				batchEnd := i + p.config.BatchSize
				if batchEnd > end {
					batchEnd = end
				}

				batch := texts[i:batchEnd]
				batchEmbeddings, err := p.embedder.EmbedBatch(ctx, batch)
				if err != nil {
					select {
					case errCh <- fmt.Errorf("worker embed batch %d-%d: %w", i, batchEnd, err):
					default:
					}
					return
				}

				for j, emb := range batchEmbeddings {
					embeddings[i+j] = emb
				}

				current := p.progress.Add(int64(len(batchEmbeddings)))
				p.reportProgress("embedding", current, p.totalItems.Load())
			}
		}(start, end)
	}

	wg.Wait()
	close(errCh)

	if err := <-errCh; err != nil {
		return nil, err
	}

	return embeddings, nil
}
