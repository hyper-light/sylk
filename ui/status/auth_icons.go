package status

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// providerCount is the number of LLM providers displayed.
const providerCount = 3

// providerIcon holds the display configuration for a single provider.
type providerIcon struct {
	name     string // "google", "anthropic", "openai"
	nerd     string // Nerd Font glyph
	fallback string // plain ASCII letter
	ok       bool   // credential available
}

// AuthIconDisplay renders fixed-width provider auth status icons.
// Each icon occupies a 2-char cell (glyph+space when authed, glyph+! when not).
// Icons are separated by a single styled space. Total section width is always
// providerCount*2 + (providerCount-1) = 8 chars.
type AuthIconDisplay struct {
	providers [providerCount]providerIcon
	nerdFonts bool
	gapStyle  lipgloss.Style // status bar base style — used for inter-icon space
	okStyle   lipgloss.Style
	errStyle  lipgloss.Style
}

// NewAuthIconDisplay creates an AuthIconDisplay with the three known providers.
func NewAuthIconDisplay(gapStyle, okStyle, errStyle lipgloss.Style) *AuthIconDisplay {
	return &AuthIconDisplay{
		providers: [providerCount]providerIcon{
			{name: "google", nerd: "\U000F02AD", fallback: "G"},
			{name: "anthropic", nerd: "\U0001D538", fallback: "A"},
			{name: "openai", nerd: "\U0001D546", fallback: "O"},
		},
		gapStyle: gapStyle,
		okStyle:  okStyle,
		errStyle: errStyle,
	}
}

// SetAvailable updates the auth state for the named provider.
func (d *AuthIconDisplay) SetAvailable(provider string, available bool) {
	lower := strings.ToLower(provider)
	for i := range d.providers {
		if d.providers[i].name == lower {
			d.providers[i].ok = available
			return
		}
	}
}

// SetNerdFonts toggles between Nerd Font glyphs and ASCII fallback letters.
func (d *AuthIconDisplay) SetNerdFonts(detected bool) {
	d.nerdFonts = detected
}

// View renders the icon section as three 2-char cells separated by styled spaces.
// Every byte of output passes through Style.Render, matching TokenDisplay's
// pattern so the compositor and bubbletea renderer handle it identically.
func (d *AuthIconDisplay) View() string {
	gap := d.gapStyle.Render(" ")
	var b strings.Builder
	for i := range d.providers {
		if i > 0 {
			b.WriteString(gap)
		}
		p := &d.providers[i]

		glyph := p.fallback
		if d.nerdFonts {
			glyph = p.nerd
		}

		if p.ok {
			b.WriteString(d.okStyle.Render(glyph + " "))
		} else {
			b.WriteString(d.errStyle.Render(glyph + "!"))
		}
	}
	return b.String()
}

// WidthCorrection returns the number of extra terminal cells the auth section
// occupies beyond what go-runewidth measures. Nerd Font glyphs in the Private
// Use Area (PUA) render as double-width in most terminals, but go-runewidth
// reports them as width 1. This correction lets the status bar padding
// calculation account for the real rendered width.
func (d *AuthIconDisplay) WidthCorrection() int {
	if !d.nerdFonts {
		return 0
	}
	correction := 0
	for i := range d.providers {
		for _, r := range d.providers[i].nerd {
			if isPUA(r) {
				correction++
			}
		}
	}
	return correction
}

// isPUA reports whether a rune falls in a Unicode Private Use Area block.
// PUA glyphs (used by Nerd Fonts) typically render as double-width in
// terminals but are measured as single-width by go-runewidth.
func isPUA(r rune) bool {
	return (r >= 0xE000 && r <= 0xF8FF) ||
		(r >= 0xF0000 && r <= 0xFFFFD) ||
		(r >= 0x100000 && r <= 0x10FFFD)
}
