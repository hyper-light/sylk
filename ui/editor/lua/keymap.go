package lua

import (
	"errors"
	"sync"

	glua "github.com/yuin/gopher-lua"
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
	Rhs     any    // string (key-sequence) or *glua.LFunction (callback)
	Noremap bool
	Silent  bool
	Expr    bool
	Buffer  int    // 0 = global
	Desc    string
}

// keymapOptField maps an option-table key to a setter on KeymapEntry.
type keymapOptField struct {
	Key    string
	Apply  func(entry *KeymapEntry, val glua.LValue)
}

// keymapOptFields is the table-driven opts parser for vim.keymap.set.
var keymapOptFields = []keymapOptField{
	{Key: "noremap", Apply: func(e *KeymapEntry, v glua.LValue) { e.Noremap = luaToBool(v) }},
	{Key: "silent", Apply: func(e *KeymapEntry, v glua.LValue) { e.Silent = luaToBool(v) }},
	{Key: "expr", Apply: func(e *KeymapEntry, v glua.LValue) { e.Expr = luaToBool(v) }},
	{Key: "buffer", Apply: func(e *KeymapEntry, v glua.LValue) { e.Buffer = luaToInt(v) }},
	{Key: "desc", Apply: func(e *KeymapEntry, v glua.LValue) { e.Desc = v.String() }},
}

// luaToBool extracts a Go bool from a Lua value.
func luaToBool(v glua.LValue) bool {
	b, ok := v.(glua.LBool)
	if !ok {
		return false
	}
	return bool(b)
}

// luaToInt extracts a Go int from a Lua number value.
func luaToInt(v glua.LValue) int {
	n, ok := v.(glua.LNumber)
	if !ok {
		return 0
	}
	return int(n)
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
func registerKeymapTable(L *glua.LState, tbl *glua.LTable, rt *Runtime) {
	tbl.RawSetString("set", L.NewFunction(func(ls *glua.LState) int {
		return luaKeymapSet(ls, rt)
	}))
	tbl.RawSetString("del", L.NewFunction(func(ls *glua.LState) int {
		return luaKeymapDel(ls, rt)
	}))
}

// luaKeymapSet implements vim.keymap.set(mode, lhs, rhs, opts?).
func luaKeymapSet(L *glua.LState, rt *Runtime) int {
	mode := L.ToString(1)
	lhs := L.ToString(2)
	rhsVal := L.Get(3)

	entry := KeymapEntry{
		Mode:    mode,
		Lhs:     lhs,
		Noremap: true, // default like vim.keymap.set
	}

	// Rhs: string or LFunction.
	entry.Rhs = resolveKeymapRhs(rhsVal)

	// Parse optional opts table.
	if opts, ok := L.Get(4).(*glua.LTable); ok {
		applyKeymapOpts(&entry, opts)
	}

	_ = rt.Keymaps.Set(entry)
	return 0
}

// resolveKeymapRhs converts a Lua value to the Go rhs representation.
func resolveKeymapRhs(v glua.LValue) any {
	if fn, ok := v.(*glua.LFunction); ok {
		return fn
	}
	return v.String()
}

// applyKeymapOpts walks keymapOptFields and applies matching opts entries.
func applyKeymapOpts(entry *KeymapEntry, opts *glua.LTable) {
	for _, f := range keymapOptFields {
		val := opts.RawGetString(f.Key)
		if val != glua.LNil {
			f.Apply(entry, val)
		}
	}
}

// luaKeymapDel implements vim.keymap.del(mode, lhs, opts?).
func luaKeymapDel(L *glua.LState, rt *Runtime) int {
	mode := L.ToString(1)
	lhs := L.ToString(2)
	buffer := 0
	if opts, ok := L.Get(3).(*glua.LTable); ok {
		val := opts.RawGetString("buffer")
		buffer = luaToInt(val)
	}
	rt.Keymaps.Del(mode, lhs, buffer)
	return 0
}
