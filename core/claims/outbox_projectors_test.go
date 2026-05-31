package claims

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
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

func TestClaimsOutbox_ReplayDoesNotDuplicate(t *testing.T) {
	dir := t.TempDir()
	record := ClaimsOutboxRecord{
		BoardID:      "board-1",
		SessionID:    "session-1",
		Sequence:     7,
		EntityType:   "claim",
		EntityID:     "claim-1",
		MutationKind: "claim_issued",
	}
	outbox, err := OpenClaimsOutbox(dir, []string{ProjectorFabric})
	if err != nil {
		t.Fatal(err)
	}
	if err := outbox.Insert(record); err != nil {
		t.Fatal(err)
	}
	if err := outbox.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenClaimsOutbox(dir, []string{ProjectorFabric})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.Insert(record); err != nil {
		t.Fatal(err)
	}
	if got := len(reopened.Records()); got != 1 {
		t.Fatalf("records after replay duplicate insert = %d, want 1", got)
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

func TestClaimsOutbox_LeaseExpires(t *testing.T) {
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
	leaseUntil := time.Now().UTC().Add(time.Minute)
	ok, err := outbox.Claim(pending[0].ID, ProjectorFabric, "worker-1", leaseUntil)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("claim returned false")
	}
	if got := outbox.Pending(ProjectorFabric, 10, time.Now().UTC()); len(got) != 0 {
		t.Fatalf("pending before lease expiry = %d, want 0", len(got))
	}
	if got := outbox.Pending(ProjectorFabric, 10, leaseUntil.Add(time.Second)); len(got) != 1 {
		t.Fatalf("pending after lease expiry = %d, want 1", len(got))
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

func TestFabricProjector_ArtifactPayloadIncludesPresentationAwareness(t *testing.T) {
	collector := activity.NewTestCollector()
	prev := activity.SetDefaultSink(collector)
	defer activity.SetDefaultSink(prev)

	board := NewClaimsBoard(ClaimsBoardConfig{BoardID: "board-1", SessionID: "session-1", TaskID: "task-1"})
	if err := board.PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{testClaim("claim-1", "Plan work")}); err != nil {
		t.Fatal(err)
	}
	if err := board.SubmitTestaments(context.Background(), Action{AgentID: "architect", Type: ActionTypeTestament}, []Testament{{
		AgentID: "architect",
		Summary: "Plan ready.",
		Relations: []Relation{
			{Related: "claim-1", RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim},
		},
		Artifacts: []*Artifact{{
			Kind:      ArtifactKindPlanMarkdown,
			Reference: "### Plan",
			Presentation: &Presentation{
				Audiences: []PresentationAudience{PresentationAudienceUser},
				Surfaces:  []PresentationSurface{PresentationSurfaceChat},
				Format:    PresentationFormatMarkdown,
				Title:     "Plan",
			},
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	artifact := board.Projection().Testaments[0].Artifacts[0]
	record := ClaimsOutboxRecord{
		BoardID:      "board-1",
		SessionID:    "session-1",
		TaskID:       "task-1",
		Sequence:     artifact.Sequence,
		EntityType:   "artifact",
		EntityID:     artifact.ID,
		MutationKind: "artifact_published",
	}

	if err := NewFabricProjector().Project(context.Background(), &record, board); err != nil {
		t.Fatal(err)
	}
	acts := collector.Snapshot()
	if len(acts) == 0 {
		t.Fatal("expected fabric activity")
	}
	var payload map[string]any
	if err := json.Unmarshal(acts[len(acts)-1].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["kind"] != ArtifactKindPlanMarkdown {
		t.Fatalf("payload kind = %v", payload["kind"])
	}
	if payload["artifact_title"] != "Plan" || payload["presentation_title"] != "Plan" {
		t.Fatalf("payload missing presentation title awareness: %+v", payload)
	}
	if payload["presentation"] == nil {
		t.Fatalf("payload missing presentation object: %+v", payload)
	}
}

type failingProjector struct {
	name     string
	failOnce bool
	failed   bool
}

func (p *failingProjector) Name() string { return p.name }

func (p *failingProjector) Project(_ context.Context, record *ClaimsOutboxRecord, _ *ClaimsBoard) error {
	if record == nil || record.EntityType != "claim" {
		return nil
	}
	if p.failOnce && p.failed {
		return nil
	}
	p.failed = true
	return errors.New("projection backend unavailable")
}

type blockingProjector struct {
	name    string
	started chan struct{}
	release chan struct{}
}

func (p *blockingProjector) Name() string {
	if strings.TrimSpace(p.name) != "" {
		return p.name
	}
	return "blocking"
}

func (p *blockingProjector) Project(ctx context.Context, _ *ClaimsOutboxRecord, _ *ClaimsBoard) error {
	select {
	case <-p.started:
	default:
		close(p.started)
	}
	select {
	case <-p.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type countingProjector struct {
	name  string
	count int
	seen  []ClaimsOutboxRecord
}

func (p *countingProjector) Name() string { return p.name }

func (p *countingProjector) Project(_ context.Context, record *ClaimsOutboxRecord, _ *ClaimsBoard) error {
	p.count++
	if record != nil {
		p.seen = append(p.seen, *record)
	}
	return nil
}

type cancelingProjector struct {
	name   string
	cancel context.CancelFunc
}

func (p *cancelingProjector) Name() string { return p.name }

func (p *cancelingProjector) Project(ctx context.Context, _ *ClaimsOutboxRecord, _ *ClaimsBoard) error {
	if p.cancel != nil {
		p.cancel()
	}
	return ctx.Err()
}

type goroutineScope struct{}

func (goroutineScope) Go(_ string, _ time.Duration, fn func(context.Context) error) error {
	go func() { _ = fn(context.Background()) }()
	return nil
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

func TestDurableBoard_WALAppendFailureDoesNotAdvanceHighWater(t *testing.T) {
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
	before := db.Board().HighWaterSequence()
	if err := db.walFile.Close(); err != nil {
		t.Fatal(err)
	}
	err = db.Board().PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{testClaim("claim-1", "Plan work")})
	if err == nil {
		t.Fatal("PostAction succeeded after WAL file was closed")
	}
	if got := db.Board().HighWaterSequence(); got != before {
		t.Fatalf("high-water after failed WAL append = %d, want %d", got, before)
	}
	if got := db.Board().Projection().TotalClaims; got != 0 {
		t.Fatalf("claims after failed WAL append = %d, want 0", got)
	}
}

func TestDurableBoard_OutboxUsesMutationSequenceForRepeatedUpdates(t *testing.T) {
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
	defer db.Close()
	board := db.Board()
	if err := board.PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{testClaim("claim-1", "Plan work")}); err != nil {
		t.Fatal(err)
	}
	if err := board.SetClaimContext(context.Background(), "claim-1", "first update"); err != nil {
		t.Fatal(err)
	}
	if err := board.SetClaimContext(context.Background(), "claim-1", "second update"); err != nil {
		t.Fatal(err)
	}
	sequences := map[uint64]struct{}{}
	for _, record := range db.outbox.Records() {
		if record.EntityID == "claim-1" && record.MutationKind == walEventClaimUpdated {
			sequences[record.Sequence] = struct{}{}
		}
	}
	if len(sequences) != 2 {
		t.Fatalf("claim update outbox mutation sequences = %v, want 2 distinct records", sequences)
	}
}

func TestDurableBoard_ReopenPreservesPendingOutbox(t *testing.T) {
	dir := t.TempDir()
	cfg := ClaimsBoardConfig{
		BoardID:    "board-1",
		SessionID:  "session-1",
		TaskID:     "task-1",
		SessionDir: filepath.Join(dir, "session-1"),
	}
	db, err := OpenDurableBoard(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Board().PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{testClaim("claim-1", "Plan work")}); err != nil {
		t.Fatal(err)
	}
	before := len(db.outbox.Pending(ProjectorFabric, 16, time.Now().UTC()))
	if before == 0 {
		t.Fatal("expected pending fabric projection before close")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenDurableBoard(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	after := len(reopened.outbox.Pending(ProjectorFabric, 16, time.Now().UTC()))
	if after != before {
		t.Fatalf("pending records after reopen = %d, want %d", after, before)
	}
}

func TestDurableBoard_ProjectionWorkerDoesNotBlockMutation(t *testing.T) {
	projector := &blockingProjector{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	db, err := OpenDurableBoard(ClaimsBoardConfig{
		BoardID:    "board-1",
		SessionID:  "session-1",
		TaskID:     "task-1",
		SessionDir: filepath.Join(t.TempDir(), "session-1"),
		Scope:      goroutineScope{},
		Projectors: []ClaimsProjector{
			projector,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	defer close(projector.release)
	done := make(chan error, 1)
	go func() {
		done <- db.Board().PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{testClaim("claim-1", "Plan work")})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("PostAction blocked on projector")
	}
	select {
	case <-projector.started:
	case <-time.After(time.Second):
		t.Fatal("projector worker did not start")
	}
}

func TestDurableBoard_CanonicalProjectionContinuesWhenKnowledgeProjectorBlocks(t *testing.T) {
	projector := &blockingProjector{
		name:    ProjectorKnowledge,
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	bus := newCaptureBus()
	db, err := OpenDurableBoard(ClaimsBoardConfig{
		BoardID:    "board-1",
		SessionID:  "session-1",
		TaskID:     "task-1",
		SessionDir: filepath.Join(t.TempDir(), "session-1"),
		Scope:      goroutineScope{},
		DeltaBus:   bus,
		Projectors: []ClaimsProjector{
			projector,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	defer close(projector.release)

	if err := db.Board().PostAction(context.Background(), Action{AgentID: "guide", Type: ActionTypeTask}, []Claim{directedClaimForOutboxIsolation("claim-1")}); err != nil {
		t.Fatal(err)
	}
	waitForProjectorStart(t, projector.started)

	if err := db.Board().PostAction(context.Background(), Action{AgentID: "guide", Type: ActionTypeTask}, []Claim{directedClaimForOutboxIsolation("claim-2")}); err != nil {
		t.Fatal(err)
	}
	waitForPublishedClaimAction(t, bus, "claim-2", DeltaActionClaimPosted)
}

func TestDurableBoard_ProjectionFailureCreatesErrorArtifact(t *testing.T) {
	projector := &failingProjector{name: "failing"}
	db, err := OpenDurableBoard(ClaimsBoardConfig{
		BoardID:    "board-1",
		SessionID:  "session-1",
		TaskID:     "task-1",
		SessionDir: filepath.Join(t.TempDir(), "session-1"),
		Projectors: []ClaimsProjector{
			projector,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Board().PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{testClaim("claim-1", "Plan work")}); err != nil {
		t.Fatal(err)
	}
	db.DrainOutbox(context.Background(), 32)
	if !projectionArtifactExists(db.Board(), ArtifactKindProjectionError) {
		t.Fatal("projection failure did not create projection_error artifact")
	}
}

func TestDurableBoard_ProjectionSuccessAfterFailureCreatesReceiptArtifact(t *testing.T) {
	projector := &failingProjector{name: "flaky", failOnce: true}
	db, err := OpenDurableBoard(ClaimsBoardConfig{
		BoardID:    "board-1",
		SessionID:  "session-1",
		TaskID:     "task-1",
		SessionDir: filepath.Join(t.TempDir(), "session-1"),
		Projectors: []ClaimsProjector{
			projector,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Board().PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{testClaim("claim-1", "Plan work")}); err != nil {
		t.Fatal(err)
	}
	db.DrainOutbox(context.Background(), 32)
	db.DrainOutbox(context.Background(), 32)
	if !projectionArtifactExists(db.Board(), ArtifactKindProjectionReceipt) {
		t.Fatal("projection success after failure did not create projection_receipt artifact")
	}
	for _, msg := range db.Board().Projection().NotificationErrors {
		if strings.Contains(msg, "projection_error projector=flaky") {
			t.Fatalf("projection warning state not cleared after success: %v", msg)
		}
	}
}

func TestProjectionHealthReportsOutboxLagAndFailures(t *testing.T) {
	projector := &failingProjector{name: "laggy"}
	db, err := OpenDurableBoard(ClaimsBoardConfig{
		BoardID:    "board-1",
		SessionID:  "session-1",
		TaskID:     "task-1",
		SessionDir: filepath.Join(t.TempDir(), "session-1"),
		Projectors: []ClaimsProjector{
			projector,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Board().PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{testClaim("claim-1", "Plan work")}); err != nil {
		t.Fatal(err)
	}
	health := db.ProjectionHealth()
	if health.QueueDepth == 0 || health.MaxLag == 0 {
		t.Fatalf("health did not report pending projection lag: %+v", health)
	}
	if len(health.Projectors) == 0 {
		t.Fatalf("health missing per-projector data: %+v", health)
	}
	db.DrainOutbox(context.Background(), 32)
	health = db.ProjectionHealth()
	if health.RetryCount == 0 {
		t.Fatalf("health did not count retryable projection failure: %+v", health)
	}
	if len(health.Warnings) == 0 {
		t.Fatalf("health missing projection warnings: %+v", health)
	}
}

func TestProjectionHealthReportsLatencyAndBoundedFailureDetails(t *testing.T) {
	projector := &countingProjector{name: "counting"}
	db, err := OpenDurableBoard(ClaimsBoardConfig{
		BoardID:    "board-1",
		SessionID:  "session-1",
		TaskID:     "task-1",
		SessionDir: filepath.Join(t.TempDir(), "session-1"),
		Projectors: []ClaimsProjector{
			projector,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Board().PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{testClaim("claim-1", "Plan work")}); err != nil {
		t.Fatal(err)
	}
	db.DrainOutbox(context.Background(), 32)
	health := db.ProjectionHealth()
	if health.AverageLatency <= 0 || len(health.Projectors) == 0 || health.Projectors[0].AverageLatency <= 0 {
		t.Fatalf("health missing projection latency: %+v", health)
	}
	for i := 0; i < projectionHealthHistoryLimit+10; i++ {
		_ = db.ProjectionHealth(time.Now().Add(time.Duration(i) * time.Millisecond))
	}
	if history := db.ProjectionHealthHistory(0); len(history) != projectionHealthHistoryLimit {
		t.Fatalf("health history len = %d, want %d", len(history), projectionHealthHistoryLimit)
	}

	for i := 0; i < projectionHealthFailureLimit+8; i++ {
		id := "claim-fail-" + strconv.Itoa(i)
		if err := db.Board().PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{testClaim(id, "Plan work")}); err != nil {
			t.Fatal(err)
		}
	}
	for _, rec := range db.outbox.Records() {
		if rec.EntityID == "claim-1" {
			continue
		}
		if err := db.outbox.MarkFailed(rec.ID, "counting", true, errors.New("terminal projection failure")); err != nil {
			t.Fatal(err)
		}
	}
	health = db.ProjectionHealth()
	var ph ProjectionProjectorHealth
	for _, candidate := range health.Projectors {
		if candidate.Projector == "counting" {
			ph = candidate
			break
		}
	}
	if ph.TerminalFailureCount <= projectionHealthFailureLimit {
		t.Fatalf("terminal failure count = %d, want more than bound", ph.TerminalFailureCount)
	}
	if len(ph.TerminalFailureIDs) != projectionHealthFailureLimit {
		t.Fatalf("bounded terminal failure IDs = %d, want %d", len(ph.TerminalFailureIDs), projectionHealthFailureLimit)
	}
}

func TestDurableBoard_RebuildProjectionsDryRunDoesNotCallProjector(t *testing.T) {
	projector := &countingProjector{name: "counting"}
	db, err := OpenDurableBoard(ClaimsBoardConfig{
		BoardID:    "board-1",
		SessionID:  "session-1",
		TaskID:     "task-1",
		SessionDir: filepath.Join(t.TempDir(), "session-1"),
		Projectors: []ClaimsProjector{
			projector,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Board().PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{testClaim("claim-1", "Plan work")}); err != nil {
		t.Fatal(err)
	}
	result, err := db.RebuildProjections(context.Background(), ProjectionRebuildOptions{
		Projectors: []string{"counting"},
		DryRun:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if projector.count != 0 {
		t.Fatalf("dry run called projector %d time(s)", projector.count)
	}
	if result.SelectedRecords == 0 || len(result.Records) == 0 {
		t.Fatalf("dry run did not report selected records: %+v", result)
	}
	if result.Projected != 0 || result.Succeeded != 0 {
		t.Fatalf("dry run mutated projection result counters: %+v", result)
	}
}

func TestDurableBoard_RebuildProjectionsReplaysAndResumesIdempotently(t *testing.T) {
	projector := &countingProjector{name: "counting"}
	db, err := OpenDurableBoard(ClaimsBoardConfig{
		BoardID:    "board-1",
		SessionID:  "session-1",
		TaskID:     "task-1",
		SessionDir: filepath.Join(t.TempDir(), "session-1"),
		Projectors: []ClaimsProjector{
			projector,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Board().PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{testClaim("claim-1", "Plan work")}); err != nil {
		t.Fatal(err)
	}
	first, err := db.RebuildProjections(context.Background(), ProjectionRebuildOptions{
		Projectors: []string{"counting"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Succeeded == 0 || projector.count == 0 {
		t.Fatalf("rebuild did not replay records: result=%+v count=%d", first, projector.count)
	}
	countAfterFirst := projector.count
	second, err := db.RebuildProjections(context.Background(), ProjectionRebuildOptions{
		Projectors:            []string{"counting"},
		ResumeFromLastSuccess: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if projector.count != countAfterFirst {
		t.Fatalf("resume replayed already-succeeded records: before=%d after=%d result=%+v", countAfterFirst, projector.count, second)
	}
	if second.Skipped == 0 {
		t.Fatalf("resume did not report skipped succeeded records: %+v", second)
	}
}

func TestDurableBoard_RebuildProjectionsReportsInterrupt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	projector := &cancelingProjector{name: "canceling", cancel: cancel}
	db, err := OpenDurableBoard(ClaimsBoardConfig{
		BoardID:    "board-1",
		SessionID:  "session-1",
		TaskID:     "task-1",
		SessionDir: filepath.Join(t.TempDir(), "session-1"),
		Projectors: []ClaimsProjector{
			projector,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Board().PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{testClaim("claim-1", "Plan work")}); err != nil {
		t.Fatal(err)
	}
	result, err := db.RebuildProjections(ctx, ProjectionRebuildOptions{Projectors: []string{"canceling"}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Interrupted || result.Failed != 0 {
		t.Fatalf("interrupt result = %+v, want interrupted without projection failure", result)
	}
}

func TestRebuildRegisteredProjectionsTargetsAllSessionsAndReportsMissing(t *testing.T) {
	registry := &SessionBoardRegistry{boards: make(map[string]*ClaimsBoard)}
	projectorOne := &countingProjector{name: "counting"}
	dbOne, err := OpenDurableBoard(ClaimsBoardConfig{
		BoardID:    "board-1",
		SessionID:  "session-1",
		TaskID:     "task-1",
		SessionDir: filepath.Join(t.TempDir(), "session-1"),
		Projectors: []ClaimsProjector{
			projectorOne,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer dbOne.Close()
	projectorTwo := &countingProjector{name: "counting"}
	dbTwo, err := OpenDurableBoard(ClaimsBoardConfig{
		BoardID:    "board-2",
		SessionID:  "session-2",
		TaskID:     "task-2",
		SessionDir: filepath.Join(t.TempDir(), "session-2"),
		Projectors: []ClaimsProjector{
			projectorTwo,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer dbTwo.Close()
	if err := registry.Register("session-1", dbOne.Board()); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register("session-2", dbTwo.Board()); err != nil {
		t.Fatal(err)
	}
	if err := dbOne.Board().PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{testClaim("claim-1", "Plan work")}); err != nil {
		t.Fatal(err)
	}
	if err := dbTwo.Board().PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{testClaim("claim-2", "Plan work")}); err != nil {
		t.Fatal(err)
	}
	batch, err := RebuildRegisteredProjections(context.Background(), registry, dbOne.Board(), ProjectionRebuildTargetOptions{
		AllSessions: true,
		Rebuild: ProjectionRebuildOptions{
			Projectors: []string{"counting"},
			DryRun:     true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if batch.SelectedBoards != 2 || len(batch.Results) != 2 || batch.SelectedRecords == 0 {
		t.Fatalf("batch all-session rebuild = %+v, want two boards with records", batch)
	}
	missing, err := RebuildRegisteredProjections(context.Background(), registry, dbOne.Board(), ProjectionRebuildTargetOptions{
		SessionID: "missing-session",
		Rebuild: ProjectionRebuildOptions{
			DryRun: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing.Warnings) == 0 || !strings.Contains(missing.Warnings[0], "missing-session") {
		t.Fatalf("missing session warnings = %+v", missing.Warnings)
	}
}

func projectionArtifactExists(board *ClaimsBoard, kind string) bool {
	for _, t := range board.Projection().Testaments {
		for _, artifact := range t.Artifacts {
			if artifact != nil && artifact.Kind == kind {
				return true
			}
		}
	}
	return false
}

func directedClaimForOutboxIsolation(id string) Claim {
	return Claim{
		ID:    id,
		Title: "Route work",
		Relations: []Relation{
			{Related: "guide", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: "architect", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		},
	}
}

func waitForProjectorStart(t *testing.T, started <-chan struct{}) {
	t.Helper()
	wait := DefaultClaimsOperationsConfig().Budgets.AuditDeadline
	select {
	case <-started:
	case <-time.After(wait):
		t.Fatalf("projector did not start within %s", wait)
	}
}

func waitForPublishedClaimAction(t *testing.T, bus *captureBus, claimID string, action DeltaAction) {
	t.Helper()
	wait := DefaultClaimsOperationsConfig().Budgets.AuditDeadline
	poll := wait / time.Duration(DefaultClaimsOperationsConfig().Budgets.OutboxProjectionBatchLimit)
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	deadline := time.After(wait)
	for {
		for _, published := range bus.Published() {
			if published.delta.DeltaKind() == string(action) && deltaClaimID(published.delta) == claimID {
				return
			}
		}
		select {
		case <-ticker.C:
		case <-deadline:
			t.Fatalf("claim %s action %s was not published within %s", claimID, action, wait)
		}
	}
}
