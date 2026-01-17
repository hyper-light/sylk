package llm

import (
	"container/heap"
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

var ErrQueueClosed = errors.New("queue is closed")

// LLMRequest represents a request to be processed by the LLM layer.
type LLMRequest struct {
	ID            string
	SessionID     string
	PipelineID    string
	TaskID        string
	AgentID       string
	AgentType     string
	Provider      string
	Model         string
	Messages      []Message
	Priority      RequestPriority
	CreatedAt     time.Time
	TokenEstimate int
}

// Message represents a single message in the conversation.
type Message struct {
	Role    string
	Content string
}

// NewLLMRequest creates a new LLMRequest with a generated UUID and current timestamp.
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
		AgentType:     agentType,
		Provider:      provider,
		Model:         model,
		Messages:      messages,
		Priority:      priority,
		CreatedAt:     time.Now(),
		TokenEstimate: tokenEstimate,
	}
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
	item := old[n-1]
	old[n-1] = nil
	*h = old[0 : n-1]
	return item
}

// PriorityQueue is a thread-safe priority queue for LLM requests.
// Higher priority requests are dequeued first.
// Within the same priority, requests are dequeued in FIFO order.
type PriorityQueue struct {
	mu     sync.Mutex
	heap   requestHeap
	cond   *sync.Cond
	closed bool
}

// NewPriorityQueue creates a new empty PriorityQueue.
func NewPriorityQueue() *PriorityQueue {
	pq := &PriorityQueue{
		heap: make(requestHeap, 0),
	}
	pq.cond = sync.NewCond(&pq.mu)
	heap.Init(&pq.heap)
	return pq
}

// Push adds a request to the queue.
func (pq *PriorityQueue) Push(req *LLMRequest) {
	pq.mu.Lock()
	defer pq.mu.Unlock()
	heap.Push(&pq.heap, req)
	pq.cond.Signal()
}

// Pop removes and returns the highest priority request.
// Blocks if the queue is empty until a request is available or the queue is closed.
// Returns ErrQueueClosed if the queue is closed while waiting.
func (pq *PriorityQueue) Pop(ctx context.Context) (*LLMRequest, error) {
	pq.mu.Lock()
	defer pq.mu.Unlock()

	done := pq.startContextWatcher(ctx)
	defer close(done)

	for pq.heap.Len() == 0 && !pq.closed {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		pq.cond.Wait()
	}

	if pq.closed && pq.heap.Len() == 0 {
		return nil, ErrQueueClosed
	}

	return heap.Pop(&pq.heap).(*LLMRequest), nil
}

func (pq *PriorityQueue) startContextWatcher(ctx context.Context) chan struct{} {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			pq.cond.Broadcast()
		case <-done:
		}
	}()
	return done
}

// Len returns the current number of requests in the queue.
func (pq *PriorityQueue) Len() int {
	pq.mu.Lock()
	defer pq.mu.Unlock()
	return pq.heap.Len()
}

// Close closes the queue and unblocks any waiting Pop calls.
func (pq *PriorityQueue) Close() {
	pq.mu.Lock()
	defer pq.mu.Unlock()
	pq.closed = true
	pq.cond.Broadcast()
}
