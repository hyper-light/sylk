package query

import (
	"encoding/json"
	"testing"
	"time"
)

// =============================================================================
// FilterType Tests
// =============================================================================

func TestFilterType_String(t *testing.T) {
	tests := []struct {
		name     string
		ft       FilterType
		expected string
	}{
		{"domain", FilterDomain, "domain"},
		{"entity_type", FilterEntityType, "entity_type"},
		{"time_range", FilterTimeRange, "time_range"},
		{"unknown", FilterType(99), "filter_type(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ft.String(); got != tt.expected {
				t.Errorf("String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestParseFilterType(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		expected  FilterType
		expectOk  bool
	}{
		{"domain", "domain", FilterDomain, true},
		{"entity_type", "entity_type", FilterEntityType, true},
		{"time_range", "time_range", FilterTimeRange, true},
		{"invalid", "invalid", FilterType(0), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseFilterType(tt.value)
			if ok != tt.expectOk {
				t.Errorf("ParseFilterType() ok = %v, want %v", ok, tt.expectOk)
			}
			if ok && got != tt.expected {
				t.Errorf("ParseFilterType() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestFilterType_IsValid(t *testing.T) {
	tests := []struct {
		name     string
		ft       FilterType
		expected bool
	}{
		{"valid_domain", FilterDomain, true},
		{"valid_entity_type", FilterEntityType, true},
		{"valid_time_range", FilterTimeRange, true},
		{"invalid", FilterType(99), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ft.IsValid(); got != tt.expected {
				t.Errorf("IsValid() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestFilterType_MarshalJSON(t *testing.T) {
	ft := FilterDomain
	data, err := json.Marshal(ft)
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}

	expected := `"domain"`
	if string(data) != expected {
		t.Errorf("MarshalJSON() = %v, want %v", string(data), expected)
	}
}

func TestFilterType_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name      string
		json      string
		expected  FilterType
		expectErr bool
	}{
		{"string_domain", `"domain"`, FilterDomain, false},
		{"int_value", `0`, FilterDomain, false},
		{"invalid_string", `"invalid"`, FilterType(0), true},
		{"invalid_type", `[]`, FilterType(0), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ft FilterType
			err := json.Unmarshal([]byte(tt.json), &ft)
			if (err != nil) != tt.expectErr {
				t.Errorf("UnmarshalJSON() error = %v, expectErr %v", err, tt.expectErr)
			}
			if !tt.expectErr && ft != tt.expected {
				t.Errorf("UnmarshalJSON() = %v, want %v", ft, tt.expected)
			}
		})
	}
}

// =============================================================================
// HybridQuery Tests
// =============================================================================

func TestHybridQuery_Validate(t *testing.T) {
	tests := []struct {
		name      string
		query     *HybridQuery
		expectErr bool
	}{
		{
			name: "valid_text_query",
			query: &HybridQuery{
				TextQuery:  "test query",
				TextWeight: 1.0,
				Limit:      10,
				Timeout:    5 * time.Second,
			},
			expectErr: false,
		},
		{
			name: "valid_semantic_query",
			query: &HybridQuery{
				SemanticVector: []float32{0.1, 0.2, 0.3},
				SemanticWeight: 1.0,
				Limit:          10,
				Timeout:        5 * time.Second,
			},
			expectErr: false,
		},
		{
			name: "valid_graph_query",
			query: &HybridQuery{
				GraphPattern: &GraphPattern{},
				GraphWeight:  1.0,
				Limit:        10,
				Timeout:      5 * time.Second,
			},
			expectErr: false,
		},
		{
			name:      "no_modality",
			query:     &HybridQuery{Limit: 10, Timeout: 5 * time.Second},
			expectErr: true,
		},
		{
			name: "negative_limit",
			query: &HybridQuery{
				TextQuery: "test",
				Limit:     -1,
			},
			expectErr: true,
		},
		{
			name: "negative_timeout",
			query: &HybridQuery{
				TextQuery: "test",
				Timeout:   -1 * time.Second,
			},
			expectErr: true,
		},
		{
			name: "negative_weights",
			query: &HybridQuery{
				TextQuery:  "test",
				TextWeight: -0.5,
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.query.Validate()
			if (err != nil) != tt.expectErr {
				t.Errorf("Validate() error = %v, expectErr %v", err, tt.expectErr)
			}
		})
	}
}

func TestHybridQuery_HasTextQuery(t *testing.T) {
	tests := []struct {
		name     string
		query    *HybridQuery
		expected bool
	}{
		{"with_text", &HybridQuery{TextQuery: "test"}, true},
		{"without_text", &HybridQuery{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.query.HasTextQuery(); got != tt.expected {
				t.Errorf("HasTextQuery() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestHybridQuery_HasSemanticQuery(t *testing.T) {
	tests := []struct {
		name     string
		query    *HybridQuery
		expected bool
	}{
		{"with_vector", &HybridQuery{SemanticVector: []float32{0.1}}, true},
		{"without_vector", &HybridQuery{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.query.HasSemanticQuery(); got != tt.expected {
				t.Errorf("HasSemanticQuery() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestHybridQuery_HasGraphQuery(t *testing.T) {
	tests := []struct {
		name     string
		query    *HybridQuery
		expected bool
	}{
		{"with_pattern", &HybridQuery{GraphPattern: &GraphPattern{}}, true},
		{"without_pattern", &HybridQuery{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.query.HasGraphQuery(); got != tt.expected {
				t.Errorf("HasGraphQuery() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestHybridQuery_JSON(t *testing.T) {
	original := &HybridQuery{
		TextQuery:      "test query",
		SemanticVector: []float32{0.1, 0.2, 0.3},
		Filters: []QueryFilter{
			{Type: FilterDomain, Value: "test.com"},
		},
		TextWeight:     0.5,
		SemanticWeight: 0.5,
		Limit:          10,
		Timeout:        5 * time.Second,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var decoded HybridQuery
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if decoded.TextQuery != original.TextQuery {
		t.Errorf("TextQuery = %v, want %v", decoded.TextQuery, original.TextQuery)
	}
	if len(decoded.SemanticVector) != len(original.SemanticVector) {
		t.Errorf("SemanticVector length = %v, want %v",
			len(decoded.SemanticVector), len(original.SemanticVector))
	}
	if decoded.Limit != original.Limit {
		t.Errorf("Limit = %v, want %v", decoded.Limit, original.Limit)
	}
}

func TestQueryFilter_JSON(t *testing.T) {
	original := QueryFilter{
		Type:  FilterDomain,
		Value: "test.com",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var decoded QueryFilter
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if decoded.Type != original.Type {
		t.Errorf("Type = %v, want %v", decoded.Type, original.Type)
	}
}
