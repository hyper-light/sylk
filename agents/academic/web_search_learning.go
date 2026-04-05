package academic

import (
	"context"
	"math"
	"sync"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/providers"
)

type academicWebSearchLearner struct {
	mu      sync.RWMutex
	global  map[int]searchOrdinalStats
	byDepth map[shared.ResearchDepth]map[int]searchOrdinalStats
}

type searchOrdinalStats struct {
	Count  float64
	Reward float64
}

type academicWebSearchFrontier struct {
	TotalSearches    int
	RepeatedSearches int
	UniqueResults    int
	UniqueDomains    int
	GroundedSources  int
	LocalRewardMean  float64
}

func newAcademicWebSearchLearner() *academicWebSearchLearner {
	return &academicWebSearchLearner{
		global:  make(map[int]searchOrdinalStats),
		byDepth: make(map[shared.ResearchDepth]map[int]searchOrdinalStats),
	}
}

func (l *academicWebSearchLearner) Observe(depth shared.ResearchDepth, ordinal int, reward float64) {
	if l == nil || ordinal <= 0 {
		return
	}
	depth = shared.EffectiveResearchDepth(string(depth))
	reward = clamp01(reward)

	l.mu.Lock()
	defer l.mu.Unlock()

	global := l.global[ordinal]
	global.Count++
	global.Reward += reward
	l.global[ordinal] = global

	perDepth := l.byDepth[depth]
	if perDepth == nil {
		perDepth = make(map[int]searchOrdinalStats)
		l.byDepth[depth] = perDepth
	}
	stats := perDepth[ordinal]
	stats.Count++
	stats.Reward += reward
	perDepth[ordinal] = stats
}

func (l *academicWebSearchLearner) PredictMarginalValue(
	depth shared.ResearchDepth,
	ordinal int,
	frontier academicWebSearchFrontier,
) float64 {
	if ordinal <= 0 {
		return 0
	}
	depth = shared.EffectiveResearchDepth(string(depth))

	l.mu.RLock()
	global := l.global[ordinal]
	perDepth := searchOrdinalStats{}
	if depthStats := l.byDepth[depth]; depthStats != nil {
		perDepth = depthStats[ordinal]
	}
	l.mu.RUnlock()

	priorMean := academicWebSearchPriorMean(depth, ordinal)
	priorWeight := 1.0
	predictedReward := (priorMean*priorWeight + global.Reward + perDepth.Reward) / (priorWeight + global.Count + perDepth.Count)
	return clamp01(predictedReward * frontier.value())
}

func academicWebSearchPriorMean(depth shared.ResearchDepth, ordinal int) float64 {
	if ordinal <= 0 {
		return 0
	}
	rank := academicResearchDepthRank(shared.EffectiveResearchDepth(string(depth)))
	return clamp01(float64(rank+1) / float64(rank+ordinal+1))
}

func academicWebSearchMarginalCost(depth shared.ResearchDepth, ordinal int) float64 {
	if ordinal <= 0 {
		return 0
	}
	rank := academicResearchDepthRank(shared.EffectiveResearchDepth(string(depth)))
	return clamp01(float64(ordinal) / float64(ordinal+rank+1))
}

func academicResearchDepthRank(depth shared.ResearchDepth) int {
	switch shared.EffectiveResearchDepth(string(depth)) {
	case shared.ResearchDepthMinimal:
		return 0
	case shared.ResearchDepthQuick:
		return 1
	case shared.ResearchDepthStandard:
		return 2
	case shared.ResearchDepthDeep:
		return 3
	case shared.ResearchDepthComprehensive:
		return 4
	default:
		return 2
	}
}

func (f academicWebSearchFrontier) value() float64 {
	if f.TotalSearches <= 0 {
		return 1
	}
	novelUnits := maxInt(f.UniqueResults, f.UniqueDomains)
	noveltyRate := clamp01(float64(novelUnits) / float64(f.TotalSearches))
	authorityGap := 1.0 / float64(1+f.GroundedSources)
	redundancyPenalty := 1.0 / float64(1+f.RepeatedSearches)
	localReward := clamp01(f.LocalRewardMean)
	return clamp01(((noveltyRate + authorityGap + localReward) / 3.0) * redundancyPenalty)
}

func (a *Academic) adaptiveNativeWebSearchLimit(
	ctx context.Context,
	execState *academicResearchExecutionState,
	hardMax int,
) int {
	if hardMax <= 0 || execState == nil || a == nil || a.webSearchModel == nil {
		return hardMax
	}
	depth := shared.EffectiveResearchDepth(string(AcademicResearchDepthFromContext(ctx)))
	frontier := execState.webSearchFrontier()
	current := frontier.TotalSearches
	allowed := 0
	for step := 1; step <= hardMax; step++ {
		ordinal := current + step
		if a.webSearchModel.PredictMarginalValue(depth, ordinal, frontier) < academicWebSearchMarginalCost(depth, ordinal) {
			break
		}
		allowed = step
	}
	return allowed
}

func (a *Academic) applyAdaptiveWebSearchBudget(
	ctx context.Context,
	tools []providers.Tool,
	execState *academicResearchExecutionState,
) []providers.Tool {
	if len(tools) == 0 {
		return nil
	}
	out := append([]providers.Tool(nil), tools...)
	for i := range out {
		if out[i].Name != "web_search" {
			continue
		}
		if out[i].WebSearch == nil {
			out[i].WebSearch = &providers.WebSearchOptions{}
		}
		hardMax := out[i].WebSearch.MaxUses
		if hardMax <= 0 {
			hardMax = a.config.MaxNativeWebSearchCalls
		}
		limit := a.adaptiveNativeWebSearchLimit(ctx, execState, hardMax)
		if limit <= 0 {
			return append(out[:i], out[i+1:]...)
		}
		out[i].WebSearch.MaxUses = limit
		return out
	}
	return out
}

func (a *Academic) commitWebSearchLearning(ctx context.Context, execState *academicResearchExecutionState) {
	if a == nil || a.webSearchModel == nil || execState == nil {
		return
	}
	depth := shared.EffectiveResearchDepth(string(AcademicResearchDepthFromContext(ctx)))
	for _, reward := range execState.webSearchEpisodeRewards() {
		a.webSearchModel.Observe(depth, reward.Ordinal, reward.Reward)
	}
}

type searchEpisodeReward struct {
	Ordinal int
	Reward  float64
}

func maxInt(values ...int) int {
	best := 0
	for _, value := range values {
		if value > best {
			best = value
		}
	}
	return best
}

func clamp01(value float64) float64 {
	return math.Max(0, math.Min(1, value))
}
