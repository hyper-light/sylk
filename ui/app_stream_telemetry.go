package ui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	coreerrors "github.com/adalundhe/sylk/core/errors"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/ui/agentidentity"
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
		if start.BranchRef != nil {
			return m.propagate(start)
		}
		return nil
	}
	if entry := m.streamEntryForCorrelation(start.CorrelationID); entry != nil {
		start.AgentID = effectiveStreamUIAgentID(entry, start.AgentID, start.RuntimeAgentID, start.AgentType, start.PipelineID, start.TaskID)
		start.RuntimeAgentID = firstNonEmpty(start.RuntimeAgentID, entry.RuntimeAgentID, start.AgentID)
	} else {
		start.AgentID = effectiveStreamUIAgentID(nil, start.AgentID, start.RuntimeAgentID, start.AgentType, start.PipelineID, start.TaskID)
	}
	m.clearExplicitTopLevelTransferState(correlationID, start.ParentCorrelationID, start.BranchRef, start.TopLevelTransfer)
	start.BranchRef = m.resolveIncomingStreamBranchRef(correlationID, start.ParentCorrelationID, start.BranchRef, start.TopLevelTransfer)
	startAttrs := []any{
		"correlation_id", correlationID,
		"agent_id", strings.TrimSpace(start.AgentID),
		"runtime_agent_id", strings.TrimSpace(start.RuntimeAgentID),
		"agent_type", strings.TrimSpace(start.AgentType),
		"parent_correlation_id", strings.TrimSpace(start.ParentCorrelationID),
		"task_id", strings.TrimSpace(start.TaskID),
		"task_name", strings.TrimSpace(start.TaskName),
		"task_slug", strings.TrimSpace(start.TaskSlug),
		"active_streams", len(m.activeStreams),
		"deferred_streams", len(m.deferredStreams),
		"nested_streams", len(m.nestedStreams),
	}
	startAttrs = appendBranchRefLogAttrs(startAttrs, "resolved_branch_ref", start.BranchRef)
	if entry := m.streamEntryForCorrelation(correlationID); entry != nil {
		startAttrs = append(startAttrs,
			"existing_stream_entry", true,
			"existing_stream_agent_id", entry.AgentID,
			"existing_stream_runtime_agent_id", entry.RuntimeAgentID,
			"existing_stream_agent_type", entry.AgentType,
		)
		startAttrs = appendBranchRefLogAttrs(startAttrs, "existing_stream_branch_ref", entry.BranchRef)
	} else {
		startAttrs = append(startAttrs, "existing_stream_entry", false)
	}
	uiDebugFileLog().Info("AppModel: STREAM_START_TELEMETRY", startAttrs...)
	transferCmd := m.observeTopLevelStreamTransfer(
		start.SessionID,
		start.ParentCorrelationID,
		start.CorrelationID,
		firstNonEmpty(strings.TrimSpace(start.AgentType), strings.TrimSpace(start.AgentID)),
		start.BranchRef,
		start.TopLevelTransfer,
	)
	start, _ = m.prepareStreamStart(start)
	startCmd := m.propagate(start)
	flushCmd := m.flushDelayedPrimaryBootstrap(correlationID)
	if transferCmd != nil {
		return tea.Batch(transferCmd, startCmd, flushCmd)
	}
	return tea.Batch(startCmd, flushCmd)
}

func (m *AppModel) prepareStreamStart(start msg.StreamStartMsg) (msg.StreamStartMsg, bool) {
	canonicalAgentID := canonicalStreamAgentID(start.AgentID, start.AgentType, start.PipelineID, start.TaskID)
	start.RuntimeAgentID = normalizeRuntimeAgentID(canonicalAgentID, firstNonEmpty(start.RuntimeAgentID, start.AgentID))
	resetRecordedState := m.shouldResetRecordedStreamStart(start)
	if resetRecordedState {
		m.recordStreamStart(start.CorrelationID)
	} else {
		m.touchRecordedStream(start.CorrelationID)
	}
	m.recordStreamBranchRef(start.CorrelationID, start.BranchRef)
	m.trackStreamStart(start)
	created := m.registerStream(start)
	attrs := []any{
		"correlation_id", strings.TrimSpace(start.CorrelationID),
		"agent_id", strings.TrimSpace(start.AgentID),
		"runtime_agent_id", strings.TrimSpace(start.RuntimeAgentID),
		"canonical_agent_id", canonicalAgentID,
		"agent_type", strings.TrimSpace(start.AgentType),
		"created", created,
		"is_nested", start.BranchRef != nil,
		"active_streams", len(m.activeStreams),
		"deferred_streams", len(m.deferredStreams),
		"nested_streams", len(m.nestedStreams),
	}
	attrs = appendBranchRefLogAttrs(attrs, "branch_ref", start.BranchRef)
	uiDebugFileLog().Info("AppModel: PREPARE_STREAM_START", attrs...)
	if start.BranchRef == nil {
		newAgent := normalizeAgentID(firstNonEmpty(canonicalAgentID, start.AgentType, start.AgentID))
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
	if entry := m.streamEntryForCorrelation(progress.CorrelationID); entry != nil {
		progress.AgentID = effectiveStreamUIAgentID(entry, progress.AgentID, progress.RuntimeAgentID, progress.AgentType, progress.PipelineID, progress.TaskID)
		progress.RuntimeAgentID = firstNonEmpty(progress.RuntimeAgentID, entry.RuntimeAgentID, progress.AgentID)
	} else {
		progress.AgentID = effectiveStreamUIAgentID(nil, progress.AgentID, progress.RuntimeAgentID, progress.AgentType, progress.PipelineID, progress.TaskID)
	}
	m.clearExplicitTopLevelTransferState(progress.CorrelationID, progress.ParentCorrelationID, progress.BranchRef, progress.TopLevelTransfer)
	progress.BranchRef = m.resolveIncomingStreamBranchRef(progress.CorrelationID, progress.ParentCorrelationID, progress.BranchRef, progress.TopLevelTransfer)
	transferCmd := m.observeTopLevelStreamTransfer(
		progress.SessionID,
		progress.ParentCorrelationID,
		progress.CorrelationID,
		firstNonEmpty(strings.TrimSpace(progress.AgentType), strings.TrimSpace(progress.AgentID)),
		progress.BranchRef,
		progress.TopLevelTransfer,
	)
	if m.shouldIgnoreLateStreamBootstrap(progress.CorrelationID) {
		if progress.BranchRef != nil {
			progress.Message = redactSecrets(progress.Message)
			if transferCmd != nil {
				return tea.Batch(transferCmd, m.propagate(progress))
			}
			return m.propagate(progress)
		}
		return nil
	}
	start := msg.StreamStartMsg{
		SessionID:           progress.SessionID,
		CorrelationID:       progress.CorrelationID,
		ParentCorrelationID: progress.ParentCorrelationID,
		TopLevelTransfer:    progress.TopLevelTransfer,
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
	progress.Message = redactSecrets(progress.Message)
	if m.shouldBootstrapStreamFromTelemetry(progress.CorrelationID, isExplicitTopLevelTransfer(progress.ParentCorrelationID, progress.BranchRef, progress.TopLevelTransfer)) {
		if m.shouldDelayAmbiguousPrimaryBootstrap(start) {
			m.enqueueDelayedPrimaryBootstrap(progress.CorrelationID, progress)
			return transferCmd
		}
		start, created := m.prepareStreamStart(start)
		startCmd := m.propagate(start)
		flushCmd := m.flushDelayedPrimaryBootstrap(progress.CorrelationID)
		if !m.shouldRenderStreamEvent(progress.CorrelationID) {
			return tea.Batch(transferCmd, flushCmd)
		}
		if created {
			if transferCmd != nil {
				return tea.Batch(transferCmd, startCmd, flushCmd, m.propagate(progress))
			}
			return tea.Batch(startCmd, flushCmd, m.propagate(progress))
		}
		if transferCmd != nil {
			return tea.Batch(transferCmd, flushCmd, m.propagate(progress))
		}
		return tea.Batch(flushCmd, m.propagate(progress))
	}
	if !m.shouldRenderStreamEvent(progress.CorrelationID) {
		return transferCmd
	}
	if transferCmd != nil {
		return tea.Batch(transferCmd, m.propagate(progress))
	}
	return m.propagate(progress)
}

func (m *AppModel) handleToolCallTelemetry(ev msg.ToolCallEventMsg) tea.Cmd {
	ev.AgentID = effectiveStreamUIAgentID(
		m.streamEntryForCorrelation(ev.CorrelationID),
		ev.AgentID,
		"",
		ev.AgentType,
		ev.PipelineID,
		ev.TaskID,
	)
	m.clearExplicitTopLevelTransferState(ev.CorrelationID, ev.ParentCorrelationID, ev.BranchRef, ev.TopLevelTransfer)
	ev.BranchRef = m.resolveIncomingStreamBranchRef(ev.CorrelationID, ev.ParentCorrelationID, ev.BranchRef, ev.TopLevelTransfer)
	explicitTopLevelTransfer := isExplicitTopLevelTransfer(ev.ParentCorrelationID, ev.BranchRef, ev.TopLevelTransfer)
	transferCmd := m.observeTopLevelStreamTransfer(
		ev.SessionID,
		ev.ParentCorrelationID,
		ev.CorrelationID,
		firstNonEmpty(strings.TrimSpace(ev.AgentType), strings.TrimSpace(ev.AgentID)),
		ev.BranchRef,
		ev.TopLevelTransfer,
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
	if m.streamEntryForCorrelation(correlationID) != nil {
		if !m.shouldRenderTerminalStreamEvent(correlationID) && ev.Phase == 1 {
			return transferCmd
		}
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
		if ev.BranchRef != nil {
			if transferCmd != nil {
				return tea.Batch(transferCmd, m.propagate(ev))
			}
			return m.propagate(ev)
		}
		return nil
	}
	if correlationID != "" {
		if _, rerouted := m.reroutedStreamCIDs[correlationID]; !rerouted && m.shouldBootstrapStreamFromTelemetry(correlationID, explicitTopLevelTransfer) {
			start := msg.StreamStartMsg{
				SessionID:           ev.SessionID,
				CorrelationID:       ev.CorrelationID,
				ParentCorrelationID: ev.ParentCorrelationID,
				TopLevelTransfer:    ev.TopLevelTransfer,
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
			if m.shouldDelayAmbiguousPrimaryBootstrap(start) {
				m.enqueueDelayedPrimaryBootstrap(ev.CorrelationID, ev)
				return transferCmd
			}
			start, created := m.prepareStreamStart(start)
			startCmd := m.propagate(start)
			flushCmd := m.flushDelayedPrimaryBootstrap(ev.CorrelationID)
			if !m.shouldRenderStreamEvent(ev.CorrelationID) {
				return tea.Batch(transferCmd, flushCmd)
			}
			if created {
				if transferCmd != nil {
					return tea.Batch(transferCmd, startCmd, flushCmd, m.propagate(ev))
				}
				return tea.Batch(startCmd, flushCmd, m.propagate(ev))
			}
			if transferCmd != nil {
				return tea.Batch(transferCmd, flushCmd, m.propagate(ev))
			}
			return tea.Batch(flushCmd, m.propagate(ev))
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
	if entry := m.streamEntryForCorrelation(done.CorrelationID); entry != nil {
		done.AgentID = effectiveStreamUIAgentID(entry, done.AgentID, done.RuntimeAgentID, done.AgentType, done.PipelineID, done.TaskID)
		done.RuntimeAgentID = firstNonEmpty(done.RuntimeAgentID, entry.RuntimeAgentID, done.AgentID)
	} else {
		done.AgentID = effectiveStreamUIAgentID(nil, done.AgentID, done.RuntimeAgentID, done.AgentType, done.PipelineID, done.TaskID)
	}
	m.clearExplicitTopLevelTransferState(done.CorrelationID, done.ParentCorrelationID, done.BranchRef, done.TopLevelTransfer)
	done.BranchRef = m.resolveIncomingStreamBranchRef(done.CorrelationID, done.ParentCorrelationID, done.BranchRef, done.TopLevelTransfer)
	uiDebugFileLog().Info("AppModel: STREAM_COMPLETE_RECEIVED",
		"correlation_id", done.CorrelationID,
		"agent_id", done.AgentID,
		"active_streams", len(m.activeStreams),
		"authoritative_text_len", len(done.AuthoritativeText))
	m.recordStreamComplete(done.CorrelationID)
	m.recordStreamBranchRef(done.CorrelationID, done.BranchRef)
	shouldRender := m.shouldRenderTerminalStreamEvent(done.CorrelationID)
	shouldPropagate := shouldRender || done.BranchRef != nil
	uiDebugFileLog().Info("AppModel: STREAM_COMPLETE_SHOULD_RENDER",
		"correlation_id", done.CorrelationID,
		"should_render", shouldRender,
		"should_propagate", shouldPropagate)
	m.applyRealStreamUsage(done)
	m.finalizeStreamUsage(done.CorrelationID, true, "")
	m.markQueueEntryByCorrelation(done.CorrelationID, true)
	if !shouldPropagate {
		m.unregisterStream(done.CorrelationID)
		m.clearReroutedStreamCID(done.CorrelationID)
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
	var cmds []tea.Cmd
	chatComp, chatCmd := m.chat.Update(done)
	m.chat = chatComp.(*chat.Model)
	cmds = appendCmd(cmds, chatCmd)
	if done.BranchRef == nil && m.chat.HasPendingCorrelation(done.CorrelationID) {
		m.deferPrimaryStream(done.CorrelationID)
	} else {
		m.unregisterStream(done.CorrelationID)
	}
	m.clearReroutedStreamCID(done.CorrelationID)
	cmds = appendCmd(cmds, m.propagateWithoutChat(done))
	if advCmd := m.tryAdvanceQueue(); advCmd != nil {
		cmds = appendCmd(cmds, advCmd)
	}
	return tea.Batch(cmds...)
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

func (m *AppModel) touchRecordedStream(correlationID string) {
	if correlationID == "" || m.streamedResponses == nil {
		return
	}
	state, ok := m.streamedResponses[correlationID]
	if !ok {
		return
	}
	state.SeenAt = time.Now()
	m.streamedResponses[correlationID] = state
}

func (m *AppModel) shouldResetRecordedStreamStart(start msg.StreamStartMsg) bool {
	correlationID := strings.TrimSpace(start.CorrelationID)
	if correlationID == "" {
		return false
	}
	if !m.hasRecordedStreamCorrelation(correlationID) {
		return true
	}
	incomingIdentity := normalizeAgentID(firstNonEmpty(
		canonicalStreamAgentID(start.AgentID, start.AgentType, start.PipelineID, start.TaskID),
		start.AgentType,
		start.AgentID,
	))
	if incomingIdentity == "" {
		return false
	}
	if usage, ok := m.streamUsage[correlationID]; ok {
		existingIdentity := normalizeAgentID(firstNonEmpty(usage.AgentID, usage.AgentType))
		if existingIdentity != "" {
			return existingIdentity != incomingIdentity
		}
	}
	if entry := m.streamEntryForCorrelation(correlationID); entry != nil {
		existingIdentity := normalizeAgentID(firstNonEmpty(entry.AgentID, entry.AgentType))
		if existingIdentity != "" {
			return existingIdentity != incomingIdentity
		}
	}
	return false
}

func (m *AppModel) recordStreamBranchRef(correlationID string, ref *msg.InterAgentBranchRefMsg) {
	if correlationID == "" || ref == nil {
		return
	}
	m.ensureStreamedResponseState()
	state := m.streamedResponses[correlationID]
	cloned := *ref
	state.BranchRef = &cloned
	state.SeenAt = time.Now()
	m.streamedResponses[correlationID] = state
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

func (m *AppModel) recordedStreamBranchRef(correlationID string) *msg.InterAgentBranchRefMsg {
	if correlationID == "" || m.streamedResponses == nil {
		return nil
	}
	state, ok := m.streamedResponses[correlationID]
	if !ok || state.BranchRef == nil {
		return nil
	}
	cloned := *state.BranchRef
	return &cloned
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
//
// The accountant (core/llm/accounting) is the canonical session-wide source:
// it aggregates every provider call across every agent, every replica, every
// model generation, and every task under a typed AccountingKey. When the
// AccountantBridge has delivered at least one snapshot (any non-zero field),
// the status bar reads those totals directly — they are correct across the
// full session regardless of which agent or provider generated them.
//
// The stream-derived `totalPrompt*` / `background*` counters are retained for
// two reasons: (1) they drive the per-stream *phase* animation (the spinner
// needs to know which direction is active mid-stream, which the accountant's
// periodic snapshot does not capture in real time); (2) they serve as a cold-
// start fallback for the first few hundred milliseconds before the
// accountant snapshot arrives, or when the accountant is disabled (tests).
func (m *AppModel) updateTokenDisplay() {
	if m.statusBar == nil {
		return
	}
	prompt := m.totalPromptTokens + m.backgroundPromptTokens
	completion := m.totalCompletionTokens + m.backgroundCompletionTokens
	cacheRead := m.totalCacheReadTokens + m.backgroundCacheReadTokens
	reasoning := m.totalReasoningTokens + m.backgroundReasoningTokens

	// Accountant-aggregated totals (canonical). Preferred whenever the
	// bridge has delivered a snapshot — any non-zero field confirms the
	// accountant is live. The accountant's Input/Output/Reasoning figures
	// already fold in cache-read separately (see AggregatedUsage.Add), so
	// we pass CacheRead through from the legacy counters; the net-input
	// rendering in TokenDisplay.View subtracts it from Input at display
	// time.
	if m.accountantTotalInput != 0 || m.accountantTotalOutput != 0 || m.accountantTotalReasoning != 0 {
		prompt = int(m.accountantTotalInput)
		completion = int(m.accountantTotalOutput)
		reasoning = int(m.accountantTotalReasoning)
	}

	m.statusBar.SetTokens(prompt, completion, cacheRead, reasoning)
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

func effectiveStreamUIAgentID(entry *activeStreamEntry, agentID, runtimeAgentID, agentType, pipelineID, taskID string) string {
	if canonicalID := agentidentity.VisibleAgentID(
		agentID,
		"",
		agentType,
		pipelineID,
		taskID,
	); canonicalID != "" {
		return canonicalID
	}
	if entry != nil {
		if canonicalID := agentidentity.VisibleAgentID(
			entry.AgentID,
			"",
			entryAgentType(entry),
			entryPipelineID(entry),
			entryTaskID(entry),
		); canonicalID != "" {
			return canonicalID
		}
	}
	if resolved := strings.TrimSpace(runtimeAgentID); resolved != "" {
		return resolved
	}
	if entry != nil {
		if resolved := strings.TrimSpace(entry.RuntimeAgentID); resolved != "" {
			return resolved
		}
	}
	return ""
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
	if effectiveBranchRef == nil {
		effectiveBranchRef = m.recordedStreamBranchRef(r.CorrelationID)
	}
	hasPendingChatCorrelation := m.chat != nil && m.chat.HasPendingCorrelation(r.CorrelationID)
	streamEntry := cloneActiveStreamEntry(m.streamEntryForCorrelation(r.CorrelationID))
	responseAttrs := []any{
		"correlation_id", strings.TrimSpace(r.CorrelationID),
		"agent_id", strings.TrimSpace(r.AgentID),
		"agent_type", strings.TrimSpace(r.AgentType),
		"has_error", r.Err != nil,
		"has_pending_chat_correlation", hasPendingChatCorrelation,
		"content_len", len(content),
		"active_streams", len(m.activeStreams),
		"deferred_streams", len(m.deferredStreams),
		"nested_streams", len(m.nestedStreams),
	}
	responseAttrs = appendBranchRefLogAttrs(responseAttrs, "effective_branch_ref", effectiveBranchRef)
	if streamEntry != nil {
		responseAttrs = append(responseAttrs,
			"stream_entry_found", true,
			"stream_entry_agent_id", streamEntry.AgentID,
			"stream_entry_runtime_agent_id", streamEntry.RuntimeAgentID,
			"stream_entry_agent_type", streamEntry.AgentType,
			"stream_entry_task_id", streamEntry.TaskID,
		)
		responseAttrs = appendBranchRefLogAttrs(responseAttrs, "stream_entry_branch_ref", streamEntry.BranchRef)
	} else {
		responseAttrs = append(responseAttrs, "stream_entry_found", false)
	}
	uiDebugFileLog().Info("AppModel: GUIDE_RESPONSE_CONTINUITY", responseAttrs...)
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
	// UI-05 dispatch stitch: route librarian/academic responses into
	// the knowledge panel. No-op for non-knowledge agents, errored
	// responses, or responses whose content cannot be adapted into
	// ResultEntry rows.
	m.pushKnowledgeResponseToPanel(r)
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
		if r.Err == nil && effectiveBranchRef == nil && m.chat.HasPendingCorrelation(r.CorrelationID) {
			m.deferPrimaryStream(r.CorrelationID)
			uiDebugFileLog().Info("AppModel: GUIDE_RESPONSE_CONTINUITY_DECISION",
				"correlation_id", strings.TrimSpace(r.CorrelationID),
				"decision", "synthetic_complete_then_defer_primary",
				"has_pending_chat_correlation", true)
		} else {
			m.unregisterStream(r.CorrelationID)
			uiDebugFileLog().Info("AppModel: GUIDE_RESPONSE_CONTINUITY_DECISION",
				"correlation_id", strings.TrimSpace(r.CorrelationID),
				"decision", "synthetic_terminal_then_unregister",
				"has_pending_chat_correlation", hasPendingChatCorrelation,
				"has_effective_branch_ref", effectiveBranchRef != nil,
				"has_error", r.Err != nil)
		}
		return cmd
	}
	m.unregisterStream(r.CorrelationID)
	if source != chat.SourceError && strings.TrimSpace(content) == "" {
		uiDebugFileLog().Info("AppModel: GUIDE_RESPONSE_CONTINUITY_DECISION",
			"correlation_id", strings.TrimSpace(r.CorrelationID),
			"decision", "skip_empty_success_response",
			"has_pending_chat_correlation", false,
			"has_effective_branch_ref", effectiveBranchRef != nil,
			"has_error", false)
		return nil
	}
	uiDebugFileLog().Info("AppModel: GUIDE_RESPONSE_CONTINUITY_DECISION",
		"correlation_id", strings.TrimSpace(r.CorrelationID),
		"decision", "create_top_level_chat_entry",
		"has_pending_chat_correlation", false,
		"has_effective_branch_ref", effectiveBranchRef != nil,
		"has_error", r.Err != nil)
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
	if source != chat.SourceError && strings.TrimSpace(content) == "" {
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
