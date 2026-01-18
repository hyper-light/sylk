package backpressure

import (
	"testing"
	"time"
)

func TestNewCostBackpressure(t *testing.T) {
	t.Parallel()

	config := DefaultCostBackpressureConfig()
	budget := NewMockBudgetGetter()

	cb := NewCostBackpressure(config, budget)

	if cb == nil {
		t.Fatal("expected non-nil CostBackpressure")
	}

	if cb.budgetGetter != budget {
		t.Error("expected budget getter to be set")
	}
}

func TestNewCostBackpressure_SortsThresholds(t *testing.T) {
	t.Parallel()

	// Create config with unsorted thresholds
	config := CostBackpressureConfig{
		DelayThresholds: []DelayThreshold{
			{UsagePercent: 0.95, Multiplier: 4.0},
			{UsagePercent: 0.80, Multiplier: 1.5},
			{UsagePercent: 0.98, Multiplier: 8.0},
			{UsagePercent: 0.90, Multiplier: 2.0},
		},
		RejectNewAt: 1.0,
		BaseDelay:   100 * time.Millisecond,
	}
	budget := NewMockBudgetGetter()

	cb := NewCostBackpressure(config, budget)

	// Verify thresholds are sorted
	for i := 1; i < len(cb.config.DelayThresholds); i++ {
		prev := cb.config.DelayThresholds[i-1].UsagePercent
		curr := cb.config.DelayThresholds[i].UsagePercent
		if prev > curr {
			t.Errorf("thresholds not sorted: %f > %f at index %d", prev, curr, i)
		}
	}
}

func TestEvaluate_BelowAllThresholds(t *testing.T) {
	t.Parallel()

	config := DefaultCostBackpressureConfig()
	budget := NewMockBudgetGetter()
	budget.SetSessionUsage("session1", 0.50) // 50% usage

	cb := NewCostBackpressure(config, budget)

	decision := cb.Evaluate("session1", "task1")

	if decision.Reject {
		t.Error("expected no rejection at 50% usage")
	}
	if decision.Delay != 0 {
		t.Errorf("expected zero delay at 50%%, got %v", decision.Delay)
	}
	if decision.UsagePercent != 0.50 {
		t.Errorf("expected usage 0.50, got %f", decision.UsagePercent)
	}
}

func TestEvaluate_FirstThreshold(t *testing.T) {
	t.Parallel()

	config := DefaultCostBackpressureConfig()
	budget := NewMockBudgetGetter()
	budget.SetSessionUsage("session1", 0.80) // Exactly at 80%

	cb := NewCostBackpressure(config, budget)

	decision := cb.Evaluate("session1", "task1")

	if decision.Reject {
		t.Error("expected no rejection at 80% usage")
	}

	expectedDelay := time.Duration(float64(config.BaseDelay) * 1.5)
	if decision.Delay != expectedDelay {
		t.Errorf("expected delay %v at 80%%, got %v", expectedDelay, decision.Delay)
	}
}

func TestEvaluate_SecondThreshold(t *testing.T) {
	t.Parallel()

	config := DefaultCostBackpressureConfig()
	budget := NewMockBudgetGetter()
	budget.SetSessionUsage("session1", 0.90) // Exactly at 90%

	cb := NewCostBackpressure(config, budget)

	decision := cb.Evaluate("session1", "task1")

	if decision.Reject {
		t.Error("expected no rejection at 90% usage")
	}

	expectedDelay := time.Duration(float64(config.BaseDelay) * 2.0)
	if decision.Delay != expectedDelay {
		t.Errorf("expected delay %v at 90%%, got %v", expectedDelay, decision.Delay)
	}
}

func TestEvaluate_ThirdThreshold(t *testing.T) {
	t.Parallel()

	config := DefaultCostBackpressureConfig()
	budget := NewMockBudgetGetter()
	budget.SetSessionUsage("session1", 0.95) // Exactly at 95%

	cb := NewCostBackpressure(config, budget)

	decision := cb.Evaluate("session1", "task1")

	if decision.Reject {
		t.Error("expected no rejection at 95% usage")
	}

	expectedDelay := time.Duration(float64(config.BaseDelay) * 4.0)
	if decision.Delay != expectedDelay {
		t.Errorf("expected delay %v at 95%%, got %v", expectedDelay, decision.Delay)
	}
}

func TestEvaluate_FourthThreshold(t *testing.T) {
	t.Parallel()

	config := DefaultCostBackpressureConfig()
	budget := NewMockBudgetGetter()
	budget.SetSessionUsage("session1", 0.98) // Exactly at 98%

	cb := NewCostBackpressure(config, budget)

	decision := cb.Evaluate("session1", "task1")

	if decision.Reject {
		t.Error("expected no rejection at 98% usage")
	}

	expectedDelay := time.Duration(float64(config.BaseDelay) * 8.0)
	if decision.Delay != expectedDelay {
		t.Errorf("expected delay %v at 98%%, got %v", expectedDelay, decision.Delay)
	}
}

func TestEvaluate_Rejection(t *testing.T) {
	t.Parallel()

	config := DefaultCostBackpressureConfig()
	budget := NewMockBudgetGetter()
	budget.SetSessionUsage("session1", 1.0) // Exactly at 100%

	cb := NewCostBackpressure(config, budget)

	decision := cb.Evaluate("session1", "task1")

	if !decision.Reject {
		t.Error("expected rejection at 100% usage")
	}
	if decision.Delay != 0 {
		t.Errorf("expected zero delay on rejection, got %v", decision.Delay)
	}
	if decision.ShouldProceed() {
		t.Error("ShouldProceed should return false when rejected")
	}
}

func TestEvaluate_RejectionOverBudget(t *testing.T) {
	t.Parallel()

	config := DefaultCostBackpressureConfig()
	budget := NewMockBudgetGetter()
	budget.SetSessionUsage("session1", 1.5) // 150% - over budget

	cb := NewCostBackpressure(config, budget)

	decision := cb.Evaluate("session1", "task1")

	if !decision.Reject {
		t.Error("expected rejection at 150% usage")
	}
	if decision.UsagePercent != 1.5 {
		t.Errorf("expected usage 1.5, got %f", decision.UsagePercent)
	}
}

func TestEvaluate_UsesHigherOfSessionAndTask(t *testing.T) {
	t.Parallel()

	config := DefaultCostBackpressureConfig()
	budget := NewMockBudgetGetter()
	budget.SetSessionUsage("session1", 0.50)                // 50% session usage
	budget.SetTaskUsage("session1", "task1", 0.95)          // 95% task usage

	cb := NewCostBackpressure(config, budget)

	decision := cb.Evaluate("session1", "task1")

	// Should use task usage (95%) which is higher
	expectedDelay := time.Duration(float64(config.BaseDelay) * 4.0)
	if decision.Delay != expectedDelay {
		t.Errorf("expected delay for 95%% usage, got %v", decision.Delay)
	}
	if decision.UsagePercent != 0.95 {
		t.Errorf("expected usage 0.95, got %f", decision.UsagePercent)
	}
}

func TestEvaluate_UsesSessionWhenHigher(t *testing.T) {
	t.Parallel()

	config := DefaultCostBackpressureConfig()
	budget := NewMockBudgetGetter()
	budget.SetSessionUsage("session1", 0.90)                // 90% session usage
	budget.SetTaskUsage("session1", "task1", 0.50)          // 50% task usage

	cb := NewCostBackpressure(config, budget)

	decision := cb.Evaluate("session1", "task1")

	// Should use session usage (90%) which is higher
	expectedDelay := time.Duration(float64(config.BaseDelay) * 2.0)
	if decision.Delay != expectedDelay {
		t.Errorf("expected delay for 90%% usage, got %v", decision.Delay)
	}
	if decision.UsagePercent != 0.90 {
		t.Errorf("expected usage 0.90, got %f", decision.UsagePercent)
	}
}

func TestEvaluate_TaskRejectsWhenOverBudget(t *testing.T) {
	t.Parallel()

	config := DefaultCostBackpressureConfig()
	budget := NewMockBudgetGetter()
	budget.SetSessionUsage("session1", 0.50)                // 50% session usage
	budget.SetTaskUsage("session1", "task1", 1.0)           // 100% task usage

	cb := NewCostBackpressure(config, budget)

	decision := cb.Evaluate("session1", "task1")

	if !decision.Reject {
		t.Error("expected rejection when task is at 100%")
	}
}

func TestEvaluate_BetweenThresholds(t *testing.T) {
	t.Parallel()

	config := DefaultCostBackpressureConfig()
	budget := NewMockBudgetGetter()
	budget.SetSessionUsage("session1", 0.85) // Between 80% and 90%

	cb := NewCostBackpressure(config, budget)

	decision := cb.Evaluate("session1", "task1")

	// Should use 80% threshold (1.5x multiplier)
	expectedDelay := time.Duration(float64(config.BaseDelay) * 1.5)
	if decision.Delay != expectedDelay {
		t.Errorf("expected delay %v for 85%% (80%% threshold), got %v", expectedDelay, decision.Delay)
	}
}

func TestEvaluate_JustBelowThreshold(t *testing.T) {
	t.Parallel()

	config := DefaultCostBackpressureConfig()
	budget := NewMockBudgetGetter()
	budget.SetSessionUsage("session1", 0.79) // Just below 80%

	cb := NewCostBackpressure(config, budget)

	decision := cb.Evaluate("session1", "task1")

	if decision.Delay != 0 {
		t.Errorf("expected zero delay at 79%%, got %v", decision.Delay)
	}
}

func TestCalculateDelay_NoThresholds(t *testing.T) {
	t.Parallel()

	config := CostBackpressureConfig{
		DelayThresholds: []DelayThreshold{},
		RejectNewAt:     1.0,
		BaseDelay:       100 * time.Millisecond,
	}
	budget := NewMockBudgetGetter()

	cb := NewCostBackpressure(config, budget)

	delay := cb.calculateDelay(0.95)

	if delay != 0 {
		t.Errorf("expected zero delay with no thresholds, got %v", delay)
	}
}

func TestCalculateDelay_CustomBaseDelay(t *testing.T) {
	t.Parallel()

	config := CostBackpressureConfig{
		DelayThresholds: []DelayThreshold{
			{UsagePercent: 0.50, Multiplier: 2.0},
		},
		RejectNewAt: 1.0,
		BaseDelay:   200 * time.Millisecond,
	}
	budget := NewMockBudgetGetter()

	cb := NewCostBackpressure(config, budget)

	delay := cb.calculateDelay(0.50)

	expectedDelay := 400 * time.Millisecond // 200ms * 2.0
	if delay != expectedDelay {
		t.Errorf("expected delay %v, got %v", expectedDelay, delay)
	}
}

func TestCalculateDelay_CustomRejectThreshold(t *testing.T) {
	t.Parallel()

	config := CostBackpressureConfig{
		DelayThresholds: []DelayThreshold{
			{UsagePercent: 0.80, Multiplier: 1.5},
		},
		RejectNewAt: 0.90, // Custom reject threshold
		BaseDelay:   100 * time.Millisecond,
	}
	budget := NewMockBudgetGetter()
	budget.SetSessionUsage("session1", 0.90)

	cb := NewCostBackpressure(config, budget)

	decision := cb.Evaluate("session1", "task1")

	if !decision.Reject {
		t.Error("expected rejection at custom 90% threshold")
	}
}

func TestDecision_ShouldProceed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		decision BackpressureDecision
		expected bool
	}{
		{
			name:     "not rejected",
			decision: BackpressureDecision{Reject: false},
			expected: true,
		},
		{
			name:     "rejected",
			decision: BackpressureDecision{Reject: true},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.decision.ShouldProceed(); got != tt.expected {
				t.Errorf("ShouldProceed() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDecision_HasDelay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		decision BackpressureDecision
		expected bool
	}{
		{
			name:     "no delay",
			decision: BackpressureDecision{Delay: 0},
			expected: false,
		},
		{
			name:     "has delay",
			decision: BackpressureDecision{Delay: 100 * time.Millisecond},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.decision.HasDelay(); got != tt.expected {
				t.Errorf("HasDelay() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestEvaluate_Reason_BelowThreshold(t *testing.T) {
	t.Parallel()

	config := DefaultCostBackpressureConfig()
	budget := NewMockBudgetGetter()
	budget.SetSessionUsage("session1", 0.50)

	cb := NewCostBackpressure(config, budget)

	decision := cb.Evaluate("session1", "task1")

	if decision.Reason != "usage below all thresholds" {
		t.Errorf("unexpected reason: %s", decision.Reason)
	}
}

func TestEvaluate_Reason_DelayTriggered(t *testing.T) {
	t.Parallel()

	config := DefaultCostBackpressureConfig()
	budget := NewMockBudgetGetter()
	budget.SetSessionUsage("session1", 0.85)

	cb := NewCostBackpressure(config, budget)

	decision := cb.Evaluate("session1", "task1")

	expected := "budget usage 85.0% triggered delay"
	if decision.Reason != expected {
		t.Errorf("expected reason %q, got %q", expected, decision.Reason)
	}
}

func TestEvaluate_Reason_Rejected(t *testing.T) {
	t.Parallel()

	config := DefaultCostBackpressureConfig()
	budget := NewMockBudgetGetter()
	budget.SetSessionUsage("session1", 1.0)

	cb := NewCostBackpressure(config, budget)

	decision := cb.Evaluate("session1", "task1")

	expected := "budget usage 100.0% exceeds rejection threshold 100.0%"
	if decision.Reason != expected {
		t.Errorf("expected reason %q, got %q", expected, decision.Reason)
	}
}

func TestEvaluate_ZeroUsage(t *testing.T) {
	t.Parallel()

	config := DefaultCostBackpressureConfig()
	budget := NewMockBudgetGetter()
	// No usage set, defaults to 0

	cb := NewCostBackpressure(config, budget)

	decision := cb.Evaluate("session1", "task1")

	if decision.Reject {
		t.Error("should not reject at zero usage")
	}
	if decision.Delay != 0 {
		t.Errorf("expected zero delay at zero usage, got %v", decision.Delay)
	}
	if decision.UsagePercent != 0 {
		t.Errorf("expected zero usage, got %f", decision.UsagePercent)
	}
}

func TestEvaluate_MultipleSessions(t *testing.T) {
	t.Parallel()

	config := DefaultCostBackpressureConfig()
	budget := NewMockBudgetGetter()
	budget.SetSessionUsage("session1", 0.95)
	budget.SetSessionUsage("session2", 0.50)

	cb := NewCostBackpressure(config, budget)

	decision1 := cb.Evaluate("session1", "task1")
	decision2 := cb.Evaluate("session2", "task1")

	// Session 1 should have 95% delay
	expectedDelay1 := time.Duration(float64(config.BaseDelay) * 4.0)
	if decision1.Delay != expectedDelay1 {
		t.Errorf("session1: expected delay %v, got %v", expectedDelay1, decision1.Delay)
	}

	// Session 2 should have no delay
	if decision2.Delay != 0 {
		t.Errorf("session2: expected zero delay, got %v", decision2.Delay)
	}
}
