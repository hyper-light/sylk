package guardian

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/steering"
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
		return "", nil, fmt.Errorf("guardian: no LLM provider configured")
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
		sc := shared.DrainAndCheckpoint(ledger, req, turn, stage, nil)
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
			g.recordTurn(req, resp, turn, 0, 0, turnStart)
			g.logDebug("tool_loop: COMPLETE",
				"stage", stage, "turn", turn,
				"content_len", len(resp.Content),
				"total_elapsed", time.Since(loopStart).String())
			return strings.TrimSpace(resp.Content), usageAcc.Total(), nil
		}

		errCount, rerouted := g.applyToolCalls(ctx, req, resp)
		g.recordTurn(req, resp, turn, len(resp.ToolCalls), errCount, turnStart)
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
	chunks, err := provider.Stream(ctx, req)
	if err != nil {
		return nil, err
	}

	correlationID := shared.LogMetaFromContext(ctx).CorrID
	var thoughts strings.Builder
	collector := providers.NewStreamCollector(func(chunk *providers.StreamChunk) {
		switch chunk.Type {
		case providers.ChunkTypeStart:
			thoughts.Reset()
			if chunk.RetryReset {
				g.publishStreamStart(ctx, correlationID)
			}
		case providers.ChunkTypeText:
			if onChunk != nil && chunk.Text != "" {
				onChunk(chunk.Text)
			}
		case providers.ChunkTypeThought:
			thoughts.WriteString(chunk.Text)
			g.publishThoughtProgress(ctx, correlationID, thoughts.String())
		}
	})

	var streamErr error
	for chunk := range chunks {
		collector.Add(chunk)
		if chunk.Type == providers.ChunkTypeError {
			streamErr = fmt.Errorf("stream error: %s", chunk.Text)
		}
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
) (int, bool) {
	req.Messages = append(req.Messages, providers.Message{
		Role:      providers.RoleAssistant,
		Content:   strings.TrimSpace(resp.Content),
		ToolCalls: resp.ToolCalls,
		Metadata:  resp.ProviderMetadata,
	})

	loadedBefore := len(g.skills.GetLoaded())

	errCount := 0
	rerouted := false
	for i, call := range resp.ToolCalls {
		if ctx.Err() != nil {
			break
		}
		callStart := time.Now()
		result, err := shared.TimedToolCall(ctx, "guardian", call, func() (string, error) {
			return g.executeToolCall(ctx, call)
		})
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
				errCount++
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

	if len(g.skills.GetLoaded()) > loadedBefore {
		g.toolDefsDirty = true
	}

	return errCount, rerouted
}

func isRerouteError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "reroute")
}

// recordTurn feeds the handoff bridge with turn metrics from this LLM call.
func (g *Guardian) recordTurn(
	req *providers.Request,
	resp *providers.Response,
	turn, toolCalls, errCount int,
	turnStart time.Time,
) {
	if g.handoffBridge == nil {
		return
	}

	g.handoffBridge.RecordTurn(handoff.TurnRecord{
		InputTokens:      resp.Usage.InputTokens,
		OutputTokens:     resp.Usage.OutputTokens,
		ContextSize:      shared.EstimateContextSize(req.Messages),
		ToolCalls:        toolCalls,
		ToolSuccesses:    toolCalls - errCount,
		TurnNumber:       turn + 1,
		Duration:         time.Since(turnStart),
		Timestamp:        time.Now(),
		StopReason:       resp.StopReason,
		CacheReadTokens:  resp.Usage.CacheReadTokens,
		CacheWriteTokens: resp.Usage.CacheWriteTokens,
	})
}
