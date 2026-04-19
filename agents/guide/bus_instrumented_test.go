package guide

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/activity"
)

func newTestBus(t *testing.T) *ChannelBus {
	t.Helper()
	bus := NewChannelBus(ChannelBusConfig{ShardCount: 4, BufferSize: 16, CloseTimeout: time.Second})
	t.Cleanup(func() { _ = bus.Close() })
	return bus
}

func TestPublish_EmitsBusMessageActivity(t *testing.T) {
	col := activity.NewTestCollector()
	prev := activity.SetDefaultSink(col)
	defer activity.SetDefaultSink(prev)

	bus := newTestBus(t)

	if err := bus.Publish("test.topic", &Message{
		ID:            "msg-1",
		CorrelationID: "corr-7",
		SourceAgentID: "tester-pipeline-1",
		TargetAgentID: "engineer-1",
		Type:          MessageTypeRequest,
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	emitted := col.FilterByKind(activity.ActionBusMessageEmitted)
	if len(emitted) == 0 {
		t.Fatal("Publish must emit ActionBusMessageEmitted")
	}
	last := emitted[len(emitted)-1]
	if !strings.Contains(string(last.Payload), "corr-7") {
		t.Errorf("payload should embed correlation_id; got %s", string(last.Payload))
	}
	if last.Subject.PathPrefix != "test.topic" {
		t.Errorf("Subject.PathPrefix should hold the topic; got %q", last.Subject.PathPrefix)
	}
	if last.Subject.TargetAgent != "engineer-1" {
		t.Errorf("Subject.TargetAgent should hold the addressed agent; got %q", last.Subject.TargetAgent)
	}
}

// TestPublish_ClosedBusStillEmitsActivity documents the non-happy-path
// invariant: a Publish to a closed bus must still leave a fabric trail
// so post-mortem can identify late publishes after Close. Without this,
// the inciting "where did my message go?" debugging session relies on
// log scraping; with it, it's a single fabric query.
func TestPublish_ClosedBusStillEmitsActivity(t *testing.T) {
	col := activity.NewTestCollector()
	prev := activity.SetDefaultSink(col)
	defer activity.SetDefaultSink(prev)

	bus := NewChannelBus(ChannelBusConfig{ShardCount: 4, BufferSize: 16, CloseTimeout: time.Second})
	_ = bus.Close()

	err := bus.Publish("test.topic", &Message{ID: "msg-after-close"})
	if err != ErrBusClosed {
		t.Fatalf("Publish to closed bus: got %v, want ErrBusClosed", err)
	}

	emitted := col.FilterByKind(activity.ActionBusMessageEmitted)
	if len(emitted) == 0 {
		t.Fatal("late Publish must still emit a fabric activity for diagnosis")
	}
	last := emitted[len(emitted)-1]
	if !strings.Contains(string(last.Payload), "bus is closed") {
		t.Errorf("error path should embed the closed-bus error; got %s", string(last.Payload))
	}
}

// TestPublish_RecordsSubscriberCount documents that the subscriber
// count makes it into the activity payload — that's how the
// "STREAM_COMPLETE published with total_subs=0" class of bug becomes
// queryable from data instead of log scraping.
func TestPublish_RecordsSubscriberCount(t *testing.T) {
	col := activity.NewTestCollector()
	prev := activity.SetDefaultSink(col)
	defer activity.SetDefaultSink(prev)

	bus := newTestBus(t)

	var (
		mu      sync.Mutex
		got     []*Message
		handler = MessageHandler(func(msg *Message) error {
			mu.Lock()
			got = append(got, msg)
			mu.Unlock()
			return nil
		})
	)
	if _, err := bus.Subscribe("counted.topic", handler); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := bus.Publish("counted.topic", &Message{ID: "msg-count"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// Wait briefly for delivery.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	emitted := col.FilterByKind(activity.ActionBusMessageEmitted)
	if len(emitted) == 0 {
		t.Fatal("expected a bus_message_emitted activity")
	}
	last := emitted[len(emitted)-1]
	if !strings.Contains(string(last.Payload), `"subscriber_count":1`) {
		t.Errorf("payload should record subscriber_count=1; got %s", string(last.Payload))
	}
}

// TestPublish_NoEmissionFailureWhenSinkIsDiscard documents that bus
// instrumentation has zero observable side-effect when the fabric is
// disabled.
func TestPublish_SinkDiscardSafe(t *testing.T) {
	prev := activity.SetDefaultSink(nil)
	defer activity.SetDefaultSink(prev)

	bus := newTestBus(t)
	if err := bus.Publish("noop.topic", &Message{ID: "x"}); err != nil {
		t.Fatalf("Publish must succeed under discard sink: %v", err)
	}
}
