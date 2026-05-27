package claims

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type fakeToolRegistry map[string]bool

func (r fakeToolRegistry) HasTool(name string) bool { return r[name] }

func TestExpectedToolCallValidation(t *testing.T) {
	valid := []ExpectedToolCall{{
		ID:       "etc-1",
		Tool:     "workspace_read",
		Required: true,
		Arguments: map[string]any{
			"op":   "glob",
			"path": ".",
		},
		ProducesArtifacts: []string{"workspace_observation"},
	}}
	if err := ValidateExpectedToolCalls(valid, fakeToolRegistry{"workspace_read": true}); err != nil {
		t.Fatalf("valid expected tool rejected: %v", err)
	}
	if err := ValidateExpectedToolCalls([]ExpectedToolCall{{Required: true}}, nil); err == nil {
		t.Fatal("missing tool name accepted")
	}
	if err := ValidateExpectedToolCalls([]ExpectedToolCall{{
		Tool:      "workspace_read",
		Arguments: map[string]any{"tool_call_id": "provider-1"},
	}}, nil); err == nil {
		t.Fatal("provider tool_call_id accepted")
	}
	if err := ValidateExpectedToolCalls([]ExpectedToolCall{{ID: "dup", Tool: "a"}, {ID: "dup", Tool: "b"}}, nil); err == nil {
		t.Fatal("duplicate expected tool id accepted")
	}
	if err := ValidateExpectedToolCalls([]ExpectedToolCall{{Tool: "unknown"}}, fakeToolRegistry{"workspace_read": true}); err == nil {
		t.Fatal("unknown registry tool accepted")
	}
	if err := ValidateExpectedToolCalls([]ExpectedToolCall{{
		Tool:      "workspace_read",
		Arguments: map[string]any{"bad": func() {}},
	}}, fakeToolRegistry{"workspace_read": true}); err == nil {
		t.Fatal("non-JSON expected tool arguments accepted")
	}
}

func TestExpectedToolFailureArtifact(t *testing.T) {
	artifact := ExpectedToolFailureArtifact(ExpectedToolCall{
		ID:       "etc-1",
		Tool:     "workspace_read",
		Purpose:  "inspect workspace",
		Required: true,
	}, "librarian", ArtifactKindToolTimeout, "workspace_read timed out")
	if artifact.Kind != ArtifactKindToolTimeout {
		t.Fatalf("kind = %q", artifact.Kind)
	}
	if artifact.Reference != "workspace_read timed out" {
		t.Fatalf("reference = %q", artifact.Reference)
	}
	if artifact.Metadata[ExpectedToolMetadataID] != "etc-1" || artifact.Metadata[ExpectedToolMetadataTool] != "workspace_read" {
		t.Fatalf("expected tool metadata missing: %#v", artifact.Metadata)
	}
	if DeriveTestamentVerdict([]*Artifact{artifact}) != TestamentVerdictError {
		t.Fatal("expected tool failure artifact should classify as error evidence")
	}
}

func TestExpectedToolCallsStampedAndSerializedOnClaims(t *testing.T) {
	board := NewClaimsBoard(ClaimsBoardConfig{SessionID: "sess", TaskID: "task"})
	err := board.PostAction(context.Background(), Action{Type: ActionTypeTask, AgentID: "architect"}, []Claim{{
		Title: "Inspect workspace",
		Relations: []Relation{
			{Related: "librarian", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		},
		ExpectedToolCalls: []ExpectedToolCall{{
			Tool:              "workspace_read",
			ProducesArtifacts: []string{"", "workspace_observation", "workspace_observation"},
		}},
		Validations: []*Validation{{
			Description: "Run tests",
			Required:    true,
			ExpectedToolCalls: []ExpectedToolCall{{
				Tool: "run_tests",
			}},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	claim := board.Projection().Claims[0]
	if len(claim.ExpectedToolCalls) != 1 || claim.ExpectedToolCalls[0].ID == "" {
		t.Fatalf("claim expected tool was not stamped: %+v", claim.ExpectedToolCalls)
	}
	if got := claim.ExpectedToolCalls[0].ProducesArtifacts; len(got) != 1 || got[0] != "workspace_observation" {
		t.Fatalf("produces_artifacts not normalized: %#v", got)
	}
	if got := claim.Validations[0].ExpectedToolCalls; len(got) != 1 || got[0].ID == "" {
		t.Fatalf("validation expected tool was not stamped: %+v", got)
	}
}

type fakeExpectedToolExecutor struct {
	calls []ExpectedToolCall
	out   ExpectedToolExecutionOutput
	err   error
	panic bool
}

func (f *fakeExpectedToolExecutor) ExecuteExpectedTool(_ context.Context, call ExpectedToolCall) (ExpectedToolExecutionOutput, error) {
	f.calls = append(f.calls, call)
	if f.panic {
		panic("boom")
	}
	return f.out, f.err
}

type fakeExpectedToolRedactor struct {
	err error
}

func (f fakeExpectedToolRedactor) RedactExpectedToolArguments(context.Context, string, map[string]any) (map[string]any, error) {
	if f.err != nil {
		return nil, f.err
	}
	return map[string]any{"redacted": true}, nil
}

type fakeRemediationPoster struct {
	ids []string
	err error
}

func (f fakeRemediationPoster) PostValidationExpectedToolRemediation(context.Context, *ClaimsBoard, *Claim, *Validation, *ValidationExpectedToolExecutionResult) ([]string, error) {
	return append([]string(nil), f.ids...), f.err
}

func boardWithExpectedValidation(t *testing.T, call ExpectedToolCall) (*ClaimsBoard, string, string) {
	t.Helper()
	board := NewClaimsBoard(ClaimsBoardConfig{SessionID: "sess", TaskID: "task"})
	err := board.PostAction(context.Background(), Action{Type: ActionTypeTask, AgentID: "architect"}, []Claim{{
		Title: "Implement feature",
		Relations: []Relation{
			{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: "engineer", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		},
		Validations: []*Validation{{
			Description:       "Run deterministic check",
			QualityBar:        "expected tool succeeds",
			Type:              ValidationTypeTest,
			Required:          true,
			ExpectedToolCalls: []ExpectedToolCall{call},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	claim := board.Projection().Claims[0]
	return board, claim.ID, claim.Validations[0].ID
}

func TestReceiptAutoPassDoesNotAutoPassInspectionValidation(t *testing.T) {
	board := NewClaimsBoard(ClaimsBoardConfig{SessionID: "sess", TaskID: "task"})
	if err := board.PostAction(context.Background(), Action{Type: ActionTypeConsultation, AgentID: "architect"}, []Claim{{
		Title: "Consult librarian",
		Relations: []Relation{
			{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: "librarian", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		},
		Validations: []*Validation{
			{Description: "response received", QualityBar: "testament linked", Type: ValidationTypeReceipt, Required: true},
			{Description: "answer inspected", QualityBar: "answer is relevant", Type: ValidationTypeInspection, Required: true},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	claimID := board.Projection().Claims[0].ID
	if err := board.SubmitTestaments(context.Background(), Action{Type: ActionTypeTestament, AgentID: "librarian"}, []Testament{{
		AgentID: "librarian",
		Summary: "I answered.",
		Relations: []Relation{
			{Related: claimID, RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	claim, _ := board.CloneClaim(claimID)
	if claim.Validations[0].Status != ValidationStatusPassed {
		t.Fatalf("receipt status = %s, want passed", claim.Validations[0].Status)
	}
	if claim.Validations[1].Status != ValidationStatusPending {
		t.Fatalf("inspection status = %s, want pending", claim.Validations[1].Status)
	}
	if claim.Status != ClaimStatusTestified {
		t.Fatalf("claim status = %s, want testified while inspection remains pending", claim.Status)
	}
}

func TestExecuteValidationExpectedToolsSuccessProducesEvidenceAndPasses(t *testing.T) {
	board, claimID, validationID := boardWithExpectedValidation(t, ExpectedToolCall{
		Tool:     "run_tests",
		Required: true,
		Arguments: map[string]any{
			"command": "go test ./...",
		},
	})
	exec := &fakeExpectedToolExecutor{out: ExpectedToolExecutionOutput{Output: map[string]any{"passed": true}, Summary: "tests passed"}}
	result, err := ExecuteValidationExpectedTools(context.Background(), board, claimID, validationID, ExpectedToolExecutionOptions{
		AgentID:      "tester",
		Executor:     exec,
		AllowedTools: map[string]bool{"run_tests": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(exec.calls) != 1 || exec.calls[0].Tool != "run_tests" {
		t.Fatalf("executor calls = %+v", exec.calls)
	}
	if !result.ValidationEvaluated || result.ValidationStatus != ValidationStatusPassed {
		t.Fatalf("result validation = evaluated:%v status:%s", result.ValidationEvaluated, result.ValidationStatus)
	}
	claim, _ := board.CloneClaim(claimID)
	if claim.Validations[0].Status != ValidationStatusPassed {
		t.Fatalf("validation status = %s, want passed", claim.Validations[0].Status)
	}
	if !strings.Contains(claim.Validations[0].StatusHistory[len(claim.Validations[0].StatusHistory)-1].Reason, "evidence_artifacts=") {
		t.Fatalf("validation reason does not cite evidence: %+v", claim.Validations[0].StatusHistory)
	}
	foundInvocation, foundOutput := false, false
	invocationID := ""
	for _, id := range result.ArtifactIDs {
		artifact, ok := board.CloneArtifact(id)
		if !ok {
			t.Fatalf("artifact %q not committed", id)
		}
		switch artifact.Kind {
		case ArtifactKindExpectedToolInvocation:
			foundInvocation = true
			invocationID = artifact.ID
			if artifact.Metadata[ExpectedToolMetadataValidationID] != validationID {
				t.Fatalf("invocation metadata missing validation id: %+v", artifact.Metadata)
			}
		case ArtifactKindExpectedToolOutput:
			foundOutput = true
			if artifact.Metadata[ExpectedToolMetadataID] == "" {
				t.Fatalf("output metadata missing expected tool id: %+v", artifact.Metadata)
			}
			if invocationID != "" && !HasRelation(artifact.Relations, RelationshipCompletes, invocationID) {
				t.Fatalf("output artifact does not complete invocation %q: %+v", invocationID, artifact.Relations)
			}
		}
	}
	if !foundInvocation || !foundOutput {
		t.Fatalf("found invocation=%v output=%v artifacts=%v", foundInvocation, foundOutput, result.ArtifactIDs)
	}
}

func TestExecuteValidationExpectedToolsPolicyDeniedFailsWithArtifact(t *testing.T) {
	board, claimID, validationID := boardWithExpectedValidation(t, ExpectedToolCall{Tool: "bash", Required: true})
	exec := &fakeExpectedToolExecutor{}
	result, err := ExecuteValidationExpectedTools(context.Background(), board, claimID, validationID, ExpectedToolExecutionOptions{
		AgentID:      "guardian",
		Executor:     exec,
		AllowedTools: map[string]bool{"workspace_read": true},
		RemediationPoster: fakeRemediationPoster{
			ids: []string{"remediate-1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(exec.calls) != 0 {
		t.Fatalf("policy-denied tool executed: %+v", exec.calls)
	}
	if result.ValidationStatus != ValidationStatusFailed {
		t.Fatalf("validation status = %s, want failed", result.ValidationStatus)
	}
	if len(result.RemediationClaimIDs) != 1 || result.RemediationClaimIDs[0] != "remediate-1" {
		t.Fatalf("remediation ids = %+v", result.RemediationClaimIDs)
	}
	artifact, ok := board.CloneArtifact(result.Attempts[0].ErrorArtifactID)
	if !ok {
		t.Fatalf("policy artifact %q not committed", result.Attempts[0].ErrorArtifactID)
	}
	if artifact.Kind != ArtifactKindPolicyDenied {
		t.Fatalf("artifact kind = %q, want policy_denied", artifact.Kind)
	}
}

func TestExecuteValidationExpectedToolsRedactsArgumentsInAuditArtifacts(t *testing.T) {
	board, claimID, validationID := boardWithExpectedValidation(t, ExpectedToolCall{
		Tool:     "workspace_read",
		Required: true,
		Arguments: map[string]any{
			"path":  ".",
			"token": "topsecret",
		},
	})
	result, err := ExecuteValidationExpectedTools(context.Background(), board, claimID, validationID, ExpectedToolExecutionOptions{
		AgentID:      "inspector",
		Executor:     &fakeExpectedToolExecutor{out: ExpectedToolExecutionOutput{Summary: "ok"}},
		AllowedTools: map[string]bool{"workspace_read": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact, ok := board.CloneArtifact(result.Attempts[0].InvocationArtifactID)
	if !ok {
		t.Fatal("invocation artifact not committed")
	}
	rendered := fmt.Sprintf("%+v", artifact.Metadata)
	if strings.Contains(rendered, "topsecret") {
		t.Fatalf("secret leaked in invocation metadata: %s", rendered)
	}
	if !strings.Contains(rendered, "[REDACTED]") {
		t.Fatalf("redacted marker missing in invocation metadata: %s", rendered)
	}
}

func TestExecuteValidationExpectedToolsRedactorFailureRecordsSafeArtifact(t *testing.T) {
	board, claimID, validationID := boardWithExpectedValidation(t, ExpectedToolCall{
		Tool:      "workspace_read",
		Required:  true,
		Arguments: map[string]any{"token": "topsecret"},
	})
	result, err := ExecuteValidationExpectedTools(context.Background(), board, claimID, validationID, ExpectedToolExecutionOptions{
		AgentID:  "inspector",
		Executor: &fakeExpectedToolExecutor{out: ExpectedToolExecutionOutput{Summary: "should not run"}},
		Redactor: fakeExpectedToolRedactor{err: errors.New("redactor failed")},
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact, ok := board.CloneArtifact(result.Attempts[0].ErrorArtifactID)
	if !ok {
		t.Fatal("safe redaction failure artifact not committed")
	}
	rendered := fmt.Sprintf("%+v", artifact)
	if strings.Contains(rendered, "topsecret") {
		t.Fatalf("secret leaked in redaction failure artifact: %s", rendered)
	}
	if artifact.Kind != ArtifactKindErrorDiagnostic {
		t.Fatalf("artifact kind = %q, want error_diagnostic", artifact.Kind)
	}
}

func TestExecuteValidationExpectedToolsRequiredSkipFailsValidation(t *testing.T) {
	board, claimID, validationID := boardWithExpectedValidation(t, ExpectedToolCall{Tool: "run_tests", Required: true})
	result, err := ExecuteValidationExpectedTools(context.Background(), board, claimID, validationID, ExpectedToolExecutionOptions{
		AgentID: "tester",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Attempts[0].Status; got != ExpectedToolExecutionFailed {
		t.Fatalf("attempt status = %s, want failed", got)
	}
	if result.ValidationStatus != ValidationStatusFailed {
		t.Fatalf("validation status = %s, want failed", result.ValidationStatus)
	}
}

func TestExecuteValidationExpectedToolsOptionalSkipDoesNotFailValidation(t *testing.T) {
	board, claimID, validationID := boardWithExpectedValidation(t, ExpectedToolCall{Tool: "optional_probe", Required: false})
	result, err := ExecuteValidationExpectedTools(context.Background(), board, claimID, validationID, ExpectedToolExecutionOptions{
		AgentID: "tester",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Attempts[0].Status; got != ExpectedToolExecutionSkipped {
		t.Fatalf("attempt status = %s, want skipped", got)
	}
	if result.ValidationStatus != ValidationStatusPassed {
		t.Fatalf("validation status = %s, want passed", result.ValidationStatus)
	}
}

func TestExecuteValidationExpectedToolsPanicBecomesErrorArtifact(t *testing.T) {
	board, claimID, validationID := boardWithExpectedValidation(t, ExpectedToolCall{Tool: "panic_tool", Required: true})
	result, err := ExecuteValidationExpectedTools(context.Background(), board, claimID, validationID, ExpectedToolExecutionOptions{
		AgentID:      "tester",
		Executor:     &fakeExpectedToolExecutor{panic: true},
		AllowedTools: map[string]bool{"panic_tool": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ValidationStatus != ValidationStatusFailed {
		t.Fatalf("validation status = %s, want failed", result.ValidationStatus)
	}
	artifact, ok := board.CloneArtifact(result.Attempts[0].ErrorArtifactID)
	if !ok {
		t.Fatal("panic error artifact not committed")
	}
	if artifact.Kind != ArtifactKindError {
		t.Fatalf("panic artifact kind = %q, want error", artifact.Kind)
	}
	if !strings.Contains(artifact.Reference, "panicked") {
		t.Fatalf("panic artifact reference = %q", artifact.Reference)
	}
}

func TestExecuteValidationExpectedToolsUnknownValidationIDFails(t *testing.T) {
	board, claimID, _ := boardWithExpectedValidation(t, ExpectedToolCall{Tool: "run_tests", Required: true})
	_, err := ExecuteValidationExpectedTools(context.Background(), board, claimID, "missing-validation", ExpectedToolExecutionOptions{})
	if err == nil {
		t.Fatal("unknown validation ID accepted")
	}
}

func TestEvaluateValidationDuplicateTerminalIsIdempotent(t *testing.T) {
	board, claimID, validationID := boardWithExpectedValidation(t, ExpectedToolCall{Tool: "run_tests", Required: true})
	if err := board.EvaluateValidation(context.Background(), claimID, validationID, StatusChange{To: string(ValidationStatusPassed), AgentID: "tester"}); err != nil {
		t.Fatal(err)
	}
	if err := board.EvaluateValidation(context.Background(), claimID, validationID, StatusChange{To: string(ValidationStatusPassed), AgentID: "tester"}); err != nil {
		t.Fatalf("same terminal verdict should be idempotent: %v", err)
	}
	if err := board.EvaluateValidation(context.Background(), claimID, validationID, StatusChange{To: string(ValidationStatusFailed), AgentID: "tester"}); err == nil {
		t.Fatal("different terminal verdict accepted")
	}
}
