package lenses

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/activity"
)

// stubSource implements activity.Source over an in-memory slice.
type stubSource struct {
	rows []activity.AgentActivity
}

func (s *stubSource) FilterActivities(_ context.Context, f activity.QueryFilter) ([]activity.AgentActivity, error) {
	var out []activity.AgentActivity
	for _, r := range s.rows {
		if !matches(r, f) {
			continue
		}
		out = append(out, r)
		if f.Limit > 0 && len(out) >= f.Limit {
			break
		}
	}
	return out, nil
}

func (s *stubSource) GetActivity(_ context.Context, id activity.ActivityID) (*activity.AgentActivity, error) {
	for i := range s.rows {
		if s.rows[i].ID == id {
			return &s.rows[i], nil
		}
	}
	return nil, nil
}

func (s *stubSource) LatestActivityID(_ context.Context, _ activity.QueryFilter) (activity.ActivityID, error) {
	if len(s.rows) == 0 {
		return "", nil
	}
	return s.rows[len(s.rows)-1].ID, nil
}

func matches(a activity.AgentActivity, f activity.QueryFilter) bool {
	if f.SessionID != "" && a.SessionID != f.SessionID {
		return false
	}
	if len(f.ActionKinds) > 0 {
		ok := false
		for _, k := range f.ActionKinds {
			if a.Action == k {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if len(f.States) > 0 {
		ok := false
		for _, s := range f.States {
			if a.State == s {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if f.SubjectPathPrefix != "" && !strings.HasPrefix(a.Subject.PathPrefix, f.SubjectPathPrefix) {
		return false
	}
	if f.SubjectTargetAgent != "" && a.Subject.TargetAgent != f.SubjectTargetAgent {
		return false
	}
	if f.ActorAgentID != "" && a.Actor.AgentID != f.ActorAgentID {
		return false
	}
	if !f.Since.IsZero() && a.Timestamp.Before(f.Since) {
		return false
	}
	if f.Caused != nil {
		if a.Caused == nil || *a.Caused != *f.Caused {
			return false
		}
	}
	return true
}

func mkAct(kind activity.ActionKind, sess, scope, agentID string) activity.AgentActivity {
	return activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(sess),
		Timestamp:  time.Now(),
		Resolution: activity.ResolutionFor(kind),
		Action:     kind,
		Subject:    activity.Subject{PathPrefix: scope},
		Actor:      activity.Actor{AgentID: agentID, AgentType: "tester-pipeline"},
		State:      activity.StatePoint,
	}
}

func TestWhatAreTheyDoing_ExcludesCallerAgent(t *testing.T) {
	src := &stubSource{rows: []activity.AgentActivity{
		mkAct(activity.ActionDecisionDeclared, "s1", "tests/", "me"),
		mkAct(activity.ActionDecisionDeclared, "s1", "tests/", "peer-1"),
		mkAct(activity.ActionDecisionDeclared, "s1", "tests/", "peer-2"),
	}}
	got, err := WhatAreTheyDoing(context.Background(), src, PeerActivityQuery{
		SessionID:    "s1",
		Scope:        "tests/",
		ExcludeAgent: "me",
	})
	if err != nil {
		t.Fatalf("WhatAreTheyDoing: %v", err)
	}
	if len(got.Activities) != 2 {
		t.Fatalf("expected 2 peer activities, got %d", len(got.Activities))
	}
	for _, a := range got.Activities {
		if a.Actor.AgentID == "me" {
			t.Fatalf("caller's own activity leaked: %+v", a)
		}
	}
}

func TestCausalContext_WalksAncestorsAndChildren(t *testing.T) {
	root := mkAct(activity.ActionChallengeEmitted, "s1", "tests/", "tester-1")
	child := mkAct(activity.ActionChallengeResponse, "s1", "tests/", "tester-2")
	rootID := root.ID
	child.Caused = &rootID

	src := &stubSource{rows: []activity.AgentActivity{root, child}}
	got, err := CausalContext(context.Background(), src, child.ID, 5)
	if err != nil {
		t.Fatalf("CausalContext: %v", err)
	}
	if got.Target.ID != child.ID {
		t.Fatalf("Target ID mismatch")
	}
	if len(got.Ancestors) != 1 || got.Ancestors[0].ID != rootID {
		t.Fatalf("expected one ancestor (root), got %v", got.Ancestors)
	}
}

func TestConflictsOpen_ReturnsInFlightChallenges(t *testing.T) {
	open := mkAct(activity.ActionChallengeEmitted, "s1", "tests/", "tester-1")
	open.State = activity.StateInFlight
	closed := mkAct(activity.ActionChallengeEmitted, "s1", "tests/", "tester-2")
	closed.State = activity.StateResolved

	src := &stubSource{rows: []activity.AgentActivity{open, closed}}
	got, err := ConflictsOpen(context.Background(), src, OpenConflictsQuery{SessionID: "s1"})
	if err != nil {
		t.Fatalf("ConflictsOpen: %v", err)
	}
	if len(got.Challenges) != 1 {
		t.Fatalf("expected 1 in-flight challenge, got %d", len(got.Challenges))
	}
}

func TestScopeHotness_ScoresChallengeDensity(t *testing.T) {
	src := &stubSource{rows: []activity.AgentActivity{
		mkAct(activity.ActionChallengeEmitted, "s1", "tests/", "a"),
		mkAct(activity.ActionChallengeEmitted, "s1", "tests/", "b"),
		mkAct(activity.ActionChallengeEmitted, "s1", "tests/", "c"),
		mkAct(activity.ActionDecisionDeclared, "s1", "tests/", "d"),
	}}
	got, err := ScopeHotness(context.Background(), src, ScopeHotnessQuery{SessionID: "s1", Scope: "tests/"})
	if err != nil {
		t.Fatalf("ScopeHotness: %v", err)
	}
	if got.ChallengeCount != 3 {
		t.Fatalf("ChallengeCount = %d, want 3", got.ChallengeCount)
	}
	if got.ActivityCount != 4 {
		t.Fatalf("ActivityCount = %d, want 4", got.ActivityCount)
	}
}

func TestAmbientFor_SurfacesInbound(t *testing.T) {
	inbound := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  "s1",
		Timestamp:  time.Now(),
		Resolution: activity.ResolutionFor(activity.ActionChallengeEmitted),
		Action:     activity.ActionChallengeEmitted,
		Subject:    activity.Subject{TargetAgent: "me", PathPrefix: "tests/"},
		Actor:      activity.Actor{AgentID: "challenger-1"},
		State:      activity.StateInFlight,
	}
	src := &stubSource{rows: []activity.AgentActivity{inbound}}
	env, err := AmbientFor(context.Background(), src, AmbientQuery{
		SessionID: "s1",
		AgentID:   "me",
		Scope:     "tests/",
	})
	if err != nil {
		t.Fatalf("AmbientFor: %v", err)
	}
	if len(env.InboundDisputes) != 1 {
		t.Fatalf("expected 1 inbound dispute; got %d", len(env.InboundDisputes))
	}
	rendered := env.Render()
	if !strings.Contains(rendered, "challenger-1") {
		t.Errorf("rendered envelope should mention challenger; got %s", rendered)
	}
	if !strings.Contains(rendered, "<ambient_context>") {
		t.Errorf("rendered envelope should be wrapped in tags; got %s", rendered)
	}
}

func TestAmbientFor_EmptyEnvelopeRendersBlank(t *testing.T) {
	src := &stubSource{}
	env, err := AmbientFor(context.Background(), src, AmbientQuery{SessionID: "s1", AgentID: "me", Scope: "tests/"})
	if err != nil {
		t.Fatalf("AmbientFor: %v", err)
	}
	if env.Render() != "" {
		t.Errorf("empty envelope should render blank; got %s", env.Render())
	}
}

func TestAmbientFor_HotnessAdvisorySurfacesUnderContention(t *testing.T) {
	rows := make([]activity.AgentActivity, 0, 4)
	for i := 0; i < 4; i++ {
		rows = append(rows, mkAct(activity.ActionChallengeEmitted, "s1", "tests/", "agent"))
	}
	src := &stubSource{rows: rows}
	env, err := AmbientFor(context.Background(), src, AmbientQuery{
		SessionID: "s1",
		AgentID:   "me",
		Scope:     "tests/",
	})
	if err != nil {
		t.Fatalf("AmbientFor: %v", err)
	}
	if env.OverflowAdvisory == "" {
		t.Fatal("expected hotness advisory to surface under contention")
	}
}

func TestSessionTimeline_ReturnsChronological(t *testing.T) {
	a := mkAct(activity.ActionDecisionDeclared, "s1", "tests/", "a")
	b := mkAct(activity.ActionDecisionDeclared, "s1", "tests/", "b")
	src := &stubSource{rows: []activity.AgentActivity{a, b}}
	out, err := SessionTimeline(context.Background(), src, "s1", time.Time{}, 0)
	if err != nil {
		t.Fatalf("SessionTimeline: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 rows; got %d", len(out))
	}
	if !out[0].Timestamp.Before(out[1].Timestamp) && !out[0].Timestamp.Equal(out[1].Timestamp) {
		t.Errorf("timeline must be chronological")
	}
}

func TestSessionIngestionContext_ProvidesDigest(t *testing.T) {
	src := &stubSource{rows: []activity.AgentActivity{
		mkAct(activity.ActionDecisionDeclared, "s1", "tests/auth/", "a"),
	}}
	got, err := SessionIngestionContext(context.Background(), src, "s1", "tests/auth/")
	if err != nil {
		t.Fatalf("SessionIngestionContext: %v", err)
	}
	if len(got.RecentActivity) != 1 {
		t.Fatalf("expected recent activity; got %d", len(got.RecentActivity))
	}
}
