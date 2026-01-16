package archivalist

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// EventLog Unit Tests
// =============================================================================

func TestEventLog_Append(t *testing.T) {
	el := newTestEventLog(t)

	event := makeEvent(EventTypeFileRead, "agent-1", "session-1", map[string]any{"path": "/test.go"})
	err := el.Append(event)

	require.NoError(t, err, "Append")
	assert.NotEmpty(t, event.ID, "Event ID should be set")
	assert.False(t, event.Timestamp.IsZero(), "Timestamp should be set")
	assert.Greater(t, event.Clock, uint64(0), "Clock should be set")
}

func TestEventLog_Append_AutoIncrementID(t *testing.T) {
	el := newTestEventLog(t)

	event1 := makeEvent(EventTypeFileRead, "agent-1", "session-1", nil)
	event2 := makeEvent(EventTypeFileRead, "agent-1", "session-1", nil)

	el.Append(event1)
	el.Append(event2)

	assert.NotEqual(t, event1.ID, event2.ID, "IDs should be unique")
}

func TestEventLog_Get(t *testing.T) {
	el := newTestEventLog(t)

	event := makeEvent(EventTypePatternAdd, "agent-1", "session-1", map[string]any{"name": "test"})
	el.Append(event)

	retrieved := el.Get(event.ID)

	assert.NotNil(t, retrieved, "Should find event")
	assert.Equal(t, EventTypePatternAdd, retrieved.Type, "Type should match")
}

func TestEventLog_Get_NotFound(t *testing.T) {
	el := newTestEventLog(t)

	retrieved := el.Get("nonexistent")

	assert.Nil(t, retrieved, "Should return nil for nonexistent")
}

func TestEventLog_GetByVersion(t *testing.T) {
	el := newTestEventLog(t)

	event := makeEvent(EventTypeFailureRecord, "agent-1", "session-1", nil)
	event.Version = "v5"
	el.Append(event)

	retrieved := el.GetByVersion("v5")

	assert.NotNil(t, retrieved, "Should find event by version")
	assert.Equal(t, event.ID, retrieved.ID, "ID should match")
}

func TestEventLog_GetRecent(t *testing.T) {
	el := newTestEventLog(t)

	for i := 0; i < 10; i++ {
		el.Append(makeEvent(EventTypeFileRead, "agent-1", "session-1", nil))
	}

	recent := el.GetRecent(5)

	assert.Len(t, recent, 5, "Should return 5 recent events")
}

func TestEventLog_GetRecent_LessThanRequested(t *testing.T) {
	el := newTestEventLog(t)

	for i := 0; i < 3; i++ {
		el.Append(makeEvent(EventTypeFileRead, "agent-1", "session-1", nil))
	}

	recent := el.GetRecent(10)

	assert.Len(t, recent, 3, "Should return all available events")
}

func TestEventLog_GetByAgent(t *testing.T) {
	el := newTestEventLog(t)

	el.Append(makeEvent(EventTypeFileRead, "agent-1", "session-1", nil))
	el.Append(makeEvent(EventTypeFileRead, "agent-2", "session-1", nil))
	el.Append(makeEvent(EventTypeFileRead, "agent-1", "session-1", nil))
	el.Append(makeEvent(EventTypeFileRead, "agent-1", "session-1", nil))

	events := el.GetByAgent("agent-1", 10)

	assert.Len(t, events, 3, "Should find 3 events for agent-1")
}

func TestEventLog_GetByAgent_WithLimit(t *testing.T) {
	el := newTestEventLog(t)

	for i := 0; i < 10; i++ {
		el.Append(makeEvent(EventTypeFileRead, "agent-1", "session-1", nil))
	}

	events := el.GetByAgent("agent-1", 5)

	assert.Len(t, events, 5, "Should respect limit")
}

func TestEventLog_GetBySession(t *testing.T) {
	el := newTestEventLog(t)

	el.Append(makeEvent(EventTypeFileRead, "agent-1", "session-1", nil))
	el.Append(makeEvent(EventTypeFileRead, "agent-1", "session-2", nil))
	el.Append(makeEvent(EventTypeFileRead, "agent-2", "session-1", nil))

	events := el.GetBySession("session-1", 10)

	assert.Len(t, events, 2, "Should find 2 events for session-1")
}

func TestEventLog_GetByType(t *testing.T) {
	el := newTestEventLog(t)

	el.Append(makeEvent(EventTypeFileRead, "agent-1", "session-1", nil))
	el.Append(makeEvent(EventTypeFileModify, "agent-1", "session-1", nil))
	el.Append(makeEvent(EventTypeFileRead, "agent-1", "session-1", nil))
	el.Append(makeEvent(EventTypePatternAdd, "agent-1", "session-1", nil))

	events := el.GetByType(EventTypeFileRead, 10)

	assert.Len(t, events, 2, "Should find 2 FileRead events")
}

func TestEventLog_GetByScope(t *testing.T) {
	el := newTestEventLog(t)

	event1 := makeEvent(EventTypeFileRead, "agent-1", "session-1", nil)
	event1.Scope = ScopeFiles
	el.Append(event1)

	event2 := makeEvent(EventTypePatternAdd, "agent-1", "session-1", nil)
	event2.Scope = ScopePatterns
	el.Append(event2)

	event3 := makeEvent(EventTypeFileModify, "agent-1", "session-1", nil)
	event3.Scope = ScopeFiles
	el.Append(event3)

	events := el.GetByScope(ScopeFiles, 10)

	assert.Len(t, events, 2, "Should find 2 events with Files scope")
}

func TestEventLog_GetSinceVersion(t *testing.T) {
	el := newTestEventLog(t)

	// Add events with versions
	for i := 0; i < 5; i++ {
		event := makeEvent(EventTypeFileRead, "agent-1", "session-1", nil)
		event.Version = "v" + string(rune('0'+i))
		el.Append(event)
	}

	events := el.GetSinceVersion("v2")

	assert.Len(t, events, 2, "Should find events after v2")
}

func TestEventLog_GetSinceClock(t *testing.T) {
	el := newTestEventLog(t)

	// Add events (clock auto-increments)
	for i := 0; i < 5; i++ {
		el.Append(makeEvent(EventTypeFileRead, "agent-1", "session-1", nil))
	}

	// Get events since clock 3
	events := el.GetSinceClock(3)

	assert.GreaterOrEqual(t, len(events), 2, "Should find events after clock 3")
	for _, e := range events {
		assert.Greater(t, e.Clock, uint64(3), "All events should have clock > 3")
	}
}

func TestEventLog_Query(t *testing.T) {
	el := newTestEventLog(t)

	el.Append(makeEvent(EventTypeFileRead, "agent-1", "session-1", nil))
	el.Append(makeEvent(EventTypeFileModify, "agent-1", "session-1", nil))
	el.Append(makeEvent(EventTypeFileRead, "agent-2", "session-1", nil))
	el.Append(makeEvent(EventTypeFileRead, "agent-1", "session-2", nil))

	events := el.Query(EventQuery{
		AgentID:   "agent-1",
		SessionID: "session-1",
	})

	assert.Len(t, events, 2, "Should find 2 matching events")
}

func TestEventLog_Query_ByTypes(t *testing.T) {
	el := newTestEventLog(t)

	el.Append(makeEvent(EventTypeFileRead, "agent-1", "session-1", nil))
	el.Append(makeEvent(EventTypeFileModify, "agent-1", "session-1", nil))
	el.Append(makeEvent(EventTypePatternAdd, "agent-1", "session-1", nil))
	el.Append(makeEvent(EventTypeFileCreate, "agent-1", "session-1", nil))

	events := el.Query(EventQuery{
		Types: []EventType{EventTypeFileRead, EventTypeFileModify, EventTypeFileCreate},
	})

	assert.Len(t, events, 3, "Should find 3 file-related events")
}

func TestEventLog_Query_WithTimeRange(t *testing.T) {
	el := newTestEventLog(t)

	el.Append(makeEvent(EventTypeFileRead, "agent-1", "session-1", nil))
	time.Sleep(50 * time.Millisecond)
	startTime := time.Now()
	time.Sleep(10 * time.Millisecond)
	el.Append(makeEvent(EventTypeFileRead, "agent-1", "session-1", nil))
	el.Append(makeEvent(EventTypeFileRead, "agent-1", "session-1", nil))

	events := el.Query(EventQuery{
		Since: &startTime,
	})

	assert.Len(t, events, 2, "Should find 2 events after start time")
}

func TestEventLog_Query_WithLimit(t *testing.T) {
	el := newTestEventLog(t)

	for i := 0; i < 10; i++ {
		el.Append(makeEvent(EventTypeFileRead, "agent-1", "session-1", nil))
	}

	events := el.Query(EventQuery{
		Limit: 3,
	})

	assert.Len(t, events, 3, "Should respect limit")
}

func TestEventLog_QueryWithContext_Cancellation(t *testing.T) {
	el := newTestEventLog(t)

	// Add many events
	for i := 0; i < 5000; i++ {
		el.Append(makeEvent(EventTypeFileRead, "agent-1", "session-1", nil))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := el.QueryWithContext(ctx, EventQuery{})

	assert.Error(t, err, "Should return error when context cancelled")
}

func TestEventLog_GetDelta(t *testing.T) {
	el := newTestEventLog(t)

	for i := 0; i < 5; i++ {
		event := makeEvent(EventTypeFileRead, "agent-1", "session-1", nil)
		event.Version = "v" + string(rune('0'+i))
		event.Scope = ScopeFiles
		event.Key = "/test.go"
		el.Append(event)
	}

	delta := el.GetDelta("v2", 10)

	assert.Len(t, delta, 2, "Should return delta entries after v2")
	for _, d := range delta {
		assert.Equal(t, "update", d.Type, "Delta type should be update for FileRead")
	}
}

func TestEventLog_Len(t *testing.T) {
	el := newTestEventLog(t)

	assert.Equal(t, 0, el.Len(), "Initial length should be 0")

	for i := 0; i < 5; i++ {
		el.Append(makeEvent(EventTypeFileRead, "agent-1", "session-1", nil))
	}

	assert.Equal(t, 5, el.Len(), "Length should be 5")
}

func TestEventLog_LastClock(t *testing.T) {
	el := newTestEventLog(t)

	assert.Equal(t, uint64(0), el.LastClock(), "Initial clock should be 0")

	el.Append(makeEvent(EventTypeFileRead, "agent-1", "session-1", nil))
	el.Append(makeEvent(EventTypeFileRead, "agent-1", "session-1", nil))

	assert.GreaterOrEqual(t, el.LastClock(), uint64(2), "Clock should be at least 2")
}

func TestEventLog_Stats(t *testing.T) {
	el := newTestEventLog(t)

	el.Append(makeEvent(EventTypeFileRead, "agent-1", "session-1", nil))
	el.Append(makeEvent(EventTypeFileRead, "agent-2", "session-1", nil))
	el.Append(makeEvent(EventTypePatternAdd, "agent-1", "session-2", nil))

	stats := el.Stats()

	assert.Equal(t, 3, stats.TotalEvents, "Total events")
	assert.Equal(t, 2, stats.EventsByType[EventTypeFileRead], "FileRead count")
	assert.Equal(t, 1, stats.EventsByType[EventTypePatternAdd], "PatternAdd count")
	assert.Equal(t, 2, stats.EventsByAgent["agent-1"], "Agent-1 events")
	assert.Equal(t, 2, stats.EventsBySession["session-1"], "Session-1 events")
}

func TestEventLog_Pruning(t *testing.T) {
	el := NewEventLog(EventLogConfig{
		MaxEvents: 100,
	})
	defer el.Close()

	// Add more events than max
	for i := 0; i < 150; i++ {
		el.Append(makeEvent(EventTypeFileRead, "agent-1", "session-1", nil))
	}

	// Should have pruned some events
	assert.LessOrEqual(t, el.Len(), 100, "Should not exceed max events")
}

func TestEventLog_PruningPreservesRecent(t *testing.T) {
	el := NewEventLog(EventLogConfig{
		MaxEvents: 50,
	})
	defer el.Close()

	// Add events
	for i := 0; i < 100; i++ {
		el.Append(makeEvent(EventTypeFileRead, "agent-1", "session-1", nil))
	}

	// Get recent events
	recent := el.GetRecent(10)

	// Recent events should still be accessible
	assert.Len(t, recent, 10, "Should still have recent events")
}

func TestEventLog_Ordering(t *testing.T) {
	el := newTestEventLog(t)

	var ids []string
	for i := 0; i < 10; i++ {
		event := makeEvent(EventTypeFileRead, "agent-1", "session-1", nil)
		el.Append(event)
		ids = append(ids, event.ID)
	}

	recent := el.GetRecent(10)

	// Verify order matches insertion order
	for i, event := range recent {
		assert.Equal(t, ids[i], event.ID, "Event order should match insertion order")
	}
}

func TestEventLog_ClockMonotonicity(t *testing.T) {
	el := newTestEventLog(t)

	var clocks []uint64
	for i := 0; i < 100; i++ {
		event := makeEvent(EventTypeFileRead, "agent-1", "session-1", nil)
		el.Append(event)
		clocks = append(clocks, event.Clock)
	}

	// Verify clocks are monotonically increasing
	for i := 1; i < len(clocks); i++ {
		assert.Greater(t, clocks[i], clocks[i-1], "Clock should be monotonically increasing")
	}
}

func TestEventLog_PreviousIDChaining(t *testing.T) {
	el := newTestEventLog(t)

	var events []*Event
	for i := 0; i < 5; i++ {
		event := makeEvent(EventTypeFileRead, "agent-1", "session-1", nil)
		el.Append(event)
		events = append(events, event)
	}

	// First event has no previous
	assert.Empty(t, events[0].PreviousID, "First event should have no previous")

	// Subsequent events should chain
	for i := 1; i < len(events); i++ {
		assert.Equal(t, events[i-1].ID, events[i].PreviousID, "PreviousID should chain")
	}
}

// =============================================================================
// Event Sourcing Tests
// =============================================================================

func TestEventSourcing_DeltaReconstruction(t *testing.T) {
	el := newTestEventLog(t)

	// Simulate a sequence of changes
	events := []struct {
		eventType EventType
		version   string
	}{
		{EventTypeFileCreate, "v1"},
		{EventTypeFileModify, "v2"},
		{EventTypePatternAdd, "v3"},
		{EventTypeFileModify, "v4"},
		{EventTypeFailureRecord, "v5"},
	}

	for _, e := range events {
		event := makeEvent(e.eventType, "agent-1", "session-1", nil)
		event.Version = e.version
		el.Append(event)
	}

	// Reconstruct from v2
	delta := el.GetDelta("v2", 10)

	assert.Len(t, delta, 3, "Should have 3 delta entries after v2")
}

func TestEventSourcing_VersionTracking(t *testing.T) {
	el := newTestEventLog(t)

	versions := []string{"v1", "v2", "v3", "v4", "v5"}

	for _, v := range versions {
		event := makeEvent(EventTypeFileRead, "agent-1", "session-1", nil)
		event.Version = v
		el.Append(event)
	}

	// Each version should be retrievable
	for _, v := range versions {
		event := el.GetByVersion(v)
		assert.NotNil(t, event, "Should find event for version %s", v)
		assert.Equal(t, v, event.Version, "Version should match")
	}
}

// =============================================================================
// Concurrency Tests
// =============================================================================

func TestEventLog_ConcurrentAppend(t *testing.T) {
	el := newTestEventLog(t)

	numWriters := 10
	eventsPerWriter := 100

	var wg sync.WaitGroup
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for j := 0; j < eventsPerWriter; j++ {
				el.Append(makeEvent(EventTypeFileRead, "agent-1", "session-1", nil))
			}
		}(i)
	}

	wg.Wait()

	assert.Equal(t, numWriters*eventsPerWriter, el.Len(), "All events should be appended")
}

func TestEventLog_ConcurrentReadWrite(t *testing.T) {
	el := newTestEventLog(t)

	var wg sync.WaitGroup
	numOps := 100

	// Writers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOps; j++ {
				el.Append(makeEvent(EventTypeFileRead, "agent-1", "session-1", nil))
			}
		}()
	}

	// Readers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOps; j++ {
				el.GetRecent(10)
				el.Query(EventQuery{AgentID: "agent-1"})
				el.Stats()
			}
		}()
	}

	wg.Wait()

	// Should complete without deadlock or panic
	assert.Greater(t, el.Len(), 0, "Should have events")
}

func TestEventLog_ToJSON_FromJSON(t *testing.T) {
	el := newTestEventLog(t)

	// Add events
	for i := 0; i < 5; i++ {
		event := makeEvent(EventTypeFileRead, "agent-1", "session-1", map[string]any{
			"path": "/test.go",
			"line": i,
		})
		event.Version = "v" + string(rune('0'+i))
		el.Append(event)
	}

	// Export to JSON
	data, err := el.ToJSON()
	require.NoError(t, err, "ToJSON")
	assert.NotEmpty(t, data, "JSON should not be empty")

	// Create new event log and import
	el2 := newTestEventLog(t)
	err = el2.FromJSON(data)
	require.NoError(t, err, "FromJSON")

	assert.Equal(t, 5, el2.Len(), "Should have imported all events")
}
