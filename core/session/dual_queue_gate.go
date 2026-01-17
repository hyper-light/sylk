package session

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

var (
	ErrDualQueueGateClosed  = errors.New("dual queue gate is closed")
	ErrRequestCancelled     = errors.New("request was cancelled")
	ErrSubscriptionLimitHit = errors.New("subscription limit exceeded")
)

const (
	SignalRequestPreempted SignalType = "request_preempted"
)

type RequestType int

const (
	RequestTypeUser RequestType = iota
	RequestTypePipeline
)

type QueuedRequest struct {
	ID           string
	SessionID    string
	RequestType  RequestType
	Priority     int
	SpawnTime    time.Time
	Payload      any
	CancelFunc   context.CancelFunc
	ResponseChan chan *QueuedResponse
}

type QueuedResponse struct {
	Result any
	Error  error
}

type SessionQueueState struct {
	SessionID      string
	ActiveRequests int
	QueuedRequests int
	LastActivity   time.Time
}

type CrossSessionDualQueueGateConfig struct {
	SignalDispatcher    *CrossSessionSignalDispatcher
	SubscriptionTracker *GlobalSubscriptionTracker
	FairShare           *FairShareCalculator
}

type CrossSessionDualQueueGate struct {
	userQueue     []*QueuedRequest
	pipelineQueue []*QueuedRequest

	sessionStates map[string]*SessionQueueState

	signalDispatcher    *CrossSessionSignalDispatcher
	subscriptionTracker *GlobalSubscriptionTracker
	fairShare           *FairShareCalculator

	mu     sync.Mutex
	closed bool
}

func NewCrossSessionDualQueueGate(cfg CrossSessionDualQueueGateConfig) *CrossSessionDualQueueGate {
	return &CrossSessionDualQueueGate{
		userQueue:           make([]*QueuedRequest, 0),
		pipelineQueue:       make([]*QueuedRequest, 0),
		sessionStates:       make(map[string]*SessionQueueState),
		signalDispatcher:    cfg.SignalDispatcher,
		subscriptionTracker: cfg.SubscriptionTracker,
		fairShare:           cfg.FairShare,
	}
}

func (g *CrossSessionDualQueueGate) Submit(ctx context.Context, sessionID string, req *QueuedRequest) (*QueuedResponse, error) {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil, ErrDualQueueGateClosed
	}

	req.SessionID = sessionID
	req.SpawnTime = time.Now()
	req.ResponseChan = make(chan *QueuedResponse, 1)

	cancelCtx, cancel := context.WithCancel(ctx)
	req.CancelFunc = cancel

	g.ensureSessionState(sessionID)
	g.enqueueRequest(req)
	g.mu.Unlock()

	return g.waitForResponse(cancelCtx, req)
}

func (g *CrossSessionDualQueueGate) SubmitAsync(ctx context.Context, sessionID string, req *QueuedRequest) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.closed {
		return ErrDualQueueGateClosed
	}

	req.SessionID = sessionID
	req.SpawnTime = time.Now()
	req.ResponseChan = make(chan *QueuedResponse, 1)

	_, cancel := context.WithCancel(ctx)
	req.CancelFunc = cancel

	g.ensureSessionState(sessionID)
	g.enqueueRequest(req)

	return nil
}

func (g *CrossSessionDualQueueGate) ensureSessionState(sessionID string) {
	if g.sessionStates[sessionID] == nil {
		g.sessionStates[sessionID] = &SessionQueueState{
			SessionID:    sessionID,
			LastActivity: time.Now(),
		}
	}
	g.sessionStates[sessionID].LastActivity = time.Now()
}

func (g *CrossSessionDualQueueGate) enqueueRequest(req *QueuedRequest) {
	state := g.sessionStates[req.SessionID]
	state.QueuedRequests++

	if req.RequestType == RequestTypeUser {
		g.enqueueUserRequest(req)
	} else {
		g.enqueuePipelineRequest(req)
	}
}

func (g *CrossSessionDualQueueGate) enqueueUserRequest(req *QueuedRequest) {
	g.userQueue = append(g.userQueue, req)
	g.maybePreemptPipeline()
}

func (g *CrossSessionDualQueueGate) enqueuePipelineRequest(req *QueuedRequest) {
	g.pipelineQueue = g.insertByPriority(g.pipelineQueue, req)
}

func (g *CrossSessionDualQueueGate) insertByPriority(queue []*QueuedRequest, req *QueuedRequest) []*QueuedRequest {
	idx := g.findInsertPosition(queue, req)
	return g.insertAt(queue, idx, req)
}

func (g *CrossSessionDualQueueGate) findInsertPosition(queue []*QueuedRequest, req *QueuedRequest) int {
	for i, existing := range queue {
		if g.shouldInsertBefore(req, existing) {
			return i
		}
	}
	return len(queue)
}

func (g *CrossSessionDualQueueGate) shouldInsertBefore(newReq, existing *QueuedRequest) bool {
	if newReq.Priority != existing.Priority {
		return newReq.Priority > existing.Priority
	}
	return newReq.SpawnTime.Before(existing.SpawnTime)
}

func (g *CrossSessionDualQueueGate) insertAt(queue []*QueuedRequest, idx int, req *QueuedRequest) []*QueuedRequest {
	queue = append(queue, nil)
	copy(queue[idx+1:], queue[idx:])
	queue[idx] = req
	return queue
}

func (g *CrossSessionDualQueueGate) maybePreemptPipeline() {
	if len(g.pipelineQueue) == 0 {
		return
	}

	victim := g.findLowestPriorityPipeline()
	if victim == nil {
		return
	}

	g.preemptRequest(victim)
}

func (g *CrossSessionDualQueueGate) findLowestPriorityPipeline() *QueuedRequest {
	if len(g.pipelineQueue) == 0 {
		return nil
	}
	return g.pipelineQueue[len(g.pipelineQueue)-1]
}

func (g *CrossSessionDualQueueGate) preemptRequest(req *QueuedRequest) {
	req.CancelFunc()
	g.notifyPreemption(req)
}

func (g *CrossSessionDualQueueGate) notifyPreemption(req *QueuedRequest) {
	if g.signalDispatcher == nil {
		return
	}

	payload, _ := json.Marshal(map[string]string{
		"request_id": req.ID,
		"session_id": req.SessionID,
	})

	g.signalDispatcher.SendSignal(CrossSessionSignal{
		Type:      SignalRequestPreempted,
		ToSession: req.SessionID,
		Timestamp: time.Now(),
		Payload:   string(payload),
	})
}

func (g *CrossSessionDualQueueGate) waitForResponse(ctx context.Context, req *QueuedRequest) (*QueuedResponse, error) {
	select {
	case <-ctx.Done():
		g.removeRequest(req)
		return nil, ErrRequestCancelled
	case resp := <-req.ResponseChan:
		return resp, nil
	}
}

func (g *CrossSessionDualQueueGate) removeRequest(req *QueuedRequest) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.userQueue = g.removeFromQueue(g.userQueue, req.ID)
	g.pipelineQueue = g.removeFromQueue(g.pipelineQueue, req.ID)

	if state := g.sessionStates[req.SessionID]; state != nil {
		state.QueuedRequests = max(0, state.QueuedRequests-1)
	}
}

func (g *CrossSessionDualQueueGate) removeFromQueue(queue []*QueuedRequest, id string) []*QueuedRequest {
	for i, r := range queue {
		if r.ID == id {
			return append(queue[:i], queue[i+1:]...)
		}
	}
	return queue
}

func (g *CrossSessionDualQueueGate) ProcessNext() (*QueuedRequest, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.closed {
		return nil, false
	}

	if req := g.dequeueUser(); req != nil {
		return req, true
	}

	if req := g.dequeuePipeline(); req != nil {
		return req, true
	}

	return nil, false
}

func (g *CrossSessionDualQueueGate) dequeueUser() *QueuedRequest {
	if len(g.userQueue) == 0 {
		return nil
	}

	req := g.userQueue[0]
	g.userQueue = g.userQueue[1:]
	g.updateSessionState(req, true)
	return req
}

func (g *CrossSessionDualQueueGate) dequeuePipeline() *QueuedRequest {
	if len(g.pipelineQueue) == 0 {
		return nil
	}

	req := g.pipelineQueue[0]
	g.pipelineQueue = g.pipelineQueue[1:]
	g.updateSessionState(req, true)
	return req
}

func (g *CrossSessionDualQueueGate) updateSessionState(req *QueuedRequest, active bool) {
	state := g.sessionStates[req.SessionID]
	if state == nil {
		return
	}

	state.QueuedRequests = max(0, state.QueuedRequests-1)
	if active {
		state.ActiveRequests++
	}
}

func (g *CrossSessionDualQueueGate) CompleteRequest(req *QueuedRequest, result any, err error) {
	g.mu.Lock()
	if state := g.sessionStates[req.SessionID]; state != nil {
		state.ActiveRequests = max(0, state.ActiveRequests-1)
	}
	g.mu.Unlock()

	select {
	case req.ResponseChan <- &QueuedResponse{Result: result, Error: err}:
	default:
	}
}

func (g *CrossSessionDualQueueGate) GetSessionState(sessionID string) *SessionQueueState {
	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.sessionStates[sessionID]
	if state == nil {
		return nil
	}

	copy := *state
	return &copy
}

func (g *CrossSessionDualQueueGate) GetAllSessionStates() map[string]*SessionQueueState {
	g.mu.Lock()
	defer g.mu.Unlock()

	result := make(map[string]*SessionQueueState, len(g.sessionStates))
	for id, state := range g.sessionStates {
		copy := *state
		result[id] = &copy
	}
	return result
}

func (g *CrossSessionDualQueueGate) UserQueueLen() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.userQueue)
}

func (g *CrossSessionDualQueueGate) PipelineQueueLen() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.pipelineQueue)
}

func (g *CrossSessionDualQueueGate) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.closed {
		return ErrDualQueueGateClosed
	}
	g.closed = true

	g.cancelAllRequests(g.userQueue)
	g.cancelAllRequests(g.pipelineQueue)

	g.userQueue = nil
	g.pipelineQueue = nil
	g.sessionStates = nil

	return nil
}

func (g *CrossSessionDualQueueGate) cancelAllRequests(queue []*QueuedRequest) {
	for _, req := range queue {
		req.CancelFunc()
		close(req.ResponseChan)
	}
}
