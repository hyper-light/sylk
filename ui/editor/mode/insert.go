package mode

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/adalundhe/sylk/ui/editor/buffer"
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
}

// HandleKey processes a key event in insert mode.
func (im *InsertMode) HandleKey(key tea.KeyMsg, state *EditorState) (Mode, tea.Cmd) {
	// Named key dispatch.
	if handler, ok := insertKeyTable[key.Type]; ok {
		return handler(state), nil
	}
	// Rune insertion.
	if len(key.Runes) > 0 {
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
	insertText(state, "\n")
	return ModeInsert
}

func insertTab(state *EditorState) Mode {
	insertText(state, "\t")
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
