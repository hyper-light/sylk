package forest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/claims"
)

const defaultDeltaIngestQueueCapacity = projectorBatchSize * 8

// DeltaIngestor is the claims-native replacement for activity-payload claims
// harvesting. It subscribes to canonical claims delta topics, deduplicates
// through forest_ledger.source_key, and projects artifact/validation evidence.
type DeltaIngestor struct {
	forest        *MemoryForest
	subscriber    claims.DeltaSubscriber
	sessionFilter string
	queue         chan claims.CanonicalDelta
	subscriptions []claims.DeltaSubscription
	stopped       atomic.Bool
	received      atomic.Uint64
	enqueued      atomic.Uint64
	overflowed    atomic.Uint64
	ingested      atomic.Uint64
	ignoredLegacy atomic.Uint64
	mu            sync.Mutex
	lastError     string
}

type DeltaIngestorSnapshot struct {
	Received      uint64
	Enqueued      uint64
	Overflowed    uint64
	Ingested      uint64
	IgnoredLegacy uint64
	LastError     string
}

func resolveDeltaIngestQueueCapacity(capacity int) int {
	if capacity > 0 {
		return capacity
	}
	return defaultDeltaIngestQueueCapacity
}

func (m *MemoryForest) MountClaimsDeltaIngestion(subscriber claims.DeltaSubscriber, sessionFilter string, capacity int) (*DeltaIngestor, error) {
	if m == nil {
		return nil, errors.New("forest is required")
	}
	if subscriber == nil {
		return nil, errors.New("claims delta subscriber is required")
	}
	if capacity <= 0 {
		return nil, fmt.Errorf("claims delta ingest queue capacity must be positive")
	}
	ingestor := &DeltaIngestor{
		forest:        m,
		subscriber:    subscriber,
		sessionFilter: strings.TrimSpace(sessionFilter),
		queue:         make(chan claims.CanonicalDelta, capacity),
	}
	for _, pattern := range canonicalDeltaIngestPatterns(firstNonEmptyString(ingestor.sessionFilter, claims.TopicWildcard)) {
		sub, err := subscriber.SubscribeDelta(pattern, ingestor.handleDelta)
		if err != nil {
			ingestor.stopSubscription()
			return nil, fmt.Errorf("subscribe claims canonical delta pattern %q: %w", pattern, err)
		}
		ingestor.subscriptions = append(ingestor.subscriptions, sub)
	}
	m.deltaIngestor = ingestor
	m.registerRuntimeQueue("claims_delta_ingestor", cap(ingestor.queue))
	m.startWorker("claims_delta_ingestor", capacity, func(ctx context.Context) error {
		return ingestor.run(ctx)
	})
	return ingestor, nil
}

func (i *DeltaIngestor) handleDelta(delta claims.Delta) {
	if i == nil || i.stopped.Load() {
		return
	}
	i.received.Add(1)
	canonical, ok := canonicalDeltaFromBusDelta(delta)
	if !ok {
		i.ignoredLegacy.Add(1)
		i.recordIgnoredLegacy(delta)
		return
	}
	select {
	case i.queue <- canonical:
		i.enqueued.Add(1)
	default:
		i.overflowed.Add(1)
		i.forest.recordRuntimeQueueOverflow("claims_delta_ingestor", "claims delta ingest queue full")
		i.recordOverflow(canonical)
	}
}

func canonicalDeltaFromBusDelta(delta claims.Delta) (claims.CanonicalDelta, bool) {
	switch typed := delta.(type) {
	case claims.CanonicalDelta:
		return typed, true
	case *claims.CanonicalDelta:
		if typed == nil {
			return claims.CanonicalDelta{}, false
		}
		return *typed, true
	default:
		return claims.CanonicalDelta{}, false
	}
}

func canonicalDeltaIngestPatterns(sessionFilter string) []string {
	actions := claims.KnownDeltaActions()
	patterns := make([]string, 0, len(actions)*5)
	seen := make(map[string]struct{}, len(actions)*5)
	add := func(pattern string) {
		if _, exists := seen[pattern]; exists {
			return
		}
		seen[pattern] = struct{}{}
		patterns = append(patterns, pattern)
	}
	for _, action := range actions {
		add(claims.CanonicalBoardActionPattern(sessionFilter, claims.TopicWildcard, action))
		add(claims.CanonicalAgentActionPattern(sessionFilter, claims.TopicWildcard, action))
		add(claims.CanonicalAgentTypeActionPattern(sessionFilter, claims.TopicWildcard, action))
		add(claims.CanonicalClaimActionPattern(sessionFilter, claims.TopicWildcard, action))
		if _, ok := claims.DeltaActionArtifactLifecycleStatus(action); ok {
			add(claims.CanonicalArtifactActionPattern(sessionFilter, claims.TopicWildcard, action))
		}
		if _, ok := claims.DeltaActionValidationLifecycleStatus(action); ok || action == claims.DeltaActionValidationEvaluated {
			add(claims.CanonicalValidationActionPattern(sessionFilter, claims.TopicWildcard, action))
		}
	}
	return patterns
}

func (i *DeltaIngestor) run(ctx context.Context) error {
	defer i.stopSubscription()
	for {
		select {
		case <-ctx.Done():
			i.drain()
			return nil
		case delta := <-i.queue:
			i.ingest(ctx, delta)
		}
	}
}

func (i *DeltaIngestor) drain() {
	for {
		select {
		case delta := <-i.queue:
			drainCtx, cancel := context.WithTimeout(context.WithoutCancel(i.forest.runCtx), time.Second)
			i.ingest(drainCtx, delta)
			cancel()
		default:
			i.stopped.Store(true)
			return
		}
	}
}

func (i *DeltaIngestor) ingest(ctx context.Context, delta claims.CanonicalDelta) {
	if _, err := i.forest.AppendCanonicalDelta(ctx, delta); err != nil {
		i.setError(err)
		if !errors.Is(err, errEvidenceProjectionRejected) {
			i.recordRejectedCanonical(delta, err)
		}
		return
	}
	i.ingested.Add(1)
}

func (i *DeltaIngestor) recordRejectedCanonical(delta claims.CanonicalDelta, cause error) {
	if i == nil || i.forest == nil || cause == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(i.forest.runCtx), time.Second)
	defer cancel()
	_, err := i.forest.AppendLedgerRecord(ctx, LedgerRecord{
		SourceKind:  LedgerSourceMaintenance,
		SourceID:    delta.DeltaID,
		SourceKey:   "claims_delta_rejected:" + firstNonEmptyString(delta.DeltaKey(), delta.DeltaID),
		EventKind:   "claims_delta_rejected",
		SessionID:   firstNonEmptyString(delta.SessionID, "global"),
		BoardID:     delta.BoardID,
		SubjectType: "delta",
		SubjectID:   firstNonEmptyString(delta.DeltaID, delta.DeltaKey()),
		Actor:       delta.Actor,
		Reason:      cause.Error(),
		OccurredAt:  time.Now().UTC(),
		Payload: map[string]any{
			"delta_id":  delta.DeltaID,
			"delta_key": delta.DeltaKey(),
			"action":    string(delta.Action),
			"error":     cause.Error(),
		},
		Refs: delta.Refs,
	})
	if err != nil {
		i.setError(err)
	}
}

func (i *DeltaIngestor) recordOverflow(delta claims.CanonicalDelta) {
	if i == nil || i.forest == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(i.forest.runCtx), time.Second)
	defer cancel()
	_, err := i.forest.AppendLedgerRecord(ctx, LedgerRecord{
		SourceKind:  LedgerSourceMaintenance,
		SourceID:    delta.DeltaID,
		SourceKey:   "claims_delta_overflow:" + delta.DeltaKey(),
		EventKind:   "claims_delta_ingest_overflow",
		SessionID:   firstNonEmptyString(delta.SessionID, "global"),
		BoardID:     delta.BoardID,
		SubjectType: "delta",
		SubjectID:   delta.DeltaID,
		Actor:       delta.Actor,
		OccurredAt:  time.Now().UTC(),
		Payload: map[string]any{
			"delta_id":  delta.DeltaID,
			"delta_key": delta.DeltaKey(),
			"action":    string(delta.Action),
		},
		Refs: delta.Refs,
	})
	if err != nil {
		i.setError(err)
	}
}

func (i *DeltaIngestor) recordIgnoredLegacy(delta claims.Delta) {
	if i == nil || i.forest == nil || delta == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(i.forest.runCtx), time.Second)
	defer cancel()
	key := fmt.Sprintf("legacy_delta_ignored:%s:%s:%d:%s",
		firstNonEmptyString(delta.DeltaSessionID(), "global"),
		delta.DeltaKind(),
		delta.DeltaSequence(),
		delta.DeltaKey(),
	)
	_, err := i.forest.AppendLedgerRecord(ctx, LedgerRecord{
		SourceKind:  LedgerSourceMaintenance,
		SourceID:    delta.DeltaKey(),
		SourceKey:   key,
		EventKind:   "claims_delta_legacy_ignored",
		SessionID:   firstNonEmptyString(delta.DeltaSessionID(), "global"),
		SubjectType: "legacy_delta",
		SubjectID:   delta.DeltaKey(),
		Reason:      "production forest ingestion accepts canonical claims deltas only",
		OccurredAt:  time.Now().UTC(),
		Payload: map[string]any{
			"kind":     delta.DeltaKind(),
			"key":      delta.DeltaKey(),
			"sequence": delta.DeltaSequence(),
		},
	})
	if err != nil {
		i.setError(err)
	}
}

func (i *DeltaIngestor) setError(err error) {
	if err == nil {
		return
	}
	i.mu.Lock()
	i.lastError = err.Error()
	i.mu.Unlock()
}

func (i *DeltaIngestor) stopSubscription() {
	if i == nil {
		return
	}
	i.stopped.Store(true)
	for _, subscription := range i.subscriptions {
		if subscription == nil {
			continue
		}
		if err := subscription.Unsubscribe(); err != nil {
			i.setError(err)
		}
	}
}

func (i *DeltaIngestor) Snapshot() DeltaIngestorSnapshot {
	if i == nil {
		return DeltaIngestorSnapshot{}
	}
	i.mu.Lock()
	lastErr := i.lastError
	i.mu.Unlock()
	return DeltaIngestorSnapshot{
		Received:      i.received.Load(),
		Enqueued:      i.enqueued.Load(),
		Overflowed:    i.overflowed.Load(),
		Ingested:      i.ingested.Load(),
		IgnoredLegacy: i.ignoredLegacy.Load(),
		LastError:     lastErr,
	}
}

func (m *MemoryForest) DeltaIngestorSnapshot() DeltaIngestorSnapshot {
	if m == nil || m.deltaIngestor == nil {
		return DeltaIngestorSnapshot{}
	}
	return m.deltaIngestor.Snapshot()
}
