package global

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/inspector/shared"
	"github.com/adalundhe/sylk/core/providers"
)

const (
	inspectorConversationMaxTokens = 16384
	inspectorConversationTemp      = 0.5
)

// ConversationResult holds the response from a conversational interaction.
type ConversationResult struct {
	Response string `json:"response"`
	Intent   string `json:"intent"`
}

// ResponseText implements the guide-layer responseTexter interface so the
// conversation history stores the clean response string, not a JSON blob.
func (r *ConversationResult) ResponseText() string {
	if r == nil {
		return ""
	}
	return r.Response
}

// handleConversation processes conversational requests. Static meta queries
// (status, issue count, help) are answered deterministically without an LLM.
// All other queries use StreamWithHandler for real-time text + thinking chunks.
func (gi *GlobalInspector) handleConversation(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	if staticResult := shared.TryStaticReply(fwd.Input, gi.getState(), nil); staticResult != nil {
		shared.PublishStreamChunk(gi.bus, gi.channels, ctx, "inspector", staticResult.Response)
		return &ConversationResult{
			Response: staticResult.Response,
			Intent:   staticResult.Intent,
		}, nil
	}

	if gi.getProvider() == nil {
		return nil, fmt.Errorf("inspector: LLM provider not configured")
	}

	return gi.executeConversationStream(ctx, fwd)
}

// executeConversationStream sends the conversation to the Anthropic provider
// using StreamWithHandler — streaming text chunks and thinking progress events
// back through the bus in real time.
func (gi *GlobalInspector) executeConversationStream(
	ctx context.Context,
	fwd *guide.ForwardedRequest,
) (*ConversationResult, error) {
	systemPrompt := shared.GlobalInspectorConversationSystemPrompt()
	userMessage := buildInspectorConversationPrompt(fwd)
	tools := gi.buildConversationToolDefinitions()

	temp := float64(inspectorConversationTemp)
	req := &providers.Request{
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: userMessage},
		},
		SystemPrompt: systemPrompt,
		MaxTokens:    inspectorConversationMaxTokens,
		Temperature:  &temp,
		Tools:        tools,
	}
	if len(tools) > 0 {
		req.ToolChoice = "auto"
	}

	llmCtx, cancel := context.WithTimeout(ctx, gi.config.DefaultTimeout)
	defer cancel()

	var text strings.Builder
	var thoughts strings.Builder

	p := gi.getProvider()
	err := p.StreamWithHandler(llmCtx, req, func(chunk *providers.StreamChunk) error {
		return gi.handleConversationChunk(ctx, chunk, &text, &thoughts)
	})
	if err != nil {
		return nil, fmt.Errorf("inspector conversation stream: %w", err)
	}

	trimmed := strings.TrimSpace(text.String())
	if trimmed == "" {
		return nil, fmt.Errorf("inspector conversation: empty response")
	}

	return &ConversationResult{
		Response: trimmed,
		Intent:   "chat",
	}, nil
}

// handleConversationChunk processes a single stream chunk — text deltas go to
// the chat viewport, thinking deltas go as progress events.
func (gi *GlobalInspector) handleConversationChunk(
	ctx context.Context,
	chunk *providers.StreamChunk,
	text *strings.Builder,
	thoughts *strings.Builder,
) error {
	switch chunk.Type {
	case providers.ChunkTypeStart:
		if chunk.RetryReset {
			shared.PublishStreamStart(gi.bus, gi.channels, ctx, "inspector")
		}
		text.Reset()
		thoughts.Reset()
	case providers.ChunkTypeText:
		text.WriteString(chunk.Text)
		shared.PublishStreamChunk(gi.bus, gi.channels, ctx, "inspector", chunk.Text)
	case providers.ChunkTypeThought:
		thoughts.WriteString(chunk.Text)
		thought := strings.TrimSpace(thoughts.String())
		if thought != "" {
			gi.publishThoughtProgress(ctx, thought)
		}
	case providers.ChunkTypeEnd:
		if chunk.Usage != nil {
			shared.AccumulateUsage(ctx, chunk.Usage)
		}
	}
	return nil
}

// publishThoughtProgress emits a thinking progress event via the stream bus.
func (gi *GlobalInspector) publishThoughtProgress(ctx context.Context, thought string) {
	if len(thought) > 200 {
		thought = thought[:197] + "..."
	}
	shared.PublishStreamEvent(gi.bus, gi.channels, ctx, "inspector", &guide.StreamEvent{
		Type: guide.StreamEventProgress,
		Data: &guide.ProgressData{
			Message: thought,
		},
		Timestamp: time.Now(),
	})
}

// buildInspectorConversationPrompt formats the forwarded request into a user
// message with conversation history and runtime context.
func buildInspectorConversationPrompt(fwd *guide.ForwardedRequest) string {
	var b strings.Builder

	if len(fwd.ConversationHistory) > 0 {
		b.WriteString("## Prior Conversation\n\n")
		for _, turn := range fwd.ConversationHistory {
			b.WriteString(fmt.Sprintf("[%s] User: %s\n", turn.Timestamp.Format(time.TimeOnly), turn.UserInput))
			if turn.AgentReply != "" {
				b.WriteString(fmt.Sprintf("[%s] %s: %s\n", turn.Timestamp.Format(time.TimeOnly), turn.AgentID, turn.AgentReply))
			}
		}
		b.WriteString("\n---\n\n")
	}

	b.WriteString("## User Query\n\n")
	b.WriteString(strings.TrimSpace(fwd.Input))

	return b.String()
}

// extractInspectorUserResponse returns the human-readable text from a result.
func extractInspectorUserResponse(data any) string {
	if cr, ok := data.(*ConversationResult); ok && cr != nil {
		return cr.Response
	}
	return ""
}

// isStreamedInspectorConversation reports whether the result was a streamed
// conversation (text already delivered via stream chunks).
func isStreamedInspectorConversation(data any) bool {
	_, ok := data.(*ConversationResult)
	return ok
}
