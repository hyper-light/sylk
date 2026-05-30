package claims_test

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	claimsmocks "github.com/adalundhe/sylk/core/claims/mocks"
	"github.com/stretchr/testify/mock"
)

func TestInvokeServiceClaimWithMockeryHandlerCompletesLifecycle(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "service-invoke-board", SessionID: "service-invoke-session", TaskID: "task"})
	participant, err := claims.NewParticipantRegistration(
		claims.ParticipantCategoryService,
		"sys:test_service",
		map[string]string{"session_id": board.SessionID()},
		1,
		1,
		time.Second,
		claims.HandlerDeterminismSideEffect,
		[]claims.ActionType{claims.ActionTypeTask},
	)
	if err != nil {
		t.Fatalf("NewParticipantRegistration: %v", err)
	}
	artifact, err := claims.NewToolRuntimeExecutionArtifact(claims.ToolRuntimeExecutionArtifactData{
		ToolName:       "mock_tool",
		PolicyDecision: "allowed",
		ExecutionMode:  "sandboxed",
		Status:         claims.InfrastructureStatusOK,
	})
	if err != nil {
		t.Fatalf("NewToolRuntimeExecutionArtifact: %v", err)
	}
	handler := claimsmocks.NewServiceHandler(t)
	handler.EXPECT().
		HandleServiceClaim(mock.Anything, mock.MatchedBy(func(req claims.ServiceClaimRequest) bool {
			return req.Board == board &&
				req.Claim != nil &&
				req.Claim.LifecycleStatus == claims.ClaimLifecycleProgressed &&
				claims.SubjectAgentID(req.Claim.Relations) == participant.RouteKey &&
				len(req.Claim.ExpectedToolCalls) == 1 &&
				req.Claim.ExpectedToolCalls[0].Tool == "mock_tool"
		})).
		Return(claims.ServiceClaimResult{Summary: "mock service completed", Artifacts: []*claims.Artifact{artifact}}, nil).
		Once()

	result, err := claims.InvokeServiceClaim(context.Background(), claims.ServiceInvocationOptions{
		Board:       board,
		Handler:     handler,
		Participant: participant,
		IssuerID:    "issuer-agent",
		SubjectID:   participant.RouteKey,
		Title:       "Invoke mock service",
		Description: "Exercise synchronous service claim invocation.",
		ActionType:  claims.ActionTypeTask,
		ExpectedCall: claims.ExpectedToolCall{
			ID:        "call-mock-tool",
			Tool:      "mock_tool",
			Arguments: map[string]any{"tool_name": "mock_tool"},
		},
		IdempotencyKey: "test.invoke.mock_tool",
	})
	if err != nil {
		t.Fatalf("InvokeServiceClaim: %v", err)
	}
	if result.ClaimID == "" || result.TestamentID == "" {
		t.Fatalf("result = %+v, want claim and testament ids", result)
	}
	claim, ok := board.CloneClaim(result.ClaimID)
	if !ok {
		t.Fatalf("claim %q not found", result.ClaimID)
	}
	if claim.LifecycleStatus != claims.ClaimLifecycleSatisfied {
		t.Fatalf("claim lifecycle = %s, want %s", claim.LifecycleStatus, claims.ClaimLifecycleSatisfied)
	}
	testament, ok := board.CloneTestament(result.TestamentID)
	if !ok {
		t.Fatalf("testament %q not found", result.TestamentID)
	}
	if testament.LifecycleStatus != claims.TestamentLifecycleValidated || testament.AgentID != participant.RouteKey {
		t.Fatalf("testament = %+v, want validated service testament", testament)
	}
	if got, want := len(testament.Artifacts), 1; got != want {
		t.Fatalf("testament artifacts = %d, want %d", got, want)
	}
}
