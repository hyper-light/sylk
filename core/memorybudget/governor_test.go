package memorybudget

import (
	"errors"
	"testing"
)

func TestGovernorScopeLimit(t *testing.T) {
	governor := NewGovernor(32, 8, 8, 8)
	reservation, err := governor.Reserve(ScopeOverlay, 4)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := reservation.Resize(16); !errors.Is(err, ErrMemoryLimitExceeded) {
		t.Fatalf("Resize error = %v, want %v", err, ErrMemoryLimitExceeded)
	}
}

func TestGovernorTotalLimit(t *testing.T) {
	governor := NewGovernor(8, 8, 8, 8)
	workspace, err := governor.Reserve(ScopeWorkspaceImage, 4)
	if err != nil {
		t.Fatalf("Reserve workspace: %v", err)
	}
	defer workspace.Release()
	_, err = governor.Reserve(ScopeExecution, 5)
	if !errors.Is(err, ErrMemoryLimitExceeded) {
		t.Fatalf("Reserve execution error = %v, want %v", err, ErrMemoryLimitExceeded)
	}
}
