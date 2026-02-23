package orchestrator

import (
	"context"
	"sync"
	"time"
)

// bootstrapGate blocks the LLM loop until the system signals readiness.
//
// Three exit conditions (whichever fires first):
//  1. SignalReady — called by cmd/tui.go after bootstrap completes
//  2. Safety deadline — prevents permanent stall if signal never arrives
//  3. Critical event — task failures during boot must not be dropped
type bootstrapGate struct {
	readyCh     chan struct{}
	readyOnce   sync.Once
	safetyDelay time.Duration
}

func newBootstrapGate(safetyDeadline time.Duration) *bootstrapGate {
	return &bootstrapGate{
		readyCh:     make(chan struct{}),
		safetyDelay: safetyDeadline,
	}
}

// SignalReady marks bootstrap as complete. Idempotent via sync.Once.
func (g *bootstrapGate) SignalReady() {
	g.readyOnce.Do(func() { close(g.readyCh) })
}

// IsReady returns true if the gate has been opened (non-blocking).
func (g *bootstrapGate) IsReady() bool {
	select {
	case <-g.readyCh:
		return true
	default:
		return false
	}
}

// Wait blocks until one of the three exit conditions fires.
// Non-critical events arriving during the wait are discarded.
// Returns any critical events that must be processed immediately.
func (g *bootstrapGate) Wait(ctx context.Context, eventCh <-chan *busEvent) []*busEvent {
	safety := time.NewTimer(g.safetyDelay)
	defer safety.Stop()

	var criticals []*busEvent

	for {
		select {
		case <-ctx.Done():
			return criticals

		case <-g.readyCh:
			return criticals

		case <-safety.C:
			return criticals

		case evt := <-eventCh:
			if evt.Severity == severityCritical {
				criticals = append(criticals, evt)
				return criticals
			}
			// Non-critical event during bootstrap — discard.
		}
	}
}
