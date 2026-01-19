package agent

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/messaging"
	"github.com/adalundhe/sylk/core/search"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// IndexingState Tests
// =============================================================================

func TestIndexingState_IsValid(t *testing.T) {
	tests := []struct {
		name  string
		state IndexingState
		want  bool
	}{
		{"queued is valid", IndexStateQueued, true},
		{"running is valid", IndexStateRunning, true},
		{"completed is valid", IndexStateCompleted, true},
		{"failed is valid", IndexStateFailed, true},
		{"cancelled is valid", IndexStateCancelled, true},
		{"empty is invalid", IndexingState(""), false},
		{"unknown is invalid", IndexingState("unknown"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.state.IsValid())
		})
	}
}

func TestIndexingState_IsTerminal(t *testing.T) {
	tests := []struct {
		name  string
		state IndexingState
		want  bool
	}{
		{"queued is not terminal", IndexStateQueued, false},
		{"running is not terminal", IndexStateRunning, false},
		{"completed is terminal", IndexStateCompleted, true},
		{"failed is terminal", IndexStateFailed, true},
		{"cancelled is terminal", IndexStateCancelled, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.state.IsTerminal())
		})
	}
}

func TestIndexingState_String(t *testing.T) {
	assert.Equal(t, "queued", IndexStateQueued.String())
	assert.Equal(t, "running", IndexStateRunning.String())
	assert.Equal(t, "completed", IndexStateCompleted.String())
	assert.Equal(t, "failed", IndexStateFailed.String())
	assert.Equal(t, "cancelled", IndexStateCancelled.String())
}

// =============================================================================
// SearchFilters Tests
// =============================================================================

func TestSearchFilters_IsEmpty(t *testing.T) {
	t.Run("nil filters is empty", func(t *testing.T) {
		var f *SearchFilters
		assert.True(t, f.IsEmpty())
	})

	t.Run("zero value filters is empty", func(t *testing.T) {
		f := &SearchFilters{}
		assert.True(t, f.IsEmpty())
	})

	t.Run("filters with type is not empty", func(t *testing.T) {
		f := &SearchFilters{Type: search.DocTypeSourceCode}
		assert.False(t, f.IsEmpty())
	})

	t.Run("filters with path prefix is not empty", func(t *testing.T) {
		f := &SearchFilters{PathPrefix: "/src"}
		assert.False(t, f.IsEmpty())
	})

	t.Run("filters with language is not empty", func(t *testing.T) {
		f := &SearchFilters{Language: "go"}
		assert.False(t, f.IsEmpty())
	})

	t.Run("filters with modified after is not empty", func(t *testing.T) {
		now := time.Now()
		f := &SearchFilters{ModifiedAfter: &now}
		assert.False(t, f.IsEmpty())
	})

	t.Run("filters with modified before is not empty", func(t *testing.T) {
		now := time.Now()
		f := &SearchFilters{ModifiedBefore: &now}
		assert.False(t, f.IsEmpty())
	})

	t.Run("filters with fuzzy level is not empty", func(t *testing.T) {
		f := &SearchFilters{FuzzyLevel: 1}
		assert.False(t, f.IsEmpty())
	})

	t.Run("filters with highlights is not empty", func(t *testing.T) {
		f := &SearchFilters{IncludeHighlights: true}
		assert.False(t, f.IsEmpty())
	})
}

func TestSearchFilters_ToSearchRequest(t *testing.T) {
	t.Run("converts filters to search request", func(t *testing.T) {
		f := &SearchFilters{
			Type:              search.DocTypeSourceCode,
			PathPrefix:        "/src",
			FuzzyLevel:        1,
			IncludeHighlights: true,
		}

		req := f.ToSearchRequest("test query", 10, 5)

		assert.Equal(t, "test query", req.Query)
		assert.Equal(t, 10, req.Limit)
		assert.Equal(t, 5, req.Offset)
		assert.Equal(t, search.DocTypeSourceCode, req.Type)
		assert.Equal(t, "/src", req.PathFilter)
		assert.Equal(t, 1, req.FuzzyLevel)
		assert.True(t, req.IncludeHighlights)
	})

	t.Run("handles empty filters", func(t *testing.T) {
		f := &SearchFilters{}
		req := f.ToSearchRequest("query", 20, 0)

		assert.Equal(t, "query", req.Query)
		assert.Equal(t, 20, req.Limit)
		assert.Equal(t, 0, req.Offset)
		assert.Equal(t, search.DocumentType(""), req.Type)
		assert.Equal(t, "", req.PathFilter)
	})
}

// =============================================================================
// SearchRequest Tests
// =============================================================================

func TestNewSearchRequest(t *testing.T) {
	req := NewSearchRequest("test query", 10)

	assert.Equal(t, "test query", req.Query)
	assert.Equal(t, 10, req.Limit)
	assert.NotEmpty(t, req.RequestID)
	assert.False(t, req.Timestamp.IsZero())
}

func TestSearchRequest_Validate(t *testing.T) {
	t.Run("valid request", func(t *testing.T) {
		req := NewSearchRequest("test query", 10)
		err := req.Validate()
		assert.NoError(t, err)
	})

	t.Run("empty request ID", func(t *testing.T) {
		req := &SearchRequest{Query: "test"}
		err := req.Validate()
		assert.ErrorIs(t, err, ErrEmptyRequestID)
	})

	t.Run("empty query", func(t *testing.T) {
		req := &SearchRequest{RequestID: "req-1", Query: ""}
		err := req.Validate()
		assert.ErrorIs(t, err, ErrEmptyQuery)
	})

	t.Run("negative limit", func(t *testing.T) {
		req := &SearchRequest{RequestID: "req-1", Query: "test", Limit: -1}
		err := req.Validate()
		assert.ErrorIs(t, err, ErrInvalidLimit)
	})

	t.Run("zero limit is valid", func(t *testing.T) {
		req := &SearchRequest{RequestID: "req-1", Query: "test", Limit: 0}
		err := req.Validate()
		assert.NoError(t, err)
	})
}

func TestSearchRequest_Normalize(t *testing.T) {
	t.Run("applies default limit", func(t *testing.T) {
		req := &SearchRequest{Query: "test"}
		req.Normalize()

		assert.Equal(t, search.DefaultLimit, req.Limit)
	})

	t.Run("applies default timestamp", func(t *testing.T) {
		req := &SearchRequest{Query: "test"}
		req.Normalize()

		assert.False(t, req.Timestamp.IsZero())
	})

	t.Run("generates request ID", func(t *testing.T) {
		req := &SearchRequest{Query: "test"}
		req.Normalize()

		assert.NotEmpty(t, req.RequestID)
	})

	t.Run("does not override existing values", func(t *testing.T) {
		ts := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		req := &SearchRequest{
			Query:     "test",
			Limit:     50,
			RequestID: "existing-id",
			Timestamp: ts,
		}
		req.Normalize()

		assert.Equal(t, 50, req.Limit)
		assert.Equal(t, "existing-id", req.RequestID)
		assert.Equal(t, ts, req.Timestamp)
	})
}

func TestSearchRequest_WithFilters(t *testing.T) {
	req := NewSearchRequest("test", 10)
	filters := &SearchFilters{Type: search.DocTypeSourceCode}

	result := req.WithFilters(filters)

	assert.Same(t, req, result)
	assert.Equal(t, filters, req.Filters)
}

func TestSearchRequest_WithOffset(t *testing.T) {
	req := NewSearchRequest("test", 10)
	result := req.WithOffset(20)

	assert.Same(t, req, result)
	assert.Equal(t, 20, req.Offset)
}

// =============================================================================
// SearchResponse Tests
// =============================================================================

func TestNewSearchResponse(t *testing.T) {
	results := []search.ScoredDocument{
		{Document: search.Document{ID: "doc-1"}, Score: 0.9},
		{Document: search.Document{ID: "doc-2"}, Score: 0.8},
	}

	resp := NewSearchResponse("req-1", results, 100, 50*time.Millisecond)

	assert.Equal(t, "req-1", resp.RequestID)
	assert.Equal(t, results, resp.Results)
	assert.Equal(t, int64(100), resp.TotalHits)
	assert.Equal(t, 50*time.Millisecond, resp.Duration)
	assert.Empty(t, resp.Error)
	assert.False(t, resp.Timestamp.IsZero())
}

func TestNewSearchErrorResponse(t *testing.T) {
	t.Run("with error", func(t *testing.T) {
		err := errors.New("search failed")
		resp := NewSearchErrorResponse("req-1", err)

		assert.Equal(t, "req-1", resp.RequestID)
		assert.Empty(t, resp.Results)
		assert.Equal(t, "search failed", resp.Error)
		assert.False(t, resp.Timestamp.IsZero())
	})

	t.Run("with nil error", func(t *testing.T) {
		resp := NewSearchErrorResponse("req-1", nil)

		assert.Equal(t, "req-1", resp.RequestID)
		assert.Empty(t, resp.Error)
	})
}

func TestSearchResponse_Success(t *testing.T) {
	t.Run("success when no error", func(t *testing.T) {
		resp := NewSearchResponse("req-1", nil, 0, 0)
		assert.True(t, resp.Success())
	})

	t.Run("failure when error present", func(t *testing.T) {
		resp := NewSearchErrorResponse("req-1", errors.New("failed"))
		assert.False(t, resp.Success())
	})
}

func TestSearchResponse_HasResults(t *testing.T) {
	t.Run("has results", func(t *testing.T) {
		results := []search.ScoredDocument{{Document: search.Document{ID: "doc-1"}}}
		resp := NewSearchResponse("req-1", results, 1, 0)
		assert.True(t, resp.HasResults())
	})

	t.Run("no results", func(t *testing.T) {
		resp := NewSearchResponse("req-1", nil, 0, 0)
		assert.False(t, resp.HasResults())
	})
}

func TestSearchResponse_ResultCount(t *testing.T) {
	results := []search.ScoredDocument{
		{Document: search.Document{ID: "doc-1"}},
		{Document: search.Document{ID: "doc-2"}},
	}
	resp := NewSearchResponse("req-1", results, 2, 0)

	assert.Equal(t, 2, resp.ResultCount())
}

// =============================================================================
// IndexRequest Tests
// =============================================================================

func TestNewIndexRequest(t *testing.T) {
	paths := []string{"/path/to/file1.go", "/path/to/file2.go"}
	req := NewIndexRequest(paths)

	assert.Equal(t, paths, req.Paths)
	assert.False(t, req.Force)
	assert.NotEmpty(t, req.RequestID)
	assert.False(t, req.Timestamp.IsZero())
}

func TestNewForceIndexRequest(t *testing.T) {
	paths := []string{"/path/to/file.go"}
	req := NewForceIndexRequest(paths)

	assert.Equal(t, paths, req.Paths)
	assert.True(t, req.Force)
}

func TestIndexRequest_Validate(t *testing.T) {
	t.Run("valid request", func(t *testing.T) {
		req := NewIndexRequest([]string{"/path/to/file.go"})
		err := req.Validate()
		assert.NoError(t, err)
	})

	t.Run("empty request ID", func(t *testing.T) {
		req := &IndexRequest{Paths: []string{"/path"}}
		err := req.Validate()
		assert.ErrorIs(t, err, ErrEmptyRequestID)
	})

	t.Run("empty paths", func(t *testing.T) {
		req := &IndexRequest{RequestID: "req-1", Paths: []string{}}
		err := req.Validate()
		assert.ErrorIs(t, err, ErrEmptyPaths)
	})

	t.Run("nil paths", func(t *testing.T) {
		req := &IndexRequest{RequestID: "req-1", Paths: nil}
		err := req.Validate()
		assert.ErrorIs(t, err, ErrEmptyPaths)
	})
}

func TestIndexRequest_Normalize(t *testing.T) {
	t.Run("applies default timestamp", func(t *testing.T) {
		req := &IndexRequest{Paths: []string{"/path"}}
		req.Normalize()

		assert.False(t, req.Timestamp.IsZero())
	})

	t.Run("generates request ID", func(t *testing.T) {
		req := &IndexRequest{Paths: []string{"/path"}}
		req.Normalize()

		assert.NotEmpty(t, req.RequestID)
	})
}

func TestIndexRequest_WithForce(t *testing.T) {
	req := NewIndexRequest([]string{"/path"})
	result := req.WithForce()

	assert.Same(t, req, result)
	assert.True(t, req.Force)
}

// =============================================================================
// IndexStatus Tests
// =============================================================================

func TestNewIndexStatus(t *testing.T) {
	status := NewIndexStatus("req-1", IndexStateRunning)

	assert.Equal(t, "req-1", status.RequestID)
	assert.Equal(t, IndexStateRunning, status.State)
	assert.False(t, status.Timestamp.IsZero())
}

func TestNewIndexStatusQueued(t *testing.T) {
	status := NewIndexStatusQueued("req-1", 100)

	assert.Equal(t, "req-1", status.RequestID)
	assert.Equal(t, IndexStateQueued, status.State)
	assert.Equal(t, 100, status.TotalFiles)
}

func TestNewIndexStatusRunning(t *testing.T) {
	status := NewIndexStatusRunning("req-1", 25, 100)

	assert.Equal(t, "req-1", status.RequestID)
	assert.Equal(t, IndexStateRunning, status.State)
	assert.Equal(t, 25, status.ProcessedFiles)
	assert.Equal(t, 100, status.TotalFiles)
	assert.Equal(t, 0.25, status.Progress)
}

func TestNewIndexStatusRunning_ZeroTotal(t *testing.T) {
	status := NewIndexStatusRunning("req-1", 0, 0)

	assert.Equal(t, float64(0), status.Progress)
}

func TestNewIndexStatusCompleted(t *testing.T) {
	status := NewIndexStatusCompleted("req-1", 90, 10, 5*time.Second)

	assert.Equal(t, "req-1", status.RequestID)
	assert.Equal(t, IndexStateCompleted, status.State)
	assert.Equal(t, 90, status.IndexedCount)
	assert.Equal(t, 10, status.FailedCount)
	assert.Equal(t, 100, status.ProcessedFiles)
	assert.Equal(t, 100, status.TotalFiles)
	assert.Equal(t, 1.0, status.Progress)
	assert.Equal(t, 5*time.Second, status.Duration)
}

func TestNewIndexStatusFailed(t *testing.T) {
	t.Run("with error", func(t *testing.T) {
		err := errors.New("disk full")
		status := NewIndexStatusFailed("req-1", err)

		assert.Equal(t, "req-1", status.RequestID)
		assert.Equal(t, IndexStateFailed, status.State)
		assert.Equal(t, "disk full", status.Error)
	})

	t.Run("with nil error", func(t *testing.T) {
		status := NewIndexStatusFailed("req-1", nil)

		assert.Equal(t, IndexStateFailed, status.State)
		assert.Empty(t, status.Error)
	})
}

func TestIndexStatus_Validate(t *testing.T) {
	t.Run("valid status", func(t *testing.T) {
		status := NewIndexStatus("req-1", IndexStateRunning)
		err := status.Validate()
		assert.NoError(t, err)
	})

	t.Run("empty request ID", func(t *testing.T) {
		status := &IndexStatus{State: IndexStateRunning}
		err := status.Validate()
		assert.ErrorIs(t, err, ErrEmptyRequestID)
	})

	t.Run("invalid state", func(t *testing.T) {
		status := &IndexStatus{RequestID: "req-1", State: IndexingState("invalid")}
		err := status.Validate()
		assert.ErrorIs(t, err, ErrInvalidState)
	})
}

func TestIndexStatus_IsComplete(t *testing.T) {
	tests := []struct {
		name  string
		state IndexingState
		want  bool
	}{
		{"queued is not complete", IndexStateQueued, false},
		{"running is not complete", IndexStateRunning, false},
		{"completed is complete", IndexStateCompleted, true},
		{"failed is complete", IndexStateFailed, true},
		{"cancelled is complete", IndexStateCancelled, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := NewIndexStatus("req-1", tt.state)
			assert.Equal(t, tt.want, status.IsComplete())
		})
	}
}

func TestIndexStatus_Success(t *testing.T) {
	t.Run("success when completed without error", func(t *testing.T) {
		status := NewIndexStatusCompleted("req-1", 100, 0, time.Second)
		assert.True(t, status.Success())
	})

	t.Run("failure when completed with error", func(t *testing.T) {
		status := NewIndexStatusCompleted("req-1", 100, 0, time.Second)
		status.Error = "some error"
		assert.False(t, status.Success())
	})

	t.Run("failure when failed state", func(t *testing.T) {
		status := NewIndexStatusFailed("req-1", errors.New("failed"))
		assert.False(t, status.Success())
	})

	t.Run("failure when running", func(t *testing.T) {
		status := NewIndexStatus("req-1", IndexStateRunning)
		assert.False(t, status.Success())
	})
}

func TestIndexStatus_SuccessRate(t *testing.T) {
	t.Run("all succeeded", func(t *testing.T) {
		status := NewIndexStatusCompleted("req-1", 100, 0, time.Second)
		assert.Equal(t, 1.0, status.SuccessRate())
	})

	t.Run("all failed", func(t *testing.T) {
		status := NewIndexStatusCompleted("req-1", 0, 100, time.Second)
		assert.Equal(t, 0.0, status.SuccessRate())
	})

	t.Run("mixed results", func(t *testing.T) {
		status := NewIndexStatusCompleted("req-1", 75, 25, time.Second)
		assert.Equal(t, 0.75, status.SuccessRate())
	})

	t.Run("no documents", func(t *testing.T) {
		status := NewIndexStatus("req-1", IndexStateRunning)
		assert.Equal(t, 0.0, status.SuccessRate())
	})
}

// =============================================================================
// Message Constructor Tests
// =============================================================================

func TestNewSearchRequestMessage(t *testing.T) {
	req := NewSearchRequest("test query", 10)
	msg := NewSearchRequestMessage("agent-1", "session-1", req)

	assert.Equal(t, MessageTypeSearchRequest, msg.Type)
	assert.Equal(t, "agent-1", msg.Source)
	assert.Equal(t, "session-1", msg.SessionID)
	assert.Equal(t, req.RequestID, msg.CorrelationID)
	assert.Equal(t, req, msg.Payload)
}

func TestNewSearchResponseMessage(t *testing.T) {
	resp := NewSearchResponse("req-1", nil, 0, 0)
	msg := NewSearchResponseMessage("agent-1", "session-1", "corr-1", resp)

	assert.Equal(t, MessageTypeSearchResponse, msg.Type)
	assert.Equal(t, "agent-1", msg.Source)
	assert.Equal(t, "session-1", msg.SessionID)
	assert.Equal(t, "corr-1", msg.CorrelationID)
	assert.Equal(t, resp, msg.Payload)
}

func TestNewIndexRequestMessage(t *testing.T) {
	req := NewIndexRequest([]string{"/path"})
	msg := NewIndexRequestMessage("agent-1", "session-1", req)

	assert.Equal(t, MessageTypeIndexRequest, msg.Type)
	assert.Equal(t, "agent-1", msg.Source)
	assert.Equal(t, "session-1", msg.SessionID)
	assert.Equal(t, req.RequestID, msg.CorrelationID)
	assert.Equal(t, req, msg.Payload)
}

func TestNewIndexStatusMessage(t *testing.T) {
	status := NewIndexStatus("req-1", IndexStateRunning)
	msg := NewIndexStatusMessage("agent-1", "session-1", "corr-1", status)

	assert.Equal(t, MessageTypeIndexStatus, msg.Type)
	assert.Equal(t, "agent-1", msg.Source)
	assert.Equal(t, "session-1", msg.SessionID)
	assert.Equal(t, "corr-1", msg.CorrelationID)
	assert.Equal(t, status, msg.Payload)
}

// =============================================================================
// JSON Serialization Tests
// =============================================================================

func TestSearchRequest_JSON_Roundtrip(t *testing.T) {
	now := time.Now()
	modifiedAfter := now.Add(-24 * time.Hour)

	original := &SearchRequest{
		Query:     "test query",
		Limit:     20,
		Offset:    10,
		RequestID: "req-123",
		Timestamp: now,
		Filters: &SearchFilters{
			Type:              search.DocTypeSourceCode,
			PathPrefix:        "/src",
			Language:          "go",
			ModifiedAfter:     &modifiedAfter,
			FuzzyLevel:        1,
			IncludeHighlights: true,
		},
	}

	data, err := MarshalSearchRequest(original)
	require.NoError(t, err)

	restored, err := UnmarshalSearchRequest(data)
	require.NoError(t, err)

	assert.Equal(t, original.Query, restored.Query)
	assert.Equal(t, original.Limit, restored.Limit)
	assert.Equal(t, original.Offset, restored.Offset)
	assert.Equal(t, original.RequestID, restored.RequestID)
	assert.Equal(t, original.Filters.Type, restored.Filters.Type)
	assert.Equal(t, original.Filters.PathPrefix, restored.Filters.PathPrefix)
	assert.Equal(t, original.Filters.Language, restored.Filters.Language)
	assert.Equal(t, original.Filters.FuzzyLevel, restored.Filters.FuzzyLevel)
	assert.Equal(t, original.Filters.IncludeHighlights, restored.Filters.IncludeHighlights)
}

func TestSearchResponse_JSON_Roundtrip(t *testing.T) {
	original := &SearchResponse{
		RequestID: "req-123",
		Results: []search.ScoredDocument{
			{
				Document: search.Document{
					ID:       "doc-1",
					Path:     "/path/to/file.go",
					Type:     search.DocTypeSourceCode,
					Language: "go",
					Content:  "package main",
				},
				Score:         0.95,
				MatchedFields: []string{"content", "symbols"},
			},
		},
		TotalHits: 100,
		Duration:  50 * time.Millisecond,
		Timestamp: time.Now(),
	}

	data, err := MarshalSearchResponse(original)
	require.NoError(t, err)

	restored, err := UnmarshalSearchResponse(data)
	require.NoError(t, err)

	assert.Equal(t, original.RequestID, restored.RequestID)
	assert.Equal(t, original.TotalHits, restored.TotalHits)
	assert.Len(t, restored.Results, 1)
	assert.Equal(t, original.Results[0].ID, restored.Results[0].ID)
	assert.Equal(t, original.Results[0].Score, restored.Results[0].Score)
}

func TestSearchResponse_JSON_WithError(t *testing.T) {
	original := NewSearchErrorResponse("req-123", errors.New("search failed"))

	data, err := MarshalSearchResponse(original)
	require.NoError(t, err)

	restored, err := UnmarshalSearchResponse(data)
	require.NoError(t, err)

	assert.Equal(t, "search failed", restored.Error)
	assert.Empty(t, restored.Results)
}

func TestIndexRequest_JSON_Roundtrip(t *testing.T) {
	original := &IndexRequest{
		Paths:     []string{"/path/to/file1.go", "/path/to/file2.go"},
		Force:     true,
		RequestID: "req-123",
		Timestamp: time.Now(),
	}

	data, err := MarshalIndexRequest(original)
	require.NoError(t, err)

	restored, err := UnmarshalIndexRequest(data)
	require.NoError(t, err)

	assert.Equal(t, original.Paths, restored.Paths)
	assert.Equal(t, original.Force, restored.Force)
	assert.Equal(t, original.RequestID, restored.RequestID)
}

func TestIndexStatus_JSON_Roundtrip(t *testing.T) {
	original := &IndexStatus{
		RequestID:      "req-123",
		State:          IndexStateRunning,
		Progress:       0.5,
		ProcessedFiles: 50,
		TotalFiles:     100,
		IndexedCount:   45,
		FailedCount:    5,
		Duration:       30 * time.Second,
		Timestamp:      time.Now(),
	}

	data, err := MarshalIndexStatus(original)
	require.NoError(t, err)

	restored, err := UnmarshalIndexStatus(data)
	require.NoError(t, err)

	assert.Equal(t, original.RequestID, restored.RequestID)
	assert.Equal(t, original.State, restored.State)
	assert.Equal(t, original.Progress, restored.Progress)
	assert.Equal(t, original.ProcessedFiles, restored.ProcessedFiles)
	assert.Equal(t, original.TotalFiles, restored.TotalFiles)
	assert.Equal(t, original.IndexedCount, restored.IndexedCount)
	assert.Equal(t, original.FailedCount, restored.FailedCount)
}

func TestIndexStatus_JSON_WithError(t *testing.T) {
	original := NewIndexStatusFailed("req-123", errors.New("disk full"))

	data, err := MarshalIndexStatus(original)
	require.NoError(t, err)

	restored, err := UnmarshalIndexStatus(data)
	require.NoError(t, err)

	assert.Equal(t, "disk full", restored.Error)
	assert.Equal(t, IndexStateFailed, restored.State)
}

func TestUnmarshal_InvalidJSON(t *testing.T) {
	invalidJSON := []byte(`{"invalid json`)

	_, err := UnmarshalSearchRequest(invalidJSON)
	assert.Error(t, err)

	_, err = UnmarshalSearchResponse(invalidJSON)
	assert.Error(t, err)

	_, err = UnmarshalIndexRequest(invalidJSON)
	assert.Error(t, err)

	_, err = UnmarshalIndexStatus(invalidJSON)
	assert.Error(t, err)
}

// =============================================================================
// Message Type Constants Tests
// =============================================================================

func TestMessageTypeConstants(t *testing.T) {
	// Verify message types are distinct and have expected values
	assert.Equal(t, messaging.MessageType("search_request"), MessageTypeSearchRequest)
	assert.Equal(t, messaging.MessageType("search_response"), MessageTypeSearchResponse)
	assert.Equal(t, messaging.MessageType("index_request"), MessageTypeIndexRequest)
	assert.Equal(t, messaging.MessageType("index_status"), MessageTypeIndexStatus)

	// Verify all types are unique
	types := []messaging.MessageType{
		MessageTypeSearchRequest,
		MessageTypeSearchResponse,
		MessageTypeIndexRequest,
		MessageTypeIndexStatus,
	}

	seen := make(map[messaging.MessageType]bool)
	for _, typ := range types {
		assert.False(t, seen[typ], "duplicate message type: %s", typ)
		seen[typ] = true
	}
}

// =============================================================================
// Integration-style Tests
// =============================================================================

func TestSearchRequestResponse_Workflow(t *testing.T) {
	// Create a search request
	req := NewSearchRequest("func main", 20)
	req.WithFilters(&SearchFilters{
		Type:     search.DocTypeSourceCode,
		Language: "go",
	})

	// Validate and normalize
	req.Normalize()
	err := req.Validate()
	require.NoError(t, err)

	// Create the message
	reqMsg := NewSearchRequestMessage("search-agent", "session-1", req)
	assert.NotNil(t, reqMsg)

	// Simulate search execution and create response
	results := []search.ScoredDocument{
		{
			Document: search.Document{
				ID:       "doc-1",
				Path:     "/main.go",
				Type:     search.DocTypeSourceCode,
				Language: "go",
				Content:  "func main() {}",
			},
			Score: 0.95,
		},
	}

	resp := NewSearchResponse(req.RequestID, results, 1, 10*time.Millisecond)
	respMsg := NewSearchResponseMessage("search-system", "session-1", reqMsg.CorrelationID, resp)

	assert.Equal(t, reqMsg.CorrelationID, respMsg.CorrelationID)
	assert.True(t, resp.Success())
	assert.True(t, resp.HasResults())
}

func TestIndexRequestStatus_Workflow(t *testing.T) {
	// Create an index request
	req := NewIndexRequest([]string{"/src/main.go", "/src/util.go"})
	req.Normalize()
	err := req.Validate()
	require.NoError(t, err)

	// Create the message
	reqMsg := NewIndexRequestMessage("indexer-agent", "session-1", req)

	// Simulate indexing workflow with status updates

	// Status: queued
	statusQueued := NewIndexStatusQueued(req.RequestID, 2)
	statusMsgQueued := NewIndexStatusMessage("search-system", "session-1", reqMsg.CorrelationID, statusQueued)
	assert.Equal(t, IndexStateQueued, statusMsgQueued.Payload.State)

	// Status: running
	statusRunning := NewIndexStatusRunning(req.RequestID, 1, 2)
	assert.Equal(t, 0.5, statusRunning.Progress)
	assert.False(t, statusRunning.IsComplete())

	// Status: completed
	statusCompleted := NewIndexStatusCompleted(req.RequestID, 2, 0, 100*time.Millisecond)
	assert.True(t, statusCompleted.IsComplete())
	assert.True(t, statusCompleted.Success())
	assert.Equal(t, 1.0, statusCompleted.SuccessRate())
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestSearchFilters_TimeFilters(t *testing.T) {
	now := time.Now()
	past := now.Add(-24 * time.Hour)
	future := now.Add(24 * time.Hour)

	t.Run("modified after only", func(t *testing.T) {
		f := &SearchFilters{ModifiedAfter: &past}
		assert.False(t, f.IsEmpty())
	})

	t.Run("modified before only", func(t *testing.T) {
		f := &SearchFilters{ModifiedBefore: &future}
		assert.False(t, f.IsEmpty())
	})

	t.Run("both time filters", func(t *testing.T) {
		f := &SearchFilters{
			ModifiedAfter:  &past,
			ModifiedBefore: &future,
		}
		assert.False(t, f.IsEmpty())
	})
}

func TestIndexStatus_ProgressCalculation(t *testing.T) {
	tests := []struct {
		name      string
		processed int
		total     int
		expected  float64
	}{
		{"0 of 100", 0, 100, 0.0},
		{"50 of 100", 50, 100, 0.5},
		{"100 of 100", 100, 100, 1.0},
		{"1 of 3", 1, 3, 1.0 / 3.0},
		{"0 of 0", 0, 0, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := NewIndexStatusRunning("req-1", tt.processed, tt.total)
			assert.InDelta(t, tt.expected, status.Progress, 0.0001)
		})
	}
}

func TestSearchResponse_EmptyResults(t *testing.T) {
	resp := NewSearchResponse("req-1", []search.ScoredDocument{}, 0, 10*time.Millisecond)

	assert.True(t, resp.Success())
	assert.False(t, resp.HasResults())
	assert.Equal(t, 0, resp.ResultCount())
	assert.Equal(t, int64(0), resp.TotalHits)
}

func TestSearchResponse_NilResults(t *testing.T) {
	resp := NewSearchResponse("req-1", nil, 0, 10*time.Millisecond)

	assert.True(t, resp.Success())
	assert.False(t, resp.HasResults())
	assert.Equal(t, 0, resp.ResultCount())
}

// Test JSON field names are correct
func TestJSON_FieldNames(t *testing.T) {
	t.Run("SearchRequest fields", func(t *testing.T) {
		req := &SearchRequest{
			Query:     "test",
			Limit:     10,
			Offset:    5,
			RequestID: "id",
			Timestamp: time.Now(),
		}

		data, _ := json.Marshal(req)
		var m map[string]any
		json.Unmarshal(data, &m)

		assert.Contains(t, m, "query")
		assert.Contains(t, m, "limit")
		assert.Contains(t, m, "offset")
		assert.Contains(t, m, "request_id")
		assert.Contains(t, m, "timestamp")
	})

	t.Run("IndexStatus fields", func(t *testing.T) {
		status := &IndexStatus{
			RequestID:      "id",
			State:          IndexStateRunning,
			Progress:       0.5,
			ProcessedFiles: 10,
			TotalFiles:     20,
			IndexedCount:   8,
			FailedCount:    2,
			Error:          "error",
			Duration:       time.Second,
			Timestamp:      time.Now(),
		}

		data, _ := json.Marshal(status)
		var m map[string]any
		json.Unmarshal(data, &m)

		assert.Contains(t, m, "request_id")
		assert.Contains(t, m, "state")
		assert.Contains(t, m, "progress")
		assert.Contains(t, m, "processed_files")
		assert.Contains(t, m, "total_files")
		assert.Contains(t, m, "indexed_count")
		assert.Contains(t, m, "failed_count")
		assert.Contains(t, m, "error")
		assert.Contains(t, m, "duration")
		assert.Contains(t, m, "timestamp")
	})
}
