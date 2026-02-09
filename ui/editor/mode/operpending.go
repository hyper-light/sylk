package mode

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/adalundhe/sylk/ui/editor/motion"
	"github.com/adalundhe/sylk/ui/theme"
)

// OperPendingState tracks the operator that is waiting for a motion or
// text object to determine its range.
type OperPendingState struct {
	Operator motion.OperatorType
}

// OperPendingMode handles keys when an operator is waiting for a motion.
type OperPendingMode struct {
	State  OperPendingState
	parser *motion.Parser
	theme  *theme.Theme
}

// NewOperPendingMode creates an OperPendingMode for the given operator.
func NewOperPendingMode(th *theme.Theme, op motion.OperatorType) *OperPendingMode {
	return &OperPendingMode{
		State: OperPendingState{
			Operator: op,
		},
		parser: motion.NewParser(),
		theme:  th,
	}
}

// opPendingNamedKeyTable maps named keys to their handlers.
var opPendingNamedKeyTable = map[tea.KeyType]func() Mode{
	tea.KeyEsc:   func() Mode { return ModeNormal },
	tea.KeyCtrlC: func() Mode { return ModeNormal },
}

// HandleKey processes a key event in operator-pending mode.
func (op *OperPendingMode) HandleKey(key tea.KeyMsg, state *EditorState) (Mode, tea.Cmd) {
	// Named key dispatch -- cancel on Esc.
	if handler, ok := opPendingNamedKeyTable[key.Type]; ok {
		return handler(), nil
	}
	// Single-rune: feed into parser.
	if len(key.Runes) != 1 {
		return ModeOperatorPending, nil
	}
	return op.feedRune(key.Runes[0], state), nil
}

// feedRune passes a rune to the motion parser and executes when complete.
func (op *OperPendingMode) feedRune(r rune, state *EditorState) Mode {
	result, done := op.parser.Feed(r)
	if !done {
		return ModeOperatorPending
	}
	executeOperPending(op.State.Operator, result, state)
	return ModeNormal
}

// executeOperPending applies the pending operator over the resolved motion range.
func executeOperPending(opType motion.OperatorType, result *motion.ParseResult, state *EditorState) {
	mr := motion.ExecuteMotion(result.Motion, state.Buffer, state.LineIndex, state.Cursor, result.Count)
	start, end := normalizeRange(mr.Start, mr.End)
	handler, ok := operatorHandlerTable[opType]
	if !ok {
		return
	}
	handler(state, start, end)
}
