package window

import "sync"

// maxTabs caps the number of simultaneous tab pages. Derived from practical
// UI limits (tab bar width on typical terminals).
const maxTabs = 20

// TabPage represents a single tab, each owning an independent window manager.
type TabPage struct {
	ID      int
	Manager *Manager
	Label   string
}

// TabManager tracks all open tabs and the active tab.
type TabManager struct {
	mu     sync.RWMutex
	tabs   []*TabPage
	active int // index into tabs
	nextID int
}

// NewTabManager creates a tab manager with one initial tab viewing bufferID.
func NewTabManager(bufferID int, label string) *TabManager {
	mgr := NewManager(bufferID)
	tab := &TabPage{ID: 0, Manager: mgr, Label: label}
	return &TabManager{
		tabs:   []*TabPage{tab},
		active: 0,
		nextID: 1,
	}
}

// NewTab creates a new tab page with a fresh window manager viewing bufferID.
// Returns an error if the tab limit is reached.
func (tm *TabManager) NewTab(bufferID int, label string) (*TabPage, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if len(tm.tabs) >= maxTabs {
		return nil, tabError("tabnew: tab limit reached")
	}
	mgr := NewManager(bufferID)
	tab := &TabPage{ID: tm.nextID, Manager: mgr, Label: label}
	tm.nextID++
	// Insert after the active tab.
	insertIdx := tm.active + 1
	tm.tabs = insertTabAt(tm.tabs, insertIdx, tab)
	tm.active = insertIdx
	return tab, nil
}

// CloseTab removes the tab with the given ID. Returns an error if it is the
// last tab.
func (tm *TabManager) CloseTab(id int) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if len(tm.tabs) <= 1 {
		return tabError("tabclose: cannot close last tab")
	}
	idx := tm.findIndex(id)
	if idx < 0 {
		return tabError("tabclose: tab not found")
	}
	tm.tabs = removeTabAt(tm.tabs, idx)
	// Adjust active index.
	if tm.active >= len(tm.tabs) {
		tm.active = len(tm.tabs) - 1
	}
	return nil
}

// NextTab advances to the next tab, wrapping around (vim gt).
func (tm *TabManager) NextTab() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.active = (tm.active + 1) % len(tm.tabs)
}

// PrevTab moves to the previous tab, wrapping around (vim gT).
func (tm *TabManager) PrevTab() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.active = (tm.active - 1 + len(tm.tabs)) % len(tm.tabs)
}

// GoToTab switches to the N-th tab (1-indexed, like vim).
func (tm *TabManager) GoToTab(n int) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	idx := n - 1
	if idx < 0 || idx >= len(tm.tabs) {
		return
	}
	tm.active = idx
}

// ActiveTab returns the currently active tab page.
func (tm *TabManager) ActiveTab() *TabPage {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.tabs[tm.active]
}

// ActiveIndex returns the 0-based index of the active tab.
func (tm *TabManager) ActiveIndex() int {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.active
}

// AllTabs returns a snapshot of all tab pages.
func (tm *TabManager) AllTabs() []*TabPage {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	out := make([]*TabPage, len(tm.tabs))
	copy(out, tm.tabs)
	return out
}

// TabCount returns the number of open tabs.
func (tm *TabManager) TabCount() int {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return len(tm.tabs)
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// findIndex returns the index of the tab with the given ID, or -1.
// Caller must hold the lock.
func (tm *TabManager) findIndex(id int) int {
	for i, tab := range tm.tabs {
		if tab.ID == id {
			return i
		}
	}
	return -1
}

// insertTabAt inserts a tab at the specified index.
func insertTabAt(tabs []*TabPage, idx int, tab *TabPage) []*TabPage {
	result := make([]*TabPage, 0, len(tabs)+1)
	result = append(result, tabs[:idx]...)
	result = append(result, tab)
	result = append(result, tabs[idx:]...)
	return result
}

// removeTabAt removes the tab at the specified index.
func removeTabAt(tabs []*TabPage, idx int) []*TabPage {
	result := make([]*TabPage, 0, len(tabs)-1)
	result = append(result, tabs[:idx]...)
	result = append(result, tabs[idx+1:]...)
	return result
}

// tabError is a typed error for tab operations.
type tabError string

func (e tabError) Error() string { return string(e) }
