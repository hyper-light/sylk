package concurrency

import (
	"container/heap"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

var (
	ErrGateClosed     = errors.New("gate is closed")
	ErrRequestTimeout = errors.New("request timeout")
	ErrQueueFull      = errors.New("pipeline queue full")
	ErrRateLimited    = errors.New("rate limited")
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

type UnboundedQueue struct {
	mu    sync.Mutex
	items []*LLMRequest
	cond  *sync.Cond
}

func NewUnboundedQueue() *UnboundedQueue {
	q := &UnboundedQueue{
		items: make([]*LLMRequest, 0, 16),
	}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *UnboundedQueue) Push(req *LLMRequest) {
	q.mu.Lock()
	q.items = append(q.items, req)
	q.mu.Unlock()
	q.cond.Signal()
}

func (q *UnboundedQueue) Pop() *LLMRequest {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.items) == 0 {
		return nil
	}
	req := q.items[0]
	q.items[0] = nil
	q.items = q.items[1:]
	return req
}

func (q *UnboundedQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
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
	MaxConcurrentRequests int
	RequestTimeout        time.Duration
	ShutdownTimeout       time.Duration
}

func DefaultDualQueueGateConfig() DualQueueGateConfig {
	return DualQueueGateConfig{
		MaxPipelineQueueSize:  1000,
		MaxConcurrentRequests: 4,
		RequestTimeout:        5 * time.Minute,
		ShutdownTimeout:       5 * time.Second,
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
}

func NewDualQueueGate(config DualQueueGateConfig, executor RequestExecutor) *DualQueueGate {
	config = normalizeDualQueueGateConfig(config)
	g := &DualQueueGate{
		config:         config,
		userQueue:      NewUnboundedQueue(),
		pipelineQueue:  NewPriorityQueue(config.MaxPipelineQueueSize),
		activeRequests: make(map[string]*ActiveRequest),
		workAvailable:  make(chan struct{}, 1),
		stopCh:         make(chan struct{}),
		executor:       executor,
	}
	g.startDispatchLoop()
	return g
}

func normalizeDualQueueGateConfig(cfg DualQueueGateConfig) DualQueueGateConfig {
	if cfg.MaxPipelineQueueSize <= 0 {
		cfg.MaxPipelineQueueSize = 1000
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
		g.userQueue.Push(req)
		g.maybePreemptForUser()
		return nil
	}

	if err := g.pipelineQueue.Push(req); err != nil {
		return err
	}
	return nil
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
	if g.config.RequestTimeout > 0 {
		return context.WithTimeout(context.Background(), g.config.RequestTimeout)
	}
	return context.WithCancel(context.Background())
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

func (g *DualQueueGate) Cancel(reqID string) {
	g.mu.Lock()
	ar, exists := g.activeRequests[reqID]
	g.mu.Unlock()

	if exists && ar.CancelFunc != nil {
		ar.CancelFunc()
	}
}

func (g *DualQueueGate) Close() {
	if g.closed.Swap(true) {
		return
	}

	close(g.stopCh)
	g.signalWork()
	g.dispatchWg.Wait()

	g.cancelActiveRequests()
	g.drainQueues()
	g.waitForRequests()
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

func (g *DualQueueGate) waitForRequests() {
	done := make(chan struct{})
	go func() {
		g.requestWg.Wait()
		close(done)
	}()

	timer := time.NewTimer(g.config.ShutdownTimeout)
	defer timer.Stop()

	select {
	case <-done:
	case <-timer.C:
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
