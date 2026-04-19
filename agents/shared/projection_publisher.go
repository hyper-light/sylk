package shared

import (
	"context"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/google/uuid"
)

// PipelineProjectionPresenceChecker returns a skills.ArtifactPresenceChecker
// that reads the current projection snapshot from state each call. The
// tool runtime consults the checker before invoking a skill that
// declares Consumes, and after handler success to validate Produces.
// Using the live state — not a frozen projection — means produce
// checks see post-handler mutations.
func PipelineProjectionPresenceChecker(state *PipelineProtocolState) skills.ArtifactPresenceChecker {
	if state == nil {
		return nil
	}
	return func(artifact string) bool {
		projection := state.Projection()
		if projection == nil {
			return false
		}
		switch artifact {
		case ArtifactTestSnapshot:
			return projection.TesterSuiteCaptured
		case ArtifactPendingChallenge:
			return projection.PendingChallenge != nil
		case ArtifactVerificationArtifact:
			return len(projection.QueuedArtifacts) > 0
		case ArtifactPendingValidation:
			return projection.PendingValidation != nil
		}
		return false
	}
}

// GlobalReviewProjectionPresenceChecker is the global-review sibling
// of PipelineProjectionPresenceChecker.
func GlobalReviewProjectionPresenceChecker(state *GlobalReviewState) skills.ArtifactPresenceChecker {
	if state == nil {
		return nil
	}
	return func(artifact string) bool {
		projection := state.Projection()
		if projection == nil {
			return false
		}
		switch artifact {
		case ArtifactPendingChallenge:
			return projection.PendingChallenge != nil
		case ArtifactPendingValidation:
			return projection.PendingValidation != nil
		}
		return false
	}
}

// recoveryHintForArtifact is the canonical recovery-hint mapper
// registered alongside the presence checker. The tool runtime's
// ContractViolation surface calls this when a Consumes check fails so
// the LLM reads the same recovery prose the typed ProtocolError path
// uses.
func recoveryHintForArtifact(artifact string) string {
	return recoveryForArtifact(artifact)
}

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
	state := PipelineProtocolStateFromContext(ctx)
	// Install the dispatcher-facing presence checker + recovery hint
	// regardless of whether the state was opened here or upstream —
	// every tool call made through this context must enforce declared
	// contracts consistently.
	if checker := PipelineProjectionPresenceChecker(state); checker != nil {
		ctx = skills.WithArtifactPresenceChecker(ctx, checker)
		ctx = skills.WithRecoveryHintFn(ctx, recoveryHintForArtifact)
	}
	if existing {
		// Protocol state was opened upstream; do not publish or close
		// here — ownership belongs to the caller that opened it.
		return ctx, func() {}
	}
	unsubscribe := PublishPipelineProjectionUpdates(bus, state, sessionID, agentType)
	return ctx, func() {
		unsubscribe()
		_ = ClosePipelineProtocolState(ctx)
	}
}

// OpenGlobalReviewContextWithPublisher is the global-review sibling of
// OpenPipelineTaskProtocolStateWithPublisher. Opens the global review
// state from metadata into ctx, installs the projection presence
// checker + recovery-hint mapper, and — when a bus is supplied —
// publishes projection snapshots on TopicProtocolProjection.
//
// Returns the derived context (unchanged when no state is opened) and
// an unsubscribe closure for the publisher (no-op when bus is nil).
// Global review state is not explicitly closed (no durable handle owned
// by the context), so this function returns only an unsubscribe.
func OpenGlobalReviewContextWithPublisher(
	ctx context.Context,
	metadata map[string]any,
	bus guide.EventBus,
	sessionID, agentType string,
) (context.Context, func()) {
	existing := GlobalReviewStateFromContext(ctx) != nil
	ctx = WithGlobalReviewContext(ctx, metadata)
	state := GlobalReviewStateFromContext(ctx)
	if checker := GlobalReviewProjectionPresenceChecker(state); checker != nil {
		ctx = skills.WithArtifactPresenceChecker(ctx, checker)
		ctx = skills.WithRecoveryHintFn(ctx, recoveryHintForArtifact)
	}
	if state == nil {
		return ctx, func() {}
	}
	if existing {
		return ctx, func() {}
	}
	unsubscribe := PublishGlobalReviewProjectionUpdates(bus, state, sessionID, agentType)
	return ctx, func() {
		unsubscribe()
		_ = CloseGlobalReviewState(ctx)
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
