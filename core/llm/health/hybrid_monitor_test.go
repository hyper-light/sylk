package health

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestNewHybridHealthMonitor(t *testing.T) {
	config := DefaultHealthConfig()
	config.ProbeInterval = 100 * time.Millisecond
	config.ProbeTimeout = 50 * time.Millisecond

	probeFunc := func(ctx context.Context) error { return nil }
	hm := NewHybridHealthMonitor("test-provider", config, probeFunc)
	defer hm.Stop()

	// Should start at healthy (1.0)
	score := hm.Score()
	if score != 1.0 {
		t.Errorf("expected initial score 1.0, got %f", score)
	}
}

func TestHybridHealthMonitor_ApplyDefaultWeights(t *testing.T) {
	config := HealthConfig{
		ProbeInterval: 100 * time.Millisecond,
		ProbeTimeout:  50 * time.Millisecond,
		WindowSize:    time.Minute,
		MinSamples:    5,
		// Weights left at zero
	}

	probeFunc := func(ctx context.Context) error { return nil }
	hm := NewHybridHealthMonitor("test-provider", config, probeFunc)
	defer hm.Stop()

	// Defaults should be applied
	if hm.config.ActiveWeight != defaultActiveWeight {
		t.Errorf("expected active weight %f, got %f", defaultActiveWeight, hm.config.ActiveWeight)
	}
	if hm.config.PassiveWeight != defaultPassiveWeight {
		t.Errorf("expected passive weight %f, got %f", defaultPassiveWeight, hm.config.PassiveWeight)
	}
}

func TestHybridHealthMonitor_CombinedScoring(t *testing.T) {
	config := DefaultHealthConfig()
	config.ProbeInterval = 50 * time.Millisecond
	config.ProbeTimeout = 25 * time.Millisecond
	config.WindowSize = time.Minute
	config.MinSamples = 2

	probeFunc := func(ctx context.Context) error { return nil }
	hm := NewHybridHealthMonitor("test-provider", config, probeFunc)
	defer hm.Stop()

	// Record some successes to have passive data
	hm.RecordSuccess(10 * time.Millisecond)
	hm.RecordSuccess(15 * time.Millisecond)

	// Wait for score update
	time.Sleep(6 * time.Second)

	score := hm.Score()
	// Active should be 1.0 (probes succeeding), passive should be 1.0 (successes)
	// Combined = 0.4*1.0 + 0.6*1.0 = 1.0
	if score < 0.9 {
		t.Errorf("expected high score with all successes, got %f", score)
	}
}

func TestHybridHealthMonitor_FailingActiveProbe(t *testing.T) {
	config := DefaultHealthConfig()
	config.ProbeInterval = 50 * time.Millisecond
	config.ProbeTimeout = 25 * time.Millisecond
	config.WindowSize = time.Minute
	config.MinSamples = 2

	// Probe always fails
	probeFunc := func(ctx context.Context) error {
		return errors.New("probe failed")
	}
	hm := NewHybridHealthMonitor("test-provider", config, probeFunc)
	defer hm.Stop()

	// Record successes so passive stays high
	hm.RecordSuccess(10 * time.Millisecond)
	hm.RecordSuccess(15 * time.Millisecond)

	// Wait for multiple probe failures and score update
	time.Sleep(6500 * time.Millisecond)

	score := hm.Score()
	// Active should be low due to failures, passive should be 1.0
	// Combined should be lower than 1.0
	if score >= 1.0 {
		t.Errorf("expected lower score with failing probes, got %f", score)
	}
}

func TestHybridHealthMonitor_CheckThresholds(t *testing.T) {
	tests := []struct {
		name           string
		score          float64
		expectedStatus HealthStatus
		expectedProc   bool
	}{
		{"healthy", 0.8, HealthHealthy, true},
		{"monitored", 0.6, HealthMonitored, true},
		{"degraded", 0.4, HealthDegraded, true},
		{"dead", 0.2, HealthDead, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hm := createMonitorWithScore(tt.score)
			defer hm.Stop()

			decision := hm.Check()

			if decision.Status != tt.expectedStatus {
				t.Errorf("expected status %v, got %v", tt.expectedStatus, decision.Status)
			}
			if decision.Proceed != tt.expectedProc {
				t.Errorf("expected proceed %v, got %v", tt.expectedProc, decision.Proceed)
			}
			if decision.Score != tt.score {
				t.Errorf("expected score %f, got %f", tt.score, decision.Score)
			}
		})
	}
}

func createMonitorWithScore(score float64) *HybridHealthMonitor {
	config := DefaultHealthConfig()
	config.ProbeInterval = time.Hour // Prevent probes during test
	config.ProbeTimeout = time.Second

	probeFunc := func(ctx context.Context) error { return nil }
	hm := NewHybridHealthMonitor("test-provider", config, probeFunc)

	// Manually set score
	hm.health.Store(int64(score * scoreMultiplier))

	return hm
}

func TestHybridHealthMonitor_RecordSuccess(t *testing.T) {
	config := DefaultHealthConfig()
	config.ProbeInterval = time.Hour
	config.ProbeTimeout = time.Second
	config.MinSamples = 2

	probeFunc := func(ctx context.Context) error { return nil }
	hm := NewHybridHealthMonitor("test-provider", config, probeFunc)
	defer hm.Stop()

	// Record successes
	hm.RecordSuccess(10 * time.Millisecond)
	hm.RecordSuccess(20 * time.Millisecond)

	// Manually trigger score update
	hm.updateCombinedScore()

	score := hm.Score()
	if score < 0.9 {
		t.Errorf("expected high score after successes, got %f", score)
	}
}

func TestHybridHealthMonitor_RecordFailure(t *testing.T) {
	config := DefaultHealthConfig()
	config.ProbeInterval = time.Hour
	config.ProbeTimeout = time.Second
	config.MinSamples = 2

	probeFunc := func(ctx context.Context) error { return nil }
	hm := NewHybridHealthMonitor("test-provider", config, probeFunc)
	defer hm.Stop()

	// Record failures
	hm.RecordFailure(errors.New("request failed"))
	hm.RecordFailure(errors.New("request failed again"))

	// Manually trigger score update
	hm.updateCombinedScore()

	score := hm.Score()
	// Passive score should be 0 (all failures), active 1.0
	// Combined = 0.4*1.0 + 0.6*0 = 0.4
	if score > 0.5 {
		t.Errorf("expected lower score after failures, got %f", score)
	}
}

func TestHybridHealthMonitor_Stop(t *testing.T) {
	config := DefaultHealthConfig()
	config.ProbeInterval = 50 * time.Millisecond
	config.ProbeTimeout = 25 * time.Millisecond

	probeFunc := func(ctx context.Context) error { return nil }
	hm := NewHybridHealthMonitor("test-provider", config, probeFunc)

	// Stop should complete without hanging
	done := make(chan struct{})
	go func() {
		hm.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Error("Stop() did not complete in time")
	}
}

func TestHybridHealthMonitor_ConcurrentAccess(t *testing.T) {
	config := DefaultHealthConfig()
	config.ProbeInterval = 50 * time.Millisecond
	config.ProbeTimeout = 25 * time.Millisecond
	config.MinSamples = 5

	probeFunc := func(ctx context.Context) error { return nil }
	hm := NewHybridHealthMonitor("test-provider", config, probeFunc)
	defer hm.Stop()

	var wg sync.WaitGroup
	const goroutines = 10
	const iterations = 100

	// Concurrent reads
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = hm.Score()
				_ = hm.Check()
			}
		}()
	}

	// Concurrent writes
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				hm.RecordSuccess(time.Millisecond)
				hm.RecordFailure(errors.New("test"))
			}
		}()
	}

	wg.Wait()
}

func TestHybridHealthMonitor_ScoreUpdaterInterval(t *testing.T) {
	config := DefaultHealthConfig()
	config.ProbeInterval = time.Hour // Disable active probing during test
	config.ProbeTimeout = time.Second
	config.MinSamples = 2

	probeFunc := func(ctx context.Context) error { return nil }
	hm := NewHybridHealthMonitor("test-provider", config, probeFunc)
	defer hm.Stop()

	// Initial score is 1.0
	initialScore := hm.Score()
	if initialScore != 1.0 {
		t.Errorf("expected initial score 1.0, got %f", initialScore)
	}

	// Record failures to change passive score
	hm.RecordFailure(errors.New("fail1"))
	hm.RecordFailure(errors.New("fail2"))

	// Score shouldn't change immediately (no manual update)
	immediateScore := hm.Score()
	if immediateScore != 1.0 {
		t.Errorf("expected score to still be 1.0 before update, got %f", immediateScore)
	}

	// Wait for automatic update (5+ seconds)
	time.Sleep(6 * time.Second)

	updatedScore := hm.Score()
	if updatedScore >= 1.0 {
		t.Errorf("expected score to decrease after update, got %f", updatedScore)
	}
}

func TestHybridHealthMonitor_DetermineStatus(t *testing.T) {
	hm := createMonitorWithScore(1.0)
	defer hm.Stop()

	tests := []struct {
		score    float64
		expected HealthStatus
	}{
		{0.29, HealthDead},       // Below reject (0.3)
		{0.30, HealthDegraded},   // At reject threshold
		{0.49, HealthDegraded},   // Below warn (0.5)
		{0.50, HealthMonitored},  // At warn threshold
		{0.69, HealthMonitored},  // Below monitor (0.7)
		{0.70, HealthHealthy},    // At monitor threshold
		{1.0, HealthHealthy},     // Perfect score
	}

	for _, tt := range tests {
		status := hm.determineStatus(tt.score)
		if status != tt.expected {
			t.Errorf("score %f: expected %v, got %v", tt.score, tt.expected, status)
		}
	}
}

func TestHybridHealthMonitor_DecisionReason(t *testing.T) {
	hm := createMonitorWithScore(1.0)
	defer hm.Stop()

	decision := hm.Check()

	expectedReason := "provider test-provider is healthy"
	if decision.Reason != expectedReason {
		t.Errorf("expected reason %q, got %q", expectedReason, decision.Reason)
	}
}

func TestHybridHealthMonitor_EdgeCaseScores(t *testing.T) {
	tests := []struct {
		name  string
		score float64
	}{
		{"zero", 0.0},
		{"one", 1.0},
		{"exactly_reject", 0.3},
		{"exactly_warn", 0.5},
		{"exactly_monitor", 0.7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hm := createMonitorWithScore(tt.score)
			defer hm.Stop()

			score := hm.Score()
			if score != tt.score {
				t.Errorf("expected score %f, got %f", tt.score, score)
			}

			// Check shouldn't panic
			decision := hm.Check()
			if decision.Score != tt.score {
				t.Errorf("expected decision score %f, got %f", tt.score, decision.Score)
			}
		})
	}
}

func TestHybridHealthMonitor_CustomWeights(t *testing.T) {
	config := DefaultHealthConfig()
	config.ProbeInterval = time.Hour
	config.ProbeTimeout = time.Second
	config.ActiveWeight = 0.7
	config.PassiveWeight = 0.3
	config.MinSamples = 2

	probeFunc := func(ctx context.Context) error { return nil }
	hm := NewHybridHealthMonitor("test-provider", config, probeFunc)
	defer hm.Stop()

	// Verify custom weights were preserved
	if hm.config.ActiveWeight != 0.7 {
		t.Errorf("expected active weight 0.7, got %f", hm.config.ActiveWeight)
	}
	if hm.config.PassiveWeight != 0.3 {
		t.Errorf("expected passive weight 0.3, got %f", hm.config.PassiveWeight)
	}
}
