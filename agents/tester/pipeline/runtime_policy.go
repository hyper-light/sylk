package pipeline

import (
	"strings"

	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
)

func (pt *PipelineTester) applyLLMRuntimeProfile(req *providers.Request, stageName string) {
	llmruntime.ApplyStage(req, pt.llmStageProfile(stageName), llmruntime.ApplyOptions{
		Model:     pipelineTesterRuntimeModel(req, pt.CurrentModel()),
		MaxTokens: req.MaxTokens,
		AgentID:   "tester-pipeline",
		SessionID: pt.config.SessionID,
	})
	req.DisableParallelToolUse = true
}

func (pt *PipelineTester) llmStageProfile(stageName string) llmruntime.StageProfile {
	stage := llmruntime.ResolveAgentStageProfile("tester-pipeline", stageName)
	if effort := strings.TrimSpace(pt.config.ReasoningEffort); effort != "" {
		stage.RequestProfile.ReasoningEffort = effort
	}
	return stage
}

func pipelineTesterRuntimeProfiles() []llmruntime.StageProfile {
	return llmruntime.AgentProfiles("tester-pipeline")
}

func pipelineTesterDefaultRuntimeProfile() string {
	return llmruntime.AgentDefaultProfile("tester-pipeline")
}

func pipelineTesterRuntimeModel(req *providers.Request, fallback string) string {
	if req != nil && req.Model != "" {
		return req.Model
	}
	return fallback
}
