package layout

import "github.com/adalundhe/sylk/ui/component"

// FocusManager tracks the currently focused panel and provides tab-order
// cycling through focusable panels.
type FocusManager struct {
	current  component.FocusID
	tabOrder []component.FocusID
}

// NewFocusManager creates a FocusManager with the given tab order.
// The first entry in the order receives initial focus.
func NewFocusManager(order []component.FocusID) *FocusManager {
	fm := &FocusManager{
		tabOrder: make([]component.FocusID, len(order)),
	}

	copy(fm.tabOrder, order)

	if len(fm.tabOrder) > 0 {
		fm.current = fm.tabOrder[0]
	}

	return fm
}

// Current returns the FocusID of the currently focused panel.
func (fm *FocusManager) Current() component.FocusID {
	return fm.current
}

// IsFocused returns whether the given ID matches the current focus.
func (fm *FocusManager) IsFocused(id component.FocusID) bool {
	return fm.current == id
}

// SetFocus moves focus directly to the specified ID. The target does
// not need to be in the tab order — this allows spatial navigation
// and programmatic focus changes to reach panels (like preview)
// that are excluded from the Tab/Shift+Tab cycle.
func (fm *FocusManager) SetFocus(id component.FocusID) {
	fm.current = id
}

// Next advances focus to the next panel in the tab order, wrapping
// around to the first entry after the last.
func (fm *FocusManager) Next() {
	fm.current = fm.offset(1)
}

// Previous moves focus to the preceding panel in the tab order,
// wrapping around to the last entry before the first.
func (fm *FocusManager) Previous() {
	fm.current = fm.offset(-1)
}

// SetTabOrder replaces the tab order. If the previously focused ID is
// still present, focus is preserved; otherwise focus moves to the first
// entry in the new order.
func (fm *FocusManager) SetTabOrder(order []component.FocusID) {
	prev := fm.current
	fm.tabOrder = make([]component.FocusID, len(order))
	copy(fm.tabOrder, order)

	if fm.indexOf(prev) >= 0 {
		fm.current = prev
		return
	}

	if len(fm.tabOrder) > 0 {
		fm.current = fm.tabOrder[0]
	}
}

// TabOrder returns a copy of the current tab order.
func (fm *FocusManager) TabOrder() []component.FocusID {
	out := make([]component.FocusID, len(fm.tabOrder))
	copy(out, fm.tabOrder)

	return out
}

// offset returns the FocusID at the given signed offset from the
// current position, wrapping around the tab order. Returns the
// current ID if the tab order is empty.
func (fm *FocusManager) offset(delta int) component.FocusID {
	n := len(fm.tabOrder)

	if n == 0 {
		return fm.current
	}

	idx := fm.indexOf(fm.current)

	if idx < 0 {
		return fm.tabOrder[0]
	}

	// Go's modulo can return negative values for negative operands.
	// Adding n before the modulo ensures a positive result.
	next := ((idx + delta) % n + n) % n

	return fm.tabOrder[next]
}

// indexOf returns the position of id in the tab order, or -1 if absent.
func (fm *FocusManager) indexOf(id component.FocusID) int {
	for i, entry := range fm.tabOrder {
		if entry == id {
			return i
		}
	}

	return -1
}
