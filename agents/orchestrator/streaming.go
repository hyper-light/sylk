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

func (o *Orchestrator) publishStreamStart(ctx context.Context) error {
	metadata, ok := orchestratorStreamMetadataFromContext(ctx)
	if ok {
		o.logInfo("publishStreamStart",
			"correlation_id", metadata.CorrelationID,
			"source_agent", metadata.SourceAgentID)
	}
	if err := o.publishStreamEvent(ctx, &guide.StreamEvent{
		Type:      guide.StreamEventStart,
		Timestamp: time.Now(),
	}); err != nil {
		return err
	}
	// Announce the canonical `receiving` activity state so the UI has an
	// indicator from the moment the orchestrator begins work. Subsequent
	// transitions (reasoning, dispatching_to_peer, awaiting_peer_response,
	// composing_response) are announced from the relevant code paths.
	return o.publishAgentState(ctx, guide.AgentStateReceiving, "", nil)
}

// publishAgentState is the orchestrator's canonical helper for declaring an
// activity-state transition. It routes through shared.PublishAgentState
// with the orchestrator's identity stamped, so every transition flows
// through the single canonical publisher.
func (o *Orchestrator) publishAgentState(
	ctx context.Context,
	state guide.AgentActivityState,
	detail string,
	peer *guide.AgentStateEvent,
) error {
	return shared.PublishAgentState(o.bus, o.channels, ctx, o.config.AgentID, "orchestrator", state, detail, peer)
}

func (o *Orchestrator) publishStreamChunk(ctx context.Context, text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	o.logInfo("publishStreamChunk",
		"text_len", len(text),
		"text_prefix", truncateForLog(text, 80))
	return o.publishStreamEvent(ctx, &guide.StreamEvent{
		Type:      guide.StreamEventData,
		Text:      text,
		Timestamp: time.Now(),
	})
}

func (o *Orchestrator) publishStreamComplete(ctx context.Context, userResponse string, usage *guide.StreamUsage) error {
	// Announce `complete` before the stream complete event so the activity
	// channel and text-stream terminal arrive in the same order.
	if err := o.publishAgentState(ctx, guide.AgentStateComplete, "", nil); err != nil {
		o.logWarnMsg("orchestrator_agent_state_complete_publish_failed",
			"error", err.Error())
	}
	event := &guide.StreamEvent{
		Type:      guide.StreamEventComplete,
		Usage:     usage,
		Timestamp: time.Now(),
	}
	if trimmed := strings.TrimSpace(userResponse); trimmed != "" {
		event.Data = map[string]string{"user_response": trimmed}
	}
	return o.publishStreamEvent(ctx, event)
}

func (o *Orchestrator) publishStreamError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	// Announce `errored` before the stream error event so the activity
	// channel and text-stream terminal arrive in the same order.
	if stateErr := o.publishAgentState(ctx, guide.AgentStateErrored, err.Error(), nil); stateErr != nil {
		o.logWarnMsg("orchestrator_agent_state_errored_publish_failed",
			"underlying_error", err.Error(),
			"publish_error", stateErr.Error())
	}
	return o.publishStreamEvent(ctx, &guide.StreamEvent{
		Type:      guide.StreamEventError,
		Data:      map[string]string{"error": err.Error()},
		Timestamp: time.Now(),
	})
}

// publishStreamEvent is the core bus publisher for conversation stream events.
// Returns the underlying publish error so the orchestrator's conversation
// loop can surface a delivery failure; nil when bus/channels/metadata are
// missing (caller is not in a publishable state).
func (o *Orchestrator) publishStreamEvent(ctx context.Context, event *guide.StreamEvent) error {
	if event == nil || o.bus == nil || o.channels == nil {
		return nil
	}
	metadata, ok := orchestratorStreamMetadataFromContext(ctx)
	if !ok {
		return nil
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
	if err := o.bus.Publish(o.channels.Responses, msg); err != nil {
		o.logWarnMsg("orchestrator_stream_publish_failed",
			"event_type", string(event.Type),
			"correlation_id", metadata.CorrelationID,
			"target_agent", metadata.SourceAgentID,
			"error", err.Error())
		return fmt.Errorf("publish orchestrator stream event %s: %w", event.Type, err)
	}
	return nil
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
// agent push mechanism. Returns the underlying publish error so the caller
// can propagate delivery failures to the agent loop.
func (o *Orchestrator) publishNotificationPush(content string) error {
	if o.bus == nil || o.channels == nil {
		return nil
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

	if err := o.bus.Publish(o.channels.Responses, msg); err != nil {
		o.logWarnMsg("orchestrator_notification_push_failed",
			"push_id", pushID,
			"error", err.Error())
		return fmt.Errorf("publish notification push: %w", err)
	}
	return nil
}

// publishStreamEventForPush publishes a stream event using a push ID as
// correlation, so the Guide routes it to the TUI via the synthetic pending.
// Returns the underlying publish error for the caller to surface.
func (o *Orchestrator) publishStreamEventForPush(pushID string, event *guide.StreamEvent) error {
	if o.bus == nil || o.channels == nil || event == nil {
		return nil
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

	if err := o.bus.Publish(o.channels.Responses, msg); err != nil {
		o.logWarnMsg("orchestrator_push_stream_publish_failed",
			"push_id", pushID,
			"event_type", string(event.Type),
			"error", err.Error())
		return fmt.Errorf("publish orchestrator push stream %s: %w", event.Type, err)
	}
	return nil
}
