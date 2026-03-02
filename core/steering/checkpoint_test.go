package steering

import (
	"encoding/json"
	"testing"
)

type testSnapshotter struct {
	state string
}

func (s *testSnapshotter) SnapshotState() (json.RawMessage, error) {
	return json.Marshal(map[string]string{"state": s.state})
}

func (s *testSnapshotter) RestoreState(data json.RawMessage) error {
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	s.state = m["state"]
	return nil
}

func TestCheckpointCaptureWithSnapshotter(t *testing.T) {
	var store CheckpointStore
	snap := &testSnapshotter{state: "analyzing"}

	cp := store.Capture(0, 3, "analyzing", snap)
	if cp.ID != "cp_000000" {
		t.Errorf("ID = %q, want %q", cp.ID, "cp_000000")
	}
	if cp.Turn != 0 {
		t.Errorf("Turn = %d, want 0", cp.Turn)
	}
	if cp.MessageCount != 3 {
		t.Errorf("MessageCount = %d, want 3", cp.MessageCount)
	}
	if cp.Phase != "analyzing" {
		t.Errorf("Phase = %q, want %q", cp.Phase, "analyzing")
	}
	if cp.AgentState == nil {
		t.Fatal("AgentState should not be nil with snapshotter")
	}
}

func TestCheckpointCaptureWithoutSnapshotter(t *testing.T) {
	var store CheckpointStore
	cp := store.Capture(1, 5, "designing", nil)
	if cp.AgentState != nil {
		t.Errorf("AgentState should be nil without snapshotter, got %s", string(cp.AgentState))
	}
	if cp.MessageCount != 5 {
		t.Errorf("MessageCount = %d, want 5", cp.MessageCount)
	}
}

func TestCheckpointFindByID(t *testing.T) {
	var store CheckpointStore
	store.Capture(0, 2, "a", nil)
	store.Capture(1, 5, "b", nil)
	store.Capture(2, 8, "c", nil)

	cp := store.FindByID("cp_000001")
	if cp == nil {
		t.Fatal("expected to find checkpoint cp_000001")
	}
	if cp.Turn != 1 {
		t.Errorf("Turn = %d, want 1", cp.Turn)
	}

	miss := store.FindByID("cp_999999")
	if miss != nil {
		t.Errorf("expected nil for missing ID, got %+v", miss)
	}
}

func TestCheckpointFindBefore(t *testing.T) {
	var store CheckpointStore
	store.Capture(0, 2, "a", nil)  // cp_000000
	store.Capture(1, 5, "b", nil)  // cp_000001
	store.Capture(2, 10, "c", nil) // cp_000002

	// Position 7: latest with msgCount <= 7 is cp_000001 (msgCount=5).
	cp := store.FindBefore(7)
	if cp == nil {
		t.Fatal("expected to find checkpoint before position 7")
	}
	if cp.ID != "cp_000001" {
		t.Errorf("ID = %q, want %q", cp.ID, "cp_000001")
	}

	// Position 1: no checkpoint with msgCount <= 1.
	miss := store.FindBefore(1)
	if miss != nil {
		t.Errorf("expected nil for position 1, got %+v", miss)
	}

	// Position 10: latest with msgCount <= 10 is cp_000002.
	cp = store.FindBefore(10)
	if cp == nil {
		t.Fatal("expected to find checkpoint before position 10")
	}
	if cp.ID != "cp_000002" {
		t.Errorf("ID = %q, want %q", cp.ID, "cp_000002")
	}
}

func TestCheckpointLatest(t *testing.T) {
	var store CheckpointStore

	if store.Latest() != nil {
		t.Fatal("Latest on empty store should be nil")
	}

	store.Capture(0, 2, "a", nil)
	store.Capture(1, 5, "b", nil)

	cp := store.Latest()
	if cp == nil {
		t.Fatal("expected non-nil Latest")
	}
	if cp.Turn != 1 {
		t.Errorf("Turn = %d, want 1", cp.Turn)
	}
}

func TestCheckpointAll(t *testing.T) {
	var store CheckpointStore

	if store.All() != nil {
		t.Fatal("All on empty store should be nil")
	}

	for i := range 5 {
		store.Capture(i, (i+1)*2, "phase", nil)
	}

	all := store.All()
	if len(all) != 5 {
		t.Fatalf("expected 5 checkpoints, got %d", len(all))
	}
	for i, cp := range all {
		if cp.Turn != i {
			t.Errorf("all[%d].Turn = %d, want %d", i, cp.Turn, i)
		}
	}
}

func TestCheckpointRingOverflow(t *testing.T) {
	var store CheckpointStore

	// Fill beyond capacity.
	for i := range maxCheckpoints + 5 {
		store.Capture(i, i*2, "phase", nil)
	}

	if store.Count() != maxCheckpoints {
		t.Fatalf("expected %d checkpoints, got %d", maxCheckpoints, store.Count())
	}

	all := store.All()
	// Oldest surviving should be turn 5 (first 5 were overwritten).
	if all[0].Turn != 5 {
		t.Errorf("oldest turn = %d, want 5", all[0].Turn)
	}
	if all[len(all)-1].Turn != maxCheckpoints+4 {
		t.Errorf("newest turn = %d, want %d", all[len(all)-1].Turn, maxCheckpoints+4)
	}
}
