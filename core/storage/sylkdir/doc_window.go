package sylkdir

// LineIndex provides O(1) line-to-byte-offset mapping for document content.
// Built once per document via BuildLineIndex, then used for repeated extractions.
type LineIndex struct {
	starts []uint32 // byte offset of each line (1-indexed: starts[1] = line 1)
	length uint32   // total number of lines
}

// BuildLineIndex scans content and records the byte offset of each line start.
// Lines are 1-indexed: line 1 starts at byte 0. A trailing newline does not
// create an additional empty line — "A\nB\n" has 2 lines, not 3.
func BuildLineIndex(content string) LineIndex {
	if len(content) == 0 {
		return LineIndex{starts: []uint32{0}, length: 0}
	}

	starts := make([]uint32, 1, 32)
	starts[0] = 0 // sentinel for index 0 (unused)
	starts = append(starts, 0) // line 1 starts at byte 0

	for i := range len(content) {
		if content[i] == '\n' && i+1 < len(content) {
			starts = append(starts, uint32(i+1))
		}
	}

	lineCount := uint32(len(starts) - 1) // subtract sentinel
	return LineIndex{starts: starts, length: lineCount}
}

// LineCount returns the total number of lines in the indexed content.
func (li LineIndex) LineCount() uint32 {
	return li.length
}

// Extract returns the exact content between lineStart and lineEnd (1-indexed, inclusive).
// Returns empty string if the range is invalid or content is empty.
func (li LineIndex) Extract(content string, lineStart, lineEnd uint32) string {
	start, end := li.byteRange(content, lineStart, lineEnd)
	return content[start:end]
}

// ExtractWithWindow returns content from lineStart-padding to lineEnd+padding,
// clamped to document bounds. Returns the actual window bounds as well.
func (li LineIndex) ExtractWithWindow(content string, lineStart, lineEnd, padding uint32) (text string, windowStart, windowEnd uint32) {
	windowStart = clampLineMin(lineStart, padding)
	windowEnd = clampLineMax(lineEnd, padding, li.length)
	start, end := li.byteRange(content, windowStart, windowEnd)
	return content[start:end], windowStart, windowEnd
}

// byteRange converts 1-indexed line numbers to byte offsets into content.
func (li LineIndex) byteRange(content string, lineStart, lineEnd uint32) (uint32, uint32) {
	if li.length == 0 || lineStart > li.length || lineStart < 1 {
		return 0, 0
	}
	if lineEnd > li.length {
		lineEnd = li.length
	}

	start := li.starts[lineStart]

	var end uint32
	if lineEnd+1 < uint32(len(li.starts)) {
		end = li.starts[lineEnd+1]
	} else {
		end = uint32(len(content))
	}
	return start, end
}

// clampLineMin returns max(line-padding, 1) with underflow guard.
func clampLineMin(line, padding uint32) uint32 {
	if padding >= line {
		return 1
	}
	return line - padding
}

// clampLineMax returns min(line+padding, maxLine) with overflow guard.
func clampLineMax(line, padding, maxLine uint32) uint32 {
	sum := line + padding
	if sum < line { // overflow
		return maxLine
	}
	if sum > maxLine {
		return maxLine
	}
	return sum
}
