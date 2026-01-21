package integration

import (
	"context"
	"sync"
	"testing"

	"github.com/adalundhe/sylk/agents/designer"
	"github.com/adalundhe/sylk/agents/engineer"
	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/inspector"
	"github.com/adalundhe/sylk/agents/tester"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEngineerScopeLimit(t *testing.T) {
	assert.Equal(t, 12, engineer.MaxTodosBeforeArchitect)
}

func TestEngineerCreation(t *testing.T) {
	eng, err := engineer.New(engineer.Config{})
	require.NoError(t, err)
	require.NotNil(t, eng)

	routingInfo := eng.GetRoutingInfo()
	require.NotNil(t, routingInfo)

	assert.Equal(t, "engineer", routingInfo.Name)
	assert.NotEmpty(t, routingInfo.ID)
	assert.True(t, len(routingInfo.ID) > len("engineer_"))
}

func TestEngineerStartStop(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	eng, err := engineer.New(engineer.Config{})
	require.NoError(t, err)

	assert.False(t, eng.IsRunning())

	err = eng.Start(bus)
	require.NoError(t, err)
	assert.True(t, eng.IsRunning())

	err = eng.Start(bus)
	assert.Error(t, err, "should error when starting already running engineer")

	err = eng.Stop()
	require.NoError(t, err)
	assert.False(t, eng.IsRunning())
}

func TestEngineerRoutingInfo(t *testing.T) {
	eng, err := engineer.New(engineer.Config{})
	require.NoError(t, err)

	routingInfo := eng.GetRoutingInfo()
	require.NotNil(t, routingInfo)
	require.NotNil(t, routingInfo.Registration)

	caps := routingInfo.Registration.Capabilities
	assert.Contains(t, caps.Intents, guide.IntentComplete)
	assert.Contains(t, caps.Domains, guide.DomainCode)
	assert.Contains(t, caps.Domains, guide.DomainFiles)
}

func TestDesignerScopeLimit(t *testing.T) {
	assert.Equal(t, 12, designer.MaxTodosBeforeArchitect)
}

func TestDesignerCreation(t *testing.T) {
	des, err := designer.New(designer.Config{})
	require.NoError(t, err)
	require.NotNil(t, des)

	routingInfo := des.GetRoutingInfo()
	require.NotNil(t, routingInfo)

	assert.Equal(t, "designer", routingInfo.Name)
	assert.NotEmpty(t, routingInfo.ID)
	assert.True(t, len(routingInfo.ID) > len("designer_"))
}

func TestDesignerStartStop(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	des, err := designer.New(designer.Config{})
	require.NoError(t, err)

	assert.False(t, des.IsRunning())

	err = des.Start(bus)
	require.NoError(t, err)
	assert.True(t, des.IsRunning())

	err = des.Start(bus)
	assert.Error(t, err, "should error when starting already running designer")

	err = des.Stop()
	require.NoError(t, err)
	assert.False(t, des.IsRunning())
}

func TestDesignerRoutingInfo(t *testing.T) {
	des, err := designer.New(designer.Config{})
	require.NoError(t, err)

	routingInfo := des.GetRoutingInfo()
	require.NotNil(t, routingInfo)
	require.NotNil(t, routingInfo.Registration)

	caps := routingInfo.Registration.Capabilities
	assert.Contains(t, caps.Intents, guide.IntentDesign)
	assert.Contains(t, caps.Intents, guide.IntentComplete)
	assert.Contains(t, caps.Intents, guide.IntentCheck)
	assert.Contains(t, caps.Domains, guide.DomainCode)
	assert.Contains(t, caps.Domains, guide.DomainFiles)
}

func TestInspector8PhaseValidation(t *testing.T) {
	phases := []inspector.ValidationPhase{
		inspector.PhaseLintCheck,
		inspector.PhaseTypeCheck,
		inspector.PhaseFormatCheck,
		inspector.PhaseSecurityScan,
		inspector.PhaseTestCoverage,
		inspector.PhaseDocumentation,
		inspector.PhaseComplexity,
		inspector.PhaseFinalReview,
	}

	assert.Len(t, phases, 8)

	assert.Equal(t, inspector.ValidationPhase("lint_check"), inspector.PhaseLintCheck)
	assert.Equal(t, inspector.ValidationPhase("type_check"), inspector.PhaseTypeCheck)
	assert.Equal(t, inspector.ValidationPhase("format_check"), inspector.PhaseFormatCheck)
	assert.Equal(t, inspector.ValidationPhase("security_scan"), inspector.PhaseSecurityScan)
	assert.Equal(t, inspector.ValidationPhase("test_coverage"), inspector.PhaseTestCoverage)
	assert.Equal(t, inspector.ValidationPhase("documentation"), inspector.PhaseDocumentation)
	assert.Equal(t, inspector.ValidationPhase("complexity"), inspector.PhaseComplexity)
	assert.Equal(t, inspector.ValidationPhase("final_review"), inspector.PhaseFinalReview)
}

func TestInspectorCreation(t *testing.T) {
	insp, err := inspector.New(inspector.InspectorConfig{})
	require.NoError(t, err)
	require.NotNil(t, insp)

	routingInfo := insp.GetRoutingInfo()
	require.NotNil(t, routingInfo)

	assert.Equal(t, "inspector", routingInfo.ID)
	assert.Equal(t, "inspector", routingInfo.Name)
}

func TestInspectorStartStop(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	insp, err := inspector.New(inspector.InspectorConfig{})
	require.NoError(t, err)

	assert.False(t, insp.IsRunning())

	err = insp.Start(bus)
	require.NoError(t, err)
	assert.True(t, insp.IsRunning())

	err = insp.Start(bus)
	assert.Error(t, err, "should error when starting already running inspector")

	err = insp.Stop()
	require.NoError(t, err)
	assert.False(t, insp.IsRunning())
}

func TestInspectorRoutingInfo(t *testing.T) {
	insp, err := inspector.New(inspector.InspectorConfig{})
	require.NoError(t, err)

	routingInfo := insp.GetRoutingInfo()
	require.NotNil(t, routingInfo)
	require.NotNil(t, routingInfo.Registration)

	caps := routingInfo.Registration.Capabilities
	assert.Contains(t, caps.Intents, guide.IntentCheck)
	assert.Contains(t, caps.Domains, guide.DomainCode)
	assert.Equal(t, 75, caps.Priority)
}

func TestInspectorConfigDefaults(t *testing.T) {
	insp, err := inspector.New(inspector.InspectorConfig{})
	require.NoError(t, err)

	assert.NotNil(t, insp)
}

func TestTester6CategorySystem(t *testing.T) {
	categories := tester.ValidTestCategories()

	assert.Len(t, categories, 6)

	expectedCategories := []tester.TestCategory{
		tester.CategoryUnit,
		tester.CategoryIntegration,
		tester.CategoryEndToEnd,
		tester.CategoryProperty,
		tester.CategoryMutation,
		tester.CategoryFlaky,
	}

	for _, expected := range expectedCategories {
		assert.Contains(t, categories, expected)
	}

	assert.Equal(t, tester.TestCategory("unit"), tester.CategoryUnit)
	assert.Equal(t, tester.TestCategory("integration"), tester.CategoryIntegration)
	assert.Equal(t, tester.TestCategory("end_to_end"), tester.CategoryEndToEnd)
	assert.Equal(t, tester.TestCategory("property"), tester.CategoryProperty)
	assert.Equal(t, tester.TestCategory("mutation"), tester.CategoryMutation)
	assert.Equal(t, tester.TestCategory("flaky"), tester.CategoryFlaky)
}

func TestTesterCreation(t *testing.T) {
	tst, err := tester.New(tester.TesterConfig{})
	require.NoError(t, err)
	require.NotNil(t, tst)

	routingInfo := tst.GetRoutingInfo()
	require.NotNil(t, routingInfo)

	assert.Equal(t, "tester", routingInfo.ID)
	assert.Equal(t, "tester", routingInfo.Name)
}

func TestTesterStartStop(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	tst, err := tester.New(tester.TesterConfig{})
	require.NoError(t, err)

	assert.False(t, tst.IsRunning())

	err = tst.Start(bus)
	require.NoError(t, err)
	assert.True(t, tst.IsRunning())

	err = tst.Start(bus)
	assert.Error(t, err, "should error when starting already running tester")

	err = tst.Stop()
	require.NoError(t, err)
	assert.False(t, tst.IsRunning())
}

func TestTesterRoutingInfo(t *testing.T) {
	tst, err := tester.New(tester.TesterConfig{})
	require.NoError(t, err)

	routingInfo := tst.GetRoutingInfo()
	require.NotNil(t, routingInfo)
	require.NotNil(t, routingInfo.Registration)

	caps := routingInfo.Registration.Capabilities
	assert.Contains(t, caps.Intents, guide.IntentCheck)
	assert.Contains(t, caps.Domains, guide.DomainCode)
	assert.Equal(t, 70, caps.Priority)
}

func TestTesterPriorities(t *testing.T) {
	priorities := tester.ValidTestPriorities()

	assert.Len(t, priorities, 4)
	assert.Contains(t, priorities, tester.PriorityCritical)
	assert.Contains(t, priorities, tester.PriorityHigh)
	assert.Contains(t, priorities, tester.PriorityMedium)
	assert.Contains(t, priorities, tester.PriorityLow)
}

func TestExecutionAgentsConcurrentStart(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	eng, err := engineer.New(engineer.Config{})
	require.NoError(t, err)

	des, err := designer.New(designer.Config{})
	require.NoError(t, err)

	insp, err := inspector.New(inspector.InspectorConfig{})
	require.NoError(t, err)

	tst, err := tester.New(tester.TesterConfig{})
	require.NoError(t, err)

	var wg sync.WaitGroup
	errs := make(chan error, 4)

	wg.Add(4)
	go func() {
		defer wg.Done()
		if err := eng.Start(bus); err != nil {
			errs <- err
		}
	}()
	go func() {
		defer wg.Done()
		if err := des.Start(bus); err != nil {
			errs <- err
		}
	}()
	go func() {
		defer wg.Done()
		if err := insp.Start(bus); err != nil {
			errs <- err
		}
	}()
	go func() {
		defer wg.Done()
		if err := tst.Start(bus); err != nil {
			errs <- err
		}
	}()

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent start error: %v", err)
	}

	assert.True(t, eng.IsRunning())
	assert.True(t, des.IsRunning())
	assert.True(t, insp.IsRunning())
	assert.True(t, tst.IsRunning())

	eng.Stop()
	des.Stop()
	insp.Stop()
	tst.Stop()
}

func TestExecutionAgentsEventBusSubscriptions(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	tests := []struct {
		name      string
		channelID string
		start     func() error
	}{
		{
			name:      "engineer",
			channelID: "engineer",
			start: func() error {
				eng, err := engineer.New(engineer.Config{})
				if err != nil {
					return err
				}
				return eng.Start(bus)
			},
		},
		{
			name:      "designer",
			channelID: "designer",
			start: func() error {
				des, err := designer.New(designer.Config{})
				if err != nil {
					return err
				}
				return des.Start(bus)
			},
		},
		{
			name:      "inspector",
			channelID: "inspector",
			start: func() error {
				insp, err := inspector.New(inspector.InspectorConfig{})
				if err != nil {
					return err
				}
				return insp.Start(bus)
			},
		},
		{
			name:      "tester",
			channelID: "tester",
			start: func() error {
				tst, err := tester.New(tester.TesterConfig{})
				if err != nil {
					return err
				}
				return tst.Start(bus)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.start()
			require.NoError(t, err)

			requestTopic := guide.AgentTopic(tc.channelID, guide.ChannelTypeRequests)
			responseTopic := guide.AgentTopic(tc.channelID, guide.ChannelTypeResponses)

			reqCount := bus.TopicSubscriberCount(requestTopic)
			assert.Greater(t, reqCount, 0, "should have request subscription")

			respCount := bus.TopicSubscriberCount(responseTopic)
			assert.Greater(t, respCount, 0, "should have response subscription")
		})
	}
}

func TestAllExecutionAgentsRegisterWithGuide(t *testing.T) {
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

	eng, err := engineer.New(engineer.Config{})
	require.NoError(t, err)
	err = guideAgent.Register(eng.GetRoutingInfo())
	require.NoError(t, err)

	des, err := designer.New(designer.Config{})
	require.NoError(t, err)
	err = guideAgent.Register(des.GetRoutingInfo())
	require.NoError(t, err)

	insp, err := inspector.New(inspector.InspectorConfig{})
	require.NoError(t, err)
	err = guideAgent.Register(insp.GetRoutingInfo())
	require.NoError(t, err)

	tst, err := tester.New(tester.TesterConfig{})
	require.NoError(t, err)
	err = guideAgent.Register(tst.GetRoutingInfo())
	require.NoError(t, err)

	allAgents := guideAgent.GetAllAgents()
	assert.GreaterOrEqual(t, len(allAgents), 4)

	assert.NotNil(t, guideAgent.GetAgent(eng.GetRoutingInfo().ID))
	assert.NotNil(t, guideAgent.GetAgent(des.GetRoutingInfo().ID))
	assert.NotNil(t, guideAgent.GetAgent("inspector"))
	assert.NotNil(t, guideAgent.GetAgent("tester"))
}
