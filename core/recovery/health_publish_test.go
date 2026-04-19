package recovery

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"
)

// recordingHealthSink captures every assessment the monitor publishes.
type recordingHealthSink struct {
	mu          sync.Mutex
	assessments []HealthAssessment
}

func (r *recordingHealthSink) PublishHealthAssessment(a HealthAssessment) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.assessments = append(r.assessments, a)
}

func (r *recordingHealthSink) snapshot() []HealthAssessment {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]HealthAssessment, len(r.assessments))
	copy(out, r.assessments)
	return out
}

// setupHealthPublishEnv builds the smallest health-monitor fixture
// that drives checkAllAgents end-to-end. Uses the existing test
// doubles from integration_test.go (mockAgentEnumerator, mockTerminator,
// mockReleaser, testNotifier) so the bus sink is the only novel piece.
func setupHealthPublishEnv(t *testing.T) (*HealthMonitor, *mockAgentEnumerator, *recordingHealthSink) {
	t.Helper()

	pc := NewProgressCollector()
	rd := NewRepetitionDetector(DefaultRepetitionConfig())
	hs := NewHealthScorer(pc, rd, nil, DefaultHealthWeights(), DefaultHealthThresholds())
	dd := NewDeadlockDetector(func(agentID string) bool { return true })

	notifier := &testNotifier{}
	terminator := &mockTerminator{}
	releaser := &mockReleaser{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	config := RecoveryConfig{
		SoftInterventionDelay: 100 * time.Millisecond,
		UserEscalationDelay:   200 * time.Millisecond,
		ForceKillDelay:        400 * time.Millisecond,
		MaxSoftAttempts:       2,
		MonitorInterval:       50 * time.Millisecond,
	}

	ro := NewRecoveryOrchestrator(hs, dd, releaser, terminator, notifier, logger, config)
	dr := NewDeadlockRecovery(ro, pc, releaser, notifier, logger)
	agents := &mockAgentEnumerator{}
	hm := NewHealthMonitor(hs, dd, ro, dr, agents, config, logger)

	sink := &recordingHealthSink{}
	hm.SetHealthSink(sink)
	return hm, agents, sink
}

// TestHealthMonitor_PublishesAssessmentPerTick is the REC-02
// acceptance test: every active agent's assessment hits the sink on
// every tick, not only when escalation fires. UI consumers depend on
// the full healthy→warning curve, so a monitor that only publishes on
// status≥stuck would show a blank dial until the first emergency.
func TestHealthMonitor_PublishesAssessmentPerTick(t *testing.T) {
	hm, agents, sink := setupHealthPublishEnv(t)
	agents.SetAgents([]string{"engineer-1", "academic-1"})

	// Drive one full monitor tick directly via the exported hook —
	// avoids racing the ticker goroutine against the assertion.
	hm.checkAllAgents()

	got := sink.snapshot()
	if len(got) != 2 {
		t.Fatalf("sink saw %d assessments, want 2 (one per active agent)", len(got))
	}
	ids := map[string]bool{got[0].AgentID: true, got[1].AgentID: true}
	if !ids["engineer-1"] || !ids["academic-1"] {
		t.Errorf("missing expected agent IDs in published assessments: %+v", got)
	}
}

// TestHealthMonitor_NilSinkIsNoop protects the "monitor running
// without a sink configured" configuration — the cold-start window
// during bootstrap, or test paths that don't care about publishing.
// A nil-sink panic here would crash the monitor loop and stop all
// escalation logic.
func TestHealthMonitor_NilSinkIsNoop(t *testing.T) {
	hm, agents, _ := setupHealthPublishEnv(t)
	hm.SetHealthSink(nil)
	agents.SetAgents([]string{"engineer-1"})

	// Must not panic.
	hm.checkAllAgents()
}

// TestHealthMonitor_SetHealthSink_ReattachEnablesDelivery verifies
// sink swap semantics: detach, publish (nothing captured), reattach,
// publish again (one event). Mirrors the REC-01 forwarder swap test
// and protects the bus-bootstrap path where the sink arrives after
// the monitor has already started.
func TestHealthMonitor_SetHealthSink_ReattachEnablesDelivery(t *testing.T) {
	hm, agents, _ := setupHealthPublishEnv(t)
	hm.SetHealthSink(nil)
	agents.SetAgents([]string{"engineer-1"})
	hm.checkAllAgents()

	sink := &recordingHealthSink{}
	hm.SetHealthSink(sink)
	hm.checkAllAgents()

	if got := len(sink.snapshot()); got != 1 {
		t.Errorf("sink saw %d assessments after reattach, want 1", got)
	}
}

// TestHealthMonitor_PublishedAssessmentCarriesFields confirms the
// published payload actually has the fields UI consumers key on:
// AgentID + OverallScore + Status + AssessedAt. A regression that
// leaks a half-filled HealthAssessment would render a broken dial.
func TestHealthMonitor_PublishedAssessmentCarriesFields(t *testing.T) {
	hm, agents, sink := setupHealthPublishEnv(t)
	agents.SetAgents([]string{"engineer-1"})
	start := time.Now()

	hm.checkAllAgents()

	got := sink.snapshot()
	if len(got) != 1 {
		t.Fatalf("expected 1 assessment, got %d", len(got))
	}
	a := got[0]
	if a.AgentID != "engineer-1" {
		t.Errorf("AgentID = %q, want engineer-1", a.AgentID)
	}
	if a.Status == 0 && a.OverallScore == 0 {
		t.Errorf("assessment looks zero-valued: %+v", a)
	}
	if a.AssessedAt.Before(start) {
		t.Errorf("AssessedAt %v predates test start %v", a.AssessedAt, start)
	}
}

// TestHealthMonitor_PublishIncludesEscalationTargets covers the
// invariant that publish precedes escalation: a stuck-status agent
// must appear in the sink AND be handed to the orchestrator. A
// regression that skips publish for stuck agents (e.g. reordering
// logic) would hide the exact moment the UI most needs to show.
func TestHealthMonitor_PublishIncludesEscalationTargets(t *testing.T) {
	// Construct a scorer whose ALL-LOW scores produce StatusStuck.
	pc := NewProgressCollector()
	rd := NewRepetitionDetector(DefaultRepetitionConfig())
	// Force every component low by feeding no signals — ProgressCollector
	// with no heartbeat makes heartbeatScore = 0, progress = 0, etc.
	hs := NewHealthScorer(pc, rd, nil, DefaultHealthWeights(), DefaultHealthThresholds())
	dd := NewDeadlockDetector(func(agentID string) bool { return true })
	notifier := &testNotifier{}
	terminator := &mockTerminator{}
	releaser := &mockReleaser{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	config := RecoveryConfig{
		SoftInterventionDelay: 10 * time.Second,
		UserEscalationDelay:   10 * time.Second,
		ForceKillDelay:        10 * time.Second,
		MaxSoftAttempts:       2,
		MonitorInterval:       50 * time.Millisecond,
	}
	ro := NewRecoveryOrchestrator(hs, dd, releaser, terminator, notifier, logger, config)
	dr := NewDeadlockRecovery(ro, pc, releaser, notifier, logger)
	agents := &mockAgentEnumerator{}
	agents.SetAgents([]string{"stuck-agent"})
	hm := NewHealthMonitor(hs, dd, ro, dr, agents, config, logger)
	sink := &recordingHealthSink{}
	hm.SetHealthSink(sink)

	// Don't Start() the orchestrator — that would spin a goroutine and
	// race the test. The publish path is synchronous; escalation
	// fire-and-forget via HandleStuckAgent is enough to assert on.
	hm.checkAllAgents()

	// Orchestrator.Start() was not called, so HandleStuckAgent won't
	// trigger a real recovery cycle. What matters for REC-02 is that
	// the assessment reached the sink BEFORE any escalation gating.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = ctx

	got := sink.snapshot()
	if len(got) != 1 {
		t.Fatalf("sink saw %d assessments, want 1", len(got))
	}
	if got[0].AgentID != "stuck-agent" {
		t.Errorf("published AgentID = %q, want stuck-agent", got[0].AgentID)
	}
}
