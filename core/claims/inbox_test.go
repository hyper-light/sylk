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

func TestInbox_StartSubscribesDefaultPatterns(t *testing.T) {
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
	patterns := DefaultInboxPatterns("sess", "eng")
	for _, p := range patterns {
		if bus.SubscriptionCount(p) == 0 {
			t.Errorf("no subscription on pattern %q", p)
		}
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

func TestInbox_StandingSubscription_PhaseDelta(t *testing.T) {
	var called atomic.Bool
	inbox, _ := NewClaimsInbox(InboxConfig{
		AgentID:   "eng",
		SessionID: "sess",
		OnResolved: func(_ *GraphEntryPoint) {
			called.Store(true)
		},
	})
	inbox.Ingest(PhaseDelta{
		BoardID:   "board-1",
		FromPhase: BoardPhaseImplementation,
		ToPhase:   BoardPhaseValidation,
		Sequence:  5,
	})
	if !called.Load() {
		t.Fatal("expected OnResolved for phase delta")
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

func TestInbox_TestamentDelta_StandingSubscription_IssuerMatch(t *testing.T) {
	var called atomic.Bool
	inbox, _ := NewClaimsInbox(InboxConfig{
		AgentID:   "eng",
		SessionID: "sess",
		OnResolved: func(_ *GraphEntryPoint) {
			called.Store(true)
		},
	})
	inbox.Ingest(TestamentDelta{
		ClaimID:        "c1",
		TestamentID:    "t1",
		IssuerAgentID:  "eng",
		SubjectAgentID: "designer",
		Sequence:       1,
	})
	if !called.Load() {
		t.Fatal("expected OnResolved for issuer match")
	}
}

func TestInbox_TestamentDelta_NoMatch(t *testing.T) {
	var called atomic.Bool
	inbox, _ := NewClaimsInbox(InboxConfig{
		AgentID:   "eng",
		SessionID: "sess",
		OnResolved: func(_ *GraphEntryPoint) {
			called.Store(true)
		},
	})
	inbox.Ingest(TestamentDelta{
		ClaimID:        "c1",
		TestamentID:    "t1",
		IssuerAgentID:  "architect",
		SubjectAgentID: "designer",
		Sequence:       1,
	})
	if called.Load() {
		t.Fatal("expected no match (neither issuer nor subject is eng)")
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
