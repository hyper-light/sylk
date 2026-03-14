package orchestrator

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/pipeline/tdd"
)

func TestHandleManagedPipelineEvent_PublishesRunningUpdate(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	t.Cleanup(func() { _ = bus.Close() })

	updates := make(chan *PipelineUpdate, 1)
	sub, err := bus.SubscribeAsync("pipeline.update.engineer", func(msg *guide.Message) error {
		update, ok := msg.Payload.(*PipelineUpdate)
		if !ok {
			if data, dataOK := msg.Payload.(map[string]any); dataOK {
				update = extractPipelineUpdate(data)
			}
		}
		if update != nil {
			updates <- update
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()

	o := &Orchestrator{
		config: Config{AgentID: "orchestrator"},
		bus:    bus,
	}

	o.handleManagedPipelineEvent(tdd.PipelineEvent{
		TaskID:     "task_1",
		DAGID:      "dag-1",
		DAGNodeID:  "task_1",
		WorkerType: tdd.WorkerEngineer,
		NewStatus:  tdd.StatusActive,
		Stage:      string(StageInspect),
		Message:    "inspector evaluating the task",
		LoopCount:  1,
		Timestamp:  time.Now(),
	})

	select {
	case update := <-updates:
		if update.NodeID != "task_1" {
			t.Fatalf("node_id = %q, want task_1", update.NodeID)
		}
		if update.DAGID != "dag-1" {
			t.Fatalf("dag_id = %q, want dag-1", update.DAGID)
		}
		if update.TaskID != "task_1" {
			t.Fatalf("task_id = %q, want task_1", update.TaskID)
		}
		if update.AgentType != "engineer" {
			t.Fatalf("agent_type = %q, want engineer", update.AgentType)
		}
		if update.Stage != string(StageInspect) {
			t.Fatalf("stage = %q, want %q", update.Stage, StageInspect)
		}
		if update.Status != "running" {
			t.Fatalf("status = %q, want running", update.Status)
		}
		if update.Message != "inspector evaluating the task" {
			t.Fatalf("message = %q, want %q", update.Message, "inspector evaluating the task")
		}
		if update.Attempt != 1 {
			t.Fatalf("attempt = %d, want 1", update.Attempt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for managed pipeline update")
	}
}

func TestHandleManagedPipelineEvent_SkipsTerminalEvent(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	t.Cleanup(func() { _ = bus.Close() })

	updates := make(chan *PipelineUpdate, 1)
	sub, err := bus.SubscribeAsync("pipeline.update.engineer", func(msg *guide.Message) error {
		update, ok := msg.Payload.(*PipelineUpdate)
		if !ok {
			if data, dataOK := msg.Payload.(map[string]any); dataOK {
				update = extractPipelineUpdate(data)
			}
		}
		if update != nil {
			updates <- update
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()

	o := &Orchestrator{
		config: Config{AgentID: "orchestrator"},
		bus:    bus,
	}

	o.handleManagedPipelineEvent(tdd.PipelineEvent{
		TaskID:     "task_1",
		DAGID:      "dag-1",
		DAGNodeID:  "task_1",
		WorkerType: tdd.WorkerEngineer,
		NewStatus:  tdd.StatusCompleted,
		LoopCount:  1,
		Timestamp:  time.Now(),
	})

	select {
	case update := <-updates:
		t.Fatalf("unexpected update published for terminal event: %#v", update)
	case <-time.After(200 * time.Millisecond):
	}
}
