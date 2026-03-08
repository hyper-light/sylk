package shared

import (
	"context"
	"fmt"
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

// CompleteWithWatchdog wraps a provider Complete call with a TTFT watchdog.
// If the LLM takes longer than the deliberation-based threshold, a
// "thinking deeply" event is emitted via the agent's event logger and
// a progress event is published to the UI.
type completionProvider interface {
	Complete(ctx context.Context, req *providers.Request) (*providers.Response, error)
}

func CompleteWithWatchdog(
	ctx context.Context,
	p completionProvider,
	req *providers.Request,
	displayName string,
) (*providers.Response, error) {
	deliberation := llmruntime.DeliberationFromRequest(req)
	threshold := thinkingThreshold(deliberation)

	start := time.Now()
	cancel := startThinkingTimer(ctx, threshold, displayName, string(deliberation))

	resp, err := p.Complete(ctx, req)
	cancel()

	LogLLMCallFromContext(ctx, req.Model, resp, time.Since(start), err)
	return resp, err
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
