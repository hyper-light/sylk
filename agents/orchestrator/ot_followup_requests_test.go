package orchestrator

import (
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/versioning"
)

func TestBuildOTGlobalFollowupRequest_UsesDirectPrompt(t *testing.T) {
	o := &Orchestrator{
		config: Config{AgentID: "orchestrator", SessionID: "sess-1"},
	}
	task := &TaskRecord{
		ID:          "task-7",
		Name:        "Checkout Recovery",
		Description: "Harden checkout recovery flow.",
		SessionID:   "sess-1",
		Metadata: map[string]any{
			"affected_files": []string{"pkg/checkout/recovery.go", "pkg/checkout/recovery_test.go"},
		},
	}
	update := &PipelineUpdate{
		NodeID:    "task-7",
		TaskID:    "task-7",
		AgentType: agentshared.PipelineAgentInspector,
		Status:    "succeeded",
		Output: map[string]any{
			"summary":       "Pipeline passed final audit and is ready for merge.",
			"evidence_refs": []any{"artifact:tester", "file:pkg/checkout/recovery.go"},
		},
		Timestamp: time.Date(2026, 3, 23, 12, 0, 0, 0, time.UTC),
	}

	req := o.buildOTGlobalFollowupRequest(task, update, "inspector", versioning.SemanticVersion{}, false)
	if req == nil {
		t.Fatal("buildOTGlobalFollowupRequest returned nil")
	}
	if !req.ExplicitTarget {
		t.Fatal("expected explicit target request")
	}
	if req.FireAndForget {
		t.Fatal("expected visible OT follow-up request, not fire-and-forget")
	}
	if req.TargetAgentID != "inspector" {
		t.Fatalf("target_agent_id = %q, want inspector", req.TargetAgentID)
	}
	if !strings.Contains(req.Input, "Global inspector follow-up is required") {
		t.Fatalf("input = %q, want global inspector follow-up prompt", req.Input)
	}
	if strings.Contains(req.Input, "\"task_id\"") {
		t.Fatalf("input = %q, want prompt text instead of pipeline JSON envelope", req.Input)
	}
	if req.Metadata["ot_handoff_followup"] != true {
		t.Fatalf("metadata ot_handoff_followup = %#v, want true", req.Metadata["ot_handoff_followup"])
	}
	if req.Metadata["global_review"] != true {
		t.Fatalf("metadata global_review = %#v, want true", req.Metadata["global_review"])
	}
	if _, ok := req.Metadata["global_review_protocol"]; !ok {
		t.Fatal("expected seeded global review protocol metadata")
	}
	if req.Metadata["agent_type"] != "inspector" {
		t.Fatalf("metadata agent_type = %#v, want inspector", req.Metadata["agent_type"])
	}
	if req.Metadata["global_review_stage"] != globalReviewStageFinal {
		t.Fatalf("metadata global_review_stage = %#v, want %q", req.Metadata["global_review_stage"], globalReviewStageFinal)
	}
	if !req.Timestamp.Equal(update.Timestamp.UTC()) {
		t.Fatalf("timestamp = %v, want %v", req.Timestamp, update.Timestamp.UTC())
	}
}

func TestBuildOTGlobalFollowupRequest_CheckpointReviewMetadata(t *testing.T) {
	o := &Orchestrator{
		state:  NewState("sess-1"),
		config: Config{AgentID: "orchestrator", SessionID: "sess-1"},
	}
	o.state.Workflows["wf-1"] = &WorkflowState{
		ID:        "wf-1",
		SessionID: "sess-1",
		TaskIDs:   []string{"task-7", "task-8"},
	}
	o.state.Tasks["task-7"] = &TaskRecord{ID: "task-7", WorkflowID: "wf-1", Status: TaskStatusCompleted}
	o.state.Tasks["task-8"] = &TaskRecord{ID: "task-8", WorkflowID: "wf-1", Status: TaskStatusPending}

	task := &TaskRecord{
		ID:          "task-7",
		WorkflowID:  "wf-1",
		Name:        "Checkout Recovery",
		Description: "Harden checkout recovery flow.",
		SessionID:   "sess-1",
		Metadata: map[string]any{
			"affected_files": []string{"pkg/checkout/recovery.go", "pkg/checkout/recovery_test.go"},
		},
	}
	update := &PipelineUpdate{
		NodeID:    "task-7",
		TaskID:    "task-7",
		AgentType: agentshared.PipelineAgentInspector,
		Status:    "succeeded",
		Output: map[string]any{
			"summary":       "Pipeline passed final audit and is ready for merge.",
			"evidence_refs": []any{"artifact:tester", "file:pkg/checkout/recovery.go"},
		},
		Timestamp: time.Date(2026, 3, 23, 12, 0, 0, 0, time.UTC),
	}

	req := o.buildOTGlobalFollowupRequest(task, update, "inspector", versioning.SemanticVersion{}, false)
	if req == nil {
		t.Fatal("buildOTGlobalFollowupRequest returned nil")
	}
	if req.Metadata["global_review_stage"] != globalReviewStageCheckpoint {
		t.Fatalf("metadata global_review_stage = %#v, want %q", req.Metadata["global_review_stage"], globalReviewStageCheckpoint)
	}
	if req.Metadata["workflow_remaining_tasks"] != 1 {
		t.Fatalf("metadata workflow_remaining_tasks = %#v, want 1", req.Metadata["workflow_remaining_tasks"])
	}
	if !strings.Contains(req.Input, "progressive checkpoint review") {
		t.Fatalf("input = %q, want checkpoint guidance", req.Input)
	}
	if !strings.Contains(req.Input, "Future planned work that has not been merged yet is pending, not missing.") {
		t.Fatalf("input = %q, want pending-not-missing guidance", req.Input)
	}
	if !strings.Contains(req.Input, "Do not branch into other completed pipelines in this turn") {
		t.Fatalf("input = %q, want single-pipeline queue guidance", req.Input)
	}
	if strings.Contains(req.Input, "Completed task IDs:") {
		t.Fatalf("input = %q, should not enumerate completed task IDs", req.Input)
	}
	if strings.Contains(req.Input, "Remaining task IDs:") {
		t.Fatalf("input = %q, should not enumerate remaining task IDs", req.Input)
	}
}

func TestBuildOTGlobalFollowupRequest_FallsBackToStateSession(t *testing.T) {
	o := &Orchestrator{
		state:  NewState("sess-state"),
		config: Config{AgentID: "orchestrator"},
	}
	task := &TaskRecord{ID: "task-8"}
	update := &PipelineUpdate{
		TaskID:    "task-8",
		AgentType: agentshared.PipelineAgentInspector,
		Status:    "succeeded",
		Timestamp: time.Date(2026, 3, 23, 12, 5, 0, 0, time.UTC),
	}

	req := o.buildOTGlobalFollowupRequest(task, update, "tester", versioning.SemanticVersion{}, false)
	if req == nil {
		t.Fatal("buildOTGlobalFollowupRequest returned nil")
	}
	if req.SessionID != "sess-state" {
		t.Fatalf("session_id = %q, want sess-state", req.SessionID)
	}
}

func TestBuildOTGlobalFollowupRequest_UsesTaskMetadataSessionID(t *testing.T) {
	o := &Orchestrator{
		state:  NewState("sess-state"),
		config: Config{AgentID: "orchestrator"},
	}
	task := &TaskRecord{
		ID: "task-8",
		Metadata: map[string]any{
			"session_id": "sess-meta",
		},
	}
	update := &PipelineUpdate{
		TaskID:    "task-8",
		AgentType: agentshared.PipelineAgentInspector,
		Status:    "succeeded",
		Timestamp: time.Date(2026, 3, 23, 12, 5, 0, 0, time.UTC),
	}

	req := o.buildOTGlobalFollowupRequest(task, update, "tester", versioning.SemanticVersion{}, false)
	if req == nil {
		t.Fatal("buildOTGlobalFollowupRequest returned nil")
	}
	if req.SessionID != "sess-meta" {
		t.Fatalf("session_id = %q, want sess-meta", req.SessionID)
	}
}

func TestFinalizePipelineUpdate_InspectorSuccessPublishesGlobalInspectorFollowup(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	t.Cleanup(func() { _ = bus.Close() })

	reqCh := make(chan *guide.RouteRequest, 2)
	sub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		if req.Metadata["ot_handoff_followup"] != true {
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
	defer sub.Unsubscribe()

	o := &Orchestrator{
		bus:   bus,
		state: NewState("sess-1"),
		config: Config{
			AgentID:   "orchestrator",
			SessionID: "sess-1",
		},
	}
	o.state.Tasks["task-9"] = &TaskRecord{
		ID:          "task-9",
		Name:        "Session Recovery",
		Description: "Ensure session recovery survives transient failures.",
		SessionID:   "sess-1",
		Metadata: map[string]any{
			"affected_files":      []string{"pkg/session/recovery.go"},
			"acceptance_criteria": []string{"Recovered sessions preserve auth state."},
			"test_requirements":   []string{"Run regression coverage for session recovery."},
		},
	}

	o.finalizePipelineUpdate(&PipelineUpdate{
		DAGID:     "dag-1",
		NodeID:    "task-9",
		TaskID:    "task-9",
		AgentType: agentshared.PipelineAgentInspector,
		Status:    "succeeded",
		Output: map[string]any{
			"summary":       "Inspector accepted the completed pipeline.",
			"evidence_refs": []any{"artifact:tester", "artifact:inspector"},
		},
		Timestamp: time.Date(2026, 3, 23, 12, 30, 0, 0, time.UTC),
	})

	collected := make(map[string]*guide.RouteRequest, 1)
	timeout := time.After(2 * time.Second)
	for len(collected) < 1 {
		select {
		case req := <-reqCh:
			collected[req.TargetAgentID] = req
		case <-timeout:
			t.Fatalf("timed out waiting for OT follow-up requests, collected=%v", mapsKeys(collected))
		}
	}

	inspectorReq := collected["inspector"]
	if inspectorReq == nil {
		t.Fatal("missing global inspector follow-up request")
	}
	if !strings.Contains(inspectorReq.Input, "Operational Transform has accepted this completed pipeline") {
		t.Fatalf("inspector input = %q, want OT merged-state prompt", inspectorReq.Input)
	}
	if inspectorReq.Metadata["global_review"] != true {
		t.Fatalf("metadata global_review = %#v, want true", inspectorReq.Metadata["global_review"])
	}
	if _, ok := inspectorReq.Metadata["global_review_protocol"]; !ok {
		t.Fatal("missing seeded global review protocol metadata on inspector follow-up")
	}
	select {
	case req := <-reqCh:
		t.Fatalf("unexpected additional OT follow-up request: target=%s", req.TargetAgentID)
	case <-time.After(150 * time.Millisecond):
	}
}

func mapsKeys(values map[string]*guide.RouteRequest) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func TestFinalizePipelineUpdate_NonInspectorSuccessDoesNotPublishGlobalFollowups(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	t.Cleanup(func() { _ = bus.Close() })

	reqCh := make(chan *guide.RouteRequest, 1)
	sub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		if req.Metadata["ot_handoff_followup"] != true {
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
	defer sub.Unsubscribe()

	o := &Orchestrator{
		bus:   bus,
		state: NewState("sess-1"),
		config: Config{
			AgentID:   "orchestrator",
			SessionID: "sess-1",
		},
	}
	o.state.Tasks["task-11"] = &TaskRecord{ID: "task-11", SessionID: "sess-1"}

	o.finalizePipelineUpdate(&PipelineUpdate{
		TaskID:    "task-11",
		NodeID:    "task-11",
		AgentType: "engineer",
		Status:    "succeeded",
	})

	select {
	case req := <-reqCh:
		t.Fatalf("unexpected OT global follow-up request: %#v", req)
	case <-time.After(150 * time.Millisecond):
	}
}
