package guide

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type recordingHook struct {
	mu            sync.Mutex
	publishes     []string
	deliveries    []int64
	subscribes    []int64
	unsubscribes  []int64
	overflows     atomic.Int64
	expireds      atomic.Int64
	lastPublisher string
	lastTarget    string
}

func (r *recordingHook) OnPublish(topic string, msg *Message, subscriberCount int, err error) {
	r.mu.Lock()
	r.publishes = append(r.publishes, topic)
	if msg != nil {
		r.lastPublisher = msg.SourceAgentID
		r.lastTarget = msg.TargetAgentID
	}
	r.mu.Unlock()
}

func (r *recordingHook) OnDelivery(publishTopic, subscriberTopic string, subscriberID int64, async bool, queueLatencyMicros int64, msg *Message, handlerErr error) {
	r.mu.Lock()
	r.deliveries = append(r.deliveries, subscriberID)
	r.mu.Unlock()
}

func (r *recordingHook) OnSubscribe(topic string, subscriberID int64, async, wildcard bool, matchPattern string) {
	r.mu.Lock()
	r.subscribes = append(r.subscribes, subscriberID)
	r.mu.Unlock()
}

func (r *recordingHook) OnUnsubscribe(topic string, subscriberID int64, duration time.Duration) {
	r.mu.Lock()
	r.unsubscribes = append(r.unsubscribes, subscriberID)
	r.mu.Unlock()
}

func (r *recordingHook) OnOverflow(event OverflowEvent) {
	r.overflows.Add(1)
}

func (r *recordingHook) OnExpired(topic string, msg *Message, expiredAt time.Time) {
	r.expireds.Add(1)
}

func TestChannelBus_HooksFireOnPublishSubscribeDeliver(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer bus.Close()

	hook := &recordingHook{}
	bus.SetEventHook(hook)

	delivered := make(chan struct{}, 1)
	handler := func(msg *Message) error {
		delivered <- struct{}{}
		return nil
	}
	sub, err := bus.Subscribe("request.engineer.eng-1", handler)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	hook.mu.Lock()
	if len(hook.subscribes) != 1 {
		t.Errorf("subscribe hook fires = %d, want 1; snapshot=%+v", len(hook.subscribes), hook.subscribes)
	}
	hook.mu.Unlock()

	msg := &Message{
		ID:            "msg-1",
		CorrelationID: "corr-1",
		Type:          MessageTypeRequest,
		SourceAgentID: "guide",
		TargetAgentID: "eng-1",
		Timestamp:     time.Now(),
	}
	if err := bus.Publish("request.engineer.eng-1", msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case <-delivered:
	case <-time.After(2 * time.Second):
		t.Fatal("handler never delivered")
	}
	// Brief wait for the async delivery hook to fire after handler returns.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		hook.mu.Lock()
		n := len(hook.deliveries)
		hook.mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	hook.mu.Lock()
	if len(hook.publishes) != 1 {
		t.Errorf("publish hook fires = %d, want 1", len(hook.publishes))
	}
	if hook.lastPublisher != "guide" {
		t.Errorf("lastPublisher = %q, want guide", hook.lastPublisher)
	}
	if len(hook.deliveries) < 1 {
		t.Errorf("delivery hook fires = %d, want >= 1", len(hook.deliveries))
	}
	hook.mu.Unlock()

	// Unsubscribe, then verify hook. Must release the hook lock
	// before calling Unsubscribe because the unsubscribe hook is
	// invoked synchronously on the caller's goroutine and needs to
	// acquire the same mutex.
	sub.Unsubscribe()
	hook.mu.Lock()
	defer hook.mu.Unlock()
	if len(hook.unsubscribes) != 1 {
		t.Errorf("unsubscribe hook fires = %d, want 1", len(hook.unsubscribes))
	}
}

func TestChannelBus_ExpiredHookFires(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer bus.Close()

	hook := &recordingHook{}
	bus.SetEventHook(hook)

	past := time.Now().Add(-time.Hour)
	msg := &Message{
		ID:        "msg-expired",
		Timestamp: past,
		Deadline:  &past,
	}
	err := bus.Publish("anything", msg)
	if err == nil {
		t.Fatal("expected ErrMessageExpired")
	}
	if hook.expireds.Load() != 1 {
		t.Errorf("expired hook fires = %d, want 1", hook.expireds.Load())
	}
}

func TestChannelBus_HookNilIsNoop(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer bus.Close()
	// Set + clear hook; ensure publish doesn't panic either way.
	bus.SetEventHook(&recordingHook{})
	bus.SetEventHook(nil)

	msg := &Message{ID: "m", Timestamp: time.Now()}
	if err := bus.Publish("t", msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}
}

func TestChannelBus_SubIDsMonotonic(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer bus.Close()

	hook := &recordingHook{}
	bus.SetEventHook(hook)

	handler := func(*Message) error { return nil }
	for i := 0; i < 5; i++ {
		if _, err := bus.Subscribe("topic", handler); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
	}
	hook.mu.Lock()
	defer hook.mu.Unlock()
	if len(hook.subscribes) != 5 {
		t.Fatalf("expected 5 subscribe hooks, got %d", len(hook.subscribes))
	}
	for i := 1; i < len(hook.subscribes); i++ {
		if hook.subscribes[i] <= hook.subscribes[i-1] {
			t.Errorf("subscribe IDs not monotonic: %v", hook.subscribes)
			break
		}
	}
}
