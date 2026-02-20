package mode

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/adalundhe/sylk/ui/editor/buffer"
	"github.com/adalundhe/sylk/ui/editor/motion"
	"github.com/adalundhe/sylk/ui/theme"
)

// modeEntry describes a direct mode transition triggered by a single key.
type modeEntry struct {
	target       Mode
	cursorAdjust func(state *EditorState)
}

// modeEntryTable maps runes to immediate mode transitions from normal mode.
var modeEntryTable = map[rune]modeEntry{
	'i': {target: ModeInsert, cursorAdjust: nil},
	'I': {target: ModeInsert, cursorAdjust: cursorToFirstNonBlank},
	'a': {target: ModeInsert, cursorAdjust: cursorAfter},
	'A': {target: ModeInsert, cursorAdjust: cursorToLineEnd},
	'o': {target: ModeInsert, cursorAdjust: insertLineBelow},
	'O': {target: ModeInsert, cursorAdjust: insertLineAbove},
	'v': {target: ModeVisual, cursorAdjust: nil},
	'V': {target: ModeVisualLine, cursorAdjust: nil},
}

// NormalMode handles keys when the editor is in normal mode.
type NormalMode struct {
	parser *motion.Parser
	theme  *theme.Theme
}

// NewNormalMode creates a NormalMode with the given theme.
func NewNormalMode(th *theme.Theme) *NormalMode {
	return &NormalMode{
		parser: motion.NewParser(),
		theme:  th,
	}
}

// HandleKey processes a key event in normal mode and returns the next mode.
func (nm *NormalMode) HandleKey(key tea.KeyMsg, state *EditorState) (Mode, tea.Cmd) {
	// Named-key dispatch.
	if cmd, next, handled := nm.handleNamedKey(key, state); handled {
		return next, cmd
	}
	// Single-rune dispatch: mode entries take priority.
	if len(key.Runes) == 1 {
		return nm.handleRune(key.Runes[0], state)
	}
	return ModeNormal, nil
}

// handleNamedKey processes non-rune keys (Esc, Ctrl combos, Enter, etc.).
func (nm *NormalMode) handleNamedKey(key tea.KeyMsg, state *EditorState) (tea.Cmd, Mode, bool) {
	handler, ok := namedKeyTable[key.Type]
	if !ok {
		return nil, ModeNormal, false
	}
	return handler(state), ModeNormal, true
}

// namedKeyTable maps special key types to their handlers.
var namedKeyTable = map[tea.KeyType]func(state *EditorState) tea.Cmd{
	tea.KeyCtrlR: func(state *EditorState) tea.Cmd {
		applyRedo(state)
		return nil
	},
	tea.KeyUp: func(state *EditorState) tea.Cmd {
		mr := motion.ExecuteMotion(motion.MotionUp, state.Buffer, state.LineIndex, state.Cursor, 1)
		state.Cursor = mr.End
		state.ClampCursor(1)
		return nil
	},
	tea.KeyDown: func(state *EditorState) tea.Cmd {
		mr := motion.ExecuteMotion(motion.MotionDown, state.Buffer, state.LineIndex, state.Cursor, 1)
		state.Cursor = mr.End
		state.ClampCursor(1)
		return nil
	},
	tea.KeyLeft: func(state *EditorState) tea.Cmd {
		// Arrow keys cross line boundaries (standard editor behaviour);
		// vim h/l motions stay within the line.
		state.Cursor = max(state.Cursor-1, 0)
		state.ClampCursor(1)
		return nil
	},
	tea.KeyRight: func(state *EditorState) tea.Cmd {
		state.Cursor = min(state.Cursor+1, state.Buffer.Length())
		state.ClampCursor(1)
		return nil
	},
}

// handleRune dispatches a single rune in normal mode.
func (nm *NormalMode) handleRune(r rune, state *EditorState) (Mode, tea.Cmd) {
	// Direct mode entry.
	if entry, ok := modeEntryTable[r]; ok {
		nm.parser.Reset()
		if entry.cursorAdjust != nil {
			entry.cursorAdjust(state)
		}
		return entry.target, nil
	}
	// Command-line entry.
	if r == ':' {
		nm.parser.Reset()
		return ModeCmdline, nil
	}
	// Undo.
	if r == 'u' {
		nm.parser.Reset()
		applyUndo(state)
		return ModeNormal, nil
	}
	// Feed into motion parser.
	return nm.feedParser(r, state)
}

// feedParser feeds the rune to the grammar parser and executes the result.
func (nm *NormalMode) feedParser(r rune, state *EditorState) (Mode, tea.Cmd) {
	result, done := nm.parser.Feed(r)
	if !done {
		return ModeNormal, nil
	}
	// Standalone operators (gd) are signalled via StandaloneResult
	// so the caller can produce the appropriate Cmd.
	if motion.IsStandalone(result.Operator) {
		return ModeNormal, standaloneCmd(result, state)
	}
	executeNormalCommand(result, state)
	return ModeNormal, nil
}

// standaloneCmd builds a tea.Cmd from a standalone parse result by
// storing the operator and cursor state in a StandaloneResult message.
func standaloneCmd(result *motion.ParseResult, state *EditorState) tea.Cmd {
	op := result.Operator
	line := state.CursorLine
	col := state.CursorCol
	count := result.Count
	hasCount := result.HasCount
	return func() tea.Msg {
		return StandaloneResult{
			Operator: op,
			Line:     line,
			Col:      col,
			Count:    count,
			HasCount: hasCount,
		}
	}
}

// StandaloneResult is returned as a tea.Msg when a standalone operator
// (like gd) is parsed. The editor model maps it to the appropriate
// LSP request message.
type StandaloneResult struct {
	Operator motion.OperatorType
	Line     int
	Col      int
	Count    int  // count prefix (product of count1*count2, minimum 1)
	HasCount bool // true when the user explicitly typed a count prefix
}

// executeNormalCommand applies a parsed motion/operator to the editor state.
func executeNormalCommand(result *motion.ParseResult, state *EditorState) {
	if result.Operator == motion.OpNone {
		executeMotionOnly(result, state)
		return
	}
	executeOperatorMotion(result, state)
}

// executeMotionOnly moves the cursor without an operator.
func executeMotionOnly(result *motion.ParseResult, state *EditorState) {
	mr := motion.ExecuteMotion(result.Motion, state.Buffer, state.LineIndex, state.Cursor, result.Count)
	state.Cursor = mr.End
	state.ClampCursor(1)
}

// executeOperatorMotion applies an operator over a motion range.
func executeOperatorMotion(result *motion.ParseResult, state *EditorState) {
	mr := motion.ExecuteMotion(result.Motion, state.Buffer, state.LineIndex, state.Cursor, result.Count)
	start, end := normalizeRange(mr.Start, mr.End)
	handler, ok := operatorHandlerTable[result.Operator]
	if !ok {
		return
	}
	handler(state, start, end)
}

// operatorHandlerTable maps operator types to their execution functions.
var operatorHandlerTable = map[motion.OperatorType]func(state *EditorState, start, end int){
	motion.OpDelete: opDelete,
	motion.OpChange: opChange,
}

func opDelete(state *EditorState, start, end int) {
	length := end - start + 1
	old := state.Buffer.Substring(start, end+1)
	state.Buffer.Delete(start, length)
	state.UndoTree.Record(buffer.EditOp{
		Type:    buffer.EditDelete,
		Pos:     start,
		OldText: old,
	})
	updateLineIndex(state)
	state.Cursor = start
	state.ClampCursor(1)
}

func opChange(state *EditorState, start, end int) {
	opDelete(state, start, end)
	// opChange transitions to insert mode; the caller reads the returned mode.
}

// ---------------------------------------------------------------------------
// Cursor adjustment helpers
// ---------------------------------------------------------------------------

func cursorAfter(state *EditorState) {
	state.Cursor = min(state.Cursor+1, state.Buffer.Length())
}

func cursorToLineEnd(state *EditorState) {
	info, ok := state.LineIndex.Get(state.CursorLine)
	if !ok {
		return
	}
	state.Cursor = info.StartPos + info.Length
}

func cursorToFirstNonBlank(state *EditorState) {
	mr := motion.ExecuteMotion(motion.MotionFirstNonBlank, state.Buffer, state.LineIndex, state.Cursor, 1)
	state.Cursor = mr.End
}

func insertLineBelow(state *EditorState) {
	info, ok := state.LineIndex.Get(state.CursorLine)
	if !ok {
		return
	}
	pos := info.StartPos + info.Length
	state.Buffer.Insert(pos, "\n")
	state.UndoTree.Record(buffer.EditOp{
		Type: buffer.EditInsert,
		Pos:  pos,
		Text: "\n",
	})
	updateLineIndex(state)
	state.Cursor = pos + 1
	state.SyncCursorPos()
}

func insertLineAbove(state *EditorState) {
	info, ok := state.LineIndex.Get(state.CursorLine)
	if !ok {
		return
	}
	pos := info.StartPos
	state.Buffer.Insert(pos, "\n")
	state.UndoTree.Record(buffer.EditOp{
		Type: buffer.EditInsert,
		Pos:  pos,
		Text: "\n",
	})
	updateLineIndex(state)
	state.Cursor = pos
	state.SyncCursorPos()
}

// ---------------------------------------------------------------------------
// Undo / redo helpers
// ---------------------------------------------------------------------------

func applyUndo(state *EditorState) {
	edits, cursorSnap, ok := state.UndoTree.Undo()
	if !ok {
		return
	}
	for _, edit := range edits {
		reverseEdit(state, edit)
	}
	state.LineIndex.Rebuild(state.Buffer)
	if cursorSnap != nil && state.Cursors != nil && len(cursorSnap) > 1 {
		state.Cursors.Restore(cursorSnap, state.LineIndex)
		p := state.Cursors.Primary()
		state.Cursor = p.Pos
		state.CursorLine = p.Line
		state.CursorCol = p.Col
	} else {
		state.Cursor = edits[0].Pos
		state.ClampCursor(1)
	}
}

func applyRedo(state *EditorState) {
	edits, cursorSnap, ok := state.UndoTree.Redo()
	if !ok {
		return
	}
	for _, edit := range edits {
		reapplyEdit(state, edit)
	}
	state.LineIndex.Rebuild(state.Buffer)
	if cursorSnap != nil && state.Cursors != nil && len(cursorSnap) > 1 {
		state.Cursors.Restore(cursorSnap, state.LineIndex)
		p := state.Cursors.Primary()
		state.Cursor = p.Pos
		state.CursorLine = p.Line
		state.CursorCol = p.Col
	} else {
		state.Cursor = edits[len(edits)-1].Pos
		state.ClampCursor(1)
	}
}

func reverseEdit(state *EditorState, edit buffer.EditOp) {
	if edit.Type == buffer.EditInsert {
		state.Buffer.Delete(edit.Pos, len([]rune(edit.Text)))
	} else {
		state.Buffer.Insert(edit.Pos, edit.OldText)
	}
}

func reapplyEdit(state *EditorState, edit buffer.EditOp) {
	if edit.Type == buffer.EditInsert {
		state.Buffer.Insert(edit.Pos, edit.Text)
	} else {
		state.Buffer.Delete(edit.Pos, len([]rune(edit.OldText)))
	}
}

// ---------------------------------------------------------------------------
// Utility
// ---------------------------------------------------------------------------

func normalizeRange(a, b int) (int, int) {
	if a > b {
		return b, a
	}
	return a, b
}

