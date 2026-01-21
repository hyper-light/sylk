package health

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// ActiveProber performs periodic health probes.
type ActiveProber struct {
	provider string
	interval time.Duration
	timeout  time.Duration
	probe    ProbeFunc

	lastSuccess        atomic.Int64 // Unix nano
	lastFailure        atomic.Int64 // Unix nano
	consecutiveFail    atomic.Int32
	consecutiveSuccess atomic.Int32

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewActiveProber creates a prober that runs in background.
// Assumes healthy at start (sets lastSuccess to now).
func NewActiveProber(provider string, interval, timeout time.Duration, probe ProbeFunc) *ActiveProber {
	ap := &ActiveProber{
		provider: provider,
		interval: interval,
		timeout:  timeout,
		probe:    probe,
		stopCh:   make(chan struct{}),
	}

	// Assume healthy at start
	ap.lastSuccess.Store(time.Now().UnixNano())

	ap.wg.Add(1)
	go ap.run()

	return ap
}

// run is the background goroutine that runs probes.
func (ap *ActiveProber) run() {
	defer ap.wg.Done()

	ticker := time.NewTicker(ap.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ap.stopCh:
			return
		case <-ticker.C:
			ap.doProbe()
		}
	}
}

// doProbe executes a single probe with timeout.
func (ap *ActiveProber) doProbe() {
	ctx, cancel := context.WithTimeout(context.Background(), ap.timeout)
	defer cancel()

	err := ap.probe(ctx)

	if err == nil {
		ap.recordSuccess()
	} else {
		ap.recordFailure()
	}
}

// recordSuccess records a successful probe.
func (ap *ActiveProber) recordSuccess() {
	ap.lastSuccess.Store(time.Now().UnixNano())
	ap.consecutiveSuccess.Add(1)
	ap.consecutiveFail.Store(0)
}

// recordFailure records a failed probe.
func (ap *ActiveProber) recordFailure() {
	ap.lastFailure.Store(time.Now().UnixNano())
	ap.consecutiveFail.Add(1)
	ap.consecutiveSuccess.Store(0)
}

// Score returns active probe health score (0-1).
func (ap *ActiveProber) Score() float64 {
	lastSuccess := ap.lastSuccess.Load()
	lastFailure := ap.lastFailure.Load()

	if lastSuccess > lastFailure {
		return ap.scoreFromSuccess(lastSuccess)
	}
	return ap.scoreFromFailure()
}

// scoreFromSuccess calculates score when last probe succeeded.
// - < 2*interval: 1.0
// - else: max(0.5, 1.0 - decay)
func (ap *ActiveProber) scoreFromSuccess(lastSuccess int64) float64 {
	elapsed := time.Since(time.Unix(0, lastSuccess))
	threshold := 2 * ap.interval

	if elapsed < threshold {
		return 1.0
	}

	decay := float64(elapsed-threshold) / float64(ap.interval) * 0.1
	return max(0.5, 1.0-decay)
}

// scoreFromFailure calculates score based on consecutive failures.
// >=5: 0.0, >=3: 0.2, >=1: 0.4
func (ap *ActiveProber) scoreFromFailure() float64 {
	fails := ap.consecutiveFail.Load()

	if fails >= 5 {
		return 0.0
	}
	if fails >= 3 {
		return 0.2
	}
	if fails >= 1 {
		return 0.4
	}
	return 1.0
}

// Stop stops the prober goroutine gracefully.
func (ap *ActiveProber) Stop() {
	ap.stopOnce.Do(func() {
		close(ap.stopCh)
	})
	ap.wg.Wait()
}
