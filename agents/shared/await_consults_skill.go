package shared

import (
	"context"
	"encoding/json"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/providers"
)

// turnContextKey carries the active LLM turn state into the tool-call
// stack so the await_consults handler can build a TurnSnapshot. The
// agent's tool dispatch loop stamps this before invoking each tool
// handler — see WithTurnContext / TurnFromContext.
type turnContextKey struct{}

// TurnContext is the active LLM turn snapshot the agent dispatcher
// stamps into ctx for tool handlers to read. AwaitConsultsOrYield
// uses it to construct the TurnSnapshot persisted to the
// continuation claim.
type TurnContext struct {
	// Request is the live providers.Request for the current LLM
	// turn. The continuation snapshot deep-copies it so the post-
	// yield mutation of req.Messages doesn't poison the snapshot.
	Request *providers.Request

	// CorrelationID is the steering ledger correlation (or
	// equivalent per-request ID) used for log/telemetry stitching
	// across yield + resume.
	CorrelationID string

	// AgentID, SessionID identify the issuing agent. These are
	// duplicated from the agent's identity for ergonomic access in
	// the await handler (no need to re-parse from ctx-stamped
	// identity).
	AgentID   string
	SessionID string
}

// WithTurnContext returns ctx decorated with the active TurnContext.
// Each agent's request handler MUST call this before invoking
// ExecuteTurnLoop / dispatching tools, so the await_consults handler
// can read it. Returns ctx unchanged when tc is nil.
func WithTurnContext(ctx context.Context, tc *TurnContext) context.Context {
	if tc == nil {
		return ctx
	}
	return context.WithValue(ctx, turnContextKey{}, tc)
}

// TurnFromContext returns the active TurnContext, or nil when none.
func TurnFromContext(ctx context.Context) *TurnContext {
	if ctx == nil {
		return nil
	}
	tc, _ := ctx.Value(turnContextKey{}).(*TurnContext)
	return tc
}

// continuationStoreKey carries the per-agent ContinuationStore into
// the tool-call stack so the await_consults handler can invoke
// AwaitConsultsOrYield without per-skill plumbing.
type continuationStoreKey struct{}

// WithContinuationStore stamps the per-agent ContinuationStore into
// ctx. Each agent does this once at request handling time (or
// globally via the per-agent dispatcher), so the await_consults
// skill resolves it without explicit plumbing.
func WithContinuationStore(ctx context.Context, store *ContinuationStore) context.Context {
	if store == nil {
		return ctx
	}
	return context.WithValue(ctx, continuationStoreKey{}, store)
}

// ContinuationStoreFromContext returns the active store, or nil.
func ContinuationStoreFromContext(ctx context.Context) *ContinuationStore {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(continuationStoreKey{}).(*ContinuationStore)
	return s
}

// AwaitConsultsResult is the JSON shape returned to the LLM as the
// resumed tool result for a yielded consult_peer call. The resume
// path injects this payload as the consult_peer call's tool result so
// the LLM sees a deterministic blocking-tool return value.
type AwaitConsultsResult struct {
	Results map[string]ConsultResolutionResult `json:"results"`
}

// ConsultResolutionResult is one entry in the results map.
type ConsultResolutionResult struct {
	Status    string          `json:"status"`
	Response  json.RawMessage `json:"response,omitempty"`
	Summary   string          `json:"summary,omitempty"`
	Error     string          `json:"error,omitempty"`
	Responder string          `json:"responder,omitempty"`
}

// FormatConsultResults is the canonical converter from a results map
// to the LLM-facing AwaitConsultsResult. Exported because the resume
// path also calls it when constructing the post-resume tool result.
func FormatConsultResults(results map[string]*AwaitedClaimResult) AwaitConsultsResult {
	out := AwaitConsultsResult{
		Results: make(map[string]ConsultResolutionResult, len(results)),
	}
	for id, delta := range results {
		if delta == nil {
			out.Results[id] = ConsultResolutionResult{Status: claims.ConsultStatusError, Error: "nil resolution"}
			continue
		}
		out.Results[id] = ConsultResolutionResult{
			Status:    delta.Status,
			Response:  delta.ResponsePayload,
			Summary:   delta.ResponseSummary,
			Error:     delta.ErrorMessage,
			Responder: delta.ResponderAgentID,
		}
	}
	return out
}

// snapshotAccumulator captures the active TestamentAccumulator (if
// any) into a serializable AccumulatorSnapshot for inclusion in the
// TurnSnapshot. Returns a zero-value snapshot when no accumulator is
// on ctx — the resume path treats that as "no in-flight accumulator
// to restore".
func snapshotAccumulator(ctx context.Context) AccumulatorSnapshot {
	acc := claims.AccumulatorFromContext(ctx)
	if acc == nil {
		return AccumulatorSnapshot{}
	}
	return AccumulatorSnapshot{
		AgentID:         acc.AgentID(),
		SessionID:       acc.SessionID(),
		ClaimID:         acc.ClaimID(),
		ResponseClaimID: acc.ResponseClaimID(),
		Started:         acc.Started(),
		Artifacts:       acc.Artifacts(),
		Notes:           acc.Notes(),
	}
}

// cloneProvidersRequest returns a deep enough copy of req that
// post-yield mutation of req.Messages does not affect the snapshot.
// We deep-copy slices the agent's tool dispatch is known to mutate
// (Messages, Tools); other slices/maps are aliased — they are
// effectively immutable in the post-yield path.
func cloneProvidersRequest(req *providers.Request) *providers.Request {
	if req == nil {
		return nil
	}
	out := *req
	if len(req.Messages) > 0 {
		out.Messages = make([]providers.Message, len(req.Messages))
		copy(out.Messages, req.Messages)
	}
	if len(req.Tools) > 0 {
		out.Tools = make([]providers.Tool, len(req.Tools))
		copy(out.Tools, req.Tools)
	}
	return &out
}

// activeToolCallFromContext bridges to the existing
// ActiveToolCallFromContext (defined in tool_timing.go) so the
// await_consults handler can self-identify without re-defining the
// context key. Returns (id, name) with empty strings when no active
// tool call is on ctx.
func activeToolCallFromContext(ctx context.Context) (id, name string) {
	active, ok := ActiveToolCallFromContext(ctx)
	if !ok {
		return "", ""
	}
	return active.ToolCallKey, active.ToolName
}
