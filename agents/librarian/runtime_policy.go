package librarian

import (
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
)

func (l *Librarian) applyConversationRuntimeProfile(req *providers.Request) {
	llmruntime.ApplyStage(req, l.conversationStageProfile(), llmruntime.ApplyOptions{
		Model:     librarianRuntimeModel(req, l.CurrentModel()),
		MaxTokens: req.MaxTokens,
		AgentID:   "librarian",
		SessionID: l.config.SessionID,
	})
}

func (l *Librarian) conversationStageProfile() llmruntime.StageProfile {
	return llmruntime.ResolveAgentStageProfile("librarian", "search")
}

func librarianRuntimeProfiles() []llmruntime.StageProfile {
	return llmruntime.AgentProfiles("librarian")
}

func librarianDefaultRuntimeProfile() string {
	return llmruntime.AgentDefaultProfile("librarian")
}

func librarianRuntimeModel(req *providers.Request, fallback string) string {
	if req != nil && req.Model != "" {
		return req.Model
	}
	return fallback
}
