package shared

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/messaging"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/google/uuid"
)

// agentDisplayNames maps agent type IDs to human-readable names shown in
// the "thinking deeply" UI notification.
var agentDisplayNames = map[string]string{
	"engineer":           "Engineer",
	"architect":          "Architect",
	"designer":           "Designer",
	"guardian":           "Guardian",
	"orchestrator":       "Orchestrator",
	"librarian":          "Librarian",
	"archivalist":        "Archivalist",
	"academic":           "Academic",
	"inspector":          "Inspector",
	"inspector-pipeline": "Pipeline Inspector",
	"tester":             "Tester",
	"tester-pipeline":    "Pipeline Tester",
	"scribe":             "Scribe",
	"guide":              "Guide",
}

// AgentDisplayName returns the human-readable display name for an agent type.
func AgentDisplayName(agentType string) string {
	if name, ok := agentDisplayNames[agentType]; ok {
		return name
	}
	return agentType
}

// thinkingThreshold returns the TTFT watchdog threshold for a deliberation
// level. Higher deliberation tolerates longer thinking before alerting.
func thinkingThreshold(d llmruntime.Deliberation) time.Duration {
	switch d {
	case llmruntime.DeliberationLow:
		return 5 * time.Second
	case llmruntime.DeliberationMedium:
		return 15 * time.Second
	case llmruntime.DeliberationHigh:
		return 30 * time.Second
	case llmruntime.DeliberationMax:
		return 60 * time.Second
	default:
		return 20 * time.Second
	}
}

// ProgressPublisher publishes StreamEventProgress messages to the bus.
type ProgressPublisher struct {
	Bus           guide.EventBus
	Channels      *guide.AgentChannels
	AgentID       string
	CorrelationID string
	SourceAgentID string
}

type progressPublisherKey struct{}

// WithProgressPublisher attaches a ProgressPublisher to a context.
func WithProgressPublisher(ctx context.Context, pp *ProgressPublisher) context.Context {
	return context.WithValue(ctx, progressPublisherKey{}, pp)
}

// ProgressPublisherFromContext retrieves the ProgressPublisher from context.
func ProgressPublisherFromContext(ctx context.Context) *ProgressPublisher {
	if ctx == nil {
		return nil
	}
	pp, _ := ctx.Value(progressPublisherKey{}).(*ProgressPublisher)
	return pp
}

// Publish sends a progress message to the UI via the bus.
func (pp *ProgressPublisher) Publish(message string) {
	if pp == nil || pp.Bus == nil || pp.Channels == nil {
		return
	}
	event := &guide.StreamEvent{
		Type: guide.StreamEventProgress,
		Data: &guide.ProgressData{
			Message: message,
		},
		Timestamp: time.Now(),
	}
	stream := &guide.StreamResponse{
		CorrelationID:     pp.CorrelationID,
		RespondingAgentID: pp.AgentID,
		TargetAgentID:     pp.SourceAgentID,
		Event:             event,
	}
	msg := &guide.Message{
		ID:            fmt.Sprintf("think_%s_%s", pp.AgentID, uuid.New().String()[:8]),
		CorrelationID: pp.CorrelationID,
		Type:          guide.MessageTypeStream,
		Payload:       stream,
		SourceAgentID: pp.AgentID,
		TargetAgentID: pp.SourceAgentID,
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityNormal,
	}
	_ = pp.Bus.Publish(pp.Channels.Responses, msg)
}

func (pp *ProgressPublisher) PublishChunk(text string) {
	if pp == nil || pp.Bus == nil || pp.Channels == nil || strings.TrimSpace(text) == "" {
		return
	}
	event := &guide.StreamEvent{
		Type:      guide.StreamEventData,
		Text:      text,
		Timestamp: time.Now(),
	}
	stream := &guide.StreamResponse{
		CorrelationID:     pp.CorrelationID,
		RespondingAgentID: pp.AgentID,
		TargetAgentID:     pp.SourceAgentID,
		Event:             event,
	}
	msg := &guide.Message{
		ID:            fmt.Sprintf("chunk_%s_%s", pp.AgentID, uuid.New().String()[:8]),
		CorrelationID: pp.CorrelationID,
		Type:          guide.MessageTypeStream,
		Payload:       stream,
		SourceAgentID: pp.AgentID,
		TargetAgentID: pp.SourceAgentID,
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityNormal,
	}
	_ = pp.Bus.Publish(pp.Channels.Responses, msg)
}

func (pp *ProgressPublisher) PublishStart() {
	if pp == nil || pp.Bus == nil || pp.Channels == nil {
		return
	}
	event := &guide.StreamEvent{
		Type:      guide.StreamEventStart,
		Timestamp: time.Now(),
	}
	stream := &guide.StreamResponse{
		CorrelationID:     pp.CorrelationID,
		RespondingAgentID: pp.AgentID,
		TargetAgentID:     pp.SourceAgentID,
		Event:             event,
	}
	msg := &guide.Message{
		ID:            fmt.Sprintf("start_%s_%s", pp.AgentID, uuid.New().String()[:8]),
		CorrelationID: pp.CorrelationID,
		Type:          guide.MessageTypeStream,
		Payload:       stream,
		SourceAgentID: pp.AgentID,
		TargetAgentID: pp.SourceAgentID,
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityNormal,
	}
	_ = pp.Bus.Publish(pp.Channels.Responses, msg)
}

// CompleteWithWatchdog wraps a provider Complete call with a TTFT watchdog.
// If the LLM takes longer than the deliberation-based threshold, a
// "thinking deeply" event is emitted via the agent's event logger and
// a progress event is published to the UI.
type completionProvider interface {
	Complete(ctx context.Context, req *providers.Request) (*providers.Response, error)
}

type streamingCompletionProvider interface {
	completionProvider
	Stream(ctx context.Context, req *providers.Request) (<-chan *providers.StreamChunk, error)
}

type streamingCompletionResult struct {
	resp      *providers.Response
	sawChunks bool
}

func CompleteWithWatchdog(
	ctx context.Context,
	p completionProvider,
	req *providers.Request,
	displayName string,
) (*providers.Response, error) {
	start := time.Now()
	if shouldLiveStreamTurn(ctx) {
		if sp, ok := p.(streamingCompletionProvider); ok {
			result, err := completeStreamingWithWatchdog(ctx, sp, req, displayName)
			if err == nil && result.sawChunks {
				LogLLMCallFromContext(ctx, req.Model, result.resp, time.Since(start), nil)
				return result.resp, nil
			}
			if result.sawChunks {
				LogLLMCallFromContext(ctx, req.Model, result.resp, time.Since(start), err)
				return result.resp, err
			}
		}
		if pp := ProgressPublisherFromContext(ctx); pp != nil {
			llmruntime.PromoteForUserFacingTurn(req, pp.SourceAgentID, llmruntime.ThoughtVisibilitySummary)
		}
	}

	cancel := StartThinkingWatchdog(ctx, req, displayName)
	resp, err := p.Complete(ctx, req)
	cancel()

	LogLLMCallFromContext(ctx, req.Model, resp, time.Since(start), err)
	return resp, err
}

func shouldLiveStreamTurn(ctx context.Context) bool {
	pp := ProgressPublisherFromContext(ctx)
	if pp == nil {
		return false
	}
	return llmruntime.IsUserFacingSource(pp.SourceAgentID)
}

func completeStreamingWithWatchdog(
	ctx context.Context,
	p streamingCompletionProvider,
	req *providers.Request,
	displayName string,
) (streamingCompletionResult, error) {
	pp := ProgressPublisherFromContext(ctx)
	if pp != nil {
		llmruntime.PromoteForUserFacingTurn(req, pp.SourceAgentID, llmruntime.ThoughtVisibilitySummary)
	}

	chunks, err := p.Stream(ctx, req)
	if err != nil {
		return streamingCompletionResult{}, err
	}

	cancel := StartThinkingWatchdog(ctx, req, displayName)
	defer cancel()

	firstVisibleChunk := false
	streamedText := false
	sawChunks := false
	emitter := NewThoughtEmitter(llmruntime.EmitsThoughts(req))
	collector := providers.NewStreamCollector(func(chunk *providers.StreamChunk) {
		if chunk == nil {
			return
		}
		switch chunk.Type {
		case providers.ChunkTypeStart:
			if chunk.RetryReset && pp != nil {
				pp.PublishStart()
			}
		case providers.ChunkTypeText:
			if chunk.Text == "" {
				return
			}
			if !firstVisibleChunk {
				cancel()
				firstVisibleChunk = true
			}
			if pp != nil {
				streamedText = true
				pp.PublishChunk(chunk.Text)
			}
		case providers.ChunkTypeThought:
			if chunk.Text == "" {
				return
			}
			if !firstVisibleChunk {
				cancel()
				firstVisibleChunk = true
			}
			if pp != nil {
				if thought := emitter.AddDelta(chunk.Text); thought != "" {
					pp.Publish(thought)
				}
			}
		}
	})

	var streamErr error
	for chunk := range chunks {
		if chunk != nil {
			sawChunks = true
		}
		collector.Add(chunk)
		if chunk != nil && chunk.Type == providers.ChunkTypeError {
			streamErr = fmt.Errorf("stream error: %s", chunk.Text)
		}
	}
	if pp != nil {
		if thought := emitter.Flush(); thought != "" {
			pp.Publish(thought)
		}
	}
	if streamErr != nil {
		return streamingCompletionResult{resp: collector.Response(), sawChunks: sawChunks}, streamErr
	}
	resp := collector.Response()
	if pp != nil && resp != nil && streamedText {
		MarkResponseStreamedText(resp)
	}
	return streamingCompletionResult{resp: resp, sawChunks: sawChunks}, nil
}

// StartThinkingWatchdog schedules the standard "thinking deeply" fallback
// notification for long-running LLM turns. Call the returned cancel function
// once the turn has produced visible output or finished.
func StartThinkingWatchdog(ctx context.Context, req *providers.Request, displayName string) func() {
	deliberation := llmruntime.DeliberationFromRequest(req)
	threshold := thinkingThreshold(deliberation)
	return startThinkingTimer(ctx, threshold, displayName, string(deliberation))
}

// startThinkingTimer fires a "thinking deeply" event after threshold.
// Returns a cancel function that stops the timer.
func startThinkingTimer(
	ctx context.Context,
	threshold time.Duration,
	displayName string,
	deliberation string,
) func() {
	timer := time.AfterFunc(threshold, func() {
		LogContextEvent(ctx, agentlog.EventThinkingDeeply,
			agentlog.ThinkingDeeplyPayload{
				AgentName:    displayName,
				ElapsedMs:    threshold.Milliseconds(),
				Deliberation: deliberation,
			})

		if pp := ProgressPublisherFromContext(ctx); pp != nil {
			pp.Publish(fmt.Sprintf("%s is reasoning deeply...", displayName))
		}
	})
	return func() { timer.Stop() }
}
