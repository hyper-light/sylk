package tdd

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/engineer"
	inspShared "github.com/adalundhe/sylk/agents/inspector/shared"
	"github.com/adalundhe/sylk/agents/tester"
)

func TestPipelineBus_InspectorFeedbackRoundTrip(t *testing.T) {
	bus := NewPipelineBus()
	defer bus.Close()
	ctx := context.Background()

	fb := &InspectorFeedback{
		Criteria: &inspShared.InspectorCriteria{TaskID: "t1"},
	}
	if err := bus.SendInspectorFeedback(ctx, fb); err != nil {
		t.Fatal(err)
	}
	got, err := bus.RecvInspectorFeedback(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Criteria.TaskID != "t1" {
		t.Errorf("got TaskID %q, want %q", got.Criteria.TaskID, "t1")
	}
}

func TestPipelineBus_TesterFeedbackRoundTrip(t *testing.T) {
	bus := NewPipelineBus()
	defer bus.Close()
	ctx := context.Background()

	fb := &TesterFeedback{
		FailedTests: []string{"TestA"},
		Response:    &tester.TesterResponse{ID: "resp1"},
	}
	if err := bus.SendTesterFeedback(ctx, fb); err != nil {
		t.Fatal(err)
	}
	got, err := bus.RecvTesterFeedback(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Response.ID != "resp1" {
		t.Errorf("got ID %q, want %q", got.Response.ID, "resp1")
	}
	if len(got.FailedTests) != 1 || got.FailedTests[0] != "TestA" {
		t.Errorf("got FailedTests %v, want [TestA]", got.FailedTests)
	}
}

func TestPipelineBus_WorkerDoneRoundTrip(t *testing.T) {
	bus := NewPipelineBus()
	defer bus.Close()
	ctx := context.Background()

	r := &WorkerResult{ChangedFiles: []string{"main.go"}}
	if err := bus.SendWorkerDone(ctx, r); err != nil {
		t.Fatal(err)
	}
	got, err := bus.RecvWorkerDone(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ChangedFiles) != 1 || got.ChangedFiles[0] != "main.go" {
		t.Errorf("got ChangedFiles %v, want [main.go]", got.ChangedFiles)
	}
}

func TestPipelineBus_InspectorDoneRoundTrip(t *testing.T) {
	bus := NewPipelineBus()
	defer bus.Close()
	ctx := context.Background()

	r := &inspShared.InspectorResult{TaskID: "t2", Passed: true}
	if err := bus.SendInspectorDone(ctx, r); err != nil {
		t.Fatal(err)
	}
	got, err := bus.RecvInspectorDone(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Passed || got.TaskID != "t2" {
		t.Errorf("unexpected result: %+v", got)
	}
}

func TestPipelineBus_TesterDoneRoundTrip(t *testing.T) {
	bus := NewPipelineBus()
	defer bus.Close()
	ctx := context.Background()

	r := &tester.TesterResponse{ID: "tr1", Success: true}
	if err := bus.SendTesterDone(ctx, r); err != nil {
		t.Fatal(err)
	}
	got, err := bus.RecvTesterDone(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Success || got.ID != "tr1" {
		t.Errorf("unexpected result: %+v", got)
	}
}

func TestPipelineBus_UserMessageRoundTrip(t *testing.T) {
	bus := NewPipelineBus()
	defer bus.Close()
	ctx := context.Background()

	if err := bus.SendUserMessage(ctx, "cancel"); err != nil {
		t.Fatal(err)
	}
	got, err := bus.RecvUserMessage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != "cancel" {
		t.Errorf("got %v, want %q", got, "cancel")
	}
}

func TestPipelineBus_ContextCancellation_Recv(t *testing.T) {
	bus := NewPipelineBus()
	defer bus.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := bus.RecvInspectorFeedback(ctx)
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestPipelineBus_ContextCancellation_SendBlocked(t *testing.T) {
	bus := NewPipelineBus()
	defer bus.Close()

	ctx, cancel := context.WithCancel(context.Background())

	// Fill the buffer-1 channel so the next send blocks.
	if err := bus.SendInspectorFeedback(ctx, &InspectorFeedback{}); err != nil {
		t.Fatal(err)
	}

	cancel()

	// Second send should block and see the cancelled context.
	err := bus.SendInspectorFeedback(ctx, &InspectorFeedback{})
	if err != context.Canceled {
		t.Errorf("expected context.Canceled on blocked send, got %v", err)
	}
}

func TestPipelineBus_ContextTimeout(t *testing.T) {
	bus := NewPipelineBus()
	defer bus.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := bus.RecvWorkerDone(ctx)
	if err != context.DeadlineExceeded {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
}

func TestPipelineBus_CloseIdempotent(t *testing.T) {
	bus := NewPipelineBus()
	bus.Close()
	bus.Close() // must not panic
	if !bus.IsClosed() {
		t.Error("expected bus to be closed")
	}
}

func TestPipelineBus_SendOnClosedBus(t *testing.T) {
	bus := NewPipelineBus()
	bus.Close()

	err := bus.SendInspectorFeedback(context.Background(), &InspectorFeedback{})
	if err != ErrBusClosed {
		t.Errorf("expected ErrBusClosed, got %v", err)
	}
}

func TestPipelineBus_RecvOnClosedBus(t *testing.T) {
	bus := NewPipelineBus()
	bus.Close()

	_, err := bus.RecvInspectorFeedback(context.Background())
	if err != ErrBusClosed {
		t.Errorf("expected ErrBusClosed, got %v", err)
	}
}

func TestMergeWorkerResults(t *testing.T) {
	primary := &WorkerResult{
		TaskResult:   &engineer.TaskResult{TaskID: "t1", Success: true},
		ChangedFiles: []string{"a.go", "b.go"},
		WorkerType:   WorkerEngineer,
	}
	coResult := &WorkerResult{
		TaskResult:   &engineer.TaskResult{TaskID: "t1", Success: true},
		ChangedFiles: []string{"b.go", "c.go"},
		WorkerType:   WorkerDesigner,
	}

	merged := mergeWorkerResults(primary, []*WorkerResult{coResult})
	if merged.TaskResult.TaskID != "t1" {
		t.Errorf("got TaskID %q, want %q", merged.TaskResult.TaskID, "t1")
	}
	if merged.WorkerType != WorkerEngineer {
		t.Errorf("got WorkerType %q, want %q", merged.WorkerType, WorkerEngineer)
	}
	// Files should be deduplicated: a.go, b.go, c.go
	if len(merged.ChangedFiles) != 3 {
		t.Fatalf("got %d files, want 3: %v", len(merged.ChangedFiles), merged.ChangedFiles)
	}
	want := map[string]bool{"a.go": true, "b.go": true, "c.go": true}
	for _, f := range merged.ChangedFiles {
		if !want[f] {
			t.Errorf("unexpected file %q", f)
		}
	}
}

func TestMergeWorkerResults_NoCo(t *testing.T) {
	primary := &WorkerResult{
		TaskResult:   &engineer.TaskResult{TaskID: "t1"},
		ChangedFiles: []string{"x.go"},
		WorkerType:   WorkerEngineer,
	}
	merged := mergeWorkerResults(primary, nil)
	if merged != primary {
		t.Error("expected same pointer returned when no co-results")
	}

	merged = mergeWorkerResults(primary, []*WorkerResult{})
	if merged != primary {
		t.Error("expected same pointer returned when empty co-results")
	}
}

func TestMergeWorkerResults_NilPrimary(t *testing.T) {
	co := &WorkerResult{ChangedFiles: []string{"y.go"}}
	merged := mergeWorkerResults(nil, []*WorkerResult{co})
	if merged != nil {
		t.Error("expected nil when primary is nil")
	}
}

func TestPipelineBus_Buffer1NonBlocking(t *testing.T) {
	bus := NewPipelineBus()
	defer bus.Close()
	ctx := context.Background()

	// First send should not block (buffer-1).
	if err := bus.SendWorkerDone(ctx, &WorkerResult{}); err != nil {
		t.Fatal(err)
	}

	// Second send should block — test with a short timeout.
	ctx2, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	err := bus.SendWorkerDone(ctx2, &WorkerResult{})
	if err != context.DeadlineExceeded {
		t.Errorf("expected second send to block and timeout, got %v", err)
	}
}
