package providers

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/google/uuid"
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

	trace := recordLLMDispatchStart(ctx, p.wrapped.Name(), req, "complete")

	resp, err := p.completeViaProviderGateway(span.Context(), req, trace)
	if err != nil {
		span.EndWithError(err)
		recordLLMDispatchEnd(ctx, p.wrapped.Name(), req, "complete", trace, nil, err)
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
	recordLLMDispatchEnd(ctx, p.wrapped.Name(), req, "complete", trace, resp, nil)
	return resp, nil
}

func (p *instrumentedProvider) Stream(ctx context.Context, req *CompletionRequest) (<-chan *StreamChunk, error) {
	span := p.beginSpan(ctx, req, "stream")
	trace := recordLLMDispatchStart(ctx, p.wrapped.Name(), req, "stream")

	resp, err := p.streamViaProviderGateway(span.Context(), req, trace)
	if err != nil {
		span.EndWithError(err)
		recordLLMDispatchEnd(ctx, p.wrapped.Name(), req, "stream", trace, nil, err)
		span.End()
		return nil, err
	}
	if resp == nil {
		err := errors.New("provider gateway returned nil streaming response")
		span.EndWithError(err)
		recordLLMDispatchEnd(ctx, p.wrapped.Name(), req, "stream", trace, nil, err)
		span.End()
		return nil, err
	}
	chunks := providerGatewayStreamChunks(resp)
	out := make(chan *StreamChunk, len(chunks))
	for _, chunk := range chunks {
		out <- chunk
	}
	close(out)
	span.SetAttribute("chunk_count", len(chunks))
	span.SetAttribute("stop_reason", string(resp.StopReason))
	span.SetAttribute("input_tokens", resp.Usage.InputTokens)
	span.SetAttribute("output_tokens", resp.Usage.OutputTokens)
	span.SetAttribute("total_tokens", resp.Usage.TotalTokens)
	span.SetAttribute("reasoning_tokens", resp.Usage.ReasoningTokens)
	span.SetAttribute("cache_read_tokens", resp.Usage.CacheReadTokens)
	span.SetAttribute("cache_write_tokens", resp.Usage.CacheWriteTokens)
	recordLLMDispatchEnd(ctx, p.wrapped.Name(), req, "stream", trace, resp, nil)
	span.End()
	return out, nil
}

func (p *instrumentedProvider) CountTokens(messages []Message) (int, error) {
	return p.wrapped.CountTokens(messages)
}

func (p *instrumentedProvider) completeViaProviderGateway(ctx context.Context, req *CompletionRequest, trace llmDispatchTrace) (*CompletionResponse, error) {
	data, resp, err := p.providerGatewayCallOrDirect(ctx, req, "complete", trace)
	if err != nil {
		return nil, err
	}
	if execErr := providerGatewayExecutionError(data); execErr != nil {
		return nil, execErr
	}
	if resp != nil {
		return resp, nil
	}
	return providerGatewayResponseFromData(data), nil
}

func (p *instrumentedProvider) streamViaProviderGateway(ctx context.Context, req *CompletionRequest, trace llmDispatchTrace) (*CompletionResponse, error) {
	data, resp, err := p.providerGatewayCallOrDirect(ctx, req, "stream", trace)
	if err != nil {
		return nil, err
	}
	if execErr := providerGatewayExecutionError(data); execErr != nil {
		return nil, execErr
	}
	if resp != nil {
		return resp, nil
	}
	return providerGatewayResponseFromData(data), nil
}

func (p *instrumentedProvider) providerGatewayCallOrDirect(ctx context.Context, req *CompletionRequest, mode string, trace llmDispatchTrace) (claims.ProviderGatewayCallArtifactData, *Response, error) {
	data, resp, err := invokeProviderGatewayServiceClaim(ctx, p.wrapped, req, mode, trace)
	if !errors.Is(err, errProviderGatewayServiceUnavailable) {
		return data, resp, err
	}
	return p.directProviderGatewayCall(ctx, req, mode, trace)
}

func (p *instrumentedProvider) directProviderGatewayCall(ctx context.Context, req *CompletionRequest, mode string, trace llmDispatchTrace) (claims.ProviderGatewayCallArtifactData, *Response, error) {
	backend := NewClaimsGatewayBackend(ClaimsGatewayBackendConfig{
		Provider:             p.wrapped,
		Request:              req,
		ResponseSummaryLimit: llmDispatchContentSummaryCap,
		PartialSummaryLimit:  defaultProviderGatewayPartialLimit(),
	})
	data := providerGatewayArtifactData(p.wrapped.Name(), req, mode, trace, nil, nil)
	out, resp, err := backend.ExecuteProviderGatewayCall(ctx, claims.ProviderGatewayCallRequest{
		Call:      claims.ExpectedToolCall{ID: trace.dispatchID, Tool: data.Operation, Arguments: providerGatewayRequestArguments(req, data)},
		Requested: data,
	})
	if err != nil {
		return out, resp, err
	}
	return out, resp, nil
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
	out, err := p.Stream(ctx, req)
	if err != nil {
		return err
	}
	for chunk := range out {
		if handler == nil {
			continue
		}
		if hErr := handler(chunk); hErr != nil {
			return hErr
		}
	}
	return nil
}

var _ ProviderAdapter = (*instrumentedProvider)(nil)

// llmDispatchContentSummaryCap caps the per-dispatch streamed-content
// snapshot recorded into the dispatch-end artifact. Stream chunks
// themselves bypass the board (per-chunk artifacts would burn the
// board lock — see CLAIMS.md slice on stream cardinality), but the
// final summary on the end artifact gives the chat panel enough text
// to render the row's preview without re-fetching from the testament.
const llmDispatchContentSummaryCap = 8 * 1024

// recordLLMDispatchStart appends an llm_dispatch:started artifact to
// the claims TestamentAccumulator on ctx. Returns the dispatch ID +
// start timestamp to thread into the matching :completed artifact.
//
// LLM dispatches are evidence of the agent's work on a parent claim —
// they belong on the testament as artifacts. Each (start, end) pair
// is keyed by dispatch_id so the chat panel can match them to update
// a single row from "in flight" to "completed".
// llmDispatchTrace bundles the IDs that pair the started artifact with
// its eventual completed artifact. Returned by recordLLMDispatchStart
// and passed verbatim to recordLLMDispatchEnd.
type llmDispatchTrace struct {
	startedArtifactID string
	dispatchID        string
	started           time.Time
}

func recordLLMDispatchStart(ctx context.Context, providerName string, req *Request, mode string) llmDispatchTrace {
	acc := claims.AccumulatorFromContext(ctx)
	if acc == nil {
		return llmDispatchTrace{}
	}
	dispatchID := uuid.NewString()
	startedID := uuid.NewString()
	start := time.Now().UTC()
	model := ""
	messages := 0
	tools := 0
	maxTokens := 0
	if req != nil {
		model = strings.TrimSpace(req.Model)
		messages = len(req.Messages)
		tools = len(req.Tools)
		maxTokens = req.MaxTokens
	}
	acc.RecordArtifact(&claims.Artifact{
		ID:        startedID,
		Kind:      "llm_started",
		Reference: model,
		Metadata: map[string]any{
			"dispatch_id": dispatchID,
			"provider":    providerName,
			"model":       model,
			"mode":        mode,
			"messages":    messages,
			"tools":       tools,
			"max_tokens":  maxTokens,
			"started_at":  start.Format(time.RFC3339Nano),
		},
		Ephemeral: true,
	})
	return llmDispatchTrace{startedArtifactID: startedID, dispatchID: dispatchID, started: start}
}

// recordLLMDispatchEnd appends an llm_completed artifact paired with
// its matching llm_started via Relation{completes}. Carries Outcome
// (success/failure/timeout/cancelled) plus token + response detail.
func recordLLMDispatchEnd(ctx context.Context, providerName string, req *Request, mode string, trace llmDispatchTrace, resp *Response, dispatchErr error) {
	acc := claims.AccumulatorFromContext(ctx)
	if acc == nil || trace.dispatchID == "" {
		return
	}
	now := time.Now().UTC()
	outcome := "success"
	switch {
	case dispatchErr == nil:
		outcome = "success"
	case errors.Is(dispatchErr, context.Canceled):
		outcome = "cancelled"
	case errors.Is(dispatchErr, context.DeadlineExceeded):
		outcome = "timeout"
	default:
		outcome = "failure"
	}
	model := ""
	if req != nil {
		model = strings.TrimSpace(req.Model)
	}
	metadata := map[string]any{
		"outcome":     outcome,
		"dispatch_id": trace.dispatchID,
		"provider":    providerName,
		"model":       model,
		"mode":        mode,
		"ended_at":    now.Format(time.RFC3339Nano),
		"duration_ms": now.Sub(trace.started).Milliseconds(),
	}
	if dispatchErr != nil {
		metadata["error"] = dispatchErr.Error()
	}
	if resp != nil {
		metadata["response_summary"] = truncateForDispatchSummary(resp.Content)
		metadata["stop_reason"] = string(resp.StopReason)
		metadata["input_tokens"] = resp.Usage.InputTokens
		metadata["output_tokens"] = resp.Usage.OutputTokens
		metadata["total_tokens"] = resp.Usage.TotalTokens
		metadata["reasoning_tokens"] = resp.Usage.ReasoningTokens
		metadata["cache_read_tokens"] = resp.Usage.CacheReadTokens
		metadata["cache_write_tokens"] = resp.Usage.CacheWriteTokens
		metadata["tool_calls"] = len(resp.ToolCalls)
	}
	artifact := &claims.Artifact{
		ID:        uuid.NewString(),
		Kind:      "llm_completed",
		Reference: model,
		Metadata:  metadata,
		Ephemeral: true,
	}
	if trace.startedArtifactID != "" {
		artifact.Relations = []claims.Relation{
			{
				Related:      trace.startedArtifactID,
				RelatedType:  claims.RelatedTypeArtifact,
				Relationship: claims.RelationshipCompletes,
			},
		}
	}
	acc.RecordArtifact(artifact)
}

func truncateForDispatchSummary(s string) string {
	if len(s) <= llmDispatchContentSummaryCap {
		return s
	}
	return s[:llmDispatchContentSummaryCap] + "...(truncated)"
}

// errFromChunk extracts an error from a ChunkTypeError chunk. The
// chunk's Text carries the error message; we wrap it as a real error
// so the dispatch-end recorder sees phase=failed and the bridge
// surfaces the failure on the row.
func errFromChunk(chunk *StreamChunk) error {
	if chunk == nil {
		return nil
	}
	msg := strings.TrimSpace(chunk.Text)
	if msg == "" {
		msg = "stream error"
	}
	return &streamErrorWrapper{message: msg}
}

type streamErrorWrapper struct {
	message string
}

func (e *streamErrorWrapper) Error() string { return e.message }
