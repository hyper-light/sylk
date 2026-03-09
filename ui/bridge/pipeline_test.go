package bridge

import (
	"testing"

	"github.com/adalundhe/sylk/core/pipeline/tdd"
)

func TestToPipelineStateMsg_UsesTaskIdentityAndSlug(t *testing.T) {
	msg := toPipelineStateMsg(tdd.PipelineEvent{
		PipelineID:        "task_auth_checkout",
		RuntimePipelineID: "pipe_8f31ab9d12",
		TaskID:            "task_auth_checkout",
		TaskSlug:          "auth-checkout",
		NewStatus:         tdd.StatusExecuting,
		WorkerType:        tdd.WorkerEngineer,
		LoopCount:         2,
		MaxLoops:          5,
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
	if msg.WorkerType != "engineer" {
		t.Fatalf("WorkerType = %q, want engineer", msg.WorkerType)
	}
}

func TestToPipelineStateMsg_FallsBackToRuntimePipelineID(t *testing.T) {
	msg := toPipelineStateMsg(tdd.PipelineEvent{
		RuntimePipelineID: "pipe_8f31ab9d12",
		NewStatus:         tdd.StatusPending,
	})

	if msg.PipelineID != "pipe_8f31ab9d12" {
		t.Fatalf("PipelineID = %q, want pipe_8f31ab9d12", msg.PipelineID)
	}
}

func TestToPipelineStateMsg_PrefersTaskIDOverMismatchedPipelineID(t *testing.T) {
	msg := toPipelineStateMsg(tdd.PipelineEvent{
		PipelineID:        "8e0a27d8-dbb9-4700-8db6-b62b6721836c",
		RuntimePipelineID: "pipe_8f31ab9d12",
		TaskID:            "task_1",
		TaskSlug:          "hello-cli",
		NewStatus:         tdd.StatusExecuting,
	})

	if msg.PipelineID != "task_1" {
		t.Fatalf("PipelineID = %q, want task_1", msg.PipelineID)
	}
	if msg.TaskID != "task_1" {
		t.Fatalf("TaskID = %q, want task_1", msg.TaskID)
	}
	if msg.TaskLabel != "hello-cli" {
		t.Fatalf("TaskLabel = %q, want hello-cli", msg.TaskLabel)
	}
}
