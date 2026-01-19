package vectorgraphdb

import (
	"sync"
	"testing"
)

func TestDomainIndex_Index(t *testing.T) {
	di := NewDomainIndex()

	di.Index("vec1", DomainCode)
	di.Index("vec2", DomainCode)
	di.Index("vec3", DomainHistory)

	if count := di.CountByDomain(DomainCode); count != 2 {
		t.Errorf("Expected 2 vectors in Code domain, got %d", count)
	}

	if count := di.CountByDomain(DomainHistory); count != 1 {
		t.Errorf("Expected 1 vector in History domain, got %d", count)
	}
}

func TestDomainIndex_Index_ReplaceDomain(t *testing.T) {
	di := NewDomainIndex()

	di.Index("vec1", DomainCode)
	di.Index("vec1", DomainHistory)

	if count := di.CountByDomain(DomainCode); count != 0 {
		t.Errorf("Expected 0 vectors in Code domain after replace, got %d", count)
	}

	if count := di.CountByDomain(DomainHistory); count != 1 {
		t.Errorf("Expected 1 vector in History domain, got %d", count)
	}

	domain, ok := di.GetDomain("vec1")
	if !ok || domain != DomainHistory {
		t.Errorf("Expected vec1 to be in History domain, got %v", domain)
	}
}

func TestDomainIndex_Remove(t *testing.T) {
	di := NewDomainIndex()

	di.Index("vec1", DomainCode)
	di.Index("vec2", DomainCode)
	di.Remove("vec1")

	if count := di.CountByDomain(DomainCode); count != 1 {
		t.Errorf("Expected 1 vector after removal, got %d", count)
	}

	_, ok := di.GetDomain("vec1")
	if ok {
		t.Error("vec1 should not exist after removal")
	}
}

func TestDomainIndex_Remove_NonExistent(t *testing.T) {
	di := NewDomainIndex()

	di.Remove("nonexistent")

	if di.TotalCount() != 0 {
		t.Error("Should handle removal of non-existent gracefully")
	}
}

func TestDomainIndex_GetVectorsByDomain(t *testing.T) {
	di := NewDomainIndex()

	di.Index("vec1", DomainCode)
	di.Index("vec2", DomainCode)
	di.Index("vec3", DomainHistory)

	codeVectors := di.GetVectorsByDomain(DomainCode)
	if len(codeVectors) != 2 {
		t.Errorf("Expected 2 Code vectors, got %d", len(codeVectors))
	}

	historyVectors := di.GetVectorsByDomain(DomainHistory)
	if len(historyVectors) != 1 {
		t.Errorf("Expected 1 History vector, got %d", len(historyVectors))
	}

	academicVectors := di.GetVectorsByDomain(DomainAcademic)
	if academicVectors != nil {
		t.Error("Expected nil for empty domain")
	}
}

func TestDomainIndex_GetDomain(t *testing.T) {
	di := NewDomainIndex()

	di.Index("vec1", DomainCode)

	domain, ok := di.GetDomain("vec1")
	if !ok {
		t.Error("Should find vec1")
	}
	if domain != DomainCode {
		t.Errorf("Expected Code domain, got %v", domain)
	}

	_, ok = di.GetDomain("nonexistent")
	if ok {
		t.Error("Should not find nonexistent vector")
	}
}

func TestDomainIndex_ContainsDomain(t *testing.T) {
	di := NewDomainIndex()

	di.Index("vec1", DomainCode)

	if !di.ContainsDomain(DomainCode) {
		t.Error("Should contain Code domain")
	}

	if di.ContainsDomain(DomainHistory) {
		t.Error("Should not contain History domain")
	}
}

func TestDomainIndex_TotalCount(t *testing.T) {
	di := NewDomainIndex()

	if di.TotalCount() != 0 {
		t.Error("New index should have 0 count")
	}

	di.Index("vec1", DomainCode)
	di.Index("vec2", DomainHistory)
	di.Index("vec3", DomainAcademic)

	if count := di.TotalCount(); count != 3 {
		t.Errorf("Expected 3 total, got %d", count)
	}
}

func TestDomainIndex_Stats(t *testing.T) {
	di := NewDomainIndex()

	di.Index("vec1", DomainCode)
	di.Index("vec2", DomainCode)
	di.Index("vec3", DomainHistory)

	stats := di.Stats()

	if stats[DomainCode] != 2 {
		t.Errorf("Expected 2 in Code stats, got %d", stats[DomainCode])
	}

	if stats[DomainHistory] != 1 {
		t.Errorf("Expected 1 in History stats, got %d", stats[DomainHistory])
	}
}

func TestDomainIndex_FilterVectorIDs(t *testing.T) {
	di := NewDomainIndex()

	di.Index("vec1", DomainCode)
	di.Index("vec2", DomainHistory)
	di.Index("vec3", DomainAcademic)

	ids := []string{"vec1", "vec2", "vec3", "vec4"}
	filtered := di.FilterVectorIDs(ids, []Domain{DomainCode, DomainHistory})

	if len(filtered) != 2 {
		t.Errorf("Expected 2 filtered IDs, got %d", len(filtered))
	}
}

func TestDomainIndex_FilterVectorIDs_EmptyAllowed(t *testing.T) {
	di := NewDomainIndex()

	di.Index("vec1", DomainCode)

	ids := []string{"vec1", "vec2"}
	filtered := di.FilterVectorIDs(ids, nil)

	if len(filtered) != len(ids) {
		t.Error("Empty allowed list should return all IDs")
	}
}

func TestDomainIndex_GetVectorsByDomains(t *testing.T) {
	di := NewDomainIndex()

	di.Index("vec1", DomainCode)
	di.Index("vec2", DomainHistory)
	di.Index("vec3", DomainAcademic)

	vectors := di.GetVectorsByDomains([]Domain{DomainCode, DomainHistory})

	if len(vectors) != 2 {
		t.Errorf("Expected 2 vectors, got %d", len(vectors))
	}
}

func TestDomainIndex_GetVectorsByDomains_Empty(t *testing.T) {
	di := NewDomainIndex()

	di.Index("vec1", DomainCode)

	vectors := di.GetVectorsByDomains(nil)
	if vectors != nil {
		t.Error("Expected nil for empty domains")
	}
}

func TestDomainIndex_BatchIndex(t *testing.T) {
	di := NewDomainIndex()

	entries := []DomainIndexEntry{
		{VectorID: "vec1", Domain: DomainCode},
		{VectorID: "vec2", Domain: DomainCode},
		{VectorID: "vec3", Domain: DomainHistory},
	}

	di.BatchIndex(entries)

	if di.TotalCount() != 3 {
		t.Errorf("Expected 3 total after batch, got %d", di.TotalCount())
	}

	if di.CountByDomain(DomainCode) != 2 {
		t.Errorf("Expected 2 in Code domain after batch, got %d", di.CountByDomain(DomainCode))
	}
}

func TestDomainIndex_BatchRemove(t *testing.T) {
	di := NewDomainIndex()

	di.Index("vec1", DomainCode)
	di.Index("vec2", DomainCode)
	di.Index("vec3", DomainHistory)

	di.BatchRemove([]string{"vec1", "vec2"})

	if di.TotalCount() != 1 {
		t.Errorf("Expected 1 total after batch remove, got %d", di.TotalCount())
	}

	if di.CountByDomain(DomainCode) != 0 {
		t.Errorf("Expected 0 in Code domain after batch remove, got %d", di.CountByDomain(DomainCode))
	}
}

func TestDomainIndex_Clear(t *testing.T) {
	di := NewDomainIndex()

	di.Index("vec1", DomainCode)
	di.Index("vec2", DomainHistory)
	di.Clear()

	if di.TotalCount() != 0 {
		t.Errorf("Expected 0 after clear, got %d", di.TotalCount())
	}

	if len(di.Stats()) != 0 {
		t.Error("Stats should be empty after clear")
	}
}

func TestDomainIndex_Rebuild(t *testing.T) {
	di := NewDomainIndex()

	di.Index("old1", DomainCode)

	vectors := []*VectorData{
		{NodeID: "vec1", Domain: DomainCode},
		{NodeID: "vec2", Domain: DomainHistory},
		{NodeID: "vec3", Domain: DomainAcademic},
	}

	di.Rebuild(vectors)

	if di.TotalCount() != 3 {
		t.Errorf("Expected 3 after rebuild, got %d", di.TotalCount())
	}

	_, ok := di.GetDomain("old1")
	if ok {
		t.Error("Old entries should be cleared on rebuild")
	}
}

func TestDomainIndex_ExistingDomains(t *testing.T) {
	di := NewDomainIndex()

	di.Index("vec1", DomainCode)
	di.Index("vec2", DomainHistory)

	domains := di.ExistingDomains()

	if len(domains) != 2 {
		t.Errorf("Expected 2 existing domains, got %d", len(domains))
	}

	hasCode := false
	hasHistory := false
	for _, d := range domains {
		if d == DomainCode {
			hasCode = true
		}
		if d == DomainHistory {
			hasHistory = true
		}
	}

	if !hasCode || !hasHistory {
		t.Error("Missing expected domains in ExistingDomains")
	}
}

func TestDomainIndex_Concurrent(t *testing.T) {
	di := NewDomainIndex()

	var wg sync.WaitGroup
	numGoroutines := 10
	opsPerGoroutine := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				vectorID := "vec_" + string(rune('A'+id)) + "_" + string(rune('0'+j%10))
				domain := Domain(j % 3)
				di.Index(vectorID, domain)
			}
		}(i)
	}

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				di.GetVectorsByDomain(Domain(j % 3))
				di.Stats()
				di.TotalCount()
			}
		}(i)
	}

	wg.Wait()

	stats := di.Stats()
	totalFromStats := 0
	for _, count := range stats {
		totalFromStats += count
	}

	if totalFromStats != di.TotalCount() {
		t.Errorf("Stats total %d != TotalCount %d", totalFromStats, di.TotalCount())
	}
}

func TestDomainIndexEntry(t *testing.T) {
	entry := DomainIndexEntry{
		VectorID: "vec1",
		Domain:   DomainCode,
	}

	if entry.VectorID != "vec1" {
		t.Error("VectorID mismatch")
	}
	if entry.Domain != DomainCode {
		t.Error("Domain mismatch")
	}
}
