package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	coreevents "github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/pipeline/coordination"
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

func TestParseTaskDispatchMessage_UsesMessageSessionID(t *testing.T) {
	msg := &guide.Message{
		Metadata: map[string]any{
			"session_id": "sess-meta",
		},
		Payload: map[string]any{
			"task_id":    "task-1",
			"node_id":    "node-1",
			"dag_id":     "dag-1",
			"agent_type": "engineer",
			"prompt":     "ship it",
		},
	}

	dispatch, ok := parseTaskDispatchMessage(msg)
	if !ok {
		t.Fatal("expected dispatch payload to parse")
	}
	if dispatch.sessionID != "sess-meta" {
		t.Fatalf("sessionID = %q, want %q", dispatch.sessionID, "sess-meta")
	}
}

func TestRegisterTaskDispatch_PreservesExistingTaskSessionAndMetadata(t *testing.T) {
	now := time.Now()
	o := &Orchestrator{
		config: Config{SessionID: ""},
		state:  NewState("sess-state"),
	}
	o.state.Tasks["task-1"] = &TaskRecord{
		ID:          "task-1",
		WorkflowID:  "wf-1",
		Name:        "Keep metadata",
		Description: "Preserve the planned task context.",
		Status:      TaskStatusPending,
		CreatedAt:   now.Add(-time.Minute),
		SessionID:   "sess-existing",
		Metadata: map[string]any{
			"plan_id": "plan-123",
		},
	}

	router := o.registerTaskDispatch(&taskDispatchContext{
		taskID:     "task-1",
		workflowID: "wf-1",
		name:       "Keep metadata",
		agentType:  "engineer",
		agentID:    "eng-1",
		dagID:      "dag-1",
		nodeID:     "node-1",
		now:        now,
	})
	if router != o.taskRouter {
		t.Fatalf("router = %#v, want %v", router, o.taskRouter)
	}

	task := o.state.Tasks["task-1"]
	if task == nil {
		t.Fatal("expected task record to remain present")
	}
	if task.SessionID != "sess-existing" {
		t.Fatalf("session_id = %q, want %q", task.SessionID, "sess-existing")
	}
	if task.Metadata["plan_id"] != "plan-123" {
		t.Fatalf("plan_id = %#v, want %q", task.Metadata["plan_id"], "plan-123")
	}
	if task.Description != "Preserve the planned task context." {
		t.Fatalf("description = %q, want preserved description", task.Description)
	}
	if task.Status != TaskStatusRunning {
		t.Fatalf("status = %q, want %q", task.Status, TaskStatusRunning)
	}
}

func TestPublishTaskDispatchAgents_BootstrapsInspectorFirstForPipelineDispatch(t *testing.T) {
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

	published := pub.collected()
	if len(published) != len(PipelinePanelAgentTypes) {
		t.Fatalf("expected %d pipeline panel events, got %d", len(PipelinePanelAgentTypes), len(published))
	}
	if published[0].AgentID != "task_1:inspector-pipeline" {
		t.Fatalf("first event agent = %q, want inspector bootstrap", published[0].AgentID)
	}
	if published[0].EventType != coreevents.EventTypeAgentAction {
		t.Fatalf("first event type = %q, want %q", published[0].EventType, coreevents.EventTypeAgentAction)
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

func TestPublishTaskDispatchAgents_GlobalDispatchDoesNotRegisterPipelineGhosts(t *testing.T) {
	pub := &trackingActivityPub{}
	o := &Orchestrator{
		config:                  Config{SessionID: "session-1"},
		activityPub:             pub,
		pipelinePanelState:      make(map[string]pipelinePanelSnapshot),
		pipelinePanelRegistered: make(map[string]struct{}),
	}

	o.publishTaskDispatchAgents(&taskDispatchContext{
		taskID:    "task-global-review",
		taskSlug:  "global-review",
		nodeID:    "node-review",
		agentType: "tester",
		coAgents:  []string{"inspector"},
		now:       time.Now(),
	}, "")

	collected := pub.collected()
	if len(collected) != 2 {
		t.Fatalf("published %d events, want 2", len(collected))
	}
	for _, evt := range collected {
		if evt.AgentID != "tester" && evt.AgentID != "inspector" {
			t.Fatalf("unexpected global dispatch activity agent_id %q", evt.AgentID)
		}
		if evt.Data["source"] != "orchestrator_task_dispatch" {
			t.Fatalf("source = %v, want orchestrator_task_dispatch", evt.Data["source"])
		}
	}
}

func TestEnrichTaskDispatchCoordination_UsesCachedPrecedentsInline(t *testing.T) {
	svc := newCoordinationTestService(t)
	if _, err := svc.QueryView(context.Background(), coordination.QueryViewInput{
		TaskID:     "task-1",
		TaskName:   "Hello CLI",
		WorkerType: "engineer",
	}); err != nil {
		t.Fatalf("QueryView: %v", err)
	}
	svc.cache.Wait()
	svc.StoreCachedPrecedents("Hello CLI", "hello-cli", "engineer", []coordination.PrecedentSummary{
		{ID: "prec-1", Summary: "Prefer small argparse entrypoints."},
	})
	svc.cache.Wait()

	o := &Orchestrator{
		config:       Config{ArchivalistEnabled: true},
		coordination: svc,
	}
	dispatch := &taskDispatchContext{
		pipelineTaskID:   "task-1",
		pipelineTaskSlug: "hello-cli",
		precedentTask:    "Hello CLI",
		agentType:        "engineer",
		nodeCtx:          map[string]any{},
	}

	o.enrichTaskDispatchCoordination(dispatch)

	packet, ok := dispatch.nodeCtx["coordination_packet"].(*coordination.WorkerPacket)
	if !ok || packet == nil {
		t.Fatalf("coordination_packet = %#v, want worker packet", dispatch.nodeCtx["coordination_packet"])
	}
	if len(packet.HistoricalPrecedents) != 1 {
		t.Fatalf("historical_precedents = %d, want 1", len(packet.HistoricalPrecedents))
	}
	if packet.HistoricalPrecedents[0].ID != "prec-1" {
		t.Fatalf("precedent id = %q, want prec-1", packet.HistoricalPrecedents[0].ID)
	}
}

