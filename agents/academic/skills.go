package academic

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/google/uuid"
)

func (a *Academic) registerCoreSkills() {
	a.skills.Register(researchTopicSkill(a))
	a.skills.Register(findBestPracticesSkill(a))
	a.skills.Register(compareApproachesSkill(a))
}

func (a *Academic) registerExtendedSkills() {
	a.skills.Register(recommendSolutionSkill(a))
	a.skills.Register(validateApproachSkill(a))
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

func (a *Academic) compareApproaches(ctx context.Context, topic string, approaches []string, criteria []string) (*ApproachComparison, error) {
	comparison := &ApproachComparison{
		Topic:      topic,
		Approaches: make([]Approach, 0, len(approaches)),
	}

	libCtx, _ := a.consultLibrarian(ctx, &ResearchQuery{
		Query: fmt.Sprintf("compare approaches for %s: %v", topic, approaches),
	})

	for i, approachName := range approaches {
		query := &ResearchQuery{
			Query:  fmt.Sprintf("%s for %s", approachName, topic),
			Intent: IntentRecall,
		}

		result, err := a.executeResearch(ctx, query, libCtx, nil)
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
			Pros:        extractPros(result.Findings),
			Cons:        extractCons(result.Findings),
			SourceIDs:   result.SourcesConsulted,
		}

		if len(criteria) > 0 {
			approach.UseCases = matchCriteria(result.Findings, criteria)
		}

		comparison.Approaches = append(comparison.Approaches, approach)
	}

	comparison.Summary = generateComparisonSummary(comparison.Approaches)
	comparison.RecommendedApproach, comparison.Rationale = selectRecommendedApproach(comparison.Approaches, libCtx)

	return comparison, nil
}

func summarizeFindings(findings []Finding) string {
	if len(findings) == 0 {
		return "No findings available"
	}
	return findings[0].Summary
}

func extractPros(findings []Finding) []string {
	var pros []string
	for _, f := range findings {
		if f.Confidence == ConfidenceLevelHigh {
			pros = append(pros, f.Summary)
		}
	}
	return pros
}

func extractCons(findings []Finding) []string {
	var cons []string
	for _, f := range findings {
		if f.Confidence == ConfidenceLevelLow {
			cons = append(cons, f.Summary)
		}
	}
	return cons
}

func matchCriteria(findings []Finding, criteria []string) []string {
	var matched []string
	for _, criterion := range criteria {
		for _, f := range findings {
			if containsSubstring(toLower(f.Summary), toLower(criterion)) {
				matched = append(matched, criterion)
				break
			}
		}
	}
	return matched
}

func generateComparisonSummary(approaches []Approach) string {
	if len(approaches) == 0 {
		return "No approaches to compare"
	}
	return fmt.Sprintf("Compared %d approaches", len(approaches))
}

func selectRecommendedApproach(approaches []Approach, libCtx *LibrarianContext) (string, string) {
	if len(approaches) == 0 {
		return "", "No approaches available"
	}

	best := approaches[0]
	for _, a := range approaches[1:] {
		if len(a.Pros) > len(best.Pros) && len(a.Cons) <= len(best.Cons) {
			best = a
		}
	}

	rationale := fmt.Sprintf("Selected %s based on having the most advantages", best.Name)
	if libCtx != nil && len(libCtx.ExistingPatterns) > 0 {
		rationale += " and compatibility with existing codebase patterns"
	}

	return best.ID, rationale
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

	libCtx, err := a.consultLibrarian(ctx, query)
	if err != nil && a.config.RequireLibrarian {
		return nil, fmt.Errorf("librarian consultation required but failed: %w", err)
	}

	pastOutcomes := a.outcomeHistory.GetSimilar(problem, 5)

	result, err := a.executeResearch(ctx, query, libCtx, pastOutcomes)
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
	}

	recommendation.LibrarianValidated = libCtx != nil && libCtx.CodebaseMaturity != "unknown"

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

func (a *Academic) validateApproach(ctx context.Context, approach string, filesAffected []string, checkConflicts bool) (*ValidationResult, error) {
	query := &ResearchQuery{
		Query:  approach,
		Intent: IntentCheck,
	}

	libCtx, err := a.consultLibrarian(ctx, query)
	if err != nil {
		return &ValidationResult{
			Valid:  false,
			Reason: "Could not consult Librarian for validation",
			Error:  err.Error(),
		}, nil
	}

	result := &ValidationResult{
		Approach:      approach,
		FilesAffected: filesAffected,
		ValidatedAt:   time.Now(),
	}

	if libCtx == nil {
		result.Valid = false
		result.Reason = "No codebase context available"
		return result, nil
	}

	result.CodebaseMaturity = libCtx.CodebaseMaturity
	result.ExistingPatterns = libCtx.ExistingPatterns

	if checkConflicts && len(libCtx.ConflictingApproaches) > 0 {
		result.Valid = false
		result.Conflicts = libCtx.ConflictingApproaches
		result.Reason = "Conflicts with existing approaches"
		result.Applicability = "INCOMPATIBLE"
		return result, nil
	}

	result.Valid = true
	result.Reason = "Approach is compatible with codebase"

	if len(libCtx.ExistingPatterns) > 0 {
		result.Applicability = "ADAPTABLE"
		result.Reason = "Approach can be adapted to existing patterns"
	} else {
		result.Applicability = "DIRECT"
	}

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

func (a *Academic) Skills() *skills.Registry {
	return a.skills
}

func (a *Academic) SendToLibrarian(ctx context.Context, message string) error {
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
