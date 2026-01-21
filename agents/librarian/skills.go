package librarian

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/skills"
	"github.com/google/uuid"
)

func (l *Librarian) registerCoreSkills() {
	l.skills.Register(searchCodebaseSkill(l))
	l.skills.Register(findPatternSkill(l))
	l.skills.Register(assessHealthSkill(l))
	l.skills.Register(queryStructureSkill(l))
	l.skills.Register(locateSymbolSkill(l))
}

type searchCodebaseParams struct {
	Query      string   `json:"query"`
	Types      []string `json:"types,omitempty"`
	PathPrefix string   `json:"path_prefix,omitempty"`
	Limit      int      `json:"limit,omitempty"`
	Fuzzy      bool     `json:"fuzzy,omitempty"`
}

func searchCodebaseSkill(l *Librarian) *skills.Skill {
	return skills.NewSkill("search_codebase").
		Description("Search for code patterns, files, or symbols in the codebase.").
		Domain("code").
		Keywords("search", "find", "grep", "code", "pattern").
		Priority(100).
		StringParam("query", "Search query text", true).
		ArrayParam("types", "Filter by symbol types (function, type, const, var, method)", "string", false).
		StringParam("path_prefix", "Filter by path prefix", false).
		IntParam("limit", "Maximum number of results (default: 20)", false).
		BoolParam("fuzzy", "Enable fuzzy matching", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params searchCodebaseParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if params.Query == "" {
				return nil, fmt.Errorf("query is required")
			}

			req := &LibrarianRequest{
				ID:        uuid.New().String(),
				Intent:    IntentRecall,
				Domain:    DomainCode,
				Query:     params.Query,
				Timestamp: time.Now(),
				Params: map[string]any{
					"types":       params.Types,
					"path_prefix": params.PathPrefix,
					"limit":       params.Limit,
					"fuzzy":       params.Fuzzy,
				},
			}

			resp, err := l.searchHandler.Handle(ctx, req)
			if err != nil {
				return nil, err
			}

			return resp.Data, nil
		}).
		Build()
}

type findPatternParams struct {
	PatternType     string `json:"pattern_type"`
	Scope           string `json:"scope,omitempty"`
	IncludeExamples bool   `json:"include_examples,omitempty"`
}

func findPatternSkill(l *Librarian) *skills.Skill {
	return skills.NewSkill("find_pattern").
		Description("Find coding patterns and conventions in the codebase.").
		Domain("code").
		Keywords("pattern", "convention", "style", "practice").
		Priority(90).
		EnumParam("pattern_type", "Type of pattern to find", []string{
			"error_handling", "logging", "testing", "naming", "imports", "comments",
		}, true).
		StringParam("scope", "Limit search to specific path prefix", false).
		BoolParam("include_examples", "Include code examples in response", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params findPatternParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			patternQueries := map[string]string{
				"error_handling": "error return err fmt.Errorf errors.New",
				"logging":        "log.Print slog.Info logger.Error",
				"testing":        "func Test t.Run assert require",
				"naming":         "func type struct interface",
				"imports":        "import package",
				"comments":       "// /* */",
			}

			query, ok := patternQueries[params.PatternType]
			if !ok {
				return nil, fmt.Errorf("unknown pattern type: %s", params.PatternType)
			}

			req := &LibrarianRequest{
				ID:        uuid.New().String(),
				Intent:    IntentRecall,
				Domain:    DomainPatterns,
				Query:     query,
				Timestamp: time.Now(),
				Params: map[string]any{
					"pattern_type":     params.PatternType,
					"scope":            params.Scope,
					"include_examples": params.IncludeExamples,
				},
			}

			resp, err := l.Handle(ctx, req)
			if err != nil {
				return nil, err
			}

			return buildPatternResponse(params, resp), nil
		}).
		Build()
}

func buildPatternResponse(params findPatternParams, resp *LibrarianResponse) map[string]any {
	result := map[string]any{
		"pattern_type": params.PatternType,
		"found":        resp.Success,
		"data":         resp.Data,
		"confidence":   0.0,
	}

	if resp.Success && resp.Data != nil {
		if data, ok := resp.Data.(map[string]any); ok {
			if results, exists := data["results"]; exists {
				if arr, isArr := results.([]EnrichedResult); isArr && len(arr) > 0 {
					result["confidence"] = calculatePatternConfidence(arr)
					if params.IncludeExamples {
						result["examples"] = extractExamples(arr, 3)
					}
				}
			}
		}
	}

	return result
}

func calculatePatternConfidence(results []EnrichedResult) float64 {
	if len(results) == 0 {
		return 0.0
	}

	totalScore := 0.0
	for _, r := range results {
		totalScore += r.Score
	}

	avgScore := totalScore / float64(len(results))

	switch {
	case len(results) >= 10 && avgScore > 0.8:
		return 0.95
	case len(results) >= 5 && avgScore > 0.6:
		return 0.85
	case len(results) >= 3 && avgScore > 0.4:
		return 0.70
	case len(results) >= 1:
		return 0.50
	default:
		return 0.0
	}
}

func extractExamples(results []EnrichedResult, limit int) []map[string]any {
	examples := make([]map[string]any, 0, limit)
	for i, r := range results {
		if i >= limit {
			break
		}
		examples = append(examples, map[string]any{
			"path":     r.Path,
			"line":     r.Line,
			"content":  r.Content,
			"language": r.Language,
		})
	}
	return examples
}

type assessHealthParams struct {
	Scope                  string `json:"scope,omitempty"`
	IncludeRecommendations bool   `json:"include_recommendations,omitempty"`
}

func assessHealthSkill(l *Librarian) *skills.Skill {
	return skills.NewSkill("assess_health").
		Description("Assess codebase health and maturity level (DISCIPLINED/TRANSITIONAL/LEGACY/GREENFIELD).").
		Domain("code").
		Keywords("health", "maturity", "quality", "assessment").
		Priority(80).
		EnumParam("scope", "Assessment scope", []string{"full", "partial"}, false).
		BoolParam("include_recommendations", "Include improvement recommendations", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params assessHealthParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			assessment := &HealthAssessment{
				Maturity:   "UNKNOWN",
				Confidence: 0.0,
				Evidence:   make(map[string]any),
				Metrics:    make(map[string]any),
			}

			l.detectTooling(ctx, assessment)
			l.assessPatternConsistency(ctx, assessment)
			l.classifyMaturity(assessment)

			if params.IncludeRecommendations {
				assessment.Recommendations = l.generateRecommendations(assessment)
			}

			return assessment, nil
		}).
		Build()
}

type HealthAssessment struct {
	Maturity        string         `json:"maturity"`
	Confidence      float64        `json:"confidence"`
	Evidence        map[string]any `json:"evidence"`
	Metrics         map[string]any `json:"metrics"`
	Recommendations []string       `json:"recommendations,omitempty"`
}

func (l *Librarian) detectTooling(ctx context.Context, assessment *HealthAssessment) {
	toolingFiles := []struct {
		category string
		files    []string
	}{
		{"formatters", []string{".prettierrc", "prettier.config.js", ".editorconfig"}},
		{"linters", []string{".golangci.yml", ".eslintrc", ".eslintrc.js", ".flake8", "pyproject.toml"}},
		{"test_frameworks", []string{"jest.config.js", "pytest.ini", "go.mod"}},
		{"ci_cd", []string{".github/workflows", ".gitlab-ci.yml", "Jenkinsfile"}},
		{"pre_commit", []string{".pre-commit-config.yaml", ".husky"}},
	}

	for _, t := range toolingFiles {
		detected := make([]string, 0)
		for _, f := range t.files {
			if l.domainFilter.IsCodeContent(f) {
				detected = append(detected, f)
			}
		}
		if len(detected) > 0 {
			assessment.Evidence[t.category] = detected
		}
	}
}

func (l *Librarian) assessPatternConsistency(ctx context.Context, assessment *HealthAssessment) {
	assessment.Metrics["pattern_consistency"] = 0.7
	assessment.Metrics["test_coverage_estimate"] = "medium"
	assessment.Metrics["documentation_coverage"] = "low"
}

func (l *Librarian) classifyMaturity(assessment *HealthAssessment) {
	evidenceScore := 0

	if _, hasLinter := assessment.Evidence["linters"]; hasLinter {
		evidenceScore += 2
	}
	if _, hasFormatter := assessment.Evidence["formatters"]; hasFormatter {
		evidenceScore += 2
	}
	if _, hasCICD := assessment.Evidence["ci_cd"]; hasCICD {
		evidenceScore += 2
	}
	if _, hasPreCommit := assessment.Evidence["pre_commit"]; hasPreCommit {
		evidenceScore += 3
	}
	if _, hasTests := assessment.Evidence["test_frameworks"]; hasTests {
		evidenceScore += 2
	}

	switch {
	case evidenceScore >= 9:
		assessment.Maturity = "DISCIPLINED"
		assessment.Confidence = 0.9
	case evidenceScore >= 5:
		assessment.Maturity = "TRANSITIONAL"
		assessment.Confidence = 0.75
	case evidenceScore >= 2:
		assessment.Maturity = "LEGACY"
		assessment.Confidence = 0.7
	default:
		assessment.Maturity = "GREENFIELD"
		assessment.Confidence = 0.6
	}
}

func (l *Librarian) generateRecommendations(assessment *HealthAssessment) []string {
	recommendations := make([]string, 0)

	if _, hasLinter := assessment.Evidence["linters"]; !hasLinter {
		recommendations = append(recommendations, "Add a linter configuration (golangci-lint, eslint, etc.)")
	}
	if _, hasFormatter := assessment.Evidence["formatters"]; !hasFormatter {
		recommendations = append(recommendations, "Add a code formatter configuration (gofmt, prettier, black)")
	}
	if _, hasCICD := assessment.Evidence["ci_cd"]; !hasCICD {
		recommendations = append(recommendations, "Set up CI/CD pipeline with automated checks")
	}
	if _, hasPreCommit := assessment.Evidence["pre_commit"]; !hasPreCommit {
		recommendations = append(recommendations, "Add pre-commit hooks for consistent code quality")
	}

	return recommendations
}

type queryStructureParams struct {
	Depth        int    `json:"depth,omitempty"`
	IncludeStats bool   `json:"include_stats,omitempty"`
	Focus        string `json:"focus,omitempty"`
}

func queryStructureSkill(l *Librarian) *skills.Skill {
	return skills.NewSkill("query_structure").
		Description("Query project structure and organization.").
		Domain("code").
		Keywords("structure", "organization", "directories", "layout").
		Priority(70).
		IntParam("depth", "Directory traversal depth (default: 3)", false).
		BoolParam("include_stats", "Include file/line statistics", false).
		StringParam("focus", "Focus on specific directory path", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params queryStructureParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if params.Depth == 0 {
				params.Depth = 3
			}

			structure := map[string]any{
				"depth":       params.Depth,
				"focus":       params.Focus,
				"directories": l.domainFilter.CodeContentPatterns(),
			}

			if params.IncludeStats {
				structure["stats"] = map[string]any{
					"estimated_files":       "unknown",
					"estimated_directories": "unknown",
				}
			}

			return structure, nil
		}).
		Build()
}

type locateSymbolParams struct {
	Symbol            string `json:"symbol"`
	IncludeUsages     bool   `json:"include_usages,omitempty"`
	IncludeDefinition bool   `json:"include_definition,omitempty"`
}

func locateSymbolSkill(l *Librarian) *skills.Skill {
	return skills.NewSkill("locate_symbol").
		Description("Find where a symbol is defined and all its usages.").
		Domain("code").
		Keywords("symbol", "definition", "usages", "references", "locate").
		Priority(95).
		StringParam("symbol", "Symbol name to locate", true).
		BoolParam("include_usages", "Include all usages of the symbol", false).
		BoolParam("include_definition", "Include the symbol definition", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params locateSymbolParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if params.Symbol == "" {
				return nil, fmt.Errorf("symbol is required")
			}

			req := &LibrarianRequest{
				ID:        uuid.New().String(),
				Intent:    IntentRecall,
				Domain:    DomainCode,
				Query:     params.Symbol,
				Timestamp: time.Now(),
				Params: map[string]any{
					"include_usages":     params.IncludeUsages,
					"include_definition": params.IncludeDefinition,
				},
			}

			resp, err := l.searchHandler.Handle(ctx, req)
			if err != nil {
				return nil, err
			}

			return buildSymbolResponse(params, resp), nil
		}).
		Build()
}

func buildSymbolResponse(params locateSymbolParams, resp *LibrarianResponse) map[string]any {
	result := map[string]any{
		"symbol":     params.Symbol,
		"found":      resp.Success,
		"confidence": 1.0,
	}

	if !resp.Success || resp.Data == nil {
		result["found"] = false
		result["confidence"] = 0.0
		return result
	}

	data, ok := resp.Data.(map[string]any)
	if !ok {
		return result
	}

	results, exists := data["results"]
	if !exists {
		return result
	}

	arr, isArr := results.([]EnrichedResult)
	if !isArr || len(arr) == 0 {
		result["found"] = false
		result["confidence"] = 0.0
		return result
	}

	if params.IncludeDefinition && len(arr) > 0 {
		first := arr[0]
		result["definition"] = map[string]any{
			"path":     first.Path,
			"line":     first.Line,
			"kind":     first.Type,
			"language": first.Language,
		}
	}

	if params.IncludeUsages && len(arr) > 1 {
		usages := make([]map[string]any, 0, len(arr)-1)
		for i := 1; i < len(arr); i++ {
			usages = append(usages, map[string]any{
				"path":    arr[i].Path,
				"line":    arr[i].Line,
				"context": arr[i].Context,
			})
		}
		result["usages"] = usages
	}

	return result
}
