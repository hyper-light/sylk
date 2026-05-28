package claims

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/google/uuid"
)

const (
	phase9TestBoardID   = "board-phase-9"
	phase9TestSessionID = "session-phase-9"
	phase9TestTaskID    = "task-phase-9"

	phase9ConcurrentLifecycleWorkers   = 8
	phase9ConcurrentLifecyclePerWorker = 12
	phase9LongReplayLifecycleCount     = 128
	phase9LongReplayMemoryBudgetBytes  = 64 << 20
)

func TestDurableBoard_ReplayRejectsLifecycleTransitionBeforeClaim(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "session")
	walDir := filepath.Join(sessionDir, "protocols", walNamespace, phase9TestBoardID, "wal")
	if err := os.MkdirAll(walDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestWALEvent(t, walDir, walEvent{
		EventID:   uuid.NewString(),
		BoardID:   phase9TestBoardID,
		Kind:      walEventClaimLifecycleTransition,
		AgentID:   "architect",
		CreatedAt: time.Now().UTC(),
		Payload: mustJSON(t, map[string]any{
			"claim_ids": []string{"missing-claim"},
			"to":        ClaimLifecyclePosted,
			"agent_id":  "architect",
			"reason":    "invalid out-of-order replay",
			"changed":   time.Now().UTC(),
		}),
	})

	db, err := OpenDurableBoard(ClaimsBoardConfig{
		BoardID:    phase9TestBoardID,
		SessionID:  phase9TestSessionID,
		TaskID:     phase9TestTaskID,
		SessionDir: sessionDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	proj := db.Board().Projection()
	if proj.TotalClaims != 0 {
		t.Fatalf("replayed claims = %d, want 0", proj.TotalClaims)
	}
	if !containsText(proj.NotificationErrors, "missing claim") {
		t.Fatalf("notification errors missing replay-order failure: %v", proj.NotificationErrors)
	}
}

func TestDurableBoard_ReplayDeduplicatesDuplicateWALContent(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "session")
	cfg := ClaimsBoardConfig{
		BoardID:    phase9TestBoardID,
		SessionID:  phase9TestSessionID,
		TaskID:     phase9TestTaskID,
		SessionDir: sessionDir,
	}
	db, err := OpenDurableBoard(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Board().PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{testClaim("claim-1", "Plan work")}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	walPath := filepath.Join(sessionDir, "protocols", walNamespace, phase9TestBoardID, "wal", "events.wal.jsonl")
	data, err := os.ReadFile(walPath)
	if err != nil {
		t.Fatal(err)
	}
	line := firstNonEmptyLine(string(data))
	var event walEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		t.Fatal(err)
	}
	event.EventID = uuid.NewString()
	appendTestWALEvent(t, walPath, event)

	reopened, err := OpenDurableBoard(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := countOutboxRecords(reopened.outbox.Records(), "claim", "claim-1", string(DeltaActionClaimPosted)); got != 1 {
		t.Fatalf("claim.posted outbox records after duplicate WAL replay = %d, want 1", got)
	}
}

func TestDurableBoard_ReopenPublishesPendingCanonicalDelta(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "session")
	firstBus := newCaptureBus()
	cfg := ClaimsBoardConfig{
		BoardID:    phase9TestBoardID,
		SessionID:  phase9TestSessionID,
		TaskID:     phase9TestTaskID,
		SessionDir: sessionDir,
		DeltaBus:   firstBus,
	}
	db, err := OpenDurableBoard(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Board().PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeConsultation}, []Claim{{
		ID:          "claim-1",
		Title:       "Ask librarian",
		Description: "Inspect project shape.",
		Relations: []Relation{
			{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: "librarian", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	if got := firstBus.filterPublishedByTopic(CanonicalAgentTypeTopic(phase9TestSessionID, "librarian", DeltaActionClaimPosted)); len(got) != 0 {
		t.Fatalf("first process published canonical delta before replay: %d", len(got))
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	replayBus := newCaptureBus()
	cfg.DeltaBus = replayBus
	reopened, err := OpenDurableBoard(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	topic := CanonicalAgentTypeTopic(phase9TestSessionID, "librarian", DeltaActionClaimPosted)
	if got := replayBus.filterPublishedByTopic(topic); len(got) != 1 {
		t.Fatalf("replay-published canonical deltas on %s = %d, want 1", topic, len(got))
	}
}

func TestClaimsBoard_BlockedDeltaPublishDoesNotHoldBoardLock(t *testing.T) {
	bus := newBlockingDeltaBus()
	scope := concurrency.NewGoroutineScope(context.Background(), "phase9-blocked-publish", nil)
	board := NewClaimsBoard(ClaimsBoardConfig{
		BoardID:   phase9TestBoardID,
		SessionID: phase9TestSessionID,
		TaskID:    phase9TestTaskID,
		DeltaBus:  bus,
		Scope:     &concurrency.ScopeAdapter{Scope: scope},
	})

	done := make(chan error, 1)
	go func() {
		done <- board.PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeConsultation}, []Claim{{
			ID:    "claim-1",
			Title: "Ask librarian",
			Relations: []Relation{
				{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
				{Related: "librarian", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
			},
		}})
	}()
	select {
	case <-bus.started:
	case <-time.After(time.Second):
		t.Fatal("delta publish did not start")
	}

	readDone := make(chan struct{})
	go func() {
		_ = board.Projection()
		close(readDone)
	}()
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("Projection blocked while delta publisher was blocked; board lock was held across publish")
	}

	close(bus.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := scope.Shutdown(time.Second, time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestDurableBoard_ReplayMatchesLifecycleProjectionAtEveryBoundary(t *testing.T) {
	steps := phase9LifecycleReplaySteps()
	for boundary := range steps {
		t.Run(steps[boundary].name, func(t *testing.T) {
			sessionDir := filepath.Join(t.TempDir(), "session")
			cfg := ClaimsBoardConfig{
				BoardID:    phase9TestBoardID,
				SessionID:  phase9TestSessionID,
				TaskID:     phase9TestTaskID,
				SessionDir: sessionDir,
			}
			db, err := OpenDurableBoard(cfg)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i <= boundary; i++ {
				if err := steps[i].apply(context.Background(), db.Board()); err != nil {
					t.Fatalf("apply %s: %v", steps[i].name, err)
				}
			}
			before := normalizedReplayProjection(t, db.Board().Projection())
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			reopened, err := OpenDurableBoard(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			after := normalizedReplayProjection(t, reopened.Board().Projection())
			if before != after {
				t.Fatalf("projection mismatch after replay at %s\nbefore=%s\nafter=%s", steps[boundary].name, before, after)
			}
		})
	}
}

func TestClaimsBoard_HighConcurrencyLifecycleNoDuplicateTerminalStates(t *testing.T) {
	board := newBenchmarkLifecycleBoard(nil)
	var wg sync.WaitGroup
	errs := make(chan error, phase9ConcurrentLifecycleWorkers*phase9ConcurrentLifecyclePerWorker)
	for worker := range phase9ConcurrentLifecycleWorkers {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for item := range phase9ConcurrentLifecyclePerWorker {
				id := strings.Join([]string{phase9TestSessionID, "worker", stringID(worker), "item", stringID(item)}, "-")
				if err := runBenchmarkLifecycleRoundTrip(context.Background(), board, id); err != nil {
					errs <- err
				}
			}
		}(worker)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	want := phase9ConcurrentLifecycleWorkers * phase9ConcurrentLifecyclePerWorker
	proj := board.Projection()
	if proj.TotalClaims != want || proj.TotalTestaments != want {
		t.Fatalf("projection totals claims=%d testaments=%d, want %d each", proj.TotalClaims, proj.TotalTestaments, want)
	}
	for _, claim := range proj.Claims {
		if claim.LifecycleStatus != ClaimLifecycleSatisfied {
			t.Fatalf("claim %q lifecycle = %q, want satisfied", claim.ID, claim.LifecycleStatus)
		}
	}
}

func TestDurableBoard_LongReplayStaysWithinMemoryBudget(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "session")
	cfg := ClaimsBoardConfig{
		BoardID:    phase9TestBoardID,
		SessionID:  phase9TestSessionID,
		TaskID:     phase9TestTaskID,
		SessionDir: sessionDir,
	}
	db, err := OpenDurableBoard(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for i := range phase9LongReplayLifecycleCount {
		if err := runBenchmarkLifecycleRoundTrip(context.Background(), db.Board(), "long-replay-"+stringID(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	reopened, err := OpenDurableBoard(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	var got uint64
	if after.Alloc > before.Alloc {
		got = after.Alloc - before.Alloc
	}
	if got > phase9LongReplayMemoryBudgetBytes {
		t.Fatalf("replay allocated %d bytes, budget %d", got, phase9LongReplayMemoryBudgetBytes)
	}
	if got := reopened.Board().Projection().TotalClaims; got != phase9LongReplayLifecycleCount {
		t.Fatalf("replayed claims = %d, want %d", got, phase9LongReplayLifecycleCount)
	}
}

type blockingDeltaBus struct {
	started chan struct{}
	release chan struct{}
}

func newBlockingDeltaBus() *blockingDeltaBus {
	return &blockingDeltaBus{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (b *blockingDeltaBus) PublishDelta(ctx context.Context, _ string, _ Delta) error {
	select {
	case <-b.started:
	default:
		close(b.started)
	}
	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *blockingDeltaBus) SubscribeDelta(pattern string, _ DeltaHandler) (DeltaSubscription, error) {
	return noopSubscription{topic: pattern}, nil
}

type phase9LifecycleReplayStep struct {
	name  string
	apply func(context.Context, *ClaimsBoard) error
}

func phase9LifecycleReplaySteps() []phase9LifecycleReplayStep {
	return []phase9LifecycleReplayStep{
		{name: "claim_generated", apply: phase9GenerateClaim},
		{name: "claim_posted", apply: func(ctx context.Context, board *ClaimsBoard) error {
			return board.PostGeneratedClaim(ctx, phase9ReplayClaimID(), "architect", ClaimPostOptions{})
		}},
		{name: "claim_received", apply: func(ctx context.Context, board *ClaimsBoard) error {
			return board.AcknowledgeClaimReceipt(ctx, phase9ReplayClaimID(), "engineer-b")
		}},
		{name: "claim_progressed", apply: func(ctx context.Context, board *ClaimsBoard) error {
			return board.UpdateClaimProgress(ctx, phase9ReplayClaimID(), ClaimProgressUpdate{WorkSummary: "work started"}, "engineer-b")
		}},
		{name: "testament_generated", apply: phase9GenerateTestament},
		{name: "testament_posted", apply: func(ctx context.Context, board *ClaimsBoard) error {
			return board.PostGeneratedTestament(ctx, phase9ReplayTestamentID(), "engineer-b", TestamentPostOptions{})
		}},
		{name: "testament_acknowledged", apply: func(ctx context.Context, board *ClaimsBoard) error {
			return board.AcknowledgeTestamentReceipt(ctx, phase9ReplayTestamentID(), "architect")
		}},
		{name: "testament_validating", apply: func(ctx context.Context, board *ClaimsBoard) error {
			return board.BeginTestamentValidation(ctx, phase9ReplayTestamentID(), "architect")
		}},
		{name: "validation_evaluated", apply: func(ctx context.Context, board *ClaimsBoard) error {
			return board.EvaluateValidation(ctx, phase9ReplayClaimID(), phase9ReplayValidationID(), StatusChange{AgentID: "architect", To: string(ValidationStatusPassed), Reason: "response received"})
		}},
		{name: "testament_validated", apply: func(ctx context.Context, board *ClaimsBoard) error {
			return board.CompleteTestamentValidation(ctx, phase9ReplayTestamentID(), "architect", TestamentLifecycleValidated, "validated")
		}},
	}
}

func phase9GenerateClaim(ctx context.Context, board *ClaimsBoard) error {
	claim := Claim{
		ID:          phase9ReplayClaimID(),
		Title:       "Replay claim",
		Description: "Exercise durable lifecycle replay",
		Relations: []Relation{
			{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: "engineer-b", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		},
		Validations: []*Validation{{
			ID:          phase9ReplayValidationID(),
			Description: "Receive response",
			QualityBar:  "response.received",
			Type:        ValidationTypeReceipt,
			Required:    true,
		}},
	}
	_, err := board.GenerateClaimAction(ctx, Action{ID: "phase9-replay-action", AgentID: "architect", Type: ActionTypeTask}, []Claim{claim}, GenerateClaimActionOptions{IdempotencyKey: "phase9-replay-action"})
	return err
}

func phase9GenerateTestament(ctx context.Context, board *ClaimsBoard) error {
	testament := Testament{
		ID:      phase9ReplayTestamentID(),
		AgentID: "engineer-b",
		Summary: "Replay response complete",
		Relations: []Relation{{
			Related:      phase9ReplayClaimID(),
			RelatedType:  RelatedTypeClaim,
			Relationship: RelationshipClaim,
		}},
		Artifacts: []*Artifact{{
			ID:        "phase9-replay-artifact",
			AgentID:   "engineer-b",
			Kind:      ArtifactKindResponseText,
			Reference: "Replay response complete",
		}},
	}
	_, err := board.GenerateTestamentAction(ctx, Action{ID: "phase9-replay-testament-action", AgentID: "engineer-b", Type: ActionTypeTestament}, []Testament{testament}, GenerateTestamentActionOptions{IdempotencyKey: "phase9-replay-testament-action"})
	return err
}

func phase9ReplayClaimID() string      { return "phase9-replay-claim" }
func phase9ReplayTestamentID() string  { return "phase9-replay-testament" }
func phase9ReplayValidationID() string { return "phase9-replay-validation" }

func normalizedReplayProjection(t *testing.T, projection *ClaimsBoardProjection) string {
	t.Helper()
	data, err := json.Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	var clone ClaimsBoardProjection
	if err := json.Unmarshal(data, &clone); err != nil {
		t.Fatal(err)
	}
	clone.Updated = time.Time{}
	clone.NotificationErrors = nil
	sort.Slice(clone.Actions, func(i, j int) bool { return clone.Actions[i].ID < clone.Actions[j].ID })
	sort.Slice(clone.Claims, func(i, j int) bool { return clone.Claims[i].ID < clone.Claims[j].ID })
	sort.Slice(clone.Testaments, func(i, j int) bool { return clone.Testaments[i].ID < clone.Testaments[j].ID })
	normalized, err := json.Marshal(clone)
	if err != nil {
		t.Fatal(err)
	}
	return string(normalized)
}

func stringID(value int) string {
	return strconv.Itoa(value)
}

func writeTestWALEvent(t *testing.T, walDir string, event walEvent) {
	t.Helper()
	walPath := filepath.Join(walDir, "events.wal.jsonl")
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(walPath, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendTestWALEvent(t *testing.T, walPath string, event walEvent) {
	t.Helper()
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(walPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func firstNonEmptyLine(data string) string {
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func countOutboxRecords(records []ClaimsOutboxRecord, entityType, entityID, mutationKind string) int {
	count := 0
	for _, record := range records {
		if record.EntityType == entityType && record.EntityID == entityID && record.MutationKind == mutationKind {
			count++
		}
	}
	return count
}

func containsText(values []string, needle string) bool {
	for _, value := range values {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
