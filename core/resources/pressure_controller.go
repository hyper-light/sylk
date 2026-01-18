package resources

import (
	"context"
	"runtime"
	"runtime/debug"
	"sync"

	"github.com/adalundhe/sylk/core/signal"
)

type GoroutineBudgetIntegration interface {
	OnPressureChange(level int32)
}

type PressureControllerConfig struct {
	MemoryLimit     int64
	Caches          []EvictableCache
	SignalBus       *signal.SignalBus
	SpikeConfig     SpikeMonitorConfig
	StateConfig     PressureStateConfig
	Scheduler       PipelineScheduler
	PipelineInfo    ActivePipelineProvider
	GoroutineBudget GoroutineBudgetIntegration
}

type PressureController struct {
	registry        *UsageRegistry
	monitor         *SpikeMonitor
	stateMachine    *PressureStateMachine
	admission       *AdmissionController
	evictor         *CacheEvictor
	compactor       *ContextCompactor
	suspender       *PipelineSuspender
	signalBus       *signal.SignalBus
	goroutineBudget GoroutineBudgetIntegration

	mu      sync.RWMutex
	running bool
}

func NewPressureController(cfg PressureControllerConfig) *PressureController {
	registry := NewUsageRegistry()
	evictor := NewCacheEvictor(cfg.Caches...)

	pc := &PressureController{
		registry:        registry,
		monitor:         NewSpikeMonitor(cfg.SpikeConfig),
		stateMachine:    NewPressureStateMachine(cfg.StateConfig),
		admission:       NewAdmissionController(cfg.Scheduler),
		evictor:         evictor,
		compactor:       NewContextCompactor(cfg.SignalBus, registry),
		suspender:       NewPipelineSuspender(cfg.PipelineInfo, cfg.SignalBus),
		signalBus:       cfg.SignalBus,
		goroutineBudget: cfg.GoroutineBudget,
	}

	pc.wireCallbacks()
	pc.configureMemoryLimit(cfg.MemoryLimit)

	return pc
}

func (pc *PressureController) wireCallbacks() {
	pc.monitor.SetOnSample(pc.handleSample)
	pc.monitor.SetOnSpike(pc.handleSpike)
	pc.stateMachine.SetTransitionCallback(pc.handleTransition)
}

func (pc *PressureController) configureMemoryLimit(limit int64) {
	if limit > 0 {
		debug.SetMemoryLimit(limit)
	}
}

func (pc *PressureController) handleSample(sample Sample) {
	pc.stateMachine.Update(sample.Usage)
}

func (pc *PressureController) handleSpike(_ Sample) {
	go pc.triggerGC()
	pc.evictor.Evict(0.25)
	pc.stateMachine.BypassCooldown()
}

func (pc *PressureController) handleTransition(from, to PressureLevel, _ float64) {
	pc.notifyGoroutineBudget(to)
	handlers := pc.levelHandlers()
	if handler, ok := handlers[to]; ok {
		handler()
	}
}

func (pc *PressureController) notifyGoroutineBudget(level PressureLevel) {
	if pc.goroutineBudget != nil {
		pc.goroutineBudget.OnPressureChange(int32(level))
	}
}

func (pc *PressureController) levelHandlers() map[PressureLevel]func() {
	return map[PressureLevel]func(){
		PressureNormal:   pc.handleNormal,
		PressureElevated: pc.handleElevated,
		PressureHigh:     pc.handleHigh,
		PressureCritical: pc.handleCritical,
	}
}

func (pc *PressureController) handleNormal() {
	pc.admission.Resume()
	pc.suspender.ResumeAll()
}

func (pc *PressureController) handleElevated() {
	pc.admission.Stop()
	go pc.triggerGC()
}

func (pc *PressureController) handleHigh() {
	pc.admission.Stop()
	go pc.triggerGC()
	pc.evictor.Evict(0.25)
	pc.compactor.CompactAll()
}

func (pc *PressureController) handleCritical() {
	pc.admission.Stop()
	go pc.triggerGC()
	pc.evictor.Evict(0.50)
	pc.compactor.CompactAll()
	pc.suspender.PauseLowestPriority()
}

func (pc *PressureController) triggerGC() {
	runtime.GC()
	debug.FreeOSMemory()
}

func (pc *PressureController) Run(ctx context.Context) {
	pc.mu.Lock()
	if pc.running {
		pc.mu.Unlock()
		return
	}
	pc.running = true
	pc.mu.Unlock()

	pc.monitor.Run(ctx)

	pc.mu.Lock()
	pc.running = false
	pc.mu.Unlock()
}

func (pc *PressureController) Registry() *UsageRegistry {
	return pc.registry
}

func (pc *PressureController) Admission() *AdmissionController {
	return pc.admission
}

func (pc *PressureController) Evictor() *CacheEvictor {
	return pc.evictor
}

func (pc *PressureController) CurrentLevel() PressureLevel {
	return pc.stateMachine.Level()
}
