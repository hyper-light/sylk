// Package mode implements the vim mode state machine and per-mode key
// handling for the editor.
package mode

import (
	"github.com/adalundhe/sylk/ui/editor/buffer"
	"github.com/adalundhe/sylk/ui/theme"
)

// Mode represents one of the editor's operating modes.
type Mode int

const (
	ModeNormal Mode = iota
	ModeInsert
	ModeVisual
	ModeVisualLine
	ModeVisualBlock
	ModeReplace
	ModeCmdline
	ModeOperatorPending
	ModePreview
)

// ModeChangeMsg is emitted when the editor switches modes.
type ModeChangeMsg struct {
	From Mode
	To   Mode
}

// ModeInfo holds display metadata for a mode.
type ModeInfo struct {
	Mode Mode
	Name string
	Icon string
}

// modeInfoTable maps each mode to its display information.
var modeInfoTable = map[Mode]ModeInfo{
	ModeNormal:          {Mode: ModeNormal, Name: "NORMAL", Icon: theme.IconNormal},
	ModeInsert:          {Mode: ModeInsert, Name: "INSERT", Icon: theme.IconInsert},
	ModeVisual:          {Mode: ModeVisual, Name: "VISUAL", Icon: theme.IconVisual},
	ModeVisualLine:      {Mode: ModeVisualLine, Name: "V-LINE", Icon: theme.IconVisual},
	ModeVisualBlock:     {Mode: ModeVisualBlock, Name: "V-BLOCK", Icon: theme.IconVisual},
	ModeReplace:         {Mode: ModeReplace, Name: "REPLACE", Icon: theme.IconReplace},
	ModeCmdline:         {Mode: ModeCmdline, Name: "COMMAND", Icon: theme.IconCommand},
	ModeOperatorPending: {Mode: ModeOperatorPending, Name: "OP-PEND", Icon: theme.IconNormal},
	ModePreview:         {Mode: ModePreview, Name: "PREVIEW", Icon: theme.IconPreview},
}

// ModeName returns the human-readable name for a mode.
func ModeName(m Mode) string {
	if info, ok := modeInfoTable[m]; ok {
		return info.Name
	}
	return "UNKNOWN"
}

// ModeIcon returns the single-character icon for a mode.
func ModeIcon(m Mode) string {
	if info, ok := modeInfoTable[m]; ok {
		return info.Icon
	}
	return "?"
}

// EditorState is the shared mutable state passed to every mode handler.
type EditorState struct {
	Buffer            *buffer.PieceTable
	LineIndex         *buffer.LineIndex
	Cursor            int
	CursorLine        int
	CursorCol         int
	UndoTree          *buffer.UndoTree
	InStringOrComment bool   // set by the model from tree-sitter or highlight regions
	CursorNodeType    string // tree-sitter node type at cursor position
}

// SyncCursorPos updates CursorLine and CursorCol from the absolute Cursor
// position using the LineIndex.
func (es *EditorState) SyncCursorPos() {
	es.CursorLine, es.CursorCol = es.LineIndex.PosToLineCol(es.Cursor)
}

// ClampCursor ensures the cursor stays within the buffer bounds, respecting
// a trailing-character offset (0 for insert mode, 1 for normal mode).
func (es *EditorState) ClampCursor(trailingOffset int) {
	length := es.Buffer.Length()
	maxPos := max(length-trailingOffset, 0)
	es.Cursor = max(min(es.Cursor, maxPos), 0)
	es.SyncCursorPos()
}

// updateLineIndex applies an incremental line index update when possible,
// falling back to a full rebuild when the edit crosses line boundaries.
// Only correct for single-mutation operations — compound operations (e.g.
// delete + insert) must use state.LineIndex.Rebuild directly.
func updateLineIndex(state *EditorState) {
	if !state.LineIndex.IncrementalUpdate(state.Buffer.LastEdit()) {
		state.LineIndex.Rebuild(state.Buffer)
	}
}
