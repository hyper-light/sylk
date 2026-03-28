package skills

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/adalundhe/sylk/core/forest"
	"github.com/adalundhe/sylk/core/skills"
)

// ForestService captures the forest capabilities exposed to skills.
type ForestService interface {
	ResolveIntent(ctx context.Context, input forest.ResolveIntentInput) (*forest.IntentResolution, error)
	Retrieve(ctx context.Context, query forest.Query) ([]*forest.BranchPacket, error)
	PredictNextBranches(ctx context.Context, query forest.Query) ([]*forest.BranchPacket, error)
	RecordOutcome(ctx context.Context, record forest.OutcomeRecord) error
}

// ForestRecallInput requests branch packets from the forest.
type ForestRecallInput struct {
	Query                  string   `json:"query"`
	SessionID              string   `json:"session_id,omitempty"`
	IntentID               string   `json:"intent_id,omitempty"`
	Families               []string `json:"families,omitempty"`
	Limit                  int      `json:"limit,omitempty"`
	IncludeCounterEvidence bool     `json:"include_counter_evidence,omitempty"`
}

// ForestRecallOutput returns forest packets.
type ForestRecallOutput struct {
	Packets []*forest.BranchPacket `json:"packets"`
}

// ForestOutcomeInput records explicit branch outcome feedback.
type ForestOutcomeInput struct {
	BranchID   string  `json:"branch_id"`
	SessionID  string  `json:"session_id,omitempty"`
	Status     string  `json:"status"`
	Summary    string  `json:"summary"`
	Confidence float64 `json:"confidence,omitempty"`
	Salience   float64 `json:"salience,omitempty"`
}

// ForestOutcomeOutput reports write success.
type ForestOutcomeOutput struct {
	Recorded bool `json:"recorded"`
}

// NewForestResolveIntentSkill creates the forest_resolve_intent skill.
func NewForestResolveIntentSkill(deps *RetrievalDependencies) *skills.Skill {
	return skills.NewSkill("forest_resolve_intent").
		Description("Resolve the active user intent frontier, constraints, preferences, and likely outcome hints from the Memory Forest.").
		Domain(RetrievalDomain).
		Keywords("intent", "goal", "constraint", "preference", "memory forest").
		Priority(100).
		StringParam("query", "Natural language description of the current task or user request", true).
		StringParam("session_id", "Optional session identifier for session-scoped intent resolution", false).
		StringParam("intent_id", "Optional explicit intent identifier", false).
		IntParam("limit", "Maximum number of supporting branches to inspect", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			if deps == nil || deps.Forest == nil {
				return nil, fmt.Errorf("forest is not configured")
			}
			var params forest.ResolveIntentInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid input: %w", err)
			}
			return deps.Forest.ResolveIntent(ctx, params)
		}).
		Build()
}

// NewForestRecallSkill creates the forest_recall skill.
func NewForestRecallSkill(deps *RetrievalDependencies) *skills.Skill {
	return skills.NewSkill("forest_recall").
		Description("Retrieve ranked branch packets from the Memory Forest, including support, conflicts, and recommended next actions.").
		Domain(RetrievalDomain).
		Keywords("recall", "precedent", "branch", "history", "forest").
		Priority(100).
		StringParam("query", "Natural language query for branch recall", true).
		StringParam("session_id", "Optional session identifier", false).
		StringParam("intent_id", "Optional explicit intent identifier", false).
		ArrayParam("families", "Optional tree families to constrain recall", "string", false).
		IntParam("limit", "Maximum number of packets to return", false).
		BoolParam("include_counter_evidence", "Whether to include contradictory evidence", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			if deps == nil || deps.Forest == nil {
				return nil, fmt.Errorf("forest is not configured")
			}
			var params ForestRecallInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid input: %w", err)
			}
			packets, err := deps.Forest.Retrieve(ctx, forest.Query{
				Query:                  params.Query,
				SessionID:              params.SessionID,
				IntentID:               params.IntentID,
				Families:               parseForestFamilies(params.Families),
				Limit:                  params.Limit,
				IncludeCounterEvidence: params.IncludeCounterEvidence,
			})
			if err != nil {
				return nil, err
			}
			return &ForestRecallOutput{Packets: packets}, nil
		}).
		Build()
}

// NewForestPredictNextSkill creates the forest_predict_next_branches skill.
func NewForestPredictNextSkill(deps *RetrievalDependencies) *skills.Skill {
	return skills.NewSkill("forest_predict_next_branches").
		Description("Predict low-risk adjacent branches that could safely improve the current work beyond literal prompt compliance.").
		Domain(RetrievalDomain).
		Keywords("predict", "next", "opportunity", "adjacent value", "forest").
		Priority(95).
		StringParam("query", "Natural language description of the current task", true).
		StringParam("session_id", "Optional session identifier", false).
		StringParam("intent_id", "Optional explicit intent identifier", false).
		IntParam("limit", "Maximum number of packets to return", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			if deps == nil || deps.Forest == nil {
				return nil, fmt.Errorf("forest is not configured")
			}
			var params ForestRecallInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid input: %w", err)
			}
			packets, err := deps.Forest.PredictNextBranches(ctx, forest.Query{
				Query:     params.Query,
				SessionID: params.SessionID,
				IntentID:  params.IntentID,
				Limit:     params.Limit,
			})
			if err != nil {
				return nil, err
			}
			return &ForestRecallOutput{Packets: packets}, nil
		}).
		Build()
}

// NewForestRecordOutcomeSkill creates the forest_record_outcome skill.
func NewForestRecordOutcomeSkill(deps *RetrievalDependencies) *skills.Skill {
	return skills.NewSkill("forest_record_outcome").
		Description("Record explicit outcome feedback for a branch so the forest can reconsolidate and learn from the result.").
		Domain(RetrievalDomain).
		Keywords("outcome", "feedback", "learn", "reconsolidate", "forest").
		Priority(90).
		StringParam("branch_id", "Branch identifier to update", true).
		StringParam("session_id", "Optional session identifier", false).
		StringParam("status", "Outcome status: succeeded, failed, or mixed", true).
		StringParam("summary", "Short description of what happened", true).
		FloatParam("confidence", "Confidence in the recorded outcome", false).
		FloatParam("salience", "Salience of the recorded outcome", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			if deps == nil || deps.Forest == nil {
				return nil, fmt.Errorf("forest is not configured")
			}
			var params ForestOutcomeInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid input: %w", err)
			}
			if err := deps.Forest.RecordOutcome(ctx, forest.OutcomeRecord{
				BranchID:   params.BranchID,
				SessionID:  params.SessionID,
				Status:     forest.OutcomeStatus(params.Status),
				Summary:    params.Summary,
				Confidence: params.Confidence,
				Salience:   params.Salience,
			}); err != nil {
				return nil, err
			}
			return &ForestOutcomeOutput{Recorded: true}, nil
		}).
		Build()
}

func parseForestFamilies(values []string) []forest.TreeFamily {
	if len(values) == 0 {
		return nil
	}
	families := make([]forest.TreeFamily, 0, len(values))
	for _, value := range values {
		switch forest.TreeFamily(value) {
		case forest.TreeFamilyIntent,
			forest.TreeFamilyConstraint,
			forest.TreeFamilyEvidence,
			forest.TreeFamilyDecision,
			forest.TreeFamilyOutcome,
			forest.TreeFamilyPreference,
			forest.TreeFamilyCapability,
			forest.TreeFamilyOpportunity,
			forest.TreeFamilyConflict:
			families = append(families, forest.TreeFamily(value))
		}
	}
	return families
}
