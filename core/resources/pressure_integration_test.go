package resources

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/signal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTestIntegration(t *testing.T) *PressureIntegration {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	t.Cleanup(func() { bus.Close() })

	cfg := PressureIntegrationConfig{
		SignalBus:      bus,
		Scheduler:      &mockPressureScheduler{},
		PipelineInfo:   &emptyPipelineProvider{},
		Caches:         nil,
		MemoryLimitMB:  512,
		SpikeConfig:    SpikeMonitorConfig{Interval: 50 * time.Millisecond},
		PressureConfig: DefaultPressureStateConfig(),
	}

	return NewPressureIntegration(cfg)
}

func TestPressureIntegration_New(t *testing.T) {
	pi := createTestIntegration(t)

	assert.NotNil(t, pi.Controller())
	assert.NotNil(t, pi.Registry())
	assert.NotNil(t, pi.Admission())
}

func TestPressureIntegration_StartStop(t *testing.T) {
	pi := createTestIntegration(t)

	ctx := context.Background()
	pi.Start(ctx)

	assert.True(t, pi.IsAdmitting())

	pi.Stop()
}

func TestPressureIntegration_RegisterCache(t *testing.T) {
	pi := createTestIntegration(t)

	cache := &mockCache{name: "test-cache", size: 1000}
	pi.RegisterCache(cache)

	assert.Equal(t, 1, pi.Controller().Evictor().CacheCount())
}

func TestPressureIntegration_ReportUsage(t *testing.T) {
	pi := createTestIntegration(t)

	pi.ReportUsage("component-1", UsageCategoryCaches, 5000)

	assert.Equal(t, int64(5000), pi.Registry().ByCategory(UsageCategoryCaches))
}

func TestPressureIntegration_ReleaseUsage(t *testing.T) {
	pi := createTestIntegration(t)

	pi.ReportUsage("component-1", UsageCategoryCaches, 5000)
	pi.ReleaseUsage("component-1")

	assert.Equal(t, int64(0), pi.Registry().ByCategory(UsageCategoryCaches))
}

func TestPressureIntegration_CurrentLevel(t *testing.T) {
	pi := createTestIntegration(t)

	assert.Equal(t, PressureNormal, pi.CurrentLevel())
}

func TestPressureIntegration_WithCaches(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	cache1 := &mockCache{name: "cache1", size: 1000}
	cache2 := &mockCache{name: "cache2", size: 2000}

	cfg := PressureIntegrationConfig{
		SignalBus:      bus,
		Scheduler:      &mockPressureScheduler{},
		PipelineInfo:   &emptyPipelineProvider{},
		Caches:         []EvictableCache{cache1, cache2},
		MemoryLimitMB:  256,
		SpikeConfig:    SpikeMonitorConfig{Interval: 50 * time.Millisecond},
		PressureConfig: DefaultPressureStateConfig(),
	}

	pi := NewPressureIntegration(cfg)

	assert.Equal(t, 2, pi.Controller().Evictor().CacheCount())
	assert.Equal(t, int64(3000), pi.Controller().Evictor().TotalSize())
}

func TestPressureIntegration_DefaultConfigs(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	cfg := PressureIntegrationConfig{
		SignalBus:    bus,
		Scheduler:    &mockPressureScheduler{},
		PipelineInfo: &emptyPipelineProvider{},
	}

	pi := NewPressureIntegration(cfg)
	require.NotNil(t, pi)
}

func TestPressureIntegration_MultipleStartsIgnored(t *testing.T) {
	pi := createTestIntegration(t)

	ctx := context.Background()
	pi.Start(ctx)
	pi.Start(ctx)
	pi.Start(ctx)

	pi.Stop()
}

func TestPressureIntegration_MultipleStopsIgnored(t *testing.T) {
	pi := createTestIntegration(t)

	ctx := context.Background()
	pi.Start(ctx)

	pi.Stop()
	pi.Stop()
	pi.Stop()
}
