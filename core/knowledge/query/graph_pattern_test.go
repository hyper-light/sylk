package query

import (
	"encoding/json"
	"testing"
)

// =============================================================================
// Direction Tests
// =============================================================================

func TestDirection_String(t *testing.T) {
	tests := []struct {
		name     string
		dir      Direction
		expected string
	}{
		{"outgoing", DirectionOutgoing, "outgoing"},
		{"incoming", DirectionIncoming, "incoming"},
		{"both", DirectionBoth, "both"},
		{"unknown", Direction(99), "direction(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.dir.String(); got != tt.expected {
				t.Errorf("String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestParseDirection(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		expected  Direction
		expectOk  bool
	}{
		{"outgoing", "outgoing", DirectionOutgoing, true},
		{"incoming", "incoming", DirectionIncoming, true},
		{"both", "both", DirectionBoth, true},
		{"invalid", "invalid", Direction(0), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseDirection(tt.value)
			if ok != tt.expectOk {
				t.Errorf("ParseDirection() ok = %v, want %v", ok, tt.expectOk)
			}
			if ok && got != tt.expected {
				t.Errorf("ParseDirection() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDirection_IsValid(t *testing.T) {
	tests := []struct {
		name     string
		dir      Direction
		expected bool
	}{
		{"valid_outgoing", DirectionOutgoing, true},
		{"valid_incoming", DirectionIncoming, true},
		{"valid_both", DirectionBoth, true},
		{"invalid", Direction(99), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.dir.IsValid(); got != tt.expected {
				t.Errorf("IsValid() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDirection_MarshalJSON(t *testing.T) {
	dir := DirectionOutgoing
	data, err := json.Marshal(dir)
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}

	expected := `"outgoing"`
	if string(data) != expected {
		t.Errorf("MarshalJSON() = %v, want %v", string(data), expected)
	}
}

func TestDirection_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name      string
		json      string
		expected  Direction
		expectErr bool
	}{
		{"string_outgoing", `"outgoing"`, DirectionOutgoing, false},
		{"int_value", `0`, DirectionOutgoing, false},
		{"invalid_string", `"invalid"`, Direction(0), true},
		{"invalid_type", `[]`, Direction(0), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var dir Direction
			err := json.Unmarshal([]byte(tt.json), &dir)
			if (err != nil) != tt.expectErr {
				t.Errorf("UnmarshalJSON() error = %v, expectErr %v", err, tt.expectErr)
			}
			if !tt.expectErr && dir != tt.expected {
				t.Errorf("UnmarshalJSON() = %v, want %v", dir, tt.expected)
			}
		})
	}
}

// =============================================================================
// NodeMatcher Tests
// =============================================================================

func TestNodeMatcher_Matches(t *testing.T) {
	entityType := "function"
	tests := []struct {
		name     string
		matcher  *NodeMatcher
		expected bool
	}{
		{
			name:     "with_entity_type",
			matcher:  &NodeMatcher{EntityType: &entityType},
			expected: true,
		},
		{
			name:     "with_name_pattern",
			matcher:  &NodeMatcher{NamePattern: "test*"},
			expected: true,
		},
		{
			name:     "with_properties",
			matcher:  &NodeMatcher{Properties: map[string]any{"key": "value"}},
			expected: true,
		},
		{
			name:     "empty",
			matcher:  &NodeMatcher{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.matcher.Matches(); got != tt.expected {
				t.Errorf("Matches() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestNodeMatcher_JSON(t *testing.T) {
	entityType := "function"
	original := &NodeMatcher{
		EntityType:  &entityType,
		NamePattern: "test*",
		Properties:  map[string]any{"key": "value"},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var decoded NodeMatcher
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if *decoded.EntityType != *original.EntityType {
		t.Errorf("EntityType = %v, want %v", *decoded.EntityType, *original.EntityType)
	}
	if decoded.NamePattern != original.NamePattern {
		t.Errorf("NamePattern = %v, want %v", decoded.NamePattern, original.NamePattern)
	}
}

// =============================================================================
// TraversalStep Tests
// =============================================================================

func TestTraversalStep_Validate(t *testing.T) {
	tests := []struct {
		name      string
		step      *TraversalStep
		expectErr bool
	}{
		{
			name: "valid_step",
			step: &TraversalStep{
				EdgeType:  "calls",
				Direction: DirectionOutgoing,
				MaxHops:   1,
			},
			expectErr: false,
		},
		{
			name: "unlimited_hops",
			step: &TraversalStep{
				Direction: DirectionOutgoing,
				MaxHops:   -1,
			},
			expectErr: false,
		},
		{
			name: "invalid_direction",
			step: &TraversalStep{
				Direction: Direction(99),
				MaxHops:   1,
			},
			expectErr: true,
		},
		{
			name: "invalid_max_hops",
			step: &TraversalStep{
				Direction: DirectionOutgoing,
				MaxHops:   -2,
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.step.Validate()
			if (err != nil) != tt.expectErr {
				t.Errorf("Validate() error = %v, expectErr %v", err, tt.expectErr)
			}
		})
	}
}

func TestTraversalStep_JSON(t *testing.T) {
	entityType := "variable"
	original := &TraversalStep{
		EdgeType:  "reads",
		Direction: DirectionIncoming,
		TargetMatcher: &NodeMatcher{
			EntityType: &entityType,
		},
		MaxHops: 2,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var decoded TraversalStep
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if decoded.EdgeType != original.EdgeType {
		t.Errorf("EdgeType = %v, want %v", decoded.EdgeType, original.EdgeType)
	}
	if decoded.Direction != original.Direction {
		t.Errorf("Direction = %v, want %v", decoded.Direction, original.Direction)
	}
	if decoded.MaxHops != original.MaxHops {
		t.Errorf("MaxHops = %v, want %v", decoded.MaxHops, original.MaxHops)
	}
}

// =============================================================================
// GraphPattern Tests
// =============================================================================

func TestGraphPattern_Validate(t *testing.T) {
	tests := []struct {
		name      string
		pattern   *GraphPattern
		expectErr bool
	}{
		{
			name: "valid_pattern",
			pattern: &GraphPattern{
				Traversals: []TraversalStep{
					{Direction: DirectionOutgoing, MaxHops: 1},
				},
			},
			expectErr: false,
		},
		{
			name:      "empty_pattern",
			pattern:   &GraphPattern{},
			expectErr: false,
		},
		{
			name: "invalid_step",
			pattern: &GraphPattern{
				Traversals: []TraversalStep{
					{Direction: Direction(99), MaxHops: 1},
				},
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.pattern.Validate()
			if (err != nil) != tt.expectErr {
				t.Errorf("Validate() error = %v, expectErr %v", err, tt.expectErr)
			}
		})
	}
}

func TestGraphPattern_IsEmpty(t *testing.T) {
	entityType := "function"
	tests := []struct {
		name     string
		pattern  *GraphPattern
		expected bool
	}{
		{
			name: "with_start_node",
			pattern: &GraphPattern{
				StartNode: &NodeMatcher{EntityType: &entityType},
			},
			expected: false,
		},
		{
			name: "with_traversals",
			pattern: &GraphPattern{
				Traversals: []TraversalStep{
					{Direction: DirectionOutgoing, MaxHops: 1},
				},
			},
			expected: false,
		},
		{
			name:     "empty",
			pattern:  &GraphPattern{},
			expected: true,
		},
		{
			name: "empty_start_node",
			pattern: &GraphPattern{
				StartNode: &NodeMatcher{},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.pattern.IsEmpty(); got != tt.expected {
				t.Errorf("IsEmpty() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestGraphPattern_TraversalCount(t *testing.T) {
	tests := []struct {
		name     string
		pattern  *GraphPattern
		expected int
	}{
		{
			name: "with_traversals",
			pattern: &GraphPattern{
				Traversals: []TraversalStep{
					{Direction: DirectionOutgoing, MaxHops: 1},
					{Direction: DirectionIncoming, MaxHops: 1},
				},
			},
			expected: 2,
		},
		{
			name:     "no_traversals",
			pattern:  &GraphPattern{},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.pattern.TraversalCount(); got != tt.expected {
				t.Errorf("TraversalCount() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestGraphPattern_JSON(t *testing.T) {
	entityType := "function"
	original := &GraphPattern{
		StartNode: &NodeMatcher{
			EntityType: &entityType,
		},
		Traversals: []TraversalStep{
			{
				EdgeType:  "calls",
				Direction: DirectionOutgoing,
				MaxHops:   2,
			},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var decoded GraphPattern
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if *decoded.StartNode.EntityType != *original.StartNode.EntityType {
		t.Errorf("StartNode.EntityType = %v, want %v",
			*decoded.StartNode.EntityType, *original.StartNode.EntityType)
	}
	if len(decoded.Traversals) != len(original.Traversals) {
		t.Errorf("Traversals length = %v, want %v",
			len(decoded.Traversals), len(original.Traversals))
	}
}
