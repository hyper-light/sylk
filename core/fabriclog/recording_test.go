package fabriclog

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/activity"
)

// ─── Fake Sink + Source ────────────────────────────────────────────

type fakeSink struct {
	mu    sync.Mutex
	seen  []activity.AgentActivity
	calls int
}

func (s *fakeSink) Append(_ context.Context, a activity.AgentActivity) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, a)
	s.calls++
}

type fakeSource struct {
	filterResult []activity.AgentActivity
	filterErr    error
	getResult    *activity.AgentActivity
	getErr       error
	latestID     activity.ActivityID
	latestErr    error

	filterCalls int
	getCalls    int
	latestCalls int
}

func (s *fakeSource) FilterActivities(_ context.Context, _ activity.QueryFilter) ([]activity.AgentActivity, error) {
	s.filterCalls++
	return s.filterResult, s.filterErr
}
func (s *fakeSource) GetActivity(_ context.Context, _ activity.ActivityID) (*activity.AgentActivity, error) {
	s.getCalls++
	return s.getResult, s.getErr
}
func (s *fakeSource) LatestActivityID(_ context.Context, _ activity.QueryFilter) (activity.ActivityID, error) {
	s.latestCalls++
	return s.latestID, s.latestErr
}

// ─── RecordingSink ─────────────────────────────────────────────────

func TestRecordingSink_ForwardsToInnerAndEmits(t *testing.T) {
	l, base := newLogger(t, 128, 1024)
	inner := &fakeSink{}
	rec := NewRecordingSink(inner, l)

	a := sampleActivity("act-x")
	rec.Append(context.Background(), a)

	if inner.calls != 1 {
		t.Fatalf("inner sink calls = %d, want 1", inner.calls)
	}
	if len(inner.seen) != 1 || inner.seen[0].ID != "act-x" {
		t.Fatalf("inner sink did not receive activity; got %+v", inner.seen)
	}

	drainAll(t, l)
	records := readAllRecords(t, base)

	hits := 0
	for _, r := range records {
		if r.Kind == KindPublish && r.Publish.ActivityID == "act-x" {
			hits++
		}
	}
	if hits != 1 {
		t.Fatalf("expected 1 KindPublish, got %d; records=%+v", hits, records)
	}
}

func TestRecordingSink_NilLoggerIsTransparent(t *testing.T) {
	inner := &fakeSink{}
	rec := NewRecordingSink(inner, nil)
	rec.Append(context.Background(), sampleActivity("nil-logger"))
	if inner.calls != 1 {
		t.Fatalf("inner sink calls = %d, want 1", inner.calls)
	}
}

// ─── RecordingSource ───────────────────────────────────────────────

func TestRecordingSource_FilterActivities_EmitsReadPlusConsumePerResult(t *testing.T) {
	l, base := newLogger(t, 256, 1024)

	// Populate recent cache via Publish so consume records can join.
	a1 := sampleActivity("act-1")
	a2 := sampleActivity("act-2")
	l.RecordPublish(a1)
	l.RecordPublish(a2)

	inner := &fakeSource{filterResult: []activity.AgentActivity{a1, a2}}
	rec := NewRecordingSource(inner, l)

	ctx := WithReader(context.Background(), AgentRef{AgentID: "reader", AgentType: "inspector"})
	ctx = WithLensAlias(ctx, "WhatAreTheyDoing")
	got, err := rec.FilterActivities(ctx, activity.QueryFilter{
		ActionKinds:       []activity.ActionKind{"decision_declared"},
		SubjectPathPrefix: "services/billing",
		Limit:             50,
	})
	if err != nil {
		t.Fatalf("FilterActivities: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("result len = %d, want 2", len(got))
	}
	if inner.filterCalls != 1 {
		t.Fatalf("inner calls = %d, want 1", inner.filterCalls)
	}

	drainAll(t, l)
	records := readAllRecords(t, base)

	var readSeq int64
	reads := 0
	consumes := 0
	for _, r := range records {
		switch r.Kind {
		case KindRead:
			reads++
			if r.Read.Lens != "FilterActivities" {
				t.Errorf("read.Lens = %q", r.Read.Lens)
			}
			if r.Read.LensAlias != "WhatAreTheyDoing" {
				t.Errorf("read.LensAlias = %q", r.Read.LensAlias)
			}
			if r.Read.ReturnedCount != 2 {
				t.Errorf("ReturnedCount = %d", r.Read.ReturnedCount)
			}
			if len(r.Read.ReturnedIDs) != 2 {
				t.Errorf("ReturnedIDs len = %d", len(r.Read.ReturnedIDs))
			}
			if r.Agent.AgentID != "reader" {
				t.Errorf("Agent attribution missing: %+v", r.Agent)
			}
			if r.Read.Filter == nil || r.Read.Filter.FilterHash == "" {
				t.Errorf("Filter hash missing: %+v", r.Read.Filter)
			}
			readSeq = r.Seq
		case KindConsume:
			consumes++
			if r.Consume.ReaderRecordSeq == 0 {
				t.Errorf("consume missing ReaderRecordSeq")
			}
			if r.Consume.PublishSeq == 0 {
				t.Errorf("consume.PublishSeq = 0; expected recent-cache join")
			}
		}
	}
	if reads != 1 {
		t.Fatalf("expected 1 KindRead, got %d", reads)
	}
	if consumes != 2 {
		t.Fatalf("expected 2 KindConsume, got %d", consumes)
	}
	if readSeq == 0 {
		t.Fatal("readSeq not captured")
	}
}

func TestRecordingSource_FilterActivities_RecordsErrorAndElapsed(t *testing.T) {
	l, base := newLogger(t, 64, 1024)
	inner := &fakeSource{filterErr: errors.New("kaboom")}
	rec := NewRecordingSource(inner, l)

	_, err := rec.FilterActivities(context.Background(), activity.QueryFilter{Limit: 10})
	if err == nil {
		t.Fatal("expected error passthrough")
	}

	drainAll(t, l)
	records := readAllRecords(t, base)
	var read *FabricLogRecord
	for i := range records {
		if records[i].Kind == KindRead {
			read = &records[i]
			break
		}
	}
	if read == nil {
		t.Fatalf("no KindRead; records=%+v", records)
	}
	if read.Read.Err != "kaboom" {
		t.Errorf("err = %q, want 'kaboom'", read.Read.Err)
	}
}

func TestRecordingSource_GetActivity_EmitsOneConsumeOnHit(t *testing.T) {
	l, base := newLogger(t, 64, 1024)
	target := sampleActivity("target")
	l.RecordPublish(target)

	inner := &fakeSource{getResult: &target}
	rec := NewRecordingSource(inner, l)

	got, err := rec.GetActivity(context.Background(), target.ID)
	if err != nil || got == nil {
		t.Fatalf("GetActivity: got=%v err=%v", got, err)
	}

	drainAll(t, l)
	records := readAllRecords(t, base)
	reads := 0
	consumes := 0
	for _, r := range records {
		switch r.Kind {
		case KindRead:
			reads++
			if r.Read.Lens != "GetActivity" {
				t.Errorf("Lens = %q", r.Read.Lens)
			}
		case KindConsume:
			consumes++
		}
	}
	if reads != 1 || consumes != 1 {
		t.Fatalf("reads=%d consumes=%d, want 1+1; records=%+v", reads, consumes, records)
	}
}

func TestRecordingSource_GetActivity_MissStillRecordsRead(t *testing.T) {
	l, base := newLogger(t, 64, 1024)
	inner := &fakeSource{getResult: nil}
	rec := NewRecordingSource(inner, l)

	got, err := rec.GetActivity(context.Background(), "missing")
	if err != nil || got != nil {
		t.Fatalf("GetActivity miss: got=%v err=%v", got, err)
	}

	drainAll(t, l)
	records := readAllRecords(t, base)
	reads := 0
	for _, r := range records {
		if r.Kind == KindRead {
			reads++
			if r.Read.ReturnedCount != 0 {
				t.Errorf("miss read ReturnedCount = %d, want 0", r.Read.ReturnedCount)
			}
		}
	}
	if reads != 1 {
		t.Fatalf("expected 1 KindRead on miss, got %d", reads)
	}
}

func TestRecordingSource_LatestActivityID_RecordsLensNotConsume(t *testing.T) {
	l, base := newLogger(t, 64, 1024)
	inner := &fakeSource{latestID: "tip"}
	rec := NewRecordingSource(inner, l)

	got, err := rec.LatestActivityID(context.Background(), activity.QueryFilter{})
	if err != nil || got != "tip" {
		t.Fatalf("got=%v err=%v", got, err)
	}

	drainAll(t, l)
	records := readAllRecords(t, base)
	reads := 0
	consumes := 0
	for _, r := range records {
		switch r.Kind {
		case KindRead:
			reads++
			if r.Read.Lens != "LatestActivityID" {
				t.Errorf("Lens = %q", r.Read.Lens)
			}
		case KindConsume:
			consumes++
		}
	}
	if reads != 1 {
		t.Fatalf("expected 1 KindRead, got %d", reads)
	}
	if consumes != 0 {
		t.Fatalf("LatestActivityID should not emit consume; got %d", consumes)
	}
}

func TestRecordingSource_NilLoggerIsTransparent(t *testing.T) {
	inner := &fakeSource{filterResult: []activity.AgentActivity{sampleActivity("x")}}
	rec := NewRecordingSource(inner, nil)
	got, err := rec.FilterActivities(context.Background(), activity.QueryFilter{})
	if err != nil {
		t.Fatalf("nil-logger passthrough: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("inner result not forwarded: %+v", got)
	}
	if inner.filterCalls != 1 {
		t.Fatalf("inner not called")
	}
}

// ─── End-to-end flow via default installation ─────────────────────

func TestRecordingDecorators_EndToEndViaDefaults(t *testing.T) {
	l, base := newLogger(t, 256, 1024)
	prev := SetDefault(l)
	defer SetDefault(prev)

	sinkInner := &fakeSink{}
	srcInner := &fakeSource{}
	// Pre-seed a publish via the recording sink so the recent cache
	// is warm for the subsequent read.
	rs := NewRecordingSink(sinkInner, l)
	a := sampleActivity("e2e")
	rs.Append(context.Background(), a)

	srcInner.filterResult = []activity.AgentActivity{a}
	rsrc := NewRecordingSource(srcInner, l)
	_, err := rsrc.FilterActivities(
		WithReader(context.Background(), AgentRef{AgentID: "r", AgentType: "librarian"}),
		activity.QueryFilter{},
	)
	if err != nil {
		t.Fatalf("FilterActivities: %v", err)
	}

	// Small yield for drain.
	time.Sleep(10 * time.Millisecond)
	drainAll(t, l)
	records := readAllRecords(t, base)

	var publish, read, consume bool
	for _, r := range records {
		switch r.Kind {
		case KindPublish:
			publish = true
		case KindRead:
			read = true
		case KindConsume:
			consume = true
		}
	}
	if !(publish && read && consume) {
		t.Fatalf("missing expected kinds: publish=%v read=%v consume=%v", publish, read, consume)
	}
}
