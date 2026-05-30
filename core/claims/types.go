// Package claims implements the claims-based execution model — a universal
// coordination primitive for sylk's multi-agent system. Every agent
// interaction (task assignment, challenge, consultation, corrective
// guidance, archival) flows through the same machinery: actions contain
// claims, subjects respond with testaments carrying artifacts, and
// issuers validate artifacts against the claim's validations.
//
// See docs/CLAIMS.md for the full design.
package claims

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ────────────────────────────────────────────────────────────────────
// Supporting types
// ────────────────────────────────────────────────────────────────────

// Relation expresses a relationship between any two entities in the
// claims system. All structural, causal, and agent relationships are
// encoded uniformly — there are no special-case fields for issuer,
// subject, parent action, dependencies, or supersession.
type Relation struct {
	// Related is the ID of the related entity.
	Related string `json:"related"`

	// RelatedType identifies what kind of entity Related points to.
	// One of: "action", "claim", "testament", "validation",
	// "artifact", "agent".
	RelatedType string `json:"related_type"`

	// Relationship describes how the entities are related.
	// Open string — not a closed enum. Common values documented in
	// the RelationshipXxx constants below.
	Relationship string `json:"relationship"`
}

// Common Relationship values. Not a closed set — new relationships
// can be added without schema changes.
const (
	// Agent relationships.
	RelationshipIssuer    = "issuer"    // agent that created/issued this object
	RelationshipSubject   = "subject"   // agent this object is directed at
	RelationshipEvaluator = "evaluator" // agent that evaluated a validation

	// Structural relationships (action/claim/testament/artifact grouping).
	RelationshipClaimAction     = "claim_action"     // parent claim action
	RelationshipTestamentAction = "testament_action" // parent testament action
	RelationshipClaim           = "claim"            // the claim this responds to
	RelationshipTestament       = "testament"        // the testament this artifact belongs to

	// Causal and semantic relationships.
	RelationshipSupersedes      = "supersedes"       // replaces the related object
	RelationshipDependsOn       = "depends_on"       // cannot proceed until related is satisfied
	RelationshipCausedBy        = "caused_by"        // created in response to the related object
	RelationshipCorrects        = "corrects"         // corrective work intended to repair the related object
	RelationshipRefines         = "refines"          // narrows or clarifies the related object
	RelationshipConflictsWith   = "conflicts_with"   // contradicts the related object
	RelationshipDerivedFrom     = "derived_from"     // content derived from the related object
	RelationshipReviews         = "reviews"          // evaluates the related object
	RelationshipAmends          = "amends"           // modifies but does not replace the related object
	RelationshipDirectAddressed = "direct_addressed" // user directly addressed this agent

	// Cycle / lifecycle relationships (UI_DESIGN.md §2.6). The bridge's
	// cycle resolver reads these to compute parent/child attribution and
	// to pair started/completed artifacts; nothing in the agent runtime
	// or the UI walks them directly.
	RelationshipHandoffFrom = "handoff_from" // successor cycle root → predecessor cycle root
	RelationshipCompletes   = "completes"    // completion artifact → started artifact
)

// RelatedTypeXxx constants for Relation.RelatedType.
const (
	RelatedTypeAction         = "action"
	RelatedTypeClaim          = "claim"
	RelatedTypeTestament      = "testament"
	RelatedTypeValidation     = "validation"
	RelatedTypeArtifact       = "artifact"
	RelatedTypeAgent          = "agent"
	RelatedTypeIdempotencyKey = "idempotency_key"
)

// StatusChange records a single status transition on a stateful object
// (Action, Claim, or Validation). Every transition is recorded — the
// full lifecycle is auditable.
type StatusChange struct {
	From          string    `json:"from"`                     // previous status value
	To            string    `json:"to"`                       // new status value
	Reason        string    `json:"reason"`                   // why the transition happened
	AgentID       string    `json:"agent_id,omitempty"`       // legacy agent instance alias
	ParticipantID string    `json:"participant_id,omitempty"` // canonical participant instance
	Changed       time.Time `json:"changed"`                  // UTC timestamp
}

// ParticipantRef is the generalized identity reference used by the
// artifacts-and-validations lifecycle. AgentRef remains the concrete
// in-repo representation during migration.
type ParticipantRef = AgentRef

// ClaimScopeEntry identifies one element of a claim's affected scope.
type ClaimScopeEntry struct {
	Kind string `json:"kind"` // "file", "symbol", "api", "test_surface", "component", "ux_surface"
	Key  string `json:"key"`  // path, symbol name, endpoint, surface ID, etc.
}

// ExpectedToolCall is a durable Sylk skill invocation specification.
// It is not a provider tool-call message; it describes work the
// receiving agent or evaluator should attempt, justify skipping, or
// record as an error artifact.
type ExpectedToolCall struct {
	ID                string                 `json:"id,omitempty"`
	Tool              string                 `json:"tool"`
	Arguments         map[string]any         `json:"arguments,omitempty"`
	Purpose           string                 `json:"purpose,omitempty"`
	Required          bool                   `json:"required,omitempty"`
	ProducesArtifacts []string               `json:"produces_artifacts,omitempty"`
	TimeoutSeconds    int                    `json:"timeout_seconds,omitempty"`
	Policy            ExpectedToolCallPolicy `json:"policy,omitempty"`
}

// ExpectedToolCallPolicy carries execution hints for an expected tool
// call. These are policy inputs, not authority grants: the runtime still
// validates tool availability, permissions, user approvals, and local
// safety before executing anything.
type ExpectedToolCallPolicy struct {
	AllowAgentSubstitution bool `json:"allow_agent_substitution,omitempty"`
	RequiresUserApproval   bool `json:"requires_user_approval,omitempty"`
}

// ScopeOverlaps returns true if any entry in a shares kind+key with
// any entry in b.
func ScopeOverlaps(a, b []ClaimScopeEntry) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	type key struct{ kind, val string }
	set := make(map[key]struct{}, len(b))
	for _, e := range b {
		set[key{strings.TrimSpace(e.Kind), strings.TrimSpace(e.Key)}] = struct{}{}
	}
	for _, e := range a {
		if _, ok := set[key{strings.TrimSpace(e.Kind), strings.TrimSpace(e.Key)}]; ok {
			return true
		}
	}
	return false
}

// ────────────────────────────────────────────────────────────────────
// Enums
// ────────────────────────────────────────────────────────────────────

// ActionType classifies the action a set of claims belongs to.
type ActionType string

const (
	ActionTypeTask         ActionType = "task"         // work assignment (architect-assembled, inspector-issued)
	ActionTypeChallenge    ActionType = "challenge"    // dispute peer work
	ActionTypeConsultation ActionType = "consultation" // request information from peer
	ActionTypeCorrective   ActionType = "corrective"   // guide misbehaving agent back on track
	ActionTypeArchival     ActionType = "archival"     // summarize, ingest, record
	ActionTypePrompt       ActionType = "prompt"       // user prompt classification/decomposition
	ActionTypeTestament    ActionType = "testament"    // agent testifying about work performed, findings, or failures
	ActionTypeBoot         ActionType = "boot"         // boot pipeline phase execution
	ActionTypeActivation   ActionType = "activation"   // agent container activation
	ActionTypeShutdown     ActionType = "shutdown"     // graceful agent shutdown
	ActionTypeHandoff      ActionType = "handoff"      // clean transfer of top-level cycle ownership (UI_DESIGN.md §2.2)
	ActionTypeCheckpoint   ActionType = "checkpoint"   // Guardian-issued safety checkpoint requiring user approval (e.g. periodic git safety snapshot)
	// ActionTypeGuardianCheck is the structured claim posted by the
	// tool runtime when an approval-gated tool needs guardian review.
	// Subject = "guardian"; the responding testament carries the
	// grant verdict (allow/deny). The bridge projects the exchange
	// from this structured claim and its testament lifecycle.
	ActionTypeGuardianCheck ActionType = "guardian_check"
	// ActionTypeConsultContinuation is a claim type that captures the
	// serialized LLM turn state of an agent that yielded mid-tool-loop
	// to await peer consults. The agent posts one of these claims when
	// the LLM calls await_consults; the artifact carries the snapshot
	// (messages, tools, accumulator, ledger, awaited consult IDs).
	// When the awaited consults all resolve, the agent's inbox
	// dispatcher resumes the continuation: re-acquires a replica,
	// restores the snapshot, and re-enters ExecuteTurnLoop.
	//
	// System-internal: the continuation is the agent's own bookkeeping
	// (not a peer-to-self request) so it MUST NOT wake other agents
	// via the inbox-delta path. IsSystemInternalAction returns true
	// for this type and AgentActivationActionTypes excludes it.
	ActionTypeConsultContinuation ActionType = "consult_continuation"
)

// IsSystemInternalAction reports whether an ActionType is system-
// internal — i.e. a lifecycle / housekeeping action that exists for
// the runtime's own bookkeeping and MUST NOT trigger agent inference
// via receiver-specific claim.posted lifecycle deltas. The amplifier skips
// lifecycle delivery for these, and the inbox's standing-subscription
// matcher rejects them as defense-in-depth
// (ClaimsInbox.matchesStandingSubscription).
//
// The split is deliberate and CLOSED:
//   - Activation set (legitimate agent wakes): task, handoff,
//     consultation, challenge, corrective, prompt, guardian_check.
//   - System-internal (never agent wakes): boot, activation, shutdown,
//     archival, testament, checkpoint.
//
// Testament is on the system list because TestamentAction posts go
// via SubmitTestaments, not PostAction; if one ever leaked through
// PostAction, treating it as system stops the storm.
func IsSystemInternalAction(t ActionType) bool {
	switch t {
	case ActionTypeBoot,
		ActionTypeActivation,
		ActionTypeShutdown,
		ActionTypeArchival,
		ActionTypeTestament,
		ActionTypeCheckpoint,
		ActionTypeConsultContinuation:
		return true
	}
	return false
}

// AgentActivationActionTypes returns the closed set of action types
// that legitimately wake an agent via a standing inbox subscription.
// Mirrors IsSystemInternalAction's complement; the lists must remain
// disjoint (every defined ActionType is either activation-bearing or
// system-internal — see TestActionType_PartitionedByVisibility).
func AgentActivationActionTypes() []ActionType {
	return []ActionType{
		ActionTypeTask,
		ActionTypeHandoff,
		ActionTypeConsultation,
		ActionTypeChallenge,
		ActionTypeCorrective,
		ActionTypePrompt,
		ActionTypeGuardianCheck,
	}
}

// Artifact kind constants for lifecycle claims.
const (
	ArtifactKindTiming      = "timing"       // phase duration
	ArtifactKindStats       = "stats"        // counts: files, nodes, edges, etc.
	ArtifactKindReadiness   = "readiness"    // agent readiness status
	ArtifactKindAgentID     = "agent_id"     // container/agent identifier
	ArtifactKindShutdownAck = "shutdown_ack" // shutdown acknowledgment
	ArtifactKindStateHash   = "state_hash"   // hash of persisted state
)

// Continuation artifact kinds carried by ActionTypeConsultContinuation
// claims. The agent that yielded mid-tool-loop posts one
// ContinuationContext artifact (the serialized TurnState JSON) plus
// one ContinuationAwait artifact per consult it is awaiting.
const (
	ArtifactKindContinuationContext = "continuation_context" // serialized TurnState JSON
	ArtifactKindContinuationAwait   = "continuation_await"   // one per awaited consult_id
	ArtifactKindContinuationVersion = "continuation_version" // codec version, for binary-upgrade strandedness checks

	// ArtifactKindPlanHandoffPayload is the architect's serialized
	// PlanHandoff JSON, attached to the testament submitted at plan
	// finalization. The architect's user-accept handoff claim
	// (subject=orchestrator, ActionType=Handoff) carries a validation
	// referencing this artifact's ID, so the orchestrator's claim
	// intake can resolve the artifact directly and run ingestPlan
	// deterministically — no LLM tool loop, no parallel bus message.
	ArtifactKindPlanHandoffPayload = "plan_handoff_payload"

	// ArtifactKindPlanMarkdown is the architect's human-reviewable
	// implementation plan. When presentable, this is the canonical
	// source for chat and approval review surfaces; the handoff payload
	// remains separate internal evidence for orchestration.
	ArtifactKindPlanMarkdown = "plan_markdown"

	// ArtifactMetadataContentTruncated marks projection copies whose
	// Reference has been shortened. The board retains the full artifact;
	// user-facing projection consumers must not render the shortened
	// Reference as complete content.
	ArtifactMetadataContentTruncated = "content_truncated"
	ArtifactMetadataContentSize      = "content_size"
	ArtifactMetadataContentInline    = "content_inline"

	// ArtifactKindAgentState records an agent's state transition
	// (Reasoning, ToolExecuting, DispatchingToPeer, etc.) on its
	// in-flight testament. Reference is the human-readable detail.
	// Metadata carries:
	//   - "state":              categorical AgentActivityState string
	//   - "peer_agent_type":    optional, when transition involves a peer
	//   - "peer_correlation_id": optional, peer's request correlation
	//   - "peer_claim_id":      optional, claim ID the peer is processing
	//   - "at":                  RFC3339Nano timestamp
	// Complements the entity's Context field (which holds the
	// latest-only narrative). The artifact stream is the durable
	// transition history — replayable, auditable, time-travelable.
	// See docs/CLAIMS_UI.md "Why agent_state artifacts complement
	// Context".
	ArtifactKindAgentState = "agent_state"
)

// ActionStatus tracks an action's aggregate lifecycle.
type ActionStatus string

const (
	ActionStatusPending   ActionStatus = "pending"   // claims not all testified
	ActionStatusActive    ActionStatus = "active"    // at least one claim in_progress
	ActionStatusTestified ActionStatus = "testified" // all claims have testaments
	ActionStatusValidated ActionStatus = "validated" // all claims accepted/rejected
	ActionStatusComplete  ActionStatus = "complete"  // terminal success
	ActionStatusFailed    ActionStatus = "failed"    // terminal failure
)

// IsTerminal reports whether this action status is a terminal state.
func (s ActionStatus) IsTerminal() bool {
	return s == ActionStatusComplete || s == ActionStatusFailed
}

// ClaimStatus tracks where a claim is in its lifecycle.
type ClaimStatus string

const (
	ClaimStatusPending    ClaimStatus = "pending"     // issued, subject has not begun
	ClaimStatusInProgress ClaimStatus = "in_progress" // subject actively working
	ClaimStatusTestified  ClaimStatus = "testified"   // subject submitted testament
	ClaimStatusAccepted   ClaimStatus = "accepted"    // all validations passed (terminal)
	ClaimStatusRejected   ClaimStatus = "rejected"    // validation failed (terminal)
	ClaimStatusSuperseded ClaimStatus = "superseded"  // replaced by newer claim (terminal)
)

// IsTerminal reports whether this claim status is a terminal state.
func (s ClaimStatus) IsTerminal() bool {
	return s == ClaimStatusAccepted || s == ClaimStatusRejected || s == ClaimStatusSuperseded
}

// IsActive reports whether this claim status represents active work.
func (s ClaimStatus) IsActive() bool {
	return s == ClaimStatusPending || s == ClaimStatusInProgress
}

// ValidationStatus tracks a validation's evaluation lifecycle.
type ValidationStatus string

const (
	ValidationStatusReady                                 ValidationStatus = "ready"
	ValidationStatusValidating                            ValidationStatus = "validating"
	ValidationStatusValidationFailed                      ValidationStatus = "validation_failed"
	ValidationStatusValidationFailedNotRequired           ValidationStatus = "validation_failed_not_required"
	ValidationStatusErrored                               ValidationStatus = "errored"
	ValidationStatusErroredNotRequired                    ValidationStatus = "errored_not_required"
	ValidationStatusValidatingQualityBar                  ValidationStatus = "validating_quality_bar"
	ValidationStatusQualityBarValidationFailed            ValidationStatus = "quality_bar_validation_failed"
	ValidationStatusQualityBarValidationFailedNotRequired ValidationStatus = "quality_bar_validation_failed_not_required"
	ValidationStatusValidated                             ValidationStatus = "validated"

	// Legacy compatibility statuses. These continue to load and
	// project through existing board paths while typed validation
	// orchestration migrates to the lifecycle statuses above.
	ValidationStatusPending    ValidationStatus = "pending"
	ValidationStatusInProgress ValidationStatus = "in_progress"
	ValidationStatusPassed     ValidationStatus = "passed"
	ValidationStatusIncomplete ValidationStatus = "incomplete"
	ValidationStatusFailed     ValidationStatus = "failed"
	ValidationStatusSkipped    ValidationStatus = "skipped"
)

// IsTerminal reports whether this validation status is a terminal state.
func (s ValidationStatus) IsTerminal() bool {
	return validationTerminalStatuses[s]
}

func (s ValidationStatus) IsNegativeTerminal() bool {
	return validationNegativeTerminalStatuses[s]
}

// ValidationType classifies what kind of check a validation performs.
type ValidationType string

const (
	ValidationTypeTest        ValidationType = "test"        // automated test must pass
	ValidationTypeInspection  ValidationType = "inspection"  // code quality / logic review
	ValidationTypeIntegration ValidationType = "integration" // cross-boundary correctness
	ValidationTypeContract    ValidationType = "contract"    // API/interface adherence
	ValidationTypeDesign      ValidationType = "design"      // UX/design quality
	ValidationTypeRegression  ValidationType = "regression"  // no existing behavior broken
	ValidationTypeReceipt     ValidationType = "receipt"     // proof of delivery/ingestion
)

// ValidationEvaluationMode classifies whether a validation is
// evaluated mechanically by the board or agentically by an evaluator.
type ValidationEvaluationMode string

const (
	ValidationEvaluationMechanical ValidationEvaluationMode = "mechanical"
	ValidationEvaluationAgentic    ValidationEvaluationMode = "agentic"
)

// ValidationTypeSemantics documents the evaluation contract for one
// ValidationType. Tests assert that every known validation type has an
// entry here so new validation types cannot ship without specifying
// who evaluates them and how.
type ValidationTypeSemantics struct {
	Mode        ValidationEvaluationMode
	Description string
}

func KnownValidationTypes() []ValidationType {
	return []ValidationType{
		ValidationTypeTest,
		ValidationTypeInspection,
		ValidationTypeIntegration,
		ValidationTypeContract,
		ValidationTypeDesign,
		ValidationTypeRegression,
		ValidationTypeReceipt,
	}
}

func ValidationTypeSemanticsFor(t ValidationType) (ValidationTypeSemantics, bool) {
	switch t {
	case ValidationTypeReceipt:
		return ValidationTypeSemantics{
			Mode:        ValidationEvaluationMechanical,
			Description: "Board passes receipt when a non-error testament is linked to the claim; error testaments fail receipt.",
		}, true
	case ValidationTypeTest:
		return ValidationTypeSemantics{Mode: ValidationEvaluationAgentic, Description: "Evaluator inspects test artifacts or executes expected tools against the claim quality bar."}, true
	case ValidationTypeInspection:
		return ValidationTypeSemantics{Mode: ValidationEvaluationAgentic, Description: "Evaluator reviews the submitted artifacts for correctness, completeness, and stated quality bar."}, true
	case ValidationTypeIntegration:
		return ValidationTypeSemantics{Mode: ValidationEvaluationAgentic, Description: "Evaluator verifies cross-boundary behavior and integration evidence."}, true
	case ValidationTypeContract:
		return ValidationTypeSemantics{Mode: ValidationEvaluationAgentic, Description: "Evaluator verifies API, interface, schema, or protocol contract adherence."}, true
	case ValidationTypeDesign:
		return ValidationTypeSemantics{Mode: ValidationEvaluationAgentic, Description: "Evaluator reviews user-facing or architectural design quality against the claim's quality bar."}, true
	case ValidationTypeRegression:
		return ValidationTypeSemantics{Mode: ValidationEvaluationAgentic, Description: "Evaluator verifies the work did not break existing behavior."}, true
	default:
		return ValidationTypeSemantics{}, false
	}
}

// BoardPhase tracks the overall claims board lifecycle.
type BoardPhase string

const (
	BoardPhaseImplementation BoardPhase = "implementation" // subjects working claims
	BoardPhaseValidation     BoardPhase = "validation"     // issuers validating testaments
	BoardPhaseComplete       BoardPhase = "complete"       // all claims accepted (terminal)
)

// IsTerminal reports whether this board phase is terminal.
func (p BoardPhase) IsTerminal() bool { return p == BoardPhaseComplete }

// ArtifactStatus tracks the artifact lifecycle defined in
// docs/ARTIFACTS_AND_VALIDATIONS.md §5.
type ArtifactStatus string

const (
	ArtifactStatusGenerated        ArtifactStatus = "generated"
	ArtifactStatusGenerationFailed ArtifactStatus = "generation_failed"
	ArtifactStatusReceived         ArtifactStatus = "received"
	ArtifactStatusReceiptFailed    ArtifactStatus = "receipt_failed"
	ArtifactStatusAttached         ArtifactStatus = "attached"
	ArtifactStatusValidating       ArtifactStatus = "validating"
	ArtifactStatusValidationFailed ArtifactStatus = "validation_failed"
	ArtifactStatusValidated        ArtifactStatus = "validated"
)

// ArtifactErrorCategory classifies durable artifact lifecycle failures.
type ArtifactErrorCategory string

const (
	ArtifactErrorCategoryGeneration        ArtifactErrorCategory = "generation"
	ArtifactErrorCategoryReceiptMetadata   ArtifactErrorCategory = "receipt_metadata"
	ArtifactErrorCategoryReceiptStructural ArtifactErrorCategory = "receipt_structural"
	ArtifactErrorCategoryValidation        ArtifactErrorCategory = "validation"
	ArtifactErrorCategoryTimeout           ArtifactErrorCategory = "timeout"
	ArtifactErrorCategoryPanic             ArtifactErrorCategory = "panic"
	ArtifactErrorCategoryInterruption      ArtifactErrorCategory = "interruption"
	ArtifactErrorCategoryInternal          ArtifactErrorCategory = "internal"
)

// ArtifactError is durable evidence attached to a failed artifact state.
type ArtifactError struct {
	Category    ArtifactErrorCategory `json:"category"`
	Description string                `json:"description"`
	Payload     map[string]any        `json:"payload,omitempty"`
	Source      ParticipantRef        `json:"source"`
	OccurredAt  time.Time             `json:"occurred_at"`
}

// ValidationErrorCategory classifies typed validation lifecycle failures.
type ValidationErrorCategory string

const (
	ValidationErrorCategoryHandler      ValidationErrorCategory = "handler"
	ValidationErrorCategoryQualityBar   ValidationErrorCategory = "quality_bar"
	ValidationErrorCategoryTimeout      ValidationErrorCategory = "timeout"
	ValidationErrorCategoryArtifactType ValidationErrorCategory = "artifact_type_mismatch"
	ValidationErrorCategoryDispatcher   ValidationErrorCategory = "dispatcher"
	ValidationErrorCategoryDependency   ValidationErrorCategory = "dependency_unavailable"
	ValidationErrorCategoryPanic        ValidationErrorCategory = "handler_panic"
	ValidationErrorCategoryInterruption ValidationErrorCategory = "interruption"
	ValidationErrorCategoryInternal     ValidationErrorCategory = "internal"
)

// ValidationError is durable evidence attached to a failed validation.
type ValidationError struct {
	Category    ValidationErrorCategory `json:"category"`
	Description string                  `json:"description"`
	Payload     map[string]any          `json:"payload,omitempty"`
	Source      ParticipantRef          `json:"source"`
	OccurredAt  time.Time               `json:"occurred_at"`
}

// PresentationAudience identifies who a presentation is intended for.
// It is rendering intent, not access control; agents continue to query
// the board for all evidence regardless of audience.
type PresentationAudience string

const (
	PresentationAudienceUser      PresentationAudience = "user"
	PresentationAudienceOperator  PresentationAudience = "operator"
	PresentationAudienceDeveloper PresentationAudience = "developer"
)

// PresentationSurface identifies the UI surface that may render a
// presentable testament or artifact.
type PresentationSurface string

const (
	PresentationSurfaceChat        PresentationSurface = "chat"
	PresentationSurfaceApproval    PresentationSurface = "approval"
	PresentationSurfaceSidePanel   PresentationSurface = "side_panel"
	PresentationSurfaceDiagnostics PresentationSurface = "diagnostics"
)

// PresentationFormat identifies how a renderer should interpret the
// presentable content.
type PresentationFormat string

const (
	PresentationFormatMarkdown PresentationFormat = "markdown"
	PresentationFormatText     PresentationFormat = "text"
	PresentationFormatJSON     PresentationFormat = "json"
	PresentationFormatDiff     PresentationFormat = "diff"
	PresentationFormatTable    PresentationFormat = "table"
)

// PresentationPlacement controls how a rendered entity attaches to a
// chat or panel lifecycle.
type PresentationPlacement string

const (
	PresentationPlacementBeforeResponse PresentationPlacement = "before_response"
	PresentationPlacementAfterResponse  PresentationPlacement = "after_response"
	PresentationPlacementInline         PresentationPlacement = "inline"
	PresentationPlacementReplace        PresentationPlacement = "replace"
	PresentationPlacementPanelOnly      PresentationPlacement = "panel_only"
)

// Presentation is optional rendering metadata for testaments and
// artifacts. It never changes evidence accessibility or validation
// semantics; omitted presentation means no automatic UI rendering.
type Presentation struct {
	Audiences  []PresentationAudience `json:"audiences,omitempty"`
	Surfaces   []PresentationSurface  `json:"surfaces,omitempty"`
	Format     PresentationFormat     `json:"format,omitempty"`
	Title      string                 `json:"title,omitempty"`
	Placement  PresentationPlacement  `json:"placement,omitempty"`
	ReplaceKey string                 `json:"replace_key,omitempty"`
	Priority   int                    `json:"priority,omitempty"`
}

// ────────────────────────────────────────────────────────────────────
// Entity types
// ────────────────────────────────────────────────────────────────────

// Action is a set of claims or testaments issued together. Actions are
// the top-level unit agents process. A claim action groups claims; a
// testament action groups testaments responding to a claim action.
type Action struct {
	// ── Universal base (9 fields) ──
	ID         string     `json:"id"`
	AgentID    string     `json:"agent_id"`
	SessionID  string     `json:"session_id"`
	PipelineID string     `json:"pipeline_id"`
	TaskID     string     `json:"task_id"`
	Sequence   uint64     `json:"sequence"`
	Relations  []Relation `json:"relations"`
	Created    time.Time  `json:"created"`
	Accessed   time.Time  `json:"accessed"`

	// ── Action-specific ──
	Type           ActionType     `json:"type"`
	Status         ActionStatus   `json:"status"`
	StatusHistory  []StatusChange `json:"status_history"`
	Priority       int            `json:"priority,omitempty"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
}

// Claim is a precise, atomic, specific assertion issued by one agent
// against another. The issuer makes the claim; the subject must satisfy
// it by submitting a Testament with Artifacts. The issuer then validates
// the artifacts against the claim's Validations.
type Claim struct {
	// ── Universal base (9 fields) ──
	ID         string     `json:"id"`
	AgentID    string     `json:"agent_id"`
	SessionID  string     `json:"session_id"`
	PipelineID string     `json:"pipeline_id"`
	TaskID     string     `json:"task_id"`
	Sequence   uint64     `json:"sequence"`
	Relations  []Relation `json:"relations"`
	Created    time.Time  `json:"created"`
	Accessed   time.Time  `json:"accessed"`

	// ── Claim-specific ──
	Title         string            `json:"title"`
	Description   string            `json:"description"`
	Scope         []ClaimScopeEntry `json:"scope"`
	ActionType    ActionType        `json:"action_type"`
	Status        ClaimStatus       `json:"status"`
	StatusHistory []StatusChange    `json:"status_history"`
	// LifecycleStatus is the fine-grained claims/testaments lifecycle
	// state. Status remains the coarse compatibility projection used
	// by existing board queues and summaries.
	LifecycleStatus  ClaimLifecycleStatus `json:"lifecycle_status,omitempty"`
	LifecycleHistory []StatusChange       `json:"lifecycle_history,omitempty"`
	IdempotencyKey   string               `json:"idempotency_key,omitempty"`
	Priority         int                  `json:"priority,omitempty"`
	Deadline         time.Time            `json:"deadline,omitempty"`
	Tags             []string             `json:"tags,omitempty"`
	Iteration        int                  `json:"iteration"`

	// Context is the mutable narrative status describing what this
	// claim's owner is currently doing. Distinct from Description
	// (durable intent, set once at post time) and from Validations
	// (quality gates). Updates throughout the claim's lifecycle as
	// the work progresses — e.g., architect's planning claim Context
	// goes "Mapping out dependencies" → "Awaiting librarian response"
	// → "Generating tasks" → "Plan ready for review". Each mutation
	// emits a ClaimContextDelta on the bus; the UI surfaces the
	// update against the row representing this claim, replacing the
	// row's prior status text in place rather than creating a new
	// row. See docs/CLAIMS_UI.md.
	//
	// Sealed only on terminal status transition.
	Context string `json:"context,omitempty"`

	// ContextTransition is the monotonic counter for Context
	// mutations on this claim. The amplifier increments it on every
	// SetClaimContext call so the UI can order Context deltas
	// deterministically under concurrent delivery.
	ContextTransition int64 `json:"context_transition,omitempty"`

	// Validations are the quality gates for this claim. Structural
	// ownership — each Validation belongs to exactly one Claim.
	Validations []*Validation `json:"validations"`

	// ExpectedToolCalls describes concrete tool work the subject should
	// perform or explicitly account for while satisfying this claim.
	ExpectedToolCalls []ExpectedToolCall `json:"expected_tool_calls,omitempty"`
}

// AllValidationsPassed reports whether every validation on this claim
// has passed.
func (c *Claim) AllValidationsPassed() bool {
	if len(c.Validations) == 0 {
		return false // no validations = not validated
	}
	for _, v := range c.Validations {
		if v.Required && !ValidationStatusPassesRequired(v.Status) {
			return false
		}
	}
	return true
}

// PendingValidationCount returns how many required validations are
// still pending.
func (c *Claim) PendingValidationCount() int {
	count := 0
	for _, v := range c.Validations {
		if v.Required && ValidationStatusPendingRequired(v.Status) {
			count++
		}
	}
	return count
}

// FindRelation returns the first Relation matching the given relationship
// type, or nil if none exists.
func (c *Claim) FindRelation(relationship string) *Relation {
	for i := range c.Relations {
		if c.Relations[i].Relationship == relationship {
			return &c.Relations[i]
		}
	}
	return nil
}

// FindRelationsByType returns all Relations matching the given relationship.
func (c *Claim) FindRelationsByType(relationship string) []Relation {
	var out []Relation
	for _, r := range c.Relations {
		if r.Relationship == relationship {
			out = append(out, r)
		}
	}
	return out
}

// Testament is the uniform response to a claim. Immutable — once
// created, never modified. Corrections produce a new testament with a
// supersedes or amends Relation to the prior one.
type Testament struct {
	// ── Universal base (9 fields) ──
	ID         string     `json:"id"`
	AgentID    string     `json:"agent_id"`
	SessionID  string     `json:"session_id"`
	PipelineID string     `json:"pipeline_id"`
	TaskID     string     `json:"task_id"`
	Sequence   uint64     `json:"sequence"`
	Relations  []Relation `json:"relations"`
	Created    time.Time  `json:"created"`
	Accessed   time.Time  `json:"accessed"`

	// ── Testament-specific ──
	Summary          string                   `json:"summary"`
	Confidence       string                   `json:"confidence,omitempty"` // hint, tentative, committed, consensus
	Duration         time.Duration            `json:"duration,omitempty"`
	LifecycleStatus  TestamentLifecycleStatus `json:"lifecycle_status,omitempty"`
	LifecycleHistory []StatusChange           `json:"lifecycle_history,omitempty"`
	IdempotencyKey   string                   `json:"idempotency_key,omitempty"`

	// Context is the mutable narrative of the testament's developing
	// synthesis. Distinct from Summary (durable conclusion text set on
	// flush) and from Artifacts (immutable evidence). Updates while
	// the testament is being built via the accumulator's SetContext;
	// sealed onto Testament.Context on Flush. UI consumes
	// TestamentContextDelta to render the developing-conclusion view
	// of the in-flight testament row. See docs/CLAIMS_UI.md.
	Context string `json:"context,omitempty"`

	// ContextTransition is the monotonic counter for Context
	// mutations. Mirrors Claim.ContextTransition for the same
	// deterministic-ordering reason.
	ContextTransition int64 `json:"context_transition,omitempty"`

	// Presentation carries optional UI rendering intent for the
	// testament Summary. It is presentation metadata only; the
	// testament remains ordinary board evidence for all agents.
	Presentation *Presentation `json:"presentation,omitempty"`

	// Artifacts are the proof attached to this testament. Structural
	// ownership — each Artifact belongs to exactly one Testament.
	Artifacts []*Artifact `json:"artifacts"`
}

// Artifact is evidence attached to a testament. Immutable — once
// created, never modified. "Updating" evidence means the subject submits
// a new testament with new artifacts carrying a supersedes Relation.
type Artifact struct {
	// ── Universal base (9 fields) ──
	ID string `json:"id"`

	// TestamentID is the structural parent — the testament this
	// artifact belongs to. Every Artifact has exactly one parent
	// Testament.
	TestamentID string `json:"testament_id"`

	ClaimID       string     `json:"claim_id,omitempty"`
	AgentID       string     `json:"agent_id"`
	ParticipantID string     `json:"participant_id,omitempty"`
	SessionID     string     `json:"session_id"`
	PipelineID    string     `json:"pipeline_id"`
	TaskID        string     `json:"task_id"`
	Sequence      uint64     `json:"sequence"`
	Relations     []Relation `json:"relations"`
	Created       time.Time  `json:"created"`
	Accessed      time.Time  `json:"accessed"`

	// ── Artifact-specific ──
	ArtifactName  string           `json:"artifact_name,omitempty"`
	Kind          string           `json:"kind"`                   // free-form: "code_reference", "test_output", etc.
	DataType      string           `json:"data_type,omitempty"`    // registered typed payload discriminator
	Data          []byte           `json:"data,omitempty"`         // canonical typed payload bytes
	Reference     string           `json:"reference"`              // content or pointer
	Metadata      map[string]any   `json:"metadata,omitempty"`     // kind-specific structured data
	ContentHash   string           `json:"content_hash,omitempty"` // SHA-256 for integrity/dedup
	Size          int64            `json:"size,omitempty"`         // byte size
	Ephemeral     bool             `json:"ephemeral,omitempty"`    // transient vs durable
	Status        ArtifactStatus   `json:"status,omitempty"`
	StatusHistory []StatusChange   `json:"status_history,omitempty"`
	Errors        []*ArtifactError `json:"errors,omitempty"`
	// Presentation carries optional UI rendering intent for the
	// artifact Reference or dereferenced content. It never removes the
	// artifact from normal board evidence queries.
	Presentation *Presentation `json:"presentation,omitempty"`
}

// Validation is a single precise, atomic means of verifying a claim.
// Stateful — has a lifecycle tracked via Status and StatusHistory.
type Validation struct {
	// ── Universal base (9 fields) ──
	ID string `json:"id"`

	// ClaimID is the structural parent — the claim this validation
	// belongs to. Every Validation has exactly one parent Claim.
	ClaimID string `json:"claim_id"`

	AgentID       string     `json:"agent_id"`
	ParticipantID string     `json:"participant_id,omitempty"`
	SessionID     string     `json:"session_id"`
	PipelineID    string     `json:"pipeline_id"`
	TaskID        string     `json:"task_id"`
	Sequence      uint64     `json:"sequence"`
	Relations     []Relation `json:"relations"`
	Created       time.Time  `json:"created"`
	Accessed      time.Time  `json:"accessed"`

	// ── Validation-specific ──
	TargetArtifactName string           `json:"target_artifact_name,omitempty"`
	ValidatorID        string           `json:"validator_id,omitempty"`
	ArtifactDataType   string           `json:"artifact_data_type,omitempty"`
	ResultDataType     string           `json:"result_data_type,omitempty"`
	Description        string           `json:"description"`
	QualityBar         string           `json:"quality_bar"`
	Type               ValidationType   `json:"type"`
	Status             ValidationStatus `json:"status"`
	StatusHistory      []StatusChange   `json:"status_history"`
	Required           bool             `json:"required"`
	Weight             int              `json:"weight,omitempty"`
	Timeout            time.Duration    `json:"timeout,omitempty"`
	Deadline           time.Time        `json:"deadline,omitempty"`
	ResultArtifactID   string           `json:"result_artifact_id,omitempty"`
	Error              *ValidationError `json:"error,omitempty"`
	EvaluatedAt        time.Time        `json:"evaluated_at,omitempty"`
	EvaluatorRef       *ParticipantRef  `json:"evaluator_ref,omitempty"`

	// ExpectedToolCalls describes concrete tool work the evaluator
	// should perform or explicitly account for while verifying this
	// validation.
	ExpectedToolCalls []ExpectedToolCall `json:"expected_tool_calls,omitempty"`
}

// Passed reports whether this validation has a terminal passed status.
func (v *Validation) Passed() bool {
	return v != nil && (v.Status == ValidationStatusPassed || v.Status == ValidationStatusValidated)
}

// ────────────────────────────────────────────────────────────────────
// Projection and helpers
// ────────────────────────────────────────────────────────────────────

// ClaimsBoardProjection is the immutable snapshot agents and the TUI
// consume. Generated on every board mutation.
type ClaimsBoardProjection struct {
	BoardID   string     `json:"board_id"`
	TaskID    string     `json:"task_id"`
	Phase     BoardPhase `json:"phase"`
	Iteration int        `json:"iteration"`

	Actions    []Action    `json:"actions"`
	Claims     []Claim     `json:"claims"`
	Testaments []Testament `json:"testaments"`
	// Validations live on Claims. Artifacts live on Testaments.
	// Access via claim.Validations and testament.Artifacts.

	// Claim status counts.
	TotalClaims     int `json:"total_claims"`
	PendingCount    int `json:"pending_count"`
	InProgressCount int `json:"in_progress_count"`
	TestifiedCount  int `json:"testified_count"`
	AcceptedCount   int `json:"accepted_count"`
	RejectedCount   int `json:"rejected_count"`

	// Validation status counts.
	TotalValidations      int `json:"total_validations"`
	PassedValidations     int `json:"passed_validations"`
	IncompleteValidations int `json:"incomplete_validations"`
	FailedValidations     int `json:"failed_validations"`
	ErroredValidations    int `json:"errored_validations"`
	SkippedValidations    int `json:"skipped_validations"`

	// Action counts.
	TotalClaimActions     int `json:"total_claim_actions"`
	TotalTestamentActions int `json:"total_testament_actions"`

	// Testament/artifact counts.
	TotalTestaments int        `json:"total_testaments"`
	TotalArtifacts  int        `json:"total_artifacts"`
	Artifacts       []Artifact `json:"artifacts,omitempty"`

	// NotificationErrors contains subscriber notification failures
	// accumulated since the last projection read. Non-empty signals
	// that board state changes were not fully propagated — agents
	// should record these as testament error artifacts.
	NotificationErrors []string `json:"notification_errors,omitempty"`

	Updated time.Time `json:"updated"`
}

// CloneClaimsBoardProjection returns a defensive copy of a board
// projection. Projection callers are allowed to keep or transform the
// returned value without mutating board state or the projection cache.
func CloneClaimsBoardProjection(p *ClaimsBoardProjection) *ClaimsBoardProjection {
	if p == nil {
		return nil
	}
	cp := *p
	if len(p.Actions) > 0 {
		cp.Actions = make([]Action, len(p.Actions))
		for i := range p.Actions {
			cp.Actions[i] = p.Actions[i]
			if len(p.Actions[i].Relations) > 0 {
				cp.Actions[i].Relations = append([]Relation(nil), p.Actions[i].Relations...)
			}
			if len(p.Actions[i].StatusHistory) > 0 {
				cp.Actions[i].StatusHistory = append([]StatusChange(nil), p.Actions[i].StatusHistory...)
			}
		}
	}
	if len(p.Claims) > 0 {
		cp.Claims = make([]Claim, len(p.Claims))
		for i := range p.Claims {
			if clone := CloneClaimEntity(&p.Claims[i]); clone != nil {
				cp.Claims[i] = *clone
			}
		}
	}
	if len(p.Testaments) > 0 {
		cp.Testaments = make([]Testament, len(p.Testaments))
		for i := range p.Testaments {
			if clone := CloneTestamentEntity(&p.Testaments[i]); clone != nil {
				cp.Testaments[i] = *clone
			}
		}
	}
	if len(p.Artifacts) > 0 {
		cp.Artifacts = make([]Artifact, len(p.Artifacts))
		for i := range p.Artifacts {
			if clone := CloneArtifact(&p.Artifacts[i]); clone != nil {
				cp.Artifacts[i] = *clone
			}
		}
	}
	if len(p.NotificationErrors) > 0 {
		cp.NotificationErrors = append([]string(nil), p.NotificationErrors...)
	}
	return &cp
}

// ClaimProgressUpdate is the payload for UpdateClaimProgress — how
// subjects report incremental work before submitting a testament.
type ClaimProgressUpdate struct {
	WorkSummary  string   `json:"work_summary,omitempty"`  // appends to existing
	Evidence     []string `json:"evidence,omitempty"`      // appends evidence refs
	FilesChanged []string `json:"files_changed,omitempty"` // scope tracking
}

// ClaimsBoardConfig configures a new ClaimsBoard.
type ClaimsBoardConfig struct {
	BoardID       string
	PipelineID    string
	TaskID        string
	SessionID     string
	SessionDir    string
	MaxIterations int

	// Scope tracks all goroutines spawned by the board (subscriber
	// notifications, amplifier emissions). Required in production.
	// When nil, notifications and emissions run synchronously (tests).
	Scope ScopeProvider

	// DeltaBus publishes every board mutation as a structured Delta
	// (see deltas.go). Nil-safe — when unset, the amplifier uses a
	// NoopDeltaBus and bus publication is a silent no-op. Inboxes
	// that want to subscribe need a real DeltaBus wired here.
	DeltaBus DeltaBus

	// AgentRefResolver resolves legacy agent relation strings into
	// canonical identity refs for canonical deltas. When nil, deltas
	// carry explicit degraded refs and route through agent_type topics.
	AgentRefResolver AgentRefResolver

	// ClaimPostPolicy is an optional policy gate for activating generated
	// claims. It runs after lifecycle structure and identity checks, before
	// claim.posted is committed.
	ClaimPostPolicy ClaimPostPolicy

	// Projectors are additional deterministic outbox projectors. The
	// durable board always installs the Fabric projector; callers may
	// add package-specific projectors such as a knowledge mirror without
	// making core/claims import those packages.
	Projectors []ClaimsProjector

	// DisableOutbox disables deterministic derived projections while
	// leaving canonical board mutation/WAL behavior intact. This is a
	// rollout/rollback gate; it must never delete existing WAL or
	// projection files.
	DisableOutbox bool

	// LegacySessionNoWAL marks a restored pre-durability session. The
	// board remains usable for new work, but recall reports that older
	// continuity must come from Archivalist/knowledge fallback rather
	// than an exact board spine.
	LegacySessionNoWAL bool

	// Rollout carries the feature-gate snapshot used by this board.
	Rollout RolloutConfig

	// ParentBoardID, when non-empty, establishes this board as a
	// scoped child of the parent (e.g., pipeline board as child of
	// session board). Stored as metadata on the board.
	ParentBoardID string

	// ValidationRuntime optionally wires generated/posted testaments
	// into the artifact and claim validation orchestrators. Nil keeps
	// the board storage-only and preserves legacy explicit validation.
	ValidationRuntime *ClaimsValidationRuntimeConfig

	// RemediationPolicy optionally enables bounded corrective claims
	// generated from terminal testament validation outcomes.
	RemediationPolicy *RemediationPolicy
}

// ScopeProvider launches tracked goroutines. Matches the signature of
// concurrency.GoroutineScope.Go without importing the concurrency
// package directly.
type ScopeProvider interface {
	Go(description string, timeout time.Duration, fn func(ctx context.Context) error) error
}

// ClaimsBoardSubscriber is a callback invoked on every board mutation
// with the updated projection. Returns an error if the subscriber
// encounters a problem — the error is logged but does not block the
// mutation (which already completed). Never panic in a subscriber.
// DEPRECATED: use BoardDeltaSubscriber for lightweight notifications.
type ClaimsBoardSubscriber func(*ClaimsBoardProjection) error

// BoardMutationDelta is the lightweight notification emitted on every
// board mutation. Carries ONLY what changed — no full projection copy.
// Subscribers use this to update counters, emit TUI events, or trigger
// downstream processing without forcing a full board read.
type BoardMutationDelta struct {
	Kind        string      `json:"kind"` // "claim_created", "claim_status_changed", "testament_submitted", "validation_evaluated", "claim_rejected", "phase_changed", "claim_context_changed", "testament_context_changed"
	ClaimID     string      `json:"claim_id,omitempty"`
	TestamentID string      `json:"testament_id,omitempty"`
	FromStatus  ClaimStatus `json:"from_status,omitempty"`
	ToStatus    ClaimStatus `json:"to_status,omitempty"`
	AgentID     string      `json:"agent_id,omitempty"`
	// Context carries the new narrative value for claim_context_changed
	// and testament_context_changed deltas. Empty for other Kinds.
	Context           string `json:"context,omitempty"`
	ContextTransition int64  `json:"context_transition,omitempty"`
	// AccumulatorID is set on testament_context_changed deltas — it
	// identifies the in-flight accumulator that owns the context
	// narrative. Populated for both pre-flush deltas (TestamentID
	// empty) and post-flush deltas (TestamentID set). UI uses
	// AccumulatorID as the in-flight row anchor and TestamentID as
	// the durable rebind anchor.
	AccumulatorID string       `json:"accumulator_id,omitempty"`
	Summary       BoardSummary `json:"summary"` // always populated — current counts
}

// BoardDeltaSubscriber receives lightweight mutation deltas instead of
// full projection copies. The delta tells the subscriber what changed;
// the summary provides current counts.
type BoardDeltaSubscriber func(BoardMutationDelta) error

// ────────────────────────────────────────────────────────────────────
// Presentation helpers
// ────────────────────────────────────────────────────────────────────

// ClonePresentation returns a defensive copy of presentation metadata.
func ClonePresentation(p *Presentation) *Presentation {
	if p == nil {
		return nil
	}
	cp := *p
	if len(p.Audiences) > 0 {
		cp.Audiences = append([]PresentationAudience(nil), p.Audiences...)
	}
	if len(p.Surfaces) > 0 {
		cp.Surfaces = append([]PresentationSurface(nil), p.Surfaces...)
	}
	return &cp
}

// NormalizePresentation trims strings and removes duplicate audiences
// and surfaces. A nil or empty presentation normalizes to nil, matching
// the default "not automatically rendered" contract.
func NormalizePresentation(p *Presentation) *Presentation {
	cp := ClonePresentation(p)
	if cp == nil {
		return nil
	}
	cp.Audiences = normalizePresentationAudiences(cp.Audiences)
	cp.Surfaces = normalizePresentationSurfaces(cp.Surfaces)
	cp.Format = PresentationFormat(strings.TrimSpace(string(cp.Format)))
	cp.Title = strings.TrimSpace(cp.Title)
	cp.Placement = PresentationPlacement(strings.TrimSpace(string(cp.Placement)))
	cp.ReplaceKey = strings.TrimSpace(cp.ReplaceKey)
	if len(cp.Audiences) == 0 &&
		len(cp.Surfaces) == 0 &&
		cp.Format == "" &&
		cp.Title == "" &&
		cp.Placement == "" &&
		cp.ReplaceKey == "" &&
		cp.Priority == 0 {
		return nil
	}
	if cp.Format == "" {
		cp.Format = PresentationFormatText
	}
	if cp.Placement == "" {
		cp.Placement = PresentationPlacementInline
	}
	return cp
}

func normalizePresentationAudiences(in []PresentationAudience) []PresentationAudience {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[PresentationAudience]struct{}, len(in))
	out := make([]PresentationAudience, 0, len(in))
	for _, audience := range in {
		audience = PresentationAudience(strings.TrimSpace(string(audience)))
		if audience == "" {
			continue
		}
		if _, ok := seen[audience]; ok {
			continue
		}
		seen[audience] = struct{}{}
		out = append(out, audience)
	}
	return out
}

func normalizePresentationSurfaces(in []PresentationSurface) []PresentationSurface {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[PresentationSurface]struct{}, len(in))
	out := make([]PresentationSurface, 0, len(in))
	for _, surface := range in {
		surface = PresentationSurface(strings.TrimSpace(string(surface)))
		if surface == "" {
			continue
		}
		if _, ok := seen[surface]; ok {
			continue
		}
		seen[surface] = struct{}{}
		out = append(out, surface)
	}
	return out
}

// ValidatePresentation checks structural presentation requirements.
// Unknown vocabulary values are preserved for forward compatibility and
// treated as non-renderable by current surface matchers.
func ValidatePresentation(p *Presentation) error {
	cp := NormalizePresentation(p)
	if cp == nil {
		return nil
	}
	if presentationHasAudience(cp, PresentationAudienceUser) && len(cp.Surfaces) == 0 {
		return fmt.Errorf("presentation audience %q requires at least one surface", PresentationAudienceUser)
	}
	return nil
}

func validPresentationAudience(audience PresentationAudience) bool {
	switch audience {
	case PresentationAudienceUser, PresentationAudienceOperator, PresentationAudienceDeveloper:
		return true
	default:
		return false
	}
}

func validPresentationSurface(surface PresentationSurface) bool {
	switch surface {
	case PresentationSurfaceChat, PresentationSurfaceApproval, PresentationSurfaceSidePanel, PresentationSurfaceDiagnostics:
		return true
	default:
		return false
	}
}

func validPresentationFormat(format PresentationFormat) bool {
	switch format {
	case PresentationFormatMarkdown, PresentationFormatText, PresentationFormatJSON, PresentationFormatDiff, PresentationFormatTable:
		return true
	default:
		return false
	}
}

func validPresentationPlacement(placement PresentationPlacement) bool {
	switch placement {
	case PresentationPlacementBeforeResponse, PresentationPlacementAfterResponse, PresentationPlacementInline, PresentationPlacementReplace, PresentationPlacementPanelOnly:
		return true
	default:
		return false
	}
}

// PresentationMatches reports whether p explicitly targets audience and
// surface. Invalid presentation metadata is treated as non-renderable.
func PresentationMatches(p *Presentation, audience, surface string) bool {
	cp := NormalizePresentation(p)
	if cp == nil || ValidatePresentation(cp) != nil {
		return false
	}
	wantAudience := PresentationAudience(strings.TrimSpace(audience))
	wantSurface := PresentationSurface(strings.TrimSpace(surface))
	if wantAudience == "" || wantSurface == "" ||
		!validPresentationAudience(wantAudience) ||
		!validPresentationSurface(wantSurface) {
		return false
	}
	if !presentationHasAudience(cp, wantAudience) {
		return false
	}
	return presentationHasSurface(cp, wantSurface)
}

func presentationHasAudience(p *Presentation, want PresentationAudience) bool {
	for _, audience := range p.Audiences {
		if audience == want {
			return true
		}
	}
	return false
}

func presentationHasSurface(p *Presentation, want PresentationSurface) bool {
	for _, surface := range p.Surfaces {
		if surface == want {
			return true
		}
	}
	return false
}

// IsUserChatPresentation reports whether p is explicitly renderable to
// the human chat surface.
func IsUserChatPresentation(p *Presentation) bool {
	return PresentationMatches(p, string(PresentationAudienceUser), string(PresentationSurfaceChat))
}

// IsPresentableToUserChat is a compatibility alias with the wording in
// docs/CLAIMS_VISIBILITY.md.
func IsPresentableToUserChat(p *Presentation) bool {
	return IsUserChatPresentation(p)
}

// PresentableArtifacts returns testament artifacts that explicitly
// target audience and surface. It is a convenience filter only; generic
// board queries still return every artifact.
func PresentableArtifacts(t *Testament, audience, surface string) []*Artifact {
	if t == nil {
		return nil
	}
	out := make([]*Artifact, 0, len(t.Artifacts))
	for _, artifact := range t.Artifacts {
		if artifact == nil || !PresentationMatches(artifact.Presentation, audience, surface) {
			continue
		}
		out = append(out, CloneArtifact(artifact))
	}
	return out
}

// HasPresentableArtifact reports whether t contains at least one
// artifact of kind that targets audience and surface.
func HasPresentableArtifact(t *Testament, kind, audience, surface string) bool {
	if t == nil {
		return false
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return false
	}
	for _, artifact := range t.Artifacts {
		if artifact == nil || strings.TrimSpace(artifact.Kind) != kind {
			continue
		}
		if PresentationMatches(artifact.Presentation, audience, surface) {
			return true
		}
	}
	return false
}

// CloneArtifact returns a defensive copy of an artifact, including
// presentation metadata. Metadata values are copied shallowly because
// they are arbitrary JSON-like values owned by artifact-kind protocols.
func CloneArtifact(a *Artifact) *Artifact {
	if a == nil {
		return nil
	}
	cp := *a
	if len(a.Data) > 0 {
		cp.Data = append([]byte(nil), a.Data...)
	}
	if len(a.Relations) > 0 {
		cp.Relations = append([]Relation(nil), a.Relations...)
	}
	if len(a.StatusHistory) > 0 {
		cp.StatusHistory = append([]StatusChange(nil), a.StatusHistory...)
	}
	if len(a.Errors) > 0 {
		cp.Errors = cloneArtifactErrors(a.Errors)
	}
	cp.Metadata = cloneAnyMap(a.Metadata)
	cp.Presentation = ClonePresentation(a.Presentation)
	return &cp
}

// CloneTestamentEntity returns a defensive copy of a testament,
// including presentation metadata and artifact copies.
func CloneTestamentEntity(t *Testament) *Testament {
	if t == nil {
		return nil
	}
	cp := *t
	if len(t.Relations) > 0 {
		cp.Relations = append([]Relation(nil), t.Relations...)
	}
	if len(t.LifecycleHistory) > 0 {
		cp.LifecycleHistory = append([]StatusChange(nil), t.LifecycleHistory...)
	}
	cp.Presentation = ClonePresentation(t.Presentation)
	if len(t.Artifacts) > 0 {
		cp.Artifacts = make([]*Artifact, len(t.Artifacts))
		for i, artifact := range t.Artifacts {
			cp.Artifacts[i] = CloneArtifact(artifact)
		}
	}
	return &cp
}

// CloneClaimEntity returns a defensive copy of a claim, including
// validation copies and expected tool specs.
func CloneClaimEntity(c *Claim) *Claim {
	if c == nil {
		return nil
	}
	cp := *c
	if len(c.Relations) > 0 {
		cp.Relations = append([]Relation(nil), c.Relations...)
	}
	if len(c.Scope) > 0 {
		cp.Scope = append([]ClaimScopeEntry(nil), c.Scope...)
	}
	if len(c.Tags) > 0 {
		cp.Tags = append([]string(nil), c.Tags...)
	}
	if len(c.ExpectedToolCalls) > 0 {
		cp.ExpectedToolCalls = append([]ExpectedToolCall(nil), c.ExpectedToolCalls...)
	}
	if len(c.Validations) > 0 {
		cp.Validations = make([]*Validation, len(c.Validations))
		for i, validation := range c.Validations {
			if validation == nil {
				continue
			}
			v := *validation
			if len(validation.Relations) > 0 {
				v.Relations = append([]Relation(nil), validation.Relations...)
			}
			if len(validation.StatusHistory) > 0 {
				v.StatusHistory = append([]StatusChange(nil), validation.StatusHistory...)
			}
			if validation.Error != nil {
				v.Error = cloneValidationError(validation.Error)
			}
			if validation.EvaluatorRef != nil {
				ref := cloneParticipantRef(*validation.EvaluatorRef)
				v.EvaluatorRef = &ref
			}
			if len(validation.ExpectedToolCalls) > 0 {
				v.ExpectedToolCalls = append([]ExpectedToolCall(nil), validation.ExpectedToolCalls...)
			}
			cp.Validations[i] = &v
		}
	}
	if len(c.StatusHistory) > 0 {
		cp.StatusHistory = append([]StatusChange(nil), c.StatusHistory...)
	}
	if len(c.LifecycleHistory) > 0 {
		cp.LifecycleHistory = append([]StatusChange(nil), c.LifecycleHistory...)
	}
	return &cp
}

func cloneArtifactErrors(in []*ArtifactError) []*ArtifactError {
	out := make([]*ArtifactError, len(in))
	for i, errEntry := range in {
		if errEntry == nil {
			continue
		}
		cp := *errEntry
		cp.Payload = cloneAnyMap(errEntry.Payload)
		cp.Source = cloneParticipantRef(errEntry.Source)
		out[i] = &cp
	}
	return out
}

func cloneValidationError(in *ValidationError) *ValidationError {
	if in == nil {
		return nil
	}
	cp := *in
	cp.Payload = cloneAnyMap(in.Payload)
	cp.Source = cloneParticipantRef(in.Source)
	return &cp
}

func cloneParticipantRef(in ParticipantRef) ParticipantRef {
	out := in
	if in.Task != nil {
		task := *in.Task
		out.Task = &task
	}
	if len(in.Labels) > 0 {
		out.Labels = make(map[string]string, len(in.Labels))
		for k, v := range in.Labels {
			out.Labels[k] = v
		}
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneMetadata(in map[string]any) map[string]any {
	return cloneAnyMap(in)
}

func mergeMetadata(base map[string]any, overlay map[string]any) map[string]any {
	out := cloneMetadata(base)
	if len(overlay) == 0 {
		return out
	}
	if out == nil {
		out = make(map[string]any, len(overlay))
	}
	for key, value := range overlay {
		key = strings.TrimSpace(key)
		if key == "" || value == nil {
			continue
		}
		out[key] = value
	}
	return out
}

// ────────────────────────────────────────────────────────────────────
// Relation helpers
// ────────────────────────────────────────────────────────────────────

// FindRelation returns the first Relation on the slice matching the
// given relationship, or nil.
func FindRelation(relations []Relation, relationship string) *Relation {
	for i := range relations {
		if relations[i].Relationship == relationship {
			return &relations[i]
		}
	}
	return nil
}

// FindRelationsByType returns all Relations matching the given relationship.
func FindRelationsByType(relations []Relation, relationship string) []Relation {
	var out []Relation
	for _, r := range relations {
		if r.Relationship == relationship {
			out = append(out, r)
		}
	}
	return out
}

// HasRelation reports whether any Relation in the slice matches the
// given relationship and related ID.
func HasRelation(relations []Relation, relationship, relatedID string) bool {
	for _, r := range relations {
		if r.Relationship == relationship && r.Related == relatedID {
			return true
		}
	}
	return false
}

// IssuerAgentID returns the Related ID of the first "issuer" Relation,
// or empty string if none.
func IssuerAgentID(relations []Relation) string {
	r := FindRelation(relations, RelationshipIssuer)
	if r == nil {
		return ""
	}
	return r.Related
}

// SubjectAgentID returns the Related ID of the first "subject" Relation,
// or empty string if none.
func SubjectAgentID(relations []Relation) string {
	r := FindRelation(relations, RelationshipSubject)
	if r == nil {
		return ""
	}
	return r.Related
}

// ClaimActionID returns the Related ID of the first "claim_action"
// Relation, or empty string if none.
func ClaimActionID(relations []Relation) string {
	r := FindRelation(relations, RelationshipClaimAction)
	if r == nil {
		return ""
	}
	return r.Related
}

// ClaimIDFromRelations returns the Related ID of the first "claim"
// Relation (RelationshipClaim) — i.e. the ID of the claim a
// testament or artifact is responding to. Empty string when no such
// relation is set.
func ClaimIDFromRelations(relations []Relation) string {
	r := FindRelation(relations, RelationshipClaim)
	if r == nil {
		return ""
	}
	return r.Related
}

// HandoffFromClaimID returns the Related ID of the first "handoff_from"
// Relation, or empty string if none. The bridge's cycle resolver reads
// this to detect handoff edges (UI_DESIGN.md §5.2).
func HandoffFromClaimID(relations []Relation) string {
	r := FindRelation(relations, RelationshipHandoffFrom)
	if r == nil {
		return ""
	}
	return r.Related
}

// CompletesArtifactID returns the Related ID of the first "completes"
// Relation, or empty string if none. Used to pair completion artifacts
// with their started counterparts (UI_DESIGN.md §2.4).
func CompletesArtifactID(relations []Relation) string {
	r := FindRelation(relations, RelationshipCompletes)
	if r == nil {
		return ""
	}
	return r.Related
}

// maxStatusHistoryLen caps StatusHistory on claims and validations.
// Older entries are preserved in the WAL for audit; the in-memory
// board only keeps the most recent transitions.
const maxStatusHistoryLen = 20

// capStatusHistory trims a StatusHistory slice to the most recent
// maxStatusHistoryLen entries.
func capStatusHistory(history []StatusChange) []StatusChange {
	if len(history) <= maxStatusHistoryLen {
		return history
	}
	return history[len(history)-maxStatusHistoryLen:]
}

// maxArtifactReferenceLen caps artifact Reference fields in the
// in-memory board. Full content is in the WAL/persistent store.
const maxArtifactReferenceLen = 500

// TruncateArtifactReference truncates a reference to maxArtifactReferenceLen.
func TruncateArtifactReference(ref string) string {
	if len(ref) <= maxArtifactReferenceLen {
		return ref
	}
	return ref[:maxArtifactReferenceLen-1] + "\u2026"
}
