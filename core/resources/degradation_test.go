package resources

import (
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/signal"
)

func newTestDegradationController(t *testing.T) (*DegradationController, *MemoryMonitor) {
	t.Helper()

	memConfig := DefaultMemoryMonitorConfig()
	memConfig.MonitorInterval = 100 * time.Millisecond
	memMon := NewMemoryMonitor(memConfig)

	config := DefaultDegradationConfig()
	config.CheckInterval = 50 * time.Millisecond

	busConfig := signal.DefaultSignalBusConfig()
	bus := signal.NewSignalBus(busConfig)

	t.Cleanup(func() {
		bus.Close()
		_ = memMon.Close()
	})

	dc := NewDegradationController(config, memMon, bus)
	return dc, memMon
}

func TestDegradationController_RegisterUnregister(t *testing.T) {
	dc, _ := newTestDegradationController(t)
	defer dc.Close()

	dc.RegisterPipeline("pipeline-1", PriorityMedium)
	dc.RegisterPipeline("pipeline-2", PriorityHigh)

	stats := dc.Stats()
	if stats.TotalPipelines != 2 {
		t.Errorf("expected 2 pipelines, got %d", stats.TotalPipelines)
	}

	dc.UnregisterPipeline("pipeline-1")
	stats = dc.Stats()
	if stats.TotalPipelines != 1 {
		t.Errorf("expected 1 pipeline, got %d", stats.TotalPipelines)
	}
}

func TestDegradationController_GetPipelineState(t *testing.T) {
	dc, _ := newTestDegradationController(t)
	defer dc.Close()

	dc.RegisterPipeline("pipeline-1", PriorityMedium)

	state, found := dc.GetPipelineState("pipeline-1")
	if !found {
		t.Fatal("expected to find pipeline")
	}
	if state != StateRunning {
		t.Errorf("expected StateRunning, got %d", state)
	}

	_, found = dc.GetPipelineState("nonexistent")
	if found {
		t.Error("expected not to find nonexistent pipeline")
	}
}

func TestDegradationController_ManualPauseResume(t *testing.T) {
	dc, _ := newTestDegradationController(t)
	defer dc.Close()

	dc.RegisterPipeline("pipeline-1", PriorityMedium)

	ok := dc.PausePipeline("pipeline-1")
	if !ok {
		t.Fatal("PausePipeline should succeed")
	}

	state, _ := dc.GetPipelineState("pipeline-1")
	if state != StatePaused {
		t.Errorf("expected StatePaused, got %d", state)
	}

	ok = dc.PausePipeline("pipeline-1")
	if ok {
		t.Error("PausePipeline should fail when already paused")
	}

	ok = dc.ResumePipeline("pipeline-1")
	if !ok {
		t.Fatal("ResumePipeline should succeed")
	}

	state, _ = dc.GetPipelineState("pipeline-1")
	if state != StateRunning {
		t.Errorf("expected StateRunning, got %d", state)
	}

	ok = dc.ResumePipeline("pipeline-1")
	if ok {
		t.Error("ResumePipeline should fail when already running")
	}
}

func TestDegradationController_PauseResumeNonexistent(t *testing.T) {
	dc, _ := newTestDegradationController(t)
	defer dc.Close()

	ok := dc.PausePipeline("nonexistent")
	if ok {
		t.Error("PausePipeline should fail for nonexistent pipeline")
	}

	ok = dc.ResumePipeline("nonexistent")
	if ok {
		t.Error("ResumePipeline should fail for nonexistent pipeline")
	}
}

func TestDegradationController_Stats(t *testing.T) {
	dc, _ := newTestDegradationController(t)
	defer dc.Close()

	dc.RegisterPipeline("p1", PriorityLow)
	dc.RegisterPipeline("p2", PriorityHigh)
	dc.RegisterPipeline("p3", PriorityCritical)

	dc.PausePipeline("p1")

	stats := dc.Stats()

	if stats.TotalPipelines != 3 {
		t.Errorf("expected 3 total, got %d", stats.TotalPipelines)
	}

	if stats.RunningCount != 2 {
		t.Errorf("expected 2 running, got %d", stats.RunningCount)
	}

	if stats.PausedCount != 1 {
		t.Errorf("expected 1 paused, got %d", stats.PausedCount)
	}

	if stats.Level != LevelNormal {
		t.Errorf("expected LevelNormal, got %d", stats.Level)
	}
}

func TestDegradationController_CalculateLevel(t *testing.T) {
	config := DefaultDegradationConfig()
	dc := NewDegradationController(config, nil, nil)
	defer dc.Close()

	tests := []struct {
		usage    float64
		expected DegradationLevel
	}{
		{0.50, LevelNormal},
		{0.69, LevelNormal},
		{0.70, LevelWarning},
		{0.84, LevelWarning},
		{0.85, LevelCritical},
		{0.94, LevelCritical},
		{0.95, LevelEmergency},
		{1.00, LevelEmergency},
	}

	for _, tc := range tests {
		level := dc.calculateLevel(tc.usage)
		if level != tc.expected {
			t.Errorf("usage %.2f: expected %d, got %d", tc.usage, tc.expected, level)
		}
	}
}

func TestDegradationController_LevelToPriority(t *testing.T) {
	dc := NewDegradationController(DefaultDegradationConfig(), nil, nil)
	defer dc.Close()

	tests := []struct {
		level    DegradationLevel
		expected PipelinePriority
	}{
		{LevelNormal, PriorityLow},
		{LevelWarning, PriorityMedium},
		{LevelCritical, PriorityHigh},
		{LevelEmergency, PriorityCritical},
	}

	for _, tc := range tests {
		priority := dc.levelToPriority(tc.level)
		if priority != tc.expected {
			t.Errorf("level %d: expected %d, got %d", tc.level, tc.expected, priority)
		}
	}
}

func TestDegradationController_ConcurrentOperations(t *testing.T) {
	dc, _ := newTestDegradationController(t)
	defer dc.Close()

	for i := range 10 {
		dc.RegisterPipeline(string(rune('A'+i)), PipelinePriority(i%4))
	}

	var wg sync.WaitGroup

	for i := range 20 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id := string(rune('A' + (idx % 10)))
			if idx%2 == 0 {
				dc.PausePipeline(id)
			} else {
				dc.ResumePipeline(id)
			}
		}(i)
	}

	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = dc.Stats()
		}()
	}

	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = dc.GetLevel()
		}()
	}

	wg.Wait()
}

func TestDegradationController_NilMemoryMonitor(t *testing.T) {
	config := DefaultDegradationConfig()
	dc := NewDegradationController(config, nil, nil)

	dc.RegisterPipeline("p1", PriorityMedium)

	ok := dc.PausePipeline("p1")
	if !ok {
		t.Fatal("should be able to pause with nil monitor")
	}

	err := dc.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}
}

func TestDegradationController_NilSignalBus(t *testing.T) {
	memConfig := DefaultMemoryMonitorConfig()
	memConfig.MonitorInterval = 10 * time.Second
	memMon := NewMemoryMonitor(memConfig)
	defer memMon.Close()

	config := DefaultDegradationConfig()
	dc := NewDegradationController(config, memMon, nil)
	defer dc.Close()

	dc.RegisterPipeline("p1", PriorityMedium)

	ok := dc.PausePipeline("p1")
	if !ok {
		t.Fatal("should be able to pause with nil bus")
	}

	ok = dc.ResumePipeline("p1")
	if !ok {
		t.Fatal("should be able to resume with nil bus")
	}
}

func TestDegradationController_PauseIncrementsCount(t *testing.T) {
	dc, _ := newTestDegradationController(t)
	defer dc.Close()

	dc.RegisterPipeline("p1", PriorityMedium)

	dc.PausePipeline("p1")
	dc.ResumePipeline("p1")
	dc.PausePipeline("p1")
	dc.ResumePipeline("p1")
	dc.PausePipeline("p1")

	stats := dc.Stats()
	for _, ps := range stats.Pipelines {
		if ps.ID == "p1" && ps.PauseCount != 3 {
			t.Errorf("expected 3 pause count, got %d", ps.PauseCount)
		}
	}
}

func TestDegradationController_DefaultConfig(t *testing.T) {
	config := DefaultDegradationConfig()

	if config.WarningThreshold != 0.70 {
		t.Errorf("expected 0.70, got %f", config.WarningThreshold)
	}

	if config.CriticalThreshold != 0.85 {
		t.Errorf("expected 0.85, got %f", config.CriticalThreshold)
	}

	if config.EmergencyThreshold != 0.95 {
		t.Errorf("expected 0.95, got %f", config.EmergencyThreshold)
	}

	if config.CheckInterval != 1*time.Second {
		t.Errorf("expected 1s, got %v", config.CheckInterval)
	}

	if config.ResumeHysteresis != 0.10 {
		t.Errorf("expected 0.10, got %f", config.ResumeHysteresis)
	}
}

func TestPipelineEntry_StateOperations(t *testing.T) {
	entry := &PipelineEntry{
		ID:       "test",
		Priority: PriorityMedium,
	}

	if entry.GetState() != StateRunning {
		t.Error("initial state should be running")
	}

	entry.SetState(StatePaused)
	if entry.GetState() != StatePaused {
		t.Error("state should be paused")
	}

	entry.SetState(StateResuming)
	if entry.GetState() != StateResuming {
		t.Error("state should be resuming")
	}
}
