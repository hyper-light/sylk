package shared

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
)

// PipelineProjection is the authoritative, read-only view of a pipeline
// protocol state at a moment in time. Every consumer — the protocol
// validator (finalize_pipeline, handoff_next, etc.), the UI activity
// channel, the LLM introspection skill — reads this struct instead of
// re-deriving state from the tool-call history. Typed fields let
// consumers switch on values instead of string-matching prose.
//
// Why this exists: the pre-projection era had finalize_pipeline
// reconstructing "did run_test_suite produce a snapshot?" / "is there a
// pending challenge?" from scratch each call. The UI had its own parallel
// state from InterAgentToolEvent streams. Drift between these sources
// produced phantom-challenge and missing-snapshot errors whose root cause
// was invisible to the LLM. A single projection eliminates drift by
// making one store authoritative; a single query skill surfaces the
// projection to the LLM so it can reason about protocol state instead of
// hitting validation gates blind.
type PipelineProjection struct {
	// Iteration is the current protocol iteration count.
	Iteration int `json:"iteration"`
	// AgentType names the agent running the projection (for scoping).
	AgentType string `json:"agent_type,omitempty"`
	// ActiveAgents lists the agent types currently engaged in this
	// pipeline turn.
	ActiveAgents []string `json:"active_agents,omitempty"`
	// Roster lists the pipeline roster with each agent's declared role.
	Roster []PipelineProtocolAgent `json:"roster,omitempty"`
	// RequestedBy names the agent that initiated this turn.
	RequestedBy string `json:"requested_by,omitempty"`
	// Mode is the current turn mode (single, cohort, etc.).
	Mode string `json:"mode,omitempty"`
	// CurrentRequest is the prose description of the active request.
	CurrentRequest string `json:"current_request,omitempty"`
	// PendingChallenge, when non-nil, names a challenge this agent must
	// respond to before ending the turn. validate_work is the canonical
	// response; the LLM checking the projection sees this directly.
	PendingChallenge *PipelineProtocolChallenge `json:"pending_challenge,omitempty"`
	// PendingValidation, when non-nil, is a validation response that
	// arrived in a prior turn and has not yet been processed.
	// process_validation is the canonical consumer.
	PendingValidation *PipelineValidationRecord `json:"pending_validation,omitempty"`
	// ProcessedValidations lists validations this agent has already
	// processed during the current turn.
	ProcessedValidations []PipelineValidationProcessing `json:"processed_validations,omitempty"`
	// RequiredAction, when non-empty, names the action this agent MUST
	// call next before any other terminal skill is legal. Set by the
	// protocol when a specific closure is forced (e.g., handoff_to_ot
	// after finalize_pipeline ready_for_ot=true).
	RequiredAction string `json:"required_action,omitempty"`
	// RequiredActionReason explains why RequiredAction is mandatory so
	// the LLM can surface it to the user when needed.
	RequiredActionReason string `json:"required_action_reason,omitempty"`
	// QueuedArtifacts maps recipient agent type to the verification
	// artifact ref the tester packaged for them. Non-empty entries block
	// ordinary turn termination; the LLM must dispatch via handoff_next
	// or validate_work to consume them.
	QueuedArtifacts map[string]PipelineHandoffArtifactRef `json:"queued_artifacts,omitempty"`
	// TerminalAction names the terminal action this turn has locked in
	// (if any). Second terminal actions are rejected by validateTerminalAction.
	TerminalAction string `json:"terminal_action,omitempty"`
	// AuditLock carries the inspector's audit-lock state when active.
	AuditLock *PipelineAuditLock `json:"audit_lock,omitempty"`
	// RecentEvents lists the last N protocol events for context.
	RecentEvents []PipelineProtocolEvent `json:"recent_events,omitempty"`

	// TesterSuiteCaptured reports whether run_test_suite has produced a
	// snapshot during the current turn. finalize_pipeline requires this
	// when the tester is packaging verification artifacts; surfacing it
	// here lets the LLM check before calling finalize_pipeline blindly.
	TesterSuiteCaptured bool `json:"tester_suite_captured,omitempty"`
	// TesterCurrentSuiteID is the snapshot id when TesterSuiteCaptured is
	// true. Empty otherwise.
	TesterCurrentSuiteID string `json:"tester_current_suite_id,omitempty"`
}

// Projection returns the current projected state of the pipeline protocol
// for consumers (validator, UI, LLM). Always returns a non-nil pointer so
// callers can read fields without a nil-check.
func (s *PipelineProtocolState) Projection() *PipelineProjection {
	if s == nil {
		return &PipelineProjection{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	projection := &PipelineProjection{}
	if s.snapshot != nil {
		projection.Iteration = s.snapshot.Iteration
		projection.ActiveAgents = append([]string(nil), s.snapshot.ActiveAgents...)
		projection.Roster = append([]PipelineProtocolAgent(nil), s.snapshot.Roster...)
		projection.RequestedBy = s.snapshot.RequestedBy
		projection.Mode = s.snapshot.Mode
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
		projection.RecentEvents = append([]PipelineProtocolEvent(nil), s.snapshot.RecentEvents...)
	}
	if s.terminalAction != nil {
		projection.TerminalAction = string(s.terminalAction.Type)
	}
	projection.ProcessedValidations = append([]PipelineValidationProcessing(nil), s.processed...)
	projection.RequiredAction = string(s.requiredAction)
	projection.RequiredActionReason = s.requiredReason
	if len(s.queuedArtifacts) > 0 {
		projection.QueuedArtifacts = make(map[string]PipelineHandoffArtifactRef, len(s.queuedArtifacts))
		for k, v := range s.queuedArtifacts {
			projection.QueuedArtifacts[k] = v
		}
	}
	if suiteID := strings.TrimSpace(s.testerSuiteID); suiteID != "" {
		projection.TesterCurrentSuiteID = suiteID
		projection.TesterSuiteCaptured = true
	}
	return projection
}

// QueryPipelineStateSkill returns the LLM-callable skill for reading the
// current pipeline protocol projection. The LLM calls this to see
// pending challenges, queued artifacts, required actions, and suite
// snapshot status before committing to a terminal action — eliminating
// the "finalize_pipeline fails three turns later with cryptic error"
// class of bug.
//
// Every pipeline agent (engineer, designer, inspector-pipeline, tester-
// pipeline) registers this skill. The config carries a TesterSuiteID
// lookup so the projection reflects whether the tester has captured a
// snapshot this turn.
func QueryPipelineStateSkill(cfg PipelineProtocolSkillConfig) *skills.Skill {
	return skills.NewSkill("query_pipeline_state").
		Description("Return the current pipeline protocol projection: pending challenges, queued verification artifacts, required terminal action, tester snapshot status, and recent events. Call this to understand what the protocol expects before committing to a terminal action (finalize_pipeline, handoff_next, validate_work, etc.).").
		Domain("pipeline").
		Keywords("state", "status", "pipeline", "pending", "snapshot", "challenge", "introspect").
		Priority(30).
		Usage("Use whenever you need to decide between finalize_pipeline, handoff_next, validate_work, or process_validation — the projection tells you which is legal right now. Also useful before any terminal action to confirm preconditions (tester snapshot present, no pending challenge, no queued artifacts blocking).").
		Satisfies("Surfaces the authoritative pipeline protocol state as a typed projection so the LLM sees the same facts the validator will evaluate.").
		Handler(func(ctx context.Context, _ json.RawMessage) (any, error) {
			state := PipelineProtocolStateFromContext(ctx)
			if state == nil {
				return &PipelineProjection{}, nil
			}
			projection := state.Projection()
			projection.AgentType = pipelineProtocolAgentType(ctx, cfg)
			if cfg.TesterCurrentSuiteID != nil {
				if suiteID := strings.TrimSpace(cfg.TesterCurrentSuiteID()); suiteID != "" {
					projection.TesterCurrentSuiteID = suiteID
					projection.TesterSuiteCaptured = true
				}
			}
			return projection, nil
		}).
		Build()
}
