package claims

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/google/uuid"
)

const (
	phase9TestBoardID   = "board-phase-9"
	phase9TestSessionID = "session-phase-9"
	phase9TestTaskID    = "task-phase-9"
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
