package claims

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type GenerateClaimActionOptions struct {
	IdempotencyKey                 string
	AllowMissingSubject            bool
	Reason                         string
	SuppressGeneratedNotifications bool
}

type ClaimPostOptions struct {
	AllowMissingSubject bool
	AllowSelfTarget     bool
	Reason              string
}

type ClaimPostPolicyRequest struct {
	Claim      *Claim
	ActorID    string
	Subject    AgentRef
	Evaluators []AgentRef
}

type ClaimPostPolicyDecision struct {
	Allowed     bool
	Reason      string
	FailureKind string
}

type ClaimPostPolicy interface {
	DecideClaimPost(ctx context.Context, req ClaimPostPolicyRequest) ClaimPostPolicyDecision
}

type ClaimPostPolicyFunc func(ctx context.Context, req ClaimPostPolicyRequest) ClaimPostPolicyDecision

func (f ClaimPostPolicyFunc) DecideClaimPost(ctx context.Context, req ClaimPostPolicyRequest) ClaimPostPolicyDecision {
	if f == nil {
		return ClaimPostPolicyDecision{Allowed: true}
	}
	return f(ctx, req)
}

type GenerateTestamentActionOptions struct {
	IdempotencyKey                 string
	AllowStandalone                bool
	AllowEmptyArtifactReference    bool
	Reason                         string
	SuppressGeneratedNotifications bool
}

type TestamentPostOptions struct {
	Reason string
}

type LifecycleFailureOptions struct {
	Reason       string
	ArtifactKind string
	Metadata     map[string]any
}

const (
	lifecycleFailureKindKey   = "lifecycle_failure_kind"
	lifecycleFailureReasonKey = "lifecycle_failure_reason"
	lifecycleFailureStatusKey = "lifecycle_failure_status"
	lifecycleFailureClaimKey  = "claim_id"
)

type lifecyclePostError struct {
	reason       string
	artifactKind string
	cause        error
}

func (e lifecyclePostError) Error() string {
	return e.reason
}

func (e lifecyclePostError) Unwrap() error {
	return e.cause
}

type GeneratedClaimAction struct {
	Action Action
	Claims []Claim
}

type GeneratedTestamentAction struct {
	Action     Action
	Testaments []Testament
}

func (b *ClaimsBoard) GenerateClaimAction(ctx context.Context, action Action, inputClaims []Claim, opts GenerateClaimActionOptions) (*GeneratedClaimAction, error) {
	if len(inputClaims) == 0 {
		return nil, fmt.Errorf("generated action must contain at least one claim")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	if existing := b.generatedClaimActionByKeyLocked(opts.IdempotencyKey); existing != nil {
		b.mu.Unlock()
		return existing, nil
	}
	now := time.Now().UTC()
	if err := b.validateGenerateClaimActionLocked(action, inputClaims, opts); err != nil {
		b.mu.Unlock()
		return nil, err
	}
	prevSeq := b.seq.Load()
	b.stampGeneratedClaimActionLocked(&action, now, opts)
	for i := range inputClaims {
		b.stampGeneratedClaimLocked(&inputClaims[i], &action, now, opts)
	}
	if err := b.appendDurableEventLocked(walEventClaimActionGenerated, action.AgentID, map[string]any{
		"action": action, "claims": inputClaims,
	}, b.outboxRecordsForClaimLifecycleLocked(inputClaims, ClaimLifecycleGenerated, now)); err != nil {
		b.seq.Store(prevSeq)
		b.mu.Unlock()
		return nil, err
	}
	b.storeGeneratedClaimActionLocked(&action, inputClaims)
	result := generatedClaimActionSnapshot(&action, inputClaims)
	b.invalidateProjectionCache()
	b.mu.Unlock()
	b.projectDurableOutbox(ctx)
	if !opts.SuppressGeneratedNotifications && b.shouldEmitCanonicalDirect() {
		for i := range result.Claims {
			claim := &result.Claims[i]
			b.amplifier.dispatchCanonical(ctx, b.amplifier.buildClaimLifecycleDeltas(ctx, &result.Action, claim, ClaimLifecycleGenerated, result.Action.AgentID, now))
		}
	}
	if !opts.SuppressGeneratedNotifications {
		b.notifyLifecycleGenerated(inputClaims)
		b.notifySubscribers()
	}
	return result, nil
}

func (b *ClaimsBoard) GenerateClaim(ctx context.Context, actionID string, claim Claim, opts GenerateClaimActionOptions) (*Claim, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	if existing := b.generatedClaimByKeyLocked(opts.IdempotencyKey); existing != nil {
		b.mu.Unlock()
		return existing, nil
	}
	action, ok := b.actions[strings.TrimSpace(actionID)]
	if !ok {
		b.mu.Unlock()
		return nil, fmt.Errorf("action %q not found", actionID)
	}
	now := time.Now().UTC()
	if err := b.validateGeneratedClaimLocked(action.Type, &claim, opts); err != nil {
		b.mu.Unlock()
		return nil, err
	}
	if err := b.ensureUniqueClaimIDInBatchLocked([]Claim{claim}, 0); err != nil {
		b.mu.Unlock()
		return nil, err
	}
	prevSeq := b.seq.Load()
	b.stampGeneratedClaimLocked(&claim, action, now, opts)
	actionSnapshot := cloneActionEntity(action)
	if err := b.appendDurableEventLocked(walEventClaimActionGenerated, action.AgentID, map[string]any{
		"action": actionSnapshot, "claims": []Claim{claim},
	}, b.outboxRecordsForClaimLifecycleLocked([]Claim{claim}, ClaimLifecycleGenerated, now)); err != nil {
		b.seq.Store(prevSeq)
		b.mu.Unlock()
		return nil, err
	}
	b.storeGeneratedClaimsLocked(action, []Claim{claim}, opts.IdempotencyKey)
	result := CloneClaimEntity(&claim)
	b.invalidateProjectionCache()
	b.mu.Unlock()
	b.projectDurableOutbox(ctx)
	if !opts.SuppressGeneratedNotifications && b.shouldEmitCanonicalDirect() {
		b.amplifier.dispatchCanonical(ctx, b.amplifier.buildClaimLifecycleDeltas(ctx, &actionSnapshot, result, ClaimLifecycleGenerated, actionSnapshot.AgentID, now))
	}
	if !opts.SuppressGeneratedNotifications {
		b.notifyLifecycleGenerated([]Claim{claim})
		b.notifySubscribers()
	}
	return result, nil
}

func (b *ClaimsBoard) PostGeneratedClaim(ctx context.Context, claimID, actorID string, opts ClaimPostOptions) error {
	return b.PostGeneratedClaims(ctx, []string{claimID}, actorID, opts)
}

func (b *ClaimsBoard) PostGeneratedClaims(ctx context.Context, claimIDs []string, actorID string, opts ClaimPostOptions) error {
	if len(claimIDs) == 0 {
		return fmt.Errorf("no generated claims supplied")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	now := time.Now().UTC()
	claims, err := b.claimsForLifecyclePostLocked(claimIDs)
	if err != nil {
		b.mu.Unlock()
		return err
	}
	if err := b.validateClaimsPostableLocked(ctx, claims, actorID, opts); err != nil {
		failureOpts := lifecycleFailureOptionsFromError(err)
		commitErr := b.failClaimsLifecycleLocked(claims, actorID, ClaimLifecyclePostFailed, failureOpts, now)
		snapshots := cloneClaims(claims)
		b.mu.Unlock()
		if commitErr != nil {
			return commitErr
		}
		b.projectDurableOutbox(ctx)
		if b.shouldEmitCanonicalDirect() {
			for _, snapshot := range snapshots {
				b.amplifier.dispatchCanonical(ctx, b.amplifier.buildClaimLifecycleDeltas(ctx, actionForClaimRecord(b, snapshot), snapshot, ClaimLifecyclePostFailed, actorID, now))
			}
		}
		b.notifySubscribers()
		return err
	}
	if allClaimsLifecyclePosted(claims) {
		b.mu.Unlock()
		return nil
	}
	prevSeq := b.seq.Load()
	if err := b.appendDurableEventLocked(walEventClaimLifecycleTransition, actorID, map[string]any{
		"claim_ids": claimIDs, "to": ClaimLifecyclePosted, "agent_id": actorID, "reason": lifecycleReason(opts.Reason, "claim posted for action"), "changed": now,
	}, b.outboxRecordsForClaimLifecyclePtrLocked(claims, ClaimLifecyclePosted, now)); err != nil {
		b.seq.Store(prevSeq)
		b.mu.Unlock()
		return err
	}
	for _, claim := range claims {
		b.transitionClaimLifecycleLocked(claim, ClaimLifecyclePosted, actorID, lifecycleReason(opts.Reason, "claim posted for action"), now)
	}
	snapshots := cloneClaims(claims)
	actions := b.actionsForClaimsLocked(claims)
	b.invalidateProjectionCache()
	b.mu.Unlock()
	b.projectDurableOutbox(ctx)
	for i := range snapshots {
		action := actions[i]
		if action == nil {
			continue
		}
		if b.shouldEmitFabricDirect() {
			b.amplifier.EmitClaimIssued(ctx, snapshots[i])
		}
		if b.amplifier != nil {
			b.amplifier.PublishInboxDeltas(ctx, action, snapshots[i])
		}
		b.notifyDelta(BoardMutationDelta{
			Kind:    "claim_posted",
			ClaimID: snapshots[i].ID,
			AgentID: SubjectAgentID(snapshots[i].Relations),
		})
	}
	b.notifySubscribers()
	return nil
}

func (b *ClaimsBoard) GenerateTestamentAction(ctx context.Context, action Action, input []Testament, opts GenerateTestamentActionOptions) (*GeneratedTestamentAction, error) {
	if len(input) == 0 {
		return nil, fmt.Errorf("generated testament action must contain at least one testament")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	if existing := b.generatedTestamentActionByKeyLocked(opts.IdempotencyKey); existing != nil {
		b.mu.Unlock()
		return existing, nil
	}
	now := time.Now().UTC()
	if err := b.validateGenerateTestamentActionLocked(action, input, opts); err != nil {
		b.mu.Unlock()
		return nil, err
	}
	prevSeq := b.seq.Load()
	b.stampGeneratedTestamentActionLocked(&action, now, opts)
	for i := range input {
		b.stampGeneratedTestamentLocked(&input[i], &action, now, opts)
	}
	if err := b.appendDurableEventLocked(walEventTestamentActionGenerated, action.AgentID, map[string]any{
		"action": action, "testaments": input,
	}, b.outboxRecordsForTestamentLifecycleLocked(input, TestamentLifecycleGenerated, now)); err != nil {
		b.seq.Store(prevSeq)
		b.mu.Unlock()
		return nil, err
	}
	b.storeGeneratedTestamentActionLocked(&action, input)
	result := generatedTestamentActionSnapshot(&action, input)
	b.invalidateProjectionCache()
	b.mu.Unlock()
	b.projectDurableOutbox(ctx)
	if !opts.SuppressGeneratedNotifications && b.shouldEmitCanonicalDirect() {
		for i := range result.Testaments {
			testament := &result.Testaments[i]
			claim, _ := b.CloneClaim(ClaimIDFromRelations(testament.Relations))
			b.amplifier.dispatchCanonical(ctx, b.amplifier.buildTestamentLifecycleDeltas(ctx, testament, claim, TestamentLifecycleGenerated, result.Action.AgentID, now))
		}
	}
	if !opts.SuppressGeneratedNotifications {
		b.notifySubscribers()
	}
	return result, nil
}

func (b *ClaimsBoard) PostGeneratedTestament(ctx context.Context, testamentID, actorID string, opts TestamentPostOptions) error {
	return b.PostGeneratedTestaments(ctx, []string{testamentID}, actorID, opts)
}

func (b *ClaimsBoard) PostGeneratedTestaments(ctx context.Context, testamentIDs []string, actorID string, opts TestamentPostOptions) error {
	if len(testamentIDs) == 0 {
		return fmt.Errorf("no generated testaments supplied")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	now := time.Now().UTC()
	testaments, err := b.testamentsForLifecyclePostLocked(testamentIDs)
	if err != nil {
		b.mu.Unlock()
		return err
	}
	if allTestamentsLifecyclePosted(testaments) {
		b.mu.Unlock()
		return nil
	}
	prevSeq := b.seq.Load()
	if err := b.appendDurableEventLocked(walEventTestamentLifecycleTransition, actorID, map[string]any{
		"testament_ids": testamentIDs, "to": TestamentLifecyclePosted, "agent_id": actorID, "reason": lifecycleReason(opts.Reason, "testament posted"), "changed": now,
	}, b.outboxRecordsForGeneratedTestamentPostLocked(testaments, now)); err != nil {
		b.seq.Store(prevSeq)
		b.mu.Unlock()
		return err
	}
	resolutions := make([]claimResolution, len(testaments))
	for i, testament := range testaments {
		b.transitionTestamentLifecycleLocked(testament, TestamentLifecyclePosted, actorID, lifecycleReason(opts.Reason, "testament posted"), now)
		resolutions[i] = b.recordClaimTestamentGeneratedLocked(testament, now)
	}
	testamentSnapshots := cloneTestaments(testaments)
	b.invalidateProjectionCache()
	b.mu.Unlock()
	b.projectDurableOutbox(ctx)
	b.publishPostedTestaments(ctx, testamentSnapshots, resolutions)
	b.notifySubscribers()
	return nil
}

func (b *ClaimsBoard) AcknowledgeClaimReceipt(ctx context.Context, claimID, receiverID string) error {
	return b.transitionClaimLifecycle(ctx, claimID, receiverID, ClaimLifecycleReceived, "claim received")
}

func (b *ClaimsBoard) AcknowledgeTestamentReceipt(ctx context.Context, testamentID, receiverID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	t, ok := b.testaments[strings.TrimSpace(testamentID)]
	if !ok {
		b.mu.Unlock()
		return fmt.Errorf("testament %q not found", testamentID)
	}
	claimID := ClaimIDFromRelations(t.Relations)
	claim := b.claims[claimID]
	if !b.canAcknowledgeTestamentReceipt(ctx, claim, receiverID) {
		b.mu.Unlock()
		return fmt.Errorf("receiver %q cannot acknowledge testament %q for claim %q", receiverID, t.ID, claimID)
	}
	if t.LifecycleStatus == TestamentLifecycleReceived {
		b.mu.Unlock()
		return nil
	}
	if !CanTransitionTestamentLifecycle(t.LifecycleStatus, TestamentLifecycleReceived) {
		b.mu.Unlock()
		return newTestamentLifecycleTransitionError(t.ID, t.LifecycleStatus, TestamentLifecycleReceived, "testament receipt requires a posted testament")
	}
	now := time.Now().UTC()
	outboxRecords := b.outboxRecordsForTestamentLifecyclePtrLocked([]*Testament{t}, TestamentLifecycleReceived, now)
	if claim != nil && CanTransitionClaimLifecycle(claim.LifecycleStatus, ClaimLifecycleTestamentAcknowledged) {
		outboxRecords = append(outboxRecords, b.outboxRecordsForClaimLifecyclePtrLocked([]*Claim{claim}, ClaimLifecycleTestamentAcknowledged, now)...)
	}
	if err := b.appendDurableEventLocked(walEventTestamentLifecycleTransition, receiverID, map[string]any{
		"testament_ids": []string{t.ID}, "to": TestamentLifecycleReceived, "agent_id": receiverID, "reason": "testament received", "changed": now,
	}, outboxRecords); err != nil {
		b.mu.Unlock()
		return err
	}
	b.transitionTestamentLifecycleLocked(t, TestamentLifecycleReceived, receiverID, "testament received", now)
	claimTransitioned := false
	if claim != nil && CanTransitionClaimLifecycle(claim.LifecycleStatus, ClaimLifecycleTestamentAcknowledged) {
		claimTransitioned = b.transitionClaimLifecycleLocked(claim, ClaimLifecycleTestamentAcknowledged, receiverID, "testament acknowledged", now)
	}
	testamentSnapshot := CloneTestamentEntity(t)
	claimSnapshot := CloneClaimEntity(claim)
	b.invalidateProjectionCache()
	b.mu.Unlock()
	b.projectDurableOutbox(ctx)
	if b.shouldEmitCanonicalDirect() {
		b.amplifier.dispatchCanonical(ctx, b.amplifier.buildTestamentLifecycleDeltas(ctx, testamentSnapshot, claimSnapshot, TestamentLifecycleReceived, receiverID, now))
		if claimTransitioned {
			b.amplifier.dispatchCanonical(ctx, b.amplifier.buildClaimLifecycleDeltas(ctx, actionForClaimRecord(b, claimSnapshot), claimSnapshot, ClaimLifecycleTestamentAcknowledged, receiverID, now))
		}
	}
	b.notifyTestamentLifecycleDelta(testamentSnapshot, TestamentLifecycleReceived, receiverID)
	if claimTransitioned {
		b.notifyClaimLifecycleDelta(claimSnapshot, ClaimLifecycleTestamentAcknowledged, receiverID, testamentSnapshot.ID)
	}
	b.notifySubscribers()
	return nil
}

func (b *ClaimsBoard) AcknowledgeClaimTestament(ctx context.Context, claimID, testamentID, receiverID string) error {
	if strings.TrimSpace(testamentID) != "" {
		return b.AcknowledgeTestamentReceipt(ctx, testamentID, receiverID)
	}
	return b.transitionClaimLifecycle(ctx, claimID, receiverID, ClaimLifecycleTestamentAcknowledged, "testament acknowledged")
}

func (b *ClaimsBoard) RecordClaimLifecycleFailure(ctx context.Context, claimID, actorID string, to ClaimLifecycleStatus, opts LifecycleFailureOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !to.IsFailure() {
		return fmt.Errorf("claim lifecycle target %q is not a failure status", to)
	}
	b.mu.Lock()
	claim, ok := b.claims[strings.TrimSpace(claimID)]
	if !ok {
		b.mu.Unlock()
		return fmt.Errorf("claim %q not found", claimID)
	}
	if claim.LifecycleStatus == to {
		b.mu.Unlock()
		return nil
	}
	if !CanTransitionClaimLifecycle(claim.LifecycleStatus, to) {
		b.mu.Unlock()
		return newClaimLifecycleTransitionError(claim.ID, claim.LifecycleStatus, to, "claim lifecycle failure target is not reachable")
	}
	now := time.Now().UTC()
	if err := b.failClaimsLifecycleLocked([]*Claim{claim}, actorID, to, opts, now); err != nil {
		b.mu.Unlock()
		return err
	}
	snapshot := CloneClaimEntity(claim)
	b.mu.Unlock()
	b.projectDurableOutbox(ctx)
	if b.shouldEmitCanonicalDirect() {
		b.amplifier.dispatchCanonical(ctx, b.amplifier.buildClaimLifecycleDeltas(ctx, actionForClaimRecord(b, snapshot), snapshot, to, actorID, now))
	}
	b.notifyClaimLifecycleDelta(snapshot, to, actorID, "")
	b.notifySubscribers()
	return nil
}

func (b *ClaimsBoard) RecordClaimGenerationFailure(ctx context.Context, claimID, actorID string, opts LifecycleFailureOptions) error {
	return b.RecordClaimLifecycleFailure(ctx, claimID, actorID, ClaimLifecycleGenerationFailed, opts)
}

func (b *ClaimsBoard) RecordClaimPostFailure(ctx context.Context, claimID, actorID string, opts LifecycleFailureOptions) error {
	return b.RecordClaimLifecycleFailure(ctx, claimID, actorID, ClaimLifecyclePostFailed, opts)
}

func (b *ClaimsBoard) RecordClaimReceiptFailure(ctx context.Context, claimID, actorID string, opts LifecycleFailureOptions) error {
	return b.RecordClaimLifecycleFailure(ctx, claimID, actorID, ClaimLifecycleReceiptFailed, opts)
}

func (b *ClaimsBoard) RecordClaimProgressFailure(ctx context.Context, claimID, actorID string, opts LifecycleFailureOptions) error {
	return b.RecordClaimLifecycleFailure(ctx, claimID, actorID, ClaimLifecycleProgressFailed, opts)
}

func (b *ClaimsBoard) RecordClaimTestamentGenerationFailure(ctx context.Context, claimID, actorID string, opts LifecycleFailureOptions) error {
	return b.RecordClaimLifecycleFailure(ctx, claimID, actorID, ClaimLifecycleTestamentGenerationFailed, opts)
}

func (b *ClaimsBoard) RecordClaimTestamentAcknowledgementFailure(ctx context.Context, claimID, actorID string, opts LifecycleFailureOptions) error {
	return b.RecordClaimLifecycleFailure(ctx, claimID, actorID, ClaimLifecycleTestamentAcknowledgementFailed, opts)
}

func (b *ClaimsBoard) RecordClaimValidationError(ctx context.Context, claimID, actorID string, opts LifecycleFailureOptions) error {
	return b.RecordClaimLifecycleFailure(ctx, claimID, actorID, ClaimLifecycleValidationErrored, opts)
}

func (b *ClaimsBoard) BeginTestamentValidation(ctx context.Context, testamentID, actorID string) error {
	return b.transitionTestamentValidation(ctx, testamentID, actorID, TestamentLifecycleValidating, "testament validation started")
}

func (b *ClaimsBoard) CompleteTestamentValidation(ctx context.Context, testamentID, actorID string, to TestamentLifecycleStatus, reason string) error {
	if !isTerminalTestamentValidationStatus(to) {
		return fmt.Errorf("testament lifecycle target %q is not a terminal validation status", to)
	}
	if to == TestamentLifecycleValidationErrored {
		return b.CompleteTestamentValidationError(ctx, testamentID, actorID, LifecycleFailureOptions{
			Reason:       lifecycleReason(reason, string(to)),
			ArtifactKind: ArtifactKindErrorDiagnostic,
		})
	}
	return b.transitionTestamentValidation(ctx, testamentID, actorID, to, lifecycleReason(reason, string(to)))
}

func (b *ClaimsBoard) CompleteTestamentValidationError(ctx context.Context, testamentID, actorID string, opts LifecycleFailureOptions) error {
	claimID, err := b.claimIDForReachableTestamentValidation(ctx, testamentID, TestamentLifecycleValidationErrored)
	if err != nil {
		return err
	}
	if claimID != "" {
		if err := b.RecordClaimValidationError(ctx, claimID, actorID, opts); err != nil {
			return err
		}
	}
	return b.transitionTestamentValidation(ctx, testamentID, actorID, TestamentLifecycleValidationErrored, lifecycleReason(opts.Reason, string(TestamentLifecycleValidationErrored)))
}

func (b *ClaimsBoard) claimIDForReachableTestamentValidation(ctx context.Context, testamentID string, to TestamentLifecycleStatus) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	testament, ok := b.testaments[strings.TrimSpace(testamentID)]
	if !ok {
		return "", fmt.Errorf("testament %q not found", testamentID)
	}
	if testament.LifecycleStatus != to && !CanTransitionTestamentLifecycle(testament.LifecycleStatus, to) {
		return "", newTestamentLifecycleTransitionError(testament.ID, testament.LifecycleStatus, to, "testament validation target is not reachable")
	}
	return ClaimIDFromRelations(testament.Relations), nil
}

func completeReceiptOnlyTestamentValidation(ctx context.Context, board *ClaimsBoard, testamentID, actorID, reason string) error {
	if board == nil {
		return fmt.Errorf("claims board is required")
	}
	testament, ok := board.CloneTestament(testamentID)
	if !ok {
		return fmt.Errorf("testament %q not found", testamentID)
	}
	claim, ok := board.CloneClaim(ClaimIDFromRelations(testament.Relations))
	if !ok {
		return fmt.Errorf("testament %q parent claim not found", testamentID)
	}
	receiptValidationIDs, err := receiptOnlyValidationIDs(claim)
	if err != nil {
		return err
	}
	if err := board.AcknowledgeTestamentReceipt(ctx, testamentID, actorID); err != nil {
		return err
	}
	if err := board.BeginTestamentValidation(ctx, testamentID, actorID); err != nil {
		return err
	}
	for _, validationID := range receiptValidationIDs {
		if err := board.EvaluateValidation(ctx, claim.ID, validationID, StatusChange{AgentID: actorID, To: string(ValidationStatusPassed), Reason: lifecycleReason(reason, "receipt validation passed")}); err != nil {
			return err
		}
	}
	return board.CompleteTestamentValidation(ctx, testamentID, actorID, TestamentLifecycleValidated, lifecycleReason(reason, "receipt-only testament validated"))
}

func receiptOnlyValidationIDs(claim *Claim) ([]string, error) {
	ids := make([]string, 0, len(claim.Validations))
	for _, validation := range claim.Validations {
		if validation == nil || !validation.Required {
			continue
		}
		if validation.Type != ValidationTypeReceipt {
			return nil, fmt.Errorf("claim %q has non-receipt required validation %q", claim.ID, validation.ID)
		}
		ids = append(ids, validation.ID)
	}
	return ids, nil
}

func (b *ClaimsBoard) transitionTestamentValidation(ctx context.Context, testamentID, actorID string, to TestamentLifecycleStatus, reason string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	testament, ok := b.testaments[strings.TrimSpace(testamentID)]
	if !ok {
		b.mu.Unlock()
		return fmt.Errorf("testament %q not found", testamentID)
	}
	if testament.LifecycleStatus == to {
		b.mu.Unlock()
		return nil
	}
	if !CanTransitionTestamentLifecycle(testament.LifecycleStatus, to) {
		b.mu.Unlock()
		return newTestamentLifecycleTransitionError(testament.ID, testament.LifecycleStatus, to, "testament validation target is not reachable")
	}
	now := time.Now().UTC()
	outboxRecords := b.outboxRecordsForTestamentLifecyclePtrLocked([]*Testament{testament}, to, now)
	if claim := b.claims[ClaimIDFromRelations(testament.Relations)]; claim != nil {
		if claimTo := claimLifecycleForTestamentValidation(to); claimTo != "" && CanTransitionClaimLifecycle(claim.LifecycleStatus, claimTo) {
			outboxRecords = append(outboxRecords, b.outboxRecordsForClaimLifecyclePtrLocked([]*Claim{claim}, claimTo, now)...)
		}
	}
	if err := b.appendDurableEventLocked(walEventTestamentLifecycleTransition, actorID, map[string]any{
		"testament_ids": []string{testament.ID}, "to": to, "agent_id": actorID, "reason": reason, "changed": now,
	}, outboxRecords); err != nil {
		b.mu.Unlock()
		return err
	}
	b.transitionTestamentLifecycleLocked(testament, to, actorID, reason, now)
	claimTransitioned := b.syncClaimLifecycleForTestamentValidationLocked(testament, to, actorID, reason, now)
	testamentSnapshot := CloneTestamentEntity(testament)
	claimSnapshot := CloneClaimEntity(b.claims[ClaimIDFromRelations(testament.Relations)])
	b.invalidateProjectionCache()
	b.mu.Unlock()
	b.projectDurableOutbox(ctx)
	if b.shouldEmitCanonicalDirect() {
		b.amplifier.dispatchCanonical(ctx, b.amplifier.buildTestamentLifecycleDeltas(ctx, testamentSnapshot, claimSnapshot, to, actorID, now))
		if claimTransitioned {
			claimStatus := claimLifecycleForTestamentValidation(to)
			b.amplifier.dispatchCanonical(ctx, b.amplifier.buildClaimLifecycleDeltas(ctx, actionForClaimRecord(b, claimSnapshot), claimSnapshot, claimStatus, actorID, now))
		}
	}
	b.notifyTestamentLifecycleDelta(testamentSnapshot, to, actorID)
	if claimTransitioned {
		b.notifyClaimLifecycleDelta(claimSnapshot, claimLifecycleForTestamentValidation(to), actorID, testamentSnapshot.ID)
	}
	b.notifySubscribers()
	return nil
}

func (b *ClaimsBoard) transitionClaimLifecycle(ctx context.Context, claimID, actorID string, to ClaimLifecycleStatus, reason string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	c, ok := b.claims[strings.TrimSpace(claimID)]
	if !ok {
		b.mu.Unlock()
		return fmt.Errorf("claim %q not found", claimID)
	}
	if err := b.validateLifecycleReceiver(ctx, c, actorID, to); err != nil {
		b.mu.Unlock()
		return err
	}
	if c.LifecycleStatus == to {
		b.mu.Unlock()
		return nil
	}
	now := time.Now().UTC()
	if !CanTransitionClaimLifecycle(c.LifecycleStatus, to) {
		b.mu.Unlock()
		return newClaimLifecycleTransitionError(c.ID, c.LifecycleStatus, to, "claim lifecycle target is not reachable")
	}
	if err := b.appendDurableEventLocked(walEventClaimLifecycleTransition, actorID, map[string]any{
		"claim_ids": []string{c.ID}, "to": to, "agent_id": actorID, "reason": reason, "changed": now,
	}, b.outboxRecordsForClaimLifecyclePtrLocked([]*Claim{c}, to, now)); err != nil {
		b.mu.Unlock()
		return err
	}
	b.transitionClaimLifecycleLocked(c, to, actorID, reason, now)
	snapshot := CloneClaimEntity(c)
	b.invalidateProjectionCache()
	b.mu.Unlock()
	b.projectDurableOutbox(ctx)
	if b.shouldEmitCanonicalDirect() {
		action := actionForClaimRecord(b, snapshot)
		b.amplifier.dispatchCanonical(ctx, b.amplifier.buildClaimLifecycleDeltas(ctx, action, snapshot, to, actorID, now))
	}
	b.notifyClaimLifecycleDelta(snapshot, to, actorID, "")
	b.notifySubscribers()
	return nil
}

func (b *ClaimsBoard) validateGenerateClaimActionLocked(action Action, claims []Claim, opts GenerateClaimActionOptions) error {
	if strings.TrimSpace(action.AgentID) == "" {
		return fmt.Errorf("generated claim action agent_id is required")
	}
	if !isKnownActionType(action.Type) {
		return fmt.Errorf("generated claim action type %q is unknown", action.Type)
	}
	for i := range claims {
		if err := b.validateGeneratedClaimLocked(action.Type, &claims[i], opts); err != nil {
			return err
		}
		if err := b.ensureUniqueClaimIDInBatchLocked(claims, i); err != nil {
			return err
		}
	}
	return nil
}

func (b *ClaimsBoard) validateGeneratedClaimLocked(_ ActionType, claim *Claim, opts GenerateClaimActionOptions) error {
	if strings.TrimSpace(claim.Title) == "" {
		return fmt.Errorf("generated claim title is required")
	}
	if strings.TrimSpace(claim.Description) == "" {
		return fmt.Errorf("generated claim %q description is required", firstNonEmpty(claim.ID, claim.Title))
	}
	if !opts.AllowMissingSubject && SubjectAgentID(claim.Relations) == "" {
		return fmt.Errorf("generated claim %q subject relation is required", firstNonEmpty(claim.ID, claim.Title))
	}
	if IssuerAgentID(claim.Relations) == "" {
		return fmt.Errorf("generated claim %q issuer relation is required", firstNonEmpty(claim.ID, claim.Title))
	}
	if err := ValidateExpectedToolCalls(claim.ExpectedToolCalls, nil); err != nil {
		return fmt.Errorf("claim %q expected tool calls: %w", firstNonEmpty(claim.ID, claim.Title), err)
	}
	return validateClaimValidationExpectedTools(claim)
}

func validateClaimValidationExpectedTools(claim *Claim) error {
	for _, validation := range claim.Validations {
		if validation == nil {
			return fmt.Errorf("claim %q has nil validation", firstNonEmpty(claim.ID, claim.Title))
		}
		if strings.TrimSpace(validation.ID) == "" {
			return fmt.Errorf("claim %q validation id is required", firstNonEmpty(claim.ID, claim.Title))
		}
		if strings.TrimSpace(validation.Description) == "" {
			return fmt.Errorf("claim %q validation description is required", firstNonEmpty(claim.ID, claim.Title))
		}
		if strings.TrimSpace(validation.QualityBar) == "" {
			return fmt.Errorf("claim %q validation %q quality bar is required", firstNonEmpty(claim.ID, claim.Title), firstNonEmpty(validation.ID, validation.Description))
		}
		if err := ValidateExpectedToolCalls(validation.ExpectedToolCalls, nil); err != nil {
			return fmt.Errorf("claim %q validation %q expected tool calls: %w", firstNonEmpty(claim.ID, claim.Title), firstNonEmpty(validation.ID, validation.Description), err)
		}
	}
	return nil
}

func (b *ClaimsBoard) ensureUniqueClaimIDInBatchLocked(claims []Claim, idx int) error {
	id := strings.TrimSpace(claims[idx].ID)
	if id == "" {
		return nil
	}
	if _, exists := b.claims[id]; exists {
		return fmt.Errorf("duplicate claim ID %q", id)
	}
	for j := 0; j < idx; j++ {
		if claims[j].ID == id {
			return fmt.Errorf("duplicate claim ID %q in batch", id)
		}
	}
	return nil
}

func (b *ClaimsBoard) validateClaimsPostableLocked(ctx context.Context, claims []*Claim, actorID string, opts ClaimPostOptions) error {
	for _, claim := range claims {
		if err := b.validateClaimPostableLocked(ctx, claim, actorID, opts); err != nil {
			return err
		}
	}
	return nil
}

func (b *ClaimsBoard) validateClaimPostableLocked(ctx context.Context, claim *Claim, actorID string, opts ClaimPostOptions) error {
	if claim == nil {
		return lifecyclePostError{reason: "nil claim", artifactKind: ArtifactKindErrorDiagnostic}
	}
	if claim.LifecycleStatus == ClaimLifecyclePosted {
		return nil
	}
	if claim.LifecycleStatus != ClaimLifecycleGenerated {
		cause := newClaimLifecycleTransitionError(claim.ID, claim.LifecycleStatus, ClaimLifecyclePosted, "claim posting requires generated lifecycle status")
		return lifecyclePostError{reason: cause.Error(), artifactKind: ArtifactKindErrorDiagnostic, cause: cause}
	}
	if !opts.AllowMissingSubject && SubjectAgentID(claim.Relations) == "" {
		return lifecyclePostError{reason: fmt.Sprintf("claim %q subject relation is required before post", claim.ID), artifactKind: ArtifactKindMissingDependency}
	}
	identity, err := b.resolveClaimPostIdentity(ctx, claim, opts)
	if err != nil {
		return err
	}
	if !opts.AllowSelfTarget && isPeerDirectedActionType(claim.ActionType) && identity.selfTargeted() {
		return lifecyclePostError{reason: fmt.Sprintf("claim %q invalid self-target for peer-directed action %q", claim.ID, claim.ActionType), artifactKind: ArtifactKindPolicyDenied}
	}
	if claim.ActionType == ActionTypeHandoff {
		if err := b.handoffEligibleForPostLocked(strings.TrimSpace(actorID), claim.ID); err != nil {
			return lifecyclePostError{reason: err.Error(), artifactKind: ArtifactKindPolicyDenied, cause: err}
		}
	}
	return b.applyClaimPostPolicy(ctx, claim, actorID, identity)
}

type claimPostIdentity struct {
	issuer     AgentRef
	subject    AgentRef
	evaluators []AgentRef
}

func (i claimPostIdentity) selfTargeted() bool {
	issuerKey := i.issuer.RouteKey()
	subjectKey := i.subject.RouteKey()
	return issuerKey != "" && issuerKey == subjectKey
}

func (b *ClaimsBoard) resolveClaimPostIdentity(ctx context.Context, claim *Claim, opts ClaimPostOptions) (claimPostIdentity, error) {
	issuerID := IssuerAgentID(claim.Relations)
	subjectID := SubjectAgentID(claim.Relations)
	issuer, err := b.resolveRequiredPostAgentRef(ctx, issuerID, "issuer", claim.ID)
	if err != nil {
		return claimPostIdentity{}, err
	}
	subject, err := b.resolveSubjectPostAgentRef(ctx, subjectID, claim.ID, opts)
	if err != nil {
		return claimPostIdentity{}, err
	}
	evaluators, err := b.resolveEvaluatorPostAgentRefs(ctx, claim.Relations, claim.ID)
	if err != nil {
		return claimPostIdentity{}, err
	}
	return claimPostIdentity{issuer: issuer, subject: subject, evaluators: evaluators}, nil
}

func (b *ClaimsBoard) resolveSubjectPostAgentRef(ctx context.Context, agentID, claimID string, opts ClaimPostOptions) (AgentRef, error) {
	if strings.TrimSpace(agentID) == "" && opts.AllowMissingSubject {
		return AgentRef{}, nil
	}
	return b.resolveRequiredPostAgentRef(ctx, agentID, "subject", claimID)
}

func (b *ClaimsBoard) resolveRequiredPostAgentRef(ctx context.Context, agentID, role, claimID string) (AgentRef, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return AgentRef{}, lifecyclePostError{reason: fmt.Sprintf("claim %q %s relation is required before post", claimID, role), artifactKind: ArtifactKindMissingDependency}
	}
	if b.agentRefResolver == nil {
		return DegradedAgentRef(agentID, "canonical identity resolver unavailable"), nil
	}
	ref, ok := b.agentRefResolver.ResolveAgentRef(ctx, b.sessionID, agentID)
	if !ok || ref.RouteKey() == "" || ref.Unresolved {
		return AgentRef{}, lifecyclePostError{reason: fmt.Sprintf("claim %q %s identity %q could not be resolved", claimID, role, agentID), artifactKind: ArtifactKindMissingDependency}
	}
	return ref.Normalized(), nil
}

func (b *ClaimsBoard) resolveEvaluatorPostAgentRefs(ctx context.Context, relations []Relation, claimID string) ([]AgentRef, error) {
	var refs []AgentRef
	for _, relation := range relations {
		if relation.RelatedType != RelatedTypeAgent || relation.Relationship != RelationshipEvaluator {
			continue
		}
		ref, err := b.resolveRequiredPostAgentRef(ctx, relation.Related, "evaluator", claimID)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func (b *ClaimsBoard) applyClaimPostPolicy(ctx context.Context, claim *Claim, actorID string, identity claimPostIdentity) error {
	if b.claimPostPolicy == nil {
		return nil
	}
	decision := b.claimPostPolicy.DecideClaimPost(ctx, ClaimPostPolicyRequest{
		Claim:      CloneClaimEntity(claim),
		ActorID:    actorID,
		Subject:    identity.subject,
		Evaluators: identity.evaluators,
	})
	if decision.Allowed {
		return nil
	}
	reason := strings.TrimSpace(decision.Reason)
	if reason == "" {
		reason = fmt.Sprintf("claim %q post denied by policy", claim.ID)
	}
	return lifecyclePostError{reason: reason, artifactKind: normalizeLifecycleFailureArtifactKind(decision.FailureKind)}
}

func (b *ClaimsBoard) claimsForLifecyclePostLocked(ids []string) ([]*Claim, error) {
	claims := make([]*Claim, 0, len(ids))
	for _, id := range ids {
		claim, ok := b.claims[strings.TrimSpace(id)]
		if !ok {
			return nil, fmt.Errorf("claim %q not found", id)
		}
		claims = append(claims, claim)
	}
	return claims, nil
}

func (b *ClaimsBoard) testamentsForLifecyclePostLocked(ids []string) ([]*Testament, error) {
	testaments := make([]*Testament, 0, len(ids))
	for _, id := range ids {
		testament, ok := b.testaments[strings.TrimSpace(id)]
		if !ok {
			return nil, fmt.Errorf("testament %q not found", id)
		}
		if testament.LifecycleStatus != TestamentLifecycleGenerated && testament.LifecycleStatus != TestamentLifecyclePosted {
			return nil, newTestamentLifecycleTransitionError(testament.ID, testament.LifecycleStatus, TestamentLifecyclePosted, "testament posting requires generated lifecycle status")
		}
		testaments = append(testaments, testament)
	}
	return testaments, nil
}

func (b *ClaimsBoard) failClaimsLifecycleLocked(claims []*Claim, actorID string, to ClaimLifecycleStatus, opts LifecycleFailureOptions, now time.Time) error {
	if !to.IsFailure() {
		return fmt.Errorf("claim lifecycle failure target %q is not a failure status", to)
	}
	reason := lifecycleReason(opts.Reason, string(to))
	action, testaments := b.lifecycleFailureTestamentsLocked(claims, actorID, to, opts, now)
	if err := b.appendDurableEventLocked(walEventClaimLifecycleTransition, actorID, map[string]any{
		"claim_ids": claimIDsFromPointers(claims), "to": to, "agent_id": actorID, "reason": reason, "changed": now,
		"failure_action": action, "failure_testaments": testaments,
	}, append(b.outboxRecordsForClaimLifecyclePtrLocked(claims, to, now), b.outboxRecordsForTestamentLifecycleLocked(testaments, TestamentLifecyclePosted, now)...)); err != nil {
		return err
	}
	if action != nil {
		b.actions[action.ID] = action
		b.indexRelations(action.ID, action.Relations)
	}
	for _, testament := range testaments {
		t := testament
		b.testaments[t.ID] = &t
		b.indexRelations(t.ID, t.Relations)
		for _, artifact := range t.Artifacts {
			if artifact != nil {
				b.indexRelations(artifact.ID, artifact.Relations)
			}
		}
	}
	for _, claim := range claims {
		b.transitionClaimLifecycleLocked(claim, to, actorID, reason, now)
		if !claim.Status.IsTerminal() {
			b.adjustStatusCounter(claim.Status, ClaimStatusRejected)
			claim.StatusHistory = append(claim.StatusHistory, StatusChange{From: string(claim.Status), To: string(ClaimStatusRejected), Reason: reason, AgentID: actorID, Changed: now})
			claim.Status = ClaimStatusRejected
		}
	}
	b.invalidateProjectionCache()
	return nil
}

func (b *ClaimsBoard) lifecycleFailureTestamentsLocked(claims []*Claim, actorID string, to ClaimLifecycleStatus, opts LifecycleFailureOptions, now time.Time) (*Action, []Testament) {
	if len(claims) == 0 {
		return nil, nil
	}
	action := &Action{ID: uuid.NewString(), AgentID: actorID, Type: ActionTypeTestament, Status: ActionStatusComplete, Created: now, Accessed: now, SessionID: b.sessionID, PipelineID: b.pipelineID, TaskID: b.taskID, Sequence: b.nextSeq()}
	testaments := make([]Testament, 0, len(claims))
	for _, claim := range claims {
		testaments = append(testaments, b.lifecycleFailureTestamentLocked(action.ID, claim, actorID, to, opts, now))
	}
	return action, testaments
}

func (b *ClaimsBoard) lifecycleFailureTestamentLocked(actionID string, claim *Claim, actorID string, to ClaimLifecycleStatus, opts LifecycleFailureOptions, now time.Time) Testament {
	testamentID := uuid.NewString()
	artifactID := uuid.NewString()
	reason := lifecycleReason(opts.Reason, string(to))
	metadata := lifecycleFailureMetadata(claim.ID, to, opts)
	return Testament{
		ID:               testamentID,
		AgentID:          actorID,
		SessionID:        b.sessionID,
		PipelineID:       b.pipelineID,
		TaskID:           b.taskID,
		Sequence:         b.nextSeq(),
		Created:          now,
		Accessed:         now,
		Summary:          reason,
		LifecycleStatus:  TestamentLifecyclePosted,
		LifecycleHistory: []StatusChange{{To: string(TestamentLifecycleGenerated), Reason: "failure testament generated", AgentID: actorID, Changed: now}, {From: string(TestamentLifecycleGenerated), To: string(TestamentLifecyclePosted), Reason: "failure testament posted", AgentID: actorID, Changed: now}},
		Relations: []Relation{
			{Related: actionID, RelatedType: RelatedTypeAction, Relationship: RelationshipTestamentAction},
			{Related: claim.ID, RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim},
			{Related: actorID, RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
		},
		Artifacts: []*Artifact{{
			ID:          artifactID,
			TestamentID: testamentID,
			AgentID:     actorID,
			SessionID:   b.sessionID,
			PipelineID:  b.pipelineID,
			TaskID:      b.taskID,
			Sequence:    b.nextSeq(),
			Created:     now,
			Accessed:    now,
			Kind:        normalizeLifecycleFailureArtifactKind(opts.ArtifactKind),
			Reference:   reason,
			Metadata:    metadata,
			Relations:   []Relation{{Related: claim.ID, RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim}},
		}},
	}
}

func (b *ClaimsBoard) validateGenerateTestamentActionLocked(action Action, testaments []Testament, opts GenerateTestamentActionOptions) error {
	if strings.TrimSpace(action.AgentID) == "" {
		return fmt.Errorf("generated testament action agent_id is required")
	}
	if action.Type != ActionTypeTestament {
		return fmt.Errorf("generated testament action type %q is invalid", action.Type)
	}
	for i := range testaments {
		testament := &testaments[i]
		if strings.TrimSpace(testament.Summary) == "" && strings.TrimSpace(testament.Context) == "" {
			return fmt.Errorf("generated testament summary or context is required")
		}
		claimID := ClaimIDFromRelations(testament.Relations)
		if !opts.AllowStandalone && claimID == "" {
			return fmt.Errorf("generated testament %q claim relation is required", firstNonEmpty(testament.ID, testament.Summary))
		}
		if claimID != "" {
			if _, exists := b.claims[claimID]; !exists {
				return fmt.Errorf("generated testament %q claim relation %q does not resolve to a board claim", firstNonEmpty(testament.ID, testament.Summary), claimID)
			}
		}
		if testament.ID != "" {
			if _, exists := b.testaments[testament.ID]; exists {
				return fmt.Errorf("duplicate testament ID %q", testament.ID)
			}
			for j := 0; j < i; j++ {
				if testaments[j].ID == testament.ID {
					return fmt.Errorf("duplicate testament ID %q in batch", testament.ID)
				}
			}
		}
		if err := validateGeneratedTestamentArtifacts(testament, opts); err != nil {
			return err
		}
	}
	return nil
}

func validateGeneratedTestamentArtifacts(testament *Testament, opts GenerateTestamentActionOptions) error {
	for _, artifact := range testament.Artifacts {
		if artifact == nil {
			return fmt.Errorf("generated testament %q has nil artifact", firstNonEmpty(testament.ID, testament.Summary))
		}
		if strings.TrimSpace(artifact.Kind) == "" {
			return fmt.Errorf("generated testament %q artifact kind is required", firstNonEmpty(testament.ID, testament.Summary))
		}
		if !opts.AllowEmptyArtifactReference && strings.TrimSpace(artifact.Reference) == "" && strings.TrimSpace(artifact.ContentHash) == "" {
			return fmt.Errorf("generated testament %q artifact %q requires reference or content hash", firstNonEmpty(testament.ID, testament.Summary), firstNonEmpty(artifact.ID, artifact.Kind))
		}
		if artifact.Size < 0 {
			return fmt.Errorf("generated testament %q artifact %q has negative size", firstNonEmpty(testament.ID, testament.Summary), firstNonEmpty(artifact.ID, artifact.Kind))
		}
	}
	return nil
}

func (b *ClaimsBoard) stampGeneratedClaimActionLocked(action *Action, now time.Time, opts GenerateClaimActionOptions) {
	if action.ID == "" {
		action.ID = uuid.NewString()
	}
	action.SessionID = b.sessionID
	action.PipelineID = b.pipelineID
	action.TaskID = b.taskID
	action.Sequence = b.nextSeq()
	action.Created = now
	action.Accessed = now
	action.IdempotencyKey = firstNonEmpty(opts.IdempotencyKey, action.IdempotencyKey)
	if action.Status == "" {
		action.Status = ActionStatusPending
	}
	action.StatusHistory = append(action.StatusHistory, StatusChange{To: string(action.Status), Reason: lifecycleReason(opts.Reason, "claim action generated"), AgentID: action.AgentID, Changed: now})
}

func (b *ClaimsBoard) stampGeneratedClaimLocked(claim *Claim, action *Action, now time.Time, opts GenerateClaimActionOptions) {
	if claim.ID == "" {
		claim.ID = uuid.NewString()
	}
	claim.SessionID = b.sessionID
	claim.PipelineID = b.pipelineID
	claim.TaskID = b.taskID
	claim.Sequence = b.nextSeq()
	claim.Created = now
	claim.Accessed = now
	claim.Status = ClaimStatusPending
	claim.ActionType = action.Type
	claim.IdempotencyKey = firstNonEmpty(opts.IdempotencyKey, claim.IdempotencyKey)
	if strings.TrimSpace(claim.Description) == "" {
		claim.Description = strings.TrimSpace(claim.Title)
	}
	claim.ExpectedToolCalls = stampExpectedToolCalls(claim.ExpectedToolCalls)
	claim.LifecycleStatus = ClaimLifecycleGenerated
	claim.LifecycleHistory = append(claim.LifecycleHistory, StatusChange{To: string(ClaimLifecycleGenerated), Reason: lifecycleReason(opts.Reason, "claim generated"), AgentID: action.AgentID, Changed: now})
	if !HasRelation(claim.Relations, RelationshipClaimAction, action.ID) {
		claim.Relations = append(claim.Relations, Relation{Related: action.ID, RelatedType: RelatedTypeAction, Relationship: RelationshipClaimAction})
	}
	b.stampValidationsLocked(claim, now)
}

func (b *ClaimsBoard) stampGeneratedTestamentActionLocked(action *Action, now time.Time, opts GenerateTestamentActionOptions) {
	if action.ID == "" {
		action.ID = uuid.NewString()
	}
	action.SessionID = b.sessionID
	action.PipelineID = b.pipelineID
	action.TaskID = b.taskID
	action.Sequence = b.nextSeq()
	action.Created = now
	action.Accessed = now
	action.IdempotencyKey = firstNonEmpty(opts.IdempotencyKey, action.IdempotencyKey)
	if action.Status == "" {
		action.Status = ActionStatusComplete
	}
	action.StatusHistory = append(action.StatusHistory, StatusChange{To: string(action.Status), Reason: lifecycleReason(opts.Reason, "testament action generated"), AgentID: action.AgentID, Changed: now})
}

func (b *ClaimsBoard) stampGeneratedTestamentLocked(testament *Testament, action *Action, now time.Time, opts GenerateTestamentActionOptions) {
	b.stampTestamentLocked(testament, action, now)
	testament.LifecycleStatus = TestamentLifecycleGenerated
	testament.LifecycleHistory = append(testament.LifecycleHistory, StatusChange{To: string(TestamentLifecycleGenerated), Reason: lifecycleReason(opts.Reason, "testament generated"), AgentID: action.AgentID, Changed: now})
	testament.IdempotencyKey = firstNonEmpty(opts.IdempotencyKey, testament.IdempotencyKey)
}

func (b *ClaimsBoard) storeGeneratedClaimActionLocked(action *Action, claims []Claim) {
	b.actions[action.ID] = action
	b.indexRelations(action.ID, action.Relations)
	if action.IdempotencyKey != "" {
		b.claimGenerationKeys[action.IdempotencyKey] = action.ID
	}
	b.storeGeneratedClaimsLocked(action, claims, "")
}

func (b *ClaimsBoard) storeGeneratedClaimsLocked(action *Action, claims []Claim, idempotencyKey string) {
	for i := range claims {
		claim := &claims[i]
		if _, exists := b.claims[claim.ID]; exists {
			continue
		}
		b.claims[claim.ID] = claim
		b.claimOrder = append(b.claimOrder, claim.ID)
		b.indexRelations(claim.ID, claim.Relations)
		b.relationsIdx.addScope(claim.ID, claim.Scope)
		b.countTotal.Add(1)
		b.countPending.Add(1)
		if strings.TrimSpace(idempotencyKey) != "" {
			b.singleClaimGenerationKeys[strings.TrimSpace(idempotencyKey)] = claim.ID
		}
	}
}

func (b *ClaimsBoard) storeGeneratedTestamentActionLocked(action *Action, testaments []Testament) {
	b.actions[action.ID] = action
	b.indexRelations(action.ID, action.Relations)
	if action.IdempotencyKey != "" {
		b.testamentGenerationKeys[action.IdempotencyKey] = action.ID
	}
	for i := range testaments {
		testament := &testaments[i]
		b.testaments[testament.ID] = testament
		b.indexRelations(testament.ID, testament.Relations)
		for _, artifact := range testament.Artifacts {
			if artifact != nil {
				b.indexRelations(artifact.ID, artifact.Relations)
			}
		}
	}
}

func (b *ClaimsBoard) transitionClaimLifecycleLocked(claim *Claim, to ClaimLifecycleStatus, agentID, reason string, now time.Time) bool {
	if claim == nil || claim.LifecycleStatus == to {
		return false
	}
	if !CanTransitionClaimLifecycle(claim.LifecycleStatus, to) {
		return false
	}
	change := StatusChange{From: string(claim.LifecycleStatus), To: string(to), Reason: reason, AgentID: agentID, Changed: now}
	claim.LifecycleHistory = capStatusHistory(append(claim.LifecycleHistory, change))
	claim.LifecycleStatus = to
	claim.Accessed = now
	return true
}

func (b *ClaimsBoard) transitionTestamentLifecycleLocked(testament *Testament, to TestamentLifecycleStatus, agentID, reason string, now time.Time) bool {
	if testament == nil || testament.LifecycleStatus == to {
		return false
	}
	if !CanTransitionTestamentLifecycle(testament.LifecycleStatus, to) {
		return false
	}
	change := StatusChange{From: string(testament.LifecycleStatus), To: string(to), Reason: reason, AgentID: agentID, Changed: now}
	testament.LifecycleHistory = capStatusHistory(append(testament.LifecycleHistory, change))
	testament.LifecycleStatus = to
	testament.Accessed = now
	return true
}

func (b *ClaimsBoard) outboxRecordsForClaimPostLocked(claims []*Claim, now time.Time) []ClaimsOutboxRecord {
	return b.outboxRecordsForClaimLifecyclePtrLocked(claims, ClaimLifecyclePosted, now)
}

func (b *ClaimsBoard) outboxRecordsForGeneratedTestamentPostLocked(testaments []*Testament, now time.Time) []ClaimsOutboxRecord {
	records := make([]ClaimsOutboxRecord, 0, len(testaments)*2)
	for _, testament := range testaments {
		records = append(records, b.outboxRecordLocked(testament.Sequence, "testament", testament.ID, string(DeltaActionTestamentPosted), now))
		if claim := b.claims[ClaimIDFromRelations(testament.Relations)]; claim != nil && CanTransitionClaimLifecycle(claim.LifecycleStatus, ClaimLifecycleTestamentGenerated) {
			records = append(records, b.outboxRecordLocked(claim.Sequence, "claim", claim.ID, string(DeltaActionClaimTestamentGenerated), now))
		}
		for _, artifact := range testament.Artifacts {
			if artifact != nil {
				records = append(records, b.outboxRecordLocked(artifact.Sequence, "artifact", artifact.ID, "artifact_published", now))
			}
		}
	}
	return records
}

func (b *ClaimsBoard) outboxRecordsForClaimLifecycleLocked(claims []Claim, status ClaimLifecycleStatus, now time.Time) []ClaimsOutboxRecord {
	action, ok := ClaimLifecycleDeltaAction(status)
	if !ok {
		return nil
	}
	records := make([]ClaimsOutboxRecord, 0, len(claims))
	for i := range claims {
		records = append(records, b.outboxRecordLocked(claims[i].Sequence, "claim", claims[i].ID, string(action), now))
	}
	return records
}

func (b *ClaimsBoard) outboxRecordsForClaimLifecyclePtrLocked(claims []*Claim, status ClaimLifecycleStatus, now time.Time) []ClaimsOutboxRecord {
	action, ok := ClaimLifecycleDeltaAction(status)
	if !ok {
		return nil
	}
	records := make([]ClaimsOutboxRecord, 0, len(claims))
	for _, claim := range claims {
		if claim != nil {
			records = append(records, b.outboxRecordLocked(claim.Sequence, "claim", claim.ID, string(action), now))
		}
	}
	return records
}

func (b *ClaimsBoard) outboxRecordsForTestamentLifecycleLocked(testaments []Testament, status TestamentLifecycleStatus, now time.Time) []ClaimsOutboxRecord {
	action, ok := TestamentLifecycleDeltaAction(status)
	if !ok {
		return nil
	}
	records := make([]ClaimsOutboxRecord, 0, len(testaments))
	for i := range testaments {
		records = append(records, b.outboxRecordLocked(testaments[i].Sequence, "testament", testaments[i].ID, string(action), now))
	}
	return records
}

func (b *ClaimsBoard) outboxRecordsForTestamentLifecyclePtrLocked(testaments []*Testament, status TestamentLifecycleStatus, now time.Time) []ClaimsOutboxRecord {
	action, ok := TestamentLifecycleDeltaAction(status)
	if !ok {
		return nil
	}
	records := make([]ClaimsOutboxRecord, 0, len(testaments))
	for _, testament := range testaments {
		if testament != nil {
			records = append(records, b.outboxRecordLocked(testament.Sequence, "testament", testament.ID, string(action), now))
		}
	}
	return records
}

func (b *ClaimsBoard) actionsForClaimsLocked(claims []*Claim) []*Action {
	actions := make([]*Action, len(claims))
	for i, claim := range claims {
		if actionID := ClaimActionID(claim.Relations); actionID != "" {
			actions[i] = b.actions[actionID]
		}
	}
	return actions
}

func (b *ClaimsBoard) generatedClaimActionByKeyLocked(key string) *GeneratedClaimAction {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	actionID := b.claimGenerationKeys[key]
	if actionID == "" {
		return nil
	}
	action, ok := b.actions[actionID]
	if !ok {
		return nil
	}
	claims := make([]Claim, 0)
	for _, id := range b.claimOrder {
		claim := b.claims[id]
		if claim != nil && ClaimActionID(claim.Relations) == actionID {
			claims = append(claims, *CloneClaimEntity(claim))
		}
	}
	return &GeneratedClaimAction{Action: *action, Claims: claims}
}

func (b *ClaimsBoard) generatedClaimByKeyLocked(key string) *Claim {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	claimID := b.singleClaimGenerationKeys[key]
	if claimID == "" {
		return nil
	}
	return CloneClaimEntity(b.claims[claimID])
}

func (b *ClaimsBoard) claimKeyBelongsToActionGenerationLocked(claim *Claim) bool {
	if claim == nil || claim.IdempotencyKey == "" {
		return false
	}
	action := b.actions[ClaimActionID(claim.Relations)]
	return action != nil && action.IdempotencyKey == claim.IdempotencyKey
}

func (b *ClaimsBoard) generatedTestamentActionByKeyLocked(key string) *GeneratedTestamentAction {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	actionID := b.testamentGenerationKeys[key]
	if actionID == "" {
		return nil
	}
	action, ok := b.actions[actionID]
	if !ok {
		return nil
	}
	var testaments []Testament
	for _, testament := range b.testaments {
		if testament != nil && HasRelation(testament.Relations, RelationshipTestamentAction, actionID) {
			testaments = append(testaments, *CloneTestamentEntity(testament))
		}
	}
	return &GeneratedTestamentAction{Action: *action, Testaments: testaments}
}

func generatedClaimActionSnapshot(action *Action, claims []Claim) *GeneratedClaimAction {
	out := &GeneratedClaimAction{Action: *action, Claims: make([]Claim, len(claims))}
	for i := range claims {
		out.Claims[i] = *CloneClaimEntity(&claims[i])
	}
	return out
}

func generatedTestamentActionSnapshot(action *Action, testaments []Testament) *GeneratedTestamentAction {
	out := &GeneratedTestamentAction{Action: *action, Testaments: make([]Testament, len(testaments))}
	for i := range testaments {
		out.Testaments[i] = *CloneTestamentEntity(&testaments[i])
	}
	return out
}

func cloneClaims(claims []*Claim) []*Claim {
	out := make([]*Claim, len(claims))
	for i, claim := range claims {
		out[i] = CloneClaimEntity(claim)
	}
	return out
}

func cloneTestaments(testaments []*Testament) []Testament {
	out := make([]Testament, len(testaments))
	for i, testament := range testaments {
		out[i] = *CloneTestamentEntity(testament)
	}
	return out
}

func allClaimsLifecyclePosted(claims []*Claim) bool {
	for _, claim := range claims {
		if claim == nil || claim.LifecycleStatus != ClaimLifecyclePosted {
			return false
		}
	}
	return true
}

func allTestamentsLifecyclePosted(testaments []*Testament) bool {
	for _, testament := range testaments {
		if testament == nil || testament.LifecycleStatus != TestamentLifecyclePosted {
			return false
		}
	}
	return true
}

func (b *ClaimsBoard) validateLifecycleReceiver(ctx context.Context, claim *Claim, actorID string, to ClaimLifecycleStatus) error {
	switch to {
	case ClaimLifecycleReceived:
		if b.canAcknowledgeClaimReceipt(ctx, claim, actorID) {
			return nil
		}
		return fmt.Errorf("receiver %q cannot acknowledge claim %q", actorID, claim.ID)
	case ClaimLifecycleTestamentAcknowledged:
		if b.canAcknowledgeClaimTestament(ctx, claim, actorID) {
			return nil
		}
		return fmt.Errorf("receiver %q cannot acknowledge testament for claim %q", actorID, claim.ID)
	default:
		return nil
	}
}

func (b *ClaimsBoard) canAcknowledgeClaimReceipt(ctx context.Context, claim *Claim, actorID string) bool {
	if claim == nil {
		return false
	}
	if isPeerDirectedActionType(claim.ActionType) &&
		b.agentRelationMatches(ctx, claim.Relations, RelationshipIssuer, actorID) &&
		b.agentRelationMatches(ctx, claim.Relations, RelationshipSubject, actorID) &&
		HandoffFromClaimID(claim.Relations) == "" {
		return false
	}
	return b.agentRelationMatches(ctx, claim.Relations, RelationshipSubject, actorID)
}

func (b *ClaimsBoard) canAcknowledgeClaimTestament(ctx context.Context, claim *Claim, actorID string) bool {
	if claim == nil {
		return false
	}
	return b.agentRelationMatches(ctx, claim.Relations, RelationshipIssuer, actorID) ||
		b.agentRelationMatches(ctx, claim.Relations, RelationshipEvaluator, actorID)
}

func (b *ClaimsBoard) canAcknowledgeTestamentReceipt(ctx context.Context, claim *Claim, actorID string) bool {
	return b.canAcknowledgeClaimTestament(ctx, claim, actorID)
}

func (b *ClaimsBoard) agentRelationMatches(ctx context.Context, relations []Relation, relationship, agentID string) bool {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return false
	}
	for _, relation := range relations {
		if relation.RelatedType != RelatedTypeAgent || relation.Relationship != relationship {
			continue
		}
		if b.agentRefResolver == nil && strings.TrimSpace(relation.Related) == agentID {
			return true
		}
		if b.agentRefsMatch(ctx, relation.Related, agentID) {
			return true
		}
	}
	return false
}

func (b *ClaimsBoard) agentRefsMatch(ctx context.Context, leftID, rightID string) bool {
	left, okLeft := b.resolveAgentRefForMatch(ctx, leftID)
	right, okRight := b.resolveAgentRefForMatch(ctx, rightID)
	if !okLeft || !okRight {
		return false
	}
	left = left.Normalized()
	right = right.Normalized()
	if left.UID != "" && right.UID != "" {
		return left.UID == right.UID
	}
	return left.RouteKey() != "" && left.RouteKey() == right.RouteKey()
}

func (b *ClaimsBoard) resolveAgentRefForMatch(ctx context.Context, agentID string) (AgentRef, bool) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || b == nil || b.agentRefResolver == nil {
		return AgentRef{}, false
	}
	ref, ok := b.agentRefResolver.ResolveAgentRef(ctx, b.sessionID, agentID)
	if !ok || ref.RouteKey() == "" || ref.Unresolved {
		return AgentRef{}, false
	}
	return ref.Normalized(), true
}

func isKnownActionType(actionType ActionType) bool {
	switch actionType {
	case ActionTypeTask,
		ActionTypeChallenge,
		ActionTypeConsultation,
		ActionTypeCorrective,
		ActionTypeArchival,
		ActionTypePrompt,
		ActionTypeTestament,
		ActionTypeBoot,
		ActionTypeActivation,
		ActionTypeShutdown,
		ActionTypeHandoff,
		ActionTypeCheckpoint,
		ActionTypeGuardianCheck,
		ActionTypeConsultContinuation:
		return true
	default:
		return false
	}
}

func claimIDsFromPointers(claims []*Claim) []string {
	ids := make([]string, 0, len(claims))
	for _, claim := range claims {
		if claim != nil {
			ids = append(ids, claim.ID)
		}
	}
	return ids
}

func (b *ClaimsBoard) notifyLifecycleGenerated(claims []Claim) {
	for i := range claims {
		b.notifyDelta(BoardMutationDelta{Kind: "claim_generated", ClaimID: claims[i].ID, AgentID: SubjectAgentID(claims[i].Relations)})
	}
}

func (b *ClaimsBoard) publishPostedTestaments(ctx context.Context, testaments []Testament, resolutions []claimResolution) {
	for i := range testaments {
		if b.shouldEmitFabricDirect() {
			b.amplifier.EmitTestamentSubmitted(ctx, &testaments[i])
			for _, artifact := range testaments[i].Artifacts {
				b.amplifier.EmitArtifactPublished(ctx, artifact)
			}
		}
		b.publishTestamentResolution(ctx, &testaments[i], resolutionAt(resolutions, i))
		b.notifyDelta(BoardMutationDelta{Kind: "testament_posted", TestamentID: testaments[i].ID, ClaimID: claimIDFromTestament(&testaments[i]), AgentID: testaments[i].AgentID})
		if resolution := resolutionAt(resolutions, i); resolution.claim != nil {
			b.notifyClaimLifecycleDelta(resolution.claim, ClaimLifecycleTestamentGenerated, testaments[i].AgentID, testaments[i].ID)
		}
	}
}

func (b *ClaimsBoard) notifyClaimLifecycleDelta(claim *Claim, status ClaimLifecycleStatus, agentID, testamentID string) {
	if b == nil || claim == nil {
		return
	}
	action, ok := ClaimLifecycleDeltaAction(status)
	if !ok {
		return
	}
	b.notifyDelta(BoardMutationDelta{
		Kind:        lifecycleBoardMutationKind(action),
		ClaimID:     claim.ID,
		TestamentID: testamentID,
		AgentID:     agentID,
	})
}

func (b *ClaimsBoard) notifyTestamentLifecycleDelta(testament *Testament, status TestamentLifecycleStatus, agentID string) {
	if b == nil || testament == nil {
		return
	}
	action, ok := TestamentLifecycleDeltaAction(status)
	if !ok {
		return
	}
	b.notifyDelta(BoardMutationDelta{
		Kind:        lifecycleBoardMutationKind(action),
		ClaimID:     claimIDFromTestament(testament),
		TestamentID: testament.ID,
		AgentID:     agentID,
	})
}

func lifecycleBoardMutationKind(action DeltaAction) string {
	return strings.ReplaceAll(string(action), ".", "_")
}

func (b *ClaimsBoard) publishTestamentResolution(ctx context.Context, testament *Testament, resolution claimResolution) {
	if resolution.claim == nil {
		return
	}
	if b.amplifier == nil {
		return
	}
	b.amplifier.PublishTestamentDelta(ctx, testament, resolution.claim)
	for _, validation := range resolution.validations {
		change := StatusChange{From: validation.from, To: validation.to, Reason: validation.reason, AgentID: validation.agentID, Changed: validation.changed}
		b.amplifier.PublishCanonicalValidationEvaluated(ctx, resolution.claim, validation.validation, change, resolution.claim.Status == ClaimStatusAccepted, validation.changed)
	}
	for _, transition := range resolution.transitions {
		b.amplifier.PublishClaimStatusDelta(ctx, ClaimStatusDelta{SessionID: b.sessionID, BoardID: b.boardID, ClaimID: resolution.claim.ID, Sequence: resolution.claim.Sequence, EmittedAt: transition.changed, ActionKind: resolution.claim.ActionType, FromStatus: transition.from, ToStatus: transition.to, Reason: transition.reason, AgentID: transition.agentID, SubjectAgentID: SubjectAgentID(resolution.claim.Relations), IssuerAgentID: IssuerAgentID(resolution.claim.Relations)})
	}
}

func resolutionAt(resolutions []claimResolution, index int) claimResolution {
	if index < 0 || index >= len(resolutions) {
		return claimResolution{}
	}
	return resolutions[index]
}

func cloneActionEntity(action *Action) Action {
	if action == nil {
		return Action{}
	}
	clone := *action
	if len(action.Relations) > 0 {
		clone.Relations = append([]Relation(nil), action.Relations...)
	}
	if len(action.StatusHistory) > 0 {
		clone.StatusHistory = append([]StatusChange(nil), action.StatusHistory...)
	}
	return clone
}

func lifecycleFailureOptionsFromError(err error) LifecycleFailureOptions {
	if err == nil {
		return LifecycleFailureOptions{}
	}
	opts := LifecycleFailureOptions{Reason: err.Error(), ArtifactKind: ArtifactKindErrorDiagnostic}
	if postErr, ok := err.(lifecyclePostError); ok {
		opts.Reason = postErr.reason
		opts.ArtifactKind = postErr.artifactKind
	}
	return opts
}

func normalizeLifecycleFailureArtifactKind(kind string) string {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return ArtifactKindErrorDiagnostic
	}
	if isErrorArtifactKind(kind) {
		return kind
	}
	return ArtifactKindErrorDiagnostic
}

func lifecycleFailureMetadata(claimID string, to ClaimLifecycleStatus, opts LifecycleFailureOptions) map[string]any {
	metadata := cloneAnyMap(opts.Metadata)
	if metadata == nil {
		metadata = make(map[string]any)
	}
	metadata[lifecycleFailureClaimKey] = claimID
	metadata[lifecycleFailureStatusKey] = string(to)
	metadata[lifecycleFailureKindKey] = normalizeLifecycleFailureArtifactKind(opts.ArtifactKind)
	metadata[lifecycleFailureReasonKey] = lifecycleReason(opts.Reason, string(to))
	return metadata
}

func isTerminalTestamentValidationStatus(status TestamentLifecycleStatus) bool {
	return status == TestamentLifecycleValidated ||
		status == TestamentLifecycleValidationIncomplete ||
		status == TestamentLifecycleValidationFailed ||
		status == TestamentLifecycleValidationErrored
}

func claimLifecycleForTestamentValidation(status TestamentLifecycleStatus) ClaimLifecycleStatus {
	switch status {
	case TestamentLifecycleValidating:
		return ClaimLifecycleValidating
	case TestamentLifecycleValidated:
		return ClaimLifecycleSatisfied
	case TestamentLifecycleValidationIncomplete:
		return ClaimLifecycleValidationIncomplete
	case TestamentLifecycleValidationFailed:
		return ClaimLifecycleValidationFailed
	case TestamentLifecycleValidationErrored:
		return ClaimLifecycleValidationErrored
	default:
		return ""
	}
}

func (b *ClaimsBoard) syncClaimLifecycleForTestamentValidationLocked(testament *Testament, status TestamentLifecycleStatus, actorID, reason string, now time.Time) bool {
	claimID := ClaimIDFromRelations(testament.Relations)
	claim := b.claims[claimID]
	to := claimLifecycleForTestamentValidation(status)
	if claim == nil || to == "" {
		return false
	}
	if b.transitionClaimLifecycleLocked(claim, to, actorID, reason, now) {
		b.setCoarseClaimStatusForLifecycleLocked(claim, to, actorID, reason, now)
		return true
	}
	return false
}

func (b *ClaimsBoard) setCoarseClaimStatusForLifecycleLocked(claim *Claim, lifecycle ClaimLifecycleStatus, actorID, reason string, now time.Time) {
	to, ok := coarseStatusForValidationLifecycle(lifecycle)
	if !ok || claim.Status == to || claim.Status.IsTerminal() {
		return
	}
	from := claim.Status
	claim.StatusHistory = append(claim.StatusHistory, StatusChange{From: string(from), To: string(to), Reason: reason, AgentID: actorID, Changed: now})
	claim.Status = to
	claim.Accessed = now
	b.adjustStatusCounter(from, to)
}

func coarseStatusForValidationLifecycle(lifecycle ClaimLifecycleStatus) (ClaimStatus, bool) {
	switch lifecycle {
	case ClaimLifecycleSatisfied:
		return ClaimStatusAccepted, true
	case ClaimLifecycleValidationIncomplete, ClaimLifecycleValidationFailed, ClaimLifecycleValidationErrored:
		return ClaimStatusRejected, true
	default:
		return "", false
	}
}

func lifecycleReason(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}
