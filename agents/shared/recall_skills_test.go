package shared

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/activity"
)

// stubRecallSource implements activity.Source over an in-memory slice.
type stubRecallSource struct {
	rows []activity.AgentActivity
}

func (s *stubRecallSource) FilterActivities(_ context.Context, f activity.QueryFilter) ([]activity.AgentActivity, error) {
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
		if f.SubjectPathPrefix != "" && !strings.HasPrefix(r.Subject.PathPrefix, f.SubjectPathPrefix) {
			continue
		}
		if !f.Since.IsZero() && r.Timestamp.Before(f.Since) {
			continue
		}
		out = append(out, r)
		if f.Limit > 0 && len(out) >= f.Limit {
			break
		}
	}
	return out, nil
}

func (s *stubRecallSource) GetActivity(_ context.Context, id activity.ActivityID) (*activity.AgentActivity, error) {
	for i := range s.rows {
		if s.rows[i].ID == id {
			return &s.rows[i], nil
		}
	}
	return nil, nil
}

func (s *stubRecallSource) LatestActivityID(_ context.Context, _ activity.QueryFilter) (activity.ActivityID, error) {
	if len(s.rows) == 0 {
		return "", nil
	}
	return s.rows[len(s.rows)-1].ID, nil
}

func mkNarration(sess, agentType, scope string, replicaGen int, when time.Time) activity.AgentActivity {
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
				"replica_generation": itoaForRecall(replicaGen),
			},
		},
		Payload:     []byte(`{"summary":"test"}`),
		SourceTable: "archivalist_entries",
		SourceID:    "entry-" + itoaForRecall(replicaGen),
	}
}

func itoaForRecall(n int) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	out := []byte{}
	for n > 0 {
		out = append([]byte{digits[n%10]}, out...)
		n /= 10
	}
	return string(out)
}

func TestRecallSkill_ReturnsRecentNarrations(t *testing.T) {
	src := &stubRecallSource{rows: []activity.AgentActivity{
		mkNarration("sess-1", "engineer", "services/billing/", 3, time.Now().Add(-5*time.Minute)),
		mkNarration("sess-1", "engineer", "services/billing/", 3, time.Now().Add(-2*time.Minute)),
		mkNarration("sess-1", "tester-pipeline", "tests/", 1, time.Now().Add(-1*time.Minute)),
	}}
	skill := recallMyHistorySkill(RecallSkillConfig{
		SourceProvider: func() activity.Source { return src },
		SessionID:      func() string { return "sess-1" },
		AgentID:        func() string { return "eng-1" },
		AgentType:      func() string { return "engineer" },
	})

	out, err := skill.Handler(context.Background(), json.RawMessage(`{"scope":"services/"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	res, ok := out.(RecallResult)
	if !ok {
		t.Fatalf("expected RecallResult; got %T", out)
	}
	if res.AgentType != "engineer" {
		t.Errorf("AgentType = %q, want engineer", res.AgentType)
	}
	if len(res.Entries) != 2 {
		t.Fatalf("expected 2 engineer narrations; got %d", len(res.Entries))
	}
	for _, e := range res.Entries {
		if e.ReplicaGeneration != 3 {
			t.Errorf("entry replica_generation = %d, want 3", e.ReplicaGeneration)
		}
	}
}

func TestRecallSkill_FiltersOtherAgentTypes(t *testing.T) {
	src := &stubRecallSource{rows: []activity.AgentActivity{
		mkNarration("sess-1", "engineer", "x/", 1, time.Now()),
		mkNarration("sess-1", "tester-pipeline", "x/", 1, time.Now()),
	}}
	skill := recallMyHistorySkill(RecallSkillConfig{
		SourceProvider: func() activity.Source { return src },
		SessionID:      func() string { return "sess-1" },
		AgentType:      func() string { return "engineer" },
	})
	out, _ := skill.Handler(context.Background(), json.RawMessage(`{}`))
	res := out.(RecallResult)
	if len(res.Entries) != 1 {
		t.Fatalf("expected only engineer narration; got %d entries", len(res.Entries))
	}
}

func TestRecallSkill_EmptyWhenSourceUnavailable(t *testing.T) {
	skill := recallMyHistorySkill(RecallSkillConfig{
		SourceProvider: func() activity.Source { return nil },
		SessionID:      func() string { return "sess-1" },
		AgentType:      func() string { return "engineer" },
	})
	out, err := skill.Handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Run with nil source must succeed: %v", err)
	}
	res := out.(RecallResult)
	if len(res.Entries) != 0 {
		t.Fatalf("expected empty entries; got %d", len(res.Entries))
	}
	if res.Note == "" {
		t.Errorf("expected note explaining empty result")
	}
}

func TestRecallSkill_ExplicitAnyGenerationSentinel(t *testing.T) {
	src := &stubRecallSource{rows: []activity.AgentActivity{
		mkNarration("sess-1", "engineer", "x/", 1, time.Now()),
		mkNarration("sess-1", "engineer", "x/", 2, time.Now()),
		mkNarration("sess-1", "engineer", "x/", 3, time.Now()),
	}}
	skill := recallMyHistorySkill(RecallSkillConfig{
		SourceProvider: func() activity.Source { return src },
		SessionID:      func() string { return "sess-1" },
		AgentType:      func() string { return "engineer" },
	})
	out, _ := skill.Handler(context.Background(), json.RawMessage(`{"replica_generations":[0]}`))
	res := out.(RecallResult)
	if len(res.Entries) != 3 {
		t.Fatalf("any-generation sentinel should include all; got %d", len(res.Entries))
	}
}

func TestRecallSkill_ExplicitGenerationFilter(t *testing.T) {
	src := &stubRecallSource{rows: []activity.AgentActivity{
		mkNarration("sess-1", "engineer", "x/", 1, time.Now()),
		mkNarration("sess-1", "engineer", "x/", 2, time.Now()),
		mkNarration("sess-1", "engineer", "x/", 3, time.Now()),
	}}
	skill := recallMyHistorySkill(RecallSkillConfig{
		SourceProvider: func() activity.Source { return src },
		SessionID:      func() string { return "sess-1" },
		AgentType:      func() string { return "engineer" },
	})
	out, _ := skill.Handler(context.Background(), json.RawMessage(`{"replica_generations":[2]}`))
	res := out.(RecallResult)
	if len(res.Entries) != 1 {
		t.Fatalf("expected only generation-2 entry; got %d", len(res.Entries))
	}
	if res.Entries[0].ReplicaGeneration != 2 {
		t.Fatalf("got generation = %d", res.Entries[0].ReplicaGeneration)
	}
}

func TestRecallSkill_RespectsMaxEntries(t *testing.T) {
	rows := make([]activity.AgentActivity, 0, 30)
	for i := 0; i < 30; i++ {
		rows = append(rows, mkNarration("sess-1", "engineer", "x/", 1, time.Now()))
	}
	skill := recallMyHistorySkill(RecallSkillConfig{
		SourceProvider: func() activity.Source { return &stubRecallSource{rows: rows} },
		SessionID:      func() string { return "sess-1" },
		AgentType:      func() string { return "engineer" },
	})
	out, _ := skill.Handler(context.Background(), json.RawMessage(`{"max_entries":5}`))
	res := out.(RecallResult)
	if len(res.Entries) != 5 {
		t.Fatalf("expected max_entries=5 to cap; got %d", len(res.Entries))
	}
}
