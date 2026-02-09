package markdown

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/theme"
)

// Panel is a scrollable read-only viewer for rendered markdown content.
type Panel struct {
	content       string   // raw markdown source
	renderedLines []string // output of RenderMarkdown
	filePath      string
	scrollOff     int
	width         int
	height        int
	focused       bool
	focusID       component.FocusID
	theme         *theme.Theme
}

// Compile-time interface checks.
var (
	_ component.Focusable = (*Panel)(nil)
	_ component.Resizable = (*Panel)(nil)
	_ component.Component = (*Panel)(nil)
)

// New creates a Panel with the given theme.
func New(th *theme.Theme) *Panel {
	return &Panel{theme: th}
}

// ---------------------------------------------------------------------------
// component.Focusable
// ---------------------------------------------------------------------------

// ID returns the dynamic focus ID assigned to this panel.
func (p *Panel) ID() component.FocusID { return p.focusID }

// SetFocusID assigns the dynamic focus ID for pane integration.
func (p *Panel) SetFocusID(id component.FocusID) { p.focusID = id }

// Focused reports whether this panel has focus.
func (p *Panel) Focused() bool { return p.focused }

// SetFocused sets the focus state.
func (p *Panel) SetFocused(focused bool) { p.focused = focused }

// ---------------------------------------------------------------------------
// component.Resizable
// ---------------------------------------------------------------------------

// SetSize updates dimensions and re-renders if the width changed.
func (p *Panel) SetSize(w, h int) {
	widthChanged := w != p.width
	p.width = w
	p.height = h
	if widthChanged && p.content != "" {
		p.renderedLines = RenderMarkdown(p.content, w, p.theme)
		p.clampScroll()
	}
}

// ---------------------------------------------------------------------------
// component.Component
// ---------------------------------------------------------------------------

// Init returns nil (no initial command).
func (p *Panel) Init() tea.Cmd { return nil }

// HandleTick is retained for interface compatibility.
func (p *Panel) HandleTick(_ time.Time) {}

// Update processes key messages for vim-style navigation.
func (p *Panel) Update(msg tea.Msg) (component.Component, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}
	switch km.String() {
	case "j", "down":
		p.scrollDown(1)
	case "k", "up":
		p.scrollUp(1)
	case "g":
		p.scrollOff = 0
	case "G":
		p.scrollToEnd()
	case "ctrl+d":
		p.scrollDown(max(p.height/2, 1))
	case "ctrl+u":
		p.scrollUp(max(p.height/2, 1))
	case "pgdown":
		p.scrollDown(p.height)
	case "pgup":
		p.scrollUp(p.height)
	}
	return p, nil
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// SetContent sets the raw markdown, renders it, and resets scroll.
func (p *Panel) SetContent(raw, path string) {
	p.content = raw
	p.filePath = path
	p.scrollOff = 0
	if p.width > 0 {
		p.renderedLines = RenderMarkdown(raw, p.width, p.theme)
	}
}

// ClearFile resets the panel to an empty state.
func (p *Panel) ClearFile() {
	p.content = ""
	p.filePath = ""
	p.renderedLines = nil
	p.scrollOff = 0
}

// FilePath returns the path of the currently rendered file.
func (p *Panel) FilePath() string { return p.filePath }

// ScrollUp scrolls up by n lines. Returns true if the offset changed.
func (p *Panel) ScrollUp(n int) bool {
	prev := p.scrollOff
	p.scrollUp(n)
	return p.scrollOff != prev
}

// ScrollDown scrolls down by n lines. Returns true if the offset changed.
func (p *Panel) ScrollDown(n int) bool {
	prev := p.scrollOff
	p.scrollDown(n)
	return p.scrollOff != prev
}

// View renders the visible portion of the rendered markdown.
func (p *Panel) View() string {
	if len(p.renderedLines) == 0 {
		return p.renderPlaceholder()
	}

	total := len(p.renderedLines)
	visEnd := min(p.scrollOff+p.height, total)
	result := make([]string, 0, p.height)

	for i := p.scrollOff; i < visEnd; i++ {
		line := p.renderedLines[i]
		visWidth := lipgloss.Width(line)
		switch {
		case visWidth > p.width && p.width > 0:
			line = truncateStyled(line, p.width)
		case visWidth < p.width:
			line += strings.Repeat(" ", p.width-visWidth)
		}
		result = append(result, line)
	}

	// Pad remaining viewport with tilde lines.
	tildeStyle := lipgloss.NewStyle().Foreground(p.theme.Palette.Muted)
	for len(result) < p.height {
		tilde := tildeStyle.Render("~")
		pad := strings.Repeat(" ", max(p.width-1, 0))
		result = append(result, tilde+pad)
	}

	return strings.Join(result, "\n")
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func (p *Panel) scrollUp(n int)   { p.scrollOff = max(p.scrollOff-n, 0) }
func (p *Panel) scrollDown(n int) { p.scrollOff = min(p.scrollOff+n, p.maxScroll()) }
func (p *Panel) scrollToEnd()     { p.scrollOff = p.maxScroll() }

func (p *Panel) maxScroll() int { return max(len(p.renderedLines)-p.height, 0) }

func (p *Panel) clampScroll() {
	p.scrollOff = min(p.scrollOff, p.maxScroll())
	p.scrollOff = max(p.scrollOff, 0)
}

func (p *Panel) renderPlaceholder() string {
	lines := make([]string, p.height)
	pad := strings.Repeat(" ", max(p.width, 0))
	for i := range lines {
		lines[i] = pad
	}
	return strings.Join(lines, "\n")
}

