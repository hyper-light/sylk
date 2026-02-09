// Package knowledge provides the knowledge query panel for the Sylk TUI.
// It lets users construct hybrid queries combining text, semantic, and graph
// search modalities and browse the results.
package knowledge

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// ---------------------------------------------------------------------------
// Weight presets (table-driven)
// ---------------------------------------------------------------------------

// WeightPreset defines a named set of query weight configurations.
type WeightPreset struct {
	Name           string
	TextWeight     float64
	SemanticWeight float64
	GraphWeight    float64
}

// presetTable defines all available weight presets.
// Each preset's weights sum to 1.0.
var presetTable = []WeightPreset{
	{Name: "balanced", TextWeight: 0.34, SemanticWeight: 0.33, GraphWeight: 0.33},
	{Name: "semantic-heavy", TextWeight: 0.15, SemanticWeight: 0.70, GraphWeight: 0.15},
	{Name: "text-only", TextWeight: 1.0, SemanticWeight: 0.0, GraphWeight: 0.0},
	{Name: "semantic-only", TextWeight: 0.0, SemanticWeight: 1.0, GraphWeight: 0.0},
	{Name: "graph-only", TextWeight: 0.0, SemanticWeight: 0.0, GraphWeight: 1.0},
	{Name: "text-semantic", TextWeight: 0.50, SemanticWeight: 0.50, GraphWeight: 0.0},
}

// PresetCount returns the number of available presets.
func PresetCount() int {
	return len(presetTable)
}

// PresetAt returns the preset at the given index, wrapping around if needed.
func PresetAt(index int) WeightPreset {
	return presetTable[index%len(presetTable)]
}

// ---------------------------------------------------------------------------
// Weight slider rendering constants
// ---------------------------------------------------------------------------

// sliderWidth is the visual width of a weight slider bar in characters.
// Derived from: 20 characters provides ~5% granularity per cell.
const sliderWidth = 20

// weightLabelWidth is the column width reserved for weight labels.
// Derived from: longest label "Semantic" is 8 chars + padding.
const weightLabelWidth = 10

// ---------------------------------------------------------------------------
// QueryBuilder
// ---------------------------------------------------------------------------

// QueryBuilder holds the state for constructing a hybrid query via the TUI.
type QueryBuilder struct {
	// Query text entered by the user.
	QueryText string

	// Weights for each search modality.
	TextWeight     float64
	SemanticWeight float64
	GraphWeight    float64

	// Active preset index into presetTable.
	presetIndex int

	theme *theme.Theme
}

// NewQueryBuilder creates a QueryBuilder initialized to the "balanced" preset.
func NewQueryBuilder(th *theme.Theme) *QueryBuilder {
	preset := presetTable[0] // "balanced"
	return &QueryBuilder{
		TextWeight:     preset.TextWeight,
		SemanticWeight: preset.SemanticWeight,
		GraphWeight:    preset.GraphWeight,
		presetIndex:    0,
		theme:          th,
	}
}

// ApplyPreset sets weights to the preset at the given index.
func (qb *QueryBuilder) ApplyPreset(index int) {
	idx := index % len(presetTable)
	preset := presetTable[idx]
	qb.TextWeight = preset.TextWeight
	qb.SemanticWeight = preset.SemanticWeight
	qb.GraphWeight = preset.GraphWeight
	qb.presetIndex = idx
}

// NextPreset advances to the next preset.
func (qb *QueryBuilder) NextPreset() {
	qb.ApplyPreset(qb.presetIndex + 1)
}

// PrevPreset moves to the previous preset.
func (qb *QueryBuilder) PrevPreset() {
	idx := qb.presetIndex - 1
	if idx < 0 {
		idx = len(presetTable) - 1
	}
	qb.ApplyPreset(idx)
}

// CurrentPresetName returns the name of the currently active preset.
func (qb *QueryBuilder) CurrentPresetName() string {
	return presetTable[qb.presetIndex%len(presetTable)].Name
}

// SetQueryText updates the query text.
func (qb *QueryBuilder) SetQueryText(text string) {
	qb.QueryText = text
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// View renders the query builder showing the current query text and weight
// sliders.
func (qb *QueryBuilder) View(width int) string {
	var b strings.Builder

	// Query line.
	promptStyle := lipgloss.NewStyle().Foreground(qb.theme.Palette.Primary).Bold(true)
	queryStyle := lipgloss.NewStyle().Foreground(qb.theme.Palette.Foreground)

	b.WriteString(promptStyle.Render(theme.IconSearch+" Query: "))
	if qb.QueryText == "" {
		placeholderStyle := lipgloss.NewStyle().Foreground(qb.theme.Palette.Muted).Italic(true)
		b.WriteString(placeholderStyle.Render("Enter search query..."))
	} else {
		b.WriteString(queryStyle.Render(qb.QueryText))
	}
	b.WriteByte('\n')

	// Preset indicator.
	presetStyle := lipgloss.NewStyle().Foreground(qb.theme.Palette.Info)
	b.WriteString(presetStyle.Render(fmt.Sprintf("  Preset: %s", qb.CurrentPresetName())))
	b.WriteByte('\n')

	// Weight sliders.
	sliders := []struct {
		label  string
		weight float64
		color  lipgloss.Color
	}{
		{label: "Text", weight: qb.TextWeight, color: qb.theme.Palette.Success},
		{label: "Semantic", weight: qb.SemanticWeight, color: qb.theme.Palette.Primary},
		{label: "Graph", weight: qb.GraphWeight, color: qb.theme.Palette.Secondary},
	}

	for _, s := range sliders {
		b.WriteString(qb.renderSlider(s.label, s.weight, s.color, width))
		b.WriteByte('\n')
	}

	return b.String()
}

// renderSlider renders a single weight slider bar.
func (qb *QueryBuilder) renderSlider(label string, weight float64, color lipgloss.Color, _ int) string {
	labelStyle := lipgloss.NewStyle().Foreground(qb.theme.Palette.Foreground).Width(weightLabelWidth)
	filledStyle := lipgloss.NewStyle().Foreground(color)
	emptyStyle := lipgloss.NewStyle().Foreground(qb.theme.Palette.Subtle)
	pctStyle := lipgloss.NewStyle().Foreground(qb.theme.Palette.Muted)

	filled := int(weight * float64(sliderWidth))
	filled = min(filled, sliderWidth)
	empty := sliderWidth - filled

	bar := filledStyle.Render(strings.Repeat("█", filled)) +
		emptyStyle.Render(strings.Repeat("░", empty))

	pct := pctStyle.Render(fmt.Sprintf(" %3.0f%%", weight*100))

	return "  " + labelStyle.Render(label) + " " + bar + pct
}
