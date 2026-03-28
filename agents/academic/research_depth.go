package academic

import (
	"context"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/llmruntime"
)

type academicResearchDepthContextKey struct{}

func WithAcademicResearchDepth(ctx context.Context, depth shared.ResearchDepth) context.Context {
	if ctx == nil || depth == shared.ResearchDepthUnset {
		return ctx
	}
	return context.WithValue(ctx, academicResearchDepthContextKey{}, depth)
}

func AcademicResearchDepthFromContext(ctx context.Context) shared.ResearchDepth {
	if ctx == nil {
		return shared.ResearchDepthUnset
	}
	depth, _ := ctx.Value(academicResearchDepthContextKey{}).(shared.ResearchDepth)
	return depth
}

func academicResearchDepthFromForwarded(fwd *guide.ForwardedRequest) shared.ResearchDepth {
	if fwd == nil {
		return shared.ResearchDepthUnset
	}
	return shared.ConsultationResearchDepth(fwd.Metadata)
}

func academicResearchDepthFromQuery(query *ResearchQuery) shared.ResearchDepth {
	if query == nil {
		return shared.ResearchDepthUnset
	}
	return shared.NormalizeResearchDepth(query.Depth)
}

func academicResearchDepthPrompt(depth shared.ResearchDepth) string {
	switch depth {
	case shared.ResearchDepthMinimal:
		return "Research depth: minimal. Deliver the fastest acceptable evidence-backed answer. Prefer the narrowest viable search, fetch only what is needed to avoid a weak or misleading conclusion, and stop once the core claim is safely supported with explicit caveats."
	case shared.ResearchDepthQuick:
		return "Research depth: quick. Favor the shortest path to a safe, evidence-backed answer. Use the minimum number of strong sources needed, avoid exhaustive sweeps, and stop once the answer is adequately supported and caveated."
	case shared.ResearchDepthStandard:
		return "Research depth: standard. Do the normal Academic workflow: enough evidence to support the answer well, without expanding into an exhaustive memo unless the question truly demands it."
	case shared.ResearchDepthDeep:
		return "Research depth: deep. Go beyond the default workflow: broaden corroboration, check stronger alternatives and counterevidence, and produce a more decision-grade analysis than a normal consultation."
	case shared.ResearchDepthComprehensive:
		return "Research depth: comprehensive. Widen the search space aggressively, corroborate across multiple evidence classes, actively seek counterevidence and strongest alternatives, and keep digging until you can produce a durable research-grade synthesis rather than a quick recommendation."
	default:
		return ""
	}
}

func academicApplyResearchDepthStageProfile(stage llmruntime.StageProfile, depth shared.ResearchDepth) llmruntime.StageProfile {
	switch depth {
	case shared.ResearchDepthMinimal:
		stage.Deliberation = lowerAcademicDeliberation(lowerAcademicDeliberation(stage.Deliberation))
		switch stage.ResponseDetail {
		case llmruntime.ResponseDetailDetailed:
			stage.ResponseDetail = llmruntime.ResponseDetailNormal
		case llmruntime.ResponseDetailNormal:
			stage.ResponseDetail = llmruntime.ResponseDetailTerse
		}
	case shared.ResearchDepthQuick:
		stage.Deliberation = lowerAcademicDeliberation(stage.Deliberation)
		if stage.ResponseDetail == llmruntime.ResponseDetailDetailed {
			stage.ResponseDetail = llmruntime.ResponseDetailNormal
		}
	case shared.ResearchDepthDeep:
		stage.Deliberation = raiseAcademicDeliberation(stage.Deliberation)
		if stage.ResponseDetail == llmruntime.ResponseDetailDefault || stage.ResponseDetail == llmruntime.ResponseDetailNormal {
			stage.ResponseDetail = llmruntime.ResponseDetailDetailed
		}
	case shared.ResearchDepthComprehensive:
		stage.Deliberation = llmruntime.DeliberationMax
		stage.ResponseDetail = llmruntime.ResponseDetailDetailed
	}
	return stage
}

func lowerAcademicDeliberation(value llmruntime.Deliberation) llmruntime.Deliberation {
	switch value {
	case llmruntime.DeliberationMax:
		return llmruntime.DeliberationHigh
	case llmruntime.DeliberationHigh:
		return llmruntime.DeliberationMedium
	case llmruntime.DeliberationMedium:
		return llmruntime.DeliberationLow
	default:
		return value
	}
}

func raiseAcademicDeliberation(value llmruntime.Deliberation) llmruntime.Deliberation {
	switch value {
	case llmruntime.DeliberationLow:
		return llmruntime.DeliberationMedium
	case llmruntime.DeliberationMedium:
		return llmruntime.DeliberationHigh
	case llmruntime.DeliberationHigh:
		return llmruntime.DeliberationMax
	default:
		return value
	}
}

func academicMaxToolRunsForContext(ctx context.Context, configured int) int {
	if configured <= 0 {
		return configured
	}
	switch AcademicResearchDepthFromContext(ctx) {
	case shared.ResearchDepthMinimal:
		if configured > 6 {
			return 6
		}
	case shared.ResearchDepthQuick:
		if configured > 16 {
			return 16
		}
	}
	return configured
}
