package claims

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

var (
	ErrValidatorRegistrationInvalid  = errors.New("validator registration invalid")
	ErrValidatorRegistrationConflict = errors.New("validator registration conflicts with existing immutable metadata")
	ErrValidatorNotRegistered        = errors.New("validator is not registered")
	ErrValidatorConcurrencyExhausted = errors.New("validator concurrency budget exhausted")
	ErrValidatorDispatchInvalid      = errors.New("validator dispatch invalid")
)

type ValidatorRegistration struct {
	ValidatorID        string
	ValidationType     ValidationType
	ActionType         ActionType
	Determinism        HandlerDeterminism
	Timeout            time.Duration
	ConcurrencyBudget  int
	TargetArtifactName string
	ArtifactDataType   string
	ResultDataType     string
	Handler            ValidatorHandler
}

// Validator is the typed registration surface. The registry erases the
// concrete input and output types into ValidatorHandler after validating
// both types against the artifact data registry.
type Validator[T, R any] func(ctx context.Context, data T) (*Artifact, error)

type ValidatorConfig struct {
	ID                 string
	ValidationType     ValidationType
	ActionType         ActionType
	Determinism        HandlerDeterminism
	Timeout            time.Duration
	ConcurrencyBudget  int
	TargetArtifactName string
	ArtifactDataType   string
	ResultDataType     string
}

type ValidatorRegistry struct {
	mu      sync.RWMutex
	records map[string]ValidatorRegistration
	types   *TypeRegistry
}

type ProgrammaticValidatorDispatcher struct {
	registry *ValidatorRegistry
	mu       sync.Mutex
	limits   map[string]chan struct{}
	clock    ClaimsClock
	scope    ScopeProvider
}

func NewValidatorRegistry() *ValidatorRegistry {
	return NewValidatorRegistryWithTypes(DefaultTypeRegistry())
}

func NewValidatorRegistryWithTypes(types *TypeRegistry) *ValidatorRegistry {
	return &ValidatorRegistry{
		records: make(map[string]ValidatorRegistration),
		types:   types,
	}
}

func (r *ValidatorRegistry) Register(reg ValidatorRegistration) (ValidatorRegistration, error) {
	if r == nil {
		return ValidatorRegistration{}, fmt.Errorf("%w: registry is nil", ErrValidatorRegistrationInvalid)
	}
	normalized, err := normalizeValidatorRegistration(reg, r.typeRegistry())
	if err != nil {
		return ValidatorRegistration{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.registrationWithValidatorIDLocked(normalized.ValidatorID); ok {
		return existing, ErrValidatorRegistrationConflict
	}
	r.records[validatorRegistrationKey(normalized)] = normalized
	return normalized, nil
}

func RegisterValidator[T, R any](registry *ValidatorRegistry, config ValidatorConfig, handler Validator[T, R]) (ValidatorRegistration, error) {
	if registry == nil {
		return ValidatorRegistration{}, fmt.Errorf("%w: registry is nil", ErrValidatorRegistrationInvalid)
	}
	if handler == nil {
		return ValidatorRegistration{}, fmt.Errorf("%w: handler is required", ErrValidatorRegistrationInvalid)
	}
	types := registry.typeRegistry()
	inputType, err := artifactTypeForGeneric[T](types)
	if err != nil {
		return ValidatorRegistration{}, fmt.Errorf("%w: input type is not registered: %v", ErrValidatorRegistrationInvalid, err)
	}
	outputType, err := artifactTypeForGeneric[R](types)
	if err != nil {
		return ValidatorRegistration{}, fmt.Errorf("%w: output type is not registered: %v", ErrValidatorRegistrationInvalid, err)
	}
	reg, err := validatorRegistrationFromConfig(config, inputType.DataType, outputType.DataType, typedValidatorHandler[T, R]{types: types, handler: handler})
	if err != nil {
		return ValidatorRegistration{}, err
	}
	return registry.Register(reg)
}

func (r *ValidatorRegistry) Lookup(claim *Claim, validation *Validation) (ValidatorRegistration, bool) {
	if r == nil || validation == nil {
		return ValidatorRegistration{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lookupLocked(claim, validation)
}

func NewProgrammaticValidatorDispatcher(registry *ValidatorRegistry, clock ClaimsClock) *ProgrammaticValidatorDispatcher {
	return newProgrammaticValidatorDispatcher(registry, clock, nil)
}

func newProgrammaticValidatorDispatcher(registry *ValidatorRegistry, clock ClaimsClock, scope ScopeProvider) *ProgrammaticValidatorDispatcher {
	return &ProgrammaticValidatorDispatcher{
		registry: registry,
		limits:   make(map[string]chan struct{}),
		clock:    firstNonNilClock(clock),
		scope:    scope,
	}
}

func (d *ProgrammaticValidatorDispatcher) DispatchValidation(ctx context.Context, req ValidationDispatchRequest) (ValidationDispatchResult, error) {
	if d == nil || d.registry == nil {
		return validationDispatchError(req, ValidationErrorCategoryDispatcher, ErrValidatorNotRegistered.Error(), time.Now().UTC()), ErrValidatorNotRegistered
	}
	reg, ok := d.registry.Lookup(req.Claim, req.Validation)
	if !ok {
		return validationDispatchError(req, ValidationErrorCategoryDispatcher, ErrValidatorNotRegistered.Error(), d.clock.Now()), ErrValidatorNotRegistered
	}
	types := d.registry.typeRegistry()
	if err := validateDispatchInput(types, reg, req); err != nil {
		return validationDispatchError(req, ValidationErrorCategoryArtifactType, err.Error(), d.clock.Now()), nil
	}
	if !d.acquire(reg) {
		return validationDispatchError(req, ValidationErrorCategoryDispatcher, ErrValidatorConcurrencyExhausted.Error(), d.clock.Now()), nil
	}
	defer d.release(reg)
	return d.invoke(ctx, types, reg, req)
}

func (d *ProgrammaticValidatorDispatcher) invoke(ctx context.Context, types *TypeRegistry, reg ValidatorRegistration, req ValidationDispatchRequest) (ValidationDispatchResult, error) {
	started := firstNonZeroTime(req.StartedAt, d.clock.Now())
	if d.scope != nil {
		return d.invokeTracked(ctx, types, reg, req, started)
	}
	return d.invokeDirect(ctx, types, reg, req, started)
}

func (d *ProgrammaticValidatorDispatcher) invokeDirect(ctx context.Context, types *TypeRegistry, reg ValidatorRegistration, req ValidationDispatchRequest, started time.Time) (ValidationDispatchResult, error) {
	runCtx, cancel := context.WithTimeout(ctxOrBackground(ctx), reg.Timeout)
	defer cancel()
	outcome := d.callValidatorHandler(runCtx, reg, req, started)
	return d.resultFromValidatorOutcome(types, reg, req, runCtx, outcome)
}

func (d *ProgrammaticValidatorDispatcher) invokeTracked(ctx context.Context, types *TypeRegistry, reg ValidatorRegistration, req ValidationDispatchRequest, started time.Time) (ValidationDispatchResult, error) {
	runCtx, cancel := context.WithTimeout(ctxOrBackground(ctx), reg.Timeout)
	defer cancel()
	done := make(chan validatorHandlerOutcome, 1)
	if err := d.scope.Go("claims_validator_handler", reg.Timeout, func(scopeCtx context.Context) error {
		outcome := d.callValidatorHandler(runCtx, reg, req, started)
		select {
		case done <- outcome:
		case <-scopeCtx.Done():
		}
		return nil
	}); err != nil {
		return validationDispatchError(req, ValidationErrorCategoryDispatcher, err.Error(), d.clock.Now()), nil
	}
	select {
	case outcome := <-done:
		return d.resultFromValidatorOutcome(types, reg, req, runCtx, outcome)
	case <-runCtx.Done():
		return validationDispatchError(req, ValidationErrorCategoryTimeout, runCtx.Err().Error(), d.clock.Now()), nil
	}
}

type validatorHandlerOutcome struct {
	result    ValidatorHandlerResult
	err       error
	recovered any
	stack     []byte
}

func (d *ProgrammaticValidatorDispatcher) callValidatorHandler(ctx context.Context, reg ValidatorRegistration, req ValidationDispatchRequest, started time.Time) (out validatorHandlerOutcome) {
	defer func() {
		if recovered := recover(); recovered != nil {
			out.recovered = recovered
			out.stack = debug.Stack()
		}
	}()
	out.result, out.err = reg.Handler.ValidateArtifact(ctx, ValidatorHandlerRequest{Artifact: req.Artifact, Validation: req.Validation, StartedAt: started})
	return out
}

func (d *ProgrammaticValidatorDispatcher) resultFromValidatorOutcome(types *TypeRegistry, reg ValidatorRegistration, req ValidationDispatchRequest, runCtx context.Context, outcome validatorHandlerOutcome) (ValidationDispatchResult, error) {
	if outcome.recovered != nil {
		result := validationDispatchError(req, ValidationErrorCategoryPanic, fmt.Sprintf("%v", outcome.recovered), d.clock.Now())
		result.ResultArtifact = validatorErrorArtifact(reg, req, result.Error, outcome.stack)
		return result, nil
	}
	if runCtx.Err() != nil {
		return validationDispatchError(req, ValidationErrorCategoryTimeout, runCtx.Err().Error(), d.clock.Now()), nil
	}
	if outcome.err != nil {
		return validationDispatchError(req, ValidationErrorCategoryHandler, outcome.err.Error(), d.clock.Now()), nil
	}
	result := outcome.result
	if err := validateDispatchResult(types, reg, req, result); err != nil {
		return validationDispatchError(req, ValidationErrorCategoryArtifactType, err.Error(), d.clock.Now()), nil
	}
	return validationDispatchSuccess(req, result, d.clock.Now()), nil
}

func (d *ProgrammaticValidatorDispatcher) acquire(reg ValidatorRegistration) bool {
	limit := d.limitFor(reg)
	select {
	case limit <- struct{}{}:
		return true
	default:
		return false
	}
}

func (d *ProgrammaticValidatorDispatcher) release(reg ValidatorRegistration) {
	<-d.limitFor(reg)
}

func (d *ProgrammaticValidatorDispatcher) limitFor(reg ValidatorRegistration) chan struct{} {
	key := validatorRegistrationKey(reg)
	d.mu.Lock()
	defer d.mu.Unlock()
	limit, ok := d.limits[key]
	if !ok {
		limit = make(chan struct{}, reg.ConcurrencyBudget)
		d.limits[key] = limit
	}
	return limit
}

func (r *ValidatorRegistry) lookupLocked(claim *Claim, validation *Validation) (ValidatorRegistration, bool) {
	candidates := []string{
		validatorRegistrationKeyFor(validation.ValidatorID, validation.Type, claimActionType(claim)),
		validatorRegistrationKeyFor(validation.ValidatorID, validation.Type, ""),
	}
	for _, key := range candidates {
		if reg, ok := r.records[key]; ok {
			return reg, true
		}
	}
	return ValidatorRegistration{}, false
}

func (r *ValidatorRegistry) registrationWithValidatorIDLocked(validatorID string) (ValidatorRegistration, bool) {
	for _, reg := range r.records {
		if reg.ValidatorID == validatorID {
			return reg, true
		}
	}
	return ValidatorRegistration{}, false
}

func (r *ValidatorRegistry) typeRegistry() *TypeRegistry {
	if r == nil || r.types == nil {
		return DefaultTypeRegistry()
	}
	return r.types
}

func normalizeValidatorRegistration(reg ValidatorRegistration, types *TypeRegistry) (ValidatorRegistration, error) {
	reg.ValidatorID = strings.TrimSpace(reg.ValidatorID)
	reg.ValidationType = ValidationType(strings.TrimSpace(string(reg.ValidationType)))
	reg.ActionType = ActionType(strings.TrimSpace(string(reg.ActionType)))
	reg.Determinism = HandlerDeterminism(strings.TrimSpace(string(reg.Determinism)))
	reg.TargetArtifactName = strings.TrimSpace(reg.TargetArtifactName)
	reg.ArtifactDataType = strings.TrimSpace(reg.ArtifactDataType)
	reg.ResultDataType = strings.TrimSpace(reg.ResultDataType)
	return reg, validateValidatorRegistration(reg, types)
}

func validateValidatorRegistration(reg ValidatorRegistration, types *TypeRegistry) error {
	if reg.ValidatorID == "" || reg.ValidationType == "" || !reg.Determinism.valid() || reg.Handler == nil {
		return fmt.Errorf("%w: validator id, validation type, determinism, and handler are required", ErrValidatorRegistrationInvalid)
	}
	if reg.Timeout <= 0 || reg.ConcurrencyBudget <= 0 {
		return fmt.Errorf("%w: timeout and concurrency budget must be bounded positive values", ErrValidatorRegistrationInvalid)
	}
	if reg.TargetArtifactName == "" || reg.ArtifactDataType == "" {
		return fmt.Errorf("%w: artifact target name and datatype are required", ErrValidatorRegistrationInvalid)
	}
	if _, err := types.LookupArtifactType(reg.ArtifactDataType); err != nil {
		return fmt.Errorf("%w: artifact datatype %q is not registered: %v", ErrValidatorRegistrationInvalid, reg.ArtifactDataType, err)
	}
	if reg.ResultDataType != "" {
		if _, err := types.LookupArtifactType(reg.ResultDataType); err != nil {
			return fmt.Errorf("%w: result datatype %q is not registered: %v", ErrValidatorRegistrationInvalid, reg.ResultDataType, err)
		}
	}
	return nil
}

func validatorRegistrationFromConfig(config ValidatorConfig, inputDataType, outputDataType string, handler ValidatorHandler) (ValidatorRegistration, error) {
	reg := ValidatorRegistration{
		ValidatorID:        config.ID,
		ValidationType:     config.ValidationType,
		ActionType:         config.ActionType,
		Determinism:        config.Determinism,
		Timeout:            config.Timeout,
		ConcurrencyBudget:  config.ConcurrencyBudget,
		TargetArtifactName: config.TargetArtifactName,
		ArtifactDataType:   firstNonEmpty(config.ArtifactDataType, inputDataType),
		ResultDataType:     firstNonEmpty(config.ResultDataType, outputDataType),
		Handler:            handler,
	}
	if strings.TrimSpace(config.ArtifactDataType) != "" && strings.TrimSpace(config.ArtifactDataType) != inputDataType {
		return ValidatorRegistration{}, fmt.Errorf("%w: input type %q does not match config artifact datatype %q", ErrValidatorRegistrationInvalid, inputDataType, config.ArtifactDataType)
	}
	if strings.TrimSpace(config.ResultDataType) != "" && strings.TrimSpace(config.ResultDataType) != outputDataType {
		return ValidatorRegistration{}, fmt.Errorf("%w: output type %q does not match config result datatype %q", ErrValidatorRegistrationInvalid, outputDataType, config.ResultDataType)
	}
	return reg, nil
}

type typedValidatorHandler[T, R any] struct {
	types   *TypeRegistry
	handler Validator[T, R]
}

func (h typedValidatorHandler[T, R]) ValidateArtifact(ctx context.Context, req ValidatorHandlerRequest) (ValidatorHandlerResult, error) {
	data, err := ArtifactDataWithRegistry[T](h.types, req.Artifact)
	if err != nil {
		return ValidatorHandlerResult{}, err
	}
	result, err := h.handler(ctx, data)
	if result == nil {
		return ValidatorHandlerResult{}, err
	}
	return ValidatorHandlerResult{ResultArtifact: result}, err
}

func validateDispatchInput(types *TypeRegistry, reg ValidatorRegistration, req ValidationDispatchRequest) error {
	if req.Validation == nil || req.Artifact == nil {
		return fmt.Errorf("validation and artifact are required")
	}
	if err := ValidateTypedValidationDeclaration(req.Validation); err != nil {
		return err
	}
	if req.Validation.ValidatorID != "" && reg.ValidatorID != "" && req.Validation.ValidatorID != reg.ValidatorID {
		return fmt.Errorf("validation validator %q does not match registered validator %q", req.Validation.ValidatorID, reg.ValidatorID)
	}
	if req.Validation.TargetArtifactName != "" && req.Artifact.ArtifactName != req.Validation.TargetArtifactName {
		return fmt.Errorf("artifact %q does not match validation target %q", req.Artifact.ArtifactName, req.Validation.TargetArtifactName)
	}
	if req.Validation.ArtifactDataType != "" && req.Artifact.DataType != req.Validation.ArtifactDataType {
		return fmt.Errorf("artifact datatype %q does not match validation target %q", req.Artifact.DataType, req.Validation.ArtifactDataType)
	}
	if reg.TargetArtifactName != "" && req.Validation.TargetArtifactName != "" && req.Validation.TargetArtifactName != reg.TargetArtifactName {
		return fmt.Errorf("validation target %q does not match registered target %q", req.Validation.TargetArtifactName, reg.TargetArtifactName)
	}
	if reg.ArtifactDataType != "" && req.Validation.ArtifactDataType != "" && req.Validation.ArtifactDataType != reg.ArtifactDataType {
		return fmt.Errorf("validation datatype %q does not match registered datatype %q", req.Validation.ArtifactDataType, reg.ArtifactDataType)
	}
	if reg.TargetArtifactName != "" && req.Artifact.ArtifactName != reg.TargetArtifactName {
		return fmt.Errorf("artifact %q does not match target %q", req.Artifact.ArtifactName, reg.TargetArtifactName)
	}
	if reg.ArtifactDataType != "" && req.Artifact.DataType != reg.ArtifactDataType {
		return fmt.Errorf("artifact datatype %q does not match target %q", req.Artifact.DataType, reg.ArtifactDataType)
	}
	if err := validateDeclaredArtifactDataType(types, req.Validation.ResultDataType, "validation result datatype"); err != nil {
		return err
	}
	if req.Artifact.DataType != "" {
		if len(req.Artifact.Data) == 0 {
			return ErrArtifactDataEmpty
		}
		if err := validateArtifactContentHash(req.Artifact); err != nil {
			return err
		}
	}
	return nil
}

func validateDispatchResult(types *TypeRegistry, reg ValidatorRegistration, req ValidationDispatchRequest, result ValidatorHandlerResult) error {
	declared := firstNonEmpty(req.Validation.ResultDataType, reg.ResultDataType)
	if err := validateDeclaredArtifactDataType(types, declared, "declared result datatype"); err != nil {
		return err
	}
	if declared == "" || result.ResultArtifact == nil {
		return validateOptionalResultArtifact(types, result.ResultArtifact)
	}
	if result.ResultArtifact.DataType != declared {
		return fmt.Errorf("result artifact datatype %q does not match declared %q", result.ResultArtifact.DataType, declared)
	}
	if len(result.ResultArtifact.Data) == 0 {
		return ErrArtifactDataEmpty
	}
	return validateArtifactContentHash(result.ResultArtifact)
}

func validateOptionalResultArtifact(types *TypeRegistry, artifact *Artifact) error {
	if artifact == nil || strings.TrimSpace(artifact.DataType) == "" {
		return nil
	}
	if err := validateDeclaredArtifactDataType(types, artifact.DataType, "result artifact datatype"); err != nil {
		return err
	}
	if len(artifact.Data) == 0 {
		return ErrArtifactDataEmpty
	}
	return validateArtifactContentHash(artifact)
}

func validateDeclaredArtifactDataType(types *TypeRegistry, dataType, label string) error {
	dataType = strings.TrimSpace(dataType)
	if dataType == "" {
		return nil
	}
	if _, err := types.LookupArtifactType(dataType); err != nil {
		return fmt.Errorf("%s %q is not registered: %w", label, dataType, err)
	}
	return nil
}

func validationDispatchSuccess(req ValidationDispatchRequest, result ValidatorHandlerResult, completedAt time.Time) ValidationDispatchResult {
	status := ValidationStatusValidated
	if result.Error != nil {
		status = validationStatusForValidationError(req.Validation, result.Error)
	}
	return ValidationDispatchResult{
		ValidationID:   validationIDFromRequest(req),
		Status:         status,
		ResultArtifact: result.ResultArtifact,
		Error:          result.Error,
		CompletedAt:    completedAt,
	}
}

func validationDispatchError(req ValidationDispatchRequest, category ValidationErrorCategory, description string, completedAt time.Time) ValidationDispatchResult {
	err := &ValidationError{
		Category:    category,
		Description: firstNonEmpty(description, string(category)),
		Source:      validationErrorSource(req),
		OccurredAt:  completedAt,
	}
	return ValidationDispatchResult{
		ValidationID: validationIDFromRequest(req),
		Status:       validationStatusForValidationError(req.Validation, err),
		Error:        err,
		CompletedAt:  completedAt,
	}
}

func validationStatusForValidationError(validation *Validation, err *ValidationError) ValidationStatus {
	category := ValidationErrorCategoryInternal
	if err != nil {
		category = err.Category
	}
	switch category {
	case ValidationErrorCategoryHandler, ValidationErrorCategoryTimeout:
		if validation != nil && !validation.Required {
			return ValidationStatusValidationFailedNotRequired
		}
		return ValidationStatusValidationFailed
	case ValidationErrorCategoryQualityBar:
		if validation != nil && !validation.Required {
			return ValidationStatusQualityBarValidationFailedNotRequired
		}
		return ValidationStatusQualityBarValidationFailed
	default:
		if validation != nil && !validation.Required {
			return ValidationStatusErroredNotRequired
		}
		return ValidationStatusErrored
	}
}

func validatorErrorArtifact(reg ValidatorRegistration, req ValidationDispatchRequest, err *ValidationError, stack []byte) *Artifact {
	metadata := map[string]any{"validator_id": reg.ValidatorID, "validation_id": validationIDFromRequest(req)}
	if len(stack) != 0 {
		metadata["stack"] = string(stack)
	}
	return &Artifact{
		AgentID:      firstNonEmpty(reg.ValidatorID, "programmatic_validator"),
		Kind:         ArtifactKindErrorDiagnostic,
		ArtifactName: "validator_error",
		Reference:    firstNonEmpty(validationErrorDescription(err), "validator failed"),
		Metadata:     metadata,
	}
}

func validationErrorSource(req ValidationDispatchRequest) ParticipantRef {
	if req.Validation != nil && req.Validation.EvaluatorRef != nil {
		return *req.Validation.EvaluatorRef
	}
	return DegradedAgentRef(firstNonEmpty(validationAgentID(req.Validation), "programmatic_validator"), "programmatic validator")
}

func validationIDFromRequest(req ValidationDispatchRequest) string {
	if req.Validation == nil {
		return ""
	}
	return req.Validation.ID
}

func validationAgentID(validation *Validation) string {
	if validation == nil {
		return ""
	}
	return firstNonEmpty(validation.ValidatorID, validation.AgentID, validation.ParticipantID)
}

func validationErrorDescription(err *ValidationError) string {
	if err == nil {
		return ""
	}
	return err.Description
}

func validatorRegistrationKey(reg ValidatorRegistration) string {
	return validatorRegistrationKeyFor(reg.ValidatorID, reg.ValidationType, reg.ActionType)
}

func validatorRegistrationKeyFor(validatorID string, validationType ValidationType, action ActionType) string {
	return strings.Join([]string{strings.TrimSpace(validatorID), string(validationType), string(action)}, "\x00")
}

func claimActionType(claim *Claim) ActionType {
	if claim == nil {
		return ""
	}
	return claim.ActionType
}

func ctxOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

type BoardValidatorDispatcherConfig struct {
	Board                *ClaimsBoard
	Registry             *ValidatorRegistry
	Scope                ScopeProvider
	Clock                ClaimsClock
	Policy               ExpectedToolPolicy
	Redactor             ExpectedToolArgumentRedactor
	MaxInputBytes        int64
	MaxOutputBytes       int64
	ApprovedValidatorIDs map[string]bool
}

type BoardValidatorDispatcher struct {
	board                *ClaimsBoard
	registry             *ValidatorRegistry
	programmatic         *ProgrammaticValidatorDispatcher
	clock                ClaimsClock
	policy               ExpectedToolPolicy
	redactor             ExpectedToolArgumentRedactor
	maxInputBytes        int64
	maxOutputBytes       int64
	approvedValidatorIDs map[string]bool
	mu                   sync.Mutex
	inFlight             map[string]struct{}
}

func NewBoardValidatorDispatcher(cfg BoardValidatorDispatcherConfig) (*BoardValidatorDispatcher, error) {
	if cfg.Board == nil || cfg.Registry == nil {
		return nil, fmt.Errorf("%w: board and registry are required", ErrValidatorDispatchInvalid)
	}
	if cfg.MaxInputBytes <= 0 || cfg.MaxOutputBytes <= 0 {
		return nil, fmt.Errorf("%w: input and output byte limits must be bounded positive values", ErrValidatorDispatchInvalid)
	}
	clock := firstNonNilClock(cfg.Clock)
	scope := cfg.Scope
	if scope == nil && cfg.Board != nil {
		scope = cfg.Board.scope
	}
	return &BoardValidatorDispatcher{
		board:                cfg.Board,
		registry:             cfg.Registry,
		programmatic:         newProgrammaticValidatorDispatcher(cfg.Registry, clock, scope),
		clock:                clock,
		policy:               cfg.Policy,
		redactor:             cfg.Redactor,
		maxInputBytes:        cfg.MaxInputBytes,
		maxOutputBytes:       cfg.MaxOutputBytes,
		approvedValidatorIDs: copyBoolMap(cfg.ApprovedValidatorIDs),
		inFlight:             make(map[string]struct{}),
	}, nil
}

func (d *BoardValidatorDispatcher) DispatchValidationByID(ctx context.Context, claimID, validationID string) (ValidationDispatchResult, error) {
	req, err := d.requestForValidation(claimID, validationID)
	if err == nil {
		return d.DispatchValidation(ctx, req)
	}
	if req.Claim != nil && req.Validation != nil {
		return d.dispatchMissingTarget(ctxOrBackground(ctx), req, err.Error())
	}
	fallback := ValidationDispatchRequest{Validation: &Validation{ID: validationID, Required: true}}
	return validationDispatchError(fallback, ValidationErrorCategoryDispatcher, err.Error(), d.now()), err
}

func (d *BoardValidatorDispatcher) DispatchValidation(ctx context.Context, req ValidationDispatchRequest) (ValidationDispatchResult, error) {
	if err := d.validateRequest(req); err != nil {
		return validationDispatchError(req, ValidationErrorCategoryDispatcher, err.Error(), d.now()), err
	}
	if !d.acquire(req.Validation.ID) {
		result := validationDispatchError(req, ValidationErrorCategoryDispatcher, ErrValidatorConcurrencyExhausted.Error(), d.now())
		return result, ErrValidatorConcurrencyExhausted
	}
	defer d.release(req.Validation.ID)
	return d.dispatchAcquiredValidation(ctxOrBackground(ctx), req)
}

func (d *BoardValidatorDispatcher) dispatchAcquiredValidation(ctx context.Context, req ValidationDispatchRequest) (ValidationDispatchResult, error) {
	reg, ok := d.registry.Lookup(req.Claim, req.Validation)
	if !ok {
		if err := d.board.BeginValidation(ctx, req.Claim.ID, req.Validation.ID, validationAgentID(req.Validation), req.Artifact.ID); err != nil {
			return validationDispatchError(req, ValidationErrorCategoryDispatcher, err.Error(), d.now()), err
		}
		return d.commitDispatchError(ctx, req, ValidationErrorCategoryDispatcher, ErrValidatorNotRegistered.Error())
	}
	if err := d.board.BeginValidation(ctx, req.Claim.ID, req.Validation.ID, reg.ValidatorID, req.Artifact.ID); err != nil {
		return validationDispatchError(req, ValidationErrorCategoryDispatcher, err.Error(), d.now()), err
	}
	if err := d.validateBoundedInput(req.Artifact); err != nil {
		return d.commitDispatchError(ctx, req, ValidationErrorCategoryDispatcher, err.Error())
	}
	if result, denied := d.enforcePolicy(ctx, reg, req); denied {
		return d.commitDispatchResult(ctx, req, reg, result)
	}
	handlerCtx, cancel := d.handlerContext(ctx, req.Validation)
	defer cancel()
	result, err := d.programmatic.DispatchValidation(handlerCtx, req)
	if err != nil {
		result = validationDispatchError(req, ValidationErrorCategoryDispatcher, err.Error(), d.now())
	}
	result = d.enforceOutputLimit(reg, req, result)
	return d.commitDispatchResult(ctx, req, reg, result)
}

func (d *BoardValidatorDispatcher) dispatchMissingTarget(ctx context.Context, req ValidationDispatchRequest, reason string) (ValidationDispatchResult, error) {
	if !d.acquire(req.Validation.ID) {
		return validationDispatchError(req, ValidationErrorCategoryDispatcher, ErrValidatorConcurrencyExhausted.Error(), d.now()), ErrValidatorConcurrencyExhausted
	}
	defer d.release(req.Validation.ID)
	_ = d.board.BeginValidation(ctx, req.Claim.ID, req.Validation.ID, validationAgentID(req.Validation), "")
	return d.commitDispatchError(ctx, req, ValidationErrorCategoryDispatcher, reason)
}

func (d *BoardValidatorDispatcher) requestForValidation(claimID, validationID string) (ValidationDispatchRequest, error) {
	validation, claim, ok := d.board.CloneValidation(strings.TrimSpace(validationID))
	if !ok || claim.ID != strings.TrimSpace(claimID) {
		return ValidationDispatchRequest{}, fmt.Errorf("validation %q not found on claim %q", validationID, claimID)
	}
	artifact, ok := d.board.cloneValidationTargetArtifact(claim.ID, validation)
	if !ok {
		return ValidationDispatchRequest{Claim: claim, Validation: validation}, fmt.Errorf("target artifact %q not found", validation.TargetArtifactName)
	}
	return ValidationDispatchRequest{Claim: claim, Validation: validation, Artifact: artifact, StartedAt: d.now()}, nil
}

func (d *BoardValidatorDispatcher) validateRequest(req ValidationDispatchRequest) error {
	if d == nil || d.board == nil || d.registry == nil || d.programmatic == nil {
		return fmt.Errorf("%w: dispatcher is not initialized", ErrValidatorDispatchInvalid)
	}
	if req.Claim == nil || req.Validation == nil || req.Artifact == nil {
		return fmt.Errorf("%w: claim, validation, and artifact are required", ErrValidatorDispatchInvalid)
	}
	if req.Validation.Status != ValidationStatusReady {
		return fmt.Errorf("%w: validation %q is %q, want %q", ErrValidatorDispatchInvalid, req.Validation.ID, req.Validation.Status, ValidationStatusReady)
	}
	return nil
}

func (d *BoardValidatorDispatcher) validateBoundedInput(artifact *Artifact) error {
	if artifact == nil {
		return fmt.Errorf("artifact is required")
	}
	if int64(len(artifact.Data)) > d.maxInputBytes {
		return fmt.Errorf("artifact data exceeds validator input limit")
	}
	return nil
}

func (d *BoardValidatorDispatcher) enforcePolicy(ctx context.Context, reg ValidatorRegistration, req ValidationDispatchRequest) (ValidationDispatchResult, bool) {
	if reg.Determinism != HandlerDeterminismSideEffect && reg.Determinism != HandlerDeterminismNondeterministic {
		return ValidationDispatchResult{}, false
	}
	payload := d.validatorPolicyPayload(ctx, reg, req)
	if !d.approvedValidatorIDs[reg.ValidatorID] {
		return d.policyDenied(req, "validator requires explicit approval", payload), true
	}
	if d.policy == nil {
		return d.policyDenied(req, "validator policy is required", payload), true
	}
	decision := d.policy.DecideExpectedTool(ctx, ExpectedToolPolicyRequest{Claim: req.Claim, Validation: req.Validation, Call: validatorPolicyCall(reg, req), AgentID: reg.ValidatorID})
	if decision.Allowed {
		return ValidationDispatchResult{}, false
	}
	return d.policyDenied(req, firstNonEmpty(decision.Reason, "validator policy denied execution"), payload), true
}

func (d *BoardValidatorDispatcher) validatorPolicyPayload(ctx context.Context, reg ValidatorRegistration, req ValidationDispatchRequest) map[string]any {
	args := map[string]any{
		"validator_id": reg.ValidatorID,
		"artifact_id":  req.Artifact.ID,
		"data_type":    req.Artifact.DataType,
		"content_hash": req.Artifact.ContentHash,
	}
	if d.redactor == nil {
		return args
	}
	redacted, err := d.redactor.RedactExpectedToolArguments(ctx, reg.ValidatorID, args)
	if err != nil {
		return map[string]any{"redaction_error": err.Error()}
	}
	return redacted
}

func (d *BoardValidatorDispatcher) policyDenied(req ValidationDispatchRequest, reason string, payload map[string]any) ValidationDispatchResult {
	err := &ValidationError{
		Category:    ValidationErrorCategoryDispatcher,
		Description: reason,
		Payload:     payload,
		Source:      validationErrorSource(req),
		OccurredAt:  d.now(),
	}
	return ValidationDispatchResult{
		ValidationID:     validationIDFromRequest(req),
		Status:           validationStatusForValidationError(req.Validation, err),
		ResultArtifact:   validatorErrorArtifact(ValidatorRegistration{ValidatorID: validationAgentID(req.Validation)}, req, err, nil),
		Error:            err,
		CompletedAt:      err.OccurredAt,
		ShortCircuitRest: req.Validation.Required,
	}
}

func (d *BoardValidatorDispatcher) enforceOutputLimit(reg ValidatorRegistration, req ValidationDispatchRequest, result ValidationDispatchResult) ValidationDispatchResult {
	if result.ResultArtifact == nil || artifactSize(result.ResultArtifact) <= d.maxOutputBytes {
		return result
	}
	err := &ValidationError{
		Category:    ValidationErrorCategoryHandler,
		Description: "validator result artifact exceeds output limit",
		Source:      validationErrorSource(req),
		OccurredAt:  d.now(),
	}
	result.Status = validationStatusForValidationError(req.Validation, err)
	result.Error = err
	result.ResultArtifact = validatorErrorArtifact(reg, req, err, nil)
	result.CompletedAt = err.OccurredAt
	return result
}

func (d *BoardValidatorDispatcher) commitDispatchError(ctx context.Context, req ValidationDispatchRequest, category ValidationErrorCategory, description string) (ValidationDispatchResult, error) {
	return d.commitDispatchResult(ctx, req, ValidatorRegistration{ValidatorID: validationAgentID(req.Validation)}, validationDispatchError(req, category, description, d.now()))
}

func (d *BoardValidatorDispatcher) commitDispatchResult(ctx context.Context, req ValidationDispatchRequest, reg ValidatorRegistration, result ValidationDispatchResult) (ValidationDispatchResult, error) {
	if result.Error != nil && result.ResultArtifact == nil {
		result.ResultArtifact = validatorErrorArtifact(reg, req, result.Error, nil)
	}
	err := d.board.CompleteValidationLifecycle(ctx, req.Claim.ID, req.Validation.ID, firstNonEmpty(reg.ValidatorID, validationAgentID(req.Validation)), result.Status, ValidationLifecycleOptions{
		Reason:           validationResultReason(result),
		TargetArtifactID: dispatchArtifactID(req.Artifact),
		ResultArtifact:   result.ResultArtifact,
		Error:            result.Error,
	})
	return result, err
}

func (d *BoardValidatorDispatcher) handlerContext(ctx context.Context, validation *Validation) (context.Context, context.CancelFunc) {
	if validation == nil || validation.Timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, validation.Timeout)
}

func (d *BoardValidatorDispatcher) acquire(validationID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, exists := d.inFlight[validationID]; exists {
		return false
	}
	d.inFlight[validationID] = struct{}{}
	return true
}

func (d *BoardValidatorDispatcher) release(validationID string) {
	d.mu.Lock()
	delete(d.inFlight, validationID)
	d.mu.Unlock()
}

func (d *BoardValidatorDispatcher) now() time.Time {
	if d == nil || d.clock == nil {
		return time.Now().UTC()
	}
	return d.clock.Now()
}

func validatorPolicyCall(reg ValidatorRegistration, req ValidationDispatchRequest) ExpectedToolCall {
	return ExpectedToolCall{
		ID:       strings.TrimSpace(req.Validation.ID) + ".validator_policy",
		Tool:     strings.TrimSpace(reg.ValidatorID),
		Purpose:  "validator side-effect policy",
		Required: req.Validation.Required,
		Policy:   ExpectedToolCallPolicy{RequiresUserApproval: true},
	}
}

func validationResultReason(result ValidationDispatchResult) string {
	if result.Error != nil {
		return result.Error.Description
	}
	if result.Status == ValidationStatusValidated {
		return "validation passed"
	}
	return string(result.Status)
}

func dispatchArtifactID(artifact *Artifact) string {
	if artifact == nil {
		return ""
	}
	return artifact.ID
}

func copyBoolMap(in map[string]bool) map[string]bool {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]bool, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func firstNonNilClock(clock ClaimsClock) ClaimsClock {
	if clock == nil {
		return SystemClock{}
	}
	return clock
}
