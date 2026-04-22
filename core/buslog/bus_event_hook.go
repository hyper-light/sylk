package buslog

import (
	"time"

	"github.com/adalundhe/sylk/agents/guide"
)

// BusEventHook is a ready-made implementation of guide.BusEventHook
// that emits one BusLogRecord per bus event into a BusLogger.
//
// Install via ChannelBus.SetEventHook to start capturing bus
// activity. Use SubscriberAgentResolver to enrich delivery records
// with agent identity — without it, delivery records carry only the
// topic (which implicitly encodes agent type/id for standard topics
// but not for custom names).
type BusEventHook struct {
	logger *BusLogger
	// resolveSubscriberAgent optionally maps a subscriber topic to
	// an "<agent_type>/<agent_id>" label. Most topics in sylk
	// follow the `{channel}.{agent_type}.{agent_id}` convention, so
	// a topic-parsing resolver is a reasonable default.
	resolveSubscriberAgent func(topic string) string
}

// NewBusEventHook wraps logger as a guide.BusEventHook. The
// resolver argument is optional — pass nil to use the default
// topic-parsing resolver.
func NewBusEventHook(logger *BusLogger, resolver func(topic string) string) *BusEventHook {
	if resolver == nil {
		resolver = DefaultSubscriberAgentResolver
	}
	return &BusEventHook{
		logger:                 logger,
		resolveSubscriberAgent: resolver,
	}
}

// DefaultSubscriberAgentResolver maps standard sylk topics to
// "<agent_type>/<agent_id>" labels. Handles the three canonical
// channel prefixes (request, response, error) and returns an empty
// string for topics that don't match the pattern.
func DefaultSubscriberAgentResolver(topic string) string {
	// Topic format: {channel}.{agent_type}.{agent_id}
	// We skip the leading channel name and join the remainder.
	first := 0
	for i, r := range topic {
		if r == '.' {
			first = i + 1
			break
		}
	}
	if first == 0 || first >= len(topic) {
		return ""
	}
	remainder := topic[first:]
	// Replace the next '.' with '/' so the output is "type/id".
	for i, r := range remainder {
		if r == '.' {
			return remainder[:i] + "/" + remainder[i+1:]
		}
	}
	return remainder
}

func (h *BusEventHook) OnPublish(topic string, msg *guide.Message, subscriberCount int, err error) {
	if h == nil || h.logger == nil {
		return
	}
	h.logger.Publish(topic, messageRefFromGuide(msg), subscriberCount, payloadSize(msg), err)
}

func (h *BusEventHook) OnDelivery(
	publishTopic, subscriberTopic string,
	subscriberID int64,
	async bool,
	queueLatencyMicros int64,
	msg *guide.Message,
	handlerErr error,
) {
	if h == nil || h.logger == nil {
		return
	}
	subscriberAgent := ""
	if h.resolveSubscriberAgent != nil {
		subscriberAgent = h.resolveSubscriberAgent(subscriberTopic)
	}
	h.logger.Delivery(
		publishTopic,
		subscriberTopic,
		subscriberID,
		subscriberAgent,
		async,
		queueLatencyMicros,
		messageRefFromGuide(msg),
		handlerErr,
	)
}

func (h *BusEventHook) OnSubscribe(topic string, subscriberID int64, async, wildcard bool, matchPattern string) {
	if h == nil || h.logger == nil {
		return
	}
	subscriberAgent := ""
	if h.resolveSubscriberAgent != nil {
		subscriberAgent = h.resolveSubscriberAgent(topic)
	}
	h.logger.Subscribed(topic, subscriberID, async, wildcard, matchPattern, subscriberAgent)
}

func (h *BusEventHook) OnUnsubscribe(topic string, subscriberID int64, duration time.Duration) {
	if h == nil || h.logger == nil {
		return
	}
	subscriberAgent := ""
	if h.resolveSubscriberAgent != nil {
		subscriberAgent = h.resolveSubscriberAgent(topic)
	}
	h.logger.Unsubscribed(topic, subscriberID, duration, subscriberAgent)
}

func (h *BusEventHook) OnOverflow(event guide.OverflowEvent) {
	if h == nil || h.logger == nil {
		return
	}
	h.logger.Overflow(event.Topic, event.Dropped, event.TotalDropped, event.Correlations, event.QueueRemaining)
}

func (h *BusEventHook) OnExpired(topic string, msg *guide.Message, expiredAt time.Time) {
	if h == nil || h.logger == nil {
		return
	}
	h.logger.Expired(topic, messageRefFromGuide(msg), expiredAt)
}

// messageRefFromGuide translates a guide.Message into the portable
// MessageRef shape the log envelope uses. Extracts only the
// identity/routing fields; payload bytes are captured separately on
// the Publish path so heavy payloads don't inflate delivery
// records.
func messageRefFromGuide(msg *guide.Message) MessageRef {
	if msg == nil {
		return MessageRef{}
	}
	return MessageRef{
		MessageID:     msg.ID,
		CorrelationID: msg.CorrelationID,
		ParentID:      msg.ParentID,
		Type:          string(msg.Type),
		SourceAgent:   msg.SourceAgentID,
		TargetAgent:   msg.TargetAgentID,
		Priority:      int(msg.Priority),
		Attempt:       msg.Attempt,
	}
}

// payloadSize returns an approximate serialized size for the
// message's payload. Used for throughput analysis; exact byte
// counts matter less than order-of-magnitude signal.
func payloadSize(msg *guide.Message) int {
	if msg == nil || msg.Payload == nil {
		return 0
	}
	switch v := msg.Payload.(type) {
	case string:
		return len(v)
	case []byte:
		return len(v)
	default:
		// For structured payloads we don't want to pay JSON
		// marshaling cost on the hot path; approximate by
		// returning 0 and letting downstream analysis reason
		// about routing rather than payload size.
		return 0
	}
}

// Ensure interface satisfaction at compile time.
var _ guide.BusEventHook = (*BusEventHook)(nil)
