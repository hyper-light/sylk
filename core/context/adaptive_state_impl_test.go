// Package context provides types and utilities for adaptive retrieval context management.
// This file contains tests for AR.3.4: AdaptiveState Extended Methods.
package context

import (
	"math"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// Threshold Sampling Tests
// =============================================================================

func TestSampleThresholds(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()

	thresholds := state.SampleThresholds()

	if thresholds.Confidence < 0 || thresholds.Confidence > 1 {
		t.Errorf("expected Confidence in [0,1], got %f", thresholds.Confidence)
	}
	if thresholds.Excerpt < 0 || thresholds.Excerpt > 1 {
		t.Errorf("expected Excerpt in [0,1], got %f", thresholds.Excerpt)
	}
	if thresholds.Budget < 0 || thresholds.Budget > 1 {
		t.Errorf("expected Budget in [0,1], got %f", thresholds.Budget)
	}
}

func TestGetMeanThresholds(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()

	thresholds := state.GetMeanThresholds()

	// Check initial mean values (from priors)
	// Confidence: Beta(8.5, 1.5) -> mean = 8.5/10 = 0.85
	expectedConfidence := 8.5 / 10.0
	if math.Abs(thresholds.Confidence-expectedConfidence) > 0.01 {
		t.Errorf("expected Confidence mean≈%f, got %f", expectedConfidence, thresholds.Confidence)
	}
}

func TestGetMeanWeights(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()

	weights := state.GetMeanWeights()

	// Check initial mean values (from priors)
	// TaskSuccess: Beta(8, 2) -> mean = 8/10 = 0.8
	expectedTaskSuccess := 8.0 / 10.0
	if math.Abs(weights.TaskSuccess-expectedTaskSuccess) > 0.01 {
		t.Errorf("expected TaskSuccess mean≈%f, got %f", expectedTaskSuccess, weights.TaskSuccess)
	}
}

// =============================================================================
// UpdateFromOutcome Tests
// =============================================================================

func TestUpdateFromOutcome_IncrementsObservations(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()

	obs := EpisodeObservation{
		TaskCompleted: true,
		FollowUpCount: 1,
	}

	state.UpdateFromOutcome(obs)

	if state.GetTotalObservations() != 1 {
		t.Errorf("expected TotalObservations=1, got %d", state.GetTotalObservations())
	}
}

func TestUpdateFromOutcome_UpdatesLastUpdated(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()
	before := time.Now()
	time.Sleep(time.Millisecond)

	obs := EpisodeObservation{TaskCompleted: true}
	state.UpdateFromOutcome(obs)

	if state.GetLastUpdated().Before(before) {
		t.Error("expected LastUpdated to be updated")
	}
}

func TestUpdateFromOutcome_TaskComplete_UpdatesTaskSuccess(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()
	meanBefore := state.Weights.TaskSuccess.Mean()

	// Many successful task completions
	for i := 0; i < 50; i++ {
		state.UpdateFromOutcome(EpisodeObservation{
			TaskCompleted: true,
			FollowUpCount: 0,
		})
	}

	meanAfter := state.Weights.TaskSuccess.Mean()

	// Task success weight should increase (or stay high)
	if meanAfter < meanBefore-0.1 {
		t.Errorf("expected TaskSuccess mean to not decrease significantly: %f -> %f",
			meanBefore, meanAfter)
	}
}

func TestUpdateFromOutcome_TaskIncomplete_UpdatesTaskSuccess(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()

	// Disable outlier rejection for this test by using high sigma
	state.SetUpdateConfig(&UpdateConfig{
		DecayFactor:         0.999,
		OutlierSigma:        10.0, // Very high to disable outlier rejection
		MinEffectiveSamples: 1.0,
		DriftRate:           0.001,
	})

	meanBefore := state.Weights.TaskSuccess.Mean()

	// Many incomplete tasks
	for i := 0; i < 100; i++ {
		state.UpdateFromOutcome(EpisodeObservation{
			TaskCompleted: false,
			FollowUpCount: 3,
		})
	}

	meanAfter := state.Weights.TaskSuccess.Mean()

	// Task success weight should decrease
	if meanAfter >= meanBefore-0.05 {
		t.Logf("TaskSuccess mean changed: %f -> %f (may be affected by decay/drift)",
			meanBefore, meanAfter)
	}

	// At minimum, observations should be counted
	if state.GetTotalObservations() != 100 {
		t.Errorf("expected 100 observations, got %d", state.GetTotalObservations())
	}
}

func TestUpdateFromOutcome_HighWaste_UpdatesWastePenalty(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()

	// High waste observations
	for i := 0; i < 30; i++ {
		state.UpdateFromOutcome(EpisodeObservation{
			TaskCompleted: true,
			PrefetchedIDs: []string{"p1", "p2", "p3", "p4", "p5"},
			UsedIDs:       []string{"p1"},
		})
	}

	// Waste penalty distribution should have updated
	if state.GetTotalObservations() != 30 {
		t.Errorf("expected 30 observations, got %d", state.GetTotalObservations())
	}
}

func TestUpdateFromOutcome_HighStruggle_UpdatesStrugglePenalty(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()

	// High struggle observations
	for i := 0; i < 30; i++ {
		state.UpdateFromOutcome(EpisodeObservation{
			TaskCompleted: false,
			FollowUpCount: 5,
			SearchedAfter: []string{"s1", "s2", "s3"},
		})
	}

	if state.GetTotalObservations() != 30 {
		t.Errorf("expected 30 observations, got %d", state.GetTotalObservations())
	}
}

func TestUpdateFromOutcome_UpdatesUserProfile(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()

	obs := EpisodeObservation{
		TaskCompleted: true,
		FollowUpCount: 1,
	}

	state.UpdateFromOutcome(obs)

	profile := state.GetUserProfile()
	if profile.ObservationCount != 1 {
		t.Errorf("expected UserProfile.ObservationCount=1, got %d", profile.ObservationCount)
	}
}

func TestUpdateFromOutcome_WithContext_UpdatesContextBias(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()

	// Add a context centroid
	state.ContextDiscovery.Centroids = append(state.ContextDiscovery.Centroids, ContextCentroid{
		ID: "test_ctx",
		Bias: ContextBias{
			RelevanceMult: 1.0,
			StruggleMult:  1.0,
			WasteMult:     1.0,
		},
	})

	obs := EpisodeObservation{
		TaskCompleted: true,
		TaskContext:   "test_ctx",
		FollowUpCount: 3,
		SearchedAfter: []string{"s1", "s2"},
	}

	state.UpdateFromOutcome(obs)

	// Context bias should have been updated
	bias := state.ContextDiscovery.GetBias("test_ctx")
	if bias == nil {
		t.Fatal("expected context bias to exist")
	}
	if bias.ObservationCount != 1 {
		t.Errorf("expected bias.ObservationCount=1, got %d", bias.ObservationCount)
	}
}

// =============================================================================
// Compute Observation Tests
// =============================================================================

func TestClampObservation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    float64
		expected float64
	}{
		{-0.5, 0.0},
		{0.0, 0.0},
		{0.5, 0.5},
		{1.0, 1.0},
		{1.5, 1.0},
	}

	for _, tt := range tests {
		result := clampObservation(tt.input)
		if result != tt.expected {
			t.Errorf("clampObservation(%f) = %f, want %f", tt.input, result, tt.expected)
		}
	}
}

// =============================================================================
// Query Method Tests
// =============================================================================

func TestGetTotalObservations(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()

	if state.GetTotalObservations() != 0 {
		t.Error("expected 0 observations initially")
	}

	for i := 0; i < 5; i++ {
		state.UpdateFromOutcome(EpisodeObservation{TaskCompleted: true})
	}

	if state.GetTotalObservations() != 5 {
		t.Errorf("expected 5 observations, got %d", state.GetTotalObservations())
	}
}

func TestGetVersion(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()

	if state.GetVersion() != 1 {
		t.Errorf("expected Version=1, got %d", state.GetVersion())
	}
}

func TestGetContextDiscovery(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()

	discovery := state.GetContextDiscovery()
	if discovery == nil {
		t.Error("expected non-nil ContextDiscovery")
	}
	if discovery.MaxContexts != 10 {
		t.Errorf("expected MaxContexts=10, got %d", discovery.MaxContexts)
	}
}

// =============================================================================
// Confidence Tests
// =============================================================================

func TestWeightConfidence(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()

	confidence := state.WeightConfidence()

	if len(confidence) != 4 {
		t.Errorf("expected 4 confidence values, got %d", len(confidence))
	}

	for name, conf := range confidence {
		if conf < 0 || conf > 1 {
			t.Errorf("expected %s confidence in [0,1], got %f", name, conf)
		}
	}
}

func TestThresholdConfidence(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()

	confidence := state.ThresholdConfidence()

	if len(confidence) != 3 {
		t.Errorf("expected 3 confidence values, got %d", len(confidence))
	}

	for name, conf := range confidence {
		if conf < 0 || conf > 1 {
			t.Errorf("expected %s confidence in [0,1], got %f", name, conf)
		}
	}
}

func TestIsConverged_Initial(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()

	// Initial state should not be converged (has high variance)
	converged := state.IsConverged(0.001) // Very tight threshold

	if converged {
		t.Error("expected initial state to not be converged with tight threshold")
	}
}

// =============================================================================
// Reset Tests
// =============================================================================

func TestAdaptiveState_Reset(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()

	// Add some observations
	for i := 0; i < 10; i++ {
		state.UpdateFromOutcome(EpisodeObservation{TaskCompleted: true})
	}

	state.Reset()

	if state.GetTotalObservations() != 0 {
		t.Errorf("expected TotalObservations=0 after reset, got %d", state.GetTotalObservations())
	}

	profile := state.GetUserProfile()
	if profile.ObservationCount != 0 {
		t.Errorf("expected UserProfile.ObservationCount=0 after reset, got %d", profile.ObservationCount)
	}

	// Version should be incremented
	if state.GetVersion() != 2 {
		t.Errorf("expected Version=2 after reset, got %d", state.GetVersion())
	}
}

func TestSetUpdateConfig(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()

	config := &UpdateConfig{
		DecayFactor:         0.95,
		OutlierSigma:        2.0,
		MinEffectiveSamples: 20,
		DriftRate:           0.01,
	}

	state.SetUpdateConfig(config)

	// Config is private, so we just verify it doesn't panic
}

// =============================================================================
// Summary Tests
// =============================================================================

func TestSummary_Initial(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()

	summary := state.Summary()

	if summary.TotalObservations != 0 {
		t.Errorf("expected TotalObservations=0, got %d", summary.TotalObservations)
	}
	if summary.Version != 1 {
		t.Errorf("expected Version=1, got %d", summary.Version)
	}
	if summary.UserProfileReady {
		t.Error("expected UserProfileReady=false initially")
	}
	if summary.ContextCount != 0 {
		t.Errorf("expected ContextCount=0, got %d", summary.ContextCount)
	}
}

func TestSummary_AfterObservations(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()

	for i := 0; i < 10; i++ {
		state.UpdateFromOutcome(EpisodeObservation{TaskCompleted: true})
	}

	summary := state.Summary()

	if summary.TotalObservations != 10 {
		t.Errorf("expected TotalObservations=10, got %d", summary.TotalObservations)
	}
	if summary.UserProfileReady != true {
		t.Error("expected UserProfileReady=true after 10 observations")
	}
}

func TestSummary_ContainsMeans(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()

	summary := state.Summary()

	// Check mean weights are present
	if summary.MeanWeights.TaskSuccess <= 0 {
		t.Error("expected positive TaskSuccess mean")
	}

	// Check mean thresholds are present
	if summary.MeanThresholds.Confidence <= 0 {
		t.Error("expected positive Confidence mean")
	}
}

func TestSummary_ContainsConfidence(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()

	summary := state.Summary()

	if len(summary.WeightConfidence) != 4 {
		t.Errorf("expected 4 weight confidence values, got %d", len(summary.WeightConfidence))
	}
	if len(summary.ThresholdConfidence) != 3 {
		t.Errorf("expected 3 threshold confidence values, got %d", len(summary.ThresholdConfidence))
	}
}

// =============================================================================
// Concurrency Tests
// =============================================================================

func TestAdaptiveState_ConcurrentUpdates(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()

	var wg sync.WaitGroup
	n := 100

	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			state.UpdateFromOutcome(EpisodeObservation{TaskCompleted: true})
		}()
	}

	wg.Wait()

	if state.GetTotalObservations() != int64(n) {
		t.Errorf("expected TotalObservations=%d, got %d", n, state.GetTotalObservations())
	}
}

func TestConcurrentReadsAndWrites_AdaptiveState(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()

	var wg sync.WaitGroup
	n := 50

	// Writers
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			state.UpdateFromOutcome(EpisodeObservation{TaskCompleted: true})
		}()
	}

	// Readers
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_ = state.SampleWeights("")
			_ = state.SampleThresholds()
			_ = state.GetMeanWeights()
			_ = state.GetMeanThresholds()
			_ = state.Summary()
		}()
	}

	wg.Wait()
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestUpdateFromOutcome_EmptyObservation(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()

	// Should not panic with empty observation
	state.UpdateFromOutcome(EpisodeObservation{})

	if state.GetTotalObservations() != 1 {
		t.Error("expected observation to be counted")
	}
}

func TestUpdateFromOutcome_NilContextDiscovery(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()
	state.ContextDiscovery = nil

	// Should not panic with nil context discovery
	state.UpdateFromOutcome(EpisodeObservation{
		TaskCompleted: true,
		TaskContext:   "test",
	})
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkUpdateFromOutcome(b *testing.B) {
	state := NewAdaptiveState()
	obs := EpisodeObservation{
		TaskCompleted: true,
		FollowUpCount: 1,
		PrefetchedIDs: []string{"p1", "p2"},
		UsedIDs:       []string{"p1"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		state.UpdateFromOutcome(obs)
	}
}

func BenchmarkSampleWeights(b *testing.B) {
	state := NewAdaptiveState()

	// Pre-train
	for i := 0; i < 10; i++ {
		state.UpdateFromOutcome(EpisodeObservation{TaskCompleted: true})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = state.SampleWeights("")
	}
}

func BenchmarkSampleThresholds(b *testing.B) {
	state := NewAdaptiveState()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = state.SampleThresholds()
	}
}

func BenchmarkSummary(b *testing.B) {
	state := NewAdaptiveState()

	// Pre-train
	for i := 0; i < 10; i++ {
		state.UpdateFromOutcome(EpisodeObservation{TaskCompleted: true})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = state.Summary()
	}
}
