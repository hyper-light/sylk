package backpressure

import (
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/llm"
)

// TestDefaultBudgetGetterCreation tests creating a DefaultBudgetGetter.
func TestDefaultBudgetGetterCreation(t *testing.T) {
	tracker := llm.NewUsageTracker()
	budget := llm.NewTokenBudget(tracker)

	bg := NewDefaultBudgetGetter(budget, tracker)

	if bg == nil {
		t.Fatal("NewDefaultBudgetGetter returned nil")
	}

	if bg.budget != budget {
		t.Error("budget not set correctly")
	}

	if bg.tracker != tracker {
		t.Error("tracker not set correctly")
	}
}

// TestDefaultBudgetGetterInterface verifies interface implementation.
func TestDefaultBudgetGetterInterface(t *testing.T) {
	tracker := llm.NewUsageTracker()
	budget := llm.NewTokenBudget(tracker)
	bg := NewDefaultBudgetGetter(budget, tracker)

	// Verify it implements BudgetGetter
	var _ BudgetGetter = bg
}

// TestDefaultBudgetGetterSessionUsage tests session usage percentage calculation.
func TestDefaultBudgetGetterSessionUsage(t *testing.T) {
	tracker := llm.NewUsageTracker()
	budget := llm.NewTokenBudget(tracker)
	bg := NewDefaultBudgetGetter(budget, tracker)

	sessionID := "test-session"
	bg.SetSessionLimit(sessionID, 1000)

	// Record some usage
	tracker.Record(llm.UsageRecord{
		SessionID:   sessionID,
		TotalTokens: 500,
	})

	usage := bg.GetUsagePercent(sessionID)
	expected := 0.5

	if usage != expected {
		t.Errorf("expected usage = %f, got %f", expected, usage)
	}
}

// TestDefaultBudgetGetterTaskUsage tests task usage percentage calculation.
func TestDefaultBudgetGetterTaskUsage(t *testing.T) {
	tracker := llm.NewUsageTracker()
	budget := llm.NewTokenBudget(tracker)
	bg := NewDefaultBudgetGetter(budget, tracker)

	sessionID := "test-session"
	taskID := "test-task"
	bg.SetTaskLimit(taskID, 200)

	// Record some usage
	tracker.Record(llm.UsageRecord{
		SessionID:   sessionID,
		TaskID:      taskID,
		TotalTokens: 150,
	})

	usage := bg.GetTaskUsagePercent(sessionID, taskID)
	expected := 0.75

	if usage != expected {
		t.Errorf("expected usage = %f, got %f", expected, usage)
	}
}

// TestDefaultBudgetGetterNoLimit tests behavior when no limit is set.
func TestDefaultBudgetGetterNoLimit(t *testing.T) {
	tracker := llm.NewUsageTracker()
	budget := llm.NewTokenBudget(tracker)
	bg := NewDefaultBudgetGetter(budget, tracker)

	sessionID := "no-limit-session"
	taskID := "no-limit-task"

	// Record usage without setting limits
	tracker.Record(llm.UsageRecord{
		SessionID:   sessionID,
		TaskID:      taskID,
		TotalTokens: 500,
	})

	sessionUsage := bg.GetUsagePercent(sessionID)
	if sessionUsage != 0.0 {
		t.Errorf("expected 0.0 for session without limit, got %f", sessionUsage)
	}

	taskUsage := bg.GetTaskUsagePercent(sessionID, taskID)
	if taskUsage != 0.0 {
		t.Errorf("expected 0.0 for task without limit, got %f", taskUsage)
	}
}

// TestDefaultBudgetGetterZeroLimit tests behavior with zero limit.
func TestDefaultBudgetGetterZeroLimit(t *testing.T) {
	tracker := llm.NewUsageTracker()
	budget := llm.NewTokenBudget(tracker)
	bg := NewDefaultBudgetGetter(budget, tracker)

	sessionID := "zero-limit-session"
	taskID := "zero-limit-task"

	bg.SetSessionLimit(sessionID, 0)
	bg.SetTaskLimit(taskID, 0)

	tracker.Record(llm.UsageRecord{
		SessionID:   sessionID,
		TaskID:      taskID,
		TotalTokens: 500,
	})

	sessionUsage := bg.GetUsagePercent(sessionID)
	if sessionUsage != 0.0 {
		t.Errorf("expected 0.0 for zero limit session, got %f", sessionUsage)
	}

	taskUsage := bg.GetTaskUsagePercent(sessionID, taskID)
	if taskUsage != 0.0 {
		t.Errorf("expected 0.0 for zero limit task, got %f", taskUsage)
	}
}

// TestDefaultBudgetGetterNegativeLimit tests behavior with negative limit.
func TestDefaultBudgetGetterNegativeLimit(t *testing.T) {
	tracker := llm.NewUsageTracker()
	budget := llm.NewTokenBudget(tracker)
	bg := NewDefaultBudgetGetter(budget, tracker)

	sessionID := "negative-limit-session"
	taskID := "negative-limit-task"

	bg.SetSessionLimit(sessionID, -100)
	bg.SetTaskLimit(taskID, -50)

	tracker.Record(llm.UsageRecord{
		SessionID:   sessionID,
		TaskID:      taskID,
		TotalTokens: 500,
	})

	sessionUsage := bg.GetUsagePercent(sessionID)
	if sessionUsage != 0.0 {
		t.Errorf("expected 0.0 for negative limit session, got %f", sessionUsage)
	}

	taskUsage := bg.GetTaskUsagePercent(sessionID, taskID)
	if taskUsage != 0.0 {
		t.Errorf("expected 0.0 for negative limit task, got %f", taskUsage)
	}
}

// TestDefaultBudgetGetterOverBudget tests usage exceeding 100%.
func TestDefaultBudgetGetterOverBudget(t *testing.T) {
	tracker := llm.NewUsageTracker()
	budget := llm.NewTokenBudget(tracker)
	bg := NewDefaultBudgetGetter(budget, tracker)

	sessionID := "over-budget-session"
	taskID := "over-budget-task"

	bg.SetSessionLimit(sessionID, 100)
	bg.SetTaskLimit(taskID, 100)

	// Record usage exceeding limits
	tracker.Record(llm.UsageRecord{
		SessionID:   sessionID,
		TaskID:      taskID,
		TotalTokens: 150,
	})

	sessionUsage := bg.GetUsagePercent(sessionID)
	if sessionUsage != 1.5 {
		t.Errorf("expected session usage = 1.5 (150%%), got %f", sessionUsage)
	}

	taskUsage := bg.GetTaskUsagePercent(sessionID, taskID)
	if taskUsage != 1.5 {
		t.Errorf("expected task usage = 1.5 (150%%), got %f", taskUsage)
	}
}

// TestDefaultBudgetGetterNilTracker tests behavior with nil tracker.
func TestDefaultBudgetGetterNilTracker(t *testing.T) {
	budget := llm.NewTokenBudget(nil)
	bg := NewDefaultBudgetGetter(budget, nil)

	sessionID := "test-session"
	taskID := "test-task"

	bg.SetSessionLimit(sessionID, 1000)
	bg.SetTaskLimit(taskID, 500)

	sessionUsage := bg.GetUsagePercent(sessionID)
	if sessionUsage != 0.0 {
		t.Errorf("expected 0.0 for nil tracker, got %f", sessionUsage)
	}

	taskUsage := bg.GetTaskUsagePercent(sessionID, taskID)
	if taskUsage != 0.0 {
		t.Errorf("expected 0.0 for nil tracker, got %f", taskUsage)
	}
}

// TestDefaultBudgetGetterConcurrentAccess tests thread safety.
func TestDefaultBudgetGetterConcurrentAccess(t *testing.T) {
	tracker := llm.NewUsageTracker()
	budget := llm.NewTokenBudget(tracker)
	bg := NewDefaultBudgetGetter(budget, tracker)

	var wg sync.WaitGroup
	numGoroutines := 100

	// Concurrently set limits and read usage
	for i := 0; i < numGoroutines; i++ {
		wg.Add(2)

		go func(id int) {
			defer wg.Done()
			sessionID := "session"
			bg.SetSessionLimit(sessionID, int64(1000+id))
		}(i)

		go func(id int) {
			defer wg.Done()
			sessionID := "session"
			_ = bg.GetUsagePercent(sessionID)
		}(i)
	}

	wg.Wait()
}

// TestDefaultBudgetGetterMultipleSessions tests multiple sessions.
func TestDefaultBudgetGetterMultipleSessions(t *testing.T) {
	tracker := llm.NewUsageTracker()
	budget := llm.NewTokenBudget(tracker)
	bg := NewDefaultBudgetGetter(budget, tracker)

	sessions := []struct {
		id    string
		limit int64
		used  int64
		want  float64
	}{
		{"session-a", 1000, 250, 0.25},
		{"session-b", 500, 400, 0.80},
		{"session-c", 200, 200, 1.0},
	}

	for _, s := range sessions {
		bg.SetSessionLimit(s.id, s.limit)
		tracker.Record(llm.UsageRecord{
			SessionID:   s.id,
			TotalTokens: s.used,
		})
	}

	for _, s := range sessions {
		got := bg.GetUsagePercent(s.id)
		if got != s.want {
			t.Errorf("session %s: expected %f, got %f", s.id, s.want, got)
		}
	}
}

// TestDefaultBudgetGetterMultipleTasks tests multiple tasks.
func TestDefaultBudgetGetterMultipleTasks(t *testing.T) {
	tracker := llm.NewUsageTracker()
	budget := llm.NewTokenBudget(tracker)
	bg := NewDefaultBudgetGetter(budget, tracker)

	sessionID := "multi-task-session"
	tasks := []struct {
		id    string
		limit int64
		used  int64
		want  float64
	}{
		{"task-a", 100, 50, 0.5},
		{"task-b", 200, 180, 0.9},
		{"task-c", 50, 75, 1.5},
	}

	for _, task := range tasks {
		bg.SetTaskLimit(task.id, task.limit)
		tracker.Record(llm.UsageRecord{
			SessionID:   sessionID,
			TaskID:      task.id,
			TotalTokens: task.used,
		})
	}

	for _, task := range tasks {
		got := bg.GetTaskUsagePercent(sessionID, task.id)
		if got != task.want {
			t.Errorf("task %s: expected %f, got %f", task.id, task.want, got)
		}
	}
}

// TestDefaultBudgetGetterAccumulatedUsage tests accumulated usage over time.
func TestDefaultBudgetGetterAccumulatedUsage(t *testing.T) {
	tracker := llm.NewUsageTracker()
	budget := llm.NewTokenBudget(tracker)
	bg := NewDefaultBudgetGetter(budget, tracker)

	sessionID := "accumulate-session"
	bg.SetSessionLimit(sessionID, 1000)

	// Record multiple usage events
	for i := 0; i < 5; i++ {
		tracker.Record(llm.UsageRecord{
			SessionID:   sessionID,
			TotalTokens: 100,
		})
	}

	// Should be 500/1000 = 0.5
	usage := bg.GetUsagePercent(sessionID)
	if usage != 0.5 {
		t.Errorf("expected accumulated usage = 0.5, got %f", usage)
	}
}

// TestMockBudgetGetterCreation tests creating a MockBudgetGetter.
func TestMockBudgetGetterCreation(t *testing.T) {
	mock := NewMockBudgetGetter()

	if mock == nil {
		t.Fatal("NewMockBudgetGetter returned nil")
	}

	if mock.SessionUsage == nil {
		t.Error("SessionUsage map not initialized")
	}

	if mock.TaskUsage == nil {
		t.Error("TaskUsage map not initialized")
	}
}

// TestMockBudgetGetterInterface verifies interface implementation.
func TestMockBudgetGetterInterface(t *testing.T) {
	mock := NewMockBudgetGetter()
	var _ BudgetGetter = mock
}

// TestMockBudgetGetterSessionUsage tests mock session usage.
func TestMockBudgetGetterSessionUsage(t *testing.T) {
	mock := NewMockBudgetGetter()

	sessionID := "mock-session"
	mock.SetSessionUsage(sessionID, 0.75)

	usage := mock.GetUsagePercent(sessionID)
	if usage != 0.75 {
		t.Errorf("expected 0.75, got %f", usage)
	}
}

// TestMockBudgetGetterTaskUsage tests mock task usage.
func TestMockBudgetGetterTaskUsage(t *testing.T) {
	mock := NewMockBudgetGetter()

	sessionID := "mock-session"
	taskID := "mock-task"
	mock.SetTaskUsage(sessionID, taskID, 0.85)

	usage := mock.GetTaskUsagePercent(sessionID, taskID)
	if usage != 0.85 {
		t.Errorf("expected 0.85, got %f", usage)
	}
}

// TestMockBudgetGetterNonexistentSession tests querying nonexistent session.
func TestMockBudgetGetterNonexistentSession(t *testing.T) {
	mock := NewMockBudgetGetter()

	usage := mock.GetUsagePercent("nonexistent")
	if usage != 0.0 {
		t.Errorf("expected 0.0 for nonexistent session, got %f", usage)
	}
}

// TestMockBudgetGetterNonexistentTask tests querying nonexistent task.
func TestMockBudgetGetterNonexistentTask(t *testing.T) {
	mock := NewMockBudgetGetter()

	// Session exists but task doesn't
	mock.SetSessionUsage("session", 0.5)
	usage := mock.GetTaskUsagePercent("session", "nonexistent")
	if usage != 0.0 {
		t.Errorf("expected 0.0 for nonexistent task, got %f", usage)
	}

	// Neither session nor task exists
	usage = mock.GetTaskUsagePercent("nosession", "notask")
	if usage != 0.0 {
		t.Errorf("expected 0.0 for nonexistent session/task, got %f", usage)
	}
}

// TestMockBudgetGetterOverBudget tests mock with over-budget values.
func TestMockBudgetGetterOverBudget(t *testing.T) {
	mock := NewMockBudgetGetter()

	mock.SetSessionUsage("over-session", 1.5)
	mock.SetTaskUsage("over-session", "over-task", 2.0)

	sessionUsage := mock.GetUsagePercent("over-session")
	if sessionUsage != 1.5 {
		t.Errorf("expected 1.5, got %f", sessionUsage)
	}

	taskUsage := mock.GetTaskUsagePercent("over-session", "over-task")
	if taskUsage != 2.0 {
		t.Errorf("expected 2.0, got %f", taskUsage)
	}
}

// TestMockBudgetGetterConcurrentAccess tests mock thread safety.
func TestMockBudgetGetterConcurrentAccess(t *testing.T) {
	mock := NewMockBudgetGetter()

	var wg sync.WaitGroup
	numGoroutines := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(3)

		go func(id int) {
			defer wg.Done()
			mock.SetSessionUsage("session", float64(id)/100.0)
		}(i)

		go func(id int) {
			defer wg.Done()
			mock.SetTaskUsage("session", "task", float64(id)/100.0)
		}(i)

		go func() {
			defer wg.Done()
			_ = mock.GetUsagePercent("session")
			_ = mock.GetTaskUsagePercent("session", "task")
		}()
	}

	wg.Wait()
}

// TestMockBudgetGetterMultipleSessionsTasks tests multiple sessions and tasks.
func TestMockBudgetGetterMultipleSessionsTasks(t *testing.T) {
	mock := NewMockBudgetGetter()

	// Set up multiple sessions with multiple tasks each
	mock.SetSessionUsage("session-1", 0.3)
	mock.SetSessionUsage("session-2", 0.6)
	mock.SetTaskUsage("session-1", "task-a", 0.4)
	mock.SetTaskUsage("session-1", "task-b", 0.5)
	mock.SetTaskUsage("session-2", "task-c", 0.7)

	// Verify all values
	if mock.GetUsagePercent("session-1") != 0.3 {
		t.Error("session-1 usage incorrect")
	}
	if mock.GetUsagePercent("session-2") != 0.6 {
		t.Error("session-2 usage incorrect")
	}
	if mock.GetTaskUsagePercent("session-1", "task-a") != 0.4 {
		t.Error("task-a usage incorrect")
	}
	if mock.GetTaskUsagePercent("session-1", "task-b") != 0.5 {
		t.Error("task-b usage incorrect")
	}
	if mock.GetTaskUsagePercent("session-2", "task-c") != 0.7 {
		t.Error("task-c usage incorrect")
	}
}

// TestMockBudgetGetterUpdateUsage tests updating usage values.
func TestMockBudgetGetterUpdateUsage(t *testing.T) {
	mock := NewMockBudgetGetter()

	sessionID := "update-session"
	taskID := "update-task"

	mock.SetSessionUsage(sessionID, 0.3)
	mock.SetTaskUsage(sessionID, taskID, 0.4)

	// Update values
	mock.SetSessionUsage(sessionID, 0.7)
	mock.SetTaskUsage(sessionID, taskID, 0.8)

	if mock.GetUsagePercent(sessionID) != 0.7 {
		t.Error("session usage not updated")
	}
	if mock.GetTaskUsagePercent(sessionID, taskID) != 0.8 {
		t.Error("task usage not updated")
	}
}

// TestMockBudgetGetterZeroUsage tests zero usage values.
func TestMockBudgetGetterZeroUsage(t *testing.T) {
	mock := NewMockBudgetGetter()

	mock.SetSessionUsage("zero-session", 0.0)
	mock.SetTaskUsage("zero-session", "zero-task", 0.0)

	if mock.GetUsagePercent("zero-session") != 0.0 {
		t.Error("expected 0.0 for explicitly set zero session usage")
	}
	if mock.GetTaskUsagePercent("zero-session", "zero-task") != 0.0 {
		t.Error("expected 0.0 for explicitly set zero task usage")
	}
}

// TestMockBudgetGetterNegativeUsage tests negative usage values (edge case).
func TestMockBudgetGetterNegativeUsage(t *testing.T) {
	mock := NewMockBudgetGetter()

	// While semantically invalid, the mock should store any value
	mock.SetSessionUsage("negative-session", -0.5)
	mock.SetTaskUsage("negative-session", "negative-task", -1.0)

	if mock.GetUsagePercent("negative-session") != -0.5 {
		t.Error("mock should store negative session usage")
	}
	if mock.GetTaskUsagePercent("negative-session", "negative-task") != -1.0 {
		t.Error("mock should store negative task usage")
	}
}

// TestBudgetGetterUsageThresholds tests usage at various threshold levels.
func TestBudgetGetterUsageThresholds(t *testing.T) {
	thresholds := []struct {
		name  string
		limit int64
		used  int64
		want  float64
	}{
		{"at_80_percent", 1000, 800, 0.8},
		{"at_90_percent", 1000, 900, 0.9},
		{"at_95_percent", 1000, 950, 0.95},
		{"at_98_percent", 1000, 980, 0.98},
		{"at_100_percent", 1000, 1000, 1.0},
		{"at_110_percent", 1000, 1100, 1.1},
	}

	for _, th := range thresholds {
		t.Run(th.name, func(t *testing.T) {
			tracker := llm.NewUsageTracker()
			budget := llm.NewTokenBudget(tracker)
			bg := NewDefaultBudgetGetter(budget, tracker)

			bg.SetSessionLimit(th.name, th.limit)
			tracker.Record(llm.UsageRecord{
				SessionID:   th.name,
				TotalTokens: th.used,
			})

			got := bg.GetUsagePercent(th.name)
			if got != th.want {
				t.Errorf("expected %f, got %f", th.want, got)
			}
		})
	}
}

// TestDefaultBudgetGetterWithTimestamp tests usage tracking with timestamps.
func TestDefaultBudgetGetterWithTimestamp(t *testing.T) {
	tracker := llm.NewUsageTracker()
	budget := llm.NewTokenBudget(tracker)
	bg := NewDefaultBudgetGetter(budget, tracker)

	sessionID := "timestamp-session"
	bg.SetSessionLimit(sessionID, 1000)

	// Record usage with different timestamps
	tracker.Record(llm.UsageRecord{
		Timestamp:   time.Now().Add(-1 * time.Hour),
		SessionID:   sessionID,
		TotalTokens: 200,
	})
	tracker.Record(llm.UsageRecord{
		Timestamp:   time.Now(),
		SessionID:   sessionID,
		TotalTokens: 300,
	})

	// Total should be 500/1000 = 0.5
	usage := bg.GetUsagePercent(sessionID)
	if usage != 0.5 {
		t.Errorf("expected 0.5, got %f", usage)
	}
}
