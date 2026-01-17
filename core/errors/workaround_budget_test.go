package errors

import (
	"sync"
	"testing"
)

func TestWorkaroundBudget_CanSpend_WithinBudget(t *testing.T) {
	tests := []struct {
		name      string
		maxTokens int
		tokens    int
		tier      ErrorTier
		want      bool
	}{
		{
			name:      "exact budget",
			maxTokens: 1000,
			tokens:    1000,
			tier:      TierTransient,
			want:      true,
		},
		{
			name:      "under budget",
			maxTokens: 1000,
			tokens:    500,
			tier:      TierTransient,
			want:      true,
		},
		{
			name:      "zero tokens",
			maxTokens: 1000,
			tokens:    0,
			tier:      TierTransient,
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := WorkaroundBudgetConfig{
				MaxTokens:      tt.maxTokens,
				ResetOnSuccess: true,
			}
			budget := NewWorkaroundBudget(config)

			got := budget.CanSpend(tt.tokens, tt.tier)
			if got != tt.want {
				t.Errorf("CanSpend(%d, %v) = %v, want %v", tt.tokens, tt.tier, got, tt.want)
			}
		})
	}
}

func TestWorkaroundBudget_CanSpend_ExceedsBudget(t *testing.T) {
	tests := []struct {
		name      string
		maxTokens int
		preSpent  int
		tokens    int
		tier      ErrorTier
		want      bool
	}{
		{
			name:      "exceeds max budget",
			maxTokens: 1000,
			preSpent:  0,
			tokens:    1001,
			tier:      TierTransient,
			want:      false,
		},
		{
			name:      "exceeds after partial spend",
			maxTokens: 1000,
			preSpent:  600,
			tokens:    500,
			tier:      TierTransient,
			want:      false,
		},
		{
			name:      "budget exhausted",
			maxTokens: 1000,
			preSpent:  1000,
			tokens:    1,
			tier:      TierTransient,
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := WorkaroundBudgetConfig{
				MaxTokens:      tt.maxTokens,
				ResetOnSuccess: true,
			}
			budget := NewWorkaroundBudget(config)
			if tt.preSpent > 0 {
				budget.Spend(tt.preSpent)
			}

			got := budget.CanSpend(tt.tokens, tt.tier)
			if got != tt.want {
				t.Errorf("CanSpend(%d, %v) = %v, want %v", tt.tokens, tt.tier, got, tt.want)
			}
		})
	}
}

func TestWorkaroundBudget_Spend_DeductTokens(t *testing.T) {
	tests := []struct {
		name          string
		maxTokens     int
		spendSequence []int
		wantSpent     int
	}{
		{
			name:          "single spend",
			maxTokens:     1000,
			spendSequence: []int{100},
			wantSpent:     100,
		},
		{
			name:          "multiple spends",
			maxTokens:     1000,
			spendSequence: []int{100, 200, 300},
			wantSpent:     600,
		},
		{
			name:          "spend to exact budget",
			maxTokens:     1000,
			spendSequence: []int{500, 500},
			wantSpent:     1000,
		},
		{
			name:          "overspend still tracked",
			maxTokens:     1000,
			spendSequence: []int{800, 400},
			wantSpent:     1200,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := WorkaroundBudgetConfig{
				MaxTokens:      tt.maxTokens,
				ResetOnSuccess: true,
			}
			budget := NewWorkaroundBudget(config)

			for _, tokens := range tt.spendSequence {
				budget.Spend(tokens)
			}

			got := budget.Spent()
			if got != tt.wantSpent {
				t.Errorf("Spent() = %d, want %d", got, tt.wantSpent)
			}
		})
	}
}

func TestWorkaroundBudget_Reset_ClearsSpent(t *testing.T) {
	config := WorkaroundBudgetConfig{
		MaxTokens:      1000,
		ResetOnSuccess: true,
	}
	budget := NewWorkaroundBudget(config)

	budget.Spend(500)
	if budget.Spent() != 500 {
		t.Fatalf("Spent() = %d, want 500", budget.Spent())
	}

	budget.Reset()

	if budget.Spent() != 0 {
		t.Errorf("After Reset(), Spent() = %d, want 0", budget.Spent())
	}

	if !budget.CanSpend(1000, TierTransient) {
		t.Error("After Reset(), CanSpend(1000) should be true")
	}
}

func TestWorkaroundBudget_PerTypeBudgets(t *testing.T) {
	config := WorkaroundBudgetConfig{
		MaxTokens:      1000,
		ResetOnSuccess: true,
		PerTypeBudgets: map[ErrorTier]int{
			TierTransient:         500,
			TierExternalRateLimit: 200,
		},
	}

	tests := []struct {
		name   string
		tokens int
		tier   ErrorTier
		want   bool
	}{
		{
			name:   "transient within tier budget",
			tokens: 400,
			tier:   TierTransient,
			want:   true,
		},
		{
			name:   "transient exceeds tier budget",
			tokens: 600,
			tier:   TierTransient,
			want:   false,
		},
		{
			name:   "rate limit within tier budget",
			tokens: 150,
			tier:   TierExternalRateLimit,
			want:   true,
		},
		{
			name:   "rate limit exceeds tier budget",
			tokens: 250,
			tier:   TierExternalRateLimit,
			want:   false,
		},
		{
			name:   "permanent uses default budget",
			tokens: 800,
			tier:   TierPermanent,
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			freshBudget := NewWorkaroundBudget(config)
			got := freshBudget.CanSpend(tt.tokens, tt.tier)
			if got != tt.want {
				t.Errorf("CanSpend(%d, %v) = %v, want %v", tt.tokens, tt.tier, got, tt.want)
			}
		})
	}
}

func TestWorkaroundBudget_ConcurrentSpend(t *testing.T) {
	config := WorkaroundBudgetConfig{
		MaxTokens:      10000,
		ResetOnSuccess: true,
	}
	budget := NewWorkaroundBudget(config)

	const numGoroutines = 100
	const tokensPerGoroutine = 10

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			budget.Spend(tokensPerGoroutine)
		}()
	}

	wg.Wait()

	expectedSpent := numGoroutines * tokensPerGoroutine
	if budget.Spent() != expectedSpent {
		t.Errorf("After concurrent spending, Spent() = %d, want %d", budget.Spent(), expectedSpent)
	}
}

func TestWorkaroundBudget_Remaining(t *testing.T) {
	config := WorkaroundBudgetConfig{
		MaxTokens:      1000,
		ResetOnSuccess: true,
		PerTypeBudgets: map[ErrorTier]int{
			TierTransient: 500,
		},
	}
	budget := NewWorkaroundBudget(config)

	if got := budget.Remaining(TierPermanent); got != 1000 {
		t.Errorf("Remaining(TierPermanent) = %d, want 1000", got)
	}
	if got := budget.Remaining(TierTransient); got != 500 {
		t.Errorf("Remaining(TierTransient) = %d, want 500", got)
	}

	budget.Spend(200)
	if got := budget.Remaining(TierPermanent); got != 800 {
		t.Errorf("After Spend(200), Remaining(TierPermanent) = %d, want 800", got)
	}
	if got := budget.Remaining(TierTransient); got != 300 {
		t.Errorf("After Spend(200), Remaining(TierTransient) = %d, want 300", got)
	}
}

func TestWorkaroundBudget_ShouldResetOnSuccess(t *testing.T) {
	tests := []struct {
		name           string
		resetOnSuccess bool
		want           bool
	}{
		{
			name:           "reset enabled",
			resetOnSuccess: true,
			want:           true,
		},
		{
			name:           "reset disabled",
			resetOnSuccess: false,
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := WorkaroundBudgetConfig{
				MaxTokens:      1000,
				ResetOnSuccess: tt.resetOnSuccess,
			}
			budget := NewWorkaroundBudget(config)

			if got := budget.ShouldResetOnSuccess(); got != tt.want {
				t.Errorf("ShouldResetOnSuccess() = %v, want %v", got, tt.want)
			}
		})
	}
}
