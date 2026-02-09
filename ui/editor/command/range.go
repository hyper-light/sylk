package command

import (
	"errors"
	"strings"
)

// Range represents an evaluated, 1-indexed inclusive line range.
type Range struct {
	Start int
	End   int
}

// AtomKind classifies a single range atom.
type AtomKind int

const (
	AtomAbsolute      AtomKind = iota // numeric literal
	AtomCurrentLine                   // .
	AtomLastLine                      // $
	AtomMark                          // 'x
	AtomSearchForward                 // /pattern/
	AtomSearchBack                    // ?pattern?
	AtomPercent                       // %
)

// Atom is a single unevaluated range component.
type Atom struct {
	Kind    AtomKind
	Value   int    // for AtomAbsolute and relative offsets
	Name    rune   // for AtomMark
	Pattern string // for AtomSearchForward / AtomSearchBack
}

// RangeSpec is the parsed but unevaluated range expression.
type RangeSpec struct {
	Left    Atom
	Right   *Atom // nil if the range is a single address
	Offsets []int // +N / -N offsets applied to the left atom
}

// atomClassEntry maps a leading character to its AtomKind.
type atomClassEntry struct {
	test func(r rune) bool
	kind AtomKind
}

// atomClassTable drives classification of the first rune in a range atom.
var atomClassTable = []atomClassEntry{
	{test: isDigitRune, kind: AtomAbsolute},
	{test: func(r rune) bool { return r == '.' }, kind: AtomCurrentLine},
	{test: func(r rune) bool { return r == '$' }, kind: AtomLastLine},
	{test: func(r rune) bool { return r == '\'' }, kind: AtomMark},
	{test: func(r rune) bool { return r == '/' }, kind: AtomSearchForward},
	{test: func(r rune) bool { return r == '?' }, kind: AtomSearchBack},
	{test: func(r rune) bool { return r == '%' }, kind: AtomPercent},
}

func isDigitRune(r rune) bool { return r >= '0' && r <= '9' }

// classifyAtomRune returns the atom kind for a leading rune.
func classifyAtomRune(r rune) (AtomKind, bool) {
	for _, entry := range atomClassTable {
		if entry.test(r) {
			return entry.kind, true
		}
	}
	return 0, false
}

// ParseRangeSpec parses a range prefix from input, returning the spec and the
// remaining unparsed input. Returns nil spec if no range is present.
func ParseRangeSpec(input string) (*RangeSpec, string) {
	runes := []rune(strings.TrimSpace(input))
	if len(runes) == 0 {
		return nil, input
	}
	// Check for % shorthand (expands to 1,$).
	if runes[0] == '%' {
		spec := &RangeSpec{
			Left:  Atom{Kind: AtomAbsolute, Value: 1},
			Right: &Atom{Kind: AtomLastLine},
		}
		return spec, string(runes[1:])
	}
	left, rest := parseAtom(runes)
	if left == nil {
		return nil, input
	}
	offsets, rest := parseOffsets(rest)
	spec := &RangeSpec{Left: *left, Offsets: offsets}
	// Check for separator (comma or semicolon).
	if len(rest) > 0 && (rest[0] == ',' || rest[0] == ';') {
		right, rest2 := parseAtom(rest[1:])
		if right != nil {
			spec.Right = right
			rest = rest2
		}
	}
	return spec, string(rest)
}

// parseAtom extracts a single range atom from the rune slice.
func parseAtom(runes []rune) (*Atom, []rune) {
	if len(runes) == 0 {
		return nil, runes
	}
	kind, ok := classifyAtomRune(runes[0])
	if !ok {
		return nil, runes
	}
	parser, found := atomParserTable[kind]
	if !found {
		return nil, runes
	}
	return parser(runes)
}

// atomParserFunc parses an atom from the beginning of a rune slice.
type atomParserFunc func(runes []rune) (*Atom, []rune)

// atomParserTable dispatches atom parsing by kind.
var atomParserTable = map[AtomKind]atomParserFunc{
	AtomAbsolute:      parseAbsolute,
	AtomCurrentLine:   parseSingleRune(AtomCurrentLine),
	AtomLastLine:      parseSingleRune(AtomLastLine),
	AtomMark:          parseMark,
	AtomSearchForward: parseSearch(AtomSearchForward, '/'),
	AtomSearchBack:    parseSearch(AtomSearchBack, '?'),
}

// parseAbsolute reads a decimal number from the front of runes.
func parseAbsolute(runes []rune) (*Atom, []rune) {
	end := 0
	for end < len(runes) && isDigitRune(runes[end]) {
		end++
	}
	val := runeDigitsToInt(runes[:end])
	return &Atom{Kind: AtomAbsolute, Value: val}, runes[end:]
}

// parseSingleRune returns a parser that consumes exactly one character.
func parseSingleRune(kind AtomKind) atomParserFunc {
	return func(runes []rune) (*Atom, []rune) {
		return &Atom{Kind: kind}, runes[1:]
	}
}

// parseMark parses 'x (mark reference).
func parseMark(runes []rune) (*Atom, []rune) {
	if len(runes) < 2 {
		return nil, runes
	}
	return &Atom{Kind: AtomMark, Name: runes[1]}, runes[2:]
}

// parseSearch returns a parser for /pattern/ or ?pattern? delimited patterns.
func parseSearch(kind AtomKind, delim rune) atomParserFunc {
	return func(runes []rune) (*Atom, []rune) {
		// Skip the opening delimiter.
		rest := runes[1:]
		end := 0
		for end < len(rest) && rest[end] != delim {
			end++
		}
		pattern := string(rest[:end])
		// Skip the closing delimiter if present.
		if end < len(rest) {
			end++
		}
		return &Atom{Kind: kind, Pattern: pattern}, rest[end:]
	}
}

// parseOffsets reads a chain of +N or -N offset modifiers.
func parseOffsets(runes []rune) ([]int, []rune) {
	var offsets []int
	for len(runes) > 0 && (runes[0] == '+' || runes[0] == '-') {
		sign := 1
		if runes[0] == '-' {
			sign = -1
		}
		runes = runes[1:]
		end := 0
		for end < len(runes) && isDigitRune(runes[end]) {
			end++
		}
		val := 1 // bare + or - means +1 / -1
		if end > 0 {
			val = runeDigitsToInt(runes[:end])
		}
		offsets = append(offsets, sign*val)
		runes = runes[end:]
	}
	return offsets, runes
}

// runeDigitsToInt converts a slice of digit runes to an integer.
func runeDigitsToInt(digits []rune) int {
	val := 0
	for _, d := range digits {
		val = val*10 + int(d-'0')
	}
	return val
}

// ---------------------------------------------------------------------------
// Evaluation
// ---------------------------------------------------------------------------

// MarkGetter resolves a mark rune to a 1-indexed line number.
type MarkGetter func(name rune) (int, bool)

// SearchFunc finds the next line matching a pattern.
// forward controls the search direction. Returns 1-indexed line number.
type SearchFunc func(pattern string, forward bool) (int, bool)

// EvalRange evaluates a RangeSpec into a concrete Range using the provided
// context. currentLine and lastLine are 1-indexed.
func EvalRange(spec *RangeSpec, currentLine, lastLine int, marks MarkGetter, search SearchFunc) (Range, error) {
	if spec == nil {
		return Range{Start: currentLine, End: currentLine}, nil
	}
	start, err := evalAtom(&spec.Left, currentLine, lastLine, marks, search)
	if err != nil {
		return Range{}, err
	}
	for _, off := range spec.Offsets {
		start += off
	}
	if spec.Right == nil {
		return clampRange(Range{Start: start, End: start}, lastLine), nil
	}
	end, err := evalAtom(spec.Right, currentLine, lastLine, marks, search)
	if err != nil {
		return Range{}, err
	}
	return clampRange(Range{Start: start, End: end}, lastLine), nil
}

// evalAtom resolves a single atom to a 1-indexed line number.
func evalAtom(a *Atom, currentLine, lastLine int, marks MarkGetter, search SearchFunc) (int, error) {
	eval, ok := atomEvalTable[a.Kind]
	if !ok {
		return 0, errors.New("range: unsupported atom kind")
	}
	return eval(a, currentLine, lastLine, marks, search)
}

// atomEvalFunc evaluates a single atom.
type atomEvalFunc func(a *Atom, cur, last int, marks MarkGetter, search SearchFunc) (int, error)

// atomEvalTable dispatches evaluation by atom kind.
var atomEvalTable = map[AtomKind]atomEvalFunc{
	AtomAbsolute:      evalAbsolute,
	AtomCurrentLine:   evalCurrentLine,
	AtomLastLine:      evalLastLine,
	AtomMark:          evalMark,
	AtomSearchForward: evalSearchForward,
	AtomSearchBack:    evalSearchBack,
}

func evalAbsolute(a *Atom, _, _ int, _ MarkGetter, _ SearchFunc) (int, error) {
	return a.Value, nil
}

func evalCurrentLine(_ *Atom, cur, _ int, _ MarkGetter, _ SearchFunc) (int, error) {
	return cur, nil
}

func evalLastLine(_ *Atom, _, last int, _ MarkGetter, _ SearchFunc) (int, error) {
	return last, nil
}

func evalMark(a *Atom, _, _ int, marks MarkGetter, _ SearchFunc) (int, error) {
	if marks == nil {
		return 0, errors.New("range: mark resolution unavailable")
	}
	line, ok := marks(a.Name)
	if !ok {
		return 0, errors.New("range: mark not set")
	}
	return line, nil
}

func evalSearchForward(a *Atom, _, _ int, _ MarkGetter, search SearchFunc) (int, error) {
	if search == nil {
		return 0, errors.New("range: search unavailable")
	}
	line, ok := search(a.Pattern, true)
	if !ok {
		return 0, errors.New("range: pattern not found")
	}
	return line, nil
}

func evalSearchBack(a *Atom, _, _ int, _ MarkGetter, search SearchFunc) (int, error) {
	if search == nil {
		return 0, errors.New("range: search unavailable")
	}
	line, ok := search(a.Pattern, false)
	if !ok {
		return 0, errors.New("range: pattern not found")
	}
	return line, nil
}

// clampRange ensures both endpoints are within [1, lastLine].
func clampRange(r Range, lastLine int) Range {
	r.Start = max(1, min(r.Start, lastLine))
	r.End = max(1, min(r.End, lastLine))
	return r
}
