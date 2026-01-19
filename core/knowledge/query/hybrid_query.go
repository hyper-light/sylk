package query

import (
	"encoding/json"
	"fmt"
	"time"
)

// =============================================================================
// Filter Types
// =============================================================================

// FilterType represents the type of filter to apply to a query.
type FilterType int

const (
	FilterDomain     FilterType = 0
	FilterEntityType FilterType = 1
	FilterTimeRange  FilterType = 2
)

// ValidFilterTypes returns all valid FilterType values.
func ValidFilterTypes() []FilterType {
	return []FilterType{
		FilterDomain,
		FilterEntityType,
		FilterTimeRange,
	}
}

// IsValid returns true if the filter type is a recognized value.
func (ft FilterType) IsValid() bool {
	for _, valid := range ValidFilterTypes() {
		if ft == valid {
			return true
		}
	}
	return false
}

func (ft FilterType) String() string {
	switch ft {
	case FilterDomain:
		return "domain"
	case FilterEntityType:
		return "entity_type"
	case FilterTimeRange:
		return "time_range"
	default:
		return fmt.Sprintf("filter_type(%d)", ft)
	}
}

func ParseFilterType(value string) (FilterType, bool) {
	switch value {
	case "domain":
		return FilterDomain, true
	case "entity_type":
		return FilterEntityType, true
	case "time_range":
		return FilterTimeRange, true
	default:
		return FilterType(0), false
	}
}

func (ft FilterType) MarshalJSON() ([]byte, error) {
	return json.Marshal(ft.String())
}

func (ft *FilterType) UnmarshalJSON(data []byte) error {
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		if parsed, ok := ParseFilterType(asString); ok {
			*ft = parsed
			return nil
		}
		return fmt.Errorf("invalid filter type: %s", asString)
	}

	var asInt int
	if err := json.Unmarshal(data, &asInt); err == nil {
		*ft = FilterType(asInt)
		return nil
	}

	return fmt.Errorf("invalid filter type")
}

// =============================================================================
// Query Filter Structure
// =============================================================================

// QueryFilter represents a filter constraint for a hybrid query.
type QueryFilter struct {
	Type  FilterType `json:"type"`
	Value any        `json:"value"`
}

// =============================================================================
// Hybrid Query Structure
// =============================================================================

// HybridQuery represents a multi-modal query combining text, semantic,
// and graph-based search capabilities.
type HybridQuery struct {
	// Text search query for Bleve full-text search
	TextQuery string `json:"text_query,omitempty"`

	// Semantic vector for HNSW similarity search
	SemanticVector []float32 `json:"semantic_vector,omitempty"`

	// Graph pattern for traversal-based search
	GraphPattern *GraphPattern `json:"graph_pattern,omitempty"`

	// Filters to constrain query results
	Filters []QueryFilter `json:"filters,omitempty"`

	// Weights for fusion scoring (should sum to 1.0)
	TextWeight     float64 `json:"text_weight"`
	SemanticWeight float64 `json:"semantic_weight"`
	GraphWeight    float64 `json:"graph_weight"`

	// Query execution parameters
	Limit   int           `json:"limit"`
	Timeout time.Duration `json:"timeout"`
}

// Validate checks if the query is well-formed.
func (hq *HybridQuery) Validate() error {
	if err := hq.validateModality(); err != nil {
		return err
	}
	return hq.validateParameters()
}

func (hq *HybridQuery) validateModality() error {
	hasQuery := hq.TextQuery != "" || len(hq.SemanticVector) > 0 || hq.GraphPattern != nil
	if !hasQuery {
		return fmt.Errorf("at least one query modality required")
	}
	return nil
}

func (hq *HybridQuery) validateParameters() error {
	if hq.Limit < 0 {
		return fmt.Errorf("limit must be non-negative")
	}
	if hq.Timeout < 0 {
		return fmt.Errorf("timeout must be non-negative")
	}
	totalWeight := hq.TextWeight + hq.SemanticWeight + hq.GraphWeight
	if totalWeight < 0 {
		return fmt.Errorf("weights must be non-negative")
	}
	return nil
}

// HasTextQuery returns true if text search is enabled.
func (hq *HybridQuery) HasTextQuery() bool {
	return hq.TextQuery != ""
}

// HasSemanticQuery returns true if semantic search is enabled.
func (hq *HybridQuery) HasSemanticQuery() bool {
	return len(hq.SemanticVector) > 0
}

// HasGraphQuery returns true if graph traversal is enabled.
func (hq *HybridQuery) HasGraphQuery() bool {
	return hq.GraphPattern != nil
}
