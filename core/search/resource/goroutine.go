// Package resource provides resource budget integration for the search system.
// It wraps core/concurrency/GoroutineBudget and core/resources/FileHandleBudget
// with search-specific semantics and graceful degradation support.
package resource

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
)

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrBudgetExhausted is returned when no goroutines are available.
	ErrBudgetExhausted = errors.New("goroutine budget exhausted")
	// ErrBudgetClosed is returned when the budget has been closed.
	ErrBudgetClosed = errors.New("goroutine budget closed")
)

// =============================================================================
// GoroutineBudget Interface
// =============================================================================

// GoroutineBudget defines the interface for the underlying goroutine budget system.
// This matches the core/concurrency/GoroutineBudget API.
type GoroutineBudget interface {
	// Acquire requests a goroutine slot, blocking if at hard limit.
	Acquire(agentID string) error
	// AcquireWithContext attempts to acquire with context cancellation support.
	AcquireWithContext(ctx context.Context, agentID string) error
	// Release returns a goroutine slot to the budget.
	Release(agentID string)
	// RegisterAgent registers an agent with the budget system.
	RegisterAgent(agentID, agentType string)
	// UnregisterAgent removes an agent from the budget system.
	UnregisterAgent(agentID string)
	// GetStats returns statistics for an agent.
	GetStats(agentID string) (active, peak, softLimit, hardLimit int64, ok bool)
	// TotalActive returns total active goroutines across all agents.
	TotalActive() int64
}

// =============================================================================
// SearchGoroutineBudget Configuration
// =============================================================================

// SearchGoroutineBudgetConfig configures the search goroutine budget.
type SearchGoroutineBudgetConfig struct {
	// Budget is the underlying goroutine budget (required).
	Budget GoroutineBudget
	// AgentID identifies this search component for budget tracking.
	AgentID string
	// AgentType determines the weight for budget allocation.
	AgentType string
	// MaxIndexingGoroutines limits concurrent indexing goroutines.
	MaxIndexingGoroutines int
	// MaxSearchGoroutines limits concurrent search goroutines.
	MaxSearchGoroutines int
	// Logger for budget operations.
	Logger *slog.Logger
	// OnDegraded is called when operating in degraded mode.
	OnDegraded func(reason string)
}

// DefaultSearchGoroutineBudgetConfig returns sensible defaults.
func DefaultSearchGoroutineBudgetConfig() SearchGoroutineBudgetConfig {
	return SearchGoroutineBudgetConfig{
		AgentID:               "search",
		AgentType:             "search",
		MaxIndexingGoroutines: 8,
		MaxSearchGoroutines:   16,
		Logger:                slog.Default(),
	}
}

// =============================================================================
// SearchGoroutineBudget
// =============================================================================

// SearchGoroutineBudget wraps GoroutineBudget with search-specific semantics.
// It provides separate tracking for indexing and search operations.
type SearchGoroutineBudget struct {
	budget    GoroutineBudget
	agentID   string
	agentType string
	logger    *slog.Logger

	maxIndexing int32
	maxSearch   int32

	activeIndexing atomic.Int32
	activeSearch   atomic.Int32

	onDegraded func(reason string)

	mu     sync.RWMutex
	closed bool
}

// NewSearchGoroutineBudget creates a new search goroutine budget wrapper.
func NewSearchGoroutineBudget(cfg SearchGoroutineBudgetConfig) *SearchGoroutineBudget {
	cfg = normalizeGoroutineConfig(cfg)

	sgb := &SearchGoroutineBudget{
		budget:      cfg.Budget,
		agentID:     cfg.AgentID,
		agentType:   cfg.AgentType,
		logger:      cfg.Logger,
		maxIndexing: int32(cfg.MaxIndexingGoroutines),
		maxSearch:   int32(cfg.MaxSearchGoroutines),
		onDegraded:  cfg.OnDegraded,
	}

	if cfg.Budget != nil {
		cfg.Budget.RegisterAgent(cfg.AgentID, cfg.AgentType)
	}

	return sgb
}

func normalizeGoroutineConfig(cfg SearchGoroutineBudgetConfig) SearchGoroutineBudgetConfig {
	if cfg.AgentID == "" {
		cfg.AgentID = "search"
	}
	if cfg.AgentType == "" {
		cfg.AgentType = "search"
	}
	if cfg.MaxIndexingGoroutines <= 0 {
		cfg.MaxIndexingGoroutines = 8
	}
	if cfg.MaxSearchGoroutines <= 0 {
		cfg.MaxSearchGoroutines = 16
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return cfg
}

// =============================================================================
// Indexing Operations
// =============================================================================

// AcquireForIndexing acquires count goroutine slots for indexing operations.
// Returns a release function and any error encountered.
func (s *SearchGoroutineBudget) AcquireForIndexing(ctx context.Context, count int) (func(), error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}

	if count <= 0 {
		return func() {}, nil
	}

	acquired, err := s.acquireIndexingSlots(ctx, count)
	if err != nil {
		s.releasePartialIndexing(acquired)
		return nil, err
	}

	return s.makeIndexingReleaser(acquired), nil
}

func (s *SearchGoroutineBudget) acquireIndexingSlots(ctx context.Context, count int) (int, error) {
	acquired := 0

	for i := 0; i < count; i++ {
		if err := s.acquireSingleIndexingSlot(ctx); err != nil {
			return acquired, err
		}
		acquired++
	}

	return acquired, nil
}

func (s *SearchGoroutineBudget) acquireSingleIndexingSlot(ctx context.Context) error {
	if !s.canAcquireIndexing() {
		s.notifyDegraded("indexing limit reached")
		return ErrBudgetExhausted
	}

	if s.budget == nil {
		s.activeIndexing.Add(1)
		return nil
	}

	if err := s.budget.AcquireWithContext(ctx, s.agentID); err != nil {
		s.notifyDegraded("budget exhausted")
		return err
	}

	s.activeIndexing.Add(1)
	return nil
}

func (s *SearchGoroutineBudget) canAcquireIndexing() bool {
	return s.activeIndexing.Load() < s.maxIndexing
}

func (s *SearchGoroutineBudget) releasePartialIndexing(count int) {
	for i := 0; i < count; i++ {
		s.releaseSingleIndexing()
	}
}

func (s *SearchGoroutineBudget) releaseSingleIndexing() {
	s.activeIndexing.Add(-1)
	if s.budget != nil {
		s.budget.Release(s.agentID)
	}
}

func (s *SearchGoroutineBudget) makeIndexingReleaser(count int) func() {
	released := &atomic.Bool{}
	return func() {
		if released.CompareAndSwap(false, true) {
			s.releasePartialIndexing(count)
		}
	}
}

// =============================================================================
// Search Operations
// =============================================================================

// AcquireForSearch acquires a single goroutine slot for a search operation.
// Returns a release function and any error encountered.
func (s *SearchGoroutineBudget) AcquireForSearch(ctx context.Context) (func(), error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}

	if err := s.acquireSingleSearchSlot(ctx); err != nil {
		return nil, err
	}

	return s.makeSearchReleaser(), nil
}

func (s *SearchGoroutineBudget) acquireSingleSearchSlot(ctx context.Context) error {
	if !s.canAcquireSearch() {
		s.notifyDegraded("search limit reached")
		return ErrBudgetExhausted
	}

	if s.budget == nil {
		s.activeSearch.Add(1)
		return nil
	}

	if err := s.budget.AcquireWithContext(ctx, s.agentID); err != nil {
		s.notifyDegraded("budget exhausted")
		return err
	}

	s.activeSearch.Add(1)
	return nil
}

func (s *SearchGoroutineBudget) canAcquireSearch() bool {
	return s.activeSearch.Load() < s.maxSearch
}

func (s *SearchGoroutineBudget) makeSearchReleaser() func() {
	released := &atomic.Bool{}
	return func() {
		if released.CompareAndSwap(false, true) {
			s.releaseSingleSearch()
		}
	}
}

func (s *SearchGoroutineBudget) releaseSingleSearch() {
	s.activeSearch.Add(-1)
	if s.budget != nil {
		s.budget.Release(s.agentID)
	}
}

// TryAcquireForSearch attempts non-blocking acquisition for search.
// Returns a release function and true if successful, nil and false otherwise.
func (s *SearchGoroutineBudget) TryAcquireForSearch() (func(), bool) {
	if s.checkClosed() != nil {
		return nil, false
	}

	if !s.canAcquireSearch() {
		return nil, false
	}

	s.activeSearch.Add(1)
	if s.budget != nil {
		if err := s.budget.Acquire(s.agentID); err != nil {
			s.activeSearch.Add(-1)
			return nil, false
		}
	}

	return s.makeSearchReleaser(), true
}

// =============================================================================
// Statistics
// =============================================================================

// Stats returns current statistics for the search budget.
type Stats struct {
	ActiveIndexing int32
	ActiveSearch   int32
	MaxIndexing    int32
	MaxSearch      int32
}

// GetStats returns current statistics.
func (s *SearchGoroutineBudget) GetStats() Stats {
	return Stats{
		ActiveIndexing: s.activeIndexing.Load(),
		ActiveSearch:   s.activeSearch.Load(),
		MaxIndexing:    s.maxIndexing,
		MaxSearch:      s.maxSearch,
	}
}

// ActiveIndexing returns the count of active indexing goroutines.
func (s *SearchGoroutineBudget) ActiveIndexing() int32 {
	return s.activeIndexing.Load()
}

// ActiveSearch returns the count of active search goroutines.
func (s *SearchGoroutineBudget) ActiveSearch() int32 {
	return s.activeSearch.Load()
}

// =============================================================================
// Lifecycle
// =============================================================================

func (s *SearchGoroutineBudget) checkClosed() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return ErrBudgetClosed
	}
	return nil
}

// Close unregisters the agent and releases resources.
func (s *SearchGoroutineBudget) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	s.closed = true

	if s.budget != nil {
		s.budget.UnregisterAgent(s.agentID)
	}

	return nil
}

// =============================================================================
// Degradation
// =============================================================================

func (s *SearchGoroutineBudget) notifyDegraded(reason string) {
	s.logger.Warn("search operating in degraded mode",
		"reason", reason,
		"active_indexing", s.activeIndexing.Load(),
		"active_search", s.activeSearch.Load(),
	)

	if s.onDegraded != nil {
		s.onDegraded(reason)
	}
}

// =============================================================================
// Graceful Degradation Helpers
// =============================================================================

// AcquireForIndexingDegraded acquires goroutines with graceful degradation.
// If the full count cannot be acquired, it acquires what's available.
// Returns the actual count acquired and a release function.
func (s *SearchGoroutineBudget) AcquireForIndexingDegraded(ctx context.Context, desired int) (int, func(), error) {
	if err := s.checkClosed(); err != nil {
		return 0, nil, err
	}

	if desired <= 0 {
		return 0, func() {}, nil
	}

	available := s.availableIndexingSlots()
	toAcquire := min(desired, available)

	if toAcquire == 0 {
		return 0, func() {}, nil
	}

	acquired, err := s.acquireIndexingSlots(ctx, toAcquire)
	if err != nil && acquired == 0 {
		return 0, nil, err
	}

	if acquired < desired {
		s.notifyDegraded("reduced indexing concurrency")
	}

	return acquired, s.makeIndexingReleaser(acquired), nil
}

func (s *SearchGoroutineBudget) availableIndexingSlots() int {
	current := s.activeIndexing.Load()
	available := s.maxIndexing - current
	if available < 0 {
		return 0
	}
	return int(available)
}
