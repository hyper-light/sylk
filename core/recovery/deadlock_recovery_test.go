package recovery

import (
	"log/slog"
	"os"
	"testing"
)

func newTestDeadlockRecovery() (*DeadlockRecovery, *testNotifier, *mockReleaser) {
	pc := NewProgressCollector()
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
	return dr, notifier, releaser
}

func TestDeadlockRecovery_NoDeadlock(t *testing.T) {
	dr, notifier, releaser := newTestDeadlockRecovery()

	result := DeadlockResult{Detected: false}
	dr.HandleDeadlock(result)

	if len(releaser.released) > 0 {
		t.Error("Should not release resources when no deadlock")
	}
	if len(notifier.escalations) > 0 {
		t.Error("Should not escalate when no deadlock")
	}
}

func TestDeadlockRecovery_DeadHolderDeadlock(t *testing.T) {
	dr, _, releaser := newTestDeadlockRecovery()

	result := DeadlockResult{
		Detected:      true,
		Type:          DeadlockDeadHolder,
		DeadHolder:    "dead-agent",
		WaitingAgents: []string{"waiting-agent"},
		ResourceType:  "file",
		ResourceID:    "f1",
	}

	dr.HandleDeadlock(result)

	releaser.mu.Lock()
	released := releaser.released["dead-agent"]
	releaser.mu.Unlock()

	if len(released) == 0 {
		t.Error("Should release dead agent's resources")
	}
}

func TestDeadlockRecovery_CircularDeadlock(t *testing.T) {
	dr, notifier, releaser := newTestDeadlockRecovery()

	result := DeadlockResult{
		Detected: true,
		Type:     DeadlockCircular,
		Cycle:    []string{"A", "B", "C"},
	}

	dr.HandleDeadlock(result)

	releaser.mu.Lock()
	releasedCount := len(releaser.released)
	releaser.mu.Unlock()

	if releasedCount == 0 {
		t.Error("Should release victim's resources")
	}

	notifier.mu.Lock()
	escalationCount := len(notifier.escalations)
	notifier.mu.Unlock()

	if escalationCount == 0 {
		t.Error("Should escalate circular deadlock to user")
	}
}

func TestDeadlockRecovery_VictimSelection(t *testing.T) {
	pc := NewProgressCollector()

	for i := 0; i < 100; i++ {
		pc.Signal(ProgressSignal{AgentID: "A"})
	}
	for i := 0; i < 10; i++ {
		pc.Signal(ProgressSignal{AgentID: "B"})
	}
	for i := 0; i < 50; i++ {
		pc.Signal(ProgressSignal{AgentID: "C"})
	}

	rd := NewRepetitionDetector(DefaultRepetitionConfig())
	hs := NewHealthScorer(pc, rd, nil, DefaultHealthWeights(), DefaultHealthThresholds())
	dd := NewDeadlockDetector(func(_ string) bool { return true })

	notifier := &testNotifier{}
	terminator := &mockTerminator{}
	releaser := &mockReleaser{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	ro := NewRecoveryOrchestrator(hs, dd, releaser, terminator, notifier, logger, DefaultRecoveryConfig())
	dr := NewDeadlockRecovery(ro, pc, releaser, notifier, logger)

	victim := dr.selectDeadlockVictim([]string{"A", "B", "C"})
	if victim != "B" {
		t.Errorf("Victim should be B (least progress), got %s", victim)
	}
}

func TestDeadlockRecovery_EmptyCycle(t *testing.T) {
	dr, _, _ := newTestDeadlockRecovery()

	victim := dr.selectDeadlockVictim([]string{})
	if victim != "" {
		t.Errorf("Empty cycle should return empty victim, got %s", victim)
	}
}

func TestDeadlockRecovery_SingleAgentCycle(t *testing.T) {
	dr, notifier, releaser := newTestDeadlockRecovery()

	result := DeadlockResult{
		Detected: true,
		Type:     DeadlockCircular,
		Cycle:    []string{"A", "A"},
	}

	dr.HandleDeadlock(result)

	releaser.mu.Lock()
	released := releaser.released["A"]
	releaser.mu.Unlock()

	if len(released) == 0 {
		t.Error("Should release self-deadlocked agent's resources")
	}

	notifier.mu.Lock()
	escalationCount := len(notifier.escalations)
	notifier.mu.Unlock()

	if escalationCount == 0 {
		t.Error("Should escalate self-deadlock to user")
	}
}
