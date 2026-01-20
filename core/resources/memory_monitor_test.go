package resources

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// Mock Signal Publisher for Testing
// =============================================================================

type mockSignalPublisher struct {
	mu       sync.Mutex
	signals  []MemorySignal
	payloads []MemorySignalPayload
}

func newMockSignalPublisher() *mockSignalPublisher {
	return &mockSignalPublisher{
		signals:  make([]MemorySignal, 0),
		payloads: make([]MemorySignalPayload, 0),
	}
}

func (m *mockSignalPublisher) PublishMemorySignal(signal MemorySignal, payload MemorySignalPayload) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.signals = append(m.signals, signal)
	m.payloads = append(m.payloads, payload)
	return nil
}

func (m *mockSignalPublisher) getSignals() []MemorySignal {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]MemorySignal, len(m.signals))
	copy(result, m.signals)
	return result
}

func (m *mockSignalPublisher) getPayloads() []MemorySignalPayload {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]MemorySignalPayload, len(m.payloads))
	copy(result, m.payloads)
	return result
}

func (m *mockSignalPublisher) hasSignal(signal MemorySignal) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.signals {
		if s == signal {
			return true
		}
	}
	return false
}

func (m *mockSignalPublisher) clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.signals = make([]MemorySignal, 0)
	m.payloads = make([]MemorySignalPayload, 0)
}

// =============================================================================
// ComponentMemory Tests
// =============================================================================

func TestComponentMemory_NewComponentMemory(t *testing.T) {
	comp := NewComponentMemory(ComponentQueryCache, 1000)

	if comp.Name() != ComponentQueryCache {
		t.Errorf("expected name %s, got %s", ComponentQueryCache, comp.Name())
	}
	if comp.Budget() != 1000 {
		t.Errorf("expected budget 1000, got %d", comp.Budget())
	}
	if comp.Current() != 0 {
		t.Errorf("expected current 0, got %d", comp.Current())
	}
	if comp.Peak() != 0 {
		t.Errorf("expected peak 0, got %d", comp.Peak())
	}
}

func TestComponentMemory_Add(t *testing.T) {
	comp := NewComponentMemory(ComponentStaging, 1000)

	result := comp.Add(500)
	if result != 500 {
		t.Errorf("expected 500, got %d", result)
	}
	if comp.Current() != 500 {
		t.Errorf("expected current 500, got %d", comp.Current())
	}
	if comp.Peak() != 500 {
		t.Errorf("expected peak 500, got %d", comp.Peak())
	}

	// Add more and check peak updates
	comp.Add(300)
	if comp.Current() != 800 {
		t.Errorf("expected current 800, got %d", comp.Current())
	}
	if comp.Peak() != 800 {
		t.Errorf("expected peak 800, got %d", comp.Peak())
	}
}

func TestComponentMemory_Remove(t *testing.T) {
	comp := NewComponentMemory(ComponentWAL, 1000)

	comp.Add(500)
	result := comp.Remove(200)

	if result != 300 {
		t.Errorf("expected 300, got %d", result)
	}
	if comp.Current() != 300 {
		t.Errorf("expected current 300, got %d", comp.Current())
	}
	// Peak should remain at 500
	if comp.Peak() != 500 {
		t.Errorf("expected peak 500, got %d", comp.Peak())
	}
}

func TestComponentMemory_Remove_NegativeClamp(t *testing.T) {
	comp := NewComponentMemory(ComponentQueryCache, 1000)

	comp.Add(100)
	result := comp.Remove(200) // Remove more than current

	if result != 0 {
		t.Errorf("expected 0 (clamped), got %d", result)
	}
	if comp.Current() != 0 {
		t.Errorf("expected current 0, got %d", comp.Current())
	}
}

func TestComponentMemory_SetBudget(t *testing.T) {
	comp := NewComponentMemory(ComponentAgentContext, 1000)

	comp.SetBudget(2000)
	if comp.Budget() != 2000 {
		t.Errorf("expected budget 2000, got %d", comp.Budget())
	}
}

func TestComponentMemory_UsagePercent(t *testing.T) {
	comp := NewComponentMemory(ComponentStaging, 1000)

	if comp.UsagePercent() != 0 {
		t.Errorf("expected 0%%, got %f", comp.UsagePercent())
	}

	comp.Add(500)
	if comp.UsagePercent() != 0.5 {
		t.Errorf("expected 50%%, got %f", comp.UsagePercent())
	}

	comp.Add(500)
	if comp.UsagePercent() != 1.0 {
		t.Errorf("expected 100%%, got %f", comp.UsagePercent())
	}
}

func TestComponentMemory_UsagePercent_ZeroBudget(t *testing.T) {
	comp := NewComponentMemory(ComponentQueryCache, 0)

	if comp.UsagePercent() != 0 {
		t.Errorf("expected 0%% for zero budget, got %f", comp.UsagePercent())
	}
}

func TestComponentMemory_Stats(t *testing.T) {
	comp := NewComponentMemory(ComponentWAL, 1000)
	comp.Add(700)

	stats := comp.Stats()

	if stats.Name != ComponentWAL {
		t.Errorf("expected name %s, got %s", ComponentWAL, stats.Name)
	}
	if stats.Budget != 1000 {
		t.Errorf("expected budget 1000, got %d", stats.Budget)
	}
	if stats.Current != 700 {
		t.Errorf("expected current 700, got %d", stats.Current)
	}
	if stats.Peak != 700 {
		t.Errorf("expected peak 700, got %d", stats.Peak)
	}
	if stats.UsagePercent != 0.7 {
		t.Errorf("expected usage 70%%, got %f", stats.UsagePercent)
	}
}

// =============================================================================
// MemoryMonitor Tests - Basic Operations
// =============================================================================

func TestMemoryMonitor_NewMemoryMonitor(t *testing.T) {
	config := DefaultMemoryMonitorConfig()
	config.MonitorInterval = 100 * time.Millisecond // Short interval for testing
	monitor := NewMemoryMonitor(config)
	defer monitor.Close()

	if monitor == nil {
		t.Fatal("expected non-nil monitor")
	}

	// Check all components are initialized
	components := []ComponentName{
		ComponentQueryCache,
		ComponentStaging,
		ComponentAgentContext,
		ComponentWAL,
	}

	for _, name := range components {
		comp := monitor.GetComponent(name)
		if comp == nil {
			t.Errorf("expected component %s to be initialized", name)
		}
	}
}

func TestMemoryMonitor_NilSignalPublisher(t *testing.T) {
	config := DefaultMemoryMonitorConfig()
	config.SignalPublisher = nil // Explicitly nil
	config.MonitorInterval = 100 * time.Millisecond

	monitor := NewMemoryMonitor(config)
	defer monitor.Close()

	// Should not panic - NoOpSignalPublisher should be used
	monitor.AddUsage(ComponentQueryCache, 100)
}

func TestMemoryMonitor_AddRemoveUsage(t *testing.T) {
	config := DefaultMemoryMonitorConfig()
	config.MonitorInterval = 100 * time.Millisecond
	monitor := NewMemoryMonitor(config)
	defer monitor.Close()

	result := monitor.AddUsage(ComponentQueryCache, 1000)
	if result != 1000 {
		t.Errorf("expected 1000, got %d", result)
	}

	result = monitor.RemoveUsage(ComponentQueryCache, 400)
	if result != 600 {
		t.Errorf("expected 600, got %d", result)
	}
}

func TestMemoryMonitor_AddUsage_InvalidComponent(t *testing.T) {
	config := DefaultMemoryMonitorConfig()
	config.MonitorInterval = 100 * time.Millisecond
	monitor := NewMemoryMonitor(config)
	defer monitor.Close()

	result := monitor.AddUsage("invalid_component", 1000)
	if result != 0 {
		t.Errorf("expected 0 for invalid component, got %d", result)
	}
}

func TestMemoryMonitor_RemoveUsage_InvalidComponent(t *testing.T) {
	config := DefaultMemoryMonitorConfig()
	config.MonitorInterval = 100 * time.Millisecond
	monitor := NewMemoryMonitor(config)
	defer monitor.Close()

	result := monitor.RemoveUsage("invalid_component", 1000)
	if result != 0 {
		t.Errorf("expected 0 for invalid component, got %d", result)
	}
}

func TestMemoryMonitor_SetComponentBudget(t *testing.T) {
	config := DefaultMemoryMonitorConfig()
	config.MonitorInterval = 100 * time.Millisecond
	monitor := NewMemoryMonitor(config)
	defer monitor.Close()

	newBudget := int64(999999)
	monitor.SetComponentBudget(ComponentQueryCache, newBudget)

	comp := monitor.GetComponent(ComponentQueryCache)
	if comp.Budget() != newBudget {
		t.Errorf("expected budget %d, got %d", newBudget, comp.Budget())
	}
}

func TestMemoryMonitor_SetComponentBudget_InvalidComponent(t *testing.T) {
	config := DefaultMemoryMonitorConfig()
	config.MonitorInterval = 100 * time.Millisecond
	monitor := NewMemoryMonitor(config)
	defer monitor.Close()

	// Should not panic
	monitor.SetComponentBudget("invalid_component", 1000)
}

// =============================================================================
// MemoryMonitor Tests - Global State
// =============================================================================

func TestMemoryMonitor_GlobalUsage(t *testing.T) {
	config := DefaultMemoryMonitorConfig()
	config.MonitorInterval = 100 * time.Millisecond
	monitor := NewMemoryMonitor(config)
	defer monitor.Close()

	monitor.AddUsage(ComponentQueryCache, 100)
	monitor.AddUsage(ComponentStaging, 200)
	monitor.AddUsage(ComponentAgentContext, 300)
	monitor.AddUsage(ComponentWAL, 400)

	total := monitor.GlobalUsage()
	if total != 1000 {
		t.Errorf("expected global usage 1000, got %d", total)
	}
}

func TestMemoryMonitor_GlobalCeiling(t *testing.T) {
	config := DefaultMemoryMonitorConfig()
	config.MonitorInterval = 100 * time.Millisecond
	monitor := NewMemoryMonitor(config)
	defer monitor.Close()

	ceiling := monitor.GlobalCeiling()
	if ceiling <= 0 {
		t.Errorf("expected positive ceiling, got %d", ceiling)
	}
}

func TestMemoryMonitor_GlobalUsagePercent(t *testing.T) {
	config := DefaultMemoryMonitorConfig()
	config.MonitorInterval = 100 * time.Millisecond
	monitor := NewMemoryMonitor(config)
	defer monitor.Close()

	// With zero usage, should be 0%
	percent := monitor.GlobalUsagePercent()
	if percent != 0 {
		t.Errorf("expected 0%%, got %f", percent)
	}
}

func TestMemoryMonitor_InitialPauseState(t *testing.T) {
	config := DefaultMemoryMonitorConfig()
	config.MonitorInterval = 100 * time.Millisecond
	monitor := NewMemoryMonitor(config)
	defer monitor.Close()

	if monitor.PipelinesPaused() {
		t.Error("expected pipelines not paused initially")
	}
	if monitor.AllPaused() {
		t.Error("expected all not paused initially")
	}
	if monitor.PressureLevel() != MemoryPressureNone {
		t.Errorf("expected pressure level None, got %d", monitor.PressureLevel())
	}
}

// =============================================================================
// MemoryMonitor Tests - Stats
// =============================================================================

func TestMemoryMonitor_Stats(t *testing.T) {
	config := DefaultMemoryMonitorConfig()
	config.MonitorInterval = 100 * time.Millisecond
	monitor := NewMemoryMonitor(config)
	defer monitor.Close()

	monitor.AddUsage(ComponentQueryCache, 100)
	monitor.AddUsage(ComponentStaging, 200)

	stats := monitor.Stats()

	if stats.GlobalUsage != 300 {
		t.Errorf("expected global usage 300, got %d", stats.GlobalUsage)
	}
	if stats.GlobalCeiling <= 0 {
		t.Errorf("expected positive ceiling, got %d", stats.GlobalCeiling)
	}
	if stats.SystemMemory <= 0 {
		t.Errorf("expected positive system memory, got %d", stats.SystemMemory)
	}
	if len(stats.Components) != 4 {
		t.Errorf("expected 4 components, got %d", len(stats.Components))
	}

	queryCacheStats := stats.Components[ComponentQueryCache]
	if queryCacheStats.Current != 100 {
		t.Errorf("expected query cache current 100, got %d", queryCacheStats.Current)
	}
}

// =============================================================================
// MemoryMonitor Tests - Component Budget Enforcement
// =============================================================================

func TestMemoryMonitor_ComponentBudgetEnforcement_70Percent(t *testing.T) {
	publisher := newMockSignalPublisher()
	config := DefaultMemoryMonitorConfig()
	config.SignalPublisher = publisher
	config.MonitorInterval = 50 * time.Millisecond
	config.QueryCacheBudget = 1000 // Small budget for testing
	config.ComponentLRUThreshold = 0.70

	monitor := NewMemoryMonitor(config)
	defer monitor.Close()

	// Add 75% of budget
	monitor.AddUsage(ComponentQueryCache, 750)

	// Wait for monitor loop to detect
	time.Sleep(150 * time.Millisecond)

	if !publisher.hasSignal(SignalComponentLRU) {
		t.Error("expected SignalComponentLRU to be published at 70% threshold")
	}
}

func TestMemoryMonitor_ComponentBudgetEnforcement_90Percent(t *testing.T) {
	publisher := newMockSignalPublisher()
	config := DefaultMemoryMonitorConfig()
	config.SignalPublisher = publisher
	config.MonitorInterval = 50 * time.Millisecond
	config.QueryCacheBudget = 1000
	config.ComponentWarnThreshold = 0.90

	monitor := NewMemoryMonitor(config)
	defer monitor.Close()

	// Add 95% of budget
	monitor.AddUsage(ComponentQueryCache, 950)

	time.Sleep(150 * time.Millisecond)

	if !publisher.hasSignal(SignalComponentAggressive) {
		t.Error("expected SignalComponentAggressive to be published at 90% threshold")
	}
}

// =============================================================================
// MemoryMonitor Tests - Global Ceiling Enforcement
// =============================================================================

func TestMemoryMonitor_GlobalCeilingEnforcement_80Percent(t *testing.T) {
	publisher := newMockSignalPublisher()
	config := DefaultMemoryMonitorConfig()
	config.SignalPublisher = publisher
	config.MonitorInterval = 50 * time.Millisecond
	config.GlobalPauseThreshold = 0.80
	config.GlobalEmergencyThreshold = 0.95

	monitor := NewMemoryMonitor(config)
	defer monitor.Close()

	// Get ceiling and add 85% of it
	ceiling := monitor.GlobalCeiling()
	usageFor85Percent := int64(float64(ceiling) * 0.85)

	// Distribute across components
	perComponent := usageFor85Percent / 4
	monitor.AddUsage(ComponentQueryCache, perComponent)
	monitor.AddUsage(ComponentStaging, perComponent)
	monitor.AddUsage(ComponentAgentContext, perComponent)
	monitor.AddUsage(ComponentWAL, perComponent)

	time.Sleep(150 * time.Millisecond)

	if !publisher.hasSignal(SignalGlobalPause) {
		t.Error("expected SignalGlobalPause to be published at 80% global threshold")
	}
	if !monitor.PipelinesPaused() {
		t.Error("expected pipelines to be paused")
	}
	if monitor.AllPaused() {
		t.Error("expected all NOT to be paused at 80%")
	}
}

func TestMemoryMonitor_GlobalCeilingEnforcement_95Percent(t *testing.T) {
	publisher := newMockSignalPublisher()
	config := DefaultMemoryMonitorConfig()
	config.SignalPublisher = publisher
	config.MonitorInterval = 50 * time.Millisecond
	config.GlobalPauseThreshold = 0.80
	config.GlobalEmergencyThreshold = 0.95

	monitor := NewMemoryMonitor(config)
	defer monitor.Close()

	// Get ceiling and add 98% of it
	ceiling := monitor.GlobalCeiling()
	usageFor98Percent := int64(float64(ceiling) * 0.98)

	// Distribute across components
	perComponent := usageFor98Percent / 4
	monitor.AddUsage(ComponentQueryCache, perComponent)
	monitor.AddUsage(ComponentStaging, perComponent)
	monitor.AddUsage(ComponentAgentContext, perComponent)
	monitor.AddUsage(ComponentWAL, perComponent)

	time.Sleep(150 * time.Millisecond)

	if !publisher.hasSignal(SignalGlobalEmergency) {
		t.Error("expected SignalGlobalEmergency to be published at 95% global threshold")
	}
	if !monitor.PipelinesPaused() {
		t.Error("expected pipelines to be paused")
	}
	if !monitor.AllPaused() {
		t.Error("expected all to be paused at 95%")
	}
	if monitor.PressureLevel() != MemoryPressureEmergency {
		t.Errorf("expected pressure level Emergency, got %d", monitor.PressureLevel())
	}
}

// =============================================================================
// MemoryMonitor Tests - Resume Signal
// =============================================================================

func TestMemoryMonitor_GlobalResume(t *testing.T) {
	publisher := newMockSignalPublisher()
	config := DefaultMemoryMonitorConfig()
	config.SignalPublisher = publisher
	config.MonitorInterval = 50 * time.Millisecond
	config.GlobalPauseThreshold = 0.80

	monitor := NewMemoryMonitor(config)
	defer monitor.Close()

	// Get ceiling and add 85% of it to trigger pause
	ceiling := monitor.GlobalCeiling()
	usageFor85Percent := int64(float64(ceiling) * 0.85)

	perComponent := usageFor85Percent / 4
	monitor.AddUsage(ComponentQueryCache, perComponent)
	monitor.AddUsage(ComponentStaging, perComponent)
	monitor.AddUsage(ComponentAgentContext, perComponent)
	monitor.AddUsage(ComponentWAL, perComponent)

	time.Sleep(150 * time.Millisecond)

	// Verify paused
	if !monitor.PipelinesPaused() {
		t.Fatal("expected pipelines to be paused first")
	}

	publisher.clear()

	// Remove most usage to go below threshold
	monitor.RemoveUsage(ComponentQueryCache, perComponent)
	monitor.RemoveUsage(ComponentStaging, perComponent)
	monitor.RemoveUsage(ComponentAgentContext, perComponent)
	monitor.RemoveUsage(ComponentWAL, perComponent/2)

	time.Sleep(150 * time.Millisecond)

	if !publisher.hasSignal(SignalGlobalResume) {
		t.Error("expected SignalGlobalResume to be published when below threshold")
	}
	if monitor.PipelinesPaused() {
		t.Error("expected pipelines to be resumed")
	}
}

// =============================================================================
// MemoryMonitor Tests - Edge Cases
// =============================================================================

func TestMemoryMonitor_ZeroUsage(t *testing.T) {
	config := DefaultMemoryMonitorConfig()
	config.MonitorInterval = 50 * time.Millisecond
	monitor := NewMemoryMonitor(config)
	defer monitor.Close()

	// Zero usage should be fine
	if monitor.GlobalUsage() != 0 {
		t.Errorf("expected 0 global usage, got %d", monitor.GlobalUsage())
	}

	percent := monitor.GlobalUsagePercent()
	if percent != 0 {
		t.Errorf("expected 0%% usage, got %f", percent)
	}
}

func TestMemoryMonitor_MaxUsage(t *testing.T) {
	config := DefaultMemoryMonitorConfig()
	config.MonitorInterval = 100 * time.Millisecond
	monitor := NewMemoryMonitor(config)
	defer monitor.Close()

	// Add maximum possible usage
	maxBytes := int64(1 << 40) // 1TB
	monitor.AddUsage(ComponentQueryCache, maxBytes)

	if monitor.GlobalUsage() != maxBytes {
		t.Errorf("expected %d global usage, got %d", maxBytes, monitor.GlobalUsage())
	}
}

// =============================================================================
// MemoryMonitor Tests - Thread Safety
// =============================================================================

func TestMemoryMonitor_ConcurrentUsageUpdates(t *testing.T) {
	config := DefaultMemoryMonitorConfig()
	config.MonitorInterval = 50 * time.Millisecond
	monitor := NewMemoryMonitor(config)
	defer monitor.Close()

	var wg sync.WaitGroup
	iterations := 1000

	// Concurrent adds
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				monitor.AddUsage(ComponentQueryCache, 1)
			}
		}()
	}

	wg.Wait()

	expected := int64(10 * iterations)
	actual := monitor.GetComponent(ComponentQueryCache).Current()
	if actual != expected {
		t.Errorf("expected %d, got %d (race condition detected)", expected, actual)
	}
}

func TestMemoryMonitor_ConcurrentAddRemove(t *testing.T) {
	config := DefaultMemoryMonitorConfig()
	config.MonitorInterval = 50 * time.Millisecond
	monitor := NewMemoryMonitor(config)
	defer monitor.Close()

	var wg sync.WaitGroup
	iterations := 1000

	// Concurrent adds
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			monitor.AddUsage(ComponentStaging, 10)
		}
	}()

	// Concurrent removes (may clamp to 0)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			monitor.RemoveUsage(ComponentStaging, 5)
		}
	}()

	wg.Wait()

	// Should not panic and value should be >= 0
	current := monitor.GetComponent(ComponentStaging).Current()
	if current < 0 {
		t.Errorf("expected non-negative usage, got %d", current)
	}
}

func TestMemoryMonitor_ConcurrentStats(t *testing.T) {
	config := DefaultMemoryMonitorConfig()
	config.MonitorInterval = 50 * time.Millisecond
	monitor := NewMemoryMonitor(config)
	defer monitor.Close()

	var wg sync.WaitGroup

	// Concurrent usage updates
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			monitor.AddUsage(ComponentQueryCache, 1)
		}
	}()

	// Concurrent stats reads
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = monitor.Stats()
		}
	}()

	// Concurrent global usage reads
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = monitor.GlobalUsage()
			_ = monitor.GlobalUsagePercent()
		}
	}()

	wg.Wait()
	// Should complete without races
}

// =============================================================================
// MemoryMonitor Tests - Background Monitoring
// =============================================================================

func TestMemoryMonitor_BackgroundMonitoring(t *testing.T) {
	publisher := newMockSignalPublisher()
	config := DefaultMemoryMonitorConfig()
	config.SignalPublisher = publisher
	config.MonitorInterval = 30 * time.Millisecond
	config.QueryCacheBudget = 1000
	config.ComponentLRUThreshold = 0.70

	monitor := NewMemoryMonitor(config)
	defer monitor.Close()

	// Add usage above threshold
	monitor.AddUsage(ComponentQueryCache, 800) // 80% of 1000

	// Wait for at least 2 monitor cycles
	time.Sleep(100 * time.Millisecond)

	signals := publisher.getSignals()
	if len(signals) == 0 {
		t.Error("expected background monitoring to detect threshold crossing")
	}
}

func TestMemoryMonitor_Close(t *testing.T) {
	config := DefaultMemoryMonitorConfig()
	config.MonitorInterval = 10 * time.Millisecond
	monitor := NewMemoryMonitor(config)

	// Close should complete without hanging
	done := make(chan struct{})
	go func() {
		monitor.Close()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(1 * time.Second):
		t.Error("Close() timed out")
	}
}

// =============================================================================
// MemoryMonitor Tests - Signal Payload
// =============================================================================

func TestMemoryMonitor_SignalPayloadContent(t *testing.T) {
	publisher := newMockSignalPublisher()
	config := DefaultMemoryMonitorConfig()
	config.SignalPublisher = publisher
	config.MonitorInterval = 50 * time.Millisecond
	config.QueryCacheBudget = 1000
	config.ComponentWarnThreshold = 0.90

	monitor := NewMemoryMonitor(config)
	defer monitor.Close()

	// Trigger aggressive threshold
	monitor.AddUsage(ComponentQueryCache, 950)

	time.Sleep(150 * time.Millisecond)

	payloads := publisher.getPayloads()
	if len(payloads) == 0 {
		t.Fatal("expected at least one payload")
	}

	// Find the component aggressive payload
	var found bool
	for _, p := range payloads {
		if p.Component == ComponentQueryCache && p.Level == MemoryPressureHigh {
			found = true
			if p.CurrentBytes != 950 {
				t.Errorf("expected CurrentBytes 950, got %d", p.CurrentBytes)
			}
			if p.BudgetBytes != 1000 {
				t.Errorf("expected BudgetBytes 1000, got %d", p.BudgetBytes)
			}
			if p.Timestamp.IsZero() {
				t.Error("expected non-zero timestamp")
			}
			break
		}
	}

	if !found {
		t.Error("expected to find component aggressive payload")
	}
}

// =============================================================================
// NoOpSignalPublisher Tests
// =============================================================================

func TestNoOpSignalPublisher(t *testing.T) {
	publisher := &NoOpSignalPublisher{}
	err := publisher.PublishMemorySignal(SignalComponentLRU, MemorySignalPayload{})
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

// =============================================================================
// Constants Tests
// =============================================================================

func TestMemoryPressureLevelConstants(t *testing.T) {
	// Ensure constants have expected values
	if MemoryPressureNone != 0 {
		t.Error("MemoryPressureNone should be 0")
	}
	if MemoryPressureLow != 1 {
		t.Error("MemoryPressureLow should be 1")
	}
	if MemoryPressureHigh != 2 {
		t.Error("MemoryPressureHigh should be 2")
	}
	if MemoryPressureCritical != 3 {
		t.Error("MemoryPressureCritical should be 3")
	}
	if MemoryPressureEmergency != 4 {
		t.Error("MemoryPressureEmergency should be 4")
	}
}

func TestComponentNameConstants(t *testing.T) {
	if ComponentQueryCache != "query_cache" {
		t.Errorf("unexpected ComponentQueryCache value: %s", ComponentQueryCache)
	}
	if ComponentStaging != "staging" {
		t.Errorf("unexpected ComponentStaging value: %s", ComponentStaging)
	}
	if ComponentAgentContext != "agent_context" {
		t.Errorf("unexpected ComponentAgentContext value: %s", ComponentAgentContext)
	}
	if ComponentWAL != "wal" {
		t.Errorf("unexpected ComponentWAL value: %s", ComponentWAL)
	}
}

func TestMemorySignalConstants(t *testing.T) {
	if SignalComponentLRU != "memory.component.lru" {
		t.Errorf("unexpected SignalComponentLRU: %s", SignalComponentLRU)
	}
	if SignalComponentAggressive != "memory.component.aggressive" {
		t.Errorf("unexpected SignalComponentAggressive: %s", SignalComponentAggressive)
	}
	if SignalGlobalPause != "memory.global.pause" {
		t.Errorf("unexpected SignalGlobalPause: %s", SignalGlobalPause)
	}
	if SignalGlobalEmergency != "memory.global.emergency" {
		t.Errorf("unexpected SignalGlobalEmergency: %s", SignalGlobalEmergency)
	}
	if SignalGlobalResume != "memory.global.resume" {
		t.Errorf("unexpected SignalGlobalResume: %s", SignalGlobalResume)
	}
}

// =============================================================================
// Default Config Tests
// =============================================================================

func TestDefaultMemoryMonitorConfig(t *testing.T) {
	config := DefaultMemoryMonitorConfig()

	if config.QueryCacheBudget != DefaultQueryCacheBudget {
		t.Errorf("expected QueryCacheBudget %d, got %d", DefaultQueryCacheBudget, config.QueryCacheBudget)
	}
	if config.StagingBudget != DefaultStagingBudget {
		t.Errorf("expected StagingBudget %d, got %d", DefaultStagingBudget, config.StagingBudget)
	}
	if config.AgentContextBudget != DefaultAgentContextBudget {
		t.Errorf("expected AgentContextBudget %d, got %d", DefaultAgentContextBudget, config.AgentContextBudget)
	}
	if config.WALBudget != DefaultWALBudget {
		t.Errorf("expected WALBudget %d, got %d", DefaultWALBudget, config.WALBudget)
	}
	if config.GlobalCeilingPercent != DefaultGlobalCeilingPercent {
		t.Errorf("expected GlobalCeilingPercent %f, got %f", DefaultGlobalCeilingPercent, config.GlobalCeilingPercent)
	}
	if config.ComponentLRUThreshold != DefaultComponentLRUThreshold {
		t.Errorf("expected ComponentLRUThreshold %f, got %f", DefaultComponentLRUThreshold, config.ComponentLRUThreshold)
	}
	if config.ComponentWarnThreshold != DefaultComponentWarnThreshold {
		t.Errorf("expected ComponentWarnThreshold %f, got %f", DefaultComponentWarnThreshold, config.ComponentWarnThreshold)
	}
	if config.GlobalPauseThreshold != DefaultGlobalPauseThreshold {
		t.Errorf("expected GlobalPauseThreshold %f, got %f", DefaultGlobalPauseThreshold, config.GlobalPauseThreshold)
	}
	if config.GlobalEmergencyThreshold != DefaultGlobalEmergencyThreshold {
		t.Errorf("expected GlobalEmergencyThreshold %f, got %f", DefaultGlobalEmergencyThreshold, config.GlobalEmergencyThreshold)
	}
	if config.MonitorInterval != DefaultMonitorInterval {
		t.Errorf("expected MonitorInterval %v, got %v", DefaultMonitorInterval, config.MonitorInterval)
	}
	if config.SignalPublisher == nil {
		t.Error("expected non-nil SignalPublisher")
	}
}

// =============================================================================
// Multiple Components Pressure Test
// =============================================================================

func TestMemoryMonitor_MultipleComponentsAboveThreshold(t *testing.T) {
	publisher := newMockSignalPublisher()
	config := DefaultMemoryMonitorConfig()
	config.SignalPublisher = publisher
	config.MonitorInterval = 50 * time.Millisecond
	config.QueryCacheBudget = 1000
	config.StagingBudget = 1000
	config.ComponentLRUThreshold = 0.70

	monitor := NewMemoryMonitor(config)
	defer monitor.Close()

	// Put multiple components above threshold
	monitor.AddUsage(ComponentQueryCache, 800) // 80%
	monitor.AddUsage(ComponentStaging, 750)    // 75%

	time.Sleep(150 * time.Millisecond)

	signals := publisher.getSignals()
	lruCount := 0
	for _, s := range signals {
		if s == SignalComponentLRU {
			lruCount++
		}
	}

	// Should have signals for both components
	if lruCount < 2 {
		t.Errorf("expected at least 2 LRU signals for two components, got %d", lruCount)
	}
}

// =============================================================================
// Rapid Usage Changes Test
// =============================================================================

func TestMemoryMonitor_RapidUsageChanges(t *testing.T) {
	config := DefaultMemoryMonitorConfig()
	config.MonitorInterval = 50 * time.Millisecond
	monitor := NewMemoryMonitor(config)
	defer monitor.Close()

	// Rapidly add and remove
	for i := 0; i < 100; i++ {
		monitor.AddUsage(ComponentQueryCache, 100)
		monitor.RemoveUsage(ComponentQueryCache, 50)
	}

	// Final state should be 100 * 50 = 5000
	expected := int64(5000)
	actual := monitor.GetComponent(ComponentQueryCache).Current()
	if actual != expected {
		t.Errorf("expected %d, got %d", expected, actual)
	}
}

// =============================================================================
// Atomic Operations Test
// =============================================================================

func TestMemoryMonitor_AtomicPauseFlags(t *testing.T) {
	config := DefaultMemoryMonitorConfig()
	config.MonitorInterval = 100 * time.Millisecond
	monitor := NewMemoryMonitor(config)
	defer monitor.Close()

	var readCount atomic.Int64
	var wg sync.WaitGroup

	// Concurrent reads of pause flags
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = monitor.PipelinesPaused()
				_ = monitor.AllPaused()
				_ = monitor.PressureLevel()
				readCount.Add(1)
			}
		}()
	}

	wg.Wait()

	if readCount.Load() != 10000 {
		t.Errorf("expected 10000 reads, got %d", readCount.Load())
	}
}

// =============================================================================
// W12.9 - MemoryMonitor Goroutine Tracking Tests
// =============================================================================

// TestMemoryMonitor_GoroutineTracking verifies the monitoring goroutine is tracked.
func TestMemoryMonitor_GoroutineTracking(t *testing.T) {
	config := DefaultMemoryMonitorConfig()
	config.MonitorInterval = 10 * time.Millisecond

	monitor := NewMemoryMonitor(config)

	// Do some work
	monitor.AddUsage(ComponentQueryCache, 100)
	time.Sleep(50 * time.Millisecond)

	// Close should not hang
	done := make(chan struct{})
	go func() {
		monitor.Close()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(1 * time.Second):
		t.Fatal("Close() timed out - goroutine tracking issue")
	}
}

// TestMemoryMonitor_MultipleCloseDoesNotPanic verifies double close is safe.
func TestMemoryMonitor_MultipleCloseDoesNotPanic(t *testing.T) {
	config := DefaultMemoryMonitorConfig()
	config.MonitorInterval = 10 * time.Millisecond

	monitor := NewMemoryMonitor(config)

	// First close should succeed
	err := monitor.Close()
	if err != nil {
		t.Errorf("first close should succeed, got %v", err)
	}

	// Second close should not panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Close() panicked on double call: %v", r)
		}
	}()

	err = monitor.Close()
	if err != nil {
		t.Errorf("second close should succeed (idempotent), got %v", err)
	}
}

// TestMemoryMonitor_RapidCreateClose verifies no goroutine leaks on rapid create/close.
func TestMemoryMonitor_RapidCreateClose(t *testing.T) {
	for i := 0; i < 100; i++ {
		config := DefaultMemoryMonitorConfig()
		config.MonitorInterval = 10 * time.Millisecond
		monitor := NewMemoryMonitor(config)

		// Small amount of work
		monitor.AddUsage(ComponentQueryCache, 10)

		// Close
		monitor.Close()
	}
	// If goroutines leak, we'd see increased memory or goroutine count
	// This test mainly checks for panics
}

// TestMemoryMonitor_ConcurrentCloseNoPanic verifies concurrent close calls don't panic.
func TestMemoryMonitor_ConcurrentCloseNoPanic(t *testing.T) {
	for iteration := 0; iteration < 50; iteration++ {
		config := DefaultMemoryMonitorConfig()
		config.MonitorInterval = 10 * time.Millisecond
		monitor := NewMemoryMonitor(config)

		var wg sync.WaitGroup
		const numClosers = 5

		// Launch concurrent closers
		for i := 0; i < numClosers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("iteration %d: Close() panicked: %v", iteration, r)
					}
				}()
				monitor.Close()
			}()
		}

		wg.Wait()
	}
}

// TestMemoryMonitor_CloseInterruptsMonitoring verifies Close stops monitoring.
func TestMemoryMonitor_CloseInterruptsMonitoring(t *testing.T) {
	publisher := newMockSignalPublisher()
	config := DefaultMemoryMonitorConfig()
	config.SignalPublisher = publisher
	config.MonitorInterval = 20 * time.Millisecond
	config.QueryCacheBudget = 1000
	config.ComponentLRUThreshold = 0.70

	monitor := NewMemoryMonitor(config)

	// Add usage above threshold
	monitor.AddUsage(ComponentQueryCache, 800)

	// Wait for some signals
	time.Sleep(100 * time.Millisecond)
	initialSignals := len(publisher.getSignals())

	// Close the monitor
	monitor.Close()

	// Clear signals and wait
	publisher.clear()
	time.Sleep(100 * time.Millisecond)

	// No new signals should be emitted
	newSignals := len(publisher.getSignals())
	if newSignals > 0 {
		t.Errorf("expected 0 signals after close, got %d", newSignals)
	}

	// Sanity check that we did get signals before close
	if initialSignals == 0 {
		t.Error("expected some signals before close")
	}
}
