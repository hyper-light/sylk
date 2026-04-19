package guide

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

type fixedGuideClassifierClient struct {
	payload map[string]any
}

func (c *fixedGuideClassifierClient) New(_ context.Context, _ anthropic.MessageNewParams) (*anthropic.Message, error) {
	body, _ := json.Marshal(c.payload)
	return &anthropic.Message{
		Content: []anthropic.ContentBlockUnion{{
			Type: "text",
			Text: string(body),
		}},
		Role:       "assistant",
		StopReason: "end_turn",
	}, nil
}

func TestGuideLearnsFinalizedCanonicalRoute(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer func() {
		_ = bus.Close()
	}()

	g, err := NewWithClassifier(&fixedGuideClassifierClient{payload: map[string]any{
		"is_retrospective": false,
		"intent":           "design",
		"domain":           "planning",
		"target_agent":     "academic",
		"confidence":       0.95,
	}}, Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "test-session",
		Factory: newTestFactory(t),
	})
	if err != nil {
		t.Fatalf("new guide: %v", err)
	}

	err = g.Register(&AgentRoutingInfo{
		ID:   "academic",
		Type: "academic",
		Name: "academic",
		Registration: &AgentRegistration{
			ID:   "academic",
			Name: "academic",
			Capabilities: AgentCapabilities{
				Intents: []Intent{IntentRecall, IntentCheck, IntentFetch, IntentHelp, IntentChat},
				Domains: []Domain{DomainResearch},
			},
		},
	})
	if err != nil {
		t.Fatalf("register academic: %v", err)
	}

	query := "How would you implement Oauth?"
	forwarded, err := g.Route(context.Background(), &RouteRequest{
		Input:         query,
		SourceAgentID: "tui",
		Timestamp:     time.Now(),
	})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if forwarded.TargetAgentID != "academic" {
		t.Fatalf("expected academic target, got %s", forwarded.TargetAgentID)
	}
	if forwarded.Intent != IntentHelp {
		t.Fatalf("expected canonicalized help intent, got %s", forwarded.Intent)
	}
	if forwarded.Domain != DomainResearch {
		t.Fatalf("expected canonicalized research domain, got %s", forwarded.Domain)
	}

	cached := g.routeCache.Get(query)
	if cached == nil {
		t.Fatalf("expected learned route cache entry")
	}
	if cached.TargetAgentID != "academic" || cached.Intent != IntentHelp || cached.Domain != DomainResearch {
		t.Fatalf("expected finalized route in cache, got %s/%s/%s", cached.TargetAgentID, cached.Intent, cached.Domain)
	}

	versioned := g.routeVersions.GetRouteByInput(normalizeInput(query))
	if versioned == nil {
		t.Fatalf("expected learned versioned route")
	}
	if versioned.TargetAgentID != "academic" || versioned.Intent != IntentHelp || versioned.Domain != DomainResearch {
		t.Fatalf("expected finalized route version, got %s/%s/%s", versioned.TargetAgentID, versioned.Intent, versioned.Domain)
	}
}
