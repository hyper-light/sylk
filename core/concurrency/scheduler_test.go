package concurrency

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewPipelineScheduler(t *testing.T) {
	s := NewPipelineScheduler(DefaultSchedulerConfig())

	if s.ActiveCount() != 0 {
		t.Errorf("got ActiveCount %d, want 0", s.ActiveCount())
	}
	if s.ReadyCount() != 0 {
		t.Errorf("got ReadyCount %d, want 0", s.ReadyCount())
	}
	if s.WaitingCount() != 0 {
		t.Errorf("got WaitingCount %d, want 0", s.WaitingCount())
	}
}

func TestPipelineScheduler_Schedule(t *testing.T) {
	s := NewPipelineScheduler(SchedulerConfig{MaxConcurrent: 2})

	p := &SchedulablePipeline{ID: "p1", Priority: PriorityNormal, SpawnTime: time.Now()}
	if err := s.Schedule(p); err != nil {
		t.Fatalf("Schedule failed: %v", err)
	}

	if s.ActiveCount() != 1 {
		t.Errorf("got ActiveCount %d, want 1", s.ActiveCount())
	}
}

func TestPipelineScheduler_MaxConcurrent(t *testing.T) {
	s := NewPipelineScheduler(SchedulerConfig{MaxConcurrent: 2})

	for i := 0; i < 5; i++ {
		p := &SchedulablePipeline{ID: pipelineID(i), Priority: PriorityNormal, SpawnTime: time.Now()}
		_ = s.Schedule(p)
	}

	if s.ActiveCount() != 2 {
		t.Errorf("got ActiveCount %d, want 2", s.ActiveCount())
	}
	if s.ReadyCount() != 3 {
		t.Errorf("got ReadyCount %d, want 3", s.ReadyCount())
	}
}

func pipelineID(i int) string {
	return "p" + string(rune('0'+i))
}

func TestPipelineScheduler_NotifyComplete(t *testing.T) {
	s := NewPipelineScheduler(SchedulerConfig{MaxConcurrent: 1})

	p1 := &SchedulablePipeline{ID: "p1", Priority: PriorityNormal, SpawnTime: time.Now()}
	p2 := &SchedulablePipeline{ID: "p2", Priority: PriorityNormal, SpawnTime: time.Now()}
	_ = s.Schedule(p1)
	_ = s.Schedule(p2)

	if s.ActiveCount() != 1 {
		t.Errorf("before complete: got ActiveCount %d, want 1", s.ActiveCount())
	}

	if err := s.NotifyComplete("p1"); err != nil {
		t.Fatalf("NotifyComplete failed: %v", err)
	}

	if s.ActiveCount() != 1 {
		t.Errorf("after complete: got ActiveCount %d, want 1", s.ActiveCount())
	}
	if s.ReadyCount() != 0 {
		t.Errorf("after complete: got ReadyCount %d, want 0", s.ReadyCount())
	}
}

func TestPipelineScheduler_Cancel(t *testing.T) {
	s := NewPipelineScheduler(SchedulerConfig{MaxConcurrent: 1})

	p1 := &SchedulablePipeline{ID: "p1", Priority: PriorityNormal, SpawnTime: time.Now()}
	p2 := &SchedulablePipeline{ID: "p2", Priority: PriorityNormal, SpawnTime: time.Now()}
	_ = s.Schedule(p1)
	_ = s.Schedule(p2)

	if err := s.Cancel("p1"); err != nil {
		t.Fatalf("Cancel active failed: %v", err)
	}

	if s.ActiveCount() != 1 {
		t.Errorf("got ActiveCount %d, want 1", s.ActiveCount())
	}
}

func TestPipelineScheduler_CancelFromReady(t *testing.T) {
	s := NewPipelineScheduler(SchedulerConfig{MaxConcurrent: 1})

	p1 := &SchedulablePipeline{ID: "p1", Priority: PriorityNormal, SpawnTime: time.Now()}
	p2 := &SchedulablePipeline{ID: "p2", Priority: PriorityNormal, SpawnTime: time.Now()}
	_ = s.Schedule(p1)
	_ = s.Schedule(p2)

	if err := s.Cancel("p2"); err != nil {
		t.Fatalf("Cancel ready failed: %v", err)
	}

	if s.ReadyCount() != 0 {
		t.Errorf("got ReadyCount %d, want 0", s.ReadyCount())
	}
}

func TestPipelineScheduler_CancelNotFound(t *testing.T) {
	s := NewPipelineScheduler(DefaultSchedulerConfig())

	if err := s.Cancel("nonexistent"); err != ErrPipelineNotFound {
		t.Errorf("got error %v, want %v", err, ErrPipelineNotFound)
	}
}

func TestPipelineScheduler_Dependencies(t *testing.T) {
	s := NewPipelineScheduler(SchedulerConfig{MaxConcurrent: 2})

	p1 := &SchedulablePipeline{ID: "p1", Priority: PriorityNormal, SpawnTime: time.Now()}
	p2 := &SchedulablePipeline{ID: "p2", Priority: PriorityNormal, SpawnTime: time.Now(), Dependencies: []string{"p1"}}

	_ = s.Schedule(p1)
	_ = s.Schedule(p2)

	if s.ActiveCount() != 1 {
		t.Errorf("got ActiveCount %d, want 1", s.ActiveCount())
	}
	if s.WaitingCount() != 1 {
		t.Errorf("got WaitingCount %d, want 1", s.WaitingCount())
	}

	_ = s.NotifyComplete("p1")

	if s.ActiveCount() != 1 {
		t.Errorf("after complete: got ActiveCount %d, want 1", s.ActiveCount())
	}
	if s.WaitingCount() != 0 {
		t.Errorf("after complete: got WaitingCount %d, want 0", s.WaitingCount())
	}
}

func TestPipelineScheduler_CircularDependency(t *testing.T) {
	s := NewPipelineScheduler(SchedulerConfig{MaxConcurrent: 2})

	p1 := &SchedulablePipeline{ID: "p1", Priority: PriorityNormal, SpawnTime: time.Now(), Dependencies: []string{"p2"}}
	p2 := &SchedulablePipeline{ID: "p2", Priority: PriorityNormal, SpawnTime: time.Now(), Dependencies: []string{"p1"}}

	_ = s.Schedule(p1)
	err := s.Schedule(p2)

	if err != ErrCircularDependency {
		t.Errorf("got error %v, want %v", err, ErrCircularDependency)
	}
}

func TestPipelineScheduler_DuplicatePipeline(t *testing.T) {
	s := NewPipelineScheduler(DefaultSchedulerConfig())

	p := &SchedulablePipeline{ID: "p1", Priority: PriorityNormal, SpawnTime: time.Now()}
	_ = s.Schedule(p)

	err := s.Schedule(p)
	if err != ErrPipelineAlreadyExists {
		t.Errorf("got error %v, want %v", err, ErrPipelineAlreadyExists)
	}
}

func TestPipelineScheduler_Close(t *testing.T) {
	s := NewPipelineScheduler(DefaultSchedulerConfig())

	p := &SchedulablePipeline{ID: "p1", Priority: PriorityNormal, SpawnTime: time.Now()}
	_ = s.Schedule(p)

	s.Close()

	if !s.IsClosed() {
		t.Error("scheduler should be closed")
	}

	err := s.Schedule(&SchedulablePipeline{ID: "p2"})
	if err != ErrSchedulerClosed {
		t.Errorf("got error %v, want %v", err, ErrSchedulerClosed)
	}
}

func TestPipelineScheduler_PriorityOrdering(t *testing.T) {
	s := NewPipelineScheduler(SchedulerConfig{MaxConcurrent: 1})

	now := time.Now()
	p1 := &SchedulablePipeline{ID: "low", Priority: PriorityLow, SpawnTime: now}
	p2 := &SchedulablePipeline{ID: "high", Priority: PriorityHigh, SpawnTime: now}
	p3 := &SchedulablePipeline{ID: "normal", Priority: PriorityNormal, SpawnTime: now}

	_ = s.Schedule(p1)
	_ = s.Schedule(p2)
	_ = s.Schedule(p3)

	_ = s.NotifyComplete("low")

	s.mu.Lock()
	activeID := getFirstActiveID(s.active)
	s.mu.Unlock()

	if activeID != "high" {
		t.Errorf("got active %s, want high", activeID)
	}
}

func getFirstActiveID(m map[string]*SchedulablePipeline) string {
	for id := range m {
		return id
	}
	return ""
}

func TestPipelineScheduler_ConcurrentAccess(t *testing.T) {
	s := NewPipelineScheduler(SchedulerConfig{MaxConcurrent: 4})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			p := &SchedulablePipeline{
				ID:        pipelineIDNum(id),
				Priority:  PipelinePriority(id % 4),
				SpawnTime: time.Now(),
			}
			s.Schedule(p)
		}(i)
	}
	wg.Wait()

	total := s.ActiveCount() + s.ReadyCount() + s.WaitingCount()
	if total != 20 {
		t.Errorf("got total %d, want 20", total)
	}
}

func pipelineIDNum(i int) string {
	return "p" + itoa(i)
}

func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return itoa(i/10) + string(rune('0'+i%10))
}

func TestNewPipelineSchedulerWithContext(t *testing.T) {
	ctx := context.Background()
	s := NewPipelineSchedulerWithContext(ctx, DefaultSchedulerConfig())

	if s.ActiveCount() != 0 {
		t.Errorf("got ActiveCount %d, want 0", s.ActiveCount())
	}
	if s.ctx == nil {
		t.Error("expected scheduler to have a context")
	}
	if s.cancel == nil {
		t.Error("expected scheduler to have a cancel function")
	}
}

func TestPipelineScheduler_ContextPropagation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := NewPipelineSchedulerWithContext(ctx, SchedulerConfig{MaxConcurrent: 2})

	pipelineStarted := make(chan struct{})
	pipelineCancelled := atomic.Bool{}

	runner := NewPipelineRunner(PipelineRunnerConfig{
		ID:              "test-runner",
		ShutdownTimeout: time.Second,
	})
	runner.RegisterPhase(PhaseWorker, func(ctx context.Context) error {
		close(pipelineStarted)
		<-ctx.Done()
		pipelineCancelled.Store(true)
		return ctx.Err()
	})

	p := &SchedulablePipeline{
		ID:        "p1",
		Priority:  PriorityNormal,
		SpawnTime: time.Now(),
		Runner:    runner,
	}
	if err := s.Schedule(p); err != nil {
		t.Fatalf("Schedule failed: %v", err)
	}

	// Wait for pipeline to start
	select {
	case <-pipelineStarted:
	case <-time.After(time.Second):
		t.Fatal("pipeline did not start in time")
	}

	// Cancel the parent context
	cancel()

	// Wait for cancellation to propagate
	time.Sleep(100 * time.Millisecond)

	if !pipelineCancelled.Load() {
		t.Error("expected pipeline to be cancelled when parent context is cancelled")
	}
}

func TestPipelineScheduler_CloseContextCancellation(t *testing.T) {
	ctx := context.Background()
	s := NewPipelineSchedulerWithContext(ctx, SchedulerConfig{MaxConcurrent: 2})

	pipelineStarted := make(chan struct{})
	pipelineCancelled := atomic.Bool{}

	runner := NewPipelineRunner(PipelineRunnerConfig{
		ID:              "test-runner",
		ShutdownTimeout: time.Second,
	})
	runner.RegisterPhase(PhaseWorker, func(ctx context.Context) error {
		close(pipelineStarted)
		<-ctx.Done()
		pipelineCancelled.Store(true)
		return ctx.Err()
	})

	p := &SchedulablePipeline{
		ID:        "p1",
		Priority:  PriorityNormal,
		SpawnTime: time.Now(),
		Runner:    runner,
	}
	if err := s.Schedule(p); err != nil {
		t.Fatalf("Schedule failed: %v", err)
	}

	// Wait for pipeline to start
	select {
	case <-pipelineStarted:
	case <-time.After(time.Second):
		t.Fatal("pipeline did not start in time")
	}

	// Close the scheduler
	s.Close()

	// Wait for cancellation to propagate
	time.Sleep(100 * time.Millisecond)

	if !pipelineCancelled.Load() {
		t.Error("expected pipeline to be cancelled when scheduler is closed")
	}
}

func TestPipelineScheduler_CancelIndividualPipeline(t *testing.T) {
	ctx := context.Background()
	s := NewPipelineSchedulerWithContext(ctx, SchedulerConfig{MaxConcurrent: 2})

	pipeline1Started := make(chan struct{})
	pipeline1Cancelled := atomic.Bool{}
	pipeline2Running := atomic.Bool{}

	runner1 := NewPipelineRunner(PipelineRunnerConfig{
		ID:              "runner-1",
		ShutdownTimeout: time.Second,
	})
	runner1.RegisterPhase(PhaseWorker, func(ctx context.Context) error {
		close(pipeline1Started)
		<-ctx.Done()
		pipeline1Cancelled.Store(true)
		return ctx.Err()
	})

	runner2 := NewPipelineRunner(PipelineRunnerConfig{
		ID:              "runner-2",
		ShutdownTimeout: time.Second,
	})
	runner2.RegisterPhase(PhaseWorker, func(ctx context.Context) error {
		pipeline2Running.Store(true)
		time.Sleep(200 * time.Millisecond)
		return nil
	})

	p1 := &SchedulablePipeline{
		ID:        "p1",
		Priority:  PriorityNormal,
		SpawnTime: time.Now(),
		Runner:    runner1,
	}
	p2 := &SchedulablePipeline{
		ID:        "p2",
		Priority:  PriorityNormal,
		SpawnTime: time.Now(),
		Runner:    runner2,
	}

	_ = s.Schedule(p1)
	_ = s.Schedule(p2)

	// Wait for pipeline 1 to start
	select {
	case <-pipeline1Started:
	case <-time.After(time.Second):
		t.Fatal("pipeline 1 did not start in time")
	}

	// Cancel only pipeline 1
	if err := s.Cancel("p1"); err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}

	// Wait for cancellation to propagate
	time.Sleep(100 * time.Millisecond)

	if !pipeline1Cancelled.Load() {
		t.Error("expected pipeline 1 to be cancelled")
	}

	// Pipeline 2 should still be running or have completed normally
	// (it was not cancelled)
	s.Close()
}
