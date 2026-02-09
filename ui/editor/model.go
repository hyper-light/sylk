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
	"github.com/adalundhe/sylk/ui/editor/motion"
	"github.com/adalundhe/sylk/ui/editor/replacebar"
	"github.com/adalundhe/sylk/ui/editor/search"
	"github.com/adalundhe/sylk/ui/editor/signature"
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
	scrollOffset  int
	scrollLeftCol int // horizontal scroll offset in display columns
	bounceOffset  int
	width         int
	height        int

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

	// Warp point indicators: line → info. Set externally by app.
	warpLines map[int]WarpLineInfo

	// Completion popup.
	completionEngine *completion.Engine
	lspSource        *completion.LSPSource

	// Navigation jump list for go-back/forward.
	jumpList *motion.JumpList

	// Find bar (in-file search).
	findBar    *findbar.FindBar
	findActive bool

	// Replace bar (in-file find and replace).
	replaceBar    *replacebar.ReplaceBar
	replaceActive bool

	// Focus state.
	focused bool
	theme   *theme.Theme

	// View cache: avoids re-rendering all visible lines when only
	// the cursor blink state changed.
	vc viewCache
}

// viewCache stores cached renderVisibleLines output for the editor.
type viewCache struct {
	lines           []string // Cached rendered lines (before post-processing).
	viewHeight      int      // Height the cache was rendered at.
	scrollOffset    int      // Vertical scroll offset at render time.
	scrollLeftCol   int      // Horizontal scroll offset at render time.
	cursorLine      int      // Source line the cursor was on.
	cursorRenderIdx int      // Index in lines[] of the cursor line.
	valid           bool     // False when structural state changed.
	cursorOK        bool     // False when only cursor blink changed.
	lastPhase       bool     // Cursor phase at last render; invalidates cursorOK on change.

	// Post-processing cache: stores the final ViewContent body output
	// (after fitLine, applyBounceShift, overlayPopups, join).
	// On cursor-only blinks, we patch one line and splice instead of
	// re-running the full post-processing pipeline.
	postLines   []string // Fitted lines (post fitLine + bounce + popups).
	postOffsets []int    // Byte offset of each postLine in postBody.
	postBody    string   // strings.Join(postLines, "\n").
	postBounce  int      // bounceOffset at cache time.
	postValid   bool     // False when post-processing must re-run.

	// Cached visual width of the cursor line. Stable across blink
	// states (only ANSI codes change, not character content), avoiding
	// repeated lipgloss.Width ANSI parsing in the splice fast path.
	cursorVisWidth int  // Visual column count from last fitLine on cursor line.
	cursorWidthOK  bool // True when cursorVisWidth is valid for current cursor line.
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
		buf:              pt,
		lineIndex:        li,
		undoTree:         ut,
		state:            st,
		currentMode:      mode.ModeNormal,
		normalMode:       mode.NewNormalMode(th),
		insertMode:       mode.NewInsertMode(th),
		highlighter:      highlight.NewHighlighter(th),
		statusLine:       statusline.New(th),
		jumpList:         motion.NewJumpList(),
		hoverPopup:       hover.New(),
		sigHelp:          signature.New(),
		completionEngine: completion.NewEngine(registry),
		lspSource:        lspSrc,
		theme:            th,
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
	m.scrollLeftCol = 0
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
	kind := msgType(incoming)
	handler, ok := msgHandlerTable[kind]
	if !ok {
		return m, nil
	}
	// Non-tick messages may change visual state → invalidate caches.
	// Tick messages only toggle cursor blink → handled in handleTickMsg.
	if kind != msgKindTickMsg {
		m.vc.valid = false
		m.vc.postValid = false
	}
	return handler(m, incoming)
}

// View renders the editor viewport and status line.
func (m *Model) View(cursorVisible bool) string {
	return m.ViewContent(cursorVisible) + "\n" + m.StatusLineView(m.width)
}

// ViewContent renders the editor viewport (find/replace bar + code lines)
// without the status line. Used by the split preview layout so the status
// line can be rendered at full width below the split.
func (m *Model) ViewContent(cursorVisible bool) string {
	if m.filePath == "" {
		return m.renderPlaceholder()
	}

	// Invalidate cursor cache when blink phase changes.
	if cursorVisible != m.vc.lastPhase {
		m.vc.cursorOK = false
		m.vc.lastPhase = cursorVisible
	}

	viewHeight := m.viewportHeight()

	// Reserve space for the find/replace bar when open.
	barStr := ""
	barH := 0
	if m.replaceActive && m.replaceBar != nil {
		barStr = m.replaceBar.View(m.width, m.theme, cursorVisible)
		barH = m.replaceBar.Height()
		viewHeight -= barH
	} else if m.findActive && m.findBar != nil {
		barStr = m.findBar.View(m.width, m.theme, cursorVisible)
		barH = m.findBar.Height()
		viewHeight -= barH
	}

	if viewHeight <= 0 {
		if barStr != "" {
			return barStr
		}
		return ""
	}
	m.adjustScroll(viewHeight)
	m.adjustScrollX()

	// Detect scroll changes since the last cache snapshot.
	scrollChanged := m.scrollOffset != m.vc.scrollOffset ||
		m.scrollLeftCol != m.vc.scrollLeftCol

	if !m.vc.valid || scrollChanged || m.vc.viewHeight != viewHeight {
		// Full re-render: structure, scroll, or size changed.
		m.vc.lines = m.renderVisibleLines(viewHeight, cursorVisible)
		m.vc.viewHeight = viewHeight
		m.vc.scrollOffset = m.scrollOffset
		m.vc.scrollLeftCol = m.scrollLeftCol
		m.vc.cursorLine = m.state.CursorLine
		m.vc.cursorRenderIdx = m.findCursorRenderIdx()
		m.vc.valid = true
		m.vc.cursorOK = true
		m.vc.postValid = false    // Force full post-processing.
		m.vc.cursorWidthOK = false // Invalidate cached cursor line width.
	} else if !m.vc.cursorOK {
		// Cursor-only patch: re-render just the cursor line.
		m.patchCursorLine(cursorVisible)
		m.vc.cursorOK = true

		// Fast path: if post cache is valid and bounce unchanged, splice
		// only the patched cursor line into the cached output.
		if m.vc.postValid && m.bounceOffset == m.vc.postBounce {
			return m.splicePostCursorLine(barStr, viewHeight)
		}
		// Bounce changed or post cache invalid — fall through to full post.
	}

	// Full post-processing: clone, tilde fill, fit, bounce, popups, join.
	body := m.buildPostOutput(viewHeight)
	if barStr != "" {
		return barStr + "\n" + body
	}
	return body
}

// buildPostOutput runs the full post-processing pipeline and caches the result.
func (m *Model) buildPostOutput(viewHeight int) string {
	lines := slices.Clone(m.vc.lines)

	for len(lines) < viewHeight {
		lines = append(lines, m.renderTildeLine())
	}
	for i, line := range lines {
		lines[i] = m.fitLine(line)
	}
	lines = applyBounceShift(lines, m.bounceOffset, viewHeight)
	lines = m.overlayPopups(lines, viewHeight)

	// Cache post-processed state for cursor-only splice on next blink.
	m.vc.postLines = lines
	m.vc.postBody = strings.Join(lines, "\n")
	m.vc.postBounce = m.bounceOffset
	m.vc.postValid = true

	// Precompute byte offsets for each line in postBody.
	m.vc.postOffsets = make([]int, len(lines))
	offset := 0
	for i, line := range lines {
		m.vc.postOffsets[i] = offset
		offset += len(line) + 1 // +1 for "\n" separator.
	}

	return m.vc.postBody
}

// splicePostCursorLine patches the cached post-processed output with the
// newly rendered cursor line. Runs fitLine on the one changed line and
// splices it at the cached byte offset. Cost: O(1 line + frame_size memcpy).
func (m *Model) splicePostCursorLine(barStr string, viewHeight int) string {
	ri := m.vc.cursorRenderIdx
	if ri < 0 || ri >= len(m.vc.postLines) || ri >= len(m.vc.postOffsets) {
		// Safety fallback: full post-processing.
		body := m.buildPostOutput(viewHeight)
		if barStr != "" {
			return barStr + "\n" + body
		}
		return body
	}

	// Fit the single patched line. Use cached visual width when available:
	// cursor blink only changes ANSI codes (reverse video), not character
	// content, so lipgloss.Width is stable across blink states.
	fitted := m.fitLineCached(m.vc.lines[ri])

	// Splice into cached postBody at the precomputed byte offset.
	oldLine := m.vc.postLines[ri]
	start := m.vc.postOffsets[ri]
	end := start + len(oldLine)

	m.vc.postLines[ri] = fitted
	m.vc.postBody = m.vc.postBody[:start] + fitted + m.vc.postBody[end:]

	// Update byte offsets if the new line has a different length.
	if delta := len(fitted) - len(oldLine); delta != 0 {
		for i := ri + 1; i < len(m.vc.postOffsets); i++ {
			m.vc.postOffsets[i] += delta
		}
	}

	if barStr != "" {
		return barStr + "\n" + m.vc.postBody
	}
	return m.vc.postBody
}

// findCursorRenderIdx computes the index in vc.lines of the cursor line,
// accounting for diagnostic virtual text lines above it.
func (m *Model) findCursorRenderIdx() int {
	idx := 0
	for i := m.scrollOffset; i < m.state.CursorLine && i < m.scrollOffset+m.vc.viewHeight; i++ {
		idx++ // The code line itself.
		if _, ok := m.diagnosticForLine(i); ok {
			idx++ // Diagnostic virtual line.
		}
	}
	return idx
}

// patchCursorLine re-renders only the cursor line in the view cache.
// Uses buildPatchCtx to avoid splitting the full buffer — only the cursor
// line content is materialized via LineIndex + PieceTable.Substring.
func (m *Model) patchCursorLine(cursorVisible bool) {
	ri := m.vc.cursorRenderIdx
	if ri < 0 || ri >= len(m.vc.lines) {
		m.vc.valid = false // Safety: fall back to full re-render.
		return
	}
	ctx := m.buildPatchCtx()
	ctx.cursorVisible = cursorVisible
	if m.state.CursorLine >= ctx.totalLines {
		m.vc.valid = false
		return
	}
	m.vc.lines[ri] = m.renderOneLine(&ctx, m.state.CursorLine)
}

// StatusLineView renders the status line at the given width.
func (m *Model) StatusLineView(width int) string {
	return m.statusLine.View(width)
}

// renderPlaceholder renders a vertically and horizontally centered placeholder
// message when no file is open.
func (m *Model) renderPlaceholder() string {
	const placeholder = "Open any file to edit."
	color := m.theme.Palette.Muted
	if m.focused {
		color = m.theme.Palette.Primary
	}
	style := lipgloss.NewStyle().Foreground(color)

	textWidth := lipgloss.Width(placeholder)
	padLeft := max((m.width-textWidth)/2, 0)
	centeredLine := strings.Repeat(" ", padLeft) + style.Render(placeholder)

	vh := m.viewportHeight()
	lines := make([]string, vh)
	midRow := vh / 2
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

func (m *Model) ID() component.FocusID { return component.FocusEditor }
func (m *Model) Focused() bool         { return m.focused }
func (m *Model) SetFocused(focused bool) {
	if !focused && isVisualMode(m.currentMode) {
		m.visualMode = nil
		m.currentMode = mode.ModeNormal
	}
	m.focused = focused
	m.invalidateView()
}

// ---------------------------------------------------------------------------
// Public accessors
// ---------------------------------------------------------------------------

// CurrentMode returns the active editor mode (Normal, Insert, etc.).
func (m *Model) CurrentMode() mode.Mode { return m.currentMode }

// SetStatusMode overrides the mode displayed in the status line without
// changing the editor's actual operating mode. Used for PREVIEW display.
func (m *Model) SetStatusMode(md mode.Mode) { m.statusLine.SetMode(md) }

// RestoreStatusMode resets the status line to show the editor's actual mode.
func (m *Model) RestoreStatusMode() { m.statusLine.SetMode(m.currentMode) }

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
	m.scrollLeftCol = 0
	m.DismissSignatureHelp()
	m.buf = buffer.NewPieceTable("")
	m.regions = nil
}

// ---------------------------------------------------------------------------
// Detach / Attach — zero-copy state transfer for the tiered editor cache.
// ---------------------------------------------------------------------------

// Detach extracts the per-file state from the editor, returning it as a
// WarmState suitable for caching. The editor is left in a cleared state.
//
// The JumpList is intentionally left in place (it is cross-file and global).
// Highlights are NOT captured — they are regenerated on Attach.
func (m *Model) Detach() *WarmState {
	ws := &WarmState{
		FilePath:     m.filePath,
		Language:     m.language,
		Modified:     m.modified,
		Buf:          m.buf,
		LineIndex:    m.lineIndex,
		UndoTree:     m.undoTree,
		Cursor:       m.state.Cursor,
		CursorLine:   m.state.CursorLine,
		CursorCol:    m.state.CursorCol,
		ScrollOffset: m.scrollOffset,
	}

	// Reset the editor to an empty state, reusing ClearFile's pattern
	// but also wiring state pointers so the editor remains consistent.
	m.filePath = ""
	m.language = ""
	m.modified = false
	m.lspDirty = false
	m.lastEditAt = time.Time{}
	m.diagnostics = nil
	m.parseErrors = nil
	m.highlightRanges = nil
	m.jumpActive = false
	m.scrollOffset = 0
	m.scrollLeftCol = 0
	m.DismissHover()
	m.DismissSignatureHelp()

	m.buf = buffer.NewPieceTable("")
	m.lineIndex = buffer.NewLineIndex(m.buf)
	m.undoTree = buffer.NewUndoTree(0)

	m.state.Buffer = m.buf
	m.state.LineIndex = m.lineIndex
	m.state.UndoTree = m.undoTree
	m.state.Cursor = 0
	m.state.SyncCursorPos()

	m.regions = nil
	return ws
}

// AttachWarm restores a warm-tier cached state into the editor. The
// PieceTable, LineIndex, and UndoTree are injected directly (O(1)).
// Syntax highlighting is regenerated from the live PieceTable.
func (m *Model) AttachWarm(ws *WarmState) {
	m.filePath = ws.FilePath
	m.language = ws.Language
	m.modified = ws.Modified

	m.buf = ws.Buf
	m.lineIndex = ws.LineIndex
	m.undoTree = ws.UndoTree

	m.state.Buffer = m.buf
	m.state.LineIndex = m.lineIndex
	m.state.UndoTree = m.undoTree
	m.state.Cursor = ws.Cursor
	m.state.CursorLine = ws.CursorLine
	m.state.CursorCol = ws.CursorCol

	m.scrollOffset = ws.ScrollOffset

	// Reset transient per-file state.
	m.lspDirty = false
	m.lastEditAt = time.Time{}
	m.diagnostics = nil
	m.parseErrors = nil
	m.highlightRanges = nil
	m.jumpActive = false
	m.DismissHover()
	m.DismissSignatureHelp()

	// Regenerate highlights from the live buffer.
	m.regions = m.highlighter.Highlight(m.buf.Content(), m.language)
	m.collectParseErrors()
	m.syncStatusLine()
}

// AttachCold restores a cold-tier cached state into the editor. The
// PieceTable and LineIndex are rebuilt from the serialized content; the
// UndoTree is injected directly, preserving full undo history.
func (m *Model) AttachCold(cs *ColdState) {
	m.filePath = cs.FilePath
	m.language = cs.Language
	m.modified = cs.Modified

	m.buf = buffer.NewPieceTable(cs.Content)
	m.lineIndex = buffer.NewLineIndex(m.buf)
	m.undoTree = cs.UndoTree

	m.state.Buffer = m.buf
	m.state.LineIndex = m.lineIndex
	m.state.UndoTree = m.undoTree
	m.state.Cursor = cs.Cursor
	m.state.CursorLine = cs.CursorLine
	m.state.CursorCol = cs.CursorCol

	m.scrollOffset = cs.ScrollOffset

	// Reset transient per-file state.
	m.lspDirty = false
	m.lastEditAt = time.Time{}
	m.diagnostics = nil
	m.parseErrors = nil
	m.highlightRanges = nil
	m.jumpActive = false
	m.DismissHover()
	m.DismissSignatureHelp()

	// Regenerate highlights from the rebuilt buffer.
	m.regions = m.highlighter.Highlight(cs.Content, m.language)
	m.collectParseErrors()
	m.syncStatusLine()
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

// GoToLineCol moves the cursor to the given 0-indexed line and column
// and adjusts the scroll offset so the line is centered in the viewport.
func (m *Model) GoToLineCol(line, col int) {
	m.state.Cursor = m.lineIndex.LineColToPos(line, col)
	m.state.ClampCursor(1)
	vh := m.viewportHeight()
	if vh > 0 {
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
	m.invalidateView()
}

// ClearJumpMarker removes the jump marker.
func (m *Model) ClearJumpMarker() {
	m.jumpActive = false
	m.invalidateView()
}

// WarpLineInfo describes a warp point on a specific line.
type WarpLineInfo struct {
	Slot     int // 1-indexed slot number for display (1-9).
	StartCol int // 0-indexed start column of highlighted symbol (rune offset).
	EndCol   int // 0-indexed end column (exclusive, rune offset).
}

// SetWarpLines updates the warp point indicators. The map keys are 0-indexed
// line numbers. Nil clears all.
func (m *Model) SetWarpLines(lines map[int]WarpLineInfo) {
	m.warpLines = lines
	m.invalidateView()
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
	m.vc.valid = false // Invalidate view cache so the new popup renders.
}

// DismissHover hides the hover tooltip.
func (m *Model) DismissHover() {
	m.hoverPopup.Dismiss()
	m.hoverActive = false
	m.vc.valid = false // Invalidate view cache so the dismissed popup re-renders.
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

// DismissCompletion hides the completion popup.
func (m *Model) DismissCompletion() { m.completionEngine.Dismiss() }

// DismissAllOverlays dismisses hover, signature help, and completion popups.
func (m *Model) DismissAllOverlays() {
	m.DismissHover()
	m.DismissSignatureHelp()
	m.DismissCompletion()
}

// ---------------------------------------------------------------------------
// Document Highlights
// ---------------------------------------------------------------------------

// SetHighlightRanges replaces the current document highlight ranges.
func (m *Model) SetHighlightRanges(ranges []lsp.DocumentHighlight) {
	m.highlightRanges = ranges
	m.invalidateView()
}

// ClearHighlightRanges removes all document highlight ranges.
func (m *Model) ClearHighlightRanges() {
	m.highlightRanges = nil
	m.invalidateView()
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
	col     int    // visual column in the viewport line (0 = left edge)
}

// newPopupPlacement derives height and width from actual rendered content.
func newPopupPlacement(content string, start, col int) popupPlacement {
	if content == "" {
		return popupPlacement{}
	}
	firstLine := strings.SplitN(content, "\n", 2)[0]
	return popupPlacement{
		content: content,
		start:   start,
		height:  strings.Count(content, "\n") + 1,
		width:   lipgloss.Width(firstLine),
		col:     col,
	}
}

// rowInterval is a half-open viewport row range [start, end).
type rowInterval struct {
	start, end int
}

// layoutCandidate represents one possible allocation of space between
// completion and hover. Scored by total visible content lines.
type layoutCandidate struct {
	compHeight  int         // total rows allocated to completion (0 = not shown)
	hoverZone   rowInterval // zone where hover is placed
	hoverHeight int         // total rows allocated to hover (0 = not shown)
	score       int         // total visible content lines (higher = better)
	anchorGap   int         // row distance from hover zone to anchor (lower = better)
}

// layoutScore computes the total visible content lines for a given
// allocation. Higher is better.
func layoutScore(compH, compItems, hoverH, hoverContentLines int) int {
	compVisible := min(max(compH-completion.PopupChromeRows(), 0), compItems)
	hoverVisible := min(max(hoverH-hover.BorderRows, 0), hoverContentLines)
	return compVisible + hoverVisible
}

// contentToViewCol converts a buffer line/column to a visual column in
// the viewport line (accounting for gutter width, tab expansion, and
// horizontal scroll).
func (m *Model) contentToViewCol(lineNum, col int) int {
	totalLines := m.lineIndex.Count()
	if lineNum < 0 || lineNum >= totalLines {
		return m.gutterWidth(totalLines)
	}
	line := m.buf.Line(lineNum)
	_, colMap := expandTabs(line, editorTabWidth)
	displayCol := safeMapCol(colMap, col)
	gutterW := m.gutterWidth(totalLines)
	return gutterW + max(displayCol-m.scrollLeftCol, 0)
}

// popupLayout computes collision-free viewport positions for all active
// popups (signature help, hover tooltip, completion), maximizing total
// visible content via cost-function optimization.
func (m *Model) popupLayout(viewHeight int) (sig, hov, comp popupPlacement) {
	cursorRow := m.contentLineToViewRow(m.state.CursorLine)
	sig = m.sigHelpPlacement(cursorRow)
	comp, hov = m.layoutCompAndHover(viewHeight, cursorRow, sig)
	return
}

// layoutCompAndHover dispatches to the appropriate placement strategy
// based on which popups are active.
func (m *Model) layoutCompAndHover(viewHeight, cursorRow int, sig popupPlacement) (comp, hov popupPlacement) {
	compActive := m.completionEngine.Active()
	hoverActive := m.hoverActive && m.hoverPopup.Active()
	if !compActive && !hoverActive {
		return
	}
	if !hoverActive {
		comp = m.completionPlacement(cursorRow, viewHeight)
		return
	}
	anchorRow := m.contentLineToViewRow(m.hoverPopup.AnchorLine())
	if !compActive {
		zones := baseZones(viewHeight, cursorRow, anchorRow, sig)
		hov = m.placeHoverInZones(zones, anchorRow)
		return
	}
	return m.optimizedDualLayout(viewHeight, cursorRow, anchorRow, sig)
}

// sigHelpPlacement renders signature help and positions it above the anchor
// line (the function call). The popup is suppressed when the anchor line is
// not visible or there is insufficient room above it to display the hint.
func (m *Model) sigHelpPlacement(_ int) popupPlacement {
	if !m.sigHelpActive || !m.sigHelp.Active() {
		return popupPlacement{}
	}
	anchor := m.sigHelp.AnchorLine()
	viewHeight := m.viewportHeight()
	if anchor < m.scrollOffset || anchor >= m.scrollOffset+viewHeight {
		return popupPlacement{}
	}
	popup := m.sigHelp.View(m.width, m.theme)
	col := m.contentToViewCol(anchor, m.sigHelp.AnchorCol())
	p := newPopupPlacement(popup, 0, col)
	anchorRow := m.contentLineToViewRow(anchor)
	if anchorRow < p.height {
		return popupPlacement{}
	}
	p.start = anchorRow - p.height
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
	col := m.contentToViewCol(m.state.CursorLine, m.completionEngine.StartCol())
	return newPopupPlacement(popup, start, col)
}

// baseZones computes free intervals with only sig, cursor, and anchor
// reserved. Completion is excluded so the optimizer can explore different
// comp heights.
func baseZones(viewHeight, cursorRow, anchorRow int, sig popupPlacement) []rowInterval {
	reserved := make([]rowInterval, 0, 3)
	if sig.height > 0 {
		reserved = append(reserved, rowInterval{sig.start, sig.start + sig.height})
	}
	reserved = append(reserved, rowInterval{cursorRow, cursorRow + 1})
	if anchorRow != cursorRow {
		reserved = append(reserved, rowInterval{anchorRow, anchorRow + 1})
	}
	slices.SortFunc(reserved, func(a, b rowInterval) int {
		return cmp.Compare(a.start, b.start)
	})
	return gapIntervals(reserved, viewHeight)
}

// optimizedDualLayout finds the allocation of space between completion and
// hover that maximizes total visible content lines.
func (m *Model) optimizedDualLayout(viewHeight, cursorRow, anchorRow int, sig popupPlacement) (comp, hov popupPlacement) {
	compStart := cursorRow + 1
	compItems := len(m.completionEngine.Items())
	compIdeal := completion.PopupChromeRows() + min(compItems, completion.MaxVisibleItems())
	compMin := completion.PopupChromeRows() + 1
	hoverContentLines := m.hoverPopup.ContentHeight(m.width, m.theme)
	hoverIdeal := hover.BorderRows + hoverContentLines
	hoverMin := hover.BorderRows + 1

	zones := baseZones(viewHeight, cursorRow, anchorRow, sig)
	// Cap comp ideal to the space actually available in its zone.
	compAvail := zoneSpaceFrom(zones, compStart)
	compIdeal = min(compIdeal, compAvail)
	compMin = min(compMin, compAvail)

	best := findBestLayout(zones, compStart, compMin, compIdeal, compItems,
		anchorRow, hoverMin, hoverIdeal, hoverContentLines)

	comp = m.renderComp(compStart, best.compHeight)
	hov = m.renderHover(best.hoverZone, best.hoverHeight, anchorRow)
	return
}

// zoneSpaceFrom returns the available rows from pos to the end of the zone
// containing pos. Returns 0 if pos is not inside any zone.
func zoneSpaceFrom(zones []rowInterval, pos int) int {
	for _, z := range zones {
		if pos >= z.start && pos < z.end {
			return z.end - pos
		}
	}
	return 0
}

// findBestLayout evaluates all candidate allocations and returns the one
// with the highest score.
func findBestLayout(
	zones []rowInterval,
	compStart, compMin, compIdeal, compItems int,
	anchorRow, hoverMin, hoverIdeal, hoverContentLines int,
) layoutCandidate {
	best := layoutCandidate{score: -1}
	for _, z := range zones {
		candidates := zoneCandidates(z, compStart, compMin, compIdeal, compItems,
			anchorRow, hoverMin, hoverIdeal, hoverContentLines)
		best = bestOfSlice(best, candidates)
	}
	return compFallback(best, compIdeal, compItems)
}

// bestOfSlice returns the candidate with the highest score among current
// best and all entries in candidates. Ties broken by proximity to anchor
// (smaller anchorGap wins), then by preferring below-anchor zones.
func bestOfSlice(current layoutCandidate, candidates []layoutCandidate) layoutCandidate {
	for _, c := range candidates {
		if betterCandidate(c, current) {
			current = c
		}
	}
	return current
}

// betterCandidate reports whether a is a better layout than b.
// Primary: closer to hover anchor (mouse or K position). Secondary: higher
// total visible content. Tertiary: prefer below-anchor zone.
func betterCandidate(a, b layoutCandidate) bool {
	if a.anchorGap != b.anchorGap {
		return a.anchorGap < b.anchorGap
	}
	if a.score != b.score {
		return a.score > b.score
	}
	return a.hoverZone.start > b.hoverZone.start // prefer below
}

// compFallback returns a comp-only layout when no valid dual layout exists.
func compFallback(best layoutCandidate, compIdeal, compItems int) layoutCandidate {
	if best.score >= 0 {
		return best
	}
	return layoutCandidate{
		compHeight: compIdeal,
		score:      min(max(compIdeal-completion.PopupChromeRows(), 0), compItems),
	}
}

// zoneCandidates generates all valid layout candidates for placing hover
// in zone z. When comp also occupies z, it tries different comp heights.
func zoneCandidates(
	z rowInterval,
	compStart, compMin, compIdeal, compItems int,
	anchorRow, hoverMin, hoverIdeal, hoverContentLines int,
) []layoutCandidate {
	compInZone := compStart >= z.start && compStart < z.end
	if !compInZone {
		return independentCandidates(z, compIdeal, compItems,
			anchorRow, hoverMin, hoverIdeal, hoverContentLines)
	}
	return sharedZoneCandidates(z, compStart, compMin, compIdeal, compItems,
		anchorRow, hoverMin, hoverIdeal, hoverContentLines)
}

// independentCandidates returns a single candidate where comp gets its
// ideal height and hover gets the full zone.
func independentCandidates(
	z rowInterval,
	compIdeal, compItems int,
	anchorRow, hoverMin, hoverIdeal, hoverContentLines int,
) []layoutCandidate {
	zoneH := z.end - z.start
	hoverH := min(zoneH, hoverIdeal)
	if hoverH < hoverMin {
		return nil
	}
	return []layoutCandidate{{
		compHeight:  compIdeal,
		hoverZone:   z,
		hoverHeight: hoverH,
		score:       layoutScore(compIdeal, compItems, hoverH, hoverContentLines),
		anchorGap:   zoneGap(z, anchorRow),
	}}
}

// sharedZoneCandidates generates candidates by varying comp height within
// a zone that both popups want to occupy.
func sharedZoneCandidates(
	z rowInterval,
	compStart, compMin, compIdeal, compItems int,
	anchorRow, hoverMin, hoverIdeal, hoverContentLines int,
) []layoutCandidate {
	maxCompInZone := z.end - compStart
	compHi := min(compIdeal, maxCompInZone)
	compLo := min(compMin, maxCompInZone)
	candidates := make([]layoutCandidate, 0, compHi-compLo+1)
	for ch := compLo; ch <= compHi; ch++ {
		hc := sharedHoverCandidate(z, compStart, ch, compItems, anchorRow,
			hoverMin, hoverIdeal, hoverContentLines)
		if hc.score >= 0 {
			candidates = append(candidates, hc)
		}
	}
	return candidates
}

// sharedHoverCandidate computes the hover allocation for a fixed comp
// height within a shared zone.
func sharedHoverCandidate(
	z rowInterval, compStart, compH, compItems int,
	anchorRow, hoverMin, hoverIdeal, hoverContentLines int,
) layoutCandidate {
	compEnd := compStart + compH
	hz := hoverSubZone(z, compStart, compEnd, anchorRow)
	hoverH := min(hz.end-hz.start, hoverIdeal)
	if hoverH < hoverMin {
		return layoutCandidate{score: -1}
	}
	return layoutCandidate{
		compHeight:  compH,
		hoverZone:   hz,
		hoverHeight: hoverH,
		score:       layoutScore(compH, compItems, hoverH, hoverContentLines),
		anchorGap:   zoneGap(hz, anchorRow),
	}
}

// hoverSubZone selects the sub-interval within zone z that is available
// for hover after comp occupies [compStart, compEnd). Picks the sub-zone
// closer to anchorRow.
func hoverSubZone(z rowInterval, compStart, compEnd, anchorRow int) rowInterval {
	above := rowInterval{z.start, compStart}
	below := rowInterval{compEnd, z.end}
	if above.end-above.start <= 0 {
		return below
	}
	if below.end-below.start <= 0 {
		return above
	}
	return closerToAnchor(above, below, anchorRow)
}

// closerToAnchor returns whichever interval is closer to anchorRow.
// Ties broken by preferring below.
func closerToAnchor(a, b rowInterval, anchorRow int) rowInterval {
	if zoneGap(a, anchorRow) < zoneGap(b, anchorRow) {
		return a
	}
	return b
}

// renderComp renders completion constrained to the allocated height.
func (m *Model) renderComp(start, allocHeight int) popupPlacement {
	if allocHeight <= 0 || !m.completionEngine.Active() {
		return popupPlacement{}
	}
	popup := m.completionEngine.Render(m.width, allocHeight, m.theme)
	if popup == "" {
		return popupPlacement{}
	}
	col := m.contentToViewCol(m.state.CursorLine, m.completionEngine.StartCol())
	return newPopupPlacement(popup, start, col)
}

// renderHover renders hover constrained to the allocated zone and height.
func (m *Model) renderHover(zone rowInterval, allocHeight, anchorRow int) popupPlacement {
	if allocHeight <= 0 || !m.hoverActive {
		return popupPlacement{}
	}
	popup := m.hoverPopup.View(m.width, allocHeight, m.theme)
	col := m.contentToViewCol(m.hoverPopup.AnchorLine(), m.hoverPopup.AnchorCol())
	p := newPopupPlacement(popup, 0, col)
	p.start = hoverSlot(zone, anchorRow, p.height)
	return p
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

// placeHoverInZones picks the zone that maximizes visible hover content,
// breaking ties by proximity to anchor, then preferring below.
func (m *Model) placeHoverInZones(zones []rowInterval, anchorRow int) popupPlacement {
	contentLines := m.hoverPopup.ContentHeight(m.width, m.theme)
	ideal := hover.BorderRows + contentLines
	minH := hover.BorderRows + 1
	best := bestHoverOnlyZone(zones, anchorRow, minH, ideal, contentLines)
	return m.renderHover(best, min(best.end-best.start, ideal), anchorRow)
}

// bestHoverOnlyZone selects the optimal free zone for hover-only placement.
// Scored by visible content; ties broken by proximity then below-preference.
func bestHoverOnlyZone(zones []rowInterval, anchorRow, minH, idealH, contentLines int) rowInterval {
	best := rowInterval{}
	bestScore := -1
	for _, z := range zones {
		h := min(z.end-z.start, idealH)
		if h < minH {
			continue
		}
		score := min(max(h-hover.BorderRows, 0), contentLines)
		if betterHoverZone(score, z, bestScore, best, anchorRow) {
			best, bestScore = z, score
		}
	}
	return best
}

// betterHoverZone reports whether (score, z) is a better hover zone than
// (bestScore, best). Primary: closer to anchor. Secondary: more content.
// Tertiary: prefer below.
func betterHoverZone(score int, z rowInterval, bestScore int, best rowInterval, anchorRow int) bool {
	dz, db := zoneGap(z, anchorRow), zoneGap(best, anchorRow)
	if dz != db {
		return dz < db
	}
	if score != bestScore {
		return score > bestScore
	}
	return z.start > best.start // prefer below
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
	applyPopupOverlay(lines, sig, m.width)
	applyPopupOverlay(lines, hover, m.width)
	applyPopupOverlay(lines, comp, m.width)
	return lines
}

// applyPopupOverlay splices a popup's rendered lines into the viewport at
// the correct column, preserving text before and after the popup.
func applyPopupOverlay(lines []string, p popupPlacement, lineWidth int) {
	if p.content == "" {
		return
	}
	// Clamp column so the popup stays within the viewport.
	col := max(p.col, 0)
	if lineWidth > 0 && p.width > 0 {
		col = min(col, max(lineWidth-p.width, 0))
	}
	for i, pLine := range strings.Split(p.content, "\n") {
		row := p.start + i
		if row < 0 || row >= len(lines) {
			continue
		}
		original := lines[row]
		left := truncateStyledLine(original, col)
		right := skipStyledCols(original, col+p.width)
		lines[row] = left + pLine + right
	}
}

// skipStyledCols drops the first skip visible columns from a styled string
// (containing ANSI escape sequences) and returns the remainder. Escape
// sequences within the skipped region are discarded; the returned text
// carries its own embedded ANSI styling.
func skipStyledCols(s string, skip int) string {
	if skip <= 0 {
		return s
	}
	vis := 0
	i := 0
	for i < len(s) && vis < skip {
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
			i = j
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		vis++
		i += size
	}
	if i >= len(s) {
		return ""
	}
	// Reset ANSI state so popup styling does not bleed into the remainder.
	return "\x1b[0m" + s[i:]
}

// acceptCompletion replaces the current prefix with the selected completion
// item's text and deactivates the popup.
func (m *Model) acceptCompletion() {
	startPos := m.completionEngine.StartCol()
	item := m.completionEngine.Accept()
	if item == nil {
		return
	}

	m.undoTree.BeginGroup()

	// Delete the typed prefix [startPos, cursor).
	prefixLen := m.state.Cursor - startPos
	if prefixLen > 0 {
		old := m.buf.Substring(startPos, m.state.Cursor)
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
	m.undoTree.EndGroup()
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
		old := m.buf.Substring(startPos, m.state.Cursor)
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
	if m.bounceOffset == offset {
		return
	}
	m.bounceOffset = offset
	m.invalidateView()
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
		old := m.buf.Substring(startPos, endPos)
		m.buf.Delete(startPos, endPos-startPos)
		m.undoTree.Record(buffer.EditOp{
			Type:    buffer.EditDelete,
			Pos:     startPos,
			OldText: old,
		})
	}

	// Insert the new text.
	if edit.NewText != "" {
		m.buf.Insert(startPos, edit.NewText)
		m.undoTree.Record(buffer.EditOp{
			Type: buffer.EditInsert,
			Pos:  startPos,
			Text: edit.NewText,
		})
	}

	// Single rebuild for the combined delete + insert effect.
	m.lineIndex.Rebuild(m.buf)
}

// lspPositionToRune converts an LSP Position (line + UTF-16 character offset)
// to an absolute rune position in the buffer.
func (m *Model) lspPositionToRune(pos lsp.Position) int {
	info, ok := m.lineIndex.Get(pos.Line)
	if !ok {
		return m.buf.Length()
	}
	lineText := m.buf.Substring(info.StartPos, info.StartPos+info.Length)
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
	return m.buf.Substring(start, end+1)
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
	hadSelection := m.visualMode != nil
	if hadSelection {
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
	m.vc.valid = false
	m.syncStatusLine()
	if hadSelection {
		m.clearSelectionSearch()
	}
}

// StartDragSelection begins a mouse-initiated text selection anchored at the
// current cursor position, entering visual-char mode.
func (m *Model) StartDragSelection() {
	m.visualMode = mode.NewVisualMode(m.theme, m.state.Cursor, mode.VisualChar)
	m.currentMode = mode.ModeVisual
	m.vc.valid = false
	m.syncStatusLine()
	m.recomputeSelectionSearch()
}

// ExtendDragSelection moves the cursor and updates the visual selection
// endpoint during a mouse drag.
func (m *Model) ExtendDragSelection(x, y int) {
	if m.visualMode == nil {
		return
	}
	m.setCursorFromViewport(x, y)
	m.visualMode.State.CursorPos = m.state.Cursor
	m.vc.valid = false
	m.syncStatusLine()
	m.recomputeSelectionSearch()
}

// recomputeSelectionSearch auto-enables [Sel] and re-runs the active search
// bar's query when the user makes a selection while a bar is open.
func (m *Model) recomputeSelectionSearch() {
	if m.findActive && m.findBar != nil {
		m.findBar.EnableSelToggle()
		m.recomputeFind()
		return
	}
	if m.replaceActive && m.replaceBar != nil {
		m.replaceBar.EnableSelToggle()
		m.recomputeReplace()
	}
}

// clearSelectionSearch disables [Sel] and recomputes when the selection is
// cleared while a search bar is open with [Sel] active.
func (m *Model) clearSelectionSearch() {
	if m.findActive && m.findBar != nil && m.findBar.FindInSelection() {
		m.findBar.DisableSelToggle()
		m.recomputeFind()
		return
	}
	if m.replaceActive && m.replaceBar != nil && m.replaceBar.FindInSelection() {
		m.replaceBar.DisableSelToggle()
		m.recomputeReplace()
	}
}

// ClearSelection clears any active visual-mode selection.
func (m *Model) ClearSelection() {
	if m.visualMode != nil {
		m.visualMode = nil
		m.currentMode = mode.ModeNormal
		m.state.ClampCursor(1)
		m.clearSelectionSearch()
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
	m.CloseReplaceBar()
	var selStart, selEnd int
	hasSel := m.visualMode != nil
	if hasSel {
		selStart, selEnd = m.visualMode.State.CharRange()
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

// ---------------------------------------------------------------------------
// Replace bar (in-file find and replace)
// ---------------------------------------------------------------------------

// OpenReplaceBar opens the find-and-replace bar, pre-populating the search
// field from the active selection. Closes the find bar if open.
func (m *Model) OpenReplaceBar() {
	m.CloseFindBar()
	query := m.SelectedText()
	var selStart, selEnd int
	hasSel := m.visualMode != nil
	if hasSel {
		selStart, selEnd = m.visualMode.State.CharRange()
	}
	m.replaceBar = replacebar.New(query, selStart, selEnd, hasSel)
	m.replaceActive = true
	m.recomputeReplace()
}

// CloseReplaceBar closes the find-and-replace bar and clears state.
func (m *Model) CloseReplaceBar() {
	m.replaceBar = nil
	m.replaceActive = false
}

// ToggleReplaceBar opens the replace bar if closed, or closes it if open.
func (m *Model) ToggleReplaceBar() {
	if m.replaceActive {
		m.CloseReplaceBar()
		return
	}
	m.OpenReplaceBar()
}

// ReplaceActive reports whether the replace bar is open.
func (m *Model) ReplaceActive() bool { return m.replaceActive }

// overlayActive reports whether any overlay input bar (find or replace)
// currently owns keyboard focus, suppressing the editor cursor.
func (m *Model) overlayActive() bool { return m.findActive || m.replaceActive }

// ReplaceBarHeight returns the number of viewport rows consumed by the
// replace bar (0 when closed).
func (m *Model) ReplaceBarHeight() int {
	if m.replaceActive && m.replaceBar != nil {
		return m.replaceBar.Height()
	}
	return 0
}

// HandleReplaceBarClick processes a mouse click at viewport-local (x, y)
// that falls within the replace bar area. Returns true if consumed.
func (m *Model) HandleReplaceBarClick(x, y int) bool {
	if !m.replaceActive || m.replaceBar == nil || y >= m.replaceBar.Height()-1 {
		return false // last row = divider, ignore
	}
	action := m.replaceBar.HandleClick(x, y)
	m.dispatchReplaceAction(action)
	return true
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
	screenCol := max(x-gutterW, 0) + m.scrollLeftCol
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

	screenCol := max(x-gutterW, 0) + m.scrollLeftCol
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
	m.invalidateView()
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
	msgKindOpenEditor:       handleOpenEditor,
	msgKindCloseEditor:      handleCloseEditor,
	msgKindKeyMsg:           handleKeyMsg,
	msgKindTickMsg:          handleTickMsg,
	msgKindLSPDiagnostic:    handleLSPDiagnostic,
	msgKindLSPHover:         handleLSPHover,
	msgKindLSPDefinition:    handleLSPDefinition,
	msgKindStandaloneResult: handleStandaloneResult,
	msgKindLSPCompletion:    handleLSPCompletion,
	msgKindLSPDocHighlight:  handleLSPDocHighlight,
	msgKindLSPSignatureHelp: handleLSPSignatureHelp,
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
	case motion.OpNextTab:
		if sr.HasCount {
			idx := sr.Count - 1
			return m, func() tea.Msg { return msg.TabJumpMsg{Index: idx} }
		}
		return m, func() tea.Msg { return msg.TabNextMsg{} }
	case motion.OpPrevTab:
		return m, func() tea.Msg { return msg.TabPrevMsg{} }
	}
	return m, nil
}

func handleCloseEditor(m *Model, _ tea.Msg) (component.Component, tea.Cmd) {
	return m, nil
}

func handleTickMsg(m *Model, _ tea.Msg) (component.Component, tea.Cmd) {
	// Cursor blink is now driven by BlinkMsg, not the tick chain.
	return m, nil
}

// ViewDirty reports whether View() would produce new output.
func (m *Model) ViewDirty() bool { return !m.vc.valid || !m.vc.cursorOK }

func handleKeyMsg(m *Model, incoming tea.Msg) (component.Component, tea.Cmd) {
	key := incoming.(tea.KeyMsg)

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

	// Replace/find bar key interception — all keys go to the active bar.
	if m.replaceActive {
		return m.handleReplaceBarKey(key)
	}
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

// contentWidth returns the number of display columns available for text
// content (total width minus gutter). Derived from current line count.
func (m *Model) contentWidth() int {
	gutterCols := m.gutterWidth(m.lineIndex.Count()) + gutterPadding
	return max(m.width-gutterCols, 0)
}

// adjustScrollX ensures the cursor's display column falls within the
// visible content window [scrollLeftCol, scrollLeftCol + contentWidth).
func (m *Model) adjustScrollX() {
	cw := m.contentWidth()
	if cw <= 0 {
		return
	}
	line := m.buf.Line(m.state.CursorLine)
	_, colMap := expandTabs(line, editorTabWidth)
	dc := safeMapCol(colMap, m.state.CursorCol)
	if dc >= m.scrollLeftCol+cw {
		m.scrollLeftCol = dc - cw + 1
	}
	if dc < m.scrollLeftCol {
		m.scrollLeftCol = dc
	}
}

// renderLineCtx holds shared state for a renderVisibleLines pass so that
// renderOneLine can be called individually for cursor-only patching.
type renderLineCtx struct {
	// lineAt returns the content of line i. For full renders this indexes
	// into a pre-split slice; for cursor-only patching it uses
	// LineIndex + PieceTable.Substring to avoid splitting the entire buffer.
	lineAt       func(i int) string
	totalLines   int
	gutterWidth  int
	defaultStyle lipgloss.Style
	hasSelection bool
	selStartLine int
	selStartCol  int
	selEndLine   int
	selEndCol    int
	cursorVisible bool // Current blink phase (true = cursor shown).
}

// buildRenderCtx computes the shared rendering context once per pass.
// This splits the full buffer into lines — used for full viewport renders.
func (m *Model) buildRenderCtx() renderLineCtx {
	content := m.buf.Content()
	contentLines := strings.Split(content, "\n")
	totalLines := len(contentLines)
	gutterWidth := m.gutterWidth(totalLines)
	defaultStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Foreground)

	var selStartLine, selStartCol, selEndLine, selEndCol int
	hasSelection, selStart, selEnd := m.selectionRange()
	if hasSelection {
		selStartLine, selStartCol = m.lineIndex.PosToLineCol(selStart)
		selEndLine, selEndCol = m.lineIndex.PosToLineCol(selEnd)
	}

	return renderLineCtx{
		lineAt:       func(i int) string { return contentLines[i] },
		totalLines:   totalLines,
		gutterWidth:  gutterWidth,
		defaultStyle: defaultStyle,
		hasSelection: hasSelection,
		selStartLine: selStartLine,
		selStartCol:  selStartCol,
		selEndLine:   selEndLine,
		selEndCol:    selEndCol,
	}
}

// buildPatchCtx computes a minimal rendering context for cursor-only patching.
// Uses LineIndex + PieceTable.Substring instead of splitting the full buffer.
func (m *Model) buildPatchCtx() renderLineCtx {
	totalLines := m.lineIndex.Count()
	gutterWidth := m.gutterWidth(totalLines)
	defaultStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Foreground)

	var selStartLine, selStartCol, selEndLine, selEndCol int
	hasSelection, selStart, selEnd := m.selectionRange()
	if hasSelection {
		selStartLine, selStartCol = m.lineIndex.PosToLineCol(selStart)
		selEndLine, selEndCol = m.lineIndex.PosToLineCol(selEnd)
	}

	return renderLineCtx{
		lineAt: func(i int) string {
			info, ok := m.lineIndex.Get(i)
			if !ok {
				return ""
			}
			return m.buf.Substring(info.StartPos, info.StartPos+info.Length)
		},
		totalLines:   totalLines,
		gutterWidth:  gutterWidth,
		defaultStyle: defaultStyle,
		hasSelection: hasSelection,
		selStartLine: selStartLine,
		selStartCol:  selStartCol,
		selEndLine:   selEndLine,
		selEndCol:    selEndCol,
	}
}

// renderOneLine renders a single source line into a styled string.
// Extracted from renderVisibleLines so it can be called individually for
// cursor-only patching.
func (m *Model) renderOneLine(ctx *renderLineCtx, i int) string {
	var regions []highlight.HighlightRegion
	if i < len(m.regions) {
		regions = m.regions[i]
	}

	// Pre-expand tabs and remap all columns to display space.
	rawLine := ctx.lineAt(i)
	displayLine, colMap := expandTabs(rawLine, editorTabWidth)
	displayRegions := remapRegions(regions, colMap)
	displayLineRunes := len([]rune(displayLine))
	inSelection := ctx.hasSelection && i >= ctx.selStartLine && i <= ctx.selEndLine
	findSpans := m.findMatchSpans(i, len([]rune(rawLine)), colMap)
	hlSpans := m.highlightSpansForLine(i, colMap)
	jumpSpans := m.jumpMarkerSpansForLine(i, colMap)

	// Compute diagnostic underline ranges for this line.
	ulRanges := m.diagnosticDisplayRanges(i, colMap)

	// Horizontal scroll: clip display data to visible window.
	vs := m.scrollLeftCol
	if vs > 0 {
		cw := m.contentWidth()
		dr := []rune(displayLine)
		if vs < len(dr) {
			end := min(vs+cw, len(dr))
			displayLine = string(dr[vs:end])
		} else {
			displayLine = ""
		}
		displayLineRunes = len([]rune(displayLine))
		displayRegions = shiftClipRegions(displayRegions, vs, cw)
		findSpans = shiftClipSpans(findSpans, vs, cw)
		hlSpans = shiftClipSpans(hlSpans, vs, cw)
		jumpSpans = shiftClipSpans(jumpSpans, vs, cw)
		ulRanges = shiftClipUnderlines(ulRanges, vs, cw)
	}

	// Choose line rendering strategy; underlines apply to all paths.
	switch {
	case inSelection:
		sc := 0
		if i == ctx.selStartLine {
			sc = max(safeMapCol(colMap, ctx.selStartCol)-vs, 0)
		}
		ec := displayLineRunes
		if i == ctx.selEndLine {
			ec = safeMapCol(colMap, ctx.selEndCol+1) - vs
		}
		if len(findSpans) > 0 {
			return m.renderSelectedLineWithMatches(
				i, displayLine, displayRegions, ctx.gutterWidth, ctx.defaultStyle, sc, ec, findSpans, ulRanges)
		}
		return m.renderSelectedLine(
			i, displayLine, displayRegions, ctx.gutterWidth, ctx.defaultStyle, sc, ec, ulRanges)
	case len(findSpans) > 0:
		curCol := -1
		if !m.overlayActive() && i == m.state.CursorLine && m.focused && ctx.cursorVisible {
			curCol = safeMapCol(colMap, m.state.CursorCol) - vs
		}
		return m.renderFindMatchLine(
			i, displayLine, displayRegions, ctx.gutterWidth, ctx.defaultStyle, findSpans, curCol, ulRanges)
	case len(jumpSpans) > 0:
		curCol := -1
		if !m.overlayActive() && i == m.state.CursorLine && m.focused && ctx.cursorVisible {
			curCol = safeMapCol(colMap, m.state.CursorCol) - vs
		}
		return m.renderHighlightLine(
			i, displayLine, displayRegions, ctx.gutterWidth, ctx.defaultStyle, jumpSpans, curCol, ulRanges)
	case !m.overlayActive() && i == m.state.CursorLine && m.focused:
		displayCol := safeMapCol(colMap, m.state.CursorCol) - vs
		curCol := -1
		if ctx.cursorVisible {
			curCol = displayCol
		}
		if len(hlSpans) > 0 {
			return m.renderHighlightLine(
				i, displayLine, displayRegions, ctx.gutterWidth, ctx.defaultStyle, hlSpans, curCol, ulRanges)
		}
		if wInfo, isWarp := m.warpLines[i]; isWarp {
			ws := max(safeMapCol(colMap, wInfo.StartCol)-vs, 0)
			we := min(safeMapCol(colMap, wInfo.EndCol)-vs, displayLineRunes)
			warpSpan := []colSpan{{start: ws, end: we}}
			return m.renderWarpLine(
				i, displayLine, displayRegions, ctx.gutterWidth, ctx.defaultStyle, warpSpan, curCol, ulRanges)
		}
		if curCol >= 0 {
			return m.applyCursor(
				displayLine, displayRegions, ctx.gutterWidth, ctx.defaultStyle, displayCol, ulRanges)
		}
		gutter := m.renderGutter(i, ctx.gutterWidth)
		lineText := highlight.RenderLineWithUnderlines(
			displayLine, displayRegions, m.theme.Syntax, ctx.defaultStyle, ulRanges)
		return gutter + lineText
	case len(hlSpans) > 0:
		return m.renderHighlightLine(
			i, displayLine, displayRegions, ctx.gutterWidth, ctx.defaultStyle, hlSpans, -1, ulRanges)
	default:
		if wInfo, isWarp := m.warpLines[i]; isWarp {
			ws := max(safeMapCol(colMap, wInfo.StartCol)-vs, 0)
			we := min(safeMapCol(colMap, wInfo.EndCol)-vs, displayLineRunes)
			warpSpan := []colSpan{{start: ws, end: we}}
			return m.renderWarpLine(
				i, displayLine, displayRegions, ctx.gutterWidth, ctx.defaultStyle, warpSpan, -1, ulRanges)
		}
		gutter := m.renderGutter(i, ctx.gutterWidth)
		lineText := highlight.RenderLineWithUnderlines(
			displayLine, displayRegions, m.theme.Syntax, ctx.defaultStyle, ulRanges)
		return gutter + lineText
	}
}

func (m *Model) renderVisibleLines(viewHeight int, cursorVisible bool) []string {
	ctx := m.buildRenderCtx()
	ctx.cursorVisible = cursorVisible

	// Track rendered lines (code + virtual text) against viewHeight.
	result := make([]string, 0, viewHeight)
	for i := m.scrollOffset; i < ctx.totalLines && len(result) < viewHeight; i++ {
		result = append(result, m.renderOneLine(&ctx, i))

		// Append diagnostic virtual text line below if space remains.
		if diag, ok := m.diagnosticForLine(i); ok && len(result) < viewHeight {
			result = append(result, m.renderDiagnosticVirtualLine(diag, ctx.gutterWidth, m.width))
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

// renderSelectedLineWithMatches renders a line that is both within the visual
// selection AND contains find-match spans. Non-match parts of the selection
// use the selection style; match spans use the match/current-match style.
func (m *Model) renderSelectedLineWithMatches(lineNum int, line string, regions []highlight.HighlightRegion, gutterWidth int, defaultStyle lipgloss.Style, selStart, selEnd int, spans []colSpan, ulRanges []highlight.UnderlineRange) string {
	gutter := m.renderGutter(lineNum, gutterWidth)
	runes := []rune(line)
	n := len(runes)

	selStyle := lipgloss.NewStyle().Reverse(true)
	matchBg := lipgloss.NewStyle().
		Background(m.theme.Palette.Warning).
		Foreground(m.theme.Palette.Background)
	currentBg := lipgloss.NewStyle().
		Background(m.theme.Palette.Primary).
		Foreground(m.theme.Palette.Background)

	selStart = max(selStart, 0)
	selEnd = min(selEnd, n)

	var b strings.Builder
	pos := 0

	// Before selection: normal syntax.
	if selStart > 0 {
		seg := string(runes[:selStart])
		b.WriteString(highlight.RenderLineWithUnderlines(
			seg, filterRegions(regions, 0, selStart), m.theme.Syntax, defaultStyle,
			filterUnderlines(ulRanges, 0, selStart)))
		pos = selStart
	}

	// Inside selection: alternate between selection style and match style.
	for _, span := range spans {
		spanStart := max(span.start, selStart)
		spanEnd := min(span.end, selEnd)
		if spanStart >= spanEnd {
			continue
		}
		// Gap before this match (still in selection): selection style.
		if spanStart > pos {
			gapEnd := min(spanStart, selEnd)
			if gapEnd > pos {
				b.WriteString(selStyle.Render(string(runes[pos:gapEnd])))
				pos = gapEnd
			}
		}
		// The match span itself: match highlight style.
		style := matchBg
		if span.isCurrent {
			style = currentBg
		}
		b.WriteString(style.Render(string(runes[spanStart:spanEnd])))
		pos = spanEnd
	}

	// Remaining selected text after last match.
	if pos < selEnd {
		b.WriteString(selStyle.Render(string(runes[pos:selEnd])))
		pos = selEnd
	}

	// After selection: normal syntax.
	if pos < n {
		seg := string(runes[pos:])
		b.WriteString(highlight.RenderLineWithUnderlines(
			seg, filterRegions(regions, pos, n), m.theme.Syntax, defaultStyle,
			filterUnderlines(ulRanges, pos, n)))
	}

	return gutter + b.String()
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

// renderWarpLine renders a line with a subtle purple warp background, using
// the same structure as renderHighlightLine but with the WarpBg palette color.
func (m *Model) renderWarpLine(lineNum int, line string, regions []highlight.HighlightRegion, gutterWidth int, defaultStyle lipgloss.Style, spans []colSpan, cursorCol int, ulRanges []highlight.UnderlineRange) string {
	gutter := m.renderGutter(lineNum, gutterWidth)
	runes := []rune(line)
	n := len(runes)

	bgRanges := make([]highlight.UnderlineRange, len(spans))
	for i, s := range spans {
		bgRanges[i] = highlight.UnderlineRange{StartCol: s.start, EndCol: min(s.end, n)}
	}

	if cursorCol < 0 {
		return gutter + highlight.RenderLineWithBgRanges(
			line, regions, m.theme.Syntax, defaultStyle, bgRanges, m.theme.Palette.WarpBg)
	}

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
		filterUnderlines(bgRanges, 0, beforeEnd), m.theme.Palette.WarpBg)
	afterStyled := highlight.RenderLineWithBgRanges(
		after, filterRegions(regions, afterStart, n), m.theme.Syntax, defaultStyle,
		filterUnderlines(bgRanges, afterStart, n), m.theme.Palette.WarpBg)

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

	// Warp point: purple dot in the line number column.
	if _, ok := m.warpLines[lineNum]; ok {
		warpStyle := lipgloss.NewStyle().Foreground(m.theme.Palette.Secondary).Bold(true)
		numStr := fmt.Sprintf("%*s", gutterWidth-gutterPadding, "•")
		return warpStyle.Render(numStr) + strings.Repeat(" ", gutterPadding)
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
// the given line. Returns nil when no search bar is active or there are no
// overlapping matches. Works with both the find bar and replace bar.
func (m *Model) findMatchSpans(lineNum int, displayLen int, colMap []int) []colSpan {
	matches, currentIdx := m.activeSearchMatches()
	if matches == nil {
		return nil
	}
	lineInfo, ok := m.lineIndex.Get(lineNum)
	if !ok {
		return nil
	}
	lineStart := lineInfo.StartPos
	lineEnd := lineStart + displayLen
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

// shiftClipRegions shifts highlight regions left by shift display columns and
// clips them to [0, width). Regions that fall entirely outside are dropped.
func shiftClipRegions(regions []highlight.HighlightRegion, shift, width int) []highlight.HighlightRegion {
	out := make([]highlight.HighlightRegion, 0, len(regions))
	for _, r := range regions {
		s := max(r.StartCol-shift, 0)
		e := min(r.EndCol-shift, width)
		if s < e {
			out = append(out, highlight.HighlightRegion{
				StartCol: s,
				EndCol:   e,
				Category: r.Category,
			})
		}
	}
	return out
}

// shiftClipSpans shifts colSpans left by shift display columns and clips
// them to [0, width). Spans that fall entirely outside are dropped.
func shiftClipSpans(spans []colSpan, shift, width int) []colSpan {
	out := make([]colSpan, 0, len(spans))
	for _, sp := range spans {
		s := max(sp.start-shift, 0)
		e := min(sp.end-shift, width)
		if s < e {
			out = append(out, colSpan{start: s, end: e, isCurrent: sp.isCurrent})
		}
	}
	return out
}

// shiftClipUnderlines shifts underline ranges left by shift display columns
// and clips them to [0, width). Ranges that fall entirely outside are dropped.
func shiftClipUnderlines(ranges []highlight.UnderlineRange, shift, width int) []highlight.UnderlineRange {
	out := make([]highlight.UnderlineRange, 0, len(ranges))
	for _, r := range ranges {
		s := max(r.StartCol-shift, 0)
		e := min(r.EndCol-shift, width)
		if s < e {
			out = append(out, highlight.UnderlineRange{StartCol: s, EndCol: e})
		}
	}
	return out
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
	if wInfo, ok := m.warpLines[m.state.CursorLine]; ok {
		m.statusLine.SetWarpSlot(wInfo.Slot)
	} else {
		m.statusLine.SetWarpSlot(0)
	}
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
		s, e, ok := m.selectionBounds()
		if ok {
			searchStart = s
			searchEnd = min(e+1, len(content))
		}
	}
	m.findBar.Recompute(content, searchStart, searchEnd)
}

// selectionBounds returns the inclusive rune range of the current selection.
// It prefers the live visual mode state; falls back to the find/replace bar's
// stored snapshot.
func (m *Model) selectionBounds() (start, end int, ok bool) {
	if m.visualMode != nil {
		s, e := m.visualMode.State.CharRange()
		return s, e, true
	}
	if m.findActive && m.findBar != nil && m.findBar.HasSelectionRange() {
		s, e := m.findBar.SelectionRange()
		return s, e, true
	}
	if m.replaceActive && m.replaceBar != nil && m.replaceBar.HasSelectionRange() {
		s, e := m.replaceBar.SelectionRange()
		return s, e, true
	}
	return 0, 0, false
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
// Replace bar helpers
// ---------------------------------------------------------------------------

// activeSearchMatches returns the match list and current index from whichever
// search bar (find or replace) is currently active. Returns nil when neither
// is active.
func (m *Model) activeSearchMatches() ([]search.MatchRange, int) {
	if m.replaceActive && m.replaceBar != nil {
		return m.replaceBar.Matches(), m.replaceBar.MatchIndex()
	}
	if m.findActive && m.findBar != nil {
		return m.findBar.Matches(), m.findBar.MatchIndex()
	}
	return nil, 0
}

func (m *Model) handleReplaceBarKey(key tea.KeyMsg) (component.Component, tea.Cmd) {
	if m.replaceBar == nil {
		return m, nil
	}
	action := m.replaceBar.HandleKey(key)
	m.dispatchReplaceAction(action)
	return m, nil
}

// dispatchReplaceAction processes a replacebar.Action.
func (m *Model) dispatchReplaceAction(action replacebar.Action) {
	switch action {
	case replacebar.ActionClose:
		m.CloseReplaceBar()
	case replacebar.ActionNextMatch:
		if m.replaceBar == nil {
			return
		}
		m.replaceBar.AdvanceMatch()
		m.jumpToCurrentReplaceMatch()
	case replacebar.ActionPrevMatch:
		if m.replaceBar == nil {
			return
		}
		m.replaceBar.RetreatMatch()
		m.jumpToCurrentReplaceMatch()
	case replacebar.ActionQueryChanged:
		if m.replaceBar == nil {
			return
		}
		m.recomputeReplace()
		m.replaceBar.NearestMatch(m.state.Cursor)
		m.jumpToCurrentReplaceMatch()
	case replacebar.ActionReplaceOne:
		m.replaceCurrentMatch()
	case replacebar.ActionReplaceAll:
		m.replaceAllMatches()
	}
}

func (m *Model) recomputeReplace() {
	if m.replaceBar == nil {
		return
	}
	content := []rune(m.buf.Content())
	searchStart, searchEnd := 0, len(content)
	if m.replaceBar.FindInSelection() {
		s, e, ok := m.selectionBounds()
		if ok {
			searchStart = s
			searchEnd = min(e+1, len(content))
		}
	}
	m.replaceBar.Recompute(content, searchStart, searchEnd)
}

func (m *Model) jumpToCurrentReplaceMatch() {
	if m.replaceBar == nil {
		return
	}
	match, ok := m.replaceBar.CurrentMatch()
	if !ok {
		return
	}
	m.state.Cursor = match.Start
	m.state.SyncCursorPos()
	m.syncStatusLine()
}

// replaceCurrentMatch replaces the current search match with the replacement
// text and advances to the next match. Supports regex backreferences.
func (m *Model) replaceCurrentMatch() {
	if m.replaceBar == nil {
		return
	}
	match, ok := m.replaceBar.CurrentMatch()
	if !ok {
		return
	}
	old := m.buf.Substring(match.Start, match.End)
	replacement := m.computeReplacement(old)

	m.undoTree.BeginGroup()
	m.buf.Delete(match.Start, match.End-match.Start)
	m.undoTree.Record(buffer.EditOp{Type: buffer.EditDelete, Pos: match.Start, OldText: old})
	m.buf.Insert(match.Start, replacement)
	m.undoTree.Record(buffer.EditOp{Type: buffer.EditInsert, Pos: match.Start, Text: replacement})
	m.undoTree.EndGroup()

	m.lineIndex.Rebuild(m.buf)
	m.markModified()
	m.recomputeReplace()
	m.replaceBar.NearestMatch(match.Start + len([]rune(replacement)))
	m.jumpToCurrentReplaceMatch()
}

// replaceAllMatches replaces all search matches in reverse order so that
// earlier positions remain valid. All replacements share a single undo group.
func (m *Model) replaceAllMatches() {
	if m.replaceBar == nil {
		return
	}
	matches := m.replaceBar.Matches()
	if len(matches) == 0 {
		return
	}
	m.undoTree.BeginGroup()
	for i := len(matches) - 1; i >= 0; i-- {
		match := matches[i]
		old := m.buf.Substring(match.Start, match.End)
		replacement := m.computeReplacement(old)
		m.buf.Delete(match.Start, match.End-match.Start)
		m.undoTree.Record(buffer.EditOp{Type: buffer.EditDelete, Pos: match.Start, OldText: old})
		m.buf.Insert(match.Start, replacement)
		m.undoTree.Record(buffer.EditOp{Type: buffer.EditInsert, Pos: match.Start, Text: replacement})
	}
	m.undoTree.EndGroup()

	m.lineIndex.Rebuild(m.buf)
	m.markModified()
	m.recomputeReplace()
}

// computeReplacement applies the compiled regex to the matched text to resolve
// backreferences ($1, $2, etc.) in the replacement string.
func (m *Model) computeReplacement(matchedText string) string {
	compiled := m.replaceBar.Compiled()
	if compiled == nil {
		return m.replaceBar.Replacement()
	}
	return compiled.ReplaceAllString(matchedText, m.replaceBar.Replacement())
}

// invalidateView marks the view cache as stale so the next ViewContent()
// performs a full re-render of all visible lines.
func (m *Model) invalidateView() {
	m.vc.valid = false
	m.vc.postValid = false
	m.vc.cursorWidthOK = false
}

// markModified updates the editor's modified/lspDirty/editGeneration state
// after a buffer mutation.
func (m *Model) markModified() {
	m.editGeneration++
	m.modified = true
	m.lspDirty = true
	m.lastEditAt = time.Now()
	m.rehighlight()
	m.syncStatusLine()
	m.invalidateView()
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

// fitLineCached is like fitLine but reuses the cached visual width for
// the cursor line. Cursor blink only changes ANSI codes (reverse video),
// not character content, so lipgloss.Width returns the same value across
// blink states. Falls back to fitLine on cache miss.
func (m *Model) fitLineCached(line string) string {
	if m.width <= 0 {
		return line
	}
	var visWidth int
	if m.vc.cursorWidthOK {
		visWidth = m.vc.cursorVisWidth
	} else {
		visWidth = lipgloss.Width(line)
		m.vc.cursorVisWidth = visWidth
		m.vc.cursorWidthOK = true
	}
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
