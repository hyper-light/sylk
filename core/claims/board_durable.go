package claims

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
)

// WAL event kinds.
const (
	walNamespace = "claims_board"

	walEventActionPosted                 = "action_posted"
	walEventClaimActionGenerated         = "claim_action_generated"
	walEventClaimLifecycleTransition     = "claim_lifecycle_transition"
	walEventTestamentActionGenerated     = "testament_action_generated"
	walEventTestamentLifecycleTransition = "testament_lifecycle_transition"
	walEventClaimUpdated                 = "claim_updated"
	walEventClaimContextSet              = "claim_context_set"
	walEventTestamentContextSet          = "testament_context_set"
	walEventTestamentSubmitted           = "testament_submitted"
	walEventValidationEvaluated          = "validation_evaluated"
	walEventClaimAccepted                = "claim_accepted"
	walEventClaimRejected                = "claim_rejected"
	walEventPhaseTransition              = "phase_transition"
	walEventBoardComplete                = "board_complete"
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
	Seq        uint64                `json:"seq"`
	UpdatedAt  time.Time             `json:"updated_at"`
}

// DurableBoard wraps a ClaimsBoard with WAL persistence.
// WAL is written FIRST, then in-memory state is mutated.
// On crash between WAL write and mutation, recovery replays the WAL
// and produces the correct state.
type DurableBoard struct {
	board *ClaimsBoard

	mu         sync.Mutex
	walDir     string
	walFile    *os.File
	seq        uint64
	seen       map[string]uint64
	outbox     *ClaimsOutbox
	projectors []ClaimsProjector

	healthMu      sync.Mutex
	healthHistory []ProjectionHealthSnapshot

	projectionScheduled atomic.Bool
}

func OpenDurableBoard(cfg ClaimsBoardConfig) (*DurableBoard, error) {
	sessionDir := strings.TrimSpace(cfg.SessionDir)
	boardID := strings.TrimSpace(cfg.BoardID)
	if boardID == "" {
		boardID = uuid.NewString()
		cfg.BoardID = boardID
	}
	if sessionDir == "" {
		db := &DurableBoard{
			board: NewClaimsBoard(cfg),
			seen:  make(map[string]uint64),
		}
		db.projectors = durableProjectors(cfg)
		if !cfg.DisableOutbox {
			outbox, err := OpenClaimsOutbox("", projectorNames(db.projectors))
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

	db := &DurableBoard{walDir: walDir, seen: make(map[string]uint64)}
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
		outbox, err := OpenClaimsOutbox(outboxDir, projectorNames(db.projectors))
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

	return db, nil
}

func (db *DurableBoard) Board() *ClaimsBoard {
	if db == nil {
		return nil
	}
	return db.board
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
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write claims snapshot: %w", err)
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	return os.Rename(tmpPath, snapshotPath)
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

	// Dedup by EventID + content fingerprint. The EventID provides
	// identity uniqueness; the content fingerprint provides collision
	// resistance across replays where the same logical event could
	// carry a different EventID (e.g., retry after partial write).
	fingerprint := eventID + "\x1f" + walContentFingerprint(kind, payloadJSON)
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

func (db *DurableBoard) projectOutbox(ctx context.Context) {
	if db == nil || db.outbox == nil || len(db.projectors) == 0 || db.board == nil {
		return
	}
	if db.board.scope == nil {
		return
	}
	if !db.outbox.HasPending(projectorNames(db.projectors), time.Now().UTC()) {
		return
	}
	if !db.projectionScheduled.CompareAndSwap(false, true) {
		return
	}
	if err := db.board.scope.Go("claims_outbox_project", 30*time.Second, func(runCtx context.Context) error {
		defer db.projectionScheduled.Store(false)
		db.DrainOutbox(runCtx, 128)
		return nil
	}); err != nil {
		db.projectionScheduled.Store(false)
		db.board.RecordNotificationError("claims outbox projector dispatch: " + err.Error())
	}
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

func (db *DurableBoard) SubmitTestaments(ctx context.Context, action Action, testaments []Testament) error {
	return db.board.SubmitTestaments(ctx, action, testaments)
}

func (db *DurableBoard) EvaluateValidation(ctx context.Context, claimID, validationID string, change StatusChange) error {
	return db.board.EvaluateValidation(ctx, claimID, validationID, change)
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
		return NewClaimsBoard(cfg), 0, nil // corrupt → start fresh
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

	return board, checkpoint.Seq, nil
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
	var corruptEntries []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		seq++

		var event walEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			corruptEntries = append(corruptEntries, fmt.Sprintf(
				"WAL seq %d: %s (prefix: %s)", seq, err.Error(), truncateForLog(line, 80),
			))
			continue
		}
		event.Sequence = seq

		fp := event.EventID + "\x1f" + walContentFingerprint(event.Kind, event.Payload)
		if seq <= afterSeq {
			db.seen[fp] = seq
			continue
		}
		if _, ok := db.seen[fp]; ok {
			continue
		}
		db.seen[fp] = seq

		if err := db.applyEvent(&event); err != nil {
			continue
		}
	}
	db.seq = seq

	// Surface corruption as notification errors on the board so the
	// first projection query exposes them to agents. Agents can then
	// record them as testament error artifacts.
	if len(corruptEntries) > 0 && db.board != nil {
		db.board.mu.Lock()
		db.board.notificationErrors = append(db.board.notificationErrors, corruptEntries...)
		db.board.mu.Unlock()
	}
	if db.board != nil {
		db.board.rebuildDerivedState()
	}
	return nil
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
		return nil
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
		if c, ok := db.board.claims[claimID]; ok {
			db.board.transitionClaimLifecycleLocked(c, payload.To, payload.AgentID, payload.Reason, changed)
			if payload.To.IsFailure() && !c.Status.IsTerminal() {
				c.Status = ClaimStatusRejected
			}
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
		return nil
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
		return nil
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
		t, ok := db.board.testaments[testamentID]
		if !ok {
			continue
		}
		db.board.transitionTestamentLifecycleLocked(t, payload.To, payload.AgentID, payload.Reason, changed)
		if payload.To == TestamentLifecyclePosted {
			db.board.resolveClaimForTestamentLocked(t, changed)
		}
		if payload.To == TestamentLifecycleReceived {
			if claimID := ClaimIDFromRelations(t.Relations); claimID != "" {
				if c, found := db.board.claims[claimID]; found {
					db.board.transitionClaimLifecycleLocked(c, ClaimLifecycleTestamentAcknowledged, payload.AgentID, "testament acknowledged", changed)
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
		return nil
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
		return nil
	}
	var validation *Validation
	for _, v := range c.Validations {
		if v.ID == payload.ValidationID {
			change := payload.Change
			if change.To == "" {
				change = StatusChange{From: string(v.Status), To: payload.Status, Changed: event.CreatedAt, AgentID: event.AgentID}
			}
			v.StatusHistory = append(v.StatusHistory, change)
			v.Status = ValidationStatus(change.To)
			v.Accessed = event.CreatedAt
			validation = v
			break
		}
	}
	accepted := c.AllValidationsPassed()
	nextStatus, nextLifecycle, hasOutcome := validationClaimOutcome(c, validation, validationStatusOf(validation), accepted)
	if hasOutcome && c.Status != nextStatus {
		c.Status = nextStatus
		c.Accessed = event.CreatedAt
	}
	if hasOutcome && CanTransitionClaimLifecycle(c.LifecycleStatus, nextLifecycle) {
		db.board.transitionClaimLifecycleLocked(c, nextLifecycle, event.AgentID, validationClaimOutcomeReason(nextStatus, nextLifecycle, ""), event.CreatedAt)
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
	if c, ok := b.claims[payload.ClaimID]; ok {
		if payload.Change.To != "" {
			c.StatusHistory = append(c.StatusHistory, payload.Change)
		}
		c.Status = ClaimStatusRejected
	}
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

func (db *DurableBoard) snapshotPath() string {
	return filepath.Join(filepath.Dir(db.walDir), "projection.snapshot.json")
}

// walContentFingerprint produces a stable hash of the event's kind +
// payload for dedup collision resistance. Combined with EventID, this
// ensures: same EventID + same content = same event (replay dedup),
// different EventID + same content = different events (no false dedup).
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
