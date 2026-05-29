package guardian

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/commandapproval"
)

type ClaimsBackendConfig struct {
	Guardian *Guardian
	Clock    func() time.Time
}

type ClaimsBackend struct {
	guardian *Guardian
	clock    func() time.Time
}

type claimsGuardianArgs struct {
	Command         string                         `json:"command,omitempty"`
	WorkingDir      string                         `json:"working_dir,omitempty"`
	WorkspaceRoot   string                         `json:"workspace_root,omitempty"`
	ToolName        string                         `json:"tool_name,omitempty"`
	Domain          string                         `json:"domain,omitempty"`
	Justification   string                         `json:"justification,omitempty"`
	AgentID         string                         `json:"agent_id,omitempty"`
	AgentType       string                         `json:"agent_type,omitempty"`
	SessionID       string                         `json:"session_id,omitempty"`
	TaskID          string                         `json:"task_id,omitempty"`
	PipelineID      string                         `json:"pipeline_id,omitempty"`
	Branch          string                         `json:"branch,omitempty"`
	Diff            string                         `json:"diff,omitempty"`
	RollbackReceipt string                         `json:"rollback_receipt,omitempty"`
	ApprovalPolicy  commandapproval.ApprovalPolicy `json:"approval_policy,omitempty"`
}

func NewClaimsBackend(cfg ClaimsBackendConfig) *ClaimsBackend {
	return &ClaimsBackend{guardian: cfg.Guardian, clock: firstClaimsGuardianClock(cfg.Clock)}
}

func (b *ClaimsBackend) HandleGuardianDecision(ctx context.Context, req claims.GuardianDecisionRequest) (claims.GuardianDecisionArtifactData, error) {
	data := req.Requested
	if b == nil || b.guardian == nil {
		data.FailureReason = "guardian not configured"
		return data, nil
	}
	args, err := decodeClaimsGuardianArgs(req.Call.Arguments)
	if err != nil {
		data.FailureReason = err.Error()
		return data, nil
	}
	switch data.Operation {
	case claims.GuardianToolCommandGate:
		return b.commandGate(ctx, data, args), nil
	case claims.GuardianToolBranchProtection:
		return b.branchProtection(data, args), nil
	case claims.GuardianToolDiffReview:
		return b.diffReview(data, args), nil
	case claims.GuardianToolRollbackAuthorization:
		return b.rollback(ctx, data, args), nil
	default:
		return data, nil
	}
}

func (b *ClaimsBackend) commandGate(ctx context.Context, data claims.GuardianDecisionArtifactData, args claimsGuardianArgs) claims.GuardianDecisionArtifactData {
	eval, err := b.guardian.evaluateCommandApproval(ctx, commandApprovalRequest(data, args))
	if err != nil {
		data.FailureReason = err.Error()
		return data
	}
	data.Allowed = eval.Decision == commandapproval.DecisionAllow
	data.UserDecision = firstClaimsGuardianString(eval.UserDecision, string(eval.Decision))
	data.DenialReason = claimsGuardianDenial(data.Allowed, eval.Reason)
	return data
}

func (b *ClaimsBackend) branchProtection(data claims.GuardianDecisionArtifactData, args claimsGuardianArgs) claims.GuardianDecisionArtifactData {
	branch := firstClaimsGuardianString(args.Branch, data.ApprovalSubject)
	if b.guardian.gitObserver == nil {
		data.FailureReason = "git observer not configured"
		return data
	}
	data.BranchState = firstClaimsGuardianString(data.BranchState, branch)
	data.BranchProtected = b.guardian.gitObserver.isBranchProtected(branch)
	data.Allowed = data.BranchProtected
	return data
}

func (b *ClaimsBackend) diffReview(data claims.GuardianDecisionArtifactData, args claimsGuardianArgs) claims.GuardianDecisionArtifactData {
	if b.guardian.diffGate == nil {
		data.FailureReason = "diff gate not configured"
		return data
	}
	review := b.guardian.diffGate.ReviewDiff(args.Diff)
	data.DiffFindings = claimsGuardianFindingTitles(review.Findings)
	data.Allowed = len(data.DiffFindings) == 0
	return data
}

func (b *ClaimsBackend) rollback(ctx context.Context, data claims.GuardianDecisionArtifactData, args claimsGuardianArgs) claims.GuardianDecisionArtifactData {
	if args.RollbackReceipt != "" {
		data.RollbackReceipt = args.RollbackReceipt
		data.Allowed = true
		return data
	}
	result, err := b.guardian.requestApprovalDetailed(ctx, &GitMutationProposal{
		ID:        "claims_rollback_" + b.clock().Format(time.RFC3339Nano),
		Op:        "rollback",
		Reason:    firstClaimsGuardianString(data.ApprovalSubject, "rollback authorization requested"),
		Timestamp: b.clock(),
	})
	if err != nil {
		data.FailureReason = err.Error()
		return data
	}
	data.Allowed = result.Approved
	data.UserDecision = string(result.Decision)
	data.DenialReason = claimsGuardianDenial(result.Approved, result.Reason)
	return data
}

func commandApprovalRequest(data claims.GuardianDecisionArtifactData, args claimsGuardianArgs) *commandapproval.Request {
	return &commandapproval.Request{
		Command:        firstClaimsGuardianString(args.Command, data.ApprovalSubject),
		WorkingDir:     args.WorkingDir,
		WorkspaceRoot:  args.WorkspaceRoot,
		ToolName:       firstClaimsGuardianString(args.ToolName, "command"),
		Domain:         args.Domain,
		Justification:  args.Justification,
		AgentID:        args.AgentID,
		AgentType:      args.AgentType,
		SessionID:      args.SessionID,
		TaskID:         args.TaskID,
		PipelineID:     args.PipelineID,
		ApprovalPolicy: args.ApprovalPolicy,
	}
}

func claimsGuardianFindingTitles(findings []Finding) []string {
	out := make([]string, 0, len(findings))
	for _, finding := range findings {
		out = append(out, firstClaimsGuardianString(finding.Title, string(finding.Type)))
	}
	return out
}

func claimsGuardianDenial(allowed bool, reason string) string {
	if allowed {
		return ""
	}
	return firstClaimsGuardianString(reason, "guardian denied request")
}

func decodeClaimsGuardianArgs(args map[string]any) (claimsGuardianArgs, error) {
	var out claimsGuardianArgs
	raw, err := json.Marshal(args)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("decode guardian claims args: %w", err)
	}
	return out, nil
}

func firstClaimsGuardianClock(clock func() time.Time) func() time.Time {
	if clock != nil {
		return clock
	}
	return time.Now
}

func firstClaimsGuardianString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
