package tdd

import "testing"

func TestValidateTransition_AllValid(t *testing.T) {
	valid := []struct {
		from, to PipelineStatus
	}{
		{StatusPending, StatusDefiningCriteria},
		{StatusPending, StatusFailed},
		{StatusPending, StatusCancelled},
		{StatusDefiningCriteria, StatusCreatingTests},
		{StatusDefiningCriteria, StatusFailed},
		{StatusDefiningCriteria, StatusCancelled},
		{StatusCreatingTests, StatusExecuting},
		{StatusCreatingTests, StatusFailed},
		{StatusCreatingTests, StatusCancelled},
		{StatusExecuting, StatusValidating},
		{StatusExecuting, StatusFailed},
		{StatusExecuting, StatusCancelled},
		{StatusValidating, StatusCompleted},
		{StatusValidating, StatusDefiningCriteria},
		{StatusValidating, StatusFailed},
		{StatusValidating, StatusCancelled},
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
		{StatusPending, StatusValidating},
		{StatusPending, StatusCompleted},
		{StatusDefiningCriteria, StatusValidating},
		{StatusCreatingTests, StatusDefiningCriteria},
		{StatusExecuting, StatusCreatingTests},
		{StatusValidating, StatusExecuting},
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
		StatusPending, StatusDefiningCriteria, StatusCreatingTests,
		StatusExecuting, StatusValidating,
	}
	for _, s := range nonTerminal {
		if IsTerminalStatus(s) {
			t.Errorf("expected %s to be non-terminal", s)
		}
	}
}
