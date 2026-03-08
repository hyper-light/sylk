package shared

import (
	"context"

	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/providers"
)

// InspectorStreamContext preserves the historical inspector-specific type
// while delegating to the shared stream context implementation.
type InspectorStreamContext = agentshared.StreamContext

// InspectorUsageAccumulator preserves the historical inspector-specific type
// while delegating to the shared accumulator implementation.
type InspectorUsageAccumulator = agentshared.UsageAccumulator

// WithStreamContext attaches streaming metadata to a context.
func WithStreamContext(ctx context.Context, correlationID, sourceAgentID string) context.Context {
	return agentshared.WithStreamContext(ctx, correlationID, sourceAgentID)
}

// StreamMetadataFromContext extracts streaming metadata from a context.
func StreamMetadataFromContext(ctx context.Context) (InspectorStreamContext, bool) {
	return agentshared.StreamMetadataFromContext(ctx)
}

// WithUsageAccumulator creates a context with an attached usage accumulator.
func WithUsageAccumulator(ctx context.Context) (context.Context, *InspectorUsageAccumulator) {
	return agentshared.WithUsageAccumulator(ctx)
}

// AccumulateUsage adds provider usage to the context's accumulator.
func AccumulateUsage(ctx context.Context, usage *providers.Usage) {
	agentshared.AccumulateUsage(ctx, usage)
}

// PublishStreamEvent publishes a stream event to the bus.
func PublishStreamEvent(
	bus guide.EventBus,
	channels *guide.AgentChannels,
	ctx context.Context,
	agentID string,
	event *guide.StreamEvent,
) {
	agentshared.PublishStreamEvent(bus, channels, ctx, agentID, event)
}

// PublishStreamStart emits a stream start event.
func PublishStreamStart(bus guide.EventBus, channels *guide.AgentChannels, ctx context.Context, agentID string) {
	agentshared.PublishStreamStart(bus, channels, ctx, agentID)
}

// PublishStreamChunk emits a text data chunk.
func PublishStreamChunk(bus guide.EventBus, channels *guide.AgentChannels, ctx context.Context, agentID, text string) {
	agentshared.PublishStreamChunk(bus, channels, ctx, agentID, text)
}

// PublishStreamComplete emits a stream completion event.
func PublishStreamComplete(
	bus guide.EventBus,
	channels *guide.AgentChannels,
	ctx context.Context,
	agentID, text string,
	usage *guide.StreamUsage,
) {
	agentshared.PublishStreamComplete(bus, channels, ctx, agentID, text, usage)
}

// PublishStreamError emits a stream error event.
func PublishStreamError(bus guide.EventBus, channels *guide.AgentChannels, ctx context.Context, agentID string, err error) {
	agentshared.PublishStreamError(bus, channels, ctx, agentID, err)
}
