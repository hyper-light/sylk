package scribe

import (
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
)

func (s *Scribe) applyCommentaryRuntimeProfile(req *providers.Request) {
	llmruntime.ApplyStage(req, s.commentaryStageProfile(), llmruntime.ApplyOptions{
		Model:     scribeRuntimeModel(req, s.model),
		MaxTokens: req.MaxTokens,
		AgentID:   "scribe",
		SessionID: "",
	})
}

func (s *Scribe) commentaryStageProfile() llmruntime.StageProfile {
	return llmruntime.ResolveAgentStageProfile("scribe", "commentary")
}

func scribeRuntimeProfiles() []llmruntime.StageProfile {
	return llmruntime.AgentProfiles("scribe")
}

func scribeDefaultRuntimeProfile() string {
	return llmruntime.AgentDefaultProfile("scribe")
}

func scribeRuntimeModel(req *providers.Request, fallback string) string {
	if req != nil && req.Model != "" {
		return req.Model
	}
	return fallback
}
