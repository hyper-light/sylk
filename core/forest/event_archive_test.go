package forest

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

// ────────────────────────────────────────────────────────────────────
// Issue #10 Phase B — archive worker tests
// ────────────────────────────────────────────────────────────────────

func TestArchive_GzipPayloadRoundTrip(t *testing.T) {
	original := `{"status":"succeeded","details":"a fairly long payload " + repeated content here}`
	compressed, err := gzipPayloadString(sql.NullString{String: original, Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(compressed) == 0 {
		t.Fatal("compressed should be non-empty")
	}
	decoded, err := gunzipPayloadBytes(compressed)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != original {
		t.Errorf("round-trip mismatch: got %q want %q", decoded, original)
	}
}

func TestArchive_GzipNullPayloadReturnsNil(t *testing.T) {
	compressed, err := gzipPayloadString(sql.NullString{Valid: false})
	if err != nil {
		t.Fatal(err)
	}
	if compressed != nil {
		t.Errorf("null input should produce nil bytes; got %v", compressed)
	}
	// Empty string also produces nil.
	compressed, err = gzipPayloadString(sql.NullString{String: "", Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	if compressed != nil {
		t.Errorf("empty string should produce nil bytes; got %v", compressed)
	}
}

func TestArchive_GunzipEmptyReturnsEmpty(t *testing.T) {
	out, err := gunzipPayloadBytes(nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Errorf("nil input: got %q want empty", out)
	}
	out, err = gunzipPayloadBytes([]byte{})
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Errorf("empty input: got %q want empty", out)
	}
}

func TestArchive_GunzipMalformedRejects(t *testing.T) {
	if _, err := gunzipPayloadBytes([]byte{1, 2, 3, 4}); err == nil {
		t.Error("malformed gzip should error")
	}
}

func TestArchive_AppendOnlyTriggerAllowsArchivedDelete(t *testing.T) {
	_, db := newTestForest(t)

	// Insert an event into hot.
	now := time.Now().UTC().Unix()
	_, err := db.Exec(`
		INSERT INTO forest_events
		(id, session_id, event_type, family, scope, root_id, branch_id,
		 confidence, salience, timestamp, title)
		VALUES ('e1', 's', ?, ?, 'episodic', 'r', 'b', 0.5, 0.5, ?, 't')
	`, string(EventTypeDecisionRecorded), string(TreeFamilyDecision), now)
	if err != nil {
		t.Fatal(err)
	}

	// DELETE without prior archival → trigger rejects.
	_, err = db.Exec(`DELETE FROM forest_events WHERE id = 'e1'`)
	if err == nil {
		t.Error("DELETE on un-archived row should be rejected by trigger")
	}

	// Insert into archive first.
	_, err = db.Exec(`
		INSERT INTO forest_events_archive
		(id, session_id, event_type, family, scope, root_id, branch_id,
		 confidence, salience, timestamp, archived_at)
		VALUES ('e1', 's', ?, ?, 'episodic', 'r', 'b', 0.5, 0.5, ?, ?)
	`, string(EventTypeDecisionRecorded), string(TreeFamilyDecision), now, now)
	if err != nil {
		t.Fatal(err)
	}

	// DELETE now succeeds.
	_, err = db.Exec(`DELETE FROM forest_events WHERE id = 'e1'`)
	if err != nil {
		t.Errorf("DELETE on archived row should succeed; got %v", err)
	}

	var count int
	db.QueryRow(`SELECT COUNT(*) FROM forest_events WHERE id = 'e1'`).Scan(&count)
	if count != 0 {
		t.Errorf("hot row should be gone; got count=%d", count)
	}
}

func TestArchive_NoUpdateTriggerStillEnforced(t *testing.T) {
	_, db := newTestForest(t)

	// Seed an event.
	_, err := db.Exec(`
		INSERT INTO forest_events
		(id, session_id, event_type, family, scope, root_id, branch_id,
		 confidence, salience, timestamp, title)
		VALUES ('e2', 's', ?, ?, 'episodic', 'r', 'b', 0.5, 0.5, ?, 't')
	`, string(EventTypeDecisionRecorded), string(TreeFamilyDecision), time.Now().UTC().Unix())
	if err != nil {
		t.Fatal(err)
	}

	// UPDATE rejected regardless of archival state.
	_, err = db.Exec(`UPDATE forest_events SET title = 'changed' WHERE id = 'e2'`)
	if err == nil {
		t.Error("UPDATE should always be rejected by no-update trigger")
	}
	if !strings.Contains(err.Error(), "append-only") {
		t.Errorf("expected append-only error; got %v", err)
	}
}

func TestArchive_RoundTripMovesEventsToArchive(t *testing.T) {
	forest, db := newTestForest(t)
	ctx := context.Background()
	forest.eventArchiveAge = 1 * time.Second
	forest.eventArchiveBatchSize = 32

	// Seed two old events + one recent event.
	old := time.Now().UTC().Add(-time.Hour).Unix()
	recent := time.Now().UTC().Unix()
	_, err := db.Exec(`
		INSERT INTO forest_events (id, session_id, event_type, family, scope, root_id, branch_id,
		 confidence, salience, timestamp, title, payload)
		VALUES ('old1', 's', ?, ?, 'episodic', 'r', 'b1', 0.5, 0.5, ?, 't', '{"k":"v"}')
	`, string(EventTypeDecisionRecorded), string(TreeFamilyDecision), old)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		INSERT INTO forest_events (id, session_id, event_type, family, scope, root_id, branch_id,
		 confidence, salience, timestamp, title, payload)
		VALUES ('old2', 's', ?, ?, 'episodic', 'r', 'b1', 0.5, 0.5, ?, 't', '{"k":"v2"}')
	`, string(EventTypeOutcomeRecorded), string(TreeFamilyOutcome), old)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		INSERT INTO forest_events (id, session_id, event_type, family, scope, root_id, branch_id,
		 confidence, salience, timestamp, title, payload)
		VALUES ('recent', 's', ?, ?, 'episodic', 'r', 'b1', 0.5, 0.5, ?, 't', '{}')
	`, string(EventTypeDecisionRecorded), string(TreeFamilyDecision), recent)
	if err != nil {
		t.Fatal(err)
	}

	archived, err := forest.runEventArchiveCycle(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if archived != 2 {
		t.Errorf("expected to archive 2 events; got %d", archived)
	}

	// Old events gone from hot.
	var hot int
	db.QueryRow(`SELECT COUNT(*) FROM forest_events WHERE id IN ('old1', 'old2')`).Scan(&hot)
	if hot != 0 {
		t.Errorf("hot should be empty for old events; got %d", hot)
	}
	// Old events in archive.
	var arc int
	db.QueryRow(`SELECT COUNT(*) FROM forest_events_archive WHERE id IN ('old1', 'old2')`).Scan(&arc)
	if arc != 2 {
		t.Errorf("archive should have 2; got %d", arc)
	}
	// Summary populated.
	var sumCount int
	db.QueryRow(`SELECT COUNT(*) FROM forest_events_archive_summary WHERE branch_id = 'b1'`).Scan(&sumCount)
	if sumCount != 2 {
		t.Errorf("summary rows for branch b1 (2 event types): got %d want 2", sumCount)
	}
	// Recent event still in hot.
	var recentInHot int
	db.QueryRow(`SELECT COUNT(*) FROM forest_events WHERE id = 'recent'`).Scan(&recentInHot)
	if recentInHot != 1 {
		t.Errorf("recent event should remain in hot; got %d", recentInHot)
	}
}

func TestArchive_PayloadRoundTripsThroughCompression(t *testing.T) {
	forest, db := newTestForest(t)
	ctx := context.Background()
	forest.eventArchiveAge = 1 * time.Second
	forest.eventArchiveBatchSize = 32

	original := `{"status":"succeeded","summary":"detailed payload here"}`
	old := time.Now().UTC().Add(-time.Hour).Unix()
	_, err := db.Exec(`
		INSERT INTO forest_events (id, session_id, event_type, family, scope, root_id, branch_id,
		 confidence, salience, timestamp, title, payload)
		VALUES ('orig', 's', ?, ?, 'episodic', 'r', 'b', 0.5, 0.5, ?, 't', ?)
	`, string(EventTypeOutcomeRecorded), string(TreeFamilyOutcome), old, original)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := forest.runEventArchiveCycle(ctx); err != nil {
		t.Fatal(err)
	}

	var compressed []byte
	if err := db.QueryRow(`SELECT payload_compressed FROM forest_events_archive WHERE id = 'orig'`).Scan(&compressed); err != nil {
		t.Fatal(err)
	}
	decoded, err := gunzipPayloadBytes(compressed)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != original {
		t.Errorf("payload round-trip: got %q want %q", decoded, original)
	}
}

func TestArchive_RetrievalEventsArchiveCycle(t *testing.T) {
	forest, db := newTestForest(t)
	ctx := context.Background()
	forest.retrievalEventArchiveAge = 1 * time.Second
	forest.eventArchiveBatchSize = 32

	old := time.Now().UTC().Add(-time.Hour).Unix()
	_, err := db.Exec(`
		INSERT INTO forest_retrieval_events
		(id, session_id, query, requested_at, requested_limit,
		 candidate_count, returned_count, model_version,
		 branch_projection_seq, candidates_blob,
		 include_counter_evidence, duration_micros, exploration_mode,
		 substrate_mode, base_score_version, base_score_variant)
		VALUES ('rev1', 's', 'q', ?, 1, 1, 1, 0, 0, '[]', 0, 0, 0, '', 0, '')
	`, old)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		INSERT INTO forest_retrieval_event_seq_log (event_id, appended_at)
		VALUES ('rev1', ?)
	`, old)
	if err != nil {
		t.Fatal(err)
	}

	archived, err := forest.runRetrievalEventArchiveCycle(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if archived != 1 {
		t.Errorf("expected to archive 1 retrieval event; got %d", archived)
	}

	var count int
	db.QueryRow(`SELECT COUNT(*) FROM forest_retrieval_events_archive WHERE id = 'rev1'`).Scan(&count)
	if count != 1 {
		t.Errorf("retrieval archive missing rev1: count=%d", count)
	}
}
