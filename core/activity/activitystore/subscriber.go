package activitystore

import (
	"context"
	"sync"

	"github.com/adalundhe/sylk/core/activity"
)

// Subscriber is the interface downstream consumers (Bleve, vectorgraphdb,
// Memory Forest, sidecar scribe) implement to receive activity stream
// events asynchronously.
//
// A subscriber receives every activity appended through a
// SubscribingSink. Implementations must never block the caller; slow
// subscribers should buffer internally or drop (with their own
// telemetry).
type Subscriber interface {
	// Name returns the subscriber's stable identifier (used for
	// logging and fan-out bookkeeping).
	Name() string

	// Receive is called with every activity appended to the sink.
	// Must be safe for concurrent callers and must not block for
	// I/O on the caller's thread; implementations either buffer
	// internally or dispatch to their own goroutine.
	Receive(ctx context.Context, a activity.AgentActivity)
}

// SubscribingSink wraps an underlying Sink (typically DualTierSink)
// with a fan-out subscriber registry. Every Append lands in the
// underlying sink AND in every registered subscriber. Subscribers are
// added/removed at runtime.
//
// The fan-out happens synchronously (subscribers must not block). If
// you need async delivery, the subscriber should spawn its own
// goroutine from Receive.
type SubscribingSink struct {
	underlying activity.Sink

	mu          sync.RWMutex
	subscribers []Subscriber
}

// NewSubscribingSink wraps sink with a subscriber registry.
func NewSubscribingSink(sink activity.Sink) *SubscribingSink {
	if sink == nil {
		sink = discardSink{}
	}
	return &SubscribingSink{underlying: sink}
}

// Subscribe adds sub to the fan-out. Returns an unsubscribe function
// callers defer. Idempotent — subscribing the same subscriber twice
// (by pointer identity) is a no-op.
func (s *SubscribingSink) Subscribe(sub Subscriber) func() {
	if s == nil || sub == nil {
		return func() {}
	}
	s.mu.Lock()
	for _, existing := range s.subscribers {
		if existing == sub {
			s.mu.Unlock()
			return func() {}
		}
	}
	s.subscribers = append(s.subscribers, sub)
	s.mu.Unlock()
	return func() { s.unsubscribe(sub) }
}

func (s *SubscribingSink) unsubscribe(sub Subscriber) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.subscribers {
		if existing == sub {
			s.subscribers = append(s.subscribers[:i], s.subscribers[i+1:]...)
			return
		}
	}
}

// Append fans the activity to the underlying sink and to every
// registered subscriber.
func (s *SubscribingSink) Append(ctx context.Context, a activity.AgentActivity) {
	if s == nil {
		return
	}
	if s.underlying != nil {
		s.underlying.Append(ctx, a)
	}
	s.mu.RLock()
	subs := make([]Subscriber, len(s.subscribers))
	copy(subs, s.subscribers)
	s.mu.RUnlock()
	for _, sub := range subs {
		safeReceive(ctx, sub, a)
	}
}

func safeReceive(ctx context.Context, sub Subscriber, a activity.AgentActivity) {
	defer func() {
		// Panic recovery: a subscriber must never crash the emitter.
		recover()
	}()
	sub.Receive(ctx, a)
}

type discardSink struct{}

func (discardSink) Append(context.Context, activity.AgentActivity) {}

var _ activity.Sink = (*SubscribingSink)(nil)

// SubscribeToDefault registers sub with the SubscribingSink that wraps
// the process-wide DefaultSink, when one is installed. Returns an
// unsubscribe function. When the DefaultSink is not a SubscribingSink
// (e.g., test fixtures with a TestCollector), returns a no-op
// unsubscribe — the registration is silently skipped.
//
// This is the canonical entry point for late-binding subscribers
// (per-agent scribes, per-session indexers) that want to attach
// themselves to whatever sink production wired up at startup. The
// orchestrator's installActivityFabric installs a SubscribingSink as
// the DefaultSink; subscribers added via this helper see the same
// stream every other registered subscriber sees.
func SubscribeToDefault(sub Subscriber) func() {
	sink, ok := activity.DefaultSink().(*SubscribingSink)
	if !ok || sink == nil {
		return func() {}
	}
	return sink.Subscribe(sub)
}
