package engineer

import (
	"strings"
	"testing"
)

// TestDefaultAuditConfig verifies the default audit configuration values.
func TestDefaultAuditConfig(t *testing.T) {
	cfg := DefaultAuditConfig()

	if cfg.MaxAuditIterations != 3 {
		t.Errorf("MaxAuditIterations = %d, want 3", cfg.MaxAuditIterations)
	}
	if cfg.MinQualityScore != 0.7 {
		t.Errorf("MinQualityScore = %f, want 0.7", cfg.MinQualityScore)
	}
}

// TestShouldReimplement_NilVerdict verifies nil verdict returns false.
func TestShouldReimplement_NilVerdict(t *testing.T) {
	cfg := DefaultAuditConfig()
	if shouldReimplement(nil, 0, cfg) {
		t.Error("nil verdict should return false")
	}
}

// TestShouldReimplement_PassTrue verifies passing verdict returns false regardless of iteration.
func TestShouldReimplement_PassTrue(t *testing.T) {
	cfg := DefaultAuditConfig()
	verdict := &AuditVerdict{Pass: true, QualityScore: 0.9}

	iterations := []int{0, 1, cfg.MaxAuditIterations - 1, cfg.MaxAuditIterations, cfg.MaxAuditIterations + 1}
	for _, iter := range iterations {
		if shouldReimplement(verdict, iter, cfg) {
			t.Errorf("pass=true at iteration %d should return false", iter)
		}
	}
}

// TestShouldReimplement_FailBelowMax verifies failing verdict below max returns true.
func TestShouldReimplement_FailBelowMax(t *testing.T) {
	cfg := DefaultAuditConfig()
	verdict := &AuditVerdict{Pass: false, QualityScore: 0.3}

	for iter := range cfg.MaxAuditIterations {
		if !shouldReimplement(verdict, iter, cfg) {
			t.Errorf("pass=false at iteration %d (< max %d) should return true", iter, cfg.MaxAuditIterations)
		}
	}
}

// TestShouldReimplement_FailAtOrAboveMax verifies failing verdict at or above max returns false.
func TestShouldReimplement_FailAtOrAboveMax(t *testing.T) {
	cfg := DefaultAuditConfig()
	verdict := &AuditVerdict{Pass: false, QualityScore: 0.3}

	overMax := []int{cfg.MaxAuditIterations, cfg.MaxAuditIterations + 1}
	for _, iter := range overMax {
		if shouldReimplement(verdict, iter, cfg) {
			t.Errorf("pass=false at iteration %d (>= max %d) should return false", iter, cfg.MaxAuditIterations)
		}
	}
}

// TestParseAuditVerdict_ValidJSON verifies valid JSON parses correctly.
func TestParseAuditVerdict_ValidJSON(t *testing.T) {
	input := `{"quality_score": 0.85, "pass": true, "issues": [{"category": "readability", "severity": "low", "description": "variable names"}], "iteration": 2}`

	verdict, err := parseAuditVerdict(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verdict.QualityScore != 0.85 {
		t.Errorf("QualityScore = %f, want 0.85", verdict.QualityScore)
	}
	if !verdict.Pass {
		t.Error("Pass should be true")
	}
	if len(verdict.Issues) != 1 {
		t.Fatalf("Issues length = %d, want 1", len(verdict.Issues))
	}
	if verdict.Issues[0].Category != AuditReadability {
		t.Errorf("Issue category = %q, want %q", verdict.Issues[0].Category, AuditReadability)
	}
	if verdict.Issues[0].Severity != "low" {
		t.Errorf("Issue severity = %q, want %q", verdict.Issues[0].Severity, "low")
	}
	if verdict.Iteration != 2 {
		t.Errorf("Iteration = %d, want 2", verdict.Iteration)
	}
}

// TestParseAuditVerdict_JSONInCodeFence verifies JSON in markdown code fence parses correctly.
func TestParseAuditVerdict_JSONInCodeFence(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "json code fence",
			input: "Here is the audit:\n```json\n{\"quality_score\": 0.6, \"pass\": false, \"issues\": []}\n```\n",
		},
		{
			name:  "bare code fence",
			input: "Result:\n```\n{\"quality_score\": 0.6, \"pass\": false, \"issues\": []}\n```\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verdict, err := parseAuditVerdict(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if verdict.QualityScore != 0.6 {
				t.Errorf("QualityScore = %f, want 0.6", verdict.QualityScore)
			}
			if verdict.Pass {
				t.Error("Pass should be false")
			}
		})
	}
}

// TestParseAuditVerdict_NonJSON verifies non-JSON content returns an error.
func TestParseAuditVerdict_NonJSON(t *testing.T) {
	inputs := []string{
		"This is not JSON at all.",
		"",
		"   ",
		"quality_score: 0.8, pass: true",
	}

	for _, input := range inputs {
		_, err := parseAuditVerdict(input)
		if err == nil {
			t.Errorf("expected error for input %q, got nil", input)
		}
	}
}

// TestParseAuditVerdict_LeadingText verifies JSON preceded by text parses correctly.
func TestParseAuditVerdict_LeadingText(t *testing.T) {
	input := `Here is my verdict: {"quality_score": 0.95, "pass": true}`

	verdict, err := parseAuditVerdict(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verdict.QualityScore != 0.95 {
		t.Errorf("QualityScore = %f, want 0.95", verdict.QualityScore)
	}
}

// TestBuildAuditPrompt_ContainsCriteria verifies the prompt includes criteria when non-empty.
func TestBuildAuditPrompt_ContainsCriteria(t *testing.T) {
	criteria := "Must handle edge cases"
	implementation := "func main() {}"

	prompt := buildAuditPrompt(implementation, criteria)

	if !strings.Contains(prompt, "Criteria:") {
		t.Error("prompt should contain 'Criteria:' header when criteria is non-empty")
	}
	if !strings.Contains(prompt, criteria) {
		t.Errorf("prompt should contain criteria text %q", criteria)
	}
}

// TestBuildAuditPrompt_OmitsCriteriaWhenEmpty verifies the prompt omits criteria when empty.
func TestBuildAuditPrompt_OmitsCriteriaWhenEmpty(t *testing.T) {
	implementation := "func main() {}"

	prompt := buildAuditPrompt(implementation, "")

	if strings.Contains(prompt, "Criteria:") {
		t.Error("prompt should not contain 'Criteria:' header when criteria is empty")
	}
}

// TestBuildAuditPrompt_ContainsImplementation verifies the prompt includes the implementation.
func TestBuildAuditPrompt_ContainsImplementation(t *testing.T) {
	implementation := "func Calculate(x, y int) int { return x + y }"

	prompt := buildAuditPrompt(implementation, "")

	if !strings.Contains(prompt, "Implementation:") {
		t.Error("prompt should contain 'Implementation:' header")
	}
	if !strings.Contains(prompt, implementation) {
		t.Error("prompt should contain the implementation text")
	}
}

// TestAuditCategoryConstants verifies all audit category constants.
func TestAuditCategoryConstants(t *testing.T) {
	tests := []struct {
		category AuditCategory
		expected string
	}{
		{AuditReadability, "readability"},
		{AuditCorrectness, "correctness"},
		{AuditPerformance, "performance"},
		{AuditMaintainability, "maintainability"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if string(tt.category) != tt.expected {
				t.Errorf("got %q, want %q", tt.category, tt.expected)
			}
		})
	}
}

// TestParseAuditVerdict_AllIssueFields verifies all AuditIssue fields parse correctly.
func TestParseAuditVerdict_AllIssueFields(t *testing.T) {
	input := `{"quality_score": 0.5, "pass": false, "issues": [{"category": "correctness", "severity": "high", "description": "null pointer", "file": "main.go", "suggestion": "add nil check"}]}`

	verdict, err := parseAuditVerdict(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(verdict.Issues) != 1 {
		t.Fatalf("Issues length = %d, want 1", len(verdict.Issues))
	}

	issue := verdict.Issues[0]
	if issue.Category != AuditCorrectness {
		t.Errorf("Category = %q, want %q", issue.Category, AuditCorrectness)
	}
	if issue.Severity != "high" {
		t.Errorf("Severity = %q, want %q", issue.Severity, "high")
	}
	if issue.Description != "null pointer" {
		t.Errorf("Description = %q, want %q", issue.Description, "null pointer")
	}
	if issue.File != "main.go" {
		t.Errorf("File = %q, want %q", issue.File, "main.go")
	}
	if issue.Suggestion != "add nil check" {
		t.Errorf("Suggestion = %q, want %q", issue.Suggestion, "add nil check")
	}
}

// TestParseAuditVerdict_EmptyIssues verifies empty issues array parses correctly.
func TestParseAuditVerdict_EmptyIssues(t *testing.T) {
	input := `{"quality_score": 1.0, "pass": true, "issues": []}`

	verdict, err := parseAuditVerdict(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(verdict.Issues) != 0 {
		t.Errorf("Issues length = %d, want 0", len(verdict.Issues))
	}
}
