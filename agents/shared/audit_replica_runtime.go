package shared

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/providers"
	skillsPkg "github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/steering"
	"github.com/adalundhe/sylk/core/versioning"
)

// skillsPkgSkill is a local alias so consult_peer's skill handle
// has a compact name. The cross_pipeline skill builder returns
// *skills.Skill values; we interrogate Name and invoke Handler.
type skillsPkgSkill = skillsPkg.Skill

// AuditReplicaRuntime is the Stage-3 per-replica audit execution
// surface. Each per-merge audit spawns a fresh runtime; the runtime
// owns its own state — there is NO shared mutable runtime state
// across concurrent audits. This is what lets N parallel merges
// produce N truly parallel inspector+tester audits with no
// serialization outside of MergePipe's single-goroutine OT loop
// (where the design requires serial ordering).
//
// docs/PARALLEL_GLOBAL_VFS.md §3 "Audit concurrency: N parallel
// merges → N parallel inspector+tester pairs" and §3.7 "audit-
// addendum merge on accept" — the addendum goes through MergePipe's
// OT path, which is the only serialization point.
//
// Design choices:
//
//   - Static, audit-scoped tool set. Dynamic skill loading mutates a
//     shared registry (`skill.Loaded`); audits don't need dynamic
//     loading, so we skip it entirely and use a fixed, known tool
//     set built from the replica's capabilities.
//   - Direct tool dispatch, bypassing the shared Runtime's
//     SerialWorker. Audit tools are thread-safe by construction
//     (ReplicaVFS has its own mutex; SessionVFS readers are
//     RLock-safe; finalizer is a single-fire callback).
//   - Own steering ledger, own provider request, own message history.
//     The only shared references are the thread-safe Provider +
//     SessionVFS + bus + ReplicaVFS (all of which have their own
//     mutexes).
//
// The runtime is single-use: Run returns when emit_audit_decision
// fires (or a terminal error). The caller disposes of the runtime
// afterwards.
type AuditReplicaRuntime struct {
	cfg AuditReplicaConfig

	// decided is set when the replica's emit_audit_decision
	// finalizer fires; the tool loop exits on the next iteration
	// rather than running another LLM turn.
	decided atomic.Bool
}

// AuditReplicaConfig configures one AuditReplicaRuntime instance.
// Every field is scoped to a single replica invocation.
type AuditReplicaConfig struct {
	// Identity fields — determined by the coordinator.
	ReplicaID     string
	AgentType     string
	SessionID     string
	MergedVersion versioning.SemanticVersion
	BaseVersion   versioning.SemanticVersion
	IsReAudit     bool

	// Request the coordinator dispatched. Gives us the descriptor +
	// the PipelineInspectorCertificate for prompt composition.
	Request *AuditMergeRequest

	// Session is the SessionVFS that owns the merge log (merges_after
	// queries) and the commit queue.
	Session *versioning.SessionVFS

	// ReplicaVFS is THIS replica's copy-on-write overlay. Writes
	// (workspace_write / workspace_delete) go here. Reads fall
	// through the chain + disk.
	ReplicaVFS *versioning.ReplicaVFS

	// Finalizer is the ctx-scoped callback that ends the audit.
	// emit_audit_decision calls it exactly once.
	Finalizer AuditDecisionFinalizer

	// Ledger is this replica's steering ledger — already scoped to
	// ReplicaID as correlation ID by the caller.
	Ledger *steering.SteeringLedger

	// Provider is the LLM provider. Shared across replicas; must be
	// thread-safe (all production providers are).
	Provider StreamingTurnProvider

	// Per-replica LLM configuration.
	Model       string
	MaxTokens   int
	MaxToolRuns int

	// Observability. The bus + channels route streamed chunks to
	// the chat panel; logger is structured output. AgentID is the
	// singleton agent's ID (used for stream attribution). None of
	// these are mutated — they're thread-safe shared references.
	Bus              guide.EventBus
	Channels         *guide.AgentChannels
	AgentID          string
	AgentDisplayName string
	Logger           *slog.Logger

	// ResponseTopic is the bus topic the replica's consult_peer
	// synchronous round-trips receive responses on. Typically the
	// singleton inspector's / tester's Responses topic. When
	// non-empty, the runtime exposes the consult_peer tool; when
	// empty, consult_peer is omitted from the tool set entirely.
	ResponseTopic string

	// ThinkingBudget is the Anthropic extended-thinking budget for
	// audit LLM turns. Zero disables thinking mode (OpenAI, Google
	// providers ignore it). The runtime threads it into
	// ContextGovernor's budget calculation.
	ThinkingBudget int

	// SystemPrompt / UserPrompt are per-replica prompt strings the
	// caller composes. See buildAuditMergeSystemPrompt / UserPrompt
	// helpers.
	SystemPrompt string
	UserPrompt   string
}

// defaultAuditMaxToolRuns is the fallback cap when the config
// doesn't specify one. Audits are bounded by their fixed tool set +
// the LLM's budget — 24 turns is enough for read/write/decide
// sequences without allowing runaway loops.
const defaultAuditMaxToolRuns = 24

// NewAuditReplicaRuntime validates the config and returns a runtime
// ready to Run. Rejects configs missing required fields (replica
// identity, VFS, session, provider, finalizer) because running
// without any of them is a programming error.
func NewAuditReplicaRuntime(cfg AuditReplicaConfig) (*AuditReplicaRuntime, error) {
	if strings.TrimSpace(cfg.ReplicaID) == "" {
		return nil, fmt.Errorf("audit replica runtime: ReplicaID required")
	}
	if cfg.Request == nil {
		return nil, fmt.Errorf("audit replica runtime: Request required")
	}
	if cfg.Session == nil {
		return nil, fmt.Errorf("audit replica runtime: Session required")
	}
	if cfg.ReplicaVFS == nil {
		return nil, fmt.Errorf("audit replica runtime: ReplicaVFS required")
	}
	if cfg.Finalizer == nil {
		return nil, fmt.Errorf("audit replica runtime: Finalizer required")
	}
	if cfg.Provider == nil {
		return nil, fmt.Errorf("audit replica runtime: Provider required")
	}
	if cfg.Ledger == nil {
		return nil, fmt.Errorf("audit replica runtime: Ledger required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.MaxToolRuns <= 0 {
		cfg.MaxToolRuns = defaultAuditMaxToolRuns
	}
	return &AuditReplicaRuntime{cfg: cfg}, nil
}

// Run executes the audit loop to completion. Returns when
// emit_audit_decision fires (success) or on a terminal error. The
// runtime is single-use: callers should not invoke Run more than
// once.
//
// Concurrency contract: Run is safe to invoke from any goroutine and
// multiple Run invocations on DIFFERENT runtimes run fully in
// parallel — the runtime holds no state that another runtime reads
// or writes.
func (r *AuditReplicaRuntime) Run(ctx context.Context) error {
	if r == nil {
		return fmt.Errorf("audit replica runtime: nil receiver")
	}

	// Attach a decide-wrapping finalizer: when the replica calls
	// emit_audit_decision, the underlying finalizer fires and we flip
	// `decided` so the tool loop exits on the next iteration.
	innerFinalizer := r.cfg.Finalizer
	ctx = WithAuditDecisionFinalizer(ctx, func(result *AuditMergeResult) {
		r.decided.Store(true)
		innerFinalizer(result)
	})
	// Audit context stays on ctx so emit_audit_decision (via skill) +
	// direct dispatch paths can look up the replica identity.
	ctx = WithAuditMergeContext(ctx, AuditMergeContext{
		SessionID:     r.cfg.SessionID,
		ReplicaID:     r.cfg.ReplicaID,
		AgentType:     r.cfg.AgentType,
		MergedVersion: r.cfg.MergedVersion,
		BaseVersion:   r.cfg.BaseVersion,
	})
	ctx = WithSteeringLedger(ctx, r.cfg.Ledger)

	// Per-replica context governor: bounds this audit's LLM context
	// window independently of any other audit's. Writes to ctx so
	// ApplyContextBudget + Calibrate can find it in the loop.
	governor := NewContextGovernor(r.cfg.Model, r.cfg.MaxTokens, r.cfg.ThinkingBudget)
	ctx = WithContextGovernor(ctx, governor)

	req := &providers.Request{
		SystemPrompt: r.cfg.SystemPrompt,
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: r.cfg.UserPrompt},
		},
		Model:     r.cfg.Model,
		MaxTokens: r.cfg.MaxTokens,
		Tools:     buildAuditReplicaTools(r.cfg.AgentType, r.cfg.ResponseTopic != ""),
	}

	for turn := 0; turn <= r.cfg.MaxToolRuns; turn++ {
		// Steering checkpoint. Unlike the non-audit tool loop, an
		// audit's Ledger is per-replica so pauses / resumes / edits
		// don't cross audits.
		sc := DrainAndCheckpoint(r.cfg.Ledger, req, turn, "auditing", nil)
		if sc.Rollback != nil || sc.EditReplay != nil {
			cp := sc.Rollback
			if cp == nil {
				cp = sc.EditReplay
			}
			req.Messages = req.Messages[:cp.MessageCount]
			if sc.EditReplay != nil {
				req.Messages = append(req.Messages, providers.Message{
					Role:    providers.RoleUser,
					Content: sc.EditText,
				})
			}
			turn = cp.Turn
			continue
		}
		if sc.ShouldPause {
			if err := r.cfg.Ledger.WaitForResume(ctx); err != nil {
				return fmt.Errorf("audit replica %s: ledger resume: %w", r.cfg.ReplicaID, err)
			}
			continue
		}

		if r.decided.Load() {
			return nil
		}

		// Context-budget gate: strips tools or triggers handoff when
		// the input token count approaches the model's window. Per-
		// audit governor; other audits' windows are independent.
		if err := ApplyContextBudget(ctx, turn, r.cfg.MaxToolRuns, req); err != nil {
			// Context critically full — emit fallback rejection so
			// the queue advances; the architect can compose a
			// scoped-down remediation.
			return r.emitFallbackRejection(ctx, fmt.Sprintf("audit context budget: %v", err))
		}

		// Run one LLM turn (streams to bus via StreamLLMTurn).
		resp, err := StreamLLMTurn(ctx, StreamingLLMTurnConfig{
			Bus:              r.cfg.Bus,
			Channels:         r.cfg.Channels,
			AgentID:          r.cfg.AgentID,
			AgentDisplayName: r.cfg.AgentDisplayName,
		}, r.cfg.Provider, req)
		if err != nil {
			return r.emitFallbackRejection(ctx, fmt.Sprintf("audit llm stream: %v", err))
		}

		// Calibrate the governor against the provider's reported
		// input token count. Ground-truth anchoring keeps the budget
		// estimator from drifting across turns.
		governor.Calibrate(ctx, resp, req.Messages)

		if len(resp.ToolCalls) == 0 {
			// No tool calls — the LLM wrote prose without terminating.
			// A missing emit_audit_decision is not an acceptance; emit
			// a fallback rejection so the queue does not stall.
			if r.decided.Load() {
				return nil
			}
			return r.emitFallbackRejection(ctx, "audit replica exited tool loop without emitting a decision")
		}

		// Accumulate assistant message (for next-turn context) then
		// dispatch the tool calls in parallel — all audit tools are
		// thread-safe, and parallel dispatch eliminates within-turn
		// serialization.
		req.Messages = append(req.Messages, providers.Message{
			Role:      providers.RoleAssistant,
			Content:   resp.Content,
			ToolCalls: append([]providers.ToolCall(nil), resp.ToolCalls...),
			Metadata:  resp.ProviderMetadata,
		})
		results := r.dispatchToolCallsParallel(ctx, resp.ToolCalls)
		for _, tr := range results {
			req.Messages = append(req.Messages, providers.Message{
				Role:       providers.RoleUser,
				ToolCallID: tr.callID,
				ToolName:   tr.name,
				Content:    tr.output,
				IsError:    tr.isError,
			})
		}
		if r.decided.Load() {
			// emit_audit_decision fired — the finalizer has run. Let
			// the commit-queue transition logic complete; Return
			// cleanly.
			return nil
		}
	}

	// Exhausted the tool-run budget without a decision. Fallback
	// rejection so the queue head doesn't stall.
	return r.emitFallbackRejection(ctx, fmt.Sprintf("audit replica exhausted tool-run budget (%d)", r.cfg.MaxToolRuns))
}

// auditToolResult carries one tool invocation's outcome for
// appending to the message history.
type auditToolResult struct {
	callID  string
	name    string
	output  string
	isError bool
}

// dispatchToolCallsParallel dispatches all tool calls from a single
// LLM turn in parallel, via the ctx's GoroutineScope. Each tool
// handler runs in its own goroutine; results are returned in the
// original tool-call order so the LLM sees a deterministic message
// history.
//
// All audit-scoped tool handlers are thread-safe by construction
// (ReplicaVFS mutex-protected writes, SessionVFS RLock reads,
// single-fire finalizer) so parallel dispatch introduces no races.
func (r *AuditReplicaRuntime) dispatchToolCallsParallel(ctx context.Context, calls []providers.ToolCall) []auditToolResult {
	if len(calls) == 0 {
		return nil
	}
	results := make([]auditToolResult, len(calls))
	scope := concurrency.ScopeFromContext(ctx)
	if scope == nil {
		// No scope — fall back to sequential dispatch. We refuse to
		// spawn untracked goroutines; sequential invocation is
		// correct, just slower for multi-tool turns. Production
		// config always has a scope (StartSessionAuditWiring
		// stamps one on ctx).
		for i, call := range calls {
			results[i] = r.dispatchToolCall(ctx, call)
		}
		return results
	}
	done := make(chan struct{}, len(calls))
	for i := range calls {
		i := i
		call := calls[i]
		if err := scope.Go("audit-tool:"+r.cfg.ReplicaID+":"+call.Name, 0, func(workCtx context.Context) error {
			// Use the caller's ctx for finalizer + audit-context
			// lookups; workCtx is scope-scoped for tracking only.
			results[i] = r.dispatchToolCall(ctx, call)
			done <- struct{}{}
			return nil
		}); err != nil {
			// Scope rejected the spawn (shutdown, budget, etc).
			// Fall back to synchronous invocation so the audit can
			// still progress.
			results[i] = r.dispatchToolCall(ctx, call)
			done <- struct{}{}
		}
	}
	for i := 0; i < len(calls); i++ {
		select {
		case <-done:
		case <-ctx.Done():
			// Context cancelled mid-dispatch. Fill unfinished
			// results with a cancellation error so the message
			// history stays consistent.
			for j := range results {
				if results[j].callID == "" {
					results[j] = auditToolResult{
						callID:  calls[j].ID,
						name:    calls[j].Name,
						output:  "context cancelled before tool dispatch",
						isError: true,
					}
				}
			}
			return results
		}
	}
	return results
}

// dispatchToolCall invokes one audit-scoped tool and returns its
// result. Unknown tool names produce an error result (the LLM sees
// "unknown tool" and can course-correct).
func (r *AuditReplicaRuntime) dispatchToolCall(ctx context.Context, call providers.ToolCall) auditToolResult {
	switch call.Name {
	case AuditToolWorkspaceRead:
		return r.handleWorkspaceRead(call)
	case AuditToolWorkspaceWrite:
		return r.handleWorkspaceWrite(call)
	case AuditToolWorkspaceDelete:
		return r.handleWorkspaceDelete(call)
	case AuditToolMergesAfter:
		return r.handleMergesAfter(call)
	case AuditToolConsultPeer:
		return r.handleConsultPeer(ctx, call)
	case AuditToolEmitDecision:
		return r.handleEmitDecision(ctx, call)
	default:
		return auditToolResult{
			callID:  call.ID,
			name:    call.Name,
			output:  fmt.Sprintf("unknown audit tool %q — valid tools: workspace_read, workspace_write, workspace_delete, merges_after, consult_peer, emit_audit_decision", call.Name),
			isError: true,
		}
	}
}

// emitFallbackRejection fires the ctx-scoped finalizer with a
// rejected verdict when the replica can't complete its audit
// normally (LLM error, budget exhausted, missing terminal tool
// call). A stranded commit queue is worse than a rejection — the
// architect retries via standard remediation.
func (r *AuditReplicaRuntime) emitFallbackRejection(ctx context.Context, reason string) error {
	if r.decided.Load() {
		// Already decided (race with emit_audit_decision finishing).
		// Don't double-fire the finalizer.
		return nil
	}
	r.decided.Store(true)
	r.cfg.Finalizer(&AuditMergeResult{
		SessionID:     r.cfg.SessionID,
		ReplicaID:     r.cfg.ReplicaID,
		MergedVersion: r.cfg.MergedVersion,
		Decision:      versioning.ReplicaDecisionRejected,
		Summary:       reason,
		DecidedAt:     time.Now().UTC(),
	})
	return nil
}

// ===== Tool handlers — one method per AuditTool* constant =====

// handleWorkspaceRead reads a path from the replica's VFS (chain +
// disk). Returns ErrFileNotFound for missing paths (the LLM sees
// the error and can try a different path).
func (r *AuditReplicaRuntime) handleWorkspaceRead(call providers.ToolCall) auditToolResult {
	var params struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &params); err != nil {
		return auditToolResult{callID: call.ID, name: call.Name, output: "invalid arguments: " + err.Error(), isError: true}
	}
	path := strings.TrimSpace(params.Path)
	if path == "" {
		return auditToolResult{callID: call.ID, name: call.Name, output: "path is required", isError: true}
	}
	content, err := r.cfg.ReplicaVFS.Read(path)
	if err != nil {
		if errors.Is(err, versioning.ErrFileNotFound) {
			return auditToolResult{callID: call.ID, name: call.Name, output: fmt.Sprintf("file not found: %s", path), isError: true}
		}
		return auditToolResult{callID: call.ID, name: call.Name, output: "read failed: " + err.Error(), isError: true}
	}
	return auditToolResult{callID: call.ID, name: call.Name, output: string(content)}
}

// handleWorkspaceWrite writes a path to the replica's VFS. The
// write lands in THIS replica's overlay — not disk, not other
// replicas' VFSs. Attribution uses the replica's ID so the WAL
// records which replica authored the write.
func (r *AuditReplicaRuntime) handleWorkspaceWrite(call providers.ToolCall) auditToolResult {
	var params struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &params); err != nil {
		return auditToolResult{callID: call.ID, name: call.Name, output: "invalid arguments: " + err.Error(), isError: true}
	}
	path := strings.TrimSpace(params.Path)
	if path == "" {
		return auditToolResult{callID: call.ID, name: call.Name, output: "path is required", isError: true}
	}
	if err := r.cfg.ReplicaVFS.Write(r.cfg.ReplicaID, path, []byte(params.Content)); err != nil {
		return auditToolResult{callID: call.ID, name: call.Name, output: "write failed: " + err.Error(), isError: true}
	}
	return auditToolResult{callID: call.ID, name: call.Name, output: fmt.Sprintf("wrote %d bytes to %s", len(params.Content), path)}
}

// handleWorkspaceDelete removes a path at this replica's layer.
// Shadows ancestors and disk via the chain-walk delete-shadow
// semantics.
func (r *AuditReplicaRuntime) handleWorkspaceDelete(call providers.ToolCall) auditToolResult {
	var params struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &params); err != nil {
		return auditToolResult{callID: call.ID, name: call.Name, output: "invalid arguments: " + err.Error(), isError: true}
	}
	path := strings.TrimSpace(params.Path)
	if path == "" {
		return auditToolResult{callID: call.ID, name: call.Name, output: "path is required", isError: true}
	}
	if err := r.cfg.ReplicaVFS.Delete(r.cfg.ReplicaID, path); err != nil {
		return auditToolResult{callID: call.ID, name: call.Name, output: "delete failed: " + err.Error(), isError: true}
	}
	return auditToolResult{callID: call.ID, name: call.Name, output: fmt.Sprintf("deleted %s", path)}
}

// handleMergesAfter returns the list of merge descriptors with
// MergedVersion > since. Used by mid-audit awareness (§3.9) so a
// replica can see merges that landed after its context was cut and
// decide whether to rebase / flag / proceed.
func (r *AuditReplicaRuntime) handleMergesAfter(call providers.ToolCall) auditToolResult {
	var params struct {
		SinceMajor uint32 `json:"since_major"`
		SinceMinor uint32 `json:"since_minor"`
	}
	if len(call.Arguments) > 0 {
		if err := json.Unmarshal([]byte(call.Arguments), &params); err != nil {
			return auditToolResult{callID: call.ID, name: call.Name, output: "invalid arguments: " + err.Error(), isError: true}
		}
	}
	since := versioning.SemanticVersion{Major: params.SinceMajor, Minor: params.SinceMinor}
	descs := r.cfg.Session.MergesAfter(since)
	type descOut struct {
		PipelineID     string   `json:"pipeline_id"`
		BaseVersion    string   `json:"base_version"`
		MergedVersion  string   `json:"merged_version"`
		Paths          []string `json:"paths"`
		AuditAddendum  bool     `json:"audit_addendum"`
		SupersedesFrom string   `json:"supersedes_from,omitempty"`
	}
	out := make([]descOut, len(descs))
	for i, d := range descs {
		entry := descOut{
			PipelineID:    d.PipelineID,
			BaseVersion:   d.BaseVersion.String(),
			MergedVersion: d.MergedVersion.String(),
			Paths:         append([]string(nil), d.Paths...),
			AuditAddendum: d.AuditAddendum,
		}
		if !d.SupersedesVersion.IsZero() {
			entry.SupersedesFrom = d.SupersedesVersion.String()
		}
		out[i] = entry
	}
	body, err := json.Marshal(out)
	if err != nil {
		return auditToolResult{callID: call.ID, name: call.Name, output: "marshal failed: " + err.Error(), isError: true}
	}
	return auditToolResult{callID: call.ID, name: call.Name, output: string(body)}
}

// handleConsultPeer routes the audit's question to a peer agent
// (e.g., academic, librarian, or another global agent) via the
// cross-pipeline consult primitive. Synchronous round-trip: the
// replica's LLM sees the peer's response inline.
//
// Requires the config to supply a non-empty ResponseTopic so the
// sync helper knows where to wait for the terminal response. Without
// a topic the tool is omitted from the replica's tool set by
// buildAuditReplicaTools (defense-in-depth: the LLM never sees a
// tool it can't invoke).
func (r *AuditReplicaRuntime) handleConsultPeer(ctx context.Context, call providers.ToolCall) auditToolResult {
	if r.cfg.Bus == nil || r.cfg.ResponseTopic == "" {
		return auditToolResult{callID: call.ID, name: call.Name, output: "consult_peer is not configured for this audit replica", isError: true}
	}
	// Construct the cross-pipeline skill on the fly with this
	// replica's identity. The skill's handler does the heavy work
	// (authority gate, deadline, activity emission, RouteSync).
	sessionID := r.cfg.SessionID
	agentID := r.cfg.ReplicaID
	agentType := r.cfg.AgentType
	mergedVer := r.cfg.MergedVersion.String()
	skillsList := CrossPipelineSkills(CrossPipelineSkillConfig{
		SessionID:  func() string { return sessionID },
		AgentID:    func() string { return agentID },
		AgentType:  func() string { return agentType },
		PipelineID: func() string { return "audit-replica:" + mergedVer },
		RouteSync: RouteSyncFromBus(
			func() guide.EventBus { return r.cfg.Bus },
			func() string { return r.cfg.ResponseTopic },
		),
	})
	var consultSkill *skillsPkgSkill
	for _, s := range skillsList {
		if s != nil && s.Name == "consult_peer" {
			consultSkill = s
			break
		}
	}
	if consultSkill == nil || consultSkill.Handler == nil {
		return auditToolResult{callID: call.ID, name: call.Name, output: "consult_peer not permitted for this replica's agent type", isError: true}
	}
	result, err := consultSkill.Handler(ctx, json.RawMessage(call.Arguments))
	if err != nil {
		return auditToolResult{callID: call.ID, name: call.Name, output: "consult failed: " + err.Error(), isError: true}
	}
	body, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		return auditToolResult{callID: call.ID, name: call.Name, output: "consult result marshal: " + marshalErr.Error(), isError: true}
	}
	return auditToolResult{callID: call.ID, name: call.Name, output: string(body)}
}

// handleEmitDecision fires the ctx-scoped finalizer with the
// replica's verdict. Idempotent — a second call after the first
// succeeded is a no-op (the decided flag guards double-fire). The
// LLM may still see the tool "succeed" twice, but the coordinator
// only receives one decision.
func (r *AuditReplicaRuntime) handleEmitDecision(ctx context.Context, call providers.ToolCall) auditToolResult {
	var params struct {
		Decision string   `json:"decision"`
		Summary  string   `json:"summary"`
		Concerns []string `json:"concerns"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &params); err != nil {
		return auditToolResult{callID: call.ID, name: call.Name, output: "invalid arguments: " + err.Error(), isError: true}
	}
	decisionStr := strings.TrimSpace(strings.ToLower(params.Decision))
	if decisionStr != "accepted" && decisionStr != "rejected" {
		return auditToolResult{callID: call.ID, name: call.Name, output: fmt.Sprintf("invalid decision %q — must be 'accepted' or 'rejected'", params.Decision), isError: true}
	}
	summary := strings.TrimSpace(params.Summary)
	if summary == "" {
		return auditToolResult{callID: call.ID, name: call.Name, output: "summary is required", isError: true}
	}
	if r.decided.Load() {
		// Second call — idempotent. LLM may have retried after a
		// streaming error; don't double-finalize.
		return auditToolResult{callID: call.ID, name: call.Name, output: "decision already emitted"}
	}
	var decision versioning.ReplicaDecision
	if decisionStr == "accepted" {
		decision = versioning.ReplicaDecisionAccepted
	} else {
		decision = versioning.ReplicaDecisionRejected
	}
	r.decided.Store(true)
	// Use the ctx's finalizer so tests and alternate wirings can
	// substitute. Falls back to the config's direct finalizer if no
	// ctx finalizer is attached (defensive — the Run path always
	// attaches one).
	finalizer := AuditDecisionFinalizerFromContext(ctx)
	if finalizer == nil {
		finalizer = r.cfg.Finalizer
	}
	finalizer(&AuditMergeResult{
		SessionID:     r.cfg.SessionID,
		ReplicaID:     r.cfg.ReplicaID,
		MergedVersion: r.cfg.MergedVersion,
		Decision:      decision,
		Summary:       summary,
		Concerns:      append([]string(nil), params.Concerns...),
		DecidedAt:     time.Now().UTC(),
	})
	return auditToolResult{callID: call.ID, name: call.Name, output: "decision emitted: " + decisionStr}
}
