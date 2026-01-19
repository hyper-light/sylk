package vectorgraphdb

import (
	"testing"
)

func TestNewRetrievalBudget(t *testing.T) {
	t.Parallel()

	budget := NewRetrievalBudget(1000, 5, 20000)

	if budget.MaxTokensPerQuery != 1000 {
		t.Errorf("MaxTokensPerQuery = %d, want 1000", budget.MaxTokensPerQuery)
	}
	if budget.MaxQueriesPerTurn != 5 {
		t.Errorf("MaxQueriesPerTurn = %d, want 5", budget.MaxQueriesPerTurn)
	}
	if budget.MaxTotalTokens != 20000 {
		t.Errorf("MaxTotalTokens = %d, want 20000", budget.MaxTotalTokens)
	}
}

func TestDefaultRetrievalBudget(t *testing.T) {
	t.Parallel()

	budget := DefaultRetrievalBudget()

	if budget.MaxTokensPerQuery <= 0 {
		t.Error("MaxTokensPerQuery should be positive")
	}
	if budget.MaxQueriesPerTurn <= 0 {
		t.Error("MaxQueriesPerTurn should be positive")
	}
	if budget.MaxTotalTokens <= 0 {
		t.Error("MaxTotalTokens should be positive")
	}
}

func TestRetrievalBudget_CanQuery(t *testing.T) {
	t.Parallel()

	budget := NewRetrievalBudget(100, 3, 500)

	// Initially should be able to query
	if !budget.CanQuery() {
		t.Error("should be able to query initially")
	}

	// Use up queries
	for i := 0; i < 3; i++ {
		budget.RecordUsage(100)
	}

	// Now should not be able to query (query limit)
	if budget.CanQuery() {
		t.Error("should not be able to query after exhausting query limit")
	}
}

func TestRetrievalBudget_TokenLimit(t *testing.T) {
	t.Parallel()

	budget := NewRetrievalBudget(100, 100, 500)

	// Use up tokens
	budget.RecordUsage(600)

	// Should not be able to query (token limit)
	if budget.CanQuery() {
		t.Error("should not be able to query after exhausting token limit")
	}
}

func TestRetrievalBudget_ResetTurn(t *testing.T) {
	t.Parallel()

	budget := NewRetrievalBudget(100, 3, 10000)

	// Use up queries
	for i := 0; i < 3; i++ {
		budget.RecordUsage(100)
	}

	if budget.CanQuery() {
		t.Error("should not be able to query after exhausting query limit")
	}

	// Reset turn
	budget.ResetTurn()

	// Now should be able to query again
	if !budget.CanQuery() {
		t.Error("should be able to query after reset turn")
	}

	// Tokens should still be counted
	if budget.UsedTokens() != 300 {
		t.Errorf("UsedTokens = %d, want 300", budget.UsedTokens())
	}
}

func TestRetrievalBudget_Reset(t *testing.T) {
	t.Parallel()

	budget := NewRetrievalBudget(100, 3, 500)

	budget.RecordUsage(100)
	budget.RecordUsage(100)

	budget.Reset()

	if budget.UsedTokens() != 0 {
		t.Errorf("UsedTokens = %d, want 0", budget.UsedTokens())
	}
	if budget.UsedQueries() != 0 {
		t.Errorf("UsedQueries = %d, want 0", budget.UsedQueries())
	}
}

func TestRetrievalBudget_RemainingTokens(t *testing.T) {
	t.Parallel()

	budget := NewRetrievalBudget(100, 10, 500)

	budget.RecordUsage(200)

	remaining := budget.RemainingTokens()
	if remaining != 300 {
		t.Errorf("RemainingTokens = %d, want 300", remaining)
	}
}

func TestRetrievalBudget_RemainingQueries(t *testing.T) {
	t.Parallel()

	budget := NewRetrievalBudget(100, 5, 5000)

	budget.RecordUsage(100)
	budget.RecordUsage(100)

	remaining := budget.RemainingQueries()
	if remaining != 3 {
		t.Errorf("RemainingQueries = %d, want 3", remaining)
	}
}

func TestRetrievalBudget_RemainingNegative(t *testing.T) {
	t.Parallel()

	budget := NewRetrievalBudget(100, 2, 100)

	// Over budget
	budget.RecordUsage(200)

	if budget.RemainingTokens() < 0 {
		t.Error("RemainingTokens should not be negative")
	}
}

func TestNewBudgetedSessionView(t *testing.T) {
	t.Parallel()

	view := &SessionScopedView{sessionID: "test-session"}
	budget := NewRetrievalBudget(1000, 5, 10000)

	bv := NewBudgetedSessionView(view, budget)

	if bv.SessionID() != "test-session" {
		t.Errorf("SessionID = %s, want test-session", bv.SessionID())
	}
	if bv.View() != view {
		t.Error("View() should return underlying view")
	}
	if bv.Budget() != budget {
		t.Error("Budget() should return underlying budget")
	}
}

func TestNewBudgetedSessionView_NilBudget(t *testing.T) {
	t.Parallel()

	view := &SessionScopedView{sessionID: "test-session"}
	bv := NewBudgetedSessionView(view, nil)

	if bv.Budget() == nil {
		t.Error("should use default budget when nil")
	}
}

func TestBudgetedSessionView_CanQuery(t *testing.T) {
	t.Parallel()

	view := &SessionScopedView{sessionID: "test"}
	budget := NewRetrievalBudget(100, 2, 1000)
	bv := NewBudgetedSessionView(view, budget)

	if !bv.CanQuery() {
		t.Error("should be able to query initially")
	}

	budget.RecordUsage(100)
	budget.RecordUsage(100)

	if bv.CanQuery() {
		t.Error("should not be able to query after exhausting budget")
	}
}

func TestBudgetedSessionView_CanQuery_Closed(t *testing.T) {
	t.Parallel()

	view := &SessionScopedView{sessionID: "test"}
	bv := NewBudgetedSessionView(view, nil)

	bv.closed = true

	if bv.CanQuery() {
		t.Error("should not be able to query when closed")
	}
}

func TestBudgetedSessionView_RecordUsage(t *testing.T) {
	t.Parallel()

	view := &SessionScopedView{sessionID: "test"}
	budget := NewRetrievalBudget(100, 10, 1000)
	bv := NewBudgetedSessionView(view, budget)

	bv.RecordUsage(150)

	if budget.UsedTokens() != 150 {
		t.Errorf("UsedTokens = %d, want 150", budget.UsedTokens())
	}
}

func TestBudgetedSessionView_ResetTurn(t *testing.T) {
	t.Parallel()

	view := &SessionScopedView{sessionID: "test"}
	budget := NewRetrievalBudget(100, 2, 10000)
	bv := NewBudgetedSessionView(view, budget)

	budget.RecordUsage(100)
	budget.RecordUsage(100)

	if bv.CanQuery() {
		t.Error("should not be able to query after exhausting queries")
	}

	bv.ResetTurn()

	if !bv.CanQuery() {
		t.Error("should be able to query after reset turn")
	}
}

func TestBudgetedSessionView_Close(t *testing.T) {
	t.Parallel()

	view := &SessionScopedView{sessionID: "test"}
	bv := NewBudgetedSessionView(view, nil)

	if bv.IsClosed() {
		t.Error("should not be closed initially")
	}

	err := bv.Close()
	if err != nil {
		t.Errorf("Close() error = %v", err)
	}

	if !bv.IsClosed() {
		t.Error("should be closed after Close()")
	}

	// Double close should be safe
	err = bv.Close()
	if err != nil {
		t.Errorf("double Close() error = %v", err)
	}
}

func TestBudgetedSessionView_Status(t *testing.T) {
	t.Parallel()

	view := &SessionScopedView{sessionID: "test"}
	budget := NewRetrievalBudget(100, 5, 1000)
	bv := NewBudgetedSessionView(view, budget)

	budget.RecordUsage(200)
	budget.RecordUsage(100)

	status := bv.Status()

	if status.UsedTokens != 300 {
		t.Errorf("UsedTokens = %d, want 300", status.UsedTokens)
	}
	if status.UsedQueries != 2 {
		t.Errorf("UsedQueries = %d, want 2", status.UsedQueries)
	}
	if status.RemainingTokens != 700 {
		t.Errorf("RemainingTokens = %d, want 700", status.RemainingTokens)
	}
	if status.RemainingQueries != 3 {
		t.Errorf("RemainingQueries = %d, want 3", status.RemainingQueries)
	}
	if !status.CanQuery {
		t.Error("CanQuery should be true")
	}
}

func TestEstimateTokensFromNodes(t *testing.T) {
	t.Parallel()

	// Empty results
	tokens := estimateTokensFromNodes(nil)
	if tokens != 0 {
		t.Errorf("estimateTokensFromNodes(nil) = %d, want 0", tokens)
	}

	// Some results
	nodes := []*GraphNode{{}, {}, {}}
	tokens = estimateTokensFromNodes(nodes)
	if tokens <= 0 {
		t.Error("should return positive tokens for non-empty nodes")
	}
}

func TestBudgetedSessionView_BudgetError(t *testing.T) {
	t.Parallel()

	view := &SessionScopedView{sessionID: "test"}
	budget := NewRetrievalBudget(100, 2, 1000)
	bv := NewBudgetedSessionView(view, budget)

	// Exhaust queries
	budget.RecordUsage(50)
	budget.RecordUsage(50)

	err := bv.budgetError()
	if err != ErrQueryLimitReached {
		t.Errorf("budgetError() = %v, want ErrQueryLimitReached", err)
	}
}

func TestBudgetedSessionView_BudgetError_TokenLimit(t *testing.T) {
	t.Parallel()

	view := &SessionScopedView{sessionID: "test"}
	budget := NewRetrievalBudget(100, 100, 500)
	bv := NewBudgetedSessionView(view, budget)

	// Exhaust tokens
	budget.RecordUsage(600)

	err := bv.budgetError()
	if err != ErrTokenLimitReached {
		t.Errorf("budgetError() = %v, want ErrTokenLimitReached", err)
	}
}
