package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- helpers ---

func testScope() *concurrency.GoroutineScope {
	return concurrency.NewGoroutineScope(context.Background(), "test-router", nil)
}

func testRouter(bus guide.EventBus, scope *concurrency.GoroutineScope) *TaskRouter {
	return NewTaskRouter(TaskRouterConfig{
		Bus:       bus,
		Scope:     scope,
		AgentID:   "orchestrator",
		SessionID: "test-session",
	})
}

func testTask() *PipelineTask {
	return &PipelineTask{
		NodeID:    "node-1",
		DAGID:     "dag-1",
		TaskID:    "task-1",
		AgentType: "engineer",
		Prompt:    "implement feature X",
		SessionID: "test-session",
	}
}

// simulateGuideResponse publishes a RouteResponse with the given correlationID
// to the orchestrator's response channel, simulating what the Guide does when
// a pipeline agent responds via response.<type>.<id>.
func simulateGuideResponse(bus guide.EventBus, corrID string, success bool, data any, errMsg string) {
	resp := &guide.RouteResponse{
		CorrelationID:     corrID,
		Success:           success,
		Data:              data,
		Error:             errMsg,
		RespondingAgentID: "engineer",
	}
	msg := guide.NewResponseMessage("", resp)
	msg.CorrelationID = corrID
	bus.Publish(guide.TopicResponses("orchestrator", "orchestrator"), msg)
}

func collectPipelineUpdate(t *testing.T, bus guide.EventBus, topic string, timeout time.Duration) *PipelineUpdate {
	t.Helper()
	ch := make(chan *PipelineUpdate, 1)
	sub, err := bus.SubscribeAsync(topic, func(msg *guide.Message) error {
		if update, ok := msg.Payload.(*PipelineUpdate); ok {
			ch <- update
		}
		return nil
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	select {
	case update := <-ch:
		return update
	case <-time.After(timeout):
		t.Fatal("timed out waiting for pipeline update on " + topic)
		return nil
	}
}

// interceptGuideRequest subscribes to guide.requests and returns the first
// RouteRequest received, along with its correlationID.
func interceptGuideRequest(t *testing.T, bus guide.EventBus, timeout time.Duration) *guide.RouteRequest {
	t.Helper()
	ch := make(chan *guide.RouteRequest, 1)
	sub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		if req, ok := msg.GetRouteRequest(); ok {
			ch <- req
		}
		return nil
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	select {
	case req := <-ch:
		return req
	case <-time.After(timeout):
		t.Fatal("timed out waiting for RouteRequest on guide.requests")
		return nil
	}
}

// --- tests ---

func TestTaskRouter_RoutePublishesToGuide(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	scope := testScope()
	router := testRouter(bus, scope)
	task := testTask()

	// Subscribe to guide.requests BEFORE routing
	reqCh := make(chan *guide.RouteRequest, 1)
	sub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		if req, ok := msg.GetRouteRequest(); ok {
			reqCh <- req
		}
		return nil
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	err = router.Route(task)
	require.NoError(t, err)

	select {
	case req := <-reqCh:
		assert.Equal(t, "engineer", req.TargetAgentID)
		assert.True(t, req.ExplicitTarget)
		assert.Equal(t, "orchestrator", req.SourceAgentID)
		assert.Equal(t, "test-session", req.SessionID)
		assert.Contains(t, req.Input, "node-1")
		assert.Contains(t, req.Input, "implement feature X")
	case <-time.After(2 * time.Second):
		t.Fatal("RouteRequest was not published to guide.requests")
	}
}

func TestTaskRouter_SuccessResponse(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	scope := testScope()
	router := testRouter(bus, scope)
	task := testTask()

	// Subscribe to pipeline.update.engineer for the result
	updateCh := make(chan *PipelineUpdate, 1)
	sub, err := bus.SubscribeAsync("pipeline.update.engineer", func(msg *guide.Message) error {
		if update, ok := msg.Payload.(*PipelineUpdate); ok {
			updateCh <- update
		}
		return nil
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	// Intercept the guide request to get the correlationID
	guideCh := make(chan string, 1)
	guideSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		if req, ok := msg.GetRouteRequest(); ok {
			guideCh <- req.CorrelationID
		}
		return nil
	})
	require.NoError(t, err)
	defer guideSub.Unsubscribe()

	err = router.Route(task)
	require.NoError(t, err)

	// Get the correlationID and simulate a successful response
	var corrID string
	select {
	case corrID = <-guideCh:
	case <-time.After(2 * time.Second):
		t.Fatal("no guide request intercepted")
	}

	simulateGuideResponse(bus, corrID, true, "task output", "")
	// Deliver the response to the router (simulating handleBusResponse)
	time.Sleep(50 * time.Millisecond) // let bus deliver

	// The router subscribes via its pending channel, but we need to deliver
	// the response message to it. In production, handleBusResponse does this.
	// Here we simulate by constructing the response message directly.
	resp := &guide.RouteResponse{
		CorrelationID:     corrID,
		Success:           true,
		Data:              "task output",
		RespondingAgentID: "engineer",
	}
	respMsg := guide.NewResponseMessage("", resp)
	respMsg.CorrelationID = corrID
	router.DeliverResponse(respMsg)

	select {
	case update := <-updateCh:
		assert.Equal(t, "succeeded", update.Status)
		assert.Equal(t, "node-1", update.NodeID)
		assert.Equal(t, "dag-1", update.DAGID)
		assert.Equal(t, "task-1", update.TaskID)
		assert.Equal(t, "engineer", update.AgentType)
		assert.Equal(t, "task output", update.Output)
		assert.Equal(t, 1.0, update.Progress)
	case <-time.After(2 * time.Second):
		t.Fatal("pipeline update was not published")
	}
}

func TestTaskRouter_DeliverResponse_RecordsNodeActivityForTerminalResponse(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	scope := testScope()
	activityCh := make(chan struct {
		dagID  string
		nodeID string
	}, 1)
	router := NewTaskRouter(TaskRouterConfig{
		Bus:       bus,
		Scope:     scope,
		AgentID:   "orchestrator",
		SessionID: "test-session",
		OnNodeActivity: func(dagID, nodeID string) {
			activityCh <- struct {
				dagID  string
				nodeID string
			}{dagID: dagID, nodeID: nodeID}
		},
	})
	task := testTask()

	guideCh := make(chan string, 1)
	guideSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		if req, ok := msg.GetRouteRequest(); ok {
			guideCh <- req.CorrelationID
		}
		return nil
	})
	require.NoError(t, err)
	defer guideSub.Unsubscribe()

	err = router.Route(task)
	require.NoError(t, err)

	var corrID string
	select {
	case corrID = <-guideCh:
	case <-time.After(2 * time.Second):
		t.Fatal("no guide request intercepted")
	}

	resp := &guide.RouteResponse{
		CorrelationID:     corrID,
		Success:           true,
		Data:              "task output",
		RespondingAgentID: "engineer",
	}
	respMsg := guide.NewResponseMessage("", resp)
	respMsg.CorrelationID = corrID
	require.True(t, router.DeliverResponse(respMsg))

	select {
	case activity := <-activityCh:
		assert.Equal(t, task.DAGID, activity.dagID)
		assert.Equal(t, task.NodeID, activity.nodeID)
	case <-time.After(2 * time.Second):
		t.Fatal("terminal response did not record node activity")
	}
}

func TestTaskRouter_ReconcilesLateTerminalResponseAfterDone(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	scope := testScope()
	router := testRouter(bus, scope)
	task := testTask()
	done := make(chan struct{})

	updateCh := make(chan *PipelineUpdate, 1)
	sub, err := bus.SubscribeAsync("pipeline.update.engineer", func(msg *guide.Message) error {
		if update, ok := msg.Payload.(*PipelineUpdate); ok {
			updateCh <- update
		}
		return nil
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	guideCh := make(chan string, 1)
	guideSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		if req, ok := msg.GetRouteRequest(); ok {
			guideCh <- req.CorrelationID
		}
		return nil
	})
	require.NoError(t, err)
	defer guideSub.Unsubscribe()

	err = router.RouteWithLifecycle(task, done)
	require.NoError(t, err)

	var corrID string
	select {
	case corrID = <-guideCh:
	case <-time.After(2 * time.Second):
		t.Fatal("no guide request intercepted")
	}

	close(done)

	resp := &guide.RouteResponse{
		CorrelationID:     corrID,
		Success:           true,
		Data:              "late output",
		RespondingAgentID: "engineer",
	}
	respMsg := guide.NewResponseMessage("", resp)
	respMsg.CorrelationID = corrID
	require.True(t, router.DeliverResponse(respMsg))

	select {
	case update := <-updateCh:
		assert.Equal(t, "succeeded", update.Status)
		assert.Equal(t, "late output", update.Output)
	case <-time.After(2 * time.Second):
		t.Fatal("late terminal response was not reconciled")
	}
}

func TestTaskRouter_FailureResponse(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	scope := testScope()
	router := testRouter(bus, scope)
	task := testTask()

	updateCh := make(chan *PipelineUpdate, 1)
	sub, err := bus.SubscribeAsync("pipeline.update.engineer", func(msg *guide.Message) error {
		if update, ok := msg.Payload.(*PipelineUpdate); ok {
			updateCh <- update
		}
		return nil
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	guideCh := make(chan string, 1)
	guideSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		if req, ok := msg.GetRouteRequest(); ok {
			guideCh <- req.CorrelationID
		}
		return nil
	})
	require.NoError(t, err)
	defer guideSub.Unsubscribe()

	err = router.Route(task)
	require.NoError(t, err)

	var corrID string
	select {
	case corrID = <-guideCh:
	case <-time.After(2 * time.Second):
		t.Fatal("no guide request intercepted")
	}

	// Simulate a failure response from the Guide
	resp := &guide.RouteResponse{
		CorrelationID:     corrID,
		Success:           false,
		Error:             "agent engineer not available",
		RespondingAgentID: "guide",
	}
	respMsg := guide.NewResponseMessage("", resp)
	respMsg.CorrelationID = corrID
	router.DeliverResponse(respMsg)

	select {
	case update := <-updateCh:
		assert.Equal(t, "failed", update.Status)
		assert.Contains(t, update.Error, "agent engineer not available")
		assert.Equal(t, "node-1", update.NodeID)
	case <-time.After(2 * time.Second):
		t.Fatal("failure update was not published")
	}
}

func TestTaskRouter_DeliverResponseIgnoresUnknownCorrelation(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	scope := testScope()
	router := testRouter(bus, scope)

	resp := &guide.RouteResponse{
		CorrelationID: "unknown-corr-id",
		Success:       true,
	}
	msg := guide.NewResponseMessage("", resp)
	msg.CorrelationID = "unknown-corr-id"

	consumed := router.DeliverResponse(msg)
	assert.False(t, consumed)
}

func TestTaskRouter_DeliverResponse_MirrorsPipelineStreamToTUI(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	scope := testScope()
	router := testRouter(bus, scope)
	task := testTask()

	updateCh := make(chan *PipelineUpdate, 1)
	sub, err := bus.SubscribeAsync("pipeline.update.engineer", func(msg *guide.Message) error {
		if update, ok := msg.Payload.(*PipelineUpdate); ok {
			updateCh <- update
		}
		return nil
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	guideCh := make(chan string, 1)
	guideSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		if req, ok := msg.GetRouteRequest(); ok {
			guideCh <- req.CorrelationID
		}
		return nil
	})
	require.NoError(t, err)
	defer guideSub.Unsubscribe()

	tuiCh := make(chan *guide.StreamResponse, 1)
	tuiSub, err := bus.SubscribeAsync(guide.TopicResponses("tui", "tui"), func(msg *guide.Message) error {
		if stream, ok := msg.GetStreamResponse(); ok {
			tuiCh <- stream
		}
		return nil
	})
	require.NoError(t, err)
	defer tuiSub.Unsubscribe()

	err = router.Route(task)
	require.NoError(t, err)

	var corrID string
	select {
	case corrID = <-guideCh:
	case <-time.After(2 * time.Second):
		t.Fatal("no guide request intercepted")
	}

	streamMsg := &guide.Message{
		ID:            "stream-1",
		CorrelationID: corrID,
		Type:          guide.MessageTypeStream,
		Payload: &guide.StreamResponse{
			CorrelationID:     corrID,
			RespondingAgentID: "task-1:engineer",
			Metadata: map[string]any{
				"agent_type":  "engineer",
				"task_id":     "task-1",
				"task_slug":   "hello-cli",
				"pipeline_id": "task-1",
			},
			Event: &guide.StreamEvent{
				Type: guide.StreamEventProgress,
				Data: &guide.ProgressData{Message: "Implementing task"},
			},
		},
	}
	assert.True(t, router.DeliverResponse(streamMsg))

	select {
	case mirrored := <-tuiCh:
		require.NotNil(t, mirrored)
		assert.Equal(t, corrID, mirrored.CorrelationID)
		assert.Equal(t, "tui", mirrored.TargetAgentID)
		assert.Equal(t, "task-1:engineer", mirrored.RespondingAgentID)
		assert.Equal(t, "engineer", mirrored.Metadata["agent_type"])
		assert.Equal(t, "task-1", mirrored.Metadata["task_id"])
	case <-time.After(2 * time.Second):
		t.Fatal("mirrored stream was not published to the TUI response topic")
	}

	resp := &guide.RouteResponse{
		CorrelationID:     corrID,
		Success:           true,
		Data:              "task output",
		RespondingAgentID: "engineer",
	}
	respMsg := guide.NewResponseMessage("", resp)
	respMsg.CorrelationID = corrID
	assert.True(t, router.DeliverResponse(respMsg))

	select {
	case update := <-updateCh:
		assert.Equal(t, "succeeded", update.Status)
		assert.Equal(t, "task output", update.Output)
	case <-time.After(2 * time.Second):
		t.Fatal("pipeline update was not published")
	}
}

func TestTaskRouter_DeliverResponse_MirrorStreamAddsTaskMetadataWhenMissing(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	scope := testScope()
	router := testRouter(bus, scope)
	task := testTask()
	task.AgentType = "tester-pipeline"
	task.TargetAgentID = PipelineWorkerRoutingTarget(task.TaskID, task.AgentType)
	task.Context = map[string]any{
		"task_slug": "hello-cli",
		"task_name": "Hello CLI",
	}

	updateCh := make(chan *PipelineUpdate, 1)
	sub, err := bus.SubscribeAsync("pipeline.update.tester-pipeline", func(msg *guide.Message) error {
		if update, ok := msg.Payload.(*PipelineUpdate); ok {
			updateCh <- update
		}
		return nil
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	guideCh := make(chan string, 1)
	guideSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		if req, ok := msg.GetRouteRequest(); ok {
			guideCh <- req.CorrelationID
		}
		return nil
	})
	require.NoError(t, err)
	defer guideSub.Unsubscribe()

	tuiCh := make(chan *guide.StreamResponse, 1)
	tuiSub, err := bus.SubscribeAsync(guide.TopicResponses("tui", "tui"), func(msg *guide.Message) error {
		if stream, ok := msg.GetStreamResponse(); ok {
			tuiCh <- stream
		}
		return nil
	})
	require.NoError(t, err)
	defer tuiSub.Unsubscribe()

	err = router.Route(task)
	require.NoError(t, err)

	var corrID string
	select {
	case corrID = <-guideCh:
	case <-time.After(2 * time.Second):
		t.Fatal("no guide request intercepted")
	}

	streamMsg := &guide.Message{
		ID:            "stream-1",
		CorrelationID: corrID,
		Type:          guide.MessageTypeStream,
		Payload: &guide.StreamResponse{
			CorrelationID:     corrID,
			RespondingAgentID: PipelineWorkerRoutingTarget(task.TaskID, task.AgentType),
			Event: &guide.StreamEvent{
				Type: guide.StreamEventProgress,
				Data: &guide.ProgressData{},
			},
		},
	}
	assert.True(t, router.DeliverResponse(streamMsg))

	select {
	case mirrored := <-tuiCh:
		require.NotNil(t, mirrored)
		assert.Equal(t, "tester-pipeline", mirrored.Metadata["agent_type"])
		assert.Equal(t, "task-1", mirrored.Metadata["task_id"])
		assert.Equal(t, "task-1", mirrored.Metadata["pipeline_id"])
		assert.Equal(t, "hello-cli", mirrored.Metadata["task_slug"])
		assert.Equal(t, "Hello CLI", mirrored.Metadata["task_name"])
	case <-time.After(2 * time.Second):
		t.Fatal("mirrored stream was not published to the TUI response topic")
	}

	resp := &guide.RouteResponse{
		CorrelationID:     corrID,
		Success:           true,
		Data:              "task output",
		RespondingAgentID: PipelineWorkerRoutingTarget(task.TaskID, task.AgentType),
	}
	respMsg := guide.NewResponseMessage("", resp)
	respMsg.CorrelationID = corrID
	assert.True(t, router.DeliverResponse(respMsg))

	select {
	case update := <-updateCh:
		assert.Equal(t, "succeeded", update.Status)
	case <-time.After(2 * time.Second):
		t.Fatal("pipeline update was not published")
	}
}

func TestTaskRouter_DeliverResponse_MirrorsUntrackedProtocolStreamAndRecordsActivity(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	scope := testScope()
	activityCh := make(chan struct {
		dagID  string
		nodeID string
	}, 1)
	router := NewTaskRouter(TaskRouterConfig{
		Bus:       bus,
		Scope:     scope,
		AgentID:   "orchestrator",
		SessionID: "test-session",
		OnNodeActivity: func(dagID, nodeID string) {
			select {
			case activityCh <- struct {
				dagID  string
				nodeID string
			}{dagID: dagID, nodeID: nodeID}:
			default:
			}
		},
	})

	tuiCh := make(chan *guide.StreamResponse, 1)
	tuiSub, err := bus.SubscribeAsync(guide.TopicResponses("tui", "tui"), func(msg *guide.Message) error {
		if stream, ok := msg.GetStreamResponse(); ok {
			tuiCh <- stream
		}
		return nil
	})
	require.NoError(t, err)
	defer tuiSub.Unsubscribe()

	streamMsg := &guide.Message{
		ID:            "stream-protocol-1",
		CorrelationID: "pipe_protocol_1",
		Type:          guide.MessageTypeStream,
		Payload: &guide.StreamResponse{
			CorrelationID:     "pipe_protocol_1",
			RespondingAgentID: PipelineWorkerRoutingTarget("task-1", agentshared.PipelineAgentTester),
			TargetAgentID:     "orchestrator",
			Metadata: map[string]any{
				"pipeline_task": true,
				"dag_id":        "dag-1",
				"node_id":       "node-1",
				"task_id":       "task-1",
				"task_slug":     "hello-cli",
				"task_name":     "Hello CLI",
				"agent_type":    agentshared.PipelineAgentTester,
			},
			Event: &guide.StreamEvent{
				Type: guide.StreamEventProgress,
				Data: &guide.ProgressData{Message: "Running tests"},
			},
		},
		SourceAgentID: PipelineWorkerRoutingTarget("task-1", agentshared.PipelineAgentTester),
		TargetAgentID: "orchestrator",
		Timestamp:     time.Now(),
	}

	assert.True(t, router.DeliverResponse(streamMsg))

	select {
	case activity := <-activityCh:
		assert.Equal(t, "dag-1", activity.dagID)
		assert.Equal(t, "node-1", activity.nodeID)
	case <-time.After(2 * time.Second):
		t.Fatal("expected node activity refresh from protocol stream")
	}

	select {
	case mirrored := <-tuiCh:
		require.NotNil(t, mirrored)
		assert.Equal(t, "tui", mirrored.TargetAgentID)
		assert.Equal(t, agentshared.PipelineAgentTester, mirrored.Metadata["agent_type"])
		assert.Equal(t, "task-1", mirrored.Metadata["task_id"])
		assert.Equal(t, "hello-cli", mirrored.Metadata["task_slug"])
	case <-time.After(2 * time.Second):
		t.Fatal("expected protocol stream to be mirrored to the TUI response topic")
	}
}

func TestTaskRouter_PublishUserVisibleRoute_MirrorsGlobalFollowupToTUI(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	scope := testScope()
	router := testRouter(bus, scope)

	reqCh := make(chan *guide.RouteRequest, 1)
	reqSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		select {
		case reqCh <- req:
		default:
		}
		return nil
	})
	require.NoError(t, err)
	defer reqSub.Unsubscribe()

	tuiStreamCh := make(chan *guide.StreamResponse, 1)
	tuiRespCh := make(chan *guide.RouteResponse, 1)
	tuiSub, err := bus.SubscribeAsync(guide.TopicResponses("tui", "tui"), func(msg *guide.Message) error {
		if stream, ok := msg.GetStreamResponse(); ok && stream != nil {
			select {
			case tuiStreamCh <- stream:
			default:
			}
		}
		if resp, ok := msg.GetRouteResponse(); ok && resp != nil {
			select {
			case tuiRespCh <- resp:
			default:
			}
		}
		return nil
	})
	require.NoError(t, err)
	defer tuiSub.Unsubscribe()

	req := &guide.RouteRequest{
		CorrelationID:   "ot_followup_1",
		Input:           "Audit the merged task result.",
		TargetAgentID:   "inspector",
		ExplicitTarget:  true,
		SourceAgentID:   "orchestrator",
		SourceAgentName: "orchestrator",
		SessionID:       "test-session",
		Metadata: map[string]any{
			"agent_type":          "inspector",
			"task_id":             "task-1",
			"task_name":           "Hello CLI",
			"task_slug":           "hello-cli",
			"ot_handoff_followup": true,
		},
		Timestamp: time.Now(),
	}
	require.NoError(t, router.PublishUserVisibleRoute(req))

	select {
	case published := <-reqCh:
		require.NotNil(t, published)
		assert.Equal(t, req.CorrelationID, published.CorrelationID)
		assert.Equal(t, "inspector", published.TargetAgentID)
	case <-time.After(2 * time.Second):
		t.Fatal("expected user-visible guide request to be published")
	}

	streamMsg := &guide.Message{
		ID:            "stream-visible-1",
		CorrelationID: req.CorrelationID,
		Type:          guide.MessageTypeStream,
		Payload: &guide.StreamResponse{
			CorrelationID:     req.CorrelationID,
			RespondingAgentID: "inspector",
			Event: &guide.StreamEvent{
				Type: guide.StreamEventProgress,
				Data: &guide.ProgressData{Message: "Auditing merged result"},
			},
		},
		SourceAgentID: "inspector",
		TargetAgentID: "orchestrator",
		Timestamp:     time.Now(),
	}
	require.True(t, router.DeliverResponse(streamMsg))

	select {
	case mirrored := <-tuiStreamCh:
		require.NotNil(t, mirrored)
		assert.Equal(t, "tui", mirrored.TargetAgentID)
		assert.Equal(t, "inspector", mirrored.RespondingAgentID)
		assert.Equal(t, "inspector", mirrored.Metadata["agent_type"])
		assert.Equal(t, "task-1", mirrored.Metadata["task_id"])
		assert.Equal(t, "Hello CLI", mirrored.Metadata["task_name"])
		assert.Equal(t, "hello-cli", mirrored.Metadata["task_slug"])
	case <-time.After(2 * time.Second):
		t.Fatal("expected mirrored visible stream on the TUI response topic")
	}

	respMsg := guide.NewResponseMessage("", &guide.RouteResponse{
		CorrelationID:       req.CorrelationID,
		Success:             true,
		Data:                "Global inspector audit complete.",
		RespondingAgentID:   "inspector",
		RespondingAgentName: "Inspector",
	})
	respMsg.CorrelationID = req.CorrelationID
	require.True(t, router.DeliverResponse(respMsg))

	select {
	case mirrored := <-tuiRespCh:
		require.NotNil(t, mirrored)
		assert.Equal(t, req.CorrelationID, mirrored.CorrelationID)
		assert.True(t, mirrored.Success)
		assert.Equal(t, "Global inspector audit complete.", mirrored.Data)
		assert.Equal(t, "inspector", mirrored.RespondingAgentID)
	case <-time.After(2 * time.Second):
		t.Fatal("expected mirrored visible terminal response on the TUI response topic")
	}
}

func TestTaskRouter_PublishUserVisibleRoute_MirrorsNestedVisibleChildStreamsToTUI(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	scope := testScope()
	router := testRouter(bus, scope)

	tuiStreamCh := make(chan *guide.StreamResponse, 4)
	tuiSub, err := bus.SubscribeAsync(guide.TopicResponses("tui", "tui"), func(msg *guide.Message) error {
		stream, ok := msg.GetStreamResponse()
		if !ok || stream == nil {
			return nil
		}
		select {
		case tuiStreamCh <- stream:
		default:
		}
		return nil
	})
	require.NoError(t, err)
	defer tuiSub.Unsubscribe()

	req := &guide.RouteRequest{
		CorrelationID:   "ot_followup_nested_1",
		Input:           "Audit the merged task result.",
		TargetAgentID:   "inspector",
		ExplicitTarget:  true,
		SourceAgentID:   "orchestrator",
		SourceAgentName: "orchestrator",
		SessionID:       "test-session",
		Metadata: map[string]any{
			"agent_type":          "inspector",
			"task_id":             "task-1",
			"task_name":           "Hello CLI",
			"task_slug":           "hello-cli",
			"ot_handoff_followup": true,
		},
		Timestamp: time.Now(),
	}
	require.NoError(t, router.PublishUserVisibleRoute(req))

	childMsg := &guide.Message{
		ID:            "stream-visible-child-1",
		CorrelationID: "corr-child-academic",
		Type:          guide.MessageTypeStream,
		Payload: &guide.StreamResponse{
			CorrelationID:     "corr-child-academic",
			RespondingAgentID: "academic",
			Metadata: map[string]any{
				"chat_nested_branch":         true,
				"chat_parent_correlation_id": req.CorrelationID,
				"chat_parent_tool_call_key":  "consult-academic-1",
				"chat_inter_agent_kind":      agentshared.InterAgentToolEventKindConsult,
				"agent_type":                 "academic",
			},
			Event: &guide.StreamEvent{
				Type: guide.StreamEventProgress,
				Data: &guide.ProgressData{Message: "Gathering stronger alternatives"},
			},
		},
		SourceAgentID: "academic",
		TargetAgentID: "orchestrator",
		Timestamp:     time.Now(),
	}
	require.True(t, router.DeliverResponse(childMsg))

	select {
	case mirrored := <-tuiStreamCh:
		require.NotNil(t, mirrored)
		assert.Equal(t, "tui", mirrored.TargetAgentID)
		assert.Equal(t, "academic", mirrored.RespondingAgentID)
		assert.Equal(t, req.Metadata["task_id"], mirrored.Metadata["task_id"])
		assert.Equal(t, req.Metadata["task_slug"], mirrored.Metadata["task_slug"])
		assert.Equal(t, req.CorrelationID, mirrored.Metadata["chat_parent_correlation_id"])
		assert.Equal(t, "consult-academic-1", mirrored.Metadata["chat_parent_tool_call_key"])
		assert.Equal(t, agentshared.InterAgentToolEventKindConsult, mirrored.Metadata["chat_inter_agent_kind"])
	case <-time.After(2 * time.Second):
		t.Fatal("expected nested child stream to be mirrored to the TUI response topic")
	}

	grandchildMsg := &guide.Message{
		ID:            "stream-visible-grandchild-1",
		CorrelationID: "corr-grandchild-librarian",
		Type:          guide.MessageTypeStream,
		Payload: &guide.StreamResponse{
			CorrelationID:     "corr-grandchild-librarian",
			RespondingAgentID: "librarian",
			Metadata: map[string]any{
				"chat_nested_branch":         true,
				"chat_parent_correlation_id": "corr-child-academic",
				"chat_parent_tool_call_key":  "consult-librarian-1",
				"chat_inter_agent_kind":      agentshared.InterAgentToolEventKindConsult,
				"agent_type":                 "librarian",
			},
			Event: &guide.StreamEvent{
				Type: guide.StreamEventProgress,
				Data: &guide.ProgressData{Message: "Checking prior repository patterns"},
			},
		},
		SourceAgentID: "librarian",
		TargetAgentID: "orchestrator",
		Timestamp:     time.Now(),
	}
	require.True(t, router.DeliverResponse(grandchildMsg))

	select {
	case mirrored := <-tuiStreamCh:
		require.NotNil(t, mirrored)
		assert.Equal(t, "tui", mirrored.TargetAgentID)
		assert.Equal(t, "librarian", mirrored.RespondingAgentID)
		assert.Equal(t, req.Metadata["task_id"], mirrored.Metadata["task_id"])
		assert.Equal(t, "corr-child-academic", mirrored.Metadata["chat_parent_correlation_id"])
		assert.Equal(t, "consult-librarian-1", mirrored.Metadata["chat_parent_tool_call_key"])
		assert.Equal(t, agentshared.InterAgentToolEventKindConsult, mirrored.Metadata["chat_inter_agent_kind"])
	case <-time.After(2 * time.Second):
		t.Fatal("expected nested grandchild stream to be mirrored to the TUI response topic")
	}
}

func TestTaskRouter_PublishUserVisibleRoute_QueuesOTFollowupsPerReviewer(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	scope := testScope()
	router := testRouter(bus, scope)

	reqCh := make(chan *guide.RouteRequest, 4)
	reqSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil || req.Metadata["ot_handoff_followup"] != true {
			return nil
		}
		reqCh <- req
		return nil
	})
	require.NoError(t, err)
	defer reqSub.Unsubscribe()

	req1 := &guide.RouteRequest{
		CorrelationID:   "ot_followup_a",
		Input:           "Audit pipeline A.",
		TargetAgentID:   "inspector",
		ExplicitTarget:  true,
		SourceAgentID:   "orchestrator",
		SourceAgentName: "orchestrator",
		SessionID:       "test-session",
		Timestamp:       time.Date(2026, 3, 25, 15, 0, 0, 0, time.UTC),
		Metadata: map[string]any{
			"agent_type":          "inspector",
			"task_id":             "task-a",
			"ot_handoff_followup": true,
		},
	}
	req2 := &guide.RouteRequest{
		CorrelationID:   "ot_followup_b",
		Input:           "Audit pipeline B.",
		TargetAgentID:   "inspector",
		ExplicitTarget:  true,
		SourceAgentID:   "orchestrator",
		SourceAgentName: "orchestrator",
		SessionID:       "test-session",
		Timestamp:       time.Date(2026, 3, 25, 15, 0, 1, 0, time.UTC),
		Metadata: map[string]any{
			"agent_type":          "inspector",
			"task_id":             "task-b",
			"ot_handoff_followup": true,
		},
	}

	require.NoError(t, router.PublishUserVisibleRoute(req1))
	require.NoError(t, router.PublishUserVisibleRoute(req2))

	select {
	case published := <-reqCh:
		require.NotNil(t, published)
		assert.Equal(t, req1.CorrelationID, published.CorrelationID)
	case <-time.After(2 * time.Second):
		t.Fatal("expected first OT follow-up request to publish immediately")
	}

	select {
	case published := <-reqCh:
		t.Fatalf("queued OT follow-up published too early: %s", published.CorrelationID)
	case <-time.After(150 * time.Millisecond):
	}

	resp1 := guide.NewResponseMessage("", &guide.RouteResponse{
		CorrelationID:       req1.CorrelationID,
		Success:             true,
		RespondingAgentID:   "inspector",
		RespondingAgentName: "Inspector",
		Data:                "audit A complete",
	})
	resp1.CorrelationID = req1.CorrelationID
	require.True(t, router.DeliverResponse(resp1))

	select {
	case published := <-reqCh:
		require.NotNil(t, published)
		assert.Equal(t, req2.CorrelationID, published.CorrelationID)
	case <-time.After(2 * time.Second):
		t.Fatal("expected second OT follow-up request after first terminal response")
	}
}

func TestTaskRouter_PublishUserVisibleRoute_OTFollowupsQueuePerTarget(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	scope := testScope()
	router := testRouter(bus, scope)

	reqCh := make(chan *guide.RouteRequest, 4)
	reqSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil || req.Metadata["ot_handoff_followup"] != true {
			return nil
		}
		reqCh <- req
		return nil
	})
	require.NoError(t, err)
	defer reqSub.Unsubscribe()

	reqInspector := &guide.RouteRequest{
		CorrelationID:   "ot_followup_inspector",
		Input:           "Audit inspector queue.",
		TargetAgentID:   "inspector",
		ExplicitTarget:  true,
		SourceAgentID:   "orchestrator",
		SourceAgentName: "orchestrator",
		SessionID:       "test-session",
		Timestamp:       time.Date(2026, 3, 25, 15, 1, 0, 0, time.UTC),
		Metadata: map[string]any{
			"agent_type":          "inspector",
			"task_id":             "task-i",
			"ot_handoff_followup": true,
		},
	}
	reqTester := &guide.RouteRequest{
		CorrelationID:   "ot_followup_tester",
		Input:           "Audit tester queue.",
		TargetAgentID:   "tester",
		ExplicitTarget:  true,
		SourceAgentID:   "orchestrator",
		SourceAgentName: "orchestrator",
		SessionID:       "test-session",
		Timestamp:       time.Date(2026, 3, 25, 15, 1, 1, 0, time.UTC),
		Metadata: map[string]any{
			"agent_type":          "tester",
			"task_id":             "task-t",
			"ot_handoff_followup": true,
		},
	}

	require.NoError(t, router.PublishUserVisibleRoute(reqInspector))
	require.NoError(t, router.PublishUserVisibleRoute(reqTester))

	got := map[string]bool{}
	timeout := time.After(2 * time.Second)
	for len(got) < 2 {
		select {
		case published := <-reqCh:
			got[published.CorrelationID] = true
		case <-timeout:
			t.Fatalf("timed out waiting for both reviewer queues to publish, got=%v", got)
		}
	}

	assert.True(t, got[reqInspector.CorrelationID])
	assert.True(t, got[reqTester.CorrelationID])
}

func TestTaskRouter_DeliverResponse_ConsumesUntrackedProtocolTerminal(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	scope := testScope()
	router := testRouter(bus, scope)

	resp := &guide.RouteResponse{
		CorrelationID:     "pipe_protocol_2",
		Success:           true,
		RespondingAgentID: PipelineWorkerRoutingTarget("task-1", agentshared.PipelineAgentTester),
		Data: &agentshared.PipelineTurnResponse{
			Action: &agentshared.PipelineTurnAction{
				Type:         agentshared.PipelineProtocolActionValidate,
				AgentType:    agentshared.PipelineAgentTester,
				ChallengeID:  "challenge-1",
				TargetAgents: []string{agentshared.PipelineAgentInspector},
			},
		},
	}
	msg := guide.NewResponseMessage("", resp)
	msg.CorrelationID = resp.CorrelationID

	assert.True(t, router.DeliverResponse(msg))
}

func TestTaskRouter_DeliverResponse_ReconcilesUntrackedProtocolTerminalOT(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	scope := testScope()
	router := testRouter(bus, scope)

	updateCh := make(chan *PipelineUpdate, 1)
	sub, err := bus.SubscribeAsync("pipeline.update."+agentshared.PipelineAgentInspector, func(msg *guide.Message) error {
		if update, ok := msg.Payload.(*PipelineUpdate); ok {
			updateCh <- update
		}
		return nil
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	resp := &guide.RouteResponse{
		CorrelationID:     "pipe_protocol_ot",
		Success:           true,
		RespondingAgentID: PipelineWorkerRoutingTarget("task-1", agentshared.PipelineAgentInspector),
		Data: &agentshared.PipelineTurnResponse{
			Action: &agentshared.PipelineTurnAction{
				Type:         agentshared.PipelineProtocolActionOT,
				AgentType:    agentshared.PipelineAgentInspector,
				Summary:      "Pipeline accepted and ready for OT merge.",
				EvidenceRefs: []string{"artifact:verification"},
			},
			Result: map[string]any{"snapshot": "final"},
		},
	}
	msg := guide.NewResponseMessage("", resp)
	msg.CorrelationID = resp.CorrelationID
	msg.Metadata = map[string]any{
		"pipeline_task": true,
		"dag_id":        "dag-1",
		"node_id":       "task-1",
		"task_id":       "task-1",
		"task_slug":     "task-one",
		"task_name":     "Task One",
		"agent_type":    agentshared.PipelineAgentInspector,
	}

	assert.True(t, router.DeliverResponse(msg))

	select {
	case update := <-updateCh:
		require.NotNil(t, update)
		assert.Equal(t, "dag-1", update.DAGID)
		assert.Equal(t, "task-1", update.TaskID)
		assert.Equal(t, agentshared.PipelineAgentInspector, update.AgentType)
		assert.Equal(t, "succeeded", update.Status)
		assert.Equal(t, string(StageInspect), update.Stage)
		assert.Equal(t, "Pipeline accepted and ready for OT merge.", update.Message)
		output, ok := update.Output.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "Pipeline accepted and ready for OT merge.", output["summary"])
	case <-time.After(2 * time.Second):
		t.Fatal("expected recovered protocol terminal update")
	}
}

func TestTaskRouter_DeliverResponse_RejectsProcessedOnlyProtocolTurn(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	scope := testScope()
	router := testRouter(bus, scope)

	reqCh := make(chan *guide.RouteRequest, 4)
	reqSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil || req.Metadata["pipeline_task"] != true {
			return nil
		}
		reqCh <- req
		return nil
	})
	require.NoError(t, err)
	defer reqSub.Unsubscribe()

	updateCh := make(chan *PipelineUpdate, 4)
	updateSub, err := bus.SubscribeAsync("pipeline.update."+agentshared.PipelineAgentInspector, func(msg *guide.Message) error {
		update, ok := msg.Payload.(*PipelineUpdate)
		if !ok || update == nil {
			return nil
		}
		updateCh <- update
		return nil
	})
	require.NoError(t, err)
	defer updateSub.Unsubscribe()

	task := testTask()
	task.TargetAgentID = pipelineWorkerTargetAgentID(task.TaskID, task.AgentType)
	task.Context = map[string]any{
		"session_dir": t.TempDir(),
		"task_slug":   "hello-cli",
		"task_name":   "Hello CLI",
	}
	require.NoError(t, router.RouteWithLifecycle(task, nil))

	select {
	case <-reqCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial protocol route")
	}
	select {
	case <-updateCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial running update")
	}

	resp := &guide.RouteResponse{
		CorrelationID:     "pipe_protocol_processed",
		Success:           true,
		RespondingAgentID: PipelineWorkerRoutingTarget(task.TaskID, agentshared.PipelineAgentInspector),
		Data: &agentshared.PipelineTurnResponse{
			Processed: []agentshared.PipelineValidationProcessing{
				{
					ChallengeID: "challenge-1",
					AgentType:   agentshared.PipelineAgentInspector,
					Decision:    agentshared.PipelineValidationDecisionAccept,
					Summary:     "Accepted tester validation.",
				},
			},
		},
	}
	msg := guide.NewResponseMessage("", resp)
	msg.CorrelationID = resp.CorrelationID
	msg.Metadata = map[string]any{
		"pipeline_task": true,
		"session_id":    task.SessionID,
		"session_dir":   task.Context["session_dir"],
		"dag_id":        task.DAGID,
		"node_id":       task.NodeID,
		"task_id":       task.TaskID,
		"task_slug":     task.Context["task_slug"],
		"task_name":     task.Context["task_name"],
		"agent_type":    agentshared.PipelineAgentInspector,
	}

	require.True(t, router.DeliverResponse(msg))

	select {
	case update := <-updateCh:
		require.NotNil(t, update)
		assert.Equal(t, "failed", update.Status)
		assert.Equal(t, agentshared.PipelineAgentInspector, update.AgentType)
		assert.Contains(t, update.Error, "processed validation but did not record the next pipeline step")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for processed-only protocol failure update")
	}

	select {
	case req := <-reqCh:
		t.Fatalf("unexpected protocol follow-up route published for processed-only protocol turn: %+v", req)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestTaskRouter_PublishProtocolRoute_SuppressesDuplicateActiveTarget(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	router := testRouter(bus, testScope())
	reqCh := make(chan *guide.RouteRequest, 2)
	reqSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil || req.Metadata["pipeline_task"] != true {
			return nil
		}
		reqCh <- req
		return nil
	})
	require.NoError(t, err)
	defer reqSub.Unsubscribe()

	target := PipelineWorkerRoutingTarget("task-1", agentshared.PipelineAgentTester)
	req1 := &guide.RouteRequest{
		CorrelationID:  "pipe_active_tester",
		TargetAgentID:  target,
		ExplicitTarget: true,
		SourceAgentID:  "orchestrator",
		SessionID:      "test-session",
		Timestamp:      time.Now().UTC(),
		Metadata: map[string]any{
			"pipeline_task": true,
			"task_id":       "task-1",
			"agent_type":    agentshared.PipelineAgentTester,
		},
	}
	req2 := &guide.RouteRequest{
		CorrelationID:  "pipe_duplicate_tester",
		TargetAgentID:  target,
		ExplicitTarget: true,
		SourceAgentID:  "orchestrator",
		SessionID:      "test-session",
		Timestamp:      time.Now().Add(1 * time.Second).UTC(),
		Metadata: map[string]any{
			"pipeline_task": true,
			"task_id":       "task-1",
			"agent_type":    agentshared.PipelineAgentTester,
		},
	}

	published, err := router.publishProtocolRoute(req1)
	require.NoError(t, err)
	require.True(t, published)
	select {
	case got := <-reqCh:
		require.NotNil(t, got)
		assert.Equal(t, req1.CorrelationID, got.CorrelationID)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first protocol route")
	}

	published, err = router.publishProtocolRoute(req2)
	require.NoError(t, err)
	assert.False(t, published)

	select {
	case got := <-reqCh:
		t.Fatalf("unexpected duplicate protocol route published: %+v", got)
	case <-time.After(200 * time.Millisecond):
	}

	assert.Equal(t, req1.CorrelationID, router.protocolRouteActive[target])
	_, ok := router.protocolRouteCorr[req2.CorrelationID]
	assert.False(t, ok)
}

func TestTaskRouter_DeliverResponse_ProcessedWithoutActionFailsProtocolTurn(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	scope := testScope()
	router := testRouter(bus, scope)

	reqCh := make(chan *guide.RouteRequest, 4)
	reqSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil || req.Metadata["pipeline_task"] != true {
			return nil
		}
		reqCh <- req
		return nil
	})
	require.NoError(t, err)
	defer reqSub.Unsubscribe()

	updateCh := make(chan *PipelineUpdate, 4)
	updateSub, err := bus.SubscribeAsync("pipeline.update."+agentshared.PipelineAgentInspector, func(msg *guide.Message) error {
		update, ok := msg.Payload.(*PipelineUpdate)
		if !ok || update == nil {
			return nil
		}
		updateCh <- update
		return nil
	})
	require.NoError(t, err)
	defer updateSub.Unsubscribe()

	task := testTask()
	task.TargetAgentID = pipelineWorkerTargetAgentID(task.TaskID, task.AgentType)
	task.Context = map[string]any{
		"session_dir": t.TempDir(),
		"task_slug":   "hello-cli",
		"task_name":   "Hello CLI",
	}
	require.NoError(t, router.RouteWithLifecycle(task, nil))

	select {
	case <-reqCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial protocol route")
	}
	select {
	case <-updateCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial running update")
	}

	resp := &guide.RouteResponse{
		CorrelationID:     "pipe_protocol_processed",
		Success:           true,
		RespondingAgentID: PipelineWorkerRoutingTarget(task.TaskID, agentshared.PipelineAgentInspector),
		Data: &agentshared.PipelineTurnResponse{
			Processed: []agentshared.PipelineValidationProcessing{
				{
					ChallengeID: "challenge-1",
					AgentType:   agentshared.PipelineAgentInspector,
					Decision:    agentshared.PipelineValidationDecisionClarify,
					Summary:     "Tester needs to repair the returned file.",
				},
			},
		},
	}
	msg := guide.NewResponseMessage("", resp)
	msg.CorrelationID = resp.CorrelationID
	msg.Metadata = map[string]any{
		"pipeline_task": true,
		"session_id":    task.SessionID,
		"session_dir":   task.Context["session_dir"],
		"dag_id":        task.DAGID,
		"node_id":       task.NodeID,
		"task_id":       task.TaskID,
		"task_slug":     task.Context["task_slug"],
		"task_name":     task.Context["task_name"],
		"agent_type":    agentshared.PipelineAgentInspector,
	}

	require.True(t, router.DeliverResponse(msg))

	select {
	case update := <-updateCh:
		require.NotNil(t, update)
		assert.Equal(t, "failed", update.Status)
		assert.Equal(t, agentshared.PipelineAgentInspector, update.AgentType)
		assert.Contains(t, update.Error, "processed validation but did not record the next pipeline step")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for malformed processed-turn failure update")
	}

	select {
	case req := <-reqCh:
		t.Fatalf("unexpected protocol follow-up route published for malformed processed turn: %+v", req)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestTaskRouterPauseActiveRoutesPublishesPaceActionsForPendingAndProtocolRoutes(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	router := testRouter(bus, testScope())

	pendingTask := testTask()
	pendingTask.TargetAgentID = pipelineWorkerTargetAgentID(pendingTask.TaskID, pendingTask.AgentType)
	router.registerPending("pipe_pending_engineer", pendingTask)

	protocolReq := &guide.RouteRequest{
		CorrelationID:  "pipe_active_tester",
		TargetAgentID:  PipelineWorkerRoutingTarget("task-1", agentshared.PipelineAgentTester),
		ExplicitTarget: true,
		SourceAgentID:  "orchestrator",
		SessionID:      "test-session",
		Timestamp:      time.Now().UTC(),
		Metadata: map[string]any{
			"pipeline_task": true,
			"dag_id":        "dag-1",
			"node_id":       "node-1",
			"task_id":       "task-1",
			"agent_type":    agentshared.PipelineAgentTester,
		},
	}
	published, err := router.publishProtocolRoute(protocolReq)
	require.NoError(t, err)
	require.True(t, published)

	actionCh := make(chan *guide.ActionRequest, 4)
	sub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetActionRequest()
		if !ok || req == nil {
			return nil
		}
		actionCh <- req
		return nil
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	count := router.PauseActiveRoutes("test-session", "", "validation hold active")
	require.Equal(t, 2, count)

	received := make(map[string]*guide.ActionRequest, 2)
	deadline := time.After(2 * time.Second)
	for len(received) < 2 {
		select {
		case req := <-actionCh:
			received[req.CorrelationID] = req
		case <-deadline:
			t.Fatalf("timed out waiting for pace actions; received=%d", len(received))
		}
	}

	engineerReq := received["pipe_pending_engineer"]
	require.NotNil(t, engineerReq)
	assert.Equal(t, "pace", engineerReq.Action)
	assert.Equal(t, "paused", engineerReq.Data.(map[string]any)["pace"])
	assert.Equal(t, pendingTask.TargetAgentID, engineerReq.TargetAgentID)

	testerReq := received["pipe_active_tester"]
	require.NotNil(t, testerReq)
	assert.Equal(t, "pace", testerReq.Action)
	assert.Equal(t, "paused", testerReq.Data.(map[string]any)["pace"])
	assert.Equal(t, protocolReq.TargetAgentID, testerReq.TargetAgentID)
}

func TestTaskRouter_DeliverResponse_AcknowledgesTopLevelHandoffWithoutRepublishing(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	scope := testScope()
	router := testRouter(bus, scope)

	reqCh := make(chan *guide.RouteRequest, 4)
	reqSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil || req.Metadata["pipeline_task"] != true {
			return nil
		}
		reqCh <- req
		return nil
	})
	require.NoError(t, err)
	defer reqSub.Unsubscribe()

	updateCh := make(chan *PipelineUpdate, 4)
	updateSub, err := bus.SubscribeAsync("pipeline.update."+agentshared.PipelineAgentInspector, func(msg *guide.Message) error {
		update, ok := msg.Payload.(*PipelineUpdate)
		if !ok || update == nil {
			return nil
		}
		updateCh <- update
		return nil
	})
	require.NoError(t, err)
	defer updateSub.Unsubscribe()

	task := testTask()
	task.TargetAgentID = pipelineWorkerTargetAgentID(task.TaskID, task.AgentType)
	task.Context = map[string]any{
		"session_dir": t.TempDir(),
		"task_slug":   "hello-cli",
		"task_name":   "Hello CLI",
	}
	require.NoError(t, router.RouteWithLifecycle(task, nil))

	select {
	case <-reqCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial protocol route")
	}
	select {
	case <-updateCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial running update")
	}

	resp := &guide.RouteResponse{
		CorrelationID:     "pipe_protocol_handoff",
		Success:           true,
		RespondingAgentID: PipelineWorkerRoutingTarget(task.TaskID, agentshared.PipelineAgentInspector),
		Data: &agentshared.PipelineTurnResponse{
			Action: &agentshared.PipelineTurnAction{
				Type:         agentshared.PipelineProtocolActionHandoff,
				AgentType:    agentshared.PipelineAgentInspector,
				TargetAgents: []string{agentshared.PipelineAgentTester},
				Mode:         agentshared.PipelineTurnModeSingle,
				Reason:       "testing should verify the criteria next",
				Request:      "Author the failing tests for the agreed contract.",
			},
		},
	}
	msg := guide.NewResponseMessage("", resp)
	msg.CorrelationID = resp.CorrelationID
	msg.Metadata = map[string]any{
		"pipeline_task": true,
		"session_id":    task.SessionID,
		"session_dir":   task.Context["session_dir"],
		"dag_id":        task.DAGID,
		"node_id":       task.NodeID,
		"task_id":       task.TaskID,
		"task_slug":     task.Context["task_slug"],
		"task_name":     task.Context["task_name"],
		"agent_type":    agentshared.PipelineAgentInspector,
	}

	require.True(t, router.DeliverResponse(msg))

	select {
	case req := <-reqCh:
		t.Fatalf("unexpected orchestrator-published follow-up route for explicit handoff action: %+v", req)
	case <-time.After(200 * time.Millisecond):
	}

	select {
	case update := <-updateCh:
		t.Fatalf("unexpected orchestrator-published running update for explicit handoff action: %+v", update)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestTaskRouter_DeliverResponse_EmptyProtocolTurnFails(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	scope := testScope()
	router := testRouter(bus, scope)

	reqCh := make(chan *guide.RouteRequest, 4)
	reqSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil || req.Metadata["pipeline_task"] != true {
			return nil
		}
		reqCh <- req
		return nil
	})
	require.NoError(t, err)
	defer reqSub.Unsubscribe()

	updateCh := make(chan *PipelineUpdate, 4)
	updateSub, err := bus.SubscribeAsync("pipeline.update."+agentshared.PipelineAgentInspector, func(msg *guide.Message) error {
		update, ok := msg.Payload.(*PipelineUpdate)
		if !ok || update == nil {
			return nil
		}
		updateCh <- update
		return nil
	})
	require.NoError(t, err)
	defer updateSub.Unsubscribe()

	task := testTask()
	task.TargetAgentID = pipelineWorkerTargetAgentID(task.TaskID, task.AgentType)
	task.Context = map[string]any{
		"session_dir": t.TempDir(),
		"task_slug":   "hello-cli",
		"task_name":   "Hello CLI",
	}
	require.NoError(t, router.RouteWithLifecycle(task, nil))

	select {
	case <-reqCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial protocol route")
	}
	select {
	case <-updateCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial running update")
	}

	resp := &guide.RouteResponse{
		CorrelationID:     "pipe_protocol_empty",
		Success:           true,
		RespondingAgentID: PipelineWorkerRoutingTarget(task.TaskID, agentshared.PipelineAgentInspector),
		Data:              &agentshared.PipelineTurnResponse{},
	}
	msg := guide.NewResponseMessage("", resp)
	msg.CorrelationID = resp.CorrelationID
	msg.Metadata = map[string]any{
		"pipeline_task": true,
		"session_id":    task.SessionID,
		"session_dir":   task.Context["session_dir"],
		"dag_id":        task.DAGID,
		"node_id":       task.NodeID,
		"task_id":       task.TaskID,
		"task_slug":     task.Context["task_slug"],
		"task_name":     task.Context["task_name"],
		"agent_type":    agentshared.PipelineAgentInspector,
	}

	require.True(t, router.DeliverResponse(msg))

	select {
	case update := <-updateCh:
		require.NotNil(t, update)
		assert.Equal(t, "failed", update.Status)
		assert.Equal(t, agentshared.PipelineAgentInspector, update.AgentType)
		assert.Contains(t, update.Error, "ended without recording the next pipeline step")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for malformed empty-turn failure update")
	}

	select {
	case req := <-reqCh:
		t.Fatalf("unexpected protocol follow-up route published for malformed empty turn: %+v", req)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestTaskRouter_ErrorMessagePublishesFailure(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	scope := testScope()
	router := testRouter(bus, scope)
	task := testTask()

	updateCh := make(chan *PipelineUpdate, 1)
	sub, err := bus.SubscribeAsync("pipeline.update.engineer", func(msg *guide.Message) error {
		if update, ok := msg.Payload.(*PipelineUpdate); ok {
			updateCh <- update
		}
		return nil
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	guideCh := make(chan string, 1)
	guideSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		if req, ok := msg.GetRouteRequest(); ok {
			guideCh <- req.CorrelationID
		}
		return nil
	})
	require.NoError(t, err)
	defer guideSub.Unsubscribe()

	err = router.Route(task)
	require.NoError(t, err)

	var corrID string
	select {
	case corrID = <-guideCh:
	case <-time.After(2 * time.Second):
		t.Fatal("no guide request intercepted")
	}

	errMsg := guide.NewErrorMessage("", corrID, "guide", "agent engineer not available")
	errMsg.CorrelationID = corrID
	assert.True(t, router.DeliverResponse(errMsg))

	select {
	case update := <-updateCh:
		assert.Equal(t, "failed", update.Status)
		assert.Contains(t, update.Error, "agent engineer not available")
	case <-time.After(2 * time.Second):
		t.Fatal("failure update was not published")
	}
}

func TestTaskRouter_ScopeContextCancellation(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	scope := testScope()
	router := testRouter(bus, scope)
	task := testTask()

	updateCh := make(chan *PipelineUpdate, 1)
	sub, err := bus.SubscribeAsync("pipeline.update.engineer", func(msg *guide.Message) error {
		if update, ok := msg.Payload.(*PipelineUpdate); ok {
			updateCh <- update
		}
		return nil
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	// Drain the guide request (don't respond)
	guideSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		return nil
	})
	require.NoError(t, err)
	defer guideSub.Unsubscribe()

	err = router.Route(task)
	require.NoError(t, err)

	// Shut down the scope — this cancels all tracked goroutines
	scope.Shutdown(100*time.Millisecond, 200*time.Millisecond)

	select {
	case update := <-updateCh:
		assert.Equal(t, "failed", update.Status)
		assert.Contains(t, update.Error, "context")
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation failure was not published")
	}
}

func TestTaskRouter_FailedRouteResponseWithoutErrorUsesFallback(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	scope := testScope()
	router := testRouter(bus, scope)
	task := testTask()

	updateCh := make(chan *PipelineUpdate, 1)
	sub, err := bus.SubscribeAsync("pipeline.update.engineer", func(msg *guide.Message) error {
		if update, ok := msg.Payload.(*PipelineUpdate); ok {
			updateCh <- update
		}
		return nil
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	guideCh := make(chan string, 1)
	guideSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		if req, ok := msg.GetRouteRequest(); ok {
			guideCh <- req.CorrelationID
		}
		return nil
	})
	require.NoError(t, err)
	defer guideSub.Unsubscribe()

	require.NoError(t, router.Route(task))

	var corrID string
	select {
	case corrID = <-guideCh:
	case <-time.After(2 * time.Second):
		t.Fatal("no guide request intercepted")
	}

	resp := &guide.RouteResponse{
		CorrelationID:       corrID,
		Success:             false,
		RespondingAgentID:   "engineer-worker",
		RespondingAgentName: "Engineer",
	}
	respMsg := guide.NewResponseMessage("", resp)
	respMsg.CorrelationID = corrID
	require.True(t, router.DeliverResponse(respMsg))

	select {
	case update := <-updateCh:
		require.Equal(t, "failed", update.Status)
		require.Equal(t, "Engineer returned an unsuccessful route response for node node-1 without error details", update.Error)
	case <-time.After(2 * time.Second):
		t.Fatal("failure update was not published")
	}
}

func TestTaskRouter_ErrorMessageWithoutPayloadUsesSourceFallback(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	scope := testScope()
	router := testRouter(bus, scope)
	task := testTask()

	updateCh := make(chan *PipelineUpdate, 1)
	sub, err := bus.SubscribeAsync("pipeline.update.engineer", func(msg *guide.Message) error {
		if update, ok := msg.Payload.(*PipelineUpdate); ok {
			updateCh <- update
		}
		return nil
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	guideCh := make(chan string, 1)
	guideSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		if req, ok := msg.GetRouteRequest(); ok {
			guideCh <- req.CorrelationID
		}
		return nil
	})
	require.NoError(t, err)
	defer guideSub.Unsubscribe()

	require.NoError(t, router.Route(task))

	var corrID string
	select {
	case corrID = <-guideCh:
	case <-time.After(2 * time.Second):
		t.Fatal("no guide request intercepted")
	}

	errMsg := guide.NewErrorMessage("", corrID, "engineer-worker", "")
	errMsg.CorrelationID = corrID
	require.True(t, router.DeliverResponse(errMsg))

	select {
	case update := <-updateCh:
		require.Equal(t, "failed", update.Status)
		require.Equal(t, "route error from engineer-worker for node node-1", update.Error)
	case <-time.After(2 * time.Second):
		t.Fatal("failure update was not published")
	}
}
