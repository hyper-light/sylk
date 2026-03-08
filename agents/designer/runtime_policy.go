package designer

import (
	"strings"

	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
)

func (d *Designer) applyDesignRuntimeProfile(req *providers.Request) {
	llmruntime.ApplyStage(req, d.designStageProfile(), llmruntime.ApplyOptions{
		Model:     designerRuntimeModel(req, d.CurrentModel()),
		MaxTokens: req.MaxTokens,
		AgentID:   "designer",
		SessionID: d.config.SessionID,
	})
}

func (d *Designer) designStageProfile() llmruntime.StageProfile {
	stage := llmruntime.ResolveAgentStageProfile("designer", "design")
	if effort := strings.TrimSpace(d.config.DesignerConfig.ReasoningEffort); effort != "" {
		stage.RequestProfile.ReasoningEffort = effort
	}
	return stage
}

func designerRuntimeProfiles() []llmruntime.StageProfile {
	return llmruntime.AgentProfiles("designer")
}

func designerDefaultRuntimeProfile() string {
	return llmruntime.AgentDefaultProfile("designer")
}

func designerRuntimeModel(req *providers.Request, fallback string) string {
	if req != nil && req.Model != "" {
		return req.Model
	}
	return fallback
}
