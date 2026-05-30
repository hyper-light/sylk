package claims

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sort"
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
	workers  sync.WaitGroup
}

type ValidatorDispatcherStats struct {
	Registered int                      `json:"registered"`
	Validators []ValidatorQueueSnapshot `json:"validators,omitempty"`
}

type ValidatorQueueSnapshot struct {
	ValidatorID       string             `json:"validator_id,omitempty"`
	ValidationType    ValidationType     `json:"validation_type,omitempty"`
	ActionType        ActionType         `json:"action_type,omitempty"`
	Determinism       HandlerDeterminism `json:"determinism,omitempty"`
	Inflight          int                `json:"inflight"`
	ConcurrencyBudget int                `json:"concurrency_budget"`
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
	key := validatorRegistrationKey(normalized)
	existing, ok := r.records[key]
	if !ok {
		r.records[key] = normalized
		return normalized, nil
	}
	if !validatorImmutableFieldsEqual(existing, normalized) {
		return existing, ErrValidatorRegistrationConflict
	}
	return existing, nil
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

func (r *ValidatorRegistry) List() []ValidatorRegistration {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	out := make([]ValidatorRegistration, 0, len(r.records))
	for _, reg := range r.records {
		out = append(out, reg)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		return validatorRegistrationKey(out[i]) < validatorRegistrationKey(out[j])
	})
	return out
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

func (d *ProgrammaticValidatorDispatcher) Wait() {
	if d == nil {
		return
	}
	d.workers.Wait()
}

func (d *ProgrammaticValidatorDispatcher) Stats() ValidatorDispatcherStats {
	if d == nil || d.registry == nil {
		return ValidatorDispatcherStats{}
	}
	regs := d.registry.List()
	out := ValidatorDispatcherStats{Registered: len(regs)}
	for _, reg := range regs {
		limit := d.limitFor(reg)
		out.Validators = append(out.Validators, ValidatorQueueSnapshot{
			ValidatorID:       reg.ValidatorID,
			ValidationType:    reg.ValidationType,
			ActionType:        reg.ActionType,
			Determinism:       reg.Determinism,
			Inflight:          len(limit),
			ConcurrencyBudget: cap(limit),
		})
	}
	return out
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
	return d.invoke(ctx, types, reg, req, func() { d.release(reg) })
}

func (d *ProgrammaticValidatorDispatcher) invoke(ctx context.Context, types *TypeRegistry, reg ValidatorRegistration, req ValidationDispatchRequest, release func()) (ValidationDispatchResult, error) {
	started := firstNonZeroTime(req.StartedAt, d.clock.Now())
	if d.scope != nil {
		return d.invokeTracked(ctx, types, reg, req, started, release)
	}
	return d.invokeDirect(ctx, types, reg, req, started, release)
}

func (d *ProgrammaticValidatorDispatcher) invokeDirect(ctx context.Context, types *TypeRegistry, reg ValidatorRegistration, req ValidationDispatchRequest, started time.Time, release func()) (ValidationDispatchResult, error) {
	runCtx, cancel := context.WithTimeout(ctxOrBackground(ctx), reg.Timeout)
	defer cancel()
	done := make(chan validatorHandlerOutcome, 1)
	d.workers.Add(1)
	go func() {
		defer d.workers.Done()
		defer release()
		done <- d.callValidatorHandler(runCtx, reg, req, started)
	}()
	select {
	case outcome := <-done:
		return d.resultFromValidatorOutcome(types, reg, req, runCtx, outcome)
	case <-runCtx.Done():
		return validationDispatchError(req, ValidationErrorCategoryTimeout, runCtx.Err().Error(), d.clock.Now()), nil
	}
}

func (d *ProgrammaticValidatorDispatcher) invokeTracked(ctx context.Context, types *TypeRegistry, reg ValidatorRegistration, req ValidationDispatchRequest, started time.Time, release func()) (ValidationDispatchResult, error) {
	runCtx, cancel := context.WithTimeout(ctxOrBackground(ctx), reg.Timeout)
	defer cancel()
	done := make(chan validatorHandlerOutcome, 1)
	if err := d.scope.Go("claims_validator_handler", reg.Timeout, func(scopeCtx context.Context) error {
		defer release()
		outcome := d.callValidatorHandler(runCtx, reg, req, started)
		select {
		case done <- outcome:
		case <-scopeCtx.Done():
		}
		return nil
	}); err != nil {
		release()
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
		validatorRegistrationKeyFor("", validation.Type, claimActionType(claim)),
		validatorRegistrationKeyFor("", validation.Type, ""),
	}
	for _, key := range candidates {
		if reg, ok := r.records[key]; ok {
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
	if reg.ValidationType == "" || !reg.Determinism.valid() || reg.Handler == nil {
		return fmt.Errorf("%w: validation type, determinism, and handler are required", ErrValidatorRegistrationInvalid)
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

func validatorImmutableFieldsEqual(a, b ValidatorRegistration) bool {
	return a.ValidatorID == b.ValidatorID &&
		a.ValidationType == b.ValidationType &&
		a.ActionType == b.ActionType &&
		a.Determinism == b.Determinism &&
		a.Timeout == b.Timeout &&
		a.ConcurrencyBudget == b.ConcurrencyBudget &&
		a.TargetArtifactName == b.TargetArtifactName &&
		a.ArtifactDataType == b.ArtifactDataType &&
		a.ResultDataType == b.ResultDataType
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
	case ValidationErrorCategoryArtifactType:
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
	CancelRegistry       *ClaimCancelRegistry
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
	cancelRegistry       *ClaimCancelRegistry
	mu                   sync.Mutex
	inFlight             map[string]struct{}
	active               map[string]context.CancelFunc
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
		cancelRegistry:       firstNonNilClaimCancelRegistry(cfg.CancelRegistry),
		inFlight:             make(map[string]struct{}),
		active:               make(map[string]context.CancelFunc),
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

func (d *BoardValidatorDispatcher) RecoverValidationByID(ctx context.Context, claimID, validationID string) (ValidationDispatchResult, error) {
	req, err := d.recoveryRequestForValidation(claimID, validationID)
	if err != nil {
		return d.recoverInvalidValidationRequest(ctxOrBackground(ctx), req, validationID, err)
	}
	if req.Validation.Status == ValidationStatusReady {
		return d.DispatchValidation(ctx, req)
	}
	if !d.acquire(req.Validation.ID) {
		result := validationDispatchError(req, ValidationErrorCategoryDispatcher, ErrValidatorConcurrencyExhausted.Error(), d.now())
		return result, ErrValidatorConcurrencyExhausted
	}
	defer d.release(req.Validation.ID)
	return d.recoverAcquiredValidation(ctxOrBackground(ctx), req)
}

func (d *BoardValidatorDispatcher) CancelValidation(validationID string) bool {
	if d == nil {
		return false
	}
	d.mu.Lock()
	cancel := d.active[strings.TrimSpace(validationID)]
	delete(d.active, strings.TrimSpace(validationID))
	d.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (d *BoardValidatorDispatcher) CancelClaimValidations(claim *Claim) int {
	if d == nil || claim == nil {
		return 0
	}
	cancelled := 0
	for _, validation := range claim.Validations {
		if validation != nil && d.CancelValidation(validation.ID) {
			cancelled++
		}
	}
	cancelled += d.cancelRegistry.CancelClaim(claim.ID)
	return cancelled
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
	handlerCtx, claimReg := d.cancelRegistry.Context(ctx, req.Claim.ID)
	defer claimReg.Done()
	handlerCtx, cancel := d.handlerContext(handlerCtx, req.Validation)
	d.trackActive(req.Validation.ID, cancel)
	defer d.untrackActive(req.Validation.ID)
	result, err := d.programmatic.DispatchValidation(handlerCtx, req)
	if err != nil {
		result = validationDispatchError(req, ValidationErrorCategoryDispatcher, err.Error(), d.now())
	}
	result = d.enforceOutputLimit(reg, req, result)
	return d.commitDispatchResult(ctx, req, reg, result)
}

func (d *BoardValidatorDispatcher) recoverAcquiredValidation(ctx context.Context, req ValidationDispatchRequest) (ValidationDispatchResult, error) {
	reg, ok := d.registry.Lookup(req.Claim, req.Validation)
	if !ok {
		return d.commitRecoveryDispatchError(ctx, req, ValidatorRegistration{ValidatorID: validationAgentID(req.Validation)}, ValidationErrorCategoryDispatcher, ErrValidatorNotRegistered.Error())
	}
	if !validationRecoveryMayRedispatch(reg, req) {
		return d.commitRecoveryDispatchError(ctx, req, reg, ValidationErrorCategoryDispatcher, "validator recovery requires pure/content determinism or idempotency evidence")
	}
	if err := validateDispatchInput(d.registry.typeRegistry(), reg, req); err != nil {
		return d.commitRecoveryDispatchError(ctx, req, reg, ValidationErrorCategoryArtifactType, err.Error())
	}
	if err := d.validateBoundedInput(req.Artifact); err != nil {
		return d.commitRecoveryDispatchError(ctx, req, reg, ValidationErrorCategoryDispatcher, err.Error())
	}
	handlerCtx, claimReg := d.cancelRegistry.Context(ctx, req.Claim.ID)
	defer claimReg.Done()
	handlerCtx, cancel := d.handlerContext(handlerCtx, req.Validation)
	d.trackActive(req.Validation.ID, cancel)
	defer d.untrackActive(req.Validation.ID)
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

func (d *BoardValidatorDispatcher) recoverInvalidValidationRequest(ctx context.Context, req ValidationDispatchRequest, validationID string, cause error) (ValidationDispatchResult, error) {
	if req.Claim != nil && req.Validation != nil {
		return d.commitRecoveryDispatchError(ctx, req, ValidatorRegistration{ValidatorID: validationAgentID(req.Validation)}, ValidationErrorCategoryDispatcher, cause.Error())
	}
	fallback := ValidationDispatchRequest{Validation: &Validation{ID: validationID, Required: true}}
	return validationDispatchError(fallback, ValidationErrorCategoryDispatcher, cause.Error(), d.now()), cause
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

func (d *BoardValidatorDispatcher) recoveryRequestForValidation(claimID, validationID string) (ValidationDispatchRequest, error) {
	validation, claim, ok := d.board.CloneValidation(strings.TrimSpace(validationID))
	if !ok || claim.ID != strings.TrimSpace(claimID) {
		return ValidationDispatchRequest{}, fmt.Errorf("validation %q not found on claim %q", validationID, claimID)
	}
	req := ValidationDispatchRequest{Claim: claim, Validation: validation, StartedAt: d.now()}
	artifact, ok := d.board.cloneValidationTargetArtifact(claim.ID, validation)
	if !ok {
		return req, fmt.Errorf("target artifact %q not found", validation.TargetArtifactName)
	}
	req.Artifact = artifact
	return req, validateRecoveryValidationStatus(validation)
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
	if err := d.validateBoardParentage(req); err != nil {
		return err
	}
	return nil
}

func validateRecoveryValidationStatus(validation *Validation) error {
	if validation == nil {
		return fmt.Errorf("%w: validation is required", ErrValidatorDispatchInvalid)
	}
	switch validation.Status {
	case ValidationStatusReady, ValidationStatusValidating, ValidationStatusValidatingQualityBar:
		return nil
	default:
		return fmt.Errorf("%w: validation %q is %q, want recoverable state", ErrValidatorDispatchInvalid, validation.ID, validation.Status)
	}
}

func (d *BoardValidatorDispatcher) validateBoardParentage(req ValidationDispatchRequest) error {
	storedValidation, storedClaim, ok := d.board.CloneValidation(req.Validation.ID)
	if !ok || storedClaim == nil || storedClaim.ID != req.Claim.ID {
		return fmt.Errorf("%w: validation %q is not parented to claim %q", ErrValidatorDispatchInvalid, req.Validation.ID, req.Claim.ID)
	}
	if storedValidation.Status != ValidationStatusReady {
		return fmt.Errorf("%w: stored validation %q is %q, want %q", ErrValidatorDispatchInvalid, storedValidation.ID, storedValidation.Status, ValidationStatusReady)
	}
	storedArtifact, ok := d.board.cloneValidationTargetArtifact(storedClaim.ID, storedValidation)
	if !ok || storedArtifact.ID != req.Artifact.ID {
		return fmt.Errorf("%w: artifact %q is not the board target for validation %q", ErrValidatorDispatchInvalid, req.Artifact.ID, req.Validation.ID)
	}
	if storedArtifact.TestamentID == "" || !artifactReadyForValidationDispatch(storedArtifact.Status) {
		return fmt.Errorf("%w: artifact %q is not attached and ready for validation", ErrValidatorDispatchInvalid, storedArtifact.ID)
	}
	return nil
}

func artifactReadyForValidationDispatch(status ArtifactStatus) bool {
	return status == ArtifactStatusAttached || status == ArtifactStatusValidating
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

func (d *BoardValidatorDispatcher) commitRecoveryDispatchError(ctx context.Context, req ValidationDispatchRequest, reg ValidatorRegistration, category ValidationErrorCategory, description string) (ValidationDispatchResult, error) {
	if req.Validation != nil && req.Validation.Status == ValidationStatusReady {
		_ = d.board.BeginValidation(ctx, req.Claim.ID, req.Validation.ID, firstNonEmpty(reg.ValidatorID, validationAgentID(req.Validation)), dispatchArtifactID(req.Artifact))
	}
	result := validationDispatchError(req, category, description, d.now())
	result.Status = validationRecoveryFailureStatus(req.Validation, result.Status)
	return d.commitDispatchResult(ctx, req, reg, result)
}

func (d *BoardValidatorDispatcher) commitDispatchResult(ctx context.Context, req ValidationDispatchRequest, reg ValidatorRegistration, result ValidationDispatchResult) (ValidationDispatchResult, error) {
	if result.Error != nil && result.ResultArtifact == nil {
		result.ResultArtifact = validatorErrorArtifact(reg, req, result.Error, nil)
	}
	result.Status = deterministicValidationCommitStatus(req.Validation, result.Status)
	err := d.board.CompleteValidationLifecycle(ctx, req.Claim.ID, req.Validation.ID, firstNonEmpty(reg.ValidatorID, validationAgentID(req.Validation)), result.Status, ValidationLifecycleOptions{
		Reason:           validationResultReason(result),
		TargetArtifactID: dispatchArtifactID(req.Artifact),
		ResultArtifact:   result.ResultArtifact,
		Error:            result.Error,
	})
	return d.withCommittedResultArtifact(req, result), err
}

func (d *BoardValidatorDispatcher) withCommittedResultArtifact(req ValidationDispatchRequest, result ValidationDispatchResult) ValidationDispatchResult {
	if d == nil || d.board == nil || req.Validation == nil {
		return result
	}
	validation, _, ok := d.board.CloneValidation(req.Validation.ID)
	if !ok || validation == nil {
		return result
	}
	result.ResultArtifactID = firstNonEmpty(validation.ResultArtifactID, result.ResultArtifactID)
	if result.ResultArtifactID == "" {
		return result
	}
	if artifact, ok := d.board.CloneArtifact(result.ResultArtifactID); ok {
		result.ResultArtifact = artifact
	}
	return result
}

func deterministicValidationCommitStatus(validation *Validation, status ValidationStatus) ValidationStatus {
	if status == ValidationStatusValidated && validationHasQualityBar(validation) {
		return ValidationStatusValidatingQualityBar
	}
	return status
}

func validationHasQualityBar(validation *Validation) bool {
	return validation != nil && strings.TrimSpace(validation.QualityBar) != ""
}

func (d *BoardValidatorDispatcher) handlerContext(ctx context.Context, validation *Validation) (context.Context, context.CancelFunc) {
	if validation == nil || validation.Timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, validation.Timeout)
}

func (d *BoardValidatorDispatcher) trackActive(validationID string, cancel context.CancelFunc) {
	if d == nil || cancel == nil {
		return
	}
	d.mu.Lock()
	d.active[strings.TrimSpace(validationID)] = cancel
	d.mu.Unlock()
}

func (d *BoardValidatorDispatcher) untrackActive(validationID string) {
	if d == nil {
		return
	}
	d.mu.Lock()
	cancel := d.active[strings.TrimSpace(validationID)]
	delete(d.active, strings.TrimSpace(validationID))
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
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

func validationRecoveryFailureStatus(validation *Validation, fallback ValidationStatus) ValidationStatus {
	if validation == nil || validation.Status != ValidationStatusValidatingQualityBar {
		return fallback
	}
	if validation.Required {
		return ValidationStatusQualityBarValidationFailed
	}
	return ValidationStatusQualityBarValidationFailedNotRequired
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

func validationRecoveryMayRedispatch(reg ValidatorRegistration, req ValidationDispatchRequest) bool {
	if reg.Determinism == HandlerDeterminismPure || reg.Determinism == HandlerDeterminismContent {
		return true
	}
	if reg.Determinism != HandlerDeterminismSideEffect && reg.Determinism != HandlerDeterminismNondeterministic {
		return false
	}
	return artifactProvesRecoveryIdempotency(req.Artifact, ParticipantRegistration{UID: reg.ValidatorID, RouteKey: reg.ValidatorID})
}
