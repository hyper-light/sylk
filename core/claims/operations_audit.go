package claims

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	ArtifactKindClaimsOperationsAudit = "claims_operations_audit"
	operationsAuditActorID            = "sys:claims_operations_audit"
	operationsAuditTaskLabel          = "claims_operations_audit"
)

type OperationsAuditSeverity string

const (
	OperationsAuditInfo    OperationsAuditSeverity = "info"
	OperationsAuditWarning OperationsAuditSeverity = "warning"
	OperationsAuditError   OperationsAuditSeverity = "error"
)

type OperationsScopeInventory interface {
	AgentID() string
	WorkerCount() int
}

type OperationsContinuationInventory interface {
	PendingContinuationCount() int
	OrphanContinuationCount() int
	ContinuationOrphanLimit() int
}

type OperationsAuditOptions struct {
	Config              ClaimsOperationsConfig
	BoardRegistry       *SessionBoardRegistry
	InboxRegistry       *SessionInboxRegistry
	ServiceDispatchers  *ServiceDispatcherRegistry
	ValidatorDispatcher *BoardValidatorDispatcher
	Boards              []*ClaimsBoard
	DurableBoards       []*DurableBoard
	Scopes              []OperationsScopeInventory
	Continuations       []OperationsContinuationInventory
	EvidenceBoard       *ClaimsBoard
	SessionID           string
	ActorID             string
	Disabled            bool
}

type OperationsAuditResult struct {
	SessionID   string                         `json:"session_id,omitempty"`
	GeneratedAt time.Time                      `json:"generated_at"`
	Budgets     ClaimsOperationsBudgetSnapshot `json:"budgets"`
	Findings    []OperationsAuditFinding       `json:"findings,omitempty"`
	Truncated   uint64                         `json:"truncated,omitempty"`
}

type OperationsAuditFinding struct {
	Kind          string                  `json:"kind"`
	Severity      OperationsAuditSeverity `json:"severity"`
	SessionID     string                  `json:"session_id,omitempty"`
	BoardID       string                  `json:"board_id,omitempty"`
	ParticipantID string                  `json:"participant_id,omitempty"`
	ResourceID    string                  `json:"resource_id,omitempty"`
	Count         int                     `json:"count,omitempty"`
	Message       string                  `json:"message"`
	Metadata      map[string]any          `json:"metadata,omitempty"`
}

type operationsAuditCollector struct {
	limit     int
	findings  []OperationsAuditFinding
	truncated uint64
}

func RunClaimsOperationsAudit(ctx context.Context, opts OperationsAuditOptions) (OperationsAuditResult, error) {
	opts = normalizeOperationsAuditOptions(opts)
	result := OperationsAuditResult{
		SessionID:   opts.SessionID,
		GeneratedAt: time.Now().UTC(),
		Budgets:     ClaimsOperationsBudgetSnapshotFromConfig(opts.Config),
	}
	if opts.Disabled || !opts.Config.Audit.Enabled {
		return result, nil
	}
	collector := newOperationsAuditCollector(opts.Config.Budgets.AuditFindingLimit)
	if err := runOperationsAuditSteps(ctxOrBackground(ctx), opts, collector); err != nil {
		return result, err
	}
	result.Findings = collector.findings
	result.Truncated = collector.truncated
	if opts.EvidenceBoard != nil && len(result.Findings) > 0 {
		err := RecordClaimsOperationsAuditEvidence(ctxOrBackground(ctx), opts.EvidenceBoard, opts.ActorID, result)
		return result, err
	}
	return result, nil
}

func StartClaimsOperationsAudits(ctx context.Context, scope ScopeProvider, opts OperationsAuditOptions) error {
	opts = normalizeOperationsAuditOptions(opts)
	if scope == nil || opts.Disabled || !opts.Config.Audit.Enabled {
		return nil
	}
	return scheduleClaimsOperationsAudit(ctxOrBackground(ctx), scope, opts)
}

func RecordClaimsOperationsAuditEvidence(ctx context.Context, board *ClaimsBoard, actorID string, result OperationsAuditResult) error {
	if board == nil {
		return nil
	}
	artifact := &Artifact{
		ArtifactName: "claims_operations_audit",
		Kind:         ArtifactKindClaimsOperationsAudit,
		Reference:    fmt.Sprintf("claims operations audit findings=%d", len(result.Findings)),
		Metadata: map[string]any{
			"finding_count": len(result.Findings),
			"truncated":     result.Truncated,
		},
	}
	if err := SetArtifactData(artifact, result); err != nil {
		return err
	}
	_, err := RecordInfrastructureEvidence(ctx, InfrastructureEvidenceOptions{
		Board:          board,
		ActorID:        firstNonEmpty(actorID, operationsAuditActorID),
		SubjectID:      operationsAuditActorID,
		Operation:      "claims_operations_audit",
		IdempotencyKey: operationsAuditIdempotencyKey(result),
		Artifact:       artifact,
		Metadata: map[string]any{
			"finding_count": len(result.Findings),
			"truncated":     result.Truncated,
		},
	})
	return err
}

func normalizeOperationsAuditOptions(opts OperationsAuditOptions) OperationsAuditOptions {
	opts.Config = NormalizeClaimsOperationsConfig(opts.Config)
	opts.SessionID = strings.TrimSpace(opts.SessionID)
	opts.ActorID = firstNonEmpty(strings.TrimSpace(opts.ActorID), operationsAuditActorID)
	return opts
}

func newOperationsAuditCollector(limit int) *operationsAuditCollector {
	if limit <= 0 {
		limit = DefaultClaimsOperationsConfig().Budgets.AuditFindingLimit
	}
	return &operationsAuditCollector{limit: limit}
}

func runOperationsAuditSteps(ctx context.Context, opts OperationsAuditOptions, c *operationsAuditCollector) error {
	steps := []func(context.Context, OperationsAuditOptions, *operationsAuditCollector) error{
		auditOperationBoards,
		auditOperationInboxes,
		auditOperationDispatchers,
		auditOperationScopes,
		auditOperationContinuations,
	}
	for _, step := range steps {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := step(ctx, opts, c); err != nil {
			return err
		}
	}
	return nil
}

func auditOperationBoards(_ context.Context, opts OperationsAuditOptions, c *operationsAuditCollector) error {
	for _, board := range operationAuditBoards(opts) {
		auditOperationBoard(board, c)
	}
	for _, db := range opts.DurableBoards {
		auditOperationDurableBoard(db, c)
	}
	return nil
}

func auditOperationInboxes(_ context.Context, opts OperationsAuditOptions, c *operationsAuditCollector) error {
	if opts.InboxRegistry == nil {
		return nil
	}
	for _, snap := range opts.InboxRegistry.SnapshotStats().Entries {
		auditOperationInbox(snap, opts.Config, c)
	}
	return nil
}

func auditOperationDispatchers(_ context.Context, opts OperationsAuditOptions, c *operationsAuditCollector) error {
	auditOperationServiceDispatchers(opts.ServiceDispatchers, opts.SessionID, c)
	auditOperationValidatorDispatcher(opts.ValidatorDispatcher, opts.SessionID, c)
	return nil
}

func auditOperationScopes(_ context.Context, opts OperationsAuditOptions, c *operationsAuditCollector) error {
	for _, scope := range opts.Scopes {
		if scope != nil && scope.WorkerCount() > 0 {
			c.add(OperationsAuditFinding{Kind: "active_scope_workers", Severity: OperationsAuditWarning, ParticipantID: scope.AgentID(), Count: scope.WorkerCount(), Message: "scope has active workers at audit time"})
		}
	}
	return nil
}

func auditOperationContinuations(_ context.Context, opts OperationsAuditOptions, c *operationsAuditCollector) error {
	for _, continuation := range opts.Continuations {
		auditOperationContinuation(continuation, opts.Config, c)
	}
	return nil
}

func operationAuditBoards(opts OperationsAuditOptions) []*ClaimsBoard {
	seen := make(map[*ClaimsBoard]struct{})
	boards := make([]*ClaimsBoard, 0, len(opts.Boards))
	for _, board := range opts.Boards {
		boards = appendUniqueBoard(boards, seen, board)
	}
	if opts.BoardRegistry == nil {
		return boards
	}
	for _, board := range opts.BoardRegistry.Snapshot() {
		boards = appendUniqueBoard(boards, seen, board)
	}
	return boards
}

func appendUniqueBoard(boards []*ClaimsBoard, seen map[*ClaimsBoard]struct{}, board *ClaimsBoard) []*ClaimsBoard {
	if board == nil {
		return boards
	}
	if _, ok := seen[board]; ok {
		return boards
	}
	seen[board] = struct{}{}
	return append(boards, board)
}

func auditOperationBoard(board *ClaimsBoard, c *operationsAuditCollector) {
	if board == nil {
		return
	}
	if errors := operationBoardNotificationErrors(board); len(errors) > 0 {
		c.add(OperationsAuditFinding{Kind: "board_notification_errors", Severity: OperationsAuditWarning, SessionID: board.SessionID(), BoardID: board.BoardID(), Count: len(errors), Message: "board has pending notification errors", Metadata: map[string]any{"errors": errors}})
	}
	auditOperationProjectionHealth(board.ProjectionHealth(), c)
	if board.durable != nil {
		auditOperationDurableBoard(board.durable, c)
	}
}

func operationBoardNotificationErrors(board *ClaimsBoard) []string {
	board.mu.RLock()
	defer board.mu.RUnlock()
	return board.notificationErrorsSnapshotLocked()
}

func auditOperationDurableBoard(db *DurableBoard, c *operationsAuditCollector) {
	if db == nil || db.board == nil {
		return
	}
	issues := db.ReplayIssues()
	if len(issues) > 0 {
		c.add(OperationsAuditFinding{Kind: "wal_replay_issues", Severity: OperationsAuditWarning, SessionID: db.board.SessionID(), BoardID: db.board.BoardID(), Count: len(issues), Message: "durable board replay reported issues", Metadata: map[string]any{"issues": issues}})
	}
}

func auditOperationProjectionHealth(snap ProjectionHealthSnapshot, c *operationsAuditCollector) {
	if snap.TerminalFailures > 0 {
		c.add(OperationsAuditFinding{Kind: "outbox_terminal_failures", Severity: OperationsAuditError, SessionID: snap.SessionID, BoardID: snap.BoardID, Count: snap.TerminalFailures, Message: "projection outbox has terminal failures"})
	}
	if snap.LeaseExpirations > 0 {
		c.add(OperationsAuditFinding{Kind: "outbox_expired_leases", Severity: OperationsAuditWarning, SessionID: snap.SessionID, BoardID: snap.BoardID, Count: snap.LeaseExpirations, Message: "projection outbox has expired leases"})
	}
}

func auditOperationInbox(snap ClaimsInboxSnapshot, cfg ClaimsOperationsConfig, c *operationsAuditCollector) {
	if snap.Closed {
		c.add(OperationsAuditFinding{Kind: "stale_inbox_registry_entry", Severity: OperationsAuditWarning, SessionID: snap.SessionID, ParticipantID: snap.AgentID, Message: "closed inbox remains registered"})
	}
	if snap.Overflow > 0 {
		c.add(OperationsAuditFinding{Kind: "inbox_overflow", Severity: OperationsAuditWarning, SessionID: snap.SessionID, ParticipantID: snap.AgentID, Count: int(snap.Overflow), Message: "inbox observed bus overflow", Metadata: map[string]any{"delivered_by_class": snap.DeliveredByClass}})
	}
	if snap.OrphanDeltaCount > cfg.Budgets.ContinuationOrphanLimit {
		c.add(OperationsAuditFinding{Kind: "inbox_orphan_buffer_oversize", Severity: OperationsAuditWarning, SessionID: snap.SessionID, ParticipantID: snap.AgentID, Count: snap.OrphanDeltaCount, Message: "inbox orphan buffer exceeds configured budget"})
	}
}

func auditOperationServiceDispatchers(registry *ServiceDispatcherRegistry, sessionID string, c *operationsAuditCollector) {
	if registry == nil {
		return
	}
	snap := registry.Snapshot(sessionID)
	for _, dispatcher := range snap.Dispatchers {
		auditOperationServiceDispatcher(dispatcher, c)
	}
}

func auditOperationServiceDispatcher(stats ServiceDispatcherStats, c *operationsAuditCollector) {
	participantID := stats.Participant.UID
	if stats.Closed {
		c.add(OperationsAuditFinding{Kind: "stale_service_dispatcher", Severity: OperationsAuditWarning, SessionID: stats.SessionID, ParticipantID: participantID, Message: "closed service dispatcher remains registered"})
	}
	if stats.Inflight > stats.Capacity {
		c.add(OperationsAuditFinding{Kind: "service_dispatcher_over_capacity", Severity: OperationsAuditError, SessionID: stats.SessionID, ParticipantID: participantID, Count: stats.Inflight, Message: "service dispatcher exceeds concurrency capacity"})
	}
}

func auditOperationValidatorDispatcher(dispatcher *BoardValidatorDispatcher, sessionID string, c *operationsAuditCollector) {
	if dispatcher == nil {
		return
	}
	stats := dispatcher.Stats()
	if stats.Active > 0 {
		c.add(OperationsAuditFinding{Kind: "active_validator_work", Severity: OperationsAuditWarning, SessionID: sessionID, Count: stats.Active, Message: "validator dispatcher has active work", Metadata: map[string]any{"validation_ids": stats.ActiveValidationIDs}})
	}
}

func auditOperationContinuation(continuation OperationsContinuationInventory, cfg ClaimsOperationsConfig, c *operationsAuditCollector) {
	if continuation == nil {
		return
	}
	orphanLimit := continuation.ContinuationOrphanLimit()
	if orphanLimit <= 0 {
		orphanLimit = cfg.Budgets.ContinuationOrphanLimit
	}
	if continuation.OrphanContinuationCount() > orphanLimit {
		c.add(OperationsAuditFinding{Kind: "continuation_orphan_buffer_oversize", Severity: OperationsAuditWarning, Count: continuation.OrphanContinuationCount(), Message: "continuation orphan buffer exceeds configured budget"})
	}
}

func (c *operationsAuditCollector) add(f OperationsAuditFinding) {
	if c == nil {
		return
	}
	f.Kind = strings.TrimSpace(f.Kind)
	f.Message = strings.TrimSpace(f.Message)
	if f.Severity == "" {
		f.Severity = OperationsAuditWarning
	}
	if len(c.findings) < c.limit {
		c.findings = append(c.findings, f)
		return
	}
	c.truncated++
}

func scheduleClaimsOperationsAudit(ctx context.Context, scope ScopeProvider, opts OperationsAuditOptions) error {
	timeout := opts.Config.Budgets.AuditInterval + opts.Config.Budgets.AuditDeadline
	return scope.Go(operationsAuditTaskLabel, timeout, func(runCtx context.Context) error {
		if waitForOperationsAuditTick(runCtx, ctx, opts.Config.Budgets.AuditInterval) {
			return nil
		}
		auditCtx, cancel := context.WithTimeout(runCtx, opts.Config.Budgets.AuditDeadline)
		defer cancel()
		_, err := RunClaimsOperationsAudit(auditCtx, opts)
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		if runCtx.Err() != nil || ctx.Err() != nil {
			return nil
		}
		return scheduleClaimsOperationsAudit(ctx, scope, opts)
	})
}

func waitForOperationsAuditTick(runCtx, parent context.Context, interval time.Duration) bool {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-runCtx.Done():
		return true
	case <-parent.Done():
		return true
	case <-timer.C:
		return false
	}
}

func operationsAuditIdempotencyKey(result OperationsAuditResult) string {
	return strings.Join([]string{
		"claims_operations_audit",
		sanitizeSystemEvidenceSegment(result.SessionID),
		result.GeneratedAt.UTC().Format(time.RFC3339Nano),
	}, ":")
}
