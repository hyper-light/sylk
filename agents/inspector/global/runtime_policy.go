package global

import (
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
)

// Global Inspector keeps runtime selection at the agent layer even when the
// current policy is just "use provider defaults".
func (gi *GlobalInspector) applyLLMRuntimeProfile(req *providers.Request, stage string) {
	llmruntime.Apply(req, gi.llmRuntimeProfile(stage))
}

func (gi *GlobalInspector) llmRuntimeProfile(stage string) llmruntime.Profile {
	switch stage {
	case "conversation", "task", "audit":
		return llmruntime.Profile{}
	default:
		return llmruntime.Profile{}
	}
}
