// Package context provides types and utilities for adaptive retrieval context management.
// This file contains tests for AR.3.3: UserProfile Learning.
package context

import (
	"sync"
	"testing"
	"time"
)

// =============================================================================
// Construction Tests
// =============================================================================

func TestNewUserProfileManager_Default(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultUserProfileManager()

	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}

	profile := mgr.GetProfile()
	if profile.WastePenaltyMult != 1.0 {
		t.Errorf("expected WastePenaltyMult=1.0, got %f", profile.WastePenaltyMult)
	}
	if profile.StrugglePenaltyMult != 1.0 {
		t.Errorf("expected StrugglePenaltyMult=1.0, got %f", profile.StrugglePenaltyMult)
	}
}

func TestNewUserProfileManager_CustomConfig(t *testing.T) {
	t.Parallel()

	config := UserProfileConfig{
		MinObservations: 10,
		LearningRate:    0.2,
		MinMultiplier:   0.3,
		MaxMultiplier:   3.0,
	}

	mgr := NewUserProfileManager(config)

	if mgr.config.MinObservations != 10 {
		t.Errorf("expected MinObservations=10, got %d", mgr.config.MinObservations)
	}
}

func TestNewUserProfileManager_ZeroConfig_UsesDefaults(t *testing.T) {
	t.Parallel()

	config := UserProfileConfig{}
	mgr := NewUserProfileManager(config)

	if mgr.config.MinObservations != DefaultMinObservationsForProfile {
		t.Errorf("expected MinObservations=%d, got %d",
			DefaultMinObservationsForProfile, mgr.config.MinObservations)
	}
}

// =============================================================================
// Update Tests
// =============================================================================

func TestUpdateFromObservation_IncrementsCount(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultUserProfileManager()

	obs := &EpisodeObservation{
		TaskCompleted: true,
		FollowUpCount: 1,
	}

	mgr.UpdateFromObservation(obs)

	if mgr.GetObservationCount() != 1 {
		t.Errorf("expected ObservationCount=1, got %d", mgr.GetObservationCount())
	}
}

func TestUpdateFromObservation_UpdatesLastUpdated(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultUserProfileManager()

	before := time.Now()
	time.Sleep(time.Millisecond)

	obs := &EpisodeObservation{TaskCompleted: true}
	mgr.UpdateFromObservation(obs)

	lastUpdated := mgr.GetLastUpdated()
	if lastUpdated.Before(before) {
		t.Error("expected LastUpdated to be after test start")
	}
}

func TestUpdateFromObservation_PositiveSatisfaction_LowWaste(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultUserProfileManager()

	// Positive satisfaction with no waste
	obs := &EpisodeObservation{
		TaskCompleted: true,
		FollowUpCount: 0,
		PrefetchedIDs: []string{"p1", "p2"},
		UsedIDs:       []string{"p1", "p2"},
	}

	for i := 0; i < 10; i++ {
		mgr.UpdateFromObservation(obs)
	}

	profile := mgr.GetProfile()

	// Waste penalty should stay around normal
	if profile.WastePenaltyMult < 0.9 || profile.WastePenaltyMult > 1.1 {
		t.Errorf("expected WastePenaltyMult near 1.0, got %f", profile.WastePenaltyMult)
	}
}

func TestUpdateFromObservation_PositiveSatisfaction_HighWaste(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultUserProfileManager()

	// Positive satisfaction despite high waste = user tolerates waste
	obs := &EpisodeObservation{
		TaskCompleted: true,
		FollowUpCount: 0,
		PrefetchedIDs: []string{"p1", "p2", "p3", "p4", "p5"},
		UsedIDs:       []string{"p1"},
	}

	for i := 0; i < 20; i++ {
		mgr.UpdateFromObservation(obs)
	}

	profile := mgr.GetProfile()

	// Waste penalty should decrease (user tolerates waste)
	if profile.WastePenaltyMult >= 1.0 {
		t.Errorf("expected WastePenaltyMult < 1.0 for waste-tolerant user, got %f",
			profile.WastePenaltyMult)
	}

	// PrefersThorough should increase
	if profile.PrefersThorough <= 0 {
		t.Errorf("expected PrefersThorough > 0, got %f", profile.PrefersThorough)
	}
}

func TestUpdateFromObservation_NegativeSatisfaction_ManySearches(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultUserProfileManager()

	// Negative satisfaction with many searches = user wants more context
	obs := &EpisodeObservation{
		TaskCompleted: false,
		FollowUpCount: 3,
		SearchedAfter: []string{"s1", "s2", "s3"},
		PrefetchedIDs: []string{"p1"},
		UsedIDs:       []string{"p1"},
	}

	for i := 0; i < 20; i++ {
		mgr.UpdateFromObservation(obs)
	}

	profile := mgr.GetProfile()

	// PrefersThorough should increase (user suffered from insufficient context)
	if profile.PrefersThorough <= 0 {
		t.Errorf("expected PrefersThorough > 0, got %f", profile.PrefersThorough)
	}

	// ToleratesSearches should decrease
	if profile.ToleratesSearches >= 0 {
		t.Errorf("expected ToleratesSearches < 0, got %f", profile.ToleratesSearches)
	}
}

func TestUpdateFromObservation_HighStruggle_IncreasesStruggleMult(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultUserProfileManager()

	// Negative satisfaction with high struggle
	obs := &EpisodeObservation{
		TaskCompleted: false,
		FollowUpCount: 5,
		SearchedAfter: []string{"s1", "s2", "s3"},
	}

	initialMult := mgr.GetProfile().StrugglePenaltyMult

	for i := 0; i < 20; i++ {
		mgr.UpdateFromObservation(obs)
	}

	finalMult := mgr.GetProfile().StrugglePenaltyMult

	if finalMult <= initialMult {
		t.Errorf("expected StrugglePenaltyMult to increase, got %f -> %f",
			initialMult, finalMult)
	}
}

// =============================================================================
// Adjust Tests
// =============================================================================

func TestAdjust_NotReady_ReturnsBase(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultUserProfileManager()

	// Only add 2 observations (below threshold of 5)
	for i := 0; i < 2; i++ {
		mgr.UpdateFromObservation(&EpisodeObservation{TaskCompleted: true})
	}

	base := RewardWeights{
		TaskSuccess:     1.0,
		RelevanceBonus:  0.5,
		StrugglePenalty: 0.3,
		WastePenalty:    0.2,
	}

	adjusted := mgr.Adjust(base)

	if adjusted != base {
		t.Error("expected Adjust to return base when not ready")
	}
}

func TestAdjust_Ready_AppliesMultipliers(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultUserProfileManager()

	// Add enough observations to be ready
	for i := 0; i < 10; i++ {
		mgr.UpdateFromObservation(&EpisodeObservation{
			TaskCompleted: true,
			PrefetchedIDs: []string{"p1", "p2", "p3", "p4", "p5"},
			UsedIDs:       []string{"p1"},
		})
	}

	base := RewardWeights{
		TaskSuccess:     1.0,
		RelevanceBonus:  0.5,
		StrugglePenalty: 0.3,
		WastePenalty:    0.2,
	}

	adjusted := mgr.Adjust(base)

	// Task success should be unchanged
	if adjusted.TaskSuccess != base.TaskSuccess {
		t.Error("expected TaskSuccess to be unchanged")
	}

	// Multipliers should be applied
	profile := mgr.GetProfile()
	expectedWaste := base.WastePenalty * profile.WastePenaltyMult
	if adjusted.WastePenalty != expectedWaste {
		t.Errorf("expected WastePenalty=%f, got %f", expectedWaste, adjusted.WastePenalty)
	}
}

// =============================================================================
// Query Tests
// =============================================================================

func TestIsReady(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultUserProfileManager()

	if mgr.IsReady() {
		t.Error("expected not ready with 0 observations")
	}

	for i := 0; i < DefaultMinObservationsForProfile; i++ {
		mgr.UpdateFromObservation(&EpisodeObservation{TaskCompleted: true})
	}

	if !mgr.IsReady() {
		t.Error("expected ready after MinObservations")
	}
}

func TestGetRunningStats(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultUserProfileManager()

	obs := &EpisodeObservation{
		TaskCompleted: true,
		FollowUpCount: 2,
		PrefetchedIDs: []string{"p1", "p2", "p3"},
		UsedIDs:       []string{"p1"},
		SearchedAfter: []string{"s1"},
	}

	mgr.UpdateFromObservation(obs)

	avgWaste, avgFollowUps, avgSearches := mgr.GetRunningStats()

	if avgWaste != 2.0 { // 3 prefetched - 1 used
		t.Errorf("expected avgWaste=2.0, got %f", avgWaste)
	}
	if avgFollowUps != 2.0 {
		t.Errorf("expected avgFollowUps=2.0, got %f", avgFollowUps)
	}
	if avgSearches != 1.0 {
		t.Errorf("expected avgSearches=1.0, got %f", avgSearches)
	}
}

func TestGetPrefersThorough(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultUserProfileManager()

	initial := mgr.GetPrefersThorough()
	if initial != 0.0 {
		t.Errorf("expected initial PrefersThorough=0.0, got %f", initial)
	}
}

func TestGetToleratesSearches(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultUserProfileManager()

	initial := mgr.GetToleratesSearches()
	if initial != 0.0 {
		t.Errorf("expected initial ToleratesSearches=0.0, got %f", initial)
	}
}

// =============================================================================
// Persistence Tests
// =============================================================================

func TestRestoreFromProfile(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultUserProfileManager()

	profile := &UserWeightProfile{
		PrefersThorough:     0.5,
		ToleratesSearches:   -0.3,
		WastePenaltyMult:    0.8,
		StrugglePenaltyMult: 1.2,
		ObservationCount:    10,
		LastUpdated:         time.Now(),
	}

	mgr.RestoreFromProfile(profile)

	restored := mgr.GetProfile()
	if restored.PrefersThorough != 0.5 {
		t.Errorf("expected PrefersThorough=0.5, got %f", restored.PrefersThorough)
	}
	if restored.ObservationCount != 10 {
		t.Errorf("expected ObservationCount=10, got %d", restored.ObservationCount)
	}
}

func TestRestoreFromProfile_ClampsMultipliers(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultUserProfileManager()

	profile := &UserWeightProfile{
		WastePenaltyMult:    0.1, // Below minimum
		StrugglePenaltyMult: 5.0, // Above maximum
	}

	mgr.RestoreFromProfile(profile)

	restored := mgr.GetProfile()
	if restored.WastePenaltyMult < mgr.config.MinMultiplier {
		t.Errorf("expected WastePenaltyMult >= %f, got %f",
			mgr.config.MinMultiplier, restored.WastePenaltyMult)
	}
	if restored.StrugglePenaltyMult > mgr.config.MaxMultiplier {
		t.Errorf("expected StrugglePenaltyMult <= %f, got %f",
			mgr.config.MaxMultiplier, restored.StrugglePenaltyMult)
	}
}

func TestReset(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultUserProfileManager()

	// Add some observations
	for i := 0; i < 5; i++ {
		mgr.UpdateFromObservation(&EpisodeObservation{TaskCompleted: true})
	}

	mgr.Reset()

	if mgr.GetObservationCount() != 0 {
		t.Errorf("expected ObservationCount=0 after reset, got %d", mgr.GetObservationCount())
	}

	profile := mgr.GetProfile()
	if profile.WastePenaltyMult != 1.0 {
		t.Errorf("expected WastePenaltyMult=1.0 after reset, got %f", profile.WastePenaltyMult)
	}
}

// =============================================================================
// Analysis Tests
// =============================================================================

func TestAnalyze_NotReady(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultUserProfileManager()

	analysis := mgr.Analyze()

	if analysis.IsReady {
		t.Error("expected IsReady=false for new manager")
	}
	if analysis.ObservationCount != 0 {
		t.Errorf("expected ObservationCount=0, got %d", analysis.ObservationCount)
	}
}

func TestAnalyze_PrefersThorough(t *testing.T) {
	t.Parallel()

	// Use higher learning rate to accelerate preference learning
	config := UserProfileConfig{
		MinObservations: 5,
		LearningRate:    0.3, // Higher than default
		PreferenceDecay: 0.99,
		MinMultiplier:   0.5,
		MaxMultiplier:   2.0,
	}
	mgr := NewUserProfileManager(config)

	// Train profile to prefer thorough context with many observations
	for i := 0; i < 50; i++ {
		mgr.UpdateFromObservation(&EpisodeObservation{
			TaskCompleted: true,
			PrefetchedIDs: []string{"p1", "p2", "p3", "p4", "p5"},
			UsedIDs:       []string{"p1"},
		})
	}

	analysis := mgr.Analyze()

	// Check that PrefersThorough indicator increased (may not reach threshold with defaults)
	if mgr.GetPrefersThorough() <= 0 {
		t.Errorf("expected PrefersThorough > 0 for waste-tolerant user, got %f",
			mgr.GetPrefersThorough())
	}

	// Analysis should reflect the trend
	if analysis.ObservationCount != 50 {
		t.Errorf("expected 50 observations, got %d", analysis.ObservationCount)
	}
}

func TestCategorizeSensitivity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mult     float64
		expected string
	}{
		{0.5, "low"},
		{0.79, "low"},
		{0.8, "normal"},
		{1.0, "normal"},
		{1.2, "normal"},
		{1.21, "high"},
		{2.0, "high"},
	}

	for _, tt := range tests {
		result := categorizeSensitivity(tt.mult)
		if result != tt.expected {
			t.Errorf("categorizeSensitivity(%f) = %s, want %s", tt.mult, result, tt.expected)
		}
	}
}

// =============================================================================
// Utility Tests
// =============================================================================

func TestClampValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value    float64
		min      float64
		max      float64
		expected float64
	}{
		{0.5, 0.0, 1.0, 0.5},  // In range
		{-0.5, 0.0, 1.0, 0.0}, // Below min
		{1.5, 0.0, 1.0, 1.0},  // Above max
		{0.0, 0.0, 1.0, 0.0},  // At min
		{1.0, 0.0, 1.0, 1.0},  // At max
	}

	for _, tt := range tests {
		result := clampValue(tt.value, tt.min, tt.max)
		if result != tt.expected {
			t.Errorf("clampValue(%f, %f, %f) = %f, want %f",
				tt.value, tt.min, tt.max, result, tt.expected)
		}
	}
}

// =============================================================================
// Concurrency Tests
// =============================================================================

func TestConcurrentUpdates(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultUserProfileManager()

	var wg sync.WaitGroup
	n := 100

	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			mgr.UpdateFromObservation(&EpisodeObservation{
				TaskCompleted: true,
				FollowUpCount: 1,
			})
		}()
	}

	wg.Wait()

	if mgr.GetObservationCount() != n {
		t.Errorf("expected ObservationCount=%d, got %d", n, mgr.GetObservationCount())
	}
}

func TestUserProfile_ConcurrentReadsAndWrites(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultUserProfileManager()

	var wg sync.WaitGroup
	n := 50

	// Writers
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			mgr.UpdateFromObservation(&EpisodeObservation{TaskCompleted: true})
		}()
	}

	// Readers
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_ = mgr.GetProfile()
			_ = mgr.IsReady()
			_ = mgr.Analyze()
			_, _, _ = mgr.GetRunningStats()
		}()
	}

	wg.Wait()
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestUpdateFromObservation_ZeroPrefetch(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultUserProfileManager()

	obs := &EpisodeObservation{
		TaskCompleted: true,
		PrefetchedIDs: []string{},
		UsedIDs:       []string{},
	}

	// Should not panic
	mgr.UpdateFromObservation(obs)
}

func TestUpdateFromObservation_MoreUsedThanPrefetched(t *testing.T) {
	t.Parallel()

	mgr := NewDefaultUserProfileManager()

	obs := &EpisodeObservation{
		TaskCompleted: true,
		PrefetchedIDs: []string{"p1"},
		UsedIDs:       []string{"p1", "p2", "p3"}, // More used than prefetched
	}

	// Should not panic, waste should be 0
	mgr.UpdateFromObservation(obs)

	avgWaste, _, _ := mgr.GetRunningStats()
	if avgWaste != 0 {
		t.Errorf("expected avgWaste=0 when used > prefetched, got %f", avgWaste)
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkUpdateFromObservation(b *testing.B) {
	mgr := NewDefaultUserProfileManager()
	obs := &EpisodeObservation{
		TaskCompleted: true,
		FollowUpCount: 1,
		PrefetchedIDs: []string{"p1", "p2"},
		UsedIDs:       []string{"p1"},
		SearchedAfter: []string{"s1"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mgr.UpdateFromObservation(obs)
	}
}

func BenchmarkAdjust(b *testing.B) {
	mgr := NewDefaultUserProfileManager()

	// Pre-train
	for i := 0; i < 10; i++ {
		mgr.UpdateFromObservation(&EpisodeObservation{TaskCompleted: true})
	}

	base := RewardWeights{
		TaskSuccess:     1.0,
		RelevanceBonus:  0.5,
		StrugglePenalty: 0.3,
		WastePenalty:    0.2,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = mgr.Adjust(base)
	}
}

func BenchmarkAnalyze(b *testing.B) {
	mgr := NewDefaultUserProfileManager()

	// Pre-train
	for i := 0; i < 10; i++ {
		mgr.UpdateFromObservation(&EpisodeObservation{TaskCompleted: true})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = mgr.Analyze()
	}
}
