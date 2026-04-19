package context

import (
	"sync"
	"testing"
)

// TestTieredSearcher_RegisterResultHook_FiresOnHotHit verifies the core
// SCORE-07 invariant: a hook registered on the searcher fires when the
// hot cache returns results, receiving the tier and batch it contributed.
func TestTieredSearcher_RegisterResultHook_FiresOnHotHit(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{MaxSize: 1 << 20})
	cache.Add("hit-1", &ContentEntry{ID: "hit-1", Content: "relevant content for query"})

	ts := NewTieredSearcher(TieredSearcherConfig{
		HotCache:     cache,
		DefaultLimit: 5,
	})

	type observed struct {
		query string
		tier  SearchTier
		size  int
	}
	var mu sync.Mutex
	var seen []observed
	ts.RegisterResultHook(SearchResultHookFunc(func(q string, tier SearchTier, batch []*ContentEntry) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, observed{query: q, tier: tier, size: len(batch)})
	}))

	result := ts.SearchWithBudget(nil, "hit-1", TierHotBudget)
	if len(result.Results) == 0 {
		t.Fatal("precondition failed: hot cache did not return the seeded entry")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 {
		t.Fatalf("hook fired %d times, want 1 (hot tier hit)", len(seen))
	}
	if seen[0].tier != TierHotCache {
		t.Errorf("fired tier = %v, want TierHotCache", seen[0].tier)
	}
	if seen[0].query != "hit-1" {
		t.Errorf("fired query = %q, want hit-1", seen[0].query)
	}
	if seen[0].size == 0 {
		t.Error("fired batch size = 0, want ≥1")
	}
}

// TestTieredSearcher_MultipleHooksAllFire verifies every registered hook
// receives every batch — no first-registered-wins or short-circuit
// semantics.
func TestTieredSearcher_MultipleHooksAllFire(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{MaxSize: 1 << 20})
	cache.Add("key", &ContentEntry{ID: "key", Content: "value"})

	ts := NewTieredSearcher(TieredSearcherConfig{HotCache: cache})

	var countA, countB int
	var mu sync.Mutex
	ts.RegisterResultHook(SearchResultHookFunc(func(string, SearchTier, []*ContentEntry) {
		mu.Lock()
		defer mu.Unlock()
		countA++
	}))
	ts.RegisterResultHook(SearchResultHookFunc(func(string, SearchTier, []*ContentEntry) {
		mu.Lock()
		defer mu.Unlock()
		countB++
	}))

	_ = ts.SearchWithBudget(nil, "key", TierHotBudget)

	mu.Lock()
	defer mu.Unlock()
	if countA != 1 || countB != 1 {
		t.Errorf("hook fire counts: A=%d B=%d, want both 1", countA, countB)
	}
}

// TestTieredSearcher_NilHookIgnored verifies registering a nil hook is a
// no-op and does not panic on subsequent searches.
func TestTieredSearcher_NilHookIgnored(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{MaxSize: 1 << 20})
	cache.Add("key", &ContentEntry{ID: "key", Content: "value"})

	ts := NewTieredSearcher(TieredSearcherConfig{HotCache: cache})
	ts.RegisterResultHook(nil)

	_ = ts.SearchWithBudget(nil, "key", TierHotBudget)
	// No assertion needed — the test passes if no panic occurred.
}

// TestTieredSearcher_ClearResultHooksStopsFiring verifies ClearResultHooks
// actually drops the registry so subsequent searches produce no hook
// invocations.
func TestTieredSearcher_ClearResultHooksStopsFiring(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{MaxSize: 1 << 20})
	cache.Add("key", &ContentEntry{ID: "key", Content: "value"})

	ts := NewTieredSearcher(TieredSearcherConfig{HotCache: cache})

	var fires int
	var mu sync.Mutex
	ts.RegisterResultHook(SearchResultHookFunc(func(string, SearchTier, []*ContentEntry) {
		mu.Lock()
		defer mu.Unlock()
		fires++
	}))

	_ = ts.SearchWithBudget(nil, "key", TierHotBudget)
	mu.Lock()
	preClear := fires
	mu.Unlock()
	if preClear != 1 {
		t.Fatalf("pre-clear fires = %d, want 1", preClear)
	}

	ts.ClearResultHooks()
	_ = ts.SearchWithBudget(nil, "key", TierHotBudget)

	mu.Lock()
	defer mu.Unlock()
	if fires != 1 {
		t.Errorf("post-clear fires = %d, want 1 (second search should not fire)", fires)
	}
}

// TestTieredSearcher_NoHookWhenNoResults verifies hooks do not fire for
// empty result batches — a tier that had nothing to contribute should
// not invoke the hook with an empty slice.
func TestTieredSearcher_NoHookWhenNoResults(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{MaxSize: 1 << 20})
	ts := NewTieredSearcher(TieredSearcherConfig{HotCache: cache})

	var fires int
	var mu sync.Mutex
	ts.RegisterResultHook(SearchResultHookFunc(func(string, SearchTier, []*ContentEntry) {
		mu.Lock()
		defer mu.Unlock()
		fires++
	}))

	// Cache is empty; hot search returns nothing.
	_ = ts.SearchWithBudget(nil, "nothing-here", TierHotBudget)

	mu.Lock()
	defer mu.Unlock()
	if fires != 0 {
		t.Errorf("fires on empty result = %d, want 0", fires)
	}
}
