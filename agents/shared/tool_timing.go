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
}

func newToolCallTracker() *toolCallTracker {
	return &toolCallTracker{
		pending:      make(map[string]*streamedToolCallState),
		preannounced: make(map[string]time.Time),
		completed:    make(map[string]struct{}),
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
	return context.WithValue(ctx, activeToolCallContextKey{}, ActiveToolCallContext{
		ToolCallKey: toolCallEventKey(call),
		ToolName:    call.Name,
		FullArgs:    fullArgs,
		InterAgent:  DeriveInterAgentToolEvent(call.Name, fullArgs, "", ToolCallStart, false, ""),
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
func EmitToolCall(ctx context.Context, event ToolCallEvent) {
	if len(event.StreamMetadata) == 0 {
		if stream, ok := StreamMetadataFromContext(ctx); ok && len(stream.Metadata) > 0 {
			event.StreamMetadata = cloneStreamMetadata(stream.Metadata)
		}
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
	publishWithStreamLifecycle(ctx, guide.StreamEventToolCall, func() {
		emitter(event)
	})
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

	var startEvent *ToolCallEvent
	var completeEvent *ToolCallEvent
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
	if chunk.Type != providers.ChunkTypeToolEnd && !state.Announced && strings.TrimSpace(state.ID) != "" && strings.TrimSpace(state.Name) != "" {
		call := providers.ToolCall{
			ID:        state.ID,
			Name:      state.Name,
			Arguments: state.Arguments.String(),
		}
		startEvent = t.preannounceToolCallStart(call, state, chunk)
	}
	if chunk.Type == providers.ChunkTypeToolEnd && !state.Announced {
		call := providers.ToolCall{
			ID:        state.ID,
			Name:      state.Name,
			Arguments: state.Arguments.String(),
		}
		startEvent = t.preannounceToolCallStart(call, state, chunk)
		if startEvent != nil && state.Kind == providers.ToolKindNativeWebSearch && !state.StartedAt.IsZero() {
			startEvent.StartedAt = state.StartedAt
		}
	}
	if chunk.Type == providers.ChunkTypeToolEnd && state.Kind == providers.ToolKindNativeWebSearch {
		call := providers.ToolCall{
			ID:        state.ID,
			Name:      state.Name,
			Arguments: state.Arguments.String(),
		}
		startedAt := state.StartedAt
		if startedAt.IsZero() {
			startedAt = chunkTimestampOrNow(chunk)
			state.StartedAt = startedAt
		}
		completedAt := chunkTimestampOrNow(chunk)
		duration := completedAt.Sub(startedAt)
		if duration < 0 {
			duration = 0
		}
		delete(t.preannounced, toolCallTrackingKey(call))
		delete(t.pending, toolCallStateKey(call.ID, call.Name))
		if key := toolCallTrackingKey(call); key != "" {
			t.completed[key] = struct{}{}
		}
		completeEvent = &ToolCallEvent{
			ToolCallKey: toolCallEventKey(call),
			Phase:       ToolCallComplete,
			ToolName:    call.Name,
			ArgsSummary: SummarizeToolArgs(call.Name, call.Arguments),
			FullArgs:    PrettyPrintArgs(call.Arguments),
			AgentID:     "",
			StartedAt:   startedAt,
			Duration:    duration,
			Success:     true,
			InterAgent: DeriveInterAgentToolEvent(
				call.Name,
				PrettyPrintArgs(call.Arguments),
				"",
				ToolCallComplete,
				true,
				"",
			),
		}
	}
	t.mu.Unlock()

	if startEvent != nil {
		EmitToolCall(ctx, *startEvent)
	}
	if completeEvent != nil {
		EmitToolCall(ctx, *completeEvent)
	}
}

func (t *toolCallTracker) preannounceToolCallStart(
	call providers.ToolCall,
	state *streamedToolCallState,
	chunk *providers.StreamChunk,
) *ToolCallEvent {
	fullArgs := PrettyPrintArgs(call.Arguments)
	interAgent := DeriveInterAgentToolEvent(
		call.Name,
		fullArgs,
		"",
		ToolCallStart,
		false,
		"",
	)
	if interAgent == nil && genericInterAgentToolRequiresResolvedArgs(call.Name) {
		return nil
	}

	startedAt := state.StartedAt
	if startedAt.IsZero() {
		startedAt = chunkTimestampOrNow(chunk)
		state.StartedAt = startedAt
	}
	state.Announced = true
	t.preannounced[toolCallTrackingKey(call)] = startedAt
	return &ToolCallEvent{
		ToolCallKey: toolCallEventKey(call),
		Phase:       ToolCallStart,
		ToolName:    call.Name,
		ArgsSummary: SummarizeToolArgs(call.Name, call.Arguments),
		FullArgs:    fullArgs,
		StartedAt:   startedAt,
		InterAgent:  interAgent,
	}
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
	key := toolCallChunkStateKey(chunk)
	if key == "" {
		return nil
	}
	if state, ok := t.pending[key]; ok {
		return state
	}
	state := &streamedToolCallState{
		ID:   strings.TrimSpace(chunk.ID),
		Name: strings.TrimSpace(chunk.Name),
		Kind: chunk.Kind,
	}
	if state.Kind == "" && state.Name == "web_search" {
		state.Kind = providers.ToolKindNativeWebSearch
	}
	t.pending[key] = state
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
	key := toolCallTrackingKey(call)
	if key == "" {
		return time.Time{}, false
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	startedAt, ok := t.preannounced[key]
	if !ok {
		return time.Time{}, false
	}
	delete(t.preannounced, key)
	delete(t.pending, toolCallStateKey(call.ID, call.Name))
	return startedAt, true
}

func (t *toolCallTracker) wasCompleted(call providers.ToolCall) bool {
	if t == nil {
		return false
	}
	key := toolCallTrackingKey(call)
	if key == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.completed[key]
	return ok
}

func toolCallChunkStateKey(chunk *providers.ToolCallChunk) string {
	if chunk == nil {
		return ""
	}
	return toolCallStateKey(chunk.ID, chunk.Name)
}

func toolCallStateKey(id, name string) string {
	if id = strings.TrimSpace(id); id != "" {
		return "id:" + id
	}
	if name = strings.TrimSpace(name); name != "" {
		return "name:" + name
	}
	return ""
}

func toolCallTrackingKey(call providers.ToolCall) string {
	if key := toolCallStateKey(call.ID, ""); key != "" {
		return key
	}
	name := strings.TrimSpace(call.Name)
	if name == "" {
		return ""
	}
	return "sig:" + name + "\x00" + normalizeToolCallArguments(call.Arguments)
}

func toolCallEventKey(call providers.ToolCall) string {
	return toolCallTrackingKey(call)
}

func normalizeToolCallArguments(arguments string) string {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return ""
	}
	var parsed any
	if err := json.Unmarshal([]byte(arguments), &parsed); err == nil {
		if normalized, marshalErr := json.Marshal(parsed); marshalErr == nil {
			return string(normalized)
		}
	}
	return strings.Join(strings.Fields(arguments), " ")
}

// TimedToolCall wraps a tool call execution with start/complete event emission.
// The execute callback performs the actual tool invocation.
func TimedToolCall(
	ctx context.Context,
	agentID string,
	call providers.ToolCall,
	execute func() (string, error),
) (string, error) {
	if err := WaitWhileExecutionPaused(ctx); err != nil {
		return "", err
	}
	summary := SummarizeToolArgs(call.Name, call.Arguments)
	fullArgs := PrettyPrintArgs(call.Arguments)
	start := time.Now()
	tracker := toolCallTrackerFromContext(ctx)

	if tracker != nil {
		if preannouncedStart, ok := tracker.consumePreannounced(call); ok {
			if !preannouncedStart.IsZero() {
				start = preannouncedStart
			}
		} else {
			startInterAgent := DeriveInterAgentToolEvent(call.Name, fullArgs, "", ToolCallStart, false, "")
			EmitToolCall(ctx, ToolCallEvent{
				ToolCallKey: toolCallEventKey(call),
				Phase:       ToolCallStart,
				ToolName:    call.Name,
				ArgsSummary: summary,
				FullArgs:    fullArgs,
				AgentID:     agentID,
				StartedAt:   start,
				InterAgent:  startInterAgent,
			})
		}
	} else {
		startInterAgent := DeriveInterAgentToolEvent(call.Name, fullArgs, "", ToolCallStart, false, "")
		EmitToolCall(ctx, ToolCallEvent{
			ToolCallKey: toolCallEventKey(call),
			Phase:       ToolCallStart,
			ToolName:    call.Name,
			ArgsSummary: summary,
			FullArgs:    fullArgs,
			AgentID:     agentID,
			StartedAt:   start,
			InterAgent:  startInterAgent,
		})
	}

	result, err := execute()
	output, success, errorMsg := toolCallCompletionOutcome(result, err)
	interAgent := DeriveInterAgentToolEvent(call.Name, fullArgs, output, ToolCallComplete, success, errorMsg)

	event := ToolCallEvent{
		ToolCallKey: toolCallEventKey(call),
		Phase:       ToolCallComplete,
		ToolName:    call.Name,
		ArgsSummary: summary,
		FullArgs:    fullArgs,
		Output:      TruncateOutput(output, maxOutputBytes),
		AgentID:     agentID,
		StartedAt:   start,
		Duration:    time.Since(start),
		Success:     success,
		ErrorMsg:    errorMsg,
		InterAgent:  interAgent,
	}
	EmitToolCall(ctx, event)

	return result, err
}

// CompleteProviderNativeToolCall emits a terminal tool-call event for a
// provider-native tool the model executed itself. If no streamed preannounce
// exists, it synthesizes the start event first so the UI can render the call as
// a normal completed tool row.
func CompleteProviderNativeToolCall(
	ctx context.Context,
	agentID string,
	call providers.ToolCall,
	output string,
) {
	summary := SummarizeToolArgs(call.Name, call.Arguments)
	fullArgs := PrettyPrintArgs(call.Arguments)
	start := time.Now()
	tracker := toolCallTrackerFromContext(ctx)
	if tracker != nil && tracker.wasCompleted(call) {
		return
	}

	if tracker != nil {
		if preannouncedStart, ok := tracker.consumePreannounced(call); ok {
			if !preannouncedStart.IsZero() {
				start = preannouncedStart
			}
		} else {
			EmitToolCall(ctx, ToolCallEvent{
				ToolCallKey: toolCallEventKey(call),
				Phase:       ToolCallStart,
				ToolName:    call.Name,
				ArgsSummary: summary,
				FullArgs:    fullArgs,
				AgentID:     agentID,
				StartedAt:   start,
				InterAgent:  DeriveInterAgentToolEvent(call.Name, fullArgs, "", ToolCallStart, false, ""),
			})
		}
	} else {
		EmitToolCall(ctx, ToolCallEvent{
			ToolCallKey: toolCallEventKey(call),
			Phase:       ToolCallStart,
			ToolName:    call.Name,
			ArgsSummary: summary,
			FullArgs:    fullArgs,
			AgentID:     agentID,
			StartedAt:   start,
			InterAgent:  DeriveInterAgentToolEvent(call.Name, fullArgs, "", ToolCallStart, false, ""),
		})
	}

	EmitToolCall(ctx, ToolCallEvent{
		ToolCallKey: toolCallEventKey(call),
		Phase:       ToolCallComplete,
		ToolName:    call.Name,
		ArgsSummary: summary,
		FullArgs:    fullArgs,
		Output:      TruncateOutput(output, maxOutputBytes),
		AgentID:     agentID,
		StartedAt:   start,
		Duration:    time.Since(start),
		Success:     true,
		InterAgent:  DeriveInterAgentToolEvent(call.Name, fullArgs, output, ToolCallComplete, true, ""),
	})
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
