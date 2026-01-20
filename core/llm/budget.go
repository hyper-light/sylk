package llm

import (
	"errors"
	"sync"
)

// Budget limit errors
var (
	ErrGlobalBudgetExceeded   = errors.New("global token budget exceeded")
	ErrSessionBudgetExceeded  = errors.New("session token budget exceeded")
	ErrTaskBudgetExceeded     = errors.New("task token budget exceeded")
	ErrProviderBudgetExceeded = errors.New("provider token budget exceeded")
)

// UnlimitedBudget indicates no limit is set for a budget category.
const UnlimitedBudget int64 = -1

// TokenBudget enforces hierarchical token limits.
type TokenBudget struct {
	mu             sync.RWMutex
	globalLimit    int64
	sessionLimits  map[string]int64
	taskLimits     map[string]int64
	providerLimits map[string]int64
	tracker        *UsageTracker
}

// NewTokenBudget creates a new TokenBudget with no limits set.
func NewTokenBudget(tracker *UsageTracker) *TokenBudget {
	return &TokenBudget{
		globalLimit:    UnlimitedBudget,
		sessionLimits:  make(map[string]int64),
		taskLimits:     make(map[string]int64),
		providerLimits: make(map[string]int64),
		tracker:        tracker,
	}
}

// SetGlobalLimit sets the global token limit (-1 = unlimited).
func (b *TokenBudget) SetGlobalLimit(limit int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.globalLimit = limit
}

// SetSessionLimit sets the token limit for a session (-1 = unlimited).
func (b *TokenBudget) SetSessionLimit(sessionID string, limit int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sessionLimits[sessionID] = limit
}

// SetTaskLimit sets the token limit for a task (-1 = unlimited).
func (b *TokenBudget) SetTaskLimit(taskID string, limit int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.taskLimits[taskID] = limit
}

// SetProviderLimit sets the token limit for a provider (-1 = unlimited).
func (b *TokenBudget) SetProviderLimit(provider string, limit int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.providerLimits[provider] = limit
}

// CheckBudget verifies all applicable limits for a request.
func (b *TokenBudget) CheckBudget(req *LLMRequest) error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.checkAllLimits(req)
}

// checkAllLimits checks all budget limits in order: global, session, task, provider.
// Caller must hold at least RLock.
func (b *TokenBudget) checkAllLimits(req *LLMRequest) error {
	if err := b.checkGlobalLimit(req); err != nil {
		return err
	}
	if err := b.checkSessionLimit(req); err != nil {
		return err
	}
	if err := b.checkTaskLimit(req); err != nil {
		return err
	}
	return b.checkProviderLimit(req)
}

// CheckAndReserve atomically checks if the budget allows the request and reserves the tokens.
// This prevents race conditions between checking and recording usage.
// Returns nil if the request is allowed and tokens have been reserved.
func (b *TokenBudget) CheckAndReserve(req *LLMRequest, record UsageRecord) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Lock tracker to make check-and-update atomic
	b.tracker.Lock()
	defer b.tracker.Unlock()

	if err := b.checkAllLimitsLocked(req); err != nil {
		return err
	}

	b.tracker.recordLocked(record)
	return nil
}

// checkGlobalLimit checks if the global budget would be exceeded.
func (b *TokenBudget) checkGlobalLimit(req *LLMRequest) error {
	if b.globalLimit == UnlimitedBudget {
		return nil
	}
	current := b.tracker.TotalTokens()
	if current+int64(req.TokenEstimate) > b.globalLimit {
		return ErrGlobalBudgetExceeded
	}
	return nil
}

// checkGlobalLimitLocked checks global limit without acquiring tracker lock.
func (b *TokenBudget) checkGlobalLimitLocked(req *LLMRequest) error {
	if b.globalLimit == UnlimitedBudget {
		return nil
	}
	current := b.tracker.totalTokensLocked()
	if current+int64(req.TokenEstimate) > b.globalLimit {
		return ErrGlobalBudgetExceeded
	}
	return nil
}

// checkSessionLimit checks if the session budget would be exceeded.
func (b *TokenBudget) checkSessionLimit(req *LLMRequest) error {
	limit, exists := b.sessionLimits[req.SessionID]
	if !exists || limit == UnlimitedBudget {
		return nil
	}
	current := b.tracker.TokensBySession(req.SessionID)
	if current+int64(req.TokenEstimate) > limit {
		return ErrSessionBudgetExceeded
	}
	return nil
}

// checkSessionLimitLocked checks session limit without acquiring tracker lock.
func (b *TokenBudget) checkSessionLimitLocked(req *LLMRequest) error {
	limit, exists := b.sessionLimits[req.SessionID]
	if !exists || limit == UnlimitedBudget {
		return nil
	}
	current := b.tracker.tokensBySessionLocked(req.SessionID)
	if current+int64(req.TokenEstimate) > limit {
		return ErrSessionBudgetExceeded
	}
	return nil
}

// checkTaskLimit checks if the task budget would be exceeded.
func (b *TokenBudget) checkTaskLimit(req *LLMRequest) error {
	limit, exists := b.taskLimits[req.TaskID]
	if !exists || limit == UnlimitedBudget {
		return nil
	}
	current := b.tracker.TokensByTask(req.TaskID)
	if current+int64(req.TokenEstimate) > limit {
		return ErrTaskBudgetExceeded
	}
	return nil
}

// checkTaskLimitLocked checks task limit without acquiring tracker lock.
func (b *TokenBudget) checkTaskLimitLocked(req *LLMRequest) error {
	limit, exists := b.taskLimits[req.TaskID]
	if !exists || limit == UnlimitedBudget {
		return nil
	}
	current := b.tracker.tokensByTaskLocked(req.TaskID)
	if current+int64(req.TokenEstimate) > limit {
		return ErrTaskBudgetExceeded
	}
	return nil
}

// checkProviderLimit checks if the provider budget would be exceeded.
func (b *TokenBudget) checkProviderLimit(req *LLMRequest) error {
	limit, exists := b.providerLimits[req.Provider]
	if !exists || limit == UnlimitedBudget {
		return nil
	}
	current := b.tracker.TokensByProvider(req.Provider)
	if current+int64(req.TokenEstimate) > limit {
		return ErrProviderBudgetExceeded
	}
	return nil
}

// checkProviderLimitLocked checks provider limit without acquiring tracker lock.
func (b *TokenBudget) checkProviderLimitLocked(req *LLMRequest) error {
	limit, exists := b.providerLimits[req.Provider]
	if !exists || limit == UnlimitedBudget {
		return nil
	}
	current := b.tracker.tokensByProviderLocked(req.Provider)
	if current+int64(req.TokenEstimate) > limit {
		return ErrProviderBudgetExceeded
	}
	return nil
}

// checkAllLimitsLocked checks all limits using locked tracker methods.
// Caller must hold both budget lock and tracker lock.
func (b *TokenBudget) checkAllLimitsLocked(req *LLMRequest) error {
	if err := b.checkGlobalLimitLocked(req); err != nil {
		return err
	}
	if err := b.checkSessionLimitLocked(req); err != nil {
		return err
	}
	if err := b.checkTaskLimitLocked(req); err != nil {
		return err
	}
	return b.checkProviderLimitLocked(req)
}

// RecordUsage records a usage record and updates the tracker.
func (b *TokenBudget) RecordUsage(record UsageRecord) {
	b.tracker.Record(record)
}
