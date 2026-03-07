package academic

import (
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
)

// Academic currently relies on provider defaults for runtime tuning. Keep the
// selection logic here so future policy changes stay agent-owned.
func (a *Academic) applyLLMRuntimeProfile(req *providers.Request, stage string) {
	llmruntime.Apply(req, a.llmRuntimeProfile(stage))
}

func (a *Academic) llmRuntimeProfile(stage string) llmruntime.Profile {
	switch stage {
	case "fetch", "recall", "check", "research":
		return llmruntime.Profile{}
	default:
		return llmruntime.Profile{}
	}
}
