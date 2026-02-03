package mode

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/adalundhe/sylk/ui/theme"
)

// cmdHistoryCapacity is the maximum number of history entries retained.
// Derived from typical command-line history depth in vim.
const cmdHistoryCapacity = 100

// CmdLinePrompt identifies the type of command-line being used.
type CmdLinePrompt rune

const (
	PromptCommand CmdLinePrompt = ':'
	PromptSearch  CmdLinePrompt = '/'
	PromptReverse CmdLinePrompt = '?'
)

// CmdLineResult is emitted when the user submits a command-line entry.
type CmdLineResult struct {
	Prompt  CmdLinePrompt
	Content string
}

// Completer provides tab-completion candidates for command-line input.
type Completer interface {
	Complete(prefix string) []string
}

// CmdLineState holds the editing state of the command-line bar.
type CmdLineState struct {
	Prompt    CmdLinePrompt
	Input     []rune
	CursorPos int
	History   *CmdHistory
	completer Completer
	compIdx   int
	compItems []string
}

// CmdHistory is a bounded ring buffer of command-line entries.
type CmdHistory struct {
	entries []string
	head    int
	count   int
	cursor  int
}

// NewCmdHistory creates a history ring buffer with the standard capacity.
func NewCmdHistory() *CmdHistory {
	return &CmdHistory{
		entries: make([]string, cmdHistoryCapacity),
	}
}

// Push appends an entry to the history ring.
func (h *CmdHistory) Push(entry string) {
	h.entries[h.head] = entry
	h.head = (h.head + 1) % len(h.entries)
	if h.count < len(h.entries) {
		h.count++
	}
	h.cursor = h.count
}

// Prev returns the previous history entry relative to the current cursor.
func (h *CmdHistory) Prev() (string, bool) {
	if h.cursor <= 0 {
		return "", false
	}
	h.cursor--
	idx := (h.head - h.count + h.cursor + len(h.entries)) % len(h.entries)
	return h.entries[idx], true
}

// Next returns the next history entry relative to the current cursor.
func (h *CmdHistory) Next() (string, bool) {
	if h.cursor >= h.count {
		return "", false
	}
	h.cursor++
	if h.cursor >= h.count {
		return "", true
	}
	idx := (h.head - h.count + h.cursor + len(h.entries)) % len(h.entries)
	return h.entries[idx], true
}

// ResetCursor resets the history navigation cursor to the end.
func (h *CmdHistory) ResetCursor() {
	h.cursor = h.count
}

// CmdLineMode handles keys when the editor is in command-line mode.
type CmdLineMode struct {
	State   CmdLineState
	theme   *theme.Theme
	history *CmdHistory
}

// NewCmdLineMode creates a CmdLineMode with the given prompt.
func NewCmdLineMode(th *theme.Theme, prompt CmdLinePrompt, history *CmdHistory, completer Completer) *CmdLineMode {
	return &CmdLineMode{
		State: CmdLineState{
			Prompt:    prompt,
			Input:     make([]rune, 0, cmdHistoryCapacity),
			CursorPos: 0,
			History:   history,
			completer: completer,
		},
		theme:   th,
		history: history,
	}
}

// cmdlineKeyHandler processes a key in command-line mode and returns the
// next mode plus an optional result.
type cmdlineKeyHandler func(cm *CmdLineMode, state *EditorState) (Mode, *CmdLineResult)

// cmdlineNamedKeyTable maps named keys to their handlers.
var cmdlineNamedKeyTable = map[tea.KeyType]cmdlineKeyHandler{
	tea.KeyEsc:       cmdlineEscape,
	tea.KeyEnter:     cmdlineEnter,
	tea.KeyBackspace: cmdlineBackspace,
	tea.KeyLeft:      cmdlineCursorLeft,
	tea.KeyRight:     cmdlineCursorRight,
	tea.KeyUp:        cmdlineHistoryPrev,
	tea.KeyDown:      cmdlineHistoryNext,
	tea.KeyTab:       cmdlineTabComplete,
}

// cmdlineCtrlTable maps ctrl key strings to their handlers.
var cmdlineCtrlTable = map[string]cmdlineKeyHandler{
	"ctrl+w": cmdlineDeleteWord,
	"ctrl+u": cmdlineClearLine,
	"ctrl+c": cmdlineEscape,
}

// HandleKey processes a key event in command-line mode.
func (cm *CmdLineMode) HandleKey(key tea.KeyMsg, state *EditorState) (Mode, tea.Cmd, *CmdLineResult) {
	// Ctrl combo dispatch.
	if handler, ok := cmdlineCtrlTable[key.String()]; ok {
		next, result := handler(cm, state)
		return next, nil, result
	}
	// Named key dispatch.
	if handler, ok := cmdlineNamedKeyTable[key.Type]; ok {
		next, result := handler(cm, state)
		return next, nil, result
	}
	// Rune insertion.
	if len(key.Runes) > 0 {
		cmdlineInsertRunes(cm, key.Runes)
	}
	return ModeCmdline, nil, nil
}

// Content returns the current command-line input as a string.
func (cm *CmdLineMode) Content() string {
	return string(cm.State.Input)
}

// DisplayLine returns the prompt character followed by the input text.
func (cm *CmdLineMode) DisplayLine() string {
	return string(cm.State.Prompt) + string(cm.State.Input)
}

// ---------------------------------------------------------------------------
// Command-line key handlers
// ---------------------------------------------------------------------------

func cmdlineEscape(_ *CmdLineMode, _ *EditorState) (Mode, *CmdLineResult) {
	return ModeNormal, nil
}

func cmdlineEnter(cm *CmdLineMode, _ *EditorState) (Mode, *CmdLineResult) {
	content := string(cm.State.Input)
	cm.history.Push(content)
	result := &CmdLineResult{
		Prompt:  cm.State.Prompt,
		Content: content,
	}
	return ModeNormal, result
}

func cmdlineBackspace(cm *CmdLineMode, _ *EditorState) (Mode, *CmdLineResult) {
	if cm.State.CursorPos <= 0 {
		return ModeNormal, nil
	}
	cm.State.CursorPos--
	cm.State.Input = append(cm.State.Input[:cm.State.CursorPos], cm.State.Input[cm.State.CursorPos+1:]...)
	return ModeCmdline, nil
}

func cmdlineCursorLeft(cm *CmdLineMode, _ *EditorState) (Mode, *CmdLineResult) {
	cm.State.CursorPos = max(cm.State.CursorPos-1, 0)
	return ModeCmdline, nil
}

func cmdlineCursorRight(cm *CmdLineMode, _ *EditorState) (Mode, *CmdLineResult) {
	cm.State.CursorPos = min(cm.State.CursorPos+1, len(cm.State.Input))
	return ModeCmdline, nil
}

func cmdlineHistoryPrev(cm *CmdLineMode, _ *EditorState) (Mode, *CmdLineResult) {
	entry, ok := cm.history.Prev()
	if !ok {
		return ModeCmdline, nil
	}
	cm.State.Input = []rune(entry)
	cm.State.CursorPos = len(cm.State.Input)
	return ModeCmdline, nil
}

func cmdlineHistoryNext(cm *CmdLineMode, _ *EditorState) (Mode, *CmdLineResult) {
	entry, ok := cm.history.Next()
	if !ok {
		return ModeCmdline, nil
	}
	cm.State.Input = []rune(entry)
	cm.State.CursorPos = len(cm.State.Input)
	return ModeCmdline, nil
}

func cmdlineTabComplete(cm *CmdLineMode, _ *EditorState) (Mode, *CmdLineResult) {
	if cm.State.completer == nil {
		return ModeCmdline, nil
	}
	prefix := string(cm.State.Input[:cm.State.CursorPos])
	// Refresh candidates when the prefix has changed.
	if len(cm.State.compItems) == 0 {
		cm.State.compItems = cm.State.completer.Complete(prefix)
		cm.State.compIdx = 0
	}
	if len(cm.State.compItems) == 0 {
		return ModeCmdline, nil
	}
	candidate := cm.State.compItems[cm.State.compIdx%len(cm.State.compItems)]
	cm.State.Input = []rune(candidate)
	cm.State.CursorPos = len(cm.State.Input)
	cm.State.compIdx++
	return ModeCmdline, nil
}

func cmdlineDeleteWord(cm *CmdLineMode, _ *EditorState) (Mode, *CmdLineResult) {
	pos := cm.State.CursorPos
	// Skip trailing spaces.
	for pos > 0 && cm.State.Input[pos-1] == ' ' {
		pos--
	}
	// Skip word characters.
	for pos > 0 && cm.State.Input[pos-1] != ' ' {
		pos--
	}
	cm.State.Input = append(cm.State.Input[:pos], cm.State.Input[cm.State.CursorPos:]...)
	cm.State.CursorPos = pos
	return ModeCmdline, nil
}

func cmdlineClearLine(cm *CmdLineMode, _ *EditorState) (Mode, *CmdLineResult) {
	cm.State.Input = cm.State.Input[:0]
	cm.State.CursorPos = 0
	return ModeCmdline, nil
}

// cmdlineInsertRunes inserts runes at the cursor position.
func cmdlineInsertRunes(cm *CmdLineMode, runes []rune) {
	// Clear completion state on new input.
	cm.State.compItems = nil
	cm.State.compIdx = 0

	pos := cm.State.CursorPos
	newInput := make([]rune, 0, len(cm.State.Input)+len(runes))
	newInput = append(newInput, cm.State.Input[:pos]...)
	newInput = append(newInput, runes...)
	newInput = append(newInput, cm.State.Input[pos:]...)
	cm.State.Input = newInput
	cm.State.CursorPos = pos + len(runes)
}
