package context

import (
	"sync"
	"testing"
	"time"
)

// =============================================================================
// Constructor Tests
// =============================================================================

func TestNewAccessTracker_DefaultConfig(t *testing.T) {
	tracker := NewDefaultAccessTracker()

	stats := tracker.GetStats()
	if stats.WindowSize != DefaultAccessWindowSize {
		t.Errorf("expected window size %d, got %d", DefaultAccessWindowSize, stats.WindowSize)
	}
	if stats.MaxEntries != DefaultMaxAccessEntries {
		t.Errorf("expected max entries %d, got %d", DefaultMaxAccessEntries, stats.MaxEntries)
	}
	if stats.MaxLogSize != DefaultMaxAccessLogSize {
		t.Errorf("expected max log size %d, got %d", DefaultMaxAccessLogSize, stats.MaxLogSize)
	}
}

func TestNewAccessTracker_CustomConfig(t *testing.T) {
	tracker := NewAccessTracker(AccessTrackerConfig{
		WindowSize: 50,
		MaxEntries: 5000,
		MaxLogSize: 500,
	})

	stats := tracker.GetStats()
	if stats.WindowSize != 50 {
		t.Errorf("expected window size 50, got %d", stats.WindowSize)
	}
	if stats.MaxEntries != 5000 {
		t.Errorf("expected max entries 5000, got %d", stats.MaxEntries)
	}
	if stats.MaxLogSize != 500 {
		t.Errorf("expected max log size 500, got %d", stats.MaxLogSize)
	}
}

func TestNewAccessTracker_InvalidConfigUsesDefaults(t *testing.T) {
	tracker := NewAccessTracker(AccessTrackerConfig{
		WindowSize: -1,
		MaxEntries: -1,
		MaxLogSize: -1,
	})

	stats := tracker.GetStats()
	if stats.WindowSize != DefaultAccessWindowSize {
		t.Errorf("expected default window size, got %d", stats.WindowSize)
	}
}

// =============================================================================
// RecordAccess Tests
// =============================================================================

func TestRecordAccess_TracksCount(t *testing.T) {
	tracker := NewDefaultAccessTracker()

	tracker.RecordAccess("file-1", 1, AccessSourceInResponse)
	tracker.RecordAccess("file-1", 1, AccessSourceInResponse)
	tracker.RecordAccess("file-1", 1, AccessSourceInResponse)

	count := tracker.GetAccessCount("file-1")
	if count != 3 {
		t.Errorf("expected count 3, got %d", count)
	}
}

func TestRecordAccess_TracksAcrossTurns(t *testing.T) {
	tracker := NewDefaultAccessTracker()

	tracker.RecordAccess("file-1", 1, AccessSourceInResponse)
	tracker.RecordAccess("file-1", 2, AccessSourceInResponse)
	tracker.RecordAccess("file-1", 3, AccessSourceInResponse)

	count := tracker.GetAccessCount("file-1")
	if count != 3 {
		t.Errorf("expected count 3 across turns, got %d", count)
	}
}

func TestRecordAccess_TracksMultipleFiles(t *testing.T) {
	tracker := NewDefaultAccessTracker()

	tracker.RecordAccess("file-1", 1, AccessSourceInResponse)
	tracker.RecordAccess("file-2", 1, AccessSourceInResponse)
	tracker.RecordAccess("file-2", 1, AccessSourceInResponse)
	tracker.RecordAccess("file-3", 1, AccessSourceInResponse)

	if tracker.GetAccessCount("file-1") != 1 {
		t.Error("expected file-1 count 1")
	}
	if tracker.GetAccessCount("file-2") != 2 {
		t.Error("expected file-2 count 2")
	}
	if tracker.GetAccessCount("file-3") != 1 {
		t.Error("expected file-3 count 1")
	}
}

func TestRecordAccess_UpdatesLastAccess(t *testing.T) {
	tracker := NewDefaultAccessTracker()

	before := time.Now()
	tracker.RecordAccess("file-1", 1, AccessSourceInResponse)
	after := time.Now()

	ts, ok := tracker.GetLastAccessed("file-1")
	if !ok {
		t.Fatal("expected last access to exist")
	}
	if ts.Before(before) || ts.After(after) {
		t.Error("timestamp not in expected range")
	}
}

func TestRecordAccess_AddsToAccessLog(t *testing.T) {
	tracker := NewDefaultAccessTracker()

	tracker.RecordAccess("file-1", 1, AccessSourceInResponse)
	tracker.RecordAccess("file-2", 2, AccessSourceToolRetrieve)

	events := tracker.GetRecentEvents(2)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	// Most recent first
	if events[0].ContentID != "file-2" {
		t.Errorf("expected file-2 first, got %s", events[0].ContentID)
	}
	if events[1].ContentID != "file-1" {
		t.Errorf("expected file-1 second, got %s", events[1].ContentID)
	}
}

// =============================================================================
// Window Pruning Tests
// =============================================================================

func TestRecordAccess_PrunesOldTurns(t *testing.T) {
	tracker := NewAccessTracker(AccessTrackerConfig{
		WindowSize: 5,
		MaxEntries: 1000,
		MaxLogSize: 100,
	})

	// Record across many turns
	for turn := 1; turn <= 10; turn++ {
		tracker.RecordAccess("file-1", turn, AccessSourceInResponse)
	}

	// Window is [currentTurn - windowSize, currentTurn]
	// With currentTurn=10 and windowSize=5, minTurn=5, so turns 5-10 kept (6 turns)
	count := tracker.GetAccessCount("file-1")
	if count != 6 {
		t.Errorf("expected count 6 (window pruning), got %d", count)
	}
}

func TestRecordAccess_WindowRespectsSize(t *testing.T) {
	tracker := NewAccessTracker(AccessTrackerConfig{
		WindowSize: 3,
		MaxEntries: 1000,
		MaxLogSize: 100,
	})

	// Record in turns 1, 2, 3, 4, 5
	for turn := 1; turn <= 5; turn++ {
		tracker.RecordAccess("file-"+string(rune('a'+turn)), turn, AccessSourceInResponse)
	}

	stats := tracker.GetStats()
	// Window is [currentTurn - windowSize, currentTurn]
	// With currentTurn=5 and windowSize=3, minTurn=2, so turns 2-5 kept (4 turns)
	if stats.TurnsTracked > 4 {
		t.Errorf("expected at most 4 turns tracked, got %d", stats.TurnsTracked)
	}
}

// =============================================================================
// LRU Eviction Tests
// =============================================================================

func TestRecordAccess_LRUEviction(t *testing.T) {
	tracker := NewAccessTracker(AccessTrackerConfig{
		WindowSize: 100,
		MaxEntries: 5,
		MaxLogSize: 100,
	})

	// Record 10 files (max is 5)
	for i := 0; i < 10; i++ {
		tracker.RecordAccess("file-"+string(rune('a'+i)), 1, AccessSourceInResponse)
	}

	stats := tracker.GetStats()
	if stats.TotalContentTracked > 5 {
		t.Errorf("expected max 5 entries, got %d", stats.TotalContentTracked)
	}

	// Oldest files should be evicted
	_, ok := tracker.GetLastAccessed("file-a")
	if ok {
		t.Error("expected file-a to be evicted (oldest)")
	}

	// Most recent should exist
	_, ok = tracker.GetLastAccessed("file-j")
	if !ok {
		t.Error("expected file-j to exist (most recent)")
	}
}

func TestRecordAccess_LRUMoveToFront(t *testing.T) {
	tracker := NewAccessTracker(AccessTrackerConfig{
		WindowSize: 100,
		MaxEntries: 5,
		MaxLogSize: 100,
	})

	// Record files a-e
	for i := 0; i < 5; i++ {
		tracker.RecordAccess("file-"+string(rune('a'+i)), 1, AccessSourceInResponse)
	}

	// Access file-a again (moves to front)
	tracker.RecordAccess("file-a", 2, AccessSourceInResponse)

	// Record new file f (should evict oldest, which is now file-b)
	tracker.RecordAccess("file-f", 3, AccessSourceInResponse)

	// file-a should still exist (was moved to front)
	_, ok := tracker.GetLastAccessed("file-a")
	if !ok {
		t.Error("expected file-a to exist (was moved to front)")
	}

	// file-b should be evicted
	_, ok = tracker.GetLastAccessed("file-b")
	if ok {
		t.Error("expected file-b to be evicted")
	}
}

// =============================================================================
// Ring Buffer Tests
// =============================================================================

func TestAccessLog_RingBuffer(t *testing.T) {
	tracker := NewAccessTracker(AccessTrackerConfig{
		WindowSize: 100,
		MaxEntries: 1000,
		MaxLogSize: 5,
	})

	// Record 10 events (max log is 5)
	for i := 0; i < 10; i++ {
		tracker.RecordAccess("file-"+string(rune('a'+i)), i, AccessSourceInResponse)
	}

	events := tracker.GetRecentEvents(10)
	if len(events) != 5 {
		t.Errorf("expected 5 events (ring buffer), got %d", len(events))
	}

	// Should have most recent 5 (f-j)
	if events[0].ContentID != "file-j" {
		t.Errorf("expected file-j (most recent), got %s", events[0].ContentID)
	}
}

// =============================================================================
// GetMostAccessed Tests
// =============================================================================

func TestGetMostAccessed_ReturnsTopN(t *testing.T) {
	tracker := NewDefaultAccessTracker()

	// file-a: 5 accesses
	for i := 0; i < 5; i++ {
		tracker.RecordAccess("file-a", 1, AccessSourceInResponse)
	}
	// file-b: 3 accesses
	for i := 0; i < 3; i++ {
		tracker.RecordAccess("file-b", 1, AccessSourceInResponse)
	}
	// file-c: 1 access
	tracker.RecordAccess("file-c", 1, AccessSourceInResponse)

	top := tracker.GetMostAccessed(2)
	if len(top) != 2 {
		t.Fatalf("expected 2 results, got %d", len(top))
	}
	if top[0] != "file-a" {
		t.Errorf("expected file-a first, got %s", top[0])
	}
	if top[1] != "file-b" {
		t.Errorf("expected file-b second, got %s", top[1])
	}
}

func TestGetMostAccessed_HandlesLessThanN(t *testing.T) {
	tracker := NewDefaultAccessTracker()

	tracker.RecordAccess("file-a", 1, AccessSourceInResponse)
	tracker.RecordAccess("file-b", 1, AccessSourceInResponse)

	top := tracker.GetMostAccessed(10)
	if len(top) != 2 {
		t.Errorf("expected 2 results, got %d", len(top))
	}
}

func TestGetMostAccessed_EmptyTracker(t *testing.T) {
	tracker := NewDefaultAccessTracker()

	top := tracker.GetMostAccessed(5)
	if len(top) != 0 {
		t.Errorf("expected empty result, got %d", len(top))
	}
}

// =============================================================================
// GetEventsBySource Tests
// =============================================================================

func TestGetEventsBySource_Filters(t *testing.T) {
	tracker := NewDefaultAccessTracker()

	tracker.RecordAccess("file-1", 1, AccessSourceInResponse)
	tracker.RecordAccess("file-2", 1, AccessSourceToolRetrieve)
	tracker.RecordAccess("file-3", 1, AccessSourceInResponse)
	tracker.RecordAccess("file-4", 1, AccessSourcePrefetched)

	inResponse := tracker.GetEventsBySource(AccessSourceInResponse, 10)
	if len(inResponse) != 2 {
		t.Errorf("expected 2 in_response events, got %d", len(inResponse))
	}

	toolRetrieved := tracker.GetEventsBySource(AccessSourceToolRetrieve, 10)
	if len(toolRetrieved) != 1 {
		t.Errorf("expected 1 tool_retrieved event, got %d", len(toolRetrieved))
	}
}

// =============================================================================
// EvictableCache Interface Tests
// =============================================================================

func TestName_ReturnsAccessTracker(t *testing.T) {
	tracker := NewDefaultAccessTracker()

	if tracker.Name() != "access-tracker" {
		t.Errorf("expected 'access-tracker', got %s", tracker.Name())
	}
}

func TestSize_ReturnsNonZeroAfterRecords(t *testing.T) {
	tracker := NewDefaultAccessTracker()

	size1 := tracker.Size()

	for i := 0; i < 100; i++ {
		tracker.RecordAccess("file-"+string(rune(i)), 1, AccessSourceInResponse)
	}

	size2 := tracker.Size()
	if size2 <= size1 {
		t.Errorf("expected size to increase, got %d <= %d", size2, size1)
	}
}

func TestEvictPercent_ReducesSize(t *testing.T) {
	tracker := NewDefaultAccessTracker()

	// Add lots of data
	for i := 0; i < 1000; i++ {
		tracker.RecordAccess("file-"+string(rune(i%256)), i, AccessSourceInResponse)
	}

	sizeBefore := tracker.Size()
	evicted := tracker.EvictPercent(0.5)

	sizeAfter := tracker.Size()
	if sizeAfter >= sizeBefore {
		t.Errorf("expected size to decrease after eviction")
	}
	if evicted <= 0 {
		t.Error("expected positive evicted bytes")
	}
}

// =============================================================================
// Reset Tests
// =============================================================================

func TestReset_ClearsAllData(t *testing.T) {
	tracker := NewDefaultAccessTracker()

	for i := 0; i < 100; i++ {
		tracker.RecordAccess("file-"+string(rune(i)), i, AccessSourceInResponse)
	}

	tracker.Reset()

	stats := tracker.GetStats()
	if stats.TotalContentTracked != 0 {
		t.Error("expected 0 content after reset")
	}
	if stats.TotalAccessEvents != 0 {
		t.Error("expected 0 events after reset")
	}
	if stats.TurnsTracked != 0 {
		t.Error("expected 0 turns after reset")
	}
}

// =============================================================================
// Snapshot/Restore Tests
// =============================================================================

func TestSnapshot_PreservesState(t *testing.T) {
	tracker := NewDefaultAccessTracker()

	tracker.RecordAccess("file-a", 1, AccessSourceInResponse)
	tracker.RecordAccess("file-a", 2, AccessSourceInResponse)
	tracker.RecordAccess("file-b", 1, AccessSourceToolRetrieve)

	snap := tracker.Snapshot()

	if len(snap.AccessCounts) == 0 {
		t.Error("expected access counts in snapshot")
	}
	if len(snap.LastAccess) != 2 {
		t.Errorf("expected 2 last access entries, got %d", len(snap.LastAccess))
	}
	if len(snap.Events) != 3 {
		t.Errorf("expected 3 events, got %d", len(snap.Events))
	}
}

func TestRestoreFromSnapshot_RestoresState(t *testing.T) {
	tracker1 := NewDefaultAccessTracker()

	tracker1.RecordAccess("file-a", 1, AccessSourceInResponse)
	tracker1.RecordAccess("file-a", 2, AccessSourceInResponse)
	tracker1.RecordAccess("file-b", 1, AccessSourceToolRetrieve)

	snap := tracker1.Snapshot()

	tracker2 := NewDefaultAccessTracker()
	tracker2.RestoreFromSnapshot(snap)

	// Verify restored state
	if tracker2.GetAccessCount("file-a") != 2 {
		t.Error("expected file-a count 2 after restore")
	}
	if tracker2.GetAccessCount("file-b") != 1 {
		t.Error("expected file-b count 1 after restore")
	}

	_, ok := tracker2.GetLastAccessed("file-a")
	if !ok {
		t.Error("expected file-a in last access after restore")
	}

	events := tracker2.GetRecentEvents(10)
	if len(events) != 3 {
		t.Errorf("expected 3 events after restore, got %d", len(events))
	}
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestAccessTracker_ConcurrentRecords(t *testing.T) {
	tracker := NewDefaultAccessTracker()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			tracker.RecordAccess("file-"+string(rune('a'+(id%26))), id, AccessSourceInResponse)
		}(i)
	}
	wg.Wait()

	stats := tracker.GetStats()
	if stats.TotalAccessEvents != 100 {
		t.Errorf("expected 100 events, got %d", stats.TotalAccessEvents)
	}
}

func TestAccessTracker_ConcurrentReadsWrites(t *testing.T) {
	tracker := NewDefaultAccessTracker()

	// Pre-populate
	for i := 0; i < 50; i++ {
		tracker.RecordAccess("file-"+string(rune('a'+(i%26))), i, AccessSourceInResponse)
	}

	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			tracker.RecordAccess("file-"+string(rune('a'+(id%26))), id+50, AccessSourceInResponse)
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tracker.GetAccessCount("file-a")
			tracker.GetMostAccessed(5)
			tracker.GetStats()
		}()
	}

	wg.Wait()
}

// =============================================================================
// Memory Bounds Tests
// =============================================================================

func TestAccessTracker_MemoryBounded(t *testing.T) {
	tracker := NewAccessTracker(AccessTrackerConfig{
		WindowSize: 10,
		MaxEntries: 100,
		MaxLogSize: 100,
	})

	// Record many accesses
	for i := 0; i < 10000; i++ {
		tracker.RecordAccess("file-"+string(rune(i%256)), i, AccessSourceInResponse)
	}

	stats := tracker.GetStats()

	// Should be bounded
	if stats.TotalContentTracked > 100 {
		t.Errorf("expected max 100 content entries, got %d", stats.TotalContentTracked)
	}
	if stats.TotalAccessEvents > 100 {
		t.Errorf("expected max 100 events, got %d", stats.TotalAccessEvents)
	}
	// Window is [currentTurn - windowSize, currentTurn], so max turns is windowSize + 1
	if stats.TurnsTracked > 11 {
		t.Errorf("expected max 11 turns, got %d", stats.TurnsTracked)
	}
}

// =============================================================================
// SetTurn Tests
// =============================================================================

func TestSetTurn_PrunesOldData(t *testing.T) {
	tracker := NewAccessTracker(AccessTrackerConfig{
		WindowSize: 5,
		MaxEntries: 100,
		MaxLogSize: 100,
	})

	// Record in turns 1-10
	for turn := 1; turn <= 10; turn++ {
		tracker.RecordAccess("file-a", turn, AccessSourceInResponse)
	}

	// Move to turn 12 (should prune turns < 7)
	// Turns 7-10 should remain (4 turns)
	tracker.SetTurn(12)

	stats := tracker.GetStats()
	if stats.TurnsTracked > 6 {
		t.Errorf("expected at most 6 turns after SetTurn, got %d", stats.TurnsTracked)
	}

	// Access count should only include turns 7-10
	count := tracker.GetAccessCount("file-a")
	if count != 4 {
		t.Errorf("expected count 4 after pruning (turns 7-10), got %d", count)
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestGetAccessCount_NonExistentFile(t *testing.T) {
	tracker := NewDefaultAccessTracker()

	count := tracker.GetAccessCount("nonexistent")
	if count != 0 {
		t.Errorf("expected count 0, got %d", count)
	}
}

func TestGetLastAccessed_NonExistentFile(t *testing.T) {
	tracker := NewDefaultAccessTracker()

	_, ok := tracker.GetLastAccessed("nonexistent")
	if ok {
		t.Error("expected false for nonexistent file")
	}
}

func TestGetRecentEvents_MoreThanExist(t *testing.T) {
	tracker := NewDefaultAccessTracker()

	tracker.RecordAccess("file-a", 1, AccessSourceInResponse)
	tracker.RecordAccess("file-b", 2, AccessSourceInResponse)

	events := tracker.GetRecentEvents(100)
	if len(events) != 2 {
		t.Errorf("expected 2 events, got %d", len(events))
	}
}
