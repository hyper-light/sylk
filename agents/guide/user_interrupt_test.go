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

func TestGuide_UserInterrupt_SessionScopeCancelsAllPendingInSession(t *testing.T) {
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
	subArchitect, err := bus.Subscribe(TopicRequests("architect", "architect"), func(m *Message) error {
		actionCh <- m
		return nil
	})
	require.NoError(t, err)
	defer func() { _ = subArchitect.Unsubscribe() }()

	subLibrarian, err := bus.Subscribe(TopicRequests("librarian", "librarian"), func(m *Message) error {
		actionCh <- m
		return nil
	})
	require.NoError(t, err)
	defer func() { _ = subLibrarian.Unsubscribe() }()

	req1 := &RouteRequest{
		CorrelationID: "corr-session-1",
		SourceAgentID: "tui",
		SessionID:     "session-a",
		Timestamp:     time.Now(),
	}
	req2 := &RouteRequest{
		CorrelationID: "corr-session-2",
		SourceAgentID: "architect",
		SessionID:     "session-a",
		Timestamp:     time.Now(),
	}
	req3 := &RouteRequest{
		CorrelationID: "corr-other-session",
		SourceAgentID: "tui",
		SessionID:     "session-b",
		Timestamp:     time.Now(),
	}
	g.pending.Add(req1, &RouteResult{TargetAgent: TargetArchitect, Confidence: 0.99}, "architect")
	g.pending.Add(req2, &RouteResult{TargetAgent: TargetLibrarian, Confidence: 0.99}, "librarian")
	g.pending.Add(req3, &RouteResult{TargetAgent: TargetArchitect, Confidence: 0.99}, "architect")

	interrupt := &UserInterruptRequest{
		SessionID:     "session-a",
		Scope:         UserInterruptScopeSession,
		SourceAgentID: "tui",
		Reason:        "esc-all",
		Timestamp:     time.Now(),
	}
	require.NoError(t, bus.Publish(TopicGuideRequests, NewUserInterruptMessage("", interrupt)))

	require.Eventually(t, func() bool {
		return g.GetPending("corr-session-1") == nil &&
			g.GetPending("corr-session-2") == nil &&
			g.GetPending("corr-other-session") != nil
	}, time.Second, 10*time.Millisecond)

	require.Eventually(t, func() bool {
		seen := map[string]bool{}
		for {
			select {
			case msg := <-actionCh:
				req, ok := msg.GetActionRequest()
				if !ok || req == nil || req.Action != "cancel" {
					continue
				}
				seen[req.CorrelationID] = true
				if seen["corr-session-1"] && seen["corr-session-2"] {
					return true
				}
			default:
				return false
			}
		}
	}, 2*time.Second, 10*time.Millisecond)
}
