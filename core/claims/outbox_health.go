package claims

import (
	"fmt"
	"sort"
	"time"
)

const (
	projectionHealthHistoryLimit = 64
	projectionHealthFailureLimit = 32
)

type ProjectionHealthSnapshot struct {
	BoardID           string                         `json:"board_id,omitempty"`
	SessionID         string                         `json:"session_id,omitempty"`
	GeneratedAt       time.Time                      `json:"generated_at"`
	HighWaterSequence uint64                         `json:"high_water_sequence"`
	QueueDepth        int                            `json:"queue_depth"`
	MaxLag            uint64                         `json:"max_lag"`
	RetryCount        int                            `json:"retry_count"`
	TerminalFailures  int                            `json:"terminal_failures"`
	LeaseExpirations  int                            `json:"lease_expirations"`
	AverageLatency    time.Duration                  `json:"average_projection_latency,omitempty"`
	Projectors        []ProjectionProjectorHealth    `json:"projectors,omitempty"`
	Warnings          []string                       `json:"warnings,omitempty"`
	FeatureFlags      map[string]string              `json:"feature_flags,omitempty"`
	ShadowDiffs       []string                       `json:"shadow_diffs,omitempty"`
	Budgets           ClaimsOperationsBudgetSnapshot `json:"budgets,omitempty"`
}

type ProjectionProjectorHealth struct {
	Projector            string                     `json:"projector"`
	Pending              int                        `json:"pending"`
	InProgress           int                        `json:"in_progress"`
	Succeeded            int                        `json:"succeeded"`
	RetryableFailures    int                        `json:"retryable_failures"`
	TerminalFailures     int                        `json:"terminal_failures"`
	LeaseExpirations     int                        `json:"lease_expirations"`
	QueueDepth           int                        `json:"queue_depth"`
	RetryCount           int                        `json:"retry_count"`
	OldestPendingAge     time.Duration              `json:"oldest_pending_age,omitempty"`
	OldestPendingSeq     uint64                     `json:"oldest_pending_sequence,omitempty"`
	AverageLatency       time.Duration              `json:"average_projection_latency,omitempty"`
	Lag                  uint64                     `json:"lag"`
	TerminalFailureCount int                        `json:"terminal_failure_count,omitempty"`
	TerminalFailureIDs   []ProjectionFailureSummary `json:"terminal_failure_ids,omitempty"`

	latencyTotal time.Duration
	latencyCount int
}

type ProjectionFailureSummary struct {
	RecordID     string `json:"record_id"`
	Projector    string `json:"projector,omitempty"`
	Sequence     uint64 `json:"sequence"`
	EntityType   string `json:"entity_type"`
	EntityID     string `json:"entity_id"`
	MutationKind string `json:"mutation_kind"`
	LastError    string `json:"last_error,omitempty"`
}

func (b *ClaimsBoard) ProjectionHealth(now ...time.Time) ProjectionHealthSnapshot {
	if b == nil {
		cfg := CurrentRolloutConfig()
		return ProjectionHealthSnapshot{GeneratedAt: healthNow(now), FeatureFlags: cfg.FeatureFlags()}
	}
	if b.durable == nil {
		cfg := b.RolloutConfig()
		return ProjectionHealthSnapshot{
			BoardID:           b.BoardID(),
			SessionID:         b.SessionID(),
			GeneratedAt:       healthNow(now),
			HighWaterSequence: b.HighWaterSequence(),
			FeatureFlags:      cfg.FeatureFlags(),
		}
	}
	return b.durable.ProjectionHealth(now...)
}

func (b *ClaimsBoard) ProjectionHealthHistory(limit int) []ProjectionHealthSnapshot {
	if b == nil || b.durable == nil {
		return nil
	}
	return b.durable.ProjectionHealthHistory(limit)
}

func (db *DurableBoard) ProjectionHealth(now ...time.Time) ProjectionHealthSnapshot {
	t := healthNow(now)
	if db == nil || db.board == nil {
		cfg := CurrentRolloutConfig()
		return ProjectionHealthSnapshot{GeneratedAt: t, FeatureFlags: cfg.FeatureFlags()}
	}
	rollout := db.board.RolloutConfig()
	if db.outbox == nil {
		return ProjectionHealthSnapshot{
			BoardID:           db.board.BoardID(),
			SessionID:         db.board.SessionID(),
			GeneratedAt:       t,
			HighWaterSequence: db.board.HighWaterSequence(),
			Warnings:          []string{"claims durable board has no projection outbox"},
			FeatureFlags:      rollout.FeatureFlags(),
		}
	}
	snap := db.outbox.Health(db.board.BoardID(), db.board.SessionID(), db.board.HighWaterSequence(), t)
	snap.FeatureFlags = rollout.FeatureFlags()
	snap.Budgets = ClaimsOperationsBudgetSnapshotFromConfig(db.operations)
	if rollout.ClaimsKnowledgeMirror == ProjectionRolloutShadow {
		snap.ShadowDiffs = projectionShadowDiffs(snap)
	}
	db.recordProjectionHealthSnapshot(snap)
	return snap
}

func (db *DurableBoard) ProjectionHealthHistory(limit int) []ProjectionHealthSnapshot {
	if db == nil {
		return nil
	}
	db.healthMu.Lock()
	defer db.healthMu.Unlock()
	if limit <= 0 || limit > len(db.healthHistory) {
		limit = len(db.healthHistory)
	}
	if limit == 0 {
		return nil
	}
	start := len(db.healthHistory) - limit
	out := make([]ProjectionHealthSnapshot, 0, limit)
	for _, snap := range db.healthHistory[start:] {
		out = append(out, cloneProjectionHealthSnapshot(snap))
	}
	return out
}

func (db *DurableBoard) recordProjectionHealthSnapshot(snap ProjectionHealthSnapshot) {
	if db == nil {
		return
	}
	db.healthMu.Lock()
	defer db.healthMu.Unlock()
	db.healthHistory = append(db.healthHistory, cloneProjectionHealthSnapshot(snap))
	if len(db.healthHistory) > projectionHealthHistoryLimit {
		copy(db.healthHistory, db.healthHistory[len(db.healthHistory)-projectionHealthHistoryLimit:])
		db.healthHistory = db.healthHistory[:projectionHealthHistoryLimit]
	}
}

func (o *ClaimsOutbox) Health(boardID, sessionID string, highWater uint64, now time.Time) ProjectionHealthSnapshot {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	snap := ProjectionHealthSnapshot{
		BoardID:           boardID,
		SessionID:         sessionID,
		GeneratedAt:       now,
		HighWaterSequence: highWater,
	}
	if o == nil {
		snap.Warnings = append(snap.Warnings, "claims projection outbox is unavailable")
		return snap
	}
	records := o.Records()
	byProjector := make(map[string]*ProjectionProjectorHealth)
	for _, rec := range records {
		for name, slot := range rec.Projectors {
			ph := byProjector[name]
			if ph == nil {
				ph = &ProjectionProjectorHealth{Projector: name}
				byProjector[name] = ph
			}
			updateProjectorHealth(ph, rec, slot, highWater, now)
		}
	}
	names := make([]string, 0, len(byProjector))
	for name := range byProjector {
		names = append(names, name)
	}
	sort.Strings(names)
	var latencyTotal time.Duration
	var latencyCount int
	for _, name := range names {
		ph := *byProjector[name]
		if ph.latencyCount > 0 {
			ph.AverageLatency = ph.latencyTotal / time.Duration(ph.latencyCount)
		}
		latencyTotal += ph.latencyTotal
		latencyCount += ph.latencyCount
		ph.TerminalFailureCount = ph.TerminalFailures
		snap.Projectors = append(snap.Projectors, ph)
		snap.QueueDepth += ph.QueueDepth
		snap.RetryCount += ph.RetryCount
		snap.TerminalFailures += ph.TerminalFailures
		snap.LeaseExpirations += ph.LeaseExpirations
		if ph.Lag > snap.MaxLag {
			snap.MaxLag = ph.Lag
		}
		if ph.QueueDepth > 0 {
			snap.Warnings = append(snap.Warnings, "projection projector "+ph.Projector+" has pending or retryable work")
		}
		if ph.TerminalFailures > 0 {
			snap.Warnings = append(snap.Warnings, "projection projector "+ph.Projector+" has terminal failures")
		}
		if ph.LeaseExpirations > 0 {
			snap.Warnings = append(snap.Warnings, "projection projector "+ph.Projector+" has expired leases")
		}
	}
	if latencyCount > 0 {
		snap.AverageLatency = latencyTotal / time.Duration(latencyCount)
	}
	return snap
}

func updateProjectorHealth(ph *ProjectionProjectorHealth, rec ClaimsOutboxRecord, slot OutboxProjectorSlot, highWater uint64, now time.Time) {
	if ph == nil {
		return
	}
	ph.RetryCount += slot.Attempts
	switch slot.Status {
	case OutboxStatusSucceeded:
		ph.Succeeded++
		recordProjectionLatency(ph, rec, slot)
	case OutboxStatusInProgress:
		ph.InProgress++
		if !slot.LeaseUntil.IsZero() && now.After(slot.LeaseUntil) {
			ph.LeaseExpirations++
			recordUnresolved(ph, rec, highWater, now)
		}
	case OutboxStatusFailedRetryable:
		ph.RetryableFailures++
		recordUnresolved(ph, rec, highWater, now)
	case OutboxStatusFailedTerminal:
		ph.TerminalFailures++
		recordUnresolved(ph, rec, highWater, now)
		if len(ph.TerminalFailureIDs) < projectionHealthFailureLimit {
			ph.TerminalFailureIDs = append(ph.TerminalFailureIDs, ProjectionFailureSummary{
				RecordID:     rec.ID,
				Projector:    ph.Projector,
				Sequence:     rec.Sequence,
				EntityType:   rec.EntityType,
				EntityID:     rec.EntityID,
				MutationKind: rec.MutationKind,
				LastError:    slot.LastError,
			})
		}
	default:
		ph.Pending++
		recordUnresolved(ph, rec, highWater, now)
	}
}

func recordProjectionLatency(ph *ProjectionProjectorHealth, rec ClaimsOutboxRecord, slot OutboxProjectorSlot) {
	if ph == nil || rec.CreatedAt.IsZero() || slot.UpdatedAt.IsZero() || slot.UpdatedAt.Before(rec.CreatedAt) {
		return
	}
	ph.latencyTotal += slot.UpdatedAt.Sub(rec.CreatedAt)
	ph.latencyCount++
}

func recordUnresolved(ph *ProjectionProjectorHealth, rec ClaimsOutboxRecord, highWater uint64, now time.Time) {
	ph.QueueDepth++
	if ph.OldestPendingSeq == 0 || rec.Sequence < ph.OldestPendingSeq {
		ph.OldestPendingSeq = rec.Sequence
		if !rec.CreatedAt.IsZero() {
			ph.OldestPendingAge = now.Sub(rec.CreatedAt)
		}
	}
	if highWater >= rec.Sequence {
		lag := highWater - rec.Sequence
		if lag > ph.Lag {
			ph.Lag = lag
		}
	}
}

func healthNow(values []time.Time) time.Time {
	if len(values) > 0 && !values[0].IsZero() {
		return values[0].UTC()
	}
	return time.Now().UTC()
}

func cloneProjectionHealthSnapshot(in ProjectionHealthSnapshot) ProjectionHealthSnapshot {
	out := in
	out.Projectors = append([]ProjectionProjectorHealth(nil), in.Projectors...)
	for i := range out.Projectors {
		out.Projectors[i].TerminalFailureIDs = append([]ProjectionFailureSummary(nil), in.Projectors[i].TerminalFailureIDs...)
	}
	out.Warnings = append([]string(nil), in.Warnings...)
	if in.FeatureFlags != nil {
		out.FeatureFlags = make(map[string]string, len(in.FeatureFlags))
		for k, v := range in.FeatureFlags {
			out.FeatureFlags[k] = v
		}
	}
	out.ShadowDiffs = append([]string(nil), in.ShadowDiffs...)
	out.Budgets = in.Budgets
	out.Budgets.Warnings = append([]string(nil), in.Budgets.Warnings...)
	return out
}

func projectionShadowDiffs(snap ProjectionHealthSnapshot) []string {
	hasKnowledge := false
	for _, ph := range snap.Projectors {
		if ph.Projector != ProjectorKnowledge {
			continue
		}
		hasKnowledge = true
		if ph.QueueDepth > 0 || ph.RetryableFailures > 0 || ph.TerminalFailures > 0 || ph.LeaseExpirations > 0 {
			return []string{fmt.Sprintf("shadow projection diff projector=%s queue_depth=%d lag=%d retryable_failures=%d terminal_failures=%d lease_expirations=%d", ph.Projector, ph.QueueDepth, ph.Lag, ph.RetryableFailures, ph.TerminalFailures, ph.LeaseExpirations)}
		}
	}
	if !hasKnowledge {
		return []string{"shadow projection diff projector=knowledge status=not_configured"}
	}
	return nil
}
