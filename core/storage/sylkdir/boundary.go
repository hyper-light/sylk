package sylkdir

import (
	"bytes"
	"slices"
)

// TaggedBoundary represents a structural break point in content with measured strength.
// Strength is COUNTED from content (consecutive blank lines, heading markers) or
// DERIVED from the AST (tree-sitter symbol kind), never chosen arbitrarily.
type TaggedBoundary struct {
	Offset   uint32
	Strength uint32
}

// SymbolBound carries tree-sitter boundary information without depending on
// ingestion or search packages. Callers convert from their symbol type at call site.
type SymbolBound struct {
	StartLine uint32 // 1-indexed, from tree-sitter AST.
	EndLine   uint32 // 1-indexed, 0 if unknown (search.SymbolInfo path).
	Strength  uint32 // Derived from SymbolKind via SymbolKindStrength.
}

// SymbolKindStrength returns boundary strength for a tree-sitter symbol kind.
// The 7 kinds are finite and enumerated from the AST grammar:
//
//	Interface/Type define new scopes       → strength 3 (strongest, like section headings)
//	Function/Method define executable blocks → strength 2 (mid-level)
//	Const/Var/Unknown are declarations      → strength 1 (minor, like blank lines)
func SymbolKindStrength(kind uint8) uint32 {
	// ingestion.SymbolKind values:
	//   0=Unknown, 1=Function, 2=Method, 3=Type, 4=Interface, 5=Const, 6=Var
	switch kind {
	case 4, 3: // Interface, Type
		return 3
	case 1, 2: // Function, Method
		return 2
	default: // Unknown, Const, Var
		return 1
	}
}

// CodeBlock represents a fenced code block detected in mixed content.
type CodeBlock struct {
	FenceStart uint32 // Byte offset of opening ``` line.
	FenceEnd   uint32 // Byte offset after closing ``` line.
	BodyStart  uint32 // Byte offset of code content (after opening fence line).
	BodyEnd    uint32 // Byte offset end of code content (before closing fence line).
	Language   string // From fence tag: ```python → "python", empty if untagged.
}

// DetectBoundaries finds all structural break points in any text content.
// Returns sorted, deduplicated tagged boundaries with measured strength.
func DetectBoundaries(content []byte, li lineIndex) []TaggedBoundary {
	blanks := findBlankLineBoundaries(content)
	headings := findHeadingBoundaries(content, li)
	codeBlocks := codeBlockBoundaries(content, li)
	return mergeTaggedBoundaries(blanks, headings, codeBlocks)
}

// DetectBoundariesWithSymbols adds tree-sitter symbol boundaries to text boundaries.
func DetectBoundariesWithSymbols(content []byte, li lineIndex, symbols []SymbolBound) []TaggedBoundary {
	base := DetectBoundaries(content, li)
	symBounds := symbolTaggedBoundaries(li, symbols)
	return mergeTaggedBoundaries(base, symBounds)
}

// DetectCodeBlocks finds fenced code blocks (``` or ~~~) in content.
func DetectCodeBlocks(content []byte, li lineIndex) []CodeBlock {
	var blocks []CodeBlock
	lineCount := li.lineCount()

	for lineNum := uint32(1); lineNum <= lineCount; lineNum++ {
		offset := li.byteAt(lineNum)
		fence, lang := parseFenceOpen(content, offset)
		if fence == 0 {
			continue
		}

		bodyStart := nextLineOffset(content, offset)
		if bodyStart == 0 {
			continue
		}

		closeLineNum, closeEnd := findFenceClose(content, li, lineNum+1, lineCount, fence)
		if closeLineNum == 0 {
			continue
		}

		bodyEnd := li.byteAt(closeLineNum)
		blocks = append(blocks, CodeBlock{
			FenceStart: offset,
			FenceEnd:   closeEnd,
			BodyStart:  bodyStart,
			BodyEnd:    bodyEnd,
			Language:   lang,
		})
		lineNum = closeLineNum // Skip past closing fence.
	}

	return blocks
}

// findBlankLineBoundaries returns boundaries at blank line sequences.
// Strength = number of consecutive blank lines (measured, not chosen).
func findBlankLineBoundaries(content []byte) []TaggedBoundary {
	var boundaries []TaggedBoundary
	i := 0
	for i < len(content) {
		if content[i] != '\n' {
			i++
			continue
		}
		count := countConsecutiveNewlines(content, i)
		if count < 2 {
			i++
			continue
		}
		// Boundary at the byte after the blank line sequence.
		end := i + count
		if end < len(content) {
			boundaries = append(boundaries, TaggedBoundary{
				Offset:   uint32(end),
				Strength: uint32(count - 1), // 2 newlines = 1 blank line = strength 1.
			})
		}
		i = end
	}
	return boundaries
}

// countConsecutiveNewlines counts consecutive '\n' bytes starting at position i.
func countConsecutiveNewlines(content []byte, i int) int {
	count := 0
	for j := i; j < len(content) && content[j] == '\n'; j++ {
		count++
	}
	return count
}

// findHeadingBoundaries returns boundaries at heading-like lines.
// All headings get strength 3 (strongest structural text marker).
func findHeadingBoundaries(content []byte, li lineIndex) []TaggedBoundary {
	var boundaries []TaggedBoundary
	lineCount := li.lineCount()

	for lineNum := uint32(1); lineNum <= lineCount; lineNum++ {
		offset := li.byteAt(lineNum)
		if isHeadingAtOffset(content, offset) {
			boundaries = append(boundaries, TaggedBoundary{
				Offset:   offset,
				Strength: 3,
			})
		}
	}
	return boundaries
}

// isHeadingAtOffset checks if the content at offset starts with '#' (markdown heading)
// or if the NEXT line is a setext underline (=== or ---).
func isHeadingAtOffset(content []byte, offset uint32) bool {
	if offset >= uint32(len(content)) {
		return false
	}
	if content[offset] == '#' {
		return true
	}
	return isSetextUnderline(content, offset)
}

// isSetextUnderline checks if this line is a setext heading underline (=== or ---).
func isSetextUnderline(content []byte, offset uint32) bool {
	if offset >= uint32(len(content)) {
		return false
	}
	ch := content[offset]
	if ch != '=' && ch != '-' {
		return false
	}
	count := 0
	for i := offset; i < uint32(len(content)) && content[i] == ch; i++ {
		count++
	}
	return count >= 3
}

// symbolTaggedBoundaries creates boundaries at symbol start lines (and end lines when available).
func symbolTaggedBoundaries(li lineIndex, symbols []SymbolBound) []TaggedBoundary {
	var boundaries []TaggedBoundary

	for _, sym := range symbols {
		if sym.StartLine >= 1 {
			offset := li.byteAt(sym.StartLine)
			boundaries = append(boundaries, TaggedBoundary{
				Offset:   offset,
				Strength: sym.Strength,
			})
		}
		// When EndLine > 0 (from ingestion.Symbol with tree-sitter AST):
		// create a closing boundary at EndLine+1 with strength 1.
		if sym.EndLine > 0 && sym.EndLine+1 <= li.lineCount() {
			offset := li.byteAt(sym.EndLine + 1)
			boundaries = append(boundaries, TaggedBoundary{
				Offset:   offset,
				Strength: 1,
			})
		}
	}

	return boundaries
}

// codeBlockBoundaries returns boundaries at code block fence lines.
// Fence open and close both get strength 3 (strongest structural marker).
func codeBlockBoundaries(content []byte, li lineIndex) []TaggedBoundary {
	blocks := DetectCodeBlocks(content, li)
	var boundaries []TaggedBoundary

	for _, blk := range blocks {
		boundaries = append(boundaries, TaggedBoundary{
			Offset:   blk.FenceStart,
			Strength: 3,
		})
		if blk.FenceEnd < uint32(len(content)) {
			boundaries = append(boundaries, TaggedBoundary{
				Offset:   blk.FenceEnd,
				Strength: 3,
			})
		}
	}

	return boundaries
}

// mergeTaggedBoundaries combines multiple boundary sets, taking max strength at each offset.
func mergeTaggedBoundaries(sets ...[]TaggedBoundary) []TaggedBoundary {
	total := 0
	for _, s := range sets {
		total += len(s)
	}
	if total == 0 {
		return nil
	}

	combined := make([]TaggedBoundary, 0, total)
	for _, s := range sets {
		combined = append(combined, s...)
	}

	slices.SortFunc(combined, func(a, b TaggedBoundary) int {
		if a.Offset != b.Offset {
			return int(a.Offset) - int(b.Offset)
		}
		return int(b.Strength) - int(a.Strength) // Higher strength first for dedup.
	})

	// Deduplicate: at each offset, keep the entry with maximum strength.
	deduped := combined[:0]
	for _, tb := range combined {
		if len(deduped) > 0 && deduped[len(deduped)-1].Offset == tb.Offset {
			continue // Already have the max-strength entry for this offset.
		}
		deduped = append(deduped, tb)
	}

	return deduped
}

// parseFenceOpen checks if content at offset is a fence opening (``` or ~~~).
// Returns the fence character count and the language tag (empty if none).
func parseFenceOpen(content []byte, offset uint32) (uint32, string) {
	if offset >= uint32(len(content)) {
		return 0, ""
	}

	ch := content[offset]
	if ch != '`' && ch != '~' {
		return 0, ""
	}

	count := uint32(0)
	i := offset
	for i < uint32(len(content)) && content[i] == ch {
		count++
		i++
	}
	if count < 3 {
		return 0, ""
	}

	// Extract language tag (text until end of line).
	langStart := i
	for i < uint32(len(content)) && content[i] != '\n' {
		i++
	}
	lang := string(bytes.TrimSpace(content[langStart:i]))

	return count, lang
}

// findFenceClose finds the closing fence matching the opening fence character and count.
func findFenceClose(content []byte, li lineIndex, startLine, endLine, fenceLen uint32) (uint32, uint32) {
	ch := byte('`')
	// Detect fence char from the opening fence's offset context isn't needed here;
	// we need to infer it. Use content at the opening fence line to get the char.
	// Actually, we need the fence char. Let's search for both.

	for lineNum := startLine; lineNum <= endLine; lineNum++ {
		offset := li.byteAt(lineNum)
		if offset >= uint32(len(content)) {
			continue
		}

		ch = content[offset]
		if ch != '`' && ch != '~' {
			continue
		}

		count := uint32(0)
		j := offset
		for j < uint32(len(content)) && content[j] == ch {
			count++
			j++
		}
		if count < fenceLen {
			continue
		}

		// Rest of line must be empty (only whitespace allowed).
		restIsEmpty := true
		for k := j; k < uint32(len(content)) && content[k] != '\n'; k++ {
			if content[k] != ' ' && content[k] != '\t' && content[k] != '\r' {
				restIsEmpty = false
				break
			}
		}
		if !restIsEmpty {
			continue
		}

		// End is byte after the closing fence line (including newline).
		end := j
		for end < uint32(len(content)) && content[end] != '\n' {
			end++
		}
		if end < uint32(len(content)) {
			end++ // Include the newline.
		}
		return lineNum, end
	}

	return 0, 0
}

// nextLineOffset returns the byte offset of the line after the line containing offset.
func nextLineOffset(content []byte, offset uint32) uint32 {
	for i := offset; i < uint32(len(content)); i++ {
		if content[i] == '\n' {
			if i+1 < uint32(len(content)) {
				return i + 1
			}
			return 0
		}
	}
	return 0
}
