// Package window implements window management with splits, tabs, and
// per-window state for the editor.
package window

// maxLocalOptions caps the number of window-local options to prevent
// unbounded growth. Derived from the built-in window-scoped option count
// with headroom for user additions.
const maxLocalOptions = 64

// Window represents a single editor viewport bound to a buffer.
type Window struct {
	ID           int
	BufferID     int
	CursorLine   int
	CursorCol    int
	ScrollOffset int
	Width        int
	Height       int
	LocalOptions map[string]any
}

// NewWindow creates a window with the given ID and buffer association.
func NewWindow(id, bufferID int) *Window {
	return &Window{
		ID:           id,
		BufferID:     bufferID,
		LocalOptions: make(map[string]any, maxLocalOptions),
	}
}

// SetSize updates the window dimensions.
func (w *Window) SetSize(width, height int) {
	w.Width = max(width, 0)
	w.Height = max(height, 0)
}

// ScrollTo sets the scroll offset, clamping to non-negative values.
func (w *Window) ScrollTo(line int) {
	w.ScrollOffset = max(line, 0)
}

// EnsureCursorVisible adjusts the scroll offset so the cursor line is
// within the visible viewport.
func (w *Window) EnsureCursorVisible() {
	if w.Height <= 0 {
		return
	}
	if w.CursorLine < w.ScrollOffset {
		w.ScrollOffset = w.CursorLine
	}
	visibleEnd := w.ScrollOffset + w.Height - 1
	if w.CursorLine > visibleEnd {
		w.ScrollOffset = w.CursorLine - w.Height + 1
	}
}

// VisibleRange returns the inclusive start and end line numbers that are
// visible in the viewport. Both values are 0-indexed.
func (w *Window) VisibleRange() (startLine, endLine int) {
	startLine = w.ScrollOffset
	endLine = w.ScrollOffset + max(w.Height-1, 0)
	return startLine, endLine
}

// SetLocalOption sets a window-local option, respecting the capacity bound.
func (w *Window) SetLocalOption(name string, value any) {
	if _, exists := w.LocalOptions[name]; !exists && len(w.LocalOptions) >= maxLocalOptions {
		return
	}
	w.LocalOptions[name] = value
}

// GetLocalOption retrieves a window-local option value.
func (w *Window) GetLocalOption(name string) (any, bool) {
	v, ok := w.LocalOptions[name]
	return v, ok
}
