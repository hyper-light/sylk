package query

import (
	"encoding/json"
	"testing"
)

// =============================================================================
// Source Tests
// =============================================================================

func TestSource_String(t *testing.T) {
	tests := []struct {
		name     string
		source   Source
		expected string
	}{
		{"bleve", SourceBleve, "bleve"},
		{"hnsw", SourceHNSW, "hnsw"},
		{"graph", SourceGraph, "graph"},
		{"combined", SourceCombined, "combined"},
		{"unknown", Source(99), "source(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.source.String(); got != tt.expected {
				t.Errorf("String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestParseSource(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		expected  Source
		expectOk  bool
	}{
		{"bleve", "bleve", SourceBleve, true},
		{"hnsw", "hnsw", SourceHNSW, true},
		{"graph", "graph", SourceGraph, true},
		{"combined", "combined", SourceCombined, true},
		{"invalid", "invalid", Source(0), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseSource(tt.value)
			if ok != tt.expectOk {
				t.Errorf("ParseSource() ok = %v, want %v", ok, tt.expectOk)
			}
			if ok && got != tt.expected {
				t.Errorf("ParseSource() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestSource_IsValid(t *testing.T) {
	tests := []struct {
		name     string
		source   Source
		expected bool
	}{
		{"valid_bleve", SourceBleve, true},
		{"valid_hnsw", SourceHNSW, true},
		{"valid_graph", SourceGraph, true},
		{"valid_combined", SourceCombined, true},
		{"invalid", Source(99), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.source.IsValid(); got != tt.expected {
				t.Errorf("IsValid() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestSource_MarshalJSON(t *testing.T) {
	source := SourceBleve
	data, err := json.Marshal(source)
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}

	expected := `"bleve"`
	if string(data) != expected {
		t.Errorf("MarshalJSON() = %v, want %v", string(data), expected)
	}
}

func TestSource_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name      string
		json      string
		expected  Source
		expectErr bool
	}{
		{"string_bleve", `"bleve"`, SourceBleve, false},
		{"int_value", `0`, SourceBleve, false},
		{"invalid_string", `"invalid"`, Source(0), true},
		{"invalid_type", `[]`, Source(0), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var source Source
			err := json.Unmarshal([]byte(tt.json), &source)
			if (err != nil) != tt.expectErr {
				t.Errorf("UnmarshalJSON() error = %v, expectErr %v", err, tt.expectErr)
			}
			if !tt.expectErr && source != tt.expected {
				t.Errorf("UnmarshalJSON() = %v, want %v", source, tt.expected)
			}
		})
	}
}

// =============================================================================
// EdgeMatch Tests
// =============================================================================

func TestEdgeMatch_JSON(t *testing.T) {
	original := EdgeMatch{
		EdgeID:   12345,
		EdgeType: "calls",
		Weight:   0.85,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var decoded EdgeMatch
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if decoded.EdgeID != original.EdgeID {
		t.Errorf("EdgeID = %v, want %v", decoded.EdgeID, original.EdgeID)
	}
	if decoded.EdgeType != original.EdgeType {
		t.Errorf("EdgeType = %v, want %v", decoded.EdgeType, original.EdgeType)
	}
	if decoded.Weight != original.Weight {
		t.Errorf("Weight = %v, want %v", decoded.Weight, original.Weight)
	}
}

// =============================================================================
// HybridResult Tests
// =============================================================================

func TestHybridResult_Validate(t *testing.T) {
	tests := []struct {
		name      string
		result    *HybridResult
		expectErr bool
	}{
		{
			name: "valid_result",
			result: &HybridResult{
				ID:      "test-id",
				Content: "test content",
				Score:   0.95,
				Source:  SourceCombined,
			},
			expectErr: false,
		},
		{
			name: "missing_id",
			result: &HybridResult{
				Content: "test content",
				Score:   0.95,
				Source:  SourceCombined,
			},
			expectErr: true,
		},
		{
			name: "negative_score",
			result: &HybridResult{
				ID:      "test-id",
				Content: "test content",
				Score:   -0.5,
				Source:  SourceCombined,
			},
			expectErr: true,
		},
		{
			name: "invalid_source",
			result: &HybridResult{
				ID:      "test-id",
				Content: "test content",
				Score:   0.95,
				Source:  Source(99),
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.result.Validate()
			if (err != nil) != tt.expectErr {
				t.Errorf("Validate() error = %v, expectErr %v", err, tt.expectErr)
			}
		})
	}
}

func TestHybridResult_HasGraphData(t *testing.T) {
	tests := []struct {
		name     string
		result   *HybridResult
		expected bool
	}{
		{
			name: "with_matched_edges",
			result: &HybridResult{
				MatchedEdges: []EdgeMatch{
					{EdgeID: 1, EdgeType: "calls", Weight: 0.8},
				},
			},
			expected: true,
		},
		{
			name: "with_traversal_path",
			result: &HybridResult{
				TraversalPath: []string{"node1", "node2"},
			},
			expected: true,
		},
		{
			name:     "without_graph_data",
			result:   &HybridResult{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.result.HasGraphData(); got != tt.expected {
				t.Errorf("HasGraphData() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestHybridResult_HasTextScore(t *testing.T) {
	tests := []struct {
		name     string
		result   *HybridResult
		expected bool
	}{
		{"with_score", &HybridResult{TextScore: 0.8}, true},
		{"without_score", &HybridResult{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.result.HasTextScore(); got != tt.expected {
				t.Errorf("HasTextScore() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestHybridResult_HasSemanticScore(t *testing.T) {
	tests := []struct {
		name     string
		result   *HybridResult
		expected bool
	}{
		{"with_score", &HybridResult{SemanticScore: 0.9}, true},
		{"without_score", &HybridResult{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.result.HasSemanticScore(); got != tt.expected {
				t.Errorf("HasSemanticScore() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestHybridResult_HasGraphScore(t *testing.T) {
	tests := []struct {
		name     string
		result   *HybridResult
		expected bool
	}{
		{"with_score", &HybridResult{GraphScore: 0.7}, true},
		{"without_score", &HybridResult{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.result.HasGraphScore(); got != tt.expected {
				t.Errorf("HasGraphScore() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestHybridResult_EdgeCount(t *testing.T) {
	tests := []struct {
		name     string
		result   *HybridResult
		expected int
	}{
		{
			name: "with_edges",
			result: &HybridResult{
				MatchedEdges: []EdgeMatch{
					{EdgeID: 1, EdgeType: "calls", Weight: 0.8},
					{EdgeID: 2, EdgeType: "reads", Weight: 0.7},
				},
			},
			expected: 2,
		},
		{
			name:     "without_edges",
			result:   &HybridResult{},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.result.EdgeCount(); got != tt.expected {
				t.Errorf("EdgeCount() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestHybridResult_PathLength(t *testing.T) {
	tests := []struct {
		name     string
		result   *HybridResult
		expected int
	}{
		{
			name: "with_path",
			result: &HybridResult{
				TraversalPath: []string{"node1", "node2", "node3"},
			},
			expected: 3,
		},
		{
			name:     "without_path",
			result:   &HybridResult{},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.result.PathLength(); got != tt.expected {
				t.Errorf("PathLength() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestHybridResult_JSON(t *testing.T) {
	original := &HybridResult{
		ID:            "result-123",
		Content:       "test content",
		Score:         0.95,
		TextScore:     0.8,
		SemanticScore: 0.9,
		GraphScore:    0.7,
		MatchedEdges: []EdgeMatch{
			{EdgeID: 1, EdgeType: "calls", Weight: 0.85},
		},
		TraversalPath: []string{"node1", "node2"},
		Source:        SourceCombined,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var decoded HybridResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if decoded.ID != original.ID {
		t.Errorf("ID = %v, want %v", decoded.ID, original.ID)
	}
	if decoded.Content != original.Content {
		t.Errorf("Content = %v, want %v", decoded.Content, original.Content)
	}
	if decoded.Score != original.Score {
		t.Errorf("Score = %v, want %v", decoded.Score, original.Score)
	}
	if decoded.TextScore != original.TextScore {
		t.Errorf("TextScore = %v, want %v", decoded.TextScore, original.TextScore)
	}
	if decoded.SemanticScore != original.SemanticScore {
		t.Errorf("SemanticScore = %v, want %v", decoded.SemanticScore, original.SemanticScore)
	}
	if decoded.GraphScore != original.GraphScore {
		t.Errorf("GraphScore = %v, want %v", decoded.GraphScore, original.GraphScore)
	}
	if len(decoded.MatchedEdges) != len(original.MatchedEdges) {
		t.Errorf("MatchedEdges length = %v, want %v",
			len(decoded.MatchedEdges), len(original.MatchedEdges))
	}
	if len(decoded.TraversalPath) != len(original.TraversalPath) {
		t.Errorf("TraversalPath length = %v, want %v",
			len(decoded.TraversalPath), len(original.TraversalPath))
	}
	if decoded.Source != original.Source {
		t.Errorf("Source = %v, want %v", decoded.Source, original.Source)
	}
}

func TestHybridResult_CompleteWorkflow(t *testing.T) {
	result := &HybridResult{
		ID:            "workflow-test",
		Content:       "function processData() { ... }",
		Score:         0.88,
		TextScore:     0.75,
		SemanticScore: 0.92,
		GraphScore:    0.80,
		MatchedEdges: []EdgeMatch{
			{EdgeID: 100, EdgeType: "calls", Weight: 0.9},
			{EdgeID: 101, EdgeType: "reads", Weight: 0.7},
		},
		TraversalPath: []string{"moduleA", "processData", "validateInput"},
		Source:        SourceCombined,
	}

	if err := result.Validate(); err != nil {
		t.Errorf("Validate() failed: %v", err)
	}

	if !result.HasGraphData() {
		t.Error("Expected HasGraphData() to be true")
	}

	if !result.HasTextScore() {
		t.Error("Expected HasTextScore() to be true")
	}

	if !result.HasSemanticScore() {
		t.Error("Expected HasSemanticScore() to be true")
	}

	if !result.HasGraphScore() {
		t.Error("Expected HasGraphScore() to be true")
	}

	if result.EdgeCount() != 2 {
		t.Errorf("EdgeCount() = %v, want 2", result.EdgeCount())
	}

	if result.PathLength() != 3 {
		t.Errorf("PathLength() = %v, want 3", result.PathLength())
	}
}
