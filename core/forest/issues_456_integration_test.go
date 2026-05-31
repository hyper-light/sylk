package forest

import (
	"context"
	"testing"
	"time"
)

// TestIssues456_EndToEndRetrieveFlow exercises the full Retrieve
// pipeline with cooldown, MMR diversity, global-priors fallback, and
// anti-precedent quota all active. Asserts the output's Source tags
// reflect the channel each packet came through.
func TestIssues456_EndToEndRetrieveFlow(t *testing.T) {
	forest, db := newTestForestWithConfig(t, Config{
		DiversityLambda:          0.7,
		RetrievalCooldownWindow:  8,
		RetrievalCooldownPenalty: 0.3,
	})
	ctx := context.Background()

	// Seed primary branches in session "active".
	for _, title := range []string{"primary-a", "primary-b"} {
		event := &Event{
			SessionID: "active",
			BranchID:  title,
			AgentID:   "engineer", AgentType: "engineer",
			EventType: EventTypeDecisionRecorded,
			Family:    TreeFamilyDecision,
			Title:     "Active " + title,
		}
		if err := forest.AppendEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	// Seed an AntiPattern + matching anti-precedent source so the
	// quota fires.
	now := time.Now().UTC()
	if _, err := db.Exec(`
		INSERT INTO forest_branches
		(id, root_id, parent_id, family, scope, state, session_id, task_id, agent_id, agent_type,
		 intent_id, title, summary, confidence, salience, utility, success_rate, scope_risk,
		 conflict_score, support_count, counter_count, success_count, failure_count,
		 access_count, last_accessed_at, created_at, updated_at, metadata, last_applied_seq)
		VALUES ('failed-src', 'failed-src-root', '', ?, ?, ?, '', '', '', 'engineer', '', 'failed approach', '', 0.5, 0.5, 0, 0, 0, 0, 1, 0, 1, 5, 0, ?, ?, ?, '{}', 0)
	`, string(TreeFamilyOutcome), string(ScopeSemantic), string(BranchStateActive),
		now.Unix(), now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}

	packets, err := forest.Retrieve(ctx, Query{
		SessionID: "active",
		AgentType: "engineer",
		Query:     "Active primary",
		Limit:     4,
		Families:  []TreeFamily{TreeFamilyDecision},
	})
	if err != nil {
		t.Fatal(err)
	}

	primary := 0
	for _, p := range packets {
		switch p.Source {
		case RetrievalSourcePrimary:
			primary++
		default:
			t.Errorf("unexpected source: %q", p.Source)
		}
	}
	if primary == 0 {
		t.Errorf("expected at least 1 primary packet; got 0")
	}
	t.Logf("packet sources: primary=%d total=%d", primary, len(packets))
}

// TestIssues456_ColdStartFallsThroughToGlobalPriors covers the case
// where the primary stage returns zero branches (fresh session) and
// global priors backfill from across the forest.
func TestIssues456_ColdStartFallsThroughToGlobalPriors(t *testing.T) {
	t.Skip("legacy branch global-prior fallback removed from public Retrieve")
	forest, _ := newTestForest(t)
	ctx := context.Background()

	// Seed two branches in session "old" — these become priors.
	for _, title := range []string{"prior-a", "prior-b"} {
		event := &Event{
			SessionID: "old",
			BranchID:  title,
			AgentID:   "engineer", AgentType: "engineer",
			EventType: EventTypeDecisionRecorded,
			Family:    TreeFamilyDecision,
			Title:     "Prior " + title,
		}
		if err := forest.AppendEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	// Retrieve from a fresh session "fresh" with the same family but
	// a query that matches nothing semantically.
	packets, err := forest.Retrieve(ctx, Query{
		SessionID: "fresh",
		AgentType: "engineer",
		Query:     "asdfqwerty no match",
		Limit:     2,
		Families:  []TreeFamily{TreeFamilyDecision},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(packets) == 0 {
		t.Fatalf("expected fallback packets; got 0")
	}
	hasPrimary := false
	for _, p := range packets {
		if p.Source == RetrievalSourcePrimary {
			hasPrimary = true
		}
	}
	if !hasPrimary {
		t.Errorf("expected at least one primary packet; got sources %v", packetSources(packets))
	}
}

func packetSources(pkts []*ForestPacket) []RetrievalSource {
	out := make([]RetrievalSource, 0, len(pkts))
	for _, p := range pkts {
		out = append(out, p.Source)
	}
	return out
}
