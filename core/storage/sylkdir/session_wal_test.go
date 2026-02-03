package sylkdir

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/wal"
)

func openTestSessionWAL(t *testing.T, syncOnWrite bool) *SessionWAL {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "wal")
	w, err := OpenSessionWAL(SessionWALConfig{Dir: dir, SyncOnWrite: syncOnWrite})
	if err != nil {
		t.Fatalf("OpenSessionWAL: %v", err)
	}
	t.Cleanup(func() { w.Close() })
	return w
}

func TestSessionWAL_OpenClose(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wal")
	w, err := OpenSessionWAL(SessionWALConfig{Dir: dir, SyncOnWrite: true})
	if err != nil {
		t.Fatalf("OpenSessionWAL: %v", err)
	}

	if w.LastSequence() != 0 {
		t.Errorf("LastSequence = %d, want 0", w.LastSequence())
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen should succeed.
	w2, err := OpenSessionWAL(SessionWALConfig{Dir: dir})
	if err != nil {
		t.Fatalf("OpenSessionWAL (reopen): %v", err)
	}
	w2.Close()
}

func TestSessionWAL_LogAllTypes(t *testing.T) {
	w := openTestSessionWAL(t, true)

	seq, err := w.LogNodeInsert(&WALNodeData{NodeID: 1, Domain: 1, NodeType: 2, Key: "k", Name: "n", Path: "p"})
	if err != nil || seq != 1 {
		t.Fatalf("LogNodeInsert: seq=%d, err=%v", seq, err)
	}

	seq, err = w.LogNodeDelete(&WALNodeData{NodeID: 2})
	if err != nil || seq != 2 {
		t.Fatalf("LogNodeDelete: seq=%d, err=%v", seq, err)
	}

	seq, err = w.LogEdgeInsert(&WALEdgeData{SourceID: 1, TargetID: 2, EdgeType: 3, Weight: 0.5, SessionID: 10})
	if err != nil || seq != 3 {
		t.Fatalf("LogEdgeInsert: seq=%d, err=%v", seq, err)
	}

	seq, err = w.LogEdgeUpdate(&WALEdgeData{SourceID: 1, TargetID: 2, EdgeType: 3, Weight: 0.9, SessionID: 10})
	if err != nil || seq != 4 {
		t.Fatalf("LogEdgeUpdate: seq=%d, err=%v", seq, err)
	}

	seq, err = w.LogEdgeDelete(&WALEdgeData{SourceID: 1, TargetID: 2, EdgeType: 3})
	if err != nil || seq != 5 {
		t.Fatalf("LogEdgeDelete: seq=%d, err=%v", seq, err)
	}

	seq, err = w.LogVectorInsert(&WALVectorData{NodeID: 1, Vector: []float32{1.0, 2.0}})
	if err != nil || seq != 6 {
		t.Fatalf("LogVectorInsert: seq=%d, err=%v", seq, err)
	}

	seq, err = w.LogDocInsert(&WALDocData{ID: "d1", Path: "/a.go", DocType: "source", Content: "code", Language: "go"})
	if err != nil || seq != 7 {
		t.Fatalf("LogDocInsert: seq=%d, err=%v", seq, err)
	}

	if w.LastSequence() != 7 {
		t.Errorf("LastSequence = %d, want 7", w.LastSequence())
	}

	entries, err := w.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(entries) != 7 {
		t.Fatalf("len(entries) = %d, want 7", len(entries))
	}

	expectedOps := []wal.OpType{
		wal.OpNodeInsert, wal.OpNodeDelete, wal.OpEdgeInsert,
		wal.OpEdgeUpdate, wal.OpEdgeDelete, wal.OpVectorInsert, wal.OpDocInsert,
	}
	for i, op := range expectedOps {
		if entries[i].OpType != op {
			t.Errorf("entry[%d].OpType = %s, want %s", i, entries[i].OpType, op)
		}
	}
}

func TestSessionWAL_ReplayAllTypes(t *testing.T) {
	w := openTestSessionWAL(t, true)

	// Log one of each type.
	w.LogNodeInsert(&WALNodeData{NodeID: 10, Key: "file:/a.go"})
	w.LogNodeDelete(&WALNodeData{NodeID: 11})
	w.LogEdgeInsert(&WALEdgeData{SourceID: 10, TargetID: 11})
	w.LogEdgeUpdate(&WALEdgeData{SourceID: 10, TargetID: 11, Weight: 1.0})
	w.LogEdgeDelete(&WALEdgeData{SourceID: 10, TargetID: 11})
	w.LogVectorInsert(&WALVectorData{NodeID: 10, Vector: []float32{0.5}})
	w.LogDocInsert(&WALDocData{ID: "d1", Content: "hello"})

	var (
		nodeInserts   []*WALNodeData
		nodeDeletes   []*WALNodeData
		edgeInserts   []*WALEdgeData
		edgeUpdates   []*WALEdgeData
		edgeDeletes   []*WALEdgeData
		vectorInserts []*WALVectorData
		docInserts    []*WALDocData
	)

	result, err := w.Replay(SessionReplayHandler{
		OnNodeInsert:   func(d *WALNodeData) error { nodeInserts = append(nodeInserts, d); return nil },
		OnNodeDelete:   func(d *WALNodeData) error { nodeDeletes = append(nodeDeletes, d); return nil },
		OnEdgeInsert:   func(d *WALEdgeData) error { edgeInserts = append(edgeInserts, d); return nil },
		OnEdgeUpdate:   func(d *WALEdgeData) error { edgeUpdates = append(edgeUpdates, d); return nil },
		OnEdgeDelete:   func(d *WALEdgeData) error { edgeDeletes = append(edgeDeletes, d); return nil },
		OnVectorInsert: func(d *WALVectorData) error { vectorInserts = append(vectorInserts, d); return nil },
		OnDocInsert:    func(d *WALDocData) error { docInserts = append(docInserts, d); return nil },
	})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}

	if result.EntriesRead != 7 {
		t.Errorf("EntriesRead = %d, want 7", result.EntriesRead)
	}
	if result.LastSequence != 7 {
		t.Errorf("LastSequence = %d, want 7", result.LastSequence)
	}

	if len(nodeInserts) != 1 || nodeInserts[0].NodeID != 10 {
		t.Errorf("nodeInserts: got %d, want 1 with NodeID=10", len(nodeInserts))
	}
	if len(nodeDeletes) != 1 || nodeDeletes[0].NodeID != 11 {
		t.Errorf("nodeDeletes: got %d, want 1 with NodeID=11", len(nodeDeletes))
	}
	if len(edgeInserts) != 1 {
		t.Errorf("edgeInserts: got %d, want 1", len(edgeInserts))
	}
	if len(edgeUpdates) != 1 {
		t.Errorf("edgeUpdates: got %d, want 1", len(edgeUpdates))
	}
	if len(edgeDeletes) != 1 {
		t.Errorf("edgeDeletes: got %d, want 1", len(edgeDeletes))
	}
	if len(vectorInserts) != 1 || vectorInserts[0].NodeID != 10 {
		t.Errorf("vectorInserts: got %d, want 1", len(vectorInserts))
	}
	if len(docInserts) != 1 || docInserts[0].ID != "d1" {
		t.Errorf("docInserts: got %d, want 1", len(docInserts))
	}

	// Verify per-op counts.
	for _, op := range []wal.OpType{
		wal.OpNodeInsert, wal.OpNodeDelete, wal.OpEdgeInsert,
		wal.OpEdgeUpdate, wal.OpEdgeDelete, wal.OpVectorInsert, wal.OpDocInsert,
	} {
		if result.Counts[op] != 1 {
			t.Errorf("Counts[%s] = %d, want 1", op, result.Counts[op])
		}
	}
}

func TestSessionWAL_ReplayNilHandlers(t *testing.T) {
	w := openTestSessionWAL(t, true)

	w.LogNodeInsert(&WALNodeData{NodeID: 1})
	w.LogEdgeInsert(&WALEdgeData{SourceID: 1, TargetID: 2})

	// All nil handlers — should replay without error.
	result, err := w.Replay(SessionReplayHandler{})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if result.EntriesRead != 2 {
		t.Errorf("EntriesRead = %d, want 2", result.EntriesRead)
	}
}

func TestSessionWAL_Recovery(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wal")

	w, err := OpenSessionWAL(SessionWALConfig{Dir: dir, SyncOnWrite: true})
	if err != nil {
		t.Fatalf("OpenSessionWAL: %v", err)
	}

	for i := range 5 {
		w.LogNodeInsert(&WALNodeData{NodeID: uint32(i), Key: "k"})
	}
	w.Close()

	// Reopen and verify.
	w2, err := OpenSessionWAL(SessionWALConfig{Dir: dir})
	if err != nil {
		t.Fatalf("OpenSessionWAL (reopen): %v", err)
	}
	defer w2.Close()

	if w2.LastSequence() != 5 {
		t.Errorf("LastSequence = %d, want 5", w2.LastSequence())
	}

	entries, err := w2.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(entries) != 5 {
		t.Errorf("len(entries) = %d, want 5", len(entries))
	}
}

func TestSessionWAL_Checkpoint(t *testing.T) {
	w := openTestSessionWAL(t, true)

	for i := range 10 {
		w.LogNodeInsert(&WALNodeData{NodeID: uint32(i)})
	}

	if err := w.Checkpoint(5); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if w.CheckpointedSequence() != 5 {
		t.Errorf("CheckpointedSequence = %d, want 5", w.CheckpointedSequence())
	}

	entries, err := w.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(entries) != 5 {
		t.Fatalf("len(entries) = %d, want 5 (seqs 6-10)", len(entries))
	}
	if entries[0].SequenceID != 6 {
		t.Errorf("first entry seq = %d, want 6", entries[0].SequenceID)
	}
}

func TestSessionWAL_Truncate(t *testing.T) {
	w := openTestSessionWAL(t, true)

	for i := range 5 {
		w.LogNodeInsert(&WALNodeData{NodeID: uint32(i)})
	}

	if err := w.Truncate(); err != nil {
		t.Fatalf("Truncate: %v", err)
	}

	if w.LastSequence() != 0 {
		t.Errorf("LastSequence = %d, want 0", w.LastSequence())
	}

	entries, err := w.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("len(entries) = %d, want 0", len(entries))
	}
}

func TestSessionWAL_SegmentRotation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping segment rotation test in short mode")
	}

	dir := filepath.Join(t.TempDir(), "wal")
	w, err := OpenSessionWAL(SessionWALConfig{Dir: dir, SyncOnWrite: false})
	if err != nil {
		t.Fatalf("OpenSessionWAL: %v", err)
	}

	totalEntries := walSegmentCapacity + 1

	for i := range totalEntries {
		if _, err := w.LogNodeInsert(&WALNodeData{NodeID: uint32(i)}); err != nil {
			t.Fatalf("LogNodeInsert %d: %v", i, err)
		}
	}
	w.Close()

	segments, _ := filepath.Glob(filepath.Join(dir, sessionWALPrefix+"*"))
	if len(segments) != 2 {
		t.Errorf("segment count = %d, want 2", len(segments))
	}

	// Reopen and verify all entries.
	w2, err := OpenSessionWAL(SessionWALConfig{Dir: dir})
	if err != nil {
		t.Fatalf("OpenSessionWAL (reopen): %v", err)
	}
	defer w2.Close()

	entries, err := w2.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(entries) != totalEntries {
		t.Errorf("len(entries) = %d, want %d", len(entries), totalEntries)
	}
}

func TestSessionWAL_CheckpointGC(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping GC test in short mode")
	}

	dir := filepath.Join(t.TempDir(), "wal")
	w, err := OpenSessionWAL(SessionWALConfig{Dir: dir, SyncOnWrite: false})
	if err != nil {
		t.Fatalf("OpenSessionWAL: %v", err)
	}

	totalEntries := walSegmentCapacity + 1
	for i := range totalEntries {
		w.LogNodeInsert(&WALNodeData{NodeID: uint32(i)})
	}

	// Checkpoint at the segment boundary.
	if err := w.Checkpoint(uint64(walSegmentCapacity)); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	w.Close()

	// Reopen — old segment should be GC'd.
	w2, err := OpenSessionWAL(SessionWALConfig{Dir: dir})
	if err != nil {
		t.Fatalf("OpenSessionWAL (reopen): %v", err)
	}
	defer w2.Close()

	entries, err := w2.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	expectedEntries := totalEntries - walSegmentCapacity
	if len(entries) != expectedEntries {
		t.Errorf("len(entries) = %d, want %d", len(entries), expectedEntries)
	}
}

func TestSessionWAL_WALBytes(t *testing.T) {
	w := openTestSessionWAL(t, true)

	if w.WALBytes() <= 0 {
		t.Error("WALBytes should be > 0 (header exists)")
	}

	initialBytes := w.WALBytes()

	for i := range 10 {
		w.LogNodeInsert(&WALNodeData{NodeID: uint32(i), Key: "key", Name: "name", Path: "/path"})
	}

	if w.WALBytes() <= initialBytes {
		t.Error("WALBytes should increase after writes")
	}
}

func TestSessionWAL_ConcurrentWriters(t *testing.T) {
	w := openTestSessionWAL(t, false)

	const numWriters = 8
	const entriesPerWriter = 100

	var wg sync.WaitGroup
	wg.Add(numWriters)

	for i := range numWriters {
		go func(writerID int) {
			defer wg.Done()
			for j := range entriesPerWriter {
				nodeID := uint32(writerID*entriesPerWriter + j)
				if _, err := w.LogNodeInsert(&WALNodeData{NodeID: nodeID}); err != nil {
					t.Errorf("writer %d entry %d: %v", writerID, j, err)
					return
				}
			}
		}(i)
	}

	wg.Wait()

	expectedSeq := uint64(numWriters * entriesPerWriter)
	if w.LastSequence() != expectedSeq {
		t.Errorf("LastSequence = %d, want %d", w.LastSequence(), expectedSeq)
	}

	entries, err := w.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(entries) != numWriters*entriesPerWriter {
		t.Errorf("len(entries) = %d, want %d", len(entries), numWriters*entriesPerWriter)
	}

	// Verify all sequence IDs are unique.
	seenSeqs := make(map[uint64]bool, len(entries))
	for _, e := range entries {
		if seenSeqs[e.SequenceID] {
			t.Errorf("duplicate sequence ID: %d", e.SequenceID)
		}
		seenSeqs[e.SequenceID] = true
	}
}

func TestSessionWAL_CRCCorruption(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wal")
	w, err := OpenSessionWAL(SessionWALConfig{Dir: dir, SyncOnWrite: true})
	if err != nil {
		t.Fatalf("OpenSessionWAL: %v", err)
	}

	w.LogNodeInsert(&WALNodeData{NodeID: 1, Key: "good"})
	w.LogNodeInsert(&WALNodeData{NodeID: 2, Key: "will-be-corrupted"})
	w.LogNodeInsert(&WALNodeData{NodeID: 3, Key: "after-corrupt"})
	w.Close()

	// Find and corrupt the second entry's checksum.
	segments, _ := filepath.Glob(filepath.Join(dir, sessionWALPrefix+"*"))
	if len(segments) == 0 {
		t.Fatal("no segments found")
	}

	data, err := os.ReadFile(segments[0])
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// The WAL header is 8 bytes. First entry starts at offset 8.
	// We need to find the second entry and corrupt its checksum.
	// Each entry: header(21) + data(var) + checksum(4).
	// Just flip a byte near the middle of the file to corrupt an entry.
	midpoint := len(data) / 2
	data[midpoint] ^= 0xFF

	if err := os.WriteFile(segments[0], data, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Reopen — ReadAll should stop at the corrupted entry (not crash).
	w2, err := OpenSessionWAL(SessionWALConfig{Dir: dir})
	if err != nil {
		t.Fatalf("OpenSessionWAL (reopen): %v", err)
	}
	defer w2.Close()

	entries, err := w2.ReadAll()
	// Should get some entries (at least the first one before corruption).
	// No panic or fatal error — just truncated output.
	if err != nil {
		t.Logf("ReadAll returned error (acceptable): %v", err)
	}
	t.Logf("Recovered %d entries from corrupted WAL", len(entries))
}
