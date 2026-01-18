package recovery

import (
	"sync"
	"testing"
	"time"
)

type mockSubscriber struct {
	mu      sync.Mutex
	signals []ProgressSignal
}

func (m *mockSubscriber) OnSignal(sig ProgressSignal) {
	m.mu.Lock()
	m.signals = append(m.signals, sig)
	m.mu.Unlock()
}

func (m *mockSubscriber) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.signals)
}

func TestProgressCollector_Signal(t *testing.T) {
	pc := NewProgressCollector()

	sig := ProgressSignal{
		AgentID:    "agent-1",
		SessionID:  "session-1",
		SignalType: SignalToolCompleted,
		Timestamp:  time.Now(),
		Operation:  "Read:/foo/bar.go",
		Hash:       12345,
	}

	pc.Signal(sig)

	if got := pc.SignalCount("agent-1"); got != 1 {
		t.Errorf("SignalCount = %d, want 1", got)
	}
}

func TestProgressCollector_LastSignalTime(t *testing.T) {
	pc := NewProgressCollector()

	_, ok := pc.LastSignalTime("nonexistent")
	if ok {
		t.Error("LastSignalTime for nonexistent agent should return false")
	}

	now := time.Now()
	pc.Signal(ProgressSignal{
		AgentID:   "agent-1",
		Timestamp: now,
	})

	got, ok := pc.LastSignalTime("agent-1")
	if !ok {
		t.Fatal("LastSignalTime should return true after signal")
	}
	if !got.Equal(now) {
		t.Errorf("LastSignalTime = %v, want %v", got, now)
	}
}

func TestProgressCollector_RecentSignals(t *testing.T) {
	pc := NewProgressCollector()

	for i := 0; i < 5; i++ {
		pc.Signal(ProgressSignal{
			AgentID:   "agent-1",
			Operation: string(rune('A' + i)),
		})
	}

	got := pc.RecentSignals("agent-1", 3)
	if len(got) != 3 {
		t.Fatalf("RecentSignals(3) len = %d, want 3", len(got))
	}

	want := []string{"C", "D", "E"}
	for i, w := range want {
		if got[i].Operation != w {
			t.Errorf("RecentSignals[%d].Operation = %s, want %s", i, got[i].Operation, w)
		}
	}
}

func TestProgressCollector_RecentSignalsNonexistent(t *testing.T) {
	pc := NewProgressCollector()
	got := pc.RecentSignals("nonexistent", 5)
	if got != nil {
		t.Errorf("RecentSignals for nonexistent = %v, want nil", got)
	}
}

func TestProgressCollector_Subscribe(t *testing.T) {
	pc := NewProgressCollector()
	sub := &mockSubscriber{}

	pc.Subscribe(sub)

	for i := 0; i < 3; i++ {
		pc.Signal(ProgressSignal{AgentID: "agent-1"})
	}

	if got := sub.count(); got != 3 {
		t.Errorf("subscriber received %d signals, want 3", got)
	}
}

func TestProgressCollector_MultipleSubscribers(t *testing.T) {
	pc := NewProgressCollector()
	sub1 := &mockSubscriber{}
	sub2 := &mockSubscriber{}

	pc.Subscribe(sub1)
	pc.Subscribe(sub2)

	pc.Signal(ProgressSignal{AgentID: "agent-1"})

	if sub1.count() != 1 {
		t.Errorf("sub1 received %d signals, want 1", sub1.count())
	}
	if sub2.count() != 1 {
		t.Errorf("sub2 received %d signals, want 1", sub2.count())
	}
}

func TestProgressCollector_RemoveAgent(t *testing.T) {
	pc := NewProgressCollector()

	pc.Signal(ProgressSignal{AgentID: "agent-1"})
	pc.RemoveAgent("agent-1")

	if got := pc.SignalCount("agent-1"); got != 0 {
		t.Errorf("SignalCount after remove = %d, want 0", got)
	}
	if _, ok := pc.LastSignalTime("agent-1"); ok {
		t.Error("LastSignalTime after remove should return false")
	}
}

func TestProgressCollector_ConcurrentSignals(t *testing.T) {
	pc := NewProgressCollector()
	sub := &mockSubscriber{}
	pc.Subscribe(sub)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(agentNum int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				pc.Signal(ProgressSignal{
					AgentID:   "agent-1",
					Timestamp: time.Now(),
				})
			}
		}(i)
	}
	wg.Wait()

	if got := pc.SignalCount("agent-1"); got != 1000 {
		t.Errorf("SignalCount = %d, want 1000", got)
	}
	if got := sub.count(); got != 1000 {
		t.Errorf("subscriber count = %d, want 1000", got)
	}
}

func TestProgressCollector_MultipleAgents(t *testing.T) {
	pc := NewProgressCollector()

	for i := 0; i < 3; i++ {
		for j := 0; j < 5; j++ {
			pc.Signal(ProgressSignal{
				AgentID: string(rune('A' + i)),
			})
		}
	}

	for i := 0; i < 3; i++ {
		agentID := string(rune('A' + i))
		if got := pc.SignalCount(agentID); got != 5 {
			t.Errorf("SignalCount(%s) = %d, want 5", agentID, got)
		}
	}
}

func TestProgressSignalType_Values(t *testing.T) {
	types := []ProgressSignalType{
		SignalToolCompleted,
		SignalLLMResponse,
		SignalFileModified,
		SignalStateTransition,
		SignalAgentRequest,
	}

	for i, typ := range types {
		if int(typ) != i {
			t.Errorf("SignalType %d has value %d, want %d", i, typ, i)
		}
	}
}

func TestProgressCollector_GetOrCreateBuffer_Concurrent(t *testing.T) {
	pc := NewProgressCollector()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pc.Signal(ProgressSignal{
				AgentID:   "agent-1",
				SessionID: "session-1",
			})
		}()
	}
	wg.Wait()

	if got := pc.SignalCount("agent-1"); got != 100 {
		t.Errorf("SignalCount = %d, want 100", got)
	}
}
