package status

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// tokenSymbol is the tau prefix rendered before token counts.
const tokenSymbol = "\u03C4"

// kThreshold is the count at which tokens are displayed in "k" notation.
// Derived from the smallest order of magnitude where abbreviation saves space.
const kThreshold = 1000

// TokenDisplay formats prompt and completion token counts for the status bar.
type TokenDisplay struct {
	promptTokens     int
	completionTokens int
	totalTokens      int
	style            lipgloss.Style
}

// NewTokenDisplay creates a TokenDisplay with the given lipgloss style.
func NewTokenDisplay(style lipgloss.Style) *TokenDisplay {
	return &TokenDisplay{
		style: style,
	}
}

// Update sets the prompt and completion token counts and recomputes the total.
func (td *TokenDisplay) Update(prompt, completion int) {
	td.promptTokens = prompt
	td.completionTokens = completion
	td.totalTokens = prompt + completion
}

// View renders the token display as "tau prompt/completion" with compact notation.
func (td *TokenDisplay) View() string {
	prompt := formatTokenCount(td.promptTokens)
	completion := formatTokenCount(td.completionTokens)
	return td.style.Render(fmt.Sprintf("%s %s/%s", tokenSymbol, prompt, completion))
}

// formatTokenCount renders a token count using compact "k" notation when the
// value reaches kThreshold, or as a plain integer otherwise.
func formatTokenCount(n int) string {
	if n < kThreshold {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/float64(kThreshold))
}
