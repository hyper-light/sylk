package shared

import (
	"context"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/google/uuid"
)

// ProtocolProjectionScope names which protocol emitted a projection
// event. Typed so consumers can fan messages to the right panel without
// string-matching topic suffixes.
type ProtocolProjectionScope string

const (
	ProtocolProjectionScopePipeline     ProtocolProjectionScope = "pipeline"
	ProtocolProjectionScopeGlobalReview ProtocolProjectionScope = "global_review"
)

// ProtocolProjectionEvent is the bus payload for projection snapshots
// published on guide.TopicProtocolProjection. Exactly one of Pipeline or
// GlobalReview is populated per event (matched against Scope).
// Consumers read the typed fields; no JSON-decoding a free-form map.
type ProtocolProjectionEvent struct {
	// Scope identifies which projection payload is populated.
	Scope ProtocolProjectionScope `json:"scope"`

	// SessionID scopes the event to a session so UI panels that track
	// per-session state can filter without inspecting the payload.
	SessionID string `json:"session_id,omitempty"`

	// AgentType is the agent that owns the protocol state this
	// projection came from (for display and per-agent filtering).
	AgentType string `json:"agent_type,omitempty"`

	// Pipeline carries a pipeline projection when Scope ==
	// ProtocolProjectionScopePipeline. nil otherwise.
	Pipeline *PipelineProjection `json:"pipeline,omitempty"`

	// GlobalReview carries a global review projection when Scope ==
	// ProtocolProjectionScopeGlobalReview. nil otherwise.
	GlobalReview *GlobalReviewProjection `json:"global_review,omitempty"`
}

// OpenPipelineTaskProtocolStateWithPublisher opens the pipeline
// protocol state for task into ctx and, if bus is non-nil, installs a
// projection publisher that forwards every state change to
// guide.TopicProtocolProjection. Returns the derived context and a
// single close function that tears down both the subscription and the
// state (matching the shape expected by agent request handlers that
// `defer close()`).
//
// This is the canonical entry point for pipeline agents: one call
// covers state-open + UI subscription + cleanup so every hot path gets
// the projection stream without per-agent wiring drift.
func OpenPipelineTaskProtocolStateWithPublisher(
	ctx context.Context,
	task *PipelineTaskInput,
	bus guide.EventBus,
	sessionID, agentType string,
) (context.Context, func()) {
	existing := PipelineProtocolStateFromContext(ctx) != nil
	ctx = WithPipelineTaskProtocolState(ctx, task)
	if existing {
		// Protocol state was opened upstream; do not publish or close
		// here — ownership belongs to the caller that opened it.
		return ctx, func() {}
	}
	state := PipelineProtocolStateFromContext(ctx)
	unsubscribe := PublishPipelineProjectionUpdates(bus, state, sessionID, agentType)
	return ctx, func() {
		unsubscribe()
		_ = ClosePipelineProtocolState(ctx)
	}
}

// PublishPipelineProjectionUpdates subscribes to projection changes on
// state and publishes each as a ProtocolProjectionEvent on
// guide.TopicProtocolProjection via bus. Returns an unsubscribe
// function; callers should defer it when the protocol state is torn
// down.
//
// Safe to pass nil bus or nil state — returns a no-op closure.
// Publish errors are swallowed by design: the subscription must not
// propagate back into the protocol state, and the projection bus is a
// best-effort notification channel. The authoritative state lives in
// the protocol store; the bus is an observer.
func PublishPipelineProjectionUpdates(bus guide.EventBus, state *PipelineProtocolState, sessionID, agentType string) func() {
	if bus == nil || state == nil {
		return func() {}
	}
	return state.SubscribeProjection(func(projection *PipelineProjection) {
		if projection == nil {
			return
		}
		_ = bus.Publish(guide.TopicProtocolProjection, &guide.Message{
			ID:        uuid.NewString(),
			Type:      guide.MessageTypeProtocolProjection,
			Timestamp: time.Now(),
			Payload: &ProtocolProjectionEvent{
				Scope:     ProtocolProjectionScopePipeline,
				SessionID: sessionID,
				AgentType: agentType,
				Pipeline:  projection,
			},
		})
	})
}

// PublishGlobalReviewProjectionUpdates is the global-review sibling of
// PublishPipelineProjectionUpdates. Same semantics.
func PublishGlobalReviewProjectionUpdates(bus guide.EventBus, state *GlobalReviewState, sessionID, agentType string) func() {
	if bus == nil || state == nil {
		return func() {}
	}
	return state.SubscribeProjection(func(projection *GlobalReviewProjection) {
		if projection == nil {
			return
		}
		_ = bus.Publish(guide.TopicProtocolProjection, &guide.Message{
			ID:        uuid.NewString(),
			Type:      guide.MessageTypeProtocolProjection,
			Timestamp: time.Now(),
			Payload: &ProtocolProjectionEvent{
				Scope:        ProtocolProjectionScopeGlobalReview,
				SessionID:    sessionID,
				AgentType:    agentType,
				GlobalReview: projection,
			},
		})
	})
}
