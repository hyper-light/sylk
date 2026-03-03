package global

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/agents/tester/shared"
	"github.com/adalundhe/sylk/core/providers"
)

const testerConversationMaxTokens = 4096

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

// handleConversation processes conversational requests. When an LLM provider
// is available, all queries — including greetings — go through the LLM for
// natural responses. Static replies are only used as a degraded-mode fallback
// when no provider is configured.
func (gt *GlobalTester) handleConversation(ctx context.Context, req *guide.ForwardedRequest) (any, error) {
	cr := gt.buildConversationRequest(req)

	// When a provider is available, always prefer the LLM — even for
	// trivial greetings — so the user gets a natural conversational reply.
	if gt.getProvider() != nil {
		return gt.executeConversationLLM(ctx, cr)
	}

	// Degraded mode: no LLM provider. Use static replies for queries we
	// can answer deterministically; return an error for everything else.
	if reply, ok := tryStaticTesterReply(cr); ok {
		gt.publishStreamChunk(ctx, reply)
		return &ConversationResult{
			Response: reply,
			Intent:   "chat",
		}, nil
	}

	return nil, fmt.Errorf("tester: LLM provider not configured — authenticate with OpenAI to enable")
}

// executeConversationLLM sends the conversation request to the LLM with
// the tester system prompt, read-only tools, and tool loop.
func (gt *GlobalTester) executeConversationLLM(ctx context.Context, cr testerConversationRequest) (*ConversationResult, error) {
	systemPrompt := shared.TesterConversationSystemPrompt()
	userMessage := buildTesterConversationUserPrompt(cr)
	tools := gt.buildConversationToolDefinitions()

	llmReq := &providers.Request{
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: userMessage},
		},
		SystemPrompt: systemPrompt,
		MaxTokens:    testerConversationMaxTokens,
		Tools:        tools,
	}
	if len(tools) > 0 {
		llmReq.ToolChoice = "auto"
	}

	llmCtx, cancel := context.WithTimeout(ctx, gt.config.DefaultTimeout)
	defer cancel()

	response, err := gt.executeToolLoop(llmCtx, llmReq, agentshared.SteeringLedgerFromContext(llmCtx))
	if err != nil {
		return nil, fmt.Errorf("tester conversation: %w", err)
	}

	trimmed := strings.TrimSpace(response)
	gt.publishStreamChunk(ctx, trimmed)

	return &ConversationResult{
		Response: trimmed,
		Intent:   "chat",
	}, nil
}

// testerConversationRequest holds runtime fields for a conversation turn.
type testerConversationRequest struct {
	SessionID   string
	UserQuery   string
	Diagnoses   int
	TestPlan    string
	Harness     string
	AgentIDs    []string
	History     []guide.ConversationTurn
}

// buildConversationRequest extracts runtime context from the global tester state
// and the forwarded request. Caller must NOT hold gt.mu.
func (gt *GlobalTester) buildConversationRequest(req *guide.ForwardedRequest) testerConversationRequest {
	gt.mu.RLock()
	diagCount := len(gt.diagnoses)
	planSummary := ""
	if gt.currentPlan != nil {
		planSummary = fmt.Sprintf("Plan %s: %d planned cases, rationale: %s",
			gt.currentPlan.ID,
			len(gt.currentPlan.PlannedCase),
			truncateTesterString(gt.currentPlan.Rationale, 120))
	}
	harnessSummary := ""
	if gt.harness != nil {
		harnessSummary = gt.harness.Summary()
	}
	agentIDs := make([]string, 0, len(gt.knownAgents))
	for id := range gt.knownAgents {
		agentIDs = append(agentIDs, id)
	}
	gt.mu.RUnlock()

	return testerConversationRequest{
		SessionID: gt.config.SessionID,
		UserQuery: strings.TrimSpace(req.Input),
		Diagnoses: diagCount,
		TestPlan:  planSummary,
		Harness:   harnessSummary,
		AgentIDs:  agentIDs,
		History:   req.ConversationHistory,
	}
}

// buildTesterConversationUserPrompt formats the runtime context and user query
// into the user message for the LLM conversation call.
func buildTesterConversationUserPrompt(cr testerConversationRequest) string {
	var b strings.Builder

	// Prior conversation history.
	if len(cr.History) > 0 {
		b.WriteString("## Prior Conversation\n\n")
		for _, turn := range cr.History {
			b.WriteString(fmt.Sprintf("[%s] User: %s\n", turn.Timestamp.Format(time.TimeOnly), turn.UserInput))
			if turn.AgentReply != "" {
				b.WriteString(fmt.Sprintf("[%s] %s: %s\n", turn.Timestamp.Format(time.TimeOnly), turn.AgentID, turn.AgentReply))
			}
		}
		b.WriteString("\n---\n\n")
	}

	// Runtime context block.
	b.WriteString("## Runtime Context\n\n")

	b.WriteString(fmt.Sprintf("Registered agents: %d", len(cr.AgentIDs)))
	if len(cr.AgentIDs) > 0 {
		b.WriteString(fmt.Sprintf(" (%s)", strings.Join(cr.AgentIDs, ", ")))
	}
	b.WriteByte('\n')

	b.WriteString(fmt.Sprintf("Diagnosis reports: %d\n", cr.Diagnoses))

	if cr.TestPlan != "" {
		b.WriteString(fmt.Sprintf("Active test plan: %s\n", cr.TestPlan))
	}
	if cr.Harness != "" {
		b.WriteString(fmt.Sprintf("Test harness: %s\n", cr.Harness))
	}

	b.WriteString("\n---\n\n")

	// User query.
	b.WriteString("## User Query\n\n")
	b.WriteString(cr.UserQuery)

	return b.String()
}

// extractTesterUserResponse returns the human-readable response from a result.
func extractTesterUserResponse(data any) string {
	if cr, ok := data.(*ConversationResult); ok && cr != nil {
		return cr.Response
	}
	return ""
}

// isStreamedTesterConversation reports whether the result was a
// streamed conversation (text already delivered via stream chunks).
func isStreamedTesterConversation(data any) bool {
	_, ok := data.(*ConversationResult)
	return ok
}

func truncateTesterString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
