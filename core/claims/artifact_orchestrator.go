package claims

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var ErrArtifactOrchestratorInvalid = errors.New("artifact orchestrator invalid")

type ArtifactOrchestratorConfig struct {
	Board             *ClaimsBoard
	Dispatcher        ValidationDispatcher
	QualityBar        *QualityBarDispatcher
	Scope             ScopeProvider
	ActorID           string
	ConcurrencyBudget int
	Timeout           time.Duration
}

type ArtifactOrchestrator struct {
	board             *ClaimsBoard
	dispatcher        ValidationDispatcher
	qualityBar        *QualityBarDispatcher
	scope             ScopeProvider
	actorID           string
	concurrencyBudget int
	timeout           time.Duration
}

type ArtifactValidationOutcome struct {
	ClaimID         string
	TestamentID     string
	ArtifactID      string
	Status          ArtifactStatus
	Results         []ValidationDispatchResult
	ResultArtifacts []*Artifact
	BlockingError   *ArtifactError
}

func NewArtifactOrchestrator(cfg ArtifactOrchestratorConfig) (*ArtifactOrchestrator, error) {
	if cfg.Board == nil || cfg.Dispatcher == nil {
		return nil, fmt.Errorf("%w: board and dispatcher are required", ErrArtifactOrchestratorInvalid)
	}
	scope := cfg.Scope
	if scope == nil {
		scope = cfg.Board.scope
	}
	return &ArtifactOrchestrator{
		board:             cfg.Board,
		dispatcher:        cfg.Dispatcher,
		qualityBar:        cfg.QualityBar,
		scope:             scope,
		actorID:           strings.TrimSpace(cfg.ActorID),
		concurrencyBudget: cfg.ConcurrencyBudget,
		timeout:           cfg.Timeout,
	}, nil
}

func (o *ArtifactOrchestrator) ValidateArtifact(ctx context.Context, claimID, testamentID, artifactID string) (ArtifactValidationOutcome, error) {
	claim, artifact, err := o.loadArtifactValidationInputs(claimID, testamentID, artifactID)
	if err != nil {
		return ArtifactValidationOutcome{}, err
	}
	outcome := ArtifactValidationOutcome{ClaimID: claim.ID, TestamentID: artifact.TestamentID, ArtifactID: artifact.ID}
	if artifact.Status.IsTerminal() {
		outcome.Status = artifact.Status
		return outcome, nil
	}
	validations := validationsForArtifact(claim, artifact.ArtifactName)
	if err := o.beginArtifactValidation(ctx, artifact); err != nil {
		return outcome, err
	}
	outcome = o.dispatchArtifactValidations(ctxOrBackground(ctx), claim, artifact, validations)
	outcome.Status = artifactTerminalStatusForOutcome(outcome)
	return outcome, o.completeArtifactValidation(ctxOrBackground(ctx), outcome)
}

func (o *ArtifactOrchestrator) loadArtifactValidationInputs(claimID, testamentID, artifactID string) (*Claim, *Artifact, error) {
	if o == nil || o.board == nil || o.dispatcher == nil {
		return nil, nil, fmt.Errorf("%w: orchestrator is not initialized", ErrArtifactOrchestratorInvalid)
	}
	artifact, testament, claim, ok := o.board.cloneArtifactWithParents(strings.TrimSpace(artifactID))
	if !ok || artifact == nil {
		return nil, nil, fmt.Errorf("%w: artifact %q not found", ErrArtifactOrchestratorInvalid, artifactID)
	}
	if err := validateArtifactOrchestratorParentage(claim, testament, artifact, claimID, testamentID); err != nil {
		return nil, nil, err
	}
	return claim, artifact, nil
}

func validateArtifactOrchestratorParentage(claim *Claim, testament *Testament, artifact *Artifact, claimID, testamentID string) error {
	if claim == nil || claim.ID != strings.TrimSpace(claimID) {
		return fmt.Errorf("%w: artifact %q claim parent mismatch", ErrArtifactOrchestratorInvalid, artifact.ID)
	}
	if testament == nil || testament.ID != strings.TrimSpace(testamentID) {
		return fmt.Errorf("%w: artifact %q testament parent mismatch", ErrArtifactOrchestratorInvalid, artifact.ID)
	}
	if artifact.TestamentID == "" || artifact.Status == ArtifactStatusGenerated || artifact.Status == ArtifactStatusReceived {
		return fmt.Errorf("%w: artifact %q is not attached", ErrArtifactOrchestratorInvalid, artifact.ID)
	}
	return nil
}

func validationsForArtifact(claim *Claim, artifactName string) []*Validation {
	out := make([]*Validation, 0, len(claim.Validations))
	for _, validation := range claim.Validations {
		if validation != nil && validation.TargetArtifactName == artifactName {
			out = append(out, cloneValidationEntity(validation))
		}
	}
	return out
}

func (o *ArtifactOrchestrator) beginArtifactValidation(ctx context.Context, artifact *Artifact) error {
	if artifact.Status == ArtifactStatusValidating {
		return nil
	}
	return o.board.BeginArtifactValidation(ctx, artifact.ID, firstNonEmpty(o.actorID, artifact.AgentID))
}

func (o *ArtifactOrchestrator) dispatchArtifactValidations(ctx context.Context, claim *Claim, artifact *Artifact, validations []*Validation) ArtifactValidationOutcome {
	if len(validations) == 0 {
		return ArtifactValidationOutcome{ClaimID: claim.ID, TestamentID: artifact.TestamentID, ArtifactID: artifact.ID}
	}
	if o.scope == nil {
		return o.dispatchArtifactValidationsSerial(ctx, claim, artifact, validations)
	}
	return o.dispatchArtifactValidationsTracked(ctx, claim, artifact, validations)
}

func (o *ArtifactOrchestrator) dispatchArtifactValidationsSerial(ctx context.Context, claim *Claim, artifact *Artifact, validations []*Validation) ArtifactValidationOutcome {
	outcome := ArtifactValidationOutcome{ClaimID: claim.ID, TestamentID: artifact.TestamentID, ArtifactID: artifact.ID}
	for _, validation := range validations {
		result := o.dispatchOneValidation(ctx, claim, artifact, validation)
		outcome = appendArtifactDispatchResult(outcome, result)
		if artifactDispatchBlocks(validation, result) {
			break
		}
	}
	return outcome
}

func (o *ArtifactOrchestrator) dispatchArtifactValidationsTracked(ctx context.Context, claim *Claim, artifact *Artifact, validations []*Validation) ArtifactValidationOutcome {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	workers := boundedWorkerCount(o.concurrencyBudget, len(validations))
	results := make(chan artifactWorkerResult, len(validations)+workers)
	scheduler := newArtifactValidationScheduler(validations)
	o.startArtifactValidationWorkers(runCtx, workers, claim, artifact, scheduler, results, cancel)
	outcome := collectArtifactWorkerResults(results, workers)
	outcome.ClaimID = claim.ID
	outcome.TestamentID = artifact.TestamentID
	outcome.ArtifactID = artifact.ID
	return outcome
}

type artifactValidationScheduler struct {
	mu          sync.Mutex
	validations []*Validation
	next        int
	stopped     bool
}

func newArtifactValidationScheduler(validations []*Validation) *artifactValidationScheduler {
	return &artifactValidationScheduler{validations: validations}
}

func (s *artifactValidationScheduler) Next(ctx context.Context) *Validation {
	if s == nil || ctx.Err() != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped || s.next >= len(s.validations) {
		return nil
	}
	validation := s.validations[s.next]
	s.next++
	return validation
}

func (s *artifactValidationScheduler) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()
}

type artifactWorkerResult struct {
	Result ValidationDispatchResult
	Done   bool
}

func (o *ArtifactOrchestrator) startArtifactValidationWorkers(ctx context.Context, workers int, claim *Claim, artifact *Artifact, scheduler *artifactValidationScheduler, results chan<- artifactWorkerResult, cancel context.CancelFunc) {
	for idx := 0; idx < workers; idx++ {
		workerID := idx
		if err := o.scope.Go("claims_artifact_validation", o.timeout, func(workerCtx context.Context) error {
			defer func() { results <- artifactWorkerResult{Done: true} }()
			o.runArtifactValidationWorker(workerCtx, claim, artifact, scheduler, results, cancel)
			return nil
		}); err != nil {
			results <- artifactWorkerResult{Result: scopeLaunchValidationError(claim, artifact, workerID, err)}
			results <- artifactWorkerResult{Done: true}
		}
	}
}

func (o *ArtifactOrchestrator) runArtifactValidationWorker(ctx context.Context, claim *Claim, artifact *Artifact, scheduler *artifactValidationScheduler, results chan<- artifactWorkerResult, cancel context.CancelFunc) {
	for {
		validation := scheduler.Next(ctx)
		if validation == nil {
			return
		}
		result := o.dispatchOneValidation(ctx, claim, artifact, validation)
		results <- artifactWorkerResult{Result: result}
		if artifactDispatchBlocks(validation, result) {
			scheduler.Stop()
			cancel()
			return
		}
	}
}

func collectArtifactWorkerResults(results <-chan artifactWorkerResult, workers int) ArtifactValidationOutcome {
	outcome := ArtifactValidationOutcome{}
	done := 0
	for done < workers {
		item := <-results
		if item.Done {
			done++
			continue
		}
		outcome = appendArtifactDispatchResult(outcome, item.Result)
	}
	return outcome
}

func (o *ArtifactOrchestrator) dispatchOneValidation(ctx context.Context, claim *Claim, artifact *Artifact, validation *Validation) ValidationDispatchResult {
	result, err := o.dispatcher.DispatchValidation(ctx, ValidationDispatchRequest{Claim: CloneClaimEntity(claim), Artifact: CloneArtifact(artifact), Validation: cloneValidationEntity(validation), StartedAt: time.Now().UTC()})
	if err != nil {
		return dispatchErrorResult(validation, artifact, err)
	}
	if result.Status == ValidationStatusValidatingQualityBar && o.qualityBar != nil {
		return o.dispatchQualityBar(ctx, claim, artifact, validation, result)
	}
	if result.Status == ValidationStatusValidatingQualityBar {
		return o.dispatchMissingQualityBar(ctx, claim, artifact, validation)
	}
	return result
}

func (o *ArtifactOrchestrator) dispatchMissingQualityBar(ctx context.Context, claim *Claim, artifact *Artifact, validation *Validation) ValidationDispatchResult {
	err := fmt.Errorf("quality-bar dispatcher is required for validation %q", validationIDFromRequest(ValidationDispatchRequest{Validation: validation}))
	result := dispatchErrorResult(validation, artifact, err)
	result.ResultArtifact = validatorErrorArtifact(ValidatorRegistration{ValidatorID: "quality_bar"}, ValidationDispatchRequest{Claim: claim, Artifact: artifact, Validation: validation}, result.Error, nil)
	commitErr := o.board.CompleteValidationLifecycle(ctx, claim.ID, validation.ID, firstNonEmpty(o.actorID, validationAgentID(validation)), result.Status, ValidationLifecycleOptions{
		Reason:           validationResultReason(result),
		TargetArtifactID: artifact.ID,
		ResultArtifact:   result.ResultArtifact,
		Error:            result.Error,
	})
	if commitErr != nil {
		return dispatchErrorResult(validation, artifact, commitErr)
	}
	return o.withCommittedDispatchResult(validation, result)
}

func (o *ArtifactOrchestrator) withCommittedDispatchResult(validation *Validation, result ValidationDispatchResult) ValidationDispatchResult {
	if o == nil || o.board == nil || validation == nil {
		return result
	}
	stored, _, ok := o.board.CloneValidation(validation.ID)
	if !ok || stored == nil {
		return result
	}
	result.ResultArtifactID = firstNonEmpty(stored.ResultArtifactID, result.ResultArtifactID)
	if result.ResultArtifactID == "" {
		return result
	}
	if artifact, ok := o.board.CloneArtifact(result.ResultArtifactID); ok {
		result.ResultArtifact = artifact
	}
	return result
}

func (o *ArtifactOrchestrator) dispatchQualityBar(ctx context.Context, claim *Claim, artifact *Artifact, validation *Validation, prior ValidationDispatchResult) ValidationDispatchResult {
	quality, err := o.qualityBar.DispatchQualityBarByID(ctx, claim.ID, validation.ID)
	if err != nil {
		return dispatchErrorResult(validation, artifact, err)
	}
	prior.Status = quality.Status
	prior.Error = quality.Error
	prior.CompletedAt = quality.EvaluatedAt
	return prior
}

func appendArtifactDispatchResult(outcome ArtifactValidationOutcome, result ValidationDispatchResult) ArtifactValidationOutcome {
	outcome.Results = append(outcome.Results, result)
	if result.ResultArtifact != nil {
		outcome.ResultArtifacts = append(outcome.ResultArtifacts, CloneArtifact(result.ResultArtifact))
	}
	if result.Error != nil && outcome.BlockingError == nil {
		outcome.BlockingError = artifactValidationError(result)
	}
	return outcome
}

func artifactTerminalStatusForOutcome(outcome ArtifactValidationOutcome) ArtifactStatus {
	for _, result := range outcome.Results {
		if result.Status.IsBlockingFailure() {
			return ArtifactStatusValidationFailed
		}
	}
	return ArtifactStatusValidated
}

func (o *ArtifactOrchestrator) completeArtifactValidation(ctx context.Context, outcome ArtifactValidationOutcome) error {
	return o.board.CompleteArtifactValidation(ctx, outcome.ArtifactID, o.actorID, outcome.Status == ArtifactStatusValidated, outcome.BlockingError)
}

func artifactDispatchBlocks(validation *Validation, result ValidationDispatchResult) bool {
	return validation != nil && validation.Required && result.Status.IsBlockingFailure()
}

func dispatchErrorResult(validation *Validation, artifact *Artifact, err error) ValidationDispatchResult {
	validationErr := &ValidationError{Category: ValidationErrorCategoryDispatcher, Description: err.Error(), Source: validationErrorSource(ValidationDispatchRequest{Validation: validation, Artifact: artifact}), OccurredAt: time.Now().UTC()}
	return ValidationDispatchResult{ValidationID: validationIDFromRequest(ValidationDispatchRequest{Validation: validation}), Status: validationStatusForValidationError(validation, validationErr), Error: validationErr, CompletedAt: validationErr.OccurredAt, ShortCircuitRest: validation != nil && validation.Required}
}

func scopeLaunchValidationError(claim *Claim, artifact *Artifact, workerID int, err error) ValidationDispatchResult {
	validation := &Validation{ID: fmt.Sprintf("artifact-worker-%d", workerID), Required: true}
	return dispatchErrorResult(validation, artifact, err)
}

func artifactValidationError(result ValidationDispatchResult) *ArtifactError {
	if result.Error == nil {
		return nil
	}
	return &ArtifactError{
		Category:    artifactErrorCategoryForValidation(result.Error.Category),
		Description: result.Error.Description,
		Source:      result.Error.Source,
		OccurredAt:  firstNonZeroTime(result.Error.OccurredAt, time.Now().UTC()),
	}
}

func artifactErrorCategoryForValidation(category ValidationErrorCategory) ArtifactErrorCategory {
	switch category {
	case ValidationErrorCategoryTimeout:
		return ArtifactErrorCategoryTimeout
	case ValidationErrorCategoryPanic:
		return ArtifactErrorCategoryPanic
	case ValidationErrorCategoryInterruption:
		return ArtifactErrorCategoryInterruption
	case ValidationErrorCategoryDispatcher, ValidationErrorCategoryDependency, ValidationErrorCategoryInternal:
		return ArtifactErrorCategoryInternal
	default:
		return ArtifactErrorCategoryValidation
	}
}

func boundedWorkerCount(configured, total int) int {
	if total <= 0 {
		return 0
	}
	if configured <= 0 || configured > total {
		return total
	}
	return configured
}
