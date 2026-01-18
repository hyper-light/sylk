package vectorgraphdb

import (
	"errors"
	"fmt"
	"testing"
)

// =============================================================================
// OCCConflictType Tests
// =============================================================================

func TestOCCConflictType_String(t *testing.T) {
	tests := []struct {
		name     string
		ct       OCCConflictType
		expected string
	}{
		{
			name:     "stale conflict type",
			ct:       OCCConflictStale,
			expected: "stale",
		},
		{
			name:     "concurrent conflict type",
			ct:       OCCConflictConcurrent,
			expected: "concurrent",
		},
		{
			name:     "deleted conflict type",
			ct:       OCCConflictDeleted,
			expected: "deleted",
		},
		{
			name:     "integrity conflict type",
			ct:       OCCConflictIntegrity,
			expected: "integrity",
		},
		{
			name:     "unknown conflict type",
			ct:       OCCConflictType(99),
			expected: "occ_conflict_type(99)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.ct.String()
			if result != tt.expected {
				t.Errorf("String() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// =============================================================================
// ConflictError.Error() Tests
// =============================================================================

func TestConflictError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *ConflictError
		expected string
	}{
		{
			name: "stale read error",
			err: &ConflictError{
				NodeID:      "node-123",
				ReadVersion: 5,
				CurrVersion: 10,
				Type:        OCCConflictStale,
				SessionID:   "session-abc",
			},
			expected: "stale read: node node-123 version 5, now 10",
		},
		{
			name: "concurrent modification error",
			err: &ConflictError{
				NodeID:    "node-456",
				Type:      OCCConflictConcurrent,
				SessionID: "session-def",
			},
			expected: "concurrent modification: node node-456",
		},
		{
			name: "deleted node error",
			err: &ConflictError{
				NodeID:    "node-789",
				Type:      OCCConflictDeleted,
				SessionID: "session-ghi",
			},
			expected: "node deleted: node-789",
		},
		{
			name: "integrity error",
			err: &ConflictError{
				NodeID:    "node-abc",
				Type:      OCCConflictIntegrity,
				SessionID: "session-jkl",
			},
			expected: "integrity error: node node-abc hash mismatch",
		},
		{
			name: "unknown conflict type error",
			err: &ConflictError{
				NodeID:    "node-unknown",
				Type:      OCCConflictType(99),
				SessionID: "session-xyz",
			},
			expected: "conflict on node node-unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.err.Error()
			if result != tt.expected {
				t.Errorf("Error() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// =============================================================================
// ConflictError.Is() Tests
// =============================================================================

func TestConflictError_Is(t *testing.T) {
	tests := []struct {
		name     string
		err      *ConflictError
		target   error
		expected bool
	}{
		{
			name:     "same type - stale",
			err:      NewStaleConflictError("node-1", 1, 2, "session-1"),
			target:   ErrOCCConflictStale,
			expected: true,
		},
		{
			name:     "same type - concurrent",
			err:      NewConcurrentConflictError("node-2", "session-2"),
			target:   ErrOCCConflictConcurrent,
			expected: true,
		},
		{
			name:     "same type - deleted",
			err:      NewDeletedConflictError("node-3", "session-3"),
			target:   ErrOCCConflictDeleted,
			expected: true,
		},
		{
			name:     "same type - integrity",
			err:      NewIntegrityConflictError("node-4", "session-4"),
			target:   ErrOCCConflictIntegrity,
			expected: true,
		},
		{
			name:     "different types - stale vs concurrent",
			err:      NewStaleConflictError("node-5", 1, 2, "session-5"),
			target:   ErrOCCConflictConcurrent,
			expected: false,
		},
		{
			name:     "different types - deleted vs integrity",
			err:      NewDeletedConflictError("node-6", "session-6"),
			target:   ErrOCCConflictIntegrity,
			expected: false,
		},
		{
			name:     "non-ConflictError target",
			err:      NewStaleConflictError("node-7", 1, 2, "session-7"),
			target:   errors.New("some other error"),
			expected: false,
		},
		{
			name:     "nil target",
			err:      NewStaleConflictError("node-8", 1, 2, "session-8"),
			target:   nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.err.Is(tt.target)
			if result != tt.expected {
				t.Errorf("Is() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestConflictError_Is_WithErrorsIs(t *testing.T) {
	staleErr := NewStaleConflictError("test-node", 1, 5, "test-session")

	if !errors.Is(staleErr, ErrOCCConflictStale) {
		t.Error("errors.Is() should return true for matching conflict types")
	}

	if errors.Is(staleErr, ErrOCCConflictConcurrent) {
		t.Error("errors.Is() should return false for non-matching conflict types")
	}
}

// =============================================================================
// ConflictError.Unwrap() Tests
// =============================================================================

func TestConflictError_Unwrap(t *testing.T) {
	err := NewStaleConflictError("node-1", 1, 2, "session-1")
	unwrapped := err.Unwrap()

	if unwrapped != nil {
		t.Errorf("Unwrap() = %v, want nil", unwrapped)
	}
}

// =============================================================================
// Helper Function Tests
// =============================================================================

func TestNewStaleConflictError(t *testing.T) {
	nodeID := "test-node"
	readVersion := uint64(5)
	currVersion := uint64(10)
	sessionID := "test-session"

	err := NewStaleConflictError(nodeID, readVersion, currVersion, sessionID)

	if err.NodeID != nodeID {
		t.Errorf("NodeID = %q, want %q", err.NodeID, nodeID)
	}
	if err.ReadVersion != readVersion {
		t.Errorf("ReadVersion = %d, want %d", err.ReadVersion, readVersion)
	}
	if err.CurrVersion != currVersion {
		t.Errorf("CurrVersion = %d, want %d", err.CurrVersion, currVersion)
	}
	if err.Type != OCCConflictStale {
		t.Errorf("Type = %v, want %v", err.Type, OCCConflictStale)
	}
	if err.SessionID != sessionID {
		t.Errorf("SessionID = %q, want %q", err.SessionID, sessionID)
	}
}

func TestNewConcurrentConflictError(t *testing.T) {
	nodeID := "test-node"
	sessionID := "test-session"

	err := NewConcurrentConflictError(nodeID, sessionID)

	if err.NodeID != nodeID {
		t.Errorf("NodeID = %q, want %q", err.NodeID, nodeID)
	}
	if err.Type != OCCConflictConcurrent {
		t.Errorf("Type = %v, want %v", err.Type, OCCConflictConcurrent)
	}
	if err.SessionID != sessionID {
		t.Errorf("SessionID = %q, want %q", err.SessionID, sessionID)
	}
}

func TestNewDeletedConflictError(t *testing.T) {
	nodeID := "test-node"
	sessionID := "test-session"

	err := NewDeletedConflictError(nodeID, sessionID)

	if err.NodeID != nodeID {
		t.Errorf("NodeID = %q, want %q", err.NodeID, nodeID)
	}
	if err.Type != OCCConflictDeleted {
		t.Errorf("Type = %v, want %v", err.Type, OCCConflictDeleted)
	}
	if err.SessionID != sessionID {
		t.Errorf("SessionID = %q, want %q", err.SessionID, sessionID)
	}
}

func TestNewIntegrityConflictError(t *testing.T) {
	nodeID := "test-node"
	sessionID := "test-session"

	err := NewIntegrityConflictError(nodeID, sessionID)

	if err.NodeID != nodeID {
		t.Errorf("NodeID = %q, want %q", err.NodeID, nodeID)
	}
	if err.Type != OCCConflictIntegrity {
		t.Errorf("Type = %v, want %v", err.Type, OCCConflictIntegrity)
	}
	if err.SessionID != sessionID {
		t.Errorf("SessionID = %q, want %q", err.SessionID, sessionID)
	}
}

// =============================================================================
// Utility Function Tests
// =============================================================================

func TestIsConflictError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "ConflictError - stale",
			err:      NewStaleConflictError("node-1", 1, 2, "session-1"),
			expected: true,
		},
		{
			name:     "ConflictError - concurrent",
			err:      NewConcurrentConflictError("node-2", "session-2"),
			expected: true,
		},
		{
			name:     "non-ConflictError",
			err:      errors.New("some other error"),
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "wrapped ConflictError",
			err:      fmt.Errorf("wrapped: %w", NewStaleConflictError("node-3", 1, 2, "session-3")),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsConflictError(tt.err)
			if result != tt.expected {
				t.Errorf("IsConflictError() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestGetConflictError(t *testing.T) {
	t.Run("returns ConflictError when present", func(t *testing.T) {
		originalErr := NewStaleConflictError("node-1", 1, 2, "session-1")
		conflictErr, ok := GetConflictError(originalErr)

		if !ok {
			t.Error("GetConflictError() ok = false, want true")
		}
		if conflictErr != originalErr {
			t.Error("GetConflictError() returned different error")
		}
	})

	t.Run("returns wrapped ConflictError", func(t *testing.T) {
		originalErr := NewStaleConflictError("node-2", 1, 2, "session-2")
		wrappedErr := fmt.Errorf("wrapped: %w", originalErr)
		conflictErr, ok := GetConflictError(wrappedErr)

		if !ok {
			t.Error("GetConflictError() ok = false, want true")
		}
		if conflictErr != originalErr {
			t.Error("GetConflictError() returned different error")
		}
	})

	t.Run("returns nil for non-ConflictError", func(t *testing.T) {
		otherErr := errors.New("some other error")
		conflictErr, ok := GetConflictError(otherErr)

		if ok {
			t.Error("GetConflictError() ok = true, want false")
		}
		if conflictErr != nil {
			t.Error("GetConflictError() conflictErr should be nil")
		}
	})

	t.Run("returns nil for nil error", func(t *testing.T) {
		conflictErr, ok := GetConflictError(nil)

		if ok {
			t.Error("GetConflictError() ok = true, want false")
		}
		if conflictErr != nil {
			t.Error("GetConflictError() conflictErr should be nil")
		}
	})
}

// =============================================================================
// Sentinel Error Tests
// =============================================================================

func TestSentinelErrors(t *testing.T) {
	tests := []struct {
		name         string
		sentinel     *ConflictError
		expectedType OCCConflictType
	}{
		{
			name:         "ErrOCCConflictStale",
			sentinel:     ErrOCCConflictStale,
			expectedType: OCCConflictStale,
		},
		{
			name:         "ErrOCCConflictConcurrent",
			sentinel:     ErrOCCConflictConcurrent,
			expectedType: OCCConflictConcurrent,
		},
		{
			name:         "ErrOCCConflictDeleted",
			sentinel:     ErrOCCConflictDeleted,
			expectedType: OCCConflictDeleted,
		},
		{
			name:         "ErrOCCConflictIntegrity",
			sentinel:     ErrOCCConflictIntegrity,
			expectedType: OCCConflictIntegrity,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.sentinel.Type != tt.expectedType {
				t.Errorf("sentinel.Type = %v, want %v", tt.sentinel.Type, tt.expectedType)
			}
		})
	}
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestConflictError_EmptyNodeID(t *testing.T) {
	err := NewStaleConflictError("", 1, 2, "session-1")

	expected := "stale read: node  version 1, now 2"
	if err.Error() != expected {
		t.Errorf("Error() = %q, want %q", err.Error(), expected)
	}
}

func TestConflictError_EmptySessionID(t *testing.T) {
	err := NewStaleConflictError("node-1", 1, 2, "")

	if err.SessionID != "" {
		t.Errorf("SessionID = %q, want empty string", err.SessionID)
	}
}

func TestConflictError_ZeroVersions(t *testing.T) {
	err := NewStaleConflictError("node-1", 0, 0, "session-1")

	expected := "stale read: node node-1 version 0, now 0"
	if err.Error() != expected {
		t.Errorf("Error() = %q, want %q", err.Error(), expected)
	}
}

func TestConflictError_LargeVersions(t *testing.T) {
	maxUint64 := ^uint64(0)
	err := NewStaleConflictError("node-1", maxUint64, maxUint64, "session-1")

	expected := fmt.Sprintf("stale read: node node-1 version %d, now %d", maxUint64, maxUint64)
	if err.Error() != expected {
		t.Errorf("Error() = %q, want %q", err.Error(), expected)
	}
}

func TestConflictError_SpecialCharactersInNodeID(t *testing.T) {
	nodeID := "node/with:special-chars_123"
	err := NewStaleConflictError(nodeID, 1, 2, "session-1")

	expected := fmt.Sprintf("stale read: node %s version 1, now 2", nodeID)
	if err.Error() != expected {
		t.Errorf("Error() = %q, want %q", err.Error(), expected)
	}
}

func TestConflictError_ImplementsErrorInterface(t *testing.T) {
	var _ error = (*ConflictError)(nil)
}

func TestConflictError_SameTypeMatchesAcrossInstances(t *testing.T) {
	err1 := NewStaleConflictError("node-1", 1, 2, "session-1")
	err2 := NewStaleConflictError("node-2", 3, 4, "session-2")

	if !errors.Is(err1, err2) {
		t.Error("Two stale errors with different data should match with errors.Is()")
	}
}
