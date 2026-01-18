package resources

import (
	"context"

	"github.com/adalundhe/sylk/core/signal"
)

type PressureIntegrationConfig struct {
	SignalBus      *signal.SignalBus
	Scheduler      PipelineScheduler
	PipelineInfo   ActivePipelineProvider
	Caches         []EvictableCache
	MemoryLimitMB  int64
	SpikeConfig    SpikeMonitorConfig
	PressureConfig PressureStateConfig
}

type PressureIntegration struct {
	controller *PressureController
	running    bool
	cancel     context.CancelFunc
}

func NewPressureIntegration(cfg PressureIntegrationConfig) *PressureIntegration {
	memLimit := cfg.MemoryLimitMB * 1024 * 1024

	spikeConfig := cfg.SpikeConfig
	if spikeConfig.MemoryLimit == 0 {
		spikeConfig.MemoryLimit = memLimit
	}

	pressureConfig := cfg.PressureConfig
	if len(pressureConfig.EnterThresholds) == 0 {
		pressureConfig = DefaultPressureStateConfig()
	}

	controller := NewPressureController(PressureControllerConfig{
		MemoryLimit:  memLimit,
		Caches:       cfg.Caches,
		SignalBus:    cfg.SignalBus,
		SpikeConfig:  spikeConfig,
		StateConfig:  pressureConfig,
		Scheduler:    cfg.Scheduler,
		PipelineInfo: cfg.PipelineInfo,
	})

	return &PressureIntegration{
		controller: controller,
	}
}

func (pi *PressureIntegration) Start(ctx context.Context) {
	if pi.running {
		return
	}

	runCtx, cancel := context.WithCancel(ctx)
	pi.cancel = cancel
	pi.running = true

	go pi.controller.Run(runCtx)
}

func (pi *PressureIntegration) Stop() {
	if !pi.running {
		return
	}

	if pi.cancel != nil {
		pi.cancel()
	}
	pi.running = false
}

func (pi *PressureIntegration) Controller() *PressureController {
	return pi.controller
}

func (pi *PressureIntegration) Registry() *UsageRegistry {
	return pi.controller.Registry()
}

func (pi *PressureIntegration) Admission() *AdmissionController {
	return pi.controller.Admission()
}

func (pi *PressureIntegration) RegisterCache(cache EvictableCache) {
	pi.controller.Evictor().Register(cache)
}

func (pi *PressureIntegration) ReportUsage(id string, category UsageCategory, bytes int64) {
	pi.controller.Registry().Report(id, category, bytes)
}

func (pi *PressureIntegration) ReleaseUsage(id string) {
	pi.controller.Registry().Release(id)
}

func (pi *PressureIntegration) CurrentLevel() PressureLevel {
	return pi.controller.CurrentLevel()
}

func (pi *PressureIntegration) IsAdmitting() bool {
	return pi.controller.Admission().IsAdmitting()
}
