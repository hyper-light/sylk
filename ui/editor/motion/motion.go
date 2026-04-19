package motion

import (
	"unicode"

	"github.com/adalundhe/sylk/ui/editor/buffer"
)

// MotionType identifies a cursor motion (h, j, k, l, w, b, etc.).
type MotionType int

const (
	MotionNone MotionType = iota
	MotionLeft
	MotionRight
	MotionUp
	MotionDown
	MotionWordForward
	MotionWordBackward
	MotionWordEnd
	MotionWORDForward
	MotionWORDBackward
	MotionWORDEnd
	MotionLineStart
	MotionLineEnd
	MotionFirstNonBlank
	MotionDocStart
	MotionDocEnd
	MotionFindChar
	MotionFindCharBack
	MotionTilChar
	MotionTilCharBack
	MotionMatchParen
	MotionParagraphForward
	MotionParagraphBackward
	MotionSentenceForward
	MotionSentenceBackward
)

// TextObjType identifies a vim text-object (iw, aw, i", etc.).
type TextObjType int

const (
	TextObjNone TextObjType = iota
	TextObjInnerWord
	TextObjAWord
	TextObjInnerWORD
	TextObjAWORD
	TextObjInnerParen
	TextObjAParen
	TextObjInnerBracket
	TextObjABracket
	TextObjInnerBrace
	TextObjABrace
	TextObjInnerQuote
	TextObjAQuote
)

// MotionRange describes the start and end rune positions produced by a motion.
type MotionRange struct {
	Start    int
	End      int
	Linewise bool
}

// motionFunc computes a MotionRange for the given context.
type motionFunc func(buf *buffer.PieceTable, li *buffer.LineIndex, cursor, count int) MotionRange

// motionTable maps single-rune motion keys to their compute functions.
var motionTable = map[rune]motionFunc{
	'h': motionLeft,
	'l': motionRight,
	'j': motionDown,
	'k': motionUp,
	'w': motionWordForward,
	'b': motionWordBackward,
	'e': motionWordEnd,
	'W': motionWORDForward,
	'B': motionWORDBackward,
	'E': motionWORDEnd,
	'0': motionLineStart,
	'$': motionLineEnd,
	'^': motionFirstNonBlank,
	'G': motionDocEnd,
	'{': motionParagraphBackward,
	'}': motionParagraphForward,
}

// motionTypeLookup maps runes to MotionType for the parser.
var motionTypeLookup = map[rune]MotionType{
	'h': MotionLeft,
	'l': MotionRight,
	'j': MotionDown,
	'k': MotionUp,
	'w': MotionWordForward,
	'b': MotionWordBackward,
	'e': MotionWordEnd,
	'W': MotionWORDForward,
	'B': MotionWORDBackward,
	'E': MotionWORDEnd,
	'0': MotionLineStart,
	'$': MotionLineEnd,
	'^': MotionFirstNonBlank,
	'G': MotionDocEnd,
	'f': MotionFindChar,
	'F': MotionFindCharBack,
	't': MotionTilChar,
	'T': MotionTilCharBack,
	'%': MotionMatchParen,
	'{': MotionParagraphBackward,
	'}': MotionParagraphForward,
}

// LookupMotion returns the MotionType for a single rune, if any.
func LookupMotion(r rune) (MotionType, bool) {
	m, ok := motionTypeLookup[r]
	return m, ok
}

// ExecuteMotion computes the cursor range for a motion applied count times.
func ExecuteMotion(motion MotionType, buf *buffer.PieceTable, lineIdx *buffer.LineIndex, cursor, count int) MotionRange {
	fn := motionTypeToFunc(motion)
	if fn == nil {
		return MotionRange{Start: cursor, End: cursor}
	}
	return fn(buf, lineIdx, cursor, count)
}

// motionTypeToFunc resolves a MotionType to its implementing function.
var motionTypeToFuncTable = map[MotionType]motionFunc{
	MotionLeft:              motionLeft,
	MotionRight:             motionRight,
	MotionDown:              motionDown,
	MotionUp:                motionUp,
	MotionWordForward:       motionWordForward,
	MotionWordBackward:      motionWordBackward,
	MotionWordEnd:           motionWordEnd,
	MotionWORDForward:       motionWORDForward,
	MotionWORDBackward:      motionWORDBackward,
	MotionWORDEnd:           motionWORDEnd,
	MotionLineStart:         motionLineStart,
	MotionLineEnd:           motionLineEnd,
	MotionFirstNonBlank:     motionFirstNonBlank,
	MotionDocStart:          motionDocStart,
	MotionDocEnd:            motionDocEnd,
	MotionParagraphForward:  motionParagraphForward,
	MotionParagraphBackward: motionParagraphBackward,
}

func motionTypeToFunc(m MotionType) motionFunc {
	return motionTypeToFuncTable[m]
}

// ---------------------------------------------------------------------------
// Motion implementations
// ---------------------------------------------------------------------------

func motionLeft(buf *buffer.PieceTable, li *buffer.LineIndex, cursor, count int) MotionRange {
	line, col := li.PosToLineCol(cursor)
	newCol := max(col-count, 0)
	end := li.LineColToPos(line, newCol)
	return MotionRange{Start: cursor, End: end}
}

func motionRight(buf *buffer.PieceTable, li *buffer.LineIndex, cursor, count int) MotionRange {
	line, col := li.PosToLineCol(cursor)
	info, ok := li.Get(line)
	if !ok {
		return MotionRange{Start: cursor, End: cursor}
	}
	maxCol := max(info.Length-1, 0)
	newCol := min(col+count, maxCol)
	end := li.LineColToPos(line, newCol)
	return MotionRange{Start: cursor, End: end}
}

func motionDown(buf *buffer.PieceTable, li *buffer.LineIndex, cursor, count int) MotionRange {
	line, col := li.PosToLineCol(cursor)
	targetLine := min(line+count, li.Count()-1)
	end := li.LineColToPos(targetLine, col)
	return MotionRange{Start: cursor, End: end, Linewise: true}
}

func motionUp(buf *buffer.PieceTable, li *buffer.LineIndex, cursor, count int) MotionRange {
	line, col := li.PosToLineCol(cursor)
	targetLine := max(line-count, 0)
	end := li.LineColToPos(targetLine, col)
	return MotionRange{Start: cursor, End: end, Linewise: true}
}

func motionLineStart(_ *buffer.PieceTable, li *buffer.LineIndex, cursor, _ int) MotionRange {
	line, _ := li.PosToLineCol(cursor)
	end := li.LineColToPos(line, 0)
	return MotionRange{Start: cursor, End: end}
}

func motionLineEnd(_ *buffer.PieceTable, li *buffer.LineIndex, cursor, _ int) MotionRange {
	line, _ := li.PosToLineCol(cursor)
	info, ok := li.Get(line)
	if !ok {
		return MotionRange{Start: cursor, End: cursor}
	}
	endCol := max(info.Length-1, 0)
	end := li.LineColToPos(line, endCol)
	return MotionRange{Start: cursor, End: end}
}

func motionFirstNonBlank(buf *buffer.PieceTable, li *buffer.LineIndex, cursor, _ int) MotionRange {
	line, _ := li.PosToLineCol(cursor)
	info, ok := li.Get(line)
	if !ok {
		return MotionRange{Start: cursor, End: cursor}
	}
	col := 0
	for col < info.Length {
		r, _ := buf.RuneAt(info.StartPos + col)
		if !unicode.IsSpace(r) {
			break
		}
		col++
	}
	end := li.LineColToPos(line, col)
	return MotionRange{Start: cursor, End: end}
}

func motionDocStart(_ *buffer.PieceTable, li *buffer.LineIndex, cursor, _ int) MotionRange {
	return MotionRange{Start: cursor, End: 0, Linewise: true}
}

func motionDocEnd(_ *buffer.PieceTable, li *buffer.LineIndex, cursor, _ int) MotionRange {
	lastLine := max(li.Count()-1, 0)
	end := li.LineColToPos(lastLine, 0)
	return MotionRange{Start: cursor, End: end, Linewise: true}
}

func motionWordForward(buf *buffer.PieceTable, li *buffer.LineIndex, cursor, count int) MotionRange {
	pos := cursor
	length := buf.Length()
	for i := 0; i < count && pos < length; i++ {
		pos = nextWordStart(buf, pos, length, false)
	}
	return MotionRange{Start: cursor, End: pos}
}

func motionWordBackward(buf *buffer.PieceTable, li *buffer.LineIndex, cursor, count int) MotionRange {
	pos := cursor
	for i := 0; i < count && pos > 0; i++ {
		pos = prevWordStart(buf, pos, false)
	}
	return MotionRange{Start: cursor, End: pos}
}

func motionWordEnd(buf *buffer.PieceTable, li *buffer.LineIndex, cursor, count int) MotionRange {
	pos := cursor
	length := buf.Length()
	for i := 0; i < count && pos < length-1; i++ {
		pos = nextWordEnd(buf, pos, length, false)
	}
	return MotionRange{Start: cursor, End: pos}
}

func motionWORDForward(buf *buffer.PieceTable, li *buffer.LineIndex, cursor, count int) MotionRange {
	pos := cursor
	length := buf.Length()
	for i := 0; i < count && pos < length; i++ {
		pos = nextWordStart(buf, pos, length, true)
	}
	return MotionRange{Start: cursor, End: pos}
}

func motionWORDBackward(buf *buffer.PieceTable, li *buffer.LineIndex, cursor, count int) MotionRange {
	pos := cursor
	for i := 0; i < count && pos > 0; i++ {
		pos = prevWordStart(buf, pos, true)
	}
	return MotionRange{Start: cursor, End: pos}
}

func motionWORDEnd(buf *buffer.PieceTable, li *buffer.LineIndex, cursor, count int) MotionRange {
	pos := cursor
	length := buf.Length()
	for i := 0; i < count && pos < length-1; i++ {
		pos = nextWordEnd(buf, pos, length, true)
	}
	return MotionRange{Start: cursor, End: pos}
}

func motionParagraphForward(buf *buffer.PieceTable, li *buffer.LineIndex, cursor, count int) MotionRange {
	line, _ := li.PosToLineCol(cursor)
	target := line
	remaining := count
	for target < li.Count()-1 && remaining > 0 {
		target++
		info, ok := li.Get(target)
		if ok && info.Length == 0 {
			remaining--
		}
	}
	end := li.LineColToPos(target, 0)
	return MotionRange{Start: cursor, End: end, Linewise: true}
}

func motionParagraphBackward(buf *buffer.PieceTable, li *buffer.LineIndex, cursor, count int) MotionRange {
	line, _ := li.PosToLineCol(cursor)
	target := line
	remaining := count
	for target > 0 && remaining > 0 {
		target--
		info, ok := li.Get(target)
		if ok && info.Length == 0 {
			remaining--
		}
	}
	end := li.LineColToPos(target, 0)
	return MotionRange{Start: cursor, End: end, Linewise: true}
}

// ---------------------------------------------------------------------------
// Word helpers
// ---------------------------------------------------------------------------

// isWordChar returns whether a rune is a word character for vim small-word.
func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// nextWordStart finds the start of the next word (or WORD if bigWord is true).
func nextWordStart(buf *buffer.PieceTable, pos, length int, bigWord bool) int {
	if pos >= length-1 {
		return length - 1
	}
	p := pos
	// Skip current word characters.
	p = skipForward(buf, p, length, bigWord, true)
	// Skip whitespace.
	p = skipWhitespaceForward(buf, p, length)
	return min(p, length-1)
}

// prevWordStart finds the start of the previous word.
func prevWordStart(buf *buffer.PieceTable, pos int, bigWord bool) int {
	if pos <= 0 {
		return 0
	}
	p := pos - 1
	// Skip whitespace backwards.
	p = skipWhitespaceBackward(buf, p)
	// Skip word characters backwards.
	p = skipBackward(buf, p, bigWord, true)
	return max(p, 0)
}

// nextWordEnd finds the end of the next word.
func nextWordEnd(buf *buffer.PieceTable, pos, length int, bigWord bool) int {
	if pos >= length-1 {
		return length - 1
	}
	p := pos + 1
	// Skip whitespace.
	p = skipWhitespaceForward(buf, p, length)
	// Skip word characters.
	p = skipForwardToEnd(buf, p, length, bigWord)
	return min(p, length-1)
}

func skipForward(buf *buffer.PieceTable, p, length int, bigWord, matchCurrent bool) int {
	if p >= length {
		return p
	}
	rStart, _ := buf.RuneAt(p)
	startIsWord := isWordCharCheck(rStart, bigWord)
	for p < length {
		r, _ := buf.RuneAt(p)
		charIsWord := isWordCharCheck(r, bigWord)
		if matchCurrent && charIsWord != startIsWord {
			return p
		}
		if !matchCurrent && !unicode.IsSpace(r) {
			return p
		}
		p++
	}
	return p
}

func skipForwardToEnd(buf *buffer.PieceTable, p, length int, bigWord bool) int {
	if p >= length {
		return p
	}
	rStart, _ := buf.RuneAt(p)
	startIsWord := isWordCharCheck(rStart, bigWord)
	for p+1 < length {
		next, _ := buf.RuneAt(p + 1)
		nextIsWord := isWordCharCheck(next, bigWord)
		if nextIsWord != startIsWord || unicode.IsSpace(next) {
			return p
		}
		p++
	}
	return p
}

func skipBackward(buf *buffer.PieceTable, p int, bigWord, matchCurrent bool) int {
	if p <= 0 {
		return 0
	}
	rStart, _ := buf.RuneAt(p)
	startIsWord := isWordCharCheck(rStart, bigWord)
	for p > 0 {
		prev, _ := buf.RuneAt(p - 1)
		prevIsWord := isWordCharCheck(prev, bigWord)
		if matchCurrent && prevIsWord != startIsWord {
			return p
		}
		if !matchCurrent && !unicode.IsSpace(prev) {
			return p
		}
		p--
	}
	return p
}

func skipWhitespaceForward(buf *buffer.PieceTable, p, length int) int {
	for p < length {
		r, _ := buf.RuneAt(p)
		if !unicode.IsSpace(r) {
			break
		}
		p++
	}
	return p
}

func skipWhitespaceBackward(buf *buffer.PieceTable, p int) int {
	for p > 0 {
		r, _ := buf.RuneAt(p)
		if !unicode.IsSpace(r) {
			break
		}
		p--
	}
	return p
}

// isWordCharCheck classifies a rune; for WORD motions only whitespace is the
// boundary.
func isWordCharCheck(r rune, bigWord bool) bool {
	if bigWord {
		return !unicode.IsSpace(r)
	}
	return isWordChar(r)
}
