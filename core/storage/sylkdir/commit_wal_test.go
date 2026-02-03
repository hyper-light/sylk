package sylkdir

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/wal"
)

func TestCommitWAL_OpenClose(t *testing.T) {
	dir := t.TempDir()

	cw, err := OpenCommitWAL(CommitWALConfig{Dir: dir, SyncOnWrite: true})
	if err != nil {
		t.Fatalf("OpenCommitWAL: %v", err)
	}

	if err := cw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Verify segment file was created.
	entries, _ := os.ReadDir(dir)
	found := false
	for _, e := range entries {
		if !e.IsDir() {
			found = true
		}
	}
	if !found {
		t.Fatal("expected at least one segment file")
	}
}

func TestCommitWAL_BeginEnd(t *testing.T) {
	dir := t.TempDir()
	cw, err := OpenCommitWAL(CommitWALConfig{Dir: dir, SyncOnWrite: true})
	if err != nil {
		t.Fatalf("OpenCommitWAL: %v", err)
	}
	defer cw.Close()

	data := &WALCommitData{SessionID: 1, Major: 2, Minor: 0, Patch: 0}

	beginSeq, err := cw.LogCommitBegin(data)
	if err != nil {
		t.Fatalf("LogCommitBegin: %v", err)
	}
	if beginSeq != 1 {
		t.Errorf("begin seq = %d, want 1", beginSeq)
	}

	endSeq, err := cw.LogCommitEnd(data)
	if err != nil {
		t.Fatalf("LogCommitEnd: %v", err)
	}
	if endSeq != 2 {
		t.Errorf("end seq = %d, want 2", endSeq)
	}

	if cw.LastSequence() != 2 {
		t.Errorf("LastSequence = %d, want 2", cw.LastSequence())
	}
}

func TestCommitWAL_FindIncompleteCommits_None(t *testing.T) {
	dir := t.TempDir()
	cw, err := OpenCommitWAL(CommitWALConfig{Dir: dir, SyncOnWrite: true})
	if err != nil {
		t.Fatalf("OpenCommitWAL: %v", err)
	}
	defer cw.Close()

	// Complete commit.
	data := &WALCommitData{SessionID: 1, Major: 2, Minor: 0, Patch: 0}
	cw.LogCommitBegin(data)
	cw.LogCommitEnd(data)

	incomplete, err := cw.FindIncompleteCommits()
	if err != nil {
		t.Fatalf("FindIncompleteCommits: %v", err)
	}
	if len(incomplete) != 0 {
		t.Errorf("expected 0 incomplete, got %d", len(incomplete))
	}
}

func TestCommitWAL_FindIncompleteCommits_One(t *testing.T) {
	dir := t.TempDir()
	cw, err := OpenCommitWAL(CommitWALConfig{Dir: dir, SyncOnWrite: true})
	if err != nil {
		t.Fatalf("OpenCommitWAL: %v", err)
	}
	defer cw.Close()

	// Begin without end.
	data := &WALCommitData{SessionID: 5, Major: 3, Minor: 0, Patch: 0}
	cw.LogCommitBegin(data)

	incomplete, err := cw.FindIncompleteCommits()
	if err != nil {
		t.Fatalf("FindIncompleteCommits: %v", err)
	}
	if len(incomplete) != 1 {
		t.Fatalf("expected 1 incomplete, got %d", len(incomplete))
	}
	if incomplete[0].SessionID != 5 {
		t.Errorf("SessionID = %d, want 5", incomplete[0].SessionID)
	}
	if incomplete[0].GlobalVersion.Major != 3 {
		t.Errorf("Major = %d, want 3", incomplete[0].GlobalVersion.Major)
	}
	if incomplete[0].BeginSeq != 1 {
		t.Errorf("BeginSeq = %d, want 1", incomplete[0].BeginSeq)
	}
}

func TestCommitWAL_Recovery(t *testing.T) {
	dir := t.TempDir()

	// First instance: write begin, close without end (simulates crash).
	cw1, err := OpenCommitWAL(CommitWALConfig{Dir: dir, SyncOnWrite: true})
	if err != nil {
		t.Fatalf("OpenCommitWAL #1: %v", err)
	}

	data := &WALCommitData{SessionID: 7, Major: 4, Minor: 0, Patch: 0}
	cw1.LogCommitBegin(data)
	cw1.Close()

	// Second instance: recover and find incomplete.
	cw2, err := OpenCommitWAL(CommitWALConfig{Dir: dir, SyncOnWrite: true})
	if err != nil {
		t.Fatalf("OpenCommitWAL #2: %v", err)
	}
	defer cw2.Close()

	incomplete, err := cw2.FindIncompleteCommits()
	if err != nil {
		t.Fatalf("FindIncompleteCommits: %v", err)
	}
	if len(incomplete) != 1 {
		t.Fatalf("expected 1 incomplete, got %d", len(incomplete))
	}
	if incomplete[0].SessionID != 7 {
		t.Errorf("SessionID = %d, want 7", incomplete[0].SessionID)
	}
	if incomplete[0].GlobalVersion.Major != 4 {
		t.Errorf("Major = %d, want 4", incomplete[0].GlobalVersion.Major)
	}
}

func TestCommitWAL_Replay(t *testing.T) {
	dir := t.TempDir()
	cw, err := OpenCommitWAL(CommitWALConfig{Dir: dir, SyncOnWrite: true})
	if err != nil {
		t.Fatalf("OpenCommitWAL: %v", err)
	}
	defer cw.Close()

	data := &WALCommitData{SessionID: 1, Major: 2, Minor: 0, Patch: 0}
	cw.LogCommitBegin(data)
	cw.LogCommitEnd(data)

	var begins, ends int
	result, err := cw.Replay(CommitReplayHandler{
		OnCommitBegin: func(d *WALCommitData) error {
			begins++
			if d.SessionID != 1 {
				t.Errorf("begin SessionID = %d, want 1", d.SessionID)
			}
			return nil
		},
		OnCommitEnd: func(d *WALCommitData) error {
			ends++
			if d.Major != 2 {
				t.Errorf("end Major = %d, want 2", d.Major)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}

	if begins != 1 {
		t.Errorf("begins = %d, want 1", begins)
	}
	if ends != 1 {
		t.Errorf("ends = %d, want 1", ends)
	}
	if result.EntriesRead != 2 {
		t.Errorf("EntriesRead = %d, want 2", result.EntriesRead)
	}
	if result.Counts[wal.OpCommitBegin] != 1 {
		t.Errorf("OpCommitBegin count = %d, want 1", result.Counts[wal.OpCommitBegin])
	}
	if result.Counts[wal.OpCommitEnd] != 1 {
		t.Errorf("OpCommitEnd count = %d, want 1", result.Counts[wal.OpCommitEnd])
	}
}

func TestCommitWAL_ReplayNilHandlers(t *testing.T) {
	dir := t.TempDir()
	cw, err := OpenCommitWAL(CommitWALConfig{Dir: dir, SyncOnWrite: true})
	if err != nil {
		t.Fatalf("OpenCommitWAL: %v", err)
	}
	defer cw.Close()

	data := &WALCommitData{SessionID: 1, Major: 2, Minor: 0, Patch: 0}
	cw.LogCommitBegin(data)
	cw.LogCommitEnd(data)

	// Nil handlers should not panic.
	result, err := cw.Replay(CommitReplayHandler{})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if result.EntriesRead != 2 {
		t.Errorf("EntriesRead = %d, want 2", result.EntriesRead)
	}
}

func TestCommitWAL_MultipleCommits(t *testing.T) {
	dir := t.TempDir()
	cw, err := OpenCommitWAL(CommitWALConfig{Dir: dir, SyncOnWrite: true})
	if err != nil {
		t.Fatalf("OpenCommitWAL: %v", err)
	}
	defer cw.Close()

	// 3 commits: 2 complete, 1 incomplete.
	for i := uint32(1); i <= 3; i++ {
		data := &WALCommitData{SessionID: i, Major: uint16(i + 1), Minor: 0, Patch: 0}
		cw.LogCommitBegin(data)
		if i <= 2 {
			cw.LogCommitEnd(data)
		}
	}

	incomplete, err := cw.FindIncompleteCommits()
	if err != nil {
		t.Fatalf("FindIncompleteCommits: %v", err)
	}
	if len(incomplete) != 1 {
		t.Fatalf("expected 1 incomplete, got %d", len(incomplete))
	}
	if incomplete[0].SessionID != 3 {
		t.Errorf("SessionID = %d, want 3", incomplete[0].SessionID)
	}
	if incomplete[0].GlobalVersion.Major != 4 {
		t.Errorf("Major = %d, want 4", incomplete[0].GlobalVersion.Major)
	}
}

func TestCommitWAL_Checkpoint(t *testing.T) {
	dir := t.TempDir()
	cw, err := OpenCommitWAL(CommitWALConfig{Dir: dir, SyncOnWrite: true})
	if err != nil {
		t.Fatalf("OpenCommitWAL: %v", err)
	}
	defer cw.Close()

	data := &WALCommitData{SessionID: 1, Major: 2, Minor: 0, Patch: 0}
	cw.LogCommitBegin(data)
	seq, _ := cw.LogCommitEnd(data)

	if err := cw.Checkpoint(seq); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	if cw.CheckpointedSequence() != seq {
		t.Errorf("CheckpointedSequence = %d, want %d", cw.CheckpointedSequence(), seq)
	}

	// Verify checkpoint file exists.
	cpPath := filepath.Join(dir, commitCheckpointFile)
	if _, err := os.Stat(cpPath); err != nil {
		t.Errorf("checkpoint file missing: %v", err)
	}

	// After checkpoint, entries before seq should be filtered on ReadAll.
	entries, err := cw.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries after checkpoint, got %d", len(entries))
	}
}

func TestCommitWAL_WALBytes(t *testing.T) {
	dir := t.TempDir()
	cw, err := OpenCommitWAL(CommitWALConfig{Dir: dir, SyncOnWrite: true})
	if err != nil {
		t.Fatalf("OpenCommitWAL: %v", err)
	}
	defer cw.Close()

	initial := cw.WALBytes()

	data := &WALCommitData{SessionID: 1, Major: 2, Minor: 0, Patch: 0}
	cw.LogCommitBegin(data)

	after := cw.WALBytes()
	if after <= initial {
		t.Errorf("WALBytes after write (%d) should be > initial (%d)", after, initial)
	}
}

func TestCommitWAL_Truncate(t *testing.T) {
	dir := t.TempDir()
	cw, err := OpenCommitWAL(CommitWALConfig{Dir: dir, SyncOnWrite: true})
	if err != nil {
		t.Fatalf("OpenCommitWAL: %v", err)
	}
	defer cw.Close()

	data := &WALCommitData{SessionID: 1, Major: 2, Minor: 0, Patch: 0}
	cw.LogCommitBegin(data)
	cw.LogCommitEnd(data)

	if err := cw.Truncate(); err != nil {
		t.Fatalf("Truncate: %v", err)
	}

	if cw.LastSequence() != 0 {
		t.Errorf("LastSequence = %d, want 0", cw.LastSequence())
	}

	// After truncate, no incomplete commits.
	incomplete, err := cw.FindIncompleteCommits()
	if err != nil {
		t.Fatalf("FindIncompleteCommits: %v", err)
	}
	if len(incomplete) != 0 {
		t.Errorf("expected 0 incomplete after truncate, got %d", len(incomplete))
	}
}

func TestCommitWAL_ReadAll(t *testing.T) {
	dir := t.TempDir()
	cw, err := OpenCommitWAL(CommitWALConfig{Dir: dir, SyncOnWrite: true})
	if err != nil {
		t.Fatalf("OpenCommitWAL: %v", err)
	}
	defer cw.Close()

	d1 := &WALCommitData{SessionID: 1, Major: 2, Minor: 0, Patch: 0}
	d2 := &WALCommitData{SessionID: 2, Major: 3, Minor: 0, Patch: 0}
	cw.LogCommitBegin(d1)
	cw.LogCommitEnd(d1)
	cw.LogCommitBegin(d2)

	entries, err := cw.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].OpType != wal.OpCommitBegin {
		t.Errorf("entries[0].OpType = %v, want CommitBegin", entries[0].OpType)
	}
	if entries[1].OpType != wal.OpCommitEnd {
		t.Errorf("entries[1].OpType = %v, want CommitEnd", entries[1].OpType)
	}
	if entries[2].OpType != wal.OpCommitBegin {
		t.Errorf("entries[2].OpType = %v, want CommitBegin", entries[2].OpType)
	}
}

func TestCommitWAL_RecoverCompletesAfterCrash(t *testing.T) {
	dir := t.TempDir()

	// Simulate: begin → crash → recover → complete.
	cw1, _ := OpenCommitWAL(CommitWALConfig{Dir: dir, SyncOnWrite: true})
	data := &WALCommitData{SessionID: 10, Major: 5, Minor: 0, Patch: 0}
	cw1.LogCommitBegin(data)
	cw1.Close()

	// Recover.
	cw2, _ := OpenCommitWAL(CommitWALConfig{Dir: dir, SyncOnWrite: true})
	defer cw2.Close()

	// Incomplete before completion.
	incomplete, _ := cw2.FindIncompleteCommits()
	if len(incomplete) != 1 {
		t.Fatalf("expected 1 incomplete, got %d", len(incomplete))
	}

	// Now complete the commit.
	cw2.LogCommitEnd(data)

	// No more incomplete.
	incomplete, _ = cw2.FindIncompleteCommits()
	if len(incomplete) != 0 {
		t.Errorf("expected 0 incomplete after end, got %d", len(incomplete))
	}
}
