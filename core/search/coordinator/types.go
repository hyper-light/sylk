// Package coordinator provides hybrid search coordination combining Bleve full-text
// search with VectorDB semantic search using configurable fusion methods.
package coordinator

import (
	"context"
	"errors"
	"time"

	"github.com/adalundhe/sylk/core/search"
)

// =============================================================================
// Constants
// =============================================================================

const (
	// DefaultLimit is the default number of results to return.
	DefaultLimit = 20

	// MaxLimit is the maximum allowed limit for search results.
	MaxLimit = 100

	// DefaultBleveWeight is the default weight for Bleve results.
	DefaultBleveWeight = 0.5

	// DefaultVectorWeight is the default weight for Vector results.
	DefaultVectorWeight = 0.5

	// DefaultRRFK is the default k parameter for RRF fusion.
	DefaultRRFK = 60

	// DefaultTimeout is the default search timeout.
	DefaultTimeout = 5 * time.Second

	// MaxFusionOverfetch is the maximum results to fetch for fusion.
	// This prevents unbounded memory allocation when fusing results.
	MaxFusionOverfetch = 1000

	// DefaultFusionMultiplier is the multiplier applied to limit for fusion.
	// We fetch more results than requested to have sufficient candidates for fusion.
	DefaultFusionMultiplier = 2
)

// =============================================================================
// FusionMethod Enum
// =============================================================================

// FusionMethod represents the algorithm used to combine search results.
type FusionMethod string

const (
	// FusionRRF uses Reciprocal Rank Fusion for combining results.
	FusionRRF FusionMethod = "rrf"

	// FusionLinear uses linear weighted score combination.
	FusionLinear FusionMethod = "linear"

	// FusionMax takes the maximum score from any source.
	FusionMax FusionMethod = "max"
)

// validFusionMethods contains all valid fusion methods.
var validFusionMethods = map[FusionMethod]struct{}{
	FusionRRF:    {},
	FusionLinear: {},
	FusionMax:    {},
}

// IsValid returns true if the fusion method is recognized.
func (fm FusionMethod) IsValid() bool {
	_, ok := validFusionMethods[fm]
	return ok
}

// String returns the string representation of the fusion method.
func (fm FusionMethod) String() string {
	return string(fm)
}

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrEmptyQuery indicates an empty search query was provided.
	ErrEmptyQuery = errors.New("search query cannot be empty")

	// ErrInvalidLimit indicates the limit is out of valid range.
	ErrInvalidLimit = errors.New("limit must be between 1 and 100")

	// ErrInvalidWeight indicates a weight value is out of valid range.
	ErrInvalidWeight = errors.New("weights must be between 0 and 1")

	// ErrInvalidFusionMethod indicates an unrecognized fusion method.
	ErrInvalidFusionMethod = errors.New("invalid fusion method")

	// ErrBothSearchersFailed indicates both Bleve and Vector searches failed.
	ErrBothSearchersFailed = errors.New("both search backends failed")

	// ErrSearchTimeout indicates the search operation timed out.
	ErrSearchTimeout = errors.New("search operation timed out")

	// ErrCoordinatorClosed indicates the coordinator has been closed.
	ErrCoordinatorClosed = errors.New("coordinator is closed")
)

// =============================================================================
// Filter Types
// =============================================================================

// SearchFilter defines filtering criteria for search results.
type SearchFilter struct {
	// Types filters by document types.
	Types []search.DocumentType `json:"types,omitempty"`

	// Languages filters by programming languages.
	Languages []string `json:"languages,omitempty"`

	// PathPrefix filters by path prefix.
	PathPrefix string `json:"path_prefix,omitempty"`

	// MinScore filters results below this score threshold.
	MinScore float64 `json:"min_score,omitempty"`
}

// IsEmpty returns true if no filters are set.
func (f *SearchFilter) IsEmpty() bool {
	if f == nil {
		return true
	}
	return len(f.Types) == 0 &&
		len(f.Languages) == 0 &&
		f.PathPrefix == "" &&
		f.MinScore == 0
}

// =============================================================================
// HybridSearchRequest
// =============================================================================

// HybridSearchRequest defines parameters for a hybrid search operation.
type HybridSearchRequest struct {
	// Query is the search query string (required).
	Query string `json:"query"`

	// QueryVector is the optional embedding vector for semantic search.
	// If nil, the coordinator will generate embeddings from Query.
	QueryVector []float32 `json:"query_vector,omitempty"`

	// BleveWeight is the weight for Bleve full-text results (0-1).
	BleveWeight float64 `json:"bleve_weight"`

	// VectorWeight is the weight for Vector semantic results (0-1).
	VectorWeight float64 `json:"vector_weight"`

	// Limit is the maximum number of results to return.
	Limit int `json:"limit"`

	// Filters contains optional filtering criteria.
	Filters *SearchFilter `json:"filters,omitempty"`

	// FusionMethod specifies the result fusion algorithm.
	FusionMethod FusionMethod `json:"fusion_method"`

	// Timeout is the maximum time to wait for results.
	Timeout time.Duration `json:"timeout,omitempty"`

	// IncludeBleveResults includes raw Bleve results in the response.
	IncludeBleveResults bool `json:"include_bleve_results,omitempty"`

	// IncludeVectorResults includes raw Vector results in the response.
	IncludeVectorResults bool `json:"include_vector_results,omitempty"`
}

// Validate checks that the request has valid parameters.
func (r *HybridSearchRequest) Validate() error {
	if r.Query == "" {
		return ErrEmptyQuery
	}
	if err := r.validateLimit(); err != nil {
		return err
	}
	if err := r.validateWeights(); err != nil {
		return err
	}
	return r.validateFusionMethod()
}

// validateLimit checks the limit is within valid range.
func (r *HybridSearchRequest) validateLimit() error {
	if r.Limit < 0 || r.Limit > MaxLimit {
		return ErrInvalidLimit
	}
	return nil
}

// validateWeights checks that weights are in valid range.
func (r *HybridSearchRequest) validateWeights() error {
	if r.BleveWeight < 0 || r.BleveWeight > 1 {
		return ErrInvalidWeight
	}
	if r.VectorWeight < 0 || r.VectorWeight > 1 {
		return ErrInvalidWeight
	}
	return nil
}

// validateFusionMethod checks that fusion method is valid.
func (r *HybridSearchRequest) validateFusionMethod() error {
	if r.FusionMethod != "" && !r.FusionMethod.IsValid() {
		return ErrInvalidFusionMethod
	}
	return nil
}

// Normalize applies default values to unset fields.
func (r *HybridSearchRequest) Normalize() {
	if r.Limit == 0 {
		r.Limit = DefaultLimit
	}
	if r.BleveWeight == 0 && r.VectorWeight == 0 {
		r.BleveWeight = DefaultBleveWeight
		r.VectorWeight = DefaultVectorWeight
	}
	if r.FusionMethod == "" {
		r.FusionMethod = FusionRRF
	}
	if r.Timeout == 0 {
		r.Timeout = DefaultTimeout
	}
}

// ValidateAndNormalize validates and normalizes the request.
func (r *HybridSearchRequest) ValidateAndNormalize() error {
	r.Normalize()
	return r.Validate()
}

// =============================================================================
// HybridSearchResult
// =============================================================================

// SearchMetadata contains metadata about the search operation.
type SearchMetadata struct {
	// TotalTime is the total time taken for the search.
	TotalTime time.Duration `json:"total_time"`

	// BleveTime is the time taken for Bleve search.
	BleveTime time.Duration `json:"bleve_time,omitempty"`

	// VectorTime is the time taken for Vector search.
	VectorTime time.Duration `json:"vector_time,omitempty"`

	// FusionTime is the time taken for result fusion.
	FusionTime time.Duration `json:"fusion_time,omitempty"`

	// BleveHits is the number of hits from Bleve search.
	BleveHits int `json:"bleve_hits"`

	// VectorHits is the number of hits from Vector search.
	VectorHits int `json:"vector_hits"`

	// FusedHits is the number of fused results.
	FusedHits int `json:"fused_hits"`

	// BleveFailed indicates if Bleve search failed.
	BleveFailed bool `json:"bleve_failed,omitempty"`

	// VectorFailed indicates if Vector search failed.
	VectorFailed bool `json:"vector_failed,omitempty"`

	// BleveError contains the Bleve error message if failed.
	BleveError string `json:"bleve_error,omitempty"`

	// VectorError contains the Vector error message if failed.
	VectorError string `json:"vector_error,omitempty"`
}

// HybridSearchResult contains the results of a hybrid search operation.
type HybridSearchResult struct {
	// BleveResults contains raw results from Bleve search.
	BleveResults []search.ScoredDocument `json:"bleve_results,omitempty"`

	// VectorResults contains raw results from Vector search.
	VectorResults []search.ScoredDocument `json:"vector_results,omitempty"`

	// FusedResults contains the combined and ranked results.
	FusedResults []search.ScoredDocument `json:"fused_results"`

	// Metadata contains search operation metadata.
	Metadata SearchMetadata `json:"metadata"`

	// FusionMethod is the fusion method used.
	FusionMethod FusionMethod `json:"fusion_method"`

	// Query is the original query string.
	Query string `json:"query"`
}

// HasResults returns true if there are any fused results.
func (r *HybridSearchResult) HasResults() bool {
	return len(r.FusedResults) > 0
}

// TopN returns the top N fused results.
func (r *HybridSearchResult) TopN(n int) []search.ScoredDocument {
	if n <= 0 {
		return nil
	}
	if n >= len(r.FusedResults) {
		return r.FusedResults
	}
	return r.FusedResults[:n]
}

// FilterByScore returns fused results with score >= minScore.
func (r *HybridSearchResult) FilterByScore(minScore float64) []search.ScoredDocument {
	filtered := make([]search.ScoredDocument, 0, len(r.FusedResults))
	for _, doc := range r.FusedResults {
		if doc.Score >= minScore {
			filtered = append(filtered, doc)
		}
	}
	return filtered
}

// TotalSources returns the count of sources that returned results.
func (r *HybridSearchResult) TotalSources() int {
	count := 0
	if len(r.BleveResults) > 0 || r.Metadata.BleveHits > 0 {
		count++
	}
	if len(r.VectorResults) > 0 || r.Metadata.VectorHits > 0 {
		count++
	}
	return count
}

// IsPartialResult returns true if one backend failed but results are available.
func (r *HybridSearchResult) IsPartialResult() bool {
	hasFailure := r.Metadata.BleveFailed || r.Metadata.VectorFailed
	return hasFailure && r.HasResults()
}

// =============================================================================
// Searcher Interfaces
// =============================================================================

// BleveSearcher defines the interface for Bleve full-text search.
type BleveSearcher interface {
	// Search performs a full-text search.
	Search(ctx context.Context, req *search.SearchRequest) (*search.SearchResult, error)

	// IsOpen returns true if the searcher is ready for queries.
	IsOpen() bool
}

// VectorSearcher defines the interface for vector semantic search.
type VectorSearcher interface {
	// Search performs a semantic search with the query vector.
	Search(ctx context.Context, query []float32, limit int) ([]ScoredVectorResult, error)
}

// EmbeddingGenerator defines the interface for generating query embeddings.
type EmbeddingGenerator interface {
	// Generate creates an embedding vector for the given text.
	Generate(ctx context.Context, text string) ([]float32, error)
}

// ScoredVectorResult represents a result from vector search.
type ScoredVectorResult struct {
	// ID is the document identifier.
	ID string `json:"id"`

	// Score is the similarity score.
	Score float64 `json:"score"`

	// Path is the document path.
	Path string `json:"path,omitempty"`

	// Content is the document content.
	Content string `json:"content,omitempty"`

	// Metadata contains additional result metadata.
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// ToScoredDocument converts a ScoredVectorResult to a search.ScoredDocument.
func (r *ScoredVectorResult) ToScoredDocument() search.ScoredDocument {
	return search.ScoredDocument{
		Document: search.Document{
			ID:      r.ID,
			Path:    r.Path,
			Content: r.Content,
		},
		Score: r.Score,
	}
}

// =============================================================================
// Configuration
// =============================================================================

// CoordinatorConfig holds configuration for the SearchCoordinator.
type CoordinatorConfig struct {
	// DefaultTimeout is the default search timeout.
	DefaultTimeout time.Duration

	// RRFK is the k parameter for RRF fusion (default 60).
	RRFK int

	// EnableFallback enables fallback to single source on failure.
	EnableFallback bool

	// MaxConcurrentSearches limits concurrent search operations.
	MaxConcurrentSearches int
}

// DefaultCoordinatorConfig returns sensible default configuration.
func DefaultCoordinatorConfig() CoordinatorConfig {
	return CoordinatorConfig{
		DefaultTimeout:        DefaultTimeout,
		RRFK:                  DefaultRRFK,
		EnableFallback:        true,
		MaxConcurrentSearches: 10,
	}
}

// Validate checks that the configuration is valid.
func (c *CoordinatorConfig) Validate() error {
	if c.DefaultTimeout <= 0 {
		c.DefaultTimeout = DefaultTimeout
	}
	if c.RRFK <= 0 {
		c.RRFK = DefaultRRFK
	}
	if c.MaxConcurrentSearches <= 0 {
		c.MaxConcurrentSearches = 10
	}
	return nil
}
