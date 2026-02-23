package guide

import (
	"testing"
	"time"
)

func TestGuideApplyConversationFlow_RemapsFollowUpToActiveAgent(t *testing.T) {
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

	if err := g.Register(&AgentRoutingInfo{
		ID:   "architect",
		Type: "architect",
		Name: "architect",
		Registration: &AgentRegistration{
			ID:   "architect",
			Name: "architect",
			Capabilities: AgentCapabilities{
				Intents: []Intent{IntentPlan, IntentDesign, IntentCheck, IntentHelp},
				Domains: []Domain{DomainDesign, DomainTasks, DomainGeneral},
			},
		},
	}); err != nil {
		t.Fatalf("register architect: %v", err)
	}

	g.conversation.ObserveRoutedRequest("session-1", "architect")
	classification := &RouteResult{
		Intent:               IntentChat,
		Domain:               DomainGeneral,
		TargetAgent:          TargetGuide,
		Confidence:           0.61,
		Action:               RouteActionExecute,
		ClassificationMethod: "llm",
	}
	request := &RouteRequest{
		Input:         "Can we tweak that plan?",
		SessionID:     "session-1",
		SourceAgentID: "tui",
		Timestamp:     time.Now(),
	}

	updated, target := g.applyConversationFlow(request, classification, "guide")
	if target != "architect" {
		t.Fatalf("target = %q, want architect", target)
	}
	if updated == nil || updated.TargetAgent != TargetAgent("architect") {
		t.Fatalf("updated target = %v, want architect", updated.TargetAgent)
	}
	if updated.Intent != IntentPlan {
		t.Fatalf("intent = %q, want %q", updated.Intent, IntentPlan)
	}
}
