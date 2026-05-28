package claims

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestProgrammaticValidatorDispatcherPassesRequiredValidation(t *testing.T) {
	registry := NewValidatorRegistry()
	reg := testValidatorRegistration(t, validatorHandlerFunc(func(context.Context, ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
		return ValidatorHandlerResult{ResultArtifact: &Artifact{Kind: ArtifactKindReadiness, Reference: "valid"}}, nil
	}))
	if _, err := registry.Register(reg); err != nil {
		t.Fatalf("Register: %v", err)
	}
	dispatcher := NewProgrammaticValidatorDispatcher(registry, fixedClock{t: time.Unix(10, 0)})
	result, err := dispatcher.DispatchValidation(context.Background(), testValidationDispatchRequest(true))
	if err != nil {
		t.Fatalf("DispatchValidation: %v", err)
	}
	if result.Status != ValidationStatusValidated || result.ResultArtifact == nil {
		t.Fatalf("result = %#v, want validated with artifact", result)
	}
}

func TestProgrammaticValidatorDispatcherHandlerErrorBecomesValidationError(t *testing.T) {
	registry := NewValidatorRegistry()
	reg := testValidatorRegistration(t, validatorHandlerFunc(func(context.Context, ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
		return ValidatorHandlerResult{}, errors.New("validator failed")
	}))
	if _, err := registry.Register(reg); err != nil {
		t.Fatalf("Register: %v", err)
	}
	result, err := NewProgrammaticValidatorDispatcher(registry, SystemClock{}).DispatchValidation(context.Background(), testValidationDispatchRequest(true))
	if err != nil {
		t.Fatalf("DispatchValidation: %v", err)
	}
	if result.Status != ValidationStatusErrored || result.Error == nil {
		t.Fatalf("result = %#v, want errored validation error", result)
	}
}

func TestProgrammaticValidatorDispatcherOptionalErrorDoesNotBlockRequired(t *testing.T) {
	registry := NewValidatorRegistry()
	reg := testValidatorRegistration(t, validatorHandlerFunc(func(context.Context, ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
		return ValidatorHandlerResult{Error: &ValidationError{Category: ValidationErrorCategoryHandler, Description: "optional failed"}}, nil
	}))
	if _, err := registry.Register(reg); err != nil {
		t.Fatalf("Register: %v", err)
	}
	result, err := NewProgrammaticValidatorDispatcher(registry, SystemClock{}).DispatchValidation(context.Background(), testValidationDispatchRequest(false))
	if err != nil {
		t.Fatalf("DispatchValidation: %v", err)
	}
	if result.Status != ValidationStatusErroredNotRequired {
		t.Fatalf("status = %s, want errored_not_required", result.Status)
	}
}

func TestValidatorRegistryRejectsMissingDeterminismAndArtifactContract(t *testing.T) {
	registry := NewValidatorRegistry()
	_, err := registry.Register(ValidatorRegistration{
		ValidationType:    ValidationTypeInspection,
		Timeout:           time.Second,
		ConcurrencyBudget: 1,
		Handler: validatorHandlerFunc(func(context.Context, ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
			return ValidatorHandlerResult{}, nil
		}),
	})
	if !errors.Is(err, ErrValidatorRegistrationInvalid) {
		t.Fatalf("Register error = %v, want invalid", err)
	}
}

func testValidatorRegistration(t *testing.T, handler ValidatorHandler) ValidatorRegistration {
	t.Helper()
	return ValidatorRegistration{
		ValidatorID:        "validator",
		ValidationType:     ValidationTypeInspection,
		ActionType:         ActionTypeTask,
		Determinism:        HandlerDeterminismPure,
		Timeout:            time.Second,
		ConcurrencyBudget:  1,
		TargetArtifactName: "evidence",
		ArtifactDataType:   "text/plain",
		Handler:            handler,
	}
}

func testValidationDispatchRequest(required bool) ValidationDispatchRequest {
	validation := &Validation{
		ID:                 "validation",
		Type:               ValidationTypeInspection,
		Required:           required,
		ValidatorID:        "validator",
		TargetArtifactName: "evidence",
		ArtifactDataType:   "text/plain",
	}
	return ValidationDispatchRequest{
		Claim:      &Claim{ID: "claim", ActionType: ActionTypeTask},
		Validation: validation,
		Artifact:   &Artifact{ArtifactName: "evidence", DataType: "text/plain", Kind: ArtifactKindReadiness, Reference: "evidence"},
		StartedAt:  time.Unix(9, 0),
	}
}

type validatorHandlerFunc func(context.Context, ValidatorHandlerRequest) (ValidatorHandlerResult, error)

func (f validatorHandlerFunc) ValidateArtifact(ctx context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	return f(ctx, req)
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }
