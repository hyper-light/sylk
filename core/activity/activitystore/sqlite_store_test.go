package activitystore

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/database"
)

func openTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	cfg := database.DefaultBunSQLiteConfig(filepath.Join(t.TempDir(), "fabric.db"))
	db, err := database.OpenBunSQLite(cfg)
	if err != nil {
		t.Fatalf("open bun sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := NewSQLiteStore(db)
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return store
}

func sampleActivity(kind ActionKind, sessionID, path string) AgentActivity {
	return AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  SessionID(sessionID),
		Timestamp:  time.Now(),
		Resolution: activity.ResolutionFor(kind),
		Actor: Actor{
			AgentID:    "test-agent-1",
			AgentType:  "tester-pipeline",
			PipelineID: "pipe-7",
		},
		Action:  kind,
		Subject: Subject{PathPrefix: path, Domain: "test_framework"},
		State:   activity.StatePoint,
	}
}

func TestSQLiteStore_AppendAndFilter(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	a := sampleActivity(ActionDecisionDeclared, "sess-1", "services/billing/")
	store.Append(ctx, a)

	got, err := store.FilterActivities(ctx, QueryFilter{
		SessionID:   "sess-1",
		ActionKinds: []ActionKind{ActionDecisionDeclared},
	})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 activity, got %d", len(got))
	}
	if got[0].ID != a.ID {
		t.Fatalf("ID mismatch: got %s want %s", got[0].ID, a.ID)
	}
	if got[0].Subject.PathPrefix != "services/billing/" {
		t.Fatalf("subject path lost across roundtrip: got %q", got[0].Subject.PathPrefix)
	}
	if got[0].Subject.Domain != "test_framework" {
		t.Fatalf("subject domain lost across roundtrip: got %q", got[0].Subject.Domain)
	}
}

func TestSQLiteStore_DropsAtomicResolution(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	// File reads are Atomic — must NOT be persisted.
	store.Append(ctx, sampleActivity(ActionFileRead, "sess-1", "main.go"))

	got, err := store.FilterActivities(ctx, QueryFilter{SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Atomic activity must not reach SQLite; got %d rows", len(got))
	}
}

func TestSQLiteStore_PathPrefixFilter(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	store.Append(ctx, sampleActivity(ActionDecisionDeclared, "sess-1", "services/billing/api/"))
	store.Append(ctx, sampleActivity(ActionDecisionDeclared, "sess-1", "services/auth/"))
	store.Append(ctx, sampleActivity(ActionDecisionDeclared, "sess-1", "internal/util/"))

	got, err := store.FilterActivities(ctx, QueryFilter{
		SessionID:         "sess-1",
		SubjectPathPrefix: "services/",
	})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 services/* activities, got %d", len(got))
	}
}

func TestSQLiteStore_CausalLink(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	parent := sampleActivity(ActionChallengeEmitted, "sess-1", "tests/auth/")
	store.Append(ctx, parent)

	child := sampleActivity(ActionChallengeResponse, "sess-1", "tests/auth/")
	parentID := parent.ID
	child.Caused = &parentID
	child.Resolves = &parentID
	store.Append(ctx, child)

	// Query by Resolves to find the response.
	got, err := store.FilterActivities(ctx, QueryFilter{
		SessionID: "sess-1",
		Resolves:  &parentID,
	})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 resolving activity, got %d", len(got))
	}
	if got[0].ID != child.ID {
		t.Fatalf("resolved activity ID mismatch")
	}
}

func TestDualTierSink_RoutesByResolution(t *testing.T) {
	store := openTestStore(t)
	sink, err := NewDualTierSink(store, DefaultDualTierSinkConfig())
	if err != nil {
		t.Fatalf("dual tier sink: %v", err)
	}
	defer sink.Close()

	prev := SetDefaultSink(sink)
	defer SetDefaultSink(prev)

	ctx := EnsureFabricContext(context.Background())
	// Emit one of each tier.
	span := StartSpan(ctx, ActionFileRead, Subject{PathPrefix: "main.go"})
	span.End()
	span = StartSpan(ctx, ActionDecisionDeclared, Subject{Domain: "test_framework", PathPrefix: "tests/"})
	span.End()

	// Ristretto.Set is asynchronous — wait briefly for the cache to
	// settle so GetActivity hits the cache for the recently-emitted
	// activity (we don't strictly require it, but it makes the test
	// deterministic).
	time.Sleep(20 * time.Millisecond)

	durable, err := store.FilterActivities(context.Background(), QueryFilter{
		ActionKinds: []ActionKind{ActionDecisionDeclared, ActionFileRead},
	})
	if err != nil {
		t.Fatalf("durable filter: %v", err)
	}
	// Only the Coarse decision_declared should be persisted; the
	// Atomic file_read is in the ring buffer only.
	if len(durable) != 1 {
		t.Fatalf("expected 1 durable activity, got %d", len(durable))
	}
	if durable[0].Action != ActionDecisionDeclared {
		t.Fatalf("expected the Coarse decision to persist; got %s", durable[0].Action)
	}

	// Ring buffer should hold the Atomic one.
	rolling := sink.RecentRolling(QueryFilter{ActionKinds: []ActionKind{ActionFileRead}})
	if len(rolling) != 1 {
		t.Fatalf("expected 1 Atomic activity in ring buffer, got %d", len(rolling))
	}
}
