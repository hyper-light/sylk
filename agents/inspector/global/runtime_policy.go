package global

import (
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
)

func (gi *GlobalInspector) applyLLMRuntimeProfile(req *providers.Request, stage string) {
	llmruntime.ApplyStage(req, gi.llmStageProfile(stage), llmruntime.ApplyOptions{
		Model:     inspectorRuntimeModel(req, gi.CurrentModel()),
		MaxTokens: req.MaxTokens,
		AgentID:   "inspector",
		SessionID: gi.config.SessionID,
	})
	req.DisableParallelToolUse = true
}

func (gi *GlobalInspector) llmStageProfile(stage string) llmruntime.StageProfile {
	return llmruntime.ResolveAgentStageProfile("inspector", stage)
}

func inspectorRuntimeProfiles() []llmruntime.StageProfile {
	return llmruntime.AgentProfiles("inspector")
}

func inspectorDefaultRuntimeProfile() string {
	return llmruntime.AgentDefaultProfile("inspector")
}

func inspectorRuntimeModel(req *providers.Request, fallback string) string {
	if req != nil && req.Model != "" {
		return req.Model
	}
	return fallback
}
