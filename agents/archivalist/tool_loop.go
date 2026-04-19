package archivalist

import (
	"context"
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

const defaultMaxToolRuns = 32

// executeToolLoop runs the LLM tool-call loop with steering checkpoints.
// Complete → check ToolCalls → execute → append results → repeat.
func (a *Archivalist) executeToolLoop(ctx context.Context, req *providers.Request, ledger *steering.SteeringLedger) (string, error) {
	return a.executeToolLoopWithBundle(ctx, req, ledger, nil)
}

func (a *Archivalist) executeToolLoopWithBundle(ctx context.Context, req *providers.Request, ledger *steering.SteeringLedger, bundle *archivalistToolBundle) (string, error) {
	maxRuns := defaultMaxToolRuns
	consecutiveErrors := 0

	p := a.getProvider()
	if p == nil {
		return "", fmt.Errorf("archivalist: %w: LLM provider not yet wired", shared.ErrAgentNotReady)
	}

	for turn := range maxRuns + 1 {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		sc := shared.DrainAndCheckpoint(ledger, req, turn, "executing", nil)
		if sc.Rollback != nil || sc.EditReplay != nil {
			cp := sc.Rollback
			if cp == nil {
				cp = sc.EditReplay
			}
			req.Messages = req.Messages[:cp.MessageCount]
			if sc.EditReplay != nil {
				req.Messages = append(req.Messages, providers.Message{
					Role: providers.RoleUser, Content: sc.EditText,
				})
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

		if bundle != nil {
			if bundle.toolDefsDirty {
				req.Tools = a.buildToolDefinitionsWithBundle(bundle)
				bundle.toolDefsDirty = false
			}
		} else if a.toolDefsDirty {
			req.Tools = a.buildToolDefinitions()
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
			return "", fmt.Errorf("archivalist llm: %w", err)
		}

		if gov := shared.ContextGovernorFromContext(ctx); gov != nil {
			gov.Calibrate(ctx, resp, req.Messages)
		}
		shared.AccumulateUsage(ctx, &resp.Usage)
		if !streamed {
			shared.PublishIntermediateToolTurn(a.bus, a.channels, ctx, a.id, resp)
		}

		toolCalls := len(resp.ToolCalls)
		bridge := shared.EffectiveHandoffBridge(ctx, a.handoffBridge)
		if toolCalls == 0 && bridge != nil {
			bridge.RecordTurn(shared.BuildHandoffTurnRecord(ctx, req, resp, turn, 0, 0, turnStart))
		}

		if toolCalls == 0 {
			return strings.TrimSpace(resp.Content), nil
		}

		if err := a.toolRuntimeForBundle(bundle).ValidateBatch(a.toolInvocationsWithBundle(ctx, resp.ToolCalls, bundle)); err != nil {
			return "", err
		}

		errCount, rerouted := a.applyToolCalls(ctx, req, resp, bundle)
		if bridge != nil {
			bridge.RecordTurn(shared.BuildHandoffTurnRecord(ctx, req, resp, turn, toolCalls, errCount, turnStart))
		}
		if rerouted {
			return "", skills.ErrRerouteRequested
		}

		consecutiveErrors = shared.UpdateToolErrors(consecutiveErrors, errCount, len(resp.ToolCalls))
		if consecutiveErrors >= shared.MaxConsecutiveToolErrors {
			return "", fmt.Errorf("archivalist tool calls failed %d consecutive turns", consecutiveErrors)
		}
	}

	return "", fmt.Errorf("archivalist exhausted tool-call loop")
}

type archivalistStreamingProvider interface {
	archivalistProvider
	Stream(ctx context.Context, req *providers.Request) (<-chan *providers.StreamChunk, error)
}

func (a *Archivalist) completeLLMTurn(
	ctx context.Context,
	p archivalistProvider,
	req *providers.Request,
) (*providers.Response, bool, error) {
	if pp := shared.ProgressPublisherFromContext(ctx); pp != nil {
		llmruntime.PromoteForUserFacingTurn(req, pp.SourceAgentID, llmruntime.ThoughtVisibilitySummary)
	}
	if sp, ok := p.(archivalistStreamingProvider); ok {
		resp, err := a.streamLLMTurn(ctx, sp, req)
		return resp, true, err
	}
	resp, err := shared.CompleteWithWatchdog(ctx, p, req, shared.AgentDisplayName("archivalist"))
	return resp, false, err
}

func (a *Archivalist) streamLLMTurn(
	ctx context.Context,
	p archivalistStreamingProvider,
	req *providers.Request,
) (*providers.Response, error) {
	chunks, err := p.Stream(ctx, req)
	if err != nil {
		return nil, err
	}

	cancelWatchdog := shared.StartThinkingWatchdog(ctx, req, shared.AgentDisplayName("archivalist"))
	defer cancelWatchdog()

	var (
		streamErr         error
		firstVisibleChunk bool
		streamedText      bool
	)
	pp := shared.ProgressPublisherFromContext(ctx)
	emitter := shared.NewThoughtEmitter(llmruntime.EmitsThoughts(req))

	collector := providers.NewStreamCollector(func(chunk *providers.StreamChunk) {
		if chunk == nil {
			return
		}
		shared.ObserveProviderToolCallChunk(ctx, chunk)
		switch chunk.Type {
		case providers.ChunkTypeText:
			if chunk.Text == "" {
				return
			}
			if !firstVisibleChunk {
				cancelWatchdog()
				firstVisibleChunk = true
			}
			streamedText = true
			shared.PublishStreamChunk(a.bus, a.channels, ctx, a.id, chunk.Text)
		case providers.ChunkTypeThought:
			if chunk.Text == "" {
				return
			}
			if !firstVisibleChunk {
				cancelWatchdog()
				firstVisibleChunk = true
			}
			if pp != nil {
				if thought := emitter.AddDelta(chunk.Text); thought != "" {
					pp.Publish(thought)
					shared.LogThinkingChunk(ctx, thought)
				}
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
	if pp != nil {
		if thought := emitter.Flush(); thought != "" {
			pp.Publish(thought)
			shared.LogThinkingChunk(ctx, thought)
		}
	}
	if streamErr != nil {
		return collector.Response(), streamErr
	}
	resp := collector.Response()
	if pp != nil && resp != nil && streamedText {
		shared.MarkResponseStreamedText(resp)
	}
	return resp, nil
}

// applyToolCalls appends the assistant message and tool results to the request.
func (a *Archivalist) applyToolCalls(
	ctx context.Context,
	req *providers.Request,
	resp *providers.Response,
	bundle *archivalistToolBundle,
) (int, bool) {
	req.Messages = append(req.Messages, providers.ToolLoopAssistantMessage(resp))

	loadedBefore := len(a.loadedSkills(bundle))

	errCount := 0
	rerouted := false
	for _, call := range resp.ToolCalls {
		if ctx.Err() != nil {
			break
		}

		var execResult toolruntime.ExecutionResult
		var execErr error
		execCtx := shared.WithActiveToolCall(ctx, call)
		result, err := shared.TimedToolCall(execCtx, "archivalist", call, func() (string, error) {
			execResult, execErr = a.executeToolCall(execCtx, call, bundle)
			return execResult.Output, execErr
		})
		if execResult.ToolDefsDirty {
			a.markToolDefsDirty(bundle)
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
		// Activity Fabric ambient_context envelope.
		result = shared.AppendAmbientContext(ctx, shared.AmbientEnvelopeConfig{
			SessionID:  func() string { return a.defaultSessionID },
			AgentID:    func() string { return a.id },
			AgentType:  func() string { return "archivalist" },
			PipelineID: func() string { return "" },
		}, result)
		req.Messages = append(req.Messages, providers.Message{
			Role:       providers.RoleTool,
			Content:    result,
			ToolCallID: call.ID,
		})
	}

	if len(a.loadedSkills(bundle)) > loadedBefore {
		a.markToolDefsDirty(bundle)
	}

	return errCount, rerouted
}

func (a *Archivalist) executeToolCall(ctx context.Context, call providers.ToolCall, bundle *archivalistToolBundle) (toolruntime.ExecutionResult, error) {
	if bundle != nil {
		return bundle.executeToolCall(ctx, a.id, call)
	}
	name := strings.TrimSpace(call.Name)
	if name == "" {
		return toolruntime.ExecutionResult{}, fmt.Errorf("tool name is required")
	}
	raw := strings.TrimSpace(call.Arguments)
	if raw == "" {
		raw = "{}"
	}
	correlationID := shared.LogMetaFromContext(ctx).CorrID
	if correlationID == "" {
		correlationID = a.id + "-local"
	}
	return a.toolRuntime().Execute(ctx, toolruntime.Invocation{
		ToolCall: providers.ToolCall{
			ID:        call.ID,
			Name:      name,
			Arguments: raw,
		},
		AgentID:         a.toolRuntime().AgentID(),
		CorrelationID:   correlationID,
		CapabilityScope: a.toolRuntime().CapabilityScope(),
	})
}

// prepareSkillsForInput progressively loads skills relevant to the user's
// input and optimizes to stay within the tool-definition token budget.
func (a *Archivalist) prepareSkillsForInput(input string) {
	if a.skillLoader == nil {
		return
	}
	a.skillLoader.LoadForInput(input)
	a.skillLoader.OptimizeForBudget()
}

// buildToolDefinitions converts loaded skills to provider Tool format.
func (a *Archivalist) buildToolDefinitions() []providers.Tool {
	return a.buildToolDefinitionsWithBundle(nil)
}

func (a *Archivalist) buildToolDefinitionsWithBundle(bundle *archivalistToolBundle) []providers.Tool {
	if bundle != nil {
		return bundle.buildToolDefinitions()
	}
	a.toolRuntime().SyncActiveFromLoaded()
	return a.toolRuntime().BuildToolDefinitions()
}

func (a *Archivalist) toolRuntime() *toolruntime.Runtime {
	return a.tools
}

func (a *Archivalist) toolInvocations(ctx context.Context, calls []providers.ToolCall) []toolruntime.Invocation {
	return a.toolInvocationsWithBundle(ctx, calls, nil)
}

func (a *Archivalist) toolInvocationsWithBundle(ctx context.Context, calls []providers.ToolCall, bundle *archivalistToolBundle) []toolruntime.Invocation {
	if bundle != nil {
		return bundle.toolInvocations(ctx, a.id, calls)
	}
	if len(calls) == 0 {
		return nil
	}
	correlationID := shared.LogMetaFromContext(ctx).CorrID
	if correlationID == "" {
		correlationID = a.id + "-local"
	}
	scope := a.toolRuntime().CapabilityScope()
	invocations := make([]toolruntime.Invocation, 0, len(calls))
	for _, call := range calls {
		invocations = append(invocations, toolruntime.Invocation{
			ToolCall:        call,
			AgentID:         a.toolRuntime().AgentID(),
			CorrelationID:   correlationID,
			CapabilityScope: scope,
		})
	}
	return invocations
}

func (a *Archivalist) toolRuntimeForBundle(bundle *archivalistToolBundle) toolruntime.Surface {
	if bundle != nil && bundle.runtime != nil {
		return bundle.runtime
	}
	return a.toolRuntime()
}

func (a *Archivalist) loadedSkills(bundle *archivalistToolBundle) []*skills.Skill {
	if bundle != nil && bundle.skills != nil {
		return bundle.skills.GetLoaded()
	}
	if a.skills == nil {
		return nil
	}
	return a.skills.GetLoaded()
}

func (a *Archivalist) markToolDefsDirty(bundle *archivalistToolBundle) {
	if bundle != nil {
		bundle.toolDefsDirty = true
		return
	}
	a.toolDefsDirty = true
}

// getProvider returns the current LLM provider, guarded by runMu.
func (a *Archivalist) getProvider() archivalistProvider {
	a.runMu.RLock()
	defer a.runMu.RUnlock()
	return a.provider
}
