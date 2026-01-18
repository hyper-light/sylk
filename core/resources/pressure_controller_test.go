package resources

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/signal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockPressureScheduler struct {
	mu        sync.Mutex
	scheduled []*concurrency.SchedulablePipeline
}

func (m *mockPressureScheduler) Schedule(_ context.Context, _ string, p *concurrency.SchedulablePipeline) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scheduled = append(m.scheduled, p)
	return true, nil
}

type emptyPipelineProvider struct{}

func (e *emptyPipelineProvider) GetActiveSortedByPriority() []PipelineInfo {
	return nil
}

func createTestController(t *testing.T) (*PressureController, *signal.SignalBus) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	t.Cleanup(func() { bus.Close() })

	cfg := PressureControllerConfig{
		MemoryLimit:  0,
		Caches:       nil,
		SignalBus:    bus,
		SpikeConfig:  SpikeMonitorConfig{Interval: 50 * time.Millisecond, SampleCount: 5},
		StateConfig:  DefaultPressureStateConfig(),
		Scheduler:    &mockPressureScheduler{},
		PipelineInfo: &emptyPipelineProvider{},
	}

	pc := NewPressureController(cfg)
	return pc, bus
}

func TestPressureController_New(t *testing.T) {
	pc, _ := createTestController(t)

	assert.NotNil(t, pc.Registry())
	assert.NotNil(t, pc.Admission())
	assert.NotNil(t, pc.Evictor())
	assert.Equal(t, PressureNormal, pc.CurrentLevel())
}

func TestPressureController_RegistryAccessor(t *testing.T) {
	pc, _ := createTestController(t)

	registry := pc.Registry()
	require.NotNil(t, registry)

	registry.Report("test-component", UsageCategoryCaches, 1000)
	assert.Equal(t, int64(1000), registry.ByCategory(UsageCategoryCaches))
}

func TestPressureController_AdmissionAccessor(t *testing.T) {
	pc, _ := createTestController(t)

	admission := pc.Admission()
	require.NotNil(t, admission)

	assert.True(t, admission.IsAdmitting())
	admission.Stop()
	assert.False(t, admission.IsAdmitting())
}

func TestPressureController_Run_StartsAndStops(t *testing.T) {
	pc, _ := createTestController(t)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan bool)
	go func() {
		pc.Run(ctx)
		done <- true
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run should exit on context cancel")
	}
}

func TestPressureController_Run_PreventsDuplicateRuns(t *testing.T) {
	pc, _ := createTestController(t)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pc.Run(ctx)
		}()
	}

	wg.Wait()
}

func TestPressureController_EvictorWithCaches(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	cache := &mockCache{name: "test-cache", size: 1000}

	cfg := PressureControllerConfig{
		Caches:       []EvictableCache{cache},
		SignalBus:    bus,
		SpikeConfig:  SpikeMonitorConfig{Interval: 50 * time.Millisecond},
		StateConfig:  DefaultPressureStateConfig(),
		Scheduler:    &mockPressureScheduler{},
		PipelineInfo: &emptyPipelineProvider{},
	}

	pc := NewPressureController(cfg)

	assert.Equal(t, 1, pc.Evictor().CacheCount())
	assert.Equal(t, int64(1000), pc.Evictor().TotalSize())
}

func TestPressureController_ResponseMatrix_Normal(t *testing.T) {
	pc, _ := createTestController(t)

	pc.Admission().Stop()

	pc.handleTransition(PressureElevated, PressureNormal, 0.3)

	assert.True(t, pc.Admission().IsAdmitting())
}

func TestPressureController_ResponseMatrix_Elevated(t *testing.T) {
	pc, _ := createTestController(t)

	pc.handleTransition(PressureNormal, PressureElevated, 0.55)

	assert.False(t, pc.Admission().IsAdmitting())
}

func TestPressureController_ResponseMatrix_High(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	cache := &mockCache{name: "test-cache", size: 1000}

	cfg := PressureControllerConfig{
		Caches:       []EvictableCache{cache},
		SignalBus:    bus,
		SpikeConfig:  SpikeMonitorConfig{Interval: 50 * time.Millisecond},
		StateConfig:  DefaultPressureStateConfig(),
		Scheduler:    &mockPressureScheduler{},
		PipelineInfo: &emptyPipelineProvider{},
	}

	pc := NewPressureController(cfg)

	initialSize := cache.Size()

	pc.handleTransition(PressureElevated, PressureHigh, 0.75)

	assert.False(t, pc.Admission().IsAdmitting())
	assert.Less(t, cache.Size(), initialSize, "Cache should be evicted")
}

func TestPressureController_ResponseMatrix_Critical(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	cache := &mockCache{name: "test-cache", size: 1000}

	cfg := PressureControllerConfig{
		Caches:       []EvictableCache{cache},
		SignalBus:    bus,
		SpikeConfig:  SpikeMonitorConfig{Interval: 50 * time.Millisecond},
		StateConfig:  DefaultPressureStateConfig(),
		Scheduler:    &mockPressureScheduler{},
		PipelineInfo: &emptyPipelineProvider{},
	}

	pc := NewPressureController(cfg)

	initialSize := cache.Size()

	pc.handleTransition(PressureHigh, PressureCritical, 0.90)

	assert.False(t, pc.Admission().IsAdmitting())
	assert.LessOrEqual(t, cache.Size(), initialSize/2, "Cache should be 50% evicted")
}

func TestPressureController_HandleSpike(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	cache := &mockCache{name: "test-cache", size: 1000}

	cfg := PressureControllerConfig{
		Caches:       []EvictableCache{cache},
		SignalBus:    bus,
		SpikeConfig:  SpikeMonitorConfig{Interval: 50 * time.Millisecond},
		StateConfig:  DefaultPressureStateConfig(),
		Scheduler:    &mockPressureScheduler{},
		PipelineInfo: &emptyPipelineProvider{},
	}

	pc := NewPressureController(cfg)

	initialSize := cache.Size()

	pc.handleSpike(Sample{})

	time.Sleep(10 * time.Millisecond)

	assert.Less(t, cache.Size(), initialSize, "Cache should be evicted on spike")
}

type mockGoroutineBudget struct {
	mu    sync.Mutex
	level int32
}

func (m *mockGoroutineBudget) OnPressureChange(level int32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.level = level
}

func (m *mockGoroutineBudget) Level() int32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.level
}

func TestPressureController_GoroutineBudgetIntegration(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	budget := &mockGoroutineBudget{}

	cfg := PressureControllerConfig{
		SignalBus:       bus,
		SpikeConfig:     SpikeMonitorConfig{Interval: 50 * time.Millisecond},
		StateConfig:     DefaultPressureStateConfig(),
		Scheduler:       &mockPressureScheduler{},
		PipelineInfo:    &emptyPipelineProvider{},
		GoroutineBudget: budget,
	}

	pc := NewPressureController(cfg)

	pc.handleTransition(PressureNormal, PressureElevated, 0.55)
	assert.Equal(t, int32(PressureElevated), budget.Level())

	pc.handleTransition(PressureElevated, PressureHigh, 0.75)
	assert.Equal(t, int32(PressureHigh), budget.Level())

	pc.handleTransition(PressureHigh, PressureCritical, 0.90)
	assert.Equal(t, int32(PressureCritical), budget.Level())

	pc.handleTransition(PressureCritical, PressureNormal, 0.30)
	assert.Equal(t, int32(PressureNormal), budget.Level())
}

func TestPressureController_GoroutineBudgetNil(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	cfg := PressureControllerConfig{
		SignalBus:       bus,
		SpikeConfig:     SpikeMonitorConfig{Interval: 50 * time.Millisecond},
		StateConfig:     DefaultPressureStateConfig(),
		Scheduler:       &mockPressureScheduler{},
		PipelineInfo:    &emptyPipelineProvider{},
		GoroutineBudget: nil,
	}

	pc := NewPressureController(cfg)

	assert.NotPanics(t, func() {
		pc.handleTransition(PressureNormal, PressureElevated, 0.55)
	})
}

type mockFileHandleBudget struct {
	mu    sync.Mutex
	level int32
}

func (m *mockFileHandleBudget) SetPressureLevel(level int32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.level = level
}

func (m *mockFileHandleBudget) PressureLevel() int32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.level
}

func TestPressureController_FileHandleBudgetIntegration(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	budget := &mockFileHandleBudget{}

	cfg := PressureControllerConfig{
		SignalBus:        bus,
		SpikeConfig:      SpikeMonitorConfig{Interval: 50 * time.Millisecond},
		StateConfig:      DefaultPressureStateConfig(),
		Scheduler:        &mockPressureScheduler{},
		PipelineInfo:     &emptyPipelineProvider{},
		FileHandleBudget: budget,
	}

	pc := NewPressureController(cfg)

	pc.handleTransition(PressureNormal, PressureElevated, 0.55)
	assert.Equal(t, int32(PressureElevated), budget.PressureLevel())

	pc.handleTransition(PressureElevated, PressureHigh, 0.75)
	assert.Equal(t, int32(PressureHigh), budget.PressureLevel())

	pc.handleTransition(PressureHigh, PressureCritical, 0.90)
	assert.Equal(t, int32(PressureCritical), budget.PressureLevel())

	pc.handleTransition(PressureCritical, PressureNormal, 0.30)
	assert.Equal(t, int32(PressureNormal), budget.PressureLevel())
}

func TestPressureController_FileHandleBudgetNil(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	cfg := PressureControllerConfig{
		SignalBus:        bus,
		SpikeConfig:      SpikeMonitorConfig{Interval: 50 * time.Millisecond},
		StateConfig:      DefaultPressureStateConfig(),
		Scheduler:        &mockPressureScheduler{},
		PipelineInfo:     &emptyPipelineProvider{},
		FileHandleBudget: nil,
	}

	pc := NewPressureController(cfg)

	assert.NotPanics(t, func() {
		pc.handleTransition(PressureNormal, PressureElevated, 0.55)
	})
}

func TestPressureController_BothBudgetsIntegration(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	goroutineBudget := &mockGoroutineBudget{}
	fileHandleBudget := &mockFileHandleBudget{}

	cfg := PressureControllerConfig{
		SignalBus:        bus,
		SpikeConfig:      SpikeMonitorConfig{Interval: 50 * time.Millisecond},
		StateConfig:      DefaultPressureStateConfig(),
		Scheduler:        &mockPressureScheduler{},
		PipelineInfo:     &emptyPipelineProvider{},
		GoroutineBudget:  goroutineBudget,
		FileHandleBudget: fileHandleBudget,
	}

	pc := NewPressureController(cfg)

	pc.handleTransition(PressureNormal, PressureCritical, 0.90)

	assert.Equal(t, int32(PressureCritical), goroutineBudget.Level())
	assert.Equal(t, int32(PressureCritical), fileHandleBudget.PressureLevel())
}
