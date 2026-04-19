package guide

import (
	"context"
	"testing"
	"time"
)

func TestGuideRoute_ExplicitTargetAndFollowupContinuity(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	g, err := NewWithClassifier(NewRuleClassifierClient(), Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "session-1",
		Factory: newTestFactory(t),
	})
	if err != nil {
		t.Fatalf("new guide: %v", err)
	}

	err = g.Register(&AgentRoutingInfo{
		ID:   "architect",
		Type: "architect",
		Name: "architect",
		Registration: &AgentRegistration{
			ID:   "architect",
			Name: "architect",
			Capabilities: AgentCapabilities{
				Intents: []Intent{IntentPlan, IntentDesign, IntentCheck, IntentHelp},
				Domains: []Domain{DomainDesign, DomainTasks},
			},
		},
	})
	if err != nil {
		t.Fatalf("register architect: %v", err)
	}

	first := &RouteRequest{
		Input:         "Design a migration plan",
		SourceAgentID: "tui",
		TargetAgentID: "architect",
		SessionID:     "session-1",
		Timestamp:     time.Now(),
	}
	forwarded, err := g.Route(context.Background(), first)
	if err != nil {
		t.Fatalf("route explicit architect: %v", err)
	}
	if forwarded.TargetAgentID != "architect" {
		t.Fatalf("explicit route target = %q, want architect", forwarded.TargetAgentID)
	}
	if forwarded.Intent == IntentUnknown {
		t.Fatalf("explicit route intent = %q, want non-unknown", forwarded.Intent)
	}
	if len(forwarded.ConversationHistory) != 1 {
		t.Fatalf("first route history len = %d, want 1", len(forwarded.ConversationHistory))
	}
	if forwarded.ConversationHistory[0].UserInput != first.Input {
		t.Fatalf("first route history input = %q, want %q", forwarded.ConversationHistory[0].UserInput, first.Input)
	}
	if forwarded.ConversationHistory[0].AgentID != "architect" {
		t.Fatalf("first route history agent = %q, want architect", forwarded.ConversationHistory[0].AgentID)
	}

	followup := &RouteRequest{
		Input:         "Can we refine that plan?",
		SourceAgentID: "tui",
		TargetAgentID: "guide",
		SessionID:     "session-1",
		Timestamp:     time.Now(),
	}
	followed, err := g.Route(context.Background(), followup)
	if err != nil {
		t.Fatalf("route follow-up: %v", err)
	}
	if followed.TargetAgentID != "architect" {
		t.Fatalf("follow-up target = %q, want architect", followed.TargetAgentID)
	}
	if len(followed.ConversationHistory) != 2 {
		t.Fatalf("follow-up history len = %d, want 2", len(followed.ConversationHistory))
	}
	if followed.ConversationHistory[0].UserInput != first.Input {
		t.Fatalf("follow-up history[0].input = %q, want %q", followed.ConversationHistory[0].UserInput, first.Input)
	}
	if followed.ConversationHistory[0].AgentID != "architect" {
		t.Fatalf("follow-up history[0].agent = %q, want architect", followed.ConversationHistory[0].AgentID)
	}
	if followed.ConversationHistory[1].UserInput != followup.Input {
		t.Fatalf("follow-up history[1].input = %q, want %q", followed.ConversationHistory[1].UserInput, followup.Input)
	}
	if followed.ConversationHistory[1].AgentID != "architect" {
		t.Fatalf("follow-up history[1].agent = %q, want architect", followed.ConversationHistory[1].AgentID)
	}
}

func TestGuideRoute_ExplicitGuideFollowupDoesNotRemapToSidecar(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	g, err := NewWithClassifier(NewRuleClassifierClient(), Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "session-sidecar",
		Factory: newTestFactory(t),
	})
	if err != nil {
		t.Fatalf("new guide: %v", err)
	}

	err = g.Register(&AgentRoutingInfo{
		ID:   "architect",
		Type: "architect",
		Name: "architect",
		Registration: &AgentRegistration{
			ID:   "architect",
			Name: "architect",
			Capabilities: AgentCapabilities{
				Intents: []Intent{IntentPlan, IntentDesign, IntentCheck, IntentHelp},
				Domains: []Domain{DomainDesign, DomainTasks},
			},
		},
	})
	if err != nil {
		t.Fatalf("register architect: %v", err)
	}

	if err := g.Register(&AgentRoutingInfo{
		ID:   "scribe-architect-deadbeef",
		Type: "scribe-architect",
		Name: "scribe-architect",
		Registration: &AgentRegistration{
			ID:   "scribe-architect-deadbeef",
			Name: "scribe-architect",
			Capabilities: AgentCapabilities{
				Priority: 999,
			},
			Priority: 999,
		},
	}); err != nil {
		t.Fatalf("register sidecar: %v", err)
	}

	first := &RouteRequest{
		Input:         "Design a migration plan",
		SourceAgentID: "tui",
		TargetAgentID: "architect",
		SessionID:     "session-sidecar",
		Timestamp:     time.Now(),
	}
	forwarded, err := g.Route(context.Background(), first)
	if err != nil {
		t.Fatalf("route explicit architect: %v", err)
	}
	if forwarded.TargetAgentID != "architect" {
		t.Fatalf("explicit route target = %q, want architect", forwarded.TargetAgentID)
	}

	g.conversation.ObserveRoutedRequest("session-sidecar", "scribe-architect")

	followup := &RouteRequest{
		Input:         "Use the guide to continue planning",
		SourceAgentID: "tui",
		TargetAgentID: "guide",
		SessionID:     "session-sidecar",
		Timestamp:     time.Now(),
	}
	followed, err := g.Route(context.Background(), followup)
	if err != nil {
		t.Fatalf("route follow-up: %v", err)
	}
	if followed.TargetAgentID != "architect" {
		t.Fatalf("follow-up target = %q, want architect", followed.TargetAgentID)
	}
	if g.resolveAgent("scribe-architect-deadbeef") != nil {
		t.Fatal("expected sidecar registration to be ignored by guide")
	}
}

func TestGuideRoute_NestedConsultDoesNotStealConversationStickiness(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	g, err := NewWithClassifier(NewRuleClassifierClient(), Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "session-nested",
		Factory: newTestFactory(t),
	})
	if err != nil {
		t.Fatalf("new guide: %v", err)
	}

	for _, info := range []*AgentRoutingInfo{
		{
			ID:   "architect",
			Type: "architect",
			Name: "architect",
			Registration: &AgentRegistration{
				ID:   "architect",
				Name: "architect",
				Capabilities: AgentCapabilities{
					Intents: []Intent{IntentPlan, IntentDesign, IntentCheck, IntentHelp},
					Domains: []Domain{DomainDesign, DomainTasks},
				},
			},
		},
		{
			ID:   "librarian",
			Type: "librarian",
			Name: "librarian",
			Registration: &AgentRegistration{
				ID:   "librarian",
				Name: "librarian",
				Capabilities: AgentCapabilities{
					Intents: []Intent{IntentFind, IntentSearch, IntentHelp},
					Domains: []Domain{DomainCode, DomainFiles, DomainLocal},
				},
			},
		},
	} {
		if err := g.Register(info); err != nil {
			t.Fatalf("register %s: %v", info.Name, err)
		}
	}

	first := &RouteRequest{
		Input:         "Design a migration plan",
		SourceAgentID: "tui",
		TargetAgentID: "architect",
		SessionID:     "session-nested",
		Timestamp:     time.Now(),
	}
	forwarded, err := g.Route(context.Background(), first)
	if err != nil {
		t.Fatalf("route explicit architect: %v", err)
	}
	if forwarded.TargetAgentID != "architect" {
		t.Fatalf("explicit route target = %q, want architect", forwarded.TargetAgentID)
	}

	nested := &RouteRequest{
		Input:         "Check repository conventions for this plan",
		SourceAgentID: "architect",
		TargetAgentID: "librarian",
		SessionID:     "session-nested",
		Timestamp:     time.Now(),
		Metadata: map[string]any{
			"chat_nested_branch":         true,
			"chat_parent_correlation_id": "corr-parent",
			"chat_parent_tool_call_key":  "consult-1",
			"chat_inter_agent_kind":      "consult",
		},
	}
	nestedForwarded, err := g.Route(context.Background(), nested)
	if err != nil {
		t.Fatalf("route nested consult: %v", err)
	}
	if nestedForwarded.TargetAgentID != "librarian" {
		t.Fatalf("nested route target = %q, want librarian", nestedForwarded.TargetAgentID)
	}

	followup := &RouteRequest{
		Input:         "Continue the planning through the guide",
		SourceAgentID: "tui",
		TargetAgentID: "guide",
		SessionID:     "session-nested",
		Timestamp:     time.Now(),
	}
	followed, err := g.Route(context.Background(), followup)
	if err != nil {
		t.Fatalf("route follow-up: %v", err)
	}
	if followed.TargetAgentID != "architect" {
		t.Fatalf("follow-up target = %q, want architect", followed.TargetAgentID)
	}
}

func TestGuideRoute_AppliesDeferredStreamRerouteToNewPending(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	g, err := NewWithClassifier(NewRuleClassifierClient(), Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "session-reroute",
		Factory: newTestFactory(t),
	})
	if err != nil {
		t.Fatalf("new guide: %v", err)
	}

	err = g.Register(&AgentRoutingInfo{
		ID:   "orchestrator",
		Type: "orchestrator",
		Name: "orchestrator",
		Registration: &AgentRegistration{
			ID:   "orchestrator",
			Name: "orchestrator",
			Capabilities: AgentCapabilities{
				Intents: []Intent{IntentExecute, IntentHelp},
				Domains: []Domain{DomainTasks, DomainGeneral},
			},
		},
	})
	if err != nil {
		t.Fatalf("register orchestrator: %v", err)
	}

	g.streamReroutes = map[string]string{
		"corr-orchestrator": "tui",
	}

	req := &RouteRequest{
		CorrelationID:  "corr-orchestrator",
		Input:          "dispatch this plan",
		SourceAgentID:  "architect",
		TargetAgentID:  "orchestrator",
		ExplicitTarget: true,
		SessionID:      "session-reroute",
		Timestamp:      time.Now(),
	}

	forwarded, err := g.Route(context.Background(), req)
	if err != nil {
		t.Fatalf("route explicit orchestrator: %v", err)
	}
	if forwarded.CorrelationID != "corr-orchestrator" {
		t.Fatalf("forwarded correlation = %q, want corr-orchestrator", forwarded.CorrelationID)
	}

	pending := g.GetPending("corr-orchestrator")
	if pending == nil {
		t.Fatal("expected pending request for rerouted orchestrator correlation")
	}
	if pending.StreamTargetOverride != "tui" {
		t.Fatalf("pending.StreamTargetOverride = %q, want tui", pending.StreamTargetOverride)
	}
	if _, ok := g.streamReroutes["corr-orchestrator"]; ok {
		t.Fatal("expected deferred reroute entry to be consumed after pending creation")
	}
}

func TestGuideHandleRouteRequestMessage_FireAndForgetExplicitTargetStillForwards(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	g, err := NewWithClassifier(NewRuleClassifierClient(), Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "session-fire-and-forget",
		Factory: newTestFactory(t),
	})
	if err != nil {
		t.Fatalf("new guide: %v", err)
	}

	err = g.Register(&AgentRoutingInfo{
		ID:   "tester",
		Type: "tester",
		Name: "tester",
		Registration: &AgentRegistration{
			ID:   "tester",
			Name: "tester",
			Capabilities: AgentCapabilities{
				Intents: []Intent{IntentCheck, IntentHelp},
				Domains: []Domain{DomainTesting, DomainGeneral},
			},
		},
	})
	if err != nil {
		t.Fatalf("register tester: %v", err)
	}

	forwardedCh := make(chan *ForwardedRequest, 1)
	sub, err := bus.SubscribeAsync(g.agentRequestTopic("tester"), func(msg *Message) error {
		fwd, ok := msg.GetForwardedRequest()
		if !ok || fwd == nil {
			return nil
		}
		select {
		case forwardedCh <- fwd:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe tester request topic: %v", err)
	}
	defer sub.Unsubscribe()

	req := &RouteRequest{
		CorrelationID:  "corr-fire-and-forget",
		Input:          "Validate the merged result.",
		SourceAgentID:  "orchestrator",
		TargetAgentID:  "tester",
		ExplicitTarget: true,
		FireAndForget:  true,
		SessionID:      "session-fire-and-forget",
		Timestamp:      time.Now(),
	}

	if err := g.handleRouteRequestMessage(context.Background(), NewRequestMessage("msg-fire-and-forget", req)); err != nil {
		t.Fatalf("handleRouteRequestMessage returned error: %v", err)
	}

	select {
	case forwarded := <-forwardedCh:
		if forwarded.TargetAgentID != "tester" {
			t.Fatalf("forwarded target = %q, want tester", forwarded.TargetAgentID)
		}
		if !forwarded.FireAndForget {
			t.Fatal("expected forwarded request to remain fire-and-forget")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for forwarded fire-and-forget request")
	}
}

func TestGuideHandleRouteRequestMessage_DSLStoreSkipsClassificationProgress(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	g, err := NewWithClassifier(NewRuleClassifierClient(), Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "session-dsl-store",
		Factory: newTestFactory(t),
	})
	if err != nil {
		t.Fatalf("new guide: %v", err)
	}

	err = g.Register(&AgentRoutingInfo{
		ID:   "archivalist",
		Type: "archivalist",
		Name: "archivalist",
		Registration: &AgentRegistration{
			ID:   "archivalist",
			Name: "archivalist",
			Capabilities: AgentCapabilities{
				Intents: []Intent{IntentStore, IntentRecall, IntentCheck, IntentDeclare, IntentComplete, IntentHelp},
				Domains: []Domain{DomainHistory, DomainPatterns, DomainDecisions, DomainLearnings, DomainFailures},
			},
		},
	})
	if err != nil {
		t.Fatalf("register archivalist: %v", err)
	}

	responseCh := make(chan *StreamResponse, 8)
	respSub, err := bus.SubscribeAsync(g.agentResponseTopic("architect"), func(msg *Message) error {
		stream, ok := msg.GetStreamResponse()
		if !ok || stream == nil {
			return nil
		}
		select {
		case responseCh <- stream:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe architect response topic: %v", err)
	}
	defer respSub.Unsubscribe()

	forwardedCh := make(chan *ForwardedRequest, 1)
	reqSub, err := bus.SubscribeAsync(g.agentRequestTopic("archivalist"), func(msg *Message) error {
		fwd, ok := msg.GetForwardedRequest()
		if !ok || fwd == nil {
			return nil
		}
		select {
		case forwardedCh <- fwd:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe archivalist request topic: %v", err)
	}
	defer reqSub.Unsubscribe()

	req := &RouteRequest{
		CorrelationID: "corr-dsl-store",
		Input:         `@archivalist:store:history{"content":"stored pre-delegation declaration"}`,
		SourceAgentID: "architect",
		FireAndForget: true,
		SessionID:     "session-dsl-store",
		Timestamp:     time.Now(),
		Metadata: map[string]any{
			"chat_nested_branch":         true,
			"chat_parent_correlation_id": "corr-parent-store",
			"chat_parent_tool_call_key":  "store-1",
			"chat_inter_agent_kind":      "store",
		},
	}

	if err := g.handleRouteRequestMessage(context.Background(), NewRequestMessage("msg-dsl-store", req)); err != nil {
		t.Fatalf("handleRouteRequestMessage returned error: %v", err)
	}

	select {
	case forwarded := <-forwardedCh:
		if forwarded.TargetAgentID != "archivalist" {
			t.Fatalf("forwarded target = %q, want archivalist", forwarded.TargetAgentID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for forwarded archivalist request")
	}

	deadline := time.After(200 * time.Millisecond)
	for {
		select {
		case stream := <-responseCh:
			if stream == nil || stream.Event == nil || stream.Event.Type != StreamEventProgress {
				continue
			}
			progress, _ := stream.Event.Data.(*ProgressData)
			if progress != nil && progress.Message == "Classifying request..." {
				t.Fatal("expected DSL store route to skip guide classification progress")
			}
		case <-deadline:
			return
		}
	}
}

func TestGuideRoute_DSLBypassesConversationStickiness(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	g, err := NewWithClassifier(NewRuleClassifierClient(), Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "session-dsl-stickiness",
		Factory: newTestFactory(t),
	})
	if err != nil {
		t.Fatalf("new guide: %v", err)
	}

	for _, info := range []*AgentRoutingInfo{
		{
			ID:   "librarian",
			Type: "librarian",
			Name: "librarian",
			Registration: &AgentRegistration{
				ID:   "librarian",
				Name: "librarian",
				Capabilities: AgentCapabilities{
					Intents: []Intent{IntentFind, IntentSearch, IntentLocate, IntentFetch, IntentRecall, IntentCheck, IntentHelp},
					Domains: []Domain{DomainCode, DomainPatterns, DomainGeneral},
				},
			},
		},
		{
			ID:   "archivalist",
			Type: "archivalist",
			Name: "archivalist",
			Registration: &AgentRegistration{
				ID:   "archivalist",
				Name: "archivalist",
				Capabilities: AgentCapabilities{
					Intents: []Intent{IntentStore, IntentRecall, IntentCheck, IntentDeclare, IntentComplete, IntentHelp},
					Domains: []Domain{DomainHistory, DomainPatterns, DomainDecisions, DomainLearnings, DomainFailures},
				},
			},
		},
	} {
		if err := g.Register(info); err != nil {
			t.Fatalf("register %s: %v", info.ID, err)
		}
		g.MarkAgentReady(info.ID)
	}

	// Seed active-conversation state toward the librarian first.
	first := &RouteRequest{
		Input:          "Find existing Python CLI patterns",
		SourceAgentID:  "architect",
		TargetAgentID:  "librarian",
		ExplicitTarget: true,
		SessionID:      "session-dsl-stickiness",
		Timestamp:      time.Now(),
	}
	forwarded, err := g.Route(context.Background(), first)
	if err != nil {
		t.Fatalf("route explicit librarian: %v", err)
	}
	if forwarded.TargetAgentID != "librarian" {
		t.Fatalf("first target = %q, want librarian", forwarded.TargetAgentID)
	}

	storeInput, err := ArchivalistStoreRouteInput("store health check result")
	if err != nil {
		t.Fatalf("build archivalist store input: %v", err)
	}

	second := &RouteRequest{
		Input:         storeInput,
		SourceAgentID: "librarian",
		SessionID:     "session-dsl-stickiness",
		Timestamp:     time.Now(),
	}
	followed, err := g.Route(context.Background(), second)
	if err != nil {
		t.Fatalf("route archivalist DSL: %v", err)
	}
	if followed.TargetAgentID != "archivalist" {
		t.Fatalf(
			"dsl route target = %q, want archivalist (method=%q intent=%q domain=%q)",
			followed.TargetAgentID,
			followed.ClassificationMethod,
			followed.Intent,
			followed.Domain,
		)
	}
	if followed.ClassificationMethod != "dsl" {
		t.Fatalf("dsl route method = %q, want dsl", followed.ClassificationMethod)
	}
}

func TestGuideResolveReadyAgentID_TaskScopedPipelineNameUsesRegisteredWorker(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	g, err := NewWithClassifier(NewRuleClassifierClient(), Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "session-task-scoped-pipeline",
		Factory: newTestFactory(t),
	})
	if err != nil {
		t.Fatalf("new guide: %v", err)
	}

	err = g.Register(&AgentRoutingInfo{
		ID:   "tester-worker-1",
		Type: "tester-pipeline",
		Name: "tester-pipeline",
		Aliases: []string{
			"task_1-tester-pipeline",
		},
		PodID: "task_1",
		Registration: &AgentRegistration{
			ID:   "tester-worker-1",
			Name: "tester-pipeline",
			Aliases: []string{
				"task_1-tester-pipeline",
			},
			Capabilities: AgentCapabilities{
				Intents: []Intent{IntentCheck, IntentHelp},
				Domains: []Domain{DomainCode, DomainTasks},
			},
		},
	})
	if err != nil {
		t.Fatalf("register tester pipeline worker: %v", err)
	}
	g.MarkAgentReady("tester-worker-1")

	if got := g.resolveReadyAgentID("task_1-tester-pipeline"); got != "tester-worker-1" {
		t.Fatalf("resolveReadyAgentID(task-scoped name) = %q, want tester-worker-1", got)
	}
}

func TestGuideRoute_ExplicitGuideTargetRetainsConversationContinuity(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	g, err := NewWithClassifier(NewRuleClassifierClient(), Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "session-explicit-guide",
		Factory: newTestFactory(t),
	})
	if err != nil {
		t.Fatalf("new guide: %v", err)
	}

	err = g.Register(&AgentRoutingInfo{
		ID:   "architect",
		Type: "architect",
		Name: "architect",
		Registration: &AgentRegistration{
			ID:   "architect",
			Name: "architect",
			Capabilities: AgentCapabilities{
				Intents: []Intent{IntentPlan, IntentDesign, IntentCheck, IntentHelp},
				Domains: []Domain{DomainDesign, DomainTasks},
			},
		},
	})
	if err != nil {
		t.Fatalf("register architect: %v", err)
	}

	first := &RouteRequest{
		Input:          "Design the API architecture",
		SourceAgentID:  "tui",
		TargetAgentID:  "architect",
		ExplicitTarget: true,
		SessionID:      "session-explicit-guide",
		Timestamp:      time.Now(),
	}
	forwarded, err := g.Route(context.Background(), first)
	if err != nil {
		t.Fatalf("route explicit architect: %v", err)
	}
	if forwarded.TargetAgentID != "architect" {
		t.Fatalf("first target = %q, want architect", forwarded.TargetAgentID)
	}

	followup := &RouteRequest{
		Input:          "Actually I want to discuss and plan building an API",
		SourceAgentID:  "tui",
		TargetAgentID:  "guide",
		ExplicitTarget: true,
		SessionID:      "session-explicit-guide",
		Timestamp:      time.Now(),
	}
	followed, err := g.Route(context.Background(), followup)
	if err != nil {
		t.Fatalf("route explicit guide follow-up: %v", err)
	}
	if followed.TargetAgentID != "architect" {
		t.Fatalf("follow-up target = %q, want architect", followed.TargetAgentID)
	}
	if len(followed.ConversationHistory) != 2 {
		t.Fatalf("follow-up history len = %d, want 2", len(followed.ConversationHistory))
	}
	if followed.ConversationHistory[1].UserInput != followup.Input {
		t.Fatalf("follow-up history[1].input = %q, want %q", followed.ConversationHistory[1].UserInput, followup.Input)
	}
	if followed.ConversationHistory[1].AgentID != "architect" {
		t.Fatalf("follow-up history[1].agent = %q, want architect", followed.ConversationHistory[1].AgentID)
	}
}

func TestGuideRoute_ExplicitNonGuideTargetOverridesConversationFlow(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	g, err := NewWithClassifier(NewRuleClassifierClient(), Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "session-2",
		Factory: newTestFactory(t),
	})
	if err != nil {
		t.Fatalf("new guide: %v", err)
	}

	for _, agent := range []*AgentRoutingInfo{
		{
			ID:   "architect",
			Type: "architect",
			Name: "architect",
			Registration: &AgentRegistration{
				ID:   "architect",
				Name: "architect",
				Capabilities: AgentCapabilities{
					Intents: []Intent{IntentPlan, IntentDesign, IntentHelp},
					Domains: []Domain{DomainDesign, DomainTasks},
				},
			},
		},
		{
			ID:   "librarian",
			Type: "librarian",
			Name: "librarian",
			Registration: &AgentRegistration{
				ID:   "librarian",
				Name: "librarian",
				Capabilities: AgentCapabilities{
					Intents: []Intent{IntentRecall, IntentSearch, IntentHelp},
					Domains: []Domain{DomainHistory, DomainGeneral},
				},
			},
		},
	} {
		if err := g.Register(agent); err != nil {
			t.Fatalf("register %s: %v", agent.ID, err)
		}
	}

	first := &RouteRequest{
		Input:         "Plan the OAuth architecture",
		SourceAgentID: "tui",
		TargetAgentID: "architect",
		SessionID:     "session-2",
		Timestamp:     time.Now(),
	}
	forwarded, err := g.Route(context.Background(), first)
	if err != nil {
		t.Fatalf("route explicit architect: %v", err)
	}
	if forwarded.TargetAgentID != "architect" {
		t.Fatalf("first target = %q, want architect", forwarded.TargetAgentID)
	}

	switchTrack := &RouteRequest{
		Input:         "Switching tracks: check prior auth references",
		SourceAgentID: "tui",
		TargetAgentID: "librarian",
		SessionID:     "session-2",
		Timestamp:     time.Now(),
	}
	followed, err := g.Route(context.Background(), switchTrack)
	if err != nil {
		t.Fatalf("route explicit librarian: %v", err)
	}
	if followed.TargetAgentID != "librarian" {
		t.Fatalf("switch target = %q, want librarian", followed.TargetAgentID)
	}
}

func TestGuideRoute_ExplicitAgentSwitchRetainsPerAgentHistory(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	g, err := NewWithClassifier(NewRuleClassifierClient(), Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "session-3",
		Factory: newTestFactory(t),
	})
	if err != nil {
		t.Fatalf("new guide: %v", err)
	}

	for _, agent := range []*AgentRoutingInfo{
		{
			ID:   "architect",
			Type: "architect",
			Name: "architect",
			Registration: &AgentRegistration{
				ID:   "architect",
				Name: "architect",
				Capabilities: AgentCapabilities{
					Intents: []Intent{IntentPlan, IntentDesign, IntentHelp},
					Domains: []Domain{DomainDesign, DomainTasks},
				},
			},
		},
		{
			ID:   "librarian",
			Type: "librarian",
			Name: "librarian",
			Registration: &AgentRegistration{
				ID:   "librarian",
				Name: "librarian",
				Capabilities: AgentCapabilities{
					Intents: []Intent{IntentRecall, IntentSearch, IntentHelp},
					Domains: []Domain{DomainHistory, DomainGeneral},
				},
			},
		},
	} {
		if err := g.Register(agent); err != nil {
			t.Fatalf("register %s: %v", agent.ID, err)
		}
	}

	firstArch := &RouteRequest{
		Input:          "Plan API surface and boundaries",
		SourceAgentID:  "tui",
		TargetAgentID:  "architect",
		ExplicitTarget: true,
		SessionID:      "session-3",
		Timestamp:      time.Now(),
	}
	firstArchFwd, err := g.Route(context.Background(), firstArch)
	if err != nil {
		t.Fatalf("route first architect: %v", err)
	}
	if firstArchFwd.TargetAgentID != "architect" {
		t.Fatalf("first architect target = %q, want architect", firstArchFwd.TargetAgentID)
	}
	if len(firstArchFwd.ConversationHistory) != 1 {
		t.Fatalf("first architect history len = %d, want 1", len(firstArchFwd.ConversationHistory))
	}

	libReq := &RouteRequest{
		Input:          "Search for existing API docs",
		SourceAgentID:  "tui",
		TargetAgentID:  "librarian",
		ExplicitTarget: true,
		SessionID:      "session-3",
		Timestamp:      time.Now(),
	}
	libFwd, err := g.Route(context.Background(), libReq)
	if err != nil {
		t.Fatalf("route librarian: %v", err)
	}
	if libFwd.TargetAgentID != "librarian" {
		t.Fatalf("librarian target = %q, want librarian", libFwd.TargetAgentID)
	}
	if len(libFwd.ConversationHistory) != 1 {
		t.Fatalf("librarian history len = %d, want 1", len(libFwd.ConversationHistory))
	}

	secondArch := &RouteRequest{
		Input:          "Now refine the API plan based on findings",
		SourceAgentID:  "tui",
		TargetAgentID:  "architect",
		ExplicitTarget: true,
		SessionID:      "session-3",
		Timestamp:      time.Now(),
	}
	secondArchFwd, err := g.Route(context.Background(), secondArch)
	if err != nil {
		t.Fatalf("route second architect: %v", err)
	}
	if secondArchFwd.TargetAgentID != "architect" {
		t.Fatalf("second architect target = %q, want architect", secondArchFwd.TargetAgentID)
	}
	if len(secondArchFwd.ConversationHistory) != 2 {
		t.Fatalf("second architect history len = %d, want 2", len(secondArchFwd.ConversationHistory))
	}
	if secondArchFwd.ConversationHistory[0].UserInput != firstArch.Input {
		t.Fatalf("second architect history[0].input = %q, want %q", secondArchFwd.ConversationHistory[0].UserInput, firstArch.Input)
	}
	if secondArchFwd.ConversationHistory[1].UserInput != secondArch.Input {
		t.Fatalf("second architect history[1].input = %q, want %q", secondArchFwd.ConversationHistory[1].UserInput, secondArch.Input)
	}
}
