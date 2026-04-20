package librarian

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/fabric"
	"github.com/adalundhe/sylk/core/agentlog"
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
	return l.executeToolLoopWithBundle(ctx, req, ledger, nil)
}

func (l *Librarian) executeToolLoopWithBundle(ctx context.Context, req *providers.Request, ledger *steering.SteeringLedger, bundle *librarianToolBundle) (string, error) {
	loop := newToolLoopState(l, ctx, req, ledger, bundle)
	provider, err := loop.provider()
	if err != nil {
		return "", err
	}
	loop.logStart()

	for turn := 0; turn <= loop.maxRuns; turn++ {
		nextTurn, shouldContinue, err := loop.prepareTurn(turn)
		if err != nil {
			return "", err
		}
		if shouldContinue {
			turn = nextTurn
			continue
		}

		resp, turnStart, err := loop.completeTurn(provider)
		if err != nil {
			return "", err
		}

		result, done, err := loop.finishTurn(resp, turn, turnStart)
		if done || err != nil {
			return result, err
		}
	}

	return "", loop.exhaustedError()
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
	bundle *librarianToolBundle,
) (int, bool) {
	req.Messages = append(req.Messages, providers.ToolLoopAssistantMessage(resp))

	loadedBefore := len(l.loadedSkills(bundle))

	errCount := 0
	rerouted := false
	for _, call := range resp.ToolCalls {
		if ctx.Err() != nil {
			break
		}
		var execResult toolruntime.ExecutionResult
		var execErr error
		execCtx := shared.WithActiveToolCall(ctx, call)
		result, err := shared.TimedToolCall(execCtx, "librarian", call, func() (string, error) {
			execResult, execErr = l.executeToolCallWithBundle(execCtx, call, bundle)
			return execResult.Output, execErr
		})
		if execResult.ToolDefsDirty {
			l.markToolDefsDirty(bundle)
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

		// Record successful search results into the ledger.
		if !isError {
			searchLedger.Record(turn, call.Name, call.Arguments, result)
		}

		// Activity Fabric ambient_context envelope.
		result = fabric.AppendAmbientContext(ctx, fabric.AmbientEnvelopeConfig{
			SessionID:  func() string { return l.config.SessionID },
			AgentID:    func() string { return l.id },
			AgentType:  func() string { return "librarian" },
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

	if len(l.loadedSkills(bundle)) > loadedBefore {
		l.markToolDefsDirty(bundle)
	}

	return errCount, rerouted
}

// executeToolCall invokes a skill by name with JSON arguments.
func (l *Librarian) executeToolCall(ctx context.Context, call providers.ToolCall) (toolruntime.ExecutionResult, error) {
	return l.executeToolCallWithBundle(ctx, call, nil)
}

func (l *Librarian) executeToolCallWithBundle(ctx context.Context, call providers.ToolCall, bundle *librarianToolBundle) (toolruntime.ExecutionResult, error) {
	if bundle != nil {
		return bundle.executeToolCall(ctx, l.id, call)
	}
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
		AgentID:         l.toolRuntime().AgentID(),
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
	return l.buildToolDefinitionsWithBundle(nil)
}

func (l *Librarian) buildToolDefinitionsWithBundle(bundle *librarianToolBundle) []providers.Tool {
	if bundle != nil {
		return bundle.buildToolDefinitions()
	}
	l.toolRuntime().SyncActiveFromLoaded()
	return l.toolRuntime().BuildToolDefinitions()
}

func (l *Librarian) toolRuntime() *toolruntime.Runtime {
	return l.tools
}

func (l *Librarian) toolInvocations(ctx context.Context, calls []providers.ToolCall) []toolruntime.Invocation {
	return l.toolInvocationsWithBundle(ctx, calls, nil)
}

func (l *Librarian) toolInvocationsWithBundle(ctx context.Context, calls []providers.ToolCall, bundle *librarianToolBundle) []toolruntime.Invocation {
	if bundle != nil {
		return bundle.toolInvocations(ctx, l.id, calls)
	}
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
			AgentID:         l.toolRuntime().AgentID(),
			CorrelationID:   correlationID,
			CapabilityScope: scope,
		})
	}
	return invocations
}

func (l *Librarian) loadedSkills(bundle *librarianToolBundle) []*skills.Skill {
	if bundle != nil && bundle.skills != nil {
		return bundle.skills.GetLoaded()
	}
	if l.skills == nil {
		return nil
	}
	return l.skills.GetLoaded()
}

func (l *Librarian) markToolDefsDirty(bundle *librarianToolBundle) {
	if bundle != nil {
		bundle.toolDefsDirty = true
		return
	}
	l.toolDefsDirty = true
}

// recordTurn feeds the handoff bridge with turn metrics from this LLM call.
func (l *Librarian) recordTurn(
	ctx context.Context,
	req *providers.Request,
	resp *providers.Response,
	turn, toolCalls, errCount int,
	turnStart time.Time,
) {
	bridge := shared.EffectiveHandoffBridge(ctx, l.handoffBridge)
	if bridge == nil {
		return
	}

	bridge.RecordTurn(shared.BuildHandoffTurnRecord(ctx, req, resp, turn, toolCalls, errCount, turnStart))
}
