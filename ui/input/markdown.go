package input

import (
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"

	"github.com/adalundhe/sylk/ui/theme"
)

// mdStyleKind identifies an inline markdown style.
type mdStyleKind int

const (
	mdNone   mdStyleKind = iota
	mdBold
	mdItalic
	mdBoldItalic
	mdCode
	mdStrike
	mdLink
)

// mdStyleRegion is a styled span in display coordinates.
type mdStyleRegion struct {
	startCol int         // display column (inclusive)
	endCol   int         // display column (exclusive)
	kind     mdStyleKind
}

// mdLine maps between raw and display coordinates for a single line.
type mdLine struct {
	rawToDisp []int           // rawCol → dispCol (-1 if hidden)
	dispToRaw []int           // dispCol → rawCol
	regions   []mdStyleRegion // sorted by startCol
}

// mdDisplay holds the parsed markdown display mapping for all lines.
type mdDisplay struct {
	lines []mdLine
}

// mdInputStyles holds lipgloss styles for markdown rendering.
type mdInputStyles struct {
	bold       lipgloss.Style
	italic     lipgloss.Style
	boldItalic lipgloss.Style
	code       lipgloss.Style
	strike     lipgloss.Style
	link       lipgloss.Style
}

func newMdInputStyles(th *theme.Theme) *mdInputStyles {
	return &mdInputStyles{
		bold:       lipgloss.NewStyle().Foreground(th.Palette.Foreground).Bold(true),
		italic:     lipgloss.NewStyle().Foreground(th.Palette.Foreground).Italic(true),
		boldItalic: lipgloss.NewStyle().Foreground(th.Palette.Foreground).Bold(true).Italic(true),
		code:       lipgloss.NewStyle().Foreground(th.Palette.Primary).Background(th.Palette.Subtle),
		strike:     lipgloss.NewStyle().Foreground(th.Palette.Muted).Strikethrough(true),
		link:       lipgloss.NewStyle().Foreground(th.Palette.Info).Underline(true),
	}
}

func (s *mdInputStyles) styleFor(kind mdStyleKind) lipgloss.Style {
	switch kind {
	case mdBold:
		return s.bold
	case mdItalic:
		return s.italic
	case mdBoldItalic:
		return s.boldItalic
	case mdCode:
		return s.code
	case mdStrike:
		return s.strike
	case mdLink:
		return s.link
	default:
		return lipgloss.NewStyle()
	}
}

// mdParser is a package-level goldmark parser singleton (stateless, goroutine-safe).
var mdInputParser parser.Parser

func init() {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
	)
	mdInputParser = md.Parser()
}

// byteRange is a half-open range of bytes in the source.
type byteRange struct {
	start, end int
}

// styledByteRange pairs a byte range with a style kind.
type styledByteRange struct {
	byteRange
	kind mdStyleKind
}

// parseMdDisplay builds an mdDisplay from raw input lines.
// Returns nil if the input has no markdown formatting worth rendering.
func parseMdDisplay(lines [][]rune, th *theme.Theme) *mdDisplay {
	// Build source with known line offsets.
	var src strings.Builder
	lineOffsets := make([]int, len(lines))
	for i, line := range lines {
		lineOffsets[i] = src.Len()
		if i > 0 {
			src.WriteByte('\n')
			lineOffsets[i] = src.Len()
		}
		src.WriteString(string(line))
	}
	source := []byte(src.String())

	// Parse AST.
	tree := mdInputParser.Parse(text.NewReader(source))

	// Collect hidden delimiter ranges and styled content ranges.
	var hidden []byteRange
	var styled []styledByteRange

	collectInlineNodes(tree, source, &hidden, &styled, mdNone)

	if len(hidden) == 0 && len(styled) == 0 {
		return nil
	}

	// Sort hidden ranges by start.
	sort.Slice(hidden, func(i, j int) bool {
		return hidden[i].start < hidden[j].start
	})

	// Build per-line mapping.
	result := &mdDisplay{
		lines: make([]mdLine, len(lines)),
	}

	hiddenSet := buildHiddenSet(hidden, len(source))

	for lineIdx := range lines {
		lineStart := lineOffsets[lineIdx]
		lineEnd := lineStart + len(string(lines[lineIdx]))

		ml := buildMdLine(lines[lineIdx], lineStart, lineEnd, hiddenSet, styled)
		result.lines[lineIdx] = ml
	}

	return result
}

// buildHiddenSet creates a boolean slice marking hidden byte positions.
func buildHiddenSet(hidden []byteRange, srcLen int) []bool {
	set := make([]bool, srcLen)
	for _, hr := range hidden {
		for i := hr.start; i < hr.end && i < srcLen; i++ {
			set[i] = true
		}
	}
	return set
}

// buildMdLine constructs the rawToDisp/dispToRaw mapping and display regions
// for a single line.
func buildMdLine(line []rune, lineStart, lineEnd int, hiddenSet []bool, styled []styledByteRange) mdLine {
	rawLen := len(line)
	rawToDisp := make([]int, rawLen+1) // +1 for end-of-line position

	dispToRaw := make([]int, 0, rawLen)
	dispCol := 0

	// Walk rune positions, mapping raw → display.
	byteOff := lineStart
	for rawCol := 0; rawCol < rawLen; rawCol++ {
		r := line[rawCol]
		runeBytes := len(string(r))

		// Check if all bytes of this rune are hidden.
		allHidden := true
		for b := byteOff; b < byteOff+runeBytes && b < len(hiddenSet); b++ {
			if !hiddenSet[b] {
				allHidden = false
				break
			}
		}

		if allHidden {
			rawToDisp[rawCol] = -1
		} else {
			rawToDisp[rawCol] = dispCol
			dispToRaw = append(dispToRaw, rawCol)
			dispCol++
		}
		byteOff += runeBytes
	}
	rawToDisp[rawLen] = dispCol // end-of-line maps to one past last display col

	// Build display regions from styled byte ranges that overlap this line.
	var regions []mdStyleRegion
	for _, sbr := range styled {
		// Skip ranges that don't overlap this line.
		if sbr.end <= lineStart || sbr.start >= lineEnd {
			continue
		}
		// Clamp to line boundaries.
		cs := max(sbr.start, lineStart)
		ce := min(sbr.end, lineEnd)

		// Convert byte offsets to raw rune columns.
		startRawCol := byteOffsetToRuneCol(line, lineStart, cs)
		endRawCol := byteOffsetToRuneCol(line, lineStart, ce)

		// Convert raw columns to display columns.
		startDisp := rawColToDisplayClamped(rawToDisp, startRawCol)
		endDisp := rawColToDisplayClamped(rawToDisp, endRawCol)

		if startDisp < endDisp {
			regions = append(regions, mdStyleRegion{
				startCol: startDisp,
				endCol:   endDisp,
				kind:     sbr.kind,
			})
		}
	}

	sort.Slice(regions, func(i, j int) bool {
		return regions[i].startCol < regions[j].startCol
	})

	return mdLine{
		rawToDisp: rawToDisp,
		dispToRaw: dispToRaw,
		regions:   regions,
	}
}

// byteOffsetToRuneCol converts an absolute byte offset to a rune column within
// the line. lineStart is the byte offset where the line begins in the source.
func byteOffsetToRuneCol(line []rune, lineStart, byteOff int) int {
	target := byteOff - lineStart
	pos := 0
	for i, r := range line {
		if pos >= target {
			return i
		}
		pos += len(string(r))
	}
	return len(line)
}

// rawColToDisplayClamped converts a raw column to a display column, snapping
// to the nearest visible position if the raw column maps to -1 (hidden).
func rawColToDisplayClamped(rawToDisp []int, rawCol int) int {
	if rawCol >= len(rawToDisp) {
		return rawToDisp[len(rawToDisp)-1]
	}
	d := rawToDisp[rawCol]
	if d >= 0 {
		return d
	}
	// Snap forward to next visible.
	for i := rawCol + 1; i < len(rawToDisp); i++ {
		if rawToDisp[i] >= 0 {
			return rawToDisp[i]
		}
	}
	// Snap backward.
	for i := rawCol - 1; i >= 0; i-- {
		if rawToDisp[i] >= 0 {
			return rawToDisp[i]
		}
	}
	return 0
}

// rawColToDisplay converts a raw column to a display column.
// If the raw column is on a hidden delimiter, snaps to the nearest visible position.
func (ml *mdLine) rawColToDisplay(rawCol int) int {
	return rawColToDisplayClamped(ml.rawToDisp, rawCol)
}

// displayColToRaw converts a display column to the corresponding raw column.
func (ml *mdLine) displayColToRaw(dispCol int) int {
	if dispCol >= len(ml.dispToRaw) {
		// Past end → end-of-line raw position.
		if len(ml.dispToRaw) == 0 {
			return 0
		}
		return ml.dispToRaw[len(ml.dispToRaw)-1] + 1
	}
	return ml.dispToRaw[dispCol]
}

// regionAt returns the style kind at a display column, using binary search.
func (ml *mdLine) regionAt(dispCol int) mdStyleKind {
	idx := sort.Search(len(ml.regions), func(i int) bool {
		return ml.regions[i].endCol > dispCol
	})
	if idx < len(ml.regions) && ml.regions[idx].startCol <= dispCol {
		return ml.regions[idx].kind
	}
	return mdNone
}

// displayRunes returns the visible rune array for a line, filtering out hidden
// delimiter characters.
func (md *mdDisplay) displayRunes(lineIdx int, rawLine []rune) []rune {
	if lineIdx >= len(md.lines) {
		return rawLine
	}
	ml := md.lines[lineIdx]
	if len(ml.dispToRaw) == len(rawLine) {
		return rawLine // no hidden chars
	}
	result := make([]rune, len(ml.dispToRaw))
	for i, rawCol := range ml.dispToRaw {
		result[i] = rawLine[rawCol]
	}
	return result
}

// collectInlineNodes walks the AST recursively, collecting hidden delimiter
// byte ranges and styled content byte ranges.
func collectInlineNodes(node ast.Node, source []byte, hidden *[]byteRange, styled *[]styledByteRange, inheritedKind mdStyleKind) {
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		switch n := child.(type) {
		case *ast.Emphasis:
			kind := mdItalic
			if n.Level >= 2 {
				kind = mdBold
			}
			// Handle nesting: bold inside italic or vice versa.
			if inheritedKind == mdItalic && kind == mdBold {
				kind = mdBoldItalic
			}
			if inheritedKind == mdBold && kind == mdItalic {
				kind = mdBoldItalic
			}

			addEmphasisDelimiters(n, source, n.Level, hidden)
			addContentRanges(n, source, styled, kind)
			collectInlineNodes(n, source, hidden, styled, kind)

		case *ast.CodeSpan:
			addCodeSpanDelimiters(n, source, hidden)
			addContentRanges(n, source, styled, mdCode)

		case *east.Strikethrough:
			addStrikethroughDelimiters(n, source, hidden)
			addContentRanges(n, source, styled, mdStrike)

		case *ast.Link:
			addLinkDelimiters(n, source, hidden)
			addContentRanges(n, source, styled, mdLink)

		case *ast.Paragraph:
			collectInlineNodes(n, source, hidden, styled, inheritedKind)

		default:
			collectInlineNodes(child, source, hidden, styled, inheritedKind)
		}
	}
}

// addEmphasisDelimiters finds and marks the delimiter bytes (*, _) around emphasis.
// Only processes top-level emphasis nodes to correctly handle nesting like ***.
func addEmphasisDelimiters(n *ast.Emphasis, source []byte, level int, hidden *[]byteRange) {
	// Skip if this emphasis is a child of another emphasis — the parent handles all delimiters.
	if _, ok := n.Parent().(*ast.Emphasis); ok {
		return
	}

	// Find first and last text segments (recursive).
	firstStart, lastEnd := nodeByteExtent(n, source)
	if firstStart < 0 {
		return
	}

	// Compute total delimiter width by summing levels through nested emphasis.
	totalLevel := totalEmphasisLevel(n)

	// Opening delimiter.
	openStart := firstStart - totalLevel
	if openStart >= 0 {
		*hidden = append(*hidden, byteRange{openStart, firstStart})
	}

	// Closing delimiter.
	closeEnd := lastEnd + totalLevel
	if closeEnd <= len(source) {
		*hidden = append(*hidden, byteRange{lastEnd, closeEnd})
	}
}

// totalEmphasisLevel sums the emphasis levels through nested emphasis children.
func totalEmphasisLevel(n *ast.Emphasis) int {
	total := n.Level
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		if em, ok := child.(*ast.Emphasis); ok {
			total += totalEmphasisLevel(em)
		}
	}
	return total
}

// addCodeSpanDelimiters finds and marks the backtick delimiters.
func addCodeSpanDelimiters(n *ast.CodeSpan, source []byte, hidden *[]byteRange) {
	firstStart, lastEnd := nodeByteExtent(n, source)
	if firstStart < 0 {
		return
	}

	// Scan backward for backticks.
	openEnd := firstStart
	openStart := openEnd
	for openStart > 0 && source[openStart-1] == '`' {
		openStart--
	}
	if openStart < openEnd {
		*hidden = append(*hidden, byteRange{openStart, openEnd})
	}

	// Scan forward for backticks.
	closeStart := lastEnd
	closeEnd := closeStart
	for closeEnd < len(source) && source[closeEnd] == '`' {
		closeEnd++
	}
	if closeStart < closeEnd {
		*hidden = append(*hidden, byteRange{closeStart, closeEnd})
	}
}

// addStrikethroughDelimiters marks the ~~ delimiters.
func addStrikethroughDelimiters(n *east.Strikethrough, source []byte, hidden *[]byteRange) {
	firstStart, lastEnd := nodeByteExtent(n, source)
	if firstStart < 0 {
		return
	}

	openStart := firstStart - 2
	if openStart >= 0 {
		*hidden = append(*hidden, byteRange{openStart, firstStart})
	}

	closeEnd := lastEnd + 2
	if closeEnd <= len(source) {
		*hidden = append(*hidden, byteRange{lastEnd, closeEnd})
	}
}

// addLinkDelimiters marks [, ](url) delimiters.
func addLinkDelimiters(n *ast.Link, source []byte, hidden *[]byteRange) {
	firstStart, lastEnd := nodeByteExtent(n, source)
	if firstStart < 0 {
		return
	}

	// Opening bracket.
	if firstStart > 0 && source[firstStart-1] == '[' {
		*hidden = append(*hidden, byteRange{firstStart - 1, firstStart})
	}

	// Closing ](url) — scan from lastEnd to find the closing paren.
	if lastEnd < len(source) && source[lastEnd] == ']' {
		closeStart := lastEnd
		closeEnd := closeStart + 1
		// Skip (url)
		if closeEnd < len(source) && source[closeEnd] == '(' {
			for closeEnd < len(source) && source[closeEnd] != ')' {
				closeEnd++
			}
			if closeEnd < len(source) {
				closeEnd++ // include ')'
			}
		}
		*hidden = append(*hidden, byteRange{closeStart, closeEnd})
	}
}

// addContentRanges adds styled byte ranges for the text segments of a node,
// excluding child nodes that have their own styling.
func addContentRanges(node ast.Node, source []byte, styled *[]styledByteRange, kind mdStyleKind) {
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		if t, ok := child.(*ast.Text); ok {
			seg := t.Segment
			*styled = append(*styled, styledByteRange{
				byteRange: byteRange{seg.Start, seg.Stop},
				kind:      kind,
			})
		}
	}
}

// nodeByteExtent returns the first byte start and last byte end of text
// segments under a node. Returns (-1, -1) if no text segments found.
func nodeByteExtent(node ast.Node, source []byte) (int, int) {
	_ = source // parameter used by callers for context
	first := -1
	last := -1
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		walkTextSegments(child, func(start, end int) {
			if first < 0 || start < first {
				first = start
			}
			if end > last {
				last = end
			}
		})
	}
	return first, last
}

// walkTextSegments visits all text segment byte ranges under a node.
func walkTextSegments(node ast.Node, fn func(start, end int)) {
	if t, ok := node.(*ast.Text); ok {
		fn(t.Segment.Start, t.Segment.Stop)
	}
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		walkTextSegments(child, fn)
	}
}
