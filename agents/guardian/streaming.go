package guardian

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/providers"
)

// =============================================================================
// Usage Accumulator
// =============================================================================

// guardianUsageAccumulator sums token counts across multiple LLM calls.
type guardianUsageAccumulator struct {
	mu              sync.Mutex
	inputTotal      int
	outputTotal     int
	reasoningTotal  int
	cacheReadTotal  int
	cacheWriteTotal int
}

func (a *guardianUsageAccumulator) Add(usage *providers.Usage) {
	if usage == nil {
		return
	}
	a.mu.Lock()
	a.inputTotal += usage.InputTokens
	a.outputTotal += usage.OutputTokens
	a.reasoningTotal += usage.ReasoningTokens
	a.cacheReadTotal += usage.CacheReadTokens
	a.cacheWriteTotal += usage.CacheWriteTokens
	a.mu.Unlock()
}

func (a *guardianUsageAccumulator) Total() *guide.StreamUsage {
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

// =============================================================================
// Stream Publishing
// =============================================================================

// publishStreamStart emits a stream start event. Returns the underlying
// publish error for the caller to surface.
func (g *Guardian) publishStreamStart(ctx context.Context, correlationID string) error {
	if g.bus == nil {
		return nil
	}
	metadata := shared.StreamResponseMetadataFromContext(ctx)
	event := &guide.StreamEvent{
		Type: guide.StreamEventStart,
		Data: map[string]any{"agent": "guardian"},
	}
	return g.publishStream(ctx, correlationID, newStreamMessage(correlationID, g.id, metadata, event), event.Type)
}

// publishStreamChunk emits a text chunk during streaming. Returns the
// underlying publish error for the caller to surface.
func (g *Guardian) publishStreamChunk(ctx context.Context, correlationID string, text string) error {
	if g.bus == nil || text == "" {
		return nil
	}
	metadata := shared.StreamResponseMetadataFromContext(ctx)
	event := &guide.StreamEvent{
		Type: guide.StreamEventData,
		Text: text,
	}
	return g.publishStream(ctx, correlationID, newStreamMessage(correlationID, g.id, metadata, event), event.Type)
}

// publishStreamComplete emits the final stream complete event. Returns the
// underlying publish error for the caller to surface.
func (g *Guardian) publishStreamComplete(ctx context.Context, correlationID string, text string, usage *guide.StreamUsage) error {
	if g.bus == nil {
		return nil
	}
	metadata := shared.StreamResponseMetadataFromContext(ctx)
	event := &guide.StreamEvent{
		Type:  guide.StreamEventComplete,
		Text:  strings.TrimSpace(text),
		Usage: usage,
	}
	return g.publishStream(ctx, correlationID, newStreamMessage(correlationID, g.id, metadata, event), event.Type)
}

func (g *Guardian) publishThoughtProgress(ctx context.Context, correlationID string, thought string) error {
	if g.bus == nil {
		return nil
	}
	metadata := shared.StreamResponseMetadataFromContext(ctx)
	thought = strings.TrimSpace(thought)
	if thought == "" {
		return nil
	}
	if len(thought) > 200 {
		thought = thought[:197] + "..."
	}
	event := &guide.StreamEvent{
		Type: guide.StreamEventProgress,
		Data: &guide.ProgressData{
			Message: thought,
		},
		Timestamp: time.Now(),
	}
	return g.publishStream(ctx, correlationID, newStreamMessage(correlationID, g.id, metadata, event), event.Type)
}

// publishStream is the single bus-publish choke point for guardian stream
// events. Any publish failure is logged (with correlation, event type, and
// error) and returned so the caller's loop can surface a delivery failure
// the same way it surfaces any other tool-execution error.
func (g *Guardian) publishStream(ctx context.Context, correlationID string, msg *guide.Message, eventType guide.StreamEventType) error {
	if err := g.bus.Publish(g.channels.Responses, msg); err != nil {
		if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			shared.LogWarning(lm.EventLogger, lm.AgentID, lm.SessionID, lm.CorrID,
				"guardian_stream_publish_failed", map[string]any{
					"event_type":     string(eventType),
					"correlation_id": correlationID,
					"error":          err.Error(),
				})
		}
		return fmt.Errorf("publish guardian stream event %s: %w", eventType, err)
	}
	return nil
}
