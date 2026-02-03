package input

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// CompletionProvider supplies tab-completion candidates for a given prefix.
type CompletionProvider interface {
	Name() string
	Complete(prefix string, limit int) []Candidate
}

// Candidate represents a single tab-completion suggestion.
type Candidate struct {
	Text        string // The completion text
	Display     string // What to show in the menu
	Description string // Brief description
	Category    string // For grouping (e.g., "agent", "command", "file")
}

// Completer provides tab completion over a set of CompletionProviders.
type Completer struct {
	providers  []CompletionProvider
	active     bool
	candidates []Candidate
	selected   int
	prefix     string
}

// completionLimit caps the number of candidates shown in the popup.
const completionLimit = 20

// NewCompleter creates a Completer backed by the given providers.
func NewCompleter(providers ...CompletionProvider) *Completer {
	return &Completer{
		providers: providers,
	}
}

// Trigger gathers candidates for the word under the cursor.
// It activates the popup when at least one candidate is found.
func (c *Completer) Trigger(text string, cursorPos int) {
	c.prefix = wordBeforeCursor(text, cursorPos)
	c.candidates = c.gather(c.prefix)
	c.selected = 0
	c.active = len(c.candidates) > 0
}

// Next moves the selection to the next candidate, wrapping around.
func (c *Completer) Next() {
	if len(c.candidates) == 0 {
		return
	}
	c.selected = (c.selected + 1) % len(c.candidates)
}

// Previous moves the selection to the previous candidate, wrapping around.
func (c *Completer) Previous() {
	if len(c.candidates) == 0 {
		return
	}
	c.selected = (c.selected - 1 + len(c.candidates)) % len(c.candidates)
}

// Accept returns the accepted completion text and dismisses the popup.
// Returns empty string when there are no candidates.
func (c *Completer) Accept() string {
	if len(c.candidates) == 0 {
		c.Dismiss()
		return ""
	}
	text := c.candidates[c.selected].Text
	c.Dismiss()
	return text
}

// Dismiss closes the completion popup.
func (c *Completer) Dismiss() {
	c.active = false
	c.candidates = nil
	c.selected = 0
	c.prefix = ""
}

// IsActive reports whether the completion popup is shown.
func (c *Completer) IsActive() bool {
	return c.active
}

// View renders the completion popup as a styled string of the given width.
// Returns an empty string when the completer is not active.
func (c *Completer) View(width int) string {
	if !c.active || len(c.candidates) == 0 {
		return ""
	}

	menuWidth := min(width, 60)
	normal := lipgloss.NewStyle().Width(menuWidth)
	highlight := lipgloss.NewStyle().Width(menuWidth).Reverse(true)

	var b strings.Builder
	for i, cand := range c.candidates {
		line := formatCandidate(cand, menuWidth)
		style := normal
		if i == c.selected {
			style = highlight
		}
		b.WriteString(style.Render(line))
		if i < len(c.candidates)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// gather collects candidates from all providers for the given prefix.
func (c *Completer) gather(prefix string) []Candidate {
	var out []Candidate
	remaining := completionLimit
	for _, p := range c.providers {
		if remaining <= 0 {
			break
		}
		candidates := p.Complete(prefix, remaining)
		out = append(out, candidates...)
		remaining -= len(candidates)
	}
	if len(out) > completionLimit {
		out = out[:completionLimit]
	}
	return out
}

// formatCandidate formats a candidate into a display line fitting the given width.
func formatCandidate(cand Candidate, width int) string {
	display := cand.Display
	if display == "" {
		display = cand.Text
	}
	if cand.Description == "" {
		return truncate(display, width)
	}
	descMax := width - len(display) - 3 // "  " separator + minimum 1 char desc
	if descMax <= 0 {
		return truncate(display, width)
	}
	desc := truncate(cand.Description, descMax)
	padding := width - len(display) - len(desc)
	if padding < 2 {
		padding = 2
	}
	return display + strings.Repeat(" ", padding) + desc
}

// wordBeforeCursor extracts the word fragment immediately before cursorPos.
func wordBeforeCursor(text string, cursorPos int) string {
	cursorPos = min(cursorPos, len(text))
	end := cursorPos
	start := end
	for start > 0 && !isWordBreak(text[start-1]) {
		start--
	}
	return text[start:end]
}

// isWordBreak reports whether b is a word-boundary byte for completion purposes.
func isWordBreak(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n'
}

// truncate shortens s to at most maxLen runes.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
