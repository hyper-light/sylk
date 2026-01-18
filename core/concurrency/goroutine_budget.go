package concurrency

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
)

// PressureLevel represents memory pressure levels for budget adjustment.
type PressureLevel int32

const (
	PressureNormal PressureLevel = iota
	PressureElevated
	PressureHigh
	PressureCritical
)

var (
	ErrAgentNotRegistered = errors.New("agent not registered")
	ErrBudgetExhausted    = errors.New("goroutine budget exhausted")
)

// BudgetCallback is called when budget thresholds are crossed.
type BudgetCallback func(agentID string, active, limit int)

// GoroutineBudget manages dynamic goroutine limits per agent.
type GoroutineBudget struct {
	mu sync.RWMutex

	agents      map[string]*agentBudgetState
	totalActive atomic.Int64
	systemLimit int64

	pressureLevel   *atomic.Int32
	burstMultiplier float64
	typeWeights     map[string]float64

	onWarning BudgetCallback
	onBlocked BudgetCallback
}

// agentBudgetState holds per-agent budget state.
type agentBudgetState struct {
	agentID   string
	agentType string
	active    atomic.Int64
	peak      atomic.Int64
	softLimit int64
	hardLimit int64
	waiters   atomic.Int32
	cond      *sync.Cond
	mu        sync.Mutex
}

// GoroutineBudgetConfig contains configuration for the budget.
type GoroutineBudgetConfig struct {
	SystemLimit     int64
	BurstMultiplier float64
	TypeWeights     map[string]float64
	OnWarning       BudgetCallback
	OnBlocked       BudgetCallback
}

// DefaultGoroutineBudgetConfig returns sensible defaults.
func DefaultGoroutineBudgetConfig() GoroutineBudgetConfig {
	return GoroutineBudgetConfig{
		SystemLimit:     int64(runtime.GOMAXPROCS(0)) * 1000,
		BurstMultiplier: 1.5,
		TypeWeights:     defaultTypeWeights(),
	}
}

func defaultTypeWeights() map[string]float64 {
	return map[string]float64{
		"engineer":    1.0,
		"architect":   0.5,
		"librarian":   0.3,
		"archivalist": 0.3,
		"inspector":   0.5,
		"tester":      0.8,
		"guide":       0.2,
	}
}

// NewGoroutineBudget creates a new goroutine budget manager.
func NewGoroutineBudget(pressureLevel *atomic.Int32) *GoroutineBudget {
	config := DefaultGoroutineBudgetConfig()
	return NewGoroutineBudgetWithConfig(pressureLevel, config)
}

// NewGoroutineBudgetWithConfig creates a budget manager with custom config.
func NewGoroutineBudgetWithConfig(pressureLevel *atomic.Int32, cfg GoroutineBudgetConfig) *GoroutineBudget {
	if cfg.SystemLimit <= 0 {
		cfg.SystemLimit = int64(runtime.GOMAXPROCS(0)) * 1000
	}
	if cfg.BurstMultiplier <= 0 {
		cfg.BurstMultiplier = 1.5
	}
	if cfg.TypeWeights == nil {
		cfg.TypeWeights = defaultTypeWeights()
	}

	return &GoroutineBudget{
		agents:          make(map[string]*agentBudgetState),
		systemLimit:     cfg.SystemLimit,
		pressureLevel:   pressureLevel,
		burstMultiplier: cfg.BurstMultiplier,
		typeWeights:     cfg.TypeWeights,
		onWarning:       cfg.OnWarning,
		onBlocked:       cfg.OnBlocked,
	}
}

// RegisterAgent registers an agent with the budget system.
func (b *GoroutineBudget) RegisterAgent(agentID, agentType string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, exists := b.agents[agentID]; exists {
		return
	}

	state := &agentBudgetState{
		agentID:   agentID,
		agentType: agentType,
	}
	state.cond = sync.NewCond(&state.mu)
	b.agents[agentID] = state
	b.recalculateLimitsLocked()
}

// UnregisterAgent removes an agent from the budget system.
func (b *GoroutineBudget) UnregisterAgent(agentID string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.agents, agentID)
	b.recalculateLimitsLocked()
}

// Acquire requests a goroutine slot, blocking if at hard limit.
func (b *GoroutineBudget) Acquire(agentID string) error {
	state := b.getAgentState(agentID)
	if state == nil {
		return ErrAgentNotRegistered
	}

	return b.acquireSlot(state)
}

func (b *GoroutineBudget) getAgentState(agentID string) *agentBudgetState {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.agents[agentID]
}

func (b *GoroutineBudget) acquireSlot(state *agentBudgetState) error {
	state.cond.L.Lock()
	defer state.cond.L.Unlock()

	b.waitForCapacity(state)
	b.incrementCounters(state)
	b.checkSoftLimit(state)

	return nil
}

func (b *GoroutineBudget) waitForCapacity(state *agentBudgetState) {
	for state.active.Load() >= state.hardLimit {
		b.notifyBlocked(state)
		state.waiters.Add(1)
		state.cond.Wait()
		state.waiters.Add(-1)
	}
}

func (b *GoroutineBudget) incrementCounters(state *agentBudgetState) {
	newActive := state.active.Add(1)
	b.totalActive.Add(1)
	b.updatePeak(state, newActive)
}

func (b *GoroutineBudget) updatePeak(state *agentBudgetState, newActive int64) {
	for {
		peak := state.peak.Load()
		if newActive <= peak || state.peak.CompareAndSwap(peak, newActive) {
			return
		}
	}
}

func (b *GoroutineBudget) notifyBlocked(state *agentBudgetState) {
	if b.onBlocked != nil {
		b.onBlocked(state.agentID, int(state.active.Load()), int(state.hardLimit))
	}
}

func (b *GoroutineBudget) checkSoftLimit(state *agentBudgetState) {
	if state.active.Load() > state.softLimit && b.onWarning != nil {
		b.onWarning(state.agentID, int(state.active.Load()), int(state.softLimit))
	}
}

// AcquireWithContext attempts to acquire with context cancellation support.
func (b *GoroutineBudget) AcquireWithContext(ctx context.Context, agentID string) error {
	state := b.getAgentState(agentID)
	if state == nil {
		return ErrAgentNotRegistered
	}

	return b.acquireWithCtx(ctx, state)
}

func (b *GoroutineBudget) acquireWithCtx(ctx context.Context, state *agentBudgetState) error {
	if state.active.Load() < state.hardLimit {
		return b.tryAcquireNonBlocking(state)
	}
	return b.acquireBlocking(ctx, state)
}

func (b *GoroutineBudget) tryAcquireNonBlocking(state *agentBudgetState) error {
	state.cond.L.Lock()
	defer state.cond.L.Unlock()

	if state.active.Load() >= state.hardLimit {
		return ErrBudgetExhausted
	}

	b.incrementCounters(state)
	b.checkSoftLimit(state)
	return nil
}

func (b *GoroutineBudget) acquireBlocking(ctx context.Context, state *agentBudgetState) error {
	done := make(chan struct{})
	defer close(done)

	go b.watchContextCancel(ctx, state, done)

	state.cond.L.Lock()
	defer state.cond.L.Unlock()

	for state.active.Load() >= state.hardLimit {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		b.notifyBlocked(state)
		state.waiters.Add(1)
		state.cond.Wait()
		state.waiters.Add(-1)
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	b.incrementCounters(state)
	b.checkSoftLimit(state)
	return nil
}

func (b *GoroutineBudget) watchContextCancel(ctx context.Context, state *agentBudgetState, done <-chan struct{}) {
	select {
	case <-ctx.Done():
		state.cond.L.Lock()
		state.cond.Broadcast()
		state.cond.L.Unlock()
	case <-done:
	}
}

// Release returns a goroutine slot to the budget.
func (b *GoroutineBudget) Release(agentID string) {
	state := b.getAgentState(agentID)
	if state == nil {
		return
	}

	b.releaseSlot(state)
}

func (b *GoroutineBudget) releaseSlot(state *agentBudgetState) {
	state.active.Add(-1)
	b.totalActive.Add(-1)

	if state.waiters.Load() > 0 {
		state.cond.L.Lock()
		state.cond.Signal()
		state.cond.L.Unlock()
	}
}

// OnPressureChange updates limits based on memory pressure.
func (b *GoroutineBudget) OnPressureChange(level PressureLevel) {
	b.pressureLevel.Store(int32(level))
	b.mu.Lock()
	defer b.mu.Unlock()
	b.recalculateLimitsLocked()
}

func (b *GoroutineBudget) recalculateLimitsLocked() {
	if len(b.agents) == 0 {
		return
	}

	totalWeight := b.calculateTotalWeight()
	pressureMultiplier := b.getPressureMultiplier()

	for _, state := range b.agents {
		b.updateAgentLimits(state, totalWeight, pressureMultiplier)
	}
}

func (b *GoroutineBudget) calculateTotalWeight() float64 {
	var total float64
	for _, state := range b.agents {
		total += b.getAgentWeight(state.agentType)
	}
	return total
}

const defaultAgentWeight = 0.5

func (b *GoroutineBudget) getAgentWeight(agentType string) float64 {
	weight, ok := b.typeWeights[agentType]
	if !ok || weight == 0 {
		return defaultAgentWeight
	}
	return weight
}

func (b *GoroutineBudget) getPressureMultiplier() float64 {
	switch PressureLevel(b.pressureLevel.Load()) {
	case PressureElevated:
		return 0.75
	case PressureHigh:
		return 0.50
	case PressureCritical:
		return 0.25
	default:
		return 1.0
	}
}

func (b *GoroutineBudget) updateAgentLimits(state *agentBudgetState, totalWeight, pressureMultiplier float64) {
	weight := b.getAgentWeight(state.agentType)
	baseBudget := float64(b.systemLimit) * (weight / totalWeight)
	effectiveBudget := baseBudget * pressureMultiplier

	state.softLimit = max(int64(effectiveBudget), 10)
	state.hardLimit = max(int64(effectiveBudget*b.burstMultiplier), 15)
}

// GetStats returns statistics for an agent.
func (b *GoroutineBudget) GetStats(agentID string) (active, peak, softLimit, hardLimit int64, ok bool) {
	state := b.getAgentState(agentID)
	if state == nil {
		return 0, 0, 0, 0, false
	}

	return state.active.Load(), state.peak.Load(), state.softLimit, state.hardLimit, true
}

// TotalActive returns the total active goroutines across all agents.
func (b *GoroutineBudget) TotalActive() int64 {
	return b.totalActive.Load()
}

// SystemLimit returns the configured system limit.
func (b *GoroutineBudget) SystemLimit() int64 {
	return b.systemLimit
}

// AgentCount returns the number of registered agents.
func (b *GoroutineBudget) AgentCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.agents)
}
