package integration

import (
	"context"
	"sync"
	"testing"

	"github.com/adalundhe/sylk/agents/designer"
	"github.com/adalundhe/sylk/agents/engineer"
	"github.com/adalundhe/sylk/agents/guide"
	inspPipeline "github.com/adalundhe/sylk/agents/inspector/pipeline"
	inspShared "github.com/adalundhe/sylk/agents/inspector/shared"
	"github.com/adalundhe/sylk/agents/tester"
	globaltester "github.com/adalundhe/sylk/agents/tester/global"
	"github.com/adalundhe/sylk/agents/tester/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEngineerScopeLimit(t *testing.T) {
	assert.Equal(t, 12, engineer.MaxTodosBeforeArchitect)
}

func TestEngineerCreation(t *testing.T) {
	eng, err := engineer.New(engineer.Config{}, nil)
	require.NoError(t, err)
	require.NotNil(t, eng)

	routingInfo := eng.GetRoutingInfo()
	require.NotNil(t, routingInfo)

	assert.Equal(t, "engineer", routingInfo.Name)
	assert.Equal(t, "engineer", routingInfo.Type)
	assert.NotEmpty(t, routingInfo.ID)
	assert.Equal(t, eng.AgentID(), routingInfo.ID)
}

func TestEngineerStartStop(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	eng, err := engineer.New(engineer.Config{}, nil)
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
	eng, err := engineer.New(engineer.Config{}, nil)
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
	des, err := designer.New(designer.Config{}, nil)
	require.NoError(t, err)
	require.NotNil(t, des)

	routingInfo := des.GetRoutingInfo()
	require.NotNil(t, routingInfo)

	assert.Equal(t, "designer", routingInfo.Name)
	assert.Equal(t, "designer", routingInfo.Type)
	assert.NotEmpty(t, routingInfo.ID)
	assert.NotContains(t, routingInfo.ID, "designer_")
}

func TestDesignerStartStop(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	des, err := designer.New(designer.Config{}, nil)
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
	des, err := designer.New(designer.Config{}, nil)
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

func TestPipelineInspectorDefaultConfig(t *testing.T) {
	cfg := inspShared.DefaultPipelineInspectorConfig()

	assert.NotEmpty(t, cfg.Model)
	assert.Greater(t, cfg.MaxToolRuns, 0)
	assert.Greater(t, cfg.MaxTokens, 0)
	assert.NotZero(t, cfg.DefaultTimeout)
	assert.Greater(t, cfg.MaxFeedbackLoops, 0)
}

func TestInspectorCreation(t *testing.T) {
	insp, err := inspPipeline.New(inspShared.PipelineInspectorConfig{}, nil)
	require.NoError(t, err)
	require.NotNil(t, insp)

	routingInfo := insp.GetRoutingInfo()
	require.NotNil(t, routingInfo)

	assert.NotEmpty(t, routingInfo.ID)
	assert.Equal(t, "inspector-pipeline", routingInfo.Type)
	assert.Equal(t, "inspector-pipeline", routingInfo.Name)
}

func TestInspectorStartStop(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	insp, err := inspPipeline.New(inspShared.PipelineInspectorConfig{}, nil)
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
	insp, err := inspPipeline.New(inspShared.PipelineInspectorConfig{}, nil)
	require.NoError(t, err)

	routingInfo := insp.GetRoutingInfo()
	require.NotNil(t, routingInfo)
	require.NotNil(t, routingInfo.Registration)

	caps := routingInfo.Registration.Capabilities
	assert.Contains(t, caps.Intents, guide.IntentCheck)
	assert.Contains(t, caps.Domains, guide.DomainCode)
	assert.Equal(t, 70, caps.Priority)
}

func TestInspectorConfigDefaults(t *testing.T) {
	insp, err := inspPipeline.New(inspShared.PipelineInspectorConfig{}, nil)
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
	tst, err := globaltester.New(shared.GlobalTesterConfig{}, nil)
	require.NoError(t, err)
	require.NotNil(t, tst)

	routingInfo := tst.GetRoutingInfo()
	require.NotNil(t, routingInfo)

	assert.NotContains(t, routingInfo.ID, "tester_")
	assert.Equal(t, "tester", routingInfo.Type)
	assert.Equal(t, "Tester", routingInfo.Name)
}

func TestTesterStartStop(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	tst, err := globaltester.New(shared.GlobalTesterConfig{}, nil)
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
	tst, err := globaltester.New(shared.GlobalTesterConfig{}, nil)
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

	eng, err := engineer.New(engineer.Config{}, nil)
	require.NoError(t, err)

	des, err := designer.New(designer.Config{}, nil)
	require.NoError(t, err)

	insp, err := inspPipeline.New(inspShared.PipelineInspectorConfig{}, nil)
	require.NoError(t, err)

	tst, err := globaltester.New(shared.GlobalTesterConfig{}, nil)
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
	_ = insp.Stop()
	tst.Stop()
}

func TestExecutionAgentsEventBusSubscriptions(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	type channelIDs struct {
		agentType string
		agentID   string
	}

	tests := []struct {
		name  string
		start func() (channelIDs, error)
	}{
		{
			name: "engineer",
			start: func() (channelIDs, error) {
				eng, err := engineer.New(engineer.Config{}, nil)
				if err != nil {
					return channelIDs{}, err
				}
				return channelIDs{"engineer", eng.AgentID()}, eng.Start(bus)
			},
		},
		{
			name: "designer",
			start: func() (channelIDs, error) {
				des, err := designer.New(designer.Config{}, nil)
				if err != nil {
					return channelIDs{}, err
				}
				return channelIDs{"designer", des.ID()}, des.Start(bus)
			},
		},
		{
			name: "inspector",
			start: func() (channelIDs, error) {
				insp, err := inspPipeline.New(inspShared.PipelineInspectorConfig{}, nil)
				if err != nil {
					return channelIDs{}, err
				}
				id := insp.AgentID()
				return channelIDs{"inspector-pipeline", id}, insp.Start(bus)
			},
		},
		{
			name: "tester",
			start: func() (channelIDs, error) {
				tst, err := globaltester.New(shared.GlobalTesterConfig{}, nil)
				if err != nil {
					return channelIDs{}, err
				}
				return channelIDs{"tester", tst.AgentID()}, tst.Start(bus)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ids, err := tc.start()
			require.NoError(t, err)

			requestTopic := guide.AgentTopic(ids.agentType, ids.agentID, guide.ChannelTypeRequests)
			responseTopic := guide.AgentTopic(ids.agentType, ids.agentID, guide.ChannelTypeResponses)

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

	eng, err := engineer.New(engineer.Config{}, nil)
	require.NoError(t, err)
	err = guideAgent.Register(eng.GetRoutingInfo())
	require.NoError(t, err)

	des, err := designer.New(designer.Config{}, nil)
	require.NoError(t, err)
	err = guideAgent.Register(des.GetRoutingInfo())
	require.NoError(t, err)

	insp, err := inspPipeline.New(inspShared.PipelineInspectorConfig{}, nil)
	require.NoError(t, err)
	err = guideAgent.Register(insp.GetRoutingInfo())
	require.NoError(t, err)

	tst, err := globaltester.New(shared.GlobalTesterConfig{}, nil)
	require.NoError(t, err)
	err = guideAgent.Register(tst.GetRoutingInfo())
	require.NoError(t, err)

	allAgents := guideAgent.GetAllAgents()
	assert.GreaterOrEqual(t, len(allAgents), 4)

	assert.NotNil(t, guideAgent.GetAgent(eng.GetRoutingInfo().ID))
	assert.NotNil(t, guideAgent.GetAgent(des.GetRoutingInfo().ID))
	assert.NotNil(t, guideAgent.GetAgent(insp.GetRoutingInfo().ID))
	assert.NotNil(t, guideAgent.GetAgent(tst.GetRoutingInfo().ID))
}
