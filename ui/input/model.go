package input

import (
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
)

// defaultMaxHeight is the maximum visible content rows before internal scrolling.
// Derived from: 3 lines of user input before the box scrolls internally.
const defaultMaxHeight = 3

// blinkInterval is the cursor blink period.
// Derived from: standard terminal cursor blink rate (~530ms).
const blinkInterval = 530 * time.Millisecond

// Model is the prompt input component.
type Model struct {
	lines     [][]rune // Content as lines of runes
	cursorRow int
	cursorCol int
	maxHeight int // Max visible rows before internal scroll
	scrollOff int // First visible line when content exceeds maxHeight

	// Cursor blink state
	cursorVisible bool
	lastBlinkAt   time.Time

	history   *InputHistory
	completer *Completer

	theme   *theme.Theme
	width   int
	height  int
	focused bool
}

// Compile-time interface checks.
var (
	_ component.Focusable = (*Model)(nil)
	_ component.Resizable = (*Model)(nil)
)

// New creates a Model with the given theme, history capacity, and completion providers.
func New(th *theme.Theme, historyCapacity int, providers ...CompletionProvider) *Model {
	return &Model{
		lines:         [][]rune{nil},
		maxHeight:     defaultMaxHeight,
		cursorVisible: true,
		lastBlinkAt:   time.Now(),
		history:       NewInputHistory(historyCapacity),
		completer:     NewCompleter(providers...),
		theme:         th,
	}
}

// ---------------------------------------------------------------------------
// component.Component
// ---------------------------------------------------------------------------

// Init returns no initial command.
func (m *Model) Init() tea.Cmd { return nil }

// Update processes a tea.Msg and returns the updated component and command.
func (m *Model) Update(raw tea.Msg) (component.Component, tea.Cmd) {
	switch typed := raw.(type) {
	case tea.KeyMsg:
		return m.handleKey(typed)
	case msg.TickMsg:
		m.advanceBlink(typed.Time)
		return m, nil
	default:
		return m, nil
	}
}

// View renders the input area.
func (m *Model) View() string {
	style := m.borderStyle()

	body := m.renderBody()
	popup := m.completer.View(m.contentWidth())

	if popup != "" {
		body = body + "\n" + popup
	}
	return style.Width(m.contentWidth()).Render(body)
}

// ---------------------------------------------------------------------------
// component.Focusable
// ---------------------------------------------------------------------------

// ID returns the focus identifier for this component.
func (m *Model) ID() component.FocusID { return component.FocusInput }

// Focused reports whether this component currently has focus.
func (m *Model) Focused() bool { return m.focused }

// SetFocused sets the focus state, resetting the cursor blink on focus gain.
func (m *Model) SetFocused(focused bool) {
	m.focused = focused
	if focused {
		m.resetBlink()
	}
}

// ---------------------------------------------------------------------------
// component.Resizable
// ---------------------------------------------------------------------------

// SetSize updates the component's available dimensions.
func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
}

// LineCount returns the number of content lines currently in the input.
func (m *Model) LineCount() int {
	return len(m.lines)
}

// ---------------------------------------------------------------------------
// Cursor blink
// ---------------------------------------------------------------------------

// advanceBlink toggles cursor visibility when the blink interval has elapsed.
func (m *Model) advanceBlink(now time.Time) {
	if !m.focused {
		return
	}
	if now.Sub(m.lastBlinkAt) >= blinkInterval {
		m.cursorVisible = !m.cursorVisible
		m.lastBlinkAt = now
	}
}

// resetBlink makes the cursor visible and resets the blink timer.
func (m *Model) resetBlink() {
	m.cursorVisible = true
	m.lastBlinkAt = time.Now()
}

// ---------------------------------------------------------------------------
// Key dispatch (table-driven)
// ---------------------------------------------------------------------------

// keyAction is a function that handles a specific key and returns a command.
type keyAction func(m *Model) tea.Cmd

// keyEntry maps a key description to an action. Order matters for lookup.
type keyEntry struct {
	match  func(k tea.KeyMsg) bool
	action keyAction
}

// completerKeys are dispatched first when the completer popup is active.
var completerKeys = []keyEntry{
	{matchKey("tab"), completerNext},
	{matchKey("up"), completerPrev},
	{matchKey("down"), completerNextDown},
	{matchKey("enter"), completerAccept},
	{matchKey("esc"), completerDismiss},
}

// normalKeys are dispatched when the completer is not active.
var normalKeys = []keyEntry{
	{matchKey("enter"), actionSubmit},
	{matchKey("shift+enter"), actionNewline},
	{matchKey("up"), actionUp},
	{matchKey("down"), actionDown},
	{matchKey("tab"), actionTab},
	{matchKey("backspace"), actionBackspace},
	{matchKey("left"), actionLeft},
	{matchKey("right"), actionRight},
	{matchKey("ctrl+a"), actionHome},
	{matchKey("ctrl+e"), actionEnd},
	{matchKey("ctrl+u"), actionClearLine},
	{matchKey("ctrl+k"), actionKillToEnd},
}

// handleKey dispatches a key message through the appropriate table.
func (m *Model) handleKey(k tea.KeyMsg) (component.Component, tea.Cmd) {
	m.resetBlink()
	if m.completer.IsActive() {
		if cmd, handled := m.dispatch(completerKeys, k); handled {
			return m, cmd
		}
	}
	if cmd, handled := m.dispatch(normalKeys, k); handled {
		return m, cmd
	}
	// Fall through: insert printable characters (including space).
	if k.Type == tea.KeyRunes || k.Type == tea.KeySpace {
		m.insertRunes([]rune(k.String()))
		m.completer.Dismiss()
	}
	return m, nil
}

// dispatch tries each entry in the table and returns (cmd, true) on first match.
func (m *Model) dispatch(table []keyEntry, k tea.KeyMsg) (tea.Cmd, bool) {
	for _, e := range table {
		if e.match(k) {
			return e.action(m), true
		}
	}
	return nil, false
}

// matchKey returns a predicate that matches a key by its string representation.
func matchKey(desc string) func(tea.KeyMsg) bool {
	return func(k tea.KeyMsg) bool { return k.String() == desc }
}

// ---------------------------------------------------------------------------
// Normal-mode actions
// ---------------------------------------------------------------------------

func actionSubmit(m *Model) tea.Cmd {
	text := m.Text()
	if text == "" {
		return nil
	}
	agent, remainder := detectAgentPrefix(text)
	m.history.Push(text)
	m.clear()
	return func() tea.Msg {
		return msg.SubmitPromptMsg{
			Text:        remainder,
			TargetAgent: agent,
		}
	}
}

func actionNewline(m *Model) tea.Cmd {
	m.insertNewline()
	return nil
}

func actionUp(m *Model) tea.Cmd {
	if m.cursorRow > 0 {
		m.cursorRow--
		m.clampCol()
		m.scrollIntoView()
		return nil
	}
	// On first line: navigate history when text is empty or already navigating.
	entry, ok := m.history.Up(m.Text())
	if !ok {
		return nil
	}
	m.setText(entry)
	return nil
}

func actionDown(m *Model) tea.Cmd {
	if m.cursorRow < len(m.lines)-1 {
		m.cursorRow++
		m.clampCol()
		m.scrollIntoView()
		return nil
	}
	entry, ok := m.history.Down()
	if !ok {
		return nil
	}
	m.setText(entry)
	return nil
}

func actionTab(m *Model) tea.Cmd {
	text := m.Text()
	m.completer.Trigger(text, m.absoluteCursorPos())
	return nil
}

func actionBackspace(m *Model) tea.Cmd {
	m.deleteBeforeCursor()
	return nil
}

func actionLeft(m *Model) tea.Cmd {
	m.moveCursorLeft()
	return nil
}

func actionRight(m *Model) tea.Cmd {
	m.moveCursorRight()
	return nil
}

func actionHome(m *Model) tea.Cmd {
	m.cursorCol = 0
	return nil
}

func actionEnd(m *Model) tea.Cmd {
	m.cursorCol = len(m.lines[m.cursorRow])
	return nil
}

func actionClearLine(m *Model) tea.Cmd {
	m.lines[m.cursorRow] = nil
	m.cursorCol = 0
	return nil
}

func actionKillToEnd(m *Model) tea.Cmd {
	m.lines[m.cursorRow] = m.lines[m.cursorRow][:m.cursorCol]
	return nil
}

// ---------------------------------------------------------------------------
// Completer-mode actions
// ---------------------------------------------------------------------------

func completerNext(m *Model) tea.Cmd {
	m.completer.Next()
	return nil
}

func completerPrev(m *Model) tea.Cmd {
	m.completer.Previous()
	return nil
}

func completerNextDown(m *Model) tea.Cmd {
	m.completer.Next()
	return nil
}

func completerAccept(m *Model) tea.Cmd {
	text := m.completer.Accept()
	if text == "" {
		return nil
	}
	m.replaceWordBeforeCursor(text)
	return nil
}

func completerDismiss(m *Model) tea.Cmd {
	m.completer.Dismiss()
	return nil
}

// ---------------------------------------------------------------------------
// Text manipulation helpers
// ---------------------------------------------------------------------------

// Text returns the full input content as a single string.
func (m *Model) Text() string {
	var b strings.Builder
	for i, line := range m.lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(string(line))
	}
	return b.String()
}

// setText replaces the entire input content and positions the cursor at the end.
func (m *Model) setText(s string) {
	raw := strings.Split(s, "\n")
	m.lines = make([][]rune, len(raw))
	for i, r := range raw {
		m.lines[i] = []rune(r)
	}
	m.cursorRow = len(m.lines) - 1
	m.cursorCol = len(m.lines[m.cursorRow])
	m.scrollIntoView()
}

// clear resets the input to an empty single line.
func (m *Model) clear() {
	m.lines = [][]rune{nil}
	m.cursorRow = 0
	m.cursorCol = 0
	m.scrollOff = 0
	m.history.Reset()
	m.completer.Dismiss()
}

// insertRunes inserts runes at the cursor position.
func (m *Model) insertRunes(rs []rune) {
	line := m.lines[m.cursorRow]
	newLine := make([]rune, 0, len(line)+len(rs))
	newLine = append(newLine, line[:m.cursorCol]...)
	newLine = append(newLine, rs...)
	newLine = append(newLine, line[m.cursorCol:]...)
	m.lines[m.cursorRow] = newLine
	m.cursorCol += len(rs)
}

// insertNewline splits the current line at the cursor.
func (m *Model) insertNewline() {
	line := m.lines[m.cursorRow]
	before := make([]rune, m.cursorCol)
	copy(before, line[:m.cursorCol])
	after := make([]rune, len(line)-m.cursorCol)
	copy(after, line[m.cursorCol:])

	// Insert a new line after the current row.
	newLines := make([][]rune, 0, len(m.lines)+1)
	newLines = append(newLines, m.lines[:m.cursorRow]...)
	newLines = append(newLines, before, after)
	newLines = append(newLines, m.lines[m.cursorRow+1:]...)
	m.lines = newLines

	m.cursorRow++
	m.cursorCol = 0
	m.scrollIntoView()
}

// deleteBeforeCursor removes the rune or joins lines when at column 0.
func (m *Model) deleteBeforeCursor() {
	if m.cursorCol > 0 {
		line := m.lines[m.cursorRow]
		m.lines[m.cursorRow] = append(line[:m.cursorCol-1], line[m.cursorCol:]...)
		m.cursorCol--
		return
	}
	if m.cursorRow == 0 {
		return
	}
	// Join with previous line.
	prev := m.lines[m.cursorRow-1]
	joinCol := len(prev)
	m.lines[m.cursorRow-1] = append(prev, m.lines[m.cursorRow]...)
	m.lines = append(m.lines[:m.cursorRow], m.lines[m.cursorRow+1:]...)
	m.cursorRow--
	m.cursorCol = joinCol
	m.scrollIntoView()
}

// moveCursorLeft moves the cursor one position to the left, wrapping to the previous line.
func (m *Model) moveCursorLeft() {
	if m.cursorCol > 0 {
		m.cursorCol--
		return
	}
	if m.cursorRow > 0 {
		m.cursorRow--
		m.cursorCol = len(m.lines[m.cursorRow])
		m.scrollIntoView()
	}
}

// moveCursorRight moves the cursor one position to the right, wrapping to the next line.
func (m *Model) moveCursorRight() {
	if m.cursorCol < len(m.lines[m.cursorRow]) {
		m.cursorCol++
		return
	}
	if m.cursorRow < len(m.lines)-1 {
		m.cursorRow++
		m.cursorCol = 0
		m.scrollIntoView()
	}
}

// clampCol ensures cursorCol does not exceed the current line length.
func (m *Model) clampCol() {
	m.cursorCol = min(m.cursorCol, len(m.lines[m.cursorRow]))
}

// scrollIntoView adjusts scrollOff so the cursor row is visible.
func (m *Model) scrollIntoView() {
	if m.cursorRow < m.scrollOff {
		m.scrollOff = m.cursorRow
	}
	if m.cursorRow >= m.scrollOff+m.maxHeight {
		m.scrollOff = m.cursorRow - m.maxHeight + 1
	}
}

// absoluteCursorPos returns the cursor position as a byte offset into Text().
func (m *Model) absoluteCursorPos() int {
	pos := 0
	for i := range m.cursorRow {
		pos += len(string(m.lines[i])) + 1 // +1 for newline
	}
	pos += len(string(m.lines[m.cursorRow][:m.cursorCol]))
	return pos
}

// replaceWordBeforeCursor replaces the word fragment before the cursor with text.
func (m *Model) replaceWordBeforeCursor(text string) {
	line := m.lines[m.cursorRow]
	start := m.cursorCol
	for start > 0 && !isWordBreak(runeToFirstByte(line[start-1])) {
		start--
	}
	replacement := []rune(text)
	newLine := make([]rune, 0, start+len(replacement)+len(line)-m.cursorCol)
	newLine = append(newLine, line[:start]...)
	newLine = append(newLine, replacement...)
	newLine = append(newLine, line[m.cursorCol:]...)
	m.lines[m.cursorRow] = newLine
	m.cursorCol = start + len(replacement)
}

// runeToFirstByte returns the first byte of a rune's UTF-8 encoding.
func runeToFirstByte(r rune) byte {
	var buf [utf8.UTFMax]byte
	utf8.EncodeRune(buf[:], r)
	return buf[0]
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// borderStyle returns the appropriate border style based on focus state.
func (m *Model) borderStyle() lipgloss.Style {
	if m.focused {
		return m.theme.InputFocused
	}
	return m.theme.InputBorder
}

// contentWidth returns the width available for text inside borders.
func (m *Model) contentWidth() int {
	// Account for border (1 char each side).
	w := m.width - 2
	if w < 1 {
		return 1
	}
	return w
}

// renderBody renders the visible lines with cursor and placeholder.
func (m *Model) renderBody() string {
	if m.isEmpty() && !m.focused {
		return m.theme.Placeholder.Render("Type a message...")
	}

	visibleEnd := min(m.scrollOff+m.maxHeight, len(m.lines))
	visible := m.lines[m.scrollOff:visibleEnd]

	var b strings.Builder
	for i, line := range visible {
		absRow := m.scrollOff + i
		rendered := m.renderLine(line, absRow)
		b.WriteString(rendered)
		if i < len(visible)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// renderLine renders a single line, highlighting @agent prefix and showing the cursor.
func (m *Model) renderLine(line []rune, absRow int) string {
	lineStr := string(line)

	// Highlight @agent prefix on the first line.
	if absRow == 0 {
		lineStr = m.highlightAgentPrefix(lineStr)
	}

	// Show cursor on the focused row.
	if !m.focused || absRow != m.cursorRow {
		return lineStr
	}
	return m.renderCursor(line, lineStr)
}

// renderCursor inserts a visible block cursor at the cursor position.
// When the cursor is in the invisible phase of its blink, the text
// renders without the reverse-video highlight.
func (m *Model) renderCursor(line []rune, lineStr string) string {
	if !m.cursorVisible {
		return lineStr
	}
	cursorStyle := lipgloss.NewStyle().Reverse(true)

	if m.cursorCol >= len(line) {
		return lineStr + cursorStyle.Render(" ")
	}
	before := string(line[:m.cursorCol])
	under := string(line[m.cursorCol : m.cursorCol+1])
	after := string(line[m.cursorCol+1:])
	return before + cursorStyle.Render(under) + after
}

// highlightAgentPrefix styles the leading @agent token using the theme badge.
func (m *Model) highlightAgentPrefix(lineStr string) string {
	agent, _ := detectAgentPrefix(lineStr)
	if agent == "" {
		return lineStr
	}
	badge := m.theme.AgentBadge(agent)
	tag := "@" + agent
	return badge.Render(tag) + lineStr[len(tag):]
}

// isEmpty reports whether the input buffer has no content.
func (m *Model) isEmpty() bool {
	return len(m.lines) == 1 && len(m.lines[0]) == 0
}

// ---------------------------------------------------------------------------
// Agent prefix detection
// ---------------------------------------------------------------------------

// detectAgentPrefix looks for @word at the start of text.
// Returns the agent name (without @) and the remainder with leading space trimmed.
func detectAgentPrefix(text string) (agentName string, remainder string) {
	if len(text) == 0 || text[0] != '@' {
		return "", text
	}
	end := 1
	for end < len(text) && text[end] != ' ' && text[end] != '\t' && text[end] != '\n' {
		end++
	}
	if end <= 1 {
		return "", text
	}
	agentName = text[1:end]
	remainder = strings.TrimLeft(text[end:], " \t")
	return agentName, remainder
}
