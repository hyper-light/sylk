package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

// EmitAuditDecisionSkillConfig configures the emit_audit_decision
// tool. The skill is the terminal of a per-merge audit replica's
// tool loop: calling it invokes the AuditDecisionFinalizer attached
// to ctx by the audit coordinator. Direct callback, no bus
// indirection — part of the direct-protocol dispatch loop.
type EmitAuditDecisionSkillConfig struct {
	// ContextResolver returns the in-flight audit scope for the
	// current request. Required.
	ContextResolver func(ctx context.Context) (AuditMergeContext, bool)
}

// AuditMergeContext is the ambient scope a replica carries while
// auditing. Stored on ctx via WithAuditMergeContext by the global's
// AuditMergeRequest handler; retrieved by emit_audit_decision and by
// any other skill that needs to correlate its output to the audit.
type AuditMergeContext struct {
	SessionID     string
	ReplicaID     string
	AgentType     string
	MergedVersion versioning.SemanticVersion
	BaseVersion   versioning.SemanticVersion
}

type auditMergeCtxKey struct{}

// WithAuditMergeContext attaches the audit scope to a context.
// Called by the AuditMergeRequest handler before invoking the tool
// loop.
func WithAuditMergeContext(ctx context.Context, amc AuditMergeContext) context.Context {
	return context.WithValue(ctx, auditMergeCtxKey{}, amc)
}

// AuditMergeContextFromContext returns the audit scope, or the zero
// value and false if the context is not currently auditing.
func AuditMergeContextFromContext(ctx context.Context) (AuditMergeContext, bool) {
	v, ok := ctx.Value(auditMergeCtxKey{}).(AuditMergeContext)
	return v, ok
}

// emitAuditDecisionInput is the JSON shape the LLM provides.
type emitAuditDecisionInput struct {
	Decision string   `json:"decision"`
	Summary  string   `json:"summary"`
	Concerns []string `json:"concerns"`
}

// NewEmitAuditDecisionSkill returns the terminal skill for a
// per-merge audit replica. The LLM calls it when its audit is
// complete; the handler publishes AuditMergeResult to the bus so
// the orchestrator's MergeAuditDispatcher translates it into
// CommitQueue.MarkAccepted / MarkRejected.
//
// The skill rejects calls made outside an audit scope (no context)
// — this prevents accidental invocation during unrelated tool
// loops. Calls with an unknown decision value are also rejected.
func NewEmitAuditDecisionSkill(cfg EmitAuditDecisionSkillConfig) *skills.Skill {
	return skills.NewSkill("emit_audit_decision").
		Description("Emit the terminal verdict for a per-merge audit. Use exactly once at the end of an audit. decision must be \"accepted\" or \"rejected\". summary is a prose explanation the architect and user will read. concerns is a structured list of big-picture issues (populated on rejection; may be empty on acceptance).").
		EnumParam("decision", "The audit verdict. Either \"accepted\" (this merge coheres with its context and should reach disk) or \"rejected\" (this merge has issues that block commit).", []string{"accepted", "rejected"}, true).
		StringParam("summary", "Prose summary of what you audited and why you reached this decision. Becomes the rejection reason on rejected verdicts; surfaces in observability on accepted.", true).
		ArrayParam("concerns", "Structured list of big-picture coherence concerns. On rejection these become the architect's remediation inputs.", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			amc, ok := cfg.ContextResolver(ctx)
			if !ok {
				return nil, fmt.Errorf("emit_audit_decision: no audit merge context — this skill must be called from within a per-merge audit")
			}
			finalizer := AuditDecisionFinalizerFromContext(ctx)
			if finalizer == nil {
				return nil, fmt.Errorf("emit_audit_decision: no audit decision finalizer on context — coordinator must attach one via WithAuditDecisionFinalizer before dispatch")
			}
			var params emitAuditDecisionInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("emit_audit_decision: invalid input: %w", err)
			}
			var decision versioning.ReplicaDecision
			switch strings.ToLower(strings.TrimSpace(params.Decision)) {
			case "accepted":
				decision = versioning.ReplicaDecisionAccepted
			case "rejected":
				decision = versioning.ReplicaDecisionRejected
			default:
				return nil, fmt.Errorf("emit_audit_decision: decision must be accepted or rejected (got %q)", params.Decision)
			}

			result := &AuditMergeResult{
				SessionID:     amc.SessionID,
				ReplicaID:     amc.ReplicaID,
				MergedVersion: amc.MergedVersion,
				Decision:      decision,
				Summary:       params.Summary,
				Concerns:      append([]string(nil), params.Concerns...),
				DecidedAt:     time.Now().UTC(),
			}
			// Direct callback into the coordinator — no bus
			// indirection. The coordinator handles lifecycle,
			// retention, queue transitions, and optionally re-
			// publishes on the observability topic for TUI /
			// architect consumers.
			finalizer(result)
			decisionStr := "accepted"
			if decision == versioning.ReplicaDecisionRejected {
				decisionStr = "rejected"
			}
			return map[string]any{
				"emitted":        true,
				"decision":       decisionStr,
				"replica_id":     amc.ReplicaID,
				"merged_version": amc.MergedVersion.String(),
			}, nil
		}).
		Build()
}
