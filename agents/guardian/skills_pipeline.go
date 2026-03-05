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
// pipeline_status — Domain: observability, Priority: 80
// ---------------------------------------------------------------------------

type pipelineStatusInput struct {
	Action string `json:"action"`
}

func pipelineStatusSkill(g *Guardian) *skills.Skill {
	type handler = func(context.Context, *pipelineStatusInput) (any, error)
	dispatch := map[string]handler{
		"summary": func(_ context.Context, _ *pipelineStatusInput) (any, error) {
			return pipelineSummary(g)
		},
		"dags": func(_ context.Context, _ *pipelineStatusInput) (any, error) {
			return pipelineDAGs(g)
		},
	}

	return skills.NewSkill("pipeline_status").
		Description("Pipeline and DAG execution observability.\n\n"+
			"Actions:\n"+
			"- summary: Orchestrator workflow/task summary (active, completed, failed counts)\n"+
			"- dags: Active DAG executions with layer progress and node state").
		Domain("observability").
		Keywords("pipeline", "dag", "workflow", "task", "orchestrator", "execution", "progress").
		Priority(80).
		TokenEstimate(500).
		EnumParam("action", "Pipeline action", []string{
			"summary", "dags",
		}, true).
		Usage("Use pipeline_status to inspect orchestrator workflow state and "+
			"DAG execution progress. action=summary for overview, action=dags for "+
			"individual DAG layer-by-layer progress.").
		BestPractice("Start with action=summary to understand overall pipeline health "+
			"before drilling into individual DAGs.").
		Example(`{"action": "summary"}`).
		Example(`{"action": "dags"}`).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params pipelineStatusInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			shared.LogAgentEvent(g.steering.EventLogger(),
				agentlog.EventSkillInvoked, g.id, "", "", "info",
				map[string]string{"skill": "pipeline_status", "action": params.Action})
			fn, ok := dispatch[params.Action]
			if !ok {
				return nil, fmt.Errorf("unknown pipeline_status action: %q", params.Action)
			}
			return fn(ctx, &params)
		}).
		Build()
}

func pipelineSummary(g *Guardian) (any, error) {
	if g.config.Pipeline == nil {
		return map[string]any{
			"available": false,
			"reason":    "pipeline querier not wired",
		}, nil
	}

	summary, err := g.config.Pipeline.Summary()
	if err != nil {
		return nil, fmt.Errorf("pipeline summary: %w", err)
	}

	return map[string]any{
		"available": true,
		"summary":   summary,
	}, nil
}

func pipelineDAGs(g *Guardian) (any, error) {
	if g.config.Pipeline == nil {
		return map[string]any{
			"available": false,
			"reason":    "pipeline querier not wired",
		}, nil
	}

	const dagLimit = 20
	snaps := g.config.Pipeline.DAGSnapshots(dagLimit)
	return map[string]any{
		"available": true,
		"dags":      snaps,
		"dag_count": len(snaps),
	}, nil
}
