package guardian

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/providers"
)

// =============================================================================
// Conversation Execution
// =============================================================================

// guardianConversationRequest encapsulates the inputs for a Guardian conversation.
type guardianConversationRequest struct {
	Input     string
	Intent    GuardianIntent
	SessionID string
	History   []guide.ConversationTurn
}

// executeConversation handles a forwarded user request by classifying intent,
// building the LLM request, and running the tool loop.
func (g *Guardian) executeConversation(ctx context.Context, fwd *guide.ForwardedRequest) (*ConversationResult, error) {
	intent := classifyGuardianIntent(fwd.Input)

	req := &guardianConversationRequest{
		Input:     fwd.Input,
		Intent:    intent,
		SessionID: fwd.SessionID,
		History:   fwd.ConversationHistory,
	}

	response, err := g.composeUserFacingResponse(ctx, req)
	if err != nil {
		return nil, err
	}

	// Record conversation turn.
	g.conversationMu.Lock()
	g.conversationHistory = append(g.conversationHistory, guide.ConversationTurn{
		UserInput:  fwd.Input,
		AgentID:    g.id,
		AgentReply: response,
		Timestamp:  time.Now(),
	})
	if len(g.conversationHistory) > DefaultConversationBuffer {
		g.conversationHistory = g.conversationHistory[len(g.conversationHistory)-DefaultConversationBuffer:]
	}
	g.conversationMu.Unlock()

	return &ConversationResult{
		Response: response,
		Intent:   intent,
	}, nil
}

// composeUserFacingResponse runs the LLM tool loop to generate a response.
func (g *Guardian) composeUserFacingResponse(ctx context.Context, req *guardianConversationRequest) (string, error) {
	provider := g.getProvider()
	if provider == nil {
		return g.composeWithoutLLM(req)
	}

	systemPrompt := g.buildSystemPrompt(req.Intent)
	messages := g.buildConversationMessages(req)

	g.ensureToolLoopSkillsLoaded()
	tools := g.buildToolDefinitions()

	llmReq := &providers.Request{
		Messages:     messages,
		SystemPrompt: systemPrompt,
		MaxTokens:    DefaultMaxOutputTokens,
		Tools:        tools,
	}

	return g.executeToolLoop(ctx, llmReq, "conversation", func(chunk string) {
		g.publishStreamChunk(ctx, "", chunk)
	}, shared.SteeringLedgerFromContext(ctx))
}

// composeWithoutLLM generates a deterministic response when no LLM is available.
func (g *Guardian) composeWithoutLLM(req *guardianConversationRequest) (string, error) {
	switch req.Intent {
	case IntentReport:
		return g.generateHealthReport()
	case IntentAssess:
		return g.generateSecurityAssessment()
	case IntentCheckpoint:
		return "Checkpoint requested. Use the git_safety skill with action=checkpoint to create a safety commit.", nil
	default:
		return "Guardian is running in deterministic mode (no LLM configured). " +
			"I can provide health reports and security assessments. " +
			"Ask me about agent health, security status, or request a checkpoint.", nil
	}
}

// buildConversationMessages constructs the LLM message history.
func (g *Guardian) buildConversationMessages(req *guardianConversationRequest) []providers.Message {
	messages := make([]providers.Message, 0, len(req.History)+1)

	// Include recent conversation history.
	g.conversationMu.RLock()
	for _, turn := range g.conversationHistory {
		messages = append(messages,
			providers.Message{Role: providers.RoleUser, Content: turn.UserInput},
			providers.Message{Role: providers.RoleAssistant, Content: turn.AgentReply},
		)
	}
	g.conversationMu.RUnlock()

	// Current user message with intent hint.
	userContent := req.Input
	if req.Intent != IntentConverse {
		userContent = fmt.Sprintf("[intent: %s] %s", req.Intent, req.Input)
	}
	messages = append(messages, providers.Message{
		Role:    providers.RoleUser,
		Content: userContent,
	})

	return messages
}

// generateHealthReport creates a deterministic health summary.
func (g *Guardian) generateHealthReport() (string, error) {
	var sb strings.Builder
	sb.WriteString("## Guardian Health Report\n\n")

	// Agent health.
	snapshots := g.healthMon.AllSnapshots()
	if len(snapshots) == 0 {
		sb.WriteString("No agent health data available yet.\n\n")
	} else {
		sb.WriteString("### Agent Status\n\n")
		for _, snap := range snapshots {
			status := "healthy"
			if !snap.Responsive {
				status = "unresponsive"
			}
			if snap.CircuitOpen {
				status = "circuit-open"
			}
			sb.WriteString(fmt.Sprintf("- **%s** (%s): %s | errors: %d | restarts: %d\n",
				snap.AgentID, snap.AgentType, status, snap.ErrorCount, snap.RestartCount))
		}
		sb.WriteString("\n")
	}

	// Budget status.
	budget := g.healthMon.BudgetSnapshot()
	if budget.TokenBudget > 0 || budget.CostBudget > 0 {
		sb.WriteString("### Budget\n\n")
		if budget.TokenBudget > 0 {
			sb.WriteString(fmt.Sprintf("- Tokens: %d / %d (%.1f%%)\n",
				budget.TokensUsed, budget.TokenBudget, budget.TokenPercent))
		}
		if budget.CostBudget > 0 {
			sb.WriteString(fmt.Sprintf("- Cost: %d¢ / %d¢ (%.1f%%)\n",
				budget.CostCentsUsed, budget.CostBudget, budget.CostPercent))
		}
	}

	return sb.String(), nil
}

// generateSecurityAssessment creates a deterministic security summary.
func (g *Guardian) generateSecurityAssessment() (string, error) {
	var sb strings.Builder
	sb.WriteString("## Guardian Security Assessment\n\n")
	sb.WriteString("### Protected Branches\n\n")
	for _, branch := range g.config.ProtectedBranches {
		sb.WriteString(fmt.Sprintf("- `%s`\n", branch))
	}
	sb.WriteString("\n### Validation Status\n\n")
	sb.WriteString(fmt.Sprintf("- Injection scanning: %v\n", g.config.InjectionScanEnabled))
	sb.WriteString(fmt.Sprintf("- Credential scanning: %v\n", g.config.CredentialScanEnabled))
	sb.WriteString(fmt.Sprintf("- Diff review: %v\n", g.config.DiffReviewEnabled))

	return sb.String(), nil
}
