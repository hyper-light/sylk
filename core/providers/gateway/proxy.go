package gateway

import (
	"context"
	"time"

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
	admitStart := time.Now()
	p.gateway.logger.Info("DEBUG: gateway_admit_start",
		"gateway", p.gateway.config.Name,
		"priority", p.priority,
		"inflight", p.gateway.inflight.Load(),
		"max_inflight", p.gateway.maxInflight,
		"model", req.Model,
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

	sessionID, agentID := extractRequestIdentity(req)
	if hook := p.gateway.eventHook; hook != nil {
		hook.OnRequest(sessionID, agentID, req.Model, 0)
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
			hook.OnError(sessionID, agentID, req.Model, err)
		} else {
			hook.OnResponse(sessionID, agentID, resp.Model, &resp.Usage, elapsed)
		}
	}

	return resp, err
}

// Stream performs a channel-based streaming completion through the gateway.
// The concurrency slot is held until the returned channel is drained.
func (p *GatewayProvider) Stream(ctx context.Context, req *providers.CompletionRequest) (<-chan *providers.StreamChunk, error) {
	if err := p.gateway.Admit(ctx, p.priority); err != nil {
		return nil, err
	}

	sessionID, agentID := extractRequestIdentity(req)
	if hook := p.gateway.eventHook; hook != nil {
		hook.OnRequest(sessionID, agentID, req.Model, 0)
	}

	ctx = p.injectRetryObserver(ctx)
	streamStart := time.Now()
	ch, err := p.inner.Stream(ctx, req)
	if err != nil {
		p.gateway.Release(err)
		p.detect429(err)
		if hook := p.gateway.eventHook; hook != nil {
			hook.OnError(sessionID, agentID, req.Model, err)
		}
		return nil, err
	}

	// Wrap the channel to release the slot when the source closes.
	out := make(chan *providers.StreamChunk, cap(ch))
	go p.forwardStreamChunks(ch, out, p.gateway.eventHook, sessionID, agentID, req.Model, streamStart)
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

	sessionID, agentID := extractRequestIdentity(req)
	hook := p.gateway.eventHook
	if hook != nil {
		hook.OnRequest(sessionID, agentID, req.Model, 0)
	}

	ctx = p.injectRetryObserver(ctx)
	streamStart := time.Now()
	wrappedHandler := p.wrapStreamHandler(handler, hook, sessionID, agentID, req.Model, streamStart)
	err := shp.StreamWithHandler(ctx, req, wrappedHandler)
	p.gateway.Release(err)
	p.detect429(err)

	if err != nil && hook != nil {
		hook.OnError(sessionID, agentID, req.Model, err)
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
	sessionID, agentID, model string,
	streamStart time.Time,
) {
	defer func() {
		close(dst)
		p.gateway.Release(nil)
	}()

	for chunk := range src {
		if hook != nil && chunk != nil && chunk.Type == providers.ChunkTypeEnd && chunk.Usage != nil {
			hook.OnResponse(sessionID, agentID, model, chunk.Usage, time.Since(streamStart))
		}
		dst <- chunk
	}
}

// wrapStreamHandler wraps a StreamHandler to capture the end-chunk usage and
// emit an OnResponse event. Returns the original handler when hook is nil.
func (p *GatewayProvider) wrapStreamHandler(
	inner providers.StreamHandler,
	hook providers.LLMProviderEventHook,
	sessionID, agentID, model string,
	streamStart time.Time,
) providers.StreamHandler {
	if hook == nil {
		return inner
	}
	return func(chunk *providers.StreamChunk) error {
		err := inner(chunk)
		if chunk != nil && chunk.Type == providers.ChunkTypeEnd && chunk.Usage != nil {
			hook.OnResponse(sessionID, agentID, model, chunk.Usage, time.Since(streamStart))
		}
		return err
	}
}

// extractRequestIdentity pulls session_id and agent_id from the request
// metadata map, returning empty strings when absent.
func extractRequestIdentity(req *providers.Request) (sessionID, agentID string) {
	if req == nil || req.Metadata == nil {
		return "", ""
	}
	if v, ok := req.Metadata["session_id"].(string); ok {
		sessionID = v
	}
	if v, ok := req.Metadata["agent_id"].(string); ok {
		agentID = v
	}
	return sessionID, agentID
}
