package recovery

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// REC-04 implementation: the minimal RecoveryNotifier interface (36 lines)
// expands into a concrete, pluggable BusRecoveryNotifier that writes to the
// user chat on recovery events. The interface-over-bus abstraction keeps
// core/recovery free of any agents/guide or ChannelBus dependency — the TUI
// wiring layer supplies a sink that forwards to the appropriate bus topic.

// RecoveryEventKind enumerates the recovery-notifier events a sink must
// surface. Each kind maps to a user-visible action: a breakout prompt for
// the affected agent, an escalation notice, a force-kill notice, or a
// resource-reacquisition handshake after recovery.
type RecoveryEventKind int

const (
	// RecoveryEventBreakout signals an agent-targeted breakout prompt
	// injection. Intended consumer: the agent's inbound message channel.
	RecoveryEventBreakout RecoveryEventKind = iota

	// RecoveryEventEscalation signals a user-facing notification that an
	// agent is stuck and needs user intervention. Intended consumer: the
	// user chat panel.
	RecoveryEventEscalation

	// RecoveryEventForceKill signals that the recovery orchestrator forced
	// an agent termination after soft intervention and user escalation
	// both failed. Intended consumer: the user chat panel.
	RecoveryEventForceKill

	// RecoveryEventReacquire signals that a recovered agent should
	// re-acquire the resources it held before termination (LLM slots,
	// file handles, etc.). Intended consumer: the resource manager hook
	// that routes re-acquisition requests back to the agent.
	RecoveryEventReacquire
)

// String returns a stable, human-readable label for RecoveryEventKind.
// Used in telemetry and in default user-facing chat messages when the sink
// does not provide its own rendering.
func (k RecoveryEventKind) String() string {
	switch k {
	case RecoveryEventBreakout:
		return "breakout"
	case RecoveryEventEscalation:
		return "escalation"
	case RecoveryEventForceKill:
		return "force_kill"
	case RecoveryEventReacquire:
		return "reacquire"
	default:
		return "unknown"
	}
}

// RecoveryEvent is the typed payload the sink receives. Timestamp is
// populated by BusRecoveryNotifier at publish time; Details is
// event-specific (prompt text, assessment snapshot, kill reason, resource
// list) and keyed by stable string names defined as exported constants
// below.
type RecoveryEvent struct {
	Timestamp time.Time
	Kind      RecoveryEventKind
	SessionID string
	AgentID   string
	Message   string
	Details   map[string]any
}

// Details keys used in RecoveryEvent.Details. Sinks (and UI consumers)
// key on these constants rather than ad-hoc strings so a rename in one
// place would fail the build everywhere.
const (
	RecoveryDetailPrompt       = "prompt"
	RecoveryDetailAssessment   = "assessment"
	RecoveryDetailReason       = "reason"
	RecoveryDetailResources    = "resources"
	RecoveryDetailOverallScore = "overall_score"
	RecoveryDetailStatus       = "status"
)

// RecoveryEventSink is the abstract delivery surface for BusRecoveryNotifier
// output. The production implementation (wired in cmd/tui.go) publishes
// events onto the ChannelBus TopicActivity; tests inject a capturing stub.
//
// The interface is intentionally narrow so core/recovery stays free of any
// bus / events package dependency — dependency cycles between recovery,
// events, and guide are avoided by keeping the sink abstract here and
// concrete only at the wiring layer.
type RecoveryEventSink interface {
	PublishRecoveryEvent(event RecoveryEvent)
}

// BusRecoveryNotifier implements RecoveryNotifier by delivering every event
// to the supplied sink. It's the production notifier; tests wire their own
// implementations. Thread-safe for concurrent calls from supervisor and
// orchestrator goroutines.
type BusRecoveryNotifier struct {
	sink  RecoveryEventSink
	clock func() time.Time

	// responseMu guards the user-response map — the orchestrator's
	// SetUserResponse path flows through OnUserResponse, which needs
	// serialized access if multiple agents are in recovery simultaneously.
	responseMu      sync.Mutex
	lastUserAnswers map[string]UserRecoveryResponse
}

// NewBusRecoveryNotifier constructs a notifier that publishes to sink.
// A nil sink is rejected — silent-drop would defeat the whole point of
// REC-04. Tests that need a no-op notifier should pass a no-op sink.
func NewBusRecoveryNotifier(sink RecoveryEventSink) (*BusRecoveryNotifier, error) {
	if sink == nil {
		return nil, fmt.Errorf("recovery: NewBusRecoveryNotifier requires a non-nil sink")
	}
	return &BusRecoveryNotifier{
		sink:            sink,
		clock:           time.Now,
		lastUserAnswers: make(map[string]UserRecoveryResponse),
	}, nil
}

// SetClock overrides the default time source. Tests use this to produce
// deterministic timestamps.
func (n *BusRecoveryNotifier) SetClock(clock func() time.Time) {
	if clock != nil {
		n.clock = clock
	}
}

// InjectBreakoutPrompt publishes a breakout-prompt event so the agent's
// inbound channel (subscribed to the sink's underlying topic) receives a
// user-facing nudge to change strategy. Returns an error only if prompt is
// empty — the sink path is fire-and-forget so downstream delivery failures
// do not propagate.
func (n *BusRecoveryNotifier) InjectBreakoutPrompt(agentID string, prompt string) error {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return fmt.Errorf("recovery: breakout prompt is empty")
	}
	n.sink.PublishRecoveryEvent(RecoveryEvent{
		Timestamp: n.clock(),
		Kind:      RecoveryEventBreakout,
		AgentID:   strings.TrimSpace(agentID),
		Message:   prompt,
		Details:   map[string]any{RecoveryDetailPrompt: prompt},
	})
	return nil
}

// EscalateToUser publishes a user-facing escalation event. The user chat
// surfaces the message; the full assessment travels in Details so advanced
// UI consumers can render the per-score breakdown.
func (n *BusRecoveryNotifier) EscalateToUser(sessionID, agentID string, assessment HealthAssessment) error {
	n.sink.PublishRecoveryEvent(RecoveryEvent{
		Timestamp: n.clock(),
		Kind:      RecoveryEventEscalation,
		SessionID: strings.TrimSpace(sessionID),
		AgentID:   strings.TrimSpace(agentID),
		Message:   escalationMessage(agentID, assessment),
		Details: map[string]any{
			RecoveryDetailAssessment:   assessment,
			RecoveryDetailOverallScore: assessment.OverallScore,
			RecoveryDetailStatus:       assessment.Status,
		},
	})
	return nil
}

// OnUserResponse records the user's recovery decision so the orchestrator
// can act on it. No external sink publish here — the orchestrator polls
// its own state for the response; the notifier simply remembers the last
// answer for observability.
func (n *BusRecoveryNotifier) OnUserResponse(agentID string, response UserRecoveryResponse) {
	n.responseMu.Lock()
	defer n.responseMu.Unlock()
	n.lastUserAnswers[strings.TrimSpace(agentID)] = response
}

// LastUserResponse returns the most recent response recorded for agentID,
// or (zero, false) if no response has been observed. Used by the
// supervisor to reconcile user decisions across restarts.
func (n *BusRecoveryNotifier) LastUserResponse(agentID string) (UserRecoveryResponse, bool) {
	n.responseMu.Lock()
	defer n.responseMu.Unlock()
	resp, ok := n.lastUserAnswers[strings.TrimSpace(agentID)]
	return resp, ok
}

// NotifyForceKill publishes a user-facing termination notice so the user
// sees why the agent was killed. Best-effort delivery — failure to publish
// must not block the orchestrator's kill path, which has already committed
// to terminating the agent.
func (n *BusRecoveryNotifier) NotifyForceKill(sessionID, agentID string, reason string) {
	n.sink.PublishRecoveryEvent(RecoveryEvent{
		Timestamp: n.clock(),
		Kind:      RecoveryEventForceKill,
		SessionID: strings.TrimSpace(sessionID),
		AgentID:   strings.TrimSpace(agentID),
		Message:   forceKillMessage(agentID, reason),
		Details:   map[string]any{RecoveryDetailReason: strings.TrimSpace(reason)},
	})
}

// NotifyReacquireResources publishes a reacquisition event directed at the
// recovered agent so it re-opens the resources it held pre-termination
// (LLM slots, file handles, embedder tokens). The resource list is the
// authoritative hint; the agent-side consumer of the sink topic owns the
// actual re-acquisition semantics.
func (n *BusRecoveryNotifier) NotifyReacquireResources(agentID string, resources []string) {
	cleaned := make([]string, 0, len(resources))
	for _, r := range resources {
		if trimmed := strings.TrimSpace(r); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	n.sink.PublishRecoveryEvent(RecoveryEvent{
		Timestamp: n.clock(),
		Kind:      RecoveryEventReacquire,
		AgentID:   strings.TrimSpace(agentID),
		Message:   reacquireMessage(agentID, cleaned),
		Details:   map[string]any{RecoveryDetailResources: cleaned},
	})
}

// escalationMessage renders the default human-readable text shown in the
// user chat when an escalation fires. Sinks that want a different format
// can rebuild from Details instead of reading Message.
func escalationMessage(agentID string, a HealthAssessment) string {
	agent := strings.TrimSpace(agentID)
	if agent == "" {
		agent = "unknown agent"
	}
	return fmt.Sprintf("%s is stuck (score %.2f, status %s) and needs your attention.", agent, a.OverallScore, statusLabel(a.Status))
}

func forceKillMessage(agentID, reason string) string {
	agent := strings.TrimSpace(agentID)
	if agent == "" {
		agent = "unknown agent"
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return fmt.Sprintf("%s was terminated to recover the session.", agent)
	}
	return fmt.Sprintf("%s was terminated to recover the session (%s).", agent, reason)
}

func reacquireMessage(agentID string, resources []string) string {
	agent := strings.TrimSpace(agentID)
	if agent == "" {
		agent = "unknown agent"
	}
	if len(resources) == 0 {
		return fmt.Sprintf("%s recovered and will re-open its resources.", agent)
	}
	return fmt.Sprintf("%s recovered and will re-open: %s.", agent, strings.Join(resources, ", "))
}

func statusLabel(s AgentHealthStatus) string {
	switch s {
	case StatusHealthy:
		return "healthy"
	case StatusWarning:
		return "warning"
	case StatusStuck:
		return "stuck"
	case StatusCritical:
		return "critical"
	case StatusDeadlocked:
		return "deadlocked"
	default:
		return "unknown"
	}
}
