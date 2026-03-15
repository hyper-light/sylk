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

func TestGuideResolveReadyAgentID_TaskScopedPipelineNameUsesRegisteredWorker(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	g, err := NewWithClassifier(NewRuleClassifierClient(), Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "session-task-scoped-pipeline",
	})
	if err != nil {
		t.Fatalf("new guide: %v", err)
	}

	err = g.Register(&AgentRoutingInfo{
		ID:    "tester-worker-1",
		Type:  "tester-pipeline",
		Name:  "task_1-tester-pipeline",
		PodID: "task_1",
		Registration: &AgentRegistration{
			ID:   "tester-worker-1",
			Name: "task_1-tester-pipeline",
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
