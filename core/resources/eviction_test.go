package resources

import (
	"sync"
	"testing"
	"time"
)

type mockEvictableEntry struct {
	id         string
	memorySize int64
	tokenCost  int64
	lastAccess time.Time
}

func (m *mockEvictableEntry) ID() string            { return m.id }
func (m *mockEvictableEntry) MemorySize() int64     { return m.memorySize }
func (m *mockEvictableEntry) TokenCost() int64      { return m.tokenCost }
func (m *mockEvictableEntry) LastAccess() time.Time { return m.lastAccess }

func newMockEntry(id string, memSize, tokenCost int64, hoursAgo float64) *mockEvictableEntry {
	return &mockEvictableEntry{
		id:         id,
		memorySize: memSize,
		tokenCost:  tokenCost,
		lastAccess: time.Now().Add(-time.Duration(hoursAgo * float64(time.Hour))),
	}
}

func TestTokenWeightedEvictor_NewTokenWeightedEvictor(t *testing.T) {
	config := DefaultEvictionConfig()
	evictor := NewTokenWeightedEvictor(config)

	if evictor == nil {
		t.Fatal("expected non-nil evictor")
	}
}

func TestTokenWeightedEvictor_ComputeScore_BasicCalculation(t *testing.T) {
	config := DefaultEvictionConfig()
	evictor := NewTokenWeightedEvictor(config)

	entry := newMockEntry("test", 1000, 500, 0)
	score := evictor.ComputeScore(entry)

	expectedBase := float64(500) / float64(1000)
	if score < expectedBase*0.9 || score > expectedBase*1.1 {
		t.Errorf("expected score around %f, got %f", expectedBase, score)
	}
}

func TestTokenWeightedEvictor_ComputeScore_ZeroMemorySize(t *testing.T) {
	config := DefaultEvictionConfig()
	evictor := NewTokenWeightedEvictor(config)

	entry := newMockEntry("test", 0, 500, 0)
	score := evictor.ComputeScore(entry)

	if score != 0 {
		t.Errorf("expected 0 for zero memory size, got %f", score)
	}
}

func TestTokenWeightedEvictor_ComputeScore_NegativeMemorySize(t *testing.T) {
	config := DefaultEvictionConfig()
	evictor := NewTokenWeightedEvictor(config)

	entry := newMockEntry("test", -100, 500, 0)
	score := evictor.ComputeScore(entry)

	if score != 0 {
		t.Errorf("expected 0 for negative memory size, got %f", score)
	}
}

func TestTokenWeightedEvictor_ComputeScore_RecencyDecay(t *testing.T) {
	config := DefaultEvictionConfig()
	config.RecencyDecayHours = 24.0
	evictor := NewTokenWeightedEvictor(config)

	recentEntry := newMockEntry("recent", 1000, 500, 0)
	oldEntry := newMockEntry("old", 1000, 500, 48)

	recentScore := evictor.ComputeScore(recentEntry)
	oldScore := evictor.ComputeScore(oldEntry)

	if oldScore >= recentScore {
		t.Errorf("old entry score (%f) should be less than recent score (%f)", oldScore, recentScore)
	}
}

func TestTokenWeightedEvictor_ComputeScore_MinRecencyFactor(t *testing.T) {
	config := DefaultEvictionConfig()
	config.RecencyDecayHours = 1.0
	config.MinRecencyFactor = 0.1
	evictor := NewTokenWeightedEvictor(config)

	veryOldEntry := newMockEntry("ancient", 1000, 1000, 1000)
	score := evictor.ComputeScore(veryOldEntry)

	minExpected := (float64(1000) / float64(1000)) * 0.1
	if score < minExpected*0.99 {
		t.Errorf("expected score at least %f, got %f", minExpected, score)
	}
}

func TestTokenWeightedEvictor_ComputeScore_HighTokenCostPreserved(t *testing.T) {
	config := DefaultEvictionConfig()
	evictor := NewTokenWeightedEvictor(config)

	lowToken := newMockEntry("low", 1000, 100, 0)
	highToken := newMockEntry("high", 1000, 10000, 0)

	lowScore := evictor.ComputeScore(lowToken)
	highScore := evictor.ComputeScore(highToken)

	if highScore <= lowScore {
		t.Errorf("high token cost entry (%f) should have higher score than low (%f)", highScore, lowScore)
	}
}

func TestTokenWeightedEvictor_SelectForEviction_EmptyEntries(t *testing.T) {
	config := DefaultEvictionConfig()
	evictor := NewTokenWeightedEvictor(config)

	candidates := evictor.SelectForEviction(nil)
	if candidates != nil {
		t.Errorf("expected nil for empty entries, got %v", candidates)
	}

	candidates = evictor.SelectForEviction([]EvictableEntry{})
	if candidates != nil {
		t.Errorf("expected nil for empty slice, got %v", candidates)
	}
}

func TestTokenWeightedEvictor_SelectForEviction_SortedByScore(t *testing.T) {
	config := DefaultEvictionConfig()
	evictor := NewTokenWeightedEvictor(config)

	entries := []EvictableEntry{
		newMockEntry("high", 1000, 10000, 0),
		newMockEntry("low", 1000, 100, 0),
		newMockEntry("medium", 1000, 1000, 0),
	}

	candidates := evictor.SelectForEviction(entries)

	if len(candidates) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(candidates))
	}

	if candidates[0].Entry.ID() != "low" {
		t.Errorf("expected lowest score first, got %s", candidates[0].Entry.ID())
	}
	if candidates[2].Entry.ID() != "high" {
		t.Errorf("expected highest score last, got %s", candidates[2].Entry.ID())
	}
}

func TestTokenWeightedEvictor_SelectToFreeBytes_ZeroTarget(t *testing.T) {
	config := DefaultEvictionConfig()
	evictor := NewTokenWeightedEvictor(config)

	entries := []EvictableEntry{
		newMockEntry("a", 1000, 100, 0),
	}

	result := evictor.SelectToFreeBytes(entries, 0)
	if result != nil {
		t.Errorf("expected nil for zero target, got %v", result)
	}

	result = evictor.SelectToFreeBytes(entries, -100)
	if result != nil {
		t.Errorf("expected nil for negative target, got %v", result)
	}
}

func TestTokenWeightedEvictor_SelectToFreeBytes_PartialEviction(t *testing.T) {
	config := DefaultEvictionConfig()
	evictor := NewTokenWeightedEvictor(config)

	entries := []EvictableEntry{
		newMockEntry("a", 1000, 100, 0),
		newMockEntry("b", 1000, 200, 0),
		newMockEntry("c", 1000, 300, 0),
	}

	result := evictor.SelectToFreeBytes(entries, 1500)

	if len(result) != 2 {
		t.Errorf("expected 2 entries to free 1500 bytes, got %d", len(result))
	}
}

func TestTokenWeightedEvictor_SelectToFreeBytes_AllEvicted(t *testing.T) {
	config := DefaultEvictionConfig()
	evictor := NewTokenWeightedEvictor(config)

	entries := []EvictableEntry{
		newMockEntry("a", 500, 100, 0),
		newMockEntry("b", 500, 200, 0),
	}

	result := evictor.SelectToFreeBytes(entries, 5000)

	if len(result) != 2 {
		t.Errorf("expected all 2 entries, got %d", len(result))
	}
}

func TestTokenWeightedEvictor_EvictFromComponent_NoEvictionNeeded(t *testing.T) {
	config := DefaultEvictionConfig()
	evictor := NewTokenWeightedEvictor(config)

	entries := []EvictableEntry{
		newMockEntry("a", 100, 100, 0),
	}

	result := evictor.EvictFromComponent(entries, 500, 1000, false)

	if len(result.Evicted) != 0 {
		t.Error("expected no eviction when under target")
	}
	if result.Remaining != 500 {
		t.Errorf("expected remaining 500, got %d", result.Remaining)
	}
}

func TestTokenWeightedEvictor_EvictFromComponent_NonAggressive(t *testing.T) {
	config := DefaultEvictionConfig()
	evictor := NewTokenWeightedEvictor(config)

	entries := []EvictableEntry{
		newMockEntry("a", 200, 100, 0),
		newMockEntry("b", 200, 200, 0),
		newMockEntry("c", 200, 300, 0),
	}

	result := evictor.EvictFromComponent(entries, 800, 1000, false)

	if result.FreedBytes == 0 {
		t.Error("expected some bytes to be freed")
	}
}

func TestTokenWeightedEvictor_EvictFromComponent_Aggressive(t *testing.T) {
	config := DefaultEvictionConfig()
	evictor := NewTokenWeightedEvictor(config)

	entries := []EvictableEntry{
		newMockEntry("a", 200, 100, 0),
		newMockEntry("b", 200, 200, 0),
		newMockEntry("c", 200, 300, 0),
		newMockEntry("d", 200, 400, 0),
	}

	nonAggResult := evictor.EvictFromComponent(entries, 800, 1000, false)
	aggResult := evictor.EvictFromComponent(entries, 800, 1000, true)

	if aggResult.FreedBytes <= nonAggResult.FreedBytes {
		t.Error("aggressive eviction should free more bytes")
	}
}

func TestLRUEvictor_NewLRUEvictor(t *testing.T) {
	evictor := NewLRUEvictor()
	if evictor == nil {
		t.Fatal("expected non-nil evictor")
	}
}

func TestLRUEvictor_SelectForEviction_EmptyEntries(t *testing.T) {
	evictor := NewLRUEvictor()

	result := evictor.SelectForEviction(nil)
	if result != nil {
		t.Errorf("expected nil for nil input, got %v", result)
	}

	result = evictor.SelectForEviction([]EvictableEntry{})
	if result != nil {
		t.Errorf("expected nil for empty slice, got %v", result)
	}
}

func TestLRUEvictor_SelectForEviction_SortedByAccessTime(t *testing.T) {
	evictor := NewLRUEvictor()

	now := time.Now()
	entries := []EvictableEntry{
		&mockEvictableEntry{id: "newest", lastAccess: now, memorySize: 100},
		&mockEvictableEntry{id: "oldest", lastAccess: now.Add(-2 * time.Hour), memorySize: 100},
		&mockEvictableEntry{id: "middle", lastAccess: now.Add(-1 * time.Hour), memorySize: 100},
	}

	result := evictor.SelectForEviction(entries)

	if len(result) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(result))
	}
	if result[0].ID() != "oldest" {
		t.Errorf("expected oldest first, got %s", result[0].ID())
	}
	if result[1].ID() != "middle" {
		t.Errorf("expected middle second, got %s", result[1].ID())
	}
	if result[2].ID() != "newest" {
		t.Errorf("expected newest last, got %s", result[2].ID())
	}
}

func TestLRUEvictor_SelectToFreeBytes_ZeroTarget(t *testing.T) {
	evictor := NewLRUEvictor()

	entries := []EvictableEntry{
		newMockEntry("a", 100, 100, 0),
	}

	result := evictor.SelectToFreeBytes(entries, 0)
	if result != nil {
		t.Errorf("expected nil for zero target, got %v", result)
	}

	result = evictor.SelectToFreeBytes(entries, -100)
	if result != nil {
		t.Errorf("expected nil for negative target, got %v", result)
	}
}

func TestLRUEvictor_SelectToFreeBytes_EvictsOldestFirst(t *testing.T) {
	evictor := NewLRUEvictor()

	now := time.Now()
	entries := []EvictableEntry{
		&mockEvictableEntry{id: "new", lastAccess: now, memorySize: 500},
		&mockEvictableEntry{id: "old", lastAccess: now.Add(-2 * time.Hour), memorySize: 500},
	}

	result := evictor.SelectToFreeBytes(entries, 400)

	if len(result) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result))
	}
	if result[0].ID() != "old" {
		t.Errorf("expected oldest to be evicted, got %s", result[0].ID())
	}
}

func TestLRUEvictor_SelectToFreeBytes_CollectsUntilTarget(t *testing.T) {
	evictor := NewLRUEvictor()

	now := time.Now()
	entries := []EvictableEntry{
		&mockEvictableEntry{id: "a", lastAccess: now.Add(-3 * time.Hour), memorySize: 300},
		&mockEvictableEntry{id: "b", lastAccess: now.Add(-2 * time.Hour), memorySize: 300},
		&mockEvictableEntry{id: "c", lastAccess: now.Add(-1 * time.Hour), memorySize: 300},
	}

	result := evictor.SelectToFreeBytes(entries, 500)

	if len(result) != 2 {
		t.Errorf("expected 2 entries to meet 500 byte target, got %d", len(result))
	}
}

func TestEvictionManager_NewEvictionManager(t *testing.T) {
	config := DefaultEvictionConfig()
	manager := NewEvictionManager(config)

	if manager == nil {
		t.Fatal("expected non-nil manager")
	}
}

func TestEvictionManager_SetAndGetPolicy(t *testing.T) {
	config := DefaultEvictionConfig()
	manager := NewEvictionManager(config)

	manager.SetPolicy(ComponentQueryCache, EvictionPolicyTokenWeighted)
	policy := manager.GetPolicy(ComponentQueryCache)

	if policy != EvictionPolicyTokenWeighted {
		t.Errorf("expected TokenWeighted policy, got %d", policy)
	}
}

func TestEvictionManager_DefaultPolicy(t *testing.T) {
	config := DefaultEvictionConfig()
	manager := NewEvictionManager(config)

	policy := manager.GetPolicy(ComponentStaging)
	if policy != EvictionPolicyLRU {
		t.Errorf("expected LRU as default policy, got %d", policy)
	}
}

func TestEvictionManager_Evict_TokenWeightedPolicy(t *testing.T) {
	config := DefaultEvictionConfig()
	manager := NewEvictionManager(config)
	manager.SetPolicy(ComponentQueryCache, EvictionPolicyTokenWeighted)

	entries := []EvictableEntry{
		newMockEntry("low", 200, 100, 0),
		newMockEntry("high", 200, 1000, 0),
	}

	result := manager.Evict(ComponentQueryCache, entries, 800, 1000, false)

	if result.FreedBytes == 0 {
		t.Error("expected some bytes freed")
	}
}

func TestEvictionManager_Evict_LRUPolicy(t *testing.T) {
	config := DefaultEvictionConfig()
	manager := NewEvictionManager(config)
	manager.SetPolicy(ComponentStaging, EvictionPolicyLRU)

	now := time.Now()
	entries := []EvictableEntry{
		&mockEvictableEntry{id: "old", lastAccess: now.Add(-2 * time.Hour), memorySize: 200},
		&mockEvictableEntry{id: "new", lastAccess: now, memorySize: 200},
	}

	result := manager.Evict(ComponentStaging, entries, 800, 1000, false)

	if result.FreedBytes == 0 {
		t.Error("expected some bytes freed")
	}
}

func TestEvictionManager_Evict_NoEvictionNeeded(t *testing.T) {
	config := DefaultEvictionConfig()
	manager := NewEvictionManager(config)

	entries := []EvictableEntry{
		newMockEntry("a", 100, 100, 0),
	}

	result := manager.Evict(ComponentQueryCache, entries, 400, 1000, false)

	if len(result.Evicted) != 0 {
		t.Error("expected no eviction when under target")
	}
	if result.Remaining != 400 {
		t.Errorf("expected remaining 400, got %d", result.Remaining)
	}
}

func TestEvictionManager_Evict_AggressiveVsNonAggressive(t *testing.T) {
	config := DefaultEvictionConfig()
	manager := NewEvictionManager(config)

	entries := []EvictableEntry{
		newMockEntry("a", 200, 100, 0),
		newMockEntry("b", 200, 200, 0),
		newMockEntry("c", 200, 300, 0),
		newMockEntry("d", 200, 400, 0),
	}

	nonAggResult := manager.Evict(ComponentQueryCache, entries, 800, 1000, false)
	aggResult := manager.Evict(ComponentQueryCache, entries, 800, 1000, true)

	if aggResult.FreedBytes <= nonAggResult.FreedBytes {
		t.Error("aggressive eviction should free more bytes")
	}
}

func TestEvictionManager_ConcurrentPolicyAccess(t *testing.T) {
	config := DefaultEvictionConfig()
	manager := NewEvictionManager(config)

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			manager.SetPolicy(ComponentQueryCache, EvictionPolicyTokenWeighted)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			_ = manager.GetPolicy(ComponentQueryCache)
		}
	}()

	wg.Wait()
}

func TestEvictionPolicy_Constants(t *testing.T) {
	if EvictionPolicyLRU != 0 {
		t.Error("EvictionPolicyLRU should be 0")
	}
	if EvictionPolicyTokenWeighted != 1 {
		t.Error("EvictionPolicyTokenWeighted should be 1")
	}
}

func TestDefaultEvictionConfig(t *testing.T) {
	config := DefaultEvictionConfig()

	if config.RecencyDecayHours != 24.0 {
		t.Errorf("expected RecencyDecayHours 24.0, got %f", config.RecencyDecayHours)
	}
	if config.MinRecencyFactor != 0.1 {
		t.Errorf("expected MinRecencyFactor 0.1, got %f", config.MinRecencyFactor)
	}
}

func TestEvictionResult_Structure(t *testing.T) {
	config := DefaultEvictionConfig()
	evictor := NewTokenWeightedEvictor(config)

	entries := []EvictableEntry{
		newMockEntry("a", 200, 100, 0),
		newMockEntry("b", 300, 200, 0),
	}

	result := evictor.EvictFromComponent(entries, 800, 1000, false)

	if result.FreedBytes < 0 {
		t.Error("FreedBytes should be non-negative")
	}
	if result.Remaining < 0 {
		t.Error("Remaining should be non-negative")
	}

	var totalEvictedSize int64
	for _, e := range result.Evicted {
		totalEvictedSize += e.MemorySize()
	}
	if totalEvictedSize != result.FreedBytes {
		t.Errorf("FreedBytes (%d) should equal sum of evicted sizes (%d)", result.FreedBytes, totalEvictedSize)
	}
}

func TestTokenWeightedEvictor_HighTokenCostEvictedLast(t *testing.T) {
	config := DefaultEvictionConfig()
	evictor := NewTokenWeightedEvictor(config)

	entries := []EvictableEntry{
		newMockEntry("expensive", 100, 10000, 0),
		newMockEntry("cheap1", 100, 10, 0),
		newMockEntry("cheap2", 100, 20, 0),
	}

	toEvict := evictor.SelectToFreeBytes(entries, 150)

	if len(toEvict) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(toEvict))
	}

	for _, e := range toEvict {
		if e.ID() == "expensive" {
			t.Error("expensive entry should be preserved (evicted last)")
		}
	}
}

func TestLRUEvictor_DoesNotModifyOriginal(t *testing.T) {
	evictor := NewLRUEvictor()

	now := time.Now()
	original := []EvictableEntry{
		&mockEvictableEntry{id: "c", lastAccess: now},
		&mockEvictableEntry{id: "a", lastAccess: now.Add(-2 * time.Hour)},
		&mockEvictableEntry{id: "b", lastAccess: now.Add(-1 * time.Hour)},
	}

	_ = evictor.SelectForEviction(original)

	if original[0].ID() != "c" || original[1].ID() != "a" || original[2].ID() != "b" {
		t.Error("original slice should not be modified")
	}
}

func TestEvictionManager_MultipleComponents(t *testing.T) {
	config := DefaultEvictionConfig()
	manager := NewEvictionManager(config)

	manager.SetPolicy(ComponentQueryCache, EvictionPolicyTokenWeighted)
	manager.SetPolicy(ComponentStaging, EvictionPolicyLRU)
	manager.SetPolicy(ComponentAgentContext, EvictionPolicyTokenWeighted)
	manager.SetPolicy(ComponentWAL, EvictionPolicyLRU)

	if manager.GetPolicy(ComponentQueryCache) != EvictionPolicyTokenWeighted {
		t.Error("QueryCache should use TokenWeighted")
	}
	if manager.GetPolicy(ComponentStaging) != EvictionPolicyLRU {
		t.Error("Staging should use LRU")
	}
	if manager.GetPolicy(ComponentAgentContext) != EvictionPolicyTokenWeighted {
		t.Error("AgentContext should use TokenWeighted")
	}
	if manager.GetPolicy(ComponentWAL) != EvictionPolicyLRU {
		t.Error("WAL should use LRU")
	}
}

func TestTokenWeightedEvictor_ZeroTokenCost(t *testing.T) {
	config := DefaultEvictionConfig()
	evictor := NewTokenWeightedEvictor(config)

	entry := newMockEntry("zero", 1000, 0, 0)
	score := evictor.ComputeScore(entry)

	if score != 0 {
		t.Errorf("expected 0 score for zero token cost, got %f", score)
	}
}

func TestEvictionCandidate_Structure(t *testing.T) {
	entry := newMockEntry("test", 100, 100, 0)
	candidate := EvictionCandidate{
		Entry: entry,
		Score: 1.5,
	}

	if candidate.Entry.ID() != "test" {
		t.Error("candidate should hold entry reference")
	}
	if candidate.Score != 1.5 {
		t.Errorf("expected score 1.5, got %f", candidate.Score)
	}
}
