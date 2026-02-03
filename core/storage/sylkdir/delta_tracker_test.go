package sylkdir

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestDeltaTracker_Increment(t *testing.T) {
	dt := NewDeltaTracker("")

	dt.IncrNodes(5)
	dt.IncrEdges(3)
	dt.IncrEdgesModified(2)
	dt.IncrVectors(10)
	dt.IncrDocsBytes(1024)

	snap := dt.Snapshot()
	if snap.NodesCreated != 5 {
		t.Errorf("NodesCreated = %d, want 5", snap.NodesCreated)
	}
	if snap.EdgesCreated != 3 {
		t.Errorf("EdgesCreated = %d, want 3", snap.EdgesCreated)
	}
	if snap.EdgesModified != 2 {
		t.Errorf("EdgesModified = %d, want 2", snap.EdgesModified)
	}
	if snap.VectorsCreated != 10 {
		t.Errorf("VectorsCreated = %d, want 10", snap.VectorsCreated)
	}
	if snap.DocsBytes != 1024 {
		t.Errorf("DocsBytes = %d, want 1024", snap.DocsBytes)
	}
}

func TestDeltaTracker_TotalEntities(t *testing.T) {
	dt := NewDeltaTracker("")
	dt.IncrNodes(10)
	dt.IncrEdges(20)
	dt.IncrVectors(30)
	dt.IncrEdgesModified(5) // Not counted in total entities.

	if total := dt.TotalEntities(); total != 60 {
		t.Errorf("TotalEntities = %d, want 60", total)
	}
}

func TestDeltaTracker_Reset(t *testing.T) {
	dt := NewDeltaTracker("")
	dt.IncrNodes(100)
	dt.IncrEdges(200)
	dt.IncrVectors(50)
	dt.IncrDocsBytes(4096)

	dt.Reset(42)

	snap := dt.Snapshot()
	if snap.NodesCreated != 0 {
		t.Errorf("NodesCreated = %d, want 0", snap.NodesCreated)
	}
	if snap.EdgesCreated != 0 {
		t.Errorf("EdgesCreated = %d, want 0", snap.EdgesCreated)
	}
	if snap.VectorsCreated != 0 {
		t.Errorf("VectorsCreated = %d, want 0", snap.VectorsCreated)
	}
	if snap.DocsBytes != 0 {
		t.Errorf("DocsBytes = %d, want 0", snap.DocsBytes)
	}
	if snap.LastCheckpointVer != 42 {
		t.Errorf("LastCheckpointVer = %d, want 42", snap.LastCheckpointVer)
	}
	if snap.LastCheckpointAt == 0 {
		t.Error("LastCheckpointAt should be set")
	}
}

func TestDeltaTracker_PersistLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "delta.json")

	dt1 := NewDeltaTracker(path)
	dt1.IncrNodes(7)
	dt1.IncrEdges(13)
	dt1.IncrEdgesModified(4)
	dt1.IncrVectors(21)
	dt1.IncrDocsBytes(512)
	dt1.Reset(99)
	dt1.IncrNodes(3) // After reset.

	if err := dt1.Persist(); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	dt2 := NewDeltaTracker(path)
	if err := dt2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	snap := dt2.Snapshot()
	if snap.NodesCreated != 3 {
		t.Errorf("NodesCreated = %d, want 3", snap.NodesCreated)
	}
	if snap.EdgesCreated != 0 {
		t.Errorf("EdgesCreated = %d, want 0 (reset)", snap.EdgesCreated)
	}
	if snap.LastCheckpointVer != 99 {
		t.Errorf("LastCheckpointVer = %d, want 99", snap.LastCheckpointVer)
	}
	if snap.SchemaVersion != deltaSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", snap.SchemaVersion, deltaSchemaVersion)
	}
}

func TestDeltaTracker_LoadMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.json")

	dt := NewDeltaTracker(path)
	if err := dt.Load(); err != nil {
		t.Fatalf("Load missing file should succeed: %v", err)
	}

	// All counters should be zero.
	if dt.TotalEntities() != 0 {
		t.Errorf("TotalEntities = %d, want 0", dt.TotalEntities())
	}
}

func TestDeltaTracker_V1Migration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "delta.json")

	// Write a v1 file (no schema_version field).
	v1Data := `{"nodes_created":42,"edges_created":10}`
	if err := writeTestFile(path, v1Data); err != nil {
		t.Fatalf("write v1: %v", err)
	}

	dt := NewDeltaTracker(path)
	if err := dt.Load(); err != nil {
		t.Fatalf("Load v1: %v", err)
	}

	snap := dt.Snapshot()
	if snap.NodesCreated != 42 {
		t.Errorf("NodesCreated = %d, want 42", snap.NodesCreated)
	}
	if snap.EdgesCreated != 10 {
		t.Errorf("EdgesCreated = %d, want 10", snap.EdgesCreated)
	}
	// SchemaVersion is 0 in the loaded data (v1 had no field),
	// but Snapshot always returns the current schema version.
}

func TestDeltaTracker_ConcurrentIncrement(t *testing.T) {
	dt := NewDeltaTracker("")
	const goroutines = 8
	const increments = 1000

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for range goroutines {
		go func() {
			defer wg.Done()
			for range increments {
				dt.IncrNodes(1)
				dt.IncrEdges(1)
				dt.IncrVectors(1)
			}
		}()
	}

	wg.Wait()

	expected := uint64(goroutines * increments)
	snap := dt.Snapshot()
	if snap.NodesCreated != expected {
		t.Errorf("NodesCreated = %d, want %d", snap.NodesCreated, expected)
	}
	if snap.EdgesCreated != expected {
		t.Errorf("EdgesCreated = %d, want %d", snap.EdgesCreated, expected)
	}
	if snap.VectorsCreated != expected {
		t.Errorf("VectorsCreated = %d, want %d", snap.VectorsCreated, expected)
	}
}

func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}
