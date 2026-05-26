// Package shared — consult continuation framework.
//
// This file implements the agent-loop machinery that lets a tool
// handler (specifically await_consults) yield mid-LLM-turn while
// awaiting peer consults, persist the LLM turn state durably to the
// claims board, release the agent's replica lease, and resume on a
// fresh replica when the awaited consults resolve.
//
// Three pieces:
//
//  1. TurnSnapshot — a JSON-serializable capture of everything needed
//     to re-enter ExecuteTurnLoop at the await point. Stored as a
//     continuation_context artifact on a ConsultContinuation claim.
//
//  2. ContinuationStore — per-agent registry of in-flight
//     continuations. Keyed by continuation_id and indexed by awaited
//     consult_id so a ConsultResolvedDelta arrival can locate its
//     parent continuation in O(1). Survives process restart via
//     restart-recovery scan of the board.
//
//  3. AwaitConsultsOrYield — the helper the await_consults skill
//     handler invokes. Inspects already-resolved consults (fast path,
//     return inline), persists the snapshot for unresolved consults,
//     and returns ErrYielded — propagated up through the tool
//     dispatch and ExecuteTurnLoop so the agent's request handler
//     exits cleanly without flushing or finalizing the turn.
//
// Resume contract: the agent registers a ResumeFn at startup. When
// all awaited consults for a continuation are resolved, the
// ContinuationStore acquires a replica via the agent's existing
// admission control, restores the turn state into a fresh ctx, and
// invokes ResumeFn. ResumeFn re-enters ExecuteTurnLoop with the
// restored Request, with the await_consults tool result message
// pre-populated from the resolved consult responses.
package shared

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/google/uuid"
)

// turnSnapshotCodecVersion is the schema version for TurnSnapshot.
// On restart recovery, continuations whose snapshot version exceeds
// this constant are rejected — they were written by a newer binary
// and cannot be safely restored. Bump this when the snapshot shape
// changes incompatibly.
const turnSnapshotCodecVersion = 1

// ErrConsultYielded is the continuation-store's internal slow-path
// signal when the LLM turn has yielded to await peer consults. Tool
// handlers convert it to a structured ToolOutcome before crossing the
// tool-runtime boundary.
//
// Some outer turn loops still use this sentinel to short-circuit
// request handling after the tool runtime has already classified the
// tool call as yielded.
// The agent's request handler MUST recognize it after ExecuteTurnLoop
// returns, skip the normal completion path (no testament flush, no
// "completed" status update), release the replica lease, and exit.
// The continuation is already persisted to the board at this point;
// resume will arrive later via the inbox.
var ErrConsultYielded = errors.New("shared: consult turn yielded — continuation persisted, resume pending")

// IsConsultYielded reports whether err signals a consult-yield.
// Tolerant of wrapping (errors.Is).
func IsConsultYielded(err error) bool { return errors.Is(err, ErrConsultYielded) }

// AccumulatorSnapshot captures TestamentAccumulator state for
// continuation persistence. Restore via claims.RestoreAccumulator.
type AccumulatorSnapshot struct {
	AgentID   string             `json:"agent_id"`
	SessionID string             `json:"session_id"`
	ClaimID   string             `json:"claim_id,omitempty"`
	Started   time.Time          `json:"started"`
	Artifacts []*claims.Artifact `json:"artifacts,omitempty"`
	Notes     []string           `json:"notes,omitempty"`
}

// TurnSnapshot is the durable continuation payload. JSON-serializable
// so it can persist as a continuation_context artifact on a
// ConsultContinuation claim and survive process restart.
type TurnSnapshot struct {
	// Version is the codec version. Mismatched versions are rejected
	// at restore time so a binary upgrade cannot silently mis-decode
	// snapshots written by an older binary.
	Version int `json:"version"`

	// AgentID and SessionID identify the issuing agent + session.
	// On restart recovery the framework matches snapshots to the
	// recovering agent's identity.
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id"`

	// CorrelationID is the agent's per-request correlation
	// (typically the steering ledger ID). Preserved so resumed
	// telemetry/logs stitch back to the original request.
	CorrelationID string `json:"correlation_id"`

	// Request is the full LLM request state at the yield point. On
	// resume, the framework injects the await_consults tool result
	// message (see AwaitToolCallID) and re-enters ExecuteTurnLoop
	// with this Request.
	Request *providers.Request `json:"request"`

	// AccumulatorState lets the resume path re-attach the in-flight
	// testament accumulator (artifacts + notes accumulated before
	// the yield) so the post-resume turn keeps producing into the
	// same testament.
	AccumulatorState AccumulatorSnapshot `json:"accumulator_state"`

	// AwaitedConsultIDs are the consult_id values the LLM is
	// waiting on. Each ID corresponds to one of the consult_peer
	// tickets the LLM accumulated before the await_consults call.
	AwaitedConsultIDs []string `json:"awaited_consult_ids"`

	// AwaitDeadline is the absolute time at which AwaitConsultsOrYield
	// should give up and resume with whatever resolutions arrived
	// (timeouts populated for the rest). Drives the ConsultStatusTimeout
	// emission logic.
	AwaitDeadline time.Time `json:"await_deadline"`

	// AwaitToolCallID is the LLM tool_call ID for the
	// await_consults invocation that yielded. The resume path
	// constructs the tool result message with this ID so the LLM
	// pairs the result with its original call.
	AwaitToolCallID string `json:"await_tool_call_id"`

	// AwaitToolName is the tool name that yielded ("await_consults"
	// in production; parameterized so test/alternative implementations
	// can use a different name).
	AwaitToolName string `json:"await_tool_name"`

	// YieldedAt is the wall-clock time of the yield. Diagnostic.
	YieldedAt time.Time `json:"yielded_at"`
}

// ResumeFn is the per-agent entry point invoked by the
// ContinuationStore when a continuation's awaited consults have all
// resolved. The framework re-acquires a replica before invoking the
// fn (via the agent's normal admission path). The fn rebuilds the
// agent's tool definitions, restores the accumulator into ctx, and
// re-enters ExecuteTurnLoop.
//
// Implementations must be re-entrant — multiple continuations may
// resume concurrently for the same agent.
type ResumeFn func(ctx context.Context, snapshot *TurnSnapshot, results map[string]*claims.ConsultResolvedDelta) error

// pendingContinuation tracks one in-flight yielded turn.
type pendingContinuation struct {
	id         string                                  // ConsultContinuation claim ID
	snapshot   *TurnSnapshot                           // serialized turn state
	awaiting   map[string]struct{}                     // consult_ids still pending
	resolved   map[string]*claims.ConsultResolvedDelta // consult_id → resolved delta
	deadline   time.Time
	idleWindow time.Duration
	activity   map[string]time.Time // consult_id -> most recent observed claim activity
	deadlineFn context.CancelFunc   // cancels the deadline watcher

	// dispatched serializes resume invocation. Two concurrent
	// completing consults (last-awaiting + last-awaiting due to
	// e.g. simultaneous resolutions arriving) could both observe
	// awaiting=0 and call dispatchResume; the dispatched flag
	// (under the store mutex) guarantees ResumeFn fires exactly
	// once per continuation.
	dispatched bool
}

// ContinuationStore is per-agent. Owns the in-memory index of
// pending continuations and dispatches resume callbacks when all
// awaited consults resolve. Survives process restart by reading
// pending ConsultContinuation claims from the board on agent Start.
type ContinuationStore struct {
	mu sync.Mutex

	agentID   string
	sessionID string
	board     *claims.ClaimsBoard

	// pending maps continuation_id → state.
	pending map[string]*pendingContinuation

	// consultIndex maps consult_id → continuation_id, for O(1)
	// lookup when ConsultResolvedDelta arrives at the inbox.
	// A consult_id appears in at most one continuation (the one
	// that issued it).
	consultIndex map[string]string

	// resumeFn is the agent-specific resume entry point. Invoked
	// from a tracked goroutine when all awaiting consults for a
	// continuation are resolved.
	resumeFn ResumeFn

	// scope is the agent's tracked goroutine scope; resume
	// invocations run via scope.Go for lifecycle safety. Nil-safe:
	// when nil, the resume runs inline on the inbox dispatch
	// goroutine (acceptable for tests, not production).
	scope goroutineScopeProxy

	// orphans buffers ConsultResolvedDeltas that arrived before
	// their issuing AwaitConsultsOrYield call registered the
	// continuation. Claimed by the next await call referencing the
	// consult_id; expire after orphanResolutionsMaxAge.
	orphans map[string]orphanedResolution
}

// GoroutineScopeProxy is the minimal interface ContinuationStore
// needs from the agent's GoroutineScope. Matches concurrency.GoroutineScope's
// Go method exactly so agents can pass their existing scope without
// an adapter.
type GoroutineScopeProxy interface {
	Go(description string, timeout time.Duration, fn concurrency.WorkFunc) error
}

type goroutineScopeProxy = GoroutineScopeProxy

// ContinuationStoreConfig wires the per-agent store at construction.
// All fields except ResumeFn are required.
type ContinuationStoreConfig struct {
	AgentID   string
	SessionID string
	Board     *claims.ClaimsBoard
	ResumeFn  ResumeFn
	Scope     GoroutineScopeProxy // optional; recommended in production
}

// NewContinuationStore constructs a per-agent continuation store.
// Returns nil if AgentID, SessionID, or Board is missing — the
// continuation framework cannot operate without a board (the
// continuation is the canonical durable state).
func NewContinuationStore(cfg ContinuationStoreConfig) *ContinuationStore {
	if cfg.AgentID == "" || cfg.SessionID == "" || cfg.Board == nil {
		return nil
	}
	return &ContinuationStore{
		agentID:      cfg.AgentID,
		sessionID:    cfg.SessionID,
		board:        cfg.Board,
		pending:      make(map[string]*pendingContinuation),
		consultIndex: make(map[string]string),
		resumeFn:     cfg.ResumeFn,
		scope:        cfg.Scope,
	}
}

// AwaitOptions configures a single AwaitConsultsOrYield invocation.
type AwaitOptions struct {
	// ConsultIDs are the consult_id values to await. Empty is a
	// programming error and yields immediately with an empty
	// results map.
	ConsultIDs []string

	// AwaitToolCallID is the tool_call_id of the await_consults
	// invocation. Persisted in the snapshot so the resume path can
	// construct a paired tool result message.
	AwaitToolCallID string

	// AwaitToolName names the yielding tool ("await_consults" in
	// production). Used by the resume path to format the tool
	// result message's ToolName field.
	AwaitToolName string

	// Deadline is the absolute time at which the await should give
	// its first inactivity window. Activity on the awaited consult
	// claim moves the effective timeout forward. Zero value defaults
	// to 5 minutes from now.
	Deadline time.Time

	// IdleTimeout is the inactivity window for awaited consults.
	// When zero, it is derived from Deadline-now. The continuation
	// times out only after an awaited consult has produced no claim
	// activity for this window.
	IdleTimeout time.Duration

	// Snapshot is the TurnSnapshot to persist. The caller (the
	// await_consults tool handler) constructs this from the active
	// LLM turn state — captured via WithTurnContext / TurnFromContext.
	// The store stamps Version, YieldedAt, AwaitToolCallID,
	// AwaitToolName, AwaitedConsultIDs, AwaitDeadline before
	// persisting.
	Snapshot *TurnSnapshot
}

// AwaitConsultsOrYield is the central yield primitive. The
// await_consults tool handler invokes it after the LLM emits an
// await_consults tool call. Behavior:
//
//  1. Validates options. ErrYielded never returns on validation
//     failure — the handler should surface validation errors as
//     normal tool errors so the LLM can correct its call.
//
//  2. Fast path: if all requested consults are already resolved
//     (e.g. they completed before the LLM had a chance to call
//     await_consults), returns the results inline with yielded=false.
//     The handler converts the results to its tool-result format and
//     returns normally — the LLM continues without a yield.
//
//  3. Slow path: persists the TurnSnapshot to the board as a
//     ConsultContinuation claim, registers the awaited consult_ids
//     in the index, starts a deadline watcher, and returns
//     (nil, true, ErrConsultYielded). The handler converts this to
//     ToolOutcome{Status: yielded}; the dispatch loop short-circuits
//     without treating the yielded tool as a failure.
//
// Resumption later flows through Store.deliverResolution → ResumeFn.
func (s *ContinuationStore) AwaitConsultsOrYield(
	ctx context.Context,
	opts AwaitOptions,
) (results map[string]*claims.ConsultResolvedDelta, yielded bool, err error) {
	if s == nil {
		return nil, false, errors.New("await_consults: continuation store not configured")
	}
	if len(opts.ConsultIDs) == 0 {
		return nil, false, errors.New("await_consults: no consult_ids supplied")
	}
	if opts.Snapshot == nil {
		return nil, false, errors.New("await_consults: snapshot is required")
	}
	if opts.AwaitToolCallID == "" {
		return nil, false, errors.New("await_consults: await_tool_call_id is required")
	}
	if opts.AwaitToolName == "" {
		opts.AwaitToolName = "await_consults"
	}

	now := time.Now().UTC()
	deadline := opts.Deadline
	if deadline.IsZero() {
		deadline = now.Add(5 * time.Minute)
	}
	idleWindow := opts.IdleTimeout
	if idleWindow <= 0 {
		idleWindow = time.Until(deadline)
	}
	if idleWindow <= 0 {
		idleWindow = 0
	}
	deadline = now.Add(idleWindow)

	s.mu.Lock()

	// Fast path: collect already-resolved consults that arrived before
	// this await call. If ALL are resolved, return inline.
	preResolved := make(map[string]*claims.ConsultResolvedDelta, len(opts.ConsultIDs))
	stillAwaiting := make(map[string]struct{}, len(opts.ConsultIDs))
	for _, id := range opts.ConsultIDs {
		// We pre-stage the index: ConsultResolvedDelta arrival
		// stores into pending[contID].resolved, but only after the
		// continuation exists. Pre-await resolutions therefore have
		// no continuation yet — they're captured into a separate
		// map. (Implemented in the resolution-routing path below
		// via OrphanResolutions.)
		if delta := s.takeOrphanResolutionLocked(id); delta != nil {
			preResolved[id] = delta
			continue
		}
		stillAwaiting[id] = struct{}{}
	}

	if len(stillAwaiting) == 0 {
		s.mu.Unlock()
		return preResolved, false, nil
	}

	// Slow path: build the snapshot envelope and persist as a
	// ConsultContinuation claim before registering in the index. If
	// the persist fails we surface the error and do NOT yield —
	// the LLM continues with whatever resolutions are already in
	// hand (degrading to the fast path's behavior).
	snapshot := opts.Snapshot
	snapshot.Version = turnSnapshotCodecVersion
	snapshot.AgentID = s.agentID
	snapshot.SessionID = s.sessionID
	snapshot.AwaitedConsultIDs = opts.ConsultIDs
	snapshot.AwaitToolCallID = opts.AwaitToolCallID
	snapshot.AwaitToolName = opts.AwaitToolName
	snapshot.AwaitDeadline = deadline
	snapshot.YieldedAt = now

	continuationID, err := s.persistContinuationLocked(ctx, snapshot, opts.ConsultIDs, deadline, idleWindow, preResolved)
	if err != nil {
		s.mu.Unlock()
		// Persist failure: degrade to fast path with whatever
		// resolutions we have. Caller will resume the LLM with
		// partial results; LLM can re-call await if needed.
		slog.Error("await_consults_persist_failed",
			"agent_id", s.agentID, "session_id", s.sessionID,
			"consult_ids", opts.ConsultIDs, "error", err.Error(),
		)
		return preResolved, false, fmt.Errorf("await_consults: persist continuation: %w", err)
	}

	pending := &pendingContinuation{
		id:         continuationID,
		snapshot:   snapshot,
		awaiting:   stillAwaiting,
		resolved:   preResolved,
		deadline:   deadline,
		idleWindow: idleWindow,
		activity:   initialConsultActivity(stillAwaiting, now),
	}
	s.pending[continuationID] = pending
	for id := range stillAwaiting {
		s.consultIndex[id] = continuationID
	}
	s.mu.Unlock()

	// Start the inactivity watcher in a tracked goroutine. The first
	// window begins at yield time; each observed consult-claim
	// activity moves that consult's timeout forward.
	s.startDeadlineWatcher(continuationID, idleWindow)

	return nil, true, ErrConsultYielded
}

// DeliverResolution is the inbox-side entry: when a
// ConsultResolvedDelta arrives at the issuing agent's inbox, the
// dispatcher calls this with the delta. If the delta matches a
// pending continuation, it's recorded; if all of that continuation's
// awaited consults are now resolved, the resume is dispatched.
//
// If the delta arrives BEFORE its continuation is registered (a race
// where the peer responded faster than the issuer reached
// AwaitConsultsOrYield), the resolution is held in an orphan map and
// claimed by the next AwaitConsultsOrYield call referencing that
// consult_id.
func (s *ContinuationStore) DeliverResolution(ctx context.Context, delta *claims.ConsultResolvedDelta) {
	if s == nil || delta == nil {
		return
	}
	s.mu.Lock()
	contID, ok := s.consultIndex[delta.ConsultID]
	if !ok {
		// Orphan: stash for the eventual await call.
		s.stashOrphanResolutionLocked(delta)
		s.mu.Unlock()
		return
	}
	pending, ok := s.pending[contID]
	if !ok {
		// Index inconsistency — should not happen. Drop the
		// resolution; the continuation's deadline watcher will
		// handle the stranded await.
		delete(s.consultIndex, delta.ConsultID)
		s.mu.Unlock()
		slog.Warn("consult_resolution_continuation_missing",
			"agent_id", s.agentID, "consult_id", delta.ConsultID,
			"continuation_id", contID,
		)
		return
	}
	pending.resolved[delta.ConsultID] = delta
	delete(pending.awaiting, delta.ConsultID)
	delete(s.consultIndex, delta.ConsultID)
	complete := len(pending.awaiting) == 0
	s.mu.Unlock()

	if complete {
		s.dispatchResume(ctx, pending)
	}
}

// dispatchResume runs the agent's ResumeFn. Acquires the agent's
// scope when present so the resume goroutine is tracked. Race-
// guarded by pending.dispatched: at most one ResumeFn invocation
// per continuation, regardless of how many concurrent paths reach
// "complete".
func (s *ContinuationStore) dispatchResume(ctx context.Context, pending *pendingContinuation) {
	if s.resumeFn == nil {
		slog.Warn("continuation_resume_skipped_no_resume_fn",
			"agent_id", s.agentID, "continuation_id", pending.id,
		)
		return
	}
	// Snapshot the resolved map under lock so the resume sees a
	// stable copy even if more resolutions arrive concurrently.
	// Race guard: the dispatched flag fences double-resume when
	// two completing consults both see awaiting=0 simultaneously.
	s.mu.Lock()
	if pending.dispatched {
		s.mu.Unlock()
		return
	}
	pending.dispatched = true
	results := make(map[string]*claims.ConsultResolvedDelta, len(pending.resolved))
	for id, delta := range pending.resolved {
		results[id] = delta
	}
	delete(s.pending, pending.id)
	if pending.deadlineFn != nil {
		pending.deadlineFn()
	}
	s.mu.Unlock()

	resume := func(workerCtx context.Context) error {
		if err := s.resumeFn(workerCtx, pending.snapshot, results); err != nil {
			slog.Error("continuation_resume_failed",
				"agent_id", s.agentID, "continuation_id", pending.id,
				"error", err.Error(),
			)
		}
		// Mark the continuation claim's validation as passed so
		// future restart-recovery scans skip it.
		s.markContinuationResolved(workerCtx, pending.id)
		return nil
	}

	if s.scope != nil {
		if err := s.scope.Go("continuation_resume", 0, resume); err != nil {
			slog.Warn("continuation_resume_dispatch_failed",
				"agent_id", s.agentID, "continuation_id", pending.id,
				"error", err.Error(),
			)
		}
		return
	}
	// Fall back to inline execution when no scope wired (tests).
	_ = resume(ctx)
}

// startDeadlineWatcher kicks off a tracked goroutine that fires
// synthetic timeout resolutions for any still-awaiting consults
// after an inactivity window. Claim activity on an awaited consult
// moves that consult's timeout forward, so a responder that is still
// reading, thinking, or using tools does not get cut off by a fixed
// wall-clock deadline.
func (s *ContinuationStore) startDeadlineWatcher(continuationID string, idleWindow time.Duration) {
	if idleWindow <= 0 {
		s.fireDeadline(continuationID)
		return
	}
	watcherCtx, cancel := context.WithCancel(context.Background())

	s.mu.Lock()
	if pending, ok := s.pending[continuationID]; ok {
		pending.deadlineFn = cancel
	}
	s.mu.Unlock()

	watch := func(workerCtx context.Context) error {
		for {
			wait, ok := s.nextDeadlineWait(continuationID)
			if !ok {
				return nil
			}
			if wait <= 0 {
				if s.fireDeadline(continuationID) {
					return nil
				}
				continue
			}
			timer := time.NewTimer(wait)
			select {
			case <-workerCtx.Done():
				timer.Stop()
				return nil
			case <-watcherCtx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
				if s.fireDeadline(continuationID) {
					return nil
				}
			}
		}
	}
	if s.scope != nil {
		if err := s.scope.Go("continuation_deadline_watcher", 0, watch); err != nil {
			slog.Warn("continuation_deadline_watcher_dispatch_failed",
				"agent_id", s.agentID, "continuation_id", continuationID,
				"error", err.Error(),
			)
			cancel()
		}
		return
	}
	// No scope: run watcher inline in a bare goroutine. Acceptable
	// for tests; production wires a scope.
	go func() { _ = watch(watcherCtx) }()
}

func (s *ContinuationStore) nextDeadlineWait(continuationID string) (time.Duration, bool) {
	s.refreshContinuationActivity(continuationID)

	s.mu.Lock()
	defer s.mu.Unlock()
	pending, ok := s.pending[continuationID]
	if !ok || len(pending.awaiting) == 0 {
		return 0, false
	}
	idleWindow := pending.idleWindow
	if idleWindow <= 0 {
		idleWindow = time.Until(pending.deadline)
	}
	if idleWindow <= 0 {
		return 0, true
	}
	var next time.Time
	for id := range pending.awaiting {
		last := pending.activity[id]
		if last.IsZero() {
			last = pending.snapshot.YieldedAt
		}
		due := last.Add(idleWindow)
		if next.IsZero() || due.Before(next) {
			next = due
		}
	}
	if next.IsZero() {
		return 0, false
	}
	return time.Until(next), true
}

// fireDeadline materializes synthetic timeout resolutions for any
// still-awaiting consults of the named continuation, then dispatches
// resume.
func (s *ContinuationStore) fireDeadline(continuationID string) bool {
	s.refreshContinuationActivity(continuationID)

	s.mu.Lock()
	pending, ok := s.pending[continuationID]
	if !ok {
		s.mu.Unlock()
		return true
	}
	now := time.Now().UTC()
	idleWindow := pending.idleWindow
	if idleWindow <= 0 {
		idleWindow = time.Until(pending.deadline)
	}
	for id := range pending.awaiting {
		last := pending.activity[id]
		if last.IsZero() {
			last = pending.snapshot.YieldedAt
		}
		if idleWindow > 0 && now.Sub(last) < idleWindow {
			continue
		}
		pending.resolved[id] = &claims.ConsultResolvedDelta{
			ConsultID:         id,
			OriginatorAgentID: s.agentID,
			Status:            claims.ConsultStatusTimeout,
			ErrorMessage:      "consult deadline elapsed",
			EmittedAt:         now,
		}
		delete(s.consultIndex, id)
		delete(pending.activity, id)
		delete(pending.awaiting, id)
	}
	complete := len(pending.awaiting) == 0
	s.mu.Unlock()
	if complete {
		s.dispatchResume(context.Background(), pending)
	}
	return complete
}

func (s *ContinuationStore) refreshContinuationActivity(continuationID string) {
	ids, ok := s.awaitingConsultIDs(continuationID)
	if !ok || len(ids) == 0 {
		return
	}
	activity := s.consultActivitySnapshot(ids)
	if len(activity) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pending, ok := s.pending[continuationID]
	if !ok {
		return
	}
	if pending.activity == nil {
		pending.activity = make(map[string]time.Time, len(pending.awaiting))
	}
	for id, at := range activity {
		if at.After(pending.activity[id]) {
			pending.activity[id] = at
		}
	}
}

func (s *ContinuationStore) awaitingConsultIDs(continuationID string) ([]string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending, ok := s.pending[continuationID]
	if !ok || len(pending.awaiting) == 0 {
		return nil, false
	}
	ids := make([]string, 0, len(pending.awaiting))
	for id := range pending.awaiting {
		ids = append(ids, id)
	}
	return ids, true
}

func initialConsultActivity(awaiting map[string]struct{}, at time.Time) map[string]time.Time {
	out := make(map[string]time.Time, len(awaiting))
	for id := range awaiting {
		out[id] = at
	}
	return out
}

func (s *ContinuationStore) recoveredConsultActivity(consultIDs []string, fallback time.Time) map[string]time.Time {
	out := make(map[string]time.Time, len(consultIDs))
	for _, id := range consultIDs {
		if id != "" {
			out[id] = fallback
		}
	}
	for id, at := range s.consultActivitySnapshot(consultIDs) {
		if at.After(out[id]) {
			out[id] = at
		}
	}
	return out
}

func (s *ContinuationStore) consultActivitySnapshot(consultIDs []string) map[string]time.Time {
	if s == nil || s.board == nil || len(consultIDs) == 0 {
		return nil
	}
	wanted := make(map[string]struct{}, len(consultIDs))
	for _, id := range consultIDs {
		if id != "" {
			wanted[id] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	proj := s.board.Projection()
	if proj == nil {
		return nil
	}
	out := make(map[string]time.Time, len(wanted))
	for i := range proj.Claims {
		claim := proj.Claims[i]
		for id := range wanted {
			if !continuationAwaitMatchesClaim(id, claim) {
				continue
			}
			at := latestClaimActivityTime(claim)
			if at.After(out[id]) {
				out[id] = at
			}
		}
	}
	for i := range proj.Testaments {
		testament := proj.Testaments[i]
		claimID := claims.ClaimIDFromRelations(testament.Relations)
		if _, ok := wanted[claimID]; !ok {
			continue
		}
		at := latestTestamentActivityTime(testament)
		if at.After(out[claimID]) {
			out[claimID] = at
		}
	}
	return out
}

func continuationAwaitMatchesClaim(awaitID string, claim claims.Claim) bool {
	if claim.ID == awaitID {
		return true
	}
	for _, entry := range claim.Scope {
		switch strings.TrimSpace(entry.Kind) {
		case "consult_id", "challenge_id", "await_id":
			if strings.TrimSpace(entry.Key) == awaitID {
				return true
			}
		}
	}
	return false
}

func latestClaimActivityTime(claim claims.Claim) time.Time {
	latest := claim.Created
	if claim.Accessed.After(latest) {
		latest = claim.Accessed
	}
	for _, status := range claim.StatusHistory {
		if status.Changed.After(latest) {
			latest = status.Changed
		}
	}
	for _, validation := range claim.Validations {
		if validation == nil {
			continue
		}
		if validation.Accessed.After(latest) {
			latest = validation.Accessed
		}
		for _, status := range validation.StatusHistory {
			if status.Changed.After(latest) {
				latest = status.Changed
			}
		}
	}
	return latest
}

func latestTestamentActivityTime(testament claims.Testament) time.Time {
	latest := testament.Created
	if testament.Accessed.After(latest) {
		latest = testament.Accessed
	}
	for _, artifact := range testament.Artifacts {
		if artifact == nil {
			continue
		}
		if artifact.Created.After(latest) {
			latest = artifact.Created
		}
		if artifact.Accessed.After(latest) {
			latest = artifact.Accessed
		}
	}
	return latest
}

// persistContinuationLocked writes a ConsultContinuation claim to the
// board carrying the serialized TurnSnapshot. Caller holds s.mu.
// The claim ID is the continuation_id.
func (s *ContinuationStore) persistContinuationLocked(
	ctx context.Context,
	snapshot *TurnSnapshot,
	consultIDs []string,
	deadline time.Time,
	idleWindow time.Duration,
	alreadyResolved map[string]*claims.ConsultResolvedDelta,
) (string, error) {
	if s.board == nil {
		return "", errors.New("no board configured")
	}

	contextBytes, err := json.Marshal(snapshot)
	if err != nil {
		return "", fmt.Errorf("marshal snapshot: %w", err)
	}

	continuationID := "cont_" + uuid.NewString()

	artifacts := make([]*claims.Artifact, 0, len(consultIDs)+2)
	artifacts = append(artifacts, &claims.Artifact{
		ID:        uuid.NewString(),
		AgentID:   s.agentID,
		SessionID: s.sessionID,
		Kind:      claims.ArtifactKindContinuationContext,
		Reference: string(contextBytes),
		Created:   time.Now().UTC(),
	})
	artifacts = append(artifacts, &claims.Artifact{
		ID:        uuid.NewString(),
		AgentID:   s.agentID,
		SessionID: s.sessionID,
		Kind:      claims.ArtifactKindContinuationVersion,
		Reference: fmt.Sprintf("%d", turnSnapshotCodecVersion),
		Created:   time.Now().UTC(),
	})
	for _, cid := range consultIDs {
		artifacts = append(artifacts, &claims.Artifact{
			ID:        uuid.NewString(),
			AgentID:   s.agentID,
			SessionID: s.sessionID,
			Kind:      claims.ArtifactKindContinuationAwait,
			Reference: cid,
			Metadata: map[string]any{
				"deadline":        deadline.UTC().Format(time.RFC3339Nano),
				"idle_timeout_ms": idleWindow.Milliseconds(),
			},
			Created: time.Now().UTC(),
		})
	}

	claim := claims.Claim{
		ID:          continuationID,
		Title:       fmt.Sprintf("Consult continuation for %s", s.agentID),
		Description: fmt.Sprintf("Yielded turn awaiting %d consult(s)", len(consultIDs)),
		ActionType:  claims.ActionTypeConsultContinuation,
		Relations: []claims.Relation{
			{Related: s.agentID, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			{Related: s.agentID, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
		},
		Validations: []*claims.Validation{{
			Type:        claims.ValidationTypeReceipt,
			Required:    true,
			Description: "All awaited consults resolved and continuation resumed",
			QualityBar:  "all_consults_resolved",
			Status:      claims.ValidationStatusPending,
		}},
	}

	action := claims.Action{
		AgentID: s.agentID,
		Type:    claims.ActionTypeConsultContinuation,
	}
	posted := []claims.Claim{claim}
	if err := s.board.PostAction(ctx, action, posted); err != nil {
		return "", fmt.Errorf("post continuation claim: %w", err)
	}

	// Submit the artifacts as a testament so they're durable on the
	// board (PostAction only commits the claim shell — artifacts go
	// via SubmitTestaments).
	testament := claims.Testament{
		AgentID:    s.agentID,
		SessionID:  s.sessionID,
		Summary:    fmt.Sprintf("Continuation snapshot for %d consults", len(consultIDs)),
		Confidence: "high",
		Relations: []claims.Relation{
			{Related: continuationID, RelatedType: claims.RelatedTypeClaim, Relationship: claims.RelationshipClaim},
		},
		Artifacts: artifacts,
	}
	if err := s.board.SubmitTestaments(ctx, action, []claims.Testament{testament}); err != nil {
		// Continuation claim is posted but artifacts failed —
		// still register in-memory; resume will work, restart
		// recovery will lose this continuation. Log loudly.
		slog.Error("await_consults_artifact_persist_failed",
			"agent_id", s.agentID, "continuation_id", continuationID,
			"error", err.Error(),
		)
	}
	_ = alreadyResolved // consumed by caller via preResolved map
	return continuationID, nil
}

// markContinuationResolved marks the ConsultContinuation claim's
// receipt validation passed so future restart scans skip it.
func (s *ContinuationStore) markContinuationResolved(ctx context.Context, continuationID string) {
	if s.board == nil {
		return
	}
	proj := s.board.Projection()
	if proj == nil {
		return
	}
	for _, c := range proj.Claims {
		if c.ID != continuationID {
			continue
		}
		for _, v := range c.Validations {
			if v == nil || v.Status != claims.ValidationStatusPending {
				continue
			}
			_ = s.board.EvaluateValidation(ctx, c.ID, v.ID, claims.StatusChange{
				To:      string(claims.ValidationStatusPassed),
				Reason:  "continuation resumed",
				AgentID: s.agentID,
				Changed: time.Now().UTC(),
			})
		}
		return
	}
}

// Stop cancels every in-flight continuation in this store. Called
// from each agent's Stop / shutdown path so a graceful agent exit
// surfaces synthetic Cancelled resolutions for any awaiting consults
// (rather than letting the deadline watcher fire much later) and
// marks the corresponding ConsultContinuation claim's validation as
// failed so restart-recovery skips them.
//
// No ResumeFn is invoked — Stop is a terminal signal that the agent
// is going away; resuming the LLM turn during shutdown would be
// counterproductive.
func (s *ContinuationStore) Stop(reason string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	pending := make([]*pendingContinuation, 0, len(s.pending))
	for _, p := range s.pending {
		pending = append(pending, p)
	}
	s.pending = make(map[string]*pendingContinuation)
	s.consultIndex = make(map[string]string)
	s.mu.Unlock()

	if reason == "" {
		reason = "agent stopped"
	}
	for _, p := range pending {
		if p.deadlineFn != nil {
			p.deadlineFn()
		}
		s.markContinuationResolved(context.Background(), p.id)
		// Best-effort: surface synthetic Cancelled resolutions on
		// the bus so any other agents holding references to the
		// awaited consult_ids see the cancellation. Uses the
		// board's amplifier directly since the in-flight peer
		// transport may have already been torn down.
		if s.board == nil {
			continue
		}
		amp := s.board.Amplifier()
		if amp == nil {
			continue
		}
		for id := range p.awaiting {
			amp.PublishConsultResolvedDelta(context.Background(), claims.ConsultResolvedDelta{
				ConsultID:         id,
				OriginatorAgentID: s.agentID,
				Status:            claims.ConsultStatusCancelled,
				ErrorMessage:      reason,
				EmittedAt:         time.Now().UTC(),
			})
		}
	}
}

// CancelContinuation cancels one specific continuation by ID. Used
// by tests and by future caller-driven cancellation surfaces (e.g.
// a user pressing Esc to abort an in-flight consult round). The
// awaiting consults receive synthetic Cancelled resolutions; the
// continuation claim's validation is marked failed; ResumeFn is
// NOT invoked.
func (s *ContinuationStore) CancelContinuation(continuationID, reason string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	p, ok := s.pending[continuationID]
	if !ok {
		s.mu.Unlock()
		return
	}
	delete(s.pending, continuationID)
	awaiting := make([]string, 0, len(p.awaiting))
	for id := range p.awaiting {
		delete(s.consultIndex, id)
		awaiting = append(awaiting, id)
	}
	if p.deadlineFn != nil {
		p.deadlineFn()
	}
	s.mu.Unlock()

	if reason == "" {
		reason = "continuation cancelled"
	}
	s.markContinuationResolved(context.Background(), continuationID)
	if s.board == nil {
		return
	}
	amp := s.board.Amplifier()
	if amp == nil {
		return
	}
	for _, id := range awaiting {
		amp.PublishConsultResolvedDelta(context.Background(), claims.ConsultResolvedDelta{
			ConsultID:         id,
			OriginatorAgentID: s.agentID,
			Status:            claims.ConsultStatusCancelled,
			ErrorMessage:      reason,
			EmittedAt:         time.Now().UTC(),
		})
	}
}

// RecoverPendingContinuations is called from each agent's Start to
// re-attach in-flight continuations after a process restart. Walks
// the session board for ConsultContinuation claims subject=this
// agent with pending validation, re-loads the TurnSnapshot from the
// claim's continuation_context artifact, and re-registers each in
// the pending map (with its consult_id index entries and deadline
// watcher). Already-resolved consults that landed before this
// recovery scan are matched against the board's existing testaments
// and processed inline — if all awaited consults are already done,
// resume dispatches immediately; otherwise the deadline watcher
// takes over.
//
// Returns the number of continuations recovered. Logs and continues
// on per-claim failures so a single bad snapshot doesn't strand the
// whole agent.
func (s *ContinuationStore) RecoverPendingContinuations(ctx context.Context) int {
	if s == nil || s.board == nil {
		return 0
	}
	proj := s.board.Projection()
	if proj == nil {
		return 0
	}
	recovered := 0
	for i := range proj.Claims {
		c := proj.Claims[i]
		if c.ActionType != claims.ActionTypeConsultContinuation {
			continue
		}
		if !claimSubjectIs(c, s.agentID) {
			continue
		}
		if !claimHasPendingReceipt(c) {
			// Validation already passed (resumed) or failed
			// (cancelled / abandoned) — don't reload.
			continue
		}
		snapshot, awaitedIDs, deadline, idleWindow, err := loadContinuationFromBoard(s.board, c.ID)
		if err != nil {
			slog.Warn("continuation_recovery_load_failed",
				"agent_id", s.agentID, "continuation_id", c.ID,
				"error", err.Error(),
			)
			continue
		}
		if snapshot == nil || len(awaitedIDs) == 0 {
			continue
		}
		if snapshot.Version > turnSnapshotCodecVersion {
			// Snapshot was written by a newer binary; refusing
			// to mis-decode is the contract. Mark the
			// continuation failed so it doesn't keep getting
			// recovered on every restart.
			slog.Warn("continuation_recovery_version_mismatch_skipping",
				"agent_id", s.agentID, "continuation_id", c.ID,
				"snapshot_version", snapshot.Version,
				"binary_version", turnSnapshotCodecVersion,
			)
			s.markContinuationResolved(ctx, c.ID)
			continue
		}

		s.mu.Lock()
		awaiting := make(map[string]struct{}, len(awaitedIDs))
		for _, id := range awaitedIDs {
			awaiting[id] = struct{}{}
		}
		pending := &pendingContinuation{
			id:         c.ID,
			snapshot:   snapshot,
			awaiting:   awaiting,
			resolved:   make(map[string]*claims.ConsultResolvedDelta),
			deadline:   deadline,
			idleWindow: idleWindow,
			activity:   s.recoveredConsultActivity(awaitedIDs, snapshot.YieldedAt),
		}
		s.pending[c.ID] = pending
		for id := range awaiting {
			s.consultIndex[id] = c.ID
		}
		s.mu.Unlock()
		s.startDeadlineWatcher(c.ID, idleWindow)
		recovered++
	}
	if recovered > 0 {
		slog.Info("continuation_recovery_complete",
			"agent_id", s.agentID, "session_id", s.sessionID,
			"recovered", recovered,
		)
	}
	return recovered
}

// claimSubjectIs reports whether claim has a Subject relation
// pointing to agentID. Used by restart recovery to filter
// ConsultContinuation claims to those owned by the recovering agent.
func claimSubjectIs(c claims.Claim, agentID string) bool {
	for _, r := range c.Relations {
		if r.Relationship == claims.RelationshipSubject &&
			r.RelatedType == claims.RelatedTypeAgent &&
			r.Related == agentID {
			return true
		}
	}
	return false
}

// claimHasPendingReceipt reports whether the claim has at least one
// ValidationTypeReceipt validation in pending state. Used to skip
// already-resumed (passed) or abandoned (failed) continuations
// during restart recovery.
func claimHasPendingReceipt(c claims.Claim) bool {
	for _, v := range c.Validations {
		if v == nil {
			continue
		}
		if v.Type == claims.ValidationTypeReceipt && v.Status == claims.ValidationStatusPending {
			return true
		}
	}
	return false
}

// loadContinuationFromBoard finds the testament submitted alongside
// the named ConsultContinuation claim, decodes the
// continuation_context artifact into a TurnSnapshot, and extracts
// the awaited consult IDs from continuation_await artifacts. Returns
// the snapshot, awaited IDs, deadline (latest of any await
// artifact's deadline metadata), idle timeout, and any decode error.
func loadContinuationFromBoard(board *claims.ClaimsBoard, continuationID string) (*TurnSnapshot, []string, time.Time, time.Duration, error) {
	if board == nil {
		return nil, nil, time.Time{}, 0, fmt.Errorf("nil board")
	}
	proj := board.Projection()
	if proj == nil {
		return nil, nil, time.Time{}, 0, fmt.Errorf("nil projection")
	}
	var snapshot *TurnSnapshot
	awaitedIDs := make([]string, 0, 4)
	var deadline time.Time
	var idleTimeout time.Duration
	for i := range proj.Testaments {
		t := proj.Testaments[i]
		if !testamentRelatesToClaim(t, continuationID) {
			continue
		}
		for _, art := range t.Artifacts {
			if art == nil {
				continue
			}
			switch art.Kind {
			case claims.ArtifactKindContinuationContext:
				var s TurnSnapshot
				if err := json.Unmarshal([]byte(art.Reference), &s); err != nil {
					return nil, nil, time.Time{}, 0, fmt.Errorf("decode continuation_context: %w", err)
				}
				snapshot = &s
			case claims.ArtifactKindContinuationAwait:
				awaitedIDs = append(awaitedIDs, art.Reference)
				if dlRaw, ok := art.Metadata["deadline"].(string); ok {
					if dl, err := time.Parse(time.RFC3339Nano, dlRaw); err == nil && dl.After(deadline) {
						deadline = dl
					}
				}
				if raw, ok := art.Metadata["idle_timeout_ms"]; ok {
					if ms := metadataInt64(raw); ms > 0 {
						d := time.Duration(ms) * time.Millisecond
						if d > idleTimeout {
							idleTimeout = d
						}
					}
				}
			}
		}
	}
	if snapshot == nil {
		return nil, nil, time.Time{}, 0, fmt.Errorf("no continuation_context artifact found")
	}
	if deadline.IsZero() {
		// Fall back to snapshot's own AwaitDeadline.
		deadline = snapshot.AwaitDeadline
	}
	if idleTimeout <= 0 {
		idleTimeout = awaitIdleTimeout(snapshot, deadline)
	}
	return snapshot, awaitedIDs, deadline, idleTimeout, nil
}

func awaitIdleTimeout(snapshot *TurnSnapshot, deadline time.Time) time.Duration {
	if snapshot != nil && !snapshot.YieldedAt.IsZero() && deadline.After(snapshot.YieldedAt) {
		return deadline.Sub(snapshot.YieldedAt)
	}
	if deadline.After(time.Now()) {
		return time.Until(deadline)
	}
	return 5 * time.Minute
}

func metadataInt64(value any) int64 {
	switch v := value.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	case string:
		var n int64
		if _, err := fmt.Sscan(v, &n); err == nil {
			return n
		}
	}
	return 0
}

// testamentRelatesToClaim reports whether t carries a RelationshipClaim
// pointing to claimID — i.e. it is the testament that holds claimID's
// continuation artifacts.
func testamentRelatesToClaim(t claims.Testament, claimID string) bool {
	for _, r := range t.Relations {
		if r.Relationship == claims.RelationshipClaim && r.Related == claimID {
			return true
		}
	}
	return false
}

// orphanResolutions buffers ConsultResolvedDeltas that arrived
// before the originating AwaitConsultsOrYield call had a chance to
// register the continuation. Cleaned up in
// takeOrphanResolutionLocked when claimed, or by a periodic GC pass
// (TODO) bounded by the max consult deadline.
//
// orphanResolutionsMaxAge bounds how long an orphan stays before
// expiry.
const orphanResolutionsMaxAge = 10 * time.Minute

func (s *ContinuationStore) stashOrphanResolutionLocked(delta *claims.ConsultResolvedDelta) {
	if s.orphans == nil {
		s.orphans = make(map[string]orphanedResolution)
	}
	s.orphans[delta.ConsultID] = orphanedResolution{
		delta:     delta,
		stashedAt: time.Now(),
	}
}

func (s *ContinuationStore) takeOrphanResolutionLocked(consultID string) *claims.ConsultResolvedDelta {
	if s.orphans == nil {
		return nil
	}
	o, ok := s.orphans[consultID]
	if !ok {
		return nil
	}
	delete(s.orphans, consultID)
	if time.Since(o.stashedAt) > orphanResolutionsMaxAge {
		// Stale orphan; drop silently. The deadline watcher on
		// the actual await will handle the timeout.
		return nil
	}
	return o.delta
}

type orphanedResolution struct {
	delta     *claims.ConsultResolvedDelta
	stashedAt time.Time
}
