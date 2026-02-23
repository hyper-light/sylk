package orchestrator

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/messaging"
	"github.com/adalundhe/sylk/core/providers"
)

// orchestratorStreamContext carries correlation metadata through context
// for stream event publishing during conversation handling.
type orchestratorStreamContext struct {
	CorrelationID string
	SourceAgentID string
}

type orchestratorStreamContextKey struct{}
type orchestratorUsageAccumulatorKey struct{}

// orchestratorUsageAccumulator sums token counts from LLM calls
// within a single orchestrator request. Thread-safe for concurrent sub-calls.
type orchestratorUsageAccumulator struct {
	mu          sync.Mutex
	inputTotal  int
	outputTotal int
}

func (a *orchestratorUsageAccumulator) Add(usage *providers.Usage) {
	if usage == nil {
		return
	}
	a.mu.Lock()
	a.inputTotal += usage.InputTokens
	a.outputTotal += usage.OutputTokens
	a.mu.Unlock()
}

func (a *orchestratorUsageAccumulator) Total() *guide.StreamUsage {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inputTotal == 0 && a.outputTotal == 0 {
		return nil
	}
	return &guide.StreamUsage{InputTokens: a.inputTotal, OutputTokens: a.outputTotal}
}

func withOrchestratorStreamContext(ctx context.Context, correlationID, sourceAgentID string) context.Context {
	metadata := orchestratorStreamContext{
		CorrelationID: strings.TrimSpace(correlationID),
		SourceAgentID: strings.TrimSpace(sourceAgentID),
	}
	return context.WithValue(ctx, orchestratorStreamContextKey{}, metadata)
}

func orchestratorStreamMetadataFromContext(ctx context.Context) (orchestratorStreamContext, bool) {
	metadata, ok := ctx.Value(orchestratorStreamContextKey{}).(orchestratorStreamContext)
	if !ok {
		return orchestratorStreamContext{}, false
	}
	if metadata.CorrelationID == "" {
		return orchestratorStreamContext{}, false
	}
	return metadata, true
}

func withOrchestratorUsageAccumulator(ctx context.Context) (context.Context, *orchestratorUsageAccumulator) {
	acc := &orchestratorUsageAccumulator{}
	return context.WithValue(ctx, orchestratorUsageAccumulatorKey{}, acc), acc
}

func accumulateOrchestratorUsage(ctx context.Context, usage *providers.Usage) {
	if usage == nil || ctx == nil {
		return
	}
	acc, ok := ctx.Value(orchestratorUsageAccumulatorKey{}).(*orchestratorUsageAccumulator)
	if !ok || acc == nil {
		return
	}
	acc.Add(usage)
}

func (o *Orchestrator) publishStreamStart(ctx context.Context) {
	o.publishStreamEvent(ctx, &guide.StreamEvent{
		Type:      guide.StreamEventStart,
		Timestamp: time.Now(),
	})
}

func (o *Orchestrator) publishStreamChunk(ctx context.Context, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	o.publishStreamEvent(ctx, &guide.StreamEvent{
		Type:      guide.StreamEventData,
		Text:      text,
		Timestamp: time.Now(),
	})
}

func (o *Orchestrator) publishStreamComplete(ctx context.Context, userResponse string, usage *guide.StreamUsage) {
	event := &guide.StreamEvent{
		Type:      guide.StreamEventComplete,
		Usage:     usage,
		Timestamp: time.Now(),
	}
	if trimmed := strings.TrimSpace(userResponse); trimmed != "" {
		event.Data = map[string]string{"user_response": trimmed}
	}
	o.publishStreamEvent(ctx, event)
}

func (o *Orchestrator) publishStreamError(ctx context.Context, err error) {
	if err == nil {
		return
	}
	o.publishStreamEvent(ctx, &guide.StreamEvent{
		Type:      guide.StreamEventError,
		Data:      map[string]string{"error": err.Error()},
		Timestamp: time.Now(),
	})
}

// publishStreamEvent is the core bus publisher for conversation stream events.
func (o *Orchestrator) publishStreamEvent(ctx context.Context, event *guide.StreamEvent) {
	if event == nil || o.bus == nil || o.channels == nil {
		return
	}
	metadata, ok := orchestratorStreamMetadataFromContext(ctx)
	if !ok {
		return
	}
	stream := &guide.StreamResponse{
		CorrelationID:     metadata.CorrelationID,
		RespondingAgentID: "orchestrator",
		TargetAgentID:     metadata.SourceAgentID,
		Event:             event,
	}
	msg := &guide.Message{
		ID:            generateMessageID(),
		CorrelationID: metadata.CorrelationID,
		Type:          guide.MessageTypeStream,
		Payload:       stream,
		SourceAgentID: "orchestrator",
		TargetAgentID: metadata.SourceAgentID,
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityNormal,
	}
	_ = o.bus.Publish(o.channels.Responses, msg)
}
