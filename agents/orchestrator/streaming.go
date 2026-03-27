package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/messaging"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/google/uuid"
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
	mu              sync.Mutex
	inputTotal      int
	outputTotal     int
	reasoningTotal  int
	cacheReadTotal  int
	cacheWriteTotal int
}

func (a *orchestratorUsageAccumulator) Add(usage *providers.Usage) {
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

func (a *orchestratorUsageAccumulator) Total() *guide.StreamUsage {
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

func handoffRequesterAgentIDFromContext(ctx context.Context) string {
	metadata, ok := orchestratorStreamMetadataFromContext(ctx)
	if !ok {
		return ""
	}
	return strings.TrimSpace(metadata.SourceAgentID)
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
	metadata, ok := orchestratorStreamMetadataFromContext(ctx)
	if ok {
		o.logInfo("publishStreamStart",
			"correlation_id", metadata.CorrelationID,
			"source_agent", metadata.SourceAgentID)
	}
	o.publishStreamEvent(ctx, &guide.StreamEvent{
		Type:      guide.StreamEventStart,
		Timestamp: time.Now(),
	})
}

func (o *Orchestrator) publishStreamChunk(ctx context.Context, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	o.logInfo("publishStreamChunk",
		"text_len", len(text),
		"text_prefix", truncateForLog(text, 80))
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
		RespondingAgentID: o.config.AgentID,
		TargetAgentID:     metadata.SourceAgentID,
		Metadata:          shared.MergeStreamMetadata(shared.StreamResponseMetadataFromContext(ctx), nil),
		Event:             event,
	}
	msg := &guide.Message{
		ID:            generateMessageID(),
		CorrelationID: metadata.CorrelationID,
		Type:          guide.MessageTypeStream,
		Payload:       stream,
		SourceAgentID: o.config.AgentID,
		TargetAgentID: metadata.SourceAgentID,
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityNormal,
	}
	_ = o.bus.Publish(o.channels.Responses, msg)
}

// publishStreamPush registers a stream push with the Guide so subsequent
// stream events route to the TUI. Returns the push ID for use as
// correlation ID in followup stream events.
func (o *Orchestrator) publishStreamPush(ctx context.Context) (string, error) {
	if o.bus == nil || o.channels == nil {
		return "", fmt.Errorf("bus or channels not available")
	}

	pushID := "push_" + uuid.New().String()

	push := &guide.AgentPush{
		PushID:   pushID,
		AgentID:  o.config.AgentID,
		PushType: guide.PushTypeStream,
	}

	event := &guide.StreamEvent{
		Type:      guide.StreamEventPush,
		Data:      push,
		Timestamp: time.Now(),
	}

	stream := &guide.StreamResponse{
		CorrelationID:     pushID,
		RespondingAgentID: o.config.AgentID,
		TargetAgentID:     o.config.AgentID,
		Event:             event,
	}

	msg := &guide.Message{
		ID:            generateMessageID(),
		CorrelationID: pushID,
		Type:          guide.MessageTypeStream,
		Payload:       stream,
		SourceAgentID: o.config.AgentID,
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityNormal,
	}

	if err := o.bus.Publish(o.channels.Responses, msg); err != nil {
		return "", fmt.Errorf("publish stream push: %w", err)
	}

	return pushID, nil
}

// publishNotificationPush sends a one-shot notification to the TUI via the
// agent push mechanism.
func (o *Orchestrator) publishNotificationPush(content string) {
	if o.bus == nil || o.channels == nil {
		return
	}

	pushID := "notif_" + uuid.New().String()

	push := &guide.AgentPush{
		PushID:   pushID,
		AgentID:  o.config.AgentID,
		PushType: guide.PushTypeNotification,
		Content:  content,
	}

	event := &guide.StreamEvent{
		Type:      guide.StreamEventPush,
		Data:      push,
		Timestamp: time.Now(),
	}

	stream := &guide.StreamResponse{
		CorrelationID:     pushID,
		RespondingAgentID: o.config.AgentID,
		TargetAgentID:     o.config.AgentID,
		Event:             event,
	}

	msg := &guide.Message{
		ID:            generateMessageID(),
		CorrelationID: pushID,
		Type:          guide.MessageTypeStream,
		Payload:       stream,
		SourceAgentID: o.config.AgentID,
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityNormal,
	}

	_ = o.bus.Publish(o.channels.Responses, msg)
}

// publishStreamEventForPush publishes a stream event using a push ID as
// correlation, so the Guide routes it to the TUI via the synthetic pending.
func (o *Orchestrator) publishStreamEventForPush(pushID string, event *guide.StreamEvent) {
	if o.bus == nil || o.channels == nil || event == nil {
		return
	}

	stream := &guide.StreamResponse{
		CorrelationID:     pushID,
		RespondingAgentID: o.config.AgentID,
		Event:             event,
	}

	msg := &guide.Message{
		ID:            generateMessageID(),
		CorrelationID: pushID,
		Type:          guide.MessageTypeStream,
		Payload:       stream,
		SourceAgentID: o.config.AgentID,
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityNormal,
	}

	_ = o.bus.Publish(o.channels.Responses, msg)
}
