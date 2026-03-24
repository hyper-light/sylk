package orchestrator

import (
	"context"
	"strings"
	"sync"
)

type dispatchHoldState struct {
	active      bool
	allowedDAGs map[string]struct{}
	dagHoldRefs map[string]int
	dagHolds    map[string]string
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
		dagHoldRefs: make(map[string]int),
		dagHolds:    make(map[string]string),
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

func (g *dispatchHoldGate) activateDAG(sessionID, dagID, holdID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	state := g.ensure(sessionID)
	dagID = strings.TrimSpace(dagID)
	holdID = strings.TrimSpace(holdID)
	if dagID == "" || holdID == "" {
		return
	}
	if existing, ok := state.dagHolds[holdID]; ok {
		if existing == dagID {
			return
		}
	}
	state.dagHolds[holdID] = dagID
	state.dagHoldRefs[dagID]++
	old := state.changed
	state.changed = make(chan struct{})
	close(old)
}

func (g *dispatchHoldGate) resolveHold(sessionID, holdID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	state := g.ensure(sessionID)
	holdID = strings.TrimSpace(holdID)
	if holdID == "" {
		return
	}
	dagID, ok := state.dagHolds[holdID]
	if !ok {
		return
	}
	delete(state.dagHolds, holdID)
	if dagID != "" {
		if refs := state.dagHoldRefs[dagID] - 1; refs > 0 {
			state.dagHoldRefs[dagID] = refs
		} else {
			delete(state.dagHoldRefs, dagID)
		}
	}
	old := state.changed
	state.changed = make(chan struct{})
	close(old)
}

func (g *dispatchHoldGate) isHeld(sessionID, dagID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	state := g.ensure(sessionID)
	if state.active {
		return true
	}
	if dagID == "" {
		return false
	}
	return state.dagHoldRefs[strings.TrimSpace(dagID)] > 0
}

func (g *dispatchHoldGate) wait(ctx context.Context, sessionID, dagID string) error {
	for {
		g.mu.Lock()
		state := g.ensure(sessionID)
		active := state.active
		_, allowed := state.allowedDAGs[dagID]
		dagHeld := state.dagHoldRefs[strings.TrimSpace(dagID)] > 0
		changed := state.changed
		g.mu.Unlock()

		if (!active || allowed) && !dagHeld {
			return nil
		}

		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
