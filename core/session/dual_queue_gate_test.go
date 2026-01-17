package session

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestNewCrossSessionDualQueueGate(t *testing.T) {
	gate := NewCrossSessionDualQueueGate(CrossSessionDualQueueGateConfig{})

	if gate == nil {
		t.Fatal("expected non-nil gate")
	}
	if gate.userQueue == nil {
		t.Error("expected non-nil user queue")
	}
	if gate.pipelineQueue == nil {
		t.Error("expected non-nil pipeline queue")
	}
}

func TestCrossSessionDualQueueGate_SubmitUserRequest(t *testing.T) {
	gate := NewCrossSessionDualQueueGate(CrossSessionDualQueueGateConfig{})
	defer gate.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	req := &QueuedRequest{
		ID:          "req-1",
		RequestType: RequestTypeUser,
		Payload:     "test",
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		processedReq, ok := gate.ProcessNext()
		if ok {
			gate.CompleteRequest(processedReq, "result", nil)
		}
	}()

	resp, err := gate.Submit(ctx, "session-1", req)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	if resp.Result != "result" {
		t.Errorf("expected result 'result', got %v", resp.Result)
	}
}

func TestCrossSessionDualQueueGate_SubmitPipelineRequest(t *testing.T) {
	gate := NewCrossSessionDualQueueGate(CrossSessionDualQueueGateConfig{})
	defer gate.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	req := &QueuedRequest{
		ID:          "req-1",
		RequestType: RequestTypePipeline,
		Priority:    5,
		Payload:     "test",
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		processedReq, ok := gate.ProcessNext()
		if ok {
			gate.CompleteRequest(processedReq, "pipeline-result", nil)
		}
	}()

	resp, err := gate.Submit(ctx, "session-1", req)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	if resp.Result != "pipeline-result" {
		t.Errorf("expected result 'pipeline-result', got %v", resp.Result)
	}
}

func TestCrossSessionDualQueueGate_UserPriorityOverPipeline(t *testing.T) {
	gate := NewCrossSessionDualQueueGate(CrossSessionDualQueueGateConfig{})
	defer gate.Close()

	pipelineReq := &QueuedRequest{
		ID:          "pipeline-1",
		RequestType: RequestTypePipeline,
		Priority:    10,
	}
	userReq := &QueuedRequest{
		ID:          "user-1",
		RequestType: RequestTypeUser,
	}

	ctx := context.Background()

	go gate.Submit(ctx, "session-1", pipelineReq)
	time.Sleep(5 * time.Millisecond)

	go gate.Submit(ctx, "session-2", userReq)
	time.Sleep(5 * time.Millisecond)

	first, ok := gate.ProcessNext()
	if !ok {
		t.Fatal("expected to process a request")
	}

	if first.RequestType != RequestTypeUser {
		t.Error("expected user request to be processed first")
	}
}

func TestCrossSessionDualQueueGate_PipelinePriorityOrdering(t *testing.T) {
	gate := NewCrossSessionDualQueueGate(CrossSessionDualQueueGateConfig{})
	defer gate.Close()

	lowPriority := &QueuedRequest{
		ID:          "low",
		RequestType: RequestTypePipeline,
		Priority:    1,
	}
	highPriority := &QueuedRequest{
		ID:          "high",
		RequestType: RequestTypePipeline,
		Priority:    10,
	}

	ctx := context.Background()

	go gate.Submit(ctx, "session-1", lowPriority)
	time.Sleep(5 * time.Millisecond)

	go gate.Submit(ctx, "session-2", highPriority)
	time.Sleep(5 * time.Millisecond)

	first, _ := gate.ProcessNext()
	if first.ID != "high" {
		t.Errorf("expected high priority first, got %s", first.ID)
	}
}

func TestCrossSessionDualQueueGate_SpawnTimeOrdering(t *testing.T) {
	gate := NewCrossSessionDualQueueGate(CrossSessionDualQueueGateConfig{})
	defer gate.Close()

	ctx := context.Background()

	first := &QueuedRequest{
		ID:          "first",
		RequestType: RequestTypePipeline,
		Priority:    5,
	}
	second := &QueuedRequest{
		ID:          "second",
		RequestType: RequestTypePipeline,
		Priority:    5,
	}

	go gate.Submit(ctx, "session-1", first)
	time.Sleep(10 * time.Millisecond)

	go gate.Submit(ctx, "session-2", second)
	time.Sleep(10 * time.Millisecond)

	processed, _ := gate.ProcessNext()
	if processed.ID != "first" {
		t.Errorf("expected first spawned request, got %s", processed.ID)
	}
}

func TestCrossSessionDualQueueGate_SessionStateTracking(t *testing.T) {
	gate := NewCrossSessionDualQueueGate(CrossSessionDualQueueGateConfig{})
	defer gate.Close()

	ctx := context.Background()

	req := &QueuedRequest{
		ID:          "req-1",
		RequestType: RequestTypeUser,
	}

	go gate.Submit(ctx, "session-1", req)
	time.Sleep(10 * time.Millisecond)

	state := gate.GetSessionState("session-1")
	if state == nil {
		t.Fatal("expected session state")
	}
	if state.QueuedRequests != 1 {
		t.Errorf("expected 1 queued request, got %d", state.QueuedRequests)
	}

	gate.ProcessNext()

	state = gate.GetSessionState("session-1")
	if state.ActiveRequests != 1 {
		t.Errorf("expected 1 active request, got %d", state.ActiveRequests)
	}
}

func TestCrossSessionDualQueueGate_MultipleSessionStates(t *testing.T) {
	gate := NewCrossSessionDualQueueGate(CrossSessionDualQueueGateConfig{})
	defer gate.Close()

	ctx := context.Background()

	for i := 0; i < 3; i++ {
		req := &QueuedRequest{
			ID:          string(rune('A' + i)),
			RequestType: RequestTypeUser,
		}
		go gate.Submit(ctx, "session-"+string(rune('1'+i)), req)
	}

	time.Sleep(20 * time.Millisecond)

	states := gate.GetAllSessionStates()
	if len(states) != 3 {
		t.Errorf("expected 3 session states, got %d", len(states))
	}
}

func TestCrossSessionDualQueueGate_RequestCancellation(t *testing.T) {
	gate := NewCrossSessionDualQueueGate(CrossSessionDualQueueGateConfig{})
	defer gate.Close()

	ctx, cancel := context.WithCancel(context.Background())

	req := &QueuedRequest{
		ID:          "req-1",
		RequestType: RequestTypeUser,
	}

	var wg sync.WaitGroup
	wg.Add(1)

	var submitErr error
	go func() {
		defer wg.Done()
		_, submitErr = gate.Submit(ctx, "session-1", req)
	}()

	time.Sleep(10 * time.Millisecond)
	cancel()
	wg.Wait()

	if submitErr != ErrRequestCancelled {
		t.Errorf("expected ErrRequestCancelled, got %v", submitErr)
	}
}

func TestCrossSessionDualQueueGate_QueueLengths(t *testing.T) {
	gate := NewCrossSessionDualQueueGate(CrossSessionDualQueueGateConfig{})
	defer gate.Close()

	ctx := context.Background()

	var wg sync.WaitGroup
	wg.Add(8)

	for i := 0; i < 3; i++ {
		go func(n int) {
			defer wg.Done()
			gate.SubmitAsync(ctx, "s1", &QueuedRequest{
				ID:          "user-" + string(rune('A'+n)),
				RequestType: RequestTypeUser,
			})
		}(i)
	}
	for i := 0; i < 5; i++ {
		go func(n int) {
			defer wg.Done()
			gate.SubmitAsync(ctx, "s2", &QueuedRequest{
				ID:          "pipeline-" + string(rune('a'+n)),
				RequestType: RequestTypePipeline,
				Priority:    n,
			})
		}(i)
	}

	wg.Wait()
	time.Sleep(10 * time.Millisecond)

	if gate.UserQueueLen() != 3 {
		t.Errorf("expected 3 user requests, got %d", gate.UserQueueLen())
	}
	if gate.PipelineQueueLen() != 5 {
		t.Errorf("expected 5 pipeline requests, got %d", gate.PipelineQueueLen())
	}
}

func TestCrossSessionDualQueueGate_ClosedGate(t *testing.T) {
	gate := NewCrossSessionDualQueueGate(CrossSessionDualQueueGateConfig{})
	gate.Close()

	ctx := context.Background()
	_, err := gate.Submit(ctx, "session-1", &QueuedRequest{ID: "req"})

	if err != ErrDualQueueGateClosed {
		t.Errorf("expected ErrDualQueueGateClosed, got %v", err)
	}

	_, ok := gate.ProcessNext()
	if ok {
		t.Error("expected ProcessNext to return false after close")
	}

	err = gate.Close()
	if err != ErrDualQueueGateClosed {
		t.Errorf("expected ErrDualQueueGateClosed on double close, got %v", err)
	}
}

func TestCrossSessionDualQueueGate_CompleteRequestUpdatesState(t *testing.T) {
	gate := NewCrossSessionDualQueueGate(CrossSessionDualQueueGateConfig{})
	defer gate.Close()

	ctx := context.Background()
	req := &QueuedRequest{
		ID:          "req-1",
		RequestType: RequestTypeUser,
	}

	go gate.Submit(ctx, "session-1", req)
	time.Sleep(10 * time.Millisecond)

	processed, _ := gate.ProcessNext()
	gate.CompleteRequest(processed, "done", nil)

	state := gate.GetSessionState("session-1")
	if state.ActiveRequests != 0 {
		t.Errorf("expected 0 active requests after completion, got %d", state.ActiveRequests)
	}
}

func TestCrossSessionDualQueueGate_ConcurrentSubmit(t *testing.T) {
	gate := NewCrossSessionDualQueueGate(CrossSessionDualQueueGateConfig{})
	defer gate.Close()

	ctx := context.Background()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			req := &QueuedRequest{
				ID:          "req-" + string(rune(n)),
				RequestType: RequestType(n % 2),
				Priority:    n % 10,
			}
			gate.SubmitAsync(ctx, "session-"+string(rune('A'+n%5)), req)
		}(i)
	}

	wg.Wait()
	time.Sleep(10 * time.Millisecond)

	totalQueued := gate.UserQueueLen() + gate.PipelineQueueLen()
	if totalQueued != 100 {
		t.Errorf("expected 100 total queued, got %d", totalQueued)
	}
}

func TestCrossSessionDualQueueGate_ConcurrentProcessing(t *testing.T) {
	gate := NewCrossSessionDualQueueGate(CrossSessionDualQueueGateConfig{})
	defer gate.Close()

	ctx := context.Background()

	for i := 0; i < 50; i++ {
		gate.SubmitAsync(ctx, "s1", &QueuedRequest{
			ID:          "req-" + string(rune(i)),
			RequestType: RequestTypeUser,
		})
	}

	var wg sync.WaitGroup
	var processed int
	var mu sync.Mutex

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				req, ok := gate.ProcessNext()
				if !ok {
					return
				}
				mu.Lock()
				processed++
				mu.Unlock()
				gate.CompleteRequest(req, nil, nil)
			}
		}()
	}

	time.Sleep(50 * time.Millisecond)
	wg.Wait()

	if processed != 50 {
		t.Errorf("expected 50 processed, got %d", processed)
	}
}

func TestCrossSessionDualQueueGate_ProcessNextEmpty(t *testing.T) {
	gate := NewCrossSessionDualQueueGate(CrossSessionDualQueueGateConfig{})
	defer gate.Close()

	req, ok := gate.ProcessNext()
	if ok {
		t.Error("expected false for empty queue")
	}
	if req != nil {
		t.Error("expected nil request for empty queue")
	}
}

func TestCrossSessionDualQueueGate_GetSessionStateNonexistent(t *testing.T) {
	gate := NewCrossSessionDualQueueGate(CrossSessionDualQueueGateConfig{})
	defer gate.Close()

	state := gate.GetSessionState("nonexistent")
	if state != nil {
		t.Error("expected nil for nonexistent session")
	}
}

func TestCrossSessionDualQueueGate_CrossSessionPreemption(t *testing.T) {
	gate := NewCrossSessionDualQueueGate(CrossSessionDualQueueGateConfig{})
	defer gate.Close()

	ctx := context.Background()

	for i := 0; i < 5; i++ {
		go gate.Submit(ctx, "session-A", &QueuedRequest{
			ID:          "pipeline-" + string(rune('A'+i)),
			RequestType: RequestTypePipeline,
			Priority:    i,
		})
	}
	time.Sleep(10 * time.Millisecond)

	go gate.Submit(ctx, "session-B", &QueuedRequest{
		ID:          "user-1",
		RequestType: RequestTypeUser,
	})
	time.Sleep(10 * time.Millisecond)

	first, _ := gate.ProcessNext()
	if first.ID != "user-1" {
		t.Errorf("expected user request from session-B to preempt, got %s", first.ID)
	}
}
