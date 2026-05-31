package claims

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
)

// WAL event kinds.
const (
	walNamespace = "claims_board"

	walEventActionPosted                  = "action_posted"
	walEventClaimActionGenerated          = "claim_action_generated"
	walEventClaimLifecycleTransition      = "claim_lifecycle_transition"
	walEventTestamentActionGenerated      = "testament_action_generated"
	walEventTestamentLifecycleTransition  = "testament_lifecycle_transition"
	walEventClaimUpdated                  = "claim_updated"
	walEventClaimContextSet               = "claim_context_set"
	walEventTestamentContextSet           = "testament_context_set"
	walEventTestamentSubmitted            = "testament_submitted"
	walEventArtifactLifecycleTransition   = "artifact_lifecycle_transition"
	walEventValidationLifecycleTransition = "validation_lifecycle_transition"
	walEventValidationEvaluated           = "validation_evaluated"
	walEventClaimAccepted                 = "claim_accepted"
	walEventClaimRejected                 = "claim_rejected"
	walEventPhaseTransition               = "phase_transition"
	walEventBoardComplete                 = "board_complete"
)

type walEvent struct {
	Sequence  uint64          `json:"-"`
	EventID   string          `json:"event_id"`
	BoardID   string          `json:"board_id"`
	Kind      string          `json:"kind"`
	AgentID   string          `json:"agent_id,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

type walCheckpoint struct {
	BoardID    string                `json:"board_id"`
	PipelineID string                `json:"pipeline_id"`
	TaskID     string                `json:"task_id"`
	SessionID  string                `json:"session_id"`
	Phase      BoardPhase            `json:"phase"`
	Iteration  int                   `json:"iteration"`
	Actions    map[string]*Action    `json:"actions"`
	Claims     map[string]*Claim     `json:"claims"`
	ClaimOrder []string              `json:"claim_order"`
	Testaments map[string]*Testament `json:"testaments"`
	Artifacts  map[string]*Artifact  `json:"artifacts,omitempty"`
	Seq        uint64                `json:"seq"`
	UpdatedAt  time.Time             `json:"updated_at"`
}

type WALReplayIssueKind string

const (
	WALReplayIssueMalformedJSON     WALReplayIssueKind = "malformed_json"
	WALReplayIssueUnknownEventKind  WALReplayIssueKind = "unknown_event_kind"
	WALReplayIssueMissingReference  WALReplayIssueKind = "missing_reference"
	WALReplayIssueDuplicateEvent    WALReplayIssueKind = "duplicate_event"
	WALReplayIssueIllegalTransition WALReplayIssueKind = "illegal_transition"
	WALReplayIssuePanic             WALReplayIssueKind = "panic"
	WALReplayIssueSnapshotInvalid   WALReplayIssueKind = "snapshot_invalid"
)

type WALReplayIssue struct {
	Sequence  uint64             `json:"sequence,omitempty"`
	Kind      WALReplayIssueKind `json:"kind"`
	EventKind string             `json:"event_kind,omitempty"`
	Message   string             `json:"message"`
	Preview   string             `json:"preview,omitempty"`
}

// DurableBoard wraps a ClaimsBoard with WAL persistence.
// WAL is written FIRST, then in-memory state is mutated.
// On crash between WAL write and mutation, recovery replays the WAL
// and produces the correct state.
type DurableBoard struct {
	board *ClaimsBoard

	mu           sync.Mutex
	walDir       string
	walFile      *os.File
	seq          uint64
	seen         map[string]uint64
	outbox       *ClaimsOutbox
	projectors   []ClaimsProjector
	operations   ClaimsOperationsConfig
	replayIssues []WALReplayIssue

	healthMu      sync.Mutex
	healthHistory []ProjectionHealthSnapshot

	projectionMu        sync.Mutex
	projectionScheduled map[string]bool
}

func OpenDurableBoard(cfg ClaimsBoardConfig) (*DurableBoard, error) {
	cfg.Operations = NormalizeClaimsOperationsConfig(cfg.Operations)
	sessionDir := strings.TrimSpace(cfg.SessionDir)
	boardID := strings.TrimSpace(cfg.BoardID)
	if boardID == "" {
		boardID = uuid.NewString()
		cfg.BoardID = boardID
	}
	if sessionDir == "" {
		db := &DurableBoard{
			board:      NewClaimsBoard(cfg),
			seen:       make(map[string]uint64),
			operations: cfg.Operations,
		}
		db.projectors = durableProjectors(cfg)
		if !cfg.DisableOutbox {
			outbox, err := OpenClaimsOutboxWithConfig("", projectorNames(db.projectors), cfg.Operations)
			if err != nil {
				return nil, err
			}
			db.outbox = outbox
		}
		db.board.durable = db
		db.board.canonicalViaOutbox = db.hasProjector(ProjectorCanonicalDelta)
		if db.board.amplifier != nil {
			db.board.amplifier.WithCanonicalDirectEnabled(!db.board.canonicalViaOutbox)
		}
		return db, nil
	}

	walDir := filepath.Join(sessionDir, "protocols", walNamespace, boardID, "wal")
	if err := os.MkdirAll(walDir, 0o755); err != nil {
		return nil, fmt.Errorf("create claims WAL dir: %w", err)
	}

	db := &DurableBoard{walDir: walDir, seen: make(map[string]uint64), operations: cfg.Operations}
	db.projectors = durableProjectors(cfg)

	walPath := filepath.Join(walDir, "events.wal.jsonl")
	f, err := os.OpenFile(walPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open claims WAL: %w", err)
	}
	// Exclusive lock prevents two DurableBoards from writing to the
	// same WAL file concurrently (which would interleave JSON lines).
	if err := lockFile(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock claims WAL: %w", err)
	}
	db.walFile = f

	board, snapshotSeq, err := db.loadSnapshot(cfg)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("load claims snapshot: %w", err)
	}
	db.board = board
	db.board.durable = db
	db.board.canonicalViaOutbox = db.hasProjector(ProjectorCanonicalDelta)
	if db.board.amplifier != nil {
		db.board.amplifier.WithCanonicalDirectEnabled(!db.board.canonicalViaOutbox)
	}
	db.seq = snapshotSeq

	if !cfg.DisableOutbox {
		outboxDir := filepath.Join(filepath.Dir(walDir), "outbox")
		outbox, err := OpenClaimsOutboxWithConfig(outboxDir, projectorNames(db.projectors), cfg.Operations)
		if err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("open claims outbox: %w", err)
		}
		db.outbox = outbox
	}

	if err := db.replayWAL(snapshotSeq); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("replay claims WAL: %w", err)
	}
	db.projectOutbox(context.Background())
	if cfg.Scope == nil && cfg.DeltaBus != nil {
		db.DrainOutbox(context.Background(), db.operations.Budgets.OutboxProjectionBatchLimit)
	}

	return db, nil
}

func (db *DurableBoard) Board() *ClaimsBoard {
	if db == nil {
		return nil
	}
	return db.board
}

func (db *DurableBoard) ReplayIssues() []WALReplayIssue {
	if db == nil {
		return nil
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	return append([]WALReplayIssue(nil), db.replayIssues...)
}

func (db *DurableBoard) Close() error {
	if db == nil || db.walFile == nil {
		return nil
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	unlockFile(db.walFile)
	walErr := db.walFile.Close()
	var outboxErr error
	if db.outbox != nil {
		outboxErr = db.outbox.Close()
	}
	if walErr != nil {
		return walErr
	}
	return outboxErr
}

func (db *DurableBoard) SaveSnapshot() error {
	if db == nil || db.board == nil {
		return nil
	}
	b := db.board
	b.mu.RLock()
	checkpoint := walCheckpoint{
		BoardID:    b.boardID,
		PipelineID: b.pipelineID,
		TaskID:     b.taskID,
		SessionID:  b.sessionID,
		Phase:      b.phase,
		Iteration:  b.iteration,
		Actions:    b.actions,
		Claims:     b.claims,
		ClaimOrder: b.claimOrder,
		Testaments: b.testaments,
		Artifacts:  b.artifacts,
		Seq:        db.seq,
		UpdatedAt:  time.Now().UTC(),
	}
	b.mu.RUnlock()

	data, err := json.Marshal(checkpoint)
	if err != nil {
		return fmt.Errorf("marshal claims snapshot: %w", err)
	}

	snapshotPath := db.snapshotPath()
	tmpPath := snapshotPath + ".tmp"
	if err := writeSnapshotFile(tmpPath, data, db.operations.Durability.WALSyncMode); err != nil {
		return fmt.Errorf("write claims snapshot: %w", err)
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := os.Rename(tmpPath, snapshotPath); err != nil {
		return err
	}
	if db.operations.Durability.WALSyncMode == WALSyncModeAppendSync {
		return syncDir(filepath.Dir(snapshotPath))
	}
	return nil
}

func writeSnapshotFile(path string, data []byte, mode WALSyncMode) error {
	if mode != WALSyncModeAppendSync {
		return os.WriteFile(path, data, 0o644)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func (db *DurableBoard) appendEvent(kind, agentID string, payload any) (uint64, time.Time, error) {
	if db == nil || db.walFile == nil {
		if db != nil {
			db.seq++
			return db.seq, time.Now().UTC(), nil
		}
		return 0, time.Time{}, nil
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("marshal WAL payload: %w", err)
	}

	eventID := uuid.NewString()

	// Dedup by logical event content. A process may retry the same
	// committed lifecycle event with a fresh EventID after a partial
	// append or restart; replay must still collapse it to one board
	// mutation and one outbox record.
	fingerprint := walContentFingerprint(kind, payloadJSON)
	if existingSeq, ok := db.seen[fingerprint]; ok {
		return existingSeq, time.Now().UTC(), nil
	}

	db.seq++
	createdAt := time.Now().UTC()
	event := walEvent{
		Sequence:  db.seq,
		EventID:   eventID,
		BoardID:   db.board.boardID,
		Kind:      kind,
		AgentID:   agentID,
		CreatedAt: createdAt,
		Payload:   payloadJSON,
	}

	line, err := json.Marshal(event)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("marshal WAL event: %w", err)
	}
	line = append(line, '\n')

	if _, err := db.walFile.Write(line); err != nil {
		return 0, time.Time{}, fmt.Errorf("write WAL event: %w", err)
	}
	if db.operations.Durability.WALSyncMode.syncsWALOnAppend() {
		if err := db.walFile.Sync(); err != nil {
			return 0, time.Time{}, fmt.Errorf("sync WAL event: %w", err)
		}
	}

	db.seen[fingerprint] = db.seq
	return db.seq, createdAt, nil
}

func (db *DurableBoard) appendCommittedEvent(kind, agentID string, payload any, outboxRecords []ClaimsOutboxRecord) error {
	if db == nil {
		return nil
	}
	seq, createdAt, err := db.appendEvent(kind, agentID, payload)
	if err != nil {
		return err
	}
	if db.outbox != nil && len(outboxRecords) > 0 {
		for i := range outboxRecords {
			outboxRecords[i].Sequence = seq
			if outboxRecords[i].CreatedAt.IsZero() {
				outboxRecords[i].CreatedAt = createdAt
			}
		}
		if err := db.outbox.InsertMany(outboxRecords); err != nil && db.board != nil {
			db.board.RecordNotificationError("claims outbox: " + err.Error())
		}
	}
	return nil
}

func (db *DurableBoard) projectOutbox(_ context.Context) {
	if db == nil || db.outbox == nil || len(db.projectors) == 0 || db.board == nil {
		return
	}
	if db.board.scope == nil {
		return
	}
	now := time.Now().UTC()
	for _, projector := range db.projectors {
		if projector == nil {
			continue
		}
		name := strings.TrimSpace(projector.Name())
		if name == "" || !db.outbox.HasPending([]string{name}, now) {
			continue
		}
		db.scheduleOutboxProjector(projector)
	}
}

func (db *DurableBoard) scheduleOutboxProjector(projector ClaimsProjector) {
	if db == nil || db.board == nil || db.board.scope == nil || projector == nil {
		return
	}
	name := strings.TrimSpace(projector.Name())
	if name == "" || !db.markProjectionScheduled(name) {
		return
	}
	if err := db.board.scope.Go(outboxProjectorWorkerDescription(name), db.operations.Budgets.AuditDeadline, func(runCtx context.Context) error {
		db.DrainOutboxProjector(runCtx, name, db.operations.Budgets.OutboxProjectionBatchLimit)
		db.clearProjectionScheduled(name)
		if runCtx.Err() == nil && db.outbox != nil && db.outbox.HasPending([]string{name}, time.Now().UTC()) {
			db.scheduleOutboxProjector(projector)
		}
		return nil
	}); err != nil {
		db.clearProjectionScheduled(name)
		db.board.RecordNotificationError("claims outbox projector dispatch: " + err.Error())
	}
}

func (db *DurableBoard) markProjectionScheduled(name string) bool {
	db.projectionMu.Lock()
	defer db.projectionMu.Unlock()
	if db.projectionScheduled == nil {
		db.projectionScheduled = make(map[string]bool)
	}
	if db.projectionScheduled[name] {
		return false
	}
	db.projectionScheduled[name] = true
	return true
}

func (db *DurableBoard) clearProjectionScheduled(name string) {
	db.projectionMu.Lock()
	defer db.projectionMu.Unlock()
	if db.projectionScheduled != nil {
		delete(db.projectionScheduled, name)
	}
}

func outboxProjectorWorkerDescription(name string) string {
	name = strings.NewReplacer(" ", "_", "/", "_", "\\", "_", "\x1f", "_").Replace(strings.TrimSpace(name))
	if name == "" {
		return "claims_outbox_project"
	}
	return "claims_outbox_project_" + name
}

// DrainOutbox synchronously projects pending outbox records. Production
// mutations schedule this on the board scope; tests and repair tools call it
// directly to make projection catch-up deterministic.
func (db *DurableBoard) DrainOutbox(ctx context.Context, limit int) int {
	if db == nil || db.outbox == nil || len(db.projectors) == 0 || db.board == nil {
		return 0
	}
	return db.outbox.ProjectPending(ctx, db.board, db.projectors, limit)
}

// DrainOutboxProjector synchronously projects pending records for one
// projector. Live session boards schedule projectors independently so a slow
// knowledge mirror cannot block canonical claim delivery.
func (db *DurableBoard) DrainOutboxProjector(ctx context.Context, name string, limit int) int {
	if db == nil || db.outbox == nil || db.board == nil {
		return 0
	}
	projector := db.projectorByName(name)
	if projector == nil {
		return 0
	}
	return db.outbox.ProjectPending(ctx, db.board, []ClaimsProjector{projector}, limit)
}

func (db *DurableBoard) projectorByName(name string) ClaimsProjector {
	name = strings.TrimSpace(name)
	if db == nil || name == "" {
		return nil
	}
	for _, projector := range db.projectors {
		if projector != nil && projector.Name() == name {
			return projector
		}
	}
	return nil
}

func (db *DurableBoard) hasProjector(name string) bool {
	if db == nil {
		return false
	}
	for _, p := range db.projectors {
		if p != nil && p.Name() == name {
			return true
		}
	}
	return false
}

// ── Durable wrappers (WAL first, then mutate) ───────────────────────

func (db *DurableBoard) PostAction(ctx context.Context, action Action, claims []Claim) error {
	return db.board.PostAction(ctx, action, claims)
}

func (db *DurableBoard) GenerateClaimAction(ctx context.Context, action Action, claims []Claim, opts GenerateClaimActionOptions) (*GeneratedClaimAction, error) {
	return db.board.GenerateClaimAction(ctx, action, claims, opts)
}

func (db *DurableBoard) GenerateClaim(ctx context.Context, actionID string, claim Claim, opts GenerateClaimActionOptions) (*Claim, error) {
	return db.board.GenerateClaim(ctx, actionID, claim, opts)
}

func (db *DurableBoard) PostGeneratedClaim(ctx context.Context, claimID, actorID string, opts ClaimPostOptions) error {
	return db.board.PostGeneratedClaim(ctx, claimID, actorID, opts)
}

func (db *DurableBoard) PostGeneratedClaims(ctx context.Context, claimIDs []string, actorID string, opts ClaimPostOptions) error {
	return db.board.PostGeneratedClaims(ctx, claimIDs, actorID, opts)
}

func (db *DurableBoard) SubmitTestaments(ctx context.Context, action Action, testaments []Testament) error {
	return db.board.SubmitTestaments(ctx, action, testaments)
}

func (db *DurableBoard) GenerateTestamentAction(ctx context.Context, action Action, testaments []Testament, opts GenerateTestamentActionOptions) (*GeneratedTestamentAction, error) {
	return db.board.GenerateTestamentAction(ctx, action, testaments, opts)
}

func (db *DurableBoard) PostGeneratedTestament(ctx context.Context, testamentID, actorID string, opts TestamentPostOptions) error {
	return db.board.PostGeneratedTestament(ctx, testamentID, actorID, opts)
}

func (db *DurableBoard) PostGeneratedTestaments(ctx context.Context, testamentIDs []string, actorID string, opts TestamentPostOptions) error {
	return db.board.PostGeneratedTestaments(ctx, testamentIDs, actorID, opts)
}

func (db *DurableBoard) AcknowledgeClaimReceipt(ctx context.Context, claimID, receiverID string) error {
	return db.board.AcknowledgeClaimReceipt(ctx, claimID, receiverID)
}

func (db *DurableBoard) AcknowledgeTestamentReceipt(ctx context.Context, testamentID, receiverID string) error {
	return db.board.AcknowledgeTestamentReceipt(ctx, testamentID, receiverID)
}

func (db *DurableBoard) AcknowledgeClaimTestament(ctx context.Context, claimID, testamentID, receiverID string) error {
	return db.board.AcknowledgeClaimTestament(ctx, claimID, testamentID, receiverID)
}

func (db *DurableBoard) RecordClaimLifecycleFailure(ctx context.Context, claimID, actorID string, to ClaimLifecycleStatus, opts LifecycleFailureOptions) error {
	return db.board.RecordClaimLifecycleFailure(ctx, claimID, actorID, to, opts)
}

func (db *DurableBoard) BeginTestamentValidation(ctx context.Context, testamentID, actorID string) error {
	return db.board.BeginTestamentValidation(ctx, testamentID, actorID)
}

func (db *DurableBoard) CompleteTestamentValidation(ctx context.Context, testamentID, actorID string, to TestamentLifecycleStatus, reason string) error {
	return db.board.CompleteTestamentValidation(ctx, testamentID, actorID, to, reason)
}

func (db *DurableBoard) CompleteTestamentValidationError(ctx context.Context, testamentID, actorID string, opts LifecycleFailureOptions) error {
	return db.board.CompleteTestamentValidationError(ctx, testamentID, actorID, opts)
}

func (db *DurableBoard) EvaluateValidation(ctx context.Context, claimID, validationID string, change StatusChange) error {
	return db.board.EvaluateValidation(ctx, claimID, validationID, change)
}

func (db *DurableBoard) TransitionArtifactLifecycle(ctx context.Context, artifactID string, to ArtifactStatus, actorID string, opts ArtifactLifecycleOptions) error {
	return db.board.TransitionArtifactLifecycle(ctx, artifactID, to, actorID, opts)
}

func (db *DurableBoard) AcknowledgeArtifactReceipt(ctx context.Context, artifactID, receiverID string) error {
	return db.board.AcknowledgeArtifactReceipt(ctx, artifactID, receiverID)
}

func (db *DurableBoard) ReceiveArtifact(ctx context.Context, artifactID, receiverID string) (*Artifact, error) {
	return db.board.ReceiveArtifact(ctx, artifactID, receiverID)
}

func (db *DurableBoard) RecordArtifactReceiptFailure(ctx context.Context, artifactID, receiverID string, artifactErr *ArtifactError) error {
	return db.board.RecordArtifactReceiptFailure(ctx, artifactID, receiverID, artifactErr)
}

func (db *DurableBoard) BeginArtifactValidation(ctx context.Context, artifactID, actorID string) error {
	return db.board.BeginArtifactValidation(ctx, artifactID, actorID)
}

func (db *DurableBoard) CompleteArtifactValidation(ctx context.Context, artifactID, actorID string, validated bool, artifactErr *ArtifactError) error {
	return db.board.CompleteArtifactValidation(ctx, artifactID, actorID, validated, artifactErr)
}

func (db *DurableBoard) TransitionValidationLifecycle(ctx context.Context, claimID, validationID string, to ValidationStatus, actorID string, opts ValidationLifecycleOptions) error {
	return db.board.TransitionValidationLifecycle(ctx, claimID, validationID, to, actorID, opts)
}

func (db *DurableBoard) MarkValidationReady(ctx context.Context, claimID, validationID, actorID string) error {
	return db.board.MarkValidationReady(ctx, claimID, validationID, actorID)
}

func (db *DurableBoard) BeginValidation(ctx context.Context, claimID, validationID, actorID, artifactID string) error {
	return db.board.BeginValidation(ctx, claimID, validationID, actorID, artifactID)
}

func (db *DurableBoard) BeginValidationQualityBar(ctx context.Context, claimID, validationID, actorID, artifactID string) error {
	return db.board.BeginValidationQualityBar(ctx, claimID, validationID, actorID, artifactID)
}

func (db *DurableBoard) CompleteValidationLifecycle(ctx context.Context, claimID, validationID, actorID string, status ValidationStatus, opts ValidationLifecycleOptions) error {
	return db.board.CompleteValidationLifecycle(ctx, claimID, validationID, actorID, status, opts)
}

func (db *DurableBoard) BuildValidationResultTestament(ctx context.Context, req ResultTestamentRequest) (ResultTestamentResult, error) {
	return db.board.BuildValidationResultTestament(ctx, req)
}

func (db *DurableBoard) RejectClaim(ctx context.Context, claimID string, change StatusChange, replacements *Action, replacementClaims []Claim) error {
	return db.board.RejectClaim(ctx, claimID, change, replacements, replacementClaims)
}

func (db *DurableBoard) TransitionToValidation(ctx context.Context) error {
	return db.board.TransitionToValidation(ctx)
}

func (db *DurableBoard) TransitionToImplementation(ctx context.Context) error {
	return db.board.TransitionToImplementation(ctx)
}

func (db *DurableBoard) MarkComplete(ctx context.Context) error {
	return db.board.MarkComplete(ctx)
}

// ── Recovery ────────────────────────────────────────────────────────

func (db *DurableBoard) loadSnapshot(cfg ClaimsBoardConfig) (*ClaimsBoard, uint64, error) {
	data, err := os.ReadFile(db.snapshotPath())
	if err != nil {
		if os.IsNotExist(err) {
			return NewClaimsBoard(cfg), 0, nil
		}
		return nil, 0, err
	}

	var checkpoint walCheckpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		board := NewClaimsBoard(cfg)
		msg := "claims snapshot invalid: " + err.Error()
		board.RecordNotificationError(msg)
		db.addReplayIssue(WALReplayIssue{Kind: WALReplayIssueSnapshotInvalid, Message: msg})
		return board, 0, nil
	}
	if err := validateWALCheckpoint(cfg, checkpoint); err != nil {
		board := NewClaimsBoard(cfg)
		msg := "claims snapshot invalid: " + err.Error()
		board.RecordNotificationError(msg)
		db.addReplayIssue(WALReplayIssue{Kind: WALReplayIssueSnapshotInvalid, Message: msg})
		return board, 0, nil
	}

	board := NewClaimsBoard(cfg)
	board.boardID = checkpoint.BoardID
	board.pipelineID = checkpoint.PipelineID
	board.taskID = checkpoint.TaskID
	board.sessionID = checkpoint.SessionID
	board.phase = checkpoint.Phase
	board.iteration = checkpoint.Iteration
	if checkpoint.Actions != nil {
		board.actions = checkpoint.Actions
	}
	if checkpoint.Claims != nil {
		board.claims = checkpoint.Claims
	}
	board.claimOrder = checkpoint.ClaimOrder
	if checkpoint.Testaments != nil {
		board.testaments = checkpoint.Testaments
	}
	if checkpoint.Artifacts != nil {
		board.artifacts = checkpoint.Artifacts
	}
	board.ensureArtifactIndexLocked()
	board.rebuildDerivedState()

	return board, checkpoint.Seq, nil
}

func validateWALCheckpoint(cfg ClaimsBoardConfig, checkpoint walCheckpoint) error {
	if strings.TrimSpace(cfg.BoardID) != "" && checkpoint.BoardID != cfg.BoardID {
		return fmt.Errorf("board id %q does not match %q", checkpoint.BoardID, cfg.BoardID)
	}
	if strings.TrimSpace(cfg.SessionID) != "" && checkpoint.SessionID != cfg.SessionID {
		return fmt.Errorf("session id %q does not match %q", checkpoint.SessionID, cfg.SessionID)
	}
	if len(checkpoint.Claims) != len(uniqueStringList(checkpoint.ClaimOrder)) {
		return fmt.Errorf("claim order does not match claim index")
	}
	for _, claimID := range checkpoint.ClaimOrder {
		if checkpoint.Claims[claimID] == nil {
			return fmt.Errorf("claim order references missing claim %q", claimID)
		}
	}
	return nil
}

func uniqueStringList(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func (db *DurableBoard) replayWAL(afterSeq uint64) error {
	walPath := filepath.Join(db.walDir, "events.wal.jsonl")
	data, err := os.ReadFile(walPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var seq uint64
	var issues []WALReplayIssue
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		seq++

		var event walEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			issues = append(issues, WALReplayIssue{
				Sequence: seq,
				Kind:     WALReplayIssueMalformedJSON,
				Message:  err.Error(),
				Preview:  truncateForLog(line, db.operations.Budgets.ReplayErrorPreviewBytes),
			})
			continue
		}
		event.Sequence = seq

		fp := walContentFingerprint(event.Kind, event.Payload)
		if seq <= afterSeq {
			db.seen[fp] = seq
			continue
		}
		if _, ok := db.seen[fp]; ok {
			issues = append(issues, WALReplayIssue{
				Sequence:  seq,
				Kind:      WALReplayIssueDuplicateEvent,
				EventKind: event.Kind,
				Message:   "duplicate logical WAL event collapsed",
			})
			continue
		}
		db.seen[fp] = seq

		if err := db.applyEventRecovering(&event); err != nil {
			issues = append(issues, classifyWALReplayIssue(seq, event.Kind, err))
			continue
		}
	}
	db.seq = seq

	// Surface corruption as notification errors on the board so the
	// first projection query exposes them to agents. Agents can then
	// record them as testament error artifacts.
	if len(issues) > 0 && db.board != nil {
		db.board.mu.Lock()
		for _, issue := range issues {
			db.board.appendNotificationErrorLocked(issue.Notification())
		}
		db.board.mu.Unlock()
	}
	db.addReplayIssues(issues)
	if db.board != nil {
		db.board.rebuildDerivedState()
	}
	return nil
}

func (issue WALReplayIssue) Notification() string {
	parts := []string{
		"WAL replay",
		"seq=" + fmt.Sprint(issue.Sequence),
		"kind=" + string(issue.Kind),
	}
	if issue.EventKind != "" {
		parts = append(parts, "event="+issue.EventKind)
	}
	if issue.Message != "" {
		parts = append(parts, "message="+issue.Message)
	}
	if issue.Preview != "" {
		parts = append(parts, "preview="+issue.Preview)
	}
	return strings.Join(parts, " ")
}

func (db *DurableBoard) addReplayIssue(issue WALReplayIssue) {
	db.addReplayIssues([]WALReplayIssue{issue})
}

func (db *DurableBoard) addReplayIssues(issues []WALReplayIssue) {
	if db == nil || len(issues) == 0 {
		return
	}
	db.mu.Lock()
	db.replayIssues = append(db.replayIssues, issues...)
	db.mu.Unlock()
}

func (db *DurableBoard) applyEventRecovering(event *walEvent) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic during WAL replay: %v", recovered)
		}
	}()
	return db.applyEvent(event)
}

func classifyWALReplayIssue(seq uint64, eventKind string, err error) WALReplayIssue {
	issue := WALReplayIssue{Sequence: seq, EventKind: eventKind, Message: err.Error()}
	var lifecycleErr *LifecycleTransitionError
	switch {
	case errors.As(err, &lifecycleErr):
		issue.Kind = WALReplayIssueIllegalTransition
	case strings.Contains(err.Error(), "missing ") && strings.Contains(err.Error(), "replay event"):
		issue.Kind = WALReplayIssueMissingReference
	case strings.Contains(err.Error(), "unknown WAL event kind"):
		issue.Kind = WALReplayIssueUnknownEventKind
	case strings.Contains(err.Error(), "panic during WAL replay"):
		issue.Kind = WALReplayIssuePanic
	default:
		issue.Kind = WALReplayIssueIllegalTransition
	}
	return issue
}

func (db *DurableBoard) applyEvent(event *walEvent) error {
	if event == nil || db.board == nil {
		return nil
	}
	switch event.Kind {
	case walEventClaimActionGenerated:
		return db.applyClaimActionGenerated(event)
	case walEventActionPosted:
		return db.applyActionPosted(event)
	case walEventClaimLifecycleTransition:
		return db.applyClaimLifecycleTransition(event)
	case walEventTestamentActionGenerated:
		return db.applyTestamentActionGenerated(event)
	case walEventTestamentLifecycleTransition:
		return db.applyTestamentLifecycleTransition(event)
	case walEventClaimUpdated:
		return db.applyClaimUpdated(event)
	case walEventClaimContextSet:
		return db.applyClaimContextSet(event)
	case walEventTestamentContextSet:
		return db.applyTestamentContextSet(event)
	case walEventTestamentSubmitted:
		return db.applyTestamentSubmitted(event)
	case walEventArtifactLifecycleTransition:
		return db.applyArtifactLifecycleTransition(event)
	case walEventValidationLifecycleTransition:
		return db.applyValidationLifecycleTransition(event)
	case walEventValidationEvaluated:
		return db.applyValidationEvaluated(event)
	case walEventClaimRejected:
		return db.applyClaimRejected(event)
	case walEventPhaseTransition:
		return db.applyPhaseTransition(event)
	case walEventBoardComplete:
		seq := db.board.seq.Load() + 1
		var payload struct {
			Sequence uint64 `json:"sequence"`
		}
		if len(event.Payload) > 0 {
			_ = json.Unmarshal(event.Payload, &payload)
			if payload.Sequence != 0 {
				seq = payload.Sequence
			}
		}
		db.board.phase = BoardPhaseComplete
		if db.board.seq.Load() < seq {
			db.board.seq.Store(seq)
		}
		db.insertReplayOutbox([]ClaimsOutboxRecord{
			db.board.outboxRecordLocked(event.Sequence, "board", db.board.boardID, walEventBoardComplete, event.CreatedAt),
		})
		return nil
	default:
		return fmt.Errorf("unknown WAL event kind %q", event.Kind)
	}
}

func (db *DurableBoard) applyClaimActionGenerated(event *walEvent) error {
	var payload struct {
		Action Action  `json:"action"`
		Claims []Claim `json:"claims"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	b := db.board
	b.actions[payload.Action.ID] = &payload.Action
	for i := range payload.Claims {
		c := &payload.Claims[i]
		if _, exists := b.claims[c.ID]; exists {
			continue
		}
		b.claims[c.ID] = c
		b.claimOrder = append(b.claimOrder, c.ID)
	}
	return nil
}

func (db *DurableBoard) applyActionPosted(event *walEvent) error {
	var payload struct {
		Action Action  `json:"action"`
		Claims []Claim `json:"claims"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	b := db.board
	b.actions[payload.Action.ID] = &payload.Action
	for i := range payload.Claims {
		c := &payload.Claims[i]
		// Skip duplicates — the board may have rejected this at runtime
		// but the WAL recorded it (WAL-first ordering). Silently
		// skipping matches the board's runtime behavior of returning an
		// error on duplicate IDs.
		if _, exists := b.claims[c.ID]; exists {
			continue
		}
		b.claims[c.ID] = c
		b.claimOrder = append(b.claimOrder, c.ID)
	}
	records := b.outboxRecordsForPostActionLocked(payload.Action, payload.Claims, event.CreatedAt)
	setOutboxRecordSequence(records, event.Sequence)
	db.insertReplayOutbox(records)
	return nil
}

func (db *DurableBoard) applyClaimLifecycleTransition(event *walEvent) error {
	var payload struct {
		ClaimIDs          []string             `json:"claim_ids"`
		To                ClaimLifecycleStatus `json:"to"`
		AgentID           string               `json:"agent_id"`
		Reason            string               `json:"reason"`
		Changed           time.Time            `json:"changed"`
		FailureAction     *Action              `json:"failure_action,omitempty"`
		FailureTestaments []Testament          `json:"failure_testaments,omitempty"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	changed := firstNonZeroTime(payload.Changed, event.CreatedAt)
	if payload.FailureAction != nil {
		db.board.actions[payload.FailureAction.ID] = payload.FailureAction
	}
	for i := range payload.FailureTestaments {
		t := &payload.FailureTestaments[i]
		db.board.testaments[t.ID] = t
	}
	for _, claimID := range payload.ClaimIDs {
		if _, ok := db.board.claims[claimID]; !ok {
			return replayMissingReference("claim", claimID)
		}
	}
	for _, claimID := range payload.ClaimIDs {
		c := db.board.claims[claimID]
		db.board.transitionClaimLifecycleLocked(c, payload.To, payload.AgentID, payload.Reason, changed)
		if payload.To.IsFailure() && !c.Status.IsTerminal() {
			c.Status = ClaimStatusRejected
		}
	}
	return nil
}

func (db *DurableBoard) applyClaimUpdated(event *walEvent) error {
	var payload struct {
		ClaimID    string      `json:"claim_id"`
		AgentID    string      `json:"agent_id"`
		FromStatus ClaimStatus `json:"from_status"`
		Accessed   time.Time   `json:"accessed"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	c, ok := db.board.claims[payload.ClaimID]
	if !ok {
		return replayMissingReference("claim", payload.ClaimID)
	}
	now := payload.Accessed
	if now.IsZero() {
		now = event.CreatedAt
	}
	if c.Status == ClaimStatusPending {
		c.StatusHistory = append(c.StatusHistory, StatusChange{
			From:    string(ClaimStatusPending),
			To:      string(ClaimStatusInProgress),
			Reason:  "work started",
			AgentID: payload.AgentID,
			Changed: now,
		})
		c.Status = ClaimStatusInProgress
	}
	if CanTransitionClaimLifecycle(c.LifecycleStatus, ClaimLifecycleProgressed) {
		db.board.transitionClaimLifecycleLocked(c, ClaimLifecycleProgressed, payload.AgentID, "work progressed", now)
	}
	c.Accessed = now
	db.insertReplayOutbox([]ClaimsOutboxRecord{
		db.board.outboxRecordLocked(event.Sequence, "claim", c.ID, walEventClaimUpdated, event.CreatedAt),
	})
	return nil
}

func (db *DurableBoard) applyClaimContextSet(event *walEvent) error {
	var payload struct {
		ClaimID      string    `json:"claim_id"`
		Context      string    `json:"context"`
		TransitionID int64     `json:"transition_id"`
		Accessed     time.Time `json:"accessed"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	c, ok := db.board.claims[payload.ClaimID]
	if !ok {
		return replayMissingReference("claim", payload.ClaimID)
	}
	c.Context = payload.Context
	c.ContextTransition = payload.TransitionID
	c.Accessed = firstNonZeroTime(payload.Accessed, event.CreatedAt)
	db.insertReplayOutbox([]ClaimsOutboxRecord{
		db.board.outboxRecordLocked(event.Sequence, "claim", c.ID, walEventClaimUpdated, event.CreatedAt),
	})
	return nil
}

func (db *DurableBoard) applyTestamentActionGenerated(event *walEvent) error {
	var payload struct {
		Action     Action      `json:"action"`
		Testaments []Testament `json:"testaments"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	b := db.board
	b.actions[payload.Action.ID] = &payload.Action
	for i := range payload.Testaments {
		t := &payload.Testaments[i]
		if _, exists := b.testaments[t.ID]; exists {
			continue
		}
		b.testaments[t.ID] = t
		for _, artifact := range t.Artifacts {
			b.indexArtifactLocked(artifact)
		}
	}
	return nil
}

func (db *DurableBoard) applyTestamentLifecycleTransition(event *walEvent) error {
	var payload struct {
		TestamentIDs []string                 `json:"testament_ids"`
		To           TestamentLifecycleStatus `json:"to"`
		AgentID      string                   `json:"agent_id"`
		Reason       string                   `json:"reason"`
		Changed      time.Time                `json:"changed"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	changed := firstNonZeroTime(payload.Changed, event.CreatedAt)
	for _, testamentID := range payload.TestamentIDs {
		if _, ok := db.board.testaments[testamentID]; !ok {
			return replayMissingReference("testament", testamentID)
		}
	}
	for _, testamentID := range payload.TestamentIDs {
		t := db.board.testaments[testamentID]
		db.board.transitionTestamentLifecycleLocked(t, payload.To, payload.AgentID, payload.Reason, changed)
		if payload.To == TestamentLifecyclePosted {
			db.board.recordClaimTestamentGeneratedLocked(t, changed)
		}
		if payload.To == TestamentLifecycleReceived {
			if claimID := ClaimIDFromRelations(t.Relations); claimID != "" {
				if c, found := db.board.claims[claimID]; found {
					db.board.transitionClaimLifecycleLocked(c, ClaimLifecycleTestamentAcknowledged, payload.AgentID, "testament acknowledged", changed)
				} else {
					return replayMissingReference("claim", claimID)
				}
			}
		}
		db.board.syncClaimLifecycleForTestamentValidationLocked(t, payload.To, payload.AgentID, payload.Reason, changed)
	}
	return nil
}

func (db *DurableBoard) applyTestamentContextSet(event *walEvent) error {
	var payload struct {
		TestamentID  string    `json:"testament_id"`
		Context      string    `json:"context"`
		TransitionID int64     `json:"transition_id"`
		Accessed     time.Time `json:"accessed"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	t, ok := db.board.testaments[payload.TestamentID]
	if !ok {
		return replayMissingReference("testament", payload.TestamentID)
	}
	t.Context = payload.Context
	t.ContextTransition = payload.TransitionID
	t.Accessed = firstNonZeroTime(payload.Accessed, event.CreatedAt)
	db.insertReplayOutbox([]ClaimsOutboxRecord{
		db.board.outboxRecordLocked(event.Sequence, "testament", t.ID, walEventTestamentSubmitted, event.CreatedAt),
	})
	return nil
}

func (db *DurableBoard) applyTestamentSubmitted(event *walEvent) error {
	var payload struct {
		Action     Action      `json:"action"`
		Testaments []Testament `json:"testaments"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	b := db.board
	b.actions[payload.Action.ID] = &payload.Action
	for i := range payload.Testaments {
		t := &payload.Testaments[i]
		b.testaments[t.ID] = t
		for _, artifact := range t.Artifacts {
			b.indexArtifactLocked(artifact)
		}
		claimRel := FindRelation(t.Relations, RelationshipClaim)
		if claimRel != nil {
			if c, ok := b.claims[claimRel.Related]; ok && !c.Status.IsTerminal() {
				c.Status = ClaimStatusTestified
				if DeriveTestamentVerdict(t.Artifacts) == TestamentVerdictError {
					failReceiptValidationsLocked(c, t.AgentID, event.CreatedAt, "receipt failed: error testament submitted")
					if claimHasRequiredFailedValidation(c) {
						c.Status = ClaimStatusRejected
					}
				} else {
					autoPassReceiptValidationsLocked(c, t.AgentID, event.CreatedAt)
				}
				if c.AllValidationsPassed() && !c.Status.IsTerminal() {
					c.Status = ClaimStatusAccepted
				}
			}
		}
	}
	records := b.outboxRecordsForSubmitTestamentsLocked(payload.Action, payload.Testaments, event.CreatedAt)
	setOutboxRecordSequence(records, event.Sequence)
	db.insertReplayOutbox(records)
	return nil
}

func (db *DurableBoard) applyArtifactLifecycleTransition(event *walEvent) error {
	var payload struct {
		ArtifactID   string                   `json:"artifact_id"`
		Artifact     *Artifact                `json:"artifact,omitempty"`
		To           ArtifactStatus           `json:"to"`
		AgentID      string                   `json:"agent_id"`
		Reason       string                   `json:"reason"`
		Changed      time.Time                `json:"changed"`
		Error        *ArtifactError           `json:"error,omitempty"`
		ValidationID string                   `json:"validation_id,omitempty"`
		TestamentTo  TestamentLifecycleStatus `json:"testament_to,omitempty"`
		ClaimTo      ClaimLifecycleStatus     `json:"claim_to,omitempty"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	artifact, testament, claim, ok := db.board.findArtifactForMutationLocked(payload.ArtifactID)
	createdFromPayload := false
	if !ok {
		if payload.Artifact == nil || !artifactGenerationReplayStatus(payload.To) {
			return replayMissingReference("artifact", payload.ArtifactID)
		}
		db.board.indexArtifactLocked(payload.Artifact)
		artifact = payload.Artifact
		createdFromPayload = true
	}
	changed := firstNonZeroTime(payload.Changed, event.CreatedAt)
	if artifact.Status != payload.To {
		if _, err := TransitionArtifactStatus(artifact, payload.To, payload.AgentID, payload.Reason, changed); err != nil {
			return err
		}
	}
	if payload.Error != nil && !createdFromPayload {
		artifact.Errors = append(artifact.Errors, cloneArtifactError(payload.Error))
	}
	artifact.Accessed = changed
	propagation := artifactTerminalPropagation{TestamentTo: payload.TestamentTo, ClaimTo: payload.ClaimTo}
	if propagation.TestamentTo == "" {
		propagation = db.board.artifactTerminalPropagationLocked(artifact, testament, claim, payload.To, payload.Error)
	}
	claimSynced := db.board.applyArtifactTerminalPropagationLocked(testament, propagation, payload.AgentID, payload.Reason, changed)
	action := mustArtifactLifecycleDeltaAction(payload.To)
	records := []ClaimsOutboxRecord{
		db.board.outboxRecordLocked(event.Sequence, RelatedTypeArtifact, artifact.ID, string(action), event.CreatedAt),
	}
	if propagation.TestamentTo != "" {
		records = append(records, db.board.outboxRecordsForTestamentLifecyclePtrLocked([]*Testament{testament}, propagation.TestamentTo, event.CreatedAt)...)
	}
	if claimSynced && claim != nil {
		records = append(records, db.board.outboxRecordsForClaimLifecyclePtrLocked([]*Claim{claim}, propagation.ClaimTo, event.CreatedAt)...)
	}
	db.insertReplayOutbox(records)
	return nil
}

func (db *DurableBoard) applyValidationLifecycleTransition(event *walEvent) error {
	var payload struct {
		ClaimID          string           `json:"claim_id"`
		ValidationID     string           `json:"validation_id"`
		To               ValidationStatus `json:"to"`
		AgentID          string           `json:"agent_id"`
		Reason           string           `json:"reason"`
		Changed          time.Time        `json:"changed"`
		TargetArtifactID string           `json:"target_artifact_id,omitempty"`
		ResultArtifact   *Artifact        `json:"result_artifact,omitempty"`
		ResultArtifactID string           `json:"result_artifact_id,omitempty"`
		Error            *ValidationError `json:"error,omitempty"`
		EvaluatorRef     *ParticipantRef  `json:"evaluator_ref,omitempty"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	validation, claim, ok := db.board.findValidationForMutationLocked(payload.ClaimID, payload.ValidationID)
	if !ok {
		return replayMissingReference("validation", payload.ValidationID)
	}
	changed := firstNonZeroTime(payload.Changed, event.CreatedAt)
	to := validationLifecycleTarget(validation, payload.To)
	if validation.Status != to && !CanTransitionValidationStatus(validation.Status, to) {
		return newValidationLifecycleTransitionError(validation.ID, validation.Status, to, payload.AgentID, "validation replay transition is not allowed")
	}
	opts := ValidationLifecycleOptions{
		Reason:           payload.Reason,
		TargetArtifactID: payload.TargetArtifactID,
		ResultArtifact:   payload.ResultArtifact,
		ResultArtifactID: payload.ResultArtifactID,
		Error:            payload.Error,
		EvaluatorRef:     payload.EvaluatorRef,
	}
	resultArtifactID := db.board.recordValidationLifecycleMutationLocked(claim, validation, to, payload.AgentID, opts, changed)
	accepted := claimAcceptedAfterValidation(claim, validation.ID, to)
	claimStatus, claimLifecycle, hasClaimOutcome := validationClaimOutcome(claim, validation, to, accepted)
	db.board.recordValidationClaimOutcomeLocked(claim, claimStatus, claimLifecycle, hasClaimOutcome, payload.AgentID, payload.Reason, changed)
	records := []ClaimsOutboxRecord{db.board.outboxRecordLocked(event.Sequence, RelatedTypeValidation, validation.ID, string(mustValidationLifecycleDeltaAction(to)), event.CreatedAt)}
	if payload.ResultArtifact != nil {
		artifactID := firstNonEmpty(resultArtifactID, payload.ResultArtifact.ID)
		records = append(records, db.board.outboxRecordLocked(event.Sequence, RelatedTypeArtifact, artifactID, string(DeltaActionArtifactGenerated), event.CreatedAt))
	}
	if hasClaimOutcome {
		records = append(records, validationClaimOutcomeOutboxRecordLocked(db.board, claim, claimStatus, event.CreatedAt))
	}
	db.insertReplayOutbox(records)
	return nil
}

func artifactGenerationReplayStatus(status ArtifactStatus) bool {
	return status == ArtifactStatusGenerated || status == ArtifactStatusGenerationFailed
}

func (db *DurableBoard) applyValidationEvaluated(event *walEvent) error {
	var payload struct {
		ClaimID      string       `json:"claim_id"`
		ValidationID string       `json:"validation_id"`
		Status       string       `json:"status"`
		Change       StatusChange `json:"change"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	c, ok := db.board.claims[payload.ClaimID]
	if !ok {
		return replayMissingReference("claim", payload.ClaimID)
	}
	var validation *Validation
	for _, v := range c.Validations {
		if v.ID == payload.ValidationID {
			change := payload.Change
			if change.To == "" {
				change = StatusChange{From: string(v.Status), To: payload.Status, Changed: event.CreatedAt, AgentID: event.AgentID}
			}
			changed := firstNonZeroTime(change.Changed, event.CreatedAt)
			change.Changed = changed
			v.StatusHistory = append(v.StatusHistory, change)
			v.Status = ValidationStatus(change.To)
			v.Accessed = changed
			validation = v
			break
		}
	}
	if validation == nil {
		return replayMissingReference("validation", payload.ValidationID)
	}
	accepted := c.AllValidationsPassed()
	nextStatus, nextLifecycle, hasOutcome := validationClaimOutcome(c, validation, validationStatusOf(validation), accepted)
	reason := validationClaimOutcomeReason(nextStatus, nextLifecycle, payload.Change.Reason)
	changed := firstNonZeroTime(payload.Change.Changed, event.CreatedAt)
	if hasOutcome && (c.Status != nextStatus || !claimStatusHistoryContainsTo(c.StatusHistory, nextStatus)) {
		fromStatus := replayClaimStatusHistoryFrom(c.StatusHistory, c.Status)
		c.StatusHistory = append(c.StatusHistory, StatusChange{
			From:    string(fromStatus),
			To:      string(nextStatus),
			Reason:  reason,
			AgentID: firstNonEmpty(payload.Change.AgentID, event.AgentID),
			Changed: changed,
		})
		c.Status = nextStatus
		c.Accessed = changed
	}
	if hasOutcome && CanTransitionClaimLifecycle(c.LifecycleStatus, nextLifecycle) {
		db.board.transitionClaimLifecycleLocked(c, nextLifecycle, firstNonEmpty(payload.Change.AgentID, event.AgentID), reason, changed)
	}
	records := []ClaimsOutboxRecord{
		db.board.outboxRecordLocked(event.Sequence, "validation", payload.ValidationID, walEventValidationEvaluated, event.CreatedAt),
	}
	switch nextStatus {
	case ClaimStatusAccepted:
		records = append(records, db.board.outboxRecordLocked(event.Sequence, "claim", c.ID, walEventClaimAccepted, event.CreatedAt))
	case ClaimStatusRejected:
		records = append(records, db.board.outboxRecordLocked(event.Sequence, "claim", c.ID, walEventClaimRejected, event.CreatedAt))
	}
	db.insertReplayOutbox(records)
	return nil
}

func (db *DurableBoard) applyClaimRejected(event *walEvent) error {
	var payload struct {
		ClaimID string       `json:"claim_id"`
		Change  StatusChange `json:"change"`
		Action  *Action      `json:"action,omitempty"`
		Claims  []Claim      `json:"claims,omitempty"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	b := db.board
	c, ok := b.claims[payload.ClaimID]
	if !ok {
		return replayMissingReference("claim", payload.ClaimID)
	}
	if payload.Change.To != "" {
		c.StatusHistory = append(c.StatusHistory, payload.Change)
	}
	c.Status = ClaimStatusRejected
	if payload.Action != nil {
		b.actions[payload.Action.ID] = payload.Action
	}
	for i := range payload.Claims {
		rc := &payload.Claims[i]
		b.claims[rc.ID] = rc
		b.claimOrder = append(b.claimOrder, rc.ID)
	}
	records := []ClaimsOutboxRecord{}
	if c, ok := b.claims[payload.ClaimID]; ok {
		records = append(records, b.outboxRecordLocked(event.Sequence, "claim", c.ID, walEventClaimRejected, event.CreatedAt))
	}
	if payload.Action != nil {
		records = append(records, b.outboxRecordLocked(event.Sequence, "action", payload.Action.ID, walEventActionPosted, event.CreatedAt))
	}
	for i := range payload.Claims {
		records = append(records, b.outboxRecordLocked(event.Sequence, "claim", payload.Claims[i].ID, "claim_issued", event.CreatedAt))
	}
	db.insertReplayOutbox(records)
	return nil
}

func (db *DurableBoard) applyPhaseTransition(event *walEvent) error {
	var payload struct {
		Phase     BoardPhase `json:"phase"`
		Iteration int        `json:"iteration"`
		Sequence  uint64     `json:"sequence"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	db.board.phase = payload.Phase
	db.board.iteration = payload.Iteration
	seq := payload.Sequence
	if seq == 0 {
		seq = db.board.seq.Load() + 1
	}
	if db.board.seq.Load() < seq {
		db.board.seq.Store(seq)
	}
	db.insertReplayOutbox([]ClaimsOutboxRecord{
		db.board.outboxRecordLocked(event.Sequence, "board", db.board.boardID, walEventPhaseTransition, event.CreatedAt),
	})
	return nil
}

func replayMissingReference(entityType, id string) error {
	return fmt.Errorf("missing %s %q for replay event", entityType, id)
}

func claimStatusHistoryContainsTo(history []StatusChange, status ClaimStatus) bool {
	for _, change := range history {
		if ClaimStatus(change.To) == status {
			return true
		}
	}
	return false
}

func replayClaimStatusHistoryFrom(history []StatusChange, fallback ClaimStatus) ClaimStatus {
	for i := len(history) - 1; i >= 0; i-- {
		if to := ClaimStatus(history[i].To); to != "" {
			return to
		}
	}
	return fallback
}

func (db *DurableBoard) snapshotPath() string {
	return filepath.Join(filepath.Dir(db.walDir), "projection.snapshot.json")
}

// walContentFingerprint produces a stable hash of the event's kind +
// payload. EventID is deliberately excluded: replay must collapse the
// same logical lifecycle fact even if a retry wrote it with a fresh
// event ID.
func walContentFingerprint(kind string, payload json.RawMessage) string {
	h := sha256.Sum256(append([]byte(kind+"\x1f"), payload...))
	return hex.EncodeToString(h[:16]) // 128-bit truncation is sufficient for dedup
}

func lockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func unlockFile(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

func truncateForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func durableProjectors(cfg ClaimsBoardConfig) []ClaimsProjector {
	if cfg.DisableOutbox {
		return nil
	}
	projectors := []ClaimsProjector{NewFabricProjector()}
	if cfg.DeltaBus != nil {
		projectors = append(projectors, NewCanonicalDeltaProjector(cfg.DeltaBus, cfg.AgentRefResolver))
	}
	projectors = append(projectors, cfg.Projectors...)
	return projectors
}

func projectorNames(projectors []ClaimsProjector) []string {
	names := make([]string, 0, len(projectors))
	for _, p := range projectors {
		if p == nil {
			continue
		}
		names = append(names, p.Name())
	}
	return names
}

func (db *DurableBoard) insertReplayOutbox(records []ClaimsOutboxRecord) {
	if db == nil || db.outbox == nil || len(records) == 0 {
		return
	}
	if err := db.outbox.InsertMany(records); err != nil && db.board != nil {
		db.board.RecordNotificationError("claims outbox replay: " + err.Error())
	}
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Now().UTC()
}

func setOutboxRecordSequence(records []ClaimsOutboxRecord, sequence uint64) {
	for i := range records {
		records[i].Sequence = sequence
	}
}
