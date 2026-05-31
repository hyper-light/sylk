package claims

import (
	"context"
	"fmt"
	"strings"
)

type ServiceInvocationOptions struct {
	Board          *ClaimsBoard
	Handler        ServiceHandler
	Participant    ParticipantRegistration
	IssuerID       string
	SubjectID      string
	Title          string
	Description    string
	ActionType     ActionType
	Scope          []ClaimScopeEntry
	ExpectedCall   ExpectedToolCall
	IdempotencyKey string
	Reason         string
}

type ServiceInvocationResult struct {
	ClaimID     string
	TestamentID string
	Result      ServiceClaimResult
}

func InvokeServiceClaim(ctx context.Context, opts ServiceInvocationOptions) (ServiceInvocationResult, error) {
	opts, err := normalizeServiceInvocationOptions(opts)
	if err != nil {
		return ServiceInvocationResult{}, err
	}
	claimID, err := generateServiceInvocationClaim(ctx, opts)
	if err != nil {
		return ServiceInvocationResult{}, err
	}
	claim, err := prepareServiceInvocationClaim(ctx, opts, claimID)
	if err != nil {
		return ServiceInvocationResult{ClaimID: claimID}, err
	}
	result, err := opts.Handler.HandleServiceClaim(ctxOrBackground(ctx), ServiceClaimRequest{Board: opts.Board, Claim: claim, Participant: opts.Participant})
	if err != nil {
		return ServiceInvocationResult{ClaimID: claimID}, err
	}
	testamentID, err := postServiceInvocationTestament(ctx, opts, claim, result)
	if err != nil {
		return ServiceInvocationResult{ClaimID: claimID, Result: result}, err
	}
	return ServiceInvocationResult{ClaimID: claimID, TestamentID: testamentID, Result: result}, nil
}

func normalizeServiceInvocationOptions(opts ServiceInvocationOptions) (ServiceInvocationOptions, error) {
	if opts.Board == nil {
		return opts, fmt.Errorf("service invocation board is required")
	}
	if opts.Handler == nil {
		return opts, fmt.Errorf("service invocation handler is required")
	}
	participant, err := normalizeParticipantRegistration(opts.Participant)
	if err != nil {
		return opts, err
	}
	opts.Participant = participant
	opts.IssuerID = firstNonEmpty(strings.TrimSpace(opts.IssuerID), participant.RouteKey)
	opts.SubjectID = firstNonEmpty(strings.TrimSpace(opts.SubjectID), participant.RouteKey)
	opts.ActionType = firstNonEmptyActionType(opts.ActionType, ActionTypeTask)
	opts.Title = firstNonEmpty(strings.TrimSpace(opts.Title), "Service task: "+participant.RouteKey)
	opts.Description = firstNonEmpty(strings.TrimSpace(opts.Description), "Run service handler for "+participant.RouteKey+".")
	opts.IdempotencyKey = firstNonEmpty(strings.TrimSpace(opts.IdempotencyKey), serviceInvocationKey(opts))
	opts.Reason = firstNonEmpty(strings.TrimSpace(opts.Reason), "service claim generated")
	return opts, nil
}

func generateServiceInvocationClaim(ctx context.Context, opts ServiceInvocationOptions) (string, error) {
	claim := Claim{
		Title:             opts.Title,
		Description:       opts.Description,
		ActionType:        opts.ActionType,
		Scope:             append([]ClaimScopeEntry(nil), opts.Scope...),
		ExpectedToolCalls: []ExpectedToolCall{opts.ExpectedCall},
		Relations: []Relation{
			{Related: opts.IssuerID, RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: opts.SubjectID, RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		},
		Validations: []*Validation{{
			ID:          validationIDForSystemEvidence(opts.IdempotencyKey),
			Type:        ValidationTypeReceipt,
			Required:    true,
			Description: "service testament received",
			QualityBar:  serviceInvocationValidationQualityBar(opts),
			Status:      ValidationStatusPending,
		}},
	}
	generated, err := opts.Board.GenerateClaimAction(ctxOrBackground(ctx), Action{AgentID: opts.IssuerID, Type: opts.ActionType}, []Claim{claim}, GenerateClaimActionOptions{
		IdempotencyKey: opts.IdempotencyKey,
		Reason:         opts.Reason,
	})
	if err != nil {
		return "", err
	}
	return generated.Claims[0].ID, postGeneratedServiceInvocationClaim(ctx, opts, generated.Claims[0].ID)
}

func serviceInvocationValidationQualityBar(opts ServiceInvocationOptions) string {
	if IsSystemInternalAction(opts.ActionType) {
		return ""
	}
	if opts.Participant.Category != ParticipantCategoryAgent && strings.TrimSpace(opts.IssuerID) == strings.TrimSpace(opts.Participant.RouteKey) {
		return ""
	}
	return "service.claim.completed"
}

func prepareServiceInvocationClaim(ctx context.Context, opts ServiceInvocationOptions, claimID string) (*Claim, error) {
	if err := opts.Board.AcknowledgeClaimReceipt(ctxOrBackground(ctx), claimID, opts.SubjectID); err != nil {
		return nil, ignoreAlreadyProgressedClaim(err, opts.Board, claimID)
	}
	if err := opts.Board.UpdateClaimProgress(ctxOrBackground(ctx), claimID, ClaimProgressUpdate{WorkSummary: "service handler running"}, opts.SubjectID); err != nil {
		return nil, ignoreAlreadyProgressedClaim(err, opts.Board, claimID)
	}
	claim, ok := opts.Board.CloneClaim(claimID)
	if !ok {
		return nil, fmt.Errorf("service invocation claim %q not found", claimID)
	}
	return claim, nil
}

func postServiceInvocationTestament(ctx context.Context, opts ServiceInvocationOptions, claim *Claim, result ServiceClaimResult) (string, error) {
	key := ServiceHandlerIdempotencyKey(claim, opts.Participant)
	testament := Testament{
		AgentID:        opts.SubjectID,
		Summary:        firstNonEmpty(strings.TrimSpace(result.Summary), "service claim completed"),
		Confidence:     "deterministic",
		IdempotencyKey: key,
		Relations: []Relation{
			{Related: claim.ID, RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim},
			{Related: key, RelatedType: RelatedTypeIdempotencyKey, Relationship: RelationshipDerivedFrom},
		},
		Artifacts: normalizeServiceArtifacts(result.Artifacts, opts.SubjectID),
	}
	generated, err := opts.Board.GenerateTestamentAction(ctxOrBackground(ctx), Action{AgentID: opts.SubjectID, Type: ActionTypeTestament, Status: ActionStatusComplete}, []Testament{testament}, GenerateTestamentActionOptions{
		IdempotencyKey: "service_invocation:" + key,
		Reason:         "service invocation testament generated",
	})
	if err != nil {
		return "", err
	}
	testamentID := generated.Testaments[0].ID
	if err := opts.Board.PostGeneratedTestament(ctxOrBackground(ctx), testamentID, opts.SubjectID, TestamentPostOptions{Reason: "service invocation testament posted"}); err != nil {
		return testamentID, err
	}
	return testamentID, completeServiceTestamentValidation(ctxOrBackground(ctx), opts.Board, testamentID, opts.IssuerID)
}

func postGeneratedServiceInvocationClaim(ctx context.Context, opts ServiceInvocationOptions, claimID string) error {
	claim, ok := opts.Board.CloneClaim(claimID)
	if !ok || claim.LifecycleStatus != ClaimLifecycleGenerated {
		return nil
	}
	return opts.Board.PostGeneratedClaim(ctxOrBackground(ctx), claimID, opts.IssuerID, ClaimPostOptions{Reason: "service invocation claim posted"})
}

func serviceInvocationKey(opts ServiceInvocationOptions) string {
	return strings.Join([]string{
		"service.invocation",
		sanitizeSystemEvidenceSegment(opts.Participant.RouteKey),
		sanitizeSystemEvidenceSegment(string(opts.ActionType)),
		sanitizeSystemEvidenceSegment(opts.ExpectedCall.Tool),
		stableInfrastructureHash(opts.ExpectedCall.Arguments),
	}, ":")
}

func firstNonEmptyActionType(values ...ActionType) ActionType {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
