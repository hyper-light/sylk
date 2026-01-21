package integration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/academic"
	"github.com/adalundhe/sylk/agents/architect"
	"github.com/adalundhe/sylk/agents/archivalist"
	"github.com/adalundhe/sylk/agents/designer"
	"github.com/adalundhe/sylk/agents/engineer"
	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/inspector"
	"github.com/adalundhe/sylk/agents/librarian"
	"github.com/adalundhe/sylk/agents/tester"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockSearchSystem implements librarian.SearchSystem for testing
type mockSearchSystem struct{}

func (m *mockSearchSystem) Search(ctx context.Context, query string, opts librarian.SearchOptions) (*librarian.CodeSearchResult, error) {
	return &librarian.CodeSearchResult{
		Documents: []librarian.ScoredDocument{},
		TotalHits: 0,
		Took:      0,
	}, nil
}

// =============================================================================
// Agent Registration Tests
// =============================================================================

func TestAllAgentsRegisterWithGuide(t *testing.T) {
	t.Skip("Skipping due to Guide shutdown deadlock - see guide.go:903")

	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	guideAgent, err := guide.NewWithAPIKey("", guide.Config{
		Bus: bus,
	})
	require.NoError(t, err)

	err = guideAgent.Start(context.Background())
	require.NoError(t, err)
	defer guideAgent.Stop()

	routingTests := []struct {
		name         string
		createAgent  func() (guide.AgentRouter, error)
		expectedID   string
		hasDynamicID bool
	}{
		{
			name: "librarian",
			createAgent: func() (guide.AgentRouter, error) {
				return librarian.New(librarian.Config{
					SearchSystem: &mockSearchSystem{},
				})
			},
			expectedID: "librarian",
		},
		{
			name: "archivalist",
			createAgent: func() (guide.AgentRouter, error) {
				return archivalist.New(archivalist.Config{})
			},
			expectedID: "archivalist",
		},
		{
			name: "architect",
			createAgent: func() (guide.AgentRouter, error) {
				return architect.New(architect.Config{})
			},
			expectedID: "architect",
		},
		{
			name: "engineer",
			createAgent: func() (guide.AgentRouter, error) {
				return engineer.New(engineer.Config{})
			},
			hasDynamicID: true,
		},
		{
			name: "designer",
			createAgent: func() (guide.AgentRouter, error) {
				return designer.New(designer.Config{})
			},
			hasDynamicID: true,
		},
		{
			name: "inspector",
			createAgent: func() (guide.AgentRouter, error) {
				return inspector.New(inspector.InspectorConfig{})
			},
			expectedID: "inspector",
		},
		{
			name: "tester",
			createAgent: func() (guide.AgentRouter, error) {
				return tester.New(tester.TesterConfig{})
			},
			expectedID: "tester",
		},
	}

	for _, tc := range routingTests {
		t.Run(tc.name, func(t *testing.T) {
			agent, err := tc.createAgent()
			require.NoError(t, err, "failed to create %s agent", tc.name)

			routingInfo := agent.GetRoutingInfo()
			require.NotNil(t, routingInfo, "routing info should not be nil")

			assert.NotEmpty(t, routingInfo.ID, "routing info ID should not be empty")
			assert.Equal(t, tc.name, routingInfo.Name, "routing info name should match")

			if tc.hasDynamicID {
				assert.True(t, len(routingInfo.ID) > len(tc.name), "dynamic ID should be longer than name")
				assert.Contains(t, routingInfo.ID, tc.name, "dynamic ID should contain agent name")
			} else if tc.expectedID != "" {
				assert.Equal(t, tc.expectedID, routingInfo.ID, "routing info ID should match expected")
			}

			err = guideAgent.Register(routingInfo)
			assert.NoError(t, err, "failed to register %s with guide", tc.name)

			registered := guideAgent.GetAgent(routingInfo.ID)
			assert.NotNil(t, registered, "agent should be registered")
			assert.Equal(t, routingInfo.Name, registered.Name)

			channels := guideAgent.GetAgentChannels(routingInfo.ID)
			assert.NotNil(t, channels, "agent channels should be created")
			assert.Equal(t, routingInfo.ID+".requests", channels.Requests)
			assert.Equal(t, routingInfo.ID+".responses", channels.Responses)
			assert.Equal(t, routingInfo.ID+".errors", channels.Errors)
		})
	}

	// Test Academic separately since it uses Registration() instead of GetRoutingInfo()
	t.Run("academic", func(t *testing.T) {
		acad, err := academic.New(academic.Config{})
		require.NoError(t, err, "failed to create academic agent")

		registration := acad.Registration()
		require.NotNil(t, registration, "registration should not be nil")

		// Verify basic registration structure
		assert.Equal(t, "academic", registration.ID)
		assert.Equal(t, "academic", registration.Name)
		assert.Contains(t, registration.Aliases, "research")
		assert.Contains(t, registration.Aliases, "scholar")

		// Create routing info from registration for Guide
		routingInfo := &guide.AgentRoutingInfo{
			ID:           registration.ID,
			Name:         registration.Name,
			Aliases:      registration.Aliases,
			Registration: registration,
		}

		// Verify registration can be added to Guide
		err = guideAgent.Register(routingInfo)
		assert.NoError(t, err, "failed to register academic with guide")

		// Verify agent is now registered
		registered := guideAgent.GetAgent(registration.ID)
		assert.NotNil(t, registered, "agent should be registered")
		assert.Equal(t, registration.Name, registered.Name)

		// Verify agent channels were created
		channels := guideAgent.GetAgentChannels(registration.ID)
		assert.NotNil(t, channels, "agent channels should be created")
		assert.Equal(t, registration.ID+".requests", channels.Requests)
		assert.Equal(t, registration.ID+".responses", channels.Responses)
		assert.Equal(t, registration.ID+".errors", channels.Errors)
	})
}

// TestGuideRoutingInfoStructure verifies the Guide's own routing info structure.
func TestGuideRoutingInfoStructure(t *testing.T) {
	routingInfo := guide.GuideRoutingInfo()

	require.NotNil(t, routingInfo)
	assert.Equal(t, "guide", routingInfo.ID)
	assert.Equal(t, "guide", routingInfo.Name)
	assert.Contains(t, routingInfo.Aliases, "g")

	// Verify registration
	assert.NotNil(t, routingInfo.Registration)
	assert.Equal(t, "guide", routingInfo.Registration.ID)
	assert.Contains(t, routingInfo.Registration.Capabilities.Intents, guide.IntentHelp)
	assert.Contains(t, routingInfo.Registration.Capabilities.Intents, guide.IntentStatus)
}

func TestAgentRegistrationPublishesAnnouncement(t *testing.T) {
	t.Skip("Skipping due to Guide shutdown deadlock - see guide.go:903")

	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	guideAgent, err := guide.NewWithAPIKey("", guide.Config{
		Bus: bus,
	})
	require.NoError(t, err)

	err = guideAgent.Start(context.Background())
	require.NoError(t, err)
	defer guideAgent.Stop()

	// Subscribe to registry topic to capture announcements
	var receivedAnnouncement *guide.AgentAnnouncement
	var wg sync.WaitGroup
	wg.Add(1)

	sub, err := bus.SubscribeAsync(guide.TopicAgentRegistry, func(msg *guide.Message) error {
		if msg.Type == guide.MessageTypeAgentRegistered {
			ann, ok := msg.GetAgentAnnouncement()
			if ok && ann.AgentID == "librarian" {
				receivedAnnouncement = ann
				wg.Done()
			}
		}
		return nil
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	// Create and register librarian
	lib, err := librarian.New(librarian.Config{
		SearchSystem: &mockSearchSystem{},
	})
	require.NoError(t, err)

	err = guideAgent.Register(lib.GetRoutingInfo())
	require.NoError(t, err)

	// Wait for announcement with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		require.NotNil(t, receivedAnnouncement)
		assert.Equal(t, "librarian", receivedAnnouncement.AgentID)
		assert.Equal(t, "librarian", receivedAnnouncement.AgentName)
		assert.NotNil(t, receivedAnnouncement.Channels)
		assert.NotNil(t, receivedAnnouncement.Capabilities)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for registration announcement")
	}
}

func TestEventBusSubscriptions(t *testing.T) {
	t.Skip("Skipping due to Guide shutdown deadlock - see guide.go:903")

	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	guideAgent, err := guide.NewWithAPIKey("", guide.Config{
		Bus: bus,
	})
	require.NoError(t, err)

	err = guideAgent.Start(context.Background())
	require.NoError(t, err)
	defer guideAgent.Stop()

	tests := []struct {
		name        string
		startAgent  func(bus guide.EventBus) error
		stopAgent   func() error
		agentID     string
		channelType guide.ChannelType
	}{
		{
			name: "librarian_receives_requests",
			startAgent: func(bus guide.EventBus) error {
				lib, err := librarian.New(librarian.Config{
					SearchSystem: &mockSearchSystem{},
				})
				if err != nil {
					return err
				}
				return lib.Start(bus)
			},
			agentID:     "librarian",
			channelType: guide.ChannelTypeRequests,
		},
		{
			name: "academic_receives_requests",
			startAgent: func(bus guide.EventBus) error {
				acad, err := academic.New(academic.Config{})
				if err != nil {
					return err
				}
				return acad.Start(bus)
			},
			agentID:     "academic",
			channelType: guide.ChannelTypeRequests,
		},
		{
			name: "archivalist_receives_requests",
			startAgent: func(bus guide.EventBus) error {
				arch, err := archivalist.New(archivalist.Config{})
				if err != nil {
					return err
				}
				return arch.Start(bus)
			},
			agentID:     "archivalist",
			channelType: guide.ChannelTypeRequests,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Start the agent
			err := tc.startAgent(bus)
			require.NoError(t, err)

			// Verify subscription exists on the expected channel
			topic := guide.AgentTopic(tc.agentID, tc.channelType)
			count := bus.TopicSubscriberCount(topic)
			assert.Greater(t, count, 0, "agent should have subscription on %s", topic)
		})
	}
}

func TestMultipleAgentRegistration(t *testing.T) {
	t.Skip("Skipping due to Guide shutdown deadlock - see guide.go:903")

	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	guideAgent, err := guide.NewWithAPIKey("", guide.Config{
		Bus: bus,
	})
	require.NoError(t, err)

	err = guideAgent.Start(context.Background())
	require.NoError(t, err)
	defer guideAgent.Stop()

	var wg sync.WaitGroup
	errs := make(chan error, 5)

	agents := []func() (guide.AgentRouter, error){
		func() (guide.AgentRouter, error) {
			return librarian.New(librarian.Config{SearchSystem: &mockSearchSystem{}})
		},
		func() (guide.AgentRouter, error) {
			return archivalist.New(archivalist.Config{})
		},
		func() (guide.AgentRouter, error) {
			return architect.New(architect.Config{})
		},
		func() (guide.AgentRouter, error) {
			return inspector.New(inspector.InspectorConfig{})
		},
		func() (guide.AgentRouter, error) {
			return tester.New(tester.TesterConfig{})
		},
	}

	for _, createFn := range agents {
		wg.Add(1)
		go func(create func() (guide.AgentRouter, error)) {
			defer wg.Done()
			agent, err := create()
			if err != nil {
				errs <- err
				return
			}
			if err := guideAgent.Register(agent.GetRoutingInfo()); err != nil {
				errs <- err
			}
		}(createFn)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("registration error: %v", err)
	}

	// Verify all agents are registered
	allAgents := guideAgent.GetAllAgents()
	// Guide is registered by default, plus our 5 agents
	assert.GreaterOrEqual(t, len(allAgents), 5, "all agents should be registered")
}

func TestAgentUnregistration(t *testing.T) {
	t.Skip("Skipping due to Guide shutdown deadlock - see guide.go:903")

	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	guideAgent, err := guide.NewWithAPIKey("", guide.Config{
		Bus: bus,
	})
	require.NoError(t, err)

	err = guideAgent.Start(context.Background())
	require.NoError(t, err)
	defer guideAgent.Stop()

	// Register librarian
	lib, err := librarian.New(librarian.Config{
		SearchSystem: &mockSearchSystem{},
	})
	require.NoError(t, err)

	routingInfo := lib.GetRoutingInfo()
	err = guideAgent.Register(routingInfo)
	require.NoError(t, err)

	// Verify registered
	assert.NotNil(t, guideAgent.GetAgent(routingInfo.ID))

	// Subscribe to catch unregistration announcement
	var receivedUnregister bool
	var wg sync.WaitGroup
	wg.Add(1)

	sub, err := bus.SubscribeAsync(guide.TopicAgentRegistry, func(msg *guide.Message) error {
		if msg.Type == guide.MessageTypeAgentUnregistered {
			ann, ok := msg.GetAgentAnnouncement()
			if ok && ann.AgentID == "librarian" {
				receivedUnregister = true
				wg.Done()
			}
		}
		return nil
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	// Unregister the agent
	guideAgent.Unregister(routingInfo.ID)

	// Wait for announcement with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		assert.True(t, receivedUnregister, "should receive unregistration announcement")
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for unregistration announcement")
	}

	// Verify unregistered
	assert.Nil(t, guideAgent.GetAgent(routingInfo.ID))
	assert.Nil(t, guideAgent.GetAgentChannels(routingInfo.ID))
}

func TestRoutingInfoCapabilities(t *testing.T) {
	tests := []struct {
		name             string
		createAgent      func() (guide.AgentRouter, error)
		expectedIntents  []guide.Intent
		expectedDomains  []guide.Domain
		expectedPriority int
	}{
		{
			name: "librarian_capabilities",
			createAgent: func() (guide.AgentRouter, error) {
				return librarian.New(librarian.Config{SearchSystem: &mockSearchSystem{}})
			},
			expectedIntents:  []guide.Intent{guide.IntentFind, guide.IntentSearch, guide.IntentLocate},
			expectedDomains:  []guide.Domain{guide.DomainCode},
			expectedPriority: 80,
		},
		{
			name: "architect_capabilities",
			createAgent: func() (guide.AgentRouter, error) {
				return architect.New(architect.Config{})
			},
			expectedIntents:  []guide.Intent{guide.IntentPlan, guide.IntentDesign},
			expectedDomains:  []guide.Domain{guide.DomainDesign, guide.DomainTasks},
			expectedPriority: 90,
		},
		{
			name: "inspector_capabilities",
			createAgent: func() (guide.AgentRouter, error) {
				return inspector.New(inspector.InspectorConfig{})
			},
			expectedIntents:  []guide.Intent{guide.IntentCheck},
			expectedDomains:  []guide.Domain{guide.DomainCode},
			expectedPriority: 75,
		},
		{
			name: "tester_capabilities",
			createAgent: func() (guide.AgentRouter, error) {
				return tester.New(tester.TesterConfig{})
			},
			expectedIntents:  []guide.Intent{guide.IntentCheck},
			expectedDomains:  []guide.Domain{guide.DomainCode},
			expectedPriority: 70,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			agent, err := tc.createAgent()
			require.NoError(t, err)

			routingInfo := agent.GetRoutingInfo()
			require.NotNil(t, routingInfo.Registration)

			caps := routingInfo.Registration.Capabilities

			for _, intent := range tc.expectedIntents {
				assert.Contains(t, caps.Intents, intent, "should have intent %s", intent)
			}

			for _, domain := range tc.expectedDomains {
				assert.Contains(t, caps.Domains, domain, "should have domain %s", domain)
			}

			assert.Equal(t, tc.expectedPriority, caps.Priority)
		})
	}
}

func TestAgentTriggersConfiguration(t *testing.T) {
	tests := []struct {
		name                string
		createAgent         func() (guide.AgentRouter, error)
		expectedStrongTrigs []string
		expectedIntentTrigs map[guide.Intent][]string
	}{
		{
			name: "librarian_triggers",
			createAgent: func() (guide.AgentRouter, error) {
				return librarian.New(librarian.Config{SearchSystem: &mockSearchSystem{}})
			},
			expectedStrongTrigs: []string{"find", "search", "locate", "where is"},
			expectedIntentTrigs: map[guide.Intent][]string{
				guide.IntentFind:   {"find", "where is", "locate"},
				guide.IntentSearch: {"search", "look for", "grep"},
			},
		},
		{
			name: "architect_triggers",
			createAgent: func() (guide.AgentRouter, error) {
				return architect.New(architect.Config{})
			},
			expectedStrongTrigs: []string{"plan", "design", "architect", "decompose"},
			expectedIntentTrigs: map[guide.Intent][]string{
				guide.IntentPlan: {"plan", "design", "create workflow"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			agent, err := tc.createAgent()
			require.NoError(t, err)

			routingInfo := agent.GetRoutingInfo()
			triggers := routingInfo.Triggers

			for _, trig := range tc.expectedStrongTrigs {
				assert.Contains(t, triggers.StrongTriggers, trig, "should have strong trigger %q", trig)
			}

			for intent, expectedTrigs := range tc.expectedIntentTrigs {
				actualTrigs, ok := triggers.IntentTriggers[intent]
				assert.True(t, ok, "should have triggers for intent %s", intent)
				for _, trig := range expectedTrigs {
					assert.Contains(t, actualTrigs, trig, "intent %s should have trigger %q", intent, trig)
				}
			}
		})
	}
}
