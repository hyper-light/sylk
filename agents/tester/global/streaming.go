package global

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/messaging"
	"github.com/adalundhe/sylk/core/providers"
)

// testerStreamContext carries correlation metadata through context
// for stream event publishing during conversation handling.
type testerStreamContext struct {
	CorrelationID string
	SourceAgentID string
}

type testerStreamContextKey struct{}
type testerUsageAccumulatorKey struct{}

// testerUsageAccumulator sums token counts from LLM calls
// within a single tester request. Thread-safe for concurrent sub-calls.
type testerUsageAccumulator struct {
	mu              sync.Mutex
	inputTotal      int
	outputTotal     int
	reasoningTotal  int
	cacheReadTotal  int
	cacheWriteTotal int
}

func (a *testerUsageAccumulator) Add(usage *providers.Usage) {
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

func (a *testerUsageAccumulator) Total() *guide.StreamUsage {
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

func withTesterStreamContext(ctx context.Context, correlationID, sourceAgentID string) context.Context {
	metadata := testerStreamContext{
		CorrelationID: strings.TrimSpace(correlationID),
		SourceAgentID: strings.TrimSpace(sourceAgentID),
	}
	return context.WithValue(ctx, testerStreamContextKey{}, metadata)
}

func testerStreamMetadataFromContext(ctx context.Context) (testerStreamContext, bool) {
	metadata, ok := ctx.Value(testerStreamContextKey{}).(testerStreamContext)
	if !ok {
		return testerStreamContext{}, false
	}
	if metadata.CorrelationID == "" {
		return testerStreamContext{}, false
	}
	return metadata, true
}

func withTesterUsageAccumulator(ctx context.Context) (context.Context, *testerUsageAccumulator) {
	acc := &testerUsageAccumulator{}
	return context.WithValue(ctx, testerUsageAccumulatorKey{}, acc), acc
}

func (gt *GlobalTester) publishStreamStart(ctx context.Context) {
	gt.publishStreamEvent(ctx, &guide.StreamEvent{
		Type:      guide.StreamEventStart,
		Timestamp: time.Now(),
	})
}

func (gt *GlobalTester) publishStreamChunk(ctx context.Context, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	gt.publishStreamEvent(ctx, &guide.StreamEvent{
		Type:      guide.StreamEventData,
		Text:      text,
		Timestamp: time.Now(),
	})
}

func (gt *GlobalTester) publishStreamComplete(ctx context.Context, userResponse string, usage *guide.StreamUsage) {
	event := &guide.StreamEvent{
		Type:      guide.StreamEventComplete,
		Usage:     usage,
		Timestamp: time.Now(),
	}
	if trimmed := strings.TrimSpace(userResponse); trimmed != "" {
		event.Data = map[string]string{"user_response": trimmed}
	}
	gt.publishStreamEvent(ctx, event)
}

func (gt *GlobalTester) publishStreamError(ctx context.Context, err error) {
	if err == nil {
		return
	}
	gt.publishStreamEvent(ctx, &guide.StreamEvent{
		Type:      guide.StreamEventError,
		Data:      map[string]string{"error": err.Error()},
		Timestamp: time.Now(),
	})
}

// publishStreamEvent is the core bus publisher for conversation stream events.
func (gt *GlobalTester) publishStreamEvent(ctx context.Context, event *guide.StreamEvent) {
	if event == nil || gt.bus == nil || gt.channels == nil {
		return
	}
	metadata, ok := testerStreamMetadataFromContext(ctx)
	if !ok {
		return
	}
	stream := &guide.StreamResponse{
		CorrelationID:     metadata.CorrelationID,
		RespondingAgentID: gt.id,
		TargetAgentID:     metadata.SourceAgentID,
		Event:             event,
	}
	msg := &guide.Message{
		ID:            gt.generateMessageID(),
		CorrelationID: metadata.CorrelationID,
		Type:          guide.MessageTypeStream,
		Payload:       stream,
		SourceAgentID: gt.id,
		TargetAgentID: metadata.SourceAgentID,
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityNormal,
	}
	_ = gt.bus.Publish(gt.channels.Responses, msg)
}
