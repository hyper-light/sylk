package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBootstrapGate_SignalBeforeWait(t *testing.T) {
	gate := newBootstrapGate(5 * time.Second)
	gate.SignalReady()

	eventCh := make(chan *busEvent, 1)
	ctx := context.Background()

	criticals := gate.Wait(ctx, eventCh)
	assert.Empty(t, criticals)
	assert.True(t, gate.IsReady())
}

func TestBootstrapGate_SignalDuringWait(t *testing.T) {
	gate := newBootstrapGate(10 * time.Second)
	eventCh := make(chan *busEvent, 8)

	// Push some non-critical events that should be discarded.
	eventCh <- &busEvent{Topic: "agents.registry", Severity: severityInfo, Summary: "test"}
	eventCh <- &busEvent{Topic: "agents.registry", Severity: severityInfo, Summary: "test2"}

	ctx := context.Background()

	go func() {
		time.Sleep(50 * time.Millisecond)
		gate.SignalReady()
	}()

	start := time.Now()
	criticals := gate.Wait(ctx, eventCh)
	elapsed := time.Since(start)

	assert.Empty(t, criticals)
	assert.True(t, gate.IsReady())
	assert.Less(t, elapsed, 5*time.Second, "should unblock well before safety deadline")
}

func TestBootstrapGate_SafetyDeadline(t *testing.T) {
	gate := newBootstrapGate(100 * time.Millisecond)
	eventCh := make(chan *busEvent)

	ctx := context.Background()

	start := time.Now()
	criticals := gate.Wait(ctx, eventCh)
	elapsed := time.Since(start)

	assert.Empty(t, criticals)
	assert.InDelta(t, 100, elapsed.Milliseconds(), 50, "should exit near safety deadline")
}

func TestBootstrapGate_CriticalEventDuringWait(t *testing.T) {
	gate := newBootstrapGate(10 * time.Second)
	eventCh := make(chan *busEvent, 8)

	// Non-critical followed by critical.
	eventCh <- &busEvent{Topic: "agents.registry", Severity: severityInfo, Summary: "reg"}
	eventCh <- &busEvent{Topic: "tasks.failed", Severity: severityCritical, Summary: "task X failed"}

	ctx := context.Background()
	criticals := gate.Wait(ctx, eventCh)

	require.Len(t, criticals, 1)
	assert.Equal(t, severityCritical, criticals[0].Severity)
	assert.Equal(t, "task X failed", criticals[0].Summary)
}

func TestBootstrapGate_ShutdownDuringWait(t *testing.T) {
	gate := newBootstrapGate(10 * time.Second)
	eventCh := make(chan *busEvent)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	criticals := gate.Wait(ctx, eventCh)
	elapsed := time.Since(start)

	assert.Empty(t, criticals)
	assert.Less(t, elapsed, 5*time.Second, "should exit promptly on context cancel")
}

func TestBootstrapGate_DoubleSignal(t *testing.T) {
	gate := newBootstrapGate(5 * time.Second)

	// Must not panic on double close.
	gate.SignalReady()
	gate.SignalReady()

	assert.True(t, gate.IsReady())
}

func TestBootstrapGate_IsReadyBeforeSignal(t *testing.T) {
	gate := newBootstrapGate(5 * time.Second)
	assert.False(t, gate.IsReady())

	gate.SignalReady()
	assert.True(t, gate.IsReady())
}
