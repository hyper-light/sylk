package guardian

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/skills"
)

// ---------------------------------------------------------------------------
// agent_logs — Domain: observability, Priority: 80
// ---------------------------------------------------------------------------

type agentLogsInput struct {
	Action     string `json:"action"`
	AgentName  string `json:"agent_name,omitempty"`
	Since      string `json:"since,omitempty"`
	Level      string `json:"level,omitempty"`
	CorrID     string `json:"corr_id,omitempty"`
	MaxEntries int    `json:"max_entries,omitempty"`
}

func agentLogsSkill(g *Guardian) *skills.Skill {
	diag := &guardianDiag{g: g}

	type handler = func(context.Context, *agentLogsInput) (any, error)
	dispatch := map[string]handler{
		"query": func(ctx context.Context, p *agentLogsInput) (any, error) {
			return agentLogsQuery(ctx, g, diag, p)
		},
		"summary": func(ctx context.Context, p *agentLogsInput) (any, error) {
			return agentLogsSummary(ctx, g, diag, p)
		},
		"recent": func(_ context.Context, p *agentLogsInput) (any, error) {
			return agentLogsRecent(g, p)
		},
	}

	return skills.NewSkill("agent_logs").
		Description("Cross-agent log querying: deep scans, aggregated summaries, and ring buffer reads.\n\n"+
			"Actions:\n"+
			"- query: Deep log scan with filters (params: agent_name, since, level, corr_id, max_entries)\n"+
			"- summary: Aggregated summary — error counts, level distribution, recent errors (params: agent_name, since)\n"+
			"- recent: Last N entries from guardian's ring buffer — zero I/O (params: max_entries)").
		Domain("observability").
		Keywords("logs", "errors", "query", "log", "trace", "correlation", "debug").
		Priority(80).
		TokenEstimate(600).
		EnumParam("action", "Log query action", []string{
			"query", "summary", "recent",
		}, true).
		StringParam("agent_name", "Agent name to query logs for (e.g. architect, orchestrator)", false).
		StringParam("since", "Time window as Go duration (e.g. 5m, 1h, 30s)", false).
		StringParam("level", "Filter by log level", false).
		StringParam("corr_id", "Filter by correlation ID", false).
		IntParam("max_entries", "Maximum entries to return (default 50)", false).
		Usage("Use agent_logs to investigate agent errors, failures, or abnormal "+
			"behavior. Start with action=summary for an overview, then drill "+
			"down with action=query and specific filters.").
		BestPractice("Use action=recent for zero-I/O ring buffer reads when "+
			"investigating live issues. Use action=query with since filter "+
			"for historical analysis.").
		Example(`{"action": "summary", "agent_name": "architect", "since": "5m"}`).
		Example(`{"action": "recent", "max_entries": 20}`).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params agentLogsInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			shared.LogAgentEvent(g.steering.EventLogger(),
				agentlog.EventSkillInvoked, g.id, "", "", "info",
				map[string]string{"skill": "agent_logs", "action": params.Action, "agent_name": params.AgentName})
			fn, ok := dispatch[params.Action]
			if !ok {
				return nil, fmt.Errorf("unknown agent_logs action: %q", params.Action)
			}
			return fn(ctx, &params)
		}).
		Build()
}

func agentLogsQuery(ctx context.Context, g *Guardian, diag *guardianDiag, p *agentLogsInput) (any, error) {
	logsDir := resolveLogsDir(g, diag, p.AgentName)
	if logsDir == "" {
		return map[string]any{
			"entries": []any{},
			"error":   "no logs directory available for agent",
		}, nil
	}

	q := agentlog.LogQuery{
		Dir:        logsDir,
		MaxEntries: coerceMaxEntries(p.MaxEntries, 50),
	}
	if p.Since != "" {
		dur, err := time.ParseDuration(p.Since)
		if err != nil {
			return nil, fmt.Errorf("invalid since duration: %w", err)
		}
		q.Since = time.Now().Add(-dur)
	}
	if p.Level != "" {
		q.Levels = []string{p.Level}
	}
	if p.CorrID != "" {
		q.CorrID = p.CorrID
	}

	entries, err := agentlog.ReadLogs(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("read logs: %w", err)
	}

	return map[string]any{
		"entries":     entries,
		"entry_count": len(entries),
		"agent_name":  p.AgentName,
		"logs_dir":    logsDir,
	}, nil
}

func agentLogsSummary(ctx context.Context, g *Guardian, diag *guardianDiag, p *agentLogsInput) (any, error) {
	logsDir := resolveLogsDir(g, diag, p.AgentName)
	if logsDir == "" {
		return map[string]any{
			"summary": nil,
			"error":   "no logs directory available for agent",
		}, nil
	}

	q := agentlog.LogQuery{
		Dir:        logsDir,
		MaxEntries: 500,
	}
	if p.Since != "" {
		dur, err := time.ParseDuration(p.Since)
		if err != nil {
			return nil, fmt.Errorf("invalid since duration: %w", err)
		}
		q.Since = time.Now().Add(-dur)
	}

	entries, err := agentlog.ReadLogs(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("read logs: %w", err)
	}

	summary := agentlog.Summarize(entries)
	return map[string]any{
		"summary":    summary,
		"agent_name": p.AgentName,
	}, nil
}

func agentLogsRecent(g *Guardian, p *agentLogsInput) (any, error) {
	logger := g.steering.EventLogger()
	if logger == nil {
		return map[string]any{
			"entries":     []any{},
			"entry_count": 0,
		}, nil
	}

	n := coerceMaxEntries(p.MaxEntries, 50)
	entries := logger.RecentEntries(n)
	return map[string]any{
		"entries":     entries,
		"entry_count": len(entries),
		"source":      "ring_buffer",
	}, nil
}

// resolveLogsDir determines the logs directory for the given agent name.
// If agentName is empty or "guardian", returns guardian's own logs dir.
// Otherwise, checks peer logs dirs.
func resolveLogsDir(g *Guardian, diag *guardianDiag, agentName string) string {
	if agentName == "" || agentName == "guardian" {
		return diag.LogsDir()
	}
	peers := diag.PeerLogsDirs()
	if dir, ok := peers[agentName]; ok {
		return dir
	}
	// Fall back to constructing the path.
	sessionDir := g.steering.SessionDir()
	if sessionDir == "" {
		return ""
	}
	return shared.LogsDirForAgent(sessionDir, agentName)
}

func coerceMaxEntries(n, defaultN int) int {
	if n <= 0 {
		return defaultN
	}
	return n
}
