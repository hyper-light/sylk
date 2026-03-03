package shared

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/steering"
)

func TestDrainAndCheckpointNilLedger(t *testing.T) {
	req := &providers.Request{
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: "hello"},
		},
	}

	result := DrainAndCheckpoint(nil, req, 0, "test", nil)
	if result.Steered || result.Rollback != nil || result.EditReplay != nil || result.ShouldPause {
		t.Error("nil ledger should return zero SteeringResult")
	}
	if len(req.Messages) != 1 {
		t.Errorf("messages should be unchanged, got %d", len(req.Messages))
	}
}

func TestDrainAndCheckpointEmptyMailbox(t *testing.T) {
	ledger := steering.NewSteeringLedger("c1", "architect", "s1", nil, nil)
	defer ledger.Close(false)

	req := &providers.Request{
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: "hello"},
		},
	}

	result := DrainAndCheckpoint(ledger, req, 0, "analyzing", nil)
	if result.Steered {
		t.Error("empty mailbox should not set Steered")
	}
	if result.Rollback != nil {
		t.Error("empty mailbox should not set Rollback")
	}

	// Checkpoint should have been recorded.
	cp := ledger.Checkpoints.Latest()
	if cp == nil {
		t.Fatal("expected checkpoint to be recorded")
	}
	if cp.Phase != "analyzing" {
		t.Errorf("phase = %q, want %q", cp.Phase, "analyzing")
	}
}

func TestDrainAndCheckpointSteer(t *testing.T) {
	ledger := steering.NewSteeringLedger("c1", "architect", "s1", nil, nil)
	defer ledger.Close(false)

	req := &providers.Request{
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: "build auth"},
		},
	}

	ledger.DepositCommand(steering.Command{
		Type:      steering.CommandSteer,
		Text:      "use RS256",
		Timestamp: time.Now(),
	})

	result := DrainAndCheckpoint(ledger, req, 0, "analyzing", nil)
	if !result.Steered {
		t.Error("expected Steered=true after steer command")
	}
	if len(req.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(req.Messages))
	}
	if req.Messages[1].Role != providers.RoleUser {
		t.Errorf("injected message role = %q, want %q", req.Messages[1].Role, providers.RoleUser)
	}
}

func TestDrainAndCheckpointMultipleSteer(t *testing.T) {
	ledger := steering.NewSteeringLedger("c1", "architect", "s1", nil, nil)
	defer ledger.Close(false)

	req := &providers.Request{
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: "build auth"},
		},
	}

	ledger.DepositCommand(steering.Command{
		Type: steering.CommandSteer, Text: "use RS256", Timestamp: time.Now(),
	})
	ledger.DepositCommand(steering.Command{
		Type: steering.CommandSteer, Text: "add rate limiting", Timestamp: time.Now(),
	})

	result := DrainAndCheckpoint(ledger, req, 0, "analyzing", nil)
	if !result.Steered {
		t.Error("expected Steered=true")
	}
	// Should be compacted into ONE message.
	if len(req.Messages) != 2 {
		t.Fatalf("expected 2 messages (original + compacted), got %d", len(req.Messages))
	}
}

func TestDrainAndCheckpointRollback(t *testing.T) {
	ledger := steering.NewSteeringLedger("c1", "architect", "s1", nil, nil)
	defer ledger.Close(false)

	req := &providers.Request{
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: "msg1"},
			{Role: providers.RoleAssistant, Content: "resp1"},
			{Role: providers.RoleUser, Content: "msg2"},
		},
	}

	// Record a checkpoint at turn 0 with 1 message.
	ledger.RecordCheckpoint(0, 1, "start", nil)

	// Deposit rollback to that checkpoint.
	ledger.DepositCommand(steering.Command{
		Type:         steering.CommandRollback,
		CheckpointID: "cp_000000",
		Timestamp:    time.Now(),
	})

	result := DrainAndCheckpoint(ledger, req, 1, "designing", nil)
	if result.Rollback == nil {
		t.Fatal("expected Rollback to be set")
	}
	if result.Rollback.ID != "cp_000000" {
		t.Errorf("rollback ID = %q, want %q", result.Rollback.ID, "cp_000000")
	}
}

func TestDrainAndCheckpointRollbackBeatsSteer(t *testing.T) {
	ledger := steering.NewSteeringLedger("c1", "architect", "s1", nil, nil)
	defer ledger.Close(false)

	req := &providers.Request{
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: "hello"},
		},
	}

	ledger.RecordCheckpoint(0, 1, "start", nil)

	// Both rollback and steer pending — rollback should win.
	ledger.DepositCommand(steering.Command{
		Type: steering.CommandSteer, Text: "steer me", Timestamp: time.Now(),
	})
	ledger.DepositCommand(steering.Command{
		Type:         steering.CommandRollback,
		CheckpointID: "cp_000000",
		Timestamp:    time.Now(),
	})

	result := DrainAndCheckpoint(ledger, req, 1, "analyzing", nil)
	if result.Rollback == nil {
		t.Fatal("rollback should take priority over steer")
	}
	if result.Steered {
		t.Error("steer should not be set when rollback wins")
	}
}

func TestDrainAndCheckpointEdit(t *testing.T) {
	ledger := steering.NewSteeringLedger("c1", "architect", "s1", nil, nil)
	defer ledger.Close(false)

	req := &providers.Request{
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: "original"},
			{Role: providers.RoleAssistant, Content: "response"},
			{Role: providers.RoleUser, Content: "follow up"},
			{Role: providers.RoleAssistant, Content: "second response"},
		},
	}

	// Checkpoint at turn 0 with msgCount=2 (after first exchange).
	ledger.RecordCheckpoint(0, 2, "start", nil)

	// Edit the follow-up message (index 2) — should find checkpoint at msgCount=2.
	ledger.DepositCommand(steering.Command{
		Type:         steering.CommandEdit,
		MessageIndex: 2,
		NewText:      "edited version",
		Timestamp:    time.Now(),
	})

	result := DrainAndCheckpoint(ledger, req, 1, "designing", nil)
	if result.EditReplay == nil {
		t.Fatal("expected EditReplay to be set")
	}
	if result.EditText != "edited version" {
		t.Errorf("edit text = %q, want %q", result.EditText, "edited version")
	}
}

func TestDrainAndCheckpointPaceStep(t *testing.T) {
	ledger := steering.NewSteeringLedger("c1", "architect", "s1", nil, nil)
	defer ledger.Close(false)

	req := &providers.Request{
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: "hello"},
		},
	}

	ledger.SetPace(steering.PaceStep)

	result := DrainAndCheckpoint(ledger, req, 0, "analyzing", nil)
	if !result.ShouldPause {
		t.Error("expected ShouldPause=true with PaceStep")
	}
}

func TestDrainAndCheckpointPaceCommandInMailbox(t *testing.T) {
	ledger := steering.NewSteeringLedger("c1", "architect", "s1", nil, nil)
	defer ledger.Close(false)

	req := &providers.Request{
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: "hello"},
		},
	}

	ledger.DepositCommand(steering.Command{
		Type:      steering.CommandPace,
		Pace:      steering.PacePaused,
		Timestamp: time.Now(),
	})

	result := DrainAndCheckpoint(ledger, req, 0, "analyzing", nil)
	if !result.ShouldPause {
		t.Error("expected ShouldPause=true after CommandPace(Paused)")
	}
}

func TestDrainAndCheckpointResumeCommand(t *testing.T) {
	ledger := steering.NewSteeringLedger("c1", "architect", "s1", nil, nil)
	defer ledger.Close(false)

	req := &providers.Request{
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: "hello"},
		},
	}

	ledger.SetPace(steering.PacePaused)

	ledger.DepositCommand(steering.Command{
		Type:      steering.CommandResume,
		Timestamp: time.Now(),
	})

	result := DrainAndCheckpoint(ledger, req, 0, "analyzing", nil)
	if result.ShouldPause {
		t.Error("expected ShouldPause=false after Resume command")
	}
}
