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
	RelationshipTestamentAction = "testament_action"  // parent testament action
	RelationshipClaim           = "claim"             // the claim this responds to
	RelationshipTestament       = "testament"         // the testament this artifact belongs to

	// Causal and semantic relationships.
	RelationshipSupersedes   = "supersedes"    // replaces the related object
	RelationshipDependsOn    = "depends_on"    // cannot proceed until related is satisfied
	RelationshipCausedBy     = "caused_by"     // created in response to the related object
	RelationshipRefines      = "refines"       // narrows or clarifies the related object
	RelationshipConflictsWith = "conflicts_with" // contradicts the related object
	RelationshipDerivedFrom  = "derived_from"  // content derived from the related object
	RelationshipReviews      = "reviews"       // evaluates the related object
	RelationshipAmends       = "amends"        // modifies but does not replace the related object
	RelationshipDirectAddressed = "direct_addressed" // user directly addressed this agent
)

// RelatedTypeXxx constants for Relation.RelatedType.
const (
	RelatedTypeAction     = "action"
	RelatedTypeClaim      = "claim"
	RelatedTypeTestament  = "testament"
	RelatedTypeValidation = "validation"
	RelatedTypeArtifact   = "artifact"
	RelatedTypeAgent      = "agent"
)

// StatusChange records a single status transition on a stateful object
// (Action, Claim, or Validation). Every transition is recorded — the
// full lifecycle is auditable.
type StatusChange struct {
	From    string    `json:"from"`     // previous status value
	To      string    `json:"to"`       // new status value
	Reason  string    `json:"reason"`   // why the transition happened
	AgentID string    `json:"agent_id"` // which agent instance caused it
	Changed time.Time `json:"changed"`  // UTC timestamp
}

// ClaimScopeEntry identifies one element of a claim's affected scope.
type ClaimScopeEntry struct {
	Kind string `json:"kind"` // "file", "symbol", "api", "test_surface", "component", "ux_surface"
	Key  string `json:"key"`  // path, symbol name, endpoint, surface ID, etc.
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
	ValidationStatusPending    ValidationStatus = "pending"     // not yet evaluated
	ValidationStatusInProgress ValidationStatus = "in_progress" // evaluator working
	ValidationStatusPassed     ValidationStatus = "passed"      // meets quality bar (terminal)
	ValidationStatusFailed     ValidationStatus = "failed"      // does not meet quality bar (terminal)
	ValidationStatusSkipped    ValidationStatus = "skipped"     // explicitly waived (terminal)
)

// IsTerminal reports whether this validation status is a terminal state.
func (s ValidationStatus) IsTerminal() bool {
	return s == ValidationStatusPassed || s == ValidationStatusFailed || s == ValidationStatusSkipped
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

// BoardPhase tracks the overall claims board lifecycle.
type BoardPhase string

const (
	BoardPhaseImplementation BoardPhase = "implementation" // subjects working claims
	BoardPhaseValidation     BoardPhase = "validation"     // issuers validating testaments
	BoardPhaseComplete       BoardPhase = "complete"       // all claims accepted (terminal)
)

// IsTerminal reports whether this board phase is terminal.
func (p BoardPhase) IsTerminal() bool { return p == BoardPhaseComplete }

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
	Type          ActionType     `json:"type"`
	Status        ActionStatus   `json:"status"`
	StatusHistory []StatusChange `json:"status_history"`
	Priority      int            `json:"priority,omitempty"`
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
	Title         string           `json:"title"`
	Description   string           `json:"description"`
	Scope         []ClaimScopeEntry `json:"scope"`
	ActionType    ActionType       `json:"action_type"`
	Status        ClaimStatus      `json:"status"`
	StatusHistory []StatusChange   `json:"status_history"`
	Priority      int              `json:"priority,omitempty"`
	Deadline      time.Time        `json:"deadline,omitempty"`
	Tags          []string         `json:"tags,omitempty"`
	Iteration     int              `json:"iteration"`

	// Validations are the quality gates for this claim. Structural
	// ownership — each Validation belongs to exactly one Claim.
	Validations []*Validation `json:"validations"`
}

// AllValidationsPassed reports whether every validation on this claim
// has passed.
func (c *Claim) AllValidationsPassed() bool {
	if len(c.Validations) == 0 {
		return false // no validations = not validated
	}
	for _, v := range c.Validations {
		if v.Required && v.Status != ValidationStatusPassed {
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
		if v.Required && v.Status == ValidationStatusPending {
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
	Summary    string        `json:"summary"`
	Confidence string        `json:"confidence,omitempty"` // hint, tentative, committed, consensus
	Duration   time.Duration `json:"duration,omitempty"`

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

	AgentID    string     `json:"agent_id"`
	SessionID  string     `json:"session_id"`
	PipelineID string     `json:"pipeline_id"`
	TaskID     string     `json:"task_id"`
	Sequence   uint64     `json:"sequence"`
	Relations  []Relation `json:"relations"`
	Created    time.Time  `json:"created"`
	Accessed   time.Time  `json:"accessed"`

	// ── Artifact-specific ──
	Kind        string         `json:"kind"`                  // free-form: "code_reference", "test_output", etc.
	Reference   string         `json:"reference"`             // content or pointer
	Metadata    map[string]any `json:"metadata,omitempty"`    // kind-specific structured data
	ContentHash string         `json:"content_hash,omitempty"` // SHA-256 for integrity/dedup
	Size        int64          `json:"size,omitempty"`         // byte size
	Ephemeral   bool           `json:"ephemeral,omitempty"`   // transient vs durable
}

// Validation is a single precise, atomic means of verifying a claim.
// Stateful — has a lifecycle tracked via Status and StatusHistory.
type Validation struct {
	// ── Universal base (9 fields) ──
	ID string `json:"id"`

	// ClaimID is the structural parent — the claim this validation
	// belongs to. Every Validation has exactly one parent Claim.
	ClaimID string `json:"claim_id"`

	AgentID    string     `json:"agent_id"`
	SessionID  string     `json:"session_id"`
	PipelineID string     `json:"pipeline_id"`
	TaskID     string     `json:"task_id"`
	Sequence   uint64     `json:"sequence"`
	Relations  []Relation `json:"relations"`
	Created    time.Time  `json:"created"`
	Accessed   time.Time  `json:"accessed"`

	// ── Validation-specific ──
	Description   string           `json:"description"`
	QualityBar    string           `json:"quality_bar"`
	Type          ValidationType   `json:"type"`
	Status        ValidationStatus `json:"status"`
	StatusHistory []StatusChange   `json:"status_history"`
	Required      bool             `json:"required"`
	Weight        int              `json:"weight,omitempty"`
	Deadline      time.Time        `json:"deadline,omitempty"`
}

// Passed reports whether this validation has a terminal passed status.
func (v *Validation) Passed() bool { return v.Status == ValidationStatusPassed }

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
	TotalValidations   int `json:"total_validations"`
	PassedValidations  int `json:"passed_validations"`
	FailedValidations  int `json:"failed_validations"`
	SkippedValidations int `json:"skipped_validations"`

	// Action counts.
	TotalClaimActions     int `json:"total_claim_actions"`
	TotalTestamentActions int `json:"total_testament_actions"`

	// Testament/artifact counts.
	TotalTestaments int `json:"total_testaments"`
	TotalArtifacts  int `json:"total_artifacts"`

	Updated time.Time `json:"updated"`
}

// ClaimProgressUpdate is the payload for UpdateClaimProgress — how
// subjects report incremental work before submitting a testament.
type ClaimProgressUpdate struct {
	WorkSummary  string   `json:"work_summary,omitempty"`  // appends to existing
	Evidence     []string `json:"evidence,omitempty"`       // appends evidence refs
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
type ClaimsBoardSubscriber func(*ClaimsBoardProjection) error

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
