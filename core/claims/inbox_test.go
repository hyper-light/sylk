package claims

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recordingBus is a DeltaBus that records subscriptions and exposes
// a Fire helper so tests drive inbox intake directly.
type recordingBus struct {
	mu    sync.Mutex
	subs  map[string][]DeltaHandler
	errOn string
}

func newRecordingBus() *recordingBus {
	return &recordingBus{subs: make(map[string][]DeltaHandler)}
}

func (b *recordingBus) PublishDelta(_ context.Context, _ string, _ Delta) error { return nil }

func (b *recordingBus) SubscribeDelta(pattern string, h DeltaHandler) (DeltaSubscription, error) {
	if b.errOn == pattern {
		return nil, errors.New("subscribe-denied")
	}
	b.mu.Lock()
	b.subs[pattern] = append(b.subs[pattern], h)
	b.mu.Unlock()
	return &recordingSub{bus: b, pattern: pattern, handler: h}, nil
}

func (b *recordingBus) Fire(pattern string, delta Delta) {
	b.mu.Lock()
	handlers := append([]DeltaHandler(nil), b.subs[pattern]...)
	b.mu.Unlock()
	for _, h := range handlers {
		h(delta)
	}
}

func (b *recordingBus) SubscriptionCount(pattern string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs[pattern])
}

type recordingSub struct {
	bus          *recordingBus
	pattern      string
	handler      DeltaHandler
	unsubscribed atomic.Bool
}

func (s *recordingSub) Topic() string { return s.pattern }
func (s *recordingSub) Unsubscribe() error {
	if !s.unsubscribed.CompareAndSwap(false, true) {
		return nil
	}
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()
	s.bus.subs[s.pattern] = nil
	return nil
}

// ────────────────────────────────────────────────────────────────────

func TestInbox_RequiresAgentAndSession(t *testing.T) {
	if _, err := NewClaimsInbox(InboxConfig{}); err == nil {
		t.Error("expected error for missing agent")
	}
	if _, err := NewClaimsInbox(InboxConfig{AgentID: "a"}); err == nil {
		t.Error("expected error for missing session")
	}
}

func TestInbox_StartSubscribesRolePatterns(t *testing.T) {
	bus := newRecordingBus()
	inbox, err := NewClaimsInbox(InboxConfig{
		AgentID: "eng", SessionID: "sess", Subscriber: bus,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := inbox.Start(nil); err != nil {
		t.Fatal(err)
	}
	// Default role (RoleSubject) → one narrow legacy pattern per
	// legitimate activation action type, plus two canonical directed-work
	// patterns. The broad
	// AgentInboxPattern(*.*) is NOT used because it would match
	// system-internal action types and trigger feedback loops.
	patterns := InboxPatternsFor(RoleSubject, "sess", "eng")
	activation := AgentActivationActionTypes()
	wantCount := len(activation) + 2 // +2 canonical directed
	if len(patterns) != wantCount {
		t.Fatalf("expected RoleSubject to derive %d patterns (activation + canonical), got %d (%v)", wantCount, len(patterns), patterns)
	}
	for _, p := range patterns {
		if bus.SubscriptionCount(p) == 0 {
			t.Errorf("no subscription on pattern %q", p)
		}
	}
	// System-typed inbox patterns must NOT be subscribed.
	systemBlocked := []string{
		AgentInboxActionPattern("sess", "eng", RelationshipSubject, ActionTypeBoot),
		AgentInboxActionPattern("sess", "eng", RelationshipSubject, ActionTypeActivation),
		AgentInboxActionPattern("sess", "eng", RelationshipSubject, ActionTypeShutdown),
	}
	for _, p := range systemBlocked {
		if bus.SubscriptionCount(p) != 0 {
			t.Errorf("subject role unexpectedly subscribed to system pattern %q", p)
		}
	}
	// The broad firehose patterns are NOT subscribed.
	broad := []string{
		"claims.sess.claim.*.*",
		"claims.sess.validation.*.*",
		"claims.sess.phase.*",
	}
	for _, p := range broad {
		if bus.SubscriptionCount(p) != 0 {
			t.Errorf("subject role unexpectedly subscribed to broad pattern %q", p)
		}
	}
}

func TestInbox_RolePatternsForRoleAuditor(t *testing.T) {
	patterns := InboxPatternsFor(RoleSubject|RoleAuditor, "sess", "inspector")
	// RoleSubject is now expressed as N narrow per-action-type
	// patterns (storm-prevention) instead of the broad
	// AgentInboxPattern(*.*). RoleAuditor adds the testified status
	// pattern unchanged.
	for _, ty := range AgentActivationActionTypes() {
		want := AgentInboxActionPattern("sess", "inspector", RelationshipSubject, ty)
		if !sliceContains(patterns, want) {
			t.Errorf("RoleSubject|RoleAuditor missing subject pattern %q", want)
		}
	}
	if !sliceContains(patterns, ClaimStatusPattern("sess", ClaimStatusTestified)) {
		t.Errorf("RoleSubject|RoleAuditor missing testified status pattern")
	}
	// The broad firehose pattern must NOT be present.
	if sliceContains(patterns, AgentInboxPattern("sess", "inspector")) {
		t.Errorf("RoleSubject|RoleAuditor unexpectedly subscribed to broad AgentInboxPattern")
	}
}

func TestInbox_RolePatternsForRolePhaseObserver(t *testing.T) {
	patterns := InboxPatternsFor(RoleSubject|RolePhaseObserver, "sess", "orch")
	var sawPhase bool
	for _, p := range patterns {
		if p == PhasePattern("sess") {
			sawPhase = true
		}
	}
	if !sawPhase {
		t.Errorf("RolePhaseObserver missing phase pattern, got %v", patterns)
	}
}

func TestInbox_RolePatternsForRoleRemediator(t *testing.T) {
	patterns := InboxPatternsFor(RoleSubject|RoleRemediator, "sess", "architect")
	var sawRejected bool
	for _, p := range patterns {
		if p == ClaimStatusPattern("sess", ClaimStatusRejected) {
			sawRejected = true
		}
	}
	if !sawRejected {
		t.Errorf("RoleRemediator missing rejected pattern, got %v", patterns)
	}
}

func TestInbox_IngestDedupsBySequence(t *testing.T) {
	inbox, _ := NewClaimsInbox(InboxConfig{AgentID: "eng", SessionID: "sess"})
	d := InboxDelta{ClaimID: "c1", AgentID: "eng", Relationship: RelationshipSubject, Sequence: 5}
	inbox.Ingest(d)
	inbox.Ingest(d) // duplicate
	if n := inbox.Len(); n != 1 {
		t.Fatalf("expected 1 match, got %d", n)
	}
}

func TestInbox_IngestDropsOlderSequenceForSameKey(t *testing.T) {
	inbox, _ := NewClaimsInbox(InboxConfig{AgentID: "eng", SessionID: "sess"})
	d1 := InboxDelta{ClaimID: "c1", AgentID: "eng", Relationship: RelationshipSubject, Sequence: 10}
	d2 := InboxDelta{ClaimID: "c1", AgentID: "eng", Relationship: RelationshipSubject, Sequence: 5}
	inbox.Ingest(d1)
	inbox.Ingest(d2) // older — dropped
	if n := inbox.Len(); n != 1 {
		t.Fatalf("expected 1, got %d", n)
	}
}

func TestInbox_StandingSubscription_InboxDelta(t *testing.T) {
	var received atomic.Pointer[GraphEntryPoint]
	inbox, _ := NewClaimsInbox(InboxConfig{
		AgentID:   "eng",
		SessionID: "sess",
		OnResolved: func(entry *GraphEntryPoint) {
			received.Store(entry)
		},
	})
	inbox.Ingest(InboxDelta{
		ClaimID:      "c1",
		AgentID:      "eng",
		Relationship: RelationshipSubject,
		ActionKind:   ActionTypeTask,
		Sequence:     1,
	})
	entry := received.Load()
	if entry == nil {
		t.Fatal("expected OnResolved to be called")
	}
	if entry.Priority != PriorityDirected {
		t.Fatalf("expected PriorityDirected, got %d", entry.Priority)
	}
	if entry.Expectation != nil {
		t.Fatal("expected nil Expectation for standing subscription")
	}
}

func TestInbox_PhaseDelta_RoleGated(t *testing.T) {
	// PhaseDelta is delivered only to RolePhaseObserver inboxes.
	var observerCalled, subjectCalled atomic.Bool

	subject, _ := NewClaimsInbox(InboxConfig{
		AgentID:   "eng",
		SessionID: "sess",
		Role:      RoleSubject,
		OnResolved: func(_ *GraphEntryPoint) {
			subjectCalled.Store(true)
		},
	})
	subject.Ingest(PhaseDelta{
		BoardID:   "board-1",
		FromPhase: BoardPhaseImplementation,
		ToPhase:   BoardPhaseValidation,
		Sequence:  5,
	})
	if subjectCalled.Load() {
		t.Fatal("RoleSubject inbox should not match PhaseDelta")
	}

	observer, _ := NewClaimsInbox(InboxConfig{
		AgentID:   "orch",
		SessionID: "sess",
		Role:      RoleSubject | RolePhaseObserver,
		OnResolved: func(_ *GraphEntryPoint) {
			observerCalled.Store(true)
		},
	})
	observer.Ingest(PhaseDelta{
		BoardID:   "board-1",
		FromPhase: BoardPhaseImplementation,
		ToPhase:   BoardPhaseValidation,
		Sequence:  5,
	})
	if !observerCalled.Load() {
		t.Fatal("RolePhaseObserver inbox should match PhaseDelta")
	}
}

func TestInbox_UnmatchedDeltaDiscarded(t *testing.T) {
	var called atomic.Bool
	inbox, _ := NewClaimsInbox(InboxConfig{
		AgentID:   "eng",
		SessionID: "sess",
		OnResolved: func(_ *GraphEntryPoint) {
			called.Store(true)
		},
	})
	// InboxDelta addressed to a different agent — discarded.
	inbox.Ingest(InboxDelta{
		ClaimID:      "c1",
		AgentID:      "other-agent",
		Relationship: RelationshipSubject,
		Sequence:     1,
	})
	if called.Load() {
		t.Fatal("OnResolved should not be called for unmatched delta")
	}
	if inbox.Len() != 0 {
		t.Fatalf("expected 0 matches, got %d", inbox.Len())
	}
}

func TestInbox_ExpectAndMatch(t *testing.T) {
	var received atomic.Pointer[GraphEntryPoint]
	inbox, _ := NewClaimsInbox(InboxConfig{
		AgentID:   "eng",
		SessionID: "sess",
		OnResolved: func(entry *GraphEntryPoint) {
			received.Store(entry)
		},
	})
	inbox.Expect(&Expectation{
		ClaimID:       "c-42",
		ExpectedDelta: DeltaKindTestament,
		ActionID:      "action-1",
		IssuedAt:      time.Now(),
		Priority:      PriorityResponse,
	})

	inbox.Ingest(TestamentDelta{
		ClaimID:     "c-42",
		TestamentID: "t-1",
		Sequence:    10,
	})

	entry := received.Load()
	if entry == nil {
		t.Fatal("expected OnResolved to be called")
	}
	if entry.Expectation == nil {
		t.Fatal("expected non-nil Expectation on matched entry")
	}
	if entry.Expectation.ClaimID != "c-42" {
		t.Fatalf("expected claim_id c-42, got %s", entry.Expectation.ClaimID)
	}
	if entry.Priority != PriorityResponse {
		t.Fatalf("expected PriorityResponse, got %d", entry.Priority)
	}
}

func TestInbox_ExpectMatchesCanonicalTestamentSubmitted(t *testing.T) {
	var received atomic.Pointer[GraphEntryPoint]
	inbox, _ := NewClaimsInbox(InboxConfig{
		AgentID:   "architect",
		SessionID: "sess",
		OnResolved: func(entry *GraphEntryPoint) {
			received.Store(entry)
		},
	})
	inbox.Expect(&Expectation{
		ClaimID:       "claim-42",
		ExpectedDelta: DeltaKindTestament,
		Priority:      PriorityResponse,
	})

	inbox.Ingest(NewCanonicalDelta(
		DeltaActionTestamentSubmitted,
		"sess",
		"board",
		2,
		time.Now(),
		DegradedAgentRef("librarian", "test"),
		[]DeltaRef{
			{Role: "claim", Type: RelatedTypeClaim, ID: "claim-42"},
			{Role: "testament", Type: RelatedTypeTestament, ID: "testament-1"},
		},
		nil,
		map[string]any{
			"claim": map[string]any{
				"id":     "claim-42",
				"action": string(ActionTypeConsultation),
				"status": string(ClaimStatusTestified),
			},
			"testaments": []map[string]any{{"id": "testament-1", "context": "done"}},
		},
	))

	entry := received.Load()
	if entry == nil || entry.Expectation == nil {
		t.Fatalf("expected canonical testament to resolve expectation, got %#v", entry)
	}
	if entry.Delta.DeltaKind() != string(DeltaActionTestamentSubmitted) {
		t.Fatalf("delta kind = %q", entry.Delta.DeltaKind())
	}
}

func TestInbox_ExpectMatchesCanonicalClaimTransitioned(t *testing.T) {
	var received atomic.Pointer[GraphEntryPoint]
	inbox, _ := NewClaimsInbox(InboxConfig{
		AgentID:   "architect",
		SessionID: "sess",
		OnResolved: func(entry *GraphEntryPoint) {
			received.Store(entry)
		},
	})
	inbox.Expect(&Expectation{
		ClaimID:       "claim-42",
		ExpectedDelta: DeltaKindClaimStatus,
		Priority:      PriorityResponse,
	})

	inbox.Ingest(NewCanonicalDelta(
		DeltaActionClaimTransitioned,
		"sess",
		"board",
		3,
		time.Now(),
		DegradedAgentRef("system", "test"),
		claimRefs("claim-42"),
		nil,
		map[string]any{
			"claim": map[string]any{
				"id":        "claim-42",
				"action":    string(ActionTypeConsultation),
				"status":    string(ClaimStatusAccepted),
				"to_status": string(ClaimStatusAccepted),
			},
		},
	))

	entry := received.Load()
	if entry == nil || entry.Expectation == nil {
		t.Fatalf("expected canonical transition to resolve expectation, got %#v", entry)
	}
}

func TestInbox_CanonicalClaimProgressedDoesNotWakeSubject(t *testing.T) {
	var called atomic.Bool
	inbox, _ := NewClaimsInbox(InboxConfig{
		AgentID:   "librarian",
		SessionID: "sess",
		Role:      RoleSubject,
		OnResolved: func(_ *GraphEntryPoint) {
			called.Store(true)
		},
	})
	inbox.Ingest(NewCanonicalDelta(
		DeltaActionClaimProgressed,
		"sess",
		"board",
		4,
		time.Now(),
		DegradedAgentRef("librarian", "test"),
		claimRefs("claim-42"),
		nil,
		map[string]any{
			"claim":    map[string]any{"id": "claim-42", "action": string(ActionTypeConsultation), "status": string(ClaimStatusInProgress)},
			"progress": map[string]any{"state": "working", "message": "reading"},
		},
	))
	if called.Load() {
		t.Fatal("claim.progressed must not wake subject ProcessEntry by default")
	}
}

func TestInbox_CanonicalClaimProgressedDoesNotResolveExpectation(t *testing.T) {
	var called atomic.Bool
	inbox, _ := NewClaimsInbox(InboxConfig{
		AgentID:   "architect",
		SessionID: "sess",
		OnResolved: func(_ *GraphEntryPoint) {
			called.Store(true)
		},
	})
	inbox.Expect(&Expectation{
		ClaimID:       "claim-42",
		ExpectedDelta: DeltaKindTestament,
		Priority:      PriorityResponse,
	})
	inbox.Ingest(NewCanonicalDelta(
		DeltaActionClaimProgressed,
		"sess",
		"board",
		5,
		time.Now(),
		DegradedAgentRef("librarian", "test"),
		claimRefs("claim-42"),
		nil,
		map[string]any{
			"claim":    map[string]any{"id": "claim-42", "action": string(ActionTypeConsultation), "status": string(ClaimStatusInProgress)},
			"progress": map[string]any{"state": "working", "message": "reading"},
		},
	))
	if called.Load() {
		t.Fatal("claim.progressed resolved a testament expectation")
	}
	if deltaMatchesExpectation(NewCanonicalDelta(
		DeltaActionClaimProgressed,
		"sess",
		"board",
		6,
		time.Now(),
		DegradedAgentRef("librarian", "test"),
		claimRefs("claim-42"),
		nil,
		nil,
	), DeltaKindTestament) {
		t.Fatal("claim.progressed matched testament expectation")
	}
	if canonicalDeltaMayResolveFutureExpectation(NewCanonicalDelta(
		DeltaActionClaimProgressed,
		"sess",
		"board",
		7,
		time.Now(),
		DegradedAgentRef("librarian", "test"),
		claimRefs("claim-42"),
		nil,
		nil,
	)) {
		t.Fatal("claim.progressed was stashed as future response")
	}
}

func TestInbox_ExpectConsumesOnMatch(t *testing.T) {
	var count atomic.Int32
	inbox, _ := NewClaimsInbox(InboxConfig{
		AgentID:   "eng",
		SessionID: "sess",
		OnResolved: func(_ *GraphEntryPoint) {
			count.Add(1)
		},
	})
	inbox.Expect(&Expectation{
		ClaimID:       "c-42",
		ExpectedDelta: DeltaKindTestament,
		Priority:      PriorityResponse,
	})

	inbox.Ingest(TestamentDelta{ClaimID: "c-42", TestamentID: "t-1", Sequence: 1})
	// Second ingest — expectation consumed. No standing subscription
	// match either (IssuerAgentID doesn't match "eng").
	inbox.Ingest(TestamentDelta{ClaimID: "c-42", TestamentID: "t-2", Sequence: 2})
	if count.Load() != 1 {
		t.Fatalf("expected 1 OnResolved call (expectation consumed), got %d", count.Load())
	}
}

func TestInbox_IngestAfterCloseIgnored(t *testing.T) {
	var called atomic.Bool
	inbox, _ := NewClaimsInbox(InboxConfig{
		AgentID:   "eng",
		SessionID: "sess",
		OnResolved: func(_ *GraphEntryPoint) {
			called.Store(true)
		},
	})
	_ = inbox.Close()
	inbox.Ingest(InboxDelta{ClaimID: "c1", AgentID: "eng", Relationship: RelationshipSubject, Sequence: 1})
	if called.Load() {
		t.Error("closed inbox should not dispatch")
	}
}

func TestInbox_BusDeliveryDispatchesToIngest(t *testing.T) {
	bus := newRecordingBus()
	var called atomic.Bool
	inbox, _ := NewClaimsInbox(InboxConfig{
		AgentID:    "eng",
		SessionID:  "sess",
		Subscriber: bus,
		OnResolved: func(_ *GraphEntryPoint) {
			called.Store(true)
		},
	})
	if err := inbox.Start([]string{"fixed-pattern"}); err != nil {
		t.Fatal(err)
	}
	d := InboxDelta{ClaimID: "c1", AgentID: "eng", Relationship: RelationshipSubject, Sequence: 1}
	bus.Fire("fixed-pattern", d)
	if !called.Load() {
		t.Error("expected OnResolved via bus delivery")
	}
}

func TestInbox_CloseIdempotent(t *testing.T) {
	bus := newRecordingBus()
	inbox, _ := NewClaimsInbox(InboxConfig{AgentID: "eng", SessionID: "sess", Subscriber: bus})
	_ = inbox.Start(nil)
	if err := inbox.Close(); err != nil {
		t.Fatal(err)
	}
	if err := inbox.Close(); err != nil {
		t.Errorf("second Close returned error: %v", err)
	}
}

func TestInbox_TestamentDelta_RoleGated(t *testing.T) {
	// Issuer-side testament delivery flows through Expect(), not
	// standing subscription. A subject-only inbox never matches a
	// TestamentDelta via the standing gate.
	var subjectCalled atomic.Bool
	subject, _ := NewClaimsInbox(InboxConfig{
		AgentID:   "eng",
		SessionID: "sess",
		Role:      RoleSubject,
		OnResolved: func(_ *GraphEntryPoint) {
			subjectCalled.Store(true)
		},
	})
	subject.Ingest(TestamentDelta{
		ClaimID:        "c1",
		TestamentID:    "t1",
		IssuerAgentID:  "eng",
		SubjectAgentID: "designer",
		Sequence:       1,
	})
	if subjectCalled.Load() {
		t.Fatal("RoleSubject inbox should NOT match TestamentDelta via standing — issuer-side path is Expect()")
	}

	// RoleAuditor matches every testament regardless of identity.
	var auditorCalled atomic.Bool
	auditor, _ := NewClaimsInbox(InboxConfig{
		AgentID:   "inspector",
		SessionID: "sess",
		Role:      RoleSubject | RoleAuditor,
		OnResolved: func(_ *GraphEntryPoint) {
			auditorCalled.Store(true)
		},
	})
	auditor.Ingest(TestamentDelta{
		ClaimID:        "c1",
		TestamentID:    "t1",
		IssuerAgentID:  "architect",
		SubjectAgentID: "designer",
		Sequence:       1,
	})
	if !auditorCalled.Load() {
		t.Fatal("RoleAuditor inbox should match every TestamentDelta")
	}
}

func TestInbox_ExpectSubscribesSpecificTestamentTopic(t *testing.T) {
	bus := newRecordingBus()
	var got *GraphEntryPoint
	inbox, err := NewClaimsInbox(InboxConfig{
		AgentID:    "architect",
		SessionID:  "sess",
		Role:       RoleSubject,
		Subscriber: bus,
		OnResolved: func(entry *GraphEntryPoint) {
			got = entry
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := inbox.Start(nil); err != nil {
		t.Fatal(err)
	}

	claimID := "claim-consult-1"
	topic := ClaimStatusTopic("sess", claimID, ClaimStatusTestified)
	if count := bus.SubscriptionCount(topic); count != 0 {
		t.Fatalf("precondition: exact expectation topic subscriptions = %d, want 0", count)
	}

	inbox.Expect(&Expectation{
		ClaimID:       claimID,
		ExpectedDelta: DeltaKindTestament,
		ActionID:      "action-1",
		Priority:      PriorityResponse,
	})
	if count := bus.SubscriptionCount(topic); count != 1 {
		t.Fatalf("exact expectation topic subscriptions = %d, want 1", count)
	}

	bus.Fire(topic, TestamentDelta{
		ClaimID:       claimID,
		TestamentID:   "testament-1",
		ActionKind:    ActionTypeConsultation,
		IssuerAgentID: "architect",
		Sequence:      1,
	})
	if got == nil {
		t.Fatal("expected testament delta to resolve via expectation")
	}
	if got.Expectation == nil || got.Expectation.ClaimID != claimID {
		t.Fatalf("expectation = %#v, want claim %q", got.Expectation, claimID)
	}
	if count := bus.SubscriptionCount(topic); count != 0 {
		t.Fatalf("one-shot expectation topic subscriptions after match = %d, want 0", count)
	}
}

func TestInbox_ExpectationReplaysEarlyCanonicalTestament(t *testing.T) {
	var got *GraphEntryPoint
	inbox, err := NewClaimsInbox(InboxConfig{
		AgentID:   "architect",
		SessionID: "sess",
		Role:      RoleSubject,
		OnResolved: func(entry *GraphEntryPoint) {
			got = entry
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	claimID := "claim-consult-early"
	delta := NewCanonicalDelta(
		DeltaActionTestamentSubmitted,
		"sess",
		"board",
		1,
		time.Now(),
		DegradedAgentRef("librarian", "test"),
		[]DeltaRef{
			{Role: "claim", Type: RelatedTypeClaim, ID: claimID},
			{Role: "testament", Type: RelatedTypeTestament, ID: "testament-early"},
		},
		&DeltaDelivery{To: []AgentRef{DegradedAgentRef("architect", "test")}, Relationship: RelationshipIssuer},
		map[string]any{
			"claim": map[string]any{
				"id":     claimID,
				"action": string(ActionTypeConsultation),
				"status": string(ClaimStatusTestified),
			},
		},
	)

	inbox.Ingest(delta)
	if got != nil {
		t.Fatal("response delta should not resolve before expectation registration")
	}
	inbox.Expect(&Expectation{
		ClaimID:       claimID,
		ExpectedDelta: DeltaKindTestament,
		Priority:      PriorityResponse,
	})
	if got == nil {
		t.Fatal("early canonical testament was not replayed into the expectation")
	}
	if got.Expectation == nil || got.Expectation.ClaimID != claimID {
		t.Fatalf("expectation = %#v, want claim %q", got.Expectation, claimID)
	}
	if got.Delta.DeltaKind() != string(DeltaActionTestamentSubmitted) {
		t.Fatalf("delta kind = %q", got.Delta.DeltaKind())
	}
}

func TestInbox_ClaimStatusDelta_RoleGated(t *testing.T) {
	// Subject-only inbox does not match claim status deltas via
	// standing — issuer/subject identity flows are Expect()-driven.
	var subjectCalled atomic.Bool
	subject, _ := NewClaimsInbox(InboxConfig{
		AgentID:   "eng",
		SessionID: "sess",
		Role:      RoleSubject,
		OnResolved: func(_ *GraphEntryPoint) {
			subjectCalled.Store(true)
		},
	})
	subject.Ingest(ClaimStatusDelta{
		ClaimID:        "c1",
		ToStatus:       ClaimStatusRejected,
		IssuerAgentID:  "eng",
		SubjectAgentID: "eng",
		Sequence:       1,
	})
	if subjectCalled.Load() {
		t.Fatal("RoleSubject inbox should NOT match ClaimStatusDelta via standing")
	}

	// RoleRemediator matches ClaimStatusRejected regardless of identity.
	var remediatorCalled atomic.Bool
	remediator, _ := NewClaimsInbox(InboxConfig{
		AgentID:   "architect",
		SessionID: "sess",
		Role:      RoleSubject | RoleRemediator,
		OnResolved: func(_ *GraphEntryPoint) {
			remediatorCalled.Store(true)
		},
	})
	remediator.Ingest(ClaimStatusDelta{
		ClaimID:        "c1",
		ToStatus:       ClaimStatusRejected,
		IssuerAgentID:  "engineer",
		SubjectAgentID: "designer",
		Sequence:       1,
	})
	if !remediatorCalled.Load() {
		t.Fatal("RoleRemediator inbox should match ClaimStatusRejected")
	}

	// RoleRemediator does NOT match ClaimStatusAccepted.
	var acceptedCalled atomic.Bool
	remediator2, _ := NewClaimsInbox(InboxConfig{
		AgentID:   "architect2",
		SessionID: "sess",
		Role:      RoleSubject | RoleRemediator,
		OnResolved: func(_ *GraphEntryPoint) {
			acceptedCalled.Store(true)
		},
	})
	remediator2.Ingest(ClaimStatusDelta{
		ClaimID:        "c2",
		ToStatus:       ClaimStatusAccepted,
		IssuerAgentID:  "engineer",
		SubjectAgentID: "designer",
		Sequence:       1,
	})
	if acceptedCalled.Load() {
		t.Fatal("RoleRemediator should not match ClaimStatusAccepted")
	}
}

func TestInbox_NilOnResolved_NoError(t *testing.T) {
	// OnResolved is nil — matched deltas are counted but not dispatched.
	inbox, _ := NewClaimsInbox(InboxConfig{AgentID: "eng", SessionID: "sess"})
	inbox.Ingest(InboxDelta{
		ClaimID: "c1", AgentID: "eng",
		Relationship: RelationshipSubject,
		Sequence:     1,
	})
	if inbox.Len() != 1 {
		t.Fatalf("expected 1 match, got %d", inbox.Len())
	}
}

func TestDedupLRU_BoundsAndEviction(t *testing.T) {
	l := newDedupLRU(3)
	if !l.observe("a", 1) || !l.observe("b", 1) || !l.observe("c", 1) {
		t.Fatal("first three observations should be new")
	}
	if l.Len() != 3 {
		t.Fatalf("expected 3 entries, got %d", l.Len())
	}
	// Inserting a fourth evicts "a" (oldest).
	if !l.observe("d", 1) {
		t.Fatal("fourth observation should be new")
	}
	if l.Len() != 3 {
		t.Fatalf("expected 3 entries after eviction, got %d", l.Len())
	}
	if _, ok := l.nodes["a"]; ok {
		t.Fatal("expected 'a' to be evicted")
	}
	// Re-inserting "a" treats it as new (since it was evicted).
	if !l.observe("a", 1) {
		t.Fatal("re-inserting evicted key should be new")
	}
	// Same key with older sequence is a duplicate.
	if l.observe("a", 1) {
		t.Fatal("same (key, seq) should be duplicate")
	}
	// Same key with newer sequence is fresh.
	if !l.observe("a", 2) {
		t.Fatal("newer sequence on same key should match")
	}
}

func TestDedupLRU_TouchKeepsRecentEntries(t *testing.T) {
	l := newDedupLRU(3)
	l.observe("a", 1)
	l.observe("b", 1)
	l.observe("c", 1)
	// Touch "a" — bumps it to most-recent.
	l.observe("a", 2)
	// Inserting "d" should now evict "b" (oldest after touch).
	l.observe("d", 1)
	if _, ok := l.nodes["b"]; ok {
		t.Fatal("expected 'b' to be evicted after 'a' was touched")
	}
	if _, ok := l.nodes["a"]; !ok {
		t.Fatal("expected 'a' to be retained")
	}
}

func TestInbox_Len_DoesNotMatchDuplicates(t *testing.T) {
	inbox, _ := NewClaimsInbox(InboxConfig{AgentID: "eng", SessionID: "sess"})
	d := InboxDelta{ClaimID: "c1", AgentID: "eng", Relationship: RelationshipSubject, Sequence: 1}
	for i := 0; i < 100; i++ {
		inbox.Ingest(d)
	}
	if inbox.Len() != 1 {
		t.Fatalf("expected 1 match across 100 duplicate ingests, got %d", inbox.Len())
	}
}

func TestInbox_Len_DoesNotMatchDuplicateCanonicalDelta(t *testing.T) {
	inbox, _ := NewClaimsInbox(InboxConfig{
		AgentID:   "librarian",
		SessionID: "sess",
		Role:      RoleSubject,
	})
	d := NewCanonicalDelta(
		DeltaActionClaimDirected,
		"sess",
		"board",
		1,
		time.Now(),
		DegradedAgentRef("architect", "test"),
		claimRefs("claim-1"),
		&DeltaDelivery{To: []AgentRef{DegradedAgentRef("librarian", "test")}, Relationship: RelationshipSubject},
		map[string]any{"claim": map[string]any{"id": "claim-1", "action": string(ActionTypeConsultation)}},
	)
	for i := 0; i < 100; i++ {
		inbox.Ingest(d)
	}
	if inbox.Len() != 1 {
		t.Fatalf("expected 1 match across 100 duplicate canonical ingests, got %d", inbox.Len())
	}
}

func TestInbox_ChallengePriority(t *testing.T) {
	var received atomic.Pointer[GraphEntryPoint]
	inbox, _ := NewClaimsInbox(InboxConfig{
		AgentID:   "eng",
		SessionID: "sess",
		OnResolved: func(entry *GraphEntryPoint) {
			received.Store(entry)
		},
	})
	inbox.Ingest(InboxDelta{
		ClaimID:      "c1",
		AgentID:      "eng",
		Relationship: RelationshipSubject,
		ActionKind:   ActionTypeChallenge,
		Sequence:     1,
	})
	entry := received.Load()
	if entry == nil {
		t.Fatal("expected OnResolved")
	}
	if entry.Priority != PriorityChallenge {
		t.Fatalf("expected PriorityChallenge (2), got %d", entry.Priority)
	}
}

// ────────────────────────────────────────────────────────────────────
// InboxClass tagging + per-class accounting
// ────────────────────────────────────────────────────────────────────

func TestDeltaClass_Mapping(t *testing.T) {
	cases := []struct {
		name string
		d    Delta
		want InboxClass
	}{
		{"consult_request", InboxDelta{ActionKind: ActionTypeConsultation}, InboxClassConsultRequest},
		{"directed_task", InboxDelta{ActionKind: ActionTypeTask}, InboxClassDirected},
		{"directed_challenge", InboxDelta{ActionKind: ActionTypeChallenge}, InboxClassDirected},
		{"directed_corrective", InboxDelta{ActionKind: ActionTypeCorrective}, InboxClassDirected},
		{"observation_testament", TestamentDelta{}, InboxClassObservation},
		{"observation_validation", ValidationDelta{}, InboxClassObservation},
		{"directed_rejected", ClaimStatusDelta{ToStatus: ClaimStatusRejected}, InboxClassDirected},
		{"observation_accepted", ClaimStatusDelta{ToStatus: ClaimStatusAccepted}, InboxClassObservation},
		{"phase_transition", PhaseDelta{}, InboxClassPhase},
		{"nil_delta", nil, InboxClassObservation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeltaClass(tc.d); got != tc.want {
				t.Fatalf("DeltaClass(%T) = %s, want %s", tc.d, got, tc.want)
			}
		})
	}
}

func TestInbox_DeliveredByClass_IncrementsPerIngest(t *testing.T) {
	inbox, _ := NewClaimsInbox(InboxConfig{
		AgentID:    "eng",
		SessionID:  "sess",
		OnResolved: func(_ *GraphEntryPoint) {},
	})
	// Two consult requests, three observations, one directed.
	inbox.Ingest(InboxDelta{ClaimID: "c1", AgentID: "eng", Relationship: RelationshipSubject, ActionKind: ActionTypeConsultation, Sequence: 1})
	inbox.Ingest(InboxDelta{ClaimID: "c2", AgentID: "eng", Relationship: RelationshipSubject, ActionKind: ActionTypeConsultation, Sequence: 1})
	inbox.Ingest(TestamentDelta{ClaimID: "c3", Sequence: 1})
	inbox.Ingest(TestamentDelta{ClaimID: "c4", Sequence: 1})
	inbox.Ingest(TestamentDelta{ClaimID: "c5", Sequence: 1})
	inbox.Ingest(InboxDelta{ClaimID: "c6", AgentID: "eng", Relationship: RelationshipSubject, ActionKind: ActionTypeTask, Sequence: 1})

	if got, want := inbox.DeliveredByClass(InboxClassConsultRequest), uint64(2); got != want {
		t.Errorf("ConsultRequest delivered = %d, want %d", got, want)
	}
	if got, want := inbox.DeliveredByClass(InboxClassObservation), uint64(3); got != want {
		t.Errorf("Observation delivered = %d, want %d", got, want)
	}
	if got, want := inbox.DeliveredByClass(InboxClassDirected), uint64(1); got != want {
		t.Errorf("Directed delivered = %d, want %d", got, want)
	}
}

// recordingSubWithDrops adds a configurable drop counter to recordingSub
// so tests can verify Inbox.OverflowCount aggregates per-subscription
// drops via the optional DroppedCounter contract.
type recordingSubWithDrops struct {
	*recordingSub
	drops atomic.Uint64
}

func (s *recordingSubWithDrops) DroppedCount() uint64 { return s.drops.Load() }

type droppingBus struct {
	*recordingBus
	subs []*recordingSubWithDrops
}

func (b *droppingBus) SubscribeDelta(pattern string, h DeltaHandler) (DeltaSubscription, error) {
	inner, err := b.recordingBus.SubscribeDelta(pattern, h)
	if err != nil {
		return nil, err
	}
	wrapped := &recordingSubWithDrops{recordingSub: inner.(*recordingSub)}
	b.subs = append(b.subs, wrapped)
	return wrapped, nil
}

func TestInbox_OverflowCount_AggregatesAcrossSubscriptions(t *testing.T) {
	bus := &droppingBus{recordingBus: newRecordingBus()}
	inbox, _ := NewClaimsInbox(InboxConfig{
		AgentID:    "ins",
		SessionID:  "sess",
		Role:       RoleSubject | RoleAuditor,
		Subscriber: bus,
		OnResolved: func(_ *GraphEntryPoint) {},
	})
	if err := inbox.Start(nil); err != nil {
		t.Fatal(err)
	}

	// No drops yet.
	if got := inbox.OverflowCount(); got != 0 {
		t.Fatalf("initial OverflowCount = %d, want 0", got)
	}

	// Inject drops on each subscription.
	if len(bus.subs) < 2 {
		t.Fatalf("expected at least 2 subscriptions, got %d", len(bus.subs))
	}
	bus.subs[0].drops.Store(7)
	bus.subs[1].drops.Store(11)
	if got, want := inbox.OverflowCount(), uint64(18); got != want {
		t.Fatalf("OverflowCount = %d, want %d", got, want)
	}
}
