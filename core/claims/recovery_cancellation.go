package claims

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	defaultServiceRecoveryScanLimit = 256
	serviceRecoveryActorID          = "sys:claims_recovery"
	claimCancellationActorID        = "sys:claims_cancellation"
)

type ServiceRecoveryOptions struct {
	Board               *ClaimsBoard
	Dispatchers         *ServiceDispatcherRegistry
	ValidatorDispatcher *BoardValidatorDispatcher
	Participants        []ParticipantRegistration
	Continuations       []PendingContinuationRecoverer
	ScanLimit           int
	ActorID             string
}

type ServiceRecoveryResult struct {
	Scanned                 int                         `json:"scanned"`
	Redispatched            int                         `json:"redispatched"`
	Failed                  int                         `json:"failed"`
	Skipped                 int                         `json:"skipped"`
	ValidationScanned       int                         `json:"validation_scanned"`
	ValidationRedispatched  int                         `json:"validation_redispatched"`
	ValidationFailed        int                         `json:"validation_failed"`
	ValidationSkipped       int                         `json:"validation_skipped"`
	ContinuationsRecovered  int                         `json:"continuations_recovered"`
	ContinuationRecovererOK int                         `json:"continuation_recoverer_ok"`
	Bounded                 bool                        `json:"bounded"`
	Outcomes                []ServiceRecoveryOutcome    `json:"outcomes,omitempty"`
	ValidationOutcomes      []ValidationRecoveryOutcome `json:"validation_outcomes,omitempty"`
}

type ServiceRecoveryOutcome struct {
	ClaimID        string             `json:"claim_id"`
	ParticipantUID string             `json:"participant_uid,omitempty"`
	Determinism    HandlerDeterminism `json:"determinism,omitempty"`
	Action         string             `json:"action"`
	Reason         string             `json:"reason,omitempty"`
}

type ValidationRecoveryOutcome struct {
	ClaimID      string             `json:"claim_id"`
	ValidationID string             `json:"validation_id"`
	ValidatorID  string             `json:"validator_id,omitempty"`
	Determinism  HandlerDeterminism `json:"determinism,omitempty"`
	Action       string             `json:"action"`
	Reason       string             `json:"reason,omitempty"`
}

//go:generate mockery --name=PendingContinuationRecoverer --output=./mocks --outpkg=mocks
type PendingContinuationRecoverer interface {
	RecoverPendingContinuations(context.Context) int
}

func RecoverServiceWork(ctx context.Context, opts ServiceRecoveryOptions) (ServiceRecoveryResult, error) {
	if opts.Board == nil {
		return ServiceRecoveryResult{}, fmt.Errorf("%w: board is required", ErrServiceDispatcherInvalid)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	result := ServiceRecoveryResult{}
	limit := positiveOrDefault(opts.ScanLimit, defaultServiceRecoveryScanLimit)
	participants := participantLookup(opts.Participants)
	for _, claim := range serviceRecoveryClaims(opts.Board.Projection(), participants) {
		if recoveryBudgetExhausted(result, limit) {
			result.Bounded = true
			break
		}
		result.Scanned++
		outcome := recoverServiceClaim(ctx, opts, claim, participants)
		result.Outcomes = append(result.Outcomes, outcome)
		incrementRecoveryCounts(&result, outcome.Action)
		if err := ctx.Err(); err != nil {
			return result, err
		}
	}
	if !recoveryBudgetExhausted(result, limit) {
		recoverValidationWork(ctx, opts, &result)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if !recoveryBudgetExhausted(result, limit) {
		recoverPendingContinuations(ctx, opts.Continuations, &result)
	}
	return result, nil
}

func recoverServiceClaim(ctx context.Context, opts ServiceRecoveryOptions, claim Claim, participants map[string]ParticipantRegistration) ServiceRecoveryOutcome {
	participant, ok := participants[strings.TrimSpace(SubjectAgentID(claim.Relations))]
	if !ok {
		return ServiceRecoveryOutcome{ClaimID: claim.ID, Action: "skipped", Reason: "service participant not registered"}
	}
	out := ServiceRecoveryOutcome{ClaimID: claim.ID, ParticipantUID: participant.UID, Determinism: participant.Determinism}
	if claim.LifecycleStatus == ClaimLifecycleGenerated {
		if err := opts.Board.PostGeneratedClaim(ctx, claim.ID, firstNonEmpty(IssuerAgentID(claim.Relations), opts.ActorID, serviceRecoveryActorID), ClaimPostOptions{Reason: "recovery reposted generated service claim"}); err != nil {
			return serviceRecoveryFailed(ctx, opts.Board, out, firstNonEmpty(opts.ActorID, serviceRecoveryActorID), err.Error())
		}
		if refreshed, ok := opts.Board.CloneClaim(claim.ID); ok {
			claim = *refreshed
		}
	}
	if claim.LifecycleStatus == ClaimLifecycleTestamentGenerated {
		return recoverGeneratedServiceTestament(ctx, opts, claim, participant, out)
	}
	if !serviceRecoveryMayRedispatch(opts.Board, claim, participant) {
		out.Action = "failed"
		out.Reason = serviceRecoveryUnsafeReason(claim, participant)
		_ = recordServiceRecoveryFailure(ctx, opts.Board, claim.ID, firstNonEmpty(opts.ActorID, serviceRecoveryActorID), out.Reason)
		return out
	}
	if opts.Dispatchers == nil {
		return serviceRecoveryFailed(ctx, opts.Board, out, firstNonEmpty(opts.ActorID, serviceRecoveryActorID), "service dispatcher registry not configured")
	}
	dispatcher, ok := opts.Dispatchers.Lookup(claim.SessionID, participant.UID)
	if !ok {
		out.Action = "failed"
		out.Reason = "service dispatcher not registered"
		_ = recordServiceRecoveryFailure(ctx, opts.Board, claim.ID, firstNonEmpty(opts.ActorID, serviceRecoveryActorID), out.Reason)
		return out
	}
	if err := dispatcher.DispatchDelta(ctx, serviceRecoveryClaimPostedDelta(opts.Board, &claim, participant)); err != nil {
		out.Action = "failed"
		out.Reason = err.Error()
		_ = recordServiceRecoveryFailure(ctx, opts.Board, claim.ID, firstNonEmpty(opts.ActorID, serviceRecoveryActorID), out.Reason)
		return out
	}
	out.Action = "redispatched"
	out.Reason = "deterministic service claim redispatched"
	return out
}

func serviceRecoveryFailed(ctx context.Context, board *ClaimsBoard, outcome ServiceRecoveryOutcome, actorID, reason string) ServiceRecoveryOutcome {
	outcome.Action = "failed"
	outcome.Reason = firstNonEmpty(reason, "service recovery failed")
	_ = recordServiceRecoveryFailure(ctx, board, outcome.ClaimID, actorID, outcome.Reason)
	return outcome
}

func recoverGeneratedServiceTestament(ctx context.Context, opts ServiceRecoveryOptions, claim Claim, participant ParticipantRegistration, outcome ServiceRecoveryOutcome) ServiceRecoveryOutcome {
	testament := firstGeneratedServiceTestament(opts.Board, claim.ID, participant.RouteKey)
	if testament == nil {
		return serviceRecoveryFailed(ctx, opts.Board, outcome, firstNonEmpty(opts.ActorID, serviceRecoveryActorID), "generated service testament not found")
	}
	if err := opts.Board.PostGeneratedTestament(ctx, testament.ID, participant.RouteKey, TestamentPostOptions{Reason: "recovery posted generated service testament"}); err != nil {
		return serviceRecoveryFailed(ctx, opts.Board, outcome, firstNonEmpty(opts.ActorID, serviceRecoveryActorID), err.Error())
	}
	if err := completeServiceTestamentValidation(ctx, opts.Board, testament.ID, firstNonEmpty(claimValidationActorID(&claim), participant.RouteKey)); err != nil {
		return serviceRecoveryFailed(ctx, opts.Board, outcome, firstNonEmpty(opts.ActorID, serviceRecoveryActorID), err.Error())
	}
	outcome.Action = "redispatched"
	outcome.Reason = "generated service testament posted and validated"
	return outcome
}

func firstGeneratedServiceTestament(board *ClaimsBoard, claimID, participantRoute string) *Testament {
	for _, testament := range board.TestamentsByClaim(claimID) {
		if strings.TrimSpace(testament.AgentID) == strings.TrimSpace(participantRoute) && testament.LifecycleStatus == TestamentLifecycleGenerated {
			return testament
		}
	}
	return nil
}

func serviceRecoveryClaims(proj *ClaimsBoardProjection, participants map[string]ParticipantRegistration) []Claim {
	if proj == nil || len(participants) == 0 {
		return nil
	}
	out := make([]Claim, 0)
	for _, claim := range proj.Claims {
		if claim.Status.IsTerminal() || claim.LifecycleStatus.IsTerminal() || claim.LifecycleStatus == ClaimLifecycleValidating {
			continue
		}
		if _, ok := participants[strings.TrimSpace(SubjectAgentID(claim.Relations))]; ok {
			out = append(out, claim)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Sequence != out[j].Sequence {
			return out[i].Sequence < out[j].Sequence
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func serviceRecoveryMayRedispatch(board *ClaimsBoard, claim Claim, participant ParticipantRegistration) bool {
	switch participant.Determinism {
	case HandlerDeterminismPure, HandlerDeterminismContent:
		return true
	case HandlerDeterminismSideEffect, HandlerDeterminismNondeterministic:
		return serviceRecoveryHasIdempotencyEvidence(board, claim, participant)
	default:
		return false
	}
}

func serviceRecoveryUnsafeReason(claim Claim, participant ParticipantRegistration) string {
	if !participant.Determinism.Valid() {
		return "service determinism contract invalid"
	}
	if participant.Determinism == HandlerDeterminismSideEffect || participant.Determinism == HandlerDeterminismNondeterministic {
		return "service recovery requires idempotency evidence"
	}
	return "service recovery policy denied redispatch"
}

func serviceRecoveryHasIdempotencyEvidence(board *ClaimsBoard, claim Claim, participant ParticipantRegistration) bool {
	if expectedToolIdempotencyNonce(claim.ExpectedToolCalls) != "" || expectedToolIdempotencyKey(claim.ExpectedToolCalls) != "" {
		return true
	}
	for _, relation := range claim.Relations {
		if relation.RelatedType == RelatedTypeIdempotencyKey && strings.TrimSpace(relation.Related) != "" {
			return true
		}
	}
	return claimHasRecoveryIdempotencyArtifact(board, claim.ID, participant)
}

func expectedToolIdempotencyKey(calls []ExpectedToolCall) string {
	for _, call := range calls {
		if key := stringArg(call.Arguments, "idempotency_key"); key != "" {
			return key
		}
	}
	return ""
}

func claimHasRecoveryIdempotencyArtifact(board *ClaimsBoard, claimID string, participant ParticipantRegistration) bool {
	if board == nil {
		return false
	}
	for _, testament := range board.TestamentsByClaim(claimID) {
		for _, artifact := range testament.Artifacts {
			if artifactProvesRecoveryIdempotency(artifact, participant) {
				return true
			}
		}
	}
	return generatedClaimArtifactProvesRecoveryIdempotency(board, claimID, participant)
}

func artifactProvesRecoveryIdempotency(artifact *Artifact, participant ParticipantRegistration) bool {
	if artifact == nil {
		return false
	}
	if artifact.DataType == ArtifactDataTypeRecoveryIdempotency {
		data, err := ArtifactData[RecoveryIdempotencyArtifactData](artifact)
		return err == nil &&
			data.Key != "" &&
			participantMatchesRecoveryEvidence(data.ParticipantID, participant) &&
			recoveryEvidenceNotExpired(data.ExpiresAt)
	}
	if artifact.Kind != ArtifactKindRecoveryIdempotency {
		return false
	}
	return participantMatchesRecoveryEvidence(firstNonEmpty(stringArg(artifact.Metadata, "participant_id"), stringArg(artifact.Metadata, "participant_uid"), stringArg(artifact.Metadata, "participant")), participant)
}

func generatedClaimArtifactProvesRecoveryIdempotency(board *ClaimsBoard, claimID string, participant ParticipantRegistration) bool {
	board.mu.RLock()
	defer board.mu.RUnlock()
	for _, artifact := range board.artifacts {
		if !artifactRelatedToClaim(artifact, claimID) {
			continue
		}
		if artifactProvesRecoveryIdempotency(artifact, participant) {
			return true
		}
	}
	return false
}

func artifactRelatedToClaim(artifact *Artifact, claimID string) bool {
	if artifact == nil || strings.TrimSpace(claimID) == "" {
		return false
	}
	for _, relation := range artifact.Relations {
		if relation.RelatedType == RelatedTypeClaim && strings.TrimSpace(relation.Related) == strings.TrimSpace(claimID) {
			return true
		}
	}
	return false
}

func participantMatchesRecoveryEvidence(value string, participant ParticipantRegistration) bool {
	value = strings.TrimSpace(value)
	return value != "" && (value == participant.UID || value == participant.RouteKey)
}

func recoveryEvidenceNotExpired(expiresAt time.Time) bool {
	return expiresAt.IsZero() || expiresAt.After(time.Now().UTC())
}

//go:generate mockery --name=ClaimWorkCanceller --output=./mocks --outpkg=mocks
type ClaimWorkCanceller interface {
	CancelClaimWork(ctx context.Context, claimID string, reason string) (bool, error)
}

func recordServiceRecoveryFailure(ctx context.Context, board *ClaimsBoard, claimID, actorID, reason string) error {
	if board == nil || strings.TrimSpace(claimID) == "" {
		return nil
	}
	return board.RecordClaimValidationError(ctx, claimID, actorID, LifecycleFailureOptions{
		Reason:       firstNonEmpty(reason, "service recovery failed"),
		ArtifactKind: ArtifactKindErrorDiagnostic,
		Metadata: map[string]any{
			"recovery": true,
			"claim_id": claimID,
		},
	})
}

func serviceRecoveryClaimPostedDelta(board *ClaimsBoard, claim *Claim, participant ParticipantRegistration) CanonicalDelta {
	claimID := ""
	if claim != nil {
		claimID = claim.ID
	}
	return NewCanonicalDelta(
		DeltaActionClaimPosted,
		board.SessionID(),
		board.BoardID(),
		board.HighWaterSequence(),
		time.Now().UTC(),
		participant.AgentRef(),
		[]DeltaRef{{Role: "claim", Type: RelatedTypeClaim, ID: claimID}},
		&DeltaDelivery{To: []AgentRef{participant.AgentRef()}, Relationship: RelationshipSubject},
		map[string]any{"claim": map[string]any{"id": claimID, "action": string(claim.ActionType)}},
	)
}

func participantLookup(participants []ParticipantRegistration) map[string]ParticipantRegistration {
	out := make(map[string]ParticipantRegistration, len(participants)*2)
	for _, participant := range participants {
		normalized, err := normalizeParticipantRegistration(participant)
		if err != nil {
			continue
		}
		out[normalized.UID] = normalized
		out[normalized.RouteKey] = normalized
	}
	return out
}

func incrementRecoveryCounts(result *ServiceRecoveryResult, action string) {
	switch action {
	case "redispatched":
		result.Redispatched++
	case "failed":
		result.Failed++
	default:
		result.Skipped++
	}
}

func recoverValidationWork(ctx context.Context, opts ServiceRecoveryOptions, result *ServiceRecoveryResult) {
	limit := positiveOrDefault(opts.ScanLimit, defaultServiceRecoveryScanLimit)
	for _, item := range validationRecoveryItems(opts.Board.Projection()) {
		if recoveryBudgetExhausted(*result, limit) {
			result.Bounded = true
			return
		}
		result.ValidationScanned++
		outcome := recoverValidationItem(ctx, opts, item)
		result.ValidationOutcomes = append(result.ValidationOutcomes, outcome)
		incrementValidationRecoveryCounts(result, outcome.Action)
		if ctx.Err() != nil {
			return
		}
	}
}

func recoveryBudgetExhausted(result ServiceRecoveryResult, limit int) bool {
	return result.Scanned+result.ValidationScanned >= limit
}

func recoverValidationItem(ctx context.Context, opts ServiceRecoveryOptions, item validationRecoveryItem) ValidationRecoveryOutcome {
	out := ValidationRecoveryOutcome{ClaimID: item.Claim.ID, ValidationID: item.Validation.ID}
	if opts.ValidatorDispatcher == nil {
		return validationRecoveryFailed(ctx, opts.Board, out, "validator dispatcher not registered")
	}
	reg, ok := opts.ValidatorDispatcher.registry.Lookup(&item.Claim, &item.Validation)
	if ok {
		out.ValidatorID = reg.ValidatorID
		out.Determinism = reg.Determinism
	}
	result, err := opts.ValidatorDispatcher.RecoverValidationByID(ctx, item.Claim.ID, item.Validation.ID)
	if err != nil {
		return validationRecoveryFailed(ctx, opts.Board, out, err.Error())
	}
	if result.Error != nil {
		return validationRecoveryFailed(ctx, nil, out, result.Error.Description)
	}
	out.Action = "redispatched"
	out.Reason = "recoverable validator dispatched"
	return out
}

func validationRecoveryFailed(ctx context.Context, board *ClaimsBoard, outcome ValidationRecoveryOutcome, reason string) ValidationRecoveryOutcome {
	outcome.Action = "failed"
	outcome.Reason = firstNonEmpty(reason, "validation recovery failed")
	_ = recordValidationRecoveryFailure(ctx, board, outcome.ClaimID, outcome.ValidationID, outcome.ValidatorID, outcome.Reason)
	return outcome
}

func validationRecoveryItems(proj *ClaimsBoardProjection) []validationRecoveryItem {
	if proj == nil {
		return nil
	}
	out := make([]validationRecoveryItem, 0)
	for _, claim := range proj.Claims {
		out = append(out, validationRecoveryItemsForClaim(claim)...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Claim.Sequence != out[j].Claim.Sequence {
			return out[i].Claim.Sequence < out[j].Claim.Sequence
		}
		return out[i].Validation.Sequence < out[j].Validation.Sequence
	})
	return out
}

type validationRecoveryItem struct {
	Claim      Claim
	Validation Validation
}

func validationRecoveryItemsForClaim(claim Claim) []validationRecoveryItem {
	if claim.Status.IsTerminal() || claim.LifecycleStatus.IsTerminal() {
		return nil
	}
	var out []validationRecoveryItem
	for _, validation := range claim.Validations {
		if validationRecoverable(validation) {
			out = append(out, validationRecoveryItem{Claim: claim, Validation: *validation})
		}
	}
	return out
}

func validationRecoverable(validation *Validation) bool {
	if validation == nil {
		return false
	}
	switch validation.Status {
	case ValidationStatusReady, ValidationStatusValidating, ValidationStatusValidatingQualityBar:
		return true
	default:
		return false
	}
}

func recordValidationRecoveryFailure(ctx context.Context, board *ClaimsBoard, claimID, validationID, validatorID, reason string) error {
	if board == nil {
		return nil
	}
	validation, claim, ok := board.CloneValidation(validationID)
	if !ok || claim == nil || claim.ID != strings.TrimSpace(claimID) {
		return nil
	}
	return recordValidationFailureForStatus(ctx, board, claim.ID, validation, firstNonEmpty(validatorID, serviceRecoveryActorID), firstNonEmpty(reason, "validation recovery failed"))
}

func recordValidationFailureForStatus(ctx context.Context, board *ClaimsBoard, claimID string, validation *Validation, actorID, reason string) error {
	if validation != nil && validation.Status == ValidationStatusReady {
		return board.EvaluateValidation(ctx, claimID, validation.ID, StatusChange{AgentID: actorID, To: string(validationRecoveryFailureStatus(validation, ValidationStatusErrored)), Reason: reason})
	}
	return board.CompleteValidationLifecycle(ctx, claimID, validation.ID, actorID, validationRecoveryFailureStatus(validation, ValidationStatusErrored), ValidationLifecycleOptions{
		Reason: reason,
		Error: &ValidationError{
			Category:    ValidationErrorCategoryDispatcher,
			Description: reason,
			Source:      DegradedAgentRef(actorID, "validation recovery"),
			OccurredAt:  time.Now().UTC(),
		},
	})
}

func incrementValidationRecoveryCounts(result *ServiceRecoveryResult, action string) {
	switch action {
	case "redispatched":
		result.ValidationRedispatched++
	case "failed":
		result.ValidationFailed++
	default:
		result.ValidationSkipped++
	}
}

func recoverPendingContinuations(ctx context.Context, recoverers []PendingContinuationRecoverer, result *ServiceRecoveryResult) {
	for _, recoverer := range recoverers {
		if recoverer == nil {
			continue
		}
		result.ContinuationsRecovered += recoverer.RecoverPendingContinuations(ctx)
		result.ContinuationRecovererOK++
	}
}

type ClaimCancellationOptions struct {
	Board               *ClaimsBoard
	Dispatchers         *ServiceDispatcherRegistry
	ValidatorDispatcher *BoardValidatorDispatcher
	WorkCancellers      []ClaimWorkCanceller
	RootClaimIDs        []string
	OriginClaimID       string
	ActorID             string
	Reason              string
	ScanLimit           int
}

type ClaimCancellationResult struct {
	OriginClaimID     string   `json:"origin_claim_id,omitempty"`
	CancelledClaimIDs []string `json:"cancelled_claim_ids,omitempty"`
	TerminalClaimIDs  []string `json:"terminal_claim_ids,omitempty"`
	MissingClaimIDs   []string `json:"missing_claim_ids,omitempty"`
	StuckClaimIDs     []string `json:"stuck_claim_ids,omitempty"`
	Bounded           bool     `json:"bounded"`
}

func CancelClaimTree(ctx context.Context, opts ClaimCancellationOptions) (ClaimCancellationResult, error) {
	if opts.Board == nil {
		return ClaimCancellationResult{}, fmt.Errorf("claim cancellation requires board")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	result := ClaimCancellationResult{OriginClaimID: strings.TrimSpace(opts.OriginClaimID)}
	proj := opts.Board.Projection()
	graph := cancellationGraph(proj)
	order, missing, bounded := cancellationOrder(opts.RootClaimIDs, graph, positiveOrDefault(opts.ScanLimit, defaultServiceRecoveryScanLimit))
	result.MissingClaimIDs = missing
	result.Bounded = bounded
	for _, claimID := range missing {
		if err := recordMissingCancellationRoot(ctx, opts, claimID); err != nil {
			result.StuckClaimIDs = appendUniqueString(result.StuckClaimIDs, claimID)
		}
	}
	for _, claimID := range order {
		claim, ok := graph.claims[claimID]
		if !ok || claim.Status.IsTerminal() || claim.LifecycleStatus.IsTerminal() {
			result.TerminalClaimIDs = append(result.TerminalClaimIDs, claimID)
			continue
		}
		cancelled := cancelActiveClaimWork(ctx, opts, &claim) > 0
		if err := recordCancellationFailure(ctx, opts, claimID, cancelled); err != nil {
			result.StuckClaimIDs = append(result.StuckClaimIDs, claimID)
			continue
		}
		result.CancelledClaimIDs = append(result.CancelledClaimIDs, claimID)
	}
	return result, ctx.Err()
}

func cancelActiveClaimWork(ctx context.Context, opts ClaimCancellationOptions, claim *Claim) int {
	cancelled := 0
	if opts.Dispatchers != nil {
		cancelled += opts.Dispatchers.CancelClaim(claim.SessionID, claim.ID)
	}
	if opts.ValidatorDispatcher != nil {
		cancelled += opts.ValidatorDispatcher.CancelClaimValidations(claim)
	}
	for _, canceller := range opts.WorkCancellers {
		if canceller == nil {
			continue
		}
		ok, _ := canceller.CancelClaimWork(ctx, claim.ID, firstNonEmpty(opts.Reason, "claim cancellation requested"))
		if ok {
			cancelled++
		}
	}
	return cancelled
}

type cancellationGraphSnapshot struct {
	claims   map[string]Claim
	children map[string][]string
}

func cancellationGraph(proj *ClaimsBoardProjection) cancellationGraphSnapshot {
	graph := cancellationGraphSnapshot{claims: map[string]Claim{}, children: map[string][]string{}}
	if proj == nil {
		return graph
	}
	for _, claim := range proj.Claims {
		graph.claims[claim.ID] = claim
		for _, rel := range claim.Relations {
			if rel.RelatedType == RelatedTypeClaim && cancellationRelationship(rel.Relationship) {
				parent := strings.TrimSpace(rel.Related)
				graph.children[parent] = appendUniqueString(graph.children[parent], claim.ID)
			}
		}
	}
	for parent := range graph.children {
		sort.Strings(graph.children[parent])
	}
	return graph
}

func cancellationRelationship(relationship string) bool {
	switch strings.TrimSpace(relationship) {
	case RelationshipCausedBy, RelationshipDependsOn:
		return true
	default:
		return false
	}
}

func cancellationOrder(rootIDs []string, graph cancellationGraphSnapshot, limit int) ([]string, []string, bool) {
	visited := make(map[string]bool, len(graph.claims))
	var order []string
	var missing []string
	bounded := false
	for _, rootID := range normalizeStringList(rootIDs) {
		if _, ok := graph.claims[rootID]; !ok {
			missing = appendUniqueString(missing, rootID)
			continue
		}
		before := len(order)
		order, bounded = appendCancellationPostOrder(rootID, graph, visited, order, limit)
		if bounded || len(order) == before && !visited[rootID] {
			break
		}
	}
	return order, missing, bounded
}

func appendCancellationPostOrder(rootID string, graph cancellationGraphSnapshot, visited map[string]bool, order []string, limit int) ([]string, bool) {
	if visited[rootID] {
		return order, false
	}
	if len(order) >= limit {
		return order, true
	}
	visited[rootID] = true
	for _, childID := range graph.children[rootID] {
		next, bounded := appendCancellationPostOrder(childID, graph, visited, order, limit)
		order = next
		if bounded {
			return order, true
		}
	}
	return append(order, rootID), false
}

func recordCancellationFailure(ctx context.Context, opts ClaimCancellationOptions, claimID string, contextCancelled bool) error {
	target := cancellationFailureTarget(opts.Board, claimID)
	if target == "" {
		return errors.New("claim cancellation target is unreachable")
	}
	return opts.Board.RecordClaimLifecycleFailure(ctx, claimID, firstNonEmpty(opts.ActorID, claimCancellationActorID), target, LifecycleFailureOptions{
		Reason:       firstNonEmpty(opts.Reason, "claim cancellation requested"),
		ArtifactKind: ArtifactKindInterrupted,
		Metadata: map[string]any{
			"origin_claim_id":   strings.TrimSpace(opts.OriginClaimID),
			"context_cancelled": contextCancelled,
		},
	})
}

func recordMissingCancellationRoot(ctx context.Context, opts ClaimCancellationOptions, claimID string) error {
	if opts.Board == nil || strings.TrimSpace(claimID) == "" {
		return nil
	}
	artifact := &Artifact{
		AgentID:      firstNonEmpty(opts.ActorID, claimCancellationActorID),
		ArtifactName: "cancellation_missing_root",
		Kind:         ArtifactKindErrorDiagnostic,
		Reference:    "cancellation root claim not found: " + strings.TrimSpace(claimID),
		Metadata: map[string]any{
			"missing_claim_id": strings.TrimSpace(claimID),
			"origin_claim_id":  strings.TrimSpace(opts.OriginClaimID),
		},
	}
	_, err := RecordInfrastructureEvidence(ctx, InfrastructureEvidenceOptions{
		Board:         opts.Board,
		ActorID:       firstNonEmpty(opts.ActorID, claimCancellationActorID),
		SubjectID:     firstNonEmpty(opts.ActorID, claimCancellationActorID),
		Operation:     "claim_cancellation_missing_root",
		ParentClaimID: strings.TrimSpace(opts.OriginClaimID),
		Artifact:      artifact,
	})
	return err
}

func cancellationFailureTarget(board *ClaimsBoard, claimID string) ClaimLifecycleStatus {
	claim, ok := board.CloneClaim(claimID)
	if !ok {
		return ""
	}
	switch claim.LifecycleStatus {
	case "", ClaimLifecycleGenerated:
		return ClaimLifecycleGenerationFailed
	case ClaimLifecyclePosted:
		return ClaimLifecycleProgressFailed
	default:
		if CanTransitionClaimLifecycle(claim.LifecycleStatus, ClaimLifecycleProgressFailed) {
			return ClaimLifecycleProgressFailed
		}
		if CanTransitionClaimLifecycle(claim.LifecycleStatus, ClaimLifecycleValidationErrored) {
			return ClaimLifecycleValidationErrored
		}
		return ""
	}
}

func positiveOrDefault(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func appendUniqueString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
