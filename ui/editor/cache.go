// Package editor — cache.go implements a tiered LRU cache for editor state.
//
// When users switch tabs, the active editor's per-file state (PieceTable,
// UndoTree, LineIndex) is extracted and stored in the cache. Recent tabs
// remain "warm" (live data structures, O(1) restore). Older tabs are
// demoted to "cold" (string content + UndoTree, rebuild on restore).
// Undo history is preserved across both tiers.
package editor

import (
	"container/list"

	"github.com/adalundhe/sylk/ui/editor/buffer"
)

// warmCapDefault is the maximum warm entries (live PieceTable + UndoTree).
// Derived from: 16 files x ~550 KB ≈ 8.8 MB resident; covers typical
// multi-file workflows where developers cycle among a handful of buffers.
const warmCapDefault = 16

// coldCapDefault is the maximum cold entries (string + UndoTree).
// Derived from: 48 files x ~200 KB ≈ 9.6 MB resident; allows broad
// history preservation across large workspaces.
const coldCapDefault = 48

// Tier identifies the cache tier for an entry.
type Tier int

const (
	// TierWarm holds live data structures: O(1) restore.
	TierWarm Tier = iota
	// TierCold holds string content + undo tree: rebuild required.
	TierCold
)

// WarmState holds live editor state extracted from Model via Detach().
// All pointer fields are transferred by reference — no copying.
type WarmState struct {
	FilePath        string
	Language        string
	Modified        bool
	Buf             *buffer.PieceTable
	LineIndex       *buffer.LineIndex
	UndoTree        *buffer.UndoTree
	Cursor          int
	CursorLine      int
	CursorCol       int
	ScrollOffset    int
	CursorPositions []int // Multi-cursor snapshot; nil means single cursor.
}

// ColdState holds serialized content plus the preserved undo tree.
// The PieceTable and LineIndex must be rebuilt from Content on restore,
// but undo history survives demotion intact.
type ColdState struct {
	FilePath        string
	Language        string
	Modified        bool
	Content         string
	UndoTree        *buffer.UndoTree
	Cursor          int
	CursorLine      int
	CursorCol       int
	ScrollOffset    int
	CursorPositions []int // Multi-cursor snapshot; nil means single cursor.
}

// cacheEntry wraps either a warm or cold state plus LRU bookkeeping.
type cacheEntry struct {
	key     string // file path
	tier    Tier
	warm    *WarmState // non-nil when tier == TierWarm
	cold    *ColdState // non-nil when tier == TierCold
	element *list.Element
}

// IsWarm reports whether the entry holds live data structures.
func (e *cacheEntry) IsWarm() bool { return e.tier == TierWarm }

// Warm returns the warm state. Caller must check IsWarm() first.
func (e *cacheEntry) Warm() *WarmState { return e.warm }

// Cold returns the cold state. Caller must check !IsWarm() first.
func (e *cacheEntry) Cold() *ColdState { return e.cold }

// CacheConfig holds the capacity parameters for the editor cache.
type CacheConfig struct {
	WarmCap int // Maximum warm entries. Zero uses warmCapDefault.
	ColdCap int // Maximum cold entries. Zero uses coldCapDefault.
}

// EditorCache implements a two-tier LRU cache for editor state.
//
// All operations are synchronous (BubbleTea single-threaded model).
//
// Invariants:
//   - warmCount <= warmCap (relaxed when all entries are modified)
//   - coldCount <= coldCap (relaxed when all entries are modified)
//   - Modified entries are never evicted.
//   - A key exists in at most one tier.
type EditorCache struct {
	warmCap int
	coldCap int

	entries map[string]*cacheEntry

	warmList *list.List // front = most recent warm entry
	coldList *list.List // front = most recent cold entry
}

// NewEditorCache creates a cache with the given configuration.
func NewEditorCache(cfg CacheConfig) *EditorCache {
	wc := cfg.WarmCap
	if wc <= 0 {
		wc = warmCapDefault
	}
	cc := cfg.ColdCap
	if cc <= 0 {
		cc = coldCapDefault
	}
	return &EditorCache{
		warmCap:  wc,
		coldCap:  cc,
		entries:  make(map[string]*cacheEntry),
		warmList: list.New(),
		coldList: list.New(),
	}
}

// Put stores a WarmState in the cache, promoting it to the front of the
// warm LRU. If the warm tier is full, the least-recently-used unmodified
// warm entry is demoted to cold. If the cold tier is also full, the
// least-recently-used unmodified cold entry is evicted.
func (c *EditorCache) Put(state *WarmState) {
	// Remove existing entry for this path (may be in either tier).
	c.deleteEntry(state.FilePath)

	entry := &cacheEntry{
		key:  state.FilePath,
		tier: TierWarm,
		warm: state,
	}
	entry.element = c.warmList.PushFront(entry)
	c.entries[state.FilePath] = entry

	c.demoteOldestWarm()
}

// Take removes the entry for path from the cache and returns it.
// Using Take (not Get) prevents shared-pointer mutation bugs: the
// caller takes ownership of the live PieceTable/UndoTree objects.
func (c *EditorCache) Take(path string) (*cacheEntry, bool) {
	entry, ok := c.entries[path]
	if !ok {
		return nil, false
	}
	c.unlinkEntry(entry)
	delete(c.entries, path)
	return entry, true
}

// Delete removes the entry for the given file path from the cache.
func (c *EditorCache) Delete(path string) {
	c.deleteEntry(path)
}

// IsModified reports whether the given file path has a cached entry
// with the Modified flag set. Returns false if not present.
func (c *EditorCache) IsModified(path string) bool {
	entry, ok := c.entries[path]
	if !ok {
		return false
	}
	if entry.tier == TierWarm {
		return entry.warm.Modified
	}
	return entry.cold.Modified
}

// Len returns the total number of entries across both tiers.
func (c *EditorCache) Len() int { return len(c.entries) }

// WarmCount returns the number of warm entries.
func (c *EditorCache) WarmCount() int { return c.warmList.Len() }

// ColdCount returns the number of cold entries.
func (c *EditorCache) ColdCount() int { return c.coldList.Len() }

// ---------------------------------------------------------------------------
// Internal
// ---------------------------------------------------------------------------

// deleteEntry removes the entry for path from the cache, freeing its
// LRU list element.
func (c *EditorCache) deleteEntry(path string) {
	entry, ok := c.entries[path]
	if !ok {
		return
	}
	c.unlinkEntry(entry)
	delete(c.entries, path)
}

// unlinkEntry removes an entry from its tier's LRU list.
func (c *EditorCache) unlinkEntry(entry *cacheEntry) {
	if entry.tier == TierWarm {
		c.warmList.Remove(entry.element)
	} else {
		c.coldList.Remove(entry.element)
	}
}

// demoteOldestWarm demotes the least-recently-used unmodified warm
// entry to cold, freeing its PieceTable and LineIndex. The UndoTree
// is transferred to the cold state intact.
func (c *EditorCache) demoteOldestWarm() {
	if c.warmList.Len() <= c.warmCap {
		return
	}
	// Walk from back (oldest) to front, skip modified.
	for el := c.warmList.Back(); el != nil; el = el.Prev() {
		entry := el.Value.(*cacheEntry)
		if entry.warm.Modified {
			continue
		}
		c.demoteEntry(entry)
		return
	}
	// All warm entries are modified — cap is temporarily exceeded.
}

// demoteEntry converts a warm entry to cold, materializing its content
// and preserving the undo tree.
func (c *EditorCache) demoteEntry(entry *cacheEntry) {
	ws := entry.warm
	entry.cold = &ColdState{
		FilePath:        ws.FilePath,
		Language:        ws.Language,
		Modified:        ws.Modified,
		Content:         ws.Buf.Content(),
		UndoTree:        ws.UndoTree,
		Cursor:          ws.Cursor,
		CursorLine:      ws.CursorLine,
		CursorCol:       ws.CursorCol,
		ScrollOffset:    ws.ScrollOffset,
		CursorPositions: ws.CursorPositions,
	}
	entry.warm = nil
	entry.tier = TierCold

	// Move from warm list to cold list front.
	c.warmList.Remove(entry.element)
	entry.element = c.coldList.PushFront(entry)

	c.evictOldestCold()
}

// evictOldestCold removes the least-recently-used unmodified cold entry.
func (c *EditorCache) evictOldestCold() {
	if c.coldList.Len() <= c.coldCap {
		return
	}
	for el := c.coldList.Back(); el != nil; el = el.Prev() {
		entry := el.Value.(*cacheEntry)
		if entry.cold.Modified {
			continue
		}
		c.coldList.Remove(el)
		delete(c.entries, entry.key)
		return
	}
	// All cold entries are modified — cap is temporarily exceeded.
}
