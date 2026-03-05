package archivalist

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/steering"
)

const defaultMaxToolRuns = 32

// executeToolLoop runs the LLM tool-call loop with steering checkpoints.
// Complete → check ToolCalls → execute → append results → repeat.
func (a *Archivalist) executeToolLoop(ctx context.Context, req *providers.Request, ledger *steering.SteeringLedger) (string, error) {
	maxRuns := defaultMaxToolRuns
	seen := make(map[shared.ToolCallSignature]int, maxRuns)
	consecutiveErrors := 0

	p := a.getProvider()
	if p == nil {
		return "", fmt.Errorf("archivalist: no LLM provider configured")
	}

	for turn := range maxRuns + 1 {
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

		// ── CONTEXT BUDGET ──
		if err := shared.ApplyContextBudget(ctx, turn, maxRuns, req); err != nil {
			return "", err
		}

		turnStart := time.Now()
		resp, err := p.Complete(ctx, req)
		shared.LogLLMCallFromContext(ctx, req.Model, resp, time.Since(turnStart), err)
		if err != nil {
			return "", fmt.Errorf("archivalist llm: %w", err)
		}

		if len(resp.ToolCalls) == 0 {
			return strings.TrimSpace(resp.Content), nil
		}

		if dup, sig := shared.DetectToolCallDuplicate(resp.ToolCalls, seen); dup {
			return "", fmt.Errorf("archivalist repeated tool call: %s", sig.Name)
		}

		errCount, rerouted := a.applyToolCalls(ctx, req, resp)
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

// applyToolCalls appends the assistant message and tool results to the request.
func (a *Archivalist) applyToolCalls(
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
		if ctx.Err() != nil {
			break
		}

		result, err := shared.TimedToolCall(ctx, "archivalist", call, func() (string, error) {
			return a.HandleToolCall(ctx, call.Name, []byte(call.Arguments))
		})

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

		req.Messages = append(req.Messages, providers.Message{
			Role:       providers.RoleTool,
			Content:    result,
			ToolCallID: call.ID,
		})
	}

	return errCount, rerouted
}

// getProvider returns the current LLM provider, guarded by runMu.
func (a *Archivalist) getProvider() archivalistProvider {
	a.runMu.RLock()
	defer a.runMu.RUnlock()
	return a.provider
}
