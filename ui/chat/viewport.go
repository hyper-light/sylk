package chat

import (
	"strings"

	"github.com/adalundhe/sylk/ui/theme"
)

// Viewport renders only visible chat entries using line-based virtual scrolling.
// It references a History ring buffer and renders entries on demand with caching.
// scrollOff tracks the number of rendered lines scrolled back from the bottom.
type Viewport struct {
	history    *History
	theme      *theme.Theme
	scrollOff  int  // Lines scrolled back from the bottom (0 = following).
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

// ScrollUp scrolls up by one line.
func (vp *Viewport) ScrollUp() {
	vp.following = false
	vp.scrollOff = min(vp.scrollOff+1, vp.maxScrollOffset())
}

// ScrollDown scrolls down by one line.
func (vp *Viewport) ScrollDown() {
	vp.scrollOff = max(vp.scrollOff-1, 0)
	vp.following = vp.scrollOff == 0
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

// ToTop scrolls to the oldest content.
func (vp *Viewport) ToTop() {
	vp.following = false
	vp.scrollOff = vp.maxScrollOffset()
}

// ToBottom scrolls to the newest content and re-enables follow mode.
func (vp *Viewport) ToBottom() {
	vp.scrollOff = 0
	vp.following = true
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
	return vp.formatOutput(lines)
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

	return all[start:end]
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
	if len(lines) > vp.viewHeight {
		lines = lines[:vp.viewHeight]
	}

	// Pad with empty lines at the bottom if fewer than viewHeight.
	for len(lines) < vp.viewHeight {
		lines = append(lines, "")
	}

	return strings.Join(lines, "\n")
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
	rendered := RenderEntry(entry, vp.viewWidth, vp.theme)
	vp.cacheRendered(index, rendered)
	return len(rendered)
}
