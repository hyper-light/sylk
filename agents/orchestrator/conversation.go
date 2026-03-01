package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/architect"
	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/providers"
)

const (
	conversationMaxTokens = 4096
	conversationTemp      = 0.5

	// ingestionLLMTimeout is the LLM timeout for generating a plan ingestion
	// summary. Runs asynchronously — does not block the architect's sync wait.
	ingestionLLMTimeout = 45 * time.Second
)

// ConversationResult holds the response from a conversational interaction.
type ConversationResult struct {
	Response string `json:"response"`
	Intent   string `json:"intent"`
}

// handleConversation processes conversational requests through the LLM.
// When the LLM provider is unavailable, known meta queries (greetings,
// status, health) fall back to deterministic replies so the orchestrator
// remains minimally responsive without an LLM.
func (o *Orchestrator) handleConversation(ctx context.Context, req *guide.ForwardedRequest) (any, error) {
	cr := o.buildConversationRequest(req)

	// Snapshot provider under lock — SetProvider may write concurrently.
	o.mu.RLock()
	provider := o.provider
	o.mu.RUnlock()

	o.logInfo("handleConversation",
		"has_provider", provider != nil,
		"input_len", len(req.Input))

	// Prefer LLM for all conversations — natural, context-aware responses.
	if provider != nil {
		return o.executeConversationLLM(ctx, cr)
	}

	// Fallback: deterministic response for known meta queries when no LLM.
	if reply, ok := tryStaticOrchestratorReply(cr); ok {
		o.logInfo("handleConversation: static reply used")
		o.publishStreamChunk(ctx, reply)
		return &ConversationResult{
			Response: reply,
			Intent:   "chat",
		}, nil
	}

	o.logWarnMsg("handleConversation: no provider and no static match")
	return nil, fmt.Errorf("orchestrator: LLM provider not available — authorize Google credentials to enable")
}

// executeConversationLLM sends the conversation request to the LLM with
// the orchestrator system prompt, read-only tools, and tool loop.
// Split from executeConversation so callers (e.g. respondToIngestion) can
// bypass the static fast-path when the query must always reach the LLM.
func (o *Orchestrator) executeConversationLLM(ctx context.Context, cr orchestratorConversationRequest) (*ConversationResult, error) {
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

// respondToIngestion returns a deterministic ack immediately (unblocking the
// architect's routeSyncTimeout) and then generates a richer LLM summary
// asynchronously, pushing it to the TUI via a notification once the
// architect's stream has completed.
//
// Split into immediate + deferred:
//  1. Immediate: deterministic ack returned via RouteResponse (~1s). The
//     architect's tool loop includes this text in its own stream to the TUI.
//  2. Deferred: LLM summary generated in a tracked goroutine. By the time
//     the LLM responds (10-30s), the architect's stream has completed, so
//     the notification push won't corrupt the TUI's single accumulator.
func (o *Orchestrator) respondToIngestion(
	ctx context.Context,
	req *guide.ForwardedRequest,
	ingestionResult any,
) (any, error) {
	o.logInfo("respondToIngestion", "correlation_id", req.CorrelationID)

	preAck := buildDeterministicIngestionAck(ingestionResult)

	// Publish stream chunk so the architect's sync waiter sees activity.
	o.publishStreamChunk(ctx, preAck)

	// Snapshot provider under lock — SetProvider may write concurrently.
	o.mu.RLock()
	provider := o.provider
	o.mu.RUnlock()

	// Spawn async LLM summary if a provider is available. The goroutine is
	// tracked via o.scope so it participates in graceful shutdown.
	if provider != nil {
		planJSON := req.Input
		o.scope.Go("ingestion-summary", ingestionLLMTimeout+5*time.Second, func(scopeCtx context.Context) error {
			result, err := o.generateIngestionSummary(scopeCtx, planJSON, ingestionResult)
			if err != nil {
				o.logWarnMsg("respondToIngestion: async LLM summary failed", "error", err)
				return nil // Non-fatal — the deterministic ack already reached the user.
			}
			o.logInfo("respondToIngestion: async LLM summary generated",
				"response_len", len(result.Response))
			o.publishNotificationPush(result.Response)
			return nil
		})
	}

	// Return the deterministic ack immediately — unblocks the architect.
	return &ConversationResult{Response: preAck, Intent: "ingestion_ack"}, nil
}

// generateIngestionSummary calls the LLM with the plan details and ingestion
// result to produce a human-readable summary of the orchestrator's
// understanding of the plan.
func (o *Orchestrator) generateIngestionSummary(
	ctx context.Context,
	planJSON string,
	ingestionResult any,
) (*ConversationResult, error) {
	prompt := buildIngestionSummaryPrompt(planJSON, ingestionResult)
	systemPrompt := OrchestratorConversationSystemPrompt()

	temp := float64(conversationTemp)
	llmReq := &providers.Request{
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: prompt},
		},
		SystemPrompt: systemPrompt,
		Model:        o.config.Model,
		MaxTokens:    conversationMaxTokens,
		Temperature:  &temp,
	}

	llmCtx, cancel := context.WithTimeout(ctx, ingestionLLMTimeout)
	defer cancel()
	llmCtx = providers.WithRetryObserver(llmCtx, o.retryObserver())

	resp, err := o.provider.Complete(llmCtx, llmReq)
	if err != nil {
		return nil, fmt.Errorf("ingestion summary LLM: %w", err)
	}

	trimmed := strings.TrimSpace(resp.Content)
	if trimmed == "" {
		return nil, fmt.Errorf("ingestion summary: empty LLM response")
	}

	accumulateOrchestratorUsage(ctx, &resp.Usage)

	return &ConversationResult{
		Response: trimmed,
		Intent:   "ingestion_ack",
	}, nil
}

// buildIngestionSummaryPrompt composes the user message for the LLM to
// summarize an ingested plan.
func buildIngestionSummaryPrompt(planJSON string, ingestionResult any) string {
	var b strings.Builder

	b.WriteString("A plan has been ingested and a DAG has been created for execution.\n\n")

	if resultMap, ok := ingestionResult.(map[string]any); ok {
		planID, _ := resultMap["plan_id"].(string)
		dagID, _ := resultMap["dag_id"].(string)
		taskCount, _ := resultMap["task_count"].(int)
		layerCount, _ := resultMap["layer_count"].(int)
		b.WriteString(fmt.Sprintf("## Ingestion Result\n- Plan ID: %s\n- DAG ID: %s\n- Tasks: %d\n- Execution Layers: %d\n\n",
			planID, dagID, taskCount, layerCount))
	}

	var handoff architect.PlanHandoff
	if err := json.Unmarshal([]byte(planJSON), &handoff); err == nil {
		b.WriteString("## Plan Details\n")
		b.WriteString(fmt.Sprintf("**Original Request:** %s\n\n", handoff.Query))

		if handoff.Architecture != nil {
			if handoff.Architecture.Name != "" {
				b.WriteString(fmt.Sprintf("**Architecture:** %s\n", handoff.Architecture.Name))
			}
			if len(handoff.Architecture.Patterns) > 0 {
				b.WriteString(fmt.Sprintf("**Patterns:** %s\n", strings.Join(handoff.Architecture.Patterns, ", ")))
			}
			if len(handoff.Architecture.Components) > 0 {
				b.WriteString("**Components:**\n")
				for _, c := range handoff.Architecture.Components {
					b.WriteString(fmt.Sprintf("- %s: %s\n", c.Name, c.Description))
				}
			}
		}

		if len(handoff.Tasks) > 0 {
			b.WriteString("\n**Tasks:**\n")
			for _, t := range handoff.Tasks {
				b.WriteString(fmt.Sprintf("- [%s] %s (%s, %s complexity)\n",
					t.ID, t.Name, t.AgentType, t.Complexity))
			}
		}

		if len(handoff.ExecutionLayers) > 0 {
			b.WriteString("\n**Execution Order:**\n")
			for i, layer := range handoff.ExecutionLayers {
				b.WriteString(fmt.Sprintf("  Layer %d: %s\n", i+1, strings.Join(layer, ", ")))
			}
		}

		if len(handoff.CriticalPath) > 0 {
			b.WriteString(fmt.Sprintf("\n**Critical Path:** %s\n", strings.Join(handoff.CriticalPath, " → ")))
		}

		if len(handoff.RiskSummary) > 0 {
			b.WriteString("\n**Risks:**\n")
			for _, r := range handoff.RiskSummary {
				b.WriteString(fmt.Sprintf("- %s\n", r))
			}
		}

		if len(handoff.Assumptions) > 0 {
			b.WriteString("\n**Assumptions:**\n")
			for _, a := range handoff.Assumptions {
				b.WriteString(fmt.Sprintf("- %s\n", a))
			}
		}
	}

	b.WriteString("\n---\n\n")
	b.WriteString("Summarize this plan for the user. Include:\n")
	b.WriteString("1. What will be built (the original request)\n")
	b.WriteString("2. How the work is organized (task breakdown, execution layers, parallelism)\n")
	b.WriteString("3. Key architectural decisions\n")
	b.WriteString("4. Any risks or assumptions worth noting\n")
	b.WriteString("5. Confirm that the DAG is now executing\n\n")
	b.WriteString("Be concise but informative. Use markdown formatting.")

	return b.String()
}

// buildDeterministicIngestionAck produces a structured acknowledgment
// from the ingestion result without requiring an LLM provider.
func buildDeterministicIngestionAck(ingestionResult any) string {
	resultMap, ok := ingestionResult.(map[string]any)
	if !ok {
		return "Plan ingested. DAG execution started."
	}
	planID, _ := resultMap["plan_id"].(string)
	dagID, _ := resultMap["dag_id"].(string)
	taskCount, _ := resultMap["task_count"].(int)
	layerCount, _ := resultMap["layer_count"].(int)

	return fmt.Sprintf(
		"Plan %s ingested. DAG %s started with %d tasks across %d layers.",
		planID, dagID, taskCount, layerCount,
	)
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
