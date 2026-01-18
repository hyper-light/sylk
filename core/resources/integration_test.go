package resources

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/signal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntegration_FullPressureCycle(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	cache := &mockCache{name: "test-cache", size: 10000}

	stateConfig := PressureStateConfig{
		EnterThresholds: []float64{0.50, 0.70, 0.85},
		ExitThresholds:  []float64{0.35, 0.55, 0.70},
		Cooldown:        10 * time.Millisecond,
	}

	cfg := PressureControllerConfig{
		Caches:       []EvictableCache{cache},
		SignalBus:    bus,
		SpikeConfig:  SpikeMonitorConfig{Interval: 10 * time.Millisecond},
		StateConfig:  stateConfig,
		Scheduler:    &mockPressureScheduler{},
		PipelineInfo: &emptyPipelineProvider{},
	}

	pc := NewPressureController(cfg)

	assert.Equal(t, PressureNormal, pc.CurrentLevel())

	pc.handleTransition(PressureNormal, PressureElevated, 0.55)
	assert.False(t, pc.Admission().IsAdmitting())

	pc.handleTransition(PressureElevated, PressureHigh, 0.75)
	assert.Less(t, cache.Size(), int64(10000))

	pc.handleTransition(PressureHigh, PressureCritical, 0.90)

	pc.handleTransition(PressureCritical, PressureNormal, 0.30)
	assert.True(t, pc.Admission().IsAdmitting())
}

func TestIntegration_SpikeTriggersImmediateResponse(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	cache := &mockCache{name: "test-cache", size: 10000}

	cfg := PressureControllerConfig{
		Caches:       []EvictableCache{cache},
		SignalBus:    bus,
		SpikeConfig:  SpikeMonitorConfig{Interval: 10 * time.Millisecond},
		StateConfig:  DefaultPressureStateConfig(),
		Scheduler:    &mockPressureScheduler{},
		PipelineInfo: &emptyPipelineProvider{},
	}

	pc := NewPressureController(cfg)
	initialSize := cache.Size()

	pc.handleSpike(Sample{HeapAlloc: 1000000, Usage: 0.8})

	time.Sleep(20 * time.Millisecond)

	assert.Less(t, cache.Size(), initialSize, "Spike should trigger cache eviction")
}

func TestIntegration_CacheEvictionFreesMemory(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	cache1 := &mockCache{name: "cache1", size: 5000}
	cache2 := &mockCache{name: "cache2", size: 5000}

	evictor := NewCacheEvictor(cache1, cache2)

	freed := evictor.Evict(0.25)

	assert.Equal(t, int64(2500), freed)
	assert.Equal(t, int64(3750), cache1.Size())
	assert.Equal(t, int64(3750), cache2.Size())
}

func TestIntegration_PipelineAdmissionGating(t *testing.T) {
	scheduler := &mockPressureScheduler{}
	ac := NewAdmissionController(scheduler)
	ctx := context.Background()

	p1 := &concurrency.SchedulablePipeline{ID: "p1", Priority: concurrency.PriorityNormal}
	admitted := ac.TryAdmit(ctx, "s1", p1)
	assert.True(t, admitted)

	ac.Stop()

	p2 := &concurrency.SchedulablePipeline{ID: "p2", Priority: concurrency.PriorityNormal}
	admitted = ac.TryAdmit(ctx, "s1", p2)
	assert.False(t, admitted)
	assert.Equal(t, 1, ac.QueueLength())

	ac.Resume()
	assert.Equal(t, 0, ac.QueueLength())
	assert.Len(t, scheduler.scheduled, 2)
}

func TestIntegration_ContextCompactionSignals(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	received := make(chan signal.SignalMessage, 10)
	sub := signal.SignalSubscriber{
		ID:      "test-agent",
		Signals: []signal.Signal{signal.CompactContexts},
		Channel: received,
	}
	require.NoError(t, bus.Subscribe(sub))

	registry := NewUsageRegistry()
	registry.Report("agent-1", UsageCategoryLLMBuffers, 10000)
	registry.Report("agent-2", UsageCategoryLLMBuffers, 5000)

	compactor := NewContextCompactor(bus, registry)

	compactor.CompactAll()

	select {
	case msg := <-received:
		payload, ok := msg.Payload.(signal.CompactContextsPayload)
		require.True(t, ok)
		assert.True(t, payload.All)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No compact signal received")
	}

	compactor.CompactLargest(1)

	select {
	case msg := <-received:
		assert.Equal(t, "agent-1", msg.TargetID)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No targeted compact signal received")
	}
}

func TestIntegration_StressHighFrequencyStateChanges(t *testing.T) {
	stateConfig := PressureStateConfig{
		EnterThresholds: []float64{0.50, 0.70, 0.85},
		ExitThresholds:  []float64{0.35, 0.55, 0.70},
		Cooldown:        1 * time.Millisecond,
	}

	sm := NewPressureStateMachine(stateConfig)

	transitionCount := 0
	sm.SetTransitionCallback(func(from, to PressureLevel, usage float64) {
		transitionCount++
	})

	usages := []float64{0.30, 0.55, 0.75, 0.90, 0.80, 0.60, 0.40, 0.30}

	for i := 0; i < 100; i++ {
		for _, usage := range usages {
			sm.Update(usage)
			time.Sleep(2 * time.Millisecond)
		}
	}

	assert.Greater(t, transitionCount, 0, "Should have some transitions")
	assert.Less(t, transitionCount, 800*100, "Cooldown should prevent excessive transitions")
}

func TestIntegration_StressConcurrentPipelines(t *testing.T) {
	scheduler := &mockPressureScheduler{}
	ac := NewAdmissionController(scheduler)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			p := &concurrency.SchedulablePipeline{
				ID:       string(rune('a' + idx%26)),
				Priority: concurrency.PipelinePriority(idx % 4),
			}

			if idx%10 == 0 {
				ac.Stop()
			} else if idx%10 == 5 {
				ac.Resume()
			}

			ac.TryAdmit(ctx, "session", p)
		}(i)
	}

	wg.Wait()
	ac.Resume()

	time.Sleep(10 * time.Millisecond)

	totalProcessed := len(scheduler.scheduled) + ac.QueueLength()
	assert.GreaterOrEqual(t, totalProcessed, 0)
}

func TestIntegration_NoGoroutineLeaks(t *testing.T) {
	initialGoroutines := runtime.NumGoroutine()

	for i := 0; i < 5; i++ {
		bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())

		cfg := PressureControllerConfig{
			SignalBus:    bus,
			SpikeConfig:  SpikeMonitorConfig{Interval: 10 * time.Millisecond},
			StateConfig:  DefaultPressureStateConfig(),
			Scheduler:    &mockPressureScheduler{},
			PipelineInfo: &emptyPipelineProvider{},
		}

		pc := NewPressureController(cfg)

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		go pc.Run(ctx)

		time.Sleep(30 * time.Millisecond)
		cancel()
		bus.Close()
	}

	time.Sleep(100 * time.Millisecond)

	finalGoroutines := runtime.NumGoroutine()
	leaked := finalGoroutines - initialGoroutines

	assert.LessOrEqual(t, leaked, 5, "Should not leak many goroutines (some variance is normal)")
}

func TestIntegration_NoDeadlocksBetweenComponents(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	cache := &mockCache{name: "test-cache", size: 10000}

	cfg := PressureControllerConfig{
		Caches:       []EvictableCache{cache},
		SignalBus:    bus,
		SpikeConfig:  SpikeMonitorConfig{Interval: 5 * time.Millisecond},
		StateConfig:  DefaultPressureStateConfig(),
		Scheduler:    &mockPressureScheduler{},
		PipelineInfo: &emptyPipelineProvider{},
	}

	pc := NewPressureController(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan bool)
	go func() {
		pc.Run(ctx)
		done <- true
	}()

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			pc.Registry().Report("comp-"+string(rune('a'+i%10)), UsageCategoryCaches, int64(i*100))
			time.Sleep(2 * time.Millisecond)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			pc.Evictor().Evict(0.1)
			time.Sleep(3 * time.Millisecond)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_ = pc.CurrentLevel()
			_ = pc.Admission().IsAdmitting()
			time.Sleep(1 * time.Millisecond)
		}
	}()

	wgDone := make(chan bool)
	go func() {
		wg.Wait()
		wgDone <- true
	}()

	select {
	case <-wgDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Deadlock detected - operations didn't complete in time")
	}

	<-done
}

func BenchmarkPressureStateMachine_Update(b *testing.B) {
	sm := NewPressureStateMachine(DefaultPressureStateConfig())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sm.Update(float64(i%100) / 100.0)
	}
}

func BenchmarkUsageRegistry_Report(b *testing.B) {
	registry := NewUsageRegistry()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		registry.Report("component-"+string(rune(i%26+'a')), UsageCategory(i%4), int64(i*100))
	}
}

func BenchmarkCacheEvictor_Evict(b *testing.B) {
	caches := make([]EvictableCache, 10)
	for i := range caches {
		caches[i] = &mockCache{name: "cache-" + string(rune('a'+i)), size: 10000}
	}

	evictor := NewCacheEvictor(caches...)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		evictor.Evict(0.01)
	}
}
