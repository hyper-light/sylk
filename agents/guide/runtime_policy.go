package guide

import (
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
)

func (g *Guide) applyPlanAcceptanceRuntimeProfile(req *providers.Request) {
	llmruntime.ApplyStage(req, g.planAcceptanceStageProfile(), llmruntime.ApplyOptions{
		Model:     guideRuntimeModel(req, g.CurrentModel()),
		MaxTokens: req.MaxTokens,
		AgentID:   "guide",
		SessionID: g.sessionID,
	})
}

func (g *Guide) planAcceptanceStageProfile() llmruntime.StageProfile {
	stage := llmruntime.ResolveAgentStageProfile("guide", "plan_acceptance")
	stage.RequestProfile.ReasoningEffort = resolveClassifierReasoningEffort(g.config.RouterConfig.ThinkingLevel)
	return stage
}

func guideRuntimeProfiles() []llmruntime.StageProfile {
	return llmruntime.AgentProfiles("guide")
}

func guideDefaultRuntimeProfile() string {
	return llmruntime.AgentDefaultProfile("guide")
}

func guideClassifierStageProfile(thinkingLevel string) llmruntime.StageProfile {
	stage := llmruntime.ResolveAgentStageProfile("guide", "classifier")
	stage.RequestProfile.ReasoningEffort = resolveClassifierReasoningEffort(thinkingLevel)
	return stage
}

func guideSelfResponseStageProfile(reasoningEffort string) llmruntime.StageProfile {
	stage := llmruntime.ResolveAgentStageProfile("guide", "self_response")
	stage.RequestProfile.ReasoningEffort = reasoningEffort
	return stage
}

func guideRuntimeModel(req *providers.Request, fallback string) string {
	if req != nil && req.Model != "" {
		return req.Model
	}
	return fallback
}
