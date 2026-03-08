package pipeline

import (
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
)

func (pi *PipelineInspector) applyLLMRuntimeProfile(req *providers.Request, stage string) {
	llmruntime.ApplyStage(req, pi.llmStageProfile(stage), llmruntime.ApplyOptions{
		Model:     pipelineInspectorRuntimeModel(req, pi.CurrentModel()),
		MaxTokens: req.MaxTokens,
		AgentID:   "inspector-pipeline",
		SessionID: pi.config.SessionID,
	})
}

func (pi *PipelineInspector) llmStageProfile(stage string) llmruntime.StageProfile {
	return llmruntime.ResolveAgentStageProfile("inspector-pipeline", stage)
}

func pipelineInspectorRuntimeProfiles() []llmruntime.StageProfile {
	return llmruntime.AgentProfiles("inspector-pipeline")
}

func pipelineInspectorDefaultRuntimeProfile() string {
	return llmruntime.AgentDefaultProfile("inspector-pipeline")
}

func pipelineInspectorRuntimeModel(req *providers.Request, fallback string) string {
	if req != nil && req.Model != "" {
		return req.Model
	}
	return fallback
}
