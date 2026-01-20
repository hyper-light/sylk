package concurrency

import (
	"container/heap"
	"context"
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

var (
	ErrGateClosed      = errors.New("gate is closed")
	ErrRequestTimeout  = errors.New("request timeout")
	ErrQueueFull       = errors.New("pipeline queue full")
	ErrRateLimited     = errors.New("rate limited")
	ErrUserQueueFull   = errors.New("user queue full")
)

type RequestPriority int

const (
	PriorityUserInteractive RequestPriority = 100
	PriorityPlanning        RequestPriority = 80
	PriorityExecution       RequestPriority = 60
	PriorityValidation      RequestPriority = 40
	PriorityBackground      RequestPriority = 20
)

const (
	AgentTypeEngineer  AgentType = "engineer"
	AgentTypeDesigner  AgentType = "designer"
	AgentTypeInspector AgentType = "inspector"
	AgentTypeTester    AgentType = "tester"
)

type Message struct {
	Role    string
	Content string
}

type LLMRequest struct {
	ID            string
	SessionID     string
	PipelineID    string
	TaskID        string
	AgentID       string
	AgentType     AgentType
	Provider      string
	Model         string
	Messages      []Message
	Priority      RequestPriority
	CreatedAt     time.Time
	TokenEstimate int
	UserInvoked   bool
	ResultCh      chan *LLMResult
}

func NewLLMRequest(
	sessionID, pipelineID, taskID, agentID, agentType string,
	provider, model string,
	messages []Message,
	priority RequestPriority,
	tokenEstimate int,
) *LLMRequest {
	return &LLMRequest{
		ID:            uuid.New().String(),
		SessionID:     sessionID,
		PipelineID:    pipelineID,
		TaskID:        taskID,
		AgentID:       agentID,
		AgentType:     AgentType(agentType),
		Provider:      provider,
		Model:         model,
		Messages:      messages,
		Priority:      priority,
		CreatedAt:     time.Now(),
		TokenEstimate: tokenEstimate,
	}
}

type LLMResult struct {
	RequestID  string
	Response   any
	Error      error
	TokensUsed int
}

type BudgetChecker interface {
	CheckBudget(req *LLMRequest) error
}

type RateLimiter interface {
	CanProceed() bool
	RecordUsage(tokens int)
}

type RequestExecutor interface {
	Execute(ctx context.Context, req *LLMRequest) (any, error)
}

type UsageAwareExecutor interface {
	ExecuteWithUsage(ctx context.Context, req *LLMRequest) (any, int, error)
}

var interactiveAgents = map[AgentType]bool{
	AgentGuide:        true,
	AgentArchitect:    true,
	AgentOrchestrator: true,
	AgentLibrarian:    true,
	AgentArchivalist:  true,
	AgentAcademic:     true,
}

// RejectPolicy defines what to do when a queue is full.
type RejectPolicy int

const (
	// RejectPolicyError returns an error immediately when queue is full.
	RejectPolicyError RejectPolicy = iota
	// RejectPolicyBlock blocks until space is available or timeout.
	RejectPolicyBlock
	// RejectPolicyDropOldest drops the oldest item to make room.
	RejectPolicyDropOldest
)

// BoundedQueueConfig holds configuration for bounded queues.
type BoundedQueueConfig struct {
	MaxSize      int           // Maximum queue size (default: 10000)
	RejectPolicy RejectPolicy  // What to do when full (default: RejectPolicyError)
	BlockTimeout time.Duration // How long to block before rejecting (for RejectPolicyBlock)
}

// DefaultBoundedQueueConfig returns a default configuration for bounded queues.
func DefaultBoundedQueueConfig() BoundedQueueConfig {
	return BoundedQueueConfig{
		MaxSize:      10000,
		RejectPolicy: RejectPolicyError,
		BlockTimeout: 5 * time.Second,
	}
}

// UnboundedQueue is a bounded FIFO queue for LLM requests with backpressure support.
// Despite its name (kept for backward compatibility), it now enforces size limits.
type UnboundedQueue struct {
	mu           sync.Mutex
	items        []*LLMRequest
	cond         *sync.Cond
	maxSize      int
	rejectPolicy RejectPolicy
	blockTimeout time.Duration
	closed       bool
}

// NewUnboundedQueue creates a new queue with default bounded configuration.
// For backward compatibility, this creates a queue with a 10000 item limit.
func NewUnboundedQueue() *UnboundedQueue {
	return NewBoundedQueue(DefaultBoundedQueueConfig())
}

// NewBoundedQueue creates a new queue with the specified configuration.
func NewBoundedQueue(config BoundedQueueConfig) *UnboundedQueue {
	maxSize := config.MaxSize
	if maxSize <= 0 {
		maxSize = 10000
	}
	blockTimeout := config.BlockTimeout
	if blockTimeout <= 0 {
		blockTimeout = 5 * time.Second
	}

	q := &UnboundedQueue{
		items:        make([]*LLMRequest, 0, min(maxSize, 100)),
		maxSize:      maxSize,
		rejectPolicy: config.RejectPolicy,
		blockTimeout: blockTimeout,
	}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// Push adds a request to the queue with backpressure handling.
// Returns ErrUserQueueFull if the queue is full and reject policy is RejectPolicyError.
// Returns ErrGateClosed if the queue has been closed.
func (q *UnboundedQueue) Push(req *LLMRequest) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return ErrGateClosed
	}

	if err := q.handleFullQueue(); err != nil {
		return err
	}

	q.items = append(q.items, req)
	q.cond.Signal()
	return nil
}

// handleFullQueue applies the reject policy when the queue is full.
// Must be called with q.mu held. Returns with q.mu held.
func (q *UnboundedQueue) handleFullQueue() error {
	if !q.isFull() {
		return nil
	}
	return q.applyRejectPolicy()
}

// isFull returns true if the queue is at capacity.
func (q *UnboundedQueue) isFull() bool {
	return len(q.items) >= q.maxSize
}

// applyRejectPolicy applies the configured policy when queue is full.
// Must be called with q.mu held. Returns with q.mu held.
func (q *UnboundedQueue) applyRejectPolicy() error {
	switch q.rejectPolicy {
	case RejectPolicyError:
		return ErrUserQueueFull
	case RejectPolicyDropOldest:
		q.dropOldest()
	case RejectPolicyBlock:
		return q.waitForSpace()
	}
	return nil
}

// dropOldest removes the oldest item from the queue.
// Must be called with q.mu held.
func (q *UnboundedQueue) dropOldest() {
	if len(q.items) > 0 {
		q.items[0] = nil // Help GC
		q.items = q.items[1:]
	}
}

// waitForSpace blocks until space is available in the queue or timeout expires.
// Must be called with q.mu held. Returns with q.mu held.
func (q *UnboundedQueue) waitForSpace() error {
	deadline := time.Now().Add(q.blockTimeout)
	timerDone := make(chan struct{})
	go q.timedWakeup(timerDone, q.blockTimeout)
	defer close(timerDone)

	return q.waitUntilSpaceOrDeadline(deadline)
}

// waitUntilSpaceOrDeadline waits until space is available, queue is closed, or deadline passes.
// Must be called with q.mu held. Returns with q.mu held.
func (q *UnboundedQueue) waitUntilSpaceOrDeadline(deadline time.Time) error {
	for q.shouldWaitForSpace() {
		if time.Now().After(deadline) {
			return ErrUserQueueFull
		}
		q.cond.Wait()
	}
	return q.checkClosedAfterWait()
}

// shouldWaitForSpace returns true if the queue is full and not closed.
func (q *UnboundedQueue) shouldWaitForSpace() bool {
	return len(q.items) >= q.maxSize && !q.closed
}

// checkClosedAfterWait returns ErrGateClosed if the queue was closed.
func (q *UnboundedQueue) checkClosedAfterWait() error {
	if q.closed {
		return ErrGateClosed
	}
	return nil
}

// timedWakeup broadcasts on the condition variable after the timeout.
// Exits early if done channel is closed.
func (q *UnboundedQueue) timedWakeup(done <-chan struct{}, timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-timer.C:
		q.cond.Broadcast()
	case <-done:
		// Caller finished, no need to wake
	}
}

// Pop removes and returns the first request from the queue.
// Returns nil if the queue is empty.
func (q *UnboundedQueue) Pop() *LLMRequest {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.items) == 0 {
		return nil
	}
	req := q.items[0]
	q.items[0] = nil // Help GC
	q.items = q.items[1:]
	q.cond.Signal() // Signal waiters that space is available
	return req
}

// Len returns the current number of items in the queue.
func (q *UnboundedQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// MaxSize returns the maximum capacity of the queue.
func (q *UnboundedQueue) MaxSize() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.maxSize
}

// IsFull returns whether the queue is at capacity.
func (q *UnboundedQueue) IsFull() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items) >= q.maxSize
}

// Close closes the queue, preventing new pushes and waking blocked waiters.
func (q *UnboundedQueue) Close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.cond.Broadcast()
}

type requestHeap []*LLMRequest

func (h requestHeap) Len() int { return len(h) }

func (h requestHeap) Less(i, j int) bool {
	if h[i].Priority != h[j].Priority {
		return h[i].Priority > h[j].Priority
	}
	return h[i].CreatedAt.Before(h[j].CreatedAt)
}

func (h requestHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *requestHeap) Push(x any) {
	*h = append(*h, x.(*LLMRequest))
}

func (h *requestHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

type PriorityQueue struct {
	mu      sync.Mutex
	heap    requestHeap
	cond    *sync.Cond
	closed  bool
	maxSize int
}

func NewPriorityQueue(maxSize int) *PriorityQueue {
	pq := &PriorityQueue{
		heap:    make(requestHeap, 0, 16),
		maxSize: maxSize,
	}
	pq.cond = sync.NewCond(&pq.mu)
	heap.Init(&pq.heap)
	return pq
}

func (pq *PriorityQueue) Push(req *LLMRequest) error {
	pq.mu.Lock()
	defer pq.mu.Unlock()

	if pq.closed {
		return ErrGateClosed
	}
	if pq.maxSize > 0 && pq.heap.Len() >= pq.maxSize {
		return ErrQueueFull
	}

	heap.Push(&pq.heap, req)
	pq.cond.Signal()
	return nil
}

func (pq *PriorityQueue) Pop() *LLMRequest {
	pq.mu.Lock()
	defer pq.mu.Unlock()

	if pq.heap.Len() == 0 {
		return nil
	}
	return heap.Pop(&pq.heap).(*LLMRequest)
}

func (pq *PriorityQueue) Len() int {
	pq.mu.Lock()
	defer pq.mu.Unlock()
	return pq.heap.Len()
}

func (pq *PriorityQueue) Close() {
	pq.mu.Lock()
	pq.closed = true
	pq.mu.Unlock()
	pq.cond.Broadcast()
}

type ActiveRequest struct {
	Request    *LLMRequest
	CancelFunc context.CancelFunc
	IsUser     bool
	StartedAt  time.Time
	Preempted  bool
}

type DualQueueGateConfig struct {
	MaxPipelineQueueSize  int
	MaxUserQueueSize      int           // Maximum size for user queue (default: 1000)
	UserQueueRejectPolicy RejectPolicy  // Policy when user queue is full (default: RejectPolicyError)
	UserQueueBlockTimeout time.Duration // Block timeout for user queue (default: 5s)
	MaxConcurrentRequests int
	RequestTimeout        time.Duration
	ShutdownTimeout       time.Duration // Soft timeout for graceful shutdown (default: 5s)
	HardDeadline          time.Duration // Hard deadline after which shutdown completes forcefully (default: 2x ShutdownTimeout)
}

func DefaultDualQueueGateConfig() DualQueueGateConfig {
	return DualQueueGateConfig{
		MaxPipelineQueueSize:  1000,
		MaxUserQueueSize:      1000,
		UserQueueRejectPolicy: RejectPolicyError,
		UserQueueBlockTimeout: 5 * time.Second,
		MaxConcurrentRequests: 4,
		RequestTimeout:        5 * time.Minute,
		ShutdownTimeout:       5 * time.Second,
		HardDeadline:          10 * time.Second, // 2x ShutdownTimeout
	}
}

type DualQueueGate struct {
	config         DualQueueGateConfig
	userQueue      *UnboundedQueue
	pipelineQueue  *PriorityQueue
	activeRequests map[string]*ActiveRequest
	mu             sync.Mutex
	closed         atomic.Bool
	workAvailable  chan struct{}
	stopCh         chan struct{}
	executor       RequestExecutor
	budgetChecker  BudgetChecker
	rateLimiter    RateLimiter
	requestWg      sync.WaitGroup
	dispatchWg     sync.WaitGroup
	ctx            context.Context
	cancel         context.CancelFunc

	// Health monitoring (optional)
	healthMonitor HealthMonitor
}

// NewDualQueueGate creates a new gate with a background context.
// For context propagation, use NewDualQueueGateWithContext instead.
func NewDualQueueGate(config DualQueueGateConfig, executor RequestExecutor) *DualQueueGate {
	return NewDualQueueGateWithContext(context.Background(), config, executor)
}

// NewDualQueueGateWithContext creates a new gate with the given parent context.
// All requests processed by this gate will respect the parent context's cancellation.
func NewDualQueueGateWithContext(ctx context.Context, config DualQueueGateConfig, executor RequestExecutor) *DualQueueGate {
	config = normalizeDualQueueGateConfig(config)

	userQueueConfig := BoundedQueueConfig{
		MaxSize:      config.MaxUserQueueSize,
		RejectPolicy: config.UserQueueRejectPolicy,
		BlockTimeout: config.UserQueueBlockTimeout,
	}

	gateCtx, cancel := context.WithCancel(ctx)
	g := &DualQueueGate{
		config:         config,
		userQueue:      NewBoundedQueue(userQueueConfig),
		pipelineQueue:  NewPriorityQueue(config.MaxPipelineQueueSize),
		activeRequests: make(map[string]*ActiveRequest),
		workAvailable:  make(chan struct{}, 1),
		stopCh:         make(chan struct{}),
		executor:       executor,
		ctx:            gateCtx,
		cancel:         cancel,
	}
	g.startDispatchLoop()
	return g
}

func normalizeDualQueueGateConfig(cfg DualQueueGateConfig) DualQueueGateConfig {
	if cfg.MaxPipelineQueueSize <= 0 {
		cfg.MaxPipelineQueueSize = 1000
	}
	if cfg.MaxUserQueueSize <= 0 {
		cfg.MaxUserQueueSize = 1000
	}
	if cfg.UserQueueBlockTimeout <= 0 {
		cfg.UserQueueBlockTimeout = 5 * time.Second
	}
	if cfg.MaxConcurrentRequests <= 0 {
		cfg.MaxConcurrentRequests = 4
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 5 * time.Minute
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 5 * time.Second
	}
	if cfg.HardDeadline <= 0 {
		cfg.HardDeadline = 2 * cfg.ShutdownTimeout
	}
	return cfg
}

func (g *DualQueueGate) SetBudget(checker BudgetChecker) {
	g.mu.Lock()
	g.budgetChecker = checker
	g.mu.Unlock()
}

func (g *DualQueueGate) SetRateLimiter(limiter RateLimiter) {
	g.mu.Lock()
	g.rateLimiter = limiter
	g.mu.Unlock()
}

// SetHealthMonitor sets an optional health monitor for observability.
// The monitor receives events for queue depth changes and shutdown.
func (g *DualQueueGate) SetHealthMonitor(monitor HealthMonitor) {
	g.mu.Lock()
	g.healthMonitor = monitor
	g.mu.Unlock()
}

func (g *DualQueueGate) Submit(ctx context.Context, req *LLMRequest) error {
	if err := g.checkClosed(); err != nil {
		return err
	}

	g.ensureRequestDefaults(req)
	isUser := g.isUserInteractive(req)

	if err := g.enqueueRequest(req, isUser); err != nil {
		return err
	}

	if err := g.checkClosed(); err != nil {
		g.notifyClosedRequest(req)
		return err
	}

	g.signalWork()
	return nil
}

func (g *DualQueueGate) ensureRequestDefaults(req *LLMRequest) {
	if req.ID == "" {
		req.ID = uuid.New().String()
	}
	if req.ResultCh == nil {
		req.ResultCh = make(chan *LLMResult, 1)
	}
	if req.CreatedAt.IsZero() {
		req.CreatedAt = time.Now()
	}
	if req.Priority == 0 {
		req.Priority = DeterminePriority(req, req.UserInvoked)
	}
}

func (g *DualQueueGate) enqueueRequest(req *LLMRequest, isUser bool) error {
	if isUser {
		if err := g.userQueue.Push(req); err != nil {
			return err
		}
		g.checkQueueDepth(g.userQueue.Len(), g.config.MaxUserQueueSize)
		g.maybePreemptForUser()
		return nil
	}

	if err := g.pipelineQueue.Push(req); err != nil {
		return err
	}
	g.checkQueueDepth(g.pipelineQueue.Len(), g.config.MaxPipelineQueueSize)
	return nil
}

// checkQueueDepth emits health event if queue depth exceeds 80% threshold.
func (g *DualQueueGate) checkQueueDepth(current, max int) {
	g.mu.Lock()
	monitor := g.healthMonitor
	g.mu.Unlock()

	if monitor == nil {
		return
	}

	threshold := (max * 80) / 100
	if current >= threshold {
		g.emitQueueDepthHigh(current, max)
	}
}

// emitQueueDepthHigh emits a health event when queue depth is high.
func (g *DualQueueGate) emitQueueDepthHigh(current, max int) {
	g.mu.Lock()
	monitor := g.healthMonitor
	g.mu.Unlock()

	if monitor == nil {
		return
	}

	event := newHealthEvent(EventQueueDepthHigh, "DualQueueGate")
	event.Count = int64(current)
	event.NewValue = int64(max)
	emitHealthEvent(monitor, event)
}

func (g *DualQueueGate) notifyClosedRequest(req *LLMRequest) {
	if req.ResultCh == nil {
		return
	}
	select {
	case req.ResultCh <- &LLMResult{RequestID: req.ID, Error: ErrGateClosed}:
	default:
	}
}

func (g *DualQueueGate) checkClosed() error {
	if g.closed.Load() {
		return ErrGateClosed
	}
	return nil
}

func (g *DualQueueGate) SubmitAndWait(ctx context.Context, req *LLMRequest) (any, error) {
	if err := g.Submit(ctx, req); err != nil {
		return nil, err
	}
	return g.waitForResult(ctx, req)
}

func (g *DualQueueGate) waitForResult(ctx context.Context, req *LLMRequest) (any, error) {
	select {
	case result := <-req.ResultCh:
		if result.Error != nil {
			return nil, result.Error
		}
		return result.Response, nil
	case <-ctx.Done():
		g.Cancel(req.ID)
		return nil, ctx.Err()
	}
}

func (g *DualQueueGate) isUserInteractive(req *LLMRequest) bool {
	if req.UserInvoked {
		return true
	}
	return interactiveAgents[req.AgentType]
}

func (g *DualQueueGate) maybePreemptForUser() {
	g.mu.Lock()
	defer g.mu.Unlock()

	if len(g.activeRequests) < g.config.MaxConcurrentRequests {
		return
	}

	lowest := g.findLowestPriorityPipeline()
	if lowest == nil {
		return
	}

	lowest.Preempted = true
	if lowest.CancelFunc != nil {
		lowest.CancelFunc()
	}
}

func (g *DualQueueGate) findLowestPriorityPipeline() *ActiveRequest {
	var lowest *ActiveRequest
	for _, ar := range g.activeRequests {
		if ar.IsUser {
			continue
		}
		if lowest == nil || ar.Request.Priority < lowest.Request.Priority {
			lowest = ar
		}
	}
	return lowest
}

func (g *DualQueueGate) startDispatchLoop() {
	g.dispatchWg.Add(1)
	go func() {
		defer g.dispatchWg.Done()
		g.dispatchLoop()
	}()
}

func (g *DualQueueGate) dispatchLoop() {
	for {
		select {
		case <-g.stopCh:
			return
		case <-g.workAvailable:
			g.processQueues()
		}
	}
}

func (g *DualQueueGate) processQueues() {
	g.processUserQueue()
	g.processPipelineQueue()
}

func (g *DualQueueGate) processUserQueue() {
	for g.userQueue.Len() > 0 && g.canStartRequest() {
		req := g.userQueue.Pop()
		if req != nil {
			g.startRequest(req, true)
		}
	}
}

func (g *DualQueueGate) processPipelineQueue() {
	for g.pipelineQueue.Len() > 0 && g.canStartRequest() {
		req := g.pipelineQueue.Pop()
		if req != nil {
			g.startRequest(req, false)
		}
	}
}

func (g *DualQueueGate) canStartRequest() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.activeRequests) < g.config.MaxConcurrentRequests
}

func (g *DualQueueGate) startRequest(req *LLMRequest, isUser bool) {
	ctx, cancel := g.newRequestContext()
	g.trackActiveRequest(req, cancel, isUser)

	g.requestWg.Add(1)
	go func() {
		defer g.requestWg.Done()
		g.executeRequest(ctx, req, isUser)
	}()
}

func (g *DualQueueGate) newRequestContext() (context.Context, context.CancelFunc) {
	return g.newRequestContextWithParent(nil)
}

// newRequestContextWithParent creates a request context that inherits from the given parent
// context if provided, otherwise from the gate's context. The timeout is always applied.
func (g *DualQueueGate) newRequestContextWithParent(parentCtx context.Context) (context.Context, context.CancelFunc) {
	// Use parent context if provided, otherwise use gate context
	baseCtx := parentCtx
	if baseCtx == nil {
		baseCtx = g.ctx
	}

	// Inherit from base context
	if g.config.RequestTimeout > 0 {
		return context.WithTimeout(baseCtx, g.config.RequestTimeout)
	}
	return context.WithCancel(baseCtx)
}

func (g *DualQueueGate) trackActiveRequest(req *LLMRequest, cancel context.CancelFunc, isUser bool) {
	g.mu.Lock()
	g.activeRequests[req.ID] = &ActiveRequest{
		Request:    req,
		CancelFunc: cancel,
		IsUser:     isUser,
		StartedAt:  time.Now(),
	}
	g.mu.Unlock()
}

func (g *DualQueueGate) executeRequest(ctx context.Context, req *LLMRequest, isUser bool) {
	defer g.completeRequest(req.ID)

	response, tokensUsed, err := g.runRequest(ctx, req)
	if g.shouldRequeue(req.ID, isUser, err) {
		g.requeuePipeline(req)
		return
	}

	g.sendResult(req, response, tokensUsed, err)
}

func (g *DualQueueGate) runRequest(ctx context.Context, req *LLMRequest) (any, int, error) {
	if err := g.checkBudget(req); err != nil {
		return nil, 0, err
	}
	if err := g.checkRateLimit(); err != nil {
		return nil, 0, err
	}
	return g.executeWithUsage(ctx, req)
}

func (g *DualQueueGate) checkBudget(req *LLMRequest) error {
	g.mu.Lock()
	checker := g.budgetChecker
	g.mu.Unlock()

	if checker == nil {
		return nil
	}
	return checker.CheckBudget(req)
}

func (g *DualQueueGate) checkRateLimit() error {
	g.mu.Lock()
	limiter := g.rateLimiter
	g.mu.Unlock()

	if limiter == nil {
		return nil
	}
	if limiter.CanProceed() {
		return nil
	}
	return ErrRateLimited
}

func (g *DualQueueGate) executeWithUsage(ctx context.Context, req *LLMRequest) (any, int, error) {
	if g.executor == nil {
		return nil, 0, nil
	}

	if execWithUsage, ok := g.executor.(UsageAwareExecutor); ok {
		response, tokens, err := execWithUsage.ExecuteWithUsage(ctx, req)
		g.recordUsage(tokens)
		return response, tokens, g.normalizeError(ctx, err)
	}

	response, err := g.executor.Execute(ctx, req)
	g.recordUsage(req.TokenEstimate)
	return response, req.TokenEstimate, g.normalizeError(ctx, err)
}

func (g *DualQueueGate) normalizeError(ctx context.Context, err error) error {
	if err != nil {
		return err
	}
	if ctx.Err() == context.DeadlineExceeded {
		return ErrRequestTimeout
	}
	if ctx.Err() == context.Canceled {
		return context.Canceled
	}
	return nil
}

func (g *DualQueueGate) recordUsage(tokens int) {
	if tokens <= 0 {
		return
	}
	g.mu.Lock()
	limiter := g.rateLimiter
	g.mu.Unlock()

	if limiter != nil {
		limiter.RecordUsage(tokens)
	}
}

func (g *DualQueueGate) shouldRequeue(reqID string, isUser bool, err error) bool {
	if isUser {
		return false
	}
	if !errors.Is(err, context.Canceled) {
		return false
	}
	if g.closed.Load() {
		return false
	}
	return g.isPreempted(reqID)
}

func (g *DualQueueGate) isPreempted(reqID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	ar, ok := g.activeRequests[reqID]
	if !ok {
		return false
	}
	return ar.Preempted
}

func (g *DualQueueGate) requeuePipeline(req *LLMRequest) {
	g.clearPreempted(req.ID)
	if err := g.pipelineQueue.Push(req); err == nil {
		g.signalWork()
		return
	}
	g.sendResult(req, nil, 0, ErrGateClosed)
}

func (g *DualQueueGate) clearPreempted(reqID string) {
	g.mu.Lock()
	if ar, ok := g.activeRequests[reqID]; ok {
		ar.Preempted = false
	}
	g.mu.Unlock()
}

func (g *DualQueueGate) sendResult(req *LLMRequest, response any, tokensUsed int, err error) {
	if req.ResultCh == nil {
		return
	}
	select {
	case req.ResultCh <- &LLMResult{
		RequestID:  req.ID,
		Response:   response,
		Error:      err,
		TokensUsed: tokensUsed,
	}:
	default:
	}
}

func (g *DualQueueGate) completeRequest(reqID string) {
	g.mu.Lock()
	delete(g.activeRequests, reqID)
	g.mu.Unlock()

	g.signalWork()
}

func (g *DualQueueGate) signalWork() {
	select {
	case g.workAvailable <- struct{}{}:
	default:
	}
}

// Cancel cancels an active request by its ID. Safe to call concurrently.
// If the request doesn't exist or has already completed, this is a no-op.
func (g *DualQueueGate) Cancel(reqID string) {
	cancelFunc := g.getRequestCancelFunc(reqID)
	if cancelFunc != nil {
		cancelFunc()
	}
}

// getRequestCancelFunc retrieves the cancel function for a request while holding the lock.
// Returns nil if the request doesn't exist or has no cancel function.
func (g *DualQueueGate) getRequestCancelFunc(reqID string) context.CancelFunc {
	g.mu.Lock()
	defer g.mu.Unlock()

	ar, exists := g.activeRequests[reqID]
	if !exists || ar == nil {
		return nil
	}
	return ar.CancelFunc
}

func (g *DualQueueGate) Close() {
	if g.closed.Swap(true) {
		return
	}

	g.emitShutdownStarted()

	close(g.stopCh)
	g.signalWork()
	g.dispatchWg.Wait()

	// Close queues to reject new items and wake blocked waiters
	g.userQueue.Close()
	g.pipelineQueue.Close()

	// Cancel the gate context, which propagates to all pending requests
	if g.cancel != nil {
		g.cancel()
	}

	g.cancelActiveRequests()
	g.drainQueues()
	g.waitForRequests()

	g.emitShutdownComplete()
}

// emitShutdownStarted emits a health event when shutdown begins.
func (g *DualQueueGate) emitShutdownStarted() {
	g.mu.Lock()
	monitor := g.healthMonitor
	g.mu.Unlock()

	if monitor == nil {
		return
	}

	event := newHealthEvent(EventShutdownStarted, "DualQueueGate")
	emitHealthEvent(monitor, event)
}

// emitShutdownComplete emits a health event when shutdown finishes.
func (g *DualQueueGate) emitShutdownComplete() {
	g.mu.Lock()
	monitor := g.healthMonitor
	g.mu.Unlock()

	if monitor == nil {
		return
	}

	event := newHealthEvent(EventShutdownComplete, "DualQueueGate")
	emitHealthEvent(monitor, event)
}

func (g *DualQueueGate) cancelActiveRequests() {
	g.mu.Lock()
	for _, ar := range g.activeRequests {
		if ar.CancelFunc != nil {
			ar.CancelFunc()
		}
	}
	g.mu.Unlock()
}

func (g *DualQueueGate) drainQueues() {
	g.drainQueue(g.userQueue)
	g.drainQueue(g.pipelineQueue)
}

func (g *DualQueueGate) drainQueue(queue interface{ Pop() *LLMRequest }) {
	for {
		req := queue.Pop()
		if req == nil {
			return
		}
		g.notifyClosedRequest(req)
	}
}

// waitForRequests waits for all pending requests to complete within the shutdown
// timeout. If requests don't complete within the soft timeout, it waits until
// the hard deadline and then logs orphaned requests before returning.
//
// This implementation uses a single shared waiter goroutine to prevent goroutine
// accumulation across timeout phases (W12.40, W12.41 fix).
func (g *DualQueueGate) waitForRequests() {
	if g.config.ShutdownTimeout <= 0 {
		return
	}

	// Create a single waiter goroutine shared between soft timeout and hard deadline.
	// This prevents spawning multiple inner goroutines that all block on wg.Wait().
	done := make(chan struct{})
	go g.signalOnWaitGroupDone(&g.requestWg, done)

	// Phase 1: Soft timeout
	if g.waitForDoneOrTimeout(done, g.config.ShutdownTimeout) {
		return
	}

	// Phase 2: Hard deadline (remaining time after soft timeout)
	g.waitForDoneOrHardDeadline(done)
}

// waitForDoneOrTimeout waits for the done channel to close or the timeout to elapse.
// Returns true if done closed, false if timeout elapsed.
func (g *DualQueueGate) waitForDoneOrTimeout(done <-chan struct{}, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

// waitForDoneOrHardDeadline waits for the done channel or remaining hard deadline time.
// Logs orphaned requests if hard deadline is reached.
func (g *DualQueueGate) waitForDoneOrHardDeadline(done <-chan struct{}) {
	remaining := g.config.HardDeadline - g.config.ShutdownTimeout
	if remaining <= 0 {
		g.logOrphanedRequests()
		return
	}

	timer := time.NewTimer(remaining)
	defer timer.Stop()

	select {
	case <-done:
		return
	case <-timer.C:
		g.logOrphanedRequests()
	}
}

// signalOnWaitGroupDone waits on the WaitGroup and closes done when finished.
func (g *DualQueueGate) signalOnWaitGroupDone(wg *sync.WaitGroup, done chan struct{}) {
	wg.Wait()
	close(done)
}

// signalOnWaitGroupDoneWithContext waits on the WaitGroup and closes done when
// finished. If the context is cancelled before the WaitGroup completes, the
// function exits without closing done. This prevents goroutine leaks when
// the caller times out waiting for the WaitGroup.
func (g *DualQueueGate) signalOnWaitGroupDoneWithContext(
	ctx context.Context,
	wg *sync.WaitGroup,
	done chan struct{},
) {
	finished := make(chan struct{})
	go func() {
		wg.Wait()
		close(finished)
	}()

	select {
	case <-finished:
		close(done)
	case <-ctx.Done():
		// Context cancelled - exit without closing done.
		// The inner goroutine exits when the WaitGroup reaches zero.
	}
}

// logOrphanedRequests logs information about requests that didn't complete.
func (g *DualQueueGate) logOrphanedRequests() {
	g.mu.Lock()
	orphans := g.collectOrphanInfo()
	g.mu.Unlock()

	for _, info := range orphans {
		log.Printf("DualQueueGate: orphaned request id=%s agent=%s running=%v",
			info.id, info.agent, info.duration)
	}
}

// orphanInfo holds information about an orphaned request for logging.
type orphanInfo struct {
	id       string
	agent    string
	duration time.Duration
}

// collectOrphanInfo gathers information about active requests. Must be called with mu held.
func (g *DualQueueGate) collectOrphanInfo() []orphanInfo {
	result := make([]orphanInfo, 0, len(g.activeRequests))
	for _, ar := range g.activeRequests {
		result = append(result, orphanInfo{
			id:       ar.Request.ID,
			agent:    string(ar.Request.AgentType),
			duration: time.Since(ar.StartedAt),
		})
	}
	return result
}

// boundedWaitGroupWait waits for the WaitGroup to complete within the given
// timeout. Uses a single goroutine that completes when the WaitGroup is done,
// avoiding nested goroutine patterns that could leak.
func (g *DualQueueGate) boundedWaitGroupWait(wg *sync.WaitGroup, timeout time.Duration) {
	done := make(chan struct{})
	go g.signalOnWaitGroupCompletion(wg, done)

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-done:
		// Wait completed successfully
	case <-timer.C:
		// Timeout fired - the goroutine will complete when wg reaches zero
		// (after requests are cancelled during shutdown)
	}
}

// signalOnWaitGroupCompletion waits on the WaitGroup and closes done when finished.
// This is a simple helper with no nested goroutines - it will complete when
// the WaitGroup reaches zero, which happens during normal shutdown when all
// requests finish (they are cancelled as part of the shutdown sequence).
func (g *DualQueueGate) signalOnWaitGroupCompletion(wg *sync.WaitGroup, done chan struct{}) {
	wg.Wait()
	close(done)
}

// waitGroupWaiter waits on the WaitGroup and respects context cancellation.
// If context is cancelled before the WaitGroup completes, the function exits
// without closing the done channel. The caller should handle both cases.
// Note: This spawns a single goroutine for the WaitGroup wait. That goroutine
// completes when the WaitGroup reaches zero (during shutdown, requests are
// cancelled causing the WaitGroup to decrement).
func (g *DualQueueGate) waitGroupWaiter(wg *sync.WaitGroup, done chan struct{}, ctx context.Context) {
	finished := make(chan struct{})
	go func() {
		wg.Wait()
		close(finished)
	}()

	select {
	case <-finished:
		close(done)
	case <-ctx.Done():
		// Context cancelled - exit without closing done.
		// The inner goroutine will complete when the WaitGroup reaches zero,
		// which happens when all requests finish during shutdown.
	}
}

type GateStats struct {
	UserQueueSize      int
	PipelineQueueSize  int
	ActiveRequests     int
	ActiveUserRequests int
}

func (g *DualQueueGate) Stats() GateStats {
	g.mu.Lock()
	activeCount := len(g.activeRequests)
	userCount := g.countActiveUsers()
	g.mu.Unlock()

	return GateStats{
		UserQueueSize:      g.userQueue.Len(),
		PipelineQueueSize:  g.pipelineQueue.Len(),
		ActiveRequests:     activeCount,
		ActiveUserRequests: userCount,
	}
}

func (g *DualQueueGate) countActiveUsers() int {
	userCount := 0
	for _, ar := range g.activeRequests {
		if ar.IsUser {
			userCount++
		}
	}
	return userCount
}

func DeterminePriority(req *LLMRequest, isUserInvoked bool) RequestPriority {
	if isUserInvoked {
		return PriorityUserInteractive
	}

	switch req.AgentType {
	case AgentArchitect:
		return PriorityPlanning
	case AgentTypeEngineer, AgentTypeDesigner:
		return PriorityExecution
	case AgentTypeInspector, AgentTypeTester:
		return PriorityValidation
	case AgentArchivalist, AgentLibrarian:
		return PriorityBackground
	default:
		return PriorityExecution
	}
}
