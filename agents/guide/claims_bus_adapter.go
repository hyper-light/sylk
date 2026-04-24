package guide

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/google/uuid"
)

// ClaimsBusAdapter bridges the guide ChannelBus to claims.DeltaBus so
// the amplifier and ClaimsInbox can ride the existing transport.
//
// Every published Delta is wrapped in a Message (MessageTypeClaimsDelta)
// with the Delta in Payload. Subscribers decode Payload back into a
// Delta via ExtractClaimsDelta — in-process the value round-trips as
// a typed claims.Delta without any byte-level marshaling.
//
// The adapter itself holds no state beyond the bus handle. Safe for
// concurrent use.
type ClaimsBusAdapter struct {
	bus *ChannelBus
}

// NewClaimsBusAdapter wraps bus as a claims.DeltaBus. Returns nil when
// bus is nil.
func NewClaimsBusAdapter(bus *ChannelBus) *ClaimsBusAdapter {
	if bus == nil {
		return nil
	}
	return &ClaimsBusAdapter{bus: bus}
}

// PublishDelta serializes delta as a MessageTypeClaimsDelta Message
// and publishes on topic. Compiles to one bus.Publish per delta.
func (a *ClaimsBusAdapter) PublishDelta(_ context.Context, topic string, delta claims.Delta) error {
	if a == nil || a.bus == nil {
		return nil
	}
	if delta == nil {
		return fmt.Errorf("nil delta")
	}
	msg := &Message{
		ID:            uuid.NewString(),
		Type:          MessageTypeClaimsDelta,
		Payload:       delta,
		Timestamp:     time.Now().UTC(),
		SourceAgentID: "claims_amplifier",
	}
	if err := a.bus.Publish(topic, msg); err != nil {
		slog.Error("claims_delta_publish_failed",
			"topic", topic,
			"delta_kind", delta.DeltaKind(),
			"err", err.Error(),
		)
		return err
	}
	return nil
}

// SubscribeDelta registers a DeltaHandler on the given topic pattern.
// The returned subscription wraps the bus subscription so callers can
// Unsubscribe via the claims.DeltaSubscription surface.
func (a *ClaimsBusAdapter) SubscribeDelta(pattern string, handler claims.DeltaHandler) (claims.DeltaSubscription, error) {
	if a == nil || a.bus == nil {
		return nil, fmt.Errorf("nil bus adapter")
	}
	if handler == nil {
		return nil, fmt.Errorf("nil handler")
	}
	msgHandler := func(msg *Message) error {
		delta, err := ExtractClaimsDelta(msg)
		if err != nil {
			slog.Warn("claims_delta_decode_failed",
				"pattern", pattern,
				"err", err.Error(),
			)
			return nil // never surface decode errors into bus retry
		}
		if delta == nil {
			return nil
		}
		handler(delta)
		return nil
	}
	sub, err := a.bus.Subscribe(pattern, msgHandler)
	if err != nil {
		return nil, fmt.Errorf("subscribe %q: %w", pattern, err)
	}
	return &claimsSubscriptionAdapter{inner: sub, pattern: pattern}, nil
}

// ExtractClaimsDelta pulls a claims.Delta out of a Message payload.
// Returns (nil, nil) when the message is not a claims_delta message —
// callers usually ignore that case. Returns an error only when the
// message is a claims_delta message whose payload cannot be decoded.
func ExtractClaimsDelta(msg *Message) (claims.Delta, error) {
	if msg == nil {
		return nil, nil
	}
	if msg.Type != MessageTypeClaimsDelta {
		return nil, nil
	}
	switch payload := msg.Payload.(type) {
	case claims.Delta:
		return payload, nil
	case []byte:
		return claims.UnmarshalDelta(payload)
	case nil:
		return nil, fmt.Errorf("claims_delta message has nil payload (id=%s)", msg.ID)
	default:
		return nil, fmt.Errorf("claims_delta payload has unexpected type %T (id=%s)", payload, msg.ID)
	}
}

// ────────────────────────────────────────────────────────────────────
// Subscription wrapper
// ────────────────────────────────────────────────────────────────────

type claimsSubscriptionAdapter struct {
	inner   Subscription
	pattern string
}

func (s *claimsSubscriptionAdapter) Topic() string { return s.pattern }

func (s *claimsSubscriptionAdapter) Unsubscribe() error {
	if s == nil || s.inner == nil {
		return nil
	}
	return s.inner.Unsubscribe()
}
