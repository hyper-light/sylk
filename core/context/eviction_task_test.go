package context

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Test Helpers
// =============================================================================

func createTestEntry(id string, taskID string, content string, tokens int, timestamp time.Time) *ContentEntry {
	return &ContentEntry{
		ID:         id,
		Content:    content,
		TokenCount: tokens,
		Timestamp:  timestamp,
		Metadata:   map[string]string{"task_id": taskID},
	}
}

func createTestEntryWithStatus(id, taskID, status, content string, tokens int, ts time.Time) *ContentEntry {
	return &ContentEntry{
		ID:         id,
		Content:    content,
		TokenCount: tokens,
		Timestamp:  ts,
		Metadata:   map[string]string{"task_id": taskID, "task_status": status},
	}
}

// =============================================================================
// DefaultTaskCompletionConfig Tests
// =============================================================================

func TestDefaultTaskCompletionConfig(t *testing.T) {
	cfg := DefaultTaskCompletionConfig()

	assert.NotEmpty(t, cfg.CompletedTaskMarkers)
	assert.Equal(t, 2, cfg.PreserveLastNTasks)
	assert.Equal(t, "task_id", cfg.TaskIDMetadataKey)
	assert.Equal(t, "task_status", cfg.TaskStatusMetadataKey)
}

func TestDefaultCompletionMarkers(t *testing.T) {
	assert.Contains(t, DefaultCompletionMarkers, "task complete")
	assert.Contains(t, DefaultCompletionMarkers, "workflow complete")
	assert.Contains(t, DefaultCompletionMarkers, "plan approved")
	assert.Contains(t, DefaultCompletionMarkers, "done")
}

// =============================================================================
// NewTaskCompletionEviction Tests
// =============================================================================

func TestNewTaskCompletionEvictionDefault(t *testing.T) {
	cfg := DefaultTaskCompletionConfig()
	eviction := NewTaskCompletionEviction(cfg)

	require.NotNil(t, eviction)
	assert.Equal(t, cfg.CompletedTaskMarkers, eviction.completedTaskMarkers)
	assert.Equal(t, cfg.PreserveLastNTasks, eviction.preserveLastNTasks)
}

func TestNewTaskCompletionEvictionEmptyMarkers(t *testing.T) {
	cfg := TaskCompletionConfig{
		CompletedTaskMarkers: nil,
		PreserveLastNTasks:   3,
	}
	eviction := NewTaskCompletionEviction(cfg)

	assert.Equal(t, DefaultCompletionMarkers, eviction.completedTaskMarkers)
}

func TestNewTaskCompletionEvictionEmptyKeys(t *testing.T) {
	cfg := TaskCompletionConfig{
		TaskIDMetadataKey:     "",
		TaskStatusMetadataKey: "",
	}
	eviction := NewTaskCompletionEviction(cfg)

	assert.Equal(t, "task_id", eviction.taskIDKey)
	assert.Equal(t, "task_status", eviction.taskStatusKey)
}

func TestNewTaskCompletionEvictionCustomConfig(t *testing.T) {
	cfg := TaskCompletionConfig{
		CompletedTaskMarkers:  []string{"finished", "done"},
		PreserveLastNTasks:    5,
		TaskIDMetadataKey:     "custom_task_id",
		TaskStatusMetadataKey: "custom_status",
	}
	eviction := NewTaskCompletionEviction(cfg)

	assert.Equal(t, []string{"finished", "done"}, eviction.completedTaskMarkers)
	assert.Equal(t, 5, eviction.preserveLastNTasks)
	assert.Equal(t, "custom_task_id", eviction.taskIDKey)
	assert.Equal(t, "custom_status", eviction.taskStatusKey)
}

// =============================================================================
// SelectForEviction Tests - Basic Cases
// =============================================================================

func TestSelectForEvictionEmptyEntries(t *testing.T) {
	cfg := DefaultTaskCompletionConfig()
	eviction := NewTaskCompletionEviction(cfg)
	ctx := context.Background()

	result, err := eviction.SelectForEviction(ctx, nil, 1000)
	require.NoError(t, err)
	assert.Nil(t, result)

	result, err = eviction.SelectForEviction(ctx, []*ContentEntry{}, 1000)
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestSelectForEvictionZeroTarget(t *testing.T) {
	cfg := DefaultTaskCompletionConfig()
	eviction := NewTaskCompletionEviction(cfg)

	entries := []*ContentEntry{
		createTestEntry("1", "task1", "Task complete", 100, time.Now()),
	}

	result, err := eviction.SelectForEviction(context.Background(), entries, 0)
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestSelectForEvictionNegativeTarget(t *testing.T) {
	cfg := DefaultTaskCompletionConfig()
	eviction := NewTaskCompletionEviction(cfg)

	entries := []*ContentEntry{
		createTestEntry("1", "task1", "Task complete", 100, time.Now()),
	}

	result, _ := eviction.SelectForEviction(context.Background(), entries, -100)
	assert.Nil(t, result)
}

// =============================================================================
// Task Boundary Detection Tests
// =============================================================================

func TestSelectForEvictionTaskBoundaryDetection(t *testing.T) {
	cfg := TaskCompletionConfig{
		CompletedTaskMarkers: DefaultCompletionMarkers,
		PreserveLastNTasks:   0, // Preserve none for this test
	}
	eviction := NewTaskCompletionEviction(cfg)

	baseTime := time.Now()
	entries := []*ContentEntry{
		createTestEntry("1", "task1", "Starting task 1", 100, baseTime),
		createTestEntry("2", "task1", "Working on task 1", 100, baseTime.Add(1*time.Minute)),
		createTestEntry("3", "task1", "Task complete", 100, baseTime.Add(2*time.Minute)),
		createTestEntry("4", "task2", "Starting task 2", 100, baseTime.Add(3*time.Minute)),
		createTestEntry("5", "task2", "Still working", 100, baseTime.Add(4*time.Minute)),
	}

	result, _ := eviction.SelectForEviction(context.Background(), entries, 500)

	// Only task1 is complete, so only task1 entries should be evicted
	assert.Len(t, result, 3)
	for _, entry := range result {
		assert.Equal(t, "task1", entry.Metadata["task_id"])
	}
}

func TestSelectForEvictionMultipleTaskBoundaries(t *testing.T) {
	cfg := TaskCompletionConfig{
		CompletedTaskMarkers: DefaultCompletionMarkers,
		PreserveLastNTasks:   0,
	}
	eviction := NewTaskCompletionEviction(cfg)

	baseTime := time.Now()
	entries := []*ContentEntry{
		createTestEntry("1", "task1", "Task 1 work", 50, baseTime),
		createTestEntry("2", "task1", "Task complete", 50, baseTime.Add(1*time.Minute)),
		createTestEntry("3", "task2", "Task 2 work", 50, baseTime.Add(2*time.Minute)),
		createTestEntry("4", "task2", "Workflow complete", 50, baseTime.Add(3*time.Minute)),
		createTestEntry("5", "task3", "Task 3 work", 50, baseTime.Add(4*time.Minute)),
		createTestEntry("6", "task3", "Done", 50, baseTime.Add(5*time.Minute)),
	}

	result, _ := eviction.SelectForEviction(context.Background(), entries, 1000)

	// All 3 tasks are complete and should be evicted
	assert.Len(t, result, 6)
}

// =============================================================================
// Preserving Recent Tasks Tests
// =============================================================================

func TestSelectForEvictionPreserveRecentTasks(t *testing.T) {
	cfg := TaskCompletionConfig{
		CompletedTaskMarkers: DefaultCompletionMarkers,
		PreserveLastNTasks:   2,
	}
	eviction := NewTaskCompletionEviction(cfg)

	baseTime := time.Now()
	entries := []*ContentEntry{
		// Task 1 - oldest completed
		createTestEntry("1", "task1", "Task complete", 100, baseTime),
		// Task 2 - middle completed
		createTestEntry("2", "task2", "Task complete", 100, baseTime.Add(1*time.Hour)),
		// Task 3 - newest completed
		createTestEntry("3", "task3", "Task complete", 100, baseTime.Add(2*time.Hour)),
	}

	result, _ := eviction.SelectForEviction(context.Background(), entries, 500)

	// Only task1 (oldest) should be evicted; task2 and task3 are preserved
	assert.Len(t, result, 1)
	assert.Equal(t, "task1", result[0].Metadata["task_id"])
}

func TestSelectForEvictionPreserveMoreThanCompleted(t *testing.T) {
	cfg := TaskCompletionConfig{
		CompletedTaskMarkers: DefaultCompletionMarkers,
		PreserveLastNTasks:   5, // More than completed tasks
	}
	eviction := NewTaskCompletionEviction(cfg)

	baseTime := time.Now()
	entries := []*ContentEntry{
		createTestEntry("1", "task1", "Task complete", 100, baseTime),
		createTestEntry("2", "task2", "Task complete", 100, baseTime.Add(1*time.Hour)),
	}

	result, _ := eviction.SelectForEviction(context.Background(), entries, 500)

	// All completed tasks are preserved, nothing to evict
	assert.Empty(t, result)
}

func TestSelectForEvictionPreserveZeroTasks(t *testing.T) {
	cfg := TaskCompletionConfig{
		CompletedTaskMarkers: DefaultCompletionMarkers,
		PreserveLastNTasks:   0,
	}
	eviction := NewTaskCompletionEviction(cfg)

	baseTime := time.Now()
	entries := []*ContentEntry{
		createTestEntry("1", "task1", "Task complete", 100, baseTime),
		createTestEntry("2", "task2", "Task complete", 100, baseTime.Add(1*time.Hour)),
	}

	result, _ := eviction.SelectForEviction(context.Background(), entries, 500)

	// All completed tasks should be evicted
	assert.Len(t, result, 2)
}

// =============================================================================
// Evicting Completed Tasks Tests
// =============================================================================

func TestSelectForEvictionCompletedTasksByMarker(t *testing.T) {
	markers := []string{"task complete", "workflow complete", "done", "finished"}

	for _, marker := range markers {
		t.Run("marker_"+marker, func(t *testing.T) {
			cfg := TaskCompletionConfig{
				CompletedTaskMarkers: []string{marker},
				PreserveLastNTasks:   0,
			}
			eviction := NewTaskCompletionEviction(cfg)

			entries := []*ContentEntry{
				createTestEntry("1", "task1", "This is "+marker, 100, time.Now()),
			}

			result, _ := eviction.SelectForEviction(context.Background(), entries, 500)
			assert.Len(t, result, 1)
		})
	}
}

func TestSelectForEvictionCompletedTasksCaseInsensitive(t *testing.T) {
	cfg := TaskCompletionConfig{
		CompletedTaskMarkers: []string{"task complete"},
		PreserveLastNTasks:   0,
	}
	eviction := NewTaskCompletionEviction(cfg)

	testCases := []string{"TASK COMPLETE", "Task Complete", "task COMPLETE", "TaSk CoMpLeTe"}

	for _, content := range testCases {
		t.Run("case_"+content, func(t *testing.T) {
			entries := []*ContentEntry{
				createTestEntry("1", "task1", content, 100, time.Now()),
			}

			result, _ := eviction.SelectForEviction(context.Background(), entries, 500)
			assert.Len(t, result, 1)
		})
	}
}

func TestSelectForEvictionByStatus(t *testing.T) {
	cfg := TaskCompletionConfig{
		CompletedTaskMarkers: DefaultCompletionMarkers,
		PreserveLastNTasks:   0,
	}
	eviction := NewTaskCompletionEviction(cfg)

	entries := []*ContentEntry{
		{
			ID:         "1",
			Content:    "No completion marker here",
			TokenCount: 100,
			Timestamp:  time.Now(),
			Metadata:   map[string]string{"task_id": "task1"},
		},
	}

	// Without completion marker or completed status, should not be evicted
	result, _ := eviction.SelectForEviction(context.Background(), entries, 500)
	assert.Empty(t, result)
}

// =============================================================================
// Edge Cases Tests
// =============================================================================

func TestSelectForEvictionNoMarkers(t *testing.T) {
	cfg := TaskCompletionConfig{
		CompletedTaskMarkers: []string{}, // Empty initially, will use defaults
		PreserveLastNTasks:   0,
	}
	eviction := NewTaskCompletionEviction(cfg)

	entries := []*ContentEntry{
		createTestEntry("1", "task1", "Regular content without markers", 100, time.Now()),
	}

	result, _ := eviction.SelectForEviction(context.Background(), entries, 500)
	assert.Empty(t, result)
}

func TestSelectForEvictionAllIncomplete(t *testing.T) {
	cfg := TaskCompletionConfig{
		CompletedTaskMarkers: DefaultCompletionMarkers,
		PreserveLastNTasks:   0,
	}
	eviction := NewTaskCompletionEviction(cfg)

	baseTime := time.Now()
	entries := []*ContentEntry{
		createTestEntry("1", "task1", "Working on task 1", 100, baseTime),
		createTestEntry("2", "task2", "Working on task 2", 100, baseTime.Add(1*time.Hour)),
		createTestEntry("3", "task3", "Still in progress", 100, baseTime.Add(2*time.Hour)),
	}

	result, _ := eviction.SelectForEviction(context.Background(), entries, 500)
	assert.Empty(t, result)
}

func TestSelectForEvictionAllComplete(t *testing.T) {
	cfg := TaskCompletionConfig{
		CompletedTaskMarkers: DefaultCompletionMarkers,
		PreserveLastNTasks:   1,
	}
	eviction := NewTaskCompletionEviction(cfg)

	baseTime := time.Now()
	entries := []*ContentEntry{
		createTestEntry("1", "task1", "Task complete", 100, baseTime),
		createTestEntry("2", "task2", "Task complete", 100, baseTime.Add(1*time.Hour)),
		createTestEntry("3", "task3", "Task complete", 100, baseTime.Add(2*time.Hour)),
	}

	result, _ := eviction.SelectForEviction(context.Background(), entries, 500)

	// Task3 (newest) preserved, task1 and task2 evicted
	assert.Len(t, result, 2)

	// Verify oldest tasks are evicted
	evictedTasks := make(map[string]bool)
	for _, entry := range result {
		evictedTasks[entry.Metadata["task_id"]] = true
	}
	assert.True(t, evictedTasks["task1"])
	assert.True(t, evictedTasks["task2"])
	assert.False(t, evictedTasks["task3"])
}

func TestSelectForEvictionNoTaskMetadata(t *testing.T) {
	cfg := TaskCompletionConfig{
		CompletedTaskMarkers: DefaultCompletionMarkers,
		PreserveLastNTasks:   0,
	}
	eviction := NewTaskCompletionEviction(cfg)

	entries := []*ContentEntry{
		{
			ID:         "1",
			Content:    "Task complete",
			TokenCount: 100,
			Timestamp:  time.Now(),
			Metadata:   nil, // No metadata
		},
		{
			ID:         "2",
			Content:    "Done",
			TokenCount: 100,
			Timestamp:  time.Now().Add(1 * time.Hour),
			Metadata:   map[string]string{}, // Empty metadata
		},
	}

	result, _ := eviction.SelectForEviction(context.Background(), entries, 500)

	// Entries without task_id go to "default" task
	assert.Len(t, result, 2)
}

func TestSelectForEvictionTargetTokenLimit(t *testing.T) {
	cfg := TaskCompletionConfig{
		CompletedTaskMarkers: DefaultCompletionMarkers,
		PreserveLastNTasks:   0,
	}
	eviction := NewTaskCompletionEviction(cfg)

	baseTime := time.Now()
	entries := []*ContentEntry{
		createTestEntry("1", "task1", "Task complete", 100, baseTime),
		createTestEntry("2", "task1", "More content", 100, baseTime.Add(1*time.Minute)),
		createTestEntry("3", "task1", "Even more", 100, baseTime.Add(2*time.Minute)),
	}

	// Request only 150 tokens
	result, _ := eviction.SelectForEviction(context.Background(), entries, 150)

	// Should get approximately 150 tokens worth
	totalTokens := 0
	for _, entry := range result {
		totalTokens += entry.TokenCount
	}
	assert.GreaterOrEqual(t, totalTokens, 100)
	assert.LessOrEqual(t, totalTokens, 200)
}

// =============================================================================
// Concurrency Safety Tests
// =============================================================================

func TestSelectForEvictionConcurrentAccess(t *testing.T) {
	cfg := DefaultTaskCompletionConfig()
	eviction := NewTaskCompletionEviction(cfg)

	baseTime := time.Now()
	entries := []*ContentEntry{
		createTestEntry("1", "task1", "Task complete", 100, baseTime),
		createTestEntry("2", "task2", "Task complete", 100, baseTime.Add(1*time.Hour)),
		createTestEntry("3", "task3", "Task complete", 100, baseTime.Add(2*time.Hour)),
	}

	var wg sync.WaitGroup
	results := make(chan []*ContentEntry, 100)

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, _ := eviction.SelectForEviction(context.Background(), entries, 500)
			results <- result
		}()
	}

	wg.Wait()
	close(results)

	// All results should be consistent (same length)
	var firstLen int
	first := true
	for result := range results {
		if first {
			firstLen = len(result)
			first = false
		}
		assert.Equal(t, firstLen, len(result))
	}
}

func TestSetMarkersConcurrentAccess(t *testing.T) {
	cfg := DefaultTaskCompletionConfig()
	eviction := NewTaskCompletionEviction(cfg)

	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			eviction.SetMarkers([]string{"marker" + string(rune('a'+n%26))})
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = eviction.GetMarkers()
		}()
	}

	wg.Wait()
	// No panics or data races is the success criteria
}

func TestSetPreserveLastNTasksConcurrentAccess(t *testing.T) {
	cfg := DefaultTaskCompletionConfig()
	eviction := NewTaskCompletionEviction(cfg)

	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			eviction.SetPreserveLastNTasks(n)
		}(i)
	}

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = eviction.GetPreserveLastNTasks()
		}()
	}

	wg.Wait()
}

// =============================================================================
// Configuration Methods Tests
// =============================================================================

func TestSetAndGetMarkers(t *testing.T) {
	cfg := DefaultTaskCompletionConfig()
	eviction := NewTaskCompletionEviction(cfg)

	newMarkers := []string{"custom1", "custom2"}
	eviction.SetMarkers(newMarkers)

	result := eviction.GetMarkers()
	assert.Equal(t, newMarkers, result)

	// Verify returned slice is a copy
	result[0] = "modified"
	assert.NotEqual(t, eviction.GetMarkers()[0], "modified")
}

func TestSetAndGetPreserveLastNTasks(t *testing.T) {
	cfg := DefaultTaskCompletionConfig()
	eviction := NewTaskCompletionEviction(cfg)

	eviction.SetPreserveLastNTasks(10)
	assert.Equal(t, 10, eviction.GetPreserveLastNTasks())

	eviction.SetPreserveLastNTasks(0)
	assert.Equal(t, 0, eviction.GetPreserveLastNTasks())
}

// =============================================================================
// TaskGroup Tests
// =============================================================================

func TestTaskGroupClone(t *testing.T) {
	baseTime := time.Now()
	original := &TaskGroup{
		TaskID:       "task1",
		TotalTokens:  500,
		LastActivity: baseTime,
		Status:       "completed",
		Entries: []*ContentEntry{
			createTestEntry("1", "task1", "Content 1", 250, baseTime),
			createTestEntry("2", "task1", "Content 2", 250, baseTime.Add(1*time.Minute)),
		},
	}

	clone := original.Clone()

	assert.Equal(t, original.TaskID, clone.TaskID)
	assert.Equal(t, original.TotalTokens, clone.TotalTokens)
	assert.Equal(t, original.LastActivity, clone.LastActivity)
	assert.Equal(t, original.Status, clone.Status)
	assert.Len(t, clone.Entries, len(original.Entries))

	// Verify entries are deep copied
	clone.Entries[0].Content = "Modified"
	assert.NotEqual(t, original.Entries[0].Content, clone.Entries[0].Content)
}

func TestTaskGroupCloneNil(t *testing.T) {
	var tg *TaskGroup
	clone := tg.Clone()
	assert.Nil(t, clone)
}

func TestTaskGroupCloneNilEntries(t *testing.T) {
	original := &TaskGroup{
		TaskID:  "task1",
		Entries: nil,
	}

	clone := original.Clone()
	assert.Nil(t, clone.Entries)
}

// =============================================================================
// DetectTaskBoundaries Tests
// =============================================================================

func TestDetectTaskBoundariesEmpty(t *testing.T) {
	result := DetectTaskBoundaries(nil, "task_id")
	assert.Nil(t, result)

	result = DetectTaskBoundaries([]*ContentEntry{}, "task_id")
	assert.Nil(t, result)
}

func TestDetectTaskBoundariesSingleTask(t *testing.T) {
	entries := []*ContentEntry{
		createTestEntry("1", "task1", "Content 1", 100, time.Now()),
		createTestEntry("2", "task1", "Content 2", 100, time.Now()),
		createTestEntry("3", "task1", "Content 3", 100, time.Now()),
	}

	result := DetectTaskBoundaries(entries, "task_id")
	assert.Equal(t, []int{0}, result)
}

func TestDetectTaskBoundariesMultipleTasks(t *testing.T) {
	entries := []*ContentEntry{
		createTestEntry("1", "task1", "Content 1", 100, time.Now()),
		createTestEntry("2", "task1", "Content 2", 100, time.Now()),
		createTestEntry("3", "task2", "Content 3", 100, time.Now()),
		createTestEntry("4", "task2", "Content 4", 100, time.Now()),
		createTestEntry("5", "task3", "Content 5", 100, time.Now()),
	}

	result := DetectTaskBoundaries(entries, "task_id")
	assert.Equal(t, []int{0, 2, 4}, result)
}

func TestDetectTaskBoundariesNoMetadata(t *testing.T) {
	entries := []*ContentEntry{
		{ID: "1", Content: "Content 1"},
		{ID: "2", Content: "Content 2"},
	}

	result := DetectTaskBoundaries(entries, "task_id")
	assert.Equal(t, []int{0}, result) // All entries have empty task_id
}

// =============================================================================
// CountTasksByStatus Tests
// =============================================================================

func TestCountTasksByStatusEmpty(t *testing.T) {
	result := CountTasksByStatus(nil)
	assert.Empty(t, result)

	result = CountTasksByStatus([]*TaskGroup{})
	assert.Empty(t, result)
}

func TestCountTasksByStatusMixed(t *testing.T) {
	groups := []*TaskGroup{
		{TaskID: "1", Status: "completed"},
		{TaskID: "2", Status: "completed"},
		{TaskID: "3", Status: "in_progress"},
		{TaskID: "4", Status: "planning"},
		{TaskID: "5", Status: "completed"},
	}

	result := CountTasksByStatus(groups)
	assert.Equal(t, 3, result["completed"])
	assert.Equal(t, 1, result["in_progress"])
	assert.Equal(t, 1, result["planning"])
}

// =============================================================================
// SelectForEvictionWithDetails Tests
// =============================================================================

func TestSelectForEvictionWithDetailsEmpty(t *testing.T) {
	cfg := DefaultTaskCompletionConfig()
	eviction := NewTaskCompletionEviction(cfg)

	result := eviction.SelectForEvictionWithDetails(nil, 1000)

	assert.Empty(t, result.EvictedEntries)
	assert.Equal(t, 0, result.TotalTokens)
	assert.Equal(t, 0, result.TasksEvicted)
	assert.Equal(t, 0, result.TasksPreserved)
}

func TestSelectForEvictionWithDetailsZeroTarget(t *testing.T) {
	cfg := DefaultTaskCompletionConfig()
	eviction := NewTaskCompletionEviction(cfg)

	entries := []*ContentEntry{
		createTestEntry("1", "task1", "Task complete", 100, time.Now()),
	}

	result := eviction.SelectForEvictionWithDetails(entries, 0)

	assert.Empty(t, result.EvictedEntries)
	assert.Equal(t, 0, result.TotalTokens)
}

func TestSelectForEvictionWithDetailsFull(t *testing.T) {
	cfg := TaskCompletionConfig{
		CompletedTaskMarkers: DefaultCompletionMarkers,
		PreserveLastNTasks:   1,
	}
	eviction := NewTaskCompletionEviction(cfg)

	baseTime := time.Now()
	entries := []*ContentEntry{
		createTestEntry("1", "task1", "Task complete", 100, baseTime),
		createTestEntry("2", "task2", "Task complete", 150, baseTime.Add(1*time.Hour)),
		createTestEntry("3", "task3", "Task complete", 200, baseTime.Add(2*time.Hour)),
	}

	result := eviction.SelectForEvictionWithDetails(entries, 1000)

	assert.Len(t, result.EvictedEntries, 2)
	assert.Equal(t, 250, result.TotalTokens) // 100 + 150
	assert.Equal(t, 2, result.TasksEvicted)
	assert.Equal(t, 1, result.TasksPreserved)
}

// =============================================================================
// cloneEntries Tests
// =============================================================================

func TestCloneEntriesNil(t *testing.T) {
	result := cloneEntries(nil)
	assert.Nil(t, result)
}

func TestCloneEntriesEmpty(t *testing.T) {
	result := cloneEntries([]*ContentEntry{})
	assert.NotNil(t, result)
	assert.Empty(t, result)
}

func TestCloneEntriesDeepCopy(t *testing.T) {
	original := []*ContentEntry{
		createTestEntry("1", "task1", "Content 1", 100, time.Now()),
		createTestEntry("2", "task1", "Content 2", 200, time.Now()),
	}

	cloned := cloneEntries(original)

	assert.Len(t, cloned, 2)

	// Modify clone and verify original is unchanged
	cloned[0].Content = "Modified"
	assert.NotEqual(t, original[0].Content, cloned[0].Content)
}

// =============================================================================
// CalculateTotalTokens Tests
// =============================================================================

func TestSumTokensEmpty(t *testing.T) {
	result := CalculateTotalTokens(nil)
	assert.Equal(t, 0, result)

	result = CalculateTotalTokens([]*ContentEntry{})
	assert.Equal(t, 0, result)
}

func TestSumTokensNormal(t *testing.T) {
	entries := []*ContentEntry{
		{TokenCount: 100},
		{TokenCount: 200},
		{TokenCount: 300},
	}

	result := CalculateTotalTokens(entries)
	assert.Equal(t, 600, result)
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestTaskCompletionEvictionIntegration(t *testing.T) {
	cfg := TaskCompletionConfig{
		CompletedTaskMarkers: DefaultCompletionMarkers,
		PreserveLastNTasks:   1,
	}
	eviction := NewTaskCompletionEviction(cfg)

	baseTime := time.Now()

	// Simulate a realistic workflow
	entries := []*ContentEntry{
		// Task 1: Complete old task (2 hours ago)
		createTestEntry("1", "task1", "Planning the feature", 50, baseTime.Add(-2*time.Hour)),
		createTestEntry("2", "task1", "Implementing the feature", 100, baseTime.Add(-90*time.Minute)),
		createTestEntry("3", "task1", "Task complete - feature implemented", 30, baseTime.Add(-1*time.Hour)),

		// Task 2: Complete recent task (30 minutes ago)
		createTestEntry("4", "task2", "Starting refactoring", 40, baseTime.Add(-45*time.Minute)),
		createTestEntry("5", "task2", "Done with refactoring", 80, baseTime.Add(-30*time.Minute)),

		// Task 3: Ongoing task
		createTestEntry("6", "task3", "Working on tests", 60, baseTime.Add(-15*time.Minute)),
		createTestEntry("7", "task3", "Still writing tests", 70, baseTime.Add(-5*time.Minute)),
	}

	// Request eviction of 500 tokens
	result, _ := eviction.SelectForEviction(context.Background(), entries, 500)

	// Task 1 should be evicted (oldest complete task)
	// Task 2 should be preserved (most recent complete task)
	// Task 3 should be preserved (not complete)
	evictedIDs := make(map[string]bool)
	for _, entry := range result {
		evictedIDs[entry.Metadata["task_id"]] = true
	}

	assert.True(t, evictedIDs["task1"], "Task 1 should be evicted")
	assert.False(t, evictedIDs["task2"], "Task 2 should be preserved")
	assert.False(t, evictedIDs["task3"], "Task 3 should be preserved")
}

func TestTaskCompletionEvictionWithMixedContent(t *testing.T) {
	cfg := TaskCompletionConfig{
		CompletedTaskMarkers: []string{"approved", "merged", "deployed"},
		PreserveLastNTasks:   0,
	}
	eviction := NewTaskCompletionEviction(cfg)

	baseTime := time.Now()
	entries := []*ContentEntry{
		createTestEntry("1", "pr-123", "Created PR for feature X", 100, baseTime),
		createTestEntry("2", "pr-123", "PR approved and merged", 50, baseTime.Add(1*time.Hour)),
		createTestEntry("3", "deploy-1", "Deploying to staging", 80, baseTime.Add(2*time.Hour)),
		createTestEntry("4", "deploy-1", "Deployed to production", 40, baseTime.Add(3*time.Hour)),
	}

	result, _ := eviction.SelectForEviction(context.Background(), entries, 1000)

	// Both tasks contain completion markers
	assert.Len(t, result, 4)

	totalTokens := 0
	for _, entry := range result {
		totalTokens += entry.TokenCount
	}
	assert.Equal(t, 270, totalTokens)
}

// =============================================================================
// W3M.11 - Performance Tests for O(n) Completion Matching
// =============================================================================

func TestCompletionMatchingPerformanceWithManyItems(t *testing.T) {
	// Test that completion matching scales linearly, not quadratically.
	// This verifies the W3M.11 fix: O(n) instead of O(n²).
	markers := []string{
		"task complete", "workflow complete", "done", "finished",
		"completed", "approved", "merged", "deployed", "released",
		"shipped", "delivered", "concluded", "resolved", "closed",
	}

	cfg := TaskCompletionConfig{
		CompletedTaskMarkers: markers,
		PreserveLastNTasks:   0,
	}
	eviction := NewTaskCompletionEviction(cfg)

	baseTime := time.Now()

	// Create 1000 entries across 100 tasks
	entries := make([]*ContentEntry, 0, 1000)
	for i := 0; i < 100; i++ {
		taskID := "task" + string(rune('0'+i/10)) + string(rune('0'+i%10))
		for j := 0; j < 10; j++ {
			content := "Working on " + taskID
			if j == 9 {
				content = "Task complete for " + taskID
			}
			entries = append(entries, createTestEntry(
				taskID+"-"+string(rune('0'+j)),
				taskID,
				content,
				100,
				baseTime.Add(time.Duration(i*10+j)*time.Minute),
			))
		}
	}

	// Measure execution time - should complete quickly with O(n) complexity
	start := time.Now()
	result, err := eviction.SelectForEviction(context.Background(), entries, 50000)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.NotEmpty(t, result)
	// With O(n) complexity, 1000 entries should complete in well under 1 second
	assert.Less(t, elapsed, 1*time.Second, "Completion matching took too long, possible O(n²) complexity")
}

func TestCompletionMatchingWithManyMarkers(t *testing.T) {
	// Test with many markers to verify pre-lowercasing optimization
	markers := make([]string, 100)
	for i := 0; i < 100; i++ {
		markers[i] = "marker" + string(rune('a'+i%26)) + string(rune('0'+i%10))
	}
	markers[50] = "special complete marker"

	cfg := TaskCompletionConfig{
		CompletedTaskMarkers: markers,
		PreserveLastNTasks:   0,
	}
	eviction := NewTaskCompletionEviction(cfg)

	baseTime := time.Now()
	entries := []*ContentEntry{
		createTestEntry("1", "task1", "Regular work content", 100, baseTime),
		createTestEntry("2", "task1", "This has the SPECIAL COMPLETE MARKER here", 100, baseTime.Add(1*time.Minute)),
	}

	result, err := eviction.SelectForEviction(context.Background(), entries, 500)

	require.NoError(t, err)
	assert.Len(t, result, 2, "Should detect completion marker regardless of case")
}

func TestLowerMarkersPrecomputation(t *testing.T) {
	// Verify that markers are pre-lowercased correctly
	cfg := TaskCompletionConfig{
		CompletedTaskMarkers: []string{"TASK COMPLETE", "Done", "FiNiShEd"},
		PreserveLastNTasks:   0,
	}
	eviction := NewTaskCompletionEviction(cfg)

	// Test with various case combinations
	testCases := []struct {
		content  string
		expected bool
	}{
		{"task complete", true},
		{"TASK COMPLETE", true},
		{"Task Complete", true},
		{"done", true},
		{"DONE", true},
		{"finished", true},
		{"FINISHED", true},
		{"no markers here", false},
	}

	for _, tc := range testCases {
		t.Run(tc.content, func(t *testing.T) {
			entries := []*ContentEntry{
				createTestEntry("1", "task1", tc.content, 100, time.Now()),
			}

			result, _ := eviction.SelectForEviction(context.Background(), entries, 500)

			if tc.expected {
				assert.Len(t, result, 1, "Should detect marker in: %s", tc.content)
			} else {
				assert.Empty(t, result, "Should not detect marker in: %s", tc.content)
			}
		})
	}
}

func TestSetMarkersUpdatesLowerCache(t *testing.T) {
	// Verify that SetMarkers updates the pre-lowercased cache
	cfg := TaskCompletionConfig{
		CompletedTaskMarkers: []string{"original marker"},
		PreserveLastNTasks:   0,
	}
	eviction := NewTaskCompletionEviction(cfg)

	entries := []*ContentEntry{
		createTestEntry("1", "task1", "NEW MARKER HERE", 100, time.Now()),
	}

	// Original marker should not match
	result, _ := eviction.SelectForEviction(context.Background(), entries, 500)
	assert.Empty(t, result, "Should not match with original markers")

	// Update markers
	eviction.SetMarkers([]string{"NEW MARKER"})

	// New marker should match (case-insensitive)
	result, _ = eviction.SelectForEviction(context.Background(), entries, 500)
	assert.Len(t, result, 1, "Should match after SetMarkers")
}

func TestCompletionDetectionEdgeCases(t *testing.T) {
	cfg := TaskCompletionConfig{
		CompletedTaskMarkers: []string{"done", "complete"},
		PreserveLastNTasks:   0,
	}
	eviction := NewTaskCompletionEviction(cfg)

	testCases := []struct {
		name     string
		content  string
		expected bool
	}{
		{"empty content", "", false},
		{"marker at start", "done with this task", true},
		{"marker at end", "this task is complete", true},
		{"marker in middle", "the task is done now", true},
		{"marker as substring", "abandoned task", true}, // "done" IS in "abandoned" - substring matching
		{"no marker present", "working on the task", false},
		{"partial marker", "com", false},
		{"unicode content with marker", "Task is done!", true},
		{"whitespace only", "   ", false},
		{"marker with extra spaces", "task is   done", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			entries := []*ContentEntry{
				createTestEntry("1", "task1", tc.content, 100, time.Now()),
			}

			result, _ := eviction.SelectForEviction(context.Background(), entries, 500)

			if tc.expected {
				assert.Len(t, result, 1, "Should detect completion in: %q", tc.content)
			} else {
				assert.Empty(t, result, "Should not detect completion in: %q", tc.content)
			}
		})
	}
}

func TestBuildLowerMarkers(t *testing.T) {
	// Test the helper function directly
	testCases := []struct {
		input    []string
		expected []string
	}{
		{nil, []string{}},
		{[]string{}, []string{}},
		{[]string{"HELLO"}, []string{"hello"}},
		{[]string{"Hello", "WORLD", "MiXeD"}, []string{"hello", "world", "mixed"}},
	}

	for _, tc := range testCases {
		result := buildLowerMarkers(tc.input)
		if tc.input == nil {
			assert.Len(t, result, 0)
		} else {
			assert.Equal(t, tc.expected, result)
		}
	}
}
