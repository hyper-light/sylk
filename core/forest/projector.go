package forest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/knowledge/memory"
	"github.com/google/uuid"
)

// Projector tunables. Named constants — no magic numbers.
const (
	// projectorBranchName is the canonical name of the branch
	// projector entry in forest_projector_state. Other projection
	// types (substrate, canopy, retrieval-cache) get their own names.
	projectorBranchName = "branch"

	// projectorLeaseDuration bounds how long a leader holds the
	// projector lease before renewal. Short enough that a crashed
	// process is replaced quickly; long enough to amortize renewal.
	projectorLeaseDuration = 30 * time.Second

	// projectorRenewInterval is how frequently the leader renews
	// its lease — well within projectorLeaseDuration so a transient
	// pause doesn't lose leadership. Doubles as the projector's
	// safety-poll backstop: every renew tick the outer loop re-runs
	// processBranchProjectorBatch, so a missed wake (e.g., a wake
	// signal raced past a draining channel in a multi-process race)
	// is recovered within at most one renewInterval.
	projectorRenewInterval = 10 * time.Second

	// projectorBatchSize is the maximum number of events processed
	// per catch-up cycle. Larger batches amortize transaction
	// overhead; smaller batches keep latency low.
	projectorBatchSize = 32

	// projectorWaitForLease is how long a non-leader process waits
	// before re-attempting lease acquisition.
	projectorWaitForLease = 5 * time.Second

	// projectorBackoffInitial is the starting backoff on apply
	// failures. Doubles up to projectorBackoffMax.
	projectorBackoffInitial = 100 * time.Millisecond
	projectorBackoffMax     = 30 * time.Second

	// projectorErrTruncate caps last_error length on
	// forest_projector_state to keep the table compact.
	projectorErrTruncate = 4096

	// projectorPoisonPillThreshold is the number of consecutive
	// failures of the SAME event seq before we halt the projector.
	// Transient errors usually resolve in 1–2 retries; a persistent
	// failure on the same event indicates a poison pill that retries
	// will not fix.
	projectorPoisonPillThreshold = 8
)

// ProjectorHealth represents the operational status of a projector.
// Exposed so operators can introspect via Health() and readers can
// detect stalls.
type ProjectorHealth string

const (
	ProjectorHealthIdle    ProjectorHealth = "idle"
	ProjectorHealthRunning ProjectorHealth = "running"
	ProjectorHealthHalted  ProjectorHealth = "halted"
)

// seqNotifier broadcasts projection-watermark advances to waiters so
// WaitForBranchSeq can return immediately on apply rather than
// polling the DB. One notifier per MemoryForest instance.
//
// The notifier is updated by the branch projector after every
// successful apply (and by the synchronous-projection path on every
// inline projection). Waiters register a target seq and a done
// channel; Advance closes done channels for waiters whose target is
// now satisfied; Halt closes ALL pending waiter channels (they then
// re-check halted state on wake and return the halt error).
type seqNotifier struct {
	mu         sync.Mutex
	currentSeq int64
	haltedErr  error
	waiters    []*seqWaiter
}

type seqWaiter struct {
	target int64
	done   chan struct{}
}

func newSeqNotifier() *seqNotifier {
	return &seqNotifier{}
}

// Advance records that the projector has applied through seq. Notifies
// any waiters whose target is now satisfied. Idempotent — calling with
// a seq <= currentSeq is a no-op.
func (n *seqNotifier) Advance(seq int64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if seq <= n.currentSeq {
		return
	}
	n.currentSeq = seq
	remaining := n.waiters[:0]
	for _, w := range n.waiters {
		if w.target <= n.currentSeq {
			close(w.done)
			continue
		}
		remaining = append(remaining, w)
	}
	n.waiters = remaining
}

// Halt records a fatal projector failure and wakes every pending
// waiter. Waiters check the halt state on wake and return the halt
// error rather than success.
func (n *seqNotifier) Halt(cause error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.haltedErr == nil {
		n.haltedErr = cause
	}
	for _, w := range n.waiters {
		close(w.done)
	}
	n.waiters = nil
}

// snapshot returns a consistent view of (currentSeq, haltedErr) under
// the notifier's lock. Used by Wait callers for predicate checks.
func (n *seqNotifier) snapshot() (int64, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.currentSeq, n.haltedErr
}

// register adds a waiter and returns its done channel. The caller is
// responsible for removing the waiter via remove() if they exit
// without being notified (timeout / context cancellation).
func (n *seqNotifier) register(target int64) *seqWaiter {
	w := &seqWaiter{target: target, done: make(chan struct{})}
	n.mu.Lock()
	n.waiters = append(n.waiters, w)
	n.mu.Unlock()
	return w
}

// remove evicts a waiter by identity. Idempotent — no-op if the
// waiter has already been removed (e.g. by Advance closing it).
func (n *seqNotifier) remove(target *seqWaiter) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for i, w := range n.waiters {
		if w == target {
			n.waiters = append(n.waiters[:i], n.waiters[i+1:]...)
			return
		}
	}
}

// projectorState bundles the persisted state of a projector entry,
// loaded from forest_projector_state on each lease renewal so the
// in-memory view never drifts from the DB-of-truth.
type projectorState struct {
	name             string
	lastAppliedSeq   int64
	lastAppliedAt    time.Time
	leaderHolder     string
	leaderLeaseUntil time.Time
	schemaVersion    int
	healthStatus     ProjectorHealth
	lastError        string
	lastErrorAt      time.Time

	// poisonSeq + poisonCount track consecutive failures of the
	// same event seq. When poisonCount reaches the threshold the
	// projector escalates to halted to avoid an infinite-retry
	// storm on a deterministic-but-uncategorized error.
	poisonSeq   int64
	poisonCount int
}

// recordPoisonHit increments the poison counter for the given event
// seq. Returns true when the counter has crossed the halt threshold —
// caller should escalate to halt.
func (s *projectorState) recordPoisonHit(seq int64) bool {
	if s.poisonSeq == seq {
		s.poisonCount++
	} else {
		s.poisonSeq = seq
		s.poisonCount = 1
	}
	return s.poisonCount >= projectorPoisonPillThreshold
}

// clearPoison resets the poison counter on successful apply.
func (s *projectorState) clearPoison() {
	s.poisonSeq = 0
	s.poisonCount = 0
}

// errLeaseHeldByOther signals that the projector cannot proceed —
// another process holds the lease. The caller backs off and retries.
var errLeaseHeldByOther = errors.New("forest projector lease held by another process")

// errProjectorHalt signals a fatal apply error. The projector marks
// itself halted; manual intervention is required to resume.
var errProjectorHalt = errors.New("forest projector halt")

// generateProjectorID returns a unique-per-process identifier used
// as the leader holder name in the lease table.
func generateProjectorID() string {
	return "forest-projector-" + uuid.NewString()
}

// startBranchProjector launches the single tracked goroutine that
// drives the branch projector. Registered on the forest's wait group
// so Close() drains cleanly.
//
// The goroutine is panic-recovered: a panic anywhere inside the
// projector's call tree is caught, logged with stack, and persisted
// on forest_projector_state.last_error with health_status='halted'.
// Operator can introspect via ProjectorStatus and resume manually.
func (m *MemoryForest) startBranchProjector() {
	if m == nil {
		return
	}
	m.registerRuntimeTicker("branch_projector_lease_renewal", projectorRenewInterval)
	m.startWorker("branch_projector", projectorBatchSize, func(context.Context) error {
		m.runBranchProjectorLoop()
		return nil
	})
}

// runBranchProjectorLoop is the projector's outer loop. Acquires the
// lease, drains events, renews the lease, and shuts down cleanly
// when the forest is closed.
func (m *MemoryForest) runBranchProjectorLoop() {
	backoff := newProjectorBackoff()
	for {
		if m.shouldStopProjector() {
			return
		}

		state, err := m.acquireBranchProjectorLease()
		if err != nil {
			m.handleLeaseAcquireFailure(err)
			if !m.sleepProjector(projectorWaitForLease) {
				return
			}
			continue
		}

		if err := m.runProjectorSession(state, backoff); err != nil {
			if errors.Is(err, errProjectorHalt) {
				m.markProjectorHalted(state, err)
				if !m.sleepProjector(projectorBackoffMax) {
					return
				}
				continue
			}
			backoff.observeFailure()
			m.recordProjectorError(state, err)
			if !m.sleepProjector(backoff.delay()) {
				return
			}
			continue
		}
		backoff.observeSuccess()
	}
}

// runProjectorSession runs the projector while it holds the lease.
// Drains the event log, periodically renews the lease, and returns
// when the lease is lost or shutdown is requested.
func (m *MemoryForest) runProjectorSession(state *projectorState, backoff *projectorBackoff) error {
	renewTicker := time.NewTicker(projectorRenewInterval)
	defer renewTicker.Stop()

	for {
		if m.shouldStopProjector() {
			return nil
		}

		processed, err := m.processBranchProjectorBatch(state)
		if err != nil {
			return err
		}
		if processed > 0 {
			backoff.observeSuccess()
			continue
		}

		if err := m.waitForProjectorWork(renewTicker, state, m.projectorWake); err != nil {
			return err
		}
	}
}

// processBranchProjectorBatch consumes up to projectorBatchSize events
// in seq order beyond state.lastAppliedSeq and applies them. Returns
// the number processed; zero means caught up.
//
// Checks shouldStopProjector between events so shutdown drains
// quickly rather than waiting for a full batch. Tracks consecutive
// failures of the same event seq via the poison-pill counter — after
// projectorPoisonPillThreshold consecutive failures of the SAME event
// the projector escalates to halt, preventing an infinite retry
// storm on a deterministic-but-uncategorized error.
func (m *MemoryForest) processBranchProjectorBatch(state *projectorState) (int, error) {
	events, err := m.loadEventsAfterSeq(state.lastAppliedSeq, projectorBatchSize)
	if err != nil {
		return 0, fmt.Errorf("load events: %w", err)
	}
	for i := range events {
		if m.shouldStopProjector() {
			return i, nil
		}
		err := m.applyBranchProjectorEvent(state, &events[i])
		if err == nil {
			state.clearPoison()
			continue
		}
		if errors.Is(err, errProjectorHalt) || errors.Is(err, errLeaseHeldByOther) {
			return i, err
		}
		if state.recordPoisonHit(events[i].Seq) {
			return i, fmt.Errorf("%w: poison pill on seq %d after %d retries: %v",
				errProjectorHalt, events[i].Seq, state.poisonCount, err)
		}
		return i, err
	}
	return len(events), nil
}

// applyBranchProjectorEvent projects a single event onto the branch
// view inside one transaction. On commit, post-commit side effects
// (warmth, training labels, scheduling) fire — these are bounded
// best-effort calls that never block projection progress.
//
// The watermark UPDATE inside the transaction is gated on
// leader_holder == m.projectorID. If our hold has expired and another
// process has acquired the lease, the watermark UPDATE affects 0 rows
// and we return errLeaseHeldByOther — closing the split-brain window
// where two processes could otherwise apply the same event in
// overlapping in-flight transactions.
func (m *MemoryForest) applyBranchProjectorEvent(state *projectorState, event *Event) error {
	if event.Seq <= state.lastAppliedSeq {
		return nil
	}

	tx, err := m.db.BeginTx(m.runCtx, nil)
	if err != nil {
		return classifyApplyErr(fmt.Errorf("begin projector tx: %w", err))
	}
	defer tx.Rollback()

	branch, created, replayDue, applyErr := m.projectBranchTx(m.runCtx, tx, event)
	if applyErr != nil {
		return classifyApplyErr(applyErr)
	}
	updated, err := setProjectorWatermarkUnderLeaseTx(m.runCtx, tx, state.name, m.projectorID, event.Seq, event.Timestamp)
	if err != nil {
		return classifyApplyErr(fmt.Errorf("update projector watermark: %w", err))
	}
	if !updated {
		// Lease was lost between BeginTx and now — abort without
		// committing. The new leader will pick up from its own
		// state.lastAppliedSeq.
		return errLeaseHeldByOther
	}
	if err := tx.Commit(); err != nil {
		return classifyApplyErr(fmt.Errorf("commit projector tx: %w", err))
	}

	state.lastAppliedSeq = event.Seq
	state.lastAppliedAt = event.Timestamp

	// Wake any WaitForBranchSeq callers whose target is now reached.
	// Done outside the transaction so a slow waiter handler can't
	// block the projection loop.
	if m.seqNotify != nil {
		m.seqNotify.Advance(event.Seq)
	}

	m.runProjectorPostCommit(event, branch, created, replayDue)
	return nil
}

// runProjectorPostCommit fires the side-effect calls that used to be
// inline in AppendEvent: warmth, training-example labels, and
// scheduling. Each call is best-effort — failures log but never halt.
func (m *MemoryForest) runProjectorPostCommit(event *Event, branch *Branch, created bool, replayDue time.Time) {
	if branch == nil {
		return
	}
	m.recordProjectorWarmth(event, branch, created)
	labeled := m.recordProjectorLabels(event, branch.ID)

	m.scheduleSubstrateRefresh(event.SessionID)
	if !replayDue.IsZero() {
		m.scheduleReplayAt(replayDue)
	}
	if labeled > 0 {
		m.scheduleTraining()
	}
}

func (m *MemoryForest) recordProjectorWarmth(event *Event, branch *Branch, created bool) {
	if m.warmth == nil {
		return
	}
	if created {
		if err := m.warmth.RecordAccess(m.runCtx, branch.ID, memory.AccessCreation, string(event.EventType)); err != nil {
			slog.Debug("forest_projector_warmth_failed",
				"branch_id", branch.ID, "phase", "creation", "err", err.Error())
		}
	}
	// Issue #4 mechanism A: warmth fires only on AccessCreation (above)
	// and AccessReinforcement (below) — never on AccessRetrieval. Recall
	// events are explicit *use* of a branch by an agent (the agent
	// deliberately invoked it), so they fold into the reinforcement
	// case alongside validation/outcome/replay. AccessRetrieval is
	// reserved for query-result observation, which doesn't reinforce.
	switch event.EventType {
	case EventTypeRecall, EventTypeValidation, EventTypeOutcomeRecorded, EventTypeReplayConsolidated:
		if err := m.warmth.RecordAccess(m.runCtx, branch.ID, memory.AccessReinforcement, event.Title); err != nil {
			slog.Debug("forest_projector_warmth_failed",
				"branch_id", branch.ID, "phase", "reinforcement", "err", err.Error())
		}
	}
}

func (m *MemoryForest) recordProjectorLabels(event *Event, branchID string) int64 {
	switch event.EventType {
	case EventTypeOutcomeRecorded:
		status := outcomeStatusFromPayload(event.Payload)
		return m.applyOutcomeLabel(branchID, event.SessionID, status)
	case EventTypeValidation:
		return m.applyOutcomeLabel(branchID, event.SessionID, OutcomeStatusSucceeded)
	case EventTypeContradiction:
		return m.applyOutcomeLabel(branchID, event.SessionID, OutcomeStatusFailed)
	}
	return 0
}

// applyOutcomeLabel writes the explicit outcome label AND the
// counterfactual labels for co-candidates from recent retrievals.
// Returns the total label count (explicit + counterfactual) for the
// "schedule training" decision in runProjectorPostCommit.
//
// As a side-effect, also feeds calibration / regret observations for
// every retrieval that contained this branch within the
// counterfactual window into the runtime hyperparameter tuner. Each
// observation is attributed to the snapshot (active vs proposed)
// that scored the originating retrieval, captured on the audit row
// at retrieve-time. Best-effort: failures here are observational and
// don't affect labeling.
func (m *MemoryForest) applyOutcomeLabel(branchID, sessionID string, status OutcomeStatus) int64 {
	result, err := m.labelExamplesForOutcomeWithCounterfactuals(m.runCtx, branchID, sessionID, status)
	if err != nil {
		slog.Debug("forest_projector_label_failed",
			"branch_id", branchID, "status", string(status), "err", err.Error())
		return 0
	}
	m.recordAdaptationObservationsForOutcome(m.runCtx, branchID, sessionID, status)
	return result.Explicit + result.Counterfactual
}

func outcomeStatusFromPayload(payload map[string]any) OutcomeStatus {
	if raw, ok := payload["status"].(OutcomeStatus); ok {
		return raw
	}
	if raw, ok := payload["status"].(string); ok {
		return OutcomeStatus(raw)
	}
	return OutcomeStatusMixed
}

// loadEventsAfterSeq fetches branch-compatible events from the canonical
// forest ledger with seq strictly greater than `after`, in ascending seq
// order, up to `limit`.
func (m *MemoryForest) loadEventsAfterSeq(after int64, limit int) ([]Event, error) {
	rows, err := m.db.QueryContext(m.runCtx, `
		SELECT l.seq, l.source_id, l.event_kind, l.session_id, l.task_id,
		       l.subject_id, l.actor_uid, l.actor_type, l.occurred_at,
		       p.payload
		FROM   forest_ledger l
		JOIN   forest_ledger_payloads p ON p.ledger_id = l.id
		WHERE  l.source_kind = ?
		  AND  l.seq > ?
		ORDER BY l.seq ASC
		LIMIT  ?
	`, string(LedgerSourceForestEvent), after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		ev, scanErr := scanLedgerEventRow(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// scanLedgerEventRow scans an Event from the canonical ledger payload and
// backfills projection-critical fields from the relational ledger columns.
func scanLedgerEventRow(rows *sql.Rows) (Event, error) {
	var (
		event     Event
		seq       int64
		sourceID  string
		eventKind string
		sessionID string
		taskID    string
		subjectID string
		actorUID  string
		actorType string
		occurred  int64
		payload   string
	)
	err := rows.Scan(
		&seq, &sourceID, &eventKind, &sessionID, &taskID,
		&subjectID, &actorUID, &actorType, &occurred, &payload,
	)
	if err != nil {
		return event, fmt.Errorf("scan ledger event row: %w", err)
	}
	if err := unmarshalJSON(payload, &event); err != nil {
		return event, fmt.Errorf("decode ledger event payload: %w", err)
	}
	event.Seq = seq
	if event.ID == "" {
		event.ID = sourceID
	}
	if event.SourceID == "" {
		event.SourceID = sourceID
	}
	if event.EventType == "" {
		event.EventType = EventType(eventKind)
	}
	if event.SessionID == "" {
		event.SessionID = sessionID
	}
	if event.TaskID == "" {
		event.TaskID = taskID
	}
	if event.BranchID == "" {
		event.BranchID = subjectID
	}
	if event.AgentID == "" {
		event.AgentID = actorUID
	}
	if event.AgentType == "" {
		event.AgentType = actorType
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Unix(occurred, 0).UTC()
	}
	prepared := prepareEvent(&event)
	prepared.Seq = seq
	return *prepared, nil
}

// ────────────────────────────────────────────────────────────────────
// Lease management
// ────────────────────────────────────────────────────────────────────

// acquireBranchProjectorLease atomically acquires or renews the
// branch projector lease via UPDATE-with-WHERE. Returns the loaded
// state on success, or errLeaseHeldByOther when another process
// holds an unexpired lease.
func (m *MemoryForest) acquireBranchProjectorLease() (*projectorState, error) {
	if err := m.ensureProjectorRow(projectorBranchName); err != nil {
		return nil, err
	}
	now := time.Now().UTC().Unix()
	expires := now + int64(projectorLeaseDuration.Seconds())

	res, err := m.db.ExecContext(m.runCtx, `
		UPDATE forest_projector_state
		SET    leader_holder      = ?,
		       leader_lease_until = ?,
		       health_status      = ?,
		       updated_at         = ?
		WHERE  projector_name      = ?
		  AND  (leader_lease_until < ? OR leader_holder = ?)
	`, m.projectorID, expires, string(ProjectorHealthRunning), now, projectorBranchName, now, m.projectorID)
	if err != nil {
		return nil, fmt.Errorf("acquire projector lease: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, errLeaseHeldByOther
	}
	return m.loadProjectorState(projectorBranchName)
}

func (m *MemoryForest) ensureProjectorRow(name string) error {
	_, err := m.db.ExecContext(m.runCtx, `
		INSERT INTO forest_projector_state (projector_name, updated_at, health_status)
		VALUES (?, ?, ?)
		ON CONFLICT (projector_name) DO NOTHING
	`, name, time.Now().UTC().Unix(), string(ProjectorHealthIdle))
	if err != nil {
		return fmt.Errorf("init projector row: %w", err)
	}
	return nil
}

func (m *MemoryForest) loadProjectorState(name string) (*projectorState, error) {
	row := m.db.QueryRowContext(m.runCtx, `
		SELECT projector_name, last_applied_seq, last_applied_at,
		       leader_holder, leader_lease_until, schema_version,
		       health_status, last_error, last_error_at
		FROM   forest_projector_state
		WHERE  projector_name = ?
	`, name)
	var (
		state        projectorState
		lastApplied  int64
		leaseUntil   int64
		healthStatus string
		lastErrAt    int64
	)
	if err := row.Scan(
		&state.name, &state.lastAppliedSeq, &lastApplied,
		&state.leaderHolder, &leaseUntil, &state.schemaVersion,
		&healthStatus, &state.lastError, &lastErrAt,
	); err != nil {
		return nil, fmt.Errorf("load projector state: %w", err)
	}
	state.lastAppliedAt = unixOrZero(lastApplied)
	state.leaderLeaseUntil = unixOrZero(leaseUntil)
	state.healthStatus = ProjectorHealth(healthStatus)
	state.lastErrorAt = unixOrZero(lastErrAt)
	return &state, nil
}

// renewLease updates leader_lease_until under the existing holder.
// Returns errLeaseHeldByOther if our hold has been taken by another
// process (e.g. our process paused beyond the lease window).
func (m *MemoryForest) renewLease(state *projectorState) error {
	now := time.Now().UTC().Unix()
	expires := now + int64(projectorLeaseDuration.Seconds())
	res, err := m.db.ExecContext(m.runCtx, `
		UPDATE forest_projector_state
		SET    leader_lease_until = ?,
		       updated_at         = ?
		WHERE  projector_name = ?
		  AND  leader_holder  = ?
	`, expires, now, state.name, m.projectorID)
	if err != nil {
		return fmt.Errorf("renew lease: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return errLeaseHeldByOther
	}
	state.leaderLeaseUntil = time.Unix(expires, 0).UTC()
	return nil
}

// setProjectorWatermarkTx persists last_applied_seq inside the same
// transaction as the projection mutation. Used by the synchronous
// projection path (tests) where lease semantics aren't enforced.
func setProjectorWatermarkTx(ctx context.Context, tx *sql.Tx, name string, seq int64, ts time.Time) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE forest_projector_state
		SET    last_applied_seq = ?,
		       last_applied_at  = ?,
		       updated_at       = ?
		WHERE  projector_name   = ?
	`, seq, ts.Unix(), time.Now().UTC().Unix(), name)
	if err != nil {
		return fmt.Errorf("update watermark: %w", err)
	}
	return nil
}

// setProjectorWatermarkUnderLeaseTx persists last_applied_seq, but
// only if the row's leader_holder still matches the caller's holder
// ID. Returns updated=false (with no error) when the lease has been
// taken by another process — caller treats this as a split-brain
// abort and rolls back the transaction.
func setProjectorWatermarkUnderLeaseTx(ctx context.Context, tx *sql.Tx, name, holder string, seq int64, ts time.Time) (bool, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE forest_projector_state
		SET    last_applied_seq = ?,
		       last_applied_at  = ?,
		       updated_at       = ?
		WHERE  projector_name = ?
		  AND  leader_holder  = ?
	`, seq, ts.Unix(), time.Now().UTC().Unix(), name, holder)
	if err != nil {
		return false, fmt.Errorf("update watermark under lease: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("watermark rows affected: %w", err)
	}
	return affected == 1, nil
}

// ────────────────────────────────────────────────────────────────────
// Health / introspection
// ────────────────────────────────────────────────────────────────────

func (m *MemoryForest) markProjectorHalted(state *projectorState, cause error) {
	// Wake any WaitForBranchSeq callers regardless of shutdown state
	// — they should learn the projector halted, not block until
	// timeout.
	if m.seqNotify != nil {
		m.seqNotify.Halt(cause)
	}
	if m.runCtx == nil || m.runCtx.Err() != nil {
		return
	}
	msg := truncateError(cause.Error(), projectorErrTruncate)
	now := time.Now().UTC().Unix()
	_, err := m.db.ExecContext(m.runCtx, `
		UPDATE forest_projector_state
		SET    health_status = ?,
		       last_error    = ?,
		       last_error_at = ?,
		       updated_at    = ?
		WHERE  projector_name = ?
	`, string(ProjectorHealthHalted), msg, now, now, state.name)
	if err != nil {
		slog.Error("forest_projector_mark_halted_failed",
			"projector", state.name, "err", err.Error())
		return
	}
	slog.Error("forest_projector_halted",
		"projector", state.name,
		"holder", m.projectorID,
		"last_applied_seq", state.lastAppliedSeq,
		"err", msg,
	)
}

func (m *MemoryForest) recordProjectorError(state *projectorState, cause error) {
	// Skip error persistence during shutdown — runCtx is already
	// cancelled and the DB may be closing. Silent exit is correct;
	// the cause was almost certainly the cancellation itself.
	if m.runCtx == nil || m.runCtx.Err() != nil {
		return
	}
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return
	}
	msg := truncateError(cause.Error(), projectorErrTruncate)
	now := time.Now().UTC().Unix()
	_, err := m.db.ExecContext(m.runCtx, `
		UPDATE forest_projector_state
		SET    last_error    = ?,
		       last_error_at = ?,
		       updated_at    = ?
		WHERE  projector_name = ?
	`, msg, now, now, state.name)
	if err != nil {
		slog.Warn("forest_projector_record_error_failed",
			"projector", state.name, "err", err.Error())
		return
	}
	slog.Warn("forest_projector_error",
		"projector", state.name,
		"holder", m.projectorID,
		"err", msg,
	)
}

func (m *MemoryForest) handleLeaseAcquireFailure(err error) {
	if errors.Is(err, errLeaseHeldByOther) {
		return
	}
	slog.Warn("forest_projector_lease_failed",
		"holder", m.projectorID, "err", err.Error())
}

// ────────────────────────────────────────────────────────────────────
// Loop control
// ────────────────────────────────────────────────────────────────────

func (m *MemoryForest) shouldStopProjector() bool {
	select {
	case <-m.stopCh:
		return true
	case <-m.runCtx.Done():
		return true
	default:
		return false
	}
}

func (m *MemoryForest) sleepProjector(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-m.stopCh:
		return false
	case <-m.runCtx.Done():
		return false
	}
}

// waitForProjectorWork blocks until a wake signal arrives, the lease
// is due for renewal, or shutdown is requested. Pure event-driven —
// no idle polling. Each projector passes its own wake channel so
// wakes aren't stolen by sibling projectors.
//
// Correctness: notifyProjector() is called on every successful AppendEvent
// canonical-ledger append (and the equivalent on retrieval-audit append for
// the retrieval-candidates projector). Because the wake channel is cap-1,
// multiple events between scans collapse to a single wake, but the next process
// pass drains everything via loadEventsAfterSeq's seq-ordered query, so no
// event is lost.
//
// Robustness: a wake CAN race past a draining channel in pathological
// multi-process scenarios where two writers commit in tight
// succession and the projector's drain happens between them. The
// renewInterval (10s) tick is the backstop — every renew, the outer
// loop re-runs processBranchProjectorBatch, recovering any missed
// wake within at most one renewInterval. No additional safety timer
// is needed.
func (m *MemoryForest) waitForProjectorWork(renew *time.Ticker, state *projectorState, wake <-chan struct{}) error {
	select {
	case <-wake:
		return nil
	case <-renew.C:
		return m.renewLease(state)
	case <-m.stopCh:
		return errors.New("forest closing")
	case <-m.runCtx.Done():
		return errors.New("forest context cancelled")
	}
}

// ────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────

// projectorBackoff implements exponential backoff for projector
// apply failures. Resets on success.
type projectorBackoff struct {
	current time.Duration
}

func newProjectorBackoff() *projectorBackoff {
	return &projectorBackoff{current: projectorBackoffInitial}
}

func (b *projectorBackoff) observeFailure() {
	b.current *= 2
	if b.current > projectorBackoffMax {
		b.current = projectorBackoffMax
	}
}

func (b *projectorBackoff) observeSuccess() {
	b.current = projectorBackoffInitial
}

func (b *projectorBackoff) delay() time.Duration {
	return b.current
}

// classifyApplyErr triages projector apply errors into transient
// (worth retrying) vs fatal (worth halting). Default is transient —
// the cost of false-retry is a brief delay; the cost of false-halt is
// operator wakeup. Only confidently-deterministic errors halt.
//
// Fatal patterns:
//   - context.Canceled / context.DeadlineExceeded *during normal
//     operation* are transient (shutdown). During non-shutdown they
//     surface as transient too — the next iteration handles shutdown
//     via shouldStopProjector.
//   - SQLite "database is locked" / SQLITE_BUSY: transient. Retry.
//   - SQLite "constraint" / "no such column" / "syntax error":
//     fatal. Re-running the same event would fail the same way.
//   - All others: transient (default). The poison-pill counter at
//     the outer-loop level escalates to halt after N consecutive
//     failures of the same event.
func classifyApplyErr(err error) error {
	if err == nil {
		return nil
	}
	if isFatalApplyErr(err) {
		return fmt.Errorf("%w: %v", errProjectorHalt, err)
	}
	return err
}

// isFatalApplyErr returns true when re-running the same event would
// produce the same error — the projector is unrecoverable without
// operator intervention. Conservative: returns true ONLY for
// schema/constraint mismatches that are deterministic.
func isFatalApplyErr(err error) bool {
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "no such column") {
		return true
	}
	if strings.Contains(msg, "no such table") {
		return true
	}
	if strings.Contains(msg, "constraint failed") {
		return true
	}
	if strings.Contains(msg, "constraint violation") {
		return true
	}
	if strings.Contains(msg, "datatype mismatch") {
		return true
	}
	if strings.Contains(msg, "syntax error") {
		return true
	}
	return false
}

// truncateError trims s to at most max bytes without splitting a UTF-8
// rune. Used to bound the size of last_error stored in
// forest_projector_state.
func truncateError(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	// Walk back from `max` until we land on a rune boundary.
	cut := max
	for cut > 0 && !isRuneStartByte(s[cut]) {
		cut--
	}
	return s[:cut] + "...(truncated)"
}

// isRuneStartByte reports whether b is a valid UTF-8 leading byte —
// 0xxxxxxx (ASCII) or 11xxxxxx (multi-byte start). Continuation
// bytes (10xxxxxx) are not rune starts.
func isRuneStartByte(b byte) bool {
	return b&0xC0 != 0x80
}

// debugStackTruncated returns a stack trace bounded to maxBytes.
// Used by the projector panic handler so persistent stacks don't
// blow the last_error budget on forest_projector_state.
func debugStackTruncated(maxBytes int) string {
	stack := string(debug.Stack())
	return truncateError(stack, maxBytes)
}

func unixOrZero(ts int64) time.Time {
	if ts <= 0 {
		return time.Time{}
	}
	return time.Unix(ts, 0).UTC()
}

// ────────────────────────────────────────────────────────────────────
// Read-your-writes
// ────────────────────────────────────────────────────────────────────

// WaitForBranchSeq blocks until the branch projector has applied
// events through `seq`. Used by tests and any caller that needs
// strict read-after-write semantics on the branch projection.
//
// Event-driven via the seq notifier — no polling. The projector
// closes the waiter's done channel the moment it commits an apply
// whose seq satisfies the target. Halt fires the channel too;
// waiters re-check the halt state on wake.
//
// Three exit conditions:
//
//   - target seq reached: return nil
//   - projector halted: return the halt cause
//   - context cancelled or forest closed: return ctx.Err() / forest-
//     closed sentinel
//   - timeout: return a timeout error (rare — implies the projector
//     is alive but persistently behind, which usually means lease
//     contention or DB pressure that operator should investigate)
func (m *MemoryForest) WaitForBranchSeq(ctx context.Context, seq int64, timeout time.Duration) error {
	if seq <= 0 {
		return nil
	}
	if m.seqNotify == nil {
		return errors.New("forest seq notifier not initialized")
	}

	// Fast path: already past the target.
	current, halted := m.seqNotify.snapshot()
	if halted != nil {
		return fmt.Errorf("branch projector halted: %w", halted)
	}
	if current >= seq {
		return nil
	}

	waiter := m.seqNotify.register(seq)
	defer m.seqNotify.remove(waiter)

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-waiter.done:
		// Notified by either Advance (target reached) or Halt
		// (fatal). Re-check to disambiguate.
		current, halted := m.seqNotify.snapshot()
		if halted != nil && current < seq {
			return fmt.Errorf("branch projector halted: %w", halted)
		}
		return nil
	case <-timer.C:
		// Last-second check: maybe Advance and timer fired in the
		// same instant. Return success rather than a false timeout.
		current, halted := m.seqNotify.snapshot()
		if current >= seq {
			return nil
		}
		if halted != nil {
			return fmt.Errorf("branch projector halted: %w", halted)
		}
		return fmt.Errorf("timeout waiting for branch projector seq %d", seq)
	case <-ctx.Done():
		return ctx.Err()
	case <-m.runCtx.Done():
		return errors.New("forest closed")
	}
}

// ProjectorStatus returns the current state of the named projector
// (defaults to the branch projector). Used by tests, health checks,
// and the Forest's Health() surface.
func (m *MemoryForest) ProjectorStatus(name string) (*projectorState, error) {
	if strings.TrimSpace(name) == "" {
		name = projectorBranchName
	}
	return m.loadProjectorState(name)
}
