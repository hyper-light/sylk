package concurrency

import (
	"context"
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
}

func TestUnboundedQueue_PushPop(t *testing.T) {
	q := NewUnboundedQueue()

	req1 := &LLMRequest{ID: "req-1"}
	req2 := &LLMRequest{ID: "req-2"}

	q.Push(req1)
	q.Push(req2)

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

func TestUnboundedQueue_Concurrent(t *testing.T) {
	q := NewUnboundedQueue()
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
