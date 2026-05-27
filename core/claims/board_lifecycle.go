package claims

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type GenerateClaimActionOptions struct {
	IdempotencyKey      string
	AllowMissingSubject bool
	Reason              string
}

type ClaimPostOptions struct {
	AllowMissingSubject bool
	AllowSelfTarget     bool
	Reason              string
}

type GenerateTestamentActionOptions struct {
	IdempotencyKey  string
	AllowStandalone bool
	Reason          string
}

type TestamentPostOptions struct {
	Reason string
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
	}, nil); err != nil {
		b.seq.Store(prevSeq)
		b.mu.Unlock()
		return nil, err
	}
	b.storeGeneratedClaimActionLocked(&action, inputClaims)
	result := generatedClaimActionSnapshot(&action, inputClaims)
	b.invalidateProjectionCache()
	b.mu.Unlock()
	b.notifyLifecycleGenerated(inputClaims)
	b.notifySubscribers()
	return result, nil
}

func (b *ClaimsBoard) GenerateClaim(ctx context.Context, actionID string, claim Claim, opts GenerateClaimActionOptions) (*Claim, error) {
	action, ok := b.CloneAction(strings.TrimSpace(actionID))
	if !ok {
		return nil, fmt.Errorf("action %q not found", actionID)
	}
	result, err := b.GenerateClaimAction(ctx, *action, []Claim{claim}, opts)
	if err != nil {
		return nil, err
	}
	if len(result.Claims) == 0 {
		return nil, fmt.Errorf("generated claim missing from result")
	}
	return &result.Claims[0], nil
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
	if err := b.validateClaimsPostableLocked(claims, opts); err != nil {
		commitErr := b.failClaimsPostLocked(claims, actorID, err.Error(), now)
		b.mu.Unlock()
		if commitErr != nil {
			return commitErr
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
	}, b.outboxRecordsForClaimPostLocked(claims, now)); err != nil {
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
		b.amplifier.PublishInboxDeltas(ctx, action, snapshots[i])
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
	if err := b.validateGenerateTestamentActionLocked(input, opts); err != nil {
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
	}, nil); err != nil {
		b.seq.Store(prevSeq)
		b.mu.Unlock()
		return nil, err
	}
	b.storeGeneratedTestamentActionLocked(&action, input)
	result := generatedTestamentActionSnapshot(&action, input)
	b.invalidateProjectionCache()
	b.mu.Unlock()
	b.notifySubscribers()
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
		resolutions[i] = b.resolveClaimForTestamentLocked(testament, now)
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
	if !canAcknowledgeTestamentReceipt(claim, receiverID) {
		b.mu.Unlock()
		return fmt.Errorf("receiver %q cannot acknowledge testament %q for claim %q", receiverID, t.ID, claimID)
	}
	if t.LifecycleStatus == TestamentLifecycleReceived {
		b.mu.Unlock()
		return nil
	}
	if !CanTransitionTestamentLifecycle(t.LifecycleStatus, TestamentLifecycleReceived) {
		b.mu.Unlock()
		return fmt.Errorf("cannot transition testament %q lifecycle from %q to %q", t.ID, t.LifecycleStatus, TestamentLifecycleReceived)
	}
	now := time.Now().UTC()
	if err := b.appendDurableEventLocked(walEventTestamentLifecycleTransition, receiverID, map[string]any{
		"testament_ids": []string{t.ID}, "to": TestamentLifecycleReceived, "agent_id": receiverID, "reason": "testament received", "changed": now,
	}, nil); err != nil {
		b.mu.Unlock()
		return err
	}
	b.transitionTestamentLifecycleLocked(t, TestamentLifecycleReceived, receiverID, "testament received", now)
	if claim != nil && CanTransitionClaimLifecycle(claim.LifecycleStatus, ClaimLifecycleTestamentAcknowledged) {
		b.transitionClaimLifecycleLocked(claim, ClaimLifecycleTestamentAcknowledged, receiverID, "testament acknowledged", now)
	}
	b.invalidateProjectionCache()
	b.mu.Unlock()
	b.notifySubscribers()
	return nil
}

func (b *ClaimsBoard) AcknowledgeClaimTestament(ctx context.Context, claimID, testamentID, receiverID string) error {
	if strings.TrimSpace(testamentID) != "" {
		return b.AcknowledgeTestamentReceipt(ctx, testamentID, receiverID)
	}
	return b.transitionClaimLifecycle(ctx, claimID, receiverID, ClaimLifecycleTestamentAcknowledged, "testament acknowledged")
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
	if c.LifecycleStatus == to {
		b.mu.Unlock()
		return nil
	}
	if err := validateLifecycleReceiver(c, actorID, to); err != nil {
		b.mu.Unlock()
		return err
	}
	now := time.Now().UTC()
	if !CanTransitionClaimLifecycle(c.LifecycleStatus, to) {
		b.mu.Unlock()
		return fmt.Errorf("cannot transition claim %q lifecycle from %q to %q", c.ID, c.LifecycleStatus, to)
	}
	if err := b.appendDurableEventLocked(walEventClaimLifecycleTransition, actorID, map[string]any{
		"claim_ids": []string{c.ID}, "to": to, "agent_id": actorID, "reason": reason, "changed": now,
	}, nil); err != nil {
		b.mu.Unlock()
		return err
	}
	b.transitionClaimLifecycleLocked(c, to, actorID, reason, now)
	b.invalidateProjectionCache()
	b.mu.Unlock()
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

func (b *ClaimsBoard) validateClaimsPostableLocked(claims []*Claim, opts ClaimPostOptions) error {
	for _, claim := range claims {
		if err := validateClaimPostable(claim, opts); err != nil {
			return err
		}
	}
	return nil
}

func validateClaimPostable(claim *Claim, opts ClaimPostOptions) error {
	if claim == nil {
		return fmt.Errorf("nil claim")
	}
	if claim.LifecycleStatus == ClaimLifecyclePosted {
		return nil
	}
	if claim.LifecycleStatus != ClaimLifecycleGenerated {
		return fmt.Errorf("claim %q lifecycle is %q, expected generated", claim.ID, claim.LifecycleStatus)
	}
	if !opts.AllowMissingSubject && SubjectAgentID(claim.Relations) == "" {
		return fmt.Errorf("claim %q subject relation is required before post", claim.ID)
	}
	if !opts.AllowSelfTarget && isPeerDirectedActionType(claim.ActionType) && claimHasSelfIssuerSubject(claim.Relations) {
		return fmt.Errorf("claim %q invalid self-target for peer-directed action %q", claim.ID, claim.ActionType)
	}
	return nil
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
			return nil, fmt.Errorf("testament %q lifecycle is %q, expected generated", testament.ID, testament.LifecycleStatus)
		}
		testaments = append(testaments, testament)
	}
	return testaments, nil
}

func (b *ClaimsBoard) failClaimsPostLocked(claims []*Claim, actorID, reason string, now time.Time) error {
	action, testaments := b.lifecycleFailureTestamentsLocked(claims, actorID, reason, now)
	if err := b.appendDurableEventLocked(walEventClaimLifecycleTransition, actorID, map[string]any{
		"claim_ids": claimIDsFromPointers(claims), "to": ClaimLifecyclePostFailed, "agent_id": actorID, "reason": reason, "changed": now,
		"failure_action": action, "failure_testaments": testaments,
	}, nil); err != nil {
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
		b.transitionClaimLifecycleLocked(claim, ClaimLifecyclePostFailed, actorID, reason, now)
		if !claim.Status.IsTerminal() {
			b.adjustStatusCounter(claim.Status, ClaimStatusRejected)
			claim.StatusHistory = append(claim.StatusHistory, StatusChange{From: string(claim.Status), To: string(ClaimStatusRejected), Reason: reason, AgentID: actorID, Changed: now})
			claim.Status = ClaimStatusRejected
		}
	}
	b.invalidateProjectionCache()
	return nil
}

func (b *ClaimsBoard) lifecycleFailureTestamentsLocked(claims []*Claim, actorID, reason string, now time.Time) (*Action, []Testament) {
	if len(claims) == 0 {
		return nil, nil
	}
	action := &Action{ID: uuid.NewString(), AgentID: actorID, Type: ActionTypeTestament, Status: ActionStatusComplete, Created: now, Accessed: now, SessionID: b.sessionID, PipelineID: b.pipelineID, TaskID: b.taskID, Sequence: b.nextSeq()}
	testaments := make([]Testament, 0, len(claims))
	for _, claim := range claims {
		testaments = append(testaments, b.lifecycleFailureTestamentLocked(action.ID, claim, actorID, reason, now))
	}
	return action, testaments
}

func (b *ClaimsBoard) lifecycleFailureTestamentLocked(actionID string, claim *Claim, actorID, reason string, now time.Time) Testament {
	testamentID := uuid.NewString()
	artifactID := uuid.NewString()
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
			Kind:        ArtifactKindErrorDiagnostic,
			Reference:   reason,
			Relations:   []Relation{{Related: claim.ID, RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim}},
		}},
	}
}

func (b *ClaimsBoard) validateGenerateTestamentActionLocked(testaments []Testament, opts GenerateTestamentActionOptions) error {
	for i := range testaments {
		testament := &testaments[i]
		if strings.TrimSpace(testament.Summary) == "" && strings.TrimSpace(testament.Context) == "" {
			return fmt.Errorf("generated testament summary or context is required")
		}
		if !opts.AllowStandalone && ClaimIDFromRelations(testament.Relations) == "" {
			return fmt.Errorf("generated testament %q claim relation is required", firstNonEmpty(testament.ID, testament.Summary))
		}
		if testament.ID != "" {
			if _, exists := b.testaments[testament.ID]; exists {
				return fmt.Errorf("duplicate testament ID %q", testament.ID)
			}
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
	for i := range claims {
		claim := &claims[i]
		b.claims[claim.ID] = claim
		b.claimOrder = append(b.claimOrder, claim.ID)
		b.indexRelations(claim.ID, claim.Relations)
		b.relationsIdx.addScope(claim.ID, claim.Scope)
		b.countTotal.Add(1)
		b.countPending.Add(1)
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

func (b *ClaimsBoard) transitionClaimLifecycleLocked(claim *Claim, to ClaimLifecycleStatus, agentID, reason string, now time.Time) {
	if claim == nil || claim.LifecycleStatus == to {
		return
	}
	change := StatusChange{From: string(claim.LifecycleStatus), To: string(to), Reason: reason, AgentID: agentID, Changed: now}
	claim.LifecycleHistory = append(claim.LifecycleHistory, change)
	claim.LifecycleStatus = to
	claim.Accessed = now
}

func (b *ClaimsBoard) transitionTestamentLifecycleLocked(testament *Testament, to TestamentLifecycleStatus, agentID, reason string, now time.Time) {
	if testament == nil || testament.LifecycleStatus == to {
		return
	}
	change := StatusChange{From: string(testament.LifecycleStatus), To: string(to), Reason: reason, AgentID: agentID, Changed: now}
	testament.LifecycleHistory = append(testament.LifecycleHistory, change)
	testament.LifecycleStatus = to
	testament.Accessed = now
}

func (b *ClaimsBoard) outboxRecordsForClaimPostLocked(claims []*Claim, now time.Time) []ClaimsOutboxRecord {
	records := make([]ClaimsOutboxRecord, 0, len(claims))
	for _, claim := range claims {
		records = append(records, b.outboxRecordLocked(claim.Sequence, "claim", claim.ID, "claim_issued", now))
	}
	return records
}

func (b *ClaimsBoard) outboxRecordsForGeneratedTestamentPostLocked(testaments []*Testament, now time.Time) []ClaimsOutboxRecord {
	records := make([]ClaimsOutboxRecord, 0, len(testaments))
	for _, testament := range testaments {
		records = append(records, b.outboxRecordLocked(testament.Sequence, "testament", testament.ID, walEventTestamentSubmitted, now))
		for _, artifact := range testament.Artifacts {
			if artifact != nil {
				records = append(records, b.outboxRecordLocked(artifact.Sequence, "artifact", artifact.ID, "artifact_published", now))
			}
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

func validateLifecycleReceiver(claim *Claim, actorID string, to ClaimLifecycleStatus) error {
	switch to {
	case ClaimLifecycleReceived:
		if canAcknowledgeClaimReceipt(claim, actorID) {
			return nil
		}
		return fmt.Errorf("receiver %q cannot acknowledge claim %q", actorID, claim.ID)
	case ClaimLifecycleTestamentAcknowledged:
		if canAcknowledgeClaimTestament(claim, actorID) {
			return nil
		}
		return fmt.Errorf("receiver %q cannot acknowledge testament for claim %q", actorID, claim.ID)
	default:
		return nil
	}
}

func canAcknowledgeClaimReceipt(claim *Claim, actorID string) bool {
	return hasAgentRelation(claim.Relations, RelationshipSubject, actorID)
}

func canAcknowledgeClaimTestament(claim *Claim, actorID string) bool {
	return hasAgentRelation(claim.Relations, RelationshipIssuer, actorID) ||
		hasAgentRelation(claim.Relations, RelationshipEvaluator, actorID)
}

func canAcknowledgeTestamentReceipt(claim *Claim, actorID string) bool {
	return claim != nil && canAcknowledgeClaimTestament(claim, actorID)
}

func hasAgentRelation(relations []Relation, relationship, agentID string) bool {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return false
	}
	for _, relation := range relations {
		if relation.RelatedType == RelatedTypeAgent &&
			relation.Relationship == relationship &&
			strings.TrimSpace(relation.Related) == agentID {
			return true
		}
	}
	return false
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
	}
}

func (b *ClaimsBoard) publishTestamentResolution(ctx context.Context, testament *Testament, resolution claimResolution) {
	if resolution.claim == nil {
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

func lifecycleReason(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}
