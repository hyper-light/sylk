package session

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
)

func TestNewGlobalPipelineScheduler(t *testing.T) {
	scheduler := NewGlobalPipelineScheduler(PipelineSchedulerConfig{})

	if scheduler == nil {
		t.Fatal("expected non-nil scheduler")
	}
	if scheduler.maxConcurrent <= 0 {
		t.Error("expected positive max concurrent")
	}
	if scheduler.sessionActive == nil {
		t.Error("expected non-nil session active map")
	}
}

func TestNewGlobalPipelineScheduler_CustomMaxConcurrent(t *testing.T) {
	scheduler := NewGlobalPipelineScheduler(PipelineSchedulerConfig{
		MaxConcurrent: 8,
	})

	if scheduler.maxConcurrent != 8 {
		t.Errorf("expected 8, got %d", scheduler.maxConcurrent)
	}
}

func TestGlobalPipelineScheduler_Schedule_Immediate(t *testing.T) {
	scheduler := NewGlobalPipelineScheduler(PipelineSchedulerConfig{
		MaxConcurrent: 4,
	})
	defer scheduler.Close()

	ctx := context.Background()
	pipeline := &concurrency.SchedulablePipeline{
		ID:        "p1",
		Priority:  concurrency.PriorityNormal,
		SpawnTime: time.Now(),
	}

	scheduled, err := scheduler.Schedule(ctx, "session-1", pipeline)
	if err != nil {
		t.Fatalf("Schedule failed: %v", err)
	}
	if !scheduled {
		t.Error("expected immediate scheduling")
	}

	stats := scheduler.GetSessionStats("session-1")
	if stats.Active != 1 {
		t.Errorf("expected 1 active, got %d", stats.Active)
	}
}

func TestGlobalPipelineScheduler_Schedule_Queued(t *testing.T) {
	scheduler := NewGlobalPipelineScheduler(PipelineSchedulerConfig{
		MaxConcurrent: 1,
	})
	defer scheduler.Close()

	ctx := context.Background()

	p1 := &concurrency.SchedulablePipeline{ID: "p1", SpawnTime: time.Now()}
	p2 := &concurrency.SchedulablePipeline{ID: "p2", SpawnTime: time.Now()}

	scheduled1, _ := scheduler.Schedule(ctx, "session-1", p1)
	scheduled2, _ := scheduler.Schedule(ctx, "session-1", p2)

	if !scheduled1 {
		t.Error("expected first pipeline to be scheduled immediately")
	}
	if scheduled2 {
		t.Error("expected second pipeline to be queued")
	}

	stats := scheduler.GetSessionStats("session-1")
	if stats.Queued != 1 {
		t.Errorf("expected 1 queued, got %d", stats.Queued)
	}
}

func TestGlobalPipelineScheduler_OnPipelineComplete(t *testing.T) {
	scheduler := NewGlobalPipelineScheduler(PipelineSchedulerConfig{
		MaxConcurrent: 1,
	})
	defer scheduler.Close()

	ctx := context.Background()

	p1 := &concurrency.SchedulablePipeline{ID: "p1", SpawnTime: time.Now()}
	scheduler.Schedule(ctx, "session-1", p1)

	err := scheduler.OnPipelineComplete("session-1", "p1")
	if err != nil {
		t.Fatalf("OnPipelineComplete failed: %v", err)
	}

	stats := scheduler.GetSessionStats("session-1")
	if stats.Active != 0 {
		t.Errorf("expected 0 active after completion, got %d", stats.Active)
	}
}

func TestGlobalPipelineScheduler_DispatchAfterComplete(t *testing.T) {
	scheduler := NewGlobalPipelineScheduler(PipelineSchedulerConfig{
		MaxConcurrent: 1,
	})
	defer scheduler.Close()

	ctx := context.Background()

	p1 := &concurrency.SchedulablePipeline{ID: "p1", SpawnTime: time.Now()}
	p2 := &concurrency.SchedulablePipeline{ID: "p2", SpawnTime: time.Now()}

	scheduler.Schedule(ctx, "session-1", p1)
	scheduler.Schedule(ctx, "session-1", p2)

	scheduler.OnPipelineComplete("session-1", "p1")

	stats := scheduler.GetSessionStats("session-1")
	if stats.Active != 1 {
		t.Errorf("expected 1 active after dispatch, got %d", stats.Active)
	}
	if stats.Queued != 0 {
		t.Errorf("expected 0 queued after dispatch, got %d", stats.Queued)
	}
}

func TestGlobalPipelineScheduler_MultipleSessions(t *testing.T) {
	scheduler := NewGlobalPipelineScheduler(PipelineSchedulerConfig{
		MaxConcurrent: 4,
	})
	defer scheduler.Close()

	ctx := context.Background()

	for i := 0; i < 2; i++ {
		sessionID := "session-" + string(rune('A'+i))
		p := &concurrency.SchedulablePipeline{
			ID:        "p-" + sessionID,
			SpawnTime: time.Now(),
		}
		scheduler.Schedule(ctx, sessionID, p)
	}

	globalStats := scheduler.GetGlobalStats()
	if globalStats.SessionCount != 2 {
		t.Errorf("expected 2 sessions, got %d", globalStats.SessionCount)
	}
	if globalStats.TotalActive != 2 {
		t.Errorf("expected 2 total active, got %d", globalStats.TotalActive)
	}
}

func TestGlobalPipelineScheduler_GlobalLimit(t *testing.T) {
	scheduler := NewGlobalPipelineScheduler(PipelineSchedulerConfig{
		MaxConcurrent: 2,
	})
	defer scheduler.Close()

	ctx := context.Background()

	for i := 0; i < 3; i++ {
		p := &concurrency.SchedulablePipeline{
			ID:        "p" + string(rune('1'+i)),
			SpawnTime: time.Now(),
		}
		scheduler.Schedule(ctx, "session-1", p)
	}

	globalStats := scheduler.GetGlobalStats()
	if globalStats.TotalActive != 2 {
		t.Errorf("expected 2 active (global limit), got %d", globalStats.TotalActive)
	}
	if globalStats.TotalQueued != 1 {
		t.Errorf("expected 1 queued, got %d", globalStats.TotalQueued)
	}
}

func TestGlobalPipelineScheduler_CancelPipeline(t *testing.T) {
	scheduler := NewGlobalPipelineScheduler(PipelineSchedulerConfig{
		MaxConcurrent: 1,
	})
	defer scheduler.Close()

	ctx := context.Background()

	p1 := &concurrency.SchedulablePipeline{ID: "p1", SpawnTime: time.Now()}
	p2 := &concurrency.SchedulablePipeline{ID: "p2", SpawnTime: time.Now()}

	scheduler.Schedule(ctx, "session-1", p1)
	scheduler.Schedule(ctx, "session-1", p2)

	cancelled := scheduler.CancelPipeline("session-1", "p2")
	if !cancelled {
		t.Error("expected pipeline to be cancelled")
	}

	stats := scheduler.GetSessionStats("session-1")
	if stats.Queued != 0 {
		t.Errorf("expected 0 queued after cancel, got %d", stats.Queued)
	}
}

func TestGlobalPipelineScheduler_CancelPipeline_NotFound(t *testing.T) {
	scheduler := NewGlobalPipelineScheduler(PipelineSchedulerConfig{})
	defer scheduler.Close()

	cancelled := scheduler.CancelPipeline("session-1", "nonexistent")
	if cancelled {
		t.Error("expected false for nonexistent pipeline")
	}
}

func TestGlobalPipelineScheduler_GetSessionStats_Empty(t *testing.T) {
	scheduler := NewGlobalPipelineScheduler(PipelineSchedulerConfig{
		MaxConcurrent: 4,
	})
	defer scheduler.Close()

	stats := scheduler.GetSessionStats("nonexistent")
	if stats.Active != 0 {
		t.Errorf("expected 0 active for nonexistent session, got %d", stats.Active)
	}
	if stats.Queued != 0 {
		t.Errorf("expected 0 queued for nonexistent session, got %d", stats.Queued)
	}
}

func TestGlobalPipelineScheduler_Rebalance(t *testing.T) {
	scheduler := NewGlobalPipelineScheduler(PipelineSchedulerConfig{
		MaxConcurrent: 1,
	})
	defer scheduler.Close()

	ctx := context.Background()

	p1 := &concurrency.SchedulablePipeline{ID: "p1", SpawnTime: time.Now()}
	p2 := &concurrency.SchedulablePipeline{ID: "p2", SpawnTime: time.Now()}

	scheduler.Schedule(ctx, "session-1", p1)
	scheduler.Schedule(ctx, "session-2", p2)

	scheduler.decrementActive("session-1")
	scheduler.Rebalance()

	globalStats := scheduler.GetGlobalStats()
	if globalStats.TotalActive != 1 {
		t.Errorf("expected 1 active after rebalance, got %d", globalStats.TotalActive)
	}
}

func TestGlobalPipelineScheduler_Close(t *testing.T) {
	scheduler := NewGlobalPipelineScheduler(PipelineSchedulerConfig{})

	err := scheduler.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	_, err = scheduler.Schedule(context.Background(), "session-1", &concurrency.SchedulablePipeline{ID: "p1"})
	if err != ErrSchedulerClosed {
		t.Errorf("expected ErrSchedulerClosed, got %v", err)
	}
}

func TestGlobalPipelineScheduler_DoubleClose(t *testing.T) {
	scheduler := NewGlobalPipelineScheduler(PipelineSchedulerConfig{})

	scheduler.Close()
	err := scheduler.Close()

	if err != ErrSchedulerClosed {
		t.Errorf("expected ErrSchedulerClosed on double close, got %v", err)
	}
}

func TestGlobalPipelineScheduler_ConcurrentSchedule(t *testing.T) {
	scheduler := NewGlobalPipelineScheduler(PipelineSchedulerConfig{
		MaxConcurrent: 10,
	})
	defer scheduler.Close()

	ctx := context.Background()
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			p := &concurrency.SchedulablePipeline{
				ID:        "p" + string(rune(n)),
				SpawnTime: time.Now(),
			}
			scheduler.Schedule(ctx, "session-"+string(rune('A'+n%5)), p)
		}(i)
	}

	wg.Wait()

	globalStats := scheduler.GetGlobalStats()
	if globalStats.TotalActive+globalStats.TotalQueued != 50 {
		t.Errorf("expected 50 total pipelines, got %d active + %d queued",
			globalStats.TotalActive, globalStats.TotalQueued)
	}
}

func TestGlobalPipelineScheduler_ConcurrentComplete(t *testing.T) {
	scheduler := NewGlobalPipelineScheduler(PipelineSchedulerConfig{
		MaxConcurrent: 20,
	})
	defer scheduler.Close()

	ctx := context.Background()

	for i := 0; i < 20; i++ {
		p := &concurrency.SchedulablePipeline{
			ID:        "p" + string(rune(i)),
			SpawnTime: time.Now(),
		}
		scheduler.Schedule(ctx, "session-1", p)
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			scheduler.OnPipelineComplete("session-1", "p"+string(rune(n)))
		}(i)
	}

	wg.Wait()

	stats := scheduler.GetSessionStats("session-1")
	if stats.Active != 0 {
		t.Errorf("expected 0 active after all complete, got %d", stats.Active)
	}
}

func TestGlobalPipelineScheduler_CrossSessionDispatch(t *testing.T) {
	scheduler := NewGlobalPipelineScheduler(PipelineSchedulerConfig{
		MaxConcurrent: 1,
	})
	defer scheduler.Close()

	ctx := context.Background()

	p1 := &concurrency.SchedulablePipeline{ID: "p1", SpawnTime: time.Now()}
	p2 := &concurrency.SchedulablePipeline{ID: "p2", SpawnTime: time.Now()}

	scheduler.Schedule(ctx, "session-1", p1)
	scheduler.Schedule(ctx, "session-2", p2)

	scheduler.OnPipelineComplete("session-1", "p1")

	statsA := scheduler.GetSessionStats("session-1")
	statsB := scheduler.GetSessionStats("session-2")

	if statsA.Active != 0 {
		t.Errorf("expected 0 active for session-1, got %d", statsA.Active)
	}
	if statsB.Active != 1 {
		t.Errorf("expected 1 active for session-2 after dispatch, got %d", statsB.Active)
	}
}

func TestGlobalPipelineScheduler_OnPipelineComplete_Closed(t *testing.T) {
	scheduler := NewGlobalPipelineScheduler(PipelineSchedulerConfig{})
	scheduler.Close()

	err := scheduler.OnPipelineComplete("session-1", "p1")
	if err != ErrSchedulerClosed {
		t.Errorf("expected ErrSchedulerClosed, got %v", err)
	}
}

func TestGlobalPipelineScheduler_CancelPipeline_Closed(t *testing.T) {
	scheduler := NewGlobalPipelineScheduler(PipelineSchedulerConfig{})
	scheduler.Close()

	cancelled := scheduler.CancelPipeline("session-1", "p1")
	if cancelled {
		t.Error("expected false when closed")
	}
}

func TestGlobalPipelineScheduler_GetActivePipelines(t *testing.T) {
	scheduler := NewGlobalPipelineScheduler(PipelineSchedulerConfig{
		MaxConcurrent: 1,
	})
	defer scheduler.Close()

	ctx := context.Background()

	p1 := &concurrency.SchedulablePipeline{
		ID:       "p1",
		Priority: concurrency.PriorityLow,
	}
	p2 := &concurrency.SchedulablePipeline{
		ID:       "p2",
		Priority: concurrency.PriorityHigh,
	}
	p3 := &concurrency.SchedulablePipeline{
		ID:       "p3",
		Priority: concurrency.PriorityNormal,
	}

	scheduler.Schedule(ctx, "s1", p1)
	scheduler.Schedule(ctx, "s1", p2)
	scheduler.Schedule(ctx, "s2", p3)

	active := scheduler.GetActivePipelines()

	if len(active) < 2 {
		t.Errorf("expected at least 2 queued pipelines, got %d", len(active))
	}

	for i := 1; i < len(active); i++ {
		if active[i].Priority < active[i-1].Priority {
			t.Error("pipelines should be sorted by priority ascending")
		}
	}
}

func TestGlobalPipelineScheduler_GetActivePipelines_Empty(t *testing.T) {
	scheduler := NewGlobalPipelineScheduler(PipelineSchedulerConfig{})
	defer scheduler.Close()

	active := scheduler.GetActivePipelines()

	if len(active) != 0 {
		t.Errorf("expected empty list, got %d items", len(active))
	}
}
