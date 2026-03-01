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

	// Per-slot dirty flags (control which components re-render).
	slotDirty map[SlotID]bool

	// Cached joined string.
	joined   string
	hasCache bool
}

// New creates an empty Compositor.
func New() Compositor {
	return Compositor{
		slotLines: make(map[SlotID][]string),
		slotDirty: make(map[SlotID]bool),
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

// SetSlotLines stores new bordered output for a slot and marks the
// appropriate section dirty.
func (c *Compositor) SetSlotLines(id SlotID, lines []string) {
	c.slotLines[id] = lines
	c.slotDirty[id] = true
	switch id {
	case SlotQueue:
		c.queueDirty = true
	case SlotInput:
		c.inputDirty = true
	case SlotStatus:
		c.statusDirty = true
	default:
		c.mainDirty = true
	}
	c.hasCache = false
}

// MarkDirty marks a specific slot as needing re-rendering and its
// section as needing recomposition.
func (c *Compositor) MarkDirty(id SlotID) {
	c.slotDirty[id] = true
	switch id {
	case SlotQueue:
		c.queueDirty = true
	case SlotInput:
		c.inputDirty = true
	case SlotStatus:
		c.statusDirty = true
	default:
		c.mainDirty = true
	}
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

// Compose rebuilds dirty sections of the frame and returns the joined string.
// Skips the O(n) strings.Join when no section was actually modified.
func (c *Compositor) Compose() string {
	dirty := c.mainDirty || c.queueDirty || c.inputDirty || c.statusDirty
	if c.mainDirty {
		c.spliceMain()
		c.mainDirty = false
	}
	if c.queueDirty {
		c.spliceVertical(c.queueStart, SlotQueue)
		c.queueDirty = false
	}
	if c.inputDirty {
		c.spliceVertical(c.inputStart, SlotInput)
		c.inputDirty = false
	}
	if c.statusDirty {
		c.spliceVertical(c.statusStart, SlotStatus)
		c.statusDirty = false
	}
	// Clear per-slot dirty flags.
	clear(c.slotDirty)
	if dirty {
		c.joined = strings.Join(c.lines, "\n")
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
	sl := c.slotLines[id]
	for i, line := range sl {
		idx := start + i
		if idx < len(c.lines) {
			c.lines[idx] = line
		}
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

// ResizeVerticalQuick updates frame geometry for a height-only resize without
// marking any slots dirty or touching cached slot data. Sections are marked
// dirty so Compose recomposes from stale caches at new positions. Because no
// slot is dirty, the caller's renderDirtySlots pass is a no-op — zero panel
// re-rendering occurs. A deferred settle message should trigger a full
// SetStructure + re-render once resizing stops.
func (c *Compositor) ResizeVerticalQuick(mainH, queueH, inputH, statusH int) {
	// Slot caches are intentionally NOT truncated. slotLine returns "" for
	// rows beyond cache length, and spliceVertical is guarded by
	// idx < len(c.lines). On shrink, spliceMain reads only rows 0..mainH-1
	// from the (longer) cached data — the visible top rows stay byte-identical.
	// On grow, excess rows are empty strings filled on settle. This avoids
	// TruncateSlot's bottom-border jump that causes visible jitter.

	// Reallocate the frame line buffer and update section boundaries.
	c.lines = make([]string, mainH+queueH+inputH+statusH)
	c.mainH = mainH
	c.queueStart = mainH
	c.inputStart = mainH + queueH
	c.statusStart = mainH + queueH + inputH

	// Sections dirty for recomposition; slots NOT dirty (no re-rendering).
	c.mainDirty = true
	c.queueDirty = true
	c.inputDirty = true
	c.statusDirty = true

	c.hasCache = false
	c.joined = ""
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
