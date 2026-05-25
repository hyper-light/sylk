package guardian

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/fabric"
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/steering"
	"github.com/adalundhe/sylk/core/toolruntime"
)

// executeToolLoop runs the LLM tool-call loop: stream → check ToolCalls →
// execute → append results → repeat, bounded by config.MaxToolRuns.
// Follows the Architect pattern.
func (g *Guardian) executeToolLoop(
	ctx context.Context,
	req *providers.Request,
	stage string,
	onChunk func(string),
	ledger *steering.SteeringLedger,
) (string, *guide.StreamUsage, error) {
	maxRuns := g.config.MaxToolRuns
	consecutiveErrors := 0
	usageAcc := &guardianUsageAccumulator{}

	provider := g.getProvider()
	if provider == nil {
		if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
				lm.AgentID, lm.SessionID, lm.CorrID, "error",
				&agentlog.ErrorPayload{Error: "no LLM provider configured"})
		}
		return "", nil, fmt.Errorf("guardian: %w: LLM provider not yet wired", shared.ErrAgentNotReady)
	}

	g.logDebug("tool_loop: START",
		"stage", stage,
		"max_runs", maxRuns,
		"tools_count", len(req.Tools),
		"messages_count", len(req.Messages))

	loopStart := time.Now()
	for turn := 0; turn <= maxRuns; turn++ {
		if ctx.Err() != nil {
			g.logWarn("executeToolLoop: context cancelled", "stage", stage, "turn", turn)
			return "", usageAcc.Total(), ctx.Err()
		}

		// ── STEERING CHECKPOINT ──
		sc := shared.DrainAndCheckpoint(ledger, req, turn, stage, nil,
			shared.WithBoardProvider(func() *claims.ClaimsBoard { return g.guardianBoard() }, g.id))
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
				return "", usageAcc.Total(), err
			}
			continue
		}
		// ── END STEERING ──

		if g.toolDefsDirty {
			req.Tools = g.buildToolDefinitions()
			g.toolDefsDirty = false
		}

		// ── CONTEXT BUDGET ──
		if err := shared.ApplyContextBudget(ctx, turn, maxRuns, req); err != nil {
			return "", usageAcc.Total(), err
		}

		shared.LogLLMCallStartedFromContext(ctx, req)
		turnStart := time.Now()
		resp, err := g.streamToolLoopTurn(ctx, provider, req, onChunk)
		shared.LogLLMCallFromContext(ctx, req.Model, resp, time.Since(turnStart), err)
		if err != nil {
			// User interrupt (Ctrl+C / Esc) cancels reqCtx via steering —
			// treat as clean abort, not an error worth logging.
			if ctx.Err() != nil {
				return "", usageAcc.Total(), ctx.Err()
			}
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "error",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("llm: %v", err)})
			}
			g.logWarn("executeToolLoop: LLM error", "stage", stage, "turn", turn, "err", err)
			return "", usageAcc.Total(), fmt.Errorf("guardian llm: %w", err)
		}
		usageAcc.Add(&resp.Usage)

		if gov := shared.ContextGovernorFromContext(ctx); gov != nil {
			gov.Calibrate(ctx, resp, req.Messages)
		}

		if len(resp.ToolCalls) == 0 {
			g.recordTurn(ctx, req, resp, turn, 0, 0, turnStart)
			g.logDebug("tool_loop: COMPLETE",
				"stage", stage, "turn", turn,
				"content_len", len(resp.Content),
				"total_elapsed", time.Since(loopStart).String())
			return strings.TrimSpace(resp.Content), usageAcc.Total(), nil
		}

		if err := g.tools.ValidateBatch(g.toolInvocations(ctx, resp.ToolCalls)); err != nil {
			return "", usageAcc.Total(), err
		}

		errCount, rerouted, yielded := g.applyToolCalls(ctx, req, resp)
		g.recordTurn(ctx, req, resp, turn, len(resp.ToolCalls), errCount, turnStart)
		if yielded {
			return "", usageAcc.Total(), shared.ErrConsultYielded
		}
		if rerouted {
			return "", usageAcc.Total(), skills.ErrRerouteRequested
		}
		consecutiveErrors = shared.UpdateToolErrors(consecutiveErrors, errCount, len(resp.ToolCalls))
		if consecutiveErrors >= shared.MaxConsecutiveToolErrors {
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "error",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("tool calls failed %d consecutive turns", consecutiveErrors)})
			}
			return "", usageAcc.Total(), fmt.Errorf("guardian tool calls failed %d consecutive turns", consecutiveErrors)
		}
	}

	g.logWarn("executeToolLoop: loop exhausted, falling back to deterministic report", "stage", stage)
	report, err := g.generateHealthReport()
	return report, usageAcc.Total(), err
}

func (g *Guardian) streamToolLoopTurn(
	ctx context.Context,
	provider guardianProvider,
	req *providers.Request,
	onChunk func(string),
) (*providers.Response, error) {
	if pp := shared.ProgressPublisherFromContext(ctx); pp != nil {
		llmruntime.PromoteForUserFacingTurn(req, pp.SourceAgentID, llmruntime.ThoughtVisibilitySummary)
	}
	chunks, err := provider.Stream(ctx, req)
	if err != nil {
		return nil, err
	}

	correlationID := shared.LogMetaFromContext(ctx).CorrID
	emitter := shared.NewThoughtEmitter(llmruntime.EmitsThoughts(req))
	collector := providers.NewStreamCollector(func(chunk *providers.StreamChunk) {
		shared.ObserveProviderToolCallChunk(ctx, chunk)
		switch chunk.Type {
		case providers.ChunkTypeStart:
			if chunk.RetryReset {
				g.publishStreamStart(ctx, correlationID)
			}
		case providers.ChunkTypeText:
			if onChunk != nil && chunk.Text != "" {
				onChunk(chunk.Text)
			}
		case providers.ChunkTypeThought:
			if llmruntime.EmitsThoughts(req) {
				if thought := emitter.AddDelta(chunk.Text); thought != "" {
					g.publishThoughtProgress(ctx, correlationID, thought)
					shared.LogThinkingChunk(ctx, thought)
				}
			}
		}
	})

	var streamErr error
	for chunk := range chunks {
		if chunk != nil && chunk.Type == providers.ChunkTypeStart && chunk.RetryReset {
			streamErr = nil
		}
		collector.Add(chunk)
		if chunk.Type == providers.ChunkTypeError {
			streamErr = fmt.Errorf("stream error: %s", chunk.Text)
		}
	}
	if thought := emitter.Flush(); thought != "" {
		g.publishThoughtProgress(ctx, correlationID, thought)
		shared.LogThinkingChunk(ctx, thought)
	}
	if streamErr != nil {
		return nil, streamErr
	}
	return collector.Response(), nil
}

// applyToolCalls appends assistant message and tool results to the request.
func (g *Guardian) applyToolCalls(
	ctx context.Context,
	req *providers.Request,
	resp *providers.Response,
) (int, bool, bool) {
	req.Messages = append(req.Messages, providers.ToolLoopAssistantMessage(resp))

	errCount := 0
	rerouted := false
	yielded := false
	for i, call := range resp.ToolCalls {
		if ctx.Err() != nil {
			break
		}
		callStart := time.Now()
		var execResult toolruntime.ExecutionResult
		var execErr error
		execCtx := shared.WithActiveToolCall(ctx, call)
		result, err := shared.TimedToolCall(execCtx, "guardian", call, func() (string, error) {
			execResult, execErr = g.executeToolCall(execCtx, call)
			return execResult.Output, execErr
		})
		if execResult.ToolDefsDirty {
			g.toolDefsDirty = true
		}
		if execResult.Yielded() {
			yielded = true
			return errCount, rerouted, yielded
		}
		g.logDebug("tool_apply: EXECUTE_DONE",
			"tool_name", call.Name, "tool_index", i,
			"elapsed", time.Since(callStart).String(),
			"result_len", len(result), "err", err)

		isError := false
		if err != nil {
			if isRerouteError(err) {
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
			shared.LogAgentEvent(lm.EventLogger, shared.ToolNameToEventType(call.Name),
				lm.AgentID, lm.SessionID, lm.CorrID, "info",
				&agentlog.ToolPayload{ToolName: call.Name, Success: !isError})
		}
		if gov := shared.ContextGovernorFromContext(ctx); gov != nil && !isError {
			result = gov.LimitToolOutput(ctx, result, call.Name)
		}
		// Activity Fabric ambient_context envelope.
		result = fabric.AppendAmbientContext(ctx, fabric.AmbientEnvelopeConfig{
			SessionID:  func() string { return g.config.SessionID },
			AgentID:    func() string { return g.id },
			AgentType:  func() string { return "guardian" },
			PipelineID: func() string { return "" },
		}, result)
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

	return errCount, rerouted, yielded
}

func isRerouteError(err error) bool {
	return errors.Is(err, skills.ErrRerouteRequested)
}

func (g *Guardian) toolInvocations(ctx context.Context, calls []providers.ToolCall) []toolruntime.Invocation {
	if len(calls) == 0 {
		return nil
	}
	correlationID := shared.LogMetaFromContext(ctx).CorrID
	scope := g.toolRuntime().CapabilityScope()
	invocations := make([]toolruntime.Invocation, 0, len(calls))
	for _, call := range calls {
		invocations = append(invocations, toolruntime.Invocation{
			ToolCall:        call,
			AgentID:         g.toolRuntime().AgentID(),
			CorrelationID:   correlationID,
			CapabilityScope: scope,
		})
	}
	return invocations
}

// recordTurn feeds the handoff bridge with turn metrics from this LLM call.
func (g *Guardian) recordTurn(
	ctx context.Context,
	req *providers.Request,
	resp *providers.Response,
	turn, toolCalls, errCount int,
	turnStart time.Time,
) {
	if g.handoffBridge == nil {
		return
	}

	g.handoffBridge.RecordTurn(shared.BuildHandoffTurnRecord(ctx, req, resp, turn, toolCalls, errCount, turnStart))
}
