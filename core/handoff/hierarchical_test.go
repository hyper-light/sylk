package handoff

import (
	"encoding/json"
	"math"
	"testing"
)

// =============================================================================
// Test BlendLevel
// =============================================================================

func TestBlendLevel_String(t *testing.T) {
	tests := []struct {
		level    BlendLevel
		expected string
	}{
		{LevelInstance, "Instance"},
		{LevelAgentModel, "AgentModel"},
		{LevelModel, "Model"},
		{LevelGlobal, "Global"},
		{BlendLevel(99), "Unknown(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := tt.level.String()
			if result != tt.expected {
				t.Errorf("String() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestBlendLevel_Order(t *testing.T) {
	// Verify the hierarchy order: Instance < AgentModel < Model < Global
	if LevelInstance >= LevelAgentModel {
		t.Error("LevelInstance should be less than LevelAgentModel")
	}
	if LevelAgentModel >= LevelModel {
		t.Error("LevelAgentModel should be less than LevelModel")
	}
	if LevelModel >= LevelGlobal {
		t.Error("LevelModel should be less than LevelGlobal")
	}
}

// =============================================================================
// Test BlendedParams
// =============================================================================

func TestBlendedParams_JSONMarshal(t *testing.T) {
	original := &BlendedParams{
		Value:               0.75,
		InstanceWeight:      0.4,
		AgentModelWeight:    0.3,
		ModelWeight:         0.2,
		GlobalWeight:        0.1,
		EffectiveConfidence: 0.85,
		DominantLevel:       LevelInstance,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded BlendedParams
	err = json.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if math.Abs(decoded.Value-original.Value) > 1e-9 {
		t.Errorf("Value mismatch: got %v, want %v", decoded.Value, original.Value)
	}
	if math.Abs(decoded.InstanceWeight-original.InstanceWeight) > 1e-9 {
		t.Errorf("InstanceWeight mismatch: got %v, want %v",
			decoded.InstanceWeight, original.InstanceWeight)
	}
	if math.Abs(decoded.AgentModelWeight-original.AgentModelWeight) > 1e-9 {
		t.Errorf("AgentModelWeight mismatch: got %v, want %v",
			decoded.AgentModelWeight, original.AgentModelWeight)
	}
	if math.Abs(decoded.ModelWeight-original.ModelWeight) > 1e-9 {
		t.Errorf("ModelWeight mismatch: got %v, want %v",
			decoded.ModelWeight, original.ModelWeight)
	}
	if math.Abs(decoded.GlobalWeight-original.GlobalWeight) > 1e-9 {
		t.Errorf("GlobalWeight mismatch: got %v, want %v",
			decoded.GlobalWeight, original.GlobalWeight)
	}
	if decoded.DominantLevel != original.DominantLevel {
		t.Errorf("DominantLevel mismatch: got %v, want %v",
			decoded.DominantLevel, original.DominantLevel)
	}
}

// =============================================================================
// Test BlenderConfig
// =============================================================================

func TestDefaultBlenderConfig(t *testing.T) {
	config := DefaultBlenderConfig()

	if config.MinConfidenceForWeight <= 0 || config.MinConfidenceForWeight >= 1 {
		t.Errorf("MinConfidenceForWeight %v not in (0,1)", config.MinConfidenceForWeight)
	}
	if config.ConfidenceExponent <= 0 {
		t.Errorf("ConfidenceExponent %v should be positive", config.ConfidenceExponent)
	}
	if config.ExplorationBonus < 0 {
		t.Errorf("ExplorationBonus %v should be non-negative", config.ExplorationBonus)
	}
}

// =============================================================================
// Test HierarchicalParamBlender Creation
// =============================================================================

func TestNewHierarchicalParamBlender(t *testing.T) {
	t.Run("with nil config", func(t *testing.T) {
		blender := NewHierarchicalParamBlender(nil)
		if blender == nil {
			t.Fatal("NewHierarchicalParamBlender returned nil")
		}
		if blender.config == nil {
			t.Error("Blender config should not be nil")
		}
		stats := blender.GetStats()
		if stats.TotalCount != 0 {
			t.Errorf("New blender should have 0 params, got %d", stats.TotalCount)
		}
	})

	t.Run("with custom config", func(t *testing.T) {
		config := &BlenderConfig{
			MinConfidenceForWeight: 0.2,
			ConfidenceExponent:     3.0,
			ExplorationBonus:       0.05,
		}
		blender := NewHierarchicalParamBlender(config)
		if blender == nil {
			t.Fatal("NewHierarchicalParamBlender returned nil")
		}
		if blender.config.MinConfidenceForWeight != 0.2 {
			t.Errorf("Config not applied correctly")
		}
	})
}

// =============================================================================
// Test Registration Methods
// =============================================================================

func TestHierarchicalParamBlender_RegisterInstance(t *testing.T) {
	blender := NewHierarchicalParamBlender(nil)
	param := NewLearnedWeight(5.0, 5.0)

	blender.RegisterInstance("coder", "gpt-4", "session-123", "threshold", param)

	retrieved, ok := blender.GetInstance("coder", "gpt-4", "session-123", "threshold")
	if !ok {
		t.Fatal("Failed to retrieve registered instance param")
	}
	if retrieved != param {
		t.Error("Retrieved param does not match registered param")
	}

	stats := blender.GetStats()
	if stats.InstanceCount != 1 {
		t.Errorf("InstanceCount = %d, expected 1", stats.InstanceCount)
	}
}

func TestHierarchicalParamBlender_RegisterAgentModel(t *testing.T) {
	blender := NewHierarchicalParamBlender(nil)
	param := NewLearnedWeight(5.0, 5.0)

	blender.RegisterAgentModel("coder", "gpt-4", "threshold", param)

	retrieved, ok := blender.GetAgentModel("coder", "gpt-4", "threshold")
	if !ok {
		t.Fatal("Failed to retrieve registered agent-model param")
	}
	if retrieved != param {
		t.Error("Retrieved param does not match registered param")
	}

	stats := blender.GetStats()
	if stats.AgentModelCount != 1 {
		t.Errorf("AgentModelCount = %d, expected 1", stats.AgentModelCount)
	}
}

func TestHierarchicalParamBlender_RegisterModel(t *testing.T) {
	blender := NewHierarchicalParamBlender(nil)
	param := NewLearnedWeight(5.0, 5.0)

	blender.RegisterModel("gpt-4", "threshold", param)

	retrieved, ok := blender.GetModel("gpt-4", "threshold")
	if !ok {
		t.Fatal("Failed to retrieve registered model param")
	}
	if retrieved != param {
		t.Error("Retrieved param does not match registered param")
	}

	stats := blender.GetStats()
	if stats.ModelCount != 1 {
		t.Errorf("ModelCount = %d, expected 1", stats.ModelCount)
	}
}

func TestHierarchicalParamBlender_RegisterGlobal(t *testing.T) {
	blender := NewHierarchicalParamBlender(nil)
	param := NewLearnedWeight(5.0, 5.0)

	blender.RegisterGlobal("threshold", param)

	retrieved, ok := blender.GetGlobal("threshold")
	if !ok {
		t.Fatal("Failed to retrieve registered global param")
	}
	if retrieved != param {
		t.Error("Retrieved param does not match registered param")
	}

	stats := blender.GetStats()
	if stats.GlobalCount != 1 {
		t.Errorf("GlobalCount = %d, expected 1", stats.GlobalCount)
	}
}

func TestHierarchicalParamBlender_RegisterNilParam(t *testing.T) {
	blender := NewHierarchicalParamBlender(nil)

	blender.RegisterInstance("coder", "gpt-4", "session-123", "threshold", nil)
	blender.RegisterAgentModel("coder", "gpt-4", "threshold", nil)
	blender.RegisterModel("gpt-4", "threshold", nil)
	blender.RegisterGlobal("threshold", nil)

	stats := blender.GetStats()
	if stats.TotalCount != 0 {
		t.Errorf("Nil params should not be registered, got %d", stats.TotalCount)
	}
}

// =============================================================================
// Test Blending - Cold Start Behavior
// =============================================================================

func TestHierarchicalParamBlender_ColdStart_NoParams(t *testing.T) {
	blender := NewHierarchicalParamBlender(nil)

	// No params registered at any level
	_, ok := blender.GetBlended("coder", "gpt-4", "session-123", "threshold")
	if ok {
		t.Error("GetBlended should return false when no params exist")
	}
}

func TestHierarchicalParamBlender_ColdStart_GlobalOnly(t *testing.T) {
	blender := NewHierarchicalParamBlender(nil)

	// Register only global prior (high confidence)
	globalParam := NewLearnedWeight(50.0, 50.0) // Mean = 0.5, high confidence
	blender.RegisterGlobal("threshold", globalParam)

	blended, ok := blender.GetBlended("coder", "gpt-4", "session-123", "threshold")
	if !ok {
		t.Fatal("GetBlended should succeed with global param")
	}

	// Should use 100% global weight
	if math.Abs(blended.GlobalWeight-1.0) > 0.01 {
		t.Errorf("GlobalWeight = %v, expected ~1.0", blended.GlobalWeight)
	}

	// Value should match global mean
	expectedMean := globalParam.Mean()
	if math.Abs(blended.Value-expectedMean) > 0.01 {
		t.Errorf("Value = %v, expected %v", blended.Value, expectedMean)
	}
}

func TestHierarchicalParamBlender_ColdStart_InstanceZeroConfidence(t *testing.T) {
	blender := NewHierarchicalParamBlender(nil)

	// Instance with zero confidence (cold start)
	instanceParam := NewLearnedWeight(1.0, 1.0) // Mean = 0.5, low confidence
	blender.RegisterInstance("coder", "gpt-4", "session-123", "threshold", instanceParam)

	// Global with high confidence
	globalParam := NewLearnedWeight(80.0, 20.0) // Mean = 0.8, high confidence
	blender.RegisterGlobal("threshold", globalParam)

	blended, ok := blender.GetBlended("coder", "gpt-4", "session-123", "threshold")
	if !ok {
		t.Fatal("GetBlended should succeed")
	}

	// Global should dominate due to higher confidence
	if blended.GlobalWeight <= blended.InstanceWeight {
		t.Errorf("GlobalWeight (%v) should exceed InstanceWeight (%v)",
			blended.GlobalWeight, blended.InstanceWeight)
	}

	// Value should be closer to global mean (0.8)
	if blended.Value < 0.6 {
		t.Errorf("Value = %v, expected closer to global mean (0.8)", blended.Value)
	}
}

// =============================================================================
// Test Blending - Confidence-Based Weighting
// =============================================================================

func TestHierarchicalParamBlender_ConfidenceWeighting(t *testing.T) {
	blender := NewHierarchicalParamBlender(nil)

	// Instance: high confidence, mean = 0.3
	instanceParam := NewLearnedWeight(30.0, 70.0)
	blender.RegisterInstance("coder", "gpt-4", "session-123", "threshold", instanceParam)

	// Global: low confidence, mean = 0.8
	globalParam := NewLearnedWeight(8.0, 2.0)
	blender.RegisterGlobal("threshold", globalParam)

	blended, ok := blender.GetBlended("coder", "gpt-4", "session-123", "threshold")
	if !ok {
		t.Fatal("GetBlended should succeed")
	}

	// Instance should dominate due to higher confidence
	if blended.InstanceWeight <= blended.GlobalWeight {
		t.Errorf("InstanceWeight (%v) should exceed GlobalWeight (%v)",
			blended.InstanceWeight, blended.GlobalWeight)
	}

	// Value should be closer to instance mean (0.3)
	if blended.Value > 0.5 {
		t.Errorf("Value = %v, expected closer to instance mean (0.3)", blended.Value)
	}
}

func TestHierarchicalParamBlender_AllLevels(t *testing.T) {
	blender := NewHierarchicalParamBlender(nil)

	// Register params at all levels with different means
	blender.RegisterInstance("coder", "gpt-4", "session-123", "threshold",
		NewLearnedWeight(10.0, 90.0)) // Mean = 0.1

	blender.RegisterAgentModel("coder", "gpt-4", "threshold",
		NewLearnedWeight(30.0, 70.0)) // Mean = 0.3

	blender.RegisterModel("gpt-4", "threshold",
		NewLearnedWeight(50.0, 50.0)) // Mean = 0.5

	blender.RegisterGlobal("threshold",
		NewLearnedWeight(70.0, 30.0)) // Mean = 0.7

	blended, ok := blender.GetBlended("coder", "gpt-4", "session-123", "threshold")
	if !ok {
		t.Fatal("GetBlended should succeed")
	}

	// All weights should be populated
	if blended.InstanceWeight == 0 {
		t.Error("InstanceWeight should be non-zero")
	}
	if blended.AgentModelWeight == 0 {
		t.Error("AgentModelWeight should be non-zero")
	}
	if blended.ModelWeight == 0 {
		t.Error("ModelWeight should be non-zero")
	}
	if blended.GlobalWeight == 0 {
		t.Error("GlobalWeight should be non-zero")
	}

	// Weights should sum to approximately 1.0
	totalWeight := blended.InstanceWeight + blended.AgentModelWeight +
		blended.ModelWeight + blended.GlobalWeight
	if math.Abs(totalWeight-1.0) > 0.01 {
		t.Errorf("Weights sum to %v, expected ~1.0", totalWeight)
	}

	// Value should be somewhere in the middle (weighted average)
	if blended.Value < 0.1 || blended.Value > 0.7 {
		t.Errorf("Value = %v, expected in [0.1, 0.7]", blended.Value)
	}
}

// =============================================================================
// Test GetBlendedWeight
// =============================================================================

func TestHierarchicalParamBlender_GetBlendedWeight(t *testing.T) {
	blender := NewHierarchicalParamBlender(nil)

	// Register params
	blender.RegisterGlobal("threshold", NewLearnedWeight(70.0, 30.0)) // Mean = 0.7

	lw, ok := blender.GetBlendedWeight("coder", "gpt-4", "session-123", "threshold")
	if !ok {
		t.Fatal("GetBlendedWeight should succeed")
	}

	// Verify the resulting LearnedWeight
	if lw == nil {
		t.Fatal("LearnedWeight should not be nil")
	}

	mean := lw.Mean()
	if mean < 0.0 || mean > 1.0 {
		t.Errorf("Mean = %v, should be in [0,1]", mean)
	}

	confidence := lw.Confidence()
	if confidence < 0.0 || confidence > 1.0 {
		t.Errorf("Confidence = %v, should be in [0,1]", confidence)
	}
}

func TestHierarchicalParamBlender_GetBlendedWeight_NoParams(t *testing.T) {
	blender := NewHierarchicalParamBlender(nil)

	_, ok := blender.GetBlendedWeight("coder", "gpt-4", "session-123", "threshold")
	if ok {
		t.Error("GetBlendedWeight should return false when no params exist")
	}
}

// =============================================================================
// Test computeBlendWeight
// =============================================================================

func TestHierarchicalParamBlender_ComputeBlendWeight(t *testing.T) {
	config := DefaultBlenderConfig()
	blender := NewHierarchicalParamBlender(config)

	tests := []struct {
		name       string
		level      BlendLevel
		confidence float64
		expectZero bool
	}{
		{
			name:       "zero confidence",
			level:      LevelInstance,
			confidence: 0.0,
			expectZero: true,
		},
		{
			name:       "below threshold",
			level:      LevelInstance,
			confidence: 0.05, // Below default 0.1
			expectZero: true,
		},
		{
			name:       "at threshold",
			level:      LevelInstance,
			confidence: 0.1,
			expectZero: false,
		},
		{
			name:       "high confidence",
			level:      LevelGlobal,
			confidence: 0.9,
			expectZero: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			weight := blender.computeBlendWeight(tt.level, tt.confidence)

			if tt.expectZero && weight != 0 {
				t.Errorf("Expected zero weight, got %v", weight)
			}
			if !tt.expectZero && weight == 0 {
				t.Error("Expected non-zero weight, got 0")
			}
			if weight < 0 {
				t.Errorf("Weight should be non-negative, got %v", weight)
			}
		})
	}
}

func TestHierarchicalParamBlender_ComputeBlendWeight_HigherConfidenceMoreWeight(t *testing.T) {
	blender := NewHierarchicalParamBlender(nil)

	lowWeight := blender.computeBlendWeight(LevelGlobal, 0.3)
	highWeight := blender.computeBlendWeight(LevelGlobal, 0.9)

	if highWeight <= lowWeight {
		t.Errorf("Higher confidence (%v) should produce higher weight than lower confidence (%v)",
			highWeight, lowWeight)
	}
}

// =============================================================================
// Test Exploration Bonus
// =============================================================================

func TestHierarchicalParamBlender_ExplorationBonus(t *testing.T) {
	config := &BlenderConfig{
		MinConfidenceForWeight: 0.1,
		ConfidenceExponent:     2.0,
		ExplorationBonus:       0.2, // Enable exploration
	}
	blender := NewHierarchicalParamBlender(config)

	// Same confidence, different levels
	instanceWeight := blender.computeBlendWeight(LevelInstance, 0.5)
	globalWeight := blender.computeBlendWeight(LevelGlobal, 0.5)

	// Instance should have higher weight due to exploration bonus
	if instanceWeight <= globalWeight {
		t.Errorf("Instance weight (%v) should exceed global weight (%v) with exploration bonus",
			instanceWeight, globalWeight)
	}
}

// =============================================================================
// Test Removal Methods
// =============================================================================

func TestHierarchicalParamBlender_RemoveInstance(t *testing.T) {
	blender := NewHierarchicalParamBlender(nil)
	blender.RegisterInstance("coder", "gpt-4", "session-123", "threshold",
		NewLearnedWeight(5.0, 5.0))

	// Remove existing
	removed := blender.RemoveInstance("coder", "gpt-4", "session-123", "threshold")
	if !removed {
		t.Error("RemoveInstance should return true for existing param")
	}

	// Verify removal
	_, ok := blender.GetInstance("coder", "gpt-4", "session-123", "threshold")
	if ok {
		t.Error("Param should be removed")
	}

	// Remove non-existing
	removed = blender.RemoveInstance("coder", "gpt-4", "session-123", "threshold")
	if removed {
		t.Error("RemoveInstance should return false for non-existing param")
	}
}

func TestHierarchicalParamBlender_RemoveAgentModel(t *testing.T) {
	blender := NewHierarchicalParamBlender(nil)
	blender.RegisterAgentModel("coder", "gpt-4", "threshold", NewLearnedWeight(5.0, 5.0))

	removed := blender.RemoveAgentModel("coder", "gpt-4", "threshold")
	if !removed {
		t.Error("RemoveAgentModel should return true for existing param")
	}

	_, ok := blender.GetAgentModel("coder", "gpt-4", "threshold")
	if ok {
		t.Error("Param should be removed")
	}
}

func TestHierarchicalParamBlender_RemoveModel(t *testing.T) {
	blender := NewHierarchicalParamBlender(nil)
	blender.RegisterModel("gpt-4", "threshold", NewLearnedWeight(5.0, 5.0))

	removed := blender.RemoveModel("gpt-4", "threshold")
	if !removed {
		t.Error("RemoveModel should return true for existing param")
	}

	_, ok := blender.GetModel("gpt-4", "threshold")
	if ok {
		t.Error("Param should be removed")
	}
}

func TestHierarchicalParamBlender_RemoveGlobal(t *testing.T) {
	blender := NewHierarchicalParamBlender(nil)
	blender.RegisterGlobal("threshold", NewLearnedWeight(5.0, 5.0))

	removed := blender.RemoveGlobal("threshold")
	if !removed {
		t.Error("RemoveGlobal should return true for existing param")
	}

	_, ok := blender.GetGlobal("threshold")
	if ok {
		t.Error("Param should be removed")
	}
}

// =============================================================================
// Test Clear and GetStats
// =============================================================================

func TestHierarchicalParamBlender_Clear(t *testing.T) {
	blender := NewHierarchicalParamBlender(nil)

	// Register params at all levels
	blender.RegisterInstance("coder", "gpt-4", "session-123", "threshold",
		NewLearnedWeight(5.0, 5.0))
	blender.RegisterAgentModel("coder", "gpt-4", "threshold",
		NewLearnedWeight(5.0, 5.0))
	blender.RegisterModel("gpt-4", "threshold", NewLearnedWeight(5.0, 5.0))
	blender.RegisterGlobal("threshold", NewLearnedWeight(5.0, 5.0))

	stats := blender.GetStats()
	if stats.TotalCount != 4 {
		t.Errorf("Expected 4 params before clear, got %d", stats.TotalCount)
	}

	blender.Clear()

	stats = blender.GetStats()
	if stats.TotalCount != 0 {
		t.Errorf("Expected 0 params after clear, got %d", stats.TotalCount)
	}
}

func TestHierarchicalParamBlender_GetStats(t *testing.T) {
	blender := NewHierarchicalParamBlender(nil)

	// Register multiple params
	blender.RegisterInstance("coder", "gpt-4", "s1", "p1", NewLearnedWeight(1, 1))
	blender.RegisterInstance("coder", "gpt-4", "s2", "p1", NewLearnedWeight(1, 1))
	blender.RegisterAgentModel("coder", "gpt-4", "p1", NewLearnedWeight(1, 1))
	blender.RegisterModel("gpt-4", "p1", NewLearnedWeight(1, 1))
	blender.RegisterGlobal("p1", NewLearnedWeight(1, 1))

	stats := blender.GetStats()

	if stats.InstanceCount != 2 {
		t.Errorf("InstanceCount = %d, expected 2", stats.InstanceCount)
	}
	if stats.AgentModelCount != 1 {
		t.Errorf("AgentModelCount = %d, expected 1", stats.AgentModelCount)
	}
	if stats.ModelCount != 1 {
		t.Errorf("ModelCount = %d, expected 1", stats.ModelCount)
	}
	if stats.GlobalCount != 1 {
		t.Errorf("GlobalCount = %d, expected 1", stats.GlobalCount)
	}
	if stats.TotalCount != 5 {
		t.Errorf("TotalCount = %d, expected 5", stats.TotalCount)
	}
}

// =============================================================================
// Test JSON Serialization
// =============================================================================

func TestHierarchicalParamBlender_JSONMarshal(t *testing.T) {
	original := NewHierarchicalParamBlender(nil)

	// Register params at all levels
	original.RegisterInstance("coder", "gpt-4", "session-123", "threshold",
		NewLearnedWeight(10.0, 90.0))
	original.RegisterAgentModel("coder", "gpt-4", "threshold",
		NewLearnedWeight(30.0, 70.0))
	original.RegisterModel("gpt-4", "threshold",
		NewLearnedWeight(50.0, 50.0))
	original.RegisterGlobal("threshold",
		NewLearnedWeight(70.0, 30.0))

	// Marshal
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Unmarshal
	decoded := NewHierarchicalParamBlender(nil)
	err = json.Unmarshal(data, decoded)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Verify counts
	origStats := original.GetStats()
	decStats := decoded.GetStats()

	if origStats.InstanceCount != decStats.InstanceCount {
		t.Errorf("InstanceCount mismatch: %d vs %d",
			origStats.InstanceCount, decStats.InstanceCount)
	}
	if origStats.AgentModelCount != decStats.AgentModelCount {
		t.Errorf("AgentModelCount mismatch: %d vs %d",
			origStats.AgentModelCount, decStats.AgentModelCount)
	}
	if origStats.ModelCount != decStats.ModelCount {
		t.Errorf("ModelCount mismatch: %d vs %d",
			origStats.ModelCount, decStats.ModelCount)
	}
	if origStats.GlobalCount != decStats.GlobalCount {
		t.Errorf("GlobalCount mismatch: %d vs %d",
			origStats.GlobalCount, decStats.GlobalCount)
	}

	// Verify global param values
	origGlobal, _ := original.GetGlobal("threshold")
	decGlobal, ok := decoded.GetGlobal("threshold")
	if !ok {
		t.Fatal("Decoded global param not found")
	}

	if math.Abs(origGlobal.Mean()-decGlobal.Mean()) > 1e-9 {
		t.Errorf("Global mean mismatch: %v vs %v",
			origGlobal.Mean(), decGlobal.Mean())
	}
}

func TestHierarchicalParamBlender_JSONEmptyBlender(t *testing.T) {
	original := NewHierarchicalParamBlender(nil)

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	decoded := NewHierarchicalParamBlender(nil)
	err = json.Unmarshal(data, decoded)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	stats := decoded.GetStats()
	if stats.TotalCount != 0 {
		t.Errorf("Decoded empty blender should have 0 params, got %d", stats.TotalCount)
	}
}

// =============================================================================
// Test Specialized Blending Functions
// =============================================================================

func TestBlendWeight_EmptyLevels(t *testing.T) {
	result := blendWeight(nil, false)
	if result != 0.5 {
		t.Errorf("blendWeight with empty levels = %v, expected 0.5", result)
	}

	result = blendWeight([]leveledParam{}, false)
	if result != 0.5 {
		t.Errorf("blendWeight with empty levels = %v, expected 0.5", result)
	}
}

func TestBlendWeight_SingleLevel(t *testing.T) {
	param := NewLearnedWeight(80.0, 20.0) // Mean = 0.8
	levels := []leveledParam{
		{Param: param, Confidence: param.Confidence(), Level: LevelGlobal},
	}

	result := blendWeight(levels, false)
	if math.Abs(result-0.8) > 0.01 {
		t.Errorf("blendWeight = %v, expected ~0.8", result)
	}
}

func TestBlendContextSize_EmptyLevels(t *testing.T) {
	result := blendContextSize(nil, false)
	if result != 100 {
		t.Errorf("blendContextSize with empty levels = %v, expected 100", result)
	}
}

func TestBlendCount_EmptyLevels(t *testing.T) {
	result := blendCount(nil, false)
	if result != 5 {
		t.Errorf("blendCount with empty levels = %v, expected 5", result)
	}
}

func TestBlendCount_EnsuresNonNegative(t *testing.T) {
	// Create a param with mean close to 0
	param := NewLearnedWeight(1.0, 99.0) // Mean = 0.01
	levels := []leveledParam{
		{Param: param, Confidence: param.Confidence(), Level: LevelGlobal},
	}

	result := blendCount(levels, false)
	if result < 0 {
		t.Errorf("blendCount = %v, should be non-negative", result)
	}
}

// =============================================================================
// Test Concurrent Access
// =============================================================================

func TestHierarchicalParamBlender_ConcurrentAccess(t *testing.T) {
	blender := NewHierarchicalParamBlender(nil)

	// Pre-register some params
	blender.RegisterGlobal("threshold", NewLearnedWeight(50.0, 50.0))

	done := make(chan bool)

	// Concurrent writers
	go func() {
		for i := 0; i < 100; i++ {
			blender.RegisterInstance("coder", "gpt-4",
				"session-"+string(rune(i%26+'a')), "threshold",
				NewLearnedWeight(float64(i), float64(100-i)))
		}
		done <- true
	}()

	// Concurrent readers
	go func() {
		for i := 0; i < 100; i++ {
			blender.GetBlended("coder", "gpt-4", "session-a", "threshold")
			blender.GetStats()
		}
		done <- true
	}()

	// Wait for completion
	<-done
	<-done

	// Should not panic and stats should be consistent
	stats := blender.GetStats()
	if stats.InstanceCount < 0 {
		t.Error("Stats should be non-negative")
	}
}

// =============================================================================
// Test DominantLevel Tracking
// =============================================================================

func TestHierarchicalParamBlender_DominantLevel(t *testing.T) {
	blender := NewHierarchicalParamBlender(nil)

	// Register only global (should be dominant)
	blender.RegisterGlobal("threshold", NewLearnedWeight(50.0, 50.0))

	blended, _ := blender.GetBlended("coder", "gpt-4", "session-123", "threshold")
	if blended.DominantLevel != LevelGlobal {
		t.Errorf("DominantLevel = %v, expected Global", blended.DominantLevel)
	}

	// Add high-confidence instance (should become dominant)
	blender.RegisterInstance("coder", "gpt-4", "session-123", "threshold",
		NewLearnedWeight(500.0, 500.0)) // Very high confidence

	blended, _ = blender.GetBlended("coder", "gpt-4", "session-123", "threshold")
	if blended.DominantLevel != LevelInstance {
		t.Errorf("DominantLevel = %v, expected Instance", blended.DominantLevel)
	}
}

// =============================================================================
// Test No Invalid Values
// =============================================================================

func TestHierarchicalParamBlender_NoInvalidValues(t *testing.T) {
	blender := NewHierarchicalParamBlender(nil)

	// Register various params
	blender.RegisterInstance("coder", "gpt-4", "session-123", "threshold",
		NewLearnedWeight(0.1, 0.1))
	blender.RegisterGlobal("threshold", NewLearnedWeight(1000.0, 1000.0))

	blended, _ := blender.GetBlended("coder", "gpt-4", "session-123", "threshold")

	assertValidBlended(t, "Value", blended.Value)
	assertValidBlended(t, "InstanceWeight", blended.InstanceWeight)
	assertValidBlended(t, "AgentModelWeight", blended.AgentModelWeight)
	assertValidBlended(t, "ModelWeight", blended.ModelWeight)
	assertValidBlended(t, "GlobalWeight", blended.GlobalWeight)
	assertValidBlended(t, "EffectiveConfidence", blended.EffectiveConfidence)

	// Weights should be in [0,1]
	if blended.InstanceWeight < 0 || blended.InstanceWeight > 1 {
		t.Errorf("InstanceWeight %v out of [0,1]", blended.InstanceWeight)
	}
	if blended.Value < 0 || blended.Value > 1 {
		t.Errorf("Value %v out of [0,1]", blended.Value)
	}
}

func assertValidBlended(t *testing.T, name string, value float64) {
	t.Helper()
	if math.IsNaN(value) {
		t.Errorf("%s is NaN", name)
	}
	if math.IsInf(value, 0) {
		t.Errorf("%s is Inf", name)
	}
}

// =============================================================================
// Benchmarks
// =============================================================================

func BenchmarkHierarchicalParamBlender_GetBlended(b *testing.B) {
	blender := NewHierarchicalParamBlender(nil)

	// Register params at all levels
	blender.RegisterInstance("coder", "gpt-4", "session-123", "threshold",
		NewLearnedWeight(10.0, 90.0))
	blender.RegisterAgentModel("coder", "gpt-4", "threshold",
		NewLearnedWeight(30.0, 70.0))
	blender.RegisterModel("gpt-4", "threshold",
		NewLearnedWeight(50.0, 50.0))
	blender.RegisterGlobal("threshold",
		NewLearnedWeight(70.0, 30.0))

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		blender.GetBlended("coder", "gpt-4", "session-123", "threshold")
	}
}

func BenchmarkHierarchicalParamBlender_GetBlendedWeight(b *testing.B) {
	blender := NewHierarchicalParamBlender(nil)

	blender.RegisterGlobal("threshold", NewLearnedWeight(70.0, 30.0))

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		blender.GetBlendedWeight("coder", "gpt-4", "session-123", "threshold")
	}
}

func BenchmarkHierarchicalParamBlender_Register(b *testing.B) {
	blender := NewHierarchicalParamBlender(nil)
	param := NewLearnedWeight(5.0, 5.0)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		blender.RegisterInstance("coder", "gpt-4", "session", "threshold", param)
	}
}

func BenchmarkHierarchicalParamBlender_ComputeBlendWeight(b *testing.B) {
	blender := NewHierarchicalParamBlender(nil)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		blender.computeBlendWeight(LevelInstance, 0.75)
	}
}
