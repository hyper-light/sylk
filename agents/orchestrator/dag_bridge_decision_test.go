package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/dag"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/pipeline/taskstate"
	"github.com/stretchr/testify/require"
)

func TestBlockingDecisionActivityContentIncludesFailureDetails(t *testing.T) {
	content := blockingDecisionActivityContent("dag-1", 0, []dag.FailedNodeSummary{
		{
			NodeName:  "engineer-worker",
			AgentType: "engineer",
			Error:     "invalid route response for node node-1",
		},
		{
			NodeID:    "node-2",
			AgentType: "tester-pipeline",
			Error:     "tests failed",
		},
		{
			NodeID: "node-3",
			Error:  "timed out",
		},
	})

	require.Contains(t, content, "engineer-worker [engineer]: invalid route response for node node-1")
	require.Contains(t, content, "node-2 [tester-pipeline]: tests failed")
	require.Contains(t, content, "1 more failure(s)")
	require.True(t, strings.HasPrefix(content, "DAG dag-1 layer 0 has blocking failures:"))
	require.True(t, strings.HasSuffix(content, "— awaiting decision"))
}

func TestOnBlockingDecisionRequiredPublishesDetailedActivity(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	activity := events.NewTestActivityCollector()
	bridge := NewDAGBridge(DefaultDAGBridgeConfig(), DAGBridgeDeps{
		ActivityPub: activity,
		SessionID:   "session-1",
		AgentID:     "orchestrator",
	})
	bridge.SetBus(bus)

	bridge.onBlockingDecisionRequired(dag.LayerDecisionRequest{
		DAGID:    "dag-1",
		LayerIdx: 0,
		FailedNodes: []dag.FailedNodeSummary{
			{
				NodeName:  "engineer-worker",
				AgentType: "engineer",
				Error:     "compile failed",
			},
		},
		Class: dag.FailureClassBlocking,
	})

	recorded := activity.Events()
	require.Len(t, recorded, 1)
	require.Contains(t, recorded[0].Content, "engineer-worker [engineer]: compile failed")
	require.Equal(t, events.EventTypeAgentError, recorded[0].EventType)
}

func TestResetPendingTaskPods_ReplacesPodAndPublishesPendingState(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	gotCh := make(chan *taskstate.Event, 1)
	sub, err := bus.SubscribeAsync(taskstate.Topic, func(msg *guide.Message) error {
		evt, ok := msg.Payload.(*taskstate.Event)
		require.True(t, ok, "payload type = %T", msg.Payload)
		gotCh <- evt
		return nil
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	act := &trackingActivator{}
	oldPod := newTestPipelineAgentPod(testPipelineAgentPodConfig{
		DAGID:     "task_1",
		SessionID: "session-1",
		Activator: act,
	})
	node := dag.NewNode(dag.NodeConfig{
		ID:        "task_1:execute",
		AgentType: "engineer",
		Prompt:    "build it",
	})
	require.NoError(t, oldPod.HoldForNode(context.Background(), "task_1:execute", NodeAgentTypes(node)))

	newPod := newTestPipelineAgentPod(testPipelineAgentPodConfig{
		DAGID:     "task_1",
		SessionID: "session-1",
		Activator: act,
	})

	bridge := NewDAGBridge(DefaultDAGBridgeConfig(), DAGBridgeDeps{
		SessionID: "session-1",
		AgentID:   "orchestrator",
	})
	bridge.SetBus(bus)
	bridge.activeDAGs["dag-1"] = &ActiveDAGMeta{
		SessionID: "session-1",
		TaskPods: map[string]*shared.AgentPod{
			"task_1": oldPod,
		},
		TaskPodFactory: func(taskID string) (*shared.AgentPod, error) {
			require.Equal(t, "task_1", taskID)
			return newPod, nil
		},
		TaskLabels: map[string]string{
			"task_1": "auth-checkout",
		},
		ResetPendingTaskPods: map[string]struct{}{
			"task_1": {},
		},
	}

	bridge.ResetPendingTaskPods("dag-1")

	meta := bridge.activeDAGs["dag-1"]
	require.Same(t, newPod, meta.TaskPods["task_1"])
	_, pending := meta.ResetPendingTaskPods["task_1"]
	require.False(t, pending)
	require.Equal(t, 1, act.releaseCount())

	select {
	case evt := <-gotCh:
		require.Equal(t, "task_1", evt.PipelineID)
		require.Equal(t, "auth-checkout", evt.TaskSlug)
		require.Equal(t, taskstate.StatusPending, evt.Status)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for pending task pipeline state")
	}
}
