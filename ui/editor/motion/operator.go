// Package motion implements the vim grammar parser, operator definitions,
// and motion definitions for the editor's normal-mode command language.
package motion

// OperatorType identifies a vim operator (d, c, y, etc.).
type OperatorType int

const (
	OpNone OperatorType = iota
	OpDelete
	OpChange
	OpYank
	OpIndent
	OpDedent
	OpFormat
	OpUppercase
	OpLowercase
	OpToggleCase
	OpGotoDefinition
	OpGotoReferences
)

// EditResult carries the outcome of an operator execution.
type EditResult struct {
	NewContent string
	CursorPos  int
	Err        error
}

// operatorEntry maps a rune sequence to its operator type.
type operatorEntry struct {
	runes []rune
	op    OperatorType
}

// operatorTable is the table-driven mapping from key sequences to operators.
var operatorTable = []operatorEntry{
	{runes: []rune{'d'}, op: OpDelete},
	{runes: []rune{'c'}, op: OpChange},
	{runes: []rune{'y'}, op: OpYank},
	{runes: []rune{'>'}, op: OpIndent},
	{runes: []rune{'<'}, op: OpDedent},
	{runes: []rune{'g', 'q'}, op: OpFormat},
	{runes: []rune{'g', 'U'}, op: OpUppercase},
	{runes: []rune{'g', 'u'}, op: OpLowercase},
	{runes: []rune{'g', '~'}, op: OpToggleCase},
	{runes: []rune{'g', 'd'}, op: OpGotoDefinition},
	{runes: []rune{'g', 'r'}, op: OpGotoReferences},
}

// LookupOperator attempts to match the given rune sequence against known
// operators. It returns the operator type and true on a full match.
func LookupOperator(runes []rune) (OperatorType, bool) {
	for _, e := range operatorTable {
		if runesEqual(runes, e.runes) {
			return e.op, true
		}
	}
	return OpNone, false
}

// IsOperatorPrefix reports whether runes is a valid prefix of any operator
// key sequence (used by the parser to stay in the operator state).
func IsOperatorPrefix(runes []rune) bool {
	for _, e := range operatorTable {
		if isPrefixOf(runes, e.runes) {
			return true
		}
	}
	return false
}

// standaloneOperators is the set of operators that complete immediately
// without requiring a subsequent motion or count. Used for commands like
// gd (go-to-definition) that act on the cursor position directly.
var standaloneOperators = map[OperatorType]bool{
	OpGotoDefinition: true,
	OpGotoReferences: true,
}

// IsStandalone reports whether an operator completes without a motion.
func IsStandalone(op OperatorType) bool {
	return standaloneOperators[op]
}

// runesEqual returns true when a and b contain the same runes.
func runesEqual(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// isPrefixOf reports whether prefix is a strict prefix of target.
func isPrefixOf(prefix, target []rune) bool {
	if len(prefix) >= len(target) {
		return false
	}
	for i := range prefix {
		if prefix[i] != target[i] {
			return false
		}
	}
	return true
}
