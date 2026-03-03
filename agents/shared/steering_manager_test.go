package shared

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/steering"
)

func TestSteeringManagerCreateLookupClose(t *testing.T) {
	sm := NewSteeringManager()

	ledger := sm.Create("corr-1", "agent-1", "session-1", nil, nil)
	if ledger == nil {
		t.Fatal("Create returned nil")
	}
	if ledger.CorrelationID != "corr-1" {
		t.Errorf("CorrelationID = %q, want %q", ledger.CorrelationID, "corr-1")
	}

	found := sm.Lookup("corr-1")
	if found != ledger {
		t.Error("Lookup should return the same ledger")
	}

	sm.Close("corr-1", false)
	if sm.Lookup("corr-1") != nil {
		t.Error("Lookup should return nil after Close")
	}
}

func TestSteeringManagerCloseAll(t *testing.T) {
	sm := NewSteeringManager()
	sm.Create("corr-1", "a", "s", nil, nil)
	sm.Create("corr-2", "b", "s", nil, nil)

	sm.CloseAll()

	if sm.Lookup("corr-1") != nil {
		t.Error("corr-1 should be nil after CloseAll")
	}
	if sm.Lookup("corr-2") != nil {
		t.Error("corr-2 should be nil after CloseAll")
	}
}

func TestSteeringManagerInitJournalRecovery(t *testing.T) {
	dir := t.TempDir()

	// Phase 1: create a journal, write an incomplete operation, close it.
	j, err := steering.OpenSteeringJournalDirect(dir)
	if err != nil {
		t.Fatal(err)
	}
	j.LogBegin("orphan-1", "architect", "session-x")
	j.Close()

	// Phase 2: init a new SteeringManager pointing at the same dir.
	sm := NewSteeringManager()
	if err := sm.InitJournal("architect", nil, dir); err != nil {
		t.Fatal(err)
	}
	defer sm.CloseAll()

	// The incomplete operation should have been closed during InitJournal.
	// Verify by opening the journal directly and scanning.
	j2, err := steering.OpenSteeringJournalDirect(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer j2.Close()

	incomplete := j2.FindIncompleteOperations()
	if len(incomplete) != 0 {
		t.Fatalf("expected 0 incomplete after InitJournal recovery, got %d", len(incomplete))
	}
}

func TestSteeringManagerNoJournal(t *testing.T) {
	sm := NewSteeringManager()

	// Ledger should still work without InitJournal being called.
	ledger := sm.Create("c1", "a", "s", nil, nil)
	if ledger == nil {
		t.Fatal("Create should work without a journal")
	}
	sm.CloseAll()
}

func TestSteeringManagerHandleSteer(t *testing.T) {
	sm := NewSteeringManager()
	ledger := sm.Create("corr-1", "a", "s", nil, nil)
	defer sm.CloseAll()

	handled := sm.HandleAction(&guide.ActionRequest{
		Action:        "steer",
		CorrelationID: "corr-1",
		Data:          "consider using JWT",
	})
	if !handled {
		t.Fatal("expected steer action to be handled")
	}

	cmds := ledger.Mailbox.Drain()
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command, got %d", len(cmds))
	}
	if cmds[0].Type != steering.CommandSteer {
		t.Errorf("command type = %v, want Steer", cmds[0].Type)
	}
	if cmds[0].Text != "consider using JWT" {
		t.Errorf("text = %q, want %q", cmds[0].Text, "consider using JWT")
	}
}

func TestSteeringManagerHandlePace(t *testing.T) {
	sm := NewSteeringManager()
	ledger := sm.Create("corr-1", "a", "s", nil, nil)
	defer sm.CloseAll()

	handled := sm.HandleAction(&guide.ActionRequest{
		Action:        "pace",
		CorrelationID: "corr-1",
		Data:          map[string]any{"pace": "step"},
	})
	if !handled {
		t.Fatal("expected pace action to be handled")
	}

	cmds := ledger.Mailbox.Drain()
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command, got %d", len(cmds))
	}
	if cmds[0].Type != steering.CommandPace {
		t.Errorf("command type = %v, want Pace", cmds[0].Type)
	}
	if cmds[0].Pace != steering.PaceStep {
		t.Errorf("pace = %v, want Step", cmds[0].Pace)
	}
}

func TestSteeringManagerHandleUnknownAction(t *testing.T) {
	sm := NewSteeringManager()
	sm.Create("corr-1", "a", "s", nil, nil)
	defer sm.CloseAll()

	handled := sm.HandleAction(&guide.ActionRequest{
		Action:        "unknown",
		CorrelationID: "corr-1",
		Data:          "test",
	})
	if handled {
		t.Error("unknown action should not be handled")
	}
}

func TestSteeringManagerHandleNilAction(t *testing.T) {
	sm := NewSteeringManager()
	if sm.HandleAction(nil) {
		t.Error("nil action should not be handled")
	}
}

func TestSteeringManagerHandleMissingLedger(t *testing.T) {
	sm := NewSteeringManager()

	handled := sm.HandleAction(&guide.ActionRequest{
		Action:        "steer",
		CorrelationID: "nonexistent",
		Data:          "test",
	})
	if handled {
		t.Error("steer to nonexistent ledger should not be handled")
	}
}

func TestContextHelpers(t *testing.T) {
	// nil context returns nil.
	if SteeringLedgerFromContext(nil) != nil {
		t.Error("nil ctx should return nil ledger")
	}

	// Empty context returns nil.
	ctx := context.Background()
	if SteeringLedgerFromContext(ctx) != nil {
		t.Error("empty ctx should return nil ledger")
	}

	// Round-trip.
	ledger := steering.NewSteeringLedger("c", "a", "s", nil, nil)
	ctx = WithSteeringLedger(ctx, ledger)
	got := SteeringLedgerFromContext(ctx)
	if got != ledger {
		t.Error("expected round-trip to return same ledger")
	}

	// nil ledger returns original context.
	ctx2 := WithSteeringLedger(ctx, nil)
	if ctx2 != ctx {
		t.Error("nil ledger should return original context")
	}
}

func TestSteeringManagerHandleRollback(t *testing.T) {
	sm := NewSteeringManager()
	ledger := sm.Create("corr-1", "a", "s", nil, nil)
	defer sm.CloseAll()

	handled := sm.HandleAction(&guide.ActionRequest{
		Action:        "rollback",
		CorrelationID: "corr-1",
		Data:          map[string]any{"checkpoint_id": "cp_000002"},
	})
	if !handled {
		t.Fatal("expected rollback action to be handled")
	}

	cmds := ledger.Mailbox.Drain()
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command, got %d", len(cmds))
	}
	if cmds[0].Type != steering.CommandRollback {
		t.Errorf("command type = %v, want Rollback", cmds[0].Type)
	}
	if cmds[0].CheckpointID != "cp_000002" {
		t.Errorf("checkpoint = %q, want %q", cmds[0].CheckpointID, "cp_000002")
	}
}

func TestSteeringManagerHandleEdit(t *testing.T) {
	sm := NewSteeringManager()
	ledger := sm.Create("corr-1", "a", "s", nil, nil)
	defer sm.CloseAll()

	handled := sm.HandleAction(&guide.ActionRequest{
		Action:        "edit",
		CorrelationID: "corr-1",
		Data: map[string]any{
			"checkpoint_id": "cp_000001",
			"message_index": float64(3),
			"new_text":      "edited message",
		},
	})
	if !handled {
		t.Fatal("expected edit action to be handled")
	}

	cmds := ledger.Mailbox.Drain()
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command, got %d", len(cmds))
	}
	if cmds[0].Type != steering.CommandEdit {
		t.Errorf("command type = %v, want Edit", cmds[0].Type)
	}
	if cmds[0].NewText != "edited message" {
		t.Errorf("new text = %q, want %q", cmds[0].NewText, "edited message")
	}
	if cmds[0].MessageIndex != 3 {
		t.Errorf("message index = %d, want %d", cmds[0].MessageIndex, 3)
	}
}

func TestSteeringManagerJournalPassthrough(t *testing.T) {
	dir := t.TempDir()

	sm := NewSteeringManager()
	if err := sm.InitJournal("test-agent", nil, dir); err != nil {
		t.Fatal(err)
	}

	// Create a ledger — it should receive the shared journal.
	ledger := sm.Create("corr-j1", "test-agent", "session-1", nil, nil)
	defer sm.CloseAll()

	// Record a checkpoint — should WAL-log via the shared journal.
	ledger.RecordCheckpoint(0, 1, "testing", nil)

	// The journal should now have entries. Re-open and verify.
	// First close everything to flush.
	sm.CloseAll()

	j, err := steering.OpenSteeringJournalDirect(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()

	// The Begin from Create + Checkpoint + Interrupted from CloseAll should be present.
	// Since CloseAll closes with interrupted=true, the bracket should be closed.
	incomplete := j.FindIncompleteOperations()
	if len(incomplete) != 0 {
		t.Fatalf("expected 0 incomplete, got %d", len(incomplete))
	}
}

func TestCancelEntryFireIdempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sm := NewSteeringManager()
	entry := sm.RegisterCancel("corr-cancel-1", cancel)

	if entry.IsFired() {
		t.Fatal("should not be fired before Fire()")
	}
	if ctx.Err() != nil {
		t.Fatal("context should not be cancelled before Fire()")
	}

	entry.Fire()
	if !entry.IsFired() {
		t.Fatal("should be fired after Fire()")
	}
	if ctx.Err() == nil {
		t.Fatal("context should be cancelled after Fire()")
	}

	// Second Fire is idempotent — should not panic.
	entry.Fire()
}

func TestCancelEntryHooksRunInOrder(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	sm := NewSteeringManager()
	entry := sm.RegisterCancel("corr-hooks", cancel)

	var order []int
	entry.AddHook(func() { order = append(order, 1) })
	entry.AddHook(func() { order = append(order, 2) })
	entry.AddHook(func() { order = append(order, 3) })

	entry.Fire()

	if len(order) != 3 {
		t.Fatalf("expected 3 hooks, got %d", len(order))
	}
	for i, v := range order {
		if v != i+1 {
			t.Errorf("order[%d] = %d, want %d", i, v, i+1)
		}
	}
}

func TestCancelEntryAddHookAfterFire(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	sm := NewSteeringManager()
	entry := sm.RegisterCancel("corr-late-hook", cancel)

	entry.Fire()

	// Adding a hook after Fire should run it immediately.
	ran := false
	entry.AddHook(func() { ran = true })
	if !ran {
		t.Fatal("hook added after Fire should run immediately")
	}
}

func TestCancelEntryDoneChannel(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	sm := NewSteeringManager()
	entry := sm.RegisterCancel("corr-done", cancel)

	select {
	case <-entry.Done():
		t.Fatal("done should not be closed before Fire()")
	default:
	}

	entry.Fire()

	select {
	case <-entry.Done():
		// Expected.
	case <-time.After(time.Second):
		t.Fatal("done should be closed after Fire()")
	}
}

func TestSteeringManagerCancelRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sm := NewSteeringManager()
	sm.RegisterCancel("corr-cr", cancel)

	found := sm.CancelRequest("corr-cr")
	if !found {
		t.Fatal("CancelRequest should find registered entry")
	}
	if ctx.Err() == nil {
		t.Fatal("context should be cancelled")
	}

	// Cancelling a non-existent correlation returns false.
	if sm.CancelRequest("nonexistent") {
		t.Fatal("CancelRequest for unknown correlation should return false")
	}
}

func TestSteeringManagerIsCancelled(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	sm := NewSteeringManager()
	sm.RegisterCancel("corr-ic", cancel)

	if sm.IsCancelled("corr-ic") {
		t.Fatal("should not be cancelled before CancelRequest")
	}
	sm.CancelRequest("corr-ic")
	if !sm.IsCancelled("corr-ic") {
		t.Fatal("should be cancelled after CancelRequest")
	}
	if sm.IsCancelled("nonexistent") {
		t.Fatal("unknown correlation should not be cancelled")
	}
}

func TestSteeringManagerHandleCancelAction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sm := NewSteeringManager()
	sm.RegisterCancel("corr-action", cancel)

	handled := sm.HandleAction(&guide.ActionRequest{
		Action:        "cancel",
		CorrelationID: "corr-action",
	})
	if !handled {
		t.Fatal("cancel action should be handled")
	}
	if ctx.Err() == nil {
		t.Fatal("context should be cancelled via HandleAction")
	}
}

func TestSteeringManagerHandleCancelActionFromData(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sm := NewSteeringManager()
	sm.RegisterCancel("corr-from-data", cancel)

	handled := sm.HandleAction(&guide.ActionRequest{
		Action:        "interrupt",
		CorrelationID: "other-id",
		Data: map[string]any{
			"correlation_id": "corr-from-data",
		},
	})
	if !handled {
		t.Fatal("interrupt action should be handled")
	}
	if ctx.Err() == nil {
		t.Fatal("context should be cancelled via data correlation_id")
	}
}

func TestSteeringManagerCloseAllFiresCancels(t *testing.T) {
	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	sm := NewSteeringManager()
	sm.RegisterCancel("corr-ca1", cancel1)
	sm.RegisterCancel("corr-ca2", cancel2)

	sm.CloseAll()

	if ctx1.Err() == nil {
		t.Fatal("ctx1 should be cancelled after CloseAll")
	}
	if ctx2.Err() == nil {
		t.Fatal("ctx2 should be cancelled after CloseAll")
	}
}

func TestSteeringManagerCloseRemovesCancelEntry(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	sm := NewSteeringManager()
	sm.Create("corr-close", "a", "s", nil, nil)
	sm.RegisterCancel("corr-close", cancel)

	sm.Close("corr-close", false)

	// After Close, the cancel entry should be removed.
	if sm.IsCancelled("corr-close") {
		t.Fatal("should not report cancelled after Close removed the entry")
	}
	if sm.CancelRequest("corr-close") {
		t.Fatal("CancelRequest should return false after Close")
	}
}

// Silence unused import warnings.
var _ = time.Now
