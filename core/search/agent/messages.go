// Package agent provides message types for agent-to-search-system communication.
// It defines the request/response protocol for search and indexing operations
// that agents can use through the messaging system.
package agent

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/adalundhe/sylk/core/messaging"
	"github.com/adalundhe/sylk/core/search"
	"github.com/google/uuid"
)

// =============================================================================
// Message Type Constants
// =============================================================================

const (
	// MessageTypeSearchRequest is the message type for search requests.
	MessageTypeSearchRequest messaging.MessageType = "search_request"

	// MessageTypeSearchResponse is the message type for search responses.
	MessageTypeSearchResponse messaging.MessageType = "search_response"

	// MessageTypeIndexRequest is the message type for index requests.
	MessageTypeIndexRequest messaging.MessageType = "index_request"

	// MessageTypeIndexStatus is the message type for indexing status updates.
	MessageTypeIndexStatus messaging.MessageType = "index_status"
)

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrEmptyRequestID indicates a request ID is required but was empty.
	ErrEmptyRequestID = errors.New("request ID cannot be empty")

	// ErrEmptyQuery indicates a search query is required but was empty.
	ErrEmptyQuery = errors.New("search query cannot be empty")

	// ErrInvalidLimit indicates an invalid limit value.
	ErrInvalidLimit = errors.New("limit must be positive")

	// ErrEmptyPaths indicates no paths were provided for indexing.
	ErrEmptyPaths = errors.New("at least one path is required")

	// ErrInvalidState indicates an invalid indexing state.
	ErrInvalidState = errors.New("invalid indexing state")
)

// =============================================================================
// Indexing State
// =============================================================================

// IndexingState represents the state of an indexing operation.
type IndexingState string

const (
	// IndexStateQueued indicates the indexing request is queued.
	IndexStateQueued IndexingState = "queued"

	// IndexStateRunning indicates indexing is in progress.
	IndexStateRunning IndexingState = "running"

	// IndexStateCompleted indicates indexing completed successfully.
	IndexStateCompleted IndexingState = "completed"

	// IndexStateFailed indicates indexing failed.
	IndexStateFailed IndexingState = "failed"

	// IndexStateCancelled indicates indexing was cancelled.
	IndexStateCancelled IndexingState = "cancelled"
)

// IsValid returns true if the state is a recognized indexing state.
func (s IndexingState) IsValid() bool {
	switch s {
	case IndexStateQueued, IndexStateRunning, IndexStateCompleted, IndexStateFailed, IndexStateCancelled:
		return true
	default:
		return false
	}
}

// IsTerminal returns true if this is a final state.
func (s IndexingState) IsTerminal() bool {
	return s == IndexStateCompleted || s == IndexStateFailed || s == IndexStateCancelled
}

// String returns the string representation of the state.
func (s IndexingState) String() string {
	return string(s)
}

// =============================================================================
// Search Filters
// =============================================================================

// SearchFilters contains optional filters for search requests.
type SearchFilters struct {
	// Type filters results by document type.
	Type search.DocumentType `json:"type,omitempty"`

	// PathPrefix filters results to documents under this path prefix.
	PathPrefix string `json:"path_prefix,omitempty"`

	// Language filters results by programming language.
	Language string `json:"language,omitempty"`

	// ModifiedAfter filters to documents modified after this time.
	ModifiedAfter *time.Time `json:"modified_after,omitempty"`

	// ModifiedBefore filters to documents modified before this time.
	ModifiedBefore *time.Time `json:"modified_before,omitempty"`

	// FuzzyLevel sets the fuzzy matching level (0-2).
	FuzzyLevel int `json:"fuzzy_level,omitempty"`

	// IncludeHighlights enables result highlighting.
	IncludeHighlights bool `json:"include_highlights,omitempty"`
}

// IsEmpty returns true if no filters are set.
func (f *SearchFilters) IsEmpty() bool {
	if f == nil {
		return true
	}
	return f.Type == "" &&
		f.PathPrefix == "" &&
		f.Language == "" &&
		f.ModifiedAfter == nil &&
		f.ModifiedBefore == nil &&
		f.FuzzyLevel == 0 &&
		!f.IncludeHighlights
}

// ToSearchRequest converts filters to a search.SearchRequest.
func (f *SearchFilters) ToSearchRequest(query string, limit, offset int) *search.SearchRequest {
	req := &search.SearchRequest{
		Query:             query,
		Limit:             limit,
		Offset:            offset,
		IncludeHighlights: f.IncludeHighlights,
		FuzzyLevel:        f.FuzzyLevel,
	}

	if f.Type != "" {
		req.Type = f.Type
	}
	if f.PathPrefix != "" {
		req.PathFilter = f.PathPrefix
	}

	return req
}

// =============================================================================
// SearchRequest
// =============================================================================

// SearchRequest represents a request from an agent to perform a search.
type SearchRequest struct {
	// Query is the search query string.
	Query string `json:"query"`

	// Limit is the maximum number of results to return.
	// Defaults to 20 if not specified.
	Limit int `json:"limit,omitempty"`

	// Offset is the number of results to skip (for pagination).
	Offset int `json:"offset,omitempty"`

	// Filters contains optional search filters.
	Filters *SearchFilters `json:"filters,omitempty"`

	// RequestID is the unique identifier for this request.
	// Used for correlating requests and responses.
	RequestID string `json:"request_id"`

	// Timestamp is when the request was created.
	Timestamp time.Time `json:"timestamp"`
}

// NewSearchRequest creates a new SearchRequest with a generated request ID.
func NewSearchRequest(query string, limit int) *SearchRequest {
	return &SearchRequest{
		Query:     query,
		Limit:     limit,
		RequestID: uuid.New().String(),
		Timestamp: time.Now(),
	}
}

// Validate checks that the SearchRequest has valid parameters.
func (r *SearchRequest) Validate() error {
	if r.RequestID == "" {
		return ErrEmptyRequestID
	}
	if r.Query == "" {
		return ErrEmptyQuery
	}
	if r.Limit < 0 {
		return ErrInvalidLimit
	}
	return nil
}

// Normalize applies default values to unset fields.
func (r *SearchRequest) Normalize() {
	if r.Limit == 0 {
		r.Limit = search.DefaultLimit
	}
	if r.Timestamp.IsZero() {
		r.Timestamp = time.Now()
	}
	if r.RequestID == "" {
		r.RequestID = uuid.New().String()
	}
}

// WithFilters sets the search filters and returns the request.
func (r *SearchRequest) WithFilters(filters *SearchFilters) *SearchRequest {
	r.Filters = filters
	return r
}

// WithOffset sets the offset and returns the request.
func (r *SearchRequest) WithOffset(offset int) *SearchRequest {
	r.Offset = offset
	return r
}

// =============================================================================
// SearchResponse
// =============================================================================

// SearchResponse represents the response to a search request.
type SearchResponse struct {
	// RequestID is the ID of the corresponding request.
	RequestID string `json:"request_id"`

	// Results contains the search results.
	Results []search.ScoredDocument `json:"results"`

	// TotalHits is the total number of matching documents.
	TotalHits int64 `json:"total_hits"`

	// Duration is how long the search took.
	Duration time.Duration `json:"duration"`

	// Error contains an error message if the search failed.
	Error string `json:"error,omitempty"`

	// Timestamp is when the response was created.
	Timestamp time.Time `json:"timestamp"`
}

// NewSearchResponse creates a new SearchResponse for a successful search.
func NewSearchResponse(requestID string, results []search.ScoredDocument, totalHits int64, duration time.Duration) *SearchResponse {
	return &SearchResponse{
		RequestID: requestID,
		Results:   results,
		TotalHits: totalHits,
		Duration:  duration,
		Timestamp: time.Now(),
	}
}

// NewSearchErrorResponse creates a new SearchResponse for a failed search.
func NewSearchErrorResponse(requestID string, err error) *SearchResponse {
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	return &SearchResponse{
		RequestID: requestID,
		Results:   []search.ScoredDocument{},
		Error:     errMsg,
		Timestamp: time.Now(),
	}
}

// Success returns true if the search completed without errors.
func (r *SearchResponse) Success() bool {
	return r.Error == ""
}

// HasResults returns true if there are any results.
func (r *SearchResponse) HasResults() bool {
	return len(r.Results) > 0
}

// ResultCount returns the number of results in this response.
func (r *SearchResponse) ResultCount() int {
	return len(r.Results)
}

// =============================================================================
// IndexRequest
// =============================================================================

// IndexRequest represents a request from an agent to index files.
type IndexRequest struct {
	// Paths is the list of file paths to index.
	Paths []string `json:"paths"`

	// Force forces re-indexing even if files haven't changed.
	Force bool `json:"force,omitempty"`

	// RequestID is the unique identifier for this request.
	RequestID string `json:"request_id"`

	// Timestamp is when the request was created.
	Timestamp time.Time `json:"timestamp"`
}

// NewIndexRequest creates a new IndexRequest with a generated request ID.
func NewIndexRequest(paths []string) *IndexRequest {
	return &IndexRequest{
		Paths:     paths,
		RequestID: uuid.New().String(),
		Timestamp: time.Now(),
	}
}

// NewForceIndexRequest creates a new IndexRequest with force=true.
func NewForceIndexRequest(paths []string) *IndexRequest {
	req := NewIndexRequest(paths)
	req.Force = true
	return req
}

// Validate checks that the IndexRequest has valid parameters.
func (r *IndexRequest) Validate() error {
	if r.RequestID == "" {
		return ErrEmptyRequestID
	}
	if len(r.Paths) == 0 {
		return ErrEmptyPaths
	}
	return nil
}

// Normalize applies default values to unset fields.
func (r *IndexRequest) Normalize() {
	if r.Timestamp.IsZero() {
		r.Timestamp = time.Now()
	}
	if r.RequestID == "" {
		r.RequestID = uuid.New().String()
	}
}

// WithForce sets force=true and returns the request.
func (r *IndexRequest) WithForce() *IndexRequest {
	r.Force = true
	return r
}

// =============================================================================
// IndexStatus
// =============================================================================

// IndexStatus represents the status of an indexing operation.
type IndexStatus struct {
	// RequestID is the ID of the corresponding index request.
	RequestID string `json:"request_id"`

	// State is the current state of the indexing operation.
	State IndexingState `json:"state"`

	// Progress is the completion percentage (0.0 to 1.0).
	Progress float64 `json:"progress"`

	// ProcessedFiles is the number of files processed so far.
	ProcessedFiles int `json:"processed_files"`

	// TotalFiles is the total number of files to process.
	TotalFiles int `json:"total_files"`

	// IndexedCount is the number of successfully indexed files.
	IndexedCount int `json:"indexed_count"`

	// FailedCount is the number of files that failed to index.
	FailedCount int `json:"failed_count"`

	// Error contains an error message if the operation failed.
	Error string `json:"error,omitempty"`

	// Duration is how long the operation has been running.
	Duration time.Duration `json:"duration,omitempty"`

	// Timestamp is when this status was generated.
	Timestamp time.Time `json:"timestamp"`
}

// NewIndexStatus creates a new IndexStatus.
func NewIndexStatus(requestID string, state IndexingState) *IndexStatus {
	return &IndexStatus{
		RequestID: requestID,
		State:     state,
		Timestamp: time.Now(),
	}
}

// NewIndexStatusQueued creates a queued index status.
func NewIndexStatusQueued(requestID string, totalFiles int) *IndexStatus {
	status := NewIndexStatus(requestID, IndexStateQueued)
	status.TotalFiles = totalFiles
	return status
}

// NewIndexStatusRunning creates a running index status with progress.
func NewIndexStatusRunning(requestID string, processed, total int) *IndexStatus {
	status := NewIndexStatus(requestID, IndexStateRunning)
	status.ProcessedFiles = processed
	status.TotalFiles = total
	if total > 0 {
		status.Progress = float64(processed) / float64(total)
	}
	return status
}

// NewIndexStatusCompleted creates a completed index status.
func NewIndexStatusCompleted(requestID string, indexed, failed int, duration time.Duration) *IndexStatus {
	status := NewIndexStatus(requestID, IndexStateCompleted)
	status.IndexedCount = indexed
	status.FailedCount = failed
	status.ProcessedFiles = indexed + failed
	status.TotalFiles = indexed + failed
	status.Progress = 1.0
	status.Duration = duration
	return status
}

// NewIndexStatusFailed creates a failed index status.
func NewIndexStatusFailed(requestID string, err error) *IndexStatus {
	status := NewIndexStatus(requestID, IndexStateFailed)
	if err != nil {
		status.Error = err.Error()
	}
	return status
}

// Validate checks that the IndexStatus has valid parameters.
func (s *IndexStatus) Validate() error {
	if s.RequestID == "" {
		return ErrEmptyRequestID
	}
	if !s.State.IsValid() {
		return ErrInvalidState
	}
	return nil
}

// IsComplete returns true if the indexing operation is finished.
func (s *IndexStatus) IsComplete() bool {
	return s.State.IsTerminal()
}

// Success returns true if indexing completed without errors.
func (s *IndexStatus) Success() bool {
	return s.State == IndexStateCompleted && s.Error == ""
}

// SuccessRate returns the proportion of files successfully indexed.
func (s *IndexStatus) SuccessRate() float64 {
	total := s.IndexedCount + s.FailedCount
	if total == 0 {
		return 0.0
	}
	return float64(s.IndexedCount) / float64(total)
}

// =============================================================================
// Message Constructors
// =============================================================================

// NewSearchRequestMessage creates a messaging envelope for a search request.
func NewSearchRequestMessage(source, sessionID string, req *SearchRequest) *messaging.Message[*SearchRequest] {
	msg := messaging.NewWithSession(MessageTypeSearchRequest, source, sessionID, req)
	msg.CorrelationID = req.RequestID
	return msg
}

// NewSearchResponseMessage creates a messaging envelope for a search response.
func NewSearchResponseMessage(source, sessionID, correlationID string, resp *SearchResponse) *messaging.Message[*SearchResponse] {
	msg := messaging.NewWithSession(MessageTypeSearchResponse, source, sessionID, resp)
	msg.CorrelationID = correlationID
	return msg
}

// NewIndexRequestMessage creates a messaging envelope for an index request.
func NewIndexRequestMessage(source, sessionID string, req *IndexRequest) *messaging.Message[*IndexRequest] {
	msg := messaging.NewWithSession(MessageTypeIndexRequest, source, sessionID, req)
	msg.CorrelationID = req.RequestID
	return msg
}

// NewIndexStatusMessage creates a messaging envelope for an index status.
func NewIndexStatusMessage(source, sessionID, correlationID string, status *IndexStatus) *messaging.Message[*IndexStatus] {
	msg := messaging.NewWithSession(MessageTypeIndexStatus, source, sessionID, status)
	msg.CorrelationID = correlationID
	return msg
}

// =============================================================================
// JSON Serialization Helpers
// =============================================================================

// MarshalSearchRequest serializes a SearchRequest to JSON.
func MarshalSearchRequest(req *SearchRequest) ([]byte, error) {
	return json.Marshal(req)
}

// UnmarshalSearchRequest deserializes a SearchRequest from JSON.
func UnmarshalSearchRequest(data []byte) (*SearchRequest, error) {
	var req SearchRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, err
	}
	return &req, nil
}

// MarshalSearchResponse serializes a SearchResponse to JSON.
func MarshalSearchResponse(resp *SearchResponse) ([]byte, error) {
	return json.Marshal(resp)
}

// UnmarshalSearchResponse deserializes a SearchResponse from JSON.
func UnmarshalSearchResponse(data []byte) (*SearchResponse, error) {
	var resp SearchResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// MarshalIndexRequest serializes an IndexRequest to JSON.
func MarshalIndexRequest(req *IndexRequest) ([]byte, error) {
	return json.Marshal(req)
}

// UnmarshalIndexRequest deserializes an IndexRequest from JSON.
func UnmarshalIndexRequest(data []byte) (*IndexRequest, error) {
	var req IndexRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, err
	}
	return &req, nil
}

// MarshalIndexStatus serializes an IndexStatus to JSON.
func MarshalIndexStatus(status *IndexStatus) ([]byte, error) {
	return json.Marshal(status)
}

// UnmarshalIndexStatus deserializes an IndexStatus from JSON.
func UnmarshalIndexStatus(data []byte) (*IndexStatus, error) {
	var status IndexStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return nil, err
	}
	return &status, nil
}
