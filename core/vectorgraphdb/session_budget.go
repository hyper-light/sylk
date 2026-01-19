// Package vectorgraphdb provides AR.11.4: SessionScopedView with Budget -
// extends SessionScopedView with retrieval budget tracking and enforcement.
package vectorgraphdb

import (
	"errors"
	"sync"
	"sync/atomic"
)

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrBudgetExhausted indicates the retrieval budget has been exhausted.
	ErrBudgetExhausted = errors.New("retrieval budget exhausted")

	// ErrQueryLimitReached indicates the per-turn query limit has been reached.
	ErrQueryLimitReached = errors.New("query limit reached for this turn")

	// ErrTokenLimitReached indicates the total token limit has been reached.
	ErrTokenLimitReached = errors.New("token limit reached for this session")
)

// =============================================================================
// Retrieval Budget
// =============================================================================

// RetrievalBudget tracks and enforces retrieval resource limits.
type RetrievalBudget struct {
	MaxTokensPerQuery int
	MaxQueriesPerTurn int
	MaxTotalTokens    int

	usedTokens  atomic.Int64
	usedQueries atomic.Int64
	mu          sync.RWMutex
}

// NewRetrievalBudget creates a new budget with the given limits.
func NewRetrievalBudget(maxTokensPerQuery, maxQueriesPerTurn, maxTotalTokens int) *RetrievalBudget {
	return &RetrievalBudget{
		MaxTokensPerQuery: maxTokensPerQuery,
		MaxQueriesPerTurn: maxQueriesPerTurn,
		MaxTotalTokens:    maxTotalTokens,
	}
}

// DefaultRetrievalBudget creates a budget with sensible defaults.
func DefaultRetrievalBudget() *RetrievalBudget {
	return NewRetrievalBudget(
		2000,  // MaxTokensPerQuery
		10,    // MaxQueriesPerTurn
		50000, // MaxTotalTokens
	)
}

// CanQuery returns true if both token and query budgets allow another query.
func (b *RetrievalBudget) CanQuery() bool {
	tokensOK := b.usedTokens.Load() < int64(b.MaxTotalTokens)
	queriesOK := b.usedQueries.Load() < int64(b.MaxQueriesPerTurn)
	return tokensOK && queriesOK
}

// RecordUsage atomically records token usage and increments the query count.
func (b *RetrievalBudget) RecordUsage(tokens int) {
	b.usedTokens.Add(int64(tokens))
	b.usedQueries.Add(1)
}

// ResetTurn resets the per-turn query counter.
func (b *RetrievalBudget) ResetTurn() {
	b.usedQueries.Store(0)
}

// Reset resets all usage counters.
func (b *RetrievalBudget) Reset() {
	b.usedTokens.Store(0)
	b.usedQueries.Store(0)
}

// UsedTokens returns the current token usage.
func (b *RetrievalBudget) UsedTokens() int64 {
	return b.usedTokens.Load()
}

// UsedQueries returns the current query count for this turn.
func (b *RetrievalBudget) UsedQueries() int64 {
	return b.usedQueries.Load()
}

// RemainingTokens returns the remaining token budget.
func (b *RetrievalBudget) RemainingTokens() int64 {
	remaining := int64(b.MaxTotalTokens) - b.usedTokens.Load()
	if remaining < 0 {
		return 0
	}
	return remaining
}

// RemainingQueries returns the remaining query budget for this turn.
func (b *RetrievalBudget) RemainingQueries() int64 {
	remaining := int64(b.MaxQueriesPerTurn) - b.usedQueries.Load()
	if remaining < 0 {
		return 0
	}
	return remaining
}

// =============================================================================
// Budgeted Session View
// =============================================================================

// BudgetedSessionView wraps SessionScopedView with budget enforcement.
type BudgetedSessionView struct {
	view   *SessionScopedView
	budget *RetrievalBudget
	mu     sync.RWMutex
	closed bool
}

// NewBudgetedSessionView creates a new budgeted view wrapping an existing view.
func NewBudgetedSessionView(view *SessionScopedView, budget *RetrievalBudget) *BudgetedSessionView {
	if budget == nil {
		budget = DefaultRetrievalBudget()
	}
	return &BudgetedSessionView{
		view:   view,
		budget: budget,
	}
}

// CanQuery checks if the budget allows another query.
func (bv *BudgetedSessionView) CanQuery() bool {
	bv.mu.RLock()
	defer bv.mu.RUnlock()

	if bv.closed {
		return false
	}
	return bv.budget.CanQuery()
}

// Budget returns the underlying budget for inspection.
func (bv *BudgetedSessionView) Budget() *RetrievalBudget {
	return bv.budget
}

// View returns the underlying session view.
func (bv *BudgetedSessionView) View() *SessionScopedView {
	return bv.view
}

// SessionID returns the session ID of the underlying view.
func (bv *BudgetedSessionView) SessionID() string {
	return bv.view.SessionID()
}

// RecordUsage records token usage against the budget.
func (bv *BudgetedSessionView) RecordUsage(tokens int) {
	bv.budget.RecordUsage(tokens)
}

// ResetTurn resets the per-turn query counter.
func (bv *BudgetedSessionView) ResetTurn() {
	bv.budget.ResetTurn()
}

// Close closes the budgeted view.
func (bv *BudgetedSessionView) Close() error {
	bv.mu.Lock()
	defer bv.mu.Unlock()

	if bv.closed {
		return nil
	}

	bv.closed = true
	return bv.view.Close()
}

// IsClosed returns true if the view is closed.
func (bv *BudgetedSessionView) IsClosed() bool {
	bv.mu.RLock()
	defer bv.mu.RUnlock()
	return bv.closed
}

// =============================================================================
// Budgeted Query Methods
// =============================================================================

// QueryNodes performs a node query with budget checking.
// Returns ErrBudgetExhausted if budget is exhausted.
func (bv *BudgetedSessionView) QueryNodes(
	filter NodeFilter,
) ([]*GraphNode, error) {
	bv.mu.RLock()
	defer bv.mu.RUnlock()

	if bv.closed {
		return nil, ErrViewClosed
	}

	if !bv.budget.CanQuery() {
		return nil, bv.budgetError()
	}

	results, err := bv.view.QueryNodes(filter)
	if err != nil {
		return nil, err
	}

	// Estimate tokens based on result count
	estimatedTokens := estimateTokensFromNodes(results)
	bv.budget.RecordUsage(estimatedTokens)

	return results, nil
}

// budgetError returns the appropriate error based on budget state.
func (bv *BudgetedSessionView) budgetError() error {
	if bv.budget.usedQueries.Load() >= int64(bv.budget.MaxQueriesPerTurn) {
		return ErrQueryLimitReached
	}
	if bv.budget.usedTokens.Load() >= int64(bv.budget.MaxTotalTokens) {
		return ErrTokenLimitReached
	}
	return ErrBudgetExhausted
}

// estimateTokensFromNodes estimates token count from graph nodes.
func estimateTokensFromNodes(nodes []*GraphNode) int {
	if len(nodes) == 0 {
		return 0
	}

	// Rough estimate: 50 tokens per node
	const tokensPerNode = 50
	return len(nodes) * tokensPerNode
}

// =============================================================================
// Budget Status
// =============================================================================

// BudgetStatus represents the current budget state.
type BudgetStatus struct {
	UsedTokens        int64 `json:"used_tokens"`
	RemainingTokens   int64 `json:"remaining_tokens"`
	UsedQueries       int64 `json:"used_queries"`
	RemainingQueries  int64 `json:"remaining_queries"`
	MaxTokensPerQuery int   `json:"max_tokens_per_query"`
	MaxQueriesPerTurn int   `json:"max_queries_per_turn"`
	MaxTotalTokens    int   `json:"max_total_tokens"`
	CanQuery          bool  `json:"can_query"`
}

// Status returns the current budget status.
func (bv *BudgetedSessionView) Status() BudgetStatus {
	return BudgetStatus{
		UsedTokens:        bv.budget.UsedTokens(),
		RemainingTokens:   bv.budget.RemainingTokens(),
		UsedQueries:       bv.budget.UsedQueries(),
		RemainingQueries:  bv.budget.RemainingQueries(),
		MaxTokensPerQuery: bv.budget.MaxTokensPerQuery,
		MaxQueriesPerTurn: bv.budget.MaxQueriesPerTurn,
		MaxTotalTokens:    bv.budget.MaxTotalTokens,
		CanQuery:          bv.CanQuery(),
	}
}
