package guide_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fixedClassifierClient struct {
	payload map[string]any
}

func (c *fixedClassifierClient) New(_ context.Context, _ anthropic.MessageNewParams) (*anthropic.Message, error) {
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

func TestGuideRoute_ChatForwardsToCapableAgent(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer func() {
		_ = bus.Close()
	}()

	classifier := &fixedClassifierClient{payload: map[string]any{
		"is_retrospective": false,
		"intent":           "chat",
		"domain":           "general",
		"target_agent":     "librarian",
		"confidence":       0.95,
	}}
	g, err := guide.NewWithClassifier(classifier, guide.Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "test-session",
		Factory: newTestFactory(t),
	})
	require.NoError(t, err)

	err = g.Register(&guide.AgentRoutingInfo{
		ID:   "librarian",
		Type: "librarian",
		Name: "librarian",
		Registration: &guide.AgentRegistration{
			ID:   "librarian",
			Name: "librarian",
			Capabilities: guide.AgentCapabilities{
				Intents: []guide.Intent{guide.IntentChat},
				Domains: []guide.Domain{guide.DomainGeneral},
			},
		},
	})
	require.NoError(t, err)

	forwarded, err := g.Route(context.Background(), &guide.RouteRequest{
		Input:         "hello there",
		SourceAgentID: "tui",
		Timestamp:     time.Now(),
	})
	require.NoError(t, err)
	// Agent explicitly supports IntentChat — request should be forwarded
	// to it rather than intercepted by the guide.
	assert.Equal(t, "librarian", forwarded.TargetAgentID)
	assert.Equal(t, guide.IntentChat, forwarded.Intent)
}

func TestGuideRoute_ChatFallsBackToGuideWhenUnsupported(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer func() {
		_ = bus.Close()
	}()

	classifier := &fixedClassifierClient{payload: map[string]any{
		"is_retrospective": false,
		"intent":           "chat",
		"domain":           "general",
		"target_agent":     "librarian",
		"confidence":       0.95,
	}}
	g, err := guide.NewWithClassifier(classifier, guide.Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "test-session",
		Factory: newTestFactory(t),
	})
	require.NoError(t, err)

	// Register agent WITHOUT IntentChat or IntentHelp — only task intent.
	err = g.Register(&guide.AgentRoutingInfo{
		ID:   "librarian",
		Type: "librarian",
		Name: "librarian",
		Registration: &guide.AgentRegistration{
			ID:   "librarian",
			Name: "librarian",
			Capabilities: guide.AgentCapabilities{
				Intents: []guide.Intent{guide.IntentFind},
				Domains: []guide.Domain{guide.DomainGeneral},
			},
		},
	})
	require.NoError(t, err)

	forwarded, err := g.Route(context.Background(), &guide.RouteRequest{
		Input:         "hello there",
		SourceAgentID: "tui",
		Timestamp:     time.Now(),
	})
	require.NoError(t, err)
	// Agent does NOT support chat — guide should intercept.
	assert.Equal(t, "guide", forwarded.TargetAgentID)
	assert.Equal(t, guide.IntentChat, forwarded.Intent)
	assert.Contains(t, forwarded.ClassificationMethod, "guide_fallback")
}
