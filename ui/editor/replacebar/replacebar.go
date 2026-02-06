// Package replacebar provides an interactive find-and-replace bar for the
// editor. It is a fully standalone component with its own query input,
// toggle state, search execution, rendering, and key handling — no
// dependency on the findbar package.
package replacebar

import (
	"regexp"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/ui/editor/search"
	"github.com/adalundhe/sylk/ui/theme"
)

// Action describes the result of a key event processed by the replace bar.
type Action int

const (
	ActionNone         Action = iota
	ActionClose                      // Esc — caller should close.
	ActionNextMatch                  // Enter in find row — jump to next match.
	ActionPrevMatch                  // Shift+Enter in find row — jump to previous.
	ActionQueryChanged               // Query or toggles changed — caller should rehighlight.
	ActionReplaceOne                 // Replace current match, advance to next.
	ActionReplaceAll                 // Replace all matches at once.
)

// ---------------------------------------------------------------------------
// Focus system
// ---------------------------------------------------------------------------

// replaceFocus tracks which element of the replace bar has keyboard focus.
// Tab stops are ordered left-to-right, row by row:
//
//	Row 1: find input → [Aa] → [ab] → [.*] → [Sel]
//	Row 2: replace input → [→1] → [→*]
type replaceFocus int

const (
	focusFind      replaceFocus = iota // Find query input.
	focusToggleAa                      // [Aa] case sensitivity.
	focusToggleAb                      // [ab] whole word.
	focusToggleRx                      // [.*] regex mode.
	focusToggleSel                     // [Sel] find in selection.
	focusReplace                       // Replace text input.
	focusBtnOne                        // [→1] replace one.
	focusBtnAll                        // [→*] replace all.
)

// focusCount is the total number of tab stops.
// Derived from: the eight replaceFocus values above.
const focusCount = 8

// ---------------------------------------------------------------------------
// Toggle system
// ---------------------------------------------------------------------------

// toggleIndex identifies a specific toggle badge.
type toggleIndex int

const (
	toggleCase toggleIndex = iota // [Aa]
	toggleWord                    // [ab]
	toggleRx                      // [.*]
	toggleSel                     // [Sel]
)

// toggleCount is the number of toggle badges.
// Derived from: the four toggleIndex values above.
const toggleCount = 4

// badgeLabels maps each toggle index to its display string.
var badgeLabels = [toggleCount]string{"Aa", "ab", ".*", "Sel"}

// focusToToggle maps a toggle-focused replaceFocus to its toggleIndex.
var focusToToggle = [toggleCount]replaceFocus{
	focusToggleAa, focusToggleAb, focusToggleRx, focusToggleSel,
}

// ---------------------------------------------------------------------------
// Layout
// ---------------------------------------------------------------------------

// barHeight is the number of terminal lines the replace bar occupies.
// Derived from: find row(1) + replace row(1) + divider(1) = 3.
const barHeight = 3

// badgePos records the display-column range of a rendered badge or button.
type badgePos struct {
	start, end int
}

// ---------------------------------------------------------------------------
// ReplaceBar
// ---------------------------------------------------------------------------

// ReplaceBar is an interactive find-and-replace bar.
type ReplaceBar struct {
	// Find query state.
	query    []rune
	queryCur int

	// Replace text state.
	replace    []rune
	replaceCur int

	// Focus and toggles.
	focus        replaceFocus
	toggleActive [toggleCount]bool

	// Selection snapshot (for [Sel] toggle).
	selStart int
	selEnd   int
	hasSel   bool

	// Compiled pattern and match state.
	compiled   *regexp.Regexp
	matches    []search.MatchRange
	matchIndex int
	matchCount string // "n/N" display cache

	// Badge/button display positions (recomputed each View call).
	badgeAbsPos [toggleCount]badgePos
	btnOnePos   badgePos
	btnAllPos   badgePos
}

// New creates a ReplaceBar, pre-populating the search query from the given
// text. selStart/selEnd are inclusive rune offsets of any active selection;
// hasSel indicates whether these bounds are valid.
func New(query string, selStart, selEnd int, hasSel bool) *ReplaceBar {
	rb := &ReplaceBar{
		selStart: selStart,
		selEnd:   selEnd,
		hasSel:   hasSel,
	}
	if query != "" {
		rb.query = []rune(query)
		rb.queryCur = len(rb.query)
	}
	return rb
}

// HasSelectionRange reports whether valid selection bounds were captured
// when the bar was created (independent of whether [Sel] is toggled on).
func (r *ReplaceBar) HasSelectionRange() bool { return r.hasSel }

// ---------------------------------------------------------------------------
// Accessors
// ---------------------------------------------------------------------------

// Height returns the number of terminal lines consumed by the bar.
func (r *ReplaceBar) Height() int { return barHeight }

// Matches returns the current set of search matches.
func (r *ReplaceBar) Matches() []search.MatchRange { return r.matches }

// CurrentMatch returns the match at the current navigation index.
func (r *ReplaceBar) CurrentMatch() (search.MatchRange, bool) {
	if len(r.matches) == 0 {
		return search.MatchRange{}, false
	}
	return r.matches[r.matchIndex], true
}

// MatchIndex returns the 0-based index of the current match.
func (r *ReplaceBar) MatchIndex() int { return r.matchIndex }

// Compiled returns the compiled regex, or nil if none.
func (r *ReplaceBar) Compiled() *regexp.Regexp { return r.compiled }

// FindInSelection reports whether the "Sel" toggle is active.
func (r *ReplaceBar) FindInSelection() bool { return r.toggleActive[toggleSel] }

// EnableSelToggle turns the [Sel] toggle on (idempotent).
func (r *ReplaceBar) EnableSelToggle() { r.toggleActive[toggleSel] = true }

// DisableSelToggle turns the [Sel] toggle off (idempotent).
func (r *ReplaceBar) DisableSelToggle() { r.toggleActive[toggleSel] = false }

// SelectionRange returns the saved selection bounds.
func (r *ReplaceBar) SelectionRange() (int, int) { return r.selStart, r.selEnd }

// Replacement returns the replacement text.
func (r *ReplaceBar) Replacement() string { return string(r.replace) }

// ---------------------------------------------------------------------------
// Search execution
// ---------------------------------------------------------------------------

// Recompute compiles the current query and finds all matches within content.
func (r *ReplaceBar) Recompute(content []rune, searchStart, searchEnd int) {
	r.compiled = r.compileQuery()
	if r.compiled == nil {
		r.matches = nil
		r.matchIndex = 0
		r.matchCount = ""
		return
	}
	r.matches = search.FindAllInRange(r.compiled, content, searchStart, searchEnd)
	r.matchIndex = min(r.matchIndex, max(len(r.matches)-1, 0))
	r.updateCountDisplay()
}

// AdvanceMatch moves to the next match, wrapping around.
func (r *ReplaceBar) AdvanceMatch() {
	if len(r.matches) == 0 {
		return
	}
	r.matchIndex = (r.matchIndex + 1) % len(r.matches)
	r.updateCountDisplay()
}

// RetreatMatch moves to the previous match, wrapping around.
func (r *ReplaceBar) RetreatMatch() {
	if len(r.matches) == 0 {
		return
	}
	r.matchIndex = (r.matchIndex - 1 + len(r.matches)) % len(r.matches)
	r.updateCountDisplay()
}

// NearestMatch sets the match index to the closest match at or after pos.
func (r *ReplaceBar) NearestMatch(pos int) {
	for i, m := range r.matches {
		if m.Start >= pos {
			r.matchIndex = i
			r.updateCountDisplay()
			return
		}
	}
	r.matchIndex = 0
	r.updateCountDisplay()
}

func (r *ReplaceBar) updateCountDisplay() {
	if len(r.matches) == 0 {
		r.matchCount = "0/0"
		return
	}
	r.matchCount = strconv.Itoa(r.matchIndex+1) + "/" + strconv.Itoa(len(r.matches))
}

// compileQuery builds a regex from the current query text and toggle state.
func (r *ReplaceBar) compileQuery() *regexp.Regexp {
	if len(r.query) == 0 {
		return nil
	}
	pattern := string(r.query)
	if !r.toggleActive[toggleRx] {
		pattern = regexp.QuoteMeta(pattern)
	}
	if r.toggleActive[toggleWord] {
		pattern = `\b` + pattern + `\b`
	}
	if !r.toggleActive[toggleCase] {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil
	}
	return re
}

// ---------------------------------------------------------------------------
// Focus helpers
// ---------------------------------------------------------------------------

// isToggleFocus reports whether f is one of the toggle badge tab stops.
func isToggleFocus(f replaceFocus) bool {
	return f >= focusToggleAa && f <= focusToggleSel
}

// toggleIdx returns the toggleIndex for the current focus.
// Only meaningful when isToggleFocus(r.focus) is true.
func (r *ReplaceBar) toggleIdx() toggleIndex {
	return toggleIndex(r.focus - focusToggleAa)
}

// ---------------------------------------------------------------------------
// Key handling
// ---------------------------------------------------------------------------

// HandleKey processes a key event and returns the resulting action.
func (r *ReplaceBar) HandleKey(key tea.KeyMsg) Action {
	if key.Type == tea.KeyEsc {
		return ActionClose
	}
	if key.Type == tea.KeyTab {
		r.focus = replaceFocus((int(r.focus) + 1) % focusCount)
		return ActionNone
	}
	if key.String() == "shift+tab" {
		r.focus = replaceFocus((int(r.focus) + focusCount - 1) % focusCount)
		return ActionNone
	}
	return r.dispatchToFocus(key)
}

// dispatchToFocus routes key input to the handler for the active zone.
func (r *ReplaceBar) dispatchToFocus(key tea.KeyMsg) Action {
	switch {
	case r.focus == focusFind:
		return r.handleFindKey(key)
	case isToggleFocus(r.focus):
		return r.handleToggleKey(key)
	case r.focus == focusReplace:
		return r.handleReplaceKey(key)
	default:
		return r.handleBtnKey(key)
	}
}

// handleFindKey handles keys when the find query input has focus.
func (r *ReplaceBar) handleFindKey(key tea.KeyMsg) Action {
	switch key.Type {
	case tea.KeyEnter:
		switch key.String() {
		case "shift+enter":
			return ActionPrevMatch
		case "ctrl+enter":
			r.insertQueryRune('\n')
			return ActionQueryChanged
		default:
			return ActionNextMatch
		}
	case tea.KeyBackspace:
		if r.queryCur > 0 {
			r.query = append(r.query[:r.queryCur-1], r.query[r.queryCur:]...)
			r.queryCur--
			return ActionQueryChanged
		}
		return ActionNone
	case tea.KeyDelete:
		if r.queryCur < len(r.query) {
			r.query = append(r.query[:r.queryCur], r.query[r.queryCur+1:]...)
			return ActionQueryChanged
		}
		return ActionNone
	case tea.KeyLeft:
		r.queryCur = max(r.queryCur-1, 0)
		return ActionNone
	case tea.KeyRight:
		r.queryCur = min(r.queryCur+1, len(r.query))
		return ActionNone
	case tea.KeyHome:
		r.queryCur = 0
		return ActionNone
	case tea.KeyEnd:
		r.queryCur = len(r.query)
		return ActionNone
	}
	if key.String() == "ctrl+u" {
		r.query = r.query[:0]
		r.queryCur = 0
		return ActionQueryChanged
	}
	if len(key.Runes) > 0 {
		r.insertQueryRunes(key.Runes)
		return ActionQueryChanged
	}
	return ActionNone
}

// handleToggleKey handles keys when a toggle badge has focus.
func (r *ReplaceBar) handleToggleKey(key tea.KeyMsg) Action {
	switch key.Type {
	case tea.KeyEnter, tea.KeySpace:
		return r.activateToggle(r.toggleIdx())
	case tea.KeyLeft:
		if r.focus > focusToggleAa {
			r.focus--
		}
		return ActionNone
	case tea.KeyRight:
		if r.focus < focusToggleSel {
			r.focus++
		}
		return ActionNone
	}
	return ActionNone
}

// handleReplaceKey handles keys when the replacement text input has focus.
func (r *ReplaceBar) handleReplaceKey(key tea.KeyMsg) Action {
	switch key.Type {
	case tea.KeyEnter:
		if key.String() == "ctrl+enter" {
			return ActionReplaceAll
		}
		return ActionReplaceOne
	case tea.KeyBackspace:
		if r.replaceCur > 0 {
			r.replace = append(r.replace[:r.replaceCur-1], r.replace[r.replaceCur:]...)
			r.replaceCur--
		}
		return ActionNone
	case tea.KeyDelete:
		if r.replaceCur < len(r.replace) {
			r.replace = append(r.replace[:r.replaceCur], r.replace[r.replaceCur+1:]...)
		}
		return ActionNone
	case tea.KeyLeft:
		r.replaceCur = max(r.replaceCur-1, 0)
		return ActionNone
	case tea.KeyRight:
		r.replaceCur = min(r.replaceCur+1, len(r.replace))
		return ActionNone
	case tea.KeyHome:
		r.replaceCur = 0
		return ActionNone
	case tea.KeyEnd:
		r.replaceCur = len(r.replace)
		return ActionNone
	}
	if key.String() == "ctrl+u" {
		r.replace = r.replace[:0]
		r.replaceCur = 0
		return ActionNone
	}
	if len(key.Runes) > 0 {
		r.insertReplaceRunes(key.Runes)
		return ActionNone
	}
	return ActionNone
}

// handleBtnKey handles keys when a replace button has focus.
func (r *ReplaceBar) handleBtnKey(key tea.KeyMsg) Action {
	switch key.Type {
	case tea.KeyEnter, tea.KeySpace:
		if r.focus == focusBtnOne {
			return ActionReplaceOne
		}
		return ActionReplaceAll
	case tea.KeyLeft:
		if r.focus == focusBtnAll {
			r.focus = focusBtnOne
		}
		return ActionNone
	case tea.KeyRight:
		if r.focus == focusBtnOne {
			r.focus = focusBtnAll
		}
		return ActionNone
	}
	return ActionNone
}

// activateToggle flips a toggle badge.
func (r *ReplaceBar) activateToggle(idx toggleIndex) Action {
	r.toggleActive[idx] = !r.toggleActive[idx]
	return ActionQueryChanged
}

// ---------------------------------------------------------------------------
// Text insertion helpers
// ---------------------------------------------------------------------------

func (r *ReplaceBar) insertQueryRune(ch rune) {
	r.insertQueryRunes([]rune{ch})
}

func (r *ReplaceBar) insertQueryRunes(runes []rune) {
	tail := make([]rune, len(r.query)-r.queryCur)
	copy(tail, r.query[r.queryCur:])
	r.query = append(append(r.query[:r.queryCur], runes...), tail...)
	r.queryCur += len(runes)
}

func (r *ReplaceBar) insertReplaceRunes(runes []rune) {
	tail := make([]rune, len(r.replace)-r.replaceCur)
	copy(tail, r.replace[r.replaceCur:])
	r.replace = append(append(r.replace[:r.replaceCur], runes...), tail...)
	r.replaceCur += len(runes)
}

// ---------------------------------------------------------------------------
// Click handling
// ---------------------------------------------------------------------------

// HandleClick processes a mouse click at display column col on the given
// row (0 = find row, 1 = replace row). Returns the resulting action.
func (r *ReplaceBar) HandleClick(col, row int) Action {
	if row == 0 {
		return r.handleFindRowClick(col)
	}
	if row == 1 {
		return r.handleReplaceRowClick(col)
	}
	return ActionNone
}

// handleFindRowClick checks toggle badges, then falls back to query focus.
func (r *ReplaceBar) handleFindRowClick(col int) Action {
	for i := range toggleCount {
		bp := r.badgeAbsPos[i]
		if col >= bp.start && col < bp.end {
			r.focus = focusToToggle[i]
			return r.activateToggle(toggleIndex(i))
		}
	}
	r.focus = focusFind
	return ActionNone
}

// handleReplaceRowClick checks buttons, then falls back to replace focus.
func (r *ReplaceBar) handleReplaceRowClick(col int) Action {
	if col >= r.btnOnePos.start && col < r.btnOnePos.end {
		r.focus = focusBtnOne
		return ActionReplaceOne
	}
	if col >= r.btnAllPos.start && col < r.btnAllPos.end {
		r.focus = focusBtnAll
		return ActionReplaceAll
	}
	r.focus = focusReplace
	return ActionNone
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// View renders the replace bar as barHeight lines (find row + replace row + divider).
func (r *ReplaceBar) View(width int, th *theme.Theme, cursorVisible bool) string {
	findRow := r.renderFindRow(width, th, cursorVisible && r.focus == focusFind)
	replaceRow := r.renderReplaceRow(width, th, cursorVisible && r.focus == focusReplace)
	divider := renderDivider(width, th)
	return findRow + "\n" + replaceRow + "\n" + divider
}

// renderFindRow renders the find query line with toggle badges.
func (r *ReplaceBar) renderFindRow(width int, th *theme.Theme, cursorVisible bool) string {
	labelStyle := lipgloss.NewStyle().Foreground(th.Palette.Primary).Bold(true)
	queryStyle := lipgloss.NewStyle().Foreground(th.Palette.Foreground)
	countStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	cursorStyle := lipgloss.NewStyle().Reverse(true)

	// Right side: badges.
	badgesStr, badgesW := r.renderBadges(th)

	// Left side: "Find:" label + query + cursor + count.
	label := labelStyle.Render(" Find:")
	labelW := lipgloss.Width(label) + 1 // +1 for trailing space

	endCursorW := 0
	if r.queryCur >= len(r.query) {
		endCursorW = 1
	}

	countStr := ""
	countW := 0
	if r.matchCount != "" {
		countStr = countStyle.Render(" " + r.matchCount)
		countW = lipgloss.Width(countStr)
	}

	// Visible window of the query that always includes the cursor.
	availQ := max(width-labelW-endCursorW-countW-badgesW-2, 0) // 2 = min gap
	qLen := len(r.query)
	visStart := 0
	if qLen > availQ {
		visStart = max(r.queryCur-availQ+1, 0)
		visStart = min(visStart, qLen-availQ)
	}
	visEnd := min(visStart+availQ, qLen)

	queryDisplay := renderTextWithCursor(r.query, r.queryCur, visStart, visEnd, queryStyle, cursorStyle, cursorVisible)

	leftPart := label + " " + queryDisplay + countStr
	leftW := lipgloss.Width(leftPart)

	// Position badges on the right; record absolute column offsets.
	gap := max(width-leftW-badgesW, 0)
	badgeAreaStart := leftW + gap
	for i := range toggleCount {
		r.badgeAbsPos[i].start += badgeAreaStart
		r.badgeAbsPos[i].end += badgeAreaStart
	}

	line := leftPart + strings.Repeat(" ", gap) + badgesStr
	lineW := lipgloss.Width(line)
	if pad := width - lineW; pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return line
}

// renderReplaceRow renders the replacement text input line with buttons.
func (r *ReplaceBar) renderReplaceRow(width int, th *theme.Theme, cursorVisible bool) string {
	labelStyle := lipgloss.NewStyle().Foreground(th.Palette.Primary).Bold(true)
	textStyle := lipgloss.NewStyle().Foreground(th.Palette.Foreground)
	cursorStyle := lipgloss.NewStyle().Reverse(true)

	// Right side: action buttons.
	buttonsStr, buttonsW := r.renderButtons(th)

	// Left side: "Replace:" label + replacement text + cursor.
	label := labelStyle.Render(" Replace:")
	labelW := lipgloss.Width(label) + 1 // +1 for trailing space

	endCursorW := 0
	if r.replaceCur >= len(r.replace) {
		endCursorW = 1
	}

	// Visible window of replace text that includes the cursor.
	availR := max(width-labelW-endCursorW-buttonsW-2, 0) // 2 = min gap
	rLen := len(r.replace)
	visStart := 0
	if rLen > availR {
		visStart = max(r.replaceCur-availR+1, 0)
		visStart = min(visStart, rLen-availR)
	}
	visEnd := min(visStart+availR, rLen)

	textDisplay := renderTextWithCursor(r.replace, r.replaceCur, visStart, visEnd, textStyle, cursorStyle, cursorVisible)

	leftPart := label + " " + textDisplay
	leftW := lipgloss.Width(leftPart)

	// Position buttons on the right; compute absolute column offsets.
	gap := max(width-leftW-buttonsW, 0)
	btnAreaStart := leftW + gap
	oneRelEnd := r.btnOnePos.end
	allRelStart := r.btnAllPos.start
	allRelEnd := r.btnAllPos.end
	r.btnOnePos = badgePos{start: btnAreaStart, end: btnAreaStart + oneRelEnd}
	r.btnAllPos = badgePos{start: btnAreaStart + allRelStart, end: btnAreaStart + allRelEnd}

	line := leftPart + strings.Repeat(" ", gap) + buttonsStr
	lineW := lipgloss.Width(line)
	if pad := width - lineW; pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return line
}

// renderBadges builds the badge string and records each badge's relative
// column position (relative to the start of the badge area).
func (r *ReplaceBar) renderBadges(th *theme.Theme) (string, int) {
	var b strings.Builder
	col := 0
	for i := range toggleCount {
		if i > 0 {
			b.WriteByte(' ')
			col++
		}
		r.badgeAbsPos[i] = badgePos{start: col, end: col} // relative, adjusted later
		badge := r.renderBadge(toggleIndex(i), th)
		bw := lipgloss.Width(badge)
		b.WriteString(badge)
		r.badgeAbsPos[i].end = col + bw
		col += bw
	}
	return b.String(), col
}

func (r *ReplaceBar) renderBadge(idx toggleIndex, th *theme.Theme) string {
	fg := th.Palette.Muted
	if r.toggleActive[idx] {
		fg = th.Palette.Primary
	}
	style := lipgloss.NewStyle().Foreground(fg)
	if isToggleFocus(r.focus) && r.toggleIdx() == idx {
		style = style.Background(th.Palette.Selection)
	}
	return style.Render("[" + badgeLabels[idx] + "]")
}

// renderButtons builds the action button string and records each button's
// relative column position. Uses the same color pattern as renderBadge.
func (r *ReplaceBar) renderButtons(th *theme.Theme) (string, int) {
	oneFg := th.Palette.Muted
	allFg := th.Palette.Muted
	oneStyle := lipgloss.NewStyle().Foreground(oneFg)
	if r.focus == focusBtnOne {
		oneStyle = oneStyle.Background(th.Palette.Selection)
	}
	allStyle := lipgloss.NewStyle().Foreground(allFg)
	if r.focus == focusBtnAll {
		allStyle = allStyle.Background(th.Palette.Selection)
	}

	oneBtn := oneStyle.Render("[" + "\u21921" + "]") // [→1]
	oneW := lipgloss.Width(oneBtn)

	allBtn := allStyle.Render("[" + "\u2192*" + "]") // [→*]
	allW := lipgloss.Width(allBtn)

	r.btnOnePos = badgePos{start: 0, end: oneW}
	r.btnAllPos = badgePos{start: oneW + 1, end: oneW + 1 + allW}

	return oneBtn + " " + allBtn, oneW + 1 + allW
}

// renderDivider renders a horizontal divider line.
func renderDivider(width int, th *theme.Theme) string {
	return lipgloss.NewStyle().
		Foreground(th.Palette.Border).
		Render(strings.Repeat("\u2500", width))
}

// renderTextWithCursor renders a visible window of text with a blinking cursor.
func renderTextWithCursor(text []rune, cursor, visStart, visEnd int, textStyle, cursorStyle lipgloss.Style, cursorVisible bool) string {
	before := displaySlice(text, visStart, min(cursor, visEnd))

	var cursorPart string
	if cursor < len(text) {
		ch := string(displayRune(text[cursor]))
		if cursorVisible {
			cursorPart = cursorStyle.Render(ch)
		} else {
			cursorPart = textStyle.Render(ch)
		}
	} else {
		if cursorVisible {
			cursorPart = cursorStyle.Render(" ")
		} else {
			cursorPart = " "
		}
	}

	afterStart := min(cursor+1, len(text))
	after := displaySlice(text, afterStart, visEnd)

	return textStyle.Render(before) + cursorPart + textStyle.Render(after)
}

// displayRune maps control characters to visible glyphs.
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

// displaySlice returns a display-safe copy of text[start:end].
func displaySlice(text []rune, start, end int) string {
	buf := make([]rune, end-start)
	for i, ch := range text[start:end] {
		buf[i] = displayRune(ch)
	}
	return string(buf)
}
