package engineer

import (
	"strings"

	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
)

func (e *Engineer) applyLLMRuntimeProfile(req *providers.Request, stageName string) {
	llmruntime.ApplyStage(req, e.llmStageProfile(stageName), llmruntime.ApplyOptions{
		Model:     engineerRuntimeModel(req, e.CurrentModel()),
		MaxTokens: req.MaxTokens,
		AgentID:   "engineer",
		SessionID: e.config.SessionID,
	})
}

func (e *Engineer) llmStageProfile(stageName string) llmruntime.StageProfile {
	stage := llmruntime.ResolveAgentStageProfile("engineer", stageName)
	if effort := strings.TrimSpace(e.config.EngineerConfig.ReasoningEffort); effort != "" {
		stage.RequestProfile.ReasoningEffort = effort
	}
	return stage
}

func engineerRuntimeProfiles() []llmruntime.StageProfile {
	return llmruntime.AgentProfiles("engineer")
}

func engineerDefaultRuntimeProfile() string {
	return llmruntime.AgentDefaultProfile("engineer")
}

func engineerRuntimeModel(req *providers.Request, fallback string) string {
	if req != nil && req.Model != "" {
		return req.Model
	}
	return fallback
}
