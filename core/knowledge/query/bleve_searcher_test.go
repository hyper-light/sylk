package query

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/blevesearch/bleve/v2"
	blevesearch "github.com/blevesearch/bleve/v2/search"
)

// =============================================================================
// Mock Bleve Index
// =============================================================================

// mockBleveIndex implements BleveIndex for testing.
type mockBleveIndex struct {
	searchFunc func(ctx context.Context, req *bleve.SearchRequest) (*bleve.SearchResult, error)
}

func (m *mockBleveIndex) SearchInContext(ctx context.Context, req *bleve.SearchRequest) (*bleve.SearchResult, error) {
	if m.searchFunc != nil {
		return m.searchFunc(ctx, req)
	}
	return &bleve.SearchResult{}, nil
}

// =============================================================================
// BleveSearcher Constructor Tests
// =============================================================================

func TestNewBleveSearcher(t *testing.T) {
	mock := &mockBleveIndex{}
	searcher := NewBleveSearcher(mock)

	if searcher == nil {
		t.Fatal("NewBleveSearcher returned nil")
	}

	if !searcher.IsReady() {
		t.Error("expected IsReady() to return true")
	}
}

func TestNewBleveSearcher_NilIndex(t *testing.T) {
	searcher := NewBleveSearcher(nil)

	if searcher == nil {
		t.Fatal("NewBleveSearcher returned nil")
	}

	if searcher.IsReady() {
		t.Error("expected IsReady() to return false for nil index")
	}
}

// =============================================================================
// BleveSearcher Execute Tests
// =============================================================================

func TestBleveSearcher_Execute_Success(t *testing.T) {
	mock := &mockBleveIndex{
		searchFunc: func(ctx context.Context, req *bleve.SearchRequest) (*bleve.SearchResult, error) {
			return &bleve.SearchResult{
				Hits: []*blevesearch.DocumentMatch{
					{
						ID:    "doc1",
						Score: 0.95,
						Fields: map[string]interface{}{
							"content": "test content 1",
						},
						Fragments: map[string][]string{
							"content": {"highlighted content"},
						},
					},
					{
						ID:    "doc2",
						Score: 0.85,
						Fields: map[string]interface{}{
							"content": "test content 2",
						},
					},
				},
				Total: 2,
			}, nil
		},
	}

	searcher := NewBleveSearcher(mock)
	ctx := context.Background()

	results, err := searcher.Execute(ctx, "test query", 10)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Check first result
	if results[0].ID != "doc1" {
		t.Errorf("results[0].ID = %v, want doc1", results[0].ID)
	}
	if results[0].Score != 0.95 {
		t.Errorf("results[0].Score = %v, want 0.95", results[0].Score)
	}
	if results[0].Content != "test content 1" {
		t.Errorf("results[0].Content = %v, want 'test content 1'", results[0].Content)
	}
	if len(results[0].Highlights) != 1 {
		t.Errorf("results[0].Highlights length = %d, want 1", len(results[0].Highlights))
	}

	// Check second result
	if results[1].ID != "doc2" {
		t.Errorf("results[1].ID = %v, want doc2", results[1].ID)
	}
}

func TestBleveSearcher_Execute_NilIndex(t *testing.T) {
	searcher := NewBleveSearcher(nil)
	ctx := context.Background()

	results, err := searcher.Execute(ctx, "test query", 10)
	if err != nil {
		t.Fatalf("Execute() should not return error for nil index, got %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected empty results for nil index, got %d", len(results))
	}
}

func TestBleveSearcher_Execute_EmptyQuery(t *testing.T) {
	mock := &mockBleveIndex{}
	searcher := NewBleveSearcher(mock)
	ctx := context.Background()

	results, err := searcher.Execute(ctx, "", 10)
	if err != nil {
		t.Fatalf("Execute() should not return error for empty query, got %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected empty results for empty query, got %d", len(results))
	}
}

func TestBleveSearcher_Execute_ZeroLimit(t *testing.T) {
	searchCalled := false
	mock := &mockBleveIndex{
		searchFunc: func(ctx context.Context, req *bleve.SearchRequest) (*bleve.SearchResult, error) {
			searchCalled = true
			// With zero limit, should default to 10
			if req.Size != 10 {
				t.Errorf("expected default limit of 10, got %d", req.Size)
			}
			return &bleve.SearchResult{}, nil
		},
	}

	searcher := NewBleveSearcher(mock)
	ctx := context.Background()

	_, err := searcher.Execute(ctx, "test", 0)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !searchCalled {
		t.Error("expected search to be called")
	}
}

func TestBleveSearcher_Execute_NegativeLimit(t *testing.T) {
	searchCalled := false
	mock := &mockBleveIndex{
		searchFunc: func(ctx context.Context, req *bleve.SearchRequest) (*bleve.SearchResult, error) {
			searchCalled = true
			// With negative limit, should default to 10
			if req.Size != 10 {
				t.Errorf("expected default limit of 10, got %d", req.Size)
			}
			return &bleve.SearchResult{}, nil
		},
	}

	searcher := NewBleveSearcher(mock)
	ctx := context.Background()

	_, err := searcher.Execute(ctx, "test", -5)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !searchCalled {
		t.Error("expected search to be called")
	}
}

func TestBleveSearcher_Execute_SearchError_GracefulDegradation(t *testing.T) {
	mock := &mockBleveIndex{
		searchFunc: func(ctx context.Context, req *bleve.SearchRequest) (*bleve.SearchResult, error) {
			return nil, errors.New("search failed")
		},
	}

	searcher := NewBleveSearcher(mock)
	ctx := context.Background()

	results, err := searcher.Execute(ctx, "test query", 10)

	// Should return empty results, not error (graceful degradation)
	if err != nil {
		t.Errorf("Execute() should return nil error for graceful degradation, got %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected empty results on search error, got %d", len(results))
	}
}

func TestBleveSearcher_Execute_ContextCancelled(t *testing.T) {
	mock := &mockBleveIndex{}
	searcher := NewBleveSearcher(mock)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	results, err := searcher.Execute(ctx, "test query", 10)

	if err != context.Canceled {
		t.Errorf("Execute() should return context.Canceled, got %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected empty results on context cancel, got %d", len(results))
	}
}

func TestBleveSearcher_Execute_ContextTimeout(t *testing.T) {
	mock := &mockBleveIndex{
		searchFunc: func(ctx context.Context, req *bleve.SearchRequest) (*bleve.SearchResult, error) {
			// Simulate slow search
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(100 * time.Millisecond):
				return &bleve.SearchResult{}, nil
			}
		},
	}

	searcher := NewBleveSearcher(mock)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	results, err := searcher.Execute(ctx, "test query", 10)

	// Should gracefully degrade on timeout
	if err != nil {
		t.Errorf("Execute() should gracefully degrade on timeout, got %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected empty results on timeout, got %d", len(results))
	}
}

func TestBleveSearcher_Execute_EmptyResults(t *testing.T) {
	mock := &mockBleveIndex{
		searchFunc: func(ctx context.Context, req *bleve.SearchRequest) (*bleve.SearchResult, error) {
			return &bleve.SearchResult{
				Hits:  []*blevesearch.DocumentMatch{},
				Total: 0,
			}, nil
		},
	}

	searcher := NewBleveSearcher(mock)
	ctx := context.Background()

	results, err := searcher.Execute(ctx, "no match query", 10)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}

func TestBleveSearcher_Execute_NilResult(t *testing.T) {
	mock := &mockBleveIndex{
		searchFunc: func(ctx context.Context, req *bleve.SearchRequest) (*bleve.SearchResult, error) {
			return nil, nil
		},
	}

	searcher := NewBleveSearcher(mock)
	ctx := context.Background()

	results, err := searcher.Execute(ctx, "test query", 10)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected empty results for nil result, got %d", len(results))
	}
}

// =============================================================================
// TextResult Tests
// =============================================================================

func TestTextResult_Fields(t *testing.T) {
	result := TextResult{
		ID:         "test-id",
		Score:      0.95,
		Content:    "test content",
		Highlights: []string{"highlighted text 1", "highlighted text 2"},
	}

	if result.ID != "test-id" {
		t.Errorf("ID = %v, want test-id", result.ID)
	}
	if result.Score != 0.95 {
		t.Errorf("Score = %v, want 0.95", result.Score)
	}
	if result.Content != "test content" {
		t.Errorf("Content = %v, want 'test content'", result.Content)
	}
	if len(result.Highlights) != 2 {
		t.Errorf("Highlights length = %d, want 2", len(result.Highlights))
	}
}

// =============================================================================
// BleveSearcher Highlight Extraction Tests
// =============================================================================

func TestBleveSearcher_ExtractHighlights_MultipleFields(t *testing.T) {
	mock := &mockBleveIndex{
		searchFunc: func(ctx context.Context, req *bleve.SearchRequest) (*bleve.SearchResult, error) {
			return &bleve.SearchResult{
				Hits: []*blevesearch.DocumentMatch{
					{
						ID:    "doc1",
						Score: 0.95,
						Fragments: map[string][]string{
							"content":  {"content highlight 1", "content highlight 2"},
							"comments": {"comment highlight"},
						},
					},
				},
				Total: 1,
			}, nil
		},
	}

	searcher := NewBleveSearcher(mock)
	ctx := context.Background()

	results, err := searcher.Execute(ctx, "test", 10)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	// Should have all highlights from all fields
	if len(results[0].Highlights) != 3 {
		t.Errorf("expected 3 highlights, got %d", len(results[0].Highlights))
	}
}

func TestBleveSearcher_ExtractHighlights_NoFragments(t *testing.T) {
	mock := &mockBleveIndex{
		searchFunc: func(ctx context.Context, req *bleve.SearchRequest) (*bleve.SearchResult, error) {
			return &bleve.SearchResult{
				Hits: []*blevesearch.DocumentMatch{
					{
						ID:        "doc1",
						Score:     0.95,
						Fragments: nil,
					},
				},
				Total: 1,
			}, nil
		},
	}

	searcher := NewBleveSearcher(mock)
	ctx := context.Background()

	results, err := searcher.Execute(ctx, "test", 10)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	if results[0].Highlights != nil && len(results[0].Highlights) != 0 {
		t.Errorf("expected nil or empty highlights, got %v", results[0].Highlights)
	}
}

// =============================================================================
// BleveSearcher IsReady Tests
// =============================================================================

func TestBleveSearcher_IsReady(t *testing.T) {
	tests := []struct {
		name     string
		index    BleveIndex
		expected bool
	}{
		{"with_index", &mockBleveIndex{}, true},
		{"nil_index", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			searcher := NewBleveSearcher(tt.index)
			if got := searcher.IsReady(); got != tt.expected {
				t.Errorf("IsReady() = %v, want %v", got, tt.expected)
			}
		})
	}
}
