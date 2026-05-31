package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/architect"
	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
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

// ResponseText implements the guide-layer responseTexter interface so the
// conversation history stores the clean response string, not a JSON blob.
func (r *ConversationResult) ResponseText() string {
	if r == nil {
		return ""
	}
	return r.Response
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
	o.prepareSkillsForInput(userMessage)
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
	o.applyConversationRuntimeProfile(llmReq)
	ctx, _, _ = shared.ApplyForestPreload(ctx, llmReq, o.config.Forest, shared.ForestPreloadInput{
		AgentType: "orchestrator",
		Query:     cr.UserQuery,
		SessionID: cr.SessionID,
		AgentID:   o.config.AgentID,
	})
	if len(tools) > 0 {
		llmReq.ToolChoice = "auto"
	}

	// Prepend conversation history as multi-turn message pairs.
	shared.PrependHistoryMessages(llmReq, cr.History)

	llmCtx, cancel := context.WithTimeout(ctx, o.config.LLMTimeout)
	defer cancel()
	llmCtx = providers.WithRetryObserver(llmCtx, o.retryObserver())

	ledger := shared.SteeringLedgerFromContext(llmCtx)
	// User-facing conversation turn — caller awaits the response
	// synchronously. Don't stamp WithContinuationStore: yielding would
	// strand the user with an empty answer while the resume runs in
	// the background. Consult_peer falls back to the inline RouteSync
	// wait so the loop completes before we return.
	loopCtx := llmCtx
	response, err := shared.ExecuteTurnLoop(ctx, ledger, llmReq, func() (string, error) {
		return o.executeToolLoop(loopCtx, llmReq, ledger)
	})
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

// respondToIngestion returns a deterministic ack immediately so the architect
// can continue without waiting on a second summarization pass. Fresh handoffs
// may already have streamed a pre-kickoff receipt summary from the ingest path;
// this method supplies the matching deterministic route result. We avoid a
// deferred notification summary here because a late out-of-band push can land
// after execution has already started, producing confusing chat ordering.
func (o *Orchestrator) respondToIngestion(
	ctx context.Context,
	req *guide.ForwardedRequest,
	ingestionResult any,
) (any, error) {
	o.logInfo("respondToIngestion", "correlation_id", req.CorrelationID)

	preAck := buildDeterministicIngestionAck(ingestionResult)

	// Fresh handoffs stream a receipt summary before kickoff from the ingest
	// path itself. Duplicate/non-execution paths still need a visible chunk.
	if !ingestionReceiptAlreadyStreamed(ingestionResult) {
		o.publishStreamChunk(ctx, preAck)
	}

	// Return the deterministic ack immediately — unblocks the architect.
	return &ConversationResult{Response: preAck, Intent: "ingestion_ack"}, nil
}

func ingestionReceiptAlreadyStreamed(ingestionResult any) bool {
	resultMap, ok := ingestionResult.(map[string]any)
	if !ok {
		return false
	}
	streamed, _ := resultMap["receipt_streamed"].(bool)
	return streamed
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
	o.applyConversationRuntimeProfile(llmReq)
	ctx, _, _ = shared.ApplyForestPreload(ctx, llmReq, o.config.Forest, shared.ForestPreloadInput{
		AgentType: "orchestrator",
		Query:     prompt,
		SessionID: o.config.SessionID,
		AgentID:   o.config.AgentID,
	})

	llmCtx, cancel := context.WithTimeout(ctx, ingestionLLMTimeout)
	defer cancel()
	llmCtx = providers.WithRetryObserver(llmCtx, o.retryObserver())

	shared.LogLLMCallStartedFromContext(llmCtx, llmReq)
	llmStart := time.Now()
	resp, err := o.provider.Complete(llmCtx, llmReq)
	shared.LogLLMCallFromContext(llmCtx, llmReq.Model, resp, time.Since(llmStart), err)
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
	duplicate, _ := resultMap["duplicate"].(bool)
	receiptStatus, _ := resultMap["receipt_status"].(string)

	if duplicate {
		if dagID != "" {
			return fmt.Sprintf(
				"Plan %s is already in handoff state %s on DAG %s.",
				planID, firstNonEmpty(receiptStatus, "running"), dagID,
			)
		}
		return fmt.Sprintf(
			"Plan %s is already in handoff state %s.",
			planID, firstNonEmpty(receiptStatus, "accepted"),
		)
	}
	if dagID == "" {
		return fmt.Sprintf(
			"Plan %s handoff recorded with status %s.",
			planID, firstNonEmpty(receiptStatus, "accepted"),
		)
	}

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
