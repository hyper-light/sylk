package providers

import (
	"context"

	"github.com/adalundhe/sylk/core/activity"
)

// Instrument wraps any ProviderAdapter with Activity Fabric chokepoint
// instrumentation. Every Complete and Stream call emits a paired
// llm_request_emitted (in-flight) → llm_response_completed (resolved)
// activity carrying model, message count, token usage, finish reason,
// and stop_reason. Streaming chunks are deliberately NOT instrumented
// here — that would land in the Atomic-resolution ring buffer if
// needed and is opt-in via a separate streaming wrapper to keep the
// hot per-chunk path free of fabric writes.
//
// Idempotent: wrapping an already-instrumented provider returns the
// same instance.
func Instrument(p ProviderAdapter) ProviderAdapter {
	if p == nil {
		return nil
	}
	if _, already := p.(*instrumentedProvider); already {
		return p
	}
	return &instrumentedProvider{wrapped: p}
}

// UnderlyingProvider strips any chain of fabric instrumentation
// wrappers and returns the innermost ProviderAdapter. Callers that
// type-assert for capability detection (e.g.,
// NativeWebSearchEvidenceProvider) should call this first so the
// wrapper is transparent.
func UnderlyingProvider(p ProviderAdapter) ProviderAdapter {
	for {
		w, ok := p.(*instrumentedProvider)
		if !ok || w == nil {
			return p
		}
		p = w.wrapped
	}
}

type instrumentedProvider struct {
	wrapped ProviderAdapter
}

func (p *instrumentedProvider) Name() string {
	return p.wrapped.Name()
}

func (p *instrumentedProvider) SupportedModels() []ModelInfo {
	return p.wrapped.SupportedModels()
}

func (p *instrumentedProvider) Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) {
	span := p.beginSpan(ctx, req, "complete")
	defer func() { span.End() }()

	resp, err := p.wrapped.Complete(span.Context(), req)
	if err != nil {
		span.EndWithError(err)
		return nil, err
	}
	if resp != nil {
		span.SetAttribute("response_model", resp.Model)
		span.SetAttribute("stop_reason", string(resp.StopReason))
		span.SetAttribute("input_tokens", resp.Usage.InputTokens)
		span.SetAttribute("output_tokens", resp.Usage.OutputTokens)
		span.SetAttribute("total_tokens", resp.Usage.TotalTokens)
		span.SetAttribute("reasoning_tokens", resp.Usage.ReasoningTokens)
		span.SetAttribute("cache_read_tokens", resp.Usage.CacheReadTokens)
		span.SetAttribute("cache_write_tokens", resp.Usage.CacheWriteTokens)
		span.SetAttribute("tool_calls", len(resp.ToolCalls))
	}
	return resp, nil
}

func (p *instrumentedProvider) Stream(ctx context.Context, req *CompletionRequest) (<-chan *StreamChunk, error) {
	span := p.beginSpan(ctx, req, "stream")
	in, err := p.wrapped.Stream(span.Context(), req)
	if err != nil {
		// End immediately on the synchronous setup failure so the
		// failed setup is captured as a finished activity.
		span.EndWithError(err)
		return nil, err
	}
	out := make(chan *StreamChunk, 16)
	go func() {
		defer close(out)
		defer span.End()
		var (
			chunkCount int
			usage      Usage
			stop       StopReason
		)
		for chunk := range in {
			chunkCount++
			if chunk != nil {
				if chunk.Usage != nil {
					usage = *chunk.Usage
				}
				if chunk.StopReason != "" {
					stop = chunk.StopReason
				}
			}
			out <- chunk
		}
		span.SetAttribute("chunk_count", chunkCount)
		span.SetAttribute("stop_reason", string(stop))
		span.SetAttribute("input_tokens", usage.InputTokens)
		span.SetAttribute("output_tokens", usage.OutputTokens)
		span.SetAttribute("total_tokens", usage.TotalTokens)
		span.SetAttribute("reasoning_tokens", usage.ReasoningTokens)
		span.SetAttribute("cache_read_tokens", usage.CacheReadTokens)
		span.SetAttribute("cache_write_tokens", usage.CacheWriteTokens)
	}()
	return out, nil
}

func (p *instrumentedProvider) CountTokens(messages []Message) (int, error) {
	return p.wrapped.CountTokens(messages)
}

func (p *instrumentedProvider) MaxContextTokens(model string) int {
	return p.wrapped.MaxContextTokens(model)
}

func (p *instrumentedProvider) HealthCheck(ctx context.Context) error {
	return p.wrapped.HealthCheck(ctx)
}

func (p *instrumentedProvider) beginSpan(ctx context.Context, req *CompletionRequest, mode string) *activity.Span {
	subject := activity.Subject{
		Domain: p.wrapped.Name(),
	}
	if req != nil {
		subject.TargetArtifact = req.Model
	}
	span := activity.StartSpan(ctx, activity.ActionLLMRequestEmitted, subject)
	span.SetAttribute("provider", p.wrapped.Name())
	span.SetAttribute("mode", mode)
	if req != nil {
		span.SetAttribute("model", req.Model)
		span.SetAttribute("messages", len(req.Messages))
		span.SetAttribute("tools", len(req.Tools))
		span.SetAttribute("max_tokens", req.MaxTokens)
		if req.ThinkingBudget > 0 {
			span.SetAttribute("thinking_budget", req.ThinkingBudget)
		}
	}
	return span
}

// Forward the optional capability interfaces. NativeWebSearchEvidenceProvider
// detection is a common type assertion; forward it transparently.
func (p *instrumentedProvider) SupportsNativeWebSearchEvidence() bool {
	if forwarder, ok := p.wrapped.(NativeWebSearchEvidenceProvider); ok {
		return forwarder.SupportsNativeWebSearchEvidence()
	}
	return false
}

func (p *instrumentedProvider) ValidateConfig() error {
	if v, ok := p.wrapped.(ProviderValidator); ok {
		return v.ValidateConfig()
	}
	return nil
}

func (p *instrumentedProvider) SupportsModel(model string) bool {
	if s, ok := p.wrapped.(ProviderModelSupporter); ok {
		return s.SupportsModel(model)
	}
	return false
}

func (p *instrumentedProvider) Close() error {
	if c, ok := p.wrapped.(ProviderCloser); ok {
		return c.Close()
	}
	return nil
}

func (p *instrumentedProvider) StreamWithHandler(ctx context.Context, req *StreamRequest, handler StreamHandler) error {
	span := p.beginSpan(ctx, req, "stream_with_handler")
	defer func() { span.End() }()

	if h, ok := p.wrapped.(StreamHandlerProvider); ok {
		err := h.StreamWithHandler(span.Context(), req, handler)
		if err != nil {
			span.EndWithError(err)
			return err
		}
		return nil
	}
	// Fall back to Stream-based delivery for providers that don't
	// implement StreamWithHandler natively.
	out, err := p.Stream(span.Context(), req)
	if err != nil {
		span.EndWithError(err)
		return err
	}
	for chunk := range out {
		if handler == nil {
			continue
		}
		if hErr := handler(chunk); hErr != nil {
			span.EndWithError(hErr)
			return hErr
		}
	}
	return nil
}

var _ ProviderAdapter = (*instrumentedProvider)(nil)
