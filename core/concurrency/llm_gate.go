package concurrency

import (
	"container/heap"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrGateClosed     = errors.New("gate is closed")
	ErrRequestTimeout = errors.New("request timeout")
	ErrQueueFull      = errors.New("pipeline queue full")
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

type LLMRequest struct {
	ID            string
	SessionID     string
	PipelineID    string
	TaskID        string
	AgentID       string
	AgentType     AgentType
	Provider      string
	Model         string
	Priority      RequestPriority
	CreatedAt     time.Time
	TokenEstimate int
	UserInvoked   bool
	ResultCh      chan *LLMResult
}

type LLMResult struct {
	RequestID string
	Response  any
	Error     error
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
}

type DualQueueGateConfig struct {
	MaxPipelineQueueSize  int
	MaxConcurrentRequests int
}

func DefaultDualQueueGateConfig() DualQueueGateConfig {
	return DualQueueGateConfig{
		MaxPipelineQueueSize:  1000,
		MaxConcurrentRequests: 4,
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
}

type RequestExecutor interface {
	Execute(ctx context.Context, req *LLMRequest) (any, error)
}

func NewDualQueueGate(config DualQueueGateConfig, executor RequestExecutor) *DualQueueGate {
	g := &DualQueueGate{
		config:         config,
		userQueue:      NewUnboundedQueue(),
		pipelineQueue:  NewPriorityQueue(config.MaxPipelineQueueSize),
		activeRequests: make(map[string]*ActiveRequest),
		workAvailable:  make(chan struct{}, 1),
		stopCh:         make(chan struct{}),
		executor:       executor,
	}
	go g.dispatchLoop()
	return g
}

func (g *DualQueueGate) Submit(ctx context.Context, req *LLMRequest) error {
	if g.closed.Load() {
		return ErrGateClosed
	}

	req.ResultCh = make(chan *LLMResult, 1)
	isUser := g.isUserInteractive(req)

	if isUser {
		g.userQueue.Push(req)
		g.maybePreemptForUser()
	} else {
		if err := g.pipelineQueue.Push(req); err != nil {
			return err
		}
	}

	g.signalWork()
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
	switch req.AgentType {
	case AgentGuide, AgentArchitect, AgentOrchestrator,
		AgentLibrarian, AgentArchivalist, AgentAcademic:
		return true
	default:
		return false
	}
}

func (g *DualQueueGate) maybePreemptForUser() {
	g.mu.Lock()
	defer g.mu.Unlock()

	if len(g.activeRequests) < g.config.MaxConcurrentRequests {
		return
	}

	var lowestPriority *ActiveRequest
	for _, ar := range g.activeRequests {
		if ar.IsUser {
			continue
		}
		if lowestPriority == nil {
			lowestPriority = ar
			continue
		}
		if ar.Request.Priority < lowestPriority.Request.Priority {
			lowestPriority = ar
		}
	}

	if lowestPriority != nil {
		lowestPriority.CancelFunc()
	}
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
	for g.userQueue.Len() > 0 && g.canStartRequest() {
		req := g.userQueue.Pop()
		if req != nil {
			g.startRequest(req, true)
		}
	}

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
	ctx, cancel := context.WithCancel(context.Background())

	g.mu.Lock()
	g.activeRequests[req.ID] = &ActiveRequest{
		Request:    req,
		CancelFunc: cancel,
		IsUser:     isUser,
		StartedAt:  time.Now(),
	}
	g.mu.Unlock()

	go g.executeRequest(ctx, req, isUser)
}

func (g *DualQueueGate) executeRequest(ctx context.Context, req *LLMRequest, isUser bool) {
	defer g.completeRequest(req.ID)

	var response any
	var err error

	if g.executor != nil {
		response, err = g.executor.Execute(ctx, req)
	}

	select {
	case req.ResultCh <- &LLMResult{
		RequestID: req.ID,
		Response:  response,
		Error:     err,
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
	g.pipelineQueue.Close()

	g.mu.Lock()
	for _, ar := range g.activeRequests {
		ar.CancelFunc()
	}
	g.mu.Unlock()
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
	userCount := 0
	for _, ar := range g.activeRequests {
		if ar.IsUser {
			userCount++
		}
	}
	g.mu.Unlock()

	return GateStats{
		UserQueueSize:      g.userQueue.Len(),
		PipelineQueueSize:  g.pipelineQueue.Len(),
		ActiveRequests:     activeCount,
		ActiveUserRequests: userCount,
	}
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
