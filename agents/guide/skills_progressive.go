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
		RecentInputs:    guideSkillInputs(g, request),
		ActiveDomains:   guideSkillDomains(request),
		RecentlyInvoked: g.recentlyInvokedGuideSkills(6),
		TokenBudget:     0,
	}
	g.LoadSkillsForContext(ctx)
	g.OptimizeSkillsForBudget()
	g.ensureCriticalGuideSkills(request.Input)
}

// PrepareToolDefinitionsForInput progressively loads likely skills for input and returns loaded tool definitions.
func (g *Guide) PrepareToolDefinitionsForInput(input string) []map[string]any {
	req := &RouteRequest{Input: input}
	g.prepareSkillsForRouting(req)
	return g.GetLoadedSkillDefinitions()
}

func guideSkillInputs(g *Guide, request *RouteRequest) []string {
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
	if g != nil && g.conversation != nil && request.SessionID != "" {
		if snapshot, ok := g.conversation.Snapshot(request.SessionID); ok {
			inputs = append(inputs, snapshot.ActiveAgentID)
		}
	}
	return trimNonEmpty(inputs)
}

func guideSkillDomains(request *RouteRequest) []string {
	if request == nil {
		return []string{"routing"}
	}
	query := strings.ToLower(strings.TrimSpace(request.Input))
	domains := []string{"routing"}
	if query == "" {
		return domains
	}
	if strings.Contains(query, "agent") || strings.Contains(query, "registry") {
		domains = append(domains, "agents")
	}
	if strings.Contains(query, "task") || strings.Contains(query, "pipeline") || strings.Contains(query, "orchestrator") {
		domains = append(domains, "tasks")
	}
	return trimNonEmpty(domains)
}

func (g *Guide) ensureCriticalGuideSkills(input string) {
	if g == nil || g.skills == nil {
		return
	}
	if guideLooksLikeTaskInput(input) {
		_ = g.skills.Load("task_interact")
	}
}

func guideLooksLikeTaskInput(input string) bool {
	query := strings.ToLower(strings.TrimSpace(input))
	if query == "" {
		return false
	}
	return strings.Contains(query, "task") ||
		strings.Contains(query, "pipeline") ||
		strings.Contains(query, "orchestrator")
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
