package forest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Retrieval-candidates projector tunables.
const (
	// projectorRetrievalCandidatesName is the canonical name of the
	// candidates projector entry in forest_projector_state.
	projectorRetrievalCandidatesName = "retrieval_candidates"

	// retrievalCandidatesBatchSize is the maximum number of audit
	// events fanned out per catch-up cycle. Each event yields N rows
	// in forest_retrieval_candidates (N = candidate count); keep the
	// outer batch small so transaction sizes stay bounded.
	retrievalCandidatesBatchSize = 16
)

// startRetrievalCandidatesProjector launches the tracked goroutine
// that drives the retrieval-candidates projector. Reuses the
// projector framework (lease, watermark, panic recovery, transient
// vs fatal classification, poison pill) used by the branch
// projector.
func (m *MemoryForest) startRetrievalCandidatesProjector() {
	if m == nil {
		return
	}
	m.startWorker("retrieval_candidates_projector", retrievalCandidatesBatchSize, func() {
		m.runRetrievalCandidatesProjectorLoop()
	})
}

// runRetrievalCandidatesProjectorLoop is the projector's outer loop.
// Mirrors runBranchProjectorLoop's structure (lease, session,
// backoff) but applies retrieval-audit-event projections.
func (m *MemoryForest) runRetrievalCandidatesProjectorLoop() {
	backoff := newProjectorBackoff()
	for {
		if m.shouldStopProjector() {
			return
		}
		state, err := m.acquireRetrievalCandidatesProjectorLease()
		if err != nil {
			m.handleLeaseAcquireFailure(err)
			if !m.sleepProjector(projectorWaitForLease) {
				return
			}
			continue
		}
		if err := m.runRetrievalCandidatesSession(state, backoff); err != nil {
			if errors.Is(err, errProjectorHalt) {
				m.markRetrievalCandidatesHalted(state, err)
				if !m.sleepProjector(projectorBackoffMax) {
					return
				}
				continue
			}
			backoff.observeFailure()
			m.recordRetrievalCandidatesError(state, err)
			if !m.sleepProjector(backoff.delay()) {
				return
			}
			continue
		}
		backoff.observeSuccess()
	}
}

// runRetrievalCandidatesSession runs while holding the lease. Drains
// retrieval audit events in seq order; renews the lease periodically.
func (m *MemoryForest) runRetrievalCandidatesSession(state *projectorState, backoff *projectorBackoff) error {
	renewTicker := time.NewTicker(projectorRenewInterval)
	defer renewTicker.Stop()
	for {
		if m.shouldStopProjector() {
			return nil
		}
		processed, err := m.processRetrievalCandidatesBatch(state)
		if err != nil {
			return err
		}
		if processed > 0 {
			backoff.observeSuccess()
			continue
		}
		if err := m.waitForProjectorWork(renewTicker, state, m.retrievalCandidatesWake); err != nil {
			return err
		}
	}
}

// processRetrievalCandidatesBatch reads up to retrievalCandidatesBatchSize
// audit events past the watermark and projects each into per-candidate
// rows. Same poison-pill protection as the branch projector.
func (m *MemoryForest) processRetrievalCandidatesBatch(state *projectorState) (int, error) {
	events, err := m.loadRetrievalAuditsAfterSeq(state.lastAppliedSeq, retrievalCandidatesBatchSize)
	if err != nil {
		return 0, fmt.Errorf("load retrieval audit events: %w", err)
	}
	for i := range events {
		if m.shouldStopProjector() {
			return i, nil
		}
		err := m.applyRetrievalCandidatesEvent(state, &events[i])
		if err == nil {
			state.clearPoison()
			continue
		}
		if errors.Is(err, errProjectorHalt) || errors.Is(err, errLeaseHeldByOther) {
			return i, err
		}
		if state.recordPoisonHit(events[i].Seq) {
			return i, fmt.Errorf("%w: poison pill on retrieval seq %d after %d retries: %v",
				errProjectorHalt, events[i].Seq, state.poisonCount, err)
		}
		return i, err
	}
	return len(events), nil
}

// applyRetrievalCandidatesEvent projects one audit event into per-
// candidate rows inside one transaction. The watermark UPDATE is
// gated on leader_holder so a lost lease aborts the transaction.
func (m *MemoryForest) applyRetrievalCandidatesEvent(state *projectorState, event *RetrievalAuditEvent) error {
	if event.Seq <= state.lastAppliedSeq {
		return nil
	}
	tx, err := m.db.BeginTx(m.runCtx, nil)
	if err != nil {
		return classifyApplyErr(fmt.Errorf("begin retrieval candidates tx: %w", err))
	}
	defer tx.Rollback()

	if err := upsertRetrievalCandidatesTx(m.runCtx, tx, event); err != nil {
		return classifyApplyErr(err)
	}
	updated, err := setProjectorWatermarkUnderLeaseTx(m.runCtx, tx, state.name, m.projectorID, event.Seq, event.RequestedAt)
	if err != nil {
		return classifyApplyErr(err)
	}
	if !updated {
		return errLeaseHeldByOther
	}
	if err := tx.Commit(); err != nil {
		return classifyApplyErr(fmt.Errorf("commit retrieval candidates tx: %w", err))
	}
	state.lastAppliedSeq = event.Seq
	state.lastAppliedAt = event.RequestedAt
	return nil
}

// upsertRetrievalCandidatesTx writes one row per candidate from the
// event's candidates_blob. Idempotent via the (retrieval_event_id,
// branch_id) PRIMARY KEY → reapply produces the same rows.
func upsertRetrievalCandidatesTx(ctx context.Context, tx *sql.Tx, event *RetrievalAuditEvent) error {
	if event == nil || len(event.Candidates) == 0 {
		return nil
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO forest_retrieval_candidates
		(retrieval_event_id, retrieval_seq, branch_id, session_id,
		 rank_position, returned, base_score, final_score,
		 predicted_utility, predicted_risk, exploration_mode, retrieval_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (retrieval_event_id, branch_id) DO UPDATE SET
			retrieval_seq     = excluded.retrieval_seq,
			session_id        = excluded.session_id,
			rank_position     = excluded.rank_position,
			returned          = excluded.returned,
			base_score        = excluded.base_score,
			final_score       = excluded.final_score,
			predicted_utility = excluded.predicted_utility,
			predicted_risk    = excluded.predicted_risk,
			exploration_mode  = excluded.exploration_mode,
			retrieval_at      = excluded.retrieval_at
	`)
	if err != nil {
		return fmt.Errorf("prepare retrieval candidate insert: %w", err)
	}
	defer stmt.Close()

	for i := range event.Candidates {
		c := &event.Candidates[i]
		if c.BranchID == "" {
			continue
		}
		if _, err := stmt.ExecContext(ctx,
			event.ID,
			event.Seq,
			c.BranchID,
			event.SessionID,
			c.RankPosition,
			boolToInt(c.Returned),
			c.BaseScore,
			c.FinalScore,
			c.PredictedUtility,
			c.PredictedRisk,
			boolToInt(event.ExplorationMode),
			event.RequestedAt.Unix(),
		); err != nil {
			return fmt.Errorf("insert retrieval candidate row: %w", err)
		}
	}
	return nil
}

// loadRetrievalAuditsAfterSeq fetches retrieval audit events with
// seq > after, in ascending seq order. Rebuilds the full
// RetrievalAuditEvent (including candidates_blob → []Candidate) for
// each so the projector can write one row per candidate.
func (m *MemoryForest) loadRetrievalAuditsAfterSeq(after int64, limit int) ([]RetrievalAuditEvent, error) {
	rows, err := m.queryRetrievalAuditRowsAfterSeq(after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RetrievalAuditEvent
	for rows.Next() {
		event, scanErr := scanRetrievalAuditRow(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, *event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate retrieval audit rows: %w", err)
	}
	return out, nil
}

func (m *MemoryForest) queryRetrievalAuditRowsAfterSeq(after int64, limit int) (*sql.Rows, error) {
	rows, err := m.db.QueryContext(m.runCtx, `
		SELECT s.seq,
		       e.id, e.session_id, e.task_id, e.agent_id, e.agent_type,
		       e.intent_id, e.query, e.horizon, e.families_blob,
		       e.requested_limit, e.include_counter_evidence, e.requested_at,
		       e.duration_micros, e.candidate_count, e.returned_count,
		       e.model_key, e.model_version, e.error_message,
		       e.branch_projection_seq, e.candidates_blob, e.metadata_blob,
		       e.exploration_mode, e.substrate_mode,
		       e.base_score_version, e.base_score_variant,
		       e.hyperparam_snapshot_id, e.proposed_hyperparams
		FROM   forest_retrieval_events e
		JOIN   forest_retrieval_event_seq_log s ON s.event_id = e.id
		WHERE  s.seq > ?
		ORDER BY s.seq ASC
		LIMIT  ?
	`, after, limit)
	if err != nil {
		return nil, fmt.Errorf("query retrieval audits after seq: %w", err)
	}
	return rows, nil
}

// ────────────────────────────────────────────────────────────────────
// Lease / health (mirrors branch projector)
// ────────────────────────────────────────────────────────────────────

func (m *MemoryForest) acquireRetrievalCandidatesProjectorLease() (*projectorState, error) {
	if err := m.ensureProjectorRow(projectorRetrievalCandidatesName); err != nil {
		return nil, err
	}
	now := time.Now().UTC().Unix()
	expires := now + int64(projectorLeaseDuration.Seconds())
	res, err := m.db.ExecContext(m.runCtx, `
		UPDATE forest_projector_state
		SET    leader_holder      = ?,
		       leader_lease_until = ?,
		       health_status      = ?,
		       updated_at         = ?
		WHERE  projector_name      = ?
		  AND  (leader_lease_until < ? OR leader_holder = ?)
	`, m.projectorID, expires, string(ProjectorHealthRunning), now, projectorRetrievalCandidatesName, now, m.projectorID)
	if err != nil {
		return nil, fmt.Errorf("acquire retrieval candidates lease: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, errLeaseHeldByOther
	}
	return m.loadProjectorState(projectorRetrievalCandidatesName)
}

func (m *MemoryForest) markRetrievalCandidatesHalted(state *projectorState, cause error) {
	if m.runCtx == nil || m.runCtx.Err() != nil {
		return
	}
	msg := truncateError(cause.Error(), projectorErrTruncate)
	now := time.Now().UTC().Unix()
	_, err := m.db.ExecContext(m.runCtx, `
		UPDATE forest_projector_state
		SET    health_status = ?,
		       last_error    = ?,
		       last_error_at = ?,
		       updated_at    = ?
		WHERE  projector_name = ?
	`, string(ProjectorHealthHalted), msg, now, now, state.name)
	if err != nil {
		slog.Error("forest_retrieval_candidates_mark_halted_failed",
			"projector", state.name, "err", err.Error())
		return
	}
	slog.Error("forest_retrieval_candidates_halted",
		"projector", state.name,
		"holder", m.projectorID,
		"last_applied_seq", state.lastAppliedSeq,
		"err", msg,
	)
}

func (m *MemoryForest) recordRetrievalCandidatesError(state *projectorState, cause error) {
	if m.runCtx == nil || m.runCtx.Err() != nil {
		return
	}
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return
	}
	msg := truncateError(cause.Error(), projectorErrTruncate)
	now := time.Now().UTC().Unix()
	_, err := m.db.ExecContext(m.runCtx, `
		UPDATE forest_projector_state
		SET    last_error    = ?,
		       last_error_at = ?,
		       updated_at    = ?
		WHERE  projector_name = ?
	`, msg, now, now, state.name)
	if err != nil {
		slog.Warn("forest_retrieval_candidates_record_error_failed",
			"projector", state.name, "err", err.Error())
		return
	}
	slog.Warn("forest_retrieval_candidates_error",
		"projector", state.name,
		"holder", m.projectorID,
		"err", msg,
	)
}
