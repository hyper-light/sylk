package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
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
