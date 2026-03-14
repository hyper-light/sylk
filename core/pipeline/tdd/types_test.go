package tdd

import "testing"

func TestValidateTransition_AllValid(t *testing.T) {
	valid := []struct {
		from, to PipelineStatus
	}{
		{StatusPending, StatusActive},
		{StatusPending, StatusFailed},
		{StatusPending, StatusCancelled},
		{StatusActive, StatusCompleted},
		{StatusActive, StatusFailed},
		{StatusActive, StatusCancelled},
	}
	for _, tc := range valid {
		if err := ValidateTransition(tc.from, tc.to); err != nil {
			t.Errorf("expected valid transition %s → %s, got: %v", tc.from, tc.to, err)
		}
	}
}

func TestValidateTransition_InvalidRejected(t *testing.T) {
	invalid := []struct {
		from, to PipelineStatus
	}{
		{StatusPending, StatusCompleted},
		{StatusActive, StatusPending},
		{StatusActive, StatusActive},
		{StatusCompleted, StatusPending},
		{StatusCompleted, StatusFailed},
		{StatusFailed, StatusPending},
		{StatusCancelled, StatusPending},
	}
	for _, tc := range invalid {
		if err := ValidateTransition(tc.from, tc.to); err == nil {
			t.Errorf("expected invalid transition %s → %s to be rejected", tc.from, tc.to)
		}
	}
}

func TestIsTerminalStatus(t *testing.T) {
	terminal := []PipelineStatus{StatusCompleted, StatusFailed, StatusCancelled}
	for _, s := range terminal {
		if !IsTerminalStatus(s) {
			t.Errorf("expected %s to be terminal", s)
		}
	}

	nonTerminal := []PipelineStatus{
		StatusPending, StatusActive,
	}
	for _, s := range nonTerminal {
		if IsTerminalStatus(s) {
			t.Errorf("expected %s to be non-terminal", s)
		}
	}
}
