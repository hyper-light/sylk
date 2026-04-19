package guide

import (
	"context"
	"testing"
	"time"
)

func TestGuideStartSubscribesPreRegisteredAgentChannels(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	g, err := NewWithClassifier(NewRuleClassifierClient(), Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "session-start-subscriptions",
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
				Intents: []Intent{IntentPlan, IntentDesign, IntentHelp},
				Domains: []Domain{DomainDesign, DomainGeneral},
			},
		},
	})
	if err != nil {
		t.Fatalf("register architect: %v", err)
	}

	if got := g.agentSubs.Len(); got != 0 {
		t.Fatalf("agent subscriptions before start = %d, want 0", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := g.Start(ctx); err != nil {
		t.Fatalf("start guide: %v", err)
	}
	defer func() { _ = g.Stop() }()

	if _, ok := g.agentSubs.Get("architect"); !ok {
		t.Fatalf("expected architect subscriptions to be created on start")
	}
}

