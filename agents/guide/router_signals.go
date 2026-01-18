package guide

import (
	"sync"
	"time"
)

// RouterSignalEmitter is the interface for emitting progress signals from the router.
// This enables the TieredRouter to report agent-to-agent communication to the
// recovery system's progress tracking.
type RouterSignalEmitter interface {
	// EmitAgentRequest signals that an agent is requesting another agent.
	// fromAgentID is the source agent, sessionID identifies the session,
	// and toAgentID is the target agent being requested.
	EmitAgentRequest(fromAgentID, sessionID, toAgentID string)
}

// RouterSignalConfig configures signal emission for the TieredRouter.
type RouterSignalConfig struct {
	// SessionID is the session identifier for signal tracking.
	SessionID string
}

// routerSignals manages signal emission for TieredRouter.
// It is designed to be embedded or composed within TieredRouter.
type routerSignals struct {
	mu        sync.RWMutex
	emitter   RouterSignalEmitter
	sessionID string
}

// newRouterSignals creates a new routerSignals instance.
func newRouterSignals() *routerSignals {
	return &routerSignals{}
}

// SetSignalEmitter configures the signal emitter for progress tracking.
// Pass nil to disable signal emission.
func (rs *routerSignals) SetSignalEmitter(emitter RouterSignalEmitter) {
	rs.mu.Lock()
	rs.emitter = emitter
	rs.mu.Unlock()
}

// SetSessionID sets the session ID for signal tracking.
func (rs *routerSignals) SetSessionID(sessionID string) {
	rs.mu.Lock()
	rs.sessionID = sessionID
	rs.mu.Unlock()
}

// emitAgentRequest emits a signal for agent-to-agent communication.
// Safe to call with nil emitter (no-op).
func (rs *routerSignals) emitAgentRequest(fromAgentID, toAgentID string) {
	rs.mu.RLock()
	emitter := rs.emitter
	sessionID := rs.sessionID
	rs.mu.RUnlock()

	if emitter == nil {
		return
	}

	emitter.EmitAgentRequest(fromAgentID, sessionID, toAgentID)
}

// SignalMetrics tracks signal emission statistics.
type SignalMetrics struct {
	DirectRouteSignals int64     `json:"direct_route_signals"`
	GuideRouteSignals  int64     `json:"guide_route_signals"`
	ActionSignals      int64     `json:"action_signals"`
	LastSignalTime     time.Time `json:"last_signal_time,omitempty"`
}

// SetSignalEmitter configures the signal emitter for progress tracking.
// This enables the router to emit SignalAgentRequest signals when routing
// requests between agents. Pass nil to disable signal emission.
func (r *TieredRouter) SetSignalEmitter(emitter RouterSignalEmitter) {
	r.signals.SetSignalEmitter(emitter)
}

// SetSignalSessionID sets the session ID for signal tracking.
func (r *TieredRouter) SetSignalSessionID(sessionID string) {
	r.signals.SetSessionID(sessionID)
}

// emitDirectRouteSignal emits a signal for direct routing to another agent.
func (r *TieredRouter) emitDirectRouteSignal(targetAgentID string) {
	r.signals.emitAgentRequest(r.agentID, targetAgentID)
}

// emitGuideRouteSignal emits a signal for routing via Guide.
func (r *TieredRouter) emitGuideRouteSignal() {
	r.signals.emitAgentRequest(r.agentID, "guide")
}

// emitActionSignal emits a signal for programmatic action triggers.
func (r *TieredRouter) emitActionSignal(targetAgentID string) {
	r.signals.emitAgentRequest(r.agentID, targetAgentID)
}
