package claims

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/skills"
)

func CarryForwardSkill(bp BoardProvider, defaultAgentID string) *skills.Skill {
	return skills.NewSkill("carry_forward").
		Description("Carry forward durable testaments and artifacts for the calling agent and topic. Use after useful consultation, discovery, research, testing, design, or error/blocker work so the next turn can reuse evidence instead of repeating it. The skill writes a continuity claim plus a continuity testament with working_context, evidence_digest, source_index, continuity_cursor, and session_cursor artifacts. It carries testaments/artifacts, not claims. Use mode=preview to inspect the deterministic source window without mutating the board.").
		Domain("memory").
		Keywords("carry", "forward", "continuity", "testaments", "artifacts", "recall", "memory").
		Priority(99).
		StringParam("topic", "Stable topic key for this work, e.g. planning:python-cli or interrupt:agents. Keep it consistent across turns.", true).
		StringParam("agent_id", "Calling agent override. Defaults to this agent.", false).
		StringParam("mode", "advance or preview. advance writes continuity artifacts; preview returns the selected sources only.", false).
		IntParam("max_sources", "Maximum source testaments/artifacts to carry. Default 8.", false).
		StringParam("plan_id", "Optional plan/workflow ID to stamp on the continuity claim and cursor.", false).
		StringParam("correlation_id", "Optional correlation/cycle ID to stamp on the continuity claim and cursor.", false).
		StringParam("previous_session_id", "Optional previous session ID when deliberately linking this topic's continuity across sessions.", false).
		StringParam("previous_board_id", "Optional previous session claims board ID for the cross-session continuity cursor.", false).
		StringParam("previous_continuity_testament_id", "Optional previous continuity testament ID for this agent/topic.", false).
		StringParam("freshness_horizon", "Optional duration such as 30m or 2h; sources older than this are skipped.", false).
		BoolParam("supersede_prior", "When true, the new continuity testament supersedes the previous one instead of amending it.", false).
		Usage("Call carry_forward(topic=..., mode=advance) after you have obtained reusable evidence. Call carry_forward(topic=..., mode=preview) before writing if you need to inspect what would be carried. Do not use it for transient spinner/progress status. Error artifacts are durable evidence and should be carried when they explain a blocker or failed path.").
		BestPractice("Use the same topic when continuing the same work across turns. Pair with recall_forward(topic=...) at the start of a later turn before consulting peers or repeating search/planning work.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			board, err := requireBoard(bp)
			if err != nil {
				return nil, fmt.Errorf("claims board: %w", err)
			}
			var params struct {
				Topic                         string `json:"topic"`
				AgentID                       string `json:"agent_id"`
				Mode                          string `json:"mode"`
				MaxSources                    int    `json:"max_sources"`
				PlanID                        string `json:"plan_id"`
				CorrelationID                 string `json:"correlation_id"`
				PreviousSessionID             string `json:"previous_session_id"`
				PreviousBoardID               string `json:"previous_board_id"`
				PreviousContinuityTestamentID string `json:"previous_continuity_testament_id"`
				FreshnessHorizon              string `json:"freshness_horizon"`
				SupersedePrior                bool   `json:"supersede_prior"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			agentID := strings.TrimSpace(params.AgentID)
			if agentID == "" {
				agentID = strings.TrimSpace(defaultAgentID)
			}
			horizon, err := parseCarryForwardDuration(params.FreshnessHorizon)
			if err != nil {
				return nil, err
			}
			return CarryForward(ctx, board, CarryForwardOptions{
				AgentID:                       agentID,
				Topic:                         params.Topic,
				PlanID:                        params.PlanID,
				CorrelationID:                 params.CorrelationID,
				PreviousSessionID:             params.PreviousSessionID,
				PreviousBoardID:               params.PreviousBoardID,
				PreviousContinuityTestamentID: params.PreviousContinuityTestamentID,
				MaxSources:                    params.MaxSources,
				Mode:                          params.Mode,
				SupersedePrior:                params.SupersedePrior,
				FreshnessHorizon:              horizon,
			})
		}).
		Build()
}

func RecallForwardSkill(bp BoardProvider, defaultAgentID string, openers ...SessionBoardOpener) *skills.Skill {
	return RecallForwardSkillWithEnrichment(bp, defaultAgentID, nil, openers...)
}

func RecallForwardSkillWithEnrichment(bp BoardProvider, defaultAgentID string, enrichment RecallForwardEnrichmentProvider, openers ...SessionBoardOpener) *skills.Skill {
	var opener SessionBoardOpener
	if len(openers) > 0 {
		opener = openers[0]
	}
	return skills.NewSkill("recall_forward").
		Description("Recall the calling agent's carried-forward testaments and artifacts for a topic. Prefer this before repeating a consult, search, design, planning, testing, or analysis step on a stable topic. Reads continuity testaments written by the same agent, follows amends/supersedes/session cursors, and returns compact working context plus evidence digests backed by source indexes. It does not consult peers.").
		Domain("memory").
		Keywords("recall", "forward", "continuity", "testaments", "artifacts", "memory", "history").
		Priority(100).
		StringParam("topic", "Stable topic key previously used with carry_forward.", true).
		StringParam("agent_id", "Calling agent override. Defaults to this agent.", false).
		IntParam("lookback_sessions", "How many session boundaries to walk backward. 0 means current session only.", false).
		IntParam("max_items", "Maximum continuity/source items to return. Default 8.", false).
		EnumParam("include_sources", "digest returns compact context; source_index includes source IDs/reasons; full hydrates source testaments/artifacts.", []string{"digest", "source_index", "full"}, false).
		Usage("Call recall_forward(topic=...) at the start of a repeated or resumed topic before consulting Archivalist, Librarian, Academic, or rerunning equivalent discovery. Treat only status=usable and usable=true as reusable evidence. Use include_sources=source_index when you need exact evidence IDs. Use include_sources=full only when the compact digest is insufficient.").
		BestPractice("A miss, insufficient, partial, stale, or contradicted recall result is not evidence. Do one narrow discovery/consult step for the remaining gap, then call carry_forward when useful source-indexed evidence exists.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			board, err := requireBoard(bp)
			if err != nil {
				return nil, fmt.Errorf("claims board: %w", err)
			}
			var params struct {
				Topic            string `json:"topic"`
				AgentID          string `json:"agent_id"`
				LookbackSessions int    `json:"lookback_sessions"`
				MaxItems         int    `json:"max_items"`
				IncludeSources   string `json:"include_sources"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			agentID := strings.TrimSpace(params.AgentID)
			if agentID == "" {
				agentID = strings.TrimSpace(defaultAgentID)
			}
			resolvedOpener := opener
			if resolvedOpener == nil {
				resolvedOpener = DurableSessionBoardOpenerFromBoard(board)
			}
			resolvedEnrichment := enrichment
			if resolvedEnrichment == nil {
				resolvedEnrichment = DefaultRecallForwardEnrichmentProvider()
			}
			return RecallForward(ctx, board, RecallForwardOptions{
				AgentID:            agentID,
				Topic:              params.Topic,
				LookbackSessions:   params.LookbackSessions,
				MaxItems:           params.MaxItems,
				IncludeSources:     params.IncludeSources,
				OpenBoard:          resolvedOpener,
				EnrichmentProvider: resolvedEnrichment,
			})
		}).
		Build()
}

func parseCarryForwardDuration(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	if n, err := strconv.Atoi(raw); err == nil {
		if n < 0 {
			return 0, fmt.Errorf("freshness_horizon must not be negative")
		}
		return time.Duration(n) * time.Second, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid freshness_horizon %q: %w", raw, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("freshness_horizon must not be negative")
	}
	return d, nil
}

func RebuildClaimsProjectionsSkill(bp BoardProvider) *skills.Skill {
	return RebuildClaimsProjectionsSkillWithRegistry(bp, DefaultSessionBoardRegistry())
}

func RebuildClaimsProjectionsSkillWithRegistry(bp BoardProvider, registry *SessionBoardRegistry) *skills.Skill {
	return skills.NewSkill("rebuild_claims_projections").
		Description("Repair or replay deterministic claims projection outboxes for the current board, a selected board/session, or all registered sessions. Replays existing outbox records through existing projectors such as fabric and knowledge; it does not mutate canonical claims except when emit_report=true records a rebuild report testament. Use dry_run=true first.").
		Domain("claims").
		Keywords("claims", "projection", "outbox", "repair", "rebuild", "knowledge", "fabric").
		Priority(30).
		StringParam("session_id", "Optional registered session ID to rebuild. Empty means current session unless all_sessions=true or board_id selects a board.", false).
		StringParam("board_id", "Optional claims board ID to rebuild. Can be combined with session_id or used across registered sessions.", false).
		BoolParam("all_sessions", "When true, rebuild every registered session board, optionally filtered by board_id.", false).
		StringParam("projectors", "Optional comma-separated projector names, e.g. fabric,knowledge. Empty means all registered projectors.", false).
		IntParam("from_sequence", "Optional inclusive starting board sequence.", false).
		IntParam("to_sequence", "Optional inclusive ending board sequence.", false).
		IntParam("limit", "Optional maximum number of outbox records to inspect.", false).
		BoolParam("dry_run", "When true, report what would be replayed without calling projectors or changing outbox state.", false).
		BoolParam("resume_from_last_success", "When true, skip records already marked succeeded for each selected projector.", false).
		BoolParam("emit_report", "When true, submit a projection_rebuild_report testament to the board.", false).
		BoolParam("authorized", "Required for non-dry-run repair. Dry-run remains the default safe mode.", false).
		Usage("Use for operational repair when projection health shows lag, retryable failures, or missing deterministic knowledge/fabric projection. Prefer dry_run=true before replaying.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			board, err := requireBoard(bp)
			if err != nil {
				return nil, fmt.Errorf("claims board: %w", err)
			}
			var params struct {
				SessionID             string `json:"session_id"`
				BoardID               string `json:"board_id"`
				AllSessions           bool   `json:"all_sessions"`
				Projectors            string `json:"projectors"`
				FromSequence          int    `json:"from_sequence"`
				ToSequence            int    `json:"to_sequence"`
				Limit                 int    `json:"limit"`
				DryRun                *bool  `json:"dry_run"`
				ResumeFromLastSuccess bool   `json:"resume_from_last_success"`
				EmitReport            bool   `json:"emit_report"`
				Authorized            bool   `json:"authorized"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			dryRun := true
			if params.DryRun != nil {
				dryRun = *params.DryRun
			}
			if !dryRun && !params.Authorized {
				return nil, fmt.Errorf("projection repair apply requires authorized=true; run dry_run=true first")
			}
			rebuild := ProjectionRebuildOptions{
				Projectors:            splitProjectorParam(params.Projectors),
				FromSequence:          positiveIntToUint64(params.FromSequence),
				ToSequence:            positiveIntToUint64(params.ToSequence),
				Limit:                 params.Limit,
				DryRun:                dryRun,
				ResumeFromLastSuccess: params.ResumeFromLastSuccess,
				EmitReport:            params.EmitReport,
			}
			if !params.AllSessions && strings.TrimSpace(params.SessionID) == "" && strings.TrimSpace(params.BoardID) == "" {
				return board.RebuildProjections(ctx, rebuild)
			}
			return RebuildRegisteredProjections(ctx, registry, board, ProjectionRebuildTargetOptions{
				SessionID:   params.SessionID,
				BoardID:     params.BoardID,
				AllSessions: params.AllSessions,
				Rebuild:     rebuild,
			})
		}).
		Build()
}

func ProjectionHealthSkill(bp BoardProvider) *skills.Skill {
	return skills.NewSkill("claims_projection_health").
		Description("Inspect deterministic claims projection health: outbox lag, queue depth, retry counts, terminal failures, expired leases, average projection latency, and per-projector backlog.").
		Domain("claims").
		Keywords("claims", "projection", "health", "outbox", "lag", "retries").
		Priority(60).
		BoolParam("include_history", "When true, include recent bounded health snapshots for trend/debug views.", false).
		IntParam("history_limit", "Maximum recent health snapshots to return when include_history=true. Default/all capped internally.", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			board, err := requireBoard(bp)
			if err != nil {
				return nil, fmt.Errorf("claims board: %w", err)
			}
			var params struct {
				IncludeHistory bool `json:"include_history"`
				HistoryLimit   int  `json:"history_limit"`
			}
			if len(input) > 0 {
				if err := json.Unmarshal(input, &params); err != nil {
					return nil, fmt.Errorf("invalid parameters: %w", err)
				}
			}
			current := board.ProjectionHealth()
			if !params.IncludeHistory {
				return current, nil
			}
			return map[string]any{
				"current": current,
				"history": board.ProjectionHealthHistory(params.HistoryLimit),
			}, nil
		}).
		Build()
}

func splitProjectorParam(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func positiveIntToUint64(value int) uint64 {
	if value <= 0 {
		return 0
	}
	return uint64(value)
}
