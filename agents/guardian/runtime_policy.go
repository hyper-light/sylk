package guardian

import (
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
)

func (g *Guardian) applyConversationRuntimeProfile(req *providers.Request, intent GuardianIntent, sessionID string) {
	llmruntime.ApplyStage(req, g.conversationStageProfile(intent), llmruntime.ApplyOptions{
		Model:     guardianRuntimeModel(req, g.CurrentModel()),
		MaxTokens: req.MaxTokens,
		AgentID:   "guardian",
		SessionID: sessionID,
	})
}

func (g *Guardian) conversationStageProfile(intent GuardianIntent) llmruntime.StageProfile {
	switch intent {
	case IntentReport:
		return llmruntime.ResolveAgentStageProfile("guardian", "conversation_report")
	case IntentAssess:
		return llmruntime.ResolveAgentStageProfile("guardian", "conversation_assess")
	case IntentCheckpoint:
		return llmruntime.ResolveAgentStageProfile("guardian", "conversation_checkpoint")
	default:
		return llmruntime.ResolveAgentStageProfile("guardian", "conversation_chat")
	}
}

func guardianRuntimeProfiles() []llmruntime.StageProfile {
	return llmruntime.AgentProfiles("guardian")
}

func guardianDefaultRuntimeProfile() string {
	return llmruntime.AgentDefaultProfile("guardian")
}

func guardianRuntimeModel(req *providers.Request, fallback string) string {
	if req != nil && req.Model != "" {
		return req.Model
	}
	return fallback
}
