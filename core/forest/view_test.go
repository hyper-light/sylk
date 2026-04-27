package forest

import (
	"context"
	"testing"
	"time"
)

func TestMemoryForestSnapshotGroupsTreesAndSelection(t *testing.T) {
	t.Parallel()

	forest, db := newTestForest(t)
	defer forest.Close()
	defer db.Close()

	now := time.Now().UTC()
	events := []*Event{
		{
			SessionID:      "session-1",
			EventType:      EventTypeDecisionRecorded,
			Family:         TreeFamilyIntent,
			RootID:         "decision-root",
			BranchID:       "decision-1",
			Confidence:     0.8,
			Salience:       0.7,
			Timestamp:      now,
			Title:          "Baseline",
			Summary:        "Baseline retry policy",
			ParentBranchID: "",
		},
		{
			SessionID:      "session-1",
			EventType:      EventTypeDecisionRecorded,
			Family:         TreeFamilyIntent,
			RootID:         "decision-root",
			BranchID:       "decision-2",
			ParentBranchID: "decision-1",
			Confidence:     0.9,
			Salience:       0.8,
			Timestamp:      now.Add(time.Minute),
			Title:          "Timeout Cap",
			Summary:        "Cap request timeout to avoid runaway retries",
		},
		{
			SessionID:      "session-1",
			EventType:      EventTypeOutcomeRecorded,
			Family:         TreeFamilyOutcome,
			RootID:         "outcome-root",
			BranchID:       "outcome-1",
			Confidence:     0.88,
			Salience:       0.92,
			Timestamp:      now.Add(2 * time.Minute),
			Title:          "Pass",
			Summary:        "Validation passed",
			ParentBranchID: "",
		},
	}
	for _, event := range events {
		if err := forest.AppendEvent(context.Background(), event); err != nil {
			t.Fatalf("append event %s: %v", event.BranchID, err)
		}
	}

	snapshot, err := forest.Snapshot(context.Background(), ViewSnapshotInput{
		SessionID: "session-1",
		Horizon:   CanopyHorizonSession,
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snapshot.Trees) != 2 {
		t.Fatalf("tree count = %d, want 2", len(snapshot.Trees))
	}
	if snapshot.Trees[0].Label != "Intent Tree" {
		t.Fatalf("tree[0].label = %q, want %q", snapshot.Trees[0].Label, "Intent Tree")
	}
	if snapshot.Trees[1].Label != "Outcome Tree" {
		t.Fatalf("tree[1].label = %q, want %q", snapshot.Trees[1].Label, "Outcome Tree")
	}
	if got := len(snapshot.Trees[0].Nodes); got != 2 {
		t.Fatalf("intent node count = %d, want 2", got)
	}
	if snapshot.Trees[0].Nodes[0].ID != "decision-1" || snapshot.Trees[0].Nodes[1].ID != "decision-2" {
		t.Fatalf("intent nodes order = [%s %s], want [decision-1 decision-2]", snapshot.Trees[0].Nodes[0].ID, snapshot.Trees[0].Nodes[1].ID)
	}
	if !snapshot.Trees[0].Nodes[1].Leaf {
		t.Fatal("expected newest intent node to be a leaf")
	}
	if snapshot.SelectedBranchID != "outcome-1" {
		t.Fatalf("selected branch = %q, want %q", snapshot.SelectedBranchID, "outcome-1")
	}
}
