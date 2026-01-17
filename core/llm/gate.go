package llm

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrGateClosed     = errors.New("gate is closed")
	ErrBudgetExceeded = errors.New("budget exceeded")
	ErrRateLimited    = errors.New("rate limited")
	ErrWorkerPoolFull = errors.New("worker pool full")
)

type LLMResult struct {
	RequestID string
	Response  any
	Error     error
	Usage     *TokenUsage
}

type TokenUsage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

type GateConfig struct {
	WorkerPoolSize     int
	MaxQueueSize       int
	RequestTimeout     time.Duration
	BudgetCheckEnabled bool
	RateLimitEnabled   bool
}

func DefaultGateConfig() GateConfig {
	return GateConfig{
		WorkerPoolSize:     4,
		MaxQueueSize:       1000,
		RequestTimeout:     5 * time.Minute,
		BudgetCheckEnabled: true,
		RateLimitEnabled:   true,
	}
}

type LLMExecutor interface {
	Execute(ctx context.Context, req *LLMRequest) (any, *TokenUsage, error)
}

type RequestGate struct {
	config      GateConfig
	queue       *PriorityQueue
	budget      *TokenBudget
	rateLimiter *ProviderRateLimiter
	executor    LLMExecutor

	workers       int32
	activeWorkers int32
	pendingCount  int32

	mu          sync.Mutex
	pendingReqs map[string]chan *LLMResult
	closed      atomic.Bool
	stopCh      chan struct{}
	workerWg    sync.WaitGroup
}

func NewRequestGate(
	config GateConfig,
	budget *TokenBudget,
	rateLimiter *ProviderRateLimiter,
	executor LLMExecutor,
) *RequestGate {
	g := &RequestGate{
		config:      config,
		queue:       NewPriorityQueue(),
		budget:      budget,
		rateLimiter: rateLimiter,
		executor:    executor,
		workers:     int32(config.WorkerPoolSize),
		pendingReqs: make(map[string]chan *LLMResult),
		stopCh:      make(chan struct{}),
	}

	g.startWorkers()
	return g
}

func (g *RequestGate) startWorkers() {
	for i := 0; i < int(g.workers); i++ {
		g.workerWg.Add(1)
		go g.workerLoop()
	}
}

func (g *RequestGate) workerLoop() {
	defer g.workerWg.Done()

	for {
		select {
		case <-g.stopCh:
			return
		default:
			g.processNext()
		}
	}
}

func (g *RequestGate) processNext() {
	ctx, cancel := context.WithTimeout(context.Background(), g.config.RequestTimeout)
	defer cancel()

	req, err := g.queue.Pop(ctx)
	if err != nil {
		return
	}

	atomic.AddInt32(&g.activeWorkers, 1)
	defer atomic.AddInt32(&g.activeWorkers, -1)

	result := g.executeRequest(ctx, req)
	g.deliverResult(req.ID, result)
}

func (g *RequestGate) executeRequest(ctx context.Context, req *LLMRequest) *LLMResult {
	result := &LLMResult{RequestID: req.ID}

	if g.config.RateLimitEnabled && g.rateLimiter != nil {
		if !g.rateLimiter.CanProceed() {
			result.Error = ErrRateLimited
			return result
		}
	}

	if g.config.BudgetCheckEnabled && g.budget != nil {
		if err := g.budget.CheckBudget(req); err != nil {
			result.Error = err
			return result
		}
	}

	if g.executor == nil {
		result.Response = nil
		return result
	}

	response, usage, err := g.executor.Execute(ctx, req)
	result.Response = response
	result.Usage = usage
	result.Error = err

	if usage != nil && g.rateLimiter != nil {
		g.rateLimiter.RecordUsage(usage.TotalTokens)
	}

	return result
}

func (g *RequestGate) deliverResult(reqID string, result *LLMResult) {
	g.mu.Lock()
	resultCh, exists := g.pendingReqs[reqID]
	if exists {
		delete(g.pendingReqs, reqID)
	}
	g.mu.Unlock()

	if exists && resultCh != nil {
		select {
		case resultCh <- result:
		default:
		}
	}

	atomic.AddInt32(&g.pendingCount, -1)
}

func (g *RequestGate) Submit(ctx context.Context, req *LLMRequest) (*LLMResult, error) {
	if g.closed.Load() {
		return nil, ErrGateClosed
	}

	if g.config.MaxQueueSize > 0 && g.queue.Len() >= g.config.MaxQueueSize {
		return nil, ErrWorkerPoolFull
	}

	resultCh := make(chan *LLMResult, 1)

	g.mu.Lock()
	g.pendingReqs[req.ID] = resultCh
	g.mu.Unlock()

	atomic.AddInt32(&g.pendingCount, 1)
	g.queue.Push(req)

	select {
	case result := <-resultCh:
		return result, nil
	case <-ctx.Done():
		g.cancelRequest(req.ID)
		return nil, ctx.Err()
	}
}

func (g *RequestGate) SubmitAsync(ctx context.Context, req *LLMRequest) (chan *LLMResult, error) {
	if g.closed.Load() {
		return nil, ErrGateClosed
	}

	if g.config.MaxQueueSize > 0 && g.queue.Len() >= g.config.MaxQueueSize {
		return nil, ErrWorkerPoolFull
	}

	resultCh := make(chan *LLMResult, 1)

	g.mu.Lock()
	g.pendingReqs[req.ID] = resultCh
	g.mu.Unlock()

	atomic.AddInt32(&g.pendingCount, 1)
	g.queue.Push(req)

	return resultCh, nil
}

func (g *RequestGate) cancelRequest(reqID string) {
	g.mu.Lock()
	delete(g.pendingReqs, reqID)
	g.mu.Unlock()
}

func (g *RequestGate) Close() error {
	if g.closed.Swap(true) {
		return nil
	}

	close(g.stopCh)
	g.queue.Close()
	g.workerWg.Wait()

	g.mu.Lock()
	for _, ch := range g.pendingReqs {
		close(ch)
	}
	g.pendingReqs = nil
	g.mu.Unlock()

	return nil
}

type GateStats struct {
	QueueSize     int
	ActiveWorkers int
	PendingCount  int
	WorkerPool    int
}

func (g *RequestGate) Stats() GateStats {
	return GateStats{
		QueueSize:     g.queue.Len(),
		ActiveWorkers: int(atomic.LoadInt32(&g.activeWorkers)),
		PendingCount:  int(atomic.LoadInt32(&g.pendingCount)),
		WorkerPool:    int(g.workers),
	}
}

func (g *RequestGate) IsHealthy() bool {
	if g.closed.Load() {
		return false
	}

	stats := g.Stats()
	return stats.QueueSize < g.config.MaxQueueSize
}

func (g *RequestGate) SetBudget(budget *TokenBudget) {
	g.mu.Lock()
	g.budget = budget
	g.mu.Unlock()
}

func (g *RequestGate) SetRateLimiter(limiter *ProviderRateLimiter) {
	g.mu.Lock()
	g.rateLimiter = limiter
	g.mu.Unlock()
}
