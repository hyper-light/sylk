package archivalist

import (
	"sync"
	"time"
)

// MemoryTier represents the tier of a memory item
type MemoryTier int

const (
	TierHot MemoryTier = iota
	TierWarm
	TierCold
)

// TokenBudget defines the token budget per tier
type TokenBudget struct {
	HotMax    int // Hot zone max tokens
	WarmMax   int // Warm zone max tokens
	BufferMax int // Buffer zone max tokens
}

// DefaultTokenBudget returns sensible defaults
func DefaultTokenBudget() TokenBudget {
	return TokenBudget{
		HotMax:    200_000,
		WarmMax:   300_000,
		BufferMax: 500_000,
	}
}

// MemoryItem represents an item in memory
type MemoryItem struct {
	ID         string
	Type       string // "session", "pattern", "failure", "file", "query"
	Tokens     int
	LastAccess time.Time
	SessionID  string
	Tier       MemoryTier
}

// MemoryManager manages the memory hierarchy
type MemoryManager struct {
	mu sync.RWMutex

	items  map[string]*MemoryItem
	byTier map[MemoryTier][]string

	tokensByTier map[MemoryTier]int
	budget       TokenBudget
}

// NewMemoryManager creates a new memory manager
func NewMemoryManager(budget TokenBudget) *MemoryManager {
	if budget.HotMax == 0 {
		budget = DefaultTokenBudget()
	}

	return &MemoryManager{
		items:        make(map[string]*MemoryItem),
		byTier:       make(map[MemoryTier][]string),
		tokensByTier: make(map[MemoryTier]int),
		budget:       budget,
	}
}

// Add adds an item to memory
func (mm *MemoryManager) Add(item *MemoryItem) error {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	tier := mm.classifyTier(item)
	item.Tier = tier
	item.LastAccess = time.Now()

	if mm.needsEviction(tier, item.Tokens) {
		mm.evictFromTier(tier, item.Tokens)
	}

	mm.items[item.ID] = item
	mm.byTier[tier] = append(mm.byTier[tier], item.ID)
	mm.tokensByTier[tier] += item.Tokens

	return nil
}

// Get retrieves an item and updates its access time
func (mm *MemoryManager) Get(id string) (*MemoryItem, bool) {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	item, ok := mm.items[id]
	if !ok {
		return nil, false
	}

	item.LastAccess = time.Now()
	return item, true
}

// Promote moves an item to a higher tier
func (mm *MemoryManager) Promote(id string) {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	item, ok := mm.items[id]
	if !ok || item.Tier == TierHot {
		return
	}

	mm.moveTier(item, item.Tier-1)
}

// Demote moves an item to a lower tier
func (mm *MemoryManager) Demote(id string) {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	item, ok := mm.items[id]
	if !ok || item.Tier == TierCold {
		return
	}

	mm.moveTier(item, item.Tier+1)
}

// Remove removes an item from memory
func (mm *MemoryManager) Remove(id string) {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	mm.removeItemLocked(id)
}

// Stats returns memory statistics
func (mm *MemoryManager) Stats() MemoryStats {
	mm.mu.RLock()
	defer mm.mu.RUnlock()

	return MemoryStats{
		TotalItems:   len(mm.items),
		HotTokens:    mm.tokensByTier[TierHot],
		WarmTokens:   mm.tokensByTier[TierWarm],
		ColdTokens:   mm.tokensByTier[TierCold],
		HotBudget:    mm.budget.HotMax,
		WarmBudget:   mm.budget.WarmMax,
		BufferBudget: mm.budget.BufferMax,
	}
}

// MemoryStats contains memory statistics
type MemoryStats struct {
	TotalItems   int `json:"total_items"`
	HotTokens    int `json:"hot_tokens"`
	WarmTokens   int `json:"warm_tokens"`
	ColdTokens   int `json:"cold_tokens"`
	HotBudget    int `json:"hot_budget"`
	WarmBudget   int `json:"warm_budget"`
	BufferBudget int `json:"buffer_budget"`
}

// classifyTier determines the appropriate tier for an item
func (mm *MemoryManager) classifyTier(item *MemoryItem) MemoryTier {
	switch item.Type {
	case "session", "resume":
		return TierHot
	case "pattern", "failure":
		return TierWarm
	default:
		return TierCold
	}
}

// needsEviction checks if eviction is needed
func (mm *MemoryManager) needsEviction(tier MemoryTier, additionalTokens int) bool {
	current := mm.tokensByTier[tier]
	max := mm.getTierBudget(tier)
	return current+additionalTokens > max
}

// getTierBudget returns the budget for a tier
func (mm *MemoryManager) getTierBudget(tier MemoryTier) int {
	switch tier {
	case TierHot:
		return mm.budget.HotMax
	case TierWarm:
		return mm.budget.WarmMax
	default:
		return mm.budget.BufferMax
	}
}

// evictFromTier evicts items from a tier to make room
func (mm *MemoryManager) evictFromTier(tier MemoryTier, needed int) {
	ids := mm.byTier[tier]
	if len(ids) == 0 {
		return
	}

	// Sort by last access (oldest first)
	mm.sortByLastAccess(ids)

	evicted := 0
	for _, id := range ids {
		if evicted >= needed {
			break
		}

		item := mm.items[id]
		if item == nil {
			continue
		}

		evicted += item.Tokens
		mm.removeItemLocked(id)
	}
}

// sortByLastAccess sorts IDs by last access time
func (mm *MemoryManager) sortByLastAccess(ids []string) {
	for i := 0; i < len(ids)-1; i++ {
		for j := i + 1; j < len(ids); j++ {
			iItem := mm.items[ids[i]]
			jItem := mm.items[ids[j]]
			if iItem != nil && jItem != nil && jItem.LastAccess.Before(iItem.LastAccess) {
				ids[i], ids[j] = ids[j], ids[i]
			}
		}
	}
}

// moveTier moves an item to a different tier
func (mm *MemoryManager) moveTier(item *MemoryItem, newTier MemoryTier) {
	oldTier := item.Tier

	mm.byTier[oldTier] = removeFromSlice(mm.byTier[oldTier], item.ID)
	mm.tokensByTier[oldTier] -= item.Tokens

	item.Tier = newTier
	mm.byTier[newTier] = append(mm.byTier[newTier], item.ID)
	mm.tokensByTier[newTier] += item.Tokens
}

// removeItemLocked removes an item (must hold lock)
func (mm *MemoryManager) removeItemLocked(id string) {
	item, ok := mm.items[id]
	if !ok {
		return
	}

	mm.byTier[item.Tier] = removeFromSlice(mm.byTier[item.Tier], id)
	mm.tokensByTier[item.Tier] -= item.Tokens
	delete(mm.items, id)
}
