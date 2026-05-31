package fabriclog

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/activity"
)

// ForestHarvestFunc matches activitystore.HarvestFunc. The package
// avoids importing core/activity/activitystore to prevent a cycle —
// the bridge only needs the function signature, not the package's
// types.
type ForestHarvestFunc func(ctx context.Context, activity activity.AgentActivity, reason string) error

// ForestFabricBridge turns fabric observability records into forest
// harvest candidates. It subscribes to the FabricLogger's stream and
// reacts to consume and resolve records by looking up the original
// activity in the logger's recent-publish cache and forwarding it to
// the forest with a reason string that captures the interaction
// pattern.
//
// The bridge deliberately emits the ORIGINAL consumed or resolved
// activity (not a synthesized "consumption" activity) so the
// forest's existing dedup / salience logic handles reinforcement:
// when the same activity is harvested multiple times with different
// reasons, the forest treats each one as corroborating precedent
// evidence. This avoids introducing a new ActionKind just for
// observability-driven harvests.
//
// Only the activities that pass ForestSubscriber election (the
// allowlist in core/activity/activitystore/forest_subscriber.go) are
// forwarded — the bridge applies an identical election before
// forwarding so we don't flood the forest with low-signal harvests.
type ForestFabricBridge struct {
	logger  *FabricLogger
	harvest ForestHarvestFunc
	scope   context.Context

	consumesForwarded atomic.Uint64
	resolvesForwarded atomic.Uint64
	cacheMisses       atomic.Uint64
	notEligible       atomic.Uint64
}

// NewForestFabricBridge creates a bridge that subscribes to logger
// and forwards consume/resolve records that reference cached
// activities whose ActionKind is forest-eligible. Call Subscribe on
// the returned bridge to wire it up.
func NewForestFabricBridge(logger *FabricLogger, harvest ForestHarvestFunc) *ForestFabricBridge {
	return NewForestFabricBridgeWithContext(context.TODO(), logger, harvest)
}

func NewForestFabricBridgeWithContext(ctx context.Context, logger *FabricLogger, harvest ForestHarvestFunc) *ForestFabricBridge {
	if ctx == nil {
		ctx = context.TODO()
	}
	return &ForestFabricBridge{
		logger:  logger,
		harvest: harvest,
		scope:   ctx,
	}
}

// Wire registers the bridge as a subscriber on its logger. Separate
// from the constructor so callers can run tests against a bridge
// without activating it.
func (b *ForestFabricBridge) Wire() {
	if b == nil || b.logger == nil {
		return
	}
	b.logger.Subscribe(b)
}

// OnRecord implements Subscriber.
func (b *ForestFabricBridge) OnRecord(record FabricLogRecord) {
	if b == nil || b.harvest == nil {
		return
	}
	switch record.Kind {
	case KindConsume:
		b.handleConsume(record)
	case KindResolve:
		b.handleResolve(record)
	}
}

// Stats returns a snapshot of bridge counters for diagnostics.
type BridgeStats struct {
	ConsumesForwarded uint64 `json:"consumes_forwarded"`
	ResolvesForwarded uint64 `json:"resolves_forwarded"`
	CacheMisses       uint64 `json:"cache_misses"`
	NotEligible       uint64 `json:"not_eligible"`
}

// Stats returns the current counter snapshot.
func (b *ForestFabricBridge) Stats() BridgeStats {
	if b == nil {
		return BridgeStats{}
	}
	return BridgeStats{
		ConsumesForwarded: b.consumesForwarded.Load(),
		ResolvesForwarded: b.resolvesForwarded.Load(),
		CacheMisses:       b.cacheMisses.Load(),
		NotEligible:       b.notEligible.Load(),
	}
}

func (b *ForestFabricBridge) handleConsume(record FabricLogRecord) {
	if record.Consume == nil || record.Consume.ActivityID == "" {
		return
	}
	orig, ok := b.logger.LookupRecent(record.Consume.ActivityID)
	if !ok {
		b.cacheMisses.Add(1)
		return
	}
	if !forestEligibleActionKind(orig.Action) {
		b.notEligible.Add(1)
		return
	}
	reader := record.Agent
	reason := fmt.Sprintf(
		"consumed by %s (%s) %s after publish via %s",
		readerLabel(reader),
		readerTypeLabel(reader),
		latencyHuman(record.Consume.LatencyMicros),
		lensLabelFromRecord(record),
	)
	ctx, cancel := context.WithTimeout(b.scope, 5*time.Second)
	defer cancel()
	_ = b.harvest(ctx, orig, reason)
	b.consumesForwarded.Add(1)
}

func (b *ForestFabricBridge) handleResolve(record FabricLogRecord) {
	if record.Resolve == nil || record.Resolve.ResolvedActivityID == "" {
		return
	}
	orig, ok := b.logger.LookupRecent(record.Resolve.ResolvedActivityID)
	if !ok {
		b.cacheMisses.Add(1)
		return
	}
	if !forestEligibleActionKind(orig.Action) {
		b.notEligible.Add(1)
		return
	}
	reason := fmt.Sprintf(
		"lifecycle resolved by %s kind=%s %s after emit",
		record.Resolve.ResolverActivityID,
		record.Resolve.ResolverKind,
		latencyHuman(record.Resolve.LatencyMicros),
	)
	ctx, cancel := context.WithTimeout(b.scope, 5*time.Second)
	defer cancel()
	_ = b.harvest(ctx, orig, reason)
	b.resolvesForwarded.Add(1)
}

// forestEligibleActionKind is an inlined, dependency-free mirror of
// the ForestSubscriber.electCandidate allowlist. Duplicating the
// list here avoids importing activitystore (which would create a
// fabriclog → activitystore → activity cycle). The two copies are
// intentionally kept in lockstep; a test in forest_bridge_test.go
// could pin the invariant if we ever feel the drift risk is real.
func forestEligibleActionKind(kind activity.ActionKind) bool {
	switch kind {
	case activity.ActionPrecedentEmitted,
		activity.ActionDecisionPromoted,
		activity.ActionCharterRatified,
		activity.ActionConsultResponse,
		activity.ActionChallengeResponse,
		activity.ActionRemediationResolved,
		activity.ActionPlanRatified,
		activity.ActionDecisionDeclared,
		activity.ActionAdvisoryEmitted,
		activity.ActionProactiveAdvisory,
		activity.ActionNarrationEmitted,
		activity.ActionToolCallCompleted,
		activity.ActionLLMResponseCompleted,
		activity.ActionForestConsultEmitted,
		activity.ActionTraversalObserved:
		return true
	}
	return false
}

func readerLabel(a AgentRef) string {
	if a.AgentID == "" {
		return "(unknown)"
	}
	return a.AgentID
}
func readerTypeLabel(a AgentRef) string {
	if a.AgentType == "" {
		return "?"
	}
	return a.AgentType
}

func lensLabelFromRecord(record FabricLogRecord) string {
	if record.Read != nil {
		if record.Read.LensAlias != "" {
			return record.Read.LensAlias
		}
		return record.Read.Lens
	}
	return "(ambient)"
}

func latencyHuman(micros int64) string {
	if micros <= 0 {
		return "immediately"
	}
	const (
		msInMicros = 1_000
		sInMicros  = 1_000_000
	)
	switch {
	case micros < msInMicros:
		return fmt.Sprintf("%dµs", micros)
	case micros < sInMicros:
		return fmt.Sprintf("%dms", micros/msInMicros)
	default:
		return fmt.Sprintf("%.1fs", float64(micros)/float64(sInMicros))
	}
}
