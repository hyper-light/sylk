package librarian

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/steering"
)

type toolLoopState struct {
	librarian *Librarian
	ctx       context.Context
	req       *providers.Request
	ledger    *steering.SteeringLedger

	maxRuns           int
	seen              map[shared.ToolCallSignature]int
	consecutiveErrors int
	searchLedger      *SearchLedger
}

func newToolLoopState(
	l *Librarian,
	ctx context.Context,
	req *providers.Request,
	ledger *steering.SteeringLedger,
) *toolLoopState {
	maxRuns := l.config.MaxToolRuns
	return &toolLoopState{
		librarian:    l,
		ctx:          ctx,
		req:          req,
		ledger:       ledger,
		maxRuns:      maxRuns,
		seen:         make(map[shared.ToolCallSignature]int, maxRuns),
		searchLedger: NewSearchLedger(),
	}
}

func (s *toolLoopState) provider() (LibrarianProvider, error) {
	provider := s.librarian.getProvider()
	if provider != nil {
		return provider, nil
	}
	if lm := shared.LogMetaFromContext(s.ctx); lm.EventLogger != nil {
		shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
			lm.AgentID, lm.SessionID, lm.CorrID, "error",
			&agentlog.ErrorPayload{Error: "no LLM provider configured"})
	}
	return nil, fmt.Errorf("librarian: no LLM provider configured")
}

func (s *toolLoopState) logStart() {
	if lm := shared.LogMetaFromContext(s.ctx); lm.EventLogger != nil {
		shared.LogAgentEvent(lm.EventLogger, agentlog.EventGenerationStarted,
			lm.AgentID, lm.SessionID, lm.CorrID, "info",
			&agentlog.GenerationPayload{Phase: "started"})
	}
}

func (s *toolLoopState) prepareTurn(turn int) (int, bool, error) {
	if err := s.ctx.Err(); err != nil {
		return turn, false, err
	}

	sc := shared.DrainAndCheckpoint(s.ledger, s.req, turn, "searching", nil)
	if sc.Rollback != nil || sc.EditReplay != nil {
		return s.applyCheckpoint(sc), true, nil
	}
	if sc.ShouldPause {
		if err := s.ledger.WaitForResume(s.ctx); err != nil {
			return turn, false, err
		}
		return turn, true, nil
	}

	if s.librarian.toolDefsDirty {
		s.req.Tools = s.librarian.buildToolDefinitions()
		s.librarian.toolDefsDirty = false
	}
	if s.searchLedger.IsSaturated() {
		s.req.Tools = nil
		if lm := shared.LogMetaFromContext(s.ctx); lm.EventLogger != nil {
			shared.LogAgentEvent(lm.EventLogger, agentlog.EventGenerationCompleted,
				lm.AgentID, lm.SessionID, lm.CorrID, "info",
				&agentlog.GenerationPayload{Phase: "search_saturated", ToolRuns: turn})
		}
	}
	if summary := s.searchLedger.EvidenceSummary(); summary != "" {
		injectEvidenceSummary(s.req, summary)
	}
	if err := shared.ApplyContextBudget(s.ctx, turn, s.maxRuns, s.req); err != nil {
		return turn, false, err
	}
	return turn, false, nil
}

func (s *toolLoopState) applyCheckpoint(sc shared.SteeringResult) int {
	cp := sc.Rollback
	action := "rollback"
	if cp == nil {
		cp = sc.EditReplay
		action = "edit_replay"
	}
	if lm := shared.LogMetaFromContext(s.ctx); lm.EventLogger != nil {
		shared.LogAgentEvent(lm.EventLogger, agentlog.EventSteeringCheckpoint,
			lm.AgentID, lm.SessionID, lm.CorrID, "info",
			&agentlog.ErrorPayload{Error: fmt.Sprintf("%s at turn %d", action, cp.Turn)})
	}
	s.req.Messages = s.req.Messages[:cp.MessageCount]
	if sc.EditReplay != nil {
		s.req.Messages = append(s.req.Messages, providers.Message{
			Role:    providers.RoleUser,
			Content: sc.EditText,
		})
	}
	s.seen = make(map[shared.ToolCallSignature]int, s.maxRuns)
	s.consecutiveErrors = 0
	return cp.Turn
}

func (s *toolLoopState) completeTurn(provider LibrarianProvider) (*providers.Response, time.Time, error) {
	turnStart := time.Now()
	resp, err := shared.CompleteWithWatchdog(s.ctx, provider, s.req, shared.AgentDisplayName("librarian"))
	if err != nil {
		if ctxErr := s.ctx.Err(); ctxErr != nil {
			return nil, time.Time{}, ctxErr
		}
		if lm := shared.LogMetaFromContext(s.ctx); lm.EventLogger != nil {
			shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
				lm.AgentID, lm.SessionID, lm.CorrID, "error",
				&agentlog.ErrorPayload{Error: fmt.Sprintf("llm: %v", err)})
		}
		return nil, time.Time{}, fmt.Errorf("librarian llm: %w", err)
	}

	if gov := shared.ContextGovernorFromContext(s.ctx); gov != nil {
		gov.Calibrate(s.ctx, resp, s.req.Messages)
	}
	shared.AccumulateUsage(s.ctx, &resp.Usage)
	shared.PublishIntermediateToolTurn(s.librarian.bus, s.librarian.channels, s.ctx, s.librarian.id, resp)
	return resp, turnStart, nil
}

func (s *toolLoopState) finishTurn(resp *providers.Response, turn int, turnStart time.Time) (string, bool, error) {
	if len(resp.ToolCalls) == 0 {
		return s.handleTextResponse(resp, turn, turnStart)
	}
	if s.handleDuplicateToolCalls(resp) {
		return "", false, nil
	}
	if turn == s.maxRuns {
		return s.handleToolLimit(resp, turn, turnStart)
	}
	if err := s.librarian.toolRuntime().ValidateBatch(s.librarian.toolInvocations(s.ctx, resp.ToolCalls)); err != nil {
		return "", false, err
	}
	return s.executeToolCalls(resp, turn, turnStart)
}

func (s *toolLoopState) handleTextResponse(resp *providers.Response, turn int, turnStart time.Time) (string, bool, error) {
	content := strings.TrimSpace(resp.Content)
	if content == "" {
		if turn < s.maxRuns {
			s.req.Messages = append(s.req.Messages, providers.Message{
				Role:    providers.RoleAssistant,
				Content: resp.Content,
			}, providers.Message{
				Role:    providers.RoleUser,
				Content: "Your previous response was empty. You MUST provide a substantive natural language answer based on the tool results you have gathered so far. Summarize your findings clearly.",
			})
			return "", false, nil
		}
		return "", true, fmt.Errorf("librarian: LLM returned empty response after %d turns", turn)
	}

	s.librarian.recordTurn(s.ctx, s.req, resp, turn, 0, 0, turnStart)
	if lm := shared.LogMetaFromContext(s.ctx); lm.EventLogger != nil {
		shared.LogAgentEvent(lm.EventLogger, agentlog.EventGenerationCompleted,
			lm.AgentID, lm.SessionID, lm.CorrID, "info",
			&agentlog.GenerationPayload{Phase: "completed", ToolRuns: turn})
	}
	return content, true, nil
}

func (s *toolLoopState) handleDuplicateToolCalls(resp *providers.Response) bool {
	dup, sig := shared.DetectToolCallDuplicate(resp.ToolCalls, s.seen, s.req.Messages)
	if !dup {
		return false
	}

	if lm := shared.LogMetaFromContext(s.ctx); lm.EventLogger != nil {
		shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
			lm.AgentID, lm.SessionID, lm.CorrID, "warn",
			&agentlog.ErrorPayload{Error: fmt.Sprintf("repeated tool call: %s (forcing synthesis)", sig.Name)})
	}
	s.req.Messages = append(s.req.Messages, providers.Message{
		Role:      providers.RoleAssistant,
		Content:   resp.Content,
		ToolCalls: resp.ToolCalls,
		Metadata:  resp.ProviderMetadata,
	})
	for _, call := range resp.ToolCalls {
		s.req.Messages = append(s.req.Messages, providers.Message{
			Role:       providers.RoleTool,
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Content:    fmt.Sprintf(`{"error":"Duplicate search — you already called %s with identical arguments. Use your existing results."}`, call.Name),
			IsError:    true,
		})
	}
	s.req.Tools = nil
	return true
}

func (s *toolLoopState) handleToolLimit(resp *providers.Response, turn int, turnStart time.Time) (string, bool, error) {
	if content := strings.TrimSpace(resp.Content); content != "" {
		if lm := shared.LogMetaFromContext(s.ctx); lm.EventLogger != nil {
			shared.LogAgentEvent(lm.EventLogger, agentlog.EventGenerationCompleted,
				lm.AgentID, lm.SessionID, lm.CorrID, "warn",
				&agentlog.GenerationPayload{Phase: "completed_at_limit", ToolRuns: turn})
		}
		s.librarian.recordTurn(s.ctx, s.req, resp, turn, 0, 0, turnStart)
		return content, true, nil
	}

	if lm := shared.LogMetaFromContext(s.ctx); lm.EventLogger != nil {
		shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
			lm.AgentID, lm.SessionID, lm.CorrID, "error",
			&agentlog.ErrorPayload{Error: fmt.Sprintf("exceeded tool-call limit (%d)", s.maxRuns)})
	}
	return "", true, fmt.Errorf("librarian exceeded tool-call limit (%d)", s.maxRuns)
}

func (s *toolLoopState) executeToolCalls(resp *providers.Response, turn int, turnStart time.Time) (string, bool, error) {
	errCount, rerouted := s.librarian.applyToolCalls(s.ctx, s.req, resp, turn, s.searchLedger)
	s.librarian.recordTurn(s.ctx, s.req, resp, turn, len(resp.ToolCalls), errCount, turnStart)
	if rerouted {
		return "", true, skills.ErrRerouteRequested
	}

	s.consecutiveErrors = shared.UpdateToolErrors(s.consecutiveErrors, errCount, len(resp.ToolCalls))
	if s.consecutiveErrors < shared.MaxConsecutiveToolErrors {
		return "", false, nil
	}

	if lm := shared.LogMetaFromContext(s.ctx); lm.EventLogger != nil {
		shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
			lm.AgentID, lm.SessionID, lm.CorrID, "error",
			&agentlog.ErrorPayload{Error: fmt.Sprintf("tool calls failed %d consecutive turns", s.consecutiveErrors)})
	}
	return "", true, fmt.Errorf("librarian tool calls failed %d consecutive turns", s.consecutiveErrors)
}

func (s *toolLoopState) exhaustedError() error {
	if lm := shared.LogMetaFromContext(s.ctx); lm.EventLogger != nil {
		shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
			lm.AgentID, lm.SessionID, lm.CorrID, "error",
			&agentlog.ErrorPayload{Error: "exhausted tool-call loop"})
	}
	return fmt.Errorf("librarian exhausted tool-call loop")
}
