package motion

import (
	"unicode"

	"github.com/adalundhe/sylk/ui/editor/buffer"
)

// TextObjectContext provides the state needed by text object functions
// without importing the mode package (which would create a cycle).
type TextObjectContext struct {
	Buffer    *buffer.PieceTable
	LineIndex *buffer.LineIndex
	Cursor    int
}

// TextObject computes the range for a text object relative to the cursor.
// When inner is true, surrounding delimiters or whitespace are excluded.
type TextObject func(ctx TextObjectContext, inner bool) MotionRange

// textObjTable is the registry of text objects indexed by their trigger rune.
var textObjTable = map[rune]TextObject{
	'w':  textObjWord,
	'W':  textObjWORD,
	's':  textObjSentence,
	'p':  textObjParagraph,
	'"':  makeQuoteObj('"'),
	'\'': makeQuoteObj('\''),
	'`':  makeQuoteObj('`'),
	'(':  makePairObj('(', ')'),
	')':  makePairObj('(', ')'),
	'b':  makePairObj('(', ')'),
	'[':  makePairObj('[', ']'),
	']':  makePairObj('[', ']'),
	'{':  makePairObj('{', '}'),
	'}':  makePairObj('{', '}'),
	'B':  makePairObj('{', '}'),
	'<':  makePairObj('<', '>'),
	'>':  makePairObj('<', '>'),
	't':  textObjTag,
}

// LookupTextObject returns the TextObject for a rune, if registered.
func LookupTextObject(key rune) (TextObject, bool) {
	obj, ok := textObjTable[key]
	return obj, ok
}

// ---------------------------------------------------------------------------
// Word objects (iw, aw, iW, aW)
// ---------------------------------------------------------------------------

func textObjWord(ctx TextObjectContext, inner bool) MotionRange {
	return wordObject(ctx.Buffer, ctx.Cursor, inner, false)
}

func textObjWORD(ctx TextObjectContext, inner bool) MotionRange {
	return wordObject(ctx.Buffer, ctx.Cursor, inner, true)
}

func wordObject(buf *buffer.PieceTable, cursor int, inner, bigWord bool) MotionRange {
	length := buf.Length()
	if length == 0 {
		return MotionRange{Start: cursor, End: cursor}
	}
	pos := max(min(cursor, length-1), 0)
	curRune, _ := buf.RuneAt(pos)
	curIsSpace := unicode.IsSpace(curRune)
	curIsWord := isWordCharCheck(curRune, bigWord)
	// Expand backward to the start of the word/WORD.
	start := pos
	for start > 0 {
		prev, _ := buf.RuneAt(start - 1)
		prevIsSpace := unicode.IsSpace(prev)
		prevIsWord := isWordCharCheck(prev, bigWord)
		if prevIsSpace != curIsSpace || prevIsWord != curIsWord {
			break
		}
		start--
	}
	// Expand forward to the end of the word/WORD.
	end := pos
	for end+1 < length {
		next, _ := buf.RuneAt(end + 1)
		nextIsSpace := unicode.IsSpace(next)
		nextIsWord := isWordCharCheck(next, bigWord)
		if nextIsSpace != curIsSpace || nextIsWord != curIsWord {
			break
		}
		end++
	}
	if inner {
		return MotionRange{Start: start, End: end}
	}
	// "a word" includes trailing whitespace if present, else leading.
	trailEnd := end
	for trailEnd+1 < length {
		r, _ := buf.RuneAt(trailEnd + 1)
		if !unicode.IsSpace(r) {
			break
		}
		trailEnd++
	}
	if trailEnd > end {
		return MotionRange{Start: start, End: trailEnd}
	}
	leadStart := start
	for leadStart > 0 {
		r, _ := buf.RuneAt(leadStart - 1)
		if !unicode.IsSpace(r) {
			break
		}
		leadStart--
	}
	return MotionRange{Start: leadStart, End: end}
}

// ---------------------------------------------------------------------------
// Sentence objects (is, as)
// ---------------------------------------------------------------------------

func textObjSentence(ctx TextObjectContext, inner bool) MotionRange {
	buf := ctx.Buffer
	length := buf.Length()
	if length == 0 {
		return MotionRange{Start: ctx.Cursor, End: ctx.Cursor}
	}
	pos := max(min(ctx.Cursor, length-1), 0)
	// Scan backward to find sentence start.
	start := pos
	for start > 0 {
		prev, _ := buf.RuneAt(start - 1)
		if isSentenceEnd(prev) {
			break
		}
		start--
	}
	// Skip leading whitespace for "inner" or include it for "a".
	if inner {
		for start < length {
			r, _ := buf.RuneAt(start)
			if !unicode.IsSpace(r) {
				break
			}
			start++
		}
	}
	// Scan forward to find sentence end.
	end := pos
	for end+1 < length {
		r, _ := buf.RuneAt(end)
		if isSentenceEnd(r) {
			break
		}
		end++
	}
	if !inner {
		// Include trailing whitespace for "a sentence".
		for end+1 < length {
			r, _ := buf.RuneAt(end + 1)
			if !unicode.IsSpace(r) {
				break
			}
			end++
		}
	}
	return MotionRange{Start: start, End: end}
}

// sentenceEndRunes holds the characters that terminate a sentence.
var sentenceEndRunes = map[rune]bool{'.': true, '!': true, '?': true}

func isSentenceEnd(r rune) bool { return sentenceEndRunes[r] }

// ---------------------------------------------------------------------------
// Paragraph objects (ip, ap)
// ---------------------------------------------------------------------------

func textObjParagraph(ctx TextObjectContext, inner bool) MotionRange {
	buf := ctx.Buffer
	li := ctx.LineIndex
	line, _ := li.PosToLineCol(ctx.Cursor)
	lineCount := li.Count()
	// Find paragraph start (first blank line above or start of file).
	startLine := line
	for startLine > 0 {
		info, ok := li.Get(startLine - 1)
		if ok && info.Length == 0 {
			break
		}
		startLine--
	}
	// Find paragraph end (first blank line below or end of file).
	endLine := line
	for endLine+1 < lineCount {
		info, ok := li.Get(endLine + 1)
		if ok && info.Length == 0 {
			break
		}
		endLine++
	}
	startInfo, ok := li.Get(startLine)
	if !ok {
		return MotionRange{Start: ctx.Cursor, End: ctx.Cursor}
	}
	endInfo, ok := li.Get(endLine)
	if !ok {
		return MotionRange{Start: ctx.Cursor, End: ctx.Cursor}
	}
	start := startInfo.StartPos
	end := endInfo.StartPos + max(endInfo.Length-1, 0)
	if !inner {
		// Include trailing blank lines for "a paragraph".
		for endLine+1 < lineCount {
			nextInfo, ok := li.Get(endLine + 1)
			if !ok || nextInfo.Length != 0 {
				break
			}
			endLine++
		}
		eInfo, ok := li.Get(endLine)
		if ok {
			end = eInfo.StartPos + max(eInfo.Length-1, 0)
			// Include the newline at end of last blank line.
			if end+1 < buf.Length() {
				end++
			}
		}
	}
	return MotionRange{Start: start, End: end, Linewise: true}
}

// ---------------------------------------------------------------------------
// Quote objects (i", a", i', a', i`, a`)
// ---------------------------------------------------------------------------

// makeQuoteObj creates a text object that finds matching quote delimiters.
func makeQuoteObj(quote rune) TextObject {
	return func(ctx TextObjectContext, inner bool) MotionRange {
		return quoteObject(ctx.Buffer, ctx.Cursor, quote, inner)
	}
}

func quoteObject(buf *buffer.PieceTable, cursor int, quote rune, inner bool) MotionRange {
	length := buf.Length()
	if length == 0 {
		return MotionRange{Start: cursor, End: cursor}
	}
	pos := max(min(cursor, length-1), 0)
	// Scan backward for the opening quote.
	start := pos
	for start > 0 {
		r, _ := buf.RuneAt(start)
		if r == quote {
			break
		}
		start--
	}
	if r, _ := buf.RuneAt(start); r != quote {
		return MotionRange{Start: cursor, End: cursor}
	}
	// Scan forward for the closing quote.
	end := pos
	if end <= start {
		end = start + 1
	}
	for end < length {
		r, _ := buf.RuneAt(end)
		if r == quote {
			break
		}
		end++
	}
	if end >= length {
		return MotionRange{Start: cursor, End: cursor}
	}
	if r, _ := buf.RuneAt(end); r != quote {
		return MotionRange{Start: cursor, End: cursor}
	}
	if inner {
		return MotionRange{Start: start + 1, End: end - 1}
	}
	return MotionRange{Start: start, End: end}
}

// ---------------------------------------------------------------------------
// Bracket/paren objects (i(, a(, i[, a[, i{, a{, i<, a<)
// ---------------------------------------------------------------------------

// makePairObj creates a text object that finds matching bracket pairs.
func makePairObj(open, close rune) TextObject {
	return func(ctx TextObjectContext, inner bool) MotionRange {
		return pairObject(ctx.Buffer, ctx.Cursor, open, close, inner)
	}
}

func pairObject(buf *buffer.PieceTable, cursor int, open, close rune, inner bool) MotionRange {
	length := buf.Length()
	if length == 0 {
		return MotionRange{Start: cursor, End: cursor}
	}
	pos := max(min(cursor, length-1), 0)
	// Scan backward for the matching opening bracket, respecting nesting.
	start := findOpenBracket(buf, pos, open, close)
	if start < 0 {
		return MotionRange{Start: cursor, End: cursor}
	}
	// Scan forward for the matching closing bracket, respecting nesting.
	end := findCloseBracket(buf, start, length, open, close)
	if end < 0 {
		return MotionRange{Start: cursor, End: cursor}
	}
	if inner {
		return MotionRange{Start: start + 1, End: end - 1}
	}
	return MotionRange{Start: start, End: end}
}

// findOpenBracket scans backward from pos for the nearest unmatched open bracket.
func findOpenBracket(buf *buffer.PieceTable, pos int, open, close rune) int {
	depth := 0
	for i := pos; i >= 0; i-- {
		r, _ := buf.RuneAt(i)
		if r == close {
			depth++
		}
		if r == open {
			if depth == 0 {
				return i
			}
			depth--
		}
	}
	return -1
}

// findCloseBracket scans forward from start for the matching close bracket.
func findCloseBracket(buf *buffer.PieceTable, start, length int, open, close rune) int {
	depth := 0
	for i := start; i < length; i++ {
		r, _ := buf.RuneAt(i)
		if r == open {
			depth++
		}
		if r == close {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// ---------------------------------------------------------------------------
// Tag objects (it, at)
// ---------------------------------------------------------------------------

func textObjTag(ctx TextObjectContext, inner bool) MotionRange {
	buf := ctx.Buffer
	length := buf.Length()
	if length == 0 {
		return MotionRange{Start: ctx.Cursor, End: ctx.Cursor}
	}
	pos := max(min(ctx.Cursor, length-1), 0)
	// Scan backward for opening '<'.
	openStart := pos
	for openStart > 0 {
		r, _ := buf.RuneAt(openStart)
		if r == '<' {
			break
		}
		openStart--
	}
	if r, _ := buf.RuneAt(openStart); r != '<' {
		return MotionRange{Start: ctx.Cursor, End: ctx.Cursor}
	}
	// Find end of opening tag '>'.
	openEnd := openStart + 1
	for openEnd < length {
		r, _ := buf.RuneAt(openEnd)
		if r == '>' {
			break
		}
		openEnd++
	}
	if openEnd >= length {
		return MotionRange{Start: ctx.Cursor, End: ctx.Cursor}
	}
	// Scan forward for matching closing tag '</'.
	closeStart := openEnd + 1
	for closeStart+1 < length {
		r, _ := buf.RuneAt(closeStart)
		if r == '<' {
			next, _ := buf.RuneAt(closeStart + 1)
			if next == '/' {
				break
			}
		}
		closeStart++
	}
	if closeStart+1 >= length {
		return MotionRange{Start: ctx.Cursor, End: ctx.Cursor}
	}
	// Find end of closing tag.
	closeEnd := closeStart + 1
	for closeEnd < length {
		r, _ := buf.RuneAt(closeEnd)
		if r == '>' {
			break
		}
		closeEnd++
	}
	if closeEnd >= length {
		return MotionRange{Start: ctx.Cursor, End: ctx.Cursor}
	}
	if inner {
		return MotionRange{Start: openEnd + 1, End: closeStart - 1}
	}
	return MotionRange{Start: openStart, End: closeEnd}
}
