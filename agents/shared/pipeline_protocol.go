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

// PipelineHandoffArtifactRef is a per-recipient verification artifact reference
// queued by the tester's finalize_pipeline. Each entry encodes which artifact
// the downstream recipient should consume, the source suite that produced it,
// and the LLM-supplied narrative for that recipient. The tester finalize
// populates one ref per target; handoff_next / validate_work / passthrough
// dispatch consume them and clear them from the queue.
//
// QueuedAtIteration captures the protocol iteration the artifact was queued
// at. The dispatch path uses it to surface age advisories on every terminal
// action result and to auto-discard artifacts older than
// pipelineArtifactMaxIterations as a bounded-loss convergence guard. Pure
// advisory data — no terminal action is rejected based on age. The LLM reads
// the age in queue_state and decides whether to converge faster, route
// directly, or invoke discard_queued_artifacts.
type PipelineHandoffArtifactRef struct {
	ArtifactID        string   `json:"artifact_id"`
	Kind              string   `json:"kind,omitempty"`
	Target            string   `json:"target"`
	SuiteID           string   `json:"suite_id,omitempty"`
	Summary           string   `json:"summary,omitempty"`
	EvidenceRefs      []string `json:"evidence_refs,omitempty"`
	FailureFocus      []string `json:"failure_focus,omitempty"`
	QueuedAtIteration int      `json:"queued_at_iteration,omitempty"`
}

// pipelineArtifactMaxIterations bounds how long a queued artifact may persist
// before the dispatch path auto-discards it. Five iterations is the empirical
// bound we picked: a normal red-green-refactor task converges in 1-3
// iterations; anything past 5 iterations stranding an artifact is divergence.
// Bounded loss (one artifact dropped, one warning event) is preferable to
// unbounded queue accumulation across pipeline runs.
const pipelineArtifactMaxIterations = 5

// PipelineTesterFinalizeTargetSpec is the per-recipient input the tester
// supplies when calling finalize_pipeline. The summary, evidence_refs, and
// failure_focus reflect what the LLM determined is relevant to that specific
// recipient — engineer-relevant failures and contracts go to engineer, etc.
type PipelineTesterFinalizeTargetSpec struct {
	Target       string   `json:"target"`
	Summary      string   `json:"summary"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
	FailureFocus []string `json:"failure_focus,omitempty"`
}

type PipelineProtocolState struct {
	mu              sync.RWMutex
	sessionDir      string
	scopeID         string
	store           *durableProtocolLog
	snapshot        *PipelineProtocolSnapshot
	terminalAction  *PipelineTurnAction
	processed       []PipelineValidationProcessing
	requiredAction  PipelineProtocolActionType
	requiredReason  string
	queuedArtifacts map[string]PipelineHandoffArtifactRef
}

// PipelineTesterFinalizeFn is the agent-supplied finalize callback. The tester
// wires this to its publish-verification-artifact path. Returns one artifact
// reference per requested target, in the same order as the input specs.
type PipelineTesterFinalizeFn func(ctx context.Context, suiteID string, specs []PipelineTesterFinalizeTargetSpec) ([]PipelineHandoffArtifactRef, error)

// PipelineTesterSuiteIDFn returns the identifier of the suite snapshot the
// tester captured during the current turn, or empty string if no suite has
// been run yet. Used by the finalize_pipeline skill for freshness and
// no-repeat checks before invoking the publish callback.
type PipelineTesterSuiteIDFn func() string

// PipelineCommitter is the inspector-owned authority that mutates the
// pipeline VFS lifecycle. The pipeline inspector's handoff_to_ot and
// discard_pipeline skills call into this interface so the actual extract /
// rollback operation is performed by the agent that decided it should
// happen. Other pipeline agents (engineer, designer, tester) leave this
// nil — they have no authority to commit or rollback.
//
// Background: the orchestrator used to react to "succeeded" / "failed"
// pipeline-update broadcasts and call SessionVFS.ExtractReviewCandidate /
// CommitPipeline / RollbackPipeline itself. That made the orchestrator the
// effective owner of pipeline VFS lifecycle even though the protocol
// designates the inspector as the only authorized terminator. The
// resulting "edit_pipeline_file failed: VFS not found" reports were the
// inspector seeing the orchestrator silently destroy the VFS while it
// still had follow-up work queued. Moving the operations behind this
// interface — invoked from the inspector's own skill handlers — keeps
// authority where the protocol says it belongs.
type PipelineCommitter interface {
	// ExtractReviewCandidate closes the named pipeline draft, stashes its
	// modifications as a review candidate for the global review tier, and
	// removes the pipeline VFS from the session map. Returns the
	// candidate's ID (empty when no modifications existed), whether a
	// draft was actually present, and the resulting checkpoint version.
	ExtractReviewCandidate(ctx context.Context, pipelineID string) (candidateID string, hadDraft bool, version versioning.SemanticVersion, err error)

	// Rollback discards a pipeline draft without merging it. Used by the
	// inspector's discard_pipeline skill when work is judged
	// irrecoverable. Idempotent — a missing pipeline returns nil.
	Rollback(ctx context.Context, pipelineID string) error
}

type PipelineProtocolSkillConfig struct {
	AgentType      func() string
	AgentID        func() string
	InspectorOT    bool
	WorkspaceViews func() versioning.WorkspaceViewAccess
	Route          PipelineProtocolRouteConfig
	// Committer returns the inspector-only VFS lifecycle authority.
	// Required when InspectorOT is true (handoff_to_ot needs it); ignored
	// otherwise. Lazy because the inspector's committer is wired after
	// skill registration runs (the SessionVFS isn't available yet at
	// agent construction). Returning nil from the lookup at call time is
	// a configuration error and surfaces as a tool-call failure.
	Committer func() PipelineCommitter
	// TesterFinalize, when non-nil, opts the agent into the tester
	// finalize_pipeline skill. The skill packages a per-recipient
	// verification artifact for each target spec the LLM provides and
	// queues the resulting refs in the protocol state for handoff_next /
	// validate_work to consume during dispatch.
	TesterFinalize PipelineTesterFinalizeFn
	// TesterCurrentSuiteID returns the current turn's suite snapshot ID
	// (empty when no run_test_suite has succeeded this turn). Required
	// when TesterFinalize is set; the finalize handler uses it for
	// freshness and no-repeat enforcement.
	TesterCurrentSuiteID PipelineTesterSuiteIDFn
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

// QueuedArtifacts returns a snapshot of pending tester verification artifact
// refs keyed by target. An empty map means no finalize_pipeline output is
// awaiting a terminal handoff/validate consumption.
func (s *PipelineProtocolState) QueuedArtifacts() map[string]PipelineHandoffArtifactRef {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return clonePipelineHandoffArtifactMap(s.queuedArtifacts)
}

// QueuedArtifactForTarget returns the queued ref for the named target, if any.
func (s *PipelineProtocolState) QueuedArtifactForTarget(target string) (PipelineHandoffArtifactRef, bool) {
	target = normalizePipelineAgentType(target)
	if s == nil || target == "" {
		return PipelineHandoffArtifactRef{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	ref, ok := s.queuedArtifacts[target]
	if !ok {
		return PipelineHandoffArtifactRef{}, false
	}
	return cloneHandoffArtifactRef(ref), true
}

func clonePipelineHandoffArtifactMap(in map[string]PipelineHandoffArtifactRef) map[string]PipelineHandoffArtifactRef {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]PipelineHandoffArtifactRef, len(in))
	for k, v := range in {
		out[k] = cloneHandoffArtifactRef(v)
	}
	return out
}

func cloneHandoffArtifactRef(in PipelineHandoffArtifactRef) PipelineHandoffArtifactRef {
	out := in
	if len(in.EvidenceRefs) > 0 {
		out.EvidenceRefs = append([]string(nil), in.EvidenceRefs...)
	}
	if len(in.FailureFocus) > 0 {
		out.FailureFocus = append([]string(nil), in.FailureFocus...)
	}
	return out
}

func clonePipelineHandoffArtifactRefList(in []PipelineHandoffArtifactRef) []PipelineHandoffArtifactRef {
	if len(in) == 0 {
		return nil
	}
	out := make([]PipelineHandoffArtifactRef, len(in))
	for i, v := range in {
		out[i] = cloneHandoffArtifactRef(v)
	}
	return out
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

// Removed: validateQueuedArtifactConsumptionLocked.
//
// Queue-state was previously enforced as a hard runtime gate on terminal
// actions (handoff_next had to match the finalize_pipeline target set;
// challenge_agent and validate_work were either rejected or constrained).
// That conflated finalize_pipeline's "package an artifact for recipient X"
// contract with handoff_next's "route the next turn" contract, which the
// system prompts treat as separate concerns (tester finalizes for engineer,
// then hands off to inspector for review; inspector eventually routes to
// engineer with the artifact attached). The strict gate boxed agents in:
// when one terminal action was rejected, the fallbacks were too — leaving
// no escape and surfacing as opaque "stream error" pipeline aborts.
//
// The contract now lives in skill Usage/Avoid clauses (the LLM can read
// and reason about it) and in the dispatch path's passthrough behavior:
// queued artifacts ride along to non-recipient handoff targets via
// Context["inherited_artifacts"], age-tracked across iterations, and
// auto-discarded if they exceed pipelineArtifactMaxIterations as a
// bounded-loss convergence guard. Every terminal action carries a
// queue_state advisory in its result so the LLM sees what's pending and
// what was delivered, without any path being silently rejected.

func pipelineTargetSetsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, target := range a {
		seen[target]++
	}
	for _, target := range b {
		seen[target]--
		if seen[target] < 0 {
			return false
		}
	}
	for _, count := range seen {
		if count != 0 {
			return false
		}
	}
	return true
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
		// discard_queued_artifacts is the explicit-recovery affordance
		// surfaced to every pipeline agent. The protocol passes artifacts
		// through and auto-discards at age threshold; this skill lets the
		// LLM converge faster when it knows an artifact is stale before
		// the threshold trips.
		pipelineDiscardQueuedArtifactsSkill(cfg),
		// query_pipeline_state surfaces the authoritative protocol
		// projection — pending challenges, queued artifacts, required
		// terminal action, tester snapshot status — to the LLM so it
		// reasons about state instead of hitting validation gates blind.
		// Eliminates the class of bugs where finalize_pipeline fails
		// with "phantom" errors the LLM had no way to anticipate.
		QueryPipelineStateSkill(cfg),
	}
	if cfg.InspectorOT {
		out = append(out, pipelineFinalizePipelineSkill(cfg), pipelineHandoffOTSkill(cfg), pipelineDiscardPipelineSkill(cfg))
	}
	if cfg.TesterFinalize != nil {
		out = append(out, pipelineTesterFinalizePipelineSkill(cfg))
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
	targetEnum := pipelineChallengeTargetEnum(pipelineInvokerAgentType(cfg))
	return skills.NewSkill("challenge_agent").
		Description("Issue a targeted follow-up challenge when returned pipeline work is unclear, off-spec, incomplete, or otherwise needs direct response.").
		Domain("pipeline").
		Keywords("challenge", "peer review", "validation", "pipeline", "route").
		Priority(100).
		Usage("Use after you have inspected returned peer work and a specific unresolved gap remains that the target agent must answer directly. Legal regardless of whether tester verification artifacts are queued — the queue persists across challenge cycles and re-attaches to the eventual delivery hop via inherited_artifacts. A first challenge to a directed target is allowed. Repeated challenges require the directed pair's required new VFS evidence before you call this skill again. Do not use this for ordinary phase progression; use `handoff_next` for the normal top-level flow.").
		Satisfies("Creates an explicit targeted pipeline challenge and routes it to the challenged agent or cohort.").
		Requirement("Do not re-challenge the same agent without fresh pipeline VFS evidence. Inspector may re-challenge Tester, Engineer, or Designer only after that target changed VFS since Inspector's previous challenge to that target. Tester, Engineer, and Designer may re-challenge each other only after the target changed VFS since the challenger's previous challenge. Tester, Engineer, and Designer may re-challenge Inspector only after the challenger changed VFS in response to Inspector's previous answer.").
		Avoid("Do not use to advance the normal Inspector -> Tester -> Engineer/Designer -> Inspector phase flow, and do not use to ping the same pipeline agent again when the required side has not changed the pipeline VFS since the prior challenge on that directed pair. Do not challenge solely as a workaround for queued artifacts — they pass through; if you genuinely need to drop them, use `discard_queued_artifacts` with a reason.").
		EnumArrayParam("target_agents", "Target pipeline peers. Each entry must be one of the pipeline roles your agent is allowed to challenge.", "string", targetEnum, true).
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

// pipelineDiscardQueuedArtifactsSkill is the explicit-recovery affordance
// in the agentic queue model. The protocol no longer rejects terminal
// actions when artifacts are queued — it auto-passes them through and
// auto-discards at age threshold. But there are situations where the
// LLM knows an artifact is no longer relevant before that threshold
// (the underlying suite was wrong, the recipient is no longer needed,
// the requirement changed mid-turn). Rather than letting stale state
// accumulate or waiting for the age sweep, the LLM names the targets
// and a reason, and the protocol records both for observability and
// drops the entries from the queue.
func pipelineDiscardQueuedArtifactsSkill(cfg PipelineProtocolSkillConfig) *skills.Skill {
	return skills.NewSkill("discard_queued_artifacts").
		Description("Explicitly drop one or more queued tester verification artifacts when they are no longer relevant. Requires a reason; recorded for observability.").
		Domain("pipeline").
		Keywords("discard", "queue", "artifact", "pipeline", "recovery").
		Priority(60).
		Usage("Use when a previously finalized verification artifact is no longer relevant — the underlying suite was wrong, the recipient is no longer in scope, or the task requirement changed before the artifact was delivered. Provide one targets entry per recipient to drop and a single reason describing why the discard is correct. Discard is bounded loss: dropped artifacts are not delivered, and the LLM is responsible for re-finalizing when fresh evidence is available.").
		Avoid("Do not use as a routing shortcut — handoff_next/validate_work already pass queued artifacts through to non-recipient targets via inherited_artifacts. Discard is for genuine obsolescence, not for sidestepping delivery semantics.").
		Satisfies("Lets the LLM converge faster when it knows an artifact is stale instead of waiting for the iteration-age sweep to drop it automatically.").
		EnumArrayParam("targets", "Recipient names whose queued artifacts should be dropped.",
			"string",
			[]string{PipelineAgentEngineer, PipelineAgentDesigner, PipelineAgentInspector, "inspector", PipelineAgentTester, "tester"},
			true).
		StringParam("reason", "Why these queued artifacts are no longer relevant", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Targets []string `json:"targets"`
				Reason  string   `json:"reason"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			reason := strings.TrimSpace(params.Reason)
			if reason == "" {
				return nil, fmt.Errorf("reason is required: explain why these artifacts are no longer relevant")
			}
			if len(params.Targets) == 0 {
				return nil, fmt.Errorf("targets is required: name at least one recipient whose artifact should be dropped")
			}
			normalized := make([]string, 0, len(params.Targets))
			seen := make(map[string]struct{}, len(params.Targets))
			for _, t := range params.Targets {
				n := normalizePipelineAgentType(t)
				if n == "" {
					continue
				}
				if _, dup := seen[n]; dup {
					continue
				}
				seen[n] = struct{}{}
				normalized = append(normalized, n)
			}
			if len(normalized) == 0 {
				return nil, fmt.Errorf("targets must name at least one valid pipeline recipient")
			}
			state := PipelineProtocolStateFromContext(ctx)
			if state == nil {
				return nil, fmt.Errorf("pipeline protocol state not available")
			}
			queueBefore := state.QueuedArtifacts()
			dropped := make([]map[string]any, 0, len(normalized))
			notFound := make([]string, 0)
			for _, target := range normalized {
				if ref, ok := queueBefore[target]; ok {
					entry := pipelineHandoffArtifactRefMap(ref)
					entry["discard_reason"] = reason
					dropped = append(dropped, entry)
				} else {
					notFound = append(notFound, target)
				}
			}
			if len(dropped) == 0 {
				return map[string]any{
					"discard_queued_artifacts": true,
					"dropped":                  []map[string]any{},
					"not_found":                notFound,
					"reason":                   reason,
				}, nil
			}
			droppedTargets := make([]string, 0, len(dropped))
			for _, entry := range dropped {
				if t, _ := entry["target"].(string); t != "" {
					droppedTargets = append(droppedTargets, t)
				}
			}
			if err := state.consumeQueuedArtifacts(ctx, droppedTargets); err != nil {
				return nil, err
			}
			result := map[string]any{
				"discard_queued_artifacts": true,
				"dropped":                  dropped,
				"reason":                   reason,
			}
			if len(notFound) > 0 {
				result["not_found"] = notFound
			}
			return result, nil
		}).
		Build()
}

func pipelineHandoffNextSkill(cfg PipelineProtocolSkillConfig) *skills.Skill {
	targetEnum := pipelineChallengeTargetEnum(pipelineInvokerAgentType(cfg))
	return skills.NewSkill("handoff_next").
		Description("Select the next active top-level pipeline owner for ordinary phase progression and state the concrete request they should satisfy.").
		Domain("pipeline").
		Keywords("handoff", "next", "pipeline", "challenge", "route").
		Priority(100).
		Usage("End the current pipeline turn by handing top-level ownership to the next agent or cohort. Routes the next turn — separate concern from packaging artifacts. Tester can finalize for engineer/designer and then handoff_next to inspector for review; queued verification artifacts ride along on every dispatched task as `inherited_artifacts` until the receiving agent eventually routes to the actual recipient. Direct routing (handoff_next target == finalize target) delivers the artifact immediately as `verification_artifact_ref`. The result includes `queue_state` describing what was delivered, what was passed through, and what's still pending with age. Do not use this when you are directly answering an active challenge; use `validate_work` instead.").
		Avoid("Do not strand artifacts. If you finalize for engineer but never route work toward engineer (directly or via inspector relay), the artifact ages out at "+intToString(pipelineArtifactMaxIterations)+" iterations — bounded loss but wasted work. If a queued artifact is no longer relevant, invoke `discard_queued_artifacts` with a reason instead of letting it age out.").
		Satisfies("Records and dispatches the next top-level pipeline owner without hardcoding semantic stage transitions in the runtime.").
		EnumArrayParam("target_agents", "Next-owner pipeline roles. Each entry must be one of the pipeline peers your agent is allowed to hand off to.", "string", targetEnum, true).
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
		if refusal := pipelineIdenticalRequestRefusal(snapshot, agentType, targets, params.Request); refusal != nil {
			action := &PipelineTurnAction{
				Type:         PipelineProtocolActionRefusal,
				AgentType:    agentType,
				TargetAgents: append([]string(nil), targets...),
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
		return pipelineTurnSelectionResult(agentType, action, dispatch, state), nil
	}
	if err := state.recordHandoffAction(ctx, action); err != nil {
		return nil, err
	}
	if err := state.setTerminalAction(action); err != nil {
		return nil, err
	}
	return pipelineTurnSelectionResult(agentType, action, nil, state), nil
}

type pipelineDispatchSelection struct {
	CorrelationIDs []string
	TargetAgentIDs []string
}

// pipelineProtocolRouteOptions is a tagged-union over the three valid
// routing intents for a pipeline-protocol-dispatched task. Exactly one field
// should be non-zero per call; leaving them all empty yields a plain task
// route with no special UI framing. Mixing more than one is a caller bug —
// the fields are applied in the order checked below but later ones clobber
// earlier metadata keys, so the behavior is undefined for mixed inputs.
//
// Intent semantics:
//
//   - InterAgentBranch: the task is child work under a parent tool call
//     (e.g. the challenge_agent dispatching work to the challenged agent).
//     The TUI renders it as a nested row under the parent's tool call.
//
//   - ForwardHandoffParentCID: the task is a forward handoff that transfers
//     top-level turn ownership to a peer (e.g. handoff_next). The TUI
//     creates a new top-level entry for the recipient while preserving
//     parent-correlation lineage.
//
//   - OriginatorContinuationCID: the task is a response returning to its
//     originator so the originator can resume its own turn inline — NOT a
//     new top-level entry, NOT nested child work. Used for challenge
//     responses (validate_work → inspector) so the originator's
//     post-response tool calls append to the same chat entry as its
//     pre-challenge work. ResponderContinuationCID (optional) identifies
//     the child whose response this is, so the TUI can settle the pending
//     challenge row for that child.
type pipelineProtocolRouteOptions struct {
	InterAgentBranch          *InterAgentBranchSpec
	ForwardHandoffParentCID   string
	OriginatorContinuationCID string
	ResponderContinuationCID  string
}

func pipelineTurnSelectionResult(agentType string, action *PipelineTurnAction, dispatch *pipelineDispatchSelection, state *PipelineProtocolState) map[string]any {
	result := map[string]any{
		"selected":       true,
		"agent_type":     agentType,
		"target_agents":  append([]string(nil), action.TargetAgents...),
		"mode":           string(action.Mode),
		"protocol_scope": pipelineProtocolNamespace,
	}
	if strings.TrimSpace(action.ChallengeID) != "" {
		result["challenge_id"] = strings.TrimSpace(action.ChallengeID)
		result["thread_key"] = pipelineThreadPrefix + strings.TrimSpace(action.ChallengeID)
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
	if advisory := buildQueueStateAdvisory(state, action, dispatch); advisory != nil {
		result["queue_state"] = advisory
	}
	return result
}

// buildQueueStateAdvisory describes the post-dispatch queue situation in
// the terminal-action result. Pure informational — every terminal action
// succeeds regardless. The advisory tells the LLM what got delivered to
// its routed cohort, what got passed through as inherited_artifacts to
// the next hop, and what's still pending and at what age. The LLM reads
// this on its next turn (via the previous result reflected back in
// context) and can converge accordingly.
//
// Returns nil when there's nothing to advise (no queue activity, no
// dispatch, etc.) so the result key is omitted entirely rather than
// always present-but-empty — keeps low-noise turns clean.
func buildQueueStateAdvisory(state *PipelineProtocolState, action *PipelineTurnAction, dispatch *pipelineDispatchSelection) map[string]any {
	if state == nil || action == nil {
		return nil
	}
	queue := state.QueuedArtifacts()
	if len(queue) == 0 && (dispatch == nil || len(dispatch.TargetAgentIDs) == 0) {
		return nil
	}
	currentIteration := 0
	if snap := state.Snapshot(); snap != nil {
		currentIteration = snap.Iteration
	}
	dispatchedTargets := make(map[string]struct{}, len(action.TargetAgents))
	for _, target := range action.TargetAgents {
		dispatchedTargets[normalizePipelineAgentType(target)] = struct{}{}
	}
	advisory := map[string]any{
		"current_iteration":       currentIteration,
		"max_artifact_iterations": pipelineArtifactMaxIterations,
	}
	delivered := make([]map[string]any, 0)
	inheritedPassthrough := make([]map[string]any, 0)
	queuedRemaining := make([]map[string]any, 0)
	for target, ref := range queue {
		entry := pipelineHandoffArtifactRefMap(ref)
		age := 0
		if ref.QueuedAtIteration > 0 {
			age = currentIteration - ref.QueuedAtIteration
			entry["age_iterations"] = age
		}
		switch {
		case dispatch == nil:
			queuedRemaining = append(queuedRemaining, entry)
		case dispatchHasTarget(dispatchedTargets, target):
			delivered = append(delivered, entry)
		default:
			entry["via"] = append([]string(nil), action.TargetAgents...)
			if age >= pipelineArtifactMaxIterations {
				entry["advisory"] = "stranded ≥" + intToString(pipelineArtifactMaxIterations) + " iterations — will auto-discard on next dispatch unless delivered or explicitly discarded via discard_queued_artifacts"
			} else if age >= 2 {
				entry["advisory"] = "stranded — consider routing directly to recipient or invoking discard_queued_artifacts with a reason"
			}
			inheritedPassthrough = append(inheritedPassthrough, entry)
		}
	}
	if len(delivered) > 0 {
		advisory["delivered"] = delivered
	}
	if len(inheritedPassthrough) > 0 {
		advisory["inherited_passthrough"] = inheritedPassthrough
	}
	if len(queuedRemaining) > 0 {
		advisory["queued_remaining"] = queuedRemaining
	}
	if len(delivered) == 0 && len(inheritedPassthrough) == 0 && len(queuedRemaining) == 0 {
		return nil
	}
	return advisory
}

func dispatchHasTarget(set map[string]struct{}, target string) bool {
	_, ok := set[target]
	return ok
}

// intToString avoids importing strconv into this file just for the
// advisory string. Small enough to inline.
func intToString(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// PipelineValidationResult is the typed return of the validate_work skill.
// Replaces the previous ad-hoc map[string]any return shape. Implements
// InterAgentResponsePayload so the inter-agent dispatch layer and chat
// UI can read the summary field directly without defensive JSON
// parsing. All legacy field names are preserved as JSON tags so
// existing consumers (inter_agent_tool_event.go, downstream parsers)
// continue to work unchanged.
type PipelineValidationResult struct {
	Validated         bool   `json:"validated"`
	ChallengeID       string `json:"challenge_id"`
	RequestingAgent   string `json:"requesting_agent"`
	RequestingAgentID string `json:"requesting_agent_id,omitempty"`
	RespondingAgent   string `json:"responding_agent"`
	RespondingAgentID string `json:"responding_agent_id,omitempty"`
	Status            string `json:"status"`
	Summary           string `json:"summary,omitempty"`
	Forwarded         bool   `json:"forwarded,omitempty"`
	CorrelationID     string `json:"correlation_id,omitempty"`
	TargetAgentID     string `json:"target_agent_id,omitempty"`
	ProtocolScope     string `json:"protocol_scope,omitempty"`
}

// InterAgentSummary implements InterAgentResponsePayload.
func (r *PipelineValidationResult) InterAgentSummary() string {
	if r == nil {
		return ""
	}
	if r.Summary != "" {
		return r.Summary
	}
	// Fall back to a constructed summary when the handler didn't supply
	// one explicitly — keeps the chat row non-empty while preserving
	// typed-field access for other consumers.
	return "pipeline validation " + r.Status
}

func pipelineValidateWorkSkill(cfg PipelineProtocolSkillConfig) *skills.Skill {
	return skills.NewSkill("validate_work").
		Description("Respond to an active concrete challenge from another pipeline agent with a structured validation result and evidence.").
		Domain("pipeline").
		Keywords("validate", "challenge", "response", "evidence", "pipeline").
		Priority(100).
		Usage("Use only when another pipeline agent has an active challenge waiting for your response. This is the required response path for inspector, tester, engineer, or designer challenge turns. Ordinary top-level phase completion still uses `handoff_next`. If a queued tester verification artifact targets the requesting agent, validate_work delivers it as part of the response; queue entries for other targets ride along as inherited_artifacts and continue forward as the routing chain progresses.").
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
				if err := state.consumeQueuedArtifacts(ctx, []string{normalizePipelineAgentType(record.RequestingAgent)}); err != nil {
					return nil, err
				}
				if err := state.recordValidation(ctx, record); err != nil {
					return nil, err
				}
				if err := state.setTerminalAction(terminalAction); err != nil {
					return nil, err
				}
				return &PipelineValidationResult{
					Validated:         true,
					ChallengeID:       record.ChallengeID,
					RequestingAgent:   record.RequestingAgent,
					RequestingAgentID: record.RequestingAgentID,
					RespondingAgent:   record.RespondingAgent,
					RespondingAgentID: record.RespondingAgentID,
					Status:            record.Status,
					Summary:           record.Summary,
					Forwarded:         true,
					CorrelationID:     correlationID,
					TargetAgentID:     strings.TrimSpace(nextTask.TargetAgentID),
					ProtocolScope:     pipelineProtocolNamespace,
				}, nil
			}
			if err := state.setTerminalAction(terminalAction); err != nil {
				return nil, err
			}
			if err := state.recordValidation(ctx, record); err != nil {
				return nil, err
			}
			return &PipelineValidationResult{
				Validated:         true,
				ChallengeID:       record.ChallengeID,
				RequestingAgent:   record.RequestingAgent,
				RequestingAgentID: record.RequestingAgentID,
				RespondingAgent:   record.RespondingAgent,
				RespondingAgentID: record.RespondingAgentID,
				Status:            record.Status,
				Summary:           record.Summary,
				ProtocolScope:     pipelineProtocolNamespace,
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
			return &PipelineValidationProcessingResult{
				Processed:     true,
				ChallengeID:   challengeID,
				Decision:      string(decision),
				Summary:       summary,
				NextTargets:   normalizeStringList(params.NextTargets),
				ProtocolScope: pipelineProtocolNamespace,
			}, nil
		}).
		Build()
}

// PipelineValidationProcessingResult is the typed return of
// process_validation. Implements InterAgentResponsePayload.
type PipelineValidationProcessingResult struct {
	Processed     bool     `json:"processed"`
	ChallengeID   string   `json:"challenge_id"`
	Decision      string   `json:"decision"`
	Summary       string   `json:"summary,omitempty"`
	NextTargets   []string `json:"next_targets,omitempty"`
	ProtocolScope string   `json:"protocol_scope,omitempty"`
}

func (r *PipelineValidationProcessingResult) InterAgentSummary() string {
	if r == nil {
		return ""
	}
	if r.Summary != "" {
		return r.Summary
	}
	return "validation " + r.Decision
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

			priorTesterAudits := priorTesterFinalizeAudits(state)
			verificationRequest := finalizePipelineVerificationRequest(priorTesterAudits)
			if refusal := pipelineIdenticalRequestRefusal(snapshot, agentType, []string{PipelineAgentTester}, verificationRequest); refusal != nil {
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
				Reason:           finalizePipelineVerificationReason(priorTesterAudits),
				Request:          verificationRequest,
				RequiredOutput:   finalizePipelineVerificationRequiredOutput(priorTesterAudits),
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
				result := pipelineTurnSelectionResult(agentType, action, dispatch, state)
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
			result := pipelineTurnSelectionResult(agentType, action, nil, state)
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

			// Inspector-owned VFS authority: extract the review candidate
			// here, where the inspector decided the pipeline is done. The
			// orchestrator no longer reacts to "succeeded" status broadcasts
			// to mutate the VFS — that previously made the orchestrator the
			// effective owner of pipeline lifecycle and produced
			// "edit_pipeline_file failed: VFS not found" reports when the
			// orchestrator destroyed the VFS while the inspector still had
			// follow-up work queued. If the committer is missing the inspector
			// is misconfigured at registration; fail loudly rather than
			// silently broadcasting success without the side effect.
			//
			// When no pipeline task is bound (protocol-only tests, fallback
			// contexts), the extract is a no-op and we just publish the
			// protocol state change — matching the prior behavior for this
			// pre-refactor edge case.
			task := pipelineTerminalUpdateTask(ctx, cfg)
			pipelineID := ""
			if task != nil {
				pipelineID = strings.TrimSpace(task.TaskID)
			}
			candidateID := ""
			hadDraft := false
			var checkpointVersion versioning.SemanticVersion
			if pipelineID != "" {
				if cfg.Committer == nil {
					return nil, fmt.Errorf("handoff_to_ot requires a configured pipeline committer (inspector misconfiguration)")
				}
				committer := cfg.Committer()
				if committer == nil {
					return nil, fmt.Errorf("handoff_to_ot requires a configured pipeline committer (inspector misconfiguration)")
				}
				cid, hd, ver, extractErr := committer.ExtractReviewCandidate(ctx, pipelineID)
				if extractErr != nil {
					return nil, fmt.Errorf("extract pipeline review candidate %s: %w", pipelineID, extractErr)
				}
				candidateID = cid
				hadDraft = hd
				checkpointVersion = ver
			}

			if task != nil {
				PublishPipelineTaskSuccessUpdate(
					cfg.Route.eventBus(),
					agentType,
					task,
					summary,
					map[string]any{
						"summary":             summary,
						"evidence_refs":       evidenceRefs,
						"review_candidate_id": candidateID,
						"had_draft":           hadDraft,
						"checkpoint_version":  checkpointVersion.String(),
					},
					PipelineTaskAttempt(task),
				)
			}
			return map[string]any{
				"handoff_to_ot":       true,
				"agent_type":          agentType,
				"evidence_refs":       evidenceRefs,
				"review_candidate_id": candidateID,
				"had_draft":           hadDraft,
			}, nil
		}).
		Build()
}

// pipelineDiscardPipelineSkill is the inspector-only rollback authority.
// Replaces the orchestrator's reaction to "failed" status broadcasts so the
// pipeline VFS is only destroyed when the inspector explicitly decides the
// work is irrecoverable. Per-agent intermediate failures (engineer crash,
// tester timeout, etc.) leave the VFS alive so the inspector can dispatch a
// retry.
func pipelineDiscardPipelineSkill(cfg PipelineProtocolSkillConfig) *skills.Skill {
	return skills.NewSkill("discard_pipeline").
		Description("Rollback the active pipeline draft when the work is irrecoverable. Inspector only.").
		Domain("pipeline").
		Keywords("rollback", "discard", "abort", "fail", "pipeline").
		Priority(95).
		Usage("Use when an audit cycle has concluded that the pipeline cannot be salvaged — repeated failures, fundamentally wrong approach, or unrecoverable peer error. Prefer challenge_agent or handoff_next for any path where the work is still potentially recoverable. This is a destructive terminal action.").
		Requirement("Provide a concrete reason explaining why the pipeline must be discarded.").
		Satisfies("Removes the pipeline VFS draft from the session and clears its in-flight modifications.").
		StringParam("reason", "Why the pipeline must be discarded", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			agentType := pipelineProtocolAgentType(ctx, cfg)
			if agentType != PipelineAgentInspector {
				return nil, fmt.Errorf("discard_pipeline is only permitted for the pipeline inspector")
			}
			reason := strings.TrimSpace(params.Reason)
			if reason == "" {
				return nil, fmt.Errorf("reason is required")
			}
			task := pipelineTerminalUpdateTask(ctx, cfg)
			pipelineID := ""
			if task != nil {
				pipelineID = strings.TrimSpace(task.TaskID)
			}
			if pipelineID == "" {
				return nil, fmt.Errorf("discard_pipeline requires a pipeline task id")
			}
			if cfg.Committer == nil {
				return nil, fmt.Errorf("discard_pipeline requires a configured pipeline committer (inspector misconfiguration)")
			}
			committer := cfg.Committer()
			if committer == nil {
				return nil, fmt.Errorf("discard_pipeline requires a configured pipeline committer (inspector misconfiguration)")
			}
			if err := committer.Rollback(ctx, pipelineID); err != nil {
				return nil, fmt.Errorf("rollback pipeline %s: %w", pipelineID, err)
			}
			if task != nil {
				PublishPipelineTaskFailureUpdate(
					cfg.Route.eventBus(),
					agentType,
					task,
					reason,
					PipelineTaskAttempt(task),
				)
			}
			return map[string]any{
				"discard_pipeline": true,
				"agent_type":       agentType,
				"reason":           reason,
			}, nil
		}).
		Build()
}

// pipelineTesterFinalizePipelineSkill is the tester analog of the inspector's
// closure-gate finalize. It packages a per-recipient verification artifact for
// each target the LLM lists and queues the resulting refs in protocol state
// for handoff_next or validate_work to consume during dispatch. The skill is
// registered only when cfg.TesterFinalize is set, and the handler hard-rejects
// callers that aren't tester-pipeline.
func pipelineTesterFinalizePipelineSkill(cfg PipelineProtocolSkillConfig) *skills.Skill {
	return skills.NewSkill("finalize_pipeline").
		Description("Package the per-recipient tester verification handoff artifact and lock the turn into handoff_next (or validate_work when answering an active challenge). Tester only.").
		Domain("pipeline").
		Keywords("finalize", "verification", "handoff", "tester", "pipeline").
		Priority(100).
		Usage("Use after run_test_suite when the turn is ready to hand verification work to one or more downstream recipients. Provide one targets entry per recipient (engineer, designer, or both); each entry carries the recipient-specific summary, evidence refs, and optional failure focus. The artifact references this returns are auto-attached to the next handoff_next or validate_work — you do not need to pass them again.").
		Requirement("Requires that run_test_suite has produced a snapshot during this turn. The targets you list determine the only legal terminal action: a finalize for {engineer, designer} requires a cohort handoff_next to that exact set; a finalize for the challenger requires validate_work. Re-finalize is rejected unless a fresh run_test_suite has produced a new snapshot since the prior finalize.").
		Avoid("Do not use as a substitute for handoff_next; this skill packages artifacts but does not route the turn. Do not finalize for a target you are not actually handing work to. Do not finalize twice for the same target without running a fresh suite first.").
		Consumes(ArtifactTestSnapshot).
		Produces(ArtifactVerificationArtifact).
		ArrayObjectParam("targets", "Per-recipient verification specs. Provide one entry per recipient.",
			map[string]*skills.Property{
				"target": {
					Type:        "string",
					Description: "Canonical recipient agent type: engineer or designer (or inspector when answering an inspector challenge).",
				},
				"summary": {
					Type:        "string",
					Description: "What this recipient should know about the verification result and why it is relevant to their work.",
				},
				"evidence_refs": {
					Type:        "array",
					Description: "Files, tests, artifacts, or commands the recipient should inspect.",
					Items:       &skills.Property{Type: "string"},
				},
				"failure_focus": {
					Type:        "array",
					Description: "Specific failing test names or failure indicators this recipient should prioritize. Optional.",
					Items:       &skills.Property{Type: "string"},
				},
			},
			[]string{"target", "summary"},
			true,
		).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Targets []PipelineTesterFinalizeTargetSpec `json:"targets"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			agentType := pipelineProtocolAgentType(ctx, cfg)
			if agentType != PipelineAgentTester {
				return nil, fmt.Errorf("finalize_pipeline (tester variant) is only permitted for the pipeline tester")
			}
			if cfg.TesterFinalize == nil {
				return nil, fmt.Errorf("tester finalize callback not configured")
			}
			specs, err := normalizeTesterFinalizeSpecs(params.Targets)
			if err != nil {
				return nil, err
			}
			state := PipelineProtocolStateFromContext(ctx)
			if state == nil {
				return nil, fmt.Errorf("pipeline protocol state not available")
			}
			snapshot := state.Snapshot()
			if snapshot != nil && snapshot.PendingChallenge != nil {
				challenger := normalizePipelineAgentType(snapshot.PendingChallenge.RequestingAgent)
				if challenger == "" {
					return nil, fmt.Errorf("active challenge has no requesting agent identity")
				}
				if len(specs) != 1 || normalizePipelineAgentType(specs[0].Target) != challenger {
					return nil, NewPipelineProtocolError(
						"pipeline.finalize.challenge_target_mismatch",
						"finalize_pipeline",
						fmt.Sprintf("a pending challenge from %q is awaiting your response — finalize_pipeline must list exactly one targets entry for %q so the verification artifact threads into validate_work", challenger, challenger),
						fmt.Sprintf("call finalize_pipeline with targets=[{target:%q,...}] so the verification artifact threads into validate_work", challenger),
					)
				}
			} else if len(specs) > 1 {
				if !pipelineFinalizeTargetsAreCohort(specs) {
					return nil, fmt.Errorf("finalize_pipeline targets must be distinct downstream recipients")
				}
			}
			suiteID := ""
			if cfg.TesterCurrentSuiteID != nil {
				suiteID = strings.TrimSpace(cfg.TesterCurrentSuiteID())
			}
			if suiteID == "" {
				return nil, NewMissingArtifactProtocolError(
					"pipeline",
					"pipeline.finalize.requires_test_snapshot",
					"finalize_pipeline",
					ArtifactTestSnapshot,
				)
			}
			existing := state.QueuedArtifacts()
			for _, spec := range specs {
				target := normalizePipelineAgentType(spec.Target)
				if existing != nil {
					if prior, ok := existing[target]; ok && strings.TrimSpace(prior.SuiteID) == suiteID {
						return nil, fmt.Errorf("finalize_pipeline already published a verification artifact for %q from suite %s — run_test_suite again to capture fresh evidence before refinalizing for that recipient", target, suiteID)
					}
				}
			}
			refs, err := cfg.TesterFinalize(ctx, suiteID, specs)
			if err != nil {
				return nil, err
			}
			if len(refs) != len(specs) {
				return nil, fmt.Errorf("tester finalize callback returned %d refs for %d specs", len(refs), len(specs))
			}
			currentIteration := 0
			if snap := state.Snapshot(); snap != nil {
				currentIteration = snap.Iteration
			}
			normalized := make([]PipelineHandoffArtifactRef, len(refs))
			for i, ref := range refs {
				if strings.TrimSpace(ref.ArtifactID) == "" {
					return nil, fmt.Errorf("tester finalize callback returned an empty artifact id for target %q", specs[i].Target)
				}
				ref.Target = normalizePipelineAgentType(specs[i].Target)
				if ref.SuiteID == "" {
					ref.SuiteID = suiteID
				}
				if ref.QueuedAtIteration == 0 {
					ref.QueuedAtIteration = currentIteration
				}
				normalized[i] = ref
			}
			if err := state.recordTesterFinalize(ctx, normalized); err != nil {
				return nil, err
			}
			result := map[string]any{
				"finalize_pipeline":    true,
				"agent_type":           agentType,
				"suite_id":             suiteID,
				"queued_targets":       collectTargets(normalized),
				"queued_artifacts":     buildQueuedArtifactsResult(normalized),
				"required_next_action": pipelineTesterRequiredNextAction(snapshot),
			}
			return result, nil
		}).
		Build()
}

func normalizeTesterFinalizeSpecs(in []PipelineTesterFinalizeTargetSpec) ([]PipelineTesterFinalizeTargetSpec, error) {
	if len(in) == 0 {
		return nil, fmt.Errorf("targets is required: provide at least one per-recipient verification spec")
	}
	out := make([]PipelineTesterFinalizeTargetSpec, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for i, spec := range in {
		target := normalizePipelineAgentType(spec.Target)
		if target == "" {
			return nil, fmt.Errorf("targets[%d].target is required", i)
		}
		if target == PipelineAgentTester {
			return nil, fmt.Errorf("targets[%d].target %q is invalid: the tester cannot finalize for itself", i, target)
		}
		if _, dup := seen[target]; dup {
			return nil, fmt.Errorf("targets[%d].target %q is duplicated", i, target)
		}
		seen[target] = struct{}{}
		summary := strings.TrimSpace(spec.Summary)
		if summary == "" {
			return nil, fmt.Errorf("targets[%d].summary is required", i)
		}
		out = append(out, PipelineTesterFinalizeTargetSpec{
			Target:       target,
			Summary:      summary,
			EvidenceRefs: normalizeStringList(spec.EvidenceRefs),
			FailureFocus: normalizeStringList(spec.FailureFocus),
		})
	}
	return out, nil
}

func pipelineFinalizeTargetsAreCohort(specs []PipelineTesterFinalizeTargetSpec) bool {
	for _, spec := range specs {
		switch normalizePipelineAgentType(spec.Target) {
		case PipelineAgentEngineer, PipelineAgentDesigner, PipelineAgentInspector:
			continue
		default:
			return false
		}
	}
	return true
}

func pipelineTesterRequiredNextAction(snapshot *PipelineProtocolSnapshot) string {
	if snapshot != nil && snapshot.PendingChallenge != nil {
		return "validate_work"
	}
	return "handoff_next"
}

func collectTargets(refs []PipelineHandoffArtifactRef) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		out = append(out, normalizePipelineAgentType(ref.Target))
	}
	return out
}

func buildQueuedArtifactsResult(refs []PipelineHandoffArtifactRef) []map[string]any {
	out := make([]map[string]any, 0, len(refs))
	for _, ref := range refs {
		out = append(out, pipelineHandoffArtifactRefMap(ref))
	}
	return out
}

// pipelineInvokerAgentType returns the invoker's role at skill-build time. Used
// to bake a per-invoker Items.Enum into tools like challenge_agent /
// handoff_next so the LLM cannot emit disallowed targets such as "orchestrator"
// or the invoker itself.
func pipelineInvokerAgentType(cfg PipelineProtocolSkillConfig) string {
	if cfg.AgentType == nil {
		return ""
	}
	return normalizePipelineAgentType(cfg.AgentType())
}

// pipelineChallengeTargetEnum returns the static list of pipeline peer
// identifiers that the given invoker is allowed to name in target_agents.
// Both the canonical form ("inspector-pipeline", "tester-pipeline") and the
// short aliases ("inspector", "tester") are accepted, mirroring the
// documentation the prompts use. The invoker itself is always excluded.
func pipelineChallengeTargetEnum(invoker string) []string {
	invokerNorm := normalizePipelineAgentType(invoker)
	type pair struct {
		canonical string
		alias     string
	}
	all := []pair{
		{PipelineAgentInspector, "inspector"},
		{PipelineAgentTester, "tester"},
		{PipelineAgentEngineer, ""},
		{PipelineAgentDesigner, ""},
	}
	out := make([]string, 0, 6)
	for _, p := range all {
		if normalizePipelineAgentType(p.canonical) == invokerNorm {
			continue
		}
		out = append(out, p.canonical)
		if p.alias != "" {
			out = append(out, p.alias)
		}
	}
	return out
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
			return nil, fmt.Errorf(
				"target agent %q is not a pipeline peer — pipeline challenge_agent and handoff_next only accept inspector-pipeline, tester-pipeline, engineer, or designer. "+
					"Orchestrator, architect, and every other agent type are outside the pipeline protocol and cannot be challenged from inside a pipeline turn; "+
					"if you need workflow or plan-level escalation, end the pipeline turn with the appropriate terminal action instead",
				value,
			)
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
	if parentCID := strings.TrimSpace(options.ForwardHandoffParentCID); parentCID != "" {
		metadata = RouteMetadataWithExplicitTopLevelTransfer(branchCtx, metadata, parentCID)
	}
	if originatorCID := strings.TrimSpace(options.OriginatorContinuationCID); originatorCID != "" {
		metadata = RouteMetadataWithOriginatorContinuation(branchCtx, metadata, originatorCID, strings.TrimSpace(options.ResponderContinuationCID))
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

// pipelineValidationRouteOptions builds the dispatch options for a
// validate_work response routed from the challenged agent back to the
// originator (inspector). The originator should resume its existing chat
// entry inline — the challenge is one interaction, not two — so this uses
// the OriginatorContinuationCID intent rather than the forward-handoff
// top-level-transfer intent.
//
// The originator's correlation is read from the tester ctx's branch
// metadata (stamped by BeginInterAgentBranch on the outbound challenge
// leg). The responder's correlation is the tester's current stream CID,
// which the TUI uses to settle the originator's pending challenge row for
// this specific child. When no branch metadata is present (legacy /
// non-challenge paths) no continuation hint is emitted and the dispatch
// falls back to a plain task route.
func pipelineValidationRouteOptions(ctx context.Context) pipelineProtocolRouteOptions {
	stream, ok := StreamMetadataFromContext(ctx)
	if !ok {
		return pipelineProtocolRouteOptions{}
	}
	originatorCID := strings.TrimSpace(pipelineTaskMetadataString(stream.Metadata, streamMetadataParentCorrelation))
	if originatorCID == "" {
		return pipelineProtocolRouteOptions{}
	}
	responderCID := strings.TrimSpace(stream.CorrelationID)
	return pipelineProtocolRouteOptions{
		OriginatorContinuationCID: originatorCID,
		ResponderContinuationCID:  responderCID,
	}
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
	if err := state.sweepAgedArtifacts(ctx); err != nil {
		return nil, err
	}
	tasks, err := buildPipelineHandoffTasks(state, task, action)
	if err != nil {
		return nil, err
	}
	dispatch := &pipelineDispatchSelection{
		CorrelationIDs: make([]string, 0, len(tasks)),
		TargetAgentIDs: make([]string, 0, len(tasks)),
	}
	dispatchedTargets := make([]string, 0, len(tasks))
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
		dispatchedTargets = append(dispatchedTargets, normalizePipelineAgentType(nextTask.AgentType))
	}
	// Consume both direct deliveries and stranded artifacts ridden along
	// in inherited_artifacts. Direct: target was in dispatch cohort, the
	// artifact was attached as verification_artifact_ref. Stranded: target
	// was NOT in dispatch cohort, the artifact was attached as
	// inherited_artifacts on every dispatched task — responsibility for
	// onward delivery now belongs to those receivers. Either way the
	// queue entry is consumed: the protocol has handed it off.
	consumeTargets := make([]string, 0, len(tasks)+len(state.QueuedArtifacts()))
	consumeTargets = append(consumeTargets, dispatchedTargets...)
	for target := range state.QueuedArtifacts() {
		dispatched := false
		for _, t := range dispatchedTargets {
			if t == target {
				dispatched = true
				break
			}
		}
		if !dispatched {
			consumeTargets = append(consumeTargets, target)
		}
	}
	if err := state.consumeQueuedArtifacts(ctx, consumeTargets); err != nil {
		return nil, err
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
	queue := state.QueuedArtifacts()
	directTargets := make(map[string]struct{}, len(action.TargetAgents))
	for _, rawTarget := range action.TargetAgents {
		directTargets[normalizePipelineAgentType(rawTarget)] = struct{}{}
	}
	// Stranded artifacts are queue entries whose recipient is NOT in the
	// current dispatch cohort. Per the agentic dispatch contract, they
	// ride along on every dispatched task as inherited_artifacts so the
	// next hop (typically inspector) can route them onward to the actual
	// recipient when it next dispatches. The first dispatch consumes the
	// stranded entries from the queue — they have been handed off to the
	// next hop's responsibility, even though they aren't yet delivered to
	// their final target.
	stranded := make([]PipelineHandoffArtifactRef, 0)
	for target, ref := range queue {
		if _, direct := directTargets[target]; direct {
			continue
		}
		stranded = append(stranded, ref)
	}
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
		if ref, ok := queue[targetAgent]; ok {
			next.Context["verification_artifact_ref"] = pipelineHandoffArtifactRefMap(ref)
		}
		// Inherited artifacts: queued for OTHER recipients that this hop
		// should carry forward. The receiving agent reads them from
		// task.Context["inherited_artifacts"] and decides whether to route
		// to those recipients on its next handoff. Inheritance is an
		// agentic responsibility — the protocol delivers context, the LLM
		// decides what to do with it.
		if len(stranded) > 0 {
			inherited := make([]map[string]any, 0, len(stranded))
			for _, ref := range stranded {
				inherited = append(inherited, pipelineHandoffArtifactRefMap(ref))
			}
			next.Context["inherited_artifacts"] = inherited
		}
		nextTasks = append(nextTasks, next)
	}
	return nextTasks, nil
}

func pipelineHandoffArtifactRefMap(ref PipelineHandoffArtifactRef) map[string]any {
	out := map[string]any{
		"artifact_id": strings.TrimSpace(ref.ArtifactID),
		"target":      normalizePipelineAgentType(ref.Target),
	}
	if kind := strings.TrimSpace(ref.Kind); kind != "" {
		out["kind"] = kind
	}
	if suite := strings.TrimSpace(ref.SuiteID); suite != "" {
		out["suite_id"] = suite
	}
	if summary := strings.TrimSpace(ref.Summary); summary != "" {
		out["summary"] = summary
	}
	if refs := normalizeStringList(ref.EvidenceRefs); len(refs) > 0 {
		out["evidence_refs"] = refs
	}
	if focus := normalizeStringList(ref.FailureFocus); len(focus) > 0 {
		out["failure_focus"] = focus
	}
	if ref.QueuedAtIteration > 0 {
		out["queued_at_iteration"] = ref.QueuedAtIteration
	}
	return out
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
	if ref, ok := state.QueuedArtifactForTarget(requestingAgent); ok {
		next.Context["verification_artifact_ref"] = pipelineHandoffArtifactRefMap(ref)
	}
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

// pipelineIdenticalRequestRefusal refuses a challenge whose request text is
// byte-identical (after whitespace normalization) to a prior challenge from
// the same requesting agent to the same target set earlier in this task's
// protocol history. Protects against duplicate-text challenges regardless of
// whether workspace state changed between rounds — the workspace-fingerprint
// check already handles the "nothing changed" case; this check catches
// "something changed but the new challenge asks the same question word-for-
// word", which is the observed symptom when a hardcoded template re-fires.
func pipelineIdenticalRequestRefusal(
	snapshot *PipelineProtocolSnapshot,
	requestingAgent string,
	targets []string,
	requestText string,
) *pipelineChallengeRefusal {
	normalized := normalizePipelineRequestText(requestText)
	if snapshot == nil || normalized == "" {
		return nil
	}
	requestingAgent = normalizePipelineAgentType(requestingAgent)
	targetKey := pipelineProtocolTargetKey(targets)
	if requestingAgent == "" || targetKey == "" {
		return nil
	}
	for i := len(snapshot.RecentEvents) - 1; i >= 0; i-- {
		event := snapshot.RecentEvents[i]
		if !event.CreatesChallenge {
			continue
		}
		if normalizePipelineAgentType(event.AgentType) != requestingAgent {
			continue
		}
		if pipelineProtocolTargetKey(event.Targets) != targetKey {
			continue
		}
		if normalizePipelineRequestText(event.Summary) != normalized {
			continue
		}
		targetLabel := strings.Join(normalizeStringList(targets), ", ")
		if strings.TrimSpace(targetLabel) == "" {
			targetLabel = "the same target"
		}
		return &pipelineChallengeRefusal{
			RefusedBy: "pipeline-protocol",
			Reason: fmt.Sprintf(
				"This is a byte-identical repeat of an earlier challenge to %s in this task. Challenges must either advance the request text with new focus or acknowledge resolution — do not re-issue the same template.",
				targetLabel,
			),
			ResumeConditions: []string{
				"Rewrite the request with concrete, new concerns (e.g., issues unresolved since the prior round, new evidence to validate).",
				fmt.Sprintf("If no new concerns remain, accept the prior %s verdict via process_validation and proceed to the next protocol action instead of re-challenging.", targetLabel),
			},
		}
	}
	return nil
}

func normalizePipelineRequestText(text string) string {
	return strings.Join(strings.Fields(text), " ")
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

// priorTesterFinalizeAudits returns processed validations where the tester
// responded to a prior finalize_pipeline-generated verification challenge.
// Used to vary the outgoing challenge text across audit cycles so tester sees
// context from prior rounds instead of an identical canned request.
func priorTesterFinalizeAudits(state *PipelineProtocolState) []PipelineValidationProcessing {
	if state == nil {
		return nil
	}
	processed := state.ProcessedValidations()
	out := make([]PipelineValidationProcessing, 0, len(processed))
	for _, entry := range processed {
		if entry.Validation == nil {
			continue
		}
		if normalizePipelineAgentType(entry.Validation.RespondingAgent) != PipelineAgentTester {
			continue
		}
		if !containsNormalizedString(entry.Validation.ChallengeReferences, finalizePipelineVerificationReference) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// finalizePipelineVerificationRequest builds the request text sent to tester
// in a finalize_pipeline-generated audit challenge. On the first cycle this
// is the canonical audit prompt. On subsequent cycles the text incorporates
// the iteration number, prior tester verdicts, and inspector decisions so the
// challenge carries continuity across rounds rather than repeating identical
// text byte-for-byte.
func finalizePipelineVerificationRequest(priors []PipelineValidationProcessing) string {
	base := "Audit the Engineer and Designer outputs as quality production code, not excessive or agentic slop. Verify correctness, robustness, performance, scope discipline, and production quality, and penalize unrelated code, premature abstraction, or verbosity. Also verify that all required tests are implemented and passing and that the tests add real value rather than noisy or low-quality surface area."
	if len(priors) == 0 {
		return base
	}
	var b strings.Builder
	b.WriteString(base)
	b.WriteString(fmt.Sprintf("\n\nThis is re-audit cycle %d. Prior audit history on this pipeline task:", len(priors)+1))
	for i, entry := range priors {
		testerSummary := shortenFinalizeAuditSummary(priorTesterSummary(entry), 220)
		inspectorDecision := strings.TrimSpace(string(entry.Decision))
		inspectorSummary := shortenFinalizeAuditSummary(entry.Summary, 180)
		b.WriteString(fmt.Sprintf("\n  Round %d:", i+1))
		if testerSummary != "" {
			b.WriteString("\n    - Tester verdict: " + testerSummary)
		}
		if inspectorDecision != "" || inspectorSummary != "" {
			b.WriteString("\n    - Inspector decision: " + strings.TrimSpace(inspectorDecision+" — "+inspectorSummary))
		}
	}
	b.WriteString("\n\nFocus this round on what changed since your previous verdict. Validate whether each unresolved concern from earlier rounds has been addressed by the new engineer/designer output, and flag any new concerns introduced by the latest changes. Do not restate resolved issues as if they are new.")
	return b.String()
}

// finalizePipelineVerificationReason varies the audit-cycle reason so repeat
// rounds are distinguishable in the protocol event stream.
func finalizePipelineVerificationReason(priors []PipelineValidationProcessing) string {
	if len(priors) == 0 {
		return "Run the inspector audit cycle before OT handoff."
	}
	return fmt.Sprintf("Run re-audit cycle %d before OT handoff; prior rounds did not settle the pipeline for acceptance.", len(priors)+1)
}

// finalizePipelineVerificationRequiredOutput narrows the required deliverables
// on re-audit rounds so tester responds to remaining concerns rather than
// re-auditing settled criteria.
func finalizePipelineVerificationRequiredOutput(priors []PipelineValidationProcessing) []string {
	if len(priors) == 0 {
		return []string{
			"State whether all required tests are implemented and passing.",
			"Audit the engineer/designer implementation for correctness, robustness, performance, scope discipline, and production quality.",
			"Call out any excessive code, premature abstraction, verbosity, low-value tests, or agentic slop that should force another cycle.",
		}
	}
	return []string{
		"State whether all required tests are implemented and passing on this re-audit.",
		"For each concern you raised in a prior round, state explicitly whether it is now resolved, still open, or superseded by newer evidence.",
		"Call out any newly introduced excessive code, premature abstraction, verbosity, low-value tests, or agentic slop from the latest changes.",
		fmt.Sprintf("If your verdict is unchanged from round %d, say so and cite the concrete reason the latest changes did not move the verdict.", len(priors)),
	}
}

func priorTesterSummary(entry PipelineValidationProcessing) string {
	if entry.Validation != nil {
		if summary := strings.TrimSpace(entry.Validation.Summary); summary != "" {
			return summary
		}
	}
	return ""
}

func shortenFinalizeAuditSummary(text string, max int) string {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" || max <= 0 || len(text) <= max {
		return text
	}
	cut := strings.LastIndex(text[:max], " ")
	if cut <= 0 {
		cut = max
	}
	return strings.TrimSpace(text[:cut]) + "…"
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
