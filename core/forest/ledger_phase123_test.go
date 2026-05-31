package forest

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
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
	var appliedAt int64
	if err := db.QueryRow(`SELECT applied_at FROM forest_schema_meta WHERE meta_key = 'active'`).Scan(&appliedAt); err != nil {
		t.Fatalf("schema meta applied_at: %v", err)
	}
	if err := ensurePhase123Schema(db); err != nil {
		t.Fatalf("rerun phase schema: %v", err)
	}
	var appliedAgain int64
	if err := db.QueryRow(`SELECT applied_at FROM forest_schema_meta WHERE meta_key = 'active'`).Scan(&appliedAgain); err != nil {
		t.Fatalf("schema meta applied_again: %v", err)
	}
	if appliedAt != appliedAgain {
		t.Fatalf("applied_at changed on idempotent migration: %d -> %d", appliedAt, appliedAgain)
	}
}

func TestPhase123SchemaRejectsUnsupportedProjection(t *testing.T) {
	db := openRawForestTestDB(t)
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	if _, err := db.Exec(`UPDATE forest_schema_meta SET projection_version = 'legacy_branch_active' WHERE meta_key = 'active'`); err != nil {
		t.Fatalf("seed unsupported projection: %v", err)
	}
	if err := ensurePhase123Schema(db); err == nil {
		t.Fatal("unsupported projection version accepted")
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

func TestAppendCanonicalDeltaDerivesInlineArtifactHashAndQueriesEvidence(t *testing.T) {
	forest, _ := newTestForest(t)
	defer forest.Close()

	delta := canonicalDeltaForForestTest(claims.DeltaActionArtifactGenerated, 31)
	delta.Refs = []claims.DeltaRef{
		{Role: "claim", Type: claims.RelatedTypeClaim, ID: "claim-31"},
		{Role: "artifact", Type: claims.RelatedTypeArtifact, ID: "artifact-inline"},
	}
	delta.Context = map[string]any{
		"claim": map[string]any{"id": "claim-31", "action": "task"},
		"artifact": map[string]any{
			"id":        "artifact-inline",
			"kind":      "markdown",
			"data_type": "text/markdown",
			"content":   "# plan\n\nship it",
		},
	}
	delta.Key = claims.BuildCanonicalDeltaKeyForSequence(delta.Action, delta.SessionID, delta.BoardID, delta.Sequence, delta.Refs, delta.Delivery)
	if _, err := forest.AppendCanonicalDelta(context.Background(), delta); err != nil {
		t.Fatalf("append inline artifact: %v", err)
	}
	records, err := forest.QueryArtifactEvidence(context.Background(), ArtifactEvidenceFilter{ClaimID: "claim-31"})
	if err != nil {
		t.Fatalf("query artifact evidence: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("artifact evidence records = %d, want 1", len(records))
	}
	if records[0].ContentHash == "" || !strings.HasPrefix(records[0].ContentHash, "sha256:") {
		t.Fatalf("content hash = %q", records[0].ContentHash)
	}
	if len(records[0].Edges) == 0 {
		t.Fatalf("artifact edges missing: %+v", records[0])
	}
}

func TestAppendCanonicalDeltaRejectsArtifactSequenceRegression(t *testing.T) {
	forest, db := newTestForest(t)
	defer forest.Close()

	first := canonicalDeltaForForestTest(claims.DeltaActionArtifactGenerated, 40)
	first.Refs = []claims.DeltaRef{{Role: "claim", Type: claims.RelatedTypeClaim, ID: "claim-regress"}, {Role: "artifact", Type: claims.RelatedTypeArtifact, ID: "artifact-regress"}}
	first.Context = map[string]any{"artifact": map[string]any{"id": "artifact-regress", "kind": "log", "content_hash": "sha256:first"}}
	first.Key = claims.BuildCanonicalDeltaKeyForSequence(first.Action, first.SessionID, first.BoardID, first.Sequence, first.Refs, first.Delivery)
	if _, err := forest.AppendCanonicalDelta(context.Background(), first); err != nil {
		t.Fatalf("append first artifact: %v", err)
	}
	second := first
	second.Sequence = 39
	second.DeltaID = "delta-regression"
	second.Key = claims.BuildCanonicalDeltaKeyForSequence(second.Action, second.SessionID, second.BoardID, second.Sequence, second.Refs, second.Delivery)
	if _, err := forest.AppendCanonicalDelta(context.Background(), second); err == nil {
		t.Fatal("sequence regression accepted")
	}
	var sequence int64
	if err := db.QueryRow(`SELECT last_sequence FROM forest_artifacts WHERE artifact_id = 'artifact-regress'`).Scan(&sequence); err != nil {
		t.Fatalf("load artifact sequence: %v", err)
	}
	if sequence != 40 {
		t.Fatalf("artifact sequence = %d, want 40", sequence)
	}
}

func TestAppendCanonicalDeltaRejectsArtifactMissingContentIdentity(t *testing.T) {
	forest, db := newTestForest(t)
	defer forest.Close()

	delta := canonicalDeltaForForestTest(claims.DeltaActionArtifactGenerated, 45)
	delta.Refs = []claims.DeltaRef{{Role: "claim", Type: claims.RelatedTypeClaim, ID: "claim-no-content"}, {Role: "artifact", Type: claims.RelatedTypeArtifact, ID: "artifact-no-content"}}
	delta.Context = map[string]any{"artifact": map[string]any{"id": "artifact-no-content", "kind": "log"}}
	delta.Key = claims.BuildCanonicalDeltaKeyForSequence(delta.Action, delta.SessionID, delta.BoardID, delta.Sequence, delta.Refs, delta.Delivery)
	if _, err := forest.AppendCanonicalDelta(context.Background(), delta); err == nil {
		t.Fatal("artifact without content identity accepted")
	}
	var errorKind string
	if err := db.QueryRow(`SELECT error_kind FROM forest_evidence_errors WHERE entity_id = ?`, "artifact-no-content").Scan(&errorKind); err != nil {
		t.Fatalf("load evidence error: %v", err)
	}
	if errorKind != "missing_content_identity" {
		t.Fatalf("error kind = %q", errorKind)
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

func TestKnownDeltaActionsAreAuditedOrProjected(t *testing.T) {
	forest, db := newTestForest(t)
	defer forest.Close()

	for idx, action := range claims.KnownDeltaActions() {
		delta := canonicalDeltaForForestTest(action, uint64(idx+1))
		if _, err := forest.AppendCanonicalDelta(context.Background(), delta); err != nil {
			t.Fatalf("append action %q: %v", action, err)
		}
	}
	var audited int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_delta_projection_audit`).Scan(&audited); err != nil {
		t.Fatalf("count projection audit: %v", err)
	}
	if audited != len(claims.KnownDeltaActions()) {
		t.Fatalf("audited actions = %d, want %d", audited, len(claims.KnownDeltaActions()))
	}
	var unknown int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_delta_projection_audit WHERE classification = 'unknown_observed_delta'`).Scan(&unknown); err != nil {
		t.Fatalf("count unknown audit: %v", err)
	}
	if unknown != 0 {
		t.Fatalf("known action classified unknown count = %d", unknown)
	}
}

func TestFutureCanonicalDeltaActionIsAuditedWithoutProjection(t *testing.T) {
	forest, db := newTestForest(t)
	defer forest.Close()

	delta := canonicalDeltaForForestTest(claims.DeltaAction("skill.generated_future"), 90)
	if _, err := forest.AppendCanonicalDelta(context.Background(), delta); err != nil {
		t.Fatalf("append future action: %v", err)
	}
	var classification string
	if err := db.QueryRow(`SELECT classification FROM forest_delta_projection_audit WHERE action = ?`, string(delta.Action)).Scan(&classification); err != nil {
		t.Fatalf("load future action audit: %v", err)
	}
	if classification != "unknown_observed_delta" {
		t.Fatalf("classification = %q", classification)
	}
}

func TestValidationFailureCreatesResultAndRemediationEvidence(t *testing.T) {
	forest, _ := newTestForest(t)
	defer forest.Close()

	artifact := canonicalDeltaForForestTest(claims.DeltaActionArtifactGenerated, 101)
	artifact.Refs = []claims.DeltaRef{{Role: "claim", Type: claims.RelatedTypeClaim, ID: "claim-fail"}, {Role: "artifact", Type: claims.RelatedTypeArtifact, ID: "artifact-fail"}}
	artifact.Context = map[string]any{"artifact": map[string]any{"id": "artifact-fail", "kind": "patch", "content_hash": "sha256:patch"}}
	artifact.Key = claims.BuildCanonicalDeltaKeyForSequence(artifact.Action, artifact.SessionID, artifact.BoardID, artifact.Sequence, artifact.Refs, artifact.Delivery)
	if _, err := forest.AppendCanonicalDelta(context.Background(), artifact); err != nil {
		t.Fatalf("append artifact: %v", err)
	}
	validation := canonicalDeltaForForestTest(claims.DeltaActionValidationValidationFailed, 102)
	validation.Refs = []claims.DeltaRef{
		{Role: "claim", Type: claims.RelatedTypeClaim, ID: "claim-fail"},
		{Role: "validation", Type: claims.RelatedTypeValidation, ID: "validation-fail"},
		{Role: "artifact", Type: claims.RelatedTypeArtifact, ID: "artifact-fail"},
		{Role: "result_artifact", Type: claims.RelatedTypeArtifact, ID: "artifact-result-fail"},
		{Role: "remediation_claim", Type: claims.RelatedTypeClaim, ID: "claim-remediate"},
	}
	validation.Context = map[string]any{
		"claim": map[string]any{"id": "claim-fail", "action": "task"},
		"validation": map[string]any{
			"id":                 "validation-fail",
			"type":               "programmatic",
			"status":             string(claims.ValidationStatusValidationFailed),
			"required":           true,
			"target_artifact_id": "artifact-fail",
			"result_artifact_id": "artifact-result-fail",
		},
	}
	validation.Key = claims.BuildCanonicalDeltaKeyForSequence(validation.Action, validation.SessionID, validation.BoardID, validation.Sequence, validation.Refs, validation.Delivery)
	if _, err := forest.AppendCanonicalDelta(context.Background(), validation); err != nil {
		t.Fatalf("append validation failure: %v", err)
	}
	validations, err := forest.QueryValidationEvidence(context.Background(), ValidationEvidenceFilter{ValidationID: "validation-fail"})
	if err != nil {
		t.Fatalf("query validation evidence: %v", err)
	}
	if len(validations) != 1 || validations[0].ResultArtifactID != "artifact-result-fail" {
		t.Fatalf("validation evidence = %+v", validations)
	}
	artifacts, err := forest.QueryArtifactEvidence(context.Background(), ArtifactEvidenceFilter{ArtifactID: "artifact-result-fail"})
	if err != nil {
		t.Fatalf("query result artifact: %v", err)
	}
	if len(artifacts) != 1 {
		t.Fatalf("result artifacts = %d, want 1", len(artifacts))
	}
	if !hasArtifactEdge(artifacts[0].Edges, artifactEdgeRemediates, claims.RelatedTypeClaim, "claim-remediate") {
		t.Fatalf("remediation edge missing: %+v", artifacts[0].Edges)
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
	if _, err := forest.AppendCanonicalDelta(context.Background(), delta); err == nil {
		t.Fatal("append validation without target succeeded")
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
	var validations int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_validations WHERE validation_id = ?`, "validation-missing-target").Scan(&validations); err != nil {
		t.Fatalf("count rejected validation rows: %v", err)
	}
	if validations != 0 {
		t.Fatalf("rejected validation rows = %d, want 0", validations)
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
	var handlers []claims.DeltaHandler
	patterns := canonicalDeltaIngestPatterns("session-1")
	subscriber := claimsmocks.NewDeltaSubscriber(t)
	subscriber.EXPECT().
		SubscribeDelta(mock.Anything, mock.Anything).
		Run(func(_ string, h claims.DeltaHandler) { handlers = append(handlers, h) }).
		Return(subscription, nil).
		Times(len(patterns))
	forest, err := New(Config{DB: db, SynchronousProjection: true, ClaimsDeltaSubscriber: subscriber, ClaimsDeltaSessionFilter: "session-1", DeltaIngestQueueCapacity: 4})
	if err != nil {
		t.Fatalf("new forest: %v", err)
	}
	defer forest.Close()
	if len(handlers) != len(patterns) {
		t.Fatalf("handlers = %d, want %d", len(handlers), len(patterns))
	}
	delta := claims.NewCanonicalDelta(
		claims.DeltaActionArtifactGenerated,
		"session-1", "board-1", 10, time.Unix(23, 0),
		claims.DegradedAgentRef("engineer", "test"),
		[]claims.DeltaRef{{Role: "artifact", Type: claims.RelatedTypeArtifact, ID: "artifact-1"}},
		nil,
		map[string]any{"artifact": map[string]any{"id": "artifact-1", "kind": "diagnostic", "status": string(claims.ArtifactStatusGenerated), "content_hash": "sha256:def"}},
	)
	handlers[0](delta)
	handlers[0](delta)
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

func TestDeltaIngestorAuditsLegacyDelta(t *testing.T) {
	forest, db := newTestForest(t)
	defer forest.Close()
	ingestor := &DeltaIngestor{forest: forest}
	legacy := claims.InboxDelta{SessionID: "session-legacy", Sequence: 7, ClaimID: "claim-legacy", AgentID: "architect"}
	ingestor.handleDelta(legacy)
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_ledger WHERE event_kind = 'claims_delta_legacy_ignored'`).Scan(&count); err != nil {
		t.Fatalf("count legacy audit: %v", err)
	}
	if count != 1 {
		t.Fatalf("legacy audit rows = %d, want 1", count)
	}
}

func TestDeltaIngestorOverflowRecordsLedgerAndRuntimeHealth(t *testing.T) {
	forest, db := newTestForest(t)
	defer forest.Close()
	ingestor := &DeltaIngestor{
		forest: forest,
		queue:  make(chan claims.CanonicalDelta, 1),
	}
	forest.deltaIngestor = ingestor
	forest.registerRuntimeQueue("claims_delta_ingestor", cap(ingestor.queue))
	delta := canonicalDeltaForForestTest(claims.DeltaActionClaimGenerated, 77)
	ingestor.handleDelta(delta)
	ingestor.handleDelta(delta)
	var overflowRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_ledger WHERE event_kind = 'claims_delta_ingest_overflow'`).Scan(&overflowRows); err != nil {
		t.Fatalf("count overflow ledger: %v", err)
	}
	if overflowRows != 1 {
		t.Fatalf("overflow rows = %d, want 1", overflowRows)
	}
	snap := forest.RuntimeSnapshot()
	if !runtimeQueueOverflowed(snap, "claims_delta_ingestor") {
		t.Fatalf("runtime queue overflow not visible: %+v", snap.Queues)
	}
}

func TestMigrateForestEventsToLedgerFixture(t *testing.T) {
	db := openRawForestTestDB(t)
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO forest_events
			(id, session_id, task_id, agent_id, agent_type, event_type, family, scope,
			 root_id, branch_id, confidence, salience, timestamp, title, summary, payload)
		VALUES
			('legacy-event-1', 'session-legacy', '', 'agent-1', 'engineer', 'observation',
			 'evidence', 'episodic', 'root-1', 'branch-1', 0.8, 0.7, 123, 'legacy', 'legacy summary', '{}')
	`); err != nil {
		t.Fatalf("seed forest_events: %v", err)
	}
	result, err := MigrateForestEventsToLedger(context.Background(), db)
	if err != nil {
		t.Fatalf("migrate forest_events: %v", err)
	}
	if result.Scanned != 1 || result.Inserted != 1 {
		t.Fatalf("migration result = %+v", result)
	}
	result, err = MigrateForestEventsToLedger(context.Background(), db)
	if err != nil {
		t.Fatalf("rerun forest_events migration: %v", err)
	}
	if result.Scanned != 1 || result.Inserted != 0 {
		t.Fatalf("second migration result = %+v", result)
	}
}

func openRawForestTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+stableID("raw-forest", t.Name())+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		t.Fatalf("busy_timeout: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE nodes (id TEXT PRIMARY KEY, domain INTEGER NOT NULL, node_type INTEGER NOT NULL, name TEXT NOT NULL)`); err != nil {
		t.Fatalf("seed nodes: %v", err)
	}
	return db
}

func canonicalDeltaForForestTest(action claims.DeltaAction, sequence uint64) claims.CanonicalDelta {
	refs := []claims.DeltaRef{{Role: "claim", Type: claims.RelatedTypeClaim, ID: fmt.Sprintf("claim-%d", sequence)}}
	context := map[string]any{"claim": map[string]any{"id": fmt.Sprintf("claim-%d", sequence), "action": "task"}}
	if _, ok := claims.DeltaActionTestamentLifecycleStatus(action); ok {
		refs = append(refs, claims.DeltaRef{Role: "testament", Type: claims.RelatedTypeTestament, ID: fmt.Sprintf("testament-%d", sequence)})
		context["testament"] = map[string]any{"id": fmt.Sprintf("testament-%d", sequence)}
	}
	if _, ok := claims.DeltaActionArtifactLifecycleStatus(action); ok {
		refs = append(refs, claims.DeltaRef{Role: "artifact", Type: claims.RelatedTypeArtifact, ID: fmt.Sprintf("artifact-%d", sequence)})
		context["artifact"] = map[string]any{"id": fmt.Sprintf("artifact-%d", sequence), "kind": "test", "content_hash": fmt.Sprintf("sha256:%d", sequence)}
	}
	if _, ok := claims.DeltaActionValidationLifecycleStatus(action); ok || action == claims.DeltaActionValidationEvaluated {
		refs = append(refs,
			claims.DeltaRef{Role: "validation", Type: claims.RelatedTypeValidation, ID: fmt.Sprintf("validation-%d", sequence)},
			claims.DeltaRef{Role: "artifact", Type: claims.RelatedTypeArtifact, ID: fmt.Sprintf("artifact-target-%d", sequence)},
		)
		context["validation"] = map[string]any{
			"id":                 fmt.Sprintf("validation-%d", sequence),
			"type":               "programmatic",
			"status":             validationStatusForAction(action),
			"target_artifact_id": fmt.Sprintf("artifact-target-%d", sequence),
			"required":           true,
		}
	}
	var delivery *claims.DeltaDelivery
	if claims.DeltaActionRequiresDelivery(action) {
		delivery = &claims.DeltaDelivery{
			To:           []claims.AgentRef{claims.DegradedAgentRef("architect", "test")},
			Relationship: claims.RelationshipSubject,
		}
	}
	return claims.NewCanonicalDelta(
		action,
		"session-actions",
		"board-actions",
		sequence,
		time.Unix(int64(sequence), 0),
		claims.DegradedAgentRef("engineer", "test"),
		refs,
		delivery,
		context,
	)
}

func validationStatusForAction(action claims.DeltaAction) string {
	if status, ok := claims.DeltaActionValidationLifecycleStatus(action); ok {
		return string(status)
	}
	if action == claims.DeltaActionValidationEvaluated {
		return string(claims.ValidationStatusValidated)
	}
	return ""
}

func hasArtifactEdge(edges []ArtifactEvidenceEdge, kind, targetType, targetID string) bool {
	for _, edge := range edges {
		if edge.Kind == kind && edge.TargetType == targetType && edge.TargetID == targetID {
			return true
		}
	}
	return false
}

func runtimeQueueOverflowed(snapshot RuntimeSnapshot, name string) bool {
	for _, queue := range snapshot.Queues {
		if queue.Name == name && queue.Overflows > 0 {
			return true
		}
	}
	return false
}
