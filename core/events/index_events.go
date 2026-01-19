package events

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

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

// =============================================================================
// IndexEventPublisher - Publishes index events to the ActivityEventBus
// =============================================================================

// IndexEventPublisher publishes index-related events to the activity event bus.
// It bridges the gap between the indexing system and the event-driven architecture.
type IndexEventPublisher struct {
	// bus is the activity event bus to publish events to
	bus *ActivityEventBus

	// sessionID is the session this publisher is associated with
	sessionID string
}

// NewIndexEventPublisher creates a new IndexEventPublisher.
func NewIndexEventPublisher(bus *ActivityEventBus, sessionID string) *IndexEventPublisher {
	return &IndexEventPublisher{
		bus:       bus,
		sessionID: sessionID,
	}
}

// PublishIndexStart publishes an index start event.
func (p *IndexEventPublisher) PublishIndexStart(indexType IndexType, rootPath string) error {
	if p.bus == nil {
		return fmt.Errorf("event bus is nil")
	}

	content := fmt.Sprintf("Index started: type=%s, path=%s", indexType.String(), rootPath)

	event := NewActivityEvent(EventTypeIndexStart, p.sessionID, content)
	event.Category = "indexing"
	event.FilePaths = []string{rootPath}
	event.Keywords = []string{"index", indexType.String(), "start"}

	// Store structured data
	event.Data["index_type"] = indexType.String()
	event.Data["root_path"] = rootPath

	p.bus.Publish(event)
	return nil
}

// PublishIndexComplete publishes an index complete event with statistics.
func (p *IndexEventPublisher) PublishIndexComplete(indexEvent *IndexEvent) error {
	if p.bus == nil {
		return fmt.Errorf("event bus is nil")
	}

	if indexEvent == nil {
		return fmt.Errorf("index event is nil")
	}

	content := fmt.Sprintf("Index completed: type=%s, files_indexed=%d, files_removed=%d, files_updated=%d, entities=%d, edges=%d, duration=%v",
		indexEvent.IndexType.String(),
		indexEvent.FilesIndexed,
		indexEvent.FilesRemoved,
		indexEvent.FilesUpdated,
		indexEvent.EntitiesFound,
		indexEvent.EdgesCreated,
		indexEvent.Duration,
	)

	event := NewActivityEvent(EventTypeIndexComplete, p.sessionID, content)
	event.Category = "indexing"
	event.FilePaths = []string{indexEvent.RootPath}
	event.Keywords = []string{"index", indexEvent.IndexType.String(), "complete"}

	// Determine outcome based on errors
	if len(indexEvent.Errors) > 0 {
		event.Outcome = OutcomeFailure
	} else {
		event.Outcome = OutcomeSuccess
	}

	// Store statistics in data
	event.Data["index_type"] = indexEvent.IndexType.String()
	event.Data["root_path"] = indexEvent.RootPath
	event.Data["duration_ms"] = indexEvent.Duration.Milliseconds()
	event.Data["files_indexed"] = indexEvent.FilesIndexed
	event.Data["files_removed"] = indexEvent.FilesRemoved
	event.Data["files_updated"] = indexEvent.FilesUpdated
	event.Data["entities_found"] = indexEvent.EntitiesFound
	event.Data["edges_created"] = indexEvent.EdgesCreated
	event.Data["index_version"] = indexEvent.IndexVersion
	event.Data["prev_version"] = indexEvent.PrevVersion

	// Include error details if present
	if len(indexEvent.Errors) > 0 {
		errorData, _ := json.Marshal(indexEvent.Errors)
		event.Data["errors"] = string(errorData)
		event.Data["error_count"] = len(indexEvent.Errors)
	}

	p.bus.Publish(event)
	return nil
}

// PublishFileAdd publishes an event for a file being added to the index.
func (p *IndexEventPublisher) PublishFileAdd(filePath string) error {
	if p.bus == nil {
		return fmt.Errorf("event bus is nil")
	}

	content := fmt.Sprintf("File added to index: %s", filePath)

	event := NewActivityEvent(EventTypeIndexFileAdded, p.sessionID, content)
	event.Category = "indexing"
	event.FilePaths = []string{filePath}
	event.Keywords = []string{"index", "file", "add"}
	event.Outcome = OutcomeSuccess

	event.Data["file_path"] = filePath
	event.Data["operation"] = "add"

	p.bus.Publish(event)
	return nil
}

// PublishFileRemove publishes an event for a file being removed from the index.
func (p *IndexEventPublisher) PublishFileRemove(filePath string) error {
	if p.bus == nil {
		return fmt.Errorf("event bus is nil")
	}

	content := fmt.Sprintf("File removed from index: %s", filePath)

	event := NewActivityEvent(EventTypeIndexFileRemoved, p.sessionID, content)
	event.Category = "indexing"
	event.FilePaths = []string{filePath}
	event.Keywords = []string{"index", "file", "remove"}
	event.Outcome = OutcomeSuccess

	event.Data["file_path"] = filePath
	event.Data["operation"] = "remove"

	p.bus.Publish(event)
	return nil
}

// PublishError publishes an indexing error event.
func (p *IndexEventPublisher) PublishError(err error, filePath string) error {
	if p.bus == nil {
		return fmt.Errorf("event bus is nil")
	}

	if err == nil {
		return fmt.Errorf("error is nil")
	}

	content := fmt.Sprintf("Index error for %s: %s", filePath, err.Error())

	event := NewActivityEvent(EventTypeIndexError, p.sessionID, content)
	event.Category = "indexing"
	event.Outcome = OutcomeFailure
	event.Keywords = []string{"index", "error"}

	if filePath != "" {
		event.FilePaths = []string{filePath}
	}

	event.Data["error_message"] = err.Error()
	event.Data["file_path"] = filePath

	p.bus.Publish(event)
	return nil
}

// =============================================================================
// IndexerEventHook - Interface for StartupIndexer integration
// =============================================================================

// IndexerEventHook defines the interface for receiving indexing events.
// This interface can be implemented by components that need to react to
// indexing operations, such as the StartupIndexer.
type IndexerEventHook interface {
	// OnIndexStart is called when indexing starts.
	OnIndexStart(indexType IndexType, rootPath string)

	// OnIndexComplete is called when indexing completes.
	OnIndexComplete(event *IndexEvent)

	// OnFileAdd is called when a file is added to the index.
	OnFileAdd(filePath string)

	// OnFileRemove is called when a file is removed from the index.
	OnFileRemove(filePath string)

	// OnError is called when an error occurs during indexing.
	OnError(err error, filePath string)
}

// =============================================================================
// IndexEventPublisherHook - Adapter implementing IndexerEventHook
// =============================================================================

// IndexEventPublisherHook adapts IndexEventPublisher to the IndexerEventHook interface.
// This allows the IndexEventPublisher to be used as a hook in the StartupIndexer.
type IndexEventPublisherHook struct {
	publisher *IndexEventPublisher
}

// NewIndexEventPublisherHook creates a new IndexEventPublisherHook.
func NewIndexEventPublisherHook(publisher *IndexEventPublisher) *IndexEventPublisherHook {
	return &IndexEventPublisherHook{
		publisher: publisher,
	}
}

// OnIndexStart implements IndexerEventHook.
func (h *IndexEventPublisherHook) OnIndexStart(indexType IndexType, rootPath string) {
	if h.publisher != nil {
		_ = h.publisher.PublishIndexStart(indexType, rootPath)
	}
}

// OnIndexComplete implements IndexerEventHook.
func (h *IndexEventPublisherHook) OnIndexComplete(event *IndexEvent) {
	if h.publisher != nil {
		_ = h.publisher.PublishIndexComplete(event)
	}
}

// OnFileAdd implements IndexerEventHook.
func (h *IndexEventPublisherHook) OnFileAdd(filePath string) {
	if h.publisher != nil {
		_ = h.publisher.PublishFileAdd(filePath)
	}
}

// OnFileRemove implements IndexerEventHook.
func (h *IndexEventPublisherHook) OnFileRemove(filePath string) {
	if h.publisher != nil {
		_ = h.publisher.PublishFileRemove(filePath)
	}
}

// OnError implements IndexerEventHook.
func (h *IndexEventPublisherHook) OnError(err error, filePath string) {
	if h.publisher != nil {
		_ = h.publisher.PublishError(err, filePath)
	}
}

// =============================================================================
// NoOpIndexerEventHook - No-operation implementation
// =============================================================================

// NoOpIndexerEventHook is a no-operation implementation of IndexerEventHook.
// Use this when you don't need to handle indexing events.
type NoOpIndexerEventHook struct{}

// NewNoOpIndexerEventHook creates a new NoOpIndexerEventHook.
func NewNoOpIndexerEventHook() *NoOpIndexerEventHook {
	return &NoOpIndexerEventHook{}
}

// OnIndexStart implements IndexerEventHook (no-op).
func (h *NoOpIndexerEventHook) OnIndexStart(indexType IndexType, rootPath string) {}

// OnIndexComplete implements IndexerEventHook (no-op).
func (h *NoOpIndexerEventHook) OnIndexComplete(event *IndexEvent) {}

// OnFileAdd implements IndexerEventHook (no-op).
func (h *NoOpIndexerEventHook) OnFileAdd(filePath string) {}

// OnFileRemove implements IndexerEventHook (no-op).
func (h *NoOpIndexerEventHook) OnFileRemove(filePath string) {}

// OnError implements IndexerEventHook (no-op).
func (h *NoOpIndexerEventHook) OnError(err error, filePath string) {}

// =============================================================================
// Helper Functions
// =============================================================================

// NewIndexEvent creates a new IndexEvent with generated ID and timestamp.
func NewIndexEvent(eventType IndexEventType, sessionID string, indexType IndexType, rootPath string) *IndexEvent {
	return &IndexEvent{
		ID:        uuid.New().String(),
		EventType: eventType,
		Timestamp: time.Now(),
		SessionID: sessionID,
		IndexType: indexType,
		RootPath:  rootPath,
		Errors:    make([]IndexError, 0),
	}
}
