package guardian

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
)

// executeToolLoop runs the LLM tool-call loop: stream → check ToolCalls →
// execute → append results → repeat, bounded by config.MaxToolRuns.
// Follows the Architect pattern.
func (g *Guardian) executeToolLoop(
	ctx context.Context,
	req *providers.Request,
	stage string,
	onChunk func(string),
) (string, error) {
	maxRuns := g.config.MaxToolRuns
	seen := make(map[shared.ToolCallSignature]int, maxRuns)
	consecutiveErrors := 0

	provider := g.getProvider()
	if provider == nil {
		return "", fmt.Errorf("guardian: no LLM provider configured")
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
			return "", ctx.Err()
		}
		if g.toolDefsDirty {
			req.Tools = g.buildToolDefinitions()
			g.toolDefsDirty = false
		}

		resp, err := provider.Complete(ctx, req)
		if err != nil {
			g.logWarn("executeToolLoop: LLM error", "stage", stage, "turn", turn, "err", err)
			return "", fmt.Errorf("guardian llm: %w", err)
		}

		// Emit text chunks.
		if onChunk != nil && resp.Content != "" {
			onChunk(resp.Content)
		}

		if len(resp.ToolCalls) == 0 {
			g.logDebug("tool_loop: COMPLETE",
				"stage", stage, "turn", turn,
				"content_len", len(resp.Content),
				"total_elapsed", time.Since(loopStart).String())
			return strings.TrimSpace(resp.Content), nil
		}

		if turn == maxRuns {
			g.logWarn("executeToolLoop: tool-call limit exceeded", "stage", stage, "max_runs", maxRuns)
			return "", fmt.Errorf("guardian exceeded tool-call limit (%d)", maxRuns)
		}

		if dup, sig := shared.DetectToolCallDuplicate(resp.ToolCalls, seen); dup {
			g.logWarn("executeToolLoop: duplicate tool call", "stage", stage, "tool", sig.Name)
			return "", fmt.Errorf("guardian repeated tool call: %s", sig.Name)
		}

		errCount, rerouted := g.applyToolCalls(ctx, req, resp)
		if rerouted {
			return "", skills.ErrRerouteRequested
		}
		consecutiveErrors = shared.UpdateToolErrors(consecutiveErrors, errCount, len(resp.ToolCalls))
		if consecutiveErrors >= shared.MaxConsecutiveToolErrors {
			return "", fmt.Errorf("guardian tool calls failed %d consecutive turns", consecutiveErrors)
		}
	}

	return "", fmt.Errorf("guardian exhausted tool-call loop")
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
			}
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
