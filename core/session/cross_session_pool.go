package session

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrPoolClosed          = errors.New("cross-session pool is closed")
	ErrAllocationExhausted = errors.New("session allocation exhausted")
	ErrAcquisitionTimeout  = errors.New("resource acquisition timed out")
	ErrPreemptionFailed    = errors.New("preemption failed")
	ErrInvalidPriority     = errors.New("invalid priority")
)

type Priority int

const (
	PriorityUser Priority = iota
	PriorityPipeline
	PriorityBackground
)

const (
	DefaultPreemptTimeout = 5 * time.Second
	DefaultAcquireTimeout = 30 * time.Second
)

type CrossSessionPoolConfig struct {
	TotalSlots     int
	PreemptTimeout time.Duration
	AcquireTimeout time.Duration
}

func DefaultCrossSessionPoolConfig() CrossSessionPoolConfig {
	return CrossSessionPoolConfig{
		TotalSlots:     100,
		PreemptTimeout: DefaultPreemptTimeout,
		AcquireTimeout: DefaultAcquireTimeout,
	}
}

type ResourceHandle struct {
	ID        string
	SessionID string
	Priority  Priority
	pool      *CrossSessionPool

	released atomic.Bool
}

func (h *ResourceHandle) Release() {
	if h.released.CompareAndSwap(false, true) {
		h.pool.release(h)
	}
}

type CrossSessionPool struct {
	config     CrossSessionPoolConfig
	calculator *FairShareCalculator
	dispatcher *CrossSessionSignalDispatcher
	registry   *Registry

	mu              sync.RWMutex
	allocations     map[string]int
	handles         map[string]*ResourceHandle
	waiters         []*waiter
	totalAllocated  int
	handleIDCounter int64
	closed          bool
	closeOnce       sync.Once

	notifyChan chan struct{}
}

type waiter struct {
	sessionID string
	priority  Priority
	result    chan *ResourceHandle
	ctx       context.Context
}

type allocationUpdate struct {
	sessionID string
	count     int
}

func NewCrossSessionPool(
	cfg CrossSessionPoolConfig,
	calculator *FairShareCalculator,
	dispatcher *CrossSessionSignalDispatcher,
	registry *Registry,
) *CrossSessionPool {
	cfg = normalizeCrossSessionPoolConfig(cfg)

	return &CrossSessionPool{
		config:      cfg,
		calculator:  calculator,
		dispatcher:  dispatcher,
		registry:    registry,
		allocations: make(map[string]int),
		handles:     make(map[string]*ResourceHandle),
		notifyChan:  make(chan struct{}, 1),
	}
}

func normalizeCrossSessionPoolConfig(cfg CrossSessionPoolConfig) CrossSessionPoolConfig {
	if cfg.TotalSlots <= 0 {
		cfg.TotalSlots = 100
	}
	if cfg.PreemptTimeout <= 0 {
		cfg.PreemptTimeout = DefaultPreemptTimeout
	}
	if cfg.AcquireTimeout <= 0 {
		cfg.AcquireTimeout = DefaultAcquireTimeout
	}
	return cfg
}

func (p *CrossSessionPool) Acquire(ctx context.Context, sessionID string, priority Priority) (*ResourceHandle, error) {
	if err := p.checkClosed(); err != nil {
		return nil, err
	}

	handle, acquired := p.tryAcquire(sessionID, priority)
	if acquired {
		return handle, nil
	}

	if priority == PriorityUser {
		return p.acquireWithPreemption(ctx, sessionID)
	}

	return p.waitForSlot(ctx, sessionID, priority)
}

func (p *CrossSessionPool) checkClosed() error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		return ErrPoolClosed
	}
	return nil
}

func (p *CrossSessionPool) tryAcquire(sessionID string, priority Priority) (*ResourceHandle, bool) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, false
	}

	if !p.hasAvailableSlot(sessionID) {
		p.mu.Unlock()
		return nil, false
	}

	handle, count := p.allocateHandle(sessionID, priority)
	p.mu.Unlock()

	p.applyAllocationUpdates([]allocationUpdate{{sessionID: sessionID, count: count}})

	return handle, true
}

func (p *CrossSessionPool) hasAvailableSlot(sessionID string) bool {
	if p.totalAllocated >= p.config.TotalSlots {
		return false
	}

	allocation := p.getSessionLimit(sessionID)
	current := p.allocations[sessionID]

	return current < allocation
}

func (p *CrossSessionPool) getSessionLimit(sessionID string) int {
	allocs := p.calculator.GetLastAllocation()
	if alloc, ok := allocs[sessionID]; ok {
		return alloc.SubprocessSlots
	}
	return 1
}

func (p *CrossSessionPool) allocateHandle(sessionID string, priority Priority) (*ResourceHandle, int) {
	p.handleIDCounter++
	id := generateHandleID(p.handleIDCounter)

	handle := &ResourceHandle{
		ID:        id,
		SessionID: sessionID,
		Priority:  priority,
		pool:      p,
	}

	p.handles[id] = handle
	p.allocations[sessionID]++
	p.totalAllocated++

	return handle, p.allocations[sessionID]
}

func generateHandleID(counter int64) string {
	return "handle-" + formatInt64(counter)
}

func formatInt64(n int64) string {
	if n == 0 {
		return "0"
	}

	var digits [20]byte
	i := len(digits)

	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}

	return string(digits[i:])
}

func (p *CrossSessionPool) applyAllocationUpdates(updates []allocationUpdate) {
	for _, update := range updates {
		p.updateRegistryAllocation(update.sessionID, update.count)
	}
}

func (p *CrossSessionPool) updateRegistryAllocation(sessionID string, count int) {
	if p.registry == nil {
		return
	}
	p.registry.UpdateAllocatedSlots(sessionID, count)
	p.registry.Touch(sessionID)
}

func (p *CrossSessionPool) acquireWithPreemption(ctx context.Context, sessionID string) (*ResourceHandle, error) {
	if err := p.sendPreemptSignal(sessionID); err != nil {
		return nil, err
	}

	return p.waitForSlotWithTimeout(ctx, sessionID, PriorityUser, p.config.PreemptTimeout)
}

func (p *CrossSessionPool) sendPreemptSignal(fromSession string) error {
	if p.dispatcher == nil {
		return nil
	}

	signal := NewPreemptSignal(fromSession, "", "resource_needed")
	return p.dispatcher.SendSignal(signal)
}

func (p *CrossSessionPool) waitForSlot(ctx context.Context, sessionID string, priority Priority) (*ResourceHandle, error) {
	return p.waitForSlotWithTimeout(ctx, sessionID, priority, p.config.AcquireTimeout)
}

func (p *CrossSessionPool) waitForSlotWithTimeout(ctx context.Context, sessionID string, priority Priority, timeout time.Duration) (*ResourceHandle, error) {
	w := &waiter{
		sessionID: sessionID,
		priority:  priority,
		result:    make(chan *ResourceHandle, 1),
		ctx:       ctx,
	}

	p.addWaiter(w)
	defer p.removeWaiter(w)

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, ErrAcquisitionTimeout
	case handle := <-w.result:
		return handle, nil
	}
}

func (p *CrossSessionPool) addWaiter(w *waiter) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.waiters = append(p.waiters, w)
}

func (p *CrossSessionPool) removeWaiter(w *waiter) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for i, existing := range p.waiters {
		if existing == w {
			p.waiters[i] = nil
			p.waiters = append(p.waiters[:i], p.waiters[i+1:]...)
			return
		}
	}
}

func (p *CrossSessionPool) release(handle *ResourceHandle) {
	p.mu.Lock()

	if _, exists := p.handles[handle.ID]; !exists {
		p.mu.Unlock()
		return
	}

	delete(p.handles, handle.ID)
	p.allocations[handle.SessionID]--
	p.totalAllocated--

	count := p.allocations[handle.SessionID]
	if count <= 0 {
		delete(p.allocations, handle.SessionID)
		count = 0
	}

	updates := p.wakeWaiters()
	p.mu.Unlock()

	updates = append(updates, allocationUpdate{sessionID: handle.SessionID, count: count})
	p.applyAllocationUpdates(updates)
}

func (p *CrossSessionPool) wakeWaiters() []allocationUpdate {
	var updates []allocationUpdate
	for _, w := range p.waiters {
		update, woke := p.tryWakeWaiter(w)
		if woke {
			updates = append(updates, update)
			break
		}
	}
	return updates
}

func (p *CrossSessionPool) tryWakeWaiter(w *waiter) (allocationUpdate, bool) {
	if !p.canWakeWaiter(w) {
		return allocationUpdate{}, false
	}

	handle, count := p.allocateHandle(w.sessionID, w.priority)

	select {
	case w.result <- handle:
	default:
	}

	return allocationUpdate{sessionID: w.sessionID, count: count}, true
}

func (p *CrossSessionPool) canWakeWaiter(w *waiter) bool {
	return w.ctx.Err() == nil && p.hasAvailableSlot(w.sessionID)
}

func (p *CrossSessionPool) GetSessionAllocation(sessionID string) int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.allocations[sessionID]
}

func (p *CrossSessionPool) GetTotalAllocated() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.totalAllocated
}

func (p *CrossSessionPool) GetWaitingCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.waiters)
}

func (p *CrossSessionPool) GetAvailableSlots() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.config.TotalSlots - p.totalAllocated
}

func (p *CrossSessionPool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrPoolClosed
	}
	p.closed = true
	waitersToClose := p.waiters
	p.waiters = nil
	p.mu.Unlock()

	p.closeOnce.Do(func() {
		p.closeWaiters(waitersToClose)
	})

	return nil
}

func (p *CrossSessionPool) closeWaiters(waiters []*waiter) {
	for _, w := range waiters {
		close(w.result)
	}
}
