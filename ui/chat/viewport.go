package chat

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// selectionRegion defines a navigable sub-range within an entry.
// Region 0 always spans the entire entry (the "full message" view).
// Regions 1..N correspond to individual code blocks within the entry.
type selectionRegion struct {
	start int // First line (inclusive, entry-relative).
	end   int // Last line (exclusive, entry-relative).
}

// edgeFlashDurationTicks is how many ticks an edge flash persists.
// Derived from: 1000ms at 16ms per tick ≈ 63 ticks.
const edgeFlashDurationTicks = 63

// wrapBadgeWidth is the visible column width of the wrap indicator badge.
// Derived from: space(1) + icon(1) + space(1) = 3.
const wrapBadgeWidth = 3

// Viewport renders only visible chat entries using line-based virtual scrolling.
// It references a History ring buffer and renders entries on demand with caching.
// scrollOff tracks the number of rendered lines scrolled back from the bottom.
type Viewport struct {
	history     *History
	theme       *theme.Theme
	scrollOff   int  // Lines scrolled back from the bottom (0 = following).
	viewHeight  int
	viewWidth   int
	following       bool // Auto-scroll to bottom on new content.
	highlightID     string // Entry ID to highlight (empty = none).
	highlightStart  int    // First entry-relative line to highlight (inclusive).
	highlightEnd    int    // Last entry-relative line to highlight (exclusive).
	selectedIndex   int    // Logical history index of selected entry (-1 = none).
	selectedRegion  int    // Region index within the selected entry.
	edgeFlash       int    // Edge flash direction: -1 = top, 0 = none, 1 = bottom.
	edgeFlashTicks  int    // Remaining ticks for edge flash.
	bounceOffset    int    // Visual line displacement from overscroll bounce.
}

// NewViewport creates a Viewport bound to the given History.
func NewViewport(history *History, th *theme.Theme) *Viewport {
	return &Viewport{
		history:       history,
		theme:         th,
		following:     true,
		selectedIndex: -1,
	}
}

// SetSize updates the viewport dimensions.
func (vp *Viewport) SetSize(width, height int) {
	vp.viewWidth = max(width, 0)
	vp.viewHeight = max(height, 0)
}

// ScrollUp scrolls up by one line. Returns true if the scroll was applied,
// false if already at the top boundary.
func (vp *Viewport) ScrollUp() bool {
	vp.following = false
	prev := vp.scrollOff
	vp.scrollOff = min(vp.scrollOff+1, vp.maxScrollOffset())
	return vp.scrollOff != prev
}

// ScrollDown scrolls down by one line. Returns true if the scroll was applied,
// false if already at the bottom boundary.
func (vp *Viewport) ScrollDown() bool {
	prev := vp.scrollOff
	vp.scrollOff = max(vp.scrollOff-1, 0)
	vp.following = vp.scrollOff == 0
	return vp.scrollOff != prev
}

// pageOverlap is the number of lines retained between pages for context.
// Derived from: standard pager convention of 1 overlap line.
const pageOverlap = 1

// PageUp scrolls up by one page minus overlap.
func (vp *Viewport) PageUp() {
	vp.following = false
	step := max(vp.viewHeight-pageOverlap, 1)
	vp.scrollOff = min(vp.scrollOff+step, vp.maxScrollOffset())
}

// PageDown scrolls down by one page minus overlap.
func (vp *Viewport) PageDown() {
	step := max(vp.viewHeight-pageOverlap, 1)
	vp.scrollOff = max(vp.scrollOff-step, 0)
	vp.following = vp.scrollOff == 0
}

// ToTop scrolls to the oldest content and clears selection.
func (vp *Viewport) ToTop() {
	vp.following = false
	vp.scrollOff = vp.maxScrollOffset()
	vp.ClearSelection()
}

// ToBottom scrolls to the newest content, re-enables follow mode, and clears selection.
func (vp *Viewport) ToBottom() {
	vp.scrollOff = 0
	vp.following = true
	vp.ClearSelection()
}

// SelectUp moves the selection cursor to the previous region or entry.
// If no entry is selected, selects the newest entry at region 0 (full message).
func (vp *Viewport) SelectUp() {
	count := vp.history.Len()
	if count == 0 {
		return
	}
	if vp.selectedIndex < 0 {
		vp.selectEntry(count-1, 0)
		return
	}
	vp.advanceSelection(-1)
}

// SelectDown moves the selection cursor to the next region or entry.
// If no entry is selected, selects the first region of the oldest entry.
func (vp *Viewport) SelectDown() {
	count := vp.history.Len()
	if count == 0 {
		return
	}
	if vp.selectedIndex < 0 {
		vp.selectEntry(0, 0)
		return
	}
	vp.advanceSelection(1)
}

// advanceSelection moves the selection by delta (+1 = down, -1 = up).
// Region 0 (full message) is the entry point. From there, subsequent
// steps move through code block regions in direction order, skipping
// back to region 0. When code blocks are exhausted, it crosses to the
// adjacent entry.
func (vp *Viewport) advanceSelection(delta int) {
	regions := vp.regions(vp.selectedIndex)

	// From the full-message region, step into code blocks.
	if vp.selectedRegion == 0 {
		if len(regions) <= 1 {
			vp.crossEntryBoundary(delta)
			return
		}
		if delta > 0 {
			vp.selectEntry(vp.selectedIndex, 1)
		} else {
			vp.selectEntry(vp.selectedIndex, len(regions)-1)
		}
		return
	}

	// Within code block regions (1..N), step linearly and skip region 0.
	next := vp.selectedRegion + delta
	if next >= 1 && next < len(regions) {
		vp.selectEntry(vp.selectedIndex, next)
		return
	}
	vp.crossEntryBoundary(delta)
}

// crossEntryBoundary moves selection to the adjacent entry in the given
// direction, always entering at region 0 (full message).
// At the edges of history, the first press shows an edge flash. A second
// press in the same direction while the flash is active wraps around.
func (vp *Viewport) crossEntryBoundary(delta int) {
	nextEntry := vp.selectedIndex + delta
	if nextEntry < 0 || nextEntry >= vp.history.Len() {
		if vp.edgeFlash == delta && vp.edgeFlashTicks > 0 {
			vp.wrapSelection(delta)
			return
		}
		vp.flashEdge(delta)
		return
	}
	vp.selectEntry(nextEntry, 0)
}

// wrapSelection jumps the selection to the opposite end of history.
func (vp *Viewport) wrapSelection(delta int) {
	if delta < 0 {
		vp.selectEntry(vp.history.Len()-1, 0)
	} else {
		vp.selectEntry(0, 0)
	}
}

// selectEntry sets the selection to a specific entry and region, then
// scrolls the viewport to ensure the selected region is visible.
// Clears any active edge flash since the selection moved.
func (vp *Viewport) selectEntry(entryIdx, regionIdx int) {
	vp.selectedIndex = entryIdx
	vp.selectedRegion = regionIdx
	vp.clearEdgeFlash()
	vp.scrollToSelected()
}

// ClearSelection deselects any selected entry and clears edge flash.
func (vp *Viewport) ClearSelection() {
	vp.selectedIndex = -1
	vp.selectedRegion = 0
	vp.clearEdgeFlash()
}

// flashEdge activates the edge flash in the given direction (-1 top, +1 bottom).
func (vp *Viewport) flashEdge(direction int) {
	vp.edgeFlash = direction
	vp.edgeFlashTicks = edgeFlashDurationTicks
}

// clearEdgeFlash cancels any active edge flash.
func (vp *Viewport) clearEdgeFlash() {
	vp.edgeFlash = 0
	vp.edgeFlashTicks = 0
}

// TickEdgeFlash decrements the edge flash countdown and clears when expired.
func (vp *Viewport) TickEdgeFlash() {
	if vp.edgeFlashTicks <= 0 {
		return
	}
	vp.edgeFlashTicks--
	if vp.edgeFlashTicks == 0 {
		vp.edgeFlash = 0
	}
}

// HasEdgeFlash reports whether an edge flash animation is active.
func (vp *Viewport) HasEdgeFlash() bool {
	return vp.edgeFlashTicks > 0
}

// EdgeFlashTicks returns the remaining flash ticks for dirty-check comparison.
func (vp *Viewport) EdgeFlashTicks() int {
	return vp.edgeFlashTicks
}

// SelectRegionContaining sets the selection to the region of the given entry
// that contains the specified line. Prefers a code block region (1+) over
// the full-message region (0). Used to align selection with copy-highlight.
func (vp *Viewport) SelectRegionContaining(entryIndex, line int) {
	regions := vp.regions(entryIndex)
	for i := 1; i < len(regions); i++ {
		r := regions[i]
		if line >= r.start && line < r.end {
			vp.selectedIndex = entryIndex
			vp.selectedRegion = i
			return
		}
	}
	vp.selectedIndex = entryIndex
	vp.selectedRegion = 0
}

// SelectedIndex returns the logical index of the selected entry, or -1 if none.
func (vp *Viewport) SelectedIndex() int {
	return vp.selectedIndex
}

// AdjustSelectionForEviction decrements the selection index after the ring
// buffer evicts its oldest entry. If the selected entry was the one evicted,
// the selection is cleared.
func (vp *Viewport) AdjustSelectionForEviction() {
	if vp.selectedIndex < 0 {
		return
	}
	vp.selectedIndex--
}

// scrollToSelected adjusts scrollOff so the selected region is fully visible.
func (vp *Viewport) scrollToSelected() {
	if vp.selectedIndex < 0 {
		return
	}
	entryStart, _ := vp.entryLineRange(vp.selectedIndex)
	regions := vp.regions(vp.selectedIndex)
	if vp.selectedRegion >= len(regions) {
		return
	}
	r := regions[vp.selectedRegion]
	vp.scrollRangeIntoView(entryStart+r.start, entryStart+r.end)
}

// scrollRangeIntoView adjusts scrollOff so the absolute line range
// [rangeStart, rangeEnd) is visible within the viewport.
// When the range is taller than the viewport, the top is preferred.
func (vp *Viewport) scrollRangeIntoView(rangeStart, rangeEnd int) {
	total := vp.totalLines()
	bottom := total - vp.scrollOff
	top := bottom - vp.viewHeight

	if rangeEnd > bottom {
		vp.scrollOff = total - rangeEnd
		top = total - vp.scrollOff - vp.viewHeight
	}
	if rangeStart < top {
		vp.scrollOff = total - rangeStart - vp.viewHeight
	}
	vp.scrollOff = min(max(vp.scrollOff, 0), vp.maxScrollOffset())
	vp.following = vp.scrollOff == 0
}

// entryLineRange returns the absolute start line (inclusive) and end line
// (exclusive) for the entry at the given logical index.
func (vp *Viewport) entryLineRange(index int) (start, end int) {
	cumulative := 0
	for i := range vp.history.Len() {
		h := vp.entryHeight(i)
		if i == index {
			return cumulative, cumulative + h
		}
		cumulative += h
	}
	return 0, 0
}

// regions returns the navigable regions for an entry, ensuring it is
// rendered first so CodeRegions are populated.
func (vp *Viewport) regions(index int) []selectionRegion {
	vp.entryHeight(index)
	entry := vp.history.Get(index)
	if entry == nil {
		return nil
	}
	return entryRegions(entry)
}

// lastRegion returns the index of the last region for an entry.
func (vp *Viewport) lastRegion(index int) int {
	return max(len(vp.regions(index))-1, 0)
}

// entryRegions computes the navigable regions for an entry.
// Region 0 is always the full message (all lines). Regions 1..N are
// individual code blocks. Entries without code blocks have only region 0.
func entryRegions(entry *ChatEntry) []selectionRegion {
	if entry.Height <= 0 {
		return nil
	}
	regions := []selectionRegion{{start: 0, end: entry.Height}}
	for _, cr := range entry.CodeRegions {
		regions = append(regions, selectionRegion{start: cr.Start, end: cr.End})
	}
	return regions
}

// OnNewEntry should be called when a new entry is pushed to History.
// If following, scroll offset stays at zero. Otherwise, the offset is
// increased by the new entry's line count so the user's view stays stable.
func (vp *Viewport) OnNewEntry() {
	if vp.following {
		return
	}
	newIdx := vp.history.Len() - 1
	h := vp.entryHeight(newIdx)
	vp.scrollOff = min(vp.scrollOff+h, vp.maxScrollOffset())
}

// Following reports whether the viewport is auto-following new content.
func (vp *Viewport) Following() bool {
	return vp.following
}

// View renders the visible portion of chat history into a string that
// fits within viewHeight lines.
func (vp *Viewport) View() string {
	total := vp.history.Len()
	if total == 0 || vp.viewHeight <= 0 || vp.viewWidth <= 0 {
		return ""
	}

	lines := vp.collectVisibleLines(total)
	lines = vp.applyEdgeFlash(lines)
	return vp.formatOutput(lines)
}

// applyEdgeFlash overlays a directional wrap indicator at the bottom-right
// corner of the viewport when an edge flash is active. The icon shows the
// direction the selection will wrap to on the next press.
func (vp *Viewport) applyEdgeFlash(lines []string) []string {
	if vp.edgeFlash == 0 || vp.edgeFlashTicks <= 0 || len(lines) == 0 {
		return lines
	}
	// edgeFlash > 0: at bottom, will wrap to top → show ↑.
	// edgeFlash < 0: at top, will wrap to bottom → show ↓.
	icon := theme.IconArrowUp
	if vp.edgeFlash < 0 {
		icon = theme.IconArrowDown
	}

	badge := vp.renderWrapBadge(icon)

	out := make([]string, len(lines))
	copy(out, lines)

	lastIdx := len(out) - 1
	out[lastIdx] = overlayRight(out[lastIdx], badge, vp.viewWidth, wrapBadgeWidth)
	return out
}

// renderWrapBadge renders the wrap indicator icon with appropriate styling.
// If the badge position overlaps a highlighted or selected region, the badge
// background matches that region's color for visual continuity.
func (vp *Viewport) renderWrapBadge(icon string) string {
	style := lipgloss.NewStyle().
		Foreground(vp.theme.Palette.Primary).
		Bold(true)
	if bg := vp.lastVisibleLineBg(); bg != "" {
		style = style.Background(bg)
	}
	return style.Render(" " + icon + " ")
}

// lastVisibleLineBg returns the highlight or selection background color
// applied to the last visible line in the viewport. Returns empty if the
// line has no active background.
func (vp *Viewport) lastVisibleLineBg() lipgloss.Color {
	total := vp.totalLines()
	lastAbs := max(total-vp.scrollOff-1, 0)
	entryIdx, lineInEntry := vp.entryAtAbsLine(lastAbs)
	if entryIdx < 0 {
		return ""
	}
	if bg := vp.copyHighlightBgAt(entryIdx, lineInEntry); bg != "" {
		return bg
	}
	return vp.selectionBgAt(entryIdx, lineInEntry)
}

// entryAtAbsLine returns the entry index and entry-relative line number
// for the given absolute line position. Returns (-1, 0) if not found.
func (vp *Viewport) entryAtAbsLine(absLine int) (int, int) {
	cumulative := 0
	for i := range vp.history.Len() {
		h := vp.entryHeight(i)
		if absLine < cumulative+h {
			return i, absLine - cumulative
		}
		cumulative += h
	}
	return -1, 0
}

// copyHighlightBgAt returns the copy-highlight background color if the
// given line falls within the active highlight range. Returns empty otherwise.
func (vp *Viewport) copyHighlightBgAt(entryIdx, lineInEntry int) lipgloss.Color {
	if vp.highlightID == "" {
		return ""
	}
	entry := vp.history.Get(entryIdx)
	if entry == nil || entry.ID != vp.highlightID {
		return ""
	}
	if lineInEntry < vp.highlightStart || lineInEntry >= vp.highlightEnd {
		return ""
	}
	return vp.theme.Palette.Highlight
}

// selectionBgAt returns the selection background color if the given line
// falls within the currently selected region. Returns empty otherwise.
func (vp *Viewport) selectionBgAt(entryIdx, lineInEntry int) lipgloss.Color {
	if vp.selectedIndex != entryIdx {
		return ""
	}
	regions := vp.regions(entryIdx)
	if vp.selectedRegion >= len(regions) {
		return ""
	}
	r := regions[vp.selectedRegion]
	if lineInEntry < r.start || lineInEntry >= r.end {
		return ""
	}
	return vp.theme.Palette.Selection
}

// overlayRight places a fixed-width badge at the right edge of a line,
// preserving visible content to the left. If the line's visible content
// extends into the badge zone, it is truncated to make room.
func overlayRight(line, badge string, viewWidth, badgeWidth int) string {
	targetCol := max(viewWidth-badgeWidth, 0)
	lineWidth := lipgloss.Width(line)
	if lineWidth <= targetCol {
		return line + strings.Repeat(" ", targetCol-lineWidth) + badge
	}
	return truncateVisible(line, targetCol) + badge
}

// truncateVisible truncates a string containing embedded ANSI escape
// sequences to the given number of visible columns. An ANSI reset is
// appended to close any open style sequences.
func truncateVisible(s string, maxCols int) string {
	runes := []rune(s)
	for i := range runes {
		if lipgloss.Width(string(runes[:i+1])) > maxCols {
			return string(runes[:i]) + ansiReset
		}
	}
	return s
}

// collectVisibleLines flattens all entry lines and extracts the window
// defined by scrollOff and viewHeight.
func (vp *Viewport) collectVisibleLines(total int) []string {
	all := vp.flattenLines(total)
	totalLines := len(all)

	end := max(totalLines-vp.scrollOff, 0)
	start := max(end-vp.viewHeight, 0)

	if end > totalLines {
		end = totalLines
	}

	visible := all[start:end]

	// Trim leading empty lines so messages align flush to the top.
	for len(visible) > 0 && visible[0] == "" {
		visible = visible[1:]
	}

	return visible
}

// flattenLines renders all entries and concatenates their lines.
// Rendering is cached per entry, so repeated calls are cheap.
func (vp *Viewport) flattenLines(total int) []string {
	var all []string
	for i := 0; i < total; i++ {
		all = append(all, vp.renderEntry(i)...)
	}
	return all
}

// renderEntry renders a single entry by logical index, using cached lines
// if available. If the entry matches highlightID, a background highlight
// is applied on top of the cached output.
func (vp *Viewport) renderEntry(index int) []string {
	entry := vp.history.Get(index)
	if entry == nil {
		return nil
	}

	var lines []string
	if entry.RenderedLines != nil && entry.Height >= 0 {
		lines = entry.RenderedLines
	} else {
		rendered, regions := RenderEntry(entry, vp.viewWidth, vp.theme)
		vp.cacheRendered(index, rendered, regions)
		lines = rendered
	}

	// Copy-highlight takes precedence over selection.
	if vp.highlightID != "" && entry.ID == vp.highlightID {
		return vp.applyHighlight(lines)
	}
	if vp.selectedIndex == index {
		return vp.applySelection(lines)
	}
	return lines
}

// SetHighlight sets the entry ID and line range to visually highlight.
// Pass empty id to clear.
func (vp *Viewport) SetHighlight(id string, start, end int) {
	vp.highlightID = id
	vp.highlightStart = start
	vp.highlightEnd = end
}

// ansiReset is the SGR full-reset sequence used by lipgloss/termenv.
const ansiReset = "\x1b[0m"

// applyHighlight applies a persistent background color to lines within
// [highlightStart, highlightEnd) of the entry's rendered output.
// Uses ANSI sequence patching to ensure the background survives inner
// style resets from syntax-highlighted code tokens.
func (vp *Viewport) applyHighlight(lines []string) []string {
	bgOpen := bgSequence(vp.theme.Palette.Highlight)
	result := make([]string, len(lines))
	for i, line := range lines {
		if i >= vp.highlightStart && i < vp.highlightEnd {
			result[i] = highlightLine(line, bgOpen, vp.viewWidth)
		} else {
			result[i] = line
		}
	}
	return result
}

// applySelection applies the selection background to lines within the
// currently selected region of an entry.
func (vp *Viewport) applySelection(lines []string) []string {
	regions := vp.regions(vp.selectedIndex)
	if vp.selectedRegion >= len(regions) {
		return lines
	}
	r := regions[vp.selectedRegion]
	bgOpen := bgSequence(vp.theme.Palette.Selection)
	result := make([]string, len(lines))
	for i, line := range lines {
		if i >= r.start && i < r.end {
			result[i] = highlightLine(line, bgOpen, vp.viewWidth)
		} else {
			result[i] = line
		}
	}
	return result
}

// highlightLine applies a background color to a single rendered line,
// patching inner ANSI resets so the background persists through styled content.
func highlightLine(line, bgOpen string, width int) string {
	if bgOpen == "" {
		return line
	}
	// Re-apply bg after every inner reset so it persists through styled tokens.
	patched := strings.ReplaceAll(line, ansiReset, ansiReset+bgOpen)
	pad := max(width-lipgloss.Width(line), 0)
	return bgOpen + patched + strings.Repeat(" ", pad) + ansiReset
}

// bgSequence returns the ANSI 24-bit background color SGR sequence for a
// hex color string. Returns empty if the color cannot be parsed.
func bgSequence(c lipgloss.Color) string {
	s := strings.TrimPrefix(string(c), "#")
	if len(s) != 6 {
		return ""
	}
	var r, g, b uint8
	if _, err := fmt.Sscanf(s, "%02x%02x%02x", &r, &g, &b); err != nil {
		return ""
	}
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
}

// CopyTarget describes the content and highlight range for a click-to-copy action.
type CopyTarget struct {
	EntryID        string
	EntryIndex     int    // Logical history index of the entry.
	Content        string // Raw text to copy (code block or full message).
	HighlightStart int    // Entry-relative first line to highlight (inclusive).
	HighlightEnd   int    // Entry-relative last line to highlight (exclusive).
}

// CopyTargetAtViewLine resolves a viewport-relative line to a CopyTarget.
// If the line falls on a code block, the target contains just that block's
// raw code. Otherwise it contains the full message content.
func (vp *Viewport) CopyTargetAtViewLine(y int) *CopyTarget {
	total := vp.history.Len()
	if total == 0 || y < 0 || y >= vp.viewHeight {
		return nil
	}

	totalLines := vp.totalLines()
	end := max(totalLines-vp.scrollOff, 0)
	start := max(end-vp.viewHeight, 0)
	absLine := start + y

	cumulative := 0
	for i := 0; i < total; i++ {
		h := vp.entryHeight(i)
		if absLine < cumulative+h {
			entry := vp.history.Get(i)
			if entry == nil {
				return nil
			}
			lineInEntry := absLine - cumulative
			target := vp.resolveCopyTarget(entry, h, lineInEntry)
			if target != nil {
				target.EntryIndex = i
			}
			return target
		}
		cumulative += h
	}
	return nil
}

// resolveCopyTarget checks whether lineInEntry falls within a code region.
// If so, returns the code block content and its line range.
// Otherwise returns the full message content and the prose line range
// (all content lines, skipping header and spacer).
func (vp *Viewport) resolveCopyTarget(entry *ChatEntry, entryHeight, lineInEntry int) *CopyTarget {
	for _, cr := range entry.CodeRegions {
		if lineInEntry >= cr.Start && lineInEntry < cr.End {
			return &CopyTarget{
				EntryID:        entry.ID,
				Content:        cr.Content,
				HighlightStart: cr.Start,
				HighlightEnd:   cr.End,
			}
		}
	}
	if entry.Content == "" {
		return nil
	}
	return &CopyTarget{
		EntryID:        entry.ID,
		Content:        entry.Content,
		HighlightStart: headerLines,
		HighlightEnd:   max(entryHeight-1, headerLines),
	}
}

// cacheRendered stores rendered lines and code regions back into the History entry.
func (vp *Viewport) cacheRendered(index int, lines []string, regions []CodeRegion) {
	vp.history.mu.Lock()
	defer vp.history.mu.Unlock()

	if index < 0 || index >= vp.history.count {
		return
	}
	physical := vp.history.logicalToPhysical(index)
	vp.history.entries[physical].RenderedLines = lines
	vp.history.entries[physical].CodeRegions = regions
	vp.history.entries[physical].Height = len(lines)
}

// SetBounceOffset updates the visual bounce displacement for rendering.
func (vp *Viewport) SetBounceOffset(offset int) {
	vp.bounceOffset = offset
}

// formatOutput applies the bounce offset, trims or pads the collected lines
// to exactly viewHeight, and joins them with newlines.
func (vp *Viewport) formatOutput(lines []string) string {
	lines = applyBounceShift(lines, vp.bounceOffset, vp.viewHeight)

	if len(lines) > vp.viewHeight {
		lines = lines[:vp.viewHeight]
	}

	// Pad with empty lines at the bottom if fewer than viewHeight.
	for len(lines) < vp.viewHeight {
		lines = append(lines, "")
	}

	return strings.Join(lines, "\n")
}

// applyBounceShift shifts the entire content block by the bounce offset,
// simulating iOS rubber-band overscroll.
// Positive offset (bottom boundary): content slides up as a whole block —
// top lines scroll off-screen, empty space appears at the bottom.
// Negative offset (top boundary): content slides down as a whole block —
// empty space appears at the top, bottom lines fall off-screen.
func applyBounceShift(lines []string, offset, viewHeight int) []string {
	if offset == 0 || viewHeight <= 0 {
		return lines
	}

	absOffset := offset
	if absOffset < 0 {
		absOffset = -absOffset
	}
	absOffset = min(absOffset, viewHeight)

	if offset > 0 {
		// Bouncing off bottom: content slides up. Top lines scroll off,
		// remaining content moves up, empty space opens at the bottom.
		shift := min(absOffset, len(lines))
		return lines[shift:]
	}

	// Bouncing off top: content slides down. Empty space opens at
	// the top, bottom lines fall off-screen (truncated by formatOutput).
	pad := make([]string, absOffset, absOffset+len(lines))
	return append(pad, lines...)
}

// totalLines returns the sum of all rendered entry heights.
func (vp *Viewport) totalLines() int {
	total := vp.history.Len()
	lines := 0
	for i := 0; i < total; i++ {
		lines += vp.entryHeight(i)
	}
	return lines
}

// maxScrollOffset returns the maximum line-based scroll offset.
// This is the total rendered lines minus one viewport, clamped to zero.
func (vp *Viewport) maxScrollOffset() int {
	return max(vp.totalLines()-vp.viewHeight, 0)
}

// EntryAtViewLine returns the chat entry visible at the given viewport-relative
// line (0 = top visible line). Returns nil if out of bounds.
func (vp *Viewport) EntryAtViewLine(y int) *ChatEntry {
	total := vp.history.Len()
	if total == 0 || y < 0 || y >= vp.viewHeight {
		return nil
	}

	totalLines := vp.totalLines()
	end := max(totalLines-vp.scrollOff, 0)
	start := max(end-vp.viewHeight, 0)
	absLine := start + y

	cumulative := 0
	for i := 0; i < total; i++ {
		h := vp.entryHeight(i)
		if absLine < cumulative+h {
			return vp.history.Get(i)
		}
		cumulative += h
	}
	return nil
}

// entryHeight returns the rendered line count for an entry, rendering it
// on demand if not already cached.
func (vp *Viewport) entryHeight(index int) int {
	entry := vp.history.Get(index)
	if entry == nil {
		return 0
	}
	if entry.Height >= 0 {
		return entry.Height
	}
	rendered, regions := RenderEntry(entry, vp.viewWidth, vp.theme)
	vp.cacheRendered(index, rendered, regions)
	return len(rendered)
}
