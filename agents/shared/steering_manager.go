package shared

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/steering"
)

// CancelEntry tracks a per-correlation cancel function with ordered cleanup
// hooks and a done signal. Fire() is idempotent — safe to call multiple times.
type CancelEntry struct {
	mu       sync.Mutex
	fired    bool
	cancelFn context.CancelFunc
	hooks    []func() // agent-specific cleanup, run in order after cancel
	done     chan struct{}
}

// Fire cancels the context, runs cleanup hooks in order, and closes done.
// Idempotent — multiple calls are safe.
func (e *CancelEntry) Fire() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.fired {
		return
	}
	e.fired = true
	e.cancelFn()
	for _, h := range e.hooks {
		h()
	}
	close(e.done)
}

// IsFired reports whether Fire() has been called.
func (e *CancelEntry) IsFired() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.fired
}

// Done returns a channel that closes when Fire() completes.
func (e *CancelEntry) Done() <-chan struct{} { return e.done }

// AddHook appends a cleanup function that runs (in order) when Fire() is called.
// If already fired, the hook runs immediately.
func (e *CancelEntry) AddHook(fn func()) {
	e.mu.Lock()
	if e.fired {
		e.mu.Unlock()
		fn()
		return
	}
	e.hooks = append(e.hooks, fn)
	e.mu.Unlock()
}

// SteeringManager manages per-correlation steering ledgers for an agent.
// Thread-safe for concurrent bus handler access.
type SteeringManager struct {
	mu        sync.Mutex
	ledgers   map[string]*steering.SteeringLedger
	cancels   map[string]*CancelEntry
	bySession map[string]map[string]struct{}

	// Shared journal + activity publisher, passed to every new ledger.
	journal     *steering.SteeringJournal
	activityPub events.ActivityPublisher

	// Lazy session binding fields.
	agentType   string
	sessionDir  string
	eventLogger *agentlog.SessionEventLogger
}

// NewSteeringManager creates a new SteeringManager.
func NewSteeringManager() *SteeringManager {
	return &SteeringManager{
		ledgers:   make(map[string]*steering.SteeringLedger),
		cancels:   make(map[string]*CancelEntry),
		bySession: make(map[string]map[string]struct{}),
	}
}

// InitJournal opens a steering WAL journal for this agent and runs crash
// recovery (closing incomplete WAL brackets). Must be called before Create()
// if WAL durability is desired. If walDir is empty, derives a default path
// under .sylk/agents/{agentType}/steering/.
func (sm *SteeringManager) InitJournal(agentType string, activityPub events.ActivityPublisher, walDir string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.activityPub = activityPub

	if walDir == "" {
		walDir = filepath.Join(".sylk", "agents", agentType, "steering")
	}

	journal, err := steering.OpenSteeringJournalDirect(walDir)
	if err != nil {
		slog.Warn("steering: journal init failed, running without WAL",
			"agent", agentType, "err", err)
		return nil
	}

	sm.journal = journal

	// Crash recovery: close any unpaired Begin entries from prior runs.
	incomplete := journal.FindIncompleteOperations()
	for _, op := range incomplete {
		slog.Info("steering: recovered incomplete operation",
			"correlation_id", op.CorrelationID,
			"agent_id", op.AgentID,
			"session_id", op.SessionID,
		)
		journal.LogInterrupted(op.CorrelationID, "", 0, "recovered")
	}

	return nil
}

// InitLazy prepares the manager for deferred session binding. No WAL files are
// opened. Call BindSession on the first request carrying a session ID.
func (sm *SteeringManager) InitLazy(agentType string, activityPub events.ActivityPublisher) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.agentType = agentType
	sm.activityPub = activityPub
	sm.eventLogger = agentlog.NewSessionEventLogger(agentType, "events")
}

// BindSession opens the steering WAL journal and event logger at the
// session-scoped directory. Idempotent for the same sessionDir. Runs crash
// recovery on first bind.
func (sm *SteeringManager) BindSession(sessionDir, sessionID string) error {
	sm.mu.Lock()
	if sm.sessionDir == sessionDir {
		sm.mu.Unlock()
		return nil
	}

	agentType := sm.agentType
	el := sm.eventLogger
	sm.mu.Unlock()

	// Bind the event logger FIRST — independent of journal success.
	if el != nil {
		if err := el.BindSession(sessionDir, sessionID); err != nil {
			slog.Warn("steering: event logger bind failed",
				"agent", agentType, "session_dir", sessionDir, "err", err)
		}
	}

	walDir := filepath.Join(sessionDir, "agents", agentType, "wal")
	journal, err := steering.OpenSteeringJournalDirect(walDir)
	if err != nil {
		slog.Warn("steering: session journal init failed, running without WAL",
			"agent", agentType, "session_dir", sessionDir, "err", err)
		// Update sessionDir to prevent retry loops even when journal fails.
		sm.mu.Lock()
		sm.sessionDir = sessionDir
		sm.mu.Unlock()
		return nil
	}

	// Crash recovery.
	incomplete := journal.FindIncompleteOperations()
	for _, op := range incomplete {
		slog.Info("steering: recovered incomplete operation",
			"correlation_id", op.CorrelationID,
			"agent_id", op.AgentID,
			"session_id", op.SessionID,
		)
		journal.LogInterrupted(op.CorrelationID, "", 0, "recovered")
	}

	sm.mu.Lock()
	// Double-check under lock.
	if sm.sessionDir == sessionDir {
		sm.mu.Unlock()
		journal.Close()
		return nil
	}

	// Close previous journal if any.
	prev := sm.journal
	sm.journal = journal
	sm.sessionDir = sessionDir
	sm.mu.Unlock()

	if prev != nil {
		prev.Close()
	}

	return nil
}

// EventLogger returns the session event logger. May be nil or unbound.
// Nil-safe on the receiver so callers holding a zero-value or uninitialized
// pointer can ask without crashing — the orchestrator's test harness
// constructs orchestrators without a steering manager bound.
func (sm *SteeringManager) EventLogger() *agentlog.SessionEventLogger {
	if sm == nil {
		return nil
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.eventLogger
}

// SessionDir returns the bound session directory, or "" if unbound.
// Nil-safe — see EventLogger.
func (sm *SteeringManager) SessionDir() string {
	if sm == nil {
		return ""
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.sessionDir
}

// Create creates and registers a new steering ledger for the given correlation.
// Automatically passes the shared journal and activity publisher if initialized.
func (sm *SteeringManager) Create(
	correlationID, agentID, sessionID string,
	activityPub events.ActivityPublisher,
	journal *steering.SteeringJournal,
) *steering.SteeringLedger {
	// Prefer explicitly passed journal/activityPub; fall back to shared.
	sm.mu.Lock()
	if journal == nil {
		journal = sm.journal
	}
	if activityPub == nil {
		activityPub = sm.activityPub
	}
	sm.mu.Unlock()

	ledger := steering.NewSteeringLedger(correlationID, agentID, sessionID, activityPub, journal)
	sm.mu.Lock()
	sm.ledgers[correlationID] = ledger
	sm.mu.Unlock()
	return ledger
}

// Lookup returns the ledger for the given correlation, or nil.
func (sm *SteeringManager) Lookup(correlationID string) *steering.SteeringLedger {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.ledgers[correlationID]
}

// Close removes and closes the ledger and cancel entry for the given correlation.
func (sm *SteeringManager) Close(correlationID string, interrupted bool) {
	sm.mu.Lock()
	l := sm.ledgers[correlationID]
	delete(sm.ledgers, correlationID)
	delete(sm.cancels, correlationID)
	for sessionID, corrIDs := range sm.bySession {
		delete(corrIDs, correlationID)
		if len(corrIDs) == 0 {
			delete(sm.bySession, sessionID)
		}
	}
	sm.mu.Unlock()
	if l != nil {
		l.Close(interrupted)
	}
}

// CloseAll fires all cancel entries, closes all active ledgers, closes the
// shared journal, and closes the event logger. Used during agent shutdown.
func (sm *SteeringManager) CloseAll() {
	sm.mu.Lock()
	cancelEntries := make([]*CancelEntry, 0, len(sm.cancels))
	for _, ce := range sm.cancels {
		cancelEntries = append(cancelEntries, ce)
	}
	sm.cancels = make(map[string]*CancelEntry)
	sm.bySession = make(map[string]map[string]struct{})

	all := make([]*steering.SteeringLedger, 0, len(sm.ledgers))
	for _, l := range sm.ledgers {
		all = append(all, l)
	}
	sm.ledgers = make(map[string]*steering.SteeringLedger)
	journal := sm.journal
	sm.journal = nil
	el := sm.eventLogger
	sm.eventLogger = nil
	sm.mu.Unlock()

	// Fire all cancel entries before closing ledgers.
	for _, ce := range cancelEntries {
		ce.Fire()
	}

	for _, l := range all {
		l.Close(true)
	}
	if journal != nil {
		journal.Close()
	}
	if el != nil {
		el.Close()
	}
}

// RegisterCancel creates and stores a CancelEntry for the given correlation.
// Returns the entry so agents can add cleanup hooks via AddHook.
func (sm *SteeringManager) RegisterCancel(correlationID, sessionID string, cancel context.CancelFunc) *CancelEntry {
	entry := &CancelEntry{
		cancelFn: cancel,
		done:     make(chan struct{}),
	}
	correlationID = strings.TrimSpace(correlationID)
	sm.mu.Lock()
	sm.cancels[correlationID] = entry
	sessionID = strings.TrimSpace(sessionID)
	if sessionID != "" {
		if sm.bySession[sessionID] == nil {
			sm.bySession[sessionID] = make(map[string]struct{})
		}
		sm.bySession[sessionID][correlationID] = struct{}{}
	}
	sm.mu.Unlock()
	slog.Info("INTERRUPT_DEBUG: steering_register_cancel",
		"correlation_id", correlationID,
		"session_id", sessionID,
	)
	return entry
}

// CancelRequest fires the cancel entry for the given correlation.
// Returns true if the entry existed and was fired.
func (sm *SteeringManager) CancelRequest(correlationID string) bool {
	correlationID = strings.TrimSpace(correlationID)
	sm.mu.Lock()
	entry := sm.cancels[correlationID]
	sm.mu.Unlock()
	if entry == nil {
		slog.Info("INTERRUPT_DEBUG: steering_cancel_request_miss",
			"correlation_id", correlationID,
		)
		return false
	}
	entry.Fire()
	slog.Info("INTERRUPT_DEBUG: steering_cancel_request_fired",
		"correlation_id", correlationID,
	)
	return true
}

// CancelSession fires all cancel entries registered for the given session.
// Returns the number of in-flight requests signaled.
func (sm *SteeringManager) CancelSession(sessionID string) int {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		slog.Info("INTERRUPT_DEBUG: steering_cancel_session_empty")
		return 0
	}
	sm.mu.Lock()
	corrIDs := sm.bySession[sessionID]
	if len(corrIDs) == 0 {
		sm.mu.Unlock()
		slog.Info("INTERRUPT_DEBUG: steering_cancel_session_miss",
			"session_id", sessionID,
		)
		return 0
	}
	entries := make([]*CancelEntry, 0, len(corrIDs))
	for correlationID := range corrIDs {
		if entry := sm.cancels[correlationID]; entry != nil {
			entries = append(entries, entry)
		}
	}
	sm.mu.Unlock()
	for _, entry := range entries {
		entry.Fire()
	}
	slog.Info("INTERRUPT_DEBUG: steering_cancel_session_fired",
		"session_id", sessionID,
		"count", len(entries),
	)
	return len(entries)
}

// IsCancelled reports whether the cancel entry for the given correlation
// has been fired. Returns false if no entry exists.
func (sm *SteeringManager) IsCancelled(correlationID string) bool {
	sm.mu.Lock()
	entry := sm.cancels[correlationID]
	sm.mu.Unlock()
	if entry == nil {
		return false
	}
	return entry.IsFired()
}

// HandleAction processes a steering action request and deposits the appropriate
// command into the matching ledger. Returns true if the action was handled.
func (sm *SteeringManager) HandleAction(req *guide.ActionRequest) bool {
	if req == nil {
		return false
	}
	action := strings.ToLower(strings.TrimSpace(req.Action))
	switch action {
	case "steer":
		return sm.handleSteer(req)
	case "edit":
		return sm.handleEdit(req)
	case "rollback":
		return sm.handleRollback(req)
	case "pace":
		return sm.handlePace(req)
	case "cancel", "interrupt", "stop":
		handled := sm.handleCancel(req)
		slog.Info("INTERRUPT_DEBUG: steering_handle_cancel_action",
			"action", action,
			"target_agent", req.TargetAgentID,
			"correlation_id", req.CorrelationID,
			"handled", handled,
		)
		return handled
	default:
		return false
	}
}

func (sm *SteeringManager) handleCancel(req *guide.ActionRequest) bool {
	correlationID := extractCancelCorrelationID(req)
	if correlationID != "" {
		return sm.CancelRequest(correlationID)
	}
	sessionID := extractCancelSessionID(req)
	if sessionID == "" {
		return false
	}
	return sm.CancelSession(sessionID) > 0
}

// extractCancelCorrelationID extracts the correlation ID from a cancel action.
func extractCancelCorrelationID(req *guide.ActionRequest) string {
	// First: look inside req.Data["correlation_id"]
	if data, ok := req.Data.(map[string]any); ok {
		if cid, ok := data["correlation_id"].(string); ok {
			trimmed := strings.TrimSpace(cid)
			if trimmed != "" {
				return trimmed
			}
		}
	}
	// Fallback: use req.CorrelationID
	return strings.TrimSpace(req.CorrelationID)
}

func extractCancelSessionID(req *guide.ActionRequest) string {
	if req == nil {
		return ""
	}
	if data, ok := req.Data.(map[string]any); ok {
		if sid, ok := data["session_id"].(string); ok {
			return strings.TrimSpace(sid)
		}
	}
	return ""
}

func (sm *SteeringManager) handleSteer(req *guide.ActionRequest) bool {
	ledger := sm.Lookup(req.CorrelationID)
	if ledger == nil {
		return false
	}
	text, _ := req.Data.(string)
	if text == "" {
		return false
	}
	ledger.DepositCommand(steering.Command{
		Type:      steering.CommandSteer,
		Text:      text,
		Timestamp: time.Now(),
	})
	return true
}

func (sm *SteeringManager) handleEdit(req *guide.ActionRequest) bool {
	ledger := sm.Lookup(req.CorrelationID)
	if ledger == nil {
		return false
	}
	data, ok := req.Data.(map[string]any)
	if !ok {
		return false
	}
	cmd := steering.Command{
		Type:      steering.CommandEdit,
		Timestamp: time.Now(),
	}
	if cpID, ok := data["checkpoint_id"].(string); ok {
		cmd.CheckpointID = cpID
	}
	if idx, ok := data["message_index"].(float64); ok {
		cmd.MessageIndex = int(idx)
	}
	if text, ok := data["new_text"].(string); ok {
		cmd.NewText = text
	}
	ledger.DepositCommand(cmd)
	return true
}

func (sm *SteeringManager) handleRollback(req *guide.ActionRequest) bool {
	ledger := sm.Lookup(req.CorrelationID)
	if ledger == nil {
		return false
	}
	data, ok := req.Data.(map[string]any)
	if !ok {
		return false
	}
	cpID, _ := data["checkpoint_id"].(string)
	if cpID == "" {
		return false
	}
	ledger.DepositCommand(steering.Command{
		Type:         steering.CommandRollback,
		CheckpointID: cpID,
		Timestamp:    time.Now(),
	})
	return true
}

func (sm *SteeringManager) handlePace(req *guide.ActionRequest) bool {
	ledger := sm.Lookup(req.CorrelationID)
	if ledger == nil {
		return false
	}
	data, ok := req.Data.(map[string]any)
	if !ok {
		return false
	}
	paceStr, _ := data["pace"].(string)
	switch strings.ToLower(paceStr) {
	case "auto":
		ledger.SetPace(steering.PaceAuto)
		ledger.DepositCommand(steering.Command{
			Type:      steering.CommandResume,
			Timestamp: time.Now(),
		})
	case "step":
		ledger.SetPace(steering.PaceStep)
		ledger.DepositCommandWithoutResume(steering.Command{
			Type:      steering.CommandPace,
			Pace:      steering.PaceStep,
			Timestamp: time.Now(),
		})
	case "paused":
		ledger.SetPace(steering.PacePaused)
		ledger.DepositCommandWithoutResume(steering.Command{
			Type:      steering.CommandPace,
			Pace:      steering.PacePaused,
			Timestamp: time.Now(),
		})
	default:
		return false
	}
	return true
}

// ── Context helpers ─────────────────────────────────────────────────────────

// logMetaKey stores per-request logging metadata in context.
type logMetaKey struct{}

// LogMeta carries per-request identifiers for event logging through call chains.
type LogMeta struct {
	EventLogger *agentlog.SessionEventLogger
	CorrID      string
	AgentID     string
	SessionID   string
}

// WithLogMeta stores logging metadata in context.
func WithLogMeta(ctx context.Context, m LogMeta) context.Context {
	return context.WithValue(ctx, logMetaKey{}, m)
}

// LogMetaFromContext retrieves logging metadata. Returns zero LogMeta if absent.
func LogMetaFromContext(ctx context.Context) LogMeta {
	if ctx == nil {
		return LogMeta{}
	}
	m, _ := ctx.Value(logMetaKey{}).(LogMeta)
	return m
}

type steeringLedgerKey struct{}

// WithSteeringLedger stores a steering ledger in the context. Used to thread
// the ledger through deep call chains without changing intermediate signatures.
func WithSteeringLedger(ctx context.Context, ledger *steering.SteeringLedger) context.Context {
	if ledger == nil {
		return ctx
	}
	return context.WithValue(ctx, steeringLedgerKey{}, ledger)
}

// SteeringLedgerFromContext retrieves the steering ledger from context. Returns
// nil if none is set (nil-safe for DrainAndCheckpoint).
func SteeringLedgerFromContext(ctx context.Context) *steering.SteeringLedger {
	if ctx == nil {
		return nil
	}
	l, _ := ctx.Value(steeringLedgerKey{}).(*steering.SteeringLedger)
	return l
}
