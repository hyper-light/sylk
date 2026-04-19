package forest

import (
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// openTestForestDB opens an in-memory SQLite DB and runs the schema
// setup so append-only triggers are in place. Minimal harness — we skip
// the vectorgraphdb `nodes` table requirement by only running the
// event-table schema portion relevant to MEM-03.
func openTestForestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Create just the forest_events table + triggers. We can't call
	// ensureSchema() directly because it requires the nodes table from
	// vectorgraphdb. Install the minimal subset this test needs.
	if _, err := db.Exec(`
		CREATE TABLE forest_events (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			branch_id TEXT NOT NULL,
			timestamp INTEGER NOT NULL,
			payload TEXT
		);
	`); err != nil {
		t.Fatalf("create forest_events: %v", err)
	}
	if err := ensureForestEventsAppendOnly(db); err != nil {
		t.Fatalf("ensureForestEventsAppendOnly: %v", err)
	}
	return db
}

// TestForestEventsAppendOnly_RejectsUpdate is the core MEM-03 invariant:
// UPDATE on forest_events must raise a SQLite ABORT so the soil/evidence
// layer stays monotonic. Any successful UPDATE would silently rewrite
// audit history and break downstream learners that assume append-only.
func TestForestEventsAppendOnly_RejectsUpdate(t *testing.T) {
	db := openTestForestDB(t)

	if _, err := db.Exec(
		`INSERT INTO forest_events (id, session_id, branch_id, timestamp, payload) VALUES (?, ?, ?, ?, ?)`,
		"ev-1", "sess-1", "branch-1", 1000, "original",
	); err != nil {
		t.Fatalf("seed INSERT: %v", err)
	}

	_, err := db.Exec(`UPDATE forest_events SET payload = 'rewritten' WHERE id = 'ev-1'`)
	if err == nil {
		t.Fatal("UPDATE must fail; append-only trigger is not firing")
	}
	if !strings.Contains(err.Error(), "append-only") {
		t.Errorf("error message = %q, should reference append-only", err.Error())
	}

	// Verify payload remained untouched (the trigger's ABORT rolled back
	// the UPDATE).
	var payload string
	if err := db.QueryRow(`SELECT payload FROM forest_events WHERE id = 'ev-1'`).Scan(&payload); err != nil {
		t.Fatalf("read-back: %v", err)
	}
	if payload != "original" {
		t.Errorf("payload after rejected UPDATE = %q, want %q", payload, "original")
	}
}

// TestForestEventsAppendOnly_RejectsDelete verifies DELETE is also
// forbidden — retention / compaction must happen via archival workflows
// that migrate rows to cold storage, not in-place deletes that destroy
// history.
func TestForestEventsAppendOnly_RejectsDelete(t *testing.T) {
	db := openTestForestDB(t)
	_, _ = db.Exec(
		`INSERT INTO forest_events (id, session_id, branch_id, timestamp, payload) VALUES (?, ?, ?, ?, ?)`,
		"ev-1", "sess-1", "branch-1", 1000, "original",
	)

	_, err := db.Exec(`DELETE FROM forest_events WHERE id = 'ev-1'`)
	if err == nil {
		t.Fatal("DELETE must fail; append-only trigger is not firing")
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_events`).Scan(&count); err != nil {
		t.Fatalf("count-back: %v", err)
	}
	if count != 1 {
		t.Errorf("row count after rejected DELETE = %d, want 1", count)
	}
}

// TestForestEventsAppendOnly_InsertsStillPass verifies the triggers only
// block UPDATE / DELETE — appending new rows (the whole point of the
// layer) remains unimpeded.
func TestForestEventsAppendOnly_InsertsStillPass(t *testing.T) {
	db := openTestForestDB(t)

	for i := 0; i < 5; i++ {
		if _, err := db.Exec(
			`INSERT INTO forest_events (id, session_id, branch_id, timestamp, payload) VALUES (?, ?, ?, ?, ?)`,
			"ev-"+itoa(i), "sess-1", "branch-1", int64(1000+i), "payload",
		); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_events`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 5 {
		t.Errorf("after 5 inserts, count = %d, want 5", count)
	}
}

// TestForestEventsAppendOnly_TriggerIsIdempotent verifies running the
// trigger-installation code twice does not error out — essential because
// ensureSchema is called on every forest open.
func TestForestEventsAppendOnly_TriggerIsIdempotent(t *testing.T) {
	db := openTestForestDB(t)
	if err := ensureForestEventsAppendOnly(db); err != nil {
		t.Fatalf("second install: %v", err)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	negative := i < 0
	if negative {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if negative {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
