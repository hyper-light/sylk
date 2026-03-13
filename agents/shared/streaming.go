package shared

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/providers"
)

const streamedTextMetadataKey = "shared_streamed_text"

// StreamContext carries streaming correlation data through context.
type StreamContext struct {
	CorrelationID string
	SourceAgentID string
	Metadata      map[string]any
}

type streamContextKey struct{}

// WithStreamContext attaches streaming metadata to a context.
func WithStreamContext(ctx context.Context, correlationID, sourceAgentID string) context.Context {
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

// StreamMetadataFromContext extracts streaming metadata from a context.
func StreamMetadataFromContext(ctx context.Context) (StreamContext, bool) {
	metadata, ok := ctx.Value(streamContextKey{}).(StreamContext)
	if !ok || metadata.CorrelationID == "" {
		return StreamContext{}, false
	}
	return metadata, true
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

	_ = bus.Publish(channels.Responses, msg)
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
	content := strings.TrimSpace(resp.Content)
	if content == "" {
		content = summarizeIntermediateThinking(resp.Thinking)
	}
	if content == "" {
		content = summarizeIntermediateToolCalls(progressNarrationAgentType(ctx), TaskExecutionContractFromContext(ctx), resp.ToolCalls)
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
	if contract != nil && contract.RequiresDeliverable(TaskDeliverableCriteriaEvaluation) {
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
		if contract != nil && contract.RequiresDeliverable(TaskDeliverableHandoffContract) {
			return "preparing a safe write context for the inspection handoff artifact"
		}
		if contract != nil && contract.RequiresDeliverable(TaskDeliverableValidationReport) {
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

// PublishIntermediateToolTurn emits assistant text for tool-using turns so the
// user sees progress before the loop reaches its final answer.
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
		PublishStreamChunk(bus, channels, ctx, agentID, text)
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

// PublishStreamComplete emits a stream completion event.
func PublishStreamComplete(
	bus guide.EventBus,
	channels *guide.AgentChannels,
	ctx context.Context,
	agentID, text string,
	usage *guide.StreamUsage,
) {
	PublishStreamEvent(bus, channels, ctx, agentID, &guide.StreamEvent{
		Type:      guide.StreamEventComplete,
		Text:      text,
		Usage:     usage,
		Timestamp: time.Now(),
	})
}

// PublishStreamError emits a stream error event.
func PublishStreamError(bus guide.EventBus, channels *guide.AgentChannels, ctx context.Context, agentID string, err error) {
	if err == nil {
		return
	}
	PublishStreamEvent(bus, channels, ctx, agentID, &guide.StreamEvent{
		Type:      guide.StreamEventError,
		Data:      map[string]string{"error": err.Error()},
		Timestamp: time.Now(),
	})
}
