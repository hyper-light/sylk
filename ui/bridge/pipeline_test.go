package bridge

import (
	"sync"
	"testing"

	"github.com/adalundhe/sylk/core/pipeline/taskstate"
	"github.com/adalundhe/sylk/core/pipeline/variants"
)

func TestToTaskPipelineStateMsg_UsesCanonicalTaskPipelineIdentity(t *testing.T) {
	msg := toTaskPipelineStateMsg(taskstate.Event{
		PipelineID: "task_auth_checkout",
		TaskID:     "task_auth_checkout",
		TaskLabel:  "auth-checkout",
		Status:     taskstate.StatusCreatingTests,
		WorkerType: "tester-pipeline",
	})

	if msg.PipelineID != "task_auth_checkout" {
		t.Fatalf("PipelineID = %q, want task_auth_checkout", msg.PipelineID)
	}
	if msg.TaskID != "task_auth_checkout" {
		t.Fatalf("TaskID = %q, want task_auth_checkout", msg.TaskID)
	}
	if msg.TaskLabel != "auth-checkout" {
		t.Fatalf("TaskLabel = %q, want auth-checkout", msg.TaskLabel)
	}
	if msg.Status != "creating_tests" {
		t.Fatalf("Status = %q, want creating_tests", msg.Status)
	}
	if msg.WorkerType != "tester-pipeline" {
		t.Fatalf("WorkerType = %q, want tester-pipeline", msg.WorkerType)
	}
}

// TestPipelineBridge_DropListener closes UI-11: when an inbound event cannot
// be enqueued because the bridge channel is full, the installed
// DropListener fires with kind + identifier and the cumulative counter
// increments. We target the bridge directly (not via Start) to avoid
// needing a live Bubble Tea program in the test.
func TestPipelineBridge_DropListener(t *testing.T) {
	b := &PipelineBridge{
		id:          "test-bridge",
		taskStateCh: make(chan taskstate.Event, 1),
		variantCh:   make(chan variants.VariantEvent, 1),
	}

	var (
		events []BridgeDropEvent
		mu     sync.Mutex
	)
	b.SetDropListener(func(evt BridgeDropEvent) {
		mu.Lock()
		events = append(events, evt)
		mu.Unlock()
	})

	// Fill task-state channel, then overflow it.
	b.enqueueTaskState(taskstate.Event{PipelineID: "p1", TaskID: "t1"})
	b.enqueueTaskState(taskstate.Event{PipelineID: "p1", TaskID: "t2"}) // overflow

	mu.Lock()
	gotTS := len(events)
	mu.Unlock()
	if gotTS != 1 {
		t.Fatalf("expected 1 taskstate drop, got %d", gotTS)
	}
	if events[0].Kind != BridgeDropKindTaskState {
		t.Fatalf("drop kind = %q, want %q", events[0].Kind, BridgeDropKindTaskState)
	}
	if events[0].TaskID != "t2" {
		t.Fatalf("drop TaskID = %q, want t2", events[0].TaskID)
	}
	if events[0].TotalDropped != 1 {
		t.Fatalf("TotalDropped = %d, want 1", events[0].TotalDropped)
	}

	// Fill variant channel and overflow.
	b.onVariantEvent(variants.VariantEvent{VariantID: "v1"})
	b.onVariantEvent(variants.VariantEvent{VariantID: "v2"}) // overflow

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 {
		t.Fatalf("expected 2 drops total, got %d", len(events))
	}
	if events[1].Kind != BridgeDropKindVariant {
		t.Fatalf("second drop kind = %q, want %q", events[1].Kind, BridgeDropKindVariant)
	}
	if events[1].VariantID != "v2" {
		t.Fatalf("VariantID = %q, want v2", events[1].VariantID)
	}
	if b.DroppedCount() != 2 {
		t.Fatalf("DroppedCount = %d, want 2", b.DroppedCount())
	}
}

// TestPipelineBridge_DropListener_NilOK verifies clearing the listener is
// safe and that drops are still counted without a listener.
func TestPipelineBridge_DropListener_NilOK(t *testing.T) {
	b := &PipelineBridge{
		id:          "test-bridge",
		taskStateCh: make(chan taskstate.Event, 1),
	}
	b.SetDropListener(func(BridgeDropEvent) {})
	b.SetDropListener(nil)

	b.enqueueTaskState(taskstate.Event{TaskID: "a"})
	b.enqueueTaskState(taskstate.Event{TaskID: "b"}) // drop, no listener

	if got := b.DroppedCount(); got != 1 {
		t.Fatalf("DroppedCount = %d, want 1", got)
	}
}
