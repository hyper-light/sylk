package shared

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/providers"
)

// InspectorStreamContext carries streaming correlation data through context.
type InspectorStreamContext struct {
	CorrelationID string
	SourceAgentID string
}

type inspectorStreamContextKey struct{}

// WithStreamContext attaches streaming metadata to a context.
func WithStreamContext(ctx context.Context, correlationID, sourceAgentID string) context.Context {
	return context.WithValue(ctx, inspectorStreamContextKey{}, InspectorStreamContext{
		CorrelationID: correlationID,
		SourceAgentID: sourceAgentID,
	})
}

// StreamMetadataFromContext extracts streaming metadata from a context.
func StreamMetadataFromContext(ctx context.Context) (InspectorStreamContext, bool) {
	metadata, ok := ctx.Value(inspectorStreamContextKey{}).(InspectorStreamContext)
	if !ok || metadata.CorrelationID == "" {
		return InspectorStreamContext{}, false
	}
	return metadata, true
}

// InspectorUsageAccumulator tracks token usage across multiple LLM calls.
type InspectorUsageAccumulator struct {
	mu              sync.Mutex
	inputTotal      int
	outputTotal     int
	reasoningTotal  int
	cacheReadTotal  int
	cacheWriteTotal int
}

type inspectorUsageAccumulatorKey struct{}

// WithUsageAccumulator creates a context with an attached usage accumulator.
func WithUsageAccumulator(ctx context.Context) (context.Context, *InspectorUsageAccumulator) {
	acc := &InspectorUsageAccumulator{}
	return context.WithValue(ctx, inspectorUsageAccumulatorKey{}, acc), acc
}

// AccumulateUsage adds provider usage to the context's accumulator.
func AccumulateUsage(ctx context.Context, usage *providers.Usage) {
	if usage == nil {
		return
	}
	acc, ok := ctx.Value(inspectorUsageAccumulatorKey{}).(*InspectorUsageAccumulator)
	if !ok || acc == nil {
		return
	}
	acc.mu.Lock()
	acc.inputTotal += usage.InputTokens
	acc.outputTotal += usage.OutputTokens
	acc.reasoningTotal += usage.ReasoningTokens
	acc.cacheReadTotal += usage.CacheReadTokens
	acc.cacheWriteTotal += usage.CacheWriteTokens
	acc.mu.Unlock()
}

// Total returns the accumulated usage as a StreamUsage.
func (a *InspectorUsageAccumulator) Total() *guide.StreamUsage {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inputTotal == 0 && a.outputTotal == 0 {
		return nil
	}
	return &guide.StreamUsage{
		InputTokens:      a.inputTotal,
		OutputTokens:     a.outputTotal,
		ReasoningTokens:  a.reasoningTotal,
		CacheReadTokens:  a.cacheReadTotal,
		CacheWriteTokens: a.cacheWriteTotal,
	}
}

// PublishStreamEvent publishes a stream event to the bus.
func PublishStreamEvent(
	bus guide.EventBus,
	channels *guide.AgentChannels,
	ctx context.Context,
	agentID string,
	event *guide.StreamEvent,
) {
	metadata, ok := StreamMetadataFromContext(ctx)
	if !ok {
		return
	}

	stream := &guide.StreamResponse{
		CorrelationID:     metadata.CorrelationID,
		RespondingAgentID: agentID,
		TargetAgentID:     metadata.SourceAgentID,
		Event:             event,
	}

	msg := &guide.Message{
		ID:            fmt.Sprintf("%s_stream_%d", agentID, time.Now().UnixNano()),
		CorrelationID: metadata.CorrelationID,
		Type:          guide.MessageTypeStream,
		Payload:       stream,
		SourceAgentID: agentID,
		TargetAgentID: metadata.SourceAgentID,
		Timestamp:     time.Now(),
	}

	_ = bus.Publish(channels.Responses, msg)
}

// PublishStreamStart emits a stream start event.
func PublishStreamStart(bus guide.EventBus, channels *guide.AgentChannels, ctx context.Context, agentID string) {
	PublishStreamEvent(bus, channels, ctx, agentID, &guide.StreamEvent{
		Type:      guide.StreamEventStart,
		Timestamp: time.Now(),
	})
}

// PublishStreamChunk emits a text data chunk.
func PublishStreamChunk(bus guide.EventBus, channels *guide.AgentChannels, ctx context.Context, agentID, text string) {
	PublishStreamEvent(bus, channels, ctx, agentID, &guide.StreamEvent{
		Type:      guide.StreamEventData,
		Text:      text,
		Timestamp: time.Now(),
	})
}

// PublishStreamComplete emits a stream completion event.
func PublishStreamComplete(
	bus guide.EventBus,
	channels *guide.AgentChannels,
	ctx context.Context,
	agentID, text string,
	usage *guide.StreamUsage,
) {
	PublishStreamEvent(bus, channels, ctx, agentID, &guide.StreamEvent{
		Type:      guide.StreamEventComplete,
		Text:      text,
		Usage:     usage,
		Timestamp: time.Now(),
	})
}

// PublishStreamError emits a stream error event.
func PublishStreamError(bus guide.EventBus, channels *guide.AgentChannels, ctx context.Context, agentID string, err error) {
	PublishStreamEvent(bus, channels, ctx, agentID, &guide.StreamEvent{
		Type:      guide.StreamEventError,
		Data:      map[string]string{"error": err.Error()},
		Timestamp: time.Now(),
	})
}
