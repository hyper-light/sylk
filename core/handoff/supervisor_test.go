package handoff

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// testSupervisorAgent implements HandoffableAgent for supervisor tests.
type testSupervisorAgent struct {
	id        string
	agentType string
	desc      AgentDescriptor
	stopped   bool
}

func (a *testSupervisorAgent) AgentID() string                          { return a.id }
func (a *testSupervisorAgent) AgentType() string                        { return a.agentType }
func (a *testSupervisorAgent) Descriptor() AgentDescriptor              { return a.desc }
func (a *testSupervisorAgent) ExtractArchivableState() *ArchivableState { return nil }
func (a *testSupervisorAgent) Terminate(_ context.Context) error        { a.stopped = true; return nil }

func newTestSupervisorAgent(id, agentType string) *testSupervisorAgent {
	desc, ok := NewDescriptorRegistry().Get(agentType)
	if !ok {
		desc = AgentDescriptor{
			AgentType:     agentType,
			ModelID:       "test-model",
			ContextWindow: 200_000,
			Category:      CategoryStandalone,
		}
	}
	return &testSupervisorAgent{id: id, agentType: agentType, desc: desc}
}

func newTestSupervisor(t *testing.T) *HandoffSupervisor {
	t.Helper()
	sup := NewHandoffSupervisor(&SupervisorConfig{
		EnableWAL:      false,
		EnableLearning: true,
	})
	if err := sup.Start(); err != nil {
		t.Fatalf("supervisor.Start: %v", err)
	}
	t.Cleanup(func() { _ = sup.Stop() })
	return sup
}

func TestSupervisor_StartStop(t *testing.T) {
	sup := NewHandoffSupervisor(nil)
	if err := sup.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !sup.started.Load() {
		t.Error("supervisor should be started")
	}
	if err := sup.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if sup.started.Load() {
		t.Error("supervisor should not be started after Stop")
	}
}

func TestSupervisor_StartIdempotent(t *testing.T) {
	sup := NewHandoffSupervisor(nil)
	if err := sup.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := sup.Start(); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	_ = sup.Stop()
}

func TestSupervisor_StopIdempotent(t *testing.T) {
	sup := NewHandoffSupervisor(nil)
	_ = sup.Start()
	if err := sup.Stop(); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if err := sup.Stop(); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

func TestSupervisor_RegisterAgent(t *testing.T) {
	sup := newTestSupervisor(t)
	agent := newTestSupervisorAgent("guide-1", "guide")

	bridge, err := sup.RegisterAgent(agent)
	if err != nil {
		t.Fatalf("RegisterAgent: %v", err)
	}
	if bridge == nil {
		t.Fatal("bridge is nil")
	}
	if sup.BridgeCount() != 1 {
		t.Errorf("BridgeCount = %d, want 1", sup.BridgeCount())
	}
}

func TestSupervisor_RegisterAgent_Idempotent(t *testing.T) {
	sup := newTestSupervisor(t)
	agent := newTestSupervisorAgent("guide-1", "guide")

	first, err := sup.RegisterAgent(agent)
	if err != nil {
		t.Fatalf("first RegisterAgent: %v", err)
	}
	second, err := sup.RegisterAgent(agent)
	if err != nil {
		t.Fatalf("second RegisterAgent: %v", err)
	}
	if first != second {
		t.Fatal("expected repeated registration to return the existing bridge")
	}
	if sup.BridgeCount() != 1 {
		t.Fatalf("BridgeCount = %d, want 1", sup.BridgeCount())
	}
}

func TestSupervisor_RegisterAgentNotStarted(t *testing.T) {
	sup := NewHandoffSupervisor(nil)
	agent := newTestSupervisorAgent("guide-1", "guide")

	_, err := sup.RegisterAgent(agent)
	if err == nil {
		t.Fatal("expected error registering agent before Start")
	}
}

func TestSupervisor_UnregisterAgent(t *testing.T) {
	sup := newTestSupervisor(t)
	agent := newTestSupervisorAgent("guide-1", "guide")

	_, err := sup.RegisterAgent(agent)
	if err != nil {
		t.Fatalf("RegisterAgent: %v", err)
	}

	if err := sup.UnregisterAgent("guide-1"); err != nil {
		t.Fatalf("UnregisterAgent: %v", err)
	}
	if sup.BridgeCount() != 0 {
		t.Errorf("BridgeCount = %d, want 0", sup.BridgeCount())
	}
}

func TestSupervisor_UnregisterMissing(t *testing.T) {
	sup := newTestSupervisor(t)

	// Should be a no-op.
	if err := sup.UnregisterAgent("nonexistent"); err != nil {
		t.Fatalf("UnregisterAgent(nonexistent): %v", err)
	}
}

func TestSupervisor_GetBridge(t *testing.T) {
	sup := newTestSupervisor(t)
	agent := newTestSupervisorAgent("arch-1", "architect")

	expected, err := sup.RegisterAgent(agent)
	if err != nil {
		t.Fatalf("RegisterAgent: %v", err)
	}

	got := sup.GetBridge("arch-1")
	if got != expected {
		t.Error("GetBridge returned wrong bridge")
	}

	if sup.GetBridge("nonexistent") != nil {
		t.Error("GetBridge(nonexistent) should return nil")
	}
}

func TestSupervisor_MultipleBridges(t *testing.T) {
	sup := newTestSupervisor(t)

	agents := []struct {
		id        string
		agentType string
	}{
		{"guide-1", "guide"},
		{"arch-1", "architect"},
		{"lib-1", "librarian"},
	}

	for _, a := range agents {
		agent := newTestSupervisorAgent(a.id, a.agentType)
		if _, err := sup.RegisterAgent(agent); err != nil {
			t.Fatalf("RegisterAgent(%s): %v", a.id, err)
		}
	}

	if sup.BridgeCount() != 3 {
		t.Errorf("BridgeCount = %d, want 3", sup.BridgeCount())
	}

	// Each bridge should be independently accessible.
	for _, a := range agents {
		bridge := sup.GetBridge(a.id)
		if bridge == nil {
			t.Errorf("GetBridge(%s) = nil", a.id)
		}
	}
}

func TestSupervisor_Factory(t *testing.T) {
	sup := newTestSupervisor(t)

	factory := sup.Factory()
	if factory == nil {
		t.Fatal("Factory returned nil")
	}
}

func TestSupervisor_Descriptors(t *testing.T) {
	sup := newTestSupervisor(t)

	descriptors := sup.Descriptors()
	if descriptors == nil {
		t.Fatal("Descriptors returned nil")
	}

	// Should have pre-populated entries.
	if descriptors.Len() < 12 {
		t.Errorf("Descriptors.Len() = %d, want >= 12", descriptors.Len())
	}
}

func TestSupervisor_SetAgentReplacedCallback(t *testing.T) {
	sup := newTestSupervisor(t)

	var calledOldID, calledNewID string
	sup.SetAgentReplacedCallback(func(oldID, newID string, _ HandoffableAgent) error {
		calledOldID = oldID
		calledNewID = newID
		return nil
	})

	// Verify callback was stored by checking the bridge can access it.
	agent := newTestSupervisorAgent("guide-1", "guide")
	bridge, _ := sup.RegisterAgent(agent)

	if bridge.supervisor.onAgentReplaced == nil {
		t.Fatal("onAgentReplaced callback not set")
	}

	// Invoke directly to verify it was stored correctly.
	agent2 := newTestSupervisorAgent("guide-2", "guide")
	_ = bridge.supervisor.onAgentReplaced("guide-1", "guide-2", agent2)
	if calledOldID != "guide-1" || calledNewID != "guide-2" {
		t.Errorf("callback args = (%q, %q), want (%q, %q)",
			calledOldID, calledNewID, "guide-1", "guide-2")
	}
}

func TestSupervisor_StopStopsAllBridges(t *testing.T) {
	sup := NewHandoffSupervisor(&SupervisorConfig{
		EnableWAL:      false,
		EnableLearning: true,
	})
	_ = sup.Start()

	agent1 := newTestSupervisorAgent("guide-1", "guide")
	agent2 := newTestSupervisorAgent("arch-1", "architect")

	bridge1, _ := sup.RegisterAgent(agent1)
	bridge2, _ := sup.RegisterAgent(agent2)

	if err := sup.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if bridge1.started.Load() {
		t.Error("bridge1 should be stopped")
	}
	if bridge2.started.Load() {
		t.Error("bridge2 should be stopped")
	}
}

func TestSupervisor_CustomDescriptor(t *testing.T) {
	sup := newTestSupervisor(t)

	// Agent with a type not in the registry.
	agent := newTestSupervisorAgent("custom-1", "custom-agent-type")

	bridge, err := sup.RegisterAgent(agent)
	if err != nil {
		t.Fatalf("RegisterAgent(custom): %v", err)
	}
	if bridge == nil {
		t.Fatal("bridge is nil for custom agent")
	}

	// Descriptor should have been registered.
	_, ok := sup.Descriptors().Get("custom-agent-type")
	if !ok {
		t.Error("custom agent type should be registered in descriptors")
	}
}

func TestSupervisor_SetBriefSource_PropagatesToExistingAndFutureBridges(t *testing.T) {
	sup := newTestSupervisor(t)
	source := &stubBriefSource{brief: &ContextBrief{TaskSummary: "brief"}}

	firstAgent := newTestSupervisorAgent("guide-1", "guide")
	firstBridge, err := sup.RegisterAgent(firstAgent)
	if err != nil {
		t.Fatalf("RegisterAgent: %v", err)
	}
	if firstBridge.briefGen != nil {
		t.Fatal("expected no brief generator before SetBriefSource")
	}

	sup.SetBriefSource(source)

	if firstBridge.briefGen == nil {
		t.Fatal("expected existing bridge to receive brief generator")
	}

	secondAgent := newTestSupervisorAgent("arch-1", "architect")
	secondBridge, err := sup.RegisterAgent(secondAgent)
	if err != nil {
		t.Fatalf("RegisterAgent(second): %v", err)
	}
	if secondBridge.briefGen == nil {
		t.Fatal("expected future bridge to receive brief generator")
	}
}

func TestSupervisor_WALRecovery(t *testing.T) {
	dir := t.TempDir()
	walDir := filepath.Join(dir, "handoff")

	// Create supervisor with WAL, start it, add some data, stop it.
	sup1 := NewHandoffSupervisor(&SupervisorConfig{
		WALDir:         walDir,
		EnableWAL:      true,
		EnableLearning: true,
	})
	if err := sup1.Start(); err != nil {
		t.Fatalf("sup1.Start: %v", err)
	}

	// Verify WAL directory was created.
	if _, err := os.Stat(walDir); os.IsNotExist(err) {
		t.Fatal("WAL directory was not created")
	}

	if err := sup1.Stop(); err != nil {
		t.Fatalf("sup1.Stop: %v", err)
	}

	// Create second supervisor and recover from WAL.
	sup2 := NewHandoffSupervisor(&SupervisorConfig{
		WALDir:         walDir,
		EnableWAL:      true,
		EnableLearning: true,
	})
	if err := sup2.Start(); err != nil {
		t.Fatalf("sup2.Start (recovery): %v", err)
	}
	if err := sup2.Stop(); err != nil {
		t.Fatalf("sup2.Stop: %v", err)
	}
}

func TestSupervisor_DefaultConfig(t *testing.T) {
	cfg := DefaultSupervisorConfig()
	if cfg == nil {
		t.Fatal("DefaultSupervisorConfig returned nil")
	}
	if !cfg.EnableWAL {
		t.Error("EnableWAL should default to true")
	}
	if !cfg.EnableLearning {
		t.Error("EnableLearning should default to true")
	}
}
