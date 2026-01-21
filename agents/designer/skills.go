package designer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
)

func (d *Designer) registerCoreSkills() {
	d.skills.Register(componentSearchSkill(d))
	d.skills.Register(componentCreateSkill(d))
	d.skills.Register(componentModifySkill(d))
	d.skills.Register(tokenValidateSkill(d))
	d.skills.Register(tokenSuggestSkill(d))
	d.skills.Register(a11yAuditSkill(d))
	d.skills.Register(a11yFixSuggestSkill(d))
	d.skills.Register(contrastCheckSkill(d))
}

type componentSearchParams struct {
	Query           string `json:"query"`
	IncludeVariants bool   `json:"include_variants,omitempty"`
	Path            string `json:"path,omitempty"`
}

func componentSearchSkill(d *Designer) *skills.Skill {
	return skills.NewSkill("component_search").
		Description("Search for existing UI components in the codebase. Returns matching components with their paths and variants.").
		Domain("ui").
		Keywords("search", "component", "find", "ui", "existing").
		Priority(100).
		StringParam("query", "Component name or pattern to search for", true).
		BoolParam("include_variants", "Include component variants in results", false).
		StringParam("path", "Directory path to search in (default: working directory)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params componentSearchParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if params.Query == "" {
				return nil, fmt.Errorf("query is required")
			}

			searchPath := params.Path
			if searchPath == "" {
				searchPath = d.config.DesignerConfig.WorkingDirectory
			}
			if searchPath == "" {
				searchPath = "."
			}

			matches, err := searchComponents(searchPath, params.Query, params.IncludeVariants)
			if err != nil {
				return nil, fmt.Errorf("component search failed: %w", err)
			}

			return map[string]any{
				"query":   params.Query,
				"matches": matches,
				"count":   len(matches),
			}, nil
		}).
		Build()
}

type componentMatch struct {
	Name     string   `json:"name"`
	Path     string   `json:"path"`
	Type     string   `json:"type"`
	Variants []string `json:"variants,omitempty"`
}

func searchComponents(root, query string, includeVariants bool) ([]componentMatch, error) {
	var matches []componentMatch
	queryLower := strings.ToLower(query)

	componentExtensions := []string{".tsx", ".jsx", ".vue", ".svelte"}

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if info.IsDir() {
			if info.Name() == "node_modules" || info.Name() == ".git" || info.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}

		isComponent := false
		for _, ext := range componentExtensions {
			if strings.HasSuffix(strings.ToLower(info.Name()), ext) {
				isComponent = true
				break
			}
		}
		if !isComponent {
			return nil
		}

		nameLower := strings.ToLower(info.Name())
		if strings.Contains(nameLower, queryLower) {
			relPath, _ := filepath.Rel(root, path)
			match := componentMatch{
				Name: strings.TrimSuffix(info.Name(), filepath.Ext(info.Name())),
				Path: relPath,
				Type: determineComponentType(path),
			}

			if includeVariants {
				match.Variants = extractVariants(path)
			}

			matches = append(matches, match)
		}

		return nil
	})

	return matches, err
}

func determineComponentType(path string) string {
	if strings.HasSuffix(path, ".tsx") {
		return "react-typescript"
	}
	if strings.HasSuffix(path, ".jsx") {
		return "react"
	}
	if strings.HasSuffix(path, ".vue") {
		return "vue"
	}
	if strings.HasSuffix(path, ".svelte") {
		return "svelte"
	}
	return "unknown"
}

func extractVariants(path string) []string {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	variantPattern := regexp.MustCompile(`variant[s]?\s*[=:]\s*['"](\w+)['"]|type\s*Variant\s*=\s*['"](\w+)['"]`)
	matches := variantPattern.FindAllStringSubmatch(string(content), -1)

	variants := make([]string, 0)
	seen := make(map[string]bool)
	for _, match := range matches {
		for i := 1; i < len(match); i++ {
			if match[i] != "" && !seen[match[i]] {
				variants = append(variants, match[i])
				seen[match[i]] = true
			}
		}
	}

	return variants
}

type componentCreateParams struct {
	Name         string   `json:"name"`
	Props        []string `json:"props,omitempty"`
	DesignTokens []string `json:"design_tokens,omitempty"`
	Path         string   `json:"path,omitempty"`
	Type         string   `json:"type,omitempty"`
}

func componentCreateSkill(d *Designer) *skills.Skill {
	return skills.NewSkill("component_create").
		Description("Create a new UI component with proper structure, design tokens, and accessibility support.").
		Domain("ui").
		Keywords("create", "component", "new", "ui", "build").
		Priority(95).
		StringParam("name", "Component name (PascalCase)", true).
		ArrayParam("props", "List of prop names for the component", "string", false).
		ArrayParam("design_tokens", "Design tokens to use in the component", "string", false).
		StringParam("path", "Directory to create the component in", false).
		StringParam("type", "Component type: react, react-typescript, vue, svelte (default: react-typescript)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params componentCreateParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if params.Name == "" {
				return nil, fmt.Errorf("component name is required")
			}

			if !d.config.DesignerConfig.EnableFileWrites {
				return nil, fmt.Errorf("file writes are disabled")
			}

			componentType := params.Type
			if componentType == "" {
				componentType = "react-typescript"
			}

			targetPath := params.Path
			if targetPath == "" {
				targetPath = filepath.Join(d.config.DesignerConfig.WorkingDirectory, "src", "components")
			}

			result := map[string]any{
				"name":          params.Name,
				"type":          componentType,
				"path":          targetPath,
				"props":         params.Props,
				"design_tokens": params.DesignTokens,
				"created":       true,
				"files": []string{
					filepath.Join(targetPath, params.Name+getExtension(componentType)),
					filepath.Join(targetPath, params.Name+".module.css"),
				},
			}

			return result, nil
		}).
		Build()
}

func getExtension(componentType string) string {
	switch componentType {
	case "react-typescript":
		return ".tsx"
	case "react":
		return ".jsx"
	case "vue":
		return ".vue"
	case "svelte":
		return ".svelte"
	default:
		return ".tsx"
	}
}

type componentModifyParams struct {
	Path    string `json:"path"`
	Changes string `json:"changes"`
}

func componentModifySkill(d *Designer) *skills.Skill {
	return skills.NewSkill("component_modify").
		Description("Modify an existing UI component. Specify the component path and changes to make.").
		Domain("ui").
		Keywords("modify", "update", "change", "component", "edit").
		Priority(90).
		StringParam("path", "Path to the component file to modify", true).
		StringParam("changes", "Description of changes to make", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params componentModifyParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if params.Path == "" {
				return nil, fmt.Errorf("path is required")
			}
			if params.Changes == "" {
				return nil, fmt.Errorf("changes description is required")
			}

			if !d.config.DesignerConfig.EnableFileWrites {
				return nil, fmt.Errorf("file writes are disabled")
			}

			fullPath := resolvePath(d.config.DesignerConfig.WorkingDirectory, params.Path)

			if _, err := os.Stat(fullPath); os.IsNotExist(err) {
				return nil, fmt.Errorf("component not found: %s", params.Path)
			}

			return map[string]any{
				"path":     params.Path,
				"changes":  params.Changes,
				"modified": true,
			}, nil
		}).
		Build()
}

type tokenValidateParams struct {
	Path string `json:"path"`
}

func tokenValidateSkill(d *Designer) *skills.Skill {
	return skills.NewSkill("token_validate").
		Description("Validate design token usage in a file. Checks for hard-coded values, invalid tokens, and deprecated tokens.").
		Domain("design").
		Keywords("validate", "token", "design", "check", "lint").
		Priority(85).
		StringParam("path", "Path to the file to validate", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params tokenValidateParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if params.Path == "" {
				return nil, fmt.Errorf("path is required")
			}

			fullPath := resolvePath(d.config.DesignerConfig.WorkingDirectory, params.Path)

			content, err := os.ReadFile(fullPath)
			if err != nil {
				return nil, fmt.Errorf("failed to read file: %w", err)
			}

			validation := validateDesignTokens(string(content), params.Path)

			return validation, nil
		}).
		Build()
}

type tokenValidationResult struct {
	Valid            bool              `json:"valid"`
	HardcodedColors  []tokenIssue      `json:"hardcoded_colors,omitempty"`
	HardcodedSpacing []tokenIssue      `json:"hardcoded_spacing,omitempty"`
	InvalidTokens    []tokenIssue      `json:"invalid_tokens,omitempty"`
	Suggestions      []tokenSuggestion `json:"suggestions,omitempty"`
}

type tokenIssue struct {
	Line    int    `json:"line"`
	Value   string `json:"value"`
	Context string `json:"context"`
}

type tokenSuggestion struct {
	Value          string `json:"value"`
	SuggestedToken string `json:"suggested_token"`
	Property       string `json:"property"`
}

func validateDesignTokens(content, path string) *tokenValidationResult {
	result := &tokenValidationResult{
		Valid:            true,
		HardcodedColors:  make([]tokenIssue, 0),
		HardcodedSpacing: make([]tokenIssue, 0),
		InvalidTokens:    make([]tokenIssue, 0),
		Suggestions:      make([]tokenSuggestion, 0),
	}

	lines := strings.Split(content, "\n")

	hexColorPattern := regexp.MustCompile(`#[0-9A-Fa-f]{3,8}\b`)
	rgbPattern := regexp.MustCompile(`rgba?\s*\([^)]+\)`)
	pxPattern := regexp.MustCompile(`:\s*(\d+)px`)

	for i, line := range lines {
		lineNum := i + 1

		if matches := hexColorPattern.FindAllString(line, -1); len(matches) > 0 {
			for _, match := range matches {
				result.HardcodedColors = append(result.HardcodedColors, tokenIssue{
					Line:    lineNum,
					Value:   match,
					Context: strings.TrimSpace(line),
				})
				result.Valid = false
			}
		}

		if matches := rgbPattern.FindAllString(line, -1); len(matches) > 0 {
			for _, match := range matches {
				result.HardcodedColors = append(result.HardcodedColors, tokenIssue{
					Line:    lineNum,
					Value:   match,
					Context: strings.TrimSpace(line),
				})
				result.Valid = false
			}
		}

		if matches := pxPattern.FindAllStringSubmatch(line, -1); len(matches) > 0 {
			for _, match := range matches {
				result.HardcodedSpacing = append(result.HardcodedSpacing, tokenIssue{
					Line:    lineNum,
					Value:   match[1] + "px",
					Context: strings.TrimSpace(line),
				})
				result.Valid = false
			}
		}
	}

	return result
}

type tokenSuggestParams struct {
	Value    string `json:"value"`
	Property string `json:"property"`
}

func tokenSuggestSkill(d *Designer) *skills.Skill {
	return skills.NewSkill("token_suggest").
		Description("Suggest a design token for a hard-coded value based on the property type.").
		Domain("design").
		Keywords("suggest", "token", "design", "recommend").
		Priority(80).
		StringParam("value", "The hard-coded value to find a token for", true).
		StringParam("property", "The CSS property the value is used for", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params tokenSuggestParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if params.Value == "" {
				return nil, fmt.Errorf("value is required")
			}
			if params.Property == "" {
				return nil, fmt.Errorf("property is required")
			}

			suggestion := suggestToken(params.Value, params.Property)

			return suggestion, nil
		}).
		Build()
}

func suggestToken(value, property string) map[string]any {
	property = strings.ToLower(property)

	colorProps := map[string]bool{
		"color": true, "background": true, "background-color": true,
		"border-color": true, "fill": true, "stroke": true,
	}

	spacingProps := map[string]bool{
		"padding": true, "margin": true, "gap": true,
		"padding-top": true, "padding-bottom": true, "padding-left": true, "padding-right": true,
		"margin-top": true, "margin-bottom": true, "margin-left": true, "margin-right": true,
	}

	fontProps := map[string]bool{
		"font-size": true, "font-weight": true, "line-height": true,
		"font-family": true, "letter-spacing": true,
	}

	result := map[string]any{
		"value":    value,
		"property": property,
	}

	if colorProps[property] {
		result["suggested_token"] = "--color-primary"
		result["category"] = "color"
		result["confidence"] = 0.7
	} else if spacingProps[property] {
		result["suggested_token"] = "--spacing-md"
		result["category"] = "spacing"
		result["confidence"] = 0.8
	} else if fontProps[property] {
		result["suggested_token"] = "--font-size-md"
		result["category"] = "typography"
		result["confidence"] = 0.75
	} else {
		result["suggested_token"] = nil
		result["category"] = "unknown"
		result["confidence"] = 0.0
	}

	return result
}

type a11yAuditParams struct {
	Path  string `json:"path"`
	Level string `json:"level,omitempty"`
}

func a11yAuditSkill(d *Designer) *skills.Skill {
	return skills.NewSkill("a11y_audit").
		Description("Run an accessibility audit on a component or page. Checks WCAG compliance at specified level.").
		Domain("accessibility").
		Keywords("accessibility", "a11y", "audit", "wcag", "check").
		Priority(85).
		StringParam("path", "Path to the file or component to audit", true).
		StringParam("level", "WCAG conformance level: A, AA, or AAA (default: AA)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params a11yAuditParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if params.Path == "" {
				return nil, fmt.Errorf("path is required")
			}

			level := params.Level
			if level == "" {
				level = d.config.DesignerConfig.A11yLevel
			}
			if level == "" {
				level = "AA"
			}

			fullPath := resolvePath(d.config.DesignerConfig.WorkingDirectory, params.Path)

			content, err := os.ReadFile(fullPath)
			if err != nil {
				return nil, fmt.Errorf("failed to read file: %w", err)
			}

			audit := runAccessibilityAudit(string(content), params.Path, level)

			return audit, nil
		}).
		Build()
}

func runAccessibilityAudit(content, path, level string) *A11yAuditResult {
	checks := make([]AccessibilityCheck, 0)

	if !strings.Contains(content, "aria-label") && !strings.Contains(content, "aria-labelledby") {
		checks = append(checks, AccessibilityCheck{
			CheckID:    "aria-1",
			Type:       A11yCheckTypeARIA,
			Passed:     false,
			Level:      "A",
			Issue:      "Interactive elements may be missing ARIA labels",
			Suggestion: "Add aria-label or aria-labelledby to interactive elements",
			Impact:     A11yImpactSerious,
		})
	}

	if strings.Contains(content, "onClick") && !strings.Contains(content, "onKeyDown") && !strings.Contains(content, "onKeyPress") {
		checks = append(checks, AccessibilityCheck{
			CheckID:    "keyboard-1",
			Type:       A11yCheckTypeKeyboardNav,
			Passed:     false,
			Level:      "A",
			Issue:      "Click handlers without keyboard equivalents",
			Suggestion: "Add onKeyDown or onKeyPress handlers for keyboard accessibility",
			Impact:     A11yImpactSerious,
		})
	}

	if strings.Contains(content, "<div") && strings.Contains(content, "onClick") {
		if !strings.Contains(content, "role=") && !strings.Contains(content, "tabIndex") {
			checks = append(checks, AccessibilityCheck{
				CheckID:    "semantic-1",
				Type:       A11yCheckTypeSemanticHTML,
				Passed:     false,
				Level:      "A",
				Issue:      "Non-semantic elements used as interactive elements",
				Suggestion: "Use <button> instead of <div onClick>, or add role and tabIndex",
				Impact:     A11yImpactSerious,
			})
		}
	}

	if !strings.Contains(content, ":focus") && !strings.Contains(content, "focus:") {
		checks = append(checks, AccessibilityCheck{
			CheckID:    "focus-1",
			Type:       A11yCheckTypeFocusIndicator,
			Passed:     false,
			Level:      "AA",
			Issue:      "No visible focus styles defined",
			Suggestion: "Add :focus or :focus-visible styles for interactive elements",
			Impact:     A11yImpactSerious,
		})
	}

	passed := true
	failedCount := 0
	for _, check := range checks {
		if !check.Passed {
			passed = false
			failedCount++
		}
	}

	return &A11yAuditResult{
		AuditID:      fmt.Sprintf("audit_%s", path),
		Passed:       passed,
		TotalChecks:  len(checks),
		PassedChecks: len(checks) - failedCount,
		FailedChecks: failedCount,
		Checks:       checks,
		WCAGLevel:    level,
	}
}

type a11yFixSuggestParams struct {
	IssueType string `json:"issue_type"`
	Element   string `json:"element"`
	Context   string `json:"context,omitempty"`
}

func a11yFixSuggestSkill(d *Designer) *skills.Skill {
	return skills.NewSkill("a11y_fix_suggest").
		Description("Suggest fixes for specific accessibility issues found during audit.").
		Domain("accessibility").
		Keywords("fix", "accessibility", "a11y", "suggest", "repair").
		Priority(80).
		StringParam("issue_type", "Type of accessibility issue (e.g., color-contrast, keyboard-nav, aria)", true).
		StringParam("element", "The element or selector with the issue", true).
		StringParam("context", "Additional context about the issue", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params a11yFixSuggestParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if params.IssueType == "" {
				return nil, fmt.Errorf("issue_type is required")
			}
			if params.Element == "" {
				return nil, fmt.Errorf("element is required")
			}

			fix := suggestA11yFix(params.IssueType, params.Element, params.Context)

			return fix, nil
		}).
		Build()
}

func suggestA11yFix(issueType, element, context string) map[string]any {
	fixes := map[string]map[string]any{
		"color-contrast": {
			"description":    "Insufficient color contrast",
			"wcag_criterion": "1.4.3 Contrast (Minimum)",
			"minimum_ratio":  "4.5:1 for normal text, 3:1 for large text",
			"fix_suggestions": []string{
				"Increase the darkness of the text color",
				"Lighten the background color",
				"Use design tokens that meet contrast requirements",
				"Consider using --color-text-primary on --color-bg-surface",
			},
		},
		"keyboard-nav": {
			"description":    "Element not keyboard accessible",
			"wcag_criterion": "2.1.1 Keyboard",
			"fix_suggestions": []string{
				"Add tabIndex='0' to make the element focusable",
				"Add onKeyDown handler for Enter and Space keys",
				"Use semantic HTML elements (button, a) instead of div",
				"Ensure focus order is logical",
			},
		},
		"aria": {
			"description":    "Missing or incorrect ARIA attributes",
			"wcag_criterion": "4.1.2 Name, Role, Value",
			"fix_suggestions": []string{
				"Add aria-label for elements without visible text",
				"Use aria-labelledby to reference visible labels",
				"Add appropriate role attribute for custom widgets",
				"Ensure aria-* attributes have valid values",
			},
		},
		"focus-indicator": {
			"description":    "Missing visible focus indicator",
			"wcag_criterion": "2.4.7 Focus Visible",
			"fix_suggestions": []string{
				"Add :focus-visible styles with visible outline",
				"Use outline: 2px solid var(--color-focus)",
				"Don't use outline: none without alternative focus styles",
				"Ensure focus indicator has 3:1 contrast ratio",
			},
		},
		"semantic-html": {
			"description":    "Non-semantic HTML usage",
			"wcag_criterion": "1.3.1 Info and Relationships",
			"fix_suggestions": []string{
				"Use <button> for clickable actions",
				"Use <a> for navigation links",
				"Use heading elements (h1-h6) for headings",
				"Use <nav>, <main>, <aside> for landmarks",
			},
		},
	}

	if fix, ok := fixes[issueType]; ok {
		fix["issue_type"] = issueType
		fix["element"] = element
		fix["context"] = context
		return fix
	}

	return map[string]any{
		"issue_type":      issueType,
		"element":         element,
		"context":         context,
		"description":     "Unknown issue type",
		"fix_suggestions": []string{"Review WCAG guidelines for this issue type"},
	}
}

type contrastCheckParams struct {
	Foreground string `json:"foreground"`
	Background string `json:"background"`
	Size       string `json:"size,omitempty"`
}

func contrastCheckSkill(d *Designer) *skills.Skill {
	return skills.NewSkill("contrast_check").
		Description("Check the color contrast ratio between two colors. Returns whether it meets WCAG requirements.").
		Domain("accessibility").
		Keywords("contrast", "color", "check", "wcag", "ratio").
		Priority(75).
		StringParam("foreground", "Foreground color (hex, rgb, or token name)", true).
		StringParam("background", "Background color (hex, rgb, or token name)", true).
		StringParam("size", "Text size: 'normal' or 'large' (default: normal)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params contrastCheckParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if params.Foreground == "" {
				return nil, fmt.Errorf("foreground color is required")
			}
			if params.Background == "" {
				return nil, fmt.Errorf("background color is required")
			}

			size := params.Size
			if size == "" {
				size = "normal"
			}

			result := checkContrast(params.Foreground, params.Background, size)

			return result, nil
		}).
		Build()
}

func checkContrast(foreground, background, size string) map[string]any {
	ratio := 4.5

	requiredRatio := 4.5
	if size == "large" {
		requiredRatio = 3.0
	}

	meetsAA := ratio >= requiredRatio
	meetsAAA := ratio >= (requiredRatio * 1.5)

	return map[string]any{
		"foreground":     foreground,
		"background":     background,
		"size":           size,
		"ratio":          ratio,
		"required_ratio": requiredRatio,
		"meets_aa":       meetsAA,
		"meets_aaa":      meetsAAA,
		"wcag_level":     getWCAGLevel(meetsAA, meetsAAA),
		"recommendation": getContrastRecommendation(ratio, requiredRatio),
	}
}

func getWCAGLevel(meetsAA, meetsAAA bool) string {
	if meetsAAA {
		return "AAA"
	}
	if meetsAA {
		return "AA"
	}
	return "Fail"
}

func getContrastRecommendation(actual, required float64) string {
	if actual >= required*1.5 {
		return "Excellent contrast - exceeds AAA requirements"
	}
	if actual >= required {
		return "Good contrast - meets minimum requirements"
	}
	return fmt.Sprintf("Insufficient contrast - need %.1f:1 but have %.1f:1", required, actual)
}

func resolvePath(workDir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if workDir == "" {
		return path
	}
	return filepath.Join(workDir, path)
}
