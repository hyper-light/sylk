package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/adalundhe/sylk/core/skills"
)

const (
	PipelineAgentInspector = "inspector-pipeline"
	PipelineAgentTester    = "tester-pipeline"
	PipelineAgentEngineer  = "engineer"
	PipelineAgentDesigner  = "designer"
)

type PipelineTurnMode string

const (
	PipelineTurnModeSingle PipelineTurnMode = "single"
	PipelineTurnModeCohort PipelineTurnMode = "cohort"
)

type PipelineProtocolActionType string

const (
	PipelineProtocolActionHandoff  PipelineProtocolActionType = "handoff"
	PipelineProtocolActionValidate PipelineProtocolActionType = "validation"
	PipelineProtocolActionOT       PipelineProtocolActionType = "handoff_to_ot"
)

type PipelineValidationStatus string

const (
	PipelineValidationPassed  PipelineValidationStatus = "passed"
	PipelineValidationFailed  PipelineValidationStatus = "failed"
	PipelineValidationBlocked PipelineValidationStatus = "blocked"
	PipelineValidationUnclear PipelineValidationStatus = "unclear"
	PipelineValidationPartial PipelineValidationStatus = "partial"
)

type PipelineValidationDecision string

const (
	PipelineValidationDecisionAccept  PipelineValidationDecision = "accept"
	PipelineValidationDecisionReject  PipelineValidationDecision = "reject"
	PipelineValidationDecisionClarify PipelineValidationDecision = "clarify"
	PipelineValidationDecisionLoop    PipelineValidationDecision = "loop"
	PipelineValidationDecisionConsult PipelineValidationDecision = "consult"
	PipelineValidationDecisionHandoff PipelineValidationDecision = "handoff"
)

type PipelineProtocolAgent struct {
	AgentType string `json:"agent_type"`
	Role      string `json:"role,omitempty"`
}

type PipelineProtocolChallenge struct {
	ID              string   `json:"id"`
	RequestingAgent string   `json:"requesting_agent"`
	TargetAgents    []string `json:"target_agents,omitempty"`
	Mode            string   `json:"mode,omitempty"`
	Reason          string   `json:"reason,omitempty"`
	Request         string   `json:"request,omitempty"`
	RequiredOutput  []string `json:"required_output,omitempty"`
	References      []string `json:"references,omitempty"`
}

type PipelineValidationRecord struct {
	ChallengeID           string   `json:"challenge_id"`
	RequestingAgent       string   `json:"requesting_agent"`
	RespondingAgent       string   `json:"responding_agent"`
	Status                string   `json:"status"`
	Summary               string   `json:"summary"`
	EvidenceRefs          []string `json:"evidence_refs,omitempty"`
	MissingInputs         []string `json:"missing_inputs,omitempty"`
	RecommendedNextAgents []string `json:"recommended_next_agents,omitempty"`
}

type PipelineProtocolEvent struct {
	Type      string   `json:"type"`
	AgentType string   `json:"agent_type"`
	Targets   []string `json:"targets,omitempty"`
	Summary   string   `json:"summary"`
}

type PipelineProtocolSnapshot struct {
	Iteration         int                        `json:"iteration"`
	Roster            []PipelineProtocolAgent    `json:"roster,omitempty"`
	ActiveAgents      []string                   `json:"active_agents,omitempty"`
	RequestedBy       string                     `json:"requested_by,omitempty"`
	Mode              string                     `json:"mode,omitempty"`
	CurrentRequest    string                     `json:"current_request,omitempty"`
	PendingChallenge  *PipelineProtocolChallenge `json:"pending_challenge,omitempty"`
	PendingValidation *PipelineValidationRecord  `json:"pending_validation,omitempty"`
	RecentEvents      []PipelineProtocolEvent    `json:"recent_events,omitempty"`
}

type PipelineTurnAction struct {
	Type           PipelineProtocolActionType
	AgentType      string
	TargetAgents   []string
	Mode           PipelineTurnMode
	Reason         string
	Request        string
	RequiredOutput []string
	References     []string
	ChallengeID    string
	Validation     *PipelineValidationRecord
	Summary        string
	EvidenceRefs   []string
}

type PipelineValidationProcessing struct {
	ChallengeID string
	AgentType   string
	Decision    PipelineValidationDecision
	Summary     string
	NextTargets []string
}

type PipelineProtocolState struct {
	mu             sync.RWMutex
	snapshot       *PipelineProtocolSnapshot
	terminalAction *PipelineTurnAction
	processed      []PipelineValidationProcessing
}

type PipelineProtocolSkillConfig struct {
	AgentType   func() string
	InspectorOT bool
}

type pipelineProtocolStateKey struct{}

func WithPipelineProtocolState(ctx context.Context, state *PipelineProtocolState) context.Context {
	if state == nil {
		return ctx
	}
	return context.WithValue(ctx, pipelineProtocolStateKey{}, state)
}

func PipelineProtocolStateFromContext(ctx context.Context) *PipelineProtocolState {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(pipelineProtocolStateKey{}).(*PipelineProtocolState)
	return state
}

func NewPipelineProtocolState(snapshot *PipelineProtocolSnapshot) *PipelineProtocolState {
	return &PipelineProtocolState{snapshot: clonePipelineProtocolSnapshot(snapshot)}
}

func (s *PipelineProtocolState) Snapshot() *PipelineProtocolSnapshot {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return clonePipelineProtocolSnapshot(s.snapshot)
}

func (s *PipelineProtocolState) TerminalAction() *PipelineTurnAction {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return clonePipelineTurnAction(s.terminalAction)
}

func (s *PipelineProtocolState) ProcessedValidations() []PipelineValidationProcessing {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]PipelineValidationProcessing, len(s.processed))
	copy(out, s.processed)
	return out
}

func (s *PipelineProtocolState) setTerminalAction(action *PipelineTurnAction) error {
	if s == nil || action == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminalAction != nil {
		return fmt.Errorf("pipeline turn already selected %s", s.terminalAction.Type)
	}
	s.terminalAction = clonePipelineTurnAction(action)
	return nil
}

func (s *PipelineProtocolState) addProcessedValidation(entry PipelineValidationProcessing) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.processed = append(s.processed, entry)
}

func PipelineProtocolSnapshotMap(snapshot *PipelineProtocolSnapshot) map[string]any {
	if snapshot == nil {
		return nil
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func ValidatePipelineProtocolCompletion(ctx context.Context, role string) error {
	state := PipelineProtocolStateFromContext(ctx)
	if state == nil {
		return nil
	}
	if state.TerminalAction() != nil {
		return nil
	}
	role = normalizePipelineAgentType(role)
	if role == PipelineAgentInspector {
		return fmt.Errorf("Before ending this pipeline turn, use `handoff_next`, `validate_work`, or `handoff_to_ot` to record the next protocol step.")
	}
	return fmt.Errorf("Before ending this pipeline turn, use `handoff_next` or `validate_work` to record the next protocol step.")
}

func PipelineProtocolSkills(cfg PipelineProtocolSkillConfig) []*skills.Skill {
	out := []*skills.Skill{
		pipelineHandoffNextSkill(cfg),
		pipelineValidateWorkSkill(cfg),
		pipelineProcessValidationSkill(cfg),
	}
	if cfg.InspectorOT {
		out = append(out, pipelineHandoffOTSkill(cfg))
	}
	return out
}

func pipelineHandoffNextSkill(cfg PipelineProtocolSkillConfig) *skills.Skill {
	return skills.NewSkill("handoff_next").
		Description("Select the next active pipeline agent or execute cohort and state the concrete request they should satisfy.").
		Domain("pipeline").
		Keywords("handoff", "next", "pipeline", "challenge", "route").
		Priority(100).
		Usage("End the current pipeline turn by handing ownership to the next agent or cohort with a concrete request, required output, and references.").
		Satisfies("Records the next pipeline owner without hardcoding semantic stage transitions in the runtime.").
		ArrayParam("target_agents", "Canonical target agents: inspector, tester, engineer, designer, inspector-pipeline, or tester-pipeline", "string", true).
		EnumParam("mode", "single or cohort", []string{string(PipelineTurnModeSingle), string(PipelineTurnModeCohort)}, false).
		StringParam("reason", "Why this handoff is the correct next move", true).
		StringParam("request", "The concrete challenge, assignment, or question for the target agent(s)", true).
		ArrayParam("required_output", "What the target agent must return or validate", "string", false).
		ArrayParam("references", "Relevant files, artifacts, tests, or criteria to inspect", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				TargetAgents   []string `json:"target_agents"`
				Mode           string   `json:"mode"`
				Reason         string   `json:"reason"`
				Request        string   `json:"request"`
				RequiredOutput []string `json:"required_output"`
				References     []string `json:"references"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			targets, err := normalizePipelineTargets(params.TargetAgents)
			if err != nil {
				return nil, err
			}
			state := PipelineProtocolStateFromContext(ctx)
			if state == nil {
				return nil, fmt.Errorf("pipeline protocol state not available")
			}
			snapshot := state.Snapshot()
			if err := validatePipelineTargets(snapshot, targets); err != nil {
				return nil, err
			}
			mode := normalizePipelineTurnMode(params.Mode, len(targets))
			if mode == PipelineTurnModeCohort && len(targets) < 2 {
				return nil, fmt.Errorf("cohort handoff requires at least two target agents")
			}
			agentType := pipelineProtocolAgentType(ctx, cfg)
			action := &PipelineTurnAction{
				Type:           PipelineProtocolActionHandoff,
				AgentType:      agentType,
				TargetAgents:   targets,
				Mode:           mode,
				Reason:         strings.TrimSpace(params.Reason),
				Request:        strings.TrimSpace(params.Request),
				RequiredOutput: normalizeStringList(params.RequiredOutput),
				References:     normalizeStringList(params.References),
			}
			if action.Reason == "" {
				return nil, fmt.Errorf("reason is required")
			}
			if action.Request == "" {
				return nil, fmt.Errorf("request is required")
			}
			if err := state.setTerminalAction(action); err != nil {
				return nil, err
			}
			return map[string]any{
				"selected":      true,
				"agent_type":    agentType,
				"target_agents": append([]string(nil), targets...),
				"mode":          string(mode),
			}, nil
		}).
		Build()
}

func pipelineValidateWorkSkill(cfg PipelineProtocolSkillConfig) *skills.Skill {
	return skills.NewSkill("validate_work").
		Description("Respond to another pipeline agent's concrete challenge with a structured validation result and evidence.").
		Domain("pipeline").
		Keywords("validate", "challenge", "response", "evidence", "pipeline").
		Priority(100).
		Usage("Use when another pipeline agent asked you to justify, implement, inspect, or test concrete work.").
		Satisfies("Returns structured adversarial validation to the requesting agent.").
		StringParam("challenge_id", "The challenge identifier from the active protocol context", true).
		StringParam("requesting_agent", "The agent that asked this question", true).
		EnumParam("status", "Validation status", []string{
			string(PipelineValidationPassed),
			string(PipelineValidationFailed),
			string(PipelineValidationBlocked),
			string(PipelineValidationUnclear),
			string(PipelineValidationPartial),
		}, true).
		StringParam("summary", "What you validated, what happened, and why", true).
		ArrayParam("evidence_refs", "Files, tests, artifacts, or commands that support this response", "string", false).
		ArrayParam("missing_inputs", "What is still unclear or missing", "string", false).
		ArrayParam("recommended_next_agents", "Suggested next agent or cohort after this response", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				ChallengeID           string   `json:"challenge_id"`
				RequestingAgent       string   `json:"requesting_agent"`
				Status                string   `json:"status"`
				Summary               string   `json:"summary"`
				EvidenceRefs          []string `json:"evidence_refs"`
				MissingInputs         []string `json:"missing_inputs"`
				RecommendedNextAgents []string `json:"recommended_next_agents"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			state := PipelineProtocolStateFromContext(ctx)
			if state == nil {
				return nil, fmt.Errorf("pipeline protocol state not available")
			}
			snapshot := state.Snapshot()
			challenge := snapshot.PendingChallenge
			if challenge == nil {
				return nil, fmt.Errorf("no active pipeline challenge is waiting for validation")
			}
			if strings.TrimSpace(params.ChallengeID) == "" {
				return nil, fmt.Errorf("challenge_id is required")
			}
			if strings.TrimSpace(params.ChallengeID) != strings.TrimSpace(challenge.ID) {
				return nil, fmt.Errorf("challenge_id %q does not match the active pipeline challenge", strings.TrimSpace(params.ChallengeID))
			}
			requestingAgent := normalizePipelineAgentType(params.RequestingAgent)
			if requestingAgent == "" {
				return nil, fmt.Errorf("requesting_agent is required")
			}
			if normalizePipelineAgentType(challenge.RequestingAgent) != requestingAgent {
				return nil, fmt.Errorf("requesting_agent %q does not match the active pipeline challenge", params.RequestingAgent)
			}
			status := strings.TrimSpace(params.Status)
			if !isPipelineValidationStatus(status) {
				return nil, fmt.Errorf("status %q is invalid", params.Status)
			}
			summary := strings.TrimSpace(params.Summary)
			if summary == "" {
				return nil, fmt.Errorf("summary is required")
			}
			record := &PipelineValidationRecord{
				ChallengeID:           challenge.ID,
				RequestingAgent:       requestingAgent,
				RespondingAgent:       pipelineProtocolAgentType(ctx, cfg),
				Status:                status,
				Summary:               summary,
				EvidenceRefs:          normalizeStringList(params.EvidenceRefs),
				MissingInputs:         normalizeStringList(params.MissingInputs),
				RecommendedNextAgents: normalizeStringList(params.RecommendedNextAgents),
			}
			if err := state.setTerminalAction(&PipelineTurnAction{
				Type:        PipelineProtocolActionValidate,
				AgentType:   record.RespondingAgent,
				ChallengeID: challenge.ID,
				Validation:  record,
			}); err != nil {
				return nil, err
			}
			return map[string]any{
				"validated":        true,
				"challenge_id":     record.ChallengeID,
				"requesting_agent": record.RequestingAgent,
				"responding_agent": record.RespondingAgent,
				"status":           record.Status,
			}, nil
		}).
		Build()
}

func pipelineProcessValidationSkill(cfg PipelineProtocolSkillConfig) *skills.Skill {
	return skills.NewSkill("process_validation").
		Description("Acknowledge and interpret a validation response from another pipeline agent before deciding the next handoff.").
		Domain("pipeline").
		Keywords("process", "validation", "ingest", "decision", "pipeline").
		Priority(99).
		Usage("Use after another pipeline agent has responded to your challenge and before you decide whether to clarify, loop, consult, or hand off.").
		Satisfies("Records how the requesting agent interpreted the validation response.").
		StringParam("challenge_id", "The validation challenge to process", true).
		EnumParam("decision", "How you interpreted the response", []string{
			string(PipelineValidationDecisionAccept),
			string(PipelineValidationDecisionReject),
			string(PipelineValidationDecisionClarify),
			string(PipelineValidationDecisionLoop),
			string(PipelineValidationDecisionConsult),
			string(PipelineValidationDecisionHandoff),
		}, true).
		StringParam("summary", "Why you accepted, rejected, or need follow-up", true).
		ArrayParam("next_targets", "Optional next agents you are considering after processing the validation", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				ChallengeID string   `json:"challenge_id"`
				Decision    string   `json:"decision"`
				Summary     string   `json:"summary"`
				NextTargets []string `json:"next_targets"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			state := PipelineProtocolStateFromContext(ctx)
			if state == nil {
				return nil, fmt.Errorf("pipeline protocol state not available")
			}
			snapshot := state.Snapshot()
			pending := snapshot.PendingValidation
			if pending == nil {
				return nil, fmt.Errorf("no pending validation response is waiting to be processed")
			}
			challengeID := strings.TrimSpace(params.ChallengeID)
			if challengeID == "" {
				return nil, fmt.Errorf("challenge_id is required")
			}
			if challengeID != strings.TrimSpace(pending.ChallengeID) {
				return nil, fmt.Errorf("challenge_id %q does not match the pending validation response", challengeID)
			}
			decision := PipelineValidationDecision(strings.TrimSpace(params.Decision))
			if !isPipelineValidationDecision(string(decision)) {
				return nil, fmt.Errorf("decision %q is invalid", params.Decision)
			}
			summary := strings.TrimSpace(params.Summary)
			if summary == "" {
				return nil, fmt.Errorf("summary is required")
			}
			state.addProcessedValidation(PipelineValidationProcessing{
				ChallengeID: challengeID,
				AgentType:   pipelineProtocolAgentType(ctx, cfg),
				Decision:    decision,
				Summary:     summary,
				NextTargets: normalizeStringList(params.NextTargets),
			})
			return map[string]any{
				"processed":    true,
				"challenge_id": challengeID,
				"decision":     string(decision),
				"next_targets": normalizeStringList(params.NextTargets),
			}, nil
		}).
		Build()
}

func pipelineHandoffOTSkill(cfg PipelineProtocolSkillConfig) *skills.Skill {
	return skills.NewSkill("handoff_to_ot").
		Description("Finalize an accepted pipeline and hand the result to Operational Transform for merge. Inspector only.").
		Domain("pipeline").
		Keywords("ot", "merge", "accept", "finalize", "pipeline").
		Priority(100).
		Usage("Use only after the inspector has validated that testing and implementation criteria are satisfied and the pipeline should terminate successfully.").
		Satisfies("Marks the pipeline as accepted and ready for OT merge.").
		StringParam("summary", "Why the pipeline is ready for OT merge", true).
		ArrayParam("evidence_refs", "Criteria, tests, artifacts, and files supporting acceptance", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Summary      string   `json:"summary"`
				EvidenceRefs []string `json:"evidence_refs"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			agentType := pipelineProtocolAgentType(ctx, cfg)
			if agentType != PipelineAgentInspector {
				return nil, fmt.Errorf("handoff_to_ot is only permitted for the pipeline inspector")
			}
			summary := strings.TrimSpace(params.Summary)
			if summary == "" {
				return nil, fmt.Errorf("summary is required")
			}
			state := PipelineProtocolStateFromContext(ctx)
			if state == nil {
				return nil, fmt.Errorf("pipeline protocol state not available")
			}
			if err := state.setTerminalAction(&PipelineTurnAction{
				Type:         PipelineProtocolActionOT,
				AgentType:    agentType,
				Summary:      summary,
				EvidenceRefs: normalizeStringList(params.EvidenceRefs),
			}); err != nil {
				return nil, err
			}
			return map[string]any{
				"handoff_to_ot": true,
				"agent_type":    agentType,
				"evidence_refs": normalizeStringList(params.EvidenceRefs),
			}, nil
		}).
		Build()
}

func pipelineProtocolAgentType(ctx context.Context, cfg PipelineProtocolSkillConfig) string {
	if contract := TaskExecutionContractFromContext(ctx); contract != nil {
		if agentType := normalizePipelineAgentType(contract.RuntimeAgentType); agentType != "" {
			return agentType
		}
	}
	if cfg.AgentType != nil {
		if agentType := normalizePipelineAgentType(cfg.AgentType()); agentType != "" {
			return agentType
		}
	}
	return ""
}

func normalizePipelineAgentType(agentType string) string {
	switch strings.TrimSpace(strings.ToLower(agentType)) {
	case "inspector", PipelineAgentInspector:
		return PipelineAgentInspector
	case "tester", PipelineAgentTester:
		return PipelineAgentTester
	case PipelineAgentEngineer:
		return PipelineAgentEngineer
	case PipelineAgentDesigner:
		return PipelineAgentDesigner
	default:
		return strings.TrimSpace(agentType)
	}
}

func normalizePipelineTargets(values []string) ([]string, error) {
	targets := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		target := normalizePipelineAgentType(value)
		if target == "" {
			continue
		}
		switch target {
		case PipelineAgentInspector, PipelineAgentTester, PipelineAgentEngineer, PipelineAgentDesigner:
		default:
			return nil, fmt.Errorf("unknown pipeline target agent %q", value)
		}
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		targets = append(targets, target)
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("at least one target agent is required")
	}
	return targets, nil
}

func validatePipelineTargets(snapshot *PipelineProtocolSnapshot, targets []string) error {
	if snapshot == nil || len(snapshot.Roster) == 0 {
		return nil
	}
	allowed := make(map[string]struct{}, len(snapshot.Roster))
	for _, member := range snapshot.Roster {
		if agentType := normalizePipelineAgentType(member.AgentType); agentType != "" {
			allowed[agentType] = struct{}{}
		}
	}
	for _, target := range targets {
		if _, ok := allowed[target]; !ok {
			return fmt.Errorf("pipeline agent %q is not registered in this pipeline", target)
		}
	}
	return nil
}

func normalizePipelineTurnMode(mode string, targetCount int) PipelineTurnMode {
	switch strings.TrimSpace(strings.ToLower(mode)) {
	case string(PipelineTurnModeCohort):
		return PipelineTurnModeCohort
	case string(PipelineTurnModeSingle):
		return PipelineTurnModeSingle
	default:
		if targetCount > 1 {
			return PipelineTurnModeCohort
		}
		return PipelineTurnModeSingle
	}
}

func normalizeStringList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func isPipelineValidationStatus(value string) bool {
	switch strings.TrimSpace(value) {
	case string(PipelineValidationPassed),
		string(PipelineValidationFailed),
		string(PipelineValidationBlocked),
		string(PipelineValidationUnclear),
		string(PipelineValidationPartial):
		return true
	default:
		return false
	}
}

func isPipelineValidationDecision(value string) bool {
	switch strings.TrimSpace(value) {
	case string(PipelineValidationDecisionAccept),
		string(PipelineValidationDecisionReject),
		string(PipelineValidationDecisionClarify),
		string(PipelineValidationDecisionLoop),
		string(PipelineValidationDecisionConsult),
		string(PipelineValidationDecisionHandoff):
		return true
	default:
		return false
	}
}

func clonePipelineProtocolSnapshot(snapshot *PipelineProtocolSnapshot) *PipelineProtocolSnapshot {
	if snapshot == nil {
		return nil
	}
	out := *snapshot
	out.Roster = append([]PipelineProtocolAgent(nil), snapshot.Roster...)
	out.ActiveAgents = append([]string(nil), snapshot.ActiveAgents...)
	out.RecentEvents = append([]PipelineProtocolEvent(nil), snapshot.RecentEvents...)
	if snapshot.PendingChallenge != nil {
		challenge := *snapshot.PendingChallenge
		challenge.TargetAgents = append([]string(nil), snapshot.PendingChallenge.TargetAgents...)
		challenge.RequiredOutput = append([]string(nil), snapshot.PendingChallenge.RequiredOutput...)
		challenge.References = append([]string(nil), snapshot.PendingChallenge.References...)
		out.PendingChallenge = &challenge
	}
	if snapshot.PendingValidation != nil {
		record := *snapshot.PendingValidation
		record.EvidenceRefs = append([]string(nil), snapshot.PendingValidation.EvidenceRefs...)
		record.MissingInputs = append([]string(nil), snapshot.PendingValidation.MissingInputs...)
		record.RecommendedNextAgents = append([]string(nil), snapshot.PendingValidation.RecommendedNextAgents...)
		out.PendingValidation = &record
	}
	return &out
}

func clonePipelineTurnAction(action *PipelineTurnAction) *PipelineTurnAction {
	if action == nil {
		return nil
	}
	out := *action
	out.TargetAgents = append([]string(nil), action.TargetAgents...)
	out.RequiredOutput = append([]string(nil), action.RequiredOutput...)
	out.References = append([]string(nil), action.References...)
	out.EvidenceRefs = append([]string(nil), action.EvidenceRefs...)
	if action.Validation != nil {
		record := *action.Validation
		record.EvidenceRefs = append([]string(nil), action.Validation.EvidenceRefs...)
		record.MissingInputs = append([]string(nil), action.Validation.MissingInputs...)
		record.RecommendedNextAgents = append([]string(nil), action.Validation.RecommendedNextAgents...)
		out.Validation = &record
	}
	return &out
}
