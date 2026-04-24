package scribe

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/providers"
)

const scribeMaxTokens = 512

func (s *Scribe) generateCommentary(ctx context.Context, history []providers.Message, feed shared.ScribeFeed) (string, error) {
	if s.provider == nil {
		return "", fmt.Errorf("scribe provider is not configured")
	}
	bundle, state, err := s.newToolBundle(feed)
	if err != nil {
		return "", err
	}
	defer bundle.Close()

	if input := s.scribeInput(history); input != "" {
		bundle.prepareSkillsForInput(input)
	}

	systemPrompt := scribeSystemPrompt(s.parentAgentType, s.config.Forest != nil)
	board := claims.DefaultSessionBoardRegistry().Lookup(s.sessionID)
	systemPrompt = claims.PrependBoardPreamble(systemPrompt, board, "scribe")

	req := &providers.Request{
		Model:                  s.model,
		MaxTokens:              scribeMaxTokens,
		SystemPrompt:           systemPrompt,
		Messages:               append([]providers.Message(nil), history...),
		ToolChoice:             "any",
		DisableParallelToolUse: true,
	}
	s.applyCommentaryRuntimeProfile(req)
	req.Tools = bundle.buildToolDefinitions()

	return s.executeToolLoop(ctx, req, bundle, state)
}

func (s *Scribe) executeToolLoop(
	ctx context.Context,
	req *providers.Request,
	bundle *scribeToolBundle,
	state *scribeRunState,
) (string, error) {
	if bundle == nil || bundle.runtime == nil {
		return "", fmt.Errorf("scribe tool runtime is not configured")
	}

	seen := make(map[shared.ToolCallSignature]int, s.maxToolRuns)
	consecutiveErrors := 0

	for turn := 0; turn <= s.maxToolRuns; turn++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		req.Tools = bundle.buildToolDefinitions()
		turnStart := time.Now()
		resp, err := shared.CompleteWithWatchdog(ctx, s.provider, req, shared.AgentDisplayName("scribe"))
		if err != nil {
			return "", fmt.Errorf("scribe llm: %w", err)
		}
		shared.AccumulateUsage(ctx, &resp.Usage)

		if len(resp.ToolCalls) == 0 {
			s.recordTurn(ctx, req, resp, turn, 0, 0, turnStart)
			if state != nil && state.stored && strings.TrimSpace(state.commentary) != "" {
				return strings.TrimSpace(state.commentary), nil
			}
			req.Messages = append(req.Messages, providers.Message{
				Role:     providers.RoleAssistant,
				Content:  strings.TrimSpace(resp.Content),
				Metadata: resp.ProviderMetadata,
			})
			req.Messages = append(req.Messages, providers.Message{
				Role:    providers.RoleUser,
				Content: "Use tools for Scribe work. Optionally use the forest skills for capture or handoff context, then call `store_archivalist` exactly once with the final structured commentary object.",
			})
			continue
		}

		if err := bundle.runtime.ValidateBatch(bundle.toolInvocations(ctx, s.id, resp.ToolCalls)); err != nil {
			return "", err
		}
		if dup, sig := shared.DetectToolCallDuplicate(resp.ToolCalls, seen, req.Messages); dup {
			return "", fmt.Errorf("scribe repeated tool call: %s", sig.Name)
		}

		errCount, err := s.applyToolCalls(ctx, req, resp, bundle, state)
		s.recordTurn(ctx, req, resp, turn, len(resp.ToolCalls), errCount, turnStart)
		if err != nil {
			return "", err
		}
		if state != nil && state.stored && strings.TrimSpace(state.commentary) != "" {
			return strings.TrimSpace(state.commentary), nil
		}

		consecutiveErrors = shared.UpdateToolErrors(consecutiveErrors, errCount, len(resp.ToolCalls))
		if consecutiveErrors >= shared.MaxConsecutiveToolErrors {
			return "", fmt.Errorf("scribe tool calls failed %d consecutive turns", consecutiveErrors)
		}
	}

	if state != nil && state.stored && strings.TrimSpace(state.commentary) != "" {
		return strings.TrimSpace(state.commentary), nil
	}
	return "", fmt.Errorf("scribe exhausted tool-call loop without storing commentary")
}

func (s *Scribe) applyToolCalls(
	ctx context.Context,
	req *providers.Request,
	resp *providers.Response,
	bundle *scribeToolBundle,
	state *scribeRunState,
) (int, error) {
	req.Messages = append(req.Messages, providers.ToolLoopAssistantMessage(resp))

	errCount := 0
	for _, call := range resp.ToolCalls {
		if ctx.Err() != nil {
			return errCount, ctx.Err()
		}

		var execResultOutput string
		execCtx := shared.WithActiveToolCall(ctx, call)
		result, err := shared.TimedToolCall(execCtx, "scribe", call, func() (string, error) {
			execResult, execErr := bundle.executeToolCall(execCtx, s.id, call)
			execResultOutput = execResult.Output
			return execResult.Output, execErr
		})
		isError := false
		if err != nil {
			result = shared.ToolErrorPayload(err)
			isError = true
			if shared.ToolErrorCountsTowardAbort(err) {
				errCount++
			}
		}

		if gov := shared.ContextGovernorFromContext(ctx); gov != nil && !isError {
			if output := strings.TrimSpace(execResultOutput); output != "" {
				result = gov.LimitToolOutput(ctx, output, call.Name)
			} else {
				result = gov.LimitToolOutput(ctx, result, call.Name)
			}
		}

		req.Messages = append(req.Messages, providers.Message{
			Role:       providers.RoleTool,
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Content:    result,
			IsError:    isError,
		})
		if state != nil && state.stored {
			break
		}
	}

	return errCount, nil
}

func (s *Scribe) scribeInput(history []providers.Message) string {
	for idx := len(history) - 1; idx >= 0; idx-- {
		if content := strings.TrimSpace(history[idx].Content); content != "" {
			return content
		}
	}
	return ""
}

func (s *Scribe) recordTurn(
	ctx context.Context,
	req *providers.Request,
	resp *providers.Response,
	turn, toolCalls, errCount int,
	turnStart time.Time,
) {
	bridge := shared.EffectiveHandoffBridge(ctx, s.handoffBridge)
	if bridge == nil {
		return
	}
	bridge.RecordTurn(shared.BuildHandoffTurnRecord(ctx, req, resp, turn, toolCalls, errCount, turnStart))
}
