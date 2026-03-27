package academic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/steering"
	"github.com/adalundhe/sylk/core/toolruntime"
)

var (
	errAcademicSearchGroundingRequested     = errors.New("academic discovery phase reached grounding threshold")
	errAcademicSearchCorroborationRequested = errors.New("academic grounded evidence now needs corroboration")
	errAcademicSearchSynthesisRequested     = errors.New("academic search plateau reached; synthesize from gathered evidence")
)

// executeToolLoop runs the LLM tool-call loop: Complete → check ToolCalls →
// execute → append results → repeat, bounded by config.MaxToolRuns.
// Follows the engineer tool loop pattern exactly.
func (a *Academic) executeToolLoop(
	ctx context.Context,
	req *providers.Request,
	ledger *steering.SteeringLedger,
	surface toolruntime.Surface,
) (string, error) {
	return a.executeToolLoopWithDiscipline(ctx, req, ledger, surface, searchDiscipline{})
}

func (a *Academic) executeToolLoopWithDiscipline(
	ctx context.Context,
	req *providers.Request,
	ledger *steering.SteeringLedger,
	surface toolruntime.Surface,
	_ searchDiscipline,
) (string, error) {
	maxRuns := a.config.MaxToolRuns
	consecutiveErrors := 0
	seen := make(map[shared.ToolCallSignature]int, maxRuns)
	baseTools := append([]providers.Tool(nil), req.Tools...)
	if surface == nil {
		surface = a.toolRuntime()
	}

	p := a.getProvider()
	if p == nil {
		if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
				lm.AgentID, lm.SessionID, lm.CorrID, "error",
				&agentlog.ErrorPayload{Error: "no LLM provider configured"})
		}
		return "", fmt.Errorf("academic: no LLM provider configured")
	}
	if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
		shared.LogAgentEvent(lm.EventLogger, agentlog.EventGenerationStarted,
			lm.AgentID, lm.SessionID, lm.CorrID, "info",
			&agentlog.GenerationPayload{Phase: "started"})
	}

	for turn := 0; turn <= maxRuns; turn++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		// ── STEERING CHECKPOINT ──
		sc := shared.DrainAndCheckpoint(ledger, req, turn, "researching", nil)
		if sc.Rollback != nil || sc.EditReplay != nil {
			cp := sc.Rollback
			if cp == nil {
				cp = sc.EditReplay
			}
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				action := "rollback"
				if sc.EditReplay != nil {
					action = "edit_replay"
				}
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventSteeringCheckpoint,
					lm.AgentID, lm.SessionID, lm.CorrID, "info",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("%s at turn %d", action, cp.Turn)})
			}
			req.Messages = req.Messages[:cp.MessageCount]
			if sc.EditReplay != nil {
				req.Messages = append(req.Messages, providers.Message{Role: providers.RoleUser, Content: sc.EditText})
			}
			turn = cp.Turn
			consecutiveErrors = 0
			continue
		}
		if sc.ShouldPause {
			if err := ledger.WaitForResume(ctx); err != nil {
				return "", err
			}
			continue
		}
		// ── END STEERING ──

		if a.toolDefsDirty {
			baseTools = a.buildToolDefinitionsWithSurface(surface)
			a.toolDefsDirty = false
		}
		req.Tools = append([]providers.Tool(nil), baseTools...)

		// ── CONTEXT BUDGET ──
		if err := shared.ApplyContextBudget(ctx, turn, maxRuns, req); err != nil {
			return "", err
		}

		turnStart := time.Now()
		resp, streamed, err := a.completeLLMTurn(ctx, p, req)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "error",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("llm: %v", err)})
			}
			return "", fmt.Errorf("academic llm: %w", err)
		}

		if gov := shared.ContextGovernorFromContext(ctx); gov != nil {
			gov.Calibrate(ctx, resp, req.Messages)
		}
		shared.AccumulateUsage(ctx, &resp.Usage)
		if !streamed {
			shared.PublishIntermediateToolTurn(a.bus, a.channels, ctx, a.id, resp)
		}
		if execState := academicResearchExecutionStateFromContext(ctx); execState != nil {
			execState.observeProviderResponse(ctx, resp)
		}

		if len(resp.ToolCalls) == 0 {
			if execState := academicResearchExecutionStateFromContext(ctx); execState != nil {
				if reminder, fields := execState.finalizationBlock(); reminder != "" {
					fields["content_preview"] = truncateStr(strings.TrimSpace(resp.Content), 200)
					academicLogResearchStateEvent(ctx, "finalization_blocked", fields)
					req.Messages = append(req.Messages, providers.Message{
						Role:    providers.RoleUser,
						Content: reminder,
					})
					a.recordTurn(ctx, req, resp, turn, 0, 0, turnStart)
					continue
				}
			}
			a.recordTurn(ctx, req, resp, turn, 0, 0, turnStart)
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventGenerationCompleted,
					lm.AgentID, lm.SessionID, lm.CorrID, "info",
					&agentlog.GenerationPayload{Phase: "completed", ToolRuns: turn})
			}
			return strings.TrimSpace(resp.Content), nil
		}

		if err := surface.ValidateBatch(a.toolInvocationsWithSurface(ctx, resp.ToolCalls, surface)); err != nil {
			return "", err
		}

		if dup, sig := shared.DetectToolCallDuplicate(resp.ToolCalls, seen, req.Messages); dup {
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "warn",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("repeated tool call: %s", sig.Name)})
			}
			return "", fmt.Errorf("academic repeated tool call: %s", sig.Name)
		}

		errCount, rerouted, delegated, delegatedMessage := a.applyToolCalls(ctx, req, resp, surface)
		a.recordTurn(ctx, req, resp, turn, len(resp.ToolCalls), errCount, turnStart)
		if rerouted {
			return "", skills.ErrRerouteRequested
		}
		if delegated {
			return strings.TrimSpace(delegatedMessage), nil
		}
		consecutiveErrors = shared.UpdateToolErrors(consecutiveErrors, errCount, len(resp.ToolCalls))
		if consecutiveErrors >= shared.MaxConsecutiveToolErrors {
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "error",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("tool calls failed %d consecutive turns", consecutiveErrors)})
			}
			return "", fmt.Errorf("academic tool calls failed %d consecutive turns", consecutiveErrors)
		}
	}

	if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
		shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
			lm.AgentID, lm.SessionID, lm.CorrID, "error",
			&agentlog.ErrorPayload{Error: "exhausted tool-call loop"})
	}
	return "", fmt.Errorf("academic exhausted tool-call loop")
}

// applyToolCalls appends the assistant message and tool results to the request.
func (a *Academic) applyToolCalls(
	ctx context.Context,
	req *providers.Request,
	resp *providers.Response,
	surface toolruntime.Surface,
) (int, bool, bool, string) {
	req.Messages = append(req.Messages, providers.ToolLoopAssistantMessage(resp))

	loadedBefore := len(a.skills.GetLoaded())

	errCount := 0
	rerouted := false
	delegated := false
	delegatedMessage := ""
	for _, call := range resp.ToolCalls {
		if ctx.Err() != nil {
			break
		}
		var execResult toolruntime.ExecutionResult
		var execErr error
		execCtx := shared.WithActiveToolCall(ctx, call)
		result, err := shared.TimedToolCall(execCtx, "academic", call, func() (string, error) {
			execResult, execErr = a.executeToolCallWithSurface(execCtx, call, surface)
			return execResult.Output, execErr
		})
		if execResult.ToolDefsDirty {
			a.toolDefsDirty = true
		}
		isError := false
		isDelegated := false
		if err != nil {
			if errors.Is(err, skills.ErrRerouteRequested) {
				rerouted = true
				result = `{"rerouted": true}`
			} else if errors.Is(err, skills.ErrDelegatedRequested) {
				delegated = true
				isDelegated = true
				delegatedMessage = shared.DelegatedToolMessage(result, err)
				if delegatedMessage == "" {
					delegatedMessage = strings.TrimSpace(result)
				}
			} else {
				result = shared.ToolErrorPayload(err)
				isError = true
				if shared.ToolErrorCountsTowardAbort(err) {
					errCount++
				}
				if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
					shared.LogAgentEvent(lm.EventLogger, agentlog.EventSkillFailed,
						lm.AgentID, lm.SessionID, lm.CorrID, "warn",
						&agentlog.ToolPayload{ToolName: call.Name, Success: false, Err: err.Error()})
				}
			}
		}
		if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			toolEvent := shared.ToolNameToEventType(call.Name)
			shared.LogAgentEvent(lm.EventLogger, toolEvent,
				lm.AgentID, lm.SessionID, lm.CorrID, "info",
				&agentlog.ToolPayload{ToolName: call.Name, Success: !isError && !isDelegated})
		}
		if !isError && call.Name == "author_research_paper" {
			if state := AcademicTurnStateFromContext(ctx); state != nil {
				action := academicTurnActionType(call.Name)
				if err := state.setTerminalAction(action); err != nil {
					isError = true
					result = shared.ToolErrorPayload(err)
					if shared.ToolErrorCountsTowardAbort(err) {
						errCount++
					}
				}
			}
		}
		if gov := shared.ContextGovernorFromContext(ctx); gov != nil && !isError {
			result = gov.LimitToolOutput(ctx, result, call.Name)
		}
		req.Messages = append(req.Messages, providers.Message{
			Role:       providers.RoleTool,
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Content:    result,
			IsError:    isError,
		})
		if !isDelegated {
			if execState := academicResearchExecutionStateFromContext(ctx); execState != nil {
				execState.observeToolResult(ctx, a, call, result, isError)
			}
			if tracker := searchEvidenceTrackerFromContext(ctx); tracker != nil {
				tracker.observeToolResult(call, result, isError)
			}
		}
		if rerouted || delegated {
			break
		}
	}

	if len(a.skills.GetLoaded()) > loadedBefore {
		a.toolDefsDirty = true
	}

	return errCount, rerouted, delegated, delegatedMessage
}

// executeToolCall invokes a skill by name with JSON arguments.
func (a *Academic) executeToolCall(ctx context.Context, call providers.ToolCall) (toolruntime.ExecutionResult, error) {
	return a.executeToolCallWithSurface(ctx, call, a.toolRuntime())
}

func (a *Academic) executeToolCallWithSurface(
	ctx context.Context,
	call providers.ToolCall,
	surface toolruntime.Surface,
) (toolruntime.ExecutionResult, error) {
	name := strings.TrimSpace(call.Name)
	if name == "" {
		return toolruntime.ExecutionResult{}, fmt.Errorf("tool name is required")
	}
	if surface == nil {
		surface = a.toolRuntime()
	}

	raw := strings.TrimSpace(call.Arguments)
	if raw == "" {
		raw = "{}"
	}
	if !json.Valid([]byte(raw)) {
		return toolruntime.ExecutionResult{}, fmt.Errorf("tool arguments for %q are not valid JSON", name)
	}
	correlationID := shared.LogMetaFromContext(ctx).CorrID
	if correlationID == "" {
		correlationID = a.id + "-local"
	}
	return surface.Execute(ctx, toolruntime.Invocation{
		ToolCall:        call,
		AgentID:         surface.AgentID(),
		CorrelationID:   correlationID,
		CapabilityScope: surface.CapabilityScope(),
	})
}

// prepareSkillsForInput progressively loads skills relevant to the user's
// input and optimizes to stay within the tool-definition token budget.
func (a *Academic) prepareSkillsForInput(input string) {
	if a.skillLoader == nil {
		return
	}
	a.skillLoader.LoadForInput(input)
	a.skillLoader.OptimizeForBudget()
}

// buildToolDefinitions converts loaded skills to provider Tool format.
func (a *Academic) buildToolDefinitions() []providers.Tool {
	return a.buildToolDefinitionsWithSurface(a.toolRuntime())
}

func (a *Academic) buildToolDefinitionsWithSurface(surface toolruntime.Surface) []providers.Tool {
	if surface == nil {
		return nil
	}
	surface.SyncActiveFromLoaded()
	tools := surface.BuildToolDefinitions()
	for i := range tools {
		if tools[i].Name != "web_search" {
			continue
		}
		if tools[i].WebSearch == nil {
			tools[i].WebSearch = &providers.WebSearchOptions{}
		}
		if tools[i].WebSearch.MaxUses <= 0 {
			tools[i].WebSearch.MaxUses = a.config.MaxNativeWebSearchCalls
		}
	}
	return tools
}

func (a *Academic) toolRuntime() *toolruntime.Runtime {
	return a.tools
}

func (a *Academic) toolInvocations(ctx context.Context, calls []providers.ToolCall) []toolruntime.Invocation {
	return a.toolInvocationsWithSurface(ctx, calls, a.toolRuntime())
}

func (a *Academic) toolInvocationsWithSurface(
	ctx context.Context,
	calls []providers.ToolCall,
	surface toolruntime.Surface,
) []toolruntime.Invocation {
	if len(calls) == 0 {
		return nil
	}
	if surface == nil {
		surface = a.toolRuntime()
	}
	correlationID := shared.LogMetaFromContext(ctx).CorrID
	if correlationID == "" {
		correlationID = a.id + "-local"
	}
	scope := surface.CapabilityScope()
	invocations := make([]toolruntime.Invocation, 0, len(calls))
	for _, call := range calls {
		invocations = append(invocations, toolruntime.Invocation{
			ToolCall:        call,
			AgentID:         surface.AgentID(),
			CorrelationID:   correlationID,
			CapabilityScope: scope,
		})
	}
	return invocations
}

func (a *Academic) completeLLMTurn(
	ctx context.Context,
	p academicProvider,
	req *providers.Request,
) (*providers.Response, bool, error) {
	if pp := shared.ProgressPublisherFromContext(ctx); pp != nil {
		llmruntime.PromoteForUserFacingTurn(req, pp.SourceAgentID, llmruntime.ThoughtVisibilitySummary)
	}
	if sp, ok := p.(academicStreamingProvider); ok {
		resp, err := a.streamLLMTurn(ctx, sp, req)
		return resp, true, err
	}

	resp, err := shared.CompleteWithWatchdog(ctx, p, req, shared.AgentDisplayName("academic"))
	retryTimeout, shouldRetry := academicLLMRetryTimeout(p)
	if err == nil || !shouldRetryAcademicDeadline(ctx, err, shouldRetry) {
		return resp, false, err
	}

	retryCtx, cancel := academicRetryContext(ctx, retryTimeout)
	defer cancel()

	resp, err = shared.CompleteWithWatchdog(retryCtx, p, req, shared.AgentDisplayName("academic"))
	return resp, false, err
}

type academicStreamingProvider interface {
	academicProvider
	Stream(ctx context.Context, req *providers.Request) (<-chan *providers.StreamChunk, error)
}

func (a *Academic) streamLLMTurn(
	ctx context.Context,
	p academicStreamingProvider,
	req *providers.Request,
) (*providers.Response, error) {
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()

	chunks, err := p.Stream(streamCtx, req)
	if err != nil {
		return nil, err
	}

	cancelWatchdog := shared.StartThinkingWatchdog(ctx, req, shared.AgentDisplayName("academic"))
	defer cancelWatchdog()

	var (
		streamErr         error
		firstVisibleChunk bool
		streamedText      bool
		bufferedText      strings.Builder
	)
	emitter := shared.NewThoughtEmitter(llmruntime.EmitsThoughts(req))

	collector := providers.NewStreamCollector(func(chunk *providers.StreamChunk) {
		if chunk == nil {
			return
		}
		if tracker := searchEvidenceTrackerFromContext(ctx); tracker != nil {
			tracker.observeStreamChunk(chunk)
			if tracker.runawayError() != nil {
				cancelStream()
				return
			}
			if tracker.suppressStreamChunk(chunk) {
				return
			}
		}
		shared.ObserveProviderToolCallChunk(ctx, chunk)
		switch chunk.Type {
		case providers.ChunkTypeStart:
			if chunk.RetryReset {
				bufferedText.Reset()
				streamedText = false
			}
		case providers.ChunkTypeText:
			if chunk.Text == "" {
				return
			}
			if !firstVisibleChunk {
				cancelWatchdog()
				firstVisibleChunk = true
			}
			bufferedText.WriteString(chunk.Text)
		case providers.ChunkTypeThought:
			if chunk.Text == "" {
				return
			}
			if !firstVisibleChunk {
				cancelWatchdog()
				firstVisibleChunk = true
			}
			if thought := emitter.AddDelta(chunk.Text); thought != "" {
				a.publishThoughtProgress(ctx, thought)
			}
		}
	})

	for chunk := range chunks {
		if chunk != nil && chunk.Type == providers.ChunkTypeStart && chunk.RetryReset {
			streamErr = nil
		}
		collector.Add(chunk)
		if tracker := searchEvidenceTrackerFromContext(ctx); tracker != nil && tracker.runawayError() != nil {
			return nil, tracker.runawayError()
		}
		if chunk != nil && chunk.Type == providers.ChunkTypeError {
			streamErr = fmt.Errorf("stream error: %s", chunk.Text)
		}
	}
	if thought := emitter.Flush(); thought != "" {
		a.publishThoughtProgress(ctx, thought)
	}
	resp := collector.Response()
	if execState := academicResearchExecutionStateFromContext(ctx); execState != nil {
		execState.observeProviderResponse(ctx, resp)
	}
	if resp != nil && len(resp.ToolCalls) > 0 {
		shared.PublishIntermediateToolTurn(a.bus, a.channels, ctx, a.id, resp)
		return resp, nil
	}
	if resp != nil {
		if text := bufferedText.String(); strings.TrimSpace(text) != "" {
			if execState := academicResearchExecutionStateFromContext(ctx); execState != nil {
				if reminder, fields := execState.finalizationBlock(); reminder != "" {
					fields["streamed_text_suppressed"] = true
					fields["content_preview"] = truncateStr(strings.TrimSpace(text), 200)
					academicLogResearchStateEvent(ctx, "finalization_blocked", fields)
				} else {
					streamedText = true
					shared.PublishStreamChunk(a.bus, a.channels, ctx, a.id, text)
				}
			} else {
				streamedText = true
				shared.PublishStreamChunk(a.bus, a.channels, ctx, a.id, text)
			}
		}
	}
	if tracker := searchEvidenceTrackerFromContext(ctx); tracker != nil && (resp == nil || strings.TrimSpace(resp.Content) == "") {
		switch tracker.currentPhase() {
		case researchPhaseGround:
			return nil, errAcademicSearchGroundingRequested
		case researchPhaseCorroborate:
			return nil, errAcademicSearchCorroborationRequested
		case researchPhaseSynthesize:
			return nil, errAcademicSearchSynthesisRequested
		}
	}
	if tracker := searchEvidenceTrackerFromContext(ctx); tracker != nil && tracker.runawayError() != nil {
		return nil, tracker.runawayError()
	}
	if streamErr != nil {
		return nil, streamErr
	}
	if resp != nil && streamedText {
		shared.MarkResponseStreamedText(resp)
	}
	return resp, nil
}

func isAcademicPhaseAdvanceError(err error) bool {
	return errors.Is(err, errAcademicSearchGroundingRequested) ||
		errors.Is(err, errAcademicSearchCorroborationRequested) ||
		errors.Is(err, errAcademicSearchSynthesisRequested)
}

func academicSearchPhaseFailure(phase researchPhase) error {
	switch phase {
	case researchPhaseGround:
		return fmt.Errorf("academic research discovered candidate sources but never grounded the answer with `web_fetch`, `fetch_document`, or equivalent source ingestion")
	case researchPhaseCorroborate:
		return fmt.Errorf("academic research grounded one source but never corroborated or resolved the remaining ambiguity before answering")
	case researchPhaseSynthesize:
		return fmt.Errorf("academic research did not converge to a final synthesis after gathering grounded evidence")
	default:
		return fmt.Errorf("academic research did not converge to an answer after search-backed evidence gathering")
	}
}

func academicRepeatedToolCallReminder(toolName string, phase researchPhase, scores researchProgressScores) string {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		toolName = "the same tool batch"
	}
	return fmt.Sprintf(
		"Do not repeat the same tool batch again (stuck on `%s`). %s",
		toolName,
		researchPhaseReminderMessage(phase, scores),
	)
}

func academicRepeatedToolCallFailure(toolName string, tracker *searchEvidenceTracker) error {
	toolName = strings.TrimSpace(toolName)
	if tracker == nil {
		return fmt.Errorf("academic repeated tool call: %s", toolName)
	}
	phaseFailure := academicLoopExhaustionError(tracker, searchDiscipline{})
	if toolName == "" {
		return phaseFailure
	}
	return fmt.Errorf("%s (repeated tool batch: %s)", phaseFailure.Error(), toolName)
}

func academicLoopExhaustionError(tracker *searchEvidenceTracker, discipline searchDiscipline) error {
	if tracker == nil {
		return fmt.Errorf("academic exhausted tool-call loop")
	}
	if err := tracker.runawayError(); err != nil {
		return err
	}
	if discipline.RequireWebSearch && !tracker.sawSearch {
		return searchDisciplineFailure()
	}
	return academicSearchPhaseFailure(tracker.currentPhase())
}

func (a *Academic) publishThoughtProgress(ctx context.Context, thought string) {
	pp := shared.ProgressPublisherFromContext(ctx)
	if pp == nil {
		return
	}
	thought = strings.TrimSpace(thought)
	if thought == "" {
		return
	}
	pp.Publish(thought)
}

func shouldRetryAcademicDeadline(ctx context.Context, err error, hasRetryTimeout bool) bool {
	if err == nil {
		return false
	}
	if !hasRetryTimeout {
		return false
	}
	if ctx == nil {
		return errors.Is(err, context.DeadlineExceeded)
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return false
	}
	return errors.Is(err, context.DeadlineExceeded)
}

func academicRetryContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	base := context.WithoutCancel(ctx)
	retryCtx, cancel := context.WithTimeout(base, timeout)
	stop := context.AfterFunc(ctx, func() {
		if errors.Is(ctx.Err(), context.Canceled) {
			cancel()
		}
	})
	return retryCtx, func() {
		stop()
		cancel()
	}
}

func academicLLMRetryTimeout(p academicProvider) (time.Duration, bool) {
	if reporter, ok := p.(interface{ RequestTimeout() time.Duration }); ok {
		if timeout := reporter.RequestTimeout(); timeout > 0 {
			return timeout, true
		}
	}
	return 0, false
}

// recordTurn feeds the handoff bridge with turn metrics from this LLM call.
func (a *Academic) recordTurn(
	ctx context.Context,
	req *providers.Request,
	resp *providers.Response,
	turn, toolCalls, errCount int,
	turnStart time.Time,
) {
	if a.handoffBridge == nil || !shared.AutomaticHandoffEnabled(ctx) {
		return
	}

	a.handoffBridge.RecordTurn(shared.BuildHandoffTurnRecord(ctx, req, resp, turn, toolCalls, errCount, turnStart))
}
