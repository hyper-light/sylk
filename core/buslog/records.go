// Package buslog is the session-global observability log for the
// inter-agent message bus (guide.ChannelBus).
//
// The bus is the substrate for every inter-agent interaction in
// sylk: route requests, forwarded work, responses, streaming events,
// registry announcements, signals, and typed coordination messages
// all flow through it. Without bus-level observability, debugging
// cross-agent behavior requires cross-referencing per-agent events
// logs and guessing at message ordering — exactly the class of
// problem buslog exists to eliminate.
//
// The output is a single append-only JSONL stream at
// .sylk/sessions/{sessionID}/bus/bus.{YYYY-MM-DD}.{nnn}.jsonl. Daily
// rotation + 64 MiB size cap via agentlog.StreamWriter. The stream
// is session-global (not per-agent) because the bus IS cross-agent;
// per-agent fragmentation would lose the interaction graph.
//
// Six record kinds cover the bus's full surface:
//
//   - bus_publish    — every ChannelBus.Publish call
//   - bus_delivery   — every successful delivery to a subscription
//   - bus_subscribe  — subscription registration
//   - bus_unsubscribe — subscription removal
//   - bus_overflow   — subscription queue overflow (drops)
//   - bus_expired    — message rejected for TTL/Deadline expiry
//   - bus_drop       — buslog itself dropped a record under pressure
//
// Correctness guarantees:
//
//   - Per-session monotonic sequence. Seq is strictly increasing so
//     cross-agent ordering is exact.
//   - Self-contained records: publisher, target, correlation ID,
//     message type, payload kind are all captured inline. Analysis
//     tools do not need to join against per-agent logs.
//   - Redaction at the write boundary via agentlog.Redactor.
//
// Robustness guarantees:
//
//   - Async bounded buffer. Publishes / deliveries never block on
//     disk I/O. Overflow emits periodic bus_drop records so lost
//     observability is visible, not silent.
//   - The bus itself stays on the fast path. Hooks are best-effort;
//     panics inside a hook are recovered so the bus publish loop
//     cannot be broken by a faulty subscriber.
//   - Close drains the buffer before returning. Hooked into
//     orchestrator shutdown.
package buslog

import (
	"time"
)

// RecordKind names the seven bus observability record types.
type RecordKind string

const (
	// KindPublish captures one ChannelBus.Publish call. Emitted
	// regardless of whether the message has subscribers — a publish
	// to a topic with no listeners is still a useful observability
	// signal.
	KindPublish RecordKind = "bus_publish"

	// KindDelivery captures one successful delivery to a subscriber.
	// Fires once per subscriber per published message.
	KindDelivery RecordKind = "bus_delivery"

	// KindSubscribe captures a subscription registration so the log
	// has the full subscriber topology visible.
	KindSubscribe RecordKind = "bus_subscribe"

	// KindUnsubscribe captures the inverse.
	KindUnsubscribe RecordKind = "bus_unsubscribe"

	// KindOverflow captures a subscription queue drop.
	KindOverflow RecordKind = "bus_overflow"

	// KindExpired captures a Publish rejected because the message
	// was already past its Deadline/TTL.
	KindExpired RecordKind = "bus_expired"

	// KindDrop captures buslog's own backpressure drops. Emitted
	// periodically (not per drop) to avoid amplification.
	KindDrop RecordKind = "bus_drop"
)

// BusLogRecord is the single envelope written to disk. Envelope
// fields (Kind, SessionID, Seq, Timestamp, MonoNano) are shared;
// one of the kind-specific bodies is populated.
type BusLogRecord struct {
	Kind      RecordKind `json:"kind"`
	SessionID string     `json:"session_id,omitempty"`

	// Seq is a per-session monotonically-increasing sequence number.
	Seq int64 `json:"seq"`

	// Timestamp is the wall-clock emission time (RFC3339 nanosecond).
	Timestamp time.Time `json:"t"`

	// MonoNano is process-monotonic nanoseconds since logger start.
	// Useful for latency math within a single process, immune to
	// wall-clock adjustments.
	MonoNano int64 `json:"mono_ns,omitempty"`

	Publish     *PublishBody     `json:"publish,omitempty"`
	Delivery    *DeliveryBody    `json:"delivery,omitempty"`
	Subscribe   *SubscribeBody   `json:"subscribe,omitempty"`
	Unsubscribe *UnsubscribeBody `json:"unsubscribe,omitempty"`
	Overflow    *OverflowBody    `json:"overflow,omitempty"`
	Expired     *ExpiredBody     `json:"expired,omitempty"`
	Drop        *DropBody        `json:"drop,omitempty"`
}

// MessageRef is the shared identity shape for every record: which
// message, which correlation chain, who sent it where.
type MessageRef struct {
	MessageID     string `json:"message_id"`
	CorrelationID string `json:"correlation_id,omitempty"`
	ParentID      string `json:"parent_id,omitempty"`
	Type          string `json:"type,omitempty"`
	SourceAgent   string `json:"source_agent,omitempty"`
	TargetAgent   string `json:"target_agent,omitempty"`
	Priority      int    `json:"priority,omitempty"`
	Attempt       int    `json:"attempt,omitempty"`
}

// PublishBody captures a single Publish call. SubscriberCount is
// the number of subscriptions the publish reached (combining exact +
// wildcard matches). PayloadBytes captures the serialized payload
// size so throughput analysis is possible.
type PublishBody struct {
	Topic           string     `json:"topic"`
	Message         MessageRef `json:"message"`
	SubscriberCount int        `json:"subscriber_count"`
	PayloadBytes    int        `json:"payload_bytes,omitempty"`
	Err             string     `json:"err,omitempty"`
}

// DeliveryBody captures a single subscriber delivery. SubscriberID
// identifies the subscription; SubscriberTopic is the topic it was
// registered on (may differ from PublishTopic on wildcard matches).
// QueueLatencyMicros captures how long the message sat in the
// subscription's queue between publish and handler invocation.
type DeliveryBody struct {
	PublishTopic       string     `json:"publish_topic"`
	SubscriberTopic    string     `json:"subscriber_topic,omitempty"`
	SubscriberID       int64      `json:"subscriber_id,omitempty"`
	SubscriberAgent    string     `json:"subscriber_agent,omitempty"`
	Async              bool       `json:"async,omitempty"`
	QueueLatencyMicros int64      `json:"queue_latency_us,omitempty"`
	Message            MessageRef `json:"message"`
	HandlerErr         string     `json:"handler_err,omitempty"`
}

// SubscribeBody captures a subscription registration. Wildcard
// flag and MatchPattern are populated for pattern subscriptions
// (e.g. "request.*") vs exact topic subscriptions.
type SubscribeBody struct {
	Topic           string `json:"topic"`
	SubscriberID    int64  `json:"subscriber_id"`
	Async           bool   `json:"async,omitempty"`
	Wildcard        bool   `json:"wildcard,omitempty"`
	MatchPattern    string `json:"match_pattern,omitempty"`
	SubscriberAgent string `json:"subscriber_agent,omitempty"`
}

// UnsubscribeBody mirrors SubscribeBody for removal. Duration
// captures how long the subscription was alive.
type UnsubscribeBody struct {
	Topic           string        `json:"topic"`
	SubscriberID    int64         `json:"subscriber_id"`
	Duration        time.Duration `json:"duration"`
	SubscriberAgent string        `json:"subscriber_agent,omitempty"`
}

// OverflowBody captures a subscription queue drop event. Maps 1:1
// to guide.OverflowEvent.
type OverflowBody struct {
	Topic            string   `json:"topic"`
	Dropped          int      `json:"dropped"`
	TotalDropped     uint64   `json:"total_dropped"`
	Correlations     []string `json:"correlations,omitempty"`
	QueueRemaining   int      `json:"queue_remaining"`
}

// ExpiredBody captures a Publish rejection due to TTL/Deadline.
type ExpiredBody struct {
	Topic     string     `json:"topic"`
	Message   MessageRef `json:"message"`
	ExpiredAt time.Time  `json:"expired_at,omitempty"`
}

// DropBody captures buslog's own backpressure: records lost to
// buffer-full or closed-mid-emit. Emitted periodically.
type DropBody struct {
	Reason       string `json:"reason"`
	DroppedCount int64  `json:"dropped_count"`
}
