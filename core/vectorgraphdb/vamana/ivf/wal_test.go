package ivf

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/wal"
)

func TestWAL_WriteAndRead(t *testing.T) {
	dir := t.TempDir()

	w, err := OpenWAL(WALConfig{Dir: dir, SyncOnWrite: true})
	if err != nil {
		t.Fatalf("OpenWAL: %v", err)
	}

	vec := []float32{1.0, 2.0, 3.0, 4.0}
	seq, err := w.LogInsert(0, vec)
	if err != nil {
		t.Fatalf("LogInsert: %v", err)
	}
	if seq != 1 {
		t.Errorf("expected seq 1, got %d", seq)
	}

	seq, err = w.LogInsert(1, []float32{5.0, 6.0, 7.0, 8.0})
	if err != nil {
		t.Fatalf("LogInsert: %v", err)
	}
	if seq != 2 {
		t.Errorf("expected seq 2, got %d", seq)
	}

	entries, err := w.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	if entries[0].OpType != wal.OpInsert {
		t.Errorf("expected OpInsert, got %v", entries[0].OpType)
	}

	vectorID, vector, err := DecodeInsertData(entries[0].Data)
	if err != nil {
		t.Fatalf("DecodeInsertData: %v", err)
	}
	if vectorID != 0 {
		t.Errorf("expected vectorID 0, got %d", vectorID)
	}
	if len(vector) != 4 {
		t.Errorf("expected 4 floats, got %d", len(vector))
	}
	for i, v := range vec {
		if vector[i] != v {
			t.Errorf("vector[%d]: expected %f, got %f", i, v, vector[i])
		}
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestWAL_Recovery(t *testing.T) {
	dir := t.TempDir()

	w, err := OpenWAL(WALConfig{Dir: dir, SyncOnWrite: true})
	if err != nil {
		t.Fatalf("OpenWAL: %v", err)
	}

	for i := range 5 {
		vec := make([]float32, 4)
		for j := range vec {
			vec[j] = float32(i*4 + j)
		}
		if _, err := w.LogInsert(uint32(i), vec); err != nil {
			t.Fatalf("LogInsert %d: %v", i, err)
		}
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	w2, err := OpenWAL(WALConfig{Dir: dir})
	if err != nil {
		t.Fatalf("OpenWAL (reopen): %v", err)
	}
	defer w2.Close()

	if w2.LastSequence() != 5 {
		t.Errorf("expected last seq 5, got %d", w2.LastSequence())
	}

	entries, err := w2.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}
}

func TestWAL_Truncate(t *testing.T) {
	dir := t.TempDir()

	w, err := OpenWAL(WALConfig{Dir: dir, SyncOnWrite: true})
	if err != nil {
		t.Fatalf("OpenWAL: %v", err)
	}
	defer w.Close()

	for i := range 3 {
		if _, err := w.LogInsert(uint32(i), []float32{float32(i)}); err != nil {
			t.Fatalf("LogInsert: %v", err)
		}
	}

	if err := w.Truncate(); err != nil {
		t.Fatalf("Truncate: %v", err)
	}

	if w.LastSequence() != 0 {
		t.Errorf("expected last seq 0 after truncate, got %d", w.LastSequence())
	}

	entries, err := w.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries after truncate, got %d", len(entries))
	}
}

func TestWAL_Checkpoint(t *testing.T) {
	dir := t.TempDir()

	w, err := OpenWAL(WALConfig{Dir: dir, SyncOnWrite: true})
	if err != nil {
		t.Fatalf("OpenWAL: %v", err)
	}

	for i := range 10 {
		if _, err := w.LogInsert(uint32(i), []float32{float32(i)}); err != nil {
			t.Fatalf("LogInsert: %v", err)
		}
	}

	if err := w.Checkpoint(5); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	if w.CheckpointedSequence() != 5 {
		t.Errorf("expected checkpointed seq 5, got %d", w.CheckpointedSequence())
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	w2, err := OpenWAL(WALConfig{Dir: dir})
	if err != nil {
		t.Fatalf("OpenWAL (reopen): %v", err)
	}
	defer w2.Close()

	entries, err := w2.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if len(entries) != 5 {
		t.Errorf("expected 5 entries after checkpoint, got %d", len(entries))
	}

	for i, e := range entries {
		expectedSeq := uint64(6 + i)
		if e.SequenceID != expectedSeq {
			t.Errorf("entry %d: expected seq %d, got %d", i, expectedSeq, e.SequenceID)
		}
	}
}

func TestWAL_SegmentRotation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping segment rotation test in short mode")
	}

	dir := t.TempDir()

	w, err := OpenWAL(WALConfig{Dir: dir, SyncOnWrite: false})
	if err != nil {
		t.Fatalf("OpenWAL: %v", err)
	}

	totalEntries := storage.VectorsPerShard + 1

	for i := range totalEntries {
		if _, err := w.LogInsert(uint32(i), []float32{float32(i)}); err != nil {
			t.Fatalf("LogInsert %d: %v", i, err)
		}
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	segments, err := filepath.Glob(filepath.Join(dir, walFilePrefix+"*"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}

	if len(segments) != 2 {
		t.Errorf("expected 2 segments, got %d", len(segments))
	}

	w2, err := OpenWAL(WALConfig{Dir: dir})
	if err != nil {
		t.Fatalf("OpenWAL (reopen): %v", err)
	}
	defer w2.Close()

	entries, err := w2.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if len(entries) != totalEntries {
		t.Errorf("expected %d entries, got %d", totalEntries, len(entries))
	}
}

func TestWAL_GarbageCollection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping GC test in short mode")
	}

	dir := t.TempDir()

	w, err := OpenWAL(WALConfig{Dir: dir, SyncOnWrite: false})
	if err != nil {
		t.Fatalf("OpenWAL: %v", err)
	}

	totalEntries := storage.VectorsPerShard + 1

	for i := range totalEntries {
		if _, err := w.LogInsert(uint32(i), []float32{float32(i)}); err != nil {
			t.Fatalf("LogInsert %d: %v", i, err)
		}
	}

	segments, _ := filepath.Glob(filepath.Join(dir, walFilePrefix+"*"))
	if len(segments) < 2 {
		t.Fatalf("expected at least 2 segments, got %d", len(segments))
	}

	checkpointSeq := uint64(storage.VectorsPerShard)
	if err := w.Checkpoint(checkpointSeq); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	w2, err := OpenWAL(WALConfig{Dir: dir})
	if err != nil {
		t.Fatalf("OpenWAL (reopen): %v", err)
	}
	defer w2.Close()

	segments, _ = filepath.Glob(filepath.Join(dir, walFilePrefix+"*"))
	if len(segments) != 2 {
		t.Errorf("expected 2 segments after GC (1 with data + 1 new for writes), got %d", len(segments))
	}

	entries, err := w2.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	expectedEntries := totalEntries - int(checkpointSeq)
	if len(entries) != expectedEntries {
		t.Errorf("expected %d entries after GC, got %d", expectedEntries, len(entries))
	}
}

func TestIndex_WALIntegration(t *testing.T) {
	dir := t.TempDir()
	dim := 8
	numVecs := 100

	vectors := make([][]float32, numVecs)
	for i := range vectors {
		vectors[i] = make([]float32, dim)
		for j := range dim {
			vectors[i][j] = float32(i*dim + j)
		}
	}

	cfg := Config{
		NumPartitions: 4,
		NProbe:        2,
	}
	idx := NewIndex(cfg, dim)
	idx.Build(vectors)

	w, err := OpenWAL(WALConfig{Dir: dir, SyncOnWrite: true})
	if err != nil {
		t.Fatalf("OpenWAL: %v", err)
	}
	idx.SetWAL(w)

	insertVecs := make([][]float32, 10)
	for i := range insertVecs {
		insertVecs[i] = make([]float32, dim)
		for j := range dim {
			insertVecs[i][j] = float32((numVecs+i)*dim + j)
		}
	}

	for _, vec := range insertVecs {
		if _, err := idx.Insert(vec); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	if idx.NumVectors() != numVecs+10 {
		t.Errorf("expected %d vectors, got %d", numVecs+10, idx.NumVectors())
	}

	entries, err := w.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(entries) != 10 {
		t.Errorf("expected 10 WAL entries, got %d", len(entries))
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestIndex_WALReplay(t *testing.T) {
	dir := t.TempDir()
	dim := 8
	numVecs := 50

	vectors := make([][]float32, numVecs)
	for i := range vectors {
		vectors[i] = make([]float32, dim)
		for j := range dim {
			vectors[i][j] = float32(i*dim + j)
		}
	}

	cfg := Config{
		NumPartitions: 4,
		NProbe:        2,
	}
	idx := NewIndex(cfg, dim)
	idx.Build(vectors)

	w, err := OpenWAL(WALConfig{Dir: dir, SyncOnWrite: true})
	if err != nil {
		t.Fatalf("OpenWAL: %v", err)
	}
	idx.SetWAL(w)

	insertVecs := make([][]float32, 5)
	for i := range insertVecs {
		insertVecs[i] = make([]float32, dim)
		for j := range dim {
			insertVecs[i][j] = float32((numVecs+i)*dim + j)
		}
	}

	for _, vec := range insertVecs {
		if _, err := idx.Insert(vec); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	idx2 := NewIndex(cfg, dim)
	idx2.Build(vectors)

	w2, err := OpenWAL(WALConfig{Dir: dir})
	if err != nil {
		t.Fatalf("OpenWAL (reopen): %v", err)
	}
	idx2.SetWAL(w2)
	defer w2.Close()

	stats, err := idx2.ReplayWAL()
	if err != nil {
		t.Fatalf("ReplayWAL: %v", err)
	}

	if stats.EntriesRead != 5 {
		t.Errorf("expected 5 entries read, got %d", stats.EntriesRead)
	}
	if stats.InsertsApplied != 5 {
		t.Errorf("expected 5 inserts applied, got %d", stats.InsertsApplied)
	}
	if idx2.NumVectors() != numVecs+5 {
		t.Errorf("expected %d vectors after replay, got %d", numVecs+5, idx2.NumVectors())
	}

	for i, original := range insertVecs {
		replayedIdx := numVecs + i
		replayed := idx2.getVector(uint32(replayedIdx))
		for j := range dim {
			if original[j] != replayed[j] {
				t.Errorf("vector %d dim %d: expected %f, got %f", i, j, original[j], replayed[j])
			}
		}
	}
}

func TestIndex_WALReplaySkipsDuplicates(t *testing.T) {
	dir := t.TempDir()
	dim := 4
	numVecs := 10

	vectors := make([][]float32, numVecs)
	for i := range vectors {
		vectors[i] = make([]float32, dim)
		for j := range dim {
			vectors[i][j] = float32(i*dim + j)
		}
	}

	cfg := Config{
		NumPartitions: 2,
		NProbe:        1,
	}
	idx := NewIndex(cfg, dim)
	idx.Build(vectors)

	w, err := OpenWAL(WALConfig{Dir: dir, SyncOnWrite: true})
	if err != nil {
		t.Fatalf("OpenWAL: %v", err)
	}
	idx.SetWAL(w)

	newVec := make([]float32, dim)
	for j := range dim {
		newVec[j] = float32(100 + j)
	}
	if _, err := idx.Insert(newVec); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	stats, err := idx.ReplayWAL()
	if err != nil {
		t.Fatalf("ReplayWAL: %v", err)
	}

	if stats.InsertsApplied != 0 {
		t.Errorf("expected 0 inserts applied (all duplicates), got %d", stats.InsertsApplied)
	}
	if idx.NumVectors() != numVecs+1 {
		t.Errorf("expected %d vectors (unchanged), got %d", numVecs+1, idx.NumVectors())
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestIndex_SaveCheckpointsWAL(t *testing.T) {
	walDir := t.TempDir()
	saveDir := t.TempDir()
	dim := 8
	numVecs := 50

	vectors := make([][]float32, numVecs)
	for i := range vectors {
		vectors[i] = make([]float32, dim)
		for j := range dim {
			vectors[i][j] = float32(i*dim + j)
		}
	}

	cfg := Config{
		NumPartitions: 4,
		NProbe:        2,
	}
	idx := NewIndex(cfg, dim)
	idx.Build(vectors)

	w, err := OpenWAL(WALConfig{Dir: walDir, SyncOnWrite: true})
	if err != nil {
		t.Fatalf("OpenWAL: %v", err)
	}
	idx.SetWAL(w)

	for i := range 10 {
		vec := make([]float32, dim)
		for j := range dim {
			vec[j] = float32((numVecs+i)*dim + j)
		}
		if _, err := idx.Insert(vec); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	entriesBefore, _ := w.ReadAll()
	if len(entriesBefore) != 10 {
		t.Errorf("expected 10 entries before save, got %d", len(entriesBefore))
	}

	if err := idx.Save(saveDir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	entriesAfter, err := w.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll after save: %v", err)
	}
	if len(entriesAfter) != 0 {
		t.Errorf("expected 0 entries after save (all checkpointed), got %d", len(entriesAfter))
	}

	if w.CheckpointedSequence() != 10 {
		t.Errorf("expected checkpointed seq 10, got %d", w.CheckpointedSequence())
	}

	checkpointFile := filepath.Join(walDir, walCheckpointFile)
	if _, err := os.Stat(checkpointFile); os.IsNotExist(err) {
		t.Error("checkpoint file should exist after save")
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
