package knowledge

import (
	"encoding/json"
	"testing"
	"time"
)

// =============================================================================
// RelationType Tests
// =============================================================================

func TestRelationType_String(t *testing.T) {
	tests := []struct {
		name     string
		rt       RelationType
		expected string
	}{
		{"calls", RelCalls, "calls"},
		{"imports", RelImports, "imports"},
		{"extends", RelExtends, "extends"},
		{"implements", RelImplements, "implements"},
		{"uses", RelUses, "uses"},
		{"references", RelReferences, "references"},
		{"defines", RelDefines, "defines"},
		{"contains", RelContains, "contains"},
		{"invalid", RelationType(99), "relation_type(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.rt.String(); got != tt.expected {
				t.Errorf("String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestParseRelationType(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		expected  RelationType
		shouldErr bool
	}{
		{"calls", "calls", RelCalls, false},
		{"imports", "imports", RelImports, false},
		{"extends", "extends", RelExtends, false},
		{"implements", "implements", RelImplements, false},
		{"uses", "uses", RelUses, false},
		{"references", "references", RelReferences, false},
		{"defines", "defines", RelDefines, false},
		{"contains", "contains", RelContains, false},
		{"invalid", "invalid", RelationType(0), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseRelationType(tt.input)
			if tt.shouldErr && ok {
				t.Errorf("ParseRelationType() should have failed")
			}
			if !tt.shouldErr && !ok {
				t.Errorf("ParseRelationType() should have succeeded")
			}
			if !tt.shouldErr && got != tt.expected {
				t.Errorf("ParseRelationType() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestRelationType_IsValid(t *testing.T) {
	tests := []struct {
		name     string
		rt       RelationType
		expected bool
	}{
		{"valid_calls", RelCalls, true},
		{"valid_imports", RelImports, true},
		{"valid_extends", RelExtends, true},
		{"valid_implements", RelImplements, true},
		{"valid_uses", RelUses, true},
		{"valid_references", RelReferences, true},
		{"valid_defines", RelDefines, true},
		{"valid_contains", RelContains, true},
		{"invalid", RelationType(99), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.rt.IsValid(); got != tt.expected {
				t.Errorf("IsValid() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestRelationType_MarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		rt       RelationType
		expected string
	}{
		{"calls", RelCalls, `"calls"`},
		{"imports", RelImports, `"imports"`},
		{"extends", RelExtends, `"extends"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.rt)
			if err != nil {
				t.Fatalf("MarshalJSON() error = %v", err)
			}
			if string(data) != tt.expected {
				t.Errorf("MarshalJSON() = %v, want %v", string(data), tt.expected)
			}
		})
	}
}

func TestRelationType_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		expected  RelationType
		shouldErr bool
	}{
		{"string_calls", `"calls"`, RelCalls, false},
		{"string_imports", `"imports"`, RelImports, false},
		{"int_0", `0`, RelCalls, false},
		{"int_1", `1`, RelImports, false},
		{"invalid_string", `"invalid"`, RelationType(0), true},
		{"invalid_json", `{`, RelationType(0), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rt RelationType
			err := json.Unmarshal([]byte(tt.input), &rt)
			if tt.shouldErr && err == nil {
				t.Errorf("UnmarshalJSON() should have failed")
			}
			if !tt.shouldErr && err != nil {
				t.Errorf("UnmarshalJSON() error = %v", err)
			}
			if !tt.shouldErr && rt != tt.expected {
				t.Errorf("UnmarshalJSON() = %v, want %v", rt, tt.expected)
			}
		})
	}
}

// =============================================================================
// EvidenceSpan Tests
// =============================================================================

func TestEvidenceSpan_JSONRoundTrip(t *testing.T) {
	original := EvidenceSpan{
		FilePath:  "/path/to/file.go",
		StartLine: 10,
		EndLine:   15,
		Snippet:   "func example() {}",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal error = %v", err)
	}

	var decoded EvidenceSpan
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal error = %v", err)
	}

	if decoded.FilePath != original.FilePath {
		t.Errorf("FilePath = %v, want %v", decoded.FilePath, original.FilePath)
	}
	if decoded.StartLine != original.StartLine {
		t.Errorf("StartLine = %v, want %v", decoded.StartLine, original.StartLine)
	}
	if decoded.EndLine != original.EndLine {
		t.Errorf("EndLine = %v, want %v", decoded.EndLine, original.EndLine)
	}
	if decoded.Snippet != original.Snippet {
		t.Errorf("Snippet = %v, want %v", decoded.Snippet, original.Snippet)
	}
}

// =============================================================================
// ExtractedRelation Tests
// =============================================================================

func TestExtractedRelation_JSONRoundTrip(t *testing.T) {
	now := time.Now().UTC()

	sourceEntity := &ExtractedEntity{
		Name:     "SourceFunc",
		Kind:     EntityKindFunction,
		FilePath: "/path/to/source.go",
	}

	targetEntity := &ExtractedEntity{
		Name:     "TargetFunc",
		Kind:     EntityKindFunction,
		FilePath: "/path/to/target.go",
	}

	original := ExtractedRelation{
		SourceEntity: sourceEntity,
		TargetEntity: targetEntity,
		RelationType: RelCalls,
		Evidence: []EvidenceSpan{
			{
				FilePath:  "/path/to/source.go",
				StartLine: 10,
				EndLine:   10,
				Snippet:   "TargetFunc()",
			},
		},
		Confidence:  0.95,
		ExtractedAt: now,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal error = %v", err)
	}

	var decoded ExtractedRelation
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal error = %v", err)
	}

	if decoded.SourceEntity.Name != original.SourceEntity.Name {
		t.Errorf("SourceEntity.Name = %v, want %v", decoded.SourceEntity.Name, original.SourceEntity.Name)
	}
	if decoded.TargetEntity.Name != original.TargetEntity.Name {
		t.Errorf("TargetEntity.Name = %v, want %v", decoded.TargetEntity.Name, original.TargetEntity.Name)
	}
	if decoded.RelationType != original.RelationType {
		t.Errorf("RelationType = %v, want %v", decoded.RelationType, original.RelationType)
	}
	if len(decoded.Evidence) != len(original.Evidence) {
		t.Errorf("Evidence length = %v, want %v", len(decoded.Evidence), len(original.Evidence))
	}
	if decoded.Confidence != original.Confidence {
		t.Errorf("Confidence = %v, want %v", decoded.Confidence, original.Confidence)
	}
}

func TestValidRelationTypes(t *testing.T) {
	types := ValidRelationTypes()
	if len(types) != 8 {
		t.Errorf("ValidRelationTypes() length = %v, want 8", len(types))
	}

	expected := []RelationType{
		RelCalls,
		RelImports,
		RelExtends,
		RelImplements,
		RelUses,
		RelReferences,
		RelDefines,
		RelContains,
	}

	for i, rt := range expected {
		if types[i] != rt {
			t.Errorf("ValidRelationTypes()[%d] = %v, want %v", i, types[i], rt)
		}
	}
}
