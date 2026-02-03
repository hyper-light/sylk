package mode

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/adalundhe/sylk/ui/editor/buffer"
	"github.com/adalundhe/sylk/ui/editor/motion"
	"github.com/adalundhe/sylk/ui/theme"
)

// VisualSubMode distinguishes char, line, and block selection.
type VisualSubMode int

const (
	VisualChar  VisualSubMode = iota
	VisualLine
	VisualBlock
)

// visualSubModeToMode maps sub-modes to their editor Mode constants.
var visualSubModeToMode = map[VisualSubMode]Mode{
	VisualChar:  ModeVisual,
	VisualLine:  ModeVisualLine,
	VisualBlock: ModeVisualBlock,
}

// VisualState tracks the active visual selection.
type VisualState struct {
	AnchorPos int
	CursorPos int
	SubMode   VisualSubMode
}

// CharRange returns the inclusive character range for visual-char mode.
func (vs *VisualState) CharRange() (int, int) {
	return normalizeRange(vs.AnchorPos, vs.CursorPos)
}

// LineRange returns the inclusive line range for visual-line mode using
// the provided LineIndex.
func (vs *VisualState) LineRange(li *buffer.LineIndex) (int, int) {
	aLine, _ := li.PosToLineCol(vs.AnchorPos)
	cLine, _ := li.PosToLineCol(vs.CursorPos)
	return normalizeRange(aLine, cLine)
}

// BlockRange returns column-aligned ranges for visual-block mode using
// the provided LineIndex.
func (vs *VisualState) BlockRange(li *buffer.LineIndex) (startLine, endLine, startCol, endCol int) {
	aLine, aCol := li.PosToLineCol(vs.AnchorPos)
	cLine, cCol := li.PosToLineCol(vs.CursorPos)
	startLine, endLine = normalizeRange(aLine, cLine)
	startCol, endCol = normalizeRange(aCol, cCol)
	return startLine, endLine, startCol, endCol
}

// VisualMode handles keys when the editor is in a visual mode.
type VisualMode struct {
	State  VisualState
	parser *motion.Parser
	theme  *theme.Theme
}

// NewVisualMode creates a VisualMode anchored at the cursor position.
func NewVisualMode(th *theme.Theme, anchorPos int, sub VisualSubMode) *VisualMode {
	return &VisualMode{
		State: VisualState{
			AnchorPos: anchorPos,
			CursorPos: anchorPos,
			SubMode:   sub,
		},
		parser: motion.NewParser(),
		theme:  th,
	}
}

// visualKeyHandler processes a key in visual mode and returns the next mode.
type visualKeyHandler func(vm *VisualMode, state *EditorState) Mode

// visualKeyTable maps runes to their handlers within visual mode.
var visualKeyTable = map[rune]visualKeyHandler{
	'v':  visualToggleChar,
	'V':  visualToggleLine,
	'o':  visualSwapAnchor,
	'd':  visualDelete,
	'y':  visualYank,
	'c':  visualChange,
	'U':  visualUpperCase,
	'u':  visualLowerCase,
	'~':  visualToggleCase,
	'>':  visualIndent,
	'<':  visualDedent,
	'J':  visualJoinLines,
	'r':  visualReplaceChar,
	'p':  visualPut,
	'x':  visualDelete,
}

// visualNamedKeyTable maps named keys to their handlers.
var visualNamedKeyTable = map[tea.KeyType]visualKeyHandler{
	tea.KeyEsc:   visualEscape,
	tea.KeyCtrlC: visualEscape,
}

// HandleKey processes a key event in visual mode.
func (vm *VisualMode) HandleKey(key tea.KeyMsg, state *EditorState) (Mode, tea.Cmd) {
	// Named key dispatch.
	if handler, ok := visualNamedKeyTable[key.Type]; ok {
		next := handler(vm, state)
		return next, nil
	}
	// Ctrl+V toggles block mode.
	if key.String() == "ctrl+v" {
		next := visualToggleBlock(vm, state)
		return next, nil
	}
	// Single-rune dispatch.
	if len(key.Runes) != 1 {
		return vm.currentMode(), nil
	}
	r := key.Runes[0]
	if handler, ok := visualKeyTable[r]; ok {
		vm.parser.Reset()
		next := handler(vm, state)
		return next, nil
	}
	// Feed to motion parser for cursor movement.
	return vm.feedMotion(r, state), nil
}

// currentMode returns the editor Mode for the current visual sub-mode.
func (vm *VisualMode) currentMode() Mode {
	m, ok := visualSubModeToMode[vm.State.SubMode]
	if !ok {
		return ModeVisual
	}
	return m
}

// feedMotion processes a rune through the motion parser and moves the cursor.
func (vm *VisualMode) feedMotion(r rune, state *EditorState) Mode {
	result, done := vm.parser.Feed(r)
	if !done {
		return vm.currentMode()
	}
	mr := motion.ExecuteMotion(result.Motion, state.Buffer, state.LineIndex, state.Cursor, result.Count)
	state.Cursor = mr.End
	state.ClampCursor(0)
	vm.State.CursorPos = state.Cursor
	return vm.currentMode()
}

// ---------------------------------------------------------------------------
// Visual key handlers
// ---------------------------------------------------------------------------

func visualEscape(_ *VisualMode, state *EditorState) Mode {
	state.ClampCursor(1)
	return ModeNormal
}

func visualToggleChar(vm *VisualMode, state *EditorState) Mode {
	if vm.State.SubMode == VisualChar {
		state.ClampCursor(1)
		return ModeNormal
	}
	vm.State.SubMode = VisualChar
	return ModeVisual
}

func visualToggleLine(vm *VisualMode, state *EditorState) Mode {
	if vm.State.SubMode == VisualLine {
		state.ClampCursor(1)
		return ModeNormal
	}
	vm.State.SubMode = VisualLine
	return ModeVisualLine
}

func visualToggleBlock(vm *VisualMode, state *EditorState) Mode {
	if vm.State.SubMode == VisualBlock {
		state.ClampCursor(1)
		return ModeNormal
	}
	vm.State.SubMode = VisualBlock
	return ModeVisualBlock
}

func visualSwapAnchor(vm *VisualMode, state *EditorState) Mode {
	vm.State.AnchorPos, vm.State.CursorPos = vm.State.CursorPos, vm.State.AnchorPos
	state.Cursor = vm.State.CursorPos
	state.SyncCursorPos()
	return vm.currentMode()
}

func visualDelete(vm *VisualMode, state *EditorState) Mode {
	start, end := vm.State.CharRange()
	length := end - start + 1
	old := substringFromBuffer(state.Buffer, start, end+1)
	state.Buffer.Delete(start, length)
	state.UndoTree.Record(buffer.EditOp{
		Type:    buffer.EditDelete,
		Pos:     start,
		OldText: old,
	})
	state.LineIndex.Rebuild(state.Buffer)
	state.Cursor = start
	state.ClampCursor(1)
	return ModeNormal
}

func visualYank(vm *VisualMode, state *EditorState) Mode {
	state.ClampCursor(1)
	return ModeNormal
}

func visualChange(vm *VisualMode, state *EditorState) Mode {
	start, end := vm.State.CharRange()
	length := end - start + 1
	old := substringFromBuffer(state.Buffer, start, end+1)
	state.Buffer.Delete(start, length)
	state.UndoTree.Record(buffer.EditOp{
		Type:    buffer.EditDelete,
		Pos:     start,
		OldText: old,
	})
	state.LineIndex.Rebuild(state.Buffer)
	state.Cursor = start
	state.SyncCursorPos()
	return ModeInsert
}

func visualUpperCase(vm *VisualMode, state *EditorState) Mode {
	applyVisualCaseTransform(vm, state, runeToUpper)
	return ModeNormal
}

func visualLowerCase(vm *VisualMode, state *EditorState) Mode {
	applyVisualCaseTransform(vm, state, runeToLower)
	return ModeNormal
}

func visualToggleCase(vm *VisualMode, state *EditorState) Mode {
	applyVisualCaseTransform(vm, state, runeToggleCase)
	return ModeNormal
}

func visualIndent(_ *VisualMode, state *EditorState) Mode {
	state.ClampCursor(1)
	return ModeNormal
}

func visualDedent(_ *VisualMode, state *EditorState) Mode {
	state.ClampCursor(1)
	return ModeNormal
}

func visualJoinLines(_ *VisualMode, state *EditorState) Mode {
	state.ClampCursor(1)
	return ModeNormal
}

func visualReplaceChar(_ *VisualMode, state *EditorState) Mode {
	state.ClampCursor(1)
	return ModeNormal
}

func visualPut(_ *VisualMode, state *EditorState) Mode {
	state.ClampCursor(1)
	return ModeNormal
}

// ---------------------------------------------------------------------------
// Case transform helpers
// ---------------------------------------------------------------------------

type runeTransform func(r rune) rune

func runeToUpper(r rune) rune {
	if r >= 'a' && r <= 'z' {
		return r - ('a' - 'A')
	}
	return r
}

func runeToLower(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return r
}

func runeToggleCase(r rune) rune {
	if r >= 'a' && r <= 'z' {
		return runeToUpper(r)
	}
	return runeToLower(r)
}

func applyVisualCaseTransform(vm *VisualMode, state *EditorState, transform runeTransform) {
	start, end := vm.State.CharRange()
	length := end - start + 1
	original := make([]rune, length)
	for i := range length {
		original[i] = state.Buffer.RuneAt(start + i)
	}
	transformed := make([]rune, length)
	for i, r := range original {
		transformed[i] = transform(r)
	}
	old := string(original)
	state.Buffer.Delete(start, length)
	state.Buffer.Insert(start, string(transformed))
	state.UndoTree.Record(buffer.EditOp{
		Type:    buffer.EditDelete,
		Pos:     start,
		OldText: old,
	})
	state.UndoTree.Record(buffer.EditOp{
		Type: buffer.EditInsert,
		Pos:  start,
		Text: string(transformed),
	})
	state.LineIndex.Rebuild(state.Buffer)
	state.Cursor = start
	state.ClampCursor(1)
}
