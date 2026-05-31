package forest

import (
	"context"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/activity/activitystore"
	"github.com/adalundhe/sylk/core/claims"
)

// ClaimsHarvester is now a Fabric context harvester. Claims lifecycle truth is
// ingested by DeltaIngestor from canonical claims deltas. This harvester keeps
// the dependency-inverted MountClaimsHarvest API alive for existing bootstrap
// wiring, but it records only traversal/context observations into
// forest_ledger.
type ClaimsHarvester struct {
	forest *MemoryForest
}

// NewClaimsHarvester creates a harvester bound to the given forest.
func NewClaimsHarvester(forest *MemoryForest) *ClaimsHarvester {
	return &ClaimsHarvester{forest: forest}
}

// Harvest is invoked by the activitystore HarvestDispatcher on each elected
// ForestCandidate. It does not infer claim, testament, artifact, or validation
// lifecycle state from activity payloads.
func (h *ClaimsHarvester) Harvest(ctx context.Context, candidate activitystore.ForestCandidate) error {
	if h == nil || h.forest == nil {
		return nil
	}
	record := ledgerRecordFromFabricCandidate(candidate)
	if _, err := h.forest.AppendLedgerRecord(ctx, record); err != nil {
		return fmt.Errorf("append fabric context ledger record: %w", err)
	}
	return nil
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
		},
	}
}

// Ensure ClaimsHarvester.Harvest satisfies the HarvestFunc signature.
var _ activitystore.HarvestFunc = (*ClaimsHarvester)(nil).Harvest

// HarvestRegistrar registers an async-wrapped harvest function with
// the orchestrator's fabric. This is dependency-inverted from the
// MountClaimsHarvest helper so this package doesn't take a hard
// dependency on the orchestrator package — the bootstrap that owns
// both the forest and the orchestrator passes orchestrator.SetForestHarvester
// (or equivalent) as the registrar.
type HarvestRegistrar func(activitystore.HarvestFunc)

// MountClaimsHarvest constructs a ClaimsHarvester bound to this
// forest, wraps it in an async HarvestDispatcher with the supplied
// config, and registers the dispatcher's Harvest method via
// `register`. Returns the dispatcher so the caller can Stop() it on
// shutdown and inspect counters for observability.
//
// Wire-once: subsequent calls replace any previously registered
// harvester (orchestrator.SetForestHarvester has single-pointer
// semantics). For multi-forest scenarios where multiple forests
// should each receive claims-board precedent, switch the registrar
// to a fan-out implementation rather than calling MountClaimsHarvest
// per forest.
//
// Pass HarvestDispatcherConfig{} for the derived defaults (NumCPU
// workers, 8×workers queue, 4×queue dedupe window, 5s work timeout).
// Pass cfg.ErrorSink to surface harvest errors as kind=error_harvest_*
// artifacts on in-flight testaments per CLAIMS.md §5.11.
func (m *MemoryForest) MountClaimsHarvest(
	register HarvestRegistrar,
	cfg activitystore.HarvestDispatcherConfig,
) *activitystore.HarvestDispatcher {
	if m == nil || register == nil {
		return nil
	}
	harvester := NewClaimsHarvester(m)
	dispatcher := activitystore.NewHarvestDispatcher(harvester.Harvest, cfg)
	register(dispatcher.Harvest)
	return dispatcher
}
