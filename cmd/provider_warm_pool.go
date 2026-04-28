// Provider warm pool — plan-independent cache of wrapped provider
// instances keyed on (modelID, priority). Eliminates the cold-start
// cost of createSwapProvider on every on-demand agent creation.
//
// Strict invariant: no plan-specific state lives in the pool. The
// pool holds only provider/auth/factory bundles that are safe to
// share across agent instances of the same type. It does NOT hold
// pre-constructed pods or pre-allocated VFS volumes — those remain
// per-task atomic constructions per the canon established in #38.
//
// Concurrency: per-key sync.Once gates first-time construction.
// Concurrent callers for the same (model, priority) block on the
// same construction; concurrent callers for different keys run in
// parallel.
package cmd

import (
	"context"
	"fmt"
	"sync"

	"github.com/adalundhe/sylk/core/credentials"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/providers/gateway"
)

type providerPoolKey struct {
	model    string
	priority gateway.RequestPriority
}

type providerPoolEntry struct {
	once     sync.Once
	provider providers.ProviderAdapter
	err      error
}

// providerWarmPool memoizes createSwapProvider results across agent
// creator calls. Each pipeline agent type (engineer, designer,
// inspector-pipeline, tester-pipeline) typically spins up many
// instances over a session; without the pool, each pays the
// createSwapProvider cost (auth resolution, HTTP client init,
// gateway wrapping) independently.
type providerWarmPool struct {
	mu      sync.Mutex
	entries map[providerPoolKey]*providerPoolEntry
}

func newProviderWarmPool() *providerWarmPool {
	return &providerWarmPool{
		entries: make(map[providerPoolKey]*providerPoolEntry),
	}
}

// Get returns a cached or freshly-constructed provider adapter for
// (model, priority). Errors are sticky per-key — a failure on first
// construction is not retried (matches the design of taskPodSlot in
// dag_bridge.go: failed initialization is a hard fault, not a
// transient).
func (p *providerWarmPool) Get(
	ctx context.Context,
	modelID string,
	authRegistry *credentials.AuthRegistry,
	googleGw, anthropicGw, openaiGw *gateway.ProviderGateway,
	priority gateway.RequestPriority,
) (providers.ProviderAdapter, error) {
	if p == nil {
		return createSwapProvider(ctx, modelID, authRegistry, googleGw, anthropicGw, openaiGw, priority)
	}
	key := providerPoolKey{model: modelID, priority: priority}
	p.mu.Lock()
	entry, ok := p.entries[key]
	if !ok {
		entry = &providerPoolEntry{}
		p.entries[key] = entry
	}
	p.mu.Unlock()

	entry.once.Do(func() {
		entry.provider, entry.err = createSwapProvider(ctx, modelID, authRegistry, googleGw, anthropicGw, openaiGw, priority)
	})
	if entry.err != nil {
		return nil, fmt.Errorf("provider warm pool [%s,%v]: %w", modelID, priority, entry.err)
	}
	return entry.provider, nil
}

// Invalidate drops a cached entry so the next Get triggers a fresh
// construction. Used when auth changes — the caller knows the
// provider's auth handle is no longer valid. Concurrent in-flight
// Get calls for that key continue to use the old entry; only
// post-Invalidate calls see the fresh construction.
func (p *providerWarmPool) Invalidate(modelID string, priority gateway.RequestPriority) {
	if p == nil {
		return
	}
	key := providerPoolKey{model: modelID, priority: priority}
	p.mu.Lock()
	delete(p.entries, key)
	p.mu.Unlock()
}
