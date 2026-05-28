package claims

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type IdentityDeterministicValidator struct{}

func (IdentityDeterministicValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[IdentityAllocationArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	expected, err := DeriveParticipantUID(data.Category, data.RouteKey, data.Scope)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if data.UID != expected {
		return identityValidatorFailure(req, "identity uid is not deterministic", map[string]any{"got": data.UID, "want": expected})
	}
	return identityValidatorPass("identity uid deterministic")
}

type IdentityUniqueValidator struct {
	Registry *IdentityRegistryService
}

func (v IdentityUniqueValidator) ValidateArtifact(ctx context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[IdentityAllocationArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if v.Registry == nil {
		return ValidatorHandlerResult{}, fmt.Errorf("%w: registry is required", ErrIdentityRegistryServiceInvalid)
	}
	existing, ok := v.Registry.Lookup(ctx, data.UID)
	if ok && !sameIdentityAllocationScope(existing, data) {
		return identityValidatorFailure(req, "identity uid is not unique", map[string]any{"uid": data.UID})
	}
	return identityValidatorPass("identity uid unique")
}

type GenerationMonotonicValidator struct {
	Registry *IdentityRegistryService
}

func (v GenerationMonotonicValidator) ValidateArtifact(ctx context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[IdentityAllocationArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if data.ParentUID == "" || v.Registry == nil {
		return identityValidatorPass("identity generation monotonic")
	}
	parent, ok := v.Registry.Lookup(ctx, data.ParentUID)
	if ok && data.Generation <= parent.Generation {
		return identityValidatorFailure(req, "identity generation regressed", map[string]any{"generation": data.Generation, "parent_generation": parent.Generation})
	}
	return identityValidatorPass("identity generation monotonic")
}

type ActivationReadinessValidator struct{}

func (ActivationReadinessValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[ActivationRecordArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if data.Ready && data.ReplicaCount > 0 && data.FailureReason == "" {
		return identityValidatorPass("activation ready")
	}
	return identityValidatorFailure(req, "activation is not ready", map[string]any{"participant_id": data.ParticipantID, "failure_reason": data.FailureReason})
}

type TierAchievedValidator struct {
	ExpectedTier string
}

func (v TierAchievedValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[ActivationRecordArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	expected := strings.TrimSpace(v.ExpectedTier)
	if expected == "" || strings.TrimSpace(data.Tier) == expected {
		return identityValidatorPass("activation tier achieved")
	}
	return identityValidatorFailure(req, "activation tier not achieved", map[string]any{"got": data.Tier, "want": expected})
}

type ReplicaCountValidator struct {
	ExpectedReplicaCount int
}

func (v ReplicaCountValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[ActivationRecordArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if data.ReplicaCount == v.ExpectedReplicaCount {
		return identityValidatorPass("activation replica count achieved")
	}
	return identityValidatorFailure(req, "activation replica count mismatch", map[string]any{"got": data.ReplicaCount, "want": v.ExpectedReplicaCount})
}

type ActivationDurationValidator struct {
	MaxDuration time.Duration
}

func (v ActivationDurationValidator) ValidateArtifact(_ context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactData[ActivationRecordArtifactData](req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	if data.Duration < 0 {
		return identityValidatorFailure(req, "activation duration is negative", map[string]any{"duration": data.Duration.String()})
	}
	if v.MaxDuration <= 0 || data.Duration <= v.MaxDuration {
		return identityValidatorPass("activation duration within budget")
	}
	return identityValidatorFailure(req, "activation duration exceeded budget", map[string]any{"duration": data.Duration.String(), "max_duration": v.MaxDuration.String()})
}

func identityValidatorPass(reference string) (ValidatorHandlerResult, error) {
	artifact := &Artifact{ArtifactName: "identity_validation", Kind: ArtifactKindReadiness, Reference: reference}
	if err := SetArtifactData(artifact, PresentationEvidenceArtifactData{Kind: "identity_validation", Reference: reference}); err != nil {
		return ValidatorHandlerResult{}, err
	}
	return ValidatorHandlerResult{ResultArtifact: artifact}, nil
}

func identityValidatorFailure(req ValidatorHandlerRequest, description string, payload map[string]any) (ValidatorHandlerResult, error) {
	err := &ValidationError{Category: ValidationErrorCategoryQualityBar, Description: description, Payload: payload, Source: validationErrorSource(ValidationDispatchRequest{Validation: req.Validation, Artifact: req.Artifact})}
	artifact := &Artifact{ArtifactName: "identity_validation_failure", Kind: ArtifactKindErrorDiagnostic, Reference: description}
	if setErr := SetArtifactData(artifact, PresentationEvidenceArtifactData{Kind: "identity_validation_failure", Reference: description, Metadata: payload}); setErr != nil {
		return ValidatorHandlerResult{}, setErr
	}
	return ValidatorHandlerResult{ResultArtifact: artifact, Error: err}, nil
}
