package steering

import (
	"os"
	"testing"

	"github.com/adalundhe/sylk/core/agentlog"
)

func newTestJournal(t *testing.T) *SteeringJournal {
	t.Helper()
	dir := t.TempDir()
	j, err := OpenSteeringJournal(agentlog.JournalConfig{
		SessionDir: dir,
		AgentName:  "test-agent",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	return j
}

func TestWALBracketing(t *testing.T) {
	j := newTestJournal(t)

	j.LogBegin("corr-1", "architect", "session-1")
	j.LogCheckpoint(Checkpoint{ID: "cp_000000", Turn: 0, MessageCount: 3, Phase: "analyzing"})
	j.LogCommand(Command{Type: CommandSteer, Text: "use RS256"})
	j.LogComplete("corr-1", 5, 3)

	incomplete := j.FindIncompleteOperations()
	if len(incomplete) != 0 {
		t.Fatalf("expected 0 incomplete operations after complete, got %d", len(incomplete))
	}
}

func TestWALIncomplete(t *testing.T) {
	j := newTestJournal(t)

	j.LogBegin("corr-1", "architect", "session-1")
	j.LogCheckpoint(Checkpoint{ID: "cp_000000", Turn: 0, MessageCount: 3, Phase: "analyzing"})
	// No Complete or Interrupted — simulates crash.

	incomplete := j.FindIncompleteOperations()
	if len(incomplete) != 1 {
		t.Fatalf("expected 1 incomplete operation, got %d", len(incomplete))
	}
	if incomplete[0].CorrelationID != "corr-1" {
		t.Errorf("correlation ID = %q, want %q", incomplete[0].CorrelationID, "corr-1")
	}
	if incomplete[0].AgentID != "architect" {
		t.Errorf("agent ID = %q, want %q", incomplete[0].AgentID, "architect")
	}
}

func TestWALInterruptedCloseBracket(t *testing.T) {
	j := newTestJournal(t)

	j.LogBegin("corr-2", "engineer", "session-2")
	j.LogInterrupted("corr-2", "cp_000003", 3, "generating")

	incomplete := j.FindIncompleteOperations()
	if len(incomplete) != 0 {
		t.Fatalf("expected 0 incomplete after interrupted, got %d", len(incomplete))
	}
}

func TestWALMultipleOperations(t *testing.T) {
	j := newTestJournal(t)

	// op-1: complete
	j.LogBegin("op-1", "architect", "s1")
	j.LogComplete("op-1", 2, 2)

	// op-2: incomplete (crash)
	j.LogBegin("op-2", "architect", "s1")
	j.LogCheckpoint(Checkpoint{ID: "cp_000000", Turn: 0, MessageCount: 1, Phase: "a"})

	// op-3: interrupted
	j.LogBegin("op-3", "engineer", "s1")
	j.LogInterrupted("op-3", "cp_000001", 1, "b")

	incomplete := j.FindIncompleteOperations()
	if len(incomplete) != 1 {
		t.Fatalf("expected 1 incomplete, got %d", len(incomplete))
	}
	if incomplete[0].CorrelationID != "op-2" {
		t.Errorf("incomplete corr = %q, want %q", incomplete[0].CorrelationID, "op-2")
	}
}

func TestWALEmptyJournal(t *testing.T) {
	dir := t.TempDir()
	// Create the WAL directory structure manually.
	walDir := dir + "/agents/test-agent/wal"
	if err := os.MkdirAll(walDir, 0o755); err != nil {
		t.Fatal(err)
	}

	j, err := OpenSteeringJournal(agentlog.JournalConfig{
		SessionDir: dir,
		AgentName:  "test-agent",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()

	incomplete := j.FindIncompleteOperations()
	if len(incomplete) != 0 {
		t.Fatalf("expected 0 incomplete on empty journal, got %d", len(incomplete))
	}
}
