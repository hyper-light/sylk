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
// knowledge_status — Domain: observability, Priority: 75
// ---------------------------------------------------------------------------

type knowledgeStatusInput struct {
	Action string `json:"action"`
}

func knowledgeStatusSkill(g *Guardian) *skills.Skill {
	type handler = func(context.Context, *knowledgeStatusInput) (any, error)
	dispatch := map[string]handler{
		"status": func(_ context.Context, _ *knowledgeStatusInput) (any, error) {
			return knowledgeStatus(g)
		},
		"query_metrics": func(_ context.Context, _ *knowledgeStatusInput) (any, error) {
			return knowledgeQueryMetrics(g)
		},
		"ingestion": func(_ context.Context, _ *knowledgeStatusInput) (any, error) {
			return knowledgeIngestion(g)
		},
	}

	return skills.NewSkill("knowledge_status").
		Description("Knowledge store readiness, query performance, and ingestion observability.\n\n"+
			"Actions:\n"+
			"- status: Readiness level and active searchers (text, semantic, graph)\n"+
			"- query_metrics: Average query latencies per search component\n"+
			"- ingestion: Background indexing progress (indexed vs total documents)").
		Domain("observability").
		Keywords("knowledge", "search", "bleve", "vector", "graph", "ingestion", "index", "readiness").
		Priority(75).
		TokenEstimate(400).
		EnumParam("action", "Knowledge action", []string{
			"status", "query_metrics", "ingestion",
		}, true).
		Usage("Use knowledge_status to check search system health. action=status "+
			"shows readiness and active searchers. action=query_metrics shows average "+
			"latencies. action=ingestion shows background indexing progress.").
		BestPractice("If readiness is 0 (none), knowledge search is unavailable. "+
			"If ingestion shows indexed < total, indexing is still in progress.").
		Example(`{"action": "status"}`).
		Example(`{"action": "query_metrics"}`).
		Example(`{"action": "ingestion"}`).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params knowledgeStatusInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			shared.LogAgentEvent(g.steering.EventLogger(),
				agentlog.EventSkillInvoked, g.id, "", "", "info",
				map[string]string{"skill": "knowledge_status", "action": params.Action})
			fn, ok := dispatch[params.Action]
			if !ok {
				return nil, fmt.Errorf("unknown knowledge_status action: %q", params.Action)
			}
			return fn(ctx, &params)
		}).
		Build()
}

func knowledgeStatus(g *Guardian) (any, error) {
	if g.config.Knowledge == nil {
		return map[string]any{
			"available": false,
			"reason":    "knowledge querier not wired",
		}, nil
	}

	status := g.config.Knowledge.Status()
	return map[string]any{
		"available": true,
		"status":    status,
	}, nil
}

func knowledgeQueryMetrics(g *Guardian) (any, error) {
	if g.config.Knowledge == nil {
		return map[string]any{
			"available": false,
			"reason":    "knowledge querier not wired",
		}, nil
	}

	metrics := g.config.Knowledge.QueryMetrics()
	return map[string]any{
		"available": true,
		"metrics":   metrics,
	}, nil
}

func knowledgeIngestion(g *Guardian) (any, error) {
	if g.config.Knowledge == nil {
		return map[string]any{
			"available": false,
			"reason":    "knowledge querier not wired",
		}, nil
	}

	indexed, total := g.config.Knowledge.IngestionProgress()
	var pct float64
	if total > 0 {
		pct = float64(indexed) / float64(total) * 100
	}

	return map[string]any{
		"available": true,
		"indexed":   indexed,
		"total":     total,
		"percent":   pct,
		"complete":  indexed >= total && total > 0,
	}, nil
}
