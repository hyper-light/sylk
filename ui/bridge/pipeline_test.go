package bridge

import (
	"testing"

	"github.com/adalundhe/sylk/core/pipeline/taskstate"
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
