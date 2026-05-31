package forest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Retrieval audit constants. Named so there are no magic numbers and
// policy knobs have one home.
const (
	// retrievalAuditDefaultLimit is the cap on QueryRetrievalAudits
	// when no Limit is supplied. Bounds the result set so large
	// audit tables don't blow operator-query budgets.
	retrievalAuditDefaultLimit = 200

	// retrievalAuditMaxLimit caps the user-supplied Limit so a
	// caller can't request the entire ledger in one query.
	retrievalAuditMaxLimit = 5000

	// retrievalAuditEventErrorTruncate caps the size of the
	// error_message column. Long stack traces shouldn't pollute the
	// audit row.
	retrievalAuditEventErrorTruncate = 2048

	// retrievalAuditQueueCapacity is the size of the buffered
	// channel between Retrieve callers (producers) and the audit
	// drainer goroutine (consumer). Sized for ~100 seconds of slack
	// at ~10 retrievals/sec; tune upward if drop-on-full warnings
	// appear in observability.
	retrievalAuditQueueCapacity = 1024

	// retrievalAuditDrainTimeout caps each individual audit DB
	// write performed by the drainer. Prevents a stuck DB from
	// blocking the drainer indefinitely; on timeout we drop and
	// log, the next audit proceeds.
	retrievalAuditDrainTimeout = 5 * time.Second
)

// AppendRetrievalAudit writes a retrieval audit event to the
// append-only ledger. Synchronous — durable on return. Idempotent
// via the event ID UNIQUE constraint: re-appending the same ID is a
// no-op that returns the canonical seq.
//
// Best-effort from the retrieval caller's perspective: callers
// typically log and swallow errors here because the retrieval has
// already completed successfully and the audit is observational.
// Returns the assigned seq on success (zero on failure).
func (m *MemoryForest) AppendRetrievalAudit(ctx context.Context, event *RetrievalAuditEvent) (int64, error) {
	if m == nil || event == nil {
		return 0, errors.New("nil forest or audit event")
	}
	prepareRetrievalAuditEvent(event)

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin retrieval audit tx: %w", err)
	}
	defer tx.Rollback()

	candidatesJSON, err := encodeRetrievalAuditCandidates(event.Candidates)
	if err != nil {
		return 0, err
	}
	familiesJSON, err := encodeRetrievalAuditFamilies(event.Families)
	if err != nil {
		return 0, err
	}
	metadataJSON, err := encodeRetrievalAuditMetadata(event.Metadata)
	if err != nil {
		return 0, err
	}

	inserted, err := insertRetrievalAuditTx(ctx, tx, event, candidatesJSON, familiesJSON, metadataJSON)
	if err != nil {
		return 0, err
	}
	seq, err := allocateOrFetchRetrievalAuditSeqTx(ctx, tx, event.ID, event.RequestedAt, inserted)
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit retrieval audit tx: %w", err)
	}
	event.Seq = seq
	// Wake the candidates projector so it picks up the new event
	// promptly. Non-blocking — drops if already pending.
	m.notifyRetrievalCandidatesProjector()
	return seq, nil
}

// notifyRetrievalCandidatesProjector wakes the retrieval-candidates
// projector. Non-blocking; the wake channel is buffered to size 1.
func (m *MemoryForest) notifyRetrievalCandidatesProjector() {
	if m == nil || m.retrievalCandidatesWake == nil {
		return
	}
	select {
	case m.retrievalCandidatesWake <- struct{}{}:
	default:
		m.recordRuntimeQueueOverflow("retrieval_candidates_wake", "retrieval candidates wake already pending")
	}
}

// QueryRetrievalAudits returns audit events matching the filter.
// Defensive — caller-supplied limits are clamped; nil receiver
// returns nil; empty result is not an error.
func (m *MemoryForest) QueryRetrievalAudits(ctx context.Context, filter RetrievalAuditFilter) ([]*RetrievalAuditEvent, error) {
	if m == nil {
		return nil, nil
	}
	limit := clampRetrievalAuditLimit(filter.Limit)

	rows, err := m.queryRetrievalAuditRows(ctx, filter, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*RetrievalAuditEvent, 0, limit)
	for rows.Next() {
		event, scanErr := scanRetrievalAuditRow(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		if filter.BranchID != "" && !auditCandidatesContain(event.Candidates, filter.BranchID) {
			continue
		}
		out = append(out, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit rows: %w", err)
	}
	return out, nil
}

// LatestRetrievalAuditSeq returns the highest seq currently in the
// audit ledger. Used by replay tooling and tests to bound iteration.
func (m *MemoryForest) LatestRetrievalAuditSeq(ctx context.Context) (int64, error) {
	if m == nil {
		return 0, nil
	}
	row := m.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM forest_retrieval_event_seq_log`)
	var seq int64
	if err := row.Scan(&seq); err != nil {
		return 0, fmt.Errorf("max audit seq: %w", err)
	}
	return seq, nil
}

// ────────────────────────────────────────────────────────────────────
// Append helpers
// ────────────────────────────────────────────────────────────────────

func prepareRetrievalAuditEvent(event *RetrievalAuditEvent) {
	if event.ID == "" {
		event.ID = "retrieval_" + uuid.NewString()
	}
	if event.RequestedAt.IsZero() {
		event.RequestedAt = time.Now().UTC()
	}
	event.ErrorMessage = truncateError(event.ErrorMessage, retrievalAuditEventErrorTruncate)
	if event.Candidates == nil {
		event.Candidates = []RetrievalAuditCandidate{}
	}
}

func encodeRetrievalAuditCandidates(candidates []RetrievalAuditCandidate) (string, error) {
	data, err := json.Marshal(candidates)
	if err != nil {
		return "", fmt.Errorf("marshal audit candidates: %w", err)
	}
	return string(data), nil
}

func encodeRetrievalAuditFamilies(families []TreeFamily) (string, error) {
	if len(families) == 0 {
		return "", nil
	}
	data, err := json.Marshal(families)
	if err != nil {
		return "", fmt.Errorf("marshal audit families: %w", err)
	}
	return string(data), nil
}

func encodeRetrievalAuditMetadata(meta map[string]any) (string, error) {
	if len(meta) == 0 {
		return "", nil
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return "", fmt.Errorf("marshal audit metadata: %w", err)
	}
	return string(data), nil
}

func insertRetrievalAuditTx(ctx context.Context, tx *sql.Tx, event *RetrievalAuditEvent, candidatesJSON, familiesJSON, metadataJSON string) (bool, error) {
	res, err := tx.ExecContext(ctx, `
		INSERT INTO forest_retrieval_events
		(id, session_id, task_id, agent_id, agent_type, intent_id,
		 query, horizon, families_blob, requested_limit,
		 include_counter_evidence, requested_at, duration_micros,
		 candidate_count, returned_count, model_key, model_version,
		 error_message, branch_projection_seq, candidates_blob,
		 metadata_blob, exploration_mode, substrate_mode,
		 base_score_version, base_score_variant,
		 hyperparam_snapshot_id, proposed_hyperparams)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO NOTHING
	`,
		event.ID,
		event.SessionID,
		nullStringValue(event.TaskID),
		nullStringValue(event.AgentID),
		nullStringValue(event.AgentType),
		nullStringValue(event.IntentID),
		event.Query,
		nullStringValue(string(event.Horizon)),
		nullStringValue(familiesJSON),
		event.RequestedLimit,
		boolToInt(event.IncludeCounterEvidence),
		event.RequestedAt.Unix(),
		event.Duration.Microseconds(),
		event.CandidateCount,
		event.ReturnedCount,
		nullStringValue(event.ModelKey),
		event.ModelVersion,
		nullStringValue(event.ErrorMessage),
		event.BranchProjectionSeq,
		candidatesJSON,
		nullStringValue(metadataJSON),
		boolToInt(event.ExplorationMode),
		string(event.SubstrateMode),
		event.BaseScoreVersion,
		string(event.BaseScoreVariant),
		event.HyperparamSnapshotID,
		boolToInt(event.ProposedHyperparams),
	)
	if err != nil {
		return false, fmt.Errorf("insert retrieval audit event: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected for retrieval audit: %w", err)
	}
	return affected == 1, nil
}

func allocateOrFetchRetrievalAuditSeqTx(ctx context.Context, tx *sql.Tx, eventID string, ts time.Time, inserted bool) (int64, error) {
	if inserted {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO forest_retrieval_event_seq_log (event_id, appended_at)
			VALUES (?, ?)
		`, eventID, ts.Unix())
		if err != nil {
			return 0, fmt.Errorf("insert retrieval audit seq log: %w", err)
		}
		seq, err := res.LastInsertId()
		if err != nil {
			return 0, fmt.Errorf("retrieval audit seq last insert id: %w", err)
		}
		return seq, nil
	}
	row := tx.QueryRowContext(ctx, `SELECT seq FROM forest_retrieval_event_seq_log WHERE event_id = ?`, eventID)
	var seq int64
	if err := row.Scan(&seq); err != nil {
		return 0, fmt.Errorf("fetch existing retrieval audit seq: %w", err)
	}
	return seq, nil
}

// ────────────────────────────────────────────────────────────────────
// Query helpers
// ────────────────────────────────────────────────────────────────────

func clampRetrievalAuditLimit(requested int) int {
	if requested <= 0 {
		return retrievalAuditDefaultLimit
	}
	if requested > retrievalAuditMaxLimit {
		return retrievalAuditMaxLimit
	}
	return requested
}

func (m *MemoryForest) queryRetrievalAuditRows(ctx context.Context, filter RetrievalAuditFilter, limit int) (*sql.Rows, error) {
	clauses, args := buildRetrievalAuditWhere(filter)
	query := `
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
	`
	if len(clauses) > 0 {
		query += "WHERE " + strings.Join(clauses, " AND ") + "\n"
	}
	query += `
		ORDER BY e.requested_at DESC, s.seq DESC
		LIMIT ?
	`
	args = append(args, limit)

	rows, err := m.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query retrieval audits: %w", err)
	}
	return rows, nil
}

func buildRetrievalAuditWhere(filter RetrievalAuditFilter) ([]string, []any) {
	var (
		clauses []string
		args    []any
	)
	if filter.SessionID != "" {
		clauses = append(clauses, "e.session_id = ?")
		args = append(args, filter.SessionID)
	}
	if filter.AgentID != "" {
		clauses = append(clauses, "e.agent_id = ?")
		args = append(args, filter.AgentID)
	}
	if filter.IntentID != "" {
		clauses = append(clauses, "e.intent_id = ?")
		args = append(args, filter.IntentID)
	}
	if !filter.Since.IsZero() {
		clauses = append(clauses, "e.requested_at >= ?")
		args = append(args, filter.Since.Unix())
	}
	if !filter.Until.IsZero() {
		clauses = append(clauses, "e.requested_at <= ?")
		args = append(args, filter.Until.Unix())
	}
	return clauses, args
}

func scanRetrievalAuditRow(rows *sql.Rows) (*RetrievalAuditEvent, error) {
	var (
		event                  RetrievalAuditEvent
		seq                    int64
		taskID                 sql.NullString
		agentID                sql.NullString
		agentType              sql.NullString
		intentID               sql.NullString
		horizon                sql.NullString
		familiesJSON           sql.NullString
		includeCounterInt      int
		requestedAtUnix        int64
		durationMicros         int64
		modelKey               sql.NullString
		errorMessage           sql.NullString
		candidatesJSON         string
		metadataJSON           sql.NullString
		explorationModeInt     int
		substrateModeStr       sql.NullString
		baseScoreVersion       int64
		baseScoreVariantStr    sql.NullString
		hyperparamSnapshotID   int64
		proposedHyperparamsInt int
	)
	err := rows.Scan(
		&seq,
		&event.ID, &event.SessionID, &taskID, &agentID, &agentType,
		&intentID, &event.Query, &horizon, &familiesJSON,
		&event.RequestedLimit, &includeCounterInt, &requestedAtUnix,
		&durationMicros, &event.CandidateCount, &event.ReturnedCount,
		&modelKey, &event.ModelVersion, &errorMessage,
		&event.BranchProjectionSeq, &candidatesJSON, &metadataJSON,
		&explorationModeInt, &substrateModeStr,
		&baseScoreVersion, &baseScoreVariantStr,
		&hyperparamSnapshotID, &proposedHyperparamsInt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan audit row: %w", err)
	}
	event.Seq = seq
	event.TaskID = taskID.String
	event.AgentID = agentID.String
	event.AgentType = agentType.String
	event.IntentID = intentID.String
	event.Horizon = CanopyHorizon(horizon.String)
	event.IncludeCounterEvidence = includeCounterInt != 0
	event.RequestedAt = time.Unix(requestedAtUnix, 0).UTC()
	event.Duration = time.Duration(durationMicros) * time.Microsecond
	event.ModelKey = modelKey.String
	event.ErrorMessage = errorMessage.String
	event.ExplorationMode = explorationModeInt != 0
	event.SubstrateMode = SubstrateMode(substrateModeStr.String)
	event.BaseScoreVersion = baseScoreVersion
	event.BaseScoreVariant = BaseScoreVariant(baseScoreVariantStr.String)
	event.HyperparamSnapshotID = hyperparamSnapshotID
	event.ProposedHyperparams = proposedHyperparamsInt != 0

	if err := decodeAuditFamilies(familiesJSON, &event); err != nil {
		return nil, err
	}
	if err := decodeAuditCandidates(candidatesJSON, &event); err != nil {
		return nil, err
	}
	if err := decodeAuditMetadata(metadataJSON, &event); err != nil {
		return nil, err
	}
	return &event, nil
}

func decodeAuditFamilies(raw sql.NullString, event *RetrievalAuditEvent) error {
	if !raw.Valid || raw.String == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw.String), &event.Families); err != nil {
		return fmt.Errorf("decode audit families: %w", err)
	}
	return nil
}

func decodeAuditCandidates(raw string, event *RetrievalAuditEvent) error {
	if raw == "" {
		event.Candidates = nil
		return nil
	}
	if err := json.Unmarshal([]byte(raw), &event.Candidates); err != nil {
		return fmt.Errorf("decode audit candidates: %w", err)
	}
	return nil
}

func decodeAuditMetadata(raw sql.NullString, event *RetrievalAuditEvent) error {
	if !raw.Valid || raw.String == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw.String), &event.Metadata); err != nil {
		return fmt.Errorf("decode audit metadata: %w", err)
	}
	return nil
}

func auditCandidatesContain(candidates []RetrievalAuditCandidate, branchID string) bool {
	for i := range candidates {
		if candidates[i].BranchID == branchID {
			return true
		}
	}
	return false
}

// ────────────────────────────────────────────────────────────────────
// Audit emission helper
// ────────────────────────────────────────────────────────────────────

// emitRetrievalAudit hands the audit event to the async drainer.
// Non-blocking — never delays the Retrieve response path. If the
// queue is full, the drainer has shut down, or the forest is closed,
// the audit cannot be queued, a bounded overflow ledger record is written so
// back-pressure is observable instead of becoming a silent drop.
func (m *MemoryForest) emitRetrievalAudit(ctx context.Context, event *RetrievalAuditEvent) {
	if m == nil || event == nil {
		return
	}
	// Post-Close shutdown: drop without enqueueing. Without this
	// guard the queue would accumulate orphaned events with no
	// consumer (drainer goroutine already exited), and
	// WaitForRetrievalAuditDrain would block forever on the
	// in-flight counter.
	if m.retrievalAuditClosed.Load() {
		slog.Debug("forest_retrieval_audit_dropped_after_close",
			"retrieval_id", event.ID,
		)
		return
	}
	if m.retrievalAuditQueue == nil {
		// Pre-startup — fall back to sync inline so test scenarios
		// that don't run a drainer still observe audits. Production
		// never hits this path.
		m.appendRetrievalAuditSync(ctx, event)
		return
	}
	m.retrievalAuditInFlight.Add(1)
	select {
	case m.retrievalAuditQueue <- event:
	default:
		m.retrievalAuditInFlight.Add(-1)
		m.recordRuntimeQueueOverflow("retrieval_audit", "retrieval audit queue full")
		m.recordRetrievalAuditOverflow(ctx, event)
		slog.Warn("forest_retrieval_audit_queue_full",
			"retrieval_id", event.ID,
			"session_id", event.SessionID,
			"queue_capacity", retrievalAuditQueueCapacity,
		)
		m.signalAuditIdleIfDone()
	}
}

func (m *MemoryForest) recordRetrievalAuditOverflow(ctx context.Context, event *RetrievalAuditEvent) {
	if m == nil || event == nil {
		return
	}
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), retrievalAuditDrainTimeout)
	defer cancel()
	_, err := m.AppendLedgerRecord(recordCtx, LedgerRecord{
		SourceKind:  LedgerSourceMaintenance,
		SourceID:    event.ID,
		SourceKey:   "retrieval_audit_overflow:" + event.ID,
		EventKind:   "retrieval_audit_overflow",
		SessionID:   firstNonEmptyString(event.SessionID, "global"),
		TaskID:      event.TaskID,
		SubjectType: "retrieval_audit",
		SubjectID:   event.ID,
		Reason:      "retrieval audit queue full",
		OccurredAt:  time.Now().UTC(),
		Payload: map[string]any{
			"retrieval_id": event.ID,
			"query":        event.Query,
			"limit":        event.RequestedLimit,
		},
	})
	if err != nil {
		slog.Warn("forest_retrieval_audit_overflow_record_failed",
			"retrieval_id", event.ID,
			"session_id", event.SessionID,
			"err", err.Error(),
		)
	}
}

// appendRetrievalAuditSync is the synchronous fallback used when no
// drainer is running (pre-init or post-shutdown). Logs failure
// without surfacing it — audit is observational.
func (m *MemoryForest) appendRetrievalAuditSync(ctx context.Context, event *RetrievalAuditEvent) {
	if _, err := m.AppendRetrievalAudit(ctx, event); err != nil {
		slog.Warn("forest_retrieval_audit_append_failed",
			"retrieval_id", event.ID,
			"session_id", event.SessionID,
			"err", err.Error(),
		)
	}
}

// startRetrievalAuditDrainer launches the tracked goroutine that
// consumes the audit queue and writes events to the ledger. One
// drainer per forest; registered on m.wg so Close() drains cleanly.
//
// Allocates the queue at start time so synchronous-projection
// callers don't pay for a queue they'll never use; emit falls back
// to sync inline write when retrievalAuditQueue is nil.
func (m *MemoryForest) startRetrievalAuditDrainer() {
	if m == nil {
		return
	}
	m.retrievalAuditQueue = make(chan *RetrievalAuditEvent, retrievalAuditQueueCapacity)
	m.registerRuntimeQueue("retrieval_audit", cap(m.retrievalAuditQueue))
	m.startWorker("retrieval_audit_drainer", retrievalAuditQueueCapacity, func(context.Context) error {
		m.runRetrievalAuditDrainerLoop()
		return nil
	})
}

// runRetrievalAuditDrainerLoop is the drainer's main loop. Pulls
// events from the queue and writes them; on shutdown, drains
// remaining queued items best-effort before returning.
func (m *MemoryForest) runRetrievalAuditDrainerLoop() {
	for {
		select {
		case event := <-m.retrievalAuditQueue:
			m.drainOneRetrievalAudit(event)
		case <-m.stopCh:
			m.drainPendingRetrievalAudits()
			return
		case <-m.runCtx.Done():
			m.drainPendingRetrievalAudits()
			return
		}
	}
}

// drainPendingRetrievalAudits flushes any audits currently in the
// queue, best-effort. Called on shutdown; uses context.Background
// so DB writes can complete even if runCtx is cancelled.
func (m *MemoryForest) drainPendingRetrievalAudits() {
	for {
		select {
		case event := <-m.retrievalAuditQueue:
			m.writeAuditWithBackground(event)
		default:
			return
		}
	}
}

func (m *MemoryForest) drainOneRetrievalAudit(event *RetrievalAuditEvent) {
	defer m.completeOneAudit()
	// Use Background, not runCtx. Loop-exit on shutdown is handled
	// by the drainer's select on stopCh; using runCtx here would
	// cancel an in-flight write the moment Close fires, dropping the
	// audit. The retrievalAuditDrainTimeout still bounds each write
	// so a stuck DB doesn't block the drainer indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), retrievalAuditDrainTimeout)
	defer cancel()
	if _, err := m.AppendRetrievalAudit(ctx, event); err != nil {
		slog.Warn("forest_retrieval_audit_append_failed",
			"retrieval_id", event.ID,
			"session_id", event.SessionID,
			"err", err.Error(),
		)
	}
}

func (m *MemoryForest) writeAuditWithBackground(event *RetrievalAuditEvent) {
	defer m.completeOneAudit()
	ctx, cancel := context.WithTimeout(context.Background(), retrievalAuditDrainTimeout)
	defer cancel()
	if _, err := m.AppendRetrievalAudit(ctx, event); err != nil {
		slog.Warn("forest_retrieval_audit_append_failed_shutdown",
			"retrieval_id", event.ID,
			"session_id", event.SessionID,
			"err", err.Error(),
		)
	}
}

// completeOneAudit decrements the in-flight counter and pings the
// idle channel non-blockingly so WaitForRetrievalAuditDrain can wake.
func (m *MemoryForest) completeOneAudit() {
	if m.retrievalAuditInFlight.Add(-1) <= 0 {
		m.signalAuditIdleIfDone()
	}
}

// signalAuditIdleIfDone non-blockingly notifies the idle channel.
// If a wake is already pending, drop ours.
func (m *MemoryForest) signalAuditIdleIfDone() {
	if m.retrievalAuditIdle == nil {
		return
	}
	select {
	case m.retrievalAuditIdle <- struct{}{}:
	default:
	}
}

// WaitForRetrievalAuditDrain blocks until every audit event currently
// in flight (queued or actively being written) has been processed.
// Used by tests and by callers that want strict read-after-emit
// semantics on the audit ledger.
//
// Returns nil when the in-flight count reaches zero, ctx.Err() when
// the context is cancelled, or a timeout error after the deadline.
func (m *MemoryForest) WaitForRetrievalAuditDrain(ctx context.Context, timeout time.Duration) error {
	if m == nil || m.retrievalAuditQueue == nil {
		return nil
	}
	deadline := time.Now().Add(timeout)
	for {
		if m.retrievalAuditInFlight.Load() <= 0 {
			return nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("timeout waiting for retrieval audit drain (in_flight=%d)",
				m.retrievalAuditInFlight.Load())
		}
		select {
		case <-m.retrievalAuditIdle:
			// Wake on idle signal; loop re-checks the counter.
		case <-time.After(remaining):
			return fmt.Errorf("timeout waiting for retrieval audit drain (in_flight=%d)",
				m.retrievalAuditInFlight.Load())
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// ────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
