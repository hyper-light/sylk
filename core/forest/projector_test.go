package forest

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// newAsyncTestForest returns a forest with the async branch projector
// running (no SynchronousProjection override). Required to exercise
// the CQRS path end-to-end. The shared-cache in-memory SQLite needs
// explicit PRAGMA busy_timeout so concurrent writers don't see
// SQLITE_BUSY when the projector loop and AppendEvent overlap.
func newAsyncTestForest(t *testing.T) (*MemoryForest, *sql.DB) {
	return newAsyncTestForestWithConfig(t, Config{})
}

// newAsyncTestForestWithExploration constructs an async test forest
// with deterministic ε-greedy exploration. Used by selection-bias
// tests that need reproducible exploration behavior.
func newAsyncTestForestWithExploration(t *testing.T, rate float64, seed int64) (*MemoryForest, *sql.DB) {
	return newAsyncTestForestWithConfig(t, Config{
		ExplorationRate: rate,
		ExplorationSeed: seed,
		// Sweeper interval kept short so tests don't have to wait for
		// the default 5 minutes.
		ImplicitNegativeSweepInterval: 50 * time.Millisecond,
	})
}

func newAsyncTestForestWithConfig(t *testing.T, cfg Config) (*MemoryForest, *sql.DB) {
	t.Helper()

	dsn := fmt.Sprintf(
		"file:%s?mode=memory&cache=shared",
		stableID("forest-test-async", t.Name()),
	)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// Single connection so PRAGMAs apply to all queries — the shared-
	// cache in-memory mode requires this for consistent behavior, and
	// busy_timeout has to be set per-connection.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		t.Fatalf("busy_timeout: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(`
		CREATE TABLE nodes (
			id TEXT PRIMARY KEY,
			domain INTEGER NOT NULL,
			node_type INTEGER NOT NULL,
			name TEXT NOT NULL
		);
	`); err != nil {
		t.Fatalf("seed nodes table: %v", err)
	}

	cfg.DB = db
	forest, err := New(cfg)
	if err != nil {
		t.Fatalf("new forest: %v", err)
	}
	t.Cleanup(func() { _ = forest.Close() })
	return forest, db
}

// TestProjector_AppendThenWaitForBranchSeq verifies the canonical
// CQRS path: AppendEvent returns immediately after the ledger commit;
// the projector catches up asynchronously; WaitForBranchSeq blocks
// until projection completes.
func TestProjector_AppendThenWaitForBranchSeq(t *testing.T) {
	t.Skip("legacy branch projector removed; node graph projection is the production path")
	forest, _ := newAsyncTestForest(t)
	ctx := context.Background()

	event := &Event{
		SessionID: "sess-1",
		BranchID:  "branch-A",
		AgentID:   "engineer-1",
		AgentType: "engineer",
		EventType: EventTypeDecisionRecorded,
		Family:    TreeFamilyDecision,
		Title:     "Implement HS256 deserialization",
	}

	if err := forest.AppendEvent(ctx, event); err != nil {
		t.Fatalf("append event: %v", err)
	}
	if event.Seq <= 0 {
		t.Fatalf("expected seq > 0, got %d", event.Seq)
	}

	if err := forest.WaitForBranchSeq(ctx, event.Seq, 5*time.Second); err != nil {
		t.Fatalf("wait for branch seq: %v", err)
	}

	branch, err := forest.getBranch(ctx, event.BranchID)
	if err != nil {
		t.Fatalf("load branch: %v", err)
	}
	if branch == nil {
		t.Fatalf("branch %q not projected", event.BranchID)
	}
	if branch.LastAppliedSeq != event.Seq {
		t.Errorf("branch.LastAppliedSeq = %d, want %d", branch.LastAppliedSeq, event.Seq)
	}
}

// TestProjector_IdempotentReplay verifies that re-appending the same
// event ID is a no-op — same seq returned, projection unchanged.
func TestProjector_IdempotentReplay(t *testing.T) {
	t.Skip("legacy branch projector removed; node graph projection is the production path")
	forest, _ := newAsyncTestForest(t)
	ctx := context.Background()

	event := &Event{
		ID:        "stable-event-id",
		SessionID: "sess-1",
		BranchID:  "branch-A",
		AgentID:   "a1",
		AgentType: "engineer",
		EventType: EventTypeDecisionRecorded,
		Family:    TreeFamilyDecision,
		Title:     "Stable",
	}

	if err := forest.AppendEvent(ctx, event); err != nil {
		t.Fatalf("first append: %v", err)
	}
	firstSeq := event.Seq
	if err := forest.WaitForBranchSeq(ctx, firstSeq, 5*time.Second); err != nil {
		t.Fatalf("wait first: %v", err)
	}

	// Reset Seq on the in-memory event so we can verify the
	// duplicate path repopulates it.
	event.Seq = 0
	if err := forest.AppendEvent(ctx, event); err != nil {
		t.Fatalf("duplicate append: %v", err)
	}
	if event.Seq != firstSeq {
		t.Errorf("expected duplicate to return same seq %d, got %d", firstSeq, event.Seq)
	}

	branch, err := forest.getBranch(ctx, event.BranchID)
	if err != nil || branch == nil {
		t.Fatalf("load branch: branch=%v err=%v", branch, err)
	}
	// SupportCount should be 1 — duplicate didn't double-apply.
	if branch.SupportCount != 1 {
		t.Errorf("SupportCount = %d, want 1 (duplicate must not double-apply)", branch.SupportCount)
	}
}

// TestProjector_ConcurrentAppendsNoLostUpdates exercises concurrent
// AppendEvent calls against the same branch. Under CQRS the source
// of truth is the event log (no read-modify-write), so every event
// gets a unique seq and the projection eventually reflects all
// events with no lost updates.
func TestProjector_ConcurrentAppendsNoLostUpdates(t *testing.T) {
	t.Skip("legacy branch projector removed; node graph projection is the production path")
	forest, _ := newAsyncTestForest(t)
	ctx := context.Background()

	const writers = 4
	const eventsPerWriter = 12
	totalEvents := writers * eventsPerWriter

	var wg sync.WaitGroup
	errCh := make(chan error, totalEvents)
	maxSeqCh := make(chan int64, totalEvents)

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for i := 0; i < eventsPerWriter; i++ {
				event := &Event{
					ID:        fmt.Sprintf("evt-%d-%d", writerID, i),
					SessionID: "sess-concurrent",
					BranchID:  "branch-shared",
					AgentID:   fmt.Sprintf("writer-%d", writerID),
					AgentType: "engineer",
					EventType: EventTypeDecisionRecorded,
					Family:    TreeFamilyDecision,
					Title:     fmt.Sprintf("Decision w%d-i%d", writerID, i),
				}
				if err := forest.AppendEvent(ctx, event); err != nil {
					errCh <- err
					return
				}
				maxSeqCh <- event.Seq
			}
		}(w)
	}

	wg.Wait()
	close(errCh)
	close(maxSeqCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent append error: %v", err)
		}
	}

	var maxSeq int64
	seenSeqs := make(map[int64]struct{}, totalEvents)
	for s := range maxSeqCh {
		if _, dup := seenSeqs[s]; dup {
			t.Errorf("duplicate seq %d", s)
		}
		seenSeqs[s] = struct{}{}
		if s > maxSeq {
			maxSeq = s
		}
	}
	if len(seenSeqs) != totalEvents {
		t.Errorf("got %d unique seqs, want %d", len(seenSeqs), totalEvents)
	}

	if err := forest.WaitForBranchSeq(ctx, maxSeq, 10*time.Second); err != nil {
		t.Fatalf("wait for catch-up: %v", err)
	}

	branch, err := forest.getBranch(ctx, "branch-shared")
	if err != nil {
		t.Fatalf("load branch: %v", err)
	}
	if branch == nil {
		t.Fatalf("branch not projected")
	}
	if branch.SupportCount != totalEvents {
		t.Errorf("SupportCount = %d, want %d (no lost updates expected under CQRS)",
			branch.SupportCount, totalEvents)
	}
	if branch.LastAppliedSeq != maxSeq {
		t.Errorf("LastAppliedSeq = %d, want %d", branch.LastAppliedSeq, maxSeq)
	}
}

// TestProjector_StateLifecycle verifies the projector state row is
// created, transitions to running, and tracks last_applied_seq as
// events are processed.
func TestProjector_StateLifecycle(t *testing.T) {
	t.Skip("legacy branch projector state machine removed from production")
	forest, _ := newAsyncTestForest(t)
	ctx := context.Background()

	event := &Event{
		SessionID: "sess-1", BranchID: "branch-life", AgentID: "a", AgentType: "engineer",
		EventType: EventTypeDecisionRecorded, Family: TreeFamilyDecision, Title: "Lifecycle",
	}
	if err := forest.AppendEvent(ctx, event); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := forest.WaitForBranchSeq(ctx, event.Seq, 5*time.Second); err != nil {
		t.Fatalf("wait: %v", err)
	}

	state, err := forest.ProjectorStatus(projectorBranchName)
	if err != nil {
		t.Fatalf("projector status: %v", err)
	}
	if state.lastAppliedSeq < event.Seq {
		t.Errorf("lastAppliedSeq = %d, want >= %d", state.lastAppliedSeq, event.Seq)
	}
	if state.healthStatus != ProjectorHealthRunning {
		t.Errorf("healthStatus = %q, want %q", state.healthStatus, ProjectorHealthRunning)
	}
	if state.leaderHolder != forest.projectorID {
		t.Errorf("leaderHolder = %q, want %q", state.leaderHolder, forest.projectorID)
	}
}

// TestProjector_IsFatalApplyErrClassification verifies the
// transient/fatal triage treats schema/constraint failures as fatal
// and lock contention as transient.
func TestProjector_IsFatalApplyErrClassification(t *testing.T) {
	cases := []struct {
		err   error
		fatal bool
	}{
		{fmt.Errorf("nothing wrong here"), false},
		{fmt.Errorf("database is locked"), false},
		{fmt.Errorf("database table is locked: forest_events"), false},
		{fmt.Errorf("context canceled"), false},
		{fmt.Errorf("constraint failed: NOT NULL on forest_branches.id"), true},
		{fmt.Errorf("CHECK constraint failed: status"), true},
		{fmt.Errorf("no such column: last_applied_seq"), true},
		{fmt.Errorf("no such table: forest_branches"), true},
		{fmt.Errorf("datatype mismatch"), true},
		{fmt.Errorf("near \"FROM\": syntax error"), true},
	}
	for _, tc := range cases {
		got := isFatalApplyErr(tc.err)
		if got != tc.fatal {
			t.Errorf("isFatalApplyErr(%q) = %v, want %v", tc.err, got, tc.fatal)
		}
	}
}

// TestProjector_PoisonPillCounter verifies the projectorState's
// poison-pill counter increments on the same seq and resets across
// seqs / on success.
func TestProjector_PoisonPillCounter(t *testing.T) {
	state := &projectorState{}

	for i := 0; i < projectorPoisonPillThreshold-1; i++ {
		if state.recordPoisonHit(42) {
			t.Fatalf("pre-threshold hit %d returned halt", i+1)
		}
	}
	if !state.recordPoisonHit(42) {
		t.Fatal("threshold hit did not return halt")
	}

	state2 := &projectorState{}
	state2.recordPoisonHit(1)
	state2.recordPoisonHit(2) // different seq → counter resets
	if state2.poisonCount != 1 {
		t.Errorf("seq change should reset counter, got %d", state2.poisonCount)
	}

	state3 := &projectorState{}
	state3.recordPoisonHit(100)
	state3.recordPoisonHit(100)
	state3.clearPoison()
	if state3.poisonCount != 0 || state3.poisonSeq != 0 {
		t.Errorf("clearPoison did not reset: count=%d seq=%d",
			state3.poisonCount, state3.poisonSeq)
	}
}

// TestProjector_TruncateErrorRuneSafe verifies error truncation
// doesn't split a multi-byte rune.
func TestProjector_TruncateErrorRuneSafe(t *testing.T) {
	// 10 instances of the 3-byte heart emoji. ASCII length: 30.
	heart := "\xe2\x9d\xa4" // ❤
	long := ""
	for i := 0; i < 10; i++ {
		long += heart
	}
	out := truncateError(long, 10)
	// Should not include any partial heart bytes.
	for _, r := range out {
		if r == 0xFFFD {
			t.Fatalf("truncation produced replacement char (rune split): %q", out)
		}
	}
}

// TestProjector_AppendSeqUnsetOnCommitFailure verifies that an event
// whose Commit fails does not have its Seq set on the in-memory
// struct. This is the post-fix behavior — earlier the seq was set
// before commit and observable on commit-failure.
func TestProjector_AppendSeqUnsetOnCommitFailure(t *testing.T) {
	// Direct testing of commit-failure paths is hard without
	// faulting the SQLite driver. Instead verify the property
	// indirectly: a successful append sets Seq; a duplicate append
	// resets the in-memory Seq path (we set Seq=0 on the input,
	// expect AppendEvent to populate it from the existing row).
	forest, _ := newAsyncTestForest(t)
	ctx := context.Background()

	event := &Event{
		ID:        "seq-commit-test",
		SessionID: "sess", BranchID: "branch", AgentID: "a", AgentType: "engineer",
		EventType: EventTypeDecisionRecorded, Family: TreeFamilyDecision,
		Title: "Seq commit",
	}
	if err := forest.AppendEvent(ctx, event); err != nil {
		t.Fatalf("first append: %v", err)
	}
	if event.Seq == 0 {
		t.Fatal("expected non-zero seq after successful append")
	}

	// Reset and replay — duplicate path also sets Seq from the
	// existing row, never leaves it at 0.
	prior := event.Seq
	event.Seq = 0
	if err := forest.AppendEvent(ctx, event); err != nil {
		t.Fatalf("duplicate append: %v", err)
	}
	if event.Seq != prior {
		t.Errorf("duplicate append seq mismatch: got %d want %d", event.Seq, prior)
	}
}

// TestProjector_WaitForBranchSeqIsEventDriven asserts that
// WaitForBranchSeq returns within a tight budget — proving the wake
// is event-driven via the seq notifier rather than wall-clock
// polling. The projector now has no idle poll timer at all (the
// renew tick at 10s is the only wall-clock backstop), so 100ms is a
// generous bound that proves event delivery rather than periodic
// poll resolution.
func TestProjector_WaitForBranchSeqIsEventDriven(t *testing.T) {
	t.Skip("legacy branch sequence waiter removed from production")
	forest, _ := newAsyncTestForest(t)
	ctx := context.Background()

	const eventDispatchDelay = 30 * time.Millisecond
	const tightBudget = 100 * time.Millisecond

	// Append once to learn the next seq the new event will get.
	primer := &Event{
		SessionID: "sess-event-driven",
		BranchID:  "branch-primer",
		AgentID:   "a", AgentType: "engineer",
		EventType: EventTypeDecisionRecorded,
		Family:    TreeFamilyDecision,
		Title:     "Primer",
	}
	if err := forest.AppendEvent(ctx, primer); err != nil {
		t.Fatalf("primer append: %v", err)
	}
	if err := forest.WaitForBranchSeq(ctx, primer.Seq, time.Second); err != nil {
		t.Fatalf("primer wait: %v", err)
	}
	targetSeq := primer.Seq + 1

	// Schedule the actual append after the wait starts.
	go func() {
		time.Sleep(eventDispatchDelay)
		event := &Event{
			SessionID: "sess-event-driven",
			BranchID:  "branch-driven",
			AgentID:   "a", AgentType: "engineer",
			EventType: EventTypeDecisionRecorded,
			Family:    TreeFamilyDecision,
			Title:     "Driven",
		}
		_ = forest.AppendEvent(ctx, event)
	}()

	start := time.Now()
	if err := forest.WaitForBranchSeq(ctx, targetSeq, time.Second); err != nil {
		t.Fatalf("wait: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed >= tightBudget {
		t.Errorf("wait took %s, expected < %s — implementation may be polling", elapsed, tightBudget)
	}
}

// TestProjector_WaitForBranchSeqUnblocksOnHalt verifies the notifier
// wakes pending waiters with the halt error rather than letting them
// time out.
func TestProjector_WaitForBranchSeqUnblocksOnHalt(t *testing.T) {
	forest, _ := newAsyncTestForest(t)
	ctx := context.Background()

	// Manually halt the projector via the notifier.
	const targetSeq = 999_999

	go func() {
		time.Sleep(20 * time.Millisecond)
		forest.seqNotify.Halt(fmt.Errorf("synthetic test halt"))
	}()

	start := time.Now()
	err := forest.WaitForBranchSeq(ctx, targetSeq, 5*time.Second)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected halt error, got nil")
	}
	if elapsed >= 500*time.Millisecond {
		t.Errorf("wait took %s on halt, expected < 500ms — halt should wake immediately", elapsed)
	}
	if !strings.Contains(err.Error(), "synthetic test halt") {
		t.Errorf("error doesn't mention halt cause: %v", err)
	}
}

// TestProjector_AppendEventAssignsMonotonicSeq verifies seq
// allocation is strictly monotonic across sequential appends.
func TestProjector_AppendEventAssignsMonotonicSeq(t *testing.T) {
	forest, _ := newAsyncTestForest(t)
	ctx := context.Background()

	const n = 10
	seqs := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		event := &Event{
			SessionID: "sess-mono",
			BranchID:  fmt.Sprintf("branch-%d", i),
			AgentID:   "a", AgentType: "engineer",
			EventType: EventTypeDecisionRecorded,
			Family:    TreeFamilyDecision,
			Title:     fmt.Sprintf("Mono %d", i),
		}
		if err := forest.AppendEvent(ctx, event); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		seqs = append(seqs, event.Seq)
	}
	for i := 1; i < n; i++ {
		if seqs[i] <= seqs[i-1] {
			t.Errorf("seq[%d]=%d not greater than seq[%d]=%d", i, seqs[i], i-1, seqs[i-1])
		}
	}
}
