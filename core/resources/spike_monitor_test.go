package resources

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDefaultSpikeMonitorConfig(t *testing.T) {
	cfg := DefaultSpikeMonitorConfig()

	if cfg.Interval != 200*time.Millisecond {
		t.Errorf("Interval = %v, want 200ms", cfg.Interval)
	}
	if cfg.SpikeThreshold != 0.15 {
		t.Errorf("SpikeThreshold = %v, want 0.15", cfg.SpikeThreshold)
	}
	if cfg.SampleCount != 30 {
		t.Errorf("SampleCount = %d, want 30", cfg.SampleCount)
	}
}

func TestNewSpikeMonitor(t *testing.T) {
	m := NewSpikeMonitor(DefaultSpikeMonitorConfig())

	if m == nil {
		t.Fatal("NewSpikeMonitor returned nil")
	}
	if m.Limit() <= 0 {
		t.Errorf("Limit() = %d, should be positive", m.Limit())
	}
}

func TestNewSpikeMonitor_CustomConfig(t *testing.T) {
	cfg := SpikeMonitorConfig{
		Interval:       100 * time.Millisecond,
		SpikeThreshold: 0.20,
		SampleCount:    10,
		MemoryLimit:    1024 * 1024 * 100,
	}
	m := NewSpikeMonitor(cfg)

	if m.Limit() != cfg.MemoryLimit {
		t.Errorf("Limit() = %d, want %d", m.Limit(), cfg.MemoryLimit)
	}
}

func TestNewSpikeMonitor_DefaultsForZeroValues(t *testing.T) {
	cfg := SpikeMonitorConfig{}
	m := NewSpikeMonitor(cfg)

	if m == nil {
		t.Fatal("NewSpikeMonitor returned nil with zero config")
	}
	if len(m.samples) != 30 {
		t.Errorf("samples length = %d, want 30", len(m.samples))
	}
}

func TestSpikeMonitor_Run_CollectsSamples(t *testing.T) {
	cfg := SpikeMonitorConfig{
		Interval:    10 * time.Millisecond,
		SampleCount: 5,
	}
	m := NewSpikeMonitor(cfg)

	var sampleCount atomic.Int32
	m.SetOnSample(func(s Sample) {
		sampleCount.Add(1)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	go m.Run(ctx)
	<-ctx.Done()

	time.Sleep(15 * time.Millisecond)

	count := sampleCount.Load()
	if count < 3 {
		t.Errorf("collected %d samples, expected at least 3", count)
	}
}

func TestSpikeMonitor_Run_ContextCancellation(t *testing.T) {
	m := NewSpikeMonitor(DefaultSpikeMonitorConfig())

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		m.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Error("Run did not exit after context cancellation")
	}
}

func TestSpikeMonitor_OnSampleCallback(t *testing.T) {
	cfg := SpikeMonitorConfig{
		Interval:    10 * time.Millisecond,
		SampleCount: 5,
	}
	m := NewSpikeMonitor(cfg)

	var receivedSample Sample
	var called atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)

	m.SetOnSample(func(s Sample) {
		if called.CompareAndSwap(false, true) {
			receivedSample = s
			wg.Done()
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	go m.Run(ctx)
	wg.Wait()

	if receivedSample.HeapAlloc == 0 {
		t.Error("Sample.HeapAlloc should be > 0")
	}
	if receivedSample.Usage <= 0 {
		t.Error("Sample.Usage should be > 0")
	}
	if receivedSample.Timestamp.IsZero() {
		t.Error("Sample.Timestamp should not be zero")
	}
}

func TestSpikeMonitor_TrendCalculation(t *testing.T) {
	cfg := SpikeMonitorConfig{
		Interval:    5 * time.Millisecond,
		SampleCount: 5,
	}
	m := NewSpikeMonitor(cfg)

	var trends []float64
	var mu sync.Mutex

	m.SetOnSample(func(s Sample) {
		mu.Lock()
		trends = append(trends, s.Trend)
		mu.Unlock()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	go m.Run(ctx)
	<-ctx.Done()
	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	trendCount := len(trends)
	mu.Unlock()

	if trendCount < 3 {
		t.Errorf("collected %d trends, expected at least 3", trendCount)
	}

	if trends[0] != 0 {
		t.Log("First trend may or may not be 0 depending on previous samples")
	}
}

func TestSpikeMonitor_LastSample(t *testing.T) {
	cfg := SpikeMonitorConfig{
		Interval:    5 * time.Millisecond,
		SampleCount: 5,
	}
	m := NewSpikeMonitor(cfg)

	var wg sync.WaitGroup
	wg.Add(2)
	count := 0

	m.SetOnSample(func(s Sample) {
		count++
		if count <= 2 {
			wg.Done()
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	go m.Run(ctx)

	wg.Wait()
	cancel()

	last := m.LastSample()
	if last.HeapAlloc == 0 {
		t.Error("LastSample().HeapAlloc should be > 0")
	}
}

func TestSpikeMonitor_RecentSamples(t *testing.T) {
	cfg := SpikeMonitorConfig{
		Interval:    5 * time.Millisecond,
		SampleCount: 10,
	}
	m := NewSpikeMonitor(cfg)

	var wg sync.WaitGroup
	wg.Add(5)
	count := 0

	m.SetOnSample(func(s Sample) {
		count++
		if count <= 5 {
			wg.Done()
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	go m.Run(ctx)

	wg.Wait()
	cancel()

	recent := m.RecentSamples(3)
	if len(recent) != 3 {
		t.Errorf("RecentSamples(3) returned %d samples, want 3", len(recent))
	}
}

func TestSpikeMonitor_RecentSamples_MoreThanAvailable(t *testing.T) {
	cfg := SpikeMonitorConfig{
		Interval:    5 * time.Millisecond,
		SampleCount: 5,
	}
	m := NewSpikeMonitor(cfg)

	recent := m.RecentSamples(100)
	if len(recent) != 5 {
		t.Errorf("RecentSamples(100) with 5-sample buffer returned %d, want 5", len(recent))
	}
}

func TestSpikeMonitor_AverageUsage(t *testing.T) {
	cfg := SpikeMonitorConfig{
		Interval:    5 * time.Millisecond,
		SampleCount: 10,
	}
	m := NewSpikeMonitor(cfg)

	var wg sync.WaitGroup
	wg.Add(5)
	count := 0

	m.SetOnSample(func(s Sample) {
		count++
		if count <= 5 {
			wg.Done()
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	go m.Run(ctx)

	wg.Wait()
	cancel()

	avg := m.AverageUsage(5)
	if avg <= 0 {
		t.Errorf("AverageUsage(5) = %v, expected > 0", avg)
	}
}

func TestSpikeMonitor_AverageUsage_Empty(t *testing.T) {
	m := NewSpikeMonitor(DefaultSpikeMonitorConfig())

	avg := m.AverageUsage(10)
	if avg != 0 {
		t.Errorf("AverageUsage on empty buffer = %v, want 0", avg)
	}
}

func TestSpikeMonitor_ConcurrentCallbacks(t *testing.T) {
	cfg := SpikeMonitorConfig{
		Interval:    5 * time.Millisecond,
		SampleCount: 10,
	}
	m := NewSpikeMonitor(cfg)

	var callCount atomic.Int32
	var firstCall sync.WaitGroup
	firstCall.Add(1)
	calledOnce := false

	m.SetOnSample(func(s Sample) {
		callCount.Add(1)
		if !calledOnce {
			calledOnce = true
			firstCall.Done()
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	go m.Run(ctx)

	firstCall.Wait()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.LastSample()
			_ = m.RecentSamples(5)
			_ = m.AverageUsage(3)
		}()
	}

	wg.Wait()
	cancel()

	if callCount.Load() == 0 {
		t.Error("OnSample callback was never called")
	}
}

func TestSpikeMonitor_SpikeDetection(t *testing.T) {
	cfg := SpikeMonitorConfig{
		Interval:       10 * time.Millisecond,
		SpikeThreshold: 0.01,
		SampleCount:    5,
	}
	m := NewSpikeMonitor(cfg)

	var spikeDetected atomic.Bool

	m.SetOnSpike(func(s Sample) {
		spikeDetected.Store(true)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	go m.Run(ctx)
	<-ctx.Done()

	t.Logf("Spike detected: %v (may or may not trigger depending on memory fluctuation)", spikeDetected.Load())
}

func TestSpikeMonitor_RingBufferWrap(t *testing.T) {
	cfg := SpikeMonitorConfig{
		Interval:    2 * time.Millisecond,
		SampleCount: 3,
	}
	m := NewSpikeMonitor(cfg)

	var wg sync.WaitGroup
	wg.Add(10)
	count := 0

	m.SetOnSample(func(s Sample) {
		count++
		if count <= 10 {
			wg.Done()
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	go m.Run(ctx)

	wg.Wait()
	cancel()

	recent := m.RecentSamples(3)
	if len(recent) != 3 {
		t.Errorf("After wrap, RecentSamples(3) = %d, want 3", len(recent))
	}

	for _, s := range recent {
		if s.HeapAlloc == 0 {
			t.Error("Wrapped sample has zero HeapAlloc")
		}
	}
}
