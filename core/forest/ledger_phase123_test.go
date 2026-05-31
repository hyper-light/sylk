package forest

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	claimsmocks "github.com/adalundhe/sylk/core/claims/mocks"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/mock"
)

func TestPhase123SchemaMetaAndAudit(t *testing.T) {
	forest, db := newTestForest(t)
	defer forest.Close()

	var version int
	var projection string
	if err := db.QueryRow(`SELECT schema_version, projection_version FROM forest_schema_meta WHERE meta_key = 'active'`).Scan(&version, &projection); err != nil {
		t.Fatalf("schema meta: %v", err)
	}
	if version != forestSchemaVersionPhase123 || projection != forestProjectionVersionPhase123 {
		t.Fatalf("schema meta = version %d projection %q", version, projection)
	}
	if !prohibitedSQLiteObjectSQL(`CREATE VIRTUAL TABLE bad USING fts5(content)`) {
		t.Fatal("fts5 virtual table was not prohibited")
	}
	if prohibitedSQLiteObjectSQL(`CREATE TABLE ok (id TEXT PRIMARY KEY)`) {
		t.Fatal("ordinary table prohibited")
	}
}

func TestAppendLedgerRecordIdempotentAndImmutable(t *testing.T) {
	forest, db := newTestForest(t)
	defer forest.Close()

	record := LedgerRecord{
		SourceKind:  LedgerSourceMaintenance,
		SourceID:    "source-1",
		SourceKey:   "maintenance:source-1",
		EventKind:   "maintenance.checked",
		SessionID:   "session-1",
		SubjectType: "maintenance",
		SubjectID:   "source-1",
		OccurredAt:  time.Unix(10, 0),
		Payload:     map[string]any{"ok": true},
		Refs:        []claims.DeltaRef{{Role: "claim", Type: claims.RelatedTypeClaim, ID: "claim-1"}},
	}
	first, err := forest.AppendLedgerRecord(context.Background(), record)
	if err != nil {
		t.Fatalf("append ledger first: %v", err)
	}
	second, err := forest.AppendLedgerRecord(context.Background(), record)
	if err != nil {
		t.Fatalf("append ledger duplicate: %v", err)
	}
	if !first.Inserted || second.Inserted || first.Seq != second.Seq || first.ID != second.ID {
		t.Fatalf("idempotency mismatch: first=%+v second=%+v", first, second)
	}
	var refs int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_ledger_refs WHERE ledger_id = ?`, first.ID).Scan(&refs); err != nil {
		t.Fatalf("count refs: %v", err)
	}
	if refs != 1 {
		t.Fatalf("refs = %d, want 1", refs)
	}
	if _, err := db.Exec(`UPDATE forest_ledger SET event_kind = 'changed' WHERE id = ?`, first.ID); err == nil {
		t.Fatal("forest_ledger update succeeded")
	}
}

func TestAppendEventUsesCanonicalLedgerWithoutForestEventsWrite(t *testing.T) {
	forest, db := newTestForest(t)
	defer forest.Close()

	event := &Event{
		ID:        "event-ledger-only",
		SessionID: "session-ledger-only",
		BranchID:  "branch-ledger-only",
		AgentID:   "engineer-1",
		AgentType: "engineer",
		EventType: EventTypeDecisionRecorded,
		Family:    TreeFamilyDecision,
		Title:     "ledger backed branch event",
		Summary:   "branch compatibility is projected from forest_ledger",
		Timestamp: time.Unix(30, 0),
	}
	if err := forest.AppendEvent(context.Background(), event); err != nil {
		t.Fatalf("append event: %v", err)
	}
	if event.Seq <= 0 {
		t.Fatalf("event seq = %d, want > 0", event.Seq)
	}
	if err := forest.AppendEvent(context.Background(), event); err != nil {
		t.Fatalf("append duplicate event: %v", err)
	}

	var ledgerRows int
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM forest_ledger
		WHERE source_kind = ? AND source_key = ?
	`, string(LedgerSourceForestEvent), "forest_event:event-ledger-only").Scan(&ledgerRows); err != nil {
		t.Fatalf("count canonical ledger rows: %v", err)
	}
	if ledgerRows != 1 {
		t.Fatalf("ledger rows = %d, want 1", ledgerRows)
	}
	var legacyRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_events WHERE id = ?`, event.ID).Scan(&legacyRows); err != nil {
		t.Fatalf("count legacy event rows: %v", err)
	}
	if legacyRows != 0 {
		t.Fatalf("forest_events rows = %d, want 0", legacyRows)
	}
	var supportCount int
	if err := db.QueryRow(`SELECT support_count FROM forest_branches WHERE id = ?`, event.BranchID).Scan(&supportCount); err != nil {
		t.Fatalf("load projected branch: %v", err)
	}
	if supportCount != 1 {
		t.Fatalf("support_count = %d, want duplicate-safe 1", supportCount)
	}
}

func TestAppendLedgerRecordRejectsMalformed(t *testing.T) {
	forest, _ := newTestForest(t)
	defer forest.Close()

	cases := []LedgerRecord{
		{SourceID: "s", SourceKey: "k", EventKind: "e", SessionID: "session"},
		{SourceKind: LedgerSourceMaintenance, SourceID: "s", EventKind: "e", SessionID: "session"},
		{SourceKind: LedgerSourceMaintenance, SourceID: "s", SourceKey: "k", SessionID: "session"},
		{SourceKind: LedgerSourceMaintenance, SourceID: "s", SourceKey: "k", EventKind: "e"},
		{SourceKind: LedgerSourceClaimsDelta, SourceID: "s", SourceKey: "k", EventKind: "e", SessionID: "session"},
	}
	for idx, record := range cases {
		if _, err := forest.AppendLedgerRecord(context.Background(), record); err == nil {
			t.Fatalf("case %d accepted malformed record", idx)
		}
	}
}

func TestAppendCanonicalDeltaProjectsArtifactEvidence(t *testing.T) {
	forest, db := newTestForest(t)
	defer forest.Close()

	delta := claims.NewCanonicalDelta(
		claims.DeltaActionArtifactGenerated,
		"session-1",
		"board-1",
		7,
		time.Unix(20, 0),
		claims.DegradedAgentRef("engineer", "test"),
		[]claims.DeltaRef{
			{Role: "claim", Type: claims.RelatedTypeClaim, ID: "claim-1"},
			{Role: "testament", Type: claims.RelatedTypeTestament, ID: "testament-1"},
			{Role: "artifact", Type: claims.RelatedTypeArtifact, ID: "artifact-1"},
		},
		nil,
		map[string]any{
			"claim":     map[string]any{"id": "claim-1", "action": "task"},
			"testament": map[string]any{"id": "testament-1"},
			"artifact": map[string]any{
				"id":           "artifact-1",
				"name":         "plan",
				"kind":         "plan_markdown",
				"data_type":    "markdown",
				"content_hash": "sha256:abc",
				"status":       string(claims.ArtifactStatusGenerated),
			},
		},
	)
	result, err := forest.AppendCanonicalDelta(context.Background(), delta)
	if err != nil {
		t.Fatalf("append canonical delta: %v", err)
	}
	if !result.Inserted {
		t.Fatal("first canonical delta was not inserted")
	}
	var claimID, kind, status string
	if err := db.QueryRow(`SELECT claim_id, artifact_kind, status FROM forest_artifacts WHERE artifact_id = 'artifact-1'`).Scan(&claimID, &kind, &status); err != nil {
		t.Fatalf("load artifact evidence: %v", err)
	}
	if claimID != "claim-1" || kind != "plan_markdown" || status != string(claims.ArtifactStatusGenerated) {
		t.Fatalf("artifact evidence claim=%q kind=%q status=%q", claimID, kind, status)
	}
}

func TestAppendCanonicalDeltaProjectsValidationEvidence(t *testing.T) {
	forest, db := newTestForest(t)
	defer forest.Close()

	artifactDelta := claims.NewCanonicalDelta(
		claims.DeltaActionArtifactGenerated,
		"session-1", "board-1", 8, time.Unix(21, 0),
		claims.DegradedAgentRef("engineer", "test"),
		[]claims.DeltaRef{{Role: "claim", Type: claims.RelatedTypeClaim, ID: "claim-1"}, {Role: "artifact", Type: claims.RelatedTypeArtifact, ID: "artifact-1"}},
		nil,
		map[string]any{"artifact": map[string]any{"id": "artifact-1", "kind": "plan_markdown", "status": string(claims.ArtifactStatusGenerated), "content_hash": "sha256:abc"}},
	)
	if _, err := forest.AppendCanonicalDelta(context.Background(), artifactDelta); err != nil {
		t.Fatalf("append artifact: %v", err)
	}
	validationDelta := claims.NewCanonicalDelta(
		claims.DeltaActionValidationValidated,
		"session-1",
		"board-1",
		9,
		time.Unix(22, 0),
		claims.DegradedAgentRef("tester", "test"),
		[]claims.DeltaRef{
			{Role: "claim", Type: claims.RelatedTypeClaim, ID: "claim-1"},
			{Role: "validation", Type: claims.RelatedTypeValidation, ID: "validation-1"},
			{Role: "artifact", Type: claims.RelatedTypeArtifact, ID: "artifact-1"},
		},
		nil,
		map[string]any{
			"claim": map[string]any{"id": "claim-1", "action": "task"},
			"validation": map[string]any{
				"id":                 "validation-1",
				"type":               "programmatic",
				"status":             string(claims.ValidationStatusValidated),
				"required":           true,
				"target_artifact_id": "artifact-1",
				"result_artifact_id": "artifact-result-1",
			},
		},
	)
	if _, err := forest.AppendCanonicalDelta(context.Background(), validationDelta); err != nil {
		t.Fatalf("append validation: %v", err)
	}
	var claimID, targetArtifactID, status string
	var required int
	if err := db.QueryRow(`SELECT claim_id, target_artifact_id, status, required FROM forest_validations WHERE validation_id = 'validation-1'`).Scan(&claimID, &targetArtifactID, &status, &required); err != nil {
		t.Fatalf("load validation evidence: %v", err)
	}
	if claimID != "claim-1" || targetArtifactID != "artifact-1" || status != string(claims.ValidationStatusValidated) || required != 1 {
		t.Fatalf("validation evidence claim=%q target=%q status=%q required=%d", claimID, targetArtifactID, status, required)
	}
	var patterns int
	if err := db.QueryRow(`SELECT success_count FROM forest_validation_patterns WHERE validation_type = 'programmatic'`).Scan(&patterns); err != nil {
		t.Fatalf("load validation pattern: %v", err)
	}
	if patterns != 1 {
		t.Fatalf("validation pattern success_count = %d", patterns)
	}
}

func TestAppendCanonicalDeltaRecordsValidationEvidenceErrorForMissingTargetArtifact(t *testing.T) {
	forest, db := newTestForest(t)
	defer forest.Close()

	delta := claims.NewCanonicalDelta(
		claims.DeltaActionValidationValidated,
		"session-1",
		"board-1",
		11,
		time.Unix(24, 0),
		claims.DegradedAgentRef("tester", "test"),
		[]claims.DeltaRef{
			{Role: "claim", Type: claims.RelatedTypeClaim, ID: "claim-1"},
			{Role: "validation", Type: claims.RelatedTypeValidation, ID: "validation-missing-target"},
		},
		nil,
		map[string]any{
			"claim": map[string]any{"id": "claim-1", "action": "task"},
			"validation": map[string]any{
				"id":       "validation-missing-target",
				"type":     "programmatic",
				"status":   string(claims.ValidationStatusValidated),
				"required": true,
			},
		},
	)
	if _, err := forest.AppendCanonicalDelta(context.Background(), delta); err != nil {
		t.Fatalf("append validation without target: %v", err)
	}
	var errorKind string
	if err := db.QueryRow(`
		SELECT error_kind
		FROM forest_evidence_errors
		WHERE entity_type = ? AND entity_id = ?
	`, claims.RelatedTypeValidation, "validation-missing-target").Scan(&errorKind); err != nil {
		t.Fatalf("load evidence error: %v", err)
	}
	if errorKind != "missing_target_artifact" {
		t.Fatalf("error kind = %q", errorKind)
	}
}

func TestDeltaIngestorUsesMockerySubscriberAndDedupes(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:"+stableID("delta-ingestor", t.Name())+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE nodes (id TEXT PRIMARY KEY, domain INTEGER NOT NULL, node_type INTEGER NOT NULL, name TEXT NOT NULL)`); err != nil {
		t.Fatalf("seed nodes: %v", err)
	}
	subscription, _ := claims.NoopDeltaBus{}.SubscribeDelta("unused", nil)
	var handler claims.DeltaHandler
	subscriber := claimsmocks.NewDeltaSubscriber(t)
	subscriber.EXPECT().
		SubscribeDelta(claims.CanonicalSessionPattern("session-1"), mock.Anything).
		Run(func(_ string, h claims.DeltaHandler) { handler = h }).
		Return(subscription, nil).
		Once()
	forest, err := New(Config{DB: db, SynchronousProjection: true, ClaimsDeltaSubscriber: subscriber, ClaimsDeltaSessionFilter: "session-1", DeltaIngestQueueCapacity: 4})
	if err != nil {
		t.Fatalf("new forest: %v", err)
	}
	defer forest.Close()
	delta := claims.NewCanonicalDelta(
		claims.DeltaActionArtifactGenerated,
		"session-1", "board-1", 10, time.Unix(23, 0),
		claims.DegradedAgentRef("engineer", "test"),
		[]claims.DeltaRef{{Role: "artifact", Type: claims.RelatedTypeArtifact, ID: "artifact-1"}},
		nil,
		map[string]any{"artifact": map[string]any{"id": "artifact-1", "kind": "diagnostic", "status": string(claims.ArtifactStatusGenerated), "content_hash": "sha256:def"}},
	)
	handler(delta)
	handler(delta)
	waitForForestCondition(t, time.Second, func() (bool, error) {
		var count int
		err := db.QueryRow(`SELECT COUNT(*) FROM forest_ledger WHERE source_key = ?`, delta.DeltaKey()).Scan(&count)
		return count == 1, err
	})
	snap := forest.DeltaIngestorSnapshot()
	if snap.Received != 2 || snap.Enqueued != 2 || snap.Ingested == 0 {
		t.Fatalf("ingestor snapshot = %+v", snap)
	}
}
