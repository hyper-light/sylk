package shared

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
)

// GlobalReviewProjection is the authoritative, read-only view of a
// global review protocol state. Mirrors PipelineProjection semantics for
// the global-review tier: single store, consumers read typed fields,
// LLM introspects via query_global_review_state.
type GlobalReviewProjection struct {
	ReviewID             string                           `json:"review_id,omitempty"`
	AgentType            string                           `json:"agent_type,omitempty"`
	ActiveAgents         []string                         `json:"active_agents,omitempty"`
	RequestedBy          string                           `json:"requested_by,omitempty"`
	CurrentRequest       string                           `json:"current_request,omitempty"`
	PendingChallenge     *GlobalReviewChallenge           `json:"pending_challenge,omitempty"`
	PendingValidation    *GlobalReviewValidationRecord    `json:"pending_validation,omitempty"`
	ProcessedValidations []GlobalReviewValidationProcessing `json:"processed_validations,omitempty"`
	RequiredAction       string                           `json:"required_action,omitempty"`
	RequiredActionReason string                           `json:"required_action_reason,omitempty"`
	TerminalAction       string                           `json:"terminal_action,omitempty"`
	AuditLock            *GlobalReviewAuditLock           `json:"audit_lock,omitempty"`
	RecentEvents         []GlobalReviewEvent              `json:"recent_events,omitempty"`
}

// Projection returns a deterministic read-only view of the current
// state. The validator, UI, and LLM query skill all consume this so
// there is one authoritative state shape.
func (s *GlobalReviewState) Projection() *GlobalReviewProjection {
	if s == nil {
		return &GlobalReviewProjection{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	projection := &GlobalReviewProjection{}
	if s.snapshot != nil {
		projection.ReviewID = s.snapshot.ReviewID
		projection.ActiveAgents = append([]string(nil), s.snapshot.ActiveAgents...)
		projection.RequestedBy = s.snapshot.RequestedBy
		projection.CurrentRequest = s.snapshot.CurrentRequest
		if s.snapshot.PendingChallenge != nil {
			cloned := *s.snapshot.PendingChallenge
			projection.PendingChallenge = &cloned
		}
		if s.snapshot.PendingValidation != nil {
			cloned := *s.snapshot.PendingValidation
			projection.PendingValidation = &cloned
		}
		if s.snapshot.AuditLock != nil {
			cloned := *s.snapshot.AuditLock
			projection.AuditLock = &cloned
		}
		projection.RecentEvents = append([]GlobalReviewEvent(nil), s.snapshot.RecentEvents...)
	}
	if s.terminalAction != nil {
		projection.TerminalAction = string(s.terminalAction.Type)
	}
	projection.ProcessedValidations = append([]GlobalReviewValidationProcessing(nil), s.processed...)
	projection.RequiredAction = string(s.requiredAction)
	projection.RequiredActionReason = s.requiredReason
	return projection
}

// QueryGlobalReviewStateSkill is the global-review sibling of
// QueryPipelineStateSkill. Same rationale: surface the authoritative
// projection so the LLM sees pending challenges, required terminal
// actions, and audit-lock state before committing to closure.
func QueryGlobalReviewStateSkill(cfg GlobalReviewProtocolSkillConfig) *skills.Skill {
	return skills.NewSkill("query_global_review_state").
		Description("Return the current global review protocol projection: pending challenges, processed validations, required terminal action, audit lock state, and recent events. Call this before any terminal action (finalize_global_review, challenge_*, handoff_next) to confirm preconditions.").
		Domain("global_review").
		Keywords("state", "status", "review", "pending", "challenge", "introspect").
		Priority(30).
		Usage("Use whenever you need to decide between finalize_global_review, challenge_global_agent(target=…), challenge_inspector, handoff_next, or process_validation — the projection shows which is legal right now.").
		Satisfies("Surfaces the authoritative global review protocol state as a typed projection so the LLM sees the same facts the validator will evaluate.").
		Handler(func(ctx context.Context, _ json.RawMessage) (any, error) {
			state := GlobalReviewStateFromContext(ctx)
			if state == nil {
				return &GlobalReviewProjection{}, nil
			}
			projection := state.Projection()
			if cfg.AgentType != nil {
				projection.AgentType = strings.TrimSpace(cfg.AgentType())
			}
			return projection, nil
		}).
		Build()
}
