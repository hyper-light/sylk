package claims

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/google/uuid"
)

var (
	ErrServiceDispatcherInvalid = errors.New("service dispatcher invalid")
	ErrServiceDispatchOverflow  = errors.New("service dispatcher concurrency budget exhausted")
)

const serviceFailureStackLimitBytes = 64 * 1024

type ServiceClaimRequest struct {
	Board       *ClaimsBoard
	Claim       *Claim
	Delta       CanonicalDelta
	Participant ParticipantRegistration
}

type ServiceClaimResult struct {
	Summary   string
	Artifacts []*Artifact
	Metadata  map[string]any
}

//go:generate mockery --name=ServiceHandler --output=./mocks --outpkg=mocks
type ServiceHandler interface {
	HandleServiceClaim(ctx context.Context, req ServiceClaimRequest) (ServiceClaimResult, error)
}

type ServiceDispatcherConfig struct {
	Board       *ClaimsBoard
	Subscriber  DeltaSubscriber
	Scope       ScopeProvider
	Participant ParticipantRegistration
	Handler     ServiceHandler
	SessionID   string
}

type ServiceDispatcher struct {
	board       *ClaimsBoard
	subscriber  DeltaSubscriber
	scope       ScopeProvider
	participant ParticipantRegistration
	handler     ServiceHandler
	sessionID   string

	mu            sync.Mutex
	seen          map[string]struct{}
	seenOrder     []string
	inflight      chan struct{}
	subscriptions []DeltaSubscription
	started       bool
	closed        bool
}

func NewServiceDispatcher(cfg ServiceDispatcherConfig) (*ServiceDispatcher, error) {
	if err := validateServiceDispatcherConfig(cfg); err != nil {
		return nil, err
	}
	participant, err := normalizeParticipantRegistration(cfg.Participant)
	if err != nil {
		return nil, err
	}
	return &ServiceDispatcher{
		board:       cfg.Board,
		subscriber:  subscribeOrNoop(cfg.Subscriber),
		scope:       cfg.Scope,
		participant: participant,
		handler:     cfg.Handler,
		sessionID:   firstNonEmpty(strings.TrimSpace(cfg.SessionID), cfg.Board.SessionID()),
		seen:        make(map[string]struct{}, participant.QueueCapacity),
		inflight:    make(chan struct{}, participant.ConcurrencyBudget),
	}, nil
}

func (d *ServiceDispatcher) Start(ctx context.Context) error {
	if d == nil {
		return fmt.Errorf("%w: dispatcher is nil", ErrServiceDispatcherInvalid)
	}
	shouldStart, err := d.markStarting()
	if err != nil {
		return err
	}
	if !shouldStart {
		return nil
	}
	subs := make([]DeltaSubscription, 0, len(d.subscriptionTopics()))
	for _, topic := range d.subscriptionTopics() {
		sub, err := d.subscriber.SubscribeDelta(topic, func(delta Delta) { d.ingest(ctx, delta) })
		if err != nil {
			_ = unsubscribeAll(subs)
			d.resetStart()
			return err
		}
		subs = append(subs, sub)
	}
	if err := d.installSubscriptions(subs); err != nil {
		_ = unsubscribeAll(subs)
		return err
	}
	return nil
}

func (d *ServiceDispatcher) Close() error {
	if d == nil {
		return nil
	}
	return unsubscribeAll(d.closeSubscriptions())
}

func (d *ServiceDispatcher) Participant() ParticipantRegistration {
	if d == nil {
		return ParticipantRegistration{}
	}
	return cloneParticipantRegistration(d.participant)
}

func (d *ServiceDispatcher) SubscriptionTopics() []string {
	if d == nil {
		return nil
	}
	return d.subscriptionTopics()
}

func (d *ServiceDispatcher) DispatchDelta(ctx context.Context, delta CanonicalDelta) error {
	if err := d.acceptDelta(delta); err != nil {
		return err
	}
	if d.isClosed() {
		return fmt.Errorf("%w: dispatcher is closed", ErrServiceDispatcherInvalid)
	}
	if !d.remember(delta.Key) {
		return nil
	}
	if !d.acquire(delta) {
		return d.recordOverflow(ctx, delta)
	}
	if err := d.scope.Go("claims.service."+d.participant.RouteKey, d.participant.HandlerTimeout, func(runCtx context.Context) error {
		defer d.release()
		return d.invoke(runCtx, delta)
	}); err != nil {
		d.release()
		return err
	}
	return nil
}

func (d *ServiceDispatcher) ingest(ctx context.Context, delta Delta) {
	canonical, ok := delta.(CanonicalDelta)
	if !ok {
		return
	}
	_ = d.DispatchDelta(ctx, canonical)
}

func (d *ServiceDispatcher) invoke(ctx context.Context, delta CanonicalDelta) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = d.recordFailure(context.Background(), delta, ValidationErrorCategoryPanic, fmt.Sprintf("%v", recovered), debug.Stack())
		}
	}()
	claim, err := d.prepareClaim(ctx, delta)
	if err != nil {
		return err
	}
	result, err := d.handler.HandleServiceClaim(ctx, ServiceClaimRequest{Board: d.board, Claim: claim, Delta: delta, Participant: d.participant})
	if err != nil {
		return d.recordFailure(ctx, delta, ValidationErrorCategoryHandler, err.Error(), nil)
	}
	return d.postSuccess(ctx, claim, result)
}

func (d *ServiceDispatcher) prepareClaim(ctx context.Context, delta CanonicalDelta) (*Claim, error) {
	claim, ok := d.board.CloneClaim(delta.ClaimID())
	if !ok {
		return nil, fmt.Errorf("service claim %q not found", delta.ClaimID())
	}
	if err := d.board.AcknowledgeClaimReceipt(ctx, claim.ID, d.participant.RouteKey); err != nil {
		return nil, ignoreAlreadyProgressedClaim(err, d.board, claim.ID)
	}
	if err := d.board.UpdateClaimProgress(ctx, claim.ID, ClaimProgressUpdate{WorkSummary: "service handler running"}, d.participant.RouteKey); err != nil {
		return nil, ignoreAlreadyProgressedClaim(err, d.board, claim.ID)
	}
	updated, ok := d.board.CloneClaim(claim.ID)
	if !ok {
		return nil, fmt.Errorf("service claim %q disappeared", claim.ID)
	}
	return updated, nil
}

func (d *ServiceDispatcher) postSuccess(ctx context.Context, claim *Claim, result ServiceClaimResult) error {
	artifacts := normalizeServiceArtifacts(result.Artifacts, d.participant.RouteKey)
	testament := Testament{
		AgentID:    d.participant.RouteKey,
		Summary:    firstNonEmpty(strings.TrimSpace(result.Summary), "service claim completed"),
		Confidence: "deterministic",
		Relations:  []Relation{{Related: claim.ID, RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim}},
		Artifacts:  artifacts,
	}
	generated, err := d.board.GenerateTestamentAction(ctx, Action{AgentID: d.participant.RouteKey, Type: ActionTypeTestament, Status: ActionStatusComplete}, []Testament{testament}, GenerateTestamentActionOptions{
		IdempotencyKey: "service_dispatch:" + claim.ID + ":" + d.participant.UID,
		Reason:         "service handler testament generated",
	})
	if err != nil {
		return err
	}
	testamentID := generated.Testaments[0].ID
	if err := d.board.PostGeneratedTestament(ctx, testamentID, d.participant.RouteKey, TestamentPostOptions{Reason: "service handler testament posted"}); err != nil {
		return err
	}
	validatorID := d.serviceTestamentValidationActor(claim, testamentID)
	return completeServiceTestamentValidation(ctx, d.board, testamentID, validatorID)
}

func (d *ServiceDispatcher) serviceTestamentValidationActor(claim *Claim, testamentID string) string {
	if actorID := claimValidationActorID(claim); actorID != "" {
		return actorID
	}
	if refreshed, ok := d.board.CloneClaim(claim.ID); ok {
		if actorID := claimValidationActorID(refreshed); actorID != "" {
			return actorID
		}
	}
	if testament, ok := d.board.CloneTestament(testamentID); ok {
		if parent, ok := d.board.CloneClaim(ClaimIDFromRelations(testament.Relations)); ok {
			if actorID := claimValidationActorID(parent); actorID != "" {
				return actorID
			}
		}
	}
	return d.participant.RouteKey
}

func claimValidationActorID(claim *Claim) string {
	if claim == nil {
		return ""
	}
	return firstNonEmpty(IssuerAgentID(claim.Relations), EvaluatorAgentID(claim.Relations))
}

func (d *ServiceDispatcher) recordOverflow(ctx context.Context, delta CanonicalDelta) error {
	return d.recordFailure(ctx, delta, ValidationErrorCategoryDispatcher, ErrServiceDispatchOverflow.Error(), nil)
}

func (d *ServiceDispatcher) recordFailure(ctx context.Context, delta CanonicalDelta, category ValidationErrorCategory, reason string, stack []byte) error {
	metadata := map[string]any{
		"participant_uid": d.participant.UID,
		"delta_key":       delta.Key,
		"category":        string(category),
	}
	if len(stack) != 0 {
		metadata["stack"] = boundedServiceFailureStack(stack)
	}
	return d.board.RecordClaimValidationError(ctx, delta.ClaimID(), d.participant.RouteKey, LifecycleFailureOptions{
		Reason:       firstNonEmpty(reason, "service handler failed"),
		ArtifactKind: ArtifactKindErrorDiagnostic,
		Metadata:     metadata,
	})
}

func (d *ServiceDispatcher) acceptDelta(delta CanonicalDelta) error {
	if err := ValidateCanonicalDeltaTolerant(delta); err != nil {
		return err
	}
	if delta.Action != DeltaActionClaimPosted || delta.ClaimID() == "" {
		return fmt.Errorf("%w: unsupported delta %s", ErrServiceDispatcherInvalid, delta.Action)
	}
	if !deltaTargetsParticipant(delta, d.participant.AgentRef()) {
		return fmt.Errorf("%w: delta is not addressed to %s", ErrServiceDispatcherInvalid, d.participant.UID)
	}
	if !actionTypeAllowed(delta.ClaimActionType(), d.participant.Actions) {
		return fmt.Errorf("%w: action %s not registered", ErrServiceDispatcherInvalid, delta.ClaimActionType())
	}
	return nil
}

func (d *ServiceDispatcher) markStarting() (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return false, fmt.Errorf("%w: dispatcher is closed", ErrServiceDispatcherInvalid)
	}
	if d.started {
		return false, nil
	}
	d.started = true
	return true, nil
}

func (d *ServiceDispatcher) resetStart() {
	d.mu.Lock()
	d.started = false
	d.mu.Unlock()
}

func (d *ServiceDispatcher) installSubscriptions(subs []DeltaSubscription) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return fmt.Errorf("%w: dispatcher is closed", ErrServiceDispatcherInvalid)
	}
	if len(d.subscriptions) == 0 {
		d.subscriptions = append([]DeltaSubscription(nil), subs...)
	}
	return nil
}

func (d *ServiceDispatcher) closeSubscriptions() []DeltaSubscription {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	subs := append([]DeltaSubscription(nil), d.subscriptions...)
	d.subscriptions = nil
	return subs
}

func (d *ServiceDispatcher) isClosed() bool {
	if d == nil {
		return true
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.closed
}

func unsubscribeAll(subs []DeltaSubscription) error {
	var out error
	for _, sub := range subs {
		if sub != nil {
			out = errors.Join(out, sub.Unsubscribe())
		}
	}
	return out
}

func (d *ServiceDispatcher) acquire(delta CanonicalDelta) bool {
	select {
	case d.inflight <- struct{}{}:
		return true
	default:
		_ = delta
		return false
	}
}

func (d *ServiceDispatcher) release() {
	<-d.inflight
}

func (d *ServiceDispatcher) remember(deltaKey string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.seen[deltaKey]; ok {
		return false
	}
	d.seen[deltaKey] = struct{}{}
	d.seenOrder = append(d.seenOrder, deltaKey)
	d.pruneSeenLocked()
	return true
}

func (d *ServiceDispatcher) pruneSeenLocked() {
	for len(d.seenOrder) > d.participant.QueueCapacity {
		delete(d.seen, d.seenOrder[0])
		d.seenOrder = d.seenOrder[1:]
	}
}

func (d *ServiceDispatcher) subscriptionTopics() []string {
	ref := d.participant.AgentRef()
	return []string{CanonicalAgentRefTopic(d.sessionID, ref, DeltaActionClaimPosted)}
}

func validateServiceDispatcherConfig(cfg ServiceDispatcherConfig) error {
	if cfg.Board == nil || cfg.Scope == nil || cfg.Handler == nil {
		return fmt.Errorf("%w: board, scope, and handler are required", ErrServiceDispatcherInvalid)
	}
	return cfg.Participant.Validate()
}

func normalizeServiceArtifacts(artifacts []*Artifact, agentID string) []*Artifact {
	out := make([]*Artifact, 0, max(1, len(artifacts)))
	for _, artifact := range artifacts {
		if artifact == nil {
			continue
		}
		copy := *artifact
		copy.AgentID = firstNonEmpty(copy.AgentID, agentID)
		copy.ParticipantID = firstNonEmpty(copy.ParticipantID, agentID)
		if copy.ID == "" {
			copy.ID = uuid.NewString()
		}
		out = append(out, &copy)
	}
	if len(out) == 0 {
		out = append(out, &Artifact{AgentID: agentID, ParticipantID: agentID, ArtifactName: "service_readiness", Kind: ArtifactKindReadiness, Reference: "service completed"})
	}
	return out
}

func completeServiceTestamentValidation(ctx context.Context, board *ClaimsBoard, testamentID, actorID string) error {
	if err := completeReceiptOnlyTestamentValidation(ctx, board, testamentID, actorID, "service handler completed"); err == nil {
		return nil
	}
	if err := board.AcknowledgeTestamentReceipt(ctx, testamentID, actorID); err != nil {
		return ignoreAlreadyTerminalTestament(err, board, testamentID)
	}
	return board.BeginTestamentValidation(ctx, testamentID, actorID)
}

func ignoreAlreadyTerminalTestament(err error, board *ClaimsBoard, testamentID string) error {
	testament, ok := board.CloneTestament(testamentID)
	if err == nil || (ok && testament.LifecycleStatus != TestamentLifecyclePosted) {
		return nil
	}
	return err
}

func ignoreAlreadyProgressedClaim(err error, board *ClaimsBoard, claimID string) error {
	claim, ok := board.CloneClaim(claimID)
	if err == nil || (ok && claim.LifecycleStatus != ClaimLifecyclePosted && claim.LifecycleStatus != ClaimLifecycleReceived) {
		return nil
	}
	return err
}

func actionTypeAllowed(action ActionType, allowed []ActionType) bool {
	for _, candidate := range allowed {
		if candidate == action {
			return true
		}
	}
	return false
}

func deltaTargetsParticipant(delta CanonicalDelta, ref AgentRef) bool {
	if delta.Delivery == nil || len(delta.Delivery.To) == 0 {
		return true
	}
	for _, target := range delta.Delivery.To {
		if target.UID != "" && target.UID == ref.UID {
			return true
		}
		if target.Type != "" && target.Type == ref.Type && target.Category == ref.Category {
			return true
		}
	}
	return false
}

func boundedServiceFailureStack(stack []byte) string {
	if len(stack) <= serviceFailureStackLimitBytes {
		return string(stack)
	}
	return string(stack[:serviceFailureStackLimitBytes]) + "\n...truncated"
}
