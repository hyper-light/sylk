package claims

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/security"
	"github.com/google/uuid"
)

const (
	ArtifactKindExpectedToolInvocation = "expected_tool_invocation"
	ArtifactKindExpectedToolOutput     = "expected_tool_output"
	ArtifactKindExpectedToolSkipped    = "expected_tool_skipped"

	ExpectedToolMetadataValidationID         = "expected_tool_validation_id"
	ExpectedToolMetadataInvocationArtifactID = "expected_tool_invocation_artifact_id"
	ExpectedToolMetadataArguments            = "expected_tool_arguments"
	ExpectedToolMetadataOutput               = "expected_tool_output"
	ExpectedToolMetadataStatus               = "expected_tool_status"
	ExpectedToolMetadataPolicyDecision       = "expected_tool_policy_decision"
	ExpectedToolMetadataValidationStatus     = "expected_tool_validation_status"
	ExpectedToolMetadataValidationReason     = "expected_tool_validation_reason"
)

type ExpectedToolExecutionStatus string

const (
	ExpectedToolExecutionSucceeded    ExpectedToolExecutionStatus = "succeeded"
	ExpectedToolExecutionFailed       ExpectedToolExecutionStatus = "failed"
	ExpectedToolExecutionSkipped      ExpectedToolExecutionStatus = "skipped"
	ExpectedToolExecutionPolicyDenied ExpectedToolExecutionStatus = "policy_denied"
)

// ExpectedToolExecutionOutput is the deterministic result returned by a
// validation expected-tool executor. Output is captured into an audit
// artifact when Artifacts is empty; Artifacts can be used when the tool
// already produces structured board evidence.
type ExpectedToolExecutionOutput struct {
	Output    any            `json:"output,omitempty"`
	Summary   string         `json:"summary,omitempty"`
	Artifacts []*Artifact    `json:"artifacts,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// ExpectedToolExecutor executes one expected Sylk skill invocation.
// Callers usually adapt the active tool runtime so ordinary tool policy
// and permission checks remain authoritative.
type ExpectedToolExecutor interface {
	ExecuteExpectedTool(ctx context.Context, call ExpectedToolCall) (ExpectedToolExecutionOutput, error)
}

type ExpectedToolPolicyRequest struct {
	Claim      *Claim
	Validation *Validation
	Call       ExpectedToolCall
	AgentID    string
}

type ExpectedToolPolicyDecision struct {
	Allowed     bool
	Reason      string
	FailureKind string
}

type ExpectedToolPolicy interface {
	DecideExpectedTool(ctx context.Context, req ExpectedToolPolicyRequest) ExpectedToolPolicyDecision
}

type ExpectedToolArgumentRedactor interface {
	RedactExpectedToolArguments(ctx context.Context, tool string, args map[string]any) (map[string]any, error)
}

type ValidationExpectedToolRemediationPoster interface {
	PostValidationExpectedToolRemediation(ctx context.Context, board *ClaimsBoard, claim *Claim, validation *Validation, result *ValidationExpectedToolExecutionResult) ([]string, error)
}

type ExpectedToolExecutionOptions struct {
	AgentID string

	Executor ExpectedToolExecutor
	Policy   ExpectedToolPolicy
	Redactor ExpectedToolArgumentRedactor

	// AllowedTools, when non-empty, is an allowlist applied before
	// Policy. It is deliberately simple so tests and constrained
	// validators can make denial deterministic without inventing a
	// second policy subsystem.
	AllowedTools map[string]bool

	// ApprovedToolIDs contains expected-tool call IDs that have already
	// received any required user approval.
	ApprovedToolIDs map[string]bool

	RemediationPoster ValidationExpectedToolRemediationPoster
}

type ExpectedToolAttemptResult struct {
	ExpectedToolCallID   string                      `json:"expected_tool_call_id"`
	Tool                 string                      `json:"tool"`
	Required             bool                        `json:"required"`
	Status               ExpectedToolExecutionStatus `json:"status"`
	InvocationArtifactID string                      `json:"invocation_artifact_id,omitempty"`
	OutputArtifactIDs    []string                    `json:"output_artifact_ids,omitempty"`
	OutputArtifactKinds  []string                    `json:"output_artifact_kinds,omitempty"`
	ErrorArtifactID      string                      `json:"error_artifact_id,omitempty"`
	ErrorKind            string                      `json:"error_kind,omitempty"`
	Error                string                      `json:"error,omitempty"`
	PolicyReason         string                      `json:"policy_reason,omitempty"`
	ValidationStatus     ValidationStatus            `json:"validation_status,omitempty"`
	ValidationReason     string                      `json:"validation_reason,omitempty"`
}

type ValidationExpectedToolExecutionResult struct {
	ClaimID              string                      `json:"claim_id"`
	ValidationID         string                      `json:"validation_id"`
	AgentID              string                      `json:"agent_id"`
	ValidationActorID    string                      `json:"validation_actor_id,omitempty"`
	AlreadyTerminal      bool                        `json:"already_terminal,omitempty"`
	Attempts             []ExpectedToolAttemptResult `json:"attempts,omitempty"`
	TestamentID          string                      `json:"testament_id,omitempty"`
	ArtifactIDs          []string                    `json:"artifact_ids,omitempty"`
	ValidationEvaluated  bool                        `json:"validation_evaluated,omitempty"`
	ValidationStatus     ValidationStatus            `json:"validation_status,omitempty"`
	ValidationReason     string                      `json:"validation_reason,omitempty"`
	RemediationClaimIDs  []string                    `json:"remediation_claim_ids,omitempty"`
	RemediationErrorText string                      `json:"remediation_error,omitempty"`
}

// ExecuteValidationExpectedTools executes the ExpectedToolCalls attached
// to one pending validation, records a testament containing audit
// artifacts for every attempted/skipped/denied tool, then evaluates the
// validation with a verdict that cites those artifacts.
func ExecuteValidationExpectedTools(ctx context.Context, board *ClaimsBoard, claimID, validationID string, opts ExpectedToolExecutionOptions) (*ValidationExpectedToolExecutionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if board == nil {
		return nil, fmt.Errorf("claims board not available")
	}
	claimID = strings.TrimSpace(claimID)
	validationID = strings.TrimSpace(validationID)
	if claimID == "" {
		return nil, fmt.Errorf("claim_id is required")
	}
	if validationID == "" {
		return nil, fmt.Errorf("validation_id is required")
	}
	claim, ok := board.CloneClaim(claimID)
	if !ok {
		return nil, fmt.Errorf("claim %q not found", claimID)
	}
	validation := findValidationOnClaim(claim, validationID)
	if validation == nil {
		return nil, fmt.Errorf("validation %q not found on claim %q", validationID, claimID)
	}
	result := &ValidationExpectedToolExecutionResult{
		ClaimID:           claimID,
		ValidationID:      validationID,
		AgentID:           firstNonEmpty(strings.TrimSpace(opts.AgentID), validation.AgentID, IssuerAgentID(claim.Relations), claim.AgentID),
		ValidationActorID: expectedToolValidationActorID(claim, validation, opts.AgentID),
	}
	if validation.Status.IsTerminal() {
		result.AlreadyTerminal = true
		result.ValidationStatus = validation.Status
		return result, nil
	}
	if len(validation.ExpectedToolCalls) == 0 {
		return result, nil
	}
	if err := ValidateExpectedToolCalls(validation.ExpectedToolCalls, nil); err != nil {
		return nil, fmt.Errorf("validation %q expected tool calls: %w", validationID, err)
	}

	var artifacts []*Artifact
	for _, call := range validation.ExpectedToolCalls {
		attempt, attemptArtifacts := executeExpectedToolAttempt(ctx, claim, validation, call, opts, result.AgentID)
		result.Attempts = append(result.Attempts, attempt)
		artifacts = append(artifacts, attemptArtifacts...)
	}
	if len(artifacts) == 0 {
		return result, nil
	}

	for _, artifact := range artifacts {
		if artifact == nil {
			continue
		}
		if artifact.ID == "" {
			artifact.ID = uuid.NewString()
		}
		if artifact.AgentID == "" {
			artifact.AgentID = result.AgentID
		}
		result.ArtifactIDs = append(result.ArtifactIDs, artifact.ID)
	}

	testament := Testament{
		AgentID:      result.AgentID,
		Summary:      validationExpectedToolSummary(result.Attempts),
		Confidence:   "deterministic",
		Relations:    validationExpectedToolRelations(claimID, validationID),
		Artifacts:    artifacts,
		Presentation: nil,
	}
	testamentID, err := postExpectedToolAuditTestament(contextWithoutCancellation(ctx), board, result, testament)
	if err != nil {
		return nil, err
	}
	result.TestamentID = testamentID

	status := validationExpectedToolStatus(result.Attempts)
	reason := validationExpectedToolValidationReason(status, result)
	if err := beginExpectedToolAuditTestamentValidation(contextWithoutCancellation(ctx), board, result, testamentID); err != nil {
		return nil, err
	}
	latestClaim, ok := board.CloneClaim(claimID)
	if !ok {
		return nil, fmt.Errorf("claim %q disappeared before validation evaluation", claimID)
	}
	latestValidation := findValidationOnClaim(latestClaim, validationID)
	if latestValidation == nil {
		return nil, fmt.Errorf("validation %q disappeared before validation evaluation", validationID)
	}
	if latestValidation.Status.IsTerminal() {
		result.AlreadyTerminal = true
		result.ValidationStatus = latestValidation.Status
		return result, nil
	}
	if err := board.EvaluateValidation(contextWithoutCancellation(ctx), claimID, validationID, StatusChange{
		To:      string(status),
		Reason:  reason,
		AgentID: result.AgentID,
	}); err != nil {
		return nil, fmt.Errorf("evaluate validation after expected-tool audit: %w", err)
	}
	result.ValidationEvaluated = true
	result.ValidationStatus = status
	result.ValidationReason = reason
	if err := completeExpectedToolAuditTestamentValidation(contextWithoutCancellation(ctx), board, result, testamentID, status, reason); err != nil {
		return nil, err
	}

	if status.IsNegativeTerminal() && opts.RemediationPoster != nil {
		ids, err := opts.RemediationPoster.PostValidationExpectedToolRemediation(contextWithoutCancellation(ctx), board, claim, validation, result)
		result.RemediationClaimIDs = append([]string(nil), ids...)
		if err != nil {
			result.RemediationErrorText = err.Error()
		}
	}
	return result, nil
}

func expectedToolValidationActorID(claim *Claim, validation *Validation, requested string) string {
	requested = strings.TrimSpace(requested)
	if agentCanValidateClaim(claim, requested) {
		return requested
	}
	if validation != nil && agentCanValidateClaim(claim, validation.AgentID) {
		return strings.TrimSpace(validation.AgentID)
	}
	if issuer := IssuerAgentID(claim.Relations); issuer != "" {
		return issuer
	}
	return firstNonEmpty(requested, validationStatusAgentID(validation), claim.AgentID)
}

func validationStatusAgentID(validation *Validation) string {
	if validation == nil {
		return ""
	}
	return strings.TrimSpace(validation.AgentID)
}

func agentCanValidateClaim(claim *Claim, agentID string) bool {
	agentID = strings.TrimSpace(agentID)
	if claim == nil || agentID == "" {
		return false
	}
	return HasRelation(claim.Relations, RelationshipIssuer, agentID) ||
		HasRelation(claim.Relations, RelationshipEvaluator, agentID)
}

func postExpectedToolAuditTestament(ctx context.Context, board *ClaimsBoard, result *ValidationExpectedToolExecutionResult, testament Testament) (string, error) {
	if result == nil {
		return "", fmt.Errorf("expected-tool result is required")
	}
	actorID := firstNonEmpty(result.AgentID, result.ValidationActorID, "validator")
	generated, err := board.GenerateTestamentAction(ctx, Action{AgentID: actorID, Type: ActionTypeTestament}, []Testament{testament}, GenerateTestamentActionOptions{
		IdempotencyKey: "expected_tool_validation:" + result.ClaimID + ":" + result.ValidationID,
		Reason:         "validation expected-tool audit testament generated",
	})
	if err != nil {
		return "", fmt.Errorf("generate validation expected-tool audit testament: %w", err)
	}
	if generated == nil || len(generated.Testaments) == 0 {
		return "", fmt.Errorf("generate validation expected-tool audit testament returned no testaments")
	}
	testamentID := strings.TrimSpace(generated.Testaments[0].ID)
	if testamentID == "" {
		return "", fmt.Errorf("generate validation expected-tool audit testament returned empty testament id")
	}
	if err := board.PostGeneratedTestament(ctx, testamentID, actorID, TestamentPostOptions{Reason: "validation expected-tool audit testament posted"}); err != nil {
		return "", fmt.Errorf("post validation expected-tool audit testament: %w", err)
	}
	return testamentID, nil
}

func beginExpectedToolAuditTestamentValidation(ctx context.Context, board *ClaimsBoard, result *ValidationExpectedToolExecutionResult, testamentID string) error {
	actorID := expectedToolLifecycleActor(result)
	if err := board.AcknowledgeTestamentReceipt(ctx, testamentID, actorID); err != nil {
		return fmt.Errorf("acknowledge validation expected-tool audit testament: %w", err)
	}
	if err := board.BeginTestamentValidation(ctx, testamentID, actorID); err != nil {
		return fmt.Errorf("begin validation expected-tool audit testament validation: %w", err)
	}
	return nil
}

func completeExpectedToolAuditTestamentValidation(ctx context.Context, board *ClaimsBoard, result *ValidationExpectedToolExecutionResult, testamentID string, status ValidationStatus, reason string) error {
	if err := board.CompleteTestamentValidation(ctx, testamentID, expectedToolLifecycleActor(result), testamentLifecycleForValidationStatus(status), reason); err != nil {
		return fmt.Errorf("complete validation expected-tool audit testament validation: %w", err)
	}
	return nil
}

func expectedToolLifecycleActor(result *ValidationExpectedToolExecutionResult) string {
	if result == nil {
		return "validator"
	}
	return firstNonEmpty(result.ValidationActorID, result.AgentID, "validator")
}

func testamentLifecycleForValidationStatus(status ValidationStatus) TestamentLifecycleStatus {
	switch status {
	case ValidationStatusPassed:
		return TestamentLifecycleValidated
	case ValidationStatusIncomplete:
		return TestamentLifecycleValidationIncomplete
	case ValidationStatusFailed:
		return TestamentLifecycleValidationFailed
	case ValidationStatusErrored:
		return TestamentLifecycleValidationErrored
	default:
		return TestamentLifecycleValidationErrored
	}
}

func executeExpectedToolAttempt(ctx context.Context, claim *Claim, validation *Validation, call ExpectedToolCall, opts ExpectedToolExecutionOptions, agentID string) (ExpectedToolAttemptResult, []*Artifact) {
	attempt := ExpectedToolAttemptResult{
		ExpectedToolCallID: strings.TrimSpace(call.ID),
		Tool:               strings.TrimSpace(call.Tool),
		Required:           call.Required,
	}
	var artifacts []*Artifact
	redactedArgs, err := redactExpectedToolArguments(ctx, opts.Redactor, call)
	if err != nil {
		artifact := expectedToolSafeFailureArtifact(call, agentID, validation.ID, ArtifactKindErrorDiagnostic, "expected tool argument redaction failed")
		artifact.Metadata["redaction_error"] = err.Error()
		attempt.Status = ExpectedToolExecutionFailed
		attempt.ErrorKind = artifact.Kind
		attempt.Error = artifact.Reference
		attempt.ErrorArtifactID = artifact.ID
		attempt.ValidationStatus = ValidationStatusErrored
		attempt.ValidationReason = "expected tool argument redaction failed"
		return attempt, []*Artifact{artifact}
	}

	decision := expectedToolPolicyDecision(ctx, claim, validation, call, opts, agentID)
	if !decision.Allowed {
		kind := firstNonEmpty(strings.TrimSpace(decision.FailureKind), ArtifactKindPolicyDenied)
		reason := firstNonEmpty(strings.TrimSpace(decision.Reason), "expected tool execution denied by policy")
		artifact := expectedToolSafeFailureArtifact(call, agentID, validation.ID, kind, reason)
		artifact.Metadata[ExpectedToolMetadataPolicyDecision] = reason
		artifact.Metadata[ExpectedToolMetadataArguments] = redactedArgs
		attempt.Status = ExpectedToolExecutionPolicyDenied
		attempt.PolicyReason = reason
		attempt.ErrorKind = artifact.Kind
		attempt.Error = artifact.Reference
		attempt.ErrorArtifactID = artifact.ID
		attempt.ValidationStatus = ValidationStatusErrored
		attempt.ValidationReason = reason
		return attempt, []*Artifact{artifact}
	}

	invocation := expectedToolInvocationArtifact(call, agentID, validation.ID, redactedArgs)
	attempt.InvocationArtifactID = invocation.ID
	artifacts = append(artifacts, invocation)
	if opts.Executor == nil {
		kind := ArtifactKindExpectedToolSkipped
		if call.Required {
			kind = ArtifactKindErrorDiagnostic
		}
		artifact := expectedToolSafeFailureArtifact(call, agentID, validation.ID, kind, "expected tool executor is not configured")
		artifact.Metadata[ExpectedToolMetadataInvocationArtifactID] = invocation.ID
		artifact.Metadata[ExpectedToolMetadataArguments] = redactedArgs
		linkExpectedToolArtifactToInvocation(artifact, invocation.ID)
		attempt.Status = ExpectedToolExecutionSkipped
		if call.Required {
			attempt.Status = ExpectedToolExecutionFailed
			attempt.ValidationStatus = ValidationStatusErrored
			attempt.ValidationReason = "expected tool executor is not configured"
		}
		attempt.ErrorKind = artifact.Kind
		attempt.Error = artifact.Reference
		attempt.ErrorArtifactID = artifact.ID
		return attempt, append(artifacts, artifact)
	}

	execCtx := ctx
	var cancel context.CancelFunc
	if call.TimeoutSeconds > 0 {
		execCtx, cancel = context.WithTimeout(ctx, time.Duration(call.TimeoutSeconds)*time.Second)
		defer cancel()
	}
	output, execErr := invokeExpectedToolSafely(execCtx, opts.Executor, call)
	if execErr != nil {
		kind := expectedToolFailureKind(execErr)
		artifact := ExpectedToolFailureArtifact(call, agentID, kind, execErr.Error())
		artifact.ID = uuid.NewString()
		artifact.Metadata[ExpectedToolMetadataValidationID] = validation.ID
		artifact.Metadata[ExpectedToolMetadataInvocationArtifactID] = invocation.ID
		artifact.Metadata[ExpectedToolMetadataArguments] = redactedArgs
		linkExpectedToolArtifactToInvocation(artifact, invocation.ID)
		attempt.Status = ExpectedToolExecutionFailed
		attempt.ErrorKind = artifact.Kind
		attempt.Error = artifact.Reference
		attempt.ErrorArtifactID = artifact.ID
		attempt.ValidationStatus = ValidationStatusErrored
		attempt.ValidationReason = execErr.Error()
		return attempt, append(artifacts, artifact)
	}

	outArtifacts := expectedToolOutputArtifacts(call, agentID, validation.ID, invocation.ID, output)
	for _, artifact := range outArtifacts {
		if artifact == nil {
			continue
		}
		attempt.OutputArtifactIDs = append(attempt.OutputArtifactIDs, artifact.ID)
		attempt.OutputArtifactKinds = append(attempt.OutputArtifactKinds, strings.TrimSpace(artifact.Kind))
	}
	attempt.Status = ExpectedToolExecutionSucceeded
	attempt.ValidationStatus, attempt.ValidationReason = expectedToolOutputValidationStatus(call, output, outArtifacts)
	if attempt.ValidationStatus == ValidationStatusIncomplete {
		missing := expectedToolMissingEvidenceArtifact(call, agentID, validation.ID, invocation.ID, attempt.ValidationReason)
		attempt.ErrorArtifactID = missing.ID
		attempt.ErrorKind = missing.Kind
		attempt.Error = missing.Reference
		return attempt, append(artifacts, append(outArtifacts, missing)...)
	}
	return attempt, append(artifacts, outArtifacts...)
}

func redactExpectedToolArguments(ctx context.Context, redactor ExpectedToolArgumentRedactor, call ExpectedToolCall) (map[string]any, error) {
	args := cloneAnyMap(call.Arguments)
	if len(args) == 0 {
		return nil, nil
	}
	if redactor != nil {
		return redactor.RedactExpectedToolArguments(ctx, call.Tool, args)
	}
	redacted, ok := redactExpectedToolValue(args).(map[string]any)
	if !ok {
		return nil, nil
	}
	return redacted, nil
}

func expectedToolPolicyDecision(ctx context.Context, claim *Claim, validation *Validation, call ExpectedToolCall, opts ExpectedToolExecutionOptions, agentID string) ExpectedToolPolicyDecision {
	tool := strings.TrimSpace(call.Tool)
	if len(opts.AllowedTools) > 0 && !opts.AllowedTools[tool] {
		return ExpectedToolPolicyDecision{Allowed: false, FailureKind: ArtifactKindPolicyDenied, Reason: "expected tool is not allowed for this validation"}
	}
	if call.Policy.RequiresUserApproval && !opts.ApprovedToolIDs[strings.TrimSpace(call.ID)] {
		return ExpectedToolPolicyDecision{Allowed: false, FailureKind: ArtifactKindPolicyDenied, Reason: "expected tool requires user approval"}
	}
	if expectedToolIsPeerRouting(tool) && !call.Policy.AllowAgentSubstitution {
		return ExpectedToolPolicyDecision{Allowed: false, FailureKind: ArtifactKindPolicyDenied, Reason: "expected validation tools may not route peer work unless delegation is explicitly authorized"}
	}
	if opts.Policy != nil {
		decision := opts.Policy.DecideExpectedTool(ctx, ExpectedToolPolicyRequest{Claim: claim, Validation: validation, Call: call, AgentID: agentID})
		if !decision.Allowed {
			if strings.TrimSpace(decision.FailureKind) == "" {
				decision.FailureKind = ArtifactKindPolicyDenied
			}
			return decision
		}
	}
	return ExpectedToolPolicyDecision{Allowed: true}
}

func expectedToolIsPeerRouting(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "consult_peer", "challenge_peer", "guardian_check", "post_action":
		return true
	default:
		return false
	}
}

func expectedToolInvocationArtifact(call ExpectedToolCall, agentID, validationID string, redactedArgs map[string]any) *Artifact {
	return &Artifact{
		ID:        uuid.NewString(),
		AgentID:   agentID,
		Kind:      ArtifactKindExpectedToolInvocation,
		Reference: strings.TrimSpace(call.Tool),
		Metadata: map[string]any{
			ExpectedToolMetadataID:           strings.TrimSpace(call.ID),
			ExpectedToolMetadataTool:         strings.TrimSpace(call.Tool),
			ExpectedToolMetadataPurpose:      strings.TrimSpace(call.Purpose),
			ExpectedToolMetadataRequired:     call.Required,
			ExpectedToolMetadataValidationID: validationID,
			ExpectedToolMetadataArguments:    redactedArgs,
			ExpectedToolMetadataStatus:       "started",
		},
	}
}

func expectedToolOutputArtifacts(call ExpectedToolCall, agentID, validationID, invocationArtifactID string, output ExpectedToolExecutionOutput) []*Artifact {
	metadataBase := map[string]any{
		ExpectedToolMetadataID:                   strings.TrimSpace(call.ID),
		ExpectedToolMetadataTool:                 strings.TrimSpace(call.Tool),
		ExpectedToolMetadataRequired:             call.Required,
		ExpectedToolMetadataValidationID:         validationID,
		ExpectedToolMetadataInvocationArtifactID: invocationArtifactID,
		ExpectedToolMetadataStatus:               string(ExpectedToolExecutionSucceeded),
	}
	if len(output.Metadata) > 0 {
		metadataBase["executor_metadata"] = sortedAnyMap(output.Metadata)
	}
	out := make([]*Artifact, 0, len(output.Artifacts)+1)
	for _, artifact := range output.Artifacts {
		if artifact == nil {
			continue
		}
		cp := CloneArtifact(artifact)
		if cp.ID == "" {
			cp.ID = uuid.NewString()
		}
		if cp.AgentID == "" {
			cp.AgentID = agentID
		}
		if cp.Kind == "" {
			cp.Kind = ArtifactKindExpectedToolOutput
		}
		if cp.Metadata == nil {
			cp.Metadata = make(map[string]any, len(metadataBase))
		}
		for key, value := range metadataBase {
			cp.Metadata[key] = value
		}
		linkExpectedToolArtifactToInvocation(cp, invocationArtifactID)
		out = append(out, cp)
	}
	if len(out) == 0 {
		artifact := &Artifact{
			ID:        uuid.NewString(),
			AgentID:   agentID,
			Kind:      ArtifactKindExpectedToolOutput,
			Reference: expectedToolOutputReference(output),
			Metadata:  cloneAnyMap(metadataBase),
		}
		linkExpectedToolArtifactToInvocation(artifact, invocationArtifactID)
		out = append(out, artifact)
	}
	return out
}

func expectedToolOutputValidationStatus(call ExpectedToolCall, output ExpectedToolExecutionOutput, artifacts []*Artifact) (ValidationStatus, string) {
	explicit := explicitExpectedToolValidationStatus(output)
	if explicit == ValidationStatusErrored {
		return explicit, explicitExpectedToolValidationReason(output, "expected tool output reported validator error")
	}
	if missing := expectedToolMissingEvidenceKinds(call, artifacts); len(missing) > 0 {
		return ValidationStatusIncomplete, "expected tool did not produce required artifact kinds: " + strings.Join(missing, ",")
	}
	if explicit.IsTerminal() {
		return explicit, explicitExpectedToolValidationReason(output, "expected tool output reported "+string(explicit))
	}
	return ValidationStatusPassed, ""
}

func explicitExpectedToolValidationStatus(output ExpectedToolExecutionOutput) ValidationStatus {
	if status, ok := parseValidationStatus(output.Metadata[ExpectedToolMetadataValidationStatus]); ok {
		return status
	}
	if status, ok := parseValidationStatus(output.Metadata["validation_status"]); ok {
		return status
	}
	if status, ok := outputMapValidationStatus(output.Output); ok {
		return status
	}
	return ""
}

func outputMapValidationStatus(output any) (ValidationStatus, bool) {
	values, ok := output.(map[string]any)
	if !ok {
		return "", false
	}
	if status, ok := parseValidationStatus(values["validation_status"]); ok {
		return status, true
	}
	if passed, ok := anyBool(values["passed"]); ok && !passed {
		return ValidationStatusFailed, true
	}
	if success, ok := anyBool(values["success"]); ok && !success {
		return ValidationStatusFailed, true
	}
	return "", false
}

func parseValidationStatus(value any) (ValidationStatus, bool) {
	raw, ok := value.(string)
	if !ok {
		return "", false
	}
	status := ValidationStatus(strings.TrimSpace(raw))
	if status.IsTerminal() {
		return status, true
	}
	return "", false
}

func explicitExpectedToolValidationReason(output ExpectedToolExecutionOutput, fallback string) string {
	if reason, ok := output.Metadata[ExpectedToolMetadataValidationReason].(string); ok && strings.TrimSpace(reason) != "" {
		return strings.TrimSpace(reason)
	}
	if reason, ok := output.Metadata["validation_reason"].(string); ok && strings.TrimSpace(reason) != "" {
		return strings.TrimSpace(reason)
	}
	if values, ok := output.Output.(map[string]any); ok {
		if reason, ok := values["validation_reason"].(string); ok && strings.TrimSpace(reason) != "" {
			return strings.TrimSpace(reason)
		}
	}
	return fallback
}

func expectedToolMissingEvidenceKinds(call ExpectedToolCall, artifacts []*Artifact) []string {
	required := normalizedProducedArtifactKinds(call.ProducesArtifacts)
	if len(required) == 0 {
		return nil
	}
	seen := artifactKindSet(artifacts)
	missing := make([]string, 0, len(required))
	for _, kind := range required {
		if !seen[kind] {
			missing = append(missing, kind)
		}
	}
	return missing
}

func normalizedProducedArtifactKinds(kinds []string) []string {
	seen := make(map[string]bool, len(kinds))
	out := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		kind = strings.TrimSpace(kind)
		if kind == "" || seen[kind] {
			continue
		}
		seen[kind] = true
		out = append(out, kind)
	}
	return out
}

func artifactKindSet(artifacts []*Artifact) map[string]bool {
	seen := make(map[string]bool, len(artifacts))
	for _, artifact := range artifacts {
		if artifact == nil {
			continue
		}
		if kind := strings.TrimSpace(artifact.Kind); kind != "" {
			seen[kind] = true
		}
	}
	return seen
}

func expectedToolMissingEvidenceArtifact(call ExpectedToolCall, agentID, validationID, invocationArtifactID, reason string) *Artifact {
	artifact := &Artifact{
		ID:        uuid.NewString(),
		AgentID:   strings.TrimSpace(agentID),
		Kind:      ArtifactKindMissingEvidence,
		Reference: firstNonEmpty(reason, "expected tool did not produce required evidence"),
		Metadata: map[string]any{
			ExpectedToolMetadataID:               strings.TrimSpace(call.ID),
			ExpectedToolMetadataTool:             strings.TrimSpace(call.Tool),
			ExpectedToolMetadataRequired:         call.Required,
			ExpectedToolMetadataValidationID:     validationID,
			ExpectedToolMetadataStatus:           string(ExpectedToolExecutionSucceeded),
			ExpectedToolMetadataValidationStatus: string(ValidationStatusIncomplete),
		},
	}
	linkExpectedToolArtifactToInvocation(artifact, invocationArtifactID)
	return artifact
}

func anyBool(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	default:
		return false, false
	}
}

func expectedToolOutputReference(output ExpectedToolExecutionOutput) string {
	if summary := strings.TrimSpace(output.Summary); summary != "" {
		return summary
	}
	if output.Output == nil {
		return "expected tool completed"
	}
	switch value := output.Output.(type) {
	case string:
		return truncateExpectedToolReference(value)
	default:
		blob, err := json.Marshal(value)
		if err != nil {
			return "expected tool completed"
		}
		return truncateExpectedToolReference(string(blob))
	}
}

func expectedToolSafeFailureArtifact(call ExpectedToolCall, agentID, validationID, failureKind, message string) *Artifact {
	if strings.TrimSpace(failureKind) == ArtifactKindExpectedToolSkipped {
		return &Artifact{
			ID:        uuid.NewString(),
			AgentID:   strings.TrimSpace(agentID),
			Kind:      ArtifactKindExpectedToolSkipped,
			Reference: firstNonEmpty(strings.TrimSpace(message), "expected tool call skipped"),
			Metadata: map[string]any{
				ExpectedToolMetadataID:           strings.TrimSpace(call.ID),
				ExpectedToolMetadataTool:         strings.TrimSpace(call.Tool),
				ExpectedToolMetadataRequired:     call.Required,
				ExpectedToolMetadataValidationID: validationID,
				ExpectedToolMetadataStatus:       string(ExpectedToolExecutionSkipped),
			},
		}
	}
	artifact := ExpectedToolFailureArtifact(call, agentID, failureKind, message)
	artifact.ID = uuid.NewString()
	if artifact.Metadata == nil {
		artifact.Metadata = map[string]any{}
	}
	artifact.Metadata[ExpectedToolMetadataValidationID] = validationID
	artifact.Metadata[ExpectedToolMetadataStatus] = string(ExpectedToolExecutionFailed)
	return artifact
}

func linkExpectedToolArtifactToInvocation(artifact *Artifact, invocationArtifactID string) {
	if artifact == nil {
		return
	}
	invocationArtifactID = strings.TrimSpace(invocationArtifactID)
	if invocationArtifactID == "" {
		return
	}
	if artifact.Metadata == nil {
		artifact.Metadata = map[string]any{}
	}
	artifact.Metadata[ExpectedToolMetadataInvocationArtifactID] = invocationArtifactID
	if !HasRelation(artifact.Relations, RelationshipCompletes, invocationArtifactID) {
		artifact.Relations = append(artifact.Relations, Relation{
			Related:      invocationArtifactID,
			RelatedType:  RelatedTypeArtifact,
			Relationship: RelationshipCompletes,
		})
	}
}

func invokeExpectedToolSafely(ctx context.Context, executor ExpectedToolExecutor, call ExpectedToolCall) (out ExpectedToolExecutionOutput, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("expected tool %q panicked: %v\n%s", strings.TrimSpace(call.Tool), recovered, debug.Stack())
		}
	}()
	return executor.ExecuteExpectedTool(ctx, call)
}

func expectedToolFailureKind(err error) string {
	if err == nil {
		return ArtifactKindError
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ArtifactKindToolTimeout
	}
	if errors.Is(err, context.Canceled) {
		return ArtifactKindErrorDiagnostic
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "permission denied"), strings.Contains(msg, "access denied"):
		return ArtifactKindPermissionDenied
	case strings.Contains(msg, "policy"):
		return ArtifactKindPolicyDenied
	case strings.Contains(msg, "missing dependency"), strings.Contains(msg, "not found"):
		return ArtifactKindMissingDependency
	default:
		return ArtifactKindError
	}
}

func validationExpectedToolRelations(claimID, validationID string) []Relation {
	return []Relation{
		{Related: claimID, RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim},
		{Related: validationID, RelatedType: RelatedTypeValidation, Relationship: RelationshipReviews},
	}
}

func validationExpectedToolSummary(attempts []ExpectedToolAttemptResult) string {
	if len(attempts) == 0 {
		return "No validation expected tools were attempted."
	}
	failed := 0
	for _, attempt := range attempts {
		if attempt.Required && attempt.Status != ExpectedToolExecutionSucceeded {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Sprintf("Validation expected tools completed with %d required failure(s).", failed)
	}
	return "Validation expected tools completed successfully."
}

func validationExpectedToolStatus(attempts []ExpectedToolAttemptResult) ValidationStatus {
	status := ValidationStatusPassed
	for _, attempt := range attempts {
		if !attempt.Required {
			continue
		}
		switch expectedToolAttemptValidationStatus(attempt) {
		case ValidationStatusErrored:
			return ValidationStatusErrored
		case ValidationStatusIncomplete:
			status = ValidationStatusIncomplete
		case ValidationStatusFailed:
			if status != ValidationStatusIncomplete {
				status = ValidationStatusFailed
			}
		}
	}
	return status
}

func expectedToolAttemptValidationStatus(attempt ExpectedToolAttemptResult) ValidationStatus {
	if attempt.ValidationStatus.IsTerminal() {
		return attempt.ValidationStatus
	}
	if attempt.Status != ExpectedToolExecutionSucceeded {
		return ValidationStatusErrored
	}
	return ValidationStatusPassed
}

func validationExpectedToolValidationReason(status ValidationStatus, result *ValidationExpectedToolExecutionResult) string {
	evidence := strings.Join(result.ArtifactIDs, ",")
	if evidence == "" {
		evidence = "none"
	}
	switch status {
	case ValidationStatusPassed:
		return "expected validation tools passed; evidence_artifacts=" + evidence
	case ValidationStatusIncomplete:
		return "expected validation tools incomplete: required evidence is missing; evidence_artifacts=" + evidence
	case ValidationStatusErrored:
		return "expected validation tools errored: validator infrastructure did not complete cleanly; evidence_artifacts=" + evidence
	default:
		return "expected validation tools failed: present evidence did not satisfy the validation; evidence_artifacts=" + evidence
	}
}

func redactExpectedToolValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, val := range typed {
			if expectedToolSensitiveKey(key) {
				out[key] = "[REDACTED]"
				continue
			}
			out[key] = redactExpectedToolValue(val)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = redactExpectedToolValue(typed[i])
		}
		return out
	case string:
		sanitized, _ := security.NewSecretSanitizer().SanitizeString(typed)
		return sanitized
	default:
		return typed
	}
}

func expectedToolSensitiveKey(key string) bool {
	key = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(key), "-", "_"))
	for _, needle := range []string{"secret", "token", "password", "passwd", "api_key", "apikey", "credential", "private_key", "authorization", "auth_header"} {
		if strings.Contains(key, needle) {
			return true
		}
	}
	return false
}

func truncateExpectedToolReference(value string) string {
	value = strings.TrimSpace(value)
	const max = 4096
	if len(value) <= max {
		return value
	}
	return value[:max] + "...(truncated)"
}

func contextWithoutCancellation(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}
