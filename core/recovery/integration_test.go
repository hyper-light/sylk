package recovery

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"
)

type integrationTestEnv struct {
	collector     *ProgressCollector
	repetitionDet *RepetitionDetector
	healthScorer  *HealthScorer
	deadlockDet   *DeadlockDetector
	orchestrator  *RecoveryOrchestrator
	deadlockRecov *DeadlockRecovery
	healthMonitor *HealthMonitor
	emitter       *SignalEmitter
	agents        *mockAgentEnumerator
	notifier      *testNotifier
	terminator    *mockTerminator
}

func setupIntegrationTest() *integrationTestEnv {
	pc := NewProgressCollector()
	rd := NewRepetitionDetector(DefaultRepetitionConfig())
	hs := NewHealthScorer(pc, rd, nil, DefaultHealthWeights(), DefaultHealthThresholds())
	dd := NewDeadlockDetector(func(agentID string) bool {
		return agentID != "dead-agent"
	})

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
	emitter := NewSignalEmitter(pc, rd)

	return &integrationTestEnv{
		collector:     pc,
		repetitionDet: rd,
		healthScorer:  hs,
		deadlockDet:   dd,
		orchestrator:  ro,
		deadlockRecov: dr,
		healthMonitor: hm,
		emitter:       emitter,
		agents:        agents,
		notifier:      notifier,
		terminator:    terminator,
	}
}

func TestIntegration_HealthyAgentFlow(t *testing.T) {
	env := setupIntegrationTest()

	env.agents.SetAgents([]string{"agent-1"})

	for i := 0; i < 20; i++ {
		env.emitter.EmitToolCompleted("agent-1", "session-1", "Read", "/file"+string(rune('A'+i))+".go")
		env.emitter.EmitLLMResponse("agent-1", "session-1")
	}

	ctx, cancel := context.WithCancel(context.Background())
	env.healthMonitor.Start(ctx)

	time.Sleep(200 * time.Millisecond)

	cancel()
	env.healthMonitor.Stop()

	env.notifier.mu.Lock()
	breakouts := len(env.notifier.breakouts)
	escalations := len(env.notifier.escalations)
	forceKills := len(env.notifier.forceKills)
	env.notifier.mu.Unlock()

	if breakouts > 0 || escalations > 0 || forceKills > 0 {
		t.Error("Healthy agent should not trigger any recovery actions")
	}
}

func TestIntegration_StuckAgentRecoveryFlow(t *testing.T) {
	env := setupIntegrationTest()

	stuckTime := time.Now().Add(-500 * time.Millisecond)
	assessment := HealthAssessment{
		AgentID:    "agent-1",
		SessionID:  "session-1",
		Status:     StatusCritical,
		StuckSince: &stuckTime,
	}

	env.orchestrator.HandleStuckAgent(assessment)

	env.terminator.mu.Lock()
	terminated := len(env.terminator.terminated)
	env.terminator.mu.Unlock()

	if terminated == 0 {
		t.Error("Stuck agent should be terminated when force kill threshold exceeded")
	}
}

func TestIntegration_DeadlockDetectionAndRecovery(t *testing.T) {
	env := setupIntegrationTest()

	env.deadlockDet.RegisterWait("agent-1", "dead-agent", "file", "f1")

	results := env.deadlockDet.Check()
	foundDeadlock := false
	for _, result := range results {
		if result.Detected && result.Type == DeadlockDeadHolder {
			foundDeadlock = true
			env.deadlockRecov.HandleDeadlock(result)
		}
	}

	if !foundDeadlock {
		t.Fatal("Expected dead holder deadlock to be detected")
	}

	env.notifier.mu.Lock()
	forceKills := len(env.notifier.forceKills)
	env.notifier.mu.Unlock()

	if forceKills == 0 {
		t.Error("Dead holder deadlock should trigger force kill of dead agent")
	}
}

func TestIntegration_SignalEmitterToHealthScorer(t *testing.T) {
	env := setupIntegrationTest()

	for i := 0; i < 10; i++ {
		env.emitter.EmitToolCompleted("agent-1", "session-1", "Read", "/file"+string(rune('A'+i))+".go")
	}

	assessment := env.healthScorer.Assess("agent-1")

	if assessment.Status != StatusHealthy {
		t.Errorf("Agent with varied signals should be healthy, got status %d", assessment.Status)
	}
	if assessment.ProgressScore < 0.5 {
		t.Errorf("Agent with varied signals should have good progress score, got %v", assessment.ProgressScore)
	}
}

func TestIntegration_RepetitionDetection(t *testing.T) {
	env := setupIntegrationTest()

	env.collector.Signal(ProgressSignal{
		AgentID:   "agent-1",
		Timestamp: time.Now().Add(-40 * time.Second),
	})

	for i := 0; i < 30; i++ {
		env.emitter.EmitToolCompleted("agent-1", "session-1", "Read", "/same/file.go")
	}

	assessment := env.healthScorer.Assess("agent-1")

	if !assessment.RepetitionConcern {
		t.Error("Repetitive operations with stale heartbeat should trigger repetition concern")
	}
}

func TestIntegration_ConcurrentSignals(t *testing.T) {
	env := setupIntegrationTest()

	env.agents.SetAgents([]string{"agent-1", "agent-2", "agent-3"})

	var wg sync.WaitGroup
	for _, agentID := range []string{"agent-1", "agent-2", "agent-3"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				env.emitter.EmitToolCompleted(id, "session-1", "Read", "/file"+string(rune('A'+i%26))+".go")
			}
		}(agentID)
	}

	ctx, cancel := context.WithCancel(context.Background())
	env.healthMonitor.Start(ctx)

	wg.Wait()
	time.Sleep(100 * time.Millisecond)

	cancel()
	env.healthMonitor.Stop()

	for _, agentID := range []string{"agent-1", "agent-2", "agent-3"} {
		if env.collector.SignalCount(agentID) != 100 {
			t.Errorf("Agent %s should have 100 signals, got %d", agentID, env.collector.SignalCount(agentID))
		}
	}
}

func TestIntegration_UserResponseWait(t *testing.T) {
	env := setupIntegrationTest()

	env.agents.SetAgents([]string{"agent-1"})

	env.collector.Signal(ProgressSignal{
		AgentID:   "agent-1",
		Timestamp: time.Now().Add(-300 * time.Millisecond),
	})

	ctx, cancel := context.WithCancel(context.Background())
	env.healthMonitor.Start(ctx)

	time.Sleep(300 * time.Millisecond)

	env.orchestrator.SetUserResponse("agent-1", UserRecoveryResponse{
		Action:    UserActionWait,
		Timestamp: time.Now(),
	})

	time.Sleep(300 * time.Millisecond)

	cancel()
	env.healthMonitor.Stop()

	env.terminator.mu.Lock()
	terminated := len(env.terminator.terminated)
	env.terminator.mu.Unlock()

	if terminated > 0 {
		t.Error("Agent should not be terminated when user chose to wait")
	}
}

func TestIntegration_CircularDeadlockRecovery(t *testing.T) {
	pc := NewProgressCollector()

	for i := 0; i < 50; i++ {
		pc.Signal(ProgressSignal{AgentID: "A"})
	}
	for i := 0; i < 10; i++ {
		pc.Signal(ProgressSignal{AgentID: "B"})
	}
	for i := 0; i < 30; i++ {
		pc.Signal(ProgressSignal{AgentID: "C"})
	}

	rd := NewRepetitionDetector(DefaultRepetitionConfig())
	hs := NewHealthScorer(pc, rd, nil, DefaultHealthWeights(), DefaultHealthThresholds())
	dd := NewDeadlockDetector(func(_ string) bool { return true })

	notifier := &testNotifier{}
	terminator := &mockTerminator{}
	releaser := &mockReleaser{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	config := DefaultRecoveryConfig()
	ro := NewRecoveryOrchestrator(hs, dd, releaser, terminator, notifier, logger, config)
	dr := NewDeadlockRecovery(ro, pc, releaser, notifier, logger)

	dd.RegisterWait("A", "B", "file", "f1")
	dd.RegisterWait("B", "C", "file", "f2")
	dd.RegisterWait("C", "A", "file", "f3")

	results := dd.Check()

	for _, result := range results {
		if result.Detected {
			dr.HandleDeadlock(result)
		}
	}

	releaser.mu.Lock()
	releasedB := releaser.released["B"]
	releaser.mu.Unlock()

	if len(releasedB) == 0 {
		t.Error("Agent B (least progress) should be selected as victim and have resources released")
	}
}
