package claims

import (
	"context"
	"fmt"
	"strings"
)

const (
	infrastructureEvidenceValidationQuality = "infrastructure.evidence.received"
	infrastructureEvidenceKeyPrefix         = "infrastructure.evidence"
)

type InfrastructureEvidenceOptions struct {
	Board          *ClaimsBoard
	ActorID        string
	SubjectID      string
	Operation      string
	IdempotencyKey string
	ParentClaimID  string
	Artifact       *Artifact
	Metadata       map[string]any
}

func RecordInfrastructureEvidence(ctx context.Context, opts InfrastructureEvidenceOptions) (SystemEvidenceResult, error) {
	opts = normalizeInfrastructureEvidenceOptions(opts)
	if err := validateInfrastructureEvidenceOptions(opts); err != nil {
		return SystemEvidenceResult{}, err
	}
	claimID, err := ensureInfrastructureEvidenceClaim(ctx, opts)
	if err != nil {
		return SystemEvidenceResult{}, err
	}
	testamentID, err := ensureInfrastructureEvidenceTestament(ctx, claimID, opts)
	if err != nil {
		return SystemEvidenceResult{ClaimID: claimID}, err
	}
	return SystemEvidenceResult{ClaimID: claimID, TestamentID: testamentID}, nil
}

func ensureInfrastructureEvidenceClaim(ctx context.Context, opts InfrastructureEvidenceOptions) (string, error) {
	relations := []Relation{
		{Related: opts.ActorID, RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
		{Related: opts.SubjectID, RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
	}
	if opts.ParentClaimID != "" {
		relations = append(relations, Relation{Related: opts.ParentClaimID, RelatedType: RelatedTypeClaim, Relationship: RelationshipCausedBy})
	}
	claim := Claim{
		Title:       "Record infrastructure " + opts.Operation,
		Description: "Record claims-plane evidence for infrastructure operation " + opts.Operation + ".",
		ActionType:  ActionTypeArchival,
		Relations:   relations,
		Validations: []*Validation{{
			ID:          validationIDForSystemEvidence(opts.IdempotencyKey),
			Type:        ValidationTypeReceipt,
			Required:    true,
			Description: "infrastructure evidence testament received",
			QualityBar:  infrastructureEvidenceValidationQuality,
			Status:      ValidationStatusPending,
		}},
	}
	generated, err := opts.Board.GenerateClaimAction(ctx, Action{AgentID: opts.ActorID, Type: ActionTypeArchival}, []Claim{claim}, GenerateClaimActionOptions{IdempotencyKey: opts.IdempotencyKey, Reason: "infrastructure evidence claim generated"})
	if err != nil {
		return "", err
	}
	claimID := generated.Claims[0].ID
	if err := opts.Board.PostGeneratedClaim(ctxOrBackground(ctx), claimID, opts.ActorID, ClaimPostOptions{Reason: "infrastructure evidence claim posted"}); err != nil {
		return claimID, err
	}
	return claimID, nil
}

func ensureInfrastructureEvidenceTestament(ctx context.Context, claimID string, opts InfrastructureEvidenceOptions) (string, error) {
	artifact := cloneInfrastructureEvidenceArtifact(opts.Artifact, opts.SubjectID)
	testament := Testament{
		AgentID:    opts.SubjectID,
		Summary:    "infrastructure " + opts.Operation + " recorded",
		Confidence: "deterministic",
		Relations:  []Relation{{Related: claimID, RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim}},
		Artifacts:  []*Artifact{artifact},
	}
	generated, err := opts.Board.GenerateTestamentAction(ctx, Action{AgentID: opts.SubjectID, Type: ActionTypeTestament, Status: ActionStatusComplete}, []Testament{testament}, GenerateTestamentActionOptions{IdempotencyKey: opts.IdempotencyKey + ":testament", Reason: "infrastructure evidence testament generated"})
	if err != nil {
		return "", err
	}
	testamentID := generated.Testaments[0].ID
	if err := opts.Board.PostGeneratedTestament(ctxOrBackground(ctx), testamentID, opts.SubjectID, TestamentPostOptions{Reason: "infrastructure evidence testament posted"}); err != nil {
		return testamentID, err
	}
	if err := completeServiceTestamentValidation(ctxOrBackground(ctx), opts.Board, testamentID, opts.ActorID); err != nil {
		return testamentID, err
	}
	return testamentID, nil
}

func normalizeInfrastructureEvidenceOptions(opts InfrastructureEvidenceOptions) InfrastructureEvidenceOptions {
	opts.ActorID = firstNonEmpty(strings.TrimSpace(opts.ActorID), "sys:infrastructure_adapter")
	opts.SubjectID = firstNonEmpty(strings.TrimSpace(opts.SubjectID), opts.ActorID)
	opts.Operation = firstNonEmpty(strings.TrimSpace(opts.Operation), "record")
	opts.ParentClaimID = strings.TrimSpace(opts.ParentClaimID)
	opts.Metadata = cloneMetadata(opts.Metadata)
	opts.IdempotencyKey = firstNonEmpty(strings.TrimSpace(opts.IdempotencyKey), infrastructureEvidenceKey(opts))
	return opts
}

func validateInfrastructureEvidenceOptions(opts InfrastructureEvidenceOptions) error {
	if opts.Board == nil {
		return fmt.Errorf("infrastructure evidence board is required")
	}
	if opts.Artifact == nil {
		return fmt.Errorf("infrastructure evidence artifact is required")
	}
	if opts.ActorID == "" || opts.SubjectID == "" || opts.Operation == "" || opts.IdempotencyKey == "" {
		return fmt.Errorf("infrastructure evidence actor, subject, operation, and idempotency key are required")
	}
	return nil
}

func cloneInfrastructureEvidenceArtifact(in *Artifact, agentID string) *Artifact {
	out := *in
	out.AgentID = firstNonEmpty(out.AgentID, agentID)
	out.Metadata = mergeMetadata(out.Metadata, map[string]any{"recorded_by": agentID})
	out.Relations = append([]Relation(nil), in.Relations...)
	out.Data = append([]byte(nil), in.Data...)
	out.Errors = append([]*ArtifactError(nil), in.Errors...)
	return &out
}

func infrastructureEvidenceKey(opts InfrastructureEvidenceOptions) string {
	return strings.Join([]string{
		infrastructureEvidenceKeyPrefix,
		sanitizeSystemEvidenceSegment(opts.Operation),
		sanitizeSystemEvidenceSegment(opts.ParentClaimID),
		sanitizeSystemEvidenceSegment(opts.Artifact.ContentHash),
	}, ":")
}
