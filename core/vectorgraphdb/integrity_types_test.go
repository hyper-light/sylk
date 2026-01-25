package vectorgraphdb

import (
	"testing"
	"time"
)

func TestDefaultIntegrityConfig(t *testing.T) {
	config := DefaultIntegrityConfig()

	t.Run("ValidateOnRead is false by default", func(t *testing.T) {
		if config.ValidateOnRead {
			t.Error("expected ValidateOnRead to be false by default")
		}
	})

	t.Run("PeriodicInterval is 1 hour", func(t *testing.T) {
		if config.PeriodicInterval != time.Hour {
			t.Errorf("expected PeriodicInterval to be 1 hour, got %v", config.PeriodicInterval)
		}
	})

	t.Run("AutoRepair is true by default", func(t *testing.T) {
		if !config.AutoRepair {
			t.Error("expected AutoRepair to be true by default")
		}
	})

	t.Run("SampleSize is 100", func(t *testing.T) {
		if config.SampleSize != 100 {
			t.Errorf("expected SampleSize to be 100, got %d", config.SampleSize)
		}
	})
}

func TestSeverityString(t *testing.T) {
	tests := []struct {
		severity Severity
		expected string
	}{
		{SeverityWarning, "warning"},
		{SeverityError, "error"},
		{SeverityCritical, "critical"},
		{Severity(99), "severity(99)"},
	}

	for _, tc := range tests {
		t.Run(tc.expected, func(t *testing.T) {
			result := tc.severity.String()
			if result != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, result)
			}
		})
	}
}

func TestParseSeverity(t *testing.T) {
	tests := []struct {
		input    string
		expected Severity
		ok       bool
	}{
		{"warning", SeverityWarning, true},
		{"error", SeverityError, true},
		{"critical", SeverityCritical, true},
		{"unknown", Severity(0), false},
		{"", Severity(0), false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result, ok := ParseSeverity(tc.input)
			if ok != tc.ok {
				t.Errorf("expected ok=%v, got ok=%v", tc.ok, ok)
			}
			if result != tc.expected {
				t.Errorf("expected %v, got %v", tc.expected, result)
			}
		})
	}
}

func TestSeverityIsValid(t *testing.T) {
	tests := []struct {
		severity Severity
		valid    bool
	}{
		{SeverityWarning, true},
		{SeverityError, true},
		{SeverityCritical, true},
		{Severity(-1), false},
		{Severity(99), false},
	}

	for _, tc := range tests {
		t.Run(tc.severity.String(), func(t *testing.T) {
			if tc.severity.IsValid() != tc.valid {
				t.Errorf("expected IsValid()=%v for %v", tc.valid, tc.severity)
			}
		})
	}
}

func TestStandardChecksCompleteness(t *testing.T) {
	expectedChecks := []string{
		"orphaned_vectors",
		"orphaned_edges_source",
		"orphaned_edges_target",
		"invalid_hnsw_entry",
		"dimension_mismatch",
		"superseded_cycle",
		"orphaned_provenance",
	}

	checks := StandardChecks()

	t.Run("has expected number of checks", func(t *testing.T) {
		if len(checks) != len(expectedChecks) {
			t.Errorf("expected %d checks, got %d", len(expectedChecks), len(checks))
		}
	})

	for _, name := range expectedChecks {
		t.Run("contains "+name, func(t *testing.T) {
			found := false
			for _, c := range checks {
				if c.Name == name {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected to find check %q", name)
			}
		})
	}
}

func TestStandardChecksHaveRequiredFields(t *testing.T) {
	for _, check := range StandardChecks() {
		t.Run(check.Name, func(t *testing.T) {
			if check.Name == "" {
				t.Error("check Name is empty")
			}
			if check.Description == "" {
				t.Error("check Description is empty")
			}
			if check.Query == "" {
				t.Error("check Query is empty")
			}
			if !check.Severity.IsValid() {
				t.Errorf("check Severity is invalid: %v", check.Severity)
			}
		})
	}
}

func TestStandardChecksSeverities(t *testing.T) {
	expectedSeverities := map[string]Severity{
		"orphaned_vectors":      SeverityError,
		"orphaned_edges_source": SeverityError,
		"orphaned_edges_target": SeverityError,
		"invalid_hnsw_entry":    SeverityCritical,
		"dimension_mismatch":    SeverityCritical,
		"superseded_cycle":      SeverityError,
		"orphaned_provenance":   SeverityWarning,
	}

	for _, check := range StandardChecks() {
		t.Run(check.Name+" severity", func(t *testing.T) {
			expected, ok := expectedSeverities[check.Name]
			if !ok {
				t.Fatalf("no expected severity for check %q", check.Name)
			}
			if check.Severity != expected {
				t.Errorf("expected severity %v, got %v", expected, check.Severity)
			}
		})
	}
}

func TestCheckNames(t *testing.T) {
	names := CheckNames()
	checks := StandardChecks()

	if len(names) != len(checks) {
		t.Errorf("expected %d names, got %d", len(checks), len(names))
	}

	for i, name := range names {
		if name != checks[i].Name {
			t.Errorf("expected name %q at index %d, got %q", checks[i].Name, i, name)
		}
	}
}

func TestGetCheckByName(t *testing.T) {
	t.Run("existing check", func(t *testing.T) {
		check, ok := GetCheckByName("orphaned_vectors")
		if !ok {
			t.Fatal("expected to find orphaned_vectors check")
		}
		if check.Name != "orphaned_vectors" {
			t.Errorf("expected name 'orphaned_vectors', got %q", check.Name)
		}
	})

	t.Run("non-existent check", func(t *testing.T) {
		_, ok := GetCheckByName("nonexistent")
		if ok {
			t.Error("expected not to find nonexistent check")
		}
	})

	t.Run("empty name", func(t *testing.T) {
		_, ok := GetCheckByName("")
		if ok {
			t.Error("expected not to find check with empty name")
		}
	})
}

func TestDimensionMismatchUsesConstant(t *testing.T) {
	check, ok := GetCheckByName("dimension_mismatch")
	if !ok {
		t.Fatal("expected to find dimension_mismatch check")
	}

	expectedSubstring := "1024"
	if len(check.Query) == 0 {
		t.Fatal("dimension_mismatch query is empty")
	}

	found := false
	for i := 0; i <= len(check.Query)-len(expectedSubstring); i++ {
		if check.Query[i:i+len(expectedSubstring)] == expectedSubstring {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected dimension_mismatch query to contain %q, got %q",
			expectedSubstring, check.Query)
	}
}

func TestValidationResultHasCritical(t *testing.T) {
	t.Run("no violations", func(t *testing.T) {
		vr := &ValidationResult{Violations: []Violation{}}
		if vr.HasCritical() {
			t.Error("expected HasCritical to be false with no violations")
		}
	})

	t.Run("only warnings", func(t *testing.T) {
		vr := &ValidationResult{
			Violations: []Violation{
				{Severity: SeverityWarning},
				{Severity: SeverityWarning},
			},
		}
		if vr.HasCritical() {
			t.Error("expected HasCritical to be false with only warnings")
		}
	})

	t.Run("has critical", func(t *testing.T) {
		vr := &ValidationResult{
			Violations: []Violation{
				{Severity: SeverityWarning},
				{Severity: SeverityCritical},
			},
		}
		if !vr.HasCritical() {
			t.Error("expected HasCritical to be true")
		}
	})
}

func TestValidationResultHasErrors(t *testing.T) {
	t.Run("no violations", func(t *testing.T) {
		vr := &ValidationResult{Violations: []Violation{}}
		if vr.HasErrors() {
			t.Error("expected HasErrors to be false with no violations")
		}
	})

	t.Run("only warnings", func(t *testing.T) {
		vr := &ValidationResult{
			Violations: []Violation{
				{Severity: SeverityWarning},
			},
		}
		if vr.HasErrors() {
			t.Error("expected HasErrors to be false with only warnings")
		}
	})

	t.Run("has errors", func(t *testing.T) {
		vr := &ValidationResult{
			Violations: []Violation{
				{Severity: SeverityWarning},
				{Severity: SeverityError},
			},
		}
		if !vr.HasErrors() {
			t.Error("expected HasErrors to be true")
		}
	})
}

func TestValidationResultUnrepairedCount(t *testing.T) {
	t.Run("no violations", func(t *testing.T) {
		vr := &ValidationResult{Violations: []Violation{}}
		if vr.UnrepairedCount() != 0 {
			t.Errorf("expected UnrepairedCount to be 0, got %d", vr.UnrepairedCount())
		}
	})

	t.Run("all repaired", func(t *testing.T) {
		vr := &ValidationResult{
			Violations: []Violation{
				{Repaired: true},
				{Repaired: true},
			},
		}
		if vr.UnrepairedCount() != 0 {
			t.Errorf("expected UnrepairedCount to be 0, got %d", vr.UnrepairedCount())
		}
	})

	t.Run("some unrepaired", func(t *testing.T) {
		vr := &ValidationResult{
			Violations: []Violation{
				{Repaired: true},
				{Repaired: false},
				{Repaired: false},
			},
		}
		if vr.UnrepairedCount() != 2 {
			t.Errorf("expected UnrepairedCount to be 2, got %d", vr.UnrepairedCount())
		}
	})
}

func TestViolationFields(t *testing.T) {
	v := Violation{
		Check:       "orphaned_vectors",
		EntityID:    "node-123",
		Description: "Vector found without corresponding node",
		Severity:    SeverityError,
		Repaired:    true,
	}

	if v.Check != "orphaned_vectors" {
		t.Errorf("unexpected Check: %s", v.Check)
	}
	if v.EntityID != "node-123" {
		t.Errorf("unexpected EntityID: %s", v.EntityID)
	}
	if v.Description != "Vector found without corresponding node" {
		t.Errorf("unexpected Description: %s", v.Description)
	}
	if v.Severity != SeverityError {
		t.Errorf("unexpected Severity: %v", v.Severity)
	}
	if !v.Repaired {
		t.Error("expected Repaired to be true")
	}
}

func TestIntegrityConfigFields(t *testing.T) {
	config := IntegrityConfig{
		ValidateOnRead:   true,
		PeriodicInterval: 30 * time.Minute,
		AutoRepair:       false,
		SampleSize:       50,
	}

	if !config.ValidateOnRead {
		t.Error("expected ValidateOnRead to be true")
	}
	if config.PeriodicInterval != 30*time.Minute {
		t.Errorf("expected PeriodicInterval to be 30m, got %v", config.PeriodicInterval)
	}
	if config.AutoRepair {
		t.Error("expected AutoRepair to be false")
	}
	if config.SampleSize != 50 {
		t.Errorf("expected SampleSize to be 50, got %d", config.SampleSize)
	}
}

func TestInvariantCheckFields(t *testing.T) {
	check := InvariantCheck{
		Name:        "test_check",
		Description: "A test check",
		Query:       "SELECT id FROM test",
		Repair:      "DELETE FROM test",
		Severity:    SeverityWarning,
	}

	if check.Name != "test_check" {
		t.Errorf("unexpected Name: %s", check.Name)
	}
	if check.Description != "A test check" {
		t.Errorf("unexpected Description: %s", check.Description)
	}
	if check.Query != "SELECT id FROM test" {
		t.Errorf("unexpected Query: %s", check.Query)
	}
	if check.Repair != "DELETE FROM test" {
		t.Errorf("unexpected Repair: %s", check.Repair)
	}
	if check.Severity != SeverityWarning {
		t.Errorf("unexpected Severity: %v", check.Severity)
	}
}

func TestChecksWithoutRepair(t *testing.T) {
	// Some checks cannot be auto-repaired
	checksWithoutRepair := []string{
		"dimension_mismatch",
		"superseded_cycle",
	}

	for _, name := range checksWithoutRepair {
		t.Run(name+" has no repair", func(t *testing.T) {
			check, ok := GetCheckByName(name)
			if !ok {
				t.Fatalf("expected to find check %q", name)
			}
			if check.Repair != "" {
				t.Errorf("expected %q to have empty Repair, got %q", name, check.Repair)
			}
		})
	}
}

func TestChecksWithRepair(t *testing.T) {
	// Some checks can be auto-repaired
	checksWithRepair := []string{
		"orphaned_vectors",
		"orphaned_edges_source",
		"orphaned_edges_target",
		"invalid_hnsw_entry",
		"orphaned_provenance",
	}

	for _, name := range checksWithRepair {
		t.Run(name+" has repair", func(t *testing.T) {
			check, ok := GetCheckByName(name)
			if !ok {
				t.Fatalf("expected to find check %q", name)
			}
			if check.Repair == "" {
				t.Errorf("expected %q to have non-empty Repair", name)
			}
		})
	}
}

func TestValidationResultZeroValue(t *testing.T) {
	vr := &ValidationResult{}

	if vr.HasCritical() {
		t.Error("zero-value ValidationResult should not have critical violations")
	}
	if vr.HasErrors() {
		t.Error("zero-value ValidationResult should not have error violations")
	}
	if vr.UnrepairedCount() != 0 {
		t.Errorf("zero-value ValidationResult should have 0 unrepaired, got %d", vr.UnrepairedCount())
	}
}
