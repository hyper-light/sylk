package query

import (
	"encoding/json"
	"fmt"
)

// =============================================================================
// Source Types
// =============================================================================

// Source represents the origin of a query result.
type Source int

const (
	SourceBleve    Source = 0
	SourceHNSW     Source = 1
	SourceGraph    Source = 2
	SourceCombined Source = 3
)

// ValidSources returns all valid Source values.
func ValidSources() []Source {
	return []Source{
		SourceBleve,
		SourceHNSW,
		SourceGraph,
		SourceCombined,
	}
}

// IsValid returns true if the source is a recognized value.
func (s Source) IsValid() bool {
	for _, valid := range ValidSources() {
		if s == valid {
			return true
		}
	}
	return false
}

func (s Source) String() string {
	switch s {
	case SourceBleve:
		return "bleve"
	case SourceHNSW:
		return "hnsw"
	case SourceGraph:
		return "graph"
	case SourceCombined:
		return "combined"
	default:
		return fmt.Sprintf("source(%d)", s)
	}
}

func ParseSource(value string) (Source, bool) {
	switch value {
	case "bleve":
		return SourceBleve, true
	case "hnsw":
		return SourceHNSW, true
	case "graph":
		return SourceGraph, true
	case "combined":
		return SourceCombined, true
	default:
		return Source(0), false
	}
}

func (s Source) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

func (s *Source) UnmarshalJSON(data []byte) error {
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		if parsed, ok := ParseSource(asString); ok {
			*s = parsed
			return nil
		}
		return fmt.Errorf("invalid source: %s", asString)
	}

	var asInt int
	if err := json.Unmarshal(data, &asInt); err == nil {
		*s = Source(asInt)
		return nil
	}

	return fmt.Errorf("invalid source")
}

// =============================================================================
// Edge Match Structure
// =============================================================================

// EdgeMatch represents a matched edge in a graph traversal result.
type EdgeMatch struct {
	EdgeID   int64   `json:"edge_id"`
	EdgeType string  `json:"edge_type"`
	Weight   float64 `json:"weight"`
}

// =============================================================================
// Hybrid Result Structure
// =============================================================================

// HybridResult represents a single result from a hybrid query execution.
type HybridResult struct {
	// Result identification
	ID      string `json:"id"`
	Content string `json:"content"`

	// Overall score (fused from component scores)
	Score float64 `json:"score"`

	// Component score breakdown
	TextScore     float64 `json:"text_score"`
	SemanticScore float64 `json:"semantic_score"`
	GraphScore    float64 `json:"graph_score"`

	// Graph-specific match information
	MatchedEdges  []EdgeMatch `json:"matched_edges,omitempty"`
	TraversalPath []string    `json:"traversal_path,omitempty"`

	// Source attribution
	Source Source `json:"source"`

	// Memory integration fields (populated by HybridQueryWithMemory)
	MemoryActivation float64 `json:"memory_activation,omitempty"`
	MemoryFactor     float64 `json:"memory_factor,omitempty"`
}

// Validate checks if the result is well-formed.
func (hr *HybridResult) Validate() error {
	if hr.ID == "" {
		return fmt.Errorf("result ID is required")
	}

	if hr.Score < 0 {
		return fmt.Errorf("score must be non-negative")
	}

	if !hr.Source.IsValid() {
		return fmt.Errorf("invalid source: %v", hr.Source)
	}

	return nil
}

// HasGraphData returns true if the result includes graph traversal data.
func (hr *HybridResult) HasGraphData() bool {
	return len(hr.MatchedEdges) > 0 || len(hr.TraversalPath) > 0
}

// HasTextScore returns true if the result includes a text search score.
func (hr *HybridResult) HasTextScore() bool {
	return hr.TextScore > 0
}

// HasSemanticScore returns true if the result includes a semantic search score.
func (hr *HybridResult) HasSemanticScore() bool {
	return hr.SemanticScore > 0
}

// HasGraphScore returns true if the result includes a graph traversal score.
func (hr *HybridResult) HasGraphScore() bool {
	return hr.GraphScore > 0
}

// EdgeCount returns the number of matched edges.
func (hr *HybridResult) EdgeCount() int {
	return len(hr.MatchedEdges)
}

// PathLength returns the length of the traversal path.
func (hr *HybridResult) PathLength() int {
	return len(hr.TraversalPath)
}
