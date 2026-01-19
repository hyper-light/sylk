package coordinator

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/search"
)

// =============================================================================
// FusionMethod Tests
// =============================================================================

func TestFusionMethod_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method FusionMethod
		valid  bool
	}{
		{FusionRRF, true},
		{FusionLinear, true},
		{FusionMax, true},
		{"invalid", false},
		{"", false},
		{"RRF", false}, // case sensitive
	}

	for _, tt := range tests {
		t.Run(string(tt.method), func(t *testing.T) {
			t.Parallel()
			if got := tt.method.IsValid(); got != tt.valid {
				t.Errorf("IsValid() = %v, want %v", got, tt.valid)
			}
		})
	}
}

func TestFusionMethod_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method FusionMethod
		want   string
	}{
		{FusionRRF, "rrf"},
		{FusionLinear, "linear"},
		{FusionMax, "max"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			if got := tt.method.String(); got != tt.want {
				t.Errorf("String() = %v, want %v", got, tt.want)
			}
		})
	}
}

// =============================================================================
// SearchFilter Tests
// =============================================================================

func TestSearchFilter_IsEmpty(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		filter *SearchFilter
		want   bool
	}{
		{
			name:   "nil filter",
			filter: nil,
			want:   true,
		},
		{
			name:   "empty filter",
			filter: &SearchFilter{},
			want:   true,
		},
		{
			name: "with types",
			filter: &SearchFilter{
				Types: []search.DocumentType{search.DocTypeSourceCode},
			},
			want: false,
		},
		{
			name: "with languages",
			filter: &SearchFilter{
				Languages: []string{"go"},
			},
			want: false,
		},
		{
			name: "with path prefix",
			filter: &SearchFilter{
				PathPrefix: "/src",
			},
			want: false,
		},
		{
			name: "with min score",
			filter: &SearchFilter{
				MinScore: 0.5,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.filter.IsEmpty(); got != tt.want {
				t.Errorf("IsEmpty() = %v, want %v", got, tt.want)
			}
		})
	}
}

// =============================================================================
// HybridSearchRequest Tests
// =============================================================================

func TestHybridSearchRequest_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		req     HybridSearchRequest
		wantErr error
	}{
		{
			name: "valid request",
			req: HybridSearchRequest{
				Query:        "test query",
				BleveWeight:  0.5,
				VectorWeight: 0.5,
				Limit:        20,
				FusionMethod: FusionRRF,
			},
			wantErr: nil,
		},
		{
			name: "empty query",
			req: HybridSearchRequest{
				Query: "",
			},
			wantErr: ErrEmptyQuery,
		},
		{
			name: "negative limit",
			req: HybridSearchRequest{
				Query: "test",
				Limit: -1,
			},
			wantErr: ErrInvalidLimit,
		},
		{
			name: "limit too high",
			req: HybridSearchRequest{
				Query: "test",
				Limit: MaxLimit + 1,
			},
			wantErr: ErrInvalidLimit,
		},
		{
			name: "negative bleve weight",
			req: HybridSearchRequest{
				Query:       "test",
				BleveWeight: -0.1,
			},
			wantErr: ErrInvalidWeight,
		},
		{
			name: "bleve weight over 1",
			req: HybridSearchRequest{
				Query:       "test",
				BleveWeight: 1.1,
			},
			wantErr: ErrInvalidWeight,
		},
		{
			name: "negative vector weight",
			req: HybridSearchRequest{
				Query:        "test",
				VectorWeight: -0.1,
			},
			wantErr: ErrInvalidWeight,
		},
		{
			name: "vector weight over 1",
			req: HybridSearchRequest{
				Query:        "test",
				VectorWeight: 1.1,
			},
			wantErr: ErrInvalidWeight,
		},
		{
			name: "invalid fusion method",
			req: HybridSearchRequest{
				Query:        "test",
				FusionMethod: "invalid",
			},
			wantErr: ErrInvalidFusionMethod,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.req.Validate()
			if err != tt.wantErr {
				t.Errorf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestHybridSearchRequest_Normalize(t *testing.T) {
	t.Parallel()

	req := HybridSearchRequest{
		Query: "test",
	}

	req.Normalize()

	if req.Limit != DefaultLimit {
		t.Errorf("Limit = %d, want %d", req.Limit, DefaultLimit)
	}
	if req.BleveWeight != DefaultBleveWeight {
		t.Errorf("BleveWeight = %f, want %f", req.BleveWeight, DefaultBleveWeight)
	}
	if req.VectorWeight != DefaultVectorWeight {
		t.Errorf("VectorWeight = %f, want %f", req.VectorWeight, DefaultVectorWeight)
	}
	if req.FusionMethod != FusionRRF {
		t.Errorf("FusionMethod = %s, want %s", req.FusionMethod, FusionRRF)
	}
	if req.Timeout != DefaultTimeout {
		t.Errorf("Timeout = %v, want %v", req.Timeout, DefaultTimeout)
	}
}

func TestHybridSearchRequest_Normalize_PreservesSetValues(t *testing.T) {
	t.Parallel()

	req := HybridSearchRequest{
		Query:        "test",
		Limit:        50,
		BleveWeight:  0.7,
		VectorWeight: 0.3,
		FusionMethod: FusionLinear,
		Timeout:      10 * time.Second,
	}

	req.Normalize()

	if req.Limit != 50 {
		t.Errorf("Limit changed from 50 to %d", req.Limit)
	}
	if req.BleveWeight != 0.7 {
		t.Errorf("BleveWeight changed from 0.7 to %f", req.BleveWeight)
	}
	if req.VectorWeight != 0.3 {
		t.Errorf("VectorWeight changed from 0.3 to %f", req.VectorWeight)
	}
	if req.FusionMethod != FusionLinear {
		t.Errorf("FusionMethod changed from linear to %s", req.FusionMethod)
	}
	if req.Timeout != 10*time.Second {
		t.Errorf("Timeout changed from 10s to %v", req.Timeout)
	}
}

func TestHybridSearchRequest_ValidateAndNormalize(t *testing.T) {
	t.Parallel()

	req := HybridSearchRequest{
		Query: "test",
	}

	err := req.ValidateAndNormalize()
	if err != nil {
		t.Errorf("ValidateAndNormalize() error = %v, want nil", err)
	}

	// Check normalization was applied
	if req.Limit != DefaultLimit {
		t.Errorf("Limit not normalized: got %d, want %d", req.Limit, DefaultLimit)
	}
}

func TestHybridSearchRequest_ValidateAndNormalize_Error(t *testing.T) {
	t.Parallel()

	req := HybridSearchRequest{
		Query: "", // Invalid
	}

	err := req.ValidateAndNormalize()
	if err != ErrEmptyQuery {
		t.Errorf("ValidateAndNormalize() error = %v, want %v", err, ErrEmptyQuery)
	}
}

// =============================================================================
// HybridSearchResult Tests
// =============================================================================

func TestHybridSearchResult_HasResults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		result HybridSearchResult
		want   bool
	}{
		{
			name:   "empty result",
			result: HybridSearchResult{},
			want:   false,
		},
		{
			name: "with fused results",
			result: HybridSearchResult{
				FusedResults: []search.ScoredDocument{{Document: search.Document{ID: "1"}}},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.result.HasResults(); got != tt.want {
				t.Errorf("HasResults() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHybridSearchResult_TopN(t *testing.T) {
	t.Parallel()

	result := HybridSearchResult{
		FusedResults: []search.ScoredDocument{
			{Document: search.Document{ID: "1"}, Score: 0.9},
			{Document: search.Document{ID: "2"}, Score: 0.8},
			{Document: search.Document{ID: "3"}, Score: 0.7},
		},
	}

	tests := []struct {
		name string
		n    int
		want int
	}{
		{"zero", 0, 0},
		{"negative", -1, 0},
		{"less than total", 2, 2},
		{"equal to total", 3, 3},
		{"more than total", 5, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := result.TopN(tt.n)
			if len(got) != tt.want {
				t.Errorf("TopN(%d) returned %d results, want %d", tt.n, len(got), tt.want)
			}
		})
	}
}

func TestHybridSearchResult_FilterByScore(t *testing.T) {
	t.Parallel()

	result := HybridSearchResult{
		FusedResults: []search.ScoredDocument{
			{Document: search.Document{ID: "1"}, Score: 0.9},
			{Document: search.Document{ID: "2"}, Score: 0.7},
			{Document: search.Document{ID: "3"}, Score: 0.5},
			{Document: search.Document{ID: "4"}, Score: 0.3},
		},
	}

	tests := []struct {
		minScore float64
		want     int
	}{
		{0.0, 4},
		{0.5, 3},
		{0.7, 2},
		{0.9, 1},
		{1.0, 0},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			t.Parallel()
			got := result.FilterByScore(tt.minScore)
			if len(got) != tt.want {
				t.Errorf("FilterByScore(%f) returned %d, want %d", tt.minScore, len(got), tt.want)
			}
		})
	}
}

func TestHybridSearchResult_TotalSources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		result HybridSearchResult
		want   int
	}{
		{
			name:   "no sources",
			result: HybridSearchResult{},
			want:   0,
		},
		{
			name: "bleve only",
			result: HybridSearchResult{
				Metadata: SearchMetadata{BleveHits: 5},
			},
			want: 1,
		},
		{
			name: "vector only",
			result: HybridSearchResult{
				Metadata: SearchMetadata{VectorHits: 5},
			},
			want: 1,
		},
		{
			name: "both sources",
			result: HybridSearchResult{
				Metadata: SearchMetadata{BleveHits: 5, VectorHits: 3},
			},
			want: 2,
		},
		{
			name: "with raw results",
			result: HybridSearchResult{
				BleveResults:  []search.ScoredDocument{{Document: search.Document{ID: "1"}}},
				VectorResults: []search.ScoredDocument{{Document: search.Document{ID: "2"}}},
			},
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.result.TotalSources(); got != tt.want {
				t.Errorf("TotalSources() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestHybridSearchResult_IsPartialResult(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		result HybridSearchResult
		want   bool
	}{
		{
			name:   "no failures, no results",
			result: HybridSearchResult{},
			want:   false,
		},
		{
			name: "no failures, has results",
			result: HybridSearchResult{
				FusedResults: []search.ScoredDocument{{Document: search.Document{ID: "1"}}},
			},
			want: false,
		},
		{
			name: "bleve failed, no results",
			result: HybridSearchResult{
				Metadata: SearchMetadata{BleveFailed: true},
			},
			want: false,
		},
		{
			name: "bleve failed, has results",
			result: HybridSearchResult{
				FusedResults: []search.ScoredDocument{{Document: search.Document{ID: "1"}}},
				Metadata:     SearchMetadata{BleveFailed: true},
			},
			want: true,
		},
		{
			name: "vector failed, has results",
			result: HybridSearchResult{
				FusedResults: []search.ScoredDocument{{Document: search.Document{ID: "1"}}},
				Metadata:     SearchMetadata{VectorFailed: true},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.result.IsPartialResult(); got != tt.want {
				t.Errorf("IsPartialResult() = %v, want %v", got, tt.want)
			}
		})
	}
}

// =============================================================================
// ScoredVectorResult Tests
// =============================================================================

func TestScoredVectorResult_ToScoredDocument(t *testing.T) {
	t.Parallel()

	vr := ScoredVectorResult{
		ID:      "doc-123",
		Score:   0.85,
		Path:    "/path/to/file.go",
		Content: "package main",
	}

	sd := vr.ToScoredDocument()

	if sd.ID != vr.ID {
		t.Errorf("ID = %s, want %s", sd.ID, vr.ID)
	}
	if sd.Score != vr.Score {
		t.Errorf("Score = %f, want %f", sd.Score, vr.Score)
	}
	if sd.Path != vr.Path {
		t.Errorf("Path = %s, want %s", sd.Path, vr.Path)
	}
	if sd.Content != vr.Content {
		t.Errorf("Content = %s, want %s", sd.Content, vr.Content)
	}
}

// =============================================================================
// CoordinatorConfig Tests
// =============================================================================

func TestDefaultCoordinatorConfig(t *testing.T) {
	t.Parallel()

	config := DefaultCoordinatorConfig()

	if config.DefaultTimeout != DefaultTimeout {
		t.Errorf("DefaultTimeout = %v, want %v", config.DefaultTimeout, DefaultTimeout)
	}
	if config.RRFK != DefaultRRFK {
		t.Errorf("RRFK = %d, want %d", config.RRFK, DefaultRRFK)
	}
	if !config.EnableFallback {
		t.Error("EnableFallback should be true by default")
	}
	if config.MaxConcurrentSearches != 10 {
		t.Errorf("MaxConcurrentSearches = %d, want 10", config.MaxConcurrentSearches)
	}
}

func TestCoordinatorConfig_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config CoordinatorConfig
		check  func(t *testing.T, c *CoordinatorConfig)
	}{
		{
			name:   "zero timeout gets default",
			config: CoordinatorConfig{},
			check: func(t *testing.T, c *CoordinatorConfig) {
				if c.DefaultTimeout != DefaultTimeout {
					t.Errorf("DefaultTimeout = %v, want %v", c.DefaultTimeout, DefaultTimeout)
				}
			},
		},
		{
			name:   "zero RRFK gets default",
			config: CoordinatorConfig{},
			check: func(t *testing.T, c *CoordinatorConfig) {
				if c.RRFK != DefaultRRFK {
					t.Errorf("RRFK = %d, want %d", c.RRFK, DefaultRRFK)
				}
			},
		},
		{
			name:   "zero max concurrent gets default",
			config: CoordinatorConfig{},
			check: func(t *testing.T, c *CoordinatorConfig) {
				if c.MaxConcurrentSearches != 10 {
					t.Errorf("MaxConcurrentSearches = %d, want 10", c.MaxConcurrentSearches)
				}
			},
		},
		{
			name: "valid config unchanged",
			config: CoordinatorConfig{
				DefaultTimeout:        10 * time.Second,
				RRFK:                  100,
				MaxConcurrentSearches: 20,
			},
			check: func(t *testing.T, c *CoordinatorConfig) {
				if c.DefaultTimeout != 10*time.Second {
					t.Errorf("DefaultTimeout changed to %v", c.DefaultTimeout)
				}
				if c.RRFK != 100 {
					t.Errorf("RRFK changed to %d", c.RRFK)
				}
				if c.MaxConcurrentSearches != 20 {
					t.Errorf("MaxConcurrentSearches changed to %d", c.MaxConcurrentSearches)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			config := tt.config
			err := config.Validate()
			if err != nil {
				t.Errorf("Validate() error = %v", err)
			}
			tt.check(t, &config)
		})
	}
}

// =============================================================================
// Error Tests
// =============================================================================

func TestErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err     error
		wantMsg string
	}{
		{ErrEmptyQuery, "search query cannot be empty"},
		{ErrInvalidLimit, "limit must be between 1 and 100"},
		{ErrInvalidWeight, "weights must be between 0 and 1"},
		{ErrInvalidFusionMethod, "invalid fusion method"},
		{ErrBothSearchersFailed, "both search backends failed"},
		{ErrSearchTimeout, "search operation timed out"},
		{ErrCoordinatorClosed, "coordinator is closed"},
	}

	for _, tt := range tests {
		t.Run(tt.wantMsg, func(t *testing.T) {
			t.Parallel()
			if tt.err.Error() != tt.wantMsg {
				t.Errorf("error = %q, want %q", tt.err.Error(), tt.wantMsg)
			}
		})
	}
}

// =============================================================================
// Constants Tests
// =============================================================================

func TestConstants(t *testing.T) {
	t.Parallel()

	if DefaultLimit != 20 {
		t.Errorf("DefaultLimit = %d, want 20", DefaultLimit)
	}
	if MaxLimit != 100 {
		t.Errorf("MaxLimit = %d, want 100", MaxLimit)
	}
	if DefaultBleveWeight != 0.5 {
		t.Errorf("DefaultBleveWeight = %f, want 0.5", DefaultBleveWeight)
	}
	if DefaultVectorWeight != 0.5 {
		t.Errorf("DefaultVectorWeight = %f, want 0.5", DefaultVectorWeight)
	}
	if DefaultRRFK != 60 {
		t.Errorf("DefaultRRFK = %d, want 60", DefaultRRFK)
	}
	if DefaultTimeout != 5*time.Second {
		t.Errorf("DefaultTimeout = %v, want 5s", DefaultTimeout)
	}
}
