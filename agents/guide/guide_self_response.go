package guide

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/resources"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/prompts"
)

const (
	defaultGuideResponseTimeout = 90 * time.Second
)

var guideResponseBaseSystemPrompt = prompts.MustLoad("guide", "self_response")
var guideResponseSystemPrompt = buildGuideResponseSystemPrompt()

// GuideResponder provides model-backed responses for guide-targeted prompts.
// Works with any providers.ProviderAdapter via Complete().
type GuideResponder struct {
	provider providers.ProviderAdapter
	model    string
	timeout  time.Duration

	responseTemperature *float64
	responseMaxTokens   int
	reasoningEffort     string

	toolDefs    func(input string) []providers.Tool
	toolInvoker func(ctx context.Context, name string, arguments string) (string, error)
	maxToolRuns int
	eventLogger *agentlog.SessionEventLogger
}

type guideThoughtEmitter func(string)
type guideEarlyUsageEmitter func(inputTokens int)

type guideThoughtEmitterKey struct{}
type guideEarlyUsageEmitterKey struct{}

// NewGuideResponder creates a provider-agnostic self-responder for Guide.
func NewGuideResponder(provider providers.ProviderAdapter, model string, cfg RouterConfig) GuideSelfResponder {
	return &GuideResponder{
		provider:            provider,
		model:               model,
		timeout:             defaultGuideResponseTimeout,
		responseTemperature: resolveResponseTemperature(cfg),
		responseMaxTokens:   resolveResponseMaxTokens(cfg),
		reasoningEffort:     resolveGuideReasoningEffort(cfg.ThinkingLevel),
		maxToolRuns:         6,
	}
}

// NewGuideResponderWithTools enables model-driven guide skill invocation.
func NewGuideResponderWithTools(
	provider providers.ProviderAdapter,
	model string,
	cfg RouterConfig,
	toolDefs func(input string) []providers.Tool,
	toolInvoker func(ctx context.Context, name string, arguments string) (string, error),
) GuideSelfResponder {
	return &GuideResponder{
		provider:            provider,
		model:               model,
		timeout:             defaultGuideResponseTimeout,
		responseTemperature: resolveResponseTemperature(cfg),
		responseMaxTokens:   resolveResponseMaxTokens(cfg),
		reasoningEffort:     resolveGuideReasoningEffort(cfg.ThinkingLevel),
		toolDefs:            toolDefs,
		toolInvoker:         toolInvoker,
		maxToolRuns:         6,
	}
}

// resolveGuideReasoningEffort normalizes the config ThinkingLevel to a
// provider-agnostic ReasoningEffort string (low/medium/high).
func resolveGuideReasoningEffort(thinkingLevel string) string {
	return strings.ToLower(strings.TrimSpace(thinkingLevel))
}

func (r *GuideResponder) Respond(ctx context.Context, request GuideSelfResponseRequest) (string, error) {
	text, _, err := r.respondInternal(ctx, request, nil)
	if err != nil {
		return "", err
	}
	return text, nil
}

func (r *GuideResponder) RespondStream(
	ctx context.Context,
	request GuideSelfResponseRequest,
	onChunk func(string) error,
) (string, *providers.Usage, error) {
	return r.respondInternal(ctx, request, onChunk)
}

func (r *GuideResponder) respondInternal(
	ctx context.Context,
	request GuideSelfResponseRequest,
	onChunk func(string) error,
) (string, *providers.Usage, error) {
	if r == nil || r.provider == nil {
		return "", nil, fmt.Errorf("guide responder is not configured")
	}
	propagator := &resources.DeadlinePropagator{CleanupBuffer: 2 * time.Second}
	guardedCtx, cancel := propagator.Propagate(ctx, r.timeout)
	defer cancel()

	req := r.buildRequest(request)
	emitGuideEarlyUsage(guardedCtx, estimateGuidePromptInputTokens(req))

	seen := make(map[guideToolCallSignature]int, r.maxToolRuns)
	consecutiveErrors := 0

	for turn := range r.maxToolRuns + 1 {
		// NOTE: Steering checkpoint integration deferred for guide self-response
		// due to agents/shared ↔ agents/guide import cycle. The guide's tool loop
		// is stateless (no ledger), so DrainAndCheckpoint would be a no-op.
		// When guide gets its own ledger, use core/steering directly here.

		turnStart := time.Now()
		resp, err := r.provider.Complete(guardedCtx, req)
		logClassifierLLMCall(r.eventLogger, r.model, resp, time.Since(turnStart), err)
		if err != nil {
			return "", nil, fmt.Errorf("guide model %s: %w", r.model, err)
		}
		if llmruntime.EmitsThoughts(req) {
			emitGuideThought(guardedCtx, resp.Thinking)
		}

		if len(resp.ToolCalls) == 0 {
			text := strings.TrimSpace(resp.Content)
			if text == "" {
				return "", nil, fmt.Errorf("guide model %s returned empty response", r.model)
			}
			if onChunk != nil {
				if err := onChunk(text); err != nil {
					return "", nil, err
				}
			}
			return text, &resp.Usage, nil
		}

		if turn == r.maxToolRuns {
			return "", nil, fmt.Errorf("guide model %s exceeded tool-call limit (%d)", r.model, r.maxToolRuns)
		}

		if dup, sig := detectGuideToolCallDuplicate(resp.ToolCalls, seen); dup {
			return "", nil, fmt.Errorf("guide model %s repeated identical tool call: %s", r.model, sig.Name)
		}

		errCount, rerouted := r.applyToolCallsTracked(guardedCtx, req, resp)
		if rerouted {
			return "", nil, skills.ErrRerouteRequested
		}
		consecutiveErrors = updateConsecutiveToolErrors(consecutiveErrors, errCount, len(resp.ToolCalls))
		if consecutiveErrors >= 2 {
			return "", nil, fmt.Errorf("guide model %s: tool calls failed %d consecutive turns", r.model, consecutiveErrors)
		}
	}

	return "", nil, fmt.Errorf("guide model %s exhausted tool-call loop", r.model)
}

// guideToolCallSignature identifies a unique tool invocation by name and arguments.
type guideToolCallSignature struct {
	Name      string
	Arguments string
}

// detectGuideToolCallDuplicate returns true if every tool call in the batch has
// been seen before with identical arguments. Single repeated calls and fully
// repeated multi-call batches are both caught.
func detectGuideToolCallDuplicate(calls []providers.ToolCall, seen map[guideToolCallSignature]int) (bool, guideToolCallSignature) {
	batch := make([]guideToolCallSignature, 0, len(calls))
	for _, call := range calls {
		sig := guideToolCallSignature{
			Name:      strings.TrimSpace(call.Name),
			Arguments: strings.TrimSpace(call.Arguments),
		}
		batch = append(batch, sig)
	}

	allDup := true
	var firstDup guideToolCallSignature
	for _, sig := range batch {
		seen[sig]++
		if seen[sig] <= 1 {
			allDup = false
		} else if firstDup.Name == "" {
			firstDup = sig
		}
	}
	return allDup, firstDup
}

// updateConsecutiveToolErrors tracks consecutive turns where every tool call
// in the batch returned an error. Resets to zero on any turn with a success.
func updateConsecutiveToolErrors(current int, errCount int, totalCalls int) int {
	if totalCalls > 0 && errCount == totalCalls {
		return current + 1
	}
	return 0
}

func (r *GuideResponder) buildRequest(request GuideSelfResponseRequest) *providers.Request {
	prompt := buildGuideResponsePrompt(request)

	messages := make([]providers.Message, 0, len(request.ConversationHistory)+1)
	messages = append(messages, request.ConversationHistory...)
	messages = append(messages, providers.Message{Role: providers.RoleUser, Content: prompt})

	req := &providers.Request{
		Messages:     messages,
		Model:        r.model,
		SystemPrompt: guideResponseSystemPrompt,
		MaxTokens:    r.responseMaxTokens,
		Temperature:  r.responseTemperature,
	}
	llmruntime.ApplyStage(req, guideSelfResponseStageProfile(r.reasoningEffort), llmruntime.ApplyOptions{
		Model:     guideRuntimeModel(req, r.model),
		MaxTokens: req.MaxTokens,
		AgentID:   "guide",
		SessionID: request.SessionID,
	})
	if r.toolDefs != nil {
		req.Tools = r.toolDefs(request.Input)
		req.ToolChoice = "auto"
	}
	return req
}

// applyToolCallsTracked executes tool calls and appends results to the request.
// Returns the error count and whether a reroute was requested.
func (r *GuideResponder) applyToolCallsTracked(
	ctx context.Context,
	req *providers.Request,
	resp *providers.Response,
) (int, bool) {
	if req == nil || resp == nil || len(resp.ToolCalls) == 0 {
		return 0, false
	}

	req.Messages = append(req.Messages, providers.Message{
		Role:      providers.RoleAssistant,
		Content:   strings.TrimSpace(resp.Content),
		ToolCalls: resp.ToolCalls,
		Metadata:  resp.ProviderMetadata,
	})

	errCount := 0
	rerouted := false
	for _, call := range resp.ToolCalls {
		result, err := executeGuideToolCall(ctx, call, r.toolInvoker)
		isError := false
		if err != nil {
			if errors.Is(err, skills.ErrRerouteRequested) {
				rerouted = true
				result = `{"rerouted": true}`
			} else {
				result = guideToolErrorPayload(err)
				isError = true
				errCount++
			}
		}
		req.Messages = append(req.Messages, providers.Message{
			Role:       providers.RoleTool,
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Content:    result,
			IsError:    isError,
		})
	}
	return errCount, rerouted
}

func executeGuideToolCall(
	ctx context.Context,
	call providers.ToolCall,
	toolInvoker func(context.Context, string, string) (string, error),
) (string, error) {
	if toolInvoker == nil {
		return "", fmt.Errorf("guide tool runtime is not configured")
	}
	return toolInvoker(ctx, call.Name, strings.TrimSpace(call.Arguments))
}

func guideToolErrorPayload(err error) string {
	if err == nil {
		return ""
	}
	payload, marshalErr := json.Marshal(map[string]any{
		"error": strings.TrimSpace(err.Error()),
	})
	if marshalErr != nil {
		return `{"error":"tool execution failed"}`
	}
	return string(payload)
}

func estimateGuidePromptInputTokens(req *providers.Request) int {
	if req == nil {
		return 0
	}
	totalChars := len(strings.TrimSpace(req.SystemPrompt))
	for _, msg := range req.Messages {
		totalChars += len(strings.TrimSpace(msg.Content))
	}
	if totalChars == 0 {
		return 0
	}
	return maxGuidePromptInt(totalChars/4, 1)
}

func buildGuideResponseSystemPrompt() string {
	sections := []string{
		strings.TrimSpace(GuideSystemPrompt),
		strings.TrimSpace(guideResponseBaseSystemPrompt),
	}
	nonEmpty := make([]string, 0, len(sections))
	for _, section := range sections {
		if section == "" {
			continue
		}
		nonEmpty = append(nonEmpty, section)
	}
	return strings.Join(nonEmpty, "\n\n---\n\n")
}

func withGuideThoughtEmitter(ctx context.Context, emit guideThoughtEmitter) context.Context {
	if emit == nil {
		return ctx
	}
	return context.WithValue(ctx, guideThoughtEmitterKey{}, emit)
}

func withGuideEarlyUsageEmitter(ctx context.Context, emit guideEarlyUsageEmitter) context.Context {
	if emit == nil {
		return ctx
	}
	return context.WithValue(ctx, guideEarlyUsageEmitterKey{}, emit)
}

func emitGuideThought(ctx context.Context, text string) {
	if ctx == nil {
		return
	}
	emit, ok := ctx.Value(guideThoughtEmitterKey{}).(guideThoughtEmitter)
	if !ok || emit == nil {
		return
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}
	emit(trimmed)
}

func emitGuideEarlyUsage(ctx context.Context, inputTokens int) {
	if ctx == nil || inputTokens <= 0 {
		return
	}
	emit, ok := ctx.Value(guideEarlyUsageEmitterKey{}).(guideEarlyUsageEmitter)
	if !ok || emit == nil {
		return
	}
	emit(inputTokens)
}

func appendThoughtDelta(buffer *strings.Builder, delta string) string {
	if buffer == nil {
		return strings.TrimSpace(delta)
	}
	if delta != "" {
		buffer.WriteString(delta)
	}
	return strings.TrimSpace(buffer.String())
}

func buildGuideResponsePrompt(request GuideSelfResponseRequest) string {
	lines := []string{
		"Runtime context:",
		"- guide_agent_id: " + strings.TrimSpace(request.AgentID),
		"- session_id: " + strings.TrimSpace(request.SessionID),
		"- pending_requests: " + strconv.Itoa(request.PendingRequests),
		"- registered_agents: " + formatGuideAgentsForPrompt(request.RegisteredAgentIDs),
		"- loaded_guide_skills: " + formatGuideLoadedSkillsForPrompt(request.LoadedSkillNames),
	}
	lines = append(lines, guideConversationPromptLines(request)...)
	lines = append(lines, "", "User request:", strings.TrimSpace(request.Input))
	return strings.Join(lines, "\n")
}

func formatGuideAgentsForPrompt(agentIDs []string) string {
	ids := sanitizedGuideAgentIDs(agentIDs)
	if len(ids) == 0 {
		return "(none)"
	}
	return strings.Join(ids, ", ")
}

func formatGuideLoadedSkillsForPrompt(skillNames []string) string {
	cleaned := make([]string, 0, len(skillNames))
	for _, name := range skillNames {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		cleaned = append(cleaned, trimmed)
	}
	if len(cleaned) == 0 {
		return "(none)"
	}
	return strings.Join(cleaned, ", ")
}

const (
	defaultResponseTemperature = 0.7
	defaultResponseMaxTokens   = 4096
)

// resolveResponseTemperature returns the self-response temperature from config,
// falling back to the agent default. Returns a pointer for the provider Request.
func resolveResponseTemperature(cfg RouterConfig) *float64 {
	if cfg.ResponseTemperature != nil {
		return cfg.ResponseTemperature
	}
	value := defaultResponseTemperature
	return &value
}

// resolveResponseMaxTokens returns the self-response max tokens from config,
// falling back to the agent default.
func resolveResponseMaxTokens(cfg RouterConfig) int {
	if cfg.ResponseMaxTokens > 0 {
		return cfg.ResponseMaxTokens
	}
	return defaultResponseMaxTokens
}

func guideConversationPromptLines(request GuideSelfResponseRequest) []string {
	agentID := strings.TrimSpace(request.ActiveConversationAgent)
	if agentID == "" {
		return []string{"- active_conversation_agent: (none)"}
	}
	lines := []string{
		"- active_conversation_agent: " + agentID,
		"- active_conversation_turns: " + strconv.Itoa(maxGuidePromptInt(request.ActiveConversationTurns, 1)),
		"- active_conversation_age_seconds: " + strconv.Itoa(maxGuidePromptInt(request.ActiveConversationAge, 0)),
	}
	if request.ActiveConversationScore > 0 {
		lines = append(lines, fmt.Sprintf("- active_conversation_score: %.2f", request.ActiveConversationScore))
	}
	return lines
}

func maxGuidePromptInt(value int, floor int) int {
	if value < floor {
		return floor
	}
	return value
}
