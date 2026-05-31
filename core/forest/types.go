// Package forest provides the Memory Forest runtime for intent-first,
// multi-tree memory projection and retrieval.
package forest

import (
	"math"
	"time"
)

// TreeFamily identifies the specialized tree a branch belongs to.
type TreeFamily string

const (
	// TreeFamilyIntent — a goal or sub-goal an agent is actively
	// pursuing. Future-directed; carries no Status. Distinct from
	// Decision (a past choice toward this Intent) and Outcome
	// (a past result of action toward this Intent).
	TreeFamilyIntent TreeFamily = "intent"

	// TreeFamilyConstraint — a rule that bounds the action space.
	// Branch.ConstraintSeverity discriminates "hard" (violation is
	// an error, contributes to ScopeRisk) from "soft" (violation is
	// suboptimal, contributes a bounded score penalty). Subsumes
	// the deprecated TreeFamilyPreference via severity="soft".
	TreeFamilyConstraint TreeFamily = "constraint"

	// TreeFamilyEvidence — an observation of fact about the world or
	// work. Past-directed; carries no agent attribution. Supports
	// decisions but doesn't itself record any agent's action.
	// Distinct from Outcome (which DOES record the consequence of
	// an agent's action).
	TreeFamilyEvidence TreeFamily = "evidence"

	// TreeFamilyDecision — DEPRECATED in Phase 3 of Issue #11.
	// A past choice among alternatives. Migrated to TreeFamilyIntent
	// with the chosen branch denormalized via metadata. Constant
	// retained for backwards-compatibility in callers; new code
	// must not author Decision branches.
	TreeFamilyDecision TreeFamily = "decision"

	// TreeFamilyOutcome — the result of an agent's action. Carries
	// OutcomeStatus (Succeeded/Failed/Mixed/Ignored). Closes the
	// loop on a prior Intent. Distinct from Evidence (which lacks
	// agent attribution).
	TreeFamilyOutcome TreeFamily = "outcome"

	// TreeFamilyPreference — DEPRECATED. Subsumed by
	// TreeFamilyConstraint with Branch.ConstraintSeverity = "soft".
	// Constant retained for backwards-compatibility; the migration
	// in ensureForestConstraintSeverity rewrites old Preference
	// rows to Constraint+soft on first new-build start.
	TreeFamilyPreference TreeFamily = "preference"

	// TreeFamilyCapability — DEPRECATED in Phase 3 of Issue #11. An
	// agent's durable affordance is now expressed as agent metadata,
	// not a forest branch family. Constant retained for backwards-
	// compatibility; new code must not author Capability branches.
	TreeFamilyCapability TreeFamily = "capability"

	// TreeFamilyOpportunity — DEPRECATED in Phase 3 of Issue #11. A
	// time-bound match between Capability and Intent is now expressed
	// as an Intent branch with metadata.expires_at. Constant retained
	// for backwards-compatibility; new code must not author
	// Opportunity branches.
	TreeFamilyOpportunity TreeFamily = "opportunity"

	// TreeFamilyConflict — DEPRECATED in Phase 3 of Issue #11. A
	// live disagreement between branches is now encoded structurally
	// via RelayRelationContradicts edges. The forest's anti-precedent
	// quota + AntiPattern family cover the "surface negative
	// evidence" use case the family used to satisfy. Constant
	// retained for backwards-compatibility.
	TreeFamilyConflict TreeFamily = "conflict"

	// TreeFamilyAntiPattern — accumulated negative evidence. Distinct
	// from Conflict (which was an active dispute, now expressed via
	// RelayRelationContradicts). AntiPattern is a long-lived record
	// that "this approach has failed N times across sessions."
	// Created by the promotion projector when a Failed-outcome branch
	// crosses the configured failure-count + failure-rate threshold.
	// Surfaced through the anti-precedent quota in Retrieve regardless
	// of IncludeCounterEvidence.
	TreeFamilyAntiPattern TreeFamily = "antipattern"
)

// ConstraintSeverity discriminates hard rules (violations are
// errors) from soft rules (violations are suboptimal). Phase 1 of
// Issue #11 introduced the field so the forest can encode the
// audit's "Constraint vs Preference" distinction without keeping
// two families.
type ConstraintSeverity string

const (
	// ConstraintSeverityHard — violation is an error; the constraint
	// MUST hold. Contributes to ScopeRisk in scoring.
	ConstraintSeverityHard ConstraintSeverity = "hard"

	// ConstraintSeveritySoft — violation is suboptimal; the constraint
	// SHOULD hold. Contributes a bounded score penalty. This was
	// formerly TreeFamilyPreference.
	ConstraintSeveritySoft ConstraintSeverity = "soft"
)

// MemoryScope identifies the temporal learning horizon of a branch.
type MemoryScope string

const (
	ScopeWorking       MemoryScope = "working"
	ScopeEpisodic      MemoryScope = "episodic"
	ScopeSemantic      MemoryScope = "semantic"
	ScopeContradiction MemoryScope = "contradiction"
	ScopeDormant       MemoryScope = "dormant"
)

// BranchState captures the lifecycle state of a branch.
type BranchState string

const (
	BranchStateActive       BranchState = "active"
	BranchStateCandidate    BranchState = "candidate"
	BranchStateValidated    BranchState = "validated"
	BranchStateContradicted BranchState = "contradicted"
	BranchStateSuperseded   BranchState = "superseded"
	BranchStateDormant      BranchState = "dormant"
)

// EventType identifies the kind of ledger event being recorded.
type EventType string

const (
	EventTypeContentIndexed     EventType = "content_indexed"
	EventTypeDecisionRecorded   EventType = "decision_recorded"
	EventTypeOutcomeRecorded    EventType = "outcome_recorded"
	EventTypePreferenceRecorded EventType = "preference_recorded"
	EventTypeHypothesisRecorded EventType = "hypothesis_recorded"
	EventTypeRecall             EventType = "recall"
	EventTypeValidation         EventType = "validation"
	EventTypeContradiction      EventType = "contradiction"
	EventTypeReplayPromoted     EventType = "replay_promoted"
	EventTypeReplayConsolidated EventType = "replay_consolidated"
	EventTypeEcologyPruned      EventType = "ecology_pruned"
	EventTypeEcologyRegrown     EventType = "ecology_regrown"
	EventTypeClaimAccepted      EventType = "claim_accepted"
	EventTypeClaimRejected      EventType = "claim_rejected"
	EventTypeTestamentRecorded  EventType = "testament_recorded"

	// EventTypeClaimValidated records one validation outcome on a
	// claim. Carries the validation Type, QualityBar, Description, and
	// Verdict in Payload — strictly richer than the coord-era flat
	// review_completed signal it replaces. Routes to TreeFamilyEvidence
	// on accepted verdict and TreeFamilyAntiPattern on rejected verdict
	// via payload-driven family routing in the harvester.
	EventTypeClaimValidated EventType = "claim_validated"

	// EventTypeRemediationResolved records the closure of an architect-
	// authored corrective-action chain (claim_rejected → architect
	// remediation testament → re-validation accepted). Carries the
	// rejected-claim ID under Supersedes so the precedent links
	// failure → fix. Family is TreeFamilyEvidence (positive precedent
	// for "this kind of failure was fixed by this kind of remediation").
	EventTypeRemediationResolved EventType = "remediation_resolved"

	// EventTypeTraversalObserved records one claims-graph traversal call
	// (start_node, edge_filter, depth) and is the substrate for traversal
	// precedent — the forest can learn which graph walks at which start
	// kinds yielded useful downstream context. Family is TreeFamilyEvidence
	// when paired with a successful downstream outcome; the harvester
	// derives status from the activity State and salience from observed
	// downstream resolution rate.
	EventTypeTraversalObserved EventType = "traversal_observed"
)

// RelayRelation captures why two branches reinforce one another.
type RelayRelation string

const (
	RelayRelationSupports    RelayRelation = "supports"
	RelayRelationContradicts RelayRelation = "contradicts"
	RelayRelationSupersedes  RelayRelation = "supersedes"
	RelayRelationCoOccurs    RelayRelation = "co_occurs"
	RelayRelationPredicts    RelayRelation = "predicts"
)

// BaseScoreComponentCount is the dimensionality of the base scorer's
// input — the 13 component values aggregated into a Base score in
// scoreBatch. Constant across the package; every weight vector and
// component name list must have exactly this length.
const BaseScoreComponentCount = 13

// BaseScoreComponentNames lists the 13 component names in the order
// scoreBatch consumes them. Operators read this when interpreting
// learned weights or pruning diagnostics.
var BaseScoreComponentNames = [BaseScoreComponentCount]string{
	"query_match",
	"evidence",
	"canopy",
	"substrate",
	"frontier",
	"confidence",
	"recency",
	"warmth",
	"utility",
	"salience",
	"conflict_safety",
	"scope_safety",
	"inhibition_safety",
}

// BaseScoreModel is a learned linear scorer over the 13 component
// values. Replaces the hardcoded defaultScoreWeights with weights
// trained on outcome-labeled training examples (Issue #8).
//
// ID + Version uniquely identify a trained model. Role tags whether
// the model is currently serving as champion (default) or challenger
// (A/B sample). Multiple models may live at once — exactly one
// champion + at most one challenger; everything else is historical.
type BaseScoreModel struct {
	ID           string                           `json:"id"`
	Version      int64                            `json:"version"`
	Weights      [BaseScoreComponentCount]float64 `json:"weights"`
	Bias         float64                          `json:"bias"`
	L1Norm       float64                          `json:"l1_norm"`
	Accuracy     float64                          `json:"accuracy"`
	TrainingSize int64                            `json:"training_size"`
	TrainedAt    time.Time                        `json:"trained_at"`
	Role         BaseScoreRole                    `json:"role,omitempty"`
}

// BaseScoreRole tags a model's serving status.
type BaseScoreRole string

const (
	// BaseScoreRoleChampion — the model used by default for every
	// retrieval (modulo A/B sampling).
	BaseScoreRoleChampion BaseScoreRole = "champion"
	// BaseScoreRoleChallenger — sampled at BaseScoreABRate to
	// produce comparison data; promoted to champion when its outcome
	// rate beats the current champion.
	BaseScoreRoleChallenger BaseScoreRole = "challenger"
)

// BaseScoreVariant records which model produced one retrieval's
// scoring. Stored as audit metadata so BaseScoreVariantStatsSince
// can compare variants empirically.
type BaseScoreVariant string

const (
	BaseScoreVariantChampion   BaseScoreVariant = "champion"
	BaseScoreVariantChallenger BaseScoreVariant = "challenger"
	// BaseScoreVariantHardcoded — the cold-start variant: no model
	// has been trained or promoted yet, so retrieval used the
	// hardcoded defaultScoreWeights. Distinguished in stats so
	// operators can see how much traffic still runs on defaults.
	BaseScoreVariantHardcoded BaseScoreVariant = "hardcoded"
)

// SubstrateMode discriminates which substrate algorithm produced the
// signals consumed by Retrieve scoring. Captured per-retrieval in the
// audit ledger so operators can A/B-compare modes via
// SubstrateModeStatsSince. Issue #7's "earn its keep" measurement
// surface — the original 4-iteration diffusion (`full`) ran with no
// documented A/B; we now record which mode every retrieval used and
// can prove out which mode (or none) is the right one to keep.
type SubstrateMode string

const (
	// SubstrateModeFull — the original 4-iteration diffusion in
	// substrate.go. O(M·iter + N) per refresh + full edge rewrite.
	SubstrateModeFull SubstrateMode = "full"
	// SubstrateModePageRank — personalized PageRank over the relay
	// graph with canopy-root personalization. Cached by graph_version.
	// Issue #7 layer 2 replacement candidate.
	SubstrateModePageRank SubstrateMode = "pagerank"
	// SubstrateModeWarmthOnly — substrate signals are skipped entirely;
	// scoring falls back to warmth + the other 11 components. Used to
	// measure "is substrate doing anything at all?"
	SubstrateModeWarmthOnly SubstrateMode = "warmth_only"
)

// CanopyHorizon defines the horizon used for active root selection.
type CanopyHorizon string

const (
	CanopyHorizonTurn    CanopyHorizon = "turn"
	CanopyHorizonTask    CanopyHorizon = "task"
	CanopyHorizonSession CanopyHorizon = "session"
	CanopyHorizonUser    CanopyHorizon = "user"
	CanopyHorizonProject CanopyHorizon = "project"
)

// ReplayState tracks background replay work.
type ReplayState string

const (
	ReplayStateQueued   ReplayState = "queued"
	ReplayStateRunning  ReplayState = "running"
	ReplayStateComplete ReplayState = "complete"
)

// OutcomeStatus captures whether a branch outcome succeeded or failed.
type OutcomeStatus string

const (
	OutcomeStatusSucceeded OutcomeStatus = "succeeded"
	OutcomeStatusFailed    OutcomeStatus = "failed"
	OutcomeStatusMixed     OutcomeStatus = "mixed"
	// OutcomeStatusIgnored is generated by the implicit-negative
	// sweeper for retrievals whose returned branches saw no explicit
	// outcome event within the configured horizon. Treated as soft-
	// negative in training (lower weight than explicit failure).
	OutcomeStatusIgnored OutcomeStatus = "ignored"
)

// LabelSource discriminates how a training-example label was
// produced. Stored on forest_training_examples.label_source so the
// trainer can apply different weights and operators can audit how
// labels accumulated over time. Orthogonal to RetrievalMode — a row
// can have label_source='counterfactual' AND
// retrieval_mode='exploration' (an exploration retrieval whose loser
// got a counterfactual label later).
type LabelSource string

const (
	// LabelSourceExplicit — direct outcome/validation/contradiction
	// event named the branch.
	LabelSourceExplicit LabelSource = "explicit"
	// LabelSourceCounterfactual — labeled because the branch was a
	// candidate alongside another branch that received an explicit
	// outcome.
	LabelSourceCounterfactual LabelSource = "counterfactual"
	// LabelSourceImplicitNegative — labeled by the sweeper after
	// the configured horizon elapsed without an outcome event.
	LabelSourceImplicitNegative LabelSource = "implicit_negative"
)

// RetrievalMode discriminates the sampling strategy that produced a
// retrieval. Stored on forest_training_examples.retrieval_mode and
// forest_retrieval_events.exploration_mode-derived columns. Distinct
// from LabelSource (which describes the *outcome label*, not the
// *retrieval strategy*).
type RetrievalMode string

const (
	// RetrievalModeExploration — the retrieval used the ε-greedy
	// exploration path: top-K replaced with a random sample. Outcomes
	// from these retrievals are unbiased and weighted up at training.
	RetrievalModeExploration RetrievalMode = "exploration"
)

// TrainingLabelStats reports the count of training examples per
// LabelSource. Used by operators to verify the bias-correction
// mechanisms are accumulating signal as expected.
type TrainingLabelStats struct {
	Explicit         int64 `json:"explicit"`
	Counterfactual   int64 `json:"counterfactual"`
	ImplicitNegative int64 `json:"implicit_negative"`
	Exploration      int64 `json:"exploration"`
}

// Event is the append-only write unit of the forest ledger.
type Event struct {
	ID               string         `json:"id"`
	SessionID        string         `json:"session_id"`
	TaskID           string         `json:"task_id,omitempty"`
	AgentID          string         `json:"agent_id"`
	AgentType        string         `json:"agent_type"`
	EventType        EventType      `json:"event_type"`
	Family           TreeFamily     `json:"family"`
	Scope            MemoryScope    `json:"scope"`
	RootID           string         `json:"root_id"`
	BranchID         string         `json:"branch_id"`
	ParentBranchID   string         `json:"parent_branch_id,omitempty"`
	IntentID         string         `json:"intent_id,omitempty"`
	ContentID        string         `json:"content_id,omitempty"`
	SourceID         string         `json:"source_id,omitempty"`
	Confidence       float64        `json:"confidence"`
	Salience         float64        `json:"salience"`
	Timestamp        time.Time      `json:"timestamp"`
	Title            string         `json:"title,omitempty"`
	Summary          string         `json:"summary,omitempty"`
	ProvenanceRefs   []string       `json:"provenance_refs,omitempty"`
	Supersedes       []string       `json:"supersedes,omitempty"`
	Contradicts      []string       `json:"contradicts,omitempty"`
	RelatedBranchIDs []string       `json:"related_branch_ids,omitempty"`
	Payload          map[string]any `json:"payload,omitempty"`

	// Seq is the monotonic sequence assigned by forest_ledger on
	// append. Zero on construction; populated by AppendEvent and every
	// projector read. Projectors consume events strictly in Seq order to
	// maintain a deterministic projection.
	Seq int64 `json:"seq,omitempty"`
}

// RetrievalAuditEvent is the canonical record of one Retrieve call.
// Append-only — the row in forest_retrieval_events is written once
// and never modified. Captures the full ranked candidate set (not
// just the top-K returned) so counterfactual training can label all
// candidates from outcome events.
type RetrievalAuditEvent struct {
	ID  string `json:"id"`
	Seq int64  `json:"seq,omitempty"`

	// Inputs reconstruct the query exactly.
	SessionID              string        `json:"session_id"`
	TaskID                 string        `json:"task_id,omitempty"`
	AgentID                string        `json:"agent_id,omitempty"`
	AgentType              string        `json:"agent_type,omitempty"`
	IntentID               string        `json:"intent_id,omitempty"`
	Query                  string        `json:"query"`
	Horizon                CanopyHorizon `json:"horizon,omitempty"`
	Families               []TreeFamily  `json:"families,omitempty"`
	RequestedLimit         int           `json:"requested_limit"`
	IncludeCounterEvidence bool          `json:"include_counter_evidence"`
	RequestedAt            time.Time     `json:"requested_at"`

	// Operational metadata observed during the retrieval call.
	Duration       time.Duration `json:"duration"`
	CandidateCount int           `json:"candidate_count"`
	ReturnedCount  int           `json:"returned_count"`
	ModelKey       string        `json:"model_key,omitempty"`
	ModelVersion   int           `json:"model_version,omitempty"`
	ErrorMessage   string        `json:"error_message,omitempty"`

	// BranchProjectionSeq snapshots the branch projector's
	// last_applied_seq at retrieval time so historical scoring is
	// replayable against a known projection version.
	BranchProjectionSeq int64 `json:"branch_projection_seq"`

	// ExplorationMode is true when this retrieval was an ε-greedy
	// exploration sample — the top-K was a random selection from
	// the broader candidate pool rather than the model-ranked top.
	// Outcomes from exploration retrievals are unbiased and get
	// higher training-label weight.
	ExplorationMode bool `json:"exploration_mode,omitempty"`

	// SubstrateMode records which substrate algorithm produced the
	// signals consumed by scoring on this retrieval. Captured from
	// resolveRetrieveSubstrateMode at retrieve-time. Empty on legacy
	// rows (pre-Issue #7); filtered out of mode comparison stats.
	SubstrateMode SubstrateMode `json:"substrate_mode,omitempty"`

	// BaseScoreVersion records which trained base-score model
	// produced this retrieval's Base scores. 0 = hardcoded defaults
	// (cold-start or pre-Issue #8). Captured at retrieve-time so
	// BaseScoreVariantStatsSince can roll up per-version outcomes.
	BaseScoreVersion int64 `json:"base_score_version,omitempty"`

	// BaseScoreVariant tags whether this retrieval ran with the
	// champion model, the challenger (A/B sample), or hardcoded
	// fallback. Distinct from BaseScoreVersion (which is a numeric
	// model identity) — BaseScoreVariant says which serving role
	// the model was playing at retrieve-time.
	BaseScoreVariant BaseScoreVariant `json:"base_score_variant,omitempty"`

	// HyperparamSnapshotID is the SnapshotID of the HyperParameters
	// instance pinned at retrieval entry. Stamps the audit row so
	// outcome-driven adaptation can attribute observations to the
	// snapshot they came from even after a snapshot replacement.
	HyperparamSnapshotID int64 `json:"hyperparam_snapshot_id,omitempty"`

	// ProposedHyperparams is true when the pinned snapshot was the
	// A/B-trial proposed (challenger) snapshot rather than the
	// active one. Read by ObserveAndAdapt to route the observation
	// into the correct rolling window.
	ProposedHyperparams bool `json:"proposed_hyperparams,omitempty"`

	// Candidates is the full scored set, not just top-K. Each entry
	// records its rank position (1..K for returned; 0 for considered
	// but not returned) and full per-component detail.
	Candidates []RetrievalAuditCandidate `json:"candidates"`

	// Metadata carries arbitrary observation data.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// RetrievalAuditCandidate captures one candidate's full scoring
// detail at retrieval time. RankPosition is 1..K for candidates
// returned to the caller; 0 for candidates that were scored but
// filtered out before truncation.
type RetrievalAuditCandidate struct {
	BranchID         string             `json:"branch_id"`
	RankPosition     int                `json:"rank_position"`
	Returned         bool               `json:"returned"`
	BaseScore        float64            `json:"base_score"`
	FinalScore       float64            `json:"final_score"`
	PredictedUtility float64            `json:"predicted_utility"`
	PredictedRisk    float64            `json:"predicted_risk"`
	Components       map[string]float64 `json:"components"`
	FeatureVector    []float32          `json:"feature_vector,omitempty"`
}

// RetrievalAuditFilter narrows QueryRetrievalAudits.
type RetrievalAuditFilter struct {
	SessionID string    `json:"session_id,omitempty"`
	AgentID   string    `json:"agent_id,omitempty"`
	IntentID  string    `json:"intent_id,omitempty"`
	BranchID  string    `json:"branch_id,omitempty"` // matches retrievals that included this branch in candidates
	Since     time.Time `json:"since,omitempty"`
	Until     time.Time `json:"until,omitempty"`
	Limit     int       `json:"limit,omitempty"`
}

// Branch is the materialized branch projection stored by the forest.
type Branch struct {
	ID             string         `json:"id"`
	RootID         string         `json:"root_id"`
	ParentID       string         `json:"parent_id,omitempty"`
	Family         TreeFamily     `json:"family"`
	Scope          MemoryScope    `json:"scope"`
	State          BranchState    `json:"state"`
	SessionID      string         `json:"session_id"`
	TaskID         string         `json:"task_id,omitempty"`
	AgentID        string         `json:"agent_id,omitempty"`
	AgentType      string         `json:"agent_type,omitempty"`
	IntentID       string         `json:"intent_id,omitempty"`
	Title          string         `json:"title"`
	Summary        string         `json:"summary"`
	Confidence     float64        `json:"confidence"`
	Salience       float64        `json:"salience"`
	Utility        float64        `json:"utility"`
	SuccessRate    float64        `json:"success_rate"`
	ScopeRisk      float64        `json:"scope_risk"`
	ConflictScore  float64        `json:"conflict_score"`
	SupportCount   int            `json:"support_count"`
	CounterCount   int            `json:"counter_count"`
	SuccessCount   int            `json:"success_count"`
	FailureCount   int            `json:"failure_count"`
	AccessCount    int            `json:"access_count"`
	LastAccessedAt time.Time      `json:"last_accessed_at,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	Metadata       map[string]any `json:"metadata,omitempty"`

	// ConstraintSeverity discriminates hard vs soft rules for
	// Constraint-family branches. Empty for non-Constraint families.
	// Phase 1 of Issue #11 — subsumes the deprecated TreeFamilyPreference
	// via severity="soft".
	ConstraintSeverity ConstraintSeverity `json:"constraint_severity,omitempty"`

	// LastAppliedSeq is the highest event Seq the branch projection
	// has consumed. Maintained by the branch projector inside the
	// same transaction that updates the rest of the projection.
	// Readers detect projection freshness via this field.
	LastAppliedSeq int64 `json:"last_applied_seq,omitempty"`
}

// Canopy is the active root set for a specific horizon.
type Canopy struct {
	Key       string        `json:"key"`
	SessionID string        `json:"session_id,omitempty"`
	TaskID    string        `json:"task_id,omitempty"`
	IntentID  string        `json:"intent_id,omitempty"`
	Horizon   CanopyHorizon `json:"horizon"`
	RootIDs   []string      `json:"root_ids"`
	Summary   string        `json:"summary,omitempty"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// PacketEvidence is a supporting or contradicting evidence item inside a branch packet.
type PacketEvidence struct {
	ContentID      string    `json:"content_id"`
	ContentType    string    `json:"content_type,omitempty"`
	Summary        string    `json:"summary"`
	Confidence     float64   `json:"confidence"`
	Salience       float64   `json:"salience"`
	ProvenanceRefs []string  `json:"provenance_refs,omitempty"`
	Timestamp      time.Time `json:"timestamp"`
}

// PacketConflict captures an unresolved contradiction or caution.
type PacketConflict struct {
	BranchID string  `json:"branch_id,omitempty"`
	Summary  string  `json:"summary"`
	Severity float64 `json:"severity"`
}

// PacketAction is a recommended next move for the calling agent.
type PacketAction struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// PacketScore explains why a branch packet ranked where it did.
type PacketScore struct {
	Total            float64 `json:"total"`
	Base             float64 `json:"base"`
	Learned          float64 `json:"learned"`
	QueryMatch       float64 `json:"query_match"`
	Evidence         float64 `json:"evidence"`
	Canopy           float64 `json:"canopy"`
	Substrate        float64 `json:"substrate"`
	Frontier         float64 `json:"frontier"`
	Confidence       float64 `json:"confidence"`
	Recency          float64 `json:"recency"`
	Warmth           float64 `json:"warmth"`
	Utility          float64 `json:"utility"`
	Salience         float64 `json:"salience"`
	Conflict         float64 `json:"conflict"`
	ScopeSafety      float64 `json:"scope_safety"`
	InhibitionSafety float64 `json:"inhibition_safety"`
	RiskPenalty      float64 `json:"risk_penalty"`
	ModelConfidence  float64 `json:"model_confidence"`
	// ModelCalibration is 1 - ECE for the active base-score model
	// computed over the most recent CalibrationWindow. 1.0 = the
	// model's predictions match observed outcomes exactly; 0.0 =
	// every prediction is fully off. Refreshed lazily after a
	// model promotion. Zero when no calibration data has been
	// computed yet (cold-start).
	ModelCalibration float64 `json:"model_calibration"`
}

// FeatureSignal captures a salient normalized feature in a learned prediction.
type FeatureSignal struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

// LearnedPrediction captures forest reranker predictions for a branch packet.
type LearnedPrediction struct {
	Utility        float64         `json:"utility"`
	Risk           float64         `json:"risk"`
	Replay         float64         `json:"replay"`
	Clarification  float64         `json:"clarification"`
	Confidence     float64         `json:"confidence"`
	UtilityModel   string          `json:"utility_model,omitempty"`
	UtilityVersion int             `json:"utility_version,omitempty"`
	RiskModel      string          `json:"risk_model,omitempty"`
	RiskVersion    int             `json:"risk_version,omitempty"`
	Signals        []FeatureSignal `json:"signals,omitempty"`
}

// BranchPacket is the agent-facing retrieval unit.
//
// MEM-05: every packet carries three first-class dimensions the Architect
// and other consumers must interpret:
//
//   - Provenance (via ProvenanceRefs on each Support item). Origin chain
//     for every supporting observation, keyed by source-record IDs.
//   - Confidence (via Branch.Confidence and Score.Confidence). The
//     branch-level posterior confidence and the packet's scoring-layer
//     confidence; helper methods below expose a rolled-up view.
//   - Conflicts (via Conflicts). Unresolved contradictions with
//     severities so the consumer can escalate, discard, or route for
//     review rather than trust every packet equally.
type BranchPacket struct {
	Branch          *Branch            `json:"branch"`
	Support         []PacketEvidence   `json:"support,omitempty"`
	CounterEvidence []PacketEvidence   `json:"counter_evidence,omitempty"`
	Conflicts       []PacketConflict   `json:"conflicts,omitempty"`
	NextActions     []PacketAction     `json:"next_actions,omitempty"`
	Score           PacketScore        `json:"score"`
	Prediction      *LearnedPrediction `json:"prediction,omitempty"`
	// Source tags how this packet entered the result set. Empty (or
	// RetrievalSourcePrimary) means it came from the normal scoring
	// pipeline. Other values mark fallback or quota-reserved packets:
	// RetrievalSourceGlobalPrior (Issue #5 cold-start fill),
	// RetrievalSourceAntiPrecedent (Issue #6 negative-evidence quota).
	Source RetrievalSource `json:"source,omitempty"`
}

// RetrievalSource discriminates the channel that placed a packet in
// the result set. Operators read this to measure how often the forest
// is in cold-start mode (GlobalPrior) and how often anti-precedents
// are surfacing (AntiPrecedent).
type RetrievalSource string

const (
	// RetrievalSourcePrimary — the packet came through the normal
	// score → MMR → top-K pipeline.
	RetrievalSourcePrimary RetrievalSource = "primary"
	// RetrievalSourcePrimed — the packet surfaced through a primed
	// canopy created by PrimeSession (cross-session bootstrap from
	// the most-recent prior session of the same agent type).
	// Distinct from RetrievalSourcePrimary because the branches
	// originated outside this session and were copied in by the
	// priming step.
	RetrievalSourcePrimed RetrievalSource = "primed"
	// RetrievalSourceGlobalPrior — the packet was filled from the
	// agent-type/family global priors when primary returned < K.
	RetrievalSourceGlobalPrior RetrievalSource = "global_prior"
	// RetrievalSourceAntiPrecedent — the packet was reserved as an
	// anti-precedent (failed-outcome or AntiPattern family).
	RetrievalSourceAntiPrecedent RetrievalSource = "anti_precedent"
)

// ProvenanceRefs returns the union of provenance refs across every
// supporting evidence item on the packet, deduplicated and in a stable
// order (first-seen wins). Consumers use this to trace a packet back to
// its source observations without walking Support themselves.
func (p *BranchPacket) ProvenanceRefs() []string {
	if p == nil {
		return nil
	}
	seen := make(map[string]struct{})
	refs := make([]string, 0)
	for _, ev := range p.Support {
		for _, ref := range ev.ProvenanceRefs {
			if ref == "" {
				continue
			}
			if _, already := seen[ref]; already {
				continue
			}
			seen[ref] = struct{}{}
			refs = append(refs, ref)
		}
	}
	return refs
}

// RolledUpConfidence returns a single-number confidence view that
// combines the branch-level confidence (from Branch.Confidence) with
// the packet-scoring-layer confidence (Score.Confidence). Returns 0
// when the packet carries no branch.
//
// The combination is the geometric mean — it rewards agreement between
// the two signals and penalizes disagreement. When either is zero the
// rolled-up value is zero (conservative), mirroring the "weakest link"
// intuition consumers expect.
func (p *BranchPacket) RolledUpConfidence() float64 {
	if p == nil || p.Branch == nil {
		return 0
	}
	if p.Branch.Confidence <= 0 || p.Score.Confidence <= 0 {
		return 0
	}
	product := p.Branch.Confidence * p.Score.Confidence
	return math.Sqrt(product)
}

// MaxConflictSeverity returns the highest conflict severity attached to
// the packet, or 0 if there are no conflicts. Consumers use this as a
// quick gating signal — a packet with severity > threshold should be
// routed for architect review before being acted upon.
func (p *BranchPacket) MaxConflictSeverity() float64 {
	if p == nil {
		return 0
	}
	var max float64
	for _, c := range p.Conflicts {
		if c.Severity > max {
			max = c.Severity
		}
	}
	return max
}

// HasUnresolvedConflicts reports whether the packet carries any conflict
// with non-zero severity. Convenience for the common "do I need to
// escalate this packet?" check.
func (p *BranchPacket) HasUnresolvedConflicts() bool {
	return p.MaxConflictSeverity() > 0
}

// Query configures a forest retrieval request.
type Query struct {
	Query                  string        `json:"query"`
	SessionID              string        `json:"session_id,omitempty"`
	TaskID                 string        `json:"task_id,omitempty"`
	AgentID                string        `json:"agent_id,omitempty"`
	AgentType              string        `json:"agent_type,omitempty"`
	IntentID               string        `json:"intent_id,omitempty"`
	Families               []TreeFamily  `json:"families,omitempty"`
	Horizon                CanopyHorizon `json:"horizon,omitempty"`
	Limit                  int           `json:"limit,omitempty"`
	IncludeCounterEvidence bool          `json:"include_counter_evidence,omitempty"`
}

// ResolveIntentInput requests the active intent envelope for a query.
type ResolveIntentInput struct {
	Query     string        `json:"query"`
	SessionID string        `json:"session_id,omitempty"`
	TaskID    string        `json:"task_id,omitempty"`
	AgentID   string        `json:"agent_id,omitempty"`
	AgentType string        `json:"agent_type,omitempty"`
	IntentID  string        `json:"intent_id,omitempty"`
	Limit     int           `json:"limit,omitempty"`
	Horizon   CanopyHorizon `json:"horizon,omitempty"`
}

// IntentResolution returns the active intent frontier for the caller.
type IntentResolution struct {
	Query          string         `json:"query"`
	PrimaryIntent  string         `json:"primary_intent,omitempty"`
	ActiveRoots    []string       `json:"active_roots"`
	Constraints    []BranchPacket `json:"constraints,omitempty"`
	Preferences    []BranchPacket `json:"preferences,omitempty"`
	IntentBranches []BranchPacket `json:"intent_branches,omitempty"`
	OutcomeHints   []BranchPacket `json:"outcome_hints,omitempty"`
}

// OutcomeRecord appends an outcome event to the forest.
type OutcomeRecord struct {
	BranchID       string        `json:"branch_id"`
	SessionID      string        `json:"session_id,omitempty"`
	TaskID         string        `json:"task_id,omitempty"`
	AgentID        string        `json:"agent_id,omitempty"`
	AgentType      string        `json:"agent_type,omitempty"`
	Status         OutcomeStatus `json:"status"`
	Summary        string        `json:"summary"`
	Confidence     float64       `json:"confidence,omitempty"`
	Salience       float64       `json:"salience,omitempty"`
	ProvenanceRefs []string      `json:"provenance_refs,omitempty"`
	Supersedes     []string      `json:"supersedes,omitempty"`
	Contradicts    []string      `json:"contradicts,omitempty"`
}

// ReplayResult reports how many queued items were consolidated.
type ReplayResult struct {
	Processed int `json:"processed"`
	Promoted  int `json:"promoted"`
}

// SubstrateResult reports substrate state refresh activity.
type SubstrateResult struct {
	StatesUpdated    int `json:"states_updated"`
	EdgesUpdated     int `json:"edges_updated"`
	FrontiersUpdated int `json:"frontiers_updated"`
}

// TrainingResult reports how many learned models were refreshed.
type TrainingResult struct {
	Trained int      `json:"trained"`
	Keys    []string `json:"keys,omitempty"`
}

// EcologyResult reports how many branches changed visibility state.
type EcologyResult struct {
	Dormant int `json:"dormant"`
	Regrown int `json:"regrown"`
}

// defaultFamilies returns the canonical post-Phase-3 taxonomy: the
// five collapsed families plus AntiPattern. Deprecated families
// (Decision/Preference/Capability/Opportunity/Conflict) are excluded
// because every author path canonicalizes to the consolidated set,
// and the family migration in ensureForestFamilyMigration rewrites
// pre-existing rows. Callers asking for "all families" should never
// retrieve along the deprecated axes.
func defaultFamilies() []TreeFamily {
	return []TreeFamily{
		TreeFamilyIntent,
		TreeFamilyConstraint,
		TreeFamilyEvidence,
		TreeFamilyOutcome,
		TreeFamilyAntiPattern,
	}
}

// canonicalizeFamily maps a deprecated TreeFamily value to its
// post-Phase-3 consolidated equivalent. Idempotent for canonical
// inputs. Applied at every author boundary (AppendEvent, content-
// type routing) and at the query-Families filter boundary so legacy
// callers keep working without surfacing deprecated families through
// the projection.
func canonicalizeFamily(family TreeFamily) TreeFamily {
	switch family {
	case TreeFamilyDecision, TreeFamilyCapability, TreeFamilyOpportunity:
		return TreeFamilyIntent
	case TreeFamilyPreference:
		return TreeFamilyConstraint
	case TreeFamilyConflict:
		return TreeFamilyAntiPattern
	default:
		return family
	}
}

// canonicalizeFamilies applies canonicalizeFamily across a slice and
// dedupes the result while preserving first-seen order. Empty input
// returns nil so downstream callers see the same "no filter" semantics
// they did before Phase 3.
func canonicalizeFamilies(families []TreeFamily) []TreeFamily {
	if len(families) == 0 {
		return nil
	}
	seen := make(map[TreeFamily]struct{}, len(families))
	out := make([]TreeFamily, 0, len(families))
	for _, family := range families {
		canonical := canonicalizeFamily(family)
		if canonical == "" {
			continue
		}
		if _, ok := seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}
		out = append(out, canonical)
	}
	return out
}
