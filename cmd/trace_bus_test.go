package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/buslog"
)

func seedBusLog(t *testing.T, sessionID string, records []buslog.BusLogRecord) string {
	t.Helper()
	root := t.TempDir()
	busDir := filepath.Join(root, sessionID, "bus", "2026-04-21")
	if err := os.MkdirAll(busDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f, err := os.Create(filepath.Join(busDir, "bus.000.jsonl"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, r := range records {
		if err := enc.Encode(r); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	return root
}

func busPublish(seq int64, topic, corrID, source string, ts time.Time) buslog.BusLogRecord {
	return buslog.BusLogRecord{
		Kind:      buslog.KindPublish,
		SessionID: "sess-t",
		Seq:       seq,
		Timestamp: ts,
		Publish: &buslog.PublishBody{
			Topic:           topic,
			SubscriberCount: 1,
			Message: buslog.MessageRef{
				MessageID:     "m-" + topic,
				CorrelationID: corrID,
				Type:          "request",
				SourceAgent:   source,
			},
		},
	}
}

func busDelivery(seq, subID int64, topic, corrID, subscriberAgent string, ts time.Time) buslog.BusLogRecord {
	return buslog.BusLogRecord{
		Kind:      buslog.KindDelivery,
		SessionID: "sess-t",
		Seq:       seq,
		Timestamp: ts,
		Delivery: &buslog.DeliveryBody{
			PublishTopic:       topic,
			SubscriberTopic:    topic,
			SubscriberID:       subID,
			SubscriberAgent:    subscriberAgent,
			QueueLatencyMicros: 150,
			Message: buslog.MessageRef{
				MessageID:     "m-" + topic,
				CorrelationID: corrID,
				Type:          "request",
			},
		},
	}
}

func busOverflow(seq int64, topic string, dropped int) buslog.BusLogRecord {
	return buslog.BusLogRecord{
		Kind:      buslog.KindOverflow,
		SessionID: "sess-t",
		Seq:       seq,
		Timestamp: time.Now(),
		Overflow: &buslog.OverflowBody{
			Topic:          topic,
			Dropped:        dropped,
			TotalDropped:   uint64(dropped),
			Correlations:   []string{"c1"},
			QueueRemaining: 4096,
		},
	}
}

func runBusCmd(t *testing.T, fn func(*bytes.Buffer) error) string {
	t.Helper()
	var out bytes.Buffer
	if err := fn(&out); err != nil {
		t.Fatalf("cmd error: %v", err)
	}
	return out.String()
}

func TestTraceBusSummary(t *testing.T) {
	ts := time.Now().Add(-time.Minute)
	records := []buslog.BusLogRecord{
		busPublish(1, "request.engineer.e1", "c-1", "guide", ts),
		busDelivery(2, 10, "request.engineer.e1", "c-1", "engineer/e1", ts),
		busPublish(3, "response.guide.g1", "c-1", "engineer", ts),
		busOverflow(4, "busy.topic", 5),
	}
	root := seedBusLog(t, "sess-t", records)

	prevDir := traceSessionDir
	prevSid := traceBusSession
	traceSessionDir = root
	traceBusSession = "sess-t"
	t.Cleanup(func() {
		traceSessionDir = prevDir
		traceBusSession = prevSid
	})

	out := runBusCmd(t, func(w *bytes.Buffer) error {
		cmd := traceBusSummaryCmd
		cmd.SetOut(w)
		return runTraceBusSummary(cmd, nil)
	})

	for _, want := range []string{
		"records             : 4",
		"bus_publish        2",
		"bus_delivery       1",
		"bus_overflow       1",
		"overflow drops total: 5",
		"top topics",
		"top publishers",
		"top subscribers",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q; output:\n%s", want, out)
		}
	}
}

func TestTraceBusTopics(t *testing.T) {
	ts := time.Now()
	records := []buslog.BusLogRecord{
		busPublish(1, "request.engineer.e1", "c-a", "guide", ts),
		busPublish(2, "request.engineer.e1", "c-b", "guide", ts),
		busPublish(3, "response.guide.g1", "c-a", "engineer", ts),
		busDelivery(4, 10, "request.engineer.e1", "c-a", "engineer/e1", ts),
	}
	root := seedBusLog(t, "sess-t", records)

	traceSessionDir = root
	traceBusSession = "sess-t"

	out := runBusCmd(t, func(w *bytes.Buffer) error {
		cmd := traceBusTopicsCmd
		cmd.SetOut(w)
		return runTraceBusTopics(cmd, nil)
	})

	if !strings.Contains(out, "request.engineer.e1") {
		t.Errorf("expected request topic; output:\n%s", out)
	}
	if !strings.Contains(out, "response.guide.g1") {
		t.Errorf("expected response topic; output:\n%s", out)
	}
	// request.engineer.e1 has 2 pubs, response has 1 — request should rank first.
	lines := strings.Split(out, "\n")
	reqIdx, respIdx := -1, -1
	for i, line := range lines {
		if strings.Contains(line, "request.engineer.e1") {
			reqIdx = i
		}
		if strings.Contains(line, "response.guide.g1") {
			respIdx = i
		}
	}
	if reqIdx < 0 || respIdx < 0 || reqIdx > respIdx {
		t.Errorf("request topic should rank first; req=%d resp=%d; output:\n%s", reqIdx, respIdx, out)
	}
}

func TestTraceBusFollow(t *testing.T) {
	ts := time.Now()
	records := []buslog.BusLogRecord{
		busPublish(1, "request.engineer.e1", "corr-follow", "guide", ts),
		busDelivery(2, 10, "request.engineer.e1", "corr-follow", "engineer/e1", ts),
		busPublish(3, "request.engineer.e1", "other-corr", "guide", ts),
	}
	root := seedBusLog(t, "sess-t", records)
	traceSessionDir = root
	traceBusSession = "sess-t"

	out := runBusCmd(t, func(w *bytes.Buffer) error {
		cmd := traceBusFollowCmd
		cmd.SetOut(w)
		return runTraceBusFollow(cmd, []string{"corr-follow"})
	})
	if !strings.Contains(out, "Correlation corr-follow — 2 touchpoints") {
		t.Errorf("follow count wrong; output:\n%s", out)
	}
	if strings.Contains(out, "other-corr") {
		t.Errorf("follow should not include unrelated correlation; output:\n%s", out)
	}
}

func TestTraceBusOverflows(t *testing.T) {
	records := []buslog.BusLogRecord{
		busOverflow(1, "busy-a", 3),
		busOverflow(2, "busy-b", 7),
	}
	root := seedBusLog(t, "sess-t", records)
	traceSessionDir = root
	traceBusSession = "sess-t"

	out := runBusCmd(t, func(w *bytes.Buffer) error {
		cmd := traceBusOverflowsCmd
		cmd.SetOut(w)
		return runTraceBusOverflows(cmd, nil)
	})
	if !strings.Contains(out, "Overflows: 2") {
		t.Errorf("overflow count wrong; output:\n%s", out)
	}
	if !strings.Contains(out, "busy-a") || !strings.Contains(out, "busy-b") {
		t.Errorf("overflow topics missing; output:\n%s", out)
	}
}

func TestTraceBusAgent(t *testing.T) {
	ts := time.Now()
	records := []buslog.BusLogRecord{
		busPublish(1, "request.engineer.e1", "c1", "guide", ts),
		busPublish(2, "request.engineer.e1", "c2", "guide", ts),
		busDelivery(3, 10, "request.engineer.e1", "c1", "engineer/e1", ts),
		busPublish(4, "other", "cx", "archivalist", ts),
	}
	root := seedBusLog(t, "sess-t", records)
	traceSessionDir = root
	traceBusSession = "sess-t"

	out := runBusCmd(t, func(w *bytes.Buffer) error {
		cmd := traceBusAgentCmd
		cmd.SetOut(w)
		return runTraceBusAgent(cmd, []string{"guide"})
	})
	if !strings.Contains(out, "footprint: 2 published, 0 received") {
		t.Errorf("guide footprint wrong; output:\n%s", out)
	}
}
