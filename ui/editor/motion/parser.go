package motion

import "unicode"

// ParserState represents the current state of the vim grammar parser.
type ParserState int

const (
	StateIdle     ParserState = iota
	StateCount1               // accumulating the first count
	StateOperator             // accumulating operator runes
	StateCount2               // accumulating the second count
	StateMotion               // awaiting motion or text-object rune
)

// ParseResult holds the fully parsed [count][operator][count][motion] command.
type ParseResult struct {
	Count    int          // count1 * count2, minimum 1
	Operator OperatorType
	Motion   MotionType
	TextObj  TextObjType
}

// transitionFunc processes a rune and returns the next state plus whether
// the rune was consumed.
type transitionFunc func(p *Parser, r rune) ParserState

// Parser implements a state-machine for the vim [count][op][count][motion]
// grammar. Feed runes one at a time; when a complete command is recognised
// Feed returns a non-nil ParseResult.
type Parser struct {
	state   ParserState
	count1  int
	count2  int
	opRunes []rune
	op      OperatorType
	pending []rune
}

// transitionTable maps each state to its handler.
var transitionTable = map[ParserState]transitionFunc{
	StateIdle:     handleIdle,
	StateCount1:   handleCount1,
	StateOperator: handleOperator,
	StateCount2:   handleCount2,
	StateMotion:   handleMotion,
}

// NewParser creates a new vim grammar parser in the idle state.
func NewParser() *Parser {
	return &Parser{opRunes: make([]rune, 0, 2), pending: make([]rune, 0, 4)}
}

// Feed sends a rune into the parser. Returns a complete ParseResult when the
// command is fully recognised, and done=true. On partial input, returns nil
// and done=false.
func (p *Parser) Feed(r rune) (*ParseResult, bool) {
	handler, ok := transitionTable[p.state]
	if !ok {
		p.Reset()
		return nil, false
	}
	p.state = handler(p, r)
	return p.tryComplete()
}

// Reset returns the parser to its idle state.
func (p *Parser) Reset() {
	p.state = StateIdle
	p.count1 = 0
	p.count2 = 0
	p.opRunes = p.opRunes[:0]
	p.op = OpNone
	p.pending = p.pending[:0]
}

// tryComplete checks whether the parser has reached a terminal state and
// returns the assembled result.
func (p *Parser) tryComplete() (*ParseResult, bool) {
	if p.state != StateIdle {
		return nil, false
	}
	if len(p.pending) == 0 {
		return nil, false
	}
	result := p.buildResult()
	p.Reset()
	return result, true
}

// buildResult assembles the ParseResult from accumulated fields.
func (p *Parser) buildResult() *ParseResult {
	c1 := max(p.count1, 1)
	c2 := max(p.count2, 1)
	m := MotionNone
	if len(p.pending) > 0 {
		m, _ = LookupMotion(p.pending[0])
	}
	return &ParseResult{
		Count:    c1 * c2,
		Operator: p.op,
		Motion:   m,
	}
}

// ---------------------------------------------------------------------------
// State handlers
// ---------------------------------------------------------------------------

func handleIdle(p *Parser, r rune) ParserState {
	if isDigitNonZero(r) {
		p.count1 = digitValue(r)
		return StateCount1
	}
	if isOperatorStart(r) {
		p.opRunes = append(p.opRunes, r)
		return resolveOperator(p)
	}
	if isMotionRune(r) {
		p.pending = append(p.pending, r)
		return StateIdle // complete
	}
	p.Reset()
	return StateIdle
}

func handleCount1(p *Parser, r rune) ParserState {
	if unicode.IsDigit(r) {
		p.count1 = p.count1*10 + digitValue(r)
		return StateCount1
	}
	if isOperatorStart(r) {
		p.opRunes = append(p.opRunes, r)
		return resolveOperator(p)
	}
	if isMotionRune(r) {
		p.pending = append(p.pending, r)
		return StateIdle
	}
	p.Reset()
	return StateIdle
}

func handleOperator(p *Parser, r rune) ParserState {
	// Continue accumulating multi-rune operators (g~, gu, gU, gd).
	extended := append(p.opRunes, r)
	if op, ok := LookupOperator(extended); ok {
		p.opRunes = extended
		p.op = op
		if IsStandalone(op) {
			// Standalone operators (gd) complete immediately.
			p.pending = append(p.pending, r) // ensure tryComplete sees content
			return StateIdle
		}
		return StateCount2
	}
	// Check for doubled operator (dd, cc, yy) => linewise self-motion.
	if len(p.opRunes) == 1 && r == p.opRunes[0] {
		p.pending = append(p.pending, 'j') // linewise current line via 'j' with count=1
		p.pending[0] = '_'                 // use _ as sentinel for current-line
		return StateIdle
	}
	if isDigitNonZero(r) {
		p.count2 = digitValue(r)
		return StateCount2
	}
	if isMotionRune(r) {
		p.pending = append(p.pending, r)
		return StateIdle
	}
	p.Reset()
	return StateIdle
}

func handleCount2(p *Parser, r rune) ParserState {
	if unicode.IsDigit(r) {
		p.count2 = p.count2*10 + digitValue(r)
		return StateCount2
	}
	if isMotionRune(r) {
		p.pending = append(p.pending, r)
		return StateIdle
	}
	p.Reset()
	return StateIdle
}

func handleMotion(p *Parser, r rune) ParserState {
	p.pending = append(p.pending, r)
	return StateIdle
}

// resolveOperator checks if the accumulated operator runes fully match.
func resolveOperator(p *Parser) ParserState {
	if op, ok := LookupOperator(p.opRunes); ok {
		p.op = op
		return StateCount2
	}
	if IsOperatorPrefix(p.opRunes) {
		return StateOperator
	}
	p.Reset()
	return StateIdle
}

// ---------------------------------------------------------------------------
// Classification helpers
// ---------------------------------------------------------------------------

func isDigitNonZero(r rune) bool {
	return r >= '1' && r <= '9'
}

func digitValue(r rune) int {
	return int(r - '0')
}

func isOperatorStart(r rune) bool {
	return r == 'd' || r == 'c' || r == 'y' || r == '>' || r == '<' || r == 'g'
}

func isMotionRune(r rune) bool {
	_, ok := motionTypeLookup[r]
	return ok
}
