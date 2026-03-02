package guardian

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/adalundhe/sylk/core/skills"
)

// ---------------------------------------------------------------------------
// agent_health — Domain: health, Priority: 85
// ---------------------------------------------------------------------------

type agentHealthInput struct {
	Action  string `json:"action"`
	AgentID string `json:"agent_id,omitempty"`
}

func agentHealthSkill(g *Guardian) *skills.Skill {
	type handler = func(context.Context, *agentHealthInput) (any, error)
	dispatch := map[string]handler{
		"status": func(_ context.Context, p *agentHealthInput) (any, error) {
			if p.AgentID != "" {
				snap, ok := g.healthMon.AgentSnapshot(p.AgentID)
				if !ok {
					return map[string]any{
						"found":    false,
						"agent_id": p.AgentID,
					}, nil
				}
				return map[string]any{
					"found":  true,
					"health": snap,
				}, nil
			}
			snapshots := g.healthMon.AllSnapshots()
			return map[string]any{
				"agents":      snapshots,
				"agent_count": len(snapshots),
			}, nil
		},
		"budget_check": func(_ context.Context, _ *agentHealthInput) (any, error) {
			budget := g.healthMon.BudgetSnapshot()
			return map[string]any{"budget": budget}, nil
		},
		"anomaly_report": func(_ context.Context, _ *agentHealthInput) (any, error) {
			anomalies := g.healthMon.DetectAnomalies()
			return map[string]any{
				"anomalies":     anomalies,
				"anomaly_count": len(anomalies),
				"clean":         len(anomalies) == 0,
			}, nil
		},
	}

	return skills.NewSkill("agent_health").
		Description("Agent health monitoring: status checks, budget tracking, anomaly detection.\n\n"+
			"Actions:\n"+
			"- status: Get health status for one or all agents (params: agent_id [optional])\n"+
			"- budget_check: Check token and cost budget consumption\n"+
			"- anomaly_report: Detect health anomalies across all agents").
		Domain("health").
		Keywords("health", "status", "budget", "tokens", "cost", "anomaly", "timeout", "agent").
		Priority(85).
		TokenEstimate(400).
		EnumParam("action", "Health action", []string{
			"status", "budget_check", "anomaly_report",
		}, true).
		StringParam("agent_id", "Agent ID to check (for status, optional — omit for all)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params agentHealthInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			fn, ok := dispatch[params.Action]
			if !ok {
				return nil, fmt.Errorf("unknown agent_health action: %q", params.Action)
			}
			return fn(ctx, &params)
		}).
		Build()
}
