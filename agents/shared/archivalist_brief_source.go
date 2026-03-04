package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/google/uuid"
)

// ArchivalistBriefSource requests handoff briefs from the Archivalist via
// bus. The Archivalist has accumulated Scribe commentary and can produce
// targeted briefs richer than direct LLM summarization.
type ArchivalistBriefSource struct {
	bus     guide.EventBus
	timeout time.Duration
}

// NewArchivalistBriefSource creates a BriefSource that routes requests
// to the Archivalist via the event bus.
func NewArchivalistBriefSource(bus guide.EventBus) *ArchivalistBriefSource {
	return &ArchivalistBriefSource{
		bus:     bus,
		timeout: 10 * time.Second,
	}
}

// RequestBrief sends a briefing request to the Archivalist and waits for
// the response within the configured timeout.
func (a *ArchivalistBriefSource) RequestBrief(ctx context.Context, agentType string, contextSize, turnNumber int) (*handoff.ContextBrief, error) {
	correlationID := uuid.New().String()
	responseTopic := fmt.Sprintf("brief.response.%s", correlationID)

	// Subscribe for the response before publishing the request.
	responseCh := make(chan *handoff.ContextBrief, 1)
	sub, err := a.bus.Subscribe(responseTopic, func(msg *guide.Message) error {
		brief := a.parseBriefResponse(msg)
		if brief != nil {
			select {
			case responseCh <- brief:
			default:
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("subscribe brief response: %w", err)
	}
	defer sub.Unsubscribe()

	// Build and publish the briefing request.
	fwd := &guide.ForwardedRequest{
		CorrelationID: correlationID,
		SourceAgentID: "brief-requester",
		Input: fmt.Sprintf(
			`Generate a handoff brief for agent type %q. Context size: %d tokens, turn number: %d. `+
				`Return JSON with fields: task_summary, key_decisions, active_state, next_steps, blockers.`,
			agentType, contextSize, turnNumber,
		),
		Intent:        guide.IntentRecall,
		FireAndForget: false,
		Metadata: map[string]any{
			"request_type": "briefing",
			"agent_type":   agentType,
			"context_size": contextSize,
			"turn_number":  turnNumber,
		},
	}

	msg := guide.NewForwardMessage(
		fmt.Sprintf("brief_req_%s", correlationID[:8]),
		fwd,
	)
	msg.ReplyTo = &guide.ReplyTo{Topic: responseTopic}

	if err := a.bus.Publish(guide.TopicRequests("archivalist", "archivalist"), msg); err != nil {
		return nil, fmt.Errorf("publish brief request: %w", err)
	}

	// Wait for response with timeout.
	deadline, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	select {
	case brief := <-responseCh:
		return brief, nil
	case <-deadline.Done():
		return nil, fmt.Errorf("brief request timed out after %s", a.timeout)
	}
}

// parseBriefResponse extracts a ContextBrief from the bus response message.
func (a *ArchivalistBriefSource) parseBriefResponse(msg *guide.Message) *handoff.ContextBrief {
	if msg == nil || msg.Payload == nil {
		return nil
	}

	// Try to extract from RouteResponse.Data.
	resp, ok := msg.Payload.(*guide.RouteResponse)
	if !ok {
		return nil
	}
	if !resp.Success {
		return nil
	}

	dataStr, ok := resp.Data.(string)
	if !ok {
		return nil
	}

	var brief handoff.ContextBrief
	if err := json.Unmarshal([]byte(dataStr), &brief); err != nil {
		brief.TaskSummary = dataStr
		brief.GeneratedAt = time.Now()
	}

	return &brief
}
