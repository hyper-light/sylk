package librarian

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/steering"
	"github.com/adalundhe/sylk/core/toolruntime"
)

// executeToolLoop runs the LLM tool-call loop: Complete → check ToolCalls →
// execute → append results → repeat, bounded by config.MaxToolRuns.
//
// The librarian's tool loop differs from other agents in that it maintains
// a SearchLedger that tracks all search evidence across turns. The ledger
// detects search saturation (repeated/overlapping results) and injects an
// evidence summary into the LLM context so the model knows what it has
// found and when to stop searching. This prevents both the "repeated tool
// call" abort and the "exceeded tool-call limit" abort by giving the LLM
// data-driven signals to synthesize an answer.
func (l *Librarian) executeToolLoop(ctx context.Context, req *providers.Request, ledger *steering.SteeringLedger) (string, error) {
	maxRuns := l.config.MaxToolRuns
	seen := make(map[shared.ToolCallSignature]int, maxRuns)
	consecutiveErrors := 0
	searchLedger := NewSearchLedger()

	p := l.getProvider()
	if p == nil {
		if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
				lm.AgentID, lm.SessionID, lm.CorrID, "error",
				&agentlog.ErrorPayload{Error: "no LLM provider configured"})
		}
		return "", fmt.Errorf("librarian: no LLM provider configured")
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
		sc := shared.DrainAndCheckpoint(ledger, req, turn, "searching", nil)
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
			seen = make(map[shared.ToolCallSignature]int, maxRuns)
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

		if l.toolDefsDirty {
			req.Tools = l.buildToolDefinitions()
			l.toolDefsDirty = false
		}

		// ── SEARCH SATURATION CHECK ──
		// When the search ledger detects saturation (consecutive searches
		// returning only already-seen files), strip tools to force synthesis.
		// This is data-driven — no magic turn thresholds.
		if searchLedger.IsSaturated() {
			req.Tools = nil
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventGenerationCompleted,
					lm.AgentID, lm.SessionID, lm.CorrID, "info",
					&agentlog.GenerationPayload{Phase: "search_saturated", ToolRuns: turn})
			}
		}

		// ── EVIDENCE SUMMARY INJECTION ──
		// After the first search turn, inject the evidence summary so the LLM
		// sees what it has found and can make informed decisions about whether
		// to continue searching or synthesize.
		if summary := searchLedger.EvidenceSummary(); summary != "" {
			injectEvidenceSummary(req, summary)
		}

		// ── CONTEXT BUDGET ──
		if err := shared.ApplyContextBudget(ctx, turn, maxRuns, req); err != nil {
			return "", err
		}

		turnStart := time.Now()
		resp, err := shared.CompleteWithWatchdog(ctx, p, req, shared.AgentDisplayName("librarian"))
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "error",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("llm: %v", err)})
			}
			return "", fmt.Errorf("librarian llm: %w", err)
		}

		if gov := shared.ContextGovernorFromContext(ctx); gov != nil {
			gov.Calibrate(ctx, resp, req.Messages)
		}

		// ── NO TOOL CALLS → RETURN ANSWER ──
		if len(resp.ToolCalls) == 0 {
			content := strings.TrimSpace(resp.Content)
			if content == "" {
				// Empty content with no tool calls — the context governor may
				// have stripped tools, or the model produced a degenerate
				// response. Inject a synthesis nudge and retry exactly once.
				if turn < maxRuns {
					req.Messages = append(req.Messages, providers.Message{
						Role:    providers.RoleAssistant,
						Content: resp.Content,
					}, providers.Message{
						Role:    providers.RoleUser,
						Content: "Your previous response was empty. You MUST provide a substantive natural language answer based on the tool results you have gathered so far. Summarize your findings clearly.",
					})
					continue
				}
				return "", fmt.Errorf("librarian: LLM returned empty response after %d turns", turn)
			}
			l.recordTurn(req, resp, turn, 0, 0, turnStart)
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventGenerationCompleted,
					lm.AgentID, lm.SessionID, lm.CorrID, "info",
					&agentlog.GenerationPayload{Phase: "completed", ToolRuns: turn})
			}
			return content, nil
		}

		// ── DUPLICATE DETECTION ──
		if dup, sig := shared.DetectToolCallDuplicate(resp.ToolCalls, seen); dup {
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "warn",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("repeated tool call: %s (forcing synthesis)", sig.Name)})
			}
			// The LLM is stuck calling the same tool with the same args.
			// Strip tools and inject the evidence summary to force synthesis.
			req.Messages = append(req.Messages, providers.Message{
				Role:      providers.RoleAssistant,
				Content:   resp.Content,
				ToolCalls: resp.ToolCalls,
				Metadata:  resp.ProviderMetadata,
			})
			for _, call := range resp.ToolCalls {
				req.Messages = append(req.Messages, providers.Message{
					Role:       providers.RoleTool,
					ToolCallID: call.ID,
					ToolName:   call.Name,
					Content:    fmt.Sprintf(`{"error":"Duplicate search — you already called %s with identical arguments. Use your existing results."}`, call.Name),
					IsError:    true,
				})
			}
			req.Tools = nil // force synthesis on next turn
			continue
		}

		// ── TOOL-CALL LIMIT ──
		if turn == maxRuns {
			// Tools were stripped by ApplyContextBudget but the LLM still
			// emitted tool calls. Return any text content as a partial answer.
			if content := strings.TrimSpace(resp.Content); content != "" {
				if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
					shared.LogAgentEvent(lm.EventLogger, agentlog.EventGenerationCompleted,
						lm.AgentID, lm.SessionID, lm.CorrID, "warn",
						&agentlog.GenerationPayload{Phase: "completed_at_limit", ToolRuns: turn})
				}
				l.recordTurn(req, resp, turn, 0, 0, turnStart)
				return content, nil
			}
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "error",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("exceeded tool-call limit (%d)", maxRuns)})
			}
			return "", fmt.Errorf("librarian exceeded tool-call limit (%d)", maxRuns)
		}

		if err := l.toolRuntime().ValidateBatch(l.toolInvocations(ctx, resp.ToolCalls)); err != nil {
			return "", err
		}

		// ── EXECUTE TOOL CALLS ──
		errCount, rerouted := l.applyToolCalls(ctx, req, resp, turn, searchLedger)
		l.recordTurn(req, resp, turn, len(resp.ToolCalls), errCount, turnStart)
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
			return "", fmt.Errorf("librarian tool calls failed %d consecutive turns", consecutiveErrors)
		}
	}

	if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
		shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
			lm.AgentID, lm.SessionID, lm.CorrID, "error",
			&agentlog.ErrorPayload{Error: "exhausted tool-call loop"})
	}
	return "", fmt.Errorf("librarian exhausted tool-call loop")
}

// injectEvidenceSummary appends or replaces the evidence summary at the
// end of the message list. The summary is placed as the final message
// so the LLM sees it as current context right before generating its
// response. If a prior evidence summary exists at the tail, it is
// replaced in-place to avoid unbounded growth.
func injectEvidenceSummary(req *providers.Request, summary string) {
	// Replace existing tail evidence summary if present.
	if n := len(req.Messages); n > 0 {
		tail := &req.Messages[n-1]
		if tail.Role == providers.RoleUser &&
			strings.HasPrefix(tail.Content, "[SEARCH EVIDENCE LEDGER]") {
			tail.Content = summary
			return
		}
	}
	// Append new evidence summary.
	req.Messages = append(req.Messages, providers.Message{
		Role:    providers.RoleUser,
		Content: summary,
	})
}

// applyToolCalls appends the assistant message and tool results to the request.
// Records each search tool's results into the SearchLedger.
func (l *Librarian) applyToolCalls(
	ctx context.Context,
	req *providers.Request,
	resp *providers.Response,
	turn int,
	searchLedger *SearchLedger,
) (int, bool) {
	req.Messages = append(req.Messages, providers.Message{
		Role:      providers.RoleAssistant,
		Content:   strings.TrimSpace(resp.Content),
		ToolCalls: resp.ToolCalls,
		Metadata:  resp.ProviderMetadata,
	})

	loadedBefore := len(l.skills.GetLoaded())

	errCount := 0
	rerouted := false
	for _, call := range resp.ToolCalls {
		if ctx.Err() != nil {
			break
		}
		var execResult toolruntime.ExecutionResult
		var execErr error
		result, err := shared.TimedToolCall(ctx, "librarian", call, func() (string, error) {
			execResult, execErr = l.executeToolCall(ctx, call)
			return execResult.Output, execErr
		})
		if execResult.ToolDefsDirty {
			l.toolDefsDirty = true
		}
		isError := false
		if err != nil {
			if errors.Is(err, skills.ErrRerouteRequested) {
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
			toolEvent := shared.ToolNameToEventType(call.Name)
			shared.LogAgentEvent(lm.EventLogger, toolEvent,
				lm.AgentID, lm.SessionID, lm.CorrID, "info",
				&agentlog.ToolPayload{ToolName: call.Name, Success: !isError})
		}
		if gov := shared.ContextGovernorFromContext(ctx); gov != nil && !isError {
			result = gov.LimitToolOutput(ctx, result, call.Name)
		}

		// Record successful search results into the ledger.
		if !isError {
			searchLedger.Record(turn, call.Name, call.Arguments, result)
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

	if len(l.skills.GetLoaded()) > loadedBefore {
		l.toolDefsDirty = true
	}

	return errCount, rerouted
}

// executeToolCall invokes a skill by name with JSON arguments.
func (l *Librarian) executeToolCall(ctx context.Context, call providers.ToolCall) (toolruntime.ExecutionResult, error) {
	name := strings.TrimSpace(call.Name)
	if name == "" {
		return toolruntime.ExecutionResult{}, fmt.Errorf("tool name is required")
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
		correlationID = l.id + "-local"
	}
	return l.toolRuntime().Execute(ctx, toolruntime.Invocation{
		ToolCall:        call,
		AgentID:         l.id,
		CorrelationID:   correlationID,
		CapabilityScope: l.toolRuntime().CapabilityScope(),
	})
}

// prepareSkillsForInput progressively loads skills relevant to the user's
// input and optimizes to stay within the tool-definition token budget.
func (l *Librarian) prepareSkillsForInput(input string) {
	if l.skillLoader == nil {
		return
	}
	l.skillLoader.LoadForInput(input)
	l.skillLoader.OptimizeForBudget()
}

// buildToolDefinitions converts loaded skills to provider Tool format.
func (l *Librarian) buildToolDefinitions() []providers.Tool {
	l.toolRuntime().SyncActiveFromLoaded()
	return l.toolRuntime().BuildToolDefinitions()
}

func (l *Librarian) toolRuntime() *toolruntime.Runtime {
	return l.tools
}

func (l *Librarian) toolInvocations(ctx context.Context, calls []providers.ToolCall) []toolruntime.Invocation {
	if len(calls) == 0 {
		return nil
	}
	correlationID := shared.LogMetaFromContext(ctx).CorrID
	if correlationID == "" {
		correlationID = l.id + "-local"
	}
	scope := l.toolRuntime().CapabilityScope()
	invocations := make([]toolruntime.Invocation, 0, len(calls))
	for _, call := range calls {
		invocations = append(invocations, toolruntime.Invocation{
			ToolCall:        call,
			AgentID:         l.id,
			CorrelationID:   correlationID,
			CapabilityScope: scope,
		})
	}
	return invocations
}

// recordTurn feeds the handoff bridge with turn metrics from this LLM call.
func (l *Librarian) recordTurn(
	req *providers.Request,
	resp *providers.Response,
	turn, toolCalls, errCount int,
	turnStart time.Time,
) {
	if l.handoffBridge == nil {
		return
	}

	l.handoffBridge.RecordTurn(handoff.TurnRecord{
		InputTokens:      resp.Usage.InputTokens,
		OutputTokens:     resp.Usage.OutputTokens,
		ContextSize:      shared.EstimateContextSize(req.Messages),
		ToolCalls:        toolCalls,
		ToolSuccesses:    toolCalls - errCount,
		TurnNumber:       turn + 1,
		Duration:         time.Since(turnStart),
		Timestamp:        time.Now(),
		Stage:            llmruntime.StageFromRequest(req),
		RuntimeProfile:   llmruntime.ProfileNameFromRequest(req),
		StopReason:       resp.StopReason,
		CacheReadTokens:  resp.Usage.CacheReadTokens,
		CacheWriteTokens: resp.Usage.CacheWriteTokens,
	})
}
