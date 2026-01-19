package coordinator

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockVectorDB is a test double for VectorDB.
type mockVectorDB struct {
	mu          sync.Mutex
	results     []vectorgraphdb.SearchResult
	err         error
	searchCount int
}

func (m *mockVectorDB) Search(query []float32, opts *vectorgraphdb.SearchOptions) ([]vectorgraphdb.SearchResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.searchCount++
	if m.err != nil {
		return nil, m.err
	}
	return m.results, nil
}

// mockEmbedder is a test double for EmbeddingProvider.
type mockEmbedder struct {
	mu         sync.Mutex
	embedding  []float32
	err        error
	embedCount int
}

func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.embedCount++
	if m.err != nil {
		return nil, m.err
	}
	return m.embedding, nil
}

func TestNewVectorDBAdapter_Success(t *testing.T) {
	vectorDB := &mockVectorDB{}
	embedder := &mockEmbedder{}

	adapter, err := NewVectorDBAdapter(vectorDB, embedder)

	require.NoError(t, err)
	assert.NotNil(t, adapter)
}

func TestNewVectorDBAdapter_NilVectorDB(t *testing.T) {
	embedder := &mockEmbedder{}

	adapter, err := NewVectorDBAdapter(nil, embedder)

	assert.ErrorIs(t, err, ErrNilVectorDB)
	assert.Nil(t, adapter)
}

func TestNewVectorDBAdapter_NilEmbedder(t *testing.T) {
	vectorDB := &mockVectorDB{}

	adapter, err := NewVectorDBAdapter(vectorDB, nil)

	assert.ErrorIs(t, err, ErrNilEmbedder)
	assert.Nil(t, adapter)
}

func TestSearch_WithResults(t *testing.T) {
	now := time.Now()
	vectorDB := &mockVectorDB{
		results: []vectorgraphdb.SearchResult{
			{
				Node: &vectorgraphdb.GraphNode{
					ID:          "node-1",
					Path:        "/path/to/file.go",
					NodeType:    vectorgraphdb.NodeTypeFile,
					Content:     "package main",
					ContentHash: "abc123",
					UpdatedAt:   now,
					CreatedAt:   now,
				},
				Similarity: 0.95,
			},
			{
				Node: &vectorgraphdb.GraphNode{
					ID:          "node-2",
					Path:        "/path/to/other.go",
					NodeType:    vectorgraphdb.NodeTypeFunction,
					Content:     "func Hello()",
					ContentHash: "def456",
					UpdatedAt:   now,
					CreatedAt:   now,
				},
				Similarity: 0.85,
			},
		},
	}
	embedder := &mockEmbedder{
		embedding: []float32{0.1, 0.2, 0.3},
	}

	adapter, err := NewVectorDBAdapter(vectorDB, embedder)
	require.NoError(t, err)

	results, err := adapter.Search(context.Background(), "test query", 10)

	require.NoError(t, err)
	require.Len(t, results, 2)

	assert.Equal(t, "node-1", results[0].ID)
	assert.Equal(t, "/path/to/file.go", results[0].Path)
	assert.Equal(t, 0.95, results[0].Score)

	assert.Equal(t, "node-2", results[1].ID)
	assert.Equal(t, "/path/to/other.go", results[1].Path)
	assert.Equal(t, 0.85, results[1].Score)
}

func TestSearch_EmptyResults(t *testing.T) {
	vectorDB := &mockVectorDB{
		results: []vectorgraphdb.SearchResult{},
	}
	embedder := &mockEmbedder{
		embedding: []float32{0.1, 0.2, 0.3},
	}

	adapter, err := NewVectorDBAdapter(vectorDB, embedder)
	require.NoError(t, err)

	results, err := adapter.Search(context.Background(), "test query", 10)

	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestSearch_EmptyQuery(t *testing.T) {
	vectorDB := &mockVectorDB{}
	embedder := &mockEmbedder{}

	adapter, err := NewVectorDBAdapter(vectorDB, embedder)
	require.NoError(t, err)

	results, err := adapter.Search(context.Background(), "", 10)

	assert.ErrorIs(t, err, ErrEmptyQuery)
	assert.Nil(t, results)
}

func TestSearch_EmbeddingError(t *testing.T) {
	vectorDB := &mockVectorDB{}
	embedder := &mockEmbedder{
		err: errors.New("embedding service unavailable"),
	}

	adapter, err := NewVectorDBAdapter(vectorDB, embedder)
	require.NoError(t, err)

	results, err := adapter.Search(context.Background(), "test query", 10)

	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrEmbeddingFail))
	assert.Nil(t, results)
}

func TestSearch_VectorDBError(t *testing.T) {
	vectorDB := &mockVectorDB{
		err: errors.New("database connection failed"),
	}
	embedder := &mockEmbedder{
		embedding: []float32{0.1, 0.2, 0.3},
	}

	adapter, err := NewVectorDBAdapter(vectorDB, embedder)
	require.NoError(t, err)

	results, err := adapter.Search(context.Background(), "test query", 10)

	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrSearchFail))
	assert.Nil(t, results)
}

func TestSearch_DefaultLimit(t *testing.T) {
	vectorDB := &mockVectorDB{
		results: []vectorgraphdb.SearchResult{},
	}
	embedder := &mockEmbedder{
		embedding: []float32{0.1, 0.2, 0.3},
	}

	adapter, err := NewVectorDBAdapter(vectorDB, embedder)
	require.NoError(t, err)

	// Pass 0 or negative limit
	_, err = adapter.Search(context.Background(), "test query", 0)
	require.NoError(t, err)

	_, err = adapter.Search(context.Background(), "test query", -5)
	require.NoError(t, err)
}

func TestSearch_NodeTypeMapping(t *testing.T) {
	tests := []struct {
		name       string
		nodeType   vectorgraphdb.NodeType
		expectType string
	}{
		{"File", vectorgraphdb.NodeTypeFile, "source_code"},
		{"Function", vectorgraphdb.NodeTypeFunction, "source_code"},
		{"Method", vectorgraphdb.NodeTypeMethod, "source_code"},
		{"Struct", vectorgraphdb.NodeTypeStruct, "source_code"},
		{"Interface", vectorgraphdb.NodeTypeInterface, "source_code"},
		{"Documentation", vectorgraphdb.NodeTypeDocumentation, "markdown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vectorDB := &mockVectorDB{
				results: []vectorgraphdb.SearchResult{
					{
						Node: &vectorgraphdb.GraphNode{
							ID:          "node-1",
							NodeType:    tt.nodeType,
							ContentHash: "hash",
						},
						Similarity: 0.9,
					},
				},
			}
			embedder := &mockEmbedder{
				embedding: []float32{0.1},
			}

			adapter, err := NewVectorDBAdapter(vectorDB, embedder)
			require.NoError(t, err)

			results, err := adapter.Search(context.Background(), "query", 10)

			require.NoError(t, err)
			require.Len(t, results, 1)
			assert.Equal(t, tt.expectType, string(results[0].Type))
		})
	}
}

func TestSearchWithTimeout_Success(t *testing.T) {
	vectorDB := &mockVectorDB{
		results: []vectorgraphdb.SearchResult{
			{
				Node: &vectorgraphdb.GraphNode{
					ID:          "node-1",
					ContentHash: "hash",
				},
				Similarity: 0.9,
			},
		},
	}
	embedder := &mockEmbedder{
		embedding: []float32{0.1, 0.2, 0.3},
	}

	adapter, err := NewVectorDBAdapter(vectorDB, embedder)
	require.NoError(t, err)

	results, err := adapter.SearchWithTimeout(
		context.Background(),
		"test query",
		10,
		5*time.Second,
	)

	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestSearch_ConcurrentAccess(t *testing.T) {
	vectorDB := &mockVectorDB{
		results: []vectorgraphdb.SearchResult{
			{
				Node: &vectorgraphdb.GraphNode{
					ID:          "node-1",
					ContentHash: "hash",
				},
				Similarity: 0.9,
			},
		},
	}
	embedder := &mockEmbedder{
		embedding: []float32{0.1, 0.2, 0.3},
	}

	adapter, err := NewVectorDBAdapter(vectorDB, embedder)
	require.NoError(t, err)

	var wg sync.WaitGroup
	errCh := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := adapter.Search(context.Background(), "concurrent query", 10)
			if err != nil {
				errCh <- err
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent search error: %v", err)
	}
}

func TestSearch_NilNodeInResult(t *testing.T) {
	vectorDB := &mockVectorDB{
		results: []vectorgraphdb.SearchResult{
			{
				Node:       nil, // nil node
				Similarity: 0.9,
			},
		},
	}
	embedder := &mockEmbedder{
		embedding: []float32{0.1, 0.2, 0.3},
	}

	adapter, err := NewVectorDBAdapter(vectorDB, embedder)
	require.NoError(t, err)

	results, err := adapter.Search(context.Background(), "test query", 10)

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, 0.9, results[0].Score)
	assert.Empty(t, results[0].ID)
}

func TestSearch_ContextCancellation(t *testing.T) {
	vectorDB := &mockVectorDB{
		results: []vectorgraphdb.SearchResult{},
	}
	embedder := &mockEmbedder{
		embedding: []float32{0.1, 0.2, 0.3},
	}

	adapter, err := NewVectorDBAdapter(vectorDB, embedder)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// The search should still work since our mock doesn't check context
	// In a real implementation, the embedder would respect the context
	_, err = adapter.Search(ctx, "test query", 10)
	// No error expected from mock - real implementation would handle this
	assert.NoError(t, err)
}
