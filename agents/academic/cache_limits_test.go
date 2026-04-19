package academic

import (
	"testing"
	"time"
)

func TestAcademicGetCachedPrunesExpiredEntryAndSource(t *testing.T) {
	a, err := New(Config{
		Factory: newTestFactory(t),
		CacheExpiry:        time.Minute,
		ResearchCacheLimit: 4,
		SourceIndexLimit:   4,
	}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	now := time.Now()
	expiredAt := now.Add(-2 * time.Minute)
	a.researchCache["expired"] = &ResearchResult{
		SourcesConsulted: []string{"src-expired"},
		CachedAt:         &expiredAt,
	}
	a.sourceIndex["src-expired"] = &Source{
		ID:        "src-expired",
		UpdatedAt: now.Add(-4 * time.Hour),
	}

	if got := a.getCached("expired"); got != nil {
		t.Fatalf("getCached(expired) = %#v, want nil", got)
	}

	a.mu.RLock()
	_, cacheExists := a.researchCache["expired"]
	_, sourceExists := a.sourceIndex["src-expired"]
	a.mu.RUnlock()
	if cacheExists {
		t.Fatal("expected expired cache entry to be pruned")
	}
	if sourceExists {
		t.Fatal("expected unreferenced expired source to be pruned")
	}
}

func TestAcademicPrunesSourceIndexButKeepsLiveReferencedSources(t *testing.T) {
	a, err := New(Config{
		Factory: newTestFactory(t),
		CacheExpiry:        time.Hour,
		ResearchCacheLimit: 4,
		SourceIndexLimit:   3,
	}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	a.setCached("live", &ResearchResult{
		SourcesConsulted: []string{"src-keep-1", "src-keep-2"},
	})

	now := time.Now().UTC()
	for _, source := range []*Source{
		{ID: "src-keep-1", UpdatedAt: now.Add(-6 * time.Hour)},
		{ID: "src-keep-2", UpdatedAt: now.Add(-5 * time.Hour)},
		{ID: "src-drop-1", UpdatedAt: now.Add(-4 * time.Hour)},
		{ID: "src-drop-2", UpdatedAt: now.Add(-3 * time.Hour)},
		{ID: "src-drop-3", UpdatedAt: now.Add(-2 * time.Hour)},
	} {
		a.upsertResearchSource(source)
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	if got := len(a.sourceIndex); got > a.config.SourceIndexLimit {
		t.Fatalf("sourceIndex size = %d, want <= %d", got, a.config.SourceIndexLimit)
	}
	if _, ok := a.sourceIndex["src-keep-1"]; !ok {
		t.Fatal("expected live referenced source src-keep-1 to be retained")
	}
	if _, ok := a.sourceIndex["src-keep-2"]; !ok {
		t.Fatal("expected live referenced source src-keep-2 to be retained")
	}
	dropped := 0
	for _, id := range []string{"src-drop-1", "src-drop-2", "src-drop-3"} {
		if _, ok := a.sourceIndex[id]; !ok {
			dropped++
		}
	}
	if dropped < 2 {
		t.Fatalf("expected at least two unreferenced sources to be pruned, dropped=%d", dropped)
	}
}
