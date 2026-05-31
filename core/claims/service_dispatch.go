package claims

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"
)

var (
	ErrServiceDispatcherInvalid = errors.New("service dispatcher invalid")
	ErrServiceDispatchOverflow  = errors.New("service dispatcher concurrency budget exhausted")
)

const (
	serviceFailureStackLimitBytes = 64 * 1024
	serviceSeenKeysPerDelta       = 2
)

type ServiceClaimRequest struct {
	Board       *ClaimsBoard
	Claim       *Claim
	Delta       CanonicalDelta
	Participant ParticipantRegistration
}

type ServiceClaimResult struct {
	Summary         string
	Artifacts       []*Artifact
	ShadowArtifacts []*Artifact
	Metadata        map[string]any
}

type ServiceShutdownRequest struct {
	Board       *ClaimsBoard
	Participant ParticipantRegistration
	SessionID   string
}

//go:generate mockery --name=ServiceHandler --output=./mocks --outpkg=mocks
type ServiceHandler interface {
	HandleServiceClaim(ctx context.Context, req ServiceClaimRequest) (ServiceClaimResult, error)
}

type ServiceToolCatalog interface {
	ServiceTools() []string
}

type serviceShutdownHandler interface {
	ShutdownService(ctx context.Context, req ServiceShutdownRequest) (ServiceClaimResult, error)
}

type ServiceDispatcherConfig struct {
	Board          *ClaimsBoard
	Subscriber     DeltaSubscriber
	Scope          ScopeProvider
	Participant    ParticipantRegistration
	Handler        ServiceHandler
	SessionID      string
	CancelRegistry *ClaimCancelRegistry
	Metrics        ClaimsMetricsSink
}

type ServiceDispatcher struct {
	board       *ClaimsBoard
	subscriber  DeltaSubscriber
	scope       ScopeProvider
	participant ParticipantRegistration
	handler     ServiceHandler
	sessionID   string

	mu             sync.Mutex
	seen           map[string]struct{}
	seenOrder      []string
	inflight       chan struct{}
	active         map[string]context.CancelFunc
	subscriptions  []DeltaSubscription
	cancelRegistry *ClaimCancelRegistry
	metrics        ClaimsMetricsSink
	started        bool
	closed         bool
}

type ServiceDispatcherStats struct {
	SessionID      string                  `json:"session_id,omitempty"`
	Participant    ParticipantRegistration `json:"participant"`
	Started        bool                    `json:"started"`
	Closed         bool                    `json:"closed"`
	SeenCount      int                     `json:"seen_count"`
	QueueDepth     int                     `json:"queue_depth"`
	Inflight       int                     `json:"inflight"`
	Capacity       int                     `json:"capacity"`
	ActiveClaimIDs []string                `json:"active_claim_ids,omitempty"`
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
		board:          cfg.Board,
		subscriber:     subscribeOrNoop(cfg.Subscriber),
		scope:          cfg.Scope,
		participant:    participant,
		handler:        cfg.Handler,
		sessionID:      firstNonEmpty(strings.TrimSpace(cfg.SessionID), cfg.Board.SessionID()),
		seen:           make(map[string]struct{}, participant.QueueCapacity*serviceSeenKeysPerDelta),
		inflight:       make(chan struct{}, participant.ConcurrencyBudget),
		active:         make(map[string]context.CancelFunc, participant.ConcurrencyBudget),
		cancelRegistry: firstNonNilClaimCancelRegistry(cfg.CancelRegistry),
		metrics:        normalizeClaimsMetricsSink(cfg.Metrics),
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
	return errors.Join(unsubscribeAll(d.closeSubscriptions()), d.recordServiceShutdown(context.Background()))
}

func (d *ServiceDispatcher) Participant() ParticipantRegistration {
	if d == nil {
		return ParticipantRegistration{}
	}
	return cloneParticipantRegistration(d.participant)
}

func (d *ServiceDispatcher) Stats() ServiceDispatcherStats {
	if d == nil {
		return ServiceDispatcherStats{}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	active := make([]string, 0, len(d.active))
	for claimID := range d.active {
		active = append(active, claimID)
	}
	sort.Strings(active)
	return ServiceDispatcherStats{
		SessionID:      d.sessionID,
		Participant:    cloneParticipantRegistration(d.participant),
		Started:        d.started,
		Closed:         d.closed,
		SeenCount:      len(d.seen),
		QueueDepth:     len(d.active),
		Inflight:       len(d.inflight),
		Capacity:       cap(d.inflight),
		ActiveClaimIDs: active,
	}
}

func (d *ServiceDispatcher) CancelClaim(claimID string) bool {
	if d == nil {
		return false
	}
	claimID = strings.TrimSpace(claimID)
	if claimID == "" {
		return false
	}
	d.mu.Lock()
	cancel := d.active[claimID]
	d.mu.Unlock()
	if cancel == nil {
		return d.cancelRegistry.CancelClaim(claimID) > 0
	}
	cancel()
	_ = d.cancelRegistry.CancelClaim(claimID)
	return true
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
	d.recordQueueMetrics(ctx)
	if !d.dispatchEnabled() {
		return d.recordFailure(ctx, delta, ValidationErrorCategoryDispatcher, "infrastructure dispatch disabled by rollout gate", nil)
	}
	if d.isClosed() {
		return fmt.Errorf("%w: dispatcher is closed", ErrServiceDispatcherInvalid)
	}
	if !d.remember(delta) {
		return nil
	}
	if !d.acquire(delta) {
		d.recordDispatcherOverflow(ctx)
		return d.recordOverflow(ctx, delta)
	}
	if err := d.scope.Go("claims.service."+d.participant.RouteKey, d.participant.HandlerTimeout, func(runCtx context.Context) error {
		runCtx, reg := d.cancelRegistry.Context(runCtx, delta.ClaimID())
		d.trackActive(delta.ClaimID(), reg.cancel)
		defer d.untrackActive(delta.ClaimID())
		defer reg.Done()
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
			d.recordHandlerInvocation(context.Background(), "panic")
			err = d.recordFailure(context.Background(), delta, ValidationErrorCategoryPanic, fmt.Sprintf("%v", recovered), debug.Stack())
		}
	}()
	claim, err := d.prepareClaim(ctx, delta)
	if err != nil {
		return err
	}
	if d.testamentAlreadyRecorded(claim) {
		d.recordHandlerInvocation(ctx, "deduped")
		return nil
	}
	result, err := d.handler.HandleServiceClaim(ctx, ServiceClaimRequest{Board: d.board, Claim: claim, Delta: delta, Participant: d.participant})
	if err != nil {
		d.recordHandlerInvocation(ctx, "failure")
		return d.recordFailure(ctx, delta, ValidationErrorCategoryHandler, err.Error(), nil)
	}
	d.recordHandlerInvocation(ctx, "success")
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
	idempotencyKey := ServiceHandlerIdempotencyKey(claim, d.participant)
	artifacts := normalizeServiceArtifacts(result.Artifacts, d.participant.RouteKey)
	if err := d.recordShadowComparison(ctx, artifacts, result.ShadowArtifacts); err != nil {
		return err
	}
	testament := Testament{
		AgentID:        d.participant.RouteKey,
		Summary:        firstNonEmpty(strings.TrimSpace(result.Summary), "service claim completed"),
		Confidence:     "deterministic",
		IdempotencyKey: idempotencyKey,
		Relations: []Relation{
			{Related: claim.ID, RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim},
			{Related: idempotencyKey, RelatedType: RelatedTypeIdempotencyKey, Relationship: RelationshipDerivedFrom},
		},
		Artifacts: artifacts,
	}
	generated, err := d.board.GenerateTestamentAction(ctx, Action{AgentID: d.participant.RouteKey, Type: ActionTypeTestament, Status: ActionStatusComplete}, []Testament{testament}, GenerateTestamentActionOptions{
		IdempotencyKey: "service_dispatch:" + idempotencyKey,
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

func (d *ServiceDispatcher) recordQueueMetrics(ctx context.Context) {
	if d == nil {
		return
	}
	depth := len(d.inflight)
	capacity := cap(d.inflight)
	queue := d.participant.RouteKey
	recordClaimsGauge(ctx, d.metrics, "claims_dispatcher_queue_depth", float64(depth), metricLabels("queue", queue))
	recordClaimsGauge(ctx, d.metrics, "claims_in_flight_handler_count", float64(depth), metricLabels("participant_type", string(d.participant.Category)))
	if capacity > 0 && depth+1 >= capacity {
		recordClaimsCounter(ctx, d.metrics, "claims_dispatcher_queue_near_capacity_total", metricLabels("queue", queue))
	}
}

func (d *ServiceDispatcher) recordDispatcherOverflow(ctx context.Context) {
	if d == nil {
		return
	}
	recordClaimsCounter(ctx, d.metrics, "claims_dispatcher_queue_overflow_total", metricLabels("queue", d.participant.RouteKey))
}

func (d *ServiceDispatcher) recordHandlerInvocation(ctx context.Context, outcome string) {
	if d == nil {
		return
	}
	recordClaimsCounter(ctx, d.metrics, "claims_dispatcher_handler_invocations_total", metricLabels(
		"participant_type", string(d.participant.Category),
		"outcome", outcome,
	))
}

func (d *ServiceDispatcher) recordServiceShutdown(ctx context.Context) error {
	handler, ok := d.handler.(serviceShutdownHandler)
	if !ok {
		return nil
	}
	result, err := handler.ShutdownService(ctx, ServiceShutdownRequest{Board: d.board, Participant: d.participant, SessionID: d.sessionID})
	if err != nil {
		return err
	}
	var out error
	for _, artifact := range result.Artifacts {
		if artifact == nil {
			continue
		}
		_, err = RecordInfrastructureEvidence(ctx, InfrastructureEvidenceOptions{
			Board:     d.board,
			ActorID:   d.participant.RouteKey,
			SubjectID: d.participant.RouteKey,
			Operation: firstNonEmpty(strings.TrimSpace(result.Summary), "service_shutdown"),
			Artifact:  artifact,
		})
		out = errors.Join(out, err)
	}
	return out
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

func (d *ServiceDispatcher) dispatchEnabled() bool {
	subsystem := InfrastructureSubsystemForParticipantID(d.participant.RouteKey)
	if subsystem == "" {
		return true
	}
	return d.board.RolloutConfig().InfrastructureDispatchEnabled(subsystem)
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
	for _, cancel := range d.active {
		cancel()
	}
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

func (d *ServiceDispatcher) trackActive(claimID string, cancel context.CancelFunc) {
	claimID = strings.TrimSpace(claimID)
	if d == nil || claimID == "" || cancel == nil {
		return
	}
	d.mu.Lock()
	d.active[claimID] = cancel
	d.mu.Unlock()
}

func (d *ServiceDispatcher) untrackActive(claimID string) {
	claimID = strings.TrimSpace(claimID)
	if d == nil || claimID == "" {
		return
	}
	d.mu.Lock()
	delete(d.active, claimID)
	d.mu.Unlock()
}

func (d *ServiceDispatcher) remember(delta CanonicalDelta) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	deltaKey := strings.TrimSpace(delta.Key)
	if _, ok := d.seen[deltaKey]; ok {
		return false
	}
	claimKey := "claim:" + strings.TrimSpace(delta.ClaimID())
	if _, ok := d.seen[claimKey]; ok {
		return false
	}
	d.seen[deltaKey] = struct{}{}
	d.seen[claimKey] = struct{}{}
	d.seenOrder = append(d.seenOrder, deltaKey, claimKey)
	d.pruneSeenLocked()
	return true
}

func (d *ServiceDispatcher) pruneSeenLocked() {
	for len(d.seenOrder) > d.seenCapacity() {
		delete(d.seen, d.seenOrder[0])
		d.seenOrder = d.seenOrder[1:]
	}
}

func (d *ServiceDispatcher) seenCapacity() int {
	return d.participant.QueueCapacity * serviceSeenKeysPerDelta
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

func firstNonNilClaimCancelRegistry(registry *ClaimCancelRegistry) *ClaimCancelRegistry {
	if registry != nil {
		return registry
	}
	return NewClaimCancelRegistry()
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

func (d *ServiceDispatcher) testamentAlreadyRecorded(claim *Claim) bool {
	if d == nil || d.board == nil || claim == nil {
		return false
	}
	key := ServiceHandlerIdempotencyKey(claim, d.participant)
	for _, testament := range d.board.TestamentsByClaim(claim.ID) {
		if testamentHasIdempotencyKey(testament, key) && testament.LifecycleStatus.IsTerminal() {
			return true
		}
	}
	return false
}

func testamentHasIdempotencyKey(testament *Testament, key string) bool {
	if testament == nil || strings.TrimSpace(key) == "" {
		return false
	}
	if strings.TrimSpace(testament.IdempotencyKey) == strings.TrimSpace(key) {
		return true
	}
	for _, relation := range testament.Relations {
		if relation.RelatedType == RelatedTypeIdempotencyKey && relation.Relationship == RelationshipDerivedFrom && strings.TrimSpace(relation.Related) == strings.TrimSpace(key) {
			return true
		}
	}
	return false
}

func (d *ServiceDispatcher) recordShadowComparison(ctx context.Context, serviceArtifacts, shadowArtifacts []*Artifact) error {
	subsystem := InfrastructureSubsystemForParticipantID(d.participant.RouteKey)
	if subsystem == "" || len(shadowArtifacts) == 0 || d.board.RolloutConfig().InfrastructureMode(subsystem) != InfrastructureRolloutShadow {
		return nil
	}
	comparison := CompareInfrastructureShadow(subsystem, serviceArtifacts, shadowArtifacts)
	_, err := RecordInfrastructureShadowComparison(ctx, d.board, comparison)
	return err
}

func ServiceHandlerIdempotencyKey(claim *Claim, participant ParticipantRegistration) string {
	if claim == nil {
		return stableInfrastructureHash(participant.UID)
	}
	return stableInfrastructureHash(map[string]any{
		"type":                string(claim.ActionType),
		"subject_uid":         firstNonEmpty(participant.UID, SubjectAgentID(claim.Relations)),
		"scope":               claim.Scope,
		"expected_tool_calls": claim.ExpectedToolCalls,
		"title":               strings.TrimSpace(claim.Title),
		"description":         strings.TrimSpace(claim.Description),
		"nonce":               expectedToolIdempotencyNonce(claim.ExpectedToolCalls),
		"participant_version": participant.Generation,
	})
}

func expectedToolIdempotencyNonce(calls []ExpectedToolCall) string {
	for _, call := range calls {
		if nonce := firstNonEmpty(stringArg(call.Arguments, "idempotency_nonce"), stringArg(call.Arguments, "nonce")); nonce != "" {
			return nonce
		}
	}
	return ""
}

func boundedServiceFailureStack(stack []byte) string {
	if len(stack) <= serviceFailureStackLimitBytes {
		return string(stack)
	}
	return string(stack[:serviceFailureStackLimitBytes]) + "\n...truncated"
}
