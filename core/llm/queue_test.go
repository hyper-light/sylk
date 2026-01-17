package llm

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestNewPriorityQueue(t *testing.T) {
	pq := NewPriorityQueue()
	if pq == nil {
		t.Fatal("NewPriorityQueue() returned nil")
	}
	if pq.Len() != 0 {
		t.Errorf("New queue should be empty, got len %d", pq.Len())
	}
}

func TestPushPop(t *testing.T) {
	pq := NewPriorityQueue()
	ctx := context.Background()

	req := &LLMRequest{
		ID:       "test-1",
		Priority: PriorityExecution,
	}

	pq.Push(req)
	if pq.Len() != 1 {
		t.Errorf("Len() = %d, want 1", pq.Len())
	}

	got, err := pq.Pop(ctx)
	if err != nil {
		t.Errorf("Pop() error = %v", err)
	}
	if got.ID != "test-1" {
		t.Errorf("Pop() returned request with ID %v, want test-1", got.ID)
	}
	if pq.Len() != 0 {
		t.Errorf("Len() after pop = %d, want 0", pq.Len())
	}
}

func TestPriorityOrdering(t *testing.T) {
	pq := NewPriorityQueue()
	ctx := context.Background()

	// Push in reverse priority order
	requests := []*LLMRequest{
		{ID: "background", Priority: PriorityBackground, CreatedAt: time.Now()},
		{ID: "validation", Priority: PriorityValidation, CreatedAt: time.Now()},
		{ID: "execution", Priority: PriorityExecution, CreatedAt: time.Now()},
		{ID: "planning", Priority: PriorityPlanning, CreatedAt: time.Now()},
		{ID: "user", Priority: PriorityUserInteractive, CreatedAt: time.Now()},
	}

	for _, req := range requests {
		pq.Push(req)
	}

	// Pop should return in priority order (highest first)
	expectedOrder := []string{"user", "planning", "execution", "validation", "background"}

	for _, expectedID := range expectedOrder {
		got, err := pq.Pop(ctx)
		if err != nil {
			t.Errorf("Pop() error = %v", err)
		}
		if got.ID != expectedID {
			t.Errorf("Pop() = %v, want %v", got.ID, expectedID)
		}
	}
}

func TestFIFOWithinSamePriority(t *testing.T) {
	pq := NewPriorityQueue()
	ctx := context.Background()

	// Push multiple requests with same priority but different timestamps
	now := time.Now()
	requests := []*LLMRequest{
		{ID: "first", Priority: PriorityExecution, CreatedAt: now},
		{ID: "second", Priority: PriorityExecution, CreatedAt: now.Add(time.Millisecond)},
		{ID: "third", Priority: PriorityExecution, CreatedAt: now.Add(2 * time.Millisecond)},
	}

	for _, req := range requests {
		pq.Push(req)
	}

	// Should pop in FIFO order (earliest first)
	expectedOrder := []string{"first", "second", "third"}

	for _, expectedID := range expectedOrder {
		got, err := pq.Pop(ctx)
		if err != nil {
			t.Errorf("Pop() error = %v", err)
		}
		if got.ID != expectedID {
			t.Errorf("Pop() = %v, want %v", got.ID, expectedID)
		}
	}
}

func TestPopBlocksUntilAvailable(t *testing.T) {
	pq := NewPriorityQueue()
	ctx := context.Background()

	var gotRequest *LLMRequest
	var popErr error
	done := make(chan struct{})

	// Start a goroutine that will block on Pop
	go func() {
		gotRequest, popErr = pq.Pop(ctx)
		close(done)
	}()

	// Give the goroutine time to start blocking
	time.Sleep(50 * time.Millisecond)

	// Push a request
	req := &LLMRequest{ID: "delayed", Priority: PriorityExecution}
	pq.Push(req)

	// Wait for Pop to complete
	select {
	case <-done:
		if popErr != nil {
			t.Errorf("Pop() error = %v", popErr)
		}
		if gotRequest.ID != "delayed" {
			t.Errorf("Pop() = %v, want delayed", gotRequest.ID)
		}
	case <-time.After(time.Second):
		t.Error("Pop() did not unblock after Push")
	}
}

func TestCloseUnblocksPop(t *testing.T) {
	pq := NewPriorityQueue()
	ctx := context.Background()

	var popErr error
	done := make(chan struct{})

	// Start a goroutine that will block on Pop
	go func() {
		_, popErr = pq.Pop(ctx)
		close(done)
	}()

	// Give the goroutine time to start blocking
	time.Sleep(50 * time.Millisecond)

	// Close the queue
	pq.Close()

	// Wait for Pop to return
	select {
	case <-done:
		if popErr != ErrQueueClosed {
			t.Errorf("Pop() error = %v, want ErrQueueClosed", popErr)
		}
	case <-time.After(time.Second):
		t.Error("Pop() did not unblock after Close")
	}
}

func TestPopContextCancellation(t *testing.T) {
	pq := NewPriorityQueue()
	ctx, cancel := context.WithCancel(context.Background())

	var popErr error
	done := make(chan struct{})

	go func() {
		_, popErr = pq.Pop(ctx)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		if popErr != context.Canceled {
			t.Errorf("Pop() error = %v, want context.Canceled", popErr)
		}
	case <-time.After(time.Second):
		t.Error("Pop() did not unblock after context cancellation")
	}
}

func TestPopContextTimeout(t *testing.T) {
	pq := NewPriorityQueue()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := pq.Pop(ctx)
	if err != context.DeadlineExceeded {
		t.Errorf("Pop() error = %v, want context.DeadlineExceeded", err)
	}
}

func TestConcurrentPushPop(t *testing.T) {
	pq := NewPriorityQueue()
	ctx := context.Background()
	var wg sync.WaitGroup

	numPushers := 10
	requestsPerPusher := 100

	// Start pushers
	for i := 0; i < numPushers; i++ {
		wg.Add(1)
		go func(pusherID int) {
			defer wg.Done()
			for j := 0; j < requestsPerPusher; j++ {
				priority := RequestPriority((j % 5) * 20) // Varies priority
				req := &LLMRequest{
					ID:        string(rune('a'+pusherID)) + string(rune('0'+j%10)),
					Priority:  priority,
					CreatedAt: time.Now(),
				}
				pq.Push(req)
			}
		}(i)
	}

	// Start poppers
	totalRequests := numPushers * requestsPerPusher
	popped := make(chan *LLMRequest, totalRequests)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				req, err := pq.Pop(ctx)
				if err == ErrQueueClosed {
					return
				}
				if err != nil {
					continue
				}
				popped <- req
			}
		}()
	}

	// Wait for all pushers to finish
	time.Sleep(500 * time.Millisecond)

	// Wait until queue is drained
	for pq.Len() > 0 {
		time.Sleep(10 * time.Millisecond)
	}

	pq.Close()
	wg.Wait()
	close(popped)

	count := 0
	for range popped {
		count++
	}

	if count != totalRequests {
		t.Errorf("Popped %d requests, want %d", count, totalRequests)
	}
}

func TestStressTest1000Requests(t *testing.T) {
	pq := NewPriorityQueue()
	ctx := context.Background()

	numRequests := 1000

	// Push 1000 requests with varying priorities
	for i := 0; i < numRequests; i++ {
		priority := RequestPriority((i % 5) * 20)
		req := &LLMRequest{
			ID:        string(rune('0' + i%10)),
			Priority:  priority,
			CreatedAt: time.Now().Add(time.Duration(i) * time.Microsecond),
		}
		pq.Push(req)
	}

	if pq.Len() != numRequests {
		t.Errorf("Len() = %d, want %d", pq.Len(), numRequests)
	}

	// Pop all and verify priority ordering
	var lastPriority RequestPriority = 1000
	var lastTime time.Time
	var sameCount int

	for i := 0; i < numRequests; i++ {
		req, err := pq.Pop(ctx)
		if err != nil {
			t.Fatalf("Pop() error at %d: %v", i, err)
		}

		if req.Priority > lastPriority {
			t.Errorf("Pop %d: priority %d > previous %d (should be decreasing)", i, req.Priority, lastPriority)
		}

		// When priority is the same, check FIFO
		if req.Priority == lastPriority {
			sameCount++
			if !lastTime.IsZero() && req.CreatedAt.Before(lastTime) {
				t.Errorf("Pop %d: FIFO violated within same priority", i)
			}
		} else {
			sameCount = 0
		}

		lastPriority = req.Priority
		lastTime = req.CreatedAt
	}

	if pq.Len() != 0 {
		t.Errorf("Queue should be empty after popping all, got len %d", pq.Len())
	}
}

func TestNewLLMRequest(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there"},
	}

	req := NewLLMRequest(
		"session-1",
		"pipeline-1",
		"task-1",
		"agent-1",
		"engineer",
		"anthropic",
		"claude-3-opus",
		messages,
		PriorityExecution,
		1500,
	)

	if req.ID == "" {
		t.Error("ID should be generated")
	}
	if req.SessionID != "session-1" {
		t.Errorf("SessionID = %v, want session-1", req.SessionID)
	}
	if req.PipelineID != "pipeline-1" {
		t.Errorf("PipelineID = %v, want pipeline-1", req.PipelineID)
	}
	if req.TaskID != "task-1" {
		t.Errorf("TaskID = %v, want task-1", req.TaskID)
	}
	if req.AgentID != "agent-1" {
		t.Errorf("AgentID = %v, want agent-1", req.AgentID)
	}
	if req.AgentType != "engineer" {
		t.Errorf("AgentType = %v, want engineer", req.AgentType)
	}
	if req.Provider != "anthropic" {
		t.Errorf("Provider = %v, want anthropic", req.Provider)
	}
	if req.Model != "claude-3-opus" {
		t.Errorf("Model = %v, want claude-3-opus", req.Model)
	}
	if len(req.Messages) != 2 {
		t.Errorf("Messages length = %d, want 2", len(req.Messages))
	}
	if req.Priority != PriorityExecution {
		t.Errorf("Priority = %v, want PriorityExecution", req.Priority)
	}
	if req.TokenEstimate != 1500 {
		t.Errorf("TokenEstimate = %v, want 1500", req.TokenEstimate)
	}
	if req.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
}

func TestPopReturnsRemainingOnClose(t *testing.T) {
	pq := NewPriorityQueue()
	ctx := context.Background()

	// Push some requests
	pq.Push(&LLMRequest{ID: "1", Priority: PriorityExecution, CreatedAt: time.Now()})
	pq.Push(&LLMRequest{ID: "2", Priority: PriorityExecution, CreatedAt: time.Now()})

	// Pop one
	req, _ := pq.Pop(ctx)
	if req == nil {
		t.Fatal("Expected to get a request")
	}

	// Close while one remains
	pq.Close()

	// Should still be able to get the remaining one
	req, err := pq.Pop(ctx)
	if err != nil {
		t.Errorf("Should be able to pop remaining request, got error: %v", err)
	}
	if req == nil {
		t.Error("Expected to get remaining request")
	}

	// Now should return ErrQueueClosed
	_, err = pq.Pop(ctx)
	if err != ErrQueueClosed {
		t.Errorf("Pop() on closed empty queue should return ErrQueueClosed, got %v", err)
	}
}

func TestMultipleCloseIsSafe(t *testing.T) {
	pq := NewPriorityQueue()

	// Multiple closes should not panic
	pq.Close()
	pq.Close()
	pq.Close()
}

func TestPushAfterClose(t *testing.T) {
	pq := NewPriorityQueue()
	pq.Close()

	// Push after close - should still work (design choice, queue accepts but won't process)
	pq.Push(&LLMRequest{ID: "late", Priority: PriorityExecution})

	// The request is added but can be retrieved since queue has items
	ctx := context.Background()
	req, err := pq.Pop(ctx)
	if err != nil && err != ErrQueueClosed {
		// Either getting the item or getting closed error is acceptable
		t.Logf("Got error: %v", err)
	}
	if req != nil && req.ID != "late" {
		t.Errorf("Got wrong request: %v", req.ID)
	}
}
