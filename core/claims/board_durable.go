package claims

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// WAL event kinds.
const (
	walNamespace = "claims_board"

	walEventActionPosted        = "action_posted"
	walEventClaimUpdated        = "claim_updated"
	walEventTestamentSubmitted  = "testament_submitted"
	walEventValidationEvaluated = "validation_evaluated"
	walEventClaimAccepted       = "claim_accepted"
	walEventClaimRejected       = "claim_rejected"
	walEventPhaseTransition     = "phase_transition"
	walEventBoardComplete       = "board_complete"
)

type walEvent struct {
	EventID   string          `json:"event_id"`
	BoardID   string          `json:"board_id"`
	Kind      string          `json:"kind"`
	AgentID   string          `json:"agent_id,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

type walCheckpoint struct {
	BoardID    string             `json:"board_id"`
	PipelineID string            `json:"pipeline_id"`
	TaskID     string             `json:"task_id"`
	SessionID  string             `json:"session_id"`
	Phase      BoardPhase         `json:"phase"`
	Iteration  int                `json:"iteration"`
	Actions    map[string]*Action `json:"actions"`
	Claims     map[string]*Claim  `json:"claims"`
	ClaimOrder []string           `json:"claim_order"`
	Testaments map[string]*Testament `json:"testaments"`
	Seq        uint64             `json:"seq"`
	UpdatedAt  time.Time          `json:"updated_at"`
}

// DurableBoard wraps a ClaimsBoard with WAL persistence.
// WAL is written FIRST, then in-memory state is mutated.
// On crash between WAL write and mutation, recovery replays the WAL
// and produces the correct state.
type DurableBoard struct {
	board *ClaimsBoard

	mu      sync.Mutex
	walDir  string
	walFile *os.File
	seq     uint64
	seen    map[string]uint64
}

func OpenDurableBoard(cfg ClaimsBoardConfig) (*DurableBoard, error) {
	sessionDir := strings.TrimSpace(cfg.SessionDir)
	boardID := strings.TrimSpace(cfg.BoardID)
	if boardID == "" {
		boardID = uuid.NewString()
		cfg.BoardID = boardID
	}
	if sessionDir == "" {
		return &DurableBoard{
			board: NewClaimsBoard(cfg),
			seen:  make(map[string]uint64),
		}, nil
	}

	walDir := filepath.Join(sessionDir, "protocols", walNamespace, boardID, "wal")
	if err := os.MkdirAll(walDir, 0o755); err != nil {
		return nil, fmt.Errorf("create claims WAL dir: %w", err)
	}

	db := &DurableBoard{walDir: walDir, seen: make(map[string]uint64)}

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
	db.seq = snapshotSeq

	if err := db.replayWAL(snapshotSeq); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("replay claims WAL: %w", err)
	}

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
	return db.walFile.Close()
}

func (db *DurableBoard) SaveSnapshot() error {
	if db == nil || db.board == nil {
		return nil
	}
	db.mu.Lock()
	defer db.mu.Unlock()

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
	return os.Rename(tmpPath, snapshotPath)
}

func (db *DurableBoard) appendEvent(kind, agentID string, payload any) (uint64, error) {
	if db == nil || db.walFile == nil {
		if db != nil {
			db.seq++
			return db.seq, nil
		}
		return 0, nil
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("marshal WAL payload: %w", err)
	}

	eventID := uuid.NewString()

	// Dedup by EventID + content fingerprint. The EventID provides
	// identity uniqueness; the content fingerprint provides collision
	// resistance across replays where the same logical event could
	// carry a different EventID (e.g., retry after partial write).
	fingerprint := eventID + "\x1f" + walContentFingerprint(kind, payloadJSON)
	if existingSeq, ok := db.seen[fingerprint]; ok {
		return existingSeq, nil
	}

	db.seq++
	event := walEvent{
		EventID:   eventID,
		BoardID:   db.board.boardID,
		Kind:      kind,
		AgentID:   agentID,
		CreatedAt: time.Now().UTC(),
		Payload:   payloadJSON,
	}

	line, err := json.Marshal(event)
	if err != nil {
		return 0, fmt.Errorf("marshal WAL event: %w", err)
	}
	line = append(line, '\n')

	if _, err := db.walFile.Write(line); err != nil {
		return 0, fmt.Errorf("write WAL event: %w", err)
	}

	db.seen[eventID] = db.seq
	return db.seq, nil
}

// ── Durable wrappers (WAL first, then mutate) ───────────────────────

func (db *DurableBoard) PostAction(ctx context.Context, action Action, claims []Claim) error {
	if _, err := db.appendEvent(walEventActionPosted, action.AgentID, map[string]any{
		"action": action, "claims": claims,
	}); err != nil {
		return err
	}
	return db.board.PostAction(ctx, action, claims)
}

func (db *DurableBoard) SubmitTestaments(ctx context.Context, action Action, testaments []Testament) error {
	if _, err := db.appendEvent(walEventTestamentSubmitted, action.AgentID, map[string]any{
		"action": action, "testaments": testaments,
	}); err != nil {
		return err
	}
	return db.board.SubmitTestaments(ctx, action, testaments)
}

func (db *DurableBoard) EvaluateValidation(ctx context.Context, claimID, validationID string, change StatusChange) error {
	if _, err := db.appendEvent(walEventValidationEvaluated, change.AgentID, map[string]any{
		"claim_id": claimID, "validation_id": validationID, "status": change.To,
	}); err != nil {
		return err
	}
	return db.board.EvaluateValidation(ctx, claimID, validationID, change)
}

func (db *DurableBoard) RejectClaim(ctx context.Context, claimID string, change StatusChange, replacements *Action, replacementClaims []Claim) error {
	if _, err := db.appendEvent(walEventClaimRejected, change.AgentID, map[string]any{
		"claim_id": claimID, "action": replacements, "claims": replacementClaims,
	}); err != nil {
		return err
	}
	return db.board.RejectClaim(ctx, claimID, change, replacements, replacementClaims)
}

func (db *DurableBoard) TransitionToValidation(ctx context.Context) error {
	if _, err := db.appendEvent(walEventPhaseTransition, "", map[string]any{
		"phase": string(BoardPhaseValidation), "iteration": db.board.iteration,
	}); err != nil {
		return err
	}
	return db.board.TransitionToValidation(ctx)
}

func (db *DurableBoard) TransitionToImplementation(ctx context.Context) error {
	if _, err := db.appendEvent(walEventPhaseTransition, "", map[string]any{
		"phase": string(BoardPhaseImplementation), "iteration": db.board.iteration + 1,
	}); err != nil {
		return err
	}
	return db.board.TransitionToImplementation(ctx)
}

func (db *DurableBoard) MarkComplete(ctx context.Context) error {
	if _, err := db.appendEvent(walEventBoardComplete, "", nil); err != nil {
		return err
	}
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
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		seq++

		var event walEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			slog.Warn("claims_wal_corrupt_entry",
				"seq", seq, "error", err.Error(),
				"line_prefix", truncateForLog(line, 100),
			)
			continue
		}

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
	return nil
}

func (db *DurableBoard) applyEvent(event *walEvent) error {
	if event == nil || db.board == nil {
		return nil
	}
	switch event.Kind {
	case walEventActionPosted:
		return db.applyActionPosted(event)
	case walEventTestamentSubmitted:
		return db.applyTestamentSubmitted(event)
	case walEventValidationEvaluated:
		return db.applyValidationEvaluated(event)
	case walEventClaimRejected:
		return db.applyClaimRejected(event)
	case walEventPhaseTransition:
		return db.applyPhaseTransition(event)
	case walEventBoardComplete:
		db.board.phase = BoardPhaseComplete
		return nil
	default:
		return nil
	}
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
			}
		}
	}
	return nil
}

func (db *DurableBoard) applyValidationEvaluated(event *walEvent) error {
	var payload struct {
		ClaimID      string `json:"claim_id"`
		ValidationID string `json:"validation_id"`
		Status       string `json:"status"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	c, ok := db.board.claims[payload.ClaimID]
	if !ok {
		return nil
	}
	for _, v := range c.Validations {
		if v.ID == payload.ValidationID {
			v.Status = ValidationStatus(payload.Status)
			v.Accessed = event.CreatedAt
			break
		}
	}
	return nil
}

func (db *DurableBoard) applyClaimRejected(event *walEvent) error {
	var payload struct {
		ClaimID string   `json:"claim_id"`
		Action  *Action  `json:"action,omitempty"`
		Claims  []Claim  `json:"claims,omitempty"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	b := db.board
	if c, ok := b.claims[payload.ClaimID]; ok {
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
	return nil
}

func (db *DurableBoard) applyPhaseTransition(event *walEvent) error {
	var payload struct {
		Phase     BoardPhase `json:"phase"`
		Iteration int        `json:"iteration"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	db.board.phase = payload.Phase
	db.board.iteration = payload.Iteration
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
