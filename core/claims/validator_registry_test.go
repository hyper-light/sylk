package claims

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
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

func TestProgrammaticValidatorDispatcherHandlerErrorBecomesErrored(t *testing.T) {
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
	if result.Status != ValidationStatusErrored || result.Error == nil || result.Error.Category != ValidationErrorCategoryHandler {
		t.Fatalf("result = %#v, want handler errored result", result)
	}
}

func TestProgrammaticValidatorDispatcherOptionalHandlerErrorBecomesErroredNotRequired(t *testing.T) {
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
		return ValidatorHandlerResult{}, ctx.Err()
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

func TestBoardValidatorDispatcherRunsHandlerThroughTrackedScope(t *testing.T) {
	board := NewClaimsBoard(ClaimsBoardConfig{BoardID: "phase345-scoped-validator", SessionID: "session", TaskID: "task"})
	claimID := postPhase345Claim(t, board, "plan.scoped", true)
	_, validationID := generatePhase345PlanTestament(t, board, claimID)
	var calls atomic.Int32
	registry := phase345PlanValidatorRegistry(t, "plan.scoped", &calls, nil)
	scope := &recordingValidatorScope{}
	dispatcher, err := NewBoardValidatorDispatcher(BoardValidatorDispatcherConfig{
		Board:          board,
		Registry:       registry,
		Scope:          scope,
		MaxInputBytes:  1024,
		MaxOutputBytes: 1024,
	})
	if err != nil {
		t.Fatalf("NewBoardValidatorDispatcher: %v", err)
	}
	result, err := dispatcher.DispatchValidationByID(context.Background(), claimID, validationID)
	if err != nil {
		t.Fatalf("DispatchValidationByID: %v", err)
	}
	if result.Status != ValidationStatusValidated || calls.Load() != 1 {
		t.Fatalf("dispatch result = %+v calls=%d, want validated once", result, calls.Load())
	}
	if scope.calls.Load() != 1 || scope.description != "claims_validator_handler" || scope.timeout != time.Second {
		t.Fatalf("scope = calls:%d desc:%q timeout:%s, want one validator handler call at registration timeout", scope.calls.Load(), scope.description, scope.timeout)
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

func TestValidatorRegistryDuplicateImmutableRegistrationIsIdempotentAndConflictsOnSameKeyChange(t *testing.T) {
	registry := NewValidatorRegistry()
	reg := testValidatorRegistration(t, validatorHandlerFunc(func(context.Context, ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
		return ValidatorHandlerResult{}, nil
	}))
	if _, err := registry.Register(reg); err != nil {
		t.Fatalf("Register first: %v", err)
	}
	again, err := registry.Register(reg)
	if err != nil {
		t.Fatalf("Register duplicate immutable: %v", err)
	}
	if again.ValidatorID != reg.ValidatorID || again.ActionType != reg.ActionType {
		t.Fatalf("duplicate registration = %+v, want original immutable registration", again)
	}
	conflict := reg
	conflict.Timeout = 2 * time.Second
	if _, err := registry.Register(conflict); !errors.Is(err, ErrValidatorRegistrationConflict) {
		t.Fatalf("Register changed same-key immutable field error = %v, want conflict", err)
	}
	actionSpecific := reg
	actionSpecific.ActionType = ActionTypeConsultation
	if _, err := registry.Register(actionSpecific); err != nil {
		t.Fatalf("Register same validator scoped to different action: %v", err)
	}
}

func TestValidatorRegistryLookupPrefersExactThenWildcardRegistrations(t *testing.T) {
	registry := NewValidatorRegistry()
	handler := validatorHandlerFunc(func(context.Context, ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
		return ValidatorHandlerResult{}, nil
	})
	wildcard := testValidatorRegistration(t, handler)
	wildcard.ValidatorID = ""
	wildcard.ActionType = ""
	if _, err := registry.Register(wildcard); err != nil {
		t.Fatalf("Register wildcard: %v", err)
	}
	actionWildcard := wildcard
	actionWildcard.ActionType = ActionTypeTask
	actionWildcard.TargetArtifactName = "plan"
	if _, err := registry.Register(actionWildcard); err != nil {
		t.Fatalf("Register action wildcard: %v", err)
	}
	validatorWildcard := testValidatorRegistration(t, handler)
	validatorWildcard.ActionType = ""
	validatorWildcard.TargetArtifactName = "summary"
	if _, err := registry.Register(validatorWildcard); err != nil {
		t.Fatalf("Register validator wildcard: %v", err)
	}
	exact := testValidatorRegistration(t, handler)
	exact.ActionType = ActionTypeTask
	exact.TargetArtifactName = "exact"
	if _, err := registry.Register(exact); err != nil {
		t.Fatalf("Register exact: %v", err)
	}
	if got, ok := registry.Lookup(&Claim{ActionType: ActionTypeTask}, &Validation{ValidatorID: "validator", Type: ValidationTypeInspection}); !ok || got.TargetArtifactName != "exact" {
		t.Fatalf("exact lookup = %#v ok=%v, want exact action registration", got, ok)
	}
	if got, ok := registry.Lookup(&Claim{ActionType: ActionTypeConsultation}, &Validation{ValidatorID: "validator", Type: ValidationTypeInspection}); !ok || got.TargetArtifactName != "summary" {
		t.Fatalf("validator wildcard lookup = %#v ok=%v, want exact validator wildcard-action registration", got, ok)
	}
	if got, ok := registry.Lookup(&Claim{ActionType: ActionTypeTask}, &Validation{ValidatorID: "missing", Type: ValidationTypeInspection}); !ok || got.TargetArtifactName != "plan" {
		t.Fatalf("action wildcard lookup = %#v ok=%v, want wildcard validator exact-action registration", got, ok)
	}
	if got, ok := registry.Lookup(&Claim{ActionType: ActionTypeConsultation}, &Validation{Type: ValidationTypeInspection}); !ok || got.TargetArtifactName != "evidence" {
		t.Fatalf("full wildcard lookup = %#v ok=%v, want generic registration", got, ok)
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

func TestRegisterValidatorGenericIsIdempotentAndAllowsSameTypePairDifferentID(t *testing.T) {
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
	if got, err := RegisterValidator[PlanMarkdownArtifactData, PresentationEvidenceArtifactData](registry, cfg, handler); err != nil || got.ValidatorID != cfg.ID {
		t.Fatalf("duplicate RegisterValidator = %+v err=%v, want idempotent registration", got, err)
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

func TestPhase345GeneratedTestamentAttachesArtifactsAndPublishesLifecycleDeltas(t *testing.T) {
	bus := newCaptureBus()
	board := NewClaimsBoard(ClaimsBoardConfig{BoardID: "phase345-board", SessionID: "session", TaskID: "task", DeltaBus: bus})
	claimID := postPhase345Claim(t, board, "plan.markdown", true)
	generated, err := board.GenerateTestamentAction(context.Background(), Action{AgentID: "engineer", Type: ActionTypeTestament}, []Testament{phase345PlanTestament(t, claimID)}, GenerateTestamentActionOptions{})
	if err != nil {
		t.Fatalf("GenerateTestamentAction: %v", err)
	}
	stored := generated.Testaments[0].Artifacts[0]
	if stored.Status != ArtifactStatusAttached {
		t.Fatalf("artifact status = %s, want attached", stored.Status)
	}
	assertPhase345StatusHistory(t, stored.StatusHistory, ArtifactStatusGenerated, ArtifactStatusAttached)
	assertCanonicalActionPublished(t, bus, DeltaActionArtifactGenerated)
	assertCanonicalActionPublished(t, bus, DeltaActionArtifactAttached)
}

func TestPhase345StandaloneArtifactGeneratedReceivedAttachedAndReplayed(t *testing.T) {
	dir := t.TempDir()
	bus := newCaptureBus()
	cfg := ClaimsBoardConfig{BoardID: "phase345-standalone-artifact", SessionID: "session", TaskID: "task", SessionDir: filepath.Join(dir, "session"), DeltaBus: bus}
	db, err := OpenDurableBoard(cfg)
	if err != nil {
		t.Fatalf("OpenDurableBoard: %v", err)
	}
	claimID := postPhase345Claim(t, db.Board(), "plan.markdown", true)
	artifact := phase345PlanArtifact(t, "plan")
	artifact.ClaimID = claimID
	generated, err := db.Board().GenerateArtifact(context.Background(), *artifact, "engineer", ArtifactLifecycleOptions{Reason: "plan streamed"})
	if err != nil {
		t.Fatalf("GenerateArtifact: %v", err)
	}
	if generated.Status != ArtifactStatusGenerated || generated.TestamentID != "" {
		t.Fatalf("generated artifact = %+v, want generated and unattached", generated)
	}
	if err := db.Board().AcknowledgeArtifactReceipt(context.Background(), generated.ID, "architect"); err != nil {
		t.Fatalf("AcknowledgeArtifactReceipt: %v", err)
	}
	testament := Testament{
		AgentID:   "engineer",
		Summary:   "plan ready",
		Relations: []Relation{{Related: claimID, RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim}},
		Artifacts: []*Artifact{{ID: generated.ID, ArtifactName: "plan"}},
	}
	action, err := db.Board().GenerateTestamentAction(context.Background(), Action{AgentID: "engineer", Type: ActionTypeTestament}, []Testament{testament}, GenerateTestamentActionOptions{})
	if err != nil {
		t.Fatalf("GenerateTestamentAction: %v", err)
	}
	stored := action.Testaments[0].Artifacts[0]
	if stored.Status != ArtifactStatusAttached || stored.TestamentID == "" {
		t.Fatalf("attached artifact = %+v, want attached with testament", stored)
	}
	assertPhase345StatusHistory(t, stored.StatusHistory, ArtifactStatusGenerated, ArtifactStatusReceived, ArtifactStatusAttached)
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened, err := OpenDurableBoard(cfg)
	if err != nil {
		t.Fatalf("reopen durable: %v", err)
	}
	defer reopened.Close()
	replayed, ok := reopened.Board().CloneArtifact(generated.ID)
	if !ok {
		t.Fatalf("artifact %q not replayed", generated.ID)
	}
	if replayed.Status != ArtifactStatusAttached || replayed.TestamentID == "" {
		t.Fatalf("replayed artifact = %+v, want attached with testament", replayed)
	}
	assertPhase345StatusHistory(t, replayed.StatusHistory, ArtifactStatusGenerated, ArtifactStatusReceived, ArtifactStatusAttached)
	assertCanonicalActionPublished(t, bus, DeltaActionArtifactGenerated)
	assertCanonicalActionPublished(t, bus, DeltaActionArtifactReceived)
	assertCanonicalActionPublished(t, bus, DeltaActionArtifactAttached)
}

func TestPhase345ValidationLifecycleDurableReplayAndOutboxProjection(t *testing.T) {
	dir := t.TempDir()
	bus := newCaptureBus()
	cfg := ClaimsBoardConfig{BoardID: "phase345-durable", SessionID: "session", TaskID: "task", SessionDir: filepath.Join(dir, "session"), DeltaBus: bus}
	db, err := OpenDurableBoard(cfg)
	if err != nil {
		t.Fatalf("OpenDurableBoard: %v", err)
	}
	claimID := postPhase345Claim(t, db.Board(), "plan.markdown", true)
	artifactID, validationID := generatePhase345PlanTestament(t, db.Board(), claimID)
	if err := db.BeginValidation(context.Background(), claimID, validationID, "plan.markdown", artifactID); err != nil {
		t.Fatalf("BeginValidation: %v", err)
	}
	resultArtifact := &Artifact{ArtifactName: "plan_validation", Kind: ArtifactKindReadiness, Reference: "valid"}
	if err := SetArtifactData(resultArtifact, PresentationEvidenceArtifactData{Kind: "validation", Reference: "valid"}); err != nil {
		t.Fatalf("SetArtifactData result: %v", err)
	}
	if err := db.CompleteValidationLifecycle(context.Background(), claimID, validationID, "plan.markdown", ValidationStatusValidated, ValidationLifecycleOptions{
		Reason:           "plan is valid",
		TargetArtifactID: artifactID,
		ResultArtifact:   resultArtifact,
	}); err != nil {
		t.Fatalf("CompleteValidationLifecycle: %v", err)
	}
	if drained := db.DrainOutbox(context.Background(), 128); drained == 0 {
		t.Fatal("DrainOutbox drained no records")
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened, err := OpenDurableBoard(cfg)
	if err != nil {
		t.Fatalf("reopen durable: %v", err)
	}
	defer reopened.Close()
	validation, _, ok := reopened.Board().CloneValidation(validationID)
	if !ok {
		t.Fatalf("validation %q not replayed", validationID)
	}
	if validation.Status != ValidationStatusValidated || validation.ResultArtifactID == "" {
		t.Fatalf("replayed validation = %+v, want validated with result artifact", validation)
	}
	result, ok := reopened.Board().CloneArtifact(validation.ResultArtifactID)
	if !ok || result.Status != ArtifactStatusGenerated {
		t.Fatalf("result artifact replay = %+v ok=%v, want generated artifact", result, ok)
	}
	claim, ok := reopened.Board().CloneClaim(claimID)
	if !ok || claim.Status != ClaimStatusAccepted || claim.LifecycleStatus != ClaimLifecycleSatisfied {
		t.Fatalf("replayed claim = %+v ok=%v, want accepted/satisfied", claim, ok)
	}
	assertCanonicalActionPublished(t, bus, DeltaActionValidationValidating)
	assertCanonicalActionPublished(t, bus, DeltaActionValidationValidated)
	assertCanonicalActionPublished(t, bus, DeltaActionArtifactGenerated)
}

func TestPhase345TypedValidatorDispatcherCommitsResultArtifact(t *testing.T) {
	board := NewClaimsBoard(ClaimsBoardConfig{BoardID: "phase345-validator", SessionID: "session", TaskID: "task"})
	claimID := postPhase345Claim(t, board, "plan.markdown", true)
	_, validationID := generatePhase345PlanTestament(t, board, claimID)
	var calls atomic.Int32
	registry := phase345PlanValidatorRegistry(t, "plan.markdown", &calls, nil)
	dispatcher := newPhase345Dispatcher(t, board, registry, int64(len(phase345PlanArtifact(t, "plan").Data)), 1024)
	result, err := dispatcher.DispatchValidationByID(context.Background(), claimID, validationID)
	if err != nil {
		t.Fatalf("DispatchValidationByID: %v", err)
	}
	if result.Status != ValidationStatusValidated || calls.Load() != 1 {
		t.Fatalf("dispatch result = %+v calls=%d, want validated once", result, calls.Load())
	}
	validation, _, _ := board.CloneValidation(validationID)
	if validation.ResultArtifactID == "" || validation.Status != ValidationStatusValidated {
		t.Fatalf("validation after dispatch = %+v", validation)
	}
	if artifact, ok := board.CloneArtifact(validation.ResultArtifactID); !ok || artifact.Status != ArtifactStatusGenerated {
		t.Fatalf("result artifact = %+v ok=%v", artifact, ok)
	}
	claim, ok := board.CloneClaim(claimID)
	if !ok || claim.Status != ClaimStatusAccepted || claim.LifecycleStatus != ClaimLifecycleSatisfied {
		t.Fatalf("claim after validation = %+v ok=%v, want accepted/satisfied", claim, ok)
	}
}

func TestPhase345BoardValidatorDispatcherRejectsPolicyInputAndDuplicateDispatch(t *testing.T) {
	t.Run("side effect requires approval", func(t *testing.T) {
		board := NewClaimsBoard(ClaimsBoardConfig{BoardID: "phase345-policy", SessionID: "session", TaskID: "task"})
		claimID := postPhase345Claim(t, board, "plan.side_effect", true)
		_, validationID := generatePhase345PlanTestament(t, board, claimID)
		registry := NewValidatorRegistry()
		if _, err := registry.Register(ValidatorRegistration{
			ValidatorID:        "plan.side_effect",
			ValidationType:     ValidationTypeInspection,
			ActionType:         ActionTypeTask,
			Determinism:        HandlerDeterminismSideEffect,
			Timeout:            time.Second,
			ConcurrencyBudget:  1,
			TargetArtifactName: "plan",
			ArtifactDataType:   ArtifactDataTypePlanMarkdown,
			ResultDataType:     ArtifactDataTypePresentationEvidence,
			Handler: validatorHandlerFunc(func(context.Context, ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
				return ValidatorHandlerResult{}, errors.New("handler should not run")
			}),
		}); err != nil {
			t.Fatalf("Register: %v", err)
		}
		dispatcher := newPhase345Dispatcher(t, board, registry, 1024, 1024)
		result, err := dispatcher.DispatchValidationByID(context.Background(), claimID, validationID)
		if err != nil {
			t.Fatalf("DispatchValidationByID: %v", err)
		}
		if result.Status != ValidationStatusErrored || result.Error == nil || result.Error.Category != ValidationErrorCategoryDispatcher {
			t.Fatalf("policy result = %+v, want dispatcher errored", result)
		}
	})
	t.Run("input limit prevents handler", func(t *testing.T) {
		board := NewClaimsBoard(ClaimsBoardConfig{BoardID: "phase345-input-limit", SessionID: "session", TaskID: "task"})
		claimID := postPhase345Claim(t, board, "plan.input_limit", true)
		artifactID, validationID := generatePhase345PlanTestament(t, board, claimID)
		artifact, ok := board.CloneArtifact(artifactID)
		if !ok {
			t.Fatalf("artifact %q not found", artifactID)
		}
		var calls atomic.Int32
		registry := phase345PlanValidatorRegistry(t, "plan.input_limit", &calls, nil)
		dispatcher := newPhase345Dispatcher(t, board, registry, int64(len(artifact.Data))-1, 1024)
		result, err := dispatcher.DispatchValidationByID(context.Background(), claimID, validationID)
		if err != nil {
			t.Fatalf("DispatchValidationByID: %v", err)
		}
		if calls.Load() != 0 {
			t.Fatalf("handler calls = %d, want 0", calls.Load())
		}
		if result.Status != ValidationStatusErrored || result.Error == nil || result.Error.Category != ValidationErrorCategoryDispatcher {
			t.Fatalf("result = %+v, want dispatcher errored input-limit result", result)
		}
		claim, ok := board.CloneClaim(claimID)
		if !ok || claim.Status != ClaimStatusRejected || claim.LifecycleStatus != ClaimLifecycleValidationErrored {
			t.Fatalf("claim after input-limit validation = %+v ok=%v, want rejected/validation_errored", claim, ok)
		}
	})
	t.Run("duplicate dispatch rejected", func(t *testing.T) {
		board := NewClaimsBoard(ClaimsBoardConfig{BoardID: "phase345-concurrent-dispatch", SessionID: "session", TaskID: "task"})
		claimID := postPhase345Claim(t, board, "plan.concurrent", true)
		_, validationID := generatePhase345PlanTestament(t, board, claimID)
		started := make(chan struct{})
		release := make(chan struct{})
		var calls atomic.Int32
		registry := phase345PlanValidatorRegistry(t, "plan.concurrent", &calls, func() {
			if calls.Load() == 1 {
				close(started)
			}
			<-release
		})
		dispatcher := newPhase345Dispatcher(t, board, registry, 1024, 1024)
		done := make(chan error, 1)
		go func() {
			_, err := dispatcher.DispatchValidationByID(context.Background(), claimID, validationID)
			done <- err
		}()
		<-started
		if _, err := dispatcher.DispatchValidationByID(context.Background(), claimID, validationID); !errors.Is(err, ErrValidatorDispatchInvalid) {
			t.Fatalf("duplicate dispatch error = %v, want invalid status error", err)
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatalf("first dispatch failed: %v", err)
		}
		if calls.Load() != 1 {
			t.Fatalf("handler calls = %d, want 1", calls.Load())
		}
	})
}

func TestPhase345BoardValidatorDispatcherRejectsUnparentedArtifactBeforeHandler(t *testing.T) {
	board := NewClaimsBoard(ClaimsBoardConfig{BoardID: "phase345-parentage", SessionID: "session", TaskID: "task"})
	claimID := postPhase345Claim(t, board, "plan.parentage", true)
	_, validationID := generatePhase345PlanTestament(t, board, claimID)
	validation, claim, ok := board.CloneValidation(validationID)
	if !ok {
		t.Fatalf("validation %q not found", validationID)
	}
	unparented := phase345PlanArtifact(t, "plan")
	unparented.ID = "not-on-board"
	unparented.TestamentID = "not-parent"
	var calls atomic.Int32
	registry := phase345PlanValidatorRegistry(t, "plan.parentage", &calls, nil)
	dispatcher := newPhase345Dispatcher(t, board, registry, 1024, 1024)
	if _, err := dispatcher.DispatchValidation(context.Background(), ValidationDispatchRequest{Claim: claim, Validation: validation, Artifact: unparented}); !errors.Is(err, ErrValidatorDispatchInvalid) {
		t.Fatalf("DispatchValidation error = %v, want invalid parentage", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("handler calls = %d, want 0", calls.Load())
	}
}

func newPhase345Dispatcher(t *testing.T, board *ClaimsBoard, registry *ValidatorRegistry, maxInput, maxOutput int64) *BoardValidatorDispatcher {
	t.Helper()
	dispatcher, err := NewBoardValidatorDispatcher(BoardValidatorDispatcherConfig{
		Board:          board,
		Registry:       registry,
		MaxInputBytes:  maxInput,
		MaxOutputBytes: maxOutput,
	})
	if err != nil {
		t.Fatalf("NewBoardValidatorDispatcher: %v", err)
	}
	return dispatcher
}

func phase345PlanValidatorRegistry(t *testing.T, validatorID string, calls *atomic.Int32, beforeReturn func()) *ValidatorRegistry {
	t.Helper()
	registry := NewValidatorRegistry()
	if _, err := RegisterValidator[PlanMarkdownArtifactData, PresentationEvidenceArtifactData](registry, ValidatorConfig{
		ID:                 validatorID,
		ValidationType:     ValidationTypeInspection,
		ActionType:         ActionTypeTask,
		Determinism:        HandlerDeterminismPure,
		Timeout:            time.Second,
		ConcurrencyBudget:  1,
		TargetArtifactName: "plan",
	}, func(_ context.Context, data PlanMarkdownArtifactData) (*Artifact, error) {
		calls.Add(1)
		if beforeReturn != nil {
			beforeReturn()
		}
		result := &Artifact{ArtifactName: "plan_validation", Kind: ArtifactKindReadiness, Reference: data.Title}
		if err := SetArtifactData(result, PresentationEvidenceArtifactData{Kind: "validation", Reference: data.Markdown}); err != nil {
			return nil, err
		}
		return result, nil
	}); err != nil {
		t.Fatalf("RegisterValidator: %v", err)
	}
	return registry
}

func postPhase345Claim(t *testing.T, board *ClaimsBoard, validatorID string, required bool) string {
	t.Helper()
	generated, err := board.GenerateClaimAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{{
		Title:       "Plan",
		Description: "Produce a plan.",
		ActionType:  ActionTypeTask,
		Relations: []Relation{
			{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: "engineer", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		},
		Validations: []*Validation{{
			ID:                 "phase345-validation",
			Type:               ValidationTypeInspection,
			Description:        "plan validates",
			Required:           required,
			ValidatorID:        validatorID,
			TargetArtifactName: "plan",
			ArtifactDataType:   ArtifactDataTypePlanMarkdown,
			ResultDataType:     ArtifactDataTypePresentationEvidence,
		}},
	}}, GenerateClaimActionOptions{IdempotencyKey: validatorID})
	if err != nil {
		t.Fatalf("GenerateClaimAction: %v", err)
	}
	claimID := generated.Claims[0].ID
	if err := board.PostGeneratedClaim(context.Background(), claimID, "architect", ClaimPostOptions{Reason: "phase345 posted"}); err != nil {
		t.Fatalf("PostGeneratedClaim: %v", err)
	}
	return claimID
}

func generatePhase345PlanTestament(t *testing.T, board *ClaimsBoard, claimID string) (string, string) {
	t.Helper()
	generated, err := board.GenerateTestamentAction(context.Background(), Action{AgentID: "engineer", Type: ActionTypeTestament}, []Testament{phase345PlanTestament(t, claimID)}, GenerateTestamentActionOptions{})
	if err != nil {
		t.Fatalf("GenerateTestamentAction: %v", err)
	}
	claim, ok := board.CloneClaim(claimID)
	if !ok || len(claim.Validations) == 0 {
		t.Fatalf("claim %q validations not found", claimID)
	}
	return generated.Testaments[0].Artifacts[0].ID, claim.Validations[0].ID
}

func phase345PlanTestament(t *testing.T, claimID string) Testament {
	t.Helper()
	return Testament{
		AgentID:   "engineer",
		Summary:   "plan ready",
		Relations: []Relation{{Related: claimID, RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim}},
		Artifacts: []*Artifact{phase345PlanArtifact(t, "plan")},
	}
}

func phase345PlanArtifact(t *testing.T, name string) *Artifact {
	t.Helper()
	artifact := &Artifact{ArtifactName: name, Kind: ArtifactKindPlanMarkdown, Reference: "# Plan"}
	if err := SetArtifactData(artifact, PlanMarkdownArtifactData{Markdown: "# Plan", Title: "Plan"}); err != nil {
		t.Fatalf("SetArtifactData: %v", err)
	}
	return artifact
}

func assertCanonicalActionPublished(t *testing.T, bus *captureBus, action DeltaAction) {
	t.Helper()
	bus.mu.Lock()
	defer bus.mu.Unlock()
	for _, published := range bus.published {
		delta, ok := published.delta.(CanonicalDelta)
		if !ok || delta.Action != action {
			continue
		}
		if err := ValidateCanonicalDeltaStrict(delta); err != nil {
			t.Fatalf("published %s delta invalid: %v", action, err)
		}
		return
	}
	t.Fatalf("canonical action %s not published; published=%d", action, len(bus.published))
}

func assertPhase345StatusHistory(t *testing.T, history []StatusChange, want ...ArtifactStatus) {
	t.Helper()
	if len(history) < len(want) {
		t.Fatalf("history length = %d, want at least %d: %+v", len(history), len(want), history)
	}
	for i, status := range want {
		if ArtifactStatus(history[i].To) != status {
			t.Fatalf("history[%d].To = %q, want %q; history=%+v", i, history[i].To, status, history)
		}
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

type recordingValidatorScope struct {
	calls       atomic.Int32
	description string
	timeout     time.Duration
}

func (s *recordingValidatorScope) Go(description string, timeout time.Duration, fn func(context.Context) error) error {
	s.calls.Add(1)
	s.description = description
	s.timeout = timeout
	return fn(context.Background())
}
