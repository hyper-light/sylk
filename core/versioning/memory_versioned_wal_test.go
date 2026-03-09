package versioning

import "testing"

func TestMemoryVersionedWAL_AppendAndQuery(t *testing.T) {
	wal := NewMemoryVersionedWAL()
	defer wal.Close()

	deltas := []WALFileDelta{{Path: "a.go", Op: WALDeltaOpCreate, NewContent: []byte("package a")}}
	v1, err := wal.AppendDelta("task_1", deltas)
	if err != nil {
		t.Fatalf("AppendDelta: %v", err)
	}
	if v1 != (SemanticVersion{Major: 0, Minor: 1}) {
		t.Fatalf("version = %s, want 0.1", v1)
	}

	entry, err := wal.GetEntry(v1)
	if err != nil {
		t.Fatalf("GetEntry: %v", err)
	}
	if entry.PipelineID != "task_1" {
		t.Fatalf("pipeline = %q, want task_1", entry.PipelineID)
	}

	chk, err := wal.AppendCheckpoint(deltas)
	if err != nil {
		t.Fatalf("AppendCheckpoint: %v", err)
	}
	if chk != (SemanticVersion{Major: 1, Minor: 0}) {
		t.Fatalf("checkpoint = %s, want 1.0", chk)
	}

	entries, err := wal.GetDeltasSince(VersionZero)
	if err != nil {
		t.Fatalf("GetDeltasSince: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
}
