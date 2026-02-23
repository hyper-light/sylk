package guide

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGuide_UserInterrupt_RemovesPendingAndForwardsCancel(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	g, err := NewWithClassifier(&MockClassifierClient{DefaultTarget: "architect"}, Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "test-session",
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, g.Start(ctx))
	defer func() { _ = g.Stop() }()

	actionCh := make(chan *Message, 8)
	sub, err := bus.Subscribe(TopicRequests("architect", "architect"), func(m *Message) error {
		select {
		case actionCh <- m:
		default:
		}
		return nil
	})
	require.NoError(t, err)
	defer func() { _ = sub.Unsubscribe() }()

	corrID := "corr-interrupt-1"
	req := &RouteRequest{
		CorrelationID: corrID,
		SourceAgentID: "tui",
		SessionID:     "test-session",
		Timestamp:     time.Now(),
	}
	g.pending.Add(req, &RouteResult{
		Intent:      IntentPlan,
		Domain:      DomainPlanning,
		TargetAgent: TargetArchitect,
		Confidence:  0.99,
	}, "architect")
	require.NotNil(t, g.GetPending(corrID))

	interrupt := &UserInterruptRequest{
		CorrelationID: corrID,
		SourceAgentID: "tui",
		Reason:        "esc",
		Timestamp:     time.Now(),
	}
	require.NoError(t, bus.Publish(TopicGuideRequests, NewUserInterruptMessage("", interrupt)))

	require.Eventually(t, func() bool {
		return g.GetPending(corrID) == nil
	}, time.Second, 10*time.Millisecond)

	require.Eventually(t, func() bool {
		select {
		case msg := <-actionCh:
			req, ok := msg.GetActionRequest()
			if !ok || req == nil {
				return false
			}
			if req.Action != "cancel" {
				return false
			}
			return req.CorrelationID == corrID
		default:
			return false
		}
	}, 2*time.Second, 10*time.Millisecond)
}
