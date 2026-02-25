package input

import "strings"

// selection tracks a range selection using an anchor+cursor model.
// The anchor stays fixed; the cursor (cursorRow/cursorCol on Model) is the
// moving end. The selected range is always [min(anchor, cursor), max(anchor, cursor)).
type selection struct {
	anchorRow int
	anchorCol int
	active    bool
}

// orderedRange returns the selection endpoints in canonical order (start ≤ end).
// cursorRow/cursorCol is the moving end of the selection.
func (s *selection) orderedRange(cursorRow, cursorCol int) (sr, sc, er, ec int) {
	if s.anchorRow < cursorRow || (s.anchorRow == cursorRow && s.anchorCol <= cursorCol) {
		return s.anchorRow, s.anchorCol, cursorRow, cursorCol
	}
	return cursorRow, cursorCol, s.anchorRow, s.anchorCol
}

// clampIdx constrains an index to [0, bound].
func clampIdx(v, bound int) int { return max(0, min(v, bound)) }

// extractText returns the text between anchor and cursor within the given lines.
func (s *selection) extractText(lines [][]rune, cursorRow, cursorCol int) string {
	sr, sc, er, ec := s.orderedRange(cursorRow, cursorCol)
	sr = max(0, min(sr, len(lines)-1))
	er = max(0, min(er, len(lines)-1))

	// Single-line selection.
	if sr == er {
		line := lines[sr]
		sc = clampIdx(sc, len(line))
		ec = clampIdx(ec, len(line))
		return string(line[sc:ec])
	}

	var b strings.Builder
	// First line: from sc to end.
	first := lines[sr]
	sc = clampIdx(sc, len(first))
	b.WriteString(string(first[sc:]))

	// Middle lines: full content.
	for i := sr + 1; i < er; i++ {
		b.WriteByte('\n')
		b.WriteString(string(lines[i]))
	}

	// Last line: from start to ec.
	b.WriteByte('\n')
	last := lines[er]
	ec = clampIdx(ec, len(last))
	b.WriteString(string(last[:ec]))

	return b.String()
}

// deleteRange removes the selected text and returns the new lines plus the
// resulting cursor position.
func (s *selection) deleteRange(lines [][]rune, cursorRow, cursorCol int) ([][]rune, int, int) {
	sr, sc, er, ec := s.orderedRange(cursorRow, cursorCol)
	sr = max(0, min(sr, len(lines)-1))
	er = max(0, min(er, len(lines)-1))

	// Clamp to line bounds.
	sc = clampIdx(sc, len(lines[sr]))
	ec = clampIdx(ec, len(lines[er]))

	// Build the replacement line: before-start + after-end.
	before := make([]rune, sc)
	copy(before, lines[sr][:sc])
	after := lines[er][ec:]

	merged := make([]rune, 0, len(before)+len(after))
	merged = append(merged, before...)
	merged = append(merged, after...)

	// Build new lines slice.
	newLines := make([][]rune, 0, len(lines)-(er-sr))
	newLines = append(newLines, lines[:sr]...)
	newLines = append(newLines, merged)
	newLines = append(newLines, lines[er+1:]...)

	if len(newLines) == 0 {
		newLines = [][]rune{nil}
	}

	return newLines, sr, sc
}

// containsPos reports whether (qRow, qCol) falls inside the selected range.
func (s *selection) containsPos(cursorRow, cursorCol, qRow, qCol int) bool {
	sr, sc, er, ec := s.orderedRange(cursorRow, cursorCol)

	if qRow < sr || qRow > er {
		return false
	}
	if qRow == sr && qCol < sc {
		return false
	}
	if qRow == er && qCol >= ec {
		return false
	}
	return true
}
