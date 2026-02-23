package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/providers"
)

const (
	conversationMaxTokens = 4096
	conversationTemp      = 0.5
)

// ConversationResult holds the response from a conversational interaction.
type ConversationResult struct {
	Response string `json:"response"`
	Intent   string `json:"intent"`
}

// handleConversation processes conversational requests via the Gemini Flash provider.
// Falls back to a deterministic status summary when no provider is configured.
func (o *Orchestrator) handleConversation(ctx context.Context, req *guide.ForwardedRequest) (any, error) {
	if o.provider == nil {
		return o.conversationFallback(ctx)
	}
	return o.executeConversation(ctx, req)
}

// executeConversation builds a conversation request, tries the static fast-path,
// and falls back to the LLM tool loop for complex queries.
func (o *Orchestrator) executeConversation(ctx context.Context, req *guide.ForwardedRequest) (*ConversationResult, error) {
	cr := o.buildConversationRequest(req)

	// Fast-path: deterministic response for trivial queries (no LLM call).
	if reply, ok := tryStaticOrchestratorReply(cr); ok {
		o.publishStreamChunk(ctx, reply)
		return &ConversationResult{
			Response: reply,
			Intent:   "chat",
		}, nil
	}

	// Build LLM request with conversation system prompt and read-only tools.
	systemPrompt := OrchestratorConversationSystemPrompt()
	userMessage := buildConversationUserPrompt(cr)
	tools := o.buildConversationToolDefinitions()

	temp := float64(conversationTemp)
	llmReq := &providers.Request{
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: userMessage},
		},
		SystemPrompt: systemPrompt,
		Model:        o.config.Model,
		MaxTokens:    conversationMaxTokens,
		Temperature:  &temp,
		Tools:        tools,
	}
	if len(tools) > 0 {
		llmReq.ToolChoice = "auto"
	}

	llmCtx, cancel := context.WithTimeout(ctx, o.config.LLMTimeout)
	defer cancel()
	llmCtx = providers.WithRetryObserver(llmCtx, o.retryObserver())

	response, err := o.executeToolLoop(llmCtx, llmReq)
	if err != nil {
		return nil, fmt.Errorf("orchestrator conversation: %w", err)
	}

	trimmed := strings.TrimSpace(response)
	o.publishStreamChunk(ctx, trimmed)

	return &ConversationResult{
		Response: trimmed,
		Intent:   "chat",
	}, nil
}

// conversationFallback returns a deterministic status summary when no LLM provider is available.
func (o *Orchestrator) conversationFallback(ctx context.Context) (*ConversationResult, error) {
	summary, err := o.GetSummary(ctx)
	if err != nil {
		return nil, fmt.Errorf("orchestrator conversation fallback: %w", err)
	}

	response := formatFallbackResponse(summary)

	// Publish as a single chunk for the stream bridge.
	o.publishStreamChunk(ctx, response)

	return &ConversationResult{
		Response: response,
		Intent:   "chat",
	}, nil
}

func formatFallbackResponse(summary *OrchestratorSummary) string {
	var b strings.Builder
	b.WriteString(summary.Overview)

	if summary.Workflows.Running > 0 {
		b.WriteString(fmt.Sprintf("\n\nActive workflows: %d running, %d completed, %d failed.",
			summary.Workflows.Running, summary.Workflows.Completed, summary.Workflows.Failed))
	}

	if summary.Tasks.Failed > 0 {
		b.WriteString(fmt.Sprintf("\n\nRecent failures: %d tasks failed.", summary.Tasks.Failed))
		for _, f := range summary.Tasks.RecentFailures {
			b.WriteString(fmt.Sprintf("\n- %s (%s): %s", f.Name, f.AgentID, f.Error))
		}
	}

	return b.String()
}

// extractOrchestratorUserResponse returns the human-readable response from a result.
func extractOrchestratorUserResponse(data any) string {
	if cr, ok := data.(*ConversationResult); ok && cr != nil {
		return cr.Response
	}
	return ""
}

// isStreamedOrchestratorConversation reports whether the result was a
// streamed conversation (text already delivered via stream chunks).
func isStreamedOrchestratorConversation(data any) bool {
	_, ok := data.(*ConversationResult)
	return ok
}
