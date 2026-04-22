package activity

import (
	"testing"
	"time"
)

func TestRecordAndRecallForestConsult(t *testing.T) {
	t.Cleanup(ClearForestConsultLinksForTesting)
	ClearForestConsultLinksForTesting()

	RecordForestConsult("sess-1", "engineer-1", "consult-abc")

	id, ok := RecentForestConsult("sess-1", "engineer-1")
	if !ok {
		t.Fatal("expected consult to be present")
	}
	if id != "consult-abc" {
		t.Errorf("ActivityID = %q, want consult-abc", id)
	}
}

func TestRecentForestConsult_MissingKeysReturnFalse(t *testing.T) {
	t.Cleanup(ClearForestConsultLinksForTesting)
	ClearForestConsultLinksForTesting()

	if _, ok := RecentForestConsult("", "engineer-1"); ok {
		t.Error("empty sessionID should return false")
	}
	if _, ok := RecentForestConsult("sess-1", ""); ok {
		t.Error("empty agentID should return false")
	}
	if _, ok := RecentForestConsult("sess-1", "engineer-1"); ok {
		t.Error("absent entry should return false")
	}
}

func TestRecordForestConsult_LastWriteWins(t *testing.T) {
	t.Cleanup(ClearForestConsultLinksForTesting)
	ClearForestConsultLinksForTesting()

	RecordForestConsult("sess-1", "engineer-1", "consult-1")
	RecordForestConsult("sess-1", "engineer-1", "consult-2")

	id, ok := RecentForestConsult("sess-1", "engineer-1")
	if !ok || id != "consult-2" {
		t.Errorf("RecentForestConsult = %q, want consult-2 (last-write-wins)", id)
	}
}

func TestRecentForestConsult_TTLExpiry(t *testing.T) {
	t.Cleanup(ClearForestConsultLinksForTesting)
	ClearForestConsultLinksForTesting()

	SetForestConsultLinkTTLForTesting(50 * time.Millisecond)
	RecordForestConsult("sess-1", "engineer-1", "consult-stale")
	time.Sleep(80 * time.Millisecond)

	if _, ok := RecentForestConsult("sess-1", "engineer-1"); ok {
		t.Error("stale consult should be evicted after TTL")
	}
}

func TestEnrichCausationFromConsult_PopulatesCausedWhenPresent(t *testing.T) {
	t.Cleanup(ClearForestConsultLinksForTesting)
	ClearForestConsultLinksForTesting()

	RecordForestConsult("sess-e", "agent-e", "consult-e")
	a := AgentActivity{
		SessionID: "sess-e",
		Actor:     Actor{AgentID: "agent-e"},
	}
	EnrichCausationFromConsult(&a)

	if a.Caused == nil {
		t.Fatal("Caused should be populated from recent consult")
	}
	if *a.Caused != "consult-e" {
		t.Errorf("Caused = %q, want consult-e", *a.Caused)
	}
}

func TestEnrichCausationFromConsult_RespectsExistingCaused(t *testing.T) {
	t.Cleanup(ClearForestConsultLinksForTesting)
	ClearForestConsultLinksForTesting()

	RecordForestConsult("sess-e", "agent-e", "consult-weaker")
	prior := ActivityID("span-ancestor")
	a := AgentActivity{
		SessionID: "sess-e",
		Actor:     Actor{AgentID: "agent-e"},
		Caused:    &prior,
	}
	EnrichCausationFromConsult(&a)

	if a.Caused == nil || *a.Caused != "span-ancestor" {
		t.Errorf("Caused = %v, want span-ancestor (stronger upstream causation should win)", a.Caused)
	}
}

func TestEnrichCausationFromConsult_NoOpWhenNoConsult(t *testing.T) {
	t.Cleanup(ClearForestConsultLinksForTesting)
	ClearForestConsultLinksForTesting()

	a := AgentActivity{
		SessionID: "sess-none",
		Actor:     Actor{AgentID: "agent-none"},
	}
	EnrichCausationFromConsult(&a)
	if a.Caused != nil {
		t.Errorf("Caused = %v, want nil when no consult recorded", a.Caused)
	}
}

func TestRecordForestConsult_PerSessionIsolation(t *testing.T) {
	t.Cleanup(ClearForestConsultLinksForTesting)
	ClearForestConsultLinksForTesting()

	RecordForestConsult("sess-a", "engineer", "consult-a")
	RecordForestConsult("sess-b", "engineer", "consult-b")

	if id, ok := RecentForestConsult("sess-a", "engineer"); !ok || id != "consult-a" {
		t.Errorf("sess-a consult = %q, want consult-a", id)
	}
	if id, ok := RecentForestConsult("sess-b", "engineer"); !ok || id != "consult-b" {
		t.Errorf("sess-b consult = %q, want consult-b", id)
	}
}
