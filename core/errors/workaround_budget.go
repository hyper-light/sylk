package errors

import (
	"sync/atomic"
)

type WorkaroundBudgetConfig struct {
	MaxTokens      int               `yaml:"max_tokens"`
	ResetOnSuccess bool              `yaml:"reset_on_success"`
	PerTypeBudgets map[ErrorTier]int `yaml:"per_type_budgets"`
}

func DefaultWorkaroundBudgetConfig() WorkaroundBudgetConfig {
	return WorkaroundBudgetConfig{
		MaxTokens:      1000,
		ResetOnSuccess: true,
		PerTypeBudgets: nil,
	}
}

type WorkaroundBudget struct {
	spent  int64
	config WorkaroundBudgetConfig
}

func NewWorkaroundBudget(config WorkaroundBudgetConfig) *WorkaroundBudget {
	return &WorkaroundBudget{
		spent:  0,
		config: config,
	}
}

func (wb *WorkaroundBudget) CanSpend(tokens int, tier ErrorTier) bool {
	current := atomic.LoadInt64(&wb.spent)
	maxBudget := int64(wb.maxBudgetForTier(tier))
	return int64(tokens) <= maxBudget-current
}

func (wb *WorkaroundBudget) maxBudgetForTier(tier ErrorTier) int {
	if wb.config.PerTypeBudgets != nil {
		if budget, ok := wb.config.PerTypeBudgets[tier]; ok {
			return budget
		}
	}
	return wb.config.MaxTokens
}

func (wb *WorkaroundBudget) Spend(tokens int) {
	atomic.AddInt64(&wb.spent, int64(tokens))
}

func (wb *WorkaroundBudget) Reset() {
	atomic.StoreInt64(&wb.spent, 0)
}

func (wb *WorkaroundBudget) Spent() int {
	return int(atomic.LoadInt64(&wb.spent))
}

func (wb *WorkaroundBudget) Remaining(tier ErrorTier) int {
	current := atomic.LoadInt64(&wb.spent)
	maxBudget := int64(wb.maxBudgetForTier(tier))
	remaining := maxBudget - current
	if remaining < 0 {
		return 0
	}
	return int(remaining)
}

func (wb *WorkaroundBudget) ShouldResetOnSuccess() bool {
	return wb.config.ResetOnSuccess
}
