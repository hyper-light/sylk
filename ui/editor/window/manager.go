package window

import "sync"

// maxWindows is the practical upper bound on simultaneous splits, derived
// from terminal size constraints (a 16-way split on a typical terminal
// leaves each pane ~5 lines tall).
const maxWindows = 16

// SplitDirection indicates the axis of a window split.
type SplitDirection int

const (
	// Horizontal splits stack windows top-to-bottom.
	Horizontal SplitDirection = iota
	// Vertical splits stack windows left-to-right.
	Vertical
)

// NavigateDirection indicates focus movement.
type NavigateDirection int

const (
	NavLeft NavigateDirection = iota
	NavDown
	NavUp
	NavRight
)

// LayoutNode is a tree node that represents either a single window (leaf) or
// a container that splits its children in a given direction (branch).
type LayoutNode struct {
	Window    *Window        // non-nil for leaf nodes
	Direction SplitDirection // meaningful only for branch nodes
	Children  []*LayoutNode  // non-empty for branch nodes
}

// isLeaf reports whether this node holds a single window.
func (n *LayoutNode) isLeaf() bool {
	return n.Window != nil
}

// Manager owns the window layout tree and tracks the active window.
type Manager struct {
	mu       sync.RWMutex
	root     *LayoutNode
	active   int // window ID of the focused window
	nextID   int
	count    int
	width    int
	height   int
	allWindows []*Window // bounded flat cache for iteration
}

// NewManager creates a manager with a single root window viewing bufferID.
func NewManager(bufferID int) *Manager {
	w := NewWindow(0, bufferID)
	m := &Manager{
		root:       &LayoutNode{Window: w},
		active:     0,
		nextID:     1,
		count:      1,
		allWindows: make([]*Window, 0, maxWindows),
	}
	m.allWindows = append(m.allWindows, w)
	return m
}

// ActiveWindow returns the currently focused window.
func (m *Manager) ActiveWindow() *Window {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.findWindowByID(m.root, m.active)
}

// AllWindows returns a snapshot of all windows in the layout.
func (m *Manager) AllWindows() []*Window {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Window, len(m.allWindows))
	copy(out, m.allWindows)
	return out
}

// WindowCount returns the number of open windows.
func (m *Manager) WindowCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.count
}

// SplitHorizontal splits the active window horizontally (top/bottom).
func (m *Manager) SplitHorizontal() (*Window, error) {
	return m.split(Horizontal)
}

// SplitVertical splits the active window vertically (left/right).
func (m *Manager) SplitVertical() (*Window, error) {
	return m.split(Vertical)
}

// split performs the actual split operation.
func (m *Manager) split(dir SplitDirection) (*Window, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.count >= maxWindows {
		return nil, errWindowLimit
	}
	activeNode := m.findNodeByID(m.root, m.active)
	if activeNode == nil {
		return nil, errWindowNotFound
	}
	newWin := NewWindow(m.nextID, activeNode.Window.BufferID)
	newWin.CursorLine = activeNode.Window.CursorLine
	newWin.CursorCol = activeNode.Window.CursorCol
	newWin.ScrollOffset = activeNode.Window.ScrollOffset
	m.nextID++
	m.count++
	m.allWindows = append(m.allWindows, newWin)
	// Replace the active leaf with a branch containing both windows.
	existingWin := activeNode.Window
	activeNode.Window = nil
	activeNode.Direction = dir
	activeNode.Children = []*LayoutNode{
		{Window: existingWin},
		{Window: newWin},
	}
	m.active = newWin.ID
	m.recalcSizes(m.root, 0, 0, m.width, m.height)
	return newWin, nil
}

// Close removes the window with the given ID and rebalances the tree.
func (m *Manager) Close(id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.count <= 1 {
		return errLastWindow
	}
	removed := m.removeNode(m.root, nil, id)
	if !removed {
		return errWindowNotFound
	}
	m.count--
	m.rebuildCache()
	// If we closed the active window, activate the first remaining window.
	if m.active == id {
		m.active = m.allWindows[0].ID
	}
	m.recalcSizes(m.root, 0, 0, m.width, m.height)
	return nil
}

// Navigate moves focus in the given direction.
func (m *Manager) Navigate(dir NavigateDirection) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur := m.findWindowByID(m.root, m.active)
	if cur == nil {
		return
	}
	best := m.findNeighbor(cur, dir)
	if best != nil {
		m.active = best.ID
	}
}

// SetActive sets the active window by ID.
func (m *Manager) SetActive(id int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.findWindowByID(m.root, id) != nil {
		m.active = id
	}
}

// Resize updates the total available area and recalculates window sizes.
func (m *Manager) Resize(width, height int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.width = width
	m.height = height
	m.recalcSizes(m.root, 0, 0, width, height)
}

// ---------------------------------------------------------------------------
// Tree traversal helpers
// ---------------------------------------------------------------------------

// findWindowByID performs a depth-first search for a window by ID.
func (m *Manager) findWindowByID(node *LayoutNode, id int) *Window {
	if node.isLeaf() {
		if node.Window.ID == id {
			return node.Window
		}
		return nil
	}
	for _, child := range node.Children {
		if w := m.findWindowByID(child, id); w != nil {
			return w
		}
	}
	return nil
}

// findNodeByID finds the leaf node containing the window with the given ID.
func (m *Manager) findNodeByID(node *LayoutNode, id int) *LayoutNode {
	if node.isLeaf() {
		if node.Window.ID == id {
			return node
		}
		return nil
	}
	for _, child := range node.Children {
		if n := m.findNodeByID(child, id); n != nil {
			return n
		}
	}
	return nil
}

// removeNode removes a leaf with the given window ID, collapsing single-child
// branches. Returns true if a node was removed.
func (m *Manager) removeNode(node, parent *LayoutNode, id int) bool {
	if node.isLeaf() {
		return false // leaf removal is handled by the parent branch case
	}
	for i, child := range node.Children {
		if child.isLeaf() && child.Window.ID == id {
			node.Children = removeIndex(node.Children, i)
			m.collapseIfSingle(node, parent)
			return true
		}
		if m.removeNode(child, node, id) {
			return true
		}
	}
	return false
}

// collapseIfSingle replaces a branch with its sole remaining child.
func (m *Manager) collapseIfSingle(node, parent *LayoutNode) {
	if len(node.Children) != 1 {
		return
	}
	sole := node.Children[0]
	*node = *sole
}

// removeIndex removes element at idx from a LayoutNode slice.
func removeIndex(nodes []*LayoutNode, idx int) []*LayoutNode {
	result := make([]*LayoutNode, 0, len(nodes)-1)
	result = append(result, nodes[:idx]...)
	result = append(result, nodes[idx+1:]...)
	return result
}

// rebuildCache walks the tree and reconstructs the flat window list.
func (m *Manager) rebuildCache() {
	m.allWindows = m.allWindows[:0]
	m.collectWindows(m.root)
}

// collectWindows performs a depth-first traversal to gather all windows.
func (m *Manager) collectWindows(node *LayoutNode) {
	if node.isLeaf() {
		m.allWindows = append(m.allWindows, node.Window)
		return
	}
	for _, child := range node.Children {
		m.collectWindows(child)
	}
}

// recalcSizes distributes available space proportionally among children.
func (m *Manager) recalcSizes(node *LayoutNode, x, y, w, h int) {
	if node.isLeaf() {
		node.Window.SetSize(w, h)
		return
	}
	childCount := len(node.Children)
	if childCount == 0 {
		return
	}
	// Dispatch by direction without a package-level table to avoid init cycles.
	switch node.Direction {
	case Horizontal:
		m.distributeHorizontal(node.Children, x, y, w, h)
	case Vertical:
		m.distributeVertical(node.Children, x, y, w, h)
	}
}

func (m *Manager) distributeHorizontal(children []*LayoutNode, x, y, w, h int) {
	childCount := len(children)
	baseH := h / childCount
	remainder := h % childCount
	cy := y
	for i, child := range children {
		ch := baseH
		if i < remainder {
			ch++
		}
		m.recalcSizes(child, x, cy, w, ch)
		cy += ch
	}
}

func (m *Manager) distributeVertical(children []*LayoutNode, x, y, w, h int) {
	childCount := len(children)
	baseW := w / childCount
	remainder := w % childCount
	cx := x
	for i, child := range children {
		cw := baseW
		if i < remainder {
			cw++
		}
		m.recalcSizes(child, cx, y, cw, h)
		cx += cw
	}
}

// findNeighbor finds the best window to navigate to from the current window.
func (m *Manager) findNeighbor(cur *Window, dir NavigateDirection) *Window {
	var best *Window
	for _, w := range m.allWindows {
		if w.ID == cur.ID {
			continue
		}
		if !isInDirection(cur, w, dir) {
			continue
		}
		if best == nil || isCloser(cur, w, best, dir) {
			best = w
		}
	}
	return best
}

// navAxisEntry maps a direction to its axis test functions.
type navAxisEntry struct {
	dir     NavigateDirection
	inDir   func(cur, candidate *Window) bool
	closer  func(cur, a, b *Window) bool
}

// navAxisTable drives directional navigation.
var navAxisTable = []navAxisEntry{
	{dir: NavLeft, inDir: func(c, n *Window) bool { return n.CursorCol < c.CursorCol }, closer: func(_, a, b *Window) bool { return a.CursorCol > b.CursorCol }},
	{dir: NavRight, inDir: func(c, n *Window) bool { return n.CursorCol > c.CursorCol }, closer: func(_, a, b *Window) bool { return a.CursorCol < b.CursorCol }},
	{dir: NavUp, inDir: func(c, n *Window) bool { return n.CursorLine < c.CursorLine }, closer: func(_, a, b *Window) bool { return a.CursorLine > b.CursorLine }},
	{dir: NavDown, inDir: func(c, n *Window) bool { return n.CursorLine > c.CursorLine }, closer: func(_, a, b *Window) bool { return a.CursorLine < b.CursorLine }},
}

func isInDirection(cur, candidate *Window, dir NavigateDirection) bool {
	for _, entry := range navAxisTable {
		if entry.dir == dir {
			return entry.inDir(cur, candidate)
		}
	}
	return false
}

func isCloser(cur, a, b *Window, dir NavigateDirection) bool {
	for _, entry := range navAxisTable {
		if entry.dir == dir {
			return entry.closer(cur, a, b)
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Sentinel errors
// ---------------------------------------------------------------------------

// windowError is a typed error for window operations.
type windowError string

func (e windowError) Error() string { return string(e) }

const (
	errWindowLimit    windowError = "window: split limit reached"
	errWindowNotFound windowError = "window: not found"
	errLastWindow     windowError = "window: cannot close last window"
)
