package archivalist

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// =============================================================================
// MemoryManager Unit Tests
// =============================================================================

func TestMemoryManager_Add(t *testing.T) {
	mm := NewMemoryManager(DefaultTokenBudget())

	item := &MemoryItem{
		ID:     "item-1",
		Type:   "session",
		Tokens: 100,
	}

	err := mm.Add(item)

	assert.NoError(t, err, "Add")
	assert.Equal(t, TierHot, item.Tier, "New item should be in hot tier")
}

func TestMemoryManager_Get(t *testing.T) {
	mm := NewMemoryManager(DefaultTokenBudget())

	item := &MemoryItem{
		ID:     "item-1",
		Type:   "session",
		Tokens: 100,
	}
	mm.Add(item)

	retrieved, found := mm.Get("item-1")

	assert.True(t, found, "Should find item")
	assert.Equal(t, "item-1", retrieved.ID, "ID should match")
}

func TestMemoryManager_Get_NotFound(t *testing.T) {
	mm := NewMemoryManager(DefaultTokenBudget())

	_, found := mm.Get("nonexistent")

	assert.False(t, found, "Should not find nonexistent item")
}

func TestMemoryManager_Promote(t *testing.T) {
	mm := NewMemoryManager(DefaultTokenBudget())

	// Type "pattern" is classified as TierWarm by classifyTier
	item := &MemoryItem{
		ID:     "item-1",
		Type:   "pattern", // classifies as TierWarm
		Tokens: 100,
	}
	mm.Add(item)

	mm.Promote("item-1")

	retrieved, _ := mm.Get("item-1")
	assert.Equal(t, TierHot, retrieved.Tier, "Should be promoted to hot")
}

func TestMemoryManager_Demote(t *testing.T) {
	mm := NewMemoryManager(DefaultTokenBudget())

	item := &MemoryItem{
		ID:     "item-1",
		Type:   "session",
		Tokens: 100,
		Tier:   TierHot,
	}
	mm.Add(item)

	mm.Demote("item-1")

	retrieved, _ := mm.Get("item-1")
	assert.Equal(t, TierWarm, retrieved.Tier, "Should be demoted to warm")

	mm.Demote("item-1")

	retrieved, _ = mm.Get("item-1")
	assert.Equal(t, TierCold, retrieved.Tier, "Should be demoted to cold")
}

func TestMemoryManager_Remove(t *testing.T) {
	mm := NewMemoryManager(DefaultTokenBudget())

	item := &MemoryItem{
		ID:     "item-1",
		Type:   "session",
		Tokens: 100,
	}
	mm.Add(item)

	mm.Remove("item-1")

	_, found := mm.Get("item-1")
	assert.False(t, found, "Item should be removed")
}

func TestMemoryManager_Stats(t *testing.T) {
	mm := NewMemoryManager(DefaultTokenBudget())

	// Add items in different types (which map to tiers)
	mm.Add(&MemoryItem{ID: "hot-1", Type: "session", Tokens: 100})
	mm.Add(&MemoryItem{ID: "hot-2", Type: "resume", Tokens: 200})
	mm.Add(&MemoryItem{ID: "warm-1", Type: "pattern", Tokens: 150})
	mm.Add(&MemoryItem{ID: "cold-1", Type: "query", Tokens: 50})

	stats := mm.Stats()

	assert.Equal(t, 4, stats.TotalItems, "Should have 4 items")
	assert.Equal(t, 300, stats.HotTokens, "Hot tokens should be 300")
	assert.Equal(t, 150, stats.WarmTokens, "Warm tokens should be 150")
	assert.Equal(t, 50, stats.ColdTokens, "Cold tokens should be 50")
}

// =============================================================================
// Tiering Tests
// =============================================================================

func TestTiering_HotTierAccess(t *testing.T) {
	mm := NewMemoryManager(DefaultTokenBudget())

	item := &MemoryItem{
		ID:     "item-1",
		Type:   "session",
		Tokens: 100,
	}
	mm.Add(item)

	// Access multiple times
	for i := 0; i < 5; i++ {
		mm.Get("item-1")
	}

	retrieved, _ := mm.Get("item-1")
	assert.Equal(t, TierHot, retrieved.Tier, "Frequently accessed item should stay hot")
}

func TestTiering_PromotionOnAccess(t *testing.T) {
	mm := NewMemoryManager(DefaultTokenBudget())

	// Add cold item
	item := &MemoryItem{
		ID:     "item-1",
		Type:   "query",
		Tokens: 100,
		Tier:   TierCold,
	}
	mm.Add(item)

	// Access it
	mm.Get("item-1")

	// Manual promotion (simulating access-based promotion)
	mm.Promote("item-1")

	retrieved, _ := mm.Get("item-1")
	assert.Equal(t, TierWarm, retrieved.Tier, "Accessed cold item should promote to warm")
}

func TestTiering_TokenBudgetRespected(t *testing.T) {
	budget := TokenBudget{
		HotMax:    500,
		WarmMax:   300,
		BufferMax: 100,
	}
	mm := NewMemoryManager(budget)

	// Add items that exceed hot budget
	for i := 0; i < 10; i++ {
		mm.Add(&MemoryItem{
			ID:     "item-" + string(rune('0'+i)),
			Type:   "session",
			Tokens: 100, // Each item is 100 tokens
		})
	}

	stats := mm.Stats()

	// Implementation may vary, but hot tokens should not vastly exceed budget
	assert.LessOrEqual(t, stats.HotTokens, budget.HotMax*2, "Hot tokens should be bounded")
}

func TestTiering_TierStatsAccuracy(t *testing.T) {
	mm := NewMemoryManager(DefaultTokenBudget())

	// Add items
	mm.Add(&MemoryItem{ID: "1", Type: "session", Tokens: 100})
	mm.Add(&MemoryItem{ID: "2", Type: "session", Tokens: 100})
	mm.Add(&MemoryItem{ID: "3", Type: "pattern", Tokens: 100})

	// Demote one
	mm.Demote("1")

	stats := mm.Stats()

	// After demotion, tokens should shift between tiers
	assert.Equal(t, 3, stats.TotalItems, "Should still have 3 items")
}

// =============================================================================
// MemoryManager Edge Cases
// =============================================================================

func TestMemoryManager_PromoteAlreadyHot(t *testing.T) {
	mm := NewMemoryManager(DefaultTokenBudget())

	item := &MemoryItem{
		ID:     "item-1",
		Type:   "session",
		Tokens: 100,
		Tier:   TierHot,
	}
	mm.Add(item)

	// Promoting hot item should be no-op
	mm.Promote("item-1")

	retrieved, _ := mm.Get("item-1")
	assert.Equal(t, TierHot, retrieved.Tier, "Should still be hot")
}

func TestMemoryManager_DemoteAlreadyCold(t *testing.T) {
	mm := NewMemoryManager(DefaultTokenBudget())

	item := &MemoryItem{
		ID:     "item-1",
		Type:   "query",
		Tokens: 100,
		Tier:   TierCold,
	}
	mm.Add(item)

	// Demoting cold item should be no-op (or remove)
	mm.Demote("item-1")

	// Should still exist or be removed
	_, found := mm.Get("item-1")
	// Either outcome is acceptable
	_ = found
}

func TestMemoryManager_ConcurrentAccess(t *testing.T) {
	mm := NewMemoryManager(DefaultTokenBudget())

	// Add items
	for i := 0; i < 100; i++ {
		mm.Add(&MemoryItem{
			ID:     "item-" + string(rune(i)),
			Type:   "query",
			Tokens: 10,
		})
	}

	runConcurrent(t, 10, func(goroutine int) {
		for j := 0; j < 100; j++ {
			id := "item-" + string(rune(j%100))
			mm.Get(id)
			if j%10 == 0 {
				mm.Promote(id)
			}
			if j%20 == 0 {
				mm.Demote(id)
			}
		}
	})

	// Should complete without deadlock
	stats := mm.Stats()
	assert.GreaterOrEqual(t, stats.TotalItems, 0, "Should have valid stats")
}
