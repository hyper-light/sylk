package shared

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/skills"
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

func TestValidatePipelineProtocolCompletion_RequiresTurnAction(t *testing.T) {
	ctx := WithPipelineProtocolState(context.Background(), NewPipelineProtocolState(&PipelineProtocolSnapshot{}))
	if err := ValidatePipelineProtocolCompletion(ctx, PipelineAgentEngineer); err == nil {
		t.Fatal("expected missing turn action to fail completion")
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
	if resultMap == nil || resultMap["forwarded"] != true {
		t.Fatalf("handoff_next result = %#v, want forwarded=true", result)
	}

	req := waitForRouteRequest(t, routeCh)
	if req.ParentCorrelationID != "corr-inspector" {
		t.Fatalf("parent_correlation_id = %q, want corr-inspector", req.ParentCorrelationID)
	}
	if req.TargetAgentID != PipelineWorkerAgentID("task-async", PipelineAgentTester) {
		t.Fatalf("target_agent_id = %q", req.TargetAgentID)
	}

	var nextTask PipelineTaskInput
	if err := json.Unmarshal([]byte(req.Input), &nextTask); err != nil {
		t.Fatalf("decode next task: %v", err)
	}
	if nextTask.AgentType != PipelineAgentTester {
		t.Fatalf("next agent_type = %q, want %q", nextTask.AgentType, PipelineAgentTester)
	}
	if nextTask.TargetAgentID != req.TargetAgentID {
		t.Fatalf("next target_agent_id = %q, want %q", nextTask.TargetAgentID, req.TargetAgentID)
	}
	snapshot, err := PipelineProtocolSnapshotFromTask(&nextTask)
	if err != nil {
		t.Fatalf("PipelineProtocolSnapshotFromTask: %v", err)
	}
	if snapshot == nil || snapshot.PendingChallenge == nil {
		t.Fatalf("pending challenge = %#v, want value", snapshot)
	}
	if snapshot.PendingChallenge.RequestingAgent != PipelineAgentInspector {
		t.Fatalf("requesting_agent = %q, want %q", snapshot.PendingChallenge.RequestingAgent, PipelineAgentInspector)
	}
	if len(snapshot.ActiveAgents) != 1 || snapshot.ActiveAgents[0] != PipelineAgentTester {
		t.Fatalf("active_agents = %#v, want tester", snapshot.ActiveAgents)
	}

	reroute := waitForReroute(t, rerouteCh)
	if reroute["from_agent"] != PipelineAgentInspector {
		t.Fatalf("from_agent = %q, want %q", reroute["from_agent"], PipelineAgentInspector)
	}
	if reroute["to_agent"] != PipelineAgentTester {
		t.Fatalf("to_agent = %q, want %q", reroute["to_agent"], PipelineAgentTester)
	}
	if reroute["original_correlation_id"] != "corr-inspector" {
		t.Fatalf("original_correlation_id = %q, want corr-inspector", reroute["original_correlation_id"])
	}
	if reroute["new_correlation_id"] != req.CorrelationID {
		t.Fatalf("new_correlation_id = %q, want %q", reroute["new_correlation_id"], req.CorrelationID)
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
	if req.TargetAgentID != PipelineWorkerAgentID("task-challenge", PipelineAgentTester) {
		t.Fatalf("target_agent_id = %q", req.TargetAgentID)
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

func TestPipelineProtocolSkills_ValidateWorkPublishesGuideRoute(t *testing.T) {
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
			"pipeline_stage": "test",
			"pipeline_protocol": PipelineProtocolSnapshotMap(&PipelineProtocolSnapshot{
				Roster: []PipelineProtocolAgent{
					{AgentType: PipelineAgentInspector},
					{AgentType: PipelineAgentTester},
				},
				ActiveAgents: []string{PipelineAgentTester},
				PendingChallenge: &PipelineProtocolChallenge{
					ID:              "challenge-1",
					RequestingAgent: PipelineAgentInspector,
					TargetAgents:    []string{PipelineAgentTester},
					Request:         "Author and run the validating tests.",
				},
			}),
		},
	}
	ctx := WithPipelineTaskProtocolState(context.Background(), task)
	ctx = WithTaskExecutionContract(ctx, &TaskExecutionContract{RuntimeAgentType: PipelineAgentTester})
	ctx = WithStreamContext(ctx, "corr-tester", "tui")

	skills := PipelineProtocolSkills(PipelineProtocolSkillConfig{
		AgentType: func() string { return PipelineAgentTester },
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
	resultMap, _ := result.(map[string]any)
	if resultMap == nil || resultMap["forwarded"] != true {
		t.Fatalf("validate_work result = %#v, want forwarded=true", result)
	}

	req := waitForRouteRequest(t, routeCh)
	if req.ParentCorrelationID != "corr-tester" {
		t.Fatalf("parent_correlation_id = %q, want corr-tester", req.ParentCorrelationID)
	}
	if req.TargetAgentID != PipelineWorkerAgentID("task-validate", PipelineAgentInspector) {
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
	if snapshot.PendingValidation.RespondingAgent != PipelineAgentTester {
		t.Fatalf("responding_agent = %q, want %q", snapshot.PendingValidation.RespondingAgent, PipelineAgentTester)
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
		TargetAgentID: PipelineWorkerAgentID("task-finalize-gate", PipelineAgentInspector),
		Prompt:        "Finalize the accepted pipeline.",
		SessionID:     "session-finalize-gate",
		Context: map[string]any{
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
	if req.TargetAgentID != PipelineWorkerAgentID("task-finalize-gate", PipelineAgentTester) {
		t.Fatalf("target_agent_id = %q", req.TargetAgentID)
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

func TestPipelineProtocolSkills_FinalizePipelineSignalsReadinessAndHandoffToOTPublishesTerminalUpdate(t *testing.T) {
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
		TargetAgentID: PipelineWorkerAgentID("task-ot", PipelineAgentInspector),
		Prompt:        "Finalize the accepted pipeline.",
		SessionID:     "session-ot",
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
		InspectorOT: true,
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
