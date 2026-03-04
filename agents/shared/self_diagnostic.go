package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/skills"
)

// AgentDiagnosticProvider is implemented by each agent to expose
// diagnostic data to the shared self_diagnostic skill.
type AgentDiagnosticProvider interface {
	AgentName() string
	LogsDir() string                            // own agent's logs dir ("" if unbound)
	SessionID() string
	EventLogger() *agentlog.SessionEventLogger  // for ring buffer + stats
	PeerLogsDirs() map[string]string            // agent_name → logs_dir (orchestrator/guardian only, nil for others)
	AgentSpecificDiagnostics() map[string]any
	RecoveryHints() []string
}

// DiagnosticReport is the structured output of the self_diagnostic skill.
type DiagnosticReport struct {
	Agent         string                `json:"agent"`
	SessionID     string                `json:"session_id"`
	Query         string                `json:"query,omitempty"`
	Stats         agentlog.LoggerStats  `json:"stats"`
	RecentRing    []agentlog.JSONLEntry `json:"recent_ring,omitempty"`
	LogSummary    *agentlog.LogSummary  `json:"log_summary,omitempty"`
	PeerSummaries map[string]*agentlog.LogSummary `json:"peer_summaries,omitempty"`
	AgentState    map[string]any        `json:"agent_state,omitempty"`
	RecoveryHints []string              `json:"recovery_hints,omitempty"`
	LogsAvailable bool                  `json:"logs_available"`
}

type diagnosticInput struct {
	Query     string `json:"query"`
	Format    string `json:"format"`
	Since     string `json:"since"`
	Level     string `json:"level"`
	CorrID    string `json:"corr_id"`
	PeerAgent string `json:"peer_agent"`
}

// NewSelfDiagnosticSkill creates a self_diagnostic skill wired to the given provider.
func NewSelfDiagnosticSkill(provider AgentDiagnosticProvider) *skills.Skill {
	return skills.NewSkill("self_diagnostic").
		Description("Introspect this agent's health, recent log entries, error patterns, and recovery hints.").
		Domain("diagnostics").
		Keywords("diagnostic", "health", "errors", "logs", "status").
		Priority(95).
		TokenEstimate(600).
		StringParam("query", "Describe what you want to diagnose (e.g. 'recent errors', 'LLM failures', 'health check')", true).
		EnumParam("format", "Output format", []string{"text", "json"}, false).
		StringParam("since", "Time window as Go duration (e.g. '5m', '1h')", false).
		EnumParam("level", "Filter log entries by level", []string{"info", "warn", "error"}, false).
		StringParam("corr_id", "Filter by correlation ID", false).
		StringParam("peer_agent", "Query a supervised peer agent's logs (orchestrator/guardian only)", false).
		Usage("Use self_diagnostic to check your own health, find recent errors, or investigate issues by correlation ID.").
		Example(`{"query": "recent errors", "format": "text", "since": "5m"}`).
		BestPractice("Start with a broad health check before narrowing to specific correlation IDs or time windows.").
		Handler(diagnosticHandler(provider)).
		Build()
}

func diagnosticHandler(p AgentDiagnosticProvider) skills.Handler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in diagnosticInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, fmt.Errorf("self_diagnostic: invalid input: %w", err)
		}

		report := DiagnosticReport{
			Agent:     p.AgentName(),
			SessionID: p.SessionID(),
			Query:     in.Query,
		}

		// 1. Instant stats from ring buffer — zero I/O.
		el := p.EventLogger()
		report.Stats = el.Stats()

		// 2. Recent entries from ring — zero I/O.
		report.RecentRing = el.RecentEntries(20)

		// 3. Agent-specific diagnostics.
		report.AgentState = p.AgentSpecificDiagnostics()
		report.RecoveryHints = p.RecoveryHints()

		// 4. Deep log scan if filters are specified.
		logsDir := p.LogsDir()
		report.LogsAvailable = logsDir != ""

		needDeepScan := in.Since != "" || in.Level != "" || in.CorrID != "" ||
			strings.Contains(strings.ToLower(in.Query), "error") ||
			strings.Contains(strings.ToLower(in.Query), "history")

		if needDeepScan && logsDir != "" {
			q := buildLogQuery(logsDir, in)
			entries, err := agentlog.ReadLogs(ctx, q)
			if err == nil {
				summary := agentlog.Summarize(entries)
				report.LogSummary = &summary
			}
		}

		// 5. Peer agent logs (orchestrator/guardian only).
		if in.PeerAgent != "" {
			peers := p.PeerLogsDirs()
			if peerDir, ok := peers[in.PeerAgent]; ok {
				q := buildLogQuery(peerDir, in)
				entries, err := agentlog.ReadLogs(ctx, q)
				if err == nil {
					summary := agentlog.Summarize(entries)
					if report.PeerSummaries == nil {
						report.PeerSummaries = make(map[string]*agentlog.LogSummary)
					}
					report.PeerSummaries[in.PeerAgent] = &summary
				}
			}
		}

		if in.Format == "text" {
			return formatDiagnosticReport(report), nil
		}
		return report, nil
	}
}

func buildLogQuery(dir string, in diagnosticInput) agentlog.LogQuery {
	q := agentlog.LogQuery{Dir: dir}

	if in.Since != "" {
		if d, err := time.ParseDuration(in.Since); err == nil {
			q.Since = time.Now().Add(-d)
		}
	}
	if in.Level != "" {
		q.Levels = []string{in.Level}
	}
	if in.CorrID != "" {
		q.CorrID = in.CorrID
	}

	return q
}

func formatDiagnosticReport(r DiagnosticReport) string {
	var b strings.Builder

	fmt.Fprintf(&b, "=== Diagnostic Report: %s ===\n", r.Agent)
	fmt.Fprintf(&b, "Session: %s\n", r.SessionID)
	fmt.Fprintf(&b, "Query: %s\n\n", r.Query)

	fmt.Fprintf(&b, "--- Stats ---\n")
	fmt.Fprintf(&b, "Total events: %d | Errors: %d | Warnings: %d\n",
		r.Stats.TotalCount, r.Stats.ErrorCount, r.Stats.WarnCount)
	if !r.Stats.LastError.IsZero() {
		fmt.Fprintf(&b, "Last error: %s\n", r.Stats.LastError.Format(time.RFC3339))
	}

	if len(r.RecentRing) > 0 {
		fmt.Fprintf(&b, "\n--- Recent Events (ring buffer, last %d) ---\n", len(r.RecentRing))
		for _, e := range r.RecentRing {
			ts := e.Timestamp.Format("15:04:05.000")
			line := fmt.Sprintf("[%s] %s %s", ts, e.Level, e.Event)
			if e.Error != "" {
				line += " err=" + e.Error
			}
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}

	if r.LogSummary != nil {
		fmt.Fprintf(&b, "\n--- Log Summary (disk scan) ---\n")
		fmt.Fprintf(&b, "Scanned: %d | Matched: %d\n", r.LogSummary.TotalScanned, r.LogSummary.TotalMatched)
		if len(r.LogSummary.LevelCounts) > 0 {
			fmt.Fprintf(&b, "Levels: ")
			for lvl, cnt := range r.LogSummary.LevelCounts {
				fmt.Fprintf(&b, "%s=%d ", lvl, cnt)
			}
			b.WriteString("\n")
		}
		if len(r.LogSummary.RecentErrors) > 0 {
			fmt.Fprintf(&b, "Recent errors:\n")
			for _, e := range r.LogSummary.RecentErrors {
				fmt.Fprintf(&b, "  [%s] %s: %s\n",
					e.Timestamp.Format("15:04:05"), e.Event, e.Error)
			}
		}
	}

	if len(r.PeerSummaries) > 0 {
		for peer, s := range r.PeerSummaries {
			fmt.Fprintf(&b, "\n--- Peer: %s ---\n", peer)
			fmt.Fprintf(&b, "Scanned: %d | Errors: %d\n", s.TotalScanned, s.LevelCounts["error"])
		}
	}

	if len(r.AgentState) > 0 {
		fmt.Fprintf(&b, "\n--- Agent State ---\n")
		for k, v := range r.AgentState {
			fmt.Fprintf(&b, "  %s: %v\n", k, v)
		}
	}

	if len(r.RecoveryHints) > 0 {
		fmt.Fprintf(&b, "\n--- Recovery Hints ---\n")
		for _, h := range r.RecoveryHints {
			fmt.Fprintf(&b, "  - %s\n", h)
		}
	}

	return b.String()
}

// LogsDirForAgent returns the standard logs directory path for an agent
// under the given session directory. Returns "" if sessionDir is empty.
func LogsDirForAgent(sessionDir, agentName string) string {
	if sessionDir == "" {
		return ""
	}
	return filepath.Join(sessionDir, "agents", agentName, "logs")
}
