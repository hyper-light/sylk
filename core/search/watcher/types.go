// Package watcher provides file change detection functionality for the Sylk
// Document Search System.
package watcher

import "time"

// =============================================================================
// FileOperation
// =============================================================================

// FileOperation represents the type of file operation detected.
type FileOperation int

const (
	// OpCreate indicates a file was created.
	OpCreate FileOperation = iota

	// OpModify indicates a file was modified.
	OpModify

	// OpDelete indicates a file was deleted.
	OpDelete

	// OpRename indicates a file was renamed.
	OpRename
)

// String returns a human-readable name for the file operation.
func (op FileOperation) String() string {
	switch op {
	case OpCreate:
		return "create"
	case OpModify:
		return "modify"
	case OpDelete:
		return "delete"
	case OpRename:
		return "rename"
	default:
		return "unknown"
	}
}

// =============================================================================
// ChangeSource
// =============================================================================

// ChangeSource indicates where a change was detected.
// Lower values indicate higher priority.
type ChangeSource int

const (
	// SourceFSNotify is the highest priority - real-time filesystem events.
	SourceFSNotify ChangeSource = iota

	// SourceGitHook is medium priority - git operation events.
	SourceGitHook

	// SourcePeriodic is lowest priority - periodic scanning events.
	SourcePeriodic
)

// String returns a human-readable name for the change source.
func (s ChangeSource) String() string {
	switch s {
	case SourceFSNotify:
		return "fsnotify"
	case SourceGitHook:
		return "git_hook"
	case SourcePeriodic:
		return "periodic"
	default:
		return "unknown"
	}
}

// =============================================================================
// ChangeEvent
// =============================================================================

// ChangeEvent represents a detected file change from any source.
type ChangeEvent struct {
	// Path is the absolute path to the changed file.
	Path string

	// Operation is the type of change detected.
	Operation FileOperation

	// Source indicates where the change was detected.
	Source ChangeSource

	// Time is when the event was detected.
	Time time.Time
}
