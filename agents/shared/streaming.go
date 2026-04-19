package shared

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/providers"
)

const streamedTextMetadataKey = "shared_streamed_text"

const (
	streamMetadataNestedBranch      = "chat_nested_branch"
	streamMetadataTopLevelTransfer  = "chat_top_level_transfer"
	streamMetadataParentCorrelation = "chat_parent_correlation_id"
	streamMetadataParentToolCallKey = "chat_parent_tool_call_key"
	streamMetadataInterAgentThread  = "chat_inter_agent_thread_key"
	streamMetadataInterAgentKind    = "chat_inter_agent_kind"
	// streamMetadataOriginatorContinuation marks a routed request as the
	// *continuation* of an earlier turn on the same originator agent — not a
	// new top-level turn and not a nested child. Used when a challenge's
	// response (e.g. tester validate_work → inspector) returns control to the
	// originator, so the TUI resumes the originator's existing chat entry
	// inline rather than creating a second entry for the same interaction.
	streamMetadataOriginatorContinuation = "chat_continuation_of_correlation_id"

	streamMetadataAgentType = "agent_type"
	streamMetadataAgentName = "agent_name"
)

// streamAttributionKeys are metadata fields that describe the *publisher* of
// a stream event. They must never be inherited from a forwarded caller —
// WithForwardedStreamContext strips them, and each agent is required to
// re-set them via WithOwnedStreamIdentity before emitting events.
var streamAttributionKeys = [...]string{
	streamMetadataAgentType,
	streamMetadataAgentName,
}

// StreamContext carries streaming correlation data through context.
type StreamContext struct {
	CorrelationID string
	SourceAgentID string
	Metadata      map[string]any
}

// InterAgentBranchMetadata identifies a nested child stream that belongs to
// an inter-agent consult/challenge branch owned by a parent chat entry.
type InterAgentBranchMetadata struct {
	ParentCorrelationID string
	ParentToolCallKey   string
	ThreadKey           string
	Kind                string
}

type streamContextKey struct{}
type streamLifecycleKey struct{}
type streamEventVisibilityKey struct{}

type streamLifecycleState struct {
	mu              sync.Mutex
	terminalStarted bool
	completed       bool
}

// WithStreamContext attaches streaming metadata to a context. Also
// initializes the per-request agent-activity tracker so terminal-state
// telemetry can observe whether the agent announced any state transitions.
func WithStreamContext(ctx context.Context, correlationID, sourceAgentID string) context.Context {
	ctx = withStreamLifecycle(ctx)
	ctx = withStreamActivityState(ctx)
	return context.WithValue(ctx, streamContextKey{}, StreamContext{
		CorrelationID: correlationID,
		SourceAgentID: sourceAgentID,
	})
}

// WithStreamContextMetadata attaches stable metadata such as pipeline identity
// to an existing stream context so UI layers can preserve canonical rows.
func WithStreamContextMetadata(ctx context.Context, metadata map[string]any) context.Context {
	if ctx == nil || len(metadata) == 0 {
		return ctx
	}
	current, ok := ctx.Value(streamContextKey{}).(StreamContext)
	if !ok || current.CorrelationID == "" {
		return ctx
	}
	cloned := make(map[string]any, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}
	current.Metadata = cloned
	return context.WithValue(ctx, streamContextKey{}, current)
}

// WithForwardedStreamContext attaches stream routing identity for a forwarded
// request and merges parent-correlation metadata needed by the chat UI.
//
// Attribution fields (agent_type, agent_name) are stripped from the inherited
// metadata because they describe the *publisher* of a stream event, not the
// routing path. Every agent must claim its own identity via
// WithOwnedStreamIdentity before publishing. Without this strip, an agent
// that forwards a request to another agent would see its chunks attributed
// to the original caller in the TUI, because the TUI reads agent_type
// directly from emitted metadata (ui/bridge/guide.go).
func WithForwardedStreamContext(
	ctx context.Context,
	correlationID, sourceAgentID, parentCorrelationID string,
	metadata map[string]any,
) context.Context {
	ctx = WithStreamContext(ctx, correlationID, sourceAgentID)
	merged := cloneStreamMetadata(metadata)
	for _, key := range streamAttributionKeys {
		if merged == nil {
			break
		}
		delete(merged, key)
	}
	parentCorrelationID = strings.TrimSpace(parentCorrelationID)
	if parentCorrelationID != "" {
		if merged == nil {
			merged = make(map[string]any, 1)
		}
		merged[streamMetadataParentCorrelation] = parentCorrelationID
		if hasNestedInterAgentBranchMetadata(merged) {
			delete(merged, streamMetadataTopLevelTransfer)
		} else {
			merged[streamMetadataTopLevelTransfer] = true
		}
	}
	return WithStreamContextMetadata(ctx, merged)
}

// WithOwnedStreamIdentity stamps the current agent's display identity into
// stream metadata. Every call to publish a stream event (chunk, start,
// complete, tool-call, progress) reads agent_type and agent_name from the
// emitted metadata, so the agent that writes to the stream is the one that
// must set them — inheriting them from the forwarder causes mis-attribution
// in the TUI (e.g. orchestrator chunks rendered under an "Architect" header
// when architect routed the plan handoff).
//
// Call this once at the top of handleBusRequest (after WithForwardedStreamContext,
// which clears inherited attribution). Forgetting to call it is caught by
// the TUI falling back to RespondingAgentID — attribution may be thin but
// never wrong.
func WithOwnedStreamIdentity(ctx context.Context, agentType, agentName string) context.Context {
	agentType = strings.TrimSpace(agentType)
	if agentType == "" {
		return ctx
	}
	existing, _ := StreamMetadataFromContext(ctx)
	merged := cloneStreamMetadata(existing.Metadata)
	if merged == nil {
		merged = make(map[string]any, 2)
	}
	merged[streamMetadataAgentType] = agentType
	if agentName = strings.TrimSpace(agentName); agentName != "" {
		merged[streamMetadataAgentName] = agentName
	}
	return WithStreamContextMetadata(ctx, merged)
}

// StreamMetadataFromContext extracts streaming metadata from a context.
func StreamMetadataFromContext(ctx context.Context) (StreamContext, bool) {
	metadata, ok := ctx.Value(streamContextKey{}).(StreamContext)
	if !ok || metadata.CorrelationID == "" {
		return StreamContext{}, false
	}
	return metadata, true
}

// ParentCorrelationIDFromContext returns the upstream/parent correlation
// id recorded in stream metadata via streamMetadataParentCorrelation, or
// empty when the current context has no parent (top-level request).
//
// Activity publishers (guardian approvals, pipeline challenges, any
// agent publishActivity) call this to stamp ParentCorrelationID on
// outgoing events so the UI can keep the parent's thinking indicator
// alive while the child runs. A non-empty return value means "I am a
// child of this correlation"; an empty return means "I am top-level."
func ParentCorrelationIDFromContext(ctx context.Context) string {
	stream, ok := StreamMetadataFromContext(ctx)
	if !ok || stream.Metadata == nil {
		return ""
	}
	raw, ok := stream.Metadata[streamMetadataParentCorrelation]
	if !ok {
		return ""
	}
	v, ok := raw.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
}

// WithStreamEventVisibility forces shared stream lifecycle/progress publishers
// to emit the supplied visibility for events emitted from ctx.
func WithStreamEventVisibility(ctx context.Context, visibility events.EventVisibility) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, streamEventVisibilityKey{}, visibility)
}

func streamEventVisibilityFromContext(ctx context.Context) (events.EventVisibility, bool) {
	if ctx == nil {
		return events.VisibilityUser, false
	}
	visibility, ok := ctx.Value(streamEventVisibilityKey{}).(events.EventVisibility)
	if !ok {
		return events.VisibilityUser, false
	}
	return visibility, true
}

func withStreamLifecycle(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if existing, _ := ctx.Value(streamLifecycleKey{}).(*streamLifecycleState); existing != nil {
		return ctx
	}
	return context.WithValue(ctx, streamLifecycleKey{}, &streamLifecycleState{})
}

func streamLifecycleFromContext(ctx context.Context) *streamLifecycleState {
	if ctx == nil {
		return nil
	}
	lifecycle, _ := ctx.Value(streamLifecycleKey{}).(*streamLifecycleState)
	return lifecycle
}

// publishWithStreamLifecycle gates a single stream-event publish against the
// stream's terminal lifecycle and propagates any publish error up to the
// caller. Returns:
//   - publishErr: non-nil if the underlying publish call returned an error.
//     The caller (typically PublishStreamEvent) must surface this to its own
//     caller so the agent's loop can observe the failure and reason about it.
//     A nil return means either the publish succeeded or the event was
//     correctly suppressed by the lifecycle gate (post-terminal text/progress).
//
// Suppression by the gate is NOT an error — it is a deliberate design choice
// to prevent late text from corrupting the user's view after StreamComplete
// finalizes the message. The caller has no recovery path for suppression and
// no need to know about it. Bus failure, on the other hand, IS observable
// (the user lost a chunk) and must propagate.
func publishWithStreamLifecycle(ctx context.Context, eventType guide.StreamEventType, publish func() error) error {
	lifecycle := streamLifecycleFromContext(ctx)
	delivered, lateBypass, publishErr := publishWithLifecycleState(lifecycle, eventType, publish)
	if lateBypass {
		// Tool-call events whose Complete races the agent's StreamComplete
		// (slow handler, callback-driven synthetic branch, request
		// cancellation race) used to be silently dropped here, leaving the
		// UI row stuck pending forever. Logging the bypass surfaces the
		// rate so we can see whether the late-delivery path is healthy or
		// hiding a regression.
		if lm := LogMetaFromContext(ctx); lm.EventLogger != nil {
			LogInfo(lm.EventLogger, lm.AgentID, lm.SessionID, lm.CorrID,
				"tool_call_event_after_stream_terminal", map[string]any{
					"event_type": string(eventType),
				})
		}
	}
	if publishErr != nil {
		return publishErr
	}
	if !delivered {
		// Suppressed by the lifecycle gate (e.g., late text after stream
		// complete). Not an error — the caller has nothing to surface.
		return nil
	}
	return nil
}

// publishWithLifecycleState gates a single stream-event publish against the
// stream's terminal lifecycle. Returns (delivered, lateBypass, publishErr):
//   - delivered = true means publish was invoked.
//   - lateBypass = true means publish was invoked despite terminalStarted being
//     set, because the event type owns its own out-of-band lifecycle (currently
//     only StreamEventToolCall). Callers can use this to log telemetry.
//   - publishErr is whatever the publish callback returned; nil if publish was
//     not invoked or if it succeeded.
//
// The lifecycle gate exists to suppress late stream-content events (text
// chunks, progress, thinking) so they cannot leak into the user's view after
// StreamComplete finalizes the message. Tool-call events are categorically
// different: they are metadata about side effects with their own Start/Complete
// pairing identity (see toolCallEventKey), and a slow handler or async
// inter-agent branch callback can legitimately deliver one after the parent
// stream's text lifecycle has ended. Including them in the gate caused every
// "tool completed but row stayed pending" bug we have hunted: the bytes never
// reached the bus to begin with, so no producer-side or matcher-side patch
// could close the row.
func publishWithLifecycleState(lifecycle *streamLifecycleState, eventType guide.StreamEventType, publish func() error) (delivered bool, lateBypass bool, publishErr error) {
	if publish == nil {
		return false, false, nil
	}
	if lifecycle == nil {
		publishErr = publish()
		return true, false, publishErr
	}

	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()

	switch eventType {
	case guide.StreamEventComplete:
		if lifecycle.completed {
			return false, false, nil
		}
		lifecycle.terminalStarted = true
		lifecycle.completed = true
		publishErr = publish()
		return true, false, publishErr
	case guide.StreamEventError:
		if lifecycle.completed {
			return false, false, nil
		}
		lifecycle.terminalStarted = true
		publishErr = publish()
		return true, false, publishErr
	case guide.StreamEventToolCall:
		// Bypass the terminal gate for tool-call events. They are out-of-band
		// metadata, paired by toolCallEventKey, and dropping them after
		// terminalStarted produces stuck-pending rows the UI cannot recover
		// from. Both Start and Complete are bypassed symmetrically so that
		// callback-driven synthetic branches (guardian approval responses,
		// archivalist consults) can still open and close their rows after
		// the parent stream finalizes.
		late := lifecycle.terminalStarted
		publishErr = publish()
		return true, late, publishErr
	case guide.StreamEventAgentState:
		// Agent-state transitions are the canonical "what is happening now"
		// channel. They are orthogonal to text-stream lifecycle — a
		// `complete` or `errored` state must be deliverable even if it
		// arrives alongside or just after StreamComplete, and an inter-agent
		// `awaiting_peer_response` state can legitimately fire while the
		// underlying text stream is between LLM turns. Dropping any state
		// event produces a silent gap in the UI activity indicator, which
		// is exactly the failure mode state events exist to eliminate.
		late := lifecycle.terminalStarted
		publishErr = publish()
		return true, late, publishErr
	default:
		if lifecycle.terminalStarted {
			return false, false, nil
		}
		publishErr = publish()
		return true, false, publishErr
	}
}

// UsageAccumulator tracks token usage across multiple LLM calls.
type UsageAccumulator struct {
	mu              sync.Mutex
	inputTotal      int
	outputTotal     int
	reasoningTotal  int
	cacheReadTotal  int
	cacheWriteTotal int
}

type usageAccumulatorKey struct{}

// WithUsageAccumulator creates a context with an attached usage accumulator.
func WithUsageAccumulator(ctx context.Context) (context.Context, *UsageAccumulator) {
	acc := &UsageAccumulator{}
	return context.WithValue(ctx, usageAccumulatorKey{}, acc), acc
}

// AccumulateUsage adds provider usage to the context's accumulator.
func AccumulateUsage(ctx context.Context, usage *providers.Usage) {
	if usage == nil {
		return
	}
	acc, ok := ctx.Value(usageAccumulatorKey{}).(*UsageAccumulator)
	if !ok || acc == nil {
		return
	}
	acc.mu.Lock()
	acc.inputTotal += usage.InputTokens
	acc.outputTotal += usage.OutputTokens
	acc.reasoningTotal += usage.ReasoningTokens
	acc.cacheReadTotal += usage.CacheReadTokens
	acc.cacheWriteTotal += usage.CacheWriteTokens
	acc.mu.Unlock()
}

// Total returns the accumulated usage as a StreamUsage.
func (a *UsageAccumulator) Total() *guide.StreamUsage {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inputTotal == 0 && a.outputTotal == 0 &&
		a.reasoningTotal == 0 && a.cacheReadTotal == 0 && a.cacheWriteTotal == 0 {
		return nil
	}
	return &guide.StreamUsage{
		InputTokens:      a.inputTotal,
		OutputTokens:     a.outputTotal,
		ReasoningTokens:  a.reasoningTotal,
		CacheReadTokens:  a.cacheReadTotal,
		CacheWriteTokens: a.cacheWriteTotal,
	}
}

func TotalUsageTokens(usage *guide.StreamUsage) int64 {
	if usage == nil {
		return 0
	}
	return int64(
		usage.InputTokens +
			usage.OutputTokens +
			usage.ReasoningTokens +
			usage.CacheReadTokens +
			usage.CacheWriteTokens,
	)
}

// PublishStreamEvent publishes a stream event to the bus.
//
// Returns an error if the bus.Publish call itself failed; nil if the event
// was delivered or correctly suppressed by the lifecycle gate (post-terminal
// text/progress, where suppression is by design and not a failure). When an
// error is returned, it is also logged at warning level here so the failure
// is observable even when the caller chooses to propagate it further. The
// agent's loop receives the error like any other tool-execution failure and
// the LLM reasons about it — there are no internal retries or fallbacks.
//
// Returns nil (without publishing) when bus, channels, event, or context
// stream metadata are missing — these are caller-side preconditions that
// indicate the agent is not in a publishable state. Those misses are logged
// at debug level for diagnostic visibility.
func PublishStreamEvent(
	bus guide.EventBus,
	channels *guide.AgentChannels,
	ctx context.Context,
	agentID string,
	event *guide.StreamEvent,
) error {
	metadata, ok := StreamMetadataFromContext(ctx)
	if !ok || bus == nil || channels == nil || event == nil {
		return nil
	}
	if visibility, ok := streamEventVisibilityFromContext(ctx); ok {
		cloned := *event
		cloned.Visibility = visibility
		event = &cloned
	}

	stream := &guide.StreamResponse{
		CorrelationID:     metadata.CorrelationID,
		RespondingAgentID: agentID,
		TargetAgentID:     metadata.SourceAgentID,
		Metadata:          cloneStreamMetadata(metadata.Metadata),
		Event:             event,
	}

	msg := &guide.Message{
		ID:            fmt.Sprintf("%s_stream_%d", agentID, time.Now().UnixNano()),
		CorrelationID: metadata.CorrelationID,
		Type:          guide.MessageTypeStream,
		Payload:       stream,
		SourceAgentID: agentID,
		TargetAgentID: metadata.SourceAgentID,
		Timestamp:     time.Now(),
	}

	if err := publishWithStreamLifecycle(ctx, event.Type, func() error {
		return bus.Publish(channels.Responses, msg)
	}); err != nil {
		if lm := LogMetaFromContext(ctx); lm.EventLogger != nil {
			LogWarning(lm.EventLogger, lm.AgentID, lm.SessionID, lm.CorrID,
				"stream_event_publish_failed", map[string]any{
					"event_type":     string(event.Type),
					"agent_id":       agentID,
					"target_agent":   metadata.SourceAgentID,
					"correlation_id": metadata.CorrelationID,
					"error":          err.Error(),
				})
		}
		return fmt.Errorf("publish stream event %s: %w", event.Type, err)
	}
	return nil
}

func cloneStreamMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}
	return cloned
}

// StreamResponseMetadataFromContext returns a cloned copy of the current
// stream metadata so custom publishers can attach the same branch and routing
// identity as the shared stream helpers.
func StreamResponseMetadataFromContext(ctx context.Context) map[string]any {
	stream, ok := StreamMetadataFromContext(ctx)
	if !ok {
		return nil
	}
	return cloneStreamMetadata(stream.Metadata)
}

// MergeStreamMetadata clones base and overlays values from extra.
func MergeStreamMetadata(base, extra map[string]any) map[string]any {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	merged := cloneStreamMetadata(base)
	if merged == nil {
		merged = make(map[string]any, len(extra))
	}
	for key, value := range extra {
		merged[key] = value
	}
	return merged
}

// RouteMetadataWithInterAgentBranch stamps nested-branch metadata onto a
// child route request when it originates from an active consult/challenge tool.
func RouteMetadataWithInterAgentBranch(ctx context.Context, metadata map[string]any) map[string]any {
	metadata = RouteMetadataWithTaskScope(ctx, metadata)
	stream, ok := StreamMetadataFromContext(ctx)
	if !ok || strings.TrimSpace(stream.CorrelationID) == "" {
		return metadata
	}
	active, ok := ActiveToolCallFromContext(ctx)
	if !ok || active.InterAgent == nil {
		return metadata
	}
	return applyInterAgentBranchMetadata(metadata, InterAgentBranchMetadata{
		ParentCorrelationID: strings.TrimSpace(stream.CorrelationID),
		ParentToolCallKey:   strings.TrimSpace(active.ToolCallKey),
		ThreadKey:           strings.TrimSpace(active.InterAgent.ThreadKey),
		Kind:                strings.TrimSpace(active.InterAgent.Kind),
	})
}

// RouteMetadataWithExplicitInterAgentBranch stamps a specific branch identity
// onto a route request for challenge validation/process flows.
func RouteMetadataWithExplicitInterAgentBranch(
	ctx context.Context,
	metadata map[string]any,
	branch InterAgentBranchMetadata,
) map[string]any {
	metadata = RouteMetadataWithTaskScope(ctx, metadata)
	stream, ok := StreamMetadataFromContext(ctx)
	if ok && strings.TrimSpace(stream.CorrelationID) != "" && strings.TrimSpace(branch.ParentCorrelationID) == "" {
		branch.ParentCorrelationID = strings.TrimSpace(stream.CorrelationID)
	}
	if active, ok := ActiveToolCallFromContext(ctx); ok {
		if strings.TrimSpace(branch.ParentToolCallKey) == "" {
			branch.ParentToolCallKey = strings.TrimSpace(active.ToolCallKey)
		}
		if strings.TrimSpace(branch.Kind) == "" && active.InterAgent != nil {
			branch.Kind = strings.TrimSpace(active.InterAgent.Kind)
		}
	}
	return applyInterAgentBranchMetadata(metadata, branch)
}

// RouteMetadataWithOriginatorContinuation stamps continuation lineage onto a
// route request so the TUI resumes the originator's existing chat entry
// inline instead of spawning a second top-level entry.
//
// Use for challenge-response returns: when the challenged agent answers via
// validate_work, the routed request back to the originator carries this
// metadata so the originator's post-validation work appends to the same
// chat entry that contains its pre-challenge work + the nested challenge
// row. Do NOT use for forward handoffs (handoff_next) that legitimately
// transfer top-level ownership — those use RouteMetadataWithExplicitTopLevelTransfer.
//
// Two lineage pointers are stamped:
//
//   - chat_continuation_of_correlation_id → the originator (inspector) whose
//     existing entry should be resumed inline.
//   - chat_parent_correlation_id → the responder (tester) whose child stream
//     is now resolved; the TUI uses this to settle the nested challenge row
//     that was waiting for the response.
//
// Mutually exclusive with nested-branch and top-level-transfer; this helper
// clears conflicting keys so exactly one routing intent reaches the UI.
func RouteMetadataWithOriginatorContinuation(
	ctx context.Context,
	metadata map[string]any,
	originatorCorrelationID string,
	responderCorrelationID string,
) map[string]any {
	metadata = RouteMetadataWithTaskScope(ctx, metadata)
	originatorCorrelationID = strings.TrimSpace(originatorCorrelationID)
	if originatorCorrelationID == "" {
		return metadata
	}
	cloned := cloneStreamMetadata(metadata)
	if cloned == nil {
		cloned = make(map[string]any, 3)
	}
	delete(cloned, streamMetadataNestedBranch)
	delete(cloned, streamMetadataParentToolCallKey)
	delete(cloned, streamMetadataInterAgentThread)
	delete(cloned, streamMetadataInterAgentKind)
	delete(cloned, streamMetadataTopLevelTransfer)
	cloned[streamMetadataOriginatorContinuation] = originatorCorrelationID
	if responderCorrelationID = strings.TrimSpace(responderCorrelationID); responderCorrelationID != "" {
		cloned[streamMetadataParentCorrelation] = responderCorrelationID
	} else {
		cloned[streamMetadataParentCorrelation] = originatorCorrelationID
	}
	return cloned
}

// RouteMetadataWithExplicitTopLevelTransfer stamps explicit top-level transfer
// lineage onto a route request so mirrored protocol streams can preserve the
// parent correlation even when the child's first visible event is a synthetic
// bootstrap from progress/tool traffic.
func RouteMetadataWithExplicitTopLevelTransfer(
	ctx context.Context,
	metadata map[string]any,
	parentCorrelationID string,
) map[string]any {
	metadata = RouteMetadataWithTaskScope(ctx, metadata)
	parentCorrelationID = strings.TrimSpace(parentCorrelationID)
	if parentCorrelationID == "" {
		if stream, ok := StreamMetadataFromContext(ctx); ok {
			parentCorrelationID = strings.TrimSpace(stream.CorrelationID)
		}
	}
	if parentCorrelationID == "" {
		return metadata
	}
	cloned := cloneStreamMetadata(metadata)
	if cloned == nil {
		cloned = make(map[string]any, 2)
	}
	delete(cloned, streamMetadataNestedBranch)
	delete(cloned, streamMetadataParentToolCallKey)
	delete(cloned, streamMetadataInterAgentThread)
	delete(cloned, streamMetadataInterAgentKind)
	cloned[streamMetadataParentCorrelation] = parentCorrelationID
	cloned[streamMetadataTopLevelTransfer] = true
	return cloned
}

func applyInterAgentBranchMetadata(metadata map[string]any, branch InterAgentBranchMetadata) map[string]any {
	branch.ParentCorrelationID = strings.TrimSpace(branch.ParentCorrelationID)
	branch.ParentToolCallKey = strings.TrimSpace(branch.ParentToolCallKey)
	branch.ThreadKey = strings.TrimSpace(branch.ThreadKey)
	branch.Kind = strings.TrimSpace(branch.Kind)
	if branch.ParentCorrelationID == "" || !isNestedInterAgentKind(branch.Kind) {
		return metadata
	}
	cloned := cloneStreamMetadata(metadata)
	if cloned == nil {
		cloned = make(map[string]any, 4)
	}
	delete(cloned, streamMetadataTopLevelTransfer)
	cloned[streamMetadataNestedBranch] = true
	cloned[streamMetadataParentCorrelation] = branch.ParentCorrelationID
	if branch.ParentToolCallKey != "" {
		cloned[streamMetadataParentToolCallKey] = branch.ParentToolCallKey
	}
	if branch.ThreadKey != "" {
		cloned[streamMetadataInterAgentThread] = branch.ThreadKey
	}
	if branch.Kind != "" {
		cloned[streamMetadataInterAgentKind] = branch.Kind
	}
	return cloned
}

func hasNestedInterAgentBranchMetadata(metadata map[string]any) bool {
	if len(metadata) == 0 {
		return false
	}
	if nested, _ := metadata[streamMetadataNestedBranch].(bool); nested {
		if parent, _ := metadata[streamMetadataParentCorrelation].(string); strings.TrimSpace(parent) != "" {
			return true
		}
	}
	if nested, _ := metadata[streamMetadataNestedBranch].(string); strings.EqualFold(strings.TrimSpace(nested), "true") {
		if parent, _ := metadata[streamMetadataParentCorrelation].(string); strings.TrimSpace(parent) != "" {
			return true
		}
	}
	return false
}

// PublishStreamStart emits a stream start event AND announces the canonical
// `receiving` agent-activity state so the UI has an activity indicator from
// the moment the request enters the agent. Returns the first error observed
// (start event or state event) so the agent's loop surfaces any delivery
// failure. Stream-start is considered the authoritative source for the
// `receiving` transition; agents should transition to `reasoning` /
// `tool_executing` / etc. explicitly as work progresses.
func PublishStreamStart(bus guide.EventBus, channels *guide.AgentChannels, ctx context.Context, agentID string) error {
	if err := PublishStreamEvent(bus, channels, ctx, agentID, &guide.StreamEvent{
		Type:      guide.StreamEventStart,
		Timestamp: time.Now(),
	}); err != nil {
		return err
	}
	return PublishAgentState(bus, channels, ctx, agentID, agentTypeFromStreamMetadata(ctx, agentID),
		guide.AgentStateReceiving, "", nil)
}

// agentTypeFromStreamMetadata returns the agent_type the agent has stamped
// into its own stream metadata via WithOwnedStreamIdentity, falling back to
// the explicit agentID when the metadata is missing. Used by the stream
// publishers to auto-emit state events without callers having to pass
// agent type again.
func agentTypeFromStreamMetadata(ctx context.Context, agentID string) string {
	if stream, ok := StreamMetadataFromContext(ctx); ok && len(stream.Metadata) > 0 {
		if v, _ := stream.Metadata[streamMetadataAgentType].(string); strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return strings.TrimSpace(agentID)
}

// PublishStreamChunk emits a text data chunk. Returns the underlying publish
// error for the agent's loop to surface; nil on success or when the caller is
// not in a publishable state.
func PublishStreamChunk(bus guide.EventBus, channels *guide.AgentChannels, ctx context.Context, agentID, text string) error {
	return PublishStreamEvent(bus, channels, ctx, agentID, &guide.StreamEvent{
		Type:      guide.StreamEventData,
		Text:      text,
		Timestamp: time.Now(),
	})
}

// PublishStreamProgress emits a progress update tied to the current stream.
// Returns the underlying publish error for the agent's loop to surface; nil
// on success or when the message is empty.
func PublishStreamProgress(bus guide.EventBus, channels *guide.AgentChannels, ctx context.Context, agentID, message string) error {
	return publishStreamProgress(bus, channels, ctx, agentID, message, false)
}

// PublishAgentState declares an explicit agent activity state transition.
//
// State events are the canonical source of truth for user-visible "what is
// the agent doing right now" — they are published at every transition
// boundary (receiving → reasoning → tool_executing / consulting_peer /
// dispatching_to_peer / awaiting_peer_response → composing_response →
// complete | errored). The UI uses these events to keep a persistent
// activity indicator current even between text-stream boundaries and
// inter-agent routing hops. They are orthogonal to text chunks and flow on
// the same response channel via StreamEventAgentState.
//
// `detail` is the short human-readable phrase surfaced to the user (e.g.,
// "asking tester for verification", "running pytest suite"). `peer` may be
// nil for self-contained transitions (Reasoning, ToolExecuting); set it to
// a filled AgentStateEvent with only PeerAgentType and/or PeerCorrelationID
// populated when the transition involves a peer.
//
// Correlation resolution: the payload's CorrelationID is taken from (in
// order) the provided `peer` override, the ActivitySession on ctx, or the
// stream metadata on ctx. Sessions are the canonical identity — bus
// handlers and async dispatchers open a session before calling this so
// the event routes cleanly even without a stream. When both are present
// the session wins so nested work attributes to the session it was
// opened under, not an inherited stream.
//
// Returns the underlying publish error so callers surface delivery failures
// the same way they do for any other stream event. No retries, no fallbacks
// — a failed state publish is an observable event, not a paper-over moment.
func PublishAgentState(
	bus guide.EventBus,
	channels *guide.AgentChannels,
	ctx context.Context,
	agentID string,
	agentType string,
	state guide.AgentActivityState,
	detail string,
	peer *guide.AgentStateEvent,
) error {
	if strings.TrimSpace(string(state)) == "" {
		return nil
	}
	transitionID := nextAgentStateTransitionID()
	payload := &guide.AgentStateEvent{
		State:        state,
		Detail:       strings.TrimSpace(detail),
		AgentID:      strings.TrimSpace(agentID),
		AgentType:    strings.TrimSpace(agentType),
		TransitionID: transitionID,
	}
	// Correlation precedence: session > stream metadata. Sessions are the
	// canonical identity for bus-handler emissions; stream metadata is the
	// fallback for in-stream emissions (where a session was never opened
	// because the stream lifecycle already carries the identity).
	if session := ActivitySessionFromContext(ctx); session != nil {
		payload.CorrelationID = session.CorrelationID
	} else if stream, ok := StreamMetadataFromContext(ctx); ok {
		payload.CorrelationID = stream.CorrelationID
	}
	// Stamp the parent correlation whenever ctx names one (guardian
	// approvals, pipeline challenges, any child-of-another-agent work).
	// The UI uses this to keep the parent's thinking indicator alive
	// while the child runs so the spinner doesn't orphan when the child
	// publishes under its own correlation.
	if parent := ParentCorrelationIDFromContext(ctx); parent != "" && parent != payload.CorrelationID {
		payload.ParentCorrelationID = parent
	}
	if peer != nil {
		payload.PeerAgentType = strings.TrimSpace(peer.PeerAgentType)
		payload.PeerCorrelationID = strings.TrimSpace(peer.PeerCorrelationID)
	}
	// Record the transition on the per-stream tracker so terminal telemetry
	// can detect producer gaps (a stream ended without ever announcing a
	// state). The tracker lives on ctx — agents get one automatically when
	// they call WithStreamContext, so this is a no-op outside a streaming
	// context.
	recordAgentStateTransition(ctx, state)
	logAgentStateTransitionToWAL(ctx, payload)
	event := &guide.StreamEvent{
		Type:      guide.StreamEventAgentState,
		Data:      payload,
		Timestamp: time.Now(),
	}
	// When ctx carries stream metadata (the in-stream path), route through
	// PublishStreamEvent so the standard lifecycle-gate telemetry and
	// metadata cloning apply. When ctx carries only an ActivitySession
	// (bus-handler path), route via the low-level bus publish so the event
	// is not gated on stream metadata the handler intentionally does not
	// own.
	if _, ok := StreamMetadataFromContext(ctx); ok {
		return PublishStreamEvent(bus, channels, ctx, agentID, event)
	}
	return publishAgentStateWithoutStream(bus, channels, ctx, agentID, payload, event)
}

// publishAgentStateWithoutStream emits an AgentStateEvent on the agent's
// response channel without requiring stream metadata on ctx. Used by bus
// handlers and async dispatchers that open an ActivitySession instead of
// a stream. The event rides the same response-channel the UI bridge is
// subscribed to, so the chat panel picks it up as a bridge row exactly
// like any other state event. A failed publish is logged (same canonical
// error-surfacing pattern as PublishStreamEvent) and returned to caller.
func publishAgentStateWithoutStream(
	bus guide.EventBus,
	channels *guide.AgentChannels,
	ctx context.Context,
	agentID string,
	payload *guide.AgentStateEvent,
	event *guide.StreamEvent,
) error {
	if bus == nil || channels == nil || payload == nil {
		return nil
	}
	if visibility, ok := streamEventVisibilityFromContext(ctx); ok {
		cloned := *event
		cloned.Visibility = visibility
		event = &cloned
	}
	stream := &guide.StreamResponse{
		CorrelationID:     payload.CorrelationID,
		RespondingAgentID: strings.TrimSpace(agentID),
		TargetAgentID:     "",
		Metadata:          nil,
		Event:             event,
	}
	msg := &guide.Message{
		ID:            fmt.Sprintf("%s_astate_%d", strings.TrimSpace(agentID), time.Now().UnixNano()),
		CorrelationID: payload.CorrelationID,
		Type:          guide.MessageTypeStream,
		Payload:       stream,
		SourceAgentID: strings.TrimSpace(agentID),
		Timestamp:     time.Now(),
	}
	if err := bus.Publish(channels.Responses, msg); err != nil {
		if lm := LogMetaFromContext(ctx); lm.EventLogger != nil {
			LogWarning(lm.EventLogger, lm.AgentID, lm.SessionID, lm.CorrID,
				"agent_state_publish_failed_no_stream", map[string]any{
					"state":          string(payload.State),
					"agent_id":       agentID,
					"correlation_id": payload.CorrelationID,
					"error":          err.Error(),
				})
		}
		return fmt.Errorf("publish agent state %s: %w", payload.State, err)
	}
	return nil
}

// logAgentStateTransitionToWAL mirrors every PublishAgentState call into
// the agent's WAL/JSONL log so the durable history reflects the same
// state machine the chat activity indicator drives. Without this hook
// the only authoritative state record is in transient bus traffic — once
// the process exits there is no way to reconstruct what each agent was
// doing during a session. No-op when ctx lacks a SessionEventLogger.
func logAgentStateTransitionToWAL(ctx context.Context, payload *guide.AgentStateEvent) {
	if payload == nil {
		return
	}
	lm := LogMetaFromContext(ctx)
	if lm.EventLogger == nil {
		return
	}
	data := map[string]any{
		"state":         string(payload.State),
		"agent_id":      payload.AgentID,
		"agent_type":    payload.AgentType,
		"transition_id": payload.TransitionID,
	}
	if payload.Detail != "" {
		data["detail"] = payload.Detail
	}
	if payload.PeerAgentType != "" {
		data["peer_agent_type"] = payload.PeerAgentType
	}
	if payload.PeerCorrelationID != "" {
		data["peer_correlation_id"] = payload.PeerCorrelationID
	}
	if payload.CorrelationID != "" {
		data["stream_correlation_id"] = payload.CorrelationID
	}
	LogAgentEvent(lm.EventLogger, agentlog.EventAgentStateTransition,
		lm.AgentID, lm.SessionID, lm.CorrID, "info", data)
}

// agentStateTransitionCounter is a process-wide monotonic counter used to
// order AgentStateEvent emissions deterministically on the receiving side.
// Per-correlation ordering is preserved as a side effect of the monotonic
// global (deliveries for a single correlation observe the counter in the
// same order the publisher issued them).
var agentStateTransitionCounter atomic.Int64

func nextAgentStateTransitionID() int64 {
	return agentStateTransitionCounter.Add(1)
}

// streamActivityState tracks the most recent agent-activity state emitted
// for a single stream so terminal telemetry can detect missing transitions.
// It is attached to the stream lifecycle object so every per-request stream
// has its own state track; concurrent requests do not share.
type streamActivityState struct {
	mu         sync.Mutex
	lastState  guide.AgentActivityState
	terminated bool
}

type streamActivityKey struct{}

func withStreamActivityState(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if existing, _ := ctx.Value(streamActivityKey{}).(*streamActivityState); existing != nil {
		return ctx
	}
	return context.WithValue(ctx, streamActivityKey{}, &streamActivityState{})
}

func streamActivityFromContext(ctx context.Context) *streamActivityState {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(streamActivityKey{}).(*streamActivityState)
	return s
}

// recordAgentStateTransition is called by PublishAgentState so the lifecycle
// tracker knows what the most recently emitted state was. Enables the
// terminal telemetry to detect "stream ended but we never saw any explicit
// state transition" producer gaps.
func recordAgentStateTransition(ctx context.Context, state guide.AgentActivityState) {
	s := streamActivityFromContext(ctx)
	if s == nil {
		return
	}
	s.mu.Lock()
	s.lastState = state
	if state == guide.AgentStateComplete || state == guide.AgentStateErrored {
		s.terminated = true
	}
	s.mu.Unlock()
}

// LogMissingTerminalAgentStateAtStreamTerminal emits a telemetry event when
// PublishStreamComplete or PublishStreamError fires without a prior terminal
// agent-activity state having been announced via PublishAgentState. Terminal
// states (complete / errored) are auto-emitted by the stream publishers, so
// the gap this detects is specifically producer-side: an agent that never
// emitted ANY state transition (not even receiving) during the full request
// lifetime, which indicates the request handler bypassed PublishStreamStart
// and similar canonical helpers. No fallback happens — we log and continue.
func LogMissingTerminalAgentStateAtStreamTerminal(ctx context.Context, agentID, reason string) {
	s := streamActivityFromContext(ctx)
	if s == nil {
		return
	}
	s.mu.Lock()
	seenAny := s.lastState != ""
	already := s.terminated
	s.mu.Unlock()
	if seenAny || already {
		return
	}
	lm := LogMetaFromContext(ctx)
	if lm.EventLogger == nil {
		return
	}
	LogInfo(lm.EventLogger, lm.AgentID, lm.SessionID, lm.CorrID,
		"agent_state_missing_at_stream_terminal", map[string]any{
			"agent_id": strings.TrimSpace(agentID),
			"reason":   strings.TrimSpace(reason),
		})
}

func publishStreamProgress(
	bus guide.EventBus,
	channels *guide.AgentChannels,
	ctx context.Context,
	agentID, message string,
	toolDerived bool,
) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	return PublishStreamEvent(bus, channels, ctx, agentID, &guide.StreamEvent{
		Type: guide.StreamEventProgress,
		Data: &guide.ProgressData{
			Message:     message,
			ToolDerived: toolDerived,
		},
		Timestamp: time.Now(),
	})
}

// IntermediateToolTurnText returns assistant text worth surfacing immediately
// for a tool-using turn. Final text-only turns continue to flow through the
// normal complete event path.
func IntermediateToolTurnText(resp *providers.Response) string {
	return IntermediateToolTurnTextWithContext(context.Background(), resp)
}

func IntermediateToolTurnTextWithContext(ctx context.Context, resp *providers.Response) string {
	if resp == nil || len(resp.ToolCalls) == 0 {
		return ""
	}
	agentType := progressNarrationAgentType(ctx)
	content := strings.TrimSpace(resp.Content)
	if content == "" && !suppressIntermediateThinkingNarration(agentType) {
		content = summarizeIntermediateThinking(resp.Thinking)
	}
	if content == "" {
		content = summarizeIntermediateToolCalls(agentType, TaskExecutionContractFromContext(ctx), resp.ToolCalls)
	}
	if content == "" && suppressIntermediateThinkingNarration(agentType) {
		content = summarizeIntermediateThinking(resp.Thinking)
	}
	if content == "" {
		return ""
	}
	return content + "\n\n"
}

func MarkResponseStreamedText(resp *providers.Response) {
	if resp == nil {
		return
	}
	if resp.ProviderMetadata == nil {
		resp.ProviderMetadata = make(map[string]any)
	}
	resp.ProviderMetadata[streamedTextMetadataKey] = true
}

func ResponseStreamedText(resp *providers.Response) bool {
	if resp == nil || resp.ProviderMetadata == nil {
		return false
	}
	value, _ := resp.ProviderMetadata[streamedTextMetadataKey].(bool)
	return value
}

func summarizeIntermediateThinking(thinking string) string {
	thinking = strings.Join(strings.Fields(strings.TrimSpace(thinking)), " ")
	if thinking == "" {
		return ""
	}
	const maxLen = 320
	if len(thinking) <= maxLen {
		return thinking
	}
	cut := strings.LastIndex(thinking[:maxLen], " ")
	if cut < 0 {
		cut = maxLen
	}
	return strings.TrimSpace(thinking[:cut]) + "..."
}

func summarizeIntermediateToolCalls(agentType string, contract *TaskExecutionContract, calls []providers.ToolCall) string {
	if len(calls) == 0 {
		return ""
	}
	actions := summarizeIntermediateActions(agentType, contract, calls)
	if len(actions) > 0 {
		return joinNarratedActions(actions)
	}
	names := make([]string, 0, len(calls))
	seen := make(map[string]struct{}, len(calls))
	for _, call := range calls {
		name := humanizeToolName(call.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	if len(names) == 0 {
		return ""
	}
	slices.Sort(names)
	switch len(names) {
	case 1:
		return "Working through this with " + names[0] + "."
	case 2:
		return "Working through this with " + names[0] + " and " + names[1] + "."
	default:
		head := strings.Join(names[:len(names)-1], ", ")
		return "Working through this with " + head + ", and " + names[len(names)-1] + "."
	}
}

func summarizeIntermediateActions(
	agentType string,
	contract *TaskExecutionContract,
	calls []providers.ToolCall,
) []string {
	actions := make([]string, 0, len(calls))
	seen := make(map[string]struct{}, len(calls))
	for _, call := range calls {
		action := intermediateToolAction(agentType, contract, call.Name)
		if action == "" {
			continue
		}
		if _, ok := seen[action]; ok {
			continue
		}
		seen[action] = struct{}{}
		actions = append(actions, action)
	}
	return actions
}

func intermediateToolAction(agentType string, contract *TaskExecutionContract, toolName string) string {
	switch strings.TrimSpace(toolName) {
	case "research_topic":
		return "researching the topic"
	case "search_skills":
		return "checking the available skills"
	case "check_inspector_gate":
		return "checking the inspector gate before test work begins"
	case "coord_query_view":
		return "reviewing coordination state and prior task artifacts"
	case "coord_claim_scope":
		return "claiming " + intermediateScopePhrase(agentType)
	case "coord_release_scope":
		return "releasing the claimed surface"
	case "read_workspace_file":
		return readWorkspaceAction(agentType)
	case "inspect_workspace_state":
		return inspectWorkspaceStateAction(agentType)
	case "summarize_workspace_state":
		return "summarizing the workspace state across the active layers"
	case "detect_test_harness":
		return "detecting the active test harness and default test surface"
	case "analyze_risk":
		return "analyzing likely implementation risks against the requested behavior"
	case "plan_tests":
		return "turning the risks and criteria into executable test coverage"
	case "prepare_test_harness":
		return "preparing the test harness and any required boilerplate"
	case "prepare_pipeline_write_context":
		return preparePipelineWriteContextAction(agentType, contract)
	case "write_test":
		return "writing the requested tests into the task workspace"
	case "run_test_suite":
		return "running the relevant test suite against the current task state"
	case "define_criteria":
		return "defining explicit success criteria from the task contract"
	case "get_validation_status":
		return "confirming the current validation status"
	case "coord_publish_artifact":
		return "publishing " + intermediateArtifactPhrase(agentType, contract)
	case "validate_criteria":
		return "validating the implementation against the defined criteria"
	case "grade_task_quality":
		return "grading the implementation quality across the active quality gates"
	default:
		return ""
	}
}

func intermediateScopePhrase(agentType string) string {
	switch strings.TrimSpace(agentType) {
	case "tester-pipeline":
		return "the test surface for this task"
	case "inspector-pipeline":
		return "the investigation surface for this task"
	case "engineer":
		return "the implementation surface for this task"
	case "designer":
		return "the design surface for this task"
	default:
		return "the relevant task surface"
	}
}

func intermediateArtifactPhrase(agentType string, contract *TaskExecutionContract) string {
	if contract != nil && !contract.PreImplementation {
		return "the validation findings artifact for downstream review"
	}
	switch strings.TrimSpace(agentType) {
	case "inspector-pipeline":
		return "the handoff artifact for downstream implementation"
	case "tester-pipeline":
		return "the test findings artifact for downstream review"
	case "engineer":
		return "the implementation artifact for downstream review"
	case "designer":
		return "the design artifact for downstream review"
	default:
		return "the artifact"
	}
}

func readWorkspaceAction(agentType string) string {
	switch strings.TrimSpace(agentType) {
	case "inspector-pipeline":
		return "reading the relevant workspace files to compare the requested contract with the current implementation"
	case "tester-pipeline":
		return "reading the relevant workspace files to compare the requested behavior with the current implementation"
	case "engineer":
		return "reading the relevant workspace files before applying the requested changes"
	case "designer":
		return "reading the relevant workspace files before applying the requested design changes"
	default:
		return "inspecting the relevant workspace files"
	}
}

func inspectWorkspaceStateAction(agentType string) string {
	switch strings.TrimSpace(agentType) {
	case "inspector-pipeline":
		return "inspecting the workspace state across disk and overlay layers"
	case "tester-pipeline":
		return "inspecting the workspace state to understand the current implementation surface"
	default:
		return "inspecting the workspace state"
	}
}

func preparePipelineWriteContextAction(agentType string, contract *TaskExecutionContract) string {
	switch strings.TrimSpace(agentType) {
	case "tester-pipeline":
		return "preparing a safe write context for the planned test artifacts"
	case "inspector-pipeline":
		if contract != nil && contract.PreImplementation {
			return "preparing a safe write context for the inspection handoff artifact"
		}
		if contract != nil && !contract.PreImplementation {
			return "preparing a safe write context for the validation artifact"
		}
		return "preparing a safe write context for the inspection artifact"
	case "engineer":
		return "preparing a safe write context for the requested implementation changes"
	case "designer":
		return "preparing a safe write context for the requested design changes"
	default:
		return "preparing the workspace write context"
	}
}

func joinNarratedActions(actions []string) string {
	if len(actions) == 0 {
		return ""
	}
	switch len(actions) {
	case 1:
		return sentenceCase(actions[0]) + "."
	case 2:
		return sentenceCase(actions[0] + " and " + actions[1] + ".")
	default:
		head := strings.Join(actions[:len(actions)-1], ", ")
		return sentenceCase(head + ", and " + actions[len(actions)-1] + ".")
	}
}

func sentenceCase(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

func humanizeToolName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	name = strings.ReplaceAll(name, "_", " ")
	return strings.Join(strings.Fields(name), " ")
}

// PublishIntermediateToolTurn emits progress narration for tool-using turns so
// the user sees meaningful status before the loop reaches its final answer.
// This uses progress events instead of data chunks so intermediate narration
// does not suppress the final route response. Returns the underlying publish
// error for the agent's loop to surface; nil when no narration was emitted.
func PublishIntermediateToolTurn(
	bus guide.EventBus,
	channels *guide.AgentChannels,
	ctx context.Context,
	agentID string,
	resp *providers.Response,
) error {
	if ResponseStreamedText(resp) {
		return nil
	}
	if text := IntermediateToolTurnTextWithContext(ctx, resp); text != "" {
		return publishStreamProgress(bus, channels, ctx, agentID, text, true)
	}
	return nil
}

func progressNarrationAgentType(ctx context.Context) string {
	stream, ok := StreamMetadataFromContext(ctx)
	if !ok || len(stream.Metadata) == 0 {
		return ""
	}
	value, _ := stream.Metadata["agent_type"].(string)
	return strings.TrimSpace(value)
}

func suppressIntermediateThinkingNarration(agentType string) bool {
	switch strings.TrimSpace(agentType) {
	case "tester", "tester-pipeline":
		return true
	default:
		return false
	}
}

// PublishStreamComplete emits a stream completion event. Before publishing it
// snapshots any tool calls whose Start has been emitted but whose Complete
// has not, and logs each as a greppable orphan-detection event. The
// (A) lifecycle bypass means a Complete may still arrive after this fires —
// the log is informational ("this call was outstanding when the stream
// terminated"), not a permanent assertion of loss.
//
// The canonical `complete` agent-activity state is announced via
// PublishAgentState immediately before the complete event reaches the bus,
// so consumers observing the activity channel see the terminal transition
// in the same order as the stream terminal. Missing prior intermediate
// transitions (no `reasoning` / `composing_response` between `receiving` and
// `complete`) are logged as producer gaps by the terminal-state telemetry.
// Returns the underlying publish error so the agent's loop can surface a
// final-event delivery failure to the user.
func PublishStreamComplete(
	bus guide.EventBus,
	channels *guide.AgentChannels,
	ctx context.Context,
	agentID, text string,
	usage *guide.StreamUsage,
) error {
	LogInFlightToolCallsAtTerminal(ctx, "stream_complete")
	LogMissingTerminalAgentStateAtStreamTerminal(ctx, agentID, "stream_complete")
	agentType := agentTypeFromStreamMetadata(ctx, agentID)
	if err := PublishAgentState(bus, channels, ctx, agentID, agentType,
		guide.AgentStateComplete, "", nil); err != nil {
		// Log but continue — the stream complete event is the user-visible
		// terminal and must still fire even if the state channel failed.
		if lm := LogMetaFromContext(ctx); lm.EventLogger != nil {
			LogWarning(lm.EventLogger, lm.AgentID, lm.SessionID, lm.CorrID,
				"agent_state_complete_publish_failed", map[string]any{
					"agent_id":   agentID,
					"agent_type": agentType,
					"error":      err.Error(),
				})
		}
	}
	return PublishStreamEvent(bus, channels, ctx, agentID, &guide.StreamEvent{
		Type:      guide.StreamEventComplete,
		Text:      text,
		Usage:     usage,
		Timestamp: time.Now(),
	})
}

// PublishStreamError emits a stream error event. As with PublishStreamComplete
// it snapshots in-flight tool calls first so a stuck call is observable in
// the log even when the agent terminates abnormally. The canonical `errored`
// agent-activity state is announced before the error event so the activity
// channel reflects the failure in the same order as the stream terminal.
// Returns the underlying publish error so the agent's loop can surface a
// final-event delivery failure (the user is then unaware the stream errored —
// a serious bug we must propagate, not silently drop).
func PublishStreamError(bus guide.EventBus, channels *guide.AgentChannels, ctx context.Context, agentID string, err error) error {
	if err == nil {
		return nil
	}
	LogInFlightToolCallsAtTerminal(ctx, "stream_error")
	LogMissingTerminalAgentStateAtStreamTerminal(ctx, agentID, "stream_error")
	agentType := agentTypeFromStreamMetadata(ctx, agentID)
	if stateErr := PublishAgentState(bus, channels, ctx, agentID, agentType,
		guide.AgentStateErrored, err.Error(), nil); stateErr != nil {
		if lm := LogMetaFromContext(ctx); lm.EventLogger != nil {
			LogWarning(lm.EventLogger, lm.AgentID, lm.SessionID, lm.CorrID,
				"agent_state_errored_publish_failed", map[string]any{
					"agent_id":         agentID,
					"agent_type":       agentType,
					"underlying_error": err.Error(),
					"publish_error":    stateErr.Error(),
				})
		}
	}
	return PublishStreamEvent(bus, channels, ctx, agentID, &guide.StreamEvent{
		Type:      guide.StreamEventError,
		Data:      map[string]string{"error": err.Error()},
		Timestamp: time.Now(),
	})
}
