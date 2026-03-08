package global

import (
	"strings"

	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
)

func (gt *GlobalTester) applyLLMRuntimeProfile(req *providers.Request, stageName string) {
	llmruntime.ApplyStage(req, gt.llmStageProfile(stageName), llmruntime.ApplyOptions{
		Model:     testerRuntimeModel(req, gt.CurrentModel()),
		MaxTokens: req.MaxTokens,
		AgentID:   "tester",
		SessionID: gt.config.SessionID,
	})
}

func (gt *GlobalTester) llmStageProfile(stageName string) llmruntime.StageProfile {
	stage := llmruntime.ResolveAgentStageProfile("tester", stageName)
	if effort := strings.TrimSpace(gt.config.ReasoningEffort); effort != "" {
		stage.RequestProfile.ReasoningEffort = effort
	}
	return stage
}

func testerRuntimeProfiles() []llmruntime.StageProfile {
	return llmruntime.AgentProfiles("tester")
}

func testerDefaultRuntimeProfile() string {
	return llmruntime.AgentDefaultProfile("tester")
}

func testerRuntimeModel(req *providers.Request, fallback string) string {
	if req != nil && req.Model != "" {
		return req.Model
	}
	return fallback
}
