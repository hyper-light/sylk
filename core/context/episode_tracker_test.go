package context

import (
	"sync"
	"testing"
	"time"
)

// =============================================================================
// StartEpisode Tests
// =============================================================================

func TestStartEpisode_InitializesEpisode(t *testing.T) {
	tracker := NewEpisodeTracker()

	obs := tracker.StartEpisode("agent-1", "coder", 1)

	if obs == nil {
		t.Fatal("expected observation to be returned")
	}
	if !tracker.HasActiveEpisode() {
		t.Error("expected active episode after start")
	}
}

func TestStartEpisode_ReplacesExisting(t *testing.T) {
	tracker := NewEpisodeTracker()

	tracker.StartEpisode("agent-1", "coder", 1)
	tracker.RecordUsedID("file-1")

	// Start new episode replaces old one
	tracker.StartEpisode("agent-2", "reviewer", 2)

	if tracker.GetUsedCount() != 0 {
		t.Error("expected used count to be reset")
	}
}

func TestStartEpisode_SetsTimestamp(t *testing.T) {
	tracker := NewEpisodeTracker()

	before := time.Now()
	obs := tracker.StartEpisode("agent-1", "coder", 1)
	after := time.Now()

	if obs.Timestamp.Before(before) || obs.Timestamp.After(after) {
		t.Error("timestamp should be between before and after")
	}
}

// =============================================================================
// RecordPrefetchedIDs Tests
// =============================================================================

func TestRecordPrefetchedIDs_AddsToEpisode(t *testing.T) {
	tracker := NewEpisodeTracker()
	tracker.StartEpisode("agent-1", "coder", 1)

	tracker.RecordPrefetchedIDs([]string{"file-1", "file-2", "file-3"})

	if tracker.GetPrefetchedCount() != 3 {
		t.Errorf("expected 3 prefetched, got %d", tracker.GetPrefetchedCount())
	}
}

func TestRecordPrefetchedIDs_Deduplicates(t *testing.T) {
	tracker := NewEpisodeTracker()
	tracker.StartEpisode("agent-1", "coder", 1)

	tracker.RecordPrefetchedIDs([]string{"file-1", "file-1", "file-2"})

	if tracker.GetPrefetchedCount() != 2 {
		t.Errorf("expected 2 unique prefetched, got %d", tracker.GetPrefetchedCount())
	}
}

func TestRecordPrefetchedIDs_RespectsLimit(t *testing.T) {
	tracker := NewEpisodeTrackerWithLimits(5, 10)
	tracker.StartEpisode("agent-1", "coder", 1)

	ids := make([]string, 10)
	for i := range ids {
		ids[i] = "file-" + string(rune('a'+i))
	}
	tracker.RecordPrefetchedIDs(ids)

	if tracker.GetPrefetchedCount() > 5 {
		t.Errorf("expected max 5 prefetched, got %d", tracker.GetPrefetchedCount())
	}
}

func TestRecordPrefetchedIDs_NoEpisode(t *testing.T) {
	tracker := NewEpisodeTracker()

	// Should not panic
	tracker.RecordPrefetchedIDs([]string{"file-1"})

	if tracker.GetPrefetchedCount() != 0 {
		t.Error("expected 0 prefetched with no episode")
	}
}

// =============================================================================
// RecordUsedID Tests
// =============================================================================

func TestRecordUsedID_AddsToEpisode(t *testing.T) {
	tracker := NewEpisodeTracker()
	tracker.StartEpisode("agent-1", "coder", 1)

	tracker.RecordUsedID("file-1")
	tracker.RecordUsedID("file-2")

	if tracker.GetUsedCount() != 2 {
		t.Errorf("expected 2 used, got %d", tracker.GetUsedCount())
	}
}

func TestRecordUsedID_Deduplicates(t *testing.T) {
	tracker := NewEpisodeTracker()
	tracker.StartEpisode("agent-1", "coder", 1)

	tracker.RecordUsedID("file-1")
	tracker.RecordUsedID("file-1")

	if tracker.GetUsedCount() != 1 {
		t.Errorf("expected 1 unique used, got %d", tracker.GetUsedCount())
	}
}

func TestRecordUsedID_RespectsLimit(t *testing.T) {
	tracker := NewEpisodeTrackerWithLimits(3, 10)
	tracker.StartEpisode("agent-1", "coder", 1)

	for i := 0; i < 10; i++ {
		tracker.RecordUsedID("file-" + string(rune('a'+i)))
	}

	if tracker.GetUsedCount() > 3 {
		t.Errorf("expected max 3 used, got %d", tracker.GetUsedCount())
	}
}

func TestRecordUsedID_NoEpisode(t *testing.T) {
	tracker := NewEpisodeTracker()

	// Should not panic
	tracker.RecordUsedID("file-1")

	if tracker.GetUsedCount() != 0 {
		t.Error("expected 0 used with no episode")
	}
}

// =============================================================================
// RecordSearchAfterPrefetch Tests
// =============================================================================

func TestRecordSearchAfterPrefetch_AddsToEpisode(t *testing.T) {
	tracker := NewEpisodeTracker()
	tracker.StartEpisode("agent-1", "coder", 1)

	tracker.RecordSearchAfterPrefetch("find main.go")
	tracker.RecordSearchAfterPrefetch("grep error")

	if tracker.GetSearchCount() != 2 {
		t.Errorf("expected 2 searches, got %d", tracker.GetSearchCount())
	}
}

func TestRecordSearchAfterPrefetch_AllowsDuplicates(t *testing.T) {
	tracker := NewEpisodeTracker()
	tracker.StartEpisode("agent-1", "coder", 1)

	tracker.RecordSearchAfterPrefetch("find main.go")
	tracker.RecordSearchAfterPrefetch("find main.go")

	// Duplicate searches are allowed (different queries can be same text)
	if tracker.GetSearchCount() != 2 {
		t.Errorf("expected 2 searches (dups allowed), got %d", tracker.GetSearchCount())
	}
}

func TestRecordSearchAfterPrefetch_RespectsLimit(t *testing.T) {
	tracker := NewEpisodeTrackerWithLimits(100, 3)
	tracker.StartEpisode("agent-1", "coder", 1)

	for i := 0; i < 10; i++ {
		tracker.RecordSearchAfterPrefetch("query-" + string(rune('a'+i)))
	}

	if tracker.GetSearchCount() > 3 {
		t.Errorf("expected max 3 searches, got %d", tracker.GetSearchCount())
	}
}

func TestRecordSearchAfterPrefetch_NoEpisode(t *testing.T) {
	tracker := NewEpisodeTracker()

	// Should not panic
	tracker.RecordSearchAfterPrefetch("query")

	if tracker.GetSearchCount() != 0 {
		t.Error("expected 0 searches with no episode")
	}
}

// =============================================================================
// RecordToolCall Tests
// =============================================================================

func TestRecordToolCall_AddsToEpisode(t *testing.T) {
	tracker := NewEpisodeTracker()
	tracker.StartEpisode("agent-1", "coder", 1)

	tracker.RecordToolCall("read_file")
	tracker.RecordToolCall("write_file")

	if tracker.GetToolCallCount() != 2 {
		t.Errorf("expected 2 tool calls, got %d", tracker.GetToolCallCount())
	}
}

func TestRecordToolCallWithDetails_AddsFullInfo(t *testing.T) {
	tracker := NewEpisodeTracker()
	tracker.StartEpisode("agent-1", "coder", 1)

	tracker.RecordToolCallWithDetails(ToolCall{
		Name:      "read_file",
		Arguments: `{"path": "main.go"}`,
		Duration:  100 * time.Millisecond,
		Success:   true,
	})

	if tracker.GetToolCallCount() != 1 {
		t.Errorf("expected 1 tool call, got %d", tracker.GetToolCallCount())
	}
}

func TestRecordToolCall_NoEpisode(t *testing.T) {
	tracker := NewEpisodeTracker()

	// Should not panic
	tracker.RecordToolCall("read_file")

	if tracker.GetToolCallCount() != 0 {
		t.Error("expected 0 tool calls with no episode")
	}
}

// =============================================================================
// FinalizeEpisode Tests
// =============================================================================

func TestFinalizeEpisode_ReturnsObservation(t *testing.T) {
	tracker := NewEpisodeTracker()
	tracker.StartEpisode("agent-1", "coder", 1)

	tracker.RecordPrefetchedIDs([]string{"file-1", "file-2"})
	tracker.RecordUsedID("file-1")
	tracker.RecordSearchAfterPrefetch("find other.go")
	tracker.RecordToolCall("read_file")

	obs := tracker.FinalizeEpisode("Task done successfully", nil)

	if len(obs.PrefetchedIDs) != 2 {
		t.Errorf("expected 2 prefetched, got %d", len(obs.PrefetchedIDs))
	}
	if len(obs.UsedIDs) != 1 {
		t.Errorf("expected 1 used, got %d", len(obs.UsedIDs))
	}
	if len(obs.SearchedAfter) != 1 {
		t.Errorf("expected 1 search, got %d", len(obs.SearchedAfter))
	}
	if obs.ToolCallCount != 1 {
		t.Errorf("expected 1 tool call, got %d", obs.ToolCallCount)
	}
}

func TestFinalizeEpisode_SetsTaskCompleted(t *testing.T) {
	tracker := NewEpisodeTracker()
	tracker.StartEpisode("agent-1", "coder", 1)

	obs := tracker.FinalizeEpisode("I have successfully completed the task", nil)

	if !obs.TaskCompleted {
		t.Error("expected task completed for success response")
	}
}

func TestFinalizeEpisode_DetectsHedging(t *testing.T) {
	tracker := NewEpisodeTracker()
	tracker.StartEpisode("agent-1", "coder", 1)

	obs := tracker.FinalizeEpisode("I'm not sure if this is correct", nil)

	if !obs.HedgingDetected {
		t.Error("expected hedging detected")
	}
}

func TestFinalizeEpisode_ClearsCurrentEpisode(t *testing.T) {
	tracker := NewEpisodeTracker()
	tracker.StartEpisode("agent-1", "coder", 1)

	tracker.FinalizeEpisode("done", nil)

	if tracker.HasActiveEpisode() {
		t.Error("expected no active episode after finalize")
	}
}

func TestFinalizeEpisode_NoEpisode(t *testing.T) {
	tracker := NewEpisodeTracker()

	obs := tracker.FinalizeEpisode("response", nil)

	if !obs.Timestamp.IsZero() {
		t.Error("expected zero observation with no episode")
	}
}

func TestFinalizeEpisode_IncludesAdditionalToolCalls(t *testing.T) {
	tracker := NewEpisodeTracker()
	tracker.StartEpisode("agent-1", "coder", 1)

	tracker.RecordToolCall("read_file")

	additionalCalls := []ToolCall{
		{Name: "write_file"},
		{Name: "run_tests"},
	}

	obs := tracker.FinalizeEpisode("done", additionalCalls)

	if obs.ToolCallCount != 3 {
		t.Errorf("expected 3 tool calls (1 + 2), got %d", obs.ToolCallCount)
	}
}

func TestFinalizeEpisode_SessionDuration(t *testing.T) {
	tracker := NewEpisodeTracker()
	tracker.StartEpisode("agent-1", "coder", 1)

	time.Sleep(10 * time.Millisecond)

	obs := tracker.FinalizeEpisode("done", nil)

	if obs.SessionDuration < 10*time.Millisecond {
		t.Errorf("expected session duration >= 10ms, got %v", obs.SessionDuration)
	}
}

// =============================================================================
// InferSatisfaction Tests
// =============================================================================

func TestInferSatisfaction_TaskCompletedNoFollowUp(t *testing.T) {
	obs := &EpisodeObservation{
		TaskCompleted: true,
		FollowUpCount: 0,
	}

	satisfaction := obs.InferSatisfaction()

	// TaskCompleted + NoFollowUp = +0.5
	if satisfaction < 0.4 || satisfaction > 0.6 {
		t.Errorf("expected ~0.5 for task completed, got %f", satisfaction)
	}
}

func TestInferSatisfaction_TaskNotCompleted(t *testing.T) {
	obs := &EpisodeObservation{
		TaskCompleted: false,
	}

	satisfaction := obs.InferSatisfaction()

	// !TaskCompleted = -0.4
	if satisfaction >= 0 {
		t.Errorf("expected negative for task not completed, got %f", satisfaction)
	}
}

func TestInferSatisfaction_FollowUpsReduce(t *testing.T) {
	obs := &EpisodeObservation{
		TaskCompleted: true,
		FollowUpCount: 3,
	}

	satisfaction := obs.InferSatisfaction()

	// TaskCompleted with follow-ups = +0.2 - 3*0.1 = -0.1
	if satisfaction >= 0.2 {
		t.Errorf("expected follow-ups to reduce satisfaction, got %f", satisfaction)
	}
}

func TestInferSatisfaction_HedgingReduces(t *testing.T) {
	obsWithHedging := &EpisodeObservation{
		TaskCompleted:   true,
		HedgingDetected: true,
	}

	obsWithoutHedging := &EpisodeObservation{
		TaskCompleted:   true,
		HedgingDetected: false,
	}

	withHedging := obsWithHedging.InferSatisfaction()
	withoutHedging := obsWithoutHedging.InferSatisfaction()

	if withHedging >= withoutHedging {
		t.Errorf("expected hedging to reduce satisfaction: %f >= %f", withHedging, withoutHedging)
	}
}

func TestInferSatisfaction_SearchesReduce(t *testing.T) {
	obs := &EpisodeObservation{
		TaskCompleted: true,
		SearchedAfter: []string{"q1", "q2", "q3"},
	}

	satisfaction := obs.InferSatisfaction()

	// Each search = -0.15, so 3 searches = -0.45
	// Combined with task completed = 0.5 - 0.45 = 0.05
	if satisfaction > 0.2 {
		t.Errorf("expected searches to significantly reduce satisfaction, got %f", satisfaction)
	}
}

func TestInferSatisfaction_WasteReduces(t *testing.T) {
	obs := &EpisodeObservation{
		TaskCompleted: true,
		PrefetchedIDs: []string{"f1", "f2", "f3", "f4", "f5"},
		UsedIDs:       []string{"f1"},
	}

	satisfaction := obs.InferSatisfaction()

	// 4 unused = -0.08, combined with completed = 0.5 - 0.08 = 0.42
	if satisfaction >= 0.5 {
		t.Errorf("expected waste to reduce satisfaction, got %f", satisfaction)
	}
}

// =============================================================================
// Completion Detection Tests
// =============================================================================

func TestDetectTaskCompletion_VariousIndicators(t *testing.T) {
	tests := []struct {
		response string
		expected bool
	}{
		{"The task is done", true},
		{"Successfully implemented the feature", true},
		{"I have fixed the bug", true},
		{"The changes are complete", true},
		{"Created the new file", true},
		{"Still working on it", false},
		{"I need more information", false},
		{"", false},
	}

	for _, tc := range tests {
		result := detectTaskCompletion(tc.response)
		if result != tc.expected {
			t.Errorf("detectTaskCompletion(%q) = %v, want %v", tc.response, result, tc.expected)
		}
	}
}

func TestDetectHedging_VariousIndicators(t *testing.T) {
	tests := []struct {
		response string
		expected bool
	}{
		{"I'm not sure about this", true},
		{"This might be the issue", true},
		{"Perhaps we should try", true},
		{"I don't know the answer", true},
		{"The solution is to use X", false},
		{"Here is the implementation", false},
		{"", false},
	}

	for _, tc := range tests {
		result := detectHedging(tc.response)
		if result != tc.expected {
			t.Errorf("detectHedging(%q) = %v, want %v", tc.response, result, tc.expected)
		}
	}
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestEpisodeTracker_ConcurrentRecordUsedID(t *testing.T) {
	tracker := NewEpisodeTracker()
	tracker.StartEpisode("agent-1", "coder", 1)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			tracker.RecordUsedID("file-" + string(rune('a'+(id%26))))
		}(i)
	}
	wg.Wait()

	// Should not panic and should have some IDs recorded
	count := tracker.GetUsedCount()
	if count == 0 {
		t.Error("expected some used IDs recorded")
	}
}

func TestEpisodeTracker_ConcurrentMixedOperations(t *testing.T) {
	tracker := NewEpisodeTracker()
	tracker.StartEpisode("agent-1", "coder", 1)

	var wg sync.WaitGroup

	// Concurrent RecordUsedID
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			tracker.RecordUsedID("used-" + string(rune('a'+(id%26))))
		}(i)
	}

	// Concurrent RecordSearchAfterPrefetch
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			tracker.RecordSearchAfterPrefetch("query-" + string(rune('a'+(id%26))))
		}(i)
	}

	// Concurrent RecordToolCall
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			tracker.RecordToolCall("tool-" + string(rune('a'+(id%26))))
		}(i)
	}

	wg.Wait()

	// Should not panic
	obs := tracker.FinalizeEpisode("done", nil)
	if len(obs.UsedIDs) == 0 {
		t.Error("expected some used IDs")
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestEpisodeTracker_EmptyEpisode(t *testing.T) {
	tracker := NewEpisodeTracker()
	tracker.StartEpisode("agent-1", "coder", 1)

	obs := tracker.FinalizeEpisode("", nil)

	if len(obs.PrefetchedIDs) != 0 {
		t.Error("expected empty prefetched IDs")
	}
	if len(obs.UsedIDs) != 0 {
		t.Error("expected empty used IDs")
	}
	if len(obs.SearchedAfter) != 0 {
		t.Error("expected empty searches")
	}
	if obs.ToolCallCount != 0 {
		t.Error("expected 0 tool calls")
	}
}

func TestNewEpisodeTrackerWithLimits_DefaultsOnInvalid(t *testing.T) {
	tracker := NewEpisodeTrackerWithLimits(-1, -1)

	// Should use defaults
	tracker.StartEpisode("agent-1", "coder", 1)

	// Record up to default limit
	for i := 0; i < DefaultMaxTrackedIDs+10; i++ {
		tracker.RecordUsedID("file-" + string(rune(i)))
	}

	if tracker.GetUsedCount() > DefaultMaxTrackedIDs {
		t.Errorf("expected max %d used, got %d", DefaultMaxTrackedIDs, tracker.GetUsedCount())
	}
}

func TestHasActiveEpisode_BeforeAndAfter(t *testing.T) {
	tracker := NewEpisodeTracker()

	if tracker.HasActiveEpisode() {
		t.Error("expected no active episode initially")
	}

	tracker.StartEpisode("agent-1", "coder", 1)
	if !tracker.HasActiveEpisode() {
		t.Error("expected active episode after start")
	}

	tracker.FinalizeEpisode("done", nil)
	if tracker.HasActiveEpisode() {
		t.Error("expected no active episode after finalize")
	}
}
