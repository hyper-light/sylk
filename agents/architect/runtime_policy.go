package architect

import (
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
)

func (a *Architect) applyConversationRuntimeProfile(req *providers.Request, mode plannerConversationMode) {
	llmruntime.Apply(req, a.conversationRuntimeProfile(mode))
}

func (a *Architect) conversationRuntimeProfile(mode plannerConversationMode) llmruntime.Profile {
	switch mode {
	case plannerConversationModeClarification, plannerConversationModeReady, plannerConversationModeConverse, plannerConversationModeFeedback:
		return llmruntime.Profile{ThinkingBudget: llmruntime.Int(0)}
	default:
		return llmruntime.Profile{ThinkingBudget: llmruntime.Int(0)}
	}
}

func (a *Architect) applyProtocolRuntimeProfile(req *providers.Request) {
	llmruntime.Apply(req, a.protocolRuntimeProfile())
}

func (a *Architect) protocolRuntimeProfile() llmruntime.Profile {
	return llmruntime.Profile{}
}

func (p *anthropicPlanner) applyStreamingRuntimeProfile(req *providers.Request, thinkingBudget int) {
	llmruntime.Apply(req, p.streamingRuntimeProfile(thinkingBudget))
}

func (p *anthropicPlanner) streamingRuntimeProfile(thinkingBudget int) llmruntime.Profile {
	return llmruntime.Profile{ThinkingBudget: llmruntime.Int(thinkingBudget)}
}
