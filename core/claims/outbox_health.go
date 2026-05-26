package claims

import (
	"sort"
	"time"
)

type ProjectionHealthSnapshot struct {
	BoardID           string                      `json:"board_id,omitempty"`
	SessionID         string                      `json:"session_id,omitempty"`
	GeneratedAt       time.Time                   `json:"generated_at"`
	HighWaterSequence uint64                      `json:"high_water_sequence"`
	QueueDepth        int                         `json:"queue_depth"`
	MaxLag            uint64                      `json:"max_lag"`
	RetryCount        int                         `json:"retry_count"`
	TerminalFailures  int                         `json:"terminal_failures"`
	LeaseExpirations  int                         `json:"lease_expirations"`
	Projectors        []ProjectionProjectorHealth `json:"projectors,omitempty"`
	Warnings          []string                    `json:"warnings,omitempty"`
}

type ProjectionProjectorHealth struct {
	Projector          string                     `json:"projector"`
	Pending            int                        `json:"pending"`
	InProgress         int                        `json:"in_progress"`
	Succeeded          int                        `json:"succeeded"`
	RetryableFailures  int                        `json:"retryable_failures"`
	TerminalFailures   int                        `json:"terminal_failures"`
	LeaseExpirations   int                        `json:"lease_expirations"`
	QueueDepth         int                        `json:"queue_depth"`
	RetryCount         int                        `json:"retry_count"`
	OldestPendingAge   time.Duration              `json:"oldest_pending_age,omitempty"`
	OldestPendingSeq   uint64                     `json:"oldest_pending_sequence,omitempty"`
	Lag                uint64                     `json:"lag"`
	TerminalFailureIDs []ProjectionFailureSummary `json:"terminal_failure_ids,omitempty"`
}

type ProjectionFailureSummary struct {
	RecordID     string `json:"record_id"`
	Sequence     uint64 `json:"sequence"`
	EntityType   string `json:"entity_type"`
	EntityID     string `json:"entity_id"`
	MutationKind string `json:"mutation_kind"`
	LastError    string `json:"last_error,omitempty"`
}

func (b *ClaimsBoard) ProjectionHealth(now ...time.Time) ProjectionHealthSnapshot {
	if b == nil {
		return ProjectionHealthSnapshot{GeneratedAt: healthNow(now)}
	}
	if b.durable == nil {
		return ProjectionHealthSnapshot{
			BoardID:           b.BoardID(),
			SessionID:         b.SessionID(),
			GeneratedAt:       healthNow(now),
			HighWaterSequence: b.HighWaterSequence(),
		}
	}
	return b.durable.ProjectionHealth(now...)
}

func (db *DurableBoard) ProjectionHealth(now ...time.Time) ProjectionHealthSnapshot {
	t := healthNow(now)
	if db == nil || db.board == nil {
		return ProjectionHealthSnapshot{GeneratedAt: t}
	}
	if db.outbox == nil {
		return ProjectionHealthSnapshot{
			BoardID:           db.board.BoardID(),
			SessionID:         db.board.SessionID(),
			GeneratedAt:       t,
			HighWaterSequence: db.board.HighWaterSequence(),
			Warnings:          []string{"claims durable board has no projection outbox"},
		}
	}
	return db.outbox.Health(db.board.BoardID(), db.board.SessionID(), db.board.HighWaterSequence(), t)
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
	for _, name := range names {
		ph := *byProjector[name]
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
		ph.TerminalFailureIDs = append(ph.TerminalFailureIDs, ProjectionFailureSummary{
			RecordID:     rec.ID,
			Sequence:     rec.Sequence,
			EntityType:   rec.EntityType,
			EntityID:     rec.EntityID,
			MutationKind: rec.MutationKind,
			LastError:    slot.LastError,
		})
	default:
		ph.Pending++
		recordUnresolved(ph, rec, highWater, now)
	}
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
