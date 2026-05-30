package guardian

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/commandapproval"
)

const (
	guardianServiceParticipantID            = "sys:guardian_service"
	guardianServiceInvocationTimeout        = 30 * time.Second
	guardianServiceInvocationQueueCapacity  = 1
	guardianServiceInvocationWorkerCapacity = 1
)

type guardianServiceClaimKey struct{}

func (g *Guardian) evaluateCommandApprovalService(ctx context.Context, req *commandapproval.Request) (commandapproval.Evaluation, bool, error) {
	if g == nil || req == nil || guardianServiceClaimActive(ctx) {
		return commandapproval.Evaluation{}, false, nil
	}
	board := g.guardianBoardForSession(req.SessionID)
	if board == nil {
		return commandapproval.Evaluation{}, false, nil
	}
	tool := claims.GuardianToolApproveCommand
	if commandapproval.IsFetchToolName(req.ToolName) {
		tool = claims.GuardianToolFetchApproval
	}
	data, err := g.invokeGuardianDecisionService(ctx, board, tool, guardianCommandArgs(req))
	if err != nil {
		return commandapproval.Evaluation{}, true, err
	}
	return commandEvaluationFromGuardianData(data), true, nil
}

func (g *Guardian) invokeGuardianDecisionService(ctx context.Context, board *claims.ClaimsBoard, tool string, args map[string]any) (claims.GuardianDecisionArtifactData, error) {
	participant, err := claims.NewParticipantRegistration(
		claims.ParticipantCategoryService,
		guardianServiceParticipantID,
		map[string]string{"session_id": firstGuardianServiceString(board.SessionID(), g.activeSessionID, board.BoardID())},
		guardianServiceInvocationQueueCapacity,
		guardianServiceInvocationWorkerCapacity,
		guardianServiceInvocationTimeout,
		claims.HandlerDeterminismSideEffect,
		[]claims.ActionType{claims.ActionTypeTask},
	)
	if err != nil {
		return claims.GuardianDecisionArtifactData{}, err
	}
	result, err := claims.InvokeServiceClaim(ctx, claims.ServiceInvocationOptions{
		Board:          board,
		Handler:        claims.NewGuardianService(claims.InfrastructureServiceConfig{GuardianBackend: NewClaimsBackend(ClaimsBackendConfig{Guardian: g})}),
		Participant:    participant,
		IssuerID:       firstGuardianServiceString(stringArg(args, "agent_id"), "guardian"),
		SubjectID:      guardianServiceParticipantID,
		Title:          "Guardian gate: " + tool,
		Description:    "Run deterministic guardian gate through the guardian service.",
		ActionType:     claims.ActionTypeTask,
		ExpectedCall:   claims.ExpectedToolCall{Tool: tool, Arguments: args},
		IdempotencyKey: guardianServiceIdempotencyKey(tool, args),
		Reason:         "guardian service claim generated",
	})
	if err != nil {
		return claims.GuardianDecisionArtifactData{}, err
	}
	return guardianDataFromServiceResult(result.Result)
}

func guardianDataFromServiceResult(result claims.ServiceClaimResult) (claims.GuardianDecisionArtifactData, error) {
	for _, artifact := range result.Artifacts {
		if artifact == nil {
			continue
		}
		if data, err := claims.ArtifactData[claims.GuardianDecisionArtifactData](artifact); err == nil {
			return data, nil
		}
	}
	return claims.GuardianDecisionArtifactData{}, nil
}

func guardianCommandArgs(req *commandapproval.Request) map[string]any {
	return map[string]any{
		"command":         req.Command,
		"working_dir":     req.WorkingDir,
		"workspace_root":  req.WorkspaceRoot,
		"tool_name":       req.ToolName,
		"domain":          req.Domain,
		"justification":   req.Justification,
		"agent_id":        req.AgentID,
		"agent_type":      req.AgentType,
		"session_id":      req.SessionID,
		"task_id":         req.TaskID,
		"pipeline_id":     req.PipelineID,
		"approval_policy": req.ApprovalPolicy,
	}
}

func commandEvaluationFromGuardianData(data claims.GuardianDecisionArtifactData) commandapproval.Evaluation {
	decision := commandapproval.DecisionDeny
	if data.Allowed && data.FailureReason == "" && data.DenialReason == "" {
		decision = commandapproval.DecisionAllow
	}
	source := commandapproval.MatchSourceInteractive
	if value, ok := data.Metadata["source"].(string); ok && strings.TrimSpace(value) != "" {
		source = commandapproval.MatchSource(strings.TrimSpace(value))
	}
	return commandapproval.Evaluation{
		Decision:     decision,
		Source:       source,
		UserDecision: strings.TrimSpace(data.UserDecision),
		Reason:       firstGuardianServiceString(data.DenialReason, data.FailureReason),
	}
}

func guardianScanServiceResult(data claims.GuardianDecisionArtifactData) map[string]any {
	return map[string]any{
		"findings":      append([]string(nil), data.DiffFindings...),
		"finding_count": len(data.DiffFindings),
		"clean":         data.Allowed && data.FailureReason == "",
	}
}

func guardianDiffServiceResult(data claims.GuardianDecisionArtifactData) map[string]any {
	clean := len(data.DiffFindings) == 0 && data.FailureReason == ""
	return map[string]any{
		"clean":         clean,
		"findings":      append([]string(nil), data.DiffFindings...),
		"finding_count": len(data.DiffFindings),
	}
}

func guardianBranchServiceResult(data claims.GuardianDecisionArtifactData, patterns []string) map[string]any {
	return map[string]any{
		"branch":    data.ApprovalSubject,
		"protected": data.BranchProtected,
		"patterns":  append([]string(nil), patterns...),
	}
}

func guardianRollbackServiceResult(data claims.GuardianDecisionArtifactData, snapshotRef string) map[string]any {
	status := "denied"
	message := firstGuardianServiceString(data.DenialReason, data.FailureReason, "User denied rollback.")
	if data.Allowed && data.FailureReason == "" {
		status = "approved"
		message = "Rollback approved. Execution delegated to git subsystem."
	}
	return map[string]any{
		"status":       status,
		"snapshot_ref": snapshotRef,
		"message":      message,
	}
}

func reviewGateActionUsesGuardianService(action string) bool {
	switch strings.TrimSpace(action) {
	case "review_diff", "pre_commit_check":
		return true
	default:
		return false
	}
}

func (g *Guardian) readClaimsServiceFile(path string) (string, error) {
	if g == nil || g.fileAccess == nil {
		return "", nil
	}
	data, err := g.fileAccess.ReadFile(context.Background(), path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (g *Guardian) guardianBoardForSession(sessionID string) *claims.ClaimsBoard {
	if g == nil {
		return nil
	}
	if board := g.guardianBoard(); board != nil {
		return board
	}
	if trimmed := strings.TrimSpace(sessionID); trimmed != "" {
		return claims.DefaultSessionBoardRegistry().Lookup(trimmed)
	}
	return nil
}

func withGuardianServiceClaim(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, guardianServiceClaimKey{}, true)
}

func guardianServiceClaimActive(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	value, _ := ctx.Value(guardianServiceClaimKey{}).(bool)
	return value
}

func guardianServiceIdempotencyKey(tool string, args map[string]any) string {
	return "guardian.service:" + tool + ":" + guardianServiceHash(args)
}

func guardianServiceHash(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		raw = []byte{}
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func stringArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}

func firstGuardianServiceString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
