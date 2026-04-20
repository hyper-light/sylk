package fabric

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/activity"
)

type ambientStubSource struct {
	rows []activity.AgentActivity
}

func (s *ambientStubSource) FilterActivities(_ context.Context, f activity.QueryFilter) ([]activity.AgentActivity, error) {
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
		if f.SubjectTargetAgent != "" && r.Subject.TargetAgent != f.SubjectTargetAgent {
			continue
		}
		if f.ActorAgentID != "" && r.Actor.AgentID != f.ActorAgentID {
			continue
		}
		out = append(out, r)
		if f.Limit > 0 && len(out) >= f.Limit {
			break
		}
	}
	return out, nil
}

func (s *ambientStubSource) GetActivity(_ context.Context, id activity.ActivityID) (*activity.AgentActivity, error) {
	for i := range s.rows {
		if s.rows[i].ID == id {
			return &s.rows[i], nil
		}
	}
	return nil, nil
}

func (s *ambientStubSource) LatestActivityID(_ context.Context, _ activity.QueryFilter) (activity.ActivityID, error) {
	if len(s.rows) == 0 {
		return "", nil
	}
	return s.rows[len(s.rows)-1].ID, nil
}

func TestAppendAmbientContext_NoOpWhenSourceUnavailable(t *testing.T) {
	ResetAmbientRateLimitForTesting()
	prev := activity.SetDefaultSource(nil)
	defer activity.SetDefaultSource(prev)

	got := AppendAmbientContext(context.Background(), AmbientEnvelopeConfig{
		SessionID: func() string { return "sess-1" },
		AgentID:   func() string { return "agent-1" },
		AgentType: func() string { return "engineer" },
	}, "tool result")
	if got != "tool result" {
		t.Fatalf("expected unchanged result; got %s", got)
	}
}

func TestAppendAmbientContext_AppendsEnvelopeWithInbound(t *testing.T) {
	ResetAmbientRateLimitForTesting()
	src := &ambientStubSource{rows: []activity.AgentActivity{
		{
			ID:         activity.NewActivityID(),
			SessionID:  "sess-1",
			Timestamp:  time.Now(),
			Resolution: activity.ResolutionCoarse,
			Action:     activity.ActionChallengeEmitted,
			State:      activity.StateInFlight,
			Subject:    activity.Subject{TargetAgent: "agent-1", PathPrefix: "tests/"},
			Actor:      activity.Actor{AgentID: "challenger-1", AgentType: "tester-pipeline"},
		},
	}}
	prev := activity.SetDefaultSource(src)
	defer activity.SetDefaultSource(prev)

	got := AppendAmbientContext(context.Background(), AmbientEnvelopeConfig{
		SessionID: func() string { return "sess-1" },
		AgentID:   func() string { return "agent-1" },
		AgentType: func() string { return "tester-pipeline" },
		ScopeHint: func() string { return "tests/" },
	}, "tool result")
	if !strings.Contains(got, "<ambient_context>") {
		t.Fatalf("expected envelope wrapper; got %s", got)
	}
	if !strings.Contains(got, "challenger-1") {
		t.Fatalf("expected challenger surfaced; got %s", got)
	}
	if !strings.HasPrefix(got, "tool result") {
		t.Fatalf("envelope must be appended after the result, not replace it; got %s", got)
	}
}

func TestAppendAmbientContext_RateLimitsRepeatedCalls(t *testing.T) {
	ResetAmbientRateLimitForTesting()
	src := &ambientStubSource{rows: []activity.AgentActivity{
		{
			ID:         activity.NewActivityID(),
			SessionID:  "sess-1",
			Timestamp:  time.Now(),
			Resolution: activity.ResolutionCoarse,
			Action:     activity.ActionChallengeEmitted,
			State:      activity.StateInFlight,
			Subject:    activity.Subject{TargetAgent: "agent-rl"},
			Actor:      activity.Actor{AgentID: "challenger-x"},
		},
	}}
	prev := activity.SetDefaultSource(src)
	defer activity.SetDefaultSource(prev)

	cfg := AmbientEnvelopeConfig{
		SessionID: func() string { return "sess-1" },
		AgentID:   func() string { return "agent-rl" },
		AgentType: func() string { return "tester-pipeline" },
	}
	first := AppendAmbientContext(context.Background(), cfg, "r1")
	second := AppendAmbientContext(context.Background(), cfg, "r2")
	if !strings.Contains(first, "<ambient_context>") {
		t.Fatal("first call should compute envelope")
	}
	if strings.Contains(second, "<ambient_context>") {
		t.Fatal("second call within rate window must skip envelope computation")
	}
}

func TestAppendAmbientContext_RateLimitIsPerAgent(t *testing.T) {
	ResetAmbientRateLimitForTesting()
	src := &ambientStubSource{rows: []activity.AgentActivity{
		{
			SessionID:  "sess-1",
			Timestamp:  time.Now(),
			Resolution: activity.ResolutionCoarse,
			Action:     activity.ActionChallengeEmitted,
			State:      activity.StateInFlight,
			Subject:    activity.Subject{TargetAgent: "agent-a"},
			Actor:      activity.Actor{AgentID: "challenger-a"},
		},
		{
			SessionID:  "sess-1",
			Timestamp:  time.Now(),
			Resolution: activity.ResolutionCoarse,
			Action:     activity.ActionChallengeEmitted,
			State:      activity.StateInFlight,
			Subject:    activity.Subject{TargetAgent: "agent-b"},
			Actor:      activity.Actor{AgentID: "challenger-b"},
		},
	}}
	prev := activity.SetDefaultSource(src)
	defer activity.SetDefaultSource(prev)

	a := AppendAmbientContext(context.Background(), AmbientEnvelopeConfig{
		SessionID: func() string { return "sess-1" },
		AgentID:   func() string { return "agent-a" },
		AgentType: func() string { return "engineer" },
	}, "r")
	b := AppendAmbientContext(context.Background(), AmbientEnvelopeConfig{
		SessionID: func() string { return "sess-1" },
		AgentID:   func() string { return "agent-b" },
		AgentType: func() string { return "engineer" },
	}, "r")
	if !strings.Contains(a, "<ambient_context>") {
		t.Fatal("agent-a should compute its envelope on first call")
	}
	if !strings.Contains(b, "<ambient_context>") {
		t.Fatal("agent-b should compute its envelope on first call (separate rate-limit bucket from agent-a)")
	}
}

func TestAppendAmbientContext_NoEnvelopeWhenEmpty(t *testing.T) {
	ResetAmbientRateLimitForTesting()
	src := &ambientStubSource{rows: nil}
	prev := activity.SetDefaultSource(src)
	defer activity.SetDefaultSource(prev)

	got := AppendAmbientContext(context.Background(), AmbientEnvelopeConfig{
		SessionID: func() string { return "sess-1" },
		AgentID:   func() string { return "agent-empty" },
		AgentType: func() string { return "engineer" },
	}, "tool result")
	if got != "tool result" {
		t.Fatalf("empty envelope should not modify result; got %s", got)
	}
}
