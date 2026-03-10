package orchestrator

import (
	"context"
	"sync"
)

type dispatchHoldState struct {
	active      bool
	allowedDAGs map[string]struct{}
	changed     chan struct{}
}

type dispatchHoldGate struct {
	mu       sync.Mutex
	sessions map[string]*dispatchHoldState
}

func newDispatchHoldGate() *dispatchHoldGate {
	return &dispatchHoldGate{sessions: make(map[string]*dispatchHoldState)}
}

func (g *dispatchHoldGate) ensure(sessionID string) *dispatchHoldState {
	state := g.sessions[sessionID]
	if state != nil {
		return state
	}
	state = &dispatchHoldState{
		allowedDAGs: make(map[string]struct{}),
		changed:     make(chan struct{}),
	}
	g.sessions[sessionID] = state
	return state
}

func (g *dispatchHoldGate) activate(sessionID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	state := g.ensure(sessionID)
	if state.active {
		return
	}
	state.active = true
	old := state.changed
	state.changed = make(chan struct{})
	close(old)
}

func (g *dispatchHoldGate) allowDAG(sessionID, dagID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	state := g.ensure(sessionID)
	if dagID != "" {
		state.allowedDAGs[dagID] = struct{}{}
	}
	old := state.changed
	state.changed = make(chan struct{})
	close(old)
}

func (g *dispatchHoldGate) resolve(sessionID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	state := g.ensure(sessionID)
	if !state.active && len(state.allowedDAGs) == 0 {
		return
	}
	state.active = false
	state.allowedDAGs = make(map[string]struct{})
	old := state.changed
	state.changed = make(chan struct{})
	close(old)
}

func (g *dispatchHoldGate) isActive(sessionID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.ensure(sessionID).active
}

func (g *dispatchHoldGate) wait(ctx context.Context, sessionID, dagID string) error {
	for {
		g.mu.Lock()
		state := g.ensure(sessionID)
		active := state.active
		_, allowed := state.allowedDAGs[dagID]
		changed := state.changed
		g.mu.Unlock()

		if !active || allowed {
			return nil
		}

		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
