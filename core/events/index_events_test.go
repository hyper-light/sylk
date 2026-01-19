package events

import (
	"fmt"
	"sync"
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

// =============================================================================
// IndexEventPublisher Tests
// =============================================================================

func TestNewIndexEventPublisher(t *testing.T) {
	bus := NewActivityEventBus(100)
	defer bus.Close()

	publisher := NewIndexEventPublisher(bus, "test-session")

	if publisher == nil {
		t.Fatal("Expected non-nil publisher")
	}

	if publisher.bus != bus {
		t.Error("Expected bus to be set")
	}

	if publisher.sessionID != "test-session" {
		t.Errorf("Expected sessionID 'test-session', got '%s'", publisher.sessionID)
	}
}

func TestIndexEventPublisher_PublishIndexStart(t *testing.T) {
	bus := NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	// Create subscriber to capture events
	sub := &mockIndexSubscriber{
		id:         "test-sub",
		eventTypes: []EventType{EventTypeIndexStart},
	}
	bus.Subscribe(sub)

	publisher := NewIndexEventPublisher(bus, "test-session")

	err := publisher.PublishIndexStart(IndexTypeFull, "/project/root")
	if err != nil {
		t.Fatalf("PublishIndexStart returned error: %v", err)
	}

	// Wait for event delivery
	time.Sleep(100 * time.Millisecond)

	events := sub.getEvents()
	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}

	event := events[0]

	// Verify event type
	if event.EventType != EventTypeIndexStart {
		t.Errorf("Expected event type %s, got %s", EventTypeIndexStart, event.EventType)
	}

	// Verify session ID
	if event.SessionID != "test-session" {
		t.Errorf("Expected sessionID 'test-session', got '%s'", event.SessionID)
	}

	// Verify content
	if event.Content == "" {
		t.Error("Expected non-empty content")
	}

	// Verify category
	if event.Category != "indexing" {
		t.Errorf("Expected category 'indexing', got '%s'", event.Category)
	}

	// Verify data
	if event.Data["index_type"] != "full" {
		t.Errorf("Expected index_type 'full', got '%v'", event.Data["index_type"])
	}

	if event.Data["root_path"] != "/project/root" {
		t.Errorf("Expected root_path '/project/root', got '%v'", event.Data["root_path"])
	}

	// Verify file paths
	if len(event.FilePaths) != 1 || event.FilePaths[0] != "/project/root" {
		t.Errorf("Expected FilePaths ['/project/root'], got %v", event.FilePaths)
	}
}

func TestIndexEventPublisher_PublishIndexStart_NilBus(t *testing.T) {
	publisher := &IndexEventPublisher{
		bus:       nil,
		sessionID: "test-session",
	}

	err := publisher.PublishIndexStart(IndexTypeFull, "/project/root")
	if err == nil {
		t.Error("Expected error for nil bus")
	}
}

func TestIndexEventPublisher_PublishIndexComplete(t *testing.T) {
	bus := NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	// Create subscriber to capture events
	sub := &mockIndexSubscriber{
		id:         "test-sub",
		eventTypes: []EventType{EventTypeIndexComplete},
	}
	bus.Subscribe(sub)

	publisher := NewIndexEventPublisher(bus, "test-session")

	indexEvent := &IndexEvent{
		ID:            "idx-1",
		EventType:     IndexEventTypeComplete,
		Timestamp:     time.Now(),
		SessionID:     "test-session",
		IndexType:     IndexTypeFull,
		IndexVersion:  1,
		PrevVersion:   0,
		RootPath:      "/project/root",
		Duration:      5 * time.Second,
		FilesIndexed:  100,
		FilesRemoved:  10,
		FilesUpdated:  20,
		EntitiesFound: 500,
		EdgesCreated:  250,
		Errors:        []IndexError{},
	}

	err := publisher.PublishIndexComplete(indexEvent)
	if err != nil {
		t.Fatalf("PublishIndexComplete returned error: %v", err)
	}

	// Wait for event delivery
	time.Sleep(100 * time.Millisecond)

	events := sub.getEvents()
	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}

	event := events[0]

	// Verify event type
	if event.EventType != EventTypeIndexComplete {
		t.Errorf("Expected event type %s, got %s", EventTypeIndexComplete, event.EventType)
	}

	// Verify outcome (success when no errors)
	if event.Outcome != OutcomeSuccess {
		t.Errorf("Expected outcome success, got %s", event.Outcome)
	}

	// Verify statistics in data
	if event.Data["files_indexed"] != 100 {
		t.Errorf("Expected files_indexed 100, got %v", event.Data["files_indexed"])
	}

	if event.Data["files_removed"] != 10 {
		t.Errorf("Expected files_removed 10, got %v", event.Data["files_removed"])
	}

	if event.Data["files_updated"] != 20 {
		t.Errorf("Expected files_updated 20, got %v", event.Data["files_updated"])
	}

	if event.Data["entities_found"] != 500 {
		t.Errorf("Expected entities_found 500, got %v", event.Data["entities_found"])
	}

	if event.Data["edges_created"] != 250 {
		t.Errorf("Expected edges_created 250, got %v", event.Data["edges_created"])
	}

	if event.Data["duration_ms"] != int64(5000) {
		t.Errorf("Expected duration_ms 5000, got %v", event.Data["duration_ms"])
	}

	if event.Data["index_version"] != 1 {
		t.Errorf("Expected index_version 1, got %v", event.Data["index_version"])
	}

	if event.Data["prev_version"] != 0 {
		t.Errorf("Expected prev_version 0, got %v", event.Data["prev_version"])
	}
}

func TestIndexEventPublisher_PublishIndexComplete_WithErrors(t *testing.T) {
	bus := NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	// Create subscriber to capture events
	sub := &mockIndexSubscriber{
		id:         "test-sub",
		eventTypes: []EventType{EventTypeIndexComplete},
	}
	bus.Subscribe(sub)

	publisher := NewIndexEventPublisher(bus, "test-session")

	indexEvent := &IndexEvent{
		ID:           "idx-1",
		EventType:    IndexEventTypeComplete,
		Timestamp:    time.Now(),
		SessionID:    "test-session",
		IndexType:    IndexTypeFull,
		IndexVersion: 1,
		PrevVersion:  0,
		RootPath:     "/project/root",
		Duration:     5 * time.Second,
		FilesIndexed: 98,
		Errors: []IndexError{
			{FilePath: "/project/file1.go", Message: "parse error"},
			{FilePath: "/project/file2.go", Message: "syntax error"},
		},
	}

	err := publisher.PublishIndexComplete(indexEvent)
	if err != nil {
		t.Fatalf("PublishIndexComplete returned error: %v", err)
	}

	// Wait for event delivery
	time.Sleep(100 * time.Millisecond)

	events := sub.getEvents()
	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}

	event := events[0]

	// Verify outcome (failure when errors present)
	if event.Outcome != OutcomeFailure {
		t.Errorf("Expected outcome failure, got %s", event.Outcome)
	}

	// Verify error count
	if event.Data["error_count"] != 2 {
		t.Errorf("Expected error_count 2, got %v", event.Data["error_count"])
	}

	// Verify errors data is present
	if event.Data["errors"] == nil {
		t.Error("Expected errors data to be set")
	}
}

func TestIndexEventPublisher_PublishIndexComplete_NilBus(t *testing.T) {
	publisher := &IndexEventPublisher{
		bus:       nil,
		sessionID: "test-session",
	}

	indexEvent := &IndexEvent{
		ID:        "idx-1",
		EventType: IndexEventTypeComplete,
	}

	err := publisher.PublishIndexComplete(indexEvent)
	if err == nil {
		t.Error("Expected error for nil bus")
	}
}

func TestIndexEventPublisher_PublishIndexComplete_NilEvent(t *testing.T) {
	bus := NewActivityEventBus(100)
	defer bus.Close()

	publisher := NewIndexEventPublisher(bus, "test-session")

	err := publisher.PublishIndexComplete(nil)
	if err == nil {
		t.Error("Expected error for nil event")
	}
}

func TestIndexEventPublisher_PublishFileAdd(t *testing.T) {
	bus := NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	// Create subscriber to capture events
	sub := &mockIndexSubscriber{
		id:         "test-sub",
		eventTypes: []EventType{EventTypeIndexFileAdded},
	}
	bus.Subscribe(sub)

	publisher := NewIndexEventPublisher(bus, "test-session")

	err := publisher.PublishFileAdd("/project/new_file.go")
	if err != nil {
		t.Fatalf("PublishFileAdd returned error: %v", err)
	}

	// Wait for event delivery
	time.Sleep(100 * time.Millisecond)

	events := sub.getEvents()
	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}

	event := events[0]

	// Verify event type
	if event.EventType != EventTypeIndexFileAdded {
		t.Errorf("Expected event type %s, got %s", EventTypeIndexFileAdded, event.EventType)
	}

	// Verify session ID
	if event.SessionID != "test-session" {
		t.Errorf("Expected sessionID 'test-session', got '%s'", event.SessionID)
	}

	// Verify outcome
	if event.Outcome != OutcomeSuccess {
		t.Errorf("Expected outcome success, got %s", event.Outcome)
	}

	// Verify file path
	if len(event.FilePaths) != 1 || event.FilePaths[0] != "/project/new_file.go" {
		t.Errorf("Expected FilePaths ['/project/new_file.go'], got %v", event.FilePaths)
	}

	// Verify data
	if event.Data["file_path"] != "/project/new_file.go" {
		t.Errorf("Expected file_path '/project/new_file.go', got '%v'", event.Data["file_path"])
	}

	if event.Data["operation"] != "add" {
		t.Errorf("Expected operation 'add', got '%v'", event.Data["operation"])
	}
}

func TestIndexEventPublisher_PublishFileAdd_NilBus(t *testing.T) {
	publisher := &IndexEventPublisher{
		bus:       nil,
		sessionID: "test-session",
	}

	err := publisher.PublishFileAdd("/project/file.go")
	if err == nil {
		t.Error("Expected error for nil bus")
	}
}

func TestIndexEventPublisher_PublishFileRemove(t *testing.T) {
	bus := NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	// Create subscriber to capture events
	sub := &mockIndexSubscriber{
		id:         "test-sub",
		eventTypes: []EventType{EventTypeIndexFileRemoved},
	}
	bus.Subscribe(sub)

	publisher := NewIndexEventPublisher(bus, "test-session")

	err := publisher.PublishFileRemove("/project/deleted_file.go")
	if err != nil {
		t.Fatalf("PublishFileRemove returned error: %v", err)
	}

	// Wait for event delivery
	time.Sleep(100 * time.Millisecond)

	events := sub.getEvents()
	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}

	event := events[0]

	// Verify event type
	if event.EventType != EventTypeIndexFileRemoved {
		t.Errorf("Expected event type %s, got %s", EventTypeIndexFileRemoved, event.EventType)
	}

	// Verify outcome
	if event.Outcome != OutcomeSuccess {
		t.Errorf("Expected outcome success, got %s", event.Outcome)
	}

	// Verify file path
	if len(event.FilePaths) != 1 || event.FilePaths[0] != "/project/deleted_file.go" {
		t.Errorf("Expected FilePaths ['/project/deleted_file.go'], got %v", event.FilePaths)
	}

	// Verify data
	if event.Data["file_path"] != "/project/deleted_file.go" {
		t.Errorf("Expected file_path '/project/deleted_file.go', got '%v'", event.Data["file_path"])
	}

	if event.Data["operation"] != "remove" {
		t.Errorf("Expected operation 'remove', got '%v'", event.Data["operation"])
	}
}

func TestIndexEventPublisher_PublishFileRemove_NilBus(t *testing.T) {
	publisher := &IndexEventPublisher{
		bus:       nil,
		sessionID: "test-session",
	}

	err := publisher.PublishFileRemove("/project/file.go")
	if err == nil {
		t.Error("Expected error for nil bus")
	}
}

func TestIndexEventPublisher_PublishError(t *testing.T) {
	bus := NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	// Create subscriber to capture events
	sub := &mockIndexSubscriber{
		id:         "test-sub",
		eventTypes: []EventType{EventTypeIndexError},
	}
	bus.Subscribe(sub)

	publisher := NewIndexEventPublisher(bus, "test-session")

	testErr := fmt.Errorf("parse error: unexpected token")
	err := publisher.PublishError(testErr, "/project/bad_file.go")
	if err != nil {
		t.Fatalf("PublishError returned error: %v", err)
	}

	// Wait for event delivery
	time.Sleep(100 * time.Millisecond)

	events := sub.getEvents()
	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}

	event := events[0]

	// Verify event type
	if event.EventType != EventTypeIndexError {
		t.Errorf("Expected event type %s, got %s", EventTypeIndexError, event.EventType)
	}

	// Verify outcome
	if event.Outcome != OutcomeFailure {
		t.Errorf("Expected outcome failure, got %s", event.Outcome)
	}

	// Verify file path
	if len(event.FilePaths) != 1 || event.FilePaths[0] != "/project/bad_file.go" {
		t.Errorf("Expected FilePaths ['/project/bad_file.go'], got %v", event.FilePaths)
	}

	// Verify data
	if event.Data["error_message"] != "parse error: unexpected token" {
		t.Errorf("Expected error_message 'parse error: unexpected token', got '%v'", event.Data["error_message"])
	}

	if event.Data["file_path"] != "/project/bad_file.go" {
		t.Errorf("Expected file_path '/project/bad_file.go', got '%v'", event.Data["file_path"])
	}
}

func TestIndexEventPublisher_PublishError_EmptyFilePath(t *testing.T) {
	bus := NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	// Create wildcard subscriber to capture all events
	sub := &mockIndexSubscriber{
		id:         "test-sub",
		eventTypes: []EventType{},
	}
	bus.Subscribe(sub)

	publisher := NewIndexEventPublisher(bus, "test-session")

	testErr := fmt.Errorf("general indexing error")
	err := publisher.PublishError(testErr, "")
	if err != nil {
		t.Fatalf("PublishError returned error: %v", err)
	}

	// Wait for event delivery
	time.Sleep(100 * time.Millisecond)

	events := sub.getEvents()
	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}

	event := events[0]

	// Verify file paths is empty for empty file path
	if len(event.FilePaths) != 0 {
		t.Errorf("Expected empty FilePaths, got %v", event.FilePaths)
	}
}

func TestIndexEventPublisher_PublishError_NilBus(t *testing.T) {
	publisher := &IndexEventPublisher{
		bus:       nil,
		sessionID: "test-session",
	}

	err := publisher.PublishError(fmt.Errorf("test error"), "/project/file.go")
	if err == nil {
		t.Error("Expected error for nil bus")
	}
}

func TestIndexEventPublisher_PublishError_NilError(t *testing.T) {
	bus := NewActivityEventBus(100)
	defer bus.Close()

	publisher := NewIndexEventPublisher(bus, "test-session")

	err := publisher.PublishError(nil, "/project/file.go")
	if err == nil {
		t.Error("Expected error for nil error argument")
	}
}

// =============================================================================
// IndexerEventHook Interface Tests
// =============================================================================

func TestIndexerEventHook_Interface(t *testing.T) {
	// Test that IndexEventPublisherHook implements IndexerEventHook
	bus := NewActivityEventBus(100)
	defer bus.Close()

	publisher := NewIndexEventPublisher(bus, "test-session")
	hook := NewIndexEventPublisherHook(publisher)

	// Verify hook implements IndexerEventHook
	var _ IndexerEventHook = hook
}

func TestIndexEventPublisherHook_OnIndexStart(t *testing.T) {
	bus := NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	sub := &mockIndexSubscriber{
		id:         "test-sub",
		eventTypes: []EventType{EventTypeIndexStart},
	}
	bus.Subscribe(sub)

	publisher := NewIndexEventPublisher(bus, "test-session")
	hook := NewIndexEventPublisherHook(publisher)

	hook.OnIndexStart(IndexTypeFull, "/project/root")

	// Wait for event delivery
	time.Sleep(100 * time.Millisecond)

	events := sub.getEvents()
	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}

	if events[0].EventType != EventTypeIndexStart {
		t.Errorf("Expected EventTypeIndexStart, got %s", events[0].EventType)
	}
}

func TestIndexEventPublisherHook_OnIndexComplete(t *testing.T) {
	bus := NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	sub := &mockIndexSubscriber{
		id:         "test-sub",
		eventTypes: []EventType{EventTypeIndexComplete},
	}
	bus.Subscribe(sub)

	publisher := NewIndexEventPublisher(bus, "test-session")
	hook := NewIndexEventPublisherHook(publisher)

	indexEvent := &IndexEvent{
		ID:           "idx-1",
		EventType:    IndexEventTypeComplete,
		IndexType:    IndexTypeFull,
		RootPath:     "/project/root",
		FilesIndexed: 100,
	}

	hook.OnIndexComplete(indexEvent)

	// Wait for event delivery
	time.Sleep(100 * time.Millisecond)

	events := sub.getEvents()
	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}

	if events[0].EventType != EventTypeIndexComplete {
		t.Errorf("Expected EventTypeIndexComplete, got %s", events[0].EventType)
	}
}

func TestIndexEventPublisherHook_OnFileAdd(t *testing.T) {
	bus := NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	sub := &mockIndexSubscriber{
		id:         "test-sub",
		eventTypes: []EventType{EventTypeIndexFileAdded},
	}
	bus.Subscribe(sub)

	publisher := NewIndexEventPublisher(bus, "test-session")
	hook := NewIndexEventPublisherHook(publisher)

	hook.OnFileAdd("/project/new_file.go")

	// Wait for event delivery
	time.Sleep(100 * time.Millisecond)

	events := sub.getEvents()
	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}

	if events[0].EventType != EventTypeIndexFileAdded {
		t.Errorf("Expected EventTypeIndexFileAdded, got %s", events[0].EventType)
	}
}

func TestIndexEventPublisherHook_OnFileRemove(t *testing.T) {
	bus := NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	sub := &mockIndexSubscriber{
		id:         "test-sub",
		eventTypes: []EventType{EventTypeIndexFileRemoved},
	}
	bus.Subscribe(sub)

	publisher := NewIndexEventPublisher(bus, "test-session")
	hook := NewIndexEventPublisherHook(publisher)

	hook.OnFileRemove("/project/deleted_file.go")

	// Wait for event delivery
	time.Sleep(100 * time.Millisecond)

	events := sub.getEvents()
	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}

	if events[0].EventType != EventTypeIndexFileRemoved {
		t.Errorf("Expected EventTypeIndexFileRemoved, got %s", events[0].EventType)
	}
}

func TestIndexEventPublisherHook_OnError(t *testing.T) {
	bus := NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	sub := &mockIndexSubscriber{
		id:         "test-sub",
		eventTypes: []EventType{EventTypeIndexError},
	}
	bus.Subscribe(sub)

	publisher := NewIndexEventPublisher(bus, "test-session")
	hook := NewIndexEventPublisherHook(publisher)

	hook.OnError(fmt.Errorf("test error"), "/project/bad_file.go")

	// Wait for event delivery
	time.Sleep(100 * time.Millisecond)

	events := sub.getEvents()
	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}

	if events[0].EventType != EventTypeIndexError {
		t.Errorf("Expected EventTypeIndexError, got %s", events[0].EventType)
	}
}

func TestIndexEventPublisherHook_NilPublisher(t *testing.T) {
	hook := NewIndexEventPublisherHook(nil)

	// These should not panic even with nil publisher
	hook.OnIndexStart(IndexTypeFull, "/project/root")
	hook.OnIndexComplete(&IndexEvent{})
	hook.OnFileAdd("/project/file.go")
	hook.OnFileRemove("/project/file.go")
	hook.OnError(fmt.Errorf("error"), "/project/file.go")
}

// =============================================================================
// NoOpIndexerEventHook Tests
// =============================================================================

func TestNoOpIndexerEventHook(t *testing.T) {
	hook := NewNoOpIndexerEventHook()

	// Verify hook implements IndexerEventHook
	var _ IndexerEventHook = hook

	// These should not panic
	hook.OnIndexStart(IndexTypeFull, "/project/root")
	hook.OnIndexComplete(&IndexEvent{})
	hook.OnFileAdd("/project/file.go")
	hook.OnFileRemove("/project/file.go")
	hook.OnError(fmt.Errorf("error"), "/project/file.go")
}

// =============================================================================
// NewIndexEvent Helper Function Tests
// =============================================================================

func TestNewIndexEvent(t *testing.T) {
	event := NewIndexEvent(IndexEventTypeStart, "test-session", IndexTypeFull, "/project/root")

	if event == nil {
		t.Fatal("Expected non-nil event")
	}

	if event.ID == "" {
		t.Error("Expected non-empty ID")
	}

	if event.EventType != IndexEventTypeStart {
		t.Errorf("Expected event type start, got %s", event.EventType)
	}

	if event.SessionID != "test-session" {
		t.Errorf("Expected sessionID 'test-session', got '%s'", event.SessionID)
	}

	if event.IndexType != IndexTypeFull {
		t.Errorf("Expected index type full, got %s", event.IndexType)
	}

	if event.RootPath != "/project/root" {
		t.Errorf("Expected root path '/project/root', got '%s'", event.RootPath)
	}

	if event.Timestamp.IsZero() {
		t.Error("Expected non-zero timestamp")
	}

	if event.Errors == nil {
		t.Error("Expected non-nil errors slice")
	}
}

// =============================================================================
// Mock Subscriber for Index Event Tests
// =============================================================================

// mockIndexSubscriber implements EventSubscriber for testing index events
type mockIndexSubscriber struct {
	id         string
	eventTypes []EventType
	events     []*ActivityEvent
	mu         sync.Mutex
}

func (m *mockIndexSubscriber) ID() string {
	return m.id
}

func (m *mockIndexSubscriber) EventTypes() []EventType {
	return m.eventTypes
}

func (m *mockIndexSubscriber) OnEvent(event *ActivityEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
	return nil
}

func (m *mockIndexSubscriber) getEvents() []*ActivityEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*ActivityEvent{}, m.events...)
}
