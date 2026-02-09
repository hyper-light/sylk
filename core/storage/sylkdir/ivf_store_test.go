package sylkdir

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIVFStoreInit(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewIVFStore(sd)
	if err := store.Init(); err != nil {
		t.Fatalf("IVFStore init failed: %v", err)
	}

	// Verify directories were created
	expectedDirs := []string{
		store.VectorsSubpath(),
		store.GraphSubpath(),
		store.NormsSubpath(),
		store.BBQSubpath(),
	}

	for _, dir := range expectedDirs {
		info, err := os.Stat(dir)
		if err != nil {
			t.Errorf("Expected directory %s to exist: %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("Expected %s to be a directory", dir)
		}
	}
}

func TestIVFStorePaths(t *testing.T) {
	tmpDir := "/project/root"
	sd := New(tmpDir)
	store := NewIVFStore(sd)

	tests := []struct {
		name     string
		got      string
		expected string
	}{
		{"Path", store.Path(), "/project/root/.sylk/data/ivf"},
		{"VectorsSubpath", store.VectorsSubpath(), "/project/root/.sylk/data/ivf/vectors"},
		{"GraphSubpath", store.GraphSubpath(), "/project/root/.sylk/data/ivf/graph"},
		{"NormsSubpath", store.NormsSubpath(), "/project/root/.sylk/data/ivf/norms"},
		{"BBQSubpath", store.BBQSubpath(), "/project/root/.sylk/data/ivf/bbq"},
		{"CentroidsPath", store.CentroidsPath(), "/project/root/.sylk/data/ivf/centroids.bin"},
		{"PartitionsPath", store.PartitionsPath(), "/project/root/.sylk/data/ivf/partitions.bin"},
		{"MetadataPath", store.MetadataPath(), "/project/root/.sylk/data/ivf/metadata.yaml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("got %s, expected %s", tt.got, tt.expected)
			}
		})
	}
}

func TestIVFStoreExistsEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewIVFStore(sd)

	// Should not exist before any data is saved
	if store.Exists() {
		t.Error("Exists should return false for empty store")
	}
}

func TestIVFStoreExistsWithMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewIVFStore(sd)
	if err := store.Init(); err != nil {
		t.Fatalf("IVFStore init failed: %v", err)
	}

	// Create fake metadata file
	metaPath := store.MetadataPath()
	if err := os.WriteFile(metaPath, []byte("version: 1"), 0644); err != nil {
		t.Fatalf("Failed to create metadata: %v", err)
	}

	// Should exist now
	if !store.Exists() {
		t.Error("Exists should return true when metadata exists")
	}
}

func TestIVFStoreDelete(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewIVFStore(sd)
	if err := store.Init(); err != nil {
		t.Fatalf("IVFStore init failed: %v", err)
	}

	// Create some files
	os.WriteFile(store.MetadataPath(), []byte("test"), 0644)
	os.WriteFile(store.CentroidsPath(), []byte("test"), 0644)
	os.WriteFile(filepath.Join(store.VectorsSubpath(), "test.bin"), []byte("test"), 0644)

	// Delete
	if err := store.Delete(); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify contents are deleted
	entries, _ := os.ReadDir(store.Path())
	if len(entries) > 0 {
		t.Errorf("Delete should remove all contents, found %d entries", len(entries))
	}
}

func TestIVFStoreStatsEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewIVFStore(sd)
	stats := store.Stats()

	if stats.Exists {
		t.Error("Stats.Exists should be false for empty store")
	}
	if stats.MetadataOK {
		t.Error("Stats.MetadataOK should be false")
	}
}

func TestIVFStoreStatsWithDirs(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewIVFStore(sd)
	if err := store.Init(); err != nil {
		t.Fatalf("IVFStore init failed: %v", err)
	}

	stats := store.Stats()

	// Directories should exist
	if !stats.VectorsOK {
		t.Error("Stats.VectorsOK should be true after init")
	}
	if !stats.GraphOK {
		t.Error("Stats.GraphOK should be true after init")
	}
	if !stats.NormsOK {
		t.Error("Stats.NormsOK should be true after init")
	}
	if !stats.BBQOK {
		t.Error("Stats.BBQOK should be true after init")
	}

	// Metadata doesn't exist yet
	if stats.MetadataOK {
		t.Error("Stats.MetadataOK should be false before save")
	}
}

func TestIVFStoreDeleteNonexistent(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	// Don't init SylkDir - vectors path doesn't exist

	store := NewIVFStore(sd)

	// Delete should not error for nonexistent path
	if err := store.Delete(); err != nil {
		t.Errorf("Delete should not error for nonexistent path: %v", err)
	}
}

func TestIVFStoreInitIdempotent(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("SylkDir init failed: %v", err)
	}

	store := NewIVFStore(sd)

	// Init multiple times should not error
	if err := store.Init(); err != nil {
		t.Fatalf("First init failed: %v", err)
	}
	if err := store.Init(); err != nil {
		t.Fatalf("Second init failed: %v", err)
	}

	// Create a file
	testFile := filepath.Join(store.VectorsSubpath(), "test.bin")
	os.WriteFile(testFile, []byte("test"), 0644)

	// Init again should not delete existing files
	if err := store.Init(); err != nil {
		t.Fatalf("Third init failed: %v", err)
	}

	if _, err := os.Stat(testFile); err != nil {
		t.Error("Init should not delete existing files")
	}
}
