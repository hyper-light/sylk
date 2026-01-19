// Package coordinator provides search coordination across multiple backends.
package coordinator

import (
	"context"
	"errors"
	"time"

	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// VectorDB defines the interface for vector similarity search operations.
// This allows for dependency injection and testing.
type VectorDB interface {
	Search(query []float32, opts *vectorgraphdb.SearchOptions) ([]vectorgraphdb.SearchResult, error)
}

// EmbeddingProvider generates embeddings for text queries.
type EmbeddingProvider interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// VectorDBAdapter errors.
var (
	ErrNilVectorDB   = errors.New("vector database is nil")
	ErrNilEmbedder   = errors.New("embedding provider is nil")
	ErrEmbeddingFail = errors.New("failed to generate embedding")
	ErrSearchFail    = errors.New("vector search failed")
)

// VectorDBAdapter wraps a VectorGraphDB to provide document search.
type VectorDBAdapter struct {
	vectorDB VectorDB
	embedder EmbeddingProvider
}

// NewVectorDBAdapter creates a new VectorDBAdapter.
func NewVectorDBAdapter(vectorDB VectorDB, embedder EmbeddingProvider) (*VectorDBAdapter, error) {
	if vectorDB == nil {
		return nil, ErrNilVectorDB
	}
	if embedder == nil {
		return nil, ErrNilEmbedder
	}
	return &VectorDBAdapter{
		vectorDB: vectorDB,
		embedder: embedder,
	}, nil
}

// Search performs a vector similarity search and returns scored documents.
func (a *VectorDBAdapter) Search(ctx context.Context, query string, limit int) ([]*search.ScoredDocument, error) {
	if query == "" {
		return nil, ErrEmptyQuery
	}
	if limit <= 0 {
		limit = 10
	}

	embedding, err := a.generateEmbedding(ctx, query)
	if err != nil {
		return nil, err
	}

	return a.executeSearch(embedding, limit)
}

// generateEmbedding creates a vector embedding for the query text.
func (a *VectorDBAdapter) generateEmbedding(ctx context.Context, query string) ([]float32, error) {
	embedding, err := a.embedder.Embed(ctx, query)
	if err != nil {
		return nil, errors.Join(ErrEmbeddingFail, err)
	}
	return embedding, nil
}

// executeSearch performs the vector search and converts results.
func (a *VectorDBAdapter) executeSearch(embedding []float32, limit int) ([]*search.ScoredDocument, error) {
	opts := &vectorgraphdb.SearchOptions{Limit: limit}
	results, err := a.vectorDB.Search(embedding, opts)
	if err != nil {
		return nil, errors.Join(ErrSearchFail, err)
	}

	return a.convertResults(results), nil
}

// convertResults transforms VectorGraphDB results to ScoredDocuments.
func (a *VectorDBAdapter) convertResults(results []vectorgraphdb.SearchResult) []*search.ScoredDocument {
	docs := make([]*search.ScoredDocument, 0, len(results))
	for _, r := range results {
		doc := a.convertSingleResult(r)
		docs = append(docs, doc)
	}
	return docs
}

// convertSingleResult converts one VectorGraphDB result to a ScoredDocument.
func (a *VectorDBAdapter) convertSingleResult(r vectorgraphdb.SearchResult) *search.ScoredDocument {
	doc := &search.ScoredDocument{
		Score: r.Similarity,
	}

	if r.Node != nil {
		doc.Document = a.nodeToDocument(r.Node)
	}

	return doc
}

// nodeToDocument converts a GraphNode to a Document.
func (a *VectorDBAdapter) nodeToDocument(node *vectorgraphdb.GraphNode) search.Document {
	return search.Document{
		ID:         node.ID,
		Path:       node.Path,
		Type:       mapNodeTypeToDocType(node.NodeType),
		Content:    node.Content,
		Checksum:   node.ContentHash,
		ModifiedAt: node.UpdatedAt,
		IndexedAt:  node.CreatedAt,
	}
}

// mapNodeTypeToDocType maps a VectorGraphDB NodeType to a DocumentType.
func mapNodeTypeToDocType(nt vectorgraphdb.NodeType) search.DocumentType {
	switch nt {
	case vectorgraphdb.NodeTypeFile:
		return search.DocTypeSourceCode
	case vectorgraphdb.NodeTypeFunction, vectorgraphdb.NodeTypeMethod:
		return search.DocTypeSourceCode
	case vectorgraphdb.NodeTypeStruct, vectorgraphdb.NodeTypeInterface:
		return search.DocTypeSourceCode
	case vectorgraphdb.NodeTypeDocumentation:
		return search.DocTypeMarkdown
	default:
		return search.DocTypeSourceCode
	}
}

// SearchWithTimeout performs a search with a timeout context.
func (a *VectorDBAdapter) SearchWithTimeout(
	ctx context.Context,
	query string,
	limit int,
	timeout time.Duration,
) ([]*search.ScoredDocument, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return a.Search(ctx, query, limit)
}
