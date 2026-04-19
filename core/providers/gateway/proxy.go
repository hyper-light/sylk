package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/adalundhe/sylk/core/agents/identity"
	"github.com/adalundhe/sylk/core/providers"
)

// ErrDispatchIdentityMissing is returned by the gateway when a
// provider call is attempted without a fully-populated dispatch
// context (AgentIdentity + TaskRef). Every LLM call site must stamp
// identity on ctx via identity.WithIdentity / WithTask before
// invoking the gateway — this error surfaces that contract breach
// loudly rather than silently attributing the call to the wrong
// bucket.
var ErrDispatchIdentityMissing = errors.New("gateway: dispatch context missing identity or task")

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
	_ providers.ProviderAdapter       = (*GatewayProvider)(nil)
	_ providers.StreamHandlerProvider = (*GatewayProvider)(nil)
)

// --- Passthrough methods (no admission required) ---

func (p *GatewayProvider) Name() string                           { return p.inner.Name() }
func (p *GatewayProvider) SupportedModels() []providers.ModelInfo { return p.inner.SupportedModels() }
func (p *GatewayProvider) CountTokens(msgs []providers.Message) (int, error) {
	return p.inner.CountTokens(msgs)
}
func (p *GatewayProvider) MaxContextTokens(model string) int     { return p.inner.MaxContextTokens(model) }
func (p *GatewayProvider) HealthCheck(ctx context.Context) error { return p.inner.HealthCheck(ctx) }

func (p *GatewayProvider) RequestTimeout() time.Duration {
	if reporter, ok := p.inner.(interface{ RequestTimeout() time.Duration }); ok {
		return reporter.RequestTimeout()
	}
	return 0
}

// --- Gated methods (admission required) ---

// Complete performs a non-streaming completion through the gateway.
func (p *GatewayProvider) Complete(ctx context.Context, req *providers.CompletionRequest) (*providers.CompletionResponse, error) {
	id, task, err := requireDispatch(ctx)
	if err != nil {
		return nil, err
	}
	model := identity.ModelID(req.Model)

	admitStart := time.Now()
	p.gateway.logger.Info("DEBUG: gateway_admit_start",
		"gateway", p.gateway.config.Name,
		"priority", p.priority,
		"inflight", p.gateway.inflight.Load(),
		"max_inflight", p.gateway.maxInflight,
		"model", req.Model,
		"agent", id.Panel(),
		"task", string(task.UID()),
		"skip_skills", req.SkipProviderSkills)
	if err := p.gateway.Admit(ctx, p.priority); err != nil {
		p.gateway.logger.Info("DEBUG: gateway_admit_failed",
			"gateway", p.gateway.config.Name,
			"elapsed_ms", time.Since(admitStart).Milliseconds(),
			"error", err)
		return nil, err
	}
	p.gateway.logger.Info("DEBUG: gateway_admit_ok",
		"gateway", p.gateway.config.Name,
		"elapsed_ms", time.Since(admitStart).Milliseconds())

	if hook := p.gateway.eventHook; hook != nil {
		hook.OnRequest(ctx, id, task, model)
	}

	ctx = p.injectRetryObserver(ctx)
	completeStart := time.Now()
	resp, err := p.inner.Complete(ctx, req)
	elapsed := time.Since(completeStart)
	p.gateway.logger.Info("DEBUG: gateway_complete_done",
		"gateway", p.gateway.config.Name,
		"elapsed_ms", elapsed.Milliseconds(),
		"error", err)
	p.gateway.Release(err)
	p.detect429(err)

	if hook := p.gateway.eventHook; hook != nil {
		if err != nil {
			hook.OnError(ctx, id, task, model, err, elapsed)
		} else {
			hook.OnResponse(ctx, id, task, identity.ModelID(resp.Model), &resp.Usage, elapsed)
		}
	}

	return resp, err
}

// Stream performs a channel-based streaming completion through the gateway.
// The concurrency slot is held until the returned channel is drained.
func (p *GatewayProvider) Stream(ctx context.Context, req *providers.CompletionRequest) (<-chan *providers.StreamChunk, error) {
	id, task, err := requireDispatch(ctx)
	if err != nil {
		return nil, err
	}
	model := identity.ModelID(req.Model)

	if err := p.gateway.Admit(ctx, p.priority); err != nil {
		return nil, err
	}

	if hook := p.gateway.eventHook; hook != nil {
		hook.OnRequest(ctx, id, task, model)
	}

	ctx = p.injectRetryObserver(ctx)
	streamStart := time.Now()
	ch, err := p.inner.Stream(ctx, req)
	if err != nil {
		p.gateway.Release(err)
		p.detect429(err)
		if hook := p.gateway.eventHook; hook != nil {
			hook.OnError(ctx, id, task, model, err, time.Since(streamStart))
		}
		return nil, err
	}

	// Wrap the channel to release the slot when the source closes. The
	// forwarder runs in a goroutine tracked by ProviderGateway.streamWG so
	// Stop() can wait for it to drain. Panics are recovered in-place —
	// otherwise an inner provider panic would strand the consumer on a
	// never-closed output channel and the gateway slot would never release.
	out := make(chan *providers.StreamChunk, cap(ch))
	p.gateway.streamWG.Add(1)
	go p.runStreamForwarder(ch, out, p.gateway.eventHook, id, task, model, streamStart)
	return out, nil
}

// runStreamForwarder wraps forwardStreamChunks with WaitGroup tracking and
// panic recovery.
func (p *GatewayProvider) runStreamForwarder(
	src <-chan *providers.StreamChunk,
	dst chan<- *providers.StreamChunk,
	hook providers.LLMProviderEventHook,
	id *identity.AgentIdentity,
	task *identity.TaskRef,
	model identity.ModelID,
	streamStart time.Time,
) {
	defer p.gateway.streamWG.Done()
	defer func() {
		if r := recover(); r != nil {
			slog.Default().Error("gateway stream forwarder: panic recovered",
				"gateway", p.gateway.config.Name,
				"model", string(model),
				"agent", id.Panel(),
				"task", string(task.UID()),
				"panic", r,
				"stack", string(debug.Stack()))
			safeClose(dst)
			p.gateway.Release(nil)
		}
	}()
	p.forwardStreamChunks(src, dst, hook, id, task, model, streamStart)
}

// safeClose closes a channel at most once, ignoring a close-of-closed panic.
func safeClose(ch chan<- *providers.StreamChunk) {
	defer func() { _ = recover() }()
	close(ch)
}

// StreamWithHandler performs a handler-based streaming completion through the
// gateway. Requires the inner provider to implement StreamHandlerProvider.
func (p *GatewayProvider) StreamWithHandler(ctx context.Context, req *providers.StreamRequest, handler providers.StreamHandler) error {
	shp, ok := p.inner.(providers.StreamHandlerProvider)
	if !ok {
		return providers.WrapError(providers.ProviderTypeGoogle, "stream_with_handler",
			providers.ErrModelNotSupported)
	}

	id, task, err := requireDispatch(ctx)
	if err != nil {
		return err
	}
	model := identity.ModelID(req.Model)

	if err := p.gateway.Admit(ctx, p.priority); err != nil {
		return err
	}

	hook := p.gateway.eventHook
	if hook != nil {
		hook.OnRequest(ctx, id, task, model)
	}

	ctx = p.injectRetryObserver(ctx)
	streamStart := time.Now()
	wrappedHandler := p.wrapStreamHandler(handler, hook, ctx, id, task, model, streamStart)
	err = shp.StreamWithHandler(ctx, req, wrappedHandler)
	p.gateway.Release(err)
	p.detect429(err)

	if err != nil && hook != nil {
		hook.OnError(ctx, id, task, model, err, time.Since(streamStart))
	}

	return err
}

// Inner returns the underlying provider for type-specific access.
func (p *GatewayProvider) Inner() providers.ProviderAdapter {
	return p.inner
}

// GatewayMetrics returns live gateway metrics for autoscaling and admission
// decisions that need provider-side headroom.
func (p *GatewayProvider) GatewayMetrics() MetricsSnapshot {
	if p == nil || p.gateway == nil {
		return MetricsSnapshot{}
	}
	return p.gateway.Metrics()
}

// GatewayCapacity returns the wrapped gateway's current static capacity limits.
func (p *GatewayProvider) GatewayCapacity() CapacitySnapshot {
	if p == nil || p.gateway == nil {
		return CapacitySnapshot{}
	}
	return p.gateway.CapacitySnapshot()
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

// forwardStreamChunks drains src into dst, releases the gateway slot, and
// emits an OnResponse event when the end chunk carries usage data.
func (p *GatewayProvider) forwardStreamChunks(
	src <-chan *providers.StreamChunk,
	dst chan<- *providers.StreamChunk,
	hook providers.LLMProviderEventHook,
	id *identity.AgentIdentity,
	task *identity.TaskRef,
	model identity.ModelID,
	streamStart time.Time,
) {
	defer func() {
		close(dst)
		p.gateway.Release(nil)
	}()

	for chunk := range src {
		if hook != nil && chunk != nil && chunk.Type == providers.ChunkTypeEnd && chunk.Usage != nil {
			hook.OnResponse(context.Background(), id, task, model, chunk.Usage, time.Since(streamStart))
		}
		dst <- chunk
	}
}

// wrapStreamHandler wraps a StreamHandler to capture the end-chunk usage and
// emit an OnResponse event. Returns the original handler when hook is nil.
func (p *GatewayProvider) wrapStreamHandler(
	inner providers.StreamHandler,
	hook providers.LLMProviderEventHook,
	ctx context.Context,
	id *identity.AgentIdentity,
	task *identity.TaskRef,
	model identity.ModelID,
	streamStart time.Time,
) providers.StreamHandler {
	if hook == nil {
		return inner
	}
	return func(chunk *providers.StreamChunk) error {
		err := inner(chunk)
		if chunk != nil && chunk.Type == providers.ChunkTypeEnd && chunk.Usage != nil {
			hook.OnResponse(ctx, id, task, model, chunk.Usage, time.Since(streamStart))
		}
		return err
	}
}

// requireDispatch extracts the typed AgentIdentity + TaskRef from ctx.
// Wraps identity.RequireDispatch to surface the gateway-scoped error
// with context for log correlation.
func requireDispatch(ctx context.Context) (*identity.AgentIdentity, *identity.TaskRef, error) {
	id, task, err := identity.RequireDispatch(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrDispatchIdentityMissing, err)
	}
	return id, task, nil
}
