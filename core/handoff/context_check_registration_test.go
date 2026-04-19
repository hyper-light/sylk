package handoff

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestBridge_RecordTurn_InvokesContextCheckHook is the HAND-08
// acceptance test: every RecordTurn call must run the bridge's
// ContextCheckHook against a freshly-built ContextState. This is the
// mechanism by which an agent's dispatch loop notices context
// utilization climbing past the handoff threshold. A regression here
// (e.g. a future refactor that moves RecordTurn's body past the
// contextCheck call) silently defeats every handoff trigger.
//
// We verify via an observer callback, not by checking handoff side
// effects — the observer fires exactly once per RecordTurn regardless
// of whether the check recommends handoff, so it's the cleanest proxy
// for "the hook was registered and invoked".
func TestBridge_RecordTurn_InvokesContextCheckHook(t *testing.T) {
	desc := AgentDescriptor{
		AgentType:     "hand08-agent",
		ModelID:       "opus-4.7",
		ContextWindow: 200_000,
		Category:      CategoryStandalone,
	}
	agent := newTestBridgeAgent("hand08-agent-1", "hand08-agent", desc)
	_, bridge := setupBridgeTest(t, agent)

	var observed atomic.Int32
	bridge.contextCheck.SetObserver(func(ctx *ContextState, _ *HandoffRecommendation) {
		if ctx != nil {
			observed.Add(1)
		}
	})

	// Feed a normal-utilization turn: the hook must fire regardless of
	// whether the recommendation says ShouldHandoff. Observer is the
	// contract surface that proves registration is live.
	bridge.RecordTurn(TurnRecord{
		ContextSize:  10_000,
		OutputTokens: 500,
		TurnNumber:   1,
		Timestamp:    time.Now(),
	})

	if got := observed.Load(); got != 1 {
		t.Errorf("observer fired %d times, want 1 — ContextCheckHook not invoked per HAND-08", got)
	}
}

// TestBridge_RecordTurn_HighUtilizationProducesShouldHandoff exercises
// the end-to-end chain: at a context size above the configured
// threshold, EvaluateContext must return ShouldHandoff=true. This is
// the "threshold works" half of the HAND-08 invariant, separate from
// the "hook is called" half.
//
// Context threshold defaults (BridgeConfigForAgent) land at ~75% for
// standard agents; we feed 80% to be safely past. Observer captures
// the recommendation for the assertion.
func TestBridge_RecordTurn_HighUtilizationProducesShouldHandoff(t *testing.T) {
	desc := AgentDescriptor{
		AgentType:     "hand08-agent",
		ModelID:       "opus-4.7",
		ContextWindow: 100_000,
		Category:      CategoryStandalone,
	}
	agent := newTestBridgeAgent("hand08-agent-high", "hand08-agent", desc)
	_, bridge := setupBridgeTest(t, agent)

	var lastRec *HandoffRecommendation
	bridge.contextCheck.SetObserver(func(_ *ContextState, rec *HandoffRecommendation) {
		if rec != nil {
			lastRec = rec
		}
	})

	// Push utilization well past any reasonable threshold by feeding
	// 99% of the configured ContextWindow. Using 99% instead of 80%
	// avoids tripping over internal ContextFullThreshold variants set
	// above the nominal ContextThreshold — the invariant we care about
	// is "clearly full → must trigger", not exact threshold calibration.
	bridge.RecordTurn(TurnRecord{
		ContextSize:  99_000,
		OutputTokens: 1_000,
		TurnNumber:   1,
		Timestamp:    time.Now(),
	})

	if lastRec == nil {
		t.Fatal("observer never fired — hook not called at all")
	}
	if !lastRec.ShouldHandoff {
		t.Errorf("ShouldHandoff = false at 80%% utilization (rec=%+v), want true", lastRec)
	}
	if lastRec.Trigger != TriggerContextFull {
		t.Errorf("Trigger = %q, want ContextFull at high utilization", lastRec.Trigger)
	}
}

// TestBridge_RecordTurn_LowUtilizationDoesNotRecommendHandoff
// guards the other end: at low utilization the hook still fires but
// ShouldHandoff must be false. A regression here (hook always
// recommending handoff) would destabilize every agent from turn 1.
func TestBridge_RecordTurn_LowUtilizationDoesNotRecommendHandoff(t *testing.T) {
	desc := AgentDescriptor{
		AgentType:     "hand08-agent",
		ModelID:       "opus-4.7",
		ContextWindow: 100_000,
		Category:      CategoryStandalone,
	}
	agent := newTestBridgeAgent("hand08-agent-low", "hand08-agent", desc)
	_, bridge := setupBridgeTest(t, agent)

	var lastRec *HandoffRecommendation
	bridge.contextCheck.SetObserver(func(_ *ContextState, rec *HandoffRecommendation) {
		if rec != nil {
			lastRec = rec
		}
	})

	bridge.RecordTurn(TurnRecord{
		ContextSize:  5_000,
		OutputTokens: 500,
		TurnNumber:   1,
		Timestamp:    time.Now(),
	})

	if lastRec == nil {
		t.Fatal("observer never fired — hook not called")
	}
	if lastRec.ShouldHandoff {
		t.Errorf("ShouldHandoff = true at 5%% utilization, want false (rec=%+v)", lastRec)
	}
}
