package gateway

import (
	"context"

	"github.com/adalundhe/sylk/core/providers"
)

// ProviderWrapper re-applies gateway admission control to a fresh provider.
// Agents store this callback and invoke it after credential refresh so the
// new provider remains rate-limited.
type ProviderWrapper func(inner providers.ProviderAdapter) *GatewayProvider

// Wrapper returns a ProviderWrapper bound to this gateway and priority.
func (g *ProviderGateway) Wrapper(priority RequestPriority) ProviderWrapper {
	return func(inner providers.ProviderAdapter) *GatewayProvider {
		return g.WrapProvider(inner, priority)
	}
}

// GatewayProvider is a transparent proxy that wraps an inner provider with
// gateway admission control. Agents use it identically to a direct provider.
type GatewayProvider struct {
	inner    providers.ProviderAdapter
	gateway  *ProviderGateway
	priority RequestPriority
}

// Compile-time interface checks.
var (
	_ providers.ProviderAdapter      = (*GatewayProvider)(nil)
	_ providers.StreamHandlerProvider = (*GatewayProvider)(nil)
)

// --- Passthrough methods (no admission required) ---

func (p *GatewayProvider) Name() string                          { return p.inner.Name() }
func (p *GatewayProvider) SupportedModels() []providers.ModelInfo { return p.inner.SupportedModels() }
func (p *GatewayProvider) CountTokens(msgs []providers.Message) (int, error) {
	return p.inner.CountTokens(msgs)
}
func (p *GatewayProvider) MaxContextTokens(model string) int { return p.inner.MaxContextTokens(model) }
func (p *GatewayProvider) HealthCheck(ctx context.Context) error { return p.inner.HealthCheck(ctx) }

// --- Gated methods (admission required) ---

// Complete performs a non-streaming completion through the gateway.
func (p *GatewayProvider) Complete(ctx context.Context, req *providers.CompletionRequest) (*providers.CompletionResponse, error) {
	if err := p.gateway.Admit(ctx, p.priority); err != nil {
		return nil, err
	}

	ctx = p.injectRetryObserver(ctx)
	resp, err := p.inner.Complete(ctx, req)
	p.gateway.Release(err)
	p.detect429(err)
	return resp, err
}

// Stream performs a channel-based streaming completion through the gateway.
// The concurrency slot is held until the returned channel is drained.
func (p *GatewayProvider) Stream(ctx context.Context, req *providers.CompletionRequest) (<-chan *providers.StreamChunk, error) {
	if err := p.gateway.Admit(ctx, p.priority); err != nil {
		return nil, err
	}

	ctx = p.injectRetryObserver(ctx)
	ch, err := p.inner.Stream(ctx, req)
	if err != nil {
		p.gateway.Release(err)
		p.detect429(err)
		return nil, err
	}

	// Wrap the channel to release the slot when the source closes.
	out := make(chan *providers.StreamChunk, cap(ch))
	go p.forwardStreamChunks(ch, out)
	return out, nil
}

// StreamWithHandler performs a handler-based streaming completion through the
// gateway. Requires the inner provider to implement StreamHandlerProvider.
func (p *GatewayProvider) StreamWithHandler(ctx context.Context, req *providers.StreamRequest, handler providers.StreamHandler) error {
	shp, ok := p.inner.(providers.StreamHandlerProvider)
	if !ok {
		return providers.WrapError(providers.ProviderTypeGoogle, "stream_with_handler",
			providers.ErrModelNotSupported)
	}

	if err := p.gateway.Admit(ctx, p.priority); err != nil {
		return err
	}

	ctx = p.injectRetryObserver(ctx)
	err := shp.StreamWithHandler(ctx, req, handler)
	p.gateway.Release(err)
	p.detect429(err)
	return err
}

// Inner returns the underlying provider for type-specific access.
func (p *GatewayProvider) Inner() providers.ProviderAdapter {
	return p.inner
}

// injectRetryObserver chains the gateway's 429-learning observer with any
// existing observer on the context.
func (p *GatewayProvider) injectRetryObserver(ctx context.Context) context.Context {
	existing := providers.RetryObserverFromContext(ctx)
	gatewayObs := func(event providers.RetryEvent) {
		if retryAfter, ok := is429Error(event.Err); ok {
			p.gateway.Record429(retryAfter)
		}
		if existing != nil {
			existing(event)
		}
	}
	return providers.WithRetryObserver(ctx, gatewayObs)
}

// detect429 checks a final error for 429 status and records it.
func (p *GatewayProvider) detect429(err error) {
	if err == nil {
		return
	}
	if retryAfter, ok := is429Error(err); ok {
		p.gateway.Record429(retryAfter)
	}
}

// forwardStreamChunks drains src into dst and releases the gateway slot
// when the source channel closes.
func (p *GatewayProvider) forwardStreamChunks(src <-chan *providers.StreamChunk, dst chan<- *providers.StreamChunk) {
	defer func() {
		close(dst)
		p.gateway.Release(nil)
	}()

	for chunk := range src {
		dst <- chunk
	}
}
