package input

// runeSpan is a half-open rune range [Start, End) within an actual line.
type runeSpan struct {
	Start int
	End   int
}

// wrapState caches the visual-line decomposition of all actual lines.
type wrapState struct {
	segments    [][]runeSpan // segments[actualRow] = visual-line spans for that row
	visualTotal int          // sum of all visual lines
	width       int          // contentWidth used for this computation
}

// computeWrap builds a wrapState from lines at the given content width.
// Wrapping is visual only — the underlying lines slice is unchanged.
func computeWrap(lines [][]rune, width int) *wrapState {
	if width < 1 {
		width = 1
	}
	ws := &wrapState{
		segments: make([][]runeSpan, len(lines)),
		width:    width,
	}
	for i, line := range lines {
		spans := wrapLine(line, width)
		ws.segments[i] = spans
		ws.visualTotal += len(spans)
	}
	return ws
}

// wrapLine computes visual-line spans for a single actual line.
func wrapLine(line []rune, width int) []runeSpan {
	n := len(line)
	if n <= width {
		return []runeSpan{{Start: 0, End: n}}
	}

	var spans []runeSpan
	pos := 0

	for pos < n {
		// On continuation segments, skip leading whitespace.
		if len(spans) > 0 {
			for pos < n && isWrapSpace(line[pos]) {
				pos++
			}
			if pos >= n {
				break
			}
		}

		segStart := pos
		segEnd := pos

		for segEnd < n && segEnd-segStart < width {
			// Find next word (including its leading whitespace).
			wordStart := segEnd
			// Skip whitespace — belongs to the upcoming word.
			for wordStart < n && isWrapSpace(line[wordStart]) {
				wordStart++
			}
			// Skip non-whitespace.
			wordEnd := wordStart
			for wordEnd < n && !isWrapSpace(line[wordEnd]) {
				wordEnd++
			}

			// Word boundary is at wordEnd; the chunk is [segEnd, wordEnd).
			chunkLen := wordEnd - segEnd
			used := segEnd - segStart

			if used+chunkLen <= width {
				// Chunk fits on this visual line.
				segEnd = wordEnd
				continue
			}

			// Chunk does not fit.
			if used > 0 {
				// Finalize current segment; new segment starts at segEnd.
				break
			}

			// Current segment is empty — single chunk > width.
			// Character-wrap: take exactly width runes.
			segEnd = segStart + width
			break
		}

		if segEnd == segStart {
			// Safety: advance at least one rune to avoid infinite loop.
			segEnd = segStart + 1
		}

		spans = append(spans, runeSpan{Start: segStart, End: segEnd})
		pos = segEnd
	}

	if len(spans) == 0 {
		return []runeSpan{{Start: 0, End: 0}}
	}
	return spans
}

// isWrapSpace reports whether r is a word-break character for wrapping.
func isWrapSpace(r rune) bool {
	return r == ' ' || r == '\t'
}

// actualToVisual maps an actual (row, col) to a visual (vRow, vCol).
// Columns in gaps between spans (stripped whitespace) map to the end
// of the preceding visual line.
func (ws *wrapState) actualToVisual(row, col int) (vRow, vCol int) {
	vRow = ws.actualRowStartVisual(row)
	spans := ws.segments[row]
	for i, sp := range spans {
		if col < sp.Start {
			// In gap before this span — belongs to previous visual line's end.
			if i > 0 {
				prev := spans[i-1]
				return vRow + i - 1, prev.End - prev.Start
			}
			return vRow, 0
		}
		if col <= sp.End || i == len(spans)-1 {
			vCol = min(col-sp.Start, sp.End-sp.Start)
			return vRow + i, vCol
		}
	}
	last := spans[len(spans)-1]
	return vRow + len(spans) - 1, last.End - last.Start
}

// visualToActual maps a visual (vRow, vCol) to an actual (row, col).
func (ws *wrapState) visualToActual(vRow, vCol int) (row, col int) {
	row, segIdx := ws.visualRowToSegment(vRow)
	span := ws.segments[row][segIdx]
	segLen := span.End - span.Start
	col = span.Start + min(vCol, segLen)
	return row, col
}

// visualRowToSegment returns the actual row and segment index for a visual row.
func (ws *wrapState) visualRowToSegment(vRow int) (actualRow, segIdx int) {
	remaining := vRow
	for i, spans := range ws.segments {
		if remaining < len(spans) {
			return i, remaining
		}
		remaining -= len(spans)
	}
	// Past end: clamp to last segment of last row.
	last := len(ws.segments) - 1
	return last, len(ws.segments[last]) - 1
}

// actualRowStartVisual returns the first visual row index for an actual row.
func (ws *wrapState) actualRowStartVisual(row int) int {
	vRow := 0
	for i := range row {
		vRow += len(ws.segments[i])
	}
	return vRow
}
