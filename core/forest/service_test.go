package forest

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	ctxpkg "github.com/adalundhe/sylk/core/context"
	_ "github.com/mattn/go-sqlite3"
)

func newTestForest(t testing.TB) (*MemoryForest, *sql.DB) {
	return newTestForestWithConfig(t, Config{})
}

func newTestForestWithConfig(t testing.TB, cfg Config) (*MemoryForest, *sql.DB) {
	t.Helper()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", stableID("forest-test", t.Name()))
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// Set busy_timeout so the audit drainer goroutine and the test's
	// own writes don't deadlock on the shared-cache file lock.
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		t.Fatalf("busy_timeout: %v", err)
	}

	schema := `
		CREATE TABLE nodes (
			id TEXT PRIMARY KEY,
			domain INTEGER NOT NULL,
			node_type INTEGER NOT NULL,
			name TEXT NOT NULL
		);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("seed nodes table: %v", err)
	}

	cfg.DB = db
	// Tests assume read-your-writes semantics on the branch
	// projection: AppendEvent → immediate query. The async CQRS
	// projector is correct for production but breaks that
	// assumption, so tests run with inline projection.
	cfg.SynchronousProjection = true
	forest, err := New(cfg)
	if err != nil {
		t.Fatalf("new forest: %v", err)
	}
	return forest, db
}

func waitForForestCondition(t *testing.T, timeout time.Duration, check func() (bool, error)) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		ok, err := check()
		if err != nil {
			if strings.Contains(err.Error(), "database table is locked") {
				err = nil
			} else {
				t.Fatalf("wait condition: %v", err)
			}
		}
		if err == nil && ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("condition not met before timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestMemoryForest_NewRequiresNodesTable(t *testing.T) {
	t.Parallel()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", stableID("forest-missing-nodes", t.Name()))
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	_, err = New(Config{DB: db})
	if err == nil {
		t.Fatal("expected error for missing nodes table")
	}
	if !strings.Contains(err.Error(), "forest db missing nodes table") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMemoryForest_RecordContentAndRetrieve(t *testing.T) {
	t.Parallel()

	forest, db := newTestForest(t)
	defer forest.Close()
	defer db.Close()

	now := time.Now().UTC()
	entry := &ctxpkg.ContentEntry{
		ID:          "decision-1",
		SessionID:   "session-1",
		AgentID:     "engineer-1",
		AgentType:   "engineer",
		ContentType: ctxpkg.ContentTypeDecision,
		Content:     "Choose retry with jitter for the API client to reduce burst failures.",
		Timestamp:   now,
		TurnNumber:  1,
		Confidence:  0.92,
		Salience:    0.88,
	}
	if err := forest.RecordContent(context.Background(), entry); err != nil {
		t.Fatalf("record content: %v", err)
	}

	packets, err := forest.Retrieve(context.Background(), Query{
		Query:     "retry jitter api client",
		SessionID: "session-1",
		Limit:     4,
		Families:  []TreeFamily{TreeFamilyIntent},
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(packets) == 0 {
		t.Fatal("expected at least one packet")
	}
	if packets[0].Branch.Family != TreeFamilyIntent {
		t.Fatalf("unexpected family: %s", packets[0].Branch.Family)
	}
	if packets[0].Score.Total <= 0 {
		t.Fatalf("expected positive score, got %f", packets[0].Score.Total)
	}
}

func TestMemoryForest_TaskHorizonIsTaskScoped(t *testing.T) {
	t.Parallel()

	forest, db := newTestForest(t)
	defer forest.Close()
	defer db.Close()

	now := time.Now().UTC()
	entries := []*ctxpkg.ContentEntry{
		{
			ID:          "task-a-decision",
			SessionID:   "session-task",
			AgentID:     "tester-1",
			AgentType:   "tester",
			ContentType: ctxpkg.ContentTypeDecision,
			Content:     "Focus test coverage on timeout handling for task A.",
			Timestamp:   now,
			TurnNumber:  1,
			Confidence:  0.9,
			Salience:    0.9,
			Metadata:    map[string]string{"task_id": "task-a"},
		},
		{
			ID:          "task-b-decision",
			SessionID:   "session-task",
			AgentID:     "tester-1",
			AgentType:   "tester",
			ContentType: ctxpkg.ContentTypeDecision,
			Content:     "Focus test coverage on install flows for task B.",
			Timestamp:   now.Add(time.Second),
			TurnNumber:  2,
			Confidence:  0.9,
			Salience:    0.9,
			Metadata:    map[string]string{"task_id": "task-b"},
		},
	}
	for _, entry := range entries {
		if err := forest.RecordContent(context.Background(), entry); err != nil {
			t.Fatalf("record content %s: %v", entry.ID, err)
		}
	}

	packets, err := forest.Retrieve(context.Background(), Query{
		Query:     "timeout coverage",
		SessionID: "session-task",
		TaskID:    "task-a",
		Horizon:   CanopyHorizonTask,
		Limit:     8,
		Families:  []TreeFamily{TreeFamilyIntent},
	})
	if err != nil {
		t.Fatalf("retrieve task-scoped packets: %v", err)
	}
	if len(packets) == 0 {
		t.Fatal("expected task-scoped packets")
	}
	for _, packet := range packets {
		if packet == nil || packet.Branch == nil {
			continue
		}
		if packet.Branch.TaskID != "task-a" {
			t.Fatalf("retrieved branch task_id = %q, want task-a", packet.Branch.TaskID)
		}
	}

	intent, err := forest.ResolveIntent(context.Background(), ResolveIntentInput{
		Query:     "timeout coverage",
		SessionID: "session-task",
		TaskID:    "task-a",
		Horizon:   CanopyHorizonTask,
		Limit:     8,
	})
	if err != nil {
		t.Fatalf("resolve intent task-scoped: %v", err)
	}
	if len(intent.ActiveRoots) == 0 {
		t.Fatal("expected task canopy roots")
	}
	for _, rootID := range intent.ActiveRoots {
		if strings.Contains(rootID, "task-b") {
			t.Fatalf("task canopy leaked other task root %q", rootID)
		}
	}
}

func TestMemoryForest_ReplayPromotesSemanticBranch(t *testing.T) {
	t.Parallel()

	forest, db := newTestForest(t)
	defer forest.Close()
	defer db.Close()

	entry := &ctxpkg.ContentEntry{
		ID:          "scribe-1",
		SessionID:   "session-2",
		AgentID:     "scribe-engineer-1",
		AgentType:   "scribe-engineer",
		ContentType: ctxpkg.ContentTypeAssistantReply,
		Content:     "Engineer sidecar notes: adding retry with jitter reduced flaky failures in three runs.",
		Timestamp:   time.Now().UTC(),
		TurnNumber:  2,
		Confidence:  0.8,
		Salience:    0.9,
	}
	if err := forest.RecordContent(context.Background(), entry); err != nil {
		t.Fatalf("record content: %v", err)
	}
	if _, err := db.Exec(`UPDATE forest_replay_queue SET available_at = 0`); err != nil {
		t.Fatalf("update replay queue: %v", err)
	}

	result, err := forest.RunReplay(context.Background(), 8)
	if err != nil {
		t.Fatalf("run replay: %v", err)
	}
	if result.Processed == 0 {
		t.Fatal("expected replay queue items to be processed")
	}

	rows, err := db.Query(`SELECT COUNT(*) FROM forest_branches WHERE scope = ?`, string(ScopeSemantic))
	if err != nil {
		t.Fatalf("query semantic branches: %v", err)
	}
	defer rows.Close()

	var count int
	if rows.Next() {
		if err := rows.Scan(&count); err != nil {
			t.Fatalf("scan semantic branch count: %v", err)
		}
	}
	if count == 0 {
		t.Fatal("expected replay to promote at least one semantic branch")
	}
}

func TestMemoryForest_ReplayPromotesSemanticBranchEventDriven(t *testing.T) {
	t.Parallel()

	forest, db := newTestForestWithConfig(t, Config{ReplayDelay: 10 * time.Millisecond})
	defer forest.Close()
	defer db.Close()

	entry := &ctxpkg.ContentEntry{
		ID:          "scribe-event-driven",
		SessionID:   "session-event-replay",
		AgentID:     "scribe-engineer-1",
		AgentType:   "scribe-engineer",
		ContentType: ctxpkg.ContentTypeAssistantReply,
		Content:     "Engineer sidecar notes: retry jitter eliminated flaky network bursts.",
		Timestamp:   time.Now().UTC(),
		TurnNumber:  3,
		Confidence:  0.86,
		Salience:    0.93,
	}
	if err := forest.RecordContent(context.Background(), entry); err != nil {
		t.Fatalf("record content: %v", err)
	}

	waitForForestCondition(t, 2*time.Second, func() (bool, error) {
		var count int
		err := db.QueryRow(`SELECT COUNT(*) FROM forest_branches WHERE scope = ?`, string(ScopeSemantic)).Scan(&count)
		return count > 0, err
	})
}

func TestMemoryForest_TrainModelsAndPredict(t *testing.T) {
	t.Parallel()

	forest, db := newTestForest(t)
	defer forest.Close()
	defer db.Close()

	now := time.Now().UTC()
	for i := 0; i < 10; i++ {
		entry := &ctxpkg.ContentEntry{
			ID:          fmt.Sprintf("positive-%d", i),
			SessionID:   "session-train",
			AgentID:     "engineer-1",
			AgentType:   "engineer",
			ContentType: ctxpkg.ContentTypeDecision,
			Content:     fmt.Sprintf("Retry with jitter and capped exponential backoff for flaky API clients %d.", i),
			Timestamp:   now.Add(time.Duration(i) * time.Second),
			TurnNumber:  i + 1,
			Confidence:  0.9,
			Salience:    0.85,
		}
		if err := forest.RecordContent(context.Background(), entry); err != nil {
			t.Fatalf("record positive content %d: %v", i, err)
		}
	}
	for i := 0; i < 10; i++ {
		entry := &ctxpkg.ContentEntry{
			ID:          fmt.Sprintf("negative-%d", i),
			SessionID:   "session-train",
			AgentID:     "engineer-1",
			AgentType:   "engineer",
			ContentType: ctxpkg.ContentTypeDecision,
			Content:     fmt.Sprintf("Refresh marketing splash gradients and update color theme token names %d.", i),
			Timestamp:   now.Add(time.Duration(20+i) * time.Second),
			TurnNumber:  i + 20,
			Confidence:  0.7,
			Salience:    0.6,
		}
		if err := forest.RecordContent(context.Background(), entry); err != nil {
			t.Fatalf("record negative content %d: %v", i, err)
		}
	}

	packets, err := forest.Retrieve(context.Background(), Query{
		Query:     "retry jitter backoff flaky api",
		SessionID: "session-train",
		AgentType: "engineer",
		Limit:     20,
		Families:  []TreeFamily{TreeFamilyIntent},
	})
	if err != nil {
		t.Fatalf("initial retrieve: %v", err)
	}
	if len(packets) < 16 {
		t.Fatalf("expected at least 16 packets for training, got %d", len(packets))
	}

	for _, packet := range packets {
		status := OutcomeStatusFailed
		if strings.Contains(strings.ToLower(packet.Branch.Summary), "retry") {
			status = OutcomeStatusSucceeded
		}
		if err := forest.RecordOutcome(context.Background(), OutcomeRecord{
			BranchID:   packet.Branch.ID,
			SessionID:  "session-train",
			AgentID:    "engineer-1",
			AgentType:  "engineer",
			Status:     status,
			Summary:    "training label",
			Confidence: 0.9,
			Salience:   0.8,
		}); err != nil {
			t.Fatalf("record outcome for %s: %v", packet.Branch.ID, err)
		}
	}

	trained, err := forest.TrainModels(context.Background(), 256)
	if err != nil {
		t.Fatalf("train models: %v", err)
	}
	if trained.Trained == 0 {
		t.Fatal("expected at least one learned model to train")
	}

	packets, err = forest.Retrieve(context.Background(), Query{
		Query:     "retry jitter backoff flaky api",
		SessionID: "session-train",
		AgentType: "engineer",
		Limit:     8,
		Families:  []TreeFamily{TreeFamilyIntent},
	})
	if err != nil {
		t.Fatalf("retrieve with learned model: %v", err)
	}
	if len(packets) == 0 {
		t.Fatal("expected packets after training")
	}
	if packets[0].Prediction == nil {
		t.Fatal("expected learned prediction on returned packet")
	}
	if packets[0].Prediction.UtilityModel != modelKey(ObjectiveUtility, "engineer") {
		t.Fatalf("unexpected utility model: %s", packets[0].Prediction.UtilityModel)
	}
	if packets[0].Score.ModelConfidence <= 0 {
		t.Fatalf("expected positive model confidence, got %f", packets[0].Score.ModelConfidence)
	}
	if !strings.Contains(strings.ToLower(packets[0].Branch.Summary), "retry") {
		t.Fatalf("expected learned reranker to prefer retry branch, got %q", packets[0].Branch.Summary)
	}
}

func TestMemoryForest_TrainModelsEventDriven(t *testing.T) {
	t.Parallel()

	forest, db := newTestForestWithConfig(t, Config{TrainingDebounce: 10 * time.Millisecond})
	defer forest.Close()
	defer db.Close()

	now := time.Now().UTC()
	for i := 0; i < 10; i++ {
		entry := &ctxpkg.ContentEntry{
			ID:          fmt.Sprintf("event-positive-%d", i),
			SessionID:   "session-train-event",
			AgentID:     "engineer-1",
			AgentType:   "engineer",
			ContentType: ctxpkg.ContentTypeDecision,
			Content:     fmt.Sprintf("Retry with jitter and capped exponential backoff for flaky API clients %d.", i),
			Timestamp:   now.Add(time.Duration(i) * time.Second),
			TurnNumber:  i + 1,
			Confidence:  0.9,
			Salience:    0.85,
		}
		if err := forest.RecordContent(context.Background(), entry); err != nil {
			t.Fatalf("record positive content %d: %v", i, err)
		}
	}
	for i := 0; i < 10; i++ {
		entry := &ctxpkg.ContentEntry{
			ID:          fmt.Sprintf("event-negative-%d", i),
			SessionID:   "session-train-event",
			AgentID:     "engineer-1",
			AgentType:   "engineer",
			ContentType: ctxpkg.ContentTypeDecision,
			Content:     fmt.Sprintf("Refresh marketing splash gradients and update color theme token names %d.", i),
			Timestamp:   now.Add(time.Duration(20+i) * time.Second),
			TurnNumber:  i + 20,
			Confidence:  0.7,
			Salience:    0.6,
		}
		if err := forest.RecordContent(context.Background(), entry); err != nil {
			t.Fatalf("record negative content %d: %v", i, err)
		}
	}

	packets, err := forest.Retrieve(context.Background(), Query{
		Query:     "retry jitter backoff flaky api",
		SessionID: "session-train-event",
		AgentType: "engineer",
		Limit:     20,
		Families:  []TreeFamily{TreeFamilyIntent},
	})
	if err != nil {
		t.Fatalf("initial retrieve: %v", err)
	}
	if len(packets) < 16 {
		t.Fatalf("expected at least 16 packets for training, got %d", len(packets))
	}

	for _, packet := range packets {
		status := OutcomeStatusFailed
		if strings.Contains(strings.ToLower(packet.Branch.Summary), "retry") {
			status = OutcomeStatusSucceeded
		}
		if err := forest.RecordOutcome(context.Background(), OutcomeRecord{
			BranchID:   packet.Branch.ID,
			SessionID:  "session-train-event",
			AgentID:    "engineer-1",
			AgentType:  "engineer",
			Status:     status,
			Summary:    "training label",
			Confidence: 0.9,
			Salience:   0.8,
		}); err != nil {
			t.Fatalf("record outcome for %s: %v", packet.Branch.ID, err)
		}
	}

	waitForForestCondition(t, 5*time.Second, func() (bool, error) {
		var count int
		err := db.QueryRow(`SELECT COUNT(*) FROM forest_models WHERE active = 1`).Scan(&count)
		return count > 0, err
	})

	packets, err = forest.Retrieve(context.Background(), Query{
		Query:     "retry jitter backoff flaky api",
		SessionID: "session-train-event",
		AgentType: "engineer",
		Limit:     8,
		Families:  []TreeFamily{TreeFamilyIntent},
	})
	if err != nil {
		t.Fatalf("retrieve with learned model: %v", err)
	}
	if len(packets) == 0 {
		t.Fatal("expected packets after training")
	}
	if packets[0].Prediction == nil {
		t.Fatal("expected learned prediction on returned packet")
	}
}

func TestMemoryForest_RetrieveDoesNotComputeSubstrate(t *testing.T) {
	t.Parallel()

	forest, db := newTestForestWithConfig(t, Config{SubstrateDebounce: 500 * time.Millisecond})
	defer forest.Close()
	defer db.Close()

	now := time.Now().UTC()
	entries := []*ctxpkg.ContentEntry{
		{
			ID:          "substrate-read-a",
			SessionID:   "session-substrate-read",
			AgentID:     "engineer-1",
			AgentType:   "engineer",
			ContentType: ctxpkg.ContentTypeDecision,
			Content:     "Implement retry jitter on the API adapter and preserve previous timeout semantics.",
			Timestamp:   now,
			TurnNumber:  1,
			Confidence:  0.88,
			Salience:    0.82,
		},
		{
			ID:          "substrate-read-b",
			SessionID:   "session-substrate-read",
			AgentID:     "guardian",
			AgentType:   "guardian",
			ContentType: ctxpkg.ContentTypeConstraint,
			Content:     "Do not broaden retry scope beyond idempotent requests without explicit approval.",
			Timestamp:   now.Add(time.Second),
			TurnNumber:  2,
			Confidence:  0.91,
			Salience:    0.86,
		},
	}
	for _, entry := range entries {
		if err := forest.RecordContent(context.Background(), entry); err != nil {
			t.Fatalf("record content %s: %v", entry.ID, err)
		}
	}

	packets, err := forest.Retrieve(context.Background(), Query{
		Query:     "retry jitter idempotent api adapter",
		SessionID: "session-substrate-read",
		AgentType: "engineer",
		Limit:     4,
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(packets) == 0 {
		t.Fatal("expected packets before substrate maintenance runs")
	}

	var stateCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_substrate_state`).Scan(&stateCount); err != nil {
		t.Fatalf("count substrate state: %v", err)
	}
	if stateCount != 0 {
		t.Fatalf("expected retrieve to avoid computing substrate state, got %d rows", stateCount)
	}
}

func TestMemoryForest_SubstrateRefreshesStateEventDriven(t *testing.T) {
	t.Parallel()

	forest, db := newTestForestWithConfig(t, Config{SubstrateDebounce: 10 * time.Millisecond})
	defer forest.Close()
	defer db.Close()

	now := time.Now().UTC()
	entries := []*ctxpkg.ContentEntry{
		{
			ID:          "substrate-a",
			SessionID:   "session-substrate",
			AgentID:     "engineer-1",
			AgentType:   "engineer",
			ContentType: ctxpkg.ContentTypeDecision,
			Content:     "Implement retry jitter on the API adapter and preserve previous timeout semantics.",
			Timestamp:   now,
			TurnNumber:  1,
			Confidence:  0.88,
			Salience:    0.82,
		},
		{
			ID:          "substrate-b",
			SessionID:   "session-substrate",
			AgentID:     "guardian",
			AgentType:   "guardian",
			ContentType: ctxpkg.ContentTypeConstraint,
			Content:     "Do not broaden retry scope beyond idempotent requests without explicit approval.",
			Timestamp:   now.Add(time.Second),
			TurnNumber:  2,
			Confidence:  0.91,
			Salience:    0.86,
		},
	}
	for _, entry := range entries {
		if err := forest.RecordContent(context.Background(), entry); err != nil {
			t.Fatalf("record content %s: %v", entry.ID, err)
		}
	}

	waitForForestCondition(t, 2*time.Second, func() (bool, error) {
		var stateCount int
		if err := db.QueryRow(`SELECT COUNT(*) FROM forest_substrate_state`).Scan(&stateCount); err != nil {
			return false, err
		}
		var frontierCount int
		if err := db.QueryRow(`SELECT COUNT(*) FROM forest_substrate_frontiers`).Scan(&frontierCount); err != nil {
			return false, err
		}
		return stateCount > 0 && frontierCount > 0, nil
	})

	packets, err := forest.Retrieve(context.Background(), Query{
		Query:     "retry jitter idempotent api adapter",
		SessionID: "session-substrate",
		AgentType: "engineer",
		Limit:     4,
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(packets) == 0 {
		t.Fatal("expected substrate-backed packets")
	}
	if packets[0].Score.Substrate <= 0 {
		t.Fatalf("expected positive substrate score, got %f", packets[0].Score.Substrate)
	}
	if packets[0].Score.Frontier <= 0 {
		t.Fatalf("expected positive frontier score, got %f", packets[0].Score.Frontier)
	}
}
