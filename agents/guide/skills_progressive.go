package guide

import (
	"sort"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
)

func (g *Guide) prepareSkillsForRouting(request *RouteRequest) {
	if g == nil || request == nil || g.skillLoader == nil {
		return
	}
	ctx := skills.LoadContext{
		RecentInputs:    guideSkillInputs(request),
		ActiveDomains:   nil,
		RecentlyInvoked: g.recentlyInvokedGuideSkills(6),
		TokenBudget:     0,
	}
	g.LoadSkillsForContext(ctx)
	g.OptimizeSkillsForBudget()
}

// PrepareToolDefinitionsForInput progressively loads likely skills for input and returns loaded tool definitions.
func (g *Guide) PrepareToolDefinitionsForInput(input string) []map[string]any {
	req := &RouteRequest{Input: input}
	g.prepareSkillsForRouting(req)
	return g.GetLoadedSkillDefinitions()
}

func guideSkillInputs(request *RouteRequest) []string {
	if request == nil {
		return nil
	}
	inputs := []string{request.Input}
	if request.TargetAgentID != "" {
		inputs = append(inputs, request.TargetAgentID)
	}
	if request.SourceAgentID != "" {
		inputs = append(inputs, request.SourceAgentID)
	}
	return trimNonEmpty(inputs)
}

func trimNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func (g *Guide) recentlyInvokedGuideSkills(limit int) []string {
	all := g.skills.GetAll()
	sort.Slice(all, func(i, j int) bool {
		return all[i].InvokeCount > all[j].InvokeCount
	})
	result := make([]string, 0, limit)
	for _, skill := range all {
		if skill.InvokeCount == 0 {
			continue
		}
		result = append(result, skill.Name)
		if len(result) >= limit {
			break
		}
	}
	return result
}
