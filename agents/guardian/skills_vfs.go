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
// vfs_status — Domain: observability, Priority: 70
// ---------------------------------------------------------------------------

type vfsStatusInput struct {
	Action string `json:"action"`
}

func vfsStatusSkill(g *Guardian) *skills.Skill {
	type handler = func(context.Context, *vfsStatusInput) (any, error)
	dispatch := map[string]handler{
		"stats": func(_ context.Context, _ *vfsStatusInput) (any, error) {
			return vfsStats(g)
		},
		"manager": func(_ context.Context, _ *vfsStatusInput) (any, error) {
			return vfsManager(g)
		},
	}

	return skills.NewSkill("vfs_status").
		Description("Observe VFS write activity and versioning state.\n\n"+
			"Actions:\n"+
			"- stats: CVS-level metrics — files, versions, operations, locks\n"+
			"- manager: VFS manager state — active VFSes, variant groups, sessions, pipelines").
		Domain("observability").
		Keywords("vfs", "versioning", "files", "versions", "locks", "pipelines", "cvs").
		Priority(70).
		TokenEstimate(300).
		EnumParam("action", "VFS action", []string{
			"stats", "manager",
		}, true).
		Usage("Use vfs_status to monitor file versioning activity. "+
			"action=stats shows CVS-level metrics (files, versions, locks). "+
			"action=manager shows VFS manager state (active sessions, pipelines).").
		BestPractice("High active_locks count may indicate contention. "+
			"Active pipelines should match expected running workflows.").
		Example(`{"action": "stats"}`).
		Example(`{"action": "manager"}`).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params vfsStatusInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			shared.LogAgentEvent(g.steering.EventLogger(),
				agentlog.EventSkillInvoked, g.id, "", "", "info",
				map[string]string{"skill": "vfs_status", "action": params.Action})
			fn, ok := dispatch[params.Action]
			if !ok {
				return nil, fmt.Errorf("unknown vfs_status action: %q", params.Action)
			}
			return fn(ctx, &params)
		}).
		Build()
}

func vfsStats(g *Guardian) (any, error) {
	if g.config.CVS == nil {
		return map[string]any{
			"available": false,
			"reason":    "CVS not wired",
		}, nil
	}

	stats := g.config.CVS.Stats()
	return map[string]any{
		"available":          true,
		"total_files":        stats.TotalFiles,
		"total_versions":     stats.TotalVersions,
		"total_operations":   stats.TotalOperations,
		"active_pipelines":   stats.ActivePipelines,
		"active_variants":    stats.ActiveVariants,
		"active_locks":       stats.ActiveLocks,
		"active_subscribers": stats.ActiveSubscribers,
	}, nil
}

func vfsManager(g *Guardian) (any, error) {
	if g.config.VFSManager == nil {
		return map[string]any{
			"available": false,
			"reason":    "VFS manager not wired",
		}, nil
	}

	stats := g.config.VFSManager.Stats()
	return map[string]any{
		"available":       true,
		"active_vfses":    stats.ActiveVFSes,
		"variant_groups":  stats.VariantGroups,
		"active_sessions": stats.ActiveSessions,
		"total_pipelines": stats.TotalPipelines,
	}, nil
}
