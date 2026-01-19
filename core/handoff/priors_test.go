package handoff

import (
	"encoding/json"
	"math"
	"sync"
	"testing"
)

// =============================================================================
// Test DefaultGlobalPriors (HO.7.1)
// =============================================================================

func TestDefaultGlobalPriors(t *testing.T) {
	priors := DefaultGlobalPriors()

	if priors == nil {
		t.Fatal("DefaultGlobalPriors returned nil")
	}

	// Check that all fields are initialized
	if priors.ContextThreshold == nil {
		t.Error("ContextThreshold should not be nil")
	}
	if priors.QualityThreshold == nil {
		t.Error("QualityThreshold should not be nil")
	}
	if priors.OptimalContextSize == nil {
		t.Error("OptimalContextSize should not be nil")
	}
	if priors.PreserveRecentTurns == nil {
		t.Error("PreserveRecentTurns should not be nil")
	}
	if priors.HandoffSuccessRate == nil {
		t.Error("HandoffSuccessRate should not be nil")
	}
	if priors.ContextDecayRate == nil {
		t.Error("ContextDecayRate should not be nil")
	}
}

func TestDefaultGlobalPriors_DefaultValues(t *testing.T) {
	priors := DefaultGlobalPriors()

	// Context threshold: Beta(5, 5) -> mean 0.5
	ctMean := priors.ContextThreshold.Mean()
	if math.Abs(ctMean-0.5) > 0.01 {
		t.Errorf("ContextThreshold mean = %v, expected ~0.5", ctMean)
	}

	// Quality threshold: Beta(7, 3) -> mean 0.7
	qtMean := priors.QualityThreshold.Mean()
	if math.Abs(qtMean-0.7) > 0.01 {
		t.Errorf("QualityThreshold mean = %v, expected ~0.7", qtMean)
	}

	// Optimal context size: Gamma(100, 1) -> mean 100
	ocsSize := priors.OptimalContextSize.Mean()
	if ocsSize != 100 {
		t.Errorf("OptimalContextSize mean = %v, expected 100", ocsSize)
	}

	// Preserve recent turns: Gamma(10, 1) -> mean 10
	prtTurns := priors.PreserveRecentTurns.Mean()
	if prtTurns != 10 {
		t.Errorf("PreserveRecentTurns mean = %v, expected 10", prtTurns)
	}

	// Handoff success rate: Beta(8, 2) -> mean 0.8
	hsrMean := priors.HandoffSuccessRate.Mean()
	if math.Abs(hsrMean-0.8) > 0.01 {
		t.Errorf("HandoffSuccessRate mean = %v, expected ~0.8", hsrMean)
	}

	// Context decay rate: Beta(3, 7) -> mean 0.3
	cdrMean := priors.ContextDecayRate.Mean()
	if math.Abs(cdrMean-0.3) > 0.01 {
		t.Errorf("ContextDecayRate mean = %v, expected ~0.3", cdrMean)
	}
}

func TestGlobalPriors_Clone(t *testing.T) {
	original := DefaultGlobalPriors()

	// Modify original
	original.ContextThreshold.Update(0.9, DefaultUpdateConfig())

	cloned := original.Clone()

	// Verify clone has same values
	if math.Abs(cloned.ContextThreshold.Mean()-original.ContextThreshold.Mean()) > 0.01 {
		t.Error("Cloned ContextThreshold mean does not match original")
	}

	// Modify original again - clone should not change
	original.ContextThreshold.Update(0.1, DefaultUpdateConfig())

	if math.Abs(cloned.ContextThreshold.Mean()-0.1) < 0.1 {
		t.Error("Clone should be independent from original")
	}
}

func TestGlobalPriors_CloneNil(t *testing.T) {
	var priors *GlobalPriors
	cloned := priors.Clone()
	if cloned != nil {
		t.Error("Clone of nil should return nil")
	}
}

func TestGlobalPriors_Confidence(t *testing.T) {
	priors := DefaultGlobalPriors()

	conf := priors.Confidence()

	if conf < 0.0 || conf > 1.0 {
		t.Errorf("Confidence = %v, should be in [0,1]", conf)
	}

	// Default priors have low confidence
	if conf > 0.8 {
		t.Errorf("Default priors confidence = %v, expected lower", conf)
	}
}

func TestGlobalPriors_ConfidenceNil(t *testing.T) {
	var priors *GlobalPriors
	conf := priors.Confidence()
	if conf != 0.0 {
		t.Errorf("Confidence of nil should be 0.0, got %v", conf)
	}
}

// =============================================================================
// Test ModelPriors (HO.7.2)
// =============================================================================

func TestNewModelPriors(t *testing.T) {
	global := DefaultGlobalPriors()
	mp := NewModelPriors("claude-3-opus", global)

	if mp == nil {
		t.Fatal("NewModelPriors returned nil")
	}

	if mp.ModelName != "claude-3-opus" {
		t.Errorf("ModelName = %v, expected claude-3-opus", mp.ModelName)
	}

	if mp.EffectiveSamples != 0.0 {
		t.Errorf("EffectiveSamples = %v, expected 0.0", mp.EffectiveSamples)
	}
}

func TestNewModelPriors_InheritsFromGlobal(t *testing.T) {
	global := DefaultGlobalPriors()
	mp := NewModelPriors("gpt-4", global)

	// Should inherit global values
	globalCtMean := global.ContextThreshold.Mean()
	mpCtMean := mp.ContextThreshold.Mean()

	if math.Abs(globalCtMean-mpCtMean) > 0.01 {
		t.Errorf("ModelPriors ContextThreshold = %v, expected %v from global",
			mpCtMean, globalCtMean)
	}
}

func TestNewModelPriors_NilGlobal(t *testing.T) {
	mp := NewModelPriors("claude-3-opus", nil)

	if mp == nil {
		t.Fatal("NewModelPriors with nil global returned nil")
	}

	// Should use defaults
	if mp.ContextThreshold == nil {
		t.Error("ContextThreshold should be initialized even with nil global")
	}

	// Should have default mean of 0.5 for context threshold
	ctMean := mp.ContextThreshold.Mean()
	if math.Abs(ctMean-0.5) > 0.01 {
		t.Errorf("ContextThreshold mean = %v, expected ~0.5", ctMean)
	}
}

func TestModelPriors_UpdateFromObservation(t *testing.T) {
	mp := NewModelPriors("claude-3-opus", nil)

	// Get initial values
	initialHsr := mp.HandoffSuccessRate.Mean()
	initialSamples := mp.EffectiveSamples

	// Update with a successful observation
	obs := NewPriorObservation(100, 0.9, true, 5)
	mp.UpdateFromObservation(obs, DefaultUpdateConfig())

	// Check that values changed
	if mp.EffectiveSamples <= initialSamples {
		t.Error("EffectiveSamples should increase after update")
	}

	// Success rate should move toward 1.0
	newHsr := mp.HandoffSuccessRate.Mean()
	if newHsr < initialHsr {
		t.Errorf("HandoffSuccessRate should increase after successful observation")
	}
}

func TestModelPriors_UpdateFromObservation_Failed(t *testing.T) {
	mp := NewModelPriors("claude-3-opus", nil)

	// Update with multiple failed observations
	for i := 0; i < 50; i++ {
		obs := NewPriorObservation(100, 0.2, false, 5)
		mp.UpdateFromObservation(obs, DefaultUpdateConfig())
	}

	// Success rate should decrease
	hsr := mp.HandoffSuccessRate.Mean()
	if hsr > 0.5 {
		t.Errorf("HandoffSuccessRate = %v, expected lower after failed observations", hsr)
	}
}

func TestModelPriors_UpdateFromObservation_NilObs(t *testing.T) {
	mp := NewModelPriors("claude-3-opus", nil)
	initialSamples := mp.EffectiveSamples

	mp.UpdateFromObservation(nil, DefaultUpdateConfig())

	if mp.EffectiveSamples != initialSamples {
		t.Error("Nil observation should not change EffectiveSamples")
	}
}

func TestModelPriors_Confidence(t *testing.T) {
	mp := NewModelPriors("claude-3-opus", nil)

	// Initial confidence should be 0
	conf := mp.Confidence()
	if conf != 0.0 {
		t.Errorf("Initial confidence = %v, expected 0.0", conf)
	}

	// After updates, confidence should increase
	for i := 0; i < 20; i++ {
		obs := NewPriorObservation(100, 0.8, true, 5)
		mp.UpdateFromObservation(obs, DefaultUpdateConfig())
	}

	conf = mp.Confidence()
	if conf <= 0.0 {
		t.Errorf("Confidence after updates = %v, expected > 0", conf)
	}
}

func TestModelPriors_Clone(t *testing.T) {
	original := NewModelPriors("claude-3-opus", nil)
	obs := NewPriorObservation(100, 0.8, true, 5)
	original.UpdateFromObservation(obs, DefaultUpdateConfig())

	cloned := original.Clone()

	// Verify clone independence
	original.UpdateFromObservation(obs, DefaultUpdateConfig())

	if math.Abs(cloned.EffectiveSamples-original.EffectiveSamples) < 0.1 {
		t.Error("Clone should be independent from original")
	}
}

// =============================================================================
// Test AgentModelPriors (HO.7.3)
// =============================================================================

func TestNewAgentModelPriors(t *testing.T) {
	mp := NewModelPriors("claude-3-opus", nil)
	amp := NewAgentModelPriors("coder", "claude-3-opus", mp)

	if amp == nil {
		t.Fatal("NewAgentModelPriors returned nil")
	}

	if amp.AgentID != "coder" {
		t.Errorf("AgentID = %v, expected coder", amp.AgentID)
	}

	if amp.ModelName != "claude-3-opus" {
		t.Errorf("ModelName = %v, expected claude-3-opus", amp.ModelName)
	}
}

func TestNewAgentModelPriors_InheritsFromModelPriors(t *testing.T) {
	mp := NewModelPriors("claude-3-opus", nil)

	// Update model priors to have specific values
	for i := 0; i < 50; i++ {
		obs := NewPriorObservation(200, 0.9, true, 10)
		mp.UpdateFromObservation(obs, DefaultUpdateConfig())
	}

	amp := NewAgentModelPriors("coder", "claude-3-opus", mp)

	// Should inherit model values
	mpCtMean := mp.ContextThreshold.Mean()
	ampCtMean := amp.ContextThreshold.Mean()

	if math.Abs(mpCtMean-ampCtMean) > 0.01 {
		t.Errorf("AgentModelPriors ContextThreshold = %v, expected %v from model",
			ampCtMean, mpCtMean)
	}
}

func TestNewAgentModelPriors_NilModelPriors(t *testing.T) {
	amp := NewAgentModelPriors("coder", "claude-3-opus", nil)

	if amp == nil {
		t.Fatal("NewAgentModelPriors with nil model priors returned nil")
	}

	// Should use defaults
	if amp.ContextThreshold == nil {
		t.Error("ContextThreshold should be initialized even with nil model priors")
	}
}

func TestAgentModelPriors_UpdateFromObservation(t *testing.T) {
	amp := NewAgentModelPriors("coder", "claude-3-opus", nil)

	initialSamples := amp.EffectiveSamples

	obs := NewPriorObservation(150, 0.85, true, 7)
	amp.UpdateFromObservation(obs, DefaultUpdateConfig())

	if amp.EffectiveSamples <= initialSamples {
		t.Error("EffectiveSamples should increase after update")
	}
}

func TestAgentModelPriors_Confidence(t *testing.T) {
	amp := NewAgentModelPriors("coder", "claude-3-opus", nil)

	// Initial confidence should be 0
	conf := amp.Confidence()
	if conf != 0.0 {
		t.Errorf("Initial confidence = %v, expected 0.0", conf)
	}

	// After updates, confidence should increase
	for i := 0; i < 30; i++ {
		obs := NewPriorObservation(100, 0.8, true, 5)
		amp.UpdateFromObservation(obs, DefaultUpdateConfig())
	}

	conf = amp.Confidence()
	if conf <= 0.5 {
		t.Errorf("Confidence after many updates = %v, expected > 0.5", conf)
	}
}

func TestAgentModelPriors_BlendWithParent(t *testing.T) {
	// Create parent with specific values
	mp := NewModelPriors("claude-3-opus", nil)
	for i := 0; i < 50; i++ {
		obs := NewPriorObservation(100, 0.3, false, 5)
		mp.UpdateFromObservation(obs, DefaultUpdateConfig())
	}

	// Create child with different values
	amp := NewAgentModelPriors("coder", "claude-3-opus", nil)
	for i := 0; i < 50; i++ {
		obs := NewPriorObservation(200, 0.9, true, 10)
		amp.UpdateFromObservation(obs, DefaultUpdateConfig())
	}

	// Blend with 0.5 weight
	blended := amp.BlendWithParent(mp, 0.5)

	if blended == nil {
		t.Fatal("BlendWithParent returned nil")
	}

	// Blended value should be between parent and child
	ampCtMean := amp.ContextThreshold.Mean()
	mpCtMean := mp.ContextThreshold.Mean()
	blendedCtMean := blended.ContextThreshold.Mean()

	minVal := math.Min(ampCtMean, mpCtMean)
	maxVal := math.Max(ampCtMean, mpCtMean)

	if blendedCtMean < minVal-0.1 || blendedCtMean > maxVal+0.1 {
		t.Errorf("Blended ContextThreshold = %v, expected between %v and %v",
			blendedCtMean, minVal, maxVal)
	}
}

func TestAgentModelPriors_BlendWithParent_NilChild(t *testing.T) {
	mp := NewModelPriors("claude-3-opus", nil)
	for i := 0; i < 20; i++ {
		obs := NewPriorObservation(100, 0.7, true, 5)
		mp.UpdateFromObservation(obs, DefaultUpdateConfig())
	}

	var amp *AgentModelPriors
	blended := amp.BlendWithParent(mp, 0.5)

	if blended == nil {
		t.Fatal("BlendWithParent with nil child returned nil")
	}

	// Should return parent values
	if math.Abs(blended.ContextThreshold.Mean()-mp.ContextThreshold.Mean()) > 0.01 {
		t.Error("With nil child, blended should match parent")
	}
}

func TestAgentModelPriors_BlendWithParent_NilParent(t *testing.T) {
	amp := NewAgentModelPriors("coder", "claude-3-opus", nil)
	for i := 0; i < 20; i++ {
		obs := NewPriorObservation(100, 0.7, true, 5)
		amp.UpdateFromObservation(obs, DefaultUpdateConfig())
	}

	blended := amp.BlendWithParent(nil, 0.5)

	if blended == nil {
		t.Fatal("BlendWithParent with nil parent returned nil")
	}

	// Should return child values
	if math.Abs(blended.ContextThreshold.Mean()-amp.ContextThreshold.Mean()) > 0.01 {
		t.Error("With nil parent, blended should match child")
	}
}

func TestAgentModelPriors_BlendWithParent_BothNil(t *testing.T) {
	var amp *AgentModelPriors
	blended := amp.BlendWithParent(nil, 0.5)

	if blended != nil {
		t.Error("BlendWithParent with both nil should return nil")
	}
}

func TestAgentModelPriors_BlendWithParent_ExtremeWeights(t *testing.T) {
	mp := NewModelPriors("claude-3-opus", nil)
	for i := 0; i < 50; i++ {
		obs := NewPriorObservation(100, 0.2, false, 5)
		mp.UpdateFromObservation(obs, DefaultUpdateConfig())
	}

	amp := NewAgentModelPriors("coder", "claude-3-opus", nil)
	for i := 0; i < 50; i++ {
		obs := NewPriorObservation(200, 0.9, true, 10)
		amp.UpdateFromObservation(obs, DefaultUpdateConfig())
	}

	// Weight = 0: should favor parent
	blended0 := amp.BlendWithParent(mp, 0.0)
	if blended0.ContextThreshold.Mean() > 0.7 {
		t.Errorf("With weight 0, blended should favor parent")
	}

	// Weight = 1: should favor child (if child has high confidence)
	blended1 := amp.BlendWithParent(mp, 1.0)
	if blended1.ContextThreshold.Mean() < 0.5 {
		t.Errorf("With weight 1, blended should favor child")
	}
}

func TestAgentModelPriors_Clone(t *testing.T) {
	original := NewAgentModelPriors("coder", "claude-3-opus", nil)
	obs := NewPriorObservation(100, 0.8, true, 5)
	original.UpdateFromObservation(obs, DefaultUpdateConfig())

	cloned := original.Clone()

	// Verify clone independence
	original.UpdateFromObservation(obs, DefaultUpdateConfig())

	if math.Abs(cloned.EffectiveSamples-original.EffectiveSamples) < 0.1 {
		t.Error("Clone should be independent from original")
	}
}

func TestAgentModelPriors_CloneNil(t *testing.T) {
	var amp *AgentModelPriors
	cloned := amp.Clone()
	if cloned != nil {
		t.Error("Clone of nil should return nil")
	}
}

// =============================================================================
// Test PriorHierarchy
// =============================================================================

func TestNewPriorHierarchy(t *testing.T) {
	ph := NewPriorHierarchy()

	if ph == nil {
		t.Fatal("NewPriorHierarchy returned nil")
	}

	if ph.globalPriors == nil {
		t.Error("globalPriors should not be nil")
	}

	stats := ph.GetStats()
	if stats.ModelCount != 0 {
		t.Errorf("ModelCount = %d, expected 0", stats.ModelCount)
	}
	if stats.AgentModelCount != 0 {
		t.Errorf("AgentModelCount = %d, expected 0", stats.AgentModelCount)
	}
}

func TestNewPriorHierarchyWithConfig(t *testing.T) {
	config := &UpdateConfig{
		LearningRate:   0.2,
		DriftThreshold: 0.9,
		PriorBlendRate: 0.1,
	}

	ph := NewPriorHierarchyWithConfig(config)

	if ph == nil {
		t.Fatal("NewPriorHierarchyWithConfig returned nil")
	}

	if ph.updateConfig.LearningRate != 0.2 {
		t.Errorf("LearningRate = %v, expected 0.2", ph.updateConfig.LearningRate)
	}
}

func TestNewPriorHierarchyWithConfig_Nil(t *testing.T) {
	ph := NewPriorHierarchyWithConfig(nil)

	if ph == nil {
		t.Fatal("NewPriorHierarchyWithConfig(nil) returned nil")
	}

	// Should use default config
	if ph.updateConfig == nil {
		t.Error("updateConfig should not be nil")
	}
}

func TestPriorHierarchy_GlobalPriors(t *testing.T) {
	ph := NewPriorHierarchy()

	// Get global priors
	global := ph.GetGlobalPriors()
	if global == nil {
		t.Fatal("GetGlobalPriors returned nil")
	}

	// Verify it's a copy by making many updates to the copy
	for i := 0; i < 100; i++ {
		global.ContextThreshold.Update(0.99, DefaultUpdateConfig())
	}
	origGlobal := ph.GetGlobalPriors()

	// The copy's mean should have moved significantly, while original remains
	// Original should still be close to 0.5, copy should be much higher
	if global.ContextThreshold.Mean() < 0.6 {
		t.Error("Copy's mean should have increased after many updates")
	}
	if origGlobal.ContextThreshold.Mean() > 0.55 {
		t.Error("Original's mean should remain close to default after modifying copy")
	}
}

func TestPriorHierarchy_SetGlobalPriors(t *testing.T) {
	ph := NewPriorHierarchy()

	newGlobal := DefaultGlobalPriors()
	newGlobal.ContextThreshold = NewLearnedWeight(9.0, 1.0) // Mean = 0.9

	ph.SetGlobalPriors(newGlobal)

	retrieved := ph.GetGlobalPriors()
	if math.Abs(retrieved.ContextThreshold.Mean()-0.9) > 0.01 {
		t.Errorf("ContextThreshold mean = %v, expected ~0.9", retrieved.ContextThreshold.Mean())
	}
}

func TestPriorHierarchy_SetGlobalPriors_Nil(t *testing.T) {
	ph := NewPriorHierarchy()
	initialMean := ph.GetGlobalPriors().ContextThreshold.Mean()

	ph.SetGlobalPriors(nil)

	// Should not change
	currentMean := ph.GetGlobalPriors().ContextThreshold.Mean()
	if math.Abs(initialMean-currentMean) > 0.01 {
		t.Error("SetGlobalPriors(nil) should not change priors")
	}
}

func TestPriorHierarchy_ModelPriors(t *testing.T) {
	ph := NewPriorHierarchy()

	// Initially no model priors
	mp := ph.GetModelPriors("claude-3-opus")
	if mp != nil {
		t.Error("GetModelPriors should return nil for non-existent model")
	}

	// Create model priors
	mp = ph.GetOrCreateModelPriors("claude-3-opus")
	if mp == nil {
		t.Fatal("GetOrCreateModelPriors returned nil")
	}

	if mp.ModelName != "claude-3-opus" {
		t.Errorf("ModelName = %v, expected claude-3-opus", mp.ModelName)
	}

	// Should now exist
	mp2 := ph.GetModelPriors("claude-3-opus")
	if mp2 == nil {
		t.Error("GetModelPriors should return priors after creation")
	}
}

func TestPriorHierarchy_SetModelPriors(t *testing.T) {
	ph := NewPriorHierarchy()

	mp := NewModelPriors("gpt-4", nil)
	for i := 0; i < 20; i++ {
		obs := NewPriorObservation(100, 0.9, true, 5)
		mp.UpdateFromObservation(obs, DefaultUpdateConfig())
	}

	ph.SetModelPriors("gpt-4", mp)

	retrieved := ph.GetModelPriors("gpt-4")
	if retrieved == nil {
		t.Fatal("GetModelPriors returned nil after set")
	}

	if math.Abs(retrieved.EffectiveSamples-mp.EffectiveSamples) > 0.5 {
		t.Error("Retrieved priors should match set priors")
	}
}

func TestPriorHierarchy_AgentModelPriors(t *testing.T) {
	ph := NewPriorHierarchy()

	// Initially no agent+model priors
	amp := ph.GetAgentModelPriors("coder", "claude-3-opus")
	if amp != nil {
		t.Error("GetAgentModelPriors should return nil for non-existent")
	}

	// Create agent+model priors
	amp = ph.GetOrCreateAgentModelPriors("coder", "claude-3-opus")
	if amp == nil {
		t.Fatal("GetOrCreateAgentModelPriors returned nil")
	}

	if amp.AgentID != "coder" {
		t.Errorf("AgentID = %v, expected coder", amp.AgentID)
	}
	if amp.ModelName != "claude-3-opus" {
		t.Errorf("ModelName = %v, expected claude-3-opus", amp.ModelName)
	}

	// Should also create model priors
	mp := ph.GetModelPriors("claude-3-opus")
	if mp == nil {
		t.Error("GetOrCreateAgentModelPriors should also create model priors")
	}
}

func TestPriorHierarchy_SetAgentModelPriors(t *testing.T) {
	ph := NewPriorHierarchy()

	amp := NewAgentModelPriors("coder", "gpt-4", nil)
	for i := 0; i < 20; i++ {
		obs := NewPriorObservation(100, 0.9, true, 5)
		amp.UpdateFromObservation(obs, DefaultUpdateConfig())
	}

	ph.SetAgentModelPriors("coder", "gpt-4", amp)

	retrieved := ph.GetAgentModelPriors("coder", "gpt-4")
	if retrieved == nil {
		t.Fatal("GetAgentModelPriors returned nil after set")
	}

	if math.Abs(retrieved.EffectiveSamples-amp.EffectiveSamples) > 0.5 {
		t.Error("Retrieved priors should match set priors")
	}
}

// =============================================================================
// Test Hierarchical Lookup
// =============================================================================

func TestPriorHierarchy_GetEffectivePrior_GlobalOnly(t *testing.T) {
	ph := NewPriorHierarchy()

	effective := ph.GetEffectivePrior("coder", "claude-3-opus")

	if effective == nil {
		t.Fatal("GetEffectivePrior returned nil")
	}

	// With only global priors, global weight should be 1.0
	if math.Abs(effective.GlobalWeight-1.0) > 0.01 {
		t.Errorf("GlobalWeight = %v, expected ~1.0", effective.GlobalWeight)
	}

	// Values should match global defaults
	globalPriors := ph.GetGlobalPriors()
	expectedCt := globalPriors.ContextThreshold.Mean()
	if math.Abs(effective.ContextThreshold-expectedCt) > 0.01 {
		t.Errorf("ContextThreshold = %v, expected %v", effective.ContextThreshold, expectedCt)
	}
}

func TestPriorHierarchy_GetEffectivePrior_WithModelPriors(t *testing.T) {
	ph := NewPriorHierarchy()

	// Add model priors with high confidence
	mp := ph.GetOrCreateModelPriors("claude-3-opus")
	for i := 0; i < 50; i++ {
		obs := NewPriorObservation(200, 0.9, true, 10)
		mp.UpdateFromObservation(obs, DefaultUpdateConfig())
	}
	ph.SetModelPriors("claude-3-opus", mp)

	effective := ph.GetEffectivePrior("coder", "claude-3-opus")

	// Model weight should be > 0
	if effective.ModelWeight <= 0 {
		t.Errorf("ModelWeight = %v, expected > 0", effective.ModelWeight)
	}

	// Global + Model should sum to ~1.0
	totalWeight := effective.GlobalWeight + effective.ModelWeight + effective.AgentModelWeight
	if math.Abs(totalWeight-1.0) > 0.01 {
		t.Errorf("Total weight = %v, expected ~1.0", totalWeight)
	}
}

func TestPriorHierarchy_GetEffectivePrior_AllLevels(t *testing.T) {
	ph := NewPriorHierarchy()

	// Add model priors
	mp := ph.GetOrCreateModelPriors("claude-3-opus")
	for i := 0; i < 30; i++ {
		obs := NewPriorObservation(150, 0.7, true, 8)
		mp.UpdateFromObservation(obs, DefaultUpdateConfig())
	}
	ph.SetModelPriors("claude-3-opus", mp)

	// Add agent+model priors
	amp := ph.GetOrCreateAgentModelPriors("coder", "claude-3-opus")
	for i := 0; i < 30; i++ {
		obs := NewPriorObservation(200, 0.9, true, 10)
		amp.UpdateFromObservation(obs, DefaultUpdateConfig())
	}
	ph.SetAgentModelPriors("coder", "claude-3-opus", amp)

	effective := ph.GetEffectivePrior("coder", "claude-3-opus")

	// All levels should have weight
	if effective.GlobalWeight <= 0 {
		t.Errorf("GlobalWeight = %v, expected > 0", effective.GlobalWeight)
	}
	if effective.ModelWeight <= 0 {
		t.Errorf("ModelWeight = %v, expected > 0", effective.ModelWeight)
	}
	if effective.AgentModelWeight <= 0 {
		t.Errorf("AgentModelWeight = %v, expected > 0", effective.AgentModelWeight)
	}

	// Weights should sum to ~1.0
	totalWeight := effective.GlobalWeight + effective.ModelWeight + effective.AgentModelWeight
	if math.Abs(totalWeight-1.0) > 0.01 {
		t.Errorf("Total weight = %v, expected ~1.0", totalWeight)
	}
}

func TestPriorHierarchy_GetEffectivePrior_FallbackBehavior(t *testing.T) {
	ph := NewPriorHierarchy()

	// Only add model priors for a different model
	mp := ph.GetOrCreateModelPriors("gpt-4")
	for i := 0; i < 50; i++ {
		obs := NewPriorObservation(100, 0.8, true, 5)
		mp.UpdateFromObservation(obs, DefaultUpdateConfig())
	}
	ph.SetModelPriors("gpt-4", mp)

	// Query for a different model - should fall back to global
	effective := ph.GetEffectivePrior("coder", "claude-3-opus")

	// Should only have global weight
	if math.Abs(effective.GlobalWeight-1.0) > 0.01 {
		t.Errorf("GlobalWeight = %v, expected ~1.0 for non-existent model", effective.GlobalWeight)
	}
}

// =============================================================================
// Test Blending with Various Confidence Levels
// =============================================================================

func TestPriorHierarchy_BlendingLowConfidence(t *testing.T) {
	ph := NewPriorHierarchy()

	// Add agent+model priors with very low confidence (few updates)
	amp := ph.GetOrCreateAgentModelPriors("coder", "claude-3-opus")
	obs := NewPriorObservation(300, 0.95, true, 15)
	amp.UpdateFromObservation(obs, DefaultUpdateConfig())
	ph.SetAgentModelPriors("coder", "claude-3-opus", amp)

	effective := ph.GetEffectivePrior("coder", "claude-3-opus")

	// With low confidence, global should dominate
	if effective.GlobalWeight < effective.AgentModelWeight {
		t.Errorf("Global weight (%v) should exceed agent+model weight (%v) with low confidence",
			effective.GlobalWeight, effective.AgentModelWeight)
	}
}

func TestPriorHierarchy_BlendingHighConfidence(t *testing.T) {
	ph := NewPriorHierarchy()

	// Use UpdateFromObservation which creates/updates both model and agent+model priors
	for i := 0; i < 100; i++ {
		obs := NewPriorObservation(300, 0.95, true, 15)
		ph.UpdateFromObservation("coder", "claude-3-opus", obs)
	}

	effective := ph.GetEffectivePrior("coder", "claude-3-opus")

	// With high confidence at agent+model level, it should have significant weight
	if effective.AgentModelWeight <= 0 {
		t.Errorf("Agent+model weight (%v) should be > 0 with high confidence",
			effective.AgentModelWeight)
	}

	// UpdateFromObservation creates both model and agent+model priors
	// so both should have weight
	if effective.ModelWeight <= 0 {
		t.Errorf("Model weight (%v) should be > 0", effective.ModelWeight)
	}

	// All weights should sum to 1
	totalWeight := effective.GlobalWeight + effective.ModelWeight + effective.AgentModelWeight
	if math.Abs(totalWeight-1.0) > 0.01 {
		t.Errorf("Total weight = %v, expected ~1.0", totalWeight)
	}

	// High confidence should give model and agent+model levels higher combined weight than global
	specificWeight := effective.ModelWeight + effective.AgentModelWeight
	if specificWeight < effective.GlobalWeight {
		t.Errorf("Combined specific weight (%v) should exceed global weight (%v) with high confidence",
			specificWeight, effective.GlobalWeight)
	}
}

// =============================================================================
// Test UpdateFromObservation
// =============================================================================

func TestPriorHierarchy_UpdateFromObservation(t *testing.T) {
	ph := NewPriorHierarchy()

	// Initially no priors
	stats := ph.GetStats()
	if stats.ModelCount != 0 || stats.AgentModelCount != 0 {
		t.Error("Initial stats should be 0")
	}

	// Update with observation - should create priors
	obs := NewPriorObservation(100, 0.8, true, 5)
	ph.UpdateFromObservation("coder", "claude-3-opus", obs)

	// Should now have priors
	stats = ph.GetStats()
	if stats.ModelCount != 1 {
		t.Errorf("ModelCount = %d, expected 1", stats.ModelCount)
	}
	if stats.AgentModelCount != 1 {
		t.Errorf("AgentModelCount = %d, expected 1", stats.AgentModelCount)
	}

	// Priors should be updated
	amp := ph.GetAgentModelPriors("coder", "claude-3-opus")
	if amp.EffectiveSamples <= 0 {
		t.Error("EffectiveSamples should be > 0 after update")
	}
}

func TestPriorHierarchy_UpdateFromObservation_Nil(t *testing.T) {
	ph := NewPriorHierarchy()

	// Should not panic
	ph.UpdateFromObservation("coder", "claude-3-opus", nil)

	stats := ph.GetStats()
	if stats.ModelCount != 0 || stats.AgentModelCount != 0 {
		t.Error("Nil observation should not create priors")
	}
}

func TestPriorHierarchy_UpdateFromObservation_Multiple(t *testing.T) {
	ph := NewPriorHierarchy()

	// Update same agent+model multiple times
	for i := 0; i < 50; i++ {
		obs := NewPriorObservation(100+i, 0.7+float64(i)/200, true, 5)
		ph.UpdateFromObservation("coder", "claude-3-opus", obs)
	}

	amp := ph.GetAgentModelPriors("coder", "claude-3-opus")
	if amp.Confidence() < 0.5 {
		t.Errorf("Confidence after 50 updates = %v, expected > 0.5", amp.Confidence())
	}
}

// =============================================================================
// Test Clear and GetStats
// =============================================================================

func TestPriorHierarchy_Clear(t *testing.T) {
	ph := NewPriorHierarchy()

	// Add priors
	ph.GetOrCreateModelPriors("claude-3-opus")
	ph.GetOrCreateModelPriors("gpt-4")
	ph.GetOrCreateAgentModelPriors("coder", "claude-3-opus")

	stats := ph.GetStats()
	if stats.ModelCount < 2 || stats.AgentModelCount < 1 {
		t.Fatal("Priors not created properly")
	}

	ph.Clear()

	stats = ph.GetStats()
	if stats.ModelCount != 0 {
		t.Errorf("ModelCount after clear = %d, expected 0", stats.ModelCount)
	}
	if stats.AgentModelCount != 0 {
		t.Errorf("AgentModelCount after clear = %d, expected 0", stats.AgentModelCount)
	}

	// Global priors should still exist
	global := ph.GetGlobalPriors()
	if global == nil {
		t.Error("Global priors should still exist after clear")
	}
}

func TestPriorHierarchy_GetStats(t *testing.T) {
	ph := NewPriorHierarchy()

	// Add multiple priors
	ph.GetOrCreateModelPriors("claude-3-opus")
	ph.GetOrCreateModelPriors("gpt-4")
	ph.GetOrCreateModelPriors("gemini")
	ph.GetOrCreateAgentModelPriors("coder", "claude-3-opus")
	ph.GetOrCreateAgentModelPriors("reviewer", "claude-3-opus")

	stats := ph.GetStats()

	if stats.ModelCount != 3 {
		t.Errorf("ModelCount = %d, expected 3", stats.ModelCount)
	}
	if stats.AgentModelCount != 2 {
		t.Errorf("AgentModelCount = %d, expected 2", stats.AgentModelCount)
	}
}

// =============================================================================
// Test JSON Serialization
// =============================================================================

func TestGlobalPriors_JSONMarshal(t *testing.T) {
	original := DefaultGlobalPriors()
	original.ContextThreshold.Update(0.9, DefaultUpdateConfig())

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded GlobalPriors
	err = json.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Verify values match
	if math.Abs(decoded.ContextThreshold.Mean()-original.ContextThreshold.Mean()) > 0.01 {
		t.Errorf("ContextThreshold mismatch: %v vs %v",
			decoded.ContextThreshold.Mean(), original.ContextThreshold.Mean())
	}
}

func TestModelPriors_JSONMarshal(t *testing.T) {
	original := NewModelPriors("claude-3-opus", nil)
	for i := 0; i < 10; i++ {
		obs := NewPriorObservation(100, 0.8, true, 5)
		original.UpdateFromObservation(obs, DefaultUpdateConfig())
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded ModelPriors
	err = json.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.ModelName != original.ModelName {
		t.Errorf("ModelName mismatch: %v vs %v", decoded.ModelName, original.ModelName)
	}

	if math.Abs(decoded.EffectiveSamples-original.EffectiveSamples) > 0.5 {
		t.Errorf("EffectiveSamples mismatch: %v vs %v",
			decoded.EffectiveSamples, original.EffectiveSamples)
	}
}

func TestAgentModelPriors_JSONMarshal(t *testing.T) {
	original := NewAgentModelPriors("coder", "claude-3-opus", nil)
	for i := 0; i < 10; i++ {
		obs := NewPriorObservation(100, 0.8, true, 5)
		original.UpdateFromObservation(obs, DefaultUpdateConfig())
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded AgentModelPriors
	err = json.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.AgentID != original.AgentID {
		t.Errorf("AgentID mismatch: %v vs %v", decoded.AgentID, original.AgentID)
	}

	if decoded.ModelName != original.ModelName {
		t.Errorf("ModelName mismatch: %v vs %v", decoded.ModelName, original.ModelName)
	}
}

func TestPriorHierarchy_JSONMarshal(t *testing.T) {
	original := NewPriorHierarchy()

	// Add priors
	ph := original
	mp := ph.GetOrCreateModelPriors("claude-3-opus")
	for i := 0; i < 10; i++ {
		obs := NewPriorObservation(100, 0.8, true, 5)
		mp.UpdateFromObservation(obs, DefaultUpdateConfig())
	}
	ph.SetModelPriors("claude-3-opus", mp)

	amp := ph.GetOrCreateAgentModelPriors("coder", "claude-3-opus")
	for i := 0; i < 10; i++ {
		obs := NewPriorObservation(150, 0.9, true, 7)
		amp.UpdateFromObservation(obs, DefaultUpdateConfig())
	}
	ph.SetAgentModelPriors("coder", "claude-3-opus", amp)

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	decoded := NewPriorHierarchy()
	err = json.Unmarshal(data, decoded)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Verify stats match
	origStats := original.GetStats()
	decStats := decoded.GetStats()

	if origStats.ModelCount != decStats.ModelCount {
		t.Errorf("ModelCount mismatch: %d vs %d",
			origStats.ModelCount, decStats.ModelCount)
	}

	if origStats.AgentModelCount != decStats.AgentModelCount {
		t.Errorf("AgentModelCount mismatch: %d vs %d",
			origStats.AgentModelCount, decStats.AgentModelCount)
	}
}

func TestPriorHierarchy_JSONMarshal_Empty(t *testing.T) {
	original := NewPriorHierarchy()

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	decoded := NewPriorHierarchy()
	err = json.Unmarshal(data, decoded)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	stats := decoded.GetStats()
	if stats.ModelCount != 0 || stats.AgentModelCount != 0 {
		t.Error("Decoded empty hierarchy should have 0 priors")
	}
}

// =============================================================================
// Test Thread Safety
// =============================================================================

func TestPriorHierarchy_ConcurrentAccess(t *testing.T) {
	ph := NewPriorHierarchy()

	var wg sync.WaitGroup
	done := make(chan bool)

	// Concurrent writers
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			obs := NewPriorObservation(100+i, 0.5+float64(i)/200, true, 5)
			ph.UpdateFromObservation("coder", "claude-3-opus", obs)
		}
	}()

	// Concurrent readers
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			ph.GetEffectivePrior("coder", "claude-3-opus")
			ph.GetStats()
		}
	}()

	// Another writer for different agent
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			obs := NewPriorObservation(200+i, 0.6+float64(i)/250, true, 7)
			ph.UpdateFromObservation("reviewer", "gpt-4", obs)
		}
	}()

	// Wait for completion with timeout
	go func() {
		wg.Wait()
		done <- true
	}()

	select {
	case <-done:
		// Success
	}

	// Verify no corruption
	stats := ph.GetStats()
	if stats.ModelCount < 0 || stats.AgentModelCount < 0 {
		t.Error("Stats should be non-negative")
	}
}

// =============================================================================
// Test No Invalid Values
// =============================================================================

func TestPriorHierarchy_NoInvalidValues(t *testing.T) {
	ph := NewPriorHierarchy()

	// Add priors with many updates
	for i := 0; i < 100; i++ {
		obs := NewPriorObservation(100+i%50, float64(i%100)/100, i%2 == 0, i%10)
		ph.UpdateFromObservation("coder", "claude-3-opus", obs)
	}

	effective := ph.GetEffectivePrior("coder", "claude-3-opus")

	assertValidPrior(t, "ContextThreshold", effective.ContextThreshold)
	assertValidPrior(t, "QualityThreshold", effective.QualityThreshold)
	assertValidPrior(t, "OptimalContextSize", float64(effective.OptimalContextSize))
	assertValidPrior(t, "PreserveRecentTurns", float64(effective.PreserveRecentTurns))
	assertValidPrior(t, "HandoffSuccessRate", effective.HandoffSuccessRate)
	assertValidPrior(t, "ContextDecayRate", effective.ContextDecayRate)
	assertValidPrior(t, "GlobalWeight", effective.GlobalWeight)
	assertValidPrior(t, "ModelWeight", effective.ModelWeight)
	assertValidPrior(t, "AgentModelWeight", effective.AgentModelWeight)
	assertValidPrior(t, "EffectiveConfidence", effective.EffectiveConfidence)
}

func assertValidPrior(t *testing.T, name string, value float64) {
	t.Helper()
	if math.IsNaN(value) {
		t.Errorf("%s is NaN", name)
	}
	if math.IsInf(value, 0) {
		t.Errorf("%s is Inf", name)
	}
}

// =============================================================================
// Test Helper Functions
// =============================================================================

func TestCloneLearnedWeight(t *testing.T) {
	original := NewLearnedWeight(5.0, 5.0)
	original.Update(0.9, DefaultUpdateConfig())

	cloned := cloneLearnedWeight(original)

	if cloned == nil {
		t.Fatal("cloneLearnedWeight returned nil")
	}

	// Verify values match immediately after clone
	if math.Abs(cloned.Mean()-original.Mean()) > 0.001 {
		t.Errorf("Cloned mean %v != original %v", cloned.Mean(), original.Mean())
	}

	// Verify independence by making many updates to original
	for i := 0; i < 50; i++ {
		original.Update(0.01, DefaultUpdateConfig())
	}
	// Original should have moved toward 0.01, clone should remain the same
	if original.Mean() > cloned.Mean() {
		t.Error("Original should have moved toward 0.01 and be less than clone")
	}
}

func TestCloneLearnedWeight_Nil(t *testing.T) {
	cloned := cloneLearnedWeight(nil)
	if cloned != nil {
		t.Error("cloneLearnedWeight(nil) should return nil")
	}
}

func TestCloneLearnedContextSize(t *testing.T) {
	original := NewLearnedContextSize(100.0, 1.0)
	original.Update(150, DefaultUpdateConfig())

	cloned := cloneLearnedContextSize(original)

	if cloned == nil {
		t.Fatal("cloneLearnedContextSize returned nil")
	}

	// Verify values match
	if cloned.Mean() != original.Mean() {
		t.Errorf("Cloned mean %v != original %v", cloned.Mean(), original.Mean())
	}
}

func TestCloneLearnedContextSize_Nil(t *testing.T) {
	cloned := cloneLearnedContextSize(nil)
	if cloned != nil {
		t.Error("cloneLearnedContextSize(nil) should return nil")
	}
}

func TestBlendLearnedWeights(t *testing.T) {
	a := NewLearnedWeight(9.0, 1.0) // Mean = 0.9
	b := NewLearnedWeight(1.0, 9.0) // Mean = 0.1

	blended := blendLearnedWeights(a, b, 0.5, 0.5)

	if blended == nil {
		t.Fatal("blendLearnedWeights returned nil")
	}

	// Blended mean should be between a and b
	mean := blended.Mean()
	if mean < 0.1 || mean > 0.9 {
		t.Errorf("Blended mean %v should be between 0.1 and 0.9", mean)
	}
}

func TestBlendLearnedWeights_NilA(t *testing.T) {
	b := NewLearnedWeight(1.0, 9.0)

	blended := blendLearnedWeights(nil, b, 0.5, 0.5)

	if blended == nil {
		t.Fatal("blendLearnedWeights returned nil")
	}

	// Should return b
	if math.Abs(blended.Mean()-b.Mean()) > 0.01 {
		t.Error("With nil a, should return b")
	}
}

func TestBlendLearnedWeights_NilB(t *testing.T) {
	a := NewLearnedWeight(9.0, 1.0)

	blended := blendLearnedWeights(a, nil, 0.5, 0.5)

	if blended == nil {
		t.Fatal("blendLearnedWeights returned nil")
	}

	// Should return a
	if math.Abs(blended.Mean()-a.Mean()) > 0.01 {
		t.Error("With nil b, should return a")
	}
}

func TestBlendLearnedWeights_BothNil(t *testing.T) {
	blended := blendLearnedWeights(nil, nil, 0.5, 0.5)

	if blended == nil {
		t.Fatal("blendLearnedWeights returned nil")
	}

	// Should return default (mean 0.5)
	if math.Abs(blended.Mean()-0.5) > 0.01 {
		t.Error("With both nil, should return default (mean 0.5)")
	}
}

func TestBlendLearnedContextSizes(t *testing.T) {
	a := NewLearnedContextSize(200.0, 1.0) // Mean = 200
	b := NewLearnedContextSize(50.0, 1.0)  // Mean = 50

	blended := blendLearnedContextSizes(a, b, 0.5, 0.5)

	if blended == nil {
		t.Fatal("blendLearnedContextSizes returned nil")
	}

	// Blended mean should be between a and b
	mean := blended.Mean()
	if mean < 50 || mean > 200 {
		t.Errorf("Blended mean %v should be between 50 and 200", mean)
	}
}

func TestBlendLearnedContextSizes_BothNil(t *testing.T) {
	blended := blendLearnedContextSizes(nil, nil, 0.5, 0.5)

	if blended == nil {
		t.Fatal("blendLearnedContextSizes returned nil")
	}

	// Should return default (mean 100)
	if blended.Mean() != 100 {
		t.Errorf("With both nil, should return default (mean 100), got %v", blended.Mean())
	}
}

// =============================================================================
// Test PriorObservation
// =============================================================================

func TestNewPriorObservation(t *testing.T) {
	obs := NewPriorObservation(150, 0.85, true, 7)

	if obs == nil {
		t.Fatal("NewPriorObservation returned nil")
	}

	if obs.ContextSize != 150 {
		t.Errorf("ContextSize = %v, expected 150", obs.ContextSize)
	}

	if math.Abs(obs.QualityScore-0.85) > 0.01 {
		t.Errorf("QualityScore = %v, expected 0.85", obs.QualityScore)
	}

	if !obs.WasSuccessful {
		t.Error("WasSuccessful should be true")
	}

	if obs.TurnNumber != 7 {
		t.Errorf("TurnNumber = %v, expected 7", obs.TurnNumber)
	}

	if obs.Timestamp.IsZero() {
		t.Error("Timestamp should not be zero")
	}
}

// =============================================================================
// Test EffectivePrior
// =============================================================================

func TestEffectivePrior_ValuesInRange(t *testing.T) {
	ph := NewPriorHierarchy()

	// Add some priors with updates
	for i := 0; i < 50; i++ {
		obs := NewPriorObservation(100, 0.7, true, 5)
		ph.UpdateFromObservation("coder", "claude-3-opus", obs)
	}

	effective := ph.GetEffectivePrior("coder", "claude-3-opus")

	// Thresholds should be in [0, 1]
	if effective.ContextThreshold < 0 || effective.ContextThreshold > 1 {
		t.Errorf("ContextThreshold %v out of [0,1]", effective.ContextThreshold)
	}

	if effective.QualityThreshold < 0 || effective.QualityThreshold > 1 {
		t.Errorf("QualityThreshold %v out of [0,1]", effective.QualityThreshold)
	}

	if effective.HandoffSuccessRate < 0 || effective.HandoffSuccessRate > 1 {
		t.Errorf("HandoffSuccessRate %v out of [0,1]", effective.HandoffSuccessRate)
	}

	if effective.ContextDecayRate < 0 || effective.ContextDecayRate > 1 {
		t.Errorf("ContextDecayRate %v out of [0,1]", effective.ContextDecayRate)
	}

	// Sizes should be positive
	if effective.OptimalContextSize < 0 {
		t.Errorf("OptimalContextSize %v should be non-negative", effective.OptimalContextSize)
	}

	if effective.PreserveRecentTurns < 0 {
		t.Errorf("PreserveRecentTurns %v should be non-negative", effective.PreserveRecentTurns)
	}

	// Weights should be in [0, 1] and sum to ~1
	if effective.GlobalWeight < 0 || effective.GlobalWeight > 1 {
		t.Errorf("GlobalWeight %v out of [0,1]", effective.GlobalWeight)
	}

	if effective.ModelWeight < 0 || effective.ModelWeight > 1 {
		t.Errorf("ModelWeight %v out of [0,1]", effective.ModelWeight)
	}

	if effective.AgentModelWeight < 0 || effective.AgentModelWeight > 1 {
		t.Errorf("AgentModelWeight %v out of [0,1]", effective.AgentModelWeight)
	}

	totalWeight := effective.GlobalWeight + effective.ModelWeight + effective.AgentModelWeight
	if math.Abs(totalWeight-1.0) > 0.01 {
		t.Errorf("Weights sum to %v, expected ~1.0", totalWeight)
	}

	if effective.EffectiveConfidence < 0 || effective.EffectiveConfidence > 1 {
		t.Errorf("EffectiveConfidence %v out of [0,1]", effective.EffectiveConfidence)
	}
}

// =============================================================================
// Benchmarks
// =============================================================================

func BenchmarkDefaultGlobalPriors(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = DefaultGlobalPriors()
	}
}

func BenchmarkModelPriors_UpdateFromObservation(b *testing.B) {
	mp := NewModelPriors("claude-3-opus", nil)
	obs := NewPriorObservation(100, 0.8, true, 5)
	config := DefaultUpdateConfig()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mp.UpdateFromObservation(obs, config)
	}
}

func BenchmarkPriorHierarchy_GetEffectivePrior(b *testing.B) {
	ph := NewPriorHierarchy()

	// Add priors
	mp := ph.GetOrCreateModelPriors("claude-3-opus")
	for i := 0; i < 20; i++ {
		obs := NewPriorObservation(100, 0.8, true, 5)
		mp.UpdateFromObservation(obs, DefaultUpdateConfig())
	}
	ph.SetModelPriors("claude-3-opus", mp)

	amp := ph.GetOrCreateAgentModelPriors("coder", "claude-3-opus")
	for i := 0; i < 20; i++ {
		obs := NewPriorObservation(100, 0.8, true, 5)
		amp.UpdateFromObservation(obs, DefaultUpdateConfig())
	}
	ph.SetAgentModelPriors("coder", "claude-3-opus", amp)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ph.GetEffectivePrior("coder", "claude-3-opus")
	}
}

func BenchmarkPriorHierarchy_UpdateFromObservation(b *testing.B) {
	ph := NewPriorHierarchy()
	obs := NewPriorObservation(100, 0.8, true, 5)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ph.UpdateFromObservation("coder", "claude-3-opus", obs)
	}
}

func BenchmarkAgentModelPriors_BlendWithParent(b *testing.B) {
	mp := NewModelPriors("claude-3-opus", nil)
	for i := 0; i < 20; i++ {
		obs := NewPriorObservation(100, 0.8, true, 5)
		mp.UpdateFromObservation(obs, DefaultUpdateConfig())
	}

	amp := NewAgentModelPriors("coder", "claude-3-opus", nil)
	for i := 0; i < 20; i++ {
		obs := NewPriorObservation(150, 0.9, true, 7)
		amp.UpdateFromObservation(obs, DefaultUpdateConfig())
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = amp.BlendWithParent(mp, 0.5)
	}
}
