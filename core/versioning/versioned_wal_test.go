package versioning

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVersionedWAL_AppendDeltaAndRead(t *testing.T) {
	dir := t.TempDir()
	w, err := OpenVersionedWAL(VersionedWALConfig{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer w.Close()

	deltas := []WALFileDelta{
		{Path: "a.go", Op: WALDeltaOpCreate, NewContent: []byte("new a")},
	}
	v1, err := w.AppendDelta("pipe1", deltas)
	if err != nil {
		t.Fatalf("AppendDelta: %v", err)
	}
	if v1 != (SemanticVersion{Major: 0, Minor: 1}) {
		t.Errorf("v1 = %s, want 0.1", v1)
	}

	v2, err := w.AppendDelta("pipe2", deltas)
	if err != nil {
		t.Fatalf("AppendDelta: %v", err)
	}
	if v2 != (SemanticVersion{Major: 0, Minor: 2}) {
		t.Errorf("v2 = %s, want 0.2", v2)
	}

	if cur := w.CurrentVersion(); cur != v2 {
		t.Errorf("CurrentVersion = %s, want %s", cur, v2)
	}

	entry, err := w.GetEntry(v1)
	if err != nil {
		t.Fatalf("GetEntry: %v", err)
	}
	if entry.PipelineID != "pipe1" {
		t.Errorf("PipelineID = %q, want pipe1", entry.PipelineID)
	}
}

func TestVersionedWAL_AppendCheckpoint(t *testing.T) {
	dir := t.TempDir()
	w, err := OpenVersionedWAL(VersionedWALConfig{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer w.Close()

	w.AppendDelta("p1", []WALFileDelta{{Path: "a.go", Op: WALDeltaOpCreate, NewContent: []byte("x")}})
	w.AppendDelta("p2", []WALFileDelta{{Path: "b.go", Op: WALDeltaOpCreate, NewContent: []byte("y")}})

	chkVer, err := w.AppendCheckpoint([]WALFileDelta{
		{Path: "a.go", Op: WALDeltaOpCreate, NewContent: []byte("x"), OldContent: nil},
	})
	if err != nil {
		t.Fatalf("AppendCheckpoint: %v", err)
	}
	if chkVer != (SemanticVersion{Major: 1, Minor: 0}) {
		t.Errorf("checkpoint ver = %s, want 1.0", chkVer)
	}

	latest, ok := w.LatestCheckpoint()
	if !ok || latest != chkVer {
		t.Errorf("LatestCheckpoint = %s, %v, want %s, true", latest, ok, chkVer)
	}
}

func TestVersionedWAL_GetDeltasSince(t *testing.T) {
	dir := t.TempDir()
	w, err := OpenVersionedWAL(VersionedWALConfig{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer w.Close()

	d := []WALFileDelta{{Path: "f", Op: WALDeltaOpCreate, NewContent: []byte("x")}}
	w.AppendDelta("p1", d)
	v2, _ := w.AppendDelta("p2", d)
	w.AppendDelta("p3", d)

	// Get entries after v2 (exclusive), should only get v3.
	entries, err := w.GetDeltasSince(v2)
	if err != nil {
		t.Fatalf("GetDeltasSince: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].PipelineID != "p3" {
		t.Errorf("PipelineID = %q, want p3", entries[0].PipelineID)
	}
}

func TestVersionedWAL_IndexRecovery(t *testing.T) {
	dir := t.TempDir()
	w, err := OpenVersionedWAL(VersionedWALConfig{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	d := []WALFileDelta{{Path: "f", Op: WALDeltaOpCreate, NewContent: []byte("x")}}
	w.AppendDelta("p1", d)
	w.AppendDelta("p2", d)
	w.Close()

	// Delete index.json to force rebuild.
	os.Remove(filepath.Join(dir, "index.json"))

	w2, err := OpenVersionedWAL(VersionedWALConfig{Dir: dir})
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	defer w2.Close()

	if w2.CurrentVersion() != (SemanticVersion{Major: 0, Minor: 2}) {
		t.Errorf("recovered version = %s, want 0.2", w2.CurrentVersion())
	}
	if w2.IndexLen() != 2 {
		t.Errorf("recovered index len = %d, want 2", w2.IndexLen())
	}
}

func TestVersionedWAL_Compaction(t *testing.T) {
	dir := t.TempDir()
	w, err := OpenVersionedWAL(VersionedWALConfig{Dir: dir, RetainedMajors: 1})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer w.Close()

	d := []WALFileDelta{{Path: "f", Op: WALDeltaOpCreate, NewContent: []byte("x")}}
	w.AppendDelta("p1", d)
	w.AppendCheckpoint(d) // major 1
	w.AppendDelta("p2", d)
	w.AppendCheckpoint(d) // major 2

	if err := w.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// major 0 entries should be deleted, major 1+ retained.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() == "index.json" || e.Name() == "index.json.tmp" {
			continue
		}
		// File 000001.delta (major 0) should be gone.
		if e.Name() == "000001.delta" {
			t.Errorf("file %s should have been compacted", e.Name())
		}
	}
}
