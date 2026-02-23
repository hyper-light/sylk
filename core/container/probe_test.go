package container

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
)

type succeedProbe struct{}

func (s *succeedProbe) Execute(_ context.Context) (ProbeResult, error) {
	return ProbeResult{Success: true, Message: "ok"}, nil
}

type failProbe struct{}

func (f *failProbe) Execute(_ context.Context) (ProbeResult, error) {
	return ProbeResult{Success: false, Message: "fail"}, nil
}

type errorProbe struct{}

func (e *errorProbe) Execute(_ context.Context) (ProbeResult, error) {
	return ProbeResult{}, errors.New("probe error")
}

type countingProbe struct {
	calls atomic.Int32
}

func (c *countingProbe) Execute(_ context.Context) (ProbeResult, error) {
	c.calls.Add(1)
	return ProbeResult{Success: true}, nil
}

func TestProbeRunner_IsHealthy_Unconfigured(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scope := concurrency.NewGoroutineScope(ctx, "test", nil)

	pr := NewProbeRunner(scope, nil)
	if !pr.IsHealthy(ProbeLiveness) {
		t.Fatal("unconfigured probe should be healthy")
	}
	if !pr.IsReady() {
		t.Fatal("unconfigured probe should be ready")
	}
}

func TestProbeRunner_SuccessThreshold(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scope := concurrency.NewGoroutineScope(ctx, "test", nil)

	pr := NewProbeRunner(scope, nil)
	specs := []ProbeSpec{{
		Type:             ProbeLiveness,
		Handler:          &succeedProbe{},
		Period:           20 * time.Millisecond,
		Timeout:          50 * time.Millisecond,
		SuccessThreshold: 2,
		FailureThreshold: 3,
	}}

	if err := pr.Start(specs); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Wait for at least 2 probe cycles
	time.Sleep(100 * time.Millisecond)

	if !pr.IsAlive() {
		t.Fatal("should be alive after success threshold met")
	}
	last := pr.LastResult(ProbeLiveness)
	if last == nil {
		t.Fatal("expected last result")
	}
	if !last.Success {
		t.Fatal("expected last result to be success")
	}
}

func TestProbeRunner_FailureThreshold(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scope := concurrency.NewGoroutineScope(ctx, "test", nil)

	pr := NewProbeRunner(scope, nil)
	specs := []ProbeSpec{{
		Type:             ProbeLiveness,
		Handler:          &failProbe{},
		Period:           20 * time.Millisecond,
		Timeout:          50 * time.Millisecond,
		SuccessThreshold: 1,
		FailureThreshold: 2,
	}}

	if err := pr.Start(specs); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if pr.IsAlive() {
		t.Fatal("should not be alive after failure threshold met")
	}
	if pr.ConsecutiveFailures(ProbeLiveness) < 2 {
		t.Fatalf("expected >= 2 failures, got %d", pr.ConsecutiveFailures(ProbeLiveness))
	}
}

func TestProbeRunner_ErrorTreatedAsFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scope := concurrency.NewGoroutineScope(ctx, "test", nil)

	pr := NewProbeRunner(scope, nil)
	specs := []ProbeSpec{{
		Type:             ProbeLiveness,
		Handler:          &errorProbe{},
		Period:           20 * time.Millisecond,
		Timeout:          50 * time.Millisecond,
		SuccessThreshold: 1,
		FailureThreshold: 1,
	}}

	if err := pr.Start(specs); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	time.Sleep(80 * time.Millisecond)

	if pr.IsAlive() {
		t.Fatal("error probe should cause failure")
	}
}

func TestProbeRunner_InitialDelay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scope := concurrency.NewGoroutineScope(ctx, "test", nil)

	counter := &countingProbe{}
	pr := NewProbeRunner(scope, nil)
	specs := []ProbeSpec{{
		Type:             ProbeLiveness,
		Handler:          counter,
		InitialDelay:     100 * time.Millisecond,
		Period:           20 * time.Millisecond,
		Timeout:          50 * time.Millisecond,
		SuccessThreshold: 1,
		FailureThreshold: 3,
	}}

	if err := pr.Start(specs); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Before initial delay, no calls should have been made
	time.Sleep(50 * time.Millisecond)
	if counter.calls.Load() > 0 {
		t.Fatal("probe should not have fired before initial delay")
	}

	// After initial delay + some probe periods
	time.Sleep(200 * time.Millisecond)
	if counter.calls.Load() == 0 {
		t.Fatal("probe should have fired after initial delay")
	}
}

func TestProbeRunner_HasStarted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scope := concurrency.NewGoroutineScope(ctx, "test", nil)

	pr := NewProbeRunner(scope, nil)
	specs := []ProbeSpec{{
		Type:             ProbeStartup,
		Handler:          &succeedProbe{},
		InitialDelay:     100 * time.Millisecond,
		Period:           20 * time.Millisecond,
		Timeout:          50 * time.Millisecond,
		SuccessThreshold: 1,
		FailureThreshold: 3,
	}}

	if err := pr.Start(specs); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if pr.HasStarted(ProbeStartup) {
		t.Fatal("should not have started yet (initial delay)")
	}

	time.Sleep(150 * time.Millisecond)

	if !pr.HasStarted(ProbeStartup) {
		t.Fatal("should have started after initial delay")
	}
}
