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

// ValidateTokenUsageSkill returns a skill that checks for hardcoded color
// literals not referencing the theme/palette system.
func ValidateTokenUsageSkill(runner *ToolRunner) *skills.Skill {
	return skills.NewSkill("validate_token_usage").
		Description("Check Go files for hardcoded hex color literals not using theme/palette tokens.").
		Domain("design").
		Keywords("token", "color", "palette", "theme", "design").
		Priority(90).
		ArrayParam("paths", "Go files to validate", "string", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			paths := extractPaths(input)
			issues := validateTokenUsage(paths)
			return analysisResult("validate_token_usage", issues), nil
		}).
		Build()
}

// ValidateAccessibilitySkill returns a skill that checks lipgloss foreground/
// background pairs for WCAG AA contrast ratio compliance.
func ValidateAccessibilitySkill(runner *ToolRunner) *skills.Skill {
	return skills.NewSkill("validate_accessibility").
		Description("Check lipgloss color pairs for WCAG AA contrast ratio compliance (4.5:1 normal, 3:1 large).").
		Domain("design").
		Keywords("accessibility", "wcag", "contrast", "a11y", "design").
		Priority(90).
		ArrayParam("paths", "Go files to validate", "string", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			paths := extractPaths(input)
			issues := validateAccessibility(paths)
			return analysisResult("validate_accessibility", issues), nil
		}).
		Build()
}

// ValidateComponentAPISkill returns a skill that AST-scans BubbleTea components
// for correct Init/Update/View signatures.
func ValidateComponentAPISkill(runner *ToolRunner) *skills.Skill {
	return skills.NewSkill("validate_component_api").
		Description("Scan BubbleTea components for Init/Update/View method signatures and tea.WindowSizeMsg handling.").
		Domain("design").
		Keywords("bubbletea", "component", "model", "update", "view", "design").
		Priority(90).
		ArrayParam("paths", "Go files to validate", "string", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			paths := extractPaths(input)
			issues := validateComponentAPI(paths)
			return analysisResult("validate_component_api", issues), nil
		}).
		Build()
}

// ValidateDesignConsistencySkill returns a skill that checks for magic number
// spacing/margin values in lipgloss calls.
func ValidateDesignConsistencySkill(runner *ToolRunner) *skills.Skill {
	return skills.NewSkill("validate_design_consistency").
		Description("Check for inconsistent spacing/margin magic numbers in lipgloss Padding/Margin calls.").
		Domain("design").
		Keywords("consistency", "spacing", "margin", "padding", "design").
		Priority(90).
		ArrayParam("paths", "Go files to validate", "string", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			paths := extractPaths(input)
			issues := validateDesignConsistency(paths)
			return analysisResult("validate_design_consistency", issues), nil
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
