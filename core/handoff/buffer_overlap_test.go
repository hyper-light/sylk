package handoff

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// mockHandoffableAgent implements HandoffableAgent and HandoffInjectable
// for testing the overlap coordinator.
type mockHandoffableAgent struct {
	id        string
	agentType string
	desc      AgentDescriptor
	injected  *PreparedContext
}

func (m *mockHandoffableAgent) AgentID() string                           { return m.id }
func (m *mockHandoffableAgent) AgentType() string                        { return m.agentType }
func (m *mockHandoffableAgent) Descriptor() AgentDescriptor              { return m.desc }
func (m *mockHandoffableAgent) ExtractArchivableState() *ArchivableState { return nil }
func (m *mockHandoffableAgent) Terminate(_ context.Context) error        { return nil }
func (m *mockHandoffableAgent) InjectPreparedContext(pc *PreparedContext) error {
	m.injected = pc
	return nil
}

func newTestOverlapSetup(t *testing.T) (*OverlapCoordinator, *FactorySessionAdapter, *COWSnapshot) {
	t.Helper()

	desc := AgentDescriptor{ContextWindow: 200_000, AgentType: "test-agent", ModelID: "test", Category: CategoryStandalone}
	oc := NewOverlapCoordinator(desc)

	reg := NewDescriptorRegistry()
	reg.Register(desc)
	factory := NewAgentFactory(reg)

	var seq atomic.Uint64
	factory.RegisterCreator("test-agent", func(_ context.Context) (HandoffableAgent, error) {
		id := seq.Add(1)
		return &mockHandoffableAgent{
			id:        "new-agent-" + string(rune('0'+id)),
			agentType: "test-agent",
			desc:      desc,
		}, nil
	})

	adapter := NewFactorySessionAdapter(factory)

	snapshot := &COWSnapshot{
		Segments: []*BufferSegment{
			{
				Messages:   []Message{{Role: "turn", Content: "hello", TokenCount: 100}},
				TokenCount: 100,
				SealedAt:   time.Now(),
			},
		},
		TotalTokens: 100,
		TokenBudget: 20_000,
		CreatedAt:   time.Now(),
		SequenceNum: 1,
	}

	return oc, adapter, snapshot
}

func TestOverlapCoordinator_BeginAndComplete(t *testing.T) {
	oc, adapter, snapshot := newTestOverlapSetup(t)

	handle, err := oc.BeginOverlap("old-agent", snapshot, adapter)
	if err != nil {
		t.Fatalf("BeginOverlap: %v", err)
	}
	if handle == nil {
		t.Fatal("handle should not be nil")
	}
	if handle.NewAgent == nil {
		t.Fatal("NewAgent should not be nil")
	}

	// Complete the overlap.
	if err := handle.Complete(); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// After completion, phase should be idle.
	if p := oc.Phase(); p != OverlapIdle {
		t.Fatalf("expected Idle after Complete, got %s", p)
	}
}

func TestOverlapCoordinator_BeginAndAbort(t *testing.T) {
	oc, adapter, snapshot := newTestOverlapSetup(t)

	handle, err := oc.BeginOverlap("old-agent", snapshot, adapter)
	if err != nil {
		t.Fatalf("BeginOverlap: %v", err)
	}

	handle.Abort("test abort")

	// After abort, phase should be idle (reset).
	if p := oc.Phase(); p != OverlapIdle {
		t.Fatalf("expected Idle after Abort, got %s", p)
	}
}

func TestOverlapCoordinator_DoubleBeginRejected(t *testing.T) {
	oc, adapter, snapshot := newTestOverlapSetup(t)

	handle, err := oc.BeginOverlap("old-agent-1", snapshot, adapter)
	if err != nil {
		t.Fatalf("first BeginOverlap: %v", err)
	}

	// Second begin should fail — overlap already in progress.
	_, err = oc.BeginOverlap("old-agent-2", snapshot, adapter)
	if err == nil {
		t.Fatal("second BeginOverlap should return error")
	}

	// Clean up.
	handle.Abort("cleanup")
}

func TestOverlapCoordinator_WaitDrainCompletes(t *testing.T) {
	oc, adapter, snapshot := newTestOverlapSetup(t)

	handle, err := oc.BeginOverlap("old-agent", snapshot, adapter)
	if err != nil {
		t.Fatalf("BeginOverlap: %v", err)
	}

	// Complete in a goroutine after a short delay.
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = handle.Complete()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := handle.WaitDrain(ctx); err != nil {
		t.Fatalf("WaitDrain: %v", err)
	}
}

func TestOverlapCoordinator_WaitDrainAbortSignal(t *testing.T) {
	oc, adapter, snapshot := newTestOverlapSetup(t)

	handle, err := oc.BeginOverlap("old-agent", snapshot, adapter)
	if err != nil {
		t.Fatalf("BeginOverlap: %v", err)
	}

	// Abort in a goroutine after a short delay.
	go func() {
		time.Sleep(50 * time.Millisecond)
		handle.Abort("test abort during drain")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = handle.WaitDrain(ctx)
	if err == nil {
		t.Fatal("WaitDrain should return error on abort")
	}
}

func TestOverlapCoordinator_WALWriterCalled(t *testing.T) {
	oc, adapter, snapshot := newTestOverlapSetup(t)

	var walEntries []OverlapWALEntry
	oc.SetWALWriter(func(entry OverlapWALEntry) {
		walEntries = append(walEntries, entry)
	})

	handle, err := oc.BeginOverlap("old-agent", snapshot, adapter)
	if err != nil {
		t.Fatalf("BeginOverlap: %v", err)
	}

	if err := handle.Complete(); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// Should have WAL entries for: Starting, Running, Draining (from WaitDrain path or direct Complete), Complete.
	if len(walEntries) < 3 {
		t.Errorf("expected at least 3 WAL entries, got %d", len(walEntries))
	}

	// First entry should be OverlapStarting.
	if walEntries[0].Phase != OverlapStarting {
		t.Errorf("first WAL entry: got %s, want starting", walEntries[0].Phase)
	}
}

func TestOverlapCoordinator_RecoverOrphanedOverlap(t *testing.T) {
	desc := AgentDescriptor{ContextWindow: 200_000, AgentType: "test", ModelID: "test"}
	oc := NewOverlapCoordinator(desc)

	var walEntries []OverlapWALEntry
	oc.SetWALWriter(func(entry OverlapWALEntry) {
		walEntries = append(walEntries, entry)
	})

	// Simulate an orphaned overlap (begin without complete/abort).
	orphanEntry := OverlapWALEntry{
		Phase:       OverlapRunning,
		OldAgentID:  "crashed-agent",
		NewAgentID:  "new-crashed-agent",
		SnapshotSeq: 42,
		Timestamp:   time.Now(),
	}

	if err := oc.RecoverFromWAL(orphanEntry); err != nil {
		t.Fatalf("RecoverFromWAL: %v", err)
	}

	// Phase should be idle after recovery.
	if p := oc.Phase(); p != OverlapIdle {
		t.Fatalf("expected Idle after recovery, got %s", p)
	}

	// WAL should have an abort entry.
	if len(walEntries) == 0 {
		t.Fatal("expected WAL abort entry for orphaned overlap")
	}
	lastEntry := walEntries[len(walEntries)-1]
	if lastEntry.Phase != OverlapAborted {
		t.Errorf("expected abort WAL entry, got %s", lastEntry.Phase)
	}
}

func TestOverlapCoordinator_ContextInjected(t *testing.T) {
	oc, adapter, snapshot := newTestOverlapSetup(t)

	handle, err := oc.BeginOverlap("old-agent", snapshot, adapter)
	if err != nil {
		t.Fatalf("BeginOverlap: %v", err)
	}

	// The mock agent should have received injected context.
	mock, ok := handle.NewAgent.(*mockHandoffableAgent)
	if !ok {
		t.Fatal("expected mockHandoffableAgent")
	}
	if mock.injected == nil {
		t.Fatal("context should have been injected")
	}

	handle.Abort("cleanup")
}

func TestOverlapCoordinator_TimeoutDerivation(t *testing.T) {
	desc200K := AgentDescriptor{ContextWindow: 200_000}
	oc200K := NewOverlapCoordinator(desc200K)

	if oc200K.creationTimeout != 4*time.Second {
		t.Errorf("200K creationTimeout: got %v, want 4s", oc200K.creationTimeout)
	}

	desc1M := AgentDescriptor{ContextWindow: 1_000_000}
	oc1M := NewOverlapCoordinator(desc1M)

	if oc1M.creationTimeout != 20*time.Second {
		t.Errorf("1M creationTimeout: got %v, want 20s", oc1M.creationTimeout)
	}
}
