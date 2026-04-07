package shared

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/google/uuid"
)

const (
	PipelineAgentInspector = "inspector-pipeline"
	PipelineAgentTester    = "tester-pipeline"
	PipelineAgentEngineer  = "engineer"
	PipelineAgentDesigner  = "designer"
)

const maxPipelineProtocolEventHistory = 8

const (
	maxPipelineProtocolRequestLen       = 1600
	maxPipelineProtocolReasonLen        = 600
	maxPipelineProtocolSummaryLen       = 1600
	maxPipelineProtocolEventSummaryLen  = 800
	maxPipelineProtocolReferenceLen     = 240
	maxPipelineProtocolMaxReferences    = 16
	maxPipelineProtocolMaxTargetAgents  = 8
	maxPipelineProtocolMaxRecentTargets = 8
)

const finalizePipelineVerificationReference = "finalize_pipeline_verification"

const (
	PipelineAuditPhaseFinalizing  = "finalize_pipeline"
	PipelineAuditPhaseHandoffToOT = "handoff_to_ot"
)

type PipelineTurnMode string

const (
	PipelineTurnModeSingle PipelineTurnMode = "single"
	PipelineTurnModeCohort PipelineTurnMode = "cohort"
)

type PipelineProtocolActionType string

const (
	PipelineProtocolActionHandoff  PipelineProtocolActionType = "handoff"
	PipelineProtocolActionRefusal  PipelineProtocolActionType = "refusal"
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
	ID                string   `json:"id"`
	RequestingAgent   string   `json:"requesting_agent"`
	RequestingAgentID string   `json:"requesting_agent_id,omitempty"`
	TargetAgents      []string `json:"target_agents,omitempty"`
	Mode              string   `json:"mode,omitempty"`
	Reason            string   `json:"reason,omitempty"`
	Request           string   `json:"request,omitempty"`
	RequiredOutput    []string `json:"required_output,omitempty"`
	References        []string `json:"references,omitempty"`
}

type PipelineValidationRecord struct {
	ChallengeID           string   `json:"challenge_id"`
	RequestingAgent       string   `json:"requesting_agent"`
	RequestingAgentID     string   `json:"requesting_agent_id,omitempty"`
	RespondingAgent       string   `json:"responding_agent"`
	RespondingAgentID     string   `json:"responding_agent_id,omitempty"`
	Status                string   `json:"status"`
	Summary               string   `json:"summary"`
	ChallengeRequest      string   `json:"challenge_request,omitempty"`
	ChallengeReferences   []string `json:"challenge_references,omitempty"`
	EvidenceRefs          []string `json:"evidence_refs,omitempty"`
	MissingInputs         []string `json:"missing_inputs,omitempty"`
	RecommendedNextAgents []string `json:"recommended_next_agents,omitempty"`
}

type PipelineProtocolEvent struct {
	Type                 string   `json:"type"`
	AgentType            string   `json:"agent_type"`
	Targets              []string `json:"targets,omitempty"`
	Summary              string   `json:"summary"`
	CreatesChallenge     bool     `json:"creates_challenge,omitempty"`
	WorkspaceFingerprint string   `json:"workspace_fingerprint,omitempty"`
}

type PipelineAuditLock struct {
	OwnerAgent string `json:"owner_agent"`
	Phase      string `json:"phase"`
	Reason     string `json:"reason,omitempty"`
}

type PipelineProtocolSnapshot struct {
	Iteration         int                        `json:"iteration"`
	Roster            []PipelineProtocolAgent    `json:"roster,omitempty"`
	ActiveAgents      []string                   `json:"active_agents,omitempty"`
	RequestedBy       string                     `json:"requested_by,omitempty"`
	Mode              string                     `json:"mode,omitempty"`
	CurrentRequest    string                     `json:"current_request,omitempty"`
	AuditLock         *PipelineAuditLock         `json:"audit_lock,omitempty"`
	PendingChallenge  *PipelineProtocolChallenge `json:"pending_challenge,omitempty"`
	PendingValidation *PipelineValidationRecord  `json:"pending_validation,omitempty"`
	RecentEvents      []PipelineProtocolEvent    `json:"recent_events,omitempty"`
}

type PipelineTurnAction struct {
	Type                 PipelineProtocolActionType
	AgentType            string
	AgentID              string
	CreatesChallenge     bool
	AuditLockPhase       string
	TargetAgents         []string
	Mode                 PipelineTurnMode
	Reason               string
	Request              string
	RequiredOutput       []string
	References           []string
	ChallengeID          string
	Validation           *PipelineValidationRecord
	Summary              string
	EvidenceRefs         []string
	WorkspaceFingerprint string
}

type PipelineValidationProcessing struct {
	ChallengeID string
	AgentType   string
	Decision    PipelineValidationDecision
	Summary     string
	NextTargets []string
	Validation  *PipelineValidationRecord `json:"validation,omitempty"`
}

type PipelineTurnResponse struct {
	Result    any                            `json:"result,omitempty"`
	Action    *PipelineTurnAction            `json:"action,omitempty"`
	Processed []PipelineValidationProcessing `json:"processed,omitempty"`
}

type pipelineTurnBaseline struct {
	processedCount int
}

type pipelineTurnBaselineKey struct{}

type pipelineResponseTexter interface {
	ResponseText() string
}

// ResponseText lets Guide/UI use the inner user-facing result text when a
// pipeline turn response is carried as a structured envelope.
func (r *PipelineTurnResponse) ResponseText() string {
	if r == nil || r.Result == nil {
		return ""
	}
	if text, ok := r.Result.(string); ok {
		return strings.TrimSpace(text)
	}
	if rt, ok := r.Result.(pipelineResponseTexter); ok {
		return strings.TrimSpace(rt.ResponseText())
	}
	return ""
}

type PipelineProtocolRouteConfig struct {
	Bus            guide.EventBus
	BusProvider    func() guide.EventBus
	SessionID      func() string
	PublishReroute func(context.Context, string, string, string)
}

type PipelineProtocolState struct {
	mu             sync.RWMutex
	sessionDir     string
	scopeID        string
	store          *durableProtocolLog
	snapshot       *PipelineProtocolSnapshot
	terminalAction *PipelineTurnAction
	processed      []PipelineValidationProcessing
	requiredAction PipelineProtocolActionType
	requiredReason string
}

type PipelineProtocolSkillConfig struct {
	AgentType      func() string
	AgentID        func() string
	InspectorOT    bool
	WorkspaceViews func() versioning.WorkspaceViewAccess
	Route          PipelineProtocolRouteConfig
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

func WithPipelineTaskProtocolState(ctx context.Context, task *PipelineTaskInput) context.Context {
	ctx = WithPipelineTask(ctx, task)
	if ctx == nil || task == nil || PipelineProtocolStateFromContext(ctx) != nil {
		return ctx
	}
	state, err := newPipelineProtocolStateForTask(task)
	if err != nil || state == nil {
		if snapshot, snapErr := PipelineProtocolSnapshotFromTask(task); snapErr == nil && snapshot != nil {
			return WithPipelineProtocolState(ctx, NewPipelineProtocolState(snapshot))
		}
		return ctx
	}
	state.HydrateTask(task)
	return WithPipelineProtocolState(ctx, state)
}

func PipelineProtocolSnapshotFromTask(task *PipelineTaskInput) (*PipelineProtocolSnapshot, error) {
	if task == nil || len(task.Context) == 0 {
		return nil, nil
	}
	raw, ok := task.Context["pipeline_protocol"]
	if !ok || raw == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var snapshot PipelineProtocolSnapshot
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
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
	for i, entry := range s.processed {
		out[i] = clonePipelineValidationProcessing(entry)
	}
	return out
}

func (s *PipelineProtocolState) RequiredAction() (PipelineProtocolActionType, string) {
	if s == nil {
		return "", ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.requiredAction, strings.TrimSpace(s.requiredReason)
}

func (s *PipelineProtocolState) Close() error {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store.Close()
}

func ClosePipelineProtocolState(ctx context.Context) error {
	state := PipelineProtocolStateFromContext(ctx)
	if state == nil {
		return nil
	}
	return state.Close()
}

func (s *PipelineProtocolState) setTerminalAction(action *PipelineTurnAction) error {
	if s == nil || action == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.requiredAction != "" && action.Type != s.requiredAction {
		return fmt.Errorf("%s", requiredPipelineActionMessageLocked(s.requiredAction, s.requiredReason))
	}
	if s.terminalAction != nil {
		return fmt.Errorf("pipeline turn already selected %s", s.terminalAction.Type)
	}
	s.terminalAction = clonePipelineTurnAction(action)
	if s.requiredAction != "" && action.Type == s.requiredAction {
		s.requiredAction = ""
		s.requiredReason = ""
	}
	return nil
}

func (s *PipelineProtocolState) validateTerminalAction(action *PipelineTurnAction) error {
	if s == nil || action == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.requiredAction != "" && action.Type != s.requiredAction {
		return fmt.Errorf("%s", requiredPipelineActionMessageLocked(s.requiredAction, s.requiredReason))
	}
	if s.terminalAction != nil {
		return fmt.Errorf("pipeline turn already selected %s", s.terminalAction.Type)
	}
	return nil
}

func (s *PipelineProtocolState) addProcessedValidation(entry PipelineValidationProcessing) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.processed = append(s.processed, clonePipelineValidationProcessing(entry))
}

func (s *PipelineProtocolState) requireTerminalAction(action PipelineProtocolActionType, reason string) {
	if s == nil || strings.TrimSpace(string(action)) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requiredAction = action
	s.requiredReason = strings.TrimSpace(reason)
}

func (s *PipelineProtocolState) HydrateTask(task *PipelineTaskInput) {
	if s == nil || task == nil {
		return
	}
	snapshot := materializePipelineProtocolSnapshot(s)
	if snapshot == nil {
		return
	}
	if task.Context == nil {
		task.Context = map[string]any{}
	}
	task.Context["pipeline_protocol"] = pipelineProtocolTaskSnapshotMap(snapshot)
	if obligations := s.CurrentAgentObligations(normalizePipelineAgentType(task.AgentType)); len(obligations) > 0 {
		task.Context["pipeline_protocol_obligations"] = obligations
	} else {
		delete(task.Context, "pipeline_protocol_obligations")
	}
}

func requiredPipelineActionMessage(action PipelineProtocolActionType, reason string) string {
	required := strings.TrimSpace(string(action))
	if required == string(PipelineProtocolActionOT) {
		message := "Before ending this pipeline turn, `finalize_pipeline` already determined the pipeline is ready for OT, so you must invoke `handoff_to_ot` now. Do not summarize the handoff, start another audit loop, choose a different terminal action, or continue with other queued work or other pipelines first."
		if strings.TrimSpace(reason) != "" {
			return message + " " + strings.TrimSpace(reason)
		}
		return message
	}
	message := fmt.Sprintf("Before ending this pipeline turn, you must invoke `%s` now.", required)
	if strings.TrimSpace(reason) != "" {
		return message + " " + strings.TrimSpace(reason)
	}
	return message
}

func requiredPipelineActionMessageLocked(action PipelineProtocolActionType, reason string) string {
	return requiredPipelineActionMessage(action, reason)
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

func pipelineProtocolTaskSnapshotMap(snapshot *PipelineProtocolSnapshot) map[string]any {
	return PipelineProtocolSnapshotMap(compactPipelineProtocolSnapshotForTask(snapshot))
}

func BuildPipelineTurnResponse(ctx context.Context, result any) any {
	state := PipelineProtocolStateFromContext(ctx)
	if state == nil {
		return result
	}
	return &PipelineTurnResponse{
		Result:    result,
		Action:    state.TerminalAction(),
		Processed: state.ProcessedValidations(),
	}
}

func WithPipelineTurnBaseline(ctx context.Context) context.Context {
	state := PipelineProtocolStateFromContext(ctx)
	if state == nil {
		return ctx
	}
	return context.WithValue(ctx, pipelineTurnBaselineKey{}, pipelineTurnBaseline{
		processedCount: len(state.ProcessedValidations()),
	})
}

func PipelinePostValidationDecisionOutstanding(ctx context.Context) bool {
	state := PipelineProtocolStateFromContext(ctx)
	if state == nil || state.TerminalAction() != nil {
		return false
	}
	baseline, ok := ctx.Value(pipelineTurnBaselineKey{}).(pipelineTurnBaseline)
	if !ok {
		return false
	}
	return len(state.ProcessedValidations()) > baseline.processedCount
}

func DecodePipelineTurnResponse(data any) (*PipelineTurnResponse, error) {
	switch typed := data.(type) {
	case nil:
		return nil, fmt.Errorf("pipeline turn response is required")
	case *PipelineTurnResponse:
		return typed, nil
	case PipelineTurnResponse:
		copy := typed
		return &copy, nil
	default:
		encoded, err := json.Marshal(data)
		if err != nil {
			return nil, err
		}
		var response PipelineTurnResponse
		if err := json.Unmarshal(encoded, &response); err != nil {
			return nil, err
		}
		return &response, nil
	}
}

func ApplyPipelineTurnResponse(ctx context.Context, response *PipelineTurnResponse) error {
	if response == nil {
		return nil
	}
	state := PipelineProtocolStateFromContext(ctx)
	if state == nil {
		return nil
	}
	if response.Action != nil {
		if err := state.setTerminalAction(response.Action); err != nil {
			return err
		}
	}
	for _, entry := range response.Processed {
		state.addProcessedValidation(entry)
	}
	return nil
}

func ValidatePipelineProtocolCompletion(ctx context.Context, role string) error {
	state := PipelineProtocolStateFromContext(ctx)
	if state == nil {
		return nil
	}
	if required, reason := state.RequiredAction(); required != "" {
		action := state.TerminalAction()
		if action == nil || action.Type != required {
			return fmt.Errorf("%s", requiredPipelineActionMessage(required, reason))
		}
	}
	if state.TerminalAction() != nil {
		return nil
	}
	role = normalizePipelineAgentType(role)
	if role == PipelineAgentInspector {
		if PipelinePostValidationDecisionOutstanding(ctx) {
			return fmt.Errorf("Before ending this inspector pipeline turn, you already called `process_validation`. Finish any remaining direct audit you still need, then record the next protocol step with `challenge_agent`, `handoff_next`, `finalize_pipeline`, or `handoff_to_ot`.")
		}
		return fmt.Errorf("Before ending this pipeline turn, use `challenge_agent`, `handoff_next`, `validate_work`, `finalize_pipeline`, or `handoff_to_ot` to record the next protocol step.")
	}
	return fmt.Errorf("Before ending this pipeline turn, use `challenge_agent`, `handoff_next`, or `validate_work` to record the next protocol step.")
}

func PipelineProtocolSkills(cfg PipelineProtocolSkillConfig) []*skills.Skill {
	out := []*skills.Skill{
		pipelineChallengeAgentSkill(cfg),
		pipelineHandoffNextSkill(cfg),
		pipelineValidateWorkSkill(cfg),
		pipelineProcessValidationSkill(cfg),
	}
	if cfg.InspectorOT {
		out = append(out, pipelineFinalizePipelineSkill(cfg), pipelineHandoffOTSkill(cfg))
	}
	return out
}

type pipelineTurnSelectionParams struct {
	TargetAgents   []string `json:"target_agents"`
	Mode           string   `json:"mode"`
	Reason         string   `json:"reason"`
	Request        string   `json:"request"`
	RequiredOutput []string `json:"required_output"`
	References     []string `json:"references"`
}

func pipelineChallengeAgentSkill(cfg PipelineProtocolSkillConfig) *skills.Skill {
	return skills.NewSkill("challenge_agent").
		Description("Issue a targeted follow-up challenge when returned pipeline work is unclear, off-spec, incomplete, or otherwise needs direct response.").
		Domain("pipeline").
		Keywords("challenge", "peer review", "validation", "pipeline", "route").
		Priority(100).
		Usage("Use after you have inspected returned peer work and a specific unresolved gap remains that the target agent must answer directly. A first challenge to a directed target is allowed. Repeated challenges require the directed pair's required new VFS evidence before you call this skill again. Do not use this for ordinary phase progression; use `handoff_next` for the normal top-level flow.").
		Satisfies("Creates an explicit targeted pipeline challenge and routes it to the challenged agent or cohort.").
		Requirement("Do not re-challenge the same agent without fresh pipeline VFS evidence. Inspector may re-challenge Tester, Engineer, or Designer only after that target changed VFS since Inspector's previous challenge to that target. Tester, Engineer, and Designer may re-challenge each other only after the target changed VFS since the challenger's previous challenge. Tester, Engineer, and Designer may re-challenge Inspector only after the challenger changed VFS in response to Inspector's previous answer.").
		Avoid("Do not use to advance the normal Inspector -> Tester -> Engineer/Designer -> Inspector phase flow, and do not use to ping the same pipeline agent again when the required side has not changed the pipeline VFS since the prior challenge on that directed pair.").
		ArrayParam("target_agents", "Canonical target agents: inspector, tester, engineer, designer, inspector-pipeline, or tester-pipeline", "string", true).
		EnumParam("mode", "single or cohort", []string{string(PipelineTurnModeSingle), string(PipelineTurnModeCohort)}, false).
		StringParam("reason", "Why this challenge is necessary now", true).
		StringParam("request", "The concrete challenge, assignment, or question for the target agent(s)", true).
		ArrayParam("required_output", "What the target agent must return or validate", "string", false).
		ArrayParam("references", "Relevant files, artifacts, tests, or criteria to inspect", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params pipelineTurnSelectionParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			return issuePipelineTurnSelection(ctx, cfg, params, true)
		}).
		Build()
}

func pipelineHandoffNextSkill(cfg PipelineProtocolSkillConfig) *skills.Skill {
	return skills.NewSkill("handoff_next").
		Description("Select the next active top-level pipeline owner for ordinary phase progression and state the concrete request they should satisfy.").
		Domain("pipeline").
		Keywords("handoff", "next", "pipeline", "challenge", "route").
		Priority(100).
		Usage("End the current pipeline turn by handing top-level ownership to the next agent or cohort with a concrete request, required output, and references. Use this for ordinary phase progression such as Inspector -> Tester for initial tests, Inspector -> Engineer/Designer for implementation, and returned top-level work back to Inspector. Do not use this when you are directly answering an active challenge; use `validate_work` instead.").
		Satisfies("Records and dispatches the next top-level pipeline owner without hardcoding semantic stage transitions in the runtime.").
		ArrayParam("target_agents", "Canonical target agents: inspector, tester, engineer, designer, inspector-pipeline, or tester-pipeline", "string", true).
		EnumParam("mode", "single or cohort", []string{string(PipelineTurnModeSingle), string(PipelineTurnModeCohort)}, false).
		StringParam("reason", "Why this handoff is the correct next move", true).
		StringParam("request", "The concrete challenge, assignment, or question for the target agent(s)", true).
		ArrayParam("required_output", "What the target agent must return or validate", "string", false).
		ArrayParam("references", "Relevant files, artifacts, tests, or criteria to inspect", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params pipelineTurnSelectionParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			return issuePipelineTurnSelection(ctx, cfg, params, false)
		}).
		Build()
}

func issuePipelineTurnSelection(
	ctx context.Context,
	cfg PipelineProtocolSkillConfig,
	params pipelineTurnSelectionParams,
	createsChallenge bool,
) (map[string]any, error) {
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
	for _, target := range targets {
		if normalizePipelineAgentType(target) == normalizePipelineAgentType(agentType) {
			return nil, fmt.Errorf("pipeline agent %q cannot target itself", strings.TrimSpace(agentType))
		}
	}
	if createsChallenge {
		if refusal := pipelineAuditChallengeRefusal(snapshot, agentType, targets); refusal != nil {
			action := &PipelineTurnAction{
				Type:         PipelineProtocolActionRefusal,
				AgentType:    agentType,
				TargetAgents: []string{PipelineAgentInspector},
				Summary:      refusal.Reason,
			}
			if err := state.setTerminalAction(action); err != nil {
				return nil, err
			}
			return map[string]any{
				"refused":           true,
				"refused_by":        firstNonEmpty(strings.TrimSpace(refusal.RefusedBy), PipelineAgentInspector),
				"agent_type":        agentType,
				"audit_phase":       refusal.AuditPhase,
				"reason":            refusal.Reason,
				"must_wait":         true,
				"resume_conditions": append([]string(nil), refusal.ResumeConditions...),
			}, nil
		}
	}
	var challengeEvidence *pipelineChallengeEvidence
	if createsChallenge {
		challengeEvidence = resolvePipelineChallengeEvidence(ctx, cfg)
		if refusal := pipelineRepeatedChallengeRefusal(snapshot, agentType, targets, challengeEvidence); refusal != nil {
			action := &PipelineTurnAction{
				Type:         PipelineProtocolActionRefusal,
				AgentType:    agentType,
				TargetAgents: []string{PipelineAgentInspector},
				Summary:      refusal.Reason,
			}
			if err := state.setTerminalAction(action); err != nil {
				return nil, err
			}
			return map[string]any{
				"refused":           true,
				"refused_by":        firstNonEmpty(strings.TrimSpace(refusal.RefusedBy), "pipeline-protocol"),
				"agent_type":        agentType,
				"reason":            refusal.Reason,
				"must_wait":         true,
				"resume_conditions": append([]string(nil), refusal.ResumeConditions...),
			}, nil
		}
	}
	action := &PipelineTurnAction{
		Type:                 PipelineProtocolActionHandoff,
		AgentType:            agentType,
		AgentID:              pipelineProtocolAgentID(ctx, cfg),
		CreatesChallenge:     createsChallenge,
		TargetAgents:         targets,
		Mode:                 mode,
		Reason:               strings.TrimSpace(params.Reason),
		Request:              strings.TrimSpace(params.Request),
		RequiredOutput:       normalizeStringList(params.RequiredOutput),
		References:           normalizeStringList(params.References),
		WorkspaceFingerprint: pipelineChallengeFingerprint(challengeEvidence),
	}
	if action.Reason == "" {
		return nil, fmt.Errorf("reason is required")
	}
	if action.Request == "" {
		return nil, fmt.Errorf("request is required")
	}
	if createsChallenge && pipelineProtocolRouteEnabled(ctx, cfg) && strings.TrimSpace(action.AgentID) == "" {
		return nil, fmt.Errorf("pipeline challenge routing requires the exact requesting agent id; missing current agent identity")
	}
	if err := state.validateTerminalAction(action); err != nil {
		return nil, err
	}
	if task := PipelineTaskFromContext(ctx); task != nil {
		var dispatch *pipelineDispatchSelection
		if createsChallenge {
			action.ChallengeID = nextPipelineChallengeID(task)
		}
		if pipelineProtocolRouteEnabled(ctx, cfg) {
			var err error
			dispatch, err = dispatchPipelineHandoffSelection(ctx, cfg, state, task, action)
			if err != nil {
				return nil, err
			}
		} else if createsChallenge {
			return nil, fmt.Errorf("pipeline handoff requires active stream routing context")
		}
		if err := state.recordHandoffAction(ctx, action); err != nil {
			return nil, err
		}
		if err := state.setTerminalAction(action); err != nil {
			return nil, err
		}
		return pipelineTurnSelectionResult(agentType, action, dispatch), nil
	}
	if err := state.recordHandoffAction(ctx, action); err != nil {
		return nil, err
	}
	if err := state.setTerminalAction(action); err != nil {
		return nil, err
	}
	return pipelineTurnSelectionResult(agentType, action, nil), nil
}

type pipelineDispatchSelection struct {
	CorrelationIDs []string
	TargetAgentIDs []string
}

type pipelineProtocolRouteOptions struct {
	InterAgentBranch                  *InterAgentBranchSpec
	ExplicitTopLevelTransferParentCID string
}

func pipelineTurnSelectionResult(agentType string, action *PipelineTurnAction, dispatch *pipelineDispatchSelection) map[string]any {
	result := map[string]any{
		"selected":      true,
		"agent_type":    agentType,
		"target_agents": append([]string(nil), action.TargetAgents...),
		"mode":          string(action.Mode),
	}
	if strings.TrimSpace(action.ChallengeID) != "" {
		result["challenge_id"] = strings.TrimSpace(action.ChallengeID)
	}
	if strings.TrimSpace(action.WorkspaceFingerprint) != "" {
		result["workspace_fingerprint"] = strings.TrimSpace(action.WorkspaceFingerprint)
	}
	if dispatch != nil && len(dispatch.TargetAgentIDs) > 0 {
		result["forwarded"] = true
		result["correlation_ids"] = append([]string(nil), dispatch.CorrelationIDs...)
		result["target_agent_ids"] = append([]string(nil), dispatch.TargetAgentIDs...)
		if len(dispatch.CorrelationIDs) == 1 {
			result["correlation_id"] = strings.TrimSpace(dispatch.CorrelationIDs[0])
		}
		if len(dispatch.TargetAgentIDs) == 1 {
			result["target_agent_id"] = strings.TrimSpace(dispatch.TargetAgentIDs[0])
		}
	}
	return result
}

func pipelineValidateWorkSkill(cfg PipelineProtocolSkillConfig) *skills.Skill {
	return skills.NewSkill("validate_work").
		Description("Respond to an active concrete challenge from another pipeline agent with a structured validation result and evidence.").
		Domain("pipeline").
		Keywords("validate", "challenge", "response", "evidence", "pipeline").
		Priority(100).
		Usage("Use only when another pipeline agent has an active challenge waiting for your response. This is the required response path for inspector, tester, engineer, or designer challenge turns. Ordinary top-level phase completion still uses `handoff_next`.").
		Satisfies("Returns structured adversarial validation to the requesting agent instead of creating a new top-level handoff.").
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
				RequestingAgentID:     strings.TrimSpace(challenge.RequestingAgentID),
				RespondingAgent:       pipelineProtocolAgentType(ctx, cfg),
				RespondingAgentID:     pipelineProtocolAgentID(ctx, cfg),
				Status:                status,
				Summary:               summary,
				ChallengeRequest:      strings.TrimSpace(challenge.Request),
				ChallengeReferences:   normalizeStringList(challenge.References),
				EvidenceRefs:          normalizeStringList(params.EvidenceRefs),
				MissingInputs:         normalizeStringList(params.MissingInputs),
				RecommendedNextAgents: normalizeStringList(params.RecommendedNextAgents),
			}
			if pipelineProtocolRouteEnabled(ctx, cfg) {
				if strings.TrimSpace(record.RequestingAgentID) == "" {
					return nil, fmt.Errorf("pipeline validation return requires the exact requesting agent id from the active challenge")
				}
				if strings.TrimSpace(record.RespondingAgentID) == "" {
					return nil, fmt.Errorf("pipeline validation return requires the exact responding agent id")
				}
			}
			terminalAction := &PipelineTurnAction{
				Type:        PipelineProtocolActionValidate,
				AgentType:   record.RespondingAgent,
				AgentID:     record.RespondingAgentID,
				ChallengeID: challenge.ID,
				Validation:  record,
			}
			if err := state.validateTerminalAction(terminalAction); err != nil {
				return nil, err
			}
			if task := PipelineTaskFromContext(ctx); task != nil {
				if !pipelineProtocolRouteEnabled(ctx, cfg) {
					return nil, fmt.Errorf("pipeline validation return requires active stream routing context")
				}
				nextTask, err := buildPipelineValidationTask(state, task, record)
				if err != nil {
					return nil, err
				}
				correlationID, err := dispatchPipelineProtocolTaskWithOptions(
					ctx,
					cfg,
					nextTask,
					record.Summary,
					pipelineValidationRouteOptions(ctx),
				)
				if err != nil {
					return nil, err
				}
				if err := state.recordValidation(ctx, record); err != nil {
					return nil, err
				}
				if err := state.setTerminalAction(terminalAction); err != nil {
					return nil, err
				}
				return map[string]any{
					"validated":           true,
					"challenge_id":        record.ChallengeID,
					"requesting_agent":    record.RequestingAgent,
					"requesting_agent_id": record.RequestingAgentID,
					"responding_agent":    record.RespondingAgent,
					"responding_agent_id": record.RespondingAgentID,
					"status":              record.Status,
					"forwarded":           true,
					"correlation_id":      correlationID,
					"target_agent_id":     strings.TrimSpace(nextTask.TargetAgentID),
				}, nil
			}
			if err := state.setTerminalAction(terminalAction); err != nil {
				return nil, err
			}
			if err := state.recordValidation(ctx, record); err != nil {
				return nil, err
			}
			return map[string]any{
				"validated":           true,
				"challenge_id":        record.ChallengeID,
				"requesting_agent":    record.RequestingAgent,
				"requesting_agent_id": record.RequestingAgentID,
				"responding_agent":    record.RespondingAgent,
				"responding_agent_id": record.RespondingAgentID,
				"status":              record.Status,
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
		Usage("Use immediately after another pipeline agent has responded to your challenge and before you decide whether to clarify, challenge again, hand off, or move toward closure with `finalize_pipeline`. Do not skip straight to another protocol action until the returned validation has been processed.").
		Requirement("When you are handling the response to one of your own challenges, your next protocol step must begin with `process_validation`. After that, choose the next concrete action from the processed evidence: another targeted challenge, a top-level handoff, or `finalize_pipeline` if the current inspector audit is complete and ready for closure.").
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
			entry := PipelineValidationProcessing{
				ChallengeID: challengeID,
				AgentType:   pipelineProtocolAgentType(ctx, cfg),
				Decision:    decision,
				Summary:     summary,
				NextTargets: normalizeStringList(params.NextTargets),
				Validation:  cloneValidationRecord(pending),
			}
			if err := state.recordValidationProcessing(ctx, entry); err != nil {
				return nil, err
			}
			return map[string]any{
				"processed":    true,
				"challenge_id": challengeID,
				"decision":     string(decision),
				"next_targets": normalizeStringList(params.NextTargets),
			}, nil
		}).
		Build()
}

func pipelineFinalizePipelineSkill(cfg PipelineProtocolSkillConfig) *skills.Skill {
	return skills.NewSkill("finalize_pipeline").
		Description("Run the inspector closure gate that determines whether final tester-backed acceptance is still required or the pipeline is ready for OT. Inspector only.").
		Domain("pipeline").
		Keywords("audit", "challenge", "review", "finalize", "pipeline").
		Priority(100).
		Usage("Invoke this only after you have completed the current inspector audit of the returned implementation and processed any challenge responses needed for that audit. Pass the strongest criteria, implementation, test, and challenge evidence into the call. This tool is the closure gate: it may request or recognize the final tester-backed acceptance audit, and if that audit has already passed it means the pipeline is ready for OT. Do not use it as the default replacement for a targeted challenge or an ordinary top-level handoff.").
		Requirement("Do not use ad hoc prose, local re-grading, or direct reroutes as a substitute for the closure path. If returned work is still unclear, challenge the responsible agent first. Once the current inspector audit is actually settled and any needed challenge responses have been consumed with `process_validation`, use `finalize_pipeline` to determine whether another loop is truly required or the pipeline is ready for OT.").
		Requirement("If this tool returns `ready_for_ot: true` or `must_handoff_to_ot: true`, your next terminal protocol action in this turn must be `handoff_to_ot`. Do not end the turn, summarize the handoff, pick another terminal action, or continue with other queued work or other pipelines first. This completed pipeline takes priority until `handoff_to_ot` is invoked.").
		Satisfies("Runs the pipeline closure gate, using the current audit evidence to issue or recognize the final tester-backed acceptance audit and, when that gate passes, requiring the inspector to call `handoff_to_ot` next.").
		StringParam("summary", "The inspector's current closure judgment and why the pipeline is or is not ready to move toward OT", true).
		ArrayParam("evidence_refs", "Criteria, tests, challenge responses, artifacts, and files the inspector used in the current closure decision", "string", false).
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
				return nil, fmt.Errorf("finalize_pipeline is only permitted for the pipeline inspector")
			}
			summary := strings.TrimSpace(params.Summary)
			if summary == "" {
				return nil, fmt.Errorf("summary is required")
			}
			state := PipelineProtocolStateFromContext(ctx)
			if state == nil {
				return nil, fmt.Errorf("pipeline protocol state not available")
			}
			snapshot := state.Snapshot()
			evidenceRefs := normalizeStringList(params.EvidenceRefs)
			if record, ok := finalizePipelineValidationReady(snapshot, state.ProcessedValidations()); ok {
				if err := state.recordReadyForOT(ctx, summary, normalizeStringList(append(append([]string(nil), evidenceRefs...), record.EvidenceRefs...)), record); err != nil {
					return nil, err
				}
				return map[string]any{
					"finalize_pipeline":         true,
					"ready_for_ot":              true,
					"must_handoff_to_ot":        true,
					"must_invoke_now":           "handoff_to_ot",
					"required_next_action":      "handoff_to_ot",
					"required_next_action_only": true,
					"next_required_action":      "handoff_to_ot",
					"agent_type":                agentType,
					"challenge_id":              strings.TrimSpace(record.ChallengeID),
					"evidence_refs":             normalizeStringList(append(append([]string(nil), evidenceRefs...), record.EvidenceRefs...)),
				}, nil
			}
			if finalizePipelineChallengePending(snapshot) {
				return map[string]any{
					"finalize_pipeline":      false,
					"verification_requested": true,
					"agent_type":             agentType,
					"challenge_id":           strings.TrimSpace(snapshot.PendingChallenge.ID),
				}, nil
			}
			challengeEvidence := resolvePipelineChallengeEvidence(ctx, cfg)
			if refusal := pipelineRepeatedChallengeRefusal(snapshot, agentType, []string{PipelineAgentTester}, challengeEvidence); refusal != nil {
				if err := state.setTerminalAction(&PipelineTurnAction{
					Type:         PipelineProtocolActionRefusal,
					AgentType:    agentType,
					TargetAgents: []string{PipelineAgentTester},
					Summary:      refusal.Reason,
				}); err != nil {
					return nil, err
				}
				return map[string]any{
					"finalize_pipeline": false,
					"refused":           true,
					"refused_by":        firstNonEmpty(strings.TrimSpace(refusal.RefusedBy), "pipeline-protocol"),
					"agent_type":        agentType,
					"reason":            refusal.Reason,
					"must_wait":         true,
					"resume_conditions": append([]string(nil), refusal.ResumeConditions...),
				}, nil
			}

			action := &PipelineTurnAction{
				Type:             PipelineProtocolActionHandoff,
				AgentType:        agentType,
				AgentID:          pipelineProtocolAgentID(ctx, cfg),
				CreatesChallenge: true,
				AuditLockPhase:   PipelineAuditPhaseFinalizing,
				TargetAgents:     []string{PipelineAgentTester},
				Mode:             PipelineTurnModeSingle,
				Reason:           "Run the inspector audit cycle before OT handoff.",
				Request:          "Audit the Engineer and Designer outputs as quality production code, not excessive or agentic slop. Verify correctness, robustness, performance, scope discipline, and production quality, and penalize unrelated code, premature abstraction, or verbosity. Also verify that all required tests are implemented and passing and that the tests add real value rather than noisy or low-quality surface area.",
				RequiredOutput: []string{
					"State whether all required tests are implemented and passing.",
					"Audit the engineer/designer implementation for correctness, robustness, performance, scope discipline, and production quality.",
					"Call out any excessive code, premature abstraction, verbosity, low-value tests, or agentic slop that should force another cycle.",
				},
				References:           append([]string{finalizePipelineVerificationReference}, evidenceRefs...),
				WorkspaceFingerprint: pipelineChallengeFingerprint(challengeEvidence),
			}
			if err := state.validateTerminalAction(action); err != nil {
				return nil, err
			}
			if task := PipelineTaskFromContext(ctx); task != nil {
				action.ChallengeID = nextPipelineChallengeID(task)
				if !pipelineProtocolRouteEnabled(ctx, cfg) {
					return nil, fmt.Errorf("pipeline handoff requires active stream routing context")
				}
				dispatch, err := dispatchPipelineHandoffSelection(ctx, cfg, state, task, action)
				if err != nil {
					return nil, err
				}
				if err := state.recordHandoffAction(ctx, action); err != nil {
					return nil, err
				}
				if err := state.setTerminalAction(action); err != nil {
					return nil, err
				}
				result := pipelineTurnSelectionResult(agentType, action, dispatch)
				result["finalize_pipeline"] = false
				result["verification_requested"] = true
				return result, nil
			}
			if err := state.recordHandoffAction(ctx, action); err != nil {
				return nil, err
			}
			if err := state.setTerminalAction(action); err != nil {
				return nil, err
			}
			result := pipelineTurnSelectionResult(agentType, action, nil)
			result["finalize_pipeline"] = false
			result["verification_requested"] = true
			return result, nil
		}).
		Build()
}

func pipelineHandoffOTSkill(cfg PipelineProtocolSkillConfig) *skills.Skill {
	return skills.NewSkill("handoff_to_ot").
		Description("Finalize an accepted pipeline and hand the result to Operational Transform for merge. Inspector only.").
		Domain("pipeline").
		Keywords("ot", "merge", "accept", "finalize", "pipeline").
		Priority(100).
		Usage("Use immediately after `finalize_pipeline` reports `ready_for_ot: true` / `must_handoff_to_ot: true`, or when the inspector has otherwise already determined the latest audit cycle passed and the pipeline should terminate successfully.").
		Requirement("When `finalize_pipeline` says the pipeline is ready for OT, invoke this immediately as the next terminal protocol action. Do not narrate the handoff instead of calling the tool, and do not continue with other queued work or other pipelines before invoking it.").
		Satisfies("Marks the pipeline as accepted and ready for OT merge, including the required terminal step after a passing `finalize_pipeline` result.").
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
			evidenceRefs := normalizeStringList(params.EvidenceRefs)
			action := &PipelineTurnAction{
				Type:         PipelineProtocolActionOT,
				AgentType:    agentType,
				AgentID:      pipelineProtocolAgentID(ctx, cfg),
				Summary:      summary,
				EvidenceRefs: evidenceRefs,
			}
			if err := state.recordHandoffToOT(ctx, action); err != nil {
				return nil, err
			}
			if err := state.setTerminalAction(&PipelineTurnAction{
				Type:         PipelineProtocolActionOT,
				AgentType:    agentType,
				AgentID:      pipelineProtocolAgentID(ctx, cfg),
				Summary:      summary,
				EvidenceRefs: evidenceRefs,
			}); err != nil {
				return nil, err
			}
			if task := pipelineTerminalUpdateTask(ctx, cfg); task != nil {
				PublishPipelineTaskSuccessUpdate(
					cfg.Route.eventBus(),
					agentType,
					task,
					summary,
					map[string]any{
						"summary":       summary,
						"evidence_refs": evidenceRefs,
					},
					PipelineTaskAttempt(task),
				)
			}
			return map[string]any{
				"handoff_to_ot": true,
				"agent_type":    agentType,
				"evidence_refs": evidenceRefs,
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

func pipelineProtocolAgentID(ctx context.Context, cfg PipelineProtocolSkillConfig) string {
	if cfg.AgentID != nil {
		if agentID := strings.TrimSpace(cfg.AgentID()); agentID != "" {
			return agentID
		}
	}
	if meta := LogMetaFromContext(ctx); strings.TrimSpace(meta.AgentID) != "" {
		return strings.TrimSpace(meta.AgentID)
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

func pipelineProtocolRouteEnabled(ctx context.Context, cfg PipelineProtocolSkillConfig) bool {
	if _, ok := StreamMetadataFromContext(ctx); !ok {
		return false
	}
	return cfg.Route.eventBus() != nil
}

func PipelineTurnTerminalAction(ctx context.Context) *PipelineTurnAction {
	state := PipelineProtocolStateFromContext(ctx)
	if state == nil {
		return nil
	}
	return state.TerminalAction()
}

func PipelineTurnTerminated(ctx context.Context) bool {
	return PipelineTurnTerminalAction(ctx) != nil
}

func (c PipelineProtocolRouteConfig) eventBus() guide.EventBus {
	if c.BusProvider != nil {
		if bus := c.BusProvider(); bus != nil {
			return bus
		}
	}
	return c.Bus
}

func (c PipelineProtocolRouteConfig) sessionID(task *PipelineTaskInput) string {
	if c.SessionID != nil {
		if sessionID := strings.TrimSpace(c.SessionID()); sessionID != "" {
			return sessionID
		}
	}
	if task != nil {
		return strings.TrimSpace(task.SessionID)
	}
	return ""
}

func dispatchPipelineProtocolTask(ctx context.Context, cfg PipelineProtocolSkillConfig, task *PipelineTaskInput, reason string) (string, error) {
	return dispatchPipelineProtocolTaskWithOptions(ctx, cfg, task, reason, pipelineProtocolRouteOptions{})
}

func dispatchPipelineProtocolTaskWithOptions(
	ctx context.Context,
	cfg PipelineProtocolSkillConfig,
	task *PipelineTaskInput,
	reason string,
	options pipelineProtocolRouteOptions,
) (string, error) {
	if task == nil {
		return "", fmt.Errorf("pipeline task is required")
	}
	stream, ok := StreamMetadataFromContext(ctx)
	if !ok || strings.TrimSpace(stream.CorrelationID) == "" || strings.TrimSpace(stream.SourceAgentID) == "" {
		return "", fmt.Errorf("pipeline handoff requires active stream context")
	}
	bus := cfg.Route.eventBus()
	if bus == nil {
		return "", fmt.Errorf("pipeline protocol route bus is not configured")
	}
	targetAgentID := firstNonEmpty(strings.TrimSpace(task.TargetAgentID), pipelineProtocolTargetAgentID(task.TaskID, task.AgentType))
	if targetAgentID == "" {
		return "", fmt.Errorf("pipeline target agent is unavailable for %s", task.AgentType)
	}
	task.TargetAgentID = targetAgentID
	payload, err := json.Marshal(task)
	if err != nil {
		return "", fmt.Errorf("encode pipeline task: %w", err)
	}
	metadata := pipelineRouteMetadata(task)
	branchCtx := ctx
	var branch InterAgentBranchHandle
	if options.InterAgentBranch != nil {
		branchCtx, branch = BeginInterAgentBranch(ctx, *options.InterAgentBranch)
		metadata = branch.ApplyMetadata(branchCtx, metadata)
	}
	if parentCID := strings.TrimSpace(options.ExplicitTopLevelTransferParentCID); parentCID != "" {
		metadata = RouteMetadataWithExplicitTopLevelTransfer(branchCtx, metadata, parentCID)
	}
	// Pipeline protocol streams should stay on the active requester channel
	// (typically orchestrator) and let the task router own any user-visible
	// mirroring. Inheriting an ancestor TUI stream override here duplicates the
	// same correlation on the UI response topic and can leave completed pipeline
	// rows visually stuck.
	metadata = guide.MetadataWithPreservedSourceStreamTarget(metadata)
	correlationID := "pipe_" + uuid.NewString()[:12]
	req := &guide.RouteRequest{
		CorrelationID:       correlationID,
		ParentCorrelationID: strings.TrimSpace(stream.CorrelationID),
		Input:               string(payload),
		TargetAgentID:       targetAgentID,
		ExplicitTarget:      true,
		SourceAgentID:       strings.TrimSpace(stream.SourceAgentID),
		SourceAgentName:     strings.TrimSpace(stream.SourceAgentID),
		SessionID:           cfg.Route.sessionID(task),
		Timestamp:           time.Now(),
		Metadata:            metadata,
	}
	// Pipeline handoffs transfer top-level turn ownership and therefore keep
	// only ParentCorrelationID lineage. Challenge routes opt into explicit
	// inter-agent branch metadata so the chat panel can render the challenged
	// worker as nested child work without changing handoff behavior.
	if err := bus.Publish(guide.TopicGuideRequests, guide.NewRequestMessage("", req)); err != nil {
		branch.Complete(branchCtx, "", "", err)
		return "", fmt.Errorf("publish pipeline handoff: %w", err)
	}
	PublishPipelineTaskRunningUpdate(
		bus,
		pipelineProtocolAgentType(ctx, cfg),
		task,
		firstNonEmpty(pipelineProtocolCurrentRequest(task), strings.TrimSpace(reason)),
		PipelineTaskAttempt(task),
	)
	if cfg.Route.PublishReroute != nil {
		cfg.Route.PublishReroute(ctx, strings.TrimSpace(task.AgentType), strings.TrimSpace(reason), correlationID)
	}
	return correlationID, nil
}

func pipelineChallengeRouteOptions(task *PipelineTaskInput, action *PipelineTurnAction) pipelineProtocolRouteOptions {
	if task == nil || action == nil || !action.CreatesChallenge {
		return pipelineProtocolRouteOptions{}
	}
	targetAgent := normalizePipelineAgentType(task.AgentType)
	if targetAgent == "" {
		return pipelineProtocolRouteOptions{}
	}
	return pipelineProtocolRouteOptions{
		InterAgentBranch: &InterAgentBranchSpec{
			Kind:          InterAgentToolEventKindChallenge,
			ToolName:      pipelineChallengeToolName(action),
			AgentTypes:    []string{targetAgent},
			Summary:       firstNonEmpty(strings.TrimSpace(action.Request), pipelineProtocolCurrentRequest(task)),
			ThreadKey:     pipelineChallengeThreadKey(task),
			SuccessStatus: InterAgentToolEventStatusPending,
			Args: map[string]any{
				"target_agent": targetAgent,
				"challenge_id": strings.TrimSpace(pipelineChallengeID(task)),
				"reason":       strings.TrimSpace(action.Reason),
				"request":      strings.TrimSpace(action.Request),
			},
		},
	}
}

func pipelineValidationRouteOptions(ctx context.Context) pipelineProtocolRouteOptions {
	stream, ok := StreamMetadataFromContext(ctx)
	if !ok {
		return pipelineProtocolRouteOptions{}
	}
	parentCID := strings.TrimSpace(stream.CorrelationID)
	if parentCID == "" {
		return pipelineProtocolRouteOptions{}
	}
	return pipelineProtocolRouteOptions{ExplicitTopLevelTransferParentCID: parentCID}
}

func pipelineChallengeToolName(action *PipelineTurnAction) string {
	if action == nil {
		return "challenge_agent"
	}
	if normalizePipelineAgentType(action.AgentType) == PipelineAgentInspector &&
		containsNormalizedString(action.References, finalizePipelineVerificationReference) {
		return "finalize_pipeline"
	}
	return "challenge_agent"
}

func dispatchPipelineHandoffSelection(
	ctx context.Context,
	cfg PipelineProtocolSkillConfig,
	state *PipelineProtocolState,
	task *PipelineTaskInput,
	action *PipelineTurnAction,
) (*pipelineDispatchSelection, error) {
	if state == nil {
		return nil, fmt.Errorf("pipeline protocol state not available")
	}
	tasks, err := buildPipelineHandoffTasks(state, task, action)
	if err != nil {
		return nil, err
	}
	dispatch := &pipelineDispatchSelection{
		CorrelationIDs: make([]string, 0, len(tasks)),
		TargetAgentIDs: make([]string, 0, len(tasks)),
	}
	for _, nextTask := range tasks {
		correlationID, err := dispatchPipelineProtocolTaskWithOptions(
			ctx,
			cfg,
			nextTask,
			action.Request,
			pipelineChallengeRouteOptions(nextTask, action),
		)
		if err != nil {
			return nil, err
		}
		dispatch.CorrelationIDs = append(dispatch.CorrelationIDs, correlationID)
		dispatch.TargetAgentIDs = append(dispatch.TargetAgentIDs, strings.TrimSpace(nextTask.TargetAgentID))
	}
	return dispatch, nil
}

func PublishPipelineHandoffReroute(
	bus guide.EventBus,
	channels *guide.AgentChannels,
	ctx context.Context,
	fromAgentID string,
	toAgentID string,
	reason string,
	newCorrelationID string,
) {
	if bus == nil || channels == nil {
		return
	}
	stream, ok := StreamMetadataFromContext(ctx)
	if !ok || strings.TrimSpace(stream.CorrelationID) == "" || strings.TrimSpace(newCorrelationID) == "" {
		return
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "pipeline handoff"
	}
	PublishStreamEvent(bus, channels, ctx, strings.TrimSpace(fromAgentID), &guide.StreamEvent{
		Type: guide.StreamEventReroute,
		Data: map[string]string{
			"from_agent":              strings.TrimSpace(fromAgentID),
			"to_agent":                strings.TrimSpace(toAgentID),
			"reason":                  reason,
			"original_correlation_id": strings.TrimSpace(stream.CorrelationID),
			"new_correlation_id":      strings.TrimSpace(newCorrelationID),
		},
		Timestamp: time.Now(),
	})
}

func buildPipelineHandoffTasks(state *PipelineProtocolState, task *PipelineTaskInput, action *PipelineTurnAction) ([]*PipelineTaskInput, error) {
	if state == nil {
		return nil, fmt.Errorf("pipeline protocol state not available")
	}
	if task == nil {
		return nil, fmt.Errorf("pipeline task not available")
	}
	if action == nil || len(action.TargetAgents) == 0 {
		return nil, fmt.Errorf("pipeline handoff requires at least one target agent")
	}
	snapshot := buildHandoffSnapshot(state, task, action)
	stage := pipelineStageForAgents(action.TargetAgents)
	nextTasks := make([]*PipelineTaskInput, 0, len(action.TargetAgents))
	for _, rawTarget := range action.TargetAgents {
		targetAgent := normalizePipelineAgentType(rawTarget)
		next := clonePipelineTaskInput(task)
		next.AgentType = targetAgent
		next.TargetAgentID = pipelineProtocolTargetAgentID(next.TaskID, targetAgent)
		if next.Context == nil {
			next.Context = map[string]any{}
		}
		next.Context["pipeline_stage"] = stage
		next.Context["pipeline_protocol"] = pipelineProtocolTaskSnapshotMap(snapshot)
		nextTasks = append(nextTasks, next)
	}
	return nextTasks, nil
}

func buildPipelineValidationTask(state *PipelineProtocolState, task *PipelineTaskInput, record *PipelineValidationRecord) (*PipelineTaskInput, error) {
	if state == nil {
		return nil, fmt.Errorf("pipeline protocol state not available")
	}
	if task == nil {
		return nil, fmt.Errorf("pipeline task not available")
	}
	if record == nil {
		return nil, fmt.Errorf("pipeline validation record is required")
	}
	if strings.TrimSpace(record.RequestingAgentID) == "" {
		return nil, fmt.Errorf("pipeline validation return is missing the exact requesting agent id; a replacement must explicitly advertise itself before pending validation can be rerouted")
	}
	next := clonePipelineTaskInput(task)
	requestingAgent := normalizePipelineAgentType(record.RequestingAgent)
	next.AgentType = requestingAgent
	next.TargetAgentID = strings.TrimSpace(record.RequestingAgentID)
	if next.Context == nil {
		next.Context = map[string]any{}
	}
	next.Context["pipeline_stage"] = pipelineStageForAgents([]string{requestingAgent})
	next.Context["pipeline_protocol"] = pipelineProtocolTaskSnapshotMap(buildValidationSnapshot(state, record))
	return next, nil
}

func buildHandoffSnapshot(state *PipelineProtocolState, task *PipelineTaskInput, action *PipelineTurnAction) *PipelineProtocolSnapshot {
	snapshot := materializePipelineProtocolSnapshot(state)
	if snapshot == nil {
		snapshot = &PipelineProtocolSnapshot{}
	}
	snapshot.ActiveAgents = append([]string(nil), action.TargetAgents...)
	snapshot.RequestedBy = action.AgentType
	snapshot.Mode = string(action.Mode)
	snapshot.CurrentRequest = strings.TrimSpace(action.Request)
	snapshot.AuditLock = nextPipelineAuditLock(snapshot.AuditLock, action)
	snapshot.PendingValidation = nil
	snapshot.PendingChallenge = nil
	if action.CreatesChallenge {
		snapshot.PendingChallenge = &PipelineProtocolChallenge{
			ID:                firstNonEmpty(strings.TrimSpace(action.ChallengeID), nextPipelineChallengeID(task)),
			RequestingAgent:   normalizePipelineAgentType(action.AgentType),
			RequestingAgentID: strings.TrimSpace(action.AgentID),
			TargetAgents:      append([]string(nil), action.TargetAgents...),
			Mode:              string(action.Mode),
			Reason:            strings.TrimSpace(action.Reason),
			Request:           strings.TrimSpace(action.Request),
			RequiredOutput:    append([]string(nil), action.RequiredOutput...),
			References:        append([]string(nil), action.References...),
		}
	}
	appendPipelineProtocolEvent(snapshot, PipelineProtocolEvent{
		Type:                 string(action.Type),
		AgentType:            action.AgentType,
		Targets:              append([]string(nil), action.TargetAgents...),
		Summary:              firstNonEmpty(strings.TrimSpace(action.Request), strings.TrimSpace(action.Summary)),
		CreatesChallenge:     action.CreatesChallenge,
		WorkspaceFingerprint: strings.TrimSpace(action.WorkspaceFingerprint),
	})
	return snapshot
}

type pipelineChallengeRefusal struct {
	RefusedBy        string
	AuditPhase       string
	Reason           string
	ResumeConditions []string
}

type pipelineChallengeEvidence struct {
	Fingerprint string
}

func pipelineAuditChallengeRefusal(snapshot *PipelineProtocolSnapshot, requestingAgent string, targets []string) *pipelineChallengeRefusal {
	if snapshot == nil || snapshot.AuditLock == nil {
		return nil
	}
	lock := snapshot.AuditLock
	if normalizePipelineAgentType(lock.OwnerAgent) != PipelineAgentInspector {
		return nil
	}
	if normalizePipelineAgentType(requestingAgent) == PipelineAgentInspector {
		return nil
	}
	if !containsPipelineAgent(targets, PipelineAgentInspector) {
		return nil
	}
	phase := strings.TrimSpace(lock.Phase)
	if phase == "" {
		phase = PipelineAuditPhaseFinalizing
	}
	reason := fmt.Sprintf(
		"Inspector is in %s audit lock. Challenges to Inspector are refused; stop work and wait for reactivation.",
		phase,
	)
	return &pipelineChallengeRefusal{
		RefusedBy:        PipelineAgentInspector,
		AuditPhase:       phase,
		Reason:           reason,
		ResumeConditions: auditRefusalResumeConditions(requestingAgent),
	}
}

func pipelineRepeatedChallengeRefusal(
	snapshot *PipelineProtocolSnapshot,
	requestingAgent string,
	targets []string,
	evidence *pipelineChallengeEvidence,
) *pipelineChallengeRefusal {
	currentFingerprint := pipelineChallengeFingerprint(evidence)
	if snapshot == nil || currentFingerprint == "" {
		return nil
	}
	previousFingerprint := recentChallengeFingerprint(snapshot.RecentEvents, requestingAgent, targets)
	if previousFingerprint == "" || previousFingerprint != currentFingerprint {
		return nil
	}
	targetLabel := strings.Join(normalizeStringList(targets), ", ")
	if strings.TrimSpace(targetLabel) == "" {
		targetLabel = "the same target"
	}
	return &pipelineChallengeRefusal{
		RefusedBy: "pipeline-protocol",
		Reason: fmt.Sprintf(
			"Repeated challenge to %s requires fresh workspace evidence, but the live task workspace state has not changed since the previous challenge on this directed pair.",
			targetLabel,
		),
		ResumeConditions: []string{
			fmt.Sprintf("Wait for the task workspace state to change before challenging %s again.", targetLabel),
			"Use `inspect_workspace_state` or `summarize_workspace_state` to reconcile live disk, global, and pipeline state before retrying.",
		},
	}
}

func resolvePipelineChallengeEvidence(ctx context.Context, cfg PipelineProtocolSkillConfig) *pipelineChallengeEvidence {
	task := PipelineTaskFromContext(ctx)
	if task == nil || cfg.WorkspaceViews == nil {
		return nil
	}
	views := cfg.WorkspaceViews()
	if views == nil {
		return nil
	}
	paths := collectTaskWorkspacePaths(task)
	if len(paths) == 0 {
		return nil
	}
	summary, err := views.SummarizePaths(ctx, paths, strings.TrimSpace(task.TaskID))
	if err != nil || summary == nil {
		return nil
	}
	payload, err := json.Marshal(summary)
	if err != nil || len(payload) == 0 {
		return nil
	}
	sum := sha256.Sum256(payload)
	return &pipelineChallengeEvidence{Fingerprint: hex.EncodeToString(sum[:])}
}

func pipelineChallengeFingerprint(evidence *pipelineChallengeEvidence) string {
	if evidence == nil {
		return ""
	}
	return strings.TrimSpace(evidence.Fingerprint)
}

func recentChallengeFingerprint(events []PipelineProtocolEvent, requestingAgent string, targets []string) string {
	requestingAgent = normalizePipelineAgentType(requestingAgent)
	targetKey := pipelineProtocolTargetKey(targets)
	if requestingAgent == "" || targetKey == "" {
		return ""
	}
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if !event.CreatesChallenge {
			continue
		}
		if normalizePipelineAgentType(event.AgentType) != requestingAgent {
			continue
		}
		if pipelineProtocolTargetKey(event.Targets) != targetKey {
			continue
		}
		return strings.TrimSpace(event.WorkspaceFingerprint)
	}
	return ""
}

func pipelineProtocolTargetKey(values []string) string {
	targets := normalizeStringList(values)
	for i, target := range targets {
		targets[i] = normalizePipelineAgentType(target)
	}
	targets = normalizeStringList(targets)
	if len(targets) == 0 {
		return ""
	}
	return strings.Join(targets, "|")
}

func auditRefusalResumeConditions(agentType string) []string {
	switch normalizePipelineAgentType(agentType) {
	case PipelineAgentTester:
		return []string{
			"Wait for a new challenge from inspector-pipeline.",
			"Wait for a handoff from inspector-pipeline.",
		}
	case PipelineAgentEngineer, PipelineAgentDesigner:
		return []string{
			"Wait for a new challenge from inspector-pipeline.",
			"Wait for a handoff from tester-pipeline.",
		}
	default:
		return []string{
			"Wait for reactivation from inspector-pipeline.",
		}
	}
}

func nextPipelineAuditLock(existing *PipelineAuditLock, action *PipelineTurnAction) *PipelineAuditLock {
	if action == nil {
		return clonePipelineAuditLock(existing)
	}
	if phase := strings.TrimSpace(action.AuditLockPhase); phase != "" {
		return &PipelineAuditLock{
			OwnerAgent: PipelineAgentInspector,
			Phase:      phase,
			Reason:     strings.TrimSpace(action.Reason),
		}
	}
	if normalizePipelineAgentType(action.AgentType) == PipelineAgentInspector {
		return nil
	}
	return clonePipelineAuditLock(existing)
}

func containsPipelineAgent(values []string, want string) bool {
	want = normalizePipelineAgentType(want)
	if want == "" {
		return false
	}
	for _, value := range values {
		if normalizePipelineAgentType(value) == want {
			return true
		}
	}
	return false
}

func buildValidationSnapshot(state *PipelineProtocolState, record *PipelineValidationRecord) *PipelineProtocolSnapshot {
	snapshot := materializePipelineProtocolSnapshot(state)
	if snapshot == nil {
		snapshot = &PipelineProtocolSnapshot{}
	}
	snapshot.ActiveAgents = []string{normalizePipelineAgentType(record.RequestingAgent)}
	snapshot.RequestedBy = normalizePipelineAgentType(record.RespondingAgent)
	snapshot.Mode = string(PipelineTurnModeSingle)
	snapshot.CurrentRequest = fmt.Sprintf(
		"Process validation response for challenge %s and decide the next handoff.",
		strings.TrimSpace(record.ChallengeID),
	)
	snapshot.PendingChallenge = nil
	snapshot.PendingValidation = cloneValidationRecord(record)
	appendPipelineProtocolEvent(snapshot, PipelineProtocolEvent{
		Type:      string(PipelineProtocolActionValidate),
		AgentType: record.RespondingAgent,
		Targets:   []string{record.RequestingAgent},
		Summary:   strings.TrimSpace(record.Summary),
	})
	return snapshot
}

func pipelineProtocolCurrentRequest(task *PipelineTaskInput) string {
	snapshot, err := PipelineProtocolSnapshotFromTask(task)
	if err != nil || snapshot == nil {
		return ""
	}
	return strings.TrimSpace(snapshot.CurrentRequest)
}

func materializePipelineProtocolSnapshot(state *PipelineProtocolState) *PipelineProtocolSnapshot {
	if state == nil {
		return nil
	}
	snapshot := clonePipelineProtocolSnapshot(state.Snapshot())
	if snapshot == nil {
		return nil
	}
	for _, entry := range state.ProcessedValidations() {
		appendPipelineProtocolEvent(snapshot, PipelineProtocolEvent{
			Type:      "process_validation",
			AgentType: entry.AgentType,
			Targets:   append([]string(nil), entry.NextTargets...),
			Summary:   strings.TrimSpace(entry.Summary),
		})
		if snapshot.PendingValidation != nil && strings.TrimSpace(snapshot.PendingValidation.ChallengeID) == strings.TrimSpace(entry.ChallengeID) {
			snapshot.PendingValidation = nil
		}
	}
	return snapshot
}

func appendPipelineProtocolEvent(snapshot *PipelineProtocolSnapshot, evt PipelineProtocolEvent) {
	if snapshot == nil {
		return
	}
	snapshot.RecentEvents = append(snapshot.RecentEvents, evt)
	if len(snapshot.RecentEvents) <= maxPipelineProtocolEventHistory {
		return
	}
	snapshot.RecentEvents = append([]PipelineProtocolEvent(nil), snapshot.RecentEvents[len(snapshot.RecentEvents)-maxPipelineProtocolEventHistory:]...)
}

func compactPipelineProtocolSnapshotForTask(snapshot *PipelineProtocolSnapshot) *PipelineProtocolSnapshot {
	out := clonePipelineProtocolSnapshot(snapshot)
	if out == nil {
		return nil
	}
	out.CurrentRequest = compactPipelineProtocolText(out.CurrentRequest, maxPipelineProtocolRequestLen)
	out.ActiveAgents = compactPipelineProtocolList(out.ActiveAgents, maxPipelineProtocolMaxTargetAgents, maxPipelineProtocolReferenceLen)
	if out.PendingChallenge != nil {
		out.PendingChallenge = compactPipelineProtocolChallenge(out.PendingChallenge)
	}
	if out.PendingValidation != nil {
		out.PendingValidation = compactPipelineValidationRecord(out.PendingValidation)
	}
	for i := range out.RecentEvents {
		out.RecentEvents[i] = compactPipelineProtocolEvent(out.RecentEvents[i])
	}
	return out
}

func compactPipelineProtocolChallenge(challenge *PipelineProtocolChallenge) *PipelineProtocolChallenge {
	if challenge == nil {
		return nil
	}
	out := *challenge
	out.TargetAgents = compactPipelineProtocolList(out.TargetAgents, maxPipelineProtocolMaxTargetAgents, maxPipelineProtocolReferenceLen)
	out.Reason = compactPipelineProtocolText(out.Reason, maxPipelineProtocolReasonLen)
	out.Request = compactPipelineProtocolText(out.Request, maxPipelineProtocolRequestLen)
	out.RequiredOutput = compactPipelineProtocolList(out.RequiredOutput, maxPipelineProtocolMaxReferences, maxPipelineProtocolReferenceLen)
	out.References = compactPipelineProtocolList(out.References, maxPipelineProtocolMaxReferences, maxPipelineProtocolReferenceLen)
	return &out
}

func compactPipelineValidationRecord(record *PipelineValidationRecord) *PipelineValidationRecord {
	if record == nil {
		return nil
	}
	out := *record
	out.Summary = compactPipelineProtocolText(out.Summary, maxPipelineProtocolSummaryLen)
	out.ChallengeRequest = compactPipelineProtocolText(out.ChallengeRequest, maxPipelineProtocolRequestLen)
	out.ChallengeReferences = compactPipelineProtocolList(out.ChallengeReferences, maxPipelineProtocolMaxReferences, maxPipelineProtocolReferenceLen)
	out.EvidenceRefs = compactPipelineProtocolList(out.EvidenceRefs, maxPipelineProtocolMaxReferences, maxPipelineProtocolReferenceLen)
	out.MissingInputs = compactPipelineProtocolList(out.MissingInputs, maxPipelineProtocolMaxReferences, maxPipelineProtocolReferenceLen)
	out.RecommendedNextAgents = compactPipelineProtocolList(out.RecommendedNextAgents, maxPipelineProtocolMaxTargetAgents, maxPipelineProtocolReferenceLen)
	return &out
}

func compactPipelineProtocolEvent(evt PipelineProtocolEvent) PipelineProtocolEvent {
	evt.Targets = compactPipelineProtocolList(evt.Targets, maxPipelineProtocolMaxRecentTargets, maxPipelineProtocolReferenceLen)
	evt.Summary = compactPipelineProtocolText(evt.Summary, maxPipelineProtocolEventSummaryLen)
	return evt
}

func compactPipelineProtocolList(values []string, limit int, itemLimit int) []string {
	if len(values) == 0 {
		return nil
	}
	if limit > 0 && len(values) > limit {
		values = values[:limit]
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		out = append(out, compactPipelineProtocolText(trimmed, itemLimit))
	}
	return out
}

func compactPipelineProtocolText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}

func nextPipelineChallengeID(task *PipelineTaskInput) string {
	base := "pipeline"
	if task != nil && strings.TrimSpace(task.TaskID) != "" {
		base = strings.TrimSpace(task.TaskID)
	}
	return fmt.Sprintf("%s-challenge-%s", base, uuid.NewString()[:8])
}

func pipelineChallengeID(task *PipelineTaskInput) string {
	if task == nil {
		return ""
	}
	snapshot, err := PipelineProtocolSnapshotFromTask(task)
	if err != nil || snapshot == nil {
		return ""
	}
	if snapshot.PendingValidation != nil {
		if challengeID := strings.TrimSpace(snapshot.PendingValidation.ChallengeID); challengeID != "" {
			return challengeID
		}
	}
	if snapshot.PendingChallenge != nil {
		if challengeID := strings.TrimSpace(snapshot.PendingChallenge.ID); challengeID != "" {
			return challengeID
		}
	}
	return ""
}

func pipelineChallengeThreadKey(task *PipelineTaskInput) string {
	if challengeID := pipelineChallengeID(task); challengeID != "" {
		return pipelineThreadPrefix + challengeID
	}
	return ""
}

func pipelineRouteMetadata(task *PipelineTaskInput) map[string]any {
	if task == nil {
		return nil
	}
	metadata := map[string]any{
		"pipeline_task": true,
		"task_id":       strings.TrimSpace(task.TaskID),
		"task_slug":     pipelineTaskContextString(task.Context, "task_slug"),
		"task_name":     pipelineTaskContextString(task.Context, "task_name"),
		"agent_type":    strings.TrimSpace(task.AgentType),
	}
	if dagID := strings.TrimSpace(task.DAGID); dagID != "" {
		metadata["dag_id"] = dagID
	}
	if nodeID := strings.TrimSpace(task.NodeID); nodeID != "" {
		metadata["node_id"] = nodeID
	}
	return metadata
}

func pipelineProtocolTargetAgentID(taskID, agentType string) string {
	taskID = strings.TrimSpace(taskID)
	agentType = strings.TrimSpace(normalizePipelineAgentType(agentType))
	return PipelineWorkerRoutingTarget(taskID, agentType)
}

func pipelineTerminalUpdateTask(ctx context.Context, cfg PipelineProtocolSkillConfig) *PipelineTaskInput {
	if task := PipelineTaskFromContext(ctx); task != nil {
		return task
	}
	stream, ok := StreamMetadataFromContext(ctx)
	if !ok {
		return nil
	}
	agentType := pipelineProtocolAgentType(ctx, cfg)
	taskID := firstNonEmpty(
		pipelineTaskMetadataString(stream.Metadata, "task_id"),
		pipelineTaskMetadataString(stream.Metadata, "pipeline_id"),
	)
	if taskID == "" || agentType == "" {
		return nil
	}
	contextData := map[string]any{
		"pipeline_stage": firstNonEmpty(
			pipelineTaskMetadataString(stream.Metadata, "pipeline_stage"),
			pipelineStageForAgents([]string{agentType}),
		),
	}
	if taskSlug := pipelineTaskMetadataString(stream.Metadata, "task_slug"); taskSlug != "" {
		contextData["task_slug"] = taskSlug
	}
	if taskName := pipelineTaskMetadataString(stream.Metadata, "task_name"); taskName != "" {
		contextData["task_name"] = taskName
	}
	return &PipelineTaskInput{
		NodeID:        firstNonEmpty(pipelineTaskMetadataString(stream.Metadata, "node_id"), taskID),
		DAGID:         pipelineTaskMetadataString(stream.Metadata, "dag_id"),
		TaskID:        taskID,
		AgentType:     agentType,
		TargetAgentID: pipelineProtocolTargetAgentID(taskID, agentType),
		SessionID:     cfg.Route.sessionID(nil),
		Context:       contextData,
	}
}

func pipelineStageForAgents(agentTypes []string) string {
	for _, agentType := range agentTypes {
		switch normalizePipelineAgentType(agentType) {
		case PipelineAgentInspector:
			return "inspect"
		case PipelineAgentTester:
			return "test"
		case PipelineAgentEngineer, PipelineAgentDesigner:
			return "execute"
		}
	}
	return "inspect"
}

func pipelineTaskContextString(ctx map[string]any, key string) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx[key].(string)
	return strings.TrimSpace(value)
}

func pipelineTaskMetadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	switch value := metadata[key].(type) {
	case string:
		return strings.TrimSpace(value)
	default:
		return ""
	}
}

func clonePipelineTaskInput(task *PipelineTaskInput) *PipelineTaskInput {
	if task == nil {
		return nil
	}
	out := *task
	if task.Context != nil {
		out.Context = make(map[string]any, len(task.Context))
		for key, value := range task.Context {
			out.Context[key] = value
		}
	}
	if task.ParentResults != nil {
		out.ParentResults = make(map[string]any, len(task.ParentResults))
		for key, value := range task.ParentResults {
			out.ParentResults[key] = value
		}
	}
	return &out
}

func cloneValidationRecord(record *PipelineValidationRecord) *PipelineValidationRecord {
	if record == nil {
		return nil
	}
	out := *record
	out.ChallengeReferences = append([]string(nil), record.ChallengeReferences...)
	out.EvidenceRefs = append([]string(nil), record.EvidenceRefs...)
	out.MissingInputs = append([]string(nil), record.MissingInputs...)
	out.RecommendedNextAgents = append([]string(nil), record.RecommendedNextAgents...)
	return &out
}

func clonePipelineValidationProcessing(entry PipelineValidationProcessing) PipelineValidationProcessing {
	entry.NextTargets = append([]string(nil), entry.NextTargets...)
	entry.Validation = cloneValidationRecord(entry.Validation)
	return entry
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func finalizePipelineValidationReady(snapshot *PipelineProtocolSnapshot, processed []PipelineValidationProcessing) (*PipelineValidationRecord, bool) {
	for i := len(processed) - 1; i >= 0; i-- {
		record, relevant := classifyProcessedFinalizePipelineValidation(processed[i].Validation)
		if !relevant {
			continue
		}
		if processed[i].Decision != PipelineValidationDecisionAccept {
			return nil, false
		}
		return record, true
	}
	if record, ok := classifyPendingFinalizePipelineValidation(snapshot.PendingValidation); ok {
		return record, true
	}
	return nil, false
}

func classifyPendingFinalizePipelineValidation(record *PipelineValidationRecord) (*PipelineValidationRecord, bool) {
	record, relevant := classifyFinalizePipelineValidationRecord(record)
	if !relevant {
		return nil, false
	}
	if strings.TrimSpace(record.Status) != string(PipelineValidationPassed) {
		return nil, false
	}
	return record, true
}

func classifyProcessedFinalizePipelineValidation(record *PipelineValidationRecord) (*PipelineValidationRecord, bool) {
	// Once Inspector has explicitly processed and accepted the tester-backed
	// final audit, that accept decision is authoritative even if Tester marked
	// the raw status partial or blocked due environmental caveats.
	return classifyFinalizePipelineValidationRecord(record)
}

func classifyFinalizePipelineValidationRecord(record *PipelineValidationRecord) (*PipelineValidationRecord, bool) {
	if record == nil {
		return nil, false
	}
	if normalizePipelineAgentType(record.RequestingAgent) != PipelineAgentInspector {
		return nil, false
	}
	if normalizePipelineAgentType(record.RespondingAgent) != PipelineAgentTester {
		return nil, false
	}
	if !containsNormalizedString(record.ChallengeReferences, finalizePipelineVerificationReference) {
		return nil, false
	}
	return cloneValidationRecord(record), true
}

func finalizePipelineChallengePending(snapshot *PipelineProtocolSnapshot) bool {
	if snapshot == nil || snapshot.PendingChallenge == nil {
		return false
	}
	challenge := snapshot.PendingChallenge
	if normalizePipelineAgentType(challenge.RequestingAgent) != PipelineAgentInspector {
		return false
	}
	if len(challenge.TargetAgents) != 1 || normalizePipelineAgentType(challenge.TargetAgents[0]) != PipelineAgentTester {
		return false
	}
	return containsNormalizedString(challenge.References, finalizePipelineVerificationReference)
}

func containsNormalizedString(values []string, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
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
	out.RecentEvents = make([]PipelineProtocolEvent, len(snapshot.RecentEvents))
	for i, evt := range snapshot.RecentEvents {
		out.RecentEvents[i] = evt
		out.RecentEvents[i].Targets = append([]string(nil), evt.Targets...)
	}
	out.AuditLock = clonePipelineAuditLock(snapshot.AuditLock)
	if snapshot.PendingChallenge != nil {
		challenge := *snapshot.PendingChallenge
		challenge.TargetAgents = append([]string(nil), snapshot.PendingChallenge.TargetAgents...)
		challenge.RequiredOutput = append([]string(nil), snapshot.PendingChallenge.RequiredOutput...)
		challenge.References = append([]string(nil), snapshot.PendingChallenge.References...)
		out.PendingChallenge = &challenge
	}
	if snapshot.PendingValidation != nil {
		record := *snapshot.PendingValidation
		record.ChallengeReferences = append([]string(nil), snapshot.PendingValidation.ChallengeReferences...)
		record.EvidenceRefs = append([]string(nil), snapshot.PendingValidation.EvidenceRefs...)
		record.MissingInputs = append([]string(nil), snapshot.PendingValidation.MissingInputs...)
		record.RecommendedNextAgents = append([]string(nil), snapshot.PendingValidation.RecommendedNextAgents...)
		out.PendingValidation = &record
	}
	return &out
}

func clonePipelineAuditLock(lock *PipelineAuditLock) *PipelineAuditLock {
	if lock == nil {
		return nil
	}
	copy := *lock
	return &copy
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
		record.ChallengeReferences = append([]string(nil), action.Validation.ChallengeReferences...)
		record.EvidenceRefs = append([]string(nil), action.Validation.EvidenceRefs...)
		record.MissingInputs = append([]string(nil), action.Validation.MissingInputs...)
		record.RecommendedNextAgents = append([]string(nil), action.Validation.RecommendedNextAgents...)
		out.Validation = &record
	}
	return &out
}
