package activation

import (
	"context"
	"fmt"
	"sync"

	"github.com/adalundhe/sylk/core/container"
)

// ActivationGate coalesces concurrent EnsureActive calls for the same
// agent type so that exactly one activation runs while others wait.
// Pattern: typed singleflight with context cancellation support.
type ActivationGate struct {
	mu       sync.Mutex
	inflight map[string]*activationFuture // agentType -> future
}

type activationFuture struct {
	done chan struct{}
	c    *container.Container
	err  error
}

// NewActivationGate creates a gate with no inflight activations.
func NewActivationGate() *ActivationGate {
	return &ActivationGate{
		inflight: make(map[string]*activationFuture),
	}
}

// DoOrWait either starts an activation (first caller) or waits for
// the in-progress activation (subsequent callers). The activate func
// is only called by the first caller; all others receive its result.
//
// Returns (container, wasCoalesced, error). wasCoalesced is true when
// the caller waited on an existing inflight activation rather than
// running activate itself.
//
// If the activate function panics, the panic is recovered and
// converted to an error. We never re-panic — all callers (including
// coalesced waiters) receive the error.
func (g *ActivationGate) DoOrWait(
	ctx context.Context,
	agentType string,
	activate func(context.Context) (*container.Container, error),
) (*container.Container, bool, error) {
	g.mu.Lock()
	if f, ok := g.inflight[agentType]; ok {
		g.mu.Unlock()
		return g.waitForFuture(ctx, f)
	}

	f := &activationFuture{done: make(chan struct{})}
	g.inflight[agentType] = f
	g.mu.Unlock()

	// Ensure f.done is always closed, even if activate panics.
	// Without this, coalesced waiters would hang forever on a panic.
	defer func() {
		close(f.done)
		g.mu.Lock()
		delete(g.inflight, agentType)
		g.mu.Unlock()
	}()

	f.c, f.err = g.safeActivate(ctx, activate)
	return f.c, false, f.err
}

// safeActivate calls the activation function and recovers from panics,
// converting them to errors. We never re-panic — the error is
// propagated to all callers (the primary and any coalesced waiters).
func (g *ActivationGate) safeActivate(
	ctx context.Context,
	activate func(context.Context) (*container.Container, error),
) (c *container.Container, err error) {
	defer func() {
		if r := recover(); r != nil {
			c = nil
			err = fmt.Errorf("activation panic recovered: %v", r)
		}
	}()
	return activate(ctx)
}

func (g *ActivationGate) waitForFuture(ctx context.Context, f *activationFuture) (*container.Container, bool, error) {
	select {
	case <-ctx.Done():
		return nil, true, ctx.Err()
	case <-f.done:
		return f.c, true, f.err
	}
}
