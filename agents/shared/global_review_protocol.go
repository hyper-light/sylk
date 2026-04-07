package shared

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	GlobalReviewActionHandoff   GlobalReviewActionType = "handoff"
	GlobalReviewActionChallenge GlobalReviewActionType = "challenge"
	GlobalReviewActionValidate  GlobalReviewActionType = "validation"
	GlobalReviewActionRefusal   GlobalReviewActionType = "refusal"
	GlobalReviewActionAccept    GlobalReviewActionType = "accept_checkpoint"
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
	GlobalReviewValidationDecisionHandoff GlobalReviewValidationDecision = "handoff"
)

type GlobalReviewChallenge struct {
	ID                string   `json:"id"`
	RequestingAgent   string   `json:"requesting_agent"`
	RequestingAgentID string   `json:"requesting_agent_id,omitempty"`
	TargetAgent       string   `json:"target_agent"`
	TargetAgentID     string   `json:"target_agent_id,omitempty"`
	Reason            string   `json:"reason,omitempty"`
	Request           string   `json:"request,omitempty"`
	RequiredOutput    []string `json:"required_output,omitempty"`
	References        []string `json:"references,omitempty"`
}

type GlobalReviewValidationRecord struct {
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

type GlobalReviewEvent struct {
	Type               string `json:"type"`
	AgentType          string `json:"agent_type"`
	TargetAgent        string `json:"target_agent,omitempty"`
	Summary            string `json:"summary,omitempty"`
	StateFingerprint   string `json:"state_fingerprint,omitempty"`
	RequestFingerprint string `json:"request_fingerprint,omitempty"`
}

type GlobalReviewSnapshot struct {
	ReviewID          string                        `json:"review_id,omitempty"`
	ActiveAgents      []string                      `json:"active_agents,omitempty"`
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
	Type               GlobalReviewActionType
	AgentType          string
	AgentID            string
	TargetAgent        string
	TargetAgentID      string
	CreatesChallenge   bool
	Reason             string
	Request            string
	RequiredOutput     []string
	References         []string
	ChallengeID        string
	Validation         *GlobalReviewValidationRecord
	Summary            string
	EvidenceRefs       []string
	StateFingerprint   string
	RequestFingerprint string
}

type globalReviewSelectionRefusal struct {
	RefusedBy        string
	Reason           string
	ResumeConditions []string
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
	AgentID        func() string
	ResolveTarget  func(agentType string) string
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
	if GlobalReviewStateFromContext(ctx) != nil {
		return ctx
	}
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

func (s *GlobalReviewState) SetBaseMetadata(metadata map[string]any) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.baseMetadata = cloneGlobalReviewMetadata(metadata)
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
	if snapshot != nil && snapshot.PendingChallenge != nil &&
		normalizeGlobalReviewAgent(snapshot.PendingChallenge.TargetAgent) == agentType {
		return fmt.Errorf("Before ending this global review turn, answer the active challenge with `validate_work`.")
	}
	switch agentType {
	case GlobalReviewAgentInspector:
		if snapshot != nil && snapshot.PendingValidation != nil {
			return fmt.Errorf("Before ending this global inspector turn, call `process_validation` and then decide whether the next move is `challenge_agent`, `handoff_next`, `finalize_global_review`, `accept_checkpoint`, or `commit_to_disk`.")
		}
		return fmt.Errorf("Before ending this global inspector turn, use `challenge_agent`, `handoff_next`, `validate_work`, `process_validation`, `finalize_global_review`, `accept_checkpoint`, or `commit_to_disk` to record the next review step.")
	case GlobalReviewAgentTester:
		if snapshot != nil && snapshot.PendingValidation != nil &&
			normalizeGlobalReviewAgent(snapshot.PendingValidation.RequestingAgent) == agentType {
			return fmt.Errorf("Before ending this global tester turn, call `process_validation` before choosing the next handoff or challenge.")
		}
		return fmt.Errorf("Before ending this global tester turn, use `challenge_agent`, `handoff_next`, `validate_work`, or `process_validation` to record the next review step.")
	case GlobalReviewAgentArchitect, GlobalReviewAgentOrchestrator:
		if snapshot != nil && snapshot.PendingValidation != nil &&
			normalizeGlobalReviewAgent(snapshot.PendingValidation.RequestingAgent) == agentType {
			return fmt.Errorf("Before ending this global review turn, call `process_validation` before choosing another challenge or handoff.")
		}
	}
	return nil
}

func WrapGlobalReviewTurnResult(ctx context.Context, result any) any {
	state := GlobalReviewStateFromContext(ctx)
	if state == nil {
		return result
	}
	action := state.TerminalAction()
	if action == nil {
		return result
	}
	return &GlobalReviewTurnResponse{
		Result: result,
		Action: action,
	}
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
			globalReviewChallengeAgentSkill(cfg),
			globalReviewHandoffNextSkill(cfg),
			globalReviewValidateWorkSkill(cfg),
			globalReviewProcessValidationSkill(cfg),
			globalReviewFinalizeSkill(cfg),
			globalReviewAcceptCheckpointSkill(cfg),
			globalReviewCommitToDiskSkill(cfg),
		}
	case GlobalReviewAgentTester:
		return []*skills.Skill{
			globalReviewChallengeAgentSkill(cfg),
			globalReviewHandoffNextSkill(cfg),
			globalReviewValidateWorkSkill(cfg),
			globalReviewProcessValidationSkill(cfg),
		}
	case GlobalReviewAgentArchitect, GlobalReviewAgentOrchestrator:
		return []*skills.Skill{
			globalReviewValidateWorkSkill(cfg),
		}
	default:
		return nil
	}
}

type globalReviewTurnSelectionParams struct {
	TargetAgents   []string `json:"target_agents"`
	Reason         string   `json:"reason"`
	Request        string   `json:"request"`
	RequiredOutput []string `json:"required_output"`
	References     []string `json:"references"`
}

func globalReviewChallengeAgentSkill(cfg GlobalReviewProtocolSkillConfig) *skills.Skill {
	return skills.NewSkill("challenge_agent").
		Description("Issue a targeted follow-up challenge during global review when returned work, execution state, or plan rationale needs direct clarification.").
		Domain("global_review").
		Keywords("global", "review", "challenge", "validation", "route").
		Priority(100).
		Usage("Use when a specific global-review gap needs a direct response from another agent. Use `handoff_next` for ordinary top-level Inspector <-> Tester progression; reserve this skill for targeted follow-up to Tester, Architect, Orchestrator, or Inspector.").
		Requirement("Provide exactly one target agent plus the concrete concern, the exact request, and any required evidence that target must return.").
		Satisfies("Creates an explicit targeted challenge and routes it to the challenged global-review agent.").
		ArrayParam("target_agents", "Canonical target agent: inspector, tester, architect, or orchestrator", "string", true).
		StringParam("reason", "Why the tester challenge is necessary now", true).
		StringParam("request", "Concrete validation work or clarification the challenged agent must perform", true).
		ArrayParam("required_output", "What the challenged agent must return", "string", false).
		ArrayParam("references", "Relevant files, evidence, or criteria", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params globalReviewTurnSelectionParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			target, err := normalizeGlobalReviewSelectionTarget(params.TargetAgents)
			if err != nil {
				return nil, err
			}
			action := &GlobalReviewTurnAction{
				Type:             GlobalReviewActionChallenge,
				AgentType:        normalizeGlobalReviewAgent(globalReviewAgentType(ctx, cfg)),
				AgentID:          globalReviewAgentID(ctx, cfg),
				TargetAgent:      target,
				TargetAgentID:    globalReviewResolveTargetAgentID(cfg, target),
				CreatesChallenge: true,
				Reason:           strings.TrimSpace(params.Reason),
				Request:          strings.TrimSpace(params.Request),
				RequiredOutput:   normalizeStringList(params.RequiredOutput),
				References:       normalizeStringList(params.References),
			}
			return issueGlobalReviewSelection(ctx, cfg, action)
		}).
		Build()
}

func globalReviewHandoffNextSkill(cfg GlobalReviewProtocolSkillConfig) *skills.Skill {
	return skills.NewSkill("handoff_next").
		Description("Hand top-level global-review ownership to the next ordinary owner for broad testing or audit progression.").
		Domain("global_review").
		Keywords("global", "review", "handoff", "next", "route").
		Priority(100).
		Usage("Use for ordinary top-level global-review phase progression: Global Inspector -> Global Tester for broad merged-state validation, and Global Tester -> Global Inspector when returning completed top-level validation evidence. Do not use this for targeted follow-up; use `challenge_agent` for that.").
		Requirement("Provide exactly one target agent plus the concrete request, why this handoff is correct now, and any required evidence the next owner must inspect or return.").
		Satisfies("Records the next top-level global-review owner without turning the handoff into a challenge.").
		ArrayParam("target_agents", "Canonical target agent: inspector or tester", "string", true).
		StringParam("reason", "Why this handoff is the correct next move", true).
		StringParam("request", "The concrete top-level testing or audit request for the target agent", true).
		ArrayParam("required_output", "What the target agent must return", "string", false).
		ArrayParam("references", "Relevant files, evidence, or criteria", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params globalReviewTurnSelectionParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			target, err := normalizeGlobalReviewSelectionTarget(params.TargetAgents)
			if err != nil {
				return nil, err
			}
			action := &GlobalReviewTurnAction{
				Type:           GlobalReviewActionHandoff,
				AgentType:      normalizeGlobalReviewAgent(globalReviewAgentType(ctx, cfg)),
				AgentID:        globalReviewAgentID(ctx, cfg),
				TargetAgent:    target,
				TargetAgentID:  globalReviewResolveTargetAgentID(cfg, target),
				Reason:         strings.TrimSpace(params.Reason),
				Request:        strings.TrimSpace(params.Request),
				RequiredOutput: normalizeStringList(params.RequiredOutput),
				References:     normalizeStringList(params.References),
			}
			return issueGlobalReviewSelection(ctx, cfg, action)
		}).
		Build()
}

func normalizeGlobalReviewSelectionTarget(raw []string) (string, error) {
	targets := normalizeStringList(raw)
	if len(targets) != 1 {
		return "", fmt.Errorf("exactly one target agent is required")
	}
	target := normalizeGlobalReviewAgent(targets[0])
	switch target {
	case GlobalReviewAgentInspector, GlobalReviewAgentTester, GlobalReviewAgentArchitect, GlobalReviewAgentOrchestrator:
		return target, nil
	default:
		return "", fmt.Errorf("target agent %q is invalid", targets[0])
	}
}

func issueGlobalReviewSelection(
	ctx context.Context,
	cfg GlobalReviewProtocolSkillConfig,
	action *GlobalReviewTurnAction,
) (map[string]any, error) {
	state := GlobalReviewStateFromContext(ctx)
	if state == nil {
		return nil, fmt.Errorf("global review state not available")
	}
	if err := validateGlobalReviewSelection(action); err != nil {
		return nil, err
	}
	if strings.TrimSpace(action.AgentID) == "" {
		return nil, fmt.Errorf("global review routing requires the exact current agent id; missing current agent identity")
	}
	if strings.TrimSpace(action.TargetAgentID) == "" {
		return nil, fmt.Errorf("global review routing requires the exact target agent id for %s", strings.TrimSpace(action.TargetAgent))
	}
	if strings.TrimSpace(action.Reason) == "" {
		return nil, fmt.Errorf("reason is required")
	}
	if strings.TrimSpace(action.Request) == "" {
		return nil, fmt.Errorf("request is required")
	}
	if err := ValidateGlobalTesterHandoffEvidence(ctx, action); err != nil {
		return nil, err
	}
	snapshot := state.Snapshot()
	if snapshot != nil && snapshot.PendingValidation != nil {
		return nil, fmt.Errorf("process the pending validation before issuing another global review challenge")
	}
	action.StateFingerprint = resolveGlobalReviewSelectionStateFingerprint(ctx, cfg, state)
	action.RequestFingerprint = globalReviewSelectionRequestFingerprint(action)
	if refusal := globalReviewRepeatedSelectionRefusal(snapshot, action); refusal != nil {
		if err := state.setTerminalAction(&GlobalReviewTurnAction{
			Type:               GlobalReviewActionRefusal,
			AgentType:          normalizeGlobalReviewAgent(action.AgentType),
			AgentID:            strings.TrimSpace(action.AgentID),
			TargetAgent:        normalizeGlobalReviewAgent(action.TargetAgent),
			TargetAgentID:      strings.TrimSpace(action.TargetAgentID),
			Summary:            refusal.Reason,
			StateFingerprint:   action.StateFingerprint,
			RequestFingerprint: action.RequestFingerprint,
		}); err != nil {
			return nil, err
		}
		return map[string]any{
			"refused":           true,
			"refused_by":        firstNonEmpty(strings.TrimSpace(refusal.RefusedBy), "global-review-protocol"),
			"agent_type":        normalizeGlobalReviewAgent(action.AgentType),
			"target_agent":      normalizeGlobalReviewAgent(action.TargetAgent),
			"reason":            refusal.Reason,
			"must_wait":         true,
			"resume_conditions": append([]string(nil), refusal.ResumeConditions...),
		}, nil
	}
	if action.CreatesChallenge {
		action.ChallengeID = nextGlobalReviewChallengeID(snapshot)
	}
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
		"selected":          true,
		"agent_type":        action.AgentType,
		"agent_id":          action.AgentID,
		"target_agent":      action.TargetAgent,
		"target_agent_id":   action.TargetAgentID,
		"target_agents":     []string{action.TargetAgent},
		"creates_challenge": action.CreatesChallenge,
		"challenge_id":      strings.TrimSpace(action.ChallengeID),
		"forwarded":         true,
		"correlation_id":    dispatch.CorrelationID,
	}, nil
}

func globalReviewRepeatedSelectionRefusal(
	snapshot *GlobalReviewSnapshot,
	action *GlobalReviewTurnAction,
) *globalReviewSelectionRefusal {
	if snapshot == nil || action == nil {
		return nil
	}
	currentState := strings.TrimSpace(action.StateFingerprint)
	currentRequest := strings.TrimSpace(action.RequestFingerprint)
	if currentState == "" || currentRequest == "" {
		return nil
	}
	previous, ok := recentGlobalReviewSelectionEvent(
		snapshot.RecentEvents,
		string(action.Type),
		action.AgentType,
		action.TargetAgent,
	)
	if !ok {
		return nil
	}
	if strings.TrimSpace(previous.StateFingerprint) != currentState ||
		strings.TrimSpace(previous.RequestFingerprint) != currentRequest {
		return nil
	}
	targetLabel := firstNonEmpty(normalizeGlobalReviewAgent(action.TargetAgent), "the same target")
	verb := "handoff"
	if action.CreatesChallenge {
		verb = "challenge"
	}
	return &globalReviewSelectionRefusal{
		RefusedBy: "global-review-protocol",
		Reason: fmt.Sprintf(
			"Repeated global review %s to %s requires fresh merged-state evidence or a materially different request, but the current merged state and routed request are unchanged since the previous selection on this directed pair.",
			verb,
			targetLabel,
		),
		ResumeConditions: []string{
			fmt.Sprintf("Wait for the merged review state to change before issuing another %s to %s.", verb, targetLabel),
			"Use `inspect_workspace_state`, `summarize_workspace_state`, or a narrower targeted challenge to gather genuinely new evidence before retrying.",
		},
	}
}

func recentGlobalReviewSelectionEvent(
	events []GlobalReviewEvent,
	actionType, agentType, targetAgent string,
) (GlobalReviewEvent, bool) {
	actionType = strings.TrimSpace(actionType)
	agentType = normalizeGlobalReviewAgent(agentType)
	targetAgent = normalizeGlobalReviewAgent(targetAgent)
	for i := len(events) - 1; i >= 0; i-- {
		evt := events[i]
		if strings.TrimSpace(evt.Type) != actionType {
			continue
		}
		if normalizeGlobalReviewAgent(evt.AgentType) != agentType {
			continue
		}
		if normalizeGlobalReviewAgent(evt.TargetAgent) != targetAgent {
			continue
		}
		return evt, true
	}
	return GlobalReviewEvent{}, false
}

func resolveGlobalReviewSelectionStateFingerprint(
	ctx context.Context,
	cfg GlobalReviewProtocolSkillConfig,
	state *GlobalReviewState,
) string {
	metadata := map[string]any(nil)
	if state != nil {
		metadata = state.BaseMetadata()
	}
	payload := map[string]any{}
	if version := strings.TrimSpace(stringAny(metadata, "global_vfs_version")); version != "" {
		payload["global_vfs_version"] = version
	}
	if stage := strings.TrimSpace(globalReviewStageFromMetadata(metadata)); stage != "" {
		payload["global_review_stage"] = stage
	}
	if summary := strings.TrimSpace(stringAny(metadata, "acceptance_summary")); summary != "" {
		payload["acceptance_summary"] = summary
	}
	if criteria := strings.TrimSpace(stringAny(metadata, "task_criteria_snapshot")); criteria != "" {
		payload["task_criteria_snapshot"] = criteria
	}
	if plan := strings.TrimSpace(stringAny(metadata, "plan_snapshot")); plan != "" {
		payload["plan_snapshot"] = plan
	}
	if description := strings.TrimSpace(stringAny(metadata, "task_description")); description != "" {
		payload["task_description"] = description
	}
	if paths := globalReviewEvidencePaths(metadata); len(paths) > 0 {
		payload["evidence_paths"] = paths
		if cfg.WorkspaceViews != nil {
			if views := cfg.WorkspaceViews(); views != nil {
				if summary, err := views.SummarizePaths(ctx, paths, ""); err == nil && summary != nil {
					payload["workspace_summary"] = summary
				}
			}
		}
	}
	return globalReviewFingerprint(payload)
}

func globalReviewSelectionRequestFingerprint(action *GlobalReviewTurnAction) string {
	if action == nil {
		return ""
	}
	return globalReviewFingerprint(map[string]any{
		"type":            strings.TrimSpace(string(action.Type)),
		"reason":          strings.TrimSpace(action.Reason),
		"request":         strings.TrimSpace(action.Request),
		"required_output": normalizeStringList(action.RequiredOutput),
		"references":      normalizeStringList(action.References),
	})
}

func globalReviewEvidencePaths(metadata map[string]any) []string {
	if len(metadata) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := []string{}
	appendPath := func(raw string) {
		path := strings.TrimSpace(raw)
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	for _, path := range stringSliceAny(metadata, "affected_files") {
		appendPath(path)
	}
	appendPath(stringAny(metadata, "plan_file_path"))
	return out
}

func globalReviewFingerprint(payload any) string {
	if payload == nil {
		return ""
	}
	data, err := json.Marshal(payload)
	if err != nil || len(data) == 0 || string(data) == "{}" || string(data) == "null" {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validateGlobalReviewSelection(action *GlobalReviewTurnAction) error {
	if action == nil {
		return fmt.Errorf("global review action is required")
	}
	actor := normalizeGlobalReviewAgent(action.AgentType)
	target := normalizeGlobalReviewAgent(action.TargetAgent)
	switch actor {
	case GlobalReviewAgentInspector:
		if action.CreatesChallenge {
			switch target {
			case GlobalReviewAgentTester, GlobalReviewAgentArchitect, GlobalReviewAgentOrchestrator:
				return nil
			}
		} else if target == GlobalReviewAgentTester {
			return nil
		}
	case GlobalReviewAgentTester:
		if action.CreatesChallenge {
			if target == GlobalReviewAgentInspector {
				return nil
			}
		} else if target == GlobalReviewAgentInspector {
			return nil
		}
	}
	verb := "handoff_next"
	if action.CreatesChallenge {
		verb = "challenge_agent"
	}
	return fmt.Errorf("%s is not permitted for %s -> %s in the global review protocol", verb, actor, target)
}

func globalReviewValidateWorkSkill(cfg GlobalReviewProtocolSkillConfig) *skills.Skill {
	return skills.NewSkill("validate_work").
		Description("Answer an active global-review challenge from the global inspector with a structured validation response and evidence.").
		Domain("global_review").
		Keywords("global", "review", "validate", "response", "evidence").
		Priority(100).
		Usage("Use only when another global-review agent has an active challenge waiting for your response. Ordinary top-level Inspector <-> Tester progression still uses `handoff_next`.").
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
			respondingAgentID := strings.TrimSpace(globalReviewAgentID(ctx, cfg))
			if respondingAgentID == "" {
				return nil, fmt.Errorf("global review validation requires the exact responding agent id")
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
				RequestingAgentID:     globalReviewExactChallengeRequestingAgentID(challenge),
				RespondingAgent:       respondingAgent,
				RespondingAgentID:     respondingAgentID,
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
				"validated":           true,
				"challenge_id":        record.ChallengeID,
				"requesting_agent":    record.RequestingAgent,
				"requesting_agent_id": record.RequestingAgentID,
				"responding_agent":    record.RespondingAgent,
				"responding_agent_id": record.RespondingAgentID,
				"status":              record.Status,
				"protocol_scope":      globalReviewNamespace,
				"thread_key":          globalReviewThreadPrefix + strings.TrimSpace(record.ChallengeID),
				"forwarded":           true,
				"correlation_id":      correlationID,
				"target_agent":        requestingAgent,
				"target_agent_id":     record.RequestingAgentID,
			}, nil
		}).
		Build()
}

func globalReviewProcessValidationSkill(cfg GlobalReviewProtocolSkillConfig) *skills.Skill {
	return skills.NewSkill("process_validation").
		Description("Acknowledge and interpret a global-review validation response before choosing the next step.").
		Domain("global_review").
		Keywords("global", "review", "process", "validation").
		Priority(99).
		Usage("Use after another global-review agent answers one of your challenges and before issuing another challenge, handing ownership forward, finalizing, or committing to disk.").
		Satisfies("Records how the requesting global-review agent interpreted the received validation response.").
		StringParam("challenge_id", "The validation challenge being processed", true).
		EnumParam("decision", "How you interpreted the response", []string{
			string(GlobalReviewValidationDecisionAccept),
			string(GlobalReviewValidationDecisionReject),
			string(GlobalReviewValidationDecisionClarify),
			string(GlobalReviewValidationDecisionLoop),
			string(GlobalReviewValidationDecisionConsult),
			string(GlobalReviewValidationDecisionHandoff),
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
			agentType := normalizeGlobalReviewAgent(globalReviewAgentType(ctx, cfg))
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
			if agentType == "" || agentType != normalizeGlobalReviewAgent(pending.RequestingAgent) {
				return nil, fmt.Errorf("process_validation is only permitted for the agent that requested the active challenge")
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
				AgentType:   agentType,
				Decision:    decision,
				Summary:     summary,
				Validation:  cloneGlobalReviewValidationRecord(pending),
			}
			if err := state.recordValidationProcessing(ctx, entry); err != nil {
				return nil, err
			}
			return map[string]any{
				"processed":      true,
				"challenge_id":   challengeID,
				"decision":       string(decision),
				"agent_type":     agentType,
				"protocol_scope": globalReviewNamespace,
				"thread_key":     globalReviewThreadPrefix + challengeID,
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
		Requirement("If this returns ready_for_checkpoint, your next terminal action in this turn must be `accept_checkpoint`. If it returns ready_for_commit or must_commit_to_disk, your next terminal action in this turn must be `commit_to_disk`.").
		Satisfies("Issues the tester challenge for the current review stage or recognizes that the tester-backed audit already passed and the current checkpoint is ready for acceptance or final commit.").
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
				allEvidence := normalizeStringList(append(append([]string(nil), evidenceRefs...), record.EvidenceRefs...))
				if finalWholePlanReview {
					if err := state.recordReadyForCommit(ctx, summary, allEvidence, record); err != nil {
						return nil, err
					}
					return map[string]any{
						"finalize_global_review":    true,
						"ready_for_commit":          true,
						"must_commit_to_disk":       true,
						"required_next_action":      "commit_to_disk",
						"required_next_action_only": true,
						"challenge_id":              record.ChallengeID,
						"evidence_refs":             allEvidence,
					}, nil
				}
				if err := state.recordReadyForCheckpoint(ctx, summary, allEvidence, record); err != nil {
					return nil, err
				}
				return map[string]any{
					"finalize_global_review":    true,
					"ready_for_checkpoint":      true,
					"must_accept_checkpoint":    true,
					"required_next_action":      "accept_checkpoint",
					"required_next_action_only": true,
					"challenge_id":              record.ChallengeID,
					"evidence_refs":             allEvidence,
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
				AgentID:          globalReviewAgentID(ctx, cfg),
				TargetAgent:      GlobalReviewAgentTester,
				TargetAgentID:    globalReviewResolveTargetAgentID(cfg, GlobalReviewAgentTester),
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
			return issueGlobalReviewSelection(ctx, cfg, action)
		}).
		Build()
}

func globalReviewAcceptCheckpointSkill(cfg GlobalReviewProtocolSkillConfig) *skills.Skill {
	return skills.NewSkill("accept_checkpoint").
		Description("Promote the currently reviewed checkpoint candidate into dependency-visible shared session state. Global inspector only.").
		Domain("global_review").
		Keywords("global", "review", "checkpoint", "accept", "promote").
		Priority(100).
		Usage("Use only after a checkpoint-stage tester-backed global review has passed and the current merged candidate is ready to become dependency-visible to downstream work.").
		Requirement("This is not the final disk commit. Use `commit_to_disk` only for the final whole-plan review stage.").
		Satisfies("Makes the current reviewed candidate the new shared checkpoint and closes the progressive checkpoint review for this task.").
		StringParam("summary", "Why the current checkpoint is safe to promote for downstream work", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Summary string `json:"summary"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if normalizeGlobalReviewAgent(globalReviewAgentType(ctx, cfg)) != GlobalReviewAgentInspector {
				return nil, fmt.Errorf("accept_checkpoint is only permitted for the global inspector")
			}
			summary := strings.TrimSpace(params.Summary)
			if summary == "" {
				return nil, fmt.Errorf("summary is required")
			}
			state := GlobalReviewStateFromContext(ctx)
			if state == nil {
				return nil, fmt.Errorf("global review state not available")
			}
			if globalReviewIsFinal(state.BaseMetadata()) {
				return nil, fmt.Errorf("accept_checkpoint is only valid during checkpoint-stage global review; use commit_to_disk for the final stage")
			}
			if _, ok := finalizeGlobalReviewValidationReady(state.Snapshot(), state.ProcessedValidations()); !ok {
				return nil, fmt.Errorf("accept_checkpoint requires a passing tester-backed global review first")
			}
			views := cfg.WorkspaceViews()
			if views == nil {
				return nil, fmt.Errorf("workspace views are unavailable")
			}
			svfs := versioning.SessionForWorkspaceViews(ctx, views)
			if svfs == nil {
				return nil, fmt.Errorf("session VFS is unavailable for accept_checkpoint")
			}
			if _, _, err := svfs.AcceptActiveReviewCandidate(ctx, "global-review"); err != nil {
				return nil, err
			}
			action := &GlobalReviewTurnAction{
				Type:      GlobalReviewActionAccept,
				AgentType: GlobalReviewAgentInspector,
				AgentID:   globalReviewAgentID(ctx, cfg),
				Summary:   summary,
			}
			if err := state.recordCheckpointAccepted(ctx, action); err != nil {
				return nil, err
			}
			if err := state.setTerminalAction(action); err != nil {
				return nil, err
			}
			return map[string]any{
				"accepted_checkpoint": true,
				"summary":             summary,
			}, nil
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
			if _, _, err := svfs.AcceptActiveReviewCandidate(ctx, "global-review"); err != nil {
				return nil, err
			}
			authReq := commandapproval.Request{
				Command:        fmt.Sprintf("commit_to_disk --reason %q", summary),
				ToolName:       "commit_to_disk",
				AgentID:        firstNonEmpty(globalReviewAgentID(ctx, cfg), GlobalReviewAgentInspector),
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
				AgentID:   globalReviewAgentID(ctx, cfg),
				Summary:   summary,
			}
			if err := state.recordCommitToDisk(ctx, action); err != nil {
				return nil, err
			}
			if err := state.setTerminalAction(&GlobalReviewTurnAction{
				Type:      GlobalReviewActionCommit,
				AgentType: GlobalReviewAgentInspector,
				AgentID:   globalReviewAgentID(ctx, cfg),
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
	branchCtx := ctx
	var branch InterAgentBranchHandle
	if action.CreatesChallenge {
		branchCtx, branch = BeginInterAgentBranch(ctx, InterAgentBranchSpec{
			Kind:          InterAgentToolEventKindChallenge,
			ToolName:      "challenge_agent",
			AgentTypes:    []string{strings.TrimSpace(action.TargetAgent)},
			Summary:       action.Request,
			ThreadKey:     globalReviewThreadPrefix + strings.TrimSpace(action.ChallengeID),
			SuccessStatus: InterAgentToolEventStatusPending,
			Args: map[string]any{
				"target_agent":   strings.TrimSpace(action.TargetAgentID),
				"target_type":    strings.TrimSpace(action.TargetAgent),
				"challenge_id":   strings.TrimSpace(action.ChallengeID),
				"request":        action.Request,
				"protocol_scope": globalReviewNamespace,
				"thread_key":     globalReviewThreadPrefix + strings.TrimSpace(action.ChallengeID),
			},
		})
		metadata = RouteMetadataWithExplicitInterAgentBranch(branchCtx, metadata, InterAgentBranchMetadata{
			ThreadKey: globalReviewThreadPrefix + strings.TrimSpace(action.ChallengeID),
			Kind:      InterAgentToolEventKindChallenge,
		})
	}
	prompt := buildGlobalReviewTurnPrompt(action, metadata)
	correlationID := "global_review_" + uuid.NewString()[:12]
	sourceAgentID := globalReviewVisibleSourceAgentID(stream)
	req := &guide.RouteRequest{
		CorrelationID:       correlationID,
		ParentCorrelationID: strings.TrimSpace(stream.CorrelationID),
		Input:               prompt,
		TargetAgentID:       strings.TrimSpace(action.TargetAgentID),
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
	publishGlobalReviewReroute(bus, ctx, action.AgentType, action.AgentID, action.TargetAgent, action.TargetAgentID, action.Request, correlationID)
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
		ToolName:   "validate_work",
		AgentTypes: []string{strings.TrimSpace(record.RequestingAgent)},
		Summary:    record.Summary,
		ThreadKey:  globalReviewThreadPrefix + strings.TrimSpace(record.ChallengeID),
		Args: map[string]any{
			"target_agent":   strings.TrimSpace(record.RequestingAgentID),
			"target_type":    strings.TrimSpace(record.RequestingAgent),
			"challenge_id":   strings.TrimSpace(record.ChallengeID),
			"summary":        record.Summary,
			"protocol_scope": globalReviewNamespace,
			"thread_key":     globalReviewThreadPrefix + strings.TrimSpace(record.ChallengeID),
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
		TargetAgentID:       strings.TrimSpace(record.RequestingAgentID),
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
	publishGlobalReviewReroute(bus, ctx, record.RespondingAgent, record.RespondingAgentID, record.RequestingAgent, record.RequestingAgentID, record.Summary, correlationID)
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
			"action":  "validate_work",
			"summary": "Answer the active global-review challenge before ending the turn.",
		}}
	}
	if validation := snapshot.PendingValidation; validation != nil && normalizeGlobalReviewAgent(validation.RequestingAgent) == targetAgent {
		return []map[string]any{{
			"action":  "process_validation",
			"summary": "Process the pending validation response before selecting the next review step.",
		}}
	}
	return nil
}

func buildGlobalReviewHandoffSnapshot(state *GlobalReviewState, action *GlobalReviewTurnAction) *GlobalReviewSnapshot {
	return applyGlobalReviewSelectionToSnapshot(materializeGlobalReviewSnapshot(state), action)
}

func buildGlobalReviewValidationSnapshot(state *GlobalReviewState, record *GlobalReviewValidationRecord) *GlobalReviewSnapshot {
	snapshot := materializeGlobalReviewSnapshot(state)
	if snapshot == nil {
		snapshot = &GlobalReviewSnapshot{}
	}
	snapshot.ActiveAgents = []string{normalizeGlobalReviewAgent(record.RequestingAgent)}
	snapshot.RequestedBy = normalizeGlobalReviewAgent(record.RespondingAgent)
	snapshot.CurrentRequest = fmt.Sprintf("Process validation response for challenge %s and decide the next handoff.", strings.TrimSpace(record.ChallengeID))
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

func applyGlobalReviewSelectionToSnapshot(base *GlobalReviewSnapshot, action *GlobalReviewTurnAction) *GlobalReviewSnapshot {
	snapshot := cloneGlobalReviewSnapshot(base)
	if snapshot == nil {
		snapshot = &GlobalReviewSnapshot{}
	}
	if action == nil {
		return snapshot
	}
	snapshot.ActiveAgents = []string{normalizeGlobalReviewAgent(action.TargetAgent)}
	snapshot.RequestedBy = normalizeGlobalReviewAgent(action.AgentType)
	snapshot.CurrentRequest = strings.TrimSpace(action.Request)
	snapshot.PendingValidation = nil
	snapshot.PendingChallenge = nil
	if action.CreatesChallenge {
		snapshot.PendingChallenge = &GlobalReviewChallenge{
			ID:                strings.TrimSpace(action.ChallengeID),
			RequestingAgent:   normalizeGlobalReviewAgent(action.AgentType),
			RequestingAgentID: strings.TrimSpace(action.AgentID),
			TargetAgent:       normalizeGlobalReviewAgent(action.TargetAgent),
			TargetAgentID:     strings.TrimSpace(action.TargetAgentID),
			Reason:            strings.TrimSpace(action.Reason),
			Request:           strings.TrimSpace(action.Request),
			RequiredOutput:    append([]string(nil), action.RequiredOutput...),
			References:        append([]string(nil), action.References...),
		}
	}
	appendGlobalReviewEvent(snapshot, GlobalReviewEvent{
		Type:               string(action.Type),
		AgentType:          normalizeGlobalReviewAgent(action.AgentType),
		TargetAgent:        normalizeGlobalReviewAgent(action.TargetAgent),
		Summary:            firstNonEmpty(strings.TrimSpace(action.Request), strings.TrimSpace(action.Summary)),
		StateFingerprint:   strings.TrimSpace(action.StateFingerprint),
		RequestFingerprint: strings.TrimSpace(action.RequestFingerprint),
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
			Type:      "process_validation",
			AgentType: normalizeGlobalReviewAgent(entry.AgentType),
			Summary:   strings.TrimSpace(entry.Summary),
		})
		if snapshot.PendingValidation != nil && strings.TrimSpace(snapshot.PendingValidation.ChallengeID) == strings.TrimSpace(entry.ChallengeID) {
			snapshot.PendingValidation = nil
			snapshot.CurrentRequest = fmt.Sprintf(
				"Choose the next global review action after processing challenge %s.",
				strings.TrimSpace(entry.ChallengeID),
			)
		}
	}
	return snapshot
}

func buildGlobalReviewTurnPrompt(action *GlobalReviewTurnAction, metadata map[string]any) string {
	normalizedTarget := normalizeGlobalReviewAgent(action.TargetAgent)
	var lines []string
	switch normalizedTarget {
	case GlobalReviewAgentArchitect:
		lines = append(lines, "Global review request for the architect.")
	case GlobalReviewAgentOrchestrator:
		lines = append(lines, "Global review request for the orchestrator.")
	case GlobalReviewAgentInspector:
		lines = append(lines, "Global tester handoff back to the global inspector.")
	default:
		if action.CreatesChallenge {
			lines = append(lines, "Global inspector challenge for the global tester.")
		} else {
			lines = append(lines, "Global review handoff for the global tester.")
		}
	}
	if action.CreatesChallenge {
		lines = append(lines,
			"This request is part of the global review protocol over merged global state.",
			"This is a targeted challenge turn. Do not answer narratively without recording the result. End this turn with `validate_work`.",
		)
	} else {
		lines = append(lines,
			"This request is part of the global review protocol over merged global state.",
			"This is an ordinary top-level handoff, not a targeted challenge. End this turn with `handoff_next` when the requested broad work is complete.",
			"Use `challenge_agent` only if a specific ambiguity or blocker needs a narrower follow-up instead of a normal top-level return.",
		)
	}
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
	if action.CreatesChallenge {
		lines = append(lines, fmt.Sprintf("Use `validate_work` with challenge_id %s to hand the result back to the requesting agent.", strings.TrimSpace(action.ChallengeID)))
	} else if normalizedTarget == GlobalReviewAgentTester {
		lines = append(lines, "When you complete the requested merged-state validation work, use `handoff_next` to return top-level ownership to the global inspector.")
	} else if normalizedTarget == GlobalReviewAgentInspector {
		lines = append(lines, "After you audit the returned global testing evidence, use `challenge_agent` for targeted follow-up, `handoff_next` for another top-level tester pass, or `finalize_global_review` when the current audit is actually settled.")
	}
	return strings.Join(lines, "\n")
}

func buildGlobalReviewValidationPrompt(record *GlobalReviewValidationRecord, metadata map[string]any) string {
	normalizedResponder := normalizeGlobalReviewAgent(record.RespondingAgent)
	lines := []string{
		fmt.Sprintf("Global review challenge response from %s.", normalizedResponder),
		"This request is part of the global review protocol over merged global state.",
		"Use `process_validation` before choosing the next action.",
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
	lines = append(lines, "After `process_validation`, decide whether the next step is another targeted `challenge_agent`, an ordinary `handoff_next`, `finalize_global_review`, `accept_checkpoint`, or `commit_to_disk`.")
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
	return "Run the adversarial global tester audit for this merged checkpoint before deciding whether the current state is safe and strong enough to become the next shared checkpoint."
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

func publishGlobalReviewReroute(
	bus guide.EventBus,
	ctx context.Context,
	fromAgentType, fromAgentID, toAgentType, toAgentID, reason, newCorrelationID string,
) {
	if bus == nil {
		return
	}
	stream, ok := StreamMetadataFromContext(ctx)
	if !ok || strings.TrimSpace(stream.CorrelationID) == "" || strings.TrimSpace(newCorrelationID) == "" {
		return
	}
	fromType := strings.TrimSpace(normalizeGlobalReviewAgent(fromAgentType))
	fromID := firstNonEmpty(strings.TrimSpace(fromAgentID), fromType)
	toID := firstNonEmpty(strings.TrimSpace(toAgentID), strings.TrimSpace(normalizeGlobalReviewAgent(toAgentType)))
	PublishStreamEvent(bus, guide.NewAgentChannels(fromType, fromID), ctx, fromID, &guide.StreamEvent{
		Type: guide.StreamEventReroute,
		Data: map[string]string{
			"from_agent":              fromID,
			"to_agent":                toID,
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

func globalReviewAgentID(ctx context.Context, cfg GlobalReviewProtocolSkillConfig) string {
	if cfg.AgentID != nil {
		if agentID := strings.TrimSpace(cfg.AgentID()); agentID != "" {
			return agentID
		}
	}
	if meta := LogMetaFromContext(ctx); strings.TrimSpace(meta.AgentID) != "" {
		return strings.TrimSpace(meta.AgentID)
	}
	return globalReviewAgentType(ctx, cfg)
}

func globalReviewResolveTargetAgentID(cfg GlobalReviewProtocolSkillConfig, agentType string) string {
	normalized := normalizeGlobalReviewAgent(agentType)
	if cfg.ResolveTarget != nil {
		if agentID := strings.TrimSpace(cfg.ResolveTarget(normalized)); agentID != "" {
			return agentID
		}
	}
	return strings.TrimSpace(normalized)
}

func globalReviewExactChallengeRequestingAgentID(challenge *GlobalReviewChallenge) string {
	if challenge == nil {
		return ""
	}
	return firstNonEmpty(strings.TrimSpace(challenge.RequestingAgentID), strings.TrimSpace(challenge.RequestingAgent))
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
	out.ActiveAgents = append([]string(nil), snapshot.ActiveAgents...)
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
		string(GlobalReviewValidationDecisionConsult),
		string(GlobalReviewValidationDecisionHandoff):
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
