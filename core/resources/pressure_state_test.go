package resources

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPressureLevel_String(t *testing.T) {
	tests := []struct {
		level PressureLevel
		want  string
	}{
		{PressureNormal, "NORMAL"},
		{PressureElevated, "ELEVATED"},
		{PressureHigh, "HIGH"},
		{PressureCritical, "CRITICAL"},
		{PressureLevel(99), "UNKNOWN"},
	}

	for _, tt := range tests {
		if got := tt.level.String(); got != tt.want {
			t.Errorf("PressureLevel(%d).String() = %q, want %q", tt.level, got, tt.want)
		}
	}
}

func TestDefaultPressureStateConfig(t *testing.T) {
	cfg := DefaultPressureStateConfig()

	if len(cfg.EnterThresholds) != 3 {
		t.Errorf("EnterThresholds length = %d, want 3", len(cfg.EnterThresholds))
	}
	if len(cfg.ExitThresholds) != 3 {
		t.Errorf("ExitThresholds length = %d, want 3", len(cfg.ExitThresholds))
	}
	if cfg.Cooldown != 3*time.Second {
		t.Errorf("Cooldown = %v, want 3s", cfg.Cooldown)
	}
}

func TestNewPressureStateMachine(t *testing.T) {
	sm := NewPressureStateMachine(DefaultPressureStateConfig())

	if sm.Level() != PressureNormal {
		t.Errorf("initial Level() = %v, want NORMAL", sm.Level())
	}
}

func TestPressureStateMachine_Update_NormalToElevated(t *testing.T) {
	sm := NewPressureStateMachine(DefaultPressureStateConfig())

	changed := sm.Update(0.55)

	if !changed {
		t.Error("Update(0.55) returned false, want true")
	}
	if sm.Level() != PressureElevated {
		t.Errorf("Level() = %v, want ELEVATED", sm.Level())
	}
}

func TestPressureStateMachine_Update_ElevatedToHigh(t *testing.T) {
	cfg := DefaultPressureStateConfig()
	cfg.Cooldown = 0
	sm := NewPressureStateMachine(cfg)

	sm.Update(0.55)
	changed := sm.Update(0.75)

	if !changed {
		t.Error("Update(0.75) returned false, want true")
	}
	if sm.Level() != PressureHigh {
		t.Errorf("Level() = %v, want HIGH", sm.Level())
	}
}

func TestPressureStateMachine_Update_HighToCritical(t *testing.T) {
	cfg := DefaultPressureStateConfig()
	cfg.Cooldown = 0
	sm := NewPressureStateMachine(cfg)

	sm.Update(0.55)
	sm.Update(0.75)
	changed := sm.Update(0.90)

	if !changed {
		t.Error("Update(0.90) returned false, want true")
	}
	if sm.Level() != PressureCritical {
		t.Errorf("Level() = %v, want CRITICAL", sm.Level())
	}
}

func TestPressureStateMachine_Hysteresis_DownwardTransition(t *testing.T) {
	cfg := DefaultPressureStateConfig()
	cfg.Cooldown = 0
	sm := NewPressureStateMachine(cfg)

	sm.Update(0.55)

	changed := sm.Update(0.45)
	if changed {
		t.Error("Update(0.45) should not transition (still above exit threshold 0.35)")
	}
	if sm.Level() != PressureElevated {
		t.Errorf("Level() = %v, want ELEVATED", sm.Level())
	}

	changed = sm.Update(0.30)
	if !changed {
		t.Error("Update(0.30) should transition back to NORMAL")
	}
	if sm.Level() != PressureNormal {
		t.Errorf("Level() = %v, want NORMAL", sm.Level())
	}
}

func TestPressureStateMachine_Cooldown_BlocksTransition(t *testing.T) {
	cfg := DefaultPressureStateConfig()
	cfg.Cooldown = 100 * time.Millisecond
	sm := NewPressureStateMachine(cfg)

	sm.Update(0.55)

	changed := sm.Update(0.75)
	if changed {
		t.Error("immediate Update should be blocked by cooldown")
	}
	if sm.Level() != PressureElevated {
		t.Errorf("Level() = %v, want ELEVATED", sm.Level())
	}
}

func TestPressureStateMachine_Cooldown_AllowsAfterExpiry(t *testing.T) {
	cfg := DefaultPressureStateConfig()
	cfg.Cooldown = 10 * time.Millisecond
	sm := NewPressureStateMachine(cfg)

	sm.Update(0.55)
	time.Sleep(15 * time.Millisecond)

	changed := sm.Update(0.75)
	if !changed {
		t.Error("Update after cooldown should succeed")
	}
	if sm.Level() != PressureHigh {
		t.Errorf("Level() = %v, want HIGH", sm.Level())
	}
}

func TestPressureStateMachine_BypassCooldown(t *testing.T) {
	cfg := DefaultPressureStateConfig()
	cfg.Cooldown = 1 * time.Hour
	sm := NewPressureStateMachine(cfg)

	sm.Update(0.55)
	sm.BypassCooldown()

	changed := sm.Update(0.75)
	if !changed {
		t.Error("Update after BypassCooldown should succeed")
	}
	if sm.Level() != PressureHigh {
		t.Errorf("Level() = %v, want HIGH", sm.Level())
	}
}

func TestPressureStateMachine_NoChangeWhenSameLevel(t *testing.T) {
	cfg := DefaultPressureStateConfig()
	cfg.Cooldown = 0
	sm := NewPressureStateMachine(cfg)

	sm.Update(0.55)

	changed := sm.Update(0.60)
	if changed {
		t.Error("Update with same level should return false")
	}
}

func TestPressureStateMachine_TransitionCallback(t *testing.T) {
	cfg := DefaultPressureStateConfig()
	cfg.Cooldown = 0
	sm := NewPressureStateMachine(cfg)

	var callbackFrom, callbackTo PressureLevel
	var callbackUsage float64
	var callbackCalled atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)

	sm.SetTransitionCallback(func(from, to PressureLevel, usage float64) {
		callbackFrom = from
		callbackTo = to
		callbackUsage = usage
		callbackCalled.Store(true)
		wg.Done()
	})

	sm.Update(0.55)
	wg.Wait()

	if !callbackCalled.Load() {
		t.Error("callback was not called")
	}
	if callbackFrom != PressureNormal {
		t.Errorf("callback from = %v, want NORMAL", callbackFrom)
	}
	if callbackTo != PressureElevated {
		t.Errorf("callback to = %v, want ELEVATED", callbackTo)
	}
	if callbackUsage != 0.55 {
		t.Errorf("callback usage = %v, want 0.55", callbackUsage)
	}
}

func TestPressureStateMachine_AllLevelsReachable(t *testing.T) {
	cfg := DefaultPressureStateConfig()
	cfg.Cooldown = 0
	sm := NewPressureStateMachine(cfg)

	testCases := []struct {
		usage float64
		want  PressureLevel
	}{
		{0.25, PressureNormal},
		{0.55, PressureElevated},
		{0.75, PressureHigh},
		{0.90, PressureCritical},
	}

	for _, tc := range testCases {
		sm.Update(tc.usage)
		if sm.Level() != tc.want {
			t.Errorf("at usage %.2f: Level() = %v, want %v", tc.usage, sm.Level(), tc.want)
		}
	}

	testCases = []struct {
		usage float64
		want  PressureLevel
	}{
		{0.65, PressureHigh},
		{0.50, PressureElevated},
		{0.30, PressureNormal},
	}

	for _, tc := range testCases {
		sm.Update(tc.usage)
		if sm.Level() != tc.want {
			t.Errorf("downward at usage %.2f: Level() = %v, want %v", tc.usage, sm.Level(), tc.want)
		}
	}
}

func TestPressureStateMachine_LastChange(t *testing.T) {
	sm := NewPressureStateMachine(DefaultPressureStateConfig())

	before := time.Now()
	sm.Update(0.55)
	after := time.Now()

	lastChange := sm.LastChange()
	if lastChange.Before(before) || lastChange.After(after) {
		t.Errorf("LastChange() = %v, should be between %v and %v", lastChange, before, after)
	}
}

func TestPressureStateMachine_InCooldown(t *testing.T) {
	cfg := DefaultPressureStateConfig()
	cfg.Cooldown = 100 * time.Millisecond
	sm := NewPressureStateMachine(cfg)

	sm.Update(0.55)

	if !sm.InCooldown() {
		t.Error("InCooldown() should return true immediately after transition")
	}

	time.Sleep(150 * time.Millisecond)

	if sm.InCooldown() {
		t.Error("InCooldown() should return false after cooldown period")
	}
}

func TestPressureStateMachine_ConcurrentLevel(t *testing.T) {
	sm := NewPressureStateMachine(DefaultPressureStateConfig())
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = sm.Level()
		}()
	}

	wg.Wait()
}

func TestPressureStateMachine_ConcurrentUpdate(t *testing.T) {
	cfg := DefaultPressureStateConfig()
	cfg.Cooldown = 0
	sm := NewPressureStateMachine(cfg)
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			usage := 0.40 + float64(idx)*0.01
			sm.Update(usage)
		}(i)
	}

	wg.Wait()

	level := sm.Level()
	if level < PressureNormal || level > PressureCritical {
		t.Errorf("Level() = %v, should be valid level", level)
	}
}

func TestPressureStateMachine_SkipLevels(t *testing.T) {
	cfg := DefaultPressureStateConfig()
	cfg.Cooldown = 0
	sm := NewPressureStateMachine(cfg)

	changed := sm.Update(0.90)
	if !changed {
		t.Error("Direct jump to CRITICAL should work")
	}
	if sm.Level() != PressureCritical {
		t.Errorf("Level() = %v, want CRITICAL", sm.Level())
	}
}

func TestPressureStateMachine_BelowNormal(t *testing.T) {
	sm := NewPressureStateMachine(DefaultPressureStateConfig())

	changed := sm.Update(0.10)
	if changed {
		t.Error("Update at 0.10 should not change from NORMAL")
	}
	if sm.Level() != PressureNormal {
		t.Errorf("Level() = %v, want NORMAL", sm.Level())
	}
}
