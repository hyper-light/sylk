package librarian

import (
	"strings"

	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
)

func (l *Librarian) applyConversationRuntimeProfile(req *providers.Request, sessionID string) {
	llmruntime.ApplyStage(req, l.conversationStageProfile(), llmruntime.ApplyOptions{
		Model:     librarianRuntimeModel(req, l.CurrentModel()),
		MaxTokens: req.MaxTokens,
		AgentID:   strings.TrimSpace(l.id),
		SessionID: librarianRuntimeSessionID(req, sessionID, l.config.SessionID),
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

func librarianRuntimeSessionID(req *providers.Request, sessionID, fallback string) string {
	if trimmed := strings.TrimSpace(sessionID); trimmed != "" {
		return trimmed
	}
	if req != nil && req.Metadata != nil {
		if trimmed, _ := req.Metadata["session_id"].(string); strings.TrimSpace(trimmed) != "" {
			return strings.TrimSpace(trimmed)
		}
	}
	return strings.TrimSpace(fallback)
}
