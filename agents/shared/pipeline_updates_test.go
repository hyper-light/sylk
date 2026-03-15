package shared

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/stretchr/testify/require"
)

func TestPublishPipelineTaskTerminalErrorUpdate_PublishesCancelledForInterrupt(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	updateCh := make(chan map[string]any, 1)
	sub, err := bus.SubscribeAsync("pipeline.update.engineer", func(msg *guide.Message) error {
		payload, _ := msg.Payload.(map[string]any)
		updateCh <- payload
		return nil
	})
	require.NoError(t, err)
	defer func() { _ = sub.Unsubscribe() }()

	task := &PipelineTaskInput{
		DAGID:     "dag-1",
		NodeID:    "node-1",
		TaskID:    "task-1",
		AgentType: "engineer",
		Context: map[string]any{
			"pipeline_stage": "execute",
		},
	}

	PublishPipelineTaskTerminalErrorUpdate(bus, "engineer-1", task, context.Canceled, 2)

	select {
	case update := <-updateCh:
		require.Equal(t, "cancelled", update["status"])
		require.Equal(t, "task-1", update["task_id"])
		require.Equal(t, "engineer", update["agent_type"])
		require.Equal(t, "request interrupted", update["message"])
		require.Equal(t, float64(1), update["progress"])
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for cancelled pipeline update")
	}
}

func TestPublishPipelineTaskTerminalErrorUpdate_PublishesFailureForNonInterrupt(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	updateCh := make(chan map[string]any, 1)
	sub, err := bus.SubscribeAsync("pipeline.update.engineer", func(msg *guide.Message) error {
		payload, _ := msg.Payload.(map[string]any)
		updateCh <- payload
		return nil
	})
	require.NoError(t, err)
	defer func() { _ = sub.Unsubscribe() }()

	task := &PipelineTaskInput{
		TaskID:    "task-1",
		AgentType: "engineer",
	}

	PublishPipelineTaskTerminalErrorUpdate(bus, "engineer-1", task, errors.New("boom"), 1)

	select {
	case update := <-updateCh:
		require.Equal(t, "failed", update["status"])
		require.Equal(t, "boom", update["error"])
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for failure pipeline update")
	}
}
