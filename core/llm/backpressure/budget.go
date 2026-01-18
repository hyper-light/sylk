// Package backpressure provides cost-aware backpressure mechanisms for LLM requests.
package backpressure

import (
	"sync"

	"github.com/adalundhe/sylk/core/llm"
)

// DefaultBudgetGetter provides budget usage information from the existing budget system.
// It bridges the backpressure package with the core llm.TokenBudget tracking.
type DefaultBudgetGetter struct {
	mu      sync.RWMutex
	budget  *llm.TokenBudget
	tracker *llm.UsageTracker

	// sessionLimits stores per-session limits for percentage calculation
	sessionLimits map[string]int64
	// taskLimits stores per-task limits for percentage calculation
	taskLimits map[string]int64
}

// NewDefaultBudgetGetter creates a BudgetGetter that interfaces with existing tracking.
// It requires both a TokenBudget and UsageTracker to compute usage percentages.
func NewDefaultBudgetGetter(budget *llm.TokenBudget, tracker *llm.UsageTracker) *DefaultBudgetGetter {
	return &DefaultBudgetGetter{
		budget:        budget,
		tracker:       tracker,
		sessionLimits: make(map[string]int64),
		taskLimits:    make(map[string]int64),
	}
}

// SetSessionLimit sets the budget limit for a session used in percentage calculations.
func (bg *DefaultBudgetGetter) SetSessionLimit(sessionID string, limit int64) {
	bg.mu.Lock()
	defer bg.mu.Unlock()
	bg.sessionLimits[sessionID] = limit
}

// SetTaskLimit sets the budget limit for a task used in percentage calculations.
func (bg *DefaultBudgetGetter) SetTaskLimit(taskID string, limit int64) {
	bg.mu.Lock()
	defer bg.mu.Unlock()
	bg.taskLimits[taskID] = limit
}

// GetUsagePercent returns session budget usage as 0.0-1.0+ (can exceed 1.0 if over budget).
// Returns 0.0 if the session has no limit set or the tracker is nil.
func (bg *DefaultBudgetGetter) GetUsagePercent(sessionID string) float64 {
	if bg.tracker == nil {
		return 0.0
	}

	bg.mu.RLock()
	limit, hasLimit := bg.sessionLimits[sessionID]
	bg.mu.RUnlock()

	if !hasLimit || limit <= 0 {
		return 0.0
	}

	used := bg.tracker.TokensBySession(sessionID)
	return float64(used) / float64(limit)
}

// GetTaskUsagePercent returns task-specific budget usage as 0.0-1.0+ (can exceed 1.0).
// Returns 0.0 if the task has no limit set or the tracker is nil.
func (bg *DefaultBudgetGetter) GetTaskUsagePercent(sessionID, taskID string) float64 {
	if bg.tracker == nil {
		return 0.0
	}

	bg.mu.RLock()
	limit, hasLimit := bg.taskLimits[taskID]
	bg.mu.RUnlock()

	if !hasLimit || limit <= 0 {
		return 0.0
	}

	used := bg.tracker.TokensByTask(taskID)
	return float64(used) / float64(limit)
}

// MockBudgetGetter is a test implementation of BudgetGetter.
// It provides simple in-memory storage for configurable usage values.
type MockBudgetGetter struct {
	mu           sync.RWMutex
	SessionUsage map[string]float64
	TaskUsage    map[string]map[string]float64
}

// NewMockBudgetGetter creates a new MockBudgetGetter for testing.
func NewMockBudgetGetter() *MockBudgetGetter {
	return &MockBudgetGetter{
		SessionUsage: make(map[string]float64),
		TaskUsage:    make(map[string]map[string]float64),
	}
}

// SetSessionUsage sets the usage percentage for a session.
func (m *MockBudgetGetter) SetSessionUsage(sessionID string, usage float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SessionUsage[sessionID] = usage
}

// SetTaskUsage sets the usage percentage for a task within a session.
func (m *MockBudgetGetter) SetTaskUsage(sessionID, taskID string, usage float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.TaskUsage[sessionID] == nil {
		m.TaskUsage[sessionID] = make(map[string]float64)
	}
	m.TaskUsage[sessionID][taskID] = usage
}

// GetUsagePercent implements BudgetGetter interface.
// Returns the configured session usage or 0.0 if not set.
func (m *MockBudgetGetter) GetUsagePercent(sessionID string) float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.SessionUsage[sessionID]
}

// GetTaskUsagePercent implements BudgetGetter interface.
// Returns the configured task usage or 0.0 if not set.
func (m *MockBudgetGetter) GetTaskUsagePercent(sessionID, taskID string) float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if tasks, ok := m.TaskUsage[sessionID]; ok {
		return tasks[taskID]
	}
	return 0.0
}
