package orchestrator

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
)

func TestRouteProtocolPipelineTask_SeedsInspectorAndPublishesRunningUpdate(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	t.Cleanup(func() { _ = bus.Close() })

	scope := testScope()
	router := testRouter(bus, scope)
	task := testTask()
	task.TargetAgentID = pipelineWorkerTargetAgentID(task.TaskID, task.AgentType)
	task.Context = map[string]any{
		"task_slug":   "hello-cli",
		"task_name":   "Hello CLI",
		"co_agents":   []any{"designer"},
		"session_dir": "/tmp/session-protocol",
	}

	reqCh := make(chan *guide.RouteRequest, 1)
	reqSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		select {
		case reqCh <- req:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer reqSub.Unsubscribe()

	updateCh := make(chan *PipelineUpdate, 1)
	updateSub, err := bus.SubscribeAsync("pipeline.update."+agentshared.PipelineAgentInspector, func(msg *guide.Message) error {
		update, ok := msg.Payload.(*PipelineUpdate)
		if !ok || update == nil {
			return nil
		}
		select {
		case updateCh <- update:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe pipeline updates: %v", err)
	}
	defer updateSub.Unsubscribe()

	if err := router.RouteWithLifecycle(task, nil); err != nil {
		t.Fatalf("RouteWithLifecycle: %v", err)
	}

	var req *guide.RouteRequest
	select {
	case req = <-reqCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for guide route request")
	}
	if req.SourceAgentID != "orchestrator" {
		t.Fatalf("source_agent_id = %q, want orchestrator", req.SourceAgentID)
	}
	if req.SourceAgentName != "orchestrator" {
		t.Fatalf("source_agent_name = %q, want orchestrator", req.SourceAgentName)
	}
	if req.Metadata["pipeline_task"] != true {
		t.Fatalf("pipeline_task metadata = %#v, want true", req.Metadata["pipeline_task"])
	}
	if req.Metadata["session_id"] != task.SessionID {
		t.Fatalf("session_id metadata = %#v, want %q", req.Metadata["session_id"], task.SessionID)
	}
	if req.Metadata["session_dir"] != "/tmp/session-protocol" {
		t.Fatalf("session_dir metadata = %#v, want /tmp/session-protocol", req.Metadata["session_dir"])
	}
	if req.TargetAgentID != pipelineWorkerTargetAgentID(task.TaskID, agentshared.PipelineAgentInspector) {
		t.Fatalf("target_agent_id = %q", req.TargetAgentID)
	}

	var seeded agentshared.PipelineTaskInput
	if err := json.Unmarshal([]byte(req.Input), &seeded); err != nil {
		t.Fatalf("decode seeded pipeline task: %v", err)
	}
	if seeded.AgentType != agentshared.PipelineAgentInspector {
		t.Fatalf("seeded agent_type = %q, want %q", seeded.AgentType, agentshared.PipelineAgentInspector)
	}
	if stage, _ := seeded.Context["pipeline_stage"].(string); stage != string(StageInspect) {
		t.Fatalf("pipeline_stage = %q, want %q", stage, StageInspect)
	}
	if workerType, _ := seeded.Context["agent_type"].(string); workerType != "engineer" {
		t.Fatalf("context agent_type = %q, want engineer", workerType)
	}

	snapshot, err := agentshared.PipelineProtocolSnapshotFromTask(&seeded)
	if err != nil {
		t.Fatalf("PipelineProtocolSnapshotFromTask: %v", err)
	}
	if snapshot == nil {
		t.Fatal("expected protocol snapshot in seeded task")
	}
	if snapshot.CurrentRequest != initialProtocolPipelineRequest {
		t.Fatalf("current_request = %q, want %q", snapshot.CurrentRequest, initialProtocolPipelineRequest)
	}
	if len(snapshot.ActiveAgents) != 1 || snapshot.ActiveAgents[0] != agentshared.PipelineAgentInspector {
		t.Fatalf("active_agents = %#v, want inspector", snapshot.ActiveAgents)
	}
	if len(snapshot.Roster) != 4 {
		t.Fatalf("roster length = %d, want 4", len(snapshot.Roster))
	}

	select {
	case update := <-updateCh:
		if update.AgentType != agentshared.PipelineAgentInspector {
			t.Fatalf("agent_type = %q, want %q", update.AgentType, agentshared.PipelineAgentInspector)
		}
		if update.Stage != string(StageInspect) {
			t.Fatalf("stage = %q, want %q", update.Stage, StageInspect)
		}
		if update.Status != "running" {
			t.Fatalf("status = %q, want running", update.Status)
		}
		if update.Message != initialProtocolPipelineRequest {
			t.Fatalf("message = %q, want %q", update.Message, initialProtocolPipelineRequest)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial protocol pipeline update")
	}
}

func TestExtractPipelineUpdate_ParsesStageAttemptAndTimestampFromMap(t *testing.T) {
	now := time.Now().UTC().Round(0)
	update := extractPipelineUpdate(map[string]any{
		"dag_id":     "dag-1",
		"node_id":    "node-1",
		"task_id":    "task-1",
		"agent_id":   "worker-1",
		"agent_type": "tester-pipeline",
		"status":     "running",
		"stage":      "test",
		"progress":   0.4,
		"message":    "testing current state",
		"attempt":    2,
		"timestamp":  now.Format(time.RFC3339Nano),
	})
	if update == nil {
		t.Fatal("expected pipeline update")
	}
	if update.Stage != "test" {
		t.Fatalf("stage = %q, want test", update.Stage)
	}
	if update.Attempt != 2 {
		t.Fatalf("attempt = %d, want 2", update.Attempt)
	}
	if !update.Timestamp.Equal(now) {
		t.Fatalf("timestamp = %s, want %s", update.Timestamp.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	}
}
