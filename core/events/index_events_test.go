package events

import (
	"testing"
	"time"
)

// =============================================================================
// IndexEventType Tests
// =============================================================================

func TestIndexEventType_String(t *testing.T) {
	tests := []struct {
		eventType IndexEventType
		expected  string
	}{
		{IndexEventTypeStart, "start"},
		{IndexEventTypeComplete, "complete"},
		{IndexEventTypeFileAdd, "file_add"},
		{IndexEventTypeFileRemove, "file_remove"},
		{IndexEventTypeError, "error"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.eventType.String(); got != tt.expected {
				t.Errorf("String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// =============================================================================
// IndexType Tests
// =============================================================================

func TestIndexType_String(t *testing.T) {
	tests := []struct {
		indexType IndexType
		expected  string
	}{
		{IndexTypeFull, "full"},
		{IndexTypeIncremental, "incremental"},
		{IndexTypeFileChange, "file_change"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.indexType.String(); got != tt.expected {
				t.Errorf("String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// =============================================================================
// IndexError Tests
// =============================================================================

func TestIndexError_Creation(t *testing.T) {
	err := IndexError{
		FilePath: "/path/to/file.go",
		Message:  "parse error",
	}

	if err.FilePath != "/path/to/file.go" {
		t.Errorf("Expected FilePath /path/to/file.go, got %s", err.FilePath)
	}

	if err.Message != "parse error" {
		t.Errorf("Expected Message 'parse error', got %s", err.Message)
	}
}

// =============================================================================
// IndexEvent Tests
// =============================================================================

func TestIndexEvent_FullIndexOperation(t *testing.T) {
	now := time.Now()
	event := IndexEvent{
		ID:           "test-index-1",
		EventType:    IndexEventTypeStart,
		Timestamp:    now,
		SessionID:    "session-123",
		IndexType:    IndexTypeFull,
		IndexVersion: 1,
		PrevVersion:  0,
		RootPath:     "/project/root",
		Duration:     0,
	}

	// Validate start event
	if event.EventType != IndexEventTypeStart {
		t.Errorf("Expected event type start, got %s", event.EventType)
	}

	if event.IndexType != IndexTypeFull {
		t.Errorf("Expected index type full, got %s", event.IndexType)
	}

	if event.IndexVersion != 1 {
		t.Errorf("Expected version 1, got %d", event.IndexVersion)
	}

	// Simulate completion
	completeEvent := IndexEvent{
		ID:            "test-index-2",
		EventType:     IndexEventTypeComplete,
		Timestamp:     now.Add(5 * time.Second),
		SessionID:     "session-123",
		IndexType:     IndexTypeFull,
		IndexVersion:  1,
		PrevVersion:   0,
		RootPath:      "/project/root",
		Duration:      5 * time.Second,
		FilesIndexed:  100,
		FilesRemoved:  0,
		FilesUpdated:  0,
		EntitiesFound: 500,
		EdgesCreated:  250,
		Errors:        []IndexError{},
	}

	if completeEvent.EventType != IndexEventTypeComplete {
		t.Errorf("Expected event type complete, got %s", completeEvent.EventType)
	}

	if completeEvent.FilesIndexed != 100 {
		t.Errorf("Expected 100 files indexed, got %d", completeEvent.FilesIndexed)
	}

	if completeEvent.Duration != 5*time.Second {
		t.Errorf("Expected duration 5s, got %v", completeEvent.Duration)
	}
}

func TestIndexEvent_IncrementalIndexOperation(t *testing.T) {
	now := time.Now()
	event := IndexEvent{
		ID:           "test-index-3",
		EventType:    IndexEventTypeComplete,
		Timestamp:    now,
		SessionID:    "session-123",
		IndexType:    IndexTypeIncremental,
		IndexVersion: 2,
		PrevVersion:  1,
		RootPath:     "/project/root",
		Duration:     2 * time.Second,
		FilesIndexed: 10,
		FilesRemoved: 2,
		FilesUpdated: 5,
		EntitiesFound: 50,
		EdgesCreated:  25,
	}

	if event.IndexType != IndexTypeIncremental {
		t.Errorf("Expected incremental index type, got %s", event.IndexType)
	}

	if event.PrevVersion != 1 {
		t.Errorf("Expected previous version 1, got %d", event.PrevVersion)
	}

	if event.IndexVersion != 2 {
		t.Errorf("Expected current version 2, got %d", event.IndexVersion)
	}

	if event.FilesUpdated != 5 {
		t.Errorf("Expected 5 files updated, got %d", event.FilesUpdated)
	}
}

func TestIndexEvent_FileChangeOperation(t *testing.T) {
	now := time.Now()
	event := IndexEvent{
		ID:           "test-index-4",
		EventType:    IndexEventTypeFileAdd,
		Timestamp:    now,
		SessionID:    "session-123",
		IndexType:    IndexTypeFileChange,
		IndexVersion: 3,
		PrevVersion:  2,
		RootPath:     "/project/root",
		Duration:     100 * time.Millisecond,
		FilesIndexed: 1,
		FilesRemoved: 0,
		FilesUpdated: 0,
		EntitiesFound: 5,
		EdgesCreated:  3,
	}

	if event.EventType != IndexEventTypeFileAdd {
		t.Errorf("Expected file_add event type, got %s", event.EventType)
	}

	if event.IndexType != IndexTypeFileChange {
		t.Errorf("Expected file_change index type, got %s", event.IndexType)
	}

	if event.FilesIndexed != 1 {
		t.Errorf("Expected 1 file indexed, got %d", event.FilesIndexed)
	}
}

func TestIndexEvent_WithErrors(t *testing.T) {
	now := time.Now()
	errors := []IndexError{
		{
			FilePath: "/project/file1.go",
			Message:  "syntax error at line 10",
		},
		{
			FilePath: "/project/file2.go",
			Message:  "import cycle detected",
		},
	}

	event := IndexEvent{
		ID:           "test-index-5",
		EventType:    IndexEventTypeError,
		Timestamp:    now,
		SessionID:    "session-123",
		IndexType:    IndexTypeFull,
		IndexVersion: 1,
		PrevVersion:  0,
		RootPath:     "/project/root",
		Duration:     3 * time.Second,
		FilesIndexed: 98,
		FilesRemoved: 0,
		FilesUpdated: 0,
		EntitiesFound: 490,
		EdgesCreated:  245,
		Errors:        errors,
	}

	if event.EventType != IndexEventTypeError {
		t.Errorf("Expected error event type, got %s", event.EventType)
	}

	if len(event.Errors) != 2 {
		t.Errorf("Expected 2 errors, got %d", len(event.Errors))
	}

	if event.Errors[0].FilePath != "/project/file1.go" {
		t.Errorf("Expected first error file path /project/file1.go, got %s", event.Errors[0].FilePath)
	}

	if event.Errors[1].Message != "import cycle detected" {
		t.Errorf("Expected second error message 'import cycle detected', got %s", event.Errors[1].Message)
	}
}

func TestIndexEvent_FileRemoveOperation(t *testing.T) {
	now := time.Now()
	event := IndexEvent{
		ID:           "test-index-6",
		EventType:    IndexEventTypeFileRemove,
		Timestamp:    now,
		SessionID:    "session-123",
		IndexType:    IndexTypeFileChange,
		IndexVersion: 4,
		PrevVersion:  3,
		RootPath:     "/project/root",
		Duration:     50 * time.Millisecond,
		FilesIndexed: 0,
		FilesRemoved: 1,
		FilesUpdated: 0,
		EntitiesFound: 0,
		EdgesCreated:  0,
	}

	if event.EventType != IndexEventTypeFileRemove {
		t.Errorf("Expected file_remove event type, got %s", event.EventType)
	}

	if event.FilesRemoved != 1 {
		t.Errorf("Expected 1 file removed, got %d", event.FilesRemoved)
	}

	if event.FilesIndexed != 0 {
		t.Errorf("Expected 0 files indexed, got %d", event.FilesIndexed)
	}
}

func TestIndexEvent_Statistics(t *testing.T) {
	event := IndexEvent{
		ID:            "test-index-7",
		EventType:     IndexEventTypeComplete,
		Timestamp:     time.Now(),
		SessionID:     "session-123",
		IndexType:     IndexTypeFull,
		IndexVersion:  1,
		PrevVersion:   0,
		RootPath:      "/project/root",
		Duration:      10 * time.Second,
		FilesIndexed:  500,
		FilesRemoved:  20,
		FilesUpdated:  30,
		EntitiesFound: 2500,
		EdgesCreated:  1250,
	}

	// Verify all statistics are captured
	if event.FilesIndexed != 500 {
		t.Errorf("Expected 500 files indexed, got %d", event.FilesIndexed)
	}

	if event.FilesRemoved != 20 {
		t.Errorf("Expected 20 files removed, got %d", event.FilesRemoved)
	}

	if event.FilesUpdated != 30 {
		t.Errorf("Expected 30 files updated, got %d", event.FilesUpdated)
	}

	if event.EntitiesFound != 2500 {
		t.Errorf("Expected 2500 entities found, got %d", event.EntitiesFound)
	}

	if event.EdgesCreated != 1250 {
		t.Errorf("Expected 1250 edges created, got %d", event.EdgesCreated)
	}

	if event.Duration != 10*time.Second {
		t.Errorf("Expected duration 10s, got %v", event.Duration)
	}
}

func TestIndexEvent_VersionTracking(t *testing.T) {
	// Version 1: Full index
	v1 := IndexEvent{
		IndexVersion: 1,
		PrevVersion:  0,
		IndexType:    IndexTypeFull,
	}

	if v1.IndexVersion != 1 {
		t.Errorf("Expected version 1, got %d", v1.IndexVersion)
	}

	if v1.PrevVersion != 0 {
		t.Errorf("Expected previous version 0, got %d", v1.PrevVersion)
	}

	// Version 2: Incremental
	v2 := IndexEvent{
		IndexVersion: 2,
		PrevVersion:  1,
		IndexType:    IndexTypeIncremental,
	}

	if v2.IndexVersion != 2 {
		t.Errorf("Expected version 2, got %d", v2.IndexVersion)
	}

	if v2.PrevVersion != 1 {
		t.Errorf("Expected previous version 1, got %d", v2.PrevVersion)
	}

	// Verify version progression
	if v2.IndexVersion != v1.IndexVersion+1 {
		t.Error("Version should increment by 1")
	}

	if v2.PrevVersion != v1.IndexVersion {
		t.Error("PrevVersion should match previous IndexVersion")
	}
}
