package orchestrator

import (
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/pipeline/tdd"
)

func TestParseTaskDispatchMessageCanonicalizesPipelineContext(t *testing.T) {
	msg := &guide.Message{
		Payload: map[string]any{
			"task_slug":   "",
			"workflow_id": "workflow-1",
			"name":        "Implement auth",
			"agent_id":    "engineer-1",
			"node_id":     "node-1",
			"agent_type":  "engineer",
			"prompt":      "ship it",
			"context": map[string]any{
				"task_id":            "task-1",
				"task_slug":          "task-auth",
				"task_name":          " Implement auth ",
				"pipeline_stage":     "execute",
				"pipeline_parent_id": "parent-1",
			},
			"parent_results": map[string]any{"inspect": "ok"},
			"dag_id":         "dag-1",
			"ack_topic":      "pipeline.ack",
			"co_agents":      []any{"tester-pipeline", "inspector-pipeline"},
		},
	}

	dispatch, ok := parseTaskDispatchMessage(msg)
	if !ok {
		t.Fatal("expected dispatch payload to parse")
	}
	if dispatch.taskID != "task-1" {
		t.Fatalf("taskID = %q, want %q", dispatch.taskID, "task-1")
	}
	if dispatch.taskSlug != "task-auth" {
		t.Fatalf("taskSlug = %q, want %q", dispatch.taskSlug, "task-auth")
	}
	if dispatch.pipelineStage != "execute" {
		t.Fatalf("pipelineStage = %q, want %q", dispatch.pipelineStage, "execute")
	}
	if dispatch.pipelineParentID != "parent-1" {
		t.Fatalf("pipelineParentID = %q, want %q", dispatch.pipelineParentID, "parent-1")
	}
	if dispatch.coordinationTask != "Implement auth" {
		t.Fatalf("coordinationTask = %q, want %q", dispatch.coordinationTask, "Implement auth")
	}
	if dispatch.precedentTask != "Implement auth" {
		t.Fatalf("precedentTask = %q, want %q", dispatch.precedentTask, "Implement auth")
	}
	if got := dispatch.nodeCtx["ack_topic"]; got != "pipeline.ack" {
		t.Fatalf("ack_topic = %#v, want %q", got, "pipeline.ack")
	}
	if got := dispatch.nodeCtx["dag_id"]; got != "dag-1" {
		t.Fatalf("dag_id = %#v, want %q", got, "dag-1")
	}
	if got := dispatch.nodeCtx["node_id"]; got != "node-1" {
		t.Fatalf("node_id = %#v, want %q", got, "node-1")
	}
	if len(dispatch.coAgents) != 2 {
		t.Fatalf("coAgents = %v, want 2 entries", dispatch.coAgents)
	}
}

func TestPublishTaskDispatchAgents_SkipsManagedPipelineDispatch(t *testing.T) {
	pub := &trackingActivityPub{}
	o := &Orchestrator{
		config:                  Config{SessionID: "session-1"},
		activityPub:             pub,
		pipelineMgr:             &tdd.PipelineManager{},
		pipelinePanelState:      make(map[string]pipelinePanelSnapshot),
		pipelinePanelRegistered: make(map[string]struct{}),
	}

	o.publishTaskDispatchAgents(&taskDispatchContext{
		nodeID:           "task_1",
		agentType:        "engineer",
		pipelineTaskID:   "task_1",
		pipelineTaskSlug: "implement-hello-cli",
		pipelineStage:    string(StageExecute),
	}, "executing")

	if got := len(pub.collected()); got != 0 {
		t.Fatalf("expected no published activity for managed pipeline dispatch, got %d events", got)
	}
}

func TestPublishTaskDispatchAgents_PublishesForUnmanagedDispatch(t *testing.T) {
	pub := &trackingActivityPub{}
	o := &Orchestrator{
		config:                  Config{SessionID: "session-1"},
		activityPub:             pub,
		pipelinePanelState:      make(map[string]pipelinePanelSnapshot),
		pipelinePanelRegistered: make(map[string]struct{}),
	}

	o.publishTaskDispatchAgents(&taskDispatchContext{
		nodeID:           "task_1",
		agentType:        "engineer",
		pipelineTaskID:   "task_1",
		pipelineTaskSlug: "implement-hello-cli",
		pipelineStage:    string(StageExecute),
	}, "executing")

	if got := len(pub.collected()); got == 0 {
		t.Fatal("expected activity events for unmanaged dispatch")
	}
}
