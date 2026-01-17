package errors

import (
	"errors"
	"testing"
)

func TestRemedy_NewRemedy(t *testing.T) {
	tests := []struct {
		name           string
		description    string
		action         RemedyAction
		confidence     float64
		wantConfidence float64
	}{
		{
			name:           "valid confidence",
			description:    "retry the operation",
			action:         RemedyRetry,
			confidence:     0.8,
			wantConfidence: 0.8,
		},
		{
			name:           "confidence clamped to max",
			description:    "abort operation",
			action:         RemedyAbort,
			confidence:     1.5,
			wantConfidence: 1.0,
		},
		{
			name:           "confidence clamped to min",
			description:    "manual intervention",
			action:         RemedyManual,
			confidence:     -0.5,
			wantConfidence: 0.0,
		},
		{
			name:           "zero confidence",
			description:    "alternative approach",
			action:         RemedyAlternative,
			confidence:     0.0,
			wantConfidence: 0.0,
		},
		{
			name:           "max confidence",
			description:    "retry with backoff",
			action:         RemedyRetry,
			confidence:     1.0,
			wantConfidence: 1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remedy := NewRemedy(tt.description, tt.action, tt.confidence)

			if remedy.Description != tt.description {
				t.Errorf("Description = %q, want %q", remedy.Description, tt.description)
			}
			if remedy.Action != tt.action {
				t.Errorf("Action = %v, want %v", remedy.Action, tt.action)
			}
			if remedy.Confidence != tt.wantConfidence {
				t.Errorf("Confidence = %v, want %v", remedy.Confidence, tt.wantConfidence)
			}
			if remedy.ID == "" {
				t.Error("ID should not be empty")
			}
			if remedy.Metadata == nil {
				t.Error("Metadata should be initialized")
			}
			if remedy.TokenCost != 0 {
				t.Errorf("TokenCost = %d, want 0", remedy.TokenCost)
			}
		})
	}
}

func TestRemedy_WithTokenCost(t *testing.T) {
	remedy := NewRemedy("test", RemedyRetry, 0.5)
	result := remedy.WithTokenCost(100)

	if result != remedy {
		t.Error("WithTokenCost should return the same remedy instance")
	}
	if remedy.TokenCost != 100 {
		t.Errorf("TokenCost = %d, want 100", remedy.TokenCost)
	}
}

func TestRemedy_WithMetadata(t *testing.T) {
	remedy := NewRemedy("test", RemedyRetry, 0.5)
	result := remedy.WithMetadata("key1", "value1").WithMetadata("key2", "value2")

	if result != remedy {
		t.Error("WithMetadata should return the same remedy instance")
	}
	if remedy.Metadata["key1"] != "value1" {
		t.Errorf("Metadata[key1] = %q, want %q", remedy.Metadata["key1"], "value1")
	}
	if remedy.Metadata["key2"] != "value2" {
		t.Errorf("Metadata[key2] = %q, want %q", remedy.Metadata["key2"], "value2")
	}
}

func TestRemedySet_Add_SortsByConfidence(t *testing.T) {
	err := errors.New("test error")
	rs := NewRemedySet(err)

	// Add remedies out of order
	rs.Add(NewRemedy("low confidence", RemedyRetry, 0.3))
	rs.Add(NewRemedy("high confidence", RemedyAbort, 0.9))
	rs.Add(NewRemedy("medium confidence", RemedyManual, 0.6))

	// All() should return sorted by confidence descending
	all := rs.All()
	if len(all) != 3 {
		t.Fatalf("All() returned %d remedies, want 3", len(all))
	}

	expected := []float64{0.9, 0.6, 0.3}
	for i, remedy := range all {
		if remedy.Confidence != expected[i] {
			t.Errorf("All()[%d].Confidence = %v, want %v", i, remedy.Confidence, expected[i])
		}
	}
}

func TestRemedySet_Best_ReturnsHighestConfidence(t *testing.T) {
	err := errors.New("test error")
	rs := NewRemedySet(err)

	rs.Add(NewRemedy("medium", RemedyRetry, 0.5))
	rs.Add(NewRemedy("best", RemedyAbort, 0.95))
	rs.Add(NewRemedy("low", RemedyManual, 0.2))

	best := rs.Best()
	if best == nil {
		t.Fatal("Best() returned nil")
	}
	if best.Description != "best" {
		t.Errorf("Best().Description = %q, want %q", best.Description, "best")
	}
	if best.Confidence != 0.95 {
		t.Errorf("Best().Confidence = %v, want 0.95", best.Confidence)
	}
}

func TestRemedySet_All_ReturnsAllRemedies(t *testing.T) {
	err := errors.New("test error")
	rs := NewRemedySet(err)

	remedies := []*Remedy{
		NewRemedy("first", RemedyRetry, 0.8),
		NewRemedy("second", RemedyAbort, 0.6),
		NewRemedy("third", RemedyManual, 0.4),
	}

	for _, r := range remedies {
		rs.Add(r)
	}

	all := rs.All()
	if len(all) != len(remedies) {
		t.Errorf("All() returned %d remedies, want %d", len(all), len(remedies))
	}

	// Verify all original remedies are present
	descMap := make(map[string]bool)
	for _, r := range all {
		descMap[r.Description] = true
	}
	for _, r := range remedies {
		if !descMap[r.Description] {
			t.Errorf("Remedy %q not found in All()", r.Description)
		}
	}
}

func TestRemedySet_Empty(t *testing.T) {
	err := errors.New("test error")
	rs := NewRemedySet(err)

	if !rs.IsEmpty() {
		t.Error("IsEmpty() = false, want true for new RemedySet")
	}
	if rs.Count() != 0 {
		t.Errorf("Count() = %d, want 0 for new RemedySet", rs.Count())
	}
	if rs.Best() != nil {
		t.Error("Best() should return nil for empty RemedySet")
	}

	all := rs.All()
	if len(all) != 0 {
		t.Errorf("All() returned %d remedies, want 0", len(all))
	}
}

func TestRemedySet_Add_NilRemedy(t *testing.T) {
	err := errors.New("test error")
	rs := NewRemedySet(err)

	rs.Add(nil)

	if !rs.IsEmpty() {
		t.Error("Adding nil remedy should not change the set")
	}
}

func TestRemedySet_Count(t *testing.T) {
	err := errors.New("test error")
	rs := NewRemedySet(err)

	if rs.Count() != 0 {
		t.Errorf("Initial Count() = %d, want 0", rs.Count())
	}

	rs.Add(NewRemedy("first", RemedyRetry, 0.5))
	if rs.Count() != 1 {
		t.Errorf("After 1 add, Count() = %d, want 1", rs.Count())
	}

	rs.Add(NewRemedy("second", RemedyAbort, 0.6))
	if rs.Count() != 2 {
		t.Errorf("After 2 adds, Count() = %d, want 2", rs.Count())
	}
}

func TestRemedySet_Error(t *testing.T) {
	err := errors.New("original error")
	rs := NewRemedySet(err)

	if rs.Error != err {
		t.Errorf("Error = %v, want %v", rs.Error, err)
	}
}

func TestRemedySet_Timestamp(t *testing.T) {
	err := errors.New("test error")
	rs := NewRemedySet(err)

	if rs.Timestamp.IsZero() {
		t.Error("Timestamp should not be zero")
	}
}

func TestRemedyAction_Values(t *testing.T) {
	// Verify action constants
	if RemedyRetry != "retry" {
		t.Errorf("RemedyRetry = %q, want %q", RemedyRetry, "retry")
	}
	if RemedyAlternative != "alternative" {
		t.Errorf("RemedyAlternative = %q, want %q", RemedyAlternative, "alternative")
	}
	if RemedyAbort != "abort" {
		t.Errorf("RemedyAbort = %q, want %q", RemedyAbort, "abort")
	}
	if RemedyManual != "manual" {
		t.Errorf("RemedyManual = %q, want %q", RemedyManual, "manual")
	}
}
