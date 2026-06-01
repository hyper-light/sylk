package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/forest"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

// ForestService captures the forest capabilities exposed to skills.
type ForestService interface {
	ResolveIntent(ctx context.Context, input forest.ResolveIntentInput) (*forest.IntentResolution, error)
	RetrieveForest(ctx context.Context, query forest.Query) ([]*forest.ForestPacket, error)
	CreateForestCursor(ctx context.Context, input forest.ForestCursorInput) (*forest.ForestCursor, error)
	ProposeForestClaim(ctx context.Context, proposal forest.ForestClaimProposal) error
	RecordOutcome(ctx context.Context, record forest.OutcomeRecord) error
}

// ForestRecallInput requests evidence-backed packets from the forest.
type ForestRecallInput struct {
	Query                  string `json:"query"`
	SessionID              string `json:"session_id,omitempty"`
	TaskID                 string `json:"task_id,omitempty"`
	IntentID               string `json:"intent_id,omitempty"`
	Horizon                string `json:"horizon,omitempty"`
	Limit                  int    `json:"limit,omitempty"`
	IncludeCounterEvidence bool   `json:"include_counter_evidence,omitempty"`
}

// ForestRecallOutput returns forest packets.
type ForestRecallOutput struct {
	Packets []*forest.ForestPacket `json:"packets"`
	Cursor  *forest.ForestCursor   `json:"cursor,omitempty"`
}

// RecallRecentInput requests recent preserved context from the Memory Forest.
type RecallRecentInput struct {
	Query                  string `json:"query,omitempty"`
	SessionID              string `json:"session_id,omitempty"`
	TaskID                 string `json:"task_id,omitempty"`
	IntentID               string `json:"intent_id,omitempty"`
	Horizon                string `json:"horizon,omitempty"`
	Limit                  int    `json:"limit,omitempty"`
	IncludeCounterEvidence *bool  `json:"include_counter_evidence,omitempty"`
}

// RecallRecentOutput returns a compact continuity summary plus the raw packets.
type RecallRecentOutput struct {
	Summary string                   `json:"summary"`
	Focus   []string                 `json:"focus,omitempty"`
	Intent  *forest.IntentResolution `json:"intent,omitempty"`
	Packets []*forest.ForestPacket   `json:"packets,omitempty"`
	Cursor  *forest.ForestCursor     `json:"cursor,omitempty"`
}

// ForestOutcomeInput records explicit node outcome feedback.
type ForestOutcomeInput struct {
	NodeID     string  `json:"node_id"`
	CursorID   string  `json:"cursor_id,omitempty"`
	SessionID  string  `json:"session_id,omitempty"`
	TaskID     string  `json:"task_id,omitempty"`
	Status     string  `json:"status"`
	Summary    string  `json:"summary"`
	Confidence float64 `json:"confidence,omitempty"`
	Salience   float64 `json:"salience,omitempty"`
}

// ForestOutcomeOutput reports write success.
type ForestOutcomeOutput struct {
	Recorded bool `json:"recorded"`
}

type ForestProposalInput struct {
	Summary                string   `json:"summary"`
	ClusterID              string   `json:"cluster_id,omitempty"`
	Dimension              string   `json:"dimension,omitempty"`
	EvidenceRefs           []string `json:"evidence_refs,omitempty"`
	CounterEvidenceRefs    []string `json:"counter_evidence_refs,omitempty"`
	GuardianReviewRequired bool     `json:"guardian_review_required,omitempty"`
}

type ForestValidationSuggestionOutput struct {
	Packets         []*forest.ForestPacket               `json:"packets"`
	Cursor          *forest.ForestCursor                 `json:"cursor,omitempty"`
	ValidationNeeds []string                             `json:"validation_needs,omitempty"`
	ProposedClaims  []forest.ForestClaimProposalTemplate `json:"proposed_claims,omitempty"`
}

type ForestProposalOutput struct {
	Proposed bool                       `json:"proposed"`
	Proposal forest.ForestClaimProposal `json:"proposal"`
}

// NewForestSkill returns a compatibility wrapper over the ForestPacket-native
// read operations. It is not registered in the LLM-visible catalog; phase-9
// agents receive the agency-aware forest.* skill surface below.
func NewForestSkill(deps *RetrievalDependencies) *skills.Skill {
	resolveIntent := NewForestResolveIntentSkill(deps)
	recall := NewForestRecallSkill(deps)
	recallRecent := NewRecallRecentSkill(deps)
	predictNext := NewForestPredictNextSkill(deps)

	return skills.NewSkill("forest").
		Description("Query the Memory Forest. One primitive for every read-side operation across session/task/project horizons.\n\n"+
			"Ops:\n"+
			"- resolve_intent: Resolve the active user intent, constraints, preferences, and likely outcome hints (params: query, horizon?, limit?)\n"+
			"- recall: Retrieve evidence-backed ForestPackets with support, conflicts, cursor context, and validation needs (params: query, horizon?, limit?, include_counter_evidence?)\n"+
			"- recall_recent: Recover recent preserved context so a terse follow-up can continue from earlier discussion (params: query?, horizon?, limit?, include_counter_evidence?)\n"+
			"- predict_next: Retrieve low-risk adjacent ForestPackets that could safely improve the current work (params: query, horizon?, limit?)").
		Domain(RetrievalDomain).
		Keywords("forest", "memory", "recall", "intent", "constraint", "preference", "precedent", "node", "history", "continuity", "predict", "adjacent").
		Priority(100).
		EnumParam("op", "Forest query operation", []string{"resolve_intent", "recall", "recall_recent", "predict_next"}, true).
		StringParam("query", "Natural language query (required for resolve_intent/recall/predict_next; optional for recall_recent)", false).
		StringParam("session_id", "Optional session identifier", false).
		StringParam("task_id", "Optional task identifier for task-scoped queries", false).
		StringParam("intent_id", "Optional explicit intent identifier", false).
		EnumParam("horizon", "Optional canopy horizon: turn, task, session, user, or project.", []string{
			string(forest.CanopyHorizonTurn),
			string(forest.CanopyHorizonTask),
			string(forest.CanopyHorizonSession),
			string(forest.CanopyHorizonUser),
			string(forest.CanopyHorizonProject),
		}, false).
		IntParam("limit", "Maximum number of packets to return", false).
		BoolParam("include_counter_evidence", "Whether to include contradictory evidence (recall/recall_recent)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var probe struct {
				Op string `json:"op"`
			}
			if err := json.Unmarshal(input, &probe); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			switch strings.TrimSpace(probe.Op) {
			case "resolve_intent":
				return resolveIntent.Handler(ctx, input)
			case "recall":
				return recall.Handler(ctx, input)
			case "recall_recent", "":
				return recallRecent.Handler(ctx, input)
			case "predict_next":
				return predictNext.Handler(ctx, input)
			default:
				return nil, fmt.Errorf("unknown forest op: %q (expected resolve_intent|recall|recall_recent|predict_next)", probe.Op)
			}
		}).
		Build()
}

func NewForestRetrieveEvidenceSkill(deps *RetrievalDependencies) *skills.Skill {
	return skills.NewSkill("forest_retrieve_evidence").
		Description("Retrieve evidence-backed ForestPackets with node, cluster, artifact, validation, cursor, risk, and counter-evidence refs.").
		Domain(RetrievalDomain).
		Keywords("forest", "evidence", "cursor", "validation", "artifact").
		Priority(110).
		StringParam("query", "Natural language evidence query", true).
		StringParam("session_id", "Optional session identifier", false).
		StringParam("task_id", "Optional task identifier", false).
		StringParam("intent_id", "Optional intent identifier", false).
		IntParam("limit", "Maximum packets to return", false).
		BoolParam("include_counter_evidence", "Include quarantined or contradictory evidence", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return executeForestEvidenceQuery(ctx, input, deps, false)
		}).
		Build()
}

func NewForestSuggestValidationsSkill(deps *RetrievalDependencies) *skills.Skill {
	return skills.NewSkill("forest_suggest_validations").
		Description("Suggest validation needs from ForestPackets, quarantine state, and missing validation evidence.").
		Domain(RetrievalDomain).
		Keywords("forest", "validation", "suggest", "evidence").
		Priority(105).
		StringParam("query", "Claim, artifact, or node area needing validation", true).
		StringParam("session_id", "Optional session identifier", false).
		StringParam("task_id", "Optional task identifier", false).
		IntParam("limit", "Maximum packets to inspect", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			output, err := executeForestEvidenceQuery(ctx, input, deps, true)
			if err != nil {
				return nil, err
			}
			return validationSuggestionsFromPackets(output), nil
		}).
		Build()
}

func NewForestProposeClaimSkill(deps *RetrievalDependencies) *skills.Skill {
	return skills.NewSkill("forest_propose_claim").
		Description("Create a proposal-only forest claim artifact with evidence refs; cannot install skills or alter permissions.").
		Domain(RetrievalDomain).
		Keywords("forest", "claim", "proposal", "remediation").
		Priority(95).
		StringParam("summary", "Proposal summary", true).
		StringParam("cluster_id", "Optional source cluster", false).
		StringParam("dimension", "Optional outbreak or risk dimension", false).
		ArrayParam("evidence_refs", "Evidence refs supporting the proposal", "string", false).
		ArrayParam("counter_evidence_refs", "Counter-evidence refs", "string", false).
		BoolParam("guardian_review_required", "Require Guardian review", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			if deps == nil || deps.Forest == nil {
				return nil, fmt.Errorf("forest is not configured")
			}
			var params ForestProposalInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid input: %w", err)
			}
			if err := rejectPrivilegedForestProposal(params.Summary); err != nil {
				return nil, err
			}
			proposal := forest.ForestClaimProposal{
				ClusterID:              strings.TrimSpace(params.ClusterID),
				Dimension:              strings.TrimSpace(params.Dimension),
				Summary:                strings.TrimSpace(params.Summary),
				EvidenceRefs:           params.EvidenceRefs,
				CounterEvidenceRefs:    params.CounterEvidenceRefs,
				GuardianReviewRequired: params.GuardianReviewRequired,
			}
			proposal.ID = "forest_claim_proposal:" + stableSkillID(proposal.Summary, proposal.ClusterID, proposal.Dimension)
			if err := deps.Forest.ProposeForestClaim(ctx, proposal); err != nil {
				return nil, err
			}
			return ForestProposalOutput{Proposed: true, Proposal: proposal}, nil
		}).
		Build()
}

func NewForestRecordOutcomeNamedSkill(name string, deps *RetrievalDependencies) *skills.Skill {
	return newForestRecordOutcomeSkill(name, deps)
}

func NewForestReviewSkill(name, description string, deps *RetrievalDependencies) *skills.Skill {
	return skills.NewSkill(name).
		Description(description).
		Domain(RetrievalDomain).
		Keywords("forest", "review", "evidence", "risk").
		Priority(100).
		StringParam("query", "Review query", true).
		StringParam("session_id", "Optional session identifier", false).
		StringParam("task_id", "Optional task identifier", false).
		IntParam("limit", "Maximum packets to inspect", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return executeForestEvidenceQuery(ctx, input, deps, true)
		}).
		Build()
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
		StringParam("task_id", "Optional task identifier for task-scoped intent resolution", false).
		StringParam("intent_id", "Optional explicit intent identifier", false).
		EnumParam("horizon", "Optional canopy horizon: turn, task, session, user, or project.", []string{
			string(forest.CanopyHorizonTurn),
			string(forest.CanopyHorizonTask),
			string(forest.CanopyHorizonSession),
			string(forest.CanopyHorizonUser),
			string(forest.CanopyHorizonProject),
		}, false).
		IntParam("limit", "Maximum number of supporting packets to inspect", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			if deps == nil || deps.Forest == nil {
				return nil, fmt.Errorf("forest is not configured")
			}
			var params forest.ResolveIntentInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid input: %w", err)
			}
			params.SessionID, params.TaskID = resolveForestSkillScope(ctx, params.SessionID, params.TaskID)
			horizon, err := resolveForestSkillHorizon(string(params.Horizon), params.SessionID, params.TaskID)
			if err != nil {
				return nil, err
			}
			params.Horizon = horizon
			return deps.Forest.ResolveIntent(ctx, params)
		}).
		Build()
}

// NewForestRecallSkill creates the forest_recall skill.
func NewForestRecallSkill(deps *RetrievalDependencies) *skills.Skill {
	return skills.NewSkill("forest_recall").
		Description("Retrieve evidence-backed ForestPackets from the Memory Forest, including support, conflicts, cursor context, and validation needs.").
		Domain(RetrievalDomain).
		Keywords("recall", "precedent", "node", "history", "forest").
		Priority(100).
		StringParam("query", "Natural language query for forest packet recall", true).
		StringParam("session_id", "Optional session identifier", false).
		StringParam("task_id", "Optional task identifier for task-scoped recall", false).
		StringParam("intent_id", "Optional explicit intent identifier", false).
		EnumParam("horizon", "Optional canopy horizon: turn, task, session, user, or project.", []string{
			string(forest.CanopyHorizonTurn),
			string(forest.CanopyHorizonTask),
			string(forest.CanopyHorizonSession),
			string(forest.CanopyHorizonUser),
			string(forest.CanopyHorizonProject),
		}, false).
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
			sessionID, taskID := resolveForestSkillScope(ctx, params.SessionID, params.TaskID)
			horizon, err := resolveForestSkillHorizon(params.Horizon, sessionID, taskID)
			if err != nil {
				return nil, err
			}
			query := forest.Query{
				Query:                  params.Query,
				SessionID:              sessionID,
				TaskID:                 taskID,
				IntentID:               params.IntentID,
				Horizon:                horizon,
				Limit:                  params.Limit,
				IncludeCounterEvidence: params.IncludeCounterEvidence,
			}
			packets, err := deps.Forest.RetrieveForest(ctx, query)
			if err != nil {
				return nil, err
			}
			cursor, err := deps.Forest.CreateForestCursor(ctx, forest.ForestCursorInput{SessionID: sessionID, TaskID: taskID, Packets: packets, Limit: params.Limit})
			if err != nil {
				return nil, err
			}
			return &ForestRecallOutput{Packets: packets, Cursor: cursor}, nil
		}).
		Build()
}

// NewRecallRecentSkill creates the recall_recent skill.
func NewRecallRecentSkill(deps *RetrievalDependencies) *skills.Skill {
	return skills.NewSkill("recall_recent").
		Description("Recover recent preserved session context from the Memory Forest so a terse follow-up can continue naturally from earlier discussion.").
		Domain(RetrievalDomain).
		Keywords("recent", "recall", "continuity", "prior discussion", "memory forest").
		Priority(100).
		StringParam("query", "Optional query to bias recent recall toward a specific topic", false).
		StringParam("session_id", "Optional session identifier", false).
		StringParam("task_id", "Optional task identifier for task-scoped recall", false).
		StringParam("intent_id", "Optional explicit intent identifier", false).
		EnumParam("horizon", "Optional canopy horizon: turn, task, session, user, or project.", []string{
			string(forest.CanopyHorizonTurn),
			string(forest.CanopyHorizonTask),
			string(forest.CanopyHorizonSession),
			string(forest.CanopyHorizonUser),
			string(forest.CanopyHorizonProject),
		}, false).
		IntParam("limit", "Maximum number of packets to inspect", false).
		BoolParam("include_counter_evidence", "Whether to include contradictory evidence", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params RecallRecentInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid input: %w", err)
			}
			return ExecuteRecallRecent(ctx, params, deps)
		}).
		Build()
}

// NewForestPredictNextSkill creates the ForestPacket-native prediction skill.
func NewForestPredictNextSkill(deps *RetrievalDependencies) *skills.Skill {
	return skills.NewSkill("forest_predict_next_packets").
		Description("Retrieve low-risk adjacent ForestPackets that could safely improve the current work beyond literal prompt compliance.").
		Domain(RetrievalDomain).
		Keywords("predict", "next", "opportunity", "adjacent value", "forest").
		Priority(95).
		StringParam("query", "Natural language description of the current task", true).
		StringParam("session_id", "Optional session identifier", false).
		StringParam("task_id", "Optional task identifier for task-scoped prediction", false).
		StringParam("intent_id", "Optional explicit intent identifier", false).
		EnumParam("horizon", "Optional canopy horizon: turn, task, session, user, or project.", []string{
			string(forest.CanopyHorizonTurn),
			string(forest.CanopyHorizonTask),
			string(forest.CanopyHorizonSession),
			string(forest.CanopyHorizonUser),
			string(forest.CanopyHorizonProject),
		}, false).
		IntParam("limit", "Maximum number of packets to return", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			if deps == nil || deps.Forest == nil {
				return nil, fmt.Errorf("forest is not configured")
			}
			var params ForestRecallInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid input: %w", err)
			}
			sessionID, taskID := resolveForestSkillScope(ctx, params.SessionID, params.TaskID)
			horizon, err := resolveForestSkillHorizon(params.Horizon, sessionID, taskID)
			if err != nil {
				return nil, err
			}
			packets, err := deps.Forest.RetrieveForest(ctx, forest.Query{
				Query:                  params.Query,
				SessionID:              sessionID,
				TaskID:                 taskID,
				IntentID:               params.IntentID,
				Horizon:                horizon,
				Limit:                  params.Limit,
				IncludeCounterEvidence: true,
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
	return newForestRecordOutcomeSkill("forest_record_outcome", deps)
}

func newForestRecordOutcomeSkill(name string, deps *RetrievalDependencies) *skills.Skill {
	return skills.NewSkill(name).
		Description("Record explicit outcome feedback for a forest node so the forest can reconsolidate and learn from the result.").
		Domain(RetrievalDomain).
		Keywords("outcome", "feedback", "learn", "reconsolidate", "forest").
		Priority(90).
		StringParam("node_id", "Forest node identifier to update", true).
		StringParam("cursor_id", "Optional forest cursor that surfaced the node", false).
		StringParam("session_id", "Optional session identifier", false).
		StringParam("task_id", "Optional task identifier", false).
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
			sessionID, taskID := resolveForestSkillScope(ctx, params.SessionID, params.TaskID)
			if err := deps.Forest.RecordOutcome(ctx, forest.OutcomeRecord{
				NodeID:     params.NodeID,
				CursorID:   params.CursorID,
				SessionID:  sessionID,
				TaskID:     taskID,
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

func executeForestEvidenceQuery(ctx context.Context, input json.RawMessage, deps *RetrievalDependencies, includeCounter bool) (*ForestRecallOutput, error) {
	if deps == nil || deps.Forest == nil {
		return nil, fmt.Errorf("forest is not configured")
	}
	var params ForestRecallInput
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	sessionID, taskID := resolveForestSkillScope(ctx, params.SessionID, params.TaskID)
	horizon, err := resolveForestSkillHorizon(params.Horizon, sessionID, taskID)
	if err != nil {
		return nil, err
	}
	packets, err := deps.Forest.RetrieveForest(ctx, forest.Query{
		Query:                  params.Query,
		SessionID:              sessionID,
		TaskID:                 taskID,
		IntentID:               params.IntentID,
		Horizon:                horizon,
		Limit:                  params.Limit,
		IncludeCounterEvidence: includeCounter || params.IncludeCounterEvidence,
	})
	if err != nil {
		return nil, err
	}
	cursor, err := deps.Forest.CreateForestCursor(ctx, forest.ForestCursorInput{SessionID: sessionID, TaskID: taskID, Packets: packets, Limit: params.Limit})
	if err != nil {
		return nil, err
	}
	return &ForestRecallOutput{Packets: packets, Cursor: cursor}, nil
}

func validationSuggestionsFromPackets(output *ForestRecallOutput) *ForestValidationSuggestionOutput {
	if output == nil {
		return &ForestValidationSuggestionOutput{}
	}
	var needs []string
	var proposals []forest.ForestClaimProposalTemplate
	for _, packet := range output.Packets {
		if packet == nil {
			continue
		}
		if packet.ValidationNeed > 0 {
			needs = append(needs, fmt.Sprintf("%s:%.3f", packet.Node.ID, packet.ValidationNeed))
		}
		proposals = append(proposals, packet.ProposedClaims...)
	}
	return &ForestValidationSuggestionOutput{
		Packets:         output.Packets,
		Cursor:          output.Cursor,
		ValidationNeeds: dedupeSkillStrings(needs),
		ProposedClaims:  proposals,
	}
}

func rejectPrivilegedForestProposal(summary string) error {
	normalized := strings.ToLower(summary)
	for _, blocked := range []string{
		"install skill",
		"generated skill",
		"enable skill",
		"alter permission",
		"change permission",
		"grant permission",
		"escalate permission",
		"bypass permission",
	} {
		if strings.Contains(normalized, blocked) {
			return fmt.Errorf("forest proposal rejected: generated skills and permission changes require governance outside forest skills")
		}
	}
	return nil
}

func stableSkillID(values ...string) string {
	hash := sha256.New()
	for _, value := range values {
		hash.Write([]byte(strings.TrimSpace(value)))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))[:16]
}

func dedupeSkillStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

// ExecuteRecallRecent performs the continuity-oriented retrieval behind the
// recall_recent skill so agents can reuse it programmatically.
func ExecuteRecallRecent(
	ctx context.Context,
	params RecallRecentInput,
	deps *RetrievalDependencies,
) (*RecallRecentOutput, error) {
	if deps == nil || deps.Forest == nil {
		return nil, fmt.Errorf("forest is not configured")
	}
	sessionID, taskID := resolveForestSkillScope(ctx, params.SessionID, params.TaskID)
	horizon, err := resolveForestSkillHorizon(params.Horizon, sessionID, taskID)
	if err != nil {
		return nil, err
	}
	limit := params.Limit
	if limit <= 0 {
		limit = 6
	}
	includeCounterEvidence := true
	if params.IncludeCounterEvidence != nil {
		includeCounterEvidence = *params.IncludeCounterEvidence
	}
	queryText := strings.TrimSpace(params.Query)
	packets, err := deps.Forest.RetrieveForest(ctx, forest.Query{
		Query:                  queryText,
		SessionID:              sessionID,
		TaskID:                 taskID,
		IntentID:               strings.TrimSpace(params.IntentID),
		Horizon:                horizon,
		Limit:                  limit,
		IncludeCounterEvidence: includeCounterEvidence,
	})
	if err != nil {
		return nil, err
	}
	cursor, err := deps.Forest.CreateForestCursor(ctx, forest.ForestCursorInput{SessionID: sessionID, TaskID: taskID, Packets: packets, Limit: limit})
	if err != nil {
		return nil, err
	}
	var intent *forest.IntentResolution
	if queryText != "" {
		intent, _ = deps.Forest.ResolveIntent(ctx, forest.ResolveIntentInput{
			Query:     queryText,
			SessionID: sessionID,
			TaskID:    taskID,
			IntentID:  strings.TrimSpace(params.IntentID),
			Horizon:   horizon,
			Limit:     maxForestSkillLimit(limit, 4),
		})
	}
	summary, focus := summarizeRecentRecall(queryText, intent, packets)
	return &RecallRecentOutput{
		Summary: summary,
		Focus:   focus,
		Intent:  intent,
		Packets: packets,
		Cursor:  cursor,
	}, nil
}

func resolveForestSkillScope(ctx context.Context, sessionID, taskID string) (string, string) {
	sessionID = strings.TrimSpace(sessionID)
	taskID = strings.TrimSpace(taskID)
	if sessionID == "" {
		sessionID = versioning.SessionIDFromContext(ctx)
	}
	if taskID == "" {
		taskID = versioning.TaskIDFromContext(ctx)
	}
	return sessionID, taskID
}

func resolveForestSkillHorizon(raw, sessionID, taskID string) (forest.CanopyHorizon, error) {
	switch normalized := strings.ToLower(strings.TrimSpace(raw)); normalized {
	case "":
		switch {
		case taskID != "":
			return forest.CanopyHorizonTask, nil
		case sessionID != "":
			return forest.CanopyHorizonSession, nil
		default:
			return forest.CanopyHorizonProject, nil
		}
	case string(forest.CanopyHorizonTurn):
		return forest.CanopyHorizonTurn, nil
	case string(forest.CanopyHorizonTask):
		return forest.CanopyHorizonTask, nil
	case string(forest.CanopyHorizonSession):
		return forest.CanopyHorizonSession, nil
	case string(forest.CanopyHorizonUser):
		return forest.CanopyHorizonUser, nil
	case string(forest.CanopyHorizonProject):
		return forest.CanopyHorizonProject, nil
	default:
		return "", fmt.Errorf("invalid horizon %q", raw)
	}
}

func summarizeRecentRecall(
	query string,
	intent *forest.IntentResolution,
	packets []*forest.ForestPacket,
) (string, []string) {
	focus := collectRecentRecallFocus(packets)
	primaryIntent := ""
	if intent != nil {
		primaryIntent = strings.TrimSpace(intent.PrimaryIntent)
	}
	if primaryIntent == "" {
		for _, packet := range packets {
			if packet == nil || packet.Node.Kind != forest.ForestNodeClaim {
				continue
			}
			primaryIntent = strings.TrimSpace(packet.Node.Summary)
			if primaryIntent == "" {
				primaryIntent = strings.TrimSpace(packet.Node.Title)
			}
			if primaryIntent != "" {
				break
			}
		}
	}
	if primaryIntent == "" && len(focus) == 0 {
		if strings.TrimSpace(query) == "" {
			return "No recent preserved context found for this session.", nil
		}
		return "No recent preserved context found for that topic in this session.", nil
	}
	var summary strings.Builder
	if primaryIntent != "" {
		summary.WriteString("Recovered recent preserved context. Prior intent: ")
		summary.WriteString(trimRecallSentence(primaryIntent))
		summary.WriteString(".")
	} else {
		summary.WriteString("Recovered recent preserved context from session memory.")
	}
	if len(focus) > 0 {
		highlights := focus
		if len(highlights) > 3 {
			highlights = highlights[:3]
		}
		summary.WriteString(" Key points: ")
		summary.WriteString(strings.Join(highlights, "; "))
		summary.WriteString(".")
	}
	return summary.String(), focus
}

func collectRecentRecallFocus(packets []*forest.ForestPacket) []string {
	focus := make([]string, 0, len(packets))
	seen := make(map[string]struct{}, len(packets))
	for _, packet := range packets {
		item := formatRecentRecallFocus(packet)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		focus = append(focus, item)
		if len(focus) >= 5 {
			break
		}
	}
	return focus
}

func formatRecentRecallFocus(packet *forest.ForestPacket) string {
	if packet == nil {
		return ""
	}
	summary := strings.TrimSpace(packet.Node.Summary)
	if summary == "" {
		summary = strings.TrimSpace(packet.Node.Title)
	}
	if summary == "" {
		return ""
	}
	label := recallNodeLabel(packet.Node.Kind)
	if label == "" {
		return trimRecallSentence(summary)
	}
	return label + ": " + trimRecallSentence(summary)
}

func recallNodeLabel(kind forest.ForestNodeKind) string {
	switch kind {
	case forest.ForestNodeClaim, forest.ForestNodeInteraction, forest.ForestNodePolicyTrial, forest.ForestNodeSkillCandidate:
		return "Intent"
	case forest.ForestNodeValidation:
		return "Constraint"
	case forest.ForestNodeArtifact, forest.ForestNodeTestament, forest.ForestNodeContent:
		return "Evidence"
	case forest.ForestNodeOutcome, forest.ForestNodeRemediation:
		return "Outcome"
	case forest.ForestNodeContradiction:
		return "AntiPattern"
	default:
		return ""
	}
}

func trimRecallSentence(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimRight(value, ".!?:;")
	return strings.TrimSpace(value)
}

func maxForestSkillLimit(value, fallback int) int {
	if value > fallback {
		return value
	}
	return fallback
}
