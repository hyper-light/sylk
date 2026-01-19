package events

import "time"

// =============================================================================
// IndexEventType - Type of index event
// =============================================================================

// IndexEventType represents the type of index event
type IndexEventType string

const (
	// IndexEventTypeStart indicates indexing started
	IndexEventTypeStart IndexEventType = "start"

	// IndexEventTypeComplete indicates indexing completed successfully
	IndexEventTypeComplete IndexEventType = "complete"

	// IndexEventTypeFileAdd indicates a file was added to the index
	IndexEventTypeFileAdd IndexEventType = "file_add"

	// IndexEventTypeFileRemove indicates a file was removed from the index
	IndexEventTypeFileRemove IndexEventType = "file_remove"

	// IndexEventTypeError indicates an indexing error occurred
	IndexEventTypeError IndexEventType = "error"
)

// String returns the string representation of the index event type
func (t IndexEventType) String() string {
	return string(t)
}

// =============================================================================
// IndexType - Type of index operation
// =============================================================================

// IndexType represents the type of index operation
type IndexType string

const (
	// IndexTypeFull indicates a full index rebuild
	IndexTypeFull IndexType = "full"

	// IndexTypeIncremental indicates an incremental index update
	IndexTypeIncremental IndexType = "incremental"

	// IndexTypeFileChange indicates a single file change
	IndexTypeFileChange IndexType = "file_change"
)

// String returns the string representation of the index type
func (t IndexType) String() string {
	return string(t)
}

// =============================================================================
// IndexError - Error during indexing
// =============================================================================

// IndexError represents an error that occurred during indexing
type IndexError struct {
	// FilePath is the path of the file that caused the error
	FilePath string `json:"file_path"`

	// Message is the error message
	Message string `json:"message"`
}

// =============================================================================
// IndexEvent - Index operation event
// =============================================================================

// IndexEvent represents an index operation event
type IndexEvent struct {
	// ID is the unique event identifier
	ID string `json:"id"`

	// EventType is the type of index event
	EventType IndexEventType `json:"event_type"`

	// Timestamp is when the event occurred
	Timestamp time.Time `json:"timestamp"`

	// SessionID is the session this index operation belongs to
	SessionID string `json:"session_id"`

	// IndexType is the type of index operation
	IndexType IndexType `json:"index_type"`

	// IndexVersion is the version of the index after this operation
	IndexVersion int `json:"index_version"`

	// PrevVersion is the version of the index before this operation
	PrevVersion int `json:"prev_version"`

	// RootPath is the root path being indexed
	RootPath string `json:"root_path"`

	// Duration is how long the index operation took
	Duration time.Duration `json:"duration"`

	// FilesIndexed is the number of files indexed
	FilesIndexed int `json:"files_indexed"`

	// FilesRemoved is the number of files removed from the index
	FilesRemoved int `json:"files_removed"`

	// FilesUpdated is the number of files updated in the index
	FilesUpdated int `json:"files_updated"`

	// EntitiesFound is the number of code entities found
	EntitiesFound int `json:"entities_found"`

	// EdgesCreated is the number of graph edges created
	EdgesCreated int `json:"edges_created"`

	// Errors contains any errors that occurred during indexing
	Errors []IndexError `json:"errors,omitempty"`
}
