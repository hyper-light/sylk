package claims

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	SystemEvidenceKindSession = "session_evidence"
	SystemEvidenceKindFabric  = "fabric_evidence"
	SystemEvidenceKindBus     = "bus_transport_evidence"

	systemEvidenceValidationQuality = "system.evidence.received"
	systemEvidenceKeyPrefix         = "system.evidence"
	systemEvidenceDefaultActor      = "sys:session_manager"
	systemEvidenceFabricActor       = "sys:fabric_subscriber"
	systemEvidenceBusActor          = "sys:bus_administrator"
)

type SystemEvidenceOptions struct {
	Board          *ClaimsBoard
	ActorID        string
	SubjectID      string
	SessionID      string
	Operation      string
	Status         string
	ArtifactKind   string
	ArtifactName   string
	Reference      string
	IdempotencyKey string
	Metadata       map[string]any
}

type SystemEvidenceResult struct {
	ClaimID     string
	TestamentID string
}

func RecordSessionLifecycleEvidence(ctx context.Context, board *ClaimsBoard, operation, sessionID string, metadata map[string]any) (SystemEvidenceResult, error) {
	return RecordSystemEvidence(ctx, SystemEvidenceOptions{
		Board:        board,
		ActorID:      systemEvidenceDefaultActor,
		SubjectID:    systemEvidenceDefaultActor,
		SessionID:    sessionID,
		Operation:    operation,
		Status:       "recorded",
		ArtifactKind: SystemEvidenceKindSession,
		ArtifactName: "session_" + sanitizeSystemEvidenceSegment(operation),
		Reference:    "session " + strings.TrimSpace(operation),
		Metadata:     metadata,
	})
}

func RecordFabricSubscriptionEvidence(ctx context.Context, board *ClaimsBoard, operation, sessionID string, metadata map[string]any) (SystemEvidenceResult, error) {
	return RecordSystemEvidence(ctx, SystemEvidenceOptions{
		Board:        board,
		ActorID:      systemEvidenceFabricActor,
		SubjectID:    systemEvidenceFabricActor,
		SessionID:    sessionID,
		Operation:    operation,
		Status:       "recorded",
		ArtifactKind: SystemEvidenceKindFabric,
		ArtifactName: "fabric_" + sanitizeSystemEvidenceSegment(operation),
		Reference:    "fabric " + strings.TrimSpace(operation),
		Metadata:     metadata,
	})
}

func RecordBusTransportEvidence(ctx context.Context, board *ClaimsBoard, operation, sessionID string, metadata map[string]any) (SystemEvidenceResult, error) {
	if board != nil {
		board.RecordNotificationError("bus transport " + strings.TrimSpace(operation))
	}
	return RecordSystemEvidence(ctx, SystemEvidenceOptions{
		Board:        board,
		ActorID:      systemEvidenceBusActor,
		SubjectID:    systemEvidenceBusActor,
		SessionID:    sessionID,
		Operation:    operation,
		Status:       "recorded",
		ArtifactKind: SystemEvidenceKindBus,
		ArtifactName: "bus_" + sanitizeSystemEvidenceSegment(operation),
		Reference:    "bus " + strings.TrimSpace(operation),
		Metadata:     metadata,
	})
}

func RecordSystemEvidence(ctx context.Context, opts SystemEvidenceOptions) (SystemEvidenceResult, error) {
	opts = normalizeSystemEvidenceOptions(opts)
	if err := validateSystemEvidenceOptions(opts); err != nil {
		return SystemEvidenceResult{}, err
	}
	claimID, err := ensureSystemEvidenceClaim(ctx, opts)
	if err != nil {
		return SystemEvidenceResult{}, err
	}
	testamentID, err := ensureSystemEvidenceTestament(ctx, claimID, opts)
	if err != nil {
		return SystemEvidenceResult{ClaimID: claimID}, err
	}
	return SystemEvidenceResult{ClaimID: claimID, TestamentID: testamentID}, nil
}

func ensureSystemEvidenceClaim(ctx context.Context, opts SystemEvidenceOptions) (string, error) {
	claim := Claim{
		Title:       "Record " + opts.Operation,
		Description: "Record durable claims-plane evidence for " + opts.Operation + ".",
		ActionType:  ActionTypeArchival,
		Relations: []Relation{
			{Related: opts.ActorID, RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: opts.SubjectID, RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		},
		Validations: []*Validation{{
			ID:          validationIDForSystemEvidence(opts.IdempotencyKey),
			Type:        ValidationTypeReceipt,
			Required:    true,
			Description: "system evidence testament received",
			QualityBar:  systemEvidenceValidationQuality,
			Status:      ValidationStatusPending,
		}},
	}
	generated, err := opts.Board.GenerateClaimAction(ctx, Action{AgentID: opts.ActorID, Type: ActionTypeArchival}, []Claim{claim}, GenerateClaimActionOptions{IdempotencyKey: opts.IdempotencyKey, Reason: "system evidence claim generated"})
	if err != nil {
		return "", err
	}
	claimID := generated.Claims[0].ID
	if err := postSystemEvidenceClaim(ctx, opts.Board, claimID, opts); err != nil {
		return claimID, err
	}
	return claimID, nil
}

func postSystemEvidenceClaim(ctx context.Context, board *ClaimsBoard, claimID string, opts SystemEvidenceOptions) error {
	claim, ok := board.CloneClaim(claimID)
	if !ok || claim.LifecycleStatus == ClaimLifecycleSatisfied {
		return nil
	}
	if claim.LifecycleStatus == ClaimLifecycleGenerated {
		if err := board.PostGeneratedClaim(ctx, claimID, opts.ActorID, ClaimPostOptions{Reason: "system evidence claim posted"}); err != nil {
			return err
		}
	}
	if claim, _ = board.CloneClaim(claimID); claim != nil && claim.LifecycleStatus == ClaimLifecyclePosted {
		if err := board.AcknowledgeClaimReceipt(ctx, claimID, opts.SubjectID); err != nil {
			return err
		}
	}
	if claim, _ = board.CloneClaim(claimID); claim != nil && claim.LifecycleStatus == ClaimLifecycleReceived {
		return board.UpdateClaimProgress(ctx, claimID, ClaimProgressUpdate{WorkSummary: "system evidence recorded"}, opts.SubjectID)
	}
	return nil
}

func ensureSystemEvidenceTestament(ctx context.Context, claimID string, opts SystemEvidenceOptions) (string, error) {
	artifact, err := systemEvidenceArtifact(opts)
	if err != nil {
		return "", err
	}
	testament := Testament{
		AgentID:    opts.SubjectID,
		Summary:    opts.Operation + " " + opts.Status,
		Confidence: "deterministic",
		Duration:   0,
		Relations:  []Relation{{Related: claimID, RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim}},
		Artifacts:  []*Artifact{artifact},
	}
	generated, err := opts.Board.GenerateTestamentAction(ctx, Action{AgentID: opts.SubjectID, Type: ActionTypeTestament, Status: ActionStatusComplete}, []Testament{testament}, GenerateTestamentActionOptions{IdempotencyKey: opts.IdempotencyKey + ":testament", Reason: "system evidence testament generated"})
	if err != nil {
		return "", err
	}
	testamentID := generated.Testaments[0].ID
	if err := postSystemEvidenceTestament(ctx, opts.Board, claimID, testamentID, opts); err != nil {
		return testamentID, err
	}
	return testamentID, nil
}

func postSystemEvidenceTestament(ctx context.Context, board *ClaimsBoard, claimID, testamentID string, opts SystemEvidenceOptions) error {
	testament, ok := board.CloneTestament(testamentID)
	if !ok || testament.LifecycleStatus == TestamentLifecycleValidated {
		return nil
	}
	if testament.LifecycleStatus == TestamentLifecycleGenerated {
		if err := board.PostGeneratedTestament(ctx, testamentID, opts.SubjectID, TestamentPostOptions{Reason: "system evidence testament posted"}); err != nil {
			return err
		}
	}
	if err := completeReceiptValidationForSystemEvidence(ctx, board, claimID, testamentID, opts); err != nil {
		return err
	}
	return nil
}

func completeReceiptValidationForSystemEvidence(ctx context.Context, board *ClaimsBoard, claimID, testamentID string, opts SystemEvidenceOptions) error {
	if testament, ok := board.CloneTestament(testamentID); ok && testament.LifecycleStatus == TestamentLifecyclePosted {
		if err := board.AcknowledgeTestamentReceipt(ctx, testamentID, opts.ActorID); err != nil {
			return err
		}
	}
	if testament, ok := board.CloneTestament(testamentID); ok && testament.LifecycleStatus == TestamentLifecycleReceived {
		if err := board.BeginTestamentValidation(ctx, testamentID, opts.ActorID); err != nil {
			return err
		}
	}
	if err := evaluateSystemEvidenceReceipt(ctx, board, claimID, opts.ActorID); err != nil {
		return err
	}
	if testament, ok := board.CloneTestament(testamentID); ok && testament.LifecycleStatus == TestamentLifecycleValidating {
		return board.CompleteTestamentValidation(ctx, testamentID, opts.ActorID, TestamentLifecycleValidated, "system evidence validated")
	}
	return nil
}

func evaluateSystemEvidenceReceipt(ctx context.Context, board *ClaimsBoard, claimID, actorID string) error {
	claim, ok := board.CloneClaim(claimID)
	if !ok {
		return fmt.Errorf("system evidence claim %q not found", claimID)
	}
	for _, validation := range claim.Validations {
		if validation == nil || validation.Type != ValidationTypeReceipt || validation.Status == ValidationStatusPassed {
			continue
		}
		if validation.Status.IsTerminal() {
			return fmt.Errorf("system evidence validation %q already terminal: %s", validation.ID, validation.Status)
		}
		if err := board.EvaluateValidation(ctx, claimID, validation.ID, StatusChange{AgentID: actorID, To: string(ValidationStatusPassed), Reason: "system evidence received"}); err != nil {
			return err
		}
	}
	return nil
}

func systemEvidenceArtifact(opts SystemEvidenceOptions) (*Artifact, error) {
	artifact := &Artifact{
		ArtifactName: opts.ArtifactName,
		Kind:         opts.ArtifactKind,
		Reference:    opts.Reference,
		AgentID:      opts.SubjectID,
		Metadata:     systemEvidenceMetadata(opts),
	}
	err := SetArtifactData(artifact, PresentationEvidenceArtifactData{
		Kind:      opts.ArtifactKind,
		Reference: opts.Reference,
		Title:     opts.ArtifactName,
		Metadata:  artifact.Metadata,
	})
	return artifact, err
}

func normalizeSystemEvidenceOptions(opts SystemEvidenceOptions) SystemEvidenceOptions {
	opts.ActorID = firstNonEmpty(strings.TrimSpace(opts.ActorID), systemEvidenceDefaultActor)
	opts.SubjectID = firstNonEmpty(strings.TrimSpace(opts.SubjectID), opts.ActorID)
	opts.SessionID = firstNonEmpty(strings.TrimSpace(opts.SessionID), boardSessionID(opts.Board))
	opts.Operation = firstNonEmpty(strings.TrimSpace(opts.Operation), "record")
	opts.Status = firstNonEmpty(strings.TrimSpace(opts.Status), "recorded")
	opts.ArtifactKind = firstNonEmpty(strings.TrimSpace(opts.ArtifactKind), SystemEvidenceKindSession)
	opts.ArtifactName = firstNonEmpty(strings.TrimSpace(opts.ArtifactName), sanitizeSystemEvidenceSegment(opts.Operation))
	opts.Reference = firstNonEmpty(strings.TrimSpace(opts.Reference), opts.Operation+" "+opts.Status)
	opts.IdempotencyKey = firstNonEmpty(strings.TrimSpace(opts.IdempotencyKey), systemEvidenceKey(opts))
	opts.Metadata = cloneMetadata(opts.Metadata)
	return opts
}

func validateSystemEvidenceOptions(opts SystemEvidenceOptions) error {
	if opts.Board == nil {
		return fmt.Errorf("system evidence board is required")
	}
	if opts.ActorID == "" || opts.SubjectID == "" || opts.Operation == "" || opts.IdempotencyKey == "" {
		return fmt.Errorf("system evidence actor, subject, operation, and idempotency key are required")
	}
	return nil
}

func systemEvidenceMetadata(opts SystemEvidenceOptions) map[string]any {
	return mergeMetadata(opts.Metadata, map[string]any{
		"actor_id":    opts.ActorID,
		"subject_id":  opts.SubjectID,
		"session_id":  opts.SessionID,
		"operation":   opts.Operation,
		"status":      opts.Status,
		"recorded_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func systemEvidenceKey(opts SystemEvidenceOptions) string {
	return strings.Join([]string{
		systemEvidenceKeyPrefix,
		sanitizeSystemEvidenceSegment(opts.SessionID),
		sanitizeSystemEvidenceSegment(opts.ArtifactKind),
		sanitizeSystemEvidenceSegment(opts.Operation),
	}, ":")
}

func validationIDForSystemEvidence(key string) string {
	return sanitizeSystemEvidenceSegment(key) + "_receipt"
}

func sanitizeSystemEvidenceSegment(value string) string {
	value = strings.NewReplacer(":", "_", "/", "_", " ", "_", "\t", "_", "\n", "_", "\r", "_").Replace(strings.TrimSpace(value))
	if value == "" {
		return "unknown"
	}
	return value
}

func boardSessionID(board *ClaimsBoard) string {
	if board == nil {
		return ""
	}
	return board.SessionID()
}
