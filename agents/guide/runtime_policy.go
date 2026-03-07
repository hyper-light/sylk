package guide

import (
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
)

func (g *Guide) applyPlanAcceptanceRuntimeProfile(req *providers.Request) {
	llmruntime.Apply(req, g.planAcceptanceRuntimeProfile())
}

func (g *Guide) planAcceptanceRuntimeProfile() llmruntime.Profile {
	return llmruntime.Profile{
		ReasoningEffort: resolveClassifierReasoningEffort(g.config.RouterConfig.ThinkingLevel),
	}
}
