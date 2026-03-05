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
// concurrency_status — Domain: observability, Priority: 70
// ---------------------------------------------------------------------------

type concurrencyStatusInput struct {
	Action string `json:"action"`
}

func concurrencyStatusSkill(g *Guardian) *skills.Skill {
	type handler = func(context.Context, *concurrencyStatusInput) (any, error)
	dispatch := map[string]handler{
		"goroutines": func(_ context.Context, _ *concurrencyStatusInput) (any, error) {
			return concurrencyGoroutines(g)
		},
		"llm_gate": func(_ context.Context, _ *concurrencyStatusInput) (any, error) {
			return concurrencyLLMGate(g)
		},
	}

	return skills.NewSkill("concurrency_status").
		Description("Goroutine budget and LLM request gate observability.\n\n"+
			"Actions:\n"+
			"- goroutines: System-wide goroutine budget (active, limit, agent count)\n"+
			"- llm_gate: Dual-queue LLM gate status (user/pipeline queues, active requests)").
		Domain("observability").
		Keywords("goroutine", "concurrency", "budget", "llm", "gate", "queue", "pressure").
		Priority(70).
		TokenEstimate(300).
		EnumParam("action", "Concurrency action", []string{
			"goroutines", "llm_gate",
		}, true).
		Usage("Use concurrency_status to check goroutine pressure and LLM gate "+
			"congestion. High active/limit ratio indicates goroutine pressure. "+
			"Non-zero queue sizes indicate LLM gate congestion.").
		BestPractice("If total_active approaches system_limit, the system is under "+
			"goroutine pressure and may reject new work. If pipeline_queue_size is high, "+
			"pipeline tasks are waiting for LLM access.").
		Example(`{"action": "goroutines"}`).
		Example(`{"action": "llm_gate"}`).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params concurrencyStatusInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			shared.LogAgentEvent(g.steering.EventLogger(),
				agentlog.EventSkillInvoked, g.id, "", "", "info",
				map[string]string{"skill": "concurrency_status", "action": params.Action})
			fn, ok := dispatch[params.Action]
			if !ok {
				return nil, fmt.Errorf("unknown concurrency_status action: %q", params.Action)
			}
			return fn(ctx, &params)
		}).
		Build()
}

func concurrencyGoroutines(g *Guardian) (any, error) {
	if g.config.Concurrency == nil {
		return map[string]any{
			"available": false,
			"reason":    "concurrency querier not wired",
		}, nil
	}

	stats := g.config.Concurrency.GoroutineStats()
	return map[string]any{
		"available": true,
		"stats":     stats,
	}, nil
}

func concurrencyLLMGate(g *Guardian) (any, error) {
	if g.config.Concurrency == nil {
		return map[string]any{
			"available": false,
			"reason":    "concurrency querier not wired",
		}, nil
	}

	stats := g.config.Concurrency.LLMGateStats()
	return map[string]any{
		"available": true,
		"stats":     stats,
	}, nil
}
