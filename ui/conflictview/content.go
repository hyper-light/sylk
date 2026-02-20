package conflictview

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	codepkg "github.com/adalundhe/sylk/ui/code"
	"github.com/adalundhe/sylk/ui/editor/search"
	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// lineRegion classifies a line within the merged content.
type lineRegion int

const (
	lineNormal        lineRegion = iota // Outside any conflict hunk.
	lineMarkerOurs                      // <<<<<<< marker line.
	lineOursContent                     // Ours content between <<<<<<< and =======.
	lineMarkerDiv                       // ======= marker line.
	lineTheirsContent                   // Theirs content between ======= and >>>>>>>.
	lineMarkerTheirs                    // >>>>>>> marker line.
)

// zeroHash is the hex string for a zero blob hash.
const zeroHash = "0000000000000000000000000000000000000000"

// classifyLine determines the region for a 0-indexed line number using
// binary search on the sorted, non-overlapping hunks slice.
func classifyLine(line int, hunks []ConflictHunk) lineRegion {
	idx := sort.Search(len(hunks), func(i int) bool {
		return hunks[i].EndLine >= line
	})
	if idx >= len(hunks) {
		return lineNormal
	}
	h := &hunks[idx]
	if line < h.StartLine {
		return lineNormal
	}
	if line == h.StartLine {
		return lineMarkerOurs
	}
	if line < h.DivLine {
		return lineOursContent
	}
	if line == h.DivLine {
		return lineMarkerDiv
	}
	if line < h.EndLine {
		return lineTheirsContent
	}
	return lineMarkerTheirs
}

// renderStyles holds precomputed styles for a single content render frame.
type renderStyles struct {
	gutterW        int
	gutterSt       lipgloss.Style
	oursMarkerSt   lipgloss.Style
	divMarkerSt    lipgloss.Style
	theirsMarkerSt lipgloss.Style
	ctxSt          lipgloss.Style
	warnSt         lipgloss.Style
}

// newRenderStyles builds the style set from the current theme.
func (m *Model) newRenderStyles() renderStyles {
	p := m.theme.Palette
	return renderStyles{
		gutterW:        m.gutterWidth(),
		gutterSt:       lipgloss.NewStyle().Foreground(p.Muted),
		oursMarkerSt:   lipgloss.NewStyle().Foreground(p.Secondary).Bold(true),
		divMarkerSt:    lipgloss.NewStyle().Foreground(p.Border).Bold(true),
		theirsMarkerSt: lipgloss.NewStyle().Foreground(p.Warning).Bold(true),
		ctxSt:          lipgloss.NewStyle().Foreground(p.Muted),
		warnSt:         lipgloss.NewStyle().Foreground(p.Warning).Bold(true),
	}
}

// renderContent renders the viewport-aware merged content with syntax
// highlighting, conflict region tinting, inline action lines, line numbers,
// and find match overlay.
func (m *Model) renderContent(vpH int) string {
	if !m.hasSelectedFile() {
		return m.renderEmpty(vpH)
	}
	if len(m.mergedLines) == 0 {
		return m.renderNonContentConflict(m.selectedEntry(), vpH)
	}
	return m.renderMergedContent(vpH)
}

// hasSelectedFile reports whether a valid file is selected.
func (m *Model) hasSelectedFile() bool {
	return m.selectedFile >= 0 && m.selectedFile < len(m.data.Entries)
}

// renderMergedContent renders merged content lines with conflict markers.
func (m *Model) renderMergedContent(vpH int) string {
	w := m.width
	m.clampDisplayScroll(vpH)
	start, end := m.displayRange(vpH)
	rs := m.newRenderStyles()
	matches, matchIdx := m.findMatchState()

	m.actionTargets = m.actionTargets[:0]
	lines := make([]string, 0, vpH)
	lines = appendBounded(lines, m.renderStepPreviewHeader(w), vpH)

	for d := start; d < end && len(lines) < vpH; d++ {
		lines = m.appendDisplayRow(lines, d, vpH, w, rs, matches, matchIdx)
	}
	return strings.Join(padToHeight(lines, vpH, w), "\n")
}

// clampDisplayScroll clamps scrollOffset to valid display bounds.
func (m *Model) clampDisplayScroll(vpH int) {
	displayTotal := m.totalContentLines()
	m.scrollOffset = max(min(m.scrollOffset, max(displayTotal-vpH, 0)), 0)
}

// displayRange returns the [start, end) display line range for the viewport.
func (m *Model) displayRange(vpH int) (int, int) {
	displayTotal := m.totalContentLines()
	end := min(m.scrollOffset+vpH, displayTotal)
	return min(m.scrollOffset, end), end
}

// findMatchState returns find bar matches and current index, or nil/0.
func (m *Model) findMatchState() ([]search.MatchRange, int) {
	if m.findActive && m.findBar != nil {
		return m.findBar.Matches(), m.findBar.MatchIndex()
	}
	return nil, 0
}

// appendDisplayRow appends rendered row(s) for display index d.
func (m *Model) appendDisplayRow(lines []string, d, vpH, w int, rs renderStyles, matches []search.MatchRange, matchIdx int) []string {
	contentLine, isAction, hunkIdx := m.displayToContent(d)
	if isAction {
		return m.appendActionRow(lines, hunkIdx, vpH, w, rs.gutterW)
	}
	return append(lines, m.renderContentLine(contentLine, w, rs, matches, matchIdx))
}

// appendActionRow appends base context (if visible) and the action line.
func (m *Model) appendActionRow(lines []string, hunkIdx, vpH, w, gutterW int) []string {
	lines = appendBounded(lines, m.renderBaseSection(hunkIdx, w, gutterW), vpH)
	rendered, target := m.renderActionLine(hunkIdx, w, gutterW)
	target.screenY = len(lines)
	m.actionTargets = append(m.actionTargets, target)
	return append(lines, padLine(rendered, w))
}

// renderContentLine renders a single content line with gutter and highlights.
func (m *Model) renderContentLine(contentLine, w int, rs renderStyles, matches []search.MatchRange, matchIdx int) string {
	gutter := m.renderGutter(contentLine, rs)
	body := m.renderLineByRegion(m.mergedLines[contentLine], contentLine, rs, matches, matchIdx)
	return padLine(gutter+body, w)
}

// renderGutter renders the line number gutter with optional regression marker.
func (m *Model) renderGutter(contentLine int, rs renderStyles) string {
	numStr := fmt.Sprintf("%*d", rs.gutterW, contentLine+1)
	suffix := " "
	if m.hasRegressionAtLine(contentLine) {
		suffix = rs.warnSt.Render("!")
	}
	return rs.gutterSt.Render(numStr) + suffix
}

// renderLineByRegion dispatches to marker or highlighted content rendering.
func (m *Model) renderLineByRegion(line string, lineIdx int, rs renderStyles, matches []search.MatchRange, matchIdx int) string {
	if rendered, ok := m.renderMarkerLine(line, lineIdx, rs); ok {
		return rendered
	}
	return m.renderHighlightedContentLine(line, lineIdx, rs, matches, matchIdx)
}

// renderMarkerLine renders conflict marker lines. Returns ("", false) for
// non-marker lines.
func (m *Model) renderMarkerLine(line string, lineIdx int, rs renderStyles) (string, bool) {
	switch classifyLine(lineIdx, m.hunks) {
	case lineMarkerOurs:
		return rs.oursMarkerSt.Render("<<<<<<< "+m.oursLabel) + renderCtxSuffix(m.oursCtx, rs.ctxSt), true
	case lineMarkerDiv:
		return rs.divMarkerSt.Render(line), true
	case lineMarkerTheirs:
		return rs.theirsMarkerSt.Render(">>>>>>> "+m.theirsLabel) + renderCtxSuffix(m.theirsCtx, rs.ctxSt), true
	default:
		return "", false
	}
}

// renderHighlightedContentLine renders a non-marker line with syntax
// highlighting and optional find match overlay.
func (m *Model) renderHighlightedContentLine(line string, lineIdx int, rs renderStyles, matches []search.MatchRange, matchIdx int) string {
	region := classifyLine(lineIdx, m.hunks)
	var lineRegions []codepkg.HighlightRegion
	if lineIdx < len(m.highlightRegions) {
		lineRegions = m.highlightRegions[lineIdx]
	}
	return m.renderHighlightedLine(line, lineIdx, lineRegions, region, matches, matchIdx, m.theme.Palette)
}

// appendBounded appends lines from text (split by newline) to dst,
// stopping when len(dst) reaches limit. Returns dst unchanged if text is empty.
func appendBounded(dst []string, text string, limit int) []string {
	if text == "" {
		return dst
	}
	parts := strings.Split(text, "\n")
	room := max(limit-len(dst), 0)
	return append(dst, parts[:min(len(parts), room)]...)
}

// padToHeight pads lines to exactly height rows with blank lines.
func padToHeight(lines []string, height, width int) []string {
	blank := strings.Repeat(" ", width)
	for len(lines) < height {
		lines = append(lines, blank)
	}
	return lines
}

// renderActionLine renders the clickable action line above a conflict hunk
// with contextual labels, and returns the click target metadata.
// The gutterW parameter aligns the action content with the code gutter.
// Format: "          Accept Dest | Accept Source | Accept Both"
func (m *Model) renderActionLine(hunkIdx, width, gutterW int) (string, actionTarget) {
	p := m.theme.Palette
	linkSt := lipgloss.NewStyle().Foreground(p.Primary).Underline(true)
	hoverLinkSt := lipgloss.NewStyle().Foreground(p.Warning).Underline(true).Bold(true)
	sepSt := lipgloss.NewStyle().Foreground(p.Muted)

	target := actionTarget{hunkIdx: hunkIdx}

	// Per-button styles: default link, override on hover.
	oursSt, theirsSt, bothSt := linkSt, linkSt, linkSt
	if m.hoverActionHunk == hunkIdx {
		switch m.hoverActionZone {
		case 0:
			oursSt = hoverLinkSt
		case 1:
			theirsSt = hoverLinkSt
		case 2:
			bothSt = hoverLinkSt
		}
	}

	var b strings.Builder
	col := gutterW + 1 // blank gutter + separator, matching content indent
	b.WriteString(strings.Repeat(" ", col))

	oursText := oursSt.Render("Accept " + m.oursLabel)
	oursW := lipgloss.Width(oursText)
	target.oursRange = [2]int{col, col + oursW}
	b.WriteString(oursText)
	col += oursW

	sep := sepSt.Render(" | ")
	sepW := lipgloss.Width(sep)
	b.WriteString(sep)
	col += sepW

	theirsText := theirsSt.Render("Accept " + m.theirsLabel)
	theirsW := lipgloss.Width(theirsText)
	target.theirsRange = [2]int{col, col + theirsW}
	b.WriteString(theirsText)
	col += theirsW

	b.WriteString(sep)
	col += sepW

	bothText := bothSt.Render("Accept Both")
	bothW := lipgloss.Width(bothText)
	target.bothRange = [2]int{col, col + bothW}
	b.WriteString(bothText)

	// Auto-resolve badge when hunk is auto-resolvable.
	if hunkIdx >= 0 && hunkIdx < len(m.hunks) && m.hunks[hunkIdx].AutoResolved {
		kindIdx := int(m.hunks[hunkIdx].AutoKind)
		label := "auto"
		if kindIdx >= 0 && kindIdx < len(autoResolveKindLabels) {
			label = autoResolveKindLabels[kindIdx]
		}
		autoSt := lipgloss.NewStyle().Foreground(p.Teal)
		b.WriteString("  ")
		b.WriteString(autoSt.Render("[auto: " + label + "]"))
	}

	return b.String(), target
}

// renderHighlightedLine renders a content line with syntax highlighting,
// optional conflict background tinting, and find match overlay.
func (m *Model) renderHighlightedLine(
	line string,
	lineIdx int,
	regions []codepkg.HighlightRegion,
	region lineRegion,
	matches []search.MatchRange,
	curMatchIdx int,
	p theme.Palette,
) string {
	// Determine background tint for conflict regions.
	var bgColor lipgloss.Color
	hasBg := false
	switch region {
	case lineOursContent:
		bgColor = p.DiffAddBg
		hasBg = true
	case lineTheirsContent:
		bgColor = p.DiffDelBg
		hasBg = true
	}

	// Check for find matches on this line.
	lineMatches := m.findMatchesOnLine(lineIdx, matches)

	if len(lineMatches) == 0 {
		if hasBg {
			return m.highlighter.HighlightLineWithSelection(line, regions, 0, len(line), bgColor)
		}
		return m.highlighter.HighlightLine(line, lineIdx, regions)
	}

	// Has find matches — render segments with match highlights.
	return m.renderLineWithMatches(line, regions, lineMatches,
		curMatchIdx, hasBg, bgColor, p)
}

// lineMatch describes a find match's byte range on a single line.
type lineMatch struct {
	byteStart int // byte offset within the line
	byteEnd   int // byte offset within the line (exclusive)
	globalIdx int // index into findBar.Matches()
}

// findMatchesOnLine returns match byte ranges that intersect the given line.
func (m *Model) findMatchesOnLine(lineIdx int, matches []search.MatchRange) []lineMatch {
	if len(matches) == 0 || lineIdx >= len(m.runeOffsetPerLine) {
		return nil
	}

	lineRuneStart := m.runeOffsetPerLine[lineIdx]
	lineRuneEnd := lineRuneStart + utf8.RuneCountInString(m.mergedLines[lineIdx])

	// Binary search for first match that could overlap this line.
	startIdx := sort.Search(len(matches), func(i int) bool {
		return matches[i].End > lineRuneStart
	})

	var result []lineMatch
	for i := startIdx; i < len(matches); i++ {
		mr := matches[i]
		if mr.Start >= lineRuneEnd {
			break
		}

		// Overlap in rune offsets relative to line start.
		overlapRuneStart := max(mr.Start-lineRuneStart, 0)
		overlapRuneEnd := min(mr.End-lineRuneStart, lineRuneEnd-lineRuneStart)

		// Convert rune offsets to byte offsets within the line.
		byteStart := runeOffsetToByteOffset(m.mergedLines[lineIdx], overlapRuneStart)
		byteEnd := runeOffsetToByteOffset(m.mergedLines[lineIdx], overlapRuneEnd)

		result = append(result, lineMatch{
			byteStart: byteStart,
			byteEnd:   byteEnd,
			globalIdx: i,
		})
	}
	return result
}

// runeOffsetToByteOffset converts a rune offset to a byte offset in a string.
func runeOffsetToByteOffset(s string, runeOffset int) int {
	byteOff := 0
	for i := 0; i < runeOffset && byteOff < len(s); i++ {
		_, size := utf8.DecodeRuneInString(s[byteOff:])
		byteOff += size
	}
	return byteOff
}

// renderLineWithMatches renders a line with syntax highlighting and find match
// overlays. Splits the line at match boundaries and renders each segment with
// the appropriate background: match bg for matches, conflict bg for conflict
// regions, or no bg for normal lines.
func (m *Model) renderLineWithMatches(
	line string,
	regions []codepkg.HighlightRegion,
	lineMatches []lineMatch,
	curMatchIdx int,
	hasBg bool,
	bgColor lipgloss.Color,
	p theme.Palette,
) string {
	lineLen := len(line)
	if lineLen == 0 {
		return ""
	}

	var b strings.Builder
	b.Grow(lineLen * 3)

	cursor := 0
	for _, lm := range lineMatches {
		mEnd := min(lm.byteEnd, lineLen)

		// Gap before this match.
		if cursor < lm.byteStart {
			seg := line[cursor:lm.byteStart]
			segRegions := offsetRegions(regions, cursor, lm.byteStart)
			if hasBg {
				b.WriteString(m.highlighter.HighlightLineWithSelection(seg, segRegions, 0, len(seg), bgColor))
			} else {
				b.WriteString(m.highlighter.HighlightLine(seg, 0, segRegions))
			}
		}

		// The match itself.
		matchBg := p.Primary
		if lm.globalIdx == curMatchIdx {
			matchBg = p.Warning
		}
		matchSeg := line[lm.byteStart:mEnd]
		matchRegions := offsetRegions(regions, lm.byteStart, mEnd)
		b.WriteString(m.highlighter.HighlightLineWithSelection(matchSeg, matchRegions, 0, len(matchSeg), matchBg))
		cursor = mEnd
	}

	// Trailing content after last match.
	if cursor < lineLen {
		seg := line[cursor:]
		segRegions := offsetRegions(regions, cursor, lineLen)
		if hasBg {
			b.WriteString(m.highlighter.HighlightLineWithSelection(seg, segRegions, 0, len(seg), bgColor))
		} else {
			b.WriteString(m.highlighter.HighlightLine(seg, 0, segRegions))
		}
	}

	return b.String()
}

// offsetRegions extracts and adjusts highlight regions for a sub-slice of
// line[segStart:segEnd], shifting byte offsets to be relative to the segment.
func offsetRegions(regions []codepkg.HighlightRegion, segStart, segEnd int) []codepkg.HighlightRegion {
	var result []codepkg.HighlightRegion
	for _, r := range regions {
		if r.EndCol <= segStart || r.StartCol >= segEnd {
			continue
		}
		result = append(result, codepkg.HighlightRegion{
			StartCol: max(r.StartCol-segStart, 0),
			EndCol:   min(r.EndCol-segStart, segEnd-segStart),
			Category: r.Category,
		})
	}
	return result
}

// renderBaseSection renders the ancestor (base) content for a hunk when
// the three-way base view is active. Returns empty string if unavailable.
func (m *Model) renderBaseSection(hunkIdx, width, gutterW int) string {
	if !m.baseVisible {
		return ""
	}
	lines := m.baseHunkLines(hunkIdx)
	if len(lines) == 0 {
		return ""
	}
	return m.formatBaseSection(lines, width, gutterW)
}

// baseHunkLines returns the base content lines windowed around a hunk.
func (m *Model) baseHunkLines(hunkIdx int) []string {
	content := m.selectedFileBaseContent()
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	lines = m.clipToHunkRegion(lines, hunkIdx)
	return capSlice(lines, 8) // max base lines
}

// selectedFileBaseContent returns the cached base content for the selected file.
func (m *Model) selectedFileBaseContent() string {
	e := m.selectedEntry()
	if e == nil || m.baseContent == nil {
		return ""
	}
	return m.baseContent[e.Path]
}

// clipToHunkRegion narrows lines to the region around a specific hunk.
func (m *Model) clipToHunkRegion(lines []string, hunkIdx int) []string {
	if hunkIdx < 0 || hunkIdx >= len(m.hunks) {
		return lines
	}
	h := m.hunks[hunkIdx]
	start := max(h.StartLine-1, 0)
	end := min(h.EndLine+1, len(lines))
	if start >= len(lines) {
		return lines
	}
	return lines[start:end]
}

// capSlice returns at most maxLen elements from the front of s.
func capSlice(s []string, maxLen int) []string {
	if len(s) > maxLen {
		return s[:maxLen]
	}
	return s
}

// formatBaseSection renders a labeled base-content block.
func (m *Model) formatBaseSection(lines []string, width, gutterW int) string {
	p := m.theme.Palette
	baseBgSt := lipgloss.NewStyle().Foreground(p.Muted).Italic(true)
	labelSt := lipgloss.NewStyle().Foreground(p.Teal).Bold(true)
	indent := strings.Repeat(" ", gutterW+1)

	result := make([]string, 0, len(lines)+1)
	result = append(result, padLine(indent+labelSt.Render("Base"), width))
	for _, l := range lines {
		result = append(result, padLine(indent+baseBgSt.Render(l), width))
	}
	return strings.Join(result, "\n")
}

// renderStepPreviewHeader renders a compact header showing the commit being
// applied and its changed files when the step preview overlay is active.
func (m *Model) renderStepPreviewHeader(width int) string {
	if !m.stepPreviewVisible || m.stepPreview == nil {
		return ""
	}
	return m.formatStepPreview(width)
}

// formatStepPreview builds the step preview header lines.
func (m *Model) formatStepPreview(width int) string {
	p := m.theme.Palette
	hashSt := lipgloss.NewStyle().Foreground(p.Primary).Bold(true)
	subjSt := lipgloss.NewStyle().Foreground(p.Foreground)

	lines := make([]string, 0, len(m.stepPreview.FileDiffs)+2)
	lines = append(lines, padLine("  "+hashSt.Render(m.stepPreview.Hash)+" "+subjSt.Render(m.stepPreview.Subject), width))
	for _, fd := range m.stepPreview.FileDiffs {
		lines = append(lines, m.formatFileDiffLine(fd, width))
	}
	lines = append(lines, padLine("", width))
	return strings.Join(lines, "\n")
}

// formatFileDiffLine renders a single file diff stat line for the step preview.
func (m *Model) formatFileDiffLine(fd FileDiffSummary, width int) string {
	p := m.theme.Palette
	fileSt := lipgloss.NewStyle().Foreground(p.Muted)
	nameSt := lipgloss.NewStyle().Foreground(p.Success)
	if m.stepPreview.ConflictPaths[fd.Path] {
		nameSt = lipgloss.NewStyle().Foreground(p.Error)
	}
	stat := fmt.Sprintf("+%d -%d", fd.Additions, fd.Deletions)
	return padLine("    "+nameSt.Render(fd.Path)+" "+fileSt.Render(stat), width)
}

// renderCtxSuffix renders " (context)" in muted style if ctx is non-empty.
func renderCtxSuffix(ctx string, st lipgloss.Style) string {
	if ctx == "" {
		return ""
	}
	return " " + st.Render("("+ctx+")")
}

// renderNonContentConflict renders a descriptive view for delete/modify and
// binary conflicts where there is no merged content with inline markers.
func (m *Model) renderNonContentConflict(entry *ConflictFileEntry, vpH int) string {
	p := m.theme.Palette
	w := m.width
	mutedSt := lipgloss.NewStyle().Foreground(p.Muted).Italic(true)
	boldSt := lipgloss.NewStyle().Foreground(p.Foreground).Bold(true)
	pathSt := lipgloss.NewStyle().Foreground(p.Primary)

	blank := strings.Repeat(" ", w)
	lines := make([]string, vpH)
	for i := range lines {
		lines[i] = blank
	}

	row := 1
	set := func(s string) {
		if row < vpH {
			lines[row] = padLine(s, w)
			row++
		}
	}

	set(padLine("  "+pathSt.Render(entry.Path), w))
	row++ // blank line

	switch entry.Type {
	case TypeDeleteModify:
		set(padLine("  "+boldSt.Render("Delete / Modify conflict"), w))
		row++
		oursDesc := "modified"
		if entry.OursHash == zeroHash {
			oursDesc = "deleted"
		}
		theirsDesc := "modified"
		if entry.TheirsHash == zeroHash {
			theirsDesc = "deleted"
		}
		set(padLine("  "+mutedSt.Render(m.oursLabel+": file "+oursDesc), w))
		set(padLine("  "+mutedSt.Render(m.theirsLabel+": file "+theirsDesc), w))
	case TypeBinary:
		set(padLine("  "+boldSt.Render("Binary file conflict"), w))
		row++
		set(padLine("  "+mutedSt.Render("Cannot display binary content."), w))
		set(padLine("  "+mutedSt.Render("Choose Ours or Theirs to resolve."), w))
	default:
		// Content/BothAdded with empty MergedContent (shouldn't happen, but handle gracefully).
		set(padLine("  "+mutedSt.Render("No content to display."), w))
	}

	// Show current resolution if set.
	if entry.Resolution != ResUnresolved {
		row++
		resLabel := m.resolutionLabel(entry.Resolution)
		resSt := lipgloss.NewStyle().Foreground(p.Success).Bold(true)
		set(padLine("  "+resSt.Render("Resolved: "+resLabel), w))
	}

	return strings.Join(lines, "\n")
}

// renderEmpty renders empty content when no file is selected.
func (m *Model) renderEmpty(vpH int) string {
	blank := strings.Repeat(" ", m.width)
	lines := make([]string, vpH)
	for i := range lines {
		lines[i] = blank
	}
	if vpH > 0 {
		mutedSt := lipgloss.NewStyle().Foreground(m.theme.Palette.Muted).Italic(true)
		lines[0] = padLine(mutedSt.Render(" No file selected"), m.width)
	}
	return strings.Join(lines, "\n")
}
