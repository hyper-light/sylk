package vectorgraphdb

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// TestItem is a simple test struct for BatchLoader tests.
type TestItem struct {
	ID    string
	Value string
}

func TestNewBatchLoader(t *testing.T) {
	loadFn := func(ids []string) (map[string]*TestItem, error) {
		return make(map[string]*TestItem), nil
	}

	t.Run("default config", func(t *testing.T) {
		loader := NewBatchLoader(loadFn, BatchLoaderConfig{})
		if loader.maxBatchSize != 100 {
			t.Errorf("expected default maxBatchSize 100, got %d", loader.maxBatchSize)
		}
	})

	t.Run("custom config", func(t *testing.T) {
		loader := NewBatchLoader(loadFn, BatchLoaderConfig{MaxBatchSize: 50})
		if loader.maxBatchSize != 50 {
			t.Errorf("expected maxBatchSize 50, got %d", loader.maxBatchSize)
		}
	})

	t.Run("negative batch size defaults to 100", func(t *testing.T) {
		loader := NewBatchLoader(loadFn, BatchLoaderConfig{MaxBatchSize: -1})
		if loader.maxBatchSize != 100 {
			t.Errorf("expected maxBatchSize 100 for negative input, got %d", loader.maxBatchSize)
		}
	})
}

func TestBatchLoader_LoadBatch(t *testing.T) {
	items := map[string]*TestItem{
		"id1": {ID: "id1", Value: "value1"},
		"id2": {ID: "id2", Value: "value2"},
		"id3": {ID: "id3", Value: "value3"},
	}

	loadFn := func(ids []string) (map[string]*TestItem, error) {
		result := make(map[string]*TestItem)
		for _, id := range ids {
			if item, ok := items[id]; ok {
				result[id] = item
			}
		}
		return result, nil
	}

	t.Run("load existing items", func(t *testing.T) {
		loader := NewBatchLoader(loadFn, DefaultBatchLoaderConfig())
		result, err := loader.LoadBatch([]string{"id1", "id2"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result) != 2 {
			t.Errorf("expected 2 items, got %d", len(result))
		}
		if result["id1"].Value != "value1" {
			t.Errorf("expected value1, got %s", result["id1"].Value)
		}
		if result["id2"].Value != "value2" {
			t.Errorf("expected value2, got %s", result["id2"].Value)
		}
	})

	t.Run("load non-existent items", func(t *testing.T) {
		loader := NewBatchLoader(loadFn, DefaultBatchLoaderConfig())
		result, err := loader.LoadBatch([]string{"nonexistent"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result) != 0 {
			t.Errorf("expected 0 items for non-existent, got %d", len(result))
		}
	})

	t.Run("load empty list", func(t *testing.T) {
		loader := NewBatchLoader(loadFn, DefaultBatchLoaderConfig())
		result, err := loader.LoadBatch([]string{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result) != 0 {
			t.Errorf("expected 0 items for empty list, got %d", len(result))
		}
	})

	t.Run("deduplicate IDs", func(t *testing.T) {
		var callCount int
		countingLoadFn := func(ids []string) (map[string]*TestItem, error) {
			callCount++
			result := make(map[string]*TestItem)
			for _, id := range ids {
				if item, ok := items[id]; ok {
					result[id] = item
				}
			}
			return result, nil
		}

		loader := NewBatchLoader(countingLoadFn, DefaultBatchLoaderConfig())
		result, err := loader.LoadBatch([]string{"id1", "id1", "id1", "id2", "id2"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result) != 2 {
			t.Errorf("expected 2 unique items, got %d", len(result))
		}
		if callCount != 1 {
			t.Errorf("expected 1 load call, got %d", callCount)
		}
	})

	t.Run("uses cache for subsequent calls", func(t *testing.T) {
		var callCount int
		countingLoadFn := func(ids []string) (map[string]*TestItem, error) {
			callCount++
			result := make(map[string]*TestItem)
			for _, id := range ids {
				if item, ok := items[id]; ok {
					result[id] = item
				}
			}
			return result, nil
		}

		loader := NewBatchLoader(countingLoadFn, DefaultBatchLoaderConfig())

		// First call loads from source
		_, err := loader.LoadBatch([]string{"id1"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Second call should use cache
		_, err = loader.LoadBatch([]string{"id1"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if callCount != 1 {
			t.Errorf("expected 1 load call (cached), got %d", callCount)
		}
	})
}

func TestBatchLoader_LoadBatch_Batching(t *testing.T) {
	items := make(map[string]*TestItem)
	for i := 0; i < 250; i++ {
		id := "id" + string(rune('A'+i%26)) + string(rune('0'+i/26))
		items[id] = &TestItem{ID: id, Value: "value" + id}
	}

	var batchSizes []int
	loadFn := func(ids []string) (map[string]*TestItem, error) {
		batchSizes = append(batchSizes, len(ids))
		result := make(map[string]*TestItem)
		for _, id := range ids {
			if item, ok := items[id]; ok {
				result[id] = item
			}
		}
		return result, nil
	}

	loader := NewBatchLoader(loadFn, BatchLoaderConfig{MaxBatchSize: 50})

	ids := make([]string, 0, 125)
	for id := range items {
		if len(ids) >= 125 {
			break
		}
		ids = append(ids, id)
	}

	result, err := loader.LoadBatch(ids)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 125 {
		t.Errorf("expected 125 items, got %d", len(result))
	}

	// Should have made 3 batch calls: 50 + 50 + 25
	if len(batchSizes) != 3 {
		t.Errorf("expected 3 batches, got %d", len(batchSizes))
	}
	for i, size := range batchSizes {
		if i < 2 && size != 50 {
			t.Errorf("batch %d: expected size 50, got %d", i, size)
		}
		if i == 2 && size != 25 {
			t.Errorf("batch %d: expected size 25, got %d", i, size)
		}
	}
}

func TestBatchLoader_LoadBatch_Error(t *testing.T) {
	expectedErr := errors.New("load error")
	loadFn := func(ids []string) (map[string]*TestItem, error) {
		return nil, expectedErr
	}

	loader := NewBatchLoader(loadFn, DefaultBatchLoaderConfig())
	_, err := loader.LoadBatch([]string{"id1"})
	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestBatchLoader_Preload(t *testing.T) {
	items := map[string]*TestItem{
		"id1": {ID: "id1", Value: "value1"},
		"id2": {ID: "id2", Value: "value2"},
	}

	var loadedIDs []string
	loadFn := func(ids []string) (map[string]*TestItem, error) {
		loadedIDs = append(loadedIDs, ids...)
		result := make(map[string]*TestItem)
		for _, id := range ids {
			if item, ok := items[id]; ok {
				result[id] = item
			}
		}
		return result, nil
	}

	loader := NewBatchLoader(loadFn, DefaultBatchLoaderConfig())

	// Preload items
	err := loader.Preload([]string{"id1", "id2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Items should be in cache
	item1, ok := loader.Get("id1")
	if !ok {
		t.Error("id1 not found in cache after preload")
	}
	if item1.Value != "value1" {
		t.Errorf("expected value1, got %s", item1.Value)
	}

	// Second preload should not reload
	err = loader.Preload([]string{"id1", "id2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should only have loaded once
	if len(loadedIDs) != 2 {
		t.Errorf("expected 2 loaded IDs, got %d", len(loadedIDs))
	}
}

func TestBatchLoader_Get(t *testing.T) {
	items := map[string]*TestItem{
		"id1": {ID: "id1", Value: "value1"},
	}

	loadFn := func(ids []string) (map[string]*TestItem, error) {
		result := make(map[string]*TestItem)
		for _, id := range ids {
			if item, ok := items[id]; ok {
				result[id] = item
			}
		}
		return result, nil
	}

	loader := NewBatchLoader(loadFn, DefaultBatchLoaderConfig())

	// Get before load should return not found
	_, ok := loader.Get("id1")
	if ok {
		t.Error("expected not found before load")
	}

	// Load and then get
	_, _ = loader.LoadBatch([]string{"id1"})
	item, ok := loader.Get("id1")
	if !ok {
		t.Error("expected item after load")
	}
	if item.Value != "value1" {
		t.Errorf("expected value1, got %s", item.Value)
	}
}

func TestBatchLoader_GetOrLoad(t *testing.T) {
	items := map[string]*TestItem{
		"id1": {ID: "id1", Value: "value1"},
	}

	var loadCount int
	loadFn := func(ids []string) (map[string]*TestItem, error) {
		loadCount++
		result := make(map[string]*TestItem)
		for _, id := range ids {
			if item, ok := items[id]; ok {
				result[id] = item
			}
		}
		return result, nil
	}

	loader := NewBatchLoader(loadFn, DefaultBatchLoaderConfig())

	// First call should load
	item, err := loader.GetOrLoad("id1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item.Value != "value1" {
		t.Errorf("expected value1, got %s", item.Value)
	}

	// Second call should use cache
	item, err = loader.GetOrLoad("id1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item.Value != "value1" {
		t.Errorf("expected value1, got %s", item.Value)
	}

	if loadCount != 1 {
		t.Errorf("expected 1 load, got %d", loadCount)
	}
}

func TestBatchLoader_GetOrLoad_NotFound(t *testing.T) {
	loadFn := func(ids []string) (map[string]*TestItem, error) {
		return make(map[string]*TestItem), nil
	}

	loader := NewBatchLoader(loadFn, DefaultBatchLoaderConfig())

	_, err := loader.GetOrLoad("nonexistent")
	if err != ErrNodeNotFound {
		t.Errorf("expected ErrNodeNotFound, got %v", err)
	}
}

func TestBatchLoader_Clear(t *testing.T) {
	items := map[string]*TestItem{
		"id1": {ID: "id1", Value: "value1"},
	}

	loadFn := func(ids []string) (map[string]*TestItem, error) {
		result := make(map[string]*TestItem)
		for _, id := range ids {
			if item, ok := items[id]; ok {
				result[id] = item
			}
		}
		return result, nil
	}

	loader := NewBatchLoader(loadFn, DefaultBatchLoaderConfig())
	_, _ = loader.LoadBatch([]string{"id1"})

	if loader.Size() != 1 {
		t.Errorf("expected size 1, got %d", loader.Size())
	}

	loader.Clear()

	if loader.Size() != 0 {
		t.Errorf("expected size 0 after clear, got %d", loader.Size())
	}

	_, ok := loader.Get("id1")
	if ok {
		t.Error("expected item not found after clear")
	}
}

func TestBatchLoader_Size(t *testing.T) {
	items := map[string]*TestItem{
		"id1": {ID: "id1", Value: "value1"},
		"id2": {ID: "id2", Value: "value2"},
	}

	loadFn := func(ids []string) (map[string]*TestItem, error) {
		result := make(map[string]*TestItem)
		for _, id := range ids {
			if item, ok := items[id]; ok {
				result[id] = item
			}
		}
		return result, nil
	}

	loader := NewBatchLoader(loadFn, DefaultBatchLoaderConfig())

	if loader.Size() != 0 {
		t.Errorf("expected initial size 0, got %d", loader.Size())
	}

	_, _ = loader.LoadBatch([]string{"id1"})
	if loader.Size() != 1 {
		t.Errorf("expected size 1, got %d", loader.Size())
	}

	_, _ = loader.LoadBatch([]string{"id2"})
	if loader.Size() != 2 {
		t.Errorf("expected size 2, got %d", loader.Size())
	}
}

func TestBatchLoader_CachedIDs(t *testing.T) {
	items := map[string]*TestItem{
		"id1": {ID: "id1", Value: "value1"},
		"id2": {ID: "id2", Value: "value2"},
	}

	loadFn := func(ids []string) (map[string]*TestItem, error) {
		result := make(map[string]*TestItem)
		for _, id := range ids {
			if item, ok := items[id]; ok {
				result[id] = item
			}
		}
		return result, nil
	}

	loader := NewBatchLoader(loadFn, DefaultBatchLoaderConfig())
	_, _ = loader.LoadBatch([]string{"id1", "id2"})

	cachedIDs := loader.CachedIDs()
	if len(cachedIDs) != 2 {
		t.Errorf("expected 2 cached IDs, got %d", len(cachedIDs))
	}

	hasID1, hasID2 := false, false
	for _, id := range cachedIDs {
		if id == "id1" {
			hasID1 = true
		}
		if id == "id2" {
			hasID2 = true
		}
	}
	if !hasID1 || !hasID2 {
		t.Error("expected both id1 and id2 in cached IDs")
	}
}

func TestBatchLoader_Invalidate(t *testing.T) {
	items := map[string]*TestItem{
		"id1": {ID: "id1", Value: "value1"},
		"id2": {ID: "id2", Value: "value2"},
	}

	loadFn := func(ids []string) (map[string]*TestItem, error) {
		result := make(map[string]*TestItem)
		for _, id := range ids {
			if item, ok := items[id]; ok {
				result[id] = item
			}
		}
		return result, nil
	}

	loader := NewBatchLoader(loadFn, DefaultBatchLoaderConfig())
	_, _ = loader.LoadBatch([]string{"id1", "id2"})

	loader.Invalidate("id1")

	_, ok := loader.Get("id1")
	if ok {
		t.Error("expected id1 not found after invalidate")
	}

	_, ok = loader.Get("id2")
	if !ok {
		t.Error("expected id2 still cached after invalidating id1")
	}

	if loader.Size() != 1 {
		t.Errorf("expected size 1 after invalidate, got %d", loader.Size())
	}
}

func TestBatchLoader_Concurrent(t *testing.T) {
	items := make(map[string]*TestItem)
	for i := 0; i < 100; i++ {
		id := string(rune('A'+i%26)) + string(rune('0'+i/26))
		items[id] = &TestItem{ID: id, Value: "value" + id}
	}

	var loadCount atomic.Int64
	loadFn := func(ids []string) (map[string]*TestItem, error) {
		loadCount.Add(1)
		result := make(map[string]*TestItem)
		for _, id := range ids {
			if item, ok := items[id]; ok {
				result[id] = item
			}
		}
		return result, nil
	}

	loader := NewBatchLoader(loadFn, BatchLoaderConfig{MaxBatchSize: 10})

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			ids := make([]string, 0, 20)
			for j := 0; j < 20; j++ {
				idx := (goroutineID*20 + j) % 100
				id := string(rune('A'+idx%26)) + string(rune('0'+idx/26))
				ids = append(ids, id)
			}
			_, err := loader.LoadBatch(ids)
			if err != nil {
				t.Errorf("goroutine %d: unexpected error: %v", goroutineID, err)
			}
		}(i)
	}

	wg.Wait()

	// Should have some items cached (exact number depends on race)
	if loader.Size() == 0 {
		t.Error("expected some items cached")
	}
}

func TestDefaultBatchLoaderConfig(t *testing.T) {
	config := DefaultBatchLoaderConfig()
	if config.MaxBatchSize != 100 {
		t.Errorf("expected default MaxBatchSize 100, got %d", config.MaxBatchSize)
	}
}
