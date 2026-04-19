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

func (s *testMemoryForestService) Retrieve(context.Context, forest.Query) ([]*forest.BranchPacket, error) {
	return nil, nil
}

func (s *testMemoryForestService) PredictNextBranches(context.Context, forest.Query) ([]*forest.BranchPacket, error) {
	return nil, nil
}

func (s *testMemoryForestService) RecordOutcome(_ context.Context, record forest.OutcomeRecord) error {
	s.outcomes = append(s.outcomes, record)
	return nil
}

// Project* stubs — MEM-01 widened MemoryForestService with family
// projections; preserve zero-behavior here since this stub only
// exercises RecordOutcome paths.
func (s *testMemoryForestService) ProjectIntent(context.Context, forest.ProjectionInput) (*forest.IntentProjection, error) {
	return nil, nil
}
func (s *testMemoryForestService) ProjectConstraints(context.Context, forest.ProjectionInput) (*forest.ConstraintProjection, error) {
	return nil, nil
}
func (s *testMemoryForestService) ProjectEvidence(context.Context, forest.ProjectionInput) (*forest.EvidenceProjection, error) {
	return nil, nil
}
func (s *testMemoryForestService) ProjectDecisions(context.Context, forest.ProjectionInput) (*forest.DecisionProjection, error) {
	return nil, nil
}
func (s *testMemoryForestService) ProjectOutcomes(context.Context, forest.ProjectionInput) (*forest.OutcomeProjection, error) {
	return nil, nil
}
func (s *testMemoryForestService) ProjectPreferences(context.Context, forest.ProjectionInput) (*forest.PreferenceProjection, error) {
	return nil, nil
}
func (s *testMemoryForestService) ProjectCapabilities(context.Context, forest.ProjectionInput) (*forest.CapabilityProjection, error) {
	return nil, nil
}
func (s *testMemoryForestService) ProjectOpportunities(context.Context, forest.ProjectionInput) (*forest.OpportunityProjection, error) {
	return nil, nil
}

func TestMemoryForestTrackerPrunesStaleAndExcessEntries(t *testing.T) {
	tracker := NewMemoryForestTracker()
	now := time.Now()
	for i := 0; i < forestTrackerMaxEntries+8; i++ {
		tracker.entries[fmt.Sprintf("stale|sess|%d", i)] = trackedForestBranches{
			BranchIDs: []string{fmt.Sprintf("branch-%d", i)},
			UpdatedAt: now.Add(-forestTrackerMaxAge - time.Minute),
		}
	}
	for i := 0; i < forestTrackerMaxEntries+8; i++ {
		tracker.entries[fmt.Sprintf("fresh|sess|%d", i)] = trackedForestBranches{
			BranchIDs: []string{fmt.Sprintf("branch-fresh-%d", i)},
			UpdatedAt: now.Add(-time.Duration(i) * time.Second),
		}
	}

	tracker.ObserveResult(context.Background(), forest.IntentResolution{
		IntentBranches: []forest.BranchPacket{{Branch: &forest.Branch{ID: "branch-new"}}},
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
		IntentBranches: []forest.BranchPacket{{Branch: &forest.Branch{ID: "branch-123"}}},
	}, "")

	svc := &testMemoryForestService{}
	if err := tracker.RecordOutcome(ctx, svc, "agent-1", "architect", "", forest.OutcomeStatusSucceeded, "done"); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	if len(svc.outcomes) != 1 {
		t.Fatalf("recorded outcomes = %d, want 1", len(svc.outcomes))
	}
	if got := tracker.Snapshot(ctx, ""); len(got) != 0 {
		t.Fatalf("tracker snapshot after record = %#v, want cleared entry", got)
	}
}
