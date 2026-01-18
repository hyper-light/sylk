package health

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewActiveProber(t *testing.T) {
	probeCount := atomic.Int32{}
	probe := func(ctx context.Context) error {
		probeCount.Add(1)
		return nil
	}

	ap := NewActiveProber("test-provider", 100*time.Millisecond, 50*time.Millisecond, probe)
	defer ap.Stop()

	// Verify initial state - should assume healthy
	if ap.lastSuccess.Load() == 0 {
		t.Error("lastSuccess should be set at start")
	}

	// Wait for at least one probe to run
	time.Sleep(150 * time.Millisecond)

	if probeCount.Load() < 1 {
		t.Error("expected at least one probe to have run")
	}
}

func TestActiveProber_ProbeExecution(t *testing.T) {
	t.Run("successful probe updates lastSuccess", func(t *testing.T) {
		probe := func(ctx context.Context) error {
			return nil
		}

		ap := NewActiveProber("test", 50*time.Millisecond, 25*time.Millisecond, probe)
		defer ap.Stop()

		// Wait for a probe
		time.Sleep(75 * time.Millisecond)

		if ap.consecutiveSuccess.Load() < 1 {
			t.Error("expected consecutiveSuccess >= 1")
		}
		if ap.consecutiveFail.Load() != 0 {
			t.Error("expected consecutiveFail == 0")
		}
	})

	t.Run("failed probe updates lastFailure", func(t *testing.T) {
		probe := func(ctx context.Context) error {
			return errors.New("probe failed")
		}

		ap := NewActiveProber("test", 50*time.Millisecond, 25*time.Millisecond, probe)
		defer ap.Stop()

		// Wait for a probe
		time.Sleep(75 * time.Millisecond)

		if ap.consecutiveFail.Load() < 1 {
			t.Error("expected consecutiveFail >= 1")
		}
		if ap.consecutiveSuccess.Load() != 0 {
			t.Error("expected consecutiveSuccess == 0 after failure")
		}
	})

	t.Run("probe respects timeout", func(t *testing.T) {
		ctxCancelled := atomic.Bool{}
		probe := func(ctx context.Context) error {
			<-ctx.Done()
			ctxCancelled.Store(true)
			return ctx.Err()
		}

		ap := NewActiveProber("test", 50*time.Millisecond, 10*time.Millisecond, probe)
		defer ap.Stop()

		// Wait for probe to run and timeout
		time.Sleep(75 * time.Millisecond)

		if !ctxCancelled.Load() {
			t.Error("expected context to be cancelled due to timeout")
		}
	})
}

func TestActiveProber_ScoreFromSuccess(t *testing.T) {
	probe := func(ctx context.Context) error { return nil }

	t.Run("recent success returns 1.0", func(t *testing.T) {
		ap := NewActiveProber("test", 100*time.Millisecond, 50*time.Millisecond, probe)
		defer ap.Stop()

		// Just created, lastSuccess is now
		score := ap.Score()
		if score != 1.0 {
			t.Errorf("expected score 1.0, got %f", score)
		}
	})

	t.Run("score decays after 2*interval", func(t *testing.T) {
		ap := &ActiveProber{
			interval: 50 * time.Millisecond,
			stopCh:   make(chan struct{}),
		}

		// Set lastSuccess to 150ms ago (> 2*interval)
		ap.lastSuccess.Store(time.Now().Add(-150 * time.Millisecond).UnixNano())

		score := ap.Score()
		if score >= 1.0 {
			t.Errorf("expected score < 1.0 after decay, got %f", score)
		}
		if score < 0.5 {
			t.Errorf("expected score >= 0.5 (minimum), got %f", score)
		}
	})

	t.Run("score never goes below 0.5 from success decay", func(t *testing.T) {
		ap := &ActiveProber{
			interval: 10 * time.Millisecond,
			stopCh:   make(chan struct{}),
		}

		// Set lastSuccess to very long ago
		ap.lastSuccess.Store(time.Now().Add(-10 * time.Second).UnixNano())

		score := ap.Score()
		if score < 0.5 {
			t.Errorf("expected score >= 0.5, got %f", score)
		}
	})
}

func TestActiveProber_ScoreFromFailure(t *testing.T) {
	probe := func(ctx context.Context) error { return nil }

	t.Run("1 consecutive failure returns 0.4", func(t *testing.T) {
		ap := &ActiveProber{
			interval: 100 * time.Millisecond,
			stopCh:   make(chan struct{}),
		}
		ap.lastFailure.Store(time.Now().UnixNano())
		ap.consecutiveFail.Store(1)

		score := ap.Score()
		if score != 0.4 {
			t.Errorf("expected score 0.4, got %f", score)
		}
	})

	t.Run("3 consecutive failures returns 0.2", func(t *testing.T) {
		ap := &ActiveProber{
			interval: 100 * time.Millisecond,
			stopCh:   make(chan struct{}),
		}
		ap.lastFailure.Store(time.Now().UnixNano())
		ap.consecutiveFail.Store(3)

		score := ap.Score()
		if score != 0.2 {
			t.Errorf("expected score 0.2, got %f", score)
		}
	})

	t.Run("5 consecutive failures returns 0.0", func(t *testing.T) {
		ap := &ActiveProber{
			interval: 100 * time.Millisecond,
			stopCh:   make(chan struct{}),
		}
		ap.lastFailure.Store(time.Now().UnixNano())
		ap.consecutiveFail.Store(5)

		score := ap.Score()
		if score != 0.0 {
			t.Errorf("expected score 0.0, got %f", score)
		}
	})

	t.Run("more than 5 consecutive failures returns 0.0", func(t *testing.T) {
		ap := &ActiveProber{
			interval: 100 * time.Millisecond,
			stopCh:   make(chan struct{}),
			probe:    probe,
		}
		ap.lastFailure.Store(time.Now().UnixNano())
		ap.consecutiveFail.Store(10)

		score := ap.Score()
		if score != 0.0 {
			t.Errorf("expected score 0.0, got %f", score)
		}
	})
}

func TestActiveProber_Stop(t *testing.T) {
	probeCount := atomic.Int32{}
	probe := func(ctx context.Context) error {
		probeCount.Add(1)
		return nil
	}

	ap := NewActiveProber("test", 10*time.Millisecond, 5*time.Millisecond, probe)

	// Let it run a bit
	time.Sleep(25 * time.Millisecond)

	countBefore := probeCount.Load()
	ap.Stop()

	// Wait a bit more
	time.Sleep(25 * time.Millisecond)

	countAfter := probeCount.Load()

	// No more probes should have run after Stop
	if countAfter > countBefore {
		t.Errorf("probes continued after Stop: before=%d, after=%d", countBefore, countAfter)
	}
}

func TestActiveProber_ConsecutiveCountReset(t *testing.T) {
	failNext := atomic.Bool{}
	failNext.Store(true)

	probe := func(ctx context.Context) error {
		if failNext.Load() {
			return errors.New("failure")
		}
		return nil
	}

	ap := NewActiveProber("test", 30*time.Millisecond, 15*time.Millisecond, probe)
	defer ap.Stop()

	// Wait for a failed probe
	time.Sleep(45 * time.Millisecond)

	if ap.consecutiveFail.Load() < 1 {
		t.Error("expected at least one consecutive failure")
	}

	// Switch to success
	failNext.Store(false)

	// Wait for a successful probe
	time.Sleep(35 * time.Millisecond)

	// After success, consecutiveFail should reset
	if ap.consecutiveFail.Load() != 0 {
		t.Errorf("expected consecutiveFail to reset to 0, got %d", ap.consecutiveFail.Load())
	}
	if ap.consecutiveSuccess.Load() < 1 {
		t.Error("expected at least one consecutive success")
	}
}

func TestActiveProber_RecordSuccess(t *testing.T) {
	ap := &ActiveProber{
		interval: 100 * time.Millisecond,
		stopCh:   make(chan struct{}),
	}

	ap.consecutiveFail.Store(5)
	ap.consecutiveSuccess.Store(0)

	before := time.Now().UnixNano()
	ap.recordSuccess()
	after := time.Now().UnixNano()

	if ap.lastSuccess.Load() < before || ap.lastSuccess.Load() > after {
		t.Error("lastSuccess not in expected range")
	}
	if ap.consecutiveSuccess.Load() != 1 {
		t.Errorf("expected consecutiveSuccess=1, got %d", ap.consecutiveSuccess.Load())
	}
	if ap.consecutiveFail.Load() != 0 {
		t.Errorf("expected consecutiveFail=0, got %d", ap.consecutiveFail.Load())
	}
}

func TestActiveProber_RecordFailure(t *testing.T) {
	ap := &ActiveProber{
		interval: 100 * time.Millisecond,
		stopCh:   make(chan struct{}),
	}

	ap.consecutiveSuccess.Store(5)
	ap.consecutiveFail.Store(0)

	before := time.Now().UnixNano()
	ap.recordFailure()
	after := time.Now().UnixNano()

	if ap.lastFailure.Load() < before || ap.lastFailure.Load() > after {
		t.Error("lastFailure not in expected range")
	}
	if ap.consecutiveFail.Load() != 1 {
		t.Errorf("expected consecutiveFail=1, got %d", ap.consecutiveFail.Load())
	}
	if ap.consecutiveSuccess.Load() != 0 {
		t.Errorf("expected consecutiveSuccess=0, got %d", ap.consecutiveSuccess.Load())
	}
}

func TestActiveProber_DoProbe(t *testing.T) {
	t.Run("success path", func(t *testing.T) {
		called := atomic.Bool{}
		probe := func(ctx context.Context) error {
			called.Store(true)
			return nil
		}

		ap := &ActiveProber{
			interval: 100 * time.Millisecond,
			timeout:  50 * time.Millisecond,
			probe:    probe,
			stopCh:   make(chan struct{}),
		}

		ap.doProbe()

		if !called.Load() {
			t.Error("probe was not called")
		}
		if ap.consecutiveSuccess.Load() != 1 {
			t.Error("expected consecutiveSuccess to be 1")
		}
	})

	t.Run("failure path", func(t *testing.T) {
		probe := func(ctx context.Context) error {
			return errors.New("fail")
		}

		ap := &ActiveProber{
			interval: 100 * time.Millisecond,
			timeout:  50 * time.Millisecond,
			probe:    probe,
			stopCh:   make(chan struct{}),
		}

		ap.doProbe()

		if ap.consecutiveFail.Load() != 1 {
			t.Error("expected consecutiveFail to be 1")
		}
	})
}
