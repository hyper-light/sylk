package shared

import (
	"encoding/json"
	"testing"
)

func TestQualityGateUnmarshal_AcceptsNumericString(t *testing.T) {
	var gate QualityGate
	err := json.Unmarshal([]byte(`{
		"name":"coverage_min",
		"metric":"coverage",
		"threshold":"85",
		"operator":">="
	}`), &gate)
	if err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if gate.Threshold != 85 {
		t.Fatalf("Threshold = %v, want 85", gate.Threshold)
	}
}

func TestQualityGateUnmarshal_AcceptsPercentString(t *testing.T) {
	var gate QualityGate
	err := json.Unmarshal([]byte(`{
		"name":"coverage_min",
		"metric":"coverage",
		"threshold":"92.5%",
		"operator":">="
	}`), &gate)
	if err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if gate.Threshold != 92.5 {
		t.Fatalf("Threshold = %v, want 92.5", gate.Threshold)
	}
}

func TestSuccessCriterionUnmarshal_AcceptsBooleanString(t *testing.T) {
	var criterion SuccessCriterion
	err := json.Unmarshal([]byte(`{
		"id":"criterion_1",
		"description":"CLI prints output",
		"verifiable":"true",
		"verification_method":"stdout_match"
	}`), &criterion)
	if err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !criterion.Verifiable {
		t.Fatal("expected Verifiable=true")
	}
}

func TestConstraintUnmarshal_AcceptsBooleanString(t *testing.T) {
	var constraint Constraint
	err := json.Unmarshal([]byte(`{
		"type":"dependency",
		"description":"Use stdlib only",
		"required":"true"
	}`), &constraint)
	if err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !constraint.Required {
		t.Fatal("expected Required=true")
	}
}

func TestQualityGateUnmarshal_RejectsInvalidThreshold(t *testing.T) {
	var gate QualityGate
	err := json.Unmarshal([]byte(`{
		"name":"coverage_min",
		"metric":"coverage",
		"threshold":"eighty",
		"operator":">="
	}`), &gate)
	if err == nil {
		t.Fatal("expected json.Unmarshal() to fail")
	}
}
