package compositor

import "strings"

// SlotID identifies a compositable section of the frame.
type SlotID int

const (
	SlotLeft       SlotID = iota + 1 // Sessions + Agents (or ring-cycled panel).
	SlotCenterLeft                   // FileTree (FourColumn only).
	SlotCenter                       // Chat.
	SlotRight                        // Code panel (editor/viewer).
	SlotQueue                        // Prompt queue strip (between main and input).
	SlotInput                        // Input bar.
	SlotStatus                       // Status bar.
)

// Compositor caches composed frame lines and re-splices only dirty sections.
// It eliminates redundant lipgloss.JoinHorizontal/JoinVertical calls when
// only a subset of panels changed.
type Compositor struct {
	// Composed frame (one string per terminal line).
	lines []string

	// Per-slot cached bordered output, split by \n.
	slotLines map[SlotID][]string

	// Column layout (main area).
	colSlots []SlotID // Left-to-right column slot IDs.
	mainH    int      // Line count for the main area columns.

	// Vertical section start indices in lines[].
	queueStart  int
	inputStart  int
	statusStart int

	// Section-level dirty flags (control spliceMain / spliceVertical).
	mainDirty   bool
	queueDirty  bool
	inputDirty  bool
	statusDirty bool

	// Row-level dirty tracking for incremental recomposition.
	mainDirtyRows   map[int]struct{}
	queueDirtyRows  map[int]struct{}
	inputDirtyRows  map[int]struct{}
	statusDirtyRows map[int]struct{}

	// Per-slot dirty flags (control which components re-render).
	slotDirty map[SlotID]bool

	// Cached joined string.
	joined   string
	hasCache bool

	frameVersion       uint64
	lastFrameDirtyRows []int
	lastFrameFull      bool
}

// FrameSnapshot captures the compositor's current lines plus metadata for the
// most recently composed frame. Lines are borrowed from the compositor and
// should not be retained without copying.
type FrameSnapshot struct {
	Lines     []string
	DirtyRows []int
	Version   uint64
	Full      bool
}

// New creates an empty Compositor.
func New() Compositor {
	return Compositor{
		slotLines:       make(map[SlotID][]string),
		slotDirty:       make(map[SlotID]bool),
		mainDirtyRows:   make(map[int]struct{}),
		queueDirtyRows:  make(map[int]struct{}),
		inputDirtyRows:  make(map[int]struct{}),
		statusDirtyRows: make(map[int]struct{}),
	}
}

// SetStructure configures the compositor for the current layout geometry.
// Called on resize and layout-mode changes. Resets all caches.
// queueH may be 0 when the prompt queue is empty (no space consumed).
func (c *Compositor) SetStructure(colSlots []SlotID, mainH, queueH, inputH, statusH int) {
	totalH := mainH + queueH + inputH + statusH
	c.lines = make([]string, totalH)
	c.colSlots = colSlots
	c.mainH = mainH
	c.queueStart = mainH
	c.inputStart = mainH + queueH
	c.statusStart = mainH + queueH + inputH

	// Full invalidation — section + per-slot.
	clear(c.slotLines)
	c.mainDirty = true
	c.queueDirty = true
	c.inputDirty = true
	c.statusDirty = true
	clear(c.mainDirtyRows)
	clear(c.queueDirtyRows)
	clear(c.inputDirtyRows)
	clear(c.statusDirtyRows)
	c.slotDirty = make(map[SlotID]bool, len(colSlots)+3)
	for _, id := range colSlots {
		c.slotDirty[id] = true
	}
	c.slotDirty[SlotQueue] = true
	c.slotDirty[SlotInput] = true
	c.slotDirty[SlotStatus] = true
	c.hasCache = false
	c.joined = ""
}

// SetSlotLines stores new bordered output for a slot and marks only the
// visible rows that actually changed dirty.
func (c *Compositor) SetSlotLines(id SlotID, lines []string) {
	prev := c.slotLines[id]
	c.slotLines[id] = lines
	c.slotDirty[id] = true
	c.markChangedRows(id, prev, lines)
	c.hasCache = false
}

// MarkDirty marks a specific slot as needing re-rendering.
func (c *Compositor) MarkDirty(id SlotID) {
	c.slotDirty[id] = true
	c.hasCache = false
}

// IsDirty returns whether a specific slot needs re-rendering.
func (c *Compositor) IsDirty(id SlotID) bool {
	return c.slotDirty[id]
}

// IsSlotCached returns true if the slot has been rendered at least once.
func (c *Compositor) IsSlotCached(id SlotID) bool {
	_, ok := c.slotLines[id]
	return ok
}

// InvalidateAll marks every section and slot dirty (used on overlay
// transitions, edit-mode toggles, etc.).
func (c *Compositor) InvalidateAll() {
	c.mainDirty = true
	c.queueDirty = true
	c.inputDirty = true
	c.statusDirty = true
	clear(c.mainDirtyRows)
	clear(c.queueDirtyRows)
	clear(c.inputDirtyRows)
	clear(c.statusDirtyRows)
	for _, id := range c.colSlots {
		c.slotDirty[id] = true
	}
	c.slotDirty[SlotQueue] = true
	c.slotDirty[SlotInput] = true
	c.slotDirty[SlotStatus] = true
	c.hasCache = false
}

// HasCache returns true if a fully composed frame string is available.
func (c *Compositor) HasCache() bool { return c.hasCache }

// CachedFrame returns the last composed frame string.
func (c *Compositor) CachedFrame() string { return c.joined }

// Snapshot returns the current frame lines plus metadata describing the most
// recent composition. DirtyRows are absolute row indices within Lines.
func (c *Compositor) Snapshot() FrameSnapshot {
	snapshot := FrameSnapshot{
		Lines:   c.lines,
		Version: c.frameVersion,
		Full:    c.lastFrameFull,
	}
	if len(c.lastFrameDirtyRows) > 0 {
		snapshot.DirtyRows = append([]int(nil), c.lastFrameDirtyRows...)
	}
	return snapshot
}

// Compose rebuilds dirty sections of the frame and returns the joined string.
// Skips the O(n) strings.Join when no section was actually modified.
func (c *Compositor) Compose() string {
	fullDirty := c.frameVersion == 0 || c.mainDirty || c.queueDirty || c.inputDirty || c.statusDirty
	dirtyRows := c.collectDirtyRows()
	dirty := fullDirty ||
		len(c.mainDirtyRows) > 0 || len(c.queueDirtyRows) > 0 ||
		len(c.inputDirtyRows) > 0 || len(c.statusDirtyRows) > 0
	if c.mainDirty {
		c.spliceMain()
		c.mainDirty = false
		clear(c.mainDirtyRows)
	} else if len(c.mainDirtyRows) > 0 {
		c.spliceMainRows(c.mainDirtyRows)
		clear(c.mainDirtyRows)
	}
	if c.queueDirty {
		c.spliceVertical(c.queueStart, SlotQueue)
		c.queueDirty = false
		clear(c.queueDirtyRows)
	} else if len(c.queueDirtyRows) > 0 {
		c.spliceVerticalRows(c.queueStart, SlotQueue, c.queueDirtyRows)
		clear(c.queueDirtyRows)
	}
	if c.inputDirty {
		c.spliceVertical(c.inputStart, SlotInput)
		c.inputDirty = false
		clear(c.inputDirtyRows)
	} else if len(c.inputDirtyRows) > 0 {
		c.spliceVerticalRows(c.inputStart, SlotInput, c.inputDirtyRows)
		clear(c.inputDirtyRows)
	}
	if c.statusDirty {
		c.spliceVertical(c.statusStart, SlotStatus)
		c.statusDirty = false
		clear(c.statusDirtyRows)
	} else if len(c.statusDirtyRows) > 0 {
		c.spliceVerticalRows(c.statusStart, SlotStatus, c.statusDirtyRows)
		clear(c.statusDirtyRows)
	}
	// Clear per-slot dirty flags.
	clear(c.slotDirty)
	if dirty {
		c.joined = strings.Join(c.lines, "\n")
		c.frameVersion++
		c.lastFrameFull = fullDirty
		if fullDirty {
			c.lastFrameDirtyRows = nil
		} else {
			c.lastFrameDirtyRows = dirtyRows
		}
	} else {
		c.lastFrameFull = false
		c.lastFrameDirtyRows = nil
	}
	c.hasCache = true
	return c.joined
}

// spliceMain recomposes all main-area rows from cached column slot lines.
func (c *Compositor) spliceMain() {
	for row := range c.mainH {
		c.lines[row] = c.spliceRow(row)
	}
}

// spliceMainRows recomposes only the requested main-area rows.
func (c *Compositor) spliceMainRows(rows map[int]struct{}) {
	for row := range rows {
		if row < 0 || row >= c.mainH {
			continue
		}
		c.lines[row] = c.spliceRow(row)
	}
}

// spliceRow concatenates one row across all column slots.
func (c *Compositor) spliceRow(row int) string {
	nCols := len(c.colSlots)
	if nCols == 1 {
		return c.slotLine(c.colSlots[0], row)
	}
	var b strings.Builder
	for _, slot := range c.colSlots {
		b.WriteString(c.slotLine(slot, row))
	}
	return b.String()
}

// slotLine returns a single cached line for a slot, or empty string
// if the slot is not yet cached or the row is out of bounds.
func (c *Compositor) slotLine(id SlotID, row int) string {
	sl := c.slotLines[id]
	if row < len(sl) {
		return sl[row]
	}
	return ""
}

// spliceVertical copies a vertical slot's cached lines into the frame
// starting at the given offset.
func (c *Compositor) spliceVertical(start int, id SlotID) {
	for i := 0; i < c.sectionHeight(id); i++ {
		idx := start + i
		if idx >= len(c.lines) {
			break
		}
		c.lines[idx] = c.slotLine(id, i)
	}
}

// spliceVerticalRows updates only the changed rows for a vertical slot.
func (c *Compositor) spliceVerticalRows(start int, id SlotID, rows map[int]struct{}) {
	for row := range rows {
		if row < 0 || row >= c.sectionHeight(id) {
			continue
		}
		idx := start + row
		if idx >= len(c.lines) {
			continue
		}
		c.lines[idx] = c.slotLine(id, row)
	}
}

// AdjustVerticalSections shifts the main/queue/input boundaries without full
// cache invalidation. The caller specifies which main-area slot contains the
// chat panel (layout-dependent); only that slot, the queue, and the input are
// marked dirty. Side panels keep their (truncated) cached output, avoiding
// the content shift that causes visible flicker. The lines[] slice is NOT
// reallocated because totalH (mainH + queueH + inputH + statusH) is
// invariant. statusDirty is set so the status section is re-spliced,
// preventing any trailing-newline overflow from corrupting the status bar row.
func (c *Compositor) AdjustVerticalSections(newMainH, newQueueH, newInputH int, chatSlot SlotID) {
	c.mainH = newMainH
	c.queueStart = newMainH
	c.inputStart = newMainH + newQueueH
	// statusStart = mainH + queueH + inputH is invariant — not updated.
	c.mainDirty = true
	c.queueDirty = true
	c.inputDirty = true
	c.statusDirty = true
	clear(c.mainDirtyRows)
	clear(c.queueDirtyRows)
	clear(c.inputDirtyRows)
	clear(c.statusDirtyRows)
	c.slotDirty[chatSlot] = true
	c.slotDirty[SlotQueue] = true
	c.slotDirty[SlotInput] = true
	c.hasCache = false
}

// AdjustInputSection shifts the main/input boundary without full cache
// invalidation. Convenience wrapper for layouts without an active queue strip.
func (c *Compositor) AdjustInputSection(newMainH, newInputH int, chatSlot SlotID) {
	c.AdjustVerticalSections(newMainH, 0, newInputH, chatSlot)
}

// TruncateSlot shortens a cached slot's output to maxLines while preserving
// the bottom border (last line). Content lines above the border are kept as-is,
// so the terminal diff sees no change for those rows. Used to keep side panels
// visually stable when the main area shrinks for input growth.
func (c *Compositor) TruncateSlot(id SlotID, maxLines int) {
	sl := c.slotLines[id]
	if len(sl) <= maxLines || maxLines < 2 {
		return
	}
	truncated := make([]string, maxLines)
	copy(truncated, sl[:maxLines-1])
	truncated[maxLines-1] = sl[len(sl)-1] // Preserve bottom border.
	c.slotLines[id] = truncated
}

// AllMainSlotsCached reports whether every column slot in the main area
// has been rendered at least once. Used to guard incremental updates that
// rely on cached side-panel output.
func (c *Compositor) AllMainSlotsCached() bool {
	for _, id := range c.colSlots {
		if _, ok := c.slotLines[id]; !ok {
			return false
		}
	}
	return true
}

// SplitLines splits a rendered string into lines. Exported helper for
// callers that need to feed bordered output into SetSlotLines.
func SplitLines(s string) []string {
	return strings.Split(s, "\n")
}

func (c *Compositor) markChangedRows(id SlotID, prev, next []string) {
	switch id {
	case SlotQueue:
		c.diffSlotRows(c.queueDirtyRows, prev, next, c.sectionHeight(SlotQueue))
	case SlotInput:
		c.diffSlotRows(c.inputDirtyRows, prev, next, c.sectionHeight(SlotInput))
	case SlotStatus:
		c.diffSlotRows(c.statusDirtyRows, prev, next, c.sectionHeight(SlotStatus))
	default:
		c.diffSlotRows(c.mainDirtyRows, prev, next, c.mainH)
	}
}

func (c *Compositor) diffSlotRows(dst map[int]struct{}, prev, next []string, visibleRows int) {
	for row := 0; row < visibleRows; row++ {
		if lineAt(prev, row) == lineAt(next, row) {
			continue
		}
		dst[row] = struct{}{}
	}
}

func (c *Compositor) sectionHeight(id SlotID) int {
	switch id {
	case SlotQueue:
		return max(c.inputStart-c.queueStart, 0)
	case SlotInput:
		return max(c.statusStart-c.inputStart, 0)
	case SlotStatus:
		return max(len(c.lines)-c.statusStart, 0)
	default:
		return c.mainH
	}
}

func lineAt(lines []string, row int) string {
	if row >= 0 && row < len(lines) {
		return lines[row]
	}
	return ""
}

func (c *Compositor) collectDirtyRows() []int {
	total := len(c.mainDirtyRows) + len(c.queueDirtyRows) + len(c.inputDirtyRows) + len(c.statusDirtyRows)
	if total == 0 {
		return nil
	}
	rows := make([]int, 0, total)
	for row := range c.mainDirtyRows {
		rows = append(rows, row)
	}
	for row := range c.queueDirtyRows {
		rows = append(rows, c.queueStart+row)
	}
	for row := range c.inputDirtyRows {
		rows = append(rows, c.inputStart+row)
	}
	for row := range c.statusDirtyRows {
		rows = append(rows, c.statusStart+row)
	}
	return rows
}
