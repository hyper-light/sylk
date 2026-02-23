package handoff

import (
	"testing"
	"time"
)

func TestEWMA_Convergence(t *testing.T) {
	e := ewmaStat{alpha: 0.1}

	// Feed constant value — should converge.
	for range 50 {
		e.update(100.0)
	}
	if e.mean < 99.9 || e.mean > 100.1 {
		t.Errorf("after 50 constant samples, mean = %v, want ~100", e.mean)
	}
}

func TestEWMA_ColdStart(t *testing.T) {
	e := ewmaStat{alpha: 0.1}
	if e.initialized {
		t.Error("expected uninitialized")
	}

	e.update(42.0)
	if !e.initialized {
		t.Error("expected initialized after first update")
	}
	if e.mean != 42.0 {
		t.Errorf("first sample: mean = %v, want 42.0", e.mean)
	}
}

func TestSignalBaseline_ColdStartReturnsZero(t *testing.T) {
	b := &SignalBaseline{
		ttft:            ewmaStat{alpha: 0.1},
		tokensPerSecond: ewmaStat{alpha: 0.1},
	}
	if b.TTFT() != 0 {
		t.Errorf("cold TTFT = %v, want 0", b.TTFT())
	}
	if b.TokensPerSecond() != 0 {
		t.Errorf("cold TPS = %v, want 0", b.TokensPerSecond())
	}
}

func TestSignalBaseline_UpdateAndRead(t *testing.T) {
	b := &SignalBaseline{
		ttft:            ewmaStat{alpha: 0.5},
		tokensPerSecond: ewmaStat{alpha: 0.5},
	}

	b.Update(StreamSignals{
		TimeToFirstToken: 100 * time.Millisecond,
		TokensPerSecond:  50,
	})

	if b.TTFT() != 100*time.Millisecond {
		t.Errorf("TTFT after first = %v, want 100ms", b.TTFT())
	}
	if b.TokensPerSecond() != 50 {
		t.Errorf("TPS after first = %v, want 50", b.TokensPerSecond())
	}
}

func TestSignalBaseline_IsWarmedUp(t *testing.T) {
	b := &SignalBaseline{
		ttft:            ewmaStat{alpha: 0.1},
		tokensPerSecond: ewmaStat{alpha: 0.1},
	}

	if b.IsWarmedUp(5) {
		t.Error("expected not warmed up with 0 samples")
	}

	for range 5 {
		b.Update(StreamSignals{TimeToFirstToken: 10 * time.Millisecond, TokensPerSecond: 10})
	}

	if !b.IsWarmedUp(5) {
		t.Error("expected warmed up after 5 samples")
	}
}

func TestBaselineRegistry_PerKeyIsolation(t *testing.T) {
	reg := NewBaselineRegistry(50)

	keyA := BaselineKey{AgentType: "guide", ModelID: "model-a"}
	keyB := BaselineKey{AgentType: "guide", ModelID: "model-b"}

	reg.Update(keyA, StreamSignals{TokensPerSecond: 100})
	reg.Update(keyB, StreamSignals{TokensPerSecond: 200})

	a := reg.Get(keyA)
	b := reg.Get(keyB)

	if a == nil || b == nil {
		t.Fatal("expected both baselines to exist")
	}
	if a.TokensPerSecond() == b.TokensPerSecond() {
		t.Error("expected different TPS for different keys")
	}
}

func TestBaselineRegistry_GetNil(t *testing.T) {
	reg := NewBaselineRegistry(50)
	if reg.Get(BaselineKey{AgentType: "x", ModelID: "y"}) != nil {
		t.Error("expected nil for absent key")
	}
}

func TestNewBaselineRegistry_AlphaDerivation(t *testing.T) {
	// halfLife=50 → alpha ≈ 0.0138
	reg := NewBaselineRegistry(50)
	if reg.alpha <= 0 || reg.alpha >= 1 {
		t.Errorf("alpha = %v, want (0, 1)", reg.alpha)
	}
}
