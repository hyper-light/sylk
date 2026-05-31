package claims

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ErrPeerSaturated is returned when a publisher's peer-side admission
// check (Inbox.ConsultBudget) reports that the destination is at or
// past its high-water mark and the publisher's deadline expires before
// space frees. Distinct from network/transport errors so callers can
// handle peer saturation semantically (e.g. proceed without the
// consult, retry with backoff, surface to the LLM as a recoverable
// signal) rather than as a generic failure.
var ErrPeerSaturated = errors.New("claims: peer inbox saturated")

// InboxClass partitions the per-agent inbox stream by semantic load.
// Each delta is mapped to one class via DeltaClass; the class drives
// (a) the publisher-side bus message Priority used for overflow eviction
// — observational classes lose their slot first when a subscriber's
// queue fills — and (b) per-class delivery / overflow counters the
// agent loop reads to surface coverage gaps.
//
// Ordering of values is the priority order: lower-numbered classes are
// more sheddable (lower bus priority); higher-numbered classes are
// load-bearing (higher bus priority).
type InboxClass uint8

const (
	// InboxClassObservation is fan-in observation traffic — testaments
	// the agent watches for auditing/persistence but whose loss does
	// not break correctness (an inspector that misses one testimony
	// will record a coverage_gap rather than fail). Sheds first under
	// queue pressure.
	InboxClassObservation InboxClass = iota

	// InboxClassPhase is board phase transitions — orchestration
	// signal flow. Important but recoverable; phase observers can
	// re-derive state from the board on demand.
	InboxClassPhase

	// InboxClassDirected is a directed inbox delta where this agent
	// is subject/evaluator on a non-consultation action (task,
	// challenge, corrective). Load-bearing — must not be silently
	// dropped.
	InboxClassDirected

	// InboxClassConsultRequest is an incoming peer consultation. The
	// agent owes the issuer a reply; dropping silently strands the
	// caller. Highest non-response priority.
	InboxClassConsultRequest

	// InboxClassConsultResolved is reserved for expectation-matched
	// canonical response/testimony deltas once the inbox has
	// subscriber-specific knowledge. Publisher-side classification
	// defaults those canonical deltas to Observation; expectation
	// subscriptions protect them from broad standing-subscription
	// eviction.
	InboxClassConsultResolved

	numInboxClasses = int(InboxClassConsultResolved) + 1
)

// String returns the canonical lowercase identifier for this class
// (used in logs, telemetry, coverage-gap testimony).
func (c InboxClass) String() string {
	switch c {
	case InboxClassObservation:
		return "observation"
	case InboxClassPhase:
		return "phase"
	case InboxClassDirected:
		return "directed"
	case InboxClassConsultRequest:
		return "consult_request"
	case InboxClassConsultResolved:
		return "consult_response"
	}
	return "unknown"
}

// DeltaClass maps a delta to its semantic InboxClass. Used both at
// publish time (to set bus message priority) and at delivery time (for
// per-class accounting on the inbox).
//
// Mapping:
//   - canonical claim.posted with ActionTypeConsultation → ConsultRequest
//   - canonical claim.posted with any other action        → Directed
//   - canonical lifecycle/validation response facts       → Observation,
//     unless matched by an explicit expectation
//   - legacy InboxDelta/TestamentDelta/ClaimStatusDelta   → Observation only;
//     legacy deltas are tolerated for replay/projection but cannot wake agents
//     or satisfy waits.
//   - PhaseDelta → Phase
func DeltaClass(d Delta) InboxClass {
	if d == nil {
		return InboxClassObservation
	}
	switch delta := d.(type) {
	case CanonicalDelta:
		return canonicalDeltaClass(delta)
	case *CanonicalDelta:
		if delta == nil {
			return InboxClassObservation
		}
		return canonicalDeltaClass(*delta)
	case InboxDelta, *InboxDelta,
		TestamentDelta, *TestamentDelta,
		ValidationDelta, *ValidationDelta,
		ClaimStatusDelta, *ClaimStatusDelta:
		return InboxClassObservation
	case PhaseDelta, *PhaseDelta:
		return InboxClassPhase
	}
	return InboxClassObservation
}

func canonicalDeltaClass(delta CanonicalDelta) InboxClass {
	switch delta.Action {
	case DeltaActionClaimPosted:
		return inboxDeltaClass(delta.ClaimActionType())
	case DeltaActionClaimValidationFailed,
		DeltaActionClaimValidationIncomplete,
		DeltaActionClaimValidationErrored,
		DeltaActionClaimPostFailed,
		DeltaActionClaimReceiptFailed,
		DeltaActionClaimProgressFailed,
		DeltaActionClaimTestamentGenerationFailed,
		DeltaActionClaimTestamentAcknowledgementFailed:
		return claimStatusClass(delta.ClaimToStatus())
	case DeltaActionTestamentPosted, DeltaActionValidationEvaluated, DeltaActionClaimProgressed:
		return InboxClassObservation
	default:
		return InboxClassObservation
	}
}

func inboxDeltaClass(kind ActionType) InboxClass {
	switch kind {
	case ActionTypeConsultation:
		return InboxClassConsultRequest
	case ActionTypeTask, ActionTypeChallenge, ActionTypeCorrective:
		return InboxClassDirected
	}
	return InboxClassObservation
}

func claimStatusClass(status ClaimStatus) InboxClass {
	switch status {
	case ClaimStatusRejected:
		return InboxClassDirected
	}
	return InboxClassObservation
}

// ClaimsRole is a bitmask of capability roles an agent has on the
// claims board. Roles drive both the bus subscription pattern set
// (InboxPatternsFor) and the receive-side standing-subscription gate
// (matchesStandingSubscription). Each role is one bit so an agent may
// hold several — e.g. an inspector is RoleSubject|RoleAuditor; an
// orchestrator is RoleSubject|RolePhaseObserver.
//
// The role set is the implementation surface of CLAIMS.md §5.4's
// "subscription patterns" table: every entry in that table maps to a
// role here, and an agent's role determines exactly which patterns it
// subscribes to (no broad claim.*.* firehose).
type ClaimsRole uint32

const (
	// RoleSubject is the baseline every agent holds. The agent
	// subscribes to claims.<sid>.inbox.<self>.*.* — every directed
	// delta where the agent is subject, evaluator, or any other
	// directed-relationship.
	RoleSubject ClaimsRole = 1 << iota

	// RoleAuditor subscribes to claims.<sid>.claim.*.testified —
	// every testament submitted in the session. Per CLAIMS.md §5.4,
	// this is the inspector's pattern.
	RoleAuditor

	// RolePhaseObserver subscribes to claims.<sid>.phase.* — every
	// board phase transition. Per CLAIMS.md §5.4, this is the
	// orchestrator's pattern.
	RolePhaseObserver

	// RoleRemediator subscribes to claims.<sid>.claim.*.rejected —
	// every rejection in the session. The architect holds this so it
	// can author corrective actions on rejection without per-claim
	// expectation registration.
	RoleRemediator

	// RoleArchivist subscribes to claims.<sid>.claim.*.testified —
	// every testament submitted in the session. The archivalist holds
	// this so it can drive long-term persistence directly off the
	// claims board, without an upstream agent dispatching a
	// fire-and-forget RouteRequest at it. Conceptually overlaps with
	// RoleAuditor (which has the same pattern but represents an
	// auditor's intent to evaluate); kept distinct so the role
	// taxonomy at the authorization/observability layer reflects
	// "evaluate" vs. "persist" without overloading either.
	RoleArchivist

	// RoleObserver is the rendering-side wildcard role: subscribes
	// to every claim status, every claim/testament context delta,
	// and every directed inbox delta in the session. The TUI uses
	// this role to register itself as a claims participant and
	// drive UI rendering off the claims board exclusively. NOT for
	// agents that take action on the deltas — those use RoleSubject
	// (with directed semantics) or RoleAuditor (testament reads).
	// See docs/CLAIMS_UI.md "UI as a claims participant".
	RoleObserver
)

// Has reports whether r contains role.
func (r ClaimsRole) Has(role ClaimsRole) bool { return r&role != 0 }

// InboxPatternsFor returns the bus topic patterns an inbox with the
// given role must subscribe to. Per CLAIMS.md §5.4 each role is a
// narrow, dimensioned pattern — never a broad firehose.
//
// The zero role is treated as RoleSubject so an unconfigured inbox
// still receives directly-addressed deltas.
func InboxPatternsFor(role ClaimsRole, sessionID, agentID string) []string {
	if role == 0 {
		role = RoleSubject
	}
	out := make([]string, 0, 8)
	if role.Has(RoleSubject) {
		out = append(out,
			CanonicalAgentActionPattern(sessionID, agentID, DeltaActionClaimPosted),
			CanonicalAgentTypeActionPattern(sessionID, agentID, DeltaActionClaimPosted),
		)
	}
	if role.Has(RoleAuditor) {
		out = append(out, CanonicalClaimActionPattern(sessionID, "*", DeltaActionTestamentPosted))
	}
	if role.Has(RolePhaseObserver) {
		out = append(out, PhasePattern(sessionID))
	}
	if role.Has(RoleRemediator) {
		out = append(out, CanonicalClaimActionPattern(sessionID, "*", DeltaActionClaimValidationFailed))
		out = append(out, CanonicalClaimActionPattern(sessionID, "*", DeltaActionClaimValidationIncomplete))
		out = append(out, CanonicalClaimActionPattern(sessionID, "*", DeltaActionClaimValidationErrored))
	}
	if role.Has(RoleArchivist) {
		out = append(out, CanonicalClaimActionPattern(sessionID, "*", DeltaActionTestamentPosted))
	}
	if role.Has(RoleObserver) {
		out = append(out, CanonicalSessionPattern(sessionID))
		// Legacy context topics remain display-only until the UI is fully
		// projected from lifecycle facts. Coarse claim-status topics are
		// deliberately absent: lifecycle deltas are the authoritative status
		// stream.
		out = append(out, ClaimContextPattern(sessionID, "*"))
		out = append(out, TestamentContextPattern(sessionID, "*"))
	}
	return out
}

// busSubscriptionQueueCapDefault is the central claims-operations
// inbox budget. It anchors the dedup LRU to the same bounded queue cap
// used by bus subscriptions.
var busSubscriptionQueueCapDefault = DefaultClaimsOperationsConfig().Budgets.InboxSubscriptionQueueCap

// InboxConfig bundles construction parameters for a ClaimsInbox.
type InboxConfig struct {
	AgentID   string
	SessionID string

	// Role drives both the subscription pattern set
	// (InboxPatternsFor) and the receive-side standing-sub gate
	// (matchesStandingSubscription). Zero defaults to RoleSubject so
	// an unconfigured inbox still sees directly-addressed deltas.
	Role ClaimsRole

	// Subscriber is the bus subscription surface. Nil is normalized
	// to NoopDeltaBus (the inbox then only accepts deltas passed
	// directly to Ingest, e.g. for tests).
	Subscriber DeltaSubscriber

	// Board provides graph resolution (CloneClaim, CloneAction, etc.)
	// when resolving deltas into GraphEntryPoints. Nil-safe — the
	// entry point is delivered without a resolved node when Board is
	// unset.
	Board *ClaimsBoard

	// OnResolved is called when a delta matches an expectation or
	// standing subscription. The resolved GraphEntryPoint is passed
	// directly. The handler runs on the bus subscriber's goroutine
	// — it MUST dispatch into the agent's GoroutineScope for
	// tracked, async execution of the agent's processing logic.
	//
	// When nil, matched deltas are silently discarded (useful for
	// tests that only verify matching behavior via Len).
	OnResolved func(entry *GraphEntryPoint)

	// Patterns overrides the role-derived pattern set. When empty,
	// InboxPatternsFor(Role, SessionID, AgentID) is used at Start.
	// Tests use this to drive the inbox over a fixed pattern.
	Patterns []string

	// BusSubscriptionQueueCap mirrors the per-subscription queue
	// capacity of the channel bus. Zero defaults to
	// busSubscriptionQueueCapDefault. The dedup LRU is sized at
	// BusSubscriptionQueueCap × len(patterns).
	BusSubscriptionQueueCap int

	// Operations supplies normalized claims-plane budgets. Explicit
	// BusSubscriptionQueueCap wins for existing tests and small local
	// harnesses; otherwise this config provides the queue cap.
	Operations ClaimsOperationsConfig

	// CancelRegistry tracks claim-scoped execution contexts owned by
	// this inbox. When nil, the inbox creates a private registry so
	// session-level cancellation can still stop work dispatched through
	// this inbox.
	CancelRegistry *ClaimCancelRegistry

	// Metrics records inbox delivery/overflow gauges. Nil is safe.
	Metrics ClaimsMetricsSink
}

// ClaimsInbox is the per-replica event-driven intake surface.
//
// Deltas arrive from bus subscriptions narrowed by the agent's role
// patterns. The inbox matches each against registered expectations
// (from the agent's emissions) or its standing-subscription role
// gate. Matched deltas resolve into GraphEntryPoints and dispatch to
// OnResolved immediately. Unmatched deltas are discarded.
//
// Thread-safe. Public methods acquire mu.
type ClaimsInbox struct {
	mu sync.Mutex

	agentID   string
	sessionID string
	role      ClaimsRole

	subscriber    DeltaSubscriber
	subscriptions []DeltaSubscription

	// seen is the bounded dedup table: DeltaKey → highest Sequence
	// applied. Cap derives from the bus per-sub queue cap × pattern
	// count, the maximum number of unique in-flight deltas this
	// inbox can ever observe.
	seen *dedupLRU

	// expectations maps claim_id → *Expectation. Populated by
	// Expect(), consumed when a matching delta arrives via Ingest.
	expectations map[string]*Expectation

	// orphans buffers response-like deltas that arrived before the
	// issuer registered the matching expectation. It is keyed by claim
	// ID and reconciled synchronously in Expect().
	orphans map[string][]orphanedInboxDelta

	// expectationSubs holds one-shot, claim-specific bus subscriptions
	// created by Expect(). RoleSubject deliberately does not subscribe to
	// the claim.*.testified firehose, so issuer-side expectations need a
	// narrow return channel for the specific claim they issued.
	expectationSubs map[string][]DeltaSubscription

	board *ClaimsBoard

	// onResolved is called when a delta matches. Runs on the bus
	// subscriber's goroutine.
	onResolved func(entry *GraphEntryPoint)

	cancelRegistry *ClaimCancelRegistry
	metrics        ClaimsMetricsSink

	// queueCap is the bus per-sub queue cap, used to size the dedup
	// LRU once the actual pattern count is known at Start.
	queueCap int

	orphanLimit      int
	orphanClaimLimit int

	// matchCount tracks how many deltas have matched (for tests).
	matchCount atomic.Uint64

	// deliveredByClass counts deltas that were ingested per InboxClass
	// (regardless of whether they matched an expectation or standing
	// subscription). Read by agent loops via DeliveredByClass to
	// detect coverage gaps when paired with overflow signals from the
	// bus subscription handles.
	deliveredByClass [numInboxClasses]atomic.Uint64

	closed atomic.Bool
}

type ClaimsInboxSnapshot struct {
	AgentID           string            `json:"agent_id"`
	SessionID         string            `json:"session_id"`
	Role              ClaimsRole        `json:"role"`
	QueueCap          int               `json:"queue_cap"`
	OrphanLimit       int               `json:"orphan_limit"`
	OrphanClaimLimit  int               `json:"orphan_claim_limit"`
	Matched           uint64            `json:"matched"`
	Overflow          uint64            `json:"overflow"`
	DeliveredByClass  map[string]uint64 `json:"delivered_by_class,omitempty"`
	SubscriptionCount int               `json:"subscription_count"`
	ExpectationCount  int               `json:"expectation_count"`
	OrphanClaimCount  int               `json:"orphan_claim_count"`
	OrphanDeltaCount  int               `json:"orphan_delta_count"`
	Closed            bool              `json:"closed"`
}

// NewClaimsInbox constructs an inbox. Subscriptions are NOT attached
// until Start is called.
func NewClaimsInbox(cfg InboxConfig) (*ClaimsInbox, error) {
	if cfg.AgentID == "" {
		return nil, fmt.Errorf("inbox requires AgentID")
	}
	if cfg.SessionID == "" {
		return nil, fmt.Errorf("inbox requires SessionID")
	}
	role := cfg.Role
	if role == 0 {
		role = RoleSubject
	}
	ops := NormalizeClaimsOperationsConfig(cfg.Operations)
	queueCap := cfg.BusSubscriptionQueueCap
	if queueCap <= 0 {
		queueCap = ops.Budgets.InboxSubscriptionQueueCap
	}
	orphanLimit := ops.Budgets.ContinuationOrphanLimit
	// Pre-size dedup at queueCap (one pattern's worth). Start grows
	// it to queueCap × pattern count once patterns resolve.
	return &ClaimsInbox{
		agentID:          cfg.AgentID,
		sessionID:        cfg.SessionID,
		role:             role,
		subscriber:       subscribeOrNoop(cfg.Subscriber),
		board:            cfg.Board,
		onResolved:       cfg.OnResolved,
		cancelRegistry:   firstNonNilClaimCancelRegistry(cfg.CancelRegistry),
		metrics:          normalizeClaimsMetricsSink(cfg.Metrics),
		seen:             newDedupLRU(queueCap),
		expectations:     make(map[string]*Expectation),
		orphans:          make(map[string][]orphanedInboxDelta),
		expectationSubs:  make(map[string][]DeltaSubscription),
		queueCap:         queueCap,
		orphanLimit:      orphanLimit,
		orphanClaimLimit: inboxOrphanClaimLimit(orphanLimit),
	}, nil
}

func (i *ClaimsInbox) ClaimContext(ctx context.Context, claimID string) (context.Context, ClaimCancelRegistration) {
	if i == nil || i.cancelRegistry == nil {
		if ctx == nil {
			ctx = context.Background()
		}
		return ctx, ClaimCancelRegistration{}
	}
	return i.cancelRegistry.Context(ctx, claimID)
}

func (i *ClaimsInbox) CancelClaimWork(_ context.Context, claimID string, _ string) (bool, error) {
	if i == nil || i.cancelRegistry == nil {
		return false, nil
	}
	return i.cancelRegistry.CancelClaim(claimID) > 0, nil
}

func (i *ClaimsInbox) ActiveClaimIDs() []string {
	if i == nil || i.cancelRegistry == nil {
		return nil
	}
	return i.cancelRegistry.ActiveClaimIDs()
}

// ────────────────────────────────────────────────────────────────────
// Subscription lifecycle
// ────────────────────────────────────────────────────────────────────

// Start subscribes the inbox to patterns. When patterns is empty, the
// role-derived set from InboxPatternsFor is used. The dedup LRU is
// resized to queueCap × len(patterns) — the upper bound on unique
// in-flight deltas the bus can deliver to this inbox.
func (i *ClaimsInbox) Start(patterns []string) error {
	if i == nil {
		return fmt.Errorf("nil inbox")
	}
	if i.closed.Load() {
		return fmt.Errorf("inbox closed")
	}
	if len(patterns) == 0 {
		patterns = InboxPatternsFor(i.role, i.sessionID, i.agentID)
	}
	RouteDebugLog().Info("claims_inbox_start",
		"agent_id", i.agentID,
		"session_id", i.sessionID,
		"role", i.role,
		"patterns", patterns,
		"queue_cap", i.queueCap,
	)
	// Resize the dedup LRU now that we know the actual pattern count.
	dedupCap := i.queueCap * len(patterns)
	if dedupCap < i.queueCap {
		dedupCap = i.queueCap
	}
	i.mu.Lock()
	i.seen = newDedupLRU(dedupCap)
	i.mu.Unlock()

	for _, p := range patterns {
		if err := i.Subscribe(p); err != nil {
			return err
		}
	}
	return nil
}

// Subscribe registers an additional pattern on the inbox's bus
// subscriber. Safe to call at any time before Close.
func (i *ClaimsInbox) Subscribe(pattern string) error {
	if i == nil || i.closed.Load() {
		return fmt.Errorf("inbox closed")
	}
	handler := i.handler()
	sub, err := i.subscriber.SubscribeDelta(pattern, handler)
	if err != nil {
		slog.Error("claims_inbox_subscribe_failed",
			"agent_id", i.agentID,
			"session_id", i.sessionID,
			"pattern", pattern,
			"error", err.Error(),
		)
		RouteDebugLog().Info("claims_inbox_subscribe_failed",
			"agent_id", i.agentID,
			"session_id", i.sessionID,
			"role", i.role,
			"pattern", pattern,
			"error", err.Error(),
		)
		return fmt.Errorf("subscribe %q: %w", pattern, err)
	}
	slog.Info("claims_inbox_subscribed",
		"agent_id", i.agentID,
		"session_id", i.sessionID,
		"role", i.role,
		"pattern", pattern,
	)
	RouteDebugLog().Info("claims_inbox_subscribed",
		"agent_id", i.agentID,
		"session_id", i.sessionID,
		"role", i.role,
		"pattern", pattern,
	)
	i.mu.Lock()
	i.subscriptions = append(i.subscriptions, sub)
	i.mu.Unlock()
	return nil
}

// Close unsubscribes all patterns and releases resources. Idempotent.
func (i *ClaimsInbox) Close() error {
	if i == nil {
		return nil
	}
	if !i.closed.CompareAndSwap(false, true) {
		return nil
	}
	i.mu.Lock()
	subs := i.subscriptions
	i.subscriptions = nil
	i.seen = nil
	i.expectations = nil
	i.orphans = nil
	i.expectationSubs = nil
	i.mu.Unlock()

	var firstErr error
	for _, s := range subs {
		if err := s.Unsubscribe(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ────────────────────────────────────────────────────────────────────
// Expectations
// ────────────────────────────────────────────────────────────────────

// Expect registers an expectation for a specific response delta.
// Called by the runtime immediately after post_action commits.
func (i *ClaimsInbox) Expect(e *Expectation) {
	if i == nil || e == nil || i.closed.Load() {
		return
	}
	claimID := strings.TrimSpace(e.ClaimID)
	if claimID == "" {
		return
	}
	copied := *e
	copied.ClaimID = claimID
	topics := expectationTopics(i.sessionID, &copied)

	var entry *GraphEntryPoint
	var releaseSubs []DeltaSubscription
	i.mu.Lock()
	if i.expectations != nil {
		i.expectations[claimID] = &copied
	}
	if orphan, ok := i.takeMatchingOrphanLocked(claimID, copied.ExpectedDelta); ok {
		if i.expectations != nil {
			delete(i.expectations, claimID)
		}
		releaseSubs = i.releaseExpectationSubscriptionsLocked(claimID)
		i.matchCount.Add(1)
		entry = ResolveEntryPoint(i.board, orphan.delta, copied.Priority, &copied)
	}
	needsSubscribe := entry == nil && len(topics) > 0 && len(i.expectationSubs[claimID]) == 0
	i.mu.Unlock()

	for _, sub := range releaseSubs {
		if sub != nil {
			_ = sub.Unsubscribe()
		}
	}
	if entry != nil {
		slog.Info("claims_inbox_expectation_replayed_orphan",
			"agent_id", i.agentID,
			"session_id", i.sessionID,
			"claim_id", claimID,
			"delta_kind", entry.Delta.DeltaKind(),
			"delta_key", entry.Delta.DeltaKey(),
		)
		if i.onResolved != nil {
			i.onResolved(entry)
		}
		return
	}
	if needsSubscribe {
		i.subscribeExpectationTopics(claimID, topics)
	}
}

func expectationTopics(sessionID string, e *Expectation) []string {
	if e == nil {
		return nil
	}
	switch strings.TrimSpace(e.ExpectedDelta) {
	case DeltaKindTestament:
		return []string{
			CanonicalClaimTopic(sessionID, e.ClaimID, DeltaActionTestamentPosted),
			CanonicalClaimTopic(sessionID, e.ClaimID, DeltaActionClaimTestamentAcknowledged),
			CanonicalClaimTopic(sessionID, e.ClaimID, DeltaActionClaimSatisfied),
			CanonicalClaimTopic(sessionID, e.ClaimID, DeltaActionClaimValidationIncomplete),
			CanonicalClaimTopic(sessionID, e.ClaimID, DeltaActionClaimValidationFailed),
			CanonicalClaimTopic(sessionID, e.ClaimID, DeltaActionClaimValidationErrored),
		}
	case DeltaKindValidation:
		return []string{
			CanonicalClaimTopic(sessionID, e.ClaimID, DeltaActionValidationEvaluated),
		}
	case DeltaKindClaimStatus:
		return []string{
			CanonicalClaimTopic(sessionID, e.ClaimID, DeltaActionClaimSatisfied),
			CanonicalClaimTopic(sessionID, e.ClaimID, DeltaActionClaimValidationIncomplete),
			CanonicalClaimTopic(sessionID, e.ClaimID, DeltaActionClaimValidationFailed),
			CanonicalClaimTopic(sessionID, e.ClaimID, DeltaActionClaimValidationErrored),
		}
	default:
		return nil
	}
}

func (i *ClaimsInbox) subscribeExpectationTopics(claimID string, topics []string) {
	if i == nil || i.closed.Load() || strings.TrimSpace(claimID) == "" || len(topics) == 0 {
		return
	}
	var subs []DeltaSubscription
	for _, topic := range topics {
		topic = strings.TrimSpace(topic)
		if topic == "" {
			continue
		}
		handler := i.handler()
		sub, err := i.subscriber.SubscribeDelta(topic, handler)
		if err != nil {
			slog.Error("claims_inbox_expectation_subscribe_failed",
				"agent_id", i.agentID,
				"session_id", i.sessionID,
				"claim_id", claimID,
				"topic", topic,
				"error", err.Error(),
			)
			for _, prior := range subs {
				_ = prior.Unsubscribe()
			}
			return
		}
		subs = append(subs, sub)
	}
	if len(subs) == 0 {
		return
	}

	keep := false
	i.mu.Lock()
	if !i.closed.Load() && i.expectations[claimID] != nil && len(i.expectationSubs[claimID]) == 0 {
		i.expectationSubs[claimID] = subs
		i.subscriptions = append(i.subscriptions, subs...)
		keep = true
	}
	i.mu.Unlock()
	if !keep {
		for _, sub := range subs {
			_ = sub.Unsubscribe()
		}
		return
	}
	slog.Info("claims_inbox_expectation_subscribed",
		"agent_id", i.agentID,
		"session_id", i.sessionID,
		"claim_id", claimID,
		"topics", topics,
	)
}

// ────────────────────────────────────────────────────────────────────
// Delta intake
// ────────────────────────────────────────────────────────────────────

// Ingest accepts a delta from the bus handler. Deduplicates by
// (DeltaKey, DeltaSequence). Matches against expectations and the
// role's standing-subscription gate. Matched deltas resolve into
// GraphEntryPoints and dispatch to OnResolved immediately. Unmatched
// deltas are discarded.
func (i *ClaimsInbox) Ingest(d Delta) {
	if i == nil || d == nil || i.closed.Load() {
		return
	}
	class := DeltaClass(d)
	RouteDebugLog().Info("claims_inbox_delta_ingest_start",
		append([]any{
			"agent_id", i.agentID,
			"session_id", i.sessionID,
			"role", i.role,
			"class", class.String(),
		}, DeltaDebugArgs(d)...)...,
	)
	i.deliveredByClass[class].Add(1)
	i.recordInboxDelivery(d, class)
	i.mu.Lock()
	entry, releaseSubs := i.ingestLocked(d)
	i.mu.Unlock()
	for _, sub := range releaseSubs {
		if sub != nil {
			_ = sub.Unsubscribe()
		}
	}

	slog.Info("claims_inbox_delta_received",
		"agent_id", i.agentID,
		"session_id", i.sessionID,
		"delta_kind", d.DeltaKind(),
		"delta_key", d.DeltaKey(),
		"claim_id", deltaClaimID(d),
		"matched", entry != nil,
		"directed_agent_id", deltaDirectedAgentID(d),
	)
	RouteDebugLog().Info("claims_inbox_delta_ingest_done",
		append([]any{
			"agent_id", i.agentID,
			"session_id", i.sessionID,
			"role", i.role,
			"class", class.String(),
			"matched", entry != nil,
			"directed_agent_id", deltaDirectedAgentID(d),
		}, DeltaDebugArgs(d)...)...,
	)

	if entry != nil && i.onResolved != nil {
		RouteDebugLog().Info("claims_inbox_on_resolved_start",
			append([]any{
				"agent_id", i.agentID,
				"session_id", i.sessionID,
			}, DeltaDebugArgs(d)...)...,
		)
		i.onResolved(entry)
	}
}

func (i *ClaimsInbox) recordInboxDelivery(d Delta, class InboxClass) {
	if i == nil {
		return
	}
	recordClaimsGauge(context.Background(), i.metrics, "claims_dispatcher_queue_depth", float64(i.Len()), metricLabels("queue", "inbox:"+i.agentID+":"+class.String()))
	if overflow := i.OverflowCount(); overflow > 0 {
		recordClaimsCounter(context.Background(), i.metrics, "claims_delta_subscriber_overflow_total", metricLabels("topic", deltaMetricAction(d)))
	}
}

// deltaDirectedAgentID returns the agent_id field that drives standing-
// subscription matching for this delta. Used to correlate inbox topic
// lookups during diagnostics.
func deltaDirectedAgentID(d Delta) string {
	switch delta := d.(type) {
	case CanonicalDelta:
		return canonicalDirectedAgentID(delta)
	case *CanonicalDelta:
		if delta == nil {
			return ""
		}
		return canonicalDirectedAgentID(*delta)
	}
	return ""
}

func canonicalDirectedAgentID(delta CanonicalDelta) string {
	if delta.Delivery == nil || len(delta.Delivery.To) == 0 {
		return ""
	}
	return delta.Delivery.To[0].RouteKey()
}

func (i *ClaimsInbox) ingestLocked(d Delta) (*GraphEntryPoint, []DeltaSubscription) {
	// Dedup.
	key := deltaDedupKey(d)
	seq := d.DeltaSequence()
	if i.seen == nil {
		RouteDebugLog().Info("claims_inbox_delta_discarded",
			append([]any{
				"agent_id", i.agentID,
				"session_id", i.sessionID,
				"reason", "dedup_lru_uninitialized",
			}, DeltaDebugArgs(d)...)...,
		)
		return nil, nil
	}
	if !i.seen.observe(key, seq) {
		RouteDebugLog().Info("claims_inbox_delta_discarded",
			append([]any{
				"agent_id", i.agentID,
				"session_id", i.sessionID,
				"reason", "duplicate",
				"dedup_key", key,
			}, DeltaDebugArgs(d)...)...,
		)
		return nil, nil
	}

	// Match against expectations first (O(1) by claim_id).
	claimID := deltaClaimID(d)
	if claimID != "" && i.expectations != nil {
		if exp, ok := i.expectations[claimID]; ok && deltaMatchesExpectation(d, exp.ExpectedDelta) {
			delete(i.expectations, claimID)
			releaseSubs := i.releaseExpectationSubscriptionsLocked(claimID)
			i.matchCount.Add(1)
			RouteDebugLog().Info("claims_inbox_delta_matched",
				append([]any{
					"agent_id", i.agentID,
					"session_id", i.sessionID,
					"match_type", "expectation",
					"expected_delta", exp.ExpectedDelta,
				}, DeltaDebugArgs(d)...)...,
			)
			return ResolveEntryPoint(i.board, d, exp.Priority, exp), releaseSubs
		}
	}

	// Standing subscription matching by role.
	if i.matchesStandingSubscription(d) {
		priority := derivePriority(d)
		i.matchCount.Add(1)
		RouteDebugLog().Info("claims_inbox_delta_matched",
			append([]any{
				"agent_id", i.agentID,
				"session_id", i.sessionID,
				"match_type", "standing_subscription",
				"priority", priority,
			}, DeltaDebugArgs(d)...)...,
		)
		return ResolveEntryPoint(i.board, d, priority, nil), nil
	}

	// Unmatched response-like deltas may have beaten expectation
	// registration. Buffer briefly by claim ID so Expect can reconcile
	// without polling the board or replaying the bus.
	i.stashOrphanIfResponseLocked(d)
	RouteDebugLog().Info("claims_inbox_delta_discarded",
		append([]any{
			"agent_id", i.agentID,
			"session_id", i.sessionID,
			"reason", "no_expectation_or_standing_match",
			"claim_id", claimID,
			"directed_agent_id", deltaDirectedAgentID(d),
		}, DeltaDebugArgs(d)...)...,
	)
	return nil, nil
}

func deltaDedupKey(d Delta) string {
	if d == nil {
		return ""
	}
	switch delta := d.(type) {
	case CanonicalDelta:
		return canonicalDeltaLogicalDedupKey(delta)
	case *CanonicalDelta:
		if delta == nil {
			return ""
		}
		return canonicalDeltaLogicalDedupKey(*delta)
	}
	return d.DeltaKey()
}

func canonicalDeltaLogicalDedupKey(delta CanonicalDelta) string {
	return BuildCanonicalDeltaKey(delta.Action, delta.SessionID, delta.BoardID, delta.Refs, delta.Delivery)
}

func deltaMatchesExpectation(d Delta, expected string) bool {
	expected = strings.TrimSpace(expected)
	if d == nil || expected == "" {
		return false
	}
	switch expected {
	case DeltaKindTestament:
		if d.DeltaKind() == string(DeltaActionTestamentPosted) {
			return true
		}
		if canonicalClaimLifecycleDeltaResolvesExpectation(d) {
			return true
		}
		return false
	case DeltaKindValidation:
		return d.DeltaKind() == string(DeltaActionValidationEvaluated)
	case DeltaKindClaimStatus:
		return canonicalClaimLifecycleDeltaResolvesExpectation(d)
	default:
		return false
	}
}

func canonicalClaimLifecycleDeltaResolvesExpectation(d Delta) bool {
	switch delta := d.(type) {
	case CanonicalDelta:
		return claimLifecycleResolvesExpectation(delta)
	case *CanonicalDelta:
		return delta != nil && claimLifecycleResolvesExpectation(*delta)
	default:
		return false
	}
}

func claimLifecycleResolvesExpectation(delta CanonicalDelta) bool {
	status, ok := DeltaActionClaimLifecycleStatus(delta.Action)
	return ok && claimLifecycleStatusResolvesExpectation(status)
}

func claimLifecycleStatusResolvesExpectation(status ClaimLifecycleStatus) bool {
	switch status {
	case ClaimLifecycleTestamentAcknowledged,
		ClaimLifecycleSatisfied,
		ClaimLifecycleValidationIncomplete,
		ClaimLifecycleValidationFailed,
		ClaimLifecycleValidationErrored,
		ClaimLifecycleTestamentGenerationFailed,
		ClaimLifecycleTestamentAcknowledgementFailed:
		return true
	default:
		return false
	}
}

const (
	orphanInboxDeltaMaxAge       = 10 * time.Minute
	orphanInboxClaimLimitDivisor = 4
	orphanInboxClaimLimitMinimum = 1
)

type orphanedInboxDelta struct {
	delta     Delta
	stashedAt time.Time
}

func (i *ClaimsInbox) takeMatchingOrphanLocked(claimID, expected string) (orphanedInboxDelta, bool) {
	if i == nil || i.orphans == nil {
		return orphanedInboxDelta{}, false
	}
	list := i.orphans[claimID]
	if len(list) == 0 {
		return orphanedInboxDelta{}, false
	}
	now := time.Now()
	kept := list[:0]
	var found orphanedInboxDelta
	matched := false
	for _, orphan := range list {
		if orphan.delta == nil || now.Sub(orphan.stashedAt) > orphanInboxDeltaMaxAge {
			continue
		}
		if !matched && deltaMatchesExpectation(orphan.delta, expected) {
			found = orphan
			matched = true
			continue
		}
		kept = append(kept, orphan)
	}
	if len(kept) == 0 {
		delete(i.orphans, claimID)
	} else {
		i.orphans[claimID] = kept
	}
	return found, matched
}

func (i *ClaimsInbox) stashOrphanIfResponseLocked(d Delta) {
	if i == nil || i.orphans == nil || d == nil {
		return
	}
	claimID := deltaClaimID(d)
	if claimID == "" || !deltaMayResolveFutureExpectation(d) {
		return
	}
	now := time.Now()
	list := i.orphans[claimID]
	kept := list[:0]
	for _, orphan := range list {
		if orphan.delta == nil || now.Sub(orphan.stashedAt) > orphanInboxDeltaMaxAge {
			continue
		}
		if orphan.delta.DeltaKey() == d.DeltaKey() && orphan.delta.DeltaSequence() == d.DeltaSequence() {
			continue
		}
		kept = append(kept, orphan)
	}
	kept = append(kept, orphanedInboxDelta{delta: d, stashedAt: now})
	if capLimit := i.orphanLimitLocked(); len(kept) > capLimit {
		kept = kept[len(kept)-capLimit:]
	}
	i.orphans[claimID] = kept
	i.pruneOrphanClaimsLocked(now)
}

func (i *ClaimsInbox) orphanLimitLocked() int {
	limit := i.orphanLimit
	if limit <= 0 {
		limit = DefaultClaimsOperationsConfig().Budgets.ContinuationOrphanLimit
	}
	return limit
}

func (i *ClaimsInbox) orphanClaimLimitLocked() int {
	if i.orphanClaimLimit > 0 {
		return i.orphanClaimLimit
	}
	return inboxOrphanClaimLimit(i.orphanLimit)
}

func inboxOrphanClaimLimit(orphanLimit int) int {
	limit := orphanLimit / orphanInboxClaimLimitDivisor
	if limit < orphanInboxClaimLimitMinimum {
		return orphanInboxClaimLimitMinimum
	}
	return limit
}

func (i *ClaimsInbox) pruneOrphanClaimsLocked(now time.Time) {
	i.pruneExpiredOrphanClaimsLocked(now)
	for len(i.orphans) > i.orphanClaimLimitLocked() {
		claimID := i.oldestOrphanClaimLocked()
		if claimID == "" {
			return
		}
		delete(i.orphans, claimID)
	}
}

func (i *ClaimsInbox) pruneExpiredOrphanClaimsLocked(now time.Time) {
	for claimID, list := range i.orphans {
		kept := compactLiveOrphans(list, now)
		if len(kept) == 0 {
			delete(i.orphans, claimID)
			continue
		}
		i.orphans[claimID] = kept
	}
}

func compactLiveOrphans(list []orphanedInboxDelta, now time.Time) []orphanedInboxDelta {
	kept := list[:0]
	for _, orphan := range list {
		if orphan.delta == nil || now.Sub(orphan.stashedAt) > orphanInboxDeltaMaxAge {
			continue
		}
		kept = append(kept, orphan)
	}
	return kept
}

func (i *ClaimsInbox) oldestOrphanClaimLocked() string {
	var oldestClaim string
	var oldestAt time.Time
	for claimID, list := range i.orphans {
		at := oldestOrphanTime(list)
		if at.IsZero() {
			continue
		}
		if oldestAt.IsZero() || at.Before(oldestAt) {
			oldestAt = at
			oldestClaim = claimID
		}
	}
	return oldestClaim
}

func oldestOrphanTime(list []orphanedInboxDelta) time.Time {
	var oldest time.Time
	for _, orphan := range list {
		if orphan.delta == nil || orphan.stashedAt.IsZero() {
			continue
		}
		if oldest.IsZero() || orphan.stashedAt.Before(oldest) {
			oldest = orphan.stashedAt
		}
	}
	return oldest
}

func deltaMayResolveFutureExpectation(d Delta) bool {
	switch delta := d.(type) {
	case CanonicalDelta:
		return canonicalDeltaMayResolveFutureExpectation(delta)
	case *CanonicalDelta:
		return delta != nil && canonicalDeltaMayResolveFutureExpectation(*delta)
	default:
		return false
	}
}

func canonicalDeltaMayResolveFutureExpectation(delta CanonicalDelta) bool {
	if !DeltaActionMayCompleteExpectedWork(delta.Action) {
		return false
	}
	switch delta.Action {
	case DeltaActionTestamentPosted, DeltaActionValidationEvaluated:
		return true
	default:
		return claimLifecycleResolvesExpectation(delta)
	}
}

func (i *ClaimsInbox) releaseExpectationSubscriptionsLocked(claimID string) []DeltaSubscription {
	if i == nil || i.expectationSubs == nil {
		return nil
	}
	sub := i.expectationSubs[claimID]
	if len(sub) == 0 {
		return nil
	}
	delete(i.expectationSubs, claimID)
	return sub
}

// matchesStandingSubscription is a defense-in-depth guard that the
// delta arrived via a route this inbox's role permits. The bus router
// has already narrowed delivery via InboxPatternsFor — this check
// confirms canonical delivery identity and rejects deltas that would
// arrive only through patterns the role does not hold.
//
// Issuer-side return paths for directed claims flow through Expect()
// registered at post_action time, NOT through standing subscriptions.
// Legacy InboxDelta/TestamentDelta/ClaimStatusDelta values are not
// workflow inputs. They may still be decoded by projection adapters, but
// they never wake agents or resolve waits.
func (i *ClaimsInbox) matchesStandingSubscription(d Delta) bool {
	role := i.role
	if role == 0 {
		role = RoleSubject
	}
	switch delta := d.(type) {
	case CanonicalDelta:
		return i.matchesCanonicalStandingSubscription(delta)
	case *CanonicalDelta:
		if delta == nil {
			return false
		}
		return i.matchesCanonicalStandingSubscription(*delta)
	case InboxDelta, *InboxDelta,
		TestamentDelta, *TestamentDelta,
		ClaimStatusDelta, *ClaimStatusDelta,
		ValidationDelta, *ValidationDelta:
		return false
	case PhaseDelta, *PhaseDelta:
		return role.Has(RolePhaseObserver) || role.Has(RoleObserver)
	case ClaimContextDelta:
		if IsSystemInternalAction(delta.ActionKind) {
			return false
		}
		return role.Has(RoleObserver)
	case *ClaimContextDelta:
		if IsSystemInternalAction(delta.ActionKind) {
			return false
		}
		return role.Has(RoleObserver)
	case TestamentContextDelta:
		return role.Has(RoleObserver)
	case *TestamentContextDelta:
		return role.Has(RoleObserver)
	}
	return false
}

func (i *ClaimsInbox) matchesCanonicalStandingSubscription(delta CanonicalDelta) bool {
	role := i.role
	if role == 0 {
		role = RoleSubject
	}
	if _, ok := DeltaActionArtifactLifecycleStatus(delta.Action); ok {
		return role.Has(RoleObserver)
	}
	if _, ok := DeltaActionValidationLifecycleStatus(delta.Action); ok {
		return role.Has(RoleObserver)
	}
	switch delta.Action {
	case DeltaActionClaimPosted:
		if IsSystemInternalAction(delta.ClaimActionType()) {
			return false
		}
		if role.Has(RoleObserver) {
			return true
		}
		return role.Has(RoleSubject) && delta.DeliveredTo(i.agentID) && i.postedClaimNeedsSubjectWork(delta)
	case DeltaActionTestamentPosted:
		if IsSystemInternalAction(delta.ClaimActionType()) {
			return false
		}
		return role.Has(RoleAuditor) || role.Has(RoleArchivist) || role.Has(RoleObserver)
	case DeltaActionClaimValidationFailed, DeltaActionClaimValidationIncomplete, DeltaActionClaimValidationErrored:
		if IsSystemInternalAction(delta.ClaimActionType()) {
			return false
		}
		return claimStatusMatchesRole(role, delta.ClaimToStatus()) || role.Has(RoleObserver)
	case DeltaActionClaimGenerated,
		DeltaActionClaimReceived,
		DeltaActionClaimProgressed,
		DeltaActionClaimTestamentGenerated,
		DeltaActionClaimTestamentAcknowledged,
		DeltaActionClaimValidating,
		DeltaActionClaimSatisfied,
		DeltaActionClaimGenerationFailed,
		DeltaActionClaimPostFailed,
		DeltaActionClaimReceiptFailed,
		DeltaActionClaimProgressFailed,
		DeltaActionClaimTestamentGenerationFailed,
		DeltaActionClaimTestamentAcknowledgementFailed,
		DeltaActionTestamentGenerated,
		DeltaActionTestamentReceived,
		DeltaActionTestamentValidating,
		DeltaActionTestamentValidationIncomplete,
		DeltaActionTestamentValidationFailed,
		DeltaActionTestamentValidationErrored,
		DeltaActionTestamentValidated,
		DeltaActionValidationEvaluated:
		return role.Has(RoleObserver)
	default:
		return false
	}
}

func (i *ClaimsInbox) postedClaimNeedsSubjectWork(delta CanonicalDelta) bool {
	if i == nil || i.board == nil {
		return true
	}
	claim, ok := i.board.CloneClaim(delta.ClaimID())
	if !ok {
		return true
	}
	return IsClaimLifecycleActionable(claim.LifecycleStatus)
}

func claimStatusMatchesRole(role ClaimsRole, status ClaimStatus) bool {
	switch status {
	case ClaimStatusRejected:
		return role.Has(RoleRemediator)
	case ClaimStatusTestified:
		return role.Has(RoleAuditor) || role.Has(RoleArchivist)
	}
	return false
}

// ────────────────────────────────────────────────────────────────────
// Accessors
// ────────────────────────────────────────────────────────────────────

// Len returns the number of deltas that have matched since creation.
func (i *ClaimsInbox) Len() int {
	if i == nil {
		return 0
	}
	return int(i.matchCount.Load())
}

func (i *ClaimsInbox) Snapshot() ClaimsInboxSnapshot {
	if i == nil {
		return ClaimsInboxSnapshot{}
	}
	i.mu.Lock()
	snap := ClaimsInboxSnapshot{
		AgentID:           i.agentID,
		SessionID:         i.sessionID,
		Role:              i.role,
		QueueCap:          i.queueCap,
		OrphanLimit:       i.orphanLimit,
		OrphanClaimLimit:  i.orphanClaimLimit,
		Matched:           i.matchCount.Load(),
		SubscriptionCount: len(i.subscriptions),
		ExpectationCount:  len(i.expectations),
		OrphanClaimCount:  len(i.orphans),
		Closed:            i.closed.Load(),
	}
	for _, list := range i.orphans {
		snap.OrphanDeltaCount += len(list)
	}
	i.mu.Unlock()
	snap.Overflow = i.OverflowCount()
	snap.DeliveredByClass = i.deliveredByClassSnapshot()
	return snap
}

// OverflowCount returns the total number of deltas the bus has dropped
// across this inbox's subscriptions due to per-subscription queue
// overflow. Subscriptions whose handle does not implement
// DroppedCounter contribute zero. Drops happen at the bus before
// Ingest is called — by the time the agent reads this counter, the
// deltas are already gone, so the value is a coverage signal: when
// non-zero, observational classes (priority-mapped to lower bus
// priority) were shed first; the agent should record a coverage_gap
// testimony rather than assume completeness.
func (i *ClaimsInbox) OverflowCount() uint64 {
	if i == nil {
		return 0
	}
	i.mu.Lock()
	subs := append([]DeltaSubscription(nil), i.subscriptions...)
	i.mu.Unlock()
	var total uint64
	for _, s := range subs {
		if dc, ok := s.(DroppedCounter); ok {
			total += dc.DroppedCount()
		}
	}
	return total
}

// DeliveredByClass returns the number of deltas of the given class
// that have been ingested by this inbox since creation. Counted at
// Ingest entry, before dedup or matching — so it reflects what the
// bus actually delivered, not what the agent acted on.
func (i *ClaimsInbox) DeliveredByClass(class InboxClass) uint64 {
	if i == nil || int(class) >= numInboxClasses {
		return 0
	}
	return i.deliveredByClass[class].Load()
}

func (i *ClaimsInbox) deliveredByClassSnapshot() map[string]uint64 {
	out := make(map[string]uint64, numInboxClasses)
	for idx := range numInboxClasses {
		class := InboxClass(idx)
		if count := i.DeliveredByClass(class); count > 0 {
			out[class.String()] = count
		}
	}
	return out
}

// ConsultBudgetSnapshot is the publisher-side view of an inbox's
// admission state for incoming consult requests. Read by peers via
// SessionInboxRegistry.Lookup(...).ConsultBudget() before issuing a
// consult so they can record consult_deferred signals when the
// destination is saturated.
type ConsultBudgetSnapshot struct {
	// AgentID identifies the inbox this snapshot describes.
	AgentID string

	// QueueCap is the configured per-subscription bus queue cap
	// derived at startup from host telemetry. Surfaces what
	// "saturated" is calibrated against.
	QueueCap int

	// Delivered is the cumulative count of consult-class deltas that
	// reached this inbox since creation. Paired with prior snapshots
	// it indicates throughput.
	Delivered uint64

	// Drops is the cumulative count of bus-level overflow drops
	// across this inbox's subscriptions. Non-zero means the bus
	// shed lower-priority traffic; consult-priority deltas are
	// protected first but a non-zero value still signals the inbox
	// is under pressure.
	Drops uint64

	// Saturated is true when the publisher should treat this peer as
	// saturated for admission purposes. The current heuristic is
	// "drops are non-zero" — any bus-level eviction means the
	// destination's per-subscription queue has been full at least
	// once recently. Refined as more telemetry becomes available
	// without changing the publisher contract.
	Saturated bool
}

// ConsultBudget returns the publisher-side admission snapshot for
// this inbox. Used by peers to decide whether to defer or proceed
// with a consult when the destination is under pressure.
func (i *ClaimsInbox) ConsultBudget() ConsultBudgetSnapshot {
	if i == nil {
		return ConsultBudgetSnapshot{}
	}
	drops := i.OverflowCount()
	return ConsultBudgetSnapshot{
		AgentID:   i.agentID,
		QueueCap:  i.queueCap,
		Delivered: i.DeliveredByClass(InboxClassConsultRequest),
		Drops:     drops,
		Saturated: drops > 0,
	}
}

// AgentID returns the agent this inbox serves.
func (i *ClaimsInbox) AgentID() string {
	if i == nil {
		return ""
	}
	return i.agentID
}

// SessionID returns the session this inbox is scoped to.
func (i *ClaimsInbox) SessionID() string {
	if i == nil {
		return ""
	}
	return i.sessionID
}

// Role returns the role bitmask this inbox carries.
func (i *ClaimsInbox) Role() ClaimsRole {
	if i == nil {
		return 0
	}
	return i.role
}

// Board returns the board handle (nil when none).
func (i *ClaimsInbox) Board() *ClaimsBoard {
	if i == nil {
		return nil
	}
	return i.board
}

// ────────────────────────────────────────────────────────────────────
// Internals
// ────────────────────────────────────────────────────────────────────

func (i *ClaimsInbox) handler() DeltaHandler {
	return func(d Delta) {
		i.Ingest(d)
	}
}

// deltaClaimID extracts the ClaimID from any delta type that carries
// one. Returns empty string for PhaseDelta.
func deltaClaimID(d Delta) string {
	switch delta := d.(type) {
	case CanonicalDelta:
		return delta.ClaimID()
	case *CanonicalDelta:
		if delta == nil {
			return ""
		}
		return delta.ClaimID()
	case InboxDelta:
		return delta.ClaimID
	case *InboxDelta:
		return delta.ClaimID
	case TestamentDelta:
		return delta.ClaimID
	case *TestamentDelta:
		return delta.ClaimID
	case ValidationDelta:
		return delta.ClaimID
	case *ValidationDelta:
		return delta.ClaimID
	case ClaimStatusDelta:
		return delta.ClaimID
	case *ClaimStatusDelta:
		return delta.ClaimID
	}
	return ""
}

// derivePriority determines the WorkUnitPriority for a delta that
// matched a standing subscription.
func derivePriority(d Delta) WorkUnitPriority {
	switch delta := d.(type) {
	case CanonicalDelta:
		return canonicalPriority(delta)
	case *CanonicalDelta:
		if delta == nil {
			return PriorityAdvisory
		}
		return canonicalPriority(*delta)
	case InboxDelta:
		return inboxDeltaPriority(delta.ActionKind)
	case *InboxDelta:
		return inboxDeltaPriority(delta.ActionKind)
	case TestamentDelta, *TestamentDelta:
		return PriorityResponse
	case ValidationDelta, *ValidationDelta:
		return PriorityEvaluation
	case ClaimStatusDelta:
		return claimStatusPriority(delta.ToStatus)
	case *ClaimStatusDelta:
		return claimStatusPriority(delta.ToStatus)
	case PhaseDelta, *PhaseDelta:
		return PriorityPhase
	}
	return PriorityAdvisory
}

func canonicalPriority(delta CanonicalDelta) WorkUnitPriority {
	switch delta.Action {
	case DeltaActionClaimPosted:
		return inboxDeltaPriority(delta.ClaimActionType())
	case DeltaActionTestamentPosted, DeltaActionTestamentReceived, DeltaActionTestamentValidated:
		return PriorityResponse
	case DeltaActionValidationEvaluated:
		return PriorityEvaluation
	case DeltaActionClaimSatisfied,
		DeltaActionClaimValidationIncomplete,
		DeltaActionClaimValidationFailed,
		DeltaActionClaimValidationErrored,
		DeltaActionClaimPostFailed,
		DeltaActionClaimReceiptFailed,
		DeltaActionClaimProgressFailed,
		DeltaActionClaimTestamentGenerationFailed,
		DeltaActionClaimTestamentAcknowledgementFailed:
		return claimStatusPriority(delta.ClaimToStatus())
	case DeltaActionClaimGenerated,
		DeltaActionClaimReceived,
		DeltaActionClaimProgressed,
		DeltaActionClaimTestamentGenerated,
		DeltaActionClaimTestamentAcknowledged,
		DeltaActionClaimValidating,
		DeltaActionTestamentGenerated,
		DeltaActionTestamentValidating,
		DeltaActionTestamentValidationIncomplete,
		DeltaActionTestamentValidationFailed,
		DeltaActionTestamentValidationErrored:
		return PriorityAdvisory
	default:
		return PriorityAdvisory
	}
}

func inboxDeltaPriority(kind ActionType) WorkUnitPriority {
	switch kind {
	case ActionTypeCorrective:
		return PriorityRemediation
	case ActionTypeChallenge:
		return PriorityChallenge
	case ActionTypeConsultation, ActionTypeTask:
		return PriorityDirected
	}
	return PriorityAdvisory
}

func claimStatusPriority(status ClaimStatus) WorkUnitPriority {
	switch status {
	case ClaimStatusRejected:
		return PriorityRemediation
	case ClaimStatusAccepted:
		return PriorityResponse
	}
	return PriorityAdvisory
}

// ────────────────────────────────────────────────────────────────────
// Bounded dedup LRU
// ────────────────────────────────────────────────────────────────────

// dedupLRU is the bounded dedup state for ClaimsInbox.seen. Sized at
// BusSubscriptionQueueCap × len(patterns) — the maximum number of
// unique in-flight deltas the bus can deliver to this inbox before
// per-subscription queue overflow. Eviction is least-recently-used,
// computed off insertion/observation order.
type dedupLRU struct {
	cap   int
	head  *dedupEntry
	tail  *dedupEntry
	nodes map[string]*dedupEntry
}

type dedupEntry struct {
	key  string
	seq  uint64
	prev *dedupEntry
	next *dedupEntry
}

func newDedupLRU(capacity int) *dedupLRU {
	if capacity <= 0 {
		capacity = busSubscriptionQueueCapDefault
	}
	return &dedupLRU{cap: capacity, nodes: make(map[string]*dedupEntry, capacity)}
}

// observe records (key, seq). Returns true when the delta is new or
// supersedes a stored older sequence on the same key (i.e. the caller
// should process it). Returns false on a duplicate or older sequence.
func (l *dedupLRU) observe(key string, seq uint64) bool {
	if l == nil {
		return false
	}
	if e, ok := l.nodes[key]; ok {
		if e.seq >= seq {
			return false
		}
		e.seq = seq
		l.touch(e)
		return true
	}
	if len(l.nodes) >= l.cap && l.head != nil {
		evicted := l.head
		l.detach(evicted)
		delete(l.nodes, evicted.key)
	}
	e := &dedupEntry{key: key, seq: seq}
	l.attachTail(e)
	l.nodes[key] = e
	return true
}

// Len returns the number of entries currently retained.
func (l *dedupLRU) Len() int {
	if l == nil {
		return 0
	}
	return len(l.nodes)
}

func (l *dedupLRU) touch(e *dedupEntry) {
	if e == l.tail {
		return
	}
	l.detach(e)
	l.attachTail(e)
}

func (l *dedupLRU) detach(e *dedupEntry) {
	if e.prev != nil {
		e.prev.next = e.next
	} else {
		l.head = e.next
	}
	if e.next != nil {
		e.next.prev = e.prev
	} else {
		l.tail = e.prev
	}
	e.prev = nil
	e.next = nil
}

func (l *dedupLRU) attachTail(e *dedupEntry) {
	e.prev = l.tail
	e.next = nil
	if l.tail != nil {
		l.tail.next = e
	} else {
		l.head = e
	}
	l.tail = e
}
