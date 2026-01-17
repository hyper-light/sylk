// Package errors implements a 5-tier error taxonomy with classification and handling behavior.
package errors

import (
	"sync"
)

// WorkaroundBudgetConfig configures the workaround token budget.
type WorkaroundBudgetConfig struct {
	// MaxTokens is the maximum token budget for workarounds (default: 1000).
	MaxTokens int `yaml:"max_tokens"`

	// ResetOnSuccess indicates whether to reset the budget on successful recovery (default: true).
	ResetOnSuccess bool `yaml:"reset_on_success"`

	// PerTypeBudgets allows per-tier budget limits (optional).
	PerTypeBudgets map[ErrorTier]int `yaml:"per_type_budgets"`
}

// DefaultWorkaroundBudgetConfig returns the default configuration.
func DefaultWorkaroundBudgetConfig() WorkaroundBudgetConfig {
	return WorkaroundBudgetConfig{
		MaxTokens:      1000,
		ResetOnSuccess: true,
		PerTypeBudgets: nil,
	}
}

// WorkaroundBudget tracks token spending for workaround attempts.
// Thread-safe via mutex.
type WorkaroundBudget struct {
	mu     sync.Mutex
	spent  int
	config WorkaroundBudgetConfig
}

// NewWorkaroundBudget creates a new WorkaroundBudget with the given configuration.
func NewWorkaroundBudget(config WorkaroundBudgetConfig) *WorkaroundBudget {
	return &WorkaroundBudget{
		spent:  0,
		config: config,
	}
}

// CanSpend checks if the specified tokens can be spent for the given tier.
func (wb *WorkaroundBudget) CanSpend(tokens int, tier ErrorTier) bool {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	remaining := wb.remainingForTier(tier)
	return tokens <= remaining
}

// remainingForTier calculates remaining budget for a tier (must hold lock).
func (wb *WorkaroundBudget) remainingForTier(tier ErrorTier) int {
	maxBudget := wb.maxBudgetForTier(tier)
	remaining := maxBudget - wb.spent
	if remaining < 0 {
		return 0
	}
	return remaining
}

// maxBudgetForTier returns the max budget for a specific tier.
func (wb *WorkaroundBudget) maxBudgetForTier(tier ErrorTier) int {
	if wb.config.PerTypeBudgets != nil {
		if budget, ok := wb.config.PerTypeBudgets[tier]; ok {
			return budget
		}
	}
	return wb.config.MaxTokens
}

// Spend deducts the specified tokens from the budget.
func (wb *WorkaroundBudget) Spend(tokens int) {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	wb.spent += tokens
}

// Reset resets the spent budget to zero.
func (wb *WorkaroundBudget) Reset() {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	wb.spent = 0
}

// Spent returns the current amount of tokens spent.
func (wb *WorkaroundBudget) Spent() int {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	return wb.spent
}

// Remaining returns the remaining budget for the given tier.
func (wb *WorkaroundBudget) Remaining(tier ErrorTier) int {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	return wb.remainingForTier(tier)
}

// ShouldResetOnSuccess returns whether the budget should reset on success.
func (wb *WorkaroundBudget) ShouldResetOnSuccess() bool {
	return wb.config.ResetOnSuccess
}
