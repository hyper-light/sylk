package forest

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"time"
)

// ────────────────────────────────────────────────────────────────────
// Issue #10 Phase B — Event archival worker
// ────────────────────────────────────────────────────────────────────
//
// Tracked goroutine that moves aged forest_events + forest_retrieval
// _events rows into compressed archive tables. The archive-conditional
// no-delete trigger (set up in ensureForestEventsAppendOnly) requires
// the row's id to exist in the archive before DELETE — this worker
// is the only path that satisfies that condition.
//
// Per cycle:
//   1. Acquire lease.
//   2. Archive forest_events (batch of N).
//   3. Archive forest_retrieval_events (batch of N).
//   4. Drain-then-sleep: full batch loops immediately; partial sleeps.
//
// Each archive cycle is one transaction:
//   BEGIN
//     INSERT OR IGNORE INTO archive (compressed) FROM hot WHERE old
//     UPSERT summary
//     DELETE FROM hot WHERE old (now allowed by trigger)
//   COMMIT
//
// All writes bounded by batch size; lease prevents multi-process
// double-archival; conditional trigger prevents data loss
// (DELETE only succeeds when archive insert succeeded).

const (
	// projectorEventArchivalName is the lease holder name.
	projectorEventArchivalName = "event_archival_worker"
)

// startEventArchivalWorker launches the tracked goroutine.
func (m *MemoryForest) startEventArchivalWorker() {
	if m == nil {
		return
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.runEventArchivalLoop()
	}()
}

// runEventArchivalLoop drives the archival worker via the canonical
// runMaintenanceCycle helper. The work callback processes BOTH
// hot tables (forest_events + forest_retrieval_events) per cycle
// and reports the larger count so the drain-then-sleep decision
// reflects whichever stream still has backlog.
func (m *MemoryForest) runEventArchivalLoop() {
	for m.runMaintenanceCycle(
		projectorEventArchivalName,
		m.eventArchiveInterval,
		m.eventArchiveBatchSize,
		m.runEventArchivalCallback,
	) {
	}
}

// runEventArchivalCallback runs one cycle of both archive streams
// (events + retrieval events) and returns the larger count so the
// loop's drain-vs-sleep decision is conservative — if either stream
// is saturated, the loop re-runs immediately. Each stream's errors
// are logged + recorded with its own slog event name + artifact.
func (m *MemoryForest) runEventArchivalCallback() int {
	eventsArchived, err := m.runEventArchiveCycle(m.runCtx)
	if err != nil {
		slog.Warn("forest_events_archive_failed", "err", err.Error())
		m.recordPrunerArtifact(projectorEventArchivalName, err)
	}
	retrievalArchived, err := m.runRetrievalEventArchiveCycle(m.runCtx)
	if err != nil {
		slog.Warn("forest_retrieval_events_archive_failed", "err", err.Error())
		m.recordPrunerArtifact(projectorEventArchivalName, err)
	}
	if eventsArchived > retrievalArchived {
		return eventsArchived
	}
	return retrievalArchived
}

// runEventArchiveCycle archives one batch of forest_events older
// than eventArchiveAge. Returns the count moved (regardless of
// per-row outcome). Single transaction wraps the archive-then-
// delete step so a partial failure rolls back; the conditional
// no-delete trigger guarantees the row never disappears without
// being archived first.
func (m *MemoryForest) runEventArchiveCycle(ctx context.Context) (int, error) {
	cutoff := timeNowSecondsMinus(m.eventArchiveAge)
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin events archive tx: %w", err)
	}
	defer tx.Rollback()

	candidates, err := loadEventArchiveCandidates(ctx, tx, cutoff, m.eventArchiveBatchSize)
	if err != nil {
		return 0, err
	}
	if len(candidates) == 0 {
		return 0, nil
	}
	if err := insertEventsToArchive(ctx, tx, candidates); err != nil {
		return 0, err
	}
	if err := upsertEventsArchiveSummary(ctx, tx, candidates); err != nil {
		return 0, err
	}
	if err := deleteArchivedEvents(ctx, tx, candidates); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit events archive tx: %w", err)
	}
	return len(candidates), nil
}

// archivableEvent is the in-flight projection of a forest_events row
// during archival. payloadCompressed is computed from payload before
// the archive INSERT.
type archivableEvent struct {
	id               string
	sessionID        string
	taskID           sql.NullString
	agentID          sql.NullString
	agentType        sql.NullString
	eventType        string
	family           string
	scope            string
	rootID           string
	branchID         string
	parentBranchID   sql.NullString
	intentID         sql.NullString
	contentID        sql.NullString
	sourceID         sql.NullString
	confidence       float64
	salience         float64
	timestamp        int64
	title            sql.NullString
	summary          sql.NullString
	provenanceRefs   sql.NullString
	supersedes       sql.NullString
	contradicts      sql.NullString
	relatedBranches  sql.NullString
	payload          sql.NullString
}

// loadEventArchiveCandidates selects up to `limit` events older than
// the cutoff. Sorted by timestamp ASC so the oldest get archived
// first (predictable progress under sustained backlog).
func loadEventArchiveCandidates(ctx context.Context, tx *sql.Tx, cutoff int64, limit int) ([]archivableEvent, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, session_id, task_id, agent_id, agent_type,
		       event_type, family, scope, root_id, branch_id,
		       parent_branch_id, intent_id, content_id, source_id,
		       confidence, salience, timestamp, title, summary,
		       provenance_refs, supersedes, contradicts,
		       related_branch_ids, payload
		FROM   forest_events
		WHERE  timestamp < ?
		ORDER BY timestamp ASC
		LIMIT  ?
	`, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("query archive candidates: %w", err)
	}
	defer rows.Close()
	out := make([]archivableEvent, 0, limit)
	for rows.Next() {
		var e archivableEvent
		if err := rows.Scan(
			&e.id, &e.sessionID, &e.taskID, &e.agentID, &e.agentType,
			&e.eventType, &e.family, &e.scope, &e.rootID, &e.branchID,
			&e.parentBranchID, &e.intentID, &e.contentID, &e.sourceID,
			&e.confidence, &e.salience, &e.timestamp, &e.title, &e.summary,
			&e.provenanceRefs, &e.supersedes, &e.contradicts,
			&e.relatedBranches, &e.payload,
		); err != nil {
			return nil, fmt.Errorf("scan archive candidate: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// insertEventsToArchive INSERTs the candidates into the archive.
// payload is gzip-compressed. Uses INSERT OR IGNORE so a re-attempt
// of an already-archived id is a no-op (the no-update trigger on
// the archive table would otherwise reject any rewrite).
func insertEventsToArchive(ctx context.Context, tx *sql.Tx, candidates []archivableEvent) error {
	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO forest_events_archive
		(id, session_id, task_id, agent_id, agent_type,
		 event_type, family, scope, root_id, branch_id,
		 parent_branch_id, intent_id, content_id, source_id,
		 confidence, salience, timestamp, title, summary,
		 provenance_refs, supersedes, contradicts,
		 related_branch_ids, payload_compressed, archived_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare archive insert: %w", err)
	}
	defer stmt.Close()

	now := time.Now().UTC().Unix()
	for k := range candidates {
		c := &candidates[k]
		compressed, err := gzipPayloadString(c.payload)
		if err != nil {
			return fmt.Errorf("compress payload for %s: %w", c.id, err)
		}
		if _, err := stmt.ExecContext(ctx,
			c.id, c.sessionID, c.taskID, c.agentID, c.agentType,
			c.eventType, c.family, c.scope, c.rootID, c.branchID,
			c.parentBranchID, c.intentID, c.contentID, c.sourceID,
			c.confidence, c.salience, c.timestamp, c.title, c.summary,
			c.provenanceRefs, c.supersedes, c.contradicts,
			c.relatedBranches, compressed, now,
		); err != nil {
			return fmt.Errorf("insert archive row %s: %w", c.id, err)
		}
	}
	return nil
}

// upsertEventsArchiveSummary increments the per-(branch,event_type)
// summary row for each archived event. UPSERT semantics: insert
// fresh row with count=1 + first/last_seen=ts, or UPDATE existing
// row to bump count + extend the seen window.
func upsertEventsArchiveSummary(ctx context.Context, tx *sql.Tx, candidates []archivableEvent) error {
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO forest_events_archive_summary
		(branch_id, event_type, count, first_seen, last_seen)
		VALUES (?, ?, 1, ?, ?)
		ON CONFLICT(branch_id, event_type) DO UPDATE SET
			count       = count + 1,
			first_seen  = MIN(first_seen, excluded.first_seen),
			last_seen   = MAX(last_seen, excluded.last_seen)
	`)
	if err != nil {
		return fmt.Errorf("prepare archive summary upsert: %w", err)
	}
	defer stmt.Close()

	for k := range candidates {
		c := &candidates[k]
		if _, err := stmt.ExecContext(ctx,
			c.branchID, c.eventType, c.timestamp, c.timestamp,
		); err != nil {
			return fmt.Errorf("upsert archive summary for %s/%s: %w", c.branchID, c.eventType, err)
		}
	}
	return nil
}

// deleteArchivedEvents removes the now-archived rows from the hot
// table. Each row's id is in forest_events_archive (we just inserted
// them this transaction), so the conditional no-delete trigger
// permits the DELETE.
func deleteArchivedEvents(ctx context.Context, tx *sql.Tx, candidates []archivableEvent) error {
	stmt, err := tx.PrepareContext(ctx, `DELETE FROM forest_events WHERE id = ?`)
	if err != nil {
		return fmt.Errorf("prepare archive delete: %w", err)
	}
	defer stmt.Close()
	for k := range candidates {
		if _, err := stmt.ExecContext(ctx, candidates[k].id); err != nil {
			return fmt.Errorf("delete archived event %s: %w", candidates[k].id, err)
		}
	}
	return nil
}

// ────────────────────────────────────────────────────────────────────
// Retrieval-events archive — same shape, separate table
// ────────────────────────────────────────────────────────────────────

func (m *MemoryForest) runRetrievalEventArchiveCycle(ctx context.Context) (int, error) {
	cutoff := timeNowSecondsMinus(m.retrievalEventArchiveAge)
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin retrieval events archive tx: %w", err)
	}
	defer tx.Rollback()

	candidates, err := loadRetrievalEventArchiveCandidates(ctx, tx, cutoff, m.eventArchiveBatchSize)
	if err != nil {
		return 0, err
	}
	if len(candidates) == 0 {
		return 0, nil
	}
	if err := insertRetrievalEventsToArchive(ctx, tx, candidates); err != nil {
		return 0, err
	}
	if err := upsertRetrievalEventsArchiveSummary(ctx, tx, candidates); err != nil {
		return 0, err
	}
	if err := deleteArchivedRetrievalEvents(ctx, tx, candidates); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit retrieval events archive tx: %w", err)
	}
	return len(candidates), nil
}

type archivableRetrievalEvent struct {
	id                     string
	sessionID              string
	taskID                 sql.NullString
	agentID                sql.NullString
	agentType              sql.NullString
	intentID               sql.NullString
	query                  string
	horizon                sql.NullString
	familiesBlob           sql.NullString
	requestedLimit         int64
	includeCounterEvidence int64
	requestedAt            int64
	durationMicros         int64
	candidateCount         int64
	returnedCount          int64
	modelKey               sql.NullString
	modelVersion           int64
	errorMessage           sql.NullString
	branchProjectionSeq    int64
	candidatesBlob         sql.NullString
	metadataBlob           sql.NullString
	explorationMode        int64
	substrateMode          string
	baseScoreVersion       int64
	baseScoreVariant       string
	hyperparamSnapshotID   int64
	proposedHyperparams    int64
}

func loadRetrievalEventArchiveCandidates(ctx context.Context, tx *sql.Tx, cutoff int64, limit int) ([]archivableRetrievalEvent, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, session_id, task_id, agent_id, agent_type, intent_id,
		       query, horizon, families_blob, requested_limit,
		       include_counter_evidence, requested_at, duration_micros,
		       candidate_count, returned_count, model_key, model_version,
		       error_message, branch_projection_seq, candidates_blob,
		       metadata_blob, exploration_mode, substrate_mode,
		       base_score_version, base_score_variant,
		       hyperparam_snapshot_id, proposed_hyperparams
		FROM   forest_retrieval_events
		WHERE  requested_at < ?
		ORDER BY requested_at ASC
		LIMIT  ?
	`, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("query retrieval archive candidates: %w", err)
	}
	defer rows.Close()
	out := make([]archivableRetrievalEvent, 0, limit)
	for rows.Next() {
		var r archivableRetrievalEvent
		if err := rows.Scan(
			&r.id, &r.sessionID, &r.taskID, &r.agentID, &r.agentType, &r.intentID,
			&r.query, &r.horizon, &r.familiesBlob, &r.requestedLimit,
			&r.includeCounterEvidence, &r.requestedAt, &r.durationMicros,
			&r.candidateCount, &r.returnedCount, &r.modelKey, &r.modelVersion,
			&r.errorMessage, &r.branchProjectionSeq, &r.candidatesBlob,
			&r.metadataBlob, &r.explorationMode, &r.substrateMode,
			&r.baseScoreVersion, &r.baseScoreVariant,
			&r.hyperparamSnapshotID, &r.proposedHyperparams,
		); err != nil {
			return nil, fmt.Errorf("scan retrieval archive candidate: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func insertRetrievalEventsToArchive(ctx context.Context, tx *sql.Tx, candidates []archivableRetrievalEvent) error {
	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO forest_retrieval_events_archive
		(id, session_id, task_id, agent_id, agent_type, intent_id,
		 query, horizon, families_blob, requested_limit,
		 include_counter_evidence, requested_at, duration_micros,
		 candidate_count, returned_count, model_key, model_version,
		 error_message, branch_projection_seq, candidates_compressed,
		 metadata_compressed, exploration_mode, substrate_mode,
		 base_score_version, base_score_variant,
		 hyperparam_snapshot_id, proposed_hyperparams, archived_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare retrieval archive insert: %w", err)
	}
	defer stmt.Close()
	now := time.Now().UTC().Unix()
	for k := range candidates {
		r := &candidates[k]
		compressedCands, err := gzipPayloadString(r.candidatesBlob)
		if err != nil {
			return fmt.Errorf("compress candidates blob for %s: %w", r.id, err)
		}
		compressedMeta, err := gzipPayloadString(r.metadataBlob)
		if err != nil {
			return fmt.Errorf("compress metadata blob for %s: %w", r.id, err)
		}
		if _, err := stmt.ExecContext(ctx,
			r.id, r.sessionID, r.taskID, r.agentID, r.agentType, r.intentID,
			r.query, r.horizon, r.familiesBlob, r.requestedLimit,
			r.includeCounterEvidence, r.requestedAt, r.durationMicros,
			r.candidateCount, r.returnedCount, r.modelKey, r.modelVersion,
			r.errorMessage, r.branchProjectionSeq, compressedCands,
			compressedMeta, r.explorationMode, r.substrateMode,
			r.baseScoreVersion, r.baseScoreVariant,
			r.hyperparamSnapshotID, r.proposedHyperparams, now,
		); err != nil {
			return fmt.Errorf("insert retrieval archive row %s: %w", r.id, err)
		}
	}
	return nil
}

func upsertRetrievalEventsArchiveSummary(ctx context.Context, tx *sql.Tx, candidates []archivableRetrievalEvent) error {
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO forest_retrieval_events_archive_summary
		(session_id, count, first_seen, last_seen)
		VALUES (?, 1, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			count      = count + 1,
			first_seen = MIN(first_seen, excluded.first_seen),
			last_seen  = MAX(last_seen, excluded.last_seen)
	`)
	if err != nil {
		return fmt.Errorf("prepare retrieval summary upsert: %w", err)
	}
	defer stmt.Close()
	for k := range candidates {
		r := &candidates[k]
		if _, err := stmt.ExecContext(ctx, r.sessionID, r.requestedAt, r.requestedAt); err != nil {
			return fmt.Errorf("upsert retrieval summary for %s: %w", r.sessionID, err)
		}
	}
	return nil
}

func deleteArchivedRetrievalEvents(ctx context.Context, tx *sql.Tx, candidates []archivableRetrievalEvent) error {
	stmt, err := tx.PrepareContext(ctx, `DELETE FROM forest_retrieval_events WHERE id = ?`)
	if err != nil {
		return fmt.Errorf("prepare retrieval archive delete: %w", err)
	}
	defer stmt.Close()
	for k := range candidates {
		if _, err := stmt.ExecContext(ctx, candidates[k].id); err != nil {
			return fmt.Errorf("delete archived retrieval event %s: %w", candidates[k].id, err)
		}
	}
	return nil
}

// ────────────────────────────────────────────────────────────────────
// Compression helpers
// ────────────────────────────────────────────────────────────────────

// gzipPayloadString compresses a NullString payload to gzip bytes.
// Returns nil for invalid (NULL) inputs and empty []byte for empty
// strings (the empty gzip stream is a few bytes, not zero, so we
// short-circuit empty input). Bounds-checked, no panic.
func gzipPayloadString(s sql.NullString) ([]byte, error) {
	if !s.Valid || s.String == "" {
		return nil, nil
	}
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write([]byte(s.String)); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("gzip write: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("gzip close: %w", err)
	}
	return buf.Bytes(), nil
}

// gunzipPayloadBytes is the inverse of gzipPayloadString — reads a
// gzip blob from the archive and returns the original payload as a
// string. Empty / nil input returns ("", nil).
func gunzipPayloadBytes(b []byte) (string, error) {
	if len(b) == 0 {
		return "", nil
	}
	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return "", fmt.Errorf("gzip new reader: %w", err)
	}
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("gzip read all: %w", err)
	}
	return string(out), nil
}
