package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/container"
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

	req := o.buildOTGlobalFollowupRequest(task, update, "inspector", versioning.SemanticVersion{}, false, "")
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
	if req.Metadata["serialized_followup_queue_key"] != "global_review:sess-1" {
		t.Fatalf("metadata serialized_followup_queue_key = %#v, want %q", req.Metadata["serialized_followup_queue_key"], "global_review:sess-1")
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

	req := o.buildOTGlobalFollowupRequest(task, update, "inspector", versioning.SemanticVersion{}, false, "")
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

	req := o.buildOTGlobalFollowupRequest(task, update, "tester", versioning.SemanticVersion{}, false, "")
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

	req := o.buildOTGlobalFollowupRequest(task, update, "tester", versioning.SemanticVersion{}, false, "")
	if req == nil {
		t.Fatal("buildOTGlobalFollowupRequest returned nil")
	}
	if req.SessionID != "sess-meta" {
		t.Fatalf("session_id = %q, want sess-meta", req.SessionID)
	}
	if req.Metadata["session_id"] != "sess-meta" {
		t.Fatalf("metadata session_id = %#v, want sess-meta", req.Metadata["session_id"])
	}
}

func TestBuildOTGlobalFollowupRequest_PreservesSessionMetadataForDurableReviewState(t *testing.T) {
	o := &Orchestrator{
		state:  NewState("sess-state"),
		config: Config{AgentID: "orchestrator"},
	}
	task := &TaskRecord{
		ID: "task-12",
		Metadata: map[string]any{
			"session_id":  "sess-meta",
			"session_dir": "/tmp/session-global-review",
		},
	}
	update := &PipelineUpdate{
		TaskID:    "task-12",
		AgentType: agentshared.PipelineAgentInspector,
		Status:    "succeeded",
		Timestamp: time.Date(2026, 3, 23, 12, 6, 0, 0, time.UTC),
	}

	req := o.buildOTGlobalFollowupRequest(task, update, "inspector", versioning.SemanticVersion{}, false, "")
	if req == nil {
		t.Fatal("buildOTGlobalFollowupRequest returned nil")
	}
	if req.Metadata["session_id"] != "sess-meta" {
		t.Fatalf("metadata session_id = %#v, want sess-meta", req.Metadata["session_id"])
	}
	if req.Metadata["session_dir"] != "/tmp/session-global-review" {
		t.Fatalf("metadata session_dir = %#v, want /tmp/session-global-review", req.Metadata["session_dir"])
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

func TestHandlePipelineUpdate_InspectorSuccessDefersResourceReleaseUntilCheckpointAccept(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	reqCh := make(chan *guide.RouteRequest, 1)
	reqSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil || req.Metadata["ot_handoff_followup"] != true {
			return nil
		}
		reqCh <- req
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer reqSub.Unsubscribe()

	quota := container.NewResourceQuota(container.ResourceQuotaConfig{
		GoroutineLimit: 200,
		ContainerLimit: 4,
	})
	runtime := newTaskPodTestRuntime(quota)
	taskPod := newTaskPodForTest("task-1", runtime, time.Minute, time.Minute, time.Minute)
	if err := taskPod.HoldForNode(context.Background(), "node-1", []string{"inspector-pipeline"}); err != nil {
		t.Fatalf("taskPod.HoldForNode: %v", err)
	}
	if usage := quota.Usage(); usage.ContainerCount != 4 {
		t.Fatalf("expected hot task pod containers before OT cleanup, got %+v", usage)
	}

	workingDir := t.TempDir()
	svfs, err := versioning.NewSessionVFS(versioning.SessionVFSConfig{
		SessionID:       versioning.SessionID("sess-1"),
		WorkingDir:      workingDir,
		AllowDiskExport: true,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	defer svfs.Close()
	pipe, err := svfs.BeginPipeline(versioning.BeginPipelineConfig{
		PipelineID: "task-1",
		SessionID:  versioning.SessionID("sess-1"),
		WorkingDir: workingDir,
		AgentID:    "inspector-pipeline",
		AgentRole:  "inspector",
	})
	if err != nil {
		t.Fatalf("BeginPipeline: %v", err)
	}
	if err := pipe.Write(context.Background(), "main.txt", []byte("candidate")); err != nil {
		t.Fatalf("pipeline write: %v", err)
	}
	if !svfs.HasPipeline("task-1") {
		t.Fatal("expected tracked pipeline draft before OT cleanup")
	}

	scope := testScope()
	buffers, err := NewBufferRegistry(DefaultBufferRegistryConfig(4), nil, scope)
	if err != nil {
		t.Fatalf("NewBufferRegistry: %v", err)
	}

	dispatcher := NewBusNodeDispatcher(nil, "orchestrator", "sess-1", "dag-1", nil, nil, nil)
	dispatcher.nodePods.Store("node-1", taskPod)

	bridge := NewDAGBridge(DefaultDAGBridgeConfig(), DAGBridgeDeps{
		SessionID: "sess-1",
		AgentID:   "orchestrator",
	})
	bridge.buffers = buffers
	bridge.activeDAGs["dag-1"] = &ActiveDAGMeta{
		Dispatcher:             dispatcher,
		TaskPods:               map[string]*agentshared.AgentPod{"task-1": taskPod},
		TaskLabels:             map[string]string{"task-1": "auth-checkout"},
		ResetPendingTaskPods:   make(map[string]struct{}),
		ReleasePendingTaskPods: make(map[string]struct{}),
	}

	o := &Orchestrator{
		bus:                      bus,
		state:                    NewState("sess-1"),
		config:                   Config{AgentID: "orchestrator", SessionID: "sess-1"},
		bufferRegistry:           buffers,
		dagBridge:                bridge,
		sessionVFS:               map[string]*versioning.SessionVFS{"sess-1": svfs},
		pendingCheckpointReviews: make(map[string]*pendingCheckpointReview),
	}
	router := NewTaskRouter(TaskRouterConfig{
		Bus:                    bus,
		Scope:                  scope,
		AgentID:                "orchestrator",
		SessionID:              "sess-1",
		OnVisibleRoutePublish:  o.ActivatePublishedReviewCandidate,
		OnVisibleRouteTerminal: o.HandleCheckpointReviewTerminal,
	})
	o.SetTaskRouter(router)
	o.state.Tasks["task-1"] = &TaskRecord{
		ID:        "task-1",
		Name:      "auth checkout",
		SessionID: "sess-1",
	}

	err = o.handlePipelineUpdate(&guide.Message{
		ID: "msg-1",
		Payload: &PipelineUpdate{
			DAGID:     "dag-1",
			NodeID:    "node-1",
			TaskID:    "task-1",
			AgentType: agentshared.PipelineAgentInspector,
			Status:    "succeeded",
			Output: map[string]any{
				"summary": "Inspector accepted the completed pipeline.",
			},
			Timestamp: time.Now().UTC(),
		},
	})
	if err != nil {
		t.Fatalf("handlePipelineUpdate: %v", err)
	}

	if svfs.HasPipeline("task-1") {
		t.Fatal("expected OT-accepted task draft to be removed from SessionVFS")
	}
	if taskPod.IsReleased() {
		t.Fatal("expected accepted task pod to remain allocated until checkpoint acceptance")
	}
	if got := buffers.ActiveBufferCount(); got == 0 {
		t.Fatal("expected task buffer to remain active until checkpoint acceptance")
	}
	select {
	case req := <-reqCh:
		if req.Metadata["review_candidate_id"] != "task-1" {
			t.Fatalf("review_candidate_id = %#v, want task-1", req.Metadata["review_candidate_id"])
		}
		respMsg := guide.NewResponseMessage("", &guide.RouteResponse{
			CorrelationID:       req.CorrelationID,
			Success:             true,
			RespondingAgentID:   "inspector",
			RespondingAgentName: "Inspector",
			Data: &agentshared.GlobalReviewTurnResponse{
				Result: "checkpoint accepted",
				Action: &agentshared.GlobalReviewTurnAction{
					Type:      agentshared.GlobalReviewActionAccept,
					AgentType: agentshared.GlobalReviewAgentInspector,
					Summary:   "checkpoint accepted",
				},
			},
		})
		respMsg.CorrelationID = req.CorrelationID
		if !router.DeliverResponse(respMsg) {
			t.Fatal("expected checkpoint-accept response to be consumed by task router")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected OT global-review follow-up request")
	}
	if !taskPod.IsReleased() {
		t.Fatal("expected accepted task pod to be released after checkpoint acceptance")
	}
	if usage := quota.Usage(); usage.ContainerCount != 0 {
		t.Fatalf("expected accepted task cleanup to deallocate containers, got %+v", usage)
	}
	if got := buffers.ActiveBufferCount(); got != 0 {
		t.Fatalf("expected accepted task buffer to be released, got %d active buffers", got)
	}
	if _, present := bridge.activeDAGs["dag-1"].TaskPods["task-1"]; present {
		t.Fatal("expected accepted task pod to be removed from active DAG state")
	}
}
