package mode

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/adalundhe/sylk/ui/editor/buffer"
	"github.com/adalundhe/sylk/ui/theme"
)

// ReplaceSubMode distinguishes single-char replace (r) from continuous replace (R).
type ReplaceSubMode int

const (
	ReplaceSingle     ReplaceSubMode = iota
	ReplaceContinuous
)

// maxReplaceOriginalBuffer bounds the restore buffer for backspace in
// continuous replace mode. Derived from the maximum reasonable line length
// that a terminal can display.
const maxReplaceOriginalBuffer = 1024

// ReplaceState tracks the original characters overwritten during continuous
// replace mode so that Backspace can restore them.
type ReplaceState struct {
	SubMode   ReplaceSubMode
	StartPos  int
	Originals []runeRestore
}

// runeRestore pairs an original rune with its position for backspace recovery.
type runeRestore struct {
	Pos  int
	Char rune
}

// ReplaceMode handles keys when the editor is in replace mode.
type ReplaceMode struct {
	State ReplaceState
	theme *theme.Theme
}

// NewReplaceMode creates a ReplaceMode.
func NewReplaceMode(th *theme.Theme, sub ReplaceSubMode, startPos int) *ReplaceMode {
	return &ReplaceMode{
		State: ReplaceState{
			SubMode:   sub,
			StartPos:  startPos,
			Originals: make([]runeRestore, 0, maxReplaceOriginalBuffer),
		},
		theme: th,
	}
}

// replaceKeyHandler processes a key in replace mode and returns the next mode.
type replaceKeyHandler func(rm *ReplaceMode, state *EditorState) Mode

// replaceNamedKeyTable maps named keys to their handlers in replace mode.
var replaceNamedKeyTable = map[tea.KeyType]replaceKeyHandler{
	tea.KeyEsc:       replaceEscape,
	tea.KeyBackspace: replaceBackspace,
}

// HandleKey processes a key event in replace mode.
func (rm *ReplaceMode) HandleKey(key tea.KeyMsg, state *EditorState) (Mode, tea.Cmd) {
	// Named key dispatch.
	if handler, ok := replaceNamedKeyTable[key.Type]; ok {
		return handler(rm, state), nil
	}
	// Rune input.
	if len(key.Runes) == 1 {
		return replaceRune(rm, state, key.Runes[0]), nil
	}
	return ModeReplace, nil
}

// ---------------------------------------------------------------------------
// Replace key handlers
// ---------------------------------------------------------------------------

func replaceEscape(_ *ReplaceMode, state *EditorState) Mode {
	state.Cursor = max(state.Cursor-1, 0)
	state.ClampCursor(1)
	return ModeNormal
}

func replaceBackspace(rm *ReplaceMode, state *EditorState) Mode {
	if rm.State.SubMode == ReplaceSingle {
		return ModeReplace
	}
	if len(rm.State.Originals) == 0 {
		return ModeReplace
	}
	last := rm.State.Originals[len(rm.State.Originals)-1]
	rm.State.Originals = rm.State.Originals[:len(rm.State.Originals)-1]
	// Restore the original character at the recorded position.
	state.Buffer.Delete(last.Pos, 1)
	state.Buffer.Insert(last.Pos, string(last.Char))
	state.UndoTree.Record(buffer.EditOp{
		Type:    buffer.EditDelete,
		Pos:     last.Pos,
		OldText: replaceOriginalText(state, last.Pos),
	})
	state.UndoTree.Record(buffer.EditOp{
		Type: buffer.EditInsert,
		Pos:  last.Pos,
		Text: string(last.Char),
	})
	state.LineIndex.Rebuild(state.Buffer)
	state.Cursor = last.Pos
	state.SyncCursorPos()
	return ModeReplace
}

func replaceRune(rm *ReplaceMode, state *EditorState, r rune) Mode {
	pos := state.Cursor
	bufLen := state.Buffer.Length()
	// Record the original character before overwriting.
	if pos < bufLen && len(rm.State.Originals) < maxReplaceOriginalBuffer {
		original, _ := state.Buffer.RuneAt(pos)
		rm.State.Originals = append(rm.State.Originals, runeRestore{
			Pos:  pos,
			Char: original,
		})
		old := string(original)
		state.Buffer.Delete(pos, 1)
		state.Buffer.Insert(pos, string(r))
		state.UndoTree.Record(buffer.EditOp{
			Type:    buffer.EditDelete,
			Pos:     pos,
			OldText: old,
		})
		state.UndoTree.Record(buffer.EditOp{
			Type: buffer.EditInsert,
			Pos:  pos,
			Text: string(r),
		})
	} else if pos >= bufLen {
		// Append at end of buffer.
		state.Buffer.Insert(pos, string(r))
		state.UndoTree.Record(buffer.EditOp{
			Type: buffer.EditInsert,
			Pos:  pos,
			Text: string(r),
		})
	}
	state.LineIndex.Rebuild(state.Buffer)
	state.Cursor = pos + 1
	state.SyncCursorPos()
	// Single replace returns to normal immediately after one character.
	if rm.State.SubMode == ReplaceSingle {
		state.ClampCursor(1)
		return ModeNormal
	}
	return ModeReplace
}

// replaceOriginalText returns the rune at pos as a string, or "" if pos is
// out of range. Used for undo-tree records in continuous replace mode.
func replaceOriginalText(state *EditorState, pos int) string {
	r, ok := state.Buffer.RuneAt(pos)
	if !ok {
		return ""
	}
	return string(r)
}
