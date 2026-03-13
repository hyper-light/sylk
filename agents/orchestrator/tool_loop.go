package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/steering"
	"github.com/adalundhe/sylk/core/toolruntime"
)

// executeToolLoop runs the model tool-call loop: Complete → check ToolCalls →
// execute → append results → repeat, bounded by config.MaxToolRuns.
func (o *Orchestrator) executeToolLoop(ctx context.Context, req *providers.Request, ledger *steering.SteeringLedger) (string, error) {
	seen := make(map[shared.ToolCallSignature]int, o.config.MaxToolRuns)
	consecutiveErrors := 0

	for turn := 0; turn <= o.config.MaxToolRuns; turn++ {
		if o.toolDefsDirty {
			req.Tools = o.buildToolDefinitions()
			o.toolDefsDirty = false
		}

		// ── STEERING CHECKPOINT ──
		sc := shared.DrainAndCheckpoint(ledger, req, turn, "orchestrating", nil)
		if sc.Rollback != nil || sc.EditReplay != nil {
			cp := sc.Rollback
			if cp == nil {
				cp = sc.EditReplay
			}
			req.Messages = req.Messages[:cp.MessageCount]
			if sc.EditReplay != nil {
				req.Messages = append(req.Messages, providers.Message{Role: providers.RoleUser, Content: sc.EditText})
			}
			turn = cp.Turn
			seen = make(map[shared.ToolCallSignature]int, o.config.MaxToolRuns)
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

		// ── CONTEXT BUDGET ──
		if err := shared.ApplyContextBudget(ctx, turn, o.config.MaxToolRuns, req); err != nil {
			return "", err
		}

		turnStart := time.Now()
		resp, err := shared.CompleteWithWatchdog(ctx, o.provider, req, shared.AgentDisplayName("orchestrator"))
		if err != nil {
			return "", fmt.Errorf("orchestrator llm: %w", err)
		}

		if gov := shared.ContextGovernorFromContext(ctx); gov != nil {
			gov.Calibrate(ctx, resp, req.Messages)
		}
		o.publishStreamChunk(ctx, shared.IntermediateToolTurnText(resp))

		if len(resp.ToolCalls) == 0 {
			o.recordTurn(req, resp, turn, 0, 0, turnStart)
			return strings.TrimSpace(resp.Content), nil
		}

		if err := o.toolRuntime().ValidateBatch(o.toolInvocations(ctx, resp.ToolCalls)); err != nil {
			return "", err
		}

		if dup, sig := shared.DetectToolCallDuplicate(resp.ToolCalls, seen, req.Messages); dup {
			return "", fmt.Errorf("orchestrator repeated tool call: %s", sig.Name)
		}

		errCount, rerouted := o.applyToolCalls(ctx, req, resp)
		o.recordTurn(req, resp, turn, len(resp.ToolCalls), errCount, turnStart)
		if rerouted {
			return "", skills.ErrRerouteRequested
		}
		consecutiveErrors = shared.UpdateToolErrors(consecutiveErrors, errCount, len(resp.ToolCalls))
		if consecutiveErrors >= 2 {
			return "", fmt.Errorf("orchestrator tool calls failed %d consecutive turns", consecutiveErrors)
		}
	}

	return "", fmt.Errorf("orchestrator exhausted tool-call loop")
}

// applyToolCalls appends the assistant message and tool results to the request.
// Returns the error count and whether a reroute was requested.
func (o *Orchestrator) applyToolCalls(
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

	errCount := 0
	rerouted := false
	for _, call := range resp.ToolCalls {
		o.publishActivity(events.EventTypeToolResult, call.Name)
		var execResult toolruntime.ExecutionResult
		var execErr error
		result, err := shared.TimedToolCall(ctx, "orchestrator", call, func() (string, error) {
			execResult, execErr = o.executeToolCall(ctx, call)
			return execResult.Output, execErr
		})
		if execResult.ToolDefsDirty {
			o.toolDefsDirty = true
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
		if rerouted {
			break
		}
	}
	return errCount, rerouted
}

// executeToolCall invokes a skill by name with JSON arguments.
func (o *Orchestrator) executeToolCall(ctx context.Context, call providers.ToolCall) (toolruntime.ExecutionResult, error) {
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
		correlationID = o.config.AgentID + "-local"
	}
	return o.toolRuntime().Execute(ctx, toolruntime.Invocation{
		ToolCall:        call,
		AgentID:         o.toolRuntime().AgentID(),
		CorrelationID:   correlationID,
		CapabilityScope: o.toolRuntime().CapabilityScope(),
	})
}

// prepareSkillsForInput progressively loads skills relevant to the user's
// input and optimizes to stay within the tool-definition token budget.
func (o *Orchestrator) prepareSkillsForInput(input string) {
	if o.skillLoader == nil {
		return
	}
	o.skillLoader.LoadForInput(input)
	o.skillLoader.OptimizeForBudget()
}

// buildToolDefinitions converts loaded skills to provider tool format.
func (o *Orchestrator) buildToolDefinitions() []providers.Tool {
	o.toolRuntime().SyncActiveFromLoaded()
	return o.toolRuntime().BuildToolDefinitions()
}

func (o *Orchestrator) toolRuntime() *toolruntime.Runtime {
	return o.tools
}

func (o *Orchestrator) toolInvocations(ctx context.Context, calls []providers.ToolCall) []toolruntime.Invocation {
	if len(calls) == 0 {
		return nil
	}
	correlationID := shared.LogMetaFromContext(ctx).CorrID
	if correlationID == "" {
		correlationID = o.config.AgentID + "-local"
	}
	scope := o.toolRuntime().CapabilityScope()
	invocations := make([]toolruntime.Invocation, 0, len(calls))
	for _, call := range calls {
		invocations = append(invocations, toolruntime.Invocation{
			ToolCall:        call,
			AgentID:         o.toolRuntime().AgentID(),
			CorrelationID:   correlationID,
			CapabilityScope: scope,
		})
	}
	return invocations
}

// recordTurn feeds the handoff bridge with turn metrics from this LLM call.
func (o *Orchestrator) recordTurn(
	req *providers.Request,
	resp *providers.Response,
	turn, toolCalls, errCount int,
	turnStart time.Time,
) {
	if o.handoffBridge == nil {
		return
	}

	o.handoffBridge.RecordTurn(handoff.TurnRecord{
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
