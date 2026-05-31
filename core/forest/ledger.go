package forest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/claims"
)

type LedgerSourceKind string

const (
	LedgerSourceClaimsDelta   LedgerSourceKind = "claims_delta"
	LedgerSourceFabricContext LedgerSourceKind = "fabric_context"
	LedgerSourceForestEvent   LedgerSourceKind = "forest_event_compat"
	LedgerSourceMaintenance   LedgerSourceKind = "forest_maintenance"
)

// LedgerRecord is the canonical immutable input to forest projections.
// Queryable fields are relational columns; Payload is persisted as canonical
// JSON in forest_ledger_payloads and is never queried through SQLite JSON
// functions.
type LedgerRecord struct {
	ID          string
	SourceKind  LedgerSourceKind
	SourceID    string
	SourceKey   string
	EventKind   string
	SessionID   string
	TaskID      string
	BoardID     string
	SubjectType string
	SubjectID   string
	Actor       claims.AgentRef
	Delivery    *claims.DeltaDelivery
	Reason      string
	OccurredAt  time.Time
	Payload     any
	Refs        []claims.DeltaRef
}

type LedgerAppendResult struct {
	ID       string
	Seq      int64
	Inserted bool
}

func (m *MemoryForest) AppendLedgerRecord(ctx context.Context, record LedgerRecord) (LedgerAppendResult, error) {
	if m == nil || m.db == nil {
		return LedgerAppendResult{}, errors.New("forest ledger append requires memory forest")
	}
	prepared, payloadJSON, payloadHash, err := prepareLedgerRecord(record)
	if err != nil {
		return LedgerAppendResult{}, err
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return LedgerAppendResult{}, fmt.Errorf("begin forest ledger tx: %w", err)
	}
	defer tx.Rollback()

	result, err := appendLedgerRecordTx(ctx, tx, prepared, payloadJSON, payloadHash)
	if err != nil {
		return LedgerAppendResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return LedgerAppendResult{}, fmt.Errorf("commit forest ledger tx: %w", err)
	}
	return result, nil
}

func (m *MemoryForest) AppendCanonicalDelta(ctx context.Context, delta claims.CanonicalDelta) (LedgerAppendResult, error) {
	if err := claims.ValidateCanonicalDeltaTolerant(delta); err != nil {
		return LedgerAppendResult{}, fmt.Errorf("canonical delta invalid: %w", err)
	}
	record := ledgerRecordFromCanonicalDelta(delta)
	prepared, payloadJSON, payloadHash, err := prepareLedgerRecord(record)
	if err != nil {
		return LedgerAppendResult{}, err
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return LedgerAppendResult{}, fmt.Errorf("begin canonical delta ledger tx: %w", err)
	}
	defer tx.Rollback()

	result, err := appendLedgerRecordTx(ctx, tx, prepared, payloadJSON, payloadHash)
	if err != nil {
		return LedgerAppendResult{}, err
	}
	if result.Inserted {
		if err := projectCanonicalDeltaEvidenceTx(ctx, tx, delta, result.ID, result.Seq, payloadHash); err != nil {
			if !errors.Is(err, errEvidenceProjectionRejected) {
				return LedgerAppendResult{}, err
			}
			if commitErr := tx.Commit(); commitErr != nil {
				return LedgerAppendResult{}, fmt.Errorf("commit rejected canonical delta ledger tx: %w", commitErr)
			}
			return result, err
		}
	}
	if err := tx.Commit(); err != nil {
		return LedgerAppendResult{}, fmt.Errorf("commit canonical delta ledger tx: %w", err)
	}
	return result, nil
}

func ledgerRecordFromCanonicalDelta(delta claims.CanonicalDelta) LedgerRecord {
	subjectType, subjectID := canonicalDeltaSubject(delta)
	return LedgerRecord{
		ID:          "ledger_" + stableID("claims_delta", delta.DeltaKey()),
		SourceKind:  LedgerSourceClaimsDelta,
		SourceID:    delta.DeltaID,
		SourceKey:   delta.DeltaKey(),
		EventKind:   string(delta.Action),
		SessionID:   delta.SessionID,
		BoardID:     delta.BoardID,
		SubjectType: subjectType,
		SubjectID:   subjectID,
		Actor:       delta.Actor,
		Delivery:    delta.Delivery,
		OccurredAt:  delta.OccurredAt,
		Payload:     delta,
		Refs:        delta.Refs,
	}
}

func canonicalDeltaSubject(delta claims.CanonicalDelta) (string, string) {
	if id := delta.RefID("artifact", claims.RelatedTypeArtifact); id != "" {
		return claims.RelatedTypeArtifact, id
	}
	if id := delta.RefID("validation", claims.RelatedTypeValidation); id != "" {
		return claims.RelatedTypeValidation, id
	}
	if id := delta.RefID("testament", claims.RelatedTypeTestament); id != "" {
		return claims.RelatedTypeTestament, id
	}
	if id := delta.ClaimID(); id != "" {
		return claims.RelatedTypeClaim, id
	}
	if len(delta.Refs) > 0 {
		return delta.Refs[0].Type, delta.Refs[0].ID
	}
	return "", ""
}

func prepareLedgerRecord(record LedgerRecord) (LedgerRecord, string, string, error) {
	record.SourceKind = LedgerSourceKind(strings.TrimSpace(string(record.SourceKind)))
	record.SourceID = strings.TrimSpace(record.SourceID)
	record.SourceKey = strings.TrimSpace(record.SourceKey)
	record.EventKind = strings.TrimSpace(record.EventKind)
	record.SessionID = strings.TrimSpace(record.SessionID)
	record.TaskID = normalizeForestTaskID(record.TaskID)
	record.BoardID = strings.TrimSpace(record.BoardID)
	record.SubjectType = strings.TrimSpace(record.SubjectType)
	record.SubjectID = strings.TrimSpace(record.SubjectID)
	record.Reason = strings.TrimSpace(record.Reason)
	record.Actor = record.Actor.Normalized()
	if record.OccurredAt.IsZero() {
		record.OccurredAt = time.Now().UTC()
	} else {
		record.OccurredAt = record.OccurredAt.UTC()
	}
	if record.SourceKind == "" {
		return record, "", "", errors.New("ledger source_kind is required")
	}
	if record.SourceKey == "" {
		return record, "", "", errors.New("ledger source_key is required")
	}
	if record.SourceID == "" {
		record.SourceID = record.SourceKey
	}
	if record.EventKind == "" {
		return record, "", "", errors.New("ledger event_kind is required")
	}
	if record.SessionID == "" {
		return record, "", "", errors.New("ledger session_id is required")
	}
	if record.SourceKind == LedgerSourceClaimsDelta {
		if err := record.Actor.Validate(); err != nil {
			return record, "", "", fmt.Errorf("ledger actor: %w", err)
		}
	}
	if record.ID == "" {
		record.ID = "ledger_" + stableID(string(record.SourceKind), record.SourceKey)
	}
	payloadJSON, payloadHash, err := canonicalPayload(record.Payload)
	if err != nil {
		return record, "", "", err
	}
	return record, payloadJSON, payloadHash, nil
}

func canonicalPayload(payload any) (string, string, error) {
	if payload == nil {
		return "", "", nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", "", fmt.Errorf("marshal ledger payload: %w", err)
	}
	sum := sha256.Sum256(data)
	return string(data), hex.EncodeToString(sum[:]), nil
}

func appendLedgerRecordTx(ctx context.Context, tx *sql.Tx, record LedgerRecord, payloadJSON, payloadHash string) (LedgerAppendResult, error) {
	now := time.Now().UTC().Unix()
	res, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO forest_ledger
			(id, source_kind, source_id, source_key, event_kind, session_id, task_id, board_id,
			 subject_type, subject_id, actor_uid, actor_type, actor_category, delivery_relationship,
			 reason, occurred_at, payload_hash, inserted_at)
		VALUES
			(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, record.ID, string(record.SourceKind), record.SourceID, record.SourceKey, record.EventKind,
		record.SessionID, record.TaskID, record.BoardID, record.SubjectType, record.SubjectID,
		record.Actor.UID, record.Actor.Type, record.Actor.Category, deliveryRelationship(record.Delivery),
		record.Reason, record.OccurredAt.Unix(), payloadHash, now)
	if err != nil {
		return LedgerAppendResult{}, fmt.Errorf("insert forest ledger: %w", err)
	}
	insertedRows, _ := res.RowsAffected()
	inserted := insertedRows > 0
	var (
		seq int64
		id  string
	)
	if err := tx.QueryRowContext(ctx, `
		SELECT seq, id
		FROM forest_ledger
		WHERE source_key = ?
	`, record.SourceKey).Scan(&seq, &id); err != nil {
		return LedgerAppendResult{}, fmt.Errorf("load forest ledger seq: %w", err)
	}
	if inserted {
		if err := insertLedgerPayloadTx(ctx, tx, id, payloadHash, payloadJSON, now); err != nil {
			return LedgerAppendResult{}, err
		}
		if err := insertLedgerRefsTx(ctx, tx, id, record.Refs); err != nil {
			return LedgerAppendResult{}, err
		}
		if err := insertLedgerDeliveryTx(ctx, tx, id, record.Delivery); err != nil {
			return LedgerAppendResult{}, err
		}
	}
	return LedgerAppendResult{ID: id, Seq: seq, Inserted: inserted}, nil
}

func insertLedgerPayloadTx(ctx context.Context, tx *sql.Tx, ledgerID, payloadHash, payloadJSON string, now int64) error {
	if payloadJSON == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO forest_ledger_payloads (ledger_id, payload_hash, payload, inserted_at)
		VALUES (?, ?, ?, ?)
	`, ledgerID, payloadHash, payloadJSON, now); err != nil {
		return fmt.Errorf("insert forest ledger payload: %w", err)
	}
	return nil
}

func insertLedgerRefsTx(ctx context.Context, tx *sql.Tx, ledgerID string, refs []claims.DeltaRef) error {
	for idx, ref := range refs {
		ref.Role = strings.TrimSpace(ref.Role)
		ref.Type = strings.TrimSpace(ref.Type)
		ref.ID = strings.TrimSpace(ref.ID)
		if ref.Role == "" || ref.Type == "" || ref.ID == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO forest_ledger_refs (ledger_id, ref_index, role, ref_type, ref_id)
			VALUES (?, ?, ?, ?, ?)
		`, ledgerID, idx, ref.Role, ref.Type, ref.ID); err != nil {
			return fmt.Errorf("insert forest ledger ref: %w", err)
		}
	}
	return nil
}

func insertLedgerDeliveryTx(ctx context.Context, tx *sql.Tx, ledgerID string, delivery *claims.DeltaDelivery) error {
	if delivery == nil {
		return nil
	}
	for idx, ref := range delivery.To {
		ref = ref.Normalized()
		route := ref.RouteKey()
		if route == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO forest_ledger_delivery
				(ledger_id, delivery_index, relationship, participant_uid, participant_type, participant_category, participant_route_key)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, ledgerID, idx, strings.TrimSpace(delivery.Relationship), ref.UID, ref.Type, ref.Category, route); err != nil {
			return fmt.Errorf("insert forest ledger delivery: %w", err)
		}
	}
	return nil
}

func deliveryRelationship(delivery *claims.DeltaDelivery) string {
	if delivery == nil {
		return ""
	}
	return strings.TrimSpace(delivery.Relationship)
}
