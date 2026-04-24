package claims

import "context"

// DeltaPublisher publishes a Delta to a bus topic. Implementations
// adapt the claims package to whichever concrete transport the host
// environment provides. Kept narrow so core/claims never imports a
// specific bus package.
//
// Implementations MUST be safe for concurrent use — the amplifier
// calls Publish from multiple tracked goroutines under the board's
// scope.
type DeltaPublisher interface {
	// PublishDelta emits delta on topic. Returns an error only when
	// the publish is definitively rejected (e.g. bus closed or
	// message expired). Transient drops (subscriber buffer overflow,
	// no current subscribers) must be handled transparently and must
	// not return an error — the inbox's WAL-cursor catch-up covers
	// that case.
	PublishDelta(ctx context.Context, topic string, delta Delta) error
}

// DeltaHandler receives a Delta via a DeltaSubscription. Handlers
// run on whatever goroutine the subscriber implementation provides;
// callers that need tracked lifecycle must register handlers that
// dispatch into their own ScopeProvider.
type DeltaHandler func(delta Delta)

// DeltaSubscription is the handle returned by DeltaSubscriber.Subscribe.
// Unsubscribe is idempotent — calling it more than once returns the
// same result as the first call.
type DeltaSubscription interface {
	Topic() string
	Unsubscribe() error
}

// DeltaSubscriber subscribes DeltaHandlers to topic patterns. Pattern
// semantics match the TopicRouter wildcard rules of the underlying
// bus (typically glob-style with '*' matching a single segment).
type DeltaSubscriber interface {
	// SubscribeDelta registers handler on pattern. Returns a
	// subscription that callers Unsubscribe during shutdown. Errors
	// are terminal (e.g. bus closed); subscribers that couldn't be
	// registered must not be silently swallowed.
	SubscribeDelta(pattern string, handler DeltaHandler) (DeltaSubscription, error)
}

// DeltaBus combines publisher and subscriber. Most real-world
// adapters implement both ends on the same underlying bus.
type DeltaBus interface {
	DeltaPublisher
	DeltaSubscriber
}

// ────────────────────────────────────────────────────────────────────
// No-op / nil-safe helpers
// ────────────────────────────────────────────────────────────────────

// NoopDeltaBus is a DeltaBus that discards every publish and rejects
// every subscribe with a no-op subscription. Used as the default
// when no bus is wired (tests, standalone tools) so callers don't
// have to nil-check.
type NoopDeltaBus struct{}

func (NoopDeltaBus) PublishDelta(_ context.Context, _ string, _ Delta) error {
	return nil
}

func (NoopDeltaBus) SubscribeDelta(pattern string, _ DeltaHandler) (DeltaSubscription, error) {
	return noopSubscription{topic: pattern}, nil
}

type noopSubscription struct{ topic string }

func (s noopSubscription) Topic() string       { return s.topic }
func (s noopSubscription) Unsubscribe() error  { return nil }

// publishOrNoop returns a non-nil DeltaPublisher. Zero nil-checks in
// callers.
func publishOrNoop(p DeltaPublisher) DeltaPublisher {
	if p == nil {
		return NoopDeltaBus{}
	}
	return p
}

// subscribeOrNoop returns a non-nil DeltaSubscriber.
func subscribeOrNoop(s DeltaSubscriber) DeltaSubscriber {
	if s == nil {
		return NoopDeltaBus{}
	}
	return s
}
