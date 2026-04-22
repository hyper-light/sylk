package buslog

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestLogger(t *testing.T) (*BusLogger, string) {
	t.Helper()
	base := t.TempDir()
	l, err := New(Config{
		BaseDir:    base,
		SessionID:  "sess-test",
		BufferSize: 256,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return l, base
}

func readAll(t *testing.T, base string) []BusLogRecord {
	t.Helper()
	var out []BusLogRecord
	_ = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasPrefix(filepath.Base(path), "bus.") || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var rec BusLogRecord
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				t.Fatalf("unmarshal %q: %v", line, err)
			}
			out = append(out, rec)
		}
		return nil
	})
	return out
}

func TestBusLogger_PublishWritesRecord(t *testing.T) {
	l, base := newTestLogger(t)

	msg := MessageRef{
		MessageID:     "msg-1",
		CorrelationID: "corr-1",
		Type:          "request",
		SourceAgent:   "guide",
		TargetAgent:   "engineer",
	}
	l.Publish("request.engineer.eng-1", msg, 2, 128, nil)

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	records := readAll(t, base)
	var pub *BusLogRecord
	for i := range records {
		if records[i].Kind == KindPublish {
			pub = &records[i]
			break
		}
	}
	if pub == nil {
		t.Fatalf("no KindPublish record; records=%+v", records)
	}
	if pub.Publish.Topic != "request.engineer.eng-1" {
		t.Errorf("Topic = %q", pub.Publish.Topic)
	}
	if pub.Publish.Message.SourceAgent != "guide" {
		t.Errorf("SourceAgent = %q", pub.Publish.Message.SourceAgent)
	}
	if pub.Publish.SubscriberCount != 2 {
		t.Errorf("SubscriberCount = %d", pub.Publish.SubscriberCount)
	}
}

func TestBusLogger_DeliveryRecordsQueueLatency(t *testing.T) {
	l, base := newTestLogger(t)

	l.Delivery("request.engineer.eng-1", "request.engineer.eng-1", 42, "engineer/eng-1", false, 1500, MessageRef{
		MessageID:     "msg-2",
		CorrelationID: "corr-2",
		Type:          "request",
		SourceAgent:   "guide",
	}, nil)

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	records := readAll(t, base)
	var d *BusLogRecord
	for i := range records {
		if records[i].Kind == KindDelivery {
			d = &records[i]
			break
		}
	}
	if d == nil {
		t.Fatalf("no delivery record; records=%+v", records)
	}
	if d.Delivery.QueueLatencyMicros != 1500 {
		t.Errorf("QueueLatencyMicros = %d, want 1500", d.Delivery.QueueLatencyMicros)
	}
	if d.Delivery.SubscriberID != 42 {
		t.Errorf("SubscriberID = %d", d.Delivery.SubscriberID)
	}
}

func TestBusLogger_DeliveryRecordsHandlerError(t *testing.T) {
	l, base := newTestLogger(t)

	l.Delivery("t", "t", 1, "engineer/x", false, 0, MessageRef{}, errors.New("handler kaboom"))
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	records := readAll(t, base)
	found := false
	for _, r := range records {
		if r.Kind == KindDelivery && r.Delivery.HandlerErr == "handler kaboom" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected handler error recorded; records=%+v", records)
	}
}

func TestBusLogger_SubscribeUnsubscribeLifecycle(t *testing.T) {
	l, base := newTestLogger(t)

	l.Subscribed("request.engineer.x", 10, false, false, "", "engineer/x")
	l.Unsubscribed("request.engineer.x", 10, 500*time.Millisecond, "engineer/x")

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	records := readAll(t, base)
	sub := 0
	unsub := 0
	for _, r := range records {
		if r.Kind == KindSubscribe {
			sub++
		}
		if r.Kind == KindUnsubscribe {
			unsub++
			if r.Unsubscribe.Duration < 500*time.Millisecond {
				t.Errorf("Duration = %s, want >= 500ms", r.Unsubscribe.Duration)
			}
		}
	}
	if sub != 1 || unsub != 1 {
		t.Errorf("sub=%d unsub=%d, want 1+1", sub, unsub)
	}
}

func TestBusLogger_OverflowAndExpired(t *testing.T) {
	l, base := newTestLogger(t)

	l.Overflow("busy-topic", 5, 100, []string{"a", "b"}, 4096)
	l.Expired("late-topic", MessageRef{MessageID: "msg-x", CorrelationID: "c-x"}, time.Now())

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	records := readAll(t, base)
	over := 0
	exp := 0
	for _, r := range records {
		switch r.Kind {
		case KindOverflow:
			over++
			if r.Overflow.Dropped != 5 {
				t.Errorf("Overflow.Dropped = %d", r.Overflow.Dropped)
			}
		case KindExpired:
			exp++
			if r.Expired.Message.MessageID != "msg-x" {
				t.Errorf("Expired.Message.MessageID = %q", r.Expired.Message.MessageID)
			}
		}
	}
	if over != 1 || exp != 1 {
		t.Errorf("over=%d exp=%d, want 1+1", over, exp)
	}
}

func TestBusLogger_SeqMonotonicUnderConcurrency(t *testing.T) {
	base := t.TempDir()
	l, err := New(Config{
		BaseDir:    base,
		SessionID:  "sess-c",
		BufferSize: 4096,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const writers = 8
	const perWriter = 100
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				l.Publish("t", MessageRef{MessageID: "x"}, 0, 0, nil)
			}
		}()
	}
	wg.Wait()
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	records := readAll(t, base)
	if len(records) < writers*perWriter {
		t.Fatalf("got %d records, want >= %d", len(records), writers*perWriter)
	}
	seen := map[int64]struct{}{}
	var maxSeq int64
	for _, r := range records {
		if r.Seq <= 0 {
			t.Fatalf("non-positive seq in %+v", r)
		}
		if _, dup := seen[r.Seq]; dup {
			t.Fatalf("duplicate seq %d", r.Seq)
		}
		seen[r.Seq] = struct{}{}
		if r.Seq > maxSeq {
			maxSeq = r.Seq
		}
	}
	for i := int64(1); i <= maxSeq; i++ {
		if _, ok := seen[i]; !ok {
			t.Fatalf("missing seq %d in [1..%d]", i, maxSeq)
		}
	}
}

func TestBusLogger_SubscriberFanout(t *testing.T) {
	l, _ := newTestLogger(t)
	var mu sync.Mutex
	var received []BusLogRecord
	l.Subscribe(SubscriberFunc(func(r BusLogRecord) {
		mu.Lock()
		received = append(received, r)
		mu.Unlock()
	}))

	l.Publish("t", MessageRef{MessageID: "m-1"}, 1, 0, nil)
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 || received[0].Kind != KindPublish {
		t.Errorf("subscriber did not receive publish; got %+v", received)
	}
}

func TestBusLogger_CloseIsIdempotent(t *testing.T) {
	l, _ := newTestLogger(t)
	if err := l.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestDefaultSubscriberAgentResolver_ParsesCanonicalTopics(t *testing.T) {
	cases := map[string]string{
		"request.engineer.eng-1":  "engineer/eng-1",
		"response.inspector.isp": "inspector/isp",
		"error.guardian.g1":      "guardian/g1",
		"guide.requests":         "requests",
		"plaintopic":             "",
	}
	for topic, want := range cases {
		got := DefaultSubscriberAgentResolver(topic)
		if got != want {
			t.Errorf("DefaultSubscriberAgentResolver(%q) = %q, want %q", topic, got, want)
		}
	}
}
