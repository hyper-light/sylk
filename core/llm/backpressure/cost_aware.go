// Package backpressure provides cost-aware backpressure mechanisms for LLM requests.
package backpressure

import (
	"fmt"
	"time"
)

// CostBackpressure applies cost-aware slowdown based on budget usage.
// It evaluates session and task usage to determine appropriate delays or rejections.
type CostBackpressure struct {
	config       CostBackpressureConfig
	budgetGetter BudgetGetter
}

// NewCostBackpressure creates backpressure with given config and budget getter.
// The config thresholds are sorted by usage percentage for consistent evaluation.
func NewCostBackpressure(config CostBackpressureConfig, budget BudgetGetter) *CostBackpressure {
	return &CostBackpressure{
		config:       config.SortThresholds(),
		budgetGetter: budget,
	}
}

// Evaluate returns the backpressure decision for a request.
// It checks both session and task usage, using the higher of the two.
func (cb *CostBackpressure) Evaluate(sessionID, taskID string) BackpressureDecision {
	usage := cb.getEffectiveUsage(sessionID, taskID)

	if usage >= cb.config.RejectNewAt {
		return cb.buildRejectDecision(usage)
	}

	return cb.buildDelayDecision(usage)
}

// getEffectiveUsage returns the higher of session and task usage.
func (cb *CostBackpressure) getEffectiveUsage(sessionID, taskID string) float64 {
	sessionUsage := cb.budgetGetter.GetUsagePercent(sessionID)
	taskUsage := cb.budgetGetter.GetTaskUsagePercent(sessionID, taskID)

	if taskUsage > sessionUsage {
		return taskUsage
	}
	return sessionUsage
}

// buildRejectDecision creates a rejection decision.
func (cb *CostBackpressure) buildRejectDecision(usage float64) BackpressureDecision {
	return BackpressureDecision{
		Delay:        0,
		Reject:       true,
		UsagePercent: usage,
		Reason:       fmt.Sprintf("budget usage %.1f%% exceeds rejection threshold %.1f%%", usage*100, cb.config.RejectNewAt*100),
	}
}

// buildDelayDecision creates a delay decision based on usage.
func (cb *CostBackpressure) buildDelayDecision(usage float64) BackpressureDecision {
	delay := cb.calculateDelay(usage)

	reason := "usage below all thresholds"
	if delay > 0 {
		reason = fmt.Sprintf("budget usage %.1f%% triggered delay", usage*100)
	}

	return BackpressureDecision{
		Delay:        delay,
		Reject:       false,
		UsagePercent: usage,
		Reason:       reason,
	}
}

// calculateDelay finds the applicable threshold and returns delay.
// Thresholds are checked from highest to lowest usage percentage.
// Returns 0 if usage is below the first threshold.
func (cb *CostBackpressure) calculateDelay(usage float64) time.Duration {
	thresholds := cb.config.DelayThresholds

	// Check from highest threshold to lowest
	for i := len(thresholds) - 1; i >= 0; i-- {
		if usage >= thresholds[i].UsagePercent {
			return cb.applyMultiplier(thresholds[i].Multiplier)
		}
	}

	return 0
}

// applyMultiplier calculates delay from base delay and multiplier.
func (cb *CostBackpressure) applyMultiplier(multiplier float64) time.Duration {
	return time.Duration(float64(cb.config.BaseDelay) * multiplier)
}
