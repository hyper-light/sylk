package librarian

import (
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
)

// Librarian currently uses provider defaults for conversation/tool-loop
// requests. Keeping the profile here makes later tuning agent-owned.
func (l *Librarian) applyConversationRuntimeProfile(req *providers.Request) {
	llmruntime.Apply(req, l.conversationRuntimeProfile())
}

func (l *Librarian) conversationRuntimeProfile() llmruntime.Profile {
	return llmruntime.Profile{}
}
