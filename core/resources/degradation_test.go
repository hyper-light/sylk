package resources

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/signal"
)

type mockCheckpointer struct {
	mu          sync.Mutex
	checkpoints []string
}

func (m *mockCheckpointer) Checkpoint(pipelineID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checkpoints = append(m.checkpoints, pipelineID)
	return nil
}

func (m *mockCheckpointer) getCheckpoints() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.checkpoints))
	copy(result, m.checkpoints)
	return result
}

type mockResourceReleaser struct {
	mu       sync.Mutex
	released []string
}

func (m *mockResourceReleaser) ReleaseBundleByID(pipelineID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.released = append(m.released, pipelineID)
	return nil
}

func (m *mockResourceReleaser) getReleased() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.released))
	copy(result, m.released)
	return result
}

type mockUserNotifier struct {
	mu       sync.Mutex
	paused   []string
	resumed  []string
	pauseErr atomic.Bool
}

func (m *mockUserNotifier) NotifyPaused(pipelineID string, _ string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.paused = append(m.paused, pipelineID)
}

func (m *mockUserNotifier) NotifyResumed(pipelineID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resumed = append(m.resumed, pipelineID)
}

func (m *mockUserNotifier) getPaused() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.paused))
	copy(result, m.paused)
	return result
}

func (m *mockUserNotifier) getResumed() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.resumed))
	copy(result, m.resumed)
	return result
}

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

	if config.ResumeInterval != 100*time.Millisecond {
		t.Errorf("expected 100ms, got %v", config.ResumeInterval)
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

func TestDegradationController_UserInteractiveNeverPaused(t *testing.T) {
	dc := NewDegradationController(DefaultDegradationConfig(), nil, nil)
	defer dc.Close()

	dc.RegisterPipelineWithOptions("interactive", PriorityLow, true)
	dc.RegisterPipeline("background", PriorityLow)

	ok := dc.PausePipeline("interactive")
	if ok {
		t.Error("user-interactive pipeline should not be pausable")
	}

	state, _ := dc.GetPipelineState("interactive")
	if state != StateRunning {
		t.Error("user-interactive should remain running")
	}

	ok = dc.PausePipeline("background")
	if !ok {
		t.Error("background pipeline should be pausable")
	}
}

func TestDegradationController_UserInteractiveInStats(t *testing.T) {
	dc := NewDegradationController(DefaultDegradationConfig(), nil, nil)
	defer dc.Close()

	dc.RegisterPipelineWithOptions("interactive", PriorityHigh, true)
	dc.RegisterPipeline("background", PriorityLow)

	stats := dc.Stats()

	var foundInteractive, foundBackground bool
	for _, ps := range stats.Pipelines {
		if ps.ID == "interactive" {
			foundInteractive = true
			if !ps.IsUserInteractive {
				t.Error("interactive pipeline should be marked as user-interactive")
			}
		}
		if ps.ID == "background" {
			foundBackground = true
			if ps.IsUserInteractive {
				t.Error("background pipeline should not be user-interactive")
			}
		}
	}

	if !foundInteractive || !foundBackground {
		t.Error("expected to find both pipelines in stats")
	}
}

func TestDegradationController_CheckpointerIntegration(t *testing.T) {
	dc := NewDegradationController(DefaultDegradationConfig(), nil, nil)
	defer dc.Close()

	cp := &mockCheckpointer{}
	dc.SetCheckpointer(cp)

	dc.RegisterPipeline("p1", PriorityMedium)

	dc.PausePipeline("p1")

	checkpoints := cp.getCheckpoints()
	if len(checkpoints) != 1 || checkpoints[0] != "p1" {
		t.Errorf("expected checkpoint for p1, got %v", checkpoints)
	}
}

func TestDegradationController_ResourceReleaserIntegration(t *testing.T) {
	dc := NewDegradationController(DefaultDegradationConfig(), nil, nil)
	defer dc.Close()

	rr := &mockResourceReleaser{}
	dc.SetResourceReleaser(rr)

	dc.RegisterPipeline("p1", PriorityMedium)

	dc.PausePipeline("p1")

	released := rr.getReleased()
	if len(released) != 1 || released[0] != "p1" {
		t.Errorf("expected release for p1, got %v", released)
	}
}

func TestDegradationController_UserNotifierIntegration(t *testing.T) {
	dc := NewDegradationController(DefaultDegradationConfig(), nil, nil)
	defer dc.Close()

	notifier := &mockUserNotifier{}
	dc.SetUserNotifier(notifier)

	dc.RegisterPipeline("p1", PriorityMedium)

	dc.PausePipeline("p1")
	paused := notifier.getPaused()
	if len(paused) != 1 || paused[0] != "p1" {
		t.Errorf("expected pause notification for p1, got %v", paused)
	}

	dc.ResumePipeline("p1")
	resumed := notifier.getResumed()
	if len(resumed) != 1 || resumed[0] != "p1" {
		t.Errorf("expected resume notification for p1, got %v", resumed)
	}
}

func TestSortPipelinesForPause(t *testing.T) {
	now := time.Now()

	entries := []*PipelineEntry{
		{ID: "high-old", Priority: PriorityHigh, CreatedAt: now.Add(-2 * time.Hour)},
		{ID: "low-new", Priority: PriorityLow, CreatedAt: now.Add(-1 * time.Minute)},
		{ID: "low-old", Priority: PriorityLow, CreatedAt: now.Add(-1 * time.Hour)},
		{ID: "medium", Priority: PriorityMedium, CreatedAt: now},
	}

	sortPipelinesForPause(entries)

	expected := []string{"low-new", "low-old", "medium", "high-old"}
	for i, id := range expected {
		if entries[i].ID != id {
			t.Errorf("position %d: expected %s, got %s", i, id, entries[i].ID)
		}
	}
}

func TestSortPipelinesForResume(t *testing.T) {
	now := time.Now()

	entries := []*PipelineEntry{
		{ID: "low", Priority: PriorityLow, PausedAt: now},
		{ID: "high-new", Priority: PriorityHigh, PausedAt: now.Add(-1 * time.Minute)},
		{ID: "high-old", Priority: PriorityHigh, PausedAt: now.Add(-1 * time.Hour)},
		{ID: "medium", Priority: PriorityMedium, PausedAt: now},
	}

	sortPipelinesForResume(entries)

	expected := []string{"high-old", "high-new", "medium", "low"}
	for i, id := range expected {
		if entries[i].ID != id {
			t.Errorf("position %d: expected %s, got %s", i, id, entries[i].ID)
		}
	}
}

func TestDegradationController_SelectPauseablePipelines(t *testing.T) {
	dc := NewDegradationController(DefaultDegradationConfig(), nil, nil)
	defer dc.Close()

	dc.RegisterPipelineWithOptions("interactive-low", PriorityLow, true)
	dc.RegisterPipeline("background-low", PriorityLow)
	dc.RegisterPipeline("background-high", PriorityHigh)
	dc.RegisterPipeline("background-critical", PriorityCritical)

	dc.mu.Lock()
	candidates := dc.selectPauseablePipelines(LevelCritical)
	dc.mu.Unlock()

	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}

	for _, c := range candidates {
		if c.IsUserInteractive {
			t.Error("user-interactive should not be in candidates")
		}
		if c.Priority >= PriorityHigh {
			t.Errorf("priority %d should not be in candidates for LevelCritical", c.Priority)
		}
	}
}

func TestDegradationController_NilIntegrations(t *testing.T) {
	dc := NewDegradationController(DefaultDegradationConfig(), nil, nil)
	defer dc.Close()

	dc.RegisterPipeline("p1", PriorityMedium)

	dc.PausePipeline("p1")
	dc.ResumePipeline("p1")
}

func TestDegradationController_ConcurrentSetters(t *testing.T) {
	dc := NewDegradationController(DefaultDegradationConfig(), nil, nil)
	defer dc.Close()

	var wg sync.WaitGroup

	for range 10 {
		wg.Add(3)
		go func() {
			defer wg.Done()
			dc.SetCheckpointer(&mockCheckpointer{})
		}()
		go func() {
			defer wg.Done()
			dc.SetResourceReleaser(&mockResourceReleaser{})
		}()
		go func() {
			defer wg.Done()
			dc.SetUserNotifier(&mockUserNotifier{})
		}()
	}

	wg.Wait()
}

func TestDegradationController_RegisterWithOptions(t *testing.T) {
	dc := NewDegradationController(DefaultDegradationConfig(), nil, nil)
	defer dc.Close()

	before := time.Now()
	dc.RegisterPipelineWithOptions("test", PriorityHigh, true)
	after := time.Now()

	dc.mu.RLock()
	entry := dc.pipelines["test"]
	dc.mu.RUnlock()

	if entry == nil {
		t.Fatal("expected entry to exist")
	}

	if entry.Priority != PriorityHigh {
		t.Errorf("expected PriorityHigh, got %d", entry.Priority)
	}

	if !entry.IsUserInteractive {
		t.Error("expected IsUserInteractive to be true")
	}

	if entry.CreatedAt.Before(before) || entry.CreatedAt.After(after) {
		t.Error("CreatedAt should be set to current time")
	}
}

func TestDegradationController_FullPauseResumeIntegration(t *testing.T) {
	dc := NewDegradationController(DefaultDegradationConfig(), nil, nil)
	defer dc.Close()

	cp := &mockCheckpointer{}
	rr := &mockResourceReleaser{}
	notifier := &mockUserNotifier{}

	dc.SetCheckpointer(cp)
	dc.SetResourceReleaser(rr)
	dc.SetUserNotifier(notifier)

	dc.RegisterPipeline("p1", PriorityMedium)
	dc.RegisterPipeline("p2", PriorityLow)
	dc.RegisterPipelineWithOptions("interactive", PriorityLow, true)

	dc.PausePipeline("p1")
	dc.PausePipeline("p2")
	dc.PausePipeline("interactive")

	checkpoints := cp.getCheckpoints()
	if len(checkpoints) != 2 {
		t.Errorf("expected 2 checkpoints, got %d", len(checkpoints))
	}

	released := rr.getReleased()
	if len(released) != 2 {
		t.Errorf("expected 2 releases, got %d", len(released))
	}

	paused := notifier.getPaused()
	if len(paused) != 2 {
		t.Errorf("expected 2 pause notifications, got %d", len(paused))
	}

	dc.ResumePipeline("p1")
	dc.ResumePipeline("p2")

	resumed := notifier.getResumed()
	if len(resumed) != 2 {
		t.Errorf("expected 2 resume notifications, got %d", len(resumed))
	}
}
