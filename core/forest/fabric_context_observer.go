package forest

import (
	"context"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/activity/activitystore"
	"github.com/adalundhe/sylk/core/claims"
)

// FabricContextObserver records fabric traversal and context observations only.
// Claim, testament, artifact, and validation lifecycle truth enters the forest
// through canonical claims deltas.
type FabricContextObserver struct {
	forest *MemoryForest
}

func NewFabricContextObserver(forest *MemoryForest) *FabricContextObserver {
	return &FabricContextObserver{forest: forest}
}

func (o *FabricContextObserver) Observe(ctx context.Context, candidate activitystore.ForestCandidate) error {
	if o == nil || o.forest == nil {
		return nil
	}
	record := ledgerRecordFromFabricCandidate(candidate)
	if _, err := o.forest.AppendLedgerRecord(ctx, record); err != nil {
		return fmt.Errorf("append fabric context ledger record: %w", err)
	}
	return nil
}

var _ activitystore.HarvestFunc = (*FabricContextObserver)(nil).Observe

type FabricObservationRegistrar func(activitystore.HarvestFunc)

func (m *MemoryForest) MountFabricContextObserver(
	register FabricObservationRegistrar,
	cfg activitystore.HarvestDispatcherConfig,
) *activitystore.HarvestDispatcher {
	if m == nil || register == nil {
		return nil
	}
	observer := NewFabricContextObserver(m)
	dispatcher := activitystore.NewHarvestDispatcher(observer.Observe, cfg)
	register(dispatcher.Harvest)
	return dispatcher
}

func ledgerRecordFromFabricCandidate(candidate activitystore.ForestCandidate) LedgerRecord {
	a := candidate.Activity
	eventKind := "fabric.context_observed"
	subjectType := "activity"
	if a.Action == activity.ActionTraversalObserved {
		eventKind = "traversal.observed"
		subjectType = "traversal"
	}
	sourceID := string(a.ID)
	if sourceID == "" {
		sourceID = stableID("fabric_activity", string(a.SessionID), string(a.Action), a.Timestamp.UTC().Format(time.RFC3339Nano), candidate.Reason)
	}
	return LedgerRecord{
		ID:          "ledger_" + stableID("fabric", sourceID, eventKind),
		SourceKind:  LedgerSourceFabricContext,
		SourceID:    sourceID,
		SourceKey:   "fabric:" + sourceID + ":" + eventKind,
		EventKind:   eventKind,
		SessionID:   firstNonEmptyString(string(a.SessionID), "global"),
		SubjectType: subjectType,
		SubjectID:   sourceID,
		Actor:       claims.DegradedAgentRef(firstNonEmptyString(a.Actor.AgentType, a.Actor.AgentID, "unknown"), "fabric activity actor"),
		Reason:      candidate.Reason,
		OccurredAt:  a.Timestamp,
		Payload: map[string]any{
			"activity": a,
			"reason":   candidate.Reason,
			"state":    string(a.State),
		},
		Refs: fabricCandidateRefs(a),
	}
}

func fabricCandidateRefs(a activity.AgentActivity) []claims.DeltaRef {
	refs := make([]claims.DeltaRef, 0, len(a.Evidence)+fabricCandidateFixedRefCapacity())
	if a.SourceTable != "" && a.SourceID != "" {
		refs = append(refs, claims.DeltaRef{Role: "source", Type: a.SourceTable, ID: a.SourceID})
	}
	if a.Caused != nil && *a.Caused != "" {
		refs = append(refs, claims.DeltaRef{Role: "caused_by", Type: "activity", ID: string(*a.Caused)})
	}
	if a.Resolves != nil && *a.Resolves != "" {
		refs = append(refs, claims.DeltaRef{Role: "resolves", Type: "activity", ID: string(*a.Resolves)})
	}
	if a.Subject.TargetArtifact != "" {
		refs = append(refs, claims.DeltaRef{Role: "target", Type: claims.RelatedTypeArtifact, ID: a.Subject.TargetArtifact})
	}
	for _, evidence := range a.Evidence {
		if evidence.Kind == "" || evidence.Ref == "" {
			continue
		}
		refs = append(refs, claims.DeltaRef{Role: "evidence", Type: string(evidence.Kind), ID: evidence.Ref})
	}
	return refs
}

func fabricCandidateFixedRefCapacity() int {
	return len([]string{"source", "caused_by", "resolves", claims.RelatedTypeArtifact})
}
