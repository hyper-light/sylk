package guide

import (
	"context"

	"github.com/adalundhe/sylk/core/activity"
)

// AttachFabricTrace serializes ctx's FabricContext onto msg.FabricTrace
// so downstream subscribers can restore causality. Call from any
// publisher path that has a context.Context with an established
// FabricContext (e.g., agent skills that emit a request).
func AttachFabricTrace(ctx context.Context, msg *Message) {
	if msg == nil || ctx == nil {
		return
	}
	fc := activity.FabricContextFromContext(ctx)
	if fc.TraceID == "" {
		return
	}
	msg.FabricTrace = &FabricTrace{
		TraceID:      fc.TraceID,
		SpanID:       fc.SpanID,
		ParentSpanID: fc.ParentSpanID,
		CausalChain:  append([]string(nil), fc.CausalChain...),
		Baggage:      copyBaggageMap(fc.Baggage),
	}
}

// RestoreFabricTrace returns ctx with the message's FabricTrace
// restored as a FabricContext, or ctx unchanged if no trace is
// attached. Receivers call this at the top of their handler so any
// downstream span / amplifier emission inherits the publisher's chain.
func RestoreFabricTrace(ctx context.Context, msg *Message) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if msg == nil || msg.FabricTrace == nil || msg.FabricTrace.TraceID == "" {
		return ctx
	}
	return activity.WithFabricContext(ctx, activity.FabricContext{
		TraceID:      msg.FabricTrace.TraceID,
		SpanID:       msg.FabricTrace.SpanID,
		ParentSpanID: msg.FabricTrace.ParentSpanID,
		CausalChain:  append([]string(nil), msg.FabricTrace.CausalChain...),
		Baggage:      copyBaggageMap(msg.FabricTrace.Baggage),
	})
}

func copyBaggageMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// publishSpan begins an Activity Fabric span for a ChannelBus.Publish
// call. Every cross-agent message becomes a bus_message_emitted
// activity (Medium resolution) carrying topic, correlation chain,
// source/target, and message type. The span uses background context
// because Publish does not currently receive a context.Context — the
// causal-chain linkage comes from the message's CorrelationID +
// ParentID, plus the optional FabricTrace serialized onto the message
// by AttachFabricTrace.
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
