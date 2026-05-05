package shared

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
)

// ToolCallPhase distinguishes start from completion events.
type ToolCallPhase int

const (
	// ToolCallStart is emitted when a tool call begins execution.
	ToolCallStart ToolCallPhase = 0

	// ToolCallComplete is emitted when a tool call finishes (success or failure).
	ToolCallComplete ToolCallPhase = 1
)

// ErrMissingToolCallID reports an invariant violation: a tool-call construction
// or emission site reached shared code without a non-empty lifecycle ID.
// Provider adapters (see providers.EnsureToolCallID) are expected to stamp an
// ID on every ToolCall they surface; sibling emission sites are expected to
// carry that ID through. Returning this error rather than panicking lets
// callers log with full agent/correlation context and keep the turn alive.
var ErrMissingToolCallID = errors.New("tool call missing required lifecycle ID")

// maxArgsSummaryLen is the maximum character length for a compact args summary.
const maxArgsSummaryLen = 60

// maxOutputBytes is the default truncation limit for tool output.
const maxOutputBytes = 512

// priorityArgKeys are extracted first for compact summaries, in order.
var priorityArgKeys = [...]string{
	"path", "file_path", "pattern", "query", "command", "script", "url",
	"name", "content", "message",
}

// ToolCallEvent carries timing and metadata for a single tool invocation.
type ToolCallEvent struct {
	ToolCallKey string               `json:"tool_call_key,omitempty"`
	ToolName    string               `json:"tool_name"`
	ArgsSummary string               `json:"args_summary"`
	FullArgs    string               `json:"full_args"`
	Output      string               `json:"output"`
	ErrorMsg    string               `json:"error_msg"`
	AgentID     string               `json:"agent_id"`
	Phase       ToolCallPhase        `json:"phase"`
	StartedAt   time.Time            `json:"started_at"`
	Duration    time.Duration        `json:"duration"`
	Success     bool                 `json:"success"`
	InterAgent  *InterAgentToolEvent `json:"inter_agent,omitempty"`
	// StreamMetadata preserves routing identity for mirrored UI streams.
	StreamMetadata map[string]any `json:"stream_metadata,omitempty"`
}

// ToolCallEmitter is a callback that publishes a tool call event to the bus.
// Returns the underlying publish error so EmitToolCall can surface it up to
// the agent's tool loop, which decides whether to abort the in-flight tool,
// emit a system error to the user, or continue. Emitters that have no
// destination (test stubs, no-op wirings) return nil.
type ToolCallEmitter func(ToolCallEvent) error

type toolCallEmitterKey struct{}
type toolCallTrackerKey struct{}
type activeToolCallContextKey struct{}

// ActiveToolCallContext carries the currently executing tool call so nested
// route requests can attach child streams back to the originating branch.
type ActiveToolCallContext struct {
	ToolCallKey string
	ToolName    string
	FullArgs    string
	InterAgent  *InterAgentToolEvent
}

type streamedToolCallState struct {
	ID        string
	Name      string
	Kind      providers.ToolKind
	Arguments strings.Builder
	StartedAt time.Time
	Announced bool
}

// ToolCallLifecycle is the single authoritative per-tool-call state record
// inside a toolCallTracker. It replaces five earlier overlapping maps
// (pending, preannounced, completed, inFlight, sessions) with one struct
// per call.ID. All five lifecycle phases — streaming accumulation,
// preannouncement, start/complete emission, completion flag, and in-flight
// telemetry — update fields on the same record, which eliminates the
// bookkeeping gaps that produced the "Start says emitted, map X says not"
// races the prior shape accumulated.
//
// Read/write synchronization is owned by the parent tracker's mutex; the
// lifecycle itself is a plain struct. The `session` handle exposes the
// emission idempotency API (Start / Complete / CompleteAt) externally and
// holds its own mutex for caller-facing concurrency.
type ToolCallLifecycle struct {
	// Streaming accumulation — populated by observeChunk as chunks arrive.
	// nil when the lifecycle was acquired directly (non-streaming path).
	stream *streamedToolCallState

	// Emission session — lazily created when a caller acquires a session for
	// this lifecycle. Once created, Start and Complete emissions route
	// through it idempotently. nil while the lifecycle only has streaming
	// state with no session yet.
	session *ToolCallSession

	// preannouncedAt records the timestamp a streaming preannounce captured
	// before TimedToolCall took ownership. Zero when no preannounce has
	// fired for this call. Consumed (cleared) by consumePreannounced when
	// the tool loop picks the call up.
	preannouncedAt time.Time

	// completed is set when a terminal (Complete / failure) emission has
	// fired for this lifecycle. Retained after session release so
	// late-arriving duplicate-complete paths (notably CompleteProviderNativeToolCall)
	// can short-circuit instead of re-emitting.
	completed bool

	// inFlight carries the telemetry snapshot populated at Start emission
	// time and cleared at Complete. Used by snapshotInFlightToolCalls to
	// emit the tool_call_in_flight_at_stream_terminal log entries. Zero
	// value means the lifecycle has no outstanding Start emission.
	inFlight inFlightToolCall
}

type toolCallTracker struct {
	mu sync.Mutex
	// lifecycles is the single map keyed by call.ID that holds every
	// tool-call record the tracker knows about. All five former maps
	// (pending, preannounced, completed, inFlight, sessions) were
	// consolidated into fields on *ToolCallLifecycle so every update to a
	// given call sees the same record atomically under t.mu.
	lifecycles map[string]*ToolCallLifecycle
}

// inFlightToolCall is a snapshot of a tool call that has emitted Start but
// not yet emitted Complete. ToolCallKey is the canonical lifecycle ID.
type inFlightToolCall struct {
	ToolCallKey string
	ToolName    string
	ArgsSummary string
	StartedAt   time.Time
}

func newToolCallTracker() *toolCallTracker {
	return &toolCallTracker{
		lifecycles: make(map[string]*ToolCallLifecycle),
	}
}

// ensureLifecycleLocked returns the lifecycle record for id, creating an
// empty one if needed. Caller must hold t.mu.
func (t *toolCallTracker) ensureLifecycleLocked(id string) *ToolCallLifecycle {
	if lc, ok := t.lifecycles[id]; ok {
		return lc
	}
	lc := &ToolCallLifecycle{}
	t.lifecycles[id] = lc
	return lc
}

// recordInFlight registers a Start emission on the lifecycle for
// event.ToolCallKey. Called from EmitToolCall after the publish succeeds (or
// would succeed if a publisher exists), regardless of whether the bus
// actually has subscribers.
func (t *toolCallTracker) recordInFlight(event ToolCallEvent) {
	if t == nil || strings.TrimSpace(event.ToolCallKey) == "" {
		return
	}
	t.mu.Lock()
	lc := t.ensureLifecycleLocked(event.ToolCallKey)
	lc.inFlight = inFlightToolCall{
		ToolCallKey: event.ToolCallKey,
		ToolName:    event.ToolName,
		ArgsSummary: event.ArgsSummary,
		StartedAt:   event.StartedAt,
	}
	t.mu.Unlock()
}

// clearInFlight drops the in-flight telemetry on the matching lifecycle.
// Called from EmitToolCall after the Complete publish.
func (t *toolCallTracker) clearInFlight(event ToolCallEvent) {
	if t == nil || strings.TrimSpace(event.ToolCallKey) == "" {
		return
	}
	t.mu.Lock()
	if lc, ok := t.lifecycles[event.ToolCallKey]; ok {
		lc.inFlight = inFlightToolCall{}
	}
	t.mu.Unlock()
}

// snapshotInFlightToolCalls returns a stable copy of the currently in-flight
// tool calls. Safe to call from any goroutine. The slice is sorted by
// StartedAt ascending so the oldest (most-likely-stuck) calls appear first.
func (t *toolCallTracker) snapshotInFlightToolCalls() []inFlightToolCall {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]inFlightToolCall, 0, len(t.lifecycles))
	for _, lc := range t.lifecycles {
		if strings.TrimSpace(lc.inFlight.ToolCallKey) == "" {
			continue
		}
		out = append(out, lc.inFlight)
	}
	if len(out) == 0 {
		return nil
	}
	// Insertion sort by StartedAt — n is small (single-digit typical).
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].StartedAt.Before(out[j-1].StartedAt); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// LogInFlightToolCallsAtTerminal logs every tool call whose Start has been
// emitted but whose Complete has not, at the moment the agent's stream
// finalizes. Each entry becomes a greppable event:
//
//	tool_call_in_flight_at_stream_terminal tool_name=summarize_workspace_state age_ms=145320 ...
//
// The (A) lifecycle bypass means a Complete may still arrive after this log
// fires — the entry is informational ("this call was outstanding when the
// stream ended"), not an assertion that the call is permanently lost.
// Combined with the late-bypass telemetry from (A), every "stuck row"
// regression can be reconstructed from logs without a screenshot.
func LogInFlightToolCallsAtTerminal(ctx context.Context, reason string) {
	tracker := toolCallTrackerFromContext(ctx)
	if tracker == nil {
		return
	}
	calls := tracker.snapshotInFlightToolCalls()
	if len(calls) == 0 {
		return
	}
	lm := LogMetaFromContext(ctx)
	if lm.EventLogger == nil {
		return
	}
	now := time.Now()
	for _, call := range calls {
		ageMS := int64(0)
		if !call.StartedAt.IsZero() {
			ageMS = now.Sub(call.StartedAt).Milliseconds()
		}
		LogInfo(lm.EventLogger, lm.AgentID, lm.SessionID, lm.CorrID,
			"tool_call_in_flight_at_stream_terminal", map[string]any{
				"reason":        strings.TrimSpace(reason),
				"tool_name":     call.ToolName,
				"tool_call_key": call.ToolCallKey,
				"args_summary":  call.ArgsSummary,
				"age_ms":        ageMS,
			})
	}
}

// WithToolCallEmitter attaches a ToolCallEmitter to a context.
func WithToolCallEmitter(ctx context.Context, emitter ToolCallEmitter) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Value(toolCallTrackerKey{}).(*toolCallTracker); !ok {
		ctx = context.WithValue(ctx, toolCallTrackerKey{}, newToolCallTracker())
	}
	return context.WithValue(ctx, toolCallEmitterKey{}, emitter)
}

// WithActiveToolCall annotates ctx with the tool currently being executed.
// If the call lacks a lifecycle ID (a producer defect), the annotation is
// skipped and the violation is logged — later callers will simply see no
// active tool call on ctx instead of a half-populated one.
func WithActiveToolCall(ctx context.Context, call providers.ToolCall) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	fullArgs := PrettyPrintArgs(call.Arguments)
	toolName := emittedToolCallNameForContext(ctx, call.Name, fullArgs, "")
	key, err := toolCallEventKey(call)
	if err != nil {
		logMissingToolCallID(ctx, "with_active_tool_call", call.Name, err)
		return ctx
	}
	return context.WithValue(ctx, activeToolCallContextKey{}, ActiveToolCallContext{
		ToolCallKey: key,
		ToolName:    toolName,
		FullArgs:    fullArgs,
		InterAgent:  DeriveInterAgentToolEvent(toolName, fullArgs, "", ToolCallStart, false, ""),
	})
}

// logMissingToolCallID emits a warning event when a code path reaches a
// lifecycle-ID-required boundary with an empty ID. Uses the logger attached
// to ctx via LogMetaFromContext; if ctx carries no logger (tests, bare
// contexts), the call is a silent no-op — by the time the turn runs live,
// the agent always wires a logger through.
func logMissingToolCallID(ctx context.Context, site, toolName string, err error) {
	if ctx == nil {
		return
	}
	lm := LogMetaFromContext(ctx)
	if lm.EventLogger == nil {
		return
	}
	LogWarning(lm.EventLogger, lm.AgentID, lm.SessionID, lm.CorrID,
		"tool_call_missing_id", map[string]any{
			"site":      site,
			"tool_name": toolName,
			"error":     err.Error(),
		})
}

// ActiveToolCallFromContext returns the tool currently executing on ctx.
func ActiveToolCallFromContext(ctx context.Context) (ActiveToolCallContext, bool) {
	if ctx == nil {
		return ActiveToolCallContext{}, false
	}
	active, ok := ctx.Value(activeToolCallContextKey{}).(ActiveToolCallContext)
	if !ok || strings.TrimSpace(active.ToolCallKey) == "" {
		return ActiveToolCallContext{}, false
	}
	return active, true
}

// EmitToolCall invokes the emitter attached to ctx, if present.
//
// Every event must carry a non-empty ToolCallKey (the tool call's lifecycle
// ID, stamped at the provider adapter boundary). Empty keys would leave the
// UI unable to pair Start and Complete events. Rather than crash the agent
// on a producer defect, EmitToolCall returns ErrMissingToolCallID wrapped
// with the tool name and phase; the caller is expected to log with full
// agent/correlation context so the producer gap surfaces without silently
// dropping the event.
//
// A nil return means the event was successfully handed to the emitter (or
// there was no emitter on ctx, which is also valid for ad-hoc paths). Name
// canonicalization that yields an empty name is likewise logged by
// EmitToolCall and then dropped with a nil return — it is not a caller-
// actionable failure, only a stream-metadata mismatch.
func EmitToolCall(ctx context.Context, event ToolCallEvent) error {
	if strings.TrimSpace(event.ToolCallKey) == "" {
		return fmt.Errorf("emit tool call: %w (tool=%q phase=%d)", ErrMissingToolCallID, event.ToolName, event.Phase)
	}
	if len(event.StreamMetadata) == 0 {
		if stream, ok := StreamMetadataFromContext(ctx); ok && len(stream.Metadata) > 0 {
			event.StreamMetadata = cloneStreamMetadata(stream.Metadata)
		}
	}
	event.ToolName = canonicalizeInterAgentToolName(event.ToolName, event.FullArgs, event.Output, event.StreamMetadata)
	if strings.TrimSpace(event.ToolName) == "" {
		if lm := LogMetaFromContext(ctx); lm.EventLogger != nil {
			LogWarning(lm.EventLogger, lm.AgentID, lm.SessionID, lm.CorrID,
				"tool_call_event_missing_name", map[string]any{
					"phase":         int(event.Phase),
					"tool_call_key": event.ToolCallKey,
					"args_summary":  event.ArgsSummary,
				})
		}
		return nil
	}
	event.InterAgent = NormalizeInterAgentToolEventForEmit(
		event.ToolName,
		event.FullArgs,
		event.Output,
		event.Phase,
		event.Success,
		event.ErrorMsg,
		event.InterAgent,
		event.StreamMetadata,
	)
	emitter, ok := ctx.Value(toolCallEmitterKey{}).(ToolCallEmitter)
	if !ok || emitter == nil {
		return nil
	}
	tracker := toolCallTrackerFromContext(ctx)
	var emitErr error
	if err := publishWithStreamLifecycle(ctx, guide.StreamEventToolCall, func() error {
		emitErr = emitter(event)
		return emitErr
	}); err != nil {
		// Lifecycle wrapper already preserved emitErr; logging is the
		// responsibility of the emitter's bus layer (it has no access to
		// LogMeta from there). We log here too so the agent's loop sees a
		// structured event no matter which layer dropped the publish.
		if lm := LogMetaFromContext(ctx); lm.EventLogger != nil {
			LogWarning(lm.EventLogger, lm.AgentID, lm.SessionID, lm.CorrID,
				"tool_call_event_emit_failed", map[string]any{
					"tool_name":     event.ToolName,
					"tool_call_key": event.ToolCallKey,
					"phase":         int(event.Phase),
					"error":         err.Error(),
				})
		}
	}
	// Track in-flight regardless of delivery: orphan telemetry reflects what
	// the agent attempted, not just what made it to the bus. recordInFlight
	// on Start, clearInFlight on Complete. The tracking is independent of
	// publish success because a Start that failed to publish should still
	// have its eventual Complete observed for symmetric accounting.
	if tracker != nil {
		switch event.Phase {
		case ToolCallStart:
			tracker.recordInFlight(event)
		case ToolCallComplete:
			tracker.clearInFlight(event)
		}
	}
	return emitErr
}

func emittedToolCallNameForContext(ctx context.Context, toolName, fullArgs, output string) string {
	if ctx == nil {
		return canonicalizeInterAgentToolName(toolName, fullArgs, output, nil)
	}
	if stream, ok := StreamMetadataFromContext(ctx); ok {
		return canonicalizeInterAgentToolName(toolName, fullArgs, output, stream.Metadata)
	}
	return canonicalizeInterAgentToolName(toolName, fullArgs, output, nil)
}

// ObserveProviderToolCallChunk translates provider tool streaming into a single
// pre-execution ToolCallStart event once the provider has fully described a
// tool call. TimedToolCall then emits only the completion event for that call.
func ObserveProviderToolCallChunk(ctx context.Context, chunk *providers.StreamChunk) {
	if chunk == nil {
		return
	}
	tracker := toolCallTrackerFromContext(ctx)
	if tracker == nil {
		return
	}
	switch chunk.Type {
	case providers.ChunkTypeStart:
		if chunk.RetryReset {
			tracker.reset()
		}
	case providers.ChunkTypeToolStart, providers.ChunkTypeToolDelta, providers.ChunkTypeToolEnd:
		tracker.observeChunk(ctx, chunk)
	}
}

func toolCallTrackerFromContext(ctx context.Context) *toolCallTracker {
	if ctx == nil {
		return nil
	}
	tracker, _ := ctx.Value(toolCallTrackerKey{}).(*toolCallTracker)
	return tracker
}

func (t *toolCallTracker) reset() {
	if t == nil {
		return
	}
	t.mu.Lock()
	clear(t.lifecycles)
	t.mu.Unlock()
}

func (t *toolCallTracker) observeChunk(ctx context.Context, chunk *providers.StreamChunk) {
	if t == nil || chunk == nil || chunk.ToolCall == nil {
		return
	}

	t.mu.Lock()
	state, err := t.ensurePendingLocked(chunk.ToolCall)
	if err != nil {
		t.mu.Unlock()
		logMissingToolCallID(ctx, "observe_chunk", chunk.ToolCall.Name, err)
		return
	}
	if state == nil {
		t.mu.Unlock()
		return
	}
	if name := strings.TrimSpace(chunk.ToolCall.Name); name != "" {
		state.Name = name
	}
	if kind := chunk.ToolCall.Kind; kind != "" {
		state.Kind = kind
	} else if state.Kind == "" && strings.TrimSpace(state.Name) == "web_search" {
		state.Kind = providers.ToolKindNativeWebSearch
	}
	if delta := chunk.ToolCall.ArgumentsDelta; delta != "" {
		state.Arguments.WriteString(delta)
	}
	if state.StartedAt.IsZero() && chunk.Type == providers.ChunkTypeToolStart {
		state.StartedAt = chunkTimestampOrNow(chunk)
	}

	// Snapshot what we'll need outside the tracker lock — Start/Complete
	// emissions happen via session methods that themselves acquire locks
	// and call EmitToolCall (which routes through the bus). We must not
	// hold tracker.mu across that work.
	wantStart := !state.Announced && strings.TrimSpace(state.ID) != "" && strings.TrimSpace(state.Name) != ""
	wantWebSearchComplete := chunk.Type == providers.ChunkTypeToolEnd && state.Kind == providers.ToolKindNativeWebSearch
	startCall := providers.ToolCall{
		ID:        state.ID,
		Name:      state.Name,
		Arguments: state.Arguments.String(),
	}
	startedAt := state.StartedAt
	if wantWebSearchComplete && startedAt.IsZero() {
		startedAt = chunkTimestampOrNow(chunk)
		state.StartedAt = startedAt
	}
	if wantWebSearchComplete {
		if lc, ok := t.lifecycles[startCall.ID]; ok {
			lc.stream = nil
			lc.preannouncedAt = time.Time{}
			lc.completed = true
		}
	}
	t.mu.Unlock()

	if wantStart {
		// preannounceToolCallStart now opens the session and emits Start
		// through it (idempotent). The legacy startEvent return is gone —
		// emission is owned by ToolCallSession. Late-args-resolved consult
		// and challenge calls reach this branch at ToolEnd because their
		// args only become parseable on the closing chunk; the session's
		// Start handles that the same way as a normal mid-stream
		// preannounce.
		t.preannounceToolCallStart(ctx, startCall, state, chunk)
	}
	if wantWebSearchComplete {
		// Native web_search emits its own Complete from the provider stream.
		// Route through the same session so Start/Complete pairing identity
		// stays consistent and the "second emit" is a no-op via session
		// idempotency. The chunk-supplied timestamps (startedAt, completedAt)
		// are the authoritative duration source — wall-clock time.Since
		// would be off by however long the chunk sat in our queue.
		completedAt := chunkTimestampOrNow(chunk)
		session := acquireToolCallSession(ctx, "", startCall)
		session.Start(startCall, startedAt, nil)
		session.CompleteAt(startCall, "", nil, completedAt)
	}
}

// preannounceToolCallStart used to emit a Start event directly. It now stages
// the start through ToolCallSession so a subsequent TimedToolCall /
// CompleteProviderNativeToolCall handoff cannot accidentally re-emit Start
// for the same call.ID. Returns a "start signal" (nil-safe) used by the
// observer caller to know whether the chunk produced an emission this time —
// the caller used to inspect the returned *ToolCallEvent to decide whether
// to publish; with sessions, Start emission happens inside the session and
// the caller just needs to know whether to bookkeep "announced".
func (t *toolCallTracker) preannounceToolCallStart(
	ctx context.Context,
	call providers.ToolCall,
	state *streamedToolCallState,
	chunk *providers.StreamChunk,
) bool {
	fullArgs := PrettyPrintArgs(call.Arguments)
	emittedToolName := emittedToolCallNameForContext(ctx, call.Name, fullArgs, "")
	interAgent := DeriveInterAgentToolEvent(
		emittedToolName,
		fullArgs,
		"",
		ToolCallStart,
		false,
		"",
	)
	if interAgent == nil && genericInterAgentToolRequiresResolvedArgs(call.Name) {
		return false
	}

	startedAt := state.StartedAt
	if startedAt.IsZero() {
		startedAt = chunkTimestampOrNow(chunk)
		state.StartedAt = startedAt
	}
	state.Announced = true
	t.mu.Lock()
	if lc := t.ensureLifecycleLocked(call.ID); lc != nil {
		lc.preannouncedAt = startedAt
	}
	t.mu.Unlock()

	// Acquire the session and emit Start through it so the
	// preannounce/TimedToolCall handoff cannot duplicate the Start
	// emission. acquireToolCallSession registers the session on the
	// lifecycle; Start is idempotent on subsequent calls.
	session := acquireToolCallSession(ctx, "", call)
	session.Start(call, startedAt, interAgent)
	return true
}

func genericInterAgentToolRequiresResolvedArgs(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "consult", "challenge_agent":
		return true
	default:
		return false
	}
}

// ensurePendingLocked returns the streaming state for chunk.ID on its
// lifecycle, creating the lifecycle (and its stream state) if needed.
// Returns (nil, error) when the chunk is missing the required lifecycle
// ID — the caller (observeChunk) logs with ctx metadata and drops the chunk
// so the turn stays alive.
func (t *toolCallTracker) ensurePendingLocked(chunk *providers.ToolCallChunk) (*streamedToolCallState, error) {
	if chunk == nil {
		return nil, nil
	}
	id := strings.TrimSpace(chunk.ID)
	if id == "" {
		return nil, fmt.Errorf("tool call chunk: %w (tool=%q)", ErrMissingToolCallID, chunk.Name)
	}
	lc := t.ensureLifecycleLocked(id)
	if lc.stream != nil {
		return lc.stream, nil
	}
	state := &streamedToolCallState{
		ID:   id,
		Name: strings.TrimSpace(chunk.Name),
		Kind: chunk.Kind,
	}
	if state.Kind == "" && state.Name == "web_search" {
		state.Kind = providers.ToolKindNativeWebSearch
	}
	lc.stream = state
	return state, nil
}

func chunkTimestampOrNow(chunk *providers.StreamChunk) time.Time {
	if chunk != nil && !chunk.Timestamp.IsZero() {
		return chunk.Timestamp
	}
	return time.Now()
}

func (t *toolCallTracker) consumePreannounced(call providers.ToolCall) (time.Time, bool) {
	if t == nil {
		return time.Time{}, false
	}
	id := strings.TrimSpace(call.ID)
	if id == "" {
		return time.Time{}, false
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	lc, ok := t.lifecycles[id]
	if !ok || lc.preannouncedAt.IsZero() {
		return time.Time{}, false
	}
	startedAt := lc.preannouncedAt
	lc.preannouncedAt = time.Time{}
	lc.stream = nil
	return startedAt, true
}

func (t *toolCallTracker) wasCompleted(call providers.ToolCall) bool {
	if t == nil {
		return false
	}
	id := strings.TrimSpace(call.ID)
	if id == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	lc, ok := t.lifecycles[id]
	return ok && lc.completed
}

// toolCallEventKey returns the tool call's lifecycle ID, which is the single,
// stable identifier used to pair ToolCallStart and ToolCallComplete events in
// the UI.
//
// Provider adapters are required to stamp a non-empty ID on every tool call
// they surface (see providers.EnsureToolCallID). An empty ID here is a
// programming defect that must be fixed at the source, not papered over with
// args/name-based fallbacks — those fallbacks previously caused UI rows to
// hang in the "active" state when accumulated args drifted between Start and
// Complete emissions. Returning ErrMissingToolCallID (wrapped with the tool
// name) lets the caller log the violation with full context instead of
// crashing the agent.
func toolCallEventKey(call providers.ToolCall) (string, error) {
	id := strings.TrimSpace(call.ID)
	if id == "" {
		return "", fmt.Errorf("tool call event key: %w (tool=%q)", ErrMissingToolCallID, call.Name)
	}
	return id, nil
}

// ToolCallSession is the canonical owner of a tool call's emission lifecycle.
// One session per call.ID. Start emits a ToolCallStart event exactly once;
// Complete emits a ToolCallComplete event exactly once. Both are idempotent —
// subsequent calls return false without re-emitting. Sessions are keyed in
// the tracker by call.ID so independent callers (streaming preannounce,
// TimedToolCall, CompleteProviderNativeToolCall) that all reach for "the
// session for this call" receive the same handle and cannot duplicate
// emissions.
//
// Migration note: synthetic inter-agent branches (BeginInterAgentBranch) still
// emit through EmitToolCall directly. They have a separate Start/Complete
// pairing pattern owned by InterAgentBranchHandle. A future pass should fold
// those into ToolCallSession too; for now they are correctly bounded by their
// own handle and do not produce the duplicate-row failure mode this type
// fixes for the streaming/TimedToolCall axis.
type ToolCallSession struct {
	ctx         context.Context
	agentID     string
	toolCallKey string

	mu        sync.Mutex
	started   bool
	completed bool
	startedAt time.Time
	// startedToolName / startedFullArgs / startedArgsSummary capture the
	// args observed at Start emission time. Complete may receive different
	// (more complete) args via the call passed to it; the start-time copies
	// are preserved here so the Complete event can reference what the UI
	// originally rendered.
	startedToolName    string
	startedFullArgs    string
	startedArgsSummary string
}

// acquireToolCallSession returns the canonical session for call.ID, creating
// one if none exists on the lifecycle. Two callers requesting a session for
// the same ID receive the same handle from the lifecycle. When no tracker is
// on the context (ad-hoc emit paths used in tests) a fresh session is
// returned that is not registered anywhere — single-use, no idempotency
// cross-caller, but still idempotent within the returned handle.
func acquireToolCallSession(ctx context.Context, agentID string, call providers.ToolCall) *ToolCallSession {
	key, err := toolCallEventKey(call)
	if err != nil {
		// Session with empty toolCallKey is a sentinel that no-ops in Start /
		// Complete. Callers do not need to nil-check; the violation is logged
		// once here so the producer gap is visible exactly at the site that
		// broke the invariant.
		logMissingToolCallID(ctx, "acquire_tool_call_session", call.Name, err)
		return &ToolCallSession{ctx: ctx, agentID: agentID}
	}
	tracker := toolCallTrackerFromContext(ctx)
	if tracker == nil {
		return &ToolCallSession{ctx: ctx, agentID: agentID, toolCallKey: key}
	}
	tracker.mu.Lock()
	lc := tracker.ensureLifecycleLocked(key)
	if lc.session != nil {
		// Update ctx/agentID on every acquire — later callers may carry
		// richer logging metadata than the streaming preannounce did. The
		// emission-state fields (started/completed/startedAt) stay pinned
		// to the first caller's state.
		lc.session.ctx = ctx
		if strings.TrimSpace(lc.session.agentID) == "" {
			lc.session.agentID = agentID
		}
		existing := lc.session
		tracker.mu.Unlock()
		return existing
	}
	sess := &ToolCallSession{ctx: ctx, agentID: agentID, toolCallKey: key}
	lc.session = sess
	tracker.mu.Unlock()
	return sess
}

// releaseToolCallSession clears the session handle from its lifecycle so
// subsequent acquires for the same call.ID create a fresh handle. The
// lifecycle itself remains in the tracker so wasCompleted can still answer
// "was a terminal event emitted for this ID" after the session is gone —
// that flag lives on lc.completed, not on the session.
func releaseToolCallSession(ctx context.Context, key string) {
	tracker := toolCallTrackerFromContext(ctx)
	if tracker == nil {
		return
	}
	tracker.mu.Lock()
	if lc, ok := tracker.lifecycles[key]; ok {
		lc.session = nil
		lc.completed = true
	}
	tracker.mu.Unlock()
}

// Start emits a ToolCallStart event the first time it is invoked. Returns
// true on the first call (event emitted) and false on every subsequent call
// (no-op — Start already fired, or this is a sentinel session acquired for a
// call with an empty lifecycle ID). Safe to call from multiple goroutines.
func (s *ToolCallSession) Start(call providers.ToolCall, startedAt time.Time, startInterAgentOverride *InterAgentToolEvent) bool {
	if s == nil || strings.TrimSpace(s.toolCallKey) == "" {
		return false
	}
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return false
	}
	fullArgs := PrettyPrintArgs(call.Arguments)
	emittedToolName := emittedToolCallNameForContext(s.ctx, call.Name, fullArgs, "")
	argsSummary := SummarizeToolArgs(call.Name, call.Arguments)
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	s.started = true
	s.startedAt = startedAt
	s.startedToolName = emittedToolName
	s.startedFullArgs = fullArgs
	s.startedArgsSummary = argsSummary
	s.mu.Unlock()

	interAgent := startInterAgentOverride
	if interAgent == nil {
		interAgent = DeriveInterAgentToolEvent(emittedToolName, fullArgs, "", ToolCallStart, false, "")
	}
	if err := EmitToolCall(s.ctx, ToolCallEvent{
		ToolCallKey: s.toolCallKey,
		Phase:       ToolCallStart,
		ToolName:    emittedToolName,
		ArgsSummary: argsSummary,
		FullArgs:    fullArgs,
		AgentID:     s.agentID,
		StartedAt:   startedAt,
		InterAgent:  interAgent,
	}); err != nil {
		logMissingToolCallID(s.ctx, "session_start", call.Name, err)
	}
	logToolCallSessionStart(s.ctx, s.toolCallKey, emittedToolName, argsSummary, startedAt)
	return true
}

// CompleteAt is Complete with an explicit completion timestamp, used by
// streaming-driven completions (notably native web_search) where the duration
// must reflect the provider-supplied chunk timestamps rather than
// time.Since(start). Pass the zero value to fall back to time.Since(startedAt).
func (s *ToolCallSession) CompleteAt(call providers.ToolCall, output string, err error, completedAt time.Time) bool {
	return s.completeInternal(call, output, err, completedAt)
}

// Complete emits a ToolCallComplete event the first time it is invoked.
// Returns true on the first call (event emitted) and false on every
// subsequent call (no-op). The session's tracker entry is released so a
// future acquire for the same call.ID returns a fresh session. Pass the
// call again because Complete-time args may be more complete than Start-time
// args (provider streaming sometimes finalizes args after preannounce).
func (s *ToolCallSession) Complete(call providers.ToolCall, output string, err error) bool {
	return s.completeInternal(call, output, err, time.Time{})
}

func (s *ToolCallSession) completeInternal(call providers.ToolCall, output string, err error, completedAt time.Time) bool {
	if s == nil || strings.TrimSpace(s.toolCallKey) == "" {
		return false
	}
	s.mu.Lock()
	if s.completed {
		s.mu.Unlock()
		return false
	}
	s.completed = true
	startedAt := s.startedAt
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	startedArgsSummary := s.startedArgsSummary
	s.mu.Unlock()

	outputStr, success, errorMsg := toolCallCompletionOutcome(output, err)
	fullArgs := PrettyPrintArgs(call.Arguments)
	emittedToolName := emittedToolCallNameForContext(s.ctx, call.Name, fullArgs, outputStr)
	argsSummary := startedArgsSummary
	if argsSummary == "" {
		argsSummary = SummarizeToolArgs(call.Name, call.Arguments)
	}
	duration := time.Since(startedAt)
	if !completedAt.IsZero() {
		if d := completedAt.Sub(startedAt); d >= 0 {
			duration = d
		}
	}
	if err := EmitToolCall(s.ctx, ToolCallEvent{
		ToolCallKey: s.toolCallKey,
		Phase:       ToolCallComplete,
		ToolName:    emittedToolName,
		ArgsSummary: argsSummary,
		FullArgs:    fullArgs,
		Output:      TruncateOutput(outputStr, maxOutputBytes),
		AgentID:     s.agentID,
		StartedAt:   startedAt,
		Duration:    duration,
		Success:     success,
		ErrorMsg:    errorMsg,
		InterAgent:  DeriveInterAgentToolEvent(emittedToolName, fullArgs, outputStr, ToolCallComplete, success, errorMsg),
	}); err != nil {
		logMissingToolCallID(s.ctx, "session_complete", call.Name, err)
	}
	logToolCallSessionComplete(s.ctx, s.toolCallKey, emittedToolName, argsSummary, outputStr, duration, success, errorMsg)
	releaseToolCallSession(s.ctx, s.toolCallKey)
	return true
}

// logToolCallSessionStart writes a tool_call_started event to both the
// agent's binary WAL and JSONL log. No-op when the context lacks a
// SessionEventLogger (boot-time emissions, ad-hoc tests). Single-point
// instrumentation: every agent that uses TimedToolCall (which is all of
// them) gets WAL coverage without per-agent wiring.
func logToolCallSessionStart(ctx context.Context, callKey, toolName, argsSummary string, startedAt time.Time) {
	lm := LogMetaFromContext(ctx)
	if lm.EventLogger == nil {
		return
	}
	payload := map[string]any{
		"tool_call_id": callKey,
		"tool_name":    toolName,
		"args_summary": argsSummary,
		"started_at":   startedAt.UnixNano(),
	}
	LogAgentEvent(lm.EventLogger, agentlog.EventToolCallStarted, lm.AgentID, lm.SessionID, lm.CorrID, "info", payload)
}

// logToolCallSessionComplete writes a tool_call_completed event with
// duration, success, output size, and error (if any). Mirrors the UI
// completion event into the durable log so the WAL has paired
// start/end records for every tool invocation.
func logToolCallSessionComplete(
	ctx context.Context,
	callKey, toolName, argsSummary, output string,
	duration time.Duration,
	success bool,
	errorMsg string,
) {
	lm := LogMetaFromContext(ctx)
	if lm.EventLogger == nil {
		return
	}
	payload := map[string]any{
		"tool_call_id": callKey,
		"tool_name":    toolName,
		"args_summary": argsSummary,
		"output_size":  len(output),
		"duration_ms":  duration.Milliseconds(),
		"success":      success,
	}
	if errorMsg != "" {
		payload["error"] = errorMsg
	}
	level := "info"
	eventType := agentlog.EventToolCallCompleted
	if !success {
		level = "warn"
	}
	LogAgentEvent(lm.EventLogger, eventType, lm.AgentID, lm.SessionID, lm.CorrID, level, payload)
}

// StartedAt returns the captured start time so streaming preannouncers can
// align their bookkeeping (preannounced map) with the session's authoritative
// start time. Returns zero time if Start has not yet been called.
func (s *ToolCallSession) StartedAt() time.Time {
	if s == nil {
		return time.Time{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startedAt
}

// HasStarted reports whether Start has been emitted for this session.
func (s *ToolCallSession) HasStarted() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.started
}

// TimedToolCall wraps a tool call execution with start/complete event emission.
// The execute callback performs the actual tool invocation.
//
// The ToolCallKey on every emitted event is call.ID — the lifecycle ID stamped
// at the provider adapter boundary. Start/Complete idempotency is owned by
// ToolCallSession: a streaming preannounce may have already started the
// session for this call.ID, in which case our Start call is a no-op and we
// only contribute the Complete.
//
// Complete is emitted via defer so the UI row closes even when execute panics
// or otherwise unwinds abnormally. We do NOT call recover() — panics are
// invariant violations in this codebase and must surface to the goroutine
// boundary so they appear in logs. The deferred function detects abnormal
// exit via the executeReturned flag (set only on the normal return path) and
// stamps the row with a panic-attributed error so the UI reflects the failure
// instead of a misleading "success" finalization. The panic then continues
// to propagate after Complete runs.
func TimedToolCall(
	ctx context.Context,
	agentID string,
	call providers.ToolCall,
	execute func() (string, error),
) (result string, err error) {
	if err := WaitWhileExecutionPaused(ctx); err != nil {
		return "", err
	}
	session := acquireToolCallSession(ctx, agentID, call)

	// Narrate ToolExecuting on the agent's parent claim. Best-effort —
	// no-ops when no accumulator/board/claim is on context. See
	// docs/CLAIMS_UI.md "Agent narration discipline".
	if acc := claims.AccumulatorFromContext(ctx); acc != nil {
		if board := acc.Board(); board != nil {
			RecordAgentState(ctx, board, acc.ClaimID(),
				"Running "+call.Name, AgentStateToolExecuting, nil)
		}
	}

	// Start is a no-op if a streaming preannounce already opened this session.
	// When that happens consumePreannounced still drains the legacy timing
	// map so older code paths that read it don't go stale; the session is
	// the source of truth for the actual emission.
	startedAt := time.Time{}
	if tracker := toolCallTrackerFromContext(ctx); tracker != nil {
		if t, ok := tracker.consumePreannounced(call); ok && !t.IsZero() {
			startedAt = t
		}
	}
	session.Start(call, startedAt, nil)

	// Structured tool-call record (Phase 0): decoupled from the session
	// emission. The session path feeds the UI; the recorder feeds the
	// per-day JSONL stream that sylk trace reads. Both are fed from the
	// same pair of call sites so they stay in lockstep with lifecycle.
	callStart := time.Now()
	if !startedAt.IsZero() {
		callStart = startedAt
	}
	emitToolStartRecord(ctx, agentID, call, callStart)

	executeReturned := false
	defer func() {
		if !executeReturned && err == nil {
			err = fmt.Errorf("tool %q unwound abnormally without returning", call.Name)
		}
		// ErrConsultYielded is a control-flow signal, not a tool
		// failure — the agent has parked awaiting a peer consult /
		// challenge / guardian-check. The session row stays open;
		// when the resume path injects the consult response as this
		// tool call's result, the matching emission completes the
		// row with the real outcome. Surfacing the yield as a
		// "tool failed" event here renders an alarming-but-bogus
		// failure in the chat panel.
		if IsConsultYielded(err) {
			return
		}
		session.Complete(call, result, err)
		emitToolCompleteRecord(ctx, agentID, call, result, err, time.Since(callStart))
	}()

	result, err = execute()
	executeReturned = true
	return result, err
}

// emitToolStartRecord writes a Phase 0 record to the structured tool
// stream when a ToolRecorder is reachable via the EventLogger bound
// to ctx. Nil-safe at every step — an agent running without an
// EventLogger or with the tool stream disabled silently skips.
func emitToolStartRecord(ctx context.Context, agentID string, call providers.ToolCall, startedAt time.Time) {
	lm := LogMetaFromContext(ctx)
	if lm.EventLogger == nil {
		return
	}
	recorder := lm.EventLogger.ToolRecorder()
	if recorder == nil {
		return
	}
	active, _ := ActiveToolCallFromContext(ctx)
	record := agentlog.ToolStartRecord{
		ToolCallKey:   toolCallKeyForRecord(active, call),
		CorrelationID: lm.CorrID,
		LLMCallID:     LLMCallIDFromContext(ctx),
		AgentID:       lm.AgentID,
		SessionID:     lm.SessionID,
		ToolName:      toolNameForRecord(active, call),
		Args:          agentlog.MarshalToolArgs(toolArgsForRecord(active, call)),
		InterAgent:    interAgentRefForRecord(active),
		StartedAt:     startedAt,
	}
	if record.AgentID == "" {
		record.AgentID = agentID
	}
	_ = recorder.Start(record)
}

// emitToolCompleteRecord mirrors emitToolStartRecord on the Phase 1
// side. duration is the wall-clock elapsed between Start and return;
// result/err are forwarded verbatim (redaction happens at write
// time through the recorder's stream writer).
func emitToolCompleteRecord(ctx context.Context, agentID string, call providers.ToolCall, result string, err error, duration time.Duration) {
	lm := LogMetaFromContext(ctx)
	if lm.EventLogger == nil {
		return
	}
	recorder := lm.EventLogger.ToolRecorder()
	if recorder == nil {
		return
	}
	active, _ := ActiveToolCallFromContext(ctx)

	output := agentlog.MarshalToolArgs(asRawOutput(result))
	output, truncatedFrom := agentlog.TruncateOutput(output, defaultToolOutputCap)

	success := err == nil
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}

	record := agentlog.ToolCompleteRecord{
		ToolCallKey:         toolCallKeyForRecord(active, call),
		CorrelationID:       lm.CorrID,
		LLMCallID:           LLMCallIDFromContext(ctx),
		AgentID:             lm.AgentID,
		SessionID:           lm.SessionID,
		ToolName:            toolNameForRecord(active, call),
		Output:              output,
		OutputTruncatedFrom: truncatedFrom,
		DurationMs:          duration.Milliseconds(),
		Success:             success,
		Error:               errStr,
		InterAgent:          interAgentRefForRecord(active),
	}
	if record.AgentID == "" {
		record.AgentID = agentID
	}
	_ = recorder.Complete(record)

	// Fabric emission: mirror the tool completion into the activity
	// stream so Memory Forest harvest, lenses, and the fabric
	// observability log can see tool-shape × outcome precedent. The
	// per-day JSONL stream above is per-agent telemetry; this is the
	// cross-agent signal source the forest needs. Emit at
	// ResolutionFine (the canonical ToolCallCompleted tier) —
	// ForestSubscriber elects it regardless of tier.
	emitToolCallActivity(ctx, record, call, duration)
}

// emitToolCallActivity appends an ActionToolCallCompleted activity
// mirroring the tool-completion record. Carries the tool name as
// TargetArtifact, an extracted scope hint as PathPrefix, and a
// compact payload of outcome metadata. Safe to call unconditionally:
// activity.Append is backed by a best-effort sink and never blocks
// the tool-complete path.
func emitToolCallActivity(
	ctx context.Context,
	record agentlog.ToolCompleteRecord,
	call providers.ToolCall,
	duration time.Duration,
) {
	actor := activity.Actor{
		AgentID:   record.AgentID,
		AgentType: agentTypeFromBaggage(ctx),
	}
	if actor.AgentType == "" {
		actor.AgentType = agentTypeFromLogContext(ctx)
	}
	subject := activity.Subject{
		TargetArtifact: record.ToolName,
		PathPrefix:     extractToolScope(call),
	}
	state := activity.ActivityState(activity.StatePoint)

	payload := map[string]any{
		"tool_call_key": record.ToolCallKey,
		"duration_ms":   duration.Milliseconds(),
		"success":       record.Success,
	}
	if record.Error != "" {
		payload["error"] = record.Error
	}
	if record.LLMCallID != "" {
		payload["llm_call_id"] = record.LLMCallID
	}
	if record.CorrelationID != "" {
		payload["correlation_id"] = record.CorrelationID
	}
	payloadJSON, _ := json.Marshal(payload)

	a := activity.AgentActivity{
		ID:         activity.ActivityID(activity.NewID()),
		SessionID:  activity.SessionID(record.SessionID),
		Timestamp:  time.Now(),
		Resolution: activity.ResolutionFor(activity.ActionToolCallCompleted),
		Actor:      actor,
		Action:     activity.ActionToolCallCompleted,
		Subject:    subject,
		Payload:    payloadJSON,
		State:      state,
	}
	// Tier 5 linkage: if the agent issued a forest consult in the
	// recent TTL window, auto-populate Caused so the forest bridge
	// can join this outcome back to the consult that shaped it.
	// The ctx-carried helper takes precedence when present
	// (explicit > implicit); falls back to the process-wide
	// tracker otherwise.
	if caused := recentForestConsultID(ctx); caused != "" {
		c := activity.ActivityID(caused)
		a.Caused = &c
	} else {
		activity.EnrichCausationFromConsult(&a)
	}
	activity.Append(ctx, a)
}

// extractToolScope pulls a workspace-ish scope hint from the tool
// call's arguments if present. The fabric uses PathPrefix for scope
// matching across activities, so threading a path here enables
// queries like "every tool call in services/billing/".
func extractToolScope(call providers.ToolCall) string {
	if strings.TrimSpace(call.Arguments) == "" {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		return ""
	}
	for _, key := range []string{"path", "file_path", "scope", "target"} {
		if v, ok := args[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// agentTypeFromBaggage reads the fabric baggage for agent_type. The
// fabric context is the canonical carrier for actor identity across
// chokepoints; chokepoint emissions use it via activity.StartSpan.
func agentTypeFromBaggage(ctx context.Context) string {
	fc := activity.FabricContextFromContext(ctx)
	if fc.Baggage == nil {
		return ""
	}
	return fc.Baggage["agent_type"]
}

// agentTypeFromLogContext falls back to LogMeta when fabric baggage
// is empty. Not every entry path installs baggage; LogMeta is
// populated on every steering-manager-bound turn.
func agentTypeFromLogContext(_ context.Context) string {
	// LogMeta currently has no AgentType field. The fabric baggage
	// path is the authoritative source; this helper is a stub so
	// callers don't have to branch on presence.
	return ""
}

// recentForestConsultID pulls the most recent forest consult ID
// attached to this ctx. Populated by the forest_consult handler so
// subsequent activity emissions within the same call stack can
// declare Caused back to the consult. Returns empty string when no
// consult is attached.
func recentForestConsultID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(forestConsultIDCtxKey{}).(string)
	return v
}

// forestConsultIDCtxKey attaches the most-recent forest-consult
// activity ID to ctx so downstream chokepoints can link their
// emissions back as Caused. Set by the `*_forest_consult` skill
// handler.
type forestConsultIDCtxKey struct{}

// WithForestConsultID returns a context carrying the consult
// activity ID so downstream tool/LLM/decision emissions link back.
// Callers should not typically set this manually — the
// `*_forest_consult` skill handler plumbs it automatically.
func WithForestConsultID(ctx context.Context, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(id) == "" {
		return ctx
	}
	return context.WithValue(ctx, forestConsultIDCtxKey{}, id)
}

const defaultToolOutputCap = 64 * 1024

// toolCallKeyForRecord prefers the active-tool-call context's key
// (stamped by the provider adapter at tool emit time) and falls back
// to the call's own ID if the context is unset.
func toolCallKeyForRecord(active ActiveToolCallContext, call providers.ToolCall) string {
	if strings.TrimSpace(active.ToolCallKey) != "" {
		return active.ToolCallKey
	}
	return strings.TrimSpace(call.ID)
}

func toolNameForRecord(active ActiveToolCallContext, call providers.ToolCall) string {
	if strings.TrimSpace(active.ToolName) != "" {
		return active.ToolName
	}
	return call.Name
}

// toolArgsForRecord returns a JSON-serializable value for the tool's
// arguments. Prefers the pretty-printed FullArgs on ActiveToolCall
// context (already validated upstream); falls back to parsing the
// call's raw argument string.
func toolArgsForRecord(active ActiveToolCallContext, call providers.ToolCall) any {
	raw := strings.TrimSpace(active.FullArgs)
	if raw == "" {
		raw = strings.TrimSpace(call.Arguments)
	}
	if raw == "" {
		return nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
		return parsed
	}
	return raw
}

// interAgentRefForRecord lifts the inter-agent metadata from the
// active tool call context into the record-shape expected by the
// structured tool stream. Returns nil when no inter-agent info is
// attached (the common case for plain read/write/run tool calls).
func interAgentRefForRecord(active ActiveToolCallContext) *agentlog.ToolInterAgentRef {
	if active.InterAgent == nil {
		return nil
	}
	return &agentlog.ToolInterAgentRef{
		Kind:       active.InterAgent.Kind,
		AgentTypes: append([]string(nil), active.InterAgent.AgentTypes...),
		ThreadKey:  active.InterAgent.ThreadKey,
		Status:     active.InterAgent.Status,
	}
}

// asRawOutput returns a value suitable for JSON marshaling. A JSON
// string input is returned as a parsed value; a non-JSON input is
// wrapped in a single-string object so the stream stays valid JSONL.
func asRawOutput(result string) any {
	trimmed := strings.TrimSpace(result)
	if trimmed == "" {
		return nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
		return parsed
	}
	return trimmed
}

// CompleteProviderNativeToolCall emits a terminal tool-call event for a
// provider-native tool the model executed itself. If no streamed preannounce
// exists, the session's Start fires first so the UI can render the call as a
// normal completed tool row. Idempotent across paths via ToolCallSession —
// the historical wasCompleted guard is now redundant but kept as a fast-path
// for the streaming web_search ToolEnd branch that already drained the tracker.
func CompleteProviderNativeToolCall(
	ctx context.Context,
	agentID string,
	call providers.ToolCall,
	output string,
) {
	tracker := toolCallTrackerFromContext(ctx)
	if tracker != nil && tracker.wasCompleted(call) {
		return
	}

	session := acquireToolCallSession(ctx, agentID, call)
	startedAt := time.Time{}
	if tracker != nil {
		if t, ok := tracker.consumePreannounced(call); ok && !t.IsZero() {
			startedAt = t
		}
	}
	session.Start(call, startedAt, nil)
	session.Complete(call, output, nil)
}

func toolCallCompletionOutcome(result string, err error) (string, bool, string) {
	if err == nil {
		return result, true, ""
	}
	if payload, ok := toolCallControlPayload(err); ok {
		if strings.TrimSpace(result) == "" {
			result = payload
		}
		return result, true, ""
	}
	return result, false, err.Error()
}

func toolCallControlPayload(err error) (string, bool) {
	switch {
	case errors.Is(err, skills.ErrRerouteRequested):
		return `{"rerouted":true}`, true
	case errors.Is(err, skills.ErrDelegatedRequested):
		if payload, marshalErr := skills.MarshalDelegatedPayload(err); marshalErr == nil && strings.TrimSpace(payload) != "" {
			return payload, true
		}
		return `{"delegated":true}`, true
	default:
		return "", false
	}
}

// PrettyPrintArgs formats JSON args with indentation for the expanded view.
// Returns the original string if parsing fails.
func PrettyPrintArgs(rawJSON string) string {
	rawJSON = strings.TrimSpace(rawJSON)
	if rawJSON == "" || rawJSON == "{}" {
		return rawJSON
	}
	var parsed any
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		return rawJSON
	}
	indented, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		return rawJSON
	}
	return string(indented)
}

// TruncateOutput returns the first maxBytes bytes of s, appending "..." if truncated.
// Avoids splitting multi-byte runes by backing up to the last valid boundary.
func TruncateOutput(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	if maxBytes <= 3 {
		return "..."
	}
	// Back up to avoid splitting a UTF-8 sequence.
	cut := maxBytes - 3
	for cut > 0 && cut < len(s) && !isUTF8Start(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

// isUTF8Start reports whether b is either ASCII or the start of a multi-byte rune.
func isUTF8Start(b byte) bool {
	return b&0xC0 != 0x80
}

// SummarizeToolArgs extracts a compact one-liner from JSON args.
// Priority keys (path, pattern, query, command, etc.) are checked first.
// Falls back to the first string value found. Truncated to maxArgsSummaryLen.
// For verb-style tools (those with an "op" key), combines op with the first
// priority key: "op=read path=some/file.go".
func SummarizeToolArgs(toolName, rawJSON string) string {
	rawJSON = strings.TrimSpace(rawJSON)
	if rawJSON == "" || rawJSON == "{}" {
		return ""
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		return ""
	}

	// For verb-style tools, prefix with op= and append the first priority key.
	opPrefix := ""
	if opVal, ok := parsed["op"]; ok {
		if s := stringifyArgValue(opVal); s != "" {
			opPrefix = "op=" + s
		}
	}

	// Check priority keys first.
	for _, key := range priorityArgKeys {
		if val, ok := parsed[key]; ok {
			if s := stringifyArgValue(val); s != "" {
				detail := key + "=" + s
				if opPrefix != "" {
					return truncateArgSummary(opPrefix + " " + detail)
				}
				return truncateArgSummary(detail)
			}
		}
	}

	// Op-only summary for verb tools with no priority key yet.
	if opPrefix != "" {
		return truncateArgSummary(opPrefix)
	}

	// Fallback: first string value.
	for key, val := range parsed {
		if s := stringifyArgValue(val); s != "" {
			return truncateArgSummary(key + "=" + s)
		}
	}

	return ""
}

// stringifyArgValue converts a JSON value to a compact string representation.
func stringifyArgValue(val any) string {
	switch v := val.(type) {
	case string:
		return v
	case float64:
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d", int64(v))
		}
		return fmt.Sprintf("%g", v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

// truncateArgSummary truncates an argument summary line to maxArgsSummaryLen.
func truncateArgSummary(s string) string {
	runes := []rune(s)
	if len(runes) <= maxArgsSummaryLen {
		return s
	}
	return string(runes[:maxArgsSummaryLen-1]) + "…"
}
