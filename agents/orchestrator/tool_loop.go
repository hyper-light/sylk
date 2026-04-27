package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/fabric"
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
	requiredActionGraceTurns := 0

	for turn := 0; turn <= o.config.MaxToolRuns+requiredActionGraceTurns; turn++ {
		if o.toolDefsDirty {
			req.Tools = o.buildToolDefinitions()
			o.toolDefsDirty = false
		}

		// ── STEERING CHECKPOINT ──
		sc := shared.DrainAndCheckpoint(ledger, req, turn, "orchestrating", nil,
			shared.WithBoardProvider(func() *claims.ClaimsBoard { return o.orchestratorBoardOrNil() }, o.AgentID()))
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
			// Enforce the global-review protocol contract: when this turn is
			// answering an inspector challenge that targets the orchestrator,
			// the LLM must end with `validate_work`. ValidateGlobalReviewCompletion
			// is a no-op when no global-review state is hydrated (i.e. ordinary
			// orchestrator turns), so this is safe for all callers.
			if err := shared.ValidateGlobalReviewCompletion(ctx, "orchestrator"); err != nil {
				o.recordTurn(ctx, req, resp, turn, 0, 1, turnStart)
				req.Messages = append(req.Messages, providers.Message{
					Role:     providers.RoleAssistant,
					Content:  strings.TrimSpace(resp.Content),
					Metadata: resp.ProviderMetadata,
				})
				req.Messages = append(req.Messages, providers.Message{
					Role: providers.RoleUser,
					Content: err.Error() +
						"\nAnswer the active global-review challenge by calling `validate_work` with your concrete findings. Do not end this turn until the validate_work call succeeds.",
				})
				requiredActionGraceTurns = shared.ExtendRequiredProtocolGrace(ctx, requiredActionGraceTurns)
				continue
			}
			o.recordTurn(ctx, req, resp, turn, 0, 0, turnStart)
			return strings.TrimSpace(resp.Content), nil
		}

		if err := o.toolRuntime().ValidateBatch(o.toolInvocations(ctx, resp.ToolCalls)); err != nil {
			return "", err
		}

		if dup, sig := shared.DetectToolCallDuplicate(resp.ToolCalls, seen, req.Messages); dup {
			return "", fmt.Errorf("orchestrator repeated tool call: %s", sig.Name)
		}

		errCount, rerouted, delegated, delegatedMessage := o.applyToolCalls(ctx, req, resp)
		o.recordTurn(ctx, req, resp, turn, len(resp.ToolCalls), errCount, turnStart)
		if rerouted {
			return "", skills.ErrRerouteRequested
		}
		if delegated {
			return strings.TrimSpace(delegatedMessage), nil
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
) (int, bool, bool, string) {
	req.Messages = append(req.Messages, providers.ToolLoopAssistantMessage(resp))

	errCount := 0
	rerouted := false
	delegated := false
	delegatedMessage := ""
	for _, call := range resp.ToolCalls {
		o.publishActivity(events.EventTypeToolResult, call.Name)
		var execResult toolruntime.ExecutionResult
		var execErr error
		execCtx := shared.WithActiveToolCall(ctx, call)
		result, err := shared.TimedToolCall(execCtx, "orchestrator", call, func() (string, error) {
			execResult, execErr = o.executeToolCall(execCtx, call)
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
			} else if errors.Is(err, skills.ErrDelegatedRequested) {
				delegated = true
				delegatedMessage = shared.DelegatedToolMessage(result, err)
			} else {
				result = shared.ToolErrorPayload(err)
				isError = true
				if shared.ToolErrorCountsTowardAbort(err) {
					errCount++
				}
			}
		}
		if gov := shared.ContextGovernorFromContext(ctx); gov != nil && !isError {
			result = gov.LimitToolOutput(ctx, result, call.Name)
		}
		// Activity Fabric ambient_context envelope.
		result = fabric.AppendAmbientContext(ctx, fabric.AmbientEnvelopeConfig{
			SessionID:  func() string { return o.SessionID() },
			AgentID:    func() string { return o.config.AgentID },
			AgentType:  func() string { return "orchestrator" },
			PipelineID: func() string { return "" },
		}, result)
		req.Messages = append(req.Messages, providers.Message{
			Role:       providers.RoleTool,
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Content:    result,
			IsError:    isError,
		})
		if rerouted || delegated {
			break
		}
	}
	return errCount, rerouted, delegated, delegatedMessage
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
	ctx context.Context,
	req *providers.Request,
	resp *providers.Response,
	turn, toolCalls, errCount int,
	turnStart time.Time,
) {
	if o.handoffBridge == nil {
		return
	}

	o.handoffBridge.RecordTurn(shared.BuildHandoffTurnRecord(ctx, req, resp, turn, toolCalls, errCount, turnStart))
}
