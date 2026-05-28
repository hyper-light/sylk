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

type ValidatorRegistry struct {
	mu      sync.RWMutex
	records map[string]ValidatorRegistration
}

type ProgrammaticValidatorDispatcher struct {
	registry *ValidatorRegistry
	mu       sync.Mutex
	limits   map[string]chan struct{}
	clock    ClaimsClock
}

func NewValidatorRegistry() *ValidatorRegistry {
	return &ValidatorRegistry{records: make(map[string]ValidatorRegistration)}
}

func (r *ValidatorRegistry) Register(reg ValidatorRegistration) (ValidatorRegistration, error) {
	if r == nil {
		return ValidatorRegistration{}, fmt.Errorf("%w: registry is nil", ErrValidatorRegistrationInvalid)
	}
	normalized, err := normalizeValidatorRegistration(reg)
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

func (r *ValidatorRegistry) Lookup(claim *Claim, validation *Validation) (ValidatorRegistration, bool) {
	if r == nil || validation == nil {
		return ValidatorRegistration{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lookupLocked(claim, validation)
}

func NewProgrammaticValidatorDispatcher(registry *ValidatorRegistry, clock ClaimsClock) *ProgrammaticValidatorDispatcher {
	return &ProgrammaticValidatorDispatcher{
		registry: registry,
		limits:   make(map[string]chan struct{}),
		clock:    firstNonNilClock(clock),
	}
}

func (d *ProgrammaticValidatorDispatcher) DispatchValidation(ctx context.Context, req ValidationDispatchRequest) (ValidationDispatchResult, error) {
	reg, ok := d.registry.Lookup(req.Claim, req.Validation)
	if !ok {
		return validationDispatchError(req, ValidationErrorCategoryDispatcher, ErrValidatorNotRegistered.Error(), d.clock.Now()), ErrValidatorNotRegistered
	}
	if err := validateDispatchInput(reg, req); err != nil {
		return validationDispatchError(req, ValidationErrorCategoryArtifactType, err.Error(), d.clock.Now()), nil
	}
	if !d.acquire(reg) {
		return validationDispatchError(req, ValidationErrorCategoryDispatcher, ErrValidatorConcurrencyExhausted.Error(), d.clock.Now()), nil
	}
	defer d.release(reg)
	return d.invoke(ctx, reg, req)
}

func (d *ProgrammaticValidatorDispatcher) invoke(ctx context.Context, reg ValidatorRegistration, req ValidationDispatchRequest) (out ValidationDispatchResult, err error) {
	started := firstNonZeroTime(req.StartedAt, d.clock.Now())
	runCtx, cancel := context.WithTimeout(ctxOrBackground(ctx), reg.Timeout)
	defer cancel()
	defer func() {
		if recovered := recover(); recovered != nil {
			out = validationDispatchError(req, ValidationErrorCategoryPanic, fmt.Sprintf("%v", recovered), d.clock.Now())
			out.ResultArtifact = validatorErrorArtifact(reg, req, out.Error, debug.Stack())
			err = nil
		}
	}()
	result, callErr := reg.Handler.ValidateArtifact(runCtx, ValidatorHandlerRequest{Artifact: req.Artifact, Validation: req.Validation, StartedAt: started})
	if callErr != nil {
		return validationDispatchError(req, ValidationErrorCategoryHandler, callErr.Error(), d.clock.Now()), nil
	}
	if runCtx.Err() != nil {
		return validationDispatchError(req, ValidationErrorCategoryTimeout, runCtx.Err().Error(), d.clock.Now()), nil
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

func normalizeValidatorRegistration(reg ValidatorRegistration) (ValidatorRegistration, error) {
	reg.ValidatorID = strings.TrimSpace(reg.ValidatorID)
	reg.ValidationType = ValidationType(strings.TrimSpace(string(reg.ValidationType)))
	reg.ActionType = ActionType(strings.TrimSpace(string(reg.ActionType)))
	reg.Determinism = HandlerDeterminism(strings.TrimSpace(string(reg.Determinism)))
	reg.TargetArtifactName = strings.TrimSpace(reg.TargetArtifactName)
	reg.ArtifactDataType = strings.TrimSpace(reg.ArtifactDataType)
	reg.ResultDataType = strings.TrimSpace(reg.ResultDataType)
	return reg, validateValidatorRegistration(reg)
}

func validateValidatorRegistration(reg ValidatorRegistration) error {
	if reg.ValidationType == "" || !reg.Determinism.valid() || reg.Handler == nil {
		return fmt.Errorf("%w: validation type, determinism, and handler are required", ErrValidatorRegistrationInvalid)
	}
	if reg.Timeout <= 0 || reg.ConcurrencyBudget <= 0 {
		return fmt.Errorf("%w: timeout and concurrency budget must be bounded positive values", ErrValidatorRegistrationInvalid)
	}
	if reg.ArtifactDataType == "" && reg.TargetArtifactName == "" {
		return fmt.Errorf("%w: artifact target contract is required", ErrValidatorRegistrationInvalid)
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

func validateDispatchInput(reg ValidatorRegistration, req ValidationDispatchRequest) error {
	if req.Validation == nil || req.Artifact == nil {
		return fmt.Errorf("validation and artifact are required")
	}
	if reg.TargetArtifactName != "" && req.Artifact.ArtifactName != reg.TargetArtifactName {
		return fmt.Errorf("artifact %q does not match target %q", req.Artifact.ArtifactName, reg.TargetArtifactName)
	}
	if reg.ArtifactDataType != "" && req.Artifact.DataType != reg.ArtifactDataType {
		return fmt.Errorf("artifact datatype %q does not match target %q", req.Artifact.DataType, reg.ArtifactDataType)
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
	if validation != nil && !validation.Required {
		return ValidationStatusErroredNotRequired
	}
	if err != nil && err.Category == ValidationErrorCategoryArtifactType {
		return ValidationStatusValidationFailed
	}
	return ValidationStatusErrored
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

func firstNonNilClock(clock ClaimsClock) ClaimsClock {
	if clock == nil {
		return SystemClock{}
	}
	return clock
}
