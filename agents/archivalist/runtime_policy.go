package archivalist

import (
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
)

func (a *Archivalist) applyConversationRuntimeProfile(req *providers.Request) {
	llmruntime.ApplyStage(req, a.conversationStageProfile(), llmruntime.ApplyOptions{
		Model:     archivalistRuntimeModel(req, a.CurrentModel()),
		MaxTokens: req.MaxTokens,
		AgentID:   "archivalist",
		SessionID: a.GetDefaultSession(),
	})
}

func (a *Archivalist) conversationStageProfile() llmruntime.StageProfile {
	return llmruntime.ResolveAgentStageProfile("archivalist", "conversation")
}

func (c *Client) applyGenerationRuntimeProfile(req *providers.Request) {
	llmruntime.ApplyStage(req, c.generationStageProfile(), llmruntime.ApplyOptions{
		Model:     archivalistRuntimeModel(req, c.model),
		MaxTokens: req.MaxTokens,
		AgentID:   "archivalist",
		SessionID: "",
	})
}

func (c *Client) generationStageProfile() llmruntime.StageProfile {
	return llmruntime.ResolveAgentStageProfile("archivalist", "generation")
}

func (s *Synthesizer) applySynthesisRuntimeProfile(req *providers.Request) {
	llmruntime.ApplyStage(req, s.synthesisStageProfile(), llmruntime.ApplyOptions{
		Model:     archivalistRuntimeModel(req, s.model),
		MaxTokens: req.MaxTokens,
		AgentID:   "archivalist",
		SessionID: "",
	})
}

func (s *Synthesizer) synthesisStageProfile() llmruntime.StageProfile {
	return llmruntime.ResolveAgentStageProfile("archivalist", "synthesis")
}

func archivalistRuntimeProfiles() []llmruntime.StageProfile {
	return llmruntime.AgentProfiles("archivalist")
}

func archivalistDefaultRuntimeProfile() string {
	return llmruntime.AgentDefaultProfile("archivalist")
}

func archivalistRuntimeModel(req *providers.Request, fallback string) string {
	if req != nil && req.Model != "" {
		return req.Model
	}
	return fallback
}
