package librarian

import (
	"context"
	"fmt"
	"io"

	"github.com/adalundhe/sylk/core/container"
)

// Compile-time assertion: Librarian implements HeavyweightReleasable.
var _ container.HeavyweightReleasable = (*Librarian)(nil)

// ReleaseHeavyweight implements container.HeavyweightReleasable. While
// the librarian is paused at Warm tier it doesn't need its LLM
// provider — that's the bulk of the agent's resident memory (model
// metadata, KV cache, connection pools). Drop it. Skills, tool
// runtime, registry, knowledge backend, and bus subscriptions are
// retained: they're entwined with the agent's goroutine surface and
// the cost of releasing them outweighs the memory recovered.
//
// Idempotent: a release on an already-released provider is a no-op.
// Safe to call without holding plannerMu — providerMu serializes the
// closer/nil swap.
func (l *Librarian) ReleaseHeavyweight() error {
	l.providerMu.Lock()
	defer l.providerMu.Unlock()
	if l.provider == nil {
		return nil
	}
	if closer, ok := l.provider.(io.Closer); ok && closer != nil {
		if err := closer.Close(); err != nil {
			return fmt.Errorf("librarian release provider: %w", err)
		}
	}
	l.provider = nil
	return nil
}

// RebuildHeavyweight implements container.HeavyweightReleasable. Uses
// the previously-wired ProviderRefresher to recreate the LLM provider
// for the agent's current model + registry-resolved auth method. If
// the refresher was never set (e.g., during early bootstrap before
// gateways are up), returns nil — the agent runs in non-LLM mode
// until something else wires the provider, which is the same
// post-init / pre-auth state the agent already handles via
// l.Ready().
//
// Idempotent: a rebuild on already-built state is a no-op.
func (l *Librarian) RebuildHeavyweight(ctx context.Context) error {
	l.providerMu.RLock()
	if l.provider != nil {
		l.providerMu.RUnlock()
		return nil
	}
	refresher := l.refresher
	model := l.config.Model
	l.providerMu.RUnlock()

	if refresher == nil {
		return nil
	}
	if model == "" {
		model = DefaultLibrarianModel
	}
	provider, err := refresher(ctx, model, "")
	if err != nil {
		return fmt.Errorf("librarian rebuild provider: %w", err)
	}
	l.SetProvider(provider)
	return nil
}
