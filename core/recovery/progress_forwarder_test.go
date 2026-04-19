package recovery

import (
	"sync"
	"testing"
	"time"
)

// recordingProgressSink captures every signal it sees for assertion.
type recordingProgressSink struct {
	mu      sync.Mutex
	signals []ProgressSignal
}

func (r *recordingProgressSink) PublishProgressSignal(sig ProgressSignal) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.signals = append(r.signals, sig)
}

func (r *recordingProgressSink) snapshot() []ProgressSignal {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ProgressSignal, len(r.signals))
	copy(out, r.signals)
	return out
}

// TestProgressBusForwarder_DeliversSignalsToSink verifies the REC-01
// forwarder pipes every collected signal to its sink by default. This
// is the smallest possible end-to-end check: Signal() → OnSignal() →
// sink.PublishProgressSignal(). A regression here breaks the
// "progress visible outside core/recovery" contract entirely.
func TestProgressBusForwarder_DeliversSignalsToSink(t *testing.T) {
	pc := NewProgressCollector()
	sink := &recordingProgressSink{}
	forwarder := NewProgressBusForwarder(sink)
	pc.Subscribe(forwarder)

	pc.Signal(ProgressSignal{
		AgentID:    "engineer-1",
		SessionID:  "sess-1",
		SignalType: SignalToolCompleted,
		Operation:  "read:file.go",
		Timestamp:  time.Now(),
	})
	pc.Signal(ProgressSignal{
		AgentID:    "engineer-1",
		SessionID:  "sess-1",
		SignalType: SignalLLMResponse,
		Operation:  "llm:complete",
		Timestamp:  time.Now(),
	})

	got := sink.snapshot()
	if len(got) != 2 {
		t.Fatalf("sink saw %d signals, want 2", len(got))
	}
	if got[0].Operation != "read:file.go" {
		t.Errorf("signal[0].Operation = %q, want read:file.go", got[0].Operation)
	}
}

// TestProgressBusForwarder_NilSinkIsNoop protects the "forwarder
// installed, sink not yet attached" configuration. A nil-sink forwarder
// must never panic; signals simply drop silently while the bus is
// bootstrapping. Callers use SetSink later to enable delivery.
func TestProgressBusForwarder_NilSinkIsNoop(t *testing.T) {
	pc := NewProgressCollector()
	forwarder := NewProgressBusForwarder(nil)
	pc.Subscribe(forwarder)

	// Must not panic.
	pc.Signal(ProgressSignal{AgentID: "a", SessionID: "s", SignalType: SignalLLMResponse})
}

// TestProgressBusForwarder_SetSinkEnablesDelivery verifies sink swap
// semantics: after SetSink fires, subsequent signals reach the new
// sink; signals that arrived before the swap were dropped as expected
// (nil-sink no-op path).
func TestProgressBusForwarder_SetSinkEnablesDelivery(t *testing.T) {
	pc := NewProgressCollector()
	forwarder := NewProgressBusForwarder(nil)
	pc.Subscribe(forwarder)

	pc.Signal(ProgressSignal{AgentID: "engineer-1", Operation: "before-sink"})

	sink := &recordingProgressSink{}
	forwarder.SetSink(sink)

	pc.Signal(ProgressSignal{AgentID: "engineer-1", Operation: "after-sink"})

	got := sink.snapshot()
	if len(got) != 1 {
		t.Fatalf("sink saw %d signals, want 1 (pre-attach signal must drop)", len(got))
	}
	if got[0].Operation != "after-sink" {
		t.Errorf("signal captured = %q, want after-sink", got[0].Operation)
	}
}

// TestProgressBusForwarder_FilterAgentAllowlist verifies the per-agent
// allowlist: signals from agents outside the set are suppressed. This
// is what lets the TUI subscribe once but narrow delivery per panel.
func TestProgressBusForwarder_FilterAgentAllowlist(t *testing.T) {
	pc := NewProgressCollector()
	sink := &recordingProgressSink{}
	forwarder := NewProgressBusForwarder(sink)
	forwarder.SetFilter(ProgressForwarderFilter{
		AgentIDs: map[string]struct{}{"engineer-1": {}},
	})
	pc.Subscribe(forwarder)

	pc.Signal(ProgressSignal{AgentID: "engineer-1", Operation: "allowed"})
	pc.Signal(ProgressSignal{AgentID: "academic-1", Operation: "blocked"})
	pc.Signal(ProgressSignal{AgentID: "engineer-1", Operation: "also-allowed"})

	got := sink.snapshot()
	if len(got) != 2 {
		t.Fatalf("sink saw %d signals, want 2 (two engineer-1 signals)", len(got))
	}
	for _, s := range got {
		if s.AgentID != "engineer-1" {
			t.Errorf("leaked signal from %q past allowlist", s.AgentID)
		}
	}
}

// TestProgressBusForwarder_FilterSignalTypes checks the type filter
// path separately — needed because a consumer watching only
// state-transitions should not see tool-completion chatter.
func TestProgressBusForwarder_FilterSignalTypes(t *testing.T) {
	pc := NewProgressCollector()
	sink := &recordingProgressSink{}
	forwarder := NewProgressBusForwarder(sink)
	forwarder.SetFilter(ProgressForwarderFilter{
		SignalTypes: map[ProgressSignalType]struct{}{SignalLLMResponse: {}},
	})
	pc.Subscribe(forwarder)

	pc.Signal(ProgressSignal{SignalType: SignalToolCompleted, Operation: "tool"})
	pc.Signal(ProgressSignal{SignalType: SignalLLMResponse, Operation: "llm-1"})
	pc.Signal(ProgressSignal{SignalType: SignalLLMResponse, Operation: "llm-2"})

	got := sink.snapshot()
	if len(got) != 2 {
		t.Fatalf("sink saw %d signals, want 2 (two LLMResponse)", len(got))
	}
	for _, s := range got {
		if s.SignalType != SignalLLMResponse {
			t.Errorf("leaked non-LLMResponse signal: %v", s.SignalType)
		}
	}
}
