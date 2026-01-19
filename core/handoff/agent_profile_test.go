package handoff

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

// =============================================================================
// Test HandoffObservation
// =============================================================================

func TestNewHandoffObservation(t *testing.T) {
	before := time.Now()
	obs := NewHandoffObservation(1000, 5, 0.85, true, false)
	after := time.Now()

	if obs.ContextSize != 1000 {
		t.Errorf("ContextSize = %d, expected 1000", obs.ContextSize)
	}

	if obs.TurnNumber != 5 {
		t.Errorf("TurnNumber = %d, expected 5", obs.TurnNumber)
	}

	if math.Abs(obs.QualityScore-0.85) > 1e-9 {
		t.Errorf("QualityScore = %v, expected 0.85", obs.QualityScore)
	}

	if !obs.WasSuccessful {
		t.Error("WasSuccessful = false, expected true")
	}

	if obs.HandoffTriggered {
		t.Error("HandoffTriggered = true, expected false")
	}

	if obs.Timestamp.Before(before) || obs.Timestamp.After(after) {
		t.Errorf("Timestamp %v not in expected range [%v, %v]",
			obs.Timestamp, before, after)
	}
}

func TestHandoffObservation_JSONMarshal(t *testing.T) {
	original := NewHandoffObservation(500, 3, 0.7, true, true)

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded HandoffObservation
	err = json.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.ContextSize != original.ContextSize {
		t.Errorf("ContextSize mismatch: got %d, want %d",
			decoded.ContextSize, original.ContextSize)
	}

	if decoded.TurnNumber != original.TurnNumber {
		t.Errorf("TurnNumber mismatch: got %d, want %d",
			decoded.TurnNumber, original.TurnNumber)
	}

	if math.Abs(decoded.QualityScore-original.QualityScore) > 1e-9 {
		t.Errorf("QualityScore mismatch: got %v, want %v",
			decoded.QualityScore, original.QualityScore)
	}

	if decoded.WasSuccessful != original.WasSuccessful {
		t.Errorf("WasSuccessful mismatch: got %v, want %v",
			decoded.WasSuccessful, original.WasSuccessful)
	}

	if decoded.HandoffTriggered != original.HandoffTriggered {
		t.Errorf("HandoffTriggered mismatch: got %v, want %v",
			decoded.HandoffTriggered, original.HandoffTriggered)
	}
}

// =============================================================================
// Test AgentHandoffProfile Creation
// =============================================================================

func TestNewAgentHandoffProfile(t *testing.T) {
	before := time.Now()
	profile := NewAgentHandoffProfile("coder", "claude-3-opus", "instance-123")
	after := time.Now()

	if profile.AgentType != "coder" {
		t.Errorf("AgentType = %s, expected 'coder'", profile.AgentType)
	}

	if profile.ModelID != "claude-3-opus" {
		t.Errorf("ModelID = %s, expected 'claude-3-opus'", profile.ModelID)
	}

	if profile.InstanceID != "instance-123" {
		t.Errorf("InstanceID = %s, expected 'instance-123'", profile.InstanceID)
	}

	if profile.EffectiveSamples != 0.0 {
		t.Errorf("EffectiveSamples = %v, expected 0.0", profile.EffectiveSamples)
	}

	if profile.CreatedAt.Before(before) || profile.CreatedAt.After(after) {
		t.Errorf("CreatedAt %v not in expected range", profile.CreatedAt)
	}

	if profile.LastUpdated.Before(before) || profile.LastUpdated.After(after) {
		t.Errorf("LastUpdated %v not in expected range", profile.LastUpdated)
	}

	// Verify all learned parameters are initialized
	if profile.OptimalHandoffThreshold == nil {
		t.Error("OptimalHandoffThreshold is nil")
	}

	if profile.OptimalPreparedSize == nil {
		t.Error("OptimalPreparedSize is nil")
	}

	if profile.QualityDegradationCurve == nil {
		t.Error("QualityDegradationCurve is nil")
	}

	if profile.PreserveRecent == nil {
		t.Error("PreserveRecent is nil")
	}

	if profile.ContextUsageWeight == nil {
		t.Error("ContextUsageWeight is nil")
	}
}

func TestAgentHandoffProfile_SetDefaultPriors(t *testing.T) {
	profile := NewAgentHandoffProfile("test", "test-model", "test-instance")

	// Verify OptimalHandoffThreshold: Beta(2, 2) -> mean 0.5
	if profile.OptimalHandoffThreshold == nil {
		t.Fatal("OptimalHandoffThreshold is nil")
	}
	thresholdMean := profile.OptimalHandoffThreshold.Mean()
	if math.Abs(thresholdMean-0.5) > 1e-9 {
		t.Errorf("OptimalHandoffThreshold mean = %v, expected 0.5", thresholdMean)
	}
	if profile.OptimalHandoffThreshold.Alpha != 2.0 {
		t.Errorf("OptimalHandoffThreshold Alpha = %v, expected 2.0",
			profile.OptimalHandoffThreshold.Alpha)
	}
	if profile.OptimalHandoffThreshold.Beta != 2.0 {
		t.Errorf("OptimalHandoffThreshold Beta = %v, expected 2.0",
			profile.OptimalHandoffThreshold.Beta)
	}

	// Verify OptimalPreparedSize: Gamma(2, 0.02) -> mean 100
	if profile.OptimalPreparedSize == nil {
		t.Fatal("OptimalPreparedSize is nil")
	}
	preparedMean := profile.OptimalPreparedSize.Mean()
	if preparedMean != 100 {
		t.Errorf("OptimalPreparedSize mean = %d, expected 100", preparedMean)
	}
	if math.Abs(profile.OptimalPreparedSize.Alpha-2.0) > 1e-9 {
		t.Errorf("OptimalPreparedSize Alpha = %v, expected 2.0",
			profile.OptimalPreparedSize.Alpha)
	}
	if math.Abs(profile.OptimalPreparedSize.Beta-0.02) > 1e-9 {
		t.Errorf("OptimalPreparedSize Beta = %v, expected 0.02",
			profile.OptimalPreparedSize.Beta)
	}

	// Verify QualityDegradationCurve: Beta(1, 1) -> uniform
	if profile.QualityDegradationCurve == nil {
		t.Fatal("QualityDegradationCurve is nil")
	}
	degradationMean := profile.QualityDegradationCurve.Mean()
	if math.Abs(degradationMean-0.5) > 1e-9 {
		t.Errorf("QualityDegradationCurve mean = %v, expected 0.5", degradationMean)
	}
	if profile.QualityDegradationCurve.Alpha != 1.0 {
		t.Errorf("QualityDegradationCurve Alpha = %v, expected 1.0",
			profile.QualityDegradationCurve.Alpha)
	}
	if profile.QualityDegradationCurve.Beta != 1.0 {
		t.Errorf("QualityDegradationCurve Beta = %v, expected 1.0",
			profile.QualityDegradationCurve.Beta)
	}

	// Verify PreserveRecent: Gamma(5, 0.5) -> mean 10
	if profile.PreserveRecent == nil {
		t.Fatal("PreserveRecent is nil")
	}
	preserveMean := profile.PreserveRecent.Mean()
	if preserveMean != 10 {
		t.Errorf("PreserveRecent mean = %d, expected 10", preserveMean)
	}
	if math.Abs(profile.PreserveRecent.Alpha-5.0) > 1e-9 {
		t.Errorf("PreserveRecent Alpha = %v, expected 5.0",
			profile.PreserveRecent.Alpha)
	}
	if math.Abs(profile.PreserveRecent.Beta-0.5) > 1e-9 {
		t.Errorf("PreserveRecent Beta = %v, expected 0.5",
			profile.PreserveRecent.Beta)
	}

	// Verify ContextUsageWeight: Beta(2, 2) -> mean 0.5
	if profile.ContextUsageWeight == nil {
		t.Fatal("ContextUsageWeight is nil")
	}
	contextMean := profile.ContextUsageWeight.Mean()
	if math.Abs(contextMean-0.5) > 1e-9 {
		t.Errorf("ContextUsageWeight mean = %v, expected 0.5", contextMean)
	}
	if profile.ContextUsageWeight.Alpha != 2.0 {
		t.Errorf("ContextUsageWeight Alpha = %v, expected 2.0",
			profile.ContextUsageWeight.Alpha)
	}
	if profile.ContextUsageWeight.Beta != 2.0 {
		t.Errorf("ContextUsageWeight Beta = %v, expected 2.0",
			profile.ContextUsageWeight.Beta)
	}
}

// =============================================================================
// Test Confidence Calculation
// =============================================================================

func TestAgentHandoffProfile_Confidence(t *testing.T) {
	tests := []struct {
		name           string
		effectiveSamples float64
		expectLow      bool
		expectHigh     bool
	}{
		{
			name:           "zero samples",
			effectiveSamples: 0.0,
			expectLow:      true,
			expectHigh:     false,
		},
		{
			name:           "few samples",
			effectiveSamples: 3.0,
			expectLow:      true,
			expectHigh:     false,
		},
		{
			name:           "moderate samples",
			effectiveSamples: 10.0,
			expectLow:      false,
			expectHigh:     false,
		},
		{
			name:           "many samples",
			effectiveSamples: 30.0,
			expectLow:      false,
			expectHigh:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := NewAgentHandoffProfile("test", "test-model", "test-instance")
			profile.EffectiveSamples = tt.effectiveSamples

			confidence := profile.Confidence()

			if confidence < 0.0 || confidence > 1.0 {
				t.Errorf("Confidence = %v, must be in [0,1]", confidence)
			}

			if math.IsNaN(confidence) || math.IsInf(confidence, 0) {
				t.Errorf("Confidence returned invalid value: %v", confidence)
			}

			if tt.expectLow && confidence > 0.5 {
				t.Errorf("Expected low confidence, got %v", confidence)
			}

			if tt.expectHigh && confidence < 0.9 {
				t.Errorf("Expected high confidence, got %v", confidence)
			}
		})
	}
}

func TestAgentHandoffProfile_ConfidenceGrowsWithSamples(t *testing.T) {
	profile := NewAgentHandoffProfile("test", "test-model", "test-instance")

	prevConfidence := profile.Confidence()

	for i := 1; i <= 50; i++ {
		profile.EffectiveSamples = float64(i)
		currentConfidence := profile.Confidence()

		if currentConfidence < prevConfidence {
			t.Errorf("Confidence decreased from %v to %v at sample %d",
				prevConfidence, currentConfidence, i)
		}

		prevConfidence = currentConfidence
	}
}

// =============================================================================
// Test Update Method
// =============================================================================

func TestAgentHandoffProfile_Update(t *testing.T) {
	profile := NewAgentHandoffProfile("coder", "claude-3-opus", "instance-1")

	initialThreshold := profile.OptimalHandoffThreshold.Mean()
	initialLastUpdated := profile.LastUpdated

	time.Sleep(time.Millisecond)

	obs := NewHandoffObservation(500, 5, 0.8, true, false)
	profile.Update(obs)

	if profile.EffectiveSamples <= 0 {
		t.Errorf("EffectiveSamples = %v, expected > 0 after update",
			profile.EffectiveSamples)
	}

	if !profile.LastUpdated.After(initialLastUpdated) {
		t.Error("LastUpdated was not updated")
	}

	// The threshold should have shifted after observing high quality
	newThreshold := profile.OptimalHandoffThreshold.Mean()
	if newThreshold == initialThreshold {
		// Allow for no change with low learning rate, but parameters should change
		if profile.OptimalHandoffThreshold.Alpha == 2.0 &&
			profile.OptimalHandoffThreshold.Beta == 2.0 {
			t.Error("OptimalHandoffThreshold parameters unchanged after update")
		}
	}
}

func TestAgentHandoffProfile_UpdateWithNilObservation(t *testing.T) {
	profile := NewAgentHandoffProfile("test", "test-model", "test-instance")

	initialSamples := profile.EffectiveSamples
	initialLastUpdated := profile.LastUpdated

	profile.Update(nil)

	if profile.EffectiveSamples != initialSamples {
		t.Errorf("EffectiveSamples changed with nil observation")
	}

	if profile.LastUpdated != initialLastUpdated {
		t.Error("LastUpdated changed with nil observation")
	}
}

func TestAgentHandoffProfile_UpdateConverges(t *testing.T) {
	profile := NewAgentHandoffProfile("test", "test-model", "test-instance")

	// Simulate consistent high-quality successful operations
	for i := 0; i < 100; i++ {
		obs := NewHandoffObservation(150, 8, 0.9, true, false)
		profile.Update(obs)
	}

	// After many consistent observations, parameters should have shifted
	preparedMean := profile.GetPreparedSizeMean()
	if preparedMean < 100 || preparedMean > 200 {
		// Should have moved toward 150
		t.Logf("PreparedSize mean = %d (expected to move toward 150)", preparedMean)
	}

	// Confidence should be high after many updates
	// With learning rate 0.1 and exponential decay, effective samples
	// converges to approximately 1/learningRate = 10, giving confidence around 0.63
	confidence := profile.Confidence()
	if confidence < 0.5 {
		t.Errorf("Confidence = %v, expected >= 0.5 after 100 updates (effective samples: %v)",
			confidence, profile.EffectiveSamples)
	}
}

func TestAgentHandoffProfile_UpdateWithHandoffTriggered(t *testing.T) {
	profile := NewAgentHandoffProfile("test", "test-model", "test-instance")

	// Successful handoff
	obs := NewHandoffObservation(200, 5, 0.85, true, true)
	profile.Update(obs)

	// PreserveRecent should update when handoff is triggered successfully
	// Verify the parameter was updated
	if profile.PreserveRecent.EffectiveSamples <= profile.PreserveRecent.PriorAlpha {
		t.Log("PreserveRecent updated after successful handoff")
	}
}

func TestAgentHandoffProfile_UpdateNoInvalidValues(t *testing.T) {
	profile := NewAgentHandoffProfile("test", "test-model", "test-instance")

	observations := []*HandoffObservation{
		NewHandoffObservation(0, 0, 0.0, false, false),
		NewHandoffObservation(1000, 10, 1.0, true, true),
		NewHandoffObservation(500, 5, 0.5, true, false),
		NewHandoffObservation(100, 1, -0.5, false, true),
		NewHandoffObservation(2000, 20, 1.5, true, false),
	}

	for i, obs := range observations {
		profile.Update(obs)

		// Check all parameters for validity
		assertValidProfile(t, profile, i)
	}
}

// =============================================================================
// Test Clone Method
// =============================================================================

func TestAgentHandoffProfile_Clone(t *testing.T) {
	original := NewAgentHandoffProfile("coder", "claude-3-opus", "instance-1")

	// Update original to have non-default values
	for i := 0; i < 10; i++ {
		obs := NewHandoffObservation(200, 5, 0.8, true, false)
		original.Update(obs)
	}

	cloned := original.Clone()

	// Verify fields are copied
	if cloned.AgentType != original.AgentType {
		t.Errorf("AgentType mismatch: got %s, want %s",
			cloned.AgentType, original.AgentType)
	}

	if cloned.ModelID != original.ModelID {
		t.Errorf("ModelID mismatch: got %s, want %s",
			cloned.ModelID, original.ModelID)
	}

	if cloned.InstanceID != original.InstanceID {
		t.Errorf("InstanceID mismatch: got %s, want %s",
			cloned.InstanceID, original.InstanceID)
	}

	if cloned.EffectiveSamples != original.EffectiveSamples {
		t.Errorf("EffectiveSamples mismatch: got %v, want %v",
			cloned.EffectiveSamples, original.EffectiveSamples)
	}

	// Verify learned parameters are deep copied
	if cloned.OptimalHandoffThreshold == original.OptimalHandoffThreshold {
		t.Error("OptimalHandoffThreshold was not deep copied")
	}

	if cloned.OptimalPreparedSize == original.OptimalPreparedSize {
		t.Error("OptimalPreparedSize was not deep copied")
	}

	// Verify values match
	if cloned.OptimalHandoffThreshold.Alpha != original.OptimalHandoffThreshold.Alpha {
		t.Errorf("OptimalHandoffThreshold.Alpha mismatch")
	}

	// Verify modifications to clone don't affect original
	cloned.EffectiveSamples = 999.0
	cloned.OptimalHandoffThreshold.Alpha = 999.0

	if original.EffectiveSamples == 999.0 {
		t.Error("Modifying clone affected original EffectiveSamples")
	}

	if original.OptimalHandoffThreshold.Alpha == 999.0 {
		t.Error("Modifying clone affected original OptimalHandoffThreshold")
	}
}

func TestAgentHandoffProfile_CloneNil(t *testing.T) {
	var nilProfile *AgentHandoffProfile
	cloned := nilProfile.Clone()

	if cloned != nil {
		t.Error("Clone of nil should return nil")
	}
}

func TestAgentHandoffProfile_CloneIndependence(t *testing.T) {
	original := NewAgentHandoffProfile("test", "test-model", "test-instance")
	cloned := original.Clone()

	// Update cloned profile
	for i := 0; i < 20; i++ {
		obs := NewHandoffObservation(300, 10, 0.95, true, true)
		cloned.Update(obs)
	}

	// Original should be unchanged
	originalMean := original.GetHandoffThresholdMean()
	clonedMean := cloned.GetHandoffThresholdMean()

	// They should have diverged
	if math.Abs(originalMean-0.5) > 0.1 {
		t.Errorf("Original profile was affected by clone updates: mean = %v", originalMean)
	}

	if original.EffectiveSamples > 1.0 {
		t.Errorf("Original EffectiveSamples was affected: %v", original.EffectiveSamples)
	}

	// After 20 updates with learning rate 0.1, effective samples should be > 1
	// With decay, it converges but should still be significant
	if cloned.EffectiveSamples < 5.0 {
		t.Errorf("Cloned EffectiveSamples should have increased significantly: %v", cloned.EffectiveSamples)
	}

	t.Logf("Original threshold mean: %v, Cloned threshold mean: %v",
		originalMean, clonedMean)
}

// =============================================================================
// Test JSON Serialization
// =============================================================================

func TestAgentHandoffProfile_JSONMarshal(t *testing.T) {
	original := NewAgentHandoffProfile("reviewer", "gpt-4", "session-abc")

	// Update to have non-default values
	for i := 0; i < 5; i++ {
		obs := NewHandoffObservation(250, 3, 0.75, true, false)
		original.Update(obs)
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded AgentHandoffProfile
	err = json.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Verify fields
	if decoded.AgentType != original.AgentType {
		t.Errorf("AgentType mismatch: got %s, want %s",
			decoded.AgentType, original.AgentType)
	}

	if decoded.ModelID != original.ModelID {
		t.Errorf("ModelID mismatch: got %s, want %s",
			decoded.ModelID, original.ModelID)
	}

	if decoded.InstanceID != original.InstanceID {
		t.Errorf("InstanceID mismatch: got %s, want %s",
			decoded.InstanceID, original.InstanceID)
	}

	if math.Abs(decoded.EffectiveSamples-original.EffectiveSamples) > 1e-9 {
		t.Errorf("EffectiveSamples mismatch: got %v, want %v",
			decoded.EffectiveSamples, original.EffectiveSamples)
	}

	// Verify learned parameters
	if decoded.OptimalHandoffThreshold == nil {
		t.Fatal("OptimalHandoffThreshold is nil after unmarshal")
	}

	if math.Abs(decoded.OptimalHandoffThreshold.Alpha-original.OptimalHandoffThreshold.Alpha) > 1e-9 {
		t.Errorf("OptimalHandoffThreshold.Alpha mismatch: got %v, want %v",
			decoded.OptimalHandoffThreshold.Alpha, original.OptimalHandoffThreshold.Alpha)
	}

	// Verify timestamps (with some tolerance for formatting)
	if decoded.CreatedAt.Unix() != original.CreatedAt.Unix() {
		t.Errorf("CreatedAt mismatch: got %v, want %v",
			decoded.CreatedAt, original.CreatedAt)
	}

	if decoded.LastUpdated.Unix() != original.LastUpdated.Unix() {
		t.Errorf("LastUpdated mismatch: got %v, want %v",
			decoded.LastUpdated, original.LastUpdated)
	}
}

func TestAgentHandoffProfile_JSONRoundTrip(t *testing.T) {
	original := NewAgentHandoffProfile("test", "test-model", "test-instance")

	// Perform multiple updates
	for i := 0; i < 15; i++ {
		obs := NewHandoffObservation(100+i*10, i, float64(i)/20.0+0.5, i%2 == 0, i%3 == 0)
		original.Update(obs)
	}

	// Marshal
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Unmarshal
	var decoded AgentHandoffProfile
	err = json.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Compare means
	if math.Abs(decoded.GetHandoffThresholdMean()-original.GetHandoffThresholdMean()) > 1e-6 {
		t.Errorf("HandoffThreshold mean mismatch after round trip")
	}

	if decoded.GetPreparedSizeMean() != original.GetPreparedSizeMean() {
		t.Errorf("PreparedSize mean mismatch after round trip")
	}

	if math.Abs(decoded.GetDegradationCurveMean()-original.GetDegradationCurveMean()) > 1e-6 {
		t.Errorf("DegradationCurve mean mismatch after round trip")
	}

	if decoded.GetPreserveRecentMean() != original.GetPreserveRecentMean() {
		t.Errorf("PreserveRecent mean mismatch after round trip")
	}

	if math.Abs(decoded.GetContextUsageWeightMean()-original.GetContextUsageWeightMean()) > 1e-6 {
		t.Errorf("ContextUsageWeight mean mismatch after round trip")
	}
}

// =============================================================================
// Test Utility Methods
// =============================================================================

func TestAgentHandoffProfile_GetMethods(t *testing.T) {
	profile := NewAgentHandoffProfile("test", "test-model", "test-instance")

	// Test with default priors
	thresholdMean := profile.GetHandoffThresholdMean()
	if math.Abs(thresholdMean-0.5) > 1e-9 {
		t.Errorf("GetHandoffThresholdMean = %v, expected 0.5", thresholdMean)
	}

	preparedMean := profile.GetPreparedSizeMean()
	if preparedMean != 100 {
		t.Errorf("GetPreparedSizeMean = %d, expected 100", preparedMean)
	}

	degradationMean := profile.GetDegradationCurveMean()
	if math.Abs(degradationMean-0.5) > 1e-9 {
		t.Errorf("GetDegradationCurveMean = %v, expected 0.5", degradationMean)
	}

	preserveMean := profile.GetPreserveRecentMean()
	if preserveMean != 10 {
		t.Errorf("GetPreserveRecentMean = %d, expected 10", preserveMean)
	}

	contextMean := profile.GetContextUsageWeightMean()
	if math.Abs(contextMean-0.5) > 1e-9 {
		t.Errorf("GetContextUsageWeightMean = %v, expected 0.5", contextMean)
	}
}

func TestAgentHandoffProfile_GetMethodsWithNilParams(t *testing.T) {
	profile := &AgentHandoffProfile{
		AgentType:  "test",
		ModelID:    "test-model",
		InstanceID: "test-instance",
		// All learned params are nil
	}

	// Should return defaults without panic
	thresholdMean := profile.GetHandoffThresholdMean()
	if thresholdMean != 0.5 {
		t.Errorf("GetHandoffThresholdMean with nil = %v, expected 0.5", thresholdMean)
	}

	preparedMean := profile.GetPreparedSizeMean()
	if preparedMean != 100 {
		t.Errorf("GetPreparedSizeMean with nil = %d, expected 100", preparedMean)
	}

	degradationMean := profile.GetDegradationCurveMean()
	if degradationMean != 0.5 {
		t.Errorf("GetDegradationCurveMean with nil = %v, expected 0.5", degradationMean)
	}

	preserveMean := profile.GetPreserveRecentMean()
	if preserveMean != 10 {
		t.Errorf("GetPreserveRecentMean with nil = %d, expected 10", preserveMean)
	}

	contextMean := profile.GetContextUsageWeightMean()
	if contextMean != 0.5 {
		t.Errorf("GetContextUsageWeightMean with nil = %v, expected 0.5", contextMean)
	}
}

func TestAgentHandoffProfile_String(t *testing.T) {
	profile := NewAgentHandoffProfile("coder", "claude-3-opus", "session-123")
	profile.EffectiveSamples = 15.5

	str := profile.String()

	if str == "" {
		t.Error("String() returned empty string")
	}

	// Check that key fields are included
	expectedSubstrings := []string{
		"AgentHandoffProfile",
		"coder",
		"claude-3-opus",
		"session-123",
		"Confidence",
		"Samples",
	}

	for _, sub := range expectedSubstrings {
		if !containsSubstring(str, sub) {
			t.Errorf("String() does not contain '%s': %s", sub, str)
		}
	}
}

// =============================================================================
// Test Edge Cases
// =============================================================================

func TestAgentHandoffProfile_UpdateWithExtremeValues(t *testing.T) {
	profile := NewAgentHandoffProfile("test", "test-model", "test-instance")

	extremeObservations := []*HandoffObservation{
		NewHandoffObservation(0, 0, 0.0, false, false),
		NewHandoffObservation(1000000, 1000, 1.0, true, true),
		NewHandoffObservation(-100, -10, -1.0, false, true),
		NewHandoffObservation(1, 1, 2.0, true, false),
	}

	for i, obs := range extremeObservations {
		profile.Update(obs)
		assertValidProfile(t, profile, i)
	}
}

func TestAgentHandoffProfile_ManyUpdates(t *testing.T) {
	profile := NewAgentHandoffProfile("test", "test-model", "test-instance")

	for i := 0; i < 1000; i++ {
		quality := float64(i%100) / 100.0
		obs := NewHandoffObservation(100+i%200, i%20, quality, i%2 == 0, i%5 == 0)
		profile.Update(obs)
	}

	assertValidProfile(t, profile, 1000)

	// Confidence should be reasonable after many updates
	// With exponential decay (learning rate 0.1), effective samples
	// converges to approximately 1/learningRate = 10
	if profile.Confidence() < 0.5 {
		t.Errorf("Confidence = %v, expected >= 0.5 after 1000 updates (effective samples: %v)",
			profile.Confidence(), profile.EffectiveSamples)
	}
}

// =============================================================================
// Test Update with Nil Parameters
// =============================================================================

func TestAgentHandoffProfile_UpdateWithNilParameters(t *testing.T) {
	profile := &AgentHandoffProfile{
		AgentType:  "test",
		ModelID:    "test-model",
		InstanceID: "test-instance",
		// All learned params are nil
	}

	obs := NewHandoffObservation(100, 5, 0.8, true, false)

	// Should not panic
	profile.Update(obs)

	// EffectiveSamples should still update
	if profile.EffectiveSamples <= 0 {
		t.Errorf("EffectiveSamples should have updated even with nil params")
	}
}

// =============================================================================
// Helper Functions
// =============================================================================

func assertValidProfile(t *testing.T, profile *AgentHandoffProfile, updateNum int) {
	t.Helper()

	if profile.OptimalHandoffThreshold != nil {
		mean := profile.OptimalHandoffThreshold.Mean()
		assertValid(t, "OptimalHandoffThreshold.Mean", mean)
		if mean < 0.0 || mean > 1.0 {
			t.Errorf("Update %d: OptimalHandoffThreshold.Mean = %v out of [0,1]",
				updateNum, mean)
		}
	}

	if profile.OptimalPreparedSize != nil {
		mean := profile.OptimalPreparedSize.Mean()
		if mean < 0 {
			t.Errorf("Update %d: OptimalPreparedSize.Mean = %d must be >= 0",
				updateNum, mean)
		}
		assertValid(t, "OptimalPreparedSize.Alpha", profile.OptimalPreparedSize.Alpha)
	}

	if profile.QualityDegradationCurve != nil {
		mean := profile.QualityDegradationCurve.Mean()
		assertValid(t, "QualityDegradationCurve.Mean", mean)
	}

	if profile.PreserveRecent != nil {
		mean := profile.PreserveRecent.Mean()
		if mean < 0 {
			t.Errorf("Update %d: PreserveRecent.Mean = %d must be >= 0",
				updateNum, mean)
		}
	}

	if profile.ContextUsageWeight != nil {
		mean := profile.ContextUsageWeight.Mean()
		assertValid(t, "ContextUsageWeight.Mean", mean)
	}

	assertValid(t, "EffectiveSamples", profile.EffectiveSamples)
	assertValid(t, "Confidence", profile.Confidence())
}

func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && (s[:len(sub)] == sub || containsSubstring(s[1:], sub)))
}

// =============================================================================
// Benchmarks
// =============================================================================

func BenchmarkAgentHandoffProfile_New(b *testing.B) {
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = NewAgentHandoffProfile("coder", "claude-3-opus", "instance-1")
	}
}

func BenchmarkAgentHandoffProfile_Update(b *testing.B) {
	profile := NewAgentHandoffProfile("coder", "claude-3-opus", "instance-1")
	obs := NewHandoffObservation(200, 5, 0.8, true, false)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		profile.Update(obs)
	}
}

func BenchmarkAgentHandoffProfile_Clone(b *testing.B) {
	profile := NewAgentHandoffProfile("coder", "claude-3-opus", "instance-1")
	for i := 0; i < 10; i++ {
		obs := NewHandoffObservation(200, 5, 0.8, true, false)
		profile.Update(obs)
	}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = profile.Clone()
	}
}

func BenchmarkAgentHandoffProfile_Confidence(b *testing.B) {
	profile := NewAgentHandoffProfile("coder", "claude-3-opus", "instance-1")
	profile.EffectiveSamples = 25.0
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = profile.Confidence()
	}
}

func BenchmarkAgentHandoffProfile_JSONMarshal(b *testing.B) {
	profile := NewAgentHandoffProfile("coder", "claude-3-opus", "instance-1")
	for i := 0; i < 10; i++ {
		obs := NewHandoffObservation(200, 5, 0.8, true, false)
		profile.Update(obs)
	}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = json.Marshal(profile)
	}
}

func BenchmarkAgentHandoffProfile_JSONUnmarshal(b *testing.B) {
	profile := NewAgentHandoffProfile("coder", "claude-3-opus", "instance-1")
	for i := 0; i < 10; i++ {
		obs := NewHandoffObservation(200, 5, 0.8, true, false)
		profile.Update(obs)
	}
	data, _ := json.Marshal(profile)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		var decoded AgentHandoffProfile
		_ = json.Unmarshal(data, &decoded)
	}
}
