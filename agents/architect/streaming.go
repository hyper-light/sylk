package architect

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/messaging"
	"github.com/adalundhe/sylk/core/providers"
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

func withArchitectSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, architectSessionIDKey{}, strings.TrimSpace(sessionID))
}

func architectSessionIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(architectSessionIDKey{}).(string)
	return v
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
}

var planStatusProgress = map[PlanStatus]streamProgressSpec{
	PlanStatusPending:       {Current: 0, Total: 6, Message: "Framing the plan..."},
	PlanStatusAnalyzing:     {Current: 1, Total: 6, Message: "Analyzing requirements..."},
	PlanStatusConsulting:    {Current: 2, Total: 6, Message: "Consulting available knowledge agents..."},
	PlanStatusClarifying:    {Current: 2, Total: 6, Message: "Waiting for clarification..."},
	PlanStatusDesigning:     {Current: 3, Total: 6, Message: "Designing architecture options..."},
	PlanStatusGenerating:    {Current: 4, Total: 6, Message: "Generating an actionable task breakdown..."},
	PlanStatusOrchestrating: {Current: 5, Total: 6, Message: "Assembling workflow and dependencies..."},
	PlanStatusReady:         {Current: 6, Total: 6, Message: "Plan is ready for your review."},
	PlanStatusExecuting:     {Current: 6, Total: 6, Message: "Handing off to orchestration..."},
	PlanStatusCompleted:     {Current: 6, Total: 6, Message: "Planning complete."},
	PlanStatusFailed:        {Current: 6, Total: 6, Message: "Planning failed."},
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
		},
		Timestamp: time.Now(),
	})
}

func formatPlanThoughtMessage(stage string, thought string) string {
	thought = strings.TrimSpace(thought)
	if thought == "" {
		return ""
	}
	stage = humanizePlanThoughtStage(stage)
	if stage == "" {
		return thought
	}
	return stage + ": " + thought
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
// newCID is the orchestrator's request CID (for the TUI to track).
func (a *Architect) publishHandoffReroute(ctx context.Context, toAgentID, originalCID, newCID string) {
	a.publishPlanStreamEvent(ctx, &guide.StreamEvent{
		Type: guide.StreamEventReroute,
		Data: map[string]string{
			"from_agent":              "architect",
			"to_agent":                toAgentID,
			"reason":                  "plan handoff",
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
	b.WriteString("### Plan\n\n**Tasks:**\n")
	for i, task := range plan.Tasks {
		if task == nil {
			continue
		}
		line := fmt.Sprintf("%d. **%s** (%s)", i+1, task.Name, task.AgentType)
		if len(task.Dependencies) > 0 {
			line += fmt.Sprintf(" \u2192 after %s", strings.Join(task.Dependencies, ", "))
		}
		b.WriteString(line + "\n")
		if desc := strings.TrimSpace(task.Description); desc != "" {
			b.WriteString("   " + truncateString(desc, 120) + "\n")
		}
	}
	layers := 0
	if plan.Workflow != nil {
		layers = len(plan.Workflow.ExecutionLayers)
	}
	if layers > 1 {
		b.WriteString(fmt.Sprintf("\n**Execution:** %d layers, %d tasks\n", layers, len(plan.Tasks)))
	} else {
		b.WriteString(fmt.Sprintf("\n**Execution:** %d tasks\n", len(plan.Tasks)))
	}
	return b.String()
}
