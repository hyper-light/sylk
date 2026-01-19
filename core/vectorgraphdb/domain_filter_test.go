package vectorgraphdb

import (
	"testing"
)

func TestDomainFilter_Allow_EmptyFilter(t *testing.T) {
	f := NewDomainFilter(nil)

	for _, d := range ValidDomains() {
		if !f.Allow(d) {
			t.Errorf("Empty filter should allow all domains, but rejected %s", d)
		}
	}
}

func TestDomainFilter_Allow_WithAllowList(t *testing.T) {
	f := NewDomainFilter(&DomainFilterConfig{
		AllowedDomains: []Domain{DomainCode, DomainHistory},
	})

	if !f.Allow(DomainCode) {
		t.Error("Should allow DomainCode")
	}
	if !f.Allow(DomainHistory) {
		t.Error("Should allow DomainHistory")
	}
	if f.Allow(DomainAcademic) {
		t.Error("Should not allow DomainAcademic")
	}
}

func TestDomainFilter_Allow_WithExcludeList(t *testing.T) {
	f := NewDomainFilter(&DomainFilterConfig{
		ExcludeDomains: []Domain{DomainAcademic},
	})

	if !f.Allow(DomainCode) {
		t.Error("Should allow DomainCode")
	}
	if f.Allow(DomainAcademic) {
		t.Error("Should not allow DomainAcademic (excluded)")
	}
}

func TestDomainFilter_Allow_ExcludeTakesPrecedence(t *testing.T) {
	f := NewDomainFilter(&DomainFilterConfig{
		AllowedDomains: []Domain{DomainCode, DomainAcademic},
		ExcludeDomains: []Domain{DomainAcademic},
	})

	if !f.Allow(DomainCode) {
		t.Error("Should allow DomainCode")
	}
	if f.Allow(DomainAcademic) {
		t.Error("Exclude should take precedence over allow")
	}
}

func TestDomainFilter_AllowNode(t *testing.T) {
	f := NewDomainFilter(&DomainFilterConfig{
		AllowedDomains: []Domain{DomainCode},
	})

	codeNode := &GraphNode{ID: "1", Domain: DomainCode}
	historyNode := &GraphNode{ID: "2", Domain: DomainHistory}

	if !f.AllowNode(codeNode) {
		t.Error("Should allow code node")
	}
	if f.AllowNode(historyNode) {
		t.Error("Should not allow history node")
	}
	if f.AllowNode(nil) {
		t.Error("Should not allow nil node")
	}
}

func TestDomainFilter_AllowVector(t *testing.T) {
	f := NewDomainFilter(&DomainFilterConfig{
		AllowedDomains: []Domain{DomainAcademic},
	})

	academicVec := &VectorData{NodeID: "1", Domain: DomainAcademic}
	codeVec := &VectorData{NodeID: "2", Domain: DomainCode}

	if !f.AllowVector(academicVec) {
		t.Error("Should allow academic vector")
	}
	if f.AllowVector(codeVec) {
		t.Error("Should not allow code vector")
	}
	if f.AllowVector(nil) {
		t.Error("Should not allow nil vector")
	}
}

func TestDomainFilter_FilterNodes(t *testing.T) {
	f := NewDomainFilter(&DomainFilterConfig{
		AllowedDomains: []Domain{DomainCode, DomainHistory},
	})

	nodes := []*GraphNode{
		{ID: "1", Domain: DomainCode},
		{ID: "2", Domain: DomainHistory},
		{ID: "3", Domain: DomainAcademic},
		{ID: "4", Domain: DomainCode},
	}

	filtered := f.FilterNodes(nodes)

	if len(filtered) != 3 {
		t.Errorf("Expected 3 filtered nodes, got %d", len(filtered))
	}

	for _, n := range filtered {
		if n.Domain == DomainAcademic {
			t.Error("Filtered result should not contain Academic nodes")
		}
	}
}

func TestDomainFilter_FilterNodes_EmptyFilter(t *testing.T) {
	f := NewDomainFilter(nil)

	nodes := []*GraphNode{
		{ID: "1", Domain: DomainCode},
		{ID: "2", Domain: DomainHistory},
	}

	filtered := f.FilterNodes(nodes)

	if len(filtered) != len(nodes) {
		t.Error("Empty filter should return all nodes")
	}
}

func TestDomainFilter_FilterVectors(t *testing.T) {
	f := NewDomainFilter(&DomainFilterConfig{
		AllowedDomains: []Domain{DomainAcademic},
	})

	vectors := []*VectorData{
		{NodeID: "1", Domain: DomainCode},
		{NodeID: "2", Domain: DomainAcademic},
		{NodeID: "3", Domain: DomainHistory},
	}

	filtered := f.FilterVectors(vectors)

	if len(filtered) != 1 {
		t.Errorf("Expected 1 filtered vector, got %d", len(filtered))
	}
	if filtered[0].Domain != DomainAcademic {
		t.Error("Filtered vector should be Academic")
	}
}

func TestDomainFilter_FilterSearchResults(t *testing.T) {
	f := NewDomainFilter(&DomainFilterConfig{
		AllowedDomains: []Domain{DomainCode},
	})

	results := []SearchResult{
		{Node: &GraphNode{ID: "1", Domain: DomainCode}, Similarity: 0.9},
		{Node: &GraphNode{ID: "2", Domain: DomainHistory}, Similarity: 0.8},
		{Node: &GraphNode{ID: "3", Domain: DomainCode}, Similarity: 0.7},
	}

	filtered := f.FilterSearchResults(results)

	if len(filtered) != 2 {
		t.Errorf("Expected 2 filtered results, got %d", len(filtered))
	}
}

func TestDomainFilter_FilterHybridResults(t *testing.T) {
	f := NewDomainFilter(&DomainFilterConfig{
		ExcludeDomains: []Domain{DomainHistory},
	})

	results := []HybridResult{
		{Node: &GraphNode{ID: "1", Domain: DomainCode}, CombinedScore: 0.9},
		{Node: &GraphNode{ID: "2", Domain: DomainHistory}, CombinedScore: 0.8},
		{Node: &GraphNode{ID: "3", Domain: DomainAcademic}, CombinedScore: 0.7},
	}

	filtered := f.FilterHybridResults(results)

	if len(filtered) != 2 {
		t.Errorf("Expected 2 filtered results, got %d", len(filtered))
	}

	for _, r := range filtered {
		if r.Node.Domain == DomainHistory {
			t.Error("Should not contain History domain")
		}
	}
}

func TestDomainFilter_AllowedDomains(t *testing.T) {
	f := NewDomainFilter(&DomainFilterConfig{
		AllowedDomains: []Domain{DomainCode, DomainAcademic},
	})

	allowed := f.AllowedDomains()

	if len(allowed) != 2 {
		t.Errorf("Expected 2 allowed domains, got %d", len(allowed))
	}
}

func TestDomainFilter_AllowedDomains_Empty(t *testing.T) {
	f := NewDomainFilter(nil)

	allowed := f.AllowedDomains()

	if allowed != nil {
		t.Error("Expected nil for empty allowed domains")
	}
}

func TestDomainFilter_ExcludedDomains(t *testing.T) {
	f := NewDomainFilter(&DomainFilterConfig{
		ExcludeDomains: []Domain{DomainHistory},
	})

	excluded := f.ExcludedDomains()

	if len(excluded) != 1 {
		t.Errorf("Expected 1 excluded domain, got %d", len(excluded))
	}
}

func TestDomainFilter_IsEmpty(t *testing.T) {
	empty := NewDomainFilter(nil)
	if !empty.IsEmpty() {
		t.Error("New filter should be empty")
	}

	withAllowed := NewDomainFilter(&DomainFilterConfig{
		AllowedDomains: []Domain{DomainCode},
	})
	if withAllowed.IsEmpty() {
		t.Error("Filter with allowed domains should not be empty")
	}

	withExcluded := NewDomainFilter(&DomainFilterConfig{
		ExcludeDomains: []Domain{DomainCode},
	})
	if withExcluded.IsEmpty() {
		t.Error("Filter with excluded domains should not be empty")
	}
}

func TestDomainFilter_AddAllowed(t *testing.T) {
	f := NewDomainFilter(nil)

	f.AddAllowed(DomainCode, DomainHistory)

	if !f.Allow(DomainCode) {
		t.Error("Should allow DomainCode after adding")
	}
	if !f.Allow(DomainHistory) {
		t.Error("Should allow DomainHistory after adding")
	}
	if f.Allow(DomainAcademic) {
		t.Error("Should not allow DomainAcademic (not added)")
	}
}

func TestDomainFilter_AddExcluded(t *testing.T) {
	f := NewDomainFilter(nil)

	f.AddExcluded(DomainAcademic)

	if !f.Allow(DomainCode) {
		t.Error("Should allow DomainCode")
	}
	if f.Allow(DomainAcademic) {
		t.Error("Should not allow DomainAcademic after excluding")
	}
}

func TestDomainFilter_RemoveAllowed(t *testing.T) {
	f := NewDomainFilter(&DomainFilterConfig{
		AllowedDomains: []Domain{DomainCode, DomainHistory},
	})

	f.RemoveAllowed(DomainHistory)

	if !f.Allow(DomainCode) {
		t.Error("Should still allow DomainCode")
	}
	if f.Allow(DomainHistory) {
		t.Error("Should not allow DomainHistory after removal")
	}
}

func TestDomainFilter_RemoveExcluded(t *testing.T) {
	f := NewDomainFilter(&DomainFilterConfig{
		ExcludeDomains: []Domain{DomainAcademic, DomainHistory},
	})

	f.RemoveExcluded(DomainAcademic)

	if !f.Allow(DomainAcademic) {
		t.Error("Should allow DomainAcademic after removal from excluded")
	}
	if f.Allow(DomainHistory) {
		t.Error("Should still exclude DomainHistory")
	}
}

func TestDomainFilter_Clear(t *testing.T) {
	f := NewDomainFilter(&DomainFilterConfig{
		AllowedDomains: []Domain{DomainCode},
		ExcludeDomains: []Domain{DomainHistory},
	})

	f.Clear()

	if !f.IsEmpty() {
		t.Error("Filter should be empty after clear")
	}
	if !f.Allow(DomainHistory) {
		t.Error("Should allow all domains after clear")
	}
}

func TestDomainFilter_Clone(t *testing.T) {
	f := NewDomainFilter(&DomainFilterConfig{
		AllowedDomains: []Domain{DomainCode},
		ExcludeDomains: []Domain{DomainHistory},
	})

	clone := f.Clone()

	if !clone.Allow(DomainCode) {
		t.Error("Clone should allow DomainCode")
	}
	if clone.Allow(DomainHistory) {
		t.Error("Clone should exclude DomainHistory")
	}

	f.AddAllowed(DomainAcademic)
	if clone.Allow(DomainAcademic) {
		t.Error("Clone should be independent from original")
	}
}

func TestAllowAllDomains(t *testing.T) {
	f := AllowAllDomains()

	for _, d := range ValidDomains() {
		if !f.Allow(d) {
			t.Errorf("AllowAllDomains should allow %s", d)
		}
	}
}

func TestAllowKnowledgeDomains(t *testing.T) {
	f := AllowKnowledgeDomains()

	for _, d := range KnowledgeDomains() {
		if !f.Allow(d) {
			t.Errorf("Should allow knowledge domain %s", d)
		}
	}

	for _, d := range PipelineDomains() {
		if f.Allow(d) {
			t.Errorf("Should not allow pipeline domain %s", d)
		}
	}
}

func TestAllowPipelineDomains(t *testing.T) {
	f := AllowPipelineDomains()

	for _, d := range PipelineDomains() {
		if !f.Allow(d) {
			t.Errorf("Should allow pipeline domain %s", d)
		}
	}

	for _, d := range KnowledgeDomains() {
		if f.Allow(d) {
			t.Errorf("Should not allow knowledge domain %s", d)
		}
	}
}

func TestAllowControlDomains(t *testing.T) {
	f := AllowControlDomains()

	for _, d := range ControlDomains() {
		if !f.Allow(d) {
			t.Errorf("Should allow control domain %s", d)
		}
	}

	if f.Allow(DomainCode) {
		t.Error("Should not allow DomainCode")
	}
}

func TestAllowSingleDomain(t *testing.T) {
	f := AllowSingleDomain(DomainAcademic)

	if !f.Allow(DomainAcademic) {
		t.Error("Should allow DomainAcademic")
	}
	if f.Allow(DomainCode) {
		t.Error("Should not allow DomainCode")
	}
	if f.Allow(DomainHistory) {
		t.Error("Should not allow DomainHistory")
	}
}

func TestExcludeSingleDomain(t *testing.T) {
	f := ExcludeSingleDomain(DomainHistory)

	if !f.Allow(DomainCode) {
		t.Error("Should allow DomainCode")
	}
	if !f.Allow(DomainAcademic) {
		t.Error("Should allow DomainAcademic")
	}
	if f.Allow(DomainHistory) {
		t.Error("Should not allow DomainHistory")
	}
}
