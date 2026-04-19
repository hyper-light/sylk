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
type ToolCallEmitter func(ToolCallEvent)

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

type toolCallTracker struct {
	mu           sync.Mutex
	pending      map[string]*streamedToolCallState
	preannounced map[string]time.Time
	completed    map[string]struct{}
	// inFlight tracks tool calls keyed by ToolCallKey whose Start event has
	// been emitted but whose Complete event has not. Populated by EmitToolCall
	// after publish so we never assert in-flight on an event that failed to
	// reach the bus. Drained at Complete. snapshotInFlightToolCalls() reads
	// this for orphan-detection telemetry.
	inFlight map[string]inFlightToolCall
	// sessions is the canonical owner index for a tool call's emission
	// lifecycle (see ToolCallSession). One session per call.ID. Multiple
	// callers requesting a session for the same ID receive the same handle,
	// which makes Start and Complete emissions idempotent across the
	// preannounce/TimedToolCall/CompleteProviderNativeToolCall handoff. This
	// is the structural fix for the "two emitters race and the UI sees
	// duplicate rows or a missing Complete" failure mode that local patches
	// could only address one collision at a time.
	sessions map[string]*ToolCallSession
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
		pending:      make(map[string]*streamedToolCallState),
		preannounced: make(map[string]time.Time),
		completed:    make(map[string]struct{}),
		inFlight:     make(map[string]inFlightToolCall),
		sessions:     make(map[string]*ToolCallSession),
	}
}

// recordInFlight registers a Start emission. Called from EmitToolCall after the
// publish succeeds (or would succeed if a publisher exists), regardless of
// whether the bus actually has subscribers.
func (t *toolCallTracker) recordInFlight(event ToolCallEvent) {
	if t == nil || strings.TrimSpace(event.ToolCallKey) == "" {
		return
	}
	t.mu.Lock()
	t.inFlight[event.ToolCallKey] = inFlightToolCall{
		ToolCallKey: event.ToolCallKey,
		ToolName:    event.ToolName,
		ArgsSummary: event.ArgsSummary,
		StartedAt:   event.StartedAt,
	}
	t.mu.Unlock()
}

// clearInFlight removes a Complete-paired entry. Called from EmitToolCall
// after the Complete publish.
func (t *toolCallTracker) clearInFlight(event ToolCallEvent) {
	if t == nil || strings.TrimSpace(event.ToolCallKey) == "" {
		return
	}
	t.mu.Lock()
	delete(t.inFlight, event.ToolCallKey)
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
	if len(t.inFlight) == 0 {
		return nil
	}
	out := make([]inFlightToolCall, 0, len(t.inFlight))
	for _, call := range t.inFlight {
		out = append(out, call)
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
func WithActiveToolCall(ctx context.Context, call providers.ToolCall) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	fullArgs := PrettyPrintArgs(call.Arguments)
	toolName := emittedToolCallNameForContext(ctx, call.Name, fullArgs, "")
	return context.WithValue(ctx, activeToolCallContextKey{}, ActiveToolCallContext{
		ToolCallKey: toolCallEventKey(call),
		ToolName:    toolName,
		FullArgs:    fullArgs,
		InterAgent:  DeriveInterAgentToolEvent(toolName, fullArgs, "", ToolCallStart, false, ""),
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
// Every event must carry a non-empty ToolCallKey (the tool call's
// lifecycle ID, stamped at the provider adapter boundary). Empty keys
// would leave the UI unable to pair Start and Complete events, so they
// are refused as a programming defect rather than silently tolerated.
func EmitToolCall(ctx context.Context, event ToolCallEvent) {
	if strings.TrimSpace(event.ToolCallKey) == "" {
		panic(fmt.Sprintf("EmitToolCall: event missing required ToolCallKey (tool=%q phase=%d)", event.ToolName, event.Phase))
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
		return
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
		return
	}
	tracker := toolCallTrackerFromContext(ctx)
	delivered := publishWithStreamLifecycle(ctx, guide.StreamEventToolCall, func() {
		emitter(event)
	})
	// Track in-flight regardless of delivery: even if the emitter is gated
	// (it currently isn't for tool-call events thanks to (A)), the orphan
	// telemetry should reflect what the agent attempted, not just what made
	// it to the bus. recordInFlight on Start, clearInFlight on Complete.
	_ = delivered
	if tracker != nil {
		switch event.Phase {
		case ToolCallStart:
			tracker.recordInFlight(event)
		case ToolCallComplete:
			tracker.clearInFlight(event)
		}
	}
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
	clear(t.pending)
	clear(t.preannounced)
	clear(t.completed)
	t.mu.Unlock()
}

func (t *toolCallTracker) observeChunk(ctx context.Context, chunk *providers.StreamChunk) {
	if t == nil || chunk == nil || chunk.ToolCall == nil {
		return
	}

	t.mu.Lock()
	state := t.ensurePendingLocked(chunk.ToolCall)
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
		delete(t.preannounced, startCall.ID)
		delete(t.pending, startCall.ID)
		t.completed[startCall.ID] = struct{}{}
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
	t.preannounced[call.ID] = startedAt

	// Acquire the session and emit Start through it so the
	// preannounce/TimedToolCall handoff cannot duplicate the Start
	// emission. acquireToolCallSession registers in t.sessions; Start is
	// idempotent on subsequent calls.
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

func (t *toolCallTracker) ensurePendingLocked(chunk *providers.ToolCallChunk) *streamedToolCallState {
	if chunk == nil {
		return nil
	}
	id := strings.TrimSpace(chunk.ID)
	if id == "" {
		// Every tool-call chunk must carry a non-empty ID stamped at the
		// provider adapter boundary (see providers.EnsureToolCallID). Missing
		// IDs break UI pairing and are a programming defect.
		panic(fmt.Sprintf("ToolCallChunk missing required ID (name=%q)", chunk.Name))
	}
	if state, ok := t.pending[id]; ok {
		return state
	}
	state := &streamedToolCallState{
		ID:   id,
		Name: strings.TrimSpace(chunk.Name),
		Kind: chunk.Kind,
	}
	if state.Kind == "" && state.Name == "web_search" {
		state.Kind = providers.ToolKindNativeWebSearch
	}
	t.pending[id] = state
	return state
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
	startedAt, ok := t.preannounced[id]
	if !ok {
		return time.Time{}, false
	}
	delete(t.preannounced, id)
	delete(t.pending, id)
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
	_, ok := t.completed[id]
	return ok
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
// Complete emissions.
func toolCallEventKey(call providers.ToolCall) string {
	id := strings.TrimSpace(call.ID)
	if id == "" {
		panic(fmt.Sprintf("providers.ToolCall missing required ID (name=%q)", call.Name))
	}
	return id
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
// one if none exists. Two callers requesting a session for the same ID
// receive the same handle. When no tracker is on the context (ad-hoc emit
// paths used in tests) a fresh session is returned that is not registered
// anywhere — single-use, no idempotency cross-caller, but still idempotent
// within the returned handle.
func acquireToolCallSession(ctx context.Context, agentID string, call providers.ToolCall) *ToolCallSession {
	key := toolCallEventKey(call)
	tracker := toolCallTrackerFromContext(ctx)
	if tracker == nil {
		return &ToolCallSession{ctx: ctx, agentID: agentID, toolCallKey: key}
	}
	tracker.mu.Lock()
	if existing, ok := tracker.sessions[key]; ok {
		// Update ctx/agentID on every acquire — later callers may carry
		// richer logging metadata than the streaming preannounce did. The
		// emission-state fields (started/completed/startedAt) stay pinned
		// to the first caller's state.
		existing.ctx = ctx
		if strings.TrimSpace(existing.agentID) == "" {
			existing.agentID = agentID
		}
		tracker.mu.Unlock()
		return existing
	}
	sess := &ToolCallSession{ctx: ctx, agentID: agentID, toolCallKey: key}
	tracker.sessions[key] = sess
	tracker.mu.Unlock()
	return sess
}

// releaseToolCallSession removes a finalized session from the tracker so
// subsequent acquires for the same call.ID create a fresh handle. Called
// from Complete after the terminal event is emitted. Without this a
// long-lived agent context would accumulate session entries indefinitely.
func releaseToolCallSession(ctx context.Context, key string) {
	tracker := toolCallTrackerFromContext(ctx)
	if tracker == nil {
		return
	}
	tracker.mu.Lock()
	delete(tracker.sessions, key)
	tracker.mu.Unlock()
}

// Start emits a ToolCallStart event the first time it is invoked. Returns
// true on the first call (event emitted) and false on every subsequent call
// (no-op — Start already fired). Safe to call from multiple goroutines.
func (s *ToolCallSession) Start(call providers.ToolCall, startedAt time.Time, startInterAgentOverride *InterAgentToolEvent) bool {
	if s == nil {
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
	EmitToolCall(s.ctx, ToolCallEvent{
		ToolCallKey: s.toolCallKey,
		Phase:       ToolCallStart,
		ToolName:    emittedToolName,
		ArgsSummary: argsSummary,
		FullArgs:    fullArgs,
		AgentID:     s.agentID,
		StartedAt:   startedAt,
		InterAgent:  interAgent,
	})
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
	if s == nil {
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
	EmitToolCall(s.ctx, ToolCallEvent{
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
	})
	releaseToolCallSession(s.ctx, s.toolCallKey)
	return true
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
func TimedToolCall(
	ctx context.Context,
	agentID string,
	call providers.ToolCall,
	execute func() (string, error),
) (string, error) {
	if err := WaitWhileExecutionPaused(ctx); err != nil {
		return "", err
	}
	session := acquireToolCallSession(ctx, agentID, call)

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

	result, err := execute()
	session.Complete(call, result, err)
	return result, err
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
func SummarizeToolArgs(toolName, rawJSON string) string {
	rawJSON = strings.TrimSpace(rawJSON)
	if rawJSON == "" || rawJSON == "{}" {
		return ""
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		return ""
	}

	// Check priority keys first.
	for _, key := range priorityArgKeys {
		if val, ok := parsed[key]; ok {
			if s := stringifyArgValue(val); s != "" {
				return truncateArgSummary(key + "=" + s)
			}
		}
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
