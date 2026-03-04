package shared

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/agentlog"
)

// testDiagProvider is a minimal test implementation of AgentDiagnosticProvider.
type testDiagProvider struct {
	name      string
	logsDir   string
	sessionID string
	logger    *agentlog.SessionEventLogger
	peers     map[string]string
	state     map[string]any
	hints     []string
}

func (t *testDiagProvider) AgentName() string                       { return t.name }
func (t *testDiagProvider) LogsDir() string                         { return t.logsDir }
func (t *testDiagProvider) SessionID() string                       { return t.sessionID }
func (t *testDiagProvider) EventLogger() *agentlog.SessionEventLogger { return t.logger }
func (t *testDiagProvider) PeerLogsDirs() map[string]string         { return t.peers }
func (t *testDiagProvider) AgentSpecificDiagnostics() map[string]any { return t.state }
func (t *testDiagProvider) RecoveryHints() []string                 { return t.hints }

func TestNewSelfDiagnosticSkill_Build(t *testing.T) {
	provider := &testDiagProvider{
		name:      "test_agent",
		sessionID: "sess-1",
	}

	skill := NewSelfDiagnosticSkill(provider)
	if skill == nil {
		t.Fatal("expected non-nil skill")
	}
	if skill.Name != "self_diagnostic" {
		t.Errorf("expected name 'self_diagnostic', got %q", skill.Name)
	}
	if skill.Domain != "diagnostics" {
		t.Errorf("expected domain 'diagnostics', got %q", skill.Domain)
	}
	if skill.Priority != 95 {
		t.Errorf("expected priority 95, got %d", skill.Priority)
	}
	if skill.Handler == nil {
		t.Error("expected non-nil handler")
	}
}

func TestDiagnosticHandler_BasicHealthCheck(t *testing.T) {
	dir := t.TempDir()
	logger := agentlog.NewSessionEventLogger("test_agent", "events")
	if err := logger.BindSession(dir, "sess-1"); err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	// Write some events to populate ring buffer and stats.
	logger.LogEvent(agentlog.JSONLEntry{
		Timestamp: time.Now(),
		Level:     "info",
		Agent:     "test_agent",
		Event:     "Activated",
		EventCode: agentlog.EventActivated,
	})
	logger.LogEvent(agentlog.JSONLEntry{
		Timestamp: time.Now(),
		Level:     "error",
		Agent:     "test_agent",
		Event:     "LLMError",
		EventCode: agentlog.EventLLMError,
		Error:     "rate limited",
	})

	provider := &testDiagProvider{
		name:      "test_agent",
		logsDir:   "",
		sessionID: "sess-1",
		logger:    logger,
		state:     map[string]any{"active_plans": 2},
		hints:     []string{"retry with backoff"},
	}

	skill := NewSelfDiagnosticSkill(provider)
	input, _ := json.Marshal(diagnosticInput{
		Query:  "health check",
		Format: "json",
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	report, ok := result.(DiagnosticReport)
	if !ok {
		t.Fatalf("expected DiagnosticReport, got %T", result)
	}

	if report.Agent != "test_agent" {
		t.Errorf("expected agent 'test_agent', got %q", report.Agent)
	}
	if report.Stats.TotalCount != 2 {
		t.Errorf("expected total_count=2, got %d", report.Stats.TotalCount)
	}
	if report.Stats.ErrorCount != 1 {
		t.Errorf("expected error_count=1, got %d", report.Stats.ErrorCount)
	}
	if len(report.RecentRing) != 2 {
		t.Errorf("expected 2 ring entries, got %d", len(report.RecentRing))
	}
	if report.AgentState["active_plans"] != 2 {
		t.Errorf("expected active_plans=2, got %v", report.AgentState["active_plans"])
	}
	if len(report.RecoveryHints) != 1 {
		t.Errorf("expected 1 hint, got %d", len(report.RecoveryHints))
	}
}

func TestDiagnosticHandler_TextFormat(t *testing.T) {
	logger := agentlog.NewSessionEventLogger("test_agent", "events")
	// Don't bind — test nil-safe path for unbound logger.

	provider := &testDiagProvider{
		name:      "test_agent",
		sessionID: "sess-1",
		logger:    logger,
	}

	skill := NewSelfDiagnosticSkill(provider)
	input, _ := json.Marshal(diagnosticInput{
		Query:  "quick check",
		Format: "text",
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	text, ok := result.(string)
	if !ok {
		t.Fatalf("expected string, got %T", result)
	}
	if len(text) == 0 {
		t.Error("expected non-empty text report")
	}
}

func TestDiagnosticHandler_DeepScan(t *testing.T) {
	dir := t.TempDir()
	logger := agentlog.NewSessionEventLogger("test_agent", "events")
	if err := logger.BindSession(dir, "sess-1"); err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	logger.LogEvent(agentlog.JSONLEntry{
		Timestamp: time.Now(),
		Level:     "error",
		Agent:     "test_agent",
		Event:     "Boom",
		Error:     "crash",
	})

	logsDir := LogsDirForAgent(dir, "test_agent")

	provider := &testDiagProvider{
		name:      "test_agent",
		logsDir:   logsDir,
		sessionID: "sess-1",
		logger:    logger,
	}

	skill := NewSelfDiagnosticSkill(provider)
	input, _ := json.Marshal(diagnosticInput{
		Query:  "show me errors",
		Format: "json",
		Since:  "5m",
		Level:  "error",
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	report := result.(DiagnosticReport)
	if report.LogSummary == nil {
		t.Fatal("expected non-nil log summary from deep scan")
	}
	if report.LogSummary.TotalMatched != 1 {
		t.Errorf("expected 1 matched entry, got %d", report.LogSummary.TotalMatched)
	}
}

func TestLogsDirForAgent(t *testing.T) {
	got := LogsDirForAgent("/sessions/abc", "architect")
	expected := "/sessions/abc/agents/architect/logs"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}

	if LogsDirForAgent("", "architect") != "" {
		t.Error("expected empty string for empty sessionDir")
	}
}
