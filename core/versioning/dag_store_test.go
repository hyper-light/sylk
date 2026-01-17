package versioning

import (
	"sync"
	"testing"
)

func TestNewMemoryDAGStore(t *testing.T) {
	store := NewMemoryDAGStore()
	if store == nil {
		t.Fatal("expected non-nil store")
	}
	if store.Count() != 0 {
		t.Errorf("expected 0 versions, got %d", store.Count())
	}
}

func TestMemoryDAGStore_Add(t *testing.T) {
	t.Run("adds root version", func(t *testing.T) {
		store := NewMemoryDAGStore()
		version := createTestVersion("test.go", nil)

		err := store.Add(version)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if store.Count() != 1 {
			t.Errorf("expected 1 version, got %d", store.Count())
		}
	})

	t.Run("adds child version with valid parent", func(t *testing.T) {
		store := NewMemoryDAGStore()
		parent := createTestVersion("test.go", nil)
		store.Add(parent)

		child := createTestVersion("test.go", []VersionID{parent.ID})
		err := store.Add(child)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("rejects duplicate version", func(t *testing.T) {
		store := NewMemoryDAGStore()
		version := createTestVersion("test.go", nil)
		store.Add(version)

		err := store.Add(version)
		if err != ErrVersionExists {
			t.Errorf("expected ErrVersionExists, got %v", err)
		}
	})

	t.Run("rejects invalid parent", func(t *testing.T) {
		store := NewMemoryDAGStore()
		invalidParent := ComputeVersionID([]byte("nonexistent"), nil)
		version := createTestVersion("test.go", []VersionID{invalidParent})

		err := store.Add(version)
		if err != ErrInvalidParent {
			t.Errorf("expected ErrInvalidParent, got %v", err)
		}
	})

	t.Run("clones version", func(t *testing.T) {
		store := NewMemoryDAGStore()
		version := createTestVersion("test.go", nil)
		store.Add(version)

		version.FilePath = "modified.go"

		retrieved, _ := store.Get(version.ID)
		if retrieved.FilePath == "modified.go" {
			t.Error("version should be cloned")
		}
	})

	t.Run("updates head", func(t *testing.T) {
		store := NewMemoryDAGStore()
		v1 := createTestVersion("test.go", nil)
		store.Add(v1)

		v2 := createTestVersion("test.go", []VersionID{v1.ID})
		store.Add(v2)

		head, _ := store.GetHead("test.go")
		if head.ID != v2.ID {
			t.Error("head should be updated to latest version")
		}
	})
}

func TestMemoryDAGStore_Get(t *testing.T) {
	t.Run("retrieves existing version", func(t *testing.T) {
		store := NewMemoryDAGStore()
		version := createTestVersion("test.go", nil)
		store.Add(version)

		retrieved, err := store.Get(version.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if retrieved.ID != version.ID {
			t.Error("ID mismatch")
		}
	})

	t.Run("returns error for missing version", func(t *testing.T) {
		store := NewMemoryDAGStore()
		var missingID VersionID

		_, err := store.Get(missingID)
		if err != ErrVersionNotFound {
			t.Errorf("expected ErrVersionNotFound, got %v", err)
		}
	})

	t.Run("returns clone", func(t *testing.T) {
		store := NewMemoryDAGStore()
		version := createTestVersion("test.go", nil)
		store.Add(version)

		retrieved, _ := store.Get(version.ID)
		retrieved.FilePath = "modified.go"

		retrieved2, _ := store.Get(version.ID)
		if retrieved2.FilePath == "modified.go" {
			t.Error("Get should return a clone")
		}
	})
}

func TestMemoryDAGStore_GetHead(t *testing.T) {
	t.Run("returns head version", func(t *testing.T) {
		store := NewMemoryDAGStore()
		v1 := createTestVersion("test.go", nil)
		store.Add(v1)

		v2 := createTestVersion("test.go", []VersionID{v1.ID})
		store.Add(v2)

		head, err := store.GetHead("test.go")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if head.ID != v2.ID {
			t.Error("expected latest version as head")
		}
	})

	t.Run("returns error for missing file", func(t *testing.T) {
		store := NewMemoryDAGStore()

		_, err := store.GetHead("nonexistent.go")
		if err != ErrNoVersionsForFile {
			t.Errorf("expected ErrNoVersionsForFile, got %v", err)
		}
	})
}

func TestMemoryDAGStore_GetHistory(t *testing.T) {
	t.Run("returns versions in reverse order", func(t *testing.T) {
		store := NewMemoryDAGStore()
		v1 := createTestVersion("test.go", nil)
		store.Add(v1)

		v2 := createTestVersion("test.go", []VersionID{v1.ID})
		store.Add(v2)

		v3 := createTestVersion("test.go", []VersionID{v2.ID})
		store.Add(v3)

		history, err := store.GetHistory("test.go", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(history) != 3 {
			t.Errorf("expected 3 versions, got %d", len(history))
		}
		if history[0].ID != v3.ID {
			t.Error("expected newest version first")
		}
	})

	t.Run("respects limit", func(t *testing.T) {
		store := NewMemoryDAGStore()
		v1 := createTestVersion("test.go", nil)
		store.Add(v1)

		v2 := createTestVersion("test.go", []VersionID{v1.ID})
		store.Add(v2)

		v3 := createTestVersion("test.go", []VersionID{v2.ID})
		store.Add(v3)

		history, _ := store.GetHistory("test.go", 2)
		if len(history) != 2 {
			t.Errorf("expected 2 versions, got %d", len(history))
		}
	})

	t.Run("returns nil for missing file", func(t *testing.T) {
		store := NewMemoryDAGStore()

		history, err := store.GetHistory("nonexistent.go", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if history != nil {
			t.Error("expected nil for missing file")
		}
	})
}

func TestMemoryDAGStore_GetChildren(t *testing.T) {
	t.Run("returns child versions", func(t *testing.T) {
		store := NewMemoryDAGStore()
		parent := createTestVersion("test.go", nil)
		store.Add(parent)

		child1 := createTestVersion("test.go", []VersionID{parent.ID})
		child2 := createTestVersion("test.go", []VersionID{parent.ID})
		store.Add(child1)
		store.Add(child2)

		children, err := store.GetChildren(parent.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(children) != 2 {
			t.Errorf("expected 2 children, got %d", len(children))
		}
	})

	t.Run("returns empty for no children", func(t *testing.T) {
		store := NewMemoryDAGStore()
		version := createTestVersion("test.go", nil)
		store.Add(version)

		children, _ := store.GetChildren(version.ID)
		if len(children) != 0 {
			t.Errorf("expected 0 children, got %d", len(children))
		}
	})
}

func TestMemoryDAGStore_GetAncestors(t *testing.T) {
	t.Run("returns ancestor chain", func(t *testing.T) {
		store := NewMemoryDAGStore()
		v1 := createTestVersion("test.go", nil)
		store.Add(v1)

		v2 := createTestVersion("test.go", []VersionID{v1.ID})
		store.Add(v2)

		v3 := createTestVersion("test.go", []VersionID{v2.ID})
		store.Add(v3)

		ancestors, err := store.GetAncestors(v3.ID, -1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ancestors) != 2 {
			t.Errorf("expected 2 ancestors, got %d", len(ancestors))
		}
	})

	t.Run("respects depth limit", func(t *testing.T) {
		store := NewMemoryDAGStore()
		v1 := createTestVersion("test.go", nil)
		store.Add(v1)

		v2 := createTestVersion("test.go", []VersionID{v1.ID})
		store.Add(v2)

		v3 := createTestVersion("test.go", []VersionID{v2.ID})
		store.Add(v3)

		ancestors, _ := store.GetAncestors(v3.ID, 1)
		if len(ancestors) != 1 {
			t.Errorf("expected 1 ancestor, got %d", len(ancestors))
		}
	})

	t.Run("returns error for missing version", func(t *testing.T) {
		store := NewMemoryDAGStore()
		var missingID VersionID

		_, err := store.GetAncestors(missingID, -1)
		if err != ErrVersionNotFound {
			t.Errorf("expected ErrVersionNotFound, got %v", err)
		}
	})

	t.Run("handles merge commits", func(t *testing.T) {
		store := NewMemoryDAGStore()
		v1 := createTestVersion("test.go", nil)
		store.Add(v1)

		v2a := createTestVersion("test.go", []VersionID{v1.ID})
		v2b := createTestVersion("test.go", []VersionID{v1.ID})
		store.Add(v2a)
		store.Add(v2b)

		merge := createTestVersion("test.go", []VersionID{v2a.ID, v2b.ID})
		store.Add(merge)

		ancestors, _ := store.GetAncestors(merge.ID, -1)
		if len(ancestors) != 3 {
			t.Errorf("expected 3 ancestors, got %d", len(ancestors))
		}
	})
}

func TestMemoryDAGStore_Has(t *testing.T) {
	t.Run("returns true for existing", func(t *testing.T) {
		store := NewMemoryDAGStore()
		version := createTestVersion("test.go", nil)
		store.Add(version)

		if !store.Has(version.ID) {
			t.Error("expected Has to return true")
		}
	})

	t.Run("returns false for missing", func(t *testing.T) {
		store := NewMemoryDAGStore()
		var missingID VersionID

		if store.Has(missingID) {
			t.Error("expected Has to return false")
		}
	})
}

func TestMemoryDAGStore_Clear(t *testing.T) {
	store := NewMemoryDAGStore()
	store.Add(createTestVersion("test.go", nil))
	store.Add(createTestVersion("other.go", nil))

	store.Clear()

	if store.Count() != 0 {
		t.Errorf("expected 0 after clear, got %d", store.Count())
	}
}

func TestMemoryDAGStore_Files(t *testing.T) {
	store := NewMemoryDAGStore()
	store.Add(createTestVersion("test.go", nil))
	store.Add(createTestVersion("other.go", nil))

	files := store.Files()
	if len(files) != 2 {
		t.Errorf("expected 2 files, got %d", len(files))
	}
}

func TestMemoryDAGStore_ConcurrentAccess(t *testing.T) {
	store := NewMemoryDAGStore()
	root := createTestVersion("test.go", nil)
	store.Add(root)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			version := createTestVersion("test.go", []VersionID{root.ID})
			store.Add(version)
			store.Get(version.ID)
			store.Has(version.ID)
			store.GetHead("test.go")
			store.Count()
		}()
	}
	wg.Wait()
}

func TestDAGStoreInterface(t *testing.T) {
	var _ DAGStore = (*MemoryDAGStore)(nil)
}

func createTestVersion(filePath string, parents []VersionID) FileVersion {
	return NewFileVersion(
		filePath,
		[]byte("content-"+filePath),
		parents,
		nil,
		"pipeline-1",
		"session-1",
		VectorClock{"session-1": 1},
	)
}
