package academic

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/google/uuid"
)

func (a *Academic) registerCoreSkills() {
	a.skills.Register(researchTopicSkill(a))
	a.skills.Register(findBestPracticesSkill(a))
	a.skills.Register(compareApproachesSkill(a))
	a.skills.Register(consultSkill(a))
	a.skills.Register(versioning.NewReadWorkspaceFileSkill(func() versioning.WorkspaceViewAccess { return a.workspaceViews }, nil))
	a.skills.Register(versioning.NewWorkspaceGlobSkill(func() versioning.WorkspaceViewAccess { return a.workspaceViews }, nil))
	a.skills.Register(versioning.NewWorkspaceGrepSkill(func() versioning.WorkspaceViewAccess { return a.workspaceViews }, nil))
	a.skills.Register(versioning.NewInspectWorkspaceStateSkill(func() versioning.WorkspaceViewAccess { return a.workspaceViews }, nil))
	a.skills.Register(versioning.NewSummarizeWorkspaceStateSkill(func() versioning.WorkspaceViewAccess { return a.workspaceViews }, nil))
	a.skills.Register(shared.NewSelfDiagnosticSkill(&academicDiag{a: a}))
	a.skills.Register(skills.NewRerouteSkill(skills.RerouteConfig{
		AgentID:   "academic",
		SessionID: func() string { return a.config.SessionID },
		Publish:   a.publishRerouteRequest,
	}))
}

type academicDiag struct{ a *Academic }

func (d *academicDiag) AgentName() string { return "academic" }
func (d *academicDiag) SessionID() string { return d.a.config.SessionID }
func (d *academicDiag) LogsDir() string {
	return shared.LogsDirForAgent(d.a.steering.SessionDir(), "academic")
}
func (d *academicDiag) EventLogger() *agentlog.SessionEventLogger { return d.a.steering.EventLogger() }
func (d *academicDiag) PeerLogsDirs() map[string]string           { return nil }
func (d *academicDiag) RecoveryHints() []string                   { return nil }

func (d *academicDiag) AgentSpecificDiagnostics() map[string]any {
	d.a.requestMu.Lock()
	inFlight := len(d.a.requestCancels)
	d.a.requestMu.Unlock()
	return map[string]any{
		"in_flight_requests": inFlight,
		"cache_size":         len(d.a.researchCache),
		"outcome_history":    d.a.outcomeHistory.Len(),
	}
}

func (a *Academic) publishRerouteRequest(reason, originalInput, suggestedTarget string) error {
	if a.bus == nil {
		return fmt.Errorf("academic bus not available")
	}
	reroute := &guide.RerouteRequest{
		OriginalInput:   originalInput,
		Reason:          reason,
		SourceAgentID:   "academic",
		SuggestedTarget: suggestedTarget,
		ExcludeAgents:   []string{"academic"},
	}
	return a.bus.Publish(guide.TopicGuideRequests, guide.NewRerouteMessage("", reroute))
}

func (a *Academic) registerExtendedSkills() {
	a.skills.Register(authorResearchPaperSkill(a))
	a.skills.Register(recommendSolutionSkill(a))
	a.skills.Register(validateApproachSkill(a))
	a.skills.Register(cloneViaLibrarianSkill(a))
}

// cloneViaLibrarianSkill lets the academic trigger a git clone through the
// librarian. The cloned repository becomes searchable in the librarian's
// package store, enabling follow-up queries against the cloned code.
func cloneViaLibrarianSkill(a *Academic) *skills.Skill {
	return skills.NewSkill("clone_via_librarian").
		Description(
			"Request the Librarian to clone a remote git repository for code analysis. "+
				"The cloned code becomes searchable via the Librarian's search tools. "+
				"Use this when you need to analyze source code from an external package.",
		).
		Domain("research").
		Keywords("clone", "repository", "package", "source code", "git").
		Priority(80).
		TokenEstimate(300).
		StringParam("url", "Repository URL (e.g. github.com/owner/repo)", true).
		StringParam("reason", "Why this repository needs to be cloned", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				URL    string `json:"url"`
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if params.URL == "" {
				return nil, fmt.Errorf("url is required")
			}
			if params.Reason == "" {
				return nil, fmt.Errorf("reason is required")
			}

			cloneQuery := fmt.Sprintf("Clone repository %s for analysis: %s", params.URL, params.Reason)
			evidence, err := a.requestConsultation(ctx, "librarian", cloneQuery, "", a.config.SessionID)
			if err != nil {
				return map[string]any{
					"success": false,
					"url":     params.URL,
					"error":   err.Error(),
				}, nil
			}

			return map[string]any{
				"success": evidence.Success,
				"url":     params.URL,
				"data":    evidence.Data,
				"error":   evidence.Error,
			}, nil
		}).
		Build()
}

// consultTargets enumerates valid consultation targets for the Academic.
var consultTargets = map[string]string{
	"librarian":   "Codebase patterns, existing implementations, and dependency information",
	"archivalist": "Historical context on code decisions and past changes",
}

func consultSkill(a *Academic) *skills.Skill {
	return skills.NewSkill("consult").
		Description("Consult a domain expert agent. Targets: librarian (codebase patterns), archivalist (historical context).").
		Domain("consultation").
		Keywords("consult", "librarian", "archivalist", "codebase", "patterns", "history").
		Priority(85).
		EnumParam("target", "Agent to consult", []string{"librarian", "archivalist"}, true).
		StringParam("query", "Consultation question", true).
		StringParam("scope", "Scope for consultation", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Target string `json:"target"`
				Query  string `json:"query"`
				Scope  string `json:"scope"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if _, ok := consultTargets[params.Target]; !ok {
				return nil, fmt.Errorf("invalid target %q: must be librarian or archivalist", params.Target)
			}
			if params.Query == "" {
				return nil, fmt.Errorf("query is required")
			}
			evidence, err := a.requestConsultation(ctx, params.Target, params.Query, params.Scope, a.config.SessionID)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"target":  params.Target,
				"success": evidence.Success,
				"data":    evidence.Data,
			}, nil
		}).
		Build()
}

type researchTopicParams struct {
	Topic   string `json:"topic"`
	Context string `json:"context"`
	Depth   string `json:"depth"`
}

func researchTopicSkill(a *Academic) *skills.Skill {
	return skills.NewSkill("research_topic").
		Description("Research a technical topic comprehensively, consulting Librarian for codebase context.").
		Domain("research").
		Keywords("research", "investigate", "study", "learn").
		Priority(100).
		StringParam("topic", "The technical topic to research", true).
		StringParam("context", "Additional context for the research", false).
		EnumParam("depth", "Research depth level", []string{"quick", "standard", "comprehensive"}, false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params researchTopicParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			query := &ResearchQuery{
				Query:  params.Topic,
				Intent: IntentRecall,
			}

			if params.Context != "" {
				query.Query = fmt.Sprintf("%s (context: %s)", params.Topic, params.Context)
			}

			return a.Research(ctx, query)
		}).
		Build()
}

type findBestPracticesParams struct {
	Technology string `json:"technology"`
	Domain     string `json:"domain"`
	Language   string `json:"language"`
}

func findBestPracticesSkill(a *Academic) *skills.Skill {
	return skills.NewSkill("find_best_practices").
		Description("Find established best practices for a technology, validated against codebase patterns.").
		Domain("research").
		Keywords("best practice", "convention", "standard", "guideline").
		Priority(90).
		StringParam("technology", "The technology to find best practices for", true).
		StringParam("domain", "Specific domain within the technology", false).
		StringParam("language", "Programming language context", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params findBestPracticesParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			queryText := fmt.Sprintf("best practices for %s", params.Technology)
			if params.Domain != "" {
				queryText = fmt.Sprintf("%s %s best practices", params.Technology, params.Domain)
			}

			query := &ResearchQuery{
				Query:          queryText,
				Intent:         IntentRecall,
				Domain:         DomainPatterns,
				LanguageFilter: params.Language,
			}

			return a.Research(ctx, query)
		}).
		Build()
}

type compareApproachesParams struct {
	Topic      string   `json:"topic"`
	Approaches []string `json:"approaches"`
	Criteria   []string `json:"criteria"`
}

func compareApproachesSkill(a *Academic) *skills.Skill {
	return skills.NewSkill("compare_approaches").
		Description("Compare different technical approaches with applicability analysis for the codebase.").
		Domain("research").
		Keywords("compare", "versus", "vs", "alternative", "option").
		Priority(85).
		StringParam("topic", "The topic being compared", true).
		ArrayParam("approaches", "List of approaches to compare", "string", true).
		ArrayParam("criteria", "Criteria for comparison", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params compareApproachesParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if len(params.Approaches) < 2 {
				return nil, fmt.Errorf("at least 2 approaches required for comparison")
			}

			return a.compareApproaches(ctx, params.Topic, params.Approaches, params.Criteria)
		}).
		Build()
}

func (a *Academic) compareApproaches(ctx context.Context, topic string, approaches []string, _ []string) (*ApproachComparison, error) {
	comparison := &ApproachComparison{
		Topic:      topic,
		Approaches: make([]Approach, 0, len(approaches)),
	}

	for i, approachName := range approaches {
		query := &ResearchQuery{
			Query:  fmt.Sprintf("%s for %s", approachName, topic),
			Intent: IntentRecall,
		}

		result, err := a.Research(ctx, query)
		if err != nil {
			a.logger.Warn("failed to research approach",
				"approach", approachName,
				"error", err,
			)
			continue
		}

		approach := Approach{
			ID:          fmt.Sprintf("approach_%d", i),
			Name:        approachName,
			Description: summarizeFindings(result.Findings),
			SourceIDs:   result.SourcesConsulted,
		}

		comparison.Approaches = append(comparison.Approaches, approach)
	}

	comparison.Summary = generateComparisonSummary(comparison.Approaches)

	return comparison, nil
}

func summarizeFindings(findings []Finding) string {
	if len(findings) == 0 {
		return "No findings available"
	}
	return findings[0].Summary
}

func generateComparisonSummary(approaches []Approach) string {
	if len(approaches) == 0 {
		return "No approaches to compare"
	}
	return fmt.Sprintf("Compared %d approaches", len(approaches))
}

type recommendSolutionParams struct {
	Problem          string   `json:"problem"`
	Constraints      []string `json:"constraints"`
	RequireLibrarian bool     `json:"require_librarian"`
}

func recommendSolutionSkill(a *Academic) *skills.Skill {
	return skills.NewSkill("recommend_solution").
		Description("Recommend a solution with full applicability analysis. ALWAYS consults Librarian.").
		Domain("research").
		Keywords("recommend", "suggest", "solution", "solve").
		Priority(95).
		StringParam("problem", "The problem to solve", true).
		ArrayParam("constraints", "Constraints to consider", "string", false).
		BoolParam("require_librarian", "Require Librarian validation (default: true)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params recommendSolutionParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			return a.recommendSolution(ctx, params.Problem, params.Constraints)
		}).
		Build()
}

func (a *Academic) recommendSolution(ctx context.Context, problem string, constraints []string) (*SolutionRecommendation, error) {
	query := &ResearchQuery{
		Query:  problem,
		Intent: IntentRecall,
		Domain: DomainDecisions,
	}

	result, err := a.Research(ctx, query)
	if err != nil {
		return nil, err
	}

	recommendation := &SolutionRecommendation{
		ID:          uuid.New().String(),
		Problem:     problem,
		Constraints: constraints,
		CreatedAt:   time.Now(),
	}

	if len(result.Recommendations) > 0 {
		rec := result.Recommendations[0]
		recommendation.Solution = rec.Description
		recommendation.Rationale = rec.Rationale
		recommendation.Applicability = rec.Applicability
		recommendation.Confidence = rec.Confidence
		recommendation.SourceIDs = rec.SourceIDs
	} else if len(result.Findings) > 0 {
		recommendation.Solution = result.Findings[0].Summary
		recommendation.Confidence = result.Findings[0].Confidence
	}

	pastOutcomes := a.outcomeHistory.GetSimilar(problem, 5)
	if len(pastOutcomes) > 0 {
		recommendation.PastOutcomes = &PastOutcomesSummary{
			Total:       len(pastOutcomes),
			SuccessRate: calculateSuccessRate(pastOutcomes),
		}
	}

	return recommendation, nil
}

func calculateSuccessRate(outcomes []*OutcomeRecord) float64 {
	if len(outcomes) == 0 {
		return 0
	}
	successCount := 0
	for _, o := range outcomes {
		if o.Success {
			successCount++
		}
	}
	return float64(successCount) / float64(len(outcomes))
}

type SolutionRecommendation struct {
	ID                 string               `json:"id"`
	Problem            string               `json:"problem"`
	Solution           string               `json:"solution"`
	Rationale          string               `json:"rationale"`
	Applicability      string               `json:"applicability"`
	Confidence         ConfidenceLevel      `json:"confidence"`
	Constraints        []string             `json:"constraints,omitempty"`
	SourceIDs          []string             `json:"source_ids,omitempty"`
	LibrarianValidated bool                 `json:"librarian_validated"`
	PastOutcomes       *PastOutcomesSummary `json:"past_outcomes,omitempty"`
	CreatedAt          time.Time            `json:"created_at"`
}

type PastOutcomesSummary struct {
	Total       int     `json:"total"`
	SuccessRate float64 `json:"success_rate"`
}

type validateApproachParams struct {
	Approach       string   `json:"approach"`
	FilesAffected  []string `json:"files_affected"`
	CheckConflicts bool     `json:"check_conflicts"`
}

func validateApproachSkill(a *Academic) *skills.Skill {
	return skills.NewSkill("validate_approach").
		Description("Validate an approach against the codebase via Librarian consultation.").
		Domain("research").
		Keywords("validate", "verify", "check", "compatible").
		Priority(80).
		StringParam("approach", "The approach to validate", true).
		ArrayParam("files_affected", "Files that would be affected", "string", false).
		BoolParam("check_conflicts", "Check for conflicts with existing patterns", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params validateApproachParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			return a.validateApproach(ctx, params.Approach, params.FilesAffected, params.CheckConflicts)
		}).
		Build()
}

func (a *Academic) validateApproach(ctx context.Context, approach string, filesAffected []string, _ bool) (*ValidationResult, error) {
	result := &ValidationResult{
		Approach:      approach,
		FilesAffected: filesAffected,
		ValidatedAt:   time.Now(),
	}

	// Consult Librarian for codebase context
	evidence, err := a.requestConsultation(ctx, "librarian",
		fmt.Sprintf("Validate approach compatibility: %s", approach),
		"", a.config.SessionID)
	if err != nil {
		result.Valid = false
		result.Reason = "Could not consult Librarian for validation"
		result.Error = err.Error()
		return result, nil
	}

	if !evidence.Success {
		result.Valid = false
		result.Reason = "Librarian consultation unsuccessful"
		if evidence.Error != "" {
			result.Error = evidence.Error
		}
		return result, nil
	}

	result.Valid = true
	result.Reason = "Approach validated via Librarian consultation"
	result.Applicability = "ADAPTABLE"

	return result, nil
}

type ValidationResult struct {
	Approach         string    `json:"approach"`
	Valid            bool      `json:"valid"`
	Reason           string    `json:"reason"`
	Error            string    `json:"error,omitempty"`
	Applicability    string    `json:"applicability,omitempty"`
	FilesAffected    []string  `json:"files_affected,omitempty"`
	Conflicts        []string  `json:"conflicts,omitempty"`
	ExistingPatterns []string  `json:"existing_patterns,omitempty"`
	CodebaseMaturity string    `json:"codebase_maturity,omitempty"`
	ValidatedAt      time.Time `json:"validated_at"`
}

func (a *Academic) SendToLibrarian(_ context.Context, message string) error {
	if !a.running {
		return fmt.Errorf("academic is not running")
	}

	req := &guide.RouteRequest{
		CorrelationID: uuid.New().String(),
		Input:         message,
		SourceAgentID: "academic",
		TargetAgentID: "librarian",
		FireAndForget: true,
		Timestamp:     time.Now(),
	}

	return a.PublishRequest(req)
}
