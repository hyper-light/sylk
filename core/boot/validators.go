package boot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/claims"
)

type BootSetupValidator struct{}
type BootDetectValidator struct{}
type BootAllocateValidator struct{}
type BootIngestValidator struct{}
type BootCommitValidator struct{}
type BootFinalizeValidator struct{}
type BootPhaseOrderValidator struct{}
type BootDurationValidator struct{ MaxDuration time.Duration }

func RegisterDefaultBootValidators(registry *claims.ValidatorRegistry) error {
	if registry == nil {
		return fmt.Errorf("%w: registry is nil", claims.ErrValidatorRegistrationInvalid)
	}
	var out error
	for _, reg := range defaultBootValidatorRegistrations() {
		_, err := registry.Register(reg)
		out = errors.Join(out, err)
	}
	return out
}

func (BootSetupValidator) ValidateArtifact(_ context.Context, req claims.ValidatorHandlerRequest) (claims.ValidatorHandlerResult, error) {
	return validateBootPhase(req, "setup")
}

func (BootDetectValidator) ValidateArtifact(_ context.Context, req claims.ValidatorHandlerRequest) (claims.ValidatorHandlerResult, error) {
	return validateBootPhase(req, "detect")
}

func (BootAllocateValidator) ValidateArtifact(_ context.Context, req claims.ValidatorHandlerRequest) (claims.ValidatorHandlerResult, error) {
	return validateBootPhase(req, "allocate")
}

func (BootIngestValidator) ValidateArtifact(_ context.Context, req claims.ValidatorHandlerRequest) (claims.ValidatorHandlerResult, error) {
	return validateBootPhase(req, "ingest")
}

func (BootCommitValidator) ValidateArtifact(_ context.Context, req claims.ValidatorHandlerRequest) (claims.ValidatorHandlerResult, error) {
	return validateBootPhase(req, "commit")
}

func (BootFinalizeValidator) ValidateArtifact(_ context.Context, req claims.ValidatorHandlerRequest) (claims.ValidatorHandlerResult, error) {
	return validateBootPhase(req, "finalize")
}

func (BootPhaseOrderValidator) ValidateArtifact(_ context.Context, req claims.ValidatorHandlerRequest) (claims.ValidatorHandlerResult, error) {
	data, err := claims.ArtifactData[claims.BootPhaseArtifactData](req.Artifact)
	if err != nil {
		return claims.ValidatorHandlerResult{}, err
	}
	expectedOrder := bootPhaseOrder(data.Phase)
	if expectedOrder == 0 {
		return bootValidatorFailure(req, "boot phase is unknown", map[string]any{"phase": data.Phase})
	}
	if data.PhaseOrder != expectedOrder {
		return bootValidatorFailure(req, "boot phase order mismatch", map[string]any{"phase": data.Phase, "got": data.PhaseOrder, "want": expectedOrder})
	}
	if bootPriorPhasesMatch(data) {
		return bootValidatorPass("boot phase order valid")
	}
	return bootValidatorFailure(req, "boot prior phases mismatch", map[string]any{"phase": data.Phase, "prior_phases": data.PriorPhases})
}

func (v BootDurationValidator) ValidateArtifact(_ context.Context, req claims.ValidatorHandlerRequest) (claims.ValidatorHandlerResult, error) {
	data, err := claims.ArtifactData[claims.BootPhaseArtifactData](req.Artifact)
	if err != nil {
		return claims.ValidatorHandlerResult{}, err
	}
	if data.Duration < 0 {
		return bootValidatorFailure(req, "boot phase duration is negative", map[string]any{"duration": data.Duration.String()})
	}
	if v.MaxDuration <= 0 || data.Duration <= v.MaxDuration {
		return bootValidatorPass("boot phase duration valid")
	}
	return bootValidatorFailure(req, "boot phase duration exceeded", map[string]any{"duration": data.Duration.String(), "max": v.MaxDuration.String()})
}

func validateBootPhase(req claims.ValidatorHandlerRequest, phase string) (claims.ValidatorHandlerResult, error) {
	data, err := claims.ArtifactData[claims.BootPhaseArtifactData](req.Artifact)
	if err != nil {
		return claims.ValidatorHandlerResult{}, err
	}
	if data.Phase == phase && data.Ready && data.Status == claims.InfrastructureStatusOK && data.FailureReason == "" {
		return bootValidatorPass("boot " + phase + " valid")
	}
	return bootValidatorFailure(req, "boot phase contract failed", map[string]any{
		"phase":  data.Phase,
		"want":   phase,
		"ready":  data.Ready,
		"status": data.Status,
		"reason": data.FailureReason,
	})
}

func bootPriorPhasesMatch(data claims.BootPhaseArtifactData) bool {
	want := bootPhaseSequence()[:bootPhaseOrder(data.Phase)-1]
	if len(data.PriorPhases) != len(want) {
		return false
	}
	for idx, phase := range want {
		if strings.TrimSpace(data.PriorPhases[idx]) != phase {
			return false
		}
	}
	return true
}

func bootValidatorPass(reference string) (claims.ValidatorHandlerResult, error) {
	artifact := &claims.Artifact{ArtifactName: "boot_validation", Kind: claims.ArtifactKindReadiness, Reference: reference}
	if err := claims.SetArtifactData(artifact, claims.PresentationEvidenceArtifactData{Kind: "boot_validation", Reference: reference}); err != nil {
		return claims.ValidatorHandlerResult{}, err
	}
	return claims.ValidatorHandlerResult{ResultArtifact: artifact}, nil
}

func bootValidatorFailure(req claims.ValidatorHandlerRequest, description string, payload map[string]any) (claims.ValidatorHandlerResult, error) {
	err := &claims.ValidationError{
		Category:    claims.ValidationErrorCategoryQualityBar,
		Description: description,
		Payload:     payload,
		OccurredAt:  time.Now().UTC(),
	}
	artifact := &claims.Artifact{ArtifactName: "boot_validation_failure", Kind: claims.ArtifactKindErrorDiagnostic, Reference: description}
	if setErr := claims.SetArtifactData(artifact, claims.PresentationEvidenceArtifactData{Kind: "boot_validation_failure", Reference: description, Metadata: payload}); setErr != nil {
		return claims.ValidatorHandlerResult{}, setErr
	}
	_ = req
	return claims.ValidatorHandlerResult{ResultArtifact: artifact, Error: err}, nil
}

func defaultBootValidatorRegistrations() []claims.ValidatorRegistration {
	return []claims.ValidatorRegistration{
		bootValidatorRegistration("boot.setup", BootSetupValidator{}),
		bootValidatorRegistration("boot.detect", BootDetectValidator{}),
		bootValidatorRegistration("boot.allocate", BootAllocateValidator{}),
		bootValidatorRegistration("boot.ingest", BootIngestValidator{}),
		bootValidatorRegistration("boot.commit", BootCommitValidator{}),
		bootValidatorRegistration("boot.finalize", BootFinalizeValidator{}),
		bootValidatorRegistration("boot.phase_order", BootPhaseOrderValidator{}),
		bootValidatorRegistration("boot.duration", BootDurationValidator{}),
	}
}

func bootValidatorRegistration(id string, handler claims.ValidatorHandler) claims.ValidatorRegistration {
	return claims.ValidatorRegistration{
		ValidatorID:        "infrastructure." + id,
		ValidationType:     claims.ValidationTypeContract,
		ActionType:         claims.ActionTypeBoot,
		Determinism:        claims.HandlerDeterminismPure,
		Timeout:            time.Second,
		ConcurrencyBudget:  1,
		TargetArtifactName: "boot_phase",
		ArtifactDataType:   claims.ArtifactDataTypeBootPhase,
		ResultDataType:     claims.ArtifactDataTypePresentationEvidence,
		Handler:            handler,
	}
}
