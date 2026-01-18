package recovery

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"
)

type mockAgentEnumerator struct {
	mu     sync.Mutex
	agents []string
}

func (m *mockAgentEnumerator) ActiveAgentIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string{}, m.agents...)
}

func (m *mockAgentEnumerator) SetAgents(agents []string) {
	m.mu.Lock()
	m.agents = agents
	m.mu.Unlock()
}

func newTestHealthMonitor() (*HealthMonitor, *ProgressCollector, *mockAgentEnumerator) {
	pc := NewProgressCollector()
	rd := NewRepetitionDetector(DefaultRepetitionConfig())
	hs := NewHealthScorer(pc, rd, nil, DefaultHealthWeights(), DefaultHealthThresholds())
	dd := NewDeadlockDetector(func(_ string) bool { return true })

	notifier := &testNotifier{}
	terminator := &mockTerminator{}
	releaser := &mockReleaser{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	config := RecoveryConfig{
		SoftInterventionDelay: 30 * time.Second,
		UserEscalationDelay:   60 * time.Second,
		ForceKillDelay:        120 * time.Second,
		MaxSoftAttempts:       2,
		MonitorInterval:       50 * time.Millisecond,
	}

	ro := NewRecoveryOrchestrator(hs, dd, releaser, terminator, notifier, logger, config)
	dr := NewDeadlockRecovery(ro, pc, releaser, notifier, logger)

	agents := &mockAgentEnumerator{}

	hm := NewHealthMonitor(hs, dd, ro, dr, agents, config, logger)
	return hm, pc, agents
}

func TestHealthMonitor_StartStop(t *testing.T) {
	hm, _, _ := newTestHealthMonitor()

	if hm.IsRunning() {
		t.Error("Monitor should not be running initially")
	}

	ctx := context.Background()
	hm.Start(ctx)

	time.Sleep(10 * time.Millisecond)
	if !hm.IsRunning() {
		t.Error("Monitor should be running after Start")
	}

	hm.Stop()

	time.Sleep(10 * time.Millisecond)
	if hm.IsRunning() {
		t.Error("Monitor should not be running after Stop")
	}
}

func TestHealthMonitor_DoubleStart(t *testing.T) {
	hm, _, _ := newTestHealthMonitor()

	ctx := context.Background()
	hm.Start(ctx)
	hm.Start(ctx)

	time.Sleep(10 * time.Millisecond)
	if !hm.IsRunning() {
		t.Error("Monitor should still be running")
	}

	hm.Stop()
}

func TestHealthMonitor_DoubleStop(t *testing.T) {
	hm, _, _ := newTestHealthMonitor()

	ctx := context.Background()
	hm.Start(ctx)
	time.Sleep(10 * time.Millisecond)

	hm.Stop()
	hm.Stop()

	if hm.IsRunning() {
		t.Error("Monitor should not be running")
	}
}

func TestHealthMonitor_ContextCancel(t *testing.T) {
	hm, _, _ := newTestHealthMonitor()

	ctx, cancel := context.WithCancel(context.Background())
	hm.Start(ctx)

	time.Sleep(10 * time.Millisecond)
	cancel()

	time.Sleep(100 * time.Millisecond)
}

func TestHealthMonitor_AssessAgent(t *testing.T) {
	hm, pc, _ := newTestHealthMonitor()

	pc.Signal(ProgressSignal{
		AgentID:   "agent-1",
		Timestamp: time.Now(),
	})

	assessment := hm.AssessAgent("agent-1")

	if assessment.AgentID != "agent-1" {
		t.Errorf("AgentID = %s, want agent-1", assessment.AgentID)
	}
}

func TestHealthMonitor_ChecksAgents(t *testing.T) {
	hm, pc, agents := newTestHealthMonitor()

	agents.SetAgents([]string{"agent-1", "agent-2"})

	for i := 0; i < 10; i++ {
		pc.Signal(ProgressSignal{
			AgentID:   "agent-1",
			Timestamp: time.Now(),
		})
		pc.Signal(ProgressSignal{
			AgentID:   "agent-2",
			Timestamp: time.Now(),
		})
	}

	ctx := context.Background()
	hm.Start(ctx)

	time.Sleep(100 * time.Millisecond)

	hm.Stop()
}

func TestHealthMonitor_StopBeforeStart(t *testing.T) {
	hm, _, _ := newTestHealthMonitor()
	hm.Stop()

	if hm.IsRunning() {
		t.Error("Monitor should not be running")
	}
}
