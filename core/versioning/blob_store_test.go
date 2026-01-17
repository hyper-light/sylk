package versioning

import (
	"sync"
	"testing"
)

func TestNewMemoryBlobStore(t *testing.T) {
	store := NewMemoryBlobStore()
	if store == nil {
		t.Fatal("expected non-nil store")
	}
	if store.Count() != 0 {
		t.Errorf("expected 0 blobs, got %d", store.Count())
	}
	if store.Size() != 0 {
		t.Errorf("expected 0 size, got %d", store.Size())
	}
}

func TestMemoryBlobStore_Put(t *testing.T) {
	t.Run("stores content and returns hash", func(t *testing.T) {
		store := NewMemoryBlobStore()
		content := []byte("test content")

		hash, err := store.Put(content)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expected := ComputeContentHash(content)
		if hash != expected {
			t.Errorf("hash mismatch")
		}
		if store.Count() != 1 {
			t.Errorf("expected 1 blob, got %d", store.Count())
		}
	})

	t.Run("deduplicates identical content", func(t *testing.T) {
		store := NewMemoryBlobStore()
		content := []byte("test content")

		hash1, _ := store.Put(content)
		hash2, _ := store.Put(content)

		if hash1 != hash2 {
			t.Error("expected same hash for same content")
		}
		if store.Count() != 1 {
			t.Errorf("expected 1 blob (deduplicated), got %d", store.Count())
		}
	})

	t.Run("stores different content separately", func(t *testing.T) {
		store := NewMemoryBlobStore()

		store.Put([]byte("content1"))
		store.Put([]byte("content2"))

		if store.Count() != 2 {
			t.Errorf("expected 2 blobs, got %d", store.Count())
		}
	})

	t.Run("clones content", func(t *testing.T) {
		store := NewMemoryBlobStore()
		content := []byte("original")

		hash, _ := store.Put(content)
		content[0] = 'X'

		retrieved, _ := store.Get(hash)
		if retrieved[0] == 'X' {
			t.Error("content should be cloned on put")
		}
	})

	t.Run("tracks size correctly", func(t *testing.T) {
		store := NewMemoryBlobStore()
		store.Put([]byte("12345"))
		store.Put([]byte("abc"))

		if store.Size() != 8 {
			t.Errorf("expected size 8, got %d", store.Size())
		}
	})
}

func TestMemoryBlobStore_Get(t *testing.T) {
	t.Run("retrieves stored content", func(t *testing.T) {
		store := NewMemoryBlobStore()
		content := []byte("test content")
		hash, _ := store.Put(content)

		retrieved, err := store.Get(hash)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(retrieved) != string(content) {
			t.Error("content mismatch")
		}
	})

	t.Run("returns error for missing blob", func(t *testing.T) {
		store := NewMemoryBlobStore()
		var missingHash ContentHash

		_, err := store.Get(missingHash)
		if err != ErrBlobNotFound {
			t.Errorf("expected ErrBlobNotFound, got %v", err)
		}
	})

	t.Run("returns copy of content", func(t *testing.T) {
		store := NewMemoryBlobStore()
		content := []byte("original")
		hash, _ := store.Put(content)

		retrieved, _ := store.Get(hash)
		retrieved[0] = 'X'

		retrieved2, _ := store.Get(hash)
		if retrieved2[0] == 'X' {
			t.Error("Get should return a copy")
		}
	})
}

func TestMemoryBlobStore_Has(t *testing.T) {
	t.Run("returns true for existing blob", func(t *testing.T) {
		store := NewMemoryBlobStore()
		content := []byte("content")
		hash, _ := store.Put(content)

		if !store.Has(hash) {
			t.Error("expected Has to return true")
		}
	})

	t.Run("returns false for missing blob", func(t *testing.T) {
		store := NewMemoryBlobStore()
		var missingHash ContentHash

		if store.Has(missingHash) {
			t.Error("expected Has to return false")
		}
	})
}

func TestMemoryBlobStore_Delete(t *testing.T) {
	t.Run("removes existing blob", func(t *testing.T) {
		store := NewMemoryBlobStore()
		content := []byte("content")
		hash, _ := store.Put(content)

		err := store.Delete(hash)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if store.Has(hash) {
			t.Error("blob should be deleted")
		}
		if store.Count() != 0 {
			t.Errorf("expected 0 blobs, got %d", store.Count())
		}
	})

	t.Run("returns error for missing blob", func(t *testing.T) {
		store := NewMemoryBlobStore()
		var missingHash ContentHash

		err := store.Delete(missingHash)
		if err != ErrBlobNotFound {
			t.Errorf("expected ErrBlobNotFound, got %v", err)
		}
	})

	t.Run("updates size on delete", func(t *testing.T) {
		store := NewMemoryBlobStore()
		content := []byte("12345")
		hash, _ := store.Put(content)

		store.Delete(hash)

		if store.Size() != 0 {
			t.Errorf("expected size 0, got %d", store.Size())
		}
	})
}

func TestMemoryBlobStore_Clear(t *testing.T) {
	store := NewMemoryBlobStore()
	store.Put([]byte("content1"))
	store.Put([]byte("content2"))

	store.Clear()

	if store.Count() != 0 {
		t.Errorf("expected 0 blobs after clear, got %d", store.Count())
	}
	if store.Size() != 0 {
		t.Errorf("expected 0 size after clear, got %d", store.Size())
	}
}

func TestMemoryBlobStore_Hashes(t *testing.T) {
	store := NewMemoryBlobStore()
	h1, _ := store.Put([]byte("content1"))
	h2, _ := store.Put([]byte("content2"))

	hashes := store.Hashes()

	if len(hashes) != 2 {
		t.Errorf("expected 2 hashes, got %d", len(hashes))
	}

	found := make(map[ContentHash]bool)
	for _, h := range hashes {
		found[h] = true
	}

	if !found[h1] || !found[h2] {
		t.Error("missing expected hashes")
	}
}

func TestMemoryBlobStore_ConcurrentAccess(t *testing.T) {
	store := NewMemoryBlobStore()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			content := []byte{byte(n)}
			hash, _ := store.Put(content)
			store.Get(hash)
			store.Has(hash)
			store.Size()
			store.Count()
		}(i)
	}

	wg.Wait()
}

func TestMemoryBlobStore_EmptyContent(t *testing.T) {
	store := NewMemoryBlobStore()
	hash, err := store.Put([]byte{})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	retrieved, err := store.Get(hash)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(retrieved) != 0 {
		t.Error("expected empty content")
	}
}

func TestBlobStoreInterface(t *testing.T) {
	var _ BlobStore = (*MemoryBlobStore)(nil)
}
