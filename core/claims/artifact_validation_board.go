package claims

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type ArtifactLifecycleOptions struct {
	Reason       string
	Error        *ArtifactError
	ValidationID string
}

type ValidationLifecycleOptions struct {
	Reason           string
	TargetArtifactID string
	ResultArtifact   *Artifact
	ResultArtifactID string
	Error            *ValidationError
	EvaluatorRef     *ParticipantRef
}

func (b *ClaimsBoard) GenerateArtifact(ctx context.Context, artifact Artifact, actorID string, opts ArtifactLifecycleOptions) (*Artifact, error) {
	return b.generateArtifactLifecycle(ctx, artifact, ArtifactStatusGenerated, actorID, opts)
}

func (b *ClaimsBoard) RecordArtifactGenerationFailure(ctx context.Context, artifact Artifact, actorID string, artifactErr *ArtifactError) (*Artifact, error) {
	return b.generateArtifactLifecycle(ctx, artifact, ArtifactStatusGenerationFailed, actorID, ArtifactLifecycleOptions{Reason: "artifact generation failed", Error: artifactErr})
}

func (b *ClaimsBoard) generateArtifactLifecycle(ctx context.Context, artifact Artifact, to ArtifactStatus, actorID string, opts ArtifactLifecycleOptions) (*Artifact, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	now := time.Now().UTC()
	stamped := CloneArtifact(&artifact)
	b.stampGeneratedArtifactLocked(stamped, to, actorID, opts, now)
	if existing := b.artifacts[stamped.ID]; existing != nil {
		b.mu.Unlock()
		return CloneArtifact(existing), nil
	}
	payload := artifactLifecyclePayload(stamped.ID, to, actorID, opts, now)
	payload["artifact"] = stamped
	if err := b.appendDurableEventLocked(walEventArtifactLifecycleTransition, actorID, payload, []ClaimsOutboxRecord{b.outboxRecordLocked(stamped.Sequence, RelatedTypeArtifact, stamped.ID, string(mustArtifactLifecycleDeltaAction(to)), now)}); err != nil {
		b.mu.Unlock()
		return nil, err
	}
	b.indexArtifactLocked(stamped)
	claimSnapshot := CloneClaimEntity(b.claims[stamped.ClaimID])
	artifactSnapshot := CloneArtifact(stamped)
	b.invalidateProjectionCache()
	b.mu.Unlock()
	b.projectDurableOutbox(ctx)
	b.amplifier.PublishCanonicalArtifactLifecycle(ctx, artifactSnapshot, nil, claimSnapshot, to, actorID, now)
	b.notifySubscribers()
	return artifactSnapshot, nil
}

func (b *ClaimsBoard) TransitionArtifactLifecycle(ctx context.Context, artifactID string, to ArtifactStatus, actorID string, opts ArtifactLifecycleOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	artifact, testament, claim, ok := b.findArtifactForMutationLocked(artifactID)
	if !ok {
		b.mu.Unlock()
		return fmt.Errorf("artifact %q not found", artifactID)
	}
	from := artifact.Status
	if from == to {
		b.mu.Unlock()
		return nil
	}
	if !CanTransitionArtifactStatus(from, to) {
		b.mu.Unlock()
		return newArtifactLifecycleTransitionError(artifact.ID, from, to, actorID, "artifact status transition is not allowed")
	}
	now := time.Now().UTC()
	action := mustArtifactLifecycleDeltaAction(to)
	outboxRecords := []ClaimsOutboxRecord{b.outboxRecordLocked(artifact.Sequence, RelatedTypeArtifact, artifact.ID, string(action), now)}
	if err := b.appendDurableEventLocked(walEventArtifactLifecycleTransition, actorID, artifactLifecyclePayload(artifact.ID, to, actorID, opts, now), outboxRecords); err != nil {
		b.mu.Unlock()
		return err
	}
	if _, err := TransitionArtifactStatus(artifact, to, actorID, opts.Reason, now); err != nil {
		b.mu.Unlock()
		return err
	}
	if opts.Error != nil {
		artifact.Errors = append(artifact.Errors, cloneArtifactError(opts.Error))
	}
	artifact.Accessed = now
	artifactSnapshot := CloneArtifact(artifact)
	testamentSnapshot := CloneTestamentEntity(testament)
	claimSnapshot := CloneClaimEntity(claim)
	b.invalidateProjectionCache()
	b.mu.Unlock()
	b.projectDurableOutbox(ctx)
	b.amplifier.PublishCanonicalArtifactLifecycle(ctx, artifactSnapshot, testamentSnapshot, claimSnapshot, to, actorID, now)
	b.notifySubscribers()
	return nil
}

func (b *ClaimsBoard) AcknowledgeArtifactReceipt(ctx context.Context, artifactID, receiverID string) error {
	return b.TransitionArtifactLifecycle(ctx, artifactID, ArtifactStatusReceived, receiverID, ArtifactLifecycleOptions{Reason: "artifact received"})
}

func (b *ClaimsBoard) RecordArtifactReceiptFailure(ctx context.Context, artifactID, receiverID string, artifactErr *ArtifactError) error {
	return b.TransitionArtifactLifecycle(ctx, artifactID, ArtifactStatusReceiptFailed, receiverID, ArtifactLifecycleOptions{Reason: "artifact receipt failed", Error: artifactErr})
}

func (b *ClaimsBoard) BeginArtifactValidation(ctx context.Context, artifactID, actorID string) error {
	return b.TransitionArtifactLifecycle(ctx, artifactID, ArtifactStatusValidating, actorID, ArtifactLifecycleOptions{Reason: "artifact validation started"})
}

func (b *ClaimsBoard) CompleteArtifactValidation(ctx context.Context, artifactID, actorID string, validated bool, artifactErr *ArtifactError) error {
	if validated {
		return b.TransitionArtifactLifecycle(ctx, artifactID, ArtifactStatusValidated, actorID, ArtifactLifecycleOptions{Reason: "artifact validated"})
	}
	return b.TransitionArtifactLifecycle(ctx, artifactID, ArtifactStatusValidationFailed, actorID, ArtifactLifecycleOptions{Reason: "artifact validation failed", Error: artifactErr})
}

func (b *ClaimsBoard) TransitionValidationLifecycle(ctx context.Context, claimID, validationID string, to ValidationStatus, actorID string, opts ValidationLifecycleOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	validation, claim, ok := b.findValidationForMutationLocked(claimID, validationID)
	if !ok {
		b.mu.Unlock()
		return fmt.Errorf("validation %q not found on claim %q", validationID, claimID)
	}
	to = validationLifecycleTarget(validation, to)
	from := validation.Status
	if from == to {
		b.mu.Unlock()
		return nil
	}
	if !CanTransitionValidationStatus(from, to) {
		b.mu.Unlock()
		return newValidationLifecycleTransitionError(validation.ID, from, to, actorID, "validation status transition is not allowed")
	}
	now := time.Now().UTC()
	prevSeq := b.seq.Load()
	opts = b.prepareValidationLifecycleOptionsLocked(claim, validation, actorID, opts, now)
	accepted := claimAcceptedAfterValidation(claim, validation.ID, to)
	claimStatus, claimLifecycle, hasClaimOutcome := validationClaimOutcome(claim, validation, to, accepted)
	payload := validationLifecyclePayload(claim.ID, validation.ID, to, actorID, opts, now)
	outboxRecords := validationLifecycleOutboxRecordsLocked(b, validation, to, now)
	if opts.ResultArtifact != nil {
		outboxRecords = append(outboxRecords, b.outboxRecordLocked(opts.ResultArtifact.Sequence, RelatedTypeArtifact, opts.ResultArtifact.ID, string(DeltaActionArtifactGenerated), now))
	}
	if hasClaimOutcome {
		outboxRecords = append(outboxRecords, validationClaimOutcomeOutboxRecordLocked(b, claim, claimStatus, now))
	}
	if err := b.appendDurableEventLocked(walEventValidationLifecycleTransition, actorID, payload, outboxRecords); err != nil {
		b.seq.Store(prevSeq)
		b.mu.Unlock()
		return err
	}
	resultArtifactID := b.recordValidationLifecycleMutationLocked(claim, validation, to, actorID, opts, now)
	b.recordValidationClaimOutcomeLocked(claim, claimStatus, claimLifecycle, hasClaimOutcome, actorID, opts.Reason, now)
	validationSnapshot := cloneValidationEntity(validation)
	claimSnapshot := CloneClaimEntity(claim)
	artifactSnapshot, _ := b.findArtifactSnapshotLocked(firstNonEmpty(opts.TargetArtifactID, resultArtifactID))
	b.invalidateProjectionCache()
	b.mu.Unlock()
	b.projectDurableOutbox(ctx)
	b.amplifier.PublishCanonicalValidationLifecycle(ctx, claimSnapshot, validationSnapshot, artifactSnapshot, to, actorID, now)
	b.notifySubscribers()
	return nil
}

func (b *ClaimsBoard) MarkValidationReady(ctx context.Context, claimID, validationID, actorID string) error {
	return b.TransitionValidationLifecycle(ctx, claimID, validationID, ValidationStatusReady, actorID, ValidationLifecycleOptions{Reason: "validation ready"})
}

func (b *ClaimsBoard) BeginValidation(ctx context.Context, claimID, validationID, actorID, artifactID string) error {
	return b.TransitionValidationLifecycle(ctx, claimID, validationID, ValidationStatusValidating, actorID, ValidationLifecycleOptions{Reason: "validation started", TargetArtifactID: artifactID})
}

func (b *ClaimsBoard) BeginValidationQualityBar(ctx context.Context, claimID, validationID, actorID, artifactID string) error {
	return b.TransitionValidationLifecycle(ctx, claimID, validationID, ValidationStatusValidatingQualityBar, actorID, ValidationLifecycleOptions{Reason: "quality-bar validation started", TargetArtifactID: artifactID})
}

func (b *ClaimsBoard) CompleteValidationLifecycle(ctx context.Context, claimID, validationID, actorID string, status ValidationStatus, opts ValidationLifecycleOptions) error {
	return b.TransitionValidationLifecycle(ctx, claimID, validationID, status, actorID, opts)
}

func validationClaimOutcomeOutboxRecordLocked(b *ClaimsBoard, claim *Claim, status ClaimStatus, now time.Time) ClaimsOutboxRecord {
	action := walEventClaimAccepted
	if status == ClaimStatusRejected {
		action = walEventClaimRejected
	}
	return b.outboxRecordLocked(claim.Sequence, RelatedTypeClaim, claim.ID, action, now)
}

func (b *ClaimsBoard) recordValidationClaimOutcomeLocked(claim *Claim, status ClaimStatus, lifecycle ClaimLifecycleStatus, ok bool, actorID, reason string, now time.Time) {
	if !ok || claim == nil {
		return
	}
	if claim.Status == status && claim.LifecycleStatus == lifecycle {
		return
	}
	prev := claim.Status
	claim.StatusHistory = append(claim.StatusHistory, StatusChange{
		From:    string(prev),
		To:      string(status),
		Reason:  validationClaimOutcomeReason(status, lifecycle, reason),
		AgentID: actorID,
		Changed: now,
	})
	claim.Status = status
	claim.Accessed = now
	b.adjustStatusCounter(prev, status)
	if CanTransitionClaimLifecycle(claim.LifecycleStatus, lifecycle) {
		b.transitionClaimLifecycleLocked(claim, lifecycle, actorID, validationClaimOutcomeReason(status, lifecycle, reason), now)
	}
}

func (b *ClaimsBoard) findArtifactForMutationLocked(id string) (*Artifact, *Testament, *Claim, bool) {
	id = strings.TrimSpace(id)
	if artifact := b.artifacts[id]; artifact != nil {
		testament := b.testaments[artifact.TestamentID]
		return artifact, testament, b.claims[artifactClaimID(artifact, testament)], true
	}
	for _, testament := range b.testaments {
		artifact, ok := artifactOnTestament(testament, id)
		if !ok {
			continue
		}
		return artifact, testament, b.claims[artifactClaimID(artifact, testament)], true
	}
	return nil, nil, nil, false
}

func (b *ClaimsBoard) cloneArtifactWithParents(id string) (*Artifact, *Testament, *Claim, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	artifact, testament, claim, ok := b.findArtifactForMutationLocked(id)
	if !ok {
		return nil, nil, nil, false
	}
	return CloneArtifact(artifact), CloneTestamentEntity(testament), CloneClaimEntity(claim), true
}

func (b *ClaimsBoard) cloneValidationTargetArtifact(claimID string, validation *Validation) (*Artifact, bool) {
	if validation == nil {
		return nil, false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, testament := range b.testaments {
		if ClaimIDFromRelations(testament.Relations) != claimID {
			continue
		}
		for _, artifact := range testament.Artifacts {
			if artifact != nil && artifact.ArtifactName == validation.TargetArtifactName {
				return CloneArtifact(artifact), true
			}
		}
	}
	return nil, false
}

func (b *ClaimsBoard) findValidationForMutationLocked(claimID, validationID string) (*Validation, *Claim, bool) {
	claim, ok := b.claims[strings.TrimSpace(claimID)]
	if !ok {
		return nil, nil, false
	}
	validation := findValidationOnClaim(claim, strings.TrimSpace(validationID))
	return validation, claim, validation != nil
}

func (b *ClaimsBoard) findArtifactSnapshotLocked(id string) (*Artifact, bool) {
	artifact, _, _, ok := b.findArtifactForMutationLocked(id)
	if !ok {
		return nil, false
	}
	return CloneArtifact(artifact), true
}

func artifactOnTestament(testament *Testament, artifactID string) (*Artifact, bool) {
	if testament == nil || artifactID == "" {
		return nil, false
	}
	for _, artifact := range testament.Artifacts {
		if artifact != nil && artifact.ID == artifactID {
			return artifact, true
		}
	}
	return nil, false
}

func artifactClaimID(artifact *Artifact, testament *Testament) string {
	if artifact == nil {
		return ""
	}
	if testament == nil {
		return strings.TrimSpace(artifact.ClaimID)
	}
	return firstNonEmpty(artifact.ClaimID, ClaimIDFromRelations(testament.Relations))
}

func (b *ClaimsBoard) stampGeneratedArtifactLocked(artifact *Artifact, to ArtifactStatus, actorID string, opts ArtifactLifecycleOptions, now time.Time) {
	if artifact.ID == "" {
		artifact.ID = uuid.NewString()
	}
	artifact.AgentID = firstNonEmpty(artifact.AgentID, actorID)
	artifact.ParticipantID = firstNonEmpty(artifact.ParticipantID, artifact.AgentID, actorID)
	artifact.SessionID = b.sessionID
	artifact.PipelineID = b.pipelineID
	artifact.TaskID = b.taskID
	if artifact.Sequence == 0 {
		artifact.Sequence = b.nextSeq()
	}
	artifact.ClaimID = firstNonEmpty(artifact.ClaimID, ClaimIDFromRelations(artifact.Relations))
	artifact.Created = firstNonZeroTime(artifact.Created, now)
	artifact.Accessed = now
	artifact.Status = to
	artifact.StatusHistory = capStatusHistory(append(artifact.StatusHistory, statusChange("", to, firstNonEmpty(actorID, artifact.ParticipantID, artifact.AgentID), firstNonEmpty(opts.Reason, string(to)), now)))
	if opts.Error != nil {
		artifact.Errors = append(artifact.Errors, cloneArtifactError(opts.Error))
	}
	if len(artifact.Data) != 0 && artifact.ContentHash == "" {
		artifact.ContentHash = ArtifactContentHash(artifact.Data)
	}
	artifact.Size = artifactSize(artifact)
	ApplyDefaultArtifactPresentation(artifact)
	artifact.Presentation = NormalizePresentation(artifact.Presentation)
}

func artifactLifecyclePayload(artifactID string, to ArtifactStatus, actorID string, opts ArtifactLifecycleOptions, changed time.Time) map[string]any {
	return map[string]any{
		"artifact_id":   strings.TrimSpace(artifactID),
		"to":            to,
		"agent_id":      strings.TrimSpace(actorID),
		"reason":        strings.TrimSpace(opts.Reason),
		"changed":       changed,
		"error":         opts.Error,
		"validation_id": strings.TrimSpace(opts.ValidationID),
	}
}

func validationLifecyclePayload(claimID, validationID string, to ValidationStatus, actorID string, opts ValidationLifecycleOptions, changed time.Time) map[string]any {
	return map[string]any{
		"claim_id":           strings.TrimSpace(claimID),
		"validation_id":      strings.TrimSpace(validationID),
		"to":                 to,
		"agent_id":           strings.TrimSpace(actorID),
		"reason":             strings.TrimSpace(opts.Reason),
		"changed":            changed,
		"target_artifact_id": strings.TrimSpace(opts.TargetArtifactID),
		"result_artifact":    opts.ResultArtifact,
		"result_artifact_id": strings.TrimSpace(opts.ResultArtifactID),
		"error":              opts.Error,
		"evaluator_ref":      opts.EvaluatorRef,
	}
}

func validationLifecycleOutboxRecordsLocked(b *ClaimsBoard, validation *Validation, to ValidationStatus, now time.Time) []ClaimsOutboxRecord {
	action := mustValidationLifecycleDeltaAction(to)
	return []ClaimsOutboxRecord{b.outboxRecordLocked(validation.Sequence, RelatedTypeValidation, validation.ID, string(action), now)}
}

func (b *ClaimsBoard) prepareValidationLifecycleOptionsLocked(claim *Claim, validation *Validation, actorID string, opts ValidationLifecycleOptions, now time.Time) ValidationLifecycleOptions {
	if opts.ResultArtifact == nil {
		return opts
	}
	result := CloneArtifact(opts.ResultArtifact)
	if b.stampValidationResultArtifactLocked(result, claim, validation, b.testamentForValidationResultLocked(claim, validation), actorID, now) {
		opts.ResultArtifact = result
		opts.ResultArtifactID = result.ID
	}
	return opts
}

func (b *ClaimsBoard) recordValidationLifecycleMutationLocked(claim *Claim, validation *Validation, to ValidationStatus, actorID string, opts ValidationLifecycleOptions, now time.Time) string {
	resultArtifactID := firstNonEmpty(opts.ResultArtifactID, validation.ResultArtifactID)
	if opts.ResultArtifact != nil {
		resultArtifactID = b.appendValidationResultArtifactLocked(claim, validation, opts.ResultArtifact, actorID, now)
	}
	_, _ = TransitionValidationStatus(validation, to, actorID, opts.Reason, now)
	validation.ResultArtifactID = firstNonEmpty(resultArtifactID, validation.ResultArtifactID)
	validation.Error = cloneValidationError(opts.Error)
	validation.EvaluatorRef = cloneParticipantRefPtr(opts.EvaluatorRef)
	validation.Accessed = now
	return validation.ResultArtifactID
}

func (b *ClaimsBoard) appendValidationResultArtifactLocked(claim *Claim, validation *Validation, result *Artifact, actorID string, now time.Time) string {
	testament := b.testamentForValidationResultLocked(claim, validation)
	if testament == nil {
		return ""
	}
	result = CloneArtifact(result)
	b.stampValidationResultArtifactLocked(result, claim, validation, testament, actorID, now)
	testament.Artifacts = append(testament.Artifacts, result)
	b.indexRelations(result.ID, result.Relations)
	return result.ID
}

func (b *ClaimsBoard) testamentForValidationResultLocked(claim *Claim, validation *Validation) *Testament {
	if claim == nil || validation == nil {
		return nil
	}
	for _, testament := range b.testaments {
		if ClaimIDFromRelations(testament.Relations) == claim.ID && validationTargetsTestament(validation, testament) {
			return testament
		}
	}
	return nil
}

func validationTargetsTestament(validation *Validation, testament *Testament) bool {
	for _, artifact := range testament.Artifacts {
		if artifact != nil && artifact.ArtifactName == validation.TargetArtifactName {
			return true
		}
	}
	return false
}

func (b *ClaimsBoard) stampValidationResultArtifactLocked(artifact *Artifact, claim *Claim, validation *Validation, testament *Testament, actorID string, now time.Time) bool {
	if artifact == nil || claim == nil || validation == nil || testament == nil {
		return false
	}
	if artifact.ID == "" {
		artifact.ID = uuid.NewString()
	}
	artifact.TestamentID = testament.ID
	artifact.ClaimID = claim.ID
	artifact.AgentID = firstNonEmpty(artifact.AgentID, actorID)
	artifact.ParticipantID = firstNonEmpty(artifact.ParticipantID, artifact.AgentID)
	artifact.SessionID = b.sessionID
	artifact.PipelineID = b.pipelineID
	artifact.TaskID = b.taskID
	if artifact.Sequence == 0 {
		artifact.Sequence = b.nextSeq()
	}
	artifact.Created = now
	artifact.Accessed = now
	if artifact.ArtifactName == "" {
		artifact.ArtifactName = validation.ID + ".result"
	}
	if artifact.DataType == "" && artifact.Kind != ArtifactKindErrorDiagnostic {
		artifact.DataType = validation.ResultDataType
	}
	if len(artifact.Data) != 0 && artifact.ContentHash == "" {
		artifact.ContentHash = ArtifactContentHash(artifact.Data)
	}
	artifact.Size = artifactSize(artifact)
	if artifact.Status == "" {
		artifact.Status = ArtifactStatusGenerated
		artifact.StatusHistory = capStatusHistory(append(artifact.StatusHistory, statusChange("", ArtifactStatusGenerated, actorID, "validation result generated", now)))
	}
	return true
}

func artifactSize(artifact *Artifact) int64 {
	if artifact == nil {
		return 0
	}
	if len(artifact.Data) == 0 {
		return artifact.Size
	}
	return int64(len(artifact.Data))
}

func validationLifecycleTarget(validation *Validation, to ValidationStatus) ValidationStatus {
	if validation == nil || validation.Required {
		return to
	}
	switch to {
	case ValidationStatusValidationFailed:
		return ValidationStatusValidationFailedNotRequired
	case ValidationStatusErrored:
		return ValidationStatusErroredNotRequired
	case ValidationStatusQualityBarValidationFailed:
		return ValidationStatusQualityBarValidationFailedNotRequired
	default:
		return to
	}
}

func mustArtifactLifecycleDeltaAction(status ArtifactStatus) DeltaAction {
	action, ok := ArtifactLifecycleDeltaAction(status)
	if !ok {
		return DeltaAction(status)
	}
	return action
}

func mustValidationLifecycleDeltaAction(status ValidationStatus) DeltaAction {
	action, ok := ValidationLifecycleDeltaAction(status)
	if !ok {
		return DeltaAction(status)
	}
	return action
}

func cloneArtifactError(in *ArtifactError) *ArtifactError {
	if in == nil {
		return nil
	}
	out := cloneArtifactErrors([]*ArtifactError{in})
	if len(out) == 0 {
		return nil
	}
	return out[0]
}

func cloneParticipantRefPtr(in *ParticipantRef) *ParticipantRef {
	if in == nil {
		return nil
	}
	out := cloneParticipantRef(*in)
	return &out
}

func cloneValidationEntity(in *Validation) *Validation {
	if in == nil {
		return nil
	}
	claim := &Claim{Validations: []*Validation{in}}
	clone := CloneClaimEntity(claim)
	if clone == nil || len(clone.Validations) == 0 {
		return nil
	}
	return clone.Validations[0]
}
