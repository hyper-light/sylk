package scribe

import (
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
)

// Scribe commentary generation currently relies on provider defaults.
func (s *Scribe) applyCommentaryRuntimeProfile(req *providers.Request) {
	llmruntime.Apply(req, s.commentaryRuntimeProfile())
}

func (s *Scribe) commentaryRuntimeProfile() llmruntime.Profile {
	return llmruntime.Profile{}
}
