// Package findbar provides an interactive in-file search bar for the editor.
package findbar

import (
	"regexp"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/ui/editor/search"
	"github.com/adalundhe/sylk/ui/theme"
)

// Action describes the result of a key event processed by the find bar.
type Action int

const (
	ActionNone         Action = iota
	ActionClose                       // Esc pressed — caller should close.
	ActionNextMatch                   // Enter — jump to next match.
	ActionPrevMatch                   // Shift+Enter — jump to previous match.
	ActionQueryChanged                // Query or toggles changed — caller should rehighlight.
)

// barFocus tracks which element of the find bar has keyboard focus.
type barFocus int

const (
	focusQuery   barFocus = iota // The text input line.
	focusToggles                 // The toggle badge row.
)

// toggleIndex identifies a specific toggle badge.
type toggleIndex int

const (
	toggleCase toggleIndex = iota // [Aa] case sensitivity.
	toggleWord                    // [ab] whole word.
	toggleRx                      // [.*] regex mode.
	toggleSel                     // [Sel] find in selection.
)

// toggleCount is the number of toggle badges.
// Derived from: the four toggleIndex values above.
const toggleCount = 4

// badgeLabels maps each toggle index to its display string.
// Derived from: the four toggle indicators.
var badgeLabels = [toggleCount]string{"Aa", "ab", ".*", "Sel"}

// barHeight is the number of terminal lines the find bar occupies
// (label row + divider row), matching the chord hint overlay.
const barHeight = 2

// badgePos records the display-column range of a rendered badge.
type badgePos struct {
	start, end int
}

// FindBar is an interactive in-file search bar.
type FindBar struct {
	query  []rune
	cursor int

	focus        barFocus
	toggleActive [toggleCount]bool
	toggleCursor toggleIndex

	// Selection snapshot captured when the bar opened.
	selStart int
	selEnd   int
	hasSel   bool

	// Compiled pattern and match state.
	compiled    *regexp.Regexp
	matches     []search.MatchRange
	matchIndex  int
	matchCount  string // "n/N" display cache

	// Badge display positions (recomputed each View call).
	badgeAbsPos [toggleCount]badgePos
}

// New creates a FindBar. If the editor has an active selection, selStart/selEnd
// are the rune offsets and hasSel is true; the "find in selection" toggle is
// auto-enabled.
func New(selStart, selEnd int, hasSel bool) *FindBar {
	fb := &FindBar{
		selStart: selStart,
		selEnd:   selEnd,
		hasSel:   hasSel,
	}
	if hasSel {
		fb.toggleActive[toggleSel] = true
	}
	return fb
}

// Height returns the number of terminal lines consumed by the bar.
func (f *FindBar) Height() int { return barHeight }

// Matches returns the current set of search matches.
func (f *FindBar) Matches() []search.MatchRange { return f.matches }

// CurrentMatch returns the match at the current navigation index.
func (f *FindBar) CurrentMatch() (search.MatchRange, bool) {
	if len(f.matches) == 0 {
		return search.MatchRange{}, false
	}
	return f.matches[f.matchIndex], true
}

// MatchIndex returns the 0-based index of the current match.
func (f *FindBar) MatchIndex() int { return f.matchIndex }

// Compiled returns the compiled regex, or nil if none.
func (f *FindBar) Compiled() *regexp.Regexp { return f.compiled }

// FindInSelection reports whether the "Sel" toggle is active.
func (f *FindBar) FindInSelection() bool { return f.toggleActive[toggleSel] }

// SelectionRange returns the saved selection bounds.
func (f *FindBar) SelectionRange() (int, int) { return f.selStart, f.selEnd }

// ---------------------------------------------------------------------------
// Search execution
// ---------------------------------------------------------------------------

// Recompute compiles the current query and finds all matches within content.
// searchStart/searchEnd define the rune range to scan; pass (0, len(content))
// for full-file search.
func (f *FindBar) Recompute(content []rune, searchStart, searchEnd int) {
	f.compiled = f.compileQuery()
	if f.compiled == nil {
		f.matches = nil
		f.matchIndex = 0
		f.matchCount = ""
		return
	}
	f.matches = search.FindAllInRange(f.compiled, content, searchStart, searchEnd)
	f.matchIndex = min(f.matchIndex, max(len(f.matches)-1, 0))
	f.updateCountDisplay()
}

// AdvanceMatch moves to the next match, wrapping around.
func (f *FindBar) AdvanceMatch() {
	if len(f.matches) == 0 {
		return
	}
	f.matchIndex = (f.matchIndex + 1) % len(f.matches)
	f.updateCountDisplay()
}

// RetreatMatch moves to the previous match, wrapping around.
func (f *FindBar) RetreatMatch() {
	if len(f.matches) == 0 {
		return
	}
	f.matchIndex = (f.matchIndex - 1 + len(f.matches)) % len(f.matches)
	f.updateCountDisplay()
}

// NearestMatch sets the match index to the closest match at or after pos.
func (f *FindBar) NearestMatch(pos int) {
	for i, m := range f.matches {
		if m.Start >= pos {
			f.matchIndex = i
			f.updateCountDisplay()
			return
		}
	}
	f.matchIndex = 0
	f.updateCountDisplay()
}

func (f *FindBar) updateCountDisplay() {
	if len(f.matches) == 0 {
		f.matchCount = "0/0"
		return
	}
	f.matchCount = strconv.Itoa(f.matchIndex+1) + "/" + strconv.Itoa(len(f.matches))
}

// compileQuery builds a regex from the current query text and toggle state.
func (f *FindBar) compileQuery() *regexp.Regexp {
	if len(f.query) == 0 {
		return nil
	}
	pattern := string(f.query)
	if !f.toggleActive[toggleRx] {
		pattern = regexp.QuoteMeta(pattern)
	}
	if f.toggleActive[toggleWord] {
		pattern = `\b` + pattern + `\b`
	}
	if !f.toggleActive[toggleCase] {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil
	}
	return re
}

// ---------------------------------------------------------------------------
// Key handling
// ---------------------------------------------------------------------------

// HandleKey processes a key event and returns the resulting action.
func (f *FindBar) HandleKey(key tea.KeyMsg) Action {
	if f.focus == focusToggles {
		return f.handleToggleKey(key)
	}
	return f.handleQueryKey(key)
}

func (f *FindBar) handleQueryKey(key tea.KeyMsg) Action {
	switch key.Type {
	case tea.KeyEsc:
		return ActionClose
	case tea.KeyEnter:
		switch key.String() {
		case "shift+enter":
			return ActionPrevMatch
		case "ctrl+enter":
			return f.insertAtCursor('\n')
		default:
			return ActionNextMatch
		}
	case tea.KeyBackspace:
		if f.cursor > 0 {
			f.query = append(f.query[:f.cursor-1], f.query[f.cursor:]...)
			f.cursor--
			return ActionQueryChanged
		}
		return ActionNone
	case tea.KeyDelete:
		if f.cursor < len(f.query) {
			f.query = append(f.query[:f.cursor], f.query[f.cursor+1:]...)
			return ActionQueryChanged
		}
		return ActionNone
	case tea.KeyLeft:
		if f.cursor > 0 {
			f.cursor--
		}
		return ActionNone
	case tea.KeyRight:
		if f.cursor < len(f.query) {
			f.cursor++
		}
		return ActionNone
	case tea.KeyHome:
		f.cursor = 0
		return ActionNone
	case tea.KeyEnd:
		f.cursor = len(f.query)
		return ActionNone
	case tea.KeyTab:
		f.focus = focusToggles
		return ActionNone
	}
	// Ctrl+U clears the query.
	if key.String() == "ctrl+u" {
		f.query = f.query[:0]
		f.cursor = 0
		return ActionQueryChanged
	}
	// Shift+Enter via string match (some terminals).
	if key.String() == "shift+enter" {
		return ActionPrevMatch
	}
	// Printable rune insertion at cursor position.
	if len(key.Runes) > 0 {
		return f.insertRunesAtCursor(key.Runes)
	}
	return ActionNone
}

// insertAtCursor inserts a single rune at the cursor position.
func (f *FindBar) insertAtCursor(r rune) Action {
	return f.insertRunesAtCursor([]rune{r})
}

// insertRunesAtCursor splices runes into the query at the cursor position.
func (f *FindBar) insertRunesAtCursor(runes []rune) Action {
	tail := make([]rune, len(f.query)-f.cursor)
	copy(tail, f.query[f.cursor:])
	f.query = append(append(f.query[:f.cursor], runes...), tail...)
	f.cursor += len(runes)
	return ActionQueryChanged
}

func (f *FindBar) handleToggleKey(key tea.KeyMsg) Action {
	switch key.Type {
	case tea.KeyEsc:
		return ActionClose
	case tea.KeyTab:
		f.focus = focusQuery
		return ActionNone
	case tea.KeyLeft:
		f.toggleCursor = toggleIndex(max(int(f.toggleCursor)-1, 0))
		return ActionNone
	case tea.KeyRight:
		f.toggleCursor = toggleIndex(min(int(f.toggleCursor)+1, toggleCount-1))
		return ActionNone
	case tea.KeyEnter, tea.KeySpace:
		return f.activateToggle(f.toggleCursor)
	}
	return ActionNone
}

// HandleClick processes a mouse click at display column col on the label
// row (row 0). Returns ActionQueryChanged when a badge was toggled.
func (f *FindBar) HandleClick(col int) Action {
	for i := range toggleCount {
		bp := f.badgeAbsPos[i]
		if col >= bp.start && col < bp.end {
			f.focus = focusToggles
			f.toggleCursor = toggleIndex(i)
			return f.activateToggle(toggleIndex(i))
		}
	}
	// Click outside badges — focus the query input.
	f.focus = focusQuery
	return ActionNone
}

// activateToggle flips a toggle badge, respecting the Sel prerequisite.
func (f *FindBar) activateToggle(idx toggleIndex) Action {
	if idx == toggleSel && !f.hasSel {
		return ActionNone
	}
	f.toggleActive[idx] = !f.toggleActive[idx]
	return ActionQueryChanged
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// View renders the find bar as barHeight lines (label row + divider),
// matching the chord hint overlay style. cursorVisible controls the
// blinking cursor in the query input (pass the editor's blink state).
func (f *FindBar) View(width int, th *theme.Theme, cursorVisible bool) string {
	labelStyle := lipgloss.NewStyle().Foreground(th.Palette.Primary).Bold(true)
	queryStyle := lipgloss.NewStyle().Foreground(th.Palette.Foreground)
	countStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	cursorStyle := lipgloss.NewStyle().Reverse(true)

	// Right side: badges.
	badgesStr, badgesW := f.renderBadges(th)

	// Left side: "Find:" label + query + cursor + count.
	label := labelStyle.Render(" Find:")
	labelW := lipgloss.Width(label) + 1 // +1 for trailing space

	// End-of-query cursor needs an extra column; mid-query cursor
	// overlays the character at that position (no extra width).
	endCursorW := 0
	if f.cursor >= len(f.query) {
		endCursorW = 1
	}

	countStr := ""
	countW := 0
	if f.matchCount != "" {
		countStr = countStyle.Render(" " + f.matchCount)
		countW = lipgloss.Width(countStr)
	}

	// Visible window of the query that always includes the cursor.
	availQ := max(width-labelW-endCursorW-countW-badgesW-2, 0) // 2 = min gap
	qLen := len(f.query)
	visStart := 0
	if qLen > availQ {
		visStart = max(f.cursor-availQ+1, 0)
		visStart = min(visStart, qLen-availQ)
	}
	visEnd := min(visStart+availQ, qLen)

	// Build query display with cursor at the correct position.
	queryDisplay := f.renderQueryWithCursor(visStart, visEnd, queryStyle, cursorStyle, cursorVisible)

	leftPart := label + " " + queryDisplay + countStr
	leftW := lipgloss.Width(leftPart)

	// Position badges on the right; record absolute column offsets.
	gap := max(width-leftW-badgesW, 0)
	badgeAreaStart := leftW + gap
	for i := range toggleCount {
		f.badgeAbsPos[i].start += badgeAreaStart
		f.badgeAbsPos[i].end += badgeAreaStart
	}

	line := leftPart + strings.Repeat(" ", gap) + badgesStr
	lineW := lipgloss.Width(line)
	if pad := width - lineW; pad > 0 {
		line += strings.Repeat(" ", pad)
	}

	divider := lipgloss.NewStyle().
		Foreground(th.Palette.Border).
		Render(strings.Repeat("\u2500", width))

	return line + "\n" + divider
}

// displayRune maps control characters to visible glyphs for the find bar.
func displayRune(r rune) rune {
	switch r {
	case '\n':
		return '\u21b5' // ↵
	case '\t':
		return '\u2192' // →
	case '\r':
		return '\u240d' // ␍
	default:
		return r
	}
}

// displaySlice returns a display-safe copy of query[start:end] with
// control characters replaced by visible glyphs.
func displaySlice(query []rune, start, end int) string {
	buf := make([]rune, end-start)
	for i, r := range query[start:end] {
		buf[i] = displayRune(r)
	}
	return string(buf)
}

// renderQueryWithCursor renders the visible window [visStart, visEnd) of the
// query text with the blinking cursor embedded at f.cursor.
func (f *FindBar) renderQueryWithCursor(visStart, visEnd int, queryStyle, cursorStyle lipgloss.Style, cursorVisible bool) string {
	before := displaySlice(f.query, visStart, min(f.cursor, visEnd))

	var cursorPart string
	if f.cursor < len(f.query) {
		ch := string(displayRune(f.query[f.cursor]))
		if cursorVisible {
			cursorPart = cursorStyle.Render(ch)
		} else {
			cursorPart = queryStyle.Render(ch)
		}
	} else {
		if cursorVisible {
			cursorPart = cursorStyle.Render(" ")
		} else {
			cursorPart = " "
		}
	}

	afterStart := min(f.cursor+1, len(f.query))
	after := displaySlice(f.query, afterStart, visEnd)

	return queryStyle.Render(before) + cursorPart + queryStyle.Render(after)
}

// renderBadges builds the badge string and records each badge's relative
// column position (relative to the start of the badge area).
func (f *FindBar) renderBadges(th *theme.Theme) (string, int) {
	var b strings.Builder
	col := 0
	for i := range toggleCount {
		if i > 0 {
			b.WriteByte(' ')
			col++
		}
		f.badgeAbsPos[i] = badgePos{start: col, end: col} // relative, adjusted later
		badge := f.renderBadge(toggleIndex(i), th)
		bw := lipgloss.Width(badge)
		b.WriteString(badge)
		f.badgeAbsPos[i].end = col + bw
		col += bw
	}
	return b.String(), col
}

func (f *FindBar) renderBadge(idx toggleIndex, th *theme.Theme) string {
	fg := th.Palette.Muted
	if f.toggleActive[idx] {
		fg = th.Palette.Primary
	}
	// Dim the Sel badge when no selection was captured.
	if idx == toggleSel && !f.hasSel {
		fg = th.Palette.Subtle
	}
	style := lipgloss.NewStyle().Foreground(fg)
	if f.focus == focusToggles && f.toggleCursor == idx {
		style = style.Background(th.Palette.Selection)
	}
	return style.Render("[" + badgeLabels[idx] + "]")
}
