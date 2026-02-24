package chat

// codeBlockKey uniquely identifies a rendered code block by its
// language, raw content, and available width. All three fields
// participate in equality comparison via Go's struct ==.
type codeBlockKey struct {
	lang    string
	content string
	width   int
}

// codeBlockCacheEntry pairs a key with its rendered output.
type codeBlockCacheEntry struct {
	key   codeBlockKey
	lines []string
}

// codeBlockCache is a bounded LRU cache of rendered code block lines.
// Keyed by (lang, content, width). NOT thread-safe — only accessed
// from BubbleTea's single goroutine.
//
// Capacity is derived from: ~5 visible code blocks x 3 headroom = 16.
type codeBlockCache struct {
	entries  []codeBlockCacheEntry
	capacity int
}

// newCodeBlockCache creates a cache with the given maximum capacity.
func newCodeBlockCache(capacity int) *codeBlockCache {
	return &codeBlockCache{
		entries:  make([]codeBlockCacheEntry, 0, capacity),
		capacity: capacity,
	}
}

// Get returns the cached rendered lines for the given key, or nil on miss.
// On hit, the entry is moved to the front (most recently used).
func (c *codeBlockCache) Get(key codeBlockKey) []string {
	for i, e := range c.entries {
		if e.key == key {
			// Move to front.
			copy(c.entries[1:i+1], c.entries[:i])
			c.entries[0] = e
			return e.lines
		}
	}
	return nil
}

// Put stores rendered lines for the given key. If at capacity, the
// least recently used entry (tail) is evicted.
func (c *codeBlockCache) Put(key codeBlockKey, lines []string) {
	// Check for existing entry — update in place and move to front.
	for i, e := range c.entries {
		if e.key == key {
			c.entries[i].lines = lines
			copy(c.entries[1:i+1], c.entries[:i])
			c.entries[0] = codeBlockCacheEntry{key: key, lines: lines}
			return
		}
	}

	entry := codeBlockCacheEntry{key: key, lines: lines}

	if len(c.entries) >= c.capacity {
		// Evict LRU tail, shift everything right, prepend.
		copy(c.entries[1:], c.entries[:len(c.entries)-1])
		c.entries[0] = entry
		return
	}

	// Grow: prepend by shifting existing entries right.
	c.entries = append(c.entries, codeBlockCacheEntry{})
	copy(c.entries[1:], c.entries[:len(c.entries)-1])
	c.entries[0] = entry
}

// Clear removes all cached entries, retaining allocated capacity.
func (c *codeBlockCache) Clear() {
	c.entries = c.entries[:0]
}
