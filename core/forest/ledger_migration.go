package forest

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type ForestEventsLedgerMigrationResult struct {
	Scanned  int
	Inserted int
}

func MigrateForestEventsToLedger(ctx context.Context, db *sql.DB) (ForestEventsLedgerMigrationResult, error) {
	if db == nil {
		return ForestEventsLedgerMigrationResult{}, fmt.Errorf("forest events ledger migration requires db")
	}
	if err := ensurePhase123Schema(db); err != nil {
		return ForestEventsLedgerMigrationResult{}, err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, session_id, COALESCE(task_id, ''), COALESCE(agent_id, ''),
		       COALESCE(agent_type, ''), event_type, family, scope, root_id,
		       branch_id, COALESCE(parent_branch_id, ''), COALESCE(intent_id, ''),
		       COALESCE(content_id, ''), COALESCE(source_id, ''), confidence,
		       salience, timestamp, COALESCE(title, ''), COALESCE(summary, ''),
		       COALESCE(provenance_refs, ''), COALESCE(supersedes, ''),
		       COALESCE(contradicts, ''), COALESCE(related_branch_ids, ''),
		       COALESCE(payload, '')
		FROM forest_events
		ORDER BY timestamp ASC, id ASC
	`)
	if err != nil {
		return ForestEventsLedgerMigrationResult{}, fmt.Errorf("read forest_events fixture: %w", err)
	}
	defer rows.Close()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ForestEventsLedgerMigrationResult{}, fmt.Errorf("begin forest_events migration: %w", err)
	}
	defer tx.Rollback()

	var result ForestEventsLedgerMigrationResult
	for rows.Next() {
		event, err := scanForestEventMigrationRow(rows)
		if err != nil {
			return ForestEventsLedgerMigrationResult{}, err
		}
		result.Scanned++
		record := ledgerRecordFromForestEvent(prepareEvent(&event))
		prepared, payloadJSON, payloadHash, err := prepareLedgerRecord(record)
		if err != nil {
			return ForestEventsLedgerMigrationResult{}, err
		}
		appendResult, err := appendLedgerRecordTx(ctx, tx, prepared, payloadJSON, payloadHash)
		if err != nil {
			return ForestEventsLedgerMigrationResult{}, err
		}
		if appendResult.Inserted {
			result.Inserted++
		}
	}
	if err := rows.Err(); err != nil {
		return ForestEventsLedgerMigrationResult{}, fmt.Errorf("iterate forest_events fixture: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ForestEventsLedgerMigrationResult{}, fmt.Errorf("commit forest_events migration: %w", err)
	}
	return result, nil
}

func scanForestEventMigrationRow(rows *sql.Rows) (Event, error) {
	var event Event
	var timestamp int64
	var provenanceRefs, supersedes, contradicts, relatedBranchIDs, payload string
	if err := rows.Scan(
		&event.ID,
		&event.SessionID,
		&event.TaskID,
		&event.AgentID,
		&event.AgentType,
		&event.EventType,
		&event.Family,
		&event.Scope,
		&event.RootID,
		&event.BranchID,
		&event.ParentBranchID,
		&event.IntentID,
		&event.ContentID,
		&event.SourceID,
		&event.Confidence,
		&event.Salience,
		&timestamp,
		&event.Title,
		&event.Summary,
		&provenanceRefs,
		&supersedes,
		&contradicts,
		&relatedBranchIDs,
		&payload,
	); err != nil {
		return Event{}, fmt.Errorf("scan forest_events fixture row: %w", err)
	}
	event.Timestamp = time.Unix(timestamp, 0).UTC()
	event.ProvenanceRefs = decodeStringList(provenanceRefs)
	event.Supersedes = decodeStringList(supersedes)
	event.Contradicts = decodeStringList(contradicts)
	event.RelatedBranchIDs = decodeStringList(relatedBranchIDs)
	if payload != "" {
		var decoded map[string]any
		if err := unmarshalJSON(payload, &decoded); err != nil {
			return Event{}, fmt.Errorf("decode forest_events payload: %w", err)
		}
		event.Payload = decoded
	}
	return event, nil
}

func decodeStringList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, "[") {
		var out []string
		if err := unmarshalJSON(raw, &out); err == nil {
			return dedupeStrings(out)
		}
	}
	return dedupeStrings(strings.Split(raw, ","))
}
