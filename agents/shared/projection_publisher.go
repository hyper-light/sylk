package shared

import (
	"context"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/google/uuid"
)

// GlobalReviewProjectionPresenceChecker is the global-review
// artifact presence checker for skill contract enforcement.
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
	ProtocolProjectionScopeGlobalReview ProtocolProjectionScope = "global_review"
)

// ProtocolProjectionEvent is the bus payload for projection snapshots
// published on guide.TopicProtocolProjection.
type ProtocolProjectionEvent struct {
	// Scope identifies which projection payload is populated.
	Scope ProtocolProjectionScope `json:"scope"`

	// SessionID scopes the event to a session so UI panels that track
	// per-session state can filter without inspecting the payload.
	SessionID string `json:"session_id,omitempty"`

	// AgentType is the agent that owns the protocol state this
	// projection came from (for display and per-agent filtering).
	AgentType string `json:"agent_type,omitempty"`

	// GlobalReview carries a global review projection when Scope ==
	// ProtocolProjectionScopeGlobalReview. nil otherwise.
	GlobalReview *GlobalReviewProjection `json:"global_review,omitempty"`
}

// OpenGlobalReviewContextWithPublisher is the global-review
// context opener. Opens the global review state from metadata into ctx,
// installs the projection presence checker + recovery-hint mapper, and
// -- when a bus is supplied -- publishes projection snapshots on
// TopicProtocolProjection.
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

// PublishGlobalReviewProjectionUpdates subscribes to projection changes
// on state and publishes each as a ProtocolProjectionEvent on
// guide.TopicProtocolProjection via bus. Returns an unsubscribe
// function; callers should defer it when the protocol state is torn
// down.
//
// Safe to pass nil bus or nil state -- returns a no-op closure.
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
