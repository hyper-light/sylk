package resources

import (
	"sort"
	"sync"
	"time"
)

type UsageCategory int

const (
	UsageCategoryCaches UsageCategory = iota
	UsageCategoryPipelines
	UsageCategoryLLMBuffers
	UsageCategoryWAL
)

var usageCategoryNames = [...]string{
	UsageCategoryCaches:     "caches",
	UsageCategoryPipelines:  "pipelines",
	UsageCategoryLLMBuffers: "llm_buffers",
	UsageCategoryWAL:        "wal",
}

func (c UsageCategory) String() string {
	if int(c) >= 0 && int(c) < len(usageCategoryNames) {
		return usageCategoryNames[c]
	}
	return "unknown"
}

type ComponentUsage struct {
	ID        string
	Category  UsageCategory
	Bytes     int64
	UpdatedAt time.Time
}

type UsageRegistry struct {
	mu          sync.RWMutex
	byCategory  map[UsageCategory]int64
	byComponent map[string]*ComponentUsage
}

func NewUsageRegistry() *UsageRegistry {
	return &UsageRegistry{
		byCategory:  make(map[UsageCategory]int64),
		byComponent: make(map[string]*ComponentUsage),
	}
}

func (r *UsageRegistry) Report(id string, category UsageCategory, bytes int64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.removeExistingUsage(id)
	r.recordNewUsage(id, category, bytes)
}

func (r *UsageRegistry) removeExistingUsage(id string) {
	if old, exists := r.byComponent[id]; exists {
		r.byCategory[old.Category] -= old.Bytes
	}
}

func (r *UsageRegistry) recordNewUsage(id string, category UsageCategory, bytes int64) {
	r.byComponent[id] = &ComponentUsage{
		ID:        id,
		Category:  category,
		Bytes:     bytes,
		UpdatedAt: time.Now(),
	}
	r.byCategory[category] += bytes
}

func (r *UsageRegistry) Release(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if usage, exists := r.byComponent[id]; exists {
		r.byCategory[usage.Category] -= usage.Bytes
		delete(r.byComponent, id)
	}
}

func (r *UsageRegistry) Total() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var total int64
	for _, bytes := range r.byCategory {
		total += bytes
	}
	return total
}

func (r *UsageRegistry) ByCategory(cat UsageCategory) int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byCategory[cat]
}

func (r *UsageRegistry) LargestComponents(n int, category UsageCategory) []*ComponentUsage {
	r.mu.RLock()
	defer r.mu.RUnlock()

	components := r.collectComponentsByCategory(category)
	r.sortByBytesDescending(components)
	return r.limitResults(components, n)
}

func (r *UsageRegistry) collectComponentsByCategory(category UsageCategory) []*ComponentUsage {
	var components []*ComponentUsage
	for _, c := range r.byComponent {
		if c.Category == category {
			components = append(components, c)
		}
	}
	return components
}

func (r *UsageRegistry) sortByBytesDescending(components []*ComponentUsage) {
	sort.Slice(components, func(i, j int) bool {
		return components[i].Bytes > components[j].Bytes
	})
}

func (r *UsageRegistry) limitResults(components []*ComponentUsage, n int) []*ComponentUsage {
	if len(components) > n {
		return components[:n]
	}
	return components
}

func (r *UsageRegistry) ComponentCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byComponent)
}

func (r *UsageRegistry) GetComponent(id string) (*ComponentUsage, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	usage, exists := r.byComponent[id]
	if !exists {
		return nil, false
	}

	copy := *usage
	return &copy, true
}

func (r *UsageRegistry) AllCategories() map[UsageCategory]int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[UsageCategory]int64, len(r.byCategory))
	for cat, bytes := range r.byCategory {
		result[cat] = bytes
	}
	return result
}
