package claims

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/activity"
)

func TestClaimsOutbox_InsertIdempotent(t *testing.T) {
	outbox, err := OpenClaimsOutbox(t.TempDir(), []string{ProjectorFabric})
	if err != nil {
		t.Fatal(err)
	}
	defer outbox.Close()

	record := ClaimsOutboxRecord{
		BoardID:      "board-1",
		SessionID:    "session-1",
		Sequence:     7,
		EntityType:   "claim",
		EntityID:     "claim-1",
		MutationKind: "claim_issued",
		CreatedAt:    time.Now().UTC(),
	}
	if err := outbox.Insert(record); err != nil {
		t.Fatal(err)
	}
	if err := outbox.Insert(record); err != nil {
		t.Fatal(err)
	}

	records := outbox.Records()
	if got := len(records); got != 1 {
		t.Fatalf("records = %d, want 1", got)
	}
	if records[0].Projectors[ProjectorFabric].Status != OutboxStatusPending {
		t.Fatalf("projector status = %s, want pending", records[0].Projectors[ProjectorFabric].Status)
	}
}

func TestClaimsOutbox_ProjectorStatusTransitions(t *testing.T) {
	outbox, err := OpenClaimsOutbox(t.TempDir(), []string{ProjectorFabric})
	if err != nil {
		t.Fatal(err)
	}
	defer outbox.Close()

	record := ClaimsOutboxRecord{
		BoardID:      "board-1",
		SessionID:    "session-1",
		Sequence:     7,
		EntityType:   "claim",
		EntityID:     "claim-1",
		MutationKind: "claim_issued",
	}
	if err := outbox.Insert(record); err != nil {
		t.Fatal(err)
	}
	pending := outbox.Pending(ProjectorFabric, 10, time.Now().UTC())
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1", len(pending))
	}
	ok, err := outbox.Claim(pending[0].ID, ProjectorFabric, "worker-1", time.Now().UTC().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("claim returned false")
	}
	if err := outbox.MarkSucceeded(pending[0].ID, ProjectorFabric); err != nil {
		t.Fatal(err)
	}
	if got := outbox.Pending(ProjectorFabric, 10, time.Now().UTC()); len(got) != 0 {
		t.Fatalf("pending after success = %d, want 0", len(got))
	}
}

func TestFabricProjector_ClaimIssuedPayload(t *testing.T) {
	collector := activity.NewTestCollector()
	prev := activity.SetDefaultSink(collector)
	defer activity.SetDefaultSink(prev)

	board := NewClaimsBoard(ClaimsBoardConfig{BoardID: "board-1", SessionID: "session-1", TaskID: "task-1"})
	if err := board.PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{testClaim("claim-1", "Plan work")}); err != nil {
		t.Fatal(err)
	}
	proj := board.Projection()
	record := ClaimsOutboxRecord{
		BoardID:      "board-1",
		SessionID:    "session-1",
		TaskID:       "task-1",
		Sequence:     proj.Claims[0].Sequence,
		EntityType:   "claim",
		EntityID:     "claim-1",
		MutationKind: "claim_issued",
	}

	if err := NewFabricProjector().Project(context.Background(), &record, board); err != nil {
		t.Fatal(err)
	}
	acts := collector.Snapshot()
	if got := len(acts); got == 0 {
		t.Fatal("expected fabric activity")
	}
	act := acts[len(acts)-1]
	if act.ID != stableClaimsActivityID(&record) {
		t.Fatalf("activity ID = %q, want %q", act.ID, stableClaimsActivityID(&record))
	}
	if act.SourceTable != "claims_board" || act.SourceID != "claim-1" {
		t.Fatalf("source = %s/%s, want claims_board/claim-1", act.SourceTable, act.SourceID)
	}
	if act.Subject.Coordinates["board_id"] != "board-1" {
		t.Fatalf("board coordinate = %q", act.Subject.Coordinates["board_id"])
	}
	var payload map[string]any
	if err := json.Unmarshal(act.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["title"] != "Plan work" {
		t.Fatalf("payload title = %v, want Plan work", payload["title"])
	}
}

func TestDurableBoard_BoardMethodsWriteWALAndOutbox(t *testing.T) {
	dir := t.TempDir()
	db, err := OpenDurableBoard(ClaimsBoardConfig{
		BoardID:    "board-1",
		SessionID:  "session-1",
		TaskID:     "task-1",
		SessionDir: filepath.Join(dir, "session-1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	board := db.Board()
	if err := board.PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{testClaim("claim-1", "Plan work")}); err != nil {
		t.Fatal(err)
	}
	high := board.HighWaterSequence()
	if high == 0 {
		t.Fatal("high-water sequence should advance")
	}
	records := db.outbox.Records()
	if len(records) < 2 {
		t.Fatalf("outbox records = %d, want at least action + claim", len(records))
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db2, err := OpenDurableBoard(ClaimsBoardConfig{
		BoardID:    "board-1",
		SessionID:  "session-1",
		TaskID:     "task-1",
		SessionDir: filepath.Join(dir, "session-1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if got := db2.Board().Projection().TotalClaims; got != 1 {
		t.Fatalf("replayed claims = %d, want 1", got)
	}
	if got := db2.Board().HighWaterSequence(); got != high {
		t.Fatalf("replayed high-water = %d, want %d", got, high)
	}
}
