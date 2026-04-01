package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/google/uuid"
)

const (
	GlobalReviewAgentInspector    = "inspector"
	GlobalReviewAgentTester       = "tester"
	GlobalReviewAgentArchitect    = "architect"
	GlobalReviewAgentOrchestrator = "orchestrator"
)

const (
	globalReviewMetadataKey        = "global_review_protocol"
	globalReviewMetadataEnabledKey = "global_review"
)

type GlobalReviewActionType string

const (
	GlobalReviewActionChallenge GlobalReviewActionType = "challenge"
	GlobalReviewActionValidate  GlobalReviewActionType = "validate"
	GlobalReviewActionCommit    GlobalReviewActionType = "commit_to_disk"
)

type GlobalReviewValidationStatus string

const (
	GlobalReviewValidationPassed  GlobalReviewValidationStatus = "passed"
	GlobalReviewValidationFailed  GlobalReviewValidationStatus = "failed"
	GlobalReviewValidationBlocked GlobalReviewValidationStatus = "blocked"
	GlobalReviewValidationUnclear GlobalReviewValidationStatus = "unclear"
	GlobalReviewValidationPartial GlobalReviewValidationStatus = "partial"
)

type GlobalReviewValidationDecision string

const (
	GlobalReviewValidationDecisionAccept  GlobalReviewValidationDecision = "accept"
	GlobalReviewValidationDecisionReject  GlobalReviewValidationDecision = "reject"
	GlobalReviewValidationDecisionClarify GlobalReviewValidationDecision = "clarify"
	GlobalReviewValidationDecisionLoop    GlobalReviewValidationDecision = "loop"
	GlobalReviewValidationDecisionConsult GlobalReviewValidationDecision = "consult"
)

type GlobalReviewChallenge struct {
	ID              string   `json:"id"`
	RequestingAgent string   `json:"requesting_agent"`
	TargetAgent     string   `json:"target_agent"`
	Reason          string   `json:"reason,omitempty"`
	Request         string   `json:"request,omitempty"`
	RequiredOutput  []string `json:"required_output,omitempty"`
	References      []string `json:"references,omitempty"`
}

type GlobalReviewValidationRecord struct {
	ChallengeID           string   `json:"challenge_id"`
	RequestingAgent       string   `json:"requesting_agent"`
	RespondingAgent       string   `json:"responding_agent"`
	Status                string   `json:"status"`
	Summary               string   `json:"summary"`
	ChallengeRequest      string   `json:"challenge_request,omitempty"`
	ChallengeReferences   []string `json:"challenge_references,omitempty"`
	EvidenceRefs          []string `json:"evidence_refs,omitempty"`
	MissingInputs         []string `json:"missing_inputs,omitempty"`
	RecommendedNextAgents []string `json:"recommended_next_agents,omitempty"`
}

type GlobalReviewEvent struct {
	Type        string `json:"type"`
	AgentType   string `json:"agent_type"`
	TargetAgent string `json:"target_agent,omitempty"`
	Summary     string `json:"summary,omitempty"`
}

type GlobalReviewSnapshot struct {
	ReviewID          string                        `json:"review_id,omitempty"`
	RequestedBy       string                        `json:"requested_by,omitempty"`
	CurrentRequest    string                        `json:"current_request,omitempty"`
	PendingChallenge  *GlobalReviewChallenge        `json:"pending_challenge,omitempty"`
	PendingValidation *GlobalReviewValidationRecord `json:"pending_validation,omitempty"`
	RecentEvents      []GlobalReviewEvent           `json:"recent_events,omitempty"`
}

type GlobalReviewValidationProcessing struct {
	ChallengeID string
	AgentType   string
	Decision    GlobalReviewValidationDecision
	Summary     string
	Validation  *GlobalReviewValidationRecord
}

type GlobalReviewTurnAction struct {
	Type             GlobalReviewActionType
	AgentType        string
	TargetAgent      string
	CreatesChallenge bool
	Reason           string
	Request          string
	RequiredOutput   []string
	References       []string
	ChallengeID      string
	Validation       *GlobalReviewValidationRecord
	Summary          string
	EvidenceRefs     []string
}

type GlobalReviewState struct {
	mu             sync.RWMutex
	sessionDir     string
	scopeID        string
	store          *durableProtocolLog
	snapshot       *GlobalReviewSnapshot
	terminalAction *GlobalReviewTurnAction
	processed      []GlobalReviewValidationProcessing
	requiredAction GlobalReviewActionType
	requiredReason string
	baseMetadata   map[string]any
}

type GlobalReviewRouteConfig struct {
	Bus         guide.EventBus
	BusProvider func() guide.EventBus
	SessionID   func() string
}

type GlobalReviewProtocolSkillConfig struct {
	AgentType      func() string
	Route          GlobalReviewRouteConfig
	WorkspaceViews func() versioning.WorkspaceViewAccess
}

type globalReviewStateKey struct{}

func WithGlobalReviewState(ctx context.Context, state *GlobalReviewState) context.Context {
	if state == nil {
		return ctx
	}
	return context.WithValue(ctx, globalReviewStateKey{}, state)
}

func WithGlobalReviewContext(ctx context.Context, metadata map[string]any) context.Context {
	if metadata == nil {
		metadata = map[string]any{}
	}
	if sessionID := strings.TrimSpace(versioning.SessionIDFromContext(ctx)); sessionID != "" {
		if _, ok := metadata["session_id"]; !ok {
			metadata = cloneGlobalReviewMetadata(metadata)
			if metadata == nil {
				metadata = map[string]any{}
			}
			metadata["session_id"] = sessionID
		}
	}
	if state := NewGlobalReviewStateFromMetadata(metadata); state != nil {
		return WithGlobalReviewState(ctx, state)
	}
	return ctx
}

func GlobalReviewStateFromContext(ctx context.Context) *GlobalReviewState {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(globalReviewStateKey{}).(*GlobalReviewState)
	return state
}

func NewGlobalReviewState(snapshot *GlobalReviewSnapshot, metadata map[string]any) *GlobalReviewState {
	return &GlobalReviewState{
		snapshot:     cloneGlobalReviewSnapshot(snapshot),
		baseMetadata: cloneGlobalReviewMetadata(metadata),
	}
}

func NewGlobalReviewStateFromMetadata(metadata map[string]any) *GlobalReviewState {
	if !globalReviewEnabled(metadata) {
		return nil
	}
	snapshot, _ := GlobalReviewSnapshotFromMetadata(metadata)
	state := NewGlobalReviewState(snapshot, metadata)
	scopeID := firstNonEmpty(strings.TrimSpace(snapshotReviewID(snapshot)), strings.TrimSpace(stringAny(metadata, "review_id")))
	sessionDir := strings.TrimSpace(stringAny(metadata, "session_dir"))
	sessionID := strings.TrimSpace(stringAny(metadata, "session_id"))
	if sessionDir == "" && sessionID != "" {
		sessionDir = filepath.Join(".sylk", "sessions", sessionID)
	}
	if scopeID == "" || sessionDir == "" {
		return state
	}
	store, err := openDurableProtocolLog(sessionDir, globalReviewNamespace, scopeID)
	if err != nil {
		return state
	}
	state.sessionDir = sessionDir
	state.scopeID = scopeID
	state.store = store
	if err := state.loadDurableProjection(); err != nil {
		_ = state.store.Close()
		state.store = nil
		state.sessionDir = ""
		state.scopeID = ""
		return state
	}
	if err := state.syncMailboxes(); err != nil {
		_ = state.store.Close()
		state.store = nil
		state.sessionDir = ""
		state.scopeID = ""
		return state
	}
	return state
}

func (s *GlobalReviewState) Snapshot() *GlobalReviewSnapshot {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneGlobalReviewSnapshot(s.snapshot)
}

func (s *GlobalReviewState) TerminalAction() *GlobalReviewTurnAction {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneGlobalReviewTurnAction(s.terminalAction)
}

func (s *GlobalReviewState) ProcessedValidations() []GlobalReviewValidationProcessing {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]GlobalReviewValidationProcessing, len(s.processed))
	for i, entry := range s.processed {
		out[i] = cloneGlobalReviewValidationProcessing(entry)
	}
	return out
}

func (s *GlobalReviewState) RequiredAction() (GlobalReviewActionType, string) {
	if s == nil {
		return "", ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.requiredAction, strings.TrimSpace(s.requiredReason)
}

func (s *GlobalReviewState) Close() error {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store.Close()
}

func CloseGlobalReviewState(ctx context.Context) error {
	state := GlobalReviewStateFromContext(ctx)
	if state == nil {
		return nil
	}
	return state.Close()
}

func (s *GlobalReviewState) BaseMetadata() map[string]any {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneGlobalReviewMetadata(s.baseMetadata)
}

func (s *GlobalReviewState) setTerminalAction(action *GlobalReviewTurnAction) error {
	if s == nil || action == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.requiredAction != "" && action.Type != s.requiredAction {
		return fmt.Errorf("%s", requiredGlobalReviewActionMessageLocked(s.requiredAction, s.requiredReason))
	}
	if s.terminalAction != nil {
		return fmt.Errorf("global review turn already selected %s", s.terminalAction.Type)
	}
	s.terminalAction = cloneGlobalReviewTurnAction(action)
	if s.requiredAction != "" && action.Type == s.requiredAction {
		s.requiredAction = ""
		s.requiredReason = ""
	}
	return nil
}

func (s *GlobalReviewState) addProcessedValidation(entry GlobalReviewValidationProcessing) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.processed = append(s.processed, cloneGlobalReviewValidationProcessing(entry))
}

func (s *GlobalReviewState) requireTerminalAction(action GlobalReviewActionType, reason string) {
	if s == nil || strings.TrimSpace(string(action)) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requiredAction = action
	s.requiredReason = strings.TrimSpace(reason)
}

func requiredGlobalReviewActionMessageLocked(action GlobalReviewActionType, reason string) string {
	message := fmt.Sprintf("Before ending this global review turn, you must invoke `%s`.", strings.TrimSpace(string(action)))
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		return message + " " + trimmed
	}
	return message
}

func GlobalReviewTurnTerminated(ctx context.Context) bool {
	state := GlobalReviewStateFromContext(ctx)
	return state != nil && state.TerminalAction() != nil
}

func ValidateGlobalReviewCompletion(ctx context.Context, agentType string) error {
	state := GlobalReviewStateFromContext(ctx)
	if state == nil {
		return nil
	}
	if action, reason := state.RequiredAction(); action != "" {
		return fmt.Errorf("%s", requiredGlobalReviewActionMessageLocked(action, reason))
	}
	if state.TerminalAction() != nil {
		return nil
	}

	snapshot := materializeGlobalReviewSnapshot(state)
	agentType = normalizeGlobalReviewAgent(agentType)
	switch agentType {
	case GlobalReviewAgentInspector:
		if snapshot != nil && snapshot.PendingValidation != nil {
			return fmt.Errorf("Before ending this global inspector turn, call `process_global_validation` and then decide the next move with `challenge_global_tester`, `challenge_orchestrator`, `challenge_architect`, `finalize_global_review`, or `commit_to_disk`.")
		}
		return fmt.Errorf("Before ending this global inspector turn, use `challenge_global_tester`, `challenge_orchestrator`, `challenge_architect`, `finalize_global_review`, or `commit_to_disk` to record the next review step.")
	case GlobalReviewAgentTester, GlobalReviewAgentArchitect, GlobalReviewAgentOrchestrator:
		if snapshot != nil && snapshot.PendingChallenge != nil && normalizeGlobalReviewAgent(snapshot.PendingChallenge.TargetAgent) == agentType {
			return fmt.Errorf("Before ending this global review turn, answer the active challenge with `validate_global_review`.")
		}
	}
	return nil
}

func GlobalReviewSnapshotFromMetadata(metadata map[string]any) (*GlobalReviewSnapshot, error) {
	if len(metadata) == 0 {
		return nil, nil
	}
	raw, ok := metadata[globalReviewMetadataKey]
	if !ok || raw == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var snapshot GlobalReviewSnapshot
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func GlobalReviewSnapshotMap(snapshot *GlobalReviewSnapshot) map[string]any {
	if snapshot == nil {
		return nil
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		return nil
	}
	return out
}

func GlobalReviewMetadata(base map[string]any, snapshot *GlobalReviewSnapshot) map[string]any {
	metadata := cloneGlobalReviewMetadata(base)
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata[globalReviewMetadataEnabledKey] = true
	metadata[globalReviewMetadataKey] = GlobalReviewSnapshotMap(snapshot)
	return metadata
}

func NewGlobalReviewProtocolSkills(cfg GlobalReviewProtocolSkillConfig) []*skills.Skill {
	switch normalizeGlobalReviewAgent(globalReviewAgentType(context.Background(), cfg)) {
	case GlobalReviewAgentInspector:
		return []*skills.Skill{
			globalReviewChallengeTesterSkill(cfg),
			globalReviewChallengeOrchestratorSkill(cfg),
			globalReviewChallengeArchitectSkill(cfg),
			globalReviewProcessValidationSkill(cfg),
			globalReviewFinalizeSkill(cfg),
			globalReviewCommitToDiskSkill(cfg),
		}
	case GlobalReviewAgentTester, GlobalReviewAgentArchitect, GlobalReviewAgentOrchestrator:
		return []*skills.Skill{
			globalReviewValidateSkill(cfg),
		}
	default:
		return nil
	}
}

func globalReviewChallengeTesterSkill(cfg GlobalReviewProtocolSkillConfig) *skills.Skill {
	return skills.NewSkill("challenge_global_tester").
		Description("Challenge the global tester to validate merged work against the entire architect plan, preserved behavior, and repository quality bar.").
		Domain("global_review").
		Keywords("global", "review", "tester", "challenge", "validation").
		Priority(100).
		Usage("Use when the global inspector needs the global tester to audit or execute merged-state validation before final acceptance.").
		Requirement("Provide the concrete concern, the exact request, and any required evidence the tester must return.").
		Satisfies("Creates the next strict global-review handoff from inspector to tester.").
		StringParam("reason", "Why the tester challenge is necessary now", true).
		StringParam("request", "Concrete validation work the global tester must perform", true).
		ArrayParam("required_output", "What the tester must return", "string", false).
		ArrayParam("references", "Relevant files, evidence, or criteria", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Reason         string   `json:"reason"`
				Request        string   `json:"request"`
				RequiredOutput []string `json:"required_output"`
				References     []string `json:"references"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			action := &GlobalReviewTurnAction{
				Type:             GlobalReviewActionChallenge,
				AgentType:        normalizeGlobalReviewAgent(globalReviewAgentType(ctx, cfg)),
				TargetAgent:      GlobalReviewAgentTester,
				CreatesChallenge: true,
				Reason:           strings.TrimSpace(params.Reason),
				Request:          strings.TrimSpace(params.Request),
				RequiredOutput:   normalizeStringList(params.RequiredOutput),
				References:       normalizeStringList(params.References),
			}
			return issueGlobalReviewChallenge(ctx, cfg, action)
		}).
		Build()
}

func globalReviewChallengeArchitectSkill(cfg GlobalReviewProtocolSkillConfig) *skills.Skill {
	return skills.NewSkill("challenge_architect").
		Description("Challenge the architect when the plan, rationale, or chosen approach appears defective, unclear, or materially suboptimal. Do not use this for DAG or workflow progress.").
		Domain("global_review").
		Keywords("global", "review", "architect", "challenge", "plan").
		Priority(100).
		Usage("Use when the global inspector concludes the audit requires direct architect response about the plan itself: defects in the plan, missing rationale, ambiguity, or stronger alternatives. For workflow progress, DAG state, or merged-work execution progress, use `challenge_orchestrator` instead.").
		Requirement("Provide the concrete plan defect or concern, the requested architect response about the plan or rationale, and supporting references.").
		Satisfies("Creates a strict architect challenge in the global review flow about plan quality, rationale, or needed plan revision.").
		StringParam("reason", "Why the architect challenge is necessary now", true).
		StringParam("request", "Concrete response or revision explanation required from the architect", true).
		ArrayParam("required_output", "What the architect must return", "string", false).
		ArrayParam("references", "Relevant files, evidence, or criteria", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Reason         string   `json:"reason"`
				Request        string   `json:"request"`
				RequiredOutput []string `json:"required_output"`
				References     []string `json:"references"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			action := &GlobalReviewTurnAction{
				Type:             GlobalReviewActionChallenge,
				AgentType:        normalizeGlobalReviewAgent(globalReviewAgentType(ctx, cfg)),
				TargetAgent:      GlobalReviewAgentArchitect,
				CreatesChallenge: true,
				Reason:           strings.TrimSpace(params.Reason),
				Request:          strings.TrimSpace(params.Request),
				RequiredOutput:   normalizeStringList(params.RequiredOutput),
				References:       normalizeStringList(params.References),
			}
			return issueGlobalReviewChallenge(ctx, cfg, action)
		}).
		Build()
}

func globalReviewChallengeOrchestratorSkill(cfg GlobalReviewProtocolSkillConfig) *skills.Skill {
	return skills.NewSkill("challenge_orchestrator").
		Description("Challenge the orchestrator for authoritative DAG, workflow, task, pipeline, and execution-progress state needed by the global review.").
		Domain("global_review").
		Keywords("global", "review", "orchestrator", "challenge", "dag", "workflow", "progress", "pipeline").
		Priority(100).
		Usage("Use when the global inspector concludes the audit requires authoritative execution-state information such as DAG progress, workflow completion, pending tasks, active pipelines, or current merged-work progress. Do not route these questions to the architect.").
		Requirement("Provide the concrete progress/state concern, the exact orchestrator response needed, and any relevant references such as DAG IDs, workflow IDs, task IDs, or pipeline IDs.").
		Satisfies("Creates a strict orchestrator challenge in the global review flow so the inspector can validate real execution-state data.").
		StringParam("reason", "Why the orchestrator challenge is necessary now", true).
		StringParam("request", "Concrete workflow, DAG, pipeline, or progress information required from the orchestrator", true).
		ArrayParam("required_output", "What the orchestrator must return", "string", false).
		ArrayParam("references", "Relevant DAG, workflow, task, or pipeline identifiers", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Reason         string   `json:"reason"`
				Request        string   `json:"request"`
				RequiredOutput []string `json:"required_output"`
				References     []string `json:"references"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			action := &GlobalReviewTurnAction{
				Type:             GlobalReviewActionChallenge,
				AgentType:        normalizeGlobalReviewAgent(globalReviewAgentType(ctx, cfg)),
				TargetAgent:      GlobalReviewAgentOrchestrator,
				CreatesChallenge: true,
				Reason:           strings.TrimSpace(params.Reason),
				Request:          strings.TrimSpace(params.Request),
				RequiredOutput:   normalizeStringList(params.RequiredOutput),
				References:       normalizeStringList(params.References),
			}
			return issueGlobalReviewChallenge(ctx, cfg, action)
		}).
		Build()
}

func issueGlobalReviewChallenge(
	ctx context.Context,
	cfg GlobalReviewProtocolSkillConfig,
	action *GlobalReviewTurnAction,
) (map[string]any, error) {
	state := GlobalReviewStateFromContext(ctx)
	if state == nil {
		return nil, fmt.Errorf("global review state not available")
	}
	if normalizeGlobalReviewAgent(action.AgentType) != GlobalReviewAgentInspector {
		return nil, fmt.Errorf("%s is only permitted for the global inspector", actionNameForTarget(action.TargetAgent))
	}
	if strings.TrimSpace(action.Reason) == "" {
		return nil, fmt.Errorf("reason is required")
	}
	if strings.TrimSpace(action.Request) == "" {
		return nil, fmt.Errorf("request is required")
	}
	if snapshot := state.Snapshot(); snapshot != nil && snapshot.PendingValidation != nil {
		return nil, fmt.Errorf("process the pending validation before issuing another global review challenge")
	}
	action.ChallengeID = nextGlobalReviewChallengeID(state.Snapshot())
	dispatch, err := dispatchGlobalReviewSelection(ctx, cfg, state, action)
	if err != nil {
		return nil, err
	}
	if err := state.recordChallenge(ctx, action); err != nil {
		return nil, err
	}
	if err := state.setTerminalAction(action); err != nil {
		return nil, err
	}
	return map[string]any{
		"selected":       true,
		"agent_type":     action.AgentType,
		"target_agent":   action.TargetAgent,
		"challenge_id":   action.ChallengeID,
		"forwarded":      true,
		"correlation_id": dispatch.CorrelationID,
	}, nil
}

func globalReviewValidateSkill(cfg GlobalReviewProtocolSkillConfig) *skills.Skill {
	return skills.NewSkill("validate_global_review").
		Description("Answer an active global-review challenge from the global inspector with a structured validation response and evidence.").
		Domain("global_review").
		Keywords("global", "review", "validate", "response", "evidence").
		Priority(100).
		Usage("Use when the global inspector challenged you to validate merged-state behavior, execution progress/state, plan fit, or approach quality.").
		Requirement("Your response must match the active challenge ID and return concrete evidence or missing inputs.").
		Satisfies("Returns the structured response that hands control back to the global inspector.").
		StringParam("challenge_id", "The active global-review challenge identifier", true).
		StringParam("requesting_agent", "The agent that issued the challenge", true).
		EnumParam("status", "Validation status", []string{
			string(GlobalReviewValidationPassed),
			string(GlobalReviewValidationFailed),
			string(GlobalReviewValidationBlocked),
			string(GlobalReviewValidationUnclear),
			string(GlobalReviewValidationPartial),
		}, true).
		StringParam("summary", "What you validated, what happened, and why", true).
		ArrayParam("evidence_refs", "Files, tests, artifacts, or commands supporting the response", "string", false).
		ArrayParam("missing_inputs", "What is still unclear or missing", "string", false).
		ArrayParam("recommended_next_agents", "Optional suggested next agents", "string", false).
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
			state := GlobalReviewStateFromContext(ctx)
			if state == nil {
				return nil, fmt.Errorf("global review state not available")
			}
			snapshot := state.Snapshot()
			challenge := snapshot.PendingChallenge
			if challenge == nil {
				return nil, fmt.Errorf("no active global-review challenge is waiting for validation")
			}
			respondingAgent := normalizeGlobalReviewAgent(globalReviewAgentType(ctx, cfg))
			if respondingAgent == "" {
				return nil, fmt.Errorf("global review responding agent is unavailable")
			}
			if normalizeGlobalReviewAgent(challenge.TargetAgent) != respondingAgent {
				return nil, fmt.Errorf("the active global-review challenge is not assigned to %s", respondingAgent)
			}
			if strings.TrimSpace(params.ChallengeID) != strings.TrimSpace(challenge.ID) {
				return nil, fmt.Errorf("challenge_id %q does not match the active global-review challenge", strings.TrimSpace(params.ChallengeID))
			}
			requestingAgent := normalizeGlobalReviewAgent(params.RequestingAgent)
			if requestingAgent == "" || requestingAgent != normalizeGlobalReviewAgent(challenge.RequestingAgent) {
				return nil, fmt.Errorf("requesting_agent %q does not match the active global-review challenge", params.RequestingAgent)
			}
			status := strings.TrimSpace(params.Status)
			if !isGlobalReviewValidationStatus(status) {
				return nil, fmt.Errorf("status %q is invalid", params.Status)
			}
			summary := strings.TrimSpace(params.Summary)
			if summary == "" {
				return nil, fmt.Errorf("summary is required")
			}
			record := &GlobalReviewValidationRecord{
				ChallengeID:           challenge.ID,
				RequestingAgent:       requestingAgent,
				RespondingAgent:       respondingAgent,
				Status:                status,
				Summary:               summary,
				ChallengeRequest:      strings.TrimSpace(challenge.Request),
				ChallengeReferences:   normalizeStringList(challenge.References),
				EvidenceRefs:          normalizeStringList(params.EvidenceRefs),
				MissingInputs:         normalizeStringList(params.MissingInputs),
				RecommendedNextAgents: normalizeStringList(params.RecommendedNextAgents),
			}
			correlationID, err := dispatchGlobalReviewValidation(ctx, cfg, state, record)
			if err != nil {
				return nil, err
			}
			if err := state.recordValidation(ctx, record); err != nil {
				return nil, err
			}
			if err := state.setTerminalAction(&GlobalReviewTurnAction{
				Type:        GlobalReviewActionValidate,
				AgentType:   respondingAgent,
				ChallengeID: record.ChallengeID,
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
				"forwarded":        true,
				"correlation_id":   correlationID,
				"target_agent":     requestingAgent,
			}, nil
		}).
		Build()
}

func globalReviewProcessValidationSkill(cfg GlobalReviewProtocolSkillConfig) *skills.Skill {
	return skills.NewSkill("process_global_validation").
		Description("Acknowledge and interpret a global-review validation response before choosing the next step.").
		Domain("global_review").
		Keywords("global", "review", "process", "validation").
		Priority(99).
		Usage("Use after the global tester or architect responds and before issuing another challenge, finalizing, or committing to disk.").
		Satisfies("Records how the global inspector interpreted the received validation response.").
		StringParam("challenge_id", "The validation challenge being processed", true).
		EnumParam("decision", "How you interpreted the response", []string{
			string(GlobalReviewValidationDecisionAccept),
			string(GlobalReviewValidationDecisionReject),
			string(GlobalReviewValidationDecisionClarify),
			string(GlobalReviewValidationDecisionLoop),
			string(GlobalReviewValidationDecisionConsult),
		}, true).
		StringParam("summary", "Why you accepted, rejected, or need follow-up", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				ChallengeID string `json:"challenge_id"`
				Decision    string `json:"decision"`
				Summary     string `json:"summary"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if normalizeGlobalReviewAgent(globalReviewAgentType(ctx, cfg)) != GlobalReviewAgentInspector {
				return nil, fmt.Errorf("process_global_validation is only permitted for the global inspector")
			}
			state := GlobalReviewStateFromContext(ctx)
			if state == nil {
				return nil, fmt.Errorf("global review state not available")
			}
			snapshot := state.Snapshot()
			pending := snapshot.PendingValidation
			if pending == nil {
				return nil, fmt.Errorf("no pending global-review validation is waiting to be processed")
			}
			challengeID := strings.TrimSpace(params.ChallengeID)
			if challengeID == "" || challengeID != strings.TrimSpace(pending.ChallengeID) {
				return nil, fmt.Errorf("challenge_id %q does not match the pending global-review validation", challengeID)
			}
			decision := GlobalReviewValidationDecision(strings.TrimSpace(params.Decision))
			if !isGlobalReviewValidationDecision(string(decision)) {
				return nil, fmt.Errorf("decision %q is invalid", params.Decision)
			}
			summary := strings.TrimSpace(params.Summary)
			if summary == "" {
				return nil, fmt.Errorf("summary is required")
			}
			entry := GlobalReviewValidationProcessing{
				ChallengeID: challengeID,
				AgentType:   GlobalReviewAgentInspector,
				Decision:    decision,
				Summary:     summary,
				Validation:  cloneGlobalReviewValidationRecord(pending),
			}
			if err := state.recordValidationProcessing(ctx, entry); err != nil {
				return nil, err
			}
			return map[string]any{
				"processed":    true,
				"challenge_id": challengeID,
				"decision":     string(decision),
			}, nil
		}).
		Build()
}

func globalReviewFinalizeSkill(cfg GlobalReviewProtocolSkillConfig) *skills.Skill {
	return skills.NewSkill("finalize_global_review").
		Description("Run or recognize the current global inspector audit cycle that gates the next tester pass or commit-to-disk.").
		Domain("global_review").
		Keywords("global", "review", "finalize", "audit", "commit").
		Priority(100).
		Usage("Invoke this when the current merged state should either be challenged again or be promoted toward commit-to-disk.").
		Requirement("If this returns ready_for_commit or must_commit_to_disk, your next terminal action in this turn must be `commit_to_disk`.").
		Satisfies("Issues the tester challenge for the current review stage or recognizes that the tester-backed audit already passed and commit-to-disk is now required.").
		StringParam("summary", "Why the merged work is ready for final global review or commit", true).
		ArrayParam("evidence_refs", "Files, tests, and artifacts supporting the audit", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Summary      string   `json:"summary"`
				EvidenceRefs []string `json:"evidence_refs"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if normalizeGlobalReviewAgent(globalReviewAgentType(ctx, cfg)) != GlobalReviewAgentInspector {
				return nil, fmt.Errorf("finalize_global_review is only permitted for the global inspector")
			}
			summary := strings.TrimSpace(params.Summary)
			if summary == "" {
				return nil, fmt.Errorf("summary is required")
			}
			state := GlobalReviewStateFromContext(ctx)
			if state == nil {
				return nil, fmt.Errorf("global review state not available")
			}
			finalWholePlanReview := globalReviewIsFinal(state.BaseMetadata())
			snapshot := state.Snapshot()
			evidenceRefs := normalizeStringList(params.EvidenceRefs)
			if record, ok := finalizeGlobalReviewValidationReady(snapshot, state.ProcessedValidations()); ok {
				if err := state.recordReadyForCommit(ctx, summary, normalizeStringList(append(append([]string(nil), evidenceRefs...), record.EvidenceRefs...)), record); err != nil {
					return nil, err
				}
				return map[string]any{
					"finalize_global_review":    true,
					"ready_for_commit":          true,
					"must_commit_to_disk":       true,
					"required_next_action":      "commit_to_disk",
					"required_next_action_only": true,
					"challenge_id":              record.ChallengeID,
					"evidence_refs":             normalizeStringList(append(append([]string(nil), evidenceRefs...), record.EvidenceRefs...)),
				}, nil
			}
			if snapshot != nil && snapshot.PendingValidation != nil {
				return nil, fmt.Errorf("process the pending global-review validation before finalizing")
			}
			if snapshot != nil && snapshot.PendingChallenge != nil {
				return map[string]any{
					"finalize_global_review": false,
					"verification_requested": true,
					"challenge_id":           strings.TrimSpace(snapshot.PendingChallenge.ID),
				}, nil
			}
			action := &GlobalReviewTurnAction{
				Type:             GlobalReviewActionChallenge,
				AgentType:        GlobalReviewAgentInspector,
				TargetAgent:      GlobalReviewAgentTester,
				CreatesChallenge: true,
				Reason:           globalReviewFinalizeReason(finalWholePlanReview),
				Request:          globalReviewFinalizeRequest(finalWholePlanReview),
				RequiredOutput: []string{
					globalReviewFinalizeRequiredOutput(finalWholePlanReview),
					"Call out correctness, robustness, performance, style-fit, and regression risks.",
					"Identify stronger alternative implementations if the current one is not the best fit.",
					"State whether the work should be handed back for more changes instead of committed to disk.",
				},
				References: evidenceRefs,
			}
			return issueGlobalReviewChallenge(ctx, cfg, action)
		}).
		Build()
}

func globalReviewCommitToDiskSkill(cfg GlobalReviewProtocolSkillConfig) *skills.Skill {
	return skills.NewSkill("commit_to_disk").
		Description("Request explicit approval, then promote the merged session-global draft to disk. Global inspector only.").
		Domain("global_review").
		Keywords("global", "review", "commit", "disk", "approval").
		Priority(100).
		Usage("Use only after the whole-plan global review has passed and the merged draft is ready to become committed disk state.").
		Requirement("This is the terminal action after a passing final global review. It must go through the existing Guardian approval mechanics before writing to disk.").
		Satisfies("Applies the approved disk promotion and closes the strict global review loop.").
		StringParam("summary", "Why the merged work is ready to be committed to disk", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Summary string `json:"summary"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if normalizeGlobalReviewAgent(globalReviewAgentType(ctx, cfg)) != GlobalReviewAgentInspector {
				return nil, fmt.Errorf("commit_to_disk is only permitted for the global inspector")
			}
			summary := strings.TrimSpace(params.Summary)
			if summary == "" {
				return nil, fmt.Errorf("summary is required")
			}
			state := GlobalReviewStateFromContext(ctx)
			if state == nil {
				return nil, fmt.Errorf("global review state not available")
			}
			if _, ok := finalizeGlobalReviewValidationReady(state.Snapshot(), state.ProcessedValidations()); !ok {
				return nil, fmt.Errorf("commit_to_disk requires a passing tester-backed global review first")
			}
			views := cfg.WorkspaceViews()
			if views == nil {
				return nil, fmt.Errorf("workspace views are unavailable")
			}
			svfs := versioning.SessionForWorkspaceViews(ctx, views)
			if svfs == nil {
				return nil, fmt.Errorf("session VFS is unavailable for commit_to_disk")
			}
			authReq := commandapproval.Request{
				Command:        fmt.Sprintf("commit_to_disk --reason %q", summary),
				ToolName:       "commit_to_disk",
				AgentID:        GlobalReviewAgentInspector,
				AgentType:      GlobalReviewAgentInspector,
				SessionID:      versioning.SessionIDFromContext(ctx),
				ApprovalPolicy: commandapproval.ApprovalPolicyExact,
			}
			PopulateCommandApprovalScope(ctx, &authReq)
			if _, err := commandapproval.Authorize(ctx, commandapproval.NewEvaluator(nil), authReq); err != nil {
				return nil, WrapApprovalDenied(authReq.ToolName, err)
			}
			result, err := versioning.PromoteSessionDraft(ctx, svfs, versioning.PromotionRequest{
				Mode:   versioning.PromotionModeExplicit,
				Reason: summary,
			})
			if err != nil {
				return nil, err
			}
			action := &GlobalReviewTurnAction{
				Type:      GlobalReviewActionCommit,
				AgentType: GlobalReviewAgentInspector,
				Summary:   summary,
			}
			if err := state.recordCommitToDisk(ctx, action); err != nil {
				return nil, err
			}
			if err := state.setTerminalAction(&GlobalReviewTurnAction{
				Type:      GlobalReviewActionCommit,
				AgentType: GlobalReviewAgentInspector,
				Summary:   summary,
			}); err != nil {
				return nil, err
			}
			return map[string]any{
				"committed":     true,
				"files_written": result.FilesWritten,
				"files_deleted": result.FilesDeleted,
			}, nil
		}).
		Build()
}

type globalReviewDispatchSelection struct {
	CorrelationID string
}

func dispatchGlobalReviewSelection(
	ctx context.Context,
	cfg GlobalReviewProtocolSkillConfig,
	state *GlobalReviewState,
	action *GlobalReviewTurnAction,
) (*globalReviewDispatchSelection, error) {
	stream, ok := StreamMetadataFromContext(ctx)
	if !ok || strings.TrimSpace(stream.CorrelationID) == "" || strings.TrimSpace(stream.SourceAgentID) == "" {
		return nil, fmt.Errorf("global review handoff requires active stream context")
	}
	bus := cfg.Route.eventBus()
	if bus == nil {
		return nil, fmt.Errorf("global review route bus is not configured")
	}
	snapshot := buildGlobalReviewHandoffSnapshot(state, action)
	metadata := buildGlobalReviewRouteMetadata(state, snapshot, action.TargetAgent)
	branchCtx, branch := BeginInterAgentBranch(ctx, InterAgentBranchSpec{
		Kind:          InterAgentToolEventKindChallenge,
		ToolName:      actionNameForTarget(strings.TrimSpace(action.TargetAgent)),
		AgentTypes:    []string{strings.TrimSpace(action.TargetAgent)},
		Summary:       action.Request,
		ThreadKey:     globalReviewThreadPrefix + strings.TrimSpace(action.ChallengeID),
		SuccessStatus: InterAgentToolEventStatusPending,
		Args: map[string]any{
			"target_agent": strings.TrimSpace(action.TargetAgent),
			"challenge_id": strings.TrimSpace(action.ChallengeID),
			"request":      action.Request,
		},
	})
	metadata = RouteMetadataWithExplicitInterAgentBranch(branchCtx, metadata, InterAgentBranchMetadata{
		ThreadKey: globalReviewThreadPrefix + strings.TrimSpace(action.ChallengeID),
		Kind:      InterAgentToolEventKindChallenge,
	})
	prompt := buildGlobalReviewChallengePrompt(action.TargetAgent, action, metadata)
	correlationID := "global_review_" + uuid.NewString()[:12]
	sourceAgentID := globalReviewVisibleSourceAgentID(stream)
	req := &guide.RouteRequest{
		CorrelationID:       correlationID,
		ParentCorrelationID: strings.TrimSpace(stream.CorrelationID),
		Input:               prompt,
		TargetAgentID:       strings.TrimSpace(action.TargetAgent),
		ExplicitTarget:      true,
		SourceAgentID:       sourceAgentID,
		SourceAgentName:     sourceAgentID,
		SessionID:           cfg.Route.sessionID(versioning.SessionIDFromContext(ctx)),
		Timestamp:           time.Now().UTC(),
		Metadata:            metadata,
	}
	if err := bus.Publish(guide.TopicGuideRequests, guide.NewRequestMessage("", req)); err != nil {
		branch.Complete(branchCtx, "", "", err)
		return nil, fmt.Errorf("publish global review handoff: %w", err)
	}
	publishGlobalReviewReroute(bus, ctx, action.AgentType, action.TargetAgent, action.Request, correlationID)
	return &globalReviewDispatchSelection{CorrelationID: correlationID}, nil
}

func dispatchGlobalReviewValidation(
	ctx context.Context,
	cfg GlobalReviewProtocolSkillConfig,
	state *GlobalReviewState,
	record *GlobalReviewValidationRecord,
) (string, error) {
	stream, ok := StreamMetadataFromContext(ctx)
	if !ok || strings.TrimSpace(stream.CorrelationID) == "" || strings.TrimSpace(stream.SourceAgentID) == "" {
		return "", fmt.Errorf("global review validation requires active stream context")
	}
	bus := cfg.Route.eventBus()
	if bus == nil {
		return "", fmt.Errorf("global review route bus is not configured")
	}
	snapshot := buildGlobalReviewValidationSnapshot(state, record)
	metadata := buildGlobalReviewRouteMetadata(state, snapshot, record.RequestingAgent)
	branchCtx, branch := BeginInterAgentBranch(ctx, InterAgentBranchSpec{
		Kind:       InterAgentToolEventKindChallenge,
		ToolName:   "validate_global_review",
		AgentTypes: []string{strings.TrimSpace(record.RequestingAgent)},
		Summary:    record.Summary,
		ThreadKey:  globalReviewThreadPrefix + strings.TrimSpace(record.ChallengeID),
		Args: map[string]any{
			"target_agent": strings.TrimSpace(record.RequestingAgent),
			"challenge_id": strings.TrimSpace(record.ChallengeID),
			"summary":      record.Summary,
		},
	})
	metadata = RouteMetadataWithExplicitInterAgentBranch(branchCtx, metadata, InterAgentBranchMetadata{
		ThreadKey: globalReviewThreadPrefix + strings.TrimSpace(record.ChallengeID),
		Kind:      InterAgentToolEventKindChallenge,
	})
	prompt := buildGlobalReviewValidationPrompt(record, metadata)
	correlationID := "global_review_" + uuid.NewString()[:12]
	sourceAgentID := globalReviewVisibleSourceAgentID(stream)
	req := &guide.RouteRequest{
		CorrelationID:       correlationID,
		ParentCorrelationID: strings.TrimSpace(stream.CorrelationID),
		Input:               prompt,
		TargetAgentID:       strings.TrimSpace(record.RequestingAgent),
		ExplicitTarget:      true,
		SourceAgentID:       sourceAgentID,
		SourceAgentName:     sourceAgentID,
		SessionID:           cfg.Route.sessionID(versioning.SessionIDFromContext(ctx)),
		Timestamp:           time.Now().UTC(),
		Metadata:            metadata,
	}
	if err := bus.Publish(guide.TopicGuideRequests, guide.NewRequestMessage("", req)); err != nil {
		branch.Complete(branchCtx, "", "", err)
		return "", fmt.Errorf("publish global review validation: %w", err)
	}
	publishGlobalReviewReroute(bus, ctx, record.RespondingAgent, record.RequestingAgent, record.Summary, correlationID)
	return correlationID, nil
}

func buildGlobalReviewRouteMetadata(state *GlobalReviewState, snapshot *GlobalReviewSnapshot, targetAgent string) map[string]any {
	metadata := GlobalReviewMetadata(state.BaseMetadata(), snapshot)
	metadata["agent_type"] = strings.TrimSpace(targetAgent)
	if obligations := globalReviewRouteObligations(state, snapshot, targetAgent); len(obligations) > 0 {
		metadata["global_review_obligations"] = obligations
	} else {
		delete(metadata, "global_review_obligations")
	}
	return metadata
}

func globalReviewRouteObligations(state *GlobalReviewState, snapshot *GlobalReviewSnapshot, targetAgent string) []map[string]any {
	targetAgent = normalizeGlobalReviewAgent(targetAgent)
	if targetAgent == "" {
		return nil
	}
	if state != nil {
		if obligations := state.CurrentAgentObligations(targetAgent); len(obligations) > 0 {
			return obligations
		}
	}
	if snapshot == nil {
		return nil
	}
	if challenge := snapshot.PendingChallenge; challenge != nil && normalizeGlobalReviewAgent(challenge.TargetAgent) == targetAgent {
		return []map[string]any{{
			"action":  "validate_global_review",
			"summary": "Answer the active global-review challenge before ending the turn.",
		}}
	}
	if validation := snapshot.PendingValidation; validation != nil && normalizeGlobalReviewAgent(validation.RequestingAgent) == targetAgent {
		return []map[string]any{{
			"action":  "process_global_validation",
			"summary": "Process the pending validation response before selecting the next review step.",
		}}
	}
	return nil
}

func buildGlobalReviewHandoffSnapshot(state *GlobalReviewState, action *GlobalReviewTurnAction) *GlobalReviewSnapshot {
	snapshot := materializeGlobalReviewSnapshot(state)
	if snapshot == nil {
		snapshot = &GlobalReviewSnapshot{}
	}
	snapshot.RequestedBy = normalizeGlobalReviewAgent(action.AgentType)
	snapshot.CurrentRequest = strings.TrimSpace(action.Request)
	snapshot.PendingValidation = nil
	snapshot.PendingChallenge = nil
	if action.CreatesChallenge {
		snapshot.PendingChallenge = &GlobalReviewChallenge{
			ID:              strings.TrimSpace(action.ChallengeID),
			RequestingAgent: normalizeGlobalReviewAgent(action.AgentType),
			TargetAgent:     normalizeGlobalReviewAgent(action.TargetAgent),
			Reason:          strings.TrimSpace(action.Reason),
			Request:         strings.TrimSpace(action.Request),
			RequiredOutput:  append([]string(nil), action.RequiredOutput...),
			References:      append([]string(nil), action.References...),
		}
	}
	appendGlobalReviewEvent(snapshot, GlobalReviewEvent{
		Type:        string(action.Type),
		AgentType:   normalizeGlobalReviewAgent(action.AgentType),
		TargetAgent: normalizeGlobalReviewAgent(action.TargetAgent),
		Summary:     firstNonEmpty(strings.TrimSpace(action.Request), strings.TrimSpace(action.Summary)),
	})
	return snapshot
}

func buildGlobalReviewValidationSnapshot(state *GlobalReviewState, record *GlobalReviewValidationRecord) *GlobalReviewSnapshot {
	snapshot := materializeGlobalReviewSnapshot(state)
	if snapshot == nil {
		snapshot = &GlobalReviewSnapshot{}
	}
	snapshot.RequestedBy = normalizeGlobalReviewAgent(record.RespondingAgent)
	snapshot.CurrentRequest = fmt.Sprintf("Process global review validation response for challenge %s.", strings.TrimSpace(record.ChallengeID))
	snapshot.PendingChallenge = nil
	snapshot.PendingValidation = cloneGlobalReviewValidationRecord(record)
	appendGlobalReviewEvent(snapshot, GlobalReviewEvent{
		Type:        string(GlobalReviewActionValidate),
		AgentType:   normalizeGlobalReviewAgent(record.RespondingAgent),
		TargetAgent: normalizeGlobalReviewAgent(record.RequestingAgent),
		Summary:     strings.TrimSpace(record.Summary),
	})
	return snapshot
}

func materializeGlobalReviewSnapshot(state *GlobalReviewState) *GlobalReviewSnapshot {
	if state == nil {
		return nil
	}
	snapshot := cloneGlobalReviewSnapshot(state.Snapshot())
	if snapshot == nil {
		return nil
	}
	for _, entry := range state.ProcessedValidations() {
		appendGlobalReviewEvent(snapshot, GlobalReviewEvent{
			Type:      "process_global_validation",
			AgentType: normalizeGlobalReviewAgent(entry.AgentType),
			Summary:   strings.TrimSpace(entry.Summary),
		})
		if snapshot.PendingValidation != nil && strings.TrimSpace(snapshot.PendingValidation.ChallengeID) == strings.TrimSpace(entry.ChallengeID) {
			snapshot.PendingValidation = nil
		}
	}
	return snapshot
}

func buildGlobalReviewChallengePrompt(targetAgent string, action *GlobalReviewTurnAction, metadata map[string]any) string {
	normalizedTarget := normalizeGlobalReviewAgent(targetAgent)
	var lines []string
	switch normalizedTarget {
	case GlobalReviewAgentArchitect:
		lines = append(lines, "Global inspector challenge for the architect.")
	case GlobalReviewAgentOrchestrator:
		lines = append(lines, "Global inspector challenge for the orchestrator.")
	default:
		lines = append(lines, "Global inspector challenge for the global tester.")
	}
	lines = append(lines,
		"This request is part of the strict global review loop over merged global state.",
		"Do not answer narratively without recording the result. End this turn with `validate_global_review`.",
	)
	appendGlobalReviewContextLines(&lines, metadata)
	appendGlobalReviewCheckpointGuard(&lines, normalizedTarget, metadata)
	if strings.TrimSpace(action.Reason) != "" {
		lines = append(lines, "Why this challenge is necessary now: "+strings.TrimSpace(action.Reason))
	}
	if strings.TrimSpace(action.Request) != "" {
		lines = append(lines, "Concrete request: "+strings.TrimSpace(action.Request))
	}
	if len(action.RequiredOutput) > 0 {
		lines = append(lines, "Required output:")
		for _, item := range action.RequiredOutput {
			lines = append(lines, "- "+item)
		}
	}
	if len(action.References) > 0 {
		lines = append(lines, "References:")
		for _, item := range action.References {
			lines = append(lines, "- "+item)
		}
	}
	lines = append(lines, fmt.Sprintf("Use `validate_global_review` with challenge_id %s to hand the result back to the global inspector.", strings.TrimSpace(action.ChallengeID)))
	return strings.Join(lines, "\n")
}

func buildGlobalReviewValidationPrompt(record *GlobalReviewValidationRecord, metadata map[string]any) string {
	normalizedResponder := normalizeGlobalReviewAgent(record.RespondingAgent)
	lines := []string{
		fmt.Sprintf("Global review validation response from %s.", normalizedResponder),
		"This request is part of the strict global review loop over merged global state.",
		"Use `process_global_validation` before choosing the next action.",
	}
	appendGlobalReviewContextLines(&lines, metadata)
	appendGlobalReviewCheckpointGuard(&lines, normalizedResponder, metadata)
	lines = append(lines,
		"Challenge ID: "+strings.TrimSpace(record.ChallengeID),
		"Challenge request: "+strings.TrimSpace(record.ChallengeRequest),
		"Validation status: "+strings.TrimSpace(record.Status),
		"Validation summary: "+strings.TrimSpace(record.Summary),
	)
	if len(record.EvidenceRefs) > 0 {
		lines = append(lines, "Evidence refs:")
		for _, item := range record.EvidenceRefs {
			lines = append(lines, "- "+item)
		}
	}
	if len(record.MissingInputs) > 0 {
		lines = append(lines, "Missing inputs:")
		for _, item := range record.MissingInputs {
			lines = append(lines, "- "+item)
		}
	}
	lines = append(lines, "After `process_global_validation`, decide whether to challenge again, challenge the orchestrator, challenge the architect, finalize the global review, or commit to disk.")
	return strings.Join(lines, "\n")
}

func appendGlobalReviewCheckpointGuard(lines *[]string, agentType string, metadata map[string]any) {
	if lines == nil {
		return
	}
	normalizedAgent := normalizeGlobalReviewAgent(agentType)
	switch normalizedAgent {
	case GlobalReviewAgentArchitect:
		*lines = append(*lines,
			"Architect scope rule: answer about plan quality, rationale, and needed plan revision. The orchestrator remains authoritative for DAG/workflow progress, but the architect may freely consult the orchestrator whenever execution-state context helps assess, defend, or revise the plan.",
		)
	case GlobalReviewAgentOrchestrator:
		*lines = append(*lines,
			"Orchestrator scope rule: answer only with authoritative execution-state information from DAG, workflow, task, pipeline, and buffer state. Do not reinterpret or revise the architect plan.",
		)
	}
	if globalReviewIsFinal(metadata) {
		return
	}
	switch normalizedAgent {
	case GlobalReviewAgentArchitect:
		*lines = append(*lines,
			"Checkpoint rule for the architect: do not call a later planned task missing solely because it is absent from the current merged state. At this stage, remaining workflow tasks may still be pending or in progress.",
			"Only treat planned work as missing during a checkpoint if the current review metadata says it should already exist now, the merged state falsely claims it is already complete, or the current implementation blocks or contradicts the remaining plan.",
		)
	case GlobalReviewAgentTester:
		*lines = append(*lines,
			"Checkpoint rule for the global tester: do not fail the merged state solely because later planned tasks are not merged yet. Judge the current checkpoint, its regressions, and whether it keeps the remaining plan viable.",
		)
	}
}

func appendGlobalReviewContextLines(lines *[]string, metadata map[string]any) {
	if lines == nil {
		return
	}
	stage := strings.TrimSpace(stringAny(metadata, "global_review_stage"))
	if stage != "" {
		*lines = append(*lines, "Review stage: "+stage)
	}
	totalTasks := intAny(metadata, "workflow_total_tasks")
	completedTasks := intAny(metadata, "workflow_completed_tasks")
	failedTasks := intAny(metadata, "workflow_failed_tasks")
	remainingTasks := intAny(metadata, "workflow_remaining_tasks")
	if totalTasks > 0 {
		*lines = append(*lines, fmt.Sprintf("Workflow progress: %d/%d tasks completed, %d failed, %d remaining.", completedTasks, totalTasks, failedTasks, remainingTasks))
	}
	if stage == "final" {
		*lines = append(*lines, "This is the final whole-plan review. Missing planned work is a defect unless the plan was explicitly revised.")
	} else if stage == "checkpoint" {
		*lines = append(*lines, "This is a progressive checkpoint review. Future planned work that has not been merged yet is pending, not missing. Audit whether the current merged state is correct, robust, stylistically sound, and still supports the remaining plan.")
	}
	if summary := strings.TrimSpace(stringAny(metadata, "acceptance_summary")); summary != "" {
		*lines = append(*lines, "Merged acceptance summary: "+summary)
	}
	if description := strings.TrimSpace(stringAny(metadata, "task_description")); description != "" {
		*lines = append(*lines, "Task description: "+description)
	}
	if version := strings.TrimSpace(stringAny(metadata, "global_vfs_version")); version != "" {
		*lines = append(*lines, "Global VFS version: "+version)
	}
	if planID := strings.TrimSpace(stringAny(metadata, "plan_id")); planID != "" {
		*lines = append(*lines, "Architect plan ID: "+planID)
	}
	if planFilePath := strings.TrimSpace(stringAny(metadata, "plan_file_path")); planFilePath != "" {
		*lines = append(*lines, "Architect plan file: "+planFilePath)
	}
	if criteriaSnapshot := strings.TrimSpace(stringAny(metadata, "task_criteria_snapshot")); criteriaSnapshot != "" {
		*lines = append(*lines, "Task criteria snapshot:", criteriaSnapshot)
	}
	if planSnapshot := strings.TrimSpace(stringAny(metadata, "plan_snapshot")); planSnapshot != "" {
		*lines = append(*lines, "Architect plan snapshot:", planSnapshot)
	}
	if obligations := summarizeGlobalReviewProtocolObligations(metadata["global_review_obligations"]); len(obligations) > 0 {
		*lines = append(*lines, "Protocol obligations:")
		for _, obligation := range obligations {
			*lines = append(*lines, "- "+obligation)
		}
	}
	if files := stringSliceAny(metadata, "affected_files"); len(files) > 0 {
		*lines = append(*lines, "Affected files:")
		for _, item := range files {
			*lines = append(*lines, "- "+item)
		}
	}
}

func globalReviewStageFromMetadata(metadata map[string]any) string {
	stage := strings.TrimSpace(stringAny(metadata, "global_review_stage"))
	if stage == "" {
		return "checkpoint"
	}
	return stage
}

func globalReviewIsFinal(metadata map[string]any) bool {
	return strings.EqualFold(globalReviewStageFromMetadata(metadata), "final")
}

func globalReviewFinalizeReason(finalWholePlanReview bool) string {
	if finalWholePlanReview {
		return "Run the adversarial whole-plan global tester audit before committing merged work to disk."
	}
	return "Run the adversarial global tester audit for this merged checkpoint before deciding whether the current state is safe and strong enough to commit to disk."
}

func globalReviewFinalizeRequest(finalWholePlanReview bool) string {
	if finalWholePlanReview {
		return "Audit the merged implementation against the entire architect plan, preserved user intent, repository style, historical failure modes, and system-level correctness. Challenge anything sloppy, overbuilt, fragile, slow, stylistically off-pattern, or insufficiently tested. Compare against stronger alternatives where relevant and treat the work as guilty until it proves correctness, robustness, elegance, and performance."
	}
	return "Audit the current merged checkpoint against the work that should exist at this stage of the architect plan, preserved user intent, repository style, historical failure modes, and system-level correctness. Future planned work that has not been merged yet is pending, not missing, but drift, regressions, slop, brittle choices, or design decisions that endanger the remaining plan are defects. Compare against stronger alternatives where relevant and treat the work as guilty until it proves correctness, robustness, elegance, and performance for the current checkpoint."
}

func globalReviewFinalizeRequiredOutput(finalWholePlanReview bool) string {
	if finalWholePlanReview {
		return "State whether the merged work satisfies the whole plan as completely and optimally as possible."
	}
	return "State whether the merged checkpoint satisfies the portion of the plan that should be true now and keeps the remaining plan on track."
}

func summarizeGlobalReviewProtocolObligations(raw any) []string {
	var items []map[string]any
	switch typed := raw.(type) {
	case []map[string]any:
		items = append(items, typed...)
	case []any:
		items = make([]map[string]any, 0, len(typed))
		for _, entry := range typed {
			if item, _ := entry.(map[string]any); item != nil {
				items = append(items, item)
			}
		}
	default:
		return nil
	}
	if len(items) == 0 {
		return nil
	}
	lines := make([]string, 0, len(items))
	for _, item := range items {
		action, _ := item["action"].(string)
		summary, _ := item["summary"].(string)
		action = strings.TrimSpace(action)
		summary = strings.TrimSpace(summary)
		switch {
		case action == "" && summary == "":
			continue
		case action == "":
			lines = append(lines, summary)
		case summary == "":
			lines = append(lines, action)
		default:
			lines = append(lines, action+": "+summary)
		}
	}
	return lines
}

func intAny(metadata map[string]any, key string) int {
	if len(metadata) == 0 {
		return 0
	}
	value, ok := metadata[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int8:
		return int(typed)
	case int16:
		return int(typed)
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float32:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func publishGlobalReviewReroute(bus guide.EventBus, ctx context.Context, fromAgent, toAgent, reason, newCorrelationID string) {
	if bus == nil {
		return
	}
	stream, ok := StreamMetadataFromContext(ctx)
	if !ok || strings.TrimSpace(stream.CorrelationID) == "" || strings.TrimSpace(newCorrelationID) == "" {
		return
	}
	PublishStreamEvent(bus, guide.NewAgentChannels(strings.TrimSpace(normalizeGlobalReviewAgent(fromAgent)), strings.TrimSpace(normalizeGlobalReviewAgent(fromAgent))), ctx, strings.TrimSpace(normalizeGlobalReviewAgent(fromAgent)), &guide.StreamEvent{
		Type: guide.StreamEventReroute,
		Data: map[string]string{
			"from_agent":              strings.TrimSpace(normalizeGlobalReviewAgent(fromAgent)),
			"to_agent":                strings.TrimSpace(normalizeGlobalReviewAgent(toAgent)),
			"reason":                  firstNonEmpty(strings.TrimSpace(reason), "global review handoff"),
			"original_correlation_id": strings.TrimSpace(stream.CorrelationID),
			"new_correlation_id":      strings.TrimSpace(newCorrelationID),
		},
		Timestamp: time.Now().UTC(),
	})
}

func globalReviewAgentType(ctx context.Context, cfg GlobalReviewProtocolSkillConfig) string {
	if cfg.AgentType != nil {
		return normalizeGlobalReviewAgent(cfg.AgentType())
	}
	return ""
}

func globalReviewVisibleSourceAgentID(stream StreamContext) string {
	if strings.EqualFold(strings.TrimSpace(stream.SourceAgentID), "tui") {
		return "tui"
	}
	return "tui"
}

func normalizeGlobalReviewAgent(agentType string) string {
	switch strings.TrimSpace(strings.ToLower(agentType)) {
	case GlobalReviewAgentInspector:
		return GlobalReviewAgentInspector
	case GlobalReviewAgentTester:
		return GlobalReviewAgentTester
	case GlobalReviewAgentArchitect:
		return GlobalReviewAgentArchitect
	case GlobalReviewAgentOrchestrator:
		return GlobalReviewAgentOrchestrator
	default:
		return strings.TrimSpace(agentType)
	}
}

func globalReviewEnabled(metadata map[string]any) bool {
	if len(metadata) == 0 {
		return false
	}
	if enabled, ok := metadata[globalReviewMetadataEnabledKey].(bool); ok {
		return enabled
	}
	_, ok := metadata[globalReviewMetadataKey]
	return ok
}

func cloneGlobalReviewSnapshot(snapshot *GlobalReviewSnapshot) *GlobalReviewSnapshot {
	if snapshot == nil {
		return nil
	}
	out := *snapshot
	out.PendingChallenge = cloneGlobalReviewChallenge(snapshot.PendingChallenge)
	out.PendingValidation = cloneGlobalReviewValidationRecord(snapshot.PendingValidation)
	out.RecentEvents = append([]GlobalReviewEvent(nil), snapshot.RecentEvents...)
	return &out
}

func cloneGlobalReviewChallenge(challenge *GlobalReviewChallenge) *GlobalReviewChallenge {
	if challenge == nil {
		return nil
	}
	out := *challenge
	out.RequiredOutput = append([]string(nil), challenge.RequiredOutput...)
	out.References = append([]string(nil), challenge.References...)
	return &out
}

func cloneGlobalReviewValidationRecord(record *GlobalReviewValidationRecord) *GlobalReviewValidationRecord {
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

func cloneGlobalReviewTurnAction(action *GlobalReviewTurnAction) *GlobalReviewTurnAction {
	if action == nil {
		return nil
	}
	out := *action
	out.RequiredOutput = append([]string(nil), action.RequiredOutput...)
	out.References = append([]string(nil), action.References...)
	out.EvidenceRefs = append([]string(nil), action.EvidenceRefs...)
	out.Validation = cloneGlobalReviewValidationRecord(action.Validation)
	return &out
}

func cloneGlobalReviewValidationProcessing(entry GlobalReviewValidationProcessing) GlobalReviewValidationProcessing {
	entry.Validation = cloneGlobalReviewValidationRecord(entry.Validation)
	return entry
}

func cloneGlobalReviewMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return nil
	}
	out := make(map[string]any, len(metadata))
	for key, value := range metadata {
		out[key] = value
	}
	return out
}

func appendGlobalReviewEvent(snapshot *GlobalReviewSnapshot, evt GlobalReviewEvent) {
	if snapshot == nil {
		return
	}
	snapshot.RecentEvents = append(snapshot.RecentEvents, evt)
	if len(snapshot.RecentEvents) <= 8 {
		return
	}
	snapshot.RecentEvents = append([]GlobalReviewEvent(nil), snapshot.RecentEvents[len(snapshot.RecentEvents)-8:]...)
}

func finalizeGlobalReviewValidationReady(snapshot *GlobalReviewSnapshot, processed []GlobalReviewValidationProcessing) (*GlobalReviewValidationRecord, bool) {
	for i := len(processed) - 1; i >= 0; i-- {
		entry := processed[i]
		if normalizeGlobalReviewAgent(entry.AgentType) != GlobalReviewAgentInspector {
			continue
		}
		if entry.Decision != GlobalReviewValidationDecisionAccept {
			continue
		}
		if entry.Validation == nil {
			continue
		}
		record := entry.Validation
		if normalizeGlobalReviewAgent(record.RespondingAgent) != GlobalReviewAgentTester {
			continue
		}
		if strings.TrimSpace(record.Status) != string(GlobalReviewValidationPassed) {
			continue
		}
		return cloneGlobalReviewValidationRecord(record), true
	}
	if snapshot == nil || snapshot.PendingValidation == nil {
		return nil, false
	}
	return nil, false
}

func nextGlobalReviewChallengeID(snapshot *GlobalReviewSnapshot) string {
	base := "global-review"
	if snapshot != nil && strings.TrimSpace(snapshot.ReviewID) != "" {
		base = strings.TrimSpace(snapshot.ReviewID)
	}
	return fmt.Sprintf("%s-challenge-%s", base, uuid.NewString()[:8])
}

func actionNameForTarget(target string) string {
	switch normalizeGlobalReviewAgent(target) {
	case GlobalReviewAgentArchitect:
		return "challenge_architect"
	case GlobalReviewAgentOrchestrator:
		return "challenge_orchestrator"
	default:
		return "challenge_global_tester"
	}
}

func isGlobalReviewValidationStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case string(GlobalReviewValidationPassed),
		string(GlobalReviewValidationFailed),
		string(GlobalReviewValidationBlocked),
		string(GlobalReviewValidationUnclear),
		string(GlobalReviewValidationPartial):
		return true
	default:
		return false
	}
}

func isGlobalReviewValidationDecision(decision string) bool {
	switch strings.TrimSpace(decision) {
	case string(GlobalReviewValidationDecisionAccept),
		string(GlobalReviewValidationDecisionReject),
		string(GlobalReviewValidationDecisionClarify),
		string(GlobalReviewValidationDecisionLoop),
		string(GlobalReviewValidationDecisionConsult):
		return true
	default:
		return false
	}
}

func stringAny(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	if value, ok := metadata[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func stringSliceAny(metadata map[string]any, key string) []string {
	if metadata == nil {
		return nil
	}
	raw, ok := metadata[key]
	if !ok || raw == nil {
		return nil
	}
	switch typed := raw.(type) {
	case []string:
		return normalizeStringList(typed)
	case []any:
		values := make([]string, 0, len(typed))
		for _, value := range typed {
			if text, ok := value.(string); ok {
				values = append(values, text)
			}
		}
		return normalizeStringList(values)
	default:
		return nil
	}
}

func (c GlobalReviewRouteConfig) eventBus() guide.EventBus {
	if c.BusProvider != nil {
		if bus := c.BusProvider(); bus != nil {
			return bus
		}
	}
	return c.Bus
}

func (c GlobalReviewRouteConfig) sessionID(fallback string) string {
	if c.SessionID != nil {
		if sessionID := strings.TrimSpace(c.SessionID()); sessionID != "" {
			return sessionID
		}
	}
	return strings.TrimSpace(fallback)
}
