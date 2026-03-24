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

// executeToolLoop runs the LLM tool-call loop: Complete → check ToolCalls →
// execute → append results → repeat, bounded by config.MaxToolRuns.
// Follows the engineer tool loop pattern exactly.
func (a *Academic) executeToolLoop(
	ctx context.Context,
	req *providers.Request,
	ledger *steering.SteeringLedger,
	surface toolruntime.Surface,
) (string, error) {
	maxRuns := a.config.MaxToolRuns
	consecutiveErrors := 0
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
			req.Tools = a.buildToolDefinitionsWithSurface(surface)
			a.toolDefsDirty = false
		}

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

		if len(resp.ToolCalls) == 0 {
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

		errCount, rerouted := a.applyToolCalls(ctx, req, resp, surface)
		a.recordTurn(ctx, req, resp, turn, len(resp.ToolCalls), errCount, turnStart)
		if rerouted {
			return "", skills.ErrRerouteRequested
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
) (int, bool) {
	req.Messages = append(req.Messages, providers.ToolLoopAssistantMessage(resp))

	loadedBefore := len(a.skills.GetLoaded())

	errCount := 0
	rerouted := false
	for _, call := range resp.ToolCalls {
		if ctx.Err() != nil {
			break
		}
		var execResult toolruntime.ExecutionResult
		var execErr error
		result, err := shared.TimedToolCall(ctx, "academic", call, func() (string, error) {
			execResult, execErr = a.executeToolCallWithSurface(ctx, call, surface)
			return execResult.Output, execErr
		})
		if execResult.ToolDefsDirty {
			a.toolDefsDirty = true
		}
		isError := false
		if err != nil {
			if errors.Is(err, skills.ErrRerouteRequested) {
				rerouted = true
				result = `{"rerouted": true}`
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
				&agentlog.ToolPayload{ToolName: call.Name, Success: !isError})
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
		if rerouted {
			break
		}
	}

	if len(a.skills.GetLoaded()) > loadedBefore {
		a.toolDefsDirty = true
	}

	return errCount, rerouted
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
	return surface.BuildToolDefinitions()
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
	chunks, err := p.Stream(ctx, req)
	if err != nil {
		return nil, err
	}

	cancelWatchdog := shared.StartThinkingWatchdog(ctx, req, shared.AgentDisplayName("academic"))
	defer cancelWatchdog()

	var (
		streamErr         error
		firstVisibleChunk bool
	)
	emitter := shared.NewThoughtEmitter(llmruntime.EmitsThoughts(req))

	collector := providers.NewStreamCollector(func(chunk *providers.StreamChunk) {
		if chunk == nil {
			return
		}
		switch chunk.Type {
		case providers.ChunkTypeText:
			if chunk.Text == "" {
				return
			}
			if !firstVisibleChunk {
				cancelWatchdog()
				firstVisibleChunk = true
			}
			shared.PublishStreamChunk(a.bus, a.channels, ctx, a.id, chunk.Text)
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
		if chunk != nil && chunk.Type == providers.ChunkTypeError {
			streamErr = fmt.Errorf("stream error: %s", chunk.Text)
		}
	}
	if thought := emitter.Flush(); thought != "" {
		a.publishThoughtProgress(ctx, thought)
	}
	if streamErr != nil {
		return nil, streamErr
	}
	return collector.Response(), nil
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
	if a.handoffBridge == nil {
		return
	}

	a.handoffBridge.RecordTurn(shared.BuildHandoffTurnRecord(ctx, req, resp, turn, toolCalls, errCount, turnStart))
}
