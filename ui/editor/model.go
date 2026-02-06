// Package editor provides the root editor model that composes the buffer,
// mode, highlight, and statusline subsystems into a full-screen editor
// overlay for the TUI.
package editor

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/core/lsp"
	"github.com/adalundhe/sylk/core/treesitter"
	"github.com/adalundhe/sylk/ui/component"
	"github.com/adalundhe/sylk/ui/editor/buffer"
	"github.com/adalundhe/sylk/ui/editor/completion"
	"github.com/adalundhe/sylk/ui/editor/findbar"
	"github.com/adalundhe/sylk/ui/editor/highlight"
	"github.com/adalundhe/sylk/ui/editor/hover"
	"github.com/adalundhe/sylk/ui/editor/mode"
	"github.com/adalundhe/sylk/ui/editor/signature"
	"github.com/adalundhe/sylk/ui/editor/motion"
	"github.com/adalundhe/sylk/ui/editor/statusline"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
)

// gutterPadding is the number of spaces after the line number.
const gutterPadding = 1

// editorTabWidth is the number of spaces per tab stop for rendering.
const editorTabWidth = 4

// statusLineHeight is the number of terminal rows reserved for the status
// line at the bottom of the editor viewport.
const statusLineHeight = 1

// blinkHalfPeriod is the duration between cursor visibility toggles.
const blinkHalfPeriod = 530 * time.Millisecond

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
	visualMode  *mode.VisualMode

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
	bounceOffset int
	width        int
	height       int

	// LSP diagnostics for the current file.
	diagnostics []lsp.Diagnostic

	// Tree-sitter parse errors, updated on every rehighlight.
	parseErrors []treesitter.ParseError

	// LSP change tracking for debounced didChange notifications.
	lspDirty       bool      // buffer changed since last didChange flush
	lastEditAt     time.Time // timestamp of last buffer modification
	editGeneration int       // monotonic counter for stale-response detection

	// LSP hover tooltip.
	hoverPopup  *hover.Hover
	hoverActive bool

	// LSP signature help popup.
	sigHelp       *signature.SignatureHelp
	sigHelpActive bool

	// LSP document highlights (same-symbol occurrences).
	highlightRanges []lsp.DocumentHighlight

	// Jump marker: visual indicator set after jumping to a symbol/reference.
	jumpLine     int  // 0-indexed target line.
	jumpStartCol int  // 0-indexed start column (rune offset).
	jumpEndCol   int  // 0-indexed end column (exclusive, rune offset).
	jumpActive   bool // Whether the jump marker is visible.

	// Completion popup.
	completionEngine *completion.Engine
	lspSource        *completion.LSPSource

	// Navigation jump list for go-back/forward.
	jumpList *motion.JumpList

	// Find bar (in-file search).
	findBar    *findbar.FindBar
	findActive bool

	// Focus and cursor blink.
	focused      bool
	cursorBlink  bool      // current blink phase (true = visible)
	lastBlinkAt  time.Time // timestamp of the last blink toggle
	theme        *theme.Theme
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
	lspSrc := completion.NewLSPSource()
	registry := completion.NewSourceRegistry()
	registry.Register(completion.BufferWordSource{})
	registry.Register(lspSrc)
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
		jumpList:         motion.NewJumpList(),
		hoverPopup:       hover.New(),
		sigHelp:          signature.New(),
		completionEngine: completion.NewEngine(registry),
		lspSource:        lspSrc,
		cursorBlink:      true,
		lastBlinkAt: time.Now(),
		theme:       th,
	}
}

// OpenFile loads content into the editor.
func (m *Model) OpenFile(path, content, language string) {
	m.filePath = path
	m.language = language
	m.modified = false
	m.lspDirty = false
	m.lastEditAt = time.Time{}
	m.diagnostics = nil
	m.parseErrors = nil
	m.highlightRanges = nil
	m.jumpActive = false
	m.scrollOffset = 0
	m.DismissHover()
	m.DismissSignatureHelp()

	m.buf = buffer.NewPieceTable(content)
	m.lineIndex = buffer.NewLineIndex(m.buf)
	m.undoTree = buffer.NewUndoTree(0)

	m.state.Buffer = m.buf
	m.state.LineIndex = m.lineIndex
	m.state.UndoTree = m.undoTree
	m.state.Cursor = 0
	m.state.SyncCursorPos()

	m.regions = m.highlighter.Highlight(content, language)
	m.collectParseErrors()
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
	if m.filePath == "" {
		return m.renderPlaceholder()
	}

	viewHeight := m.viewportHeight()

	// Reserve space for the find bar when open.
	findBarStr := ""
	findBarH := 0
	if m.findActive && m.findBar != nil {
		findBarStr = m.findBar.View(m.width, m.theme, m.cursorBlink)
		findBarH = m.findBar.Height()
		viewHeight -= findBarH
	}

	if viewHeight <= 0 {
		if findBarStr != "" {
			return findBarStr + "\n" + m.statusLine.View(m.width)
		}
		return m.statusLine.View(m.width)
	}
	m.adjustScroll(viewHeight)
	lines := m.renderVisibleLines(viewHeight)
	for len(lines) < viewHeight {
		lines = append(lines, m.renderTildeLine())
	}
	// Fit each line to panel width (truncate or pad).
	for i, line := range lines {
		lines[i] = m.fitLine(line)
	}
	// Apply bounce shift for overscroll feedback.
	lines = applyBounceShift(lines, m.bounceOffset, viewHeight)

	// Overlay all active popups with collision detection.
	lines = m.overlayPopups(lines, viewHeight)

	body := strings.Join(lines, "\n")
	if findBarStr != "" {
		return findBarStr + "\n" + body + "\n" + m.statusLine.View(m.width)
	}
	return body + "\n" + m.statusLine.View(m.width)
}

// renderPlaceholder renders a vertically and horizontally centered placeholder
// message when no file is open.
func (m *Model) renderPlaceholder() string {
	const placeholder = "Open any file to edit."
	style := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)

	textWidth := lipgloss.Width(placeholder)
	padLeft := max((m.width-textWidth)/2, 0)
	centeredLine := strings.Repeat(" ", padLeft) + style.Render(placeholder)

	lines := make([]string, m.height)
	midRow := m.height / 2
	for i := range lines {
		if i == midRow {
			lines[i] = centeredLine
		}
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// component.Focusable
// ---------------------------------------------------------------------------

func (m *Model) ID() component.FocusID  { return component.FocusEditor }
func (m *Model) Focused() bool           { return m.focused }
func (m *Model) SetFocused(focused bool) {
	if !focused && isVisualMode(m.currentMode) {
		m.visualMode = nil
		m.currentMode = mode.ModeNormal
	}
	m.focused = focused
}

// ---------------------------------------------------------------------------
// Public accessors
// ---------------------------------------------------------------------------

// CurrentMode returns the active editor mode (Normal, Insert, etc.).
func (m *Model) CurrentMode() mode.Mode { return m.currentMode }

// Content materializes and returns the full buffer text.
func (m *Model) Content() string { return m.buf.Content() }

// Modified reports whether the buffer has unsaved changes.
func (m *Model) Modified() bool { return m.modified }

// FilePath returns the current file path.
func (m *Model) FilePath() string { return m.filePath }

// TabWidth returns the editor's tab stop width for formatting requests.
func (m *Model) TabWidth() int { return editorTabWidth }

// ClearFile resets the editor to an empty state with no file loaded.
func (m *Model) ClearFile() {
	m.filePath = ""
	m.language = ""
	m.modified = false
	m.lspDirty = false
	m.diagnostics = nil
	m.parseErrors = nil
	m.highlightRanges = nil
	m.jumpActive = false
	m.scrollOffset = 0
	m.DismissSignatureHelp()
	m.buf = buffer.NewPieceTable("")
	m.regions = nil
}

// HasSelection reports whether any selection (keyboard or mouse) is active.
func (m *Model) HasSelection() bool { return m.visualMode != nil }

// IsNormalMode reports whether the editor is in normal mode.
func (m *Model) IsNormalMode() bool { return m.currentMode == mode.ModeNormal }

// CursorPos returns the absolute rune-offset cursor position.
func (m *Model) CursorPos() int { return m.state.Cursor }

// ScrollOffset returns the first visible line index.
func (m *Model) ScrollOffset() int { return m.scrollOffset }

// Language returns the language identifier for the current file.
func (m *Model) Language() string { return m.language }

// CompletionActive reports whether the completion popup is currently visible.
func (m *Model) CompletionActive() bool { return m.completionEngine.Active() }

// LSPDirty reports whether the buffer has changed since the last didChange flush.
func (m *Model) LSPDirty() bool { return m.lspDirty }

// LastEditAt returns the timestamp of the last buffer modification.
func (m *Model) LastEditAt() time.Time { return m.lastEditAt }

// ClearLSPDirty resets the dirty flag after a didChange is sent.
func (m *Model) ClearLSPDirty() { m.lspDirty = false }

// EditGeneration returns a monotonic counter that increments on every
// buffer mutation. Used to detect stale LSP responses.
func (m *Model) EditGeneration() int { return m.editGeneration }

// CursorLine returns the 0-indexed line number of the cursor.
func (m *Model) CursorLine() int { return m.state.CursorLine }

// CursorCol returns the 0-indexed column offset of the cursor within its line.
func (m *Model) CursorCol() int { return m.state.CursorCol }

// GoToLine moves the cursor to the beginning of the given 0-indexed line
// and adjusts the scroll offset so the line is visible.
func (m *Model) GoToLine(line int) {
	m.state.Cursor = m.lineIndex.LineColToPos(line, 0)
	m.state.ClampCursor(1)
	vh := m.viewportHeight()
	if vh > 0 {
		// Center the target line in the viewport.
		m.scrollOffset = max(m.state.CursorLine-vh/2, 0)
	}
	m.syncStatusLine()
}

// SetJumpMarker sets a visual marker on the given line/column range.
// The marker persists until the next cursor movement or buffer change.
func (m *Model) SetJumpMarker(line, startCol, endCol int) {
	m.jumpLine = line
	m.jumpStartCol = startCol
	m.jumpEndCol = endCol
	m.jumpActive = true
}

// ClearJumpMarker removes the jump marker.
func (m *Model) ClearJumpMarker() {
	m.jumpActive = false
}

// ---------------------------------------------------------------------------
// Hover
// ---------------------------------------------------------------------------

// ShowHover displays the hover tooltip with the given content at the cursor.
func (m *Model) ShowHover(contents string) {
	m.ShowHoverAt(contents, m.state.CursorLine, m.state.CursorCol)
}

// ShowHoverAt displays the hover tooltip at a specific line/col anchor.
func (m *Model) ShowHoverAt(contents string, line, col int) {
	if contents == "" {
		return
	}
	m.hoverPopup.Show(contents, line, col)
	m.hoverActive = true
}

// DismissHover hides the hover tooltip.
func (m *Model) DismissHover() {
	m.hoverPopup.Dismiss()
	m.hoverActive = false
}

// HoverActive reports whether the hover tooltip is visible.
func (m *Model) HoverActive() bool { return m.hoverActive }

// ScrollHoverDown scrolls the hover popup content down by one line.
func (m *Model) ScrollHoverDown() { m.hoverPopup.ScrollDown() }

// ScrollHoverUp scrolls the hover popup content up by one line.
func (m *Model) ScrollHoverUp() { m.hoverPopup.ScrollUp() }

// SetHoverDefinition stores the qualified symbol name and package path
// on the active hover tooltip for display in the footer.
func (m *Model) SetHoverDefinition(symbol, pkgPath string) {
	if !m.hoverActive {
		return
	}
	m.hoverPopup.SetDefinition(symbol, pkgPath)
}

// HoverAnchorLine returns the line the hover popup is anchored to.
func (m *Model) HoverAnchorLine() int { return m.hoverPopup.AnchorLine() }

// HoverAnchorCol returns the column the hover popup is anchored to.
func (m *Model) HoverAnchorCol() int { return m.hoverPopup.AnchorCol() }

// ---------------------------------------------------------------------------
// Signature Help
// ---------------------------------------------------------------------------

// ShowSignatureHelp displays the signature help popup at the current cursor.
// A nil or empty result dismisses the popup.
func (m *Model) ShowSignatureHelp(result *lsp.SignatureHelp) {
	if result == nil || len(result.Signatures) == 0 {
		m.DismissSignatureHelp()
		return
	}
	m.sigHelp.Show(result, m.state.CursorLine, m.state.CursorCol)
	m.sigHelpActive = true
}

// DismissSignatureHelp hides the signature help popup.
func (m *Model) DismissSignatureHelp() {
	m.sigHelp.Dismiss()
	m.sigHelpActive = false
}

// SignatureHelpActive reports whether the signature popup is visible.
func (m *Model) SignatureHelpActive() bool { return m.sigHelpActive }

// ---------------------------------------------------------------------------
// Document Highlights
// ---------------------------------------------------------------------------

// SetHighlightRanges replaces the current document highlight ranges.
func (m *Model) SetHighlightRanges(ranges []lsp.DocumentHighlight) {
	m.highlightRanges = ranges
}

// ClearHighlightRanges removes all document highlight ranges.
func (m *Model) ClearHighlightRanges() {
	m.highlightRanges = nil
}

// PushJump saves the current cursor position in the jump list so the user
// can return with Ctrl+O.
func (m *Model) PushJump() {
	m.jumpList.Push(motion.JumpEntry{
		File: m.filePath,
		Line: m.state.CursorLine,
		Col:  m.state.CursorCol,
	})
}

// WordAt returns the text of the word at (line, col), or "" if not on a word.
func (m *Model) WordAt(line, col int) string {
	start, end := m.WordBoundsAt(line, col)
	if start == end {
		return ""
	}
	info, ok := m.lineIndex.Get(line)
	if !ok {
		return ""
	}
	var b strings.Builder
	for c := start; c < end; c++ {
		b.WriteRune(m.buf.RuneAt(info.StartPos + c))
	}
	return b.String()
}

// handleHoverKey processes a key event while the hover popup is active.
// Returns (cmd, true) when the key was consumed by the hover.
func (m *Model) handleHoverKey(key tea.KeyMsg) (tea.Cmd, bool) {
	switch {
	case key.Type == tea.KeyEsc:
		m.DismissHover()
		return nil, true
	case len(key.Runes) == 1 && key.Runes[0] == 'j':
		m.hoverPopup.ScrollDown()
		return nil, true
	case len(key.Runes) == 1 && key.Runes[0] == 'k':
		m.hoverPopup.ScrollUp()
		return nil, true
	case key.Type == tea.KeyEnter && m.currentMode == mode.ModeNormal:
		anchorLine := m.hoverPopup.AnchorLine()
		anchorCol := m.hoverPopup.AnchorCol()
		filePath := m.filePath
		m.jumpList.Push(motion.JumpEntry{
			File: m.filePath,
			Line: m.state.CursorLine,
			Col:  m.state.CursorCol,
		})
		m.DismissHover()
		return func() tea.Msg {
			return msg.LSPDefinitionRequestMsg{
				FilePath: filePath,
				Line:     anchorLine,
				Col:      anchorCol,
			}
		}, true
	default:
		m.DismissHover()
		return nil, false
	}
}

// IsWordCharAtPos reports whether the character at (line, col) is a word
// character (letter, digit, or underscore). Returns false if the position
// is beyond the line content.
func (m *Model) IsWordCharAtPos(line, col int) bool {
	info, ok := m.lineIndex.Get(line)
	if !ok || col >= info.Length {
		return false
	}
	r := m.buf.RuneAt(info.StartPos + col)
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// WordBoundsAt returns the start and end column of the word at (line, col).
// If the position is not on a word character, returns (col, col).
func (m *Model) WordBoundsAt(line, col int) (start, end int) {
	info, ok := m.lineIndex.Get(line)
	if !ok || col >= info.Length {
		return col, col
	}
	isWord := func(c int) bool {
		if c < 0 || c >= info.Length {
			return false
		}
		r := m.buf.RuneAt(info.StartPos + c)
		return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
	}
	if !isWord(col) {
		return col, col
	}
	start = col
	for start > 0 && isWord(start-1) {
		start--
	}
	end = col + 1
	for end < info.Length && isWord(end) {
		end++
	}
	return start, end
}

// IsInsideHoverPopup reports whether viewport-local (x, y) falls within the
// currently displayed hover popup, using the actual rendered dimensions.
func (m *Model) IsInsideHoverPopup(x, y int) bool {
	viewHeight := m.viewportHeight()
	_, hov, _ := m.popupLayout(viewHeight)
	return insidePopup(hov, x, y)
}

// IsInsideOverlayPopup reports whether viewport-local (x, y) falls within
// any non-hover overlay popup (signature help or completion), using actual
// rendered dimensions. Prevents hover dismissal when the mouse crosses an
// overlay on its way to the tooltip.
func (m *Model) IsInsideOverlayPopup(x, y int) bool {
	viewHeight := m.viewportHeight()
	sig, _, comp := m.popupLayout(viewHeight)
	return insidePopup(sig, x, y) || insidePopup(comp, x, y)
}

// insidePopup checks whether (x, y) falls within a popup's actual bounds.
func insidePopup(p popupPlacement, x, y int) bool {
	if p.content == "" {
		return false
	}
	return inBounds(y, p.start, p.start+p.height) && inBounds(x, 0, p.width)
}

// inBounds reports whether v is in the half-open range [lo, hi).
func inBounds(v, lo, hi int) bool { return v >= lo && v < hi }

// jumpBack navigates to the previous position in the jump list (Ctrl+O).
func (m *Model) jumpBack() (component.Component, tea.Cmd) {
	entry, ok := m.jumpList.Back()
	if !ok {
		return m, nil
	}
	return m.jumpTo(entry)
}

// jumpForward navigates to the next position in the jump list (Ctrl+I).
func (m *Model) jumpForward() (component.Component, tea.Cmd) {
	entry, ok := m.jumpList.Forward()
	if !ok {
		return m, nil
	}
	return m.jumpTo(entry)
}

// jumpTo navigates to a jump list entry. Same-file jumps move the cursor;
// cross-file jumps emit a FileOpenMsg.
func (m *Model) jumpTo(entry motion.JumpEntry) (component.Component, tea.Cmd) {
	if entry.File == m.filePath {
		m.state.Cursor = m.lineIndex.LineColToPos(entry.Line, entry.Col)
		m.state.SyncCursorPos()
		m.syncStatusLine()
		return m, nil
	}
	return m, func() tea.Msg {
		return msg.FileOpenMsg{
			Path:     entry.File,
			Name:     entry.File,
			Language: detectLanguage(entry.File),
			Line:     entry.Line + 1, // 1-based for FileOpenMsg
		}
	}
}

// ---------------------------------------------------------------------------
// Unified Popup Layout — collision detection
// ---------------------------------------------------------------------------

// popupPlacement describes a rendered popup's viewport position and size.
type popupPlacement struct {
	content string // rendered popup text ("" = inactive)
	start   int    // first viewport row
	height  int    // number of viewport rows occupied
	width   int    // visual width in columns (derived from rendered content)
}

// newPopupPlacement derives height and width from actual rendered content.
func newPopupPlacement(content string, start int) popupPlacement {
	if content == "" {
		return popupPlacement{}
	}
	firstLine := strings.SplitN(content, "\n", 2)[0]
	return popupPlacement{
		content: content,
		start:   start,
		height:  strings.Count(content, "\n") + 1,
		width:   lipgloss.Width(firstLine),
	}
}

// rowInterval is a half-open viewport row range [start, end).
type rowInterval struct {
	start, end int
}

// popupLayout computes collision-free viewport positions for all active
// popups (signature help, hover tooltip, completion).
func (m *Model) popupLayout(viewHeight int) (sig, hover, comp popupPlacement) {
	cursorRow := m.contentLineToViewRow(m.state.CursorLine)
	sig = m.sigHelpPlacement(cursorRow)
	comp = m.completionPlacement(cursorRow, viewHeight)
	hover = m.hoverPlacement(viewHeight, cursorRow, sig, comp)
	return
}

// sigHelpPlacement renders signature help and positions it above the cursor.
func (m *Model) sigHelpPlacement(cursorRow int) popupPlacement {
	if !m.sigHelpActive || !m.sigHelp.Active() {
		return popupPlacement{}
	}
	popup := m.sigHelp.View(m.width, m.theme)
	p := newPopupPlacement(popup, 0)
	p.start = max(cursorRow-p.height, 0)
	return p
}

// completionPlacement renders completion and positions it below the cursor.
func (m *Model) completionPlacement(cursorRow, viewHeight int) popupPlacement {
	if !m.completionEngine.Active() {
		return popupPlacement{}
	}
	start := cursorRow + 1
	avail := viewHeight - start
	if avail <= 0 {
		return popupPlacement{}
	}
	popup := m.completionEngine.Render(m.width, avail, m.theme)
	if popup == "" {
		return popupPlacement{}
	}
	return newPopupPlacement(popup, start)
}

// hoverPlacement positions hover in the largest free viewport zone,
// avoiding signature help, the cursor row, and completion.
func (m *Model) hoverPlacement(viewHeight, cursorRow int, sig, comp popupPlacement) popupPlacement {
	if !m.hoverActive || !m.hoverPopup.Active() {
		return popupPlacement{}
	}
	anchorRow := m.contentLineToViewRow(m.hoverPopup.AnchorLine())
	zones := hoverFreeZones(viewHeight, cursorRow, anchorRow, sig, comp)
	return m.placeHoverInZones(zones, anchorRow)
}

// hoverFreeZones builds reserved intervals (sig, cursor, anchor, comp) and
// returns the free gaps between them within [0, viewHeight). The anchor row
// is reserved so the hover popup never covers the text being hovered.
func hoverFreeZones(viewHeight, cursorRow, anchorRow int, sig, comp popupPlacement) []rowInterval {
	reserved := make([]rowInterval, 0, 4)
	if sig.height > 0 {
		reserved = append(reserved, rowInterval{sig.start, sig.start + sig.height})
	}
	reserved = append(reserved, rowInterval{cursorRow, cursorRow + 1})
	if anchorRow != cursorRow {
		reserved = append(reserved, rowInterval{anchorRow, anchorRow + 1})
	}
	if comp.height > 0 {
		reserved = append(reserved, rowInterval{comp.start, comp.start + comp.height})
	}
	slices.SortFunc(reserved, func(a, b rowInterval) int {
		return cmp.Compare(a.start, b.start)
	})
	return gapIntervals(reserved, viewHeight)
}

// gapIntervals returns the free intervals between sorted, non-overlapping
// reserved intervals within [0, viewHeight).
func gapIntervals(sorted []rowInterval, viewHeight int) []rowInterval {
	gaps := make([]rowInterval, 0, len(sorted)+1)
	edge := 0
	for _, r := range sorted {
		if r.start > edge {
			gaps = append(gaps, rowInterval{edge, r.start})
		}
		edge = max(edge, r.end)
	}
	if edge < viewHeight {
		gaps = append(gaps, rowInterval{edge, viewHeight})
	}
	return gaps
}

// placeHoverInZones greedily picks the best free zone for the hover tooltip.
// Strategy: among zones closest to the anchor, pick the largest so the popup
// can display the most content without scrolling. Renders the popup
// constrained to the chosen zone's actual height.
func (m *Model) placeHoverInZones(zones []rowInterval, anchorRow int) popupPlacement {
	zone := bestHoverZone(zones, anchorRow)
	zoneH := zone.end - zone.start
	if zoneH <= hover.BorderRows {
		return popupPlacement{}
	}
	popup := m.hoverPopup.View(m.width, zoneH, m.theme)
	p := newPopupPlacement(popup, 0)
	p.start = hoverSlot(zone, anchorRow, p.height)
	return p
}

// bestHoverZone selects the optimal free zone for hover placement. Zones are
// ranked by: (1) proximity to anchor (actual row gap), (2) available height
// (larger = more content visible), (3) position below anchor preferred.
func bestHoverZone(zones []rowInterval, anchorRow int) rowInterval {
	sorted := slices.Clone(zones)
	slices.SortStableFunc(sorted, zoneComparator(anchorRow))
	if len(sorted) == 0 {
		return rowInterval{}
	}
	return sorted[0]
}

// zoneComparator returns a sort function that ranks zones by proximity to
// anchor, then by size (descending), then by below-preference.
func zoneComparator(anchorRow int) func(a, b rowInterval) int {
	return func(a, b rowInterval) int {
		da, db := zoneGap(a, anchorRow), zoneGap(b, anchorRow)
		if da != db {
			return cmp.Compare(da, db)
		}
		sizeA, sizeB := a.end-a.start, b.end-b.start
		if sizeA != sizeB {
			return cmp.Compare(sizeB, sizeA) // descending: larger first
		}
		return cmp.Compare(b.start, a.start) // descending: below first
	}
}

// zoneGap returns the number of rows between the zone boundary and the
// anchor row. Adjacent zones (immediately above or below the reserved
// anchor row) return 0.
func zoneGap(z rowInterval, anchorRow int) int {
	if z.start > anchorRow+1 {
		return z.start - anchorRow - 1
	}
	if z.end < anchorRow {
		return anchorRow - z.end
	}
	return 0
}

// hoverSlot finds the position within zone z that puts the popup closest
// to anchorRow, preferring below-anchor when distances tie.
func hoverSlot(z rowInterval, anchorRow, popupH int) int {
	below := max(min(anchorRow+1, z.end-popupH), z.start)
	above := max(min(anchorRow-popupH, z.end-popupH), z.start)
	if preferAbove(below-anchorRow, anchorRow-(above+popupH-1)) {
		return above
	}
	return below
}

// preferAbove returns true when placing the popup above the anchor is
// strictly closer than placing it below.
func preferAbove(distBelow, distAbove int) bool {
	return distAbove > 0 && (distBelow < 0 || distAbove < distBelow)
}

// overlayPopups positions all active popups with collision detection and
// writes them into the viewport lines.
func (m *Model) overlayPopups(lines []string, viewHeight int) []string {
	sig, hover, comp := m.popupLayout(viewHeight)
	applyPopupOverlay(lines, sig)
	applyPopupOverlay(lines, hover)
	applyPopupOverlay(lines, comp)
	return lines
}

// applyPopupOverlay stamps a popup's rendered lines onto the viewport.
func applyPopupOverlay(lines []string, p popupPlacement) {
	if p.content == "" {
		return
	}
	for i, pLine := range strings.Split(p.content, "\n") {
		row := p.start + i
		if row >= 0 && row < len(lines) {
			lines[row] = pLine
		}
	}
}

// acceptCompletion replaces the current prefix with the selected completion
// item's text and deactivates the popup.
func (m *Model) acceptCompletion() {
	startPos := m.completionEngine.StartCol()
	item := m.completionEngine.Accept()
	if item == nil {
		return
	}

	// Delete the typed prefix [startPos, cursor).
	prefixLen := m.state.Cursor - startPos
	if prefixLen > 0 {
		old := m.substringAt(startPos, m.state.Cursor)
		m.buf.Delete(startPos, prefixLen)
		m.undoTree.Record(buffer.EditOp{
			Type:    buffer.EditDelete,
			Pos:     startPos,
			OldText: old,
		})
	}

	// Insert the completion word.
	m.buf.Insert(startPos, item.Word)
	m.undoTree.Record(buffer.EditOp{
		Type: buffer.EditInsert,
		Pos:  startPos,
		Text: item.Word,
	})
	m.lineIndex.Rebuild(m.buf)
	m.state.Cursor = startPos + len([]rune(item.Word))
	m.state.SyncCursorPos()
	m.editGeneration++
	m.modified = true
	m.lspDirty = true
	m.lastEditAt = time.Now()
	m.rehighlight()
}

// syncCursorContext updates the EditorState's InStringOrComment flag and
// CursorNodeType. Uses tree-sitter node queries when a parse tree is
// available, falling back to highlight regions otherwise.
func (m *Model) syncCursorContext() {
	m.state.InStringOrComment = false
	m.state.CursorNodeType = ""
	tree := m.highlighter.Tree()
	if tree != nil {
		m.syncCursorContextFromTree(tree)
		return
	}
	m.syncCursorContextFromRegions()
}

// syncCursorContextFromTree queries the parse tree for the node at the
// cursor position and walks up the parent chain to detect string/comment
// context.
func (m *Model) syncCursorContextFromTree(tree *treesitter.Tree) {
	row := uint32(m.state.CursorLine)
	lineInfo, ok := m.lineIndex.Get(m.state.CursorLine)
	if !ok {
		return
	}
	col := uint32(m.runeColToByteCol(lineInfo, m.state.CursorCol))
	node := tree.RootNode().NamedDescendantForPointRange(row, col, row, col)
	if node == nil {
		return
	}
	m.state.CursorNodeType = node.Type()
	for n := node; n != nil; n = n.Parent() {
		cat, ok := theme.NodeTypeToCategory[n.Type()]
		if ok && (cat == theme.CatString || cat == theme.CatComment) {
			m.state.InStringOrComment = true
			return
		}
	}
}

// syncCursorContextFromRegions checks highlight regions for string/comment
// context at the cursor position (fallback when no tree is available).
func (m *Model) syncCursorContextFromRegions() {
	line := m.state.CursorLine
	col := m.state.CursorCol
	if line < 0 || line >= len(m.regions) {
		return
	}
	for _, r := range m.regions[line] {
		if col >= r.StartCol && col < r.EndCol {
			m.state.InStringOrComment = r.Category == theme.CatString || r.Category == theme.CatComment
			return
		}
	}
}

// runeColToByteCol converts a rune-based column offset to a byte-based
// column offset for tree-sitter point queries.
func (m *Model) runeColToByteCol(lineInfo buffer.LineInfo, runeCol int) int {
	byteCol := 0
	limit := min(runeCol, lineInfo.Length)
	for i := range limit {
		r := m.buf.RuneAt(lineInfo.StartPos + i)
		byteCol += utf8.RuneLen(r)
	}
	return byteCol
}

// substringAt extracts a string from the buffer between two rune positions.
func (m *Model) substringAt(start, end int) string {
	runes := make([]rune, 0, end-start)
	for i := start; i < end; i++ {
		runes = append(runes, m.buf.RuneAt(i))
	}
	return string(runes)
}

// triggerCompletion starts the completion engine and returns a Cmd to
// request LSP completion items for the current cursor position. Returns
// nil if no file is open or the prefix is empty.
func (m *Model) triggerCompletion() tea.Cmd {
	content := []rune(m.buf.Content())
	m.completionEngine.Start(completion.ModeGeneric, content, m.state.Cursor, m.state.CursorLine)

	// Fire async LSP completion request.
	if m.filePath == "" {
		return nil
	}
	return func() tea.Msg {
		return msg.LSPCompletionRequestMsg{
			FilePath: m.filePath,
			Line:     m.state.CursorLine,
			Col:      m.state.CursorCol,
		}
	}
}

// triggerSignatureHelp checks whether the last-typed character should
// trigger a signature help request. Returns nil if no trigger.
func (m *Model) triggerSignatureHelp(key tea.KeyMsg) tea.Cmd {
	if m.filePath == "" {
		return nil
	}

	// Check typed runes for explicit dismiss/trigger characters.
	if len(key.Runes) > 0 {
		typed := string(key.Runes[:1])
		if typed == ")" {
			m.DismissSignatureHelp()
			return nil
		}
		if typed == "(" || typed == "," {
			return m.signatureHelpCmd()
		}
	}

	// If already active, retrigger on any buffer-changing keystroke
	// (including Backspace/Delete) so the server can update activeParameter
	// or dismiss when the call context is deleted.
	if m.sigHelpActive {
		return m.signatureHelpCmd()
	}

	return nil
}

// signatureHelpCmd emits the LSP signature help request message.
func (m *Model) signatureHelpCmd() tea.Cmd {
	return func() tea.Msg {
		return msg.LSPSignatureHelpRequestMsg{
			FilePath: m.filePath,
			Line:     m.state.CursorLine,
			Col:      m.state.CursorCol,
		}
	}
}

// HandleCompletionClick checks whether viewport coordinates (x, y) land on
// the completion popup. If they do, the clicked item is accepted and true is
// returned. The caller should skip normal cursor placement when true.
func (m *Model) HandleCompletionClick(_, y int) bool {
	if !m.completionEngine.Active() {
		return false
	}

	viewHeight := m.viewportHeight()
	cursorRow := m.state.CursorLine - m.scrollOffset
	startRow := cursorRow + 1
	availRows := viewHeight - startRow
	if availRows <= 0 {
		return false
	}

	itemLimit := min(completion.MaxVisibleItems(), max(availRows-completion.PopupChromeRows(), 1))
	visStart, visEnd := m.completionEngine.VisibleRange(itemLimit)

	// Popup layout: row 0 = top border, rows 1..N = items, then bottom
	// border + footer. Only item rows are clickable.
	clickedPopupRow := y - startRow
	itemRow := clickedPopupRow - 1 // subtract top border
	if itemRow < 0 || itemRow >= (visEnd-visStart) {
		// Click landed on border or footer — just dismiss.
		m.completionEngine.Dismiss()
		return true
	}

	itemIndex := visStart + itemRow
	startPos := m.completionEngine.StartCol()
	item := m.completionEngine.AcceptAt(itemIndex)
	if item == nil {
		return true
	}

	// Replace the typed prefix with the clicked item.
	prefixLen := m.state.Cursor - startPos
	if prefixLen > 0 {
		old := m.substringAt(startPos, m.state.Cursor)
		m.buf.Delete(startPos, prefixLen)
		m.undoTree.Record(buffer.EditOp{
			Type:    buffer.EditDelete,
			Pos:     startPos,
			OldText: old,
		})
	}
	m.buf.Insert(startPos, item.Word)
	m.undoTree.Record(buffer.EditOp{
		Type: buffer.EditInsert,
		Pos:  startPos,
		Text: item.Word,
	})
	m.lineIndex.Rebuild(m.buf)
	m.state.Cursor = startPos + len([]rune(item.Word))
	m.state.SyncCursorPos()
	m.editGeneration++
	m.modified = true
	m.lspDirty = true
	m.lastEditAt = time.Now()
	m.rehighlight()
	m.syncStatusLine()
	return true
}

// HandleHoverClick checks whether a viewport-local click (x, y) lands on
// the hover popup. If so, it pushes the current position to the jump list,
// dismisses the popup, and returns a go-to-definition command at the hover
// anchor. Returns (cmd, true) when the click was consumed.
func (m *Model) HandleHoverClick(x, y int) (tea.Cmd, bool) {
	viewHeight := m.viewportHeight()
	_, hov, _ := m.popupLayout(viewHeight)
	if !insidePopup(hov, x, y) {
		return nil, false
	}
	anchorLine := m.hoverPopup.AnchorLine()
	anchorCol := m.hoverPopup.AnchorCol()
	filePath := m.filePath
	m.jumpList.Push(motion.JumpEntry{
		File: m.filePath,
		Line: m.state.CursorLine,
		Col:  m.state.CursorCol,
	})
	m.DismissHover()
	cmd := func() tea.Msg {
		return msg.LSPDefinitionRequestMsg{
			FilePath: filePath,
			Line:     anchorLine,
			Col:      anchorCol,
		}
	}
	return cmd, true
}

// contentLineToViewRow maps a content line number to its row index in the
// rendered viewport slice, accounting for diagnostic virtual lines that
// renderVisibleLines inserts after each diagnosed line.
func (m *Model) contentLineToViewRow(contentLine int) int {
	row := contentLine - m.scrollOffset
	for i := m.scrollOffset; i < contentLine; i++ {
		if _, ok := m.diagnosticForLine(i); ok {
			row++
		}
	}
	return row
}

// completionPopupStart returns the first viewport row where the completion
// popup should begin — directly after the cursor line. The diagnostic
// virtual line on the cursor line (if any) is intentionally ignored so
// that the popup position stays stable as diagnostics appear/disappear
// during typing; the popup overlays on top of the virtual line.
func (m *Model) completionPopupStart() int {
	return m.contentLineToViewRow(m.state.CursorLine) + 1
}

// ScrollUp scrolls the viewport up by one line.
// Returns true if the scroll was applied, false if at the top boundary.
func (m *Model) ScrollUp() bool {
	prev := m.scrollOffset
	m.scrollOffset = max(m.scrollOffset-1, 0)
	m.clampCursorToViewport()
	return m.scrollOffset != prev
}

// ScrollDown scrolls the viewport down by one line.
// Returns true if the scroll was applied, false if at the bottom boundary.
func (m *Model) ScrollDown() bool {
	prev := m.scrollOffset
	totalLines := m.lineIndex.Count()
	viewHeight := m.viewportHeight()
	maxOffset := max(totalLines-viewHeight, 0)
	m.scrollOffset = min(m.scrollOffset+1, maxOffset)
	m.clampCursorToViewport()
	return m.scrollOffset != prev
}

// SetBounceOffset updates the visual bounce displacement for rendering.
func (m *Model) SetBounceOffset(offset int) {
	m.bounceOffset = offset
}

// clampCursorToViewport ensures the cursor line stays within the visible
// viewport after a scroll-only movement.
func (m *Model) clampCursorToViewport() {
	viewHeight := m.viewportHeight()
	cursorLine := m.state.CursorLine
	topLine := m.scrollOffset
	bottomLine := m.scrollOffset + viewHeight - 1

	if cursorLine < topLine {
		m.state.Cursor = m.lineIndex.LineColToPos(topLine, m.state.CursorCol)
		m.state.ClampCursor(1)
	} else if cursorLine > bottomLine {
		m.state.Cursor = m.lineIndex.LineColToPos(bottomLine, m.state.CursorCol)
		m.state.ClampCursor(1)
	}
}

// RestoreState sets the cursor and scroll offset from a saved snapshot,
// clamping the cursor to valid bounds.
func (m *Model) RestoreState(cursor, scrollOffset int) {
	m.state.Cursor = cursor
	m.state.ClampCursor(1)
	m.scrollOffset = scrollOffset
	m.syncStatusLine()
}

// MarkSaved clears the modified flag after a successful write.
func (m *Model) MarkSaved() {
	m.modified = false
	m.syncStatusLine()
}

// Undo reverses the last edit operation (or grouped operation).
func (m *Model) Undo() {
	edits, ok := m.undoTree.Undo()
	if !ok {
		return
	}
	for _, edit := range edits {
		m.reverseEdit(edit)
	}
	m.lineIndex.Rebuild(m.buf)
	m.state.Cursor = edits[0].Pos
	m.state.ClampCursor(1)
	m.editGeneration++
	m.modified = true
	m.lspDirty = true
	m.lastEditAt = time.Now()
	m.rehighlight()
	m.syncStatusLine()
}

// Redo reapplies the last undone edit operation (or grouped operation).
func (m *Model) Redo() {
	edits, ok := m.undoTree.Redo()
	if !ok {
		return
	}
	for _, edit := range edits {
		m.reapplyEdit(edit)
	}
	m.lineIndex.Rebuild(m.buf)
	m.state.Cursor = edits[len(edits)-1].Pos
	m.state.ClampCursor(1)
	m.editGeneration++
	m.modified = true
	m.lspDirty = true
	m.lastEditAt = time.Now()
	m.rehighlight()
	m.syncStatusLine()
}

// reverseEdit undoes a single edit operation on the buffer.
func (m *Model) reverseEdit(edit buffer.EditOp) {
	if edit.Type == buffer.EditInsert {
		m.buf.Delete(edit.Pos, len([]rune(edit.Text)))
	} else {
		m.buf.Insert(edit.Pos, edit.OldText)
	}
}

// reapplyEdit re-applies a single edit operation on the buffer.
func (m *Model) reapplyEdit(edit buffer.EditOp) {
	if edit.Type == buffer.EditInsert {
		m.buf.Insert(edit.Pos, edit.Text)
	} else {
		m.buf.Delete(edit.Pos, len([]rune(edit.OldText)))
	}
}

// ApplyTextEdits applies a set of LSP text edits to the buffer. Edits are
// sorted in reverse document order (bottom-up) so that earlier positions
// remain valid as later edits shift content. Each edit is recorded in the
// undo tree. Returns the number of edits applied.
func (m *Model) ApplyTextEdits(edits []lsp.TextEdit) int {
	if len(edits) == 0 {
		return 0
	}

	// Sort edits in reverse document order so bottom-up application
	// preserves earlier positions.
	sorted := make([]lsp.TextEdit, len(edits))
	copy(sorted, edits)
	slices.SortFunc(sorted, func(a, b lsp.TextEdit) int {
		if c := cmp.Compare(b.Range.Start.Line, a.Range.Start.Line); c != 0 {
			return c
		}
		return cmp.Compare(b.Range.Start.Character, a.Range.Start.Character)
	})

	m.undoTree.BeginGroup()
	applied := 0
	for _, edit := range sorted {
		m.applySingleEdit(edit)
		applied++
	}
	m.undoTree.EndGroup()
	m.state.ClampCursor(1)
	m.editGeneration++
	m.modified = true
	m.lspDirty = true
	m.lastEditAt = time.Now()
	m.rehighlight()
	m.syncStatusLine()
	return applied
}

// applySingleEdit converts an LSP TextEdit to buffer operations and records
// undo entries. The caller must call lineIndex.Rebuild afterwards.
func (m *Model) applySingleEdit(edit lsp.TextEdit) {
	startPos := m.lspPositionToRune(edit.Range.Start)
	endPos := m.lspPositionToRune(edit.Range.End)

	// Delete the replaced range.
	if endPos > startPos {
		old := m.substringAt(startPos, endPos)
		m.buf.Delete(startPos, endPos-startPos)
		m.undoTree.Record(buffer.EditOp{
			Type:    buffer.EditDelete,
			Pos:     startPos,
			OldText: old,
		})
		// Rebuild line index between delete and insert so the insert
		// position is correct after line shifts.
		m.lineIndex.Rebuild(m.buf)
	}

	// Insert the new text.
	if edit.NewText != "" {
		m.buf.Insert(startPos, edit.NewText)
		m.undoTree.Record(buffer.EditOp{
			Type: buffer.EditInsert,
			Pos:  startPos,
			Text: edit.NewText,
		})
		m.lineIndex.Rebuild(m.buf)
	}
}

// lspPositionToRune converts an LSP Position (line + UTF-16 character offset)
// to an absolute rune position in the buffer.
func (m *Model) lspPositionToRune(pos lsp.Position) int {
	info, ok := m.lineIndex.Get(pos.Line)
	if !ok {
		return m.buf.Length()
	}
	lineText := m.substringAt(info.StartPos, info.StartPos+info.Length)
	runeCol := lsp.UTF16ToRuneOffset(lineText, pos.Character)
	return info.StartPos + min(runeCol, info.Length)
}

// SelectedText returns the text covered by the active selection (keyboard
// visual mode or mouse selection). Returns empty string if nothing selected.
func (m *Model) SelectedText() string {
	hasSel, start, end := m.selectionRange()
	if !hasSel {
		return ""
	}
	length := end - start + 1
	runes := make([]rune, length)
	for i := range length {
		runes[i] = m.buf.RuneAt(start + i)
	}
	return string(runes)
}

// selectionRange returns the active visual-mode selection bounds.
// Returns (false, 0, 0) if nothing selected.
func (m *Model) selectionRange() (bool, int, int) {
	if m.visualMode != nil {
		start, end := m.visualMode.State.CharRange()
		return true, start, end
	}
	return false, 0, 0
}

// ClickAt moves the cursor to the buffer position corresponding to
// content-local viewport coordinates (x, y). The gutter and scroll offset
// are accounted for internally. Any active selection and completion popup
// are dismissed.
func (m *Model) ClickAt(x, y int) {
	if m.visualMode != nil {
		m.visualMode = nil
		m.currentMode = mode.ModeNormal
	}
	if m.completionEngine.Active() {
		m.completionEngine.Dismiss()
	}
	if m.sigHelpActive {
		m.DismissSignatureHelp()
	}
	m.setCursorFromViewport(x, y)
	m.syncStatusLine()
}

// StartDragSelection begins a mouse-initiated text selection anchored at the
// current cursor position, entering visual-char mode.
func (m *Model) StartDragSelection() {
	m.visualMode = mode.NewVisualMode(m.theme, m.state.Cursor, mode.VisualChar)
	m.currentMode = mode.ModeVisual
	m.syncStatusLine()
}

// ExtendDragSelection moves the cursor and updates the visual selection
// endpoint during a mouse drag.
func (m *Model) ExtendDragSelection(x, y int) {
	if m.visualMode == nil {
		return
	}
	m.setCursorFromViewport(x, y)
	m.visualMode.State.CursorPos = m.state.Cursor
	m.syncStatusLine()
}

// ClearSelection clears any active visual-mode selection.
func (m *Model) ClearSelection() {
	if m.visualMode != nil {
		m.visualMode = nil
		m.currentMode = mode.ModeNormal
		m.state.ClampCursor(1)
	}
}

// SelectAll selects all buffer content by entering visual-char mode
// spanning the entire buffer.
func (m *Model) SelectAll() {
	totalLen := m.buf.Length()
	if totalLen == 0 {
		return
	}
	m.state.Cursor = totalLen - 1
	m.state.SyncCursorPos()
	m.visualMode = mode.NewVisualMode(m.theme, 0, mode.VisualChar)
	m.visualMode.State.CursorPos = m.state.Cursor
	m.currentMode = mode.ModeVisual
	m.syncStatusLine()
}

// deleteSelection removes the text covered by the active selection
// (mouse or keyboard visual), records an undo operation, and returns
// to normal mode.
func (m *Model) deleteSelection() {
	hasSel, start, end := m.selectionRange()
	if !hasSel {
		return
	}
	length := end - start + 1
	runes := make([]rune, length)
	for i := range length {
		runes[i] = m.buf.RuneAt(start + i)
	}
	m.buf.Delete(start, length)
	m.undoTree.Record(buffer.EditOp{
		Type:    buffer.EditDelete,
		Pos:     start,
		OldText: string(runes),
	})
	m.lineIndex.Rebuild(m.buf)
	m.state.Cursor = start
	m.state.ClampCursor(1)

	m.visualMode = nil
	m.currentMode = mode.ModeNormal
	m.editGeneration++
	m.modified = true
	m.lspDirty = true
	m.lastEditAt = time.Now()
	m.rehighlight()
	m.syncStatusLine()
}

// CutSelection copies the selected text and deletes it, returning the
// text that was removed. Returns "" if nothing is selected.
func (m *Model) CutSelection() string {
	text := m.SelectedText()
	if text == "" {
		return ""
	}
	m.deleteSelection()
	return text
}

// ---------------------------------------------------------------------------
// Find bar
// ---------------------------------------------------------------------------

// OpenFindBar opens the in-file search bar. If a visual selection is active
// its bounds are captured so the "find in selection" toggle can restrict the
// search range.
func (m *Model) OpenFindBar() {
	var selStart, selEnd int
	hasSel := false
	if m.visualMode != nil {
		selStart, selEnd = m.visualMode.State.CharRange()
		hasSel = true
	}
	m.findBar = findbar.New(selStart, selEnd, hasSel)
	m.findActive = true
	m.recomputeFind()
}

// CloseFindBar closes the in-file search bar and clears match state.
func (m *Model) CloseFindBar() {
	m.findBar = nil
	m.findActive = false
}

// FindActive reports whether the find bar is open.
func (m *Model) FindActive() bool { return m.findActive }

// FindBarHeight returns the number of viewport rows consumed by the find bar
// (0 when closed).
func (m *Model) FindBarHeight() int {
	if m.findActive && m.findBar != nil {
		return m.findBar.Height()
	}
	return 0
}

// HandleFindBarClick processes a mouse click at viewport-local (x, y) that
// falls within the find bar area. Returns true if the click was consumed.
func (m *Model) HandleFindBarClick(x, y int) bool {
	if !m.findActive || m.findBar == nil || y >= m.findBar.Height()-1 {
		return false // row 0 = label line; row 1 = divider (ignore)
	}
	action := m.findBar.HandleClick(x)
	if action == findbar.ActionQueryChanged {
		m.recomputeFind()
		m.findBar.NearestMatch(m.state.Cursor)
		m.jumpToCurrentMatch()
	}
	return true
}

// ToggleFindBar opens the find bar if closed, or closes it if open.
func (m *Model) ToggleFindBar() {
	if m.findActive {
		m.CloseFindBar()
		return
	}
	m.OpenFindBar()
}

// ViewportToBufferPos converts viewport-local (x, y) to a buffer line and
// column without modifying the cursor. Returns ok=false if the position
// is outside buffer bounds.
func (m *Model) ViewportToBufferPos(x, y int) (line, col int, ok bool) {
	totalLines := m.lineIndex.Count()
	gutterW := m.gutterWidth(totalLines)
	line = y + m.scrollOffset
	if line < 0 || line >= totalLines {
		return 0, 0, false
	}
	screenCol := max(x-gutterW, 0)
	col = m.screenToBufferCol(line, screenCol)
	return line, col, true
}

// screenToBufferCol converts a display column (post-tab-expansion) to the
// corresponding buffer column (rune index) for the given line. Positions
// that fall within a tab's visual expansion map to the tab's buffer column.
func (m *Model) screenToBufferCol(line, screenCol int) int {
	info, ok := m.lineIndex.Get(line)
	if !ok {
		return screenCol
	}
	visCol := 0
	for bufCol := 0; bufCol < info.Length; bufCol++ {
		r := m.buf.RuneAt(info.StartPos + bufCol)
		charWidth := 1
		if r == '\t' {
			charWidth = editorTabWidth - (visCol % editorTabWidth)
		}
		if screenCol < visCol+charWidth {
			return bufCol
		}
		visCol += charWidth
	}
	return info.Length
}

// setCursorFromViewport converts viewport-local (x, y) to a buffer
// position and updates the cursor.
func (m *Model) setCursorFromViewport(x, y int) {
	totalLines := m.lineIndex.Count()
	gutterW := m.gutterWidth(totalLines)

	line := y + m.scrollOffset
	line = max(min(line, totalLines-1), 0)

	screenCol := max(x-gutterW, 0)
	col := m.screenToBufferCol(line, screenCol)

	trailingOffset := 1
	if m.currentMode == mode.ModeInsert {
		trailingOffset = 0
	}

	m.state.Cursor = m.lineIndex.LineColToPos(line, col)
	m.state.ClampCursor(trailingOffset)
}

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
	msgKindTickMsg
	msgKindLSPDiagnostic
	msgKindLSPHover
	msgKindLSPDefinition
	msgKindStandaloneResult
	msgKindLSPCompletion
	msgKindLSPDocHighlight
	msgKindLSPSignatureHelp
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
	case msg.TickMsg:
		return msgKindTickMsg
	case msg.LSPDiagnosticMsg:
		return msgKindLSPDiagnostic
	case msg.LSPHoverMsg:
		return msgKindLSPHover
	case msg.LSPDefinitionMsg:
		return msgKindLSPDefinition
	case mode.StandaloneResult:
		return msgKindStandaloneResult
	case msg.LSPCompletionMsg:
		return msgKindLSPCompletion
	case msg.LSPDocumentHighlightMsg:
		return msgKindLSPDocHighlight
	case msg.LSPSignatureHelpMsg:
		return msgKindLSPSignatureHelp
	default:
		return msgKindUnknown
	}
}

type msgHandler func(m *Model, incoming tea.Msg) (component.Component, tea.Cmd)

var msgHandlerTable = map[msgKind]msgHandler{
	msgKindOpenEditor:    handleOpenEditor,
	msgKindCloseEditor:   handleCloseEditor,
	msgKindKeyMsg:        handleKeyMsg,
	msgKindTickMsg:       handleTickMsg,
	msgKindLSPDiagnostic: handleLSPDiagnostic,
	msgKindLSPHover:      handleLSPHover,
	msgKindLSPDefinition:    handleLSPDefinition,
	msgKindStandaloneResult: handleStandaloneResult,
	msgKindLSPCompletion:    handleLSPCompletion,
	msgKindLSPDocHighlight:   handleLSPDocHighlight,
	msgKindLSPSignatureHelp:  handleLSPSignatureHelp,
}

func handleOpenEditor(m *Model, incoming tea.Msg) (component.Component, tea.Cmd) {
	o := incoming.(msg.OpenEditorMsg)
	lang := detectLanguage(o.FilePath)
	m.OpenFile(o.FilePath, o.Content, lang)
	return m, nil
}

func handleLSPDiagnostic(m *Model, incoming tea.Msg) (component.Component, tea.Cmd) {
	d := incoming.(msg.LSPDiagnosticMsg)
	if d.FilePath != m.filePath {
		return m, nil
	}
	m.diagnostics = d.Diagnostics
	return m, nil
}

func handleLSPHover(m *Model, incoming tea.Msg) (component.Component, tea.Cmd) {
	h := incoming.(msg.LSPHoverMsg)
	if h.Err != nil || h.Result == nil || h.FilePath != m.filePath {
		return m, nil
	}
	m.ShowHoverAt(h.Result.Contents, h.Line, h.Col)
	return m, nil
}

func handleLSPDefinition(m *Model, incoming tea.Msg) (component.Component, tea.Cmd) {
	d := incoming.(msg.LSPDefinitionMsg)
	if d.Err != nil || len(d.Locations) == 0 {
		return m, nil
	}
	loc := d.Locations[0]
	filePath := lsp.FileURIToPath(loc.URI)

	if filePath == m.filePath {
		// Same file: jump cursor to target position.
		targetPos := m.state.LineIndex.LineColToPos(loc.Range.Start.Line, loc.Range.Start.Character)
		m.state.Cursor = targetPos
		m.state.SyncCursorPos()
		m.syncStatusLine()
		return m, nil
	}

	// Different file: emit FileOpenMsg for the app layer.
	return m, func() tea.Msg {
		return msg.FileOpenMsg{
			Path:     filePath,
			Name:     filePath,
			Language: detectLanguage(filePath),
			Line:     loc.Range.Start.Line + 1, // 1-based for FileOpenMsg
		}
	}
}

func handleLSPCompletion(m *Model, incoming tea.Msg) (component.Component, tea.Cmd) {
	c := incoming.(msg.LSPCompletionMsg)
	if c.Err != nil || c.FilePath != m.filePath {
		return m, nil
	}
	m.lspSource.SetItems(c.Items)
	// Re-trigger the engine if still in insert mode. The initial
	// triggerCompletion may have found 0 buffer-word matches (e.g. after
	// typing "fmt.") and dismissed the engine before LSP items arrived.
	// Mode check prevents stale responses from reopening after Esc.
	if m.currentMode == mode.ModeInsert {
		content := []rune(m.buf.Content())
		m.completionEngine.Start(completion.ModeGeneric, content, m.state.Cursor, m.state.CursorLine)
	}
	return m, nil
}

func handleLSPSignatureHelp(m *Model, incoming tea.Msg) (component.Component, tea.Cmd) {
	sh := incoming.(msg.LSPSignatureHelpMsg)
	if sh.Err != nil || sh.FilePath != m.filePath {
		return m, nil
	}
	m.ShowSignatureHelp(sh.Result)
	return m, nil
}

func handleLSPDocHighlight(m *Model, incoming tea.Msg) (component.Component, tea.Cmd) {
	dh := incoming.(msg.LSPDocumentHighlightMsg)
	if dh.Err != nil || dh.FilePath != m.filePath {
		return m, nil
	}
	m.highlightRanges = dh.Highlights
	return m, nil
}

func handleStandaloneResult(m *Model, incoming tea.Msg) (component.Component, tea.Cmd) {
	sr := incoming.(mode.StandaloneResult)
	switch sr.Operator {
	case motion.OpGotoDefinition:
		m.jumpList.Push(motion.JumpEntry{
			File: m.filePath,
			Line: m.state.CursorLine,
			Col:  m.state.CursorCol,
		})
		return m, func() tea.Msg {
			return msg.LSPDefinitionRequestMsg{
				FilePath: m.filePath,
				Line:     sr.Line,
				Col:      sr.Col,
			}
		}
	case motion.OpGotoReferences:
		return m, func() tea.Msg {
			return msg.LSPReferencesRequestMsg{
				FilePath: m.filePath,
				Line:     sr.Line,
				Col:      sr.Col,
			}
		}
	}
	return m, nil
}

func handleCloseEditor(m *Model, _ tea.Msg) (component.Component, tea.Cmd) {
	return m, nil
}

func handleTickMsg(m *Model, incoming tea.Msg) (component.Component, tea.Cmd) {
	tick := incoming.(msg.TickMsg)
	if !m.focused {
		m.cursorBlink = true
		m.lastBlinkAt = tick.Time
		return m, nil
	}
	if tick.Time.Sub(m.lastBlinkAt) >= blinkHalfPeriod {
		m.cursorBlink = !m.cursorBlink
		m.lastBlinkAt = tick.Time
	}
	return m, nil
}

func handleKeyMsg(m *Model, incoming tea.Msg) (component.Component, tea.Cmd) {
	key := incoming.(tea.KeyMsg)

	// Any keystroke resets cursor to visible and restarts blink cycle.
	m.cursorBlink = true
	m.lastBlinkAt = time.Now()

	// Hover interaction: scroll with j/k, go-to-definition with Enter,
	// dismiss with Esc or any other key.
	if m.hoverActive {
		if cmd, handled := m.handleHoverKey(key); handled {
			return m, cmd
		}
	}

	// Completion popup key interception in insert mode.
	if m.completionEngine.Active() && m.currentMode == mode.ModeInsert {
		switch key.Type {
		case tea.KeyTab, tea.KeyEnter:
			m.acceptCompletion()
			m.syncStatusLine()
			return m, nil
		case tea.KeyEsc:
			m.completionEngine.Dismiss()
			m.syncStatusLine()
			return m, nil
		case tea.KeyCtrlN, tea.KeyDown:
			m.completionEngine.Next()
			return m, nil
		case tea.KeyCtrlP, tea.KeyUp:
			m.completionEngine.Prev()
			return m, nil
		}
		// All other keys fall through to normal insert mode dispatch,
		// then retrigger completion after the buffer edit.
	}

	// Find bar key interception — all keys go to the find bar while open.
	if m.findActive {
		return m.handleFindKey(key)
	}

	// Delete/backspace with active selection: delete the selected range
	// before clearing the selection or dispatching the key.
	if m.HasSelection() && (key.Type == tea.KeyBackspace || key.Type == tea.KeyDelete) {
		m.deleteSelection()
		return m, nil
	}

	// Shift+arrow: enter or extend visual-char selection.
	// Left/right use simple increment to allow crossing line boundaries
	// (standard text editor behaviour); up/down use line-based motions.
	if isShiftArrow(key) {
		anchor := m.state.Cursor
		switch key.String() {
		case "shift+right":
			m.state.Cursor = min(m.state.Cursor+1, m.buf.Length())
		case "shift+left":
			m.state.Cursor = max(m.state.Cursor-1, 0)
		default:
			mr := motion.ExecuteMotion(shiftArrowMotion(key), m.state.Buffer, m.state.LineIndex, m.state.Cursor, 1)
			m.state.Cursor = mr.End
		}
		m.state.SyncCursorPos()
		if !isVisualMode(m.currentMode) {
			m.visualMode = mode.NewVisualMode(m.theme, anchor, mode.VisualChar)
			m.currentMode = mode.ModeVisual
		}
		m.visualMode.State.CursorPos = m.state.Cursor
		m.syncStatusLine()
		return m, nil
	}

	// Normal mode key interceptions.
	if m.currentMode == mode.ModeNormal {
		switch key.Type {
		case tea.KeyCtrlO:
			return m.jumpBack()
		case tea.KeyCtrlI:
			return m.jumpForward()
		case tea.KeyEsc:
			if m.jumpList.CanBack() {
				return m.jumpBack()
			}
		}
	}

	// K in normal mode: request LSP hover at cursor position.
	if m.currentMode == mode.ModeNormal && len(key.Runes) == 1 && key.Runes[0] == 'K' {
		return m, func() tea.Msg {
			return msg.LSPHoverRequestMsg{
				FilePath: m.filePath,
				Line:     m.state.CursorLine,
				Col:      m.state.CursorCol,
			}
		}
	}

	m.syncCursorContext()
	prevCursorLine := m.state.CursorLine
	prevCursorCol := m.state.CursorCol
	prevVersion := m.buf.Version()
	next, cmd := m.dispatchKey(key)
	if next != m.currentMode {
		m.transitionMode(next)
	}
	bufChanged := m.buf.Version() != prevVersion
	if bufChanged {
		m.editGeneration++
		m.modified = true
		m.lspDirty = true
		m.lastEditAt = time.Now()
		// Clear stale diagnostics on edited lines so gutter signs vanish
		// immediately when the user fixes an issue. The server will
		// re-publish if the error persists after the didChange flush.
		m.clearDiagnosticsOnLine(prevCursorLine)
		if m.state.CursorLine != prevCursorLine {
			m.clearDiagnosticsOnLine(m.state.CursorLine)
		}
		m.rehighlight()
	}

	// Trigger completion in insert mode after buffer edits.
	if m.currentMode == mode.ModeInsert && bufChanged {
		compCmd := m.triggerCompletion()
		cmd = tea.Batch(cmd, compCmd)
	}

	// Trigger signature help in insert mode on specific chars.
	if m.currentMode == mode.ModeInsert && bufChanged {
		sigCmd := m.triggerSignatureHelp(key)
		cmd = tea.Batch(cmd, sigCmd)
	}

	// Dismiss completion and signature help when buffer didn't change in
	// insert mode (e.g. arrow keys moved cursor away from context).
	if m.currentMode == mode.ModeInsert && !bufChanged {
		if m.completionEngine.Active() {
			m.completionEngine.Dismiss()
		}
		if m.sigHelpActive {
			m.DismissSignatureHelp()
		}
	}

	// Clear document highlights when the cursor moves to a different word.
	cursorMoved := m.state.CursorLine != prevCursorLine || m.state.CursorCol != prevCursorCol
	if (cursorMoved || bufChanged) && len(m.highlightRanges) > 0 {
		sameWord := !bufChanged && prevCursorLine == m.state.CursorLine
		if sameWord {
			ps, pe := m.WordBoundsAt(prevCursorLine, prevCursorCol)
			ns, ne := m.WordBoundsAt(m.state.CursorLine, m.state.CursorCol)
			sameWord = ps == ns && pe == ne && ps != pe
		}
		if !sameWord {
			m.highlightRanges = nil
		}
	}

	// Clear jump marker on any cursor movement or buffer change.
	if m.jumpActive && (cursorMoved || bufChanged) {
		m.jumpActive = false
	}

	m.syncStatusLine()
	return m, cmd
}

// isVisualMode reports whether a mode is one of the visual sub-modes.
func isVisualMode(md mode.Mode) bool {
	return md == mode.ModeVisual || md == mode.ModeVisualLine || md == mode.ModeVisualBlock
}

// isShiftArrow reports whether a key message is a shift+arrow combination.
func isShiftArrow(key tea.KeyMsg) bool {
	switch key.String() {
	case "shift+up", "shift+down", "shift+left", "shift+right":
		return true
	}
	return false
}

// shiftArrowMotion maps a shift+arrow key to the corresponding motion type.
func shiftArrowMotion(key tea.KeyMsg) motion.MotionType {
	switch key.String() {
	case "shift+up":
		return motion.MotionUp
	case "shift+down":
		return motion.MotionDown
	case "shift+left":
		return motion.MotionLeft
	default:
		return motion.MotionRight
	}
}

// transitionMode handles creating and destroying visual mode instances
// when the editor mode changes, and dismisses completion when leaving
// insert mode.
func (m *Model) transitionMode(next mode.Mode) {
	// Dismiss completion and signature help when leaving insert mode.
	if m.currentMode == mode.ModeInsert && next != mode.ModeInsert {
		m.completionEngine.Dismiss()
		m.lspSource.Clear()
		m.DismissSignatureHelp()
		// Force a final didChange so the server re-publishes diagnostics
		// for the post-edit buffer. The completion flush may have already
		// cleared lspDirty, so we unconditionally mark dirty and zero the
		// edit timestamp so the next 16ms tick fires the debounce immediately.
		m.lspDirty = true
		m.lastEditAt = time.Time{}
	}

	wasVisual := isVisualMode(m.currentMode)
	entering := isVisualMode(next)

	if entering && !wasVisual {
		sub := mode.VisualChar
		switch next {
		case mode.ModeVisualLine:
			sub = mode.VisualLine
		case mode.ModeVisualBlock:
			sub = mode.VisualBlock
		}
		m.visualMode = mode.NewVisualMode(m.theme, m.state.Cursor, sub)
	} else if !entering {
		m.visualMode = nil
	}
	m.currentMode = next
}

// ---------------------------------------------------------------------------
// Key dispatch
// ---------------------------------------------------------------------------

// keyDispatchTable maps modes to their handler functions.
var keyDispatchTable = map[mode.Mode]func(m *Model, key tea.KeyMsg) (mode.Mode, tea.Cmd){
	mode.ModeNormal:      dispatchNormal,
	mode.ModeInsert:      dispatchInsert,
	mode.ModeVisual:      dispatchVisual,
	mode.ModeVisualLine:  dispatchVisual,
	mode.ModeVisualBlock: dispatchVisual,
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

func dispatchVisual(m *Model, key tea.KeyMsg) (mode.Mode, tea.Cmd) {
	if m.visualMode == nil {
		return mode.ModeNormal, nil
	}
	return m.visualMode.HandleKey(key, m.state)
}

// ---------------------------------------------------------------------------
// Rendering helpers
// ---------------------------------------------------------------------------

// MaxVisibleLineWidth returns the maximum display-column width of the
// currently visible lines, including gutter. Used by the layout manager
// to detect content cutoff and trigger a panel mode downgrade.
func (m *Model) MaxVisibleLineWidth() int {
	content := m.buf.Content()
	contentLines := strings.Split(content, "\n")
	totalLines := len(contentLines)
	gutterW := m.gutterWidth(totalLines)
	viewH := m.viewportHeight()

	maxW := 0
	for i := m.scrollOffset; i < totalLines && (i-m.scrollOffset) < viewH; i++ {
		expanded, _ := expandTabs(contentLines[i], editorTabWidth)
		lineW := gutterW + gutterPadding + len([]rune(expanded))
		if lineW > maxW {
			maxW = lineW
		}
	}
	return maxW
}

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
	defaultStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Foreground)

	// Pre-compute selection range (keyboard visual or mouse).
	var selStartLine, selStartCol, selEndLine, selEndCol int
	hasSelection, selStart, selEnd := m.selectionRange()
	if hasSelection {
		selStartLine, selStartCol = m.lineIndex.PosToLineCol(selStart)
		selEndLine, selEndCol = m.lineIndex.PosToLineCol(selEnd)
	}

	// Track rendered lines (code + virtual text) against viewHeight.
	result := make([]string, 0, viewHeight)
	for i := m.scrollOffset; i < totalLines && len(result) < viewHeight; i++ {
		var regions []highlight.HighlightRegion
		if i < len(m.regions) {
			regions = m.regions[i]
		}

		// Pre-expand tabs and remap all columns to display space.
		displayLine, colMap := expandTabs(contentLines[i], editorTabWidth)
		displayRegions := remapRegions(regions, colMap)
		displayLineRunes := len([]rune(displayLine))
		inSelection := hasSelection && i >= selStartLine && i <= selEndLine
		findSpans := m.findMatchSpans(i, len([]rune(contentLines[i])), colMap)
		hlSpans := m.highlightSpansForLine(i, colMap)
		jumpSpans := m.jumpMarkerSpansForLine(i, colMap)

		// Compute diagnostic underline ranges for this line.
		ulRanges := m.diagnosticDisplayRanges(i, colMap)

		// Choose line rendering strategy; underlines apply to all paths.
		switch {
		case inSelection:
			sc := 0
			if i == selStartLine {
				sc = safeMapCol(colMap, selStartCol)
			}
			ec := displayLineRunes
			if i == selEndLine {
				ec = safeMapCol(colMap, selEndCol+1)
			}
			result = append(result, m.renderSelectedLine(
				i, displayLine, displayRegions, gutterWidth, defaultStyle, sc, ec, ulRanges))
		case len(findSpans) > 0:
			curCol := -1
			if !m.findActive && i == m.state.CursorLine && m.focused && m.cursorBlink {
				curCol = safeMapCol(colMap, m.state.CursorCol)
			}
			result = append(result, m.renderFindMatchLine(
				i, displayLine, displayRegions, gutterWidth, defaultStyle, findSpans, curCol, ulRanges))
		case len(jumpSpans) > 0:
			curCol := -1
			if i == m.state.CursorLine && m.focused && m.cursorBlink {
				curCol = safeMapCol(colMap, m.state.CursorCol)
			}
			result = append(result, m.renderHighlightLine(
				i, displayLine, displayRegions, gutterWidth, defaultStyle, jumpSpans, curCol, ulRanges))
		case !m.findActive && i == m.state.CursorLine && m.focused:
			displayCol := safeMapCol(colMap, m.state.CursorCol)
			curCol := -1
			if m.cursorBlink {
				curCol = displayCol
			}
			if len(hlSpans) > 0 {
				result = append(result, m.renderHighlightLine(
					i, displayLine, displayRegions, gutterWidth, defaultStyle, hlSpans, curCol, ulRanges))
			} else if curCol >= 0 {
				result = append(result, m.applyCursor(
					displayLine, displayRegions, gutterWidth, defaultStyle, displayCol, ulRanges))
			} else {
				gutter := m.renderGutter(i, gutterWidth)
				lineText := highlight.RenderLineWithUnderlines(
					displayLine, displayRegions, m.theme.Syntax, defaultStyle, ulRanges)
				result = append(result, gutter+lineText)
			}
		case len(hlSpans) > 0:
			result = append(result, m.renderHighlightLine(
				i, displayLine, displayRegions, gutterWidth, defaultStyle, hlSpans, -1, ulRanges))
		default:
			gutter := m.renderGutter(i, gutterWidth)
			lineText := highlight.RenderLineWithUnderlines(
				displayLine, displayRegions, m.theme.Syntax, defaultStyle, ulRanges)
			result = append(result, gutter+lineText)
		}

		// Append diagnostic virtual text line below if space remains.
		if diag, ok := m.diagnosticForLine(i); ok && len(result) < viewHeight {
			result = append(result, m.renderDiagnosticVirtualLine(diag, gutterWidth, m.width))
		}
	}
	return result
}

// renderSelectedLine renders a line with a portion highlighted as visual
// selection. selStart and selEnd are column indices (selEnd is exclusive).
func (m *Model) renderSelectedLine(lineNum int, line string, regions []highlight.HighlightRegion, gutterWidth int, defaultStyle lipgloss.Style, selStart, selEnd int, ulRanges []highlight.UnderlineRange) string {
	gutter := m.renderGutter(lineNum, gutterWidth)
	runes := []rune(line)
	selStyle := lipgloss.NewStyle().Reverse(true)

	selStart = max(selStart, 0)
	selEnd = min(selEnd, len(runes))

	before := string(runes[:selStart])
	selected := string(runes[selStart:selEnd])
	after := string(runes[selEnd:])

	beforeStyled := highlight.RenderLineWithUnderlines(
		before, filterRegions(regions, 0, selStart), m.theme.Syntax, defaultStyle,
		filterUnderlines(ulRanges, 0, selStart))
	afterStyled := highlight.RenderLineWithUnderlines(
		after, filterRegions(regions, selEnd, len(runes)), m.theme.Syntax, defaultStyle,
		filterUnderlines(ulRanges, selEnd, len(runes)))

	return gutter + beforeStyled + selStyle.Render(selected) + afterStyled
}

// renderHighlightLine renders a line with document-highlight background
// spans, optionally including a cursor block at cursorCol (pass -1 for
// no cursor). Syntax highlighting foreground colors are preserved.
func (m *Model) renderHighlightLine(lineNum int, line string, regions []highlight.HighlightRegion, gutterWidth int, defaultStyle lipgloss.Style, spans []colSpan, cursorCol int, ulRanges []highlight.UnderlineRange) string {
	gutter := m.renderGutter(lineNum, gutterWidth)
	runes := []rune(line)
	n := len(runes)

	// Convert colSpans to UnderlineRange for the bg renderer.
	bgRanges := make([]highlight.UnderlineRange, len(spans))
	for i, s := range spans {
		bgRanges[i] = highlight.UnderlineRange{StartCol: s.start, EndCol: min(s.end, n)}
	}

	// No cursor — render the whole line with syntax + highlight bg.
	if cursorCol < 0 {
		return gutter + highlight.RenderLineWithBgRanges(
			line, regions, m.theme.Syntax, defaultStyle, bgRanges, m.theme.Palette.Selection)
	}

	// With cursor — split into before / cursor char / after so the cursor
	// block renders with Reverse and the rest preserves syntax + bg.
	cursorStyle := lipgloss.NewStyle().Reverse(true)
	beforeEnd := min(cursorCol, n)
	afterStart := min(cursorCol+1, n)

	before := string(runes[:beforeEnd])
	after := string(runes[afterStart:])
	cursorChar := " "
	if cursorCol < n {
		r := runes[cursorCol]
		if r >= ' ' {
			cursorChar = string(r)
		}
	}

	beforeStyled := highlight.RenderLineWithBgRanges(
		before, filterRegions(regions, 0, beforeEnd), m.theme.Syntax, defaultStyle,
		filterUnderlines(bgRanges, 0, beforeEnd), m.theme.Palette.Selection)
	afterStyled := highlight.RenderLineWithBgRanges(
		after, filterRegions(regions, afterStart, n), m.theme.Syntax, defaultStyle,
		filterUnderlines(bgRanges, afterStart, n), m.theme.Palette.Selection)

	return gutter + beforeStyled + cursorStyle.Render(cursorChar) + afterStyled
}

func (m *Model) applyCursor(line string, regions []highlight.HighlightRegion, gutterWidth int, defaultStyle lipgloss.Style, col int, ulRanges []highlight.UnderlineRange) string {
	gutter := m.renderGutter(m.state.CursorLine, gutterWidth)
	runes := []rune(line)
	cursorStyle := lipgloss.NewStyle().Reverse(true)
	beforeEnd := min(col, len(runes))
	afterStart := min(col+1, len(runes))
	before := string(runes[:beforeEnd])
	cursorChar := " "
	if col < len(runes) {
		ch := runes[col]
		if ch < ' ' {
			cursorChar = " "
		} else {
			cursorChar = string(ch)
		}
	}
	after := string(runes[afterStart:])
	beforeStyled := highlight.RenderLineWithUnderlines(before, filterRegions(regions, 0, beforeEnd), m.theme.Syntax, defaultStyle,
		filterUnderlines(ulRanges, 0, beforeEnd))
	afterStyled := highlight.RenderLineWithUnderlines(after, filterRegions(regions, afterStart, len(runes)), m.theme.Syntax, defaultStyle,
		filterUnderlines(ulRanges, afterStart, len(runes)))
	return gutter + beforeStyled + cursorStyle.Render(cursorChar) + afterStyled
}

// severitySign maps diagnostic severity to a gutter sign character.
var severitySign = map[lsp.DiagnosticSeverity]string{
	lsp.SeverityError:       "E",
	lsp.SeverityWarning:     "W",
	lsp.SeverityInformation: "I",
	lsp.SeverityHint:        "H",
}

func (m *Model) renderGutter(lineNum, gutterWidth int) string {
	// Check for diagnostic on this line (0-based lineNum matches LSP 0-based lines).
	if diag, ok := m.diagnosticForLine(lineNum); ok {
		sign := severitySign[diag.Severity]
		if sign == "" {
			sign = "?"
		}
		color := m.diagnosticColor(diag.Severity)
		signStyle := lipgloss.NewStyle().Foreground(color).Bold(true)
		numStr := fmt.Sprintf("%*s", gutterWidth-gutterPadding, sign)
		return signStyle.Render(numStr) + strings.Repeat(" ", gutterPadding)
	}

	// Jump marker dot takes priority over plain line number.
	if m.jumpActive && lineNum == m.jumpLine {
		dotStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Primary).Bold(true)
		numStr := fmt.Sprintf("%*s", gutterWidth-gutterPadding, "•")
		return dotStyle.Render(numStr) + strings.Repeat(" ", gutterPadding)
	}

	gutterStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted)
	numStr := fmt.Sprintf("%*d", gutterWidth-gutterPadding, lineNum+1)
	return gutterStyle.Render(numStr) + strings.Repeat(" ", gutterPadding)
}

// clearDiagnosticsOnLine removes all LSP diagnostics anchored to the given
// line. Edits to a line invalidate any diagnostic on it; the server will
// re-publish if the error persists after the next didChange flush.
func (m *Model) clearDiagnosticsOnLine(line int) {
	n := 0
	for _, d := range m.diagnostics {
		if d.Range.Start.Line != line {
			m.diagnostics[n] = d
			n++
		}
	}
	m.diagnostics = m.diagnostics[:n]
}

// diagnosticForLine returns the highest-severity diagnostic on a given line.
// LSP diagnostics take precedence; tree-sitter parse errors are used as
// fallback for instant feedback before the LSP server responds.
func (m *Model) diagnosticForLine(line int) (lsp.Diagnostic, bool) {
	var best lsp.Diagnostic
	found := false
	for _, d := range m.diagnostics {
		if d.Range.Start.Line != line {
			continue
		}
		if !found || d.Severity < best.Severity {
			best = d
			found = true
		}
	}
	if found {
		return best, true
	}
	for _, pe := range m.parseErrors {
		if int(pe.Line-1) != line {
			continue
		}
		return lsp.Diagnostic{
			Range:    lsp.Range{Start: lsp.Position{Line: line, Character: int(pe.Column)}},
			Severity: lsp.SeverityError,
			Message:  pe.Message,
			Source:   "treesitter",
		}, true
	}
	return best, false
}

// diagnosticColor returns the palette color for a diagnostic severity.
func (m *Model) diagnosticColor(severity lsp.DiagnosticSeverity) lipgloss.Color {
	switch severity {
	case lsp.SeverityError:
		return m.theme.Palette.Error
	case lsp.SeverityWarning:
		return m.theme.Palette.Warning
	case lsp.SeverityInformation:
		return m.theme.Palette.Info
	default:
		return m.theme.Palette.Muted
	}
}

// diagnosticDisplayRanges returns underline ranges (in display-column space)
// for all diagnostics and tree-sitter parse errors on a given line.
func (m *Model) diagnosticDisplayRanges(lineNum int, colMap []int) []highlight.UnderlineRange {
	var ranges []highlight.UnderlineRange
	hasLSP := false
	for _, d := range m.diagnostics {
		if d.Range.Start.Line != lineNum {
			continue
		}
		hasLSP = true
		sc := safeMapCol(colMap, d.Range.Start.Character)
		ec := safeMapCol(colMap, d.Range.End.Character)
		if ec <= sc {
			ec = sc + 1
		}
		ranges = append(ranges, highlight.UnderlineRange{StartCol: sc, EndCol: ec})
	}
	if hasLSP {
		return ranges
	}
	for _, pe := range m.parseErrors {
		if int(pe.Line-1) != lineNum {
			continue
		}
		sc := safeMapCol(colMap, int(pe.Column))
		ranges = append(ranges, highlight.UnderlineRange{StartCol: sc, EndCol: sc + 1})
	}
	return ranges
}

// renderDiagnosticVirtualLine renders a virtual text line below a diagnostic
// showing the column range and message.
func (m *Model) renderDiagnosticVirtualLine(diag lsp.Diagnostic, gutterWidth, maxWidth int) string {
	gutter := strings.Repeat(" ", gutterWidth+gutterPadding)
	sc := diag.Range.Start.Character + 1 // 1-based for display
	ec := diag.Range.End.Character + 1
	color := m.diagnosticColor(diag.Severity)
	style := lipgloss.NewStyle().Foreground(color)
	cols := fmt.Sprintf("[%d:%d] ", sc, ec)
	msg := cols + diag.Message
	avail := maxWidth - gutterWidth - gutterPadding
	rMsg := []rune(msg)
	if avail > 0 && len(rMsg) > avail {
		msg = string(rMsg[:avail-1]) + "…"
	}
	return gutter + style.Render(msg)
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
// Find match rendering
// ---------------------------------------------------------------------------

// colSpan describes a display-column range within a line that is part of a
// find match. isCurrent marks the span that corresponds to the active
// navigation match.
type colSpan struct {
	start, end int
	isCurrent  bool
}

// findMatchSpans computes display-column spans for find matches that overlap
// the given line. Returns nil when the find bar is inactive or there are no
// overlapping matches.
func (m *Model) findMatchSpans(lineNum int, displayLen int, colMap []int) []colSpan {
	if !m.findActive || m.findBar == nil {
		return nil
	}
	lineInfo, ok := m.lineIndex.Get(lineNum)
	if !ok {
		return nil
	}
	lineStart := lineInfo.StartPos
	lineEnd := lineStart + displayLen
	matches := m.findBar.Matches()
	currentIdx := m.findBar.MatchIndex()
	var spans []colSpan
	for i, match := range matches {
		if match.End <= lineStart || match.Start >= lineEnd {
			continue
		}
		cs := max(match.Start-lineStart, 0)
		ce := min(match.End-lineStart, displayLen)
		spans = append(spans, colSpan{
			start:     safeMapCol(colMap, cs),
			end:       safeMapCol(colMap, ce),
			isCurrent: i == currentIdx,
		})
	}
	return spans
}

// renderFindMatchLine renders a line with find-match highlighting and an
// optional cursor block (cursorCol < 0 means no cursor on this line).
func (m *Model) renderFindMatchLine(lineNum int, line string, regions []highlight.HighlightRegion, gutterWidth int, defaultStyle lipgloss.Style, spans []colSpan, cursorCol int, ulRanges []highlight.UnderlineRange) string {
	gutter := m.renderGutter(lineNum, gutterWidth)
	runes := []rune(line)
	n := len(runes)

	matchBg := lipgloss.NewStyle().
		Background(m.theme.Palette.Warning).
		Foreground(m.theme.Palette.Background)
	currentBg := lipgloss.NewStyle().
		Background(m.theme.Palette.Primary).
		Foreground(m.theme.Palette.Background)
	cursorStyle := lipgloss.NewStyle().Reverse(true)

	var b strings.Builder
	pos := 0
	for _, span := range spans {
		if span.start > pos {
			m.writeSegment(&b, runes, regions, defaultStyle, pos, span.start, cursorCol, cursorStyle, ulRanges)
			pos = span.start
		}
		endCol := min(span.end, n)
		style := matchBg
		if span.isCurrent {
			style = currentBg
		}
		// Render match span, splitting at cursor if needed.
		if cursorCol >= span.start && cursorCol < endCol {
			if cursorCol > span.start {
				b.WriteString(style.Render(string(runes[span.start:cursorCol])))
			}
			ch := " "
			if cursorCol < n {
				ch = string(runes[cursorCol])
			}
			b.WriteString(cursorStyle.Render(ch))
			if cursorCol+1 < endCol {
				b.WriteString(style.Render(string(runes[cursorCol+1 : endCol])))
			}
		} else {
			b.WriteString(style.Render(string(runes[span.start:endCol])))
		}
		pos = endCol
	}
	if pos < n {
		m.writeSegment(&b, runes, regions, defaultStyle, pos, n, cursorCol, cursorStyle, ulRanges)
	}
	// Cursor past end of line.
	if cursorCol >= 0 && cursorCol >= n {
		b.WriteString(cursorStyle.Render(" "))
	}
	return gutter + b.String()
}

// writeSegment writes a non-match segment [start, end) to b with syntax
// highlighting, splitting at cursorCol if the cursor falls within.
func (m *Model) writeSegment(b *strings.Builder, runes []rune, regions []highlight.HighlightRegion, defaultStyle lipgloss.Style, start, end, cursorCol int, cursorStyle lipgloss.Style, ulRanges []highlight.UnderlineRange) {
	if cursorCol >= start && cursorCol < end {
		if cursorCol > start {
			seg := string(runes[start:cursorCol])
			b.WriteString(highlight.RenderLineWithUnderlines(
				seg, filterRegions(regions, start, cursorCol), m.theme.Syntax, defaultStyle,
				filterUnderlines(ulRanges, start, cursorCol)))
		}
		ch := " "
		if cursorCol < len(runes) {
			r := runes[cursorCol]
			if r >= ' ' {
				ch = string(r)
			}
		}
		b.WriteString(cursorStyle.Render(ch))
		if cursorCol+1 < end {
			seg := string(runes[cursorCol+1 : end])
			b.WriteString(highlight.RenderLineWithUnderlines(
				seg, filterRegions(regions, cursorCol+1, end), m.theme.Syntax, defaultStyle,
				filterUnderlines(ulRanges, cursorCol+1, end)))
		}
		return
	}
	seg := string(runes[start:end])
	b.WriteString(highlight.RenderLineWithUnderlines(
		seg, filterRegions(regions, start, end), m.theme.Syntax, defaultStyle,
		filterUnderlines(ulRanges, start, end)))
}

// ---------------------------------------------------------------------------
// Highlight region helpers
// ---------------------------------------------------------------------------

// highlightSpansForLine computes display-column spans for document-highlight
// ranges that overlap the given line. Returns nil when no highlights exist.
func (m *Model) highlightSpansForLine(lineNum int, colMap []int) []colSpan {
	if len(m.highlightRanges) == 0 {
		return nil
	}
	var spans []colSpan
	for _, h := range m.highlightRanges {
		if h.Range.Start.Line > lineNum || h.Range.End.Line < lineNum {
			continue
		}
		sc := 0
		if h.Range.Start.Line == lineNum {
			sc = safeMapCol(colMap, h.Range.Start.Character)
		}
		ec := safeMapCol(colMap, len(colMap)-1)
		if h.Range.End.Line == lineNum {
			ec = safeMapCol(colMap, h.Range.End.Character)
		}
		if ec > sc {
			spans = append(spans, colSpan{start: sc, end: ec})
		}
	}
	return spans
}

// jumpMarkerSpansForLine returns a display-column span for the jump marker
// if it overlaps the given line. Returns nil otherwise.
func (m *Model) jumpMarkerSpansForLine(lineNum int, colMap []int) []colSpan {
	if !m.jumpActive || lineNum != m.jumpLine {
		return nil
	}
	sc := safeMapCol(colMap, m.jumpStartCol)
	ec := safeMapCol(colMap, m.jumpEndCol)
	if ec <= sc {
		ec = sc + 1
	}
	return []colSpan{{start: sc, end: ec}}
}

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

// filterUnderlines clips underline ranges to [startCol, endCol) and shifts to
// segment-local coordinates, mirroring filterRegions.
func filterUnderlines(uls []highlight.UnderlineRange, startCol, endCol int) []highlight.UnderlineRange {
	var result []highlight.UnderlineRange
	for _, u := range uls {
		if u.EndCol <= startCol || u.StartCol >= endCol {
			continue
		}
		clamped := highlight.UnderlineRange{
			StartCol: max(u.StartCol, startCol) - startCol,
			EndCol:   min(u.EndCol, endCol) - startCol,
		}
		result = append(result, clamped)
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
	m.statusLine.SetNodeType(m.state.CursorNodeType)
	m.statusLine.SetParseErrorCount(len(m.parseErrors))
	m.statusLine.SetJumpBack(m.jumpList.CanBack())
}

func (m *Model) rehighlight() {
	m.regions = m.highlighter.Highlight(m.buf.Content(), m.language)
	m.collectParseErrors()
}

// collectParseErrors extracts syntax errors from the current parse tree.
func (m *Model) collectParseErrors() {
	tree := m.highlighter.Tree()
	if tree == nil {
		m.parseErrors = nil
		return
	}
	m.parseErrors = treesitter.CollectParseErrors(tree.RootNode())
}

// ---------------------------------------------------------------------------
// Find bar helpers
// ---------------------------------------------------------------------------

func (m *Model) handleFindKey(key tea.KeyMsg) (component.Component, tea.Cmd) {
	action := m.findBar.HandleKey(key)
	switch action {
	case findbar.ActionClose:
		m.CloseFindBar()
	case findbar.ActionNextMatch:
		m.findBar.AdvanceMatch()
		m.jumpToCurrentMatch()
	case findbar.ActionPrevMatch:
		m.findBar.RetreatMatch()
		m.jumpToCurrentMatch()
	case findbar.ActionQueryChanged:
		m.recomputeFind()
		m.findBar.NearestMatch(m.state.Cursor)
		m.jumpToCurrentMatch()
	}
	return m, nil
}

func (m *Model) recomputeFind() {
	content := []rune(m.buf.Content())
	searchStart, searchEnd := 0, len(content)
	if m.findBar.FindInSelection() {
		s, e := m.findBar.SelectionRange()
		searchStart = s
		searchEnd = min(e+1, len(content))
	}
	m.findBar.Recompute(content, searchStart, searchEnd)
}

func (m *Model) jumpToCurrentMatch() {
	match, ok := m.findBar.CurrentMatch()
	if !ok {
		return
	}
	m.state.Cursor = match.Start
	m.state.SyncCursorPos()
	m.syncStatusLine()
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

// ---------------------------------------------------------------------------
// Line width helpers
// ---------------------------------------------------------------------------

// fitLine truncates or pads a styled line to exactly m.width visual columns.
// Tabs are pre-expanded in renderVisibleLines before this is called.
func (m *Model) fitLine(line string) string {
	if m.width <= 0 {
		return line
	}
	visWidth := lipgloss.Width(line)
	switch {
	case visWidth > m.width:
		return truncateStyledLine(line, m.width)
	case visWidth < m.width:
		return line + strings.Repeat(" ", m.width-visWidth)
	default:
		return line
	}
}

// ---------------------------------------------------------------------------
// Tab expansion helpers
// ---------------------------------------------------------------------------

// expandTabs replaces tab characters with spaces up to the next tab stop,
// returning the expanded string and a column mapping from raw rune index
// to display column. colMap has len(runes)+1 entries so the sentinel maps
// past-the-end to the final display column.
func expandTabs(line string, tw int) (string, []int) {
	runes := []rune(line)
	colMap := make([]int, len(runes)+1)
	displayCol := 0
	var buf strings.Builder
	buf.Grow(len(line) + len(line)/4)
	for i, r := range runes {
		colMap[i] = displayCol
		if r == '\t' {
			spaces := tw - (displayCol % tw)
			buf.WriteString(strings.Repeat(" ", spaces))
			displayCol += spaces
		} else {
			buf.WriteRune(r)
			displayCol++
		}
	}
	colMap[len(runes)] = displayCol
	return buf.String(), colMap
}

// remapRegions translates highlight region columns from raw rune space
// into display space using the column map produced by expandTabs.
func remapRegions(regions []highlight.HighlightRegion, colMap []int) []highlight.HighlightRegion {
	if len(regions) == 0 {
		return regions
	}
	out := make([]highlight.HighlightRegion, len(regions))
	for i, r := range regions {
		out[i] = highlight.HighlightRegion{
			StartCol: safeMapCol(colMap, r.StartCol),
			EndCol:   safeMapCol(colMap, r.EndCol),
			Category: r.Category,
		}
	}
	return out
}

// safeMapCol looks up a raw column in the map, clamping to the last entry
// for out-of-range indices.
func safeMapCol(colMap []int, col int) int {
	if col < 0 {
		return 0
	}
	if col >= len(colMap) {
		return colMap[len(colMap)-1]
	}
	return colMap[col]
}

// truncateStyledLine clips a styled string (with ANSI escape codes) to fit
// within the given visual width. ANSI CSI sequences are copied verbatim.
func truncateStyledLine(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	var buf strings.Builder
	visWidth := 0
	i := 0
	for i < len(s) && visWidth < w {
		if s[i] == '\x1b' {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && !isCSIEnd(s[j]) {
					j++
				}
				if j < len(s) {
					j++
				}
			}
			buf.WriteString(s[i:j])
			i = j
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		buf.WriteString(s[i : i+size])
		visWidth++
		i += size
	}
	buf.WriteString("\x1b[0m")
	return buf.String()
}

// isCSIEnd reports whether b is a CSI sequence final byte.
func isCSIEnd(b byte) bool {
	return b >= 0x40 && b <= 0x7E
}

// ---------------------------------------------------------------------------
// Bounce shift
// ---------------------------------------------------------------------------

// applyBounceShift applies the bounce visual offset to rendered lines,
// simulating overscroll rubber-banding.
func applyBounceShift(lines []string, offset, viewHeight int) []string {
	if offset == 0 || viewHeight <= 0 {
		return lines
	}
	absOffset := offset
	if absOffset < 0 {
		absOffset = -absOffset
	}
	absOffset = min(absOffset, viewHeight)

	if offset > 0 {
		shift := min(absOffset, len(lines))
		lines = lines[shift:]
	} else {
		pad := make([]string, absOffset, absOffset+len(lines))
		lines = append(pad, lines...)
	}
	// Ensure exactly viewHeight lines after shift.
	for len(lines) < viewHeight {
		lines = append(lines, "")
	}
	if len(lines) > viewHeight {
		lines = lines[:viewHeight]
	}
	return lines
}
