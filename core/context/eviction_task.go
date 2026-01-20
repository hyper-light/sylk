// Package context provides types and utilities for adaptive retrieval context management.
// This file implements task completion eviction strategy for the Architect agent.
// ES.4: TaskCompletionEviction - evict completed task discussions.
package context

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// Note: EvictionStrategy and EvictionStrategyWithPriority interfaces are defined in eviction_strategy.go

// TaskCompletionEvictionName is the name identifier for the task completion eviction strategy.
const TaskCompletionEvictionName = "task_completion"

// DefaultTaskCompletionPriority is the default priority for task completion eviction.
const DefaultTaskCompletionPriority = 75

// =============================================================================
// Task Completion Markers
// =============================================================================

// DefaultCompletionMarkers are the default strings that indicate task completion.
var DefaultCompletionMarkers = []string{
	"task complete",
	"workflow complete",
	"plan approved",
	"task completed",
	"workflow completed",
	"done",
	"finished",
	"completed successfully",
}

// =============================================================================
// Configuration
// =============================================================================

// TaskCompletionConfig holds configuration for task completion eviction.
type TaskCompletionConfig struct {
	// CompletedTaskMarkers are strings that indicate a task is complete.
	CompletedTaskMarkers []string

	// PreserveLastNTasks is the number of recently completed tasks to preserve.
	PreserveLastNTasks int

	// TaskIDMetadataKey is the metadata key used to identify task IDs.
	TaskIDMetadataKey string

	// TaskStatusMetadataKey is the metadata key used to identify task status.
	TaskStatusMetadataKey string
}

// DefaultTaskCompletionConfig returns the default configuration.
func DefaultTaskCompletionConfig() TaskCompletionConfig {
	return TaskCompletionConfig{
		CompletedTaskMarkers:  DefaultCompletionMarkers,
		PreserveLastNTasks:    2,
		TaskIDMetadataKey:     "task_id",
		TaskStatusMetadataKey: "task_status",
	}
}

// =============================================================================
// Task Group
// =============================================================================

// TaskGroup represents a group of entries belonging to a single task.
type TaskGroup struct {
	TaskID       string
	Entries      []*ContentEntry
	TotalTokens  int
	LastActivity time.Time
	Status       string // "planning", "in_progress", "completed", "abandoned"
}

// Clone creates a deep copy of the task group.
func (tg *TaskGroup) Clone() *TaskGroup {
	if tg == nil {
		return nil
	}

	clone := &TaskGroup{
		TaskID:       tg.TaskID,
		TotalTokens:  tg.TotalTokens,
		LastActivity: tg.LastActivity,
		Status:       tg.Status,
	}

	clone.Entries = cloneEntries(tg.Entries)
	return clone
}

// cloneEntries creates a deep copy of a slice of ContentEntry pointers.
func cloneEntries(entries []*ContentEntry) []*ContentEntry {
	if entries == nil {
		return nil
	}
	cloned := make([]*ContentEntry, len(entries))
	for i, entry := range entries {
		cloned[i] = entry.Clone()
	}
	return cloned
}

// =============================================================================
// Task Completion Eviction
// =============================================================================

// TaskCompletionEviction implements EvictionStrategy for the Architect agent.
// It evicts completed task discussions while preserving ongoing tasks.
type TaskCompletionEviction struct {
	completedTaskMarkers []string
	// lowerMarkers contains pre-lowercased markers for O(1) lookup per marker.
	// This avoids repeated strings.ToLower calls during marker matching.
	lowerMarkers       []string
	preserveLastNTasks int
	taskIDKey          string
	taskStatusKey      string
	mu                 sync.RWMutex
}

// NewTaskCompletionEviction creates a new TaskCompletionEviction with the given config.
func NewTaskCompletionEviction(config TaskCompletionConfig) *TaskCompletionEviction {
	markers := config.CompletedTaskMarkers
	if len(markers) == 0 {
		markers = DefaultCompletionMarkers
	}

	taskIDKey := config.TaskIDMetadataKey
	if taskIDKey == "" {
		taskIDKey = "task_id"
	}

	taskStatusKey := config.TaskStatusMetadataKey
	if taskStatusKey == "" {
		taskStatusKey = "task_status"
	}

	// Pre-compute lowercased markers for O(n) matching instead of O(n*m).
	lowerMarkers := buildLowerMarkers(markers)

	return &TaskCompletionEviction{
		completedTaskMarkers: markers,
		lowerMarkers:         lowerMarkers,
		preserveLastNTasks:   config.PreserveLastNTasks,
		taskIDKey:            taskIDKey,
		taskStatusKey:        taskStatusKey,
	}
}

// buildLowerMarkers creates a slice of pre-lowercased markers.
func buildLowerMarkers(markers []string) []string {
	lower := make([]string, len(markers))
	for i, m := range markers {
		lower[i] = strings.ToLower(m)
	}
	return lower
}

// Name returns the strategy identifier.
func (e *TaskCompletionEviction) Name() string {
	return TaskCompletionEvictionName
}

// Priority returns the strategy priority. Lower values indicate higher priority.
func (e *TaskCompletionEviction) Priority() int {
	return DefaultTaskCompletionPriority
}

// SelectForEviction selects entries for eviction based on task completion.
// It returns entries from completed tasks, oldest first, up to targetTokens.
func (e *TaskCompletionEviction) SelectForEviction(
	_ context.Context,
	entries []*ContentEntry,
	targetTokens int,
) ([]*ContentEntry, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if len(entries) == 0 || targetTokens <= 0 {
		return nil, nil
	}

	taskGroups := e.groupByTask(entries)
	completedTasks := e.filterCompletedTasks(taskGroups)
	evictableTasks := e.excludeRecentTasks(completedTasks)

	return e.selectEntriesUpToTarget(evictableTasks, targetTokens), nil
}

// groupByTask groups entries by their task ID.
func (e *TaskCompletionEviction) groupByTask(entries []*ContentEntry) []*TaskGroup {
	taskMap := make(map[string]*TaskGroup)

	for _, entry := range entries {
		taskID := e.getTaskID(entry)
		group := e.getOrCreateGroup(taskMap, taskID)
		e.addEntryToGroup(group, entry)
	}

	return e.mapToSlice(taskMap)
}

// getTaskID extracts the task ID from an entry.
func (e *TaskCompletionEviction) getTaskID(entry *ContentEntry) string {
	if entry.Metadata != nil {
		if taskID, ok := entry.Metadata[e.taskIDKey]; ok && taskID != "" {
			return taskID
		}
	}
	return "default"
}

// getOrCreateGroup gets or creates a task group in the map.
func (e *TaskCompletionEviction) getOrCreateGroup(taskMap map[string]*TaskGroup, taskID string) *TaskGroup {
	if group, ok := taskMap[taskID]; ok {
		return group
	}
	group := &TaskGroup{TaskID: taskID, Status: "in_progress"}
	taskMap[taskID] = group
	return group
}

// addEntryToGroup adds an entry to a task group.
func (e *TaskCompletionEviction) addEntryToGroup(group *TaskGroup, entry *ContentEntry) {
	group.Entries = append(group.Entries, entry)
	group.TotalTokens += entry.TokenCount
	if entry.Timestamp.After(group.LastActivity) {
		group.LastActivity = entry.Timestamp
	}
}

// mapToSlice converts a task map to a slice.
func (e *TaskCompletionEviction) mapToSlice(taskMap map[string]*TaskGroup) []*TaskGroup {
	groups := make([]*TaskGroup, 0, len(taskMap))
	for _, group := range taskMap {
		groups = append(groups, group)
	}
	return groups
}

// filterCompletedTasks filters task groups to only include completed tasks.
// Pre-allocates slice with estimated capacity based on typical completion rates
// (approximately 50% of tasks are usually completed) to reduce allocations.
func (e *TaskCompletionEviction) filterCompletedTasks(groups []*TaskGroup) []*TaskGroup {
	// Pre-allocate with estimated capacity (assume ~50% completion rate)
	estimatedCapacity := len(groups) / 2
	if estimatedCapacity == 0 && len(groups) > 0 {
		estimatedCapacity = 1
	}
	completed := make([]*TaskGroup, 0, estimatedCapacity)
	for _, group := range groups {
		if e.isTaskComplete(group) {
			completed = append(completed, group)
		}
	}
	return completed
}

// isTaskComplete checks if a task group represents a completed task.
func (e *TaskCompletionEviction) isTaskComplete(group *TaskGroup) bool {
	if e.hasCompletedStatus(group) {
		return true
	}
	return e.hasCompletionMarkerInContent(group)
}

// hasCompletedStatus checks if the group has a completed status.
func (e *TaskCompletionEviction) hasCompletedStatus(group *TaskGroup) bool {
	return group.Status == "completed"
}

// hasCompletionMarkerInContent checks for completion markers in entry content.
func (e *TaskCompletionEviction) hasCompletionMarkerInContent(group *TaskGroup) bool {
	for _, entry := range group.Entries {
		if e.contentContainsMarker(entry.Content) {
			return true
		}
	}
	return false
}

// contentContainsMarker checks if content contains any completion marker.
// Uses pre-lowercased markers for O(n) complexity instead of O(n*m).
func (e *TaskCompletionEviction) contentContainsMarker(content string) bool {
	lowerContent := strings.ToLower(content)
	for _, marker := range e.lowerMarkers {
		if strings.Contains(lowerContent, marker) {
			return true
		}
	}
	return false
}

// excludeRecentTasks removes the most recent tasks from consideration.
func (e *TaskCompletionEviction) excludeRecentTasks(tasks []*TaskGroup) []*TaskGroup {
	if len(tasks) <= e.preserveLastNTasks {
		return nil
	}

	sortTasksByActivity(tasks)
	return tasks[:len(tasks)-e.preserveLastNTasks]
}

// sortTasksByActivity sorts tasks by last activity time, oldest first.
func sortTasksByActivity(tasks []*TaskGroup) {
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].LastActivity.Before(tasks[j].LastActivity)
	})
}

// selectEntriesUpToTarget selects entries from tasks up to the target token count.
func (e *TaskCompletionEviction) selectEntriesUpToTarget(tasks []*TaskGroup, target int) []*ContentEntry {
	var selected []*ContentEntry
	tokensSelected := 0

	for _, task := range tasks {
		if tokensSelected >= target {
			break
		}
		selected, tokensSelected = e.addTaskEntries(selected, tokensSelected, task, target)
	}

	return selected
}

// addTaskEntries adds entries from a task to the selected list.
func (e *TaskCompletionEviction) addTaskEntries(
	selected []*ContentEntry,
	tokensSelected int,
	task *TaskGroup,
	target int,
) ([]*ContentEntry, int) {
	for _, entry := range task.Entries {
		if tokensSelected >= target {
			break
		}
		selected = append(selected, entry)
		tokensSelected += entry.TokenCount
	}
	return selected, tokensSelected
}

// =============================================================================
// Configuration Methods
// =============================================================================

// SetMarkers sets the completion markers (thread-safe).
// Also updates the pre-lowercased markers cache.
func (e *TaskCompletionEviction) SetMarkers(markers []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.completedTaskMarkers = markers
	e.lowerMarkers = buildLowerMarkers(markers)
}

// GetMarkers returns the current completion markers (thread-safe).
func (e *TaskCompletionEviction) GetMarkers() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]string, len(e.completedTaskMarkers))
	copy(result, e.completedTaskMarkers)
	return result
}

// SetPreserveLastNTasks sets the number of tasks to preserve (thread-safe).
func (e *TaskCompletionEviction) SetPreserveLastNTasks(n int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.preserveLastNTasks = n
}

// GetPreserveLastNTasks returns the number of tasks to preserve (thread-safe).
func (e *TaskCompletionEviction) GetPreserveLastNTasks() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.preserveLastNTasks
}

// =============================================================================
// Task Detection Utilities
// =============================================================================

// DetectTaskBoundaries identifies potential task boundaries in a sequence of entries.
// Returns indices where new tasks appear to begin. Index 0 is always a boundary
// if there are any entries.
func DetectTaskBoundaries(entries []*ContentEntry, taskIDKey string) []int {
	if len(entries) == 0 {
		return nil
	}

	// First entry is always a boundary
	boundaries := []int{0}
	currentTaskID := extractTaskID(entries[0], taskIDKey)

	for i := 1; i < len(entries); i++ {
		taskID := extractTaskID(entries[i], taskIDKey)
		if taskID != currentTaskID {
			boundaries = append(boundaries, i)
			currentTaskID = taskID
		}
	}

	return boundaries
}

// extractTaskID extracts the task ID from an entry's metadata.
func extractTaskID(entry *ContentEntry, taskIDKey string) string {
	if entry.Metadata == nil {
		return ""
	}
	return entry.Metadata[taskIDKey]
}

// CountTasksByStatus counts tasks by their status.
func CountTasksByStatus(groups []*TaskGroup) map[string]int {
	counts := make(map[string]int)
	for _, group := range groups {
		counts[group.Status]++
	}
	return counts
}

// =============================================================================
// Eviction Result
// =============================================================================

// TaskEvictionResult contains details about a task completion eviction operation.
type TaskEvictionResult struct {
	EvictedEntries []*ContentEntry
	TotalTokens    int
	TasksEvicted   int
	TasksPreserved int
}

// SelectForEvictionWithDetails performs eviction and returns detailed results.
func (e *TaskCompletionEviction) SelectForEvictionWithDetails(
	entries []*ContentEntry,
	targetTokens int,
) *TaskEvictionResult {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := &TaskEvictionResult{}

	if len(entries) == 0 || targetTokens <= 0 {
		return result
	}

	taskGroups := e.groupByTask(entries)
	completedTasks := e.filterCompletedTasks(taskGroups)

	result.TasksPreserved = min(len(completedTasks), e.preserveLastNTasks)
	evictableTasks := e.excludeRecentTasks(completedTasks)
	result.TasksEvicted = len(evictableTasks)

	result.EvictedEntries = e.selectEntriesUpToTarget(evictableTasks, targetTokens)
	result.TotalTokens = CalculateTotalTokens(result.EvictedEntries)

	return result
}
