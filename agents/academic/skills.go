package academic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/google/uuid"
)

func (a *Academic) registerCoreSkills() {
	a.skills.Register(researchTopicSkill(a))
	a.skills.Register(findBestPracticesSkill(a))
	a.skills.Register(compareApproachesSkill(a))
	a.skills.Register(consultSkill(a))
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
			evidence, err := a.requestConsultation(ctx, "librarian", cloneQuery, "", "")
			if err != nil {
				if errors.Is(err, skills.ErrDelegatedRequested) {
					return nil, err
				}
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

func academicConsultFailureResult(target, query, scope string, err error) map[string]any {
	result := map[string]any{
		"target":   strings.TrimSpace(target),
		"query":    strings.TrimSpace(query),
		"scope":    strings.TrimSpace(scope),
		"success":  false,
		"status":   "failed",
		"guidance": "Continue with direct research or synthesize current evidence instead of repeating the same consultation.",
	}
	if err != nil {
		if message := strings.TrimSpace(err.Error()); message != "" {
			result["error"] = message
		}
	}
	return result
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
			if execState := academicResearchExecutionStateFromContext(ctx); execState != nil {
				if err := execState.recordConsultAttempt(params.Target, params.Query, params.Scope); err != nil {
					academicLogDuplicateConsultationBlocked(ctx, params.Target, params.Query, params.Scope)
					return academicConsultFailureResult(params.Target, params.Query, params.Scope, err), nil
				}
			}
			evidence, err := a.requestConsultation(ctx, params.Target, params.Query, params.Scope, "")
			if err != nil {
				if errors.Is(err, skills.ErrDelegatedRequested) {
					return nil, err
				}
				return academicConsultFailureResult(params.Target, params.Query, params.Scope, err), nil
			}
			result := map[string]any{
				"target":  params.Target,
				"success": evidence.Success,
				"data":    evidence.Data,
			}
			if evidence.Success {
				result["status"] = "ok"
			} else {
				result["status"] = "failed"
			}
			if message := strings.TrimSpace(evidence.Error); message != "" {
				result["error"] = message
			}
			return result, nil
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
		Description("Research a topic comprehensively with explicit decision framing, evidence-class coverage, grounded synthesis, and Librarian validation when codebase fit matters.").
		Domain("research").
		Keywords("research", "investigate", "study", "learn").
		Usage("Use for open-ended technical research that should end in a cited, codebase-aware answer rather than a quick intuition dump.").
		BestPractice("Start by naming the decision frame: objective, governing criteria, relevant evidence classes, likely confounders, and what findings would weaken each major hypothesis.").
		BestPractice("When performance, reliability, cost, scale, adoption, or security claims materially affect the answer, support them with grounded statistics or say explicitly that only qualitative evidence is available.").
		BestPractice("When measurable criteria matter, surface the decision-relevant measurable criteria for this domain and quantify them when credible evidence exists instead of relying on adjectives.").
		BestPractice("When relevant literature, benchmarks, standards, or postmortems exist, digest the strongest ones and summarize method, findings, and limitations instead of only listing citations.").
		BestPractice("Actively look for counterevidence, strongest negative cases, failure modes, conflicting studies, lock-in, adoption barriers, or migration burden rather than only confirming a preferred conclusion.").
		BestPractice("Treat self-authored, vendor-authored, or project-authored sources as capability evidence first. Use them to establish what an option claims to support, not to prove comparative superiority by themselves.").
		BestPractice("Stress-test assumptions, inspect methodology, and call out dataset bias or threats to validity when they materially affect the conclusion.").
		Requirement("For substantial research questions, map the relevant evidence classes for the domain and say which are strong, weak, or unavailable: primary or authoritative sources, empirical or observational evidence, counterevidence or failure cases, methodological or limitations evidence, implementation or operational burden, institutional or ecosystem context, and formal or academic or regulatory evidence.").
		Requirement("For substantial research questions, synthesize evidence into a structured comparison, literature digest, or technical memo rather than a loose source roundup.").
		Requirement("Do your own analysis on top of the sources: validate assumptions, challenge hypotheses, and note where the evidence is weak or biased.").
		Requirement("Do not open a substantial externally sourced synthesis with a naked winner or default pick; make the decision frame and evidence quality legible before or alongside the recommendation.").
		Requirement("Do not emit a Short Answer, shortlist, or compressed winner section before the evidence basis and comparison are established.").
		Requirement("Treat the answer as incomplete if it lacks the relevant structured artifact for the question, such as an evidence table, comparison matrix, methodology digest, risk register, math walkthrough, algorithm sketch, or architecture or flow model.").
		Requirement("Before finalizing, run a rigor audit across all relevant dimensions: per-link grounding, corroboration, counterevidence, math checks, methodology review, dataset quality, bias review, and threats to validity.").
		Requirement("Calibrate confidence to evidence completeness: high confidence requires broad grounded support across the relevant evidence classes, explicit counterevidence review, and disclosed limitations.").
		Requirement("If the relevant comparative evidence remains dominated by self-authored sources when independent validation matters, keep the conclusion provisional or explicitly inconclusive rather than collapsing to a clean recommendation.").
		Avoid("Do not stop at surface positioning, promotional framing, or shallow summaries when deeper evidence is obtainable.").
		Avoid("Do not answer with a winner-plus-alternatives roundup followed by a source dump when the question requires deeper research synthesis.").
		Avoid("Do not use unsupported adjectives like best, strongest, safer, simpler, faster, mature, or standard in place of explicit criteria and evidence.").
		Priority(100).
		StringParam("topic", "The technical topic to research", true).
		StringParam("context", "Additional context for the research", false).
		EnumParam("depth", "Research depth level", shared.ResearchDepthEnumValues(), false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params researchTopicParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			query := &ResearchQuery{
				Query:  params.Topic,
				Intent: IntentRecall,
				Depth:  params.Depth,
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
		Description("Find established best practices for a technology, validated against codebase patterns and backed by verifiable sources.").
		Domain("research").
		Keywords("best practice", "convention", "standard", "guideline").
		BestPractice("Prefer standards, official documentation, peer-reviewed work, and maintainers' guidance over trend pieces or anecdotal blog posts.").
		Requirement("When best-practice advice is contested or high impact, compare the strongest primary sources and call out where they disagree.").
		Avoid("Do not present conventional wisdom as settled fact without identifying the evidence quality behind it.").
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
		Description("Compare approaches with explicit criteria, evidence-class coverage, applicability analysis, and evidence-backed tradeoffs.").
		Domain("research").
		Keywords("compare", "versus", "vs", "alternative", "option").
		BestPractice("Define or infer the governing criteria before ranking options, and make the weighting or priority legible.").
		BestPractice("When the ranking hinges on performance, reliability, cost, scale, or adoption, support the comparison with grounded statistics or explain why only qualitative evidence is available.").
		BestPractice("When measurable criteria matter, quantify the decision-relevant metrics for this domain when credible evidence exists, and identify when the available numbers are stale, indirect, or not comparable.").
		BestPractice("Prefer primary benchmarks, standards, papers, and official telemetry over secondary summaries when comparing numeric claims.").
		BestPractice("Seek evidence across multiple relevant classes for each option, not only vendor positioning or one convenient source type.").
		BestPractice("Treat self-authored, vendor-authored, or project-authored sources as capability evidence first rather than as standalone proof that one option beats another.").
		BestPractice("Use a side-by-side comparison matrix and surface the strongest counterarguments against the leading option.").
		BestPractice("Call out methodology limits, dataset bias, measurement caveats, and hidden assumptions that could change the ranking.").
		Requirement("For substantial comparisons, evaluate the options against the criteria that actually drive the decision and summarize the result in a table or equivalently structured comparison.").
		Requirement("For each major option, include the strongest supporting evidence, the strongest contrary evidence or failure mode, and any major unresolved uncertainty.").
		Requirement("Map the relevant evidence classes for the domain and note which are strong, weak, or unavailable before you finalize the ranking.").
		Requirement("Include the most decision-relevant evidence type for each major claim: measured data, official guidance, peer-reviewed research, or qualitative evidence.").
		Requirement("Validate the assumptions behind the comparison and note where the evidence does not cleanly support the ranking.").
		Requirement("Do not start a substantial comparison with a winner; make the criteria and evidence matrix legible before or alongside the ranking.").
		Requirement("Do not emit a Short Answer, shortlist, or compressed winner section before the comparison evidence is established.").
		Requirement("Before finalizing, run a rigor audit over all relevant comparison dimensions: per-link grounding, corroboration, counterevidence, math checks, methodology quality, bias risks, and threats to validity.").
		Requirement("Only use high confidence when multiple relevant evidence classes align and the major counterarguments have been addressed explicitly.").
		Requirement("If independent validation is relevant and the ranking still depends mainly on self-authored sources, mark it provisional or incomplete instead of presenting a clean winner.").
		Avoid("Do not stop at surface positioning, shallow inventories, or summary-style prose when deeper tradeoff analysis is needed.").
		Avoid("Do not reduce the comparison to a default winner, a few alternatives, and a bare source list.").
		Avoid("Do not use unsupported adjectives like best, strongest, simpler, faster, safer, or mature without tying them to explicit criteria and grounded evidence.").
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
		Description("Recommend a solution with explicit decision criteria, grounded evidence, applicability analysis, and Librarian validation. ALWAYS consults Librarian.").
		Domain("research").
		Keywords("recommend", "suggest", "solution", "solve").
		BestPractice("Start from decision criteria and evidence quality, not brand preference, familiarity, or rhetorical certainty.").
		BestPractice("Back material claims about performance, reliability, cost, security impact, scale, or adoption with grounded statistics, studies, standards, or official operational data whenever credible evidence exists.").
		BestPractice("When measurable criteria matter, surface the decision-relevant metrics for this domain and quantify them when credible evidence exists; otherwise state plainly that the support is qualitative or incomplete.").
		BestPractice("If the evidence for a recommendation is mixed, indirect, or mostly qualitative, say so plainly instead of overstating confidence.").
		BestPractice("Research the strongest downside, failure mode, criticism, lock-in risk, migration burden, or conflicting evidence against the leading option before recommending it.").
		BestPractice("Treat self-authored, vendor-authored, or project-authored sources as capability evidence first rather than as standalone proof that the preferred option is superior.").
		BestPractice("Include the strongest downside of the preferred option and explain why it still wins.").
		BestPractice("Surface the key assumptions, methodology caveats, and bias risks that could invalidate the recommendation.").
		Requirement("For substantial recommendations, map the relevant evidence classes for this domain and disclose which are strong, weak, or unavailable: primary or authoritative sources, empirical or observational evidence, counterevidence or failure cases, methodological or limitations evidence, implementation or operational burden, institutional or ecosystem context, and formal or academic or regulatory evidence.").
		Requirement("For substantial recommendations, justify the choice with explicit evidence categories and include a structured comparison or design sketch when it materially improves clarity.").
		Requirement("Show why the preferred option beats the strongest alternative on the highest-weighted criteria, not just why it sounds good in isolation.").
		Requirement("Validate important assumptions and note where the supporting math, datasets, or empirical evidence is thin, biased, or contested.").
		Requirement("Do not open a substantial externally sourced recommendation with a naked default pick; make the decision frame and evidence quality legible before or alongside the recommendation.").
		Requirement("Do not emit a Short Answer, shortlist, or compressed winner section before the evidence basis and strongest-alternative comparison are established.").
		Requirement("Before finalizing, run a rigor audit over all relevant recommendation dimensions: per-link grounding, corroboration, alternatives, counterevidence, math checks, methodology quality, bias risks, and threats to validity.").
		Requirement("Do not assign high confidence unless the recommendation is supported by broad grounded evidence across the relevant classes, counterevidence was examined, and important limitations are explicit.").
		Requirement("If independent validation matters and the conclusion still relies mainly on self-authored sources, keep the recommendation provisional or explicitly inconclusive.").
		Avoid("Do not make the recommendation read like promotional copy or a generic roundup; make the tradeoffs concrete and evidence-backed.").
		Avoid("Do not stop after naming a winner and a few alternatives when the decision needs comparative evidence synthesis.").
		Avoid("Do not use unsupported adjectives like best, strongest, safer, simpler, faster, mature, or standard without tying them to explicit criteria and grounded evidence.").
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
		if errors.Is(err, skills.ErrDelegatedRequested) {
			return nil, err
		}
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
		SourceAgentID: strings.TrimSpace(a.id),
		TargetAgentID: "librarian",
		FireAndForget: true,
		Timestamp:     time.Now(),
	}

	return a.PublishRequest(req)
}
