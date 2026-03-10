package shared

import "testing"

func TestThoughtEmitter_DefersIncompletePrefixUntilBoundary(t *testing.T) {
	emitter := NewThoughtEmitter(true)

	if got := emitter.AddDelta("Let me"); got != "" {
		t.Fatalf("incomplete prefix should stay buffered, got %q", got)
	}
	if got := emitter.AddDelta(" think through this"); got != "" {
		t.Fatalf("incomplete sentence should stay buffered, got %q", got)
	}
	if got := emitter.AddDelta("."); got != "Let me think through this." {
		t.Fatalf("sentence boundary should emit full thought, got %q", got)
	}
}

func TestThoughtEmitter_FlushReturnsIncompleteTail(t *testing.T) {
	emitter := NewThoughtEmitter(true)

	if got := emitter.AddDelta("Sequencing tasks"); got != "" {
		t.Fatalf("incomplete prefix should stay buffered, got %q", got)
	}
	if got := emitter.Flush(); got != "Sequencing tasks" {
		t.Fatalf("Flush() = %q, want %q", got, "Sequencing tasks")
	}
}
