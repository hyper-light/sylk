package handoff

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// testBridgeAgent implements HandoffableAgent for bridge tests.
type testBridgeAgent struct {
	id        string
	agentType string
	desc      AgentDescriptor
	stopped   atomic.Bool
}

func (a *testBridgeAgent) AgentID() string                          { return a.id }
func (a *testBridgeAgent) AgentType() string                        { return a.agentType }
func (a *testBridgeAgent) Descriptor() AgentDescriptor              { return a.desc }
func (a *testBridgeAgent) ExtractArchivableState() *ArchivableState { return nil }
func (a *testBridgeAgent) Terminate(_ context.Context) error        { a.stopped.Store(true); return nil }

func newTestBridgeAgent(id, agentType string, desc AgentDescriptor) *testBridgeAgent {
	return &testBridgeAgent{id: id, agentType: agentType, desc: desc}
}

// testEvictableAgent implements ContextEvictable for bridge tests.
type testEvictableAgent struct {
	testBridgeAgent
	evictedTokens int
	evictCalls    int
}

func (a *testEvictableAgent) EvictEntries(candidates []EvictionCandidate) (int, error) {
	a.evictCalls++
	total := 0
	for _, c := range candidates {
		total += c.Entry.GetTokenCount()
	}
	a.evictedTokens += total
	return total, nil
}

func newTestEvictableAgent(id, agentType string, desc AgentDescriptor) *testEvictableAgent {
	return &testEvictableAgent{
		testBridgeAgent: testBridgeAgent{id: id, agentType: agentType, desc: desc},
	}
}

// testInjectableAgent implements HandoffInjectable for bridge tests.
type testInjectableAgent struct {
	testBridgeAgent
	injected bool
}

func (a *testInjectableAgent) InjectPreparedContext(_ *PreparedContext) error {
	a.injected = true
	return nil
}

func newTestInjectableAgent(id, agentType string, desc AgentDescriptor) *testInjectableAgent {
	return &testInjectableAgent{
		testBridgeAgent: testBridgeAgent{id: id, agentType: agentType, desc: desc},
	}
}

// setupBridgeTest creates a supervisor and bridge for testing.
func setupBridgeTest(t *testing.T, agent HandoffableAgent) (*HandoffSupervisor, *HandoffBridge) {
	t.Helper()

	sup := NewHandoffSupervisor(&SupervisorConfig{
		EnableWAL:      false,
		EnableLearning: true,
	})
	if err := sup.Start(); err != nil {
		t.Fatalf("supervisor.Start: %v", err)
	}

	bridge, err := sup.RegisterAgent(agent)
	if err != nil {
		t.Fatalf("RegisterAgent: %v", err)
	}

	t.Cleanup(func() { _ = sup.Stop() })
	return sup, bridge
}

func TestBridge_StartStop(t *testing.T) {
	desc := AgentDescriptor{
		AgentType:     "guide",
		ModelID:       "haiku-4.5-200k",
		ContextWindow: 200_000,
		Category:      CategoryStandalone,
	}
	agent := newTestBridgeAgent("guide-1", "guide", desc)
	_, bridge := setupBridgeTest(t, agent)

	// Bridge should be started by RegisterAgent.
	if !bridge.started.Load() {
		t.Error("bridge should be started after RegisterAgent")
	}

	// Stop and verify.
	if err := bridge.Stop(); err != nil {
		t.Fatalf("bridge.Stop: %v", err)
	}
	if bridge.started.Load() {
		t.Error("bridge should not be started after Stop")
	}
}

func TestBridge_RecordTurn_PropagatesGP(t *testing.T) {
	desc := AgentDescriptor{
		AgentType:     "guide",
		ModelID:       "haiku-4.5-200k",
		ContextWindow: 200_000,
		Category:      CategoryStandalone,
	}
	agent := newTestBridgeAgent("guide-1", "guide", desc)
	_, bridge := setupBridgeTest(t, agent)

	rec := TurnRecord{
		ContextSize:   50_000,
		OutputTokens:  500,
		ToolCalls:     3,
		ToolSuccesses: 2,
		TurnNumber:    1,
		Duration:      100 * time.Millisecond,
		Timestamp:     time.Now(),
	}
	bridge.RecordTurn(rec)

	// GP should have one observation.
	if got := bridge.gp.NumObservations(); got != 1 {
		t.Errorf("gp.NumObservations = %d, want 1", got)
	}

	// Turn count should be 1.
	if got := bridge.turnCount.Load(); got != 1 {
		t.Errorf("turnCount = %d, want 1", got)
	}
}

func TestBridge_RecordTurn_PreparedContext(t *testing.T) {
	desc := AgentDescriptor{
		AgentType:     "architect",
		ModelID:       "opus-4.5-200k",
		ContextWindow: 200_000,
		Category:      CategoryKnowledge,
	}
	agent := newTestEvictableAgent("arch-1", "architect", desc)
	_, bridge := setupBridgeTest(t, agent)

	for i := range 5 {
		bridge.RecordTurn(TurnRecord{
			ContextSize:   (i + 1) * 10_000,
			OutputTokens:  200,
			ToolCalls:     1,
			ToolSuccesses: 1,
			TurnNumber:    i + 1,
			Timestamp:     time.Now(),
		})
	}

	// Prepared context should have 5 messages.
	msgs := bridge.prepared.RecentMessages()
	if len(msgs) != 5 {
		t.Errorf("prepared messages = %d, want 5", len(msgs))
	}
}

func TestBridge_RecordTurn_QualityHook(t *testing.T) {
	desc := AgentDescriptor{
		AgentType:     "guide",
		ModelID:       "haiku-4.5-200k",
		ContextWindow: 200_000,
		Category:      CategoryStandalone,
	}
	agent := newTestBridgeAgent("guide-1", "guide", desc)
	_, bridge := setupBridgeTest(t, agent)

	bridge.RecordTurn(TurnRecord{
		ContextSize:   50_000,
		OutputTokens:  500,
		ToolCalls:     4,
		ToolSuccesses: 3,
		TurnNumber:    1,
		Timestamp:     time.Now(),
	})

	// Quality hook should have a score.
	score := bridge.QualityScore()
	if score == nil {
		t.Fatal("QualityScore returned nil")
	}
}

func TestBridge_ReplaceAgent(t *testing.T) {
	desc := AgentDescriptor{
		AgentType:     "guide",
		ModelID:       "haiku-4.5-200k",
		ContextWindow: 200_000,
		Category:      CategoryStandalone,
	}
	agent1 := newTestBridgeAgent("guide-1", "guide", desc)
	_, bridge := setupBridgeTest(t, agent1)

	// Record some turns.
	bridge.RecordTurn(TurnRecord{
		ContextSize: 50_000, OutputTokens: 500, TurnNumber: 1, Timestamp: time.Now(),
	})

	if bridge.turnCount.Load() != 1 {
		t.Fatal("expected turnCount = 1")
	}

	// Replace agent.
	agent2 := newTestBridgeAgent("guide-2", "guide", desc)
	bridge.ReplaceAgent(agent2)

	// Verify replacement.
	if bridge.Agent().AgentID() != "guide-2" {
		t.Errorf("AgentID = %q, want %q", bridge.Agent().AgentID(), "guide-2")
	}

	// Turn count should reset.
	if bridge.turnCount.Load() != 0 {
		t.Error("turnCount should reset after ReplaceAgent")
	}
}

func TestBridge_Agent(t *testing.T) {
	desc := AgentDescriptor{
		AgentType:     "guide",
		ModelID:       "haiku-4.5-200k",
		ContextWindow: 200_000,
		Category:      CategoryStandalone,
	}
	agent := newTestBridgeAgent("guide-1", "guide", desc)
	_, bridge := setupBridgeTest(t, agent)

	if bridge.Agent().AgentID() != "guide-1" {
		t.Errorf("AgentID = %q, want %q", bridge.Agent().AgentID(), "guide-1")
	}
}

func TestBridge_Profile(t *testing.T) {
	desc := AgentDescriptor{
		AgentType:     "architect",
		ModelID:       "opus-4.5-200k",
		ContextWindow: 200_000,
		Category:      CategoryKnowledge,
	}
	agent := newTestEvictableAgent("arch-1", "architect", desc)
	_, bridge := setupBridgeTest(t, agent)

	profile := bridge.Profile()
	if profile == nil {
		t.Fatal("Profile returned nil")
	}
	if profile.AgentType != "architect" {
		t.Errorf("profile.AgentType = %q, want %q", profile.AgentType, "architect")
	}
}

func TestBridge_MultipleTurns_GPGrows(t *testing.T) {
	desc := AgentDescriptor{
		AgentType:     "guide",
		ModelID:       "haiku-4.5-200k",
		ContextWindow: 200_000,
		Category:      CategoryStandalone,
	}
	agent := newTestBridgeAgent("guide-1", "guide", desc)
	_, bridge := setupBridgeTest(t, agent)

	for i := range 10 {
		bridge.RecordTurn(TurnRecord{
			ContextSize:   (i + 1) * 5_000,
			OutputTokens:  200 + i*50,
			ToolCalls:     i % 3,
			ToolSuccesses: i % 3,
			TurnNumber:    i + 1,
			Timestamp:     time.Now(),
		})
	}

	if got := bridge.gp.NumObservations(); got != 10 {
		t.Errorf("gp.NumObservations = %d, want 10", got)
	}
	if got := bridge.turnCount.Load(); got != 10 {
		t.Errorf("turnCount = %d, want 10", got)
	}
}

func TestBridge_StopIdempotent(t *testing.T) {
	desc := AgentDescriptor{
		AgentType:     "guide",
		ModelID:       "haiku-4.5-200k",
		ContextWindow: 200_000,
		Category:      CategoryStandalone,
	}
	agent := newTestBridgeAgent("guide-1", "guide", desc)
	_, bridge := setupBridgeTest(t, agent)

	if err := bridge.Stop(); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if err := bridge.Stop(); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

func TestBridgeConfigForAgent_Standalone(t *testing.T) {
	desc := AgentDescriptor{
		AgentType:     "guide",
		ModelID:       "haiku-4.5-200k",
		ContextWindow: 200_000,
		Category:      CategoryStandalone,
	}

	cfg := BridgeConfigForAgent(desc)

	if cfg.Descriptor != desc {
		t.Error("descriptor mismatch")
	}
	if cfg.EvictionConfig != nil {
		t.Error("standalone agent should not have eviction config")
	}
	if cfg.ManagerConfig == nil {
		t.Fatal("ManagerConfig is nil")
	}
	if cfg.QualityConfig == nil {
		t.Fatal("QualityConfig is nil")
	}
	if cfg.ContextConfig == nil {
		t.Fatal("ContextConfig is nil")
	}
}

func TestBridgeConfigForAgent_Knowledge(t *testing.T) {
	desc := AgentDescriptor{
		AgentType:     "librarian",
		ModelID:       "sonnet-4.5-1m",
		ContextWindow: 1_000_000,
		Category:      CategoryKnowledge,
	}

	cfg := BridgeConfigForAgent(desc)

	if cfg.EvictionConfig == nil {
		t.Fatal("knowledge agent should have eviction config")
	}
	if cfg.EvictionConfig.PreserveRecentTurns < 3 {
		t.Errorf("PreserveRecentTurns = %d, want >= 3", cfg.EvictionConfig.PreserveRecentTurns)
	}
}

func TestBridgeConfigForAgent_EvalInterval(t *testing.T) {
	// 200K context: 200000/10000 = 20 seconds.
	desc200k := AgentDescriptor{
		AgentType:     "guide",
		ModelID:       "haiku-4.5-200k",
		ContextWindow: 200_000,
		Category:      CategoryStandalone,
	}
	cfg200k := BridgeConfigForAgent(desc200k)

	// 1M context: 1000000/10000 = 100 seconds.
	desc1m := AgentDescriptor{
		AgentType:     "librarian",
		ModelID:       "sonnet-4.5-1m",
		ContextWindow: 1_000_000,
		Category:      CategoryKnowledge,
	}
	cfg1m := BridgeConfigForAgent(desc1m)

	// Larger context window should have longer evaluation interval.
	if cfg1m.ManagerConfig.EvaluationInterval <= cfg200k.ManagerConfig.EvaluationInterval {
		t.Errorf("1M eval interval (%v) should be greater than 200K (%v)",
			cfg1m.ManagerConfig.EvaluationInterval, cfg200k.ManagerConfig.EvaluationInterval)
	}
}
