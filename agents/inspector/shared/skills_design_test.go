package shared

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestQualityGrade_OverallForDomain_Code(t *testing.T) {
	g := QualityGrade{
		Correctness: 1.0,
		Robustness:  1.0,
		Performance: 1.0,
		Security:    1.0,
		Adherence:   1.0,
	}
	got := g.OverallForDomain(DomainCode)
	if math.Abs(got-1.0) > 1e-9 {
		t.Errorf("code overall = %f, want 1.0", got)
	}
	// Code overall must equal Overall() for backward compat.
	if got != g.Overall() {
		t.Errorf("OverallForDomain(code) = %f != Overall() = %f", got, g.Overall())
	}
}

func TestQualityGrade_OverallForDomain_Design(t *testing.T) {
	g := QualityGrade{
		Correctness:      0.9,
		Robustness:       0.8,
		Performance:      0.7,
		Security:         0.6,
		Adherence:        0.5,
		TokenConsistency: 1.0,
		Accessibility:    1.0,
		ComponentAPI:     1.0,
	}
	code := g.OverallForDomain(DomainCode)
	design := g.OverallForDomain(DomainDesign)

	// Design should differ from code when design dimensions are set.
	if code == design {
		t.Errorf("expected different scores for code (%f) and design (%f)", code, design)
	}

	// Verify design overall weights sum to ~1.0.
	maxGrade := QualityGrade{
		Correctness: 1.0, Robustness: 1.0, Performance: 1.0,
		Security: 1.0, Adherence: 1.0,
		TokenConsistency: 1.0, Accessibility: 1.0, ComponentAPI: 1.0,
	}
	designMax := maxGrade.OverallForDomain(DomainDesign)
	if math.Abs(designMax-1.0) > 1e-9 {
		t.Errorf("design overall with all 1.0 = %f, want 1.0", designMax)
	}
}

func TestQualityGrade_OverallForDomain_DefaultsToCode(t *testing.T) {
	g := QualityGrade{Correctness: 0.5}
	got := g.OverallForDomain("")
	want := g.OverallForDomain(DomainCode)
	if got != want {
		t.Errorf("empty domain = %f, want code = %f", got, want)
	}
}

func TestValidateTokenUsage_HardcodedColor(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "bad.go")
	os.WriteFile(f, []byte(`package ui

var color = "#fab387"
`), 0o644)

	issues := validateTokenUsage([]string{f})
	if len(issues) == 0 {
		t.Fatal("expected token usage issues for hardcoded color")
	}
	if issues[0].Domain != DomainDesign {
		t.Errorf("issue domain = %q, want %q", issues[0].Domain, DomainDesign)
	}
	if issues[0].RuleID != "design/token-hardcoded-color" {
		t.Errorf("issue rule = %q, want design/token-hardcoded-color", issues[0].RuleID)
	}
}

func TestValidateTokenUsage_ThemeReference_NoIssue(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "good.go")
	os.WriteFile(f, []byte(`package ui

var color = theme.Peach
`), 0o644)

	issues := validateTokenUsage([]string{f})
	if len(issues) != 0 {
		t.Errorf("expected no issues for theme reference, got %d", len(issues))
	}
}

func TestValidateComponentAPI_MissingMethods(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "model.go")
	os.WriteFile(f, []byte(`package ui

import tea "github.com/charmbracelet/bubbletea"

type Model struct{}

func (m Model) View() string { return "" }
`), 0o644)

	issues := validateComponentAPI([]string{f})
	// Should flag missing Init and Update.
	hasInit := false
	hasUpdate := false
	for _, issue := range issues {
		if issue.RuleID == "design/component-missing-init" {
			hasInit = true
		}
		if issue.RuleID == "design/component-missing-update" {
			hasUpdate = true
		}
	}
	if !hasInit {
		t.Error("expected missing Init issue")
	}
	if !hasUpdate {
		t.Error("expected missing Update issue")
	}
}

func TestValidateComponentAPI_Complete_NoIssue(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "model.go")
	os.WriteFile(f, []byte(`package ui

import tea "github.com/charmbracelet/bubbletea"

type Model struct{ width int }

func (m Model) Init() tea.Cmd { return nil }
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case tea.WindowSizeMsg:
		return m, nil
	}
	return m, nil
}
func (m Model) View() string { return "" }
`), 0o644)

	issues := validateComponentAPI([]string{f})
	if len(issues) != 0 {
		t.Errorf("expected no issues for complete component, got %d: %+v", len(issues), issues)
	}
}

func TestValidateDesignConsistency_MagicNumber(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "style.go")
	os.WriteFile(f, []byte(`package ui

var style = lipgloss.NewStyle().Padding(1, 2).MarginLeft(4)
`), 0o644)

	issues := validateDesignConsistency([]string{f})
	if len(issues) == 0 {
		t.Fatal("expected design consistency issues for magic numbers")
	}
	if issues[0].RuleID != "design/consistency-magic-spacing" {
		t.Errorf("issue rule = %q, want design/consistency-magic-spacing", issues[0].RuleID)
	}
}

func TestValidateAccessibility_LowContrast(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "colors.go")
	// Near-white on white: extremely low contrast.
	os.WriteFile(f, []byte(`package ui

var s = lipgloss.NewStyle().Foreground("#FEFEFE").Background("#FFFFFF")
`), 0o644)

	issues := validateAccessibility([]string{f})
	if len(issues) == 0 {
		t.Fatal("expected accessibility issue for low contrast")
	}
	if issues[0].RuleID != "design/a11y-contrast-ratio" {
		t.Errorf("issue rule = %q, want design/a11y-contrast-ratio", issues[0].RuleID)
	}
}

func TestContrastRatio_BlackOnWhite(t *testing.T) {
	ratio := contrastRatio("#000000", "#FFFFFF")
	if ratio < 20.0 {
		t.Errorf("black on white ratio = %f, want >= 21:1", ratio)
	}
}

func TestContrastRatio_SameColor(t *testing.T) {
	ratio := contrastRatio("#888888", "#888888")
	if math.Abs(ratio-1.0) > 0.01 {
		t.Errorf("same color ratio = %f, want ~1.0", ratio)
	}
}

func TestValidationDomainFromWorkerType(t *testing.T) {
	if ValidationDomainFromWorkerType("designer") != DomainDesign {
		t.Error("expected DomainDesign for designer")
	}
	if ValidationDomainFromWorkerType("engineer") != DomainCode {
		t.Error("expected DomainCode for engineer")
	}
	if ValidationDomainFromWorkerType("") != DomainCode {
		t.Error("expected DomainCode for empty")
	}
}
