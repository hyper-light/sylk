package archivalist

import (
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
)

// Archivalist currently leaves runtime tuning to provider defaults for its
// conversational tool-loop path.
func (a *Archivalist) applyConversationRuntimeProfile(req *providers.Request) {
	llmruntime.Apply(req, a.conversationRuntimeProfile())
}

func (a *Archivalist) conversationRuntimeProfile() llmruntime.Profile {
	return llmruntime.Profile{}
}

func (c *Client) applyGenerationRuntimeProfile(req *providers.Request) {
	llmruntime.Apply(req, c.generationRuntimeProfile())
}

func (c *Client) generationRuntimeProfile() llmruntime.Profile {
	return llmruntime.Profile{}
}

func (s *Synthesizer) applySynthesisRuntimeProfile(req *providers.Request) {
	llmruntime.Apply(req, s.synthesisRuntimeProfile())
}

func (s *Synthesizer) synthesisRuntimeProfile() llmruntime.Profile {
	return llmruntime.Profile{}
}
