// Package editor provides the root editor model that composes the buffer,
// mode, highlight, and statusline subsystems into a full-screen editor
// overlay for the TUI.
package editor

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/editor/buffer"
	"github.com/adalundhe/sylk/ui/editor/highlight"
	"github.com/adalundhe/sylk/ui/editor/mode"
	"github.com/adalundhe/sylk/ui/editor/statusline"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
)

// gutterPadding is the number of spaces after the line number.
const gutterPadding = 1

// statusLineHeight is the number of terminal rows reserved for the status
// line at the bottom of the editor viewport.
const statusLineHeight = 1

// Model is the root editor component. It composes the piece-table buffer,
// vim-mode handling, syntax highlighting, and status line into a single
// full-screen overlay.
type Model struct {
	// Text storage and metadata.
	buf       *buffer.PieceTable
	lineIndex *buffer.LineIndex
	undoTree  *buffer.UndoTree
	state     *mode.EditorState

	// Modal subsystems.
	currentMode mode.Mode
	normalMode  *mode.NormalMode
	insertMode  *mode.InsertMode

	// Rendering.
	highlighter *highlight.Highlighter
	statusLine  *statusline.StatusLine
	regions     [][]highlight.HighlightRegion

	// File metadata.
	filePath string
	language string
	modified bool

	// Viewport.
	scrollOffset int
	width        int
	height       int

	// Focus.
	focused bool
	theme   *theme.Theme
}

// Compile-time interface checks.
var (
	_ component.Focusable = (*Model)(nil)
	_ component.Resizable = (*Model)(nil)
)

// New creates a new editor model.
func New(th *theme.Theme) *Model {
	pt := buffer.NewPieceTable("")
	li := buffer.NewLineIndex(pt)
	ut := buffer.NewUndoTree(0) // use default max nodes

	st := &mode.EditorState{
		Buffer:    pt,
		LineIndex: li,
		UndoTree:  ut,
	}
	return &Model{
		buf:         pt,
		lineIndex:   li,
		undoTree:    ut,
		state:       st,
		currentMode: mode.ModeNormal,
		normalMode:  mode.NewNormalMode(th),
		insertMode:  mode.NewInsertMode(th),
		highlighter: highlight.NewHighlighter(th),
		statusLine:  statusline.New(th),
		theme:       th,
	}
}

// OpenFile loads content into the editor.
func (m *Model) OpenFile(path, content, language string) {
	m.filePath = path
	m.language = language
	m.modified = false
	m.scrollOffset = 0

	m.buf = buffer.NewPieceTable(content)
	m.lineIndex = buffer.NewLineIndex(m.buf)
	m.undoTree = buffer.NewUndoTree(0)

	m.state.Buffer = m.buf
	m.state.LineIndex = m.lineIndex
	m.state.UndoTree = m.undoTree
	m.state.Cursor = 0
	m.state.SyncCursorPos()

	m.regions = m.highlighter.Highlight(content, language)
	m.syncStatusLine()
}

// ---------------------------------------------------------------------------
// component.Component
// ---------------------------------------------------------------------------

// Init performs no initialisation work.
func (m *Model) Init() tea.Cmd { return nil }

// Update handles messages dispatched to the editor.
func (m *Model) Update(incoming tea.Msg) (component.Component, tea.Cmd) {
	handler, ok := msgHandlerTable[msgType(incoming)]
	if !ok {
		return m, nil
	}
	return handler(m, incoming)
}

// View renders the editor viewport and status line.
func (m *Model) View() string {
	viewHeight := m.viewportHeight()
	if viewHeight <= 0 {
		return m.statusLine.View(m.width)
	}
	m.adjustScroll(viewHeight)
	lines := m.renderVisibleLines(viewHeight)
	pad := viewHeight - len(lines)
	for i := 0; i < pad; i++ {
		lines = append(lines, m.renderTildeLine())
	}
	body := strings.Join(lines, "\n")
	return body + "\n" + m.statusLine.View(m.width)
}

// ---------------------------------------------------------------------------
// component.Focusable
// ---------------------------------------------------------------------------

func (m *Model) ID() component.FocusID  { return component.FocusEditor }
func (m *Model) Focused() bool           { return m.focused }
func (m *Model) SetFocused(focused bool) { m.focused = focused }

// ---------------------------------------------------------------------------
// component.Resizable
// ---------------------------------------------------------------------------

func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
}

// ---------------------------------------------------------------------------
// Message dispatch (table-driven)
// ---------------------------------------------------------------------------

type msgKind int

const (
	msgKindOpenEditor msgKind = iota
	msgKindCloseEditor
	msgKindKeyMsg
	msgKindUnknown
)

func msgType(incoming tea.Msg) msgKind {
	switch incoming.(type) {
	case msg.OpenEditorMsg:
		return msgKindOpenEditor
	case msg.CloseEditorMsg:
		return msgKindCloseEditor
	case tea.KeyMsg:
		return msgKindKeyMsg
	default:
		return msgKindUnknown
	}
}

type msgHandler func(m *Model, incoming tea.Msg) (component.Component, tea.Cmd)

var msgHandlerTable = map[msgKind]msgHandler{
	msgKindOpenEditor:  handleOpenEditor,
	msgKindCloseEditor: handleCloseEditor,
	msgKindKeyMsg:      handleKeyMsg,
}

func handleOpenEditor(m *Model, incoming tea.Msg) (component.Component, tea.Cmd) {
	o := incoming.(msg.OpenEditorMsg)
	lang := detectLanguage(o.FilePath)
	m.OpenFile(o.FilePath, o.Content, lang)
	return m, nil
}

func handleCloseEditor(m *Model, _ tea.Msg) (component.Component, tea.Cmd) {
	return m, nil
}

func handleKeyMsg(m *Model, incoming tea.Msg) (component.Component, tea.Cmd) {
	key := incoming.(tea.KeyMsg)
	prevModified := m.buf.Length()
	next, cmd := m.dispatchKey(key)
	if next != m.currentMode {
		m.currentMode = next
	}
	if m.buf.Length() != prevModified {
		m.modified = true
		m.rehighlight()
	}
	m.syncStatusLine()
	return m, cmd
}

// ---------------------------------------------------------------------------
// Key dispatch
// ---------------------------------------------------------------------------

// keyDispatchTable maps modes to their handler functions.
var keyDispatchTable = map[mode.Mode]func(m *Model, key tea.KeyMsg) (mode.Mode, tea.Cmd){
	mode.ModeNormal: dispatchNormal,
	mode.ModeInsert: dispatchInsert,
}

func (m *Model) dispatchKey(key tea.KeyMsg) (mode.Mode, tea.Cmd) {
	fn, ok := keyDispatchTable[m.currentMode]
	if !ok {
		return m.currentMode, nil
	}
	return fn(m, key)
}

func dispatchNormal(m *Model, key tea.KeyMsg) (mode.Mode, tea.Cmd) {
	return m.normalMode.HandleKey(key, m.state)
}

func dispatchInsert(m *Model, key tea.KeyMsg) (mode.Mode, tea.Cmd) {
	return m.insertMode.HandleKey(key, m.state)
}

// ---------------------------------------------------------------------------
// Rendering helpers
// ---------------------------------------------------------------------------

func (m *Model) viewportHeight() int {
	return max(m.height-statusLineHeight, 0)
}

func (m *Model) adjustScroll(viewHeight int) {
	cursorLine := m.state.CursorLine
	if cursorLine < m.scrollOffset {
		m.scrollOffset = cursorLine
	}
	if cursorLine >= m.scrollOffset+viewHeight {
		m.scrollOffset = cursorLine - viewHeight + 1
	}
}

func (m *Model) renderVisibleLines(viewHeight int) []string {
	content := m.buf.Content()
	contentLines := strings.Split(content, "\n")
	totalLines := len(contentLines)
	gutterWidth := m.gutterWidth(totalLines)
	end := min(m.scrollOffset+viewHeight, totalLines)
	defaultStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Foreground)
	result := make([]string, 0, end-m.scrollOffset)
	for i := m.scrollOffset; i < end; i++ {
		gutter := m.renderGutter(i, gutterWidth)
		var regions []highlight.HighlightRegion
		if i < len(m.regions) {
			regions = m.regions[i]
		}
		lineText := highlight.RenderLine(contentLines[i], regions, m.theme.Syntax, defaultStyle)
		rendered := gutter + lineText
		// Add cursor indicator.
		if i == m.state.CursorLine {
			rendered = m.applyCursor(contentLines[i], regions, gutterWidth, defaultStyle)
		}
		result = append(result, rendered)
	}
	return result
}

func (m *Model) applyCursor(line string, regions []highlight.HighlightRegion, gutterWidth int, defaultStyle lipgloss.Style) string {
	gutter := m.renderGutter(m.state.CursorLine, gutterWidth)
	runes := []rune(line)
	col := m.state.CursorCol
	cursorStyle := lipgloss.NewStyle().Reverse(true)
	// Split into before-cursor, cursor-char, and after-cursor.
	beforeEnd := min(col, len(runes))
	afterStart := min(col+1, len(runes))
	before := string(runes[:beforeEnd])
	cursorChar := " "
	if col < len(runes) {
		cursorChar = string(runes[col])
	}
	after := string(runes[afterStart:])
	beforeStyled := highlight.RenderLine(before, filterRegions(regions, 0, beforeEnd), m.theme.Syntax, defaultStyle)
	afterStyled := highlight.RenderLine(after, shiftRegions(filterRegions(regions, afterStart, len(runes)), -afterStart), m.theme.Syntax, defaultStyle)
	return gutter + beforeStyled + cursorStyle.Render(cursorChar) + afterStyled
}

func (m *Model) renderGutter(lineNum, gutterWidth int) string {
	gutterStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	numStr := fmt.Sprintf("%*d", gutterWidth-gutterPadding, lineNum+1)
	return gutterStyle.Render(numStr) + strings.Repeat(" ", gutterPadding)
}

func (m *Model) renderTildeLine() string {
	tildeStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	return tildeStyle.Render("~")
}

func (m *Model) gutterWidth(totalLines int) int {
	digits := 1
	n := totalLines
	for n >= 10 {
		digits++
		n /= 10
	}
	return digits + gutterPadding
}

// ---------------------------------------------------------------------------
// Highlight region helpers
// ---------------------------------------------------------------------------

// filterRegions returns regions that overlap with [startCol, endCol).
func filterRegions(regions []highlight.HighlightRegion, startCol, endCol int) []highlight.HighlightRegion {
	var result []highlight.HighlightRegion
	for _, r := range regions {
		if r.EndCol <= startCol || r.StartCol >= endCol {
			continue
		}
		clamped := highlight.HighlightRegion{
			StartCol: max(r.StartCol, startCol) - startCol,
			EndCol:   min(r.EndCol, endCol) - startCol,
			Category: r.Category,
		}
		result = append(result, clamped)
	}
	return result
}

// shiftRegions adjusts all region columns by delta.
func shiftRegions(regions []highlight.HighlightRegion, delta int) []highlight.HighlightRegion {
	result := make([]highlight.HighlightRegion, len(regions))
	for i, r := range regions {
		result[i] = highlight.HighlightRegion{
			StartCol: r.StartCol + delta,
			EndCol:   r.EndCol + delta,
			Category: r.Category,
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// State sync helpers
// ---------------------------------------------------------------------------

func (m *Model) syncStatusLine() {
	m.statusLine.SetMode(m.currentMode)
	m.statusLine.SetFile(m.filePath, m.language)
	m.statusLine.SetPosition(m.state.CursorLine, m.state.CursorCol, m.lineIndex.Count())
	m.statusLine.SetModified(m.modified)
}

func (m *Model) rehighlight() {
	m.regions = m.highlighter.Highlight(m.buf.Content(), m.language)
}

// ---------------------------------------------------------------------------
// Language detection
// ---------------------------------------------------------------------------

// extToLang maps file extensions to language identifiers.
var extToLang = map[string]string{
	".go":   "go",
	".py":   "python",
	".js":   "javascript",
	".ts":   "typescript",
	".tsx":  "typescript",
	".jsx":  "javascript",
	".rs":   "rust",
	".rb":   "ruby",
	".java": "java",
	".c":    "c",
	".cpp":  "cpp",
	".h":    "c",
	".hpp":  "cpp",
	".md":   "markdown",
	".yaml": "yaml",
	".yml":  "yaml",
	".json": "json",
	".toml": "toml",
	".sql":  "sql",
	".sh":   "bash",
	".bash": "bash",
}

func detectLanguage(path string) string {
	for ext, lang := range extToLang {
		if strings.HasSuffix(path, ext) {
			return lang
		}
	}
	return ""
}
