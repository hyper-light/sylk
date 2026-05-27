package claims

import (
	"errors"
	"fmt"
	"log/slog"
	"runtime"
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
//   - InboxDelta with ActionTypeConsultation → ConsultRequest
//   - InboxDelta with any other ActionType    → Directed
//   - TestamentDelta / ValidationDelta with a directed claim_id the
//     agent is awaiting → response handling is resolved by explicit
//     expectations, not by an independent consult_resolved delta.
//   - TestamentDelta / ValidationDelta otherwise → Observation
//   - ClaimStatusDelta with rejected → Directed (remediator role
//     responds); other statuses → Observation
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
	case InboxDelta:
		return inboxDeltaClass(delta.ActionKind)
	case *InboxDelta:
		return inboxDeltaClass(delta.ActionKind)
	case TestamentDelta, *TestamentDelta:
		return InboxClassObservation
	case ValidationDelta, *ValidationDelta:
		return InboxClassObservation
	case ClaimStatusDelta:
		return claimStatusClass(delta.ToStatus)
	case *ClaimStatusDelta:
		return claimStatusClass(delta.ToStatus)
	case PhaseDelta, *PhaseDelta:
		return InboxClassPhase
	}
	return InboxClassObservation
}

func canonicalDeltaClass(delta CanonicalDelta) InboxClass {
	switch delta.Action {
	case DeltaActionClaimDirected:
		return inboxDeltaClass(delta.ClaimActionType())
	case DeltaActionClaimTransitioned:
		return claimStatusClass(delta.ClaimToStatus())
	case DeltaActionTestamentSubmitted, DeltaActionValidationEvaluated, DeltaActionClaimProgressed:
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
	activationTypes := AgentActivationActionTypes()
	// Pre-size for the worst case: subject (one per activation type)
	// + auditor + phase + remediator + archivist.
	out := make([]string, 0, len(activationTypes)+4)
	if role.Has(RoleSubject) {
		out = append(out,
			CanonicalAgentActionPattern(sessionID, agentID, DeltaActionClaimDirected),
			CanonicalAgentTypeActionPattern(sessionID, agentID, DeltaActionClaimDirected),
		)
		// RoleSubject subscribes to N narrow patterns — one per
		// legitimate activation action type — instead of the broad
		// AgentInboxPattern(*.*) firehose. System-internal action
		// types (boot/activation/shutdown/archival/testament/
		// checkpoint/consult_continuation) match no pattern in this
		// set, so an inbox delta on those types never wakes the
		// inbox handler. Defense-in-depth:
		// matchesStandingSubscription rejects system action types
		// even if a pattern slips through.
		for _, kind := range activationTypes {
			out = append(out, AgentInboxActionPattern(sessionID, agentID, RelationshipSubject, kind))
		}
	}
	if role.Has(RoleAuditor) {
		out = append(out, CanonicalClaimActionPattern(sessionID, "*", DeltaActionTestamentSubmitted))
		out = append(out, ClaimStatusPattern(sessionID, ClaimStatusTestified))
	}
	if role.Has(RolePhaseObserver) {
		out = append(out, PhasePattern(sessionID))
	}
	if role.Has(RoleRemediator) {
		out = append(out, CanonicalClaimActionPattern(sessionID, "*", DeltaActionClaimTransitioned))
		out = append(out, ClaimStatusPattern(sessionID, ClaimStatusRejected))
	}
	if role.Has(RoleArchivist) {
		out = append(out, CanonicalClaimActionPattern(sessionID, "*", DeltaActionTestamentSubmitted))
		out = append(out, ClaimStatusPattern(sessionID, ClaimStatusTestified))
	}
	if role.Has(RoleObserver) {
		out = append(out, CanonicalSessionPattern(sessionID))
		// Wildcard set: every claim status, every context delta, every
		// directed inbox delta in the session. The UI uses this to
		// rebuild the chat tree + agent panel exclusively from claims
		// signals.
		for _, status := range []ClaimStatus{
			ClaimStatusPending, ClaimStatusInProgress, ClaimStatusTestified,
			ClaimStatusAccepted, ClaimStatusRejected,
		} {
			out = append(out, ClaimStatusPattern(sessionID, status))
		}
		out = append(out, ClaimContextPattern(sessionID, "*"))
		out = append(out, TestamentContextPattern(sessionID, "*"))
		// Wildcard agent inbox — match any agent's directed deltas
		// across activation types so the UI sees every consult /
		// challenge / handoff / task being directed.
		for _, kind := range activationTypes {
			out = append(out, AgentInboxActionPattern(sessionID, "*", RelationshipSubject, kind))
		}
	}
	return out
}

// busSubscriptionQueueCapDefault is the upper bound on unique
// in-flight deltas the bus can deliver to one inbox subscription
// before the bus's drop-oldest-by-priority eviction kicks in. It
// anchors the dedup LRU, which must be at least this size to avoid
// dedup-eviction racing the bus drop window. The value is derived
// from the host's GOMAXPROCS so it scales with hardware:
//
//	cap = max(perCoreFloor × GOMAXPROCS, expectedFanIn ×
//	          messagesPerTurn × replicasPerAgent × safetyFactor)
//
// where the right-hand term is the worst-case observed fan-in (every
// peer agent emitting a full turn's worth of deltas at maximum replica
// concurrency). With default coefficients on an 8-core machine this
// resolves to 2048 — the same magnitude as the previous hand-picked
// 4096 but expressed as a function of host capacity, peer count, and
// observed turn volume rather than a literal.
var busSubscriptionQueueCapDefault = computeBusSubscriptionQueueCap()

const (
	// inboxQueuePerCoreFloor is the floor scaling factor: each core
	// gets at least this many slots so a high-core host can absorb
	// proportionally more pending deltas. The product (procs ×
	// floor) is the lower bound returned when the fan-in formula
	// produces a smaller result.
	inboxQueuePerCoreFloor = 256

	// inboxQueueExpectedFanIn anchors the upper-bound formula in the
	// observed maximum number of distinct agents that can publish
	// deltas to one inbox simultaneously. Sized off the agent
	// taxonomy (architect, librarian, orchestrator, engineer,
	// guardian, inspector, tester, archivalist, academic, designer,
	// guide ≈ 11 + headroom).
	inboxQueueExpectedFanIn = 12

	// inboxQueueMessagesPerTurn anchors how many deltas each peer
	// loop emits on a single LLM iteration (claims/testimony
	// architecture: ~1 claim + 5–10 tool artifacts + 1 testament
	// flush ≈ 12–15).
	inboxQueueMessagesPerTurn = 15

	// inboxQueueReplicasPerAgent anchors the maximum concurrent
	// replicas of one peer agent that can run in parallel
	// (RequestReplicaPool default ceilings).
	inboxQueueReplicasPerAgent = 8

	// inboxQueueSafetyFactor reserves headroom for short bursts
	// above the steady-state arrival rate. Two means we can absorb a
	// 2× momentary spike without bus drops.
	inboxQueueSafetyFactor = 2
)

func computeBusSubscriptionQueueCap() int {
	procs := runtime.GOMAXPROCS(0)
	if procs < 1 {
		procs = 1
	}
	floor := procs * inboxQueuePerCoreFloor
	upper := inboxQueueExpectedFanIn *
		inboxQueueMessagesPerTurn *
		inboxQueueReplicasPerAgent *
		inboxQueueSafetyFactor
	if floor > upper {
		return floor
	}
	return upper
}

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

	// queueCap is the bus per-sub queue cap, used to size the dedup
	// LRU once the actual pattern count is known at Start.
	queueCap int

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
	queueCap := cfg.BusSubscriptionQueueCap
	if queueCap <= 0 {
		queueCap = busSubscriptionQueueCapDefault
	}
	// Pre-size dedup at queueCap (one pattern's worth). Start grows
	// it to queueCap × pattern count once patterns resolve.
	return &ClaimsInbox{
		agentID:         cfg.AgentID,
		sessionID:       cfg.SessionID,
		role:            role,
		subscriber:      subscribeOrNoop(cfg.Subscriber),
		board:           cfg.Board,
		onResolved:      cfg.OnResolved,
		seen:            newDedupLRU(queueCap),
		expectations:    make(map[string]*Expectation),
		orphans:         make(map[string][]orphanedInboxDelta),
		expectationSubs: make(map[string][]DeltaSubscription),
		queueCap:        queueCap,
	}, nil
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
		return fmt.Errorf("subscribe %q: %w", pattern, err)
	}
	slog.Info("claims_inbox_subscribed",
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
			CanonicalClaimTopic(sessionID, e.ClaimID, DeltaActionTestamentSubmitted),
			CanonicalClaimTopic(sessionID, e.ClaimID, DeltaActionClaimTransitioned),
			ClaimStatusTopic(sessionID, e.ClaimID, ClaimStatusTestified),
			ClaimStatusTopic(sessionID, e.ClaimID, ClaimStatusAccepted),
			ClaimStatusTopic(sessionID, e.ClaimID, ClaimStatusRejected),
			ClaimStatusTopic(sessionID, e.ClaimID, ClaimStatusSuperseded),
		}
	case DeltaKindValidation:
		return []string{
			CanonicalClaimTopic(sessionID, e.ClaimID, DeltaActionValidationEvaluated),
			ValidationVerdictPattern(sessionID, ValidationStatusPassed),
			ValidationVerdictPattern(sessionID, ValidationStatusFailed),
		}
	case DeltaKindClaimStatus:
		return []string{
			CanonicalClaimTopic(sessionID, e.ClaimID, DeltaActionClaimTransitioned),
			ClaimStatusTopic(sessionID, e.ClaimID, ClaimStatusTestified),
			ClaimStatusTopic(sessionID, e.ClaimID, ClaimStatusAccepted),
			ClaimStatusTopic(sessionID, e.ClaimID, ClaimStatusRejected),
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
	i.deliveredByClass[DeltaClass(d)].Add(1)
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

	if entry != nil && i.onResolved != nil {
		i.onResolved(entry)
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
	case InboxDelta:
		return delta.AgentID
	case *InboxDelta:
		return delta.AgentID
	case TestamentDelta:
		return delta.IssuerAgentID
	case *TestamentDelta:
		return delta.IssuerAgentID
	case ValidationDelta:
		return delta.IssuerAgentID
	case *ValidationDelta:
		return delta.IssuerAgentID
	case ClaimStatusDelta:
		return delta.SubjectAgentID
	case *ClaimStatusDelta:
		return delta.SubjectAgentID
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
		return nil, nil
	}
	if !i.seen.observe(key, seq) {
		return nil, nil
	}

	// Match against expectations first (O(1) by claim_id).
	claimID := deltaClaimID(d)
	if claimID != "" && i.expectations != nil {
		if exp, ok := i.expectations[claimID]; ok && deltaMatchesExpectation(d, exp.ExpectedDelta) {
			delete(i.expectations, claimID)
			releaseSubs := i.releaseExpectationSubscriptionsLocked(claimID)
			i.matchCount.Add(1)
			return ResolveEntryPoint(i.board, d, exp.Priority, exp), releaseSubs
		}
	}

	// Standing subscription matching by role.
	if i.matchesStandingSubscription(d) {
		priority := derivePriority(d)
		i.matchCount.Add(1)
		return ResolveEntryPoint(i.board, d, priority, nil), nil
	}

	// Unmatched response-like deltas may have beaten expectation
	// registration. Buffer briefly by claim ID so Expect can reconcile
	// without polling the board or replaying the bus.
	i.stashOrphanIfResponseLocked(d)
	return nil, nil
}

func deltaDedupKey(d Delta) string {
	if d == nil {
		return ""
	}
	switch delta := d.(type) {
	case CanonicalDelta:
		return canonicalSemanticDedupKey(delta)
	case *CanonicalDelta:
		if delta == nil {
			return ""
		}
		return canonicalSemanticDedupKey(*delta)
	default:
		return d.DeltaKey()
	}
}

func canonicalSemanticDedupKey(delta CanonicalDelta) string {
	claimID := delta.ClaimID()
	switch delta.Action {
	case DeltaActionClaimDirected:
		directed := ""
		relationship := ""
		if delta.Delivery != nil {
			relationship = delta.Delivery.Relationship
			if len(delta.Delivery.To) > 0 {
				directed = delta.Delivery.To[0].RouteKey()
			}
		}
		return inboxDeltaKey(claimID, directed, relationship)
	case DeltaActionTestamentSubmitted:
		return DeltaKindTestament + "|" + delta.TestamentID()
	case DeltaActionValidationEvaluated:
		return DeltaKindValidation + "|" + delta.ValidationID()
	case DeltaActionClaimTransitioned:
		return DeltaKindClaimStatus + "|" + claimID + "|" + string(delta.ClaimToStatus())
	default:
		if key := strings.TrimSpace(delta.Key); key != "" {
			return key
		}
		return string(delta.Action) + "|" + claimID
	}
}

func deltaMatchesExpectation(d Delta, expected string) bool {
	expected = strings.TrimSpace(expected)
	if d == nil || expected == "" {
		return false
	}
	if d.DeltaKind() == expected {
		return true
	}
	switch expected {
	case DeltaKindTestament:
		if d.DeltaKind() == string(DeltaActionTestamentSubmitted) {
			return true
		}
		if d.DeltaKind() == string(DeltaActionClaimTransitioned) {
			return claimTransitionDeltaResolvesExpectation(d)
		}
		if d.DeltaKind() == DeltaKindClaimStatus {
			return claimStatusDeltaResolvesExpectation(d)
		}
		return false
	case DeltaKindValidation:
		return d.DeltaKind() == string(DeltaActionValidationEvaluated)
	case DeltaKindClaimStatus:
		return d.DeltaKind() == string(DeltaActionClaimTransitioned)
	default:
		return false
	}
}

func claimTransitionDeltaResolvesExpectation(d Delta) bool {
	switch delta := d.(type) {
	case CanonicalDelta:
		return claimStatusResolvesExpectation(delta.ClaimToStatus())
	case *CanonicalDelta:
		return delta != nil && claimStatusResolvesExpectation(delta.ClaimToStatus())
	default:
		return false
	}
}

func claimStatusDeltaResolvesExpectation(d Delta) bool {
	switch delta := d.(type) {
	case ClaimStatusDelta:
		return claimStatusResolvesExpectation(delta.ToStatus)
	case *ClaimStatusDelta:
		return delta != nil && claimStatusResolvesExpectation(delta.ToStatus)
	default:
		return false
	}
}

func claimStatusResolvesExpectation(status ClaimStatus) bool {
	return status == ClaimStatusTestified || status.IsTerminal()
}

const orphanInboxDeltaMaxAge = 10 * time.Minute

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
}

func (i *ClaimsInbox) orphanLimitLocked() int {
	limit := i.queueCap
	if limit <= 0 {
		limit = busSubscriptionQueueCapDefault
	}
	if limit < 16 {
		limit = 16
	}
	if limit > 1024 {
		limit = 1024
	}
	return limit
}

func deltaMayResolveFutureExpectation(d Delta) bool {
	switch delta := d.(type) {
	case CanonicalDelta:
		return canonicalDeltaMayResolveFutureExpectation(delta)
	case *CanonicalDelta:
		return delta != nil && canonicalDeltaMayResolveFutureExpectation(*delta)
	case TestamentDelta, *TestamentDelta:
		return true
	case ValidationDelta, *ValidationDelta:
		return true
	case ClaimStatusDelta:
		return claimStatusResolvesExpectation(delta.ToStatus)
	case *ClaimStatusDelta:
		return delta != nil && claimStatusResolvesExpectation(delta.ToStatus)
	default:
		return false
	}
}

func canonicalDeltaMayResolveFutureExpectation(delta CanonicalDelta) bool {
	if !DeltaActionMayCompleteExpectedWork(delta.Action) {
		return false
	}
	switch delta.Action {
	case DeltaActionTestamentSubmitted, DeltaActionValidationEvaluated:
		return true
	case DeltaActionClaimTransitioned:
		return claimStatusResolvesExpectation(delta.ClaimToStatus())
	default:
		return false
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
// confirms identity (for InboxDelta) and rejects deltas that would
// arrive only through patterns the role does not hold.
//
// Issuer-side return paths for directed claims flow through Expect()
// registered at post_action time, NOT through standing subscriptions.
// Lifecycle and observational testaments emitted with no parent claim
// never publish a TestamentDelta (resolveClaimForTestament returns
// nil), so the bus never delivers them here at all.
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
	case InboxDelta:
		if IsSystemInternalAction(delta.ActionKind) {
			return false
		}
		if role.Has(RoleObserver) {
			return true
		}
		return role.Has(RoleSubject) && delta.AgentID == i.agentID
	case *InboxDelta:
		if delta == nil {
			return false
		}
		if IsSystemInternalAction(delta.ActionKind) {
			return false
		}
		if role.Has(RoleObserver) {
			return true
		}
		return role.Has(RoleSubject) && delta.AgentID == i.agentID
	case TestamentDelta:
		if IsSystemInternalAction(delta.ActionKind) {
			return false
		}
		return role.Has(RoleAuditor) || role.Has(RoleArchivist)
	case *TestamentDelta:
		if delta == nil {
			return false
		}
		if IsSystemInternalAction(delta.ActionKind) {
			return false
		}
		return role.Has(RoleAuditor) || role.Has(RoleArchivist)
	case ClaimStatusDelta:
		if IsSystemInternalAction(delta.ActionKind) {
			return false
		}
		return claimStatusMatchesRole(role, delta.ToStatus) || role.Has(RoleObserver)
	case *ClaimStatusDelta:
		if delta == nil {
			return false
		}
		if IsSystemInternalAction(delta.ActionKind) {
			return false
		}
		return claimStatusMatchesRole(role, delta.ToStatus) || role.Has(RoleObserver)
	case ValidationDelta:
		// Auto-acceptance is mirrored onto the claim-accepted topic as
		// a ValidationDelta, so the rendering observer must accept it to
		// close the cycle. Action-taking roles still use expectations.
		return role.Has(RoleObserver) && delta.ClaimAutoAccepted
	case *ValidationDelta:
		if delta == nil {
			return false
		}
		return role.Has(RoleObserver) && delta.ClaimAutoAccepted
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
	switch delta.Action {
	case DeltaActionClaimDirected:
		if IsSystemInternalAction(delta.ClaimActionType()) {
			return false
		}
		if role.Has(RoleObserver) {
			return true
		}
		return role.Has(RoleSubject) && delta.DeliveredTo(i.agentID)
	case DeltaActionTestamentSubmitted:
		if IsSystemInternalAction(delta.ClaimActionType()) {
			return false
		}
		return role.Has(RoleAuditor) || role.Has(RoleArchivist) || role.Has(RoleObserver)
	case DeltaActionClaimTransitioned:
		if IsSystemInternalAction(delta.ClaimActionType()) {
			return false
		}
		return claimStatusMatchesRole(role, delta.ClaimToStatus()) || role.Has(RoleObserver)
	case DeltaActionValidationEvaluated, DeltaActionClaimProgressed:
		return role.Has(RoleObserver)
	default:
		return false
	}
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
	case DeltaActionClaimDirected:
		return inboxDeltaPriority(delta.ClaimActionType())
	case DeltaActionTestamentSubmitted:
		return PriorityResponse
	case DeltaActionValidationEvaluated:
		return PriorityEvaluation
	case DeltaActionClaimTransitioned:
		return claimStatusPriority(delta.ClaimToStatus())
	case DeltaActionClaimProgressed:
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
