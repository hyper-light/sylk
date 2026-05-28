package claims

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const (
	KnowledgeBackendReadyClaimID  = "knowledge_backend.ready"
	KnowledgeBackendAgentID       = "knowledge_backend"
	KnowledgeBackendReadyQuality  = "knowledge_backend.ready"
	KnowledgeBackendReadyScopeKey = "committed_search"
)

var (
	ErrNilKnowledgeReadinessBoard = errors.New("claims: knowledge readiness requires a claims board")
)

// EnsureKnowledgeBackendReadyClaim posts the deterministic board-local claim
// that knowledge searches wait on. The claim is intentionally system-internal:
// it produces ordinary board mutation deltas for waiters without waking a
// conversational agent.
func EnsureKnowledgeBackendReadyClaim(ctx context.Context, board *ClaimsBoard) error {
	if board == nil {
		return ErrNilKnowledgeReadinessBoard
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if existing, ok := board.CloneClaim(KnowledgeBackendReadyClaimID); ok {
		if existing != nil && existing.LifecycleStatus == ClaimLifecycleGenerated {
			return board.PostGeneratedClaim(ctx, KnowledgeBackendReadyClaimID, KnowledgeBackendAgentID, ClaimPostOptions{Reason: "knowledge readiness claim posted"})
		}
		return nil
	}
	generated, err := board.GenerateClaimAction(ctx, knowledgeBackendReadyAction(), []Claim{knowledgeBackendReadyClaim()}, GenerateClaimActionOptions{
		IdempotencyKey: KnowledgeBackendReadyClaimID,
		Reason:         "knowledge readiness claim generated",
	})
	if isDuplicateKnowledgeReadyClaim(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(generated.Claims) == 0 {
		return fmt.Errorf("claims: knowledge readiness claim generation returned no claims")
	}
	if err := board.PostGeneratedClaim(ctx, generated.Claims[0].ID, KnowledgeBackendAgentID, ClaimPostOptions{Reason: "knowledge readiness claim posted"}); err != nil {
		if isDuplicateKnowledgeReadyClaim(err) {
			return nil
		}
		return err
	}
	return nil
}

// KnowledgeBackendReadyTestament returns the first readiness testament linked
// to the deterministic knowledge-backend readiness claim.
func KnowledgeBackendReadyTestament(board *ClaimsBoard) (*Testament, bool) {
	if board == nil {
		return nil, false
	}
	if claim, ok := board.CloneClaim(KnowledgeBackendReadyClaimID); !ok || claim == nil || claim.LifecycleStatus != ClaimLifecycleSatisfied {
		return nil, false
	}
	for _, testament := range board.TestamentsByClaim(KnowledgeBackendReadyClaimID) {
		if knowledgeReadinessTestamentReady(testament) {
			return testament, true
		}
	}
	return nil, false
}

// SubmitKnowledgeBackendReadyTestament records that the committed knowledge
// backend is searchable. Existing readiness testimony is reused so repeated
// partial/full readiness events stay idempotent.
func SubmitKnowledgeBackendReadyTestament(ctx context.Context, board *ClaimsBoard, metadata map[string]any) (*Testament, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if testament, ok := KnowledgeBackendReadyTestament(board); ok {
		return testament, nil
	}
	if err := EnsureKnowledgeBackendReadyClaim(ctx, board); err != nil {
		return nil, err
	}
	testament := knowledgeBackendReadyTestament(metadata)
	generated, err := board.GenerateTestamentAction(ctx, knowledgeBackendReadyTestamentAction(), []Testament{testament}, GenerateTestamentActionOptions{
		IdempotencyKey: "knowledge_backend.ready.testament",
		Reason:         "knowledge readiness testament generated",
	})
	if err != nil {
		return nil, err
	}
	if len(generated.Testaments) == 0 {
		return nil, fmt.Errorf("claims: knowledge readiness testament generation returned no testaments")
	}
	if err := board.PostGeneratedTestament(ctx, generated.Testaments[0].ID, KnowledgeBackendAgentID, TestamentPostOptions{Reason: "knowledge readiness testament posted"}); err != nil {
		return nil, err
	}
	if submitted, ok := KnowledgeBackendReadyTestament(board); ok {
		return submitted, nil
	}
	return nil, fmt.Errorf("claims: knowledge readiness testament was submitted but is not queryable")
}

// WaitForKnowledgeBackendReady waits for a readiness testament using board
// mutation deltas. It never polls; callers should pass a request-scoped context
// so cancellation or tool timeout bounds the wait.
func WaitForKnowledgeBackendReady(ctx context.Context, board *ClaimsBoard) (*Testament, error) {
	if board == nil {
		return nil, ErrNilKnowledgeReadinessBoard
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if testament, ok := KnowledgeBackendReadyTestament(board); ok {
		return testament, nil
	}

	ready := make(chan struct{}, 1)
	unsubscribe := board.SubscribeDelta(func(delta BoardMutationDelta) error {
		if (delta.Kind == "testament_posted" || delta.Kind == "testament_submitted") && delta.ClaimID == KnowledgeBackendReadyClaimID {
			select {
			case ready <- struct{}{}:
			default:
			}
		}
		return nil
	})
	defer unsubscribe()

	if err := EnsureKnowledgeBackendReadyClaim(ctx, board); err != nil {
		return nil, err
	}
	if testament, ok := KnowledgeBackendReadyTestament(board); ok {
		return testament, nil
	}

	for {
		select {
		case <-ready:
			if testament, ok := KnowledgeBackendReadyTestament(board); ok {
				return testament, nil
			}
		case <-ctx.Done():
			RecordKnowledgeBackendReadinessFailure(context.Background(), board, ctx.Err())
			return nil, ctx.Err()
		}
	}
}

// RecordKnowledgeBackendReadinessFailure persists a readiness wait/backend
// failure as claim lifecycle evidence. Callers use this when the committed
// backend cannot become ready before a request deadline or initialization
// fails outright.
func RecordKnowledgeBackendReadinessFailure(ctx context.Context, board *ClaimsBoard, cause error) error {
	if board == nil {
		return ErrNilKnowledgeReadinessBoard
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := EnsureKnowledgeBackendReadyClaim(ctx, board); err != nil {
		return err
	}
	reason := "knowledge backend readiness failed"
	if cause != nil && strings.TrimSpace(cause.Error()) != "" {
		reason = cause.Error()
	}
	kind := ArtifactKindErrorDiagnostic
	if errors.Is(cause, context.DeadlineExceeded) {
		kind = ArtifactKindToolTimeout
	}
	if errors.Is(cause, context.Canceled) {
		kind = ArtifactKindInterrupted
	}
	metadata := map[string]any{
		"component":   KnowledgeBackendAgentID,
		"quality_bar": KnowledgeBackendReadyQuality,
	}
	if cause != nil {
		metadata["error"] = cause.Error()
	}
	failureClaimID, err := postKnowledgeBackendReadinessFailureClaim(ctx, board, reason)
	if err != nil {
		return err
	}
	return board.RecordClaimValidationError(ctx, failureClaimID, KnowledgeBackendAgentID, LifecycleFailureOptions{
		Reason:       reason,
		ArtifactKind: kind,
		Metadata:     metadata,
	})
}

func postKnowledgeBackendReadinessFailureClaim(ctx context.Context, board *ClaimsBoard, reason string) (string, error) {
	claimID := "knowledge_backend.ready.failure." + uuid.NewString()
	claim := Claim{
		ID:          claimID,
		AgentID:     KnowledgeBackendAgentID,
		Title:       "Knowledge backend readiness wait failed",
		Description: lifecycleReason(reason, "knowledge backend readiness wait failed"),
		ActionType:  ActionTypeTask,
		Context:     lifecycleReason(reason, "knowledge backend readiness wait failed"),
		Scope: []ClaimScopeEntry{
			{Kind: "knowledge_backend", Key: KnowledgeBackendReadyScopeKey},
			{Kind: "knowledge_backend_failure", Key: claimID},
		},
		Relations: []Relation{
			{Related: KnowledgeBackendAgentID, RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: KnowledgeBackendAgentID, RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
			{Related: KnowledgeBackendReadyClaimID, RelatedType: RelatedTypeClaim, Relationship: RelationshipCausedBy},
		},
		Validations: []*Validation{{
			ID:          claimID + ".readiness",
			Description: "Knowledge readiness wait completed without backend readiness",
			QualityBar:  KnowledgeBackendReadyQuality,
			Type:        ValidationTypeInspection,
			Required:    true,
		}},
	}
	generated, err := board.GenerateClaimAction(ctx, Action{ID: claimID + ".action", AgentID: KnowledgeBackendAgentID, Type: ActionTypeTask}, []Claim{claim}, GenerateClaimActionOptions{
		IdempotencyKey: claimID,
		Reason:         "knowledge readiness failure claim generated",
	})
	if err != nil {
		return "", err
	}
	if len(generated.Claims) == 0 {
		return "", fmt.Errorf("claims: knowledge readiness failure claim generation returned no claims")
	}
	if err := board.PostGeneratedClaim(ctx, generated.Claims[0].ID, KnowledgeBackendAgentID, ClaimPostOptions{Reason: "knowledge readiness failure claim posted"}); err != nil {
		return "", err
	}
	return generated.Claims[0].ID, nil
}

func knowledgeBackendReadyAction() Action {
	return Action{
		ID:      KnowledgeBackendReadyClaimID + ".action",
		AgentID: KnowledgeBackendAgentID,
		Type:    ActionTypeBoot,
		Relations: []Relation{
			{Related: KnowledgeBackendAgentID, RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: KnowledgeBackendAgentID, RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		},
	}
}

func knowledgeBackendReadyClaim() Claim {
	return Claim{
		ID:          KnowledgeBackendReadyClaimID,
		AgentID:     KnowledgeBackendAgentID,
		Title:       "Committed knowledge backend ready",
		Description: "Committed knowledge search must wait until the backend posts readiness testimony.",
		ActionType:  ActionTypeBoot,
		Context:     "Awaiting committed knowledge backend readiness testament",
		Scope:       []ClaimScopeEntry{{Kind: "knowledge_backend", Key: KnowledgeBackendReadyScopeKey}},
		Relations: []Relation{
			{Related: KnowledgeBackendAgentID, RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: KnowledgeBackendAgentID, RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		},
		Validations: []*Validation{
			{
				ID:          KnowledgeBackendReadyClaimID + ".receipt",
				Description: "Knowledge backend readiness testament received",
				QualityBar:  KnowledgeBackendReadyQuality,
				Type:        ValidationTypeReceipt,
				Required:    true,
			},
		},
	}
}

func knowledgeBackendReadyTestament(metadata map[string]any) Testament {
	return Testament{
		AgentID:    KnowledgeBackendAgentID,
		Summary:    "Committed knowledge backend is ready.",
		Confidence: "committed",
		Relations: []Relation{
			{Related: KnowledgeBackendReadyClaimID, RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim},
			{Related: KnowledgeBackendAgentID, RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: KnowledgeBackendAgentID, RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		},
		Artifacts: []*Artifact{
			{
				AgentID:   KnowledgeBackendAgentID,
				Kind:      ArtifactKindReadiness,
				Reference: "committed knowledge backend ready",
				Metadata:  knowledgeBackendReadyMetadata(metadata),
			},
		},
	}
}

func knowledgeBackendReadyTestamentAction() Action {
	return Action{
		AgentID: KnowledgeBackendAgentID,
		Type:    ActionTypeTestament,
		Relations: []Relation{
			{Related: KnowledgeBackendAgentID, RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: KnowledgeBackendAgentID, RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
			{Related: KnowledgeBackendReadyClaimID, RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim},
		},
	}
}

func knowledgeBackendReadyMetadata(metadata map[string]any) map[string]any {
	out := make(map[string]any, len(metadata)+2)
	for key, value := range metadata {
		if strings.TrimSpace(key) != "" {
			out[key] = value
		}
	}
	out["component"] = KnowledgeBackendAgentID
	out["quality_bar"] = KnowledgeBackendReadyQuality
	return out
}

func knowledgeReadinessTestamentReady(testament *Testament) bool {
	if testament == nil || DeriveTestamentVerdict(testament.Artifacts) == TestamentVerdictError {
		return false
	}
	for _, artifact := range testament.Artifacts {
		if knowledgeReadinessArtifactReady(artifact) {
			return true
		}
	}
	return false
}

func knowledgeReadinessArtifactReady(artifact *Artifact) bool {
	if artifact == nil || artifact.Kind != ArtifactKindReadiness {
		return false
	}
	component, _ := artifact.Metadata["component"].(string)
	return component == KnowledgeBackendAgentID
}

func isDuplicateKnowledgeReadyClaim(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate claim ID")
}
