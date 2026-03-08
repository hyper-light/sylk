package academic

import (
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
)

func (a *Academic) applyLLMRuntimeProfile(req *providers.Request, stage string) {
	llmruntime.ApplyStage(req, a.llmStageProfile(stage), llmruntime.ApplyOptions{
		Model:     academicRuntimeModel(req, a.CurrentModel()),
		MaxTokens: req.MaxTokens,
		AgentID:   "academic",
		SessionID: a.config.SessionID,
	})
}

func (a *Academic) llmStageProfile(stage string) llmruntime.StageProfile {
	return llmruntime.ResolveAgentStageProfile("academic", stage)
}

func academicRuntimeProfiles() []llmruntime.StageProfile {
	return llmruntime.AgentProfiles("academic")
}

func academicDefaultRuntimeProfile() string {
	return llmruntime.AgentDefaultProfile("academic")
}

func academicRuntimeModel(req *providers.Request, fallback string) string {
	if req != nil && req.Model != "" {
		return req.Model
	}
	return fallback
}
