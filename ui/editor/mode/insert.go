package mode

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/adalundhe/sylk/ui/editor/buffer"
	"github.com/adalundhe/sylk/ui/editor/motion"
	"github.com/adalundhe/sylk/ui/theme"
)

// InsertMode handles keys when the editor is in insert mode.
type InsertMode struct {
	theme *theme.Theme
}

// NewInsertMode creates an InsertMode with the given theme.
func NewInsertMode(th *theme.Theme) *InsertMode {
	return &InsertMode{theme: th}
}

// insertKeyHandler processes a key and returns the resulting mode.
type insertKeyHandler func(state *EditorState) Mode

// insertKeyTable maps named key types to their handlers.
var insertKeyTable = map[tea.KeyType]insertKeyHandler{
	tea.KeyEsc:       insertEsc,
	tea.KeyBackspace: insertBackspace,
	tea.KeyEnter:     insertEnter,
	tea.KeyTab:       insertTab,
	tea.KeyUp:        insertArrowUp,
	tea.KeyDown:      insertArrowDown,
	tea.KeyLeft:      insertArrowLeft,
	tea.KeyRight:     insertArrowRight,
}

// bracketPairs maps opening brackets to their closing counterpart.
var bracketPairs = map[rune]rune{
	'(': ')',
	'{': '}',
	'[': ']',
}

// closingBrackets is the set of closing brackets eligible for skip-over.
var closingBrackets = map[rune]struct{}{
	')': {},
	'}': {},
	']': {},
}

// HandleKey processes a key event in insert mode.
func (im *InsertMode) HandleKey(key tea.KeyMsg, state *EditorState) (Mode, tea.Cmd) {
	// Named key dispatch.
	if handler, ok := insertKeyTable[key.Type]; ok {
		return handler(state), nil
	}
	// Rune insertion with bracket auto-close (disabled in strings/comments).
	if len(key.Runes) > 0 {
		r := key.Runes[0]
		if !state.InStringOrComment {
			if closer, ok := bracketPairs[r]; ok {
				insertBracketPair(state, r, closer)
				return ModeInsert, nil
			}
			if skipClosingBracket(state, r) {
				return ModeInsert, nil
			}
		}
		insertRunes(state, key.Runes)
		return ModeInsert, nil
	}
	return ModeInsert, nil
}

// ---------------------------------------------------------------------------
// Key handlers
// ---------------------------------------------------------------------------

func insertEsc(state *EditorState) Mode {
	// Move cursor left by one when leaving insert mode (vim behaviour).
	state.Cursor = max(state.Cursor-1, 0)
	state.ClampCursor(1)
	return ModeNormal
}

func insertBackspace(state *EditorState) Mode {
	if state.Cursor <= 0 {
		return ModeInsert
	}
	// Delete both characters when the cursor sits between a matching pair.
	if deleteEmptyPair(state) {
		return ModeInsert
	}
	pos := state.Cursor - 1
	old := string(state.Buffer.RuneAt(pos))
	state.Buffer.Delete(pos, 1)
	state.UndoTree.Record(buffer.EditOp{
		Type:    buffer.EditDelete,
		Pos:     pos,
		OldText: old,
	})
	state.LineIndex.Rebuild(state.Buffer)
	state.Cursor = pos
	state.SyncCursorPos()
	return ModeInsert
}

func insertEnter(state *EditorState) Mode {
	indent := currentLineIndent(state)
	insertText(state, "\n"+indent)
	return ModeInsert
}

// currentLineIndent returns the leading whitespace to replicate on a new line.
// When the cursor is within the existing indentation, no extra indent is added
// because the remaining whitespace after the split naturally becomes the new
// line's leading whitespace. When the cursor is at or past the indentation
// boundary, the full indent is returned.
func currentLineIndent(state *EditorState) string {
	info, ok := state.LineIndex.Get(state.CursorLine)
	if !ok {
		return ""
	}
	indentLen := 0
	for i := range info.Length {
		r := state.Buffer.RuneAt(info.StartPos + i)
		if r != ' ' && r != '\t' {
			break
		}
		indentLen++
	}
	if state.CursorCol < indentLen {
		return ""
	}
	buf := make([]rune, indentLen)
	for i := range indentLen {
		buf[i] = state.Buffer.RuneAt(info.StartPos + i)
	}
	return string(buf)
}

func insertTab(state *EditorState) Mode {
	insertText(state, "\t")
	return ModeInsert
}

// ---------------------------------------------------------------------------
// Arrow key handlers
// ---------------------------------------------------------------------------

func insertArrowUp(state *EditorState) Mode {
	mr := motion.ExecuteMotion(motion.MotionUp, state.Buffer, state.LineIndex, state.Cursor, 1)
	state.Cursor = mr.End
	state.SyncCursorPos()
	return ModeInsert
}

func insertArrowDown(state *EditorState) Mode {
	mr := motion.ExecuteMotion(motion.MotionDown, state.Buffer, state.LineIndex, state.Cursor, 1)
	state.Cursor = mr.End
	state.SyncCursorPos()
	return ModeInsert
}

func insertArrowLeft(state *EditorState) Mode {
	mr := motion.ExecuteMotion(motion.MotionLeft, state.Buffer, state.LineIndex, state.Cursor, 1)
	state.Cursor = mr.End
	state.SyncCursorPos()
	return ModeInsert
}

func insertArrowRight(state *EditorState) Mode {
	line, col := state.LineIndex.PosToLineCol(state.Cursor)
	info, ok := state.LineIndex.Get(line)
	if !ok {
		return ModeInsert
	}
	// Insert mode allows cursor one past the last character.
	newCol := min(col+1, info.Length)
	state.Cursor = state.LineIndex.LineColToPos(line, newCol)
	state.SyncCursorPos()
	return ModeInsert
}

// ---------------------------------------------------------------------------
// Insertion helpers
// ---------------------------------------------------------------------------

func insertRunes(state *EditorState, runes []rune) {
	text := string(runes)
	insertText(state, text)
}

func insertText(state *EditorState, text string) {
	// Normalize line endings so the LineIndex stays consistent.
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	pos := state.Cursor
	state.Buffer.Insert(pos, text)
	state.UndoTree.Record(buffer.EditOp{
		Type: buffer.EditInsert,
		Pos:  pos,
		Text: text,
	})
	state.LineIndex.Rebuild(state.Buffer)
	state.Cursor = pos + len([]rune(text))
	state.SyncCursorPos()
}

// ---------------------------------------------------------------------------
// Bracket auto-close helpers
// ---------------------------------------------------------------------------

// insertBracketPair inserts an opening and closing bracket, placing the
// cursor between them.
func insertBracketPair(state *EditorState, open, close rune) {
	text := string([]rune{open, close})
	pos := state.Cursor
	state.Buffer.Insert(pos, text)
	state.UndoTree.Record(buffer.EditOp{
		Type: buffer.EditInsert,
		Pos:  pos,
		Text: text,
	})
	state.LineIndex.Rebuild(state.Buffer)
	state.Cursor = pos + 1 // between the pair
	state.SyncCursorPos()
}

// skipClosingBracket advances the cursor past a closing bracket when the
// character at the cursor already matches. Returns true if the skip was
// applied.
func skipClosingBracket(state *EditorState, r rune) bool {
	if _, ok := closingBrackets[r]; !ok {
		return false
	}
	if state.Cursor >= state.Buffer.Length() {
		return false
	}
	if state.Buffer.RuneAt(state.Cursor) != r {
		return false
	}
	state.Cursor++
	state.SyncCursorPos()
	return true
}

// deleteEmptyPair checks whether the cursor sits between a matching bracket
// pair and deletes both characters if so. Returns true if the deletion was
// applied.
func deleteEmptyPair(state *EditorState) bool {
	if state.Cursor <= 0 || state.Cursor >= state.Buffer.Length() {
		return false
	}
	prev := state.Buffer.RuneAt(state.Cursor - 1)
	closer, ok := bracketPairs[prev]
	if !ok || state.Buffer.RuneAt(state.Cursor) != closer {
		return false
	}
	pos := state.Cursor - 1
	old := string([]rune{prev, closer})
	state.Buffer.Delete(pos, 2)
	state.UndoTree.Record(buffer.EditOp{
		Type:    buffer.EditDelete,
		Pos:     pos,
		OldText: old,
	})
	state.LineIndex.Rebuild(state.Buffer)
	state.Cursor = pos
	state.SyncCursorPos()
	return true
}
