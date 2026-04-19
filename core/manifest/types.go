// Package manifest defines the cross-pipeline Decision Manifest: a typed,
// scoped record of agent decisions (e.g. "use pytest for Python tests in
// services/api/") that lets parallel pipelines see each other's in-flight
// choices before committing incompatible work.
//
// The manifest sits alongside the coordination service but at a different
// layer: coordination tracks POST-decision artifacts, claims, and reviews;
// the manifest tracks PRE-commitment typed decisions. Together they give
// parallel pipelines both "what have peers produced" (coordination) and
// "what are peers about to decide" (manifest) visibility.
//
// Scopes are open-vocabulary typed coordinates, not a fixed hierarchy. A
// Lua game, a Rust monorepo, and a Python microservices stack all use the
// same primitives — they just register different dimensions (filesystem
// path, language, runtime target, workspace member, etc.) per decision
// domain. See docs/DECISION_MANIFEST.md for the broader design rationale.
package manifest

import "time"

// Bus action names for the manifest RPC surface. Dispatched by the
// orchestrator alongside the coordination actions.
const (
	ActionDeclareDecision = "manifest_declare"
	ActionQueryDecisions  = "manifest_query"
)

// Dimension is a typed key in a Scope coordinate. Built-in dimensions are
// declared as constants below; agents may introduce new dimensions via
// declarations (open vocabulary).
type Dimension string

const (
	// DimensionPath is filesystem path. This is the ONLY built-in dimension
	// with prefix-match semantics: a decision scoped to "services/api/"
	// applies to any query whose path starts with that prefix. All other
	// dimensions use exact match.
	DimensionPath Dimension = "path"

	// DimensionLanguage is the source language enum (python, go, rust,
	// lua, cpp, ts, sql, bash, …). Exact match.
	DimensionLanguage Dimension = "language"

	// DimensionSurface is the test surface (unit | integration | e2e |
	// property) for test-framework-style decisions. Exact match. Added
	// as a well-known dimension because it recurs across test-related
	// domains; domains that don't need it simply omit it.
	DimensionSurface Dimension = "surface"
)

// Scope is an unordered set of (dimension, value) pairs that locates a
// decision in the project's coordinate space. An empty Scope means the
// decision is ambient — applies to any query in its domain unless
// superseded by a more specific decision.
type Scope map[Dimension]string

// Confidence ranks how binding a decision is. Tiers escalate in order:
//
//   - Hint: a soft prior surfaced from the Memory Forest or another
//     out-of-session source. Informational — agents may freely override.
//   - Tentative: an agent has declared intent but not yet committed code.
//     Cheap to retract; auto-reaped if the declaring pipeline terminates
//     without promoting it.
//   - Committed: the agent has started work based on this decision.
//     Retractable but with rework cost.
//   - Consensus: peer-ratified, architect-ratified, or survived the
//     negotiation window unchallenged. Binding for downstream agents.
type Confidence string

const (
	ConfidenceHint      Confidence = "hint"
	ConfidenceTentative Confidence = "tentative"
	ConfidenceCommitted Confidence = "committed"
	ConfidenceConsensus Confidence = "consensus"
)

// Compatibility is a domain-specific predicate on two candidate values.
// It lets domains avoid spurious peer challenges for version drift or
// equivalent framings ("pytest" and "pytest-asyncio" are Equivalent;
// "pytest" and "unittest" are Incompatible).
type Compatibility int

const (
	// CompatibilityEquivalent — values are semantically the same; the
	// later declaration is redundant corroboration of the earlier one.
	CompatibilityEquivalent Compatibility = iota
	// CompatibilityCompatible — values can coexist without conflict
	// (e.g. ruff for lint plus black for format).
	CompatibilityCompatible
	// CompatibilityIncompatible — values genuinely disagree; a peer
	// challenge or explicit reconciliation is required.
	CompatibilityIncompatible
)

func (c Compatibility) String() string {
	switch c {
	case CompatibilityEquivalent:
		return "equivalent"
	case CompatibilityCompatible:
		return "compatible"
	case CompatibilityIncompatible:
		return "incompatible"
	}
	return "unknown"
}

// ResolutionPolicy determines how the manifest picks a winner when
// multiple matching decisions apply to a query.
type ResolutionPolicy string

const (
	// ResolvePolicySpecificityFirstMover — the most-specific match wins.
	// Ties broken by declaration time ascending (first-mover). This is
	// the sensible default for most framework / tooling choices.
	ResolvePolicySpecificityFirstMover ResolutionPolicy = "specificity_first_mover"

	// ResolvePolicyAuthorityFirst — architect Charter entries win,
	// then peer-ratified Consensus, then first-mover among peers.
	// Use for decisions where cross-pipeline safety requires the
	// architect to arbitrate (secret storage, security policy).
	ResolvePolicyAuthorityFirst ResolutionPolicy = "authority_first"

	// ResolvePolicyLatestWins — the most recent declaration wins.
	// Use for deliberately mutable decisions (feature flag defaults,
	// per-turn configuration overrides).
	ResolvePolicyLatestWins ResolutionPolicy = "latest_wins"

	// ResolvePolicyExplicitConsensusRequired — matching decisions cannot
	// auto-converge; a peer challenge or architect ratification must
	// resolve them before the winner is defined. Used for high-risk
	// cross-pipeline decisions where silent convergence is unsafe.
	ResolvePolicyExplicitConsensusRequired ResolutionPolicy = "explicit_consensus_required"
)

// AgentRef identifies the agent that authored a decision. Both ID and
// type are kept so audit trails remain meaningful after agent lifecycle
// churn (a pipeline tester may be replaced across tiers; the type stays).
type AgentRef struct {
	AgentID   string `json:"agent_id"`
	AgentType string `json:"agent_type"`
}

// DecisionEvent captures a lifecycle transition on a decision — useful
// for audit trails, TUI visualization, and the inspector's pre-commit
// consistency checks.
type DecisionEvent struct {
	Type      string    `json:"type"`
	AgentID   string    `json:"agent_id,omitempty"`
	AgentType string    `json:"agent_type,omitempty"`
	Note      string    `json:"note,omitempty"`
	At        time.Time `json:"at"`
}

// Standard event types. Kept as string constants so new event kinds
// (e.g. "challenged_by_inspector") can be added without breaking
// existing persisted records.
const (
	DecisionEventDeclared   = "declared"
	DecisionEventAdopted    = "adopted"    // a peer independently declared the same value
	DecisionEventChallenged = "challenged" // a peer issued a challenge_decision
	DecisionEventRatified   = "ratified"   // peer challenge closed with this value winning
	DecisionEventSuperseded = "superseded" // replaced by a later decision
)

// Decision is the core record in the manifest. Persisted per session.
type Decision struct {
	ID         string          `json:"id"`
	SessionID  string          `json:"session_id"`
	Domain     string          `json:"domain"`
	Scope      Scope           `json:"scope"`
	Value      string          `json:"value"`
	Author     AgentRef        `json:"author"`
	DeclaredAt time.Time       `json:"declared_at"`
	Confidence Confidence      `json:"confidence"`
	Evidence   []string        `json:"evidence,omitempty"`
	Supersedes string          `json:"supersedes,omitempty"`
	History    []DecisionEvent `json:"history,omitempty"`
	ExpiresAt  *time.Time      `json:"expires_at,omitempty"`
}

// DeclareDecisionInput is the payload an agent publishes to
// ActionDeclareDecision.
type DeclareDecisionInput struct {
	Domain     string     `json:"domain"`
	Scope      Scope      `json:"scope"`
	Value      string     `json:"value"`
	Evidence   []string   `json:"evidence,omitempty"`
	Confidence Confidence `json:"confidence"`
	Supersedes string     `json:"supersedes,omitempty"`
}

// DeclareDecisionResult carries back the persisted decision plus any
// conflict detected against existing decisions. A non-nil Conflict means
// the agent should inspect and choose: adopt, align at a different scope,
// or issue a peer challenge via the existing challenge_agent primitive.
type DeclareDecisionResult struct {
	Decision *Decision         `json:"decision"`
	Conflict *DecisionConflict `json:"conflict,omitempty"`
}

// ConflictKind classifies overlap semantics for a newly-declared decision
// against an existing decision in a scope-overlapping position.
type ConflictKind string

const (
	// ConflictNone — no overlap detected; the new decision stands alone.
	ConflictNone ConflictKind = ""
	// ConflictEquivalent — the two values are semantically the same per
	// the domain's compatibility predicate. The new declaration serves
	// as corroboration and the existing decision is promoted toward
	// Consensus (Tentative → Committed, Committed → Consensus) on the
	// second independent declaration.
	ConflictEquivalent ConflictKind = "equivalent"
	// ConflictCompatible — distinct values that can coexist. Both
	// decisions stay in the manifest; queries with more specific scopes
	// pick the right one.
	ConflictCompatible ConflictKind = "compatible"
	// ConflictIncompatible — genuine disagreement. The declaring agent
	// should either adopt the existing decision, narrow its own scope to
	// a non-overlapping coordinate, or issue a challenge_decision via
	// challenge_agent.
	ConflictIncompatible ConflictKind = "incompatible"
)

// DecisionConflict describes an overlap detected at declaration time.
type DecisionConflict struct {
	Kind          ConflictKind  `json:"kind"`
	Existing      *Decision     `json:"existing"`
	Compatibility Compatibility `json:"compatibility"`
	Message       string        `json:"message"`
}

// QueryDecisionsInput is the payload an agent publishes to
// ActionQueryDecisions.
type QueryDecisionsInput struct {
	Domain string `json:"domain"`
	Scope  Scope  `json:"scope"`
}

// QueryDecisionsResult carries the resolved winner (if any) plus all
// matching decisions for transparency. Matches are ordered from most
// specific to least specific, with resolution-policy tiebreaking applied.
//
// Hints is reserved for future Memory Forest priors; in phase 1 it's
// always empty.
type QueryDecisionsResult struct {
	Winner  *Decision   `json:"winner,omitempty"`
	Matches []*Decision `json:"matches,omitempty"`
	Hints   []*Decision `json:"hints,omitempty"`
}
