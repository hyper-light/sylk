package resources

import (
	"sync"
	"time"
)

type PressureLevel int

const (
	PressureNormal PressureLevel = iota
	PressureElevated
	PressureHigh
	PressureCritical
)

var pressureLevelNames = [...]string{
	PressureNormal:   "NORMAL",
	PressureElevated: "ELEVATED",
	PressureHigh:     "HIGH",
	PressureCritical: "CRITICAL",
}

func (p PressureLevel) String() string {
	if int(p) >= 0 && int(p) < len(pressureLevelNames) {
		return pressureLevelNames[p]
	}
	return "UNKNOWN"
}

type PressureStateConfig struct {
	EnterThresholds []float64
	ExitThresholds  []float64
	Cooldown        time.Duration
}

func DefaultPressureStateConfig() PressureStateConfig {
	return PressureStateConfig{
		EnterThresholds: []float64{0.50, 0.70, 0.85},
		ExitThresholds:  []float64{0.35, 0.55, 0.70},
		Cooldown:        3 * time.Second,
	}
}

type TransitionCallback func(from, to PressureLevel, usage float64)

type PressureStateMachine struct {
	mu         sync.Mutex
	level      PressureLevel
	lastChange time.Time
	config     PressureStateConfig

	onTransition TransitionCallback
}

func NewPressureStateMachine(config PressureStateConfig) *PressureStateMachine {
	return &PressureStateMachine{
		level:  PressureNormal,
		config: config,
	}
}

func (sm *PressureStateMachine) SetTransitionCallback(cb TransitionCallback) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.onTransition = cb
}

func (sm *PressureStateMachine) Update(usage float64) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.inCooldown() {
		return false
	}

	newLevel := sm.computeLevel(usage)
	if newLevel == sm.level {
		return false
	}

	return sm.transition(newLevel, usage)
}

func (sm *PressureStateMachine) inCooldown() bool {
	return time.Since(sm.lastChange) < sm.config.Cooldown
}

func (sm *PressureStateMachine) computeLevel(usage float64) PressureLevel {
	upward := sm.checkUpwardTransition(usage)
	if upward != sm.level {
		return upward
	}
	return sm.checkDownwardTransition(usage)
}

func (sm *PressureStateMachine) checkUpwardTransition(usage float64) PressureLevel {
	highestLevel := sm.level
	for i := int(sm.level); i < len(sm.config.EnterThresholds); i++ {
		if usage >= sm.config.EnterThresholds[i] {
			highestLevel = PressureLevel(i + 1)
		}
	}
	return highestLevel
}

func (sm *PressureStateMachine) checkDownwardTransition(usage float64) PressureLevel {
	for i := int(sm.level) - 1; i >= 0; i-- {
		if usage < sm.config.ExitThresholds[i] {
			return PressureLevel(i)
		}
	}
	return sm.level
}

func (sm *PressureStateMachine) transition(newLevel PressureLevel, usage float64) bool {
	from := sm.level
	sm.level = newLevel
	sm.lastChange = time.Now()

	if sm.onTransition != nil {
		sm.onTransition(from, newLevel, usage)
	}

	return true
}

func (sm *PressureStateMachine) Level() PressureLevel {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.level
}

func (sm *PressureStateMachine) BypassCooldown() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.lastChange = time.Time{}
}

func (sm *PressureStateMachine) LastChange() time.Time {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.lastChange
}

func (sm *PressureStateMachine) InCooldown() bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.inCooldown()
}
