// Package forest provides the Memory Forest runtime for intent-first,
// multi-tree memory projection and retrieval.
package forest

import "time"

// TreeFamily identifies the specialized tree a branch belongs to.
type TreeFamily string

const (
	TreeFamilyIntent      TreeFamily = "intent"
	TreeFamilyConstraint  TreeFamily = "constraint"
	TreeFamilyEvidence    TreeFamily = "evidence"
	TreeFamilyDecision    TreeFamily = "decision"
	TreeFamilyOutcome     TreeFamily = "outcome"
	TreeFamilyPreference  TreeFamily = "preference"
	TreeFamilyCapability  TreeFamily = "capability"
	TreeFamilyOpportunity TreeFamily = "opportunity"
	TreeFamilyConflict    TreeFamily = "conflict"
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

// CanopyHorizon defines the horizon used for active root selection.
type CanopyHorizon string

const (
	CanopyHorizonTurn    CanopyHorizon = "turn"
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
)

// Event is the append-only write unit of the forest ledger.
type Event struct {
	ID               string         `json:"id"`
	SessionID        string         `json:"session_id"`
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
}

// Canopy is the active root set for a specific horizon.
type Canopy struct {
	Key       string        `json:"key"`
	SessionID string        `json:"session_id,omitempty"`
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
type BranchPacket struct {
	Branch          *Branch            `json:"branch"`
	Support         []PacketEvidence   `json:"support,omitempty"`
	CounterEvidence []PacketEvidence   `json:"counter_evidence,omitempty"`
	Conflicts       []PacketConflict   `json:"conflicts,omitempty"`
	NextActions     []PacketAction     `json:"next_actions,omitempty"`
	Score           PacketScore        `json:"score"`
	Prediction      *LearnedPrediction `json:"prediction,omitempty"`
}

// Query configures a forest retrieval request.
type Query struct {
	Query                  string        `json:"query"`
	SessionID              string        `json:"session_id,omitempty"`
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

func defaultFamilies() []TreeFamily {
	return []TreeFamily{
		TreeFamilyIntent,
		TreeFamilyConstraint,
		TreeFamilyEvidence,
		TreeFamilyDecision,
		TreeFamilyOutcome,
		TreeFamilyPreference,
		TreeFamilyCapability,
		TreeFamilyOpportunity,
		TreeFamilyConflict,
	}
}
