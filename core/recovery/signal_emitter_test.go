package recovery

import (
	"testing"
)

func newTestEmitter() (*SignalEmitter, *ProgressCollector) {
	pc := NewProgressCollector()
	rd := NewRepetitionDetector(DefaultRepetitionConfig())
	emitter := NewSignalEmitter(pc, rd)
	return emitter, pc
}

func TestSignalEmitter_EmitToolCompleted(t *testing.T) {
	emitter, pc := newTestEmitter()

	emitter.EmitToolCompleted("agent-1", "session-1", "Read", "/foo/bar.go")

	if pc.SignalCount("agent-1") != 1 {
		t.Errorf("SignalCount = %d, want 1", pc.SignalCount("agent-1"))
	}

	signals := pc.RecentSignals("agent-1", 1)
	if len(signals) != 1 {
		t.Fatal("Expected 1 signal")
	}

	if signals[0].SignalType != SignalToolCompleted {
		t.Errorf("SignalType = %d, want SignalToolCompleted", signals[0].SignalType)
	}
	if signals[0].Operation != "Read:/foo/bar.go" {
		t.Errorf("Operation = %s, want Read:/foo/bar.go", signals[0].Operation)
	}
}

func TestSignalEmitter_EmitLLMResponse(t *testing.T) {
	emitter, pc := newTestEmitter()

	emitter.EmitLLMResponse("agent-1", "session-1")

	signals := pc.RecentSignals("agent-1", 1)
	if len(signals) != 1 {
		t.Fatal("Expected 1 signal")
	}

	if signals[0].SignalType != SignalLLMResponse {
		t.Errorf("SignalType = %d, want SignalLLMResponse", signals[0].SignalType)
	}
	if signals[0].Operation != "llm:complete" {
		t.Errorf("Operation = %s, want llm:complete", signals[0].Operation)
	}
}

func TestSignalEmitter_EmitFileModified(t *testing.T) {
	emitter, pc := newTestEmitter()

	emitter.EmitFileModified("agent-1", "session-1", "/src/main.go")

	signals := pc.RecentSignals("agent-1", 1)
	if len(signals) != 1 {
		t.Fatal("Expected 1 signal")
	}

	if signals[0].SignalType != SignalFileModified {
		t.Errorf("SignalType = %d, want SignalFileModified", signals[0].SignalType)
	}
	if signals[0].Operation != "file:/src/main.go" {
		t.Errorf("Operation = %s, want file:/src/main.go", signals[0].Operation)
	}
}

func TestSignalEmitter_EmitStateTransition(t *testing.T) {
	emitter, pc := newTestEmitter()

	emitter.EmitStateTransition("agent-1", "session-1", "idle", "running")

	signals := pc.RecentSignals("agent-1", 1)
	if len(signals) != 1 {
		t.Fatal("Expected 1 signal")
	}

	if signals[0].SignalType != SignalStateTransition {
		t.Errorf("SignalType = %d, want SignalStateTransition", signals[0].SignalType)
	}
	if signals[0].Operation != "idle->running" {
		t.Errorf("Operation = %s, want idle->running", signals[0].Operation)
	}
	if signals[0].Details["from"] != "idle" {
		t.Error("Details should contain from state")
	}
	if signals[0].Details["to"] != "running" {
		t.Error("Details should contain to state")
	}
}

func TestSignalEmitter_EmitAgentRequest(t *testing.T) {
	emitter, pc := newTestEmitter()

	emitter.EmitAgentRequest("agent-1", "session-1", "agent-2")

	signals := pc.RecentSignals("agent-1", 1)
	if len(signals) != 1 {
		t.Fatal("Expected 1 signal")
	}

	if signals[0].SignalType != SignalAgentRequest {
		t.Errorf("SignalType = %d, want SignalAgentRequest", signals[0].SignalType)
	}
	if signals[0].Operation != "agent:agent-2" {
		t.Errorf("Operation = %s, want agent:agent-2", signals[0].Operation)
	}
}

func TestSignalEmitter_MultipleSignals(t *testing.T) {
	emitter, pc := newTestEmitter()

	emitter.EmitToolCompleted("agent-1", "session-1", "Read", "/file1.go")
	emitter.EmitToolCompleted("agent-1", "session-1", "Write", "/file2.go")
	emitter.EmitLLMResponse("agent-1", "session-1")

	if pc.SignalCount("agent-1") != 3 {
		t.Errorf("SignalCount = %d, want 3", pc.SignalCount("agent-1"))
	}
}

func TestHashOperation(t *testing.T) {
	h1 := HashOperation("Read", "/foo/bar.go")
	h2 := HashOperation("Read", "/foo/bar.go")
	h3 := HashOperation("Write", "/foo/bar.go")
	h4 := HashOperation("Read", "/baz/qux.go")

	if h1 != h2 {
		t.Error("Same operation should produce same hash")
	}
	if h1 == h3 {
		t.Error("Different action should produce different hash")
	}
	if h1 == h4 {
		t.Error("Different target should produce different hash")
	}
}
