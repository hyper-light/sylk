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
		Factory: newTestFactory(t),
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
		Factory: newTestFactory(t),
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

func TestGuide_UserInterrupt_CancelsPendingDescendants(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	g, err := NewWithClassifier(&MockClassifierClient{DefaultTarget: "architect"}, Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "test-session",
		Factory: newTestFactory(t),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, g.Start(ctx))
	defer func() { _ = g.Stop() }()

	actionCh := make(chan *ActionRequest, 8)
	subArchitect, err := bus.SubscribeAsync(TopicRequests("architect", "architect"), func(m *Message) error {
		req, ok := m.GetActionRequest()
		if ok && req != nil {
			actionCh <- req
		}
		return nil
	})
	require.NoError(t, err)
	defer func() { _ = subArchitect.Unsubscribe() }()

	subLibrarian, err := bus.SubscribeAsync(TopicRequests("librarian", "librarian"), func(m *Message) error {
		req, ok := m.GetActionRequest()
		if ok && req != nil {
			actionCh <- req
		}
		return nil
	})
	require.NoError(t, err)
	defer func() { _ = subLibrarian.Unsubscribe() }()

	root := &RouteRequest{
		CorrelationID: "corr-root",
		SourceAgentID: "tui",
		SessionID:     "session-tree",
		Timestamp:     time.Now(),
	}
	child := &RouteRequest{
		CorrelationID:       "corr-child",
		ParentCorrelationID: "corr-root",
		SourceAgentID:       "inspector-pipeline",
		SessionID:           "session-tree",
		Timestamp:           time.Now(),
	}
	grandchild := &RouteRequest{
		CorrelationID:       "corr-grandchild",
		ParentCorrelationID: "corr-child",
		SourceAgentID:       "tester-pipeline",
		SessionID:           "session-tree",
		Timestamp:           time.Now(),
	}
	g.pending.Add(root, &RouteResult{TargetAgent: TargetArchitect, Confidence: 0.99}, "architect")
	g.pending.Add(child, &RouteResult{TargetAgent: TargetLibrarian, Confidence: 0.99}, "librarian")
	g.pending.Add(grandchild, &RouteResult{TargetAgent: TargetArchitect, Confidence: 0.99}, "architect")

	interrupt := &UserInterruptRequest{
		CorrelationID: "corr-root",
		SourceAgentID: "tui",
		Reason:        "esc",
		Timestamp:     time.Now(),
	}
	require.NoError(t, bus.Publish(TopicGuideRequests, NewUserInterruptMessage("", interrupt)))

	require.Eventually(t, func() bool {
		return g.GetPending("corr-root") == nil &&
			g.GetPending("corr-child") == nil &&
			g.GetPending("corr-grandchild") == nil
	}, time.Second, 10*time.Millisecond)

	require.Eventually(t, func() bool {
		seen := map[string]bool{}
		for {
			select {
			case req := <-actionCh:
				if req == nil || req.Action != "cancel" {
					continue
				}
				seen[req.CorrelationID] = true
				if seen["corr-root"] && seen["corr-child"] && seen["corr-grandchild"] {
					return true
				}
			default:
				return false
			}
		}
	}, 2*time.Second, 10*time.Millisecond)
}

func TestGuide_UserInterrupt_DropsLateChildRouteRequestForInterruptedParent(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	g, err := NewWithClassifier(&MockClassifierClient{DefaultTarget: "architect"}, Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "test-session",
		Factory: newTestFactory(t),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, g.Start(ctx))
	defer func() { _ = g.Stop() }()

	forwardedCh := make(chan *ForwardedRequest, 1)
	sub, err := bus.SubscribeAsync(TopicRequests("architect", "architect"), func(m *Message) error {
		fwd, ok := m.GetForwardedRequest()
		if ok && fwd != nil {
			forwardedCh <- fwd
		}
		return nil
	})
	require.NoError(t, err)
	defer func() { _ = sub.Unsubscribe() }()

	interrupt := &UserInterruptRequest{
		CorrelationID: "corr-parent",
		SourceAgentID: "tui",
		Reason:        "esc",
		Timestamp:     time.Now(),
	}
	require.NoError(t, bus.Publish(TopicGuideRequests, NewUserInterruptMessage("", interrupt)))

	childReq := &RouteRequest{
		CorrelationID:       "corr-child",
		ParentCorrelationID: "corr-parent",
		SourceAgentID:       "inspector-pipeline",
		TargetAgentID:       "architect",
		ExplicitTarget:      true,
		Input:               "late child handoff",
		SessionID:           "test-session",
		Timestamp:           time.Now(),
	}
	require.NoError(t, bus.Publish(TopicGuideRequests, NewRequestMessage("", childReq)))

	select {
	case forwarded := <-forwardedCh:
		t.Fatalf("expected interrupted child request to be dropped, got forward %#v", forwarded)
	case <-time.After(150 * time.Millisecond):
	}

	if pending := g.GetPending("corr-child"); pending != nil {
		t.Fatalf("expected no pending child request, got %#v", pending)
	}
}

func TestGuide_UserInterrupt_DropsLateRerouteForInterruptedCorrelation(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	g, err := NewWithClassifier(&MockClassifierClient{DefaultTarget: "architect"}, Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "test-session",
		Factory: newTestFactory(t),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, g.Start(ctx))
	defer func() { _ = g.Stop() }()

	forwardedCh := make(chan *ForwardedRequest, 1)
	sub, err := bus.SubscribeAsync(TopicRequests("librarian", "librarian"), func(m *Message) error {
		fwd, ok := m.GetForwardedRequest()
		if ok && fwd != nil {
			forwardedCh <- fwd
		}
		return nil
	})
	require.NoError(t, err)
	defer func() { _ = sub.Unsubscribe() }()

	interrupt := &UserInterruptRequest{
		CorrelationID: "corr-reroute",
		SourceAgentID: "tui",
		Reason:        "esc",
		Timestamp:     time.Now(),
	}
	require.NoError(t, bus.Publish(TopicGuideRequests, NewUserInterruptMessage("", interrupt)))

	reroute := &RerouteRequest{
		OriginalCorrelationID: "corr-reroute",
		OriginalInput:         "late reroute",
		SourceAgentID:         "architect",
		SuggestedTarget:       "librarian",
		SessionID:             "test-session",
	}
	require.NoError(t, bus.Publish(TopicGuideRequests, NewRerouteMessage("", reroute)))

	select {
	case forwarded := <-forwardedCh:
		t.Fatalf("expected interrupted reroute to be dropped, got forward %#v", forwarded)
	case <-time.After(150 * time.Millisecond):
	}
}
