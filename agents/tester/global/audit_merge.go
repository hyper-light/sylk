package global

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	testerShared "github.com/adalundhe/sylk/agents/tester/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/steering"
	"github.com/adalundhe/sylk/core/versioning"
)

// SpawnAuditReplica is the direct-dispatch entry point the audit
// coordinator calls for every merge. Implements
// agentshared.AuditReplicaSpawner. Symmetric with the inspector's
// SpawnAuditReplica.
func (gt *GlobalTester) SpawnAuditReplica(ctx context.Context, req *agentshared.AuditMergeRequest) error {
	if req == nil {
		return errors.New("tester: SpawnAuditReplica: nil request")
	}
	if req.AgentType != "tester-global" {
		return fmt.Errorf("tester: SpawnAuditReplica: wrong agent type %q", req.AgentType)
	}
	runCtx := gt.runCtx
	if runCtx == nil {
		runCtx = context.Background()
	}
	carryCtx := runCtx
	if f := agentshared.AuditDecisionFinalizerFromContext(ctx); f != nil {
		carryCtx = agentshared.WithAuditDecisionFinalizer(carryCtx, f)
	}
	go func() {
		if err := gt.handleAuditMerge(carryCtx, req); err != nil {
			gt.logger.Warn("audit_merge: handler error",
				"replica_id", req.ReplicaID,
				"merged_version", req.Descriptor.MergedVersion.String(),
				"error", err.Error(),
			)
		}
	}()
	return nil
}

// handleAuditMerge runs one per-merge audit to completion. Mirror of
// the global inspector's handleAuditMerge — scoped differently at
// the prompt level (tests pass/fail rather than architectural
// coherence) but structurally identical.
func (gt *GlobalTester) handleAuditMerge(ctx context.Context, req *agentshared.AuditMergeRequest) error {
	if gt.getProvider() == nil {
		return gt.emitFallbackRejection(ctx, req, "tester provider not available")
	}
	if !gt.requestSerializer.Acquire(ctx) {
		return ctx.Err()
	}
	defer gt.requestSerializer.Release()

	ctx = agentshared.WithAuditMergeContext(ctx, agentshared.AuditMergeContext{
		SessionID:     req.SessionID,
		ReplicaID:     req.ReplicaID,
		AgentType:     req.AgentType,
		MergedVersion: req.Descriptor.MergedVersion,
		BaseVersion:   req.Descriptor.BaseVersion,
	})

	systemPrompt := buildAuditMergeSystemPrompt(req)
	userPrompt := buildAuditMergeUserPrompt(req)

	gt.prepareSkillsForInput(userPrompt)
	tools := gt.buildToolDefinitions()

	providerReq := &providers.Request{
		SystemPrompt: systemPrompt,
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: userPrompt},
		},
		Model:     gt.config.Model,
		MaxTokens: gt.config.MaxTokens,
		Tools:     tools,
	}
	gt.applyLLMRuntimeProfile(providerReq, "audit_merge")

	// Open a session-scoped steering journal when the coordinator
	// supplied a path so the ledger persists across Close/Open
	// cycles (docs/PARALLEL_GLOBAL_VFS.md §8).
	var journal *steering.SteeringJournal
	if dir := strings.TrimSpace(req.SteeringJournalDir); dir != "" {
		j, jerr := steering.OpenSteeringJournalDirect(dir)
		if jerr != nil {
			gt.logger.Warn("audit replica: open steering journal failed",
				"replica_id", req.ReplicaID,
				"error", jerr.Error(),
			)
		} else {
			journal = j
		}
	}
	ledger := gt.steering.Create(req.ReplicaID, gt.id, req.SessionID, nil, journal)
	defer gt.steering.Close(req.ReplicaID, ctx.Err() != nil)
	ctx = agentshared.WithSteeringLedger(ctx, ledger)
	ctx = agentshared.WithLogMeta(ctx, agentshared.LogMeta{
		EventLogger: gt.steering.EventLogger(),
		CorrID:      req.ReplicaID,
		AgentID:     gt.id,
		SessionID:   req.SessionID,
	})

	emitted, err := gt.runAuditToolLoop(ctx, providerReq, ledger)
	if err != nil && !errors.Is(err, context.Canceled) {
		if !emitted {
			_ = gt.emitFallbackRejection(ctx, req,
				fmt.Sprintf("audit tool loop failed: %v", err))
		}
		if lm := agentshared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			agentshared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
				lm.AgentID, lm.SessionID, lm.CorrID, "error",
				&agentlog.ErrorPayload{Error: fmt.Sprintf("audit merge: %v", err)})
		}
		return err
	}
	if !emitted {
		return gt.emitFallbackRejection(ctx, req, "tester tool loop terminated without emitting a decision")
	}
	return nil
}

// runAuditToolLoop drives the tool loop and observes emission on
// the result topic, same pattern as the inspector's runAuditToolLoop.
// runAuditToolLoop mirrors the inspector's runAuditToolLoop: wraps
// the ctx finalizer with a flag-setter, executes the tool loop,
// and reports whether emission occurred.
func (gt *GlobalTester) runAuditToolLoop(ctx context.Context, req *providers.Request, ledger *steering.SteeringLedger) (bool, error) {
	inner := agentshared.AuditDecisionFinalizerFromContext(ctx)
	if inner == nil {
		return false, fmt.Errorf("runAuditToolLoop: no finalizer on ctx")
	}
	var emittedFlag atomic.Bool
	wrapCtx := agentshared.WithAuditDecisionFinalizer(ctx, func(r *agentshared.AuditMergeResult) {
		emittedFlag.Store(true)
		inner(r)
	})
	_, err := agentshared.ExecuteTurnLoop(ledger, req, func() (string, error) {
		return gt.executeToolLoop(wrapCtx, req, ledger)
	})
	return emittedFlag.Load(), err
}

func (gt *GlobalTester) emitFallbackRejection(ctx context.Context, req *agentshared.AuditMergeRequest, reason string) error {
	result := &agentshared.AuditMergeResult{
		SessionID:     req.SessionID,
		ReplicaID:     req.ReplicaID,
		MergedVersion: req.Descriptor.MergedVersion,
		Decision:      versioning.ReplicaDecisionRejected,
		Summary:       reason,
		DecidedAt:     time.Now().UTC(),
	}
	if f := agentshared.AuditDecisionFinalizerFromContext(ctx); f != nil {
		f(result)
		return nil
	}
	return errors.New("tester: fallback rejection has no finalizer on ctx")
}

// buildAuditMergeSystemPrompt constructs the tester's system prompt.
// Similar shape to the inspector's but framed for functional/test-
// oriented auditing rather than architectural coherence.
func buildAuditMergeSystemPrompt(req *agentshared.AuditMergeRequest) string {
	base := testerShared.GlobalTesterSystemPrompt()
	if base == "" {
		base = "You are the global tester, validating merges via cross-pipeline integration testing."
	}
	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\n")
	b.WriteString("# Per-Merge Audit Mode\n\n")
	b.WriteString("You are auditing a single merge for functional correctness and test impact.\n\n")
	fmt.Fprintf(&b, "Target merge: %s (pipeline: %s)\n", req.Descriptor.MergedVersion.String(), req.Descriptor.PipelineID)
	fmt.Fprintf(&b, "Audit base: %s — the green state immediately BEFORE this merge applied.\n", req.Descriptor.BaseVersion.String())
	fmt.Fprintf(&b, "Paths touched: %d file(s): %s\n", req.Descriptor.PathCount, strings.Join(req.Descriptor.Paths, ", "))
	b.WriteString("\n")
	b.WriteString("Your audit:\n")
	b.WriteString("  1. Inspect the changeset (workspace_read) to understand what changed.\n")
	b.WriteString("  2. Run applicable tests via run_analyzer / harness skills.\n")
	b.WriteString("  3. Optionally call merges_after to see concurrent merges landing in parallel.\n")
	b.WriteString("  4. Terminate by calling emit_audit_decision exactly once with accepted or rejected.\n")
	b.WriteString("\n")
	b.WriteString("The audit completes when you call emit_audit_decision. Do not exit without calling it.\n")
	return b.String()
}

// buildAuditMergeUserPrompt builds the user turn content — same
// shape as the inspector's so the LLM sees a consistent factual
// handoff regardless of which role it's playing.
func buildAuditMergeUserPrompt(req *agentshared.AuditMergeRequest) string {
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
			fmt.Fprintf(&b, "Pipeline tester verdict: %s\n", cert.TesterVerdict)
		}
		if len(cert.OpenConcerns) > 0 {
			fmt.Fprintf(&b, "Open concerns: %s\n", strings.Join(cert.OpenConcerns, "; "))
		}
	}
	if req.IsReAudit {
		b.WriteString("\n(Note: this is a re-audit following a supersession event.)\n")
	}
	return b.String()
}
