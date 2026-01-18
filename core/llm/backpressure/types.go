// Package backpressure provides cost-aware backpressure mechanisms for LLM requests.
package backpressure

import (
	"sort"
	"time"
)

// CostBackpressureConfig configures backpressure thresholds.
type CostBackpressureConfig struct {
	// DelayThresholds maps budget usage percentages to delay multipliers.
	// Thresholds should be sorted by UsagePercent in ascending order.
	DelayThresholds []DelayThreshold

	// RejectNewAt is the usage percentage at which new requests are rejected.
	// For example, 1.0 means reject at 100% budget usage.
	RejectNewAt float64

	// BaseDelay is the base delay duration for calculations.
	BaseDelay time.Duration
}

// DelayThreshold defines a usage threshold and its corresponding delay multiplier.
type DelayThreshold struct {
	UsagePercent float64
	Multiplier   float64
}

// IsValid checks if the threshold values are within acceptable bounds.
func (t DelayThreshold) IsValid() bool {
	return t.UsagePercent >= 0 && t.UsagePercent <= 1.0 && t.Multiplier >= 0
}

// DefaultCostBackpressureConfig returns production defaults for cost backpressure.
func DefaultCostBackpressureConfig() CostBackpressureConfig {
	return CostBackpressureConfig{
		DelayThresholds: []DelayThreshold{
			{UsagePercent: 0.80, Multiplier: 1.5},
			{UsagePercent: 0.90, Multiplier: 2.0},
			{UsagePercent: 0.95, Multiplier: 4.0},
			{UsagePercent: 0.98, Multiplier: 8.0},
		},
		RejectNewAt: 1.0,
		BaseDelay:   100 * time.Millisecond,
	}
}

// Validate checks if the configuration is valid.
func (c CostBackpressureConfig) Validate() bool {
	if c.BaseDelay < 0 {
		return false
	}
	if c.RejectNewAt < 0 {
		return false
	}
	return c.validateThresholds()
}

// validateThresholds checks if thresholds are properly configured.
func (c CostBackpressureConfig) validateThresholds() bool {
	for _, t := range c.DelayThresholds {
		if !t.IsValid() {
			return false
		}
	}
	return true
}

// SortThresholds returns a copy of the config with thresholds sorted by UsagePercent.
func (c CostBackpressureConfig) SortThresholds() CostBackpressureConfig {
	sorted := make([]DelayThreshold, len(c.DelayThresholds))
	copy(sorted, c.DelayThresholds)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].UsagePercent < sorted[j].UsagePercent
	})
	return CostBackpressureConfig{
		DelayThresholds: sorted,
		RejectNewAt:     c.RejectNewAt,
		BaseDelay:       c.BaseDelay,
	}
}

// BackpressureDecision represents the backpressure to apply to a request.
type BackpressureDecision struct {
	// Delay is the duration to delay the request.
	Delay time.Duration

	// Reject indicates whether to reject the request entirely.
	Reject bool

	// UsagePercent is the current budget usage percentage.
	UsagePercent float64

	// Reason provides a human-readable explanation for the decision.
	Reason string
}

// ShouldProceed returns true if the request should proceed (possibly after delay).
func (d BackpressureDecision) ShouldProceed() bool {
	return !d.Reject
}

// HasDelay returns true if a delay should be applied.
func (d BackpressureDecision) HasDelay() bool {
	return d.Delay > 0
}

// BudgetGetter retrieves current budget usage for dependency injection.
type BudgetGetter interface {
	// GetUsagePercent returns the budget usage percentage for a session.
	// Returns a value between 0.0 and 1.0+ (can exceed 1.0 if over budget).
	GetUsagePercent(sessionID string) float64

	// GetTaskUsagePercent returns the budget usage percentage for a specific task.
	// Returns a value between 0.0 and 1.0+ (can exceed 1.0 if over budget).
	GetTaskUsagePercent(sessionID, taskID string) float64
}
