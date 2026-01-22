package document

import (
	"context"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

type BleveIndexer interface {
	Index(ctx context.Context, doc BleveDocument) error
}

type BleveDocument struct {
	ID          string
	DocumentID  string
	ChunkIndex  int
	Content     string
	ContentHash string
	Domain      string
	NodeType    string
	Source      string
	CreatedAt   time.Time
}

type VectorIndexer interface {
	AddNode(node *vectorgraphdb.GraphNode, embedding []float32) error
}

type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

type IndexedChunk struct {
	Chunk      Chunk
	BleveDocID string
	VectorID   string
	Embedding  []float32
}

type DualIndexResult struct {
	DocumentID    string
	ChunksIndexed int
	BleveIndexed  int
	VectorIndexed int
	Embeddings    [][]float32
	IndexedChunks []IndexedChunk
}

type DualIndexerConfig struct {
	Domain   vectorgraphdb.Domain
	NodeType vectorgraphdb.NodeType
	Source   string
}

func DefaultDualIndexerConfig() DualIndexerConfig {
	return DualIndexerConfig{
		Domain:   vectorgraphdb.DomainAcademic,
		NodeType: vectorgraphdb.NodeTypePaper,
		Source:   "document",
	}
}

type DualIndexer struct {
	chunker  *Chunker
	bleve    BleveIndexer
	vector   VectorIndexer
	embedder Embedder
	config   DualIndexerConfig
}

func NewDualIndexer(
	chunker *Chunker,
	bleve BleveIndexer,
	vector VectorIndexer,
	embedder Embedder,
	config DualIndexerConfig,
) *DualIndexer {
	return &DualIndexer{
		chunker:  chunker,
		bleve:    bleve,
		vector:   vector,
		embedder: embedder,
		config:   config,
	}
}

func (d *DualIndexer) Index(ctx context.Context, documentID string, content string) (*DualIndexResult, error) {
	chunks := d.chunker.Chunk(documentID, content)
	if len(chunks) == 0 {
		return &DualIndexResult{DocumentID: documentID}, nil
	}

	texts := make([]string, len(chunks))
	for i, chunk := range chunks {
		texts[i] = chunk.Text
	}

	embeddings, err := d.embedder.EmbedBatch(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("embed batch: %w", err)
	}

	result := &DualIndexResult{
		DocumentID:    documentID,
		Embeddings:    embeddings,
		IndexedChunks: make([]IndexedChunk, 0, len(chunks)),
	}

	now := time.Now()

	for i, chunk := range chunks {
		indexed := IndexedChunk{
			Chunk:      chunk,
			BleveDocID: chunk.ID,
			VectorID:   chunk.ID,
			Embedding:  embeddings[i],
		}

		if d.bleve != nil {
			bleveDoc := BleveDocument{
				ID:          chunk.ID,
				DocumentID:  documentID,
				ChunkIndex:  chunk.Index,
				Content:     chunk.Text,
				ContentHash: chunk.ContentHash,
				Domain:      d.config.Domain.String(),
				NodeType:    d.config.NodeType.String(),
				Source:      d.config.Source,
				CreatedAt:   now,
			}

			if err := d.bleve.Index(ctx, bleveDoc); err != nil {
				return nil, fmt.Errorf("bleve index chunk %d: %w", i, err)
			}
			result.BleveIndexed++
		}

		if d.vector != nil {
			node := &vectorgraphdb.GraphNode{
				ID:          chunk.ID,
				Domain:      d.config.Domain,
				NodeType:    d.config.NodeType,
				Name:        fmt.Sprintf("%s_chunk_%d", documentID, chunk.Index),
				Content:     chunk.Text,
				ContentHash: chunk.ContentHash,
				Source:      d.config.Source,
				CreatedAt:   now,
				UpdatedAt:   now,
				Metadata: map[string]any{
					"document_id":  documentID,
					"chunk_index":  chunk.Index,
					"start_offset": chunk.StartOffset,
					"end_offset":   chunk.EndOffset,
				},
			}

			if err := d.vector.AddNode(node, embeddings[i]); err != nil {
				return nil, fmt.Errorf("vector index chunk %d: %w", i, err)
			}
			result.VectorIndexed++
		}

		result.IndexedChunks = append(result.IndexedChunks, indexed)
		result.ChunksIndexed++
	}

	return result, nil
}

func (d *DualIndexer) IndexBatch(ctx context.Context, documents map[string]string) ([]*DualIndexResult, error) {
	results := make([]*DualIndexResult, 0, len(documents))

	for docID, content := range documents {
		result, err := d.Index(ctx, docID, content)
		if err != nil {
			return results, fmt.Errorf("index document %s: %w", docID, err)
		}
		results = append(results, result)
	}

	return results, nil
}
