package architect

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/messaging"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/versioning"
)

type architectStreamContext struct {
	CorrelationID string
	SourceAgentID string
}

type architectStreamContextKey struct{}
type architectEarlyUsageEmitter func(inputTokens int)
type architectEarlyUsageKey struct{}
type architectUsageAccumulatorKey struct{}

// streamRetryResetEmitter is called when the provider retries a stream,
// signaling the UI to reset its accumulator before replayed content arrives.
type streamRetryResetEmitter func()
type streamRetryResetEmitterKey struct{}

// architectSessionIDKey carries the active session ID through the context so
// skill handlers (e.g. start_planning) can inherit it without relying on the
// LLM to echo it back as a tool parameter.
type architectSessionIDKey struct{}
type architectConversationContextKey struct{}

type architectConversationContext struct {
	UserQuery           string
	ConversationHistory []guide.ConversationTurn
}

func withArchitectSessionID(ctx context.Context, sessionID string) context.Context {
	sessionID = strings.TrimSpace(sessionID)
	ctx = versioning.WithSessionID(ctx, sessionID)
	return context.WithValue(ctx, architectSessionIDKey{}, sessionID)
}

func architectSessionIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(architectSessionIDKey{}).(string)
	return v
}

func withArchitectConversationContext(
	ctx context.Context,
	userQuery string,
	history []guide.ConversationTurn,
) context.Context {
	payload := architectConversationContext{
		UserQuery:           strings.TrimSpace(userQuery),
		ConversationHistory: append([]guide.ConversationTurn(nil), history...),
	}
	return context.WithValue(ctx, architectConversationContextKey{}, payload)
}

func architectConversationContextFromContext(ctx context.Context) (architectConversationContext, bool) {
	payload, ok := ctx.Value(architectConversationContextKey{}).(architectConversationContext)
	return payload, ok
}

func withStreamRetryResetEmitter(ctx context.Context, emitter streamRetryResetEmitter) context.Context {
	if emitter == nil {
		return ctx
	}
	return context.WithValue(ctx, streamRetryResetEmitterKey{}, emitter)
}

func emitStreamRetryReset(ctx context.Context) {
	if ctx == nil {
		return
	}
	emitter, ok := ctx.Value(streamRetryResetEmitterKey{}).(streamRetryResetEmitter)
	if !ok || emitter == nil {
		return
	}
	emitter()
}

// architectUsageAccumulator sums real token counts from multiple LLM calls
// within a single Architect request. Thread-safe for concurrent sub-calls.
type architectUsageAccumulator struct {
	mu              sync.Mutex
	inputTotal      int
	outputTotal     int
	reasoningTotal  int
	cacheReadTotal  int
	cacheWriteTotal int
}

func (a *architectUsageAccumulator) Add(usage *providers.Usage) {
	if usage == nil {
		return
	}
	a.mu.Lock()
	a.inputTotal += usage.InputTokens
	a.outputTotal += usage.OutputTokens
	a.reasoningTotal += usage.ReasoningTokens
	a.cacheReadTotal += usage.CacheReadTokens
	a.cacheWriteTotal += usage.CacheWriteTokens
	a.mu.Unlock()
}

func (a *architectUsageAccumulator) Total() *guide.StreamUsage {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inputTotal == 0 && a.outputTotal == 0 {
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

func withArchitectUsageAccumulator(ctx context.Context) (context.Context, *architectUsageAccumulator) {
	acc := &architectUsageAccumulator{}
	return context.WithValue(ctx, architectUsageAccumulatorKey{}, acc), acc
}

func accumulateArchitectUsage(ctx context.Context, usage *providers.Usage) {
	if usage == nil || ctx == nil {
		return
	}
	acc, ok := ctx.Value(architectUsageAccumulatorKey{}).(*architectUsageAccumulator)
	if !ok || acc == nil {
		return
	}
	acc.Add(usage)
}

type streamProgressSpec struct {
	Current int
	Total   int
	Message string
	UIState events.AgentUIState
}

var planStatusProgress = map[PlanStatus]streamProgressSpec{
	PlanStatusPending:       {Current: 0, Total: 6, Message: "Framing the plan...", UIState: events.AgentUIStatePlanning},
	PlanStatusAnalyzing:     {Current: 1, Total: 6, Message: "Analyzing requirements...", UIState: events.AgentUIStatePlanning},
	PlanStatusConsulting:    {Current: 2, Total: 6, Message: "Consulting available knowledge agents...", UIState: events.AgentUIStatePlanning},
	PlanStatusClarifying:    {Current: 2, Total: 6, Message: "Waiting for clarification...", UIState: events.AgentUIStatePlanning},
	PlanStatusDesigning:     {Current: 3, Total: 6, Message: "Designing architecture options...", UIState: events.AgentUIStatePlanning},
	PlanStatusGenerating:    {Current: 4, Total: 6, Message: "Generating an actionable task breakdown...", UIState: events.AgentUIStatePlanning},
	PlanStatusOrchestrating: {Current: 5, Total: 6, Message: "Assembling workflow and dependencies...", UIState: events.AgentUIStatePlanning},
	PlanStatusReady:         {Current: 6, Total: 6, Message: "Plan is ready for your review.", UIState: events.AgentUIStatePlanning},
	PlanStatusExecuting:     {Current: 6, Total: 6, Message: "Handing off to orchestration...", UIState: events.AgentUIStatePlanAccepted},
	PlanStatusCompleted:     {Current: 6, Total: 6, Message: "Planning complete.", UIState: events.AgentUIStatePlanAccepted},
	PlanStatusFailed:        {Current: 6, Total: 6, Message: "Planning failed.", UIState: events.AgentUIStatePlanFailed},
}

func activityEventTypeForPlanStatus(status PlanStatus) events.EventType {
	switch status {
	case PlanStatusPending, PlanStatusAnalyzing, PlanStatusConsulting:
		return events.EventTypeAgentDecision
	case PlanStatusFailed:
		return events.EventTypeAgentError
	default:
		return events.EventTypeAgentAction
	}
}

func withArchitectEarlyUsageEmitter(ctx context.Context, emit architectEarlyUsageEmitter) context.Context {
	if emit == nil {
		return ctx
	}
	return context.WithValue(ctx, architectEarlyUsageKey{}, emit)
}

func emitArchitectEarlyUsage(ctx context.Context, inputTokens int) {
	if ctx == nil || inputTokens <= 0 {
		return
	}
	emit, ok := ctx.Value(architectEarlyUsageKey{}).(architectEarlyUsageEmitter)
	if !ok || emit == nil {
		return
	}
	emit(inputTokens)
}

func withArchitectStreamContext(ctx context.Context, correlationID, sourceAgentID string) context.Context {
	metadata := architectStreamContext{
		CorrelationID: strings.TrimSpace(correlationID),
		SourceAgentID: strings.TrimSpace(sourceAgentID),
	}
	return context.WithValue(ctx, architectStreamContextKey{}, metadata)
}

func architectStreamMetadataFromContext(ctx context.Context) (architectStreamContext, bool) {
	metadata, ok := ctx.Value(architectStreamContextKey{}).(architectStreamContext)
	if !ok {
		return architectStreamContext{}, false
	}
	if metadata.CorrelationID == "" {
		return architectStreamContext{}, false
	}
	return metadata, true
}

func correlationIDFromContext(ctx context.Context) string {
	if meta, ok := architectStreamMetadataFromContext(ctx); ok {
		return meta.CorrelationID
	}
	return ""
}

func (a *Architect) publishPlanStreamStart(ctx context.Context) {
	a.publishPlanStreamEvent(ctx, &guide.StreamEvent{
		Type:      guide.StreamEventStart,
		Timestamp: time.Now(),
	})
}

func (a *Architect) publishPlanStreamProgress(ctx context.Context, status PlanStatus) {
	spec, ok := planStatusProgress[status]
	if !ok {
		return
	}
	a.publishActivity(activityEventTypeForPlanStatus(status), spec.Message)
	a.publishPlanStreamEvent(ctx, &guide.StreamEvent{
		Type: guide.StreamEventProgress,
		Data: &guide.ProgressData{
			Current: spec.Current,
			Total:   spec.Total,
			Percent: progressPercent(spec.Current, spec.Total),
			Message: spec.Message,
			UIState: spec.UIState,
		},
		Timestamp: time.Now(),
	})
}

func (a *Architect) publishPlanThought(ctx context.Context, stage string, thought string) {
	message := formatPlanThoughtMessage(stage, thought)
	if message == "" {
		return
	}
	a.publishPlanStreamEvent(ctx, &guide.StreamEvent{
		Type: guide.StreamEventProgress,
		Data: &guide.ProgressData{
			Message: message,
			UIState: events.AgentUIStatePlanning,
		},
		Timestamp: time.Now(),
	})
}

func formatPlanThoughtMessage(stage string, thought string) string {
	thought = strings.TrimSpace(thought)
	if thought == "" {
		return ""
	}
	if !hasTerminalThoughtBoundary(thought) {
		thought += "..."
	}
	stage = humanizePlanThoughtStage(stage)
	if stage == "" {
		return thought
	}
	return stage + ": " + thought
}

func hasTerminalThoughtBoundary(thought string) bool {
	thought = strings.TrimSpace(thought)
	if thought == "" {
		return false
	}
	for i := len(thought) - 1; i >= 0; i-- {
		switch thought[i] {
		case '.', '!', '?':
			return true
		case ' ', '\t', '\n', '\r':
			continue
		default:
			return false
		}
	}
	return false
}

func humanizePlanThoughtStage(stage string) string {
	switch strings.TrimSpace(stage) {
	case "requirements":
		return "Requirements"
	case "design":
		return "Design"
	case "tasks":
		return "Tasks"
	default:
		return ""
	}
}

func progressPercent(current, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(current) / float64(total) * 100
}

func (a *Architect) publishPlanStreamChunk(ctx context.Context, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	a.publishPlanStreamEvent(ctx, &guide.StreamEvent{
		Type:      guide.StreamEventData,
		Text:      text,
		Timestamp: time.Now(),
	})
}

func (a *Architect) publishPlanStreamEarlyUsage(ctx context.Context, inputTokens int) {
	if inputTokens <= 0 {
		return
	}
	a.publishPlanStreamEvent(ctx, &guide.StreamEvent{
		Type:      guide.StreamEventData,
		Usage:     &guide.StreamUsage{InputTokens: inputTokens},
		Timestamp: time.Now(),
	})
}

func (a *Architect) publishPlanStreamComplete(ctx context.Context, userResponse string, usage *guide.StreamUsage, directive *guide.ResponseDirective) {
	event := &guide.StreamEvent{
		Type:      guide.StreamEventComplete,
		Text:      strings.TrimSpace(userResponse),
		Usage:     usage,
		Directive: directive,
		Timestamp: time.Now(),
	}
	a.publishPlanStreamEvent(ctx, event)
}

// publishHandoffReroute emits a StreamEventReroute so the TUI switches
// the engaged agent indicator from "architect" to the handoff target.
// originalCID is the architect's stream CID (to be cleared in the TUI).
// newCID is the target agent's request CID (for the TUI to track).
func (a *Architect) publishHandoffReroute(ctx context.Context, toAgentID, reason, originalCID, newCID string) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "agent handoff"
	}
	a.publishPlanStreamEvent(ctx, &guide.StreamEvent{
		Type: guide.StreamEventReroute,
		Data: map[string]string{
			"from_agent":              "architect",
			"to_agent":                toAgentID,
			"reason":                  reason,
			"original_correlation_id": originalCID,
			"new_correlation_id":      newCID,
		},
		Timestamp: time.Now(),
	})
}

// extractResponseDirective extracts a ResponseDirective from a planning or
// conversation result, for inclusion on the StreamEventComplete event.
// Unwraps *ArchitectResponse to reach the inner result type.
func extractResponseDirective(data any) *guide.ResponseDirective {
	inner := unwrapArchitectResult(data)
	switch v := inner.(type) {
	case *DesignPlan:
		if v != nil {
			return v.ReadyDirective()
		}
	case *ConversationResult:
		if v != nil {
			return v.Directive
		}
	}
	return nil
}

func directivePhaseStr(d *guide.ResponseDirective) string {
	if d == nil {
		return "<nil>"
	}
	return string(d.Phase)
}

func directiveAgentStr(d *guide.ResponseDirective) string {
	if d == nil {
		return "<nil>"
	}
	return d.AgentID
}

func (a *Architect) publishPlanStreamError(ctx context.Context, err error) {
	if err == nil {
		return
	}
	a.publishPlanStreamEvent(ctx, &guide.StreamEvent{
		Type:      guide.StreamEventError,
		Data:      map[string]string{"error": err.Error()},
		Timestamp: time.Now(),
	})
}

// publishPlanSnapshot emits a full plan state snapshot as a StreamEventData
// event so the TUI can update the plan viewer panel.
func (a *Architect) publishPlanSnapshot(ctx context.Context, plan *DesignPlan) {
	if plan == nil {
		return
	}
	componentCount := 0
	if plan.Architecture != nil {
		componentCount = len(plan.Architecture.Components)
	}
	a.logInfo("publishPlanSnapshot",
		"plan_id", plan.ID,
		"status", plan.Status.String(),
		"tasks", len(plan.Tasks),
		"components", componentCount)
	snapshot := buildPlanSnapshotData(plan)
	a.publishPlanStreamEvent(ctx, &guide.StreamEvent{
		Type:      guide.StreamEventData,
		Data:      snapshot,
		Timestamp: time.Now(),
	})
}

// buildPlanSnapshotData creates a serializable map from a DesignPlan
// suitable for bridging to the TUI as a PlanUpdateMsg.
func buildPlanSnapshotData(plan *DesignPlan) map[string]any {
	tasks := make([]map[string]any, 0, len(plan.Tasks))
	for _, t := range plan.Tasks {
		task := map[string]any{
			"ID":        t.ID,
			"Slug":      taskSlugForTask(t, 0),
			"Name":      t.Name,
			"AgentType": t.AgentType,
			"Status":    t.Status.String(),
		}
		if t.Description != "" {
			task["Description"] = t.Description
		}
		if len(t.Dependencies) > 0 {
			task["Dependencies"] = t.Dependencies
		}
		if len(t.AcceptanceCriteria) > 0 {
			task["AcceptanceCriteria"] = t.AcceptanceCriteria
		}
		if t.ImplementationGuide != "" {
			task["ImplementationGuide"] = t.ImplementationGuide
		}
		if len(t.Guidelines) > 0 {
			task["Guidelines"] = t.Guidelines
		}
		if len(t.Examples) > 0 {
			task["Examples"] = t.Examples
		}
		if len(t.AffectedFiles) > 0 {
			task["AffectedFiles"] = t.AffectedFiles
		}
		if t.Result != nil {
			task["TokensIn"] = t.Result.Metrics.TokensUsed
			task["Duration"] = t.Result.Metrics.Duration.String()
			if t.Result.Error != "" {
				task["StatusMessage"] = t.Result.Error
			}
		}
		tasks = append(tasks, task)
	}

	var layers [][]string
	if plan.Workflow != nil {
		layers = plan.Workflow.ExecutionLayers
	}

	return map[string]any{
		"PlanID":          plan.ID,
		"Status":          plan.Status.String(),
		"Tasks":           tasks,
		"ExecutionLayers": layers,
		"StartTime":       plan.CreatedAt,
	}
}

func (a *Architect) publishPlanStreamEvent(ctx context.Context, event *guide.StreamEvent) {
	if event == nil || a == nil || a.bus == nil || a.channels == nil {
		architectDebugLog().Warn("publishPlanStreamEvent: NIL_GUARD",
			"event_nil", event == nil,
			"bus_nil", a == nil || a.bus == nil,
			"channels_nil", a == nil || a.channels == nil)
		return
	}
	metadata, ok := architectStreamMetadataFromContext(ctx)
	if !ok {
		architectDebugLog().Warn("publishPlanStreamEvent: NO_STREAM_METADATA",
			"event_type", string(event.Type),
			"ctx_err", ctx.Err())
		return
	}
	stream := &guide.StreamResponse{
		CorrelationID:     metadata.CorrelationID,
		RespondingAgentID: a.id,
		TargetAgentID:     metadata.SourceAgentID,
		Metadata:          shared.MergeStreamMetadata(shared.StreamResponseMetadataFromContext(ctx), nil),
		Event:             event,
	}
	msg := &guide.Message{
		ID:            a.generateMessageID(),
		CorrelationID: metadata.CorrelationID,
		Type:          guide.MessageTypeStream,
		Payload:       stream,
		SourceAgentID: a.id,
		TargetAgentID: metadata.SourceAgentID,
		Timestamp:     time.Now(),
		Status:        messaging.StatusQueued,
		Attempt:       1,
		Priority:      messaging.PriorityNormal,
	}
	err := a.bus.Publish(a.channels.Responses, msg)
	if event.Type == guide.StreamEventComplete {
		architectDebugLog().Info("publishPlanStreamEvent: STREAM_COMPLETE_PUBLISHED",
			"correlation_id", metadata.CorrelationID,
			"has_directive", event.Directive != nil,
			"topic", a.channels.Responses,
			"publish_err", err)
	}
}

// formatPlanForChat renders a DesignPlan as readable markdown suitable for
// inline streaming into the chat. Derived entirely from plan data — no LLM.
func formatPlanForChat(plan *DesignPlan) string {
	if plan == nil || len(plan.Tasks) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("### Plan\n\n")
	b.WriteString("**Status:** ")
	b.WriteString(plan.Status.String())
	b.WriteString("\n\n")
	b.WriteString("#### Tasks\n\n")

	taskByID := make(map[string]*AtomicTask, len(plan.Tasks))
	for _, task := range plan.Tasks {
		if task == nil {
			continue
		}
		taskByID[task.ID] = task
	}

	taskNum := 1
	if plan.Workflow != nil && len(plan.Workflow.ExecutionLayers) > 1 {
		for layerIdx, layer := range plan.Workflow.ExecutionLayers {
			b.WriteString("##### Layer ")
			b.WriteString(strconv.Itoa(layerIdx + 1))
			b.WriteString("\n\n")
			for _, id := range layer {
				task := taskByID[id]
				if task == nil {
					continue
				}
				writePlanMarkdownTask(&b, task, taskNum)
				taskNum++
			}
		}
		for _, task := range plan.Tasks {
			if task == nil || taskInExecutionLayers(task.ID, plan.Workflow.ExecutionLayers) {
				continue
			}
			writePlanMarkdownTask(&b, task, taskNum)
			taskNum++
		}
	} else {
		for _, task := range plan.Tasks {
			if task == nil {
				continue
			}
			writePlanMarkdownTask(&b, task, taskNum)
			taskNum++
		}
	}

	layers := 0
	if plan.Workflow != nil {
		layers = len(plan.Workflow.ExecutionLayers)
	}
	if layers > 1 {
		b.WriteString(fmt.Sprintf("\n#### Execution\n\n%d layers, %d tasks\n", layers, len(plan.Tasks)))
	} else {
		b.WriteString(fmt.Sprintf("\n#### Execution\n\n%d tasks\n", len(plan.Tasks)))
	}
	return b.String()
}

func writePlanMarkdownTask(b *strings.Builder, task *AtomicTask, num int) {
	if task == nil {
		return
	}
	b.WriteString(strconv.Itoa(num))
	b.WriteString(". **")
	b.WriteString(firstNonEmptyPlanMarkdown(task.Name, task.ID, "Untitled task"))
	b.WriteString("**")
	if agent := strings.TrimSpace(task.AgentType); agent != "" {
		b.WriteString(" `")
		b.WriteString(agent)
		b.WriteString("`")
	}
	b.WriteString(" ")
	b.WriteString(planMarkdownStatusIcon(task.Status.String()))
	b.WriteString(" ")
	b.WriteString(task.Status.String())
	b.WriteByte('\n')

	indent := strings.Repeat(" ", len(strconv.Itoa(num))+2)

	if desc := normalizePlanMarkdownBlock(task.Description); desc != "" {
		b.WriteByte('\n')
		writeIndentedPlanMarkdownBlock(b, indent, desc)
		b.WriteByte('\n')
	}
	if len(task.Dependencies) > 0 {
		b.WriteByte('\n')
		writeIndentedPlanMarkdownBlock(b, indent, "**Depends On:** "+inlineCodeList(task.Dependencies))
		b.WriteByte('\n')
	}
	if len(task.AcceptanceCriteria) > 0 {
		b.WriteByte('\n')
		writeIndentedPlanMarkdownBlock(b, indent, "**Acceptance Criteria**")
		b.WriteByte('\n')
		writePlanMarkdownCriteria(b, indent, task.AcceptanceCriteria)
	}
	if len(task.AffectedFiles) > 0 {
		b.WriteByte('\n')
		writeIndentedPlanMarkdownBlock(b, indent, "**Affected Files**")
		b.WriteByte('\n')
		for _, file := range task.AffectedFiles {
			line := "- `" + strings.TrimSpace(file.Path) + "`"
			if op := strings.TrimSpace(file.Operation); op != "" {
				line += " (" + op + ")"
			}
			if reason := strings.TrimSpace(file.Reason); reason != "" {
				line += ": " + reason
			}
			writeIndentedPlanMarkdownBlock(b, indent, line)
		}
		b.WriteByte('\n')
	}
	if guide := normalizePlanMarkdownBlock(task.ImplementationGuide); guide != "" {
		b.WriteByte('\n')
		writeIndentedPlanMarkdownBlock(b, indent, "**Implementation Guide**")
		b.WriteByte('\n')
		b.WriteByte('\n')
		writeIndentedPlanMarkdownBlock(b, indent, guide)
		b.WriteByte('\n')
	}
	if len(task.Examples) > 0 {
		b.WriteByte('\n')
		writeIndentedPlanMarkdownBlock(b, indent, "**Examples**")
		b.WriteByte('\n')
		for i, example := range task.Examples {
			writePlanMarkdownExample(b, indent, example)
			if i < len(task.Examples)-1 {
				b.WriteByte('\n')
			}
		}
	}
	if len(task.Guidelines) > 0 {
		b.WriteByte('\n')
		writeIndentedPlanMarkdownBlock(b, indent, "**Guidelines**")
		b.WriteByte('\n')
		for _, guideline := range task.Guidelines {
			if strings.TrimSpace(guideline) == "" {
				continue
			}
			writeIndentedPlanMarkdownBlock(b, indent, "- "+strings.TrimSpace(guideline))
		}
		b.WriteByte('\n')
	}
	if task.Result != nil && strings.TrimSpace(task.Result.Error) != "" {
		b.WriteByte('\n')
		writeIndentedPlanMarkdownBlock(b, indent, "> "+strings.TrimSpace(task.Result.Error))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
}

func writePlanMarkdownCriteria(b *strings.Builder, indent string, criteria []AcceptanceCriterion) {
	priorities := []string{"must", "should", "could"}
	grouped := map[string][]AcceptanceCriterion{}
	for _, criterion := range criteria {
		grouped[normalizePlanMarkdownPriority(criterion.Priority)] = append(grouped[normalizePlanMarkdownPriority(criterion.Priority)], criterion)
	}
	for _, priority := range priorities {
		group := grouped[priority]
		if len(group) == 0 {
			continue
		}
		writeIndentedPlanMarkdownBlock(b, indent, "**"+planMarkdownPriorityHeading(priority)+"**")
		for _, criterion := range group {
			line := "- [" + priority + "] Given " + strings.TrimSpace(criterion.Given) +
				" / When " + strings.TrimSpace(criterion.When) +
				" / Then " + strings.TrimSpace(criterion.Then)
			writeIndentedPlanMarkdownBlock(b, indent, line)
		}
		b.WriteByte('\n')
	}
}

func writePlanMarkdownExample(b *strings.Builder, indent string, example TaskExample) {
	example = normalizePlanMarkdownExample(example)
	if example.Label != "" {
		writeIndentedPlanMarkdownBlock(b, indent, "*"+example.Label+"*")
	}
	if example.Code != "" {
		fence := "```"
		if example.Language != "" {
			fence += example.Language
		}
		writeIndentedPlanMarkdownBlock(b, indent, fence)
		writeIndentedPlanMarkdownBlock(b, indent, example.Code)
		writeIndentedPlanMarkdownBlock(b, indent, "```")
	}
	if explanation := normalizePlanMarkdownBlock(example.Explanation); explanation != "" {
		writeIndentedPlanMarkdownBlock(b, indent, explanation)
	}
}

func writeIndentedPlanMarkdownBlock(b *strings.Builder, indent, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			b.WriteByte('\n')
			continue
		}
		b.WriteString(indent)
		b.WriteString(line)
		b.WriteByte('\n')
	}
}

func normalizePlanMarkdownBlock(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	normalized := make([]string, 0, len(lines))
	inFence := false
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		if strings.HasPrefix(strings.TrimSpace(trimmed), "```") {
			inFence = !inFence
			normalized = append(normalized, strings.TrimSpace(trimmed))
			continue
		}
		if inFence {
			normalized = append(normalized, trimmed)
			continue
		}
		normalized = append(normalized, rewritePlanMarkdownStepLine(trimmed))
	}
	return strings.TrimSpace(strings.Join(normalized, "\n"))
}

func rewritePlanMarkdownStepLine(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return ""
	}
	if number, rest, ok := parsePlanMarkdownStepPrefix(trimmed); ok {
		return strconv.Itoa(number) + ". " + rest
	}
	if number, rest, ok := parsePlanMarkdownParenPrefix(trimmed); ok {
		return strconv.Itoa(number) + ". " + rest
	}
	return trimmed
}

func parsePlanMarkdownStepPrefix(line string) (int, string, bool) {
	lower := strings.ToLower(line)
	if !strings.HasPrefix(lower, "step ") {
		return 0, "", false
	}
	i := len("step ")
	start := i
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == start || i >= len(line) || line[i] != ':' {
		return 0, "", false
	}
	number, err := strconv.Atoi(line[start:i])
	if err != nil {
		return 0, "", false
	}
	rest := strings.TrimSpace(line[i+1:])
	if rest == "" {
		return 0, "", false
	}
	return number, rest, true
}

func parsePlanMarkdownParenPrefix(line string) (int, string, bool) {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(line) || line[i] != ')' {
		return 0, "", false
	}
	number, err := strconv.Atoi(line[:i])
	if err != nil {
		return 0, "", false
	}
	rest := strings.TrimSpace(line[i+1:])
	if rest == "" {
		return 0, "", false
	}
	return number, rest, true
}

func normalizePlanMarkdownPriority(priority string) string {
	switch strings.ToLower(strings.TrimSpace(priority)) {
	case "must", "should", "could":
		return strings.ToLower(strings.TrimSpace(priority))
	default:
		return "must"
	}
}

func planMarkdownPriorityHeading(priority string) string {
	switch priority {
	case "must":
		return "Must"
	case "should":
		return "Should"
	case "could":
		return "Could"
	default:
		return "Criteria"
	}
}

func normalizePlanMarkdownExample(example TaskExample) TaskExample {
	code, detectedLanguage := trimPlanMarkdownExampleFence(example.Code)
	example.Label = strings.TrimSpace(example.Label)
	example.Language = normalizePlanMarkdownLanguage(firstNonEmptyPlanMarkdown(example.Language, detectedLanguage, ""))
	example.Code = strings.TrimSpace(code)
	example.Explanation = strings.TrimSpace(example.Explanation)
	if example.Language == "" {
		example.Language = inferPlanMarkdownLanguage(example.Code)
	}
	return example
}

func trimPlanMarkdownExampleFence(code string) (string, string) {
	trimmed := strings.TrimSpace(code)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed, ""
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) < 2 {
		return strings.TrimPrefix(trimmed, "```"), ""
	}
	first := strings.TrimSpace(lines[0])
	last := strings.TrimSpace(lines[len(lines)-1])
	if last != "```" {
		return trimmed, ""
	}
	language := strings.TrimSpace(strings.TrimPrefix(first, "```"))
	body := strings.Join(lines[1:len(lines)-1], "\n")
	return strings.Trim(body, "\n"), language
}

func normalizePlanMarkdownLanguage(language string) string {
	language = strings.ToLower(strings.TrimSpace(language))
	if language == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range language {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("+-_#", r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func inferPlanMarkdownLanguage(code string) string {
	for _, line := range strings.Split(code, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "flowchart ") ||
			strings.HasPrefix(lower, "graph ") ||
			strings.HasPrefix(lower, "sequencediagram") ||
			strings.HasPrefix(lower, "statediagram") {
			return "mermaid"
		}
		break
	}
	return ""
}

func planMarkdownStatusIcon(status string) string {
	switch status {
	case "pending":
		return "○"
	case "queued", "running":
		return "◉"
	case "completed":
		return "✓"
	case "failed":
		return "✕"
	case "blocked":
		return "◌"
	case "skipped":
		return "─"
	default:
		return "○"
	}
}

func inlineCodeList(values []string) string {
	items := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			items = append(items, "`"+trimmed+"`")
		}
	}
	return strings.Join(items, ", ")
}

func taskInExecutionLayers(taskID string, layers [][]string) bool {
	for _, layer := range layers {
		for _, id := range layer {
			if id == taskID {
				return true
			}
		}
	}
	return false
}

func firstNonEmptyPlanMarkdown(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
