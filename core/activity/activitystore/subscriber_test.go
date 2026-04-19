package activitystore

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/activity"
)

type recordingSubscriber struct {
	mu       sync.Mutex
	received []activity.AgentActivity
	name     string
}

func (r *recordingSubscriber) Name() string { return r.name }
func (r *recordingSubscriber) Receive(_ context.Context, a activity.AgentActivity) {
	r.mu.Lock()
	r.received = append(r.received, a)
	r.mu.Unlock()
}
func (r *recordingSubscriber) Count() int {
	r.mu.Lock()
	n := len(r.received)
	r.mu.Unlock()
	return n
}

type panickingSubscriber struct{ name string }

func (p *panickingSubscriber) Name() string                              { return p.name }
func (p *panickingSubscriber) Receive(context.Context, activity.AgentActivity) { panic("subscriber boom") }

func TestSubscribingSink_FansOutToAllSubscribers(t *testing.T) {
	col := activity.NewTestCollector()
	sink := NewSubscribingSink(col)

	a := &recordingSubscriber{name: "a"}
	b := &recordingSubscriber{name: "b"}
	defer sink.Subscribe(a)()
	defer sink.Subscribe(b)()

	act := sampleActivity(activity.ActionDecisionDeclared, "s1", "tests/")
	sink.Append(context.Background(), act)

	if a.Count() != 1 || b.Count() != 1 {
		t.Fatalf("expected both subscribers to receive 1; got a=%d b=%d", a.Count(), b.Count())
	}
	if got := col.Count(); got != 1 {
		t.Fatalf("underlying sink should also receive; got %d", got)
	}
}

func TestSubscribingSink_PanicInSubscriberDoesNotPropagate(t *testing.T) {
	sink := NewSubscribingSink(activity.NewTestCollector())
	sink.Subscribe(&panickingSubscriber{name: "boom"})
	good := &recordingSubscriber{name: "good"}
	sink.Subscribe(good)

	// This must not panic.
	sink.Append(context.Background(), sampleActivity(activity.ActionDecisionDeclared, "s1", "tests/"))

	if good.Count() != 1 {
		t.Fatal("good subscriber must still receive after a peer's panic")
	}
}

func TestSubscribingSink_UnsubscribeStopsDelivery(t *testing.T) {
	sink := NewSubscribingSink(activity.NewTestCollector())
	a := &recordingSubscriber{name: "a"}
	cancel := sink.Subscribe(a)

	sink.Append(context.Background(), sampleActivity(activity.ActionDecisionDeclared, "s1", "tests/"))
	cancel()
	sink.Append(context.Background(), sampleActivity(activity.ActionDecisionDeclared, "s1", "tests/"))

	if a.Count() != 1 {
		t.Fatalf("expected 1 received before/at unsubscribe; got %d", a.Count())
	}
}

func TestSubscribingSink_DoubleSubscribeIsIdempotent(t *testing.T) {
	sink := NewSubscribingSink(activity.NewTestCollector())
	a := &recordingSubscriber{name: "a"}
	sink.Subscribe(a)
	sink.Subscribe(a) // second call must not duplicate

	sink.Append(context.Background(), sampleActivity(activity.ActionDecisionDeclared, "s1", "tests/"))
	if a.Count() != 1 {
		t.Fatalf("idempotent subscribe must not double-deliver; got %d", a.Count())
	}
}

func TestBleveSubscriber_IndexesAndSearches(t *testing.T) {
	bs, err := NewBleveSubscriber()
	if err != nil {
		t.Fatalf("new bleve subscriber: %v", err)
	}
	defer bs.Close()

	a := sampleActivity(activity.ActionDecisionDeclared, "s1", "services/billing/tests/")
	a.Subject.Domain = "test_framework"
	a.Payload = []byte(`{"value":"pytest","author":"tester-1"}`)
	bs.Receive(context.Background(), a)

	// Allow Bleve a moment to settle the in-memory index.
	time.Sleep(20 * time.Millisecond)

	hits, err := bs.Search("pytest", 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("expected at least one hit for 'pytest'")
	}
}

func TestBleveSubscriber_DropsAtomicAndFineResolution(t *testing.T) {
	bs, err := NewBleveSubscriber()
	if err != nil {
		t.Fatalf("new bleve subscriber: %v", err)
	}
	defer bs.Close()

	// FileRead is Atomic — must NOT be indexed.
	bs.Receive(context.Background(), sampleActivity(activity.ActionFileRead, "s1", "main.go"))
	if got := bs.IndexedCount(); got != 0 {
		t.Fatalf("Atomic activities must not be indexed; got %d", got)
	}
}

func TestForestSubscriber_HarvestesPrecedentEmitted(t *testing.T) {
	var (
		mu        sync.Mutex
		harvested []ForestCandidate
	)
	fs := NewForestSubscriber(func(_ context.Context, c ForestCandidate) error {
		mu.Lock()
		harvested = append(harvested, c)
		mu.Unlock()
		return nil
	})
	a := sampleActivity(activity.ActionPrecedentEmitted, "s1", "services/billing/")
	fs.Receive(context.Background(), a)

	if got := fs.HarvestedCount(); got != 1 {
		t.Fatalf("expected 1 harvested; got %d", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(harvested) != 1 || harvested[0].Reason != "explicit precedent_emitted" {
		t.Fatalf("harvest reason mismatch; got %+v", harvested)
	}
}

func TestForestSubscriber_DropsOperationalActivities(t *testing.T) {
	fs := NewForestSubscriber(func(context.Context, ForestCandidate) error { return nil })
	fs.Receive(context.Background(), sampleActivity(activity.ActionFileRead, "s1", "main.go"))
	if fs.HarvestedCount() != 0 {
		t.Fatalf("Atomic activities must not be harvested; got %d", fs.HarvestedCount())
	}
}

func TestForestSubscriber_HarvestErrorDoesNotIncrement(t *testing.T) {
	fs := NewForestSubscriber(func(context.Context, ForestCandidate) error {
		return errors.New("forest boom")
	})
	fs.Receive(context.Background(), sampleActivity(activity.ActionPrecedentEmitted, "s1", "x/"))
	if fs.HarvestedCount() != 0 {
		t.Fatal("HarvestedCount should not advance on error")
	}
	if fs.CandidateCount() != 1 {
		t.Fatalf("CandidateCount should advance regardless of error; got %d", fs.CandidateCount())
	}
}

// TestForestSubscriber_HarvestesConsensusDecision documents that
// decisions promoted to Consensus also become harvest candidates —
// they represent cross-pipeline corroboration whose reasoning chain
// is precedent-quality.
func TestForestSubscriber_HarvestesConsensusDecision(t *testing.T) {
	fs := NewForestSubscriber(func(context.Context, ForestCandidate) error { return nil })
	a := sampleActivity(activity.ActionDecisionPromoted, "s1", "tests/")
	a.Confidence = activity.ConfidenceConsensus
	fs.Receive(context.Background(), a)
	if fs.HarvestedCount() != 1 {
		t.Fatalf("Consensus decision must be harvested; got %d", fs.HarvestedCount())
	}
}
