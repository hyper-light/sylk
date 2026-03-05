package guardian

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/skills"
)

// ---------------------------------------------------------------------------
// usage_breakdown — Domain: observability, Priority: 80
// ---------------------------------------------------------------------------

type usageBreakdownInput struct {
	Action string `json:"action"`
}

func usageBreakdownSkill(g *Guardian) *skills.Skill {
	type handler = func(context.Context, *usageBreakdownInput) (any, error)
	dispatch := map[string]handler{
		"by_agent": func(_ context.Context, _ *usageBreakdownInput) (any, error) {
			return usageByAgent(g)
		},
		"by_model": func(_ context.Context, _ *usageBreakdownInput) (any, error) {
			return usageByModel(g)
		},
		"totals": func(_ context.Context, _ *usageBreakdownInput) (any, error) {
			return usageTotals(g)
		},
	}

	return skills.NewSkill("usage_breakdown").
		Description("Per-agent and per-model token/cost usage breakdown.\n\n"+
			"Actions:\n"+
			"- by_agent: Token and cost usage grouped by agent ID\n"+
			"- by_model: Token and cost usage grouped by model name\n"+
			"- totals: Aggregate token/cost totals with budget status").
		Domain("observability").
		Keywords("usage", "tokens", "cost", "breakdown", "agent", "model", "spending", "attribution").
		Priority(80).
		TokenEstimate(500).
		EnumParam("action", "Usage action", []string{
			"by_agent", "by_model", "totals",
		}, true).
		Usage("Use usage_breakdown to understand where tokens and costs are being "+
			"consumed. action=by_agent shows per-agent attribution. action=by_model "+
			"shows per-model attribution. action=totals shows budget consumption.").
		BestPractice("Use action=by_agent to identify which agents are consuming "+
			"the most tokens. Use action=by_model to see which LLM models are "+
			"driving costs.").
		Example(`{"action": "by_agent"}`).
		Example(`{"action": "by_model"}`).
		Example(`{"action": "totals"}`).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params usageBreakdownInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			shared.LogAgentEvent(g.steering.EventLogger(),
				agentlog.EventSkillInvoked, g.id, "", "", "info",
				map[string]string{"skill": "usage_breakdown", "action": params.Action})
			fn, ok := dispatch[params.Action]
			if !ok {
				return nil, fmt.Errorf("unknown usage_breakdown action: %q", params.Action)
			}
			return fn(ctx, &params)
		}).
		Build()
}

func usageByAgent(g *Guardian) (any, error) {
	agentBreakdown, _ := g.healthMon.UsageBreakdown()
	return map[string]any{
		"by_agent":    agentBreakdown,
		"agent_count": len(agentBreakdown),
	}, nil
}

func usageByModel(g *Guardian) (any, error) {
	_, modelBreakdown := g.healthMon.UsageBreakdown()
	return map[string]any{
		"by_model":    modelBreakdown,
		"model_count": len(modelBreakdown),
	}, nil
}

func usageTotals(g *Guardian) (any, error) {
	budget := g.healthMon.BudgetSnapshot()
	agentBreakdown, modelBreakdown := g.healthMon.UsageBreakdown()

	return map[string]any{
		"budget":      budget,
		"agent_count": len(agentBreakdown),
		"model_count": len(modelBreakdown),
	}, nil
}
