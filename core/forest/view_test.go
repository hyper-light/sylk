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
	intentTree := viewTreeByFamily(snapshot.Trees, TreeFamilyIntent)
	if intentTree == nil {
		t.Fatalf("missing intent tree in snapshot: %+v", snapshot.Trees)
	}
	outcomeTree := viewTreeByFamily(snapshot.Trees, TreeFamilyOutcome)
	if outcomeTree == nil {
		t.Fatalf("missing outcome tree in snapshot: %+v", snapshot.Trees)
	}
	if !viewTreeContainsTitle(intentTree, "Baseline") || !viewTreeContainsTitle(intentTree, "Timeout Cap") {
		t.Fatalf("intent tree missing projected event nodes: %+v", intentTree.Nodes)
	}
	if !viewTreeContainsTitle(outcomeTree, "Pass") {
		t.Fatalf("outcome tree missing projected event node: %+v", outcomeTree.Nodes)
	}
	if snapshot.SelectedBranchID == "" {
		t.Fatal("selected node id is empty")
	}
}

func viewTreeByFamily(trees []ViewTree, family TreeFamily) *ViewTree {
	for i := range trees {
		if trees[i].Family == family {
			return &trees[i]
		}
	}
	return nil
}

func viewTreeContainsTitle(tree *ViewTree, title string) bool {
	if tree == nil {
		return false
	}
	for _, node := range tree.Nodes {
		if node.Title == title {
			return true
		}
	}
	return false
}
