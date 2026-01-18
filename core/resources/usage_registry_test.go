package resources

import (
	"sync"
	"testing"
	"time"
)

func TestUsageCategory_String(t *testing.T) {
	tests := []struct {
		cat  UsageCategory
		want string
	}{
		{UsageCategoryCaches, "caches"},
		{UsageCategoryPipelines, "pipelines"},
		{UsageCategoryLLMBuffers, "llm_buffers"},
		{UsageCategoryWAL, "wal"},
		{UsageCategory(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.cat.String(); got != tt.want {
			t.Errorf("UsageCategory(%d).String() = %q, want %q", tt.cat, got, tt.want)
		}
	}
}

func TestNewUsageRegistry(t *testing.T) {
	r := NewUsageRegistry()
	if r == nil {
		t.Fatal("NewUsageRegistry() returned nil")
	}
	if r.Total() != 0 {
		t.Errorf("Total() = %d, want 0", r.Total())
	}
	if r.ComponentCount() != 0 {
		t.Errorf("ComponentCount() = %d, want 0", r.ComponentCount())
	}
}

func TestUsageRegistry_Report(t *testing.T) {
	r := NewUsageRegistry()

	r.Report("cache-1", UsageCategoryCaches, 1000)

	if got := r.Total(); got != 1000 {
		t.Errorf("Total() = %d, want 1000", got)
	}
	if got := r.ByCategory(UsageCategoryCaches); got != 1000 {
		t.Errorf("ByCategory(Caches) = %d, want 1000", got)
	}
	if r.ComponentCount() != 1 {
		t.Errorf("ComponentCount() = %d, want 1", r.ComponentCount())
	}
}

func TestUsageRegistry_Report_UpdateExisting(t *testing.T) {
	r := NewUsageRegistry()

	r.Report("cache-1", UsageCategoryCaches, 1000)
	r.Report("cache-1", UsageCategoryCaches, 2000)

	if got := r.Total(); got != 2000 {
		t.Errorf("Total() after update = %d, want 2000", got)
	}
	if got := r.ByCategory(UsageCategoryCaches); got != 2000 {
		t.Errorf("ByCategory(Caches) after update = %d, want 2000", got)
	}
	if r.ComponentCount() != 1 {
		t.Errorf("ComponentCount() = %d, want 1", r.ComponentCount())
	}
}

func TestUsageRegistry_Report_CategoryChange(t *testing.T) {
	r := NewUsageRegistry()

	r.Report("component-1", UsageCategoryCaches, 1000)
	r.Report("component-1", UsageCategoryPipelines, 500)

	if got := r.ByCategory(UsageCategoryCaches); got != 0 {
		t.Errorf("ByCategory(Caches) after category change = %d, want 0", got)
	}
	if got := r.ByCategory(UsageCategoryPipelines); got != 500 {
		t.Errorf("ByCategory(Pipelines) = %d, want 500", got)
	}
	if got := r.Total(); got != 500 {
		t.Errorf("Total() = %d, want 500", got)
	}
}

func TestUsageRegistry_Release(t *testing.T) {
	r := NewUsageRegistry()

	r.Report("cache-1", UsageCategoryCaches, 1000)
	r.Release("cache-1")

	if got := r.Total(); got != 0 {
		t.Errorf("Total() after release = %d, want 0", got)
	}
	if got := r.ByCategory(UsageCategoryCaches); got != 0 {
		t.Errorf("ByCategory(Caches) after release = %d, want 0", got)
	}
	if r.ComponentCount() != 0 {
		t.Errorf("ComponentCount() after release = %d, want 0", r.ComponentCount())
	}
}

func TestUsageRegistry_Release_NonExistent(t *testing.T) {
	r := NewUsageRegistry()

	r.Release("nonexistent")

	if got := r.Total(); got != 0 {
		t.Errorf("Total() = %d, want 0", got)
	}
}

func TestUsageRegistry_MultipleComponents(t *testing.T) {
	r := NewUsageRegistry()

	r.Report("cache-1", UsageCategoryCaches, 1000)
	r.Report("cache-2", UsageCategoryCaches, 2000)
	r.Report("pipeline-1", UsageCategoryPipelines, 500)

	if got := r.Total(); got != 3500 {
		t.Errorf("Total() = %d, want 3500", got)
	}
	if got := r.ByCategory(UsageCategoryCaches); got != 3000 {
		t.Errorf("ByCategory(Caches) = %d, want 3000", got)
	}
	if got := r.ByCategory(UsageCategoryPipelines); got != 500 {
		t.Errorf("ByCategory(Pipelines) = %d, want 500", got)
	}
	if r.ComponentCount() != 3 {
		t.Errorf("ComponentCount() = %d, want 3", r.ComponentCount())
	}
}

func TestUsageRegistry_LargestComponents(t *testing.T) {
	r := NewUsageRegistry()

	r.Report("cache-small", UsageCategoryCaches, 100)
	r.Report("cache-medium", UsageCategoryCaches, 500)
	r.Report("cache-large", UsageCategoryCaches, 1000)
	r.Report("pipeline-1", UsageCategoryPipelines, 2000)

	largest := r.LargestComponents(2, UsageCategoryCaches)

	if len(largest) != 2 {
		t.Fatalf("LargestComponents(2) returned %d items, want 2", len(largest))
	}
	if largest[0].ID != "cache-large" {
		t.Errorf("largest[0].ID = %q, want %q", largest[0].ID, "cache-large")
	}
	if largest[1].ID != "cache-medium" {
		t.Errorf("largest[1].ID = %q, want %q", largest[1].ID, "cache-medium")
	}
}

func TestUsageRegistry_LargestComponents_LessAvailable(t *testing.T) {
	r := NewUsageRegistry()

	r.Report("cache-1", UsageCategoryCaches, 100)

	largest := r.LargestComponents(10, UsageCategoryCaches)

	if len(largest) != 1 {
		t.Errorf("LargestComponents(10) returned %d items, want 1", len(largest))
	}
}

func TestUsageRegistry_LargestComponents_EmptyCategory(t *testing.T) {
	r := NewUsageRegistry()

	r.Report("cache-1", UsageCategoryCaches, 100)

	largest := r.LargestComponents(5, UsageCategoryPipelines)

	if len(largest) != 0 {
		t.Errorf("LargestComponents() for empty category returned %d items, want 0", len(largest))
	}
}

func TestUsageRegistry_GetComponent(t *testing.T) {
	r := NewUsageRegistry()

	r.Report("cache-1", UsageCategoryCaches, 1000)

	comp, exists := r.GetComponent("cache-1")
	if !exists {
		t.Fatal("GetComponent() returned exists=false, want true")
	}
	if comp.ID != "cache-1" {
		t.Errorf("component.ID = %q, want %q", comp.ID, "cache-1")
	}
	if comp.Bytes != 1000 {
		t.Errorf("component.Bytes = %d, want 1000", comp.Bytes)
	}
	if comp.Category != UsageCategoryCaches {
		t.Errorf("component.Category = %d, want %d", comp.Category, UsageCategoryCaches)
	}
}

func TestUsageRegistry_GetComponent_NotFound(t *testing.T) {
	r := NewUsageRegistry()

	_, exists := r.GetComponent("nonexistent")
	if exists {
		t.Error("GetComponent() returned exists=true for nonexistent component")
	}
}

func TestUsageRegistry_AllCategories(t *testing.T) {
	r := NewUsageRegistry()

	r.Report("cache-1", UsageCategoryCaches, 1000)
	r.Report("pipeline-1", UsageCategoryPipelines, 500)

	cats := r.AllCategories()

	if cats[UsageCategoryCaches] != 1000 {
		t.Errorf("AllCategories()[Caches] = %d, want 1000", cats[UsageCategoryCaches])
	}
	if cats[UsageCategoryPipelines] != 500 {
		t.Errorf("AllCategories()[Pipelines] = %d, want 500", cats[UsageCategoryPipelines])
	}
}

func TestUsageRegistry_ConcurrentAccess(t *testing.T) {
	r := NewUsageRegistry()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id := "component-" + itoa(idx)
			r.Report(id, UsageCategoryCaches, int64(idx*100))
		}(i)
	}

	wg.Wait()

	if r.ComponentCount() != 100 {
		t.Errorf("ComponentCount() = %d, want 100", r.ComponentCount())
	}
}

func TestUsageRegistry_ConcurrentReportRelease(t *testing.T) {
	r := NewUsageRegistry()
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(idx int) {
			defer wg.Done()
			id := "component-" + itoa(idx)
			r.Report(id, UsageCategoryCaches, int64(idx*100))
		}(i)
		go func(idx int) {
			defer wg.Done()
			id := "component-" + itoa(idx)
			time.Sleep(time.Microsecond)
			r.Release(id)
		}(i)
	}

	wg.Wait()

	total := r.Total()
	count := r.ComponentCount()

	if total < 0 {
		t.Errorf("Total() = %d, should not be negative", total)
	}
	if count < 0 || count > 50 {
		t.Errorf("ComponentCount() = %d, should be between 0 and 50", count)
	}
}

func TestUsageRegistry_UpdatedAt(t *testing.T) {
	r := NewUsageRegistry()
	before := time.Now()

	r.Report("cache-1", UsageCategoryCaches, 1000)

	after := time.Now()

	comp, _ := r.GetComponent("cache-1")
	if comp.UpdatedAt.Before(before) || comp.UpdatedAt.After(after) {
		t.Errorf("UpdatedAt = %v, should be between %v and %v", comp.UpdatedAt, before, after)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}
