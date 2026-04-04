package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/ui/agentidentity"
	"github.com/adalundhe/sylk/ui/chat"
	"github.com/adalundhe/sylk/ui/compositor"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/queue"
	"github.com/adalundhe/sylk/ui/status"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
)

func appendBranchRefLogAttrs(attrs []any, prefix string, ref *msg.InterAgentBranchRefMsg) []any {
	attrs = append(attrs, prefix+"_present", ref != nil)
	if ref == nil {
		return attrs
	}
	return append(attrs,
		prefix+"_parent_correlation_id", strings.TrimSpace(ref.ParentCorrelationID),
		prefix+"_parent_tool_call_key", strings.TrimSpace(ref.ParentToolCallKey),
		prefix+"_kind", strings.TrimSpace(ref.Kind),
		prefix+"_thread_key", strings.TrimSpace(ref.ThreadKey),
	)
}

func normalizeExplicitTargetAgent(raw string) string {
	target := strings.ToLower(strings.TrimSpace(raw))
	if target == "" {
		return ""
	}
	if alias, ok := explicitTargetAliases[target]; ok {
		target = alias
	}
	if !isSimpleTargetToken(target) {
		return ""
	}
	return target
}

func (m *AppModel) resolveConcreteTargetAgent(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if target := normalizeExplicitTargetAgent(raw); target != "" {
		return target
	}
	if m != nil && m.agentPanel != nil {
		if resolved := strings.TrimSpace(m.agentPanel.ResolveTargetAgentID(raw)); resolved != "" {
			return resolved
		}
	}
	return ""
}

func (m *AppModel) resolveSubmitTarget(explicit string) string {
	target := strings.TrimSpace(explicit)
	if normalized := normalizeExplicitTargetAgent(target); normalized != "" {
		target = normalized
	}
	if target != "" {
		// Guide is the router — "@guide" means "let classifier decide",
		// so clear the sticky target and return empty.
		if target == "guide" || target == "g" {
			m.manualTargetAgent = ""
			return ""
		}
		m.manualTargetAgent = target
		// Explicit @agent overrides existing engagement.
		if normalizeAgentID(target) != m.engagedAgentID {
			m.clearEngagedAgent()
		}
		return target
	}
	target = strings.TrimSpace(m.manualTargetAgent)
	if normalized := normalizeExplicitTargetAgent(target); normalized != "" {
		target = normalized
	}
	if target != "" {
		// Guard against stale "guide" in manualTargetAgent.
		if target == "guide" || target == "g" {
			m.manualTargetAgent = ""
			return ""
		}
		if normalizeAgentID(target) != m.engagedAgentID {
			m.clearEngagedAgent()
		}
		return target
	}
	// Conversation continuity: if the user is engaged with an agent
	// (set on every StreamStart), route to that agent. Survives
	// interrupts — cleared only by selecting Guide in the panel or
	// @agent to a different target.
	engaged := normalizeExplicitTargetAgent(m.engagedAgentID)
	if engaged != "" && engaged != "guide" && engaged != "g" {
		return engaged
	}
	return ""
}

func (m *AppModel) syncManualTargetFromAgentSelection() {
	if m == nil || m.agentPanel == nil {
		return
	}
	selected := strings.TrimSpace(m.agentPanel.SelectedAgentID())
	if selected == "" {
		return
	}
	// Guide is the router, not a target. Selecting it in the panel means
	// "let the classifier decide" — clear any sticky target so subsequent
	// prompts flow through normal LLM classification.
	if selected == "guide" || selected == "g" {
		m.manualTargetAgent = ""
		m.clearEngagedAgent()
		return
	}
	m.manualTargetAgent = selected
	// Clear engagement when the user explicitly selects a different agent.
	// Without this, the previous engaged agent (e.g. architect) stays
	// sticky in the UI even after the user navigates away.
	if normalizeAgentID(selected) != m.engagedAgentID {
		m.clearEngagedAgent()
	}
}

func isSimpleTargetToken(token string) bool {
	for _, r := range token {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

func routingStatusText(target string) string {
	if target == "" {
		return "Classifying and routing request"
	}
	return fmt.Sprintf("Routing request to @%s", target)
}

func thinkingAgentType(target string) string {
	if target != "" {
		return target
	}
	return guideAgentType
}

func (m *AppModel) handleInterrupt() tea.Cmd {
	cmd := m.interruptActiveRoute("ctrl+c")
	if cmd != nil {
		return cmd
	}
	m.statusBar.SetFlash("Press Ctrl+C again to quit")
	return nil
}

func (m *AppModel) interruptActiveRoute(reason string) tea.Cmd {
	correlationID, agentID := m.resolveInterruptTarget()
	if correlationID == "" {
		return nil
	}
	targets := m.interruptTargetsForCorrelation(correlationID)
	if len(targets) == 0 {
		return nil
	}

	m.chat.MuteThinking("")
	m.chat.AbortStream()
	if m.interruptedCorrelations == nil {
		m.interruptedCorrelations = make(map[string]struct{})
	}
	for _, target := range targets {
		m.interruptedCorrelations[target.CorrelationID] = struct{}{}
		m.finalizeStreamUsage(target.CorrelationID, false, "interrupted")
		m.markQueueEntryByCorrelation(target.CorrelationID, false)
		m.unregisterStream(target.CorrelationID)
	}
	m.pushInterruptedChatMessage(agentID)
	m.agentPanel.DemoteAllActive()
	if m.statusBar != nil {
		m.statusBar.SetTokenPhase(status.PhaseIdle)
	}
	// Pause the queue on user interrupt — deliberate resume required.
	if !m.promptQueue.IsEmpty() {
		m.promptQueue.SetPaused(true)
		m.recalcLayout()
		m.viewDirty = true
	}
	m.statusBar.SetFlash("Agent interrupted")

	if m.deps.GuideBus == nil {
		return nil
	}

	interruptReq := &guide.UserInterruptRequest{
		SourceAgentID: sourceAgentTUI,
		Reason:        strings.TrimSpace(reason),
	}
	return func() tea.Msg {
		sessionID := m.resolveRouteSessionID("")
		for _, target := range targets {
			req := *interruptReq
			req.CorrelationID = target.CorrelationID
			req.Timestamp = time.Now()
			if err := m.deps.GuideBus.Publish(guide.TopicGuideRequests, guide.NewUserInterruptMessage("", &req)); err != nil {
				return msg.StreamErrorMsg{
					SessionID:     sessionID,
					CorrelationID: target.CorrelationID,
					Err:           err,
				}
			}
		}
		return nil
	}
}

// interruptAllActiveRoutes interrupts every active stream — all agents.
// Triggered by triple-Esc. Each active stream gets its own interrupt
// request so every agent receives a cancel action.
func (m *AppModel) interruptAllActiveRoutes(reason string) tea.Cmd {
	// Collect all active stream entries.
	targets := m.allVisibleStreamEntries()

	if len(targets) > 0 {
		// UI cleanup — same as interruptActiveRoute but for all visible streams.
		m.chat.MuteThinking("")
		m.chat.AbortStream()
		if m.interruptedCorrelations == nil {
			m.interruptedCorrelations = make(map[string]struct{})
		}
		for _, t := range targets {
			m.interruptedCorrelations[t.CorrelationID] = struct{}{}
			m.finalizeStreamUsage(t.CorrelationID, false, "interrupted")
			m.markQueueEntryByCorrelation(t.CorrelationID, false)
			m.unregisterStream(t.CorrelationID)
		}
		m.pushInterruptedChatMessage("all agents")
		m.agentPanel.DemoteAllActive()
		if m.statusBar != nil {
			m.statusBar.SetTokenPhase(status.PhaseIdle)
		}
		if !m.promptQueue.IsEmpty() {
			m.promptQueue.SetPaused(true)
			m.recalcLayout()
			m.viewDirty = true
		}
	}
	m.statusBar.SetFlash("All agents interrupted")

	return func() tea.Msg {
		if m.deps.InterruptAllAgents != nil {
			sessionID := m.resolveRouteSessionID("")
			if err := m.deps.InterruptAllAgents(sessionID, strings.TrimSpace(reason)); err != nil {
				return msg.StreamErrorMsg{
					SessionID:     sessionID,
					CorrelationID: "",
					Err:           err,
				}
			}
			return nil
		}
		if m.deps.GuideBus != nil {
			for _, t := range targets {
				req := &guide.UserInterruptRequest{
					CorrelationID: t.CorrelationID,
					SourceAgentID: sourceAgentTUI,
					Reason:        strings.TrimSpace(reason),
					Timestamp:     time.Now(),
				}
				_ = m.deps.GuideBus.Publish(guide.TopicGuideRequests, guide.NewUserInterruptMessage("", req))
			}
		}
		return nil
	}
}

// publishSteerAction sends a live steering command to the active agent
// via the guide bus. The user's text is injected into the agent's tool loop
// at the next checkpoint boundary.
func (m *AppModel) publishSteerAction(text string) tea.Cmd {
	// Resolve the target agent's active stream for steering.
	target := m.manualTargetAgent
	if target == "" {
		target = m.engagedAgentID
	}
	stream := m.activeStreamForAgent(target)
	if stream == nil {
		return nil
	}
	correlationID := stream.CorrelationID
	agentID := stream.AgentID
	if correlationID == "" || m.deps.GuideBus == nil {
		return nil
	}

	// Show the steering input with holographic shimmer until the agent acknowledges.
	m.chat.PushSteeringEntry(text, correlationID)

	actionReq := &guide.ActionRequest{
		CorrelationID: correlationID,
		SourceAgentID: sourceAgentTUI,
		TargetAgentID: agentID,
		Action:        "steer",
		Data:          text,
		FireAndForget: true,
		Timestamp:     time.Now(),
	}
	busMsg := guide.NewActionMessage("", actionReq)

	return func() tea.Msg {
		if err := m.deps.GuideBus.Publish(guide.TopicGuideRequests, busMsg); err != nil {
			return msg.StreamErrorMsg{
				SessionID:     m.resolveRouteSessionID(""),
				CorrelationID: correlationID,
				Err:           err,
			}
		}
		return nil
	}
}

// toggleSteeringPace cycles the steering pace: auto → step → paused → auto.
func (m *AppModel) toggleSteeringPace() tea.Cmd {
	// Find the selected/engaged agent's active stream to read/write pace.
	target := m.manualTargetAgent
	if target == "" {
		target = m.engagedAgentID
	}
	stream := m.activeStreamForAgent(target)
	if stream == nil {
		return nil
	}
	var next string
	switch stream.SteeringPace {
	case "", "auto":
		next = "step"
	case "step":
		next = "paused"
	default:
		next = "auto"
	}
	stream.SteeringPace = next
	m.statusBar.SetFlash(fmt.Sprintf("Steering pace: %s", next))
	return m.publishPaceAction(next)
}

// publishPaceAction sends a pace-change action to the active agent.
func (m *AppModel) publishPaceAction(pace string) tea.Cmd {
	// Resolve the target agent's active stream for the pace action.
	target := m.manualTargetAgent
	if target == "" {
		target = m.engagedAgentID
	}
	stream := m.activeStreamForAgent(target)
	if stream == nil {
		return nil
	}
	correlationID := stream.CorrelationID
	agentID := stream.AgentID
	if correlationID == "" || m.deps.GuideBus == nil {
		return nil
	}

	actionReq := &guide.ActionRequest{
		CorrelationID: correlationID,
		SourceAgentID: sourceAgentTUI,
		TargetAgentID: agentID,
		Action:        "pace",
		Data:          map[string]any{"pace": pace},
		FireAndForget: true,
		Timestamp:     time.Now(),
	}
	busMsg := guide.NewActionMessage("", actionReq)

	return func() tea.Msg {
		if err := m.deps.GuideBus.Publish(guide.TopicGuideRequests, busMsg); err != nil {
			return msg.StreamErrorMsg{
				SessionID:     m.resolveRouteSessionID(""),
				CorrelationID: correlationID,
				Err:           err,
			}
		}
		return nil
	}
}

func (m *AppModel) pushInterruptedChatMessage(agentID string) {
	content := fmt.Sprintf("%s interrupted. What would you like to do next?", interruptAgentDisplayName(agentID))
	entry := &chat.ChatEntry{
		ID:        uuid.New().String(),
		Timestamp: time.Now(),
		Source:    chat.SourceSystem,
		AgentType: "system",
		AgentID:   normalizeAgentID(agentID),
		Content:   content,
		Height:    -1,
	}
	m.chat.FinishThinking(entry)
}

func interruptAgentDisplayName(agentID string) string {
	names := map[string]string{
		"guide":       "Guide",
		"architect":   "Architect",
		"librarian":   "Librarian",
		"archivalist": "Archivalist",
		"academic":    "Academic",
	}
	key := strings.ToLower(strings.TrimSpace(agentID))
	if name, ok := names[key]; ok {
		return name
	}
	trimmed := strings.TrimSpace(agentID)
	if trimmed == "" {
		return "Agent"
	}
	return strings.ToUpper(trimmed[:1]) + trimmed[1:]
}

// registerStream adds a stream to the active set. Idempotent — no-op if
// the correlationID is already registered. Multiple agents can stream
// concurrently (e.g. architect + orchestrator).
func logicalStreamPipelineID(pipelineID, taskID string) string {
	if trimmed := strings.TrimSpace(taskID); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(pipelineID)
}

func parseTaskScopedPipelineAgentID(agentID string) (taskID, agentType string, ok bool) {
	return agentidentity.ParseTaskScopedPipelineAgentID(agentID)
}

func parseCanonicalPipelinePanelAgentID(agentID string) (pipelineID, agentType string, ok bool) {
	return agentidentity.ParseCanonicalPipelinePanelAgentID(agentID)
}

func isPipelineWorkerType(agentType string) bool {
	return agentidentity.IsPipelineWorkerType(agentType)
}

func canonicalPipelineWorkerPlaceholderID(agentType, pipelineID string) string {
	return agentidentity.CanonicalPipelinePanelID(agentType, pipelineID)
}

func streamPanelAgentID(agentID, agentType, pipelineID string) string {
	return agentidentity.VisibleAgentID(agentID, "", agentType, pipelineID, "")
}

func canonicalStreamAgentID(agentID, agentType, pipelineID, taskID string) string {
	return agentidentity.VisibleAgentID(agentID, "", agentType, pipelineID, taskID)
}

func effectiveCanonicalStreamAgentID(entry *activeStreamEntry, agentID, agentType, pipelineID, taskID string) string {
	effectiveType := firstNonEmpty(strings.TrimSpace(agentType), strings.TrimSpace(entryAgentType(entry)))
	effectivePipelineID := firstNonEmpty(
		logicalStreamPipelineID(pipelineID, taskID),
		logicalStreamPipelineID(entryPipelineID(entry), entryTaskID(entry)),
	)
	if placeholder := canonicalPipelineWorkerPlaceholderID(effectiveType, effectivePipelineID); placeholder != "" {
		return placeholder
	}
	canonicalID := canonicalStreamAgentID(agentID, agentType, pipelineID, taskID)
	if strings.TrimSpace(canonicalID) != "" {
		return canonicalID
	}
	if entry != nil {
		return strings.TrimSpace(entry.AgentID)
	}
	return canonicalID
}

func entryAgentType(entry *activeStreamEntry) string {
	if entry == nil {
		return ""
	}
	return entry.AgentType
}

func entryPipelineID(entry *activeStreamEntry) string {
	if entry == nil {
		return ""
	}
	return entry.PipelineID
}

func entryTaskID(entry *activeStreamEntry) string {
	if entry == nil {
		return ""
	}
	return entry.TaskID
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func activityDataString(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	value, ok := data[key]
	if !ok {
		return ""
	}
	typed, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(typed)
}

func activityDataInt(data map[string]any, key string) (int, bool) {
	if data == nil {
		return 0, false
	}
	value, ok := data[key]
	if !ok {
		return 0, false
	}
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	default:
		return 0, false
	}
}

func canonicalActivityAgentID(ev *events.ActivityEvent) string {
	if ev == nil {
		return ""
	}
	return agentidentity.VisibleAgentID(
		ev.AgentID,
		activityDataString(ev.Data, "canonical_agent_id"),
		activityDataString(ev.Data, "agent_type"),
		activityDataString(ev.Data, "pipeline_id"),
		activityDataString(ev.Data, "task_id"),
	)
}

func (m *AppModel) applyActivityTelemetry(activity msg.ActivityEventMsg) {
	if activity.Event == nil {
		return
	}
	canonicalID := canonicalActivityAgentID(activity.Event)
	if canonicalID == "" {
		return
	}
	runtimeAgentID := normalizeRuntimeAgentID(canonicalID, firstNonEmpty(
		activityDataString(activity.Event.Data, "runtime_agent_id"),
		activity.Event.AgentID,
	))
	if active, ok := activityDataInt(activity.Event.Data, "active_replicas"); ok {
		m.noteAgentReplicaCount(canonicalID, active)
	}
	if tokens, ok := activityDataInt(activity.Event.Data, "context_tokens"); ok {
		if tokens == 0 {
			m.clearAgentReplicaContextUsage(canonicalID, runtimeAgentID)
			return
		}
		m.setAgentReplicaContextUsage(canonicalID, runtimeAgentID, "", tokens)
		return
	}
	if activityDataString(activity.Event.Data, "handoff_state") == "completed" {
		m.clearAgentReplicaContextUsage(canonicalID, runtimeAgentID)
	}
}

func cloneActiveStreamEntry(entry *activeStreamEntry) *activeStreamEntry {
	if entry == nil {
		return nil
	}
	cloned := *entry
	cloned.BranchRef = cloneInterAgentBranchRef(entry.BranchRef)
	return &cloned
}

func cloneInterAgentBranchRef(ref *msg.InterAgentBranchRefMsg) *msg.InterAgentBranchRefMsg {
	if ref == nil {
		return nil
	}
	cloned := *ref
	return &cloned
}

func isExplicitTopLevelTransfer(parentCorrelationID string, ref *msg.InterAgentBranchRefMsg, topLevelTransfer bool) bool {
	return topLevelTransfer && ref == nil && strings.TrimSpace(parentCorrelationID) != ""
}

func (m *AppModel) clearExplicitTopLevelTransferState(correlationID, parentCorrelationID string, ref *msg.InterAgentBranchRefMsg, topLevelTransfer bool) {
	if !isExplicitTopLevelTransfer(parentCorrelationID, ref, topLevelTransfer) || m == nil || m.nestedStreams == nil {
		return
	}
	delete(m.nestedStreams, strings.TrimSpace(correlationID))
}

func (m *AppModel) resolveIncomingStreamBranchRef(
	correlationID, parentCorrelationID string,
	ref *msg.InterAgentBranchRefMsg,
	topLevelTransfer bool,
) *msg.InterAgentBranchRefMsg {
	if isExplicitTopLevelTransfer(parentCorrelationID, ref, topLevelTransfer) {
		return nil
	}
	if resolved := m.effectiveStreamBranchRef(correlationID, ref); resolved != nil {
		return resolved
	}
	return nil
}

func (m *AppModel) effectiveStreamBranchRef(correlationID string, ref *msg.InterAgentBranchRefMsg) *msg.InterAgentBranchRefMsg {
	if ref != nil {
		return cloneInterAgentBranchRef(ref)
	}
	if existing := m.streamEntryForCorrelation(correlationID); existing != nil && existing.BranchRef != nil {
		return cloneInterAgentBranchRef(existing.BranchRef)
	}
	if recorded := m.recordedStreamBranchRef(correlationID); recorded != nil {
		return cloneInterAgentBranchRef(recorded)
	}
	return nil
}

func (m *AppModel) registerStream(start msg.StreamStartMsg) bool {
	if start.BranchRef != nil {
		return m.registerNestedStream(start)
	}
	return m.registerPrimaryStream(start)
}

func (m *AppModel) registerPrimaryStream(start msg.StreamStartMsg) bool {
	correlationID := strings.TrimSpace(start.CorrelationID)
	if correlationID == "" {
		return false
	}
	if m.activeStreams == nil {
		m.activeStreams = make(map[string]*activeStreamEntry)
	}
	if m.deferredStreams == nil {
		m.deferredStreams = make(map[string]*activeStreamEntry)
	}
	logicalPipelineID := logicalStreamPipelineID(start.PipelineID, start.TaskID)
	canonicalAgentID := canonicalStreamAgentID(start.AgentID, start.AgentType, start.PipelineID, start.TaskID)
	if existing, exists := m.activeStreams[correlationID]; exists {
		updateStreamEntry(existing, start, canonicalAgentID, logicalPipelineID)
		attrs := []any{
			"correlation_id", correlationID,
			"registration", "active_update",
			"canonical_agent_id", canonicalAgentID,
			"runtime_agent_id", normalizeRuntimeAgentID(canonicalAgentID, firstNonEmpty(start.RuntimeAgentID, start.AgentID)),
			"agent_type", strings.TrimSpace(start.AgentType),
			"active_streams", len(m.activeStreams),
			"deferred_streams", len(m.deferredStreams),
			"nested_streams", len(m.nestedStreams),
		}
		attrs = appendBranchRefLogAttrs(attrs, "branch_ref", start.BranchRef)
		uiDebugFileLog().Info("AppModel: REGISTER_PRIMARY_STREAM", attrs...)
		return false
	}
	if existing, exists := m.deferredStreams[correlationID]; exists {
		delete(m.deferredStreams, correlationID)
		updateStreamEntry(existing, start, canonicalAgentID, logicalPipelineID)
		m.activeStreams[correlationID] = existing
		attrs := []any{
			"correlation_id", correlationID,
			"registration", "deferred_resume",
			"canonical_agent_id", canonicalAgentID,
			"runtime_agent_id", normalizeRuntimeAgentID(canonicalAgentID, firstNonEmpty(start.RuntimeAgentID, start.AgentID)),
			"agent_type", strings.TrimSpace(start.AgentType),
			"active_streams", len(m.activeStreams),
			"deferred_streams", len(m.deferredStreams),
			"nested_streams", len(m.nestedStreams),
		}
		attrs = appendBranchRefLogAttrs(attrs, "branch_ref", start.BranchRef)
		uiDebugFileLog().Info("AppModel: REGISTER_PRIMARY_STREAM", attrs...)
		return false
	}
	if existing, exists := m.nestedStreams[correlationID]; exists && existing != nil {
		updateStreamEntry(existing, start, canonicalAgentID, logicalPipelineID)
		attrs := []any{
			"correlation_id", correlationID,
			"registration", "nested_preserve",
			"canonical_agent_id", canonicalAgentID,
			"runtime_agent_id", normalizeRuntimeAgentID(canonicalAgentID, firstNonEmpty(start.RuntimeAgentID, start.AgentID)),
			"agent_type", strings.TrimSpace(start.AgentType),
			"active_streams", len(m.activeStreams),
			"deferred_streams", len(m.deferredStreams),
			"nested_streams", len(m.nestedStreams),
		}
		attrs = appendBranchRefLogAttrs(attrs, "branch_ref", start.BranchRef)
		attrs = append(attrs, "preserved_existing_nested", true)
		uiDebugFileLog().Info("AppModel: REGISTER_PRIMARY_STREAM", attrs...)
		return false
	}
	m.replaceActivePipelineWorkerStream(correlationID, canonicalAgentID)
	delete(m.nestedStreams, correlationID)
	m.activeStreams[correlationID] = newStreamEntry(start, canonicalAgentID, logicalPipelineID)
	attrs := []any{
		"correlation_id", correlationID,
		"registration", "new_primary",
		"canonical_agent_id", canonicalAgentID,
		"runtime_agent_id", normalizeRuntimeAgentID(canonicalAgentID, firstNonEmpty(start.RuntimeAgentID, start.AgentID)),
		"agent_type", strings.TrimSpace(start.AgentType),
		"active_streams", len(m.activeStreams),
		"deferred_streams", len(m.deferredStreams),
		"nested_streams", len(m.nestedStreams),
	}
	attrs = appendBranchRefLogAttrs(attrs, "branch_ref", start.BranchRef)
	uiDebugFileLog().Info("AppModel: REGISTER_PRIMARY_STREAM", attrs...)
	return true
}

func (m *AppModel) registerNestedStream(start msg.StreamStartMsg) bool {
	correlationID := strings.TrimSpace(start.CorrelationID)
	if correlationID == "" {
		return false
	}
	if m.nestedStreams == nil {
		m.nestedStreams = make(map[string]*activeStreamEntry)
	}
	logicalPipelineID := logicalStreamPipelineID(start.PipelineID, start.TaskID)
	canonicalAgentID := canonicalStreamAgentID(start.AgentID, start.AgentType, start.PipelineID, start.TaskID)
	if existing, exists := m.nestedStreams[correlationID]; exists {
		updateStreamEntry(existing, start, canonicalAgentID, logicalPipelineID)
		return false
	}
	delete(m.activeStreams, correlationID)
	m.nestedStreams[correlationID] = newStreamEntry(start, canonicalAgentID, logicalPipelineID)
	return true
}

func newStreamEntry(start msg.StreamStartMsg, canonicalAgentID, logicalPipelineID string) *activeStreamEntry {
	return &activeStreamEntry{
		CorrelationID:  strings.TrimSpace(start.CorrelationID),
		AgentID:        strings.TrimSpace(canonicalAgentID),
		RuntimeAgentID: normalizeRuntimeAgentID(canonicalAgentID, firstNonEmpty(start.RuntimeAgentID, start.AgentID)),
		AgentType:      strings.TrimSpace(start.AgentType),
		AgentName:      strings.TrimSpace(start.AgentName),
		PipelineID:     strings.TrimSpace(logicalPipelineID),
		TaskID:         strings.TrimSpace(start.TaskID),
		TaskName:       strings.TrimSpace(start.TaskName),
		TaskSlug:       strings.TrimSpace(start.TaskSlug),
		BranchRef:      cloneInterAgentBranchRef(start.BranchRef),
		StartedAt:      time.Now(),
	}
}

func updateStreamEntry(entry *activeStreamEntry, start msg.StreamStartMsg, canonicalAgentID, logicalPipelineID string) {
	if entry == nil {
		return
	}
	entry.AgentID = firstNonEmpty(strings.TrimSpace(canonicalAgentID), entry.AgentID)
	entry.RuntimeAgentID = firstNonEmpty(normalizeRuntimeAgentID(canonicalAgentID, firstNonEmpty(start.RuntimeAgentID, start.AgentID)), entry.RuntimeAgentID)
	entry.AgentType = firstNonEmpty(strings.TrimSpace(start.AgentType), entry.AgentType)
	entry.AgentName = firstNonEmpty(strings.TrimSpace(start.AgentName), entry.AgentName)
	entry.PipelineID = firstNonEmpty(strings.TrimSpace(logicalPipelineID), entry.PipelineID)
	entry.TaskID = firstNonEmpty(strings.TrimSpace(start.TaskID), entry.TaskID)
	entry.TaskName = firstNonEmpty(strings.TrimSpace(start.TaskName), entry.TaskName)
	entry.TaskSlug = firstNonEmpty(strings.TrimSpace(start.TaskSlug), entry.TaskSlug)
	if start.BranchRef != nil {
		entry.BranchRef = cloneInterAgentBranchRef(start.BranchRef)
	}
}

func (m *AppModel) replaceActivePipelineWorkerStream(correlationID, canonicalAgentID string) {
	canonicalAgentID = strings.TrimSpace(canonicalAgentID)
	if canonicalAgentID == "" || !strings.Contains(canonicalAgentID, ":") {
		return
	}
	for existingCID, entry := range m.activeStreams {
		if existingCID == correlationID || entry == nil {
			continue
		}
		if strings.TrimSpace(entry.AgentID) != canonicalAgentID {
			continue
		}
		m.markReroutedStreamCID(existingCID)
		delete(m.activeStreams, existingCID)
		uiDebugFileLog().Info("AppModel: REPLACE_ACTIVE_PIPELINE_WORKER_STREAM",
			"correlation_id", correlationID,
			"replaced_correlation_id", existingCID,
			"canonical_agent_id", canonicalAgentID,
			"source_registry", "active")
	}
	for existingCID, entry := range m.deferredStreams {
		if existingCID == correlationID || entry == nil {
			continue
		}
		if strings.TrimSpace(entry.AgentID) != canonicalAgentID {
			continue
		}
		delete(m.deferredStreams, existingCID)
		uiDebugFileLog().Info("AppModel: REPLACE_ACTIVE_PIPELINE_WORKER_STREAM",
			"correlation_id", correlationID,
			"replaced_correlation_id", existingCID,
			"canonical_agent_id", canonicalAgentID,
			"source_registry", "deferred")
	}
}

// shouldRenderStreamEvent returns true when the correlationID belongs to
// a registered (active) stream. When no streams are active, any event is
// accepted — this preserves the "first-event wins" bootstrap behaviour.
func (m *AppModel) shouldRenderStreamEvent(correlationID string) bool {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return false
	}
	if _, interrupted := m.interruptedCorrelations[correlationID]; interrupted {
		return false
	}
	if m.visibleStreamCount() == 0 {
		return true
	}
	return m.streamEntryForCorrelation(correlationID) != nil
}

// shouldRenderTerminalStreamEvent returns true when a completion should reach
// downstream UI components even if the stream was already rerouted away from
// the active set. This lets the source agent finalize its existing chat slot
// and clear thinking state after a handoff.
func (m *AppModel) shouldRenderTerminalStreamEvent(correlationID string) bool {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return false
	}
	if m.shouldRenderStreamEvent(correlationID) {
		return true
	}
	if _, interrupted := m.interruptedCorrelations[correlationID]; interrupted {
		return false
	}
	if m.reroutedStreamCIDs == nil {
		return false
	}
	now := time.Now()
	m.pruneReroutedStreamCIDs(now)
	_, ok := m.reroutedStreamCIDs[correlationID]
	return ok
}

func (m *AppModel) shouldIgnoreLateStreamBootstrap(correlationID string) bool {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" || m.streamedResponses == nil {
		return false
	}
	state, ok := m.streamedResponses[correlationID]
	if !ok || !state.Completed {
		return false
	}
	return m.streamEntryForCorrelation(correlationID) == nil
}

func (m *AppModel) shouldRenderToolCallTerminalEvent(correlationID string) bool {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return false
	}
	if m.shouldRenderTerminalStreamEvent(correlationID) {
		return true
	}
	if _, interrupted := m.interruptedCorrelations[correlationID]; interrupted {
		return false
	}
	return m.hasRecordedStreamCorrelation(correlationID)
}

func (m *AppModel) hasRecordedStreamCorrelation(correlationID string) bool {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" || m.streamedResponses == nil {
		return false
	}
	_, ok := m.streamedResponses[correlationID]
	return ok
}

func (m *AppModel) shouldBootstrapStreamFromTelemetry(correlationID string, explicitTopLevelTransfer bool) bool {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return false
	}
	if m.streamEntryForCorrelation(correlationID) != nil {
		return false
	}
	if explicitTopLevelTransfer {
		return true
	}
	return !m.hasRecordedStreamCorrelation(correlationID)
}

func (m *AppModel) markReroutedStreamCID(correlationID string) {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return
	}
	if m.reroutedStreamCIDs == nil {
		m.reroutedStreamCIDs = make(map[string]time.Time)
	}
	now := time.Now()
	m.pruneReroutedStreamCIDs(now)
	m.reroutedStreamCIDs[correlationID] = now
}

func (m *AppModel) clearReroutedStreamCID(correlationID string) {
	if m.reroutedStreamCIDs == nil {
		return
	}
	delete(m.reroutedStreamCIDs, strings.TrimSpace(correlationID))
}

func (m *AppModel) pruneReroutedStreamCIDs(now time.Time) {
	if m.reroutedStreamCIDs == nil {
		return
	}
	for correlationID, seenAt := range m.reroutedStreamCIDs {
		if now.Sub(seenAt) > reroutedStreamCIDTTL {
			delete(m.reroutedStreamCIDs, correlationID)
		}
	}
}

// unregisterStream removes the given correlationID from the active set.
func (m *AppModel) unregisterStream(correlationID string) {
	correlationID = strings.TrimSpace(correlationID)
	delete(m.activeStreams, correlationID)
	delete(m.deferredStreams, correlationID)
	delete(m.nestedStreams, correlationID)
	delete(m.delayedPrimaryBootstrap, correlationID)
}

func (m *AppModel) deferPrimaryStream(correlationID string) {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return
	}
	entry, ok := m.activeStreams[correlationID]
	if !ok || entry == nil {
		return
	}
	if m.deferredStreams == nil {
		m.deferredStreams = make(map[string]*activeStreamEntry)
	}
	delete(m.activeStreams, correlationID)
	m.deferredStreams[correlationID] = entry
	attrs := []any{
		"correlation_id", correlationID,
		"agent_id", entry.AgentID,
		"runtime_agent_id", entry.RuntimeAgentID,
		"agent_type", entry.AgentType,
		"task_id", entry.TaskID,
		"active_streams", len(m.activeStreams),
		"deferred_streams", len(m.deferredStreams),
		"nested_streams", len(m.nestedStreams),
	}
	attrs = appendBranchRefLogAttrs(attrs, "branch_ref", entry.BranchRef)
	uiDebugFileLog().Info("AppModel: DEFER_PRIMARY_STREAM", attrs...)
}

// activeStreamForAgent returns the active stream entry for the given agent,
// or nil if the agent has no active stream.
func (m *AppModel) activeStreamForAgent(agentID string) *activeStreamEntry {
	return streamEntryForAgentMap(m.activeStreams, agentID, m.resolveConcreteTargetAgent(agentID))
}

func (m *AppModel) visibleStreamForAgent(agentID string) *activeStreamEntry {
	if entry := streamEntryForAgentMap(m.activeStreams, agentID, m.resolveConcreteTargetAgent(agentID)); entry != nil {
		return entry
	}
	if entry := streamEntryForAgentMap(m.deferredStreams, agentID, m.resolveConcreteTargetAgent(agentID)); entry != nil {
		return entry
	}
	return streamEntryForAgentMap(m.nestedStreams, agentID, m.resolveConcreteTargetAgent(agentID))
}

func streamEntryForAgentMap(streams map[string]*activeStreamEntry, agentID, resolvedAgentID string) *activeStreamEntry {
	original := normalizeAgentID(agentID)
	resolved := normalizeAgentID(resolvedAgentID)
	for _, entry := range streams {
		if entry == nil {
			continue
		}
		if entry.AgentID == original || (resolved != "" && entry.AgentID == resolved) {
			return entry
		}
	}
	return nil
}

func (m *AppModel) streamEntryForCorrelation(correlationID string) *activeStreamEntry {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return nil
	}
	if entry, ok := m.activeStreams[correlationID]; ok && entry != nil {
		return entry
	}
	if entry, ok := m.deferredStreams[correlationID]; ok && entry != nil {
		return entry
	}
	if entry, ok := m.nestedStreams[correlationID]; ok && entry != nil {
		return entry
	}
	return nil
}

func (m *AppModel) visibleStreamCount() int {
	return len(m.activeStreams) + len(m.deferredStreams) + len(m.nestedStreams)
}

func (m *AppModel) shouldDelayAmbiguousPrimaryBootstrap(start msg.StreamStartMsg) bool {
	if m == nil || start.BranchRef != nil {
		return false
	}
	if strings.TrimSpace(start.CorrelationID) == "" {
		return false
	}
	if strings.TrimSpace(start.ParentCorrelationID) != "" ||
		strings.TrimSpace(start.TaskID) != "" ||
		strings.TrimSpace(start.PipelineID) != "" {
		return false
	}
	canonicalAgentID := canonicalStreamAgentID(start.AgentID, start.AgentType, start.PipelineID, start.TaskID)
	if strings.Contains(strings.TrimSpace(canonicalAgentID), ":") {
		return false
	}
	bootstrapIdentity := normalizeAgentID(firstNonEmpty(strings.TrimSpace(start.AgentType), strings.TrimSpace(start.AgentID)))
	if bootstrapIdentity == "" || len(m.deferredStreams) == 0 {
		return false
	}
	for _, entry := range m.deferredStreams {
		if entry == nil || entry.BranchRef != nil {
			continue
		}
		entryIdentity := normalizeAgentID(firstNonEmpty(strings.TrimSpace(entry.AgentType), strings.TrimSpace(entry.AgentID)))
		if entryIdentity != bootstrapIdentity {
			continue
		}
		if strings.TrimSpace(entry.TaskID) != "" ||
			strings.TrimSpace(entry.PipelineID) != "" ||
			strings.Contains(strings.TrimSpace(entry.AgentID), ":") {
			return true
		}
	}
	return false
}

func (m *AppModel) enqueueDelayedPrimaryBootstrap(correlationID string, event tea.Msg) {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" || event == nil {
		return
	}
	if m.delayedPrimaryBootstrap == nil {
		m.delayedPrimaryBootstrap = make(map[string][]tea.Msg)
	}
	m.delayedPrimaryBootstrap[correlationID] = append(m.delayedPrimaryBootstrap[correlationID], event)
}

func (m *AppModel) flushDelayedPrimaryBootstrap(correlationID string) tea.Cmd {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" || len(m.delayedPrimaryBootstrap) == 0 {
		return nil
	}
	events := m.delayedPrimaryBootstrap[correlationID]
	if len(events) == 0 {
		delete(m.delayedPrimaryBootstrap, correlationID)
		return nil
	}
	delete(m.delayedPrimaryBootstrap, correlationID)
	cmds := make([]tea.Cmd, 0, len(events))
	for _, pending := range events {
		switch typed := pending.(type) {
		case msg.StreamProgressMsg:
			cmds = appendCmd(cmds, m.handleStreamProgressTelemetry(typed))
		case msg.ToolCallEventMsg:
			cmds = appendCmd(cmds, m.handleToolCallTelemetry(typed))
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

func (m *AppModel) allVisibleStreamEntries() []activeStreamEntry {
	targets := make([]activeStreamEntry, 0, m.visibleStreamCount())
	for _, entry := range m.activeStreams {
		if entry != nil {
			targets = append(targets, *entry)
		}
	}
	for _, entry := range m.deferredStreams {
		if entry != nil {
			targets = append(targets, *entry)
		}
	}
	for _, entry := range m.nestedStreams {
		if entry != nil {
			targets = append(targets, *entry)
		}
	}
	return targets
}

func (m *AppModel) interruptTargetsForCorrelation(correlationID string) []activeStreamEntry {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return nil
	}

	seen := map[string]struct{}{}
	queue := make([]string, 0, 4)
	targets := make([]activeStreamEntry, 0, 4)
	addTarget := func(entry *activeStreamEntry) {
		if entry == nil {
			return
		}
		cid := strings.TrimSpace(entry.CorrelationID)
		if cid == "" {
			return
		}
		if _, ok := seen[cid]; ok {
			return
		}
		seen[cid] = struct{}{}
		targets = append(targets, *entry)
		queue = append(queue, cid)
	}

	if entry := m.streamEntryForCorrelation(correlationID); entry != nil {
		addTarget(entry)
	} else {
		seen[correlationID] = struct{}{}
		queue = append(queue, correlationID)
	}

	for len(queue) > 0 {
		parentCID := queue[0]
		queue = queue[1:]
		for _, entry := range m.nestedStreams {
			if entry == nil || entry.BranchRef == nil {
				continue
			}
			if strings.TrimSpace(entry.BranchRef.ParentCorrelationID) != parentCID {
				continue
			}
			addTarget(entry)
		}
	}

	if len(targets) == 0 {
		targets = append(targets, activeStreamEntry{
			CorrelationID: correlationID,
			AgentID:       m.streamAgentID(correlationID),
		})
	}

	return targets
}

// hasActiveStreams reports whether any streams are currently active.
func (m *AppModel) hasActiveStreams() bool {
	return len(m.activeStreams) > 0
}

// tryAdvanceQueue finds all pending queue entries whose target agents are free
// and dispatches them concurrently. Returns a Cmd or nil.
func (m *AppModel) tryAdvanceQueue() tea.Cmd {
	if m.promptQueue.IsPaused() || m.promptQueue.IsEmpty() {
		return nil
	}
	ready := m.promptQueue.AdvanceReady(func(agentID string) bool {
		return m.activeStreamForAgent(agentID) != nil
	})
	if len(ready) == 0 {
		m.recalcLayout()
		m.viewDirty = true
		return nil
	}
	ids := make([]string, len(ready))
	for i, e := range ready {
		m.promptQueue.MarkDispatching(e.ID)
		ids[i] = e.ID
	}
	return func() tea.Msg {
		return msg.QueueAdvanceMsg{EntryIDs: ids}
	}
}

// dispatchQueueEntries dispatches one or more queue entries through the
// normal submit path. Each entry targets a different agent.
func (m *AppModel) dispatchQueueEntries(entryIDs []string) tea.Cmd {
	var cmds []tea.Cmd
	for _, id := range entryIDs {
		entry := m.promptQueue.Find(id)
		if entry == nil || entry.State != queue.StateDispatching {
			continue
		}
		submit := msg.SubmitPromptMsg{
			Text:        entry.Text,
			TargetAgent: entry.TargetAgent,
			SessionID:   entry.SessionID,
		}
		cmd := m.handleSubmit(submit)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		// Find the stream that was just created for this agent.
		if stream := m.activeStreamForAgent(entry.TargetAgent); stream != nil {
			m.promptQueue.MarkActive(id, stream.CorrelationID)
		}
	}
	m.recalcLayout()
	m.viewDirty = true
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// markQueueEntryByCorrelation marks a queue entry as completed or failed based
// on its correlation ID. No-op if no queue entry matches the correlation ID.
func (m *AppModel) markQueueEntryByCorrelation(correlationID string, success bool) {
	active, ok := m.promptQueue.ActiveEntryByCorrelation(correlationID)
	if !ok {
		return
	}
	if success {
		m.promptQueue.MarkCompleted(active.ID)
	} else {
		m.promptQueue.MarkFailed(active.ID)
	}
}

// setEngagedAgent updates the sticky engaged agent for conversation continuity.
// Non-guide agents are tracked; "guide" is ignored since the Guide is a router.
func (m *AppModel) setEngagedAgent(agentID string) {
	normalized := normalizeAgentID(agentID)
	if normalized == "" || normalized == "guide" {
		return
	}
	m.engagedAgentID = normalized
	if m.agentPanel != nil {
		m.agentPanel.SetEngagedAgent(normalized)
	}
	if m.statusBar != nil {
		m.statusBar.SetEngagedAgent(normalized)
	}
}

// clearEngagedAgent removes engagement tracking, forcing full classification.
func (m *AppModel) clearEngagedAgent() {
	m.engagedAgentID = ""
	if m.agentPanel != nil {
		m.agentPanel.ClearEngagedAgent()
	}
	if m.statusBar != nil {
		m.statusBar.SetEngagedAgent("")
	}
}

// handleStreamReroute processes a reroute notification from the Guide.
// It transitions the active stream from the original correlationID to the
// rerouted one so that stream events from the new target agent are rendered.
func (m *AppModel) handleStreamReroute(reroute msg.StreamRerouteMsg) tea.Cmd {
	return m.applyStreamTransfer(reroute, true)
}

func (m *AppModel) applyStreamTransfer(reroute msg.StreamRerouteMsg, bootstrapNewStream bool) tea.Cmd {
	var startCmd tea.Cmd
	// Guard interrupted correlations — drop reroutes for dead requests.
	if reroute.OriginalCorrelationID != "" {
		if _, interrupted := m.interruptedCorrelations[reroute.OriginalCorrelationID]; interrupted {
			return nil
		}
	}
	if reroute.CorrelationID != "" {
		if _, interrupted := m.interruptedCorrelations[reroute.CorrelationID]; interrupted {
			return nil
		}
	}
	// Remove the old stream to unblock new stream events.
	if reroute.OriginalCorrelationID != "" {
		m.markReroutedStreamCID(reroute.OriginalCorrelationID)
		m.unregisterStream(reroute.OriginalCorrelationID)
	}
	// Register the new stream for the rerouted agent.
	if bootstrapNewStream && reroute.CorrelationID != "" {
		start, created := m.prepareStreamStart(msg.StreamStartMsg{
			SessionID:     reroute.SessionID,
			CorrelationID: reroute.CorrelationID,
			AgentID:       normalizeAgentID(reroute.ToAgentID),
			AgentType:     normalizeAgentID(reroute.ToAgentID),
			AgentName:     reroute.ToAgentID,
		})
		if created {
			startCmd = m.propagate(start)
		}
	}
	// Demote the handing-off agent (e.g. guide) so it no longer shows as active.
	if reroute.FromAgentID != "" && m.agentPanel != nil {
		m.agentPanel.DemoteAgent(normalizeAgentID(reroute.FromAgentID))
	}
	m.clearEngagedAgent()
	if reroute.ToAgentID != "" {
		m.setEngagedAgent(reroute.ToAgentID)
	}
	if reroute.FromAgentID != "" && m.statusBar != nil {
		m.statusBar.SetFlash(reroute.FromAgentID + " -> " + reroute.ToAgentID)
	}
	rerouteCmd := m.propagate(reroute)
	if startCmd != nil {
		return tea.Batch(rerouteCmd, startCmd)
	}
	return rerouteCmd
}

func (m *AppModel) observeTopLevelStreamTransfer(
	sessionID, parentCorrelationID, correlationID, toAgentID string,
	branchRef *msg.InterAgentBranchRefMsg,
	topLevelTransfer bool,
) tea.Cmd {
	parentCorrelationID = strings.TrimSpace(parentCorrelationID)
	correlationID = strings.TrimSpace(correlationID)
	if !isExplicitTopLevelTransfer(parentCorrelationID, branchRef, topLevelTransfer) || correlationID == "" || parentCorrelationID == correlationID {
		return nil
	}
	fromAgentID := ""
	if entry := m.streamEntryForCorrelation(parentCorrelationID); entry != nil {
		fromAgentID = firstNonEmpty(strings.TrimSpace(entry.AgentID), strings.TrimSpace(entry.AgentType))
	}
	return m.applyStreamTransfer(msg.StreamRerouteMsg{
		SessionID:             sessionID,
		OriginalCorrelationID: parentCorrelationID,
		CorrelationID:         correlationID,
		FromAgentID:           fromAgentID,
		ToAgentID:             strings.TrimSpace(toAgentID),
		Reason:                "ownership transfer",
	}, false)
}

func (m *AppModel) resolveInterruptTarget() (string, string) {
	// Prefer the selected/engaged agent's active stream.
	selected := m.manualTargetAgent
	if selected == "" {
		selected = m.engagedAgentID
	}
	if stream := m.activeStreamForAgent(selected); stream != nil {
		return stream.CorrelationID, stream.AgentID
	}
	// Fallback: most recently started active stream.
	if len(m.activeStreams) > 0 {
		var best *activeStreamEntry
		for _, entry := range m.activeStreams {
			if best == nil || entry.StartedAt.After(best.StartedAt) {
				best = entry
			}
		}
		if best != nil {
			return best.CorrelationID, best.AgentID
		}
	}
	return m.latestStreamUsage()
}

func (m *AppModel) resolveRouteSessionID(candidate string) string {
	if sessionID := strings.TrimSpace(candidate); sessionID != "" {
		return sessionID
	}
	if m != nil && m.deps.SessionManager != nil {
		if active, ok := m.deps.SessionManager.GetActive(); ok && active != nil {
			if sessionID := strings.TrimSpace(active.ID()); sessionID != "" {
				return sessionID
			}
		}
	}
	return defaultGuideSessionID
}

func (m *AppModel) latestStreamUsage() (string, string) {
	if len(m.streamUsage) == 0 {
		return "", ""
	}
	correlationID := ""
	agentID := ""
	var latest time.Time
	for cid, usage := range m.streamUsage {
		if correlationID == "" || usage.StartedAt.After(latest) {
			correlationID = cid
			agentID = usage.AgentID
			latest = usage.StartedAt
		}
	}
	return correlationID, agentID
}

func (m *AppModel) handleQuit() tea.Cmd {
	return tea.Quit
}

func (m *AppModel) handleTick(tick msg.TickMsg) tea.Cmd {
	// Drop ticks from invalidated chains (e.g., after slow→fast upgrade).
	if tick.Gen != m.tickGen {
		return nil
	}
	if m.animationsSuspended(tick.Time) {
		return m.continueTickChain()
	}

	// Scroll momentum and bounce.
	if !m.scroll.settled() {
		m.tickScrollMomentum()
		m.viewDirty = true
	}
	m.tickSwipeDecay()

	// Drain pending commands from scroll-triggered pagination.
	var cmds []tea.Cmd
	if m.gitPanel != nil {
		if cmd := m.gitPanel.DrainCmd(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if m.commitTree != nil {
		if cmd := m.commitTree.DrainCmd(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	cmds = append(cmds, m.continueTickChain())
	return tea.Batch(cmds...)
}

func (m *AppModel) handleDecorTick(tick msg.DecorTickMsg) tea.Cmd {
	if tick.Gen != m.decorGen {
		return nil
	}
	if m.animationsSuspended(tick.Time) {
		return m.continueDecorTickChain()
	}

	changed := false
	changed = m.advanceStatusDecor(tick, changed)
	changed = m.advanceChatDecor(tick, changed)
	changed = m.expireTabArrowFlash(tick.Time, changed)
	changed = m.refreshStreamingRingHint(changed)
	changed = m.advanceQueueStripDecor(changed)
	changed = m.advanceRightPanelDecor(changed)
	changed = m.advanceGitPanelDecor(changed)
	changed = m.advanceSidebarDecor(tick.Time, changed)
	changed = m.advanceFocusDecor(tick.Time, changed)
	if m.viewMode == ViewMemory && m.refreshMemoryView(false) {
		changed = true
	}

	if changed {
		m.viewDirty = true
	}

	return m.continueDecorTickChain()
}

func (m *AppModel) advanceStatusDecor(tick msg.DecorTickMsg, changed bool) bool {
	if !m.statusBar.IsAnimating() {
		return changed
	}
	model, cmd := m.statusBar.Update(tick)
	_ = cmd
	m.statusBar = model.(*status.Model)
	return changed || m.statusBar.ViewDirty()
}

func (m *AppModel) advanceChatDecor(tick msg.DecorTickMsg, changed bool) bool {
	if !m.chat.HasActiveAnimation() {
		return changed
	}
	chatComp, _ := m.chat.Update(tick)
	m.chat = chatComp.(*chat.Model)
	return true
}

func (m *AppModel) expireTabArrowFlash(now time.Time, changed bool) bool {
	tabFlashChanged := false
	if (m.tabArrowFlashLeftUntil != (time.Time{})) && !now.Before(m.tabArrowFlashLeftUntil) {
		m.tabArrowFlashLeftUntil = time.Time{}
		tabFlashChanged = true
	}
	if (m.tabArrowFlashRightUntil != (time.Time{})) && !now.Before(m.tabArrowFlashRightUntil) {
		m.tabArrowFlashRightUntil = time.Time{}
		tabFlashChanged = true
	}
	if tabFlashChanged {
		m.markSlotDirty(compositor.SlotRight)
		return true
	}
	return changed
}

func (m *AppModel) refreshStreamingRingHint(changed bool) bool {
	if (m.leftRing.empty() && m.rightRing.empty()) || !m.chat.IsStreaming() {
		return changed
	}
	m.statusBar.SetViewRingHint(m.buildRingHint())
	return true
}

func (m *AppModel) advanceQueueStripDecor(changed bool) bool {
	if m.promptQueue.IsEmpty() || m.promptQueue.IsPaused() {
		return changed
	}
	m.markSlotDirty(compositor.SlotQueue)
	return true
}

func (m *AppModel) advanceRightPanelDecor(changed bool) bool {
	if m.commitTree != nil && m.commitTree.NeedsDecorTick() {
		m.markSlotDirty(compositor.SlotRight)
		changed = true
	}
	if m.diffViewActive && m.diffView != nil && m.diffView.NeedsDecorTick() {
		m.diffView.AdvanceSpinner()
		m.markSlotDirty(compositor.SlotRight)
		changed = true
	}
	if m.mergeDiffViewActive && m.mergeDiffView != nil && m.mergeDiffView.NeedsDecorTick() {
		m.mergeDiffView.AdvanceSpinner()
		m.markSlotDirty(compositor.SlotRight)
		changed = true
	}
	if m.conflictViewActive && m.conflictView != nil && m.conflictView.NeedsDecorTick() {
		m.markSlotDirty(compositor.SlotRight)
		changed = true
	}
	if m.planView != nil && m.planView.NeedsDecorTick() {
		m.planView.MarkViewDirty()
		m.markSlotDirty(compositor.SlotRight)
		changed = true
	}
	return changed
}

func (m *AppModel) advanceGitPanelDecor(changed bool) bool {
	if m.viewMode != ViewGit || m.gitPanel == nil || !m.gitPanel.NeedsDecorTick() {
		return changed
	}
	m.gitPanel.MarkViewDirty()
	m.markSlotDirty(m.sidebarFileListSlot())
	return true
}

func (m *AppModel) advanceSidebarDecor(now time.Time, changed bool) bool {
	agentActive := m.hasActiveAgent()
	if m.agentPanel != nil && m.agentPanel.AdvanceDecor(now) {
		m.markSlotDirty(compositor.SlotLeft)
		changed = true
	}
	if m.sessionPanel != nil {
		m.sessionPanel.SetAgentActive(agentActive)
	}
	if agentActive && m.sessionPanel != nil {
		m.sessionPanel.AdvanceDotFrame()
		m.markSlotDirty(compositor.SlotLeft)
		changed = true
	}
	return changed
}

func (m *AppModel) advanceFocusDecor(now time.Time, changed bool) bool {
	m.focusGradient = m.currentFocusGradient()
	if m.input != nil && m.focusGradient != nil && m.input.CanScroll() {
		if m.input.SetScrollIndicatorColor(m.focusGradient.Sample(now.Sub(m.focusRingStart))) {
			m.markSlotDirty(compositor.SlotInput)
			changed = true
		}
	}
	if m.focusBorderFrameChanged(now) {
		m.markSlotBorderDirty(m.focusBorderGroup())
		changed = true
	}
	return changed
}
