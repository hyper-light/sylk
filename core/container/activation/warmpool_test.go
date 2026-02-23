package activation

import (
	"testing"

	"github.com/adalundhe/sylk/core/container"
)

func TestWarmPool_PutAndTake(t *testing.T) {
	pool := NewWarmPool(4)
	c := &container.Container{}

	evicted := pool.Put("engineer", c)
	if evicted != nil {
		t.Fatal("no eviction expected for first put")
	}

	if pool.Len() != 1 {
		t.Fatalf("expected len 1, got %d", pool.Len())
	}

	taken := pool.Take("engineer")
	if taken != c {
		t.Fatal("expected same container back")
	}
	if pool.Len() != 0 {
		t.Fatalf("expected len 0 after take, got %d", pool.Len())
	}
}

func TestWarmPool_TakeMissing(t *testing.T) {
	pool := NewWarmPool(4)

	taken := pool.Take("nonexistent")
	if taken != nil {
		t.Fatal("expected nil for missing type")
	}
}

func TestWarmPool_LRUEviction(t *testing.T) {
	pool := NewWarmPool(2)

	c1 := &container.Container{}
	c2 := &container.Container{}
	c3 := &container.Container{}

	pool.Put("a", c1)
	pool.Put("b", c2)

	// Adding c3 should evict c1 (oldest).
	evicted := pool.Put("c", c3)
	if evicted != c1 {
		t.Fatal("expected c1 to be evicted as LRU")
	}
	if pool.Len() != 2 {
		t.Fatalf("expected len 2, got %d", pool.Len())
	}

	// "a" should be gone.
	if pool.Take("a") != nil {
		t.Fatal("a should have been evicted")
	}
	// "b" should still be present.
	if pool.Take("b") != c2 {
		t.Fatal("b should still be present")
	}
}

func TestWarmPool_PutExistingUpdates(t *testing.T) {
	pool := NewWarmPool(4)

	c1 := &container.Container{}
	c2 := &container.Container{}

	pool.Put("engineer", c1)
	evicted := pool.Put("engineer", c2)
	if evicted != c1 {
		t.Fatal("expected old container returned on update")
	}
	if pool.Len() != 1 {
		t.Fatalf("expected len 1, got %d", pool.Len())
	}

	taken := pool.Take("engineer")
	if taken != c2 {
		t.Fatal("expected updated container")
	}
}

func TestWarmPool_Contains(t *testing.T) {
	pool := NewWarmPool(4)
	c := &container.Container{}

	pool.Put("engineer", c)

	if !pool.Contains("engineer") {
		t.Fatal("should contain engineer")
	}
	if pool.Contains("architect") {
		t.Fatal("should not contain architect")
	}
}

func TestWarmPool_LRUOrderRefresh(t *testing.T) {
	pool := NewWarmPool(2)

	c1 := &container.Container{}
	c2 := &container.Container{}
	c3 := &container.Container{}

	pool.Put("a", c1)
	pool.Put("b", c2)

	// Access "a" by re-putting — moves it to back of LRU.
	pool.Put("a", c1)

	// Adding "c" should evict "b" (now the oldest).
	evicted := pool.Put("c", c3)
	if evicted != c2 {
		t.Fatal("expected b to be evicted after a was refreshed")
	}
}
