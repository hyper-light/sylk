package coldstart

import (
	"testing"
)

func TestNewCodebaseProfile(t *testing.T) {
	profile := NewCodebaseProfile()

	if profile.TotalNodes != 0 {
		t.Errorf("TotalNodes = %v, want 0", profile.TotalNodes)
	}
	if profile.EntityCounts == nil {
		t.Error("EntityCounts should be initialized")
	}
	if profile.DomainCounts == nil {
		t.Error("DomainCounts should be initialized")
	}
	if len(profile.EntityCounts) != 0 {
		t.Errorf("EntityCounts length = %v, want 0", len(profile.EntityCounts))
	}
	if len(profile.DomainCounts) != 0 {
		t.Errorf("DomainCounts length = %v, want 0", len(profile.DomainCounts))
	}
}

func TestCodebaseProfile_AddEntity(t *testing.T) {
	profile := NewCodebaseProfile()

	tests := []struct {
		entityType    string
		expectedTotal int
		expectedCount int
	}{
		{"function", 1, 1},
		{"function", 2, 2},
		{"type", 3, 1},
		{"function", 4, 3},
		{"method", 5, 1},
	}

	for _, tt := range tests {
		profile.AddEntity(tt.entityType)

		if profile.TotalNodes != tt.expectedTotal {
			t.Errorf("After adding %s: TotalNodes = %v, want %v",
				tt.entityType, profile.TotalNodes, tt.expectedTotal)
		}
		if profile.EntityCounts[tt.entityType] != tt.expectedCount {
			t.Errorf("After adding %s: EntityCounts[%s] = %v, want %v",
				tt.entityType, tt.entityType,
				profile.EntityCounts[tt.entityType], tt.expectedCount)
		}
	}
}

func TestCodebaseProfile_AddDomain(t *testing.T) {
	profile := NewCodebaseProfile()

	tests := []struct {
		domain        int
		expectedCount int
	}{
		{1, 1},
		{1, 2},
		{2, 1},
		{1, 3},
		{3, 1},
	}

	for _, tt := range tests {
		profile.AddDomain(tt.domain)

		if profile.DomainCounts[tt.domain] != tt.expectedCount {
			t.Errorf("After adding domain %d: DomainCounts[%d] = %v, want %v",
				tt.domain, tt.domain,
				profile.DomainCounts[tt.domain], tt.expectedCount)
		}
	}
}

func TestCodebaseProfile_ComputeAverages_EmptyProfile(t *testing.T) {
	profile := NewCodebaseProfile()
	profile.ComputeAverages()

	// Should not panic with zero nodes
	if profile.AvgInDegree != 0 {
		t.Errorf("AvgInDegree = %v, want 0", profile.AvgInDegree)
	}
	if profile.AvgOutDegree != 0 {
		t.Errorf("AvgOutDegree = %v, want 0", profile.AvgOutDegree)
	}
}

func TestCodebaseProfile_ComputeAverages(t *testing.T) {
	profile := NewCodebaseProfile()

	// Simulate accumulated totals
	profile.AddEntity("function")
	profile.AddEntity("type")
	profile.AddEntity("method")
	profile.AddEntity("function")
	profile.AddEntity("struct")

	// Set accumulated degree totals (sum of all nodes)
	profile.AvgInDegree = 25.0  // Total in-degrees
	profile.AvgOutDegree = 15.0 // Total out-degrees

	profile.ComputeAverages()

	expectedAvgIn := 25.0 / 5.0
	expectedAvgOut := 15.0 / 5.0

	if profile.AvgInDegree != expectedAvgIn {
		t.Errorf("AvgInDegree = %v, want %v", profile.AvgInDegree, expectedAvgIn)
	}
	if profile.AvgOutDegree != expectedAvgOut {
		t.Errorf("AvgOutDegree = %v, want %v", profile.AvgOutDegree, expectedAvgOut)
	}
}

func TestCodebaseProfile_FullWorkflow(t *testing.T) {
	profile := NewCodebaseProfile()

	// Add various entities
	entityTypes := []string{
		"function", "function", "function",
		"type", "type",
		"method", "method", "method", "method",
		"struct",
		"interface",
	}

	for _, et := range entityTypes {
		profile.AddEntity(et)
	}

	// Add domains
	for i := 0; i < 11; i++ {
		profile.AddDomain(i % 3) // Domains 0, 1, 2
	}

	// Verify counts
	if profile.TotalNodes != 11 {
		t.Errorf("TotalNodes = %v, want 11", profile.TotalNodes)
	}
	if profile.EntityCounts["function"] != 3 {
		t.Errorf("function count = %v, want 3", profile.EntityCounts["function"])
	}
	if profile.EntityCounts["method"] != 4 {
		t.Errorf("method count = %v, want 4", profile.EntityCounts["method"])
	}
	if profile.DomainCounts[0] != 4 {
		t.Errorf("domain 0 count = %v, want 4", profile.DomainCounts[0])
	}
	if profile.DomainCounts[1] != 4 {
		t.Errorf("domain 1 count = %v, want 4", profile.DomainCounts[1])
	}

	// Set statistics
	profile.AvgInDegree = 55.0
	profile.AvgOutDegree = 33.0
	profile.MaxPageRank = 0.95
	profile.PageRankSum = 5.5
	profile.BetweennessMax = 0.88

	profile.ComputeAverages()

	expectedAvgIn := 55.0 / 11.0
	expectedAvgOut := 33.0 / 11.0

	if profile.AvgInDegree != expectedAvgIn {
		t.Errorf("AvgInDegree = %v, want %v", profile.AvgInDegree, expectedAvgIn)
	}
	if profile.AvgOutDegree != expectedAvgOut {
		t.Errorf("AvgOutDegree = %v, want %v", profile.AvgOutDegree, expectedAvgOut)
	}
}
