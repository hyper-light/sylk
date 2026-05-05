// Package runtime executes MCP capability invocations through Sylk's
// governance, durability, and knowledge planes.
//
// Every tool call, resource read, and prompt invocation flows through this
// package. The flow:
//
//  1. compile invocation envelope (op id, server id, args, policy fingerprint),
//  2. append op_requested to durable journal (BEFORE any transport call),
//  3. if policy is approval-sensitive, request a Guardian grant and append
//     the grant resolution,
//  4. dispatch to the client.Session,
//  5. on result, append op_completed (or op_failed/op_cancelled) and persist
//     blobs, indexed content, and outcome signals.
//
// The runtime owns its goroutine scope: every async write (knowledge ingest,
// forest learning, blob fsync) happens in tracked goroutines so leak detection
// still applies.
package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/mcp"
	"github.com/adalundhe/sylk/core/mcp/client"
	"github.com/adalundhe/sylk/core/mcp/durable"
	"github.com/adalundhe/sylk/core/toolruntime"
	"github.com/google/uuid"
)

// SessionResolver returns the active client session for the given server id.
// The fabric implements it. Keeping it as an interface lets the runtime stay
// independent of the fabric's connection lifecycle code.
type SessionResolver interface {
	Resolve(serverID string) (*client.Session, error)
}

// PolicyResolver returns the toolruntime.ToolPolicy for a compiled MCP tool.
// The fabric implements it via the catalog/compiler pipeline.
type PolicyResolver interface {
	ResolvePolicy(canonicalName string) (toolruntime.ToolPolicy, string, bool)
}

// GuardianGate is the runtime's edge to Guardian. It mirrors the existing
// toolruntime.GuardianControlPlane interface so MCP runs through the exact
// same approval pipeline as local tools — no second approval system.
type GuardianGate interface {
	RequestGrant(ctx context.Context, req toolruntime.GuardianControlRequest) (*toolruntime.GuardianControlGrant, error)
}

// OutcomeListener receives end-of-life signals for outcome learning. The
// fabric wires this to Forest. Errors are logged but never block the runtime.
type OutcomeListener interface {
	OnOperation(op durable.Operation, finalEvent durable.Event)
}

// ContentIngester receives indexed content (resource text, prompt rendering,
// tool result summaries) for the knowledge substrate. Async; no return error
// surfaces to the caller because indexing is observational.
type ContentIngester interface {
	Ingest(ctx context.Context, payload IngestPayload)
}

// IngestPayload carries one piece of content from the MCP fabric to the
// knowledge plane. Empty fields are dropped by the receiver.
type IngestPayload struct {
	Source        string         // mcp://server/uri or mcp.tool result reference
	ServerID      string
	OperationID   string
	SessionID     string
	CapabilityKind string
	MIMEType      string
	Title         string
	Text          string
	Metadata      map[string]any
}

// Config configures the MCP runtime.
type Config struct {
	SessionID       string
	Journal         *durable.Journal
	Projection      *durable.Projection
	Blobs           *durable.BlobStore
	SessionResolver SessionResolver
	PolicyResolver  PolicyResolver
	Guardian        GuardianGate
	Outcomes        OutcomeListener
	Ingester        ContentIngester
	// Scope owns the runtime's async write goroutines. Required.
	Scope *concurrency.GoroutineScope
}

// Runtime is the executor. Goroutine-safe; one Runtime per session.
type Runtime struct {
	cfg Config

	// elicitations is the in-flight elicitation rendezvous map: opID ->
	// channel that the active call awaits. Ensures elicitation is restart
	// resilient — on boot, pending elicitations are recreated from the WAL.
	elicMu       sync.Mutex
	elicitations map[string]chan elicitationResult
}

type elicitationResult struct {
	args json.RawMessage
	err  error
}

// New constructs a Runtime.
func New(cfg Config) (*Runtime, error) {
	if cfg.Journal == nil {
		return nil, errors.New("mcp runtime: journal is required")
	}
	if cfg.Projection == nil {
		return nil, errors.New("mcp runtime: projection is required")
	}
	if cfg.SessionResolver == nil {
		return nil, errors.New("mcp runtime: session resolver is required")
	}
	if cfg.PolicyResolver == nil {
		return nil, errors.New("mcp runtime: policy resolver is required")
	}
	if cfg.Scope == nil {
		return nil, errors.New("mcp runtime: scope is required")
	}
	return &Runtime{cfg: cfg, elicitations: map[string]chan elicitationResult{}}, nil
}

// Invocation is a request to execute one MCP tool/resource/prompt.
type Invocation struct {
	CanonicalName  string
	ServerID       string
	CapabilityKind mcp.CapabilityKind
	RemoteName     string
	RemoteURI      string
	PromptName     string
	AgentID        string
	CorrelationID  string
	CapabilityScope string
	Arguments      json.RawMessage
}

// ExecuteTool runs a tool invocation end-to-end. The signature matches the
// compiler's ToolExecutor contract.
func (r *Runtime) ExecuteTool(ctx context.Context, serverID, canonicalName string, arguments json.RawMessage) (any, error) {
	policy, fingerprint, ok := r.cfg.PolicyResolver.ResolvePolicy(canonicalName)
	if !ok {
		return nil, fmt.Errorf("mcp runtime: %s: no policy", canonicalName)
	}
	inv := r.invocationFromContext(ctx, Invocation{
		CanonicalName:  canonicalName,
		ServerID:       serverID,
		CapabilityKind: mcp.CapabilityTool,
		Arguments:      arguments,
	})
	op := r.beginOperation(inv, fingerprint)
	if err := r.requestApproval(ctx, op, policy, fingerprint); err != nil {
		r.recordFailure(op, err)
		return nil, err
	}
	session, err := r.cfg.SessionResolver.Resolve(serverID)
	if err != nil {
		r.recordFailure(op, err)
		return nil, err
	}
	result, err := session.CallTool(ctx, sessionRemoteName(canonicalName, serverID), arguments)
	return r.completeToolCall(ctx, op, result, err)
}

// ReadResource runs a resource read end-to-end.
func (r *Runtime) ReadResource(ctx context.Context, serverID, uri string) (any, error) {
	canonical := canonicalResourceID(serverID, uri)
	op := r.beginOperation(Invocation{
		CanonicalName:  canonical,
		ServerID:       serverID,
		CapabilityKind: mcp.CapabilityResource,
		RemoteURI:      uri,
		AgentID:        agentIDFromContext(ctx),
		CorrelationID:  correlationIDFromContext(ctx),
		CapabilityScope: capabilityScopeFromContext(ctx),
	}, "")
	session, err := r.cfg.SessionResolver.Resolve(serverID)
	if err != nil {
		r.recordFailure(op, err)
		return nil, err
	}
	result, err := session.ReadResource(ctx, uri)
	if err != nil {
		r.recordFailure(op, err)
		return nil, err
	}
	r.recordResourceRead(op, result)
	return result, nil
}

// GetPrompt runs a prompt invocation end-to-end.
func (r *Runtime) GetPrompt(ctx context.Context, serverID, promptName string, args map[string]string) (any, error) {
	canonical := canonicalPromptID(serverID, promptName)
	op := r.beginOperation(Invocation{
		CanonicalName:  canonical,
		ServerID:       serverID,
		CapabilityKind: mcp.CapabilityPrompt,
		PromptName:     promptName,
		AgentID:        agentIDFromContext(ctx),
		CorrelationID:  correlationIDFromContext(ctx),
		CapabilityScope: capabilityScopeFromContext(ctx),
	}, "")
	session, err := r.cfg.SessionResolver.Resolve(serverID)
	if err != nil {
		r.recordFailure(op, err)
		return nil, err
	}
	result, err := session.GetPrompt(ctx, promptName, args)
	if err != nil {
		r.recordFailure(op, err)
		return nil, err
	}
	r.recordPromptInvoke(op, result)
	return result, nil
}

func (r *Runtime) invocationFromContext(ctx context.Context, base Invocation) Invocation {
	if base.AgentID == "" {
		base.AgentID = agentIDFromContext(ctx)
	}
	if base.CorrelationID == "" {
		base.CorrelationID = correlationIDFromContext(ctx)
	}
	if base.CapabilityScope == "" {
		base.CapabilityScope = capabilityScopeFromContext(ctx)
	}
	return base
}

// beginOperation appends op_requested before any transport work so the WAL
// has a record of the attempt even if the next syscall crashes.
func (r *Runtime) beginOperation(inv Invocation, fingerprint string) durable.Operation {
	op := durable.Operation{
		OperationID:       uuid.NewString(),
		SessionID:         r.cfg.SessionID,
		CorrelationID:     inv.CorrelationID,
		AgentID:           inv.AgentID,
		ServerID:          inv.ServerID,
		CapabilityScope:   inv.CapabilityScope,
		CapabilityKind:    string(inv.CapabilityKind),
		LocalName:         inv.CanonicalName,
		RemoteName:        firstNonEmpty(inv.RemoteName, inv.PromptName, inv.RemoteURI),
		ArgumentsHash:     hashArguments(inv.Arguments),
		PolicyFingerprint: fingerprint,
		Status:            durable.StatusRequested,
		StartedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	}
	ev := durable.Event{
		Kind:           durable.EventOpRequested,
		OperationID:    op.OperationID,
		SessionID:      op.SessionID,
		CorrelationID:  op.CorrelationID,
		AgentID:        op.AgentID,
		ServerID:       op.ServerID,
		CapabilityKind: op.CapabilityKind,
		LocalName:      op.LocalName,
		RemoteName:     op.RemoteName,
		ArgumentsHash:  op.ArgumentsHash,
		PolicyHash:     fingerprint,
	}
	_ = r.cfg.Journal.Append(ev)
	r.cfg.Projection.Apply(ev)
	return op
}

func (r *Runtime) requestApproval(
	ctx context.Context,
	op durable.Operation,
	policy toolruntime.ToolPolicy,
	manifestFingerprint string,
) error {
	if !policy.ApprovalSensitive && policy.Execution != toolruntime.ExecutionModeGuardian {
		return nil
	}
	if r.cfg.Guardian == nil {
		return fmt.Errorf("mcp runtime: %s requires guardian but none configured", op.LocalName)
	}
	r.recordApprovalRequested(op)
	req := toolruntime.GuardianControlRequest{
		AgentID:           op.AgentID,
		CorrelationID:     op.CorrelationID,
		CapabilityScope:   op.CapabilityScope,
		ToolName:          op.LocalName,
		ToolID:            op.OperationID,
		Arguments:         "",
		ArgumentsHash:     op.ArgumentsHash,
		Policy:            policy,
		PolicyFingerprint: manifestFingerprint,
		Timestamp:         time.Now().UTC(),
	}
	grant, err := r.cfg.Guardian.RequestGrant(ctx, req)
	if err != nil {
		r.recordApprovalDenied(op, err)
		return err
	}
	if grant == nil || !grant.Approved {
		denyErr := fmt.Errorf("guardian denied %s", op.LocalName)
		r.recordApprovalDenied(op, denyErr)
		return denyErr
	}
	r.recordApprovalGranted(op)
	return nil
}

func (r *Runtime) recordApprovalRequested(op durable.Operation) {
	ev := durable.Event{
		Kind:           durable.EventApprovalRequested,
		OperationID:    op.OperationID,
		SessionID:      op.SessionID,
		CorrelationID:  op.CorrelationID,
		AgentID:        op.AgentID,
		ServerID:       op.ServerID,
		CapabilityKind: op.CapabilityKind,
		LocalName:      op.LocalName,
		RemoteName:     op.RemoteName,
		ArgumentsHash:  op.ArgumentsHash,
	}
	_ = r.cfg.Journal.Append(ev)
	r.cfg.Projection.Apply(ev)
}

func (r *Runtime) recordApprovalGranted(op durable.Operation) {
	ev := durable.Event{
		Kind:        durable.EventApprovalResolved,
		OperationID: op.OperationID,
		SessionID:   op.SessionID,
	}
	_ = r.cfg.Journal.Append(ev)
	r.cfg.Projection.Apply(ev)
}

func (r *Runtime) recordApprovalDenied(op durable.Operation, denyErr error) {
	ev := durable.Event{
		Kind:         durable.EventApprovalResolved,
		OperationID:  op.OperationID,
		SessionID:    op.SessionID,
		ErrorMessage: denyErr.Error(),
	}
	_ = r.cfg.Journal.Append(ev)
	r.cfg.Projection.Apply(ev)
}

// completeToolCall appends op_completed (or op_failed) and dispatches
// outcomes/ingestion through tracked goroutines so the call's hot path
// never blocks on side-effects.
func (r *Runtime) completeToolCall(
	ctx context.Context,
	op durable.Operation,
	result *mcp.CallToolResult,
	callErr error,
) (any, error) {
	if callErr != nil {
		r.recordFailure(op, callErr)
		return nil, callErr
	}
	if result.IsError {
		err := errors.New(summarizeContent(result.Content))
		r.recordFailure(op, err)
		return result, err
	}
	summary := summarizeContent(result.Content)
	ev := durable.Event{
		Kind:          durable.EventOpCompleted,
		OperationID:   op.OperationID,
		SessionID:     op.SessionID,
		ResultSummary: summary,
	}
	_ = r.cfg.Journal.Append(ev)
	r.cfg.Projection.Apply(ev)
	r.scheduleObservational(ctx, op, ev, contentToIngest(op, result.Content))
	return result, nil
}

func (r *Runtime) recordResourceRead(op durable.Operation, result *mcp.ReadResourceResult) {
	summary := summarizeResourceContents(result.Contents)
	ev := durable.Event{
		Kind:          durable.EventOpCompleted,
		OperationID:   op.OperationID,
		SessionID:     op.SessionID,
		ResultSummary: summary,
	}
	_ = r.cfg.Journal.Append(ev)
	r.cfg.Projection.Apply(ev)
	payloads := resourceToIngest(op, result.Contents)
	r.scheduleObservational(context.Background(), op, ev, payloads)
}

func (r *Runtime) recordPromptInvoke(op durable.Operation, result *mcp.GetPromptResult) {
	summary := strings.TrimSpace(result.Description)
	ev := durable.Event{
		Kind:          durable.EventOpCompleted,
		OperationID:   op.OperationID,
		SessionID:     op.SessionID,
		ResultSummary: summary,
	}
	_ = r.cfg.Journal.Append(ev)
	r.cfg.Projection.Apply(ev)
	payloads := promptToIngest(op, result)
	r.scheduleObservational(context.Background(), op, ev, payloads)
}

func (r *Runtime) recordFailure(op durable.Operation, callErr error) {
	ev := durable.Event{
		Kind:         durable.EventOpFailed,
		OperationID:  op.OperationID,
		SessionID:    op.SessionID,
		ErrorMessage: callErr.Error(),
	}
	_ = r.cfg.Journal.Append(ev)
	r.cfg.Projection.Apply(ev)
	r.scheduleObservational(context.Background(), op, ev, nil)
}

// scheduleObservational fires outcome and ingest hooks on the runtime scope.
// The hooks are best-effort — failures inside them never propagate to the
// hot path. Async-by-default principle.
func (r *Runtime) scheduleObservational(ctx context.Context, op durable.Operation, ev durable.Event, payloads []IngestPayload) {
	final := op
	if updated, ok := r.cfg.Projection.Get(op.OperationID); ok {
		final = updated
	}
	scope := r.cfg.Scope
	if scope == nil {
		return
	}
	if r.cfg.Outcomes != nil {
		_ = scope.Go("mcp.runtime.outcome."+op.OperationID, observationalTimeout, func(_ context.Context) error {
			r.cfg.Outcomes.OnOperation(final, ev)
			return nil
		})
	}
	if r.cfg.Ingester != nil && len(payloads) > 0 {
		for _, payload := range payloads {
			r.scheduleIngest(payload)
		}
	}
}

const observationalTimeout = 5 * time.Second

func (r *Runtime) scheduleIngest(payload IngestPayload) {
	scope := r.cfg.Scope
	if scope == nil {
		return
	}
	_ = scope.Go("mcp.runtime.ingest."+payload.Source, observationalTimeout, func(ctx context.Context) error {
		r.cfg.Ingester.Ingest(ctx, payload)
		return nil
	})
}

// hashArguments computes a stable sha256 of the JSON arguments. Used both
// for ArgumentsHash on the projection (so Guardian grants validate) and
// as part of the operation's identity for replay.
func hashArguments(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func canonicalResourceID(serverID, uri string) string {
	return fmt.Sprintf("mcp://%s/%s", serverID, strings.TrimPrefix(uri, "/"))
}

func canonicalPromptID(serverID, name string) string {
	return fmt.Sprintf("mcp.prompt.%s.%s", serverID, name)
}

// sessionRemoteName extracts the remote tool name from the canonical name.
// Format is mcp.<server>.<name>; we strip the prefix to get the remote tool.
func sessionRemoteName(canonicalName, serverID string) string {
	prefix := "mcp." + serverID + "."
	return strings.TrimPrefix(canonicalName, prefix)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		trimmed := strings.TrimSpace(v)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}
