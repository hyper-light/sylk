package pipeline

import (
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
)

// Pipeline Inspector keeps runtime policy selection in the agent package even
// when the current stages rely on provider defaults.
func (pi *PipelineInspector) applyLLMRuntimeProfile(req *providers.Request, stage string) {
	llmruntime.Apply(req, pi.llmRuntimeProfile(stage))
}

func (pi *PipelineInspector) llmRuntimeProfile(stage string) llmruntime.Profile {
	switch stage {
	case "task", "validation", "revalidation":
		return llmruntime.Profile{}
	default:
		return llmruntime.Profile{}
	}
}
