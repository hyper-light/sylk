// Package context provides AE.3.5 EvictionEventPublisher tests.
package context

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/events"
)

// =============================================================================
// AE.3.5 EvictionReason Tests
// =============================================================================

func TestEvictionReason_String(t *testing.T) {
	tests := []struct {
		reason   EvictionReason
		expected string
	}{
		{EvictionReasonMemoryPressure, "memory_pressure"},
		{EvictionReasonTokenLimit, "token_limit"},
		{EvictionReasonAge, "age"},
		{EvictionReasonLowRelevance, "low_relevance"},
		{EvictionReasonStrategyBased, "strategy_based"},
		{EvictionReasonManual, "manual"},
	}

	for _, tc := range tests {
		t.Run(tc.expected, func(t *testing.T) {
			if tc.reason.String() != tc.expected {
				t.Errorf("expected %s, got %s", tc.expected, tc.reason.String())
			}
		})
	}
}

// =============================================================================
// AE.3.5 EventDetailLevel Tests
// =============================================================================

func TestEventDetailLevel_String(t *testing.T) {
	tests := []struct {
		level    EventDetailLevel
		expected string
	}{
		{EventDetailNone, "none"},
		{EventDetailSummary, "summary"},
		{EventDetailStandard, "standard"},
		{EventDetailVerbose, "verbose"},
		{EventDetailLevel(99), "level(99)"},
	}

	for _, tc := range tests {
		t.Run(tc.expected, func(t *testing.T) {
			if tc.level.String() != tc.expected {
				t.Errorf("expected %s, got %s", tc.expected, tc.level.String())
			}
		})
	}
}

// =============================================================================
// AE.3.5 EvictionPublisherConfig Tests
// =============================================================================

func TestDefaultEvictionPublisherConfig(t *testing.T) {
	config := DefaultEvictionPublisherConfig()

	if config.DetailLevel != EventDetailStandard {
		t.Errorf("expected standard detail level, got %v", config.DetailLevel)
	}

	if config.BufferSize != 100 {
		t.Errorf("expected buffer size 100, got %d", config.BufferSize)
	}

	if config.MaxItemsPerEvent != 50 {
		t.Errorf("expected max items 50, got %d", config.MaxItemsPerEvent)
	}

	if config.IncludeMetadata {
		t.Error("expected include metadata to be false")
	}
}

// =============================================================================
// AE.3.5 EvictionEventPublisher Creation Tests
// =============================================================================

func TestNewEvictionEventPublisher(t *testing.T) {
	collector := events.NewTestActivityCollector()

	publisher := NewEvictionEventPublisher(collector, "session-1", "agent-1", nil)
	defer publisher.Close()

	if publisher == nil {
		t.Fatal("publisher should not be nil")
	}

	if publisher.GetDetailLevel() != EventDetailStandard {
		t.Errorf("expected standard detail level, got %v", publisher.GetDetailLevel())
	}
}

func TestNewEvictionEventPublisher_WithConfig(t *testing.T) {
	collector := events.NewTestActivityCollector()

	config := &EvictionPublisherConfig{
		DetailLevel:      EventDetailVerbose,
		BufferSize:       50,
		MaxItemsPerEvent: 25,
		IncludeMetadata:  true,
	}

	publisher := NewEvictionEventPublisher(collector, "session-1", "agent-1", config)
	defer publisher.Close()

	if publisher.GetDetailLevel() != EventDetailVerbose {
		t.Errorf("expected verbose detail level, got %v", publisher.GetDetailLevel())
	}
}

func TestNewEvictionEventPublisher_NilBus(t *testing.T) {
	publisher := NewEvictionEventPublisher(nil, "session-1", "agent-1", nil)
	defer publisher.Close()

	// Should not panic with nil bus
	err := publisher.PublishEvictionStarted("agent-1", EvictionReasonMemoryPressure, 1000)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// =============================================================================
// AE.3.5 PublishEvictionStarted Tests
// =============================================================================

func TestPublishEvictionStarted(t *testing.T) {
	collector := events.NewTestActivityCollector()

	publisher := NewEvictionEventPublisher(collector, "session-1", "agent-1", nil)
	defer publisher.Close()

	err := publisher.PublishEvictionStarted("agent-1", EvictionReasonMemoryPressure, 5000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	evts := collector.Events()
	if len(evts) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evts))
	}

	event := evts[0]
	if event.Category != "eviction" {
		t.Errorf("expected category 'eviction', got '%s'", event.Category)
	}

	if event.Data["phase"] != "started" {
		t.Errorf("expected phase 'started', got '%v'", event.Data["phase"])
	}

	if event.Data["target_tokens"] != 5000 {
		t.Errorf("expected target_tokens 5000, got '%v'", event.Data["target_tokens"])
	}
}

func TestPublishEvictionStarted_DetailNone(t *testing.T) {
	collector := events.NewTestActivityCollector()

	config := &EvictionPublisherConfig{
		DetailLevel: EventDetailNone,
		BufferSize:  100,
	}
	publisher := NewEvictionEventPublisher(collector, "session-1", "agent-1", config)
	defer publisher.Close()

	err := publisher.PublishEvictionStarted("agent-1", EvictionReasonMemoryPressure, 5000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if collector.EventCount() != 0 {
		t.Errorf("expected no events at detail level none, got %d", collector.EventCount())
	}
}

// =============================================================================
// AE.3.5 PublishItemEvicted Tests
// =============================================================================

func TestPublishItemEvicted(t *testing.T) {
	collector := events.NewTestActivityCollector()

	config := &EvictionPublisherConfig{
		DetailLevel: EventDetailVerbose,
		BufferSize:  100,
	}
	publisher := NewEvictionEventPublisher(collector, "session-1", "agent-1", config)
	defer publisher.Close()

	evt := &EvictionEvent{
		ItemID:     "item-123",
		ItemType:   ContentTypeCodeFile,
		Reason:     EvictionReasonAge,
		Timestamp:  time.Now(),
		TokenCount: 500,
		AgentID:    "agent-1",
		SessionID:  "session-1",
	}

	err := publisher.PublishItemEvicted(evt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	evts := collector.Events()
	if len(evts) == 0 {
		t.Fatal("expected at least one event")
	}

	event := evts[0]
	if event.Data["item_id"] != "item-123" {
		t.Errorf("expected item_id 'item-123', got '%v'", event.Data["item_id"])
	}

	if event.Data["item_type"] != "code_file" {
		t.Errorf("expected item_type 'code_file', got '%v'", event.Data["item_type"])
	}
}

func TestPublishItemEvicted_BelowVerbose(t *testing.T) {
	collector := events.NewTestActivityCollector()

	config := &EvictionPublisherConfig{
		DetailLevel: EventDetailStandard, // Below verbose
		BufferSize:  100,
	}
	publisher := NewEvictionEventPublisher(collector, "session-1", "agent-1", config)
	defer publisher.Close()

	evt := &EvictionEvent{
		ItemID:     "item-123",
		ItemType:   ContentTypeCodeFile,
		Reason:     EvictionReasonAge,
		TokenCount: 500,
	}

	err := publisher.PublishItemEvicted(evt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if collector.EventCount() != 0 {
		t.Errorf("expected no events below verbose level, got %d", collector.EventCount())
	}
}

func TestPublishItemEvicted_WithMetadata(t *testing.T) {
	collector := events.NewTestActivityCollector()

	config := &EvictionPublisherConfig{
		DetailLevel:     EventDetailVerbose,
		BufferSize:      100,
		IncludeMetadata: true,
	}
	publisher := NewEvictionEventPublisher(collector, "session-1", "agent-1", config)
	defer publisher.Close()

	evt := &EvictionEvent{
		ItemID:     "item-123",
		ItemType:   ContentTypeCodeFile,
		Reason:     EvictionReasonAge,
		TokenCount: 500,
		Metadata: map[string]any{
			"file_path": "/path/to/file.go",
		},
	}

	err := publisher.PublishItemEvicted(evt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	evts := collector.Events()
	if len(evts) == 0 {
		t.Fatal("expected at least one event")
	}

	event := evts[0]
	if event.Data["metadata"] == nil {
		t.Error("expected metadata to be included")
	}
}

// =============================================================================
// AE.3.5 PublishEvictionCompleted Tests
// =============================================================================

func TestPublishEvictionCompleted(t *testing.T) {
	collector := events.NewTestActivityCollector()

	publisher := NewEvictionEventPublisher(collector, "session-1", "agent-1", nil)
	defer publisher.Close()

	now := time.Now()
	batch := &EvictionBatchEvent{
		BatchID:     "batch-123",
		AgentID:     "agent-1",
		SessionID:   "session-1",
		StartTime:   now.Add(-1 * time.Second),
		EndTime:     now,
		TotalItems:  10,
		TotalTokens: 5000,
		Reason:      EvictionReasonTokenLimit,
	}

	err := publisher.PublishEvictionCompleted(batch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	evts := collector.Events()
	if len(evts) == 0 {
		t.Fatal("expected at least one event")
	}

	event := evts[0]
	if event.Data["phase"] != "completed" {
		t.Errorf("expected phase 'completed', got '%v'", event.Data["phase"])
	}

	if event.Data["total_items"] != 10 {
		t.Errorf("expected total_items 10, got '%v'", event.Data["total_items"])
	}

	if event.Outcome != 1 { // OutcomeSuccess = 1
		t.Errorf("expected success outcome (1), got %v", event.Outcome)
	}
}

func TestPublishEvictionCompleted_WithItems(t *testing.T) {
	collector := events.NewTestActivityCollector()

	config := &EvictionPublisherConfig{
		DetailLevel:      EventDetailVerbose,
		BufferSize:       100,
		MaxItemsPerEvent: 5,
	}
	publisher := NewEvictionEventPublisher(collector, "session-1", "agent-1", config)
	defer publisher.Close()

	now := time.Now()
	items := []EvictionEvent{
		{ItemID: "item-1", ItemType: ContentTypeCodeFile, TokenCount: 100},
		{ItemID: "item-2", ItemType: ContentTypeCodeFile, TokenCount: 200},
		{ItemID: "item-3", ItemType: ContentTypeCodeFile, TokenCount: 150},
	}

	batch := &EvictionBatchEvent{
		BatchID:     "batch-123",
		AgentID:     "agent-1",
		SessionID:   "session-1",
		StartTime:   now.Add(-1 * time.Second),
		EndTime:     now,
		TotalItems:  3,
		TotalTokens: 450,
		Reason:      EvictionReasonTokenLimit,
		Items:       items,
	}

	err := publisher.PublishEvictionCompleted(batch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	evts := collector.Events()
	if len(evts) == 0 {
		t.Fatal("expected at least one event")
	}

	event := evts[0]
	itemData, ok := event.Data["items"].([]map[string]any)
	if !ok {
		t.Fatal("expected items in event data")
	}

	if len(itemData) != 3 {
		t.Errorf("expected 3 items, got %d", len(itemData))
	}
}

func TestPublishEvictionCompleted_ItemsTruncated(t *testing.T) {
	collector := events.NewTestActivityCollector()

	config := &EvictionPublisherConfig{
		DetailLevel:      EventDetailVerbose,
		BufferSize:       100,
		MaxItemsPerEvent: 2, // Limit to 2 items
	}
	publisher := NewEvictionEventPublisher(collector, "session-1", "agent-1", config)
	defer publisher.Close()

	now := time.Now()
	items := []EvictionEvent{
		{ItemID: "item-1", ItemType: ContentTypeCodeFile, TokenCount: 100},
		{ItemID: "item-2", ItemType: ContentTypeCodeFile, TokenCount: 200},
		{ItemID: "item-3", ItemType: ContentTypeCodeFile, TokenCount: 150},
		{ItemID: "item-4", ItemType: ContentTypeCodeFile, TokenCount: 175},
	}

	batch := &EvictionBatchEvent{
		BatchID:     "batch-123",
		AgentID:     "agent-1",
		SessionID:   "session-1",
		StartTime:   now.Add(-1 * time.Second),
		EndTime:     now,
		TotalItems:  4,
		TotalTokens: 625,
		Reason:      EvictionReasonTokenLimit,
		Items:       items,
	}

	err := publisher.PublishEvictionCompleted(batch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	evts := collector.Events()
	if len(evts) == 0 {
		t.Fatal("expected at least one event")
	}

	event := evts[0]
	if event.Data["items_truncated"] != true {
		t.Error("expected items_truncated flag")
	}

	itemData := event.Data["items"].([]map[string]any)
	if len(itemData) != 2 {
		t.Errorf("expected 2 items after truncation, got %d", len(itemData))
	}
}

// =============================================================================
// AE.3.5 PublishEvictionFailed Tests
// =============================================================================

func TestPublishEvictionFailed(t *testing.T) {
	collector := events.NewTestActivityCollector()

	publisher := NewEvictionEventPublisher(collector, "session-1", "agent-1", nil)
	defer publisher.Close()

	testErr := errors.New("eviction failed: out of memory")
	err := publisher.PublishEvictionFailed("agent-1", EvictionReasonMemoryPressure, testErr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	evts := collector.Events()
	if len(evts) == 0 {
		t.Fatal("expected at least one event")
	}

	event := evts[0]
	if event.Data["phase"] != "failed" {
		t.Errorf("expected phase 'failed', got '%v'", event.Data["phase"])
	}

	if event.Outcome != 2 { // OutcomeFailure = 2
		t.Errorf("expected failure outcome (2), got %v", event.Outcome)
	}

	if event.Data["error"] != "eviction failed: out of memory" {
		t.Errorf("expected error message, got '%v'", event.Data["error"])
	}
}

// =============================================================================
// AE.3.5 PublishBatch Tests
// =============================================================================

func TestPublishBatch(t *testing.T) {
	collector := events.NewTestActivityCollector()

	publisher := NewEvictionEventPublisher(collector, "session-1", "agent-1", nil)
	defer publisher.Close()

	entries := []*ContentEntry{
		{ID: "entry-1", ContentType: ContentTypeCodeFile, TokenCount: 100},
		{ID: "entry-2", ContentType: ContentTypeToolResult, TokenCount: 200},
		{ID: "entry-3", ContentType: ContentTypeAssistantReply, TokenCount: 150},
	}

	err := publisher.PublishBatch("agent-1", EvictionReasonTokenLimit, entries, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	evts := collector.Events()
	if len(evts) == 0 {
		t.Fatal("expected at least 1 event")
	}

	// Verify we received at least one eviction event
	var foundEvictionEvent bool
	for _, e := range evts {
		if e.Category == "eviction" {
			foundEvictionEvent = true
			// If it's a complete event, verify the data
			if e.Data["phase"] == "completed" {
				if e.Data["total_items"] != 3 {
					t.Errorf("expected total_items 3, got %v", e.Data["total_items"])
				}
				if e.Data["total_tokens"] != 450 {
					t.Errorf("expected total_tokens 450, got %v", e.Data["total_tokens"])
				}
			}
		}
	}

	if !foundEvictionEvent {
		t.Error("expected at least one eviction event")
	}
}

func TestPublishBatch_VerboseLevel(t *testing.T) {
	collector := events.NewTestActivityCollector()

	config := &EvictionPublisherConfig{
		DetailLevel:      EventDetailVerbose,
		BufferSize:       100,
		MaxItemsPerEvent: 50,
	}
	publisher := NewEvictionEventPublisher(collector, "session-1", "agent-1", config)
	defer publisher.Close()

	entries := []*ContentEntry{
		{ID: "entry-1", ContentType: ContentTypeCodeFile, TokenCount: 100},
		{ID: "entry-2", ContentType: ContentTypeToolResult, TokenCount: 200},
	}

	err := publisher.PublishBatch("agent-1", EvictionReasonTokenLimit, entries, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	evts := collector.Events()
	if len(evts) == 0 {
		t.Fatal("expected at least 1 event at verbose level")
	}

	// Verify we got at least one eviction-related event
	var foundEviction bool
	for _, e := range evts {
		if e.Category == "eviction" {
			foundEviction = true
			break
		}
	}

	if !foundEviction {
		t.Error("expected at least one eviction event at verbose level")
	}
}

func TestPublishBatch_EmptyEntries(t *testing.T) {
	collector := events.NewTestActivityCollector()

	publisher := NewEvictionEventPublisher(collector, "session-1", "agent-1", nil)
	defer publisher.Close()

	err := publisher.PublishBatch("agent-1", EvictionReasonTokenLimit, []*ContentEntry{}, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if collector.EventCount() != 0 {
		t.Errorf("expected no events for empty batch, got %d", collector.EventCount())
	}
}

// =============================================================================
// AE.3.5 Configuration Update Tests
// =============================================================================

func TestSetDetailLevel(t *testing.T) {
	collector := events.NewTestActivityCollector()

	publisher := NewEvictionEventPublisher(collector, "session-1", "agent-1", nil)
	defer publisher.Close()

	if publisher.GetDetailLevel() != EventDetailStandard {
		t.Errorf("expected standard, got %v", publisher.GetDetailLevel())
	}

	publisher.SetDetailLevel(EventDetailVerbose)

	if publisher.GetDetailLevel() != EventDetailVerbose {
		t.Errorf("expected verbose, got %v", publisher.GetDetailLevel())
	}
}

func TestSetIncludeMetadata(t *testing.T) {
	collector := events.NewTestActivityCollector()

	publisher := NewEvictionEventPublisher(collector, "session-1", "agent-1", nil)
	defer publisher.Close()

	publisher.SetIncludeMetadata(true)
	// Can't directly check, but ensures no panic
}

func TestUpdateSession(t *testing.T) {
	collector := events.NewTestActivityCollector()

	publisher := NewEvictionEventPublisher(collector, "session-1", "agent-1", nil)
	defer publisher.Close()

	publisher.UpdateSession("session-2", "agent-2")

	err := publisher.PublishEvictionStarted("agent-2", EvictionReasonManual, 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	evts := collector.Events()
	if len(evts) == 0 {
		t.Fatal("expected at least one event")
	}

	if evts[0].SessionID != "session-2" {
		t.Errorf("expected session-2, got %s", evts[0].SessionID)
	}
}

// =============================================================================
// AE.3.5 Non-blocking Behavior Tests
// =============================================================================

func TestNonBlockingPublish(t *testing.T) {
	collector := events.NewTestActivityCollector()

	config := &EvictionPublisherConfig{
		DetailLevel: EventDetailVerbose,
		BufferSize:  5, // Very small buffer
	}
	publisher := NewEvictionEventPublisher(collector, "session-1", "agent-1", config)
	defer publisher.Close()

	// Publish more events than buffer can hold
	for i := 0; i < 100; i++ {
		evt := &EvictionEvent{
			ItemID:     "item",
			ItemType:   ContentTypeCodeFile,
			Reason:     EvictionReasonAge,
			TokenCount: 100,
		}
		err := publisher.PublishItemEvicted(evt)
		if err != nil {
			t.Errorf("publish should not error: %v", err)
		}
	}
	// Should not block - test completes successfully
}

func TestFlush(t *testing.T) {
	collector := events.NewTestActivityCollector()

	publisher := NewEvictionEventPublisher(collector, "session-1", "agent-1", nil)
	defer publisher.Close()

	// Publish event
	err := publisher.PublishEvictionStarted("agent-1", EvictionReasonMemoryPressure, 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Flush
	publisher.Flush()

	if collector.EventCount() == 0 {
		t.Error("expected event after flush")
	}
}

// =============================================================================
// AE.3.5 Hook Interface Tests
// =============================================================================

func TestEvictionHookInterface(t *testing.T) {
	collector := events.NewTestActivityCollector()

	publisher := NewEvictionEventPublisher(collector, "session-1", "agent-1", nil)
	defer publisher.Close()

	// Use as hook interface
	var hook EvictionHook = publisher

	// Test OnEvictionStarted
	hook.OnEvictionStarted("agent-1", EvictionReasonMemoryPressure, 1000)

	if collector.EventCount() == 0 {
		t.Fatal("expected at least one event from OnEvictionStarted")
	}

	collector.Reset()

	// Test OnEvictionCompleted
	batch := &EvictionBatchEvent{
		BatchID:     "batch-1",
		AgentID:     "agent-1",
		SessionID:   "session-1",
		StartTime:   time.Now().Add(-time.Second),
		EndTime:     time.Now(),
		TotalItems:  5,
		TotalTokens: 1000,
		Reason:      EvictionReasonMemoryPressure,
	}
	hook.OnEvictionCompleted(batch)

	if collector.EventCount() == 0 {
		t.Fatal("expected at least one event from OnEvictionCompleted")
	}

	collector.Reset()

	// Test OnEvictionFailed
	hook.OnEvictionFailed("agent-1", EvictionReasonMemoryPressure, errors.New("test error"))

	if collector.EventCount() == 0 {
		t.Fatal("expected at least one event from OnEvictionFailed")
	}
}

// =============================================================================
// AE.3.5 NoOpEvictionPublisher Tests
// =============================================================================

func TestNoOpEvictionPublisher(t *testing.T) {
	noop := NewNoOpEvictionPublisher()

	// Should not panic
	noop.OnEvictionStarted("agent-1", EvictionReasonMemoryPressure, 1000)

	noop.OnItemEvicted(&EvictionEvent{
		ItemID:   "item-1",
		ItemType: ContentTypeCodeFile,
	})

	noop.OnEvictionCompleted(&EvictionBatchEvent{
		BatchID: "batch-1",
	})

	noop.OnEvictionFailed("agent-1", EvictionReasonMemoryPressure, errors.New("test"))

	// Verify implements interface
	var _ EvictionHook = noop
}

// =============================================================================
// AE.3.5 Close and Cleanup Tests
// =============================================================================

func TestClose(t *testing.T) {
	collector := events.NewTestActivityCollector()

	publisher := NewEvictionEventPublisher(collector, "session-1", "agent-1", nil)

	// Publish some events
	for i := 0; i < 10; i++ {
		_ = publisher.PublishEvictionStarted("agent-1", EvictionReasonMemoryPressure, 1000)
	}

	// Close should complete without hanging
	done := make(chan bool)
	go func() {
		publisher.Close()
		done <- true
	}()

	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("Close timed out")
	}
}

func TestCloseMultipleTimes(t *testing.T) {
	collector := events.NewTestActivityCollector()

	publisher := NewEvictionEventPublisher(collector, "session-1", "agent-1", nil)

	// Close multiple times should not panic
	publisher.Close()
	publisher.Close()
	publisher.Close()
}

func TestPublishAfterClose(t *testing.T) {
	collector := events.NewTestActivityCollector()

	publisher := NewEvictionEventPublisher(collector, "session-1", "agent-1", nil)
	publisher.Close()

	// Should not error or panic
	err := publisher.PublishEvictionStarted("agent-1", EvictionReasonMemoryPressure, 1000)
	if err != nil {
		t.Errorf("publish after close should not error: %v", err)
	}
}

// =============================================================================
// AE.3.5 Concurrency Tests
// =============================================================================

func TestConcurrentPublish(t *testing.T) {
	collector := events.NewTestActivityCollector()

	config := &EvictionPublisherConfig{
		DetailLevel: EventDetailVerbose,
		BufferSize:  1000,
	}
	publisher := NewEvictionEventPublisher(collector, "session-1", "agent-1", config)
	defer publisher.Close()

	var wg sync.WaitGroup
	published := int64(0)

	// Spawn multiple goroutines publishing events
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				evt := &EvictionEvent{
					ItemID:     "item",
					ItemType:   ContentTypeCodeFile,
					Reason:     EvictionReasonAge,
					TokenCount: 100,
				}
				if err := publisher.PublishItemEvicted(evt); err == nil {
					atomic.AddInt64(&published, 1)
				}
			}
		}(i)
	}

	wg.Wait()
	publisher.Flush()

	// Should have published events without errors
	if published == 0 {
		t.Error("expected some events to be published")
	}

	t.Logf("Published %d events, collected %d", published, collector.EventCount())
}

func TestConcurrentConfigUpdate(t *testing.T) {
	collector := events.NewTestActivityCollector()

	publisher := NewEvictionEventPublisher(collector, "session-1", "agent-1", nil)
	defer publisher.Close()

	var wg sync.WaitGroup

	// Concurrent detail level updates
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				level := EventDetailLevel(j % 4)
				publisher.SetDetailLevel(level)
				_ = publisher.GetDetailLevel()
			}
		}(i)
	}

	// Concurrent publishing
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = publisher.PublishEvictionStarted("agent-1", EvictionReasonAge, 100)
			}
		}()
	}

	wg.Wait()
	// Test passes if no race conditions or panics
}
