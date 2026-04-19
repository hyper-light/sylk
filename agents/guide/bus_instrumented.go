package guide

import (
	"context"

	"github.com/adalundhe/sylk/core/activity"
)

// publishSpan begins an Activity Fabric span for a ChannelBus.Publish
// call. Every cross-agent message becomes a bus_message_emitted
// activity (Medium resolution) carrying topic, correlation chain,
// source/target, and message type. The span uses background context
// because Publish does not currently receive a context.Context — the
// causal-chain linkage comes from the message's CorrelationID +
// ParentID, which downstream subscribers Resolves against when they
// emit their own activities.
//
// Future tier: when bus.Publish is upgraded to take a context, switch
// to that context for FabricContext propagation. Until then,
// CorrelationID is the fabric's coupling.
func publishSpan(topic string, msg *Message) *activity.Span {
	subject := activity.Subject{
		PathPrefix: topic,
	}
	if msg != nil {
		subject.TargetAgent = msg.TargetAgentID
	}
	span := activity.StartSpan(context.Background(), activity.ActionBusMessageEmitted, subject)
	span.SetAttribute("topic", topic)
	if msg != nil {
		span.SetAttribute("message_id", msg.ID)
		span.SetAttribute("correlation_id", msg.CorrelationID)
		span.SetAttribute("parent_id", msg.ParentID)
		span.SetAttribute("type", string(msg.Type))
		span.SetAttribute("source_agent_id", msg.SourceAgentID)
		span.SetAttribute("target_agent_id", msg.TargetAgentID)
	}
	return span
}
