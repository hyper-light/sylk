package orchestrator

import (
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
)

func (o *Orchestrator) applyConversationRuntimeProfile(req *providers.Request) {
	llmruntime.ApplyStage(req, o.conversationStageProfile(), llmruntime.ApplyOptions{
		Model:     orchestratorRuntimeModel(req, o.CurrentModel()),
		MaxTokens: req.MaxTokens,
		AgentID:   "orchestrator",
		SessionID: o.config.SessionID,
	})
}

func (o *Orchestrator) applyBackgroundRuntimeProfile(req *providers.Request) {
	llmruntime.ApplyStage(req, o.backgroundStageProfile(), llmruntime.ApplyOptions{
		Model:     orchestratorRuntimeModel(req, o.CurrentModel()),
		MaxTokens: req.MaxTokens,
		AgentID:   "orchestrator",
		SessionID: o.config.SessionID,
	})
}

func (o *Orchestrator) conversationStageProfile() llmruntime.StageProfile {
	return llmruntime.ResolveAgentStageProfile("orchestrator", "conversation")
}

func (o *Orchestrator) backgroundStageProfile() llmruntime.StageProfile {
	return llmruntime.ResolveAgentStageProfile("orchestrator", "background_loop")
}

func orchestratorRuntimeProfiles() []llmruntime.StageProfile {
	return llmruntime.AgentProfiles("orchestrator")
}

func orchestratorDefaultRuntimeProfile() string {
	return llmruntime.AgentDefaultProfile("orchestrator")
}

func orchestratorRuntimeModel(req *providers.Request, fallback string) string {
	if req != nil && req.Model != "" {
		return req.Model
	}
	return fallback
}
