package recovery

import (
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"
)

type mockTerminator struct {
	mu         sync.Mutex
	terminated []string
	failNext   bool
	failErr    error
}

func (m *mockTerminator) TerminateAgent(agentID string, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.terminated = append(m.terminated, agentID)
	if m.failNext {
		m.failNext = false
		return m.failErr
	}
	return nil
}

type mockReleaser struct {
	mu       sync.Mutex
	released map[string][]string
}

func (m *mockReleaser) ForceReleaseByAgent(agentID string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.released == nil {
		m.released = make(map[string][]string)
	}
	resources := []string{"resource-1", "resource-2"}
	m.released[agentID] = resources
	return resources
}

type testNotifier struct {
	mu              sync.Mutex
	breakouts       []string
	escalations     []string
	forceKills      []string
	reacquireNotifs []string
	failBreakout    bool
	failEscalation  bool
	breakoutErr     error
	escalationErr   error
}

func (n *testNotifier) InjectBreakoutPrompt(agentID string, prompt string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.breakouts = append(n.breakouts, agentID)
	if n.failBreakout {
		return n.breakoutErr
	}
	return nil
}

func (n *testNotifier) EscalateToUser(sessionID, agentID string, _ HealthAssessment) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.escalations = append(n.escalations, agentID)
	if n.failEscalation {
		return n.escalationErr
	}
	return nil
}

func (n *testNotifier) OnUserResponse(agentID string, _ UserRecoveryResponse) {}

func (n *testNotifier) NotifyForceKill(sessionID, agentID string, reason string) {
	n.mu.Lock()
	n.forceKills = append(n.forceKills, agentID)
	n.mu.Unlock()
}

func (n *testNotifier) NotifyReacquireResources(agentID string, resources []string) {
	n.mu.Lock()
	n.reacquireNotifs = append(n.reacquireNotifs, agentID)
	n.mu.Unlock()
}

func newTestOrchestrator() (*RecoveryOrchestrator, *testNotifier, *mockTerminator) {
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
		MonitorInterval:       5 * time.Second,
	}

	ro := NewRecoveryOrchestrator(hs, dd, releaser, terminator, notifier, logger, config)
	return ro, notifier, terminator
}

func newTestOrchestratorWithComponents() (*RecoveryOrchestrator, *testNotifier, *mockTerminator, *mockReleaser) {
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
		MonitorInterval:       5 * time.Second,
	}

	ro := NewRecoveryOrchestrator(hs, dd, releaser, terminator, notifier, logger, config)
	return ro, notifier, terminator, releaser
}

func TestRecoveryOrchestrator_HealthyAgentNoAction(t *testing.T) {
	ro, notifier, _ := newTestOrchestrator()

	assessment := HealthAssessment{
		AgentID: "agent-1",
		Status:  StatusHealthy,
	}

	err := ro.HandleStuckAgent(assessment)
	if err != nil {
		t.Errorf("HandleStuckAgent() returned unexpected error: %v", err)
	}

	if len(notifier.breakouts) > 0 {
		t.Error("Should not send breakout for healthy agent")
	}
	if len(notifier.escalations) > 0 {
		t.Error("Should not escalate for healthy agent")
	}
}

func TestRecoveryOrchestrator_SoftIntervention(t *testing.T) {
	ro, notifier, _ := newTestOrchestrator()

	stuckTime := time.Now().Add(-35 * time.Second)
	assessment := HealthAssessment{
		AgentID:    "agent-1",
		SessionID:  "session-1",
		Status:     StatusStuck,
		StuckSince: &stuckTime,
	}

	err := ro.HandleStuckAgent(assessment)
	if err != nil {
		t.Errorf("HandleStuckAgent() returned unexpected error: %v", err)
	}

	if len(notifier.breakouts) != 1 {
		t.Errorf("Expected 1 breakout, got %d", len(notifier.breakouts))
	}
}

func TestRecoveryOrchestrator_MaxSoftAttempts(t *testing.T) {
	ro, notifier, _ := newTestOrchestrator()

	stuckTime := time.Now().Add(-35 * time.Second)
	assessment := HealthAssessment{
		AgentID:    "agent-1",
		SessionID:  "session-1",
		Status:     StatusStuck,
		StuckSince: &stuckTime,
	}

	_ = ro.HandleStuckAgent(assessment)
	_ = ro.HandleStuckAgent(assessment)
	_ = ro.HandleStuckAgent(assessment)

	if len(notifier.breakouts) != 2 {
		t.Errorf("Expected max 2 breakouts, got %d", len(notifier.breakouts))
	}
}

func TestRecoveryOrchestrator_UserEscalation(t *testing.T) {
	ro, notifier, _ := newTestOrchestrator()

	stuckTime := time.Now().Add(-65 * time.Second)
	assessment := HealthAssessment{
		AgentID:    "agent-1",
		SessionID:  "session-1",
		Status:     StatusStuck,
		StuckSince: &stuckTime,
	}

	err := ro.HandleStuckAgent(assessment)
	if err != nil {
		t.Errorf("HandleStuckAgent() returned unexpected error: %v", err)
	}

	if len(notifier.escalations) != 1 {
		t.Errorf("Expected 1 escalation, got %d", len(notifier.escalations))
	}
}

func TestRecoveryOrchestrator_ForceKill(t *testing.T) {
	ro, notifier, terminator := newTestOrchestrator()

	stuckTime := time.Now().Add(-125 * time.Second)
	assessment := HealthAssessment{
		AgentID:    "agent-1",
		SessionID:  "session-1",
		Status:     StatusCritical,
		StuckSince: &stuckTime,
	}

	err := ro.HandleStuckAgent(assessment)
	if err != nil {
		t.Errorf("HandleStuckAgent() returned unexpected error: %v", err)
	}

	if len(notifier.forceKills) != 1 {
		t.Errorf("Expected 1 force kill notification, got %d", len(notifier.forceKills))
	}
	if len(terminator.terminated) != 1 {
		t.Errorf("Expected 1 termination, got %d", len(terminator.terminated))
	}
}

func TestRecoveryOrchestrator_UserWaitPreventsForceKill(t *testing.T) {
	ro, notifier, terminator := newTestOrchestrator()

	stuckTime := time.Now().Add(-65 * time.Second)
	assessment := HealthAssessment{
		AgentID:    "agent-1",
		SessionID:  "session-1",
		Status:     StatusStuck,
		StuckSince: &stuckTime,
	}

	_ = ro.HandleStuckAgent(assessment)

	ro.SetUserResponse("agent-1", UserRecoveryResponse{
		Action:    UserActionWait,
		Timestamp: time.Now(),
	})

	stuckTime = time.Now().Add(-125 * time.Second)
	assessment.StuckSince = &stuckTime
	_ = ro.HandleStuckAgent(assessment)

	if len(terminator.terminated) > 0 {
		t.Error("Should not terminate when user chose to wait")
	}
	if len(notifier.forceKills) > 0 {
		t.Error("Should not notify force kill when user chose to wait")
	}
}

func TestRecoveryOrchestrator_UserKillAction(t *testing.T) {
	ro, _, terminator := newTestOrchestrator()

	stuckTime := time.Now().Add(-65 * time.Second)
	assessment := HealthAssessment{
		AgentID:    "agent-1",
		SessionID:  "session-1",
		Status:     StatusStuck,
		StuckSince: &stuckTime,
	}

	_ = ro.HandleStuckAgent(assessment)

	ro.SetUserResponse("agent-1", UserRecoveryResponse{
		Action:    UserActionKill,
		Timestamp: time.Now(),
	})

	_ = ro.HandleStuckAgent(assessment)

	if len(terminator.terminated) != 1 {
		t.Errorf("Expected 1 termination after user kill, got %d", len(terminator.terminated))
	}
}

func TestRecoveryOrchestrator_AgentRecovery(t *testing.T) {
	ro, notifier, _ := newTestOrchestrator()

	stuckTime := time.Now().Add(-125 * time.Second)
	assessment := HealthAssessment{
		AgentID:    "agent-1",
		SessionID:  "session-1",
		Status:     StatusCritical,
		StuckSince: &stuckTime,
	}

	_ = ro.HandleStuckAgent(assessment)

	assessment.Status = StatusHealthy
	assessment.StuckSince = nil
	_ = ro.HandleStuckAgent(assessment)

	if len(notifier.reacquireNotifs) != 1 {
		t.Errorf("Expected 1 reacquire notification, got %d", len(notifier.reacquireNotifs))
	}
}

func TestRecoveryOrchestrator_GetState(t *testing.T) {
	ro, _, _ := newTestOrchestrator()

	_, ok := ro.GetState("nonexistent")
	if ok {
		t.Error("GetState should return false for nonexistent agent")
	}

	stuckTime := time.Now().Add(-35 * time.Second)
	_ = ro.HandleStuckAgent(HealthAssessment{
		AgentID:    "agent-1",
		SessionID:  "session-1",
		Status:     StatusStuck,
		StuckSince: &stuckTime,
	})

	state, ok := ro.GetState("agent-1")
	if !ok {
		t.Error("GetState should return true after handling stuck agent")
	}
	if state.Level != RecoverySoftIntervention {
		t.Errorf("State level = %d, want RecoverySoftIntervention", state.Level)
	}
}

func TestRecoveryOrchestrator_RepetitionConcernPrompt(t *testing.T) {
	ro, _, _ := newTestOrchestrator()

	assessment := HealthAssessment{
		AgentID:           "agent-1",
		RepetitionConcern: true,
	}

	prompt := ro.buildBreakoutPrompt(assessment)
	if prompt == "" {
		t.Error("Breakout prompt should not be empty")
	}
	if len(prompt) < 50 {
		t.Error("Breakout prompt should be descriptive")
	}
}

func TestRecoveryOrchestrator_NilStuckSince(t *testing.T) {
	ro, notifier, _ := newTestOrchestrator()

	assessment := HealthAssessment{
		AgentID:    "agent-1",
		Status:     StatusStuck,
		StuckSince: nil,
	}

	err := ro.HandleStuckAgent(assessment)
	if err != nil {
		t.Errorf("HandleStuckAgent() returned unexpected error: %v", err)
	}

	if len(notifier.breakouts) > 0 {
		t.Error("Should not intervene without StuckSince")
	}
}

// Error propagation tests

func TestRecoveryOrchestrator_SoftInterventionError(t *testing.T) {
	ro, notifier, _, _ := newTestOrchestratorWithComponents()

	// Configure notifier to fail breakout
	notifier.failBreakout = true
	notifier.breakoutErr = errors.New("injection failed")

	stuckTime := time.Now().Add(-35 * time.Second)
	assessment := HealthAssessment{
		AgentID:    "agent-1",
		SessionID:  "session-1",
		Status:     StatusStuck,
		StuckSince: &stuckTime,
	}

	err := ro.HandleStuckAgent(assessment)

	if err == nil {
		t.Error("HandleStuckAgent() should return error when soft intervention fails")
	}
	if !errors.Is(err, ErrSoftInterventionFailed) {
		t.Errorf("error should wrap ErrSoftInterventionFailed, got: %v", err)
	}
}

func TestRecoveryOrchestrator_UserEscalationError(t *testing.T) {
	ro, notifier, _, _ := newTestOrchestratorWithComponents()

	// Configure notifier to fail escalation
	notifier.failEscalation = true
	notifier.escalationErr = errors.New("escalation failed")

	stuckTime := time.Now().Add(-65 * time.Second)
	assessment := HealthAssessment{
		AgentID:    "agent-1",
		SessionID:  "session-1",
		Status:     StatusStuck,
		StuckSince: &stuckTime,
	}

	err := ro.HandleStuckAgent(assessment)

	if err == nil {
		t.Error("HandleStuckAgent() should return error when escalation fails")
	}
	if !errors.Is(err, ErrUserEscalationFailed) {
		t.Errorf("error should wrap ErrUserEscalationFailed, got: %v", err)
	}
}

func TestRecoveryOrchestrator_TerminationError(t *testing.T) {
	ro, _, terminator, _ := newTestOrchestratorWithComponents()

	// Configure terminator to fail
	terminator.failNext = true
	terminator.failErr = errors.New("termination failed")

	stuckTime := time.Now().Add(-125 * time.Second)
	assessment := HealthAssessment{
		AgentID:    "agent-1",
		SessionID:  "session-1",
		Status:     StatusCritical,
		StuckSince: &stuckTime,
	}

	err := ro.HandleStuckAgent(assessment)

	if err == nil {
		t.Error("HandleStuckAgent() should return error when termination fails")
	}
	if !errors.Is(err, ErrAgentTerminationFailed) {
		t.Errorf("error should wrap ErrAgentTerminationFailed, got: %v", err)
	}
}

func TestRecoveryOrchestrator_ErrorContainsOriginal(t *testing.T) {
	ro, notifier, _, _ := newTestOrchestratorWithComponents()

	originalErr := errors.New("underlying cause")
	notifier.failBreakout = true
	notifier.breakoutErr = originalErr

	stuckTime := time.Now().Add(-35 * time.Second)
	assessment := HealthAssessment{
		AgentID:    "agent-1",
		SessionID:  "session-1",
		Status:     StatusStuck,
		StuckSince: &stuckTime,
	}

	err := ro.HandleStuckAgent(assessment)

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Check that original error is wrapped
	if !errors.Is(err, originalErr) {
		t.Errorf("error chain should contain original error: %v", err)
	}
}

func TestRecoveryOrchestrator_NoErrorOnSuccess(t *testing.T) {
	ro, _, _, _ := newTestOrchestratorWithComponents()

	// Test soft intervention success
	stuckTime := time.Now().Add(-35 * time.Second)
	assessment := HealthAssessment{
		AgentID:    "agent-1",
		SessionID:  "session-1",
		Status:     StatusStuck,
		StuckSince: &stuckTime,
	}

	err := ro.HandleStuckAgent(assessment)
	if err != nil {
		t.Errorf("HandleStuckAgent() returned error on success: %v", err)
	}

	// Test user escalation success
	stuckTime = time.Now().Add(-65 * time.Second)
	assessment2 := HealthAssessment{
		AgentID:    "agent-2",
		SessionID:  "session-2",
		Status:     StatusStuck,
		StuckSince: &stuckTime,
	}

	err = ro.HandleStuckAgent(assessment2)
	if err != nil {
		t.Errorf("HandleStuckAgent() returned error on escalation success: %v", err)
	}

	// Test force kill success
	stuckTime = time.Now().Add(-125 * time.Second)
	assessment3 := HealthAssessment{
		AgentID:    "agent-3",
		SessionID:  "session-3",
		Status:     StatusCritical,
		StuckSince: &stuckTime,
	}

	err = ro.HandleStuckAgent(assessment3)
	if err != nil {
		t.Errorf("HandleStuckAgent() returned error on force kill success: %v", err)
	}
}

func TestRecoveryOrchestrator_ConcurrentHandling(t *testing.T) {
	ro, _, _, _ := newTestOrchestratorWithComponents()

	const goroutines = 10
	const iterations = 50

	done := make(chan struct{})

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer func() { done <- struct{}{} }()
			for j := 0; j < iterations; j++ {
				stuckTime := time.Now().Add(-35 * time.Second)
				_ = ro.HandleStuckAgent(HealthAssessment{
					AgentID:    "agent-concurrent",
					SessionID:  "session-concurrent",
					Status:     StatusStuck,
					StuckSince: &stuckTime,
				})
			}
		}(i)
	}

	for i := 0; i < goroutines; i++ {
		<-done
	}
}
