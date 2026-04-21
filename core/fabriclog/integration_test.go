package fabriclog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/agentlog"
)

// recordingSinkFake stores appended activities in an in-memory slice
// so we can verify the decorator forwards to the inner sink while
// also emitting observability records.
type recordingSinkFake struct {
	mu   sync.Mutex
	seen []activity.AgentActivity
}

func (s *recordingSinkFake) Append(_ context.Context, a activity.AgentActivity) {
	s.mu.Lock()
	s.seen = append(s.seen, a)
	s.mu.Unlock()
}

// TestIntegration_EndToEnd_LogsToDisk wires a real FabricLogger,
// wraps a fake Sink and Source with the recording decorators, emits
// a publish + read + consume cycle, and verifies that all three
// record kinds land on disk under the expected .sylk/-style path.
func TestIntegration_EndToEnd_LogsToDisk(t *testing.T) {
	base := t.TempDir()

	logger, err := NewFabricLogger(FabricLoggerConfig{
		BaseDir:   base,
		SessionID: "sess-integ",
		BufferSize: 128,
		Redactor:   agentlog.NewRedactor(agentlog.RedactorConfig{}, nil),
	})
	if err != nil {
		t.Fatalf("NewFabricLogger: %v", err)
	}

	innerSink := &recordingSinkFake{}
	innerSource := &fakeSource{
		filterResult: []activity.AgentActivity{},
	}

	sink := NewRecordingSink(innerSink, logger)
	source := NewRecordingSource(innerSource, logger)

	// Publish an activity — goes through the decorator, which forwards
	// to the inner sink AND emits a KindPublish.
	pub := activity.AgentActivity{
		ID:        "act-e2e-1",
		SessionID: "sess-integ",
		Timestamp: time.Now(),
		Actor: activity.Actor{
			AgentID:   "engineer-1",
			AgentType: "engineer",
		},
		Action: activity.ActionKind("decision_declared"),
		Subject: activity.Subject{
			PathPrefix: "services/billing",
		},
		State: activity.StatePoint,
	}
	sink.Append(context.Background(), pub)

	// Configure the source to return that activity on the next read.
	innerSource.filterResult = []activity.AgentActivity{pub}

	// Read with attributed reader identity + lens alias.
	ctx := WithReader(context.Background(), AgentRef{
		AgentID:   "inspector-1",
		AgentType: "inspector",
	})
	ctx = WithLensAlias(ctx, "WhatAreTheyDoing")
	_, err = source.FilterActivities(ctx, activity.QueryFilter{
		SubjectPathPrefix: "services/billing",
		Limit:             10,
	})
	if err != nil {
		t.Fatalf("FilterActivities: %v", err)
	}

	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Verify the inner sink received the activity.
	if len(innerSink.seen) != 1 || innerSink.seen[0].ID != "act-e2e-1" {
		t.Fatalf("inner sink did not receive activity: %+v", innerSink.seen)
	}

	// Walk the logged files.
	var files []string
	_ = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".jsonl") {
			files = append(files, path)
		}
		return nil
	})
	if len(files) == 0 {
		t.Fatalf("no JSONL files created under %s", base)
	}
	sort.Strings(files)

	// Read every line and bucket by kind.
	byKind := map[RecordKind][]FabricLogRecord{}
	for _, p := range files {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var rec FabricLogRecord
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				t.Fatalf("parse %q: %v", line, err)
			}
			byKind[rec.Kind] = append(byKind[rec.Kind], rec)
		}
	}

	// Three kinds expected: publish, read, consume.
	if len(byKind[KindPublish]) != 1 {
		t.Errorf("expected 1 KindPublish, got %d", len(byKind[KindPublish]))
	}
	if len(byKind[KindRead]) != 1 {
		t.Errorf("expected 1 KindRead, got %d", len(byKind[KindRead]))
	}
	if len(byKind[KindConsume]) != 1 {
		t.Errorf("expected 1 KindConsume, got %d", len(byKind[KindConsume]))
	}

	if p := byKind[KindPublish]; len(p) == 1 {
		if p[0].Publish.ActivityID != "act-e2e-1" {
			t.Errorf("publish.ActivityID = %q", p[0].Publish.ActivityID)
		}
		if p[0].Agent.AgentID != "engineer-1" {
			t.Errorf("publish.Agent.AgentID = %q", p[0].Agent.AgentID)
		}
	}
	if r := byKind[KindRead]; len(r) == 1 {
		if r[0].Agent.AgentID != "inspector-1" {
			t.Errorf("read.Agent.AgentID = %q, want inspector-1 (from ctx)", r[0].Agent.AgentID)
		}
		if r[0].Read.LensAlias != "WhatAreTheyDoing" {
			t.Errorf("read.LensAlias = %q", r[0].Read.LensAlias)
		}
		if r[0].Read.Filter == nil || r[0].Read.Filter.SubjectPathPrefix != "services/billing" {
			t.Errorf("read.Filter = %+v", r[0].Read.Filter)
		}
	}
	if c := byKind[KindConsume]; len(c) == 1 {
		if c[0].Consume.ActivityID != "act-e2e-1" {
			t.Errorf("consume.ActivityID = %q", c[0].Consume.ActivityID)
		}
		if c[0].Consume.PublishSeq == 0 {
			t.Errorf("consume.PublishSeq = 0; expected recent-cache join")
		}
		if c[0].Consume.ProducedByAgent != "engineer-1" {
			t.Errorf("consume.ProducedByAgent = %q", c[0].Consume.ProducedByAgent)
		}
	}
}

// TestIntegration_ResolveEdgeEmitted tests the lifecycle edge path:
// publish a consult, publish its response with Resolves set, verify
// a KindResolve record lands with matched kind + latency.
func TestIntegration_ResolveEdgeEmitted(t *testing.T) {
	base := t.TempDir()
	logger, err := NewFabricLogger(FabricLoggerConfig{
		BaseDir:    base,
		SessionID:  "sess-life",
		BufferSize: 64,
	})
	if err != nil {
		t.Fatalf("NewFabricLogger: %v", err)
	}

	innerSink := &recordingSinkFake{}
	sink := NewRecordingSink(innerSink, logger)

	start := time.Now()
	orig := activity.AgentActivity{
		ID:        "consult-1",
		SessionID: "sess-life",
		Timestamp: start,
		Actor:     activity.Actor{AgentID: "eng-1", AgentType: "engineer"},
		Action:    activity.ActionKind("consult_emitted"),
		State:     activity.StateInFlight,
	}
	sink.Append(context.Background(), orig)

	resolvedID := orig.ID
	resp := activity.AgentActivity{
		ID:        "consult-1-resp",
		SessionID: "sess-life",
		Timestamp: start.Add(200 * time.Millisecond),
		Actor:     activity.Actor{AgentID: "librarian-1", AgentType: "librarian"},
		Action:    activity.ActionKind("consult_response"),
		Resolves:  &resolvedID,
		State:     activity.StatePoint,
	}
	sink.Append(context.Background(), resp)

	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Load records and find the resolve edge.
	var resolves []FabricLogRecord
	_ = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		data, _ := os.ReadFile(path)
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var rec FabricLogRecord
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				continue
			}
			if rec.Kind == KindResolve {
				resolves = append(resolves, rec)
			}
		}
		return nil
	})

	if len(resolves) != 1 {
		t.Fatalf("expected 1 KindResolve, got %d", len(resolves))
	}
	r := resolves[0].Resolve
	if r.ResolverActivityID != "consult-1-resp" {
		t.Errorf("ResolverActivityID = %q", r.ResolverActivityID)
	}
	if r.ResolvedActivityID != "consult-1" {
		t.Errorf("ResolvedActivityID = %q", r.ResolvedActivityID)
	}
	if r.ResolvedKind != "consult_emitted" {
		t.Errorf("ResolvedKind = %q (should join from recent cache)", r.ResolvedKind)
	}
	if r.LatencyMicros < 100_000 {
		t.Errorf("LatencyMicros = %d, expected >= 100000 (200ms)", r.LatencyMicros)
	}
}
