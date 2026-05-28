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
		artifact := &Artifact{Kind: ArtifactKindReadiness, Reference: "valid"}
		if err := SetArtifactData(artifact, PresentationEvidenceArtifactData{Kind: "validation", Reference: "valid"}); err != nil {
			t.Fatalf("SetArtifactData result: %v", err)
		}
		return ValidatorHandlerResult{ResultArtifact: artifact}, nil
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

func TestProgrammaticValidatorDispatcherRecoversPanicAsErrored(t *testing.T) {
	registry := NewValidatorRegistry()
	reg := testValidatorRegistration(t, validatorHandlerFunc(func(context.Context, ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
		panic("validator exploded")
	}))
	if _, err := registry.Register(reg); err != nil {
		t.Fatalf("Register: %v", err)
	}
	result, err := NewProgrammaticValidatorDispatcher(registry, SystemClock{}).DispatchValidation(context.Background(), testValidationDispatchRequest(true))
	if err != nil {
		t.Fatalf("DispatchValidation: %v", err)
	}
	if result.Status != ValidationStatusErrored || result.Error == nil || result.Error.Category != ValidationErrorCategoryPanic {
		t.Fatalf("result = %#v, want panic errored result", result)
	}
	if result.ResultArtifact == nil || result.ResultArtifact.Kind != ArtifactKindErrorDiagnostic {
		t.Fatalf("panic result artifact = %#v, want error diagnostic", result.ResultArtifact)
	}
}

func TestProgrammaticValidatorDispatcherTimeoutBecomesErrored(t *testing.T) {
	registry := NewValidatorRegistry()
	reg := testValidatorRegistration(t, validatorHandlerFunc(func(ctx context.Context, _ ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
		<-ctx.Done()
		return ValidatorHandlerResult{}, nil
	}))
	reg.Timeout = time.Nanosecond
	if _, err := registry.Register(reg); err != nil {
		t.Fatalf("Register: %v", err)
	}
	result, err := NewProgrammaticValidatorDispatcher(registry, SystemClock{}).DispatchValidation(context.Background(), testValidationDispatchRequest(true))
	if err != nil {
		t.Fatalf("DispatchValidation: %v", err)
	}
	if result.Status != ValidationStatusErrored || result.Error == nil || result.Error.Category != ValidationErrorCategoryTimeout {
		t.Fatalf("result = %#v, want timeout errored result", result)
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

func TestValidatorRegistryDuplicateImmutableRegistrationIsIdempotent(t *testing.T) {
	registry := NewValidatorRegistry()
	reg := testValidatorRegistration(t, validatorHandlerFunc(func(context.Context, ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
		return ValidatorHandlerResult{}, nil
	}))
	first, err := registry.Register(reg)
	if err != nil {
		t.Fatalf("Register first: %v", err)
	}
	second, err := registry.Register(reg)
	if err != nil {
		t.Fatalf("Register duplicate: %v", err)
	}
	if first.ValidatorID != second.ValidatorID || first.ArtifactDataType != second.ArtifactDataType {
		t.Fatalf("duplicate registration changed immutable metadata: first=%#v second=%#v", first, second)
	}
	conflict := reg
	conflict.ResultDataType = ArtifactDataTypeKnowledgeReadiness
	if _, err := registry.Register(conflict); !errors.Is(err, ErrValidatorRegistrationConflict) {
		t.Fatalf("Register conflict error = %v, want conflict", err)
	}
}

func TestProgrammaticValidatorDispatcherRejectsArtifactContractBeforeHandler(t *testing.T) {
	called := false
	registry := NewValidatorRegistry()
	reg := testValidatorRegistration(t, validatorHandlerFunc(func(context.Context, ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
		called = true
		return ValidatorHandlerResult{}, nil
	}))
	if _, err := registry.Register(reg); err != nil {
		t.Fatalf("Register: %v", err)
	}
	req := testValidationDispatchRequest(true)
	req.Artifact.ArtifactName = "wrong"
	result, err := NewProgrammaticValidatorDispatcher(registry, SystemClock{}).DispatchValidation(context.Background(), req)
	if err != nil {
		t.Fatalf("DispatchValidation: %v", err)
	}
	if called {
		t.Fatal("handler was invoked for artifact contract mismatch")
	}
	if result.Status != ValidationStatusValidationFailed || result.Error == nil || result.Error.Category != ValidationErrorCategoryArtifactType {
		t.Fatalf("result = %#v, want artifact type validation failure", result)
	}
}

func TestProgrammaticValidatorDispatcherRejectsMismatchedResultDataType(t *testing.T) {
	registry := NewValidatorRegistry()
	reg := testValidatorRegistration(t, validatorHandlerFunc(func(context.Context, ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
		artifact := &Artifact{Kind: ArtifactKindReadiness, Reference: "wrong result"}
		if err := SetArtifactData(artifact, KnowledgeReadinessArtifactData{Component: "validator"}); err != nil {
			t.Fatalf("SetArtifactData result: %v", err)
		}
		return ValidatorHandlerResult{ResultArtifact: artifact}, nil
	}))
	if _, err := registry.Register(reg); err != nil {
		t.Fatalf("Register: %v", err)
	}
	result, err := NewProgrammaticValidatorDispatcher(registry, SystemClock{}).DispatchValidation(context.Background(), testValidationDispatchRequest(true))
	if err != nil {
		t.Fatalf("DispatchValidation: %v", err)
	}
	if result.Status != ValidationStatusValidationFailed || result.Error == nil || result.Error.Category != ValidationErrorCategoryArtifactType {
		t.Fatalf("result = %#v, want artifact type validation failure", result)
	}
}

func TestProgrammaticValidatorDispatcherRejectsUnknownDeclaredResultDataTypeBeforeHandler(t *testing.T) {
	called := false
	registry := NewValidatorRegistry()
	reg := testValidatorRegistration(t, validatorHandlerFunc(func(context.Context, ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
		called = true
		return ValidatorHandlerResult{}, nil
	}))
	reg.ResultDataType = ""
	if _, err := registry.Register(reg); err != nil {
		t.Fatalf("Register: %v", err)
	}
	req := testValidationDispatchRequest(true)
	req.Validation.ResultDataType = "unknown.result.v1"
	result, err := NewProgrammaticValidatorDispatcher(registry, SystemClock{}).DispatchValidation(context.Background(), req)
	if err != nil {
		t.Fatalf("DispatchValidation: %v", err)
	}
	if called {
		t.Fatal("handler was invoked for unknown declared result datatype")
	}
	if result.Status != ValidationStatusValidationFailed || result.Error == nil || result.Error.Category != ValidationErrorCategoryArtifactType {
		t.Fatalf("result = %#v, want artifact type validation failure", result)
	}
}

func TestValidatorRegistryRejectsUnknownRegisteredDataTypes(t *testing.T) {
	reg := testValidatorRegistration(t, validatorHandlerFunc(func(context.Context, ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
		return ValidatorHandlerResult{}, nil
	}))
	reg.ArtifactDataType = "unknown.v1"
	_, err := NewValidatorRegistry().Register(reg)
	if !errors.Is(err, ErrValidatorRegistrationInvalid) {
		t.Fatalf("Register unknown artifact datatype error = %v, want invalid", err)
	}
}

func TestRegisterValidatorGenericRejectsDuplicateAndAllowsSameTypePairDifferentID(t *testing.T) {
	registry := NewValidatorRegistry()
	handler := func(context.Context, PlanMarkdownArtifactData) (*Artifact, error) {
		artifact := &Artifact{Kind: ArtifactKindReadiness, Reference: "ok"}
		if err := SetArtifactData(artifact, PresentationEvidenceArtifactData{Kind: "validation", Reference: "ok"}); err != nil {
			return nil, err
		}
		return artifact, nil
	}
	cfg := ValidatorConfig{
		ID:                 "plan.validator.one",
		ValidationType:     ValidationTypeInspection,
		ActionType:         ActionTypeTask,
		Determinism:        HandlerDeterminismPure,
		Timeout:            time.Second,
		ConcurrencyBudget:  1,
		TargetArtifactName: "evidence",
	}
	if _, err := RegisterValidator[PlanMarkdownArtifactData, PresentationEvidenceArtifactData](registry, cfg, handler); err != nil {
		t.Fatalf("RegisterValidator first: %v", err)
	}
	if _, err := RegisterValidator[PlanMarkdownArtifactData, PresentationEvidenceArtifactData](registry, cfg, handler); err != nil {
		t.Fatalf("duplicate RegisterValidator should be idempotent: %v", err)
	}
	cfg.ID = "plan.validator.two"
	if _, err := RegisterValidator[PlanMarkdownArtifactData, PresentationEvidenceArtifactData](registry, cfg, handler); err != nil {
		t.Fatalf("same type pair different ID failed: %v", err)
	}
}

func TestRegisterValidatorGenericRejectsUnknownTypeAndNilHandler(t *testing.T) {
	registry := NewValidatorRegistry()
	cfg := ValidatorConfig{
		ID:                 "unknown.validator",
		ValidationType:     ValidationTypeInspection,
		ActionType:         ActionTypeTask,
		Determinism:        HandlerDeterminismPure,
		Timeout:            time.Second,
		ConcurrencyBudget:  1,
		TargetArtifactName: "evidence",
	}
	if _, err := RegisterValidator[struct{ Name string }, PresentationEvidenceArtifactData](registry, cfg, func(context.Context, struct{ Name string }) (*Artifact, error) {
		return &Artifact{}, nil
	}); !errors.Is(err, ErrValidatorRegistrationInvalid) {
		t.Fatalf("unknown input type error = %v, want invalid", err)
	}
	if _, err := RegisterValidator[PlanMarkdownArtifactData, PresentationEvidenceArtifactData](registry, cfg, nil); !errors.Is(err, ErrValidatorRegistrationInvalid) {
		t.Fatalf("nil handler error = %v, want invalid", err)
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
		ArtifactDataType:   ArtifactDataTypePlanMarkdown,
		ResultDataType:     ArtifactDataTypePresentationEvidence,
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
		ArtifactDataType:   ArtifactDataTypePlanMarkdown,
		ResultDataType:     ArtifactDataTypePresentationEvidence,
	}
	artifact := &Artifact{ArtifactName: "evidence", Kind: ArtifactKindReadiness, Reference: "evidence"}
	if err := SetArtifactData(artifact, PlanMarkdownArtifactData{Markdown: "# Evidence"}); err != nil {
		panic(err)
	}
	return ValidationDispatchRequest{
		Claim:      &Claim{ID: "claim", ActionType: ActionTypeTask},
		Validation: validation,
		Artifact:   artifact,
		StartedAt:  time.Unix(9, 0),
	}
}

type validatorHandlerFunc func(context.Context, ValidatorHandlerRequest) (ValidatorHandlerResult, error)

func (f validatorHandlerFunc) ValidateArtifact(ctx context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	return f(ctx, req)
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }
