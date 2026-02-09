package lua

import (
	"errors"
	"sync"

	rt "github.com/arnodel/golua/runtime"
)

// maxKeymaps is the upper bound on the total number of keymap entries.
// Derived from a practical limit -- most editors carry fewer than 1024
// custom bindings even with heavy plugin usage.
const maxKeymaps = 1024

// errKeymapsFull is returned when the store has reached its capacity.
var errKeymapsFull = errors.New("keymap store full")

// ---------------------------------------------------------------------------
// KeymapEntry
// ---------------------------------------------------------------------------

// KeymapEntry describes a single mode-specific key mapping.
type KeymapEntry struct {
	Mode    string
	Lhs     string
	Rhs     any    // string (key-sequence) or rt.Value (callback)
	Noremap bool
	Silent  bool
	Expr    bool
	Buffer  int    // 0 = global
	Desc    string
}

// keymapOptField maps an option-table key to a setter on KeymapEntry.
type keymapOptField struct {
	Key   string
	Apply func(entry *KeymapEntry, val rt.Value)
}

// keymapOptFields is the table-driven opts parser for vim.keymap.set.
var keymapOptFields = []keymapOptField{
	{Key: "noremap", Apply: func(e *KeymapEntry, v rt.Value) { e.Noremap = luaToBool(v) }},
	{Key: "silent", Apply: func(e *KeymapEntry, v rt.Value) { e.Silent = luaToBool(v) }},
	{Key: "expr", Apply: func(e *KeymapEntry, v rt.Value) { e.Expr = luaToBool(v) }},
	{Key: "buffer", Apply: func(e *KeymapEntry, v rt.Value) { e.Buffer = luaToInt(v) }},
	{Key: "desc", Apply: func(e *KeymapEntry, v rt.Value) {
		if s, ok := v.TryString(); ok {
			e.Desc = s
		}
	}},
}

// ---------------------------------------------------------------------------
// KeymapStore
// ---------------------------------------------------------------------------

// keymapKey uniquely identifies a keymap binding.
type keymapKey struct {
	Mode   string
	Lhs    string
	Buffer int
}

// KeymapStore holds all active keymap entries with bounded capacity.
type KeymapStore struct {
	mu      sync.RWMutex
	entries map[keymapKey]KeymapEntry
}

// NewKeymapStore returns a ready-to-use store.
func NewKeymapStore() *KeymapStore {
	return &KeymapStore{
		entries: make(map[keymapKey]KeymapEntry, maxKeymaps),
	}
}

// Set adds or replaces a keymap entry.  Returns an error if the store
// is at capacity and the key is new.
func (ks *KeymapStore) Set(entry KeymapEntry) error {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	key := keymapKey{Mode: entry.Mode, Lhs: entry.Lhs, Buffer: entry.Buffer}
	_, exists := ks.entries[key]
	if !exists && len(ks.entries) >= maxKeymaps {
		return errKeymapsFull
	}
	ks.entries[key] = entry
	return nil
}

// Del removes a keymap entry.
func (ks *KeymapStore) Del(mode, lhs string, buffer int) {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	delete(ks.entries, keymapKey{Mode: mode, Lhs: lhs, Buffer: buffer})
}

// Get retrieves a keymap entry.
func (ks *KeymapStore) Get(mode, lhs string, buffer int) (*KeymapEntry, bool) {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	e, ok := ks.entries[keymapKey{Mode: mode, Lhs: lhs, Buffer: buffer}]
	if !ok {
		return nil, false
	}
	return &e, true
}

// AllMappings returns every entry matching the given mode.
func (ks *KeymapStore) AllMappings(mode string) []KeymapEntry {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	out := make([]KeymapEntry, 0, len(ks.entries))
	for _, e := range ks.entries {
		if e.Mode == mode {
			out = append(out, e)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Lua registration
// ---------------------------------------------------------------------------

// registerKeymapTable attaches vim.keymap.set and vim.keymap.del.
func registerKeymapTable(_ *rt.Runtime, tbl *rt.Table, luaRT *Runtime) {
	setGoFunc(tbl, "set", func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
		return luaKeymapSet(t, c, luaRT)
	}, 4, false)
	setGoFunc(tbl, "del", func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
		return luaKeymapDel(t, c, luaRT)
	}, 3, false)
}

// luaKeymapSet implements vim.keymap.set(mode, lhs, rhs, opts?).
func luaKeymapSet(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	mode, _ := c.StringArg(0)
	lhs, _ := c.StringArg(1)
	rhsVal := c.Arg(2)

	entry := KeymapEntry{
		Mode:    mode,
		Lhs:     lhs,
		Noremap: true, // default like vim.keymap.set
	}

	entry.Rhs = resolveKeymapRhs(rhsVal)

	// Parse optional opts table.
	optsVal := c.Arg(3)
	if opts, ok := optsVal.TryTable(); ok {
		applyKeymapOpts(&entry, opts)
	}

	_ = luaRT.Keymaps.Set(entry)
	return c.Next(), nil
}

// resolveKeymapRhs converts a Lua value to the Go rhs representation.
func resolveKeymapRhs(v rt.Value) any {
	if _, ok := v.TryCallable(); ok {
		return v
	}
	s, _ := v.TryString()
	return s
}

// applyKeymapOpts walks keymapOptFields and applies matching opts entries.
func applyKeymapOpts(entry *KeymapEntry, opts *rt.Table) {
	for _, f := range keymapOptFields {
		val := opts.Get(rt.StringValue(f.Key))
		if !val.IsNil() {
			f.Apply(entry, val)
		}
	}
}

// luaKeymapDel implements vim.keymap.del(mode, lhs, opts?).
func luaKeymapDel(t *rt.Thread, c *rt.GoCont, luaRT *Runtime) (rt.Cont, error) {
	mode, _ := c.StringArg(0)
	lhs, _ := c.StringArg(1)
	buffer := 0
	optsVal := c.Arg(2)
	if opts, ok := optsVal.TryTable(); ok {
		val := opts.Get(rt.StringValue("buffer"))
		buffer = luaToInt(val)
	}
	luaRT.Keymaps.Del(mode, lhs, buffer)
	return c.Next(), nil
}
