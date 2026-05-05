package durable

import (
	"path/filepath"
	"testing"
)

func TestJournalAppendAndReplay(t *testing.T) {
	dir := t.TempDir()
	j, err := Open(JournalConfig{SessionDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	events := []Event{
		{Kind: EventOpRequested, OperationID: "op-1", LocalName: "mcp.test.read"},
		{Kind: EventApprovalRequested, OperationID: "op-1"},
		{Kind: EventApprovalResolved, OperationID: "op-1"},
		{Kind: EventOpCompleted, OperationID: "op-1", ResultSummary: "done"},
	}
	for _, ev := range events {
		if err := j.Append(ev); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if err := j.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	j2, err := Open(JournalConfig{SessionDir: dir})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer j2.Close()
	var seen int
	if err := j2.Replay(func(Event) error { seen++; return nil }); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if seen != len(events) {
		t.Fatalf("expected %d events, got %d", len(events), seen)
	}
}

func TestProjectionApplyTransitions(t *testing.T) {
	proj := NewProjection()
	op := "op-1"
	proj.Apply(Event{Kind: EventOpRequested, OperationID: op, LocalName: "mcp.test.write"})
	proj.Apply(Event{Kind: EventApprovalRequested, OperationID: op})
	proj.Apply(Event{Kind: EventApprovalResolved, OperationID: op})
	proj.Apply(Event{Kind: EventOpCompleted, OperationID: op, ResultSummary: "ok"})
	got, ok := proj.Get(op)
	if !ok {
		t.Fatal("op not found in projection")
	}
	if got.Status != StatusCompleted {
		t.Fatalf("expected completed, got %s", got.Status)
	}
	if got.ResultSummary != "ok" {
		t.Fatalf("expected result, got %q", got.ResultSummary)
	}
}

func TestProjectionApprovalDenialKeepsOpVisible(t *testing.T) {
	proj := NewProjection()
	op := "op-deny"
	proj.Apply(Event{Kind: EventOpRequested, OperationID: op})
	proj.Apply(Event{Kind: EventApprovalRequested, OperationID: op})
	proj.Apply(Event{Kind: EventApprovalResolved, OperationID: op, ErrorMessage: "guardian denied"})
	got, ok := proj.Get(op)
	if !ok {
		t.Fatal("op missing")
	}
	if got.Status != StatusApprovalDenied {
		t.Fatalf("status = %s", got.Status)
	}
	if got.Error == "" {
		t.Fatal("expected error to be recorded")
	}
}

func TestBlobStorePutIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	bs, err := NewBlobStore(dir)
	if err != nil {
		t.Fatalf("blob store: %v", err)
	}
	body := []byte("the quick brown fox")
	a, err := bs.Put(body)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	b, err := bs.Put(body)
	if err != nil {
		t.Fatalf("put twice: %v", err)
	}
	if a != b {
		t.Fatalf("digest changed: %s vs %s", a, b)
	}
	if _, err := bs.Open(a); err != nil {
		t.Fatalf("open: %v", err)
	}
	if got := bs.Path(a); got == "" || filepath.Base(got) != a {
		t.Fatalf("unexpected path: %s", got)
	}
}
