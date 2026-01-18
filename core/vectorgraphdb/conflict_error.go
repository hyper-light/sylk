package vectorgraphdb

import (
	"errors"
	"fmt"
)

// =============================================================================
// Optimistic Concurrency Control (OCC) Conflict Types
// =============================================================================

// OCCConflictType represents the type of concurrency conflict in OCC operations.
// This is distinct from ConflictType which handles data conflicts.
type OCCConflictType int

const (
	// OCCConflictStale indicates a read of an outdated version.
	OCCConflictStale OCCConflictType = iota

	// OCCConflictConcurrent indicates a concurrent write was detected.
	OCCConflictConcurrent

	// OCCConflictDeleted indicates the node was deleted.
	OCCConflictDeleted

	// OCCConflictIntegrity indicates a hash mismatch.
	OCCConflictIntegrity
)

// occConflictTypeNames maps OCCConflictType values to their string representations.
var occConflictTypeNames = map[OCCConflictType]string{
	OCCConflictStale:      "stale",
	OCCConflictConcurrent: "concurrent",
	OCCConflictDeleted:    "deleted",
	OCCConflictIntegrity:  "integrity",
}

// String returns a human-readable string for the OCC conflict type.
func (ct OCCConflictType) String() string {
	if name, ok := occConflictTypeNames[ct]; ok {
		return name
	}
	return fmt.Sprintf("occ_conflict_type(%d)", ct)
}

// =============================================================================
// Conflict Error
// =============================================================================

// ConflictError represents a concurrency conflict during optimistic concurrency control.
// It implements the error interface and supports errors.Is() for comparison.
type ConflictError struct {
	NodeID      string
	ReadVersion uint64
	CurrVersion uint64
	Type        OCCConflictType
	SessionID   string
}

// errorFormatters maps OCCConflictType to a function that formats the error message.
var errorFormatters = map[OCCConflictType]func(e *ConflictError) string{
	OCCConflictStale:      formatStaleError,
	OCCConflictConcurrent: formatConcurrentError,
	OCCConflictDeleted:    formatDeletedError,
	OCCConflictIntegrity:  formatIntegrityError,
}

func formatStaleError(e *ConflictError) string {
	return fmt.Sprintf("stale read: node %s version %d, now %d",
		e.NodeID, e.ReadVersion, e.CurrVersion)
}

func formatConcurrentError(e *ConflictError) string {
	return fmt.Sprintf("concurrent modification: node %s", e.NodeID)
}

func formatDeletedError(e *ConflictError) string {
	return fmt.Sprintf("node deleted: %s", e.NodeID)
}

func formatIntegrityError(e *ConflictError) string {
	return fmt.Sprintf("integrity error: node %s hash mismatch", e.NodeID)
}

// Error implements the error interface.
func (e *ConflictError) Error() string {
	if formatter, ok := errorFormatters[e.Type]; ok {
		return formatter(e)
	}
	return fmt.Sprintf("conflict on node %s", e.NodeID)
}

// Is implements errors.Is() support for comparing ConflictError instances.
// Two ConflictErrors are considered equal if they have the same Type.
func (e *ConflictError) Is(target error) bool {
	var t *ConflictError
	if errors.As(target, &t) {
		return e.Type == t.Type
	}
	return false
}

// Unwrap returns nil as ConflictError does not wrap another error.
func (e *ConflictError) Unwrap() error {
	return nil
}

// =============================================================================
// Helper Functions for Creating Specific Conflict Errors
// =============================================================================

// NewStaleConflictError creates a ConflictError for stale read conflicts.
func NewStaleConflictError(nodeID string, readVersion, currVersion uint64, sessionID string) *ConflictError {
	return &ConflictError{
		NodeID:      nodeID,
		ReadVersion: readVersion,
		CurrVersion: currVersion,
		Type:        OCCConflictStale,
		SessionID:   sessionID,
	}
}

// NewConcurrentConflictError creates a ConflictError for concurrent modification conflicts.
func NewConcurrentConflictError(nodeID string, sessionID string) *ConflictError {
	return &ConflictError{
		NodeID:    nodeID,
		Type:      OCCConflictConcurrent,
		SessionID: sessionID,
	}
}

// NewDeletedConflictError creates a ConflictError for deleted node conflicts.
func NewDeletedConflictError(nodeID string, sessionID string) *ConflictError {
	return &ConflictError{
		NodeID:    nodeID,
		Type:      OCCConflictDeleted,
		SessionID: sessionID,
	}
}

// NewIntegrityConflictError creates a ConflictError for integrity/hash mismatch conflicts.
func NewIntegrityConflictError(nodeID string, sessionID string) *ConflictError {
	return &ConflictError{
		NodeID:    nodeID,
		Type:      OCCConflictIntegrity,
		SessionID: sessionID,
	}
}

// =============================================================================
// Sentinel Errors for errors.Is() Comparisons
// =============================================================================

var (
	// ErrOCCConflictStale is a sentinel error for stale read conflicts.
	ErrOCCConflictStale = &ConflictError{Type: OCCConflictStale}

	// ErrOCCConflictConcurrent is a sentinel error for concurrent modification conflicts.
	ErrOCCConflictConcurrent = &ConflictError{Type: OCCConflictConcurrent}

	// ErrOCCConflictDeleted is a sentinel error for deleted node conflicts.
	ErrOCCConflictDeleted = &ConflictError{Type: OCCConflictDeleted}

	// ErrOCCConflictIntegrity is a sentinel error for integrity conflicts.
	ErrOCCConflictIntegrity = &ConflictError{Type: OCCConflictIntegrity}
)

// IsConflictError returns true if the error is a ConflictError.
func IsConflictError(err error) bool {
	var conflictErr *ConflictError
	return errors.As(err, &conflictErr)
}

// GetConflictError extracts the ConflictError from an error if present.
func GetConflictError(err error) (*ConflictError, bool) {
	var conflictErr *ConflictError
	if errors.As(err, &conflictErr) {
		return conflictErr, true
	}
	return nil, false
}
