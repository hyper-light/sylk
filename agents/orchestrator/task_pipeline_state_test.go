package orchestrator

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/pipeline/taskstate"
)

func TestPipelinePhaseStatus(t *testing.T) {
	tests := []struct {
		stage string
		want  taskstate.Status
	}{
		{stage: string(StageInspect), want: taskstate.StatusDefiningCriteria},
		{stage: string(StageTest), want: taskstate.StatusCreatingTests},
		{stage: string(StageExecute), want: taskstate.StatusExecuting},
		{stage: "", want: ""},
	}

	for _, tt := range tests {
		if got := pipelinePhaseStatus(tt.stage); got != tt.want {
			t.Fatalf("pipelinePhaseStatus(%q) = %q, want %q", tt.stage, got, tt.want)
		}
	}
}

func TestPublishTaskPipelineState(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	gotCh := make(chan *taskstate.Event, 1)
	sub, err := bus.SubscribeAsync(taskstate.Topic, func(msg *guide.Message) error {
		evt, ok := msg.Payload.(*taskstate.Event)
		if !ok {
			t.Fatalf("payload type = %T, want *taskstate.Event", msg.Payload)
		}
		gotCh <- evt
		return nil
	})
	if err != nil {
		t.Fatalf("SubscribeAsync: %v", err)
	}
	defer sub.Unsubscribe()

	publishTaskPipelineState(bus, "orchestrator", "task_auth_checkout", "auth-checkout", taskstate.StatusPending, "")

	select {
	case evt := <-gotCh:
		if evt.PipelineID != "task_auth_checkout" {
			t.Fatalf("PipelineID = %q, want task_auth_checkout", evt.PipelineID)
		}
		if evt.TaskLabel != "auth-checkout" {
			t.Fatalf("TaskLabel = %q, want auth-checkout", evt.TaskLabel)
		}
		if evt.Status != taskstate.StatusPending {
			t.Fatalf("Status = %q, want pending", evt.Status)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for task pipeline state")
	}
}
