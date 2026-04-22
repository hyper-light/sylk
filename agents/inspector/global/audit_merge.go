package global

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/agents/inspector/shared"
	agentShared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/steering"
	"github.com/adalundhe/sylk/core/versioning"
)

// SpawnAuditReplica is the direct-dispatch entry point the audit
// coordinator calls for every merge completed by
// SessionVFS.MergePipelineIntoGreen. Implements
// agentShared.AuditReplicaSpawner. Validates agent-type routing,
// then runs the audit in a fresh goroutine so the coordinator's
// merge callback returns promptly. The ctx carries both the
// AuditMergeContext (for the emit_audit_decision skill) and the
// AuditDecisionFinalizer that routes the replica's verdict back to
// the coordinator — no bus broadcast.
func (gi *GlobalInspector) SpawnAuditReplica(ctx context.Context, req *agentShared.AuditMergeRequest) error {
	if req == nil {
		return errors.New("inspector: SpawnAuditReplica: nil request")
	}
	if req.AgentType != "inspector-global" {
		return fmt.Errorf("inspector: SpawnAuditReplica: wrong agent type %q", req.AgentType)
	}
	runCtx := gi.runCtx
	if runCtx == nil {
		runCtx = context.Background()
	}
	// Carry finalizer + audit context forward onto a background
	// ctx scoped to the agent's lifetime. ctx from the coordinator
	// may be tied to the merge-callback's goroutine; we want the
	// audit to outlive that.
	carryCtx := runCtx
	if f := agentShared.AuditDecisionFinalizerFromContext(ctx); f != nil {
		carryCtx = agentShared.WithAuditDecisionFinalizer(carryCtx, f)
	}
	go func() {
		if err := gi.handleAuditMerge(carryCtx, req); err != nil {
			gi.logger.Warn("audit_merge: handler error",
				"replica_id", req.ReplicaID,
				"merged_version", req.Descriptor.MergedVersion.String(),
				"error", err.Error(),
			)
		}
	}()
	return nil
}

// handleAuditMerge runs one per-merge audit to completion. Mirrors
// the shape of handleTaskRequest but scoped to an AuditMergeRequest:
//   - Session / task scope set from the request.
//   - AuditMergeContext attached so emit_audit_decision can resolve
//     the replica ID / merged version at call time.
//   - Tool loop invoked with an audit-scoped prompt that instructs
//     the LLM to inspect the diff, consult peers as needed, and
//     terminate via emit_audit_decision.
//   - If the tool loop exits without emitting a decision (model
//     declined, context cancelled, etc.), synthesize a rejection
//     with the failure reason so the commit queue does not stall.
func (gi *GlobalInspector) handleAuditMerge(ctx context.Context, req *agentShared.AuditMergeRequest) error {
	if gi.getProvider() == nil {
		// Provider not yet wired — emit a rejection so the queue
		// doesn't stall forever. The architect can retry when auth
		// lands.
		return gi.emitFallbackRejection(ctx, req, "inspector provider not available")
	}
	if !gi.requestSerializer.Acquire(ctx) {
		return ctx.Err()
	}
	defer gi.requestSerializer.Release()

	// Scope ctx with the audit context so emit_audit_decision and
	// any future audit-scoped skills can resolve the replica ID /
	// merged version without needing per-call parameters.
	ctx = agentShared.WithAuditMergeContext(ctx, agentShared.AuditMergeContext{
		SessionID:     req.SessionID,
		ReplicaID:     req.ReplicaID,
		AgentType:     req.AgentType,
		MergedVersion: req.Descriptor.MergedVersion,
		BaseVersion:   req.Descriptor.BaseVersion,
	})

	// Build an audit-scoped system prompt + user input that tells
	// the LLM its sole purpose for this turn-loop is to audit the
	// named merge and terminate by calling emit_audit_decision.
	systemPrompt := buildAuditMergeSystemPrompt(req)
	userPrompt := buildAuditMergeUserPrompt(req)

	gi.prepareSkillsForInput(userPrompt)
	tools := gi.buildToolDefinitions()

	providerReq := &providers.Request{
		SystemPrompt: systemPrompt,
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: userPrompt},
		},
		Model:     gi.config.Model,
		MaxTokens: gi.config.MaxTokens,
		Tools:     tools,
	}
	gi.applyLLMRuntimeProfile(providerReq, "audit_merge")

	// Wire the steering ledger to this replica's deterministic ID so
	// the ledger rehydrates correctly if the session resumes. When
	// the coordinator supplies a session-scoped SteeringJournalDir,
	// open a journal rooted there so the ledger is durable across
	// session Close / Open cycles (docs/PARALLEL_GLOBAL_VFS.md §8).
	var journal *steering.SteeringJournal
	if dir := strings.TrimSpace(req.SteeringJournalDir); dir != "" {
		j, jerr := steering.OpenSteeringJournalDirect(dir)
		if jerr != nil {
			gi.logger.Warn("audit replica: open steering journal failed",
				"replica_id", req.ReplicaID,
				"error", jerr.Error(),
			)
		} else {
			journal = j
		}
	}
	ledger := gi.steering.Create(req.ReplicaID, gi.id, req.SessionID, nil, journal)
	defer gi.steering.Close(req.ReplicaID, ctx.Err() != nil)
	ctx = agentShared.WithSteeringLedger(ctx, ledger)
	ctx = agentShared.WithLogMeta(ctx, agentShared.LogMeta{
		EventLogger: gi.steering.EventLogger(),
		CorrID:      req.ReplicaID,
		AgentID:     gi.id,
		SessionID:   req.SessionID,
	})

	emitted, err := gi.runAuditToolLoop(ctx, providerReq, ledger)
	if err != nil && !errors.Is(err, context.Canceled) {
		// Tool loop failed mechanically — synthesize a rejection
		// referencing the failure so the queue is not stranded.
		if !emitted {
			_ = gi.emitFallbackRejection(ctx, req,
				fmt.Sprintf("audit tool loop failed: %v", err))
		}
		if lm := agentShared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			agentShared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
				lm.AgentID, lm.SessionID, lm.CorrID, "error",
				&agentlog.ErrorPayload{Error: fmt.Sprintf("audit merge: %v", err)})
		}
		return err
	}
	if !emitted {
		// Loop exited cleanly but the LLM did not call
		// emit_audit_decision. Synthesize a rejection — a missing
		// decision is not an acceptance.
		return gi.emitFallbackRejection(ctx, req, "inspector tool loop terminated without emitting a decision")
	}
	return nil
}

// runAuditToolLoop executes the existing tool loop and returns
// whether emit_audit_decision was called during the loop. Emission
// is detected by wrapping the ctx-scoped AuditDecisionFinalizer
// with a flag-setter — no bus indirection. When the real finalizer
// fires, the wrapper sets emitted=true and delegates to the
// coordinator's callback. A loop exit with emitted=false triggers
// the caller's fallback rejection.
func (gi *GlobalInspector) runAuditToolLoop(ctx context.Context, req *providers.Request, ledger *steering.SteeringLedger) (bool, error) {
	inner := agentShared.AuditDecisionFinalizerFromContext(ctx)
	if inner == nil {
		return false, fmt.Errorf("runAuditToolLoop: no finalizer on ctx")
	}
	var emittedFlag atomic.Bool
	wrapCtx := agentShared.WithAuditDecisionFinalizer(ctx, func(r *agentShared.AuditMergeResult) {
		emittedFlag.Store(true)
		inner(r)
	})
	_, err := agentShared.ExecuteTurnLoop(ledger, req, func() (string, error) {
		return gi.executeToolLoop(wrapCtx, req, ledger)
	})
	return emittedFlag.Load(), err
}

// emitFallbackRejection routes a rejection verdict through the
// ctx-scoped finalizer when the replica can't run its audit to
// conclusion (provider unavailable, loop crashed, etc). A stranded
// commit queue is worse than a rejection — the architect retries
// via standard remediation.
func (gi *GlobalInspector) emitFallbackRejection(ctx context.Context, req *agentShared.AuditMergeRequest, reason string) error {
	result := &agentShared.AuditMergeResult{
		SessionID:     req.SessionID,
		ReplicaID:     req.ReplicaID,
		MergedVersion: req.Descriptor.MergedVersion,
		Decision:      versioning.ReplicaDecisionRejected,
		Summary:       reason,
		DecidedAt:     time.Now().UTC(),
	}
	if f := agentShared.AuditDecisionFinalizerFromContext(ctx); f != nil {
		f(result)
		return nil
	}
	return errors.New("inspector: fallback rejection has no finalizer on ctx")
}

// buildAuditMergeSystemPrompt constructs the system prompt that
// scopes the LLM's tool loop to a single per-merge audit. The prompt
// spells out the terminal contract (emit_audit_decision) so the
// model knows what shape its output must take.
func buildAuditMergeSystemPrompt(req *agentShared.AuditMergeRequest) string {
	base := shared.GlobalInspectorSystemPrompt()
	if base == "" {
		base = "You are the global inspector, auditing cross-pipeline coherence."
	}
	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\n")
	b.WriteString("# Per-Merge Audit Mode\n\n")
	b.WriteString("You are auditing a single merge for architectural coherence.\n\n")
	fmt.Fprintf(&b, "Target merge: %s (pipeline: %s)\n", req.Descriptor.MergedVersion.String(), req.Descriptor.PipelineID)
	fmt.Fprintf(&b, "Audit base: %s — the green state immediately BEFORE this merge applied.\n", req.Descriptor.BaseVersion.String())
	fmt.Fprintf(&b, "Paths touched: %d file(s): %s\n", req.Descriptor.PathCount, strings.Join(req.Descriptor.Paths, ", "))
	b.WriteString("\n")
	b.WriteString("Your audit:\n")
	b.WriteString("  1. Inspect the target paths (workspace_read) to understand the changeset.\n")
	b.WriteString("  2. Inspect audit-base context as needed (read siblings, related files).\n")
	b.WriteString("  3. Optionally call merges_after to see concurrent merges landing in parallel.\n")
	b.WriteString("  4. Consult peers (consult_peer) if architectural context is needed.\n")
	b.WriteString("  5. Terminate by calling emit_audit_decision exactly once with accepted or rejected.\n")
	b.WriteString("\n")
	b.WriteString("The audit completes when you call emit_audit_decision. Do not exit without calling it.\n")
	return b.String()
}

// buildAuditMergeUserPrompt builds the user turn's content. Keeps
// the factual descriptor payload separate from the instructional
// system prompt, so the LLM can quote / cite it cleanly.
func buildAuditMergeUserPrompt(req *agentShared.AuditMergeRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Audit merge %s.\n\n", req.Descriptor.MergedVersion.String())
	fmt.Fprintf(&b, "Base version: %s\n", req.Descriptor.BaseVersion.String())
	fmt.Fprintf(&b, "Pipeline: %s\n", req.Descriptor.PipelineID)
	fmt.Fprintf(&b, "Paths (%d): %s\n", req.Descriptor.PathCount, strings.Join(req.Descriptor.Paths, ", "))
	if cert := req.Descriptor.PipelineCertificate; cert.Summary != "" || cert.DeclaredScope != "" {
		b.WriteString("\n## Pipeline inspector certificate\n")
		if cert.DeclaredScope != "" {
			fmt.Fprintf(&b, "Declared scope: %s\n", cert.DeclaredScope)
		}
		if cert.Summary != "" {
			fmt.Fprintf(&b, "Summary: %s\n", cert.Summary)
		}
		if cert.TesterVerdict != "" {
			fmt.Fprintf(&b, "Tester verdict: %s\n", cert.TesterVerdict)
		}
		if len(cert.OpenConcerns) > 0 {
			fmt.Fprintf(&b, "Open concerns: %s\n", strings.Join(cert.OpenConcerns, "; "))
		}
	}
	if req.IsReAudit {
		b.WriteString("\n(Note: this is a re-audit following a supersession event — earlier context may have changed.)\n")
	}
	return b.String()
}
