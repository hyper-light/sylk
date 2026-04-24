package claims

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
)

// InboxConfig bundles construction parameters for a ClaimsInbox.
type InboxConfig struct {
	AgentID   string
	SessionID string

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

	// Patterns is the initial set of bus topic patterns the inbox
	// will subscribe to. When empty, DefaultInboxPatterns is used.
	Patterns []string
}

// DefaultInboxPatterns returns the standard pattern set an agent
// replica subscribes to: every inbox-delta directed at the agent,
// every claim status transition in the session, every validation
// verdict, every phase transition.
func DefaultInboxPatterns(sessionID, agentID string) []string {
	return []string{
		AgentInboxPattern(sessionID, agentID),
		joinTopic(
			TopicNamespace,
			wildcardOrSegment(sessionID),
			TopicSegmentClaim, TopicWildcard, TopicWildcard,
		),
		joinTopic(
			TopicNamespace,
			wildcardOrSegment(sessionID),
			TopicSegmentValidation, TopicWildcard, TopicWildcard,
		),
		PhasePattern(sessionID),
	}
}

// ClaimsInbox is the per-replica event-driven intake surface.
//
// Deltas arrive from bus subscriptions. The inbox matches each
// against registered expectations (from the agent's emissions) or
// standing subscriptions (from the agent's identity). Matched deltas
// are resolved into GraphEntryPoints and dispatched to OnResolved
// immediately. Unmatched deltas are discarded.
//
// Thread-safe. Public methods acquire mu.
type ClaimsInbox struct {
	mu sync.Mutex

	agentID   string
	sessionID string

	subscriber    DeltaSubscriber
	subscriptions []DeltaSubscription

	// seen is the dedup table: DeltaKey → highest Sequence applied.
	seen map[string]uint64

	// expectations maps claim_id → *Expectation. Populated by
	// Expect(), consumed when a matching delta arrives via Ingest.
	expectations map[string]*Expectation

	board *ClaimsBoard

	// onResolved is called when a delta matches. Runs on the bus
	// subscriber's goroutine.
	onResolved func(entry *GraphEntryPoint)

	// matchCount tracks how many deltas have matched (for tests).
	matchCount atomic.Uint64

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
	return &ClaimsInbox{
		agentID:      cfg.AgentID,
		sessionID:    cfg.SessionID,
		subscriber:   subscribeOrNoop(cfg.Subscriber),
		board:        cfg.Board,
		onResolved:   cfg.OnResolved,
		seen:         make(map[string]uint64),
		expectations: make(map[string]*Expectation),
	}, nil
}

// ────────────────────────────────────────────────────────────────────
// Subscription lifecycle
// ────────────────────────────────────────────────────────────────────

// Start subscribes the inbox to patterns. When patterns is empty,
// DefaultInboxPatterns is used.
func (i *ClaimsInbox) Start(patterns []string) error {
	if i == nil {
		return fmt.Errorf("nil inbox")
	}
	if i.closed.Load() {
		return fmt.Errorf("inbox closed")
	}
	if len(patterns) == 0 {
		patterns = DefaultInboxPatterns(i.sessionID, i.agentID)
	}
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
		return fmt.Errorf("subscribe %q: %w", pattern, err)
	}
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
	i.mu.Lock()
	if i.expectations != nil {
		i.expectations[e.ClaimID] = e
	}
	i.mu.Unlock()
}

// ────────────────────────────────────────────────────────────────────
// Delta intake
// ────────────────────────────────────────────────────────────────────

// Ingest accepts a delta from the bus handler. Deduplicates by
// (DeltaKey, DeltaSequence). Matches against expectations and
// standing subscriptions. Matched deltas are resolved into
// GraphEntryPoints and dispatched to OnResolved immediately.
// Unmatched deltas are discarded.
func (i *ClaimsInbox) Ingest(d Delta) {
	if i == nil || d == nil || i.closed.Load() {
		return
	}
	i.mu.Lock()
	entry := i.ingestLocked(d)
	i.mu.Unlock()

	if entry != nil && i.onResolved != nil {
		i.onResolved(entry)
	}
}

func (i *ClaimsInbox) ingestLocked(d Delta) *GraphEntryPoint {
	// Dedup.
	key := d.DeltaKey()
	seq := d.DeltaSequence()
	if i.seen == nil {
		return nil
	}
	if existing, ok := i.seen[key]; ok && existing >= seq {
		return nil
	}
	i.seen[key] = seq

	// Match against expectations first (O(1) by claim_id).
	claimID := deltaClaimID(d)
	if claimID != "" && i.expectations != nil {
		if exp, ok := i.expectations[claimID]; ok && exp.ExpectedDelta == d.DeltaKind() {
			delete(i.expectations, claimID)
			i.matchCount.Add(1)
			return ResolveEntryPoint(i.board, d, exp.Priority, exp)
		}
	}

	// Standing subscription matching by agent identity.
	if i.matchesStandingSubscription(d) {
		priority := derivePriority(d)
		i.matchCount.Add(1)
		return ResolveEntryPoint(i.board, d, priority, nil)
	}

	// Unmatched — discard.
	return nil
}

// matchesStandingSubscription checks whether a delta matches this
// agent's standing subscriptions — derived from the agent's identity.
func (i *ClaimsInbox) matchesStandingSubscription(d Delta) bool {
	switch delta := d.(type) {
	case InboxDelta:
		return strings.TrimSpace(delta.AgentID) == i.agentID
	case *InboxDelta:
		return strings.TrimSpace(delta.AgentID) == i.agentID
	case TestamentDelta:
		return strings.TrimSpace(delta.IssuerAgentID) == i.agentID
	case *TestamentDelta:
		return strings.TrimSpace(delta.IssuerAgentID) == i.agentID
	case ValidationDelta:
		return strings.TrimSpace(delta.IssuerAgentID) == i.agentID ||
			strings.TrimSpace(delta.SubjectAgentID) == i.agentID
	case *ValidationDelta:
		return strings.TrimSpace(delta.IssuerAgentID) == i.agentID ||
			strings.TrimSpace(delta.SubjectAgentID) == i.agentID
	case ClaimStatusDelta:
		return strings.TrimSpace(delta.SubjectAgentID) == i.agentID ||
			strings.TrimSpace(delta.IssuerAgentID) == i.agentID
	case *ClaimStatusDelta:
		return strings.TrimSpace(delta.SubjectAgentID) == i.agentID ||
			strings.TrimSpace(delta.IssuerAgentID) == i.agentID
	case PhaseDelta, *PhaseDelta:
		return true // all agents observe phase transitions
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

// OverflowCount is retained for interface compatibility. The event-
// driven model has no internal buffer — the ChannelBus provides
// per-subscription bounded queues. Returns 0.
func (i *ClaimsInbox) OverflowCount() uint64 { return 0 }

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

// logUnmatchedDelta logs a discarded delta at debug level.
// Currently unused — the discard is silent. Uncomment for debugging.
func logUnmatchedDelta(agentID string, d Delta) {
	slog.Debug("claims_inbox_unmatched",
		"agent_id", agentID,
		"delta_kind", d.DeltaKind(),
		"delta_key", d.DeltaKey(),
		"sequence", d.DeltaSequence(),
	)
}
