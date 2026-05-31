package architect

import (
	"context"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
)

// injectForestPreload prepends architect-lane forest projections
// (Intent + Constraint + Decision) to the LLM system prompt so the
// planner enters its tool loop with memory context as framing rather
// than as a mid-loop skill call. MEM-01.
func (a *Architect) injectForestPreload(ctx context.Context, req *providers.Request, query, sessionID string) context.Context {
	if req == nil || a.config.Forest == nil {
		return ctx
	}
	ctx, _, err := shared.ApplyForestPreload(ctx, req, a.config.Forest, shared.ForestPreloadInput{
		AgentType: "architect",
		Query:     query,
		SessionID: sessionID,
	})
	if err != nil {
		return ctx
	}
	return ctx
}

func (a *Architect) applyConversationRuntimeProfile(req *providers.Request, mode plannerConversationMode, sessionID string) {
	llmruntime.ApplyStage(req, a.conversationStageProfile(mode), llmruntime.ApplyOptions{
		Model:     architectRuntimeModel(req, a.config.Model),
		MaxTokens: req.MaxTokens,
		AgentID:   "architect",
		SessionID: sessionID,
	})
}

func (a *Architect) conversationStageProfile(mode plannerConversationMode) llmruntime.StageProfile {
	stage := llmruntime.ResolveAgentStageProfile("architect", "conversation_"+string(mode))
	stage.RequestProfile.ThinkingBudget = llmruntime.Int(0)
	return stage
}

func (a *Architect) applyProtocolRuntimeProfile(req *providers.Request, sessionID string) {
	llmruntime.ApplyStage(req, a.protocolStageProfile(), llmruntime.ApplyOptions{
		Model:     architectRuntimeModel(req, a.config.Model),
		MaxTokens: req.MaxTokens,
		AgentID:   "architect",
		SessionID: sessionID,
	})
}

func (a *Architect) protocolStageProfile() llmruntime.StageProfile {
	return llmruntime.ResolveAgentStageProfile("architect", "planning_protocol")
}

func (p *anthropicPlanner) applyStreamingRuntimeProfile(req *providers.Request, stage string, thinkingBudget int, sessionID string) {
	llmruntime.ApplyStage(req, p.streamingStageProfile(stage, thinkingBudget), llmruntime.ApplyOptions{
		Model:     architectRuntimeModel(req, ""),
		MaxTokens: req.MaxTokens,
		AgentID:   "architect",
		SessionID: sessionID,
	})
}

func (p *anthropicPlanner) streamingStageProfile(stageName string, thinkingBudget int) llmruntime.StageProfile {
	stage := llmruntime.ResolveAgentStageProfile("architect", stageName)
	stage.RequestProfile.ThinkingBudget = llmruntime.Int(thinkingBudget)
	return stage
}

func architectRuntimeProfiles() []llmruntime.StageProfile {
	return llmruntime.AgentProfiles("architect")
}

func architectDefaultRuntimeProfile() string {
	return llmruntime.AgentDefaultProfile("architect")
}

func architectRuntimeModel(req *providers.Request, fallback string) string {
	if req != nil && req.Model != "" {
		return req.Model
	}
	return fallback
}
