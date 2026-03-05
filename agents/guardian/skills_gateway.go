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
// gateway_metrics — Domain: observability, Priority: 75
// ---------------------------------------------------------------------------

type gatewayMetricsInput struct {
	Action string `json:"action"`
}

func gatewayMetricsSkill(g *Guardian) *skills.Skill {
	type handler = func(context.Context, *gatewayMetricsInput) (any, error)
	dispatch := map[string]handler{
		"overview": func(_ context.Context, _ *gatewayMetricsInput) (any, error) {
			return gatewayOverview(g)
		},
	}

	return skills.NewSkill("gateway_metrics").
		Description("LLM gateway throughput and rate-limit observability.\n\n"+
			"Actions:\n"+
			"- overview: Metrics for all gateways (admitted, rejected, queued, 429s, inflight, wait time)").
		Domain("observability").
		Keywords("gateway", "llm", "rate", "limit", "429", "throttle", "inflight", "throughput").
		Priority(75).
		TokenEstimate(400).
		EnumParam("action", "Gateway action", []string{
			"overview",
		}, true).
		Usage("Use gateway_metrics to check LLM gateway health. High rejected or "+
			"429 counts indicate rate-limiting pressure. High inflight indicates "+
			"concurrency saturation.").
		BestPractice("Compare admitted vs rejected counts to assess overall "+
			"gateway throughput. Rising errors_429 suggests API quota pressure.").
		Example(`{"action": "overview"}`).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params gatewayMetricsInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			shared.LogAgentEvent(g.steering.EventLogger(),
				agentlog.EventSkillInvoked, g.id, "", "", "info",
				map[string]string{"skill": "gateway_metrics", "action": params.Action})
			fn, ok := dispatch[params.Action]
			if !ok {
				return nil, fmt.Errorf("unknown gateway_metrics action: %q", params.Action)
			}
			return fn(ctx, &params)
		}).
		Build()
}

func gatewayOverview(g *Guardian) (any, error) {
	if g.config.Gateway == nil {
		return map[string]any{
			"available": false,
			"reason":    "gateway querier not wired",
		}, nil
	}

	metrics := g.config.Gateway.AllMetrics()
	return map[string]any{
		"available":     true,
		"gateways":      metrics,
		"gateway_count": len(metrics),
	}, nil
}
