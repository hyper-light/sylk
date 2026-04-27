package archivalist

import (
	"context"
	"fmt"
	"io"

	"github.com/adalundhe/sylk/core/container"
)

// Compile-time assertion: Archivalist implements HeavyweightReleasable.
var _ container.HeavyweightReleasable = (*Archivalist)(nil)

// ReleaseHeavyweight implements container.HeavyweightReleasable. While
// the archivalist is paused at Warm tier it doesn't need its LLM
// provider — that's the bulk of the agent's resident memory (model
// metadata, KV cache, connection pools). Drop it. Skills, tool
// runtime, registry, knowledge backend, and bus subscriptions are
// retained: they're entwined with the agent's goroutine surface
// (replicas, queryCoordinator, hybridMemory, embeddings store) and
// the cost of releasing them outweighs the memory recovered.
//
// Idempotent: a release on an already-released provider is a no-op.
// Synthesizer/client consume the provider via lookupProvider on every
// call, so they tolerate transient nil values without snapshotting.
func (a *Archivalist) ReleaseHeavyweight() error {
	a.runMu.Lock()
	defer a.runMu.Unlock()
	if a.provider == nil {
		return nil
	}
	if closer, ok := a.provider.(io.Closer); ok && closer != nil {
		if err := closer.Close(); err != nil {
			return fmt.Errorf("archivalist release provider: %w", err)
		}
	}
	a.provider = nil
	return nil
}

// RebuildHeavyweight implements container.HeavyweightReleasable. Uses
// the previously-wired ProviderRefresher to recreate the LLM provider
// for the agent's current model + registry-resolved auth method. If
// the refresher was never set, returns nil — the agent runs in
// non-LLM mode until something else wires the provider, the same
// post-init / pre-auth state Ready() already handles.
//
// Idempotent: a rebuild on already-built state is a no-op.
func (a *Archivalist) RebuildHeavyweight(ctx context.Context) error {
	a.runMu.RLock()
	if a.provider != nil {
		a.runMu.RUnlock()
		return nil
	}
	refresher := a.refresher
	model := a.config.Model
	a.runMu.RUnlock()

	if refresher == nil {
		return nil
	}
	if model == "" {
		model = ModelSonnet45
	}
	p, err := refresher(ctx, model, "")
	if err != nil {
		return fmt.Errorf("archivalist rebuild provider: %w", err)
	}
	a.SetProvider(p)
	return nil
}
