package shared

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/forest"
)

type testMemoryForestService struct {
	outcomes []forest.OutcomeRecord
}

func (s *testMemoryForestService) ResolveIntent(context.Context, forest.ResolveIntentInput) (*forest.IntentResolution, error) {
	return nil, nil
}

func (s *testMemoryForestService) RetrieveForest(context.Context, forest.Query) ([]*forest.ForestPacket, error) {
	return nil, nil
}

func (s *testMemoryForestService) CreateForestCursor(context.Context, forest.ForestCursorInput) (*forest.ForestCursor, error) {
	return nil, nil
}

func (s *testMemoryForestService) ProposeForestClaim(context.Context, forest.ForestClaimProposal) error {
	return nil
}

func (s *testMemoryForestService) RecordOutcome(_ context.Context, record forest.OutcomeRecord) error {
	s.outcomes = append(s.outcomes, record)
	return nil
}

func TestMemoryForestTrackerPrunesStaleAndExcessEntries(t *testing.T) {
	tracker := NewMemoryForestTracker()
	now := time.Now()
	for i := 0; i < forestTrackerMaxEntries+8; i++ {
		tracker.entries[fmt.Sprintf("stale|sess|%d", i)] = trackedForestNodes{
			NodeIDs:   []string{fmt.Sprintf("node-%d", i)},
			UpdatedAt: now.Add(-forestTrackerMaxAge - time.Minute),
		}
	}
	for i := 0; i < forestTrackerMaxEntries+8; i++ {
		tracker.entries[fmt.Sprintf("fresh|sess|%d", i)] = trackedForestNodes{
			NodeIDs:   []string{fmt.Sprintf("node-fresh-%d", i)},
			UpdatedAt: now.Add(-time.Duration(i) * time.Second),
		}
	}

	tracker.ObserveResult(context.Background(), forest.IntentResolution{
		IntentNodes: []*forest.ForestPacket{testForestPacket("node-new")},
	}, "sess")

	if got := len(tracker.entries); got > forestTrackerMaxEntries {
		t.Fatalf("tracker entries = %d, want <= %d", got, forestTrackerMaxEntries)
	}
	for key := range tracker.entries {
		if len(key) >= 5 && key[:5] == "stale" {
			t.Fatalf("expected stale tracker entry %q to be pruned", key)
		}
	}
}

func TestMemoryForestTrackerRecordOutcomeClearsTrackedEntry(t *testing.T) {
	tracker := NewMemoryForestTracker()
	ctx := WithLogMeta(context.Background(), LogMeta{
		CorrID:    "corr-123",
		AgentID:   "agent-1",
		SessionID: "sess-1",
	})
	tracker.ObserveResult(ctx, forest.IntentResolution{
		IntentNodes: []*forest.ForestPacket{testForestPacket("node-123")},
	}, "")

	svc := &testMemoryForestService{}
	if err := tracker.RecordOutcome(ctx, svc, "agent-1", "architect", "", forest.OutcomeStatusSucceeded, "done"); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	if len(svc.outcomes) != 1 {
		t.Fatalf("recorded outcomes = %d, want 1", len(svc.outcomes))
	}
	if got, _ := tracker.Snapshot(ctx, ""); len(got) != 0 {
		t.Fatalf("tracker snapshot after record = %#v, want cleared entry", got)
	}
}

func testForestPacket(id string) *forest.ForestPacket {
	return &forest.ForestPacket{
		Node: forest.ForestNode{ID: id, Kind: forest.ForestNodeClaim},
		Evidence: []forest.ForestEvidence{{
			RefType: "node",
			RefID:   id,
			NodeID:  id,
		}},
	}
}
