package compositor

import "strings"

// SlotID identifies a compositable section of the frame.
type SlotID int

const (
	SlotLeft       SlotID = iota + 1 // Sessions + Agents (or ring-cycled panel).
	SlotCenterLeft                   // FileTree (FourColumn only).
	SlotCenter                       // Chat.
	SlotRight                        // Code panel (editor/viewer).
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
	inputStart  int
	statusStart int

	// Dirty flags per section.
	mainDirty   bool
	inputDirty  bool
	statusDirty bool

	// Cached joined string.
	joined   string
	hasCache bool
}

// New creates an empty Compositor.
func New() Compositor {
	return Compositor{
		slotLines: make(map[SlotID][]string),
	}
}

// SetStructure configures the compositor for the current layout geometry.
// Called on resize and layout-mode changes. Resets all caches.
func (c *Compositor) SetStructure(colSlots []SlotID, mainH, inputH, statusH int) {
	totalH := mainH + inputH + statusH
	c.lines = make([]string, totalH)
	c.colSlots = colSlots
	c.mainH = mainH
	c.inputStart = mainH
	c.statusStart = mainH + inputH

	// Full invalidation.
	clear(c.slotLines)
	c.mainDirty = true
	c.inputDirty = true
	c.statusDirty = true
	c.hasCache = false
	c.joined = ""
}

// SetSlotLines stores new bordered output for a slot and marks the
// appropriate section dirty.
func (c *Compositor) SetSlotLines(id SlotID, lines []string) {
	c.slotLines[id] = lines
	switch id {
	case SlotInput:
		c.inputDirty = true
	case SlotStatus:
		c.statusDirty = true
	default:
		c.mainDirty = true
	}
	c.hasCache = false
}

// MarkDirty marks a specific slot's section as needing recomposition.
func (c *Compositor) MarkDirty(id SlotID) {
	switch id {
	case SlotInput:
		c.inputDirty = true
	case SlotStatus:
		c.statusDirty = true
	default:
		c.mainDirty = true
	}
	c.hasCache = false
}

// IsDirty returns whether a slot's section needs recomposition.
func (c *Compositor) IsDirty(id SlotID) bool {
	switch id {
	case SlotInput:
		return c.inputDirty
	case SlotStatus:
		return c.statusDirty
	default:
		return c.mainDirty
	}
}

// IsSlotCached returns true if the slot has been rendered at least once.
func (c *Compositor) IsSlotCached(id SlotID) bool {
	_, ok := c.slotLines[id]
	return ok
}

// InvalidateAll marks every section dirty (used on overlay transitions,
// edit-mode toggles, etc.).
func (c *Compositor) InvalidateAll() {
	c.mainDirty = true
	c.inputDirty = true
	c.statusDirty = true
	c.hasCache = false
}

// HasCache returns true if a fully composed frame string is available.
func (c *Compositor) HasCache() bool { return c.hasCache }

// CachedFrame returns the last composed frame string.
func (c *Compositor) CachedFrame() string { return c.joined }

// Compose rebuilds dirty sections of the frame and returns the joined string.
func (c *Compositor) Compose() string {
	if c.mainDirty {
		c.spliceMain()
		c.mainDirty = false
	}
	if c.inputDirty {
		c.spliceVertical(c.inputStart, SlotInput)
		c.inputDirty = false
	}
	if c.statusDirty {
		c.spliceVertical(c.statusStart, SlotStatus)
		c.statusDirty = false
	}
	c.joined = strings.Join(c.lines, "\n")
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

// SplitLines splits a rendered string into lines. Exported helper for
// callers that need to feed bordered output into SetSlotLines.
func SplitLines(s string) []string {
	return strings.Split(s, "\n")
}
