package ui

import (
	"context"
	"sync"
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
	agentpkg "github.com/adalundhe/sylk/ui/agent"
	chatpkg "github.com/adalundhe/sylk/ui/chat"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
)

type recordingGuideBus struct {
	mu       sync.Mutex
	messages []recordedGuideMessage
}

type recordedGuideMessage struct {
	topic   string
	message *guide.Message
}

func (b *recordingGuideBus) Publish(topic string, m *guide.Message) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.messages = append(b.messages, recordedGuideMessage{topic: topic, message: m})
	return nil
}

func (b *recordingGuideBus) Subscribe(string, guide.MessageHandler) (guide.Subscription, error) {
	return nil, nil
}

func (b *recordingGuideBus) SubscribeAsync(string, guide.MessageHandler) (guide.Subscription, error) {
	return nil, nil
}

func (b *recordingGuideBus) Close() error {
	return nil
}

func (b *recordingGuideBus) CloseWithContext(context.Context) error {
	return nil
}

func (b *recordingGuideBus) userInterrupts() []*guide.UserInterruptRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []*guide.UserInterruptRequest
	for _, record := range b.messages {
		if req, ok := record.message.GetUserInterruptRequest(); ok {
			out = append(out, req)
		}
	}
	return out
}

func (b *recordingGuideBus) actions() []*guide.ActionRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []*guide.ActionRequest
	for _, record := range b.messages {
		if req, ok := record.message.GetActionRequest(); ok {
			out = append(out, req)
		}
	}
	return out
}

func (b *recordingGuideBus) actionTopics() map[string][]*guide.ActionRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string][]*guide.ActionRequest)
	for _, record := range b.messages {
		if req, ok := record.message.GetActionRequest(); ok {
			out[record.topic] = append(out[record.topic], req)
		}
	}
	return out
}

func TestInterruptActiveRouteTargetsSelectedAgentStreamCorrelation(t *testing.T) {
	bus := &recordingGuideBus{}
	panel := newInterruptTestPanel(t)
	markInterruptTestAgentActive(t, panel, "architect", "cycle-architect", "route-architect")
	markInterruptTestAgentActive(t, panel, "librarian", "cycle-librarian", "route-librarian")
	if !panel.SelectByID("librarian") {
		t.Fatal("failed to select librarian row")
	}

	app := &AppModel{
		deps:       Deps{GuideBus: bus},
		agentPanel: panel,
	}
	cmd := app.interruptActiveRoute("esc")
	if cmd == nil {
		t.Fatal("interruptActiveRoute returned nil command")
	}
	_ = cmd()

	interrupts := bus.userInterrupts()
	if len(interrupts) != 1 {
		t.Fatalf("published %d user interrupts, want 1", len(interrupts))
	}
	if interrupts[0].Scope != guide.UserInterruptScopeRequest {
		t.Fatalf("interrupt scope = %q, want request", interrupts[0].Scope)
	}
	if interrupts[0].CorrelationID != "route-librarian" {
		t.Fatalf("interrupt correlation = %q, want route-librarian", interrupts[0].CorrelationID)
	}

	actions := bus.actions()
	if len(actions) < 2 {
		t.Fatalf("published %d cancel actions, want at least guide + direct", len(actions))
	}
	if actions[0].Action != "cancel" {
		t.Fatalf("action = %q, want cancel", actions[0].Action)
	}
	if actions[0].TargetAgentID != "librarian" {
		t.Fatalf("action target = %q, want librarian", actions[0].TargetAgentID)
	}
	if actions[0].CorrelationID != "route-librarian" {
		t.Fatalf("action correlation = %q, want route-librarian", actions[0].CorrelationID)
	}
	topics := bus.actionTopics()
	if len(topics[guide.TopicGuideRequests]) == 0 {
		t.Fatal("expected guide.requests cancel action")
	}
	if len(topics[guide.TopicRequests("librarian", "librarian")]) == 0 {
		t.Fatal("expected direct librarian request-topic cancel action")
	}
	if !hasSessionScopedCancel(actions, "librarian", defaultGuideSessionID) {
		t.Fatal("expected session-scoped librarian cancel fallback")
	}
}

func TestInterruptActiveRoutePrefersChatRouteOverPanelClaimCycle(t *testing.T) {
	bus := &recordingGuideBus{}
	panel := newInterruptTestPanel(t)
	markInterruptTestAgentActive(t, panel, "architect", "cycle-architect", "")
	if !panel.SelectByID("architect") {
		t.Fatal("failed to select architect row")
	}
	chat := chatpkg.New(theme.DefaultDark(), 256)
	chat.Update(msg.StreamStartMsg{
		SessionID:     "test-session",
		CorrelationID: "route-architect",
		AgentID:       "architect",
		AgentType:     "architect",
	})
	chat.Update(msg.ClaimsAgentStatusMsg{
		SessionID:           "test-session",
		AgentID:             "architect",
		CycleID:             "cycle-architect",
		StreamCorrelationID: "route-architect",
		ActionType:          "prompt",
		Active:              true,
	})

	app := &AppModel{
		deps:       Deps{GuideBus: bus},
		agentPanel: panel,
		chat:       chat,
	}
	cmd := app.interruptActiveRoute("esc")
	if cmd == nil {
		t.Fatal("interruptActiveRoute returned nil command")
	}
	_ = cmd()

	interrupts := bus.userInterrupts()
	if len(interrupts) != 1 {
		t.Fatalf("published %d user interrupts, want 1", len(interrupts))
	}
	if interrupts[0].CorrelationID != "route-architect" {
		t.Fatalf("interrupt correlation = %q, want route-architect", interrupts[0].CorrelationID)
	}
	target, ok := chat.LatestActiveInterruptTarget()
	if ok {
		t.Fatalf("chat still has active interrupt target after local mark: %+v", target)
	}
}

func TestInterruptAllActiveRoutesPublishesSessionInterruptAndPerAgentCancelActions(t *testing.T) {
	bus := &recordingGuideBus{}
	all := &recordingInterruptAllAgents{}
	panel := newInterruptTestPanel(t)
	markInterruptTestAgentActive(t, panel, "architect", "cycle-architect", "route-architect")
	markInterruptTestAgentActive(t, panel, "librarian", "cycle-librarian", "route-librarian")

	app := &AppModel{
		deps:       Deps{GuideBus: bus, InterruptAllAgents: all.InterruptAllAgents},
		agentPanel: panel,
	}
	cmd := app.interruptAllActiveRoutes("esc-all")
	if cmd == nil {
		t.Fatal("interruptAllActiveRoutes returned nil command")
	}
	_ = cmd()

	interrupts := bus.userInterrupts()
	if len(interrupts) != 1 {
		t.Fatalf("published %d user interrupts, want 1", len(interrupts))
	}
	if interrupts[0].Scope != guide.UserInterruptScopeSession {
		t.Fatalf("interrupt scope = %q, want session", interrupts[0].Scope)
	}
	if interrupts[0].SessionID != defaultGuideSessionID {
		t.Fatalf("interrupt session = %q, want %q", interrupts[0].SessionID, defaultGuideSessionID)
	}

	actions := bus.actions()
	if len(actions) < 4 {
		t.Fatalf("published %d cancel actions, want at least guide + direct per target", len(actions))
	}
	for _, action := range actions {
		if action.Action != "cancel" {
			t.Fatalf("action = %q, want cancel", action.Action)
		}
	}
	if !hasCorrelationCancel(actions, "architect", "route-architect") {
		t.Fatal("expected architect correlation-scoped cancel")
	}
	if !hasCorrelationCancel(actions, "librarian", "route-librarian") {
		t.Fatal("expected librarian correlation-scoped cancel")
	}
	topics := bus.actionTopics()
	if len(topics[guide.TopicRequests("architect", "architect")]) == 0 {
		t.Fatal("expected direct architect request-topic cancel action")
	}
	if len(topics[guide.TopicRequests("librarian", "librarian")]) == 0 {
		t.Fatal("expected direct librarian request-topic cancel action")
	}
	if !hasSessionScopedCancel(actions, "architect", defaultGuideSessionID) {
		t.Fatal("expected session-scoped architect cancel fallback")
	}
	if !hasSessionScopedCancel(actions, "librarian", defaultGuideSessionID) {
		t.Fatal("expected session-scoped librarian cancel fallback")
	}
	sessionID, reason, calls := all.Snapshot()
	if calls != 1 {
		t.Fatalf("InterruptAllAgents calls = %d, want 1", calls)
	}
	if sessionID != defaultGuideSessionID {
		t.Fatalf("InterruptAllAgents session = %q, want %q", sessionID, defaultGuideSessionID)
	}
	if reason != "esc-all" {
		t.Fatalf("InterruptAllAgents reason = %q, want esc-all", reason)
	}
}

type recordingInterruptAllAgents struct {
	mu        sync.Mutex
	calls     int
	sessionID string
	reason    string
}

func (r *recordingInterruptAllAgents) InterruptAllAgents(sessionID, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.sessionID = sessionID
	r.reason = reason
	return nil
}

func (r *recordingInterruptAllAgents) Snapshot() (string, string, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessionID, r.reason, r.calls
}

func hasSessionScopedCancel(actions []*guide.ActionRequest, targetAgentID, sessionID string) bool {
	for _, action := range actions {
		if action == nil || action.Action != "cancel" || action.TargetAgentID != targetAgentID {
			continue
		}
		if action.CorrelationID != "" {
			continue
		}
		data, _ := action.Data.(map[string]any)
		if data == nil {
			continue
		}
		if _, ok := data["correlation_id"]; ok {
			continue
		}
		if got, _ := data["session_id"].(string); got == sessionID {
			return true
		}
	}
	return false
}

func hasCorrelationCancel(actions []*guide.ActionRequest, targetAgentID, correlationID string) bool {
	for _, action := range actions {
		if action == nil || action.Action != "cancel" || action.TargetAgentID != targetAgentID {
			continue
		}
		if action.CorrelationID == correlationID {
			return true
		}
	}
	return false
}

func newInterruptTestPanel(t *testing.T) *agentpkg.Model {
	t.Helper()
	return agentpkg.New(theme.DefaultDark())
}

func markInterruptTestAgentActive(t *testing.T, panel *agentpkg.Model, agentID, cycleID, routeID string) {
	t.Helper()
	_, _ = panel.Update(msg.ClaimsAgentStatusMsg{
		SessionID:           "test-session",
		AgentID:             agentID,
		CycleID:             cycleID,
		StreamCorrelationID: routeID,
		Active:              true,
	})
}
