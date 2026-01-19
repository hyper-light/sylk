package coldstart

import (
	"testing"
)

func TestDefaultColdPriorWeights(t *testing.T) {
	weights := DefaultColdPriorWeights()

	// Check category weights
	if weights.StructuralWeight != 0.40 {
		t.Errorf("StructuralWeight = %v, want 0.40", weights.StructuralWeight)
	}
	if weights.ContentWeight != 0.35 {
		t.Errorf("ContentWeight = %v, want 0.35", weights.ContentWeight)
	}
	if weights.DistributionalWeight != 0.25 {
		t.Errorf("DistributionalWeight = %v, want 0.25", weights.DistributionalWeight)
	}

	// Check structural sub-weights
	if weights.PageRankWeight != 0.50 {
		t.Errorf("PageRankWeight = %v, want 0.50", weights.PageRankWeight)
	}
	if weights.DegreeWeight != 0.30 {
		t.Errorf("DegreeWeight = %v, want 0.30", weights.DegreeWeight)
	}
	if weights.ClusterWeight != 0.20 {
		t.Errorf("ClusterWeight = %v, want 0.20", weights.ClusterWeight)
	}

	// Check content sub-weights
	if weights.EntityTypeWeight != 0.40 {
		t.Errorf("EntityTypeWeight = %v, want 0.40", weights.EntityTypeWeight)
	}
	if weights.NameWeight != 0.35 {
		t.Errorf("NameWeight = %v, want 0.35", weights.NameWeight)
	}
	if weights.DocWeight != 0.25 {
		t.Errorf("DocWeight = %v, want 0.25", weights.DocWeight)
	}

	// Check distributional sub-weights
	if weights.FrequencyWeight != 0.60 {
		t.Errorf("FrequencyWeight = %v, want 0.60", weights.FrequencyWeight)
	}
	if weights.RarityWeight != 0.40 {
		t.Errorf("RarityWeight = %v, want 0.40", weights.RarityWeight)
	}
}

func TestColdPriorWeights_Validate_DefaultWeights(t *testing.T) {
	weights := DefaultColdPriorWeights()

	err := weights.Validate()
	if err != nil {
		t.Errorf("Default weights should be valid, got error: %v", err)
	}
}

func TestColdPriorWeights_Validate_CategoryWeightsInvalid(t *testing.T) {
	tests := []struct {
		name       string
		structural float64
		content    float64
		distrib    float64
	}{
		{"sum too high", 0.50, 0.35, 0.25},
		{"sum too low", 0.30, 0.35, 0.25},
		{"all zeros", 0.0, 0.0, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			weights := DefaultColdPriorWeights()
			weights.StructuralWeight = tt.structural
			weights.ContentWeight = tt.content
			weights.DistributionalWeight = tt.distrib

			err := weights.Validate()
			if err == nil {
				t.Error("Expected error for invalid category weights, got nil")
			}
		})
	}
}

func TestColdPriorWeights_Validate_StructuralSubWeightsInvalid(t *testing.T) {
	tests := []struct {
		name      string
		pageRank  float64
		degree    float64
		cluster   float64
		wantError bool
	}{
		{"sum too high", 0.60, 0.30, 0.20, true},
		{"sum too low", 0.40, 0.30, 0.20, true},
		{"valid", 0.50, 0.30, 0.20, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			weights := DefaultColdPriorWeights()
			weights.PageRankWeight = tt.pageRank
			weights.DegreeWeight = tt.degree
			weights.ClusterWeight = tt.cluster

			err := weights.Validate()
			if tt.wantError && err == nil {
				t.Error("Expected error for invalid structural sub-weights, got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("Expected no error, got: %v", err)
			}
		})
	}
}

func TestColdPriorWeights_Validate_ContentSubWeightsInvalid(t *testing.T) {
	tests := []struct {
		name       string
		entityType float64
		nameWeight float64
		doc        float64
		wantError  bool
	}{
		{"sum too high", 0.50, 0.35, 0.25, true},
		{"sum too low", 0.30, 0.35, 0.25, true},
		{"valid", 0.40, 0.35, 0.25, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			weights := DefaultColdPriorWeights()
			weights.EntityTypeWeight = tt.entityType
			weights.NameWeight = tt.nameWeight
			weights.DocWeight = tt.doc

			err := weights.Validate()
			if tt.wantError && err == nil {
				t.Error("Expected error for invalid content sub-weights, got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("Expected no error, got: %v", err)
			}
		})
	}
}

func TestColdPriorWeights_Validate_DistributionalSubWeightsInvalid(t *testing.T) {
	tests := []struct {
		name      string
		frequency float64
		rarity    float64
		wantError bool
	}{
		{"sum too high", 0.70, 0.40, true},
		{"sum too low", 0.50, 0.40, true},
		{"valid", 0.60, 0.40, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			weights := DefaultColdPriorWeights()
			weights.FrequencyWeight = tt.frequency
			weights.RarityWeight = tt.rarity

			err := weights.Validate()
			if tt.wantError && err == nil {
				t.Error("Expected error for invalid distributional sub-weights, got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("Expected no error, got: %v", err)
			}
		})
	}
}

func TestApproxEqual(t *testing.T) {
	tests := []struct {
		name      string
		a         float64
		b         float64
		tolerance float64
		want      bool
	}{
		{"exact match", 1.0, 1.0, 0.01, true},
		{"within tolerance positive", 1.0, 1.005, 0.01, true},
		{"within tolerance negative", 1.0, 0.995, 0.01, true},
		{"outside tolerance", 1.0, 1.02, 0.01, false},
		{"zero values", 0.0, 0.0, 0.01, true},
		{"negative values", -1.0, -1.005, 0.01, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := approxEqual(tt.a, tt.b, tt.tolerance)
			if got != tt.want {
				t.Errorf("approxEqual(%v, %v, %v) = %v, want %v",
					tt.a, tt.b, tt.tolerance, got, tt.want)
			}
		})
	}
}

func TestColdPriorWeights_CustomWeights(t *testing.T) {
	weights := &ColdPriorWeights{
		StructuralWeight:     0.50,
		PageRankWeight:       0.60,
		DegreeWeight:         0.25,
		ClusterWeight:        0.15,
		ContentWeight:        0.30,
		EntityTypeWeight:     0.50,
		NameWeight:           0.30,
		DocWeight:            0.20,
		DistributionalWeight: 0.20,
		FrequencyWeight:      0.70,
		RarityWeight:         0.30,
	}

	err := weights.Validate()
	if err != nil {
		t.Errorf("Custom valid weights should pass validation, got error: %v", err)
	}
}
