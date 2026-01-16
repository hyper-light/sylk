package guide_test

import (
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRegistry_NewRegistry tests creating new registry
func TestRegistry_NewRegistry(t *testing.T) {
	registry := guide.NewRegistry()
	require.NotNil(t, registry)

	agents := registry.GetAll()
	assert.Len(t, agents, 0)
}

// TestRegistry_Register tests registering agents
func TestRegistry_Register(t *testing.T) {
	registry := guide.NewRegistry()

	reg := &guide.AgentRegistration{
		ID:      "agent-1",
		Name:    "TestAgent",
		Aliases: []string{"ta", "test"},
		Capabilities: guide.AgentCapabilities{
			Intents: []guide.Intent{guide.IntentRecall},
			Domains: []guide.Domain{guide.DomainPatterns},
		},
		Description: "Test agent",
		Priority:    100,
	}

	registry.Register(reg)

	// Should be able to get by ID
	agent := registry.Get("agent-1")
	require.NotNil(t, agent)
	assert.Equal(t, "TestAgent", agent.Name)
}

// TestRegistry_GetByName tests getting by name
func TestRegistry_GetByName(t *testing.T) {
	registry := guide.NewRegistry()

	registry.Register(&guide.AgentRegistration{
		ID:      "agent-1",
		Name:    "TestAgent",
		Aliases: []string{"ta", "test"},
	})

	// Get by exact name
	agent := registry.GetByName("TestAgent")
	require.NotNil(t, agent)
	assert.Equal(t, "agent-1", agent.ID)

	// Get by name (case insensitive)
	agent = registry.GetByName("testagent")
	require.NotNil(t, agent)

	// Get by alias
	agent = registry.GetByName("ta")
	require.NotNil(t, agent)

	agent = registry.GetByName("test")
	require.NotNil(t, agent)

	// Unknown name
	agent = registry.GetByName("unknown")
	assert.Nil(t, agent)
}

// TestRegistry_Unregister tests unregistering agents
func TestRegistry_Unregister(t *testing.T) {
	registry := guide.NewRegistry()

	registry.Register(&guide.AgentRegistration{
		ID:      "agent-1",
		Name:    "TestAgent",
		Aliases: []string{"ta"},
	})

	// Verify registered
	assert.NotNil(t, registry.Get("agent-1"))
	assert.NotNil(t, registry.GetByName("ta"))

	// Unregister
	registry.Unregister("agent-1")

	// Should no longer exist
	assert.Nil(t, registry.Get("agent-1"))
	assert.Nil(t, registry.GetByName("TestAgent"))
	assert.Nil(t, registry.GetByName("ta"))

	// Unregistering non-existent should be safe
	registry.Unregister("non-existent")
}

// TestRegistry_UpdateRegistration tests updating existing registration
func TestRegistry_UpdateRegistration(t *testing.T) {
	registry := guide.NewRegistry()

	// Initial registration
	registry.Register(&guide.AgentRegistration{
		ID:      "agent-1",
		Name:    "OldName",
		Aliases: []string{"old"},
	})

	assert.NotNil(t, registry.GetByName("OldName"))
	assert.NotNil(t, registry.GetByName("old"))

	// Update registration
	registry.Register(&guide.AgentRegistration{
		ID:      "agent-1",
		Name:    "NewName",
		Aliases: []string{"new"},
	})

	// Old names should be gone
	assert.Nil(t, registry.GetByName("OldName"))
	assert.Nil(t, registry.GetByName("old"))

	// New names should work
	assert.NotNil(t, registry.GetByName("NewName"))
	assert.NotNil(t, registry.GetByName("new"))
}

// TestRegistry_GetAll tests getting all agents
func TestRegistry_GetAll(t *testing.T) {
	registry := guide.NewRegistry()

	registry.Register(&guide.AgentRegistration{ID: "agent-1", Name: "A1"})
	registry.Register(&guide.AgentRegistration{ID: "agent-2", Name: "A2"})
	registry.Register(&guide.AgentRegistration{ID: "agent-3", Name: "A3"})

	all := registry.GetAll()
	assert.Len(t, all, 3)
}

// TestRegistry_FindBestMatch tests finding best agent match
func TestRegistry_FindBestMatch(t *testing.T) {
	registry := guide.NewRegistry()

	// Register agents with different capabilities
	registry.Register(&guide.AgentRegistration{
		ID:   "archivalist",
		Name: "Archivalist",
		Capabilities: guide.AgentCapabilities{
			Intents: []guide.Intent{guide.IntentRecall, guide.IntentStore},
			Domains: []guide.Domain{guide.DomainPatterns, guide.DomainFailures, guide.DomainDecisions},
		},
		Priority: 100,
	})

	registry.Register(&guide.AgentRegistration{
		ID:   "guide",
		Name: "Guide",
		Capabilities: guide.AgentCapabilities{
			Intents: []guide.Intent{guide.IntentHelp, guide.IntentStatus},
			Domains: []guide.Domain{guide.DomainSystem, guide.DomainAgents},
		},
		Priority: 50,
	})

	// Test pattern recall - should match archivalist
	result := &guide.RouteResult{
		Intent: guide.IntentRecall,
		Domain: guide.DomainPatterns,
	}
	best := registry.FindBestMatch(result)
	require.NotNil(t, best)
	assert.Equal(t, "archivalist", best.ID)

	// Test help - should match guide
	result = &guide.RouteResult{
		Intent: guide.IntentHelp,
		Domain: guide.DomainSystem,
	}
	best = registry.FindBestMatch(result)
	require.NotNil(t, best)
	assert.Equal(t, "guide", best.ID)

	// Test unknown - should return nil or best match
	result = &guide.RouteResult{
		Intent: guide.IntentUnknown,
		Domain: guide.DomainUnknown,
	}
	best = registry.FindBestMatch(result)
	// May be nil or match based on implementation
}

// TestRegistry_NewRegistryWithDefaults tests default registry
func TestRegistry_NewRegistryWithDefaults(t *testing.T) {
	registry := guide.NewRegistryWithDefaults()
	require.NotNil(t, registry)

	// Should have guide registered
	guideAgent := registry.Get("guide")
	require.NotNil(t, guideAgent)
	assert.Equal(t, "guide", guideAgent.Name)
}

// TestRegistry_GuideRegistration tests guide registration
func TestRegistry_GuideRegistration(t *testing.T) {
	reg := guide.GuideRegistration()
	require.NotNil(t, reg)

	assert.Equal(t, "guide", reg.ID)
	assert.Equal(t, "guide", reg.Name)
	assert.Contains(t, reg.Aliases, "g")
	assert.Contains(t, reg.Capabilities.Intents, guide.IntentHelp)
	assert.Contains(t, reg.Capabilities.Domains, guide.DomainSystem)
}

// TestRegistry_RegisterFromRoutingInfo tests registering from routing info
func TestRegistry_RegisterFromRoutingInfo(t *testing.T) {
	registry := guide.NewRegistry()

	info := &guide.AgentRoutingInfo{
		Registration: &guide.AgentRegistration{
			ID:   "test-agent",
			Name: "Test",
		},
	}

	registry.RegisterFromRoutingInfo(info)

	agent := registry.Get("test-agent")
	require.NotNil(t, agent)
	assert.Equal(t, "Test", agent.Name)

	// Nil info should be safe
	registry.RegisterFromRoutingInfo(nil)

	// Nil registration should be safe
	registry.RegisterFromRoutingInfo(&guide.AgentRoutingInfo{})
}

// TestAgentRegistration_MatchScore tests match scoring
func TestAgentRegistration_MatchScore(t *testing.T) {
	reg := &guide.AgentRegistration{
		ID:   "test",
		Name: "Test",
		Capabilities: guide.AgentCapabilities{
			Intents: []guide.Intent{guide.IntentRecall, guide.IntentStore},
			Domains: []guide.Domain{guide.DomainPatterns, guide.DomainFailures},
		},
		Priority: 100,
	}

	// Matching intent and domain should have high score
	result := &guide.RouteResult{
		Intent: guide.IntentRecall,
		Domain: guide.DomainPatterns,
	}
	score := reg.MatchScore(result)
	assert.Greater(t, score, 0)

	// Matching both intent and domain should have high score
	result = &guide.RouteResult{
		Intent: guide.IntentStore,
		Domain: guide.DomainFailures,
	}
	score = reg.MatchScore(result)
	assert.Greater(t, score, 0)

	// Non-matching intent should have zero score
	result = &guide.RouteResult{
		Intent: guide.IntentHelp,
		Domain: guide.DomainPatterns,
	}
	score = reg.MatchScore(result)
	assert.Equal(t, 0, score)

	// Non-matching domain should have zero score
	result = &guide.RouteResult{
		Intent: guide.IntentRecall,
		Domain: guide.DomainSystem,
	}
	score = reg.MatchScore(result)
	assert.Equal(t, 0, score)
}
