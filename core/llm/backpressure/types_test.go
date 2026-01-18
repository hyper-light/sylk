package backpressure

import (
	"testing"
	"time"
)

// TestDefaultCostBackpressureConfig verifies default configuration values.
func TestDefaultCostBackpressureConfig(t *testing.T) {
	cfg := DefaultCostBackpressureConfig()

	if cfg.RejectNewAt != 1.0 {
		t.Errorf("expected RejectNewAt = 1.0, got %f", cfg.RejectNewAt)
	}

	if cfg.BaseDelay != 100*time.Millisecond {
		t.Errorf("expected BaseDelay = 100ms, got %v", cfg.BaseDelay)
	}

	if len(cfg.DelayThresholds) != 4 {
		t.Fatalf("expected 4 delay thresholds, got %d", len(cfg.DelayThresholds))
	}

	expectedThresholds := []DelayThreshold{
		{UsagePercent: 0.80, Multiplier: 1.5},
		{UsagePercent: 0.90, Multiplier: 2.0},
		{UsagePercent: 0.95, Multiplier: 4.0},
		{UsagePercent: 0.98, Multiplier: 8.0},
	}

	for i, expected := range expectedThresholds {
		if cfg.DelayThresholds[i] != expected {
			t.Errorf("threshold[%d]: expected %+v, got %+v", i, expected, cfg.DelayThresholds[i])
		}
	}
}

// TestCostBackpressureConfigValidate tests configuration validation.
func TestCostBackpressureConfigValidate(t *testing.T) {
	tests := []struct {
		name   string
		config CostBackpressureConfig
		valid  bool
	}{
		{
			name:   "default config is valid",
			config: DefaultCostBackpressureConfig(),
			valid:  true,
		},
		{
			name: "empty thresholds is valid",
			config: CostBackpressureConfig{
				DelayThresholds: []DelayThreshold{},
				RejectNewAt:     1.0,
				BaseDelay:       100 * time.Millisecond,
			},
			valid: true,
		},
		{
			name: "zero base delay is valid",
			config: CostBackpressureConfig{
				DelayThresholds: []DelayThreshold{},
				RejectNewAt:     1.0,
				BaseDelay:       0,
			},
			valid: true,
		},
		{
			name: "negative base delay is invalid",
			config: CostBackpressureConfig{
				DelayThresholds: []DelayThreshold{},
				RejectNewAt:     1.0,
				BaseDelay:       -1 * time.Millisecond,
			},
			valid: false,
		},
		{
			name: "negative RejectNewAt is invalid",
			config: CostBackpressureConfig{
				DelayThresholds: []DelayThreshold{},
				RejectNewAt:     -0.5,
				BaseDelay:       100 * time.Millisecond,
			},
			valid: false,
		},
		{
			name: "negative usage percent threshold is invalid",
			config: CostBackpressureConfig{
				DelayThresholds: []DelayThreshold{
					{UsagePercent: -0.1, Multiplier: 1.5},
				},
				RejectNewAt: 1.0,
				BaseDelay:   100 * time.Millisecond,
			},
			valid: false,
		},
		{
			name: "usage percent > 1.0 is invalid",
			config: CostBackpressureConfig{
				DelayThresholds: []DelayThreshold{
					{UsagePercent: 1.5, Multiplier: 1.5},
				},
				RejectNewAt: 1.0,
				BaseDelay:   100 * time.Millisecond,
			},
			valid: false,
		},
		{
			name: "negative multiplier is invalid",
			config: CostBackpressureConfig{
				DelayThresholds: []DelayThreshold{
					{UsagePercent: 0.80, Multiplier: -1.0},
				},
				RejectNewAt: 1.0,
				BaseDelay:   100 * time.Millisecond,
			},
			valid: false,
		},
		{
			name: "zero multiplier is valid",
			config: CostBackpressureConfig{
				DelayThresholds: []DelayThreshold{
					{UsagePercent: 0.80, Multiplier: 0},
				},
				RejectNewAt: 1.0,
				BaseDelay:   100 * time.Millisecond,
			},
			valid: true,
		},
		{
			name: "RejectNewAt > 1.0 is valid",
			config: CostBackpressureConfig{
				DelayThresholds: []DelayThreshold{},
				RejectNewAt:     1.5,
				BaseDelay:       100 * time.Millisecond,
			},
			valid: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.config.Validate()
			if result != tc.valid {
				t.Errorf("expected Validate() = %v, got %v", tc.valid, result)
			}
		})
	}
}

// TestCostBackpressureConfigSortThresholds tests threshold sorting.
func TestCostBackpressureConfigSortThresholds(t *testing.T) {
	cfg := CostBackpressureConfig{
		DelayThresholds: []DelayThreshold{
			{UsagePercent: 0.95, Multiplier: 4.0},
			{UsagePercent: 0.80, Multiplier: 1.5},
			{UsagePercent: 0.98, Multiplier: 8.0},
			{UsagePercent: 0.90, Multiplier: 2.0},
		},
		RejectNewAt: 1.0,
		BaseDelay:   100 * time.Millisecond,
	}

	sorted := cfg.SortThresholds()

	expectedOrder := []float64{0.80, 0.90, 0.95, 0.98}
	for i, expected := range expectedOrder {
		if sorted.DelayThresholds[i].UsagePercent != expected {
			t.Errorf("sorted[%d].UsagePercent: expected %f, got %f",
				i, expected, sorted.DelayThresholds[i].UsagePercent)
		}
	}

	// Verify original is unchanged
	if cfg.DelayThresholds[0].UsagePercent != 0.95 {
		t.Error("original config was modified by SortThresholds")
	}

	// Verify other fields are preserved
	if sorted.RejectNewAt != cfg.RejectNewAt {
		t.Errorf("RejectNewAt not preserved: expected %f, got %f", cfg.RejectNewAt, sorted.RejectNewAt)
	}
	if sorted.BaseDelay != cfg.BaseDelay {
		t.Errorf("BaseDelay not preserved: expected %v, got %v", cfg.BaseDelay, sorted.BaseDelay)
	}
}

// TestCostBackpressureConfigSortThresholdsEmpty tests sorting empty thresholds.
func TestCostBackpressureConfigSortThresholdsEmpty(t *testing.T) {
	cfg := CostBackpressureConfig{
		DelayThresholds: []DelayThreshold{},
		RejectNewAt:     1.0,
		BaseDelay:       100 * time.Millisecond,
	}

	sorted := cfg.SortThresholds()

	if len(sorted.DelayThresholds) != 0 {
		t.Errorf("expected empty thresholds, got %d", len(sorted.DelayThresholds))
	}
}

// TestCostBackpressureConfigSortThresholdsAlreadySorted tests sorting already sorted thresholds.
func TestCostBackpressureConfigSortThresholdsAlreadySorted(t *testing.T) {
	cfg := DefaultCostBackpressureConfig()
	sorted := cfg.SortThresholds()

	for i, threshold := range sorted.DelayThresholds {
		if threshold != cfg.DelayThresholds[i] {
			t.Errorf("threshold[%d] changed unexpectedly", i)
		}
	}
}

// TestDelayThresholdStruct tests DelayThreshold struct.
func TestDelayThresholdStruct(t *testing.T) {
	threshold := DelayThreshold{
		UsagePercent: 0.85,
		Multiplier:   2.5,
	}

	if threshold.UsagePercent != 0.85 {
		t.Errorf("expected UsagePercent = 0.85, got %f", threshold.UsagePercent)
	}

	if threshold.Multiplier != 2.5 {
		t.Errorf("expected Multiplier = 2.5, got %f", threshold.Multiplier)
	}
}

// TestDelayThresholdEquality tests DelayThreshold comparison.
func TestDelayThresholdEquality(t *testing.T) {
	t1 := DelayThreshold{UsagePercent: 0.80, Multiplier: 1.5}
	t2 := DelayThreshold{UsagePercent: 0.80, Multiplier: 1.5}
	t3 := DelayThreshold{UsagePercent: 0.80, Multiplier: 2.0}

	if t1 != t2 {
		t.Error("identical thresholds should be equal")
	}

	if t1 == t3 {
		t.Error("thresholds with different multipliers should not be equal")
	}
}

// TestDelayThresholdIsValid tests the IsValid method on DelayThreshold.
func TestDelayThresholdIsValid(t *testing.T) {
	tests := []struct {
		name      string
		threshold DelayThreshold
		valid     bool
	}{
		{
			name:      "valid threshold",
			threshold: DelayThreshold{UsagePercent: 0.80, Multiplier: 1.5},
			valid:     true,
		},
		{
			name:      "zero usage percent is valid",
			threshold: DelayThreshold{UsagePercent: 0.0, Multiplier: 1.5},
			valid:     true,
		},
		{
			name:      "100% usage is valid",
			threshold: DelayThreshold{UsagePercent: 1.0, Multiplier: 1.5},
			valid:     true,
		},
		{
			name:      "zero multiplier is valid",
			threshold: DelayThreshold{UsagePercent: 0.80, Multiplier: 0},
			valid:     true,
		},
		{
			name:      "negative usage percent is invalid",
			threshold: DelayThreshold{UsagePercent: -0.1, Multiplier: 1.5},
			valid:     false,
		},
		{
			name:      "usage percent > 1.0 is invalid",
			threshold: DelayThreshold{UsagePercent: 1.5, Multiplier: 1.5},
			valid:     false,
		},
		{
			name:      "negative multiplier is invalid",
			threshold: DelayThreshold{UsagePercent: 0.80, Multiplier: -1.0},
			valid:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.threshold.IsValid()
			if result != tc.valid {
				t.Errorf("expected IsValid() = %v, got %v", tc.valid, result)
			}
		})
	}
}

// TestBackpressureDecisionShouldProceed tests the ShouldProceed method.
func TestBackpressureDecisionShouldProceed(t *testing.T) {
	tests := []struct {
		name     string
		decision BackpressureDecision
		expected bool
	}{
		{
			name:     "not rejected should proceed",
			decision: BackpressureDecision{Reject: false},
			expected: true,
		},
		{
			name:     "rejected should not proceed",
			decision: BackpressureDecision{Reject: true},
			expected: false,
		},
		{
			name: "not rejected with delay should proceed",
			decision: BackpressureDecision{
				Reject: false,
				Delay:  100 * time.Millisecond,
			},
			expected: true,
		},
		{
			name: "rejected with delay should not proceed",
			decision: BackpressureDecision{
				Reject: true,
				Delay:  100 * time.Millisecond,
			},
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.decision.ShouldProceed()
			if result != tc.expected {
				t.Errorf("expected ShouldProceed() = %v, got %v", tc.expected, result)
			}
		})
	}
}

// TestBackpressureDecisionHasDelay tests the HasDelay method.
func TestBackpressureDecisionHasDelay(t *testing.T) {
	tests := []struct {
		name     string
		decision BackpressureDecision
		expected bool
	}{
		{
			name:     "zero delay has no delay",
			decision: BackpressureDecision{Delay: 0},
			expected: false,
		},
		{
			name:     "positive delay has delay",
			decision: BackpressureDecision{Delay: 100 * time.Millisecond},
			expected: true,
		},
		{
			name:     "1 nanosecond delay has delay",
			decision: BackpressureDecision{Delay: 1 * time.Nanosecond},
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.decision.HasDelay()
			if result != tc.expected {
				t.Errorf("expected HasDelay() = %v, got %v", tc.expected, result)
			}
		})
	}
}

// TestBackpressureDecisionFields tests all BackpressureDecision fields.
func TestBackpressureDecisionFields(t *testing.T) {
	decision := BackpressureDecision{
		Delay:        150 * time.Millisecond,
		Reject:       true,
		UsagePercent: 0.95,
		Reason:       "budget at 95%",
	}

	if decision.Delay != 150*time.Millisecond {
		t.Errorf("expected Delay = 150ms, got %v", decision.Delay)
	}

	if !decision.Reject {
		t.Error("expected Reject = true")
	}

	if decision.UsagePercent != 0.95 {
		t.Errorf("expected UsagePercent = 0.95, got %f", decision.UsagePercent)
	}

	if decision.Reason != "budget at 95%" {
		t.Errorf("expected Reason = 'budget at 95%%', got %s", decision.Reason)
	}
}

// TestBackpressureDecisionZeroValue tests zero value BackpressureDecision.
func TestBackpressureDecisionZeroValue(t *testing.T) {
	var decision BackpressureDecision

	if decision.Delay != 0 {
		t.Errorf("expected zero Delay, got %v", decision.Delay)
	}

	if decision.Reject {
		t.Error("expected Reject = false for zero value")
	}

	if decision.UsagePercent != 0 {
		t.Errorf("expected zero UsagePercent, got %f", decision.UsagePercent)
	}

	if decision.Reason != "" {
		t.Errorf("expected empty Reason, got %s", decision.Reason)
	}

	// Zero value should proceed without delay
	if !decision.ShouldProceed() {
		t.Error("zero value decision should proceed")
	}

	if decision.HasDelay() {
		t.Error("zero value decision should have no delay")
	}
}

// TestBudgetGetterInterface verifies the BudgetGetter interface.
func TestBudgetGetterInterface(t *testing.T) {
	mock := NewMockBudgetGetter()

	// Verify it implements the interface
	var _ BudgetGetter = mock

	mock.SetSessionUsage("session1", 0.75)
	mock.SetTaskUsage("session1", "task1", 0.85)

	if usage := mock.GetUsagePercent("session1"); usage != 0.75 {
		t.Errorf("expected session usage = 0.75, got %f", usage)
	}

	if usage := mock.GetTaskUsagePercent("session1", "task1"); usage != 0.85 {
		t.Errorf("expected task usage = 0.85, got %f", usage)
	}

	// Non-existent keys should return 0
	if usage := mock.GetUsagePercent("nonexistent"); usage != 0 {
		t.Errorf("expected 0 for nonexistent session, got %f", usage)
	}

	if usage := mock.GetTaskUsagePercent("session1", "nonexistent"); usage != 0 {
		t.Errorf("expected 0 for nonexistent task, got %f", usage)
	}
}

// TestBudgetGetterOverBudget tests budget getter with usage > 1.0.
func TestBudgetGetterOverBudget(t *testing.T) {
	mock := NewMockBudgetGetter()
	mock.SetSessionUsage("session1", 1.25)
	mock.SetTaskUsage("session1", "task1", 1.5)

	if usage := mock.GetUsagePercent("session1"); usage != 1.25 {
		t.Errorf("expected session usage = 1.25, got %f", usage)
	}

	if usage := mock.GetTaskUsagePercent("session1", "task1"); usage != 1.5 {
		t.Errorf("expected task usage = 1.5, got %f", usage)
	}
}

// TestCostBackpressureConfigCopy tests that config copies are independent.
func TestCostBackpressureConfigCopy(t *testing.T) {
	original := DefaultCostBackpressureConfig()
	sorted := original.SortThresholds()

	// Modify sorted
	sorted.DelayThresholds[0].Multiplier = 999.0

	// Original should be unchanged
	if original.DelayThresholds[0].Multiplier == 999.0 {
		t.Error("modifying sorted config affected original")
	}
}

// TestDelayThresholdBoundaryValues tests boundary values for DelayThreshold.
func TestDelayThresholdBoundaryValues(t *testing.T) {
	tests := []struct {
		name      string
		threshold DelayThreshold
	}{
		{
			name:      "zero percent",
			threshold: DelayThreshold{UsagePercent: 0.0, Multiplier: 1.0},
		},
		{
			name:      "100 percent",
			threshold: DelayThreshold{UsagePercent: 1.0, Multiplier: 1.0},
		},
		{
			name:      "zero multiplier",
			threshold: DelayThreshold{UsagePercent: 0.5, Multiplier: 0.0},
		},
		{
			name:      "large multiplier",
			threshold: DelayThreshold{UsagePercent: 0.99, Multiplier: 100.0},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Just verify they can be created without panic
			_ = tc.threshold
		})
	}
}

// TestBackpressureDecisionCombinations tests various combinations of decision fields.
func TestBackpressureDecisionCombinations(t *testing.T) {
	tests := []struct {
		name          string
		decision      BackpressureDecision
		shouldProceed bool
		hasDelay      bool
	}{
		{
			name:          "allow no delay",
			decision:      BackpressureDecision{Reject: false, Delay: 0},
			shouldProceed: true,
			hasDelay:      false,
		},
		{
			name:          "allow with delay",
			decision:      BackpressureDecision{Reject: false, Delay: 200 * time.Millisecond},
			shouldProceed: true,
			hasDelay:      true,
		},
		{
			name:          "reject no delay",
			decision:      BackpressureDecision{Reject: true, Delay: 0},
			shouldProceed: false,
			hasDelay:      false,
		},
		{
			name:          "reject with delay",
			decision:      BackpressureDecision{Reject: true, Delay: 500 * time.Millisecond},
			shouldProceed: false,
			hasDelay:      true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.decision.ShouldProceed() != tc.shouldProceed {
				t.Errorf("expected ShouldProceed() = %v", tc.shouldProceed)
			}
			if tc.decision.HasDelay() != tc.hasDelay {
				t.Errorf("expected HasDelay() = %v", tc.hasDelay)
			}
		})
	}
}

// TestCostBackpressureConfigValidateMultipleInvalid tests validation with multiple invalid thresholds.
func TestCostBackpressureConfigValidateMultipleInvalid(t *testing.T) {
	cfg := CostBackpressureConfig{
		DelayThresholds: []DelayThreshold{
			{UsagePercent: 0.80, Multiplier: 1.5},  // valid
			{UsagePercent: -0.10, Multiplier: 2.0}, // invalid
			{UsagePercent: 0.95, Multiplier: 4.0},  // valid
		},
		RejectNewAt: 1.0,
		BaseDelay:   100 * time.Millisecond,
	}

	if cfg.Validate() {
		t.Error("config with invalid threshold should be invalid")
	}
}

// TestConfigSortPreservesMultipliers tests that sorting preserves correct multipliers.
func TestConfigSortPreservesMultipliers(t *testing.T) {
	cfg := CostBackpressureConfig{
		DelayThresholds: []DelayThreshold{
			{UsagePercent: 0.90, Multiplier: 2.0},
			{UsagePercent: 0.80, Multiplier: 1.5},
		},
		RejectNewAt: 1.0,
		BaseDelay:   100 * time.Millisecond,
	}

	sorted := cfg.SortThresholds()

	// After sorting, 0.80 should come first with its 1.5 multiplier
	if sorted.DelayThresholds[0].UsagePercent != 0.80 {
		t.Error("expected 0.80 to be first after sorting")
	}
	if sorted.DelayThresholds[0].Multiplier != 1.5 {
		t.Errorf("expected multiplier 1.5 for 0.80, got %f", sorted.DelayThresholds[0].Multiplier)
	}

	// 0.90 should come second with its 2.0 multiplier
	if sorted.DelayThresholds[1].UsagePercent != 0.90 {
		t.Error("expected 0.90 to be second after sorting")
	}
	if sorted.DelayThresholds[1].Multiplier != 2.0 {
		t.Errorf("expected multiplier 2.0 for 0.90, got %f", sorted.DelayThresholds[1].Multiplier)
	}
}
