package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPipelineProtocolSkills_RecordTurnActions(t *testing.T) {
	snapshot := &PipelineProtocolSnapshot{
		Roster: []PipelineProtocolAgent{
			{AgentType: PipelineAgentInspector},
			{AgentType: PipelineAgentTester},
			{AgentType: PipelineAgentEngineer},
		},
		ActiveAgents: []string{PipelineAgentInspector},
		PendingChallenge: &PipelineProtocolChallenge{
			ID:              "challenge-1",
			RequestingAgent: PipelineAgentInspector,
			TargetAgents:    []string{PipelineAgentTester},
		},
		PendingValidation: &PipelineValidationRecord{
			ChallengeID:     "challenge-0",
			RequestingAgent: PipelineAgentInspector,
			RespondingAgent: PipelineAgentTester,
			Status:          "passed",
			Summary:         "tests are ready",
		},
	}
	ctx := WithPipelineProtocolState(context.Background(), NewPipelineProtocolState(snapshot))
	ctx = WithTaskExecutionContract(ctx, &TaskExecutionContract{RuntimeAgentType: PipelineAgentInspector})

	skills := PipelineProtocolSkills(PipelineProtocolSkillConfig{
		AgentType:   func() string { return PipelineAgentInspector },
		InspectorOT: true,
	})

	runSkill(t, ctx, skills, "process_validation", map[string]any{
		"challenge_id": "challenge-0",
		"decision":     "accept",
		"summary":      "accepted the tester response",
	})
	runSkill(t, ctx, skills, "handoff_next", map[string]any{
		"target_agents": []string{"tester"},
		"reason":        "need follow-up",
		"request":       "Clarify the failing case.",
	})

	state := PipelineProtocolStateFromContext(ctx)
	if state == nil {
		t.Fatal("pipeline protocol state missing from context")
	}
	if action := state.TerminalAction(); action == nil || action.Type != PipelineProtocolActionHandoff {
		t.Fatalf("terminal action = %#v, want handoff", action)
	}
	if processed := state.ProcessedValidations(); len(processed) != 1 || processed[0].Decision != PipelineValidationDecisionAccept {
		t.Fatalf("processed validations = %#v", processed)
	}
}

func TestPipelineProtocol_RejectsSelfTargetSelection(t *testing.T) {
	ctx := WithPipelineProtocolState(context.Background(), NewPipelineProtocolState(&PipelineProtocolSnapshot{
		Roster: []PipelineProtocolAgent{
			{AgentType: PipelineAgentInspector},
			{AgentType: PipelineAgentTester},
		},
		ActiveAgents: []string{PipelineAgentInspector},
	}))
	ctx = WithTaskExecutionContract(ctx, &TaskExecutionContract{RuntimeAgentType: PipelineAgentInspector})

	skills := PipelineProtocolSkills(PipelineProtocolSkillConfig{
		AgentType: func() string { return PipelineAgentInspector },
	})

	for _, toolName := range []string{"challenge_agent", "handoff_next"} {
		_, err := callSkill(t, ctx, skills, toolName, map[string]any{
			"target_agents": []string{"inspector-pipeline"},
			"reason":        "loop back to inspector",
			"request":       "this should be refused",
		})
		if err == nil || !strings.Contains(err.Error(), "cannot target itself") {
			t.Fatalf("%s error = %v, want self-target refusal", toolName, err)
		}
	}
}

func TestValidatePipelineProtocolCompletion_RequiresTurnAction(t *testing.T) {
	ctx := WithPipelineProtocolState(context.Background(), NewPipelineProtocolState(&PipelineProtocolSnapshot{}))
	if err := ValidatePipelineProtocolCompletion(ctx, PipelineAgentEngineer); err == nil {
		t.Fatal("expected missing turn action to fail completion")
	}
}

func TestFinalizePipeline_RequiresImmediateHandoffToOT(t *testing.T) {
	snapshot := &PipelineProtocolSnapshot{
		PendingValidation: &PipelineValidationRecord{
			ChallengeID:         "challenge-ready",
			RequestingAgent:     PipelineAgentInspector,
			RespondingAgent:     PipelineAgentTester,
			Status:              string(PipelineValidationPassed),
			Summary:             "tester accepted the audit",
			ChallengeReferences: []string{finalizePipelineVerificationReference},
			EvidenceRefs:        []string{"artifact:tester"},
		},
	}
	ctx := WithPipelineProtocolState(context.Background(), NewPipelineProtocolState(snapshot))
	ctx = WithTaskExecutionContract(ctx, &TaskExecutionContract{RuntimeAgentType: PipelineAgentInspector})

	skills := PipelineProtocolSkills(PipelineProtocolSkillConfig{
		AgentType:   func() string { return PipelineAgentInspector },
		InspectorOT: true,
		Committer:   func() PipelineCommitter { return testNoopPipelineCommitter{} },
	})

	result, err := callSkill(t, ctx, skills, "finalize_pipeline", map[string]any{
		"summary":       "all criteria passed",
		"evidence_refs": []string{"artifact:inspector"},
	})
	if err != nil {
		t.Fatalf("finalize_pipeline: %v", err)
	}
	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("finalize_pipeline result = %#v, want map", result)
	}
	if resultMap["must_handoff_to_ot"] != true {
		t.Fatalf("finalize_pipeline result = %#v, want must_handoff_to_ot=true", result)
	}
	if resultMap["required_next_action"] != "handoff_to_ot" {
		t.Fatalf("finalize_pipeline result = %#v, want required_next_action=handoff_to_ot", result)
	}

	if _, err := callSkill(t, ctx, skills, "handoff_next", map[string]any{
		"target_agents": []string{"tester"},
		"reason":        "try another audit anyway",
		"request":       "this should be blocked",
	}); err == nil || !strings.Contains(err.Error(), "must invoke `handoff_to_ot` now") {
		t.Fatalf("handoff_next error = %v, want immediate handoff_to_ot requirement", err)
	}

	if err := ValidatePipelineProtocolCompletion(ctx, PipelineAgentInspector); err == nil || !strings.Contains(err.Error(), "must invoke `handoff_to_ot` now") {
		t.Fatalf("ValidatePipelineProtocolCompletion error = %v, want immediate handoff_to_ot requirement", err)
	}

	runSkill(t, ctx, skills, "handoff_to_ot", map[string]any{
		"summary":       "ready for OT merge",
		"evidence_refs": []string{"artifact:inspector", "artifact:tester"},
	})

	if err := ValidatePipelineProtocolCompletion(ctx, PipelineAgentInspector); err != nil {
		t.Fatalf("ValidatePipelineProtocolCompletion after handoff_to_ot = %v", err)
	}
}

func TestFinalizePipeline_RequiresImmediateHandoffToOT_BlocksHandoffDispatch(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	routeCh := make(chan *guide.RouteRequest, 1)
	reqSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		select {
		case routeCh <- req:
		default:
		}
		return nil
	})
	require.NoError(t, err)
	defer reqSub.Unsubscribe()

	task := &PipelineTaskInput{
		TaskID:        "task-ready-ot",
		AgentType:     PipelineAgentInspector,
		TargetAgentID: PipelineWorkerRoutingTarget("task-ready-ot", PipelineAgentInspector),
		Prompt:        "Close the pipeline.",
		SessionID:     "session-ready-ot",
		Context: map[string]any{
			"pipeline_stage": "inspect",
			"pipeline_protocol": PipelineProtocolSnapshotMap(&PipelineProtocolSnapshot{
				Roster: []PipelineProtocolAgent{
					{AgentType: PipelineAgentInspector},
					{AgentType: PipelineAgentTester},
					{AgentType: PipelineAgentEngineer},
				},
				ActiveAgents: []string{PipelineAgentInspector},
				PendingValidation: &PipelineValidationRecord{
					ChallengeID:         "challenge-ready-dispatch",
					RequestingAgent:     PipelineAgentInspector,
					RespondingAgent:     PipelineAgentTester,
					Status:              string(PipelineValidationPassed),
					Summary:             "tester accepted the audit",
					ChallengeReferences: []string{finalizePipelineVerificationReference},
					EvidenceRefs:        []string{"artifact:tester"},
				},
			}),
		},
	}
	ctx := WithPipelineTaskProtocolState(context.Background(), task)
	ctx = WithTaskExecutionContract(ctx, &TaskExecutionContract{RuntimeAgentType: PipelineAgentInspector})
	ctx = WithStreamContext(ctx, "corr-ready-ot", "tui")

	skills := PipelineProtocolSkills(PipelineProtocolSkillConfig{
		AgentType:   func() string { return PipelineAgentInspector },
		AgentID:     func() string { return "inspector-ready-ot-1" },
		InspectorOT: true,
		Route: PipelineProtocolRouteConfig{
			BusProvider: func() guide.EventBus { return bus },
			SessionID:   func() string { return task.SessionID },
		},
	})

	_, err = callSkill(t, ctx, skills, "finalize_pipeline", map[string]any{
		"summary":       "all criteria passed",
		"evidence_refs": []string{"artifact:inspector"},
	})
	require.NoError(t, err)

	if _, err := callSkill(t, ctx, skills, "handoff_next", map[string]any{
		"target_agents": []string{"tester"},
		"reason":        "try another audit anyway",
		"request":       "this should be blocked",
	}); err == nil || !strings.Contains(err.Error(), "must invoke `handoff_to_ot` now") {
		t.Fatalf("handoff_next error = %v, want immediate handoff_to_ot requirement", err)
	}

	select {
	case req := <-routeCh:
		t.Fatalf("unexpected route published while handoff_to_ot was required: %+v", req)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestFinalizePipeline_ReadyAfterAcceptedProcessValidation(t *testing.T) {
	snapshot := &PipelineProtocolSnapshot{
		PendingValidation: &PipelineValidationRecord{
			ChallengeID:         "challenge-ready-processed",
			RequestingAgent:     PipelineAgentInspector,
			RespondingAgent:     PipelineAgentTester,
			Status:              string(PipelineValidationPassed),
			Summary:             "tester accepted the audit",
			ChallengeReferences: []string{finalizePipelineVerificationReference},
			EvidenceRefs:        []string{"artifact:tester"},
		},
	}
	ctx := WithPipelineProtocolState(context.Background(), NewPipelineProtocolState(snapshot))
	ctx = WithTaskExecutionContract(ctx, &TaskExecutionContract{RuntimeAgentType: PipelineAgentInspector})

	skills := PipelineProtocolSkills(PipelineProtocolSkillConfig{
		AgentType:   func() string { return PipelineAgentInspector },
		InspectorOT: true,
	})

	runSkill(t, ctx, skills, "process_validation", map[string]any{
		"challenge_id": "challenge-ready-processed",
		"decision":     "accept",
		"summary":      "accepted the passing tester audit",
	})

	state := PipelineProtocolStateFromContext(ctx)
	if state == nil {
		t.Fatal("pipeline protocol state missing from context")
	}
	processed := state.ProcessedValidations()
	if len(processed) != 1 || processed[0].Validation == nil {
		t.Fatalf("processed validations = %#v, want accepted validation record", processed)
	}

	result, err := callSkill(t, ctx, skills, "finalize_pipeline", map[string]any{
		"summary":       "all criteria passed",
		"evidence_refs": []string{"artifact:inspector"},
	})
	if err != nil {
		t.Fatalf("finalize_pipeline after process_validation: %v", err)
	}
	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("finalize_pipeline result = %#v, want map", result)
	}
	if resultMap["ready_for_ot"] != true {
		t.Fatalf("finalize_pipeline result = %#v, want ready_for_ot=true", result)
	}
	if resultMap["must_handoff_to_ot"] != true {
		t.Fatalf("finalize_pipeline result = %#v, want must_handoff_to_ot=true", result)
	}
	if resultMap["required_next_action"] != "handoff_to_ot" {
		t.Fatalf("finalize_pipeline result = %#v, want required_next_action=handoff_to_ot", result)
	}
}

func TestFinalizePipeline_ReadyAfterAcceptedPartialProcessValidation(t *testing.T) {
	snapshot := &PipelineProtocolSnapshot{
		PendingValidation: &PipelineValidationRecord{
			ChallengeID:         "challenge-ready-partial",
			RequestingAgent:     PipelineAgentInspector,
			RespondingAgent:     PipelineAgentTester,
			Status:              string(PipelineValidationPartial),
			Summary:             "tester accepted the audit but execution remained partially blocked by environment issues",
			ChallengeReferences: []string{finalizePipelineVerificationReference},
			EvidenceRefs:        []string{"artifact:tester", "tests/test_init.py"},
		},
	}
	ctx := WithPipelineProtocolState(context.Background(), NewPipelineProtocolState(snapshot))
	ctx = WithTaskExecutionContract(ctx, &TaskExecutionContract{RuntimeAgentType: PipelineAgentInspector})

	skills := PipelineProtocolSkills(PipelineProtocolSkillConfig{
		AgentType:   func() string { return PipelineAgentInspector },
		InspectorOT: true,
	})

	runSkill(t, ctx, skills, "process_validation", map[string]any{
		"challenge_id": "challenge-ready-partial",
		"decision":     "accept",
		"summary":      "accepted the tester audit because the remaining blockers were harness-only and the implementation audit passed",
	})

	result, err := callSkill(t, ctx, skills, "finalize_pipeline", map[string]any{
		"summary":       "the implementation is correct and the remaining execution caveats are environmental only",
		"evidence_refs": []string{"artifact:inspector", "tests/test_init.py"},
	})
	if err != nil {
		t.Fatalf("finalize_pipeline after accepted partial process_validation: %v", err)
	}
	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("finalize_pipeline result = %#v, want map", result)
	}
	if resultMap["ready_for_ot"] != true {
		t.Fatalf("finalize_pipeline result = %#v, want ready_for_ot=true", result)
	}
	if resultMap["must_handoff_to_ot"] != true {
		t.Fatalf("finalize_pipeline result = %#v, want must_handoff_to_ot=true", result)
	}
	if resultMap["required_next_action"] != "handoff_to_ot" {
		t.Fatalf("finalize_pipeline result = %#v, want required_next_action=handoff_to_ot", result)
	}
}

func TestWithPipelineTaskProtocolState_RehydratesSnapshotFromTaskContext(t *testing.T) {
	task := &PipelineTaskInput{
		Context: map[string]any{
			"pipeline_protocol": map[string]any{
				"iteration": 2,
				"roster": []any{
					map[string]any{"agent_type": PipelineAgentInspector},
					map[string]any{"agent_type": PipelineAgentTester},
				},
				"active_agents": []any{PipelineAgentInspector},
				"pending_challenge": map[string]any{
					"id":               "challenge-1",
					"requesting_agent": PipelineAgentInspector,
					"target_agents":    []any{PipelineAgentTester},
				},
			},
		},
	}

	ctx := WithPipelineTaskProtocolState(context.Background(), task)
	state := PipelineProtocolStateFromContext(ctx)
	if state == nil {
		t.Fatal("expected pipeline protocol state in context")
	}
	snapshot := state.Snapshot()
	if snapshot == nil {
		t.Fatal("expected pipeline protocol snapshot")
	}
	if snapshot.Iteration != 2 {
		t.Fatalf("iteration = %d, want 2", snapshot.Iteration)
	}
	if len(snapshot.ActiveAgents) != 1 || snapshot.ActiveAgents[0] != PipelineAgentInspector {
		t.Fatalf("active agents = %#v", snapshot.ActiveAgents)
	}
	if snapshot.PendingChallenge == nil || snapshot.PendingChallenge.ID != "challenge-1" {
		t.Fatalf("pending challenge = %#v", snapshot.PendingChallenge)
	}

	ctx = WithTaskExecutionContract(ctx, &TaskExecutionContract{RuntimeAgentType: PipelineAgentInspector})
	localCtx := WithPipelineProtocolState(context.Background(), state)
	localCtx = WithTaskExecutionContract(localCtx, &TaskExecutionContract{RuntimeAgentType: PipelineAgentInspector})
	skills := PipelineProtocolSkills(PipelineProtocolSkillConfig{
		AgentType: func() string { return PipelineAgentInspector },
	})
	runSkill(t, localCtx, skills, "handoff_next", map[string]any{
		"target_agents": []string{"tester"},
		"reason":        "begin TDD red phase",
		"request":       "Author the failing tests for the defined criteria.",
	})
	action := state.TerminalAction()
	if action == nil || action.Type != PipelineProtocolActionHandoff {
		t.Fatalf("terminal action = %#v, want handoff", action)
	}
}

func TestPipelineProtocolSkills_HandoffNextPublishesGuideRoute(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	channels := guide.NewAgentChannels(PipelineAgentInspector, "inspector-1")
	routeCh := make(chan *guide.RouteRequest, 1)
	rerouteCh := make(chan map[string]string, 1)
	updateCh := make(chan map[string]any, 1)

	reqSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		select {
		case routeCh <- req:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer reqSub.Unsubscribe()

	updateSub, err := bus.SubscribeAsync("pipeline.update."+PipelineAgentTester, func(msg *guide.Message) error {
		payload := pipelineUpdatePayloadFromMessage(msg)
		if payload == nil {
			return nil
		}
		select {
		case updateCh <- payload:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe pipeline updates: %v", err)
	}
	defer updateSub.Unsubscribe()

	streamSub, err := bus.SubscribeAsync(channels.Responses, func(msg *guide.Message) error {
		stream, ok := msg.GetStreamResponse()
		if !ok || stream == nil || stream.Event == nil || stream.Event.Type != guide.StreamEventReroute {
			return nil
		}
		data, _ := stream.Event.Data.(map[string]string)
		select {
		case rerouteCh <- data:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe reroute stream: %v", err)
	}
	defer streamSub.Unsubscribe()

	task := &PipelineTaskInput{
		TaskID:    "task-async",
		AgentType: PipelineAgentInspector,
		Prompt:    "Inspect the task and hand off test work.",
		SessionID: "session-1",
		Context: map[string]any{
			"pipeline_stage": "inspect",
			"pipeline_protocol": PipelineProtocolSnapshotMap(&PipelineProtocolSnapshot{
				Roster: []PipelineProtocolAgent{
					{AgentType: PipelineAgentInspector},
					{AgentType: PipelineAgentTester},
					{AgentType: PipelineAgentEngineer},
				},
				ActiveAgents: []string{PipelineAgentInspector},
			}),
		},
	}
	ctx := WithPipelineTaskProtocolState(context.Background(), task)
	ctx = WithTaskExecutionContract(ctx, &TaskExecutionContract{RuntimeAgentType: PipelineAgentInspector})
	ctx = WithStreamContext(ctx, "corr-inspector", "tui")

	skills := PipelineProtocolSkills(PipelineProtocolSkillConfig{
		AgentType: func() string { return PipelineAgentInspector },
		AgentID:   func() string { return "inspector-1" },
		Route: PipelineProtocolRouteConfig{
			BusProvider: func() guide.EventBus { return bus },
			SessionID:   func() string { return task.SessionID },
			PublishReroute: func(ctx context.Context, toAgentID, reason, newCorrelationID string) {
				PublishPipelineHandoffReroute(bus, channels, ctx, PipelineAgentInspector, toAgentID, reason, newCorrelationID)
			},
		},
	})

	result, err := callSkill(t, ctx, skills, "handoff_next", map[string]any{
		"target_agents":   []string{"tester"},
		"reason":          "testing should verify the criteria next",
		"request":         "Author the failing tests for the agreed contract.",
		"required_output": []string{"failing tests"},
		"references":      []string{"tests/auth_test.go"},
	})
	if err != nil {
		t.Fatalf("handoff_next error = %v", err)
	}
	resultMap, _ := result.(map[string]any)
	if resultMap == nil {
		t.Fatalf("handoff_next result = %#v, want result map", result)
	}
	if resultMap["forwarded"] != true {
		t.Fatalf("handoff_next result = %#v, want forwarded=true", result)
	}

	state := PipelineProtocolStateFromContext(ctx)
	if state == nil {
		t.Fatal("expected pipeline protocol state")
	}
	action := state.TerminalAction()
	if action == nil || action.Type != PipelineProtocolActionHandoff {
		t.Fatalf("terminal action = %#v, want handoff", action)
	}
	if action.CreatesChallenge {
		t.Fatalf("handoff_next should not create a challenge action: %#v", action)
	}
	if action.Request != "Author the failing tests for the agreed contract." {
		t.Fatalf("request = %q", action.Request)
	}
	if len(action.TargetAgents) != 1 || action.TargetAgents[0] != PipelineAgentTester {
		t.Fatalf("target_agents = %#v, want [tester-pipeline]", action.TargetAgents)
	}

	select {
	case req := <-routeCh:
		require.NotNil(t, req)
		assert.Equal(t, "corr-inspector", req.ParentCorrelationID)
		assert.Equal(t, PipelineWorkerRoutingTarget("task-async", PipelineAgentTester), req.TargetAgentID)
		assert.NotEqual(t, true, req.Metadata[streamMetadataNestedBranch])
		if preserved, _ := req.Metadata["chat_preserve_source_stream_target"].(bool); !preserved {
			t.Fatalf("chat_preserve_source_stream_target = %#v, want true", req.Metadata["chat_preserve_source_stream_target"])
		}

		var nextTask PipelineTaskInput
		require.NoError(t, json.Unmarshal([]byte(req.Input), &nextTask))
		assert.Equal(t, PipelineAgentTester, nextTask.AgentType)
		assert.Equal(t, "Author the failing tests for the agreed contract.", PipelineCurrentRequest(&nextTask))

		snapshot, err := PipelineProtocolSnapshotFromTask(&nextTask)
		require.NoError(t, err)
		require.NotNil(t, snapshot)
		assert.Equal(t, []string{PipelineAgentTester}, snapshot.ActiveAgents)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for top-level handoff route")
	}

	reroute := waitForReroute(t, rerouteCh)
	if reroute["from_agent"] != PipelineAgentInspector {
		t.Fatalf("from_agent = %q, want %q", reroute["from_agent"], PipelineAgentInspector)
	}
	if reroute["to_agent"] != PipelineAgentTester {
		t.Fatalf("to_agent = %q, want %q", reroute["to_agent"], PipelineAgentTester)
	}

	update := waitForPipelineUpdate(t, updateCh)
	if update["status"] != "running" {
		t.Fatalf("status = %#v, want running", update["status"])
	}
	if update["stage"] != "test" {
		t.Fatalf("stage = %#v, want test", update["stage"])
	}
	if update["message"] != "Author the failing tests for the agreed contract." {
		t.Fatalf("message = %#v", update["message"])
	}
}

func TestPipelineProtocolSkills_ChallengeAgentPublishesGuideRoute(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	channels := guide.NewAgentChannels(PipelineAgentEngineer, "engineer-1")
	routeCh := make(chan *guide.RouteRequest, 1)
	rerouteCh := make(chan map[string]string, 1)

	reqSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		select {
		case routeCh <- req:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer reqSub.Unsubscribe()

	streamSub, err := bus.SubscribeAsync(channels.Responses, func(msg *guide.Message) error {
		stream, ok := msg.GetStreamResponse()
		if !ok || stream == nil || stream.Event == nil || stream.Event.Type != guide.StreamEventReroute {
			return nil
		}
		data, _ := stream.Event.Data.(map[string]string)
		select {
		case rerouteCh <- data:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe reroute stream: %v", err)
	}
	defer streamSub.Unsubscribe()

	task := &PipelineTaskInput{
		TaskID:    "task-challenge",
		AgentType: PipelineAgentEngineer,
		Prompt:    "Challenge the tester on edge cases.",
		SessionID: "session-3",
		Context: map[string]any{
			"pipeline_stage": "execute",
			"session_dir":    t.TempDir(),
			"pipeline_protocol": PipelineProtocolSnapshotMap(&PipelineProtocolSnapshot{
				Roster: []PipelineProtocolAgent{
					{AgentType: PipelineAgentInspector},
					{AgentType: PipelineAgentTester},
					{AgentType: PipelineAgentEngineer},
				},
				ActiveAgents: []string{PipelineAgentEngineer},
			}),
		},
	}
	ctx := WithPipelineTaskProtocolState(context.Background(), task)
	ctx = WithTaskExecutionContract(ctx, &TaskExecutionContract{RuntimeAgentType: PipelineAgentEngineer})
	ctx = WithStreamContext(ctx, "corr-engineer", "tui")

	skills := PipelineProtocolSkills(PipelineProtocolSkillConfig{
		AgentType: func() string { return PipelineAgentEngineer },
		AgentID:   func() string { return "engineer-1" },
		Route: PipelineProtocolRouteConfig{
			BusProvider: func() guide.EventBus { return bus },
			SessionID:   func() string { return task.SessionID },
			PublishReroute: func(ctx context.Context, toAgentID, reason, newCorrelationID string) {
				PublishPipelineHandoffReroute(bus, channels, ctx, PipelineAgentEngineer, toAgentID, reason, newCorrelationID)
			},
		},
	})

	result, err := callSkill(t, ctx, skills, "challenge_agent", map[string]any{
		"target_agents":   []string{"tester"},
		"reason":          "Need failing edge-case coverage before implementation continues.",
		"request":         "Add a regression test for the nil-config path.",
		"required_output": []string{"failing regression test"},
		"references":      []string{"pkg/config/config_test.go"},
	})
	if err != nil {
		t.Fatalf("challenge_agent error = %v", err)
	}
	resultMap, _ := result.(map[string]any)
	if resultMap == nil || resultMap["forwarded"] != true {
		t.Fatalf("challenge_agent result = %#v, want forwarded=true", result)
	}

	req := waitForRouteRequest(t, routeCh)
	if req.ParentCorrelationID != "corr-engineer" {
		t.Fatalf("parent_correlation_id = %q, want corr-engineer", req.ParentCorrelationID)
	}
	if req.TargetAgentID != PipelineWorkerRoutingTarget("task-challenge", PipelineAgentTester) {
		t.Fatalf("target_agent_id = %q", req.TargetAgentID)
	}
	if preserved, _ := req.Metadata["chat_preserve_source_stream_target"].(bool); !preserved {
		t.Fatalf("chat_preserve_source_stream_target = %#v, want true", req.Metadata["chat_preserve_source_stream_target"])
	}

	var nextTask PipelineTaskInput
	if err := json.Unmarshal([]byte(req.Input), &nextTask); err != nil {
		t.Fatalf("decode next task: %v", err)
	}
	snapshot, err := PipelineProtocolSnapshotFromTask(&nextTask)
	if err != nil {
		t.Fatalf("PipelineProtocolSnapshotFromTask: %v", err)
	}
	if snapshot == nil || snapshot.PendingChallenge == nil {
		t.Fatalf("pending challenge = %#v, want value", snapshot)
	}
	if snapshot.PendingChallenge.RequestingAgent != PipelineAgentEngineer {
		t.Fatalf("requesting_agent = %q, want %q", snapshot.PendingChallenge.RequestingAgent, PipelineAgentEngineer)
	}
	if snapshot.PendingChallenge.RequestingAgentID != "engineer-1" {
		t.Fatalf("requesting_agent_id = %q, want %q", snapshot.PendingChallenge.RequestingAgentID, "engineer-1")
	}
	if req.Metadata[streamMetadataNestedBranch] != true {
		t.Fatalf("chat_nested_branch = %#v, want true", req.Metadata[streamMetadataNestedBranch])
	}
	if got, _ := req.Metadata[streamMetadataParentCorrelation].(string); got != "corr-engineer" {
		t.Fatalf("chat_parent_correlation_id = %q, want corr-engineer", got)
	}
	if got, _ := req.Metadata[streamMetadataInterAgentKind].(string); got != InterAgentToolEventKindChallenge {
		t.Fatalf("chat_inter_agent_kind = %q, want %q", got, InterAgentToolEventKindChallenge)
	}
	if got, _ := req.Metadata[streamMetadataInterAgentThread].(string); got != pipelineThreadPrefix+snapshot.PendingChallenge.ID {
		t.Fatalf("chat_inter_agent_thread = %q, want %q", got, pipelineThreadPrefix+snapshot.PendingChallenge.ID)
	}

	reroute := waitForReroute(t, rerouteCh)
	if reroute["from_agent"] != PipelineAgentEngineer {
		t.Fatalf("from_agent = %q, want %q", reroute["from_agent"], PipelineAgentEngineer)
	}
	if reroute["to_agent"] != PipelineAgentTester {
		t.Fatalf("to_agent = %q, want %q", reroute["to_agent"], PipelineAgentTester)
	}
	if reroute["new_correlation_id"] != req.CorrelationID {
		t.Fatalf("new_correlation_id = %q, want %q", reroute["new_correlation_id"], req.CorrelationID)
	}
}

func TestBuildPipelineHandoffTasks_HandoffNextClearsPendingChallenge(t *testing.T) {
	state := NewPipelineProtocolState(&PipelineProtocolSnapshot{
		Roster: []PipelineProtocolAgent{
			{AgentType: PipelineAgentInspector},
			{AgentType: PipelineAgentTester},
			{AgentType: PipelineAgentEngineer},
		},
		ActiveAgents: []string{PipelineAgentTester},
		RequestedBy:  PipelineAgentInspector,
		PendingChallenge: &PipelineProtocolChallenge{
			ID:              "challenge-red-phase",
			RequestingAgent: PipelineAgentInspector,
			TargetAgents:    []string{PipelineAgentTester},
			Request:         "Define the failing test surface and route execution.",
		},
	})
	task := &PipelineTaskInput{
		TaskID:    "task-auth",
		AgentType: PipelineAgentTester,
		Context:   map[string]any{},
	}
	action := &PipelineTurnAction{
		Type:             PipelineProtocolActionHandoff,
		AgentType:        PipelineAgentTester,
		TargetAgents:     []string{PipelineAgentEngineer},
		Mode:             PipelineTurnModeSingle,
		Reason:           "Implementation should proceed against the red tests.",
		Request:          "Implement the auth fix and satisfy the failing test.",
		CreatesChallenge: false,
	}

	tasks, err := buildPipelineHandoffTasks(state, task, action)
	if err != nil {
		t.Fatalf("buildPipelineHandoffTasks error = %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("task count = %d, want 1", len(tasks))
	}

	snapshot, err := PipelineProtocolSnapshotFromTask(tasks[0])
	if err != nil {
		t.Fatalf("PipelineProtocolSnapshotFromTask: %v", err)
	}
	if snapshot == nil {
		t.Fatalf("snapshot = %#v, want value", snapshot)
	}
	if snapshot.PendingChallenge != nil {
		t.Fatalf("pending challenge = %#v, want nil", snapshot.PendingChallenge)
	}
	if snapshot.RequestedBy != PipelineAgentTester {
		t.Fatalf("requested_by = %q, want %q", snapshot.RequestedBy, PipelineAgentTester)
	}
}

func TestBuildPipelineHandoffTasks_ChallengeAgentCreatesPendingChallenge(t *testing.T) {
	state := NewPipelineProtocolState(&PipelineProtocolSnapshot{
		Roster: []PipelineProtocolAgent{
			{AgentType: PipelineAgentInspector},
			{AgentType: PipelineAgentTester},
			{AgentType: PipelineAgentEngineer},
		},
		ActiveAgents: []string{PipelineAgentTester},
	})
	task := &PipelineTaskInput{
		TaskID:    "task-auth",
		AgentType: PipelineAgentTester,
		Context:   map[string]any{},
	}
	action := &PipelineTurnAction{
		Type:             PipelineProtocolActionHandoff,
		AgentType:        PipelineAgentTester,
		TargetAgents:     []string{PipelineAgentEngineer},
		Mode:             PipelineTurnModeSingle,
		Reason:           "Tester is explicitly requesting another engineer pass.",
		Request:          "Adjust the implementation for the missing edge case.",
		CreatesChallenge: true,
		ChallengeID:      "challenge-tester",
	}

	tasks, err := buildPipelineHandoffTasks(state, task, action)
	if err != nil {
		t.Fatalf("buildPipelineHandoffTasks error = %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("task count = %d, want 1", len(tasks))
	}

	snapshot, err := PipelineProtocolSnapshotFromTask(tasks[0])
	if err != nil {
		t.Fatalf("PipelineProtocolSnapshotFromTask: %v", err)
	}
	if snapshot == nil || snapshot.PendingChallenge == nil {
		t.Fatalf("pending challenge = %#v, want value", snapshot)
	}
	if snapshot.PendingChallenge.RequestingAgent != PipelineAgentTester {
		t.Fatalf("requesting_agent = %q, want %q", snapshot.PendingChallenge.RequestingAgent, PipelineAgentTester)
	}
}

func TestBuildPipelineHandoffTasks_CompactsProtocolSnapshotForTaskPayload(t *testing.T) {
	state := NewPipelineProtocolState(&PipelineProtocolSnapshot{
		Roster: []PipelineProtocolAgent{
			{AgentType: PipelineAgentInspector},
			{AgentType: PipelineAgentTester},
			{AgentType: PipelineAgentEngineer},
		},
		ActiveAgents: []string{PipelineAgentInspector},
	})
	task := &PipelineTaskInput{
		TaskID:    "task-compact",
		AgentType: PipelineAgentInspector,
		Context:   map[string]any{},
	}
	longReason := strings.Repeat("reason ", 200)
	longRequest := strings.Repeat("request ", 400)
	action := &PipelineTurnAction{
		Type:             PipelineProtocolActionHandoff,
		AgentType:        PipelineAgentInspector,
		TargetAgents:     []string{PipelineAgentTester},
		Mode:             PipelineTurnModeSingle,
		Reason:           longReason,
		Request:          longRequest,
		CreatesChallenge: true,
		ChallengeID:      "challenge-compact",
	}

	tasks, err := buildPipelineHandoffTasks(state, task, action)
	if err != nil {
		t.Fatalf("buildPipelineHandoffTasks error = %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("task count = %d, want 1", len(tasks))
	}

	snapshot, err := PipelineProtocolSnapshotFromTask(tasks[0])
	if err != nil {
		t.Fatalf("PipelineProtocolSnapshotFromTask: %v", err)
	}
	if snapshot == nil || snapshot.PendingChallenge == nil {
		t.Fatalf("snapshot = %#v, want pending challenge", snapshot)
	}
	if got := len(snapshot.CurrentRequest); got > maxPipelineProtocolRequestLen {
		t.Fatalf("current_request len = %d, want <= %d", got, maxPipelineProtocolRequestLen)
	}
	if got := len(snapshot.PendingChallenge.Reason); got > maxPipelineProtocolReasonLen {
		t.Fatalf("challenge reason len = %d, want <= %d", got, maxPipelineProtocolReasonLen)
	}
	if got := len(snapshot.PendingChallenge.Request); got > maxPipelineProtocolRequestLen {
		t.Fatalf("challenge request len = %d, want <= %d", got, maxPipelineProtocolRequestLen)
	}
	if len(snapshot.RecentEvents) == 0 {
		t.Fatal("expected recent protocol event")
	}
	if got := len(snapshot.RecentEvents[len(snapshot.RecentEvents)-1].Summary); got > maxPipelineProtocolEventSummaryLen {
		t.Fatalf("recent event summary len = %d, want <= %d", got, maxPipelineProtocolEventSummaryLen)
	}
}

func TestBuildPipelineValidationTask_CompactsValidationSummaryForTaskPayload(t *testing.T) {
	state := NewPipelineProtocolState(&PipelineProtocolSnapshot{
		Roster: []PipelineProtocolAgent{
			{AgentType: PipelineAgentInspector},
			{AgentType: PipelineAgentTester},
		},
		ActiveAgents: []string{PipelineAgentTester},
	})
	task := &PipelineTaskInput{
		TaskID:    "task-validate-compact",
		AgentType: PipelineAgentTester,
		Context:   map[string]any{},
	}
	record := &PipelineValidationRecord{
		ChallengeID:         "challenge-1",
		RequestingAgent:     PipelineAgentInspector,
		RequestingAgentID:   "inspector-runtime-1",
		RespondingAgent:     PipelineAgentTester,
		RespondingAgentID:   "tester-runtime-1",
		Status:              string(PipelineValidationPassed),
		Summary:             strings.Repeat("summary ", 400),
		ChallengeRequest:    strings.Repeat("request ", 300),
		ChallengeReferences: []string{strings.Repeat("ref", 120)},
		EvidenceRefs:        []string{strings.Repeat("evidence", 80)},
	}

	next, err := buildPipelineValidationTask(state, task, record)
	if err != nil {
		t.Fatalf("buildPipelineValidationTask error = %v", err)
	}

	snapshot, err := PipelineProtocolSnapshotFromTask(next)
	if err != nil {
		t.Fatalf("PipelineProtocolSnapshotFromTask: %v", err)
	}
	if snapshot == nil || snapshot.PendingValidation == nil {
		t.Fatalf("snapshot = %#v, want pending validation", snapshot)
	}
	if got := len(snapshot.PendingValidation.Summary); got > maxPipelineProtocolSummaryLen {
		t.Fatalf("validation summary len = %d, want <= %d", got, maxPipelineProtocolSummaryLen)
	}
	if got := len(snapshot.PendingValidation.ChallengeRequest); got > maxPipelineProtocolRequestLen {
		t.Fatalf("challenge request len = %d, want <= %d", got, maxPipelineProtocolRequestLen)
	}
	if len(snapshot.PendingValidation.ChallengeReferences) != 1 || len(snapshot.PendingValidation.ChallengeReferences[0]) > maxPipelineProtocolReferenceLen {
		t.Fatalf("challenge references = %#v, want compact values", snapshot.PendingValidation.ChallengeReferences)
	}
	if len(snapshot.PendingValidation.EvidenceRefs) != 1 || len(snapshot.PendingValidation.EvidenceRefs[0]) > maxPipelineProtocolReferenceLen {
		t.Fatalf("evidence refs = %#v, want compact values", snapshot.PendingValidation.EvidenceRefs)
	}
}

func TestBuildPipelineValidationTask_RequiresExactRequestingAgentID(t *testing.T) {
	state := NewPipelineProtocolState(&PipelineProtocolSnapshot{
		Roster: []PipelineProtocolAgent{
			{AgentType: PipelineAgentInspector},
			{AgentType: PipelineAgentTester},
		},
		ActiveAgents: []string{PipelineAgentTester},
	})
	task := &PipelineTaskInput{
		TaskID:    "task-validate-missing-id",
		AgentType: PipelineAgentTester,
		Context:   map[string]any{},
	}
	record := &PipelineValidationRecord{
		ChallengeID:       "challenge-1",
		RequestingAgent:   PipelineAgentInspector,
		RespondingAgent:   PipelineAgentTester,
		RespondingAgentID: "tester-runtime-1",
		Status:            string(PipelineValidationPassed),
		Summary:           "Validation passed.",
	}

	_, err := buildPipelineValidationTask(state, task, record)
	if err == nil || !strings.Contains(err.Error(), "missing the exact requesting agent id") {
		t.Fatalf("buildPipelineValidationTask error = %v, want missing requester id failure", err)
	}
}

func TestPipelineProtocolSkills_ValidateWorkPublishesGuideRoute(t *testing.T) {
	sessionDir := t.TempDir()
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	channels := guide.NewAgentChannels(PipelineAgentTester, "tester-1")
	routeCh := make(chan *guide.RouteRequest, 1)
	rerouteCh := make(chan map[string]string, 1)
	updateCh := make(chan map[string]any, 1)

	reqSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		select {
		case routeCh <- req:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer reqSub.Unsubscribe()

	updateSub, err := bus.SubscribeAsync("pipeline.update."+PipelineAgentInspector, func(msg *guide.Message) error {
		payload := pipelineUpdatePayloadFromMessage(msg)
		if payload == nil {
			return nil
		}
		select {
		case updateCh <- payload:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe pipeline updates: %v", err)
	}
	defer updateSub.Unsubscribe()

	streamSub, err := bus.SubscribeAsync(channels.Responses, func(msg *guide.Message) error {
		stream, ok := msg.GetStreamResponse()
		if !ok || stream == nil || stream.Event == nil || stream.Event.Type != guide.StreamEventReroute {
			return nil
		}
		data, _ := stream.Event.Data.(map[string]string)
		select {
		case rerouteCh <- data:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe reroute stream: %v", err)
	}
	defer streamSub.Unsubscribe()

	task := &PipelineTaskInput{
		TaskID:    "task-validate",
		AgentType: PipelineAgentTester,
		Prompt:    "Validate the inspector challenge.",
		SessionID: "session-2",
		Context: map[string]any{
			"session_dir":    sessionDir,
			"pipeline_stage": "test",
			"pipeline_protocol": PipelineProtocolSnapshotMap(&PipelineProtocolSnapshot{
				Roster: []PipelineProtocolAgent{
					{AgentType: PipelineAgentInspector},
					{AgentType: PipelineAgentTester},
				},
				ActiveAgents: []string{PipelineAgentTester},
				PendingChallenge: &PipelineProtocolChallenge{
					ID:                "challenge-1",
					RequestingAgent:   PipelineAgentInspector,
					RequestingAgentID: "inspector-runtime-1",
					TargetAgents:      []string{PipelineAgentTester},
					Request:           "Author and run the validating tests.",
				},
			}),
		},
	}
	ctx := WithPipelineTaskProtocolState(context.Background(), task)
	ctx = WithTaskExecutionContract(ctx, &TaskExecutionContract{RuntimeAgentType: PipelineAgentTester})
	ctx = WithStreamContext(ctx, "corr-tester", "tui")
	// Simulate the branch metadata the tester would have inherited from the
	// inspector's outbound challenge (BeginInterAgentBranch stamps
	// chat_parent_correlation_id = originator's CID). Without this, the
	// validate_work route has no originator to continue and emits a plain
	// route; with it, the validate_work route carries continuation metadata.
	ctx = WithStreamContextMetadata(ctx, map[string]any{
		streamMetadataParentCorrelation: "corr-inspector",
	})

	skills := PipelineProtocolSkills(PipelineProtocolSkillConfig{
		AgentType: func() string { return PipelineAgentTester },
		AgentID:   func() string { return "tester-1" },
		Route: PipelineProtocolRouteConfig{
			BusProvider: func() guide.EventBus { return bus },
			SessionID:   func() string { return task.SessionID },
			PublishReroute: func(ctx context.Context, toAgentID, reason, newCorrelationID string) {
				PublishPipelineHandoffReroute(bus, channels, ctx, PipelineAgentTester, toAgentID, reason, newCorrelationID)
			},
		},
	})

	result, err := callSkill(t, ctx, skills, "validate_work", map[string]any{
		"challenge_id":            "challenge-1",
		"requesting_agent":        "inspector",
		"status":                  "passed",
		"summary":                 "Tests are implemented and passing.",
		"evidence_refs":           []string{"tests/auth_test.go"},
		"recommended_next_agents": []string{"inspector"},
	})
	if err != nil {
		t.Fatalf("validate_work error = %v", err)
	}
	typed, ok := result.(*PipelineValidationResult)
	if !ok || typed == nil || !typed.Forwarded {
		t.Fatalf("validate_work result = %#v, want *PipelineValidationResult with Forwarded=true", result)
	}

	req := waitForRouteRequest(t, routeCh)
	if req.ParentCorrelationID != "corr-tester" {
		t.Fatalf("parent_correlation_id = %q, want corr-tester", req.ParentCorrelationID)
	}
	if req.Metadata["chat_nested_branch"] == true {
		t.Fatalf("validate_work should not stamp nested chat branch metadata: %#v", req.Metadata)
	}
	// Continuation semantics: the route points the TUI at the originator's
	// existing entry (continuation key = inspector's CID) and the responder
	// that triggered this resumption (parent correlation = tester's CID).
	// Top-level-transfer is NOT set — the originator resumes inline.
	if got, _ := req.Metadata[streamMetadataOriginatorContinuation].(string); got != "corr-inspector" {
		t.Fatalf("chat_continuation_of_correlation_id = %q, want corr-inspector", got)
	}
	if got, _ := req.Metadata[streamMetadataParentCorrelation].(string); got != "corr-tester" {
		t.Fatalf("chat_parent_correlation_id = %q, want corr-tester", got)
	}
	if _, set := req.Metadata[streamMetadataTopLevelTransfer]; set {
		t.Fatalf("chat_top_level_transfer should be absent on continuation route, got %#v", req.Metadata[streamMetadataTopLevelTransfer])
	}
	if preserved, _ := req.Metadata["chat_preserve_source_stream_target"].(bool); !preserved {
		t.Fatalf("chat_preserve_source_stream_target = %#v, want true", req.Metadata["chat_preserve_source_stream_target"])
	}
	if req.TargetAgentID != "inspector-runtime-1" {
		t.Fatalf("target_agent_id = %q", req.TargetAgentID)
	}

	var nextTask PipelineTaskInput
	if err := json.Unmarshal([]byte(req.Input), &nextTask); err != nil {
		t.Fatalf("decode next task: %v", err)
	}
	if nextTask.AgentType != PipelineAgentInspector {
		t.Fatalf("next agent_type = %q, want %q", nextTask.AgentType, PipelineAgentInspector)
	}
	snapshot, err := PipelineProtocolSnapshotFromTask(&nextTask)
	if err != nil {
		t.Fatalf("PipelineProtocolSnapshotFromTask: %v", err)
	}
	if snapshot == nil || snapshot.PendingValidation == nil {
		t.Fatalf("pending_validation = %#v, want value", snapshot)
	}
	if snapshot.PendingValidation.RequestingAgentID != "inspector-runtime-1" {
		t.Fatalf("requesting_agent_id = %q, want %q", snapshot.PendingValidation.RequestingAgentID, "inspector-runtime-1")
	}
	if snapshot.PendingValidation.RespondingAgent != PipelineAgentTester {
		t.Fatalf("responding_agent = %q, want %q", snapshot.PendingValidation.RespondingAgent, PipelineAgentTester)
	}
	if snapshot.PendingValidation.RespondingAgentID != "tester-1" {
		t.Fatalf("responding_agent_id = %q, want %q", snapshot.PendingValidation.RespondingAgentID, "tester-1")
	}
	if len(snapshot.ActiveAgents) != 1 || snapshot.ActiveAgents[0] != PipelineAgentInspector {
		t.Fatalf("active_agents = %#v, want inspector", snapshot.ActiveAgents)
	}

	reroute := waitForReroute(t, rerouteCh)
	if reroute["from_agent"] != PipelineAgentTester {
		t.Fatalf("from_agent = %q, want %q", reroute["from_agent"], PipelineAgentTester)
	}
	if reroute["to_agent"] != PipelineAgentInspector {
		t.Fatalf("to_agent = %q, want %q", reroute["to_agent"], PipelineAgentInspector)
	}
	if reroute["new_correlation_id"] != req.CorrelationID {
		t.Fatalf("new_correlation_id = %q, want %q", reroute["new_correlation_id"], req.CorrelationID)
	}

	update := waitForPipelineUpdate(t, updateCh)
	if update["status"] != "running" {
		t.Fatalf("status = %#v, want running", update["status"])
	}
	if update["stage"] != "inspect" {
		t.Fatalf("stage = %#v, want inspect", update["stage"])
	}
	if update["message"] != "Process validation response for challenge challenge-1 and decide the next handoff." {
		t.Fatalf("message = %#v", update["message"])
	}
}

func TestPipelineProtocolSkills_FinalizePipelineChallengesTester(t *testing.T) {
	sessionDir := t.TempDir()
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	channels := guide.NewAgentChannels(PipelineAgentInspector, "inspector-verify")
	routeCh := make(chan *guide.RouteRequest, 1)
	rerouteCh := make(chan map[string]string, 1)
	updateCh := make(chan map[string]any, 1)

	reqSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		select {
		case routeCh <- req:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer reqSub.Unsubscribe()

	updateSub, err := bus.SubscribeAsync("pipeline.update."+PipelineAgentTester, func(msg *guide.Message) error {
		payload := pipelineUpdatePayloadFromMessage(msg)
		if payload == nil {
			return nil
		}
		select {
		case updateCh <- payload:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe pipeline updates: %v", err)
	}
	defer updateSub.Unsubscribe()

	streamSub, err := bus.SubscribeAsync(channels.Responses, func(msg *guide.Message) error {
		stream, ok := msg.GetStreamResponse()
		if !ok || stream == nil || stream.Event == nil || stream.Event.Type != guide.StreamEventReroute {
			return nil
		}
		data, _ := stream.Event.Data.(map[string]string)
		select {
		case rerouteCh <- data:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe reroute stream: %v", err)
	}
	defer streamSub.Unsubscribe()

	task := &PipelineTaskInput{
		TaskID:        "task-finalize-gate",
		AgentType:     PipelineAgentInspector,
		TargetAgentID: PipelineWorkerRoutingTarget("task-finalize-gate", PipelineAgentInspector),
		Prompt:        "Finalize the accepted pipeline.",
		SessionID:     "session-finalize-gate",
		Context: map[string]any{
			"session_dir":    sessionDir,
			"pipeline_stage": "inspect",
			"pipeline_protocol": PipelineProtocolSnapshotMap(&PipelineProtocolSnapshot{
				Roster: []PipelineProtocolAgent{
					{AgentType: PipelineAgentInspector},
					{AgentType: PipelineAgentTester},
					{AgentType: PipelineAgentEngineer},
					{AgentType: PipelineAgentDesigner},
				},
				ActiveAgents: []string{PipelineAgentInspector},
			}),
		},
	}
	ctx := WithPipelineTaskProtocolState(context.Background(), task)
	ctx = WithTaskExecutionContract(ctx, &TaskExecutionContract{RuntimeAgentType: PipelineAgentInspector})
	ctx = WithStreamContext(ctx, "corr-finalize-gate", "tui")

	skills := PipelineProtocolSkills(PipelineProtocolSkillConfig{
		AgentType:   func() string { return PipelineAgentInspector },
		AgentID:     func() string { return "inspector-finalize-1" },
		InspectorOT: true,
		Route: PipelineProtocolRouteConfig{
			BusProvider: func() guide.EventBus { return bus },
			SessionID:   func() string { return task.SessionID },
			PublishReroute: func(ctx context.Context, toAgentID, reason, newCorrelationID string) {
				PublishPipelineHandoffReroute(bus, channels, ctx, PipelineAgentInspector, toAgentID, reason, newCorrelationID)
			},
		},
	})

	result, err := callSkill(t, ctx, skills, "finalize_pipeline", map[string]any{
		"summary":       "Final verification required before OT handoff.",
		"evidence_refs": []string{"artifacts/criteria.md", "tests/auth_test.go"},
	})
	if err != nil {
		t.Fatalf("finalize_pipeline error = %v", err)
	}
	resultMap, _ := result.(map[string]any)
	if resultMap == nil || resultMap["verification_requested"] != true {
		t.Fatalf("finalize_pipeline result = %#v, want verification_requested=true", result)
	}
	if resultMap["finalize_pipeline"] != false {
		t.Fatalf("finalize_pipeline result = %#v, want finalize_pipeline=false during verification challenge", result)
	}

	req := waitForRouteRequest(t, routeCh)
	if req.TargetAgentID != PipelineWorkerRoutingTarget("task-finalize-gate", PipelineAgentTester) {
		t.Fatalf("target_agent_id = %q", req.TargetAgentID)
	}
	if req.Metadata[streamMetadataNestedBranch] != true {
		t.Fatalf("chat_nested_branch = %#v, want true", req.Metadata[streamMetadataNestedBranch])
	}

	var nextTask PipelineTaskInput
	if err := json.Unmarshal([]byte(req.Input), &nextTask); err != nil {
		t.Fatalf("decode next task: %v", err)
	}
	snapshot, err := PipelineProtocolSnapshotFromTask(&nextTask)
	if err != nil {
		t.Fatalf("PipelineProtocolSnapshotFromTask: %v", err)
	}
	if snapshot == nil || snapshot.PendingChallenge == nil {
		t.Fatalf("pending challenge = %#v, want value", snapshot)
	}
	if snapshot.PendingChallenge.RequestingAgentID != "inspector-finalize-1" {
		t.Fatalf("requesting_agent_id = %q, want %q", snapshot.PendingChallenge.RequestingAgentID, "inspector-finalize-1")
	}
	if snapshot.AuditLock == nil {
		t.Fatalf("audit_lock = %#v, want value", snapshot.AuditLock)
	}
	if snapshot.AuditLock.OwnerAgent != PipelineAgentInspector {
		t.Fatalf("audit_lock.owner_agent = %q, want %q", snapshot.AuditLock.OwnerAgent, PipelineAgentInspector)
	}
	if snapshot.AuditLock.Phase != PipelineAuditPhaseFinalizing {
		t.Fatalf("audit_lock.phase = %q, want %q", snapshot.AuditLock.Phase, PipelineAuditPhaseFinalizing)
	}
	if got, _ := req.Metadata[streamMetadataParentCorrelation].(string); got != "corr-finalize-gate" {
		t.Fatalf("chat_parent_correlation_id = %q, want corr-finalize-gate", got)
	}
	if got, _ := req.Metadata[streamMetadataInterAgentKind].(string); got != InterAgentToolEventKindChallenge {
		t.Fatalf("chat_inter_agent_kind = %q, want %q", got, InterAgentToolEventKindChallenge)
	}
	if got, _ := req.Metadata[streamMetadataInterAgentThread].(string); got != pipelineThreadPrefix+snapshot.PendingChallenge.ID {
		t.Fatalf("chat_inter_agent_thread = %q, want %q", got, pipelineThreadPrefix+snapshot.PendingChallenge.ID)
	}
	if !containsNormalizedString(snapshot.PendingChallenge.References, finalizePipelineVerificationReference) {
		t.Fatalf("challenge references = %#v, want finalize marker", snapshot.PendingChallenge.References)
	}
	if !strings.Contains(snapshot.PendingChallenge.Request, "quality production") && !strings.Contains(snapshot.PendingChallenge.Request, "agentic slop") {
		t.Fatalf("challenge request = %q, want production-quality/slop language", snapshot.PendingChallenge.Request)
	}
	if !strings.Contains(snapshot.PendingChallenge.Request, "tests add real value") {
		t.Fatalf("challenge request = %q, want test signal language", snapshot.PendingChallenge.Request)
	}

	reroute := waitForReroute(t, rerouteCh)
	if reroute["to_agent"] != PipelineAgentTester {
		t.Fatalf("to_agent = %q, want %q", reroute["to_agent"], PipelineAgentTester)
	}

	update := waitForPipelineUpdate(t, updateCh)
	if update["stage"] != "test" {
		t.Fatalf("stage = %#v, want test", update["stage"])
	}
	if update["status"] != "running" {
		t.Fatalf("status = %#v, want running", update["status"])
	}
}

func TestPipelineProtocolSkills_ChallengeAgentToInspectorRefusedDuringAuditLock(t *testing.T) {
	task := &PipelineTaskInput{
		TaskID:    "task-audit-lock",
		AgentType: PipelineAgentTester,
		Prompt:    "Challenge inspector during audit lock.",
		Context: map[string]any{
			"pipeline_stage": "test",
			"pipeline_protocol": PipelineProtocolSnapshotMap(&PipelineProtocolSnapshot{
				Roster: []PipelineProtocolAgent{
					{AgentType: PipelineAgentInspector},
					{AgentType: PipelineAgentTester},
					{AgentType: PipelineAgentEngineer},
				},
				ActiveAgents:   []string{PipelineAgentTester},
				RequestedBy:    PipelineAgentInspector,
				CurrentRequest: "Audit the implementation and return validation evidence.",
				AuditLock: &PipelineAuditLock{
					OwnerAgent: PipelineAgentInspector,
					Phase:      PipelineAuditPhaseFinalizing,
					Reason:     "Inspector is conducting terminal audit review.",
				},
			}),
		},
	}
	ctx := WithPipelineTaskProtocolState(context.Background(), task)
	ctx = WithTaskExecutionContract(ctx, &TaskExecutionContract{RuntimeAgentType: PipelineAgentTester})

	skills := PipelineProtocolSkills(PipelineProtocolSkillConfig{
		AgentType: func() string { return PipelineAgentTester },
	})

	result, err := callSkill(t, ctx, skills, "challenge_agent", map[string]any{
		"target_agents": []string{"inspector"},
		"reason":        "Need clarification before continuing.",
		"request":       "Clarify whether the flaky case is in scope.",
	})
	if err != nil {
		t.Fatalf("challenge_agent error = %v", err)
	}
	resultMap, _ := result.(map[string]any)
	if resultMap == nil || resultMap["refused"] != true {
		t.Fatalf("challenge_agent result = %#v, want refused=true", result)
	}
	if resultMap["refused_by"] != PipelineAgentInspector {
		t.Fatalf("refused_by = %#v, want %q", resultMap["refused_by"], PipelineAgentInspector)
	}
	if resultMap["audit_phase"] != PipelineAuditPhaseFinalizing {
		t.Fatalf("audit_phase = %#v, want %q", resultMap["audit_phase"], PipelineAuditPhaseFinalizing)
	}
	if resultMap["must_wait"] != true {
		t.Fatalf("must_wait = %#v, want true", resultMap["must_wait"])
	}

	state := PipelineProtocolStateFromContext(ctx)
	if state == nil {
		t.Fatal("pipeline protocol state missing from context")
	}
	action := state.TerminalAction()
	if action == nil || action.Type != PipelineProtocolActionRefusal {
		t.Fatalf("terminal action = %#v, want refusal", action)
	}
}

func TestPipelineProtocolSkills_ChallengeAgentRefusesRepeatedChallengeWithoutWorkspaceChange(t *testing.T) {
	views := stubPipelineProtocolWorkspaceViews{
		summary: &versioning.WorkspaceSummary{
			DefaultView:   versioning.WorkspaceViewPipeline,
			SourceOfTruth: versioning.WorkspaceViewDisk,
			PipelineID:    "task-repeat",
			Paths:         []string{"src/app.go"},
			ViewsAvailable: []versioning.WorkspaceView{
				versioning.WorkspaceViewDisk,
				versioning.WorkspaceViewGlobal,
				versioning.WorkspaceViewPipeline,
			},
			Entries: []versioning.WorkspacePathState{
				{
					Path:        "src/app.go",
					DefaultView: versioning.WorkspaceViewPipeline,
					Disk: versioning.WorkspaceLayerState{
						View:        versioning.WorkspaceViewDisk,
						Available:   true,
						Exists:      true,
						ContentHash: "disk-hash",
					},
					Global: &versioning.WorkspaceLayerState{
						View:        versioning.WorkspaceViewGlobal,
						Available:   true,
						Exists:      true,
						ContentHash: "global-hash",
					},
					Pipeline: &versioning.WorkspaceLayerState{
						View:        versioning.WorkspaceViewPipeline,
						Available:   true,
						Exists:      true,
						ContentHash: "pipeline-hash",
					},
					GlobalDiffersFromDisk:       true,
					GlobalDiffKnown:             true,
					PipelineDiffersFromDisk:     true,
					PipelineDiffFromDiskKnown:   true,
					PipelineDiffersFromGlobal:   true,
					PipelineDiffFromGlobalKnown: true,
				},
			},
		},
	}
	baseTask := &PipelineTaskInput{
		TaskID:    "task-repeat",
		AgentType: PipelineAgentInspector,
		Context: map[string]any{
			"pipeline_stage": "inspect",
			"affected_files": []any{"src/app.go"},
			"workspace": map[string]any{
				"write_set": []any{"src/app.go"},
			},
		},
	}
	fingerprint := pipelineChallengeFingerprint(resolvePipelineChallengeEvidence(
		WithPipelineTask(context.Background(), baseTask),
		PipelineProtocolSkillConfig{
			WorkspaceViews: func() versioning.WorkspaceViewAccess { return views },
		},
	))
	if fingerprint == "" {
		t.Fatal("expected workspace fingerprint")
	}

	task := &PipelineTaskInput{
		TaskID:    baseTask.TaskID,
		AgentType: baseTask.AgentType,
		Context: map[string]any{
			"pipeline_stage": "inspect",
			"affected_files": []any{"src/app.go"},
			"workspace": map[string]any{
				"write_set": []any{"src/app.go"},
			},
			"pipeline_protocol": PipelineProtocolSnapshotMap(&PipelineProtocolSnapshot{
				Roster: []PipelineProtocolAgent{
					{AgentType: PipelineAgentInspector},
					{AgentType: PipelineAgentTester},
				},
				ActiveAgents: []string{PipelineAgentInspector},
				RecentEvents: []PipelineProtocolEvent{
					{
						Type:                 string(PipelineProtocolActionHandoff),
						AgentType:            PipelineAgentInspector,
						Targets:              []string{PipelineAgentTester},
						Summary:              "Audit the implementation.",
						CreatesChallenge:     true,
						WorkspaceFingerprint: fingerprint,
					},
				},
			}),
		},
	}
	ctx := WithPipelineTaskProtocolState(context.Background(), task)
	ctx = WithTaskExecutionContract(ctx, &TaskExecutionContract{RuntimeAgentType: PipelineAgentInspector})

	skills := PipelineProtocolSkills(PipelineProtocolSkillConfig{
		AgentType:      func() string { return PipelineAgentInspector },
		WorkspaceViews: func() versioning.WorkspaceViewAccess { return views },
	})

	result, err := callSkill(t, ctx, skills, "challenge_agent", map[string]any{
		"target_agents": []string{"tester"},
		"reason":        "Need another audit pass.",
		"request":       "Audit the implementation again.",
	})
	if err != nil {
		t.Fatalf("challenge_agent error = %v", err)
	}
	resultMap, _ := result.(map[string]any)
	if resultMap == nil || resultMap["refused"] != true {
		t.Fatalf("challenge_agent result = %#v, want refused=true", result)
	}
	if resultMap["refused_by"] != "pipeline-protocol" {
		t.Fatalf("refused_by = %#v, want pipeline-protocol", resultMap["refused_by"])
	}
	if resultMap["must_wait"] != true {
		t.Fatalf("must_wait = %#v, want true", resultMap["must_wait"])
	}
	if !strings.Contains(fmt.Sprint(resultMap["reason"]), "fresh workspace evidence") {
		t.Fatalf("reason = %#v, want fresh workspace evidence guidance", resultMap["reason"])
	}
}

func TestPipelineProtocolSkills_FinalizePipelineRefusesRepeatedAuditWithoutWorkspaceChange(t *testing.T) {
	views := stubPipelineProtocolWorkspaceViews{
		summary: &versioning.WorkspaceSummary{
			DefaultView:          versioning.WorkspaceViewPipeline,
			SourceOfTruth:        versioning.WorkspaceViewPipeline,
			PipelineID:           "task-repeat-finalize",
			Paths:                []string{"src/app.go"},
			PipelineChangedPaths: []string{"src/app.go"},
		},
	}
	baseTask := &PipelineTaskInput{
		TaskID:    "task-repeat-finalize",
		AgentType: PipelineAgentInspector,
		Context: map[string]any{
			"pipeline_stage": "inspect",
			"affected_files": []any{"src/app.go"},
			"workspace": map[string]any{
				"write_set": []any{"src/app.go"},
			},
		},
	}
	fingerprint := pipelineChallengeFingerprint(resolvePipelineChallengeEvidence(
		WithPipelineTask(context.Background(), baseTask),
		PipelineProtocolSkillConfig{
			WorkspaceViews: func() versioning.WorkspaceViewAccess { return views },
		},
	))
	if fingerprint == "" {
		t.Fatal("expected workspace fingerprint")
	}

	task := &PipelineTaskInput{
		TaskID:    baseTask.TaskID,
		AgentType: baseTask.AgentType,
		Context: map[string]any{
			"pipeline_stage": "inspect",
			"affected_files": []any{"src/app.go"},
			"workspace": map[string]any{
				"write_set": []any{"src/app.go"},
			},
			"pipeline_protocol": PipelineProtocolSnapshotMap(&PipelineProtocolSnapshot{
				Roster: []PipelineProtocolAgent{
					{AgentType: PipelineAgentInspector},
					{AgentType: PipelineAgentTester},
				},
				ActiveAgents: []string{PipelineAgentInspector},
				RecentEvents: []PipelineProtocolEvent{
					{
						Type:                 string(PipelineProtocolActionHandoff),
						AgentType:            PipelineAgentInspector,
						Targets:              []string{PipelineAgentTester},
						Summary:              "Audit the implementation.",
						CreatesChallenge:     true,
						WorkspaceFingerprint: fingerprint,
					},
				},
			}),
		},
	}
	ctx := WithPipelineTaskProtocolState(context.Background(), task)
	ctx = WithTaskExecutionContract(ctx, &TaskExecutionContract{RuntimeAgentType: PipelineAgentInspector})

	skills := PipelineProtocolSkills(PipelineProtocolSkillConfig{
		AgentType:      func() string { return PipelineAgentInspector },
		InspectorOT:    true,
		WorkspaceViews: func() versioning.WorkspaceViewAccess { return views },
	})

	result, err := callSkill(t, ctx, skills, "finalize_pipeline", map[string]any{
		"summary": "Run the audit again.",
	})
	if err != nil {
		t.Fatalf("finalize_pipeline error = %v", err)
	}
	resultMap, _ := result.(map[string]any)
	if resultMap == nil || resultMap["refused"] != true {
		t.Fatalf("finalize_pipeline result = %#v, want refused=true", result)
	}
	if resultMap["refused_by"] != "pipeline-protocol" {
		t.Fatalf("refused_by = %#v, want pipeline-protocol", resultMap["refused_by"])
	}
	if resultMap["must_wait"] != true {
		t.Fatalf("must_wait = %#v, want true", resultMap["must_wait"])
	}
	if !strings.Contains(fmt.Sprint(resultMap["reason"]), "fresh workspace evidence") {
		t.Fatalf("reason = %#v, want fresh workspace evidence guidance", resultMap["reason"])
	}
}

type stubPipelineProtocolWorkspaceViews struct {
	summary *versioning.WorkspaceSummary
}

func (s stubPipelineProtocolWorkspaceViews) ReadFile(context.Context, versioning.WorkspaceView, string, string) ([]byte, error) {
	return nil, nil
}

func (s stubPipelineProtocolWorkspaceViews) Glob(context.Context, versioning.WorkspaceView, string, string, []string, string) ([]string, error) {
	return nil, nil
}

func (s stubPipelineProtocolWorkspaceViews) Grep(context.Context, versioning.WorkspaceView, string, string, string, int, int, string) ([]versioning.GrepMatch, error) {
	return nil, nil
}

func (s stubPipelineProtocolWorkspaceViews) InspectPath(context.Context, string, string) (*versioning.WorkspacePathState, error) {
	return nil, nil
}

func (s stubPipelineProtocolWorkspaceViews) SummarizePaths(context.Context, []string, string) (*versioning.WorkspaceSummary, error) {
	return s.summary, nil
}

func (s stubPipelineProtocolWorkspaceViews) DefaultView() versioning.WorkspaceView {
	return versioning.WorkspaceViewPipeline
}

func TestBuildPipelineHandoffTasks_PreservesAuditLockAcrossWorkerHandoff(t *testing.T) {
	state := NewPipelineProtocolState(&PipelineProtocolSnapshot{
		Roster: []PipelineProtocolAgent{
			{AgentType: PipelineAgentInspector},
			{AgentType: PipelineAgentTester},
			{AgentType: PipelineAgentEngineer},
		},
		ActiveAgents: []string{PipelineAgentTester},
		AuditLock: &PipelineAuditLock{
			OwnerAgent: PipelineAgentInspector,
			Phase:      PipelineAuditPhaseFinalizing,
			Reason:     "Inspector is conducting terminal audit review.",
		},
	})
	task := &PipelineTaskInput{
		TaskID:    "task-auth",
		AgentType: PipelineAgentTester,
		Context:   map[string]any{},
	}
	action := &PipelineTurnAction{
		Type:         PipelineProtocolActionHandoff,
		AgentType:    PipelineAgentTester,
		TargetAgents: []string{PipelineAgentEngineer},
		Mode:         PipelineTurnModeSingle,
		Reason:       "Need an implementation follow-up before the tester can complete the audit evidence.",
		Request:      "Address the failing edge case under audit.",
	}

	tasks, err := buildPipelineHandoffTasks(state, task, action)
	if err != nil {
		t.Fatalf("buildPipelineHandoffTasks error = %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("task count = %d, want 1", len(tasks))
	}

	snapshot, err := PipelineProtocolSnapshotFromTask(tasks[0])
	if err != nil {
		t.Fatalf("PipelineProtocolSnapshotFromTask: %v", err)
	}
	if snapshot == nil || snapshot.AuditLock == nil {
		t.Fatalf("audit_lock = %#v, want value", snapshot)
	}
	if snapshot.AuditLock.Phase != PipelineAuditPhaseFinalizing {
		t.Fatalf("audit_lock.phase = %q, want %q", snapshot.AuditLock.Phase, PipelineAuditPhaseFinalizing)
	}
}

func TestPipelineProtocolSkills_FinalizePipelineSignalsReadinessAndHandoffToOTPublishesTerminalUpdate(t *testing.T) {
	sessionDir := t.TempDir()
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	updateCh := make(chan map[string]any, 1)
	updateSub, err := bus.SubscribeAsync("pipeline.update."+PipelineAgentInspector, func(msg *guide.Message) error {
		payload := pipelineUpdatePayloadFromMessage(msg)
		if payload == nil {
			return nil
		}
		select {
		case updateCh <- payload:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe pipeline updates: %v", err)
	}
	defer updateSub.Unsubscribe()

	task := &PipelineTaskInput{
		TaskID:        "task-ot",
		AgentType:     PipelineAgentInspector,
		TargetAgentID: PipelineWorkerRoutingTarget("task-ot", PipelineAgentInspector),
		Prompt:        "Finalize the accepted pipeline.",
		SessionID:     "session-ot",
		Context: map[string]any{
			"session_dir":    sessionDir,
			"pipeline_stage": "inspect",
			"pipeline_protocol": PipelineProtocolSnapshotMap(&PipelineProtocolSnapshot{
				Roster: []PipelineProtocolAgent{
					{AgentType: PipelineAgentInspector},
					{AgentType: PipelineAgentTester},
					{AgentType: PipelineAgentEngineer},
				},
				ActiveAgents: []string{PipelineAgentInspector},
				PendingValidation: &PipelineValidationRecord{
					ChallengeID:         "challenge-final-1",
					RequestingAgent:     PipelineAgentInspector,
					RespondingAgent:     PipelineAgentTester,
					Status:              string(PipelineValidationPassed),
					Summary:             "Implementation and tests meet the final gate.",
					ChallengeRequest:    "Conduct terminal verification.",
					ChallengeReferences: []string{finalizePipelineVerificationReference, "tests/auth_test.go"},
					EvidenceRefs:        []string{"tests/auth_test.go", "artifacts/verification.json"},
				},
			}),
		},
	}
	ctx := WithPipelineTaskProtocolState(context.Background(), task)
	ctx = WithTaskExecutionContract(ctx, &TaskExecutionContract{RuntimeAgentType: PipelineAgentInspector})

	skills := PipelineProtocolSkills(PipelineProtocolSkillConfig{
		AgentType:   func() string { return PipelineAgentInspector },
		AgentID:     func() string { return "inspector-ot-1" },
		InspectorOT: true,
		Committer:   func() PipelineCommitter { return testNoopPipelineCommitter{} },
		Route: PipelineProtocolRouteConfig{
			BusProvider: func() guide.EventBus { return bus },
			SessionID:   func() string { return task.SessionID },
		},
	})

	result, err := callSkill(t, ctx, skills, "finalize_pipeline", map[string]any{
		"summary":       "Criteria satisfied and pipeline is ready for merge.",
		"evidence_refs": []string{"tests/auth_test.go", "cli.py"},
	})
	if err != nil {
		t.Fatalf("finalize_pipeline error = %v", err)
	}
	resultMap, _ := result.(map[string]any)
	if resultMap == nil || resultMap["ready_for_ot"] != true {
		t.Fatalf("finalize_pipeline result = %#v, want ready_for_ot=true", result)
	}
	if resultMap["finalize_pipeline"] != true {
		t.Fatalf("finalize_pipeline result = %#v, want finalize_pipeline=true after passing tester audit", result)
	}
	if resultMap["must_handoff_to_ot"] != true {
		t.Fatalf("finalize_pipeline result = %#v, want must_handoff_to_ot=true", result)
	}
	if resultMap["next_required_action"] != "handoff_to_ot" {
		t.Fatalf("finalize_pipeline result = %#v, want next_required_action=handoff_to_ot", result)
	}

	state := PipelineProtocolStateFromContext(ctx)
	if state == nil {
		t.Fatal("pipeline protocol state missing from context")
	}
	if action := state.TerminalAction(); action != nil {
		t.Fatalf("terminal action = %#v, want nil before handoff_to_ot", action)
	}

	runSkill(t, ctx, skills, "handoff_to_ot", map[string]any{
		"summary":       "Criteria satisfied and pipeline is ready for merge.",
		"evidence_refs": []string{"tests/auth_test.go", "cli.py"},
	})

	if action := state.TerminalAction(); action == nil || action.Type != PipelineProtocolActionOT {
		t.Fatalf("terminal action = %#v, want handoff_to_ot", action)
	}

	update := waitForPipelineUpdate(t, updateCh)
	if update["status"] != "succeeded" {
		t.Fatalf("status = %#v, want succeeded", update["status"])
	}
	if update["stage"] != "inspect" {
		t.Fatalf("stage = %#v, want inspect", update["stage"])
	}
	if update["message"] != "Criteria satisfied and pipeline is ready for merge." {
		t.Fatalf("message = %#v", update["message"])
	}
	output, _ := update["output"].(map[string]any)
	if output == nil {
		t.Fatalf("output = %#v, want map", update["output"])
	}
	if output["summary"] != "Criteria satisfied and pipeline is ready for merge." {
		t.Fatalf("output.summary = %#v", output["summary"])
	}
}

func TestPipelineProtocolSkills_HandoffToOTPublishesTerminalUpdateWithoutBoundPipelineTask(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	updateCh := make(chan map[string]any, 1)
	updateSub, err := bus.SubscribeAsync("pipeline.update."+PipelineAgentInspector, func(msg *guide.Message) error {
		payload := pipelineUpdatePayloadFromMessage(msg)
		if payload == nil {
			return nil
		}
		select {
		case updateCh <- payload:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe pipeline updates: %v", err)
	}
	defer updateSub.Unsubscribe()

	ctx := WithPipelineProtocolState(context.Background(), NewPipelineProtocolState(&PipelineProtocolSnapshot{}))
	ctx = WithTaskExecutionContract(ctx, &TaskExecutionContract{
		RuntimeAgentType: PipelineAgentInspector,
		Stage:            "inspect",
	})
	ctx = WithStreamContext(ctx, "corr-fallback-ot", "orchestrator")
	ctx = WithStreamContextMetadata(ctx, map[string]any{
		"pipeline_task": true,
		"dag_id":        "dag-fallback",
		"node_id":       "task-fallback",
		"task_id":       "task-fallback",
		"task_slug":     "fallback-task",
		"task_name":     "Fallback Task",
		"agent_type":    PipelineAgentInspector,
	})

	skills := PipelineProtocolSkills(PipelineProtocolSkillConfig{
		AgentType:   func() string { return PipelineAgentInspector },
		AgentID:     func() string { return "inspector-fallback-1" },
		InspectorOT: true,
		Committer:   func() PipelineCommitter { return testNoopPipelineCommitter{} },
		Route: PipelineProtocolRouteConfig{
			BusProvider: func() guide.EventBus { return bus },
			SessionID:   func() string { return "session-fallback" },
		},
	})

	runSkill(t, ctx, skills, "handoff_to_ot", map[string]any{
		"summary":       "Fallback OT handoff should still publish the terminal update.",
		"evidence_refs": []string{"artifact:fallback"},
	})

	update := waitForPipelineUpdate(t, updateCh)
	if update["status"] != "succeeded" {
		t.Fatalf("status = %#v, want succeeded", update["status"])
	}
	if update["task_id"] != "task-fallback" {
		t.Fatalf("task_id = %#v, want task-fallback", update["task_id"])
	}
	if update["message"] != "Fallback OT handoff should still publish the terminal update." {
		t.Fatalf("message = %#v", update["message"])
	}
}

func waitForRouteRequest(t *testing.T, ch <-chan *guide.RouteRequest) *guide.RouteRequest {
	t.Helper()
	select {
	case req := <-ch:
		if req == nil {
			t.Fatal("expected route request")
		}
		return req
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for route request")
		return nil
	}
}

// TestPipelineChallengeAgentSchema_PerInvokerEnumExcludesSelfAndNonPeers
// verifies that the per-invoker enum baked into challenge_agent.target_agents
// excludes the invoker itself and every non-pipeline agent (orchestrator,
// architect, guardian, etc). This is the schema-level guard that prevents the
// LLM from even attempting a challenge to orchestrator — the bug that
// produced the stuck "orchestrator 486s" UI row.
func TestPipelineChallengeAgentSchema_PerInvokerEnumExcludesSelfAndNonPeers(t *testing.T) {
	invokers := map[string]struct {
		mustInclude []string
		mustExclude []string
	}{
		PipelineAgentInspector: {
			mustInclude: []string{PipelineAgentTester, PipelineAgentEngineer, PipelineAgentDesigner, "tester"},
			mustExclude: []string{PipelineAgentInspector, "inspector", "orchestrator", "architect", "guardian"},
		},
		PipelineAgentTester: {
			mustInclude: []string{PipelineAgentInspector, PipelineAgentEngineer, PipelineAgentDesigner, "inspector"},
			mustExclude: []string{PipelineAgentTester, "tester", "orchestrator", "architect"},
		},
		PipelineAgentEngineer: {
			mustInclude: []string{PipelineAgentInspector, PipelineAgentTester, PipelineAgentDesigner},
			mustExclude: []string{PipelineAgentEngineer, "orchestrator", "architect"},
		},
		PipelineAgentDesigner: {
			mustInclude: []string{PipelineAgentInspector, PipelineAgentTester, PipelineAgentEngineer},
			mustExclude: []string{PipelineAgentDesigner, "orchestrator", "architect"},
		},
	}
	for invoker, spec := range invokers {
		invoker := invoker
		spec := spec
		t.Run(invoker, func(t *testing.T) {
			skillsList := PipelineProtocolSkills(PipelineProtocolSkillConfig{
				AgentType: func() string { return invoker },
			})
			for _, toolName := range []string{"challenge_agent", "handoff_next"} {
				var challenge *skills.Skill
				for _, s := range skillsList {
					if s.Name == toolName {
						challenge = s
						break
					}
				}
				if challenge == nil {
					t.Fatalf("%s skill not registered for invoker %s", toolName, invoker)
				}
				prop, ok := challenge.InputSchema.Properties["target_agents"]
				if !ok || prop == nil || prop.Items == nil {
					t.Fatalf("%s: target_agents property missing for invoker %s", toolName, invoker)
				}
				if len(prop.Items.Enum) == 0 {
					t.Fatalf("%s: target_agents.Items.Enum must be populated for invoker %s", toolName, invoker)
				}
				enumSet := map[string]struct{}{}
				for _, v := range prop.Items.Enum {
					enumSet[v] = struct{}{}
				}
				for _, v := range spec.mustInclude {
					if _, ok := enumSet[v]; !ok {
						t.Errorf("%s invoker %s: enum missing %q (have %v)", toolName, invoker, v, prop.Items.Enum)
					}
				}
				for _, v := range spec.mustExclude {
					if _, ok := enumSet[v]; ok {
						t.Errorf("%s invoker %s: enum must not include %q (have %v)", toolName, invoker, v, prop.Items.Enum)
					}
				}
			}
		})
	}
}

func waitForReroute(t *testing.T, ch <-chan map[string]string) map[string]string {
	t.Helper()
	select {
	case data := <-ch:
		if data == nil {
			t.Fatal("expected reroute payload")
		}
		return data
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reroute")
		return nil
	}
}

func waitForPipelineUpdate(t *testing.T, ch <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case payload := <-ch:
		if payload == nil {
			t.Fatal("expected pipeline update payload")
		}
		return payload
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for pipeline update")
		return nil
	}
}

func pipelineUpdatePayloadFromMessage(msg *guide.Message) map[string]any {
	if msg == nil {
		return nil
	}
	payload, _ := msg.Payload.(map[string]any)
	return payload
}

func runSkill(t *testing.T, ctx context.Context, skills []*skills.Skill, name string, payload any) {
	t.Helper()
	_, err := callSkill(t, ctx, skills, name, payload)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func callSkill(t *testing.T, ctx context.Context, skills []*skills.Skill, name string, payload any) (any, error) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	for _, skill := range skills {
		if skill.Name != name {
			continue
		}
		return skill.Handler(ctx, raw)
	}
	t.Fatalf("skill %s not found", name)
	return nil, nil
}

// testNoopPipelineCommitter satisfies PipelineCommitter for tests that
// exercise the inspector handoff_to_ot / discard_pipeline skills without a
// real SessionVFS. Real wiring (cmd/tui.go) installs a SessionVFS-backed
// committer; here we just want the skill to succeed past its committer
// gate so the test can observe protocol-state mutations and published
// updates.
type testNoopPipelineCommitter struct{}

func (testNoopPipelineCommitter) MergePipelineIntoGreen(_ context.Context, pipelineID string) (versioning.MergePipelineResult, error) {
	return versioning.MergePipelineResult{PipelineID: pipelineID}, nil
}

func (testNoopPipelineCommitter) ExtractReviewCandidate(_ context.Context, _ string) (string, bool, versioning.SemanticVersion, error) {
	return "", false, versioning.SemanticVersion{}, nil
}

func (testNoopPipelineCommitter) Rollback(_ context.Context, _ string) error { return nil }

// The following tests lock in the schema-level routing contract for
// finalize_pipeline when a challenge is pending. Historically the
// handler required the LLM to specify target=<challenger> and
// rejected with challenge_target_mismatch when the LLM supplied the
// previous-handoff target (engineer/designer) instead. The fix makes
// target optional when a challenge is pending and auto-derives it
// from PendingChallenge.RequestingAgent — the LLM cannot misroute
// if it follows the new "omit target when challenged" guidance.

func TestReconcileChallengeFinalizeTarget_EmptyTargetAutoDerived(t *testing.T) {
	specs := []PipelineTesterFinalizeTargetSpec{
		{Target: "", Summary: "verified", EvidenceRefs: []string{"tests/foo_test.go"}},
	}
	snapshot := &PipelineProtocolSnapshot{
		PendingChallenge: &PipelineProtocolChallenge{
			ID:              "chal-1",
			RequestingAgent: PipelineAgentInspector,
		},
	}
	if err := reconcileChallengeFinalizeTarget(specs, snapshot); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if specs[0].Target != PipelineAgentInspector {
		t.Errorf("target = %q, want %q (auto-derived from challenger)", specs[0].Target, PipelineAgentInspector)
	}
}

func TestReconcileChallengeFinalizeTarget_MatchingTargetAccepted(t *testing.T) {
	specs := []PipelineTesterFinalizeTargetSpec{
		{Target: PipelineAgentInspector, Summary: "verified"},
	}
	snapshot := &PipelineProtocolSnapshot{
		PendingChallenge: &PipelineProtocolChallenge{
			ID:              "chal-1",
			RequestingAgent: PipelineAgentInspector,
		},
	}
	if err := reconcileChallengeFinalizeTarget(specs, snapshot); err != nil {
		t.Fatalf("unexpected error for explicit-matching target: %v", err)
	}
	if specs[0].Target != PipelineAgentInspector {
		t.Errorf("target = %q, should be preserved", specs[0].Target)
	}
}

func TestReconcileChallengeFinalizeTarget_WrongTargetRejectedWithRecovery(t *testing.T) {
	// This is the bug from the reported screenshot: LLM supplied
	// target=engineer while the inspector challenge was pending.
	specs := []PipelineTesterFinalizeTargetSpec{
		{Target: PipelineAgentEngineer, Summary: "verified", EvidenceRefs: []string{"tests/foo_test.go"}},
	}
	snapshot := &PipelineProtocolSnapshot{
		PendingChallenge: &PipelineProtocolChallenge{
			ID:              "chal-1",
			RequestingAgent: PipelineAgentInspector,
		},
	}
	err := reconcileChallengeFinalizeTarget(specs, snapshot)
	if err == nil {
		t.Fatal("expected error for explicit wrong target")
	}
	msg := err.Error()
	if !strings.Contains(msg, "challenge_target_mismatch") {
		t.Errorf("error code missing: %v", err)
	}
	if !strings.Contains(msg, "omit target") {
		t.Errorf("error recovery should tell the LLM it may omit target; got: %v", err)
	}
	// Original spec should NOT have been silently overwritten — the
	// substrate surfaces the LLM's mistake rather than hiding it.
	if specs[0].Target != PipelineAgentEngineer {
		t.Errorf("spec was mutated on error; got target %q", specs[0].Target)
	}
}

func TestReconcileChallengeFinalizeTarget_MultiTargetRejected(t *testing.T) {
	specs := []PipelineTesterFinalizeTargetSpec{
		{Target: PipelineAgentEngineer, Summary: "x"},
		{Target: PipelineAgentDesigner, Summary: "y"},
	}
	snapshot := &PipelineProtocolSnapshot{
		PendingChallenge: &PipelineProtocolChallenge{
			ID:              "chal-1",
			RequestingAgent: PipelineAgentInspector,
		},
	}
	err := reconcileChallengeFinalizeTarget(specs, snapshot)
	if err == nil {
		t.Fatal("expected error for len(specs) != 1 while challenged")
	}
	if !strings.Contains(err.Error(), "challenge_target_mismatch") {
		t.Errorf("error code missing: %v", err)
	}
}

func TestReconcileChallengeFinalizeTarget_ChallengerMissingIdentity(t *testing.T) {
	specs := []PipelineTesterFinalizeTargetSpec{{Summary: "x"}}
	snapshot := &PipelineProtocolSnapshot{
		PendingChallenge: &PipelineProtocolChallenge{ID: "chal-1"},
	}
	err := reconcileChallengeFinalizeTarget(specs, snapshot)
	if err == nil {
		t.Fatal("expected error when challenger has no identity")
	}
	if !strings.Contains(err.Error(), "no requesting agent identity") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRequireExplicitFinalizeTargets_RejectsEmpty(t *testing.T) {
	specs := []PipelineTesterFinalizeTargetSpec{
		{Target: "", Summary: "x"},
	}
	err := requireExplicitFinalizeTargets(specs)
	if err == nil {
		t.Fatal("expected error for empty target when no challenge")
	}
	if !strings.Contains(err.Error(), "targets[0].target is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRequireExplicitFinalizeTargets_AcceptsPopulated(t *testing.T) {
	specs := []PipelineTesterFinalizeTargetSpec{
		{Target: PipelineAgentEngineer, Summary: "x"},
		{Target: PipelineAgentDesigner, Summary: "y"},
	}
	if err := requireExplicitFinalizeTargets(specs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestPipelineDispatchHandoffOptions_ForwardHandoffSetsParentCID locks in
// the fix for the chat-panel grouping bug where Engineer→Inspector handoffs
// were appended under the engineer's entry instead of spawning a new
// inspector-pipeline entry. When action.CreatesChallenge is false, the
// dispatcher MUST stamp ForwardHandoffParentCID with the current stream
// correlation ID so the route request carries TopLevelTransfer=true and
// the chat model creates a new primary entry for the recipient.
func TestPipelineDispatchHandoffOptions_ForwardHandoffSetsParentCID(t *testing.T) {
	action := &PipelineTurnAction{
		AgentType:        PipelineAgentInspector,
		CreatesChallenge: false,
		Request:          "Audit the implementation.",
	}
	task := &PipelineTaskInput{AgentType: PipelineAgentInspector, TaskID: "task_1"}
	ctx := WithStreamContext(context.Background(), "pipe_engineer_abc", "engineer_1")

	opts := pipelineDispatchHandoffOptions(ctx, task, action)

	if opts.ForwardHandoffParentCID != "pipe_engineer_abc" {
		t.Fatalf("ForwardHandoffParentCID = %q, want pipe_engineer_abc", opts.ForwardHandoffParentCID)
	}
	if opts.InterAgentBranch != nil {
		t.Errorf("InterAgentBranch must be nil for ordinary handoffs; got %+v", opts.InterAgentBranch)
	}
	if opts.OriginatorContinuationCID != "" {
		t.Errorf("OriginatorContinuationCID must be empty for forward handoffs; got %q", opts.OriginatorContinuationCID)
	}
}

// TestPipelineDispatchHandoffOptions_ChallengeStaysNested locks the
// anti-regression constraint: challenges MUST continue to use
// InterAgentBranch (nested rendering under the challenger's tool call)
// and MUST NOT receive ForwardHandoffParentCID. Re-introducing that on
// challenges would break the earlier fix where every consult/challenge
// started spawning its own top-level chat entry.
func TestPipelineDispatchHandoffOptions_ChallengeStaysNested(t *testing.T) {
	action := &PipelineTurnAction{
		AgentType:        PipelineAgentTester,
		CreatesChallenge: true,
		Request:          "Explain why test_cli has no assertion.",
	}
	task := &PipelineTaskInput{AgentType: PipelineAgentTester, TaskID: "challenge_1"}
	ctx := WithStreamContext(context.Background(), "pipe_engineer_abc", "engineer_1")

	opts := pipelineDispatchHandoffOptions(ctx, task, action)

	if opts.ForwardHandoffParentCID != "" {
		t.Fatalf("challenges must NOT set ForwardHandoffParentCID; got %q — would regress the consult/challenge top-level-continuity fix", opts.ForwardHandoffParentCID)
	}
	if opts.InterAgentBranch == nil {
		t.Fatal("challenges must set InterAgentBranch for nested rendering")
	}
	if opts.InterAgentBranch.Kind != InterAgentToolEventKindChallenge {
		t.Errorf("InterAgentBranch.Kind = %q, want %q", opts.InterAgentBranch.Kind, InterAgentToolEventKindChallenge)
	}
}

// TestPipelineDispatchHandoffOptions_NoStreamContext covers the defensive
// path: when there is no stream metadata on ctx (dispatcher invoked
// outside a live tool loop), options are empty and dispatch falls back
// to plain routing — matches prior behaviour, no regression.
func TestPipelineDispatchHandoffOptions_NoStreamContext(t *testing.T) {
	action := &PipelineTurnAction{AgentType: PipelineAgentInspector, CreatesChallenge: false}
	task := &PipelineTaskInput{AgentType: PipelineAgentInspector}

	opts := pipelineDispatchHandoffOptions(context.Background(), task, action)

	if opts.ForwardHandoffParentCID != "" {
		t.Errorf("expected empty ForwardHandoffParentCID without stream context; got %q", opts.ForwardHandoffParentCID)
	}
	if opts.InterAgentBranch != nil {
		t.Errorf("expected nil InterAgentBranch without stream context; got %+v", opts.InterAgentBranch)
	}
}
