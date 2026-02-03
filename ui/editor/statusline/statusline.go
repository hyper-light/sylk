// Package statusline renders the editor's status bar showing mode, file
// information, and cursor position.
package statusline

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/ui/editor/mode"
	"github.com/adalundhe/sylk/ui/theme"
)

// StatusLine holds the state displayed in the editor's bottom status bar.
type StatusLine struct {
	theme      *theme.Theme
	mode       mode.Mode
	fileName   string
	modified   bool
	line       int
	col        int
	totalLines int
	fileType   string
	encoding   string
}

// New creates a StatusLine with sensible defaults.
func New(th *theme.Theme) *StatusLine {
	return &StatusLine{
		theme:    th,
		encoding: "utf-8",
	}
}

// SetMode updates the displayed mode.
func (s *StatusLine) SetMode(m mode.Mode) { s.mode = m }

// SetFile updates the displayed file name and type.
func (s *StatusLine) SetFile(name, fileType string) {
	s.fileName = name
	s.fileType = fileType
}

// SetPosition updates the displayed cursor position.
func (s *StatusLine) SetPosition(line, col, total int) {
	s.line = line
	s.col = col
	s.totalLines = total
}

// SetModified updates the modification indicator.
func (s *StatusLine) SetModified(m bool) { s.modified = m }

// View renders the status line to fill the given width.
func (s *StatusLine) View(width int) string {
	left := s.renderLeft()
	right := s.renderRight()
	leftLen := lipgloss.Width(left)
	rightLen := lipgloss.Width(right)
	pad := max(width-leftLen-rightLen, 0)
	return s.theme.StatusBar.
		Width(width).
		Render(left + strings.Repeat(" ", pad) + right)
}

// ---------------------------------------------------------------------------
// Section renderers
// ---------------------------------------------------------------------------

// renderLeft produces the left portion: [MODE] filename [+]
func (s *StatusLine) renderLeft() string {
	badge := s.modeBadge()
	name := s.fileName
	if name == "" {
		name = "[No Name]"
	}
	mod := ""
	if s.modified {
		mod = " [+]"
	}
	return fmt.Sprintf(" %s %s%s ", badge, name, mod)
}

// renderRight produces the right portion: filetype | encoding | line:col/total
func (s *StatusLine) renderRight() string {
	ft := s.fileType
	if ft == "" {
		ft = "plain"
	}
	return fmt.Sprintf(" %s | %s | %d:%d/%d ",
		ft, s.encoding,
		s.line+1, s.col+1, s.totalLines)
}

// modeBadge returns the styled mode indicator.
func (s *StatusLine) modeBadge() string {
	style := s.modeStyle()
	name := mode.ModeName(s.mode)
	return style.Render(fmt.Sprintf(" %s ", name))
}

// modeStyleTable maps mode categories to theme style selectors.
var modeStyleTable = map[mode.Mode]func(th *theme.Theme) lipgloss.Style{
	mode.ModeNormal:      normalStyle,
	mode.ModeInsert:      insertStyle,
	mode.ModeVisual:      visualStyle,
	mode.ModeVisualLine:  visualStyle,
	mode.ModeVisualBlock: visualStyle,
	mode.ModeReplace:     replaceStyle,
	mode.ModeCmdline:     cmdlineStyle,
}

// modeStyle returns the lipgloss style for the current mode.
func (s *StatusLine) modeStyle() lipgloss.Style {
	fn, ok := modeStyleTable[s.mode]
	if !ok {
		return s.theme.StatusNormal
	}
	return fn(s.theme)
}

func normalStyle(th *theme.Theme) lipgloss.Style  { return th.StatusNormal }
func insertStyle(th *theme.Theme) lipgloss.Style   { return th.StatusWarning }
func visualStyle(th *theme.Theme) lipgloss.Style   { return th.StatusWarning }
func replaceStyle(th *theme.Theme) lipgloss.Style  { return th.StatusError }
func cmdlineStyle(th *theme.Theme) lipgloss.Style  { return th.StatusNormal }
