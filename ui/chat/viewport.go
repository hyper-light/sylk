package chat

import (
	"strings"

	"github.com/adalundhe/sylk/ui/theme"
)

// scrollPageFraction is the fraction of viewHeight used for page scrolling.
// Derived from standard terminal pager behavior (full page minus one overlap line).
const scrollPageFraction = 1

// Viewport renders only visible chat entries using offset-based virtual scrolling.
// It references a History ring buffer and renders entries on demand, avoiding
// the need to pre-render all messages.
type Viewport struct {
	history    *History
	theme      *theme.Theme
	scrollOff  int  // Number of entries scrolled back from the bottom.
	viewHeight int
	viewWidth  int
	following  bool // Auto-scroll to bottom on new content.
}

// NewViewport creates a Viewport bound to the given History.
func NewViewport(history *History, th *theme.Theme) *Viewport {
	return &Viewport{
		history:   history,
		theme:     th,
		following: true,
	}
}

// SetSize updates the viewport dimensions.
func (vp *Viewport) SetSize(width, height int) {
	vp.viewWidth = max(width, 0)
	vp.viewHeight = max(height, 0)
}

// ScrollUp scrolls up by one entry.
func (vp *Viewport) ScrollUp() {
	vp.following = false
	maxOff := vp.maxScrollOffset()
	vp.scrollOff = min(vp.scrollOff+1, maxOff)
}

// ScrollDown scrolls down by one entry.
func (vp *Viewport) ScrollDown() {
	vp.scrollOff = max(vp.scrollOff-1, 0)
	vp.following = vp.scrollOff == 0
}

// PageUp scrolls up by approximately one page of entries.
func (vp *Viewport) PageUp() {
	vp.following = false
	step := max(vp.estimatePageEntries(), scrollPageFraction)
	maxOff := vp.maxScrollOffset()
	vp.scrollOff = min(vp.scrollOff+step, maxOff)
}

// PageDown scrolls down by approximately one page of entries.
func (vp *Viewport) PageDown() {
	step := max(vp.estimatePageEntries(), scrollPageFraction)
	vp.scrollOff = max(vp.scrollOff-step, 0)
	vp.following = vp.scrollOff == 0
}

// ToTop scrolls to the oldest visible entry.
func (vp *Viewport) ToTop() {
	vp.following = false
	vp.scrollOff = vp.maxScrollOffset()
}

// ToBottom scrolls to the newest entry and re-enables follow mode.
func (vp *Viewport) ToBottom() {
	vp.scrollOff = 0
	vp.following = true
}

// OnNewEntry should be called when a new entry is pushed to History.
// If in follow mode, scroll offset stays at zero (bottom).
// Otherwise, the offset is incremented so the user's view does not shift.
func (vp *Viewport) OnNewEntry() {
	if vp.following {
		return
	}
	maxOff := vp.maxScrollOffset()
	vp.scrollOff = min(vp.scrollOff+1, maxOff)
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
	return vp.formatOutput(lines)
}

// collectVisibleLines walks backwards from the bottom entry (accounting for
// scrollOff) and collects rendered lines until the viewport is full.
func (vp *Viewport) collectVisibleLines(total int) []string {
	// The bottom-most entry index (logical) is total-1-scrollOff.
	bottomIdx := total - 1 - min(vp.scrollOff, total-1)

	var collected []string
	for i := bottomIdx; i >= 0 && len(collected) < vp.viewHeight; i-- {
		entryLines := vp.renderEntry(i)
		// Prepend lines in reverse order.
		collected = prependLines(collected, entryLines, vp.viewHeight)
	}
	return collected
}

// renderEntry renders a single entry by logical index, using cached lines
// if available.
func (vp *Viewport) renderEntry(index int) []string {
	entry := vp.history.Get(index)
	if entry == nil {
		return nil
	}

	if entry.RenderedLines != nil && entry.Height >= 0 {
		return entry.RenderedLines
	}

	rendered := RenderEntry(entry, vp.viewWidth, vp.theme)
	vp.cacheRendered(index, rendered)
	return rendered
}

// cacheRendered stores rendered lines back into the History entry.
func (vp *Viewport) cacheRendered(index int, lines []string) {
	vp.history.mu.Lock()
	defer vp.history.mu.Unlock()

	if index < 0 || index >= vp.history.count {
		return
	}
	physical := vp.history.logicalToPhysical(index)
	vp.history.entries[physical].RenderedLines = lines
	vp.history.entries[physical].Height = len(lines)
}

// formatOutput trims or pads the collected lines to exactly viewHeight
// and joins them with newlines.
func (vp *Viewport) formatOutput(lines []string) string {
	// Take only the last viewHeight lines.
	if len(lines) > vp.viewHeight {
		lines = lines[len(lines)-vp.viewHeight:]
	}

	// Pad with empty lines if fewer than viewHeight.
	for len(lines) < vp.viewHeight {
		lines = append([]string{""}, lines...)
	}

	return strings.Join(lines, "\n")
}

// maxScrollOffset returns the maximum valid scroll offset.
func (vp *Viewport) maxScrollOffset() int {
	total := vp.history.Len()
	if total <= 0 {
		return 0
	}
	return total - 1
}

// estimatePageEntries estimates how many entries fit in one page.
// Uses a simple heuristic: viewHeight divided by average entry height.
// Falls back to viewHeight / 3 (assumes ~3 lines per entry on average).
const estimatedLinesPerEntry = 3

func (vp *Viewport) estimatePageEntries() int {
	if vp.viewHeight <= 0 {
		return 1
	}
	return max(vp.viewHeight/estimatedLinesPerEntry, 1)
}

// prependLines inserts src lines before dst, respecting a maximum total.
func prependLines(dst []string, src []string, maxTotal int) []string {
	remaining := maxTotal - len(dst)
	if remaining <= 0 {
		return dst
	}

	// If src exceeds remaining space, take only the tail.
	if len(src) > remaining {
		src = src[len(src)-remaining:]
	}

	result := make([]string, 0, len(src)+len(dst))
	result = append(result, src...)
	result = append(result, dst...)
	return result
}
