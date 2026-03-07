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


// TokenPhase indicates which token counter is actively updating.
type TokenPhase int

const (
	PhaseIdle   TokenPhase = iota // Neither side counting.
	PhaseInput                    // Prompt tokens actively counting.
	PhaseOutput                   // Completion tokens actively counting.
)

// TokenDisplay formats prompt and completion token counts for the status bar.
// Each side highlights independently with a spinner when its phase is active.
type TokenDisplay struct {
	promptTokens     int
	completionTokens int
	cacheReadTokens  int
	reasoningTokens  int
	phase            TokenPhase
	spinner          *Spinner
	idleStyle        lipgloss.Style
	activeStyle      lipgloss.Style
}

// NewTokenDisplay creates a TokenDisplay with idle and active styles.
func NewTokenDisplay(idle, active lipgloss.Style) *TokenDisplay {
	return &TokenDisplay{
		spinner:     NewSpinner(),
		idleStyle:   idle,
		activeStyle: active,
	}
}

// Update sets the prompt, completion, cache-read, and reasoning token counts.
func (td *TokenDisplay) Update(prompt, completion, cacheRead, reasoning int) {
	td.promptTokens = prompt
	td.completionTokens = completion
	td.cacheReadTokens = cacheRead
	td.reasoningTokens = reasoning
}

// SetPhase sets which token counter is actively updating.
func (td *TokenDisplay) SetPhase(phase TokenPhase) {
	if phase != PhaseIdle && td.phase == PhaseIdle {
		td.spinner.Reset()
	}
	td.phase = phase
}

// Tick advances the counting spinner. Called by the status bar decor tick.
func (td *TokenDisplay) Tick() {
	if td.phase != PhaseIdle {
		td.spinner.Tick()
	}
}

// IsAnimating reports whether the token display has an active counting phase.
func (td *TokenDisplay) IsAnimating() bool {
	return td.phase != PhaseIdle
}

// View renders the token display as "Sτ ↓in/↑out". Input (prompt) tokens
// are shown net of cache hits; output (completion) tokens point upward.
func (td *TokenDisplay) View() string {
	netInput := td.promptTokens - td.cacheReadTokens
	if netInput < 0 {
		netInput = 0
	}
	in := formatTokenCount(netInput)
	out := formatTokenCount(td.completionTokens)

	spin := " "
	inStyle := td.idleStyle
	outStyle := td.idleStyle

	switch td.phase {
	case PhaseInput:
		inStyle = td.activeStyle
		spin = td.spinner.Current()
	case PhaseOutput:
		outStyle = td.activeStyle
		spin = td.spinner.Current()
	}

	spinStyle := td.idleStyle
	if td.phase != PhaseIdle {
		spinStyle = td.activeStyle
	}
	spinPart := spinStyle.Render(spin)
	gap := td.idleStyle.Render(" ")
	sym := td.idleStyle.Render(tokenSymbol + " ")
	sep := td.idleStyle.Render("/")
	inPart := inStyle.Render("↓" + in)
	outPart := outStyle.Render("↑" + out)

	return spinPart + gap + sym + inPart + sep + outPart
}

// formatTokenCount renders a token count using compact "k" notation when the
// value reaches kThreshold, or as a plain integer otherwise.
func formatTokenCount(n int) string {
	if n < kThreshold {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/float64(kThreshold))
}
