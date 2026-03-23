package chat

import (
	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// chatMdStyles holds all lipgloss styles used by the markdown renderer.
// Constructed once per render call from the theme and base message style.
type chatMdStyles struct {
	text           lipgloss.Style    // Base prose (inherited from ChatSource).
	code           lipgloss.Style    // Inline code spans.
	bold           lipgloss.Style    // **bold**
	italic         lipgloss.Style    // *italic*
	boldItalic     lipgloss.Style    // ***bold italic***
	link           lipgloss.Style    // [text](url)
	strike         lipgloss.Style    // ~~strikethrough~~
	heading        [6]lipgloss.Style // H1–H6
	h1Rule         lipgloss.Style    // ═ underline for H1
	h2Rule         lipgloss.Style    // ─ underline for H2
	bullet         lipgloss.Style    // List bullet/number
	priorityMust   lipgloss.Style    // [must]
	priorityShould lipgloss.Style    // [should]
	priorityCould  lipgloss.Style    // [could]
	quoteBar       lipgloss.Style    // │ bar
	quoteTx        lipgloss.Style    // Blockquote text
	sep            lipgloss.Style    // Thematic break ─
	tableHeader    lipgloss.Style    // Header row cells
	tableBorder    lipgloss.Style    // Table separators │ ─
}

// newChatMdStyles constructs markdown styles from a theme and the base
// message style for the current ChatSource.
func newChatMdStyles(th *theme.Theme, base lipgloss.Style) *chatMdStyles {
	p := th.Palette

	fg := base.GetForeground()

	s := &chatMdStyles{
		text: base,

		code: lipgloss.NewStyle().
			Foreground(p.Primary).
			Background(p.Subtle),

		bold: lipgloss.NewStyle().
			Foreground(fg).
			Bold(true),

		italic: lipgloss.NewStyle().
			Foreground(fg).
			Italic(true),

		boldItalic: lipgloss.NewStyle().
			Foreground(fg).
			Bold(true).
			Italic(true),

		link: lipgloss.NewStyle().
			Foreground(p.Info).
			Underline(true),

		strike: lipgloss.NewStyle().
			Foreground(p.Muted).
			Strikethrough(true),

		h1Rule: lipgloss.NewStyle().
			Foreground(p.Primary),

		h2Rule: lipgloss.NewStyle().
			Foreground(p.Border),

		bullet: lipgloss.NewStyle().
			Foreground(p.Primary),

		priorityMust: lipgloss.NewStyle().
			Foreground(p.Background).
			Background(p.Error).
			Bold(true).
			Padding(0, 1),

		priorityShould: lipgloss.NewStyle().
			Foreground(p.Background).
			Background(p.Warning).
			Bold(true).
			Padding(0, 1),

		priorityCould: lipgloss.NewStyle().
			Foreground(p.Background).
			Background(p.Info).
			Bold(true).
			Padding(0, 1),

		quoteBar: lipgloss.NewStyle().
			Foreground(p.Border),

		quoteTx: lipgloss.NewStyle().
			Foreground(p.Subtext).
			Italic(true),

		sep: lipgloss.NewStyle().
			Foreground(p.Border),

		tableHeader: lipgloss.NewStyle().
			Foreground(fg).
			Bold(true),

		tableBorder: lipgloss.NewStyle().
			Foreground(p.Border),
	}

	// Heading styles: H1 is primary+bold, H2 is secondary+bold,
	// H3–H6 progressively dimmer.
	headingColors := [6]lipgloss.Color{
		p.Primary,    // H1
		p.Secondary,  // H2
		p.Accent,     // H3
		p.Foreground, // H4
		p.Subtext,    // H5
		p.Muted,      // H6
	}
	for i := range 6 {
		s.heading[i] = lipgloss.NewStyle().
			Foreground(headingColors[i]).
			Bold(true)
	}

	return s
}
