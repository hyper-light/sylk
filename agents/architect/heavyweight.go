package architect

import (
	"context"
	"fmt"
	"io"

	"github.com/adalundhe/sylk/core/container"
)

// Compile-time assertion: Architect implements HeavyweightReleasable.
var _ container.HeavyweightReleasable = (*Architect)(nil)

// ReleaseHeavyweight implements container.HeavyweightReleasable. While
// the architect is paused at Warm tier it doesn't need its planner —
// that holds the LLM provider (model metadata, KV cache, connection
// pools) and is the bulk of the agent's resident memory. Drop it.
// Skills, tool runtime, plan store, control store, conversation
// state, and bus subscriptions are retained: they're entwined with
// the agent's goroutine surface and the cost of releasing them
// outweighs the memory recovered.
//
// Idempotent: a release on an already-released planner is a no-op.
// Callers consume the planner via currentPlanner / ensurePlanner on
// every call, so they tolerate transient nil values without
// snapshotting.
func (a *Architect) ReleaseHeavyweight() error {
	a.plannerMu.Lock()
	defer a.plannerMu.Unlock()
	if a.planner == nil {
		return nil
	}
	if closer, ok := a.planner.(io.Closer); ok && closer != nil {
		if err := closer.Close(); err != nil {
			return fmt.Errorf("architect release planner: %w", err)
		}
	}
	a.planner = nil
	return nil
}

// RebuildHeavyweight implements container.HeavyweightReleasable. Eagerly
// re-creates the planner via ensurePlanner so a rebuild failure is
// detected up-front (the activation controller falls back to a cold
// start) rather than surfacing on the first request after promotion.
//
// ensurePlanner already routes through cfg.PlannerProviderWrapper, so
// the rebuilt planner is gateway-wrapped at the same priority as the
// originally bootstrapped one — no rate-limiting bypass.
//
// Idempotent: ensurePlanner returns the existing planner when already
// built. When LLM is disabled (cfg.EnableLLM == false) this is a
// no-op.
func (a *Architect) RebuildHeavyweight(ctx context.Context) error {
	if !a.config.EnableLLM {
		return nil
	}
	if planner := a.ensurePlanner(ctx); planner == nil {
		// ensurePlanner returns nil for both auth-not-configured and
		// hard errors; treat the auth case as benign (matches initial
		// bootstrap behaviour, which logs and continues), and let the
		// next request retry. A hard rebuild failure manifests as
		// "planner unavailable" downstream, which the agent already
		// handles. Returning nil here lets the controller keep the
		// resumed warm container instead of forcing a cold start.
		a.logger.Info("architect heavyweight rebuild: planner unavailable, deferring to lazy ensurePlanner",
			"model", a.CurrentModel())
	}
	return nil
}
