package global

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/steering"
)

// executeToolLoop runs the model tool-call loop: Complete → check ToolCalls →
// execute → append results → repeat, bounded by config.MaxToolRuns.
// Follows the orchestrator tool_loop.go pattern exactly.
func (gt *GlobalTester) executeToolLoop(ctx context.Context, req *providers.Request, ledger *steering.SteeringLedger) (string, error) {
	seen := make(map[toolCallSignature]int, gt.config.MaxToolRuns)
	consecutiveErrors := 0

	provider := gt.getProvider()
	if provider == nil {
		if lm := agentshared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			agentshared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
				lm.AgentID, lm.SessionID, lm.CorrID, "error",
				&agentlog.ErrorPayload{Error: "no LLM provider configured"})
		}
		return "", fmt.Errorf("global tester: no LLM provider configured")
	}

	for turn := 0; turn <= gt.config.MaxToolRuns; turn++ {
		// ── STEERING CHECKPOINT ──
		sc := agentshared.DrainAndCheckpoint(ledger, req, turn, "testing", nil)
		if sc.Rollback != nil || sc.EditReplay != nil {
			cp := sc.Rollback
			if cp == nil {
				cp = sc.EditReplay
			}
			if lm := agentshared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				action := "rollback"
				if sc.EditReplay != nil {
					action = "edit_replay"
				}
				agentshared.LogAgentEvent(lm.EventLogger, agentlog.EventSteeringCheckpoint,
					lm.AgentID, lm.SessionID, lm.CorrID, "info",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("%s at turn %d", action, cp.Turn)})
			}
			req.Messages = req.Messages[:cp.MessageCount]
			if sc.EditReplay != nil {
				req.Messages = append(req.Messages, providers.Message{Role: providers.RoleUser, Content: sc.EditText})
			}
			turn = cp.Turn
			seen = make(map[toolCallSignature]int, gt.config.MaxToolRuns)
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

		turnStart := time.Now()
		resp, err := provider.Complete(ctx, req)
		agentshared.LogLLMCallFromContext(ctx, req.Model, resp, time.Since(turnStart), err)
		if err != nil {
			if lm := agentshared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				agentshared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "error",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("llm: %v", err)})
			}
			return "", fmt.Errorf("global tester llm: %w", err)
		}

		if len(resp.ToolCalls) == 0 {
			if lm := agentshared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				agentshared.LogAgentEvent(lm.EventLogger, agentlog.EventSuiteCompleted,
					lm.AgentID, lm.SessionID, lm.CorrID, "info",
					&agentlog.TestSuitePayload{Phase: "tool_loop_completed"})
			}
			return strings.TrimSpace(resp.Content), nil
		}

		if turn == gt.config.MaxToolRuns {
			if lm := agentshared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				agentshared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "error",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("exceeded tool-call limit (%d)", gt.config.MaxToolRuns)})
			}
			return "", fmt.Errorf("global tester exceeded tool-call limit (%d)", gt.config.MaxToolRuns)
		}

		if dup, sig := detectToolCallDuplicate(resp.ToolCalls, seen); dup {
			if lm := agentshared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				agentshared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "error",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("repeated tool call: %s", sig.Name)})
			}
			return "", fmt.Errorf("global tester repeated tool call: %s", sig.Name)
		}

		errCount, rerouted := gt.applyToolCalls(ctx, req, resp)
		if rerouted {
			return "", skills.ErrRerouteRequested
		}
		consecutiveErrors = updateToolErrors(consecutiveErrors, errCount, len(resp.ToolCalls))
		if consecutiveErrors >= 2 {
			if lm := agentshared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				agentshared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "error",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("tool calls failed %d consecutive turns", consecutiveErrors)})
			}
			return "", fmt.Errorf("global tester tool calls failed %d consecutive turns", consecutiveErrors)
		}
	}

	if lm := agentshared.LogMetaFromContext(ctx); lm.EventLogger != nil {
		agentshared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
			lm.AgentID, lm.SessionID, lm.CorrID, "error",
			&agentlog.ErrorPayload{Error: "exhausted tool-call loop"})
	}
	return "", fmt.Errorf("global tester exhausted tool-call loop")
}

// applyToolCalls appends the assistant message and tool results to the request.
func (gt *GlobalTester) applyToolCalls(
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
		result, err := agentshared.TimedToolCall(ctx, "tester", call, func() (string, error) {
			return gt.executeToolCall(ctx, call)
		})
		isError := false
		if err != nil {
			if errors.Is(err, skills.ErrRerouteRequested) {
				rerouted = true
				result = `{"rerouted": true}`
			} else {
				result = toolErrorPayload(err)
				isError = true
				errCount++
				if lm := agentshared.LogMetaFromContext(ctx); lm.EventLogger != nil {
					agentshared.LogAgentEvent(lm.EventLogger, agentlog.EventSkillFailed,
						lm.AgentID, lm.SessionID, lm.CorrID, "warn",
						&agentlog.ToolPayload{ToolName: call.Name, Success: false, Err: err.Error()})
				}
			}
		}
		if lm := agentshared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			agentshared.LogAgentEvent(lm.EventLogger, agentshared.ToolNameToEventType(call.Name),
				lm.AgentID, lm.SessionID, lm.CorrID, "info",
				&agentlog.ToolPayload{ToolName: call.Name, Success: !isError})
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
func (gt *GlobalTester) executeToolCall(ctx context.Context, call providers.ToolCall) (string, error) {
	name := strings.TrimSpace(call.Name)
	if name == "" {
		return "", fmt.Errorf("tool name is required")
	}

	raw := strings.TrimSpace(call.Arguments)
	if raw == "" {
		raw = "{}"
	}
	if !json.Valid([]byte(raw)) {
		return "", fmt.Errorf("tool arguments for %q are not valid JSON", name)
	}

	result := gt.skills.Invoke(ctx, name, json.RawMessage(raw))
	if result == nil {
		return "", fmt.Errorf("tool %q returned nil", name)
	}
	if !result.Success {
		return "", fmt.Errorf("tool %q failed: %s", name, strings.TrimSpace(result.Error))
	}

	return marshalToolOutput(result.Data)
}

// buildToolDefinitions converts loaded skills to provider tool format.
func (gt *GlobalTester) buildToolDefinitions() []providers.Tool {
	loaded := gt.skills.GetLoaded()
	if len(loaded) == 0 {
		return nil
	}

	tools := make([]providers.Tool, 0, len(loaded))
	for _, skill := range loaded {
		def := skill.ToToolDefinition()
		name, _ := def["name"].(string)
		if name == "" {
			continue
		}
		description, _ := def["description"].(string)
		parameters := coerceMap(def["input_schema"])
		if len(parameters) == 0 {
			parameters = map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
		}
		tools = append(tools, providers.Tool{
			Name:        name,
			Description: description,
			Parameters:  parameters,
		})
	}
	return tools
}

// --- Tool loop helpers (identical to orchestrator pattern) ---

func marshalToolOutput(data any) (string, error) {
	switch typed := data.(type) {
	case string:
		return typed, nil
	case fmt.Stringer:
		return typed.String(), nil
	default:
		payload, err := json.Marshal(data)
		if err != nil {
			return "", fmt.Errorf("marshal tool output: %w", err)
		}
		return string(payload), nil
	}
}

func toolErrorPayload(err error) string {
	if err == nil {
		return ""
	}
	payload, marshalErr := json.Marshal(map[string]any{
		"error": strings.TrimSpace(err.Error()),
	})
	if marshalErr != nil {
		return `{"error":"tool execution failed"}`
	}
	return string(payload)
}

func coerceMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	if mapped, ok := value.(map[string]any); ok {
		return mapped
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var mapped map[string]any
	if err := json.Unmarshal(payload, &mapped); err != nil {
		return nil
	}
	return mapped
}

type toolCallSignature struct {
	Name      string
	Arguments string
}

func detectToolCallDuplicate(calls []providers.ToolCall, seen map[toolCallSignature]int) (bool, toolCallSignature) {
	batch := make([]toolCallSignature, 0, len(calls))
	for _, call := range calls {
		sig := toolCallSignature{
			Name:      strings.TrimSpace(call.Name),
			Arguments: strings.TrimSpace(call.Arguments),
		}
		batch = append(batch, sig)
	}

	allDup := true
	var firstDup toolCallSignature
	for _, sig := range batch {
		seen[sig]++
		if seen[sig] <= 1 {
			allDup = false
		} else if firstDup.Name == "" {
			firstDup = sig
		}
	}
	return allDup, firstDup
}

func updateToolErrors(current, errCount, totalCalls int) int {
	if totalCalls > 0 && errCount == totalCalls {
		return current + 1
	}
	return 0
}
