package claims

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrQualityBarDispatchInvalid = errors.New("quality-bar dispatch invalid")

const defaultQualityBarInlineBytes = 8 * 1024

type QualityBarDispatcherConfig struct {
	Board          *ClaimsBoard
	Evaluator      AgentTurnEvaluator
	Scope          ScopeProvider
	Clock          ClaimsClock
	ActorID        string
	Timeout        time.Duration
	MaxInlineBytes int
}

type QualityBarDispatcher struct {
	board          *ClaimsBoard
	evaluator      AgentTurnEvaluator
	scope          ScopeProvider
	clock          ClaimsClock
	actorID        string
	timeout        time.Duration
	maxInlineBytes int
}

func NewQualityBarDispatcher(cfg QualityBarDispatcherConfig) (*QualityBarDispatcher, error) {
	if cfg.Board == nil || cfg.Evaluator == nil {
		return nil, fmt.Errorf("%w: board and evaluator are required", ErrQualityBarDispatchInvalid)
	}
	scope := cfg.Scope
	if scope == nil {
		scope = cfg.Board.scope
	}
	return &QualityBarDispatcher{
		board:          cfg.Board,
		evaluator:      cfg.Evaluator,
		scope:          scope,
		clock:          firstNonNilClock(cfg.Clock),
		actorID:        strings.TrimSpace(cfg.ActorID),
		timeout:        cfg.Timeout,
		maxInlineBytes: positiveOrDefault(cfg.MaxInlineBytes, defaultQualityBarInlineBytes),
	}, nil
}

func (d *QualityBarDispatcher) DispatchQualityBarByID(ctx context.Context, claimID, validationID string) (QualityBarEvaluationResult, error) {
	req, err := d.requestForQualityBar(claimID, validationID)
	if err != nil {
		return QualityBarEvaluationResult{}, err
	}
	return d.DispatchQualityBar(ctx, req)
}

func (d *QualityBarDispatcher) DispatchQualityBar(ctx context.Context, req QualityBarEvaluationRequest) (QualityBarEvaluationResult, error) {
	if err := d.validateRequest(req); err != nil {
		return QualityBarEvaluationResult{}, err
	}
	req.Context = d.qualityBarContext(req)
	result, err := d.evaluate(ctxOrBackground(ctx), req)
	if err != nil {
		result = d.errorResult(req, ValidationErrorCategoryInternal, err.Error())
	}
	result = d.normalizeResult(req, result)
	return result, d.commitResult(ctxOrBackground(ctx), req, result)
}

func (d *QualityBarDispatcher) requestForQualityBar(claimID, validationID string) (QualityBarEvaluationRequest, error) {
	validation, claim, ok := d.board.CloneValidation(strings.TrimSpace(validationID))
	if !ok || claim == nil || claim.ID != strings.TrimSpace(claimID) {
		return QualityBarEvaluationRequest{}, fmt.Errorf("%w: validation %q not found on claim %q", ErrQualityBarDispatchInvalid, validationID, claimID)
	}
	artifact, ok := d.board.cloneValidationTargetArtifact(claim.ID, validation)
	if !ok {
		return QualityBarEvaluationRequest{}, fmt.Errorf("%w: target artifact %q not found", ErrQualityBarDispatchInvalid, validation.TargetArtifactName)
	}
	result, _ := d.board.CloneArtifact(validation.ResultArtifactID)
	return QualityBarEvaluationRequest{Claim: claim, Artifact: artifact, Validation: validation, ResultArtifact: result}, nil
}

func (d *QualityBarDispatcher) validateRequest(req QualityBarEvaluationRequest) error {
	if d == nil || d.board == nil || d.evaluator == nil {
		return fmt.Errorf("%w: dispatcher is not initialized", ErrQualityBarDispatchInvalid)
	}
	if req.Claim == nil || req.Validation == nil || req.Artifact == nil {
		return fmt.Errorf("%w: claim, validation, and artifact are required", ErrQualityBarDispatchInvalid)
	}
	if req.Validation.Status != ValidationStatusValidatingQualityBar {
		return fmt.Errorf("%w: validation %q is %q, want %q", ErrQualityBarDispatchInvalid, req.Validation.ID, req.Validation.Status, ValidationStatusValidatingQualityBar)
	}
	if strings.TrimSpace(req.Validation.QualityBar) == "" {
		return fmt.Errorf("%w: validation %q has empty quality bar", ErrQualityBarDispatchInvalid, req.Validation.ID)
	}
	return nil
}

func (d *QualityBarDispatcher) evaluate(ctx context.Context, req QualityBarEvaluationRequest) (QualityBarEvaluationResult, error) {
	runCtx, cancel := d.evaluationContext(ctx)
	defer cancel()
	if d.scope == nil {
		return d.evaluator.EvaluateArtifactQualityBar(runCtx, req)
	}
	return d.evaluateTracked(runCtx, req)
}

func (d *QualityBarDispatcher) evaluationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if d.timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d.timeout)
}

func (d *QualityBarDispatcher) evaluateTracked(ctx context.Context, req QualityBarEvaluationRequest) (QualityBarEvaluationResult, error) {
	type outcome struct {
		result QualityBarEvaluationResult
		err    error
	}
	done := make(chan outcome, 1)
	if err := d.scope.Go("claims_quality_bar_evaluation", d.timeout, func(scopeCtx context.Context) error {
		result, err := d.evaluator.EvaluateArtifactQualityBar(scopeCtx, req)
		select {
		case done <- outcome{result: result, err: err}:
		case <-ctx.Done():
		}
		return nil
	}); err != nil {
		return QualityBarEvaluationResult{}, err
	}
	select {
	case got := <-done:
		return got.result, got.err
	case <-ctx.Done():
		return QualityBarEvaluationResult{}, ctx.Err()
	}
}

func (d *QualityBarDispatcher) qualityBarContext(req QualityBarEvaluationRequest) QualityBarEvaluationContext {
	ctx := QualityBarEvaluationContext{QualityBar: strings.TrimSpace(req.Validation.QualityBar)}
	ctx.ArtifactInline, ctx.ArtifactReference = d.artifactContextPayload(req.Artifact)
	ctx.ResultArtifactInline, ctx.ResultArtifactReference = d.artifactContextPayload(req.ResultArtifact)
	ctx.RetrievalRequired = ctx.ArtifactReference != "" || ctx.ResultArtifactReference != ""
	return ctx
}

func (d *QualityBarDispatcher) artifactContextPayload(artifact *Artifact) (string, string) {
	if artifact == nil {
		return "", ""
	}
	if len(artifact.Data) > 0 && len(artifact.Data) <= d.maxInlineBytes {
		return string(artifact.Data), ""
	}
	if strings.TrimSpace(artifact.Reference) != "" && len(artifact.Reference) <= d.maxInlineBytes {
		return artifact.Reference, ""
	}
	return "", artifactRetrievalReference(artifact)
}

func artifactRetrievalReference(artifact *Artifact) string {
	if artifact == nil {
		return ""
	}
	return "claims://artifact/" + strings.TrimSpace(artifact.ID) + "?hash=" + strings.TrimSpace(artifact.ContentHash)
}

func (d *QualityBarDispatcher) normalizeResult(req QualityBarEvaluationRequest, result QualityBarEvaluationResult) QualityBarEvaluationResult {
	if result.EvaluatedAt.IsZero() {
		result.EvaluatedAt = d.clock.Now()
	}
	status, err := qualityBarTerminalStatus(req.Validation, result.Status)
	if err != nil {
		return d.errorResult(req, ValidationErrorCategoryQualityBar, err.Error())
	}
	result.Status = status
	if result.Error != nil {
		result.Error.Source = firstNonZeroParticipantRef(result.Error.Source, result.Evaluator)
	}
	return result
}

func qualityBarTerminalStatus(validation *Validation, status ValidationStatus) (ValidationStatus, error) {
	switch status {
	case ValidationStatusValidated:
		return ValidationStatusValidated, nil
	case ValidationStatusQualityBarValidationFailed, ValidationStatusFailed, ValidationStatusValidationFailed:
		return validationLifecycleTarget(validation, ValidationStatusQualityBarValidationFailed), nil
	case ValidationStatusQualityBarValidationFailedNotRequired, ValidationStatusSkipped:
		return ValidationStatusQualityBarValidationFailedNotRequired, nil
	case ValidationStatusErrored, ValidationStatusErroredNotRequired:
		return validationLifecycleTarget(validation, ValidationStatusErrored), nil
	default:
		return "", fmt.Errorf("malformed quality-bar verdict status %q", status)
	}
}

func (d *QualityBarDispatcher) errorResult(req QualityBarEvaluationRequest, category ValidationErrorCategory, reason string) QualityBarEvaluationResult {
	err := &ValidationError{
		Category:    category,
		Description: strings.TrimSpace(reason),
		Source:      validationErrorSource(ValidationDispatchRequest{Claim: req.Claim, Artifact: req.Artifact, Validation: req.Validation}),
		OccurredAt:  d.clock.Now(),
	}
	return QualityBarEvaluationResult{
		Status:      validationLifecycleTarget(req.Validation, ValidationStatusErrored),
		Error:       err,
		Evaluator:   DegradedAgentRef(d.actorID, "quality-bar evaluator failed"),
		EvaluatedAt: err.OccurredAt,
	}
}

func (d *QualityBarDispatcher) commitResult(ctx context.Context, req QualityBarEvaluationRequest, result QualityBarEvaluationResult) error {
	opts := ValidationLifecycleOptions{
		Reason:           qualityBarResultReason(result),
		TargetArtifactID: req.Artifact.ID,
		ResultArtifactID: resultArtifactID(req.ResultArtifact),
		Error:            result.Error,
		EvaluatorRef:     &result.Evaluator,
	}
	if result.Error != nil {
		opts.ResultArtifact = validatorErrorArtifact(ValidatorRegistration{ValidatorID: "quality_bar"}, ValidationDispatchRequest{Claim: req.Claim, Artifact: req.Artifact, Validation: req.Validation}, result.Error, nil)
	}
	return d.board.CompleteValidationLifecycle(ctx, req.Claim.ID, req.Validation.ID, firstNonEmpty(d.actorID, validationAgentID(req.Validation)), result.Status, opts)
}

func qualityBarResultReason(result QualityBarEvaluationResult) string {
	if result.Error != nil && strings.TrimSpace(result.Error.Description) != "" {
		return result.Error.Description
	}
	return "quality-bar validation completed"
}

func resultArtifactID(artifact *Artifact) string {
	if artifact == nil {
		return ""
	}
	return artifact.ID
}

func firstNonZeroParticipantRef(values ...ParticipantRef) ParticipantRef {
	for _, value := range values {
		if value.RouteKey() != "" {
			return value
		}
	}
	return ParticipantRef{}
}
