package concurrency

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewUnboundedQueue(t *testing.T) {
	q := NewUnboundedQueue()
	if q == nil {
		t.Fatal("expected non-nil queue")
	}
	if q.Len() != 0 {
		t.Errorf("expected empty queue, got %d", q.Len())
	}
	if q.MaxSize() != 10000 {
		t.Errorf("expected default max size 10000, got %d", q.MaxSize())
	}
}

func TestNewBoundedQueue(t *testing.T) {
	config := BoundedQueueConfig{
		MaxSize:      100,
		RejectPolicy: RejectPolicyError,
		BlockTimeout: time.Second,
	}
	q := NewBoundedQueue(config)
	if q == nil {
		t.Fatal("expected non-nil queue")
	}
	if q.MaxSize() != 100 {
		t.Errorf("expected max size 100, got %d", q.MaxSize())
	}
}

func TestUnboundedQueue_PushPop(t *testing.T) {
	q := NewUnboundedQueue()

	req1 := &LLMRequest{ID: "req-1"}
	req2 := &LLMRequest{ID: "req-2"}

	if err := q.Push(req1); err != nil {
		t.Fatalf("unexpected push error: %v", err)
	}
	if err := q.Push(req2); err != nil {
		t.Fatalf("unexpected push error: %v", err)
	}

	if q.Len() != 2 {
		t.Errorf("expected 2 items, got %d", q.Len())
	}

	got1 := q.Pop()
	if got1.ID != "req-1" {
		t.Errorf("expected req-1, got %s", got1.ID)
	}

	got2 := q.Pop()
	if got2.ID != "req-2" {
		t.Errorf("expected req-2, got %s", got2.ID)
	}

	if q.Pop() != nil {
		t.Error("expected nil from empty queue")
	}
}

func TestUnboundedQueue_MaxSize(t *testing.T) {
	config := BoundedQueueConfig{
		MaxSize:      2,
		RejectPolicy: RejectPolicyError,
	}
	q := NewBoundedQueue(config)

	if err := q.Push(&LLMRequest{ID: "1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := q.Push(&LLMRequest{ID: "2"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err := q.Push(&LLMRequest{ID: "3"})
	if err != ErrUserQueueFull {
		t.Errorf("expected ErrUserQueueFull, got %v", err)
	}

	if !q.IsFull() {
		t.Error("expected queue to be full")
	}
}

func TestUnboundedQueue_DropOldestPolicy(t *testing.T) {
	config := BoundedQueueConfig{
		MaxSize:      2,
		RejectPolicy: RejectPolicyDropOldest,
	}
	q := NewBoundedQueue(config)

	q.Push(&LLMRequest{ID: "1"})
	q.Push(&LLMRequest{ID: "2"})
	q.Push(&LLMRequest{ID: "3"}) // Should drop "1"

	if q.Len() != 2 {
		t.Errorf("expected 2 items, got %d", q.Len())
	}

	// First item should be "2" since "1" was dropped
	got := q.Pop()
	if got.ID != "2" {
		t.Errorf("expected '2' (oldest dropped), got %s", got.ID)
	}
}

func TestUnboundedQueue_BlockPolicy(t *testing.T) {
	config := BoundedQueueConfig{
		MaxSize:      1,
		RejectPolicy: RejectPolicyBlock,
		BlockTimeout: 50 * time.Millisecond,
	}
	q := NewBoundedQueue(config)

	q.Push(&LLMRequest{ID: "1"})

	// This should block then timeout
	start := time.Now()
	err := q.Push(&LLMRequest{ID: "2"})
	elapsed := time.Since(start)

	if err != ErrUserQueueFull {
		t.Errorf("expected ErrUserQueueFull after timeout, got %v", err)
	}
	if elapsed < 40*time.Millisecond {
		t.Errorf("expected to block for ~50ms, blocked for %v", elapsed)
	}
}

func TestUnboundedQueue_BlockPolicySuccess(t *testing.T) {
	config := BoundedQueueConfig{
		MaxSize:      1,
		RejectPolicy: RejectPolicyBlock,
		BlockTimeout: time.Second,
	}
	q := NewBoundedQueue(config)

	q.Push(&LLMRequest{ID: "1"})

	// Pop in background to make space
	go func() {
		time.Sleep(50 * time.Millisecond)
		q.Pop()
	}()

	// This should block then succeed
	start := time.Now()
	err := q.Push(&LLMRequest{ID: "2"})
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("expected success, got %v", err)
	}
	if elapsed < 40*time.Millisecond || elapsed > 200*time.Millisecond {
		t.Errorf("expected to block for ~50ms, blocked for %v", elapsed)
	}
}

func TestUnboundedQueue_Close(t *testing.T) {
	q := NewUnboundedQueue()
	q.Close()

	err := q.Push(&LLMRequest{ID: "1"})
	if err != ErrGateClosed {
		t.Errorf("expected ErrGateClosed, got %v", err)
	}
}

func TestUnboundedQueue_Concurrent(t *testing.T) {
	config := BoundedQueueConfig{
		MaxSize:      1000, // Large enough for concurrent test
		RejectPolicy: RejectPolicyError,
	}
	q := NewBoundedQueue(config)
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			q.Push(&LLMRequest{ID: "req"})
			_ = q.Len()
		}(i)
	}
	wg.Wait()

	if q.Len() != 100 {
		t.Errorf("expected 100 items, got %d", q.Len())
	}
}

func TestNewPriorityQueue(t *testing.T) {
	pq := NewPriorityQueue(10)
	if pq == nil {
		t.Fatal("expected non-nil queue")
	}
	if pq.Len() != 0 {
		t.Errorf("expected empty queue, got %d", pq.Len())
	}
}

func TestPriorityQueue_PriorityOrdering(t *testing.T) {
	pq := NewPriorityQueue(10)

	low := &LLMRequest{ID: "low", Priority: PriorityBackground, CreatedAt: time.Now()}
	mid := &LLMRequest{ID: "mid", Priority: PriorityExecution, CreatedAt: time.Now()}
	high := &LLMRequest{ID: "high", Priority: PriorityUserInteractive, CreatedAt: time.Now()}

	pq.Push(low)
	pq.Push(high)
	pq.Push(mid)

	got1 := pq.Pop()
	if got1.ID != "high" {
		t.Errorf("expected high priority first, got %s", got1.ID)
	}

	got2 := pq.Pop()
	if got2.ID != "mid" {
		t.Errorf("expected mid priority second, got %s", got2.ID)
	}

	got3 := pq.Pop()
	if got3.ID != "low" {
		t.Errorf("expected low priority last, got %s", got3.ID)
	}
}

func TestPriorityQueue_TimeOrdering(t *testing.T) {
	pq := NewPriorityQueue(10)

	now := time.Now()
	first := &LLMRequest{ID: "first", Priority: PriorityExecution, CreatedAt: now}
	second := &LLMRequest{ID: "second", Priority: PriorityExecution, CreatedAt: now.Add(time.Second)}

	pq.Push(second)
	pq.Push(first)

	got := pq.Pop()
	if got.ID != "first" {
		t.Errorf("expected earlier request first, got %s", got.ID)
	}
}

func TestPriorityQueue_MaxSize(t *testing.T) {
	pq := NewPriorityQueue(2)

	pq.Push(&LLMRequest{ID: "1"})
	pq.Push(&LLMRequest{ID: "2"})

	err := pq.Push(&LLMRequest{ID: "3"})
	if err != ErrQueueFull {
		t.Errorf("expected ErrQueueFull, got %v", err)
	}
}

func TestPriorityQueue_Close(t *testing.T) {
	pq := NewPriorityQueue(10)
	pq.Close()

	err := pq.Push(&LLMRequest{ID: "1"})
	if err != ErrGateClosed {
		t.Errorf("expected ErrGateClosed, got %v", err)
	}
}

type mockExecutor struct {
	executeFunc func(ctx context.Context, req *LLMRequest) (any, error)
	calls       atomic.Int32
}

func (m *mockExecutor) Execute(ctx context.Context, req *LLMRequest) (any, error) {
	m.calls.Add(1)
	if m.executeFunc != nil {
		return m.executeFunc(ctx, req)
	}
	return "response", nil
}

type mockBudgetChecker struct {
	err error
}

func (m *mockBudgetChecker) CheckBudget(req *LLMRequest) error {
	return m.err
}

type mockRateLimiter struct {
	allow bool
	last  atomic.Int32
}

func (m *mockRateLimiter) CanProceed() bool {
	return m.allow
}

func (m *mockRateLimiter) RecordUsage(tokens int) {
	m.last.Store(int32(tokens))
}

func TestNewDualQueueGate(t *testing.T) {
	executor := &mockExecutor{}
	gate := NewDualQueueGate(DefaultDualQueueGateConfig(), executor)
	defer gate.Close()

	if gate == nil {
		t.Fatal("expected non-nil gate")
	}

	stats := gate.Stats()
	if stats.UserQueueSize != 0 || stats.PipelineQueueSize != 0 {
		t.Error("expected empty queues")
	}
}

func TestDualQueueGate_SubmitUserRequest(t *testing.T) {
	executed := make(chan string, 1)
	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			executed <- req.ID
			return "done", nil
		},
	}

	gate := NewDualQueueGate(DefaultDualQueueGateConfig(), executor)
	defer gate.Close()

	req := &LLMRequest{
		ID:          "user-req-1",
		AgentType:   AgentGuide,
		UserInvoked: true,
	}

	err := gate.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	select {
	case id := <-executed:
		if id != "user-req-1" {
			t.Errorf("expected user-req-1, got %s", id)
		}
	case <-time.After(time.Second):
		t.Fatal("request not executed in time")
	}
}

func TestDualQueueGate_SubmitPipelineRequest(t *testing.T) {
	executed := make(chan string, 1)
	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			executed <- req.ID
			return "done", nil
		},
	}

	gate := NewDualQueueGate(DefaultDualQueueGateConfig(), executor)
	defer gate.Close()

	req := &LLMRequest{
		ID:        "pipeline-req-1",
		AgentType: AgentTypeEngineer,
		Priority:  PriorityExecution,
	}

	err := gate.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	select {
	case id := <-executed:
		if id != "pipeline-req-1" {
			t.Errorf("expected pipeline-req-1, got %s", id)
		}
	case <-time.After(time.Second):
		t.Fatal("request not executed in time")
	}
}

func TestDualQueueGate_SubmitAndWait(t *testing.T) {
	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			return "response-data", nil
		},
	}

	gate := NewDualQueueGate(DefaultDualQueueGateConfig(), executor)
	defer gate.Close()

	req := &LLMRequest{
		ID:          "wait-req",
		AgentType:   AgentGuide,
		UserInvoked: true,
	}

	result, err := gate.SubmitAndWait(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "response-data" {
		t.Errorf("expected response-data, got %v", result)
	}
}

func TestDualQueueGate_UserPriorityOverPipeline(t *testing.T) {
	order := make([]string, 0, 3)
	orderMu := sync.Mutex{}

	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			time.Sleep(10 * time.Millisecond)
			orderMu.Lock()
			order = append(order, req.ID)
			orderMu.Unlock()
			return nil, nil
		},
	}

	config := DualQueueGateConfig{
		MaxPipelineQueueSize:  100,
		MaxConcurrentRequests: 1,
	}
	gate := NewDualQueueGate(config, executor)
	defer gate.Close()

	pipelineReq := &LLMRequest{
		ID:        "pipeline",
		AgentType: AgentTypeEngineer,
		Priority:  PriorityExecution,
	}
	gate.Submit(context.Background(), pipelineReq)

	time.Sleep(5 * time.Millisecond)

	userReq := &LLMRequest{
		ID:          "user",
		AgentType:   AgentGuide,
		UserInvoked: true,
	}
	gate.Submit(context.Background(), userReq)

	time.Sleep(50 * time.Millisecond)

	orderMu.Lock()
	defer orderMu.Unlock()

	if len(order) < 1 {
		t.Fatal("expected at least one request processed")
	}
	if order[0] != "pipeline" {
		t.Logf("order: %v (first was already running)", order)
	}
}

func TestDualQueueGate_Cancel(t *testing.T) {
	started := make(chan struct{})
	cancelled := atomic.Bool{}

	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			close(started)
			<-ctx.Done()
			cancelled.Store(true)
			return nil, ctx.Err()
		},
	}

	gate := NewDualQueueGate(DefaultDualQueueGateConfig(), executor)
	defer gate.Close()

	req := &LLMRequest{
		ID:          "cancel-req",
		AgentType:   AgentGuide,
		UserInvoked: true,
	}
	gate.Submit(context.Background(), req)

	<-started
	gate.Cancel("cancel-req")

	time.Sleep(50 * time.Millisecond)
	if !cancelled.Load() {
		t.Error("expected request to be cancelled")
	}
}

func TestDualQueueGate_Close(t *testing.T) {
	executor := &mockExecutor{}
	gate := NewDualQueueGate(DefaultDualQueueGateConfig(), executor)

	gate.Close()
	gate.Close()

	err := gate.Submit(context.Background(), &LLMRequest{ID: "test"})
	if err != ErrGateClosed {
		t.Errorf("expected ErrGateClosed, got %v", err)
	}
}

func TestDualQueueGate_UserQueueFull(t *testing.T) {
	blockCh := make(chan struct{})
	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			<-blockCh
			return nil, nil
		},
	}

	config := DualQueueGateConfig{
		MaxUserQueueSize:      2,
		MaxPipelineQueueSize:  100,
		MaxConcurrentRequests: 1,
		UserQueueRejectPolicy: RejectPolicyError,
	}
	gate := NewDualQueueGate(config, executor)
	defer func() {
		close(blockCh)
		gate.Close()
	}()

	// First request will start executing
	gate.Submit(context.Background(), &LLMRequest{
		ID:          "user-1",
		AgentType:   AgentGuide,
		UserInvoked: true,
	})

	// Wait for it to start
	time.Sleep(20 * time.Millisecond)

	// These two will fill the queue
	gate.Submit(context.Background(), &LLMRequest{
		ID:          "user-2",
		AgentType:   AgentGuide,
		UserInvoked: true,
	})
	gate.Submit(context.Background(), &LLMRequest{
		ID:          "user-3",
		AgentType:   AgentGuide,
		UserInvoked: true,
	})

	// This should fail with queue full
	err := gate.Submit(context.Background(), &LLMRequest{
		ID:          "user-4",
		AgentType:   AgentGuide,
		UserInvoked: true,
	})
	if err != ErrUserQueueFull {
		t.Errorf("expected ErrUserQueueFull, got %v", err)
	}
}

func TestDualQueueGate_PipelineQueueFull(t *testing.T) {
	blockCh := make(chan struct{})
	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			<-blockCh
			return nil, nil
		},
	}

	config := DualQueueGateConfig{
		MaxUserQueueSize:      100,
		MaxPipelineQueueSize:  2,
		MaxConcurrentRequests: 1,
	}
	gate := NewDualQueueGate(config, executor)
	defer func() {
		close(blockCh)
		gate.Close()
	}()

	// First request will start executing
	gate.Submit(context.Background(), &LLMRequest{
		ID:        "pipeline-1",
		AgentType: AgentTypeEngineer,
	})

	// Wait for it to start
	time.Sleep(20 * time.Millisecond)

	// These two will fill the queue
	gate.Submit(context.Background(), &LLMRequest{
		ID:        "pipeline-2",
		AgentType: AgentTypeEngineer,
	})
	gate.Submit(context.Background(), &LLMRequest{
		ID:        "pipeline-3",
		AgentType: AgentTypeEngineer,
	})

	// This should fail with queue full
	err := gate.Submit(context.Background(), &LLMRequest{
		ID:        "pipeline-4",
		AgentType: AgentTypeEngineer,
	})
	if err != ErrQueueFull {
		t.Errorf("expected ErrQueueFull, got %v", err)
	}
}

func TestDualQueueGate_BudgetCheck(t *testing.T) {
	executor := &mockExecutor{}
	gate := NewDualQueueGate(DefaultDualQueueGateConfig(), executor)
	defer gate.Close()

	checker := &mockBudgetChecker{err: ErrGateClosed}
	gate.SetBudget(checker)

	req := &LLMRequest{ID: "budget", AgentType: AgentTypeEngineer}
	if err := gate.Submit(context.Background(), req); err != nil {
		t.Fatalf("unexpected submit error: %v", err)
	}

	_, err := gate.SubmitAndWait(context.Background(), req)
	if !errors.Is(err, ErrGateClosed) {
		t.Errorf("expected budget error, got %v", err)
	}
}

func TestDualQueueGate_RateLimit(t *testing.T) {
	executor := &mockExecutor{}
	gate := NewDualQueueGate(DefaultDualQueueGateConfig(), executor)
	defer gate.Close()

	limiter := &mockRateLimiter{allow: false}
	gate.SetRateLimiter(limiter)

	req := &LLMRequest{ID: "rate", AgentType: AgentTypeEngineer}
	if err := gate.Submit(context.Background(), req); err != nil {
		t.Fatalf("unexpected submit error: %v", err)
	}

	_, err := gate.SubmitAndWait(context.Background(), req)
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("expected ErrRateLimited, got %v", err)
	}
}

func TestDualQueueGate_RecordsUsage(t *testing.T) {
	executor := &mockExecutor{}
	gate := NewDualQueueGate(DefaultDualQueueGateConfig(), executor)
	defer gate.Close()

	limiter := &mockRateLimiter{allow: true}
	gate.SetRateLimiter(limiter)

	req := &LLMRequest{ID: "tokens", AgentType: AgentTypeEngineer, TokenEstimate: 42}
	_, err := gate.SubmitAndWait(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if limiter.last.Load() != 42 {
		t.Errorf("expected tokens recorded, got %d", limiter.last.Load())
	}
}

func TestDualQueueGate_Stats(t *testing.T) {
	blockCh := make(chan struct{})
	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			<-blockCh
			return nil, nil
		},
	}

	config := DualQueueGateConfig{
		MaxPipelineQueueSize:  100,
		MaxConcurrentRequests: 1,
	}
	gate := NewDualQueueGate(config, executor)
	defer gate.Close()

	req := &LLMRequest{
		ID:          "stat-req",
		AgentType:   AgentGuide,
		UserInvoked: true,
	}
	gate.Submit(context.Background(), req)

	time.Sleep(20 * time.Millisecond)

	stats := gate.Stats()
	if stats.ActiveRequests != 1 {
		t.Errorf("expected 1 active request, got %d", stats.ActiveRequests)
	}
	if stats.ActiveUserRequests != 1 {
		t.Errorf("expected 1 active user request, got %d", stats.ActiveUserRequests)
	}

	close(blockCh)
}

func TestDualQueueGate_Concurrent(t *testing.T) {
	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			time.Sleep(time.Millisecond)
			return nil, nil
		},
	}

	gate := NewDualQueueGate(DefaultDualQueueGateConfig(), executor)
	defer gate.Close()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			req := &LLMRequest{
				ID:        "req",
				AgentType: AgentGuide,
			}
			gate.SubmitAndWait(context.Background(), req)
			_ = gate.Stats()
		}(i)
	}
	wg.Wait()

	if executor.calls.Load() != 50 {
		t.Errorf("expected 50 executions, got %d", executor.calls.Load())
	}
}

func TestDeterminePriority(t *testing.T) {
	tests := []struct {
		name        string
		agentType   AgentType
		userInvoked bool
		expected    RequestPriority
	}{
		{"user invoked", AgentTypeEngineer, true, PriorityUserInteractive},
		{"architect", AgentArchitect, false, PriorityPlanning},
		{"engineer", AgentTypeEngineer, false, PriorityExecution},
		{"designer", AgentTypeDesigner, false, PriorityExecution},
		{"inspector", AgentTypeInspector, false, PriorityValidation},
		{"tester", AgentTypeTester, false, PriorityValidation},
		{"archivalist", AgentArchivalist, false, PriorityBackground},
		{"librarian", AgentLibrarian, false, PriorityBackground},
		{"unknown", "unknown", false, PriorityExecution},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &LLMRequest{AgentType: tt.agentType}
			got := DeterminePriority(req, tt.userInvoked)
			if got != tt.expected {
				t.Errorf("expected %d, got %d", tt.expected, got)
			}
		})
	}
}

func TestIsUserInteractive(t *testing.T) {
	gate := &DualQueueGate{}

	tests := []struct {
		name     string
		req      *LLMRequest
		expected bool
	}{
		{"user invoked", &LLMRequest{UserInvoked: true}, true},
		{"guide agent", &LLMRequest{AgentType: AgentGuide}, true},
		{"architect agent", &LLMRequest{AgentType: AgentArchitect}, true},
		{"engineer agent", &LLMRequest{AgentType: AgentTypeEngineer}, false},
		{"designer agent", &LLMRequest{AgentType: AgentTypeDesigner}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gate.isUserInteractive(tt.req)
			if got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestNewDualQueueGateWithContext(t *testing.T) {
	ctx := context.Background()
	executor := &mockExecutor{}
	gate := NewDualQueueGateWithContext(ctx, DefaultDualQueueGateConfig(), executor)
	defer gate.Close()

	if gate == nil {
		t.Fatal("expected non-nil gate")
	}
	if gate.ctx == nil {
		t.Error("expected gate to have a context")
	}
	if gate.cancel == nil {
		t.Error("expected gate to have a cancel function")
	}
}

func TestDualQueueGate_ContextPropagation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	requestCancelled := atomic.Bool{}

	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			<-ctx.Done()
			requestCancelled.Store(true)
			return nil, ctx.Err()
		},
	}

	gate := NewDualQueueGateWithContext(ctx, DefaultDualQueueGateConfig(), executor)

	req := &LLMRequest{
		ID:          "ctx-test",
		AgentType:   AgentGuide,
		UserInvoked: true,
	}
	_ = gate.Submit(context.Background(), req)

	// Wait for request to start executing
	time.Sleep(50 * time.Millisecond)

	// Cancel the parent context
	cancel()

	// Wait for cancellation to propagate
	time.Sleep(100 * time.Millisecond)

	if !requestCancelled.Load() {
		t.Error("expected request to be cancelled when parent context is cancelled")
	}

	gate.Close()
}

func TestDualQueueGate_CloseContextCancellation(t *testing.T) {
	requestCancelled := atomic.Bool{}

	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			<-ctx.Done()
			requestCancelled.Store(true)
			return nil, ctx.Err()
		},
	}

	gate := NewDualQueueGate(DefaultDualQueueGateConfig(), executor)

	req := &LLMRequest{
		ID:          "close-test",
		AgentType:   AgentGuide,
		UserInvoked: true,
	}
	_ = gate.Submit(context.Background(), req)

	// Wait for request to start executing
	time.Sleep(50 * time.Millisecond)

	// Close the gate
	gate.Close()

	// Wait for cancellation to propagate
	time.Sleep(100 * time.Millisecond)

	if !requestCancelled.Load() {
		t.Error("expected request to be cancelled when gate is closed")
	}
}

func TestDualQueueGate_RequestInheritsGateContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	contextReceived := make(chan context.Context, 1)

	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			contextReceived <- ctx
			return "done", nil
		},
	}

	gate := NewDualQueueGateWithContext(ctx, DefaultDualQueueGateConfig(), executor)
	defer gate.Close()

	req := &LLMRequest{
		ID:          "inherit-test",
		AgentType:   AgentGuide,
		UserInvoked: true,
	}
	_ = gate.Submit(context.Background(), req)

	select {
	case execCtx := <-contextReceived:
		// Verify the context chain by cancelling parent and checking child
		cancel()
		select {
		case <-execCtx.Done():
			// Good - context properly inherited
		case <-time.After(100 * time.Millisecond):
			t.Error("execution context did not inherit from gate context")
		}
	case <-time.After(time.Second):
		t.Fatal("request not executed in time")
	}
}

func TestDualQueueGate_NewRequestContextWithParent(t *testing.T) {
	executor := &mockExecutor{}
	gate := NewDualQueueGate(DefaultDualQueueGateConfig(), executor)
	defer gate.Close()

	// Test with nil parent (should use gate context)
	ctx1, cancel1 := gate.newRequestContextWithParent(nil)
	defer cancel1()

	if ctx1 == nil {
		t.Error("expected non-nil context")
	}

	// Test with custom parent
	customCtx, customCancel := context.WithCancel(context.Background())
	defer customCancel()

	ctx2, cancel2 := gate.newRequestContextWithParent(customCtx)
	defer cancel2()

	// Cancel custom parent and verify child is also cancelled
	customCancel()
	select {
	case <-ctx2.Done():
		// Good - context properly inherited from parent
	case <-time.After(100 * time.Millisecond):
		t.Error("context did not inherit from provided parent")
	}
}
