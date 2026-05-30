package claims

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type phase345DroppedSubscription struct {
	topic   string
	dropped uint64
}

func (s phase345DroppedSubscription) Topic() string        { return s.topic }
func (s phase345DroppedSubscription) Unsubscribe() error   { return nil }
func (s phase345DroppedSubscription) DroppedCount() uint64 { return s.dropped }

type phase345RepairProjector struct {
	name  string
	err   error
	calls int
}

func (p *phase345RepairProjector) Name() string {
	return p.name
}

func (p *phase345RepairProjector) Project(context.Context, *ClaimsOutboxRecord, *ClaimsBoard) error {
	p.calls++
	return p.err
}

func TestOperationsConfigNormalizesBoundedDefaultsAndAuditOptOut(t *testing.T) {
	defaults := DefaultClaimsOperationsConfig()
	if !defaults.Audit.Enabled {
		t.Fatal("default operations audit should be enabled")
	}
	if defaults.Budgets.InboxSubscriptionQueueCap <= 0 || defaults.Budgets.OutboxProjectionBatchLimit <= 0 || defaults.Budgets.ShutdownHardDeadline <= 0 {
		t.Fatalf("default budgets are not bounded positive values: %+v", defaults.Budgets)
	}

	cfg := NormalizeClaimsOperationsConfig(ClaimsOperationsConfig{
		Budgets: ClaimsOperationsBudgets{
			InboxSubscriptionQueueCap:  -1,
			OutboxProjectionBatchLimit: -1,
			ShutdownGracePeriod:        -1,
		},
		Audit: ClaimsOperationsAuditConfig{Disabled: true},
	})
	if cfg.Audit.Enabled {
		t.Fatal("disabled operations audit should stay disabled")
	}
	if cfg.Budgets.InboxSubscriptionQueueCap <= 0 || cfg.Budgets.OutboxProjectionBatchLimit <= 0 || cfg.Budgets.ShutdownGracePeriod <= 0 {
		t.Fatalf("negative budgets were not normalized: %+v", cfg.Budgets)
	}
	if len(cfg.Warnings) == 0 {
		t.Fatal("budget normalization should surface warnings")
	}
}

func TestDurableBoardReplayClassifiesUnknownWALEvent(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "session")
	walDir := filepath.Join(sessionDir, "protocols", walNamespace, "board-ops", "wal")
	if err := os.MkdirAll(walDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestWALEvent(t, walDir, walEvent{
		EventID:   uuid.NewString(),
		BoardID:   "board-ops",
		Kind:      "unknown_event_kind_for_test",
		AgentID:   "architect",
		CreatedAt: time.Now().UTC(),
		Payload:   mustJSON(t, map[string]any{"claim_id": "claim-missing"}),
	})

	db, err := OpenDurableBoard(ClaimsBoardConfig{
		BoardID:    "board-ops",
		SessionID:  "session-ops",
		TaskID:     "task-ops",
		SessionDir: sessionDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	issues := db.ReplayIssues()
	if len(issues) != 1 || issues[0].Kind != WALReplayIssueUnknownEventKind {
		t.Fatalf("replay issues = %+v, want unknown event kind", issues)
	}
	if !containsText(db.Board().Projection().NotificationErrors, string(WALReplayIssueUnknownEventKind)) {
		t.Fatalf("notification errors missing classified issue: %v", db.Board().Projection().NotificationErrors)
	}
}

func TestDurableBoardRepairOutboxDryRunReplayAndTerminalFailure(t *testing.T) {
	projector := &phase345RepairProjector{name: "phase345_repair"}
	db, err := OpenDurableBoard(ClaimsBoardConfig{
		BoardID:    "repair-board",
		SessionID:  "repair-session",
		TaskID:     "repair-task",
		SessionDir: t.TempDir(),
		Projectors: []ClaimsProjector{projector},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.Board().PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{testClaim("repair-claim", "Repair projection")}); err != nil {
		t.Fatal(err)
	}
	dry, err := db.RepairOutbox(context.Background(), OutboxRepairOptions{DryRun: true, Limit: 1, Projectors: []string{projector.name}})
	if err != nil {
		t.Fatal(err)
	}
	if dry.WouldProject != 1 || dry.Projected != 0 || !dry.Bounded || projector.calls != 0 {
		t.Fatalf("dry-run report=%+v calls=%d", dry, projector.calls)
	}

	report, err := db.RepairOutbox(context.Background(), OutboxRepairOptions{Projectors: []string{projector.name}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Projected == 0 || projector.calls == 0 {
		t.Fatalf("repair report=%+v calls=%d", report, projector.calls)
	}

	projector.err = errors.New("phase345 projector failure")
	if err := db.Board().PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{testClaim("repair-fail", "Repair projection failure")}); err != nil {
		t.Fatal(err)
	}
	failed, err := db.RepairOutbox(context.Background(), OutboxRepairOptions{Projectors: []string{projector.name}, TerminalOnError: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(failed.TerminalFailures) == 0 || !strings.Contains(failed.TerminalFailures[0].LastError, "phase345 projector failure") {
		t.Fatalf("terminal failure report = %+v", failed)
	}
}

func TestCancelAllActiveRootClaimsAndCycleDetection(t *testing.T) {
	board := NewClaimsBoard(ClaimsBoardConfig{BoardID: "cancel-ops", SessionID: "cancel-session", TaskID: "task"})
	root := testClaim("root", "Root")
	child := testClaim("child", "Child")
	child.Relations = append(child.Relations, Relation{Related: "root", RelatedType: RelatedTypeClaim, Relationship: RelationshipCausedBy})
	if err := board.PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{root, child}); err != nil {
		t.Fatal(err)
	}
	result, err := CancelAllActiveRootClaims(context.Background(), ClaimCancellationOptions{
		Board:  board,
		Reason: "user interrupt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.CancelledClaimIDs) != 2 || result.CancelledClaimIDs[0] != "child" || result.CancelledClaimIDs[1] != "root" {
		t.Fatalf("cancellation order = %+v", result.CancelledClaimIDs)
	}

	cycleBoard := NewClaimsBoard(ClaimsBoardConfig{BoardID: "cancel-cycle", SessionID: "cancel-session", TaskID: "task"})
	left := testClaim("left", "Left")
	right := testClaim("right", "Right")
	left.Relations = append(left.Relations, Relation{Related: "right", RelatedType: RelatedTypeClaim, Relationship: RelationshipDependsOn})
	right.Relations = append(right.Relations, Relation{Related: "left", RelatedType: RelatedTypeClaim, Relationship: RelationshipDependsOn})
	if err := cycleBoard.PostAction(context.Background(), Action{AgentID: "architect", Type: ActionTypeTask}, []Claim{left, right}); err != nil {
		t.Fatal(err)
	}
	cycle, err := CancelClaimTree(context.Background(), ClaimCancellationOptions{
		Board:        cycleBoard,
		RootClaimIDs: []string{"left"},
		Reason:       "cycle interrupt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cycle.CyclicClaimIDs) == 0 {
		t.Fatalf("cycle was not reported: %+v", cycle)
	}
}

func TestClaimCancelRegistryCancelsAndReleasesContexts(t *testing.T) {
	registry := NewClaimCancelRegistry()
	runCtx, registration := registry.Context(context.Background(), "claim-1")
	if ids := registry.ActiveClaimIDs(); len(ids) != 1 || ids[0] != "claim-1" {
		t.Fatalf("active claim ids after register = %v", ids)
	}
	if cancelled := registry.CancelClaim("claim-1"); cancelled != 1 {
		t.Fatalf("CancelClaim cancelled %d contexts, want 1", cancelled)
	}
	select {
	case <-runCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("claim context was not cancelled")
	}
	registration.Done()
	if ids := registry.ActiveClaimIDs(); len(ids) != 0 {
		t.Fatalf("active claim ids after done = %v", ids)
	}
}

func TestBackpressureReporterAndOperationsAuditSurfaceOverflow(t *testing.T) {
	inbox, err := NewClaimsInbox(InboxConfig{AgentID: "architect", SessionID: "session-audit"})
	if err != nil {
		t.Fatal(err)
	}
	inbox.mu.Lock()
	inbox.subscriptions = []DeltaSubscription{phase345DroppedSubscription{topic: "claims.session-audit", dropped: 3}}
	inbox.closed.Store(true)
	inbox.mu.Unlock()

	reporter := NewBackpressureReporter(DefaultClaimsOperationsConfig())
	snapshot, err := ReportInboxBackpressure(context.Background(), nil, inbox, reporter)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.TotalOverflow != 3 || len(snapshot.Events) != 1 {
		t.Fatalf("backpressure snapshot = %+v", snapshot)
	}

	registry := &SessionInboxRegistry{inboxes: make(map[string]*ClaimsInbox)}
	registry.Register("session-audit", "architect", inbox)
	result, err := RunClaimsOperationsAudit(context.Background(), OperationsAuditOptions{InboxRegistry: registry, SessionID: "session-audit"})
	if err != nil {
		t.Fatal(err)
	}
	if !operationsAuditHasFinding(result, "stale_inbox_registry_entry") || !operationsAuditHasFinding(result, "inbox_overflow") {
		t.Fatalf("audit result missing inbox findings: %+v", result)
	}
	if result.Budgets.InboxSubscriptionQueueCap <= 0 || result.Budgets.AuditFindingLimit <= 0 {
		t.Fatalf("audit budgets missing: %+v", result.Budgets)
	}
}

func operationsAuditHasFinding(result OperationsAuditResult, kind string) bool {
	for _, finding := range result.Findings {
		if finding.Kind == kind {
			return true
		}
	}
	return false
}
