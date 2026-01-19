package inference

import (
	"strings"
	"testing"
	"time"
)

// =============================================================================
// InferenceResult.Validate Tests
// =============================================================================

func TestInferenceResult_Validate_Valid(t *testing.T) {
	result := InferenceResult{
		DerivedEdge: DerivedEdge{
			SourceID: "nodeA",
			TargetID: "nodeB",
			EdgeType: "calls",
		},
		RuleID: "rule1",
		Evidence: []EvidenceEdge{
			{SourceID: "nodeA", TargetID: "nodeX", EdgeType: "imports"},
			{SourceID: "nodeX", TargetID: "nodeB", EdgeType: "exports"},
		},
		DerivedAt:  time.Now(),
		Confidence: 0.95,
		Provenance: "Test provenance",
	}

	if err := result.Validate(); err != nil {
		t.Errorf("Valid result should pass: %v", err)
	}
}

func TestInferenceResult_Validate_EmptySourceID(t *testing.T) {
	result := InferenceResult{
		DerivedEdge: DerivedEdge{
			SourceID: "",
			TargetID: "nodeB",
			EdgeType: "calls",
		},
		RuleID: "rule1",
		Evidence: []EvidenceEdge{
			{SourceID: "nodeA", TargetID: "nodeB", EdgeType: "calls"},
		},
		Confidence: 0.9,
	}

	err := result.Validate()
	if err == nil {
		t.Error("Should fail with empty source_id")
	}
	if !strings.Contains(err.Error(), "source_id") {
		t.Errorf("Error should mention source_id: %v", err)
	}
}

func TestInferenceResult_Validate_EmptyTargetID(t *testing.T) {
	result := InferenceResult{
		DerivedEdge: DerivedEdge{
			SourceID: "nodeA",
			TargetID: "",
			EdgeType: "calls",
		},
		RuleID: "rule1",
		Evidence: []EvidenceEdge{
			{SourceID: "nodeA", TargetID: "nodeB", EdgeType: "calls"},
		},
		Confidence: 0.9,
	}

	err := result.Validate()
	if err == nil {
		t.Error("Should fail with empty target_id")
	}
	if !strings.Contains(err.Error(), "target_id") {
		t.Errorf("Error should mention target_id: %v", err)
	}
}

func TestInferenceResult_Validate_EmptyEdgeType(t *testing.T) {
	result := InferenceResult{
		DerivedEdge: DerivedEdge{
			SourceID: "nodeA",
			TargetID: "nodeB",
			EdgeType: "",
		},
		RuleID: "rule1",
		Evidence: []EvidenceEdge{
			{SourceID: "nodeA", TargetID: "nodeB", EdgeType: "calls"},
		},
		Confidence: 0.9,
	}

	err := result.Validate()
	if err == nil {
		t.Error("Should fail with empty edge_type")
	}
	if !strings.Contains(err.Error(), "edge_type") {
		t.Errorf("Error should mention edge_type: %v", err)
	}
}

func TestInferenceResult_Validate_EmptyRuleID(t *testing.T) {
	result := InferenceResult{
		DerivedEdge: DerivedEdge{
			SourceID: "nodeA",
			TargetID: "nodeB",
			EdgeType: "calls",
		},
		RuleID: "",
		Evidence: []EvidenceEdge{
			{SourceID: "nodeA", TargetID: "nodeB", EdgeType: "calls"},
		},
		Confidence: 0.9,
	}

	err := result.Validate()
	if err == nil {
		t.Error("Should fail with empty rule_id")
	}
	if !strings.Contains(err.Error(), "rule_id") {
		t.Errorf("Error should mention rule_id: %v", err)
	}
}

func TestInferenceResult_Validate_EmptyEvidence(t *testing.T) {
	result := InferenceResult{
		DerivedEdge: DerivedEdge{
			SourceID: "nodeA",
			TargetID: "nodeB",
			EdgeType: "calls",
		},
		RuleID:     "rule1",
		Evidence:   []EvidenceEdge{},
		Confidence: 0.9,
	}

	err := result.Validate()
	if err == nil {
		t.Error("Should fail with empty evidence")
	}
	if !strings.Contains(err.Error(), "evidence") {
		t.Errorf("Error should mention evidence: %v", err)
	}
}

func TestInferenceResult_Validate_ConfidenceTooLow(t *testing.T) {
	result := InferenceResult{
		DerivedEdge: DerivedEdge{
			SourceID: "nodeA",
			TargetID: "nodeB",
			EdgeType: "calls",
		},
		RuleID: "rule1",
		Evidence: []EvidenceEdge{
			{SourceID: "nodeA", TargetID: "nodeB", EdgeType: "calls"},
		},
		Confidence: -0.1,
	}

	err := result.Validate()
	if err == nil {
		t.Error("Should fail with confidence < 0")
	}
	if !strings.Contains(err.Error(), "confidence") {
		t.Errorf("Error should mention confidence: %v", err)
	}
}

func TestInferenceResult_Validate_ConfidenceTooHigh(t *testing.T) {
	result := InferenceResult{
		DerivedEdge: DerivedEdge{
			SourceID: "nodeA",
			TargetID: "nodeB",
			EdgeType: "calls",
		},
		RuleID: "rule1",
		Evidence: []EvidenceEdge{
			{SourceID: "nodeA", TargetID: "nodeB", EdgeType: "calls"},
		},
		Confidence: 1.5,
	}

	err := result.Validate()
	if err == nil {
		t.Error("Should fail with confidence > 1")
	}
	if !strings.Contains(err.Error(), "confidence") {
		t.Errorf("Error should mention confidence: %v", err)
	}
}

func TestInferenceResult_Validate_ConfidenceBoundaries(t *testing.T) {
	tests := []struct {
		confidence float64
		valid      bool
	}{
		{0.0, true},
		{0.5, true},
		{1.0, true},
		{-0.0001, false},
		{1.0001, false},
	}

	for _, tt := range tests {
		result := InferenceResult{
			DerivedEdge: DerivedEdge{
				SourceID: "nodeA",
				TargetID: "nodeB",
				EdgeType: "calls",
			},
			RuleID: "rule1",
			Evidence: []EvidenceEdge{
				{SourceID: "nodeA", TargetID: "nodeB", EdgeType: "calls"},
			},
			Confidence: tt.confidence,
		}

		err := result.Validate()
		if tt.valid && err != nil {
			t.Errorf("Confidence %f should be valid: %v", tt.confidence, err)
		}
		if !tt.valid && err == nil {
			t.Errorf("Confidence %f should be invalid", tt.confidence)
		}
	}
}

// =============================================================================
// NewInferenceResult Tests
// =============================================================================

func TestNewInferenceResult(t *testing.T) {
	derived := DerivedEdge{
		SourceID: "funcA",
		TargetID: "funcB",
		EdgeType: "calls",
	}
	evidence := []EvidenceEdge{
		{SourceID: "funcA", TargetID: "pkg1", EdgeType: "imports"},
		{SourceID: "pkg1", TargetID: "funcB", EdgeType: "exports"},
	}

	before := time.Now()
	result := NewInferenceResult(derived, "rule1", evidence, 0.85)
	after := time.Now()

	if result.DerivedEdge.SourceID != "funcA" {
		t.Errorf("DerivedEdge.SourceID = %s, want funcA", result.DerivedEdge.SourceID)
	}
	if result.RuleID != "rule1" {
		t.Errorf("RuleID = %s, want rule1", result.RuleID)
	}
	if len(result.Evidence) != 2 {
		t.Errorf("Evidence length = %d, want 2", len(result.Evidence))
	}
	if result.Confidence != 0.85 {
		t.Errorf("Confidence = %f, want 0.85", result.Confidence)
	}
	if result.DerivedAt.Before(before) || result.DerivedAt.After(after) {
		t.Errorf("DerivedAt not in expected range: %v", result.DerivedAt)
	}
	if result.Provenance != "" {
		t.Errorf("Provenance should be empty initially, got %q", result.Provenance)
	}
}

// =============================================================================
// InferenceResult.GenerateProvenance Tests
// =============================================================================

func TestInferenceResult_GenerateProvenance_Simple(t *testing.T) {
	result := InferenceResult{
		DerivedEdge: DerivedEdge{
			SourceID: "funcA",
			TargetID: "funcB",
			EdgeType: "calls",
		},
		RuleID: "rule1",
		Evidence: []EvidenceEdge{
			{SourceID: "funcA", TargetID: "pkg1", EdgeType: "imports"},
		},
	}

	result.GenerateProvenance("Transitivity")

	if result.Provenance == "" {
		t.Error("Provenance should not be empty")
	}
	if !strings.Contains(result.Provenance, "funcA") {
		t.Errorf("Provenance should contain funcA: %s", result.Provenance)
	}
	if !strings.Contains(result.Provenance, "funcB") {
		t.Errorf("Provenance should contain funcB: %s", result.Provenance)
	}
	if !strings.Contains(result.Provenance, "Transitivity") {
		t.Errorf("Provenance should contain rule name: %s", result.Provenance)
	}
	if !strings.Contains(result.Provenance, "imports") {
		t.Errorf("Provenance should contain evidence: %s", result.Provenance)
	}
}

func TestInferenceResult_GenerateProvenance_MultipleEvidence(t *testing.T) {
	result := InferenceResult{
		DerivedEdge: DerivedEdge{
			SourceID: "nodeA",
			TargetID: "nodeC",
			EdgeType: "transitive_calls",
		},
		RuleID: "rule1",
		Evidence: []EvidenceEdge{
			{SourceID: "nodeA", TargetID: "nodeB", EdgeType: "calls"},
			{SourceID: "nodeB", TargetID: "nodeC", EdgeType: "calls"},
		},
	}

	result.GenerateProvenance("Call Transitivity")

	if !strings.Contains(result.Provenance, "nodeA") {
		t.Errorf("Should contain nodeA: %s", result.Provenance)
	}
	if !strings.Contains(result.Provenance, "nodeB") {
		t.Errorf("Should contain nodeB: %s", result.Provenance)
	}
	if !strings.Contains(result.Provenance, "nodeC") {
		t.Errorf("Should contain nodeC: %s", result.Provenance)
	}
	if !strings.Contains(result.Provenance, "Call Transitivity") {
		t.Errorf("Should contain rule name: %s", result.Provenance)
	}

	parts := strings.Split(result.Provenance, ",")
	if len(parts) < 2 {
		t.Errorf("Should have multiple evidence items separated by comma: %s", result.Provenance)
	}
}

func TestInferenceResult_GenerateProvenance_Format(t *testing.T) {
	result := InferenceResult{
		DerivedEdge: DerivedEdge{
			SourceID: "A",
			TargetID: "B",
			EdgeType: "rel",
		},
		RuleID: "r1",
		Evidence: []EvidenceEdge{
			{SourceID: "A", TargetID: "X", EdgeType: "e1"},
		},
	}

	result.GenerateProvenance("TestRule")

	expected := "Derived A-rel->B via rule TestRule from evidence: A-e1->X"
	if result.Provenance != expected {
		t.Errorf("Provenance = %q, want %q", result.Provenance, expected)
	}
}

// =============================================================================
// Helper Function Tests
// =============================================================================

func TestFormatEdge(t *testing.T) {
	edge := DerivedEdge{
		SourceID: "funcMain",
		TargetID: "funcHelper",
		EdgeType: "calls",
	}

	result := formatEdge(edge)
	expected := "funcMain-calls->funcHelper"

	if result != expected {
		t.Errorf("formatEdge() = %q, want %q", result, expected)
	}
}

func TestFormatEvidenceEdge(t *testing.T) {
	edge := EvidenceEdge{
		SourceID: "pkgA",
		TargetID: "pkgB",
		EdgeType: "imports",
	}

	result := formatEvidenceEdge(edge)
	expected := "pkgA-imports->pkgB"

	if result != expected {
		t.Errorf("formatEvidenceEdge() = %q, want %q", result, expected)
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestInferenceResult_FullWorkflow(t *testing.T) {
	// Create a result
	derived := DerivedEdge{
		SourceID: "main",
		TargetID: "helper",
		EdgeType: "indirectly_calls",
	}
	evidence := []EvidenceEdge{
		{SourceID: "main", TargetID: "middleware", EdgeType: "calls"},
		{SourceID: "middleware", TargetID: "helper", EdgeType: "calls"},
	}

	result := NewInferenceResult(derived, "transitivity_rule", evidence, 0.9)

	// Generate provenance
	result.GenerateProvenance("Call Chain Transitivity")

	// Validate
	if err := result.Validate(); err != nil {
		t.Fatalf("Result should be valid: %v", err)
	}

	// Check provenance
	if !strings.Contains(result.Provenance, "main") {
		t.Error("Provenance missing source")
	}
	if !strings.Contains(result.Provenance, "helper") {
		t.Error("Provenance missing target")
	}
	if !strings.Contains(result.Provenance, "middleware") {
		t.Error("Provenance missing intermediate node")
	}
	if !strings.Contains(result.Provenance, "Call Chain Transitivity") {
		t.Error("Provenance missing rule name")
	}
}

func TestInferenceResult_HighConfidence(t *testing.T) {
	result := NewInferenceResult(
		DerivedEdge{SourceID: "a", TargetID: "b", EdgeType: "rel"},
		"rule1",
		[]EvidenceEdge{{SourceID: "a", TargetID: "b", EdgeType: "direct"}},
		1.0,
	)

	if err := result.Validate(); err != nil {
		t.Errorf("Maximum confidence should be valid: %v", err)
	}
}

func TestInferenceResult_LowConfidence(t *testing.T) {
	result := NewInferenceResult(
		DerivedEdge{SourceID: "a", TargetID: "b", EdgeType: "rel"},
		"rule1",
		[]EvidenceEdge{{SourceID: "a", TargetID: "x", EdgeType: "e1"}},
		0.0,
	)

	if err := result.Validate(); err != nil {
		t.Errorf("Zero confidence should be valid: %v", err)
	}
}
