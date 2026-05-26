package claims

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// ArtifactProgressSink observes artifacts as they accrete on a
// TestamentAccumulator during a claim's processing. The sink is the
// real-time projection from in-progress evidence to the UI: each tool
// call, each LLM dispatch start/end, each peer-consult artifact fires
// OnArtifactAdded the moment it is recorded — long before the
// accumulator flushes the final testament. Renderers read the sink to
// populate child rows under the parent claim row in the chat panel.
//
// Implementations MUST be non-blocking and panic-safe. The sink runs
// inline on the recording goroutine — slow or panicking sinks would
// stall the agent's tool/LLM hot path. The bridge implementation
// enqueues to a bounded channel and drains on a separate goroutine.
type ArtifactProgressSink interface {
	OnArtifactAdded(claimID, agentID, sessionID string, artifact *Artifact)
}

// AccumulatorLifecycleSink observes the open/close lifecycle of
// TestamentAccumulators across the process. The signal is the
// canonical "agent X is doing work" indicator: an open accumulator
// means the agent is in an in-progress claim handler, an interactive
// turn, or a background processing path. Closing fires after Flush
// has completed (or the accumulator finalized empty). The TUI agent
// panel uses this to drive its active/idle indicator without
// depending on stream-event heuristics — every processing path that
// uses an accumulator is observable, including remediation entries
// and consultation responses where the agent is not the subject of
// any claim.
//
// Implementations MUST be non-blocking and panic-safe — same hot-
// path constraints as ArtifactProgressSink.
type AccumulatorLifecycleSink interface {
	OnAccumulatorOpened(agentID, sessionID string)
	OnAccumulatorClosed(agentID, sessionID string)
}

// globalArtifactProgressSink is the process-wide sink. The TUI
// bootstrap sets this once at startup with a bridge implementation
// that fans events into a Bubble Tea program. Tests leave it nil
// (sink calls are no-ops). Atomic.Pointer keeps the read path
// lock-free on the agent's hot path.
var globalArtifactProgressSink atomic.Pointer[ArtifactProgressSink]

// globalAccumulatorLifecycleSink is the process-wide sink for
// open/close events. Same atomic.Pointer pattern as the artifact
// sink; nil-tolerant when no observer is registered.
var globalAccumulatorLifecycleSink atomic.Pointer[AccumulatorLifecycleSink]

// SetArtifactProgressSink registers the process-wide sink. Pass nil
// to clear (used by tests for isolation). Returns the previous sink
// so callers can restore it (defer-restore pattern in tests).
func SetArtifactProgressSink(sink ArtifactProgressSink) ArtifactProgressSink {
	prev := globalArtifactProgressSink.Load()
	if sink == nil {
		globalArtifactProgressSink.Store(nil)
	} else {
		globalArtifactProgressSink.Store(&sink)
	}
	if prev == nil {
		return nil
	}
	return *prev
}

func loadArtifactProgressSink() ArtifactProgressSink {
	p := globalArtifactProgressSink.Load()
	if p == nil {
		return nil
	}
	return *p
}

// SetAccumulatorLifecycleSink registers the process-wide accumulator
// lifecycle sink. Same defer-restore pattern as SetArtifactProgressSink.
func SetAccumulatorLifecycleSink(sink AccumulatorLifecycleSink) AccumulatorLifecycleSink {
	prev := globalAccumulatorLifecycleSink.Load()
	if sink == nil {
		globalAccumulatorLifecycleSink.Store(nil)
	} else {
		globalAccumulatorLifecycleSink.Store(&sink)
	}
	if prev == nil {
		return nil
	}
	return *prev
}

func loadAccumulatorLifecycleSink() AccumulatorLifecycleSink {
	p := globalAccumulatorLifecycleSink.Load()
	if p == nil {
		return nil
	}
	return *p
}

// TestamentAccumulator collects observations within any bounded
// lifecycle — a request, a response relay, a planning protocol, a
// session — and flushes them as a single composite testament when the
// lifecycle completes. This avoids flooding the board with per-event
// testaments while preserving full audit trail fidelity.
//
// The lifecycle boundary is defined by the caller, not the type.
// A request handler creates and flushes one accumulator per request.
// A response handler creates one per response relay. A session manager
// creates one per session. Each produces one testament with all
// accumulated artifacts.
//
// Usage:
//
//	acc := claims.NewTestamentAccumulator("librarian", sessionID)
//	ctx = claims.WithTestamentAccumulator(ctx, acc)
//	defer acc.Flush(ctx, board, scope)
//
//	// Later, anywhere in the call chain:
//	if acc := claims.AccumulatorFromContext(ctx); acc != nil {
//	    acc.Record("search_result", "grep foo → 12 files")
//	}
type TestamentAccumulator struct {
	mu        sync.Mutex
	id        string // synthetic stable ID across the request, used as the in-flight TestamentContextDelta anchor before testament submission. Set on construction.
	agentID   string
	sessionID string
	claimID   string // parent claim being processed; threaded into ArtifactProgressSink callbacks
	artifacts []*Artifact
	notes     []string
	started   time.Time
	flushed   bool

	// context is the testament's developing-conclusion narrative,
	// updatable via SetContext while the testament is still in
	// flight. Sealed onto Testament.Context on Flush. Stored as
	// pointer so atomic loads see consistent values without a wider
	// lock; mutation goes through SetContext under accumulator mu.
	context           string
	contextTransition int64

	// sink overrides the global ArtifactProgressSink for this
	// accumulator. Nil falls back to the process-wide sink. Tests
	// use this for isolation.
	sink ArtifactProgressSink

	// board lets SetContext publish TestamentContextDeltas through
	// the amplifier mid-flight. Optional — when nil, SetContext only
	// updates the in-memory value (no UI delta until Flush). Set via
	// WithBoard during accumulator construction in agents that wire
	// it.
	board *ClaimsBoard
}

// NewTestamentAccumulator creates an accumulator for a request lifecycle.
// Fires the global AccumulatorLifecycleSink's OnAccumulatorOpened so
// observers (TUI agent panel, telemetry) see the start of every
// agent processing path the moment the accumulator is constructed —
// not later when the first artifact gets recorded. The matching
// OnAccumulatorClosed fires from Flush.
func NewTestamentAccumulator(agentID, sessionID string) *TestamentAccumulator {
	acc := &TestamentAccumulator{
		id:        uuid.NewString(),
		agentID:   agentID,
		sessionID: sessionID,
		started:   time.Now().UTC(),
	}
	if sink := loadAccumulatorLifecycleSink(); sink != nil {
		fireOnAccumulatorOpened(sink, agentID, sessionID)
	}
	return acc
}

// ID returns the accumulator's synthetic stable ID, used as the
// anchor for in-flight TestamentContextDeltas before the testament
// has been submitted to the board. UI keys the in-flight testament
// row by this ID and rebinds to the real TestamentID on Flush.
func (a *TestamentAccumulator) ID() string {
	if a == nil {
		return ""
	}
	return a.id
}

// WithBoard wires the accumulator to a board for mid-flight
// TestamentContextDelta emission. SetContext publishes through the
// board's amplifier when set; it's a no-op delta when not. Returns
// the receiver for chaining.
func (a *TestamentAccumulator) WithBoard(board *ClaimsBoard) *TestamentAccumulator {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	a.board = board
	a.mu.Unlock()
	return a
}

// Board returns the board the accumulator was wired to via WithBoard,
// or nil if no board is wired. Used by callers (e.g. shared tool
// timing) that want to push a ClaimContextDelta on the same board the
// accumulator is anchored to without re-resolving via the session
// registry.
func (a *TestamentAccumulator) Board() *ClaimsBoard {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.board
}

// SuppressFlush marks this accumulator as "do not submit on Flush."
// The agent's tool dispatch loop calls this when a consult / challenge
// / guardian wait yields the LLM turn — the snapshot captured at
// yield-time is the authoritative state for resume to restore, and
// the original accumulator must not produce its own testament. Any
// subsequent Flush() call (typically the agent's deferred Flush in
// processClaimsEntry) becomes a no-op.
//
// Without this guard, processClaimsEntry's deferred Flush submits a
// premature testament with the partial pre-yield state, the board
// sees the agent testify mid-cycle, the cycle resolver closes the
// cycle, and downstream artifacts emitted by peers responding to the
// (still in-flight) consult fail to nest under the issuer's
// consult_started row. See docs/CLAIMS_UI.md.
func (a *TestamentAccumulator) SuppressFlush() {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.flushed = true
	a.mu.Unlock()
}

// SetContext updates the testament's developing-conclusion narrative.
// Replaces the prior value in place; increments the monotonic
// ContextTransition counter; emits a TestamentContextDelta on the
// board's amplifier (if wired) so the UI can update the in-flight
// testament row's status text without creating a new row.
//
// The delta is keyed by AccumulatorID before flush — the testament
// has no board ID yet — so the UI tracks an in-flight row keyed by
// the accumulator's synthetic ID. On Flush, a final delta with the
// real TestamentID + same AccumulatorID lets the UI rebind the row.
//
// Safe to call from any goroutine. Drops silently if the accumulator
// is already flushed (post-flush mutations should go through
// board.SetTestamentContext on the durable testament).
func (a *TestamentAccumulator) SetContext(ctx context.Context, value string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	if a.flushed {
		a.mu.Unlock()
		return
	}
	a.context = value
	a.contextTransition++
	transition := a.contextTransition
	board := a.board
	agentID := a.agentID
	sessionID := a.sessionID
	claimID := a.claimID
	accID := a.id
	a.mu.Unlock()

	if board == nil {
		return
	}
	if amp := board.Amplifier(); amp != nil {
		amp.PublishTestamentContextDelta(ctx, TestamentContextDelta{
			SessionID:     sessionID,
			BoardID:       board.BoardID(),
			AccumulatorID: accID,
			Sequence:      board.seq.Load(),
			EmittedAt:     time.Now().UTC(),
			TransitionID:  transition,
			Context:       value,
			AgentID:       agentID,
			ClaimID:       claimID,
		})
	}
	// Also fire an in-process BoardMutationDelta so the UI bridge
	// (subscribed via board.SubscribeDelta) sees in-flight context
	// updates. Without this the bridge only learned about post-flush
	// context (via SetTestamentContext); accumulator-time updates
	// were silently dropped from the UI plane. AccumulatorID is the
	// in-flight row anchor; TestamentID is empty until flush.
	board.notifyDelta(BoardMutationDelta{
		Kind:              "testament_context_changed",
		ClaimID:           claimID,
		AccumulatorID:     accID,
		AgentID:           agentID,
		Context:           value,
		ContextTransition: transition,
	})
}

// fireOnAccumulatorOpened invokes the lifecycle sink's
// OnAccumulatorOpened with panic recovery — same hot-path
// constraints as the artifact sink. A misbehaving observer must not
// stall accumulator construction, which sits on the entry boundary
// of every agent processing path.
func fireOnAccumulatorOpened(sink AccumulatorLifecycleSink, agentID, sessionID string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("accumulator_lifecycle_sink_open_panic",
				"agent_id", agentID,
				"session_id", sessionID,
				"panic", r,
			)
		}
	}()
	sink.OnAccumulatorOpened(agentID, sessionID)
}

// fireOnAccumulatorClosed invokes the lifecycle sink's
// OnAccumulatorClosed with panic recovery. Symmetric to
// fireOnAccumulatorOpened.
func fireOnAccumulatorClosed(sink AccumulatorLifecycleSink, agentID, sessionID string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("accumulator_lifecycle_sink_close_panic",
				"agent_id", agentID,
				"session_id", sessionID,
				"panic", r,
			)
		}
	}()
	sink.OnAccumulatorClosed(agentID, sessionID)
}

// WithClaimID attaches the parent claim ID. The accumulator threads
// this into ArtifactProgressSink callbacks so the UI can route
// artifact events to the correct chat row.
func (a *TestamentAccumulator) WithClaimID(claimID string) *TestamentAccumulator {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	a.claimID = strings.TrimSpace(claimID)
	a.mu.Unlock()
	return a
}

// WithSink overrides the process-wide ArtifactProgressSink for this
// accumulator. Used by tests for isolation; production code leaves
// this unset and the global sink applies.
func (a *TestamentAccumulator) WithSink(sink ArtifactProgressSink) *TestamentAccumulator {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	a.sink = sink
	a.mu.Unlock()
	return a
}

// ClaimID returns the parent claim ID (empty when unset).
func (a *TestamentAccumulator) ClaimID() string {
	if a == nil {
		return ""
	}
	a.mu.Lock()
	id := a.claimID
	a.mu.Unlock()
	return id
}

// AgentID returns the accumulator's agent ID.
func (a *TestamentAccumulator) AgentID() string {
	if a == nil {
		return ""
	}
	return a.agentID
}

// SessionID returns the accumulator's session ID.
func (a *TestamentAccumulator) SessionID() string {
	if a == nil {
		return ""
	}
	return a.sessionID
}

// Record appends a single artifact observation. Thread-safe.
// No board interaction — artifacts are buffered until Flush. Fires
// the ArtifactProgressSink so observers see the artifact in real time.
func (a *TestamentAccumulator) Record(kind, reference string) {
	if a == nil {
		return
	}
	a.RecordArtifact(&Artifact{
		AgentID:   a.agentID,
		SessionID: a.sessionID,
		Kind:      kind,
		Reference: reference,
	})
}

// ArtifactKindResponseText is the canonical kind for the final
// assistant message produced by an agent's tool loop. When present,
// Flush promotes its Reference to the testament's Summary so board
// readers (TUI, archivalist, audit) see the agent's actual answer
// instead of a join of accumulator notes. Recorded by ExecuteTurnLoop
// so every agent benefits without per-site wiring.
const ArtifactKindResponseText = "response_text"

// DefaultResponseTextPresentation returns the migration-default
// presentation contract for final assistant text. Response text remains
// ordinary evidence; this only describes the default user-facing route.
func DefaultResponseTextPresentation() *Presentation {
	return &Presentation{
		Audiences: []PresentationAudience{PresentationAudienceUser},
		Surfaces:  []PresentationSurface{PresentationSurfaceChat},
		Format:    PresentationFormatMarkdown,
		Placement: PresentationPlacementAfterResponse,
	}
}

// ApplyDefaultArtifactPresentation attaches kind-specific presentation
// defaults without overriding an explicit contract supplied by the caller.
func ApplyDefaultArtifactPresentation(artifact *Artifact) {
	if artifact == nil || artifact.Presentation != nil {
		return
	}
	switch strings.TrimSpace(artifact.Kind) {
	case ArtifactKindResponseText:
		artifact.Presentation = DefaultResponseTextPresentation()
	}
}

// responseTextSummaryMax bounds the size of the testament Summary
// derived from a response_text artifact. The full text remains in
// the artifact's Reference; only the headline is capped. Set off the
// claim Summary's expected display width × a generous multiplier so
// the limit derives from how the UI actually renders summaries
// rather than a hand-picked byte count.
const responseTextSummaryMax = 4096

// RecordResponseText records the agent's final assistant message as
// a response_text artifact. Idempotent in the common case: a turn
// loop produces one final response, so this is called once per
// flush. If called multiple times the most recent recording wins
// for Summary derivation (Flush picks the last response_text).
//
// Empty / whitespace-only content is ignored so callers can record
// unconditionally after their tool loop returns without checking
// for the empty-error case.
func (a *TestamentAccumulator) RecordResponseText(content string) {
	if a == nil {
		return
	}
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return
	}
	a.RecordArtifact(&Artifact{
		AgentID:      a.agentID,
		SessionID:    a.sessionID,
		Kind:         ArtifactKindResponseText,
		Reference:    trimmed,
		Presentation: DefaultResponseTextPresentation(),
	})
}

// RecordArtifact appends a fully-formed artifact. The accumulator
// stamps AgentID/SessionID if unset, sets Created if zero, and fires
// the ArtifactProgressSink so observers see the artifact accrue in
// real time. Thread-safe. No board interaction.
//
// Use this for evidence with structured Metadata (tool calls, LLM
// dispatches, peer consultations) where Record's plain (kind,
// reference) signature is insufficient.
func (a *TestamentAccumulator) RecordArtifact(artifact *Artifact) {
	if a == nil || artifact == nil {
		return
	}
	a.mu.Lock()
	if artifact.AgentID == "" {
		artifact.AgentID = a.agentID
	}
	if artifact.SessionID == "" {
		artifact.SessionID = a.sessionID
	}
	if artifact.Created.IsZero() {
		artifact.Created = time.Now().UTC()
	}
	if artifact.Accessed.IsZero() {
		artifact.Accessed = artifact.Created
	}
	ApplyDefaultArtifactPresentation(artifact)
	artifact.Presentation = NormalizePresentation(artifact.Presentation)
	a.artifacts = append(a.artifacts, artifact)
	claimID := a.claimID
	agentID := a.agentID
	sessionID := a.sessionID
	sink := a.sink
	a.mu.Unlock()

	if sink == nil {
		sink = loadArtifactProgressSink()
	}
	if sink != nil {
		// Sink runs inline on the recorder's goroutine — implementations
		// MUST be non-blocking. Recover from panics so a misbehaving
		// observer cannot corrupt the agent's hot path.
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("artifact_progress_sink_panic",
						"agent_id", agentID,
						"claim_id", claimID,
						"artifact_kind", artifact.Kind,
						"panic", r,
					)
				}
			}()
			sink.OnArtifactAdded(claimID, agentID, sessionID, artifact)
		}()
	}
}

// RecordJSON appends an artifact with a JSON-serialized reference.
func (a *TestamentAccumulator) RecordJSON(kind string, value any) {
	if a == nil {
		return
	}
	ref, err := json.Marshal(value)
	if err != nil {
		ref = []byte(`{"error":"` + strings.ReplaceAll(err.Error(), `"`, `\"`) + `"}`)
	}
	a.Record(kind, string(ref))
}

// Note appends a human-readable summary line. Notes are joined
// into the testament's Summary field at flush time.
func (a *TestamentAccumulator) Note(summary string) {
	if a == nil || strings.TrimSpace(summary) == "" {
		return
	}
	a.mu.Lock()
	a.notes = append(a.notes, strings.TrimSpace(summary))
	a.mu.Unlock()
}

// Len returns the number of accumulated artifacts.
func (a *TestamentAccumulator) Len() int {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	n := len(a.artifacts)
	a.mu.Unlock()
	return n
}

// Artifacts returns a snapshot copy of the accumulated artifacts in
// insertion order. The returned slice is safe for the caller to mutate
// — internal state is not exposed. Intended for tests + observability;
// production code paths use the ArtifactProgressSink instead.
func (a *TestamentAccumulator) Artifacts() []*Artifact {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]*Artifact, len(a.artifacts))
	copy(out, a.artifacts)
	return out
}

// Notes returns a snapshot copy of accumulated notes in insertion
// order. Used by ConsultContinuation snapshotting so a yielded turn
// can resume with its prior notes intact.
func (a *TestamentAccumulator) Notes() []string {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, len(a.notes))
	copy(out, a.notes)
	return out
}

// Started returns the wall-clock time the accumulator was opened.
// Used by ConsultContinuation snapshotting so the resumed accumulator
// preserves its original lifecycle start (duration metrics measured
// from the original open, not the resume time).
func (a *TestamentAccumulator) Started() time.Time {
	if a == nil {
		return time.Time{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.started
}

// RestoreAccumulator reconstructs a TestamentAccumulator from a
// previous Artifacts/Notes/Started snapshot. Used by
// ConsultContinuation resume to re-attach a yielded turn's
// accumulator context. Does not fire OnAccumulatorOpened — the open
// already fired on the original turn; resume is a continuation, not
// a new lifecycle.
func RestoreAccumulator(agentID, sessionID, claimID string, started time.Time, artifacts []*Artifact, notes []string) *TestamentAccumulator {
	dupArtifacts := make([]*Artifact, len(artifacts))
	copy(dupArtifacts, artifacts)
	dupNotes := make([]string, len(notes))
	copy(dupNotes, notes)
	return &TestamentAccumulator{
		agentID:   agentID,
		sessionID: sessionID,
		claimID:   claimID,
		artifacts: dupArtifacts,
		notes:     dupNotes,
		started:   started,
	}
}

type accumulatorFlushPayload struct {
	agentID           string
	sessionID         string
	claimID           string
	contextValue      string
	contextTransition int64
	accID             string
	started           time.Time
	artifacts         []*Artifact
	notes             []string
}

func (a *TestamentAccumulator) beginFlush() *accumulatorFlushPayload {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	if a.flushed {
		a.mu.Unlock()
		return nil
	}
	a.flushed = true
	artifacts := make([]*Artifact, len(a.artifacts))
	copy(artifacts, a.artifacts)
	notes := make([]string, len(a.notes))
	copy(notes, a.notes)
	payload := &accumulatorFlushPayload{
		agentID:           a.agentID,
		sessionID:         a.sessionID,
		claimID:           a.claimID,
		contextValue:      a.context,
		contextTransition: a.contextTransition,
		accID:             a.id,
		started:           a.started,
		artifacts:         artifacts,
		notes:             notes,
	}
	a.mu.Unlock()
	return payload
}

func (p *accumulatorFlushPayload) closeLifecycle() {
	if p == nil {
		return
	}
	if sink := loadAccumulatorLifecycleSink(); sink != nil {
		fireOnAccumulatorClosed(sink, p.agentID, p.sessionID)
	}
}

func submitAccumulatorFlush(ctx context.Context, board *ClaimsBoard, p *accumulatorFlushPayload) error {
	if board == nil || p == nil || len(p.artifacts) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	artifacts := make([]*Artifact, 0, len(p.artifacts)+1)
	artifacts = append(artifacts, p.artifacts...)
	artifacts = append(artifacts, &Artifact{
		AgentID:   p.agentID,
		SessionID: p.sessionID,
		Kind:      "request_duration_ms",
		Reference: fmt.Sprintf("%d", time.Since(p.started).Milliseconds()),
	})

	summary := deriveTestamentSummary(artifacts, p.notes)
	// claimID is informational on the accumulator (used by the
	// artifact-progress sink for chat-row routing); intentionally
	// not added as a Relation here — see Flush doc comment.
	claimID := p.claimID

	// Pre-stamp the testament ID so the post-submit final
	// TestamentContextDelta can carry it. The board's
	// stampTestamentLocked preserves a non-empty ID.
	testamentID := uuid.NewString()
	testament := Testament{
		ID:         testamentID,
		AgentID:    p.agentID,
		SessionID:  p.sessionID,
		Summary:    summary,
		Confidence: "committed",
		// Seal the accumulator's developing-conclusion narrative onto
		// the durable Testament. After submission the value is
		// queryable via the board projection; mid-flight updates
		// arrived as TestamentContextDeltas keyed by AccumulatorID.
		Context:           p.contextValue,
		ContextTransition: p.contextTransition,
		Relations: []Relation{
			{Related: p.agentID, RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
		},
		Artifacts: artifacts,
	}
	action := Action{AgentID: p.agentID, Type: ActionTypeTestament}
	if err := board.SubmitTestaments(ctx, action, []Testament{testament}); err != nil {
		return err
	}
	// Final TestamentContextDelta carrying BOTH AccumulatorID and
	// the now-real TestamentID. The UI rebinds its in-flight
	// testament row from the synthetic accumulator anchor to the
	// durable testament ID. Skipped when no Context was ever
	// recorded — saves an empty delta on the firehose.
	if amp := board.Amplifier(); amp != nil && (p.contextValue != "" || p.contextTransition > 0) {
		amp.PublishTestamentContextDelta(ctx, TestamentContextDelta{
			SessionID:     p.sessionID,
			BoardID:       board.BoardID(),
			TestamentID:   testamentID,
			AccumulatorID: p.accID,
			Sequence:      board.seq.Load(),
			EmittedAt:     time.Now().UTC(),
			TransitionID:  p.contextTransition,
			Context:       p.contextValue,
			AgentID:       p.agentID,
			ClaimID:       claimID,
		})
	}
	return nil
}

// Flush submits the accumulated artifacts as a single composite
// testament to the board. No-op if empty or already flushed.
// Best-effort — board unavailability does not fail the caller.
//
// If scope is non-nil, the submission is dispatched async.
//
// Summary derivation prefers the most recent response_text artifact
// (the final assistant message produced by the agent's tool loop)
// truncated to responseTextSummaryMax. Falls back to joined notes
// when no response_text is present, then to a generic count when
// notes are empty too. The full response always remains in the
// artifact stream regardless of truncation.
//
// Relations carry Issuer = agent. The accumulator does NOT
// automatically add a RelationshipClaim relation even when a
// claimID is set on it: doing so would cause every accumulator
// flush (including routine forwarded-request bookkeeping) to
// publish a TestamentDelta carrying the claim ID, which delivers
// to all RoleAuditor subscribers and fires their full
// LLM-evaluation tool loop on every routine testament. That
// auto-link was a real audit-loop amplifier in practice. Agents
// that explicitly want to tie a testament to an originating claim
// (consultation responses, evaluation outcomes, etc.) call
// SubmitTestamentsSkill directly with explicit Relations — the
// claim-board emission path that has its own per-call routing
// semantics.
func (a *TestamentAccumulator) Flush(ctx context.Context, board *ClaimsBoard, scope ScopeProvider) {
	payload := a.beginFlush()
	if payload == nil {
		return
	}

	// Lifecycle close fires regardless of whether the flush ultimately
	// commits — the agent's processing path is done either way and
	// the activity indicator must release.
	defer payload.closeLifecycle()

	if board == nil || len(payload.artifacts) == 0 {
		return
	}

	if scope == nil {
		slog.Error("accumulator_flush_scope_unwired",
			"agent", payload.agentID, "artifacts", len(payload.artifacts)+1,
			"reason", "scope required for tracked async dispatch; flush dropped")
		board.RecordNotificationError(fmt.Sprintf(
			"accumulator flush dropped: scope unwired (agent %s, %d artifacts)",
			payload.agentID, len(payload.artifacts)+1))
		return
	}
	if err := scope.Go("accumulator_flush", 5*time.Second, func(gctx context.Context) error {
		return submitAccumulatorFlush(gctx, board, payload)
	}); err != nil {
		slog.Error("accumulator_flush_dispatch_failed",
			"agent", payload.agentID, "artifacts", len(payload.artifacts)+1, "error", err.Error())
		board.RecordNotificationError("accumulator flush dispatch: " + err.Error())
	}
}

// FlushBlocking submits the accumulated artifacts synchronously before
// returning. Forwarded-request handlers use this before publishing
// stream completion or route responses so the board/UI sees all tool
// and child-agent evidence before the user-facing final text.
func (a *TestamentAccumulator) FlushBlocking(ctx context.Context, board *ClaimsBoard) error {
	payload := a.beginFlush()
	if payload == nil {
		return nil
	}
	defer payload.closeLifecycle()
	if board == nil || len(payload.artifacts) == 0 {
		return nil
	}
	if err := submitAccumulatorFlush(ctx, board, payload); err != nil {
		slog.Error("accumulator_flush_blocking_failed",
			"agent", payload.agentID,
			"artifacts", len(payload.artifacts)+1,
			"error", err.Error(),
		)
		board.RecordNotificationError("accumulator flush blocking: " + err.Error())
		return err
	}
	return nil
}

// deriveTestamentSummary picks the testament Summary from the
// accumulated artifacts and notes. Preference order:
//
//  1. Most recent response_text artifact (truncated to
//     responseTextSummaryMax) — the agent's final assistant message,
//     which is the headline a board reader actually wants.
//  2. Joined notes — the legacy fallback for accumulators that
//     never received a final response_text (e.g. error paths that
//     flush before the tool loop returns).
//  3. Generic artifact count — last-resort placeholder so the
//     testament always has a non-empty Summary.
//
// Walks artifacts in reverse so the latest response_text wins when
// a turn loop emitted multiple final messages (e.g. across steering
// re-entries). Bounded scan: artifact slice is per-request.
func deriveTestamentSummary(artifacts []*Artifact, notes []string) string {
	for i := len(artifacts) - 1; i >= 0; i-- {
		art := artifacts[i]
		if art == nil || art.Kind != ArtifactKindResponseText {
			continue
		}
		ref := strings.TrimSpace(art.Reference)
		if ref == "" {
			continue
		}
		if len(ref) > responseTextSummaryMax {
			ref = ref[:responseTextSummaryMax] + "…"
		}
		return ref
	}
	if len(notes) > 0 {
		return strings.Join(notes, "; ")
	}
	return fmt.Sprintf("Request observations: %d artifacts", len(artifacts))
}

// ── Context key pattern ──

type testamentAccumulatorKey struct{}
type parentClaimIDKey struct{}

// WithTestamentAccumulator stores an accumulator in the context. If
// the accumulator has no ClaimID set and the context carries one
// (via WithParentClaimID), the accumulator inherits it — so artifacts
// recorded on the accumulator are attributed to the right claim row
// in the chat panel without each call site having to plumb the ID.
func WithTestamentAccumulator(ctx context.Context, acc *TestamentAccumulator) context.Context {
	if acc != nil && acc.ClaimID() == "" {
		if id := ParentClaimIDFromContext(ctx); id != "" {
			acc.WithClaimID(id)
		}
	}
	return context.WithValue(ctx, testamentAccumulatorKey{}, acc)
}

// AccumulatorFromContext retrieves the accumulator, or nil if none.
func AccumulatorFromContext(ctx context.Context) *TestamentAccumulator {
	acc, _ := ctx.Value(testamentAccumulatorKey{}).(*TestamentAccumulator)
	return acc
}

// WithParentClaimID stamps the claim being processed on ctx so any
// TestamentAccumulator subsequently created with this ctx
// automatically inherits the claim ID. The wiring layer
// (shared.WireClaimsIntake) sets this when dispatching a graph entry
// to the agent, so every artifact recorded during processing carries
// the right claim attribution.
func WithParentClaimID(ctx context.Context, claimID string) context.Context {
	claimID = strings.TrimSpace(claimID)
	if claimID == "" {
		return ctx
	}
	return context.WithValue(ctx, parentClaimIDKey{}, claimID)
}

// ParentClaimIDFromContext retrieves the parent claim ID, or empty
// string if none was stamped.
func ParentClaimIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(parentClaimIDKey{}).(string)
	return id
}
