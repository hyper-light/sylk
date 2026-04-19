package shared

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
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

// WithStreamContext attaches streaming metadata to a context.
func WithStreamContext(ctx context.Context, correlationID, sourceAgentID string) context.Context {
	ctx = withStreamLifecycle(ctx)
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

func publishWithStreamLifecycle(ctx context.Context, eventType guide.StreamEventType, publish func()) bool {
	lifecycle := streamLifecycleFromContext(ctx)
	delivered, lateBypass := publishWithLifecycleState(lifecycle, eventType, publish)
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
	return delivered
}

// publishWithLifecycleState gates a single stream-event publish against the
// stream's terminal lifecycle. Returns (delivered, lateBypass):
//   - delivered = true means publish was invoked.
//   - lateBypass = true means publish was invoked despite terminalStarted being
//     set, because the event type owns its own out-of-band lifecycle (currently
//     only StreamEventToolCall). Callers can use this to log telemetry.
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
func publishWithLifecycleState(lifecycle *streamLifecycleState, eventType guide.StreamEventType, publish func()) (delivered bool, lateBypass bool) {
	if publish == nil {
		return false, false
	}
	if lifecycle == nil {
		publish()
		return true, false
	}

	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()

	switch eventType {
	case guide.StreamEventComplete:
		if lifecycle.completed {
			return false, false
		}
		lifecycle.terminalStarted = true
		lifecycle.completed = true
		publish()
		return true, false
	case guide.StreamEventError:
		if lifecycle.completed {
			return false, false
		}
		lifecycle.terminalStarted = true
		publish()
		return true, false
	case guide.StreamEventToolCall:
		// Bypass the terminal gate for tool-call events. They are out-of-band
		// metadata, paired by toolCallEventKey, and dropping them after
		// terminalStarted produces stuck-pending rows the UI cannot recover
		// from. Both Start and Complete are bypassed symmetrically so that
		// callback-driven synthetic branches (guardian approval responses,
		// archivalist consults) can still open and close their rows after
		// the parent stream finalizes.
		late := lifecycle.terminalStarted
		publish()
		return true, late
	default:
		if lifecycle.terminalStarted {
			return false, false
		}
		publish()
		return true, false
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
func PublishStreamEvent(
	bus guide.EventBus,
	channels *guide.AgentChannels,
	ctx context.Context,
	agentID string,
	event *guide.StreamEvent,
) {
	metadata, ok := StreamMetadataFromContext(ctx)
	if !ok || bus == nil || channels == nil || event == nil {
		return
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

	publishWithStreamLifecycle(ctx, event.Type, func() {
		_ = bus.Publish(channels.Responses, msg)
	})
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

// PublishStreamStart emits a stream start event.
func PublishStreamStart(bus guide.EventBus, channels *guide.AgentChannels, ctx context.Context, agentID string) {
	PublishStreamEvent(bus, channels, ctx, agentID, &guide.StreamEvent{
		Type:      guide.StreamEventStart,
		Timestamp: time.Now(),
	})
}

// PublishStreamChunk emits a text data chunk.
func PublishStreamChunk(bus guide.EventBus, channels *guide.AgentChannels, ctx context.Context, agentID, text string) {
	PublishStreamEvent(bus, channels, ctx, agentID, &guide.StreamEvent{
		Type:      guide.StreamEventData,
		Text:      text,
		Timestamp: time.Now(),
	})
}

// PublishStreamProgress emits a progress update tied to the current stream.
func PublishStreamProgress(bus guide.EventBus, channels *guide.AgentChannels, ctx context.Context, agentID, message string) {
	publishStreamProgress(bus, channels, ctx, agentID, message, false)
}

func publishStreamProgress(
	bus guide.EventBus,
	channels *guide.AgentChannels,
	ctx context.Context,
	agentID, message string,
	toolDerived bool,
) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	PublishStreamEvent(bus, channels, ctx, agentID, &guide.StreamEvent{
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
// does not suppress the final route response.
func PublishIntermediateToolTurn(
	bus guide.EventBus,
	channels *guide.AgentChannels,
	ctx context.Context,
	agentID string,
	resp *providers.Response,
) {
	if ResponseStreamedText(resp) {
		return
	}
	if text := IntermediateToolTurnTextWithContext(ctx, resp); text != "" {
		publishStreamProgress(bus, channels, ctx, agentID, text, true)
	}
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
func PublishStreamComplete(
	bus guide.EventBus,
	channels *guide.AgentChannels,
	ctx context.Context,
	agentID, text string,
	usage *guide.StreamUsage,
) {
	LogInFlightToolCallsAtTerminal(ctx, "stream_complete")
	PublishStreamEvent(bus, channels, ctx, agentID, &guide.StreamEvent{
		Type:      guide.StreamEventComplete,
		Text:      text,
		Usage:     usage,
		Timestamp: time.Now(),
	})
}

// PublishStreamError emits a stream error event. As with PublishStreamComplete
// it snapshots in-flight tool calls first so a stuck call is observable in
// the log even when the agent terminates abnormally.
func PublishStreamError(bus guide.EventBus, channels *guide.AgentChannels, ctx context.Context, agentID string, err error) {
	if err == nil {
		return
	}
	LogInFlightToolCallsAtTerminal(ctx, "stream_error")
	PublishStreamEvent(bus, channels, ctx, agentID, &guide.StreamEvent{
		Type:      guide.StreamEventError,
		Data:      map[string]string{"error": err.Error()},
		Timestamp: time.Now(),
	})
}
