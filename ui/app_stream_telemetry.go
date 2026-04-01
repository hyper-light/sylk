package ui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	coreerrors "github.com/adalundhe/sylk/core/errors"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/ui/chat"
	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/conflictview"
	"github.com/adalundhe/sylk/ui/modal"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/redact"
	"github.com/adalundhe/sylk/ui/status"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/google/uuid"
)

func (m *AppModel) handleStreamStartTelemetry(start msg.StreamStartMsg) tea.Cmd {
	// Guard interrupted correlations BEFORE any side effects (registration,
	// agent promotion, flash updates). StreamStart must be allowed to
	// bootstrap a brand-new correlation even while another stream is active;
	// follow-up events will be gated by the active-stream registry.
	correlationID := strings.TrimSpace(start.CorrelationID)
	if correlationID == "" {
		return nil
	}
	if _, interrupted := m.interruptedCorrelations[correlationID]; interrupted {
		return nil
	}
	if m.shouldIgnoreLateStreamBootstrap(correlationID) {
		return nil
	}
	m.clearExplicitTopLevelTransferState(correlationID, start.ParentCorrelationID, start.BranchRef)
	start.BranchRef = m.resolveIncomingStreamBranchRef(correlationID, start.ParentCorrelationID, start.BranchRef)
	transferCmd := m.observeTopLevelStreamTransfer(
		start.SessionID,
		start.ParentCorrelationID,
		start.CorrelationID,
		firstNonEmpty(strings.TrimSpace(start.AgentType), strings.TrimSpace(start.AgentID)),
		start.BranchRef,
	)
	start, _ = m.prepareStreamStart(start)
	if transferCmd != nil {
		return tea.Batch(transferCmd, m.propagate(start))
	}
	return m.propagate(start)
}

func (m *AppModel) prepareStreamStart(start msg.StreamStartMsg) (msg.StreamStartMsg, bool) {
	start.RuntimeAgentID = normalizeRuntimeAgentID(start.AgentID, firstNonEmpty(start.RuntimeAgentID, start.AgentID))
	start.AgentID = canonicalStreamAgentID(start.AgentID, start.AgentType, start.PipelineID, start.TaskID)
	m.recordStreamStart(start.CorrelationID)
	m.trackStreamStart(start)
	created := m.registerStream(start)
	if start.BranchRef == nil {
		newAgent := normalizeAgentID(start.AgentID)
		if newAgent != "" && newAgent != "guide" && m.agentPanel != nil {
			m.agentPanel.DemoteAgent("guide")
		}
		if m.statusBar != nil && m.engagedAgentID != "" && newAgent != "" && newAgent != m.engagedAgentID && newAgent != "guide" {
			m.statusBar.SetFlash(m.engagedAgentID + " -> " + newAgent)
		}
		if created {
			m.publishStreamStartActivity(start)
		}
	}
	if m.statusBar != nil {
		m.statusBar.SetTokenPhase(status.PhaseOutput)
	}
	return start, created
}

func (m *AppModel) handleStreamChunkTelemetry(chunk msg.StreamChunkMsg) tea.Cmd {
	chunk.Text = redactSecrets(chunk.Text)
	m.trackStreamChunk(chunk.CorrelationID, chunk.Text)
	if chunk.InputTokens > 0 {
		m.applyEarlyInputTokens(chunk.CorrelationID, chunk.InputTokens)
	}
	if !m.shouldRenderStreamEvent(chunk.CorrelationID) {
		return nil
	}
	if strings.TrimSpace(chunk.Text) == "" {
		return nil // Usage-only chunk; status bar updated, no chat content to render.
	}
	// Record HadChunk only after confirming the chunk will be rendered.
	// Setting HadChunk before this point would cause shouldSuppressStreamedRouteResponse
	// to suppress the GuideResponseMsg even when no chunks were actually displayed.
	m.recordStreamChunk(chunk.CorrelationID, chunk.Text)
	return m.propagate(chunk)
}

func (m *AppModel) handleStreamProgressTelemetry(progress msg.StreamProgressMsg) tea.Cmd {
	if m.shouldIgnoreLateStreamBootstrap(progress.CorrelationID) {
		return nil
	}
	progress.AgentID = effectiveCanonicalStreamAgentID(
		m.streamEntryForCorrelation(progress.CorrelationID),
		progress.AgentID,
		progress.AgentType,
		progress.PipelineID,
		progress.TaskID,
	)
	if entry := m.streamEntryForCorrelation(progress.CorrelationID); entry != nil {
		progress.RuntimeAgentID = firstNonEmpty(progress.RuntimeAgentID, entry.RuntimeAgentID, progress.AgentID)
	}
	m.clearExplicitTopLevelTransferState(progress.CorrelationID, progress.ParentCorrelationID, progress.BranchRef)
	progress.BranchRef = m.resolveIncomingStreamBranchRef(progress.CorrelationID, progress.ParentCorrelationID, progress.BranchRef)
	transferCmd := m.observeTopLevelStreamTransfer(
		progress.SessionID,
		progress.ParentCorrelationID,
		progress.CorrelationID,
		firstNonEmpty(strings.TrimSpace(progress.AgentType), strings.TrimSpace(progress.AgentID)),
		progress.BranchRef,
	)
	start := msg.StreamStartMsg{
		SessionID:           progress.SessionID,
		CorrelationID:       progress.CorrelationID,
		ParentCorrelationID: progress.ParentCorrelationID,
		AgentID:             progress.AgentID,
		RuntimeAgentID:      progress.RuntimeAgentID,
		AgentType:           progress.AgentType,
		AgentName:           progress.AgentName,
		PipelineID:          progress.PipelineID,
		TaskID:              progress.TaskID,
		TaskName:            progress.TaskName,
		TaskSlug:            progress.TaskSlug,
		BranchRef:           progress.BranchRef,
	}
	start, created := m.prepareStreamStart(start)
	progress.Message = redactSecrets(progress.Message)
	if !m.shouldRenderStreamEvent(progress.CorrelationID) {
		return transferCmd
	}
	if created {
		if transferCmd != nil {
			return tea.Batch(transferCmd, m.propagate(start), m.propagate(progress))
		}
		return tea.Batch(m.propagate(start), m.propagate(progress))
	}
	if transferCmd != nil {
		return tea.Batch(transferCmd, m.propagate(progress))
	}
	return m.propagate(progress)
}

func (m *AppModel) handleToolCallTelemetry(ev msg.ToolCallEventMsg) tea.Cmd {
	ev.AgentID = effectiveCanonicalStreamAgentID(
		m.streamEntryForCorrelation(ev.CorrelationID),
		ev.AgentID,
		ev.AgentType,
		ev.PipelineID,
		ev.TaskID,
	)
	m.clearExplicitTopLevelTransferState(ev.CorrelationID, ev.ParentCorrelationID, ev.BranchRef)
	ev.BranchRef = m.resolveIncomingStreamBranchRef(ev.CorrelationID, ev.ParentCorrelationID, ev.BranchRef)
	explicitTopLevelTransfer := isExplicitTopLevelTransfer(ev.ParentCorrelationID, ev.BranchRef)
	transferCmd := m.observeTopLevelStreamTransfer(
		ev.SessionID,
		ev.ParentCorrelationID,
		ev.CorrelationID,
		firstNonEmpty(strings.TrimSpace(ev.AgentType), strings.TrimSpace(ev.AgentID)),
		ev.BranchRef,
	)

	correlationID := strings.TrimSpace(ev.CorrelationID)
	if !explicitTopLevelTransfer &&
		correlationID != "" &&
		m.hasRecordedStreamCorrelation(correlationID) &&
		m.chat != nil &&
		m.chat.HasPendingCorrelation(correlationID) {
		if transferCmd != nil {
			return tea.Batch(transferCmd, m.propagate(ev))
		}
		return m.propagate(ev)
	}
	if !explicitTopLevelTransfer && correlationID != "" && ev.Phase == 1 && m.hasRecordedStreamCorrelation(correlationID) {
		if m.shouldRenderToolCallTerminalEvent(correlationID) {
			if transferCmd != nil {
				return tea.Batch(transferCmd, m.propagate(ev))
			}
			return m.propagate(ev)
		}
		return transferCmd
	}
	if m.shouldIgnoreLateStreamBootstrap(correlationID) {
		return nil
	}
	if correlationID != "" {
		if _, rerouted := m.reroutedStreamCIDs[correlationID]; !rerouted {
			start := msg.StreamStartMsg{
				SessionID:           ev.SessionID,
				CorrelationID:       ev.CorrelationID,
				ParentCorrelationID: ev.ParentCorrelationID,
				AgentID:             ev.AgentID,
				RuntimeAgentID:      ev.AgentID,
				AgentType:           ev.AgentType,
				AgentName:           ev.AgentName,
				PipelineID:          ev.PipelineID,
				TaskID:              ev.TaskID,
				TaskName:            ev.TaskName,
				TaskSlug:            ev.TaskSlug,
				BranchRef:           ev.BranchRef,
			}
			start, created := m.prepareStreamStart(start)
			if !m.shouldRenderStreamEvent(ev.CorrelationID) {
				return transferCmd
			}
			if created {
				if transferCmd != nil {
					return tea.Batch(transferCmd, m.propagate(start), m.propagate(ev))
				}
				return tea.Batch(m.propagate(start), m.propagate(ev))
			}
			if transferCmd != nil {
				return tea.Batch(transferCmd, m.propagate(ev))
			}
			return m.propagate(ev)
		}
	}

	if !m.shouldRenderTerminalStreamEvent(ev.CorrelationID) {
		return transferCmd
	}
	if transferCmd != nil {
		return tea.Batch(transferCmd, m.propagate(ev))
	}
	return m.propagate(ev)
}

func (m *AppModel) handleStreamCompleteTelemetry(done msg.StreamCompleteMsg) tea.Cmd {
	done.AgentID = effectiveCanonicalStreamAgentID(
		m.streamEntryForCorrelation(done.CorrelationID),
		done.AgentID,
		done.AgentType,
		done.PipelineID,
		done.TaskID,
	)
	if entry := m.streamEntryForCorrelation(done.CorrelationID); entry != nil {
		done.RuntimeAgentID = firstNonEmpty(done.RuntimeAgentID, entry.RuntimeAgentID, done.AgentID)
	}
	m.clearExplicitTopLevelTransferState(done.CorrelationID, done.ParentCorrelationID, done.BranchRef)
	done.BranchRef = m.resolveIncomingStreamBranchRef(done.CorrelationID, done.ParentCorrelationID, done.BranchRef)
	uiDebugFileLog().Info("AppModel: STREAM_COMPLETE_RECEIVED",
		"correlation_id", done.CorrelationID,
		"agent_id", done.AgentID,
		"active_streams", len(m.activeStreams),
		"authoritative_text_len", len(done.AuthoritativeText))
	m.recordStreamComplete(done.CorrelationID)
	shouldRender := m.shouldRenderTerminalStreamEvent(done.CorrelationID)
	shouldPropagate := shouldRender || done.BranchRef != nil
	uiDebugFileLog().Info("AppModel: STREAM_COMPLETE_SHOULD_RENDER",
		"correlation_id", done.CorrelationID,
		"should_render", shouldRender,
		"should_propagate", shouldPropagate)
	m.applyRealStreamUsage(done)
	m.finalizeStreamUsage(done.CorrelationID, true, "")
	m.markQueueEntryByCorrelation(done.CorrelationID, true)
	m.unregisterStream(done.CorrelationID)
	m.clearReroutedStreamCID(done.CorrelationID)
	if !shouldPropagate {
		uiDebugFileLog().Warn("AppModel: STREAM_COMPLETE_NOT_RENDERED",
			"correlation_id", done.CorrelationID)
		m.statusBar.StopSpinner()
		return m.tryAdvanceQueue()
	}
	// AuthoritativeText in the completion event delivers final content to the
	// chat accumulator, equivalent to having received text chunks. Record it
	// so shouldSuppressStreamedRouteResponse suppresses the duplicate
	// GuideResponseMsg that follows.
	if strings.TrimSpace(done.AuthoritativeText) != "" {
		m.recordStreamChunk(done.CorrelationID, done.AuthoritativeText)
	}
	if advCmd := m.tryAdvanceQueue(); advCmd != nil {
		return tea.Batch(m.propagate(done), advCmd)
	}
	return m.propagate(done)
}

// isTerminalStreamError returns true when the error represents a condition
// that no guide-level retry, reroute, or circuit-breaker recovery can fix.
// For these errors the agent panel animations should stop immediately.
//
// Non-terminal errors (rate-limit exhaustion, server 5xx, timeouts) may still
// be recovered by the Guide via reroute or retry-queue, so animations persist
// until either a terminal event or a successful recovery arrives.
func isTerminalStreamError(err error) bool {
	if err == nil {
		return false
	}

	// Tiered error taxonomy: Permanent and UserFixable are unrecoverable.
	var te *coreerrors.TieredError
	if errors.As(err, &te) {
		return te.Tier == coreerrors.TierPermanent || te.Tier == coreerrors.TierUserFixable
	}

	// Provider errors: non-retryable status codes (401, 402, 403, 400, 404)
	// indicate credential, permission, or configuration problems the Guide
	// cannot route around.
	var pe *providers.ProviderError
	if errors.As(err, &pe) {
		return !pe.Retryable
	}

	// Sentinel errors from the providers package.
	switch {
	case errors.Is(err, providers.ErrAuthenticationError):
		return true
	case errors.Is(err, providers.ErrQuotaExceeded):
		return true
	case errors.Is(err, providers.ErrProviderNotFound):
		return true
	case errors.Is(err, providers.ErrModelNotSupported):
		return true
	case errors.Is(err, providers.ErrInvalidConfig):
		return true
	}

	return false
}

func (m *AppModel) handleStreamErrorTelemetry(streamErr msg.StreamErrorMsg) tea.Cmd {
	streamErr.Err = redactedError(streamErr.Err)
	streamErr.BranchRef = m.effectiveStreamBranchRef(streamErr.CorrelationID, streamErr.BranchRef)
	summary := ""
	if streamErr.Err != nil {
		summary = streamErr.Err.Error()
	}
	if m.shouldSuppressErrorAfterSuccess(streamErr.CorrelationID) {
		m.logSuppressedLLMError("stream", streamErr.CorrelationID, m.streamAgentID(streamErr.CorrelationID), streamErr.Err, "success_already_returned")
		m.discardStreamUsage(streamErr.CorrelationID)
		return nil
	}
	m.clearRecordedStream(streamErr.CorrelationID)
	m.finalizeStreamUsage(streamErr.CorrelationID, false, summary)
	m.markQueueEntryByCorrelation(streamErr.CorrelationID, false)
	// Resolve the responding agent before unregistering the stream (which
	// removes the correlation→agent mapping).
	errorAgentID := m.streamAgentID(streamErr.CorrelationID)
	m.unregisterStream(streamErr.CorrelationID)
	m.clearReroutedStreamCID(streamErr.CorrelationID)
	// The stream is over — demote the responding agent so its panel card
	// transitions from working (Thinking/Acting) back to Idle. For terminal
	// errors demote all active agents and pause the queue.
	if isTerminalStreamError(streamErr.Err) {
		m.agentPanel.DemoteAllActive()
		// Pause the queue on terminal errors to prevent blindly dispatching
		// into a broken agent.
		m.promptQueue.SetPaused(true)
		m.recalcLayout()
		m.viewDirty = true
	} else {
		m.agentPanel.DemoteAgent(errorAgentID)
	}
	if advCmd := m.tryAdvanceQueue(); advCmd != nil {
		return tea.Batch(m.propagate(streamErr), advCmd)
	}
	return m.propagate(streamErr)
}

func (m *AppModel) trackStreamStart(start msg.StreamStartMsg) {
	correlationID := strings.TrimSpace(start.CorrelationID)
	if correlationID == "" {
		return
	}
	logicalPipelineID := logicalStreamPipelineID(start.PipelineID, start.TaskID)
	canonicalAgentID := canonicalStreamAgentID(start.AgentID, start.AgentType, start.PipelineID, start.TaskID)
	runtimeAgentID := normalizeRuntimeAgentID(canonicalAgentID, firstNonEmpty(start.RuntimeAgentID, start.AgentID))
	startIdentity := normalizeAgentID(firstNonEmpty(canonicalAgentID, start.AgentType, start.AgentID))
	entry, ok := m.streamUsage[correlationID]
	if !ok {
		entry = streamUsageEntry{StartedAt: time.Now()}
	} else {
		existingIdentity := normalizeAgentID(firstNonEmpty(entry.AgentID, entry.AgentType))
		if existingIdentity != "" && startIdentity != "" && existingIdentity != startIdentity {
			// A shared correlation can move across agents during reroute or
			// orchestration. Reset per-stream counters so the next agent's
			// authoritative usage does not replace the prior agent's already
			// accumulated session totals.
			entry = streamUsageEntry{StartedAt: time.Now()}
		}
	}
	entry.AgentID = canonicalAgentID
	entry.RuntimeAgentID = runtimeAgentID
	entry.AgentType = strings.TrimSpace(start.AgentType)
	entry.AgentName = strings.TrimSpace(start.AgentName)
	entry.PipelineID = logicalPipelineID
	entry.TaskID = strings.TrimSpace(start.TaskID)
	entry.TaskName = strings.TrimSpace(start.TaskName)
	entry.TaskSlug = strings.TrimSpace(start.TaskSlug)
	if entry.StartedAt.IsZero() {
		entry.StartedAt = time.Now()
	}
	m.streamUsage[correlationID] = entry
}

func (m *AppModel) trackStreamChunk(correlationID, text string) {
	if correlationID == "" {
		return
	}
	state, ok := m.streamUsage[correlationID]
	if !ok {
		return
	}
	added := estimateGuideTokens(text)
	state.Tokens += added
	m.streamUsage[correlationID] = state
	m.totalCompletionTokens += added
	if m.statusBar != nil {
		m.updateTokenDisplay()
	}
}

func (m *AppModel) recordStreamStart(correlationID string) {
	if correlationID == "" {
		return
	}
	m.ensureStreamedResponseState()
	m.pruneRecordedStreams(time.Now())
	m.streamedResponses[correlationID] = streamedResponseState{
		HadChunk:  false,
		Completed: false,
		Succeeded: false,
		SeenAt:    time.Now(),
	}
}

func (m *AppModel) recordStreamChunk(correlationID, text string) {
	if correlationID == "" || strings.TrimSpace(text) == "" {
		return
	}
	m.ensureStreamedResponseState()
	state := m.streamedResponses[correlationID]
	state.HadChunk = true
	state.SeenAt = time.Now()
	m.streamedResponses[correlationID] = state
}

func (m *AppModel) recordStreamComplete(correlationID string) {
	if correlationID == "" {
		return
	}
	m.ensureStreamedResponseState()
	state := m.streamedResponses[correlationID]
	state.Completed = true
	state.SeenAt = time.Now()
	m.streamedResponses[correlationID] = state
	m.pruneRecordedStreams(time.Now())
}

func (m *AppModel) markSuccessfulRouteResponse(correlationID string) {
	if correlationID == "" {
		return
	}
	m.ensureStreamedResponseState()
	state := m.streamedResponses[correlationID]
	state.Succeeded = true
	state.SeenAt = time.Now()
	m.streamedResponses[correlationID] = state
}

func (m *AppModel) shouldSuppressErrorAfterSuccess(correlationID string) bool {
	if correlationID == "" || m.streamedResponses == nil {
		return false
	}
	state, ok := m.streamedResponses[correlationID]
	if !ok {
		return false
	}
	return state.Succeeded
}

func (m *AppModel) streamAgentID(correlationID string) string {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return guideAgentID
	}
	if usage, ok := m.streamUsage[correlationID]; ok {
		return normalizeAgentID(usage.AgentID)
	}
	if entry := m.streamEntryForCorrelation(correlationID); entry != nil {
		return normalizeAgentID(entry.AgentID)
	}
	return guideAgentID
}

func (m *AppModel) logSuppressedLLMError(kind, correlationID, agentID string, err error, reason string) {
	if m == nil || m.walLogger == nil || err == nil {
		return
	}
	m.walLogger.Warn(
		"ui llm error suppressed",
		"kind", strings.TrimSpace(kind),
		"correlation_id", strings.TrimSpace(correlationID),
		"agent_id", normalizeAgentID(agentID),
		"reason", strings.TrimSpace(reason),
		"error", err.Error(),
	)
}

func (m *AppModel) clearRecordedStream(correlationID string) {
	if correlationID == "" || m.streamedResponses == nil {
		return
	}
	delete(m.streamedResponses, correlationID)
}

func (m *AppModel) shouldSuppressStreamedRouteResponse(correlationID string, hasErr bool) bool {
	if correlationID == "" || hasErr || m.streamedResponses == nil {
		return false
	}
	state, ok := m.streamedResponses[correlationID]
	if !ok {
		return false
	}
	// Suppress when content was already delivered via stream chunks.
	// Progress-only streams (start→complete with no chunks) are not
	// suppressed since they never delivered user-visible content.
	delivered := state.HadChunk
	if state.Succeeded {
		state.SeenAt = time.Now()
		m.streamedResponses[correlationID] = state
		return delivered
	}
	delete(m.streamedResponses, correlationID)
	return delivered
}

func (m *AppModel) ensureStreamedResponseState() {
	if m.streamedResponses != nil {
		return
	}
	m.streamedResponses = make(map[string]streamedResponseState)
}

func (m *AppModel) pruneRecordedStreams(now time.Time) {
	if m.streamedResponses == nil {
		return
	}
	for correlationID, state := range m.streamedResponses {
		if now.Sub(state.SeenAt) <= streamedResponseStateTTL {
			continue
		}
		delete(m.streamedResponses, correlationID)
	}
}

func (m *AppModel) finalizeStreamUsage(correlationID string, success bool, summary string) {
	if correlationID == "" {
		return
	}
	state, ok := m.streamUsage[correlationID]
	if !ok {
		return
	}
	delete(m.streamUsage, correlationID)

	// Input tokens represent the full conversation context sent to the agent,
	// i.e. the actual context window occupancy. Use them directly when available
	// (no decay — each call sends the full history). Fall back to output-based
	// estimation when real input tokens are unavailable.
	if isEphemeralReplicaRuntime(state.RuntimeAgentID) {
		m.clearAgentReplicaContextUsage(state.AgentID, state.RuntimeAgentID)
	} else if state.InputTokens > 0 {
		m.setAgentReplicaContextUsage(state.AgentID, state.RuntimeAgentID, m.agentContextModels[state.AgentID], state.InputTokens)
	} else {
		m.bumpAgentContextUsage(state.AgentID, state.Tokens+guideResponseOverheadTokens)
	}
	m.publishStreamActivity(correlationID, success, summary)

	if m.statusBar != nil && len(m.streamUsage) == 0 {
		m.statusBar.SetTokenPhase(status.PhaseIdle)
	}
}

func (m *AppModel) discardStreamUsage(correlationID string) {
	if correlationID == "" {
		return
	}
	delete(m.streamUsage, correlationID)
	if m.statusBar != nil && len(m.streamUsage) == 0 {
		m.statusBar.SetTokenPhase(status.PhaseIdle)
	}
}

func (m *AppModel) tokenUsageOverlapsActiveStream(correlationID, canonicalAgentID string) bool {
	if correlationID = strings.TrimSpace(correlationID); correlationID != "" {
		if _, ok := m.streamUsage[correlationID]; ok {
			return true
		}
	}
	canonicalAgentID = normalizeAgentID(canonicalAgentID)
	if canonicalAgentID == "" {
		return false
	}
	return m.visibleStreamForAgent(canonicalAgentID) != nil
}

func (m *AppModel) recordStreamInputTokens(correlationID, canonicalAgentID string, inputTokens int) {
	if inputTokens <= 0 {
		return
	}
	if correlationID = strings.TrimSpace(correlationID); correlationID != "" {
		if state, ok := m.streamUsage[correlationID]; ok {
			state.InputTokens = inputTokens
			m.streamUsage[correlationID] = state
		}
		return
	}

	canonicalAgentID = normalizeAgentID(canonicalAgentID)
	if canonicalAgentID == "" {
		return
	}

	latestCID := ""
	var latestStart time.Time
	for cid, state := range m.streamUsage {
		if normalizeAgentID(state.AgentID) != canonicalAgentID {
			continue
		}
		if latestCID == "" || state.StartedAt.After(latestStart) {
			latestCID = cid
			latestStart = state.StartedAt
		}
	}
	if latestCID == "" {
		return
	}

	state := m.streamUsage[latestCID]
	state.InputTokens = inputTokens
	m.streamUsage[latestCID] = state
}

// applyEarlyInputTokens applies real input tokens as soon as the provider
// reports them (at stream start), avoiding the need to wait for completion.
func (m *AppModel) applyEarlyInputTokens(correlationID string, inputTokens int) {
	if inputTokens <= 0 {
		return
	}
	state, ok := m.streamUsage[correlationID]
	if !ok || state.EarlyInputApplied {
		return
	}
	state.EarlyInputApplied = true
	state.InputTokens = inputTokens
	m.streamUsage[correlationID] = state
	m.totalPromptTokens += inputTokens
	if m.statusBar != nil {
		m.updateTokenDisplay()
	}
}

// applyRealStreamUsage corrects the accumulated token estimate with real
// provider-reported values when available. Called before finalizeStreamUsage
// so the corrected values are used for context usage computation.
func (m *AppModel) applyRealStreamUsage(done msg.StreamCompleteMsg) {
	if done.InputTokens == 0 && done.OutputTokens == 0 {
		return
	}
	state, ok := m.streamUsage[done.CorrelationID]
	if !ok {
		return
	}
	if done.OutputTokens > 0 {
		m.totalCompletionTokens -= state.Tokens
		m.totalCompletionTokens += done.OutputTokens
		state.Tokens = done.OutputTokens
		m.streamUsage[done.CorrelationID] = state
	}
	if done.InputTokens > 0 {
		// StreamComplete carries request-wide accumulated input tokens for
		// multi-turn loops. Preserve the last per-call occupancy when we've
		// already observed it via TokenUsageMsg or early stream telemetry.
		if state.InputTokens == 0 {
			state.InputTokens = done.InputTokens
			m.streamUsage[done.CorrelationID] = state
		}
		if !state.EarlyInputApplied {
			m.totalPromptTokens += done.InputTokens
		}
	}
	m.totalCacheReadTokens += done.CacheReadTokens
	m.totalCacheWriteTokens += done.CacheWriteTokens
	m.totalReasoningTokens += done.ReasoningTokens
	m.updateTokenDisplay()
}

// updateTokenDisplay pushes cumulative token counts to the status bar.
// Prefer the live stream totals so usage keeps accumulating across follow-on
// streams within the same request. Bus totals are retained as a fallback for
// paths that report token usage outside the visible stream lifecycle.
func (m *AppModel) updateTokenDisplay() {
	if m.statusBar == nil {
		return
	}
	m.statusBar.SetTokens(
		m.totalPromptTokens+m.backgroundPromptTokens,
		m.totalCompletionTokens+m.backgroundCompletionTokens,
		m.totalCacheReadTokens+m.backgroundCacheReadTokens,
		m.totalReasoningTokens+m.backgroundReasoningTokens,
	)
}

func normalizeAgentID(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return guideAgentID
	}
	return normalized
}

func normalizeRuntimeAgentID(panelAgentID, runtimeAgentID string) string {
	runtimeAgentID = normalizeAgentID(runtimeAgentID)
	if runtimeAgentID != "" {
		return runtimeAgentID
	}
	return normalizeAgentID(panelAgentID)
}

func runtimeContextKey(panelAgentID, runtimeAgentID string) string {
	panelAgentID = normalizeAgentID(panelAgentID)
	runtimeAgentID = normalizeRuntimeAgentID(panelAgentID, runtimeAgentID)
	if panelAgentID == "" {
		return runtimeAgentID
	}
	return panelAgentID + "|" + runtimeAgentID
}

func isEphemeralReplicaRuntime(runtimeAgentID string) bool {
	return strings.Contains(strings.TrimSpace(runtimeAgentID), "#replica-")
}

func redactedError(err error) error {
	return redact.Error(err)
}

func (m *AppModel) streamIdentityForCorrelation(correlationID string) (string, string, string, map[string]any) {
	var (
		agentID    string
		runtimeID  string
		agentName  string
		agentType  string
		pipelineID string
		taskID     string
		taskName   string
		taskSlug   string
	)
	if entry := m.streamEntryForCorrelation(correlationID); entry != nil {
		agentID = entry.AgentID
		runtimeID = entry.RuntimeAgentID
		agentName = entry.AgentName
		agentType = entry.AgentType
		pipelineID = entry.PipelineID
		taskID = entry.TaskID
		taskName = entry.TaskName
		taskSlug = entry.TaskSlug
	}
	if usage, ok := m.streamUsage[strings.TrimSpace(correlationID)]; ok {
		agentID = firstNonEmpty(agentID, usage.AgentID)
		runtimeID = firstNonEmpty(runtimeID, usage.RuntimeAgentID)
		agentName = firstNonEmpty(agentName, usage.AgentName)
		agentType = firstNonEmpty(agentType, usage.AgentType)
		pipelineID = firstNonEmpty(pipelineID, usage.PipelineID)
		taskID = firstNonEmpty(taskID, usage.TaskID)
		taskName = firstNonEmpty(taskName, usage.TaskName)
		taskSlug = firstNonEmpty(taskSlug, usage.TaskSlug)
	}
	pipelineID = logicalStreamPipelineID(pipelineID, taskID)
	canonicalID := streamPanelAgentID(agentID, agentType, pipelineID)
	if canonicalID == "" {
		canonicalID = normalizeAgentID(firstNonEmpty(agentID, agentType, guideAgentID))
	}
	if strings.TrimSpace(agentName) == "" {
		agentName = canonicalID
	}
	if strings.TrimSpace(agentType) == "" {
		if m.agentPanel != nil {
			agentType = m.agentPanel.AgentTypeOf(canonicalID)
		}
	}
	if strings.TrimSpace(agentType) == "" {
		agentType = strings.ToLower(strings.TrimSpace(agentName))
	}
	data := map[string]any{
		"agent_type": agentType,
		"agent_name": agentName,
	}
	if pipelineID = strings.TrimSpace(pipelineID); pipelineID != "" {
		data["pipeline_id"] = pipelineID
	}
	if taskID = strings.TrimSpace(taskID); taskID != "" {
		data["task_id"] = taskID
	}
	if taskName = strings.TrimSpace(taskName); taskName != "" {
		data["task_name"] = taskName
	}
	if taskSlug = strings.TrimSpace(taskSlug); taskSlug != "" {
		data["task_slug"] = taskSlug
	}
	if runtimeID = strings.TrimSpace(firstNonEmpty(runtimeID, agentID)); runtimeID != "" && runtimeID != canonicalID {
		data["runtime_agent_id"] = runtimeID
	}
	return canonicalID, agentName, agentType, data
}

func (m *AppModel) publishStreamActivity(correlationID string, success bool, summary string) {
	if m.deps.ActivityPub == nil {
		return
	}
	id, name, agentType, data := m.streamIdentityForCorrelation(correlationID)
	eventType := events.EventTypeLLMResponse
	outcome := events.OutcomeSuccess
	content := "Streaming response complete"
	if !success {
		eventType = events.EventTypeAgentError
		outcome = events.OutcomeFailure
		content = "Streaming response failed"
		if trimmed := strings.TrimSpace(summary); trimmed != "" {
			content = summarizeActivityContent(trimmed)
		}
	}
	data["agent_name"] = name
	data["agent_type"] = agentType
	m.deps.ActivityPub.PublishActivity(&events.ActivityEvent{
		ID:        uuid.New().String(),
		EventType: eventType,
		Timestamp: time.Now(),
		AgentID:   id,
		Content:   content,
		Outcome:   outcome,
		Data:      data,
	})
}

func (m *AppModel) publishStreamStartActivity(start msg.StreamStartMsg) {
	if m.deps.ActivityPub == nil {
		return
	}
	pipelineID := logicalStreamPipelineID(start.PipelineID, start.TaskID)
	canonicalID := streamPanelAgentID(start.AgentID, start.AgentType, pipelineID)
	panelAgentType := ""
	if m.agentPanel != nil {
		panelAgentType = m.agentPanel.AgentTypeOf(canonicalID)
	}
	data := map[string]any{
		"agent_type": firstNonEmpty(start.AgentType, panelAgentType),
		"agent_name": firstNonEmpty(start.AgentName, canonicalID),
	}
	if pipelineID != "" {
		data["pipeline_id"] = pipelineID
	}
	if taskID := strings.TrimSpace(start.TaskID); taskID != "" {
		data["task_id"] = taskID
	}
	if taskName := strings.TrimSpace(start.TaskName); taskName != "" {
		data["task_name"] = taskName
	}
	if taskSlug := strings.TrimSpace(start.TaskSlug); taskSlug != "" {
		data["task_slug"] = taskSlug
	}
	if runtimeID := strings.TrimSpace(firstNonEmpty(start.RuntimeAgentID, start.AgentID)); runtimeID != "" && runtimeID != canonicalID {
		data["runtime_agent_id"] = runtimeID
	}
	m.deps.ActivityPub.PublishActivity(&events.ActivityEvent{
		ID:        uuid.New().String(),
		EventType: events.EventTypeLLMRequest,
		Timestamp: time.Now(),
		AgentID:   canonicalID,
		Content:   "Streaming response started",
		Outcome:   events.OutcomePending,
		Data:      data,
	})
}

func (m *AppModel) handleGuideResponse(r msg.GuideResponseMsg) tea.Cmd {
	// Guard interrupted correlations — drop guide responses for dead requests.
	if r.CorrelationID != "" {
		if _, interrupted := m.interruptedCorrelations[r.CorrelationID]; interrupted {
			return nil
		}
		// Route responses are terminal for the correlation even when they did not
		// arrive through the stream transport. Record completion before any
		// synthetic StreamComplete translation so late nested progress/start
		// events cannot resurrect child chat spinners after approval finished.
		m.recordStreamComplete(r.CorrelationID)
	}
	if r.Err == nil {
		m.markSuccessfulRouteResponse(r.CorrelationID)
	}
	if r.Err != nil && m.shouldSuppressErrorAfterSuccess(r.CorrelationID) {
		m.logSuppressedLLMError("route", r.CorrelationID, r.AgentID, r.Err, "success_already_returned")
		m.clearRecordedStream(r.CorrelationID)
		m.discardStreamUsage(r.CorrelationID)
		m.statusBar.StopSpinner()
		return nil
	}
	if m.shouldSuppressStreamedRouteResponse(r.CorrelationID, r.Err != nil) {
		m.unregisterStream(r.CorrelationID)
		m.discardStreamUsage(r.CorrelationID)
		m.statusBar.StopSpinner()
		return nil
	}
	source := chat.SourceAgent
	content := redactSecrets(r.Content)
	if r.Err != nil {
		source = chat.SourceError
		content = redactSecrets(r.Err.Error())
	}
	effectiveBranchRef := m.effectiveStreamBranchRef(r.CorrelationID, r.BranchRef)
	hasPendingChatCorrelation := m.chat != nil && m.chat.HasPendingCorrelation(r.CorrelationID)
	streamEntry := cloneActiveStreamEntry(m.streamEntryForCorrelation(r.CorrelationID))
	m.unregisterStream(r.CorrelationID)
	added := estimateGuideTokens(content)
	contextAgentID := r.AgentID
	if streamEntry != nil {
		contextAgentID = firstNonEmpty(streamEntry.AgentID, contextAgentID)
	}
	m.bumpAgentContextUsage(contextAgentID, added+guideResponseOverheadTokens)
	m.totalCompletionTokens += added
	m.updateTokenDisplay()
	m.statusBar.SetTokenPhase(status.PhaseIdle)
	m.publishResponseActivity(r, source, content, streamEntry)
	if effectiveBranchRef != nil || hasPendingChatCorrelation {
		entryAgentID := strings.TrimSpace(r.AgentID)
		entryAgentType := strings.TrimSpace(r.AgentType)
		if entryAgentType == "" {
			resolvedID, _, resolvedType := m.resolveAgentIdentity(entryAgentID, r.AgentName)
			if entryAgentID == "" {
				entryAgentID = resolvedID
			}
			entryAgentType = resolvedType
		}
		var (
			comp component.Component
			cmd  tea.Cmd
		)
		if r.Err != nil {
			uiDebugFileLog().Info("AppModel: ROUTE_RESPONSE_SYNTHETIC_STREAM_ERROR",
				"correlation_id", r.CorrelationID,
				"has_branch_ref", effectiveBranchRef != nil,
				"has_pending_chat_correlation", hasPendingChatCorrelation)
			comp, cmd = m.chat.Update(msg.StreamErrorMsg{
				CorrelationID: r.CorrelationID,
				Err:           r.Err,
				BranchRef:     effectiveBranchRef,
			})
		} else {
			uiDebugFileLog().Info("AppModel: ROUTE_RESPONSE_SYNTHETIC_STREAM_COMPLETE",
				"correlation_id", r.CorrelationID,
				"has_branch_ref", effectiveBranchRef != nil,
				"has_pending_chat_correlation", hasPendingChatCorrelation,
				"content_len", len(content))
			comp, cmd = m.chat.Update(msg.StreamCompleteMsg{
				CorrelationID:     r.CorrelationID,
				AgentID:           entryAgentID,
				AgentType:         entryAgentType,
				AuthoritativeText: content,
				BranchRef:         effectiveBranchRef,
			})
		}
		m.chat = comp.(*chat.Model)
		return cmd
	}
	streamTaskID := ""
	streamTaskName := ""
	streamTaskSlug := ""
	entryAgentID := strings.TrimSpace(r.AgentID)
	entryAgentType := ""
	if streamEntry != nil {
		entryAgentID = firstNonEmpty(streamEntry.AgentID, entryAgentID)
		entryAgentType = strings.TrimSpace(streamEntry.AgentType)
		streamTaskID = strings.TrimSpace(streamEntry.TaskID)
		streamTaskName = strings.TrimSpace(streamEntry.TaskName)
		streamTaskSlug = strings.TrimSpace(streamEntry.TaskSlug)
	}
	if entryAgentType == "" {
		var resolvedID string
		resolvedID, _, entryAgentType = m.resolveAgentIdentity(entryAgentID, r.AgentName)
		if entryAgentID == "" {
			entryAgentID = resolvedID
		}
	}
	entry := &chat.ChatEntry{
		ID:            uuid.New().String(),
		Timestamp:     time.Now(),
		CorrelationID: r.CorrelationID,
		Source:        source,
		AgentType:     entryAgentType,
		AgentID:       entryAgentID,
		TaskID:        streamTaskID,
		TaskName:      streamTaskName,
		TaskSlug:      streamTaskSlug,
		Content:       content,
		Height:        -1,
	}
	m.chat.FinishThinking(entry)
	return nil
}

func (m *AppModel) publishResponseActivity(
	r msg.GuideResponseMsg,
	source chat.ChatSource,
	content string,
	streamEntry *activeStreamEntry,
) {
	if m.deps.ActivityPub == nil {
		return
	}
	streamAgentID := ""
	streamRuntimeAgentID := ""
	streamAgentType := ""
	streamAgentName := ""
	streamPipelineID := ""
	streamTaskID := ""
	streamTaskName := ""
	streamTaskSlug := ""
	if streamEntry != nil {
		streamAgentID = streamEntry.AgentID
		streamRuntimeAgentID = streamEntry.RuntimeAgentID
		streamAgentType = streamEntry.AgentType
		streamAgentName = streamEntry.AgentName
		streamPipelineID = streamEntry.PipelineID
		streamTaskID = streamEntry.TaskID
		streamTaskName = streamEntry.TaskName
		streamTaskSlug = streamEntry.TaskSlug
	}
	streamPipelineID = logicalStreamPipelineID(streamPipelineID, streamTaskID)
	agentID := streamPanelAgentID(firstNonEmpty(r.AgentID, streamAgentID), streamAgentType, streamPipelineID)
	agentName := firstNonEmpty(streamAgentName, r.AgentName)
	panelAgentType := ""
	if m.agentPanel != nil {
		panelAgentType = m.agentPanel.AgentTypeOf(agentID)
	}
	agentType := firstNonEmpty(streamAgentType, panelAgentType)
	if agentID == "" || agentID == guideAgentID {
		agentID, agentName, agentType = m.resolveAgentIdentity(r.AgentID, r.AgentName)
	}
	outcome := events.OutcomeSuccess
	eventType := events.EventTypeLLMResponse
	if source == chat.SourceError {
		outcome = events.OutcomeFailure
		eventType = events.EventTypeAgentError
	}
	data := map[string]any{
		"agent_type": agentType,
		"agent_name": firstNonEmpty(agentName, agentID),
	}
	if streamEntry != nil {
		if pipelineID := strings.TrimSpace(streamPipelineID); pipelineID != "" {
			data["pipeline_id"] = pipelineID
		}
		if taskID := strings.TrimSpace(streamTaskID); taskID != "" {
			data["task_id"] = taskID
		}
		if taskName := strings.TrimSpace(streamTaskName); taskName != "" {
			data["task_name"] = taskName
		}
		if taskSlug := strings.TrimSpace(streamTaskSlug); taskSlug != "" {
			data["task_slug"] = taskSlug
		}
		if runtimeID := strings.TrimSpace(firstNonEmpty(streamRuntimeAgentID, r.AgentID)); runtimeID != "" && runtimeID != agentID {
			data["runtime_agent_id"] = runtimeID
		}
	}
	m.deps.ActivityPub.PublishActivity(&events.ActivityEvent{
		ID:        uuid.New().String(),
		EventType: eventType,
		Timestamp: time.Now(),
		AgentID:   agentID,
		Content:   summarizeActivityContent(content),
		Outcome:   outcome,
		Data:      data,
	})
}

// resolveAgentIdentity resolves the canonical agent ID, display name, and
// agent type from a response message. The type is resolved from the agent
// panel's state (populated by prior activity events from the agent itself)
// rather than parsed from the ID string — agent IDs are opaque UUIDs.
func (m *AppModel) resolveAgentIdentity(agentID, agentName string) (string, string, string) {
	id := strings.TrimSpace(agentID)
	name := strings.TrimSpace(agentName)
	if id == "" {
		id = strings.ToLower(name)
	}
	if id == "" {
		id = "agent"
	}
	if name == "" {
		name = id
	}
	if strings.EqualFold(id, guideAgentID) {
		return guideAgentID, guideAgentName, guideAgentType
	}
	agentType := m.agentPanel.AgentTypeOf(id)
	if agentType == "" {
		agentType = strings.ToLower(name)
	}
	return id, name, agentType
}

func summarizeActivityContent(content string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return "Response generated"
	}
	const maxActivityContentRunes = 160
	runes := []rune(trimmed)
	if len(runes) <= maxActivityContentRunes {
		return trimmed
	}
	return string(runes[:maxActivityContentRunes]) + "..."
}

// handleConflictPreview processes a dry-run merge result. If conflicts are
// found, shows a list modal; otherwise proceeds immediately.
func (m *AppModel) handleConflictPreview(typed msg.ConflictPreviewMsg) tea.Cmd {
	if typed.Result == nil || typed.Result.Clean {
		// No conflicts — proceed directly.
		if m.pendingSeqOp != nil {
			op := m.pendingSeqOp
			m.pendingSeqOp = nil
			return m.executeSequencerOp(op)
		}
		return nil
	}

	conflicts := typed.Result.Conflicts
	items := make([]modal.ListModalItem, len(conflicts))
	for i, c := range conflicts {
		items[i] = modal.ListModalItem{
			Label: c.Path,
			Color: m.config.Theme().Palette.Error,
		}
	}
	footer := fmt.Sprintf("%d file(s) will conflict", len(conflicts))
	lm := modal.NewListModal("Conflicts Detected ("+typed.Op+")", items, footer,
		[]string{"Continue", "Cancel"}, m.config.Theme())
	m.modalOverlay.Push(lm)
	if m.pendingSeqOp != nil {
		m.pendingSeqOp.phase = 2
	}
	return nil
}

// handleIntegrationDetected processes cherry-pick duplicate detection results.
// Shows a list modal when already-integrated commits are found.
func (m *AppModel) handleIntegrationDetected(typed msg.IntegrationDetectedMsg) tea.Cmd {
	// Count integrated commits.
	var integrated int
	for _, r := range typed.Results {
		if r.Integrated {
			integrated++
		}
	}

	if integrated == 0 {
		// No duplicates — proceed to conflict preview.
		if m.pendingSeqOp != nil {
			return m.conflictPreviewCherryPickCmd(m.pendingSeqOp.hashes, nil)
		}
		return nil
	}

	items := make([]modal.ListModalItem, 0, len(typed.Results))
	for _, r := range typed.Results {
		badge := ""
		var color lipgloss.Color
		if r.Integrated {
			badge = "INTEGRATED"
			color = m.config.Theme().Palette.Teal
		}
		items = append(items, modal.ListModalItem{
			Label:  r.CommitHash[:min(len(r.CommitHash), 8)],
			Detail: r.Subject,
			Badge:  badge,
			Color:  color,
		})
	}
	footer := fmt.Sprintf("%d of %d commit(s) already integrated", integrated, len(typed.Results))
	lm := modal.NewListModal("Cherry-Pick Integration Check", items, footer,
		[]string{"Apply All", "Cancel"}, m.config.Theme())
	m.modalOverlay.Push(lm)
	if m.pendingSeqOp != nil {
		m.pendingSeqOp.phase = 1
	}
	return nil
}

// handleAbortPreserved handles the result of a pre-abort preservation.
func (m *AppModel) handleAbortPreserved(typed msg.AbortPreservedMsg) tea.Cmd {
	if typed.Err != nil {
		m.statusBar.SetFlash("Abort preservation failed: " + typed.Err.Error())
	} else if typed.Preservation != nil {
		parts := []string{"State preserved"}
		if typed.Preservation.BackupBranch != "" {
			parts = append(parts, "ref: "+typed.Preservation.BackupBranch)
		}
		if len(typed.Preservation.StashedPaths) > 0 {
			parts = append(parts, fmt.Sprintf("%d file(s) stashed", len(typed.Preservation.StashedPaths)))
		}
		m.statusBar.SetFlash(strings.Join(parts, " — "))
	}

	// Now proceed with the actual abort.
	bus := m.gitBus
	return func() tea.Msg {
		if err := bus.SequencerAbort(); err != nil {
			return sequencerAbortFailedMsg{reason: err.Error()}
		}
		return sequencerAbortedMsg{}
	}
}

// handleBranchStashAvailable offers to restore a branch stash after checkout.
func (m *AppModel) handleBranchStashAvailable(typed msg.BranchStashAvailableMsg) tea.Cmd {
	m.statusBar.SetFlash(
		fmt.Sprintf("Branch stash available (%d files) — restoring", typed.Meta.FileCount))
	bus := m.gitBus
	branch := typed.Meta.BranchName
	return func() tea.Msg {
		err := bus.UnstashForBranch(branch)
		return msg.BranchStashRestoredMsg{Err: err}
	}
}

func (m *AppModel) handleModalClosed(result any) tea.Cmd {
	if !m.modalOverlay.Active() {
		m.overlay = overlayNone
	}

	lr, ok := result.(modal.ListModalResult)
	if !ok {
		return nil
	}

	// Route pre-commit pipeline results.
	if m.pendingCommitPhase != 0 {
		return m.routePreCommitModal(lr)
	}

	// Route sequencer operation modals (integration detection, conflict preview).
	if m.pendingSeqOp != nil {
		return m.routeSequencerModal(lr)
	}

	// Route syntax validation modal.
	if m.pendingSyntaxValidation {
		m.pendingSyntaxValidation = false
		if lr.Action == 0 {
			// "Continue Anyway" — re-emit with Proceed=true.
			return func() tea.Msg {
				return conflictview.SyntaxValidationResultMsg{Proceed: true}
			}
		}
		return nil // Cancel — do nothing.
	}

	return nil
}

// routePreCommitModal handles large-file / secret modal confirmations.
func (m *AppModel) routePreCommitModal(lr modal.ListModalResult) tea.Cmd {
	phase := m.pendingCommitPhase
	paths := m.pendingCommitPaths
	message := m.pendingCommitMessage
	m.pendingCommitPaths = nil
	m.pendingCommitMessage = ""
	m.pendingCommitPhase = 0

	if lr.Action != 0 {
		m.statusBar.SetFlash("Commit cancelled")
		return nil
	}

	bus := m.gitBus
	switch phase {
	case 1:
		// Large-file modal confirmed — proceed to secret scan.
		return func() tea.Msg {
			secrets, err := bus.ScanStagedSecrets(paths)
			if err == nil && len(secrets) > 0 {
				return msg.SecretsDetectedMsg{Findings: secrets, Paths: paths, Message: message}
			}
			return msg.PreCommitCleanMsg{Paths: paths, Message: message}
		}
	case 2:
		// Secrets modal confirmed — proceed to commit.
		return func() tea.Msg {
			if err := bus.CommitFiles(paths, message); err != nil {
				return commitFailedMsg{reason: err.Error()}
			}
			return commitSucceededMsg{message: message}
		}
	}
	return nil
}

// routeSequencerModal handles integration detection and conflict preview
// modal confirmations for pending cherry-pick/rebase/merge operations.
func (m *AppModel) routeSequencerModal(lr modal.ListModalResult) tea.Cmd {
	op := m.pendingSeqOp
	m.pendingSeqOp = nil

	if lr.Action != 0 {
		// Last action = cancel.
		if m.commitTree != nil {
			m.commitTree.ClearLoadingMessage()
		}
		m.statusBar.SetFlash(op.op + " cancelled")
		return nil
	}

	return m.executeSequencerOp(op)
}

// executeSequencerOp dispatches the stored sequencer operation.
func (m *AppModel) executeSequencerOp(op *pendingSequencerOp) tea.Cmd {
	bus := m.gitBus
	switch op.op {
	case "cherry-pick":
		return func() tea.Msg {
			status, err := bus.CherryPickSequence(op.hashes, op.target)
			if err != nil {
				return sequencerFailedMsg{reason: err.Error()}
			}
			return sequencerResultMsg{status: status}
		}
	case "rebase":
		return func() tea.Msg {
			status, err := bus.RebaseInteractive(op.target, op.sourceBranch, op.plan)
			if err != nil {
				return sequencerFailedMsg{reason: err.Error()}
			}
			return sequencerResultMsg{status: status}
		}
	case "merge":
		return m.executeMergeBranch(op.delete)
	}
	return nil
}
