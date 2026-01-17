package llm

import (
	"sync"
	"testing"
)

// Helper to create a test request
func newTestRequest(sessionID, taskID, provider string, tokenEstimate int) *LLMRequest {
	return &LLMRequest{
		ID:            "test-id",
		SessionID:     sessionID,
		TaskID:        taskID,
		Provider:      provider,
		TokenEstimate: tokenEstimate,
	}
}

// TestNewTokenBudget verifies initial state of a new TokenBudget.
func TestNewTokenBudget(t *testing.T) {
	tracker := NewUsageTracker()
	budget := NewTokenBudget(tracker)

	if budget == nil {
		t.Fatal("NewTokenBudget returned nil")
	}
	if budget.globalLimit != UnlimitedBudget {
		t.Errorf("expected globalLimit = %d, got %d", UnlimitedBudget, budget.globalLimit)
	}
	if budget.tracker != tracker {
		t.Error("tracker not set correctly")
	}
}

// TestSetGlobalLimit tests setting global token limits.
func TestSetGlobalLimit(t *testing.T) {
	budget := NewTokenBudget(NewUsageTracker())

	budget.SetGlobalLimit(10000)
	if budget.globalLimit != 10000 {
		t.Errorf("expected globalLimit = 10000, got %d", budget.globalLimit)
	}

	budget.SetGlobalLimit(UnlimitedBudget)
	if budget.globalLimit != UnlimitedBudget {
		t.Errorf("expected globalLimit = %d, got %d", UnlimitedBudget, budget.globalLimit)
	}

	budget.SetGlobalLimit(0)
	if budget.globalLimit != 0 {
		t.Errorf("expected globalLimit = 0, got %d", budget.globalLimit)
	}
}

// TestSetSessionLimit tests setting session token limits.
func TestSetSessionLimit(t *testing.T) {
	budget := NewTokenBudget(NewUsageTracker())

	budget.SetSessionLimit("session1", 5000)
	if limit := budget.sessionLimits["session1"]; limit != 5000 {
		t.Errorf("expected session limit = 5000, got %d", limit)
	}

	budget.SetSessionLimit("session2", UnlimitedBudget)
	if limit := budget.sessionLimits["session2"]; limit != UnlimitedBudget {
		t.Errorf("expected session limit = %d, got %d", UnlimitedBudget, limit)
	}
}

// TestSetTaskLimit tests setting task token limits.
func TestSetTaskLimit(t *testing.T) {
	budget := NewTokenBudget(NewUsageTracker())

	budget.SetTaskLimit("task1", 2000)
	if limit := budget.taskLimits["task1"]; limit != 2000 {
		t.Errorf("expected task limit = 2000, got %d", limit)
	}
}

// TestSetProviderLimit tests setting provider token limits.
func TestSetProviderLimit(t *testing.T) {
	budget := NewTokenBudget(NewUsageTracker())

	budget.SetProviderLimit("openai", 100000)
	if limit := budget.providerLimits["openai"]; limit != 100000 {
		t.Errorf("expected provider limit = 100000, got %d", limit)
	}
}

// TestCheckBudgetUnlimited verifies unlimited budgets pass all checks.
func TestCheckBudgetUnlimited(t *testing.T) {
	budget := NewTokenBudget(NewUsageTracker())
	req := newTestRequest("session1", "task1", "openai", 10000)

	err := budget.CheckBudget(req)
	if err != nil {
		t.Errorf("expected no error for unlimited budget, got %v", err)
	}
}

// TestCheckBudgetGlobalExceeded verifies global budget enforcement.
func TestCheckBudgetGlobalExceeded(t *testing.T) {
	tracker := NewUsageTracker()
	budget := NewTokenBudget(tracker)
	budget.SetGlobalLimit(1000)

	// Record usage that uses up most of the budget
	tracker.Record(UsageRecord{TotalTokens: 900})

	// Request that would exceed the limit
	req := newTestRequest("session1", "task1", "openai", 200)
	err := budget.CheckBudget(req)
	if err != ErrGlobalBudgetExceeded {
		t.Errorf("expected ErrGlobalBudgetExceeded, got %v", err)
	}

	// Request that fits within the limit
	req = newTestRequest("session1", "task1", "openai", 50)
	err = budget.CheckBudget(req)
	if err != nil {
		t.Errorf("expected no error for request within budget, got %v", err)
	}
}

// TestCheckBudgetSessionExceeded verifies session budget enforcement.
func TestCheckBudgetSessionExceeded(t *testing.T) {
	tracker := NewUsageTracker()
	budget := NewTokenBudget(tracker)
	budget.SetSessionLimit("session1", 500)

	tracker.Record(UsageRecord{SessionID: "session1", TotalTokens: 400})

	// Request that would exceed session limit
	req := newTestRequest("session1", "task1", "openai", 200)
	err := budget.CheckBudget(req)
	if err != ErrSessionBudgetExceeded {
		t.Errorf("expected ErrSessionBudgetExceeded, got %v", err)
	}

	// Different session should not be affected
	req = newTestRequest("session2", "task1", "openai", 200)
	err = budget.CheckBudget(req)
	if err != nil {
		t.Errorf("expected no error for different session, got %v", err)
	}
}

// TestCheckBudgetTaskExceeded verifies task budget enforcement.
func TestCheckBudgetTaskExceeded(t *testing.T) {
	tracker := NewUsageTracker()
	budget := NewTokenBudget(tracker)
	budget.SetTaskLimit("task1", 300)

	tracker.Record(UsageRecord{TaskID: "task1", TotalTokens: 250})

	// Request that would exceed task limit
	req := newTestRequest("session1", "task1", "openai", 100)
	err := budget.CheckBudget(req)
	if err != ErrTaskBudgetExceeded {
		t.Errorf("expected ErrTaskBudgetExceeded, got %v", err)
	}

	// Different task should not be affected
	req = newTestRequest("session1", "task2", "openai", 100)
	err = budget.CheckBudget(req)
	if err != nil {
		t.Errorf("expected no error for different task, got %v", err)
	}
}

// TestCheckBudgetProviderExceeded verifies provider budget enforcement.
func TestCheckBudgetProviderExceeded(t *testing.T) {
	tracker := NewUsageTracker()
	budget := NewTokenBudget(tracker)
	budget.SetProviderLimit("openai", 1000)

	tracker.Record(UsageRecord{Provider: "openai", TotalTokens: 900})

	// Request that would exceed provider limit
	req := newTestRequest("session1", "task1", "openai", 200)
	err := budget.CheckBudget(req)
	if err != ErrProviderBudgetExceeded {
		t.Errorf("expected ErrProviderBudgetExceeded, got %v", err)
	}

	// Different provider should not be affected
	req = newTestRequest("session1", "task1", "anthropic", 200)
	err = budget.CheckBudget(req)
	if err != nil {
		t.Errorf("expected no error for different provider, got %v", err)
	}
}

// TestCheckBudgetHierarchy verifies the hierarchy: global -> session -> task -> provider.
func TestCheckBudgetHierarchy(t *testing.T) {
	tracker := NewUsageTracker()
	budget := NewTokenBudget(tracker)

	// Set all limits
	budget.SetGlobalLimit(10000)
	budget.SetSessionLimit("session1", 5000)
	budget.SetTaskLimit("task1", 2000)
	budget.SetProviderLimit("openai", 3000)

	tests := []struct {
		name           string
		setupUsage     []UsageRecord
		request        *LLMRequest
		expectedErr    error
		errDescription string
	}{
		{
			name:        "global limit exceeded first",
			setupUsage:  []UsageRecord{{TotalTokens: 9500}},
			request:     newTestRequest("session1", "task1", "openai", 600),
			expectedErr: ErrGlobalBudgetExceeded,
		},
		{
			name:        "session limit exceeded",
			setupUsage:  []UsageRecord{{SessionID: "session1", TotalTokens: 4800}},
			request:     newTestRequest("session1", "task1", "openai", 300),
			expectedErr: ErrSessionBudgetExceeded,
		},
		{
			name:        "task limit exceeded",
			setupUsage:  []UsageRecord{{SessionID: "session1", TaskID: "task1", TotalTokens: 1900}},
			request:     newTestRequest("session1", "task1", "openai", 200),
			expectedErr: ErrTaskBudgetExceeded,
		},
		{
			name:        "provider limit exceeded",
			setupUsage:  []UsageRecord{{SessionID: "session1", TaskID: "task2", Provider: "openai", TotalTokens: 2900}},
			request:     newTestRequest("session1", "task2", "openai", 200),
			expectedErr: ErrProviderBudgetExceeded,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tracker := NewUsageTracker()
			budget := NewTokenBudget(tracker)
			budget.SetGlobalLimit(10000)
			budget.SetSessionLimit("session1", 5000)
			budget.SetTaskLimit("task1", 2000)
			budget.SetProviderLimit("openai", 3000)

			for _, rec := range tc.setupUsage {
				tracker.Record(rec)
			}

			err := budget.CheckBudget(tc.request)
			if err != tc.expectedErr {
				t.Errorf("expected %v, got %v", tc.expectedErr, err)
			}
		})
	}
}

// TestCheckBudgetZeroLimit verifies zero limit behavior.
func TestCheckBudgetZeroLimit(t *testing.T) {
	tracker := NewUsageTracker()
	budget := NewTokenBudget(tracker)
	budget.SetGlobalLimit(0)

	req := newTestRequest("session1", "task1", "openai", 1)
	err := budget.CheckBudget(req)
	if err != ErrGlobalBudgetExceeded {
		t.Errorf("expected ErrGlobalBudgetExceeded for zero limit, got %v", err)
	}
}

// TestCheckBudgetExactLimit verifies behavior at exact limit boundary.
func TestCheckBudgetExactLimit(t *testing.T) {
	tracker := NewUsageTracker()
	budget := NewTokenBudget(tracker)
	budget.SetGlobalLimit(100)

	tracker.Record(UsageRecord{TotalTokens: 50})

	// Request that exactly reaches the limit should pass
	req := newTestRequest("session1", "task1", "openai", 50)
	err := budget.CheckBudget(req)
	if err != nil {
		t.Errorf("expected no error at exact limit boundary, got %v", err)
	}

	// Request that exceeds by 1 should fail
	req = newTestRequest("session1", "task1", "openai", 51)
	err = budget.CheckBudget(req)
	if err != ErrGlobalBudgetExceeded {
		t.Errorf("expected ErrGlobalBudgetExceeded, got %v", err)
	}
}

// TestBudgetRecordUsage verifies usage recording integration.
func TestBudgetRecordUsage(t *testing.T) {
	tracker := NewUsageTracker()
	budget := NewTokenBudget(tracker)

	record := UsageRecord{
		SessionID:   "session1",
		TaskID:      "task1",
		Provider:    "openai",
		TotalTokens: 500,
	}
	budget.RecordUsage(record)

	if tracker.TotalTokens() != 500 {
		t.Errorf("expected total tokens = 500, got %d", tracker.TotalTokens())
	}
}

// TestBudgetConcurrency verifies thread safety.
func TestBudgetConcurrency(t *testing.T) {
	tracker := NewUsageTracker()
	budget := NewTokenBudget(tracker)
	budget.SetGlobalLimit(1000000) // Large limit to allow many concurrent operations

	var wg sync.WaitGroup
	numGoroutines := 100
	opsPerGoroutine := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				// Mix of read and write operations
				budget.SetSessionLimit("session", int64(id*j))
				budget.SetTaskLimit("task", int64(id*j))
				budget.SetProviderLimit("provider", int64(id*j))
				budget.SetGlobalLimit(1000000)

				req := newTestRequest("session", "task", "provider", 1)
				budget.CheckBudget(req)

				budget.RecordUsage(UsageRecord{
					SessionID:   "session",
					TaskID:      "task",
					Provider:    "provider",
					TotalTokens: 1,
				})
			}
		}(i)
	}

	wg.Wait()

	// Verify we recorded the expected number of usages
	expectedTokens := int64(numGoroutines * opsPerGoroutine)
	if tracker.TotalTokens() != expectedTokens {
		t.Errorf("expected total tokens = %d, got %d", expectedTokens, tracker.TotalTokens())
	}
}

// TestUnlimitedBudgetConstant verifies the UnlimitedBudget constant.
func TestUnlimitedBudgetConstant(t *testing.T) {
	if UnlimitedBudget != -1 {
		t.Errorf("expected UnlimitedBudget = -1, got %d", UnlimitedBudget)
	}
}

// TestCheckBudgetNoLimitSet verifies behavior when no limit is set for a category.
func TestCheckBudgetNoLimitSet(t *testing.T) {
	tracker := NewUsageTracker()
	budget := NewTokenBudget(tracker)

	// Only set a global limit, leave others unset
	budget.SetGlobalLimit(10000)

	// Large request to session/task/provider without limits should pass
	req := newTestRequest("session1", "task1", "openai", 5000)
	err := budget.CheckBudget(req)
	if err != nil {
		t.Errorf("expected no error when session/task/provider limits not set, got %v", err)
	}
}

// TestBudgetErrorMessages verifies error constants.
func TestBudgetErrorMessages(t *testing.T) {
	tests := []struct {
		err     error
		message string
	}{
		{ErrGlobalBudgetExceeded, "global token budget exceeded"},
		{ErrSessionBudgetExceeded, "session token budget exceeded"},
		{ErrTaskBudgetExceeded, "task token budget exceeded"},
		{ErrProviderBudgetExceeded, "provider token budget exceeded"},
	}

	for _, tc := range tests {
		if tc.err.Error() != tc.message {
			t.Errorf("expected error message %q, got %q", tc.message, tc.err.Error())
		}
	}
}
