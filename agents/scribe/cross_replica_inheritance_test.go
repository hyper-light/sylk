package scribe

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/providers"
)

func TestInheritPriorReplicaNarrative_NoOpForFirstReplica(t *testing.T) {
	col := activity.NewTestCollector()
	prevSink := activity.SetDefaultSink(col)
	defer activity.SetDefaultSink(prevSink)

	s := &Scribe{
		parentAgentType:   "tester-pipeline",
		sessionID:         "sess-1",
		replicaGeneration: 1, // first replica → no prior to inherit
		workstreams:       make(map[string]*scribeWorkstream),
	}
	s.inheritPriorReplicaNarrative(context.Background())
	if _, ok := s.workstreams[crossReplicaWorkstreamKey]; ok {
		t.Fatal("first replica must not seed inheritance workstream")
	}
}

func TestInheritPriorReplicaNarrative_NoOpWhenSourceUnavailable(t *testing.T) {
	prevSrc := activity.SetDefaultSource(nil)
	defer activity.SetDefaultSource(prevSrc)

	s := &Scribe{
		parentAgentType:   "engineer",
		sessionID:         "sess-1",
		replicaGeneration: 3,
		workstreams:       make(map[string]*scribeWorkstream),
	}
	s.inheritPriorReplicaNarrative(context.Background())
	if _, ok := s.workstreams[crossReplicaWorkstreamKey]; ok {
		t.Fatal("inheritance must be a no-op when source isn't wired")
	}
}

func TestInheritPriorReplicaNarrative_SeedsFromPriorReplicas(t *testing.T) {
	src := &stubInheritanceSource{rows: []activity.AgentActivity{
		makeNarrationActivity("sess-1", "engineer", "services/billing/", 1, time.Now().Add(-30*time.Minute)),
		makeNarrationActivity("sess-1", "engineer", "services/api/", 2, time.Now().Add(-15*time.Minute)),
		// current generation 3 — should NOT be included
		makeNarrationActivity("sess-1", "engineer", "services/auth/", 3, time.Now().Add(-1*time.Minute)),
	}}
	prevSrc := activity.SetDefaultSource(src)
	defer activity.SetDefaultSource(prevSrc)

	s := &Scribe{
		parentAgentType:   "engineer",
		sessionID:         "sess-1",
		replicaGeneration: 3,
		workstreams:       make(map[string]*scribeWorkstream),
	}
	s.inheritPriorReplicaNarrative(context.Background())

	ws, ok := s.workstreams[crossReplicaWorkstreamKey]
	if !ok {
		t.Fatal("inheritance workstream not seeded")
	}
	if len(ws.messages) != 1 {
		t.Fatalf("expected 1 seed message; got %d", len(ws.messages))
	}
	content := ws.messages[0].Content
	if !strings.Contains(content, "Replica 3 of engineer") {
		t.Errorf("seed should mention current generation; got %s", content)
	}
	if !strings.Contains(content, "gen 1") || !strings.Contains(content, "gen 2") {
		t.Errorf("seed should reference prior generations 1 and 2; got %s", content)
	}
	if strings.Contains(content, "gen 3") {
		t.Errorf("seed must NOT include the current generation; got %s", content)
	}
}

func TestInheritPriorReplicaNarrative_IsIdempotent(t *testing.T) {
	src := &stubInheritanceSource{rows: []activity.AgentActivity{
		makeNarrationActivity("sess-1", "engineer", "x/", 1, time.Now()),
	}}
	prevSrc := activity.SetDefaultSource(src)
	defer activity.SetDefaultSource(prevSrc)

	s := &Scribe{
		parentAgentType:   "engineer",
		sessionID:         "sess-1",
		replicaGeneration: 2,
		workstreams:       make(map[string]*scribeWorkstream),
	}
	s.inheritPriorReplicaNarrative(context.Background())
	originalSeed := s.workstreams[crossReplicaWorkstreamKey]
	s.inheritPriorReplicaNarrative(context.Background())
	if got := s.workstreams[crossReplicaWorkstreamKey]; got != originalSeed {
		t.Fatal("second invocation must not replace existing seed")
	}
}

func TestSelectPriorReplicaEntries_OmitsCurrentGeneration(t *testing.T) {
	rows := []activity.AgentActivity{
		makeNarrationActivity("sess-1", "engineer", "x/", 1, time.Now()),
		makeNarrationActivity("sess-1", "engineer", "x/", 2, time.Now()),
		makeNarrationActivity("sess-1", "engineer", "x/", 3, time.Now()), // current
	}
	got := selectPriorReplicaEntries(rows, 3)
	if len(got) != 2 {
		t.Fatalf("expected 2 prior entries; got %d", len(got))
	}
	for _, e := range got {
		if e.replicaGeneration >= 3 {
			t.Errorf("entry generation = %d, must be < current (3)", e.replicaGeneration)
		}
	}
}

func TestBuildInheritanceSeedMessage_ContainsPreamble(t *testing.T) {
	entries := []inheritanceEntry{
		{
			timestamp:         time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
			replicaGeneration: 1,
			scope:             "services/billing/",
			commentary:        []byte(`{"summary":"prior life"}`),
		},
	}
	msg := buildInheritanceSeedMessage("tester-pipeline", 2, entries)
	if msg.Role != providers.RoleUser {
		t.Errorf("seed message role = %s, want user", msg.Role)
	}
	for _, want := range []string{
		"cross-replica inheritance",
		"Replica 2 of tester-pipeline",
		"gen 1",
		"prior life",
	} {
		if !strings.Contains(msg.Content, want) {
			t.Errorf("seed missing %q; got %s", want, msg.Content)
		}
	}
}

// ─── stub source for inheritance tests ────────────────────────────────

type stubInheritanceSource struct {
	rows []activity.AgentActivity
}

func (s *stubInheritanceSource) FilterActivities(_ context.Context, f activity.QueryFilter) ([]activity.AgentActivity, error) {
	var out []activity.AgentActivity
	for _, r := range s.rows {
		if f.SessionID != "" && r.SessionID != f.SessionID {
			continue
		}
		if len(f.ActionKinds) > 0 {
			ok := false
			for _, k := range f.ActionKinds {
				if r.Action == k {
					ok = true
					break
				}
			}
			if !ok {
				continue
			}
		}
		if f.SubjectDomain != "" && r.Subject.Domain != f.SubjectDomain {
			continue
		}
		out = append(out, r)
		if f.Limit > 0 && len(out) >= f.Limit {
			break
		}
	}
	return out, nil
}

func (s *stubInheritanceSource) GetActivity(_ context.Context, id activity.ActivityID) (*activity.AgentActivity, error) {
	for i := range s.rows {
		if s.rows[i].ID == id {
			return &s.rows[i], nil
		}
	}
	return nil, nil
}

func (s *stubInheritanceSource) LatestActivityID(_ context.Context, _ activity.QueryFilter) (activity.ActivityID, error) {
	if len(s.rows) == 0 {
		return "", nil
	}
	return s.rows[len(s.rows)-1].ID, nil
}

func makeNarrationActivity(sess, agentType, scope string, replicaGen int, when time.Time) activity.AgentActivity {
	return activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(sess),
		Timestamp:  when,
		Resolution: activity.ResolutionCoarse,
		Action:     activity.ActionNarrationEmitted,
		Subject: activity.Subject{
			Domain:     agentType,
			PathPrefix: scope,
			Coordinates: map[string]string{
				"replica_generation": itoaShort(replicaGen),
			},
		},
		Payload: []byte(`{"summary":"test"}`),
	}
}

func itoaShort(n int) string {
	if n == 0 {
		return "0"
	}
	out := []byte{}
	for n > 0 {
		out = append([]byte{byte('0' + n%10)}, out...)
		n /= 10
	}
	return string(out)
}
