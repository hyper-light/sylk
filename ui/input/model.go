package input

import (
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
)

// borderSize is the vertical space consumed by the input border (top + bottom).
const borderSize = 2

// Model is the prompt input component.
type Model struct {
	lines     [][]rune // Content as lines of runes
	cursorRow int
	cursorCol int
	maxHeight int // Max visible rows before internal scroll
	scrollOff int // First visible visual line when content exceeds maxHeight

	// Word-wrap state (visual-only decomposition of actual lines).
	wrap      *wrapState
	wrapDirty bool

	// Selection state.
	sel selection // Anchor+cursor range selection.

	// Markdown display state.
	md      *mdDisplay    // Parsed markdown mapping (nil if none).
	mdDirty bool          // True when lines have changed since last parse.
	mdStyle *mdInputStyles // Cached markdown styles.

	history   *InputHistory
	completer *Completer

	theme       *theme.Theme
	placeholder string // Idle text when empty and unfocused.
	width       int
	height      int
	focused     bool

	// lineStyler, when set, overrides default text styling for line 0.
	// Receives the raw line text and returns a styled string plus an
	// optional hint displayed after the text in muted style.
	lineStyler func(text string) (styled string, hint string)

	// slashValidator reports whether a command name (without "/") is a
	// known slash command. Used by highlightSlashCommand.
	slashValidator func(cmd string) bool

	// focusBorderRender, when set, replaces the lipgloss border style with
	// per-character gradient border rendering for the focus ring shimmer.
	focusBorderRender func(content string, innerW, innerH, maxH int) string

	// View cache: avoids re-rendering when no visible state changed.
	viewCache      string
	viewDirty      bool
	lastBlinkPhase bool // Blink phase at last render; invalidates viewCache on change.
}

// Compile-time interface checks.
var (
	_ component.Focusable = (*Model)(nil)
	_ component.Resizable = (*Model)(nil)
)

// New creates a Model with the given theme, history capacity, and completion providers.
func New(th *theme.Theme, historyCapacity int, providers ...CompletionProvider) *Model {
	return &Model{
		lines:       [][]rune{nil},
		maxHeight:   1,
		wrapDirty:   true,
		history:     NewInputHistory(historyCapacity),
		completer:   NewCompleter(providers...),
		theme:       th,
		placeholder: "Type a message...",
	}
}

// SetLineStyler sets an optional styling function for line 0 text.
// The function returns styled text and an optional hint string.
// Pass nil to clear.
func (m *Model) SetLineStyler(fn func(string) (string, string)) {
	m.lineStyler = fn
	m.viewDirty = true
}

// SetSlashValidator sets a function that reports whether a command name
// (without the leading "/") is a known slash command. Only exact matches
// are highlighted.
func (m *Model) SetSlashValidator(fn func(string) bool) {
	m.slashValidator = fn
	m.viewDirty = true
}

// SetPlaceholder changes the idle text shown when the input is empty.
func (m *Model) SetPlaceholder(text string) {
	m.placeholder = text
	m.viewDirty = true
}

// SetText replaces the input content and positions the cursor at the end.
func (m *Model) SetText(s string) {
	m.setText(s)
	m.viewDirty = true
}

// Clear resets the input content to empty.
func (m *Model) Clear() {
	m.lines = [][]rune{nil}
	m.cursorRow = 0
	m.cursorCol = 0
	m.scrollOff = 0
	m.sel.active = false
	m.completer.Dismiss()
	m.wrapDirty = true
	m.mdDirty = true
	m.viewDirty = true
}

// SelectAll marks all input content as selected.
func (m *Model) SelectAll() {
	m.sel = selection{anchorRow: 0, anchorCol: 0, active: true}
	m.cursorRow = len(m.lines) - 1
	m.cursorCol = len(m.lines[m.cursorRow])
	m.viewDirty = true
}

// HasSelection reports whether a valid selection is active.
// Deactivates a stale selection whose anchor/cursor coordinates are
// out of bounds relative to the current buffer.
func (m *Model) HasSelection() bool {
	if !m.sel.active || m.isEmpty() {
		return false
	}
	if m.sel.anchorRow < 0 || m.sel.anchorRow >= len(m.lines) ||
		m.cursorRow < 0 || m.cursorRow >= len(m.lines) ||
		m.sel.anchorCol < 0 || m.cursorCol < 0 {
		m.sel.active = false
		return false
	}
	return true
}

// SelectedText returns the selected text, or "" if nothing is selected.
func (m *Model) SelectedText() string {
	if !m.HasSelection() {
		return ""
	}
	return m.sel.extractText(m.lines, m.cursorRow, m.cursorCol)
}

// ClearSelection deactivates the selection.
func (m *Model) ClearSelection() {
	m.sel.active = false
	m.viewDirty = true
}

// CutSelection copies the selected text and removes it from the buffer.
func (m *Model) CutSelection() string {
	text := m.SelectedText()
	if text == "" {
		return ""
	}
	m.clearSelectionContent()
	return text
}

// clearSelectionContent deletes selected text, ready for new input.
// No-op when nothing is selected.
func (m *Model) clearSelectionContent() {
	if !m.sel.active {
		return
	}
	newLines, row, col := m.sel.deleteRange(m.lines, m.cursorRow, m.cursorCol)
	m.lines = newLines
	m.cursorRow = row
	m.cursorCol = col
	m.sel.active = false
	m.wrapDirty = true
	m.mdDirty = true
	m.viewDirty = true
	m.scrollIntoView()
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
		m.viewDirty = true
		return m.handleKey(typed)
	default:
		return m, nil
	}
}

// View renders the input area.
// ViewDirty reports whether View() would produce new output.
func (m *Model) ViewDirty() bool { return m.viewDirty }

func (m *Model) View(cursorVisible bool) string {
	if cursorVisible != m.lastBlinkPhase {
		m.viewDirty = true
		m.lastBlinkPhase = cursorVisible
	}
	if !m.viewDirty && m.viewCache != "" {
		return m.viewCache
	}

	body := m.renderBody(cursorVisible)

	// Right-align a submit hint when the input is empty.
	cw := m.contentWidth()
	if m.isEmpty() {
		hint := m.theme.Placeholder.Render("enter ↵")
		if gap := cw - lipgloss.Width(body) - lipgloss.Width(hint); gap > 0 {
			body = body + strings.Repeat(" ", gap) + hint
		}
	}

	if m.focused && m.focusBorderRender != nil {
		m.viewCache = m.focusBorderRender(body, cw, m.maxHeight, m.height)
	} else {
		m.viewCache = m.borderStyle().Width(cw).Render(body)
	}
	m.viewDirty = false
	return m.viewCache
}

// ---------------------------------------------------------------------------
// component.Focusable
// ---------------------------------------------------------------------------

// ID returns the focus identifier for this component.
func (m *Model) ID() component.FocusID { return component.FocusInput }

// Focused reports whether this component currently has focus.
func (m *Model) Focused() bool { return m.focused }

// SetFocused sets the focus state.
func (m *Model) SetFocused(focused bool) {
	m.focused = focused
	m.viewDirty = true
}

// ---------------------------------------------------------------------------
// component.Resizable
// ---------------------------------------------------------------------------

// SetSize updates the component's available dimensions.
// The maxHeight for internal scrolling is derived from the allocated height.
func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.maxHeight = max(height-borderSize, 1)

	// Clamp scrollOff so the visible area is fully utilized when maxHeight
	// grows (e.g. after a paste triggers input expansion). Without this,
	// scrollIntoView() during paste sets scrollOff using the OLD maxHeight,
	// leaving the rendered body shorter than the compositor's allocation.
	if m.wrap != nil {
		maxOff := m.wrap.visualTotal - m.maxHeight
		if maxOff < 0 {
			maxOff = 0
		}
		if m.scrollOff > maxOff {
			m.scrollOff = maxOff
		}
	}

	m.wrapDirty = true
	m.viewDirty = true
}

// LineCount returns the number of content lines currently in the input.
func (m *Model) LineCount() int {
	return len(m.lines)
}

// ensureMd reparses the markdown display mapping if dirty.
func (m *Model) ensureMd() {
	if !m.mdDirty && m.md != nil {
		return
	}
	if m.mdStyle == nil {
		m.mdStyle = newMdInputStyles(m.theme)
	}
	m.md = parseMdDisplay(m.lines, m.theme)
	m.mdDirty = false
}

// ensureWrap recomputes the wrap state if dirty.
func (m *Model) ensureWrap() {
	if !m.wrapDirty && m.wrap != nil {
		return
	}
	m.ensureMd()
	if m.md != nil {
		// Wrap on display text (with hidden delimiters removed).
		dispLines := make([][]rune, len(m.lines))
		for i, line := range m.lines {
			dispLines[i] = m.md.displayRunes(i, line)
		}
		m.wrap = computeWrap(dispLines, m.contentWidth())
	} else {
		m.wrap = computeWrap(m.lines, m.contentWidth())
	}
	m.wrapDirty = false
}

// VisualLineCount returns the number of visual lines after word wrapping.
func (m *Model) VisualLineCount() int {
	m.ensureWrap()
	return m.wrap.visualTotal
}

// ScrollUp decrements scrollOff by 1 visual line (clamped to 0).
func (m *Model) ScrollUp() {
	if m.scrollOff > 0 {
		m.scrollOff--
		m.viewDirty = true
	}
}

// ScrollDown increments scrollOff by 1 visual line (clamped to max).
func (m *Model) ScrollDown() {
	m.ensureWrap()
	maxOff := m.wrap.visualTotal - m.maxHeight
	if maxOff < 0 {
		maxOff = 0
	}
	if m.scrollOff < maxOff {
		m.scrollOff++
		m.viewDirty = true
	}
}

// CanScroll reports whether the content exceeds the visible area.
func (m *Model) CanScroll() bool {
	m.ensureWrap()
	return m.wrap.visualTotal > m.maxHeight
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
	{matchKey("tab"), completerAccept},
	{matchKey("up"), completerPrev},
	{matchKey("down"), completerNextDown},
	{matchKey("enter"), completerAccept},
	{matchKey("esc"), completerDismiss},
}

// selectionKeys extend or start a range selection with shift+arrow.
var selectionKeys = []keyEntry{
	{matchKey("shift+left"), actionSelLeft},
	{matchKey("shift+right"), actionSelRight},
	{matchKey("shift+up"), actionSelUp},
	{matchKey("shift+down"), actionSelDown},
	{matchKey("shift+home"), actionSelHome},
	{matchKey("shift+end"), actionSelEnd},
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
	// Bracketed paste: replace selection if active, then insert.
	if k.Paste && len(k.Runes) > 0 {
		m.clearSelectionContent()
		m.insertPaste(k.Runes)
		m.completer.Dismiss()
		return m, nil
	}

	// Backspace/Delete with selection: clear selected content.
	if m.sel.active && (k.Type == tea.KeyBackspace || k.Type == tea.KeyDelete) {
		m.clearSelectionContent()
		m.retriggerOrDismiss()
		return m, nil
	}

	if m.completer.IsActive() {
		if cmd, handled := m.dispatch(completerKeys, k); handled {
			return m, cmd
		}
	}

	// Shift+arrow: extend or start selection.
	if cmd, handled := m.dispatch(selectionKeys, k); handled {
		return m, cmd
	}

	if cmd, handled := m.dispatch(normalKeys, k); handled {
		m.sel.active = false
		return m, cmd
	}
	// Fall through: insert printable characters (replacing selection).
	if k.Type == tea.KeyRunes || k.Type == tea.KeySpace {
		m.clearSelectionContent()
		m.insertRunes(k.Runes)
		m.retriggerOrDismiss()
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
	vRow, vCol := m.cursorToVisual()
	if vRow > 0 {
		m.cursorRow, m.cursorCol = m.visualToCursor(vRow-1, vCol)
		m.clampCol()
		m.skipHiddenDelimiters(-1)
		m.scrollIntoView()
		return nil
	}
	// At visual top: history navigation.
	entry, ok := m.history.Up(m.Text())
	if !ok {
		return nil
	}
	m.setText(entry)
	return nil
}

func actionDown(m *Model) tea.Cmd {
	vRow, vCol := m.cursorToVisual()
	if vRow < m.wrap.visualTotal-1 {
		m.cursorRow, m.cursorCol = m.visualToCursor(vRow+1, vCol)
		m.clampCol()
		m.skipHiddenDelimiters(1)
		m.scrollIntoView()
		return nil
	}
	// At visual bottom: history navigation.
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
	m.retriggerOrDismiss()
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
	vRow, _ := m.cursorToVisual()
	m.cursorRow, m.cursorCol = m.visualToCursor(vRow, 0)
	m.clampCol()
	return nil
}

func actionEnd(m *Model) tea.Cmd {
	vRow, _ := m.cursorToVisual()
	m.ensureWrap()
	_, segIdx := m.wrap.visualRowToSegment(vRow)
	row, _ := m.wrap.visualToActual(vRow, 0)
	span := m.wrap.segments[row][segIdx]
	m.cursorRow, m.cursorCol = m.visualToCursor(vRow, span.End-span.Start)
	m.clampCol()
	return nil
}

func actionClearLine(m *Model) tea.Cmd {
	m.lines[m.cursorRow] = nil
	m.cursorCol = 0
	m.wrapDirty = true
	m.mdDirty = true
	return nil
}

func actionKillToEnd(m *Model) tea.Cmd {
	m.lines[m.cursorRow] = m.lines[m.cursorRow][:m.cursorCol]
	m.wrapDirty = true
	m.mdDirty = true
	return nil
}

// ---------------------------------------------------------------------------
// Selection-mode actions (shift+arrow)
// ---------------------------------------------------------------------------

// beginSel activates selection with anchor at the current cursor if not already active.
func (m *Model) beginSel() {
	if !m.sel.active {
		m.sel = selection{
			anchorRow: m.cursorRow,
			anchorCol: m.cursorCol,
			active:    true,
		}
		m.viewDirty = true
	}
}

func actionSelLeft(m *Model) tea.Cmd {
	m.beginSel()
	m.moveCursorLeft()
	return nil
}

func actionSelRight(m *Model) tea.Cmd {
	m.beginSel()
	m.moveCursorRight()
	return nil
}

func actionSelUp(m *Model) tea.Cmd {
	m.beginSel()
	vRow, vCol := m.cursorToVisual()
	if vRow > 0 {
		m.cursorRow, m.cursorCol = m.visualToCursor(vRow-1, vCol)
		m.clampCol()
		m.skipHiddenDelimiters(-1)
		m.scrollIntoView()
	}
	return nil
}

func actionSelDown(m *Model) tea.Cmd {
	m.beginSel()
	vRow, vCol := m.cursorToVisual()
	if vRow < m.wrap.visualTotal-1 {
		m.cursorRow, m.cursorCol = m.visualToCursor(vRow+1, vCol)
		m.clampCol()
		m.skipHiddenDelimiters(1)
		m.scrollIntoView()
	}
	return nil
}

func actionSelHome(m *Model) tea.Cmd {
	m.beginSel()
	vRow, _ := m.cursorToVisual()
	m.cursorRow, m.cursorCol = m.visualToCursor(vRow, 0)
	m.clampCol()
	return nil
}

func actionSelEnd(m *Model) tea.Cmd {
	m.beginSel()
	vRow, _ := m.cursorToVisual()
	m.ensureWrap()
	_, segIdx := m.wrap.visualRowToSegment(vRow)
	row, _ := m.wrap.visualToActual(vRow, 0)
	span := m.wrap.segments[row][segIdx]
	m.cursorRow, m.cursorCol = m.visualToCursor(vRow, span.End-span.Start)
	m.clampCol()
	return nil
}

// ---------------------------------------------------------------------------
// Mouse click positioning
// ---------------------------------------------------------------------------

// ClickAt positions the cursor at the given content-relative coordinates.
// x is the column within the content area, y is the row.
func (m *Model) ClickAt(x, y int) {
	m.sel.active = false
	m.completer.Dismiss()

	m.ensureWrap()
	vRow := m.scrollOff + y
	if vRow < 0 {
		vRow = 0
	}
	if vRow >= m.wrap.visualTotal {
		vRow = m.wrap.visualTotal - 1
	}

	m.cursorRow, m.cursorCol = m.visualToCursor(vRow, x)
	m.clampCol()
	m.skipHiddenDelimiters(1)
	m.viewDirty = true
}

// DragStart begins a mouse drag selection at the given content-relative coordinates.
func (m *Model) DragStart(x, y int) {
	m.completer.Dismiss()
	m.ensureWrap()
	vRow := max(min(m.scrollOff+y, m.wrap.visualTotal-1), 0)
	m.cursorRow, m.cursorCol = m.visualToCursor(vRow, x)
	m.clampCol()
	m.sel = selection{
		anchorRow: m.cursorRow,
		anchorCol: m.cursorCol,
		active:    true,
	}
	m.viewDirty = true
}

// DragTo extends the selection to the given content-relative coordinates.
func (m *Model) DragTo(x, y int) {
	if !m.sel.active {
		return
	}
	m.ensureWrap()
	vRow := max(min(m.scrollOff+y, m.wrap.visualTotal-1), 0)
	m.cursorRow, m.cursorCol = m.visualToCursor(vRow, max(x, 0))
	m.clampCol()
	m.scrollIntoView()
	m.viewDirty = true
}

// ---------------------------------------------------------------------------
// Completer-mode actions
// ---------------------------------------------------------------------------

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
	m.sel.active = false
	m.wrapDirty = true
	m.mdDirty = true
	m.scrollIntoView()
}

// clear resets the input to an empty single line.
func (m *Model) clear() {
	m.lines = [][]rune{nil}
	m.cursorRow = 0
	m.cursorCol = 0
	m.scrollOff = 0
	m.sel.active = false
	m.wrapDirty = true
	m.mdDirty = true
	m.md = nil
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
	m.wrapDirty = true
	m.mdDirty = true
}

// retriggerOrDismiss re-triggers the completer when the cursor is on
// line 0 and the current word prefix starts with "/". Otherwise it
// dismisses any active completion popup.
func (m *Model) retriggerOrDismiss() {
	if m.cursorRow == 0 && m.slashValidator != nil {
		prefix := wordBeforeCursor(string(m.lines[0]), m.cursorCol)
		if len(prefix) >= 1 && prefix[0] == '/' {
			m.completer.Trigger(m.Text(), m.absoluteCursorPos())
			return
		}
	}
	m.completer.Dismiss()
}

// insertPaste handles bracketed paste by normalizing line endings and
// splitting the pasted text into proper multi-line content.
func (m *Model) insertPaste(runes []rune) {
	text := string(runes)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	parts := strings.Split(text, "\n")
	m.insertRunes([]rune(parts[0]))
	for _, part := range parts[1:] {
		m.insertNewline()
		if len(part) > 0 {
			m.insertRunes([]rune(part))
		}
	}
}

// insertNewline splits the current line at the cursor.
func (m *Model) insertNewline() {
	m.sel.active = false
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
	m.wrapDirty = true
	m.mdDirty = true
	m.scrollIntoView()
}

// deleteBeforeCursor removes the rune or joins lines when at column 0.
func (m *Model) deleteBeforeCursor() {
	if m.cursorCol > 0 {
		line := m.lines[m.cursorRow]
		m.lines[m.cursorRow] = append(line[:m.cursorCol-1], line[m.cursorCol:]...)
		m.cursorCol--
		m.wrapDirty = true
		m.mdDirty = true
		return
	}
	if m.cursorRow == 0 {
		return
	}
	// Join with previous line; deactivate selection since row count changes.
	m.sel.active = false
	prev := m.lines[m.cursorRow-1]
	joinCol := len(prev)
	m.lines[m.cursorRow-1] = append(prev, m.lines[m.cursorRow]...)
	m.lines = append(m.lines[:m.cursorRow], m.lines[m.cursorRow+1:]...)
	m.cursorRow--
	m.cursorCol = joinCol
	m.wrapDirty = true
	m.mdDirty = true
	m.scrollIntoView()
}

// moveCursorLeft moves the cursor one position to the left, wrapping across
// visual-line segments and actual lines.
func (m *Model) moveCursorLeft() {
	if m.cursorCol > 0 {
		m.cursorCol--
		m.skipHiddenDelimiters(-1)
		m.scrollIntoView()
		return
	}
	if m.cursorRow > 0 {
		m.cursorRow--
		m.cursorCol = len(m.lines[m.cursorRow])
		m.scrollIntoView()
	}
}

// moveCursorRight moves the cursor one position to the right, wrapping across
// visual-line segments and actual lines.
func (m *Model) moveCursorRight() {
	if m.cursorCol < len(m.lines[m.cursorRow]) {
		m.cursorCol++
		m.skipHiddenDelimiters(1)
		m.scrollIntoView()
		return
	}
	if m.cursorRow < len(m.lines)-1 {
		m.cursorRow++
		m.cursorCol = 0
		m.skipHiddenDelimiters(1)
		m.scrollIntoView()
	}
}

// clampCol ensures cursorCol does not exceed the current line length.
func (m *Model) clampCol() {
	m.cursorCol = min(m.cursorCol, len(m.lines[m.cursorRow]))
}

// cursorToVisual converts raw cursor (row, col) to visual (vRow, vCol),
// accounting for markdown display mapping when active.
func (m *Model) cursorToVisual() (vRow, vCol int) {
	m.ensureWrap()
	if m.md != nil && m.cursorRow < len(m.md.lines) {
		ml := m.md.lines[m.cursorRow]
		dispCol := ml.rawColToDisplay(m.cursorCol)
		return m.wrap.actualToVisual(m.cursorRow, dispCol)
	}
	return m.wrap.actualToVisual(m.cursorRow, m.cursorCol)
}

// visualToCursor converts visual (vRow, vCol) to raw cursor (row, col),
// accounting for markdown display mapping when active.
func (m *Model) visualToCursor(vRow, vCol int) (row, col int) {
	m.ensureWrap()
	row, dispCol := m.wrap.visualToActual(vRow, vCol)
	if m.md != nil && row < len(m.md.lines) {
		ml := m.md.lines[row]
		col = ml.displayColToRaw(dispCol)
	} else {
		col = dispCol
	}
	return row, col
}

// skipHiddenDelimiters advances or retreats the cursor past hidden delimiter
// positions when markdown is active. dir should be -1 (left) or +1 (right).
func (m *Model) skipHiddenDelimiters(dir int) {
	if m.md == nil || m.cursorRow >= len(m.md.lines) {
		return
	}
	ml := m.md.lines[m.cursorRow]
	lineLen := len(m.lines[m.cursorRow])
	for m.cursorCol > 0 && m.cursorCol < lineLen {
		if ml.rawToDisp[m.cursorCol] != -1 {
			break
		}
		m.cursorCol += dir
	}
}

// scrollIntoView adjusts scrollOff so the cursor's visual row is visible.
func (m *Model) scrollIntoView() {
	vRow, _ := m.cursorToVisual()
	if vRow < m.scrollOff {
		m.scrollOff = vRow
	}
	if vRow >= m.scrollOff+m.maxHeight {
		m.scrollOff = vRow - m.maxHeight + 1
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
	m.wrapDirty = true
	m.mdDirty = true
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

// SetFocusBorderStyle updates the focused border style for the input panel.
// Called per frame by the app to apply the focus ring shimmer.
func (m *Model) SetFocusBorderStyle(style lipgloss.Style) {
	m.theme.InputFocused = style
}

// SetFocusBorderRender sets a per-character gradient border renderer for the
// input panel's focus ring. When non-nil, replaces the lipgloss border style.
// Marks the view dirty since the gradient changes each frame.
func (m *Model) SetFocusBorderRender(fn func(content string, innerW, innerH, maxH int) string) {
	m.focusBorderRender = fn
	m.viewDirty = true
}

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

// renderBody renders the visible visual lines with cursor and placeholder.
func (m *Model) renderBody(cursorVisible bool) string {
	if m.isEmpty() && !m.focused {
		return m.theme.Placeholder.Render(m.placeholder)
	}
	m.ensureWrap()
	end := min(m.scrollOff+m.maxHeight, m.wrap.visualTotal)
	var b strings.Builder
	for vRow := m.scrollOff; vRow < end; vRow++ {
		if vRow > m.scrollOff {
			b.WriteByte('\n')
		}
		actualRow, segIdx := m.wrap.visualRowToSegment(vRow)
		span := m.wrap.segments[actualRow][segIdx]

		if m.md != nil {
			dispRunes := m.md.displayRunes(actualRow, m.lines[actualRow])
			segment := dispRunes[span.Start:span.End]
			b.WriteString(m.renderMdVisualLine(segment, actualRow, span.Start, segIdx, cursorVisible))
		} else {
			segment := m.lines[actualRow][span.Start:span.End]
			b.WriteString(m.renderVisualLine(segment, actualRow, span.Start, segIdx, cursorVisible))
		}
	}
	return b.String()
}

// renderVisualLine renders a single visual-line segment.
// segOffset is the rune offset within the actual line where this segment starts.
func (m *Model) renderVisualLine(segment []rune, actualRow, segOffset, segIdx int, cursorVisible bool) string {
	lineStr := string(segment)
	needsCursor := m.focused && actualRow == m.cursorRow &&
		m.cursorCol >= segOffset && m.cursorCol <= segOffset+len(segment)

	// localCol is the cursor column relative to this segment.
	localCol := m.cursorCol - segOffset

	// Line 0, first segment only: apply line styler / @agent / slash highlighting.
	isFirstSegment := actualRow == 0 && segIdx == 0
	if isFirstSegment && m.lineStyler != nil {
		return m.renderStyledLineSegment(segment, localCol, needsCursor, cursorVisible)
	}
	if isFirstSegment {
		if len(lineStr) > 0 && lineStr[0] == '/' {
			lineStr = m.highlightSlashCommand(lineStr)
		} else {
			lineStr = m.highlightAgentPrefix(lineStr)
		}
	}

	// Apply selection highlight if active.
	if m.sel.active {
		ghost := ""
		if needsCursor {
			ghost = m.completer.GhostSuffix()
		}
		return m.renderSelectionWithCursor(segment, actualRow, segOffset, needsCursor, localCol, cursorVisible, ghost)
	}

	// Ghost suffix appears only on the segment containing the cursor.
	ghost := ""
	if needsCursor {
		ghost = m.completer.GhostSuffix()
	}

	if !needsCursor {
		return lineStr
	}
	return m.renderCursorWithGhostLocal(segment, lineStr, localCol, cursorVisible, ghost)
}

// renderMdVisualLine renders a visual-line segment with markdown styling.
// The segment contains display runes (hidden delimiters removed).
// segOffset is the display-column offset within the display rune array.
func (m *Model) renderMdVisualLine(segment []rune, actualRow, segOffset, segIdx int, cursorVisible bool) string {
	if actualRow >= len(m.md.lines) {
		return m.renderVisualLine(segment, actualRow, segOffset, segIdx, cursorVisible)
	}
	ml := m.md.lines[actualRow]

	// Determine cursor position in display coordinates.
	rawCursorCol := m.cursorCol
	dispCursorCol := ml.rawColToDisplay(rawCursorCol)
	needsCursor := m.focused && actualRow == m.cursorRow &&
		dispCursorCol >= segOffset && dispCursorCol <= segOffset+len(segment)
	localCol := dispCursorCol - segOffset

	// Build styled output character by character.
	var b strings.Builder
	selStyle := lipgloss.NewStyle().Background(m.theme.Palette.Selection)
	cursorStyle := lipgloss.NewStyle().Reverse(true)

	for i, r := range segment {
		dispCol := segOffset + i
		kind := ml.regionAt(dispCol)
		ch := string(r)

		// Determine if this position is selected.
		isSel := m.sel.active && m.selContainsDisplay(actualRow, dispCol, ml)

		// Apply markdown style.
		styled := ch
		if kind != mdNone {
			styled = m.mdStyle.styleFor(kind).Render(ch)
		}

		// Apply selection overlay.
		if isSel {
			styled = selStyle.Render(ch)
		}

		// Apply cursor overlay.
		if needsCursor && cursorVisible && i == localCol {
			styled = cursorStyle.Render(ch)
		}

		b.WriteString(styled)
	}

	// Cursor at end of segment.
	if needsCursor && localCol >= len(segment) {
		ghost := m.completer.GhostSuffix()
		if len(ghost) > 0 {
			ghostRunes := []rune(ghost)
			mutedStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
			if cursorVisible {
				b.WriteString(cursorStyle.Render(string(ghostRunes[:1])))
			} else {
				b.WriteString(mutedStyle.Render(string(ghostRunes[:1])))
			}
			if len(ghostRunes) > 1 {
				b.WriteString(mutedStyle.Render(string(ghostRunes[1:])))
			}
		} else if cursorVisible {
			b.WriteString(cursorStyle.Render(" "))
		}
	}

	return b.String()
}

// selContainsDisplay checks if a display column on a given row is within the selection.
// It converts the display column back to raw coordinates for the selection check.
func (m *Model) selContainsDisplay(actualRow, dispCol int, ml mdLine) bool {
	rawCol := ml.displayColToRaw(dispCol)
	return m.sel.containsPos(m.cursorRow, m.cursorCol, actualRow, rawCol)
}

// applySelectionHighlight renders a segment with selection background highlight.
func (m *Model) applySelectionHighlight(segment []rune, actualRow, segOffset int) string {
	selStyle := lipgloss.NewStyle().Background(m.theme.Palette.Selection)

	var b strings.Builder
	inSel := false
	var run []rune

	flush := func() {
		if len(run) == 0 {
			return
		}
		s := string(run)
		if inSel {
			b.WriteString(selStyle.Render(s))
		} else {
			b.WriteString(s)
		}
		run = run[:0]
	}

	for i, r := range segment {
		col := segOffset + i
		sel := m.sel.containsPos(m.cursorRow, m.cursorCol, actualRow, col)
		if sel != inSel {
			flush()
			inSel = sel
		}
		run = append(run, r)
	}
	flush()
	return b.String()
}

// renderSelectionWithCursor renders a segment with selection background and
// cursor overlay in a single pass, preventing the cursor from stripping
// selection styling.
func (m *Model) renderSelectionWithCursor(segment []rune, actualRow, segOffset int, needsCursor bool, localCol int, cursorVisible bool, ghost string) string {
	selStyle := lipgloss.NewStyle().Background(m.theme.Palette.Selection)
	cursorStyle := lipgloss.NewStyle().Reverse(true)

	var b strings.Builder
	for i, r := range segment {
		col := segOffset + i
		ch := string(r)
		isSel := m.sel.containsPos(m.cursorRow, m.cursorCol, actualRow, col)

		if needsCursor && cursorVisible && i == localCol {
			b.WriteString(cursorStyle.Render(ch))
		} else if isSel {
			b.WriteString(selStyle.Render(ch))
		} else {
			b.WriteString(ch)
		}
	}

	// Cursor at end of segment.
	if needsCursor && localCol >= len(segment) {
		ghostRunes := []rune(ghost)
		mutedStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
		if len(ghostRunes) > 0 {
			if cursorVisible {
				b.WriteString(cursorStyle.Render(string(ghostRunes[:1])))
			} else {
				b.WriteString(mutedStyle.Render(string(ghostRunes[:1])))
			}
			if len(ghostRunes) > 1 {
				b.WriteString(mutedStyle.Render(string(ghostRunes[1:])))
			}
		} else if cursorVisible {
			b.WriteString(cursorStyle.Render(" "))
		}
	} else if needsCursor && len(ghost) > 0 {
		b.WriteString(m.renderGhost(ghost))
	}

	return b.String()
}

// renderGhost renders the ghost suffix in muted style, or "" if empty.
func (m *Model) renderGhost(suffix string) string {
	if suffix == "" {
		return ""
	}
	return lipgloss.NewStyle().Foreground(m.theme.Palette.Muted).Render(suffix)
}

// renderCursorWithGhostLocal renders the cursor and ghost text together,
// using localCol (cursor position relative to the segment start).
func (m *Model) renderCursorWithGhostLocal(seg []rune, segStr string, localCol int, cursorVisible bool, ghost string) string {
	ghostRunes := []rune(ghost)
	mutedStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	cursorStyle := lipgloss.NewStyle().Reverse(true)

	atEnd := localCol >= len(seg)

	// Cursor mid-segment: standard rendering, ghost appended after.
	if !atEnd {
		return renderCursorAt(seg, segStr, localCol, cursorVisible) + m.renderGhost(ghost)
	}

	// Cursor at end-of-segment with ghost text: use first ghost char as cursor.
	if len(ghostRunes) > 0 {
		tail := mutedStyle.Render(string(ghostRunes[1:]))
		if cursorVisible {
			return segStr + cursorStyle.Render(string(ghostRunes[:1])) + tail
		}
		return segStr + mutedStyle.Render(string(ghostRunes[:1])) + tail
	}

	// No ghost: standard end-of-segment cursor.
	if cursorVisible {
		return segStr + cursorStyle.Render(" ")
	}
	return segStr
}

// renderStyledLineSegment applies the lineStyler to a segment and appends hint text.
// localCol is the cursor column relative to the segment.
func (m *Model) renderStyledLineSegment(seg []rune, localCol int, needsCursor bool, cursorVisible bool) string {
	hintStyle := m.theme.Placeholder
	if needsCursor {
		styled, hint := renderStyledCursorAt(seg, localCol, m.lineStyler, cursorVisible)
		if hint != "" {
			return styled + hintStyle.Render(" "+hint)
		}
		return styled
	}
	styled, hint := m.lineStyler(string(seg))
	if hint != "" {
		return styled + hintStyle.Render(" "+hint)
	}
	return styled
}

// renderStyledCursorAt combines a line styler with cursor rendering at localCol.
func renderStyledCursorAt(seg []rune, localCol int, styler func(string) (string, string), cursorVisible bool) (string, string) {
	_, hint := styler(string(seg))
	textStyler := func(s string) string { r, _ := styler(s); return r }

	if !cursorVisible {
		return textStyler(string(seg)), hint
	}
	cursorStyle := lipgloss.NewStyle().Reverse(true)
	if localCol >= len(seg) {
		return textStyler(string(seg)) + cursorStyle.Render(" "), hint
	}
	before := textStyler(string(seg[:localCol]))
	under := cursorStyle.Render(string(seg[localCol : localCol+1]))
	after := textStyler(string(seg[localCol+1:]))
	return before + under + after, hint
}

// renderCursorAt inserts a visible block cursor at localCol within the segment.
func renderCursorAt(seg []rune, segStr string, localCol int, cursorVisible bool) string {
	if !cursorVisible {
		return segStr
	}
	cursorStyle := lipgloss.NewStyle().Reverse(true)
	if localCol >= len(seg) {
		return segStr + cursorStyle.Render(" ")
	}
	before := string(seg[:localCol])
	under := string(seg[localCol : localCol+1])
	after := string(seg[localCol+1:])
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

// highlightSlashCommand styles the leading /command token. It highlights
// when the command exactly matches a known slash command, or when the
// completer popup is active for a "/" prefix (partial match in progress).
func (m *Model) highlightSlashCommand(lineStr string) string {
	if m.slashValidator == nil {
		return lineStr
	}
	cmd := detectSlashCommand(lineStr)
	if cmd == "" {
		return lineStr
	}
	matched := m.slashValidator(cmd)
	completing := m.completer.IsActive() && strings.HasPrefix(m.completer.Prefix(), "/")
	if !matched && !completing {
		return lineStr
	}
	style := lipgloss.NewStyle().Foreground(m.theme.Palette.Secondary).Bold(true)
	tag := "/" + cmd
	return style.Render(tag) + lineStr[len(tag):]
}

// detectSlashCommand returns the command name (without "/") if text begins
// with an unquoted /word token, or "" otherwise.
func detectSlashCommand(text string) string {
	trimmed := strings.TrimLeft(text, " \t")
	if len(trimmed) < 2 {
		return ""
	}
	// Reject text enclosed in quotes.
	if trimmed[0] == '"' || trimmed[0] == '\'' {
		return ""
	}
	if trimmed[0] != '/' {
		return ""
	}
	end := 1
	for end < len(trimmed) && trimmed[end] != ' ' && trimmed[end] != '\t' && trimmed[end] != '\n' {
		end++
	}
	if end <= 1 {
		return ""
	}
	cmd := strings.ToLower(trimmed[1:end])
	for _, r := range cmd {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_', r == '-':
		default:
			return ""
		}
	}
	return cmd
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
	token, ok := normalizeAgentPrefixToken(text[1:end])
	if !ok {
		// Preserve raw input for Guide DSL commands like "@to:agent ..."
		// and other non-simple @ syntaxes.
		return "", text
	}
	agentName = token
	remainder = strings.TrimLeft(text[end:], " \t")
	return agentName, remainder
}

func normalizeAgentPrefixToken(token string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(token))
	if normalized == "" {
		return "", false
	}
	if !isAgentPrefixToken(normalized) {
		return "", false
	}
	return normalized, true
}

func isAgentPrefixToken(token string) bool {
	for _, r := range token {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}
