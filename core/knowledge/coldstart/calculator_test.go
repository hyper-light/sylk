package coldstart

import (
	"math"
	"testing"
)

func TestNewColdPriorCalculator(t *testing.T) {
	profile := NewCodebaseProfile()
	weights := DefaultColdPriorWeights()

	calc := NewColdPriorCalculator(profile, weights)

	if calc.profile != profile {
		t.Error("Profile not set correctly")
	}
	if calc.weights != weights {
		t.Error("Weights not set correctly")
	}
	if calc.entityBasePriors == nil {
		t.Error("Entity base priors not initialized")
	}
	if len(calc.entityBasePriors) == 0 {
		t.Error("Entity base priors should not be empty")
	}
}

func TestDefaultEntityBasePriors(t *testing.T) {
	priors := defaultEntityBasePriors()

	expectedPriors := map[string]float64{
		"function":  0.50,
		"type":      0.45,
		"method":    0.50,
		"struct":    0.45,
		"interface": 0.40,
		"variable":  0.30,
		"constant":  0.25,
		"import":    0.20,
		"package":   0.35,
		"file":      0.15,
	}

	for entityType, expectedPrior := range expectedPriors {
		if priors[entityType] != expectedPrior {
			t.Errorf("Base prior for %s = %v, want %v",
				entityType, priors[entityType], expectedPrior)
		}
	}
}

func TestClamp(t *testing.T) {
	tests := []struct {
		name  string
		value float64
		min   float64
		max   float64
		want  float64
	}{
		{"within range", 0.5, 0.0, 1.0, 0.5},
		{"below min", -0.1, 0.0, 1.0, 0.0},
		{"above max", 1.5, 0.0, 1.0, 1.0},
		{"at min", 0.0, 0.0, 1.0, 0.0},
		{"at max", 1.0, 0.0, 1.0, 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clamp(tt.value, tt.min, tt.max)
			if got != tt.want {
				t.Errorf("clamp(%v, %v, %v) = %v, want %v",
					tt.value, tt.min, tt.max, got, tt.want)
			}
		})
	}
}

func TestColdPriorCalculator_ComputeColdPrior_ZeroSignals(t *testing.T) {
	profile := NewCodebaseProfile()
	weights := DefaultColdPriorWeights()
	calc := NewColdPriorCalculator(profile, weights)

	signals := NewNodeColdStartSignals("node1", "function", 1)

	prior := calc.ComputeColdPrior(signals)

	// Should return a valid prior in [0,1]
	if prior < 0.0 || prior > 1.0 {
		t.Errorf("Prior = %v, should be in [0,1]", prior)
	}
}

func TestColdPriorCalculator_ComputeColdPrior_BasicScenario(t *testing.T) {
	// Setup profile
	profile := NewCodebaseProfile()
	profile.AddEntity("function")
	profile.AddEntity("type")
	profile.AvgInDegree = 10.0
	profile.AvgOutDegree = 8.0
	profile.MaxPageRank = 1.0
	profile.ComputeAverages()

	weights := DefaultColdPriorWeights()
	calc := NewColdPriorCalculator(profile, weights)

	// Create signals with realistic values
	signals := NewNodeColdStartSignals("node1", "function", 1)
	signals.InDegree = 5
	signals.OutDegree = 3
	signals.PageRank = 0.45
	signals.ClusterCoeff = 0.60
	signals.Betweenness = 0.20
	signals.NameSalience = 0.75
	signals.DocCoverage = 0.80
	signals.Complexity = 0.40
	signals.TypeFrequency = 0.50
	signals.TypeRarity = 0.50

	prior := calc.ComputeColdPrior(signals)

	// Should return a valid prior in [0,1]
	if prior < 0.0 || prior > 1.0 {
		t.Errorf("Prior = %v, should be in [0,1]", prior)
	}

	// With these signals, prior should be reasonably high (> 0.4)
	if prior < 0.4 {
		t.Errorf("Prior = %v, expected > 0.4 for high-quality signals", prior)
	}
}

func TestColdPriorCalculator_ComputeColdPrior_HighImportanceNode(t *testing.T) {
	// Setup profile
	profile := NewCodebaseProfile()
	for i := 0; i < 10; i++ {
		profile.AddEntity("function")
	}
	profile.AvgInDegree = 50.0
	profile.AvgOutDegree = 40.0
	profile.MaxPageRank = 1.0
	profile.ComputeAverages()

	weights := DefaultColdPriorWeights()
	calc := NewColdPriorCalculator(profile, weights)

	// Create signals for high-importance node
	signals := NewNodeColdStartSignals("important_node", "function", 1)
	signals.InDegree = 20
	signals.OutDegree = 15
	signals.PageRank = 0.90
	signals.ClusterCoeff = 0.85
	signals.Betweenness = 0.75
	signals.NameSalience = 0.95
	signals.DocCoverage = 0.98
	signals.Complexity = 0.70
	signals.TypeFrequency = 0.80
	signals.TypeRarity = 0.20

	prior := calc.ComputeColdPrior(signals)

	// High-importance node should have high prior
	if prior < 0.6 {
		t.Errorf("Prior = %v, expected > 0.6 for high-importance node", prior)
	}
	if prior > 1.0 {
		t.Errorf("Prior = %v, should not exceed 1.0", prior)
	}
}

func TestColdPriorCalculator_ComputeColdPrior_LowImportanceNode(t *testing.T) {
	// Setup profile
	profile := NewCodebaseProfile()
	for i := 0; i < 10; i++ {
		profile.AddEntity("variable")
	}
	profile.AvgInDegree = 30.0
	profile.AvgOutDegree = 25.0
	profile.MaxPageRank = 1.0
	profile.ComputeAverages()

	weights := DefaultColdPriorWeights()
	calc := NewColdPriorCalculator(profile, weights)

	// Create signals for low-importance node
	signals := NewNodeColdStartSignals("minor_node", "variable", 1)
	signals.InDegree = 1
	signals.OutDegree = 0
	signals.PageRank = 0.05
	signals.ClusterCoeff = 0.10
	signals.Betweenness = 0.02
	signals.NameSalience = 0.20
	signals.DocCoverage = 0.15
	signals.Complexity = 0.10
	signals.TypeFrequency = 0.15
	signals.TypeRarity = 0.85

	prior := calc.ComputeColdPrior(signals)

	// Low-importance node should have lower prior
	if prior < 0.0 {
		t.Errorf("Prior = %v, should not be negative", prior)
	}
	if prior > 0.5 {
		t.Errorf("Prior = %v, expected < 0.5 for low-importance node", prior)
	}
}

func TestColdPriorCalculator_ComputeStructuralScore(t *testing.T) {
	profile := NewCodebaseProfile()
	profile.AvgInDegree = 10.0
	profile.AvgOutDegree = 8.0
	profile.MaxPageRank = 1.0

	weights := DefaultColdPriorWeights()
	calc := NewColdPriorCalculator(profile, weights)

	signals := NewNodeColdStartSignals("node1", "function", 1)
	signals.InDegree = 5
	signals.OutDegree = 4
	signals.PageRank = 0.50
	signals.ClusterCoeff = 0.60
	signals.Betweenness = 0.30

	score := calc.computeStructuralScore(signals)

	// Score should be in [0,1]
	if score < 0.0 || score > 1.0 {
		t.Errorf("Structural score = %v, should be in [0,1]", score)
	}
}

func TestColdPriorCalculator_ComputeContentScore(t *testing.T) {
	profile := NewCodebaseProfile()
	weights := DefaultColdPriorWeights()
	calc := NewColdPriorCalculator(profile, weights)

	tests := []struct {
		name       string
		entityType string
	}{
		{"known type function", "function"},
		{"known type method", "method"},
		{"unknown type", "unknown_entity"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			signals := NewNodeColdStartSignals("node1", tt.entityType, 1)
			signals.NameSalience = 0.70
			signals.DocCoverage = 0.80
			signals.Complexity = 0.40

			score := calc.computeContentScore(signals)

			// Score should be in [0,1]
			if score < 0.0 || score > 1.0 {
				t.Errorf("Content score = %v, should be in [0,1]", score)
			}
		})
	}
}

func TestColdPriorCalculator_ComputeDistributionalScore(t *testing.T) {
	profile := NewCodebaseProfile()
	weights := DefaultColdPriorWeights()
	calc := NewColdPriorCalculator(profile, weights)

	signals := NewNodeColdStartSignals("node1", "function", 1)
	signals.TypeFrequency = 0.60
	signals.TypeRarity = 0.40

	score := calc.computeDistributionalScore(signals)

	// Score should be in [0,1]
	if score < 0.0 || score > 1.0 {
		t.Errorf("Distributional score = %v, should be in [0,1]", score)
	}

	// With these weights (freq: 0.60, rarity: 0.40)
	// Score should be 0.60*0.60 + 0.40*0.40 = 0.52
	expected := 0.52
	if math.Abs(score-expected) > 0.001 {
		t.Errorf("Distributional score = %v, want %v", score, expected)
	}
}

func TestColdPriorCalculator_DifferentEntityTypes(t *testing.T) {
	profile := NewCodebaseProfile()
	profile.MaxPageRank = 1.0
	profile.AvgInDegree = 10.0
	profile.AvgOutDegree = 8.0

	weights := DefaultColdPriorWeights()
	calc := NewColdPriorCalculator(profile, weights)

	// Set identical signals for different entity types
	entityTypes := []string{
		"function", "type", "method", "struct", "interface",
		"variable", "constant", "import", "package", "file",
	}

	for _, entityType := range entityTypes {
		signals := NewNodeColdStartSignals("node", entityType, 1)
		signals.PageRank = 0.50
		signals.InDegree = 5
		signals.OutDegree = 5
		signals.ClusterCoeff = 0.50
		signals.NameSalience = 0.50
		signals.DocCoverage = 0.50
		signals.TypeFrequency = 0.50
		signals.TypeRarity = 0.50

		prior := calc.ComputeColdPrior(signals)

		// All priors should be valid
		if prior < 0.0 || prior > 1.0 {
			t.Errorf("Prior for %s = %v, should be in [0,1]", entityType, prior)
		}
	}
}

func TestColdPriorCalculator_EdgeCases(t *testing.T) {
	profile := NewCodebaseProfile()
	weights := DefaultColdPriorWeights()
	calc := NewColdPriorCalculator(profile, weights)

	tests := []struct {
		name   string
		setup  func(*NodeColdStartSignals)
		verify func(float64) bool
	}{
		{
			name: "all zeros",
			setup: func(s *NodeColdStartSignals) {
				// All fields already zero
			},
			verify: func(prior float64) bool {
				return prior >= 0.0 && prior <= 1.0
			},
		},
		{
			name: "all max values",
			setup: func(s *NodeColdStartSignals) {
				s.InDegree = 1000
				s.OutDegree = 1000
				s.PageRank = 1.0
				s.ClusterCoeff = 1.0
				s.Betweenness = 1.0
				s.NameSalience = 1.0
				s.DocCoverage = 1.0
				s.Complexity = 1.0
				s.TypeFrequency = 1.0
				s.TypeRarity = 1.0
			},
			verify: func(prior float64) bool {
				return prior >= 0.0 && prior <= 1.0
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			signals := NewNodeColdStartSignals("node", "function", 1)
			tt.setup(signals)

			prior := calc.ComputeColdPrior(signals)

			if !tt.verify(prior) {
				t.Errorf("Prior = %v, failed verification", prior)
			}
		})
	}
}
