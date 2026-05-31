package guide

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/messaging"
	"github.com/google/uuid"
)

// priorityForInboxClass maps the semantic inbox class of a delta to
// the bus message priority used for queue ordering and overflow
// eviction. Higher priority is inserted ahead in the per-subscription
// queue and is the last to be dropped under DropOldest.
//
//   - ConsultResponse → Critical (a parked caller is waiting)
//   - ConsultRequest  → High     (the agent owes a reply)
//   - Directed        → Normal   (task / challenge / corrective)
//   - Phase           → Low      (recoverable from board state)
//   - Observation     → Background (sheds first under pressure)
func priorityForInboxClass(class claims.InboxClass) messaging.Priority {
	switch class {
	case claims.InboxClassConsultResolved:
		return messaging.PriorityCritical
	case claims.InboxClassConsultRequest:
		return messaging.PriorityHigh
	case claims.InboxClassDirected:
		return messaging.PriorityNormal
	case claims.InboxClassPhase:
		return messaging.PriorityLow
	}
	return messaging.PriorityBackground
}

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
	claims.TraceRouteFlowDelta("claims_route_flow_bus_publish_start", delta,
		"topic", topic,
		"subscriber_count", a.bus.TopicSubscriberCount(topic),
	)
	claims.RouteDebugLog().Info("claims_bus_publish_delta_start",
		append([]any{
			"topic", topic,
			"subscriber_count", a.bus.TopicSubscriberCount(topic),
		}, claims.DeltaDebugArgs(delta)...)...,
	)
	msg := &Message{
		ID:            uuid.NewString(),
		Type:          MessageTypeClaimsDelta,
		Payload:       delta,
		Timestamp:     time.Now().UTC(),
		SourceAgentID: "claims_amplifier",
		Priority:      priorityForInboxClass(claims.DeltaClass(delta)),
	}
	if err := a.bus.Publish(topic, msg); err != nil {
		slog.Error("claims_delta_publish_failed",
			"topic", topic,
			"delta_kind", delta.DeltaKind(),
			"err", err.Error(),
		)
		claims.RouteDebugLog().Info("claims_bus_publish_delta_failed",
			append([]any{
				"topic", topic,
				"message_id", msg.ID,
				"error", err.Error(),
			}, claims.DeltaDebugArgs(delta)...)...,
		)
		claims.TraceRouteFlowDelta("claims_route_flow_bus_publish_failed", delta,
			"topic", topic,
			"message_id", msg.ID,
			"error", err.Error(),
		)
		return err
	}
	claims.RouteDebugLog().Info("claims_bus_publish_delta_done",
		append([]any{
			"topic", topic,
			"message_id", msg.ID,
		}, claims.DeltaDebugArgs(delta)...)...,
	)
	claims.TraceRouteFlowDelta("claims_route_flow_bus_publish_done", delta,
		"topic", topic,
		"message_id", msg.ID,
	)
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
			claims.RouteDebugLog().Info("claims_bus_subscribe_decode_failed",
				"pattern", pattern,
				"message_id", messageIDForDebug(msg),
				"message_type", messageTypeForDebug(msg),
				"error", err.Error(),
			)
			return nil // never surface decode errors into bus retry
		}
		if delta == nil {
			claims.RouteDebugLog().Info("claims_bus_subscribe_ignored_non_delta",
				"pattern", pattern,
				"message_id", messageIDForDebug(msg),
				"message_type", messageTypeForDebug(msg),
			)
			return nil
		}
		claims.TraceRouteFlowDelta("claims_route_flow_bus_subscribe_delta_delivered", delta,
			"pattern", pattern,
			"message_id", messageIDForDebug(msg),
		)
		claims.RouteDebugLog().Info("claims_bus_subscribe_delta_delivered",
			append([]any{
				"pattern", pattern,
				"message_id", messageIDForDebug(msg),
			}, claims.DeltaDebugArgs(delta)...)...,
		)
		handler(delta)
		return nil
	}
	sub, err := a.bus.Subscribe(pattern, msgHandler)
	if err != nil {
		claims.RouteDebugLog().Info("claims_bus_subscribe_failed",
			"pattern", pattern,
			"error", err.Error(),
		)
		return nil, fmt.Errorf("subscribe %q: %w", pattern, err)
	}
	claims.RouteDebugLog().Info("claims_bus_subscribed",
		"pattern", pattern,
	)
	claims.RouteFlowDebugLog().Info("claims_route_flow_bus_subscribed",
		"pattern", pattern,
	)
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

func messageIDForDebug(msg *Message) string {
	if msg == nil {
		return ""
	}
	return msg.ID
}

func messageTypeForDebug(msg *Message) string {
	if msg == nil {
		return ""
	}
	return string(msg.Type)
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

// DroppedCount surfaces the bus-level overflow drop counter so the
// claims inbox (via claims.DroppedCounter) can aggregate per-agent
// coverage signals. Returns 0 when the underlying subscription does
// not expose its drop counter.
func (s *claimsSubscriptionAdapter) DroppedCount() uint64 {
	if s == nil || s.inner == nil {
		return 0
	}
	if dc, ok := s.inner.(interface{ DroppedCount() uint64 }); ok {
		return dc.DroppedCount()
	}
	return 0
}
