package guardian

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
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

// publishStreamStart emits a stream start event.
func (g *Guardian) publishStreamStart(ctx context.Context, correlationID string) {
	if g.bus == nil {
		return
	}
	event := &guide.StreamEvent{
		Type: guide.StreamEventStart,
		Data: map[string]any{"agent": "guardian"},
	}
	_ = g.bus.Publish(g.channels.Responses, newStreamMessage(correlationID, g.id, event))
}

// publishStreamChunk emits a text chunk during streaming.
func (g *Guardian) publishStreamChunk(ctx context.Context, correlationID string, text string) {
	if g.bus == nil || text == "" {
		return
	}
	event := &guide.StreamEvent{
		Type: guide.StreamEventData,
		Text: text,
	}
	_ = g.bus.Publish(g.channels.Responses, newStreamMessage(correlationID, g.id, event))
}

// publishStreamComplete emits the final stream complete event.
func (g *Guardian) publishStreamComplete(ctx context.Context, correlationID string, text string, usage *guide.StreamUsage) {
	if g.bus == nil {
		return
	}
	event := &guide.StreamEvent{
		Type:  guide.StreamEventComplete,
		Text:  strings.TrimSpace(text),
		Usage: usage,
	}
	_ = g.bus.Publish(g.channels.Responses, newStreamMessage(correlationID, g.id, event))
}

func (g *Guardian) publishThoughtProgress(ctx context.Context, correlationID string, thought string) {
	if g.bus == nil {
		return
	}
	thought = strings.TrimSpace(thought)
	if thought == "" {
		return
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
	_ = g.bus.Publish(g.channels.Responses, newStreamMessage(correlationID, g.id, event))
}
