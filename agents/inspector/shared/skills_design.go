package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
)

// Phase 2.K / 10.B refactor (docs/PIPELINE_SKILL_REFACTOR.md §10.B):
// validate_token_usage + validate_accessibility + validate_component_api
// + validate_design_consistency collapsed into
// validate_ui_compliance(aspect=…). One primitive, same underlying
// scanners, one enum parameter tells it which compliance aspect to
// evaluate. The old per-aspect skill names are gone; downstream
// callers (quality-gate evaluation, validation plan) route legacy
// names through aspect via legacyUIComplianceAspectForTool.
const (
	uiComplianceAspectTokens       = "tokens"
	uiComplianceAspectAccessibility = "a11y"
	uiComplianceAspectComponentAPI = "component_api"
	uiComplianceAspectConsistency  = "consistency"
)

func uiComplianceAspects() []string {
	return []string{
		uiComplianceAspectTokens,
		uiComplianceAspectAccessibility,
		uiComplianceAspectComponentAPI,
		uiComplianceAspectConsistency,
	}
}

// ValidateUIComplianceSkill returns a single primitive that dispatches
// to the token, accessibility, component-API, or spacing-consistency
// scanner based on the aspect parameter. Replaces the previous four
// validate_* skills.
func ValidateUIComplianceSkill(runner *ToolRunner) *skills.Skill {
	return skills.NewSkill("validate_ui_compliance").
		Description("Scan engineer-authored Go UI code for design-system compliance in the requested aspect. Collapses validate_token_usage, validate_accessibility, validate_component_api, and validate_design_consistency into one primitive dispatched by the aspect enum.").
		Domain("design").
		Keywords("design", "token", "palette", "a11y", "accessibility", "wcag", "contrast", "component", "bubbletea", "consistency", "spacing", "margin").
		Priority(90).
		Usage("Set aspect=tokens|a11y|component_api|consistency. Run on the concrete UI scope under review, not the whole repo by default. Use during implementation-validation when the task changed UI code or design-system surfaces.").
		Satisfies("Produces design-compliance findings for validation reports, quality gates, and downstream designer/engineer fixes.").
		Avoid("Do not use during pre-implementation contract synthesis. Aspect must match the concern you actually have evidence for — a11y needs lipgloss fg/bg pairs, component_api needs BubbleTea model files, tokens/consistency need lipgloss styling code.").
		EnumParam("aspect", "UI compliance aspect", uiComplianceAspects(), true).
		ArrayParam("paths", "Go files to validate", "string", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Aspect string   `json:"aspect"`
				Paths  []string `json:"paths"`
			}
			_ = json.Unmarshal(input, &params)
			aspect := strings.ToLower(strings.TrimSpace(params.Aspect))
			if aspect == "" {
				return nil, fmt.Errorf("aspect is required (expected one of: tokens, a11y, component_api, consistency)")
			}
			paths := params.Paths
			if len(paths) == 0 {
				return nil, fmt.Errorf("paths is required")
			}

			var (
				issues []ValidationIssue
				label  string
			)
			switch aspect {
			case uiComplianceAspectTokens:
				label = "validate_token_usage"
				issues = validateTokenUsage(paths)
			case uiComplianceAspectAccessibility:
				label = "validate_accessibility"
				issues = validateAccessibility(paths)
			case uiComplianceAspectComponentAPI:
				label = "validate_component_api"
				issues = validateComponentAPI(paths)
			case uiComplianceAspectConsistency:
				label = "validate_design_consistency"
				issues = validateDesignConsistency(paths)
			default:
				return nil, fmt.Errorf("unknown aspect: %q (expected one of: tokens, a11y, component_api, consistency)", params.Aspect)
			}
			result := analysisResult(label, issues)
			result["aspect"] = aspect
			return result, nil
		}).
		Build()
}

// --- Token usage validation ---

// hexColorPattern matches standalone hex color literals like "#fab387" or "#FF6600".
var hexColorPattern = regexp.MustCompile(`"#[0-9a-fA-F]{6}"`)

// inlineColorPattern matches lipgloss.Color("...") with a hardcoded hex value.
var inlineColorPattern = regexp.MustCompile(`lipgloss\.Color\(\s*"#[0-9a-fA-F]{6}"\s*\)`)

func validateTokenUsage(paths []string) []ValidationIssue {
	var issues []ValidationIssue
	for _, path := range paths {
		issues = append(issues, scanFileForTokenViolations(path)...)
	}
	return issues
}

func scanFileForTokenViolations(path string) []ValidationIssue {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var issues []ValidationIssue
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if isCommentLine(line) {
			continue
		}
		// Skip lines that reference theme/palette (proper token usage).
		if strings.Contains(line, "theme.") || strings.Contains(line, "palette.") {
			continue
		}
		matches := hexColorPattern.FindAllStringIndex(line, -1)
		for range matches {
			issues = append(issues, ValidationIssue{
				Severity: Medium,
				File:     path,
				Line:     i + 1,
				Message:  "Hardcoded hex color literal — use theme/palette token instead.",
				RuleID:   "design/token-hardcoded-color",
				Domain:   DomainDesign,
			})
		}
		inlineMatches := inlineColorPattern.FindAllStringIndex(line, -1)
		for range inlineMatches {
			issues = append(issues, ValidationIssue{
				Severity: Medium,
				File:     path,
				Line:     i + 1,
				Message:  "Inline lipgloss.Color with hardcoded hex — use theme/palette token instead.",
				RuleID:   "design/token-inline-color",
				Domain:   DomainDesign,
			})
		}
	}
	return issues
}

func isCommentLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*")
}

// --- Accessibility validation ---

// fgBgPattern matches paired Foreground/Background lipgloss calls for contrast checking.
var fgBgPattern = regexp.MustCompile(`(?:Foreground|Background)\(.*?"(#[0-9a-fA-F]{6})"`)

func validateAccessibility(paths []string) []ValidationIssue {
	var issues []ValidationIssue
	for _, path := range paths {
		issues = append(issues, scanFileForA11yViolations(path)...)
	}
	return issues
}

func scanFileForA11yViolations(path string) []ValidationIssue {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	content := string(data)
	lines := strings.Split(content, "\n")

	// Collect foreground/background hex colors per line.
	var issues []ValidationIssue
	for i, line := range lines {
		fgColors := extractColorCalls(line, "Foreground")
		bgColors := extractColorCalls(line, "Background")

		for _, fg := range fgColors {
			for _, bg := range bgColors {
				ratio := contrastRatio(fg, bg)
				if ratio < 4.5 {
					issues = append(issues, ValidationIssue{
						Severity:     High,
						File:         path,
						Line:         i + 1,
						Message:      fmt.Sprintf("WCAG AA contrast violation: ratio %.2f:1 (fg=%s, bg=%s, requires 4.5:1).", ratio, fg, bg),
						SuggestedFix: "Increase contrast between foreground and background colors.",
						RuleID:       "design/a11y-contrast-ratio",
						Domain:       DomainDesign,
					})
				}
			}
		}
	}
	return issues
}

var colorCallPattern = regexp.MustCompile(`"(#[0-9a-fA-F]{6})"`)

func extractColorCalls(line, method string) []string {
	idx := strings.Index(line, method+"(")
	if idx < 0 {
		return nil
	}
	segment := line[idx:]
	matches := colorCallPattern.FindAllStringSubmatch(segment, -1)
	var colors []string
	for _, m := range matches {
		colors = append(colors, m[1])
	}
	return colors
}

// contrastRatio computes the WCAG 2.1 contrast ratio between two hex colors.
func contrastRatio(hex1, hex2 string) float64 {
	l1 := relativeLuminance(hex1)
	l2 := relativeLuminance(hex2)
	lighter := max(l1, l2)
	darker := min(l1, l2)
	return (lighter + 0.05) / (darker + 0.05)
}

func relativeLuminance(hex string) float64 {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return 0
	}
	r := linearize(hexByte(hex[0:2]))
	g := linearize(hexByte(hex[2:4]))
	b := linearize(hexByte(hex[4:6]))
	return 0.2126*r + 0.7152*g + 0.0722*b
}

func linearize(srgb float64) float64 {
	if srgb <= 0.04045 {
		return srgb / 12.92
	}
	return math.Pow((srgb+0.055)/1.055, 2.4)
}

func hexByte(s string) float64 {
	var v int
	for _, c := range s {
		v <<= 4
		switch {
		case c >= '0' && c <= '9':
			v |= int(c - '0')
		case c >= 'a' && c <= 'f':
			v |= int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			v |= int(c-'A') + 10
		}
	}
	return float64(v) / 255.0
}

// --- Component API validation ---

// bubbleteaModelPattern detects types that embed or reference tea.Model.
var bubbleteaModelPattern = regexp.MustCompile(`func\s+\([^)]+\)\s+(Init|Update|View)\s*\(`)

func validateComponentAPI(paths []string) []ValidationIssue {
	var issues []ValidationIssue
	for _, path := range paths {
		issues = append(issues, scanFileForComponentAPI(path)...)
	}
	return issues
}

func scanFileForComponentAPI(path string) []ValidationIssue {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	content := string(data)

	// Only check files that import bubbletea.
	if !strings.Contains(content, "bubbletea") && !strings.Contains(content, `tea "`) {
		return nil
	}

	hasInit := strings.Contains(content, ") Init(")
	hasUpdate := strings.Contains(content, ") Update(")
	hasView := strings.Contains(content, ") View(")
	hasWindowSize := strings.Contains(content, "tea.WindowSizeMsg")

	var issues []ValidationIssue

	if !hasInit {
		issues = append(issues, ValidationIssue{
			Severity: High,
			File:     path,
			Line:     1,
			Message:  "BubbleTea component missing Init() method.",
			RuleID:   "design/component-missing-init",
			Domain:   DomainDesign,
		})
	}
	if !hasUpdate {
		issues = append(issues, ValidationIssue{
			Severity: Critical,
			File:     path,
			Line:     1,
			Message:  "BubbleTea component missing Update() method.",
			RuleID:   "design/component-missing-update",
			Domain:   DomainDesign,
		})
	}
	if !hasView {
		issues = append(issues, ValidationIssue{
			Severity: Critical,
			File:     path,
			Line:     1,
			Message:  "BubbleTea component missing View() method.",
			RuleID:   "design/component-missing-view",
			Domain:   DomainDesign,
		})
	}
	if hasUpdate && !hasWindowSize {
		issues = append(issues, ValidationIssue{
			Severity: Medium,
			File:     path,
			Line:     1,
			Message:  "BubbleTea component Update() does not handle tea.WindowSizeMsg.",
			RuleID:   "design/component-no-windowsize",
			Domain:   DomainDesign,
		})
	}

	return issues
}

// --- Design consistency validation ---

// magicPaddingPattern matches lipgloss Padding/Margin calls with numeric literals.
var magicPaddingPattern = regexp.MustCompile(`\.(Padding|Margin|MarginLeft|MarginRight|MarginTop|MarginBottom|PaddingLeft|PaddingRight|PaddingTop|PaddingBottom)\(\s*\d+`)

func validateDesignConsistency(paths []string) []ValidationIssue {
	var issues []ValidationIssue
	for _, path := range paths {
		issues = append(issues, scanFileForDesignConsistency(path)...)
	}
	return issues
}

func scanFileForDesignConsistency(path string) []ValidationIssue {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var issues []ValidationIssue
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if isCommentLine(line) {
			continue
		}
		matches := magicPaddingPattern.FindAllStringIndex(line, -1)
		for range matches {
			issues = append(issues, ValidationIssue{
				Severity:     Low,
				File:         path,
				Line:         i + 1,
				Message:      "Magic number in lipgloss spacing — use a named constant for consistency.",
				SuggestedFix: "Extract the numeric value to a named constant in a spacing/layout constants block.",
				RuleID:       "design/consistency-magic-spacing",
				Domain:       DomainDesign,
			})
		}
	}
	return issues
}
