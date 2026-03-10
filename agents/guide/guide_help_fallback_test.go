package guide_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGuideRoute_FallsBackToGuideWhenTargetLacksIntentSupport(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer func() {
		_ = bus.Close()
	}()

	g, err := guide.NewWithClassifier(&guide.MockClassifierClient{DefaultTarget: "mock-agent"}, guide.Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "test-session",
	})
	require.NoError(t, err)

	err = g.Register(&guide.AgentRoutingInfo{
		ID:   "mock-agent",
		Type: "mock-agent",
		Name: "mock-agent",
		Registration: &guide.AgentRegistration{
			ID:   "mock-agent",
			Name: "mock-agent",
			Capabilities: guide.AgentCapabilities{
				Intents: []guide.Intent{guide.IntentCheck},
				Domains: []guide.Domain{guide.DomainCode},
			},
		},
	})
	require.NoError(t, err)

	req := &guide.RouteRequest{
		Input:         "Help me understand this workflow",
		SourceAgentID: "tui",
		Timestamp:     time.Now(),
	}

	forwarded, err := g.Route(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "guide", forwarded.TargetAgentID)
	assert.Equal(t, guide.IntentHelp, forwarded.Intent)
	assert.True(t, strings.Contains(forwarded.ClassificationMethod, "guide_fallback") || forwarded.ClassificationMethod == "llm")
}

func TestGuideRoute_ExplicitTargetHonorsUserSelection(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer func() {
		_ = bus.Close()
	}()

	g, err := guide.NewWithClassifier(&guide.MockClassifierClient{DefaultTarget: "mock-agent"}, guide.Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "test-session",
	})
	require.NoError(t, err)

	err = g.Register(&guide.AgentRoutingInfo{
		ID:   "mock-agent",
		Type: "mock-agent",
		Name: "mock-agent",
		Registration: &guide.AgentRegistration{
			ID:   "mock-agent",
			Name: "mock-agent",
			Capabilities: guide.AgentCapabilities{
				Intents: []guide.Intent{guide.IntentCheck},
				Domains: []guide.Domain{guide.DomainCode},
			},
		},
	})
	require.NoError(t, err)

	// User deliberately selected mock-agent (ExplicitTarget=true).
	// Even though IntentHelp is not in mock-agent's capabilities,
	// the request must be routed to the selected agent.
	req := &guide.RouteRequest{
		Input:          "route this directly",
		SourceAgentID:  "tui",
		TargetAgentID:  "mock-agent",
		ExplicitTarget: true,
		Timestamp:      time.Now(),
	}

	forwarded, err := g.Route(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "mock-agent", forwarded.TargetAgentID)
	assert.Equal(t, guide.IntentCheck, forwarded.Intent)
	assert.Equal(t, "explicit", forwarded.ClassificationMethod)
}

func TestGuideRoute_PromotesChatToHelpForSpecialistTarget(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer func() {
		_ = bus.Close()
	}()

	classifier := &fixedClassifierClient{payload: map[string]any{
		"is_retrospective": false,
		"intent":           "chat",
		"domain":           "general",
		"target_agent":     "architect",
		"confidence":       0.95,
	}}
	g, err := guide.NewWithClassifier(classifier, guide.Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "test-session",
	})
	require.NoError(t, err)

	err = g.Register(&guide.AgentRoutingInfo{
		ID:   "architect",
		Type: "architect",
		Name: "architect",
		Registration: &guide.AgentRegistration{
			ID:   "architect",
			Name: "architect",
			Capabilities: guide.AgentCapabilities{
				Intents: []guide.Intent{
					guide.IntentPlan,
					guide.IntentDesign,
					guide.IntentHelp,
				},
				Domains: []guide.Domain{guide.DomainDesign, guide.DomainTasks},
			},
		},
	})
	require.NoError(t, err)

	req := &guide.RouteRequest{
		Input:         "Can you help me get started with OAuth?",
		SourceAgentID: "tui",
		Timestamp:     time.Now(),
	}

	forwarded, err := g.Route(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "architect", forwarded.TargetAgentID)
	assert.Equal(t, guide.IntentHelp, forwarded.Intent)
	assert.NotContains(t, forwarded.ClassificationMethod, "guide_fallback")
}

func TestGuideRoute_NormalizesAcademicIntentBeforeGuideFallback(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer func() {
		_ = bus.Close()
	}()

	classifier := &fixedClassifierClient{payload: map[string]any{
		"is_retrospective": false,
		"intent":           "design",
		"domain":           "planning",
		"target_agent":     "academic",
		"confidence":       0.95,
	}}
	g, err := guide.NewWithClassifier(classifier, guide.Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "test-session",
	})
	require.NoError(t, err)

	err = g.Register(&guide.AgentRoutingInfo{
		ID:   "academic",
		Type: "academic",
		Name: "academic",
		Registration: &guide.AgentRegistration{
			ID:   "academic",
			Name: "academic",
			Capabilities: guide.AgentCapabilities{
				Intents: []guide.Intent{
					guide.IntentRecall,
					guide.IntentCheck,
					guide.IntentFetch,
					guide.IntentHelp,
					guide.IntentChat,
				},
				Domains: []guide.Domain{guide.DomainResearch},
			},
		},
	})
	require.NoError(t, err)

	forwarded, err := g.Route(context.Background(), &guide.RouteRequest{
		Input:         "How would you implement Oauth?",
		SourceAgentID: "tui",
		Timestamp:     time.Now(),
	})
	require.NoError(t, err)
	assert.Equal(t, "academic", forwarded.TargetAgentID)
	assert.Equal(t, guide.DomainResearch, forwarded.Domain)
	assert.NotContains(t, forwarded.ClassificationMethod, "guide_fallback")
}

func TestGuideRoute_CanonicalizesArchitectPlanningDomain(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer func() {
		_ = bus.Close()
	}()

	classifier := &fixedClassifierClient{payload: map[string]any{
		"is_retrospective": false,
		"intent":           "plan",
		"domain":           "planning",
		"target_agent":     "architect",
		"confidence":       0.95,
	}}
	g, err := guide.NewWithClassifier(classifier, guide.Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "test-session",
	})
	require.NoError(t, err)

	err = g.Register(&guide.AgentRoutingInfo{
		ID:   "architect",
		Type: "architect",
		Name: "architect",
		Registration: &guide.AgentRegistration{
			ID:   "architect",
			Name: "architect",
			Capabilities: guide.AgentCapabilities{
				Intents: []guide.Intent{
					guide.IntentPlan,
					guide.IntentDesign,
					guide.IntentExecute,
					guide.IntentHelp,
				},
				Domains: []guide.Domain{guide.DomainDesign, guide.DomainTasks},
			},
		},
	})
	require.NoError(t, err)

	forwarded, err := g.Route(context.Background(), &guide.RouteRequest{
		Input:         "Can you help me plan a REST API for user authentication?",
		SourceAgentID: "tui",
		Timestamp:     time.Now(),
	})
	require.NoError(t, err)
	assert.Equal(t, "architect", forwarded.TargetAgentID)
	assert.Equal(t, guide.DomainDesign, forwarded.Domain)
	assert.NotContains(t, forwarded.ClassificationMethod, "guide_fallback")
}

func TestGuideRoute_CanonicalizesLibrarianLocalDomain(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer func() {
		_ = bus.Close()
	}()

	classifier := &fixedClassifierClient{payload: map[string]any{
		"is_retrospective": false,
		"intent":           "find",
		"domain":           "local",
		"target_agent":     "librarian",
		"confidence":       0.95,
	}}
	g, err := guide.NewWithClassifier(classifier, guide.Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "test-session",
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
				Intents: []guide.Intent{
					guide.IntentFind,
					guide.IntentSearch,
					guide.IntentLocate,
					guide.IntentHelp,
				},
				Domains: []guide.Domain{guide.DomainCode},
			},
		},
	})
	require.NoError(t, err)

	forwarded, err := g.Route(context.Background(), &guide.RouteRequest{
		Input:         "Find the OAuth callback handler in the repo",
		SourceAgentID: "tui",
		Timestamp:     time.Now(),
	})
	require.NoError(t, err)
	assert.Equal(t, "librarian", forwarded.TargetAgentID)
	assert.Equal(t, guide.DomainCode, forwarded.Domain)
	assert.NotContains(t, forwarded.ClassificationMethod, "guide_fallback")
}

func TestGuideRoute_CanonicalizesArchivalistHistoryDomain(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer func() {
		_ = bus.Close()
	}()

	classifier := &fixedClassifierClient{payload: map[string]any{
		"is_retrospective": true,
		"intent":           "recall",
		"domain":           "history",
		"target_agent":     "archivalist",
		"confidence":       0.95,
	}}
	g, err := guide.NewWithClassifier(classifier, guide.Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "test-session",
	})
	require.NoError(t, err)

	err = g.Register(&guide.AgentRoutingInfo{
		ID:   "archivalist",
		Type: "archivalist",
		Name: "archivalist",
		Registration: &guide.AgentRegistration{
			ID:   "archivalist",
			Name: "archivalist",
			Capabilities: guide.AgentCapabilities{
				Intents: []guide.Intent{
					guide.IntentRecall,
					guide.IntentStore,
					guide.IntentCheck,
					guide.IntentDeclare,
					guide.IntentComplete,
					guide.IntentHelp,
				},
				Domains: []guide.Domain{
					guide.DomainPatterns,
					guide.DomainFailures,
					guide.DomainDecisions,
					guide.DomainFiles,
					guide.DomainLearnings,
					guide.DomainIntents,
				},
			},
			Constraints: guide.AgentConstraints{
				RetrospectiveOnly: true,
				TemporalFocus:     guide.TemporalPast,
			},
		},
	})
	require.NoError(t, err)

	forwarded, err := g.Route(context.Background(), &guide.RouteRequest{
		Input:         "Why did we decide to use OAuth device code last time?",
		SourceAgentID: "tui",
		Timestamp:     time.Now(),
	})
	require.NoError(t, err)
	assert.Equal(t, "archivalist", forwarded.TargetAgentID)
	assert.Equal(t, guide.DomainDecisions, forwarded.Domain)
	assert.NotContains(t, forwarded.ClassificationMethod, "guide_fallback")
}
